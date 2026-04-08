// Package teststack provides an in-process Tela test harness: a hub
// and an agent running side by side as goroutines, both bound to
// localhost on a random port, ready to be exercised by tests.
//
// Usage:
//
//	stack := teststack.New(t)
//	defer stack.Close()
//	stack.AddMachine("barn", []uint16{22})
//	stack.WaitAgentRegistered("barn", 5*time.Second)
//	// ... run assertions ...
//
// The harness is sequential: there is one hub per process and tests
// share the package-level state under internal/hub. Each test must
// call ResetForTesting via the Stack's Close so the next test starts
// from a clean slate. Test files can use t.Cleanup(stack.Close) or
// defer stack.Close() interchangeably.
//
// What the harness can do today:
//   - Spin up a hub on 127.0.0.1:0
//   - Spin up an agent that registers a configurable set of machines
//   - Wait for an agent to appear in the hub's /api/status response
//   - Tear everything down via context cancellation
//
// What it cannot do yet (deferred to a later commit):
//   - Open a real client tunnel and push bytes through. That requires
//     extracting cmd/tela in the same way and is bigger than the
//     harness shape itself.
package teststack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/paulmooreparks/tela/internal/agent"
	"github.com/paulmooreparks/tela/internal/client"
	"github.com/paulmooreparks/tela/internal/hub"
	"github.com/paulmooreparks/tela/internal/telelog"
)

// init runs once per test process to set up telelog. Production
// Main() does this; the harness skips Main() and calls Run() directly,
// so we have to do it ourselves. telelog uses sync.Once internally so
// repeat calls are safe -- the first init wins and the output stays
// pointed at the chosen sink for every subsequent test in the process.
//
// Set TELASTACK_LOGS=1 in the environment to route hub/agent/client
// log output to stderr while iterating on a test. Default is to
// discard so passing tests are quiet.
func init() {
	out := io.Discard
	if os.Getenv("TELASTACK_LOGS") != "" {
		out = os.Stderr
	}
	telelog.Init("test", out)
}

// Stack is one in-process hub + one in-process agent. Tests construct
// it via New(t), exercise it through the helper methods, and let
// t.Cleanup tear it down.
type Stack struct {
	t      *testing.T
	cancel context.CancelFunc
	wg     sync.WaitGroup

	tempDir   string
	hubAddr   string
	hubURL    string // ws://127.0.0.1:PORT
	hubHTTP   string // http://127.0.0.1:PORT
	agentYAML string

	machinesMu sync.Mutex
	machines   []machineSpec
}

// machineSpec is a queued addMachine call. Machines added before Run
// is called are written into the agent YAML up front; machines added
// after Run cause a re-write and a re-register (not yet implemented;
// for now, all machines must be added before the first call to a
// helper that requires the agent to be running).
type machineSpec struct {
	Name  string
	Ports []uint16
}

// New constructs a Stack with a hub listening on a random localhost
// port. The agent is not started until at least one machine has been
// added via AddMachine and a helper that requires the agent (such as
// WaitAgentRegistered) is called.
//
// The caller must arrange for Close to run, either via defer or
// t.Cleanup. Close cancels the context that drives both the hub and
// the agent and removes the temp directory.
func New(t *testing.T) *Stack {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "tela-stack-*")
	if err != nil {
		t.Fatalf("teststack: mkdir: %v", err)
	}

	s := &Stack{
		t:         t,
		tempDir:   tempDir,
		agentYAML: filepath.Join(tempDir, "telad.yaml"),
	}
	s.startHub()
	t.Cleanup(s.Close)
	return s
}

// startHub launches the hub goroutine on 127.0.0.1:0 and waits for it
// to report its actual bound address.
func (s *Stack) startHub() {
	s.t.Helper()

	// Reset hub package state from any previous test in the same
	// process so we always start from a clean slate.
	hub.ResetForTesting()

	// Disable auth in tests so we don't have to manage tokens. The
	// auth store unit tests cover the auth boundary; the harness is
	// for end-to-end behavior.
	hub.SetTestConfig(&hub.Config{
		Name: "teststack-hub",
	})

	// Tell the UDP relay to bind to a random port. Without this, two
	// tests in the same process collide on the default udpPort 41820.
	// applyHubConfig ignores zero values (production callers use zero
	// to mean "use the default"), so we set udpPort directly via the
	// dedicated test helper.
	hub.SetUDPPortForTesting(0)

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	addrCh := make(chan string, 1)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := hub.Run(ctx, "127.0.0.1:0", addrCh); err != nil && ctx.Err() == nil {
			s.t.Errorf("teststack: hub.Run: %v", err)
		}
	}()

	select {
	case addr := <-addrCh:
		s.hubAddr = addr
		s.hubURL = "ws://" + addr
		s.hubHTTP = "http://" + addr
	case <-time.After(5 * time.Second):
		s.t.Fatal("teststack: hub did not bind a port within 5s")
	}
}

// HubURL returns the ws:// URL the agent and clients can dial.
func (s *Stack) HubURL() string {
	return s.hubURL
}

// HubHTTP returns the http:// URL for /api/status and admin endpoints.
func (s *Stack) HubHTTP() string {
	return s.hubHTTP
}

// AddMachine queues a machine to be registered by the agent. Must be
// called before WaitAgentRegistered or other helpers that start the
// agent. Adding a machine after the agent is running is not yet
// supported.
func (s *Stack) AddMachine(name string, ports []uint16) {
	s.machinesMu.Lock()
	defer s.machinesMu.Unlock()
	s.machines = append(s.machines, machineSpec{Name: name, Ports: ports})
}

// startAgent writes the agent YAML, loads it, and runs the agent in a
// goroutine. Idempotent: a second call is a no-op.
func (s *Stack) startAgent() {
	s.t.Helper()

	if _, err := os.Stat(s.agentYAML); err == nil {
		// Already started.
		return
	}

	s.machinesMu.Lock()
	machines := s.machines
	s.machinesMu.Unlock()

	if len(machines) == 0 {
		s.t.Fatal("teststack: startAgent called with no machines; call AddMachine first")
	}

	// Build the YAML by hand. Keeps the harness independent of the
	// agent's YAML schema struct, which is unexported.
	var b []byte
	b = append(b, fmt.Sprintf("hub: %s\nmachines:\n", s.hubURL)...)
	for _, m := range machines {
		b = append(b, fmt.Sprintf("  - name: %s\n    ports: [", m.Name)...)
		for i, p := range m.Ports {
			if i > 0 {
				b = append(b, ',', ' ')
			}
			b = append(b, fmt.Sprintf("%d", p)...)
		}
		b = append(b, "]\n"...)
	}
	if err := os.WriteFile(s.agentYAML, b, 0600); err != nil {
		s.t.Fatalf("teststack: write agent yaml: %v", err)
	}

	// Reset agent package state for the same reason we reset hub state.
	agent.ResetForTesting()

	cfg, err := agent.Load(s.agentYAML)
	if err != nil {
		s.t.Fatalf("teststack: load agent config: %v", err)
	}
	agent.SetActiveConfig(cfg, s.agentYAML)

	ctx, cancel := context.WithCancel(context.Background())
	// Chain agent cancellation off the stack's main cancel function.
	prevCancel := s.cancel
	s.cancel = func() {
		cancel()
		prevCancel()
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := agent.Run(ctx, cfg); err != nil && ctx.Err() == nil {
			s.t.Errorf("teststack: agent.Run: %v", err)
		}
	}()
}

// WaitAgentRegistered blocks until the named machine appears in the
// hub's /api/status response, or until the timeout elapses. Calls
// startAgent on first use.
func (s *Stack) WaitAgentRegistered(machineID string, timeout time.Duration) {
	s.t.Helper()
	s.startAgent()

	deadline := time.Now().Add(timeout)
	url := s.hubHTTP + "/api/status"
	for time.Now().Before(deadline) {
		if s.statusContainsMachine(url, machineID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("teststack: agent %q did not register within %s", machineID, timeout)
}

// statusContainsMachine returns true if /api/status lists machineID.
func (s *Stack) statusContainsMachine(url, machineID string) bool {
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var payload struct {
		Machines []struct {
			ID string `json:"id"`
		} `json:"machines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false
	}
	for _, m := range payload.Machines {
		if m.ID == machineID {
			return true
		}
	}
	return false
}

// Connect spins up an in-process tela client that dials the harness's
// hub and forwards localPort -> agent's remotePort. Returns the local
// listen port the test should dial. The connection lives until the
// stack is closed; tests do not need to manage its lifetime.
//
// Use this in conjunction with StartLocalEcho or any other "real" TCP
// listener you set up at the agent target so that bytes written to
// localhost:<localPort> arrive at the listener through a real
// WireGuard tunnel through the in-process hub.
func (s *Stack) Connect(machineID string, localPort, remotePort uint16) {
	s.t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	prevCancel := s.cancel
	s.cancel = func() {
		cancel()
		if prevCancel != nil {
			prevCancel()
		}
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := client.Connect(ctx, client.ConnectOptions{
			HubURL:    s.hubURL,
			MachineID: machineID,
			Ports: []client.PortMapping{
				{LocalPort: localPort, RemotePort: remotePort},
			},
		})
		if err != nil && ctx.Err() == nil {
			s.t.Errorf("teststack: client.Connect: %v", err)
		}
	}()
}

// StartLocalEcho starts a TCP echo server bound to 127.0.0.1 on a
// random port and returns the port it picked. Used by tests as the
// "real" service the agent forwards to. The listener is closed when
// the stack is torn down.
func (s *Stack) StartLocalEcho() uint16 {
	s.t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatalf("teststack: start echo: %v", err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	prevCancel := s.cancel
	s.cancel = func() {
		listener.Close()
		if prevCancel != nil {
			prevCancel()
		}
	}
	return port
}

// MachineCount returns the number of machines currently registered
// with the hub. Useful for sanity checks in tests.
func (s *Stack) MachineCount() int {
	resp, err := http.Get(s.hubHTTP + "/api/status")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var payload struct {
		Machines []json.RawMessage `json:"machines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0
	}
	return len(payload.Machines)
}

// Close cancels the hub and agent contexts, waits for both goroutines
// to exit, and removes the temp directory. Safe to call multiple times.
func (s *Stack) Close() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.cancel = nil

	// Give the goroutines a bounded time to exit before we tear down.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.t.Logf("teststack: goroutines did not exit within 5s of Close (continuing teardown)")
	}

	// Clean up package-level state so the next test starts fresh.
	hub.ResetForTesting()
	agent.ResetForTesting()
	client.ResetForTesting()

	// Remove the temp directory. Best-effort; ignore errors so test
	// teardown does not mask the real test failure.
	_ = os.RemoveAll(s.tempDir)
}

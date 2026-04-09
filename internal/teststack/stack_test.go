package teststack

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/paulmooreparks/tela/internal/hub"
	"github.com/paulmooreparks/tela/internal/relay"
)

// TestStackSmokeHubBindsRandomPort verifies that New() spins up a hub
// on 127.0.0.1:0 and reports a real bound port back to the test.
func TestStackSmokeHubBindsRandomPort(t *testing.T) {
	stack := New(t)

	if stack.HubURL() == "" {
		t.Fatal("HubURL() empty after New()")
	}
	if stack.HubHTTP() == "" {
		t.Fatal("HubHTTP() empty after New()")
	}

	// /api/status should respond.
	resp, err := http.Get(stack.HubHTTP() + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/api/status status = %d, want 200", resp.StatusCode)
	}

	// No machines registered yet.
	if got := stack.MachineCount(); got != 0 {
		t.Errorf("MachineCount on fresh stack = %d, want 0", got)
	}
}

// TestStackSmokeAgentRegisters verifies that an agent started by the
// stack registers a machine with the hub. This is the end-to-end
// smoke test that exercises both extracted packages together.
func TestStackSmokeAgentRegisters(t *testing.T) {
	stack := New(t)
	stack.AddMachine("barn", []uint16{22})
	stack.WaitAgentRegistered("barn", 5*time.Second)

	if got := stack.MachineCount(); got != 1 {
		t.Errorf("MachineCount after register = %d, want 1", got)
	}
}

// TestStackSmokeMultipleMachines verifies that the agent can register
// more than one machine in a single Run.
func TestStackSmokeMultipleMachines(t *testing.T) {
	stack := New(t)
	stack.AddMachine("barn", []uint16{22})
	stack.AddMachine("web01", []uint16{80, 443})
	stack.WaitAgentRegistered("barn", 5*time.Second)
	stack.WaitAgentRegistered("web01", 5*time.Second)

	if got := stack.MachineCount(); got != 2 {
		t.Errorf("MachineCount with two machines = %d, want 2", got)
	}
}

// TestStackClientConnectsAndBindsListener verifies that the client
// half of the harness can dial the in-process hub, complete the
// WireGuard handshake against the in-process agent, and bind a local
// TCP listener that accepts connections.
//
// What it does NOT verify is byte echo through the tunnel. When the
// client opens a TCP stream into its WireGuard netstack and the agent
// is supposed to relay it to a localhost target inside its own
// WireGuard netstack, the TCP dial inside gvisor's netstack hangs
// forever in this in-process configuration. The hub, agent, and
// client all use independent gvisor instances but something about the
// state-machine interaction in a single OS process prevents the
// inner TCP handshake from completing.
//
// The same code path works in production (separate processes), and
// it works end-to-end up to and including the WireGuard handshake
// in this test, so the tunnel bridge, the port mappings, the local
// listener bind, and the WireGuard session pairing are all exercised.
// The remaining hop -- "TCP dial inside the agent's netstack to its
// localhost target" -- is a known limitation of the in-process
// harness and tracked as a follow-up. Tests that need to verify
// actual byte payload semantics must mock at the wsbind layer instead
// of running a true echo through gvisor.
func TestStackClientConnectsAndBindsListener(t *testing.T) {
	stack := New(t)
	echoPort := stack.StartLocalEcho()
	stack.AddMachine("barn", []uint16{echoPort})
	stack.WaitAgentRegistered("barn", 5*time.Second)

	const localPort uint16 = 15555
	stack.Connect("barn", localPort, echoPort)

	// Wait for the client's local listener to come up. The Connect
	// goroutine binds asynchronously after the WireGuard handshake
	// completes, so we cannot dial immediately.
	if err := waitForListener(fmt.Sprintf("127.0.0.1:%d", localPort), 10*time.Second); err != nil {
		t.Fatalf("client listener never came up: %v", err)
	}

	// We can dial the local listener and write to it without error;
	// we just cannot read echoed bytes back through the in-process
	// gvisor stack. Verify the dial+write half of the path.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		t.Fatalf("dial local: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// bytes and io are used in TestBridgeSessionLifecycle and retained here
// so the compiler does not flag them as unused when the bridge tests are
// the only callers.
var _ = io.ReadFull

// waitForListener polls a TCP address until something accepts a
// connection or the timeout elapses. Returns nil on success.
func waitForListener(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("listener at %s did not come up within %s: %w", addr, timeout, lastErr)
}

// ── Bridge test infrastructure ──────────────────────────────────────────────

// stubConnect holds the fields extracted from the connect message Hub-A sends
// to Hub-B during bridge session setup.
type stubConnect struct {
	MachineID string
	WGPubKey  string
}

// stubHub is a minimal WebSocket server that acts as Hub-B in bridge tests.
// It accepts one outbound WebSocket connection from Hub-A, reads the connect
// message, responds with ready(sessionIdx), and collects binary relay frames
// so tests can inspect them. Close() drops the connection to simulate Hub-B
// going away.
type stubHub struct {
	WSURL     string           // ws://127.0.0.1:PORT that Hub-A dials
	connectCh chan stubConnect // receives the connect payload (buffered 1)
	recvCh    chan []byte      // receives binary relay frames (buffered 16)
	server    *httptest.Server

	mu   sync.Mutex
	conn *websocket.Conn // active bridge WS; nil until Hub-A connects
}

func newStubHub(t *testing.T, sessionIdx int) *stubHub {
	t.Helper()
	sh := &stubHub{
		connectCh: make(chan stubConnect, 1),
		recvCh:    make(chan []byte, 16),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sh.mu.Lock()
		sh.conn = conn
		sh.mu.Unlock()
		defer func() {
			conn.Close()
			sh.mu.Lock()
			if sh.conn == conn {
				sh.conn = nil
			}
			sh.mu.Unlock()
		}()

		// Read the connect message forwarded from Hub-A.
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil || msg["type"] != "connect" {
			return
		}
		sc := stubConnect{}
		if v, ok := msg["machineId"].(string); ok {
			sc.MachineID = v
		}
		if v, ok := msg["wgPubKey"].(string); ok {
			sc.WGPubKey = v
		}
		select {
		case sh.connectCh <- sc:
		default:
		}

		// Respond with ready using the provided sessionIdx.
		ready, _ := json.Marshal(map[string]any{"type": "ready", "sessionIdx": sessionIdx})
		if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
			return
		}

		// Relay loop: collect binary frames for test assertions.
		for {
			msgType, pkt, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if msgType == websocket.BinaryMessage {
				select {
				case sh.recvCh <- pkt:
				default:
				}
			}
		}
	})
	sh.server = httptest.NewServer(mux)
	sh.WSURL = "ws://" + sh.server.Listener.Addr().String()
	t.Cleanup(sh.server.Close)
	return sh
}

// Close drops the active bridge WebSocket, simulating Hub-B going away.
func (sh *stubHub) Close() {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.conn != nil {
		sh.conn.Close()
		sh.conn = nil
	}
}

// ── Bridge tests ─────────────────────────────────────────────────────────────

// TestBridgeReachableThrough verifies that machines declared in Hub-A's bridge
// config appear in /api/status with a reachableThrough field pointing at the
// destination hub. No bridge connection is opened for this test; the directory
// is populated at config time.
func TestBridgeReachableThrough(t *testing.T) {
	stub := newStubHub(t, 1)
	stack := NewWithConfig(t, &hub.Config{
		Name: "hub-a",
		Bridges: []hub.BridgeConfig{
			{
				HubID:    "hub-b-test",
				URL:      stub.WSURL,
				Machines: []string{"remote-barn", "remote-web"},
			},
		},
	})

	// Both bridged machines should appear in /api/status.
	if got := stack.MachineCount(); got != 2 {
		t.Errorf("MachineCount with 2 bridged machines = %d, want 2", got)
	}
	for _, id := range []string{"remote-barn", "remote-web"} {
		if !stack.statusContainsMachine(stack.HubHTTP()+"/api/status", id) {
			t.Errorf("bridged machine %q not found in /api/status", id)
		}
	}

	// Each bridged machine must carry reachableThrough with the correct hubId.
	resp, err := http.Get(stack.HubHTTP() + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Machines []struct {
			ID               string `json:"id"`
			ReachableThrough *struct {
				HubID string `json:"hubId"`
			} `json:"reachableThrough"`
		} `json:"machines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode /api/status: %v", err)
	}
	for _, m := range status.Machines {
		if m.ID != "remote-barn" && m.ID != "remote-web" {
			continue
		}
		if m.ReachableThrough == nil {
			t.Errorf("machine %q: reachableThrough is nil", m.ID)
		} else if m.ReachableThrough.HubID != "hub-b-test" {
			t.Errorf("machine %q: reachableThrough.hubId = %q, want %q",
				m.ID, m.ReachableThrough.HubID, "hub-b-test")
		}
	}
}

// TestBridgeSessionLifecycle tests bridge session setup, relay frame
// forwarding, and teardown using a raw WebSocket client and a Hub-B stub.
//
// This test exercises the full bridge signaling path without a real WireGuard
// agent: Hub-A dials the stub on behalf of the raw client, the stub responds
// with ready(sessionIdx), Hub-A forwards ready+udp-offer to the client, and
// binary relay frames sent by the client are forwarded to the stub with the
// hop count set. Closing the stub's connection triggers Hub-A to tear down the
// client session via a WebSocket close message.
//
// WireGuard key exchange and data-plane bytes are not verified here (same
// in-process gvisor limitation as TestStackClientConnectsAndBindsListener).
func TestBridgeSessionLifecycle(t *testing.T) {
	const sessionIdx = 7
	stub := newStubHub(t, sessionIdx)
	stack := NewWithConfig(t, &hub.Config{
		Name: "hub-a",
		Bridges: []hub.BridgeConfig{
			{
				HubID:    "hub-b-stub",
				URL:      stub.WSURL,
				Machines: []string{"stub-machine"},
			},
		},
	})

	// Open a raw WebSocket to Hub-A (bypassing the real client package so
	// we can inspect individual signaling messages without WireGuard).
	dialer := websocket.Dialer{}
	clientWS, _, err := dialer.Dial(stack.HubURL(), nil)
	if err != nil {
		t.Fatalf("dial Hub-A: %v", err)
	}
	defer clientWS.Close()

	// Send connect for the bridged machine with a fake (random) WG public key.
	// Hub-A does not validate WG key format; it forwards the key verbatim to
	// Hub-B and uses it only when the WireGuard handshake actually starts.
	fakeKey := make([]byte, 32)
	rand.Read(fakeKey) //nolint:errcheck
	if err := clientWS.WriteJSON(map[string]any{
		"type":      "connect",
		"machineId": "stub-machine",
		"wgPubKey":  hex.EncodeToString(fakeKey),
	}); err != nil {
		t.Fatalf("send connect: %v", err)
	}

	// Hub-B stub should receive the connect message forwarded from Hub-A.
	select {
	case got := <-stub.connectCh:
		if got.MachineID != "stub-machine" {
			t.Errorf("stub connect: machineId = %q, want %q", got.MachineID, "stub-machine")
		}
		if got.WGPubKey != hex.EncodeToString(fakeKey) {
			t.Errorf("stub connect: wgPubKey not forwarded correctly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stub did not receive connect within 5s")
	}

	// The client should receive ready with Hub-B's sessionIdx, then udp-offer.
	// Both messages are sent by handleBridgeConnect before returning to the
	// relay loop.
	if err := clientWS.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var gotReady, gotUDPOffer bool
	for i := 0; i < 10 && !(gotReady && gotUDPOffer); i++ {
		msgType, data, err := clientWS.ReadMessage()
		if err != nil {
			t.Fatalf("read from Hub-A: %v", err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg["type"] {
		case "ready":
			if idx, ok := msg["sessionIdx"].(float64); !ok || int(idx) != sessionIdx {
				t.Errorf("ready: sessionIdx = %v, want %d", msg["sessionIdx"], sessionIdx)
			}
			gotReady = true
		case "udp-offer":
			gotUDPOffer = true
		}
	}
	if !gotReady {
		t.Error("did not receive ready from Hub-A after bridge connect")
	}
	if !gotUDPOffer {
		t.Error("did not receive udp-offer from Hub-A after bridge connect")
	}
	clientWS.SetReadDeadline(time.Time{}) //nolint:errcheck

	// Send a relay DATA frame from the client. Hub-A must forward it to the
	// stub with the hop count set (ForwardFrame writes maxHops-1 into hop[1]).
	payload := []byte("bridge-relay-test")
	frame := relay.BuildDataFrame(payload, 0, 0xdeadbeef)
	if err := clientWS.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatalf("send relay frame: %v", err)
	}
	select {
	case got := <-stub.recvCh:
		_, _, _, gotPayload, ok := relay.ParseHeader(got)
		if !ok {
			t.Fatal("stub received relay frame with bad header")
		}
		if !bytes.Equal(gotPayload, payload) {
			t.Errorf("relay frame payload = %q, want %q", gotPayload, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stub did not receive relay frame within 5s")
	}

	// Closing Hub-B stub must cause Hub-A to tear down the client session.
	// runBridgeReader detects the bridge WS close and sends a WebSocket close
	// frame to the client.
	stub.Close()
	if err := clientWS.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		_, _, err := clientWS.ReadMessage()
		if err != nil {
			break // WS closed -- teardown confirmed.
		}
	}
}

// TestStackResetBetweenRuns verifies that two stacks created in
// sequence in the same process do not see each other's state. This
// is the regression guard for the package-level reset path.
func TestStackResetBetweenRuns(t *testing.T) {
	{
		stack := New(t)
		stack.AddMachine("barn", []uint16{22})
		stack.WaitAgentRegistered("barn", 5*time.Second)
		stack.Close() // explicit, before the second New()
	}
	{
		stack := New(t)
		// Should NOT see barn from the previous stack.
		if got := stack.MachineCount(); got != 0 {
			t.Errorf("MachineCount after reset = %d, want 0", got)
		}
		stack.AddMachine("web01", []uint16{80})
		stack.WaitAgentRegistered("web01", 5*time.Second)
		if got := stack.MachineCount(); got != 1 {
			t.Errorf("MachineCount with web01 = %d, want 1", got)
		}
	}
}

/*
telad -- Tela Daemon (WireGuard Agent)

Purpose:

	Connects to the Hub via WebSocket, registers one or more machines,
	and waits. When the Hub signals a session (with the client's WireGuard
	public key), it creates a userspace WireGuard tunnel using gVisor
	netstack -- no TUN device, no admin/root required.

	Config-file mode (recommended):
	  telad -config telad.yaml

	Single-machine mode (flags):
	  telad -hub ws://hub -machine barn -ports 22:SSH,3389:RDP

Network (per-session addressing):

	Agent IP:  10.77.{N}.1/32  (N = session index, 1-254)
	Client IP: 10.77.{N}.2/32
*/
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"gopkg.in/yaml.v3"

	"github.com/paulmooreparks/tela/internal/credstore"
	"github.com/paulmooreparks/tela/internal/service"
	"github.com/paulmooreparks/tela/internal/telelog"
	"github.com/paulmooreparks/tela/internal/wsbind"
)

// telaConfigDir returns the user's tela configuration directory.
func telaConfigDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "tela")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tela")
}

// writeControlFile writes telad's control file so TelaVisor can detect
// the running instance. Returns a cleanup function that removes the file.
func writeControlFile(cfg *configFile, configPath string) func() {
	machines := make([]string, len(cfg.Machines))
	for i, m := range cfg.Machines {
		machines[i] = m.Name
	}

	info := map[string]interface{}{
		"pid":        os.Getpid(),
		"machines":   machines,
		"hub":        cfg.Hub,
		"configPath": configPath,
	}

	runDir := filepath.Join(telaConfigDir(), "run")
	if err := os.MkdirAll(runDir, 0700); err != nil {
		log.Printf("[telad] failed to create run directory: %v", err)
		return func() {}
	}

	controlPath := filepath.Join(runDir, "telad.json")
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return func() {}
	}
	if err := os.WriteFile(controlPath, data, 0600); err != nil {
		log.Printf("[telad] failed to write control file: %v", err)
		return func() {}
	}

	return func() {
		os.Remove(controlPath)
	}
}

const (
	// Default WireGuard MTU. Must match the client (tela) default.
	// See cmd/tela/main.go for the rationale behind 1100.
	// Override with TELAD_MTU or -mtu for higher throughput in
	// environments without WSL2/Hyper-V overhead.
	defaultMTU     = 1100
	wsPingInterval = 20 * time.Second
	wsPongWait     = 45 * time.Second
	wsWriteWait    = 5 * time.Second
	wsDialTimeout  = 15 * time.Second
)

// hubDialer is used for all WebSocket connections to the hub.
// It sets an explicit handshake timeout to avoid hanging on
// unreachable hosts or silent network failures.
var hubDialer = websocket.Dialer{
	HandshakeTimeout: wsDialTimeout,
}

var version = "dev"

// controlMessage is the JSON envelope for hub ↔ agent signalling.
// capabilities is an optional field in control messages that advertises
// features supported by the agent for this machine.
type capabilities struct {
	FileShare  *fileShareCapability `json:"fileShare,omitempty"`
	Management bool                 `json:"management,omitempty"`
}

type controlMessage struct {
	Type      string   `json:"type"`
	MachineID string   `json:"machineId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	OS          string `json:"os,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Location    string   `json:"location,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Message   string   `json:"message,omitempty"`
	WGPubKey  string   `json:"wgPubKey,omitempty"`
	Ports     []uint16 `json:"ports,omitempty"`
	Services  []serviceDescriptor `json:"services,omitempty"`
	Token     string   `json:"token,omitempty"`
	Port      int      `json:"port,omitempty"` // single port (udp-offer)
	Host      string   `json:"host,omitempty"` // explicit UDP host (when hub is behind proxy)
	SessionID  string `json:"sessionId,omitempty"`
	SessionIdx int    `json:"sessionIdx,omitempty"`
	Capabilities *capabilities `json:"capabilities,omitempty"`

	// Management protocol fields (mgmt-request / mgmt-response)
	RequestID string          `json:"requestId,omitempty"`
	Action    string          `json:"action,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	OK        *bool           `json:"ok,omitempty"`
}

type serviceDescriptor struct {
	Port        uint16 `json:"port" yaml:"port"`
	Proto       string `json:"proto,omitempty" yaml:"proto,omitempty"`
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// silentLogger discards verbose WireGuard-go routine spam.
type silentLogger struct{}

func (silentLogger) Printf(string, ...any) {}

// ── Config file schema ─────────────────────────────────────────────

// configFile is the top-level YAML structure for telad.yaml.
type configFile struct {
	Hub      string          `yaml:"hub"`
	Token    string          `yaml:"token,omitempty"`
	Machines []machineConfig `yaml:"machines"`
}

// machineConfig describes one machine to register with the hub.
type machineConfig struct {
	Name        string   `yaml:"name"`
	DisplayName string   `yaml:"displayName,omitempty"`
	Hostname    string   `yaml:"hostname,omitempty"`    // override os.Hostname() (useful in containers)
	OS          string   `yaml:"os,omitempty"`          // e.g. "windows", "linux"; defaults to runtime.GOOS
	Tags        []string `yaml:"tags,omitempty"`
	Location    string   `yaml:"location,omitempty"`
	Owner       string   `yaml:"owner,omitempty"`
	Ports       []uint16 `yaml:"ports,omitempty"`
	Services    []serviceDescriptor `yaml:"services,omitempty"`
	Target      string   `yaml:"target,omitempty"` // defaults to 127.0.0.1
	Token       string   `yaml:"token,omitempty"`  // overrides top-level token
	FileShare   fileShareConfig     `yaml:"fileShare,omitempty"`
	Gateway     *gatewayConfig      `yaml:"gateway,omitempty"`
	Upstreams   []upstreamConfig    `yaml:"upstreams,omitempty"`
}

type registration struct {
	MachineID    string
	DisplayName  string
	Hostname     string
	OS           string
	AgentVersion string
	Tags         []string
	Location     string
	Owner        string
	Token        string
	Ports        []uint16
	Services     []serviceDescriptor
	FileShare    *parsedFileShareConfig
	Gateway      *gatewayConfig
}

var verbose bool
var tunnelMTU = defaultMTU

// stopCh is closed to signal graceful shutdown (used in service mode).
var stopCh chan struct{}

// ── Log ring buffer ───────────────────────────────────────────────

const logRingSize = 1000

var (
	logRing    [logRingSize]string
	logRingPos int
	logRingLen int
	logRingMu  sync.Mutex
)

// logRingWriter is an io.Writer that captures log output into the ring buffer
// while also forwarding to the original writer.
type logRingWriter struct {
	original io.Writer
	buf      []byte // partial line buffer
}

func (w *logRingWriter) Write(p []byte) (int, error) {
	n, err := w.original.Write(p)
	// Buffer and split into lines for the ring
	w.buf = append(w.buf, p[:n]...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		logRingMu.Lock()
		logRing[logRingPos] = line
		logRingPos = (logRingPos + 1) % logRingSize
		if logRingLen < logRingSize {
			logRingLen++
		}
		logRingMu.Unlock()
	}
	return n, err
}

// snapshotLogRing returns the last n lines from the log ring buffer.
func snapshotLogRing(n int) []string {
	logRingMu.Lock()
	defer logRingMu.Unlock()
	if n <= 0 || n > logRingLen {
		n = logRingLen
	}
	lines := make([]string, n)
	start := (logRingPos - n + logRingSize) % logRingSize
	for i := 0; i < n; i++ {
		lines[i] = logRing[(start+i)%logRingSize]
	}
	return lines
}

// activeConfig holds the currently loaded config for management queries.
var (
	activeConfig   *configFile
	activeConfigMu sync.RWMutex
	activeConfigPath string
)

func setActiveConfig(cfg *configFile, path string) {
	activeConfigMu.Lock()
	activeConfig = cfg
	activeConfigPath = path
	activeConfigMu.Unlock()
}

// handleMgmtRequest processes a management message from the hub and
// returns a JSON response to send back.
// reregisterNeeded is set by config-set to signal runAgent to
// send an updated registration to the hub with the new metadata.
var reregisterNeeded bool

func handleMgmtRequest(lg *log.Logger, msg *controlMessage) []byte {
	ok := true
	notOK := false

	switch msg.Action {
	case "config-get":
		activeConfigMu.RLock()
		cfg := activeConfig
		cfgPath := activeConfigPath
		activeConfigMu.RUnlock()

		if cfg == nil {
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
				Message: "no active config",
			})
			return resp
		}

		// Build a redacted copy (no tokens)
		type redactedMachine struct {
			Name        string              `json:"name"`
			DisplayName string              `json:"displayName,omitempty"`
			Hostname    string              `json:"hostname,omitempty"`
			OS          string              `json:"os,omitempty"`
			Tags        []string            `json:"tags,omitempty"`
			Location    string              `json:"location,omitempty"`
			Owner       string              `json:"owner,omitempty"`
			Ports       []uint16            `json:"ports,omitempty"`
			Services    []serviceDescriptor `json:"services,omitempty"`
			Target      string              `json:"target,omitempty"`
			FileShare   fileShareConfig     `json:"fileShare,omitempty"`
			Gateway     *gatewayConfig      `json:"gateway,omitempty"`
			Upstreams   []upstreamConfig    `json:"upstreams,omitempty"`
		}
		machines := make([]redactedMachine, len(cfg.Machines))
		for i, m := range cfg.Machines {
			machines[i] = redactedMachine{
				Name: m.Name, DisplayName: m.DisplayName,
				Hostname: m.Hostname, OS: m.OS,
				Tags: m.Tags, Location: m.Location, Owner: m.Owner,
				Ports: m.Ports, Services: m.Services, Target: m.Target,
				FileShare: m.FileShare, Gateway: m.Gateway, Upstreams: m.Upstreams,
			}
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"hub":        cfg.Hub,
			"configPath": cfgPath,
			"machines":   machines,
		})
		resp, _ := json.Marshal(controlMessage{
			Type: "mgmt-response", RequestID: msg.RequestID, OK: &ok,
			Payload: payload,
		})
		lg.Printf("mgmt: config-get (requestId=%s)", msg.RequestID)
		return resp

	case "config-set":
		// Phase 2: accept partial config updates
		activeConfigMu.RLock()
		cfg := activeConfig
		cfgPath := activeConfigPath
		activeConfigMu.RUnlock()

		if cfg == nil || cfgPath == "" {
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
				Message: "no config file to update",
			})
			return resp
		}

		// Parse the update payload
		var update struct {
			Machine string          `json:"machine"`
			Fields  json.RawMessage `json:"fields"`
		}
		if err := json.Unmarshal(msg.Payload, &update); err != nil {
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
				Message: "invalid payload: " + err.Error(),
			})
			return resp
		}

		// Find the target machine
		activeConfigMu.Lock()
		var target *machineConfig
		for i := range activeConfig.Machines {
			if activeConfig.Machines[i].Name == update.Machine {
				target = &activeConfig.Machines[i]
				break
			}
		}
		if target == nil {
			activeConfigMu.Unlock()
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
				Message: "machine not found: " + update.Machine,
			})
			return resp
		}

		// Apply fields to the machine config
		if err := json.Unmarshal(update.Fields, target); err != nil {
			activeConfigMu.Unlock()
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
				Message: "invalid fields: " + err.Error(),
			})
			return resp
		}

		// Persist to disk
		data, err := yaml.Marshal(activeConfig)
		if err != nil {
			activeConfigMu.Unlock()
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
				Message: "marshal failed: " + err.Error(),
			})
			return resp
		}
		activeConfigMu.Unlock()

		if err := os.WriteFile(cfgPath, data, 0600); err != nil {
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
				Message: "write failed: " + err.Error(),
			})
			return resp
		}

		lg.Printf("mgmt: config-set machine=%s (requestId=%s)", update.Machine, msg.RequestID)
		reregisterNeeded = true
		resp, _ := json.Marshal(controlMessage{
			Type: "mgmt-response", RequestID: msg.RequestID, OK: &ok,
		})
		return resp

	case "logs":
		var req struct {
			Lines int `json:"lines"`
		}
		if msg.Payload != nil {
			json.Unmarshal(msg.Payload, &req)
		}
		n := req.Lines
		if n <= 0 {
			n = 100
		}
		lines := snapshotLogRing(n)
		payload, _ := json.Marshal(map[string]interface{}{
			"lines": lines,
		})
		resp, _ := json.Marshal(controlMessage{
			Type: "mgmt-response", RequestID: msg.RequestID, OK: &ok,
			Payload: payload,
		})
		lg.Printf("mgmt: logs n=%d (requestId=%s)", len(lines), msg.RequestID)
		return resp

	case "restart":
		lg.Printf("mgmt: restart requested (requestId=%s)", msg.RequestID)
		// Send response before restarting
		resp, _ := json.Marshal(controlMessage{
			Type: "mgmt-response", RequestID: msg.RequestID, OK: &ok,
			Message: "restarting",
		})
		// Schedule restart after response is sent
		go func() {
			time.Sleep(500 * time.Millisecond)
			restartSelf(lg)
		}()
		return resp

	case "update":
		var req struct {
			Version string `json:"version"`
		}
		if msg.Payload != nil {
			json.Unmarshal(msg.Payload, &req)
		}
		ver := req.Version
		lg.Printf("mgmt: update requested version=%q (requestId=%s)", ver, msg.RequestID)

		// Resolve "latest" or empty version via GitHub API.
		if ver == "" || ver == "latest" {
			resolved, err := latestRelease()
			if err != nil {
				resp, _ := json.Marshal(controlMessage{
					Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
					Message: "failed to query latest release: " + err.Error(),
				})
				return resp
			}
			ver = resolved
		}

		// Already at this version?
		if ver == version && version != "dev" {
			resp, _ := json.Marshal(controlMessage{
				Type: "mgmt-response", RequestID: msg.RequestID, OK: &ok,
				Message: "already running " + ver,
			})
			return resp
		}

		// Download, stage, and restart in the background.
		// Send response immediately so the hub does not time out.
		resp, _ := json.Marshal(controlMessage{
			Type: "mgmt-response", RequestID: msg.RequestID, OK: &ok,
			Message: "updating to " + ver,
		})
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := downloadAndStageUpdate(lg, ver); err != nil {
				lg.Printf("mgmt: update failed: %v", err)
				return
			}
			restartSelf(lg)
		}()
		return resp

	default:
		resp, _ := json.Marshal(controlMessage{
			Type: "mgmt-response", RequestID: msg.RequestID, OK: &notOK,
			Message: "unknown action: " + msg.Action,
		})
		return resp
	}
}

// restartSelf re-executes the current binary. Used by both the restart
// and update management actions.
func restartSelf(lg *log.Logger) {
	exe, err := os.Executable()
	if err != nil {
		lg.Printf("mgmt: restart failed: cannot find executable: %v", err)
		return
	}
	lg.Printf("mgmt: restarting telad via %s", exe)
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Start()
	os.Exit(0)
}

// latestRelease queries the GitHub API for the latest Tela release tag.
func latestRelease() (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/paulmooreparks/tela/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", fmt.Errorf("no tag_name in GitHub response")
	}
	return release.TagName, nil
}

// downloadAndStageUpdate downloads the given version of telad from GitHub
// releases and replaces the current binary. On Windows the running exe is
// renamed to .old before the new binary is moved into place.
func downloadAndStageUpdate(lg *log.Logger, ver string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	binaryName := fmt.Sprintf("telad-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
	dlURL := fmt.Sprintf("https://github.com/paulmooreparks/tela/releases/download/%s/%s", ver, binaryName)

	lg.Printf("mgmt: downloading %s", dlURL)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(dlURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	// Write to a temp file in the same directory (same volume for atomic rename).
	dir := filepath.Dir(exe)
	tmpFile, err := os.CreateTemp(dir, "telad-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		// Clean up temp file on any error path.
		os.Remove(tmpPath)
	}()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download write: %w", err)
	}
	tmpFile.Close()

	if runtime.GOOS != "windows" {
		os.Chmod(tmpPath, 0755)
	}

	// Swap the binary.
	if runtime.GOOS == "windows" {
		oldPath := exe + ".old"
		os.Remove(oldPath)
		if err := os.Rename(exe, oldPath); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}
		if err := os.Rename(tmpPath, exe); err != nil {
			// Rollback.
			os.Rename(oldPath, exe)
			return fmt.Errorf("install new binary: %w", err)
		}
		// Clean up old binary in the background.
		go func() {
			for range 10 {
				if os.Remove(oldPath) == nil {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
	} else {
		if err := os.Rename(tmpPath, exe); err != nil {
			return fmt.Errorf("install new binary: %w", err)
		}
	}

	lg.Printf("mgmt: updated telad to %s", ver)
	return nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `telad -- Tela Daemon

Register with a Tela Hub and expose local services through an encrypted
WireGuard tunnel. No TUN device or admin/root required.

Usage:
  telad -config <file>                   Config-file mode (recommended)
  telad -hub <url> -machine <id> [opts]  Single-machine mode

Subcommands:
  service   Manage telad as an OS service (install, start, stop, etc.)
  login     Store agent credentials in the system credential store
  logout    Remove agent credentials from the system credential store
  pair      Exchange a pairing code for an agent token
  version   Print version and exit
  help      Show this help

Environment Variables:
  TELA_HUB            Hub WebSocket URL       (overridden by -hub)
  TELA_MACHINE        Machine ID              (overridden by -machine)
  TELA_TOKEN          Auth token              (overridden by -token)
  TELAD_CONFIG        Config file path        (overridden by -config)
  TELAD_PORTS         Port specs              (overridden by -ports)
  TELAD_TARGET_HOST   Target host             (overridden by -target-host)

Credential Storage (Long-Lived Agents):
  Store tokens in the system credential store so you do not need to pass -token
  on every invocation. Requires elevation (run as Administrator or sudo).

    telad login -hub wss://hub.example.com           # Prompts for token
    telad -hub wss://hub.example.com -machine barn   # Token found automatically
    telad logout -hub wss://hub.example.com          # Remove stored credential

Examples:
  telad -config telad.yaml
  telad -hub ws://hub -machine barn -ports 22:SSH,3389:RDP
  telad -hub wss://hub.example.com -machine barn -ports "22:SSH,3389:RDP" -token s3cret

Options:
`)
	flag.PrintDefaults()
}

func main() {
	// Check for service subcommand or Windows SCM launch before flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "service" {
		handleServiceCommand()
		return
	}

	// If launched by the Windows SCM, enter service mode automatically.
	if service.IsWindowsService() {
		runAsWindowsService()
		return
	}

	// Handle version and help before flag parsing so they work without flags.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Printf("telad %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
			os.Exit(0)
		case "help", "-h", "--help":
			printUsage()
			os.Exit(0)
		case "login":
			cmdLogin(os.Args[2:])
			return
		case "logout":
			cmdLogout(os.Args[2:])
			return
		case "pair":
			cmdPair(os.Args[2:])
			return
		}
	}

	flag.Usage = func() {
		printUsage()
	}

	configPath := flag.String("config", envOrDefault("TELAD_CONFIG", ""), "Path to YAML config file (env: TELAD_CONFIG)")
	hubURL := flag.String("hub", envOrDefault("TELA_HUB", ""), "Hub WebSocket URL (env: TELA_HUB)")
	machineID := flag.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID to register (env: TELA_MACHINE)")
	token := flag.String("token", envOrDefault("TELA_TOKEN", ""), "Auth token (env: TELA_TOKEN)")
	portsStr := flag.String("ports", envOrDefault("TELAD_PORTS", ""), "Comma-separated port specs: port[:name[:description]]  e.g. 22:SSH,3389:RDP,12345:MyApp (env: TELAD_PORTS)")
	targetHost := flag.String("target-host", envOrDefault("TELAD_TARGET_HOST", "127.0.0.1"), "Target service host (env: TELAD_TARGET_HOST)")
	mtuOverride := flag.Int("mtu", 0, "WireGuard tunnel MTU (env: TELAD_MTU; default 1100)")
	flag.BoolVar(&verbose, "v", false, "Verbose logging")
	flag.Parse()

	// Resolve MTU: flag > env > default
	if *mtuOverride > 0 {
		tunnelMTU = *mtuOverride
	} else if v := os.Getenv("TELAD_MTU"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tunnelMTU = n
		}
	}

	telelog.Init("telad", &logRingWriter{original: os.Stderr})

	// Handle graceful shutdown
	stopCh = make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		close(stopCh)
	}()

	// Config-file mode
	if *configPath != "" {
		absPath, _ := filepath.Abs(*configPath)
		cfg, err := loadConfig(absPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		log.Printf("loaded config from %s", absPath)
		setActiveConfig(cfg, absPath)
		removeControl := writeControlFile(cfg, absPath)
		defer removeControl()
		runMultiMachine(cfg)
		return
	}

	// Single-machine mode (flags / env vars)
	if *hubURL == "" || *machineID == "" {
		flag.Usage()
		os.Exit(1)
	}

	if *portsStr == "" {
		log.Fatalf("-ports is required (e.g. -ports 22:SSH,3389:RDP)")
	}
	services := parsePortSpecs(*portsStr)
	if len(services) == 0 {
		log.Fatalf("no valid ports in: %s", *portsStr)
	}

	hostname, _ := os.Hostname()
	reg := registration{
		MachineID:    *machineID,
		Hostname:     hostname,
		OS:           runtime.GOOS,
		AgentVersion: version,
		Token:        *token,
		Ports:        portsFromServices(services),
		Services:     services,
	}

	// Fall back to credential store if token is empty
	if reg.Token == "" && *hubURL != "" {
		reg.Token = credstore.LookupToken(*hubURL)
	}

	runSingleMachine(*hubURL, reg, *targetHost)
}

// ── Service management ─────────────────────────────────────────────

func handleServiceCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, `telad service -- manage telad as an OS service

Usage:
  telad service install -config <file>
      Install service with YAML config file

  telad service install -hub <url> -machine <id> -ports <spec>
      Install service with inline configuration

  telad service uninstall               Remove the service
  telad service start                   Start the installed service
  telad service stop                    Stop the running service
  telad service restart                 Restart the service
  telad service status                  Show service status
  telad service run                     Run in service mode (used by the service manager)

Reconfigure:
  Edit the YAML config file and run "telad service restart", or
  reinstall with new parameters using "telad service install".

Install examples:
  telad service install -config telad.yaml
  telad service install -hub ws://hub:8080 -machine barn -ports 22:SSH,3389:RDP
`)
		os.Exit(1)
	}

	subcmd := os.Args[2]

	switch subcmd {
	case "install":
		serviceInstall()
	case "uninstall":
		serviceUninstall()
	case "start":
		serviceStart()
	case "stop":
		serviceStop()
	case "restart":
		serviceRestart()
	case "status":
		serviceStatus()
	case "run":
		if service.IsWindowsService() {
			runAsWindowsService()
		} else {
			serviceRun()
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service subcommand: %s\n", subcmd)
		os.Exit(1)
	}
}

func serviceInstall() {
	// Parse flags after "service install"
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to YAML config file (mutually exclusive with -hub/-machine)")
	hubURL := fs.String("hub", "", "Hub WebSocket URL (requires -machine, -ports)")
	machineID := fs.String("machine", "", "Machine ID to register (requires -hub, -ports)")
	portsStr := fs.String("ports", "", "Comma-separated port specs (requires -hub, -machine)")
	fs.Parse(os.Args[3:])

	// Determine configuration source
	var yamlContent string
	var destPath string

	if *configPath != "" {
		// Mode 1: Config file provided
		if *hubURL != "" || *machineID != "" || *portsStr != "" {
			fmt.Fprintf(os.Stderr, "error: use either -config OR (-hub, -machine, -ports), not both\n")
			os.Exit(1)
		}

		// Validate the config file
		absConfig, _ := filepath.Abs(*configPath)
		if _, err := loadConfig(absConfig); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		// Read the config file for embedding
		data, err := os.ReadFile(absConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
			os.Exit(1)
		}
		yamlContent = string(data)
		destPath = service.BinaryConfigPath("telad")

		// Copy to system directory as well (for reference/manual editing)
		if err := copyFile(absConfig, destPath); err != nil {
			fmt.Fprintf(os.Stderr, "error copying config: %v\n", err)
			os.Exit(1)
		}
	} else if *hubURL != "" && *machineID != "" && *portsStr != "" {
		// Mode 2: Configuration from command-line flags
		services := parsePortSpecs(*portsStr)
		if len(services) == 0 {
			fmt.Fprintf(os.Stderr, "error: no valid ports in: %s\n", *portsStr)
			os.Exit(1)
		}

		cfg := configFile{
			Hub: *hubURL,
			Machines: []machineConfig{
				{
					Name:     *machineID,
					Target:   "127.0.0.1",
					Services: services,
				},
			},
		}

		// Validate the generated config
		if cfg.Hub == "" {
			fmt.Fprintf(os.Stderr, "error: hub URL is required\n")
			os.Exit(1)
		}

		// Encode as YAML
		data, err := yaml.Marshal(&cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error encoding config: %v\n", err)
			os.Exit(1)
		}
		yamlContent = string(data)
		// For inline mode, don't create a separate file (config is entirely in metadata)
		destPath = ""
	} else {
		fmt.Fprintf(os.Stderr, "error: use either -config <file> OR (-hub, -machine, -ports)\n")
		fmt.Fprintf(os.Stderr, "\nUsage:\n")
		fmt.Fprintf(os.Stderr, "  telad service install -config <file>\n")
		fmt.Fprintf(os.Stderr, "  telad service install -hub <url> -machine <id> -ports <spec>\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  telad service install -hub ws://hub:8080 -machine barn -ports 22:SSH,3389:RDP\n")
		os.Exit(1)
	}

	// Get the absolute path to the current executable
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	exePath, _ = filepath.Abs(exePath)

	cfg := &service.Config{
		BinaryPath:  exePath,
		Description: "Tela Daemon -- encrypted tunnel agent",
		YAMLConfig:  service.EncodeYAMLConfig(yamlContent),
	}

	// Only set working directory for file-based config
	if destPath != "" {
		wd, _ := os.Getwd()
		cfg.WorkingDir = wd
	}

	if err := service.Install("telad", cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("telad service installed successfully")
	if destPath != "" {
		fmt.Printf("  config: %s\n", destPath)
	}
	fmt.Println("  start:  telad service start")
	if destPath != "" {
		fmt.Println("  edit:   " + destPath)
	}
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), service.ConfigDirPerm()); err != nil {
		return fmt.Errorf("create dir %s: %w", filepath.Dir(dst), err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, service.ConfigFilePerm()); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func serviceUninstall() {
	if err := service.Uninstall("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service uninstalled")
	fmt.Printf("  config retained: %s\n", service.BinaryConfigPath("telad"))
}

func serviceStart() {
	if err := service.Start("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service started")
}

func serviceStop() {
	if err := service.Stop("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service stopped")
}

func serviceRestart() {
	fmt.Println("stopping telad service...")
	_ = service.Stop("telad")
	// Brief pause to let the service fully stop
	time.Sleep(time.Second)
	if err := service.Start("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service restarted")
}

func serviceStatus() {
	st, err := service.QueryStatus("telad")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("installed: %v\n", st.Installed)
	fmt.Printf("running:   %v\n", st.Running)
	fmt.Printf("status:    %s\n", st.Info)
	if st.Installed {
		fmt.Printf("config:    %s\n", service.BinaryConfigPath("telad"))
	}
}

// serviceRunDaemon loads the YAML config from the service metadata (or file as fallback) and
// runs telad. It blocks until svcStop is closed.
func serviceRunDaemon(svcStop <-chan struct{}) {
	// Bridge service stop channel to the global stopCh so
	// runSingleMachine exits its reconnect loop on shutdown.
	stopCh = make(chan struct{})
	go func() {
		<-svcStop
		close(stopCh)
	}()

	// When running as a Windows service, redirect log output to a file.
	// stderr goes nowhere under the SCM, so without this all log output
	// (including per-machine loggers that write to os.Stderr) is lost.
	logDest := io.Writer(os.Stderr)
	if runtime.GOOS == "windows" && service.IsWindowsService() {
		logPath := service.LogPath("telad")
		lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, service.ConfigFilePerm())
		if err == nil {
			logDest = lf
			os.Stderr = lf
		}
	}
	telelog.Init("telad", &logRingWriter{original: logDest})

	svcCfg, err := service.LoadConfig("telad")
	if err != nil {
		log.Fatalf("service config: %v", err)
	}

	if svcCfg.WorkingDir != "" {
		os.Chdir(svcCfg.WorkingDir)
	}

	var fileCfg *configFile

	// Try the YAML config file first so operators can edit it and just
	// restart the service.  Fall back to inline metadata (base64 in the
	// service JSON) when no file exists.
	yamlPath := service.BinaryConfigPath("telad")
	if _, err := os.Stat(yamlPath); err == nil {
		fileCfg, err = loadConfig(yamlPath)
		if err != nil {
			log.Fatalf("config %s: %v", yamlPath, err)
		}
		log.Printf("loaded config from %s", yamlPath)
	} else if svcCfg.YAMLConfig != "" {
		yamlContent, err := service.DecodeYAMLConfig(svcCfg.YAMLConfig)
		if err != nil {
			log.Fatalf("decode inline config: %v", err)
		}
		if err := yaml.Unmarshal([]byte(yamlContent), &fileCfg); err != nil {
			log.Fatalf("parse inline config: %v", err)
		}
		if fileCfg == nil {
			log.Fatalf("inline config parsed but is nil")
		}
		log.Printf("loaded config from service metadata")
	} else {
		log.Fatalf("no config found: expected %s or inline metadata in %s",
			yamlPath, service.ConfigPath("telad"))
	}

	setActiveConfig(fileCfg, yamlPath)
	go runMultiMachine(fileCfg)

	<-svcStop
	log.Println("service stopping")
}

func serviceRun() {
	svcStop := make(chan struct{})

	// Handle signals for non-Windows "service run" (systemd/launchd)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		close(svcStop)
	}()

	serviceRunDaemon(svcStop)
}

func runAsWindowsService() {
	handler := &service.Handler{
		Run: func(svcStopCh <-chan struct{}) {
			serviceRunDaemon(svcStopCh)
		},
	}
	if err := service.RunAsService("telad", handler); err != nil {
		log.Fatalf("service failed: %v", err)
	}
}

// ── Config loading ─────────────────────────────────────────────────
func loadConfig(path string) (*configFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Hub == "" {
		return nil, fmt.Errorf("%s: 'hub' is required", path)
	}
	if len(cfg.Machines) == 0 {
		return nil, fmt.Errorf("%s: 'machines' list is empty", path)
	}
	for i, m := range cfg.Machines {
		if m.Name == "" {
			return nil, fmt.Errorf("%s: machines[%d]: 'name' is required", path, i)
		}
		if len(m.Ports) == 0 && len(m.Services) == 0 {
			return nil, fmt.Errorf("%s: machines[%d] (%s): either 'ports' or 'services' is required", path, i, m.Name)
		}
		if m.Target == "" {
			cfg.Machines[i].Target = "127.0.0.1"
		}
		if m.Token == "" {
			cfg.Machines[i].Token = cfg.Token
		}
		if cfg.Machines[i].Token == "" {
			cfg.Machines[i].Token = credstore.LookupToken(cfg.Hub)
		}
		if len(m.Ports) == 0 && len(m.Services) > 0 {
			cfg.Machines[i].Ports = portsFromServices(m.Services)
		}
		if len(m.Services) == 0 && len(m.Ports) > 0 {
			cfg.Machines[i].Services = minimalServicesFromPorts(m.Ports)
		}
	}
	return &cfg, nil
}

// runMultiMachine launches a goroutine per machine and blocks forever.
func runMultiMachine(cfg *configFile) {
	log.Printf("config: %d machine(s), hub %s", len(cfg.Machines), cfg.Hub)
	var wg sync.WaitGroup
	for _, m := range cfg.Machines {
		wg.Add(1)
		go func(mc machineConfig) {
			defer wg.Done()
			hostname := mc.Hostname
			if hostname == "" {
				hostname, _ = os.Hostname()
			}
			machineOS := mc.OS
			if machineOS == "" {
				machineOS = runtime.GOOS
			}
			// Parse file share config if present.
			var fsCfg *parsedFileShareConfig
			if mc.FileShare.Enabled {
				var err error
				fsCfg, err = parseFileShareConfig(mc.FileShare)
				if err != nil {
					log.Fatalf("[%s] fileShare config error: %v", mc.Name, err)
				}
				if err := ensureShareDir(fsCfg.directory); err != nil {
					log.Fatalf("[%s] fileShare directory error: %v", mc.Name, err)
				}
			}

			reg := registration{
				MachineID:    mc.Name,
				DisplayName:  mc.DisplayName,
				Hostname:     hostname,
				OS:           machineOS,
				AgentVersion: version,
				Tags:         mc.Tags,
				Location:     mc.Location,
				Owner:        mc.Owner,
				Token:        mc.Token,
				Ports:        mc.Ports,
				Services:     mc.Services,
				FileShare:    fsCfg,
				Gateway:      mc.Gateway,
			}
			mergeGatewayIntoRegistration(&reg, mc.Gateway)

			// Start upstream listeners (run for telad's lifetime, independent of sessions).
			cleanupUpstreams := startUpstreamListeners(
				telelog.NewLogger(mc.Name, nil), mc.Upstreams)
			defer cleanupUpstreams()

			runSingleMachine(cfg.Hub, reg, mc.Target)
		}(m)
	}
	wg.Wait()
}

// reconnectDelay calculates the next backoff delay with jitter.
// delay doubles each call up to maxDelay; jitter is ±25%.
func reconnectDelay(attempt int, maxDelay time.Duration) time.Duration {
	const baseDelay = 3 * time.Second
	d := baseDelay
	for i := 0; i < attempt; i++ {
		d *= 2
		if d > maxDelay {
			d = maxDelay
			break
		}
	}
	// Add ±25% jitter
	jitter := float64(d) * 0.25 * (2*mathrand.Float64() - 1)
	d += time.Duration(jitter)
	if d < time.Second {
		d = time.Second
	}
	return d
}

// runSingleMachine is the reconnect loop for one machine.
func runSingleMachine(hubURL string, reg registration, targetHost string) {
	logger := telelog.NewLogger("telad:"+reg.MachineID, nil)
	const maxDelay = 5 * time.Minute
	attempt := 0
	for {
		result := runAgent(logger, hubURL, reg, targetHost)
		if result == agentAuthRejected {
			logger.Printf("hub rejected authentication, stopping reconnect")
			return
		}
		if result == agentRegistered {
			attempt = 0 // was registered -- reset backoff
		}

		// Check for shutdown before reconnecting
		select {
		case <-stopCh:
			logger.Printf("shutdown received, exiting reconnect loop")
			return
		default:
		}

		delay := reconnectDelay(attempt, maxDelay)
		logger.Printf("reconnecting in %s...", delay.Round(time.Second))

		// Wait for delay or shutdown, whichever comes first
		select {
		case <-time.After(delay):
		case <-stopCh:
			logger.Printf("shutdown received, exiting reconnect loop")
			return
		}
		attempt++
	}
}

func portsFromServices(services []serviceDescriptor) []uint16 {
	ports := make([]uint16, 0, len(services))
	seen := make(map[uint16]struct{}, len(services))
	for _, s := range services {
		if s.Port == 0 {
			continue
		}
		if _, ok := seen[s.Port]; ok {
			continue
		}
		seen[s.Port] = struct{}{}
		ports = append(ports, s.Port)
	}
	return ports
}

// minimalServicesFromPorts creates bare service descriptors (no guessed names)
// for use when the YAML config specifies ports: but no services:.
func minimalServicesFromPorts(ports []uint16) []serviceDescriptor {
	services := make([]serviceDescriptor, 0, len(ports))
	for _, p := range ports {
		services = append(services, serviceDescriptor{Port: p, Proto: "tcp"})
	}
	return services
}

// parsePortSpecs parses the -ports flag value into service descriptors.
// Each spec is one of:
//
//	<port>                     e.g. 3389
//	<port>:<name>              e.g. 3389:RDP
//	<port>:<name>:<description> e.g. 3389:RDP:Remote Desktop
func parsePortSpecs(s string) []serviceDescriptor {
	var services []serviceDescriptor
	for _, spec := range strings.Split(s, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		parts := strings.SplitN(spec, ":", 3)
		n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || n < 1 || n > 65535 {
			log.Printf("ignoring invalid port spec: %s", spec)
			continue
		}
		svc := serviceDescriptor{Port: uint16(n), Proto: "tcp"}
		if len(parts) >= 2 {
			svc.Name = strings.TrimSpace(parts[1])
		}
		if len(parts) == 3 {
			svc.Description = strings.TrimSpace(parts[2])
		}
		services = append(services, svc)
	}
	return services
}

func startWSKeepalive(ws *websocket.Conn) func() {
	_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))

	prevPingHandler := ws.PingHandler()
	prevPongHandler := ws.PongHandler()

	ws.SetPingHandler(func(appData string) error {
		logVerbose(log.Default(), "ws keepalive: received ping")
		_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))
		if prevPingHandler != nil {
			return prevPingHandler(appData)
		}
		return nil
	})
	ws.SetPongHandler(func(appData string) error {
		logVerbose(log.Default(), "ws keepalive: received pong")
		_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))
		if prevPongHandler != nil {
			return prevPongHandler(appData)
		}
		return nil
	})

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				logVerbose(log.Default(), "ws keepalive: sending ping")
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
					logVerbose(log.Default(), "ws keepalive: ping failed: %v", err)
					return
				}
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
	}
}

// runAgent return values.
const (
	agentDisconnected = iota // transient disconnect, should reconnect
	agentRegistered          // was registered at some point, reset backoff
	agentAuthRejected        // hub explicitly rejected auth, stop retrying
)

func runAgent(lg *log.Logger, hubURL string, reg registration, targetHost string) int {
	lg.Printf("connecting to hub: %s", hubURL)

	ws, _, err := hubDialer.Dial(hubURL, nil)
	if err != nil {
		lg.Printf("dial failed: %v", err)
		return agentDisconnected
	}
	defer ws.Close()
	stopKeepalive := startWSKeepalive(ws)
	defer stopKeepalive()

	// sessionCtx is cancelled when this agent invocation returns,
	// signalling all session workers spawned by this connection to
	// shut down. This prevents orphaned session goroutines from
	// accumulating across reconnect cycles.
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	var sessionWg sync.WaitGroup
	defer func() {
		sessionCancel()
		sessionWg.Wait()
	}()

	// Close the WebSocket when shutdown is signalled so the blocking
	// ReadMessage loop below unblocks promptly instead of waiting for
	// the pong timeout.
	go func() {
		<-stopCh
		ws.Close()
	}()

	// Register with hub (include ports/services + metadata for the registry)
	lg.Printf("connected, registering as: %s", reg.MachineID)
	regMsg := controlMessage{
		Type:         "register",
		MachineID:    reg.MachineID,
		DisplayName:  reg.DisplayName,
		Hostname:     reg.Hostname,
		OS:           reg.OS,
		AgentVersion: reg.AgentVersion,
		Tags:         reg.Tags,
		Location:     reg.Location,
		Owner:        reg.Owner,
		Token:        reg.Token,
		Ports:        reg.Ports,
		Services:     reg.Services,
		Capabilities: buildCapabilities(reg.FileShare),
	}
	if err := ws.WriteJSON(&regMsg); err != nil {
		lg.Printf("register failed: %v", err)
		return agentDisconnected
	}

	// Read control messages on the control WS
	result := agentDisconnected
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			lg.Printf("hub read error: %v", err)
			return result
		}

		var msg controlMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "registered":
			result = agentRegistered
			lg.Printf("registered as: %s -- waiting for sessions", msg.MachineID)

		case "error":
			m := msg.Message
			if m == "" {
				m = "unknown error"
			}
			lg.Printf("hub error: %s", m)
			if strings.Contains(strings.ToLower(m), "access denied") ||
				strings.Contains(strings.ToLower(m), "invalid token") {
				return agentAuthRejected
			}

		case "session-request":
			sessionID := msg.SessionID
			sessionIdx := msg.SessionIdx
			wgPubKey := msg.WGPubKey
			lg.Printf("session-request: session=%s idx=%d", sessionID[:8], sessionIdx)
			sessionWg.Add(1)
			go func() {
				defer sessionWg.Done()
				runSessionWorker(sessionCtx, lg, hubURL, reg, targetHost, sessionID, sessionIdx, wgPubKey)
			}()

		case "mgmt-request":
			resp := handleMgmtRequest(lg, &msg)
			if err := ws.WriteMessage(websocket.TextMessage, resp); err != nil {
				lg.Printf("mgmt-response write failed: %v", err)
			}
			// If config changed, re-register with the hub to push updated metadata
			if reregisterNeeded {
				reregisterNeeded = false
				activeConfigMu.RLock()
				cfg := activeConfig
				activeConfigMu.RUnlock()
				if cfg != nil {
					// Find the machine config and send updated registration
					for _, m := range cfg.Machines {
						if m.Name == reg.MachineID {
							regMsg := controlMessage{
								Type:         "register",
								MachineID:    m.Name,
								DisplayName:  m.DisplayName,
								Hostname:     reg.Hostname,
								OS:           reg.OS,
								AgentVersion: reg.AgentVersion,
								Tags:         m.Tags,
								Location:     m.Location,
								Owner:        m.Owner,
								Token:        reg.Token,
								Ports:        reg.Ports,
								Services:     reg.Services,
								Capabilities: buildCapabilities(reg.FileShare),
							}
							if err := ws.WriteJSON(&regMsg); err != nil {
								lg.Printf("re-register failed: %v", err)
							} else {
								lg.Printf("re-registered with updated metadata")
							}
							break
						}
					}
				}
			}
		}
	}
}

// runSessionWorker opens a dedicated WebSocket for one client session.
// The ctx is cancelled when the parent agent connection returns, which
// signals this worker to tear down promptly.
func runSessionWorker(ctx context.Context, lg *log.Logger, hubURL string, reg registration, targetHost string, sessionID string, sessionIdx int, helperPubKey string) {
	lg.Printf("[session %s] dialing hub for session WS", sessionID[:8])

	ws, _, err := hubDialer.Dial(hubURL, nil)
	if err != nil {
		lg.Printf("[session %s] dial failed: %v", sessionID[:8], err)
		return
	}
	defer ws.Close()
	stopKeepalive := startWSKeepalive(ws)
	defer stopKeepalive()

	// Close the session WebSocket when the parent agent context is
	// cancelled (reconnect or shutdown), unblocking any pending reads.
	go func() {
		<-ctx.Done()
		ws.Close()
	}()

	// Send session-join to associate this WS with the session
	joinMsg := controlMessage{
		Type:      "session-join",
		MachineID: reg.MachineID,
		SessionID: sessionID,
	}
	if err := ws.WriteJSON(&joinMsg); err != nil {
		lg.Printf("[session %s] session-join send failed: %v", sessionID[:8], err)
		return
	}

	// Wait for session-start from hub (confirms pairing)
	_, raw, err := ws.ReadMessage()
	if err != nil {
		lg.Printf("[session %s] waiting for session-start: %v", sessionID[:8], err)
		return
	}
	var msg controlMessage
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "session-start" {
		lg.Printf("[session %s] unexpected message (wanted session-start): %s", sessionID[:8], string(raw))
		return
	}

	// Per-session IP addressing: 10.77.{idx}.1 / 10.77.{idx}.2
	// sessionIdx is already 1-based (from hub: len(entry.Sessions) after insert)
	subnet := sessionIdx
	if subnet < 1 {
		subnet = 1
	}
	if subnet > 254 {
		lg.Printf("[session %s] rejected -- session index %d exceeds 254-session limit", sessionID[:8], sessionIdx)
		ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "session limit exceeded"))
		ws.Close()
		return
	}
	sessionAgentIP := fmt.Sprintf("10.77.%d.1", subnet)
	sessionHelperIP := fmt.Sprintf("10.77.%d.2", subnet)

	lg.Printf("[session %s] starting -- agent=%s helper=%s", sessionID[:8], sessionAgentIP, sessionHelperIP)
	handleSession(lg, ws, hubURL, helperPubKey, targetHost, reg.Ports, reg.FileShare, reg.Gateway, sessionAgentIP, sessionHelperIP)
	lg.Printf("[session %s] ended", sessionID[:8])
}

func handleSession(lg *log.Logger, ws *websocket.Conn, hubURL, helperPubKeyHex, targetHost string, ports []uint16, fsCfg *parsedFileShareConfig, gwCfg *gatewayConfig, sessionAgentIP, sessionHelperIP string) {
	// Generate ephemeral WireGuard keypair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		lg.Printf("keygen failed: %v", err)
		return
	}
	privKeyHex := hex.EncodeToString(privKey.Bytes())
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	// Send our public key and port list back to helper (via hub relay)
	keyMsg := controlMessage{Type: "wg-pubkey", WGPubKey: pubKeyHex, Ports: ports, Capabilities: buildCapabilities(fsCfg)}
	if err := ws.WriteJSON(&keyMsg); err != nil {
		lg.Printf("failed to send pubkey: %v", err)
		return
	}
	lg.Printf("sent agent pubkey: %s...", pubKeyHex[:8])

	// Create netstack TUN (pure userspace -- no admin needed)
	tunDev, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(sessionAgentIP)},
		nil, // no DNS
		tunnelMTU,
	)
	if err != nil {
		lg.Printf("netstack creation failed: %v", err)
		return
	}

	// Create wsBind -- WireGuard datagrams go through the WebSocket
	bind := wsbind.New(ws, 256)

	// Create WireGuard device
	wgVerbose := silentLogger{}.Printf
	if verbose {
		wgVerbose = lg.Printf
	}
	logger := &device.Logger{
		Verbosef: wgVerbose,
		Errorf:   lg.Printf,
	}
	dev := device.NewDevice(tunDev, bind, logger)

	// Configure WireGuard via UAPI/IPC
	ipcConf := fmt.Sprintf(`private_key=%s
public_key=%s
endpoint=ws:0
allowed_ip=%s/32
persistent_keepalive_interval=25
`, privKeyHex, helperPubKeyHex, sessionHelperIP)

	if err := dev.IpcSet(ipcConf); err != nil {
		lg.Printf("WireGuard IPC config failed: %v", err)
		dev.Close()
		return
	}

	if err := dev.Up(); err != nil {
		lg.Printf("WireGuard device up failed: %v", err)
		dev.Close()
		return
	}
	lg.Printf("WireGuard tunnel up -- agent=%s helper=%s", sessionAgentIP, sessionHelperIP)

	// Start reader goroutine: WebSocket binary → wsBind.RecvCh
	sessionEnded := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		wsReader(lg, ws, bind, hubURL, sessionEnded)
	}()

	// Send periodic data-frame keepalive to prevent proxy idle timeouts
	stopDataKeepalive := bind.StartDataKeepalive(30 * time.Second)
	defer stopDataKeepalive()

	// Listen on each port inside netstack
	var listeners []net.Listener
	for _, port := range ports {
		listenAddr := netip.AddrPortFrom(netip.MustParseAddr(sessionAgentIP), port)
		listener, err := tnet.ListenTCPAddrPort(listenAddr)
		if err != nil {
			lg.Printf("netstack listen failed on %s: %v", listenAddr, err)
			continue
		}
		listeners = append(listeners, listener)
		lg.Printf("forwarding %s → %s:%d", listenAddr, targetHost, port)

		// Accept connections from helper through the WireGuard tunnel
		go func(l net.Listener, p uint16) {
			for {
				nsConn, err := l.Accept()
				if err != nil {
					return // listener closed
				}
				logVerbose(lg, "tunnel→%s:%d from %s", targetHost, p, nsConn.RemoteAddr())
				go proxyToTarget(lg, nsConn, targetHost, int(p))
			}
		}(listener, port)
	}

	// Start file share listener if configured
	cleanupFileShare := startFileShareListener(lg, tnet, sessionAgentIP, fsCfg)

	// Start gateway if configured
	cleanupGateway := startGatewayListener(lg, tnet, sessionAgentIP, gwCfg, targetHost)

	// Wait for session to end (either session-end message or WS error)
	<-done

	lg.Println("session ended -- tearing down WireGuard")
	cleanupGateway()
	cleanupFileShare()
	for _, l := range listeners {
		l.Close()
	}
	dev.Close()
}

// wsReader reads from the WebSocket and dispatches:
//   - Binary messages → wsBind.RecvCh (WireGuard datagrams)
//   - Text messages → parsed for control commands (udp-offer, peer-endpoint), else logged
func wsReader(lg *log.Logger, ws *websocket.Conn, bind *wsbind.Bind, hubURL string, sessionEnded chan struct{}) {
	for {
		msgType, data, err := ws.ReadMessage()
		if err != nil {
			lg.Printf("ws read error: %v", err)
			return
		}
		if msgType == websocket.BinaryMessage {
			select {
			case bind.RecvCh <- data:
			default:
				lg.Printf("wsBind recv buffer full, dropping %dB", len(data))
			}
		} else {
			var msg controlMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				logVerbose(lg, "text message during session: %s", string(data))
				continue
			}
			switch msg.Type {
			case "udp-offer":
				if !bind.UDPActive() {
					lg.Printf("received UDP relay offer (port %d)", msg.Port)
					tryUDPUpgrade(lg, bind, hubURL, msg.Token, msg.Port, msg.Host)
					if bind.UDPActive() {
						go tryDirectUpgrade(lg, bind, hubURL)
					}
				}
			case "peer-endpoint":
				if bind.UDPActive() && !bind.DirectActive() {
					lg.Printf("received peer endpoint: %s", msg.Message)
					go func() {
						if err := bind.AttemptDirect(msg.Message); err != nil {
							lg.Printf("direct tunnel failed (staying on relay): %v", err)
						}
					}()
				}
			case "session-end":
				lg.Println("client disconnected -- ending session")
				close(sessionEnded)
				return
			default:
				logVerbose(lg, "text message during session: %s", string(data))
			}
		}
	}
}

// tryUDPUpgrade attempts to switch from WebSocket to UDP relay transport.
func tryUDPUpgrade(lg *log.Logger, bind *wsbind.Bind, hubURL, tokenHex string, port int, host string) {
	if host == "" {
		u, err := url.Parse(hubURL)
		if err != nil {
			lg.Printf("UDP upgrade: cannot parse hub URL: %v", err)
			return
		}
		host = u.Hostname()
	}
	token, err := hex.DecodeString(tokenHex)
	if err != nil {
		lg.Printf("UDP upgrade: invalid token: %v", err)
		return
	}
	if err := bind.UpgradeUDP(host, port, token); err != nil {
		lg.Printf("UDP upgrade failed (continuing on WebSocket): %v", err)
	}
}

// tryDirectUpgrade performs STUN discovery and sends our reflexive
// address to the peer via the hub relay for hole punching.
func tryDirectUpgrade(lg *log.Logger, bind *wsbind.Bind, hubURL string) {
	reflexive, err := bind.STUNDiscover()
	if err != nil {
		lg.Printf("STUN discovery failed (staying on relay): %v", err)
		return
	}
	lg.Printf("STUN reflexive address: %s", reflexive)

	msg := controlMessage{Type: "peer-endpoint", Message: reflexive}
	data, _ := json.Marshal(msg)
	if err := bind.SendText(data); err != nil {
		lg.Printf("failed to send peer-endpoint: %v", err)
	}
}

// proxyToTarget connects to the real target service and pipes data bidirectionally.
func proxyToTarget(lg *log.Logger, nsConn net.Conn, targetHost string, targetPort int) {
	defer nsConn.Close()

	addr := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	targetConn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		lg.Printf("target dial failed (%s): %v", addr, err)
		return
	}
	defer targetConn.Close()

	if tc, ok := targetConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	logVerbose(lg, "proxying tunnel ↔ %s", addr)

	var wg sync.WaitGroup
	wg.Add(2)

	// tunnel (netstack) → target
	go func() {
		defer wg.Done()
		n, _ := io.Copy(targetConn, nsConn)
		logVerbose(lg, "tunnel→target closed (%d bytes)", n)
		if tc, ok := targetConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// target → tunnel (netstack)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(nsConn, targetConn)
		logVerbose(lg, "target→tunnel closed (%d bytes)", n)
	}()

	wg.Wait()
	logVerbose(lg, "proxy session ended for %s", addr)
}

// cmdLogin stores agent credentials in the system credential store.
func cmdLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub WebSocket URL (env: TELA_HUB)")
	fs.Parse(args)

	if *hubURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: telad login -hub <url>")
		fmt.Fprintln(os.Stderr, "Stores agent credentials in the system credential store.")
		fmt.Fprintln(os.Stderr, "Requires administrator/root privileges.")
		os.Exit(1)
	}

	if !service.IsElevated() {
		fmt.Fprintln(os.Stderr, "Error: telad login requires administrator/root privileges.")
		os.Exit(1)
	}

	// Prompt for token
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Token: ")
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: token cannot be empty")
		os.Exit(1)
	}

	// Optionally prompt for identity label
	fmt.Print("Identity (press Enter to skip): ")
	identity, _ := reader.ReadString('\n')
	identity = strings.TrimSpace(identity)

	store, err := credstore.Load(credstore.SystemPath())
	if err != nil {
		store = &credstore.Store{Hubs: make(map[string]credstore.Credential)}
	}
	store.Set(*hubURL, credstore.Credential{Token: token, Identity: identity})
	if err := store.Save(credstore.SystemPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving credentials: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Credentials stored for %s\n", *hubURL)
}

// cmdLogout removes agent credentials from the system credential store.
func cmdLogout(args []string) {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub WebSocket URL (env: TELA_HUB)")
	fs.Parse(args)

	if *hubURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: telad logout -hub <url>")
		os.Exit(1)
	}

	if !service.IsElevated() {
		fmt.Fprintln(os.Stderr, "Error: telad logout requires administrator/root privileges.")
		os.Exit(1)
	}

	store, err := credstore.Load(credstore.SystemPath())
	if err != nil {
		fmt.Println("No credentials stored.")
		return
	}

	if !store.Remove(*hubURL) {
		fmt.Printf("No credentials found for %s\n", *hubURL)
		return
	}
	if err := store.Save(credstore.SystemPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving credentials: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Credentials removed for %s\n", *hubURL)
}

// cmdPair exchanges a pairing code for a permanent agent token.
func cmdPair(args []string) {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub WebSocket URL (env: TELA_HUB)")
	code := fs.String("code", "", "Pairing code (e.g., ABCD-1234)")
	machineID := fs.String("machine", "", "Machine ID (optional; if omitted, code determines it)")
	fs.Parse(args)

	if *hubURL == "" || *code == "" {
		fmt.Fprintln(os.Stderr, "Usage: telad pair -hub <url> -code <code> [-machine <id>]")
		fmt.Fprintln(os.Stderr, "Exchanges a pairing code for a permanent agent token.")
		os.Exit(1)
	}

	// Convert WS URL to HTTP for API calls
	httpURL := wsToHTTP(*hubURL)

	// Call /api/pair to redeem the code
	req := map[string]string{
		"code": *code,
	}
	if *machineID != "" {
		req["machineId"] = *machineID
	}

	body, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding request: %v\n", err)
		os.Exit(1)
	}

	resp, err := http.Post(
		fmt.Sprintf("%s/api/pair", httpURL),
		"application/json",
		strings.NewReader(string(body)),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to hub: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		json.Unmarshal(respBody, &errResp)
		if msg, ok := errResp["error"]; ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
		} else {
			fmt.Fprintf(os.Stderr, "Error: HTTP %d\n", resp.StatusCode)
		}
		os.Exit(1)
	}

	var result map[string]string
	if err := json.Unmarshal(respBody, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	token, ok := result["token"]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: no token in response\n")
		os.Exit(1)
	}

	identity, ok := result["identity"]
	if !ok {
		identity = "agent"
	}

	// Store the token in the credential store
	// Try to use elevated store first; fall back to user store
	storePath := credstore.SystemPath()
	store, err := credstore.Load(storePath)
	if err != nil {
		store = &credstore.Store{Hubs: make(map[string]credstore.Credential)}
	}

	store.Set(*hubURL, credstore.Credential{Token: token, Identity: identity})
	if err := store.Save(storePath); err != nil {
		// Fallback to user store if system store isn't writable
		storePath = credstore.UserPath()
		store, _ := credstore.Load(storePath)
		store.Set(*hubURL, credstore.Credential{Token: token, Identity: identity})
		if err := store.Save(storePath); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving credentials: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Token stored in user credential store (system store not writable)")
	}

	fmt.Printf("Token redeemed and stored for %s\n", *hubURL)
	fmt.Printf("Identity: %s\n", identity)
	fmt.Printf("Agent can now connect without passing -token flag\n")
}

// wsToHTTP converts a WebSocket URL to HTTP URL.
func wsToHTTP(wsURL string) string {
	s := strings.Replace(wsURL, "wss://", "https://", 1)
	s = strings.Replace(s, "ws://", "http://", 1)
	return strings.TrimRight(s, "/")
}

func logVerbose(lg *log.Logger, format string, args ...any) {
	if verbose {
		lg.Printf(format, args...)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

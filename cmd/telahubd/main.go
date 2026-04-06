// telahubd -- Tela Hub
//
// Combined HTTP + WebSocket + UDP relay
// server on a single port. Serves the hub console (static files), exposes
// /api/status and /api/history with permissive CORS, and relays paired
// WireGuard sessions between agents and clients.
//
// Configuration (in order of precedence, highest first):
//   1. Environment variables  (TELAHUBD_PORT, TELAHUBD_UDP_PORT, TELAHUBD_NAME, TELAHUBD_WWW_DIR)
//   2. YAML config file       (-config telahubd.yaml)
//   3. Built-in defaults      (port 80, udpPort 41820, wwwDir ./www)
//
// When running as an OS service the binary reads its config from the
// system-wide location (e.g. /etc/tela/telahubd.yaml or
// %ProgramData%\Tela\telahubd.yaml). Edit the file and restart the
// service to reconfigure.
//
// Invariants:
//   - Hub never inspects encrypted tunnel payloads (zero-knowledge relay).
//   - WebSocket upgrade must be supported end-to-end (reverse proxy must
//     forward Upgrade/Connection headers).
//   - Binary messages are relayed verbatim between paired peers.
//   - UDP relay tokens are 8 random bytes; PROBE/READY handshake is
//     compatible with tela/telad wsBind implementation.
//
// WARNING: Do not modify the relay pairing or UDP token protocol without
// verifying compatibility with tela (cmd/tela) and telad (cmd/telad).

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"

	"github.com/paulmooreparks/tela/console"
	"github.com/paulmooreparks/tela/internal/service"
	"github.com/paulmooreparks/tela/internal/telelog"
)

// version is set by -ldflags at build time.
var version = "dev"

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

// writeControlFile writes telahubd's control file so TelaVisor can
// detect the running instance. Returns a cleanup function.
func writeHubControlFile() func() {
	info := map[string]interface{}{
		"pid":       os.Getpid(),
		"port":      httpPort,
		"name":      hubName,
		"adminPort": httpPort,
	}

	runDir := filepath.Join(telaConfigDir(), "run")
	if err := os.MkdirAll(runDir, 0700); err != nil {
		log.Printf("[hub] failed to create run directory: %v", err)
		return func() {}
	}

	controlPath := filepath.Join(runDir, "telahubd.json")
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return func() {}
	}
	if err := os.WriteFile(controlPath, data, 0600); err != nil {
		log.Printf("[hub] failed to write control file: %v", err)
		return func() {}
	}

	return func() {
		os.Remove(controlPath)
	}
}

// ── Configuration ──────────────────────────────────────────────────

// portalEntry stores a registered portal association.
type portalEntry struct {
	URL          string `yaml:"url"`                     // Portal base URL
	Token        string `yaml:"token,omitempty"`         // Legacy admin API token (cleared on new registrations)
	SyncToken    string `yaml:"syncToken,omitempty"`     // Per-hub sync token for viewer token updates
	HubDirectory string `yaml:"hubDirectory,omitempty"` // Discovered hub directory path
}

// hubConfig is the YAML configuration for telahubd.
type hubConfig struct {
	Port      int                    `yaml:"port"`              // HTTP+WS listen port (default 80)
	UDPPort   int                    `yaml:"udpPort"`           // UDP relay port (default 41820)
	UDPHost   string                 `yaml:"udpHost,omitempty"` // public IP/hostname for UDP relay (when behind proxy)
	Name      string                 `yaml:"name"`              // Display name for this hub
	WWWDir    string                 `yaml:"wwwDir"`            // Static file directory (default ./www)
	Auth      authConfig             `yaml:"auth,omitempty"`    // Token-based access control
	Portals   map[string]portalEntry `yaml:"portals,omitempty"` // Registered portals
}

// loadHubConfig reads a telahubd YAML config file.
func loadHubConfig(path string) (*hubConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg hubConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// applyHubConfig sets package-level vars from a config, then lets env
// vars override (so env always wins over file).
func applyHubConfig(cfg *hubConfig) {
	if cfg != nil {
		if cfg.Port != 0 {
			httpPort = cfg.Port
		}
		if cfg.UDPPort != 0 {
			udpPort = cfg.UDPPort
		}
		if cfg.UDPHost != "" {
			udpHost = cfg.UDPHost
		}
		if cfg.Name != "" {
			hubName = cfg.Name
		}
		if cfg.WWWDir != "" {
			if info, err := os.Stat(cfg.WWWDir); err == nil && info.IsDir() {
				wwwDir = cfg.WWWDir
				wwwDirOverride = true
			}
		}
	}

	// Env vars override config file
	httpPort = envInt("TELAHUBD_PORT", httpPort)
	udpPort = envInt("TELAHUBD_UDP_PORT", udpPort)
	udpHost = envStr("TELAHUBD_UDP_HOST", udpHost)
	hubName = envStr("TELAHUBD_NAME", hubName)
	if v := os.Getenv("TELAHUBD_WWW_DIR"); v != "" {
		if info, err := os.Stat(v); err == nil && info.IsDir() {
			wwwDir = v
			wwwDirOverride = true
		}
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	httpPort       = 80
	udpPort        = 41820
	udpHost        = "" // if set, included in udp-offer for proxy setups
	hubName        = ""
	wwwDir         = "./www"
	wwwDirOverride = false // true when wwwDir explicitly set via config/env
	verbose        = false // if true, log individual relay messages
	globalAuth     = newAuthStore(nil) // replaced at startup; open hub until config is loaded
	globalCfg      *hubConfig          // live config; mutated by admin API
	globalCfgMu    sync.RWMutex        // protects globalCfg + config file writes
	globalCfgPath  string              // path to YAML config file (for admin API persistence)
)

// ── Data structures ────────────────────────────────────────────────

// clientSession tracks one active client↔agent session on a machine.
type clientSession struct {
	SessionID  string
	SessionIdx int
	ClientWS   *safeConn
	AgentWS    *safeConn // agent's per-session WS (opened via session-join)
	UDPTokens  []string
	CreatedAt  time.Time // for session-request timeout detection
}

// machineEntry stores the state of a registered machine.
type machineEntry struct {
	mu sync.Mutex

	ControlWS  *safeConn                   // agent's registration/signaling channel
	ControlGen uint64                       // incremented on each new ControlWS; used to avoid stale disconnect races
	Sessions   map[string]*clientSession    // sessionID → active session
	NextIdx    int                          // monotonically incrementing session index

	// Metadata from registration
	Ports        []int
	Services     []serviceDesc
	Token        string
	RegisteredAt time.Time
	LastSeen     time.Time
	DisplayName  string
	Hostname     string
	OS           string
	AgentVersion string
	Tags         []string
	Location     string
	Owner        string
	Capabilities map[string]interface{}
}

type serviceDesc struct {
	Port        int    `json:"port"`
	Proto       string `json:"proto"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// historyEvent records a session event.
type historyEvent struct {
	MachineID string `json:"machineId"`
	Event     string `json:"event"`
	Timestamp string `json:"timestamp"`
	Detail    string `json:"detail,omitempty"`
}

// wsState is stored per WebSocket connection.
type wsState struct {
	Role      string // "agent", "client", or "agent-session"
	MachineID string
	SessionID string // non-empty for session-specific connections
	Paired    bool
	Peer      *safeConn
	WGPubKey  string // client's WireGuard public key

	// ControlGen is set on agent registration to the machineEntry's ControlGen
	// at that time. handleDisconnect uses it to avoid clearing a newer
	// ControlWS when a stale connection's goroutine finally exits.
	ControlGen uint64

	// RequestedPorts is an optional hint from the client about which service
	// ports it intends to use (e.g., when tela connect uses -port/-target-port).
	// If empty, the hub treats the session as covering all advertised services.
	RequestedPorts []int

	// SessionDetail is a human-friendly summary for portals/console.
	// Example: "services=SSH:22,RDP:3389".
	SessionDetail string
}

// udpSession tracks one side of a UDP relay pair.
type udpSession struct {
	PeerTokenHex string
	PeerWS       *safeConn    // fallback: peer's WebSocket (write-safe)
	Role         string
	Addr         *net.UDPAddr // learned from first UDP message
	MachineID    string
	CreatedAt    time.Time
}

// ── Synchronized WebSocket writer ──────────────────────────────────

// safeConn wraps a websocket.Conn with a mutex to serialize all writes.
// gorilla/websocket is not safe for concurrent WriteMessage/WriteControl
// calls, so every write to a connection must go through this wrapper.
type safeConn struct {
	*websocket.Conn
	writeMu sync.Mutex
}

func newSafeConn(ws *websocket.Conn) *safeConn {
	return &safeConn{Conn: ws}
}

func (sc *safeConn) WriteMessage(messageType int, data []byte) error {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	return sc.Conn.WriteMessage(messageType, data)
}

func (sc *safeConn) WriteControl(messageType int, data []byte, deadline time.Time) error {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	return sc.Conn.WriteControl(messageType, data, deadline)
}

// ── Global state ───────────────────────────────────────────────────

var (
	machines   = make(map[string]*machineEntry)
	machinesMu sync.RWMutex

	// Per-WebSocket state, keyed by connection pointer.
	wsStates   = make(map[*websocket.Conn]*wsState)
	wsStatesMu sync.RWMutex

	// Session history (ring buffer, most recent first when read).
	history      [maxHistory]historyEvent
	historyHead  int // next write position (circular)
	historyCount int // total items written (capped at maxHistory)
	historyMu    sync.Mutex

	// UDP relay sessions, keyed by token hex.
	udpSessions   = make(map[string]*udpSession)
	udpSessionsMu sync.Mutex

	// Pending management requests, keyed by requestId.
	// The HTTP handler writes a request and blocks on the channel;
	// the WebSocket handler delivers the response.
	mgmtPending   = make(map[string]chan json.RawMessage)
	mgmtPendingMu sync.Mutex
)

const maxHistory = 100

func recordEvent(machineID, event, detail string) {
	historyMu.Lock()
	defer historyMu.Unlock()
	history[historyHead] = historyEvent{
		MachineID: machineID,
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Detail:    detail,
	}
	historyHead = (historyHead + 1) % maxHistory
	if historyCount < maxHistory {
		historyCount++
	}
}

// snapshotHistory returns a copy of the history ring buffer, most recent first.
func snapshotHistory() []historyEvent {
	historyMu.Lock()
	defer historyMu.Unlock()
	out := make([]historyEvent, 0, historyCount)
	for i := 0; i < historyCount; i++ {
		idx := (historyHead - 1 - i + maxHistory) % maxHistory
		out = append(out, history[idx])
	}
	return out
}

func sessionDetailFrom(entry *machineEntry, requestedPorts []int) string {
	entry.mu.Lock()
	services := normalizeServices(entry.Ports, entry.Services)
	entry.mu.Unlock()

	if len(services) == 0 {
		return ""
	}

	portSet := map[int]bool{}
	if len(requestedPorts) > 0 {
		for _, p := range requestedPorts {
			portSet[p] = true
		}
	}

	parts := make([]string, 0, len(services))
	for _, s := range services {
		if len(portSet) > 0 && !portSet[s.Port] {
			continue
		}
		name := s.Name
		if name == "" {
			name = portLabel(s.Port)
		}
		parts = append(parts, fmt.Sprintf("%s:%d", name, s.Port))
	}
	if len(parts) == 0 {
		return ""
	}
	return "services=" + strings.Join(parts, ",")
}

// ── Well-known port labels ─────────────────────────────────────────

var portLabels = map[int]string{
	22: "SSH", 80: "HTTP", 443: "HTTPS", 3389: "RDP",
	5900: "VNC", 8080: "HTTP-Alt", 8443: "HTTPS-Alt",
}

func portLabel(p int) string {
	if name, ok := portLabels[p]; ok {
		return name
	}
	return fmt.Sprintf("port %d", p)
}

// normalizeServices converts ports[] / services[] into a consistent
// list of serviceDesc, matching hub.js behavior exactly.
func normalizeServices(ports []int, services []serviceDesc) []serviceDesc {
	if len(services) > 0 {
		out := make([]serviceDesc, 0, len(services))
		for _, s := range services {
			if s.Port == 0 {
				continue
			}
			proto := strings.ToLower(s.Proto)
			if proto == "" {
				proto = "tcp"
			}
			name := s.Name
			if name == "" {
				name = portLabel(s.Port)
			}
			out = append(out, serviceDesc{
				Port:        s.Port,
				Proto:       proto,
				Name:        name,
				Description: s.Description,
			})
		}
		return out
	}
	out := make([]serviceDesc, 0, len(ports))
	for _, p := range ports {
		out = append(out, serviceDesc{
			Port:  p,
			Proto: "tcp",
			Name:  portLabel(p),
		})
	}
	return out
}

// ── HTTP handlers ──────────────────────────────────────────────────

var startTime = time.Now()

func corsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// adminCorsHeaders sets restrictive CORS for admin endpoints -- no wildcard origin.
// Cross-origin browser requests to admin endpoints are not expected; admin
// access is via CLI or same-origin console.
func adminCorsHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser request (curl, CLI) -- no CORS headers needed.
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Vary", "Origin")
}

// tokenFromRequest extracts the caller token from Authorization: Bearer,
// the tela_viewer cookie, or (deprecated) the ?token= query param.
func tokenFromRequest(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if c, err := r.Cookie("tela_viewer"); err == nil {
		return c.Value
	}
	if t := r.URL.Query().Get("token"); t != "" {
		log.Printf("[hub] DEPRECATED: token passed via ?token= query param from %s -- use Authorization header instead", r.RemoteAddr)
		return t
	}
	return ""
}

func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Auth: if enabled and no valid token provided, return 401.
	callerToken := tokenFromRequest(r)
	if globalAuth.isEnabled() && globalAuth.identityID(callerToken) == "" {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": "auth_required", "machines": []any{}})
		return
	}

	machinesMu.RLock()
	type statusMachine struct {
		ID             string        `json:"id"`
		DisplayName    *string       `json:"displayName"`
		Hostname       *string       `json:"hostname"`
		OS             *string       `json:"os"`
		AgentVersion   *string       `json:"agentVersion"`
		Tags           []string      `json:"tags"`
		Location       *string       `json:"location"`
		Owner          *string       `json:"owner"`
		AgentConnected bool                   `json:"agentConnected"`
		SessionCount   int                    `json:"sessionCount"`
		RegisteredAt   *string                `json:"registeredAt"`
		LastSeen       *string                `json:"lastSeen"`
		Services       []serviceDesc          `json:"services"`
		Capabilities   map[string]interface{} `json:"capabilities,omitempty"`
	}

	list := make([]statusMachine, 0, len(machines))
	for id, entry := range machines {
		// Filter: skip machines the caller cannot view.
		if globalAuth.isEnabled() && !globalAuth.canViewMachine(callerToken, id) {
			continue
		}
		entry.mu.Lock()
		sm := statusMachine{
			ID:             id,
			Tags:           entry.Tags,
			AgentConnected: entry.ControlWS != nil,
			SessionCount:   len(entry.Sessions),
			Services:       normalizeServices(entry.Ports, entry.Services),
		}
		if entry.DisplayName != "" {
			sm.DisplayName = &entry.DisplayName
		}
		if entry.Hostname != "" {
			sm.Hostname = &entry.Hostname
		}
		if entry.OS != "" {
			sm.OS = &entry.OS
		}
		if entry.AgentVersion != "" {
			sm.AgentVersion = &entry.AgentVersion
		}
		if entry.Location != "" {
			sm.Location = &entry.Location
		}
		if entry.Owner != "" {
			sm.Owner = &entry.Owner
		}
		if entry.Capabilities != nil {
			sm.Capabilities = entry.Capabilities
		}
		if !entry.RegisteredAt.IsZero() {
			s := entry.RegisteredAt.UTC().Format(time.RFC3339)
			sm.RegisteredAt = &s
		}
		if !entry.LastSeen.IsZero() {
			s := entry.LastSeen.UTC().Format(time.RFC3339)
			sm.LastSeen = &s
		}
		if sm.Tags == nil {
			sm.Tags = []string{}
		}
		if sm.Services == nil {
			sm.Services = []serviceDesc{}
		}
		entry.mu.Unlock()
		list = append(list, sm)
	}
	machinesMu.RUnlock()

	hostname, _ := os.Hostname()
	payload := map[string]any{
		"machines":  list,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"hub": map[string]any{
			"os":        runtime.GOOS,
			"arch":      runtime.GOARCH,
			"hostname":  hostname,
			"goVersion": runtime.Version(),
			"version":   version,
			"uptime":    int(time.Since(startTime).Seconds()),
		},
	}
	if hubName != "" {
		payload["hubName"] = hubName
	}

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(payload)
}

func handleAPIHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	callerToken := tokenFromRequest(r)

	// Auth: if enabled and no valid token provided, return 401.
	if globalAuth.isEnabled() && globalAuth.identityID(callerToken) == "" {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": "auth_required", "events": []any{}})
		return
	}

	historySnapshot := snapshotHistory()
	events := make([]historyEvent, 0, len(historySnapshot))
	for _, e := range historySnapshot {
		if globalAuth.isEnabled() && !globalAuth.canViewMachine(callerToken, e.MachineID) {
			continue
		}
		events = append(events, e)
	}

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"events":    events,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// ── Static file serving ────────────────────────────────────────────

// wwwFS returns the filesystem used for serving static console files.
// If wwwDir was explicitly set (via config or env), serve from disk;
// otherwise serve from the embedded console.FS.
func wwwFS() fs.FS {
	if wwwDirOverride {
		return os.DirFS(wwwDir)
	}
	// console.FS has files under "www/", so strip that prefix.
	sub, _ := fs.Sub(console.FS, "www")
	return sub
}

func readFSFile(fsys fs.FS, name string) ([]byte, error) {
	return fs.ReadFile(fsys, name)
}

func handleStatic(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	if urlPath == "/" {
		urlPath = "/index.html"
	}
	// Strip leading slash for fs.FS paths
	fsPath := strings.TrimPrefix(urlPath, "/")
	fsPath = strings.TrimSuffix(fsPath, "/")

	root := wwwFS()

	info, err := fs.Stat(root, fsPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		fsPath = fsPath + "/index.html"
		info, err = fs.Stat(root, fsPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}

	// For index.html, inject runtime values and set a viewer cookie so
	// same-origin API fetches are authenticated automatically.
	if strings.HasSuffix(fsPath, "index.html") {
		data, readErr := readFSFile(root, fsPath)
		if readErr == nil {
			viewerToken := globalAuth.consoleViewerToken()

			// Only inject viewer token and set cookie for the console
			// page (root index.html), not for sub-pages or static content.
			isConsolePage := fsPath == "index.html"

			// Set HttpOnly cookie so browser API fetches include the token.
			if viewerToken != "" && isConsolePage {
				http.SetCookie(w, &http.Cookie{
					Name:     "tela_viewer",
					Value:    viewerToken,
					Path:     "/",
					HttpOnly: true,
					Secure:   r.TLS != nil,
					SameSite: http.SameSiteLaxMode,
				})
			}

			var parts []string
			parts = append(parts, fmt.Sprintf("window.TELA_HUB_VERSION=%q;", version))
			if viewerToken != "" && isConsolePage {
				parts = append(parts, fmt.Sprintf("window.TELA_CONSOLE_TOKEN=%q;", viewerToken))
			}
			injection := "<script>" + strings.Join(parts, "") + "</script>"
			html := bytes.Replace(data, []byte("</head>"), []byte(injection+"\n</head>"), 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(html)
			return
		}
	}

	// Serve the file using http.FileServer over the fs.FS.
	ext := filepath.Ext(fsPath)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	data, err := readFSFile(root, fsPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, fsPath, info.ModTime(), bytes.NewReader(data))
}

// ── WebSocket relay ────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Agents and CLI clients don't send an Origin header -- always allow.
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		// When auth is enabled, require same-origin for browser-initiated
		// WebSocket upgrades to prevent cross-site WebSocket hijacking.
		if globalAuth.isEnabled() {
			host := r.Host
			// Origin typically includes scheme: "http://host" or "https://host"
			return strings.HasSuffix(origin, "://"+host)
		}
		return true
	},
}

// JSON messages from agents and clients during signaling.
type signalingMsg struct {
	Type         string        `json:"type"`
	MachineID    string        `json:"machineId,omitempty"`
	Token        string        `json:"token,omitempty"`
	WGPubKey     string        `json:"wgPubKey,omitempty"`
	Ports        []int         `json:"ports,omitempty"`
	Services     []serviceDesc `json:"services,omitempty"`
	DisplayName  string        `json:"displayName,omitempty"`
	Hostname     string        `json:"hostname,omitempty"`
	OS           string        `json:"os,omitempty"`
	AgentVersion string        `json:"agentVersion,omitempty"`
	Tags         []string      `json:"tags,omitempty"`
	Location     string        `json:"location,omitempty"`
	Owner        string        `json:"owner,omitempty"`

	SessionID string `json:"sessionId,omitempty"`

	// Forwarded through (peer-endpoint, wg-pubkey, etc.)
	Message      string                 `json:"message,omitempty"`
	Capabilities map[string]interface{} `json:"capabilities,omitempty"`

	// Management protocol fields
	RequestID string          `json:"requestId,omitempty"`
	Action    string          `json:"action,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[hub] ws upgrade failed: %v", err)
		return
	}
	sc := newSafeConn(ws)

	state := &wsState{}
	wsStatesMu.Lock()
	wsStates[ws] = state
	wsStatesMu.Unlock()

	// Set up pong handler for keepalive
	ws.SetPongHandler(func(appData string) error {
		if state.Role == "agent" && state.MachineID != "" {
			machinesMu.RLock()
			entry, ok := machines[state.MachineID]
			machinesMu.RUnlock()
			if ok {
				entry.mu.Lock()
				entry.LastSeen = time.Now()
				entry.mu.Unlock()
			}
		}
		return nil
	})

	defer func() {
		handleDisconnect(sc)
		wsStatesMu.Lock()
		delete(wsStates, ws)
		wsStatesMu.Unlock()
		ws.Close()
	}()

	for {
		msgType, data, err := ws.ReadMessage()
		if err != nil {
			return
		}

		// If paired, relay to peer (absorb keepalive frames)
		if state.Paired {
			if msgType == websocket.TextMessage && len(data) < 64 && bytes.Contains(data, []byte(`"keepalive"`)) {
				continue // proxy keepalive; do not relay
			}
			wsStatesMu.RLock()
			peer := state.Peer
			wsStatesMu.RUnlock()
			if peer != nil {
				if err := peer.WriteMessage(msgType, data); err != nil {
					log.Printf("[hub] relay write error: %v", err)
				} else if verbose {
					peerRole := "?"
					wsStatesMu.RLock()
					if ps, ok := wsStates[peer.Conn]; ok {
						peerRole = ps.Role
					}
					wsStatesMu.RUnlock()
					log.Printf("[hub] relay %s→%s %dB binary=%v", state.Role, peerRole, len(data), msgType == websocket.BinaryMessage)
				}
			}
			continue
		}

		// First message must be JSON signaling
		if msgType != websocket.TextMessage {
			sc.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseProtocolError, "Expected JSON for first message"))
			return
		}

		var msg signalingMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			sc.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseProtocolError, "Expected JSON for first message"))
			return
		}

		switch msg.Type {
		case "register":
			handleRegister(sc, state, &msg)
		case "connect":
			handleConnect(sc, state, &msg)
		case "session-join":
			handleSessionJoin(sc, state, &msg)
		case "mgmt-response":
			// Deliver management response to the waiting HTTP handler
			if msg.RequestID != "" {
				mgmtPendingMu.Lock()
				ch, ok := mgmtPending[msg.RequestID]
				mgmtPendingMu.Unlock()
				if ok {
					ch <- data
				}
			}
		default:
			// Ignore unknown message types (do not close the connection)
			if verbose {
				log.Printf("[hub] unknown message type %q from %s", msg.Type, state.MachineID)
			}
		}
	}
}

func handleRegister(ws *safeConn, state *wsState, msg *signalingMsg) {
	machineID := msg.MachineID
	state.Role = "agent"
	state.MachineID = machineID

	// Auth check: is this token allowed to register this machine?
	if globalAuth.isEnabled() && !globalAuth.canRegister(msg.Token, machineID) {
		id := globalAuth.identityID(msg.Token)
		if id == "" {
			id = "unknown"
		}
		log.Printf("[hub] agent register denied: %s (identity: %s)", machineID, id)
		sendError(ws, "Access denied")
		return
	}

	now := time.Now()

	machinesMu.Lock()
	entry, exists := machines[machineID]
	if !exists {
		entry = &machineEntry{
			RegisteredAt: now,
		}
		machines[machineID] = entry
	}
	machinesMu.Unlock()

	entry.mu.Lock()
	// Close the old control WebSocket if the agent is reconnecting.
	// This forces the old goroutine to exit. The generation counter
	// ensures that when the old goroutine's handleDisconnect fires,
	// it will not clear the new ControlWS (see handleDisconnect).
	if entry.ControlWS != nil && entry.ControlWS != ws {
		oldWS := entry.ControlWS
		go oldWS.Close()
	}
	entry.ControlWS = ws
	entry.ControlGen++
	state.ControlGen = entry.ControlGen
	entry.Token = msg.Token
	entry.Ports = msg.Ports
	entry.Services = msg.Services
	if entry.Sessions == nil {
		entry.Sessions = make(map[string]*clientSession)
	}
	if msg.DisplayName != "" {
		entry.DisplayName = msg.DisplayName
	}
	if msg.Hostname != "" {
		entry.Hostname = msg.Hostname
	}
	if msg.OS != "" {
		entry.OS = msg.OS
	}
	if msg.AgentVersion != "" {
		entry.AgentVersion = msg.AgentVersion
	}
	if msg.Tags != nil {
		entry.Tags = msg.Tags
	}
	if msg.Location != "" {
		entry.Location = msg.Location
	}
	if msg.Owner != "" {
		entry.Owner = msg.Owner
	}
	if msg.Capabilities != nil {
		entry.Capabilities = msg.Capabilities
	}
	entry.LastSeen = now
	if entry.RegisteredAt.IsZero() {
		entry.RegisteredAt = now
	}

	normalized := normalizeServices(entry.Ports, entry.Services)
	ports := make([]int, len(normalized))
	for i, s := range normalized {
		ports[i] = s.Port
	}
	entry.mu.Unlock()

	log.Printf("[hub] agent registered: %s ports=%v version=%q%s", machineID, ports, msg.AgentVersion, tokenLog(msg.Token))
	recordEvent(machineID, "agent-register", fmt.Sprintf("ports=%v", ports))

	reply, _ := json.Marshal(map[string]string{"type": "registered", "machineId": machineID})
	ws.WriteMessage(websocket.TextMessage, reply)
}

func handleConnect(ws *safeConn, state *wsState, msg *signalingMsg) {
	machineID := msg.MachineID
	state.Role = "client"
	state.MachineID = machineID
	state.WGPubKey = msg.WGPubKey
	if msg.Ports != nil {
		state.RequestedPorts = append([]int(nil), msg.Ports...)
	}

	machinesMu.RLock()
	entry, exists := machines[machineID]
	machinesMu.RUnlock()

	if !exists || entry == nil {
		sendError(ws, "Machine not found")
		return
	}

	entry.mu.Lock()
	controlWS := entry.ControlWS
	entryToken := entry.Token
	entry.mu.Unlock()

	if controlWS == nil {
		sendError(ws, "Machine not found")
		return
	}

	// Token / auth validation
	if globalAuth.isEnabled() {
		if !globalAuth.canConnect(msg.Token, machineID) {
			id := globalAuth.identityID(msg.Token)
			if id == "" {
				id = "unknown"
			}
			log.Printf("[hub] client connect denied: %s (identity: %s)", machineID, id)
			sendError(ws, "Access denied")
			return
		}
	} else if entryToken != "" && subtle.ConstantTimeCompare([]byte(entryToken), []byte(msg.Token)) != 1 {
		log.Printf("[hub] client token mismatch for %s", machineID)
		errMsg, _ := json.Marshal(map[string]string{"type": "error", "message": "Invalid token"})
		ws.WriteMessage(websocket.TextMessage, errMsg)
		ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Invalid token"))
		return
	}

	// Generate session ID
	sidBytes := make([]byte, 8)
	rand.Read(sidBytes)
	sessionID := hex.EncodeToString(sidBytes)
	state.SessionID = sessionID

	// Store session (client side; agent WS added when agent joins)
	session := &clientSession{
		SessionID: sessionID,
		ClientWS:  ws,
		CreatedAt: time.Now(),
	}
	entry.mu.Lock()
	if entry.Sessions == nil {
		entry.Sessions = make(map[string]*clientSession)
	}
	if entry.NextIdx >= 254 {
		entry.mu.Unlock()
		sendError(ws, "Too many sessions for this machine (max 254)")
		return
	}
	entry.Sessions[sessionID] = session
	entry.NextIdx++
	sessionIdx := entry.NextIdx
	session.SessionIdx = sessionIdx
	entry.mu.Unlock()

	log.Printf("[hub] client connected for: %s session=%s%s", machineID, sessionID[:8], wgLog(msg.WGPubKey))

	// Ask the agent to open a session WebSocket
	req := map[string]any{
		"type":      "session-request",
		"sessionId": sessionID,
		"wgPubKey":  msg.WGPubKey,
		"sessionIdx": sessionIdx,
	}
	data, _ := json.Marshal(req)
	controlWS.WriteMessage(websocket.TextMessage, data)

	// Start a timeout: if the agent does not join within 30 seconds,
	// clean up the dangling session so it does not leak.
	go func() {
		time.Sleep(30 * time.Second)
		entry.mu.Lock()
		sess, ok := entry.Sessions[sessionID]
		if !ok || sess.AgentWS != nil {
			// Session was joined or already cleaned up.
			entry.mu.Unlock()
			return
		}
		delete(entry.Sessions, sessionID)
		entry.mu.Unlock()
		log.Printf("[hub] session %s timed out waiting for agent join (%s)", sessionID[:8], machineID)
		cleanupSession(machineID, sess)
		if sess.ClientWS != nil {
			sess.ClientWS.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "agent did not join session"))
			sess.ClientWS.Close()
		}
	}()
}

func handleSessionJoin(ws *safeConn, state *wsState, msg *signalingMsg) {
	machineID := msg.MachineID
	sessionID := msg.SessionID
	state.Role = "agent-session"
	state.MachineID = machineID
	state.SessionID = sessionID

	machinesMu.RLock()
	entry, exists := machines[machineID]
	machinesMu.RUnlock()
	if !exists {
		sendError(ws, "Machine not found")
		return
	}

	entry.mu.Lock()
	session, ok := entry.Sessions[sessionID]
	if !ok || session == nil {
		entry.mu.Unlock()
		sendError(ws, "Session not found")
		return
	}
	session.AgentWS = ws
	clientWS := session.ClientWS
	entry.mu.Unlock()

	if clientWS == nil {
		sendError(ws, "Client gone")
		return
	}

	log.Printf("[hub] agent joined session %s for: %s", sessionID[:8], machineID)
	pairSession(machineID, entry, session)
}

func pairSession(machineID string, entry *machineEntry, session *clientSession) {
	agentWS := session.AgentWS
	clientWS := session.ClientWS
	if agentWS == nil || clientWS == nil {
		return
	}

	// Cross-link peers
	wsStatesMu.Lock()
	agentState := wsStates[agentWS.Conn]
	clientState := wsStates[clientWS.Conn]
	if agentState != nil {
		agentState.Paired = true
		agentState.Peer = clientWS
	}
	if clientState != nil {
		clientState.Paired = true
		clientState.Peer = agentWS
	}
	if clientState != nil {
		entryDetail := sessionDetailFrom(entry, clientState.RequestedPorts)
		clientState.SessionDetail = entryDetail
		if agentState != nil {
			agentState.SessionDetail = entryDetail
		}
	}
	wgPubKey := ""
	if clientState != nil {
		wgPubKey = clientState.WGPubKey
	}
	wsStatesMu.Unlock()

	log.Printf("[hub] paired agent <-> client for: %s session=%s", machineID, session.SessionID[:8])
	ss := ""
	wsStatesMu.RLock()
	if cs := wsStates[clientWS.Conn]; cs != nil {
		ss = cs.SessionDetail
	}
	wsStatesMu.RUnlock()
	recordEvent(machineID, "client-connect", ss)

	// Signal agent session WS: session-start (with client's WG public key)
	sessionStart := map[string]any{"type": "session-start"}
	if wgPubKey != "" {
		sessionStart["wgPubKey"] = wgPubKey
	}
	data, _ := json.Marshal(sessionStart)
	agentWS.WriteMessage(websocket.TextMessage, data)

	// Signal client: ready (include session index for per-session IP addressing)
	ready, _ := json.Marshal(map[string]any{"type": "ready", "sessionIdx": session.SessionIdx})
	clientWS.WriteMessage(websocket.TextMessage, ready)

	// Generate UDP relay tokens and send udp-offer to both sides
	const tokenLen = 8
	agentToken := make([]byte, tokenLen)
	clientToken := make([]byte, tokenLen)
	rand.Read(agentToken)
	rand.Read(clientToken)
	agentTokenHex := hex.EncodeToString(agentToken)
	clientTokenHex := hex.EncodeToString(clientToken)

	now := time.Now()
	udpSessionsMu.Lock()
	udpSessions[agentTokenHex] = &udpSession{
		PeerTokenHex: clientTokenHex,
		PeerWS:       clientWS,
		Role:         "agent",
		MachineID:    machineID,
		CreatedAt:    now,
	}
	udpSessions[clientTokenHex] = &udpSession{
		PeerTokenHex: agentTokenHex,
		PeerWS:       agentWS,
		Role:         "client",
		MachineID:    machineID,
		CreatedAt:    now,
	}
	udpSessionsMu.Unlock()

	entry.mu.Lock()
	session.UDPTokens = []string{agentTokenHex, clientTokenHex}
	entry.mu.Unlock()

	// Send udp-offer to both sides
	offer := map[string]any{"type": "udp-offer", "port": udpPort}
	if udpHost != "" {
		offer["host"] = udpHost
	}

	offer["token"] = agentTokenHex
	agentOffer, _ := json.Marshal(offer)
	agentWS.WriteMessage(websocket.TextMessage, agentOffer)

	offer["token"] = clientTokenHex
	clientOffer, _ := json.Marshal(offer)
	clientWS.WriteMessage(websocket.TextMessage, clientOffer)

	log.Printf("[hub] sent udp-offer to both sides for: %s session=%s (port %d)", machineID, session.SessionID[:8], udpPort)
}

func handleDisconnect(ws *safeConn) {
	wsStatesMu.RLock()
	state, ok := wsStates[ws.Conn]
	wsStatesMu.RUnlock()
	if !ok || state.MachineID == "" {
		return
	}

	machinesMu.RLock()
	entry, exists := machines[state.MachineID]
	machinesMu.RUnlock()
	if !exists {
		return
	}

	log.Printf("[hub] %s disconnected: %s (session=%s)", state.Role, state.MachineID, state.SessionID)
	detail := state.SessionDetail
	if detail == "" {
		detail = state.Role + " disconnected"
	}
	recordEvent(state.MachineID, state.Role+"-disconnect", detail)

	switch state.Role {
	case "agent":
		// Agent control WS disconnected. Use the generation counter to
		// determine whether this is still the active connection. If the
		// agent already reconnected with a newer ControlWS, the new
		// registration bumped ControlGen, so this stale disconnect must
		// not touch the entry's ControlWS or sessions.
		entry.mu.Lock()
		stale := state.ControlGen != entry.ControlGen
		if !stale {
			entry.ControlWS = nil
			entry.LastSeen = time.Now()
		}
		var sessions map[string]*clientSession
		if !stale {
			sessions = make(map[string]*clientSession, len(entry.Sessions))
			for k, v := range entry.Sessions {
				sessions[k] = v
			}
			entry.Sessions = make(map[string]*clientSession)
		}
		entry.mu.Unlock()

		if stale {
			log.Printf("[hub] ignoring stale agent disconnect for %s (gen %d, current %d)",
				state.MachineID, state.ControlGen, entry.ControlGen)
			return
		}

		for _, sess := range sessions {
			cleanupSession(state.MachineID, sess)
			if sess.ClientWS != nil {
				sess.ClientWS.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "agent disconnected"))
				sess.ClientWS.Close()
			}
			if sess.AgentWS != nil {
				sess.AgentWS.Close()
			}
		}

	case "agent-session":
		// Agent session WS disconnected -- remove session, close its client
		sid := state.SessionID
		entry.mu.Lock()
		sess := entry.Sessions[sid]
		delete(entry.Sessions, sid)
		entry.mu.Unlock()

		if sess != nil {
			cleanupSession(state.MachineID, sess)
			if sess.ClientWS != nil {
				sess.ClientWS.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "agent session ended"))
				sess.ClientWS.Close()
			}
		}

	case "client", "helper":
		// Client disconnected -- remove session, notify agent session WS
		sid := state.SessionID
		entry.mu.Lock()
		sess := entry.Sessions[sid]
		delete(entry.Sessions, sid)
		entry.mu.Unlock()

		if sess != nil {
			cleanupSession(state.MachineID, sess)
			if sess.AgentWS != nil {
				msg, _ := json.Marshal(map[string]string{"type": "session-end"})
				sess.AgentWS.WriteMessage(websocket.TextMessage, msg)
				sess.AgentWS.Close()
			}
		}
	}
}

// cleanupSession removes UDP tokens and clears WS peer state for a session.
func cleanupSession(machineID string, sess *clientSession) {
	if sess.UDPTokens != nil {
		udpSessionsMu.Lock()
		for _, tokenHex := range sess.UDPTokens {
			delete(udpSessions, tokenHex)
		}
		udpSessionsMu.Unlock()
		log.Printf("[hub] cleaned up UDP sessions for: %s session=%s", machineID, sess.SessionID[:8])
	}

	wsStatesMu.Lock()
	if sess.AgentWS != nil {
		if as, ok := wsStates[sess.AgentWS.Conn]; ok {
			as.Paired = false
			as.Peer = nil
		}
	}
	if sess.ClientWS != nil {
		if cs, ok := wsStates[sess.ClientWS.Conn]; ok {
			cs.Paired = false
			cs.Peer = nil
		}
	}
	wsStatesMu.Unlock()
}

func sendError(ws *safeConn, message string) {
	log.Printf("[hub] error: %s", message)
	errMsg, _ := json.Marshal(map[string]string{"type": "error", "message": message})
	ws.WriteMessage(websocket.TextMessage, errMsg)
	ws.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, message))
}

func tokenLog(token string) string {
	if token != "" {
		return " (token-protected)"
	}
	return ""
}

func wgLog(key string) string {
	if key != "" {
		return " (WG)"
	}
	return ""
}

// ── UDP relay ──────────────────────────────────────────────────────

const (
	udpTokenLen = 8 // 8 bytes = 16 hex chars
	probeWord   = "PROBE"
	readyWord   = "READY"
)

// udpBufPool reuses buffers for outgoing UDP relay datagrams, avoiding
// a heap allocation per relayed datagram.
var udpBufPool = sync.Pool{
	New: func() any { return make([]byte, 0, 1500) },
}

func runUDPRelay() {
	addr := &net.UDPAddr{Port: udpPort}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Fatalf("[hub] UDP listen failed: %v", err)
	}
	log.Printf("[hub] UDP relay on port %d", udpPort)

	buf := make([]byte, 65536)
	for {
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("[hub] UDP read error: %v", err)
			continue
		}
		if n < udpTokenLen {
			continue // too short
		}

		tokenHex := hex.EncodeToString(buf[:udpTokenLen])
		payload := buf[udpTokenLen:n]

		udpSessionsMu.Lock()
		session, ok := udpSessions[tokenHex]
		if !ok {
			udpSessionsMu.Unlock()
			continue // unknown token
		}

		// Record sender's address
		session.Addr = raddr

		// Check if this is a PROBE
		if string(payload) == probeWord {
			// Send READY back: [same token]["READY"]
			resp := make([]byte, udpTokenLen+len(readyWord))
			copy(resp, buf[:udpTokenLen])
			copy(resp[udpTokenLen:], readyWord)
			conn.WriteToUDP(resp, raddr)
			log.Printf("[hub] UDP probe OK from %s (%s)", raddr, session.MachineID)
			udpSessionsMu.Unlock()
			continue
		}

		// Relay WG datagram to peer
		peer, peerOK := udpSessions[session.PeerTokenHex]
		udpSessionsMu.Unlock()

		if !peerOK {
			continue
		}

		if peer.Addr != nil {
			// Peer is on UDP -- forward via UDP
			peerTokenBytes, _ := hex.DecodeString(session.PeerTokenHex)
			relayBuf := udpBufPool.Get().([]byte)[:0]
			relayBuf = append(relayBuf, peerTokenBytes...)
			relayBuf = append(relayBuf, payload...)
			conn.WriteToUDP(relayBuf, peer.Addr)
			udpBufPool.Put(relayBuf)
		} else if peer.PeerWS != nil {
			// Peer hasn't upgraded to UDP -- fall back to WebSocket relay
			peer.PeerWS.WriteMessage(websocket.BinaryMessage, payload)
		}
	}
}

// ── Keepalive ──────────────────────────────────────────────────────

func runKeepalive() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		machinesMu.RLock()
		for _, entry := range machines {
			entry.mu.Lock()
			if entry.ControlWS != nil {
				entry.ControlWS.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
			entry.mu.Unlock()
		}
		machinesMu.RUnlock()
	}
}

// runUDPSessionReaper periodically removes orphaned UDP session entries
// that were not cleaned up by normal session teardown (e.g., if a
// session was created but the agent or client disconnected before
// pairing completed and cleanupSession never ran).
//
// Only removes entries whose corresponding machine session no longer
// exists in the machines map. Active sessions are never reaped.
func runUDPSessionReaper() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-5 * time.Minute)
		udpSessionsMu.Lock()
		for token, sess := range udpSessions {
			if sess.CreatedAt.After(cutoff) {
				continue // too recent, skip
			}
			// Check if the machine still has an active session referencing this token
			machinesMu.RLock()
			entry, exists := machines[sess.MachineID]
			machinesMu.RUnlock()
			if exists {
				entry.mu.Lock()
				active := false
				for _, cs := range entry.Sessions {
					for _, t := range cs.UDPTokens {
						if t == token {
							active = true
							break
						}
					}
					if active {
						break
					}
				}
				entry.mu.Unlock()
				if active {
					continue // session still alive, do not reap
				}
			}
			delete(udpSessions, token)
		}
		udpSessionsMu.Unlock()
	}
}

// ── Main ───────────────────────────────────────────────────────────

func printHubUsage() {
	fmt.Fprintf(os.Stderr, `telahubd -- Tela Hub Server

Combined HTTP + WebSocket + UDP relay server. Serves the hub console,
exposes status/history APIs, and relays encrypted WireGuard sessions
between agents and clients.

Usage:
  telahubd [options]                Run the hub server
  telahubd <command> [options]      Run a subcommand

Commands:
  service   Manage telahubd as an OS service (install, start, stop, etc.)
  user      Manage auth tokens (add, remove, grant, revoke, rotate)
  portal    Manage portal registrations (add, remove, list, sync)
  version   Print version and exit
  help      Show this help

Server Options:
  -config <file>   Path to YAML config file
  -v               Verbose logging (include per-message relay logs)

Environment Variables (override config file):
  TELAHUBD_PORT         HTTP+WS listen port       (default: 80)
  TELAHUBD_UDP_PORT     UDP relay port             (default: 41820)
  TELAHUBD_UDP_HOST     Explicit UDP host          (for proxied setups)
  TELAHUBD_NAME         Hub display name
  TELAHUBD_WWW_DIR      Static file directory      (default: ./www)
  TELA_OWNER_TOKEN      Bootstrap owner auth token

Portal Bootstrap:
  TELAHUBD_PORTAL_URL   Portal URL to register with on first start
  TELAHUBD_PORTAL_TOKEN Admin API token for portal registration
  TELAHUBD_PUBLIC_URL   This hub's public URL (for portal registration)

Examples:
  telahubd -config telahubd.yaml
  telahubd -config telahubd.yaml -v
  telahubd user bootstrap
  telahubd service install -config telahubd.yaml

Run 'telahubd <command>' without arguments for command-specific help.
`)
}

func main() {
	// Handle subcommands and special cases before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "service":
			handleServiceCommand()
			return
		case "user":
			handleUserCommand()
			return
		case "portal":
			handlePortalCommand()
			return
		case "version", "--version":
			fmt.Printf("telahubd %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
			os.Exit(0)
		case "help", "-h", "--help":
			printHubUsage()
			os.Exit(0)
		}
	}

	// If launched by Windows SCM, enter service mode automatically.
	if service.IsWindowsService() {
		runAsWindowsService()
		return
	}

	telelog.Init("hub", &logRingWriter{original: os.Stderr})

	// Parse flags
	flag.Usage = func() { printHubUsage() }
	configPath := flag.String("config", "", "Path to YAML config file")
	verboseFlag := flag.Bool("v", false, "Verbose logging (include per-message relay logs)")
	flag.Parse()
	verbose = *verboseFlag

	// Load config file if given, or auto-detect persisted config from a
	// previous env-var bootstrap.
	var cfg *hubConfig
	if *configPath != "" {
		absPath, _ := filepath.Abs(*configPath)
		var err error
		cfg, err = loadHubConfig(absPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		*configPath = absPath
		log.Printf("[hub] loaded config from %s", absPath)
	} else {
		// Check for previously persisted config (from env bootstrap / admin API)
		const defaultDataConfig = "data/telahubd.yaml"
		if _, err := os.Stat(defaultDataConfig); err == nil {
			cfg, _ = loadHubConfig(defaultDataConfig)
			if cfg != nil {
				*configPath = defaultDataConfig
				log.Printf("[hub] loaded persisted config from %s", defaultDataConfig)
			}
		}
	}

	// Apply config (file values, then env overrides)
	applyHubConfig(cfg)

	// Initialize the auth store (nil cfg => open/disabled).
	if cfg == nil {
		cfg = &hubConfig{Port: httpPort, UDPPort: udpPort, WWWDir: wwwDir}
	}

	// Env-var bootstrap: TELA_OWNER_TOKEN
	if bootstrapFromEnv(cfg, *configPath) {
		log.Println("[hub] auth bootstrapped from TELA_OWNER_TOKEN")
	}

	// Auto-bootstrap: if still no auth, generate an owner token.
	// Secure by default -- hubs never run in open mode unless explicitly
	// started with -open.
	if autoBootstrapAuth(cfg, *configPath) {
		log.Println("[hub] auth auto-bootstrapped (owner token generated)")
	}

	globalCfg = cfg
	globalCfgPath = *configPath

	// If no config path was specified but we have auth config (e.g. from env
	// bootstrap), default to "telahubd.yaml" in a data directory so that
	// admin API mutations and bootstrap changes persist to disk.
	if globalCfgPath == "" && len(cfg.Auth.Tokens) > 0 {
		globalCfgPath = "data/telahubd.yaml"
		os.MkdirAll("data", 0o700)
		_ = writeHubConfig(globalCfgPath, cfg)
	}

	// Ensure a console-viewer token exists for the built-in hub console.
	// This handles upgrades where auth was previously bootstrapped without one.
	if ensureConsoleViewer(cfg, globalCfgPath) {
		log.Println("[hub] created console-viewer token for hub console")
	}

	globalAuth = newAuthStore(&cfg.Auth)

	// Portal env-var bootstrap (requires globalAuth for viewer token)
	if bootstrapPortalsFromEnv(cfg, globalCfgPath) {
		log.Println("[hub] portal registered from TELAHUBD_PORTAL_URL")
	}

	runHub(nil)
}

// runHub starts the hub server and blocks until shutdown.
// If stopCh is non-nil, shutdown is triggered when it closes (service mode).
// If stopCh is nil, shutdown is triggered by SIGINT/SIGTERM (interactive mode).
// handleAdminAgents proxies management requests to agents through their
// ControlWS. The URL pattern is /api/admin/agents/{machineId}/{action}.
func handleAdminAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Parse path first to get machineID for permission check
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/agents/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "expected /api/admin/agents/{machine}/{action}"})
		return
	}
	machineID := parts[0]
	action := parts[1]

	// Check manage permission (owner/admin always allowed, or per-machine manageTokens)
	callerToken := tokenFromRequest(r)
	if !globalAuth.canManage(callerToken, machineID) {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusForbidden, map[string]string{"error": "manage permission required for " + machineID})
		return
	}

	// Find the agent's ControlWS
	machinesMu.RLock()
	entry, exists := machines[machineID]
	machinesMu.RUnlock()
	if !exists {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "machine not found"})
		return
	}
	entry.mu.Lock()
	controlWS := entry.ControlWS
	entry.mu.Unlock()
	if controlWS == nil {
		writeAdminJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "agent not connected"})
		return
	}

	// Read request payload (if POST/PUT)
	var payload json.RawMessage
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		body, err := io.ReadAll(r.Body)
		if err == nil && len(body) > 0 {
			payload = body
		}
	}

	// Generate request ID and create response channel
	reqIDBytes := make([]byte, 8)
	rand.Read(reqIDBytes)
	reqID := hex.EncodeToString(reqIDBytes)

	respCh := make(chan json.RawMessage, 1)
	mgmtPendingMu.Lock()
	mgmtPending[reqID] = respCh
	mgmtPendingMu.Unlock()
	defer func() {
		mgmtPendingMu.Lock()
		delete(mgmtPending, reqID)
		mgmtPendingMu.Unlock()
	}()

	// Send mgmt-request to agent
	log.Printf("[hub] mgmt-request: machine=%s action=%s reqId=%s", machineID, action, reqID)
	req := map[string]interface{}{
		"type":      "mgmt-request",
		"requestId": reqID,
		"action":    action,
	}
	if payload != nil {
		req["payload"] = payload
	}
	data, _ := json.Marshal(req)
	if err := controlWS.WriteMessage(websocket.TextMessage, data); err != nil {
		writeAdminJSON(w, r, http.StatusBadGateway, map[string]string{"error": "failed to send to agent"})
		return
	}

	// Wait for response with timeout
	select {
	case resp := <-respCh:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(resp)
	case <-time.After(30 * time.Second):
		writeAdminJSON(w, r, http.StatusGatewayTimeout, map[string]string{"error": "agent did not respond within 30 seconds"})
	}
}

// writeAdminJSON is a helper for admin API JSON responses.
func writeAdminJSON(w http.ResponseWriter, r *http.Request, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func runHub(stopCh <-chan struct{}) {
	// Register HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", handleAPIStatus)
	mux.HandleFunc("/status", handleAPIStatus)
	mux.HandleFunc("/api/history", handleAPIHistory)
	mux.HandleFunc("/api/admin/tokens/", handleAdminTokens) // DELETE /api/admin/tokens/{id}
	mux.HandleFunc("/api/admin/tokens", handleAdminTokens)
	mux.HandleFunc("/api/admin/acls", handleAdminACLs)
	mux.HandleFunc("/api/admin/grant", handleAdminGrant)
	mux.HandleFunc("/api/admin/revoke", handleAdminRevoke)
	mux.HandleFunc("/api/admin/grant-register", handleAdminGrantRegister)
	mux.HandleFunc("/api/admin/revoke-register", handleAdminRevokeRegister)
	mux.HandleFunc("/api/admin/rotate/", handleAdminRotate) // /api/admin/rotate/{id}
	mux.HandleFunc("/api/admin/portals/", handleAdminPortals) // DELETE /api/admin/portals/{name}
	mux.HandleFunc("/api/admin/portals", handleAdminPortals)
	mux.HandleFunc("/api/admin/grant-manage", handleAdminGrantManage)
	mux.HandleFunc("/api/admin/revoke-manage", handleAdminRevokeManage)
	mux.HandleFunc("/api/admin/pair-code", handleAdminPairCode)
	mux.HandleFunc("/api/admin/access/", handleAdminAccess)
	mux.HandleFunc("/api/admin/access", handleAdminAccess)
	mux.HandleFunc("/api/admin/agents/", handleAdminAgents)
	mux.HandleFunc("/api/admin/logs", handleAdminLogs)
	mux.HandleFunc("/api/admin/restart", handleAdminRestart)
	mux.HandleFunc("/api/admin/update", handleAdminUpdate)
	mux.HandleFunc("/api/pair", handlePair)
	mux.HandleFunc("/ws", handleWS)

	// WebSocket upgrade on root path too (agents/clients connect to /)
	// Static files are served for non-upgrade HTTP requests.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// If this is a WebSocket upgrade, handle it
		if websocket.IsWebSocketUpgrade(r) {
			handleWS(w, r)
			return
		}
		// Otherwise serve static files
		handleStatic(w, r)
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", httpPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start UDP relay
	go runUDPRelay()

	// Start keepalive pinger
	go runKeepalive()

	// Start stale UDP session reaper
	go runUDPSessionReaper()

	// Clean up expired pairing codes every minute
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			globalPairStore.CleanupExpiredCodes()
		}
	}()

	// Sync viewer token to portals after a short delay
	go func() {
		time.Sleep(2 * time.Second)
		syncViewerTokenToPortals()
	}()

	// Graceful shutdown
	if stopCh != nil {
		// Service mode: stop when SCM/systemd signals
		go func() {
			<-stopCh
			log.Println("[hub] service stop received, shutting down")
			server.Close()
		}()
	} else {
		// Interactive mode: stop on SIGINT/SIGTERM
		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			sig := <-sigCh
			log.Printf("[hub] received %v, shutting down", sig)
			server.Close()
		}()
	}

	log.Printf("[hub] telahubd %s listening on http+ws://0.0.0.0:%d", version, httpPort)
	removeControl := writeHubControlFile()
	defer removeControl()
	if !globalAuth.isEnabled() {
		log.Println("[hub] WARNING *** Hub running in OPEN mode -- any client may connect and register ***")
		log.Println("[hub] WARNING *** Set TELA_OWNER_TOKEN or use a config file to enable auth ***")
	}
	if wwwDirOverride {
		log.Printf("[hub] static site: %s (disk override)", wwwDir)
	} else {
		log.Printf("[hub] static site: embedded")
	}
	if hubName != "" {
		log.Printf("[hub] hub name: %s", hubName)
	}
	if udpHost != "" {
		log.Printf("[hub] UDP host override: %s", udpHost)
	}
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[hub] server error: %v", err)
	}
	log.Println("[hub] stopped")
}

// ── Service management ─────────────────────────────────────────────

func handleServiceCommand() {
	if len(os.Args) < 3 {
		cfgPath := service.BinaryConfigPath("telahubd")
		fmt.Fprintf(os.Stderr, `telahubd service -- manage telahubd as an OS service

Usage:
  telahubd service install [flags]  Install service (generates config in system dir)
  telahubd service uninstall        Remove the service
  telahubd service start            Start the installed service
  telahubd service stop             Stop the running service
  telahubd service restart          Restart the service
  telahubd service status           Show service status
  telahubd service run              Run in service mode (used by the service manager)

The service reads its configuration from:
  %s

Edit that file and run "telahubd service restart" to reconfigure.

Install examples:
  telahubd service install -config telahubd.yaml
  telahubd service install -name myhub -port 80 -udp-port 41820 -www ./www
`, cfgPath)
		os.Exit(1)
	}

	subcmd := os.Args[2]

	switch subcmd {
	case "install":
		hubServiceInstall()
	case "uninstall":
		hubServiceUninstall()
	case "start":
		hubServiceStart()
	case "stop":
		hubServiceStop()
	case "restart":
		hubServiceRestart()
	case "status":
		hubServiceStatus()
	case "run":
		hubServiceRun()
	default:
		fmt.Fprintf(os.Stderr, "unknown service subcommand: %s\n", subcmd)
		os.Exit(1)
	}
}

func hubServiceInstall() {
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	cfgFile := fs.String("config", "", "Path to YAML config file (optional -- generates one if omitted)")
	name := fs.String("name", "", "Hub display name")
	port := fs.Int("port", 80, "HTTP+WS listen port")
	udpPortFlag := fs.Int("udp-port", 41820, "UDP relay port")
	www := fs.String("www", "./www", "Static file directory")
	fs.Parse(os.Args[3:])

	destPath := service.BinaryConfigPath("telahubd")

	if *cfgFile != "" {
		// Copy existing config to system dir
		absConfig, _ := filepath.Abs(*cfgFile)
		if _, err := loadHubConfig(absConfig); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := copyFile(absConfig, destPath); err != nil {
			fmt.Fprintf(os.Stderr, "error copying config: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Generate a YAML config from flags
		wwwAbs, _ := filepath.Abs(*www)
		cfg := hubConfig{
			Port:    *port,
			UDPPort: *udpPortFlag,
			Name:    *name,
			WWWDir:  wwwAbs,
		}
		if err := writeHubConfig(destPath, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
			os.Exit(1)
		}
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	exePath, _ = filepath.Abs(exePath)

	wd, _ := os.Getwd()
	svcCfg := &service.Config{
		BinaryPath:  exePath,
		Description: "Tela Hub Server -- encrypted tunnel relay",
		WorkingDir:  wd,
	}

	if err := service.Install("telahubd", svcCfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telahubd service installed successfully")
	fmt.Printf("  config: %s\n", destPath)
	fmt.Println("  start:  telahubd service start")
	fmt.Println("")
	fmt.Println("Edit the config file and run \"telahubd service restart\" to reconfigure.")
}

// writeHubConfig writes a telahubd YAML config file.
func writeHubConfig(path string, cfg *hubConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), service.ConfigDirPerm()); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	header := "# telahubd configuration\n# Edit and restart the service to apply changes.\n\n"
	if err := os.WriteFile(path, []byte(header+string(data)), service.ConfigFilePerm()); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
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

func hubServiceUninstall() {
	if err := service.Uninstall("telahubd"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telahubd service uninstalled")
	fmt.Printf("  config retained: %s\n", service.BinaryConfigPath("telahubd"))
}

func hubServiceStart() {
	if err := service.Start("telahubd"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telahubd service started")
}

func hubServiceStop() {
	if err := service.Stop("telahubd"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telahubd service stopped")
}

func hubServiceRestart() {
	fmt.Println("stopping telahubd service...")
	_ = service.Stop("telahubd")
	time.Sleep(time.Second)
	if err := service.Start("telahubd"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telahubd service restarted")
}

func hubServiceStatus() {
	st, err := service.QueryStatus("telahubd")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("installed: %v\n", st.Installed)
	fmt.Printf("running:   %v\n", st.Running)
	fmt.Printf("status:    %s\n", st.Info)
	if st.Installed {
		fmt.Printf("config:    %s\n", service.BinaryConfigPath("telahubd"))
	}
}

// serviceRunHub loads the YAML config from the system directory and
// starts the hub. It blocks until stopCh is closed.
func serviceRunHub(stopCh <-chan struct{}) {
	// Redirect log output to a file when running as a Windows service.
	logDest := io.Writer(os.Stderr)
	if runtime.GOOS == "windows" && service.IsWindowsService() {
		logPath := service.LogPath("telahubd")
		lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, service.ConfigFilePerm())
		if err == nil {
			logDest = lf
			os.Stderr = lf
		}
	}
	telelog.Init("hub", &logRingWriter{original: logDest})

	svcCfg, err := service.LoadConfig("telahubd")
	if err != nil {
		log.Fatalf("service config: %v", err)
	}

	if svcCfg.WorkingDir != "" {
		os.Chdir(svcCfg.WorkingDir)
	}

	// Load the YAML config from the system-wide location
	yamlPath := service.BinaryConfigPath("telahubd")
	cfg, err := loadHubConfig(yamlPath)
	if err != nil {
		log.Fatalf("config %s: %v", yamlPath, err)
	}

	log.Printf("[hub] loaded config from %s", yamlPath)
	applyHubConfig(cfg)

	// Env-var bootstrap: TELA_OWNER_TOKEN
	if bootstrapFromEnv(cfg, yamlPath) {
		log.Println("[hub] auth bootstrapped from TELA_OWNER_TOKEN")
	}

	// Auto-bootstrap: if still no auth, generate an owner token.
	if autoBootstrapAuth(cfg, yamlPath) {
		log.Println("[hub] auth auto-bootstrapped (owner token generated)")
	}

	globalCfg = cfg
	globalCfgPath = yamlPath

	// Ensure a console-viewer token exists for the built-in hub console.
	if ensureConsoleViewer(cfg, yamlPath) {
		log.Println("[hub] created console-viewer token for hub console")
	}

	globalAuth = newAuthStore(&cfg.Auth)

	// Portal env-var bootstrap (requires globalAuth for viewer token)
	if bootstrapPortalsFromEnv(cfg, yamlPath) {
		log.Println("[hub] portal registered from TELAHUBD_PORTAL_URL")
	}

	runHub(stopCh)
}

func hubServiceRun() {
	stopCh := make(chan struct{})

	// Handle signals for non-Windows "service run" (systemd/launchd)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		close(stopCh)
	}()

	serviceRunHub(stopCh)
}

func runAsWindowsService() {
	handler := &service.Handler{
		Run: func(svcStopCh <-chan struct{}) {
			serviceRunHub(svcStopCh)
		},
	}
	if err := service.RunAsService("telahubd", handler); err != nil {
		log.Fatalf("service failed: %v", err)
	}
}

// ── Portal management subcommand ───────────────────────────────────

// telaWellKnown is the JSON shape returned by /.well-known/tela (RFC 8615).
type telaWellKnown struct {
	HubDirectory string `json:"hub_directory"`
}

// discoverHubDirectory queries /.well-known/tela to discover the hub directory
// endpoint. Returns the path (e.g. "/api/hubs") or falls back to the conventional
// default if the well-known endpoint is not available.
func discoverHubDirectory(baseURL string, token string) string {
	const fallback = "/api/hubs"
	wkURL := strings.TrimRight(baseURL, "/") + "/.well-known/tela"

	req, err := http.NewRequest("GET", wkURL, nil)
	if err != nil {
		return fallback
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fallback
	}

	var wk telaWellKnown
	if err := json.NewDecoder(resp.Body).Decode(&wk); err != nil {
		return fallback
	}

	if wk.HubDirectory == "" {
		return fallback
	}
	return wk.HubDirectory
}

// syncViewerTokenToPortals pushes the current console-viewer token to all
// registered portals that have a sync token. Safe to call from goroutines.
func syncViewerTokenToPortals() {
	globalCfgMu.Lock()
	portals := make(map[string]portalEntry)
	for k, v := range globalCfg.Portals {
		portals[k] = v
	}
	name := globalCfg.Name
	globalCfgMu.Unlock()

	if name == "" {
		name = hubName
	}
	if name == "" {
		log.Println("[hub] portal sync: no hub name configured, skipping")
		return
	}

	viewerToken := globalAuth.consoleViewerToken()
	if viewerToken == "" {
		return
	}

	for pName, p := range portals {
		if p.SyncToken == "" {
			log.Printf("[hub] portal %q: no sync token, skipping (re-register to enable auto-sync)", pName)
			continue
		}
		syncURL := p.URL + p.HubDirectory + "/sync"
		payload, _ := json.Marshal(map[string]string{
			"name":        name,
			"viewerToken": viewerToken,
		})

		req, err := http.NewRequest("PATCH", syncURL, strings.NewReader(string(payload)))
		if err != nil {
			log.Printf("[hub] portal %q sync: request error: %v", pName, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.SyncToken)

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[hub] portal %q sync: %v", pName, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == 200 {
			log.Printf("[hub] portal %q: viewer token synced", pName)
		} else {
			log.Printf("[hub] portal %q sync: HTTP %d", pName, resp.StatusCode)
		}
	}
}

// registerResult holds the outcome of a portal registration.
type registerResult struct {
	Entry   portalEntry
	Updated bool // true if the portal already had an entry for this hub (upsert)
}

// registerWithPortal performs hub directory discovery and registers the hub
// with a portal. adminToken is used for the registration POST only and is
// NOT included in the returned entry (security: never persisted).
// Returns a registerResult with the portalEntry to save (containing only
// the sync token) and whether the registration was an update.
func registerWithPortal(portalURL, adminToken, regHubName, regHubURL, viewerToken string) (registerResult, error) {
	portalURL = strings.TrimRight(portalURL, "/")
	if !strings.HasPrefix(portalURL, "http://") && !strings.HasPrefix(portalURL, "https://") {
		portalURL = "https://" + portalURL
	}

	// Discover hub directory endpoint via /.well-known/tela (RFC 8615)
	hubDirectory := discoverHubDirectory(portalURL, adminToken)

	// Build registration payload
	payload := map[string]string{
		"name": regHubName,
		"url":  regHubURL,
	}
	if viewerToken != "" {
		payload["viewerToken"] = viewerToken
	}

	payloadBytes, _ := json.Marshal(payload)
	apiURL := portalURL + hubDirectory

	req, err := http.NewRequest("POST", apiURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return registerResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+adminToken)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return registerResult{}, fmt.Errorf("could not reach %s: %w", portalURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var respData struct {
		Updated   bool   `json:"updated"`
		SyncToken string `json:"syncToken"`
	}

	if resp.StatusCode == 401 {
		return registerResult{}, fmt.Errorf("unauthorized. Check your API token")
	}
	if resp.StatusCode == 409 {
		// Hub already registered -- older portals that don't support upsert
		respData.Updated = true
	} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return registerResult{}, fmt.Errorf("portal returned HTTP %d: %s", resp.StatusCode, string(body))
	} else {
		json.Unmarshal(body, &respData)
	}

	entry := portalEntry{
		URL:          portalURL,
		SyncToken:    respData.SyncToken,
		HubDirectory: hubDirectory,
	}
	if respData.SyncToken == "" && adminToken != "" {
		// Old portal that doesn't issue sync tokens -- keep admin token for compat
		entry.Token = adminToken
	}

	return registerResult{Entry: entry, Updated: respData.Updated}, nil
}

// bootstrapPortalsFromEnv checks for TELAHUBD_PORTAL_URL. If the hub has no
// portals configured and the env var is set, it registers with the portal
// and writes the config. Returns true if bootstrap occurred.
// Requires globalAuth to be initialized (needs viewer token).
func bootstrapPortalsFromEnv(cfg *hubConfig, cfgPath string) bool {
	portalURL := os.Getenv("TELAHUBD_PORTAL_URL")
	if portalURL == "" {
		return false
	}
	if len(cfg.Portals) > 0 {
		// Already have portal(s) -- env var is ignored (same pattern as auth bootstrap).
		return false
	}

	portalToken := os.Getenv("TELAHUBD_PORTAL_TOKEN") // admin token for registration (not persisted)
	hubURL := os.Getenv("TELAHUBD_PUBLIC_URL")
	regHubName := cfg.Name
	if regHubName == "" {
		regHubName = envStr("TELAHUBD_NAME", "")
	}

	if hubURL == "" || regHubName == "" {
		log.Println("[hub] portal bootstrap: TELAHUBD_PUBLIC_URL and hub name (TELAHUBD_NAME or config) required, skipping")
		return false
	}

	viewerToken := globalAuth.consoleViewerToken()

	result, err := registerWithPortal(portalURL, portalToken, regHubName, hubURL, viewerToken)
	if err != nil {
		log.Printf("[hub] portal bootstrap: %v", err)
		return false
	}

	if cfg.Portals == nil {
		cfg.Portals = make(map[string]portalEntry)
	}
	cfg.Portals["default"] = result.Entry

	if cfgPath != "" {
		_ = writeHubConfig(cfgPath, cfg)
	}
	return true
}

// handlePortalCommand implements "telahubd portal <subcmd>".
func handlePortalCommand() {
	if len(os.Args) < 3 {
		printPortalUsage()
		os.Exit(1)
	}
	subcmd := os.Args[2]
	switch subcmd {
	case "add":
		cmdPortalAdd()
	case "remove", "rm":
		cmdPortalRemove()
	case "list", "ls":
		cmdPortalList()
	case "sync":
		cmdPortalSync()
	default:
		fmt.Fprintf(os.Stderr, "unknown portal subcommand: %s\n", subcmd)
		printPortalUsage()
		os.Exit(1)
	}
}

func printPortalUsage() {
	fmt.Fprintf(os.Stderr, `telahubd portal -- manage portal registrations

Register this hub with one or more Tela portals (like Awan Saya) so that
users who query the portal can discover it.

Usage:
  telahubd portal add <name> <url> [-config <path>]    Register with a portal
  telahubd portal remove <name> [-config <path>]        Unregister from a portal
  telahubd portal list [-config <path>] [-json]          List portal registrations
  telahubd portal sync [-config <path>]                  Push viewer token to all portals

Examples:
  telahubd portal add awansaya https://awansaya.net
  telahubd portal remove awansaya
  telahubd portal list
  telahubd portal list -json
`)
}

// portalCmdConfigPathDefault returns the default config file path for portal commands.
func portalCmdConfigPathDefault() string {
	// Check for persisted config in data dir
	const dataConfig = "data/telahubd.yaml"
	if _, err := os.Stat(dataConfig); err == nil {
		return dataConfig
	}
	return service.BinaryConfigPath("telahubd")
}

func cmdPortalAdd() {
	fs := flag.NewFlagSet("portal add", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: telahubd portal add <name> <url> [-config <path>]")
		fmt.Fprintln(os.Stderr, "Example: telahubd portal add awansaya https://awansaya.net")
		os.Exit(1)
	}

	name := fs.Arg(0)
	portalURL := strings.TrimRight(fs.Arg(1), "/")

	// Validate URL
	if !strings.HasPrefix(portalURL, "http://") && !strings.HasPrefix(portalURL, "https://") {
		portalURL = "https://" + portalURL
	}

	// Prompt for API token
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("API token for %s (press Enter for none): ", portalURL)
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)

	// Load config
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = portalCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	applyHubConfig(cfg)

	if cfg.Portals == nil {
		cfg.Portals = make(map[string]portalEntry)
	}

	// Ensure a console-viewer token exists (handles configs from older bootstraps).
	if ensureConsoleViewer(cfg, cfgPath) {
		fmt.Println("Created console-viewer token.")
	}

	// Determine hub name for registration
	regHubName := cfg.Name
	if regHubName == "" {
		regHubName = hubName
	}
	if regHubName == "" {
		fmt.Print("Hub name for registration: ")
		regHubName, _ = reader.ReadString('\n')
		regHubName = strings.TrimSpace(regHubName)
	}

	// Determine hub URL for registration
	regHubURL := ""
	fmt.Printf("Hub public URL (e.g. https://gohub.example.com): ")
	regHubURL, _ = reader.ReadString('\n')
	regHubURL = strings.TrimSpace(regHubURL)
	if regHubURL == "" {
		fmt.Fprintln(os.Stderr, "Error: hub URL is required for portal registration")
		os.Exit(1)
	}

	// Build the viewer token to include if the hub has auth
	viewerToken := ""
	if len(cfg.Auth.Tokens) > 0 {
		for _, t := range cfg.Auth.Tokens {
			if t.ID == "console-viewer" {
				viewerToken = t.Token
				break
			}
		}
	}

	// Register with the portal using the shared function
	fmt.Printf("Registering %q at %s... ", regHubName, portalURL)
	result, err := registerWithPortal(portalURL, token, regHubName, regHubURL, viewerToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	if result.Updated {
		fmt.Println("updated")
	} else {
		fmt.Println("ok")
	}

	if result.Entry.SyncToken != "" {
		fmt.Println("Sync token received -- admin token will not be stored in config")
	} else if token != "" {
		fmt.Println("Warning: portal did not return a sync token -- storing admin token (upgrade portal to enable auto-sync)")
	}

	cfg.Portals[name] = result.Entry

	if err := writeHubConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Portal %q added (%s)\n", name, portalURL)
	fmt.Printf("Config saved to %s\n", cfgPath)
}

func cmdPortalRemove() {
	fs := flag.NewFlagSet("portal remove", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: telahubd portal remove <name> [-config <path>]")
		os.Exit(1)
	}

	name := fs.Arg(0)
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = portalCmdConfigPathDefault()
	}

	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if cfg.Portals == nil || cfg.Portals[name] == (portalEntry{}) {
		fmt.Fprintf(os.Stderr, "Error: portal %q not found\n", name)
		os.Exit(1)
	}

	// Optionally deregister from the portal (best-effort)
	portal := cfg.Portals[name]
	if portal.URL != "" && portal.Token != "" && cfg.Name != "" {
		hubDir := portal.HubDirectory
		if hubDir == "" {
			hubDir = "/api/hubs"
		}
		apiURL := portal.URL + hubDir + "?name=" + cfg.Name
		req, _ := http.NewRequest("DELETE", apiURL, nil)
		if req != nil {
			req.Header.Set("Authorization", "Bearer "+portal.Token)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					fmt.Printf("Deregistered from %s\n", portal.URL)
				}
			}
		}
	}

	delete(cfg.Portals, name)

	if err := writeHubConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Portal %q removed.\n", name)
}

func cmdPortalList() {
	fs := flag.NewFlagSet("portal list", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = portalCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if cfg.Portals == nil || len(cfg.Portals) == 0 {
		fmt.Println("No portals configured.")
		fmt.Println("Add one with: telahubd portal add <name> <url>")
		return
	}

	// Sort for deterministic output
	var names []string
	for n := range cfg.Portals {
		names = append(names, n)
	}
	sort.Strings(names)

	if *asJSON {
		type entry struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Token string `json:"token"`
		}
		var entries []entry
		for _, n := range names {
			p := cfg.Portals[n]
			tokenDisplay := ""
			if p.Token != "" {
				if len(p.Token) > 4 {
					tokenDisplay = "****" + p.Token[len(p.Token)-4:]
				} else {
					tokenDisplay = "****"
				}
			}
			entries = append(entries, entry{Name: n, URL: p.URL, Token: tokenDisplay})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(entries)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tURL\tTOKEN")
	for _, n := range names {
		p := cfg.Portals[n]
		tokenDisplay := "(none)"
		if p.Token != "" {
			if len(p.Token) > 4 {
				tokenDisplay = "****" + p.Token[len(p.Token)-4:]
			} else {
				tokenDisplay = "****"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", n, p.URL, tokenDisplay)
	}
	w.Flush()
}

func cmdPortalSync() {
	fs := flag.NewFlagSet("portal sync", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = portalCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	applyHubConfig(cfg)

	if cfg.Portals == nil || len(cfg.Portals) == 0 {
		fmt.Println("No portals configured.")
		fmt.Println("Add one with: telahubd portal add <name> <url>")
		return
	}

	// Ensure a console-viewer token exists (handles configs from older bootstraps).
	if ensureConsoleViewer(cfg, cfgPath) {
		fmt.Println("Created console-viewer token.")
	}

	// Load config into globals so syncViewerTokenToPortals can access them
	globalCfgMu.Lock()
	globalCfg = cfg
	globalCfgMu.Unlock()
	globalAuth = newAuthStore(&cfg.Auth)

	viewerToken := globalAuth.consoleViewerToken()
	if viewerToken == "" {
		fmt.Println("No viewer token configured -- nothing to sync.")
		return
	}

	name := cfg.Name
	if name == "" {
		name = hubName
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: no hub name configured (set 'name' in config or TELAHUBD_NAME env)")
		os.Exit(1)
	}

	fmt.Printf("Syncing viewer token for %q to %d portal(s)...\n", name, len(cfg.Portals))
	syncViewerTokenToPortals()
	fmt.Println("Done.")
}

// ── User / auth management subcommand ──────────────────────────────

// handleUserCommand implements "telahubd user <subcmd>".
func handleUserCommand() {
	if len(os.Args) < 3 {
		printUserUsage()
		os.Exit(1)
	}
	subcmd := os.Args[2]
	switch subcmd {
	case "bootstrap":
		cmdUserBootstrap()
	case "list":
		cmdUserList()
	case "show-owner":
		cmdUserShowOwner()
	case "show-viewer":
		cmdUserShowViewer()
	case "add":
		cmdUserAdd()
	case "remove":
		cmdUserRemove()
	case "grant":
		cmdUserGrant()
	case "revoke":
		cmdUserRevoke()
	case "rotate":
		cmdUserRotate()
	default:
		fmt.Fprintf(os.Stderr, "unknown user subcommand: %s\n", subcmd)
		printUserUsage()
		os.Exit(1)
	}
}

func printUserUsage() {
	fmt.Fprintf(os.Stderr, `telahubd user -- manage auth tokens

Usage:
  telahubd user bootstrap [-config <path>]         First-run: generate owner token
  telahubd user show-owner [-config <path>]          Print the owner token (for scripting)
  telahubd user show-viewer [-config <path>]         Print the viewer token (for scripting)
  telahubd user list [-config <path>] [-json]       List all token identities
  telahubd user add <id> [-config <path>] [-role owner|admin]
                                                     Add a new token identity
  telahubd user remove <id> [-config <path>]        Remove a token identity
  telahubd user grant <id> <machineId> [-config <path>]
                                                     Grant connect access to a machine
  telahubd user revoke <id> <machineId> [-config <path>]
                                                     Revoke connect access to a machine
  telahubd user rotate <id> [-config <path>]        Regenerate token for identity
`)
}

// userCmdConfigPathDefault returns the default config file path.
func userCmdConfigPathDefault() string {
	return service.BinaryConfigPath("telahubd")
}

// loadOrCreateHubConfig reads an existing config or returns a blank one
// if the file doesn't exist.
func loadOrCreateHubConfig(path string) (*hubConfig, error) {
	cfg, err := loadHubConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &hubConfig{
				Port:    80,
				UDPPort: 41820,
				WWWDir:  "./www",
			}, nil
		}
		return nil, err
	}
	return cfg, nil
}

// saveUserConfig writes the hub config back to disk.
func saveUserConfig(path string, cfg *hubConfig) error {
	return writeHubConfig(path, cfg)
}

// findTokenEntry returns a pointer to the tokenEntry with the given ID, or nil.
func findTokenEntry(cfg *hubConfig, id string) *tokenEntry {
	for i := range cfg.Auth.Tokens {
		if cfg.Auth.Tokens[i].ID == id {
			return &cfg.Auth.Tokens[i]
		}
	}
	return nil
}

func cmdUserBootstrap() {
	fs := flag.NewFlagSet("user bootstrap", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Refuse if tokens already exist
	if len(cfg.Auth.Tokens) > 0 {
		fmt.Fprintln(os.Stderr, "error: auth is already bootstrapped (tokens exist in config)")
		fmt.Fprintln(os.Stderr, "Use 'telahubd user add' to create additional identities.")
		os.Exit(1)
	}

	token, err := generateToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Generate a viewer token for the built-in hub console.
	viewerToken, err := generateToken()
	if err != nil {
		viewerToken = "" // non-fatal; console will show empty
	}

	cfg.Auth.Tokens = []tokenEntry{
		{ID: "owner", Token: token, HubRole: "owner"},
	}
	if viewerToken != "" {
		cfg.Auth.Tokens = append(cfg.Auth.Tokens, tokenEntry{
			ID: "console-viewer", Token: viewerToken, HubRole: "viewer",
		})
	}
	// Default machine ACL: wildcard allows the owner token to connect everywhere
	if cfg.Auth.Machines == nil {
		cfg.Auth.Machines = make(map[string]machineACL)
	}
	cfg.Auth.Machines["*"] = machineACL{
		ConnectTokens: []string{token},
	}

	if err := saveUserConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Auth bootstrapped successfully.")
	fmt.Printf("  Config:  %s\n", cfgPath)
	fmt.Printf("  Owner token: %s\n", token)
	fmt.Println()
	fmt.Println("SAVE THIS TOKEN -- it will not be shown again.")
	fmt.Println("Restart the hub to activate auth enforcement.")
}

func cmdUserList() {
	fs := flag.NewFlagSet("user list", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.Auth.Tokens) == 0 {
		fmt.Println("No auth tokens configured. Run 'telahubd user bootstrap' to get started.")
		return
	}

	if *asJSON {
		type entry struct {
			ID           string `json:"id"`
			Role         string `json:"role"`
			TokenPreview string `json:"tokenPreview"`
		}
		var entries []entry
		for _, t := range cfg.Auth.Tokens {
			role := t.HubRole
			if role == "" {
				role = "user"
			}
			preview := t.Token
			if len(preview) > 8 {
				preview = preview[:8] + "..."
			}
			entries = append(entries, entry{ID: t.ID, Role: role, TokenPreview: preview})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(entries)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tROLE\tTOKEN (first 8)")
	for _, t := range cfg.Auth.Tokens {
		role := t.HubRole
		if role == "" {
			role = "user"
		}
		preview := t.Token
		if len(preview) > 8 {
			preview = preview[:8] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", t.ID, role, preview)
	}
	w.Flush()
}

func cmdUserShowOwner() {
	fs := flag.NewFlagSet("user show-owner", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for _, t := range cfg.Auth.Tokens {
		if t.HubRole == "owner" {
			fmt.Println(t.Token)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "no owner token found")
	os.Exit(1)
}

func cmdUserShowViewer() {
	fs := flag.NewFlagSet("user show-viewer", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for _, t := range cfg.Auth.Tokens {
		if t.HubRole == "viewer" {
			fmt.Println(t.Token)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "no viewer token found")
	os.Exit(1)
}

func cmdUserAdd() {
	fs := flag.NewFlagSet("user add", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	role := fs.String("role", "", "Role: owner, admin, or omit for user")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user add <id> [-config <path>] [-role owner|admin]")
		os.Exit(1)
	}
	id := fs.Arg(0)

	if *role != "" && *role != "owner" && *role != "admin" {
		fmt.Fprintf(os.Stderr, "error: role must be 'owner' or 'admin' (omit for regular user)\n")
		os.Exit(1)
	}

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if findTokenEntry(cfg, id) != nil {
		fmt.Fprintf(os.Stderr, "error: identity '%s' already exists\n", id)
		os.Exit(1)
	}

	token, err := generateToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	cfg.Auth.Tokens = append(cfg.Auth.Tokens, tokenEntry{
		ID:      id,
		Token:   token,
		HubRole: *role,
	})

	if err := saveUserConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Added identity '%s'", id)
	if *role != "" {
		fmt.Printf(" (role: %s)", *role)
	}
	fmt.Println()
	fmt.Printf("  Token: %s\n", token)
	fmt.Println()
	fmt.Println("SAVE THIS TOKEN -- it will not be shown again.")
	fmt.Println("Restart the hub to apply changes.")
}

func cmdUserRemove() {
	fs := flag.NewFlagSet("user remove", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user remove <id> [-config <path>]")
		os.Exit(1)
	}
	id := fs.Arg(0)

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	found := false
	var removedToken string
	filtered := make([]tokenEntry, 0, len(cfg.Auth.Tokens))
	for _, t := range cfg.Auth.Tokens {
		if t.ID == id {
			found = true
			removedToken = t.Token
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "error: identity '%s' not found\n", id)
		os.Exit(1)
	}
	cfg.Auth.Tokens = filtered

	// Remove the token from any machine ACL connectTokens / registerToken
	for name, acl := range cfg.Auth.Machines {
		changed := false
		if acl.RegisterToken == removedToken {
			acl.RegisterToken = ""
			changed = true
		}
		newCT := make([]string, 0, len(acl.ConnectTokens))
		for _, ct := range acl.ConnectTokens {
			if ct == removedToken {
				changed = true
				continue
			}
			newCT = append(newCT, ct)
		}
		if changed {
			acl.ConnectTokens = newCT
			cfg.Auth.Machines[name] = acl
		}
	}

	if err := saveUserConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed identity '%s' and cleaned up machine ACLs.\n", id)
	fmt.Println("Restart the hub to apply changes.")
}

func cmdUserGrant() {
	fs := flag.NewFlagSet("user grant", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user grant <id> <machineId> [-config <path>]")
		os.Exit(1)
	}
	id := fs.Arg(0)
	machineID := fs.Arg(1)

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	entry := findTokenEntry(cfg, id)
	if entry == nil {
		fmt.Fprintf(os.Stderr, "error: identity '%s' not found\n", id)
		os.Exit(1)
	}

	if cfg.Auth.Machines == nil {
		cfg.Auth.Machines = make(map[string]machineACL)
	}
	acl := cfg.Auth.Machines[machineID]

	// Check if already granted
	for _, ct := range acl.ConnectTokens {
		if ct == entry.Token {
			fmt.Printf("Identity '%s' already has connect access to '%s'.\n", id, machineID)
			return
		}
	}
	acl.ConnectTokens = append(acl.ConnectTokens, entry.Token)
	cfg.Auth.Machines[machineID] = acl

	if err := saveUserConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Granted '%s' connect access to '%s'.\n", id, machineID)
	fmt.Println("Restart the hub to apply changes.")
}

func cmdUserRevoke() {
	fs := flag.NewFlagSet("user revoke", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user revoke <id> <machineId> [-config <path>]")
		os.Exit(1)
	}
	id := fs.Arg(0)
	machineID := fs.Arg(1)

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	entry := findTokenEntry(cfg, id)
	if entry == nil {
		fmt.Fprintf(os.Stderr, "error: identity '%s' not found\n", id)
		os.Exit(1)
	}

	acl, exists := cfg.Auth.Machines[machineID]
	if !exists {
		fmt.Fprintf(os.Stderr, "error: no ACL entry for machine '%s'\n", machineID)
		os.Exit(1)
	}

	found := false
	newCT := make([]string, 0, len(acl.ConnectTokens))
	for _, ct := range acl.ConnectTokens {
		if ct == entry.Token {
			found = true
			continue
		}
		newCT = append(newCT, ct)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "error: identity '%s' does not have connect access to '%s'\n", id, machineID)
		os.Exit(1)
	}
	acl.ConnectTokens = newCT
	cfg.Auth.Machines[machineID] = acl

	if err := saveUserConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Revoked '%s' connect access to '%s'.\n", id, machineID)
	fmt.Println("Restart the hub to apply changes.")
}

func cmdUserRotate() {
	fs := flag.NewFlagSet("user rotate", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(permuteArgs(fs, os.Args[3:]))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user rotate <id> [-config <path>]")
		os.Exit(1)
	}
	id := fs.Arg(0)

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = userCmdConfigPathDefault()
	}
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	entry := findTokenEntry(cfg, id)
	if entry == nil {
		fmt.Fprintf(os.Stderr, "error: identity '%s' not found\n", id)
		os.Exit(1)
	}

	oldToken := entry.Token
	newToken, err := generateToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	entry.Token = newToken

	// Update machine ACLs that reference the old token
	for name, acl := range cfg.Auth.Machines {
		changed := false
		if acl.RegisterToken == oldToken {
			acl.RegisterToken = newToken
			changed = true
		}
		for i, ct := range acl.ConnectTokens {
			if ct == oldToken {
				acl.ConnectTokens[i] = newToken
				changed = true
			}
		}
		if changed {
			cfg.Auth.Machines[name] = acl
		}
	}

	if err := saveUserConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Rotated token for '%s'.\n", id)
	fmt.Printf("  New token: %s\n", newToken)
	fmt.Println()
	fmt.Println("SAVE THIS TOKEN -- it will not be shown again.")
	fmt.Println("Restart the hub to apply changes.")
}

// permuteArgs reorders args so that flags come before positional arguments,
// allowing users to write "telahubd user add myid -config path" instead of
// requiring flags before positional args.
func permuteArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 0 && a[0] == '-' {
			flags = append(flags, a)
			if !containsEquals(a) && i+1 < len(args) {
				name := a
				for len(name) > 0 && name[0] == '-' {
					name = name[1:]
				}
				if f := fs.Lookup(name); f != nil {
					if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !bf.IsBoolFlag() {
						i++
						flags = append(flags, args[i])
					}
				} else {
					i++
					if i < len(args) {
						flags = append(flags, args[i])
					}
				}
			}
		} else {
			positional = append(positional, a)
		}
	}
	return append(flags, positional...)
}

func containsEquals(s string) bool {
	for _, c := range s {
		if c == '=' {
			return true
		}
	}
	return false
}

// telahubd — Go Tela Hub
//
// Drop-in replacement for poc/hub.js. Combined HTTP + WebSocket + UDP relay
// server on a single port. Serves the hub console (static files), exposes
// /api/status and /api/history with permissive CORS, and relays paired
// WireGuard sessions between agents and clients.
//
// Configuration (in order of precedence, highest first):
//   1. Environment variables  (HUB_PORT, HUB_UDP_PORT, HUB_NAME, HUB_WWW_DIR)
//   2. YAML config file       (-config telahubd.yaml)
//   3. Built-in defaults      (port 8080, udpPort 41820, wwwDir ./www)
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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"

	"github.com/paulmooreparks/tela/internal/service"
)

// version is set by -ldflags at build time.
var version = "dev"

// ── Configuration ──────────────────────────────────────────────────

// hubConfig is the YAML configuration for telahubd.
type hubConfig struct {
	Port      int        `yaml:"port"`              // HTTP+WS listen port (default 8080)
	UDPPort   int        `yaml:"udpPort"`           // UDP relay port (default 41820)
	Name      string     `yaml:"name"`              // Display name for this hub
	WWWDir    string     `yaml:"wwwDir"`            // Static file directory (default ./www)
	PublicURL string     `yaml:"publicURL"`         // Externally reachable URL (for portal registration)
	PortalURL string     `yaml:"portalURL"`         // Awan Satu portal URL (self-registration target)
	Auth      authConfig `yaml:"auth,omitempty"`    // Token-based access control
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
		if cfg.Name != "" {
			hubName = cfg.Name
		}
		if cfg.WWWDir != "" {
			wwwDir = cfg.WWWDir
		}
		if cfg.PublicURL != "" {
			publicURL = cfg.PublicURL
		}
		if cfg.PortalURL != "" {
			portalURL = cfg.PortalURL
		}
	}

	// Env vars override config file
	httpPort = envInt("HUB_PORT", httpPort)
	udpPort = envInt("HUB_UDP_PORT", udpPort)
	hubName = envStr("HUB_NAME", hubName)
	wwwDir = envStr("HUB_WWW_DIR", wwwDir)
	publicURL = envStr("HUB_PUBLIC_URL", publicURL)
	portalURL = envStr("HUB_PORTAL_URL", portalURL)
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
	httpPort    = 8080
	udpPort     = 41820
	hubName     = ""
	wwwDir      = "./www"
	publicURL   = ""  // externally reachable URL (for portal registration)
	portalURL   = ""  // Awan Satu portal URL (self-registration target)
	globalAuth  = newAuthStore(nil) // replaced at startup; open hub until config is loaded
	globalCfg   *hubConfig         // live config; mutated by admin API
	globalCfgMu sync.Mutex         // protects globalCfg + config file writes
	globalCfgPath string           // path to YAML config file (for admin API persistence)
)

// ── Data structures ────────────────────────────────────────────────

// machineEntry stores the state of a registered machine.
type machineEntry struct {
	mu sync.Mutex

	AgentWS  *websocket.Conn
	ClientWS *websocket.Conn

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

	// UDP relay tokens (hex) — cleaned up on disconnect
	UDPTokens []string
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
	Role      string // "agent" or "client"
	MachineID string
	Paired    bool
	Peer      *websocket.Conn
	WGPubKey  string // client's WireGuard public key

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
	PeerWS       *websocket.Conn // fallback: peer's WebSocket
	Role         string
	Addr         *net.UDPAddr // learned from first UDP message
	MachineID    string
}

// ── Global state ───────────────────────────────────────────────────

var (
	machines   = make(map[string]*machineEntry)
	machinesMu sync.RWMutex

	// Per-WebSocket state, keyed by connection pointer.
	wsStates   = make(map[*websocket.Conn]*wsState)
	wsStatesMu sync.RWMutex

	// Session history (ring buffer, most recent first).
	history   []historyEvent
	historyMu sync.Mutex

	// UDP relay sessions, keyed by token hex.
	udpSessions   = make(map[string]*udpSession)
	udpSessionsMu sync.Mutex
)

const maxHistory = 100

func recordEvent(machineID, event, detail string) {
	historyMu.Lock()
	defer historyMu.Unlock()
	e := historyEvent{
		MachineID: machineID,
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Detail:    detail,
	}
	history = append([]historyEvent{e}, history...)
	if len(history) > maxHistory {
		history = history[:maxHistory]
	}
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

// tokenFromRequest extracts the caller token from Authorization: Bearer or ?token= query param.
func tokenFromRequest(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
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
		AgentConnected bool          `json:"agentConnected"`
		HasSession     bool          `json:"hasSession"`
		RegisteredAt   *string       `json:"registeredAt"`
		LastSeen       *string       `json:"lastSeen"`
		Services       []serviceDesc `json:"services"`
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
			AgentConnected: entry.AgentWS != nil,
			HasSession:     entry.ClientWS != nil,
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

	historyMu.Lock()
	events := make([]historyEvent, 0, len(history))
	for _, e := range history {
		if globalAuth.isEnabled() && !globalAuth.canViewMachine(callerToken, e.MachineID) {
			continue
		}
		events = append(events, e)
	}
	historyMu.Unlock()

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"events":    events,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// ── Static file serving ────────────────────────────────────────────

func handleStatic(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	if urlPath == "/" {
		urlPath = "/index.html"
	}

	filePath := filepath.Join(wwwDir, filepath.FromSlash(urlPath))
	filePath = filepath.Clean(filePath)

	// Prevent path traversal
	absWWW, _ := filepath.Abs(wwwDir)
	absFile, _ := filepath.Abs(filePath)
	if !strings.HasPrefix(absFile, absWWW) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		filePath = filepath.Join(filePath, "index.html")
		info, err = os.Stat(filePath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}

	// For index.html, inject the console viewer token so the page can
	// call /api/status and /api/history without user authentication.
	if filepath.Base(filePath) == "index.html" {
		viewerToken := globalAuth.consoleViewerToken()
		if viewerToken != "" {
			data, readErr := os.ReadFile(filePath)
			if readErr == nil {
				injection := fmt.Sprintf(
					`<script>window.TELA_CONSOLE_TOKEN=%q;</script>`,
					viewerToken,
				)
				html := strings.Replace(string(data), "</head>", injection+"\n</head>", 1)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(html))
				return
			}
		}
	}

	ext := filepath.Ext(filePath)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	http.ServeFile(w, r, filePath)
}

// ── WebSocket relay ────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
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

	// Forwarded through (peer-endpoint, wg-pubkey, etc.)
	Message string `json:"message,omitempty"`
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[hub] ws upgrade failed: %v", err)
		return
	}

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
		handleDisconnect(ws)
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

		// If paired, relay to peer
		if state.Paired {
			wsStatesMu.RLock()
			peer := state.Peer
			wsStatesMu.RUnlock()
			if peer != nil {
				if err := peer.WriteMessage(msgType, data); err != nil {
					log.Printf("[hub] relay write error: %v", err)
				} else {
					peerRole := "?"
					wsStatesMu.RLock()
					if ps, ok := wsStates[peer]; ok {
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
			ws.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseProtocolError, "Expected JSON for first message"))
			return
		}

		var msg signalingMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			ws.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseProtocolError, "Expected JSON for first message"))
			return
		}

		switch msg.Type {
		case "register":
			handleRegister(ws, state, &msg)
		case "connect":
			handleConnect(ws, state, &msg)
		default:
			ws.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseProtocolError, "Unknown message type"))
			return
		}
	}
}

func handleRegister(ws *websocket.Conn, state *wsState, msg *signalingMsg) {
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
	entry.AgentWS = ws
	entry.Token = msg.Token
	entry.Ports = msg.Ports
	entry.Services = msg.Services
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
	entry.LastSeen = now
	if entry.RegisteredAt.IsZero() {
		entry.RegisteredAt = now
	}

	normalized := normalizeServices(entry.Ports, entry.Services)
	ports := make([]int, len(normalized))
	for i, s := range normalized {
		ports[i] = s.Port
	}
	clientWS := entry.ClientWS
	entry.mu.Unlock()

	log.Printf("[hub] agent registered: %s ports=%v%s", machineID, ports, tokenLog(msg.Token))
	recordEvent(machineID, "agent-register", fmt.Sprintf("ports=%v", ports))

	reply, _ := json.Marshal(map[string]string{"type": "registered", "machineId": machineID})
	ws.WriteMessage(websocket.TextMessage, reply)

	// If a client is already waiting, pair them
	if clientWS != nil {
		pair(machineID)
	}
}

func handleConnect(ws *websocket.Conn, state *wsState, msg *signalingMsg) {
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
	agentWS := entry.AgentWS
	entryToken := entry.Token
	entry.mu.Unlock()

	if agentWS == nil {
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
	} else if entryToken != "" && entryToken != msg.Token {
		log.Printf("[hub] client token mismatch for %s", machineID)
		errMsg, _ := json.Marshal(map[string]string{"type": "error", "message": "Invalid token"})
		ws.WriteMessage(websocket.TextMessage, errMsg)
		ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Invalid token"))
		return
	}

	entry.mu.Lock()
	entry.ClientWS = ws
	entry.mu.Unlock()

	log.Printf("[hub] client connected for: %s%s", machineID, wgLog(msg.WGPubKey))
	pair(machineID)
}

func pair(machineID string) {
	machinesMu.RLock()
	entry, exists := machines[machineID]
	machinesMu.RUnlock()
	if !exists {
		return
	}

	entry.mu.Lock()
	agentWS := entry.AgentWS
	clientWS := entry.ClientWS
	entry.mu.Unlock()

	if agentWS == nil || clientWS == nil {
		return
	}

	// Cross-link peers
	wsStatesMu.Lock()
	agentState := wsStates[agentWS]
	clientState := wsStates[clientWS]
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

	log.Printf("[hub] paired agent <-> client for: %s", machineID)
	ss := ""
	wsStatesMu.RLock()
	if cs := wsStates[clientWS]; cs != nil {
		ss = cs.SessionDetail
	}
	wsStatesMu.RUnlock()
	recordEvent(machineID, "session-start", ss)

	// Signal agent: session-start (with client's WG public key if present)
	sessionStart := map[string]any{"type": "session-start"}
	if wgPubKey != "" {
		sessionStart["wgPubKey"] = wgPubKey
	}
	data, _ := json.Marshal(sessionStart)
	agentWS.WriteMessage(websocket.TextMessage, data)

	// Signal client: ready
	ready, _ := json.Marshal(map[string]string{"type": "ready"})
	clientWS.WriteMessage(websocket.TextMessage, ready)

	// Generate UDP relay tokens and send udp-offer to both sides
	const tokenLen = 8
	agentToken := make([]byte, tokenLen)
	clientToken := make([]byte, tokenLen)
	rand.Read(agentToken)
	rand.Read(clientToken)
	agentTokenHex := hex.EncodeToString(agentToken)
	clientTokenHex := hex.EncodeToString(clientToken)

	udpSessionsMu.Lock()
	udpSessions[agentTokenHex] = &udpSession{
		PeerTokenHex: clientTokenHex,
		PeerWS:       clientWS,
		Role:         "agent",
		MachineID:    machineID,
	}
	udpSessions[clientTokenHex] = &udpSession{
		PeerTokenHex: agentTokenHex,
		PeerWS:       agentWS,
		Role:         "client",
		MachineID:    machineID,
	}
	udpSessionsMu.Unlock()

	entry.mu.Lock()
	entry.UDPTokens = []string{agentTokenHex, clientTokenHex}
	entry.mu.Unlock()

	// Send udp-offer to both sides
	offer := map[string]any{"type": "udp-offer", "port": udpPort}

	offer["token"] = agentTokenHex
	agentOffer, _ := json.Marshal(offer)
	agentWS.WriteMessage(websocket.TextMessage, agentOffer)

	offer["token"] = clientTokenHex
	clientOffer, _ := json.Marshal(offer)
	clientWS.WriteMessage(websocket.TextMessage, clientOffer)

	log.Printf("[hub] sent udp-offer to both sides for: %s (port %d)", machineID, udpPort)
}

func handleDisconnect(ws *websocket.Conn) {
	wsStatesMu.RLock()
	state, ok := wsStates[ws]
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

	log.Printf("[hub] %s disconnected: %s", state.Role, state.MachineID)
	detail := state.SessionDetail
	if detail == "" {
		detail = state.Role + " disconnected"
	}
	recordEvent(state.MachineID, state.Role+"-disconnect", detail)

	// Clean up UDP session tokens
	entry.mu.Lock()
	if entry.UDPTokens != nil {
		udpSessionsMu.Lock()
		for _, tokenHex := range entry.UDPTokens {
			delete(udpSessions, tokenHex)
		}
		udpSessionsMu.Unlock()
		entry.UDPTokens = nil
		log.Printf("[hub] cleaned up UDP sessions for: %s", state.MachineID)
	}
	entry.mu.Unlock()

	// Close peer
	wsStatesMu.RLock()
	peer := state.Peer
	wsStatesMu.RUnlock()
	if peer != nil {
		peer.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, state.Role+" disconnected"))
		peer.Close()
	}

	entry.mu.Lock()
	if state.Role == "agent" {
		entry.AgentWS = nil
		entry.LastSeen = time.Now()
	} else if state.Role == "client" {
		entry.ClientWS = nil
	}
	entry.mu.Unlock()
}

func sendError(ws *websocket.Conn, message string) {
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
			// Peer is on UDP — forward via UDP
			peerTokenBytes, _ := hex.DecodeString(session.PeerTokenHex)
			relayBuf := make([]byte, udpTokenLen+len(payload))
			copy(relayBuf, peerTokenBytes)
			copy(relayBuf[udpTokenLen:], payload)
			conn.WriteToUDP(relayBuf, peer.Addr)
		} else if peer.PeerWS != nil {
			// Peer hasn't upgraded to UDP — fall back to WebSocket relay
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
			if entry.AgentWS != nil {
				entry.AgentWS.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
			entry.mu.Unlock()
		}
		machinesMu.RUnlock()
	}
}

// ── Main ───────────────────────────────────────────────────────────

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	// Handle service subcommand before anything else.
	if len(os.Args) > 1 && os.Args[1] == "service" {
		handleServiceCommand()
		return
	}

	// Handle user/auth management subcommand.
	if len(os.Args) > 1 && os.Args[1] == "user" {
		handleUserCommand()
		return
	}

	// If launched by Windows SCM, enter service mode automatically.
	if service.IsWindowsService() {
		runAsWindowsService()
		return
	}

	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("telahubd %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	// Parse flags
	configPath := flag.String("config", "", "Path to YAML config file")
	flag.Parse()

	// Load config file if given, or auto-detect persisted config from a
	// previous env-var bootstrap.
	var cfg *hubConfig
	if *configPath != "" {
		var err error
		cfg, err = loadHubConfig(*configPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
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

	globalCfg = cfg
	globalCfgPath = *configPath

	// If no config path was specified but we have auth config (e.g. from env
	// bootstrap), default to "telahubd.yaml" in a data directory so that
	// admin API mutations and bootstrap changes persist to disk.
	if globalCfgPath == "" && len(cfg.Auth.Tokens) > 0 {
		globalCfgPath = "data/telahubd.yaml"
		os.MkdirAll("data", 0o755)
		_ = writeHubConfig(globalCfgPath, cfg)
	}

	// Ensure a console-viewer token exists for the built-in hub console.
	// This handles upgrades where auth was previously bootstrapped without one.
	if ensureConsoleViewer(cfg, globalCfgPath) {
		log.Println("[hub] created console-viewer token for hub console")
	}

	globalAuth = newAuthStore(&cfg.Auth)

	runHub(nil)
}

// ── Portal self-registration ───────────────────────────────────────

// registerWithPortal POSTs the hub's name, public URL, and viewer token
// to the Awan Satu portal so it can proxy status requests server-side.
func registerWithPortal() {
	if portalURL == "" || hubName == "" {
		return
	}
	viewerToken := globalAuth.consoleViewerToken()
	hubURL := publicURL
	if hubURL == "" {
		// Fall back to hubName-based assumption — won't work in many cases,
		// but provides a visible log entry so operators can add HUB_PUBLIC_URL.
		log.Printf("[hub] WARNING: HUB_PUBLIC_URL not set; portal registration may use wrong URL")
		hubURL = fmt.Sprintf("https://%s", hubName)
	}

	body := map[string]string{
		"name":        hubName,
		"url":         hubURL,
		"viewerToken": viewerToken,
	}
	data, err := json.Marshal(body)
	if err != nil {
		log.Printf("[hub] portal registration marshal error: %v", err)
		return
	}

	target := strings.TrimRight(portalURL, "/") + "/api/hub-register"
	resp, err := http.Post(target, "application/json", strings.NewReader(string(data)))
	if err != nil {
		log.Printf("[hub] portal registration failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[hub] registered with portal %s", portalURL)
	} else {
		log.Printf("[hub] portal registration returned HTTP %d", resp.StatusCode)
	}
}

// startPortalHeartbeat runs registerWithPortal immediately and then
// every 60 seconds. Stops when done channel closes.
func startPortalHeartbeat(done <-chan struct{}) {
	if portalURL == "" {
		return
	}
	registerWithPortal()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			registerWithPortal()
		case <-done:
			return
		}
	}
}

// runHub starts the hub server and blocks until shutdown.
// If stopCh is non-nil, shutdown is triggered when it closes (service mode).
// If stopCh is nil, shutdown is triggered by SIGINT/SIGTERM (interactive mode).
func runHub(stopCh <-chan struct{}) {
	// Register HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", handleAPIStatus)
	mux.HandleFunc("/status", handleAPIStatus)
	mux.HandleFunc("/api/history", handleAPIHistory)
	mux.HandleFunc("/api/admin/tokens", handleAdminTokens)
	mux.HandleFunc("/api/admin/grant", handleAdminGrant)
	mux.HandleFunc("/api/admin/revoke", handleAdminRevoke)
	mux.HandleFunc("/api/admin/rotate/", handleAdminRotate) // /api/admin/rotate/{id}
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

	// Start portal heartbeat (self-registration with Awan Satu)
	portalDone := make(chan struct{})
	go startPortalHeartbeat(portalDone)

	// Graceful shutdown
	if stopCh != nil {
		// Service mode: stop when SCM/systemd signals
		go func() {
			<-stopCh
			log.Println("[hub] service stop received, shutting down")
			close(portalDone)
			server.Close()
		}()
	} else {
		// Interactive mode: stop on SIGINT/SIGTERM
		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			sig := <-sigCh
			log.Printf("[hub] received %v, shutting down", sig)
			close(portalDone)
			server.Close()
		}()
	}

	log.Printf("[hub] telahubd %s listening on http+ws://0.0.0.0:%d", version, httpPort)
	log.Printf("[hub] static site: %s", wwwDir)
	if hubName != "" {
		log.Printf("[hub] hub name: %s", hubName)
	}
	if portalURL != "" {
		log.Printf("[hub] portal registration: %s (heartbeat every 60s)", portalURL)
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
		fmt.Fprintf(os.Stderr, `telahubd service — manage telahubd as an OS service

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
  telahubd service install -name myhub -port 8080 -udp-port 41820 -www ./www
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
	cfgFile := fs.String("config", "", "Path to YAML config file (optional — generates one if omitted)")
	name := fs.String("name", "", "Hub display name")
	port := fs.Int("port", 8080, "HTTP+WS listen port")
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
		Description: "Tela Hub Server — encrypted tunnel relay",
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
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	header := "# telahubd configuration\n# Edit and restart the service to apply changes.\n\n"
	if err := os.WriteFile(path, []byte(header+string(data)), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", filepath.Dir(dst), err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func hubServiceUninstall() {
	if err := service.Uninstall("telahubd"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	// Also remove the YAML config
	yamlPath := service.BinaryConfigPath("telahubd")
	_ = os.Remove(yamlPath)
	fmt.Println("telahubd service uninstalled")
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

	globalCfg = cfg
	globalCfgPath = yamlPath
	globalAuth = newAuthStore(&cfg.Auth)

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
	fmt.Fprintf(os.Stderr, `telahubd user — manage auth tokens

Usage:
  telahubd user bootstrap [-config <path>]         First-run: generate owner token
  telahubd user list [-config <path>]               List all token identities
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

// userCmdConfigPath returns the config file path from -config flag or
// the system default.
func userCmdConfigPath() string {
	for i, arg := range os.Args {
		if arg == "-config" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return service.BinaryConfigPath("telahubd")
}

// loadOrCreateHubConfig reads an existing config or returns a blank one
// if the file doesn't exist.
func loadOrCreateHubConfig(path string) (*hubConfig, error) {
	cfg, err := loadHubConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &hubConfig{
				Port:    8080,
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
	cfgPath := userCmdConfigPath()
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

	cfg.Auth.Tokens = []tokenEntry{
		{ID: "owner", Token: token, HubRole: "owner"},
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
	fmt.Println("SAVE THIS TOKEN — it will not be shown again.")
	fmt.Println("Restart the hub to activate auth enforcement.")
}

func cmdUserList() {
	cfgPath := userCmdConfigPath()
	cfg, err := loadOrCreateHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.Auth.Tokens) == 0 {
		fmt.Println("No auth tokens configured. Run 'telahubd user bootstrap' to get started.")
		return
	}

	fmt.Printf("%-20s %-10s %s\n", "ID", "ROLE", "TOKEN (first 8)")
	fmt.Printf("%-20s %-10s %s\n", "----", "----", "--------")
	for _, t := range cfg.Auth.Tokens {
		role := t.HubRole
		if role == "" {
			role = "user"
		}
		preview := t.Token
		if len(preview) > 8 {
			preview = preview[:8] + "..."
		}
		fmt.Printf("%-20s %-10s %s\n", t.ID, role, preview)
	}
}

func cmdUserAdd() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user add <id> [-config <path>] [-role owner|admin]")
		os.Exit(1)
	}
	id := os.Args[3]

	// Parse optional -role flag
	role := ""
	for i, arg := range os.Args {
		if arg == "-role" && i+1 < len(os.Args) {
			role = os.Args[i+1]
		}
	}
	if role != "" && role != "owner" && role != "admin" {
		fmt.Fprintf(os.Stderr, "error: role must be 'owner' or 'admin' (omit for regular user)\n")
		os.Exit(1)
	}

	cfgPath := userCmdConfigPath()
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
		HubRole: role,
	})

	if err := saveUserConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Added identity '%s'", id)
	if role != "" {
		fmt.Printf(" (role: %s)", role)
	}
	fmt.Println()
	fmt.Printf("  Token: %s\n", token)
	fmt.Println()
	fmt.Println("SAVE THIS TOKEN — it will not be shown again.")
	fmt.Println("Restart the hub to apply changes.")
}

func cmdUserRemove() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user remove <id> [-config <path>]")
		os.Exit(1)
	}
	id := os.Args[3]

	cfgPath := userCmdConfigPath()
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
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user grant <id> <machineId> [-config <path>]")
		os.Exit(1)
	}
	id := os.Args[3]
	machineID := os.Args[4]

	cfgPath := userCmdConfigPath()
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
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user revoke <id> <machineId> [-config <path>]")
		os.Exit(1)
	}
	id := os.Args[3]
	machineID := os.Args[4]

	cfgPath := userCmdConfigPath()
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
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: telahubd user rotate <id> [-config <path>]")
		os.Exit(1)
	}
	id := os.Args[3]

	cfgPath := userCmdConfigPath()
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
	fmt.Println("SAVE THIS TOKEN — it will not be shown again.")
	fmt.Println("Restart the hub to apply changes.")
}

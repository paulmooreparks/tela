// telahubd — Go Tela Hub
//
// Drop-in replacement for poc/hub.js. Combined HTTP + WebSocket + UDP relay
// server on a single port. Serves the hub console (static files), exposes
// /api/status and /api/history with permissive CORS, and relays paired
// WireGuard sessions between agents and clients.
//
// Environment variables:
//   HUB_PORT      – HTTP+WS listen port  (default 8080)
//   HUB_UDP_PORT  – UDP relay port        (default 41820)
//   HUB_NAME      – optional display name for this hub
//   HUB_WWW_DIR   – static file directory (default ./www)
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
)

// version is set by -ldflags at build time.
var version = "dev"

// ── Configuration ──────────────────────────────────────────────────

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
	httpPort = envInt("HUB_PORT", 8080)
	udpPort  = envInt("HUB_UDP_PORT", 41820)
	hubName  = envStr("HUB_NAME", "")
	wwwDir   = envStr("HUB_WWW_DIR", "./www")
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
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
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

	historyMu.Lock()
	events := make([]historyEvent, len(history))
	copy(events, history)
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

	// Token validation
	if entryToken != "" && entryToken != msg.Token {
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
	wgPubKey := ""
	if clientState != nil {
		wgPubKey = clientState.WGPubKey
	}
	wsStatesMu.Unlock()

	log.Printf("[hub] paired agent <-> client for: %s", machineID)
	recordEvent(machineID, "session-start", "Client connected")

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
	recordEvent(state.MachineID, state.Role+"-disconnect", state.Role+" disconnected")

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

	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("telahubd %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	// Register HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", handleAPIStatus)
	mux.HandleFunc("/status", handleAPIStatus)
	mux.HandleFunc("/api/history", handleAPIHistory)
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

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("[hub] received %v, shutting down", sig)
		server.Close()
	}()

	log.Printf("[hub] telahubd %s listening on http+ws://0.0.0.0:%d", version, httpPort)
	log.Printf("[hub] static site: %s", wwwDir)
	if hubName != "" {
		log.Printf("[hub] hub name: %s", hubName)
	}

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[hub] server error: %v", err)
	}
	log.Println("[hub] stopped")
}

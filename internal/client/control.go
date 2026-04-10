package client

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/paulmooreparks/tela/internal/telelog"
)

// controlOutput captures log output for the browser terminal.
var (
	controlOutput   string
	controlOutputMu sync.RWMutex
)

// controlLogWriter captures log output to the controlOutput buffer.
type controlLogWriter struct {
	original *os.File
}

func (w *controlLogWriter) Write(p []byte) (int, error) {
	// Write to original stderr
	n, err := w.original.Write(p)
	// Also capture to buffer
	controlOutputMu.Lock()
	controlOutput += string(p)
	// Cap at 1MB to prevent unbounded growth
	if len(controlOutput) > 1024*1024 {
		controlOutput = controlOutput[len(controlOutput)-512*1024:]
	}
	controlOutputMu.Unlock()

	// Log lines are available via GET /output, not pushed via WebSocket
	// to avoid recursive loops and UI flooding.

	return n, err
}

// controlInfo is the JSON structure written to the control file.
type controlInfo struct {
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
	Token string `json:"token"`
}

// BoundService describes a locally bound port forwarding a remote service.
type BoundService struct {
	Name    string `json:"name"`
	Local   int    `json:"local"`
	Remote  int    `json:"remote"`
	Machine string `json:"machine"`
	Hub     string `json:"hub"`
}

var (
	boundServicesMu sync.RWMutex
	boundServices   []BoundService
)

// addBoundService records a successfully bound local port.
func addBoundService(svc BoundService) {
	boundServicesMu.Lock()
	defer boundServicesMu.Unlock()
	boundServices = append(boundServices, svc)

	// Emit service_bound event to WebSocket clients.
	emitEvent(struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Local   int    `json:"local"`
		Remote  int    `json:"remote"`
		Machine string `json:"machine"`
		Hub     string `json:"hub"`
	}{
		Type:    "service_bound",
		Name:    svc.Name,
		Local:   svc.Local,
		Remote:  svc.Remote,
		Machine: svc.Machine,
		Hub:     svc.Hub,
	})
}

// activeConnection describes one tunnel connection for the control API.
type activeConnection struct {
	Index   int    `json:"index"`
	Hub     string `json:"hub"`
	Machine string `json:"machine"`
	Status  string `json:"status"`
	Tunnel  string `json:"tunnel"`
}

var (
	activeConnsMu sync.RWMutex
	activeConns   []activeConnection
)

// setActiveConnections replaces the active connections list.
func setActiveConnections(conns []activeConnection) {
	activeConnsMu.Lock()
	defer activeConnsMu.Unlock()
	activeConns = make([]activeConnection, len(conns))
	copy(activeConns, conns)
}

// snapshotActiveConnections returns a copy of the active connections list.
func snapshotActiveConnections() []activeConnection {
	activeConnsMu.RLock()
	defer activeConnsMu.RUnlock()
	out := make([]activeConnection, len(activeConns))
	copy(out, activeConns)
	return out
}

// snapshotBoundServices returns a copy of the bound services list.
func snapshotBoundServices() []BoundService {
	boundServicesMu.RLock()
	defer boundServicesMu.RUnlock()
	out := make([]BoundService, len(boundServices))
	copy(out, boundServices)
	return out
}

// ── WebSocket event broadcasting ──────────────────────────────────

// wsClient represents a connected WebSocket client.
type wsClient struct {
	conn   *websocket.Conn
	events chan []byte
}

var (
	wsClientsMu sync.Mutex
	wsClients   []*wsClient
)

// wsEvent is the JSON envelope for server-to-client events.
type wsEvent struct {
	Type string `json:"type"`
}

// addWSClient registers a new WebSocket client for event broadcasting.
func addWSClient(c *wsClient) {
	wsClientsMu.Lock()
	defer wsClientsMu.Unlock()
	wsClients = append(wsClients, c)
}

// removeWSClient unregisters a WebSocket client.
func removeWSClient(c *wsClient) {
	wsClientsMu.Lock()
	defer wsClientsMu.Unlock()
	for i, cl := range wsClients {
		if cl == c {
			wsClients = append(wsClients[:i], wsClients[i+1:]...)
			return
		}
	}
}

// broadcastEvent sends a JSON event to all connected WebSocket clients.
// If a client's channel is full, the event is dropped for that client.
func broadcastEvent(data []byte) {
	wsClientsMu.Lock()
	defer wsClientsMu.Unlock()
	for _, c := range wsClients {
		select {
		case c.events <- data:
		default:
			// Channel full, drop event for this client.
		}
	}
}

// emitEvent marshals an event and broadcasts it to all WebSocket clients.
func emitEvent(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[control] failed to marshal event: %v", err)
		return
	}
	broadcastEvent(data)
}

// controlLogWriter debounce state for WebSocket log_line events.
var (
	logBatchMu    sync.Mutex
	logBatchBuf   strings.Builder
	logBatchTimer *time.Timer
)

// flushLogBatch sends any buffered log lines as a log_line event.
func flushLogBatch() {
	logBatchMu.Lock()
	if logBatchBuf.Len() == 0 {
		logBatchMu.Unlock()
		return
	}
	text := logBatchBuf.String()
	logBatchBuf.Reset()
	logBatchMu.Unlock()

	emitEvent(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "log_line", Text: text})
}

// controlFilePath returns the path to the control socket info file.
func controlFilePath() string {
	return filepath.Join(telaConfigDir(), "run", "control.json")
}

// startControlServer starts an HTTP control server on a random localhost port.
// It writes the control file and returns a cleanup function that removes it
// and shuts down the server.
func startControlServer(profileName string, stopCh chan struct{}) func() {
	// Capture log output for the control API. telelog writes formatted
	// lines to its output (os.Stderr by default). We replace the telelog
	// output with a tee that writes to both stderr and the capture buffer.
	telelog.SetOutput(&controlLogWriter{original: os.Stderr})

	// Generate a 32-byte random token (64 hex chars).
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Printf("[control] failed to generate token: %v", err)
		return func() {}
	}
	token := hex.EncodeToString(tokenBytes)

	// Listen on a random localhost port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("[control] failed to listen: %v", err)
		return func() {}
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Create the run directory.
	runDir := filepath.Join(telaConfigDir(), "run")
	if err := os.MkdirAll(runDir, 0700); err != nil {
		log.Printf("[control] failed to create run directory: %v", err)
		listener.Close()
		return func() {}
	}

	// Write the control file.
	info := controlInfo{
		PID:   os.Getpid(),
		Port:  port,
		Token: token,
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		log.Printf("[control] failed to marshal control info: %v", err)
		listener.Close()
		return func() {}
	}
	if err := os.WriteFile(controlFilePath(), data, 0600); err != nil {
		log.Printf("[control] failed to write control file: %v", err)
		listener.Close()
		return func() {}
	}

	startTime := time.Now()

	// Build the HTTP mux.
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !checkControlAuth(w, r, token) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			conns := snapshotActiveConnections()
			svcs := snapshotBoundServices()
			resp := map[string]interface{}{
				"version":     version,
				"uptime":      time.Since(startTime).Round(time.Second).String(),
				"profile":     profileName,
				"connections": len(conns),
				"services":    len(svcs),
				"_links": map[string]string{
					"connections": "/connections",
					"services":    "/services",
				},
			}
			writeJSON(w, http.StatusOK, resp)

		case http.MethodDelete:
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting down"})
			go func() {
				// Small delay so the response is sent before shutdown.
				time.Sleep(100 * time.Millisecond)
				log.Println("[control] shutdown requested via control API")
				log.Println("shutting down all connections")
				close(stopCh)
			}()

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/connections", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, snapshotActiveConnections())
	})

	mux.HandleFunc("/services", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, snapshotBoundServices())
	})

	mux.HandleFunc("/tunnels", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		machines := listTunnelMachines()
		type tunnelEntry struct {
			Machine string `json:"machine"`
		}
		out := make([]tunnelEntry, len(machines))
		for i, m := range machines {
			out[i] = tunnelEntry{Machine: m}
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("/reconnect", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Close and recreate stopCh to trigger reconnection of all sessions.
		// For now, return accepted. Reconnect logic can be wired up later
		// when a dedicated reconnect channel is available.
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "reconnect requested"})
	})

	mux.HandleFunc("/verbose", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]bool{"verbose": verbose})
		case http.MethodPut:
			verbose = !verbose
			log.Printf("[control] verbose logging %s", map[bool]string{true: "enabled", false: "disabled"}[verbose])
			writeJSON(w, http.StatusOK, map[string]bool{"verbose": verbose})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// File share probe: GET /files-probe/<machine> -- quick check for
	// whether a machine has a file share listener. Uses a single 3s
	// dial attempt, not the full retry cascade. Returns 200 if file
	// sharing is available, 503 if not.
	mux.HandleFunc("/files-probe/", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		machine := strings.TrimPrefix(r.URL.Path, "/files-probe/")
		if machine == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "machine name required"})
			return
		}
		conn, err := tryDialFileShare(machine)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no file share"})
			return
		}
		conn.Close()
		writeJSON(w, http.StatusOK, map[string]string{"status": "available"})
	})

	// File share proxy: POST /files/<machine> with JSON body.
	// Proxies a single request/response through the tunnel to port 17377.
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		machine := strings.TrimPrefix(r.URL.Path, "/files/")
		if machine == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "machine name required"})
			return
		}

		conn, err := dialFileShare(machine)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		defer conn.Close()

		// Read the full request body and write it to the tunnel.
		reqBody, _ := io.ReadAll(r.Body)
		conn.Write(reqBody)

		// Read the JSON response line.
		reader := bufio.NewReaderSize(conn, 32768)
		respLine, err := reader.ReadBytes('\n')
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "proxy recv failed"})
			return
		}

		var fsResp struct {
			OK   bool  `json:"ok"`
			Size int64 `json:"size"`
		}
		json.Unmarshal(respLine, &fsResp)

		flusher, canFlush := w.(http.Flusher)

		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(respLine)
		if canFlush {
			flusher.Flush()
		}

		// For read responses, forward chunk data until CHUNK 0.
		if fsResp.OK && fsResp.Size > 0 {
			for {
				chunkLine, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				w.Write([]byte(chunkLine))
				trimmed := strings.TrimSpace(chunkLine)
				if trimmed == "CHUNK 0" {
					break
				}
				if strings.HasPrefix(trimmed, "CHUNK ") {
					n, _ := strconv.ParseInt(strings.TrimPrefix(trimmed, "CHUNK "), 10, 64)
					if n > 0 {
						io.CopyN(w, reader, n)
					}
				}
				if canFlush {
					flusher.Flush()
				}
			}
		}
	})

	// WebSocket upgrade for real-time events.
	var controlUpgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Localhost only, allow all origins.
		},
	}

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// Auth via query param (legacy) or first-message.
		qToken := r.URL.Query().Get("token")
		authed := subtle.ConstantTimeCompare([]byte(qToken), []byte(token)) == 1

		conn, err := controlUpgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[control] WebSocket upgrade failed: %v", err)
			return
		}

		// If not authed via query param, expect token as first message.
		if !authed {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, msg, err := conn.ReadMessage()
			conn.SetReadDeadline(time.Time{})
			if err != nil || subtle.ConstantTimeCompare(msg, []byte(token)) != 1 {
				conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid token"))
				conn.Close()
				return
			}
		}

		client := &wsClient{
			conn:   conn,
			events: make(chan []byte, 64),
		}
		addWSClient(client)
		log.Printf("[control] WebSocket client connected")

		// Emit initial connection_state.
		emitEvent(struct {
			Type        string `json:"type"`
			Connected   bool   `json:"connected"`
			ProfileName string `json:"profileName"`
		}{Type: "connection_state", Connected: true, ProfileName: profileName})

		// Writer goroutine: sends events and ping keepalives.
		go func() {
			ticker := time.NewTicker(wsPingInterval)
			defer ticker.Stop()
			defer func() {
				removeWSClient(client)
				conn.Close()
			}()
			for {
				select {
				case msg, ok := <-client.events:
					if !ok {
						return
					}
					_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
					if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
						return
					}
				case <-ticker.C:
					if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
						return
					}
				case <-stopCh:
					// Connection is shutting down. Try to send a final event.
					data, _ := json.Marshal(struct {
						Type      string `json:"type"`
						Connected bool   `json:"connected"`
					}{Type: "connection_state", Connected: false})
					_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
					_ = conn.WriteMessage(websocket.TextMessage, data)
					_ = conn.WriteMessage(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutting down"))
					return
				}
			}
		}()

		// Reader goroutine: reads commands from client.
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(wsPongWait))
			return nil
		})
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var cmd struct {
				Type    string `json:"type"`
				Enabled *bool  `json:"enabled,omitempty"`
			}
			if err := json.Unmarshal(msg, &cmd); err != nil {
				continue
			}
			switch cmd.Type {
			case "disconnect":
				log.Println("[control] disconnect requested via WebSocket")
				log.Println("shutting down all connections")
				close(stopCh)
			case "reconnect":
				log.Println("[control] reconnect requested via WebSocket")
				// Placeholder: reconnect logic can be wired up later.
			case "set_verbose":
				if cmd.Enabled != nil {
					verbose = *cmd.Enabled
				} else {
					verbose = !verbose
				}
				log.Printf("[control] verbose logging %s (via WebSocket)",
					map[bool]string{true: "enabled", false: "disabled"}[verbose])
			}
		}
		// Reader exited; clean up.
		removeWSClient(client)
		conn.Close()
		log.Printf("[control] WebSocket client disconnected")
	})

	// Browser-accessible terminal -- no auth required (localhost only)
	// Shows live tela output in a browser window.
	mux.HandleFunc("/terminal", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Tela Terminal</title>
<style>
body { margin:0; background:#1e293b; color:#e2e8f0; font-family:monospace; font-size:13px; }
#hdr { padding:8px 16px; background:#0f172a; border-bottom:1px solid #334155; display:flex; align-items:center; gap:12px; position:sticky; top:0; }
#hdr span { font-weight:700; }
#status { font-size:12px; }
#out { padding:12px 16px; white-space:pre-wrap; word-break:break-all; }
</style></head><body>
<div id="hdr"><span>Tela Terminal</span><span id="status">Loading...</span></div>
<pre id="out"></pre>
<script>
var token=%q;
var autoScroll=true;
window.addEventListener("scroll",function(){
  autoScroll=(window.innerHeight+window.scrollY)>=(document.body.scrollHeight-30);
});
function poll(){
  fetch("/",{headers:{"Authorization":"Bearer "+token}})
  .then(function(r){return r.json()})
  .then(function(d){
    document.getElementById("status").textContent=d.uptime?"Connected ("+d.uptime+")":"Unknown";
  }).catch(function(){});
  fetch("/output",{headers:{"Authorization":"Bearer "+token}})
  .then(function(r){return r.text()})
  .then(function(t){
    if(t){document.getElementById("out").textContent=t;}
    if(autoScroll)window.scrollTo(0,document.body.scrollHeight);
  }).catch(function(){});
}
setInterval(poll,1000);
poll();
</script></body></html>`, token)
	})

	// Raw output endpoint for the browser terminal
	mux.HandleFunc("/output", func(w http.ResponseWriter, r *http.Request) {
		if !checkControlAuth(w, r, token) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		controlOutputMu.RLock()
		defer controlOutputMu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(controlOutput))
	})

	server := &http.Server{Handler: mux}

	// Start serving in the background.
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("[control] server error: %v", err)
		}
	}()

	log.Printf("[control] listening on 127.0.0.1:%d", port)

	// Return cleanup function.
	return func() {
		server.Close()
		os.Remove(controlFilePath())
		log.Printf("[control] stopped")
	}
}

// checkControlAuth validates the Bearer token on a control API request.
// Returns true if authorized; writes a 401 response and returns false otherwise.
func checkControlAuth(w http.ResponseWriter, r *http.Request, token string) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) < len(prefix) {
		http.Error(w, "authorization required", http.StatusUnauthorized)
		return false
	}
	provided := auth[len(prefix):]
	if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return false
	}
	return true
}

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("[control] failed to write response: %v", err)
	}
}

// controlRunDir returns the path to the run directory, exported for use in
// cleanup code.
func controlRunDir() string {
	return filepath.Join(telaConfigDir(), "run")
}

package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
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

// controlFilePath returns the path to the control socket info file.
func controlFilePath() string {
	return filepath.Join(telaConfigDir(), "run", "control.json")
}

// startControlServer starts an HTTP control server on a random localhost port.
// It writes the control file and returns a cleanup function that removes it
// and shuts down the server.
func startControlServer(profileName string, stopCh chan struct{}) func() {
	// Capture log output for the browser terminal
	log.SetOutput(&controlLogWriter{original: os.Stderr})

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

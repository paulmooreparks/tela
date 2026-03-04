/*
  tela-helper — Tela POC Helper (Go)

  Purpose:
    Single static binary that connects to a Tela Hub via WebSocket,
    requests a session with a named machine, then binds localhost:<port>
    as a TCP listener. When a local client (browser, mstsc, ssh, etc.)
    connects to that port, data is piped bidirectionally between the
    TCP socket and the WebSocket tunnel.

  Invariants:
    - Must not store credentials or persist any state.
    - Must not require admin/root rights.
    - Must not install itself or run as a service.
    - Must be a pure user-mode, ephemeral process.

  Usage:
    tela-helper [flags]
    tela-helper -hub wss://tela.awansatu.net -machine barn -port 8000

  Defaults:
    hub:     wss://localhost:8080
    machine: my-pc
    port:    8000
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
)

// controlMessage is the JSON envelope used for hub ↔ helper signalling.
type controlMessage struct {
	Type      string `json:"type"`
	MachineID string `json:"machineId,omitempty"`
	Message   string `json:"message,omitempty"`
}

func main() {
	hubURL := flag.String("hub", envOrDefault("HUB_URL", "wss://localhost:8080"), "Hub WebSocket URL")
	machineID := flag.String("machine", envOrDefault("MACHINE_ID", "my-pc"), "Target machine ID")
	localPort := flag.Int("port", envOrDefaultInt("LOCAL_PORT", 8000), "Local TCP port to bind")
	flag.Parse()

	// Also support positional args for compatibility with the Node helper:
	//   tela-helper [hubUrl] [machineId] [localPort]
	args := flag.Args()
	if len(args) >= 1 {
		*hubURL = args[0]
	}
	if len(args) >= 2 {
		*machineID = args[1]
	}
	if len(args) >= 3 {
		fmt.Sscanf(args[2], "%d", localPort)
	}

	log.SetFlags(log.Ltime)
	log.SetPrefix("[helper] ")

	log.Printf("connecting to hub: %s", *hubURL)

	// Connect to Hub via WebSocket
	wsConn, _, err := websocket.DefaultDialer.Dial(*hubURL, nil)
	if err != nil {
		log.Fatalf("websocket dial failed: %v", err)
	}
	defer wsConn.Close()

	log.Printf("connected, requesting session for: %s", *machineID)

	// Send connect request
	connectMsg := controlMessage{Type: "connect", MachineID: *machineID}
	if err := wsConn.WriteJSON(&connectMsg); err != nil {
		log.Fatalf("failed to send connect message: %v", err)
	}

	// Wait for "ready" or "error"
	for {
		_, rawMsg, err := wsConn.ReadMessage()
		if err != nil {
			log.Fatalf("failed reading from hub: %v", err)
		}

		var msg controlMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			// Not JSON — ignore during handshake
			continue
		}

		switch msg.Type {
		case "ready":
			log.Printf("tunnel ready")
			goto ready
		case "error":
			log.Fatalf("hub error: %s", msg.Message)
		}
	}

ready:
	// Bind local TCP listener
	listenAddr := fmt.Sprintf("127.0.0.1:%d", *localPort)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()

	log.Printf("listening on %s", listenAddr)
	log.Printf(">>> Open: http://localhost:%d", *localPort)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("shutting down")
		listener.Close()
		wsConn.Close()
		os.Exit(0)
	}()

	// Accept TCP connections and pipe to WebSocket.
	// POC: one client at a time (matches Node helper behavior).
	for {
		tcpConn, err := listener.Accept()
		if err != nil {
			// Listener closed (shutdown)
			return
		}

		if tc, ok := tcpConn.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}

		remoteAddr := tcpConn.RemoteAddr().String()
		log.Printf("client connected from %s", remoteAddr)

		handleClient(wsConn, tcpConn)

		log.Printf("client disconnected: %s", remoteAddr)
	}
}

// handleClient pipes data between a single TCP connection and the WebSocket.
// It blocks until the TCP connection or WebSocket closes.
func handleClient(wsConn *websocket.Conn, tcpConn net.Conn) {
	defer tcpConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// TCP → WebSocket
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := tcpConn.Read(buf)
			if n > 0 {
				log.Printf("tcp→ws %dB", n)
				if writeErr := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					log.Printf("ws write error: %v", writeErr)
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("tcp read error: %v", err)
				} else {
					log.Printf("tcp EOF")
				}
				return
			}
		}
	}()

	// WebSocket → TCP
	go func() {
		defer wg.Done()
		for {
			msgType, data, err := wsConn.ReadMessage()
			if err != nil {
				log.Printf("ws read error: %v", err)
				tcpConn.Close() // unblock the TCP→WS goroutine
				return
			}
			if msgType == websocket.BinaryMessage {
				log.Printf("ws→tcp %dB", len(data))
				if _, writeErr := tcpConn.Write(data); writeErr != nil {
					log.Printf("tcp write error: %v", writeErr)
					return
				}
			} else {
				log.Printf("ws→tcp SKIPPED text msg %dB", len(data))
			}
		}
	}()

	wg.Wait()
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return fallback
}

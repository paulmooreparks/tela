/*
  tela-agent — Tela WireGuard Agent

  Purpose:
    Connects to the Hub via WebSocket, registers a machine ID, and waits.
    When the Hub signals a session (with the helper's WireGuard public key),
    it creates a userspace WireGuard tunnel using gVisor netstack — no TUN
    device, no admin/root required.

    The agent listens inside the netstack on 10.77.0.1:<target-port> and
    proxies accepted TCP connections to a real host service (e.g. RDP on
    host.docker.internal:3389).

    All traffic between agent and helper is encrypted end-to-end with
    WireGuard (Curve25519 + ChaCha20-Poly1305). The Hub sees only opaque
    binary blobs.

  Usage:
    tela-agent -hub ws://hub:8080 -machine barn-rdp -target-port 3389 -target-host host.docker.internal

  Network:
    Agent IP: 10.77.0.1/24   (inside netstack)
    Helper IP: 10.77.0.2/24  (inside netstack)
*/
package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/paulmooreparks/tela/internal/wsbind"
)

const (
	agentIP  = "10.77.0.1"
	helperIP = "10.77.0.2"
	mtu      = 1420
)

// controlMessage is the JSON envelope for hub ↔ agent signalling.
type controlMessage struct {
	Type      string `json:"type"`
	MachineID string `json:"machineId,omitempty"`
	Message   string `json:"message,omitempty"`
	WGPubKey  string `json:"wgPubKey,omitempty"`
}

func main() {
	hubURL := flag.String("hub", envOrDefault("HUB_URL", "ws://localhost:8080"), "Hub WebSocket URL")
	machineID := flag.String("machine", envOrDefault("MACHINE_ID", "my-pc"), "Machine ID to register")
	targetPort := flag.Int("target-port", envOrDefaultInt("TARGET_PORT", 3389), "Target service port on target host")
	targetHost := flag.String("target-host", envOrDefault("TARGET_HOST", "127.0.0.1"), "Target service host")
	flag.Parse()

	log.SetFlags(log.Ltime)
	log.SetPrefix("[agent] ")

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		os.Exit(0)
	}()

	for {
		runAgent(*hubURL, *machineID, *targetHost, *targetPort)
		log.Println("reconnecting in 3 seconds...")
		time.Sleep(3 * time.Second)
	}
}

func runAgent(hubURL, machineID, targetHost string, targetPort int) {
	log.Printf("connecting to hub: %s", hubURL)

	ws, _, err := websocket.DefaultDialer.Dial(hubURL, nil)
	if err != nil {
		log.Printf("dial failed: %v", err)
		return
	}
	defer ws.Close()

	// Register with hub
	log.Printf("connected, registering as: %s", machineID)
	regMsg := controlMessage{Type: "register", MachineID: machineID}
	if err := ws.WriteJSON(&regMsg); err != nil {
		log.Printf("register failed: %v", err)
		return
	}

	// Read control messages until session-start
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			log.Printf("hub read error: %v", err)
			return
		}

		var msg controlMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "registered":
			log.Printf("registered as: %s — waiting for session", msg.MachineID)

		case "session-start":
			helperPubKey := msg.WGPubKey
			if helperPubKey == "" {
				log.Printf("session-start missing helper public key")
				return
			}
			log.Printf("session starting — helper pubkey: %s...", helperPubKey[:8])
			handleSession(ws, helperPubKey, targetHost, targetPort)
			return // reconnect after session ends
		}
	}
}

func handleSession(ws *websocket.Conn, helperPubKeyHex, targetHost string, targetPort int) {
	// Generate ephemeral WireGuard keypair using Go standard library
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Printf("keygen failed: %v", err)
		return
	}
	privKeyHex := hex.EncodeToString(privKey.Bytes())
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	// Send our public key back to helper (via hub relay)
	keyMsg := controlMessage{Type: "wg-pubkey", WGPubKey: pubKeyHex}
	if err := ws.WriteJSON(&keyMsg); err != nil {
		log.Printf("failed to send pubkey: %v", err)
		return
	}
	log.Printf("sent agent pubkey: %s...", pubKeyHex[:8])

	// Create netstack TUN (pure userspace — no admin needed)
	tunDev, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(agentIP)},
		nil, // no DNS
		mtu,
	)
	if err != nil {
		log.Printf("netstack creation failed: %v", err)
		return
	}

	// Create wsBind — WireGuard datagrams go through the WebSocket
	bind := wsbind.New(ws, 256)

	// Create WireGuard device
	logger := &device.Logger{
		Verbosef: log.Printf,
		Errorf:   log.Printf,
	}
	dev := device.NewDevice(tunDev, bind, logger)

	// Configure WireGuard via UAPI/IPC
	// Keys must be lowercase hex-encoded 32 bytes
	ipcConf := fmt.Sprintf(`private_key=%s
public_key=%s
endpoint=ws:0
allowed_ip=%s/32
persistent_keepalive_interval=25
`, privKeyHex, helperPubKeyHex, helperIP)

	if err := dev.IpcSet(ipcConf); err != nil {
		log.Printf("WireGuard IPC config failed: %v", err)
		dev.Close()
		return
	}

	if err := dev.Up(); err != nil {
		log.Printf("WireGuard device up failed: %v", err)
		dev.Close()
		return
	}
	log.Printf("WireGuard tunnel up — agent=%s helper=%s", agentIP, helperIP)

	// Start reader goroutine: WebSocket binary → wsBind.RecvCh
	done := make(chan struct{})
	go func() {
		defer close(done)
		wsReader(ws, bind)
	}()

	// Listen on netstack for incoming TCP connections from the helper
	listenAddr := netip.AddrPortFrom(netip.MustParseAddr(agentIP), uint16(targetPort))
	listener, err := tnet.ListenTCPAddrPort(listenAddr)
	if err != nil {
		log.Printf("netstack listen failed on %s: %v", listenAddr, err)
		dev.Close()
		return
	}
	log.Printf("netstack listening on %s → %s:%d", listenAddr, targetHost, targetPort)

	// Accept connections from helper through the WireGuard tunnel
	go func() {
		for {
			nsConn, err := listener.Accept()
			if err != nil {
				log.Printf("netstack accept error: %v", err)
				return
			}
			log.Printf("tunnel connection from %s → proxy to %s:%d", nsConn.RemoteAddr(), targetHost, targetPort)
			go proxyToTarget(nsConn, targetHost, targetPort)
		}
	}()

	// Wait for WebSocket to close (session end)
	<-done
	log.Println("session ended — tearing down WireGuard")
	listener.Close()
	dev.Close()
}

// wsReader reads from the WebSocket and dispatches:
//   - Binary messages → wsBind.RecvCh (WireGuard datagrams)
//   - Text messages → logged (control messages during data phase)
func wsReader(ws *websocket.Conn, bind *wsbind.Bind) {
	for {
		msgType, data, err := ws.ReadMessage()
		if err != nil {
			log.Printf("ws read error: %v", err)
			return
		}
		if msgType == websocket.BinaryMessage {
			select {
			case bind.RecvCh <- data:
			default:
				log.Printf("wsBind recv buffer full, dropping %dB", len(data))
			}
		} else {
			log.Printf("text message during session: %s", string(data))
		}
	}
}

// proxyToTarget connects to the real target service and pipes data bidirectionally.
func proxyToTarget(nsConn net.Conn, targetHost string, targetPort int) {
	defer nsConn.Close()

	addr := fmt.Sprintf("%s:%d", targetHost, targetPort)
	targetConn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		log.Printf("target dial failed (%s): %v", addr, err)
		return
	}
	defer targetConn.Close()

	if tc, ok := targetConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	log.Printf("proxying tunnel ↔ %s", addr)

	var wg sync.WaitGroup
	wg.Add(2)

	// tunnel (netstack) → target
	go func() {
		defer wg.Done()
		n, _ := io.Copy(targetConn, nsConn)
		log.Printf("tunnel→target closed (%d bytes)", n)
		if tc, ok := targetConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// target → tunnel (netstack)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(nsConn, targetConn)
		log.Printf("target→tunnel closed (%d bytes)", n)
	}()

	wg.Wait()
	log.Printf("proxy session ended for %s", addr)
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

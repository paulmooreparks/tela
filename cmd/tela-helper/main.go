/*
  tela-helper — Tela WireGuard Helper

  Purpose:
    Connects to a Tela Hub via WebSocket, performs a WireGuard key exchange
    with the target agent, and establishes an encrypted L3 tunnel.

    Two modes of operation:

    1. Default (netstack) — no admin required:
       Binds a local TCP listener on 127.0.0.1:<port>.
       When a client (mstsc, ssh, browser) connects, data is piped through
       a userspace gVisor netstack into the WireGuard tunnel.

    2. TUN mode (--tun) — requires admin/root:
       Creates a real TUN interface with IP 10.77.0.2.
       The OS routes traffic to 10.77.0.0/24 through the TUN.
       Clients connect directly to the agent's IP (e.g. mstsc /v:10.77.0.1).
       This is the Tailscale-like experience — completely transparent.

  Usage:
    tela-helper -hub wss://tela.awansatu.net -machine barn-rdp -port 13389
    tela-helper -hub wss://tela.awansatu.net -machine barn-rdp --tun

  Network:
    Agent IP:  10.77.0.1/24
    Helper IP: 10.77.0.2/24
*/
package main

import (
	"context"
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

type controlMessage struct {
	Type      string `json:"type"`
	MachineID string `json:"machineId,omitempty"`
	Message   string `json:"message,omitempty"`
	WGPubKey  string `json:"wgPubKey,omitempty"`
}

func main() {
	hubURL := flag.String("hub", envOrDefault("HUB_URL", "wss://localhost:8080"), "Hub WebSocket URL")
	machineID := flag.String("machine", envOrDefault("MACHINE_ID", "my-pc"), "Target machine ID")
	localPort := flag.Int("port", envOrDefaultInt("LOCAL_PORT", 8000), "Local TCP port to bind (netstack mode)")
	targetPort := flag.Int("target-port", envOrDefaultInt("TARGET_PORT", 0), "Target port on agent (default: same as -port)")
	// tunMode := flag.Bool("tun", false, "Use real TUN interface (requires admin)")
	flag.Parse()

	if *targetPort == 0 {
		*targetPort = *localPort
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

	// Generate ephemeral WireGuard keypair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("keygen failed: %v", err)
	}
	privKeyHex := hex.EncodeToString(privKey.Bytes())
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	log.Printf("connected, requesting session for: %s", *machineID)
	log.Printf("helper pubkey: %s...", pubKeyHex[:8])

	// Send connect request with our public key
	connectMsg := controlMessage{Type: "connect", MachineID: *machineID, WGPubKey: pubKeyHex}
	if err := wsConn.WriteJSON(&connectMsg); err != nil {
		log.Fatalf("failed to send connect: %v", err)
	}

	// Wait for "ready" and agent's public key
	var agentPubKeyHex string
	ready := false

	for {
		_, rawMsg, err := wsConn.ReadMessage()
		if err != nil {
			log.Fatalf("failed reading from hub: %v", err)
		}

		var msg controlMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "ready":
			ready = true
			log.Printf("tunnel ready signal received")
		case "wg-pubkey":
			agentPubKeyHex = msg.WGPubKey
			log.Printf("agent pubkey: %s...", agentPubKeyHex[:8])
		case "error":
			log.Fatalf("hub error: %s", msg.Message)
		}

		// Need both ready and agent pubkey to proceed
		if ready && agentPubKeyHex != "" {
			break
		}
	}

	// Create netstack TUN (pure userspace, no admin)
	tunDev, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(helperIP)},
		nil, // no DNS
		mtu,
	)
	if err != nil {
		log.Fatalf("netstack creation failed: %v", err)
	}

	// Create wsBind — WireGuard datagrams go through the WebSocket
	bind := wsbind.New(wsConn, 256)

	// Create WireGuard device
	logger := &device.Logger{
		Verbosef: log.Printf,
		Errorf:   log.Printf,
	}
	dev := device.NewDevice(tunDev, bind, logger)

	// Configure WireGuard
	ipcConf := fmt.Sprintf(`private_key=%s
public_key=%s
endpoint=ws:0
allowed_ip=%s/32
persistent_keepalive_interval=25
`, privKeyHex, agentPubKeyHex, agentIP)

	if err := dev.IpcSet(ipcConf); err != nil {
		log.Fatalf("WireGuard IPC config failed: %v", err)
	}

	if err := dev.Up(); err != nil {
		log.Fatalf("WireGuard device up failed: %v", err)
	}
	log.Printf("WireGuard tunnel up — helper=%s agent=%s", helperIP, agentIP)

	// Start reader goroutine: WebSocket binary → wsBind.RecvCh
	go wsReader(wsConn, bind)

	// Netstack mode: bind local TCP listener and proxy through WireGuard tunnel
	runNetstackMode(tnet, *localPort, *targetPort, dev)
}

// runNetstackMode binds a local TCP listener and proxies each connection
// through the WireGuard tunnel to the agent.
func runNetstackMode(tnet *netstack.Net, localPort, targetPort int, dev *device.Device) {
	listenAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()

	log.Printf("listening on %s → tunnel to %s:%d", listenAddr, agentIP, targetPort)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("shutting down")
		listener.Close()
		dev.Close()
		os.Exit(0)
	}()

	// Accept local TCP connections and proxy through WireGuard tunnel
	for {
		localConn, err := listener.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			return
		}

		if tc, ok := localConn.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}

		log.Printf("client connected from %s", localConn.RemoteAddr())
		go handleNetstackClient(tnet, localConn, targetPort)
	}
}

// handleNetstackClient dials the agent through the WireGuard tunnel
// (via netstack) and pipes data bidirectionally.
func handleNetstackClient(tnet *netstack.Net, localConn net.Conn, targetPort int) {
	defer localConn.Close()

	// Dial through the WireGuard tunnel to the agent's netstack
	agentAddr := netip.AddrPortFrom(netip.MustParseAddr(agentIP), uint16(targetPort))
	tunnelConn, err := tnet.DialContextTCPAddrPort(context.Background(), agentAddr)
	if err != nil {
		log.Printf("tunnel dial failed (%s): %v", agentAddr, err)
		return
	}
	defer tunnelConn.Close()

	log.Printf("tunnel connected to %s", agentAddr)

	var wg sync.WaitGroup
	wg.Add(2)

	// local → tunnel
	go func() {
		defer wg.Done()
		n, _ := io.Copy(tunnelConn, localConn)
		log.Printf("local→tunnel closed (%d bytes)", n)
	}()

	// tunnel → local
	go func() {
		defer wg.Done()
		n, _ := io.Copy(localConn, tunnelConn)
		log.Printf("tunnel→local closed (%d bytes)", n)
	}()

	wg.Wait()
	log.Printf("client disconnected")
}

// wsReader reads from the WebSocket and dispatches binary messages
// to the wsBind receive channel for WireGuard processing.
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

/*
  tela — Tela Client

  Purpose:
    Connects to a Tela Hub via WebSocket, performs a WireGuard key exchange
    with the target daemon, and establishes an encrypted L3 tunnel.

    Two modes of operation:

    1. Default (netstack) — no admin required:
       Binds a local TCP listener on 127.0.0.1:<port>.
       When a client (mstsc, ssh, browser) connects, data is piped through
       a userspace gVisor netstack into the WireGuard tunnel.

    2. TUN mode (--tun) — requires admin/root:
       Creates a real TUN interface with IP 10.77.0.2.
       The OS routes traffic to 10.77.0.0/24 through the TUN.
       Clients connect directly to the daemon's IP (e.g. mstsc /v:10.77.0.1).
       This is the Tailscale-like experience — completely transparent.

  Usage:
    tela -hub wss://tela.awansatu.net -machine barn-wg
    tela -hub wss://tela.awansatu.net -machine barn-wg -port 13389 -target-port 3389

  Network:
    Daemon IP: 10.77.0.1/24
    Client IP: 10.77.0.2/24
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
	Type      string   `json:"type"`
	MachineID string   `json:"machineId,omitempty"`
	Message   string   `json:"message,omitempty"`
	WGPubKey  string   `json:"wgPubKey,omitempty"`
	Ports     []uint16 `json:"ports,omitempty"`
}

// Well-known port names for friendly display.
var portNames = map[uint16]string{
	22: "SSH", 80: "HTTP", 443: "HTTPS", 3389: "RDP",
	5900: "VNC", 8080: "HTTP", 8443: "HTTPS",
}

var verbose bool

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `tela — Tela Client

Connect to a remote machine through a Tela Hub using an encrypted
WireGuard tunnel. No admin/root required.

Usage:
  tela -hub <url> -machine <id> [options]

Examples:
  tela -hub wss://tela.awansatu.net -machine barn-wg
  tela -hub wss://tela.awansatu.net -machine barn-wg -v
  tela -hub wss://tela.awansatu.net -machine barn-wg -port 13389 -target-port 3389

Options:
`)
		flag.PrintDefaults()
	}

	hubURL := flag.String("hub", envOrDefault("HUB_URL", ""), "Hub WebSocket URL (required)")
	machineID := flag.String("machine", envOrDefault("MACHINE_ID", ""), "Target machine ID (required)")
	localPort := flag.Int("port", 0, "Local TCP port (advanced: single-port override)")
	targetPort := flag.Int("target-port", 0, "Target port on daemon (advanced: with -port)")
	flag.BoolVar(&verbose, "v", false, "Verbose logging")
	flag.Parse()

	if *hubURL == "" || *machineID == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Single-port override mode: -port given explicitly
	singlePortMode := *localPort != 0
	if singlePortMode && *targetPort == 0 {
		*targetPort = *localPort
	}

	log.SetFlags(log.Ltime)
	log.SetPrefix("[tela] ")

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

	// Wait for "ready" and agent's public key + port list
	var agentPubKeyHex string
	var agentPorts []uint16
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
			agentPorts = msg.Ports
			log.Printf("agent pubkey: %s...", agentPubKeyHex[:8])
			if len(agentPorts) > 0 {
				log.Printf("agent ports: %v", agentPorts)
			}
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
	wgVerbose := func(string, ...any) {}
	if verbose {
		wgVerbose = log.Printf
	}
	logger := &device.Logger{
		Verbosef: wgVerbose,
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

	if singlePortMode {
		// Advanced: single-port override
		runNetstackMode(tnet, []portMapping{{local: uint16(*localPort), remote: uint16(*targetPort)}}, dev)
	} else if len(agentPorts) > 0 {
		// Auto mode: bind local listeners for each agent-advertised port
		var mappings []portMapping
		for _, p := range agentPorts {
			mappings = append(mappings, portMapping{local: p, remote: p})
		}
		runNetstackMode(tnet, mappings, dev)
	} else {
		log.Fatalf("agent did not advertise any ports — use -port and -target-port")
	}
}

// portMapping pairs a local listener port with the remote agent port.
type portMapping struct {
	local  uint16
	remote uint16
}

// runNetstackMode binds local TCP listeners for each port mapping and proxies
// connections through the WireGuard tunnel to the agent.
func runNetstackMode(tnet *netstack.Net, mappings []portMapping, dev *device.Device) {
	var listeners []net.Listener

	log.Println("Services available:")
	for _, m := range mappings {
		listenAddr := fmt.Sprintf("127.0.0.1:%d", m.local)
		listener, err := net.Listen("tcp", listenAddr)
		if err != nil {
			// Port conflict — try local + 10000
			alt := m.local + 10000
			listenAddr = fmt.Sprintf("127.0.0.1:%d", alt)
			listener, err = net.Listen("tcp", listenAddr)
			if err != nil {
				log.Printf("  SKIP port %d (could not bind: %v)", m.local, err)
				continue
			}
			log.Printf("  localhost:%-5d → %s (port %d in use, remapped)", alt, portLabel(m.remote), m.remote)
		} else {
			log.Printf("  localhost:%-5d → %s", m.local, portLabel(m.remote))
		}

		listeners = append(listeners, listener)

		go func(l net.Listener, remote uint16) {
			for {
				localConn, err := l.Accept()
				if err != nil {
					return
				}
				if tc, ok := localConn.(*net.TCPConn); ok {
					tc.SetNoDelay(true)
				}
				go handleNetstackClient(tnet, localConn, int(remote))
			}
		}(listener, m.remote)
	}

	if len(listeners) == 0 {
		log.Fatalf("no ports could be bound")
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutting down")
	for _, l := range listeners {
		l.Close()
	}
	dev.Close()
}

// portLabel returns a friendly name for well-known ports.
func portLabel(port uint16) string {
	if name, ok := portNames[port]; ok {
		return name
	}
	return fmt.Sprintf("port %d", port)
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
		logVerbose("local→tunnel closed (%d bytes)", n)
	}()

	// tunnel → local
	go func() {
		defer wg.Done()
		n, _ := io.Copy(localConn, tunnelConn)
		logVerbose("tunnel→local closed (%d bytes)", n)
	}()

	wg.Wait()
	logVerbose("client disconnected")
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
			logVerbose("text message during session: %s", string(data))
		}
	}
}

func logVerbose(format string, args ...any) {
	if verbose {
		log.Printf(format, args...)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

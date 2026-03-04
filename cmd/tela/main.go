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
	"net/url"
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

type controlMessage struct {
	Type      string   `json:"type"`
	MachineID string   `json:"machineId,omitempty"`
	Message   string   `json:"message,omitempty"`
	WGPubKey  string   `json:"wgPubKey,omitempty"`
	Ports     []uint16 `json:"ports,omitempty"`
	Token     string   `json:"token,omitempty"`
	Port      int      `json:"port,omitempty"` // single port (udp-offer)
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
  tela -hub wss://tela.awansatu.net -machine barn-wg -token s3cret
  tela -hub wss://tela.awansatu.net -machine barn-wg -port 13389 -target-port 3389

Options:
`)
		flag.PrintDefaults()
	}

	hubURL := flag.String("hub", envOrDefault("HUB_URL", ""), "Hub WebSocket URL (required)")
	machineID := flag.String("machine", envOrDefault("MACHINE_ID", ""), "Target machine ID (required)")
	token := flag.String("token", envOrDefault("TELA_TOKEN", ""), "Connection token (must match daemon)")
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

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		os.Exit(0)
	}()

	var mappings []portMapping
	if singlePortMode {
		mappings = []portMapping{{local: uint16(*localPort), remote: uint16(*targetPort)}}
	}

	for {
		runSession(*hubURL, *machineID, *token, mappings)
		log.Println("reconnecting in 3 seconds...")
		time.Sleep(3 * time.Second)
	}
}

func runSession(hubURL, machineID, token string, overrideMappings []portMapping) {
	log.Printf("connecting to hub: %s", hubURL)

	// Connect to Hub via WebSocket
	wsConn, _, err := websocket.DefaultDialer.Dial(hubURL, nil)
	if err != nil {
		log.Printf("websocket dial failed: %v", err)
		return
	}
	defer wsConn.Close()

	// Generate ephemeral WireGuard keypair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Printf("keygen failed: %v", err)
		return
	}
	privKeyHex := hex.EncodeToString(privKey.Bytes())
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	log.Printf("connected, requesting session for: %s", machineID)
	logVerbose("client pubkey: %s...", pubKeyHex[:8])

	// Send connect request with our public key and token
	connectMsg := controlMessage{Type: "connect", MachineID: machineID, WGPubKey: pubKeyHex, Token: token}
	if err := wsConn.WriteJSON(&connectMsg); err != nil {
		log.Printf("failed to send connect: %v", err)
		return
	}

	// Wait for "ready" and daemon's public key + port list
	var agentPubKeyHex string
	var agentPorts []uint16
	var udpTokenHex string
	var udpPort int
	ready := false

	for {
		_, rawMsg, err := wsConn.ReadMessage()
		if err != nil {
			log.Printf("failed reading from hub: %v", err)
			return
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
			logVerbose("daemon pubkey: %s...", agentPubKeyHex[:8])
			if len(agentPorts) > 0 {
				log.Printf("daemon ports: %v", agentPorts)
			}
		case "udp-offer":
			udpTokenHex = msg.Token
			udpPort = msg.Port
			log.Printf("received UDP relay offer (port %d)", udpPort)
		case "error":
			log.Printf("hub error: %s", msg.Message)
			return
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
		log.Printf("netstack creation failed: %v", err)
		return
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
		log.Printf("WireGuard IPC config failed: %v", err)
		dev.Close()
		return
	}

	if err := dev.Up(); err != nil {
		log.Printf("WireGuard device up failed: %v", err)
		dev.Close()
		return
	}
	log.Printf("WireGuard tunnel up — client=%s daemon=%s", helperIP, agentIP)

	// Start reader goroutine: WebSocket binary → wsBind.RecvCh
	done := make(chan struct{})
	go func() {
		defer close(done)
		wsReader(wsConn, bind, hubURL)
	}()

	// Attempt UDP relay upgrade (if hub offered it)
	if udpTokenHex != "" && udpPort > 0 {
		tryUDPUpgrade(bind, hubURL, udpTokenHex, udpPort)
	}

	// Phase 3: attempt direct tunnel via STUN + hole punching
	if bind.UDPActive() {
		go tryDirectUpgrade(bind, hubURL)
	}

	// Determine port mappings
	mappings := overrideMappings
	if len(mappings) == 0 && len(agentPorts) > 0 {
		for _, p := range agentPorts {
			mappings = append(mappings, portMapping{local: p, remote: p})
		}
	}
	if len(mappings) == 0 {
		log.Printf("daemon did not advertise any ports — use -port and -target-port")
		dev.Close()
		return
	}

	// Bind local listeners
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
		log.Printf("no ports could be bound")
		dev.Close()
		return
	}

	// Wait for WebSocket to close (session end) or signal
	<-done
	log.Println("session ended — cleaning up")
	for _, l := range listeners {
		l.Close()
	}
	dev.Close()
}

// portMapping pairs a local listener port with the remote agent port.
type portMapping struct {
	local  uint16
	remote uint16
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
// Text messages are checked for control commands (e.g. udp-offer, peer-endpoint).
func wsReader(ws *websocket.Conn, bind *wsbind.Bind, hubURL string) {
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
			var msg controlMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				logVerbose("text message during session: %s", string(data))
				continue
			}
			switch msg.Type {
			case "udp-offer":
				if !bind.UDPActive() {
					log.Printf("received late UDP relay offer (port %d)", msg.Port)
					tryUDPUpgrade(bind, hubURL, msg.Token, msg.Port)
					if bind.UDPActive() {
						go tryDirectUpgrade(bind, hubURL)
					}
				}
			case "peer-endpoint":
				if bind.UDPActive() && !bind.DirectActive() {
					log.Printf("received peer endpoint: %s", msg.Message)
					go func() {
						if err := bind.AttemptDirect(msg.Message); err != nil {
							log.Printf("direct tunnel failed (staying on relay): %v", err)
						}
					}()
				}
			default:
				logVerbose("text message during session: %s", string(data))
			}
		}
	}
}

// tryUDPUpgrade attempts to switch from WebSocket to UDP relay transport.
func tryUDPUpgrade(bind *wsbind.Bind, hubURL, tokenHex string, port int) {
	u, err := url.Parse(hubURL)
	if err != nil {
		log.Printf("UDP upgrade: cannot parse hub URL: %v", err)
		return
	}
	token, err := hex.DecodeString(tokenHex)
	if err != nil {
		log.Printf("UDP upgrade: invalid token: %v", err)
		return
	}
	if err := bind.UpgradeUDP(u.Hostname(), port, token); err != nil {
		log.Printf("UDP upgrade failed (continuing on WebSocket): %v", err)
	}
}

// tryDirectUpgrade performs STUN discovery and sends our reflexive
// address to the peer via the hub relay for hole punching.
func tryDirectUpgrade(bind *wsbind.Bind, hubURL string) {
	reflexive, err := bind.STUNDiscover()
	if err != nil {
		log.Printf("STUN discovery failed (staying on relay): %v", err)
		return
	}
	log.Printf("STUN reflexive address: %s", reflexive)

	msg := controlMessage{Type: "peer-endpoint", Message: reflexive}
	data, _ := json.Marshal(msg)
	if err := bind.SendText(data); err != nil {
		log.Printf("failed to send peer-endpoint: %v", err)
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

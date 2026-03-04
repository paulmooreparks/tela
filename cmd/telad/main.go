/*
  telad — Tela Daemon (WireGuard Agent)

  Purpose:
    Connects to the Hub via WebSocket, registers a machine ID, and waits.
    When the Hub signals a session (with the client's WireGuard public key),
    it creates a userspace WireGuard tunnel using gVisor netstack — no TUN
    device, no admin/root required.

    The daemon listens inside the netstack on 10.77.0.1 for each port in
    the -ports list and proxies accepted TCP connections to the target host.
    One daemon can expose RDP, SSH, HTTP, and any other TCP service.

    All traffic between daemon and client is encrypted end-to-end with
    WireGuard (Curve25519 + ChaCha20-Poly1305). The Hub sees only opaque
    binary blobs.

  Usage:
    telad -hub ws://hub:8080 -machine barn-wg -ports 22,3389,8080 -target-host host.docker.internal

  Network:
    Daemon IP: 10.77.0.1/24  (inside netstack)
    Client IP: 10.77.0.2/24  (inside netstack)
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
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
	Type      string   `json:"type"`
	MachineID string   `json:"machineId,omitempty"`
	Message   string   `json:"message,omitempty"`
	WGPubKey  string   `json:"wgPubKey,omitempty"`
	Ports     []uint16 `json:"ports,omitempty"`
	Token     string   `json:"token,omitempty"`
	Port      int      `json:"port,omitempty"` // single port (udp-offer)
}

// silentLogger discards verbose WireGuard-go routine spam.
type silentLogger struct{}

func (silentLogger) Printf(string, ...any) {}

var verbose bool

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `telad — Tela Daemon

Register with a Tela Hub and expose local services through an encrypted
WireGuard tunnel. No TUN device or admin/root required.

Usage:
  telad -hub <url> -machine <id> [options]

Examples:
  telad -hub ws://hub:8080 -machine barn-wg -ports 22,3389
  telad -hub wss://tela.awansatu.net -machine barn-wg -ports 3389 -token s3cret
  telad -hub wss://tela.awansatu.net -machine barn-wg -ports 3389 -target-host 192.168.1.10

Options:
`)
		flag.PrintDefaults()
	}

	hubURL := flag.String("hub", envOrDefault("HUB_URL", ""), "Hub WebSocket URL (required)")
	machineID := flag.String("machine", envOrDefault("MACHINE_ID", ""), "Machine ID to register (required)")
	token := flag.String("token", envOrDefault("TELA_TOKEN", ""), "Connection token (clients must match to connect)")
	portsStr := flag.String("ports", envOrDefault("PORTS", "3389"), "Comma-separated list of TCP ports to forward")
	targetHost := flag.String("target-host", envOrDefault("TARGET_HOST", "127.0.0.1"), "Target service host")
	flag.BoolVar(&verbose, "v", false, "Verbose logging")
	flag.Parse()

	if *hubURL == "" || *machineID == "" {
		flag.Usage()
		os.Exit(1)
	}

	ports := parsePorts(*portsStr)
	if len(ports) == 0 {
		log.Fatalf("no valid ports in: %s", *portsStr)
	}

	log.SetFlags(log.Ltime)
	log.SetPrefix("[telad] ")

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		os.Exit(0)
	}()

	for {
		runAgent(*hubURL, *machineID, *token, *targetHost, ports)
		log.Println("reconnecting in 3 seconds...")
		time.Sleep(3 * time.Second)
	}
}

func parsePorts(s string) []uint16 {
	var ports []uint16
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			log.Printf("ignoring invalid port: %s", p)
			continue
		}
		ports = append(ports, uint16(n))
	}
	return ports
}

func runAgent(hubURL, machineID, token, targetHost string, ports []uint16) {
	log.Printf("connecting to hub: %s", hubURL)

	ws, _, err := websocket.DefaultDialer.Dial(hubURL, nil)
	if err != nil {
		log.Printf("dial failed: %v", err)
		return
	}
	defer ws.Close()

	// Register with hub
	log.Printf("connected, registering as: %s", machineID)
	regMsg := controlMessage{Type: "register", MachineID: machineID, Token: token}
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
			handleSession(ws, hubURL, helperPubKey, targetHost, ports)
			return // reconnect after session ends
		}
	}
}

func handleSession(ws *websocket.Conn, hubURL, helperPubKeyHex, targetHost string, ports []uint16) {
	// Generate ephemeral WireGuard keypair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Printf("keygen failed: %v", err)
		return
	}
	privKeyHex := hex.EncodeToString(privKey.Bytes())
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	// Send our public key and port list back to helper (via hub relay)
	keyMsg := controlMessage{Type: "wg-pubkey", WGPubKey: pubKeyHex, Ports: ports}
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
	wgVerbose := silentLogger{}.Printf
	if verbose {
		wgVerbose = log.Printf
	}
	logger := &device.Logger{
		Verbosef: wgVerbose,
		Errorf:   log.Printf,
	}
	dev := device.NewDevice(tunDev, bind, logger)

	// Configure WireGuard via UAPI/IPC
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
		wsReader(ws, bind, hubURL)
	}()

	// Listen on each port inside netstack
	var listeners []net.Listener
	for _, port := range ports {
		listenAddr := netip.AddrPortFrom(netip.MustParseAddr(agentIP), port)
		listener, err := tnet.ListenTCPAddrPort(listenAddr)
		if err != nil {
			log.Printf("netstack listen failed on %s: %v", listenAddr, err)
			continue
		}
		listeners = append(listeners, listener)
		log.Printf("forwarding %s → %s:%d", listenAddr, targetHost, port)

		// Accept connections from helper through the WireGuard tunnel
		go func(l net.Listener, p uint16) {
			for {
				nsConn, err := l.Accept()
				if err != nil {
					return // listener closed
				}
					logVerbose("tunnel→%s:%d from %s", targetHost, p, nsConn.RemoteAddr())
				go proxyToTarget(nsConn, targetHost, int(p))
			}
		}(listener, port)
	}

	// Wait for WebSocket to close (session end)
	<-done
	log.Println("session ended — tearing down WireGuard")
	for _, l := range listeners {
		l.Close()
	}
	dev.Close()
}

// wsReader reads from the WebSocket and dispatches:
//   - Binary messages → wsBind.RecvCh (WireGuard datagrams)
//   - Text messages → parsed for control commands (udp-offer, peer-endpoint), else logged
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
					log.Printf("received UDP relay offer (port %d)", msg.Port)
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

	logVerbose("proxying tunnel ↔ %s", addr)

	var wg sync.WaitGroup
	wg.Add(2)

	// tunnel (netstack) → target
	go func() {
		defer wg.Done()
		n, _ := io.Copy(targetConn, nsConn)
		logVerbose("tunnel→target closed (%d bytes)", n)
		if tc, ok := targetConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// target → tunnel (netstack)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(nsConn, targetConn)
		logVerbose("target→tunnel closed (%d bytes)", n)
	}()

	wg.Wait()
	logVerbose("proxy session ended for %s", addr)
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

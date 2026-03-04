/*
tela — Tela Client

Purpose:

	Connects to a Tela Hub via WebSocket, performs a WireGuard key exchange
	with the target daemon, and establishes an encrypted L3 tunnel.

	Subcommands:
	  tela connect  — connect to a machine
	  tela machines — list registered machines and their services
	  tela services — list services on a specific machine
	  tela status   — show hub status summary

	Environment variables (provide defaults so flags can be omitted):
	  TELA_HUB      — hub WebSocket URL
	  TELA_MACHINE  — target machine ID
	  TELA_TOKEN    — authentication token

Network:

	Daemon IP: 10.77.0.1/24
	Client IP: 10.77.0.2/24
*/
package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gorilla/websocket"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"gopkg.in/yaml.v3"

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
	log.SetFlags(log.Ltime)
	log.SetPrefix("[tela] ")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "connect":
		cmdConnect(os.Args[2:])
	case "machines":
		cmdMachines(os.Args[2:])
	case "services":
		cmdServices(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `tela — Tela Client

Usage:
  tela <command> [options]

Commands:
  connect   Connect to a machine through an encrypted WireGuard tunnel
  machines  List registered machines and their services
  services  List services on a specific machine
  status    Show hub status summary

Environment Variables:
  TELA_HUB      Default hub URL or alias  (overridden by -hub)
  TELA_MACHINE  Default machine ID        (overridden by -machine)
  TELA_TOKEN    Default auth token        (overridden by -token)

  When set, these provide defaults so flags can be omitted.

Hub Aliases:
  The -hub flag accepts a full URL (wss://...) or a short alias.
  Aliases are defined in a config file:
    Linux/macOS:  ~/.tela/hubs.yaml
    Windows:      %%APPDATA%%\tela\hubs.yaml

  Config file format:
    hubs:
      myHub: wss://example.com

Examples:
  tela connect  -hub owlsnest -machine barn
  tela machines -hub owlsnest
  tela connect  -hub wss://tela.awansatu.net -machine barn

  # Or set env vars and skip the flags:
  export TELA_HUB=owlsnest TELA_MACHINE=barn
  tela connect          # uses env defaults
  tela machines         # only needs TELA_HUB

Run 'tela <command> -h' for command-specific help.
`)
}

// ── "tela connect" ─────────────────────────────────────────────────

func cmdConnect(args []string) {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub WebSocket URL (env: TELA_HUB)")
	machineID := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Target machine ID (env: TELA_MACHINE)")
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Auth token (env: TELA_TOKEN)")
	localPort := fs.Int("port", 0, "Local TCP port (advanced: single-port override)")
	targetPort := fs.Int("target-port", 0, "Target port on daemon (advanced: with -port)")
	fs.BoolVar(&verbose, "v", false, "Verbose logging")
	fs.Parse(args)
	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" || *machineID == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub and -machine are required (or set TELA_HUB / TELA_MACHINE)")
		fs.Usage()
		os.Exit(1)
	}

	singlePortMode := *localPort != 0
	if singlePortMode && *targetPort == 0 {
		*targetPort = *localPort
	}

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

// ── "tela machines" ────────────────────────────────────────────────

// hubStatusResponse is the JSON shape returned by the hub /api/status endpoint.
type hubStatusResponse struct {
	Machines  []hubMachine `json:"machines"`
	Timestamp string       `json:"timestamp"`
}

type hubMachine struct {
	ID             string       `json:"id"`
	DisplayName    string       `json:"displayName,omitempty"`
	Hostname       string       `json:"hostname,omitempty"`
	OS             string       `json:"os,omitempty"`
	AgentVersion   string       `json:"agentVersion,omitempty"`
	Tags           []string     `json:"tags,omitempty"`
	Location       string       `json:"location,omitempty"`
	Owner          string       `json:"owner,omitempty"`
	LastSeen       string       `json:"lastSeen,omitempty"`
	AgentConnected bool         `json:"agentConnected"`
	HasSession     bool         `json:"hasSession"`
	RegisteredAt   string       `json:"registeredAt"`
	Services       []hubService `json:"services"`
}

type hubService struct {
	Port        int    `json:"port"`
	Proto       string `json:"proto,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Label       string `json:"label,omitempty"` // backward compat
}

func serviceLabel(s hubService) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Label != "" {
		return s.Label
	}
	if s.Port > 0 {
		return fmt.Sprintf("port %d", s.Port)
	}
	return "service"
}

func cmdMachines(args []string) {
	fs := flag.NewFlagSet("machines", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub is required (or set TELA_HUB)")
		fs.Usage()
		os.Exit(1)
	}

	data, err := fetchHubStatus(*hubURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(data)
		return
	}

	if len(data.Machines) == 0 {
		fmt.Println("No machines registered.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MACHINE\tSTATUS\tSERVICES\tSESSION")
	for _, m := range data.Machines {
		status := "offline"
		if m.AgentConnected {
			status = "online"
		}
		var svcs []string
		for _, s := range m.Services {
			svcs = append(svcs, fmt.Sprintf("%s:%d", serviceLabel(s), s.Port))
		}
		svcStr := strings.Join(svcs, ", ")
		if svcStr == "" {
			svcStr = "—"
		}
		sess := "—"
		if m.HasSession {
			sess = "active"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.ID, status, svcStr, sess)
	}
	w.Flush()
}

// ── "tela services" ────────────────────────────────────────────────

func cmdServices(args []string) {
	fs := flag.NewFlagSet("services", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	machineID := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID (env: TELA_MACHINE)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" || *machineID == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub and -machine are required (or set TELA_HUB / TELA_MACHINE)")
		fs.Usage()
		os.Exit(1)
	}

	data, err := fetchHubStatus(*hubURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var found *hubMachine
	for i := range data.Machines {
		if data.Machines[i].ID == *machineID {
			found = &data.Machines[i]
			break
		}
	}
	if found == nil {
		fmt.Fprintf(os.Stderr, "Machine not found: %s\n", *machineID)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(found)
		return
	}

	status := "offline"
	if found.AgentConnected {
		status = "online"
	}
	fmt.Printf("Machine: %s (%s)\n", found.ID, status)

	if len(found.Services) == 0 {
		fmt.Println("No services advertised.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PORT\tSERVICE\tCONNECT")
	for _, s := range found.Services {
		fmt.Fprintf(w, "%d\t%s\ttela connect -hub %s -machine %s\n", s.Port, serviceLabel(s), *hubURL, *machineID)
	}
	w.Flush()
}

// ── "tela status" ──────────────────────────────────────────────────

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub is required (or set TELA_HUB)")
		fs.Usage()
		os.Exit(1)
	}

	data, err := fetchHubStatus(*hubURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(data)
		return
	}

	online := 0
	sessions := 0
	totalSvcs := 0
	for _, m := range data.Machines {
		if m.AgentConnected {
			online++
		}
		if m.HasSession {
			sessions++
		}
		totalSvcs += len(m.Services)
	}

	fmt.Printf("Hub:       %s\n", *hubURL)
	fmt.Printf("Machines:  %d registered, %d online\n", len(data.Machines), online)
	fmt.Printf("Services:  %d total\n", totalSvcs)
	fmt.Printf("Sessions:  %d active\n", sessions)
	fmt.Printf("Timestamp: %s\n", data.Timestamp)
}

// ── Hub API client ─────────────────────────────────────────────────

// fetchHubStatus queries the hub's /api/status HTTP endpoint.
// The hubURL may be a ws:// or wss:// URL; we convert to http(s).
func fetchHubStatus(hubURL string) (*hubStatusResponse, error) {
	apiURL := wsToHTTP(hubURL) + "/api/status"

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{},
		},
	}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("could not reach hub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("hub returned HTTP %d", resp.StatusCode)
	}

	var data hubStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid JSON from hub: %w", err)
	}
	return &data, nil
}

// wsToHTTP converts a ws:// or wss:// URL to http:// or https://.
func wsToHTTP(wsURL string) string {
	s := strings.Replace(wsURL, "wss://", "https://", 1)
	s = strings.Replace(s, "ws://", "http://", 1)
	return strings.TrimRight(s, "/")
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

// ── Hub alias config ───────────────────────────────────────────────

// hubsConfig is the schema for ~/.tela/hubs.yaml (or %APPDATA%\tela\hubs.yaml).
type hubsConfig struct {
	Hubs map[string]string `yaml:"hubs"`
}

// hubsConfigPath returns the platform-appropriate path to the hubs config file.
func hubsConfigPath() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "tela", "hubs.yaml")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tela", "hubs.yaml")
}

// loadHubsConfig reads and parses the hubs config file.
func loadHubsConfig() (*hubsConfig, error) {
	data, err := os.ReadFile(hubsConfigPath())
	if err != nil {
		return nil, err
	}
	var cfg hubsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", hubsConfigPath(), err)
	}
	if cfg.Hubs == nil {
		cfg.Hubs = make(map[string]string)
	}
	return &cfg, nil
}

// resolveHubURL resolves a hub reference to a WebSocket URL.
// If the value starts with ws:// or wss://, it is used as-is.
// Otherwise it is looked up as a named alias in the hubs config file.
func resolveHubURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") {
		return value, nil
	}

	cfg, err := loadHubsConfig()
	if err != nil {
		return "", fmt.Errorf("hub %q is not a URL and no config file found (%s)", value, hubsConfigPath())
	}

	url, ok := cfg.Hubs[value]
	if !ok {
		var known []string
		for k := range cfg.Hubs {
			known = append(known, k)
		}
		return "", fmt.Errorf("hub alias %q not found in %s (known: %s)", value, hubsConfigPath(), strings.Join(known, ", "))
	}
	logVerbose("resolved hub alias %q → %s", value, url)
	return url, nil
}

// mustResolveHub resolves a hub alias, exiting on error.
func mustResolveHub(value string) string {
	resolved, err := resolveHubURL(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return resolved
}

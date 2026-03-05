/*
telad — Tela Daemon (WireGuard Agent)

Purpose:

	Connects to the Hub via WebSocket, registers one or more machines,
	and waits. When the Hub signals a session (with the client's WireGuard
	public key), it creates a userspace WireGuard tunnel using gVisor
	netstack — no TUN device, no admin/root required.

	Config-file mode (recommended):
	  telad -config telad.yaml

	Single-machine mode (flags):
	  telad -hub ws://hub:8080 -machine barn -ports 22,3389

Network:

	Daemon IP: 10.77.0.1/24  (inside netstack, per machine)
	Client IP: 10.77.0.2/24  (inside netstack, per machine)
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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"gopkg.in/yaml.v3"

	"github.com/paulmooreparks/tela/internal/service"
	"github.com/paulmooreparks/tela/internal/wsbind"
)

const (
	agentIP  = "10.77.0.1"
	helperIP = "10.77.0.2"
	mtu      = 1420
)

var version = "dev"

// controlMessage is the JSON envelope for hub ↔ agent signalling.
type controlMessage struct {
	Type      string   `json:"type"`
	MachineID string   `json:"machineId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	OS          string `json:"os,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Location    string   `json:"location,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Message   string   `json:"message,omitempty"`
	WGPubKey  string   `json:"wgPubKey,omitempty"`
	Ports     []uint16 `json:"ports,omitempty"`
	Services  []serviceDescriptor `json:"services,omitempty"`
	Token     string   `json:"token,omitempty"`
	Port      int      `json:"port,omitempty"` // single port (udp-offer)
}

type serviceDescriptor struct {
	Port        uint16 `json:"port" yaml:"port"`
	Proto       string `json:"proto,omitempty" yaml:"proto,omitempty"`
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// silentLogger discards verbose WireGuard-go routine spam.
type silentLogger struct{}

func (silentLogger) Printf(string, ...any) {}

// ── Config file schema ─────────────────────────────────────────────

// configFile is the top-level YAML structure for telad.yaml.
type configFile struct {
	Hub      string          `yaml:"hub"`
	Token    string          `yaml:"token,omitempty"`
	Machines []machineConfig `yaml:"machines"`
}

// machineConfig describes one machine to register with the hub.
type machineConfig struct {
	Name        string   `yaml:"name"`
	DisplayName string   `yaml:"displayName,omitempty"`
	Hostname    string   `yaml:"hostname,omitempty"`    // override os.Hostname() (useful in containers)
	OS          string   `yaml:"os,omitempty"`          // e.g. "windows", "linux"; defaults to runtime.GOOS
	Tags        []string `yaml:"tags,omitempty"`
	Location    string   `yaml:"location,omitempty"`
	Owner       string   `yaml:"owner,omitempty"`
	Ports       []uint16 `yaml:"ports,omitempty"`
	Services    []serviceDescriptor `yaml:"services,omitempty"`
	Target      string   `yaml:"target,omitempty"` // defaults to 127.0.0.1
	Token       string   `yaml:"token,omitempty"`  // overrides top-level token
}

type registration struct {
	MachineID    string
	DisplayName  string
	Hostname     string
	OS           string
	AgentVersion string
	Tags         []string
	Location     string
	Owner        string
	Token        string
	Ports        []uint16
	Services     []serviceDescriptor
}

var verbose bool

// stopCh is closed to signal graceful shutdown (used in service mode).
var stopCh chan struct{}

func main() {
	// Check for service subcommand or Windows SCM launch before flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "service" {
		handleServiceCommand()
		return
	}

	// If launched by the Windows SCM, enter service mode automatically.
	if service.IsWindowsService() {
		runAsWindowsService()
		return
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `telad — Tela Daemon

Register with a Tela Hub and expose local services through an encrypted
WireGuard tunnel. No TUN device or admin/root required.

Usage:
  telad -config <file>                   Config-file mode (recommended)
  telad -hub <url> -machine <id> [opts]  Single-machine mode

Examples:
  telad -config telad.yaml
  telad -hub ws://hub:8080 -machine barn -ports 22:SSH,3389:RDP
  telad -hub wss://tela.awansatu.net -machine barn -ports "22:SSH,3389:RDP,12345:Prometheus" -token s3cret

Options:
`)
		flag.PrintDefaults()
	}

	configPath := flag.String("config", envOrDefault("TELA_CONFIG", ""), "Path to YAML config file (env: TELA_CONFIG)")
	hubURL := flag.String("hub", envOrDefault("TELA_HUB", ""), "Hub WebSocket URL (env: TELA_HUB)")
	machineID := flag.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID to register (env: TELA_MACHINE)")
	token := flag.String("token", envOrDefault("TELA_TOKEN", ""), "Auth token (env: TELA_TOKEN)")
	portsStr := flag.String("ports", envOrDefault("TELA_PORTS", "3389"), "Comma-separated port specs: port[:name[:description]]  e.g. 22:SSH,3389:RDP,12345:MyApp (env: TELA_PORTS)")
	targetHost := flag.String("target-host", envOrDefault("TELA_TARGET_HOST", "127.0.0.1"), "Target service host (env: TELA_TARGET_HOST)")
	flag.BoolVar(&verbose, "v", false, "Verbose logging")
	flag.Parse()

	// Handle version flag before anything else
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Printf("telad %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
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

	// Config-file mode
	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		runMultiMachine(cfg)
		return
	}

	// Single-machine mode (flags / env vars)
	if *hubURL == "" || *machineID == "" {
		flag.Usage()
		os.Exit(1)
	}

	services := parsePortSpecs(*portsStr)
	if len(services) == 0 {
		log.Fatalf("no valid ports in: %s", *portsStr)
	}

	hostname, _ := os.Hostname()
	reg := registration{
		MachineID:    *machineID,
		Hostname:     hostname,
		OS:           runtime.GOOS,
		AgentVersion: version,
		Token:        *token,
		Ports:        portsFromServices(services),
		Services:     services,
	}

	runSingleMachine(*hubURL, reg, *targetHost)
}

// ── Service management ─────────────────────────────────────────────

func handleServiceCommand() {
	if len(os.Args) < 3 {
		cfgPath := service.BinaryConfigPath("telad")
		fmt.Fprintf(os.Stderr, `telad service — manage telad as an OS service

Usage:
  telad service install -config <file>  Install service (copies config to system dir)
  telad service uninstall               Remove the service
  telad service start                   Start the installed service
  telad service stop                    Stop the running service
  telad service restart                 Restart the service
  telad service status                  Show service status
  telad service run                     Run in service mode (used by the service manager)

The service reads its configuration from:
  %s

Edit that file and run "telad service restart" to reconfigure.

Install example:
  telad service install -config telad.yaml
`, cfgPath)
		os.Exit(1)
	}

	subcmd := os.Args[2]

	switch subcmd {
	case "install":
		serviceInstall()
	case "uninstall":
		serviceUninstall()
	case "start":
		serviceStart()
	case "stop":
		serviceStop()
	case "restart":
		serviceRestart()
	case "status":
		serviceStatus()
	case "run":
		serviceRun()
	default:
		fmt.Fprintf(os.Stderr, "unknown service subcommand: %s\n", subcmd)
		os.Exit(1)
	}
}

func serviceInstall() {
	// Parse flags after "service install"
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to YAML config file (required)")
	fs.Parse(os.Args[3:])

	if *configPath == "" {
		fmt.Fprintf(os.Stderr, "error: -config is required\n")
		fmt.Fprintf(os.Stderr, "usage: telad service install -config <file>\n")
		os.Exit(1)
	}

	// Validate the config file
	absConfig, _ := filepath.Abs(*configPath)
	if _, err := loadConfig(absConfig); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Copy the config to the system-wide location
	destPath := service.BinaryConfigPath("telad")
	if err := copyFile(absConfig, destPath); err != nil {
		fmt.Fprintf(os.Stderr, "error copying config: %v\n", err)
		os.Exit(1)
	}

	// Get the absolute path to the current executable
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	exePath, _ = filepath.Abs(exePath)

	wd, _ := os.Getwd()
	cfg := &service.Config{
		BinaryPath:  exePath,
		Description: "Tela Daemon — encrypted tunnel agent",
		WorkingDir:  wd,
	}

	if err := service.Install("telad", cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service installed successfully")
	fmt.Printf("  config: %s\n", destPath)
	fmt.Println("  start:  telad service start")
	fmt.Println("")
	fmt.Println("Edit the config file and run \"telad service restart\" to reconfigure.")
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", filepath.Dir(dst), err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func serviceUninstall() {
	if err := service.Uninstall("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	// Also remove the YAML config
	yamlPath := service.BinaryConfigPath("telad")
	_ = os.Remove(yamlPath)
	fmt.Println("telad service uninstalled")
}

func serviceStart() {
	if err := service.Start("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service started")
}

func serviceStop() {
	if err := service.Stop("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service stopped")
}

func serviceRestart() {
	fmt.Println("stopping telad service...")
	_ = service.Stop("telad")
	// Brief pause to let the service fully stop
	time.Sleep(time.Second)
	if err := service.Start("telad"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telad service restarted")
}

func serviceStatus() {
	st, err := service.QueryStatus("telad")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("installed: %v\n", st.Installed)
	fmt.Printf("running:   %v\n", st.Running)
	fmt.Printf("status:    %s\n", st.Info)
	if st.Installed {
		fmt.Printf("config:    %s\n", service.BinaryConfigPath("telad"))
	}
}

// serviceRunDaemon loads the YAML config from the system directory and
// runs telad. It blocks until stopCh is closed.
func serviceRunDaemon(stopCh <-chan struct{}) {
	log.SetFlags(log.Ltime)
	log.SetPrefix("[telad] ")

	svcCfg, err := service.LoadConfig("telad")
	if err != nil {
		log.Fatalf("service config: %v", err)
	}

	if svcCfg.WorkingDir != "" {
		os.Chdir(svcCfg.WorkingDir)
	}

	// Load the YAML config from the system-wide location
	yamlPath := service.BinaryConfigPath("telad")
	fileCfg, err := loadConfig(yamlPath)
	if err != nil {
		log.Fatalf("config %s: %v", yamlPath, err)
	}

	log.Printf("loaded config from %s", yamlPath)
	go runMultiMachine(fileCfg)

	<-stopCh
	log.Println("service stopping")
}

func serviceRun() {
	stopCh = make(chan struct{})

	// Handle signals for non-Windows "service run" (systemd/launchd)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		close(stopCh)
	}()

	serviceRunDaemon(stopCh)
}

func runAsWindowsService() {
	handler := &service.Handler{
		Run: func(svcStopCh <-chan struct{}) {
			serviceRunDaemon(svcStopCh)
		},
	}
	if err := service.RunAsService("telad", handler); err != nil {
		log.Fatalf("service failed: %v", err)
	}
}

// ── Config loading ─────────────────────────────────────────────────
func loadConfig(path string) (*configFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Hub == "" {
		return nil, fmt.Errorf("%s: 'hub' is required", path)
	}
	if len(cfg.Machines) == 0 {
		return nil, fmt.Errorf("%s: 'machines' list is empty", path)
	}
	for i, m := range cfg.Machines {
		if m.Name == "" {
			return nil, fmt.Errorf("%s: machines[%d]: 'name' is required", path, i)
		}
		if len(m.Ports) == 0 && len(m.Services) == 0 {
			return nil, fmt.Errorf("%s: machines[%d] (%s): either 'ports' or 'services' is required", path, i, m.Name)
		}
		if m.Target == "" {
			cfg.Machines[i].Target = "127.0.0.1"
		}
		if m.Token == "" {
			cfg.Machines[i].Token = cfg.Token
		}
		if len(m.Ports) == 0 && len(m.Services) > 0 {
			cfg.Machines[i].Ports = portsFromServices(m.Services)
		}
		if len(m.Services) == 0 && len(m.Ports) > 0 {
			cfg.Machines[i].Services = minimalServicesFromPorts(m.Ports)
		}
	}
	return &cfg, nil
}

// runMultiMachine launches a goroutine per machine and blocks forever.
func runMultiMachine(cfg *configFile) {
	log.Printf("config: %d machine(s), hub %s", len(cfg.Machines), cfg.Hub)
	var wg sync.WaitGroup
	for _, m := range cfg.Machines {
		wg.Add(1)
		go func(mc machineConfig) {
			defer wg.Done()
			hostname := mc.Hostname
			if hostname == "" {
				hostname, _ = os.Hostname()
			}
			machineOS := mc.OS
			if machineOS == "" {
				machineOS = runtime.GOOS
			}
			reg := registration{
				MachineID:    mc.Name,
				DisplayName:  mc.DisplayName,
				Hostname:     hostname,
				OS:           machineOS,
				AgentVersion: version,
				Tags:         mc.Tags,
				Location:     mc.Location,
				Owner:        mc.Owner,
				Token:        mc.Token,
				Ports:        mc.Ports,
				Services:     mc.Services,
			}
			runSingleMachine(cfg.Hub, reg, mc.Target)
		}(m)
	}
	wg.Wait()
}

// runSingleMachine is the reconnect loop for one machine.
func runSingleMachine(hubURL string, reg registration, targetHost string) {
	prefix := fmt.Sprintf("[telad:%s] ", reg.MachineID)
	logger := log.New(os.Stderr, prefix, log.Ltime)
	for {
		runAgent(logger, hubURL, reg, targetHost)
		logger.Println("reconnecting in 3 seconds...")
		time.Sleep(3 * time.Second)
	}
}

func portsFromServices(services []serviceDescriptor) []uint16 {
	ports := make([]uint16, 0, len(services))
	seen := make(map[uint16]struct{}, len(services))
	for _, s := range services {
		if s.Port == 0 {
			continue
		}
		if _, ok := seen[s.Port]; ok {
			continue
		}
		seen[s.Port] = struct{}{}
		ports = append(ports, s.Port)
	}
	return ports
}

// minimalServicesFromPorts creates bare service descriptors (no guessed names)
// for use when the YAML config specifies ports: but no services:.
func minimalServicesFromPorts(ports []uint16) []serviceDescriptor {
	services := make([]serviceDescriptor, 0, len(ports))
	for _, p := range ports {
		services = append(services, serviceDescriptor{Port: p, Proto: "tcp"})
	}
	return services
}

// parsePortSpecs parses the -ports flag value into service descriptors.
// Each spec is one of:
//
//	<port>                     e.g. 3389
//	<port>:<name>              e.g. 3389:RDP
//	<port>:<name>:<description> e.g. 3389:RDP:Remote Desktop
func parsePortSpecs(s string) []serviceDescriptor {
	var services []serviceDescriptor
	for _, spec := range strings.Split(s, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		parts := strings.SplitN(spec, ":", 3)
		n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || n < 1 || n > 65535 {
			log.Printf("ignoring invalid port spec: %s", spec)
			continue
		}
		svc := serviceDescriptor{Port: uint16(n), Proto: "tcp"}
		if len(parts) >= 2 {
			svc.Name = strings.TrimSpace(parts[1])
		}
		if len(parts) == 3 {
			svc.Description = strings.TrimSpace(parts[2])
		}
		services = append(services, svc)
	}
	return services
}

func runAgent(lg *log.Logger, hubURL string, reg registration, targetHost string) {
	lg.Printf("connecting to hub: %s", hubURL)

	ws, _, err := websocket.DefaultDialer.Dial(hubURL, nil)
	if err != nil {
		lg.Printf("dial failed: %v", err)
		return
	}
	defer ws.Close()

	// Register with hub (include ports/services + metadata for the registry)
	lg.Printf("connected, registering as: %s", reg.MachineID)
	regMsg := controlMessage{
		Type:         "register",
		MachineID:    reg.MachineID,
		DisplayName:  reg.DisplayName,
		Hostname:     reg.Hostname,
		OS:           reg.OS,
		AgentVersion: reg.AgentVersion,
		Tags:         reg.Tags,
		Location:     reg.Location,
		Owner:        reg.Owner,
		Token:        reg.Token,
		Ports:        reg.Ports,
		Services:     reg.Services,
	}
	if err := ws.WriteJSON(&regMsg); err != nil {
		lg.Printf("register failed: %v", err)
		return
	}

	// Read control messages until session-start
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			lg.Printf("hub read error: %v", err)
			return
		}

		var msg controlMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "registered":
			lg.Printf("registered as: %s — waiting for session", msg.MachineID)

		case "session-start":
			helperPubKey := msg.WGPubKey
			if helperPubKey == "" {
				lg.Printf("session-start missing helper public key")
				return
			}
			lg.Printf("session starting — helper pubkey: %s...", helperPubKey[:8])
			handleSession(lg, ws, hubURL, helperPubKey, targetHost, reg.Ports)
			return // reconnect after session ends
		}
	}
}

func handleSession(lg *log.Logger, ws *websocket.Conn, hubURL, helperPubKeyHex, targetHost string, ports []uint16) {
	// Generate ephemeral WireGuard keypair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		lg.Printf("keygen failed: %v", err)
		return
	}
	privKeyHex := hex.EncodeToString(privKey.Bytes())
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	// Send our public key and port list back to helper (via hub relay)
	keyMsg := controlMessage{Type: "wg-pubkey", WGPubKey: pubKeyHex, Ports: ports}
	if err := ws.WriteJSON(&keyMsg); err != nil {
		lg.Printf("failed to send pubkey: %v", err)
		return
	}
	lg.Printf("sent agent pubkey: %s...", pubKeyHex[:8])

	// Create netstack TUN (pure userspace — no admin needed)
	tunDev, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(agentIP)},
		nil, // no DNS
		mtu,
	)
	if err != nil {
		lg.Printf("netstack creation failed: %v", err)
		return
	}

	// Create wsBind — WireGuard datagrams go through the WebSocket
	bind := wsbind.New(ws, 256)

	// Create WireGuard device
	wgVerbose := silentLogger{}.Printf
	if verbose {
		wgVerbose = lg.Printf
	}
	logger := &device.Logger{
		Verbosef: wgVerbose,
		Errorf:   lg.Printf,
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
		lg.Printf("WireGuard IPC config failed: %v", err)
		dev.Close()
		return
	}

	if err := dev.Up(); err != nil {
		lg.Printf("WireGuard device up failed: %v", err)
		dev.Close()
		return
	}
	lg.Printf("WireGuard tunnel up — agent=%s helper=%s", agentIP, helperIP)

	// Start reader goroutine: WebSocket binary → wsBind.RecvCh
	done := make(chan struct{})
	go func() {
		defer close(done)
		wsReader(lg, ws, bind, hubURL)
	}()

	// Listen on each port inside netstack
	var listeners []net.Listener
	for _, port := range ports {
		listenAddr := netip.AddrPortFrom(netip.MustParseAddr(agentIP), port)
		listener, err := tnet.ListenTCPAddrPort(listenAddr)
		if err != nil {
			lg.Printf("netstack listen failed on %s: %v", listenAddr, err)
			continue
		}
		listeners = append(listeners, listener)
		lg.Printf("forwarding %s → %s:%d", listenAddr, targetHost, port)

		// Accept connections from helper through the WireGuard tunnel
		go func(l net.Listener, p uint16) {
			for {
				nsConn, err := l.Accept()
				if err != nil {
					return // listener closed
				}
				logVerbose(lg, "tunnel→%s:%d from %s", targetHost, p, nsConn.RemoteAddr())
				go proxyToTarget(lg, nsConn, targetHost, int(p))
			}
		}(listener, port)
	}

	// Wait for WebSocket to close (session end)
	<-done
	lg.Println("session ended — tearing down WireGuard")
	for _, l := range listeners {
		l.Close()
	}
	dev.Close()
}

// wsReader reads from the WebSocket and dispatches:
//   - Binary messages → wsBind.RecvCh (WireGuard datagrams)
//   - Text messages → parsed for control commands (udp-offer, peer-endpoint), else logged
func wsReader(lg *log.Logger, ws *websocket.Conn, bind *wsbind.Bind, hubURL string) {
	for {
		msgType, data, err := ws.ReadMessage()
		if err != nil {
			lg.Printf("ws read error: %v", err)
			return
		}
		if msgType == websocket.BinaryMessage {
			select {
			case bind.RecvCh <- data:
			default:
				lg.Printf("wsBind recv buffer full, dropping %dB", len(data))
			}
		} else {
			var msg controlMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				logVerbose(lg, "text message during session: %s", string(data))
				continue
			}
			switch msg.Type {
			case "udp-offer":
				if !bind.UDPActive() {
					lg.Printf("received UDP relay offer (port %d)", msg.Port)
					tryUDPUpgrade(lg, bind, hubURL, msg.Token, msg.Port)
					if bind.UDPActive() {
						go tryDirectUpgrade(lg, bind, hubURL)
					}
				}
			case "peer-endpoint":
				if bind.UDPActive() && !bind.DirectActive() {
					lg.Printf("received peer endpoint: %s", msg.Message)
					go func() {
						if err := bind.AttemptDirect(msg.Message); err != nil {
							lg.Printf("direct tunnel failed (staying on relay): %v", err)
						}
					}()
				}
			default:
				logVerbose(lg, "text message during session: %s", string(data))
			}
		}
	}
}

// tryUDPUpgrade attempts to switch from WebSocket to UDP relay transport.
func tryUDPUpgrade(lg *log.Logger, bind *wsbind.Bind, hubURL, tokenHex string, port int) {
	u, err := url.Parse(hubURL)
	if err != nil {
		lg.Printf("UDP upgrade: cannot parse hub URL: %v", err)
		return
	}
	token, err := hex.DecodeString(tokenHex)
	if err != nil {
		lg.Printf("UDP upgrade: invalid token: %v", err)
		return
	}
	if err := bind.UpgradeUDP(u.Hostname(), port, token); err != nil {
		lg.Printf("UDP upgrade failed (continuing on WebSocket): %v", err)
	}
}

// tryDirectUpgrade performs STUN discovery and sends our reflexive
// address to the peer via the hub relay for hole punching.
func tryDirectUpgrade(lg *log.Logger, bind *wsbind.Bind, hubURL string) {
	reflexive, err := bind.STUNDiscover()
	if err != nil {
		lg.Printf("STUN discovery failed (staying on relay): %v", err)
		return
	}
	lg.Printf("STUN reflexive address: %s", reflexive)

	msg := controlMessage{Type: "peer-endpoint", Message: reflexive}
	data, _ := json.Marshal(msg)
	if err := bind.SendText(data); err != nil {
		lg.Printf("failed to send peer-endpoint: %v", err)
	}
}

// proxyToTarget connects to the real target service and pipes data bidirectionally.
func proxyToTarget(lg *log.Logger, nsConn net.Conn, targetHost string, targetPort int) {
	defer nsConn.Close()

	addr := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	targetConn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		lg.Printf("target dial failed (%s): %v", addr, err)
		return
	}
	defer targetConn.Close()

	if tc, ok := targetConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	logVerbose(lg, "proxying tunnel ↔ %s", addr)

	var wg sync.WaitGroup
	wg.Add(2)

	// tunnel (netstack) → target
	go func() {
		defer wg.Done()
		n, _ := io.Copy(targetConn, nsConn)
		logVerbose(lg, "tunnel→target closed (%d bytes)", n)
		if tc, ok := targetConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// target → tunnel (netstack)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(nsConn, targetConn)
		logVerbose(lg, "target→tunnel closed (%d bytes)", n)
	}()

	wg.Wait()
	logVerbose(lg, "proxy session ended for %s", addr)
}

func logVerbose(lg *log.Logger, format string, args ...any) {
	if verbose {
		lg.Printf(format, args...)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

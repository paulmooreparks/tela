/*
tela — Tela Client

Purpose:

	Connects to a Tela Hub via WebSocket, performs a WireGuard key exchange
	with the target daemon, and establishes an encrypted L3 tunnel.

	Subcommands:
	  tela connect  — connect to a machine (ad-hoc or via profile)
	  tela machines — list registered machines and their services
	  tela services — list services on a specific machine
	  tela status   — show hub status summary

	Service selection (connect):
	  -ports    — comma-separated port numbers or local:remote pairs
	  -services — comma-separated service names (resolved via hub API)
	  -profile  — load a named connection profile (multiple hubs in parallel)

	Environment variables (provide defaults so flags can be omitted):
	  TELA_HUB      — hub WebSocket URL
	  TELA_MACHINE  — target machine ID
	  TELA_TOKEN    — authentication token

Network (per-session addressing):

	Agent IP:  10.77.{N}.1/32   (N = session index, 1-254)
	Client IP: 10.77.{N}.2/32
*/
package main

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

const (
	mtu = 1420
)

type controlMessage struct {
	Type       string   `json:"type"`
	MachineID  string   `json:"machineId,omitempty"`
	Message    string   `json:"message,omitempty"`
	WGPubKey   string   `json:"wgPubKey,omitempty"`
	Ports      []uint16 `json:"ports,omitempty"`
	Token      string   `json:"token,omitempty"`
	Port       int      `json:"port,omitempty"` // single port (udp-offer)
	SessionIdx int      `json:"sessionIdx,omitempty"`
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
	case "login":
		cmdLogin(os.Args[2:])
	case "logout":
		cmdLogout(os.Args[2:])
	case "admin":
		cmdAdmin(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("tela %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
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
  login     Authenticate with a Tela portal
  logout    Remove stored portal credentials
  admin     Remote hub auth management (tokens, ACLs)
  version   Print version and exit

Environment Variables:
  TELA_HUB      Default hub URL or alias  (overridden by -hub)
  TELA_MACHINE  Default machine ID        (overridden by -machine)
  TELA_TOKEN    Default auth token        (overridden by -token)

  When set, these provide defaults so flags can be omitted.

Hub Name Resolution:
  The -hub flag accepts a full URL (wss://...) or a short hub name.
  Short names are resolved by querying the portal you logged into:

    tela login https://awansatu.net   # authenticate once
    tela connect -hub owlsnest -machine barn

  Names can also be defined locally in a config file (used as fallback):
    Linux/macOS:  ~/.tela/hubs.yaml
    Windows:      %%APPDATA%%\tela\hubs.yaml

  Config file format:
    hubs:
      myHub: wss://example.com

Examples:
  tela login https://awansatu.net
  tela connect  -hub owlsnest -machine barn
  tela machines -hub owlsnest
  tela connect  -hub wss://tela.awansatu.net -machine barn

  # Select specific ports or services:
  tela connect -hub owlsnest -machine barn -ports 22,5432
  tela connect -hub owlsnest -machine barn -ports 2222:22,15432:5432
  tela connect -hub owlsnest -machine barn -services ssh,postgres

  # Use a connection profile (all connections in parallel):
  tela connect -profile mixed-env

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
	portsFlag := fs.String("ports", "", "Comma-separated ports or local:remote pairs (e.g. 22,2222:22,5432)")
	servicesFlag := fs.String("services", "", "Comma-separated service names (e.g. ssh,postgres)")
	profileFlag := fs.String("profile", "", "Connection profile name (from ~/.tela/profiles/<name>.yaml)")
	// Legacy single-port flags (kept for backward compat)
	localPort := fs.Int("port", 0, "Local TCP port (legacy; prefer -ports)")
	targetPort := fs.Int("target-port", 0, "Target port on daemon (legacy; with -port)")
	fs.BoolVar(&verbose, "v", false, "Verbose logging")
	fs.Parse(args)

	// ── Profile mode: load YAML and run parallel connections ──
	if *profileFlag != "" {
		runProfile(*profileFlag)
		return
	}

	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" || *machineID == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub and -machine are required (or set TELA_HUB / TELA_MACHINE)")
		fs.Usage()
		os.Exit(1)
	}

	// Build port mappings from flags
	mappings, err := buildMappings(*portsFlag, *servicesFlag, *localPort, *targetPort, *hubURL, *machineID, *token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		os.Exit(0)
	}()

	for {
		if err := runSession(*hubURL, *machineID, *token, mappings); errors.Is(err, errFatal) {
			os.Exit(1)
		}
		log.Println("reconnecting in 3 seconds...")
		time.Sleep(3 * time.Second)
	}
}

// ── Port/service mapping helpers ────────────────────────────────────

// buildMappings consolidates -ports, -services, and legacy -port/-target-port
// flags into a single []portMapping. Service names are resolved via the hub API.
func buildMappings(portsFlag, servicesFlag string, legacyLocal, legacyTarget int, hubURL, machineID, token string) ([]portMapping, error) {
	var mappings []portMapping

	// Legacy single-port mode
	if legacyLocal != 0 {
		if legacyTarget == 0 {
			legacyTarget = legacyLocal
		}
		mappings = append(mappings, portMapping{local: uint16(legacyLocal), remote: uint16(legacyTarget)})
	}

	// Parse -ports flag: "22,2222:22,5432" → [{22,22},{2222,22},{5432,5432}]
	if portsFlag != "" {
		parsed, err := parsePorts(portsFlag)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, parsed...)
	}

	// Resolve -services flag: "ssh,postgres" → port numbers via hub API
	if servicesFlag != "" {
		resolved, err := resolveServiceNames(servicesFlag, hubURL, machineID, token)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, resolved...)
	}

	return mappings, nil
}

// parsePorts parses a comma-separated port spec: "22,2222:22,5432"
// Each element is either "port" (local=remote) or "local:remote".
func parsePorts(spec string) ([]portMapping, error) {
	var mappings []portMapping
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, ":"); idx >= 0 {
			local, err := parsePort(part[:idx])
			if err != nil {
				return nil, fmt.Errorf("invalid local port in %q: %w", part, err)
			}
			remote, err := parsePort(part[idx+1:])
			if err != nil {
				return nil, fmt.Errorf("invalid remote port in %q: %w", part, err)
			}
			mappings = append(mappings, portMapping{local: local, remote: remote})
		} else {
			p, err := parsePort(part)
			if err != nil {
				return nil, fmt.Errorf("invalid port %q: %w", part, err)
			}
			mappings = append(mappings, portMapping{local: p, remote: p})
		}
	}
	return mappings, nil
}

// parsePort converts a string to a uint16 port number.
func parsePort(s string) (uint16, error) {
	s = strings.TrimSpace(s)
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q is not a valid port number", s)
	}
	return uint16(n), nil
}

// resolveServiceNames queries the hub's /api/status endpoint to translate
// service names (e.g. "ssh,postgres") into port mappings.
func resolveServiceNames(servicesFlag, hubURL, machineID, token string) ([]portMapping, error) {
	names := strings.Split(servicesFlag, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}

	// Fetch machine's service list from the hub
	data, err := fetchHubStatusWithToken(hubURL, token)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve service names: %w", err)
	}

	var machine *hubMachine
	for i := range data.Machines {
		if data.Machines[i].ID == machineID {
			machine = &data.Machines[i]
			break
		}
	}
	if machine == nil {
		return nil, fmt.Errorf("machine %q not found on hub (cannot resolve service names)", machineID)
	}

	// Build name→port lookup (case-insensitive)
	svcMap := make(map[string]int) // lowercase name → port
	for _, s := range machine.Services {
		if s.Name != "" {
			svcMap[strings.ToLower(s.Name)] = s.Port
		}
		if s.Label != "" {
			svcMap[strings.ToLower(s.Label)] = s.Port
		}
	}

	var mappings []portMapping
	for _, name := range names {
		if name == "" {
			continue
		}
		port, ok := svcMap[strings.ToLower(name)]
		if !ok {
			// List available services in error message
			var available []string
			for _, s := range machine.Services {
				available = append(available, fmt.Sprintf("%s:%d", serviceLabel(s), s.Port))
			}
			return nil, fmt.Errorf("service %q not found on machine %q (available: %s)", name, machineID, strings.Join(available, ", "))
		}
		mappings = append(mappings, portMapping{local: uint16(port), remote: uint16(port)})
	}
	return mappings, nil
}

// fetchHubStatusWithToken queries /api/status with an optional auth token.
func fetchHubStatusWithToken(hubURL, token string) (*hubStatusResponse, error) {
	apiURL := wsToHTTP(hubURL) + "/api/status"
	if token != "" {
		apiURL += "?token=" + url.QueryEscape(token)
	}

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

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("hub returned 401 unauthorized — check your -token")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("hub returned HTTP %d", resp.StatusCode)
	}

	var data hubStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid JSON from hub: %w", err)
	}
	return &data, nil
}

// ── Connection profiles ─────────────────────────────────────────────

// connectionProfile is the YAML schema for ~/.tela/profiles/<name>.yaml.
type connectionProfile struct {
	Connections []profileConnection `yaml:"connections"`
}

// profileConnection defines one hub+machine+services entry in a profile.
type profileConnection struct {
	Hub      string           `yaml:"hub"`
	Machine  string           `yaml:"machine"`
	Token    string           `yaml:"token"`
	Services []profileService `yaml:"services,omitempty"`
}

// profileService defines a port mapping within a profile connection.
type profileService struct {
	Remote int    `yaml:"remote"`          // required: remote port
	Local  int    `yaml:"local,omitempty"` // optional: local port (defaults to remote)
	Name   string `yaml:"name,omitempty"`  // alternative: resolve by service name
}

// profilesDir returns the directory containing connection profiles.
func profilesDir() string {
	return filepath.Join(telaConfigDir(), "profiles")
}

// loadProfile reads a connection profile from ~/.tela/profiles/<name>.yaml.
func loadProfile(name string) (*connectionProfile, error) {
	// Try exact path first, then profiles directory
	path := name
	if !strings.Contains(name, string(filepath.Separator)) && !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		path = filepath.Join(profilesDir(), name+".yaml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read profile %q: %w", path, err)
	}

	// Expand environment variables in the YAML (for ${TOKEN} references)
	expanded := os.ExpandEnv(string(data))

	var profile connectionProfile
	if err := yaml.Unmarshal([]byte(expanded), &profile); err != nil {
		return nil, fmt.Errorf("invalid profile %q: %w", path, err)
	}
	if len(profile.Connections) == 0 {
		return nil, fmt.Errorf("profile %q has no connections defined", path)
	}
	return &profile, nil
}

// runProfile loads a connection profile and runs all connections in parallel.
// It blocks until all connections exit (Ctrl+C kills the process via signal handler).
func runProfile(name string) {
	profile, err := loadProfile(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down all connections")
		os.Exit(0)
	}()

	log.Printf("loaded profile with %d connection(s)", len(profile.Connections))

	var wg sync.WaitGroup
	for i, conn := range profile.Connections {
		hubURL := mustResolveHub(conn.Hub)
		token := conn.Token
		machine := conn.Machine

		if hubURL == "" || machine == "" {
			log.Printf("[profile:%d] skipping: hub and machine are required", i+1)
			continue
		}

		// Build mappings from profile services
		var mappings []portMapping
		var serviceNames []string
		for _, svc := range conn.Services {
			if svc.Name != "" {
				serviceNames = append(serviceNames, svc.Name)
			} else if svc.Remote > 0 {
				local := svc.Local
				if local == 0 {
					local = svc.Remote
				}
				mappings = append(mappings, portMapping{local: uint16(local), remote: uint16(svc.Remote)})
			}
		}

		// Resolve service names to ports
		if len(serviceNames) > 0 {
			resolved, err := resolveServiceNames(strings.Join(serviceNames, ","), hubURL, machine, token)
			if err != nil {
				log.Printf("[profile:%d] %v", i+1, err)
				continue
			}
			mappings = append(mappings, resolved...)
		}

		wg.Add(1)
		go func(idx int, hub, mach, tok string, maps []portMapping) {
			defer wg.Done()
			log.Printf("[profile:%d] connecting to %s → %s", idx, hub, mach)
			for {
				if err := runSession(hub, mach, tok, maps); errors.Is(err, errFatal) {
					log.Printf("[profile:%d] fatal error, stopping", idx)
					return
				}
				log.Printf("[profile:%d] reconnecting in 3 seconds...", idx)
				time.Sleep(3 * time.Second)
			}
		}(i+1, hubURL, machine, token, mappings)
	}

	wg.Wait()
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
	SessionCount   int          `json:"sessionCount"`
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Auth token (env: TELA_TOKEN)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub is required (or set TELA_HUB)")
		fs.Usage()
		os.Exit(1)
	}

	data, err := fetchHubStatusWithToken(*hubURL, *token)
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
		if m.SessionCount > 0 {
			sess = fmt.Sprintf("%d active", m.SessionCount)
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Auth token (env: TELA_TOKEN)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" || *machineID == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub and -machine are required (or set TELA_HUB / TELA_MACHINE)")
		fs.Usage()
		os.Exit(1)
	}

	data, err := fetchHubStatusWithToken(*hubURL, *token)
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Auth token (env: TELA_TOKEN)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	*hubURL = mustResolveHub(*hubURL)

	if *hubURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub is required (or set TELA_HUB)")
		fs.Usage()
		os.Exit(1)
	}

	data, err := fetchHubStatusWithToken(*hubURL, *token)
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
		if m.SessionCount > 0 {
			sessions += m.SessionCount
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

// wsToHTTP converts a ws:// or wss:// URL to http:// or https://.
func wsToHTTP(wsURL string) string {
	s := strings.Replace(wsURL, "wss://", "https://", 1)
	s = strings.Replace(s, "ws://", "http://", 1)
	return strings.TrimRight(s, "/")
}

// errFatal is returned by runSession when the error is not worth retrying.
var errFatal = fmt.Errorf("fatal")

func runSession(hubURL, machineID, token string, overrideMappings []portMapping) error {
	log.Printf("connecting to hub: %s", hubURL)

	// Connect to Hub via WebSocket
	wsConn, _, err := websocket.DefaultDialer.Dial(hubURL, nil)
	if err != nil {
		log.Printf("websocket dial failed: %v", err)
		return err
	}
	defer wsConn.Close()

	// Generate ephemeral WireGuard keypair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Printf("keygen failed: %v", err)
		return err
	}
	privKeyHex := hex.EncodeToString(privKey.Bytes())
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	log.Printf("connected, requesting session for: %s", machineID)
	logVerbose("client pubkey: %s...", pubKeyHex[:8])

	// Send connect request with our public key and token
	connectMsg := controlMessage{Type: "connect", MachineID: machineID, WGPubKey: pubKeyHex, Token: token}
	if len(overrideMappings) > 0 {
		// Hint to the hub which service port(s) this session intends to use.
		// (When not provided, the hub treats the session as covering all advertised services.)
		ports := make([]uint16, 0, len(overrideMappings))
		for _, m := range overrideMappings {
			ports = append(ports, m.remote)
		}
		connectMsg.Ports = ports
	}
	if err := wsConn.WriteJSON(&connectMsg); err != nil {
		log.Printf("failed to send connect: %v", err)
		return nil
	}

	// Wait for "ready" and daemon's public key + port list
	var agentPubKeyHex string
	var agentPorts []uint16
	var udpTokenHex string
	var udpPort int
	var sessionIdx int
	ready := false

	for {
		_, rawMsg, err := wsConn.ReadMessage()
		if err != nil {
			log.Printf("failed reading from hub: %v", err)
			return nil
		}

		var msg controlMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "ready":
			ready = true
			sessionIdx = msg.SessionIdx
			log.Printf("tunnel ready signal received (session %d)", sessionIdx)
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
			lower := strings.ToLower(msg.Message)
			if strings.Contains(lower, "not found") || strings.Contains(lower, "invalid token") {
				// Provide actionable diagnostics
				if strings.Contains(lower, "not found") {
					httpURL := wsToHTTP(hubURL)
					log.Printf("machine %q is not registered on this hub", machineID)
					log.Printf("check available machines: tela machines -hub %s", httpURL)
					log.Printf("or run: curl %s/api/status", httpURL)
				}
				return errFatal
			}
			return fmt.Errorf("hub error: %s", msg.Message)
		}

		// Need both ready and agent pubkey to proceed
		if ready && agentPubKeyHex != "" {
			break
		}
	}

	// Per-session IP addressing: 10.77.{idx}.1 (agent) / 10.77.{idx}.2 (client)
	subnet := sessionIdx
	if subnet < 1 {
		subnet = 1
	}
	if subnet > 254 {
		subnet = 254
	}
	sessionAgentIP := fmt.Sprintf("10.77.%d.1", subnet)
	sessionHelperIP := fmt.Sprintf("10.77.%d.2", subnet)

	// Create netstack TUN (pure userspace, no admin)
	tunDev, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(sessionHelperIP)},
		nil, // no DNS
		mtu,
	)
	if err != nil {
		log.Printf("netstack creation failed: %v", err)
		return nil
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
`, privKeyHex, agentPubKeyHex, sessionAgentIP)

	if err := dev.IpcSet(ipcConf); err != nil {
		log.Printf("WireGuard IPC config failed: %v", err)
		dev.Close()
		return nil
	}

	if err := dev.Up(); err != nil {
		log.Printf("WireGuard device up failed: %v", err)
		dev.Close()
		return nil
	}
	log.Printf("WireGuard tunnel up — client=%s daemon=%s", sessionHelperIP, sessionAgentIP)

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
		return nil
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
				go handleNetstackClient(tnet, localConn, int(remote), sessionAgentIP)
			}
		}(listener, m.remote)
	}

	if len(listeners) == 0 {
		log.Printf("no ports could be bound")
		dev.Close()
		return nil
	}

	// Wait for WebSocket to close (session end) or signal
	<-done
	log.Println("session ended — cleaning up")
	for _, l := range listeners {
		l.Close()
	}
	dev.Close()
	return nil
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
func handleNetstackClient(tnet *netstack.Net, localConn net.Conn, targetPort int, sessionAgentIP string) {
	defer localConn.Close()

	// Dial through the WireGuard tunnel to the agent's netstack
	agentAddr := netip.AddrPortFrom(netip.MustParseAddr(sessionAgentIP), uint16(targetPort))
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

// telaConfig is the schema for ~/.tela/config.yaml (or %APPDATA%\tela\config.yaml).
type telaConfig struct {
	Portal struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"portal"`
}

// telaConfigDir returns the platform-appropriate tela config directory.
func telaConfigDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "tela")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tela")
}

// hubsConfigPath returns the platform-appropriate path to the hubs config file.
func hubsConfigPath() string {
	return filepath.Join(telaConfigDir(), "hubs.yaml")
}

// telaConfigPath returns the platform-appropriate path to the main config file.
func telaConfigPath() string {
	return filepath.Join(telaConfigDir(), "config.yaml")
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

// loadTelaConfig reads and parses the main tela config file.
func loadTelaConfig() (*telaConfig, error) {
	data, err := os.ReadFile(telaConfigPath())
	if err != nil {
		return nil, err
	}
	var cfg telaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", telaConfigPath(), err)
	}
	return &cfg, nil
}

// saveTelaConfig writes the main tela config file.
func saveTelaConfig(cfg *telaConfig) error {
	dir := telaConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(telaConfigPath(), data, 0600)
}

// portalHubEntry matches the JSON shape from the portal /api/hubs endpoint.
type portalHubEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// resolveHubFromPortal queries the authenticated portal to resolve a hub name.
func resolveHubFromPortal(name string) (string, error) {
	cfg, err := loadTelaConfig()
	if err != nil || cfg.Portal.URL == "" {
		return "", fmt.Errorf("not logged in to a portal (run 'tela login <url>')")
	}

	apiURL := strings.TrimRight(cfg.Portal.URL, "/") + "/api/hubs"
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	if cfg.Portal.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Portal.Token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach portal %s: %w", cfg.Portal.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("portal returned 401 unauthorized — try 'tela login %s'", cfg.Portal.URL)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("portal returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Hubs []portalHubEntry `json:"hubs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid JSON from portal: %w", err)
	}

	for _, h := range result.Hubs {
		if strings.EqualFold(h.Name, name) {
			// Convert https:// → wss://, http:// → ws://
			wsURL := strings.Replace(h.URL, "https://", "wss://", 1)
			wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
			logVerbose("resolved hub %q via portal → %s", name, wsURL)
			return wsURL, nil
		}
	}

	var known []string
	for _, h := range result.Hubs {
		known = append(known, h.Name)
	}
	return "", fmt.Errorf("hub %q not found on portal (known: %s)", name, strings.Join(known, ", "))
}

// resolveHubURL resolves a hub reference to a WebSocket URL.
// If the value starts with ws:// or wss://, it is used as-is.
// Otherwise it is resolved via: (1) portal API, (2) local hubs.yaml fallback.
func resolveHubURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") {
		return value, nil
	}

	// Try portal first
	if resolved, err := resolveHubFromPortal(value); err == nil {
		return resolved, nil
	} else {
		logVerbose("portal lookup failed: %v", err)
	}

	// Fallback to local hubs.yaml
	cfg, err := loadHubsConfig()
	if err != nil {
		return "", fmt.Errorf("hub %q is not a URL and could not be resolved (no portal login, no local config)", value)
	}

	hubURL, ok := cfg.Hubs[value]
	if !ok {
		var known []string
		for k := range cfg.Hubs {
			known = append(known, k)
		}
		return "", fmt.Errorf("hub %q not found (portal unreachable, not in %s; known: %s)", value, hubsConfigPath(), strings.Join(known, ", "))
	}
	logVerbose("resolved hub alias %q via local config → %s", value, hubURL)
	return hubURL, nil
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

// ── "tela login" ───────────────────────────────────────────────────

func cmdLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela login <portal-url>")
		fmt.Fprintln(os.Stderr, "Example: tela login https://awansatu.net")
		os.Exit(1)
	}

	portalURL := strings.TrimRight(fs.Arg(0), "/")

	// Validate URL
	if !strings.HasPrefix(portalURL, "http://") && !strings.HasPrefix(portalURL, "https://") {
		portalURL = "https://" + portalURL
	}

	// Prompt for token
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Token for %s (press Enter for none): ", portalURL)
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)

	// Test the connection
	fmt.Printf("Verifying portal... ")
	apiURL := portalURL + "/api/hubs"
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: could not reach %s: %v\n", portalURL, err)
		os.Exit(1)
	}
	resp.Body.Close()

	if resp.StatusCode == 401 {
		fmt.Fprintln(os.Stderr, "\nError: unauthorized — check your token")
		os.Exit(1)
	}
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "\nError: portal returned HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Println("ok")

	// Save config
	var cfg telaConfig
	cfg.Portal.URL = portalURL
	cfg.Portal.Token = token
	if err := saveTelaConfig(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Logged in to %s\n", portalURL)
	fmt.Printf("Config saved to %s\n", telaConfigPath())
}

// ── "tela logout" ──────────────────────────────────────────────────

func cmdLogout(args []string) {
	path := telaConfigPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Println("Not logged in.")
		return
	}

	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Logged out. Portal credentials removed.")
}

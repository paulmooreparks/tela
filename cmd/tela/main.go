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
	mathrand "math/rand"
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
	"github.com/paulmooreparks/tela/internal/service"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

const (
	mtu            = 1420
	wsPingInterval = 20 * time.Second
	wsPongWait     = 45 * time.Second
	wsWriteWait    = 5 * time.Second
)

type controlMessage struct {
	Type       string   `json:"type"`
	MachineID  string   `json:"machineId,omitempty"`
	Message    string   `json:"message,omitempty"`
	WGPubKey   string   `json:"wgPubKey,omitempty"`
	Ports      []uint16 `json:"ports,omitempty"`
	Token      string   `json:"token,omitempty"`
	Port       int      `json:"port,omitempty"` // single port (udp-offer)
	Host       string   `json:"host,omitempty"` // explicit UDP host (when hub is behind proxy)
	SessionIdx int      `json:"sessionIdx,omitempty"`
}

// Well-known port names for friendly display.
var portNames = map[uint16]string{
	22: "SSH", 80: "HTTP", 443: "HTTPS", 3389: "RDP",
	5900: "VNC", 8080: "HTTP", 8443: "HTTPS",
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
	case "remote":
		cmdRemote(os.Args[2:])
	case "login":
		cmdLogin(os.Args[2:])
	case "logout":
		cmdLogout(os.Args[2:])
	case "admin":
		cmdAdmin(os.Args[2:])
	case "service":
		handleServiceCommand()
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
  remote    Manage hub directory remotes (add, remove, list)
  admin     Remote hub auth management (tokens, ACLs)
  service   Manage tela as an OS service (install, start, stop, etc.)
  version   Print version and exit

Environment Variables:
  TELA_HUB      Default hub URL or alias  (overridden by -hub)
  TELA_MACHINE  Default machine ID        (overridden by -machine)
  TELA_TOKEN    Default auth token        (overridden by -token)

  When set, these provide defaults so flags can be omitted.

Hub Name Resolution:
  The -hub flag accepts a full URL (wss://...) or a short hub name.
  Short names are resolved by querying configured remotes:

    tela remote add awansaya https://awansaya.net  # add a remote once
    tela connect -hub myhub -machine mybox

  Resolution order: configured remotes (sorted by name), then local config.
  Names can also be defined locally in a config file (used as fallback):
    Linux/macOS:  ~/.tela/hubs.yaml
    Windows:      %%APPDATA%%\tela\hubs.yaml

  Config file format:
    hubs:
      myHub: wss://example.com

Examples:
  tela remote add awansaya https://awansaya.net
  tela connect  -hub myhub -machine mybox
  tela machines -hub myhub
  tela connect  -hub wss://hub.example.com -machine mybox

  # Select specific ports or services:
  tela connect -hub myhub -machine mybox -ports 22,5432
  tela connect -hub myhub -machine mybox -ports 2222:22,15432:5432
  tela connect -hub myhub -machine mybox -services ssh,postgres

  # Use a connection profile (all connections in parallel):
  tela connect -profile mixed-env

  # Or set env vars and skip the flags:
  export TELA_HUB=myhub TELA_MACHINE=mybox
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
	profileFlag := fs.String("profile", envOrDefault("TELA_PROFILE", ""), "Connection profile name (from ~/.tela/profiles/<name>.yaml) (env: TELA_PROFILE)")
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

	stopCh = make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		close(stopCh)
	}()

	const maxDelay = 5 * time.Minute
	attempt := 0
	for {
		err := runSession(*hubURL, *machineID, *token, mappings)
		if errors.Is(err, errFatal) {
			os.Exit(1)
		}
		if err == nil {
			attempt = 0 // successful session — reset backoff
		}

		// Check for shutdown before reconnecting
		select {
		case <-stopCh:
			return
		default:
		}

		delay := reconnectDelay(attempt, maxDelay)
		log.Printf("reconnecting in %s...", delay.Round(time.Second))

		// Wait for delay or shutdown, whichever comes first
		select {
		case <-time.After(delay):
		case <-stopCh:
			return
		}
		attempt++
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
// It blocks until all connections exit or stopCh is closed.
func runProfile(name string) {
	profile, err := loadProfile(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Only initialize stopCh if not already set (service mode sets it externally).
	if stopCh == nil {
		stopCh = make(chan struct{})
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			log.Println("shutting down all connections")
			close(stopCh)
		}()
	}

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
			const maxDelay = 5 * time.Minute
			attempt := 0
			for {
				err := runSession(hub, mach, tok, maps)
				if errors.Is(err, errFatal) {
					log.Printf("[profile:%d] fatal error, stopping", idx)
					return
				}
				if err == nil {
					attempt = 0 // successful session — reset backoff
				}

				// Check for shutdown before reconnecting
				select {
				case <-stopCh:
					log.Printf("[profile:%d] shutdown received", idx)
					return
				default:
				}

				delay := reconnectDelay(attempt, maxDelay)
				log.Printf("[profile:%d] reconnecting in %s...", idx, delay.Round(time.Second))

				// Wait for delay or shutdown, whichever comes first
				select {
				case <-time.After(delay):
				case <-stopCh:
					log.Printf("[profile:%d] shutdown received", idx)
					return
				}
				attempt++
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

func startWSKeepalive(ws *websocket.Conn) func() {
	_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))

	prevPingHandler := ws.PingHandler()
	prevPongHandler := ws.PongHandler()

	ws.SetPingHandler(func(appData string) error {
		logVerbose("ws keepalive: received ping")
		_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))
		if prevPingHandler != nil {
			return prevPingHandler(appData)
		}
		return nil
	})
	ws.SetPongHandler(func(appData string) error {
		logVerbose("ws keepalive: received pong")
		_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))
		if prevPongHandler != nil {
			return prevPongHandler(appData)
		}
		return nil
	})

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				logVerbose("ws keepalive: sending ping")
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
					logVerbose("ws keepalive: ping failed: %v", err)
					return
				}
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
	}
}

// errFatal is returned by runSession when the error is not worth retrying.
var errFatal = fmt.Errorf("fatal")

// errMachineNotFound is returned when the machine isn't registered on the hub.
// This is retryable — the daemon may reconnect at any time.
var errMachineNotFound = fmt.Errorf("machine not found")

// reconnectDelay calculates the next backoff delay with jitter.
// delay doubles each call up to maxDelay; jitter is ±25%.
func reconnectDelay(attempt int, maxDelay time.Duration) time.Duration {
	const baseDelay = 3 * time.Second
	d := baseDelay
	for i := 0; i < attempt; i++ {
		d *= 2
		if d > maxDelay {
			d = maxDelay
			break
		}
	}
	// Add ±25% jitter
	jitter := float64(d) * 0.25 * (2*mathrand.Float64() - 1)
	d += time.Duration(jitter)
	if d < time.Second {
		d = time.Second
	}
	return d
}

func runSession(hubURL, machineID, token string, overrideMappings []portMapping) error {
	log.Printf("connecting to hub: %s", hubURL)

	// Connect to Hub via WebSocket
	wsConn, _, err := websocket.DefaultDialer.Dial(hubURL, nil)
	if err != nil {
		log.Printf("websocket dial failed: %v", err)
		return err
	}
	defer wsConn.Close()
	stopKeepalive := startWSKeepalive(wsConn)
	defer stopKeepalive()

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
	var udpHost string
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
			udpHost = msg.Host
			if udpHost != "" {
				log.Printf("received UDP relay offer (host %s, port %d)", udpHost, udpPort)
			} else {
				log.Printf("received UDP relay offer (port %d)", udpPort)
			}
		case "error":
			log.Printf("hub error: %s", msg.Message)
			lower := strings.ToLower(msg.Message)
			if strings.Contains(lower, "invalid token") {
				return errFatal
			}
			if strings.Contains(lower, "not found") {
				log.Printf("machine %q is not registered on this hub (will retry)", machineID)
				return errMachineNotFound
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
		tryUDPUpgrade(bind, hubURL, udpTokenHex, udpPort, udpHost)
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
					tryUDPUpgrade(bind, hubURL, msg.Token, msg.Port, msg.Host)
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
func tryUDPUpgrade(bind *wsbind.Bind, hubURL, tokenHex string, port int, host string) {
	if host == "" {
		u, err := url.Parse(hubURL)
		if err != nil {
			log.Printf("UDP upgrade: cannot parse hub URL: %v", err)
			return
		}
		host = u.Hostname()
	}
	token, err := hex.DecodeString(tokenHex)
	if err != nil {
		log.Printf("UDP upgrade: invalid token: %v", err)
		return
	}
	if err := bind.UpgradeUDP(host, port, token); err != nil {
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

// ── Service management ─────────────────────────────────────────────

func handleServiceCommand() {
	if len(os.Args) < 3 {
		cfgPath := service.BinaryConfigPath("tela")
		fmt.Fprintf(os.Stderr, `tela service — manage tela as an OS service

Usage:
  tela service install -config <file>  Install service (copies config to system dir)
  tela service uninstall               Remove the service
  tela service start                   Start the installed service
  tela service stop                    Stop the running service
  tela service restart                 Restart the service
  tela service status                  Show service status
  tela service run                     Run in service mode (used by the service manager)

The service reads its configuration from:
  %s

The configuration file uses the connection profile YAML format:
  connections:
    - hub: wss://hub.example.com
      machine: mybox
      token: mytoken
      services:
        - remote: 22
          local: 2222

Edit that file and run "tela service restart" to reconfigure.

Install example:
  tela service install -config myprofile.yaml
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
		fmt.Fprintf(os.Stderr, "usage: tela service install -config <file>\n")
		os.Exit(1)
	}

	// Validate the config file
	absConfig, _ := filepath.Abs(*configPath)
	profile, err := loadProfile(absConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Validate: service mode requires full WebSocket URLs (no hub name resolution)
	for i, conn := range profile.Connections {
		lower := strings.ToLower(conn.Hub)
		if !strings.HasPrefix(lower, "ws://") && !strings.HasPrefix(lower, "wss://") {
			fmt.Fprintf(os.Stderr, "error: connections[%d].hub must be a full WebSocket URL (ws:// or wss://) in service mode, got %q\n", i, conn.Hub)
			fmt.Fprintf(os.Stderr, "Hub name resolution is not available in service mode.\n")
			os.Exit(1)
		}
		// Validate: service mode requires numeric ports, not name-based resolution
		for j, svc := range conn.Services {
			if svc.Name != "" && svc.Remote == 0 {
				fmt.Fprintf(os.Stderr, "error: connections[%d].services[%d] uses name-based resolution (%q) which is not supported in service mode. Use 'remote: <port>' instead.\n", i, j, svc.Name)
				os.Exit(1)
			}
		}
	}

	// Copy the config to the system-wide location
	destPath := service.BinaryConfigPath("tela")
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
		Description: "Tela Client — encrypted tunnel client",
		WorkingDir:  wd,
	}

	if err := service.Install("tela", cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tela service installed successfully")
	fmt.Printf("  config: %s\n", destPath)
	fmt.Println("  start:  tela service start")
	fmt.Println("")
	fmt.Println("Edit the config file and run \"tela service restart\" to reconfigure.")
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return fmt.Errorf("create dir %s: %w", filepath.Dir(dst), err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func serviceUninstall() {
	if err := service.Uninstall("tela"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tela service uninstalled")
	fmt.Printf("  config retained: %s\n", service.BinaryConfigPath("tela"))
}

func serviceStart() {
	if err := service.Start("tela"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tela service started")
}

func serviceStop() {
	if err := service.Stop("tela"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tela service stopped")
}

func serviceRestart() {
	fmt.Println("stopping tela service...")
	_ = service.Stop("tela")
	// Brief pause to let the service fully stop
	time.Sleep(time.Second)
	if err := service.Start("tela"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tela service restarted")
}

func serviceStatus() {
	st, err := service.QueryStatus("tela")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("installed: %v\n", st.Installed)
	fmt.Printf("running:   %v\n", st.Running)
	fmt.Printf("status:    %s\n", st.Info)
	if st.Installed {
		fmt.Printf("config:    %s\n", service.BinaryConfigPath("tela"))
	}
}

// serviceRunDaemon loads the YAML config from the system directory and
// runs tela. It blocks until svcStop is closed.
func serviceRunDaemon(svcStop <-chan struct{}) {
	// Bridge service stop channel to the global stopCh so
	// reconnect loops exit on shutdown.
	stopCh = make(chan struct{})
	go func() {
		<-svcStop
		close(stopCh)
	}()

	log.SetFlags(log.Ltime)
	log.SetPrefix("[tela] ")

	svcCfg, err := service.LoadConfig("tela")
	if err != nil {
		log.Fatalf("service config: %v", err)
	}

	if svcCfg.WorkingDir != "" {
		os.Chdir(svcCfg.WorkingDir)
	}

	// Load the YAML config (profile format) from the system-wide location
	yamlPath := service.BinaryConfigPath("tela")
	profile, err := loadProfile(yamlPath)
	if err != nil {
		log.Fatalf("config %s: %v", yamlPath, err)
	}

	log.Printf("loaded %d connection(s) from %s", len(profile.Connections), yamlPath)

	// Run all connections in parallel
	var wg sync.WaitGroup
	for i, conn := range profile.Connections {
		hubURL := conn.Hub // Already validated as full URL at install time
		token := conn.Token
		machine := conn.Machine

		if hubURL == "" || machine == "" {
			log.Printf("[svc:%d] skipping: hub and machine are required", i+1)
			continue
		}

		// Build mappings from profile services
		var mappings []portMapping
		for _, svc := range conn.Services {
			if svc.Remote > 0 {
				local := svc.Local
				if local == 0 {
					local = svc.Remote
				}
				mappings = append(mappings, portMapping{local: uint16(local), remote: uint16(svc.Remote)})
			}
		}

		wg.Add(1)
		go func(idx int, hub, mach, tok string, maps []portMapping) {
			defer wg.Done()
			log.Printf("[svc:%d] connecting to %s -> %s", idx, hub, mach)
			const maxDelay = 5 * time.Minute
			attempt := 0
			for {
				err := runSession(hub, mach, tok, maps)
				if errors.Is(err, errFatal) {
					log.Printf("[svc:%d] fatal error, stopping", idx)
					return
				}
				if err == nil {
					attempt = 0
				}

				select {
				case <-stopCh:
					log.Printf("[svc:%d] shutdown received", idx)
					return
				default:
				}

				delay := reconnectDelay(attempt, maxDelay)
				log.Printf("[svc:%d] reconnecting in %s...", idx, delay.Round(time.Second))

				select {
				case <-time.After(delay):
				case <-stopCh:
					log.Printf("[svc:%d] shutdown received", idx)
					return
				}
				attempt++
			}
		}(i+1, hubURL, machine, token, mappings)
	}

	// Block until service stop is signaled
	<-svcStop
	log.Println("service stopping")
	wg.Wait()
}

func serviceRun() {
	svcStop := make(chan struct{})

	// Handle signals for non-Windows "service run" (systemd/launchd)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		close(svcStop)
	}()

	serviceRunDaemon(svcStop)
}

func runAsWindowsService() {
	handler := &service.Handler{
		Run: func(svcStopCh <-chan struct{}) {
			serviceRunDaemon(svcStopCh)
		},
	}
	if err := service.RunAsService("tela", handler); err != nil {
		log.Fatalf("service failed: %v", err)
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

// remoteEntry is a named hub directory endpoint (like a git remote).
type remoteEntry struct {
	URL          string `yaml:"url"`
	Token        string `yaml:"token,omitempty"`
	HubDirectory string `yaml:"hub_directory,omitempty"` // discovered via /.well-known/tela
}

// telaConfig is the schema for ~/.tela/config.yaml (or %APPDATA%\tela\config.yaml).
type telaConfig struct {
	// Remotes is the set of named hub directory endpoints.
	Remotes map[string]remoteEntry `yaml:"remotes,omitempty"`

	// Deprecated: Portal is the legacy single-portal config. Migrated to Remotes on load.
	Portal *struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"portal,omitempty"`
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
// If the legacy single-portal block is present, it is migrated to the remotes map.
func loadTelaConfig() (*telaConfig, error) {
	data, err := os.ReadFile(telaConfigPath())
	if err != nil {
		return nil, err
	}
	var cfg telaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", telaConfigPath(), err)
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]remoteEntry)
	}
	// Migrate legacy portal block → remote named "portal"
	if cfg.Portal != nil && cfg.Portal.URL != "" {
		if _, exists := cfg.Remotes["portal"]; !exists {
			cfg.Remotes["portal"] = remoteEntry{URL: cfg.Portal.URL, Token: cfg.Portal.Token}
		}
		cfg.Portal = nil
		// Best-effort save the migrated config
		_ = saveTelaConfig(&cfg)
	}
	return &cfg, nil
}

// saveTelaConfig writes the main tela config file.
func saveTelaConfig(cfg *telaConfig) error {
	dir := telaConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Never persist the legacy portal block
	cfg.Portal = nil
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(telaConfigPath(), data, 0600)
}

// telaWellKnown is the JSON shape returned by /.well-known/tela (RFC 8615).
type telaWellKnown struct {
	HubDirectory string `json:"hub_directory"`
}

// discoverHubDirectory queries /.well-known/tela to discover the hub directory
// endpoint. Returns the path (e.g. "/api/hubs") or falls back to the conventional
// default if the well-known endpoint is not available.
func discoverHubDirectory(baseURL string, token string) string {
	const fallback = "/api/hubs"
	wkURL := strings.TrimRight(baseURL, "/") + "/.well-known/tela"

	req, err := http.NewRequest("GET", wkURL, nil)
	if err != nil {
		logVerbose("well-known discovery failed (request): %v", err)
		return fallback
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logVerbose("well-known discovery failed (network): %v", err)
		return fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logVerbose("well-known discovery: HTTP %d, falling back to %s", resp.StatusCode, fallback)
		return fallback
	}

	var wk telaWellKnown
	if err := json.NewDecoder(resp.Body).Decode(&wk); err != nil {
		logVerbose("well-known discovery: invalid JSON, falling back to %s", fallback)
		return fallback
	}

	if wk.HubDirectory == "" {
		return fallback
	}

	logVerbose("well-known discovery: hub_directory = %s", wk.HubDirectory)
	return wk.HubDirectory
}

// remoteHubEntry matches the JSON shape from a remote hub directory's /api/hubs endpoint.
type remoteHubEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// queryRemote queries a single remote hub directory to resolve a hub name.
func queryRemote(remoteName string, remote remoteEntry, hubName string) (string, error) {
	// Use the discovered hub_directory path, or fall back to convention
	hubDir := remote.HubDirectory
	if hubDir == "" {
		hubDir = "/api/hubs"
	}
	apiURL := strings.TrimRight(remote.URL, "/") + hubDir
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	if remote.Token != "" {
		req.Header.Set("Authorization", "Bearer "+remote.Token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach remote %q (%s): %w", remoteName, remote.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("remote %q returned 401 unauthorized — try 'tela remote add %s %s' with a valid token", remoteName, remoteName, remote.URL)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("remote %q returned HTTP %d", remoteName, resp.StatusCode)
	}

	var result struct {
		Hubs []remoteHubEntry `json:"hubs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid JSON from remote %q: %w", remoteName, err)
	}

	for _, h := range result.Hubs {
		if strings.EqualFold(h.Name, hubName) {
			// Convert https:// → wss://, http:// → ws://
			wsURL := strings.Replace(h.URL, "https://", "wss://", 1)
			wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
			logVerbose("resolved hub %q via remote %q → %s", hubName, remoteName, wsURL)
			return wsURL, nil
		}
	}

	return "", fmt.Errorf("hub %q not found on remote %q", hubName, remoteName)
}

// resolveHubFromRemotes queries all configured remotes (in order) to resolve a hub name.
func resolveHubFromRemotes(name string) (string, error) {
	cfg, err := loadTelaConfig()
	if err != nil || len(cfg.Remotes) == 0 {
		return "", fmt.Errorf("no remotes configured (run 'tela remote add <name> <url>')")
	}

	// Iterate remotes in sorted order for deterministic resolution
	var names []string
	for n := range cfg.Remotes {
		names = append(names, n)
	}
	// sort
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	var lastErr error
	for _, rn := range names {
		resolved, err := queryRemote(rn, cfg.Remotes[rn], name)
		if err == nil {
			return resolved, nil
		}
		logVerbose("remote %q: %v", rn, err)
		lastErr = err
	}

	return "", fmt.Errorf("hub %q not found on any configured remote: %w", name, lastErr)
}

// resolveHubURL resolves a hub reference to a WebSocket URL.
// If the value starts with ws:// or wss://, it is used as-is.
// Otherwise it is resolved via: (1) configured remotes, (2) local hubs.yaml fallback.
func resolveHubURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") {
		return value, nil
	}

	// Try configured remotes first
	if resolved, err := resolveHubFromRemotes(value); err == nil {
		return resolved, nil
	} else {
		logVerbose("remote lookup failed: %v", err)
	}

	// Fallback to local hubs.yaml
	cfg, err := loadHubsConfig()
	if err != nil {
		return "", fmt.Errorf("hub %q is not a URL and could not be resolved (no remotes configured, no local config)", value)
	}

	hubURL, ok := cfg.Hubs[value]
	if !ok {
		var known []string
		for k := range cfg.Hubs {
			known = append(known, k)
		}
		return "", fmt.Errorf("hub %q not found (remotes unreachable, not in %s; known: %s)", value, hubsConfigPath(), strings.Join(known, ", "))
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

// ── "tela remote" ──────────────────────────────────────────────────

func cmdRemote(args []string) {
	if len(args) == 0 {
		cmdRemoteList()
		return
	}

	switch args[0] {
	case "add":
		cmdRemoteAdd(args[1:])
	case "remove", "rm":
		cmdRemoteRemove(args[1:])
	case "list", "ls":
		cmdRemoteList()
	default:
		fmt.Fprintf(os.Stderr, "Unknown remote subcommand: %s\n\n", args[0])
		fmt.Fprintln(os.Stderr, `Usage:
  tela remote                       List configured remotes
  tela remote add <name> <url>      Add a hub directory
  tela remote remove <name>         Remove a hub directory
  tela remote list                  List configured remotes`)
		os.Exit(1)
	}
}

func cmdRemoteAdd(args []string) {
	fs := flag.NewFlagSet("remote add", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tela remote add <name> <url>")
		fmt.Fprintln(os.Stderr, "Example: tela remote add awansaya https://awansaya.net")
		os.Exit(1)
	}

	name := fs.Arg(0)
	remoteURL := strings.TrimRight(fs.Arg(1), "/")

	// Validate URL
	if !strings.HasPrefix(remoteURL, "http://") && !strings.HasPrefix(remoteURL, "https://") {
		remoteURL = "https://" + remoteURL
	}

	// Prompt for token
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Token for %s (press Enter for none): ", remoteURL)
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)

	// Discover hub directory endpoint via /.well-known/tela (RFC 8615)
	fmt.Printf("Discovering %s... ", remoteURL)
	hubDirectory := discoverHubDirectory(remoteURL, token)
	fmt.Printf("%s\n", hubDirectory)

	// Verify the hub directory endpoint
	fmt.Printf("Verifying %s%s... ", remoteURL, hubDirectory)
	apiURL := remoteURL + hubDirectory
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
		fmt.Fprintf(os.Stderr, "\nError: could not reach %s: %v\n", remoteURL, err)
		os.Exit(1)
	}
	resp.Body.Close()

	if resp.StatusCode == 401 {
		fmt.Fprintln(os.Stderr, "\nError: unauthorized — check your token")
		os.Exit(1)
	}
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "\nError: remote returned HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Println("ok")

	// Load config and add/update remote
	cfg, err := loadTelaConfig()
	if err != nil {
		cfg = &telaConfig{Remotes: make(map[string]remoteEntry)}
	}

	cfg.Remotes[name] = remoteEntry{URL: remoteURL, Token: token, HubDirectory: hubDirectory}
	if err := saveTelaConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Remote %q added (%s)\n", name, remoteURL)
	fmt.Printf("Config saved to %s\n", telaConfigPath())
}

func cmdRemoteRemove(args []string) {
	fs := flag.NewFlagSet("remote remove", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela remote remove <name>")
		os.Exit(1)
	}

	name := fs.Arg(0)

	cfg, err := loadTelaConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: no config file found (%s)\n", telaConfigPath())
		os.Exit(1)
	}

	if _, ok := cfg.Remotes[name]; !ok {
		fmt.Fprintf(os.Stderr, "Error: remote %q not found\n", name)
		os.Exit(1)
	}

	delete(cfg.Remotes, name)
	if err := saveTelaConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Remote %q removed.\n", name)
}

func cmdRemoteList() {
	cfg, err := loadTelaConfig()
	if err != nil || len(cfg.Remotes) == 0 {
		fmt.Println("No remotes configured.")
		fmt.Println("Add one with: tela remote add <name> <url>")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tURL\tTOKEN")
	// Sort for deterministic output
	var names []string
	for n := range cfg.Remotes {
		names = append(names, n)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		r := cfg.Remotes[n]
		tokenDisplay := "(none)"
		if r.Token != "" {
			tokenDisplay = "****" + r.Token[max(0, len(r.Token)-4):]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", n, r.URL, tokenDisplay)
	}
	w.Flush()
}

// ── Deprecated: "tela login" / "tela logout" (aliases for remote add/remove) ──

func cmdLogin(args []string) {
	fmt.Fprintln(os.Stderr, "Note: 'tela login' is deprecated. Use 'tela remote add <name> <url>' instead.")
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela login <url>")
		os.Exit(1)
	}

	// Treat as "tela remote add portal <url>"
	cmdRemoteAdd([]string{"portal", fs.Arg(0)})
}

func cmdLogout(args []string) {
	fmt.Fprintln(os.Stderr, "Note: 'tela logout' is deprecated. Use 'tela remote remove <name>' instead.")

	cfg, err := loadTelaConfig()
	if err != nil || len(cfg.Remotes) == 0 {
		fmt.Println("No remotes configured.")
		return
	}

	// Remove the "portal" remote if it exists (legacy behavior)
	if _, ok := cfg.Remotes["portal"]; ok {
		delete(cfg.Remotes, "portal")
		if err := saveTelaConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Remote \"portal\" removed.")
	} else {
		fmt.Println("No remote named \"portal\" found.")
		fmt.Println("Use 'tela remote list' to see configured remotes and 'tela remote remove <name>' to remove one.")
	}
}

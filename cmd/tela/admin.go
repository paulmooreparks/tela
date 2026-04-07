// admin.go -- "tela admin" subcommands for remote hub management
//
// These commands call the hub's /api/admin/* REST endpoints so you can
// manage token identities, machine ACLs, and portal registrations from
// any workstation -- no SSH or shell access to the hub required.
//
// All commands require an owner or admin token passed via -token flag
// or TELA_OWNER_TOKEN environment variable (falls back to TELA_TOKEN).

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/paulmooreparks/tela/internal/credstore"
)

func cmdAdmin(args []string) {
	if len(args) < 1 {
		printAdminUsage()
		os.Exit(1)
	}
	subcmd := args[0]
	rest := args[1:]

	switch subcmd {
	case "access":
		cmdAdminAccess(rest)
	case "agent":
		cmdAdminAgent(rest)
	case "tokens":
		cmdAdminTokens(rest)
	case "portals":
		cmdAdminPortals(rest)
	case "rotate":
		cmdAdminRotate(rest)
	case "pair-code":
		cmdAdminPairCode(rest)
	case "help", "-h", "--help":
		printAdminUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown admin command: %s\n\n", subcmd)
		printAdminUsage()
		os.Exit(1)
	}
}

func printAdminUsage() {
	fmt.Fprintf(os.Stderr, `tela admin -- remote hub administration

Usage:
  tela admin <resource> <action> [options]

Resources:
  access    Per-identity, per-machine permissions (the unified RBAC view)
  agent     Remote agent management through the hub
  tokens    Token identity CRUD
  portals   Portal registrations on the hub

Standalone commands:
  rotate     Regenerate the token for an identity
  pair-code  Generate a one-time pairing code for agent onboarding

Access:
  tela admin access                                List all identities and their permissions
  tela admin access grant <id> <machine> <perms>   Grant permissions (comma-separated: connect,register,manage)
  tela admin access revoke <id> <machine>          Revoke all permissions on a machine
  tela admin access rename <id> <new-id>           Rename an identity
  tela admin access remove <id>                    Remove an identity entirely

Agent:
  tela admin agent list                            List agents registered with the hub
  tela admin agent config -machine <id>            Show an agent's running configuration
  tela admin agent set -machine <id> <json>        Push a partial config update
  tela admin agent logs -machine <id> [-n 100]     Retrieve recent log lines
  tela admin agent restart -machine <id>           Request a graceful restart
  tela admin agent update -machine <id> [-version vX.Y.Z]  Download a new release and restart

Tokens:
  tela admin tokens list                           List all token identities
  tela admin tokens add <id> [-role <role>]        Create a new token identity (returns once)
  tela admin tokens remove <id>                    Remove a token identity

Portals:
  tela admin portals list                          List portal registrations
  tela admin portals add <name> -portal-url <url>  Register the hub with a portal
  tela admin portals remove <name>                 Remove a portal registration

All commands require -hub and -token.
The token must belong to an owner or admin identity.

Token resolution (in order):
  1. -token flag
  2. TELA_OWNER_TOKEN env var
  3. TELA_TOKEN env var

Examples:
  tela admin access -hub gohub -token <owner-token>
  tela admin access grant alice barn connect,manage -hub gohub -token <owner-token>
  tela admin tokens add alice -hub gohub -token <owner-token>
  tela admin tokens add bob -role admin -hub gohub -token <owner-token>
  tela admin rotate alice -hub gohub -token <owner-token>
  tela admin pair-code barn -hub gohub -token <owner-token>

  tela admin portals add awansaya -portal-url https://awansaya.net \
    -hub gohub -token <owner-token>

  tela admin agent list -hub gohub -token <owner-token>
  tela admin agent logs -machine barn -n 200 -hub gohub -token <owner-token>
  tela admin agent restart -machine barn -hub gohub -token <owner-token>
  tela admin agent update -machine barn -hub gohub -token <owner-token>
  tela admin agent update -machine barn -version v0.4.0 -hub gohub -token <owner-token>

Tip: set TELA_OWNER_TOKEN in your shell profile so you don't need -token
every time. Use a separate TELA_TOKEN for day-to-day tela connect usage.
`)
}

// adminTokenDefault returns the best available admin token from env vars.
// Prefers TELA_OWNER_TOKEN, falls back to TELA_TOKEN.
func adminTokenDefault() string {
	if v := os.Getenv("TELA_OWNER_TOKEN"); v != "" {
		return v
	}
	return os.Getenv("TELA_TOKEN")
}

// adminClient is a shared HTTP client for admin API calls, avoiding a
// new client (and TLS handshake) per request.
var adminClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{}},
}

// adminHTTP performs an HTTP request against the hub's admin API.
func adminHTTP(method, hubURL, path, token string, body any) (int, map[string]any, error) {
	apiURL := wsToHTTP(hubURL) + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, apiURL, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := adminClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("could not reach hub: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	var result map[string]any
	_ = json.Unmarshal(respBody, &result)

	return resp.StatusCode, result, nil
}

func adminParseHubAndToken(fs *flag.FlagSet) (string, string) {
	hubURL := fs.Lookup("hub").Value.String()
	token := fs.Lookup("token").Value.String()
	hubURL = mustResolveHub(hubURL)
	if hubURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub is required (or set TELA_HUB)")
		os.Exit(1)
	}
	if token == "" {
		token = credstore.LookupToken(hubURL)
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: -token is required (or set TELA_OWNER_TOKEN / TELA_TOKEN, or use 'tela login')")
		os.Exit(1)
	}
	return hubURL, token
}

// permuteArgs reorders args so that flag-like tokens (starting with "-")
// and their values come before positional arguments.  This lets callers
// write "tela admin add-token alice -hub myhub" instead of requiring all
// flags before the positional name.
//
// Handles: -flag value, -flag=value, and bare positional args.
// The "--" terminator is respected (everything after it stays as-is).
func permuteArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 0 && a[0] == '-' {
			flags = append(flags, a)
			// If the flag is not -flag=value, consume the next arg as its value
			// (unless it's a boolean flag, which has no separate value).
			if !containsEquals(a) && i+1 < len(args) {
				name := a
				for len(name) > 0 && name[0] == '-' {
					name = name[1:]
				}
				if f := fs.Lookup(name); f != nil {
					if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !bf.IsBoolFlag() {
						i++
						flags = append(flags, args[i])
					}
				} else {
					// Unknown flag -- assume it takes a value.
					i++
					if i < len(args) {
						flags = append(flags, args[i])
					}
				}
			}
		} else {
			positional = append(positional, a)
		}
	}
	return append(flags, positional...)
}

func containsEquals(s string) bool {
	for _, c := range s {
		if c == '=' {
			return true
		}
	}
	return false
}

func adminCheckError(status int, result map[string]any) {
	if status >= 200 && status < 300 {
		return
	}
	errMsg := "unknown error"
	if e, ok := result["error"].(string); ok {
		errMsg = e
	}
	fmt.Fprintf(os.Stderr, "Error (HTTP %d): %s\n", status, errMsg)
	os.Exit(1)
}

// ── tela admin list-tokens ─────────────────────────────────────────

func cmdAdminListTokens(args []string) {
	fs := flag.NewFlagSet("admin list-tokens", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	_ = hubURL
	_ = token

	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("GET", hub, "/api/admin/tokens", tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
		return
	}

	tokens, _ := result["tokens"].([]any)
	if len(tokens) == 0 {
		fmt.Println("No auth tokens configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tROLE\tTOKEN PREVIEW")
	for _, t := range tokens {
		entry, ok := t.(map[string]any)
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		role, _ := entry["role"].(string)
		preview, _ := entry["tokenPreview"].(string)
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, role, preview)
	}
	w.Flush()
}

// ── tela admin add-token ───────────────────────────────────────────

func cmdAdminAddToken(args []string) {
	fs := flag.NewFlagSet("admin add-token", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	role := fs.String("role", "", "Role: owner, admin, or omit for user")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin add-token <id> -hub <hub> -token <token> [-role owner|admin]")
		os.Exit(1)
	}
	id := fs.Arg(0)
	hub, tok := adminParseHubAndToken(fs)

	body := map[string]string{"id": id}
	if *role != "" {
		body["role"] = *role
	}

	status, result, err := adminHTTP("POST", hub, "/api/admin/tokens", tok, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	newToken, _ := result["token"].(string)
	fmt.Printf("Added identity '%s'", id)
	if *role != "" {
		fmt.Printf(" (role: %s)", *role)
	}
	fmt.Println()
	fmt.Printf("  Token: %s\n", newToken)
	fmt.Println()
	fmt.Println("SAVE THIS TOKEN -- it will not be shown again.")
	fmt.Println("Change is already active (no hub restart needed).")
}

// ── tela admin remove-token ────────────────────────────────────────

func cmdAdminRemoveToken(args []string) {
	fs := flag.NewFlagSet("admin remove-token", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin remove-token <id> -hub <hub> -token <token>")
		os.Exit(1)
	}
	id := fs.Arg(0)
	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("DELETE", hub, "/api/admin/access/"+url.PathEscape(id), tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Removed identity '%s' and cleaned up machine ACLs.\n", id)
	fmt.Println("Change is already active (no hub restart needed).")
}

// ── tela admin rotate ──────────────────────────────────────────────

func cmdAdminRotate(args []string) {
	fs := flag.NewFlagSet("admin rotate", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin rotate <id> -hub <hub> -token <token>")
		os.Exit(1)
	}
	id := fs.Arg(0)
	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("POST", hub, "/api/admin/rotate/"+id, tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	newToken, _ := result["token"].(string)
	fmt.Printf("Rotated token for '%s'.\n", id)
	fmt.Printf("  New token: %s\n", newToken)
	fmt.Println()
	fmt.Println("SAVE THIS TOKEN -- it will not be shown again.")
	fmt.Println("Change is already active (no hub restart needed).")
}

// ── tela admin list-portals ──────────────────────────────────────

func cmdAdminListPortals(args []string) {
	fs := flag.NewFlagSet("admin list-portals", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)
	_ = hubURL
	_ = token

	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("GET", hub, "/api/admin/portals", tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
		return
	}

	portals, _ := result["portals"].([]any)
	if len(portals) == 0 {
		fmt.Println("No portals configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tURL\tSYNC TOKEN")
	for _, p := range portals {
		entry, ok := p.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		url, _ := entry["url"].(string)
		hasSyncToken, _ := entry["hasSyncToken"].(bool)
		syncStatus := "no"
		if hasSyncToken {
			syncStatus = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, url, syncStatus)
	}
	w.Flush()
}

// ── tela admin add-portal ────────────────────────────────────────

func cmdAdminAddPortal(args []string) {
	fs := flag.NewFlagSet("admin add-portal", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	portalURL := fs.String("portal-url", "", "Portal URL (e.g. https://awansaya.net)")
	portalToken := fs.String("portal-token", "", "Portal admin API token (used once, not stored on hub)")
	portalHubURL := fs.String("hub-url", "", "Hub's public URL for portal registration (defaults to -hub)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin add-portal <name> -hub <hub> -token <token> -portal-url <url> [-hub-url <url>] [-portal-token <token>]")
		os.Exit(1)
	}
	name := fs.Arg(0)
	hub, tok := adminParseHubAndToken(fs)

	if *portalURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -portal-url is required")
		os.Exit(1)
	}

	// Default -hub-url to the HTTPS form of -hub
	effectiveHubURL := *portalHubURL
	if effectiveHubURL == "" {
		effectiveHubURL = wsToHTTP(hub)
	}

	body := map[string]string{
		"name":      name,
		"portalUrl": *portalURL,
		"hubUrl":    effectiveHubURL,
	}
	if *portalToken != "" {
		body["portalToken"] = *portalToken
	}

	status, result, err := adminHTTP("POST", hub, "/api/admin/portals", tok, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	st, _ := result["status"].(string)
	url, _ := result["url"].(string)
	hasSyncToken, _ := result["hasSyncToken"].(bool)

	if st == "updated" {
		fmt.Printf("Updated portal '%s' (%s)\n", name, url)
	} else {
		fmt.Printf("Added portal '%s' (%s)\n", name, url)
	}
	if hasSyncToken {
		fmt.Println("Sync token received -- viewer token updates will be automatic.")
	} else {
		fmt.Println("Warning: no sync token returned -- upgrade the portal to enable auto-sync.")
	}
	fmt.Println("Change is already active (no hub restart needed).")
}

// ── tela admin pair-code ────────────────────────────────────────

type pairCodeRequest struct {
	MachineID string   `json:"machineId"`
	Type      string   `json:"type,omitempty"`
	Machines  []string `json:"machines,omitempty"`
	ExpiresIn int      `json:"expiresIn"`
}

type pairCodeResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}

// parseDuration parses a duration string like "10m", "1h", "24h", "7d".
// Standard Go durations (s, m, h) are handled by time.ParseDuration.
// The "d" suffix is handled by converting days to hours first.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		// Parse as a float to handle fractional days
		var days float64
		if _, err := fmt.Sscanf(numStr, "%f", &days); err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}

func cmdAdminPairCode(args []string) {
	fs := flag.NewFlagSet("admin pair-code", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	expires := fs.String("expires", "10m", "Code expiration duration (e.g. 10m, 1h, 24h, 7d)")
	codeType := fs.String("type", "connect", "Code type: connect or register")
	machines := fs.String("machines", "*", "Comma-separated machine IDs (default: *)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin pair-code <machineId> -hub <hub> -token <token> [-expires 10m] [-type connect] [-machines *]")
		os.Exit(1)
	}
	machineID := fs.Arg(0)
	hub, tok := adminParseHubAndToken(fs)

	dur, err := parseDuration(*expires)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid -expires value: %v\n", err)
		os.Exit(1)
	}
	expiresInSec := int(dur.Seconds())

	machineList := strings.Split(*machines, ",")
	for i := range machineList {
		machineList[i] = strings.TrimSpace(machineList[i])
	}

	body := pairCodeRequest{
		MachineID: machineID,
		Type:      *codeType,
		Machines:  machineList,
		ExpiresIn: expiresInSec,
	}

	status, result, err := adminHTTP("POST", hub, "/api/admin/pair-code", tok, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	code, _ := result["code"].(string)
	expiresAt, _ := result["expiresAt"].(string)

	fmt.Printf("Generated pairing code: %s\n", code)
	fmt.Printf("Expires: %s\n", expiresAt)

	if *codeType == "connect" {
		fmt.Printf("\nClient pairing command:\n")
		fmt.Printf("  tela pair -hub %s -code %s\n", hub, code)
	} else {
		fmt.Printf("\nAgent onboarding command:\n")
		fmt.Printf("  telad pair -hub %s -code %s\n", hub, code)
	}
}

// ── tela admin remove-portal ─────────────────────────────────────

func cmdAdminRemovePortal(args []string) {
	fs := flag.NewFlagSet("admin remove-portal", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin remove-portal <name> -hub <hub> -token <token>")
		os.Exit(1)
	}
	name := fs.Arg(0)
	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("DELETE", hub, "/api/admin/portals/"+url.PathEscape(name), tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Removed portal '%s'.\n", name)
	fmt.Println("Change is already active (no hub restart needed).")
}

// ── tela admin tokens ─────────────────────────────────────────────
// Noun-style dispatcher that delegates to the existing list/add/remove
// functions. Keeps the public CLI surface RESTful: tokens is the
// resource, list/add/remove are the operations on it.

func cmdAdminTokens(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, `Usage:
  tela admin tokens list                       List all token identities
  tela admin tokens add <id> [-role <role>]    Create a new token identity
  tela admin tokens remove <id>                Remove a token identity`)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		cmdAdminListTokens(args[1:])
	case "add":
		cmdAdminAddToken(args[1:])
	case "remove", "rm":
		cmdAdminRemoveToken(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown tokens subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ── tela admin portals ────────────────────────────────────────────
// Noun-style dispatcher for portal registrations on the hub.

func cmdAdminPortals(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, `Usage:
  tela admin portals list                              List portal registrations
  tela admin portals add <name> -portal-url <url>      Register hub with a portal
  tela admin portals remove <name>                     Remove a portal registration`)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		cmdAdminListPortals(args[1:])
	case "add":
		cmdAdminAddPortal(args[1:])
	case "remove", "rm":
		cmdAdminRemovePortal(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown portals subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ── Agent management ──────────────────────────────────────────────

func cmdAdminAgent(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, `tela admin agent -- remote agent management

Usage:
  tela admin agent list     -hub <url> -token <tok>
  tela admin agent config   -hub <url> -token <tok> -machine <id>
  tela admin agent set      -hub <url> -token <tok> -machine <id> <json-fields>
  tela admin agent logs     -hub <url> -token <tok> -machine <id> [-n 100]
  tela admin agent restart  -hub <url> -token <tok> -machine <id>
  tela admin agent update   -hub <url> -token <tok> -machine <id> [-version vX.Y.Z]
`)
		os.Exit(1)
	}

	switch args[0] {
	case "config":
		cmdAdminAgentConfig(args[1:])
	case "set":
		cmdAdminAgentSet(args[1:])
	case "list":
		cmdAdminAgentList(args[1:])
	case "logs":
		cmdAdminAgentLogs(args[1:])
	case "restart":
		cmdAdminAgentRestart(args[1:])
	case "update":
		cmdAdminAgentUpdate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown agent command: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdAdminAgentConfig(args []string) {
	fs := flag.NewFlagSet("admin agent config", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL")
	token := fs.String("token", envOrDefault("TELA_OWNER_TOKEN", envOrDefault("TELA_TOKEN", "")), "Auth token")
	machine := fs.String("machine", "", "Machine ID")
	args = permuteArgs(fs, args)
	fs.Parse(args)

	hub := mustResolveHub(*hubURL)
	tok := *token
	if tok == "" {
		tok = credstore.LookupToken(hub)
	}
	if hub == "" || tok == "" || *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub, -token, and -machine are required")
		os.Exit(1)
	}

	status, result, err := adminHTTP("GET", hub, "/api/admin/agents/"+url.QueryEscape(*machine)+"/config-get", tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	// Pretty-print the response
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func cmdAdminAgentSet(args []string) {
	fs := flag.NewFlagSet("admin agent set", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL")
	token := fs.String("token", envOrDefault("TELA_OWNER_TOKEN", envOrDefault("TELA_TOKEN", "")), "Auth token")
	machine := fs.String("machine", "", "Machine ID")
	args = permuteArgs(fs, args)
	fs.Parse(args)

	hub := mustResolveHub(*hubURL)
	tok := *token
	if tok == "" {
		tok = credstore.LookupToken(hub)
	}
	if hub == "" || tok == "" || *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub, -token, and -machine are required")
		os.Exit(1)
	}

	// Remaining args after flags are the JSON fields payload
	remaining := fs.Args()
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Error: provide JSON fields to update, e.g. '{\"displayName\":\"New Name\"}'")
		os.Exit(1)
	}

	payload := map[string]interface{}{
		"machine": *machine,
		"fields":  json.RawMessage(remaining[0]),
	}

	status, result, err := adminHTTP("POST", hub, "/api/admin/agents/"+url.QueryEscape(*machine)+"/config-set", tok, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Println("Config updated.")
}

func cmdAdminAgentList(args []string) {
	fs := flag.NewFlagSet("admin agent list", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL")
	token := fs.String("token", envOrDefault("TELA_OWNER_TOKEN", envOrDefault("TELA_TOKEN", "")), "Auth token")
	args = permuteArgs(fs, args)
	fs.Parse(args)

	hub := mustResolveHub(*hubURL)
	tok := *token
	if tok == "" {
		tok = credstore.LookupToken(hub)
	}
	if hub == "" || tok == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub and -token are required")
		os.Exit(1)
	}

	// Use the existing status endpoint to list machines (agents)
	status, result, err := adminHTTP("GET", hub, "/api/status", tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	machines, ok := result["machines"].([]interface{})
	if !ok || len(machines) == 0 {
		fmt.Println("No agents registered.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "MACHINE\tSTATUS\tVERSION\tSERVICES")
	for _, m := range machines {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := mm["id"].(string)
		online := "offline"
		if c, ok := mm["agentConnected"].(bool); ok && c {
			online = "online"
		}
		ver, _ := mm["agentVersion"].(string)
		svcs := ""
		if ss, ok := mm["services"].([]interface{}); ok {
			names := make([]string, 0, len(ss))
			for _, s := range ss {
				if sm, ok := s.(map[string]interface{}); ok {
					if n, ok := sm["name"].(string); ok {
						names = append(names, n)
					}
				}
			}
			svcs = strings.Join(names, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, online, ver, svcs)
	}
	w.Flush()
}

func cmdAdminAgentLogs(args []string) {
	fs := flag.NewFlagSet("admin agent logs", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL")
	token := fs.String("token", envOrDefault("TELA_OWNER_TOKEN", envOrDefault("TELA_TOKEN", "")), "Auth token")
	machine := fs.String("machine", "", "Machine ID")
	n := fs.Int("n", 100, "Number of log lines to retrieve")
	args = permuteArgs(fs, args)
	fs.Parse(args)

	hub := mustResolveHub(*hubURL)
	tok := *token
	if tok == "" {
		tok = credstore.LookupToken(hub)
	}
	if hub == "" || tok == "" || *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub, -token, and -machine are required")
		os.Exit(1)
	}

	payload := map[string]int{"lines": *n}
	status, result, err := adminHTTP("POST", hub, "/api/admin/agents/"+url.QueryEscape(*machine)+"/logs", tok, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	if lines, ok := result["lines"].([]interface{}); ok {
		for _, line := range lines {
			if s, ok := line.(string); ok {
				fmt.Println(s)
			}
		}
	}
}

func cmdAdminAgentRestart(args []string) {
	fs := flag.NewFlagSet("admin agent restart", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL")
	token := fs.String("token", envOrDefault("TELA_OWNER_TOKEN", envOrDefault("TELA_TOKEN", "")), "Auth token")
	machine := fs.String("machine", "", "Machine ID")
	args = permuteArgs(fs, args)
	fs.Parse(args)

	hub := mustResolveHub(*hubURL)
	tok := *token
	if tok == "" {
		tok = credstore.LookupToken(hub)
	}
	if hub == "" || tok == "" || *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub, -token, and -machine are required")
		os.Exit(1)
	}

	status, result, err := adminHTTP("POST", hub, "/api/admin/agents/"+url.QueryEscape(*machine)+"/restart", tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Restart requested for '%s'.\n", *machine)
}

func cmdAdminAgentUpdate(args []string) {
	fs := flag.NewFlagSet("admin agent update", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL")
	token := fs.String("token", envOrDefault("TELA_OWNER_TOKEN", envOrDefault("TELA_TOKEN", "")), "Auth token")
	machine := fs.String("machine", "", "Machine ID")
	version := fs.String("version", "", "Target release version (default: latest)")
	args = permuteArgs(fs, args)
	fs.Parse(args)

	hub := mustResolveHub(*hubURL)
	tok := *token
	if tok == "" {
		tok = credstore.LookupToken(hub)
	}
	if hub == "" || tok == "" || *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -hub, -token, and -machine are required")
		os.Exit(1)
	}

	var payload map[string]string
	if *version != "" {
		payload = map[string]string{"version": *version}
	}

	status, result, err := adminHTTP("POST", hub, "/api/admin/agents/"+url.QueryEscape(*machine)+"/update", tok, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	if msg, ok := result["message"].(string); ok {
		fmt.Printf("%s: %s\n", *machine, msg)
	} else {
		fmt.Printf("Update requested for '%s'.\n", *machine)
	}
}

// ── tela admin access ──────────────────────────────────────────────

func cmdAdminAccess(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "grant":
			cmdAdminAccessGrant(args[1:])
			return
		case "revoke":
			cmdAdminAccessRevoke(args[1:])
			return
		case "rename":
			cmdAdminAccessRename(args[1:])
			return
		case "remove":
			cmdAdminAccessRemove(args[1:])
			return
		}
	}

	// Default: list
	fs := flag.NewFlagSet("admin access", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	asJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("GET", hub, "/api/admin/access", tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
		return
	}

	entries, _ := result["access"].([]any)
	if len(entries) == 0 {
		fmt.Println("No access entries configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "IDENTITY\tROLE\tMACHINES")
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		role, _ := entry["role"].(string)
		machines, _ := entry["machines"].([]any)

		var machSummary string
		if role == "owner" || role == "admin" {
			machSummary = "* (all permissions)"
		} else if role == "viewer" {
			machSummary = "(view only)"
		} else if len(machines) == 0 {
			machSummary = "(no permissions)"
		} else {
			var parts []string
			for _, m := range machines {
				mach, ok := m.(map[string]any)
				if !ok {
					continue
				}
				mid, _ := mach["machineId"].(string)
				perms, _ := mach["permissions"].([]any)
				var permStrs []string
				for _, p := range perms {
					if s, ok := p.(string); ok {
						permStrs = append(permStrs, s)
					}
				}
				parts = append(parts, mid+": "+strings.Join(permStrs, ", "))
			}
			machSummary = strings.Join(parts, " | ")
		}

		fmt.Fprintf(w, "%s\t%s\t%s\n", id, role, machSummary)
	}
	w.Flush()
}

func cmdAdminAccessGrant(args []string) {
	fs := flag.NewFlagSet("admin access grant", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 3 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin access grant <id> <machineId> <permissions>")
		fmt.Fprintln(os.Stderr, "  permissions: comma-separated list of connect,register,manage")
		fmt.Fprintln(os.Stderr, "  Example: tela admin access grant alice barn connect,manage")
		os.Exit(1)
	}
	id := fs.Arg(0)
	machineID := fs.Arg(1)
	perms := strings.Split(fs.Arg(2), ",")

	hub, tok := adminParseHubAndToken(fs)

	body := map[string]any{"permissions": perms}

	status, result, err := adminHTTP("PUT", hub, "/api/admin/access/"+url.PathEscape(id)+"/machines/"+url.PathEscape(machineID), tok, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Set '%s' permissions on '%s' to [%s].\n", id, machineID, strings.Join(perms, ", "))
}

func cmdAdminAccessRevoke(args []string) {
	fs := flag.NewFlagSet("admin access revoke", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin access revoke <id> <machineId>")
		os.Exit(1)
	}
	id := fs.Arg(0)
	machineID := fs.Arg(1)

	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("DELETE", hub, "/api/admin/access/"+url.PathEscape(id)+"/machines/"+url.PathEscape(machineID), tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Revoked all '%s' permissions on '%s'.\n", id, machineID)
}

func cmdAdminAccessRename(args []string) {
	fs := flag.NewFlagSet("admin access rename", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin access rename <id> <newId>")
		os.Exit(1)
	}
	id := fs.Arg(0)
	newID := fs.Arg(1)

	hub, tok := adminParseHubAndToken(fs)

	body := map[string]string{"id": newID}

	status, result, err := adminHTTP("PATCH", hub, "/api/admin/access/"+url.PathEscape(id), tok, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Renamed '%s' to '%s'.\n", id, newID)
}

func cmdAdminAccessRemove(args []string) {
	fs := flag.NewFlagSet("admin access remove", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin access remove <id>")
		os.Exit(1)
	}
	id := fs.Arg(0)

	hub, tok := adminParseHubAndToken(fs)

	status, result, err := adminHTTP("DELETE", hub, "/api/admin/access/"+url.PathEscape(id), tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Removed identity '%s' and all its permissions.\n", id)
}

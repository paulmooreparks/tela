// admin.go — "tela admin" subcommands for remote hub management
//
// These commands call the hub's /api/admin/* REST endpoints so you can
// manage token identities, machine ACLs, and portal registrations from
// any workstation — no SSH or shell access to the hub required.
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
	"os"
	"text/tabwriter"
	"time"
)

func cmdAdmin(args []string) {
	if len(args) < 1 {
		printAdminUsage()
		os.Exit(1)
	}
	subcmd := args[0]
	rest := args[1:]

	switch subcmd {
	case "list-tokens":
		cmdAdminListTokens(rest)
	case "add-token":
		cmdAdminAddToken(rest)
	case "remove-token":
		cmdAdminRemoveToken(rest)
	case "grant":
		cmdAdminGrant(rest)
	case "revoke":
		cmdAdminRevoke(rest)
	case "rotate":
		cmdAdminRotate(rest)
	case "list-portals":
		cmdAdminListPortals(rest)
	case "add-portal":
		cmdAdminAddPortal(rest)
	case "remove-portal":
		cmdAdminRemovePortal(rest)
	case "help", "-h", "--help":
		printAdminUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown admin command: %s\n\n", subcmd)
		printAdminUsage()
		os.Exit(1)
	}
}

func printAdminUsage() {
	fmt.Fprintf(os.Stderr, `tela admin — remote hub auth and portal management

Usage:
  tela admin <command> [options]

Token commands:
  list-tokens    List all token identities on the hub
  add-token      Add a new token identity (returns the token once)
  remove-token   Remove a token identity
  grant          Grant connect access to a machine
  revoke         Revoke connect access to a machine
  rotate         Regenerate token for an identity

Portal commands:
  list-portals    List portal registrations
  add-portal      Register hub with a portal
  remove-portal   Remove a portal registration

All commands require -hub and -token.
The token must belong to an owner or admin identity.

Token resolution (in order):
  1. -token flag
  2. TELA_OWNER_TOKEN env var
  3. TELA_TOKEN env var

Examples:
  tela admin list-tokens -hub gohub -token <owner-token>
  tela admin add-token alice -hub gohub -token <owner-token>
  tela admin add-token bob -hub gohub -token <owner-token> -role admin
  tela admin grant alice my-desktop -hub gohub -token <owner-token>
  tela admin revoke alice my-desktop -hub gohub -token <owner-token>
  tela admin remove-token alice -hub gohub -token <owner-token>
  tela admin rotate alice -hub gohub -token <owner-token>

  tela admin list-portals -hub gohub -token <owner-token>
  tela admin add-portal awansaya -hub gohub -token <owner-token> \
    -portal-url https://awansaya.net
  tela admin remove-portal awansaya -hub gohub -token <owner-token>

Tip: set TELA_OWNER_TOKEN in your shell profile so you don't need -token
every time.  Use a separate TELA_TOKEN for day-to-day tela connect usage.
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

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{}},
	}
	resp, err := client.Do(req)
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
		fmt.Fprintln(os.Stderr, "Error: -token is required (or set TELA_OWNER_TOKEN)")
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
					// Unknown flag — assume it takes a value.
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
	fmt.Println("SAVE THIS TOKEN — it will not be shown again.")
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

	status, result, err := adminHTTP("DELETE", hub, "/api/admin/tokens?id="+id, tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Removed identity '%s' and cleaned up machine ACLs.\n", id)
	fmt.Println("Change is already active (no hub restart needed).")
}

// ── tela admin grant ───────────────────────────────────────────────

func cmdAdminGrant(args []string) {
	fs := flag.NewFlagSet("admin grant", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin grant <id> <machineId> -hub <hub> -token <token>")
		os.Exit(1)
	}
	id := fs.Arg(0)
	machineID := fs.Arg(1)
	hub, tok := adminParseHubAndToken(fs)

	body := map[string]string{"id": id, "machineId": machineID}

	status, result, err := adminHTTP("POST", hub, "/api/admin/grant", tok, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	st, _ := result["status"].(string)
	if st == "already_granted" {
		fmt.Printf("Identity '%s' already has connect access to '%s'.\n", id, machineID)
	} else {
		fmt.Printf("Granted '%s' connect access to '%s'.\n", id, machineID)
		fmt.Println("Change is already active (no hub restart needed).")
	}
}

// ── tela admin revoke ──────────────────────────────────────────────

func cmdAdminRevoke(args []string) {
	fs := flag.NewFlagSet("admin revoke", flag.ExitOnError)
	hubURL := fs.String("hub", envOrDefault("TELA_HUB", ""), "Hub URL (env: TELA_HUB)")
	token := fs.String("token", adminTokenDefault(), "Admin token (env: TELA_OWNER_TOKEN)")
	fs.Parse(permuteArgs(fs, args))
	_ = hubURL
	_ = token

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tela admin revoke <id> <machineId> -hub <hub> -token <token>")
		os.Exit(1)
	}
	id := fs.Arg(0)
	machineID := fs.Arg(1)
	hub, tok := adminParseHubAndToken(fs)

	body := map[string]string{"id": id, "machineId": machineID}

	status, result, err := adminHTTP("POST", hub, "/api/admin/revoke", tok, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Revoked '%s' connect access to '%s'.\n", id, machineID)
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
	fmt.Println("SAVE THIS TOKEN — it will not be shown again.")
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
		fmt.Println("Sync token received — viewer token updates will be automatic.")
	} else {
		fmt.Println("Warning: no sync token returned — upgrade the portal to enable auto-sync.")
	}
	fmt.Println("Change is already active (no hub restart needed).")
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

	status, result, err := adminHTTP("DELETE", hub, "/api/admin/portals?name="+name, tok, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	adminCheckError(status, result)

	fmt.Printf("Removed portal '%s'.\n", name)
	fmt.Println("Change is already active (no hub restart needed).")
}

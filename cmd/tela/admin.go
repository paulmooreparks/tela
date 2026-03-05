// admin.go — "tela admin" subcommands for remote hub auth management
//
// These commands call the hub's /api/admin/* REST endpoints so you can
// manage token identities and machine ACLs from any workstation — no
// SSH or shell access to the hub required.
//
// All commands require an owner or admin token passed via -token flag
// or TELA_TOKEN environment variable.

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
	case "help", "-h", "--help":
		printAdminUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown admin command: %s\n\n", subcmd)
		printAdminUsage()
		os.Exit(1)
	}
}

func printAdminUsage() {
	fmt.Fprintf(os.Stderr, `tela admin — remote hub auth management

Usage:
  tela admin <command> [options]

Commands:
  list-tokens    List all token identities on the hub
  add-token      Add a new token identity (returns the token once)
  remove-token   Remove a token identity
  grant          Grant connect access to a machine
  revoke         Revoke connect access to a machine
  rotate         Regenerate token for an identity

All commands require -hub and -token (or TELA_HUB / TELA_TOKEN env vars).
The token must belong to an owner or admin identity.

Examples:
  tela admin list-tokens -hub gohub -token <owner-token>
  tela admin add-token alice -hub gohub -token <owner-token>
  tela admin add-token bob -hub gohub -token <owner-token> -role admin
  tela admin grant alice my-desktop -hub gohub -token <owner-token>
  tela admin revoke alice my-desktop -hub gohub -token <owner-token>
  tela admin remove-token alice -hub gohub -token <owner-token>
  tela admin rotate alice -hub gohub -token <owner-token>
`)
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
		fmt.Fprintln(os.Stderr, "Error: -token is required (or set TELA_TOKEN)")
		os.Exit(1)
	}
	return hubURL, token
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Admin token (env: TELA_TOKEN)")
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Admin token (env: TELA_TOKEN)")
	role := fs.String("role", "", "Role: owner, admin, or omit for user")
	fs.Parse(args)
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Admin token (env: TELA_TOKEN)")
	fs.Parse(args)
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Admin token (env: TELA_TOKEN)")
	fs.Parse(args)
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Admin token (env: TELA_TOKEN)")
	fs.Parse(args)
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
	token := fs.String("token", envOrDefault("TELA_TOKEN", ""), "Admin token (env: TELA_TOKEN)")
	fs.Parse(args)
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

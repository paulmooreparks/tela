package hub

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// ── Auth configuration types ────────────────────────────────────────────────

// authConfig is the YAML "auth:" block inside hubConfig.
// When Tokens is empty the hub runs in open mode (backward compatible).
type authConfig struct {
	Tokens   []tokenEntry          `yaml:"tokens,omitempty"`
	Machines map[string]machineACL `yaml:"machines,omitempty"`
}

// tokenEntry defines a named identity that may interact with the hub.
type tokenEntry struct {
	ID      string `yaml:"id"`                // human-friendly label
	Token   string `yaml:"token"`             // secret hex value
	HubRole string `yaml:"hubRole,omitempty"` // "owner" | "admin" | (empty = user)
}

// machineACL controls which tokens may register or connect to a specific machine.
// Use the key "*" as a wildcard that applies to all machines.
type machineACL struct {
	RegisterToken string         `yaml:"registerToken,omitempty"` // if set, only this token may (re)register
	ConnectTokens []connectGrant `yaml:"connectTokens,omitempty"` // tokens permitted to connect, each with an optional service filter
	ManageTokens  []string       `yaml:"manageTokens,omitempty"`  // tokens permitted to manage (config, restart, logs)
}

// connectGrant is one token entry in a machine's connect ACL, optionally
// restricted to a subset of the agent's named services. An empty Services
// list means "all services" (the pre-0.15 behavior). A non-empty list
// means the session-setup path on the hub exposes only those named
// services to the client; other services are invisible, not merely
// blocked.
type connectGrant struct {
	Token    string   `yaml:"token"`              // secret hex value
	Services []string `yaml:"services,omitempty"` // nil or empty = all services
}

// UnmarshalYAML accepts either a bare string (pre-0.15 config form) or
// the struct form {token: ..., services: [...]}. The bare-string form
// is rewritten to the struct form on the next config write, so configs
// self-upgrade without an explicit migration.
func (g *connectGrant) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		g.Token = node.Value
		g.Services = nil
		return nil
	}
	// Use a helper type to avoid infinite recursion into UnmarshalYAML.
	type rawGrant struct {
		Token    string   `yaml:"token"`
		Services []string `yaml:"services,omitempty"`
	}
	var r rawGrant
	if err := node.Decode(&r); err != nil {
		return err
	}
	g.Token = r.Token
	g.Services = r.Services
	return nil
}

// ── Runtime auth store ──────────────────────────────────────────────────────

// authStore is the runtime representation of authConfig, built once at startup.
type authStore struct {
	mu       sync.RWMutex
	enabled  bool
	byToken  map[string]*tokenEntry // token value → entry
	machines map[string]machineACL  // machineID → ACL (including "*" wildcard)
}

// newAuthStore builds a runtime authStore from an authConfig.
// Returns a disabled (open) store when cfg is nil or has no tokens.
func newAuthStore(cfg *authConfig) *authStore {
	s := &authStore{
		byToken:  make(map[string]*tokenEntry),
		machines: make(map[string]machineACL),
	}
	if cfg == nil || len(cfg.Tokens) == 0 {
		return s // disabled → open hub
	}
	s.enabled = true
	for i := range cfg.Tokens {
		e := &cfg.Tokens[i]
		if e.Token != "" {
			s.byToken[e.Token] = e
		}
	}
	for name, acl := range cfg.Machines {
		s.machines[name] = acl
	}
	return s
}

// isEnabled reports whether auth enforcement is active.
func (s *authStore) isEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// identityID returns the human-friendly ID for a token, or "" if unknown.
func (s *authStore) identityID(token string) string {
	if token == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.byToken[token]; ok {
		return e.ID
	}
	return ""
}

// isOwnerOrAdmin returns true when the token belongs to a hub-level owner or admin.
func (s *authStore) isOwnerOrAdmin(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return false
	}
	e, ok := s.byToken[token]
	return ok && (e.HubRole == "owner" || e.HubRole == "admin")
}

// canRegister returns true when the token may register machineID.
//   - Auth disabled: always true.
//   - Hub owner/admin: always true.
//   - Machine has an explicit registerToken: must match exactly.
//   - Machine has an ACL entry but no registerToken: any known token is allowed.
//   - Machine has no ACL entry: only owner/admin.
func (s *authStore) canRegister(token, machineID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return true
	}
	if e, ok := s.byToken[token]; ok && (e.HubRole == "owner" || e.HubRole == "admin") {
		return true
	}
	acl, hasACL := s.machines[machineID]
	if !hasACL {
		// No ACL entry and not owner/admin → deny
		return false
	}
	if acl.RegisterToken != "" {
		return subtle.ConstantTimeCompare([]byte(acl.RegisterToken), []byte(token)) == 1
	}
	// ACL entry exists, no registerToken restriction → any known token may register
	_, known := s.byToken[token]
	return known
}

// canConnect returns true when the token may open a session to machineID.
//   - Auth disabled: always true.
//   - Hub owner/admin: always true.
//   - Token appears in the machine's connectTokens: true.
//   - Token appears in the wildcard "*" connectTokens: true.
//
// canConnect does not consider service-name filtering; use
// connectServicesFilter to obtain the filter for a specific grant.
func (s *authStore) canConnect(token, machineID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return true
	}
	if e, ok := s.byToken[token]; ok && (e.HubRole == "owner" || e.HubRole == "admin") {
		return true
	}
	if _, ok := findConnectGrant(s.machines[machineID].ConnectTokens, token); ok {
		return true
	}
	if _, ok := findConnectGrant(s.machines["*"].ConnectTokens, token); ok {
		return true
	}
	return false
}

// connectServicesFilter returns the allowed service-name set for a
// (token, machineID) pair, plus a boolean "filtered" flag.
//   - filtered=false means "all services" (no filter applied; callers
//     should expose every service the agent advertises).
//   - filtered=true with names set means only those service names are
//     visible to the session.
//   - filtered=true with names empty is treated as filtered=false (no
//     filter); an empty list in config means "all services."
//
// When both a machine-specific grant and a wildcard grant match, the
// machine-specific grant wins. Owner/admin tokens are always
// unfiltered.
func (s *authStore) connectServicesFilter(token, machineID string) (names []string, filtered bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return nil, false
	}
	if e, ok := s.byToken[token]; ok && (e.HubRole == "owner" || e.HubRole == "admin") {
		return nil, false
	}
	if g, ok := findConnectGrant(s.machines[machineID].ConnectTokens, token); ok {
		if len(g.Services) == 0 {
			return nil, false
		}
		return append([]string(nil), g.Services...), true
	}
	if g, ok := findConnectGrant(s.machines["*"].ConnectTokens, token); ok {
		if len(g.Services) == 0 {
			return nil, false
		}
		return append([]string(nil), g.Services...), true
	}
	return nil, false
}

// canManage returns true when the token may send management commands to machineID.
//   - Auth disabled: always true.
//   - Hub owner/admin: always true.
//   - Token in machine-specific manageTokens: true.
//   - Token in wildcard "*" manageTokens: true.
func (s *authStore) canManage(token, machineID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return true
	}
	if e, ok := s.byToken[token]; ok && (e.HubRole == "owner" || e.HubRole == "admin") {
		return true
	}
	if inTokenList(s.machines[machineID].ManageTokens, token) {
		return true
	}
	if inTokenList(s.machines["*"].ManageTokens, token) {
		return true
	}
	return false
}

// canViewMachine returns true when the token may see machineID in API responses.
// Viewer-role tokens can see all machines (read-only console access).
func (s *authStore) canViewMachine(token, machineID string) bool {
	if s.isViewer(token) {
		return true
	}
	return s.canConnect(token, machineID)
}

// isViewer returns true when the token has the "viewer" hub role.
func (s *authStore) isViewer(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return false
	}
	e, ok := s.byToken[token]
	return ok && e.HubRole == "viewer"
}

// consoleViewerToken returns the token value of the first viewer-role
// identity, or "" if none exists.
func (s *authStore) consoleViewerToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.byToken {
		if e.HubRole == "viewer" {
			return e.Token
		}
	}
	return ""
}

func inTokenList(list []string, target string) bool {
	for _, v := range list {
		if subtle.ConstantTimeCompare([]byte(v), []byte(target)) == 1 {
			return true
		}
	}
	return false
}

// findConnectGrant returns the first connectGrant whose Token matches
// target (constant-time compared). The boolean is false if no entry
// matches. The returned grant is a copy; callers that want to mutate
// storage must modify the slice element in place.
func findConnectGrant(grants []connectGrant, target string) (connectGrant, bool) {
	for _, g := range grants {
		if subtle.ConstantTimeCompare([]byte(g.Token), []byte(target)) == 1 {
			return g, true
		}
	}
	return connectGrant{}, false
}

// hasConnectGrant reports whether any grant's Token matches target.
func hasConnectGrant(grants []connectGrant, target string) bool {
	_, ok := findConnectGrant(grants, target)
	return ok
}

// removeConnectGrant returns grants with every entry whose Token
// matches target removed. The underlying array may be reused.
func removeConnectGrant(grants []connectGrant, target string) []connectGrant {
	out := grants[:0]
	for _, g := range grants {
		if subtle.ConstantTimeCompare([]byte(g.Token), []byte(target)) != 1 {
			out = append(out, g)
		}
	}
	return out
}

// replaceConnectGrantToken walks grants and rewrites any entry whose
// Token equals oldToken to newToken, leaving Services untouched.
// Returns true when at least one entry changed.
func replaceConnectGrantToken(grants []connectGrant, oldToken, newToken string) bool {
	changed := false
	for i := range grants {
		if subtle.ConstantTimeCompare([]byte(grants[i].Token), []byte(oldToken)) == 1 {
			grants[i].Token = newToken
			changed = true
		}
	}
	return changed
}

// ── Token generation ────────────────────────────────────────────────────────

// generateToken returns a cryptographically random 32-byte hex string (64 chars).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ── Hot-reload ──────────────────────────────────────────────────────────────

// reload replaces the store's internal state from a new authConfig.
// Existing WebSocket connections are unaffected; only subsequent checks
// use the new data. This is safe to call concurrently.
func (s *authStore) reload(cfg *authConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byToken = make(map[string]*tokenEntry)
	s.machines = make(map[string]machineACL)

	if cfg == nil || len(cfg.Tokens) == 0 {
		s.enabled = false
		return
	}
	s.enabled = true
	for i := range cfg.Tokens {
		e := &cfg.Tokens[i]
		if e.Token != "" {
			s.byToken[e.Token] = e
		}
	}
	for name, acl := range cfg.Machines {
		s.machines[name] = acl
	}
}

// toConfig exports the current auth state as an authConfig value
// suitable for persisting to YAML.
func (s *authStore) toConfig() authConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := authConfig{
		Machines: make(map[string]machineACL, len(s.machines)),
	}
	for _, e := range s.byToken {
		cfg.Tokens = append(cfg.Tokens, *e)
	}
	for name, acl := range s.machines {
		cfg.Machines[name] = acl
	}
	return cfg
}

// ── Environment-variable bootstrap ──────────────────────────────────────────

// bootstrapFromEnv checks for TELA_OWNER_TOKEN. If the hub has no auth
// configured and the env var is set, it creates the owner identity and
// writes the config. Returns true if bootstrap occurred.
func bootstrapFromEnv(cfg *hubConfig, cfgPath string) bool {
	ownerToken := os.Getenv("TELA_OWNER_TOKEN")
	if ownerToken == "" {
		return false
	}
	if len(cfg.Auth.Tokens) > 0 {
		// Already bootstrapped -- env var is ignored.
		return false
	}

	bootstrapAuth(cfg, cfgPath, ownerToken)
	return true
}

// bootstrapAuth installs the owner token, a console-viewer token, and a
// wildcard connect ACL into cfg, then persists to cfgPath.
func bootstrapAuth(cfg *hubConfig, cfgPath string, ownerToken string) {
	// Generate a viewer token for the built-in hub console.
	viewerToken, err := generateToken()
	if err != nil {
		viewerToken = "" // non-fatal; console will show empty
	}

	cfg.Auth.Tokens = []tokenEntry{
		{ID: "owner", Token: ownerToken, HubRole: "owner"},
	}
	if viewerToken != "" {
		cfg.Auth.Tokens = append(cfg.Auth.Tokens, tokenEntry{
			ID: "console-viewer", Token: viewerToken, HubRole: "viewer",
		})
	}
	if cfg.Auth.Machines == nil {
		cfg.Auth.Machines = make(map[string]machineACL)
	}
	cfg.Auth.Machines["*"] = machineACL{
		ConnectTokens: []connectGrant{{Token: ownerToken}},
	}

	// Persist so the token survives container and service restarts. A
	// write failure here means bootstrap ran in memory only -- the next
	// restart will generate a fresh token and the one the operator just
	// captured is useless. Log loudly; do not silently swallow.
	if cfgPath != "" {
		if err := writeHubConfig(cfgPath, cfg); err != nil {
			log.Printf("[hub] WARNING: bootstrap token not persisted to %s: %v", cfgPath, err)
			log.Printf("[hub] WARNING: the token above will NOT survive restart; fix config file permissions or restart with a writable -config path")
		}
	}
}

// autoBootstrapAuth generates a new owner token when the hub has no auth
// configured and no TELA_OWNER_TOKEN env var. The token is printed once
// to stdout so the operator can save it. Returns true if bootstrap occurred.
func autoBootstrapAuth(cfg *hubConfig, cfgPath string) bool {
	if len(cfg.Auth.Tokens) > 0 {
		return false
	}
	ownerToken, err := generateToken()
	if err != nil {
		log.Printf("[hub] WARNING: could not auto-generate owner token: %v", err)
		return false
	}

	bootstrapAuth(cfg, cfgPath, ownerToken)

	fmt.Println("==============================================================")
	fmt.Println("  AUTH BOOTSTRAPPED -- owner token generated automatically")
	fmt.Println("")
	fmt.Printf("  Owner token: %s\n", ownerToken)
	fmt.Println("")
	fmt.Println("  Use it with: tela admin --hub <URL> --token <TOKEN>")
	fmt.Println("  Or set env:  TELA_OWNER_TOKEN=" + ownerToken)
	if cfgPath != "" {
		fmt.Printf("  Persisted to: %s\n", cfgPath)
		fmt.Println("  Recover later with: telahubd user show-owner -config " + cfgPath)
	} else {
		fmt.Println("  WARNING: no -config path set; token is IN-MEMORY ONLY and")
		fmt.Println("  will be regenerated on restart.")
	}
	fmt.Println("==============================================================")

	return true
}

// ensureConsoleViewer checks whether auth is enabled and a viewer-role
// token already exists. If not, it creates one, appends it to the
// config, and persists. Returns true if a new viewer was created.
func ensureConsoleViewer(cfg *hubConfig, cfgPath string) bool {
	if len(cfg.Auth.Tokens) == 0 {
		return false // auth disabled
	}
	for _, t := range cfg.Auth.Tokens {
		if t.HubRole == "viewer" {
			return false // already have one
		}
	}
	viewerToken, err := generateToken()
	if err != nil {
		return false
	}
	cfg.Auth.Tokens = append(cfg.Auth.Tokens, tokenEntry{
		ID: "console-viewer", Token: viewerToken, HubRole: "viewer",
	})
	if cfgPath != "" {
		_ = writeHubConfig(cfgPath, cfg)
	}
	return true
}

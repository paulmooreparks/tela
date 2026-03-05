package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
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
	ID      string `yaml:"id"`               // human-friendly label
	Token   string `yaml:"token"`            // secret hex value
	HubRole string `yaml:"hubRole,omitempty"` // "owner" | "admin" | (empty = user)
}

// machineACL controls which tokens may register or connect to a specific machine.
// Use the key "*" as a wildcard that applies to all machines.
type machineACL struct {
	RegisterToken string   `yaml:"registerToken,omitempty"` // if set, only this token may (re)register
	ConnectTokens []string `yaml:"connectTokens,omitempty"` // tokens permitted to connect
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
	if !s.enabled {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	if !s.enabled {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.byToken[token]; ok && (e.HubRole == "owner" || e.HubRole == "admin") {
		return true
	}
	acl, hasACL := s.machines[machineID]
	if !hasACL {
		// No ACL entry and not owner/admin → deny
		return false
	}
	if acl.RegisterToken != "" {
		return acl.RegisterToken == token
	}
	// ACL entry exists, no registerToken restriction → any known token may register
	_, known := s.byToken[token]
	return known
}

// canConnect returns true when the token may open a session to machineID.
//   - Auth disabled: always true.
//   - Hub owner/admin: always true.
//   - Token in machine-specific connectTokens: true.
//   - Token in wildcard "*" connectTokens: true.
func (s *authStore) canConnect(token, machineID string) bool {
	if !s.enabled {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.byToken[token]; ok && (e.HubRole == "owner" || e.HubRole == "admin") {
		return true
	}
	if inTokenList(s.machines[machineID].ConnectTokens, token) {
		return true
	}
	if inTokenList(s.machines["*"].ConnectTokens, token) {
		return true
	}
	return false
}

// canViewMachine returns true when the token may see machineID in API responses.
func (s *authStore) canViewMachine(token, machineID string) bool {
	return s.canConnect(token, machineID)
}

func inTokenList(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
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
		// Already bootstrapped — env var is ignored.
		return false
	}

	cfg.Auth.Tokens = []tokenEntry{
		{ID: "owner", Token: ownerToken, HubRole: "owner"},
	}
	if cfg.Auth.Machines == nil {
		cfg.Auth.Machines = make(map[string]machineACL)
	}
	cfg.Auth.Machines["*"] = machineACL{
		ConnectTokens: []string{ownerToken},
	}

	// Persist so the token survives container restarts.
	if cfgPath != "" {
		_ = writeHubConfig(cfgPath, cfg)
	}
	return true
}

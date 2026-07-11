package hub

import (
	"testing"
	"time"
)

// ── tokenEntry validity primitives ──────────────────────────────────

func TestTokenEntry_IsValid_DefaultIsValid(t *testing.T) {
	e := &tokenEntry{ID: "alice", Token: tokenUser1}
	if !e.isValid(time.Now()) {
		t.Error("a fresh entry with no metadata should be valid")
	}
}

func TestTokenEntry_IsRevoked(t *testing.T) {
	now := time.Now().UTC()
	e := &tokenEntry{ID: "alice", Token: tokenUser1, RevokedAt: &now}
	if !e.isRevoked() {
		t.Error("entry with RevokedAt set should report revoked")
	}
	if e.isValid(time.Now()) {
		t.Error("revoked entry should not be valid")
	}
}

func TestTokenEntry_IsExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC()
	future := time.Now().Add(time.Hour).UTC()

	expired := &tokenEntry{ID: "alice", Token: tokenUser1, ExpiresAt: &past}
	if !expired.isExpired(time.Now()) {
		t.Error("entry with past ExpiresAt should report expired")
	}
	if expired.isValid(time.Now()) {
		t.Error("expired entry should not be valid")
	}

	live := &tokenEntry{ID: "bob", Token: tokenUser2, ExpiresAt: &future}
	if live.isExpired(time.Now()) {
		t.Error("entry with future ExpiresAt should not report expired")
	}
	if !live.isValid(time.Now()) {
		t.Error("entry with future ExpiresAt should be valid")
	}
}

func TestTokenEntry_NilIsNotValid(t *testing.T) {
	var e *tokenEntry
	if e.isValid(time.Now()) {
		t.Error("nil entry should never be valid")
	}
}

// ── newAuthStore: pre-0.16 entries keep IssuedAt nil ────────────────

// TestNewAuthStore_LeavesIssuedAtNilForPre016 confirms that an entry
// loaded from a pre-0.16 config (no IssuedAt on disk) keeps IssuedAt
// nil in memory. Synthesizing a value at load time would be misleading
// (every pre-existing token would appear to have been issued at the
// moment the new binary started up). The truthful display is "unknown
// / pre-0.16," which the CLI and TelaVisor both render as blank.
func TestNewAuthStore_LeavesIssuedAtNilForPre016(t *testing.T) {
	tokens := []tokenEntry{
		{ID: "alice", Token: tokenUser1}, // no IssuedAt -- pre-0.16 shape
	}
	cfg := &authConfig{Tokens: tokens}
	_ = newAuthStore(cfg)

	if cfg.Tokens[0].IssuedAt != nil {
		t.Errorf("newAuthStore synthesized IssuedAt = %v for a pre-0.16 entry; want nil",
			*cfg.Tokens[0].IssuedAt)
	}
}

// TestNewAuthStore_PreservesIssuedAt confirms that an entry that
// already has IssuedAt set keeps it (no migration overwrite).
func TestNewAuthStore_PreservesIssuedAt(t *testing.T) {
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tokens := []tokenEntry{
		{ID: "alice", Token: tokenUser1, IssuedAt: &stamp},
	}
	cfg := &authConfig{Tokens: tokens}
	_ = newAuthStore(cfg)

	if cfg.Tokens[0].IssuedAt == nil {
		t.Fatal("newAuthStore lost IssuedAt")
	}
	if !cfg.Tokens[0].IssuedAt.Equal(stamp) {
		t.Errorf("IssuedAt = %v, want preserved %v", cfg.Tokens[0].IssuedAt, stamp)
	}
}

// ── auth checks honor revocation ────────────────────────────────────

func TestCanRegister_DeniesRevokedOwner(t *testing.T) {
	now := time.Now().UTC()
	tokens := stdTokens()
	tokens[0].RevokedAt = &now // revoke owner
	s := makeStore(tokens, nil)

	if s.canRegister(tokenOwner, "barn") {
		t.Error("revoked owner should not pass canRegister")
	}
}

func TestCanConnect_DeniesRevokedUser(t *testing.T) {
	now := time.Now().UTC()
	tokens := stdTokens()
	tokens[2].RevokedAt = &now // revoke alice
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(tokens, acls)

	if s.canConnect(tokenUser1, "barn") {
		t.Error("revoked user should not pass canConnect even with ACL grant")
	}
}

func TestCanManage_DeniesRevokedAdmin(t *testing.T) {
	now := time.Now().UTC()
	tokens := stdTokens()
	tokens[1].RevokedAt = &now // revoke admin
	s := makeStore(tokens, nil)

	if s.canManage(tokenAdmin, "barn") {
		t.Error("revoked admin should not pass canManage")
	}
}

func TestIdentityID_RevokedReturnsEmpty(t *testing.T) {
	now := time.Now().UTC()
	tokens := stdTokens()
	tokens[2].RevokedAt = &now // revoke alice
	s := makeStore(tokens, nil)

	if id := s.identityID(tokenUser1); id != "" {
		t.Errorf("identityID for revoked token = %q, want empty", id)
	}
}

func TestIsViewer_DeniesRevokedViewer(t *testing.T) {
	now := time.Now().UTC()
	tokens := stdTokens()
	tokens[4].RevokedAt = &now // revoke viewer
	s := makeStore(tokens, nil)

	if s.isViewer(tokenViewer) {
		t.Error("revoked viewer should not pass isViewer")
	}
}

// ── auth checks honor expiry ────────────────────────────────────────

func TestCanConnect_DeniesExpiredUser(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC()
	tokens := stdTokens()
	tokens[2].ExpiresAt = &past // expire alice
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(tokens, acls)

	if s.canConnect(tokenUser1, "barn") {
		t.Error("expired user should not pass canConnect")
	}
}

func TestCanConnect_AllowsFutureExpiry(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC()
	tokens := stdTokens()
	tokens[2].ExpiresAt = &future // alice has a future expiry
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(tokens, acls)

	if !s.canConnect(tokenUser1, "barn") {
		t.Error("user with future ExpiresAt should pass canConnect")
	}
}

func TestIsOwnerOrAdmin_DeniesExpiredOwner(t *testing.T) {
	past := time.Now().Add(-time.Minute).UTC()
	tokens := stdTokens()
	tokens[0].ExpiresAt = &past // expire owner
	s := makeStore(tokens, nil)

	if s.isOwnerOrAdmin(tokenOwner) {
		t.Error("expired owner should not pass isOwnerOrAdmin")
	}
}

// ── revocation does not delete ──────────────────────────────────────

// TestRevokedEntry_StaysInConfig confirms the audit-trail-preserving
// property: a revoked entry remains in cfg.Tokens. This is checked at
// the configuration level since the revoke endpoint is exercised
// through the admin API tests; here we just confirm the data model
// supports the property.
func TestRevokedEntry_StaysInConfig(t *testing.T) {
	now := time.Now().UTC()
	tokens := stdTokens()
	tokens[2].RevokedAt = &now
	cfg := &authConfig{Tokens: tokens}
	_ = newAuthStore(cfg)

	// Entry must still be present in the slice with RevokedAt intact.
	var found *tokenEntry
	for i := range cfg.Tokens {
		if cfg.Tokens[i].ID == "alice" {
			found = &cfg.Tokens[i]
			break
		}
	}
	if found == nil {
		t.Fatal("revoked entry was removed from cfg.Tokens; should be preserved for audit")
	}
	if found.RevokedAt == nil {
		t.Error("RevokedAt was cleared from the persisted entry")
	}
}

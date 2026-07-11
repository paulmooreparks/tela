package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
)

// These tests exercise admin_api.go at two levels. The view-builder
// tests set the globals (globalCfg, globalCfgMu) directly and call
// buildAccessEntry. The handler tests use withTestConfig plus an
// httptest.NewRecorder to drive the unexported handleAdmin* functions
// end to end through their auth gate, status codes, optimistic-
// concurrency (If-Match/ETag) paths, and config mutations. Real
// over-the-wire HTTP coverage (net/http against a live teststack hub)
// lives in admin_api_e2e_test.go (package hub_test), which closes the
// internal/teststack adoption box tracked by issue #6. Together these
// pin the admin REST contract that freezes at 1.0 (issue #8).

func TestBuildAccessEntry_ReportsConnectServicesFilter(t *testing.T) {
	globalCfgMu.Lock()
	prev := globalCfg
	defer func() {
		globalCfg = prev
		globalCfgMu.Unlock()
	}()

	globalCfg = &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "alice", Token: "tok-alice"},
			},
			Machines: map[string]machineACL{
				"barn": {
					ConnectTokens: []connectGrant{{Token: "tok-alice", Services: []string{"Jellyfin", "SSH"}}},
				},
			},
		},
	}

	entry := buildAccessEntry(globalCfg.Auth.Tokens[0])
	if len(entry.Machines) != 1 {
		t.Fatalf("expected 1 machine entry, got %d", len(entry.Machines))
	}
	got := entry.Machines[0]
	if got.MachineID != "barn" {
		t.Errorf("MachineID = %q, want %q", got.MachineID, "barn")
	}
	if !reflect.DeepEqual(got.Permissions, []string{"connect"}) {
		t.Errorf("Permissions = %v, want [connect]", got.Permissions)
	}
	if !reflect.DeepEqual(got.Services, []string{"Jellyfin", "SSH"}) {
		t.Errorf("Services = %v, want [Jellyfin SSH]", got.Services)
	}
}

func TestBuildAccessEntry_UnfilteredConnectOmitsServices(t *testing.T) {
	// A connect grant with no Services filter must leave the Services
	// field unset in the view, so JSON-encoded responses elide it
	// (avoiding a confusing "services: []" alongside unfiltered
	// grants).
	globalCfgMu.Lock()
	prev := globalCfg
	defer func() {
		globalCfg = prev
		globalCfgMu.Unlock()
	}()

	globalCfg = &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "alice", Token: "tok-alice"},
			},
			Machines: map[string]machineACL{
				"barn": {
					ConnectTokens: []connectGrant{{Token: "tok-alice"}},
				},
			},
		},
	}

	entry := buildAccessEntry(globalCfg.Auth.Tokens[0])
	if len(entry.Machines) != 1 {
		t.Fatalf("expected 1 machine entry, got %d", len(entry.Machines))
	}
	if entry.Machines[0].Services != nil {
		t.Errorf("unfiltered connect should leave Services nil, got %v", entry.Machines[0].Services)
	}
}

func TestBuildAccessEntry_OwnerAdminSkipsPerMachineServices(t *testing.T) {
	// Owner and admin get the implicit "all access" row; they should
	// not grow a Services filter even if somebody added one to their
	// token entry (which would be a misconfiguration but must not
	// break the view).
	globalCfgMu.Lock()
	prev := globalCfg
	defer func() {
		globalCfg = prev
		globalCfgMu.Unlock()
	}()

	globalCfg = &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
			},
			Machines: map[string]machineACL{
				"barn": {
					ConnectTokens: []connectGrant{{Token: "tok-owner", Services: []string{"ignored"}}},
				},
			},
		},
	}

	entry := buildAccessEntry(globalCfg.Auth.Tokens[0])
	if len(entry.Machines) != 1 {
		t.Fatalf("expected 1 machine entry (the implicit all-access row), got %d", len(entry.Machines))
	}
	got := entry.Machines[0]
	if got.MachineID != "*" {
		t.Errorf("owner machine entry should be the implicit *, got %q", got.MachineID)
	}
	if got.Services != nil {
		t.Errorf("owner row should not carry Services, got %v", got.Services)
	}
}

// withTestConfig swaps globalCfg with tc for the lifetime of the test
// and always restores the previous value. The mutex is acquired around
// both the install and the restore so tests can be run serially (our
// test binary runs tests sequentially, which is fine; -parallel would
// not be).
func withTestConfig(t *testing.T, tc *hubConfig) func() {
	t.Helper()
	globalCfgMu.Lock()
	prev := globalCfg
	prevAuth := globalAuth
	globalCfg = tc
	globalAuth = newAuthStore(&tc.Auth)
	globalCfgMu.Unlock()
	return func() {
		globalCfgMu.Lock()
		globalCfg = prev
		globalAuth = prevAuth
		globalCfgMu.Unlock()
	}
}

func TestAdminPatchAccess_RejectsDemotingLastOwner(t *testing.T) {
	// The hub must always have at least one owner-role identity. The
	// patch handler refuses to demote the last one (409) so the hub
	// cannot be left in an unmanageable state. Promoting a peer first
	// is the documented recovery path.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	body := strings.NewReader(`{"role":"admin"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/access/owner", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	respBody, _ := io.ReadAll(rr.Body)
	var resp map[string]string
	_ = json.Unmarshal(respBody, &resp)
	if !strings.Contains(resp["error"], "sole owner") {
		t.Errorf("error = %q, want one mentioning the sole owner guard", resp["error"])
	}
}

func TestAdminPatchAccess_ChangesRole(t *testing.T) {
	// Happy path: promoting a user to admin writes the new role,
	// persists, bumps the version counter, and returns a 200 with
	// an ETag the client can pin future requests to.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	body := strings.NewReader(`{"role":"admin"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/access/alice", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// Alice's role must have been updated in the config.
	for _, tkn := range globalCfg.Auth.Tokens {
		if tkn.ID == "alice" && tkn.HubRole != "admin" {
			t.Errorf("alice.HubRole = %q, want admin", tkn.HubRole)
		}
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("expected ETag on successful PATCH")
	}
}

func TestBuildAccessEntry_ReportsWildcardInheritance(t *testing.T) {
	// Clients render the effective per-machine permission state using
	// WildcardInherited on the identity record rather than re-
	// implementing the cascade rules. The hub cascades connect and
	// manage from the wildcard "*" ACL; register does not cascade.
	// This test pins that contract at the builder level.
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "charlie", Token: "tok-charlie"},
			},
			Machines: map[string]machineACL{
				"*": {
					ConnectTokens: []connectGrant{{Token: "tok-charlie", Services: []string{"SSH"}}},
					ManageTokens:  []string{"tok-charlie"},
				},
			},
		},
	})
	defer restore()

	entry := buildAccessEntry(globalCfg.Auth.Tokens[0])
	if !reflect.DeepEqual(entry.WildcardInherited, []string{"connect", "manage"}) {
		t.Errorf("WildcardInherited = %v, want [connect manage]", entry.WildcardInherited)
	}
	if !reflect.DeepEqual(entry.WildcardInheritedServices, []string{"SSH"}) {
		t.Errorf("WildcardInheritedServices = %v, want [SSH]", entry.WildcardInheritedServices)
	}
}

func TestBuildAccessEntry_OmitsWildcardInheritanceForOwnerAdmin(t *testing.T) {
	// Owner and admin identities get a role-based synthetic "*" entry
	// in Machines. Their access comes from the role, not the ACL
	// cascade, so WildcardInherited must be empty to avoid
	// double-counting implicit-all-access as both the synthetic "*"
	// row and a cascade.
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
			},
			Machines: map[string]machineACL{
				"*": {ConnectTokens: []connectGrant{{Token: "tok-owner"}}},
			},
		},
	})
	defer restore()

	entry := buildAccessEntry(globalCfg.Auth.Tokens[0])
	if len(entry.WildcardInherited) != 0 {
		t.Errorf("owner WildcardInherited should be empty, got %v", entry.WildcardInherited)
	}
}

func TestAdminPatchAccess_RejectsUnknownRole(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	body := strings.NewReader(`{"role":"superuser"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/access/alice", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// ── POST /api/admin/tokens/{id}/revoke ────────────────────────────

func TestAdminRevokeToken_MarksRevokedNoDelete(t *testing.T) {
	// Revocation is audit-trail-preserving: the entry stays in the
	// config with RevokedAt set, and the auth checks reject it on
	// every subsequent call. The endpoint returns 200 with the
	// revocation timestamp.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens/alice/revoke", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminTokenAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// Alice must still be in the slice (audit trail) but RevokedAt set.
	var found *tokenEntry
	for i := range globalCfg.Auth.Tokens {
		if globalCfg.Auth.Tokens[i].ID == "alice" {
			found = &globalCfg.Auth.Tokens[i]
		}
	}
	if found == nil {
		t.Fatal("alice was deleted from the config; revoke must preserve the audit entry")
	}
	if found.RevokedAt == nil {
		t.Error("RevokedAt was not set on the entry")
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("expected ETag on successful revoke")
	}
}

func TestAdminRevokeToken_BlocksAuthAfterRevoke(t *testing.T) {
	// After revocation, the token can no longer pass any auth check.
	// This is the primary guarantee of the feature: revocation
	// propagates within one request, not minutes.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
			Machines: map[string]machineACL{
				"barn": {ConnectTokens: []connectGrant{{Token: "tok-alice"}}},
			},
		},
	})
	defer restore()

	if !globalAuth.canConnect("tok-alice", "barn") {
		t.Fatal("precondition: alice should be able to connect before revocation")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens/alice/revoke", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminTokenAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("revoke returned %d; want 200", rr.Code)
	}

	// Reload the auth store from the now-mutated config so the change
	// is visible. Production hubs do this through the persistConfig +
	// hot-reload path; the test exercises the same downstream effect.
	globalAuth.reload(&globalCfg.Auth)

	if globalAuth.canConnect("tok-alice", "barn") {
		t.Error("revoked token still passed canConnect; revocation did not propagate")
	}
}

func TestAdminRevokeToken_RefusesLastOwner(t *testing.T) {
	// Same shape as the patch-access last-owner guard. If the only
	// owner is revoked, no one can manage the hub. The endpoint
	// returns 409 with a clear message.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens/owner/revoke", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminTokenAction(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	respBody, _ := io.ReadAll(rr.Body)
	var resp map[string]string
	_ = json.Unmarshal(respBody, &resp)
	if !strings.Contains(resp["error"], "last remaining owner") {
		t.Errorf("error = %q, want one mentioning the last-owner guard", resp["error"])
	}
}

func TestAdminRevokeToken_IdempotentSecondCall(t *testing.T) {
	// Calling revoke twice on the same identity returns 200 with
	// status "already-revoked" and does not change RevokedAt. This
	// keeps automation that retries on transient errors safe.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	doRevoke := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens/alice/revoke", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminTokenAction(rr, req)
		return rr
	}

	first := doRevoke()
	if first.Code != http.StatusOK {
		t.Fatalf("first revoke status = %d, want 200", first.Code)
	}
	var firstResp map[string]any
	_ = json.NewDecoder(first.Body).Decode(&firstResp)
	if firstResp["status"] != "revoked" {
		t.Errorf("first revoke status = %q, want \"revoked\"", firstResp["status"])
	}

	second := doRevoke()
	if second.Code != http.StatusOK {
		t.Fatalf("second revoke status = %d, want 200", second.Code)
	}
	var secondResp map[string]any
	_ = json.NewDecoder(second.Body).Decode(&secondResp)
	if secondResp["status"] != "already-revoked" {
		t.Errorf("second revoke status = %q, want \"already-revoked\"", secondResp["status"])
	}
}

// ── POST /api/admin/rotate/{id} (session-token interactions) ──────

func TestAdminRotate_RefreshesIssuedAtAndClearsRevocation(t *testing.T) {
	// Rotation is the "re-enable + new credential" operation. It
	// generates a new token, resets IssuedAt to now, and clears any
	// prior RevokedAt so an admin can rotate a revoked identity back
	// into service without a separate unrevoke step.
	resetAccessVersions()
	pastIssue := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	revokedTime := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice", IssuedAt: &pastIssue, RevokedAt: &revokedTime},
			},
		},
	})
	defer restore()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/rotate/alice", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminRotate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var found *tokenEntry
	for i := range globalCfg.Auth.Tokens {
		if globalCfg.Auth.Tokens[i].ID == "alice" {
			found = &globalCfg.Auth.Tokens[i]
		}
	}
	if found == nil {
		t.Fatal("alice missing after rotate")
	}
	if found.RevokedAt != nil {
		t.Error("rotation should clear RevokedAt")
	}
	if found.IssuedAt == nil || !found.IssuedAt.After(pastIssue) {
		t.Errorf("rotation should refresh IssuedAt; got %v, want after %v", found.IssuedAt, pastIssue)
	}
	if found.Token == "tok-alice" {
		t.Error("rotation should generate a new token value")
	}
}

// ── POST /api/admin/tokens with expiry ────────────────────────────

func TestAdminAddToken_RecordsExpiresAt(t *testing.T) {
	// The -expires flag flows through as RFC3339 in the request body
	// and the hub stores the parsed time.Time in the entry.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
			},
		},
	})
	defer restore()

	body := strings.NewReader(`{"id":"bob","expiresAt":"2027-01-01T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminTokens(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var found *tokenEntry
	for i := range globalCfg.Auth.Tokens {
		if globalCfg.Auth.Tokens[i].ID == "bob" {
			found = &globalCfg.Auth.Tokens[i]
		}
	}
	if found == nil {
		t.Fatal("new identity not created")
	}
	if found.ExpiresAt == nil {
		t.Fatal("ExpiresAt was not recorded")
	}
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if !found.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", found.ExpiresAt, want)
	}
	if found.IssuedAt == nil {
		t.Error("IssuedAt should be set on a newly-created token")
	}
}

func TestAdminAddToken_RejectsMalformedExpiresAt(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
			},
		},
	})
	defer restore()

	body := strings.NewReader(`{"id":"bob","expiresAt":"next tuesday"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminTokens(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// ── Auth enforcement across the requireOwnerOrAdmin-gated surface ─────

// fourRoleConfig returns a config with one identity per relevant class:
// an owner, an admin, a plain user, and a viewer. The auth-enforcement
// and precondition tables reuse it so every handler sees the same token
// set.
func fourRoleConfig() *hubConfig {
	return &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "admin", Token: "tok-admin", HubRole: "admin"},
				{ID: "user", Token: "tok-user"},
				{ID: "viewer", Token: "tok-viewer", HubRole: "viewer"},
			},
		},
	}
}

func TestAdmin_AuthEnforcement(t *testing.T) {
	// Every admin endpoint that goes through requireOwnerOrAdmin must 403
	// for a request with no Authorization header, an unknown token, a
	// user-role token, or a viewer-role token, and must let owner/admin
	// through. The gate is the frozen security boundary of the admin API,
	// so it is pinned here handler by handler.
	resetAccessVersions()
	restore := withTestConfig(t, fourRoleConfig())
	defer restore()

	gated := []struct {
		name   string
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{"handleAdminAccess", http.MethodGet, "/api/admin/access", handleAdminAccess},
		{"handleAdminTokens", http.MethodGet, "/api/admin/tokens", handleAdminTokens},
		{"handleAdminRotate", http.MethodPost, "/api/admin/rotate/user", handleAdminRotate},
		{"handleAdminTokenAction", http.MethodPost, "/api/admin/tokens/user/revoke", handleAdminTokenAction},
		{"handleAdminPortals", http.MethodGet, "/api/admin/portals", handleAdminPortals},
		{"handleAdminUpdate", http.MethodGet, "/api/admin/update", handleAdminUpdate},
		{"handleAdminUpdateSources", http.MethodGet, "/api/admin/update/sources", handleAdminUpdateSources},
		{"handleAdminLogs", http.MethodGet, "/api/admin/logs", handleAdminLogs},
		// handleAdminRestart: only the 403 (non-owner/admin) path is safe
		// to exercise. Its 200 path schedules hubRestartSelf, which calls
		// os.Exit on a background goroutine and would tear down the whole
		// test binary. The rejection branch returns before scheduling
		// anything, so calling it with a non-owner token is safe. Do not
		// add an owner/admin call here or elsewhere in the suite.
		{"handleAdminRestart", http.MethodPost, "/api/admin/restart", handleAdminRestart},
	}

	unauthorized := []struct{ name, header string }{
		{"no auth header", ""},
		{"unknown token", "Bearer tok-does-not-exist"},
		{"user role", "Bearer tok-user"},
		{"viewer role", "Bearer tok-viewer"},
	}

	for _, h := range gated {
		for _, u := range unauthorized {
			t.Run(h.name+"/"+u.name, func(t *testing.T) {
				req := httptest.NewRequest(h.method, h.path, nil)
				if u.header != "" {
					req.Header.Set("Authorization", u.header)
				}
				rr := httptest.NewRecorder()
				h.fn(rr, req)
				if rr.Code != http.StatusForbidden {
					t.Errorf("%s with %s: status = %d, want 403", h.name, u.name, rr.Code)
				}
			})
		}
	}

	// Owner and admin must pass the gate (non-403) on a representative,
	// side-effect-free call. handleAdminAccess GET (list) is chosen
	// because it neither mutates config nor performs network I/O;
	// handleAdminUpdate GET fetches a manifest and handleAdminRestart
	// schedules a restart, so neither is a safe representative here.
	for _, tok := range []struct{ name, header string }{
		{"owner", "Bearer tok-owner"},
		{"admin", "Bearer tok-admin"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/access", nil)
		req.Header.Set("Authorization", tok.header)
		rr := httptest.NewRecorder()
		handleAdminAccess(rr, req)
		if rr.Code == http.StatusForbidden {
			t.Errorf("%s: got 403, expected to pass the owner/admin gate", tok.name)
		}
	}
}

// TestAdminAgents_AuthEnforcement covers the agent-mgmt route's gate only.
// Per the card's out-of-scope note, the actual forwarding-to-telad round
// trip belongs to a dedicated agent-proxy suite; here we only assert that
// non-owner/admin tokens are refused. handleAdminAgents gates on canManage
// (not requireOwnerOrAdmin), so an unknown/user/viewer token that holds no
// manage grant for the target machine gets 403.
func TestAdminAgents_AuthEnforcement(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, fourRoleConfig())
	defer restore()

	for _, u := range []struct{ name, header string }{
		{"no auth header", ""},
		{"unknown token", "Bearer tok-does-not-exist"},
		{"user role", "Bearer tok-user"},
		{"viewer role", "Bearer tok-viewer"},
	} {
		t.Run(u.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/barn/restart", nil)
			if u.header != "" {
				req.Header.Set("Authorization", u.header)
			}
			rr := httptest.NewRecorder()
			handleAdminAgents(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", u.name, rr.Code)
			}
		})
	}
}

// ── GET /api/admin/access/{id} ───────────────────────────────────────

func TestAdminGetAccess_KnownAndUnknown(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/access/alice", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	wantETag := `"` + strconv.FormatUint(currentAccessVersion("alice"), 10) + `"`
	if got := rr.Header().Get("ETag"); got != wantETag {
		t.Errorf("ETag = %q, want %q (the identity version)", got, wantETag)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/access/ghost", nil)
	req2.Header.Set("Authorization", "Bearer tok-owner")
	rr2 := httptest.NewRecorder()
	handleAdminAccess(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", rr2.Code)
	}
}

// ── DELETE /api/admin/access/{id} ────────────────────────────────────

func TestAdminDeleteAccess_RemovesEntirelyAndScrubsACLs(t *testing.T) {
	// Deletion differs from revocation: revoke keeps the entry in
	// Auth.Tokens with RevokedAt set (audit trail, see
	// TestAdminRevokeToken_MarksRevokedNoDelete), while delete removes
	// the identity from Auth.Tokens outright and scrubs its token from
	// every machine ACL's register/connect/manage lists.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
			Machines: map[string]machineACL{
				"barn": {
					RegisterToken: "tok-alice",
					ConnectTokens: []connectGrant{{Token: "tok-alice"}},
					ManageTokens:  []string{"tok-alice"},
				},
			},
		},
	})
	defer restore()

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/access/alice", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if findTokenEntryInCfg("alice") != nil {
		t.Error("alice still present in Auth.Tokens after delete; delete must remove the entry entirely")
	}
	acl := globalCfg.Auth.Machines["barn"]
	if acl.RegisterToken == "tok-alice" {
		t.Error("alice's RegisterToken not scrubbed from barn ACL")
	}
	if hasConnectGrant(acl.ConnectTokens, "tok-alice") {
		t.Error("alice's connect grant not scrubbed from barn ACL")
	}
	for _, mt := range acl.ManageTokens {
		if mt == "tok-alice" {
			t.Error("alice's manage token not scrubbed from barn ACL")
		}
	}

	req2 := httptest.NewRequest(http.MethodDelete, "/api/admin/access/ghost", nil)
	req2.Header.Set("Authorization", "Bearer tok-owner")
	rr2 := httptest.NewRecorder()
	handleAdminAccess(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", rr2.Code)
	}
}

// TestAdminDeleteAccess_SoleOwnerCharacterization pins the CURRENT,
// UNGUARDED behavior of deleting the sole remaining owner via
// DELETE /api/admin/access/{id}. Unlike adminRevokeToken and
// adminPatchAccess (which both count owners and refuse at <=1 with 409),
// adminDeleteAccess has no last-owner guard: it removes the entry and
// returns 200 even when the target is the last owner, which is a real
// self-lockout path. This is decision D6 / card tela-14 item 7574. The
// gap is tracked as a separate fix in card tela-66 (Last-Owner Guard on
// Identity Deletion). This test deliberately asserts the present behavior
// so it goes red the moment tela-66 lands the guard; at that point flip
// the expectation from 200 to 409 and delete this comment.
func TestAdminDeleteAccess_SoleOwnerCharacterization(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
		},
	})
	defer restore()

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/access/owner", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	// CURRENT behavior: the sole owner is deleted (200). See tela-66.
	if rr.Code != http.StatusOK {
		t.Fatalf("sole-owner delete status = %d, want 200 (current unguarded behavior; see card tela-66); body=%s", rr.Code, rr.Body.String())
	}
	if findTokenEntryInCfg("owner") != nil {
		t.Error("sole owner still present after delete; characterization expected removal under current behavior")
	}
}

// ── PUT/DELETE /api/admin/access/{id}/machines/{m} ───────────────────

func TestAdminSetMachineAccess_SetsPermsServicesAndCreatesMap(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
			// Machines intentionally left nil so we exercise the
			// lazy-initialization path in adminSetMachineAccess.
		},
	})
	defer restore()

	body := strings.NewReader(`{"permissions":["register","connect","manage"],"services":["SSH"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/access/alice/machines/barn", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if globalCfg.Auth.Machines == nil {
		t.Fatal("Auth.Machines was not created")
	}
	acl := globalCfg.Auth.Machines["barn"]
	if acl.RegisterToken != "tok-alice" {
		t.Errorf("RegisterToken = %q, want tok-alice", acl.RegisterToken)
	}
	grant, ok := findConnectGrant(acl.ConnectTokens, "tok-alice")
	if !ok {
		t.Fatal("no connect grant for alice on barn")
	}
	if !reflect.DeepEqual(grant.Services, []string{"SSH"}) {
		t.Errorf("connect services = %v, want [SSH]", grant.Services)
	}
	if !inTokenList(acl.ManageTokens, "tok-alice") {
		t.Error("manage token for alice not set on barn")
	}

	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if v, ok := resp["version"].(float64); !ok || uint64(v) <= 1 {
		t.Errorf("response version = %v, want a bumped value > 1", resp["version"])
	}
}

func TestAdminRevokeMachineAccess_ClearsAndNotFound(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			},
			Machines: map[string]machineACL{
				"barn": {
					RegisterToken: "tok-alice",
					ConnectTokens: []connectGrant{{Token: "tok-alice"}},
					ManageTokens:  []string{"tok-alice"},
				},
			},
		},
	})
	defer restore()

	// Happy path: 200 and all of alice's perms on barn cleared.
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/access/alice/machines/barn", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	acl := globalCfg.Auth.Machines["barn"]
	if acl.RegisterToken == "tok-alice" || hasConnectGrant(acl.ConnectTokens, "tok-alice") || inTokenList(acl.ManageTokens, "tok-alice") {
		t.Error("alice still has permissions on barn after revoke")
	}

	// Unknown identity: 404.
	req2 := httptest.NewRequest(http.MethodDelete, "/api/admin/access/ghost/machines/barn", nil)
	req2.Header.Set("Authorization", "Bearer tok-owner")
	rr2 := httptest.NewRecorder()
	handleAdminAccess(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("unknown identity status = %d, want 404", rr2.Code)
	}

	// Machine with no ACL entry at all: 404.
	req3 := httptest.NewRequest(http.MethodDelete, "/api/admin/access/alice/machines/no-such-machine", nil)
	req3.Header.Set("Authorization", "Bearer tok-owner")
	rr3 := httptest.NewRecorder()
	handleAdminAccess(rr3, req3)
	if rr3.Code != http.StatusNotFound {
		t.Fatalf("unknown machine status = %d, want 404", rr3.Code)
	}
}

// ── PATCH rename collision ───────────────────────────────────────────

func TestAdminPatchAccess_RenameCollision(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
				{ID: "bob", Token: "tok-bob"},
			},
		},
	})
	defer restore()

	body := strings.NewReader(`{"id":"bob"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/access/alice", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminAccess(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	// Neither identity may be mutated by a rejected rename.
	if e := findTokenEntryInCfg("alice"); e == nil || e.Token != "tok-alice" {
		t.Error("alice was mutated by a rejected rename collision")
	}
	if e := findTokenEntryInCfg("bob"); e == nil || e.Token != "tok-bob" {
		t.Error("bob was mutated by a rejected rename collision")
	}
}

// ── 412 Precondition Failed across every If-Match-guarded mutation ────

func assertAccessConflict(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 412 body: %v", err)
	}
	if _, ok := body["version"]; !ok {
		t.Error("412 body missing current version")
	}
	if _, ok := body["current"]; !ok {
		t.Error("412 body missing current accessEntry")
	}
}

func TestAdminAccess_PreconditionFailed(t *testing.T) {
	// A stale If-Match against any of the six mutating access/token
	// endpoints returns 412 with the current version and accessEntry, and
	// leaves the config untouched. The version counter initializes to 1 on
	// first read, so "999" is always stale.
	const stale = `"999"`

	cases := []struct {
		name    string
		cfg     *hubConfig
		build   func() *http.Request
		handler func(http.ResponseWriter, *http.Request)
		// mutated reports whether the config changed (should stay false).
		mutated func() bool
	}{
		{
			name: "adminPatchAccess",
			cfg: &hubConfig{Auth: authConfig{Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			}}},
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodPatch, "/api/admin/access/alice", strings.NewReader(`{"role":"admin"}`))
				r.Header.Set("Content-Type", "application/json")
				return r
			},
			handler: handleAdminAccess,
			mutated: func() bool {
				e := findTokenEntryInCfg("alice")
				return e == nil || e.HubRole != ""
			},
		},
		{
			name: "adminDeleteAccess",
			cfg: &hubConfig{Auth: authConfig{Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			}}},
			build: func() *http.Request {
				return httptest.NewRequest(http.MethodDelete, "/api/admin/access/alice", nil)
			},
			handler: handleAdminAccess,
			mutated: func() bool { return findTokenEntryInCfg("alice") == nil },
		},
		{
			name: "adminSetMachineAccess",
			cfg: &hubConfig{Auth: authConfig{Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			}}},
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodPut, "/api/admin/access/alice/machines/barn", strings.NewReader(`{"permissions":["connect"]}`))
				r.Header.Set("Content-Type", "application/json")
				return r
			},
			handler: handleAdminAccess,
			mutated: func() bool {
				acl, ok := globalCfg.Auth.Machines["barn"]
				return ok && hasConnectGrant(acl.ConnectTokens, "tok-alice")
			},
		},
		{
			name: "adminRevokeMachineAccess",
			cfg: &hubConfig{Auth: authConfig{
				Tokens: []tokenEntry{
					{ID: "owner", Token: "tok-owner", HubRole: "owner"},
					{ID: "alice", Token: "tok-alice"},
				},
				Machines: map[string]machineACL{
					"barn": {ConnectTokens: []connectGrant{{Token: "tok-alice"}}},
				},
			}},
			build: func() *http.Request {
				return httptest.NewRequest(http.MethodDelete, "/api/admin/access/alice/machines/barn", nil)
			},
			handler: handleAdminAccess,
			mutated: func() bool {
				acl := globalCfg.Auth.Machines["barn"]
				return !hasConnectGrant(acl.ConnectTokens, "tok-alice")
			},
		},
		{
			name: "handleAdminRotate",
			cfg: &hubConfig{Auth: authConfig{Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			}}},
			build: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/admin/rotate/alice", nil)
			},
			handler: handleAdminRotate,
			mutated: func() bool {
				e := findTokenEntryInCfg("alice")
				return e == nil || e.Token != "tok-alice"
			},
		},
		{
			name: "adminRevokeToken",
			// alice is a plain user so the last-owner guard (which runs
			// before the If-Match check in adminRevokeToken) does not
			// short-circuit to 409 first.
			cfg: &hubConfig{Auth: authConfig{Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"},
			}}},
			build: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/admin/tokens/alice/revoke", nil)
			},
			handler: handleAdminTokenAction,
			mutated: func() bool {
				e := findTokenEntryInCfg("alice")
				return e == nil || e.RevokedAt != nil
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetAccessVersions()
			restore := withTestConfig(t, c.cfg)
			defer restore()

			req := c.build()
			req.Header.Set("Authorization", "Bearer tok-owner")
			req.Header.Set("If-Match", stale)
			rr := httptest.NewRecorder()
			c.handler(rr, req)

			assertAccessConflict(t, rr)
			if c.mutated() {
				t.Errorf("%s mutated the config despite a stale If-Match", c.name)
			}
		})
	}

	// Omitting If-Match skips the precondition entirely: a rotate with no
	// If-Match still succeeds. This is the "at least one of the six called
	// without the header" leg of the acceptance criterion.
	t.Run("no If-Match still succeeds", func(t *testing.T) {
		resetAccessVersions()
		restore := withTestConfig(t, &hubConfig{Auth: authConfig{Tokens: []tokenEntry{
			{ID: "owner", Token: "tok-owner", HubRole: "owner"},
			{ID: "alice", Token: "tok-alice"},
		}}})
		defer restore()

		req := httptest.NewRequest(http.MethodPost, "/api/admin/rotate/alice", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminRotate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if e := findTokenEntryInCfg("alice"); e == nil || e.Token == "tok-alice" {
			t.Error("rotate without If-Match did not regenerate the token")
		}
	})
}

// ── POST /api/admin/tokens duplicate id ──────────────────────────────

func TestAdminAddToken_DuplicateID(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
			},
		},
	})
	defer restore()

	body := strings.NewReader(`{"id":"owner"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens", body)
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminTokens(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "already exists") {
		t.Errorf("error = %q, want one mentioning the identity already exists", resp["error"])
	}
}

// ── GET /api/admin/tokens list view ──────────────────────────────────

func TestAdminListTokens_RoleDefaultPreviewAndTimestamps(t *testing.T) {
	resetAccessVersions()
	issued := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	expires := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				// alice: long token (exercises the 8-char preview
				// truncation), empty HubRole (defaults to "user"),
				// IssuedAt + ExpiresAt set, RevokedAt absent.
				{ID: "alice", Token: "0123456789abcdef0123", IssuedAt: &issued, ExpiresAt: &expires},
			},
		},
	})
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/tokens", nil)
	req.Header.Set("Authorization", "Bearer tok-owner")
	rr := httptest.NewRecorder()
	handleAdminTokens(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Tokens []adminTokenView `json:"tokens"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var alice *adminTokenView
	for i := range resp.Tokens {
		if resp.Tokens[i].ID == "alice" {
			alice = &resp.Tokens[i]
		}
	}
	if alice == nil {
		t.Fatal("alice not in token list")
	}
	if alice.Role != "user" {
		t.Errorf("alice.Role = %q, want user (default for empty HubRole)", alice.Role)
	}
	if alice.TokenPreview != "01234567..." {
		t.Errorf("alice.TokenPreview = %q, want \"01234567...\"", alice.TokenPreview)
	}
	if alice.IssuedAt == nil || !alice.IssuedAt.Equal(issued) {
		t.Errorf("alice.IssuedAt = %v, want %v", alice.IssuedAt, issued)
	}
	if alice.ExpiresAt == nil || !alice.ExpiresAt.Equal(expires) {
		t.Errorf("alice.ExpiresAt = %v, want %v", alice.ExpiresAt, expires)
	}
	if alice.RevokedAt != nil {
		t.Errorf("alice.RevokedAt = %v, want nil (omitted)", alice.RevokedAt)
	}
}

// ── Portals ──────────────────────────────────────────────────────────

func TestAdminPortals_Lifecycle(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Name: "testhub",
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
			},
		},
	})
	defer restore()

	// Stand-in portal. registerWithPortal first probes
	// /.well-known/tela (we 404 it so it falls back to /api/hubs), then
	// POSTs the registration. We return updated=false on the first
	// registration and updated=true on the second for the same name, and
	// always include a hubsync_-prefixed sync token per protocol 1.0.
	const secretSyncToken = "hubsync_secret_value_do_not_leak"
	var registrations int
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/tela" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		registrations++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"updated":   registrations > 1,
			"syncToken": secretSyncToken,
		})
	}))
	defer portal.Close()

	// Empty list first.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/portals", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminPortals(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("list status = %d, want 200", rr.Code)
		}
		var resp struct {
			Portals []adminPortalView `json:"portals"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if len(resp.Portals) != 0 {
			t.Errorf("portals = %v, want empty", resp.Portals)
		}
	}

	addBody := fmt.Sprintf(`{"name":"p1","portalUrl":%q,"hubUrl":"https://myhub.example"}`, portal.URL)

	// First registration -> 201 "created".
	{
		req := httptest.NewRequest(http.MethodPost, "/api/admin/portals", strings.NewReader(addBody))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminPortals(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("first add status = %d, want 201; body=%s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp["status"] != "created" {
			t.Errorf("first add status = %v, want created", resp["status"])
		}
	}

	// Second registration for the same name -> 201 "updated".
	{
		req := httptest.NewRequest(http.MethodPost, "/api/admin/portals", strings.NewReader(addBody))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminPortals(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("second add status = %d, want 201; body=%s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp["status"] != "updated" {
			t.Errorf("second add status = %v, want updated", resp["status"])
		}
	}

	// Populated list: HasSyncToken true, and the raw sync token never
	// appears in the response body.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/portals", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminPortals(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("list status = %d, want 200", rr.Code)
		}
		if strings.Contains(rr.Body.String(), secretSyncToken) {
			t.Error("raw sync token leaked in portals list response")
		}
		var resp struct {
			Portals []adminPortalView `json:"portals"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if len(resp.Portals) != 1 || resp.Portals[0].Name != "p1" || !resp.Portals[0].HasSyncToken {
			t.Errorf("portals = %+v, want one p1 with HasSyncToken true", resp.Portals)
		}
	}

	// Missing required fields -> 400.
	{
		req := httptest.NewRequest(http.MethodPost, "/api/admin/portals", strings.NewReader(`{"name":"p2"}`))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminPortals(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("missing-fields status = %d, want 400", rr.Code)
		}
	}

	// Remove known -> 200, unknown -> 404.
	{
		req := httptest.NewRequest(http.MethodDelete, "/api/admin/portals?name=p1", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminPortals(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("remove known status = %d, want 200", rr.Code)
		}
		req2 := httptest.NewRequest(http.MethodDelete, "/api/admin/portals?name=ghost", nil)
		req2.Header.Set("Authorization", "Bearer tok-owner")
		rr2 := httptest.NewRecorder()
		handleAdminPortals(rr2, req2)
		if rr2.Code != http.StatusNotFound {
			t.Fatalf("remove unknown status = %d, want 404", rr2.Code)
		}
	}
}

// ── GET/PATCH/POST /api/admin/update ─────────────────────────────────

func TestAdminUpdateGet_ReachableAndUnreachable(t *testing.T) {
	resetAccessVersions()

	manifest := channel.Manifest{
		Channel:      "e2etest",
		Version:      "v1.0.0",
		Tag:          "v1.0.0",
		DownloadBase: "https://example.com/dl/",
		Binaries: map[string]channel.BinaryEntry{
			"telahubd-linux-amd64": {SHA256: strings.Repeat("a", 64), Size: 1},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	defer srv.Close()

	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{Tokens: []tokenEntry{
			{ID: "owner", Token: "tok-owner", HubRole: "owner"},
		}},
		Update: updateConfig{
			Channel: "e2etest",
			Sources: map[string]string{"e2etest": srv.URL},
		},
	})
	defer restore()

	// Reachable manifest: 200 with latestVersion from the manifest and
	// updateAvailable computed by channel.ShouldOfferUpdate. The running
	// version is "dev" in tests, so any real target is offered.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/update", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminUpdate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp["latestVersion"] != "v1.0.0" {
			t.Errorf("latestVersion = %v, want v1.0.0", resp["latestVersion"])
		}
		if resp["updateAvailable"] != true {
			t.Errorf("updateAvailable = %v, want true", resp["updateAvailable"])
		}
	}

	// Unreachable source: still 200, but with an error field and
	// updateAvailable false. We point the channel at a server we have
	// already closed so the fetch fails with no cache to fall back on.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	globalCfgMu.Lock()
	globalCfg.Update.Channel = "deadchan"
	globalCfg.Update.Sources = map[string]string{"deadchan": deadURL}
	globalCfgMu.Unlock()
	globalAuth.reload(&globalCfg.Auth)

	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/update", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminUpdate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unreachable status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if _, ok := resp["error"]; !ok {
			t.Error("expected an error field when the manifest source is unreachable")
		}
		if resp["updateAvailable"] != false {
			t.Errorf("updateAvailable = %v, want false", resp["updateAvailable"])
		}
		if resp["latestVersion"] != "" {
			t.Errorf("latestVersion = %v, want empty on fetch failure", resp["latestVersion"])
		}
	}
}

func TestAdminUpdatePatch_ChannelAndManifestBase(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{Tokens: []tokenEntry{
			{ID: "owner", Token: "tok-owner", HubRole: "owner"},
		}},
	})
	defer restore()

	// Valid channel -> 200, persisted into globalCfg.
	{
		req := httptest.NewRequest(http.MethodPatch, "/api/admin/update", strings.NewReader(`{"channel":"beta"}`))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminUpdate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("valid channel status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if globalCfg.Update.Channel != "beta" {
			t.Errorf("Update.Channel = %q, want beta", globalCfg.Update.Channel)
		}
	}

	// Invalid channel name -> 400.
	{
		req := httptest.NewRequest(http.MethodPatch, "/api/admin/update", strings.NewReader(`{"channel":"bad name"}`))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminUpdate(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid channel status = %d, want 400", rr.Code)
		}
	}

	// A manifestBase in the body is redirected into Sources[channel].
	{
		req := httptest.NewRequest(http.MethodPatch, "/api/admin/update", strings.NewReader(`{"channel":"local","manifestBase":"https://mirror.example/local"}`))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminUpdate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("manifestBase status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if globalCfg.Update.Sources["local"] != "https://mirror.example/local" {
			t.Errorf("Sources[local] = %q, want the redirected manifestBase", globalCfg.Update.Sources["local"])
		}
	}
}

func TestAdminUpdatePost_AlreadyRunning(t *testing.T) {
	// The only POST /api/admin/update path this card exercises is the
	// "already running" short-circuit, which returns before the async
	// download+restart goroutine. Any POST whose resolved version differs
	// from the running version would schedule hubDownloadAndStage followed
	// by hubRestartSelf (os.Exit) on a goroutine and kill the test binary.
	// We swap the package-level version var to a fixed value, POST that
	// exact version, and restore the var afterward. Do NOT change this to
	// POST a different version.
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{Tokens: []tokenEntry{
			{ID: "owner", Token: "tok-owner", HubRole: "owner"},
		}},
	})
	defer restore()

	savedVersion := version
	version = "v9.9.9"
	defer func() { version = savedVersion }()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/update", strings.NewReader(`{"version":"v9.9.9"}`))
	req.Header.Set("Authorization", "Bearer tok-owner")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAdminUpdate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "already running") {
		t.Errorf("message = %q, want one mentioning already running", msg)
	}
}

// ── handleAdminUpdateSources CRUD ────────────────────────────────────

func TestAdminUpdateSources_CRUD(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{Tokens: []tokenEntry{
			{ID: "owner", Token: "tok-owner", HubRole: "owner"},
		}},
		Update: updateConfig{Sources: map[string]string{"dev": "https://dev.example"}},
	})
	defer restore()

	// GET lists the current sources.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/update/sources", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminUpdateSources(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("list status = %d, want 200", rr.Code)
		}
		var resp struct {
			Sources map[string]string `json:"sources"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp.Sources["dev"] != "https://dev.example" {
			t.Errorf("sources[dev] = %q, want https://dev.example", resp.Sources["dev"])
		}
	}

	// PUT sets a source; a trailing "/foo.json" is normalized off.
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/update/sources/beta", strings.NewReader(`{"base":"https://mirror.example/beta/beta.json"}`))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminUpdateSources(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("put status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if globalCfg.Update.Sources["beta"] != "https://mirror.example/beta" {
			t.Errorf("sources[beta] = %q, want the .json and slash stripped", globalCfg.Update.Sources["beta"])
		}
	}

	// PUT with an invalid channel name -> 400.
	{
		req := httptest.NewRequest(http.MethodPut, "/api/admin/update/sources/bad_name", strings.NewReader(`{"base":"https://x.example"}`))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleAdminUpdateSources(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid-name put status = %d, want 400", rr.Code)
		}
	}

	// DELETE a known entry -> 200; unknown -> 404.
	{
		req := httptest.NewRequest(http.MethodDelete, "/api/admin/update/sources/dev", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminUpdateSources(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("delete known status = %d, want 200", rr.Code)
		}
		req2 := httptest.NewRequest(http.MethodDelete, "/api/admin/update/sources/ghost", nil)
		req2.Header.Set("Authorization", "Bearer tok-owner")
		rr2 := httptest.NewRecorder()
		handleAdminUpdateSources(rr2, req2)
		if rr2.Code != http.StatusNotFound {
			t.Fatalf("delete unknown status = %d, want 404", rr2.Code)
		}
	}
}

// ── GET /api/admin/logs ──────────────────────────────────────────────

func TestAdminLogs_DefaultAndLimit(t *testing.T) {
	resetAccessVersions()
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{Tokens: []tokenEntry{
			{ID: "owner", Token: "tok-owner", HubRole: "owner"},
		}},
	})
	defer restore()

	// Seed the shared log ring with more than the default 100 lines so the
	// default cap and the ?lines=N override produce distinct counts. Save
	// and restore the ring so we do not perturb other tests.
	logRingMu.Lock()
	savedRing := logRing
	savedPos := logRingPos
	savedLen := logRingLen
	logRingPos = 0
	logRingLen = 0
	for i := 0; i < 150; i++ {
		logRing[logRingPos] = fmt.Sprintf("seeded-line-%d", i)
		logRingPos = (logRingPos + 1) % logRingSize
		if logRingLen < logRingSize {
			logRingLen++
		}
	}
	logRingMu.Unlock()
	defer func() {
		logRingMu.Lock()
		logRing = savedRing
		logRingPos = savedPos
		logRingLen = savedLen
		logRingMu.Unlock()
	}()

	decodeLines := func(rr *httptest.ResponseRecorder) []string {
		var resp struct {
			Lines []string `json:"lines"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		return resp.Lines
	}

	// Default: no query param -> 100 lines.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/logs", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminLogs(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if got := len(decodeLines(rr)); got != 100 {
			t.Errorf("default lines = %d, want 100", got)
		}
	}

	// Override: ?lines=5 -> 5 lines.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/admin/logs?lines=5", nil)
		req.Header.Set("Authorization", "Bearer tok-owner")
		rr := httptest.NewRecorder()
		handleAdminLogs(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if got := len(decodeLines(rr)); got != 5 {
			t.Errorf("lines=5 returned %d lines, want 5", got)
		}
	}
}

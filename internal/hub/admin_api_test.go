package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// These tests exercise admin_api.go's view builder with the globals
// (globalCfg, globalCfgMu) set up explicitly. End-to-end HTTP tests
// against the admin handlers are tracked as part of issue #8 and the
// in-process test harness (issue #6); for now we cover the Services
// round-trip at the builder level, which is where the Phase 3 schema
// change lands.

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

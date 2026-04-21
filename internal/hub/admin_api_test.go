package hub

import (
	"reflect"
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

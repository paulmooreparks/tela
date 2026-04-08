package portal_test

// teststack_test.go runs the portal HTTP handlers against a real
// in-process Tela hub spun up by internal/teststack. The unit tests in
// server_test.go cover handler logic with stub upstreams; this file is
// the spec-conformance layer that proves the portal protocol composes
// correctly against a real hub on the other end of the wire.
//
// Coverage:
//   - Discovery: GET /.well-known/tela returns the documented shape.
//   - Directory CRUD: POST /api/hubs registers a real hub, GET lists
//     it, DELETE removes it.
//   - Sync flow: PATCH /api/hubs/sync rotates the viewer token using
//     the sync token returned at registration time.
//   - Fleet aggregation: GET /api/fleet/agents calls the real hub's
//     /api/status and tags each machine with hub + hubUrl.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/paulmooreparks/tela/internal/portal"
	"github.com/paulmooreparks/tela/internal/portal/store/file"
	"github.com/paulmooreparks/tela/internal/teststack"
)

// newConformanceFixture builds the full chain: a teststack hub, a
// fresh file-backed portal store, and an httptest.Server wrapping the
// portal.Server. Returns the portal HTTP base URL and the live stack
// so individual tests can register machines on the harness hub.
func newConformanceFixture(t *testing.T) (portalBase string, stack *teststack.Stack) {
	t.Helper()

	stack = teststack.New(t)

	fs, err := file.Open(filepath.Join(t.TempDir(), "portal.yaml"))
	if err != nil {
		t.Fatalf("file.Open: %v", err)
	}
	srv := portal.NewServer(fs)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts.URL, stack
}

// TestConformance_Discovery checks that GET /.well-known/tela on a
// portal returns the documented shape from DESIGN-portal.md section 2.
func TestConformance_Discovery(t *testing.T) {
	portalBase, _ := newConformanceFixture(t)

	status, body := doJSON(t, http.MethodGet, portalBase+"/.well-known/tela", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got, _ := body["hub_directory"].(string); got != portal.HubDirectoryPath {
		t.Errorf("hub_directory = %q, want %q", got, portal.HubDirectoryPath)
	}
	if got, _ := body["protocolVersion"].(string); got != portal.ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", got, portal.ProtocolVersion)
	}
	versions, _ := body["supportedVersions"].([]any)
	found := false
	for _, v := range versions {
		if s, _ := v.(string); s == portal.ProtocolVersion {
			found = true
		}
	}
	if !found {
		t.Errorf("supportedVersions %v missing %q", versions, portal.ProtocolVersion)
	}
}

// TestConformance_DirectoryCRUD walks a hub through register → list →
// delete on the portal directory endpoints, using the real harness hub
// URL as the registered URL.
func TestConformance_DirectoryCRUD(t *testing.T) {
	portalBase, stack := newConformanceFixture(t)
	hubURL := stack.HubHTTP()

	// POST /api/hubs
	status, body := doJSON(t, http.MethodPost, portalBase+portal.HubDirectoryPath, map[string]string{
		"name":       "harness",
		"url":        hubURL,
		"adminToken": "test-admin-token",
	})
	if status != http.StatusOK {
		t.Fatalf("POST /api/hubs status = %d", status)
	}
	if tok, _ := body["syncToken"].(string); tok == "" {
		t.Error("expected non-empty syncToken on first registration")
	}
	if upd, _ := body["updated"].(bool); upd {
		t.Error("expected updated=false on first registration")
	}
	hubs := hubsFromBody(t, body)
	if len(hubs) != 1 || hubs[0].Name != "harness" {
		t.Fatalf("hubs after add = %+v", hubs)
	}
	if hubs[0].URL != hubURL {
		t.Errorf("hub URL = %q, want %q", hubs[0].URL, hubURL)
	}
	if !hubs[0].CanManage {
		t.Error("expected canManage=true for single-user file store")
	}

	// GET /api/hubs
	status, body = doJSON(t, http.MethodGet, portalBase+portal.HubDirectoryPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/hubs status = %d", status)
	}
	if got := hubsFromBody(t, body); len(got) != 1 || got[0].Name != "harness" {
		t.Fatalf("listed hubs = %+v", got)
	}

	// DELETE /api/hubs?name=harness
	status, body = doJSON(t, http.MethodDelete, portalBase+portal.HubDirectoryPath+"?name=harness", nil)
	if status != http.StatusOK {
		t.Fatalf("DELETE /api/hubs status = %d", status)
	}
	if got := hubsFromBody(t, body); len(got) != 0 {
		t.Errorf("hubs after delete = %+v, want empty", got)
	}
}

// TestConformance_SyncFlow registers a hub via POST, then uses the
// returned sync token to PATCH a new viewer token via /api/hubs/sync.
// Also asserts that a wrong sync token is rejected with 401.
func TestConformance_SyncFlow(t *testing.T) {
	portalBase, stack := newConformanceFixture(t)
	hubURL := stack.HubHTTP()

	status, body := doJSON(t, http.MethodPost, portalBase+portal.HubDirectoryPath, map[string]string{
		"name":        "harness",
		"url":         hubURL,
		"viewerToken": "initial-viewer",
		"adminToken":  "ignored",
	})
	if status != http.StatusOK {
		t.Fatalf("POST /api/hubs status = %d", status)
	}
	syncToken, _ := body["syncToken"].(string)
	if syncToken == "" {
		t.Fatal("no sync token returned")
	}

	// PATCH /api/hubs/sync with the correct sync token.
	patchBody, _ := json.Marshal(map[string]string{
		"name":        "harness",
		"viewerToken": "rotated-viewer",
	})
	req, err := http.NewRequest(http.MethodPatch, portalBase+portal.HubDirectoryPath+"/sync", bytes.NewReader(patchBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+syncToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH sync: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d, want 200", resp.StatusCode)
	}

	// PATCH /api/hubs/sync with a wrong sync token must be 401.
	req2, _ := http.NewRequest(http.MethodPatch, portalBase+portal.HubDirectoryPath+"/sync", bytes.NewReader(patchBody))
	req2.Header.Set("Authorization", "Bearer hubsync_wrongwrongwrongwrongwrongwrongwrongwrong")
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("PATCH sync (bad token): %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad sync token status = %d, want 401", resp2.StatusCode)
	}
}

// TestConformance_FleetAgents registers the harness hub with the
// portal, starts an agent that registers a machine, and verifies that
// GET /api/fleet/agents returns that machine tagged with hub + hubUrl.
// This is the end-to-end check that the portal can call a real Tela
// hub's /api/status and merge the results per spec section 5.
func TestConformance_FleetAgents(t *testing.T) {
	portalBase, stack := newConformanceFixture(t)
	hubURL := stack.HubHTTP()

	stack.AddMachine("barn", []uint16{22})
	stack.WaitAgentRegistered("barn", 5*time.Second)

	// Register the harness hub with the portal.
	status, _ := doJSON(t, http.MethodPost, portalBase+portal.HubDirectoryPath, map[string]string{
		"name":       "harness",
		"url":        hubURL,
		"adminToken": "test-admin-token",
	})
	if status != http.StatusOK {
		t.Fatalf("register harness hub: status %d", status)
	}

	// GET /api/fleet/agents
	status, body := doJSON(t, http.MethodGet, portalBase+"/api/fleet/agents", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/fleet/agents status = %d", status)
	}
	agents, _ := body["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("fleet agents = %d, want 1; body=%v", len(agents), body)
	}
	agent, _ := agents[0].(map[string]any)
	if id, _ := agent["id"].(string); id != "barn" {
		t.Errorf("agent id = %q, want %q", id, "barn")
	}
	if h, _ := agent["hub"].(string); h != "harness" {
		t.Errorf("agent hub tag = %q, want %q", h, "harness")
	}
	if hu, _ := agent["hubUrl"].(string); hu != hubURL {
		t.Errorf("agent hubUrl tag = %q, want %q", hu, hubURL)
	}
}

// hubsFromBody decodes the "hubs" field of a portal response into a
// typed slice. The doJSON helper returns map[string]any, so we
// re-marshal that one field into the strongly-typed slice for cleaner
// assertions in the tests above.
func hubsFromBody(t *testing.T, body map[string]any) []portal.HubVisibility {
	t.Helper()
	raw, ok := body["hubs"]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("re-marshal hubs: %v", err)
	}
	var out []portal.HubVisibility
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("unmarshal hubs: %v", err)
	}
	return out
}

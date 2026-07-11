package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// These tests exercise the hub's own outbound portal client in hub.go:
// registerWithPortal, discoverHubDirectory, syncViewerTokenToPortals, and
// bootstrapPortalsFromEnv. They are white-box (package hub) because all
// four functions are unexported and several read package-level globals
// (globalCfg, globalAuth, hubID), matching the convention in
// admin_api_test.go, protocol_version_test.go, and session_tokens_test.go.
//
// The portal counterpart is a hand-rolled httptest.NewServer stub
// (portalStub) rather than internal/portal's real Server + file store.
// internal/portal already has its own conformance suite; a stub gives
// full control over the exact edge-case responses (missing fields,
// malformed JSON, wrong status codes, unreachable endpoints) needed to
// reach the specific branches in the functions under test.
//
// hubID, globalCfg, and globalAuth are package-level mutable state, so
// these tests run sequentially (no t.Parallel) and call ResetForTesting
// via t.Cleanup so no test leaks state into the next.

// portalStub is a controllable stand-in for a Tela portal. It routes the
// three endpoints the hub's outbound client touches
// (GET /.well-known/tela, POST /api/hubs, PATCH /api/hubs/sync) and
// captures decoded request bodies so assertions can inspect exactly what
// the hub sent. Response behavior is set through the knob fields before a
// test drives the server; the knobs are written once (before any request)
// and only read from the handler goroutines afterward.
type portalStub struct {
	*httptest.Server

	mu        sync.Mutex
	posts     []map[string]any // decoded POST /api/hubs bodies, in call order
	patches   []map[string]any // decoded PATCH /api/hubs/sync bodies
	patchAuth []string         // Authorization header seen on each PATCH

	// well-known knobs
	wellKnownDirectory string   // "" omits hub_directory (triggers fallback)
	wellKnownStatus    int      // 0 -> 200
	wellKnownBody      []byte   // non-nil overrides the JSON encoding
	supportedVersions  []string // advertised supportedVersions, if any

	// POST /api/hubs knobs
	postStatus        int    // 0 -> 200
	postBody          []byte // response body for a non-2xx status
	postOmitSyncToken bool   // when true, success response omits syncToken

	// PATCH /api/hubs/sync knobs
	syncTokenExpected string // when set, PATCH must present Bearer <this> or gets 401
}

func newPortalStub() *portalStub {
	p := &portalStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/tela", p.handleWellKnown)
	mux.HandleFunc("/api/hubs/sync", p.handleSync)
	mux.HandleFunc("/api/hubs", p.handleHubs)
	p.Server = httptest.NewServer(mux)
	return p
}

func (p *portalStub) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	status := p.wellKnownStatus
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if p.wellKnownBody != nil {
		_, _ = w.Write(p.wellKnownBody)
		return
	}
	doc := map[string]any{"protocolVersion": "1.0"}
	if p.wellKnownDirectory != "" {
		doc["hub_directory"] = p.wellKnownDirectory
	}
	if len(p.supportedVersions) > 0 {
		doc["supportedVersions"] = p.supportedVersions
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func (p *portalStub) handleHubs(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	p.mu.Lock()
	p.posts = append(p.posts, body)
	n := len(p.posts)
	p.mu.Unlock()

	status := p.postStatus
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		if p.postBody != nil {
			_, _ = w.Write(p.postBody)
		}
		return
	}

	resp := map[string]any{"updated": n > 1}
	if !p.postOmitSyncToken {
		// A fresh token per call, so a re-registration (second call)
		// produces a token that differs from the first.
		resp["syncToken"] = fmt.Sprintf("hubsync_%d", n)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (p *portalStub) handleSync(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	p.mu.Lock()
	p.patches = append(p.patches, body)
	p.patchAuth = append(p.patchAuth, auth)
	p.mu.Unlock()

	if p.syncTokenExpected != "" && auth != "Bearer "+p.syncTokenExpected {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (p *portalStub) postCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.posts)
}

func (p *portalStub) patchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.patches)
}

// resetHubState clears all package-level mutable state after a test.
func resetHubState(t *testing.T) {
	t.Helper()
	t.Cleanup(ResetForTesting)
}

// ---- registerWithPortal ----

func TestRegisterWithPortal_FirstRegistration(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	// hubID is the package global the D2 fix now forwards in the payload.
	hubID = "hub-test-uuid"

	res, err := registerWithPortal(stub.URL, "admin-tok", "myhub", "https://myhub.example", "viewer-tok")
	if err != nil {
		t.Fatalf("registerWithPortal: %v", err)
	}

	if stub.postCount() != 1 {
		t.Fatalf("expected 1 POST to portal, got %d", stub.postCount())
	}
	post := stub.posts[0]
	if got := post["hubId"]; got != "hub-test-uuid" {
		t.Errorf("POST body hubId = %v, want %q (D2 fix)", got, "hub-test-uuid")
	}
	if got := post["name"]; got != "myhub" {
		t.Errorf("POST body name = %v, want myhub", got)
	}
	if got := post["url"]; got != "https://myhub.example" {
		t.Errorf("POST body url = %v, want https://myhub.example", got)
	}
	if got := post["viewerToken"]; got != "viewer-tok" {
		t.Errorf("POST body viewerToken = %v, want viewer-tok", got)
	}

	if res.Updated {
		t.Errorf("first registration should report Updated=false")
	}
	if res.Entry.SyncToken == "" {
		t.Errorf("expected a populated SyncToken, got empty")
	}
	if res.Entry.HubDirectory == "" {
		t.Errorf("expected a populated HubDirectory, got empty")
	}
}

func TestRegisterWithPortal_ReRegistrationRecovery(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	first, err := registerWithPortal(stub.URL, "admin-tok", "myhub", "https://myhub.example", "")
	if err != nil {
		t.Fatalf("first registerWithPortal: %v", err)
	}
	if first.Updated {
		t.Errorf("first registration should report Updated=false")
	}

	// Re-register per DESIGN-portal.md section 8 (hub lost its sync token
	// and recovers by registering again). The portal upserts and issues a
	// fresh token.
	second, err := registerWithPortal(stub.URL, "admin-tok", "myhub", "https://myhub.example", "")
	if err != nil {
		t.Fatalf("second registerWithPortal: %v", err)
	}
	if !second.Updated {
		t.Errorf("re-registration should report Updated=true")
	}
	if second.Entry.SyncToken == first.Entry.SyncToken {
		t.Errorf("re-registration should return a different sync token, both were %q", first.Entry.SyncToken)
	}
}

func TestRegisterWithPortal_MissingSyncToken(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.postOmitSyncToken = true

	_, err := registerWithPortal(stub.URL, "admin-tok", "myhub", "https://myhub.example", "")
	if err == nil {
		t.Fatalf("expected an error when the portal omits syncToken")
	}
	if !strings.Contains(err.Error(), "no syncToken; portal does not implement protocol 1.0") {
		t.Errorf("error = %q, want it to mention the missing-syncToken protocol failure", err)
	}
}

func TestRegisterWithPortal_Unauthorized(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.postStatus = http.StatusUnauthorized

	_, err := registerWithPortal(stub.URL, "bad-tok", "myhub", "https://myhub.example", "")
	if err == nil {
		t.Fatalf("expected an error on 401")
	}
	if !strings.Contains(err.Error(), "unauthorized. Check your API token") {
		t.Errorf("error = %q, want the unauthorized message", err)
	}
}

func TestRegisterWithPortal_NonSuccessPassthrough(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.postStatus = http.StatusInternalServerError
	stub.postBody = []byte("boom: portal exploded")

	_, err := registerWithPortal(stub.URL, "admin-tok", "myhub", "https://myhub.example", "")
	if err == nil {
		t.Fatalf("expected an error on 500")
	}
	if !strings.Contains(err.Error(), "portal returned HTTP 500") {
		t.Errorf("error = %q, want it to wrap 'portal returned HTTP 500'", err)
	}
	if !strings.Contains(err.Error(), "boom: portal exploded") {
		t.Errorf("error = %q, want it to echo the portal response body", err)
	}
}

func TestRegisterWithPortal_UnreachablePortal(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	url := stub.URL
	stub.Close() // no listener remains at url

	_, err := registerWithPortal(url, "admin-tok", "myhub", "https://myhub.example", "")
	if err == nil {
		t.Fatalf("expected an error when the portal is unreachable")
	}
	if !strings.Contains(err.Error(), "could not reach") {
		t.Errorf("error = %q, want it to wrap 'could not reach'", err)
	}
}

// ---- discoverHubDirectory ----

func TestDiscoverHubDirectory_CustomDirectory(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.wellKnownDirectory = "/custom/hubs"

	if got := discoverHubDirectory(stub.URL, ""); got != "/custom/hubs" {
		t.Errorf("discoverHubDirectory = %q, want /custom/hubs", got)
	}
}

func TestDiscoverHubDirectory_FallbackOn404(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.wellKnownStatus = http.StatusNotFound

	if got := discoverHubDirectory(stub.URL, ""); got != "/api/hubs" {
		t.Errorf("discoverHubDirectory = %q, want fallback /api/hubs", got)
	}
}

func TestDiscoverHubDirectory_FallbackOnUnreachable(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	url := stub.URL
	stub.Close()

	if got := discoverHubDirectory(url, ""); got != "/api/hubs" {
		t.Errorf("discoverHubDirectory = %q, want fallback /api/hubs", got)
	}
}

func TestDiscoverHubDirectory_FallbackOnMalformedJSON(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.wellKnownBody = []byte("{ this is not valid json")

	if got := discoverHubDirectory(stub.URL, ""); got != "/api/hubs" {
		t.Errorf("discoverHubDirectory = %q, want fallback /api/hubs", got)
	}
}

func TestDiscoverHubDirectory_ProceedsWhenNoMajor1(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.wellKnownDirectory = "/api/portal/hubs"
	stub.supportedVersions = []string{"2.0", "3.0"}

	// Per hub.go:3004-3006 the hub logs the mismatch but still returns the
	// discovered directory and attempts registration anyway.
	if got := discoverHubDirectory(stub.URL, ""); got != "/api/portal/hubs" {
		t.Errorf("discoverHubDirectory = %q, want /api/portal/hubs despite no 1.x version", got)
	}
}

// ---- syncViewerTokenToPortals ----

func TestSyncViewerTokenToPortals_Success(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()
	stub.syncTokenExpected = "sync-abc"

	restore := withTestConfig(t, &hubConfig{
		Name: "myhub",
		Portals: map[string]portalEntry{
			"p1": {URL: stub.URL, HubDirectory: "/api/hubs", SyncToken: "sync-abc"},
		},
		Auth: authConfig{
			Tokens: []tokenEntry{{ID: "console", Token: "viewer-tok", HubRole: "viewer"}},
		},
	})
	defer restore()

	syncViewerTokenToPortals()

	if stub.patchCount() != 1 {
		t.Fatalf("expected 1 PATCH to the portal, got %d", stub.patchCount())
	}
	if got := stub.patchAuth[0]; got != "Bearer sync-abc" {
		t.Errorf("PATCH Authorization = %q, want Bearer sync-abc", got)
	}
	patch := stub.patches[0]
	if got := patch["name"]; got != "myhub" {
		t.Errorf("PATCH body name = %v, want myhub", got)
	}
	if got := patch["viewerToken"]; got != "viewer-tok" {
		t.Errorf("PATCH body viewerToken = %v, want viewer-tok", got)
	}
}

func TestSyncViewerTokenToPortals_SkipsEmptySyncToken(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	restore := withTestConfig(t, &hubConfig{
		Name: "myhub",
		Portals: map[string]portalEntry{
			"p1": {URL: stub.URL, HubDirectory: "/api/hubs", SyncToken: ""},
		},
		Auth: authConfig{
			Tokens: []tokenEntry{{ID: "console", Token: "viewer-tok", HubRole: "viewer"}},
		},
	})
	defer restore()

	syncViewerTokenToPortals()

	if stub.patchCount() != 0 {
		t.Errorf("portal with empty sync token should be skipped, got %d PATCH calls", stub.patchCount())
	}
}

func TestSyncViewerTokenToPortals_ContinuesPastUnreachable(t *testing.T) {
	resetHubState(t)

	dead := newPortalStub()
	deadURL := dead.URL
	dead.Close() // unreachable

	live := newPortalStub()
	defer live.Close()
	live.syncTokenExpected = "sync-live"

	restore := withTestConfig(t, &hubConfig{
		Name: "myhub",
		Portals: map[string]portalEntry{
			"dead": {URL: deadURL, HubDirectory: "/api/hubs", SyncToken: "sync-dead"},
			"live": {URL: live.URL, HubDirectory: "/api/hubs", SyncToken: "sync-live"},
		},
		Auth: authConfig{
			Tokens: []tokenEntry{{ID: "console", Token: "viewer-tok", HubRole: "viewer"}},
		},
	})
	defer restore()

	syncViewerTokenToPortals()

	if live.patchCount() != 1 {
		t.Errorf("live portal should still receive its PATCH despite the dead portal, got %d", live.patchCount())
	}
}

func TestSyncViewerTokenToPortals_NoViewerToken(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	// Auth has no viewer-role identity, so consoleViewerToken() is empty
	// and the function must return before touching any portal.
	restore := withTestConfig(t, &hubConfig{
		Name: "myhub",
		Portals: map[string]portalEntry{
			"p1": {URL: stub.URL, HubDirectory: "/api/hubs", SyncToken: "sync-abc"},
		},
		Auth: authConfig{
			Tokens: []tokenEntry{{ID: "admin", Token: "admin-tok", HubRole: "admin"}},
		},
	})
	defer restore()

	syncViewerTokenToPortals()

	if stub.patchCount() != 0 {
		t.Errorf("no viewer token should mean zero PATCH calls, got %d", stub.patchCount())
	}
}

// ---- bootstrapPortalsFromEnv ----

func TestBootstrapPortalsFromEnv_Success(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	t.Setenv("TELAHUBD_PORTAL_URL", stub.URL)
	t.Setenv("TELAHUBD_PORTAL_TOKEN", "admin-tok")
	t.Setenv("TELAHUBD_PUBLIC_URL", "https://myhub.example")

	cfg := &hubConfig{Name: "myhub"}
	ok := bootstrapPortalsFromEnv(cfg, "")
	if !ok {
		t.Fatalf("expected bootstrap to occur")
	}
	if stub.postCount() != 1 {
		t.Fatalf("expected exactly 1 registration POST, got %d", stub.postCount())
	}
	entry, exists := cfg.Portals["default"]
	if !exists {
		t.Fatalf("expected cfg.Portals[default] to be populated")
	}
	if entry.SyncToken == "" {
		t.Errorf("expected the persisted entry to carry a sync token")
	}
	if entry.URL != stub.URL {
		t.Errorf("entry URL = %q, want %q", entry.URL, stub.URL)
	}
	if entry.Token != "" {
		t.Errorf("admin token must not be persisted in the entry, got %q", entry.Token)
	}
}

func TestBootstrapPortalsFromEnv_SkipsWhenPortalsExist(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	t.Setenv("TELAHUBD_PORTAL_URL", stub.URL)
	t.Setenv("TELAHUBD_PORTAL_TOKEN", "admin-tok")
	t.Setenv("TELAHUBD_PUBLIC_URL", "https://myhub.example")

	cfg := &hubConfig{
		Name:    "myhub",
		Portals: map[string]portalEntry{"existing": {URL: "https://other.example"}},
	}
	ok := bootstrapPortalsFromEnv(cfg, "")
	if ok {
		t.Errorf("bootstrap should not run when portals already exist")
	}
	if stub.postCount() != 0 {
		t.Errorf("expected zero HTTP calls, got %d", stub.postCount())
	}
}

func TestBootstrapPortalsFromEnv_SkipsWhenPublicURLMissing(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	t.Setenv("TELAHUBD_PORTAL_URL", stub.URL)
	t.Setenv("TELAHUBD_PORTAL_TOKEN", "admin-tok")
	t.Setenv("TELAHUBD_PUBLIC_URL", "")

	cfg := &hubConfig{Name: "myhub"}
	ok := bootstrapPortalsFromEnv(cfg, "")
	if ok {
		t.Errorf("bootstrap should not run without TELAHUBD_PUBLIC_URL")
	}
	if stub.postCount() != 0 {
		t.Errorf("expected zero HTTP calls, got %d", stub.postCount())
	}
}

func TestBootstrapPortalsFromEnv_SkipsWhenHubNameMissing(t *testing.T) {
	resetHubState(t)
	stub := newPortalStub()
	defer stub.Close()

	t.Setenv("TELAHUBD_PORTAL_URL", stub.URL)
	t.Setenv("TELAHUBD_PORTAL_TOKEN", "admin-tok")
	t.Setenv("TELAHUBD_PUBLIC_URL", "https://myhub.example")
	t.Setenv("TELAHUBD_NAME", "")

	cfg := &hubConfig{Name: ""}
	ok := bootstrapPortalsFromEnv(cfg, "")
	if ok {
		t.Errorf("bootstrap should not run without a hub name")
	}
	if stub.postCount() != 0 {
		t.Errorf("expected zero HTTP calls, got %d", stub.postCount())
	}
}

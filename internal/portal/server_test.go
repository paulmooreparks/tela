package portal_test

// server_test.go exercises the portal Server end-to-end via httptest.
// Tests use the file-backed store from internal/portal/store/file as
// their fixture; that store is exercised in its own package's tests
// for unit-level coverage. Here we drive it through the HTTP handler
// surface to verify the wire shape matches DESIGN-portal.md.
//
// Tests are in the portal_test package (not portal) so they go
// through the public API the same way an external caller would.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulmooreparks/tela/internal/portal"
	filestore "github.com/paulmooreparks/tela/internal/portal/store/file"
)

// ── Test fixtures ──────────────────────────────────────────────────

// newTestServer constructs a portal.Server backed by a fresh file
// store in a temp directory, mounts it on an httptest.Server, and
// returns the live URL plus the underlying file store (so tests can
// pre-populate hubs without going through the HTTP layer when they
// just want a fixture).
func newTestServer(t *testing.T) (baseURL string, store *filestore.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "portal.yaml")
	store, err := filestore.Open(path)
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}
	server := portal.NewServer(store)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer.URL, store
}

// doJSON performs an HTTP request with a JSON body, returns the
// status code and decoded JSON response. Tests use this instead of
// hand-rolling http.NewRequest + Do + io.ReadAll on every call.
func doJSON(t *testing.T, method, url string, reqBody any) (int, map[string]any) {
	t.Helper()
	var bodyReader io.Reader
	if reqBody != nil {
		buf, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.ContentLength != 0 {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp.StatusCode, out
}

// ── Discovery: /.well-known/tela ───────────────────────────────────

func TestWellKnown_GETReturnsExpectedShape(t *testing.T) {
	base, _ := newTestServer(t)
	status, body := doJSON(t, http.MethodGet, base+"/.well-known/tela", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["hub_directory"] != portal.HubDirectoryPath {
		t.Errorf("hub_directory = %v, want %q", body["hub_directory"], portal.HubDirectoryPath)
	}
	if body["protocolVersion"] != portal.ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %q", body["protocolVersion"], portal.ProtocolVersion)
	}
	supported, ok := body["supportedVersions"].([]any)
	if !ok || len(supported) == 0 {
		t.Errorf("supportedVersions missing or empty: %v", body["supportedVersions"])
	}
	// First entry should be the current ProtocolVersion (the package
	// only ships one version today; later versions go at the end).
	if supported[0] != portal.ProtocolVersion {
		t.Errorf("supportedVersions[0] = %v, want %q", supported[0], portal.ProtocolVersion)
	}
}

func TestWellKnown_HEADReturns200(t *testing.T) {
	base, _ := newTestServer(t)
	resp, err := http.Head(base + "/.well-known/tela")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("HEAD status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestWellKnown_RejectsPOST(t *testing.T) {
	base, _ := newTestServer(t)
	resp, err := http.Post(base+"/.well-known/tela", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", resp.StatusCode)
	}
}

// ── Hub directory: GET /api/hubs ───────────────────────────────────

func TestGetHubs_EmptyStore(t *testing.T) {
	base, _ := newTestServer(t)
	status, body := doJSON(t, http.MethodGet, base+"/api/hubs", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	hubs, ok := body["hubs"].([]any)
	if !ok {
		t.Fatalf("hubs field missing or wrong type: %v", body)
	}
	if len(hubs) != 0 {
		t.Errorf("expected empty hub list, got %d entries", len(hubs))
	}
}

func TestGetHubs_RedactsSecrets(t *testing.T) {
	base, store := newTestServer(t)
	// Pre-populate via the store directly so we can put real-looking
	// admin/viewer tokens in and verify they don't leak.
	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name:        "myhub",
		URL:         "https://hub.example.com",
		ViewerToken: "secret-viewer",
		AdminToken:  "secret-admin",
	}); err != nil {
		t.Fatal(err)
	}

	_, body := doJSON(t, http.MethodGet, base+"/api/hubs", nil)
	hubs := body["hubs"].([]any)
	if len(hubs) != 1 {
		t.Fatalf("expected 1 hub, got %d", len(hubs))
	}

	// Re-marshal the response and check for the substring of either
	// secret. They MUST NOT appear anywhere in the response body.
	raw, _ := json.Marshal(body)
	if bytes.Contains(raw, []byte("secret-viewer")) {
		t.Error("response leaked viewer token")
	}
	if bytes.Contains(raw, []byte("secret-admin")) {
		t.Error("response leaked admin token")
	}
}

// ── Hub directory: POST /api/hubs ──────────────────────────────────

func TestPostHubs_AddNewReturnsSyncToken(t *testing.T) {
	base, _ := newTestServer(t)
	status, body := doJSON(t, http.MethodPost, base+"/api/hubs", map[string]any{
		"name": "myhub",
		"url":  "https://hub.example.com",
	})
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}

	syncToken, _ := body["syncToken"].(string)
	if !portal.IsSyncTokenFormat(syncToken) {
		t.Errorf("syncToken = %q, expected hubsync_-prefixed token", syncToken)
	}
	if updated, _ := body["updated"].(bool); updated {
		t.Error("first POST should not set updated=true")
	}
	hubs := body["hubs"].([]any)
	if len(hubs) != 1 {
		t.Errorf("hubs after add should have 1 entry, got %d", len(hubs))
	}
}

func TestPostHubs_UpsertReturnsUpdated(t *testing.T) {
	base, _ := newTestServer(t)
	doJSON(t, http.MethodPost, base+"/api/hubs", map[string]any{
		"name": "myhub", "url": "https://a",
	})

	_, body := doJSON(t, http.MethodPost, base+"/api/hubs", map[string]any{
		"name": "myhub", "url": "https://b",
	})
	if updated, _ := body["updated"].(bool); !updated {
		t.Error("second POST should set updated=true")
	}
}

func TestPostHubs_RejectsMissingFields(t *testing.T) {
	base, _ := newTestServer(t)
	cases := []map[string]any{
		{"url": "https://x"},         // missing name
		{"name": "myhub"},            // missing URL
		{"name": "", "url": "https"}, // empty name
		{"name": "myhub", "url": ""}, // empty URL
	}
	for _, c := range cases {
		status, body := doJSON(t, http.MethodPost, base+"/api/hubs", c)
		if status != http.StatusBadRequest {
			t.Errorf("POST %v: status = %d, want 400", c, status)
		}
		if body["error"] == nil {
			t.Errorf("POST %v: response missing error field", c)
		}
	}
}

func TestPostHubs_RejectsInvalidJSON(t *testing.T) {
	base, _ := newTestServer(t)
	resp, err := http.Post(base+"/api/hubs", "application/json", strings.NewReader("not-json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// ── Hub directory: PATCH /api/hubs ─────────────────────────────────

func TestPatchHubs_PartialUpdate(t *testing.T) {
	base, _ := newTestServer(t)
	doJSON(t, http.MethodPost, base+"/api/hubs", map[string]any{
		"name": "myhub", "url": "https://old", "viewerToken": "old-vt",
	})

	status, body := doJSON(t, http.MethodPatch, base+"/api/hubs", map[string]any{
		"currentName": "myhub",
		"url":         "https://new",
	})
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	hubs := body["hubs"].([]any)
	hub := hubs[0].(map[string]any)
	if hub["url"] != "https://new" {
		t.Errorf("URL not updated: %v", hub["url"])
	}
}

func TestPatchHubs_RejectsMissingCurrentName(t *testing.T) {
	base, _ := newTestServer(t)
	status, _ := doJSON(t, http.MethodPatch, base+"/api/hubs", map[string]any{
		"name": "newname",
	})
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

func TestPatchHubs_404OnMissingHub(t *testing.T) {
	base, _ := newTestServer(t)
	url := "https://x"
	status, _ := doJSON(t, http.MethodPatch, base+"/api/hubs", map[string]any{
		"currentName": "ghost",
		"url":         url,
	})
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}

// ── Hub directory: DELETE /api/hubs ────────────────────────────────

func TestDeleteHubs_Existing(t *testing.T) {
	base, _ := newTestServer(t)
	doJSON(t, http.MethodPost, base+"/api/hubs", map[string]any{
		"name": "myhub", "url": "https://x",
	})

	status, body := doJSON(t, http.MethodDelete, base+"/api/hubs?name=myhub", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	hubs := body["hubs"].([]any)
	if len(hubs) != 0 {
		t.Errorf("hub list after delete should be empty, got %d", len(hubs))
	}
}

func TestDeleteHubs_RejectsMissingNameQuery(t *testing.T) {
	base, _ := newTestServer(t)
	status, _ := doJSON(t, http.MethodDelete, base+"/api/hubs", nil)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

func TestDeleteHubs_404OnMissing(t *testing.T) {
	base, _ := newTestServer(t)
	status, _ := doJSON(t, http.MethodDelete, base+"/api/hubs?name=ghost", nil)
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}

// ── Hub-driven sync: PATCH /api/hubs/sync ──────────────────────────

func TestHubSync_HappyPath(t *testing.T) {
	base, _ := newTestServer(t)
	_, body := doJSON(t, http.MethodPost, base+"/api/hubs", map[string]any{
		"name": "myhub", "url": "https://x", "viewerToken": "old",
	})
	syncToken := body["syncToken"].(string)

	req, _ := http.NewRequest(http.MethodPatch, base+"/api/hubs/sync", strings.NewReader(`{"name":"myhub","viewerToken":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+syncToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHubSync_RejectsWrongToken(t *testing.T) {
	base, _ := newTestServer(t)
	doJSON(t, http.MethodPost, base+"/api/hubs", map[string]any{
		"name": "myhub", "url": "https://x",
	})

	wrong, _ := portal.GenerateSyncToken()
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/hubs/sync", strings.NewReader(`{"name":"myhub","viewerToken":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+wrong)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHubSync_RejectsMissingBearer(t *testing.T) {
	base, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/hubs/sync", strings.NewReader(`{"name":"x","viewerToken":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHubSync_RejectsMissingFields(t *testing.T) {
	base, _ := newTestServer(t)
	tok, _ := portal.GenerateSyncToken()
	cases := []string{
		`{}`,
		`{"name":"myhub"}`,
		`{"viewerToken":"x"}`,
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodPatch, base+"/api/hubs/sync", strings.NewReader(c))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", c, resp.StatusCode)
		}
	}
}

// ── Authentication ─────────────────────────────────────────────────

func TestAuth_RejectsAnonymousWhenStoreRequiresAuth(t *testing.T) {
	base, store := newTestServer(t)
	if err := store.SetAdminToken("super-secret"); err != nil {
		t.Fatal(err)
	}

	// No Authorization header → 401 on every protected endpoint.
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(method, base+"/api/hubs", strings.NewReader("{}"))
		if method != http.MethodGet && method != http.MethodDelete {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s /api/hubs status = %d, want 401", method, resp.StatusCode)
		}
	}
}

func TestAuth_AcceptsValidBearer(t *testing.T) {
	base, store := newTestServer(t)
	if err := store.SetAdminToken("super-secret"); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/api/hubs", nil)
	req.Header.Set("Authorization", "Bearer super-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// ── Admin proxy: /api/hub-admin/{hubName}/{operation} ──────────────

// newProxyTestStack creates a portal pointing at a single mock
// upstream hub. The mock hub records the inbound proxied request and
// returns a configurable response. Returns the portal base URL, the
// store (so the test can pre-populate hubs), and a pointer to a
// hubRecorder the test can inspect after the call.
func newProxyTestStack(t *testing.T) (portalBase string, store *filestore.Store, recorder *hubRecorder) {
	t.Helper()
	recorder = &hubRecorder{
		respondStatus: 200,
		respondBody:   []byte(`{"ok":true}`),
	}
	hubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.method = r.Method
		recorder.path = r.URL.Path
		recorder.query = r.URL.RawQuery
		recorder.authHeader = r.Header.Get("Authorization")
		recorder.contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		recorder.body = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(recorder.respondStatus)
		w.Write(recorder.respondBody)
	}))
	t.Cleanup(hubServer.Close)

	portalBase, store = newTestServer(t)
	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name:       "myhub",
		URL:        hubServer.URL,
		AdminToken: "hub-admin-token",
	}); err != nil {
		t.Fatal(err)
	}
	return
}

// hubRecorder captures the inbound request the upstream mock hub
// received. Tests inspect its fields after the proxy call.
type hubRecorder struct {
	respondStatus int
	respondBody   []byte

	method      string
	path        string
	query       string
	authHeader  string
	contentType string
	body        []byte
}

func TestAdminProxy_PassesThroughRequestAndResponse(t *testing.T) {
	base, _, rec := newProxyTestStack(t)
	rec.respondBody = []byte(`{"hello":"world"}`)

	resp, err := http.Get(base + "/api/hub-admin/myhub/access?foo=bar")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("hello")) {
		t.Errorf("response body did not contain upstream payload: %q", body)
	}

	// Verify the upstream saw the right request.
	if rec.method != "GET" {
		t.Errorf("upstream method = %q, want GET", rec.method)
	}
	if rec.path != "/api/admin/access" {
		t.Errorf("upstream path = %q, want /api/admin/access", rec.path)
	}
	if rec.query != "foo=bar" {
		t.Errorf("upstream query = %q, want foo=bar", rec.query)
	}
	if rec.authHeader != "Bearer hub-admin-token" {
		t.Errorf("upstream Authorization = %q, want Bearer hub-admin-token", rec.authHeader)
	}
}

func TestAdminProxy_PreservesPATCHVerb(t *testing.T) {
	// This is the regression guard for the bug where the Awan Saya
	// proxy was collapsing PATCH into POST. PATCH /api/hub-admin/...
	// must arrive at the upstream as PATCH.
	base, _, rec := newProxyTestStack(t)

	req, _ := http.NewRequest(http.MethodPatch, base+"/api/hub-admin/myhub/update", strings.NewReader(`{"channel":"beta"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if rec.method != "PATCH" {
		t.Errorf("upstream method = %q, want PATCH", rec.method)
	}
	if rec.path != "/api/admin/update" {
		t.Errorf("upstream path = %q, want /api/admin/update", rec.path)
	}
	if !bytes.Contains(rec.body, []byte(`"channel":"beta"`)) {
		t.Errorf("upstream did not receive PATCH body: %q", rec.body)
	}
}

func TestAdminProxy_RejectsLegacyDoublePrefix(t *testing.T) {
	base, _, _ := newProxyTestStack(t)
	// The old shape /api/hub-admin/myhub/api/admin/access is forbidden
	// in protocol version 1.0. The proxy must refuse it instead of
	// silently dispatching to /api/admin/api/admin/access at the hub.
	resp, err := http.Get(base + "/api/hub-admin/myhub/api/admin/access")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("legacy double-prefix status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminProxy_404OnUnknownHub(t *testing.T) {
	base, _, _ := newProxyTestStack(t)
	resp, err := http.Get(base + "/api/hub-admin/ghost/access")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestAdminProxy_400WhenNoAdminTokenStored(t *testing.T) {
	base, store := newTestServer(t)
	// Add a hub with no admin token.
	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name: "tokenless",
		URL:  "https://hub.example.com",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(base + "/api/hub-admin/tokenless/access")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminProxy_502OnHubUnreachable(t *testing.T) {
	// Set up a portal pointing at a hub URL that doesn't resolve.
	base, store := newTestServer(t)
	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name:       "deadhub",
		URL:        "http://127.0.0.1:1", // port 1, nothing should listen
		AdminToken: "tok",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(base + "/api/hub-admin/deadhub/access")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestAdminProxy_PreservesUpstreamErrorStatus(t *testing.T) {
	base, _, rec := newProxyTestStack(t)
	rec.respondStatus = http.StatusForbidden
	rec.respondBody = []byte(`{"error":"forbidden by hub"}`)

	resp, err := http.Get(base + "/api/hub-admin/myhub/access")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (passthrough)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("forbidden by hub")) {
		t.Errorf("body did not pass through: %q", body)
	}
}

// ── Fleet aggregation: GET /api/fleet/agents ───────────────────────

// fakeStatusHub returns an httptest server that serves /api/status
// with the given machines list.
func fakeStatusHub(t *testing.T, machines []map[string]any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"machines": machines})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFleetAgents_AggregatesAcrossHubs(t *testing.T) {
	base, store := newTestServer(t)

	hub1 := fakeStatusHub(t, []map[string]any{
		{"id": "barn", "agentConnected": true, "services": []any{}},
	})
	hub2 := fakeStatusHub(t, []map[string]any{
		{"id": "web01", "agentConnected": false, "services": []any{}},
		{"id": "db01", "agentConnected": true, "services": []any{}},
	})

	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name: "alpha", URL: hub1, AdminToken: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name: "bravo", URL: hub2, AdminToken: "x",
	}); err != nil {
		t.Fatal(err)
	}

	status, body := doJSON(t, http.MethodGet, base+"/api/fleet/agents", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	agents := body["agents"].([]any)
	if len(agents) != 3 {
		t.Errorf("expected 3 agents across both hubs, got %d", len(agents))
	}

	// Each agent record must have hub and hubUrl tags.
	hubsSeen := map[string]bool{}
	for _, a := range agents {
		am := a.(map[string]any)
		hub, _ := am["hub"].(string)
		if hub == "" {
			t.Errorf("agent record missing hub tag: %v", am)
		}
		hubsSeen[hub] = true
		if _, ok := am["hubUrl"]; !ok {
			t.Errorf("agent record missing hubUrl: %v", am)
		}
	}
	if !hubsSeen["alpha"] || !hubsSeen["bravo"] {
		t.Errorf("expected agents tagged with both hub names, got %v", hubsSeen)
	}
}

func TestFleetAgents_EmptyOnNoHubs(t *testing.T) {
	base, _ := newTestServer(t)
	_, body := doJSON(t, http.MethodGet, base+"/api/fleet/agents", nil)
	agents, ok := body["agents"].([]any)
	if !ok {
		t.Fatalf("agents field missing or wrong type: %v", body)
	}
	if len(agents) != 0 {
		t.Errorf("expected empty agents list, got %d", len(agents))
	}
}

func TestFleetAgents_SkipsUnreachableHubs(t *testing.T) {
	base, store := newTestServer(t)
	good := fakeStatusHub(t, []map[string]any{
		{"id": "alive", "services": []any{}},
	})

	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name: "good", URL: good, AdminToken: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddHub(t.Context(), filestore.LocalUser, portal.Hub{
		Name: "dead", URL: "http://127.0.0.1:1", AdminToken: "x",
	}); err != nil {
		t.Fatal(err)
	}

	status, body := doJSON(t, http.MethodGet, base+"/api/fleet/agents", nil)
	if status != 200 {
		t.Errorf("status = %d, want 200 even with one hub unreachable", status)
	}
	agents := body["agents"].([]any)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent (from reachable hub only), got %d", len(agents))
	}
}

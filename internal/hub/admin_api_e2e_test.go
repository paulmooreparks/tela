// admin_api_e2e_test.go -- end-to-end coverage of the admin REST API
// over real net/http against a live in-process hub.
//
// This suite lives in package hub_test (an external test package) on
// purpose: internal/teststack imports internal/hub, so an in-package
// hub test file that imported teststack would form an import cycle. An
// external test package can import both internal/hub and
// internal/teststack without a cycle, which is exactly the pattern
// external test packages exist for. This file is also the internal/
// teststack adoption box tracked by GitHub #6: a teststack consumer
// other than teststack's own tests.
//
// The handler-level contract (status codes, If-Match/ETag preconditions,
// config mutations) is pinned cheaply and exhaustively in
// admin_api_test.go (package hub, httptest.ResponseRecorder). What this
// file adds is proof that the real mux registration, the real HTTP
// transport, and the header contract (ETag quoting, If-Match casing)
// all survive a round trip through an actual TCP listener.
package hub_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/paulmooreparks/tela/internal/hub"
	"github.com/paulmooreparks/tela/internal/teststack"
)

// newSeededStack spins up a teststack hub seeded with a sole owner
// identity plus one plain user, using the exported AuthConfig/TokenEntry
// aliases. Without those aliases an external package cannot construct a
// seeded hub.Config, and a zero-token hub runs in open mode where every
// admin endpoint 403s unconditionally, making the happy paths below
// unreachable. NewWithConfig registers stack.Close via t.Cleanup, so the
// stack is torn down automatically at the end of the test.
func newSeededStack(t *testing.T) *teststack.Stack {
	t.Helper()
	cfg := &hub.Config{
		Name: "e2e-hub",
		Auth: hub.AuthConfig{
			Tokens: []hub.TokenEntry{
				{ID: "owner", Token: "e2e-owner-tok", HubRole: "owner"},
				{ID: "alice", Token: "e2e-alice-tok"},
			},
		},
	}
	return teststack.NewWithConfig(t, cfg)
}

// adminReq issues a real HTTP request to the hub's admin API and returns
// the status code and the (already-read) body. token, ifMatch, and body
// are optional; pass "" to omit each.
func adminReq(t *testing.T, method, url, token, ifMatch, body string) (int, http.Header, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func TestAdminAccessE2E_OwnerListsSeededIdentity(t *testing.T) {
	stack := newSeededStack(t)

	status, _, body := adminReq(t, http.MethodGet, stack.HubHTTP()+"/api/admin/access", "e2e-owner-tok", "", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, string(body))
	}

	var resp struct {
		Access []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"access"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, string(body))
	}
	var foundOwner bool
	for _, a := range resp.Access {
		if a.ID == "owner" && a.Role == "owner" {
			foundOwner = true
		}
	}
	if !foundOwner {
		t.Errorf("seeded owner identity not present in access list: %+v", resp.Access)
	}
}

func TestAdminAccessE2E_Rejects403WithoutOwnerToken(t *testing.T) {
	stack := newSeededStack(t)

	// No Authorization header at all.
	if status, _, body := adminReq(t, http.MethodGet, stack.HubHTTP()+"/api/admin/access", "", "", ""); status != http.StatusForbidden {
		t.Errorf("no-auth status = %d, want 403; body=%s", status, string(body))
	}
	// A garbage bearer token.
	if status, _, body := adminReq(t, http.MethodGet, stack.HubHTTP()+"/api/admin/access", "not-a-real-token", "", ""); status != http.StatusForbidden {
		t.Errorf("garbage-token status = %d, want 403; body=%s", status, string(body))
	}
}

func TestAdminAccessE2E_MutatingRoundTripHonorsIfMatch(t *testing.T) {
	stack := newSeededStack(t)
	base := stack.HubHTTP() + "/api/admin/access/alice"

	// Read the current version off the ETag header over real HTTP.
	status, hdr, body := adminReq(t, http.MethodGet, base, "e2e-owner-tok", "", "")
	if status != http.StatusOK {
		t.Fatalf("GET alice status = %d, want 200; body=%s", status, string(body))
	}
	etag := hdr.Get("ETag")
	if etag == "" {
		t.Fatal("GET alice returned no ETag over real transport")
	}

	// PATCH with the fresh ETag as If-Match succeeds and proves the
	// quoted-ETag header contract survives the real transport.
	status, _, body = adminReq(t, http.MethodPatch, base, "e2e-owner-tok", etag, `{"role":"admin"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH with fresh If-Match status = %d, want 200; body=%s", status, string(body))
	}

	// The original ETag is now stale (the PATCH bumped the version); a
	// second PATCH with it must be refused with 412, confirming the
	// optimistic-concurrency contract holds end to end.
	status, _, body = adminReq(t, http.MethodPatch, base, "e2e-owner-tok", etag, `{"role":"viewer"}`)
	if status != http.StatusPreconditionFailed {
		t.Fatalf("PATCH with stale If-Match status = %d, want 412; body=%s", status, string(body))
	}
}

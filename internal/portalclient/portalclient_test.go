package portalclient_test

// portalclient_test.go runs the typed client wrappers against a real
// in-process portal backed by the file store. This is the smallest
// test that proves the client and server agree on URL paths, method
// verbs, JSON shapes, and bearer-token auth. Lower-level handler
// behavior is covered by internal/portal's own tests; this file is
// the round-trip layer.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/paulmooreparks/tela/internal/portal"
	"github.com/paulmooreparks/tela/internal/portal/store/file"
	"github.com/paulmooreparks/tela/internal/portalclient"
)

// newClientFixture spins up a fresh file-backed portal store, mints a
// bearer token, mounts portal.Server on an httptest.Server, and
// returns a configured client pointing at it.
func newClientFixture(t *testing.T) *portalclient.Client {
	t.Helper()

	fs, err := file.Open(filepath.Join(t.TempDir(), "portal.yaml"))
	if err != nil {
		t.Fatalf("file.Open: %v", err)
	}
	const bearer = "test-bearer-token"
	if err := fs.SetAdminToken(bearer); err != nil {
		t.Fatalf("SetAdminToken: %v", err)
	}
	srv := portal.NewServer(fs)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return portalclient.New(ts.URL, bearer)
}

func TestClient_Discover(t *testing.T) {
	c := newClientFixture(t)

	disc, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.HubDirectory != portal.HubDirectoryPath {
		t.Errorf("HubDirectory = %q, want %q", disc.HubDirectory, portal.HubDirectoryPath)
	}
	if disc.ProtocolVersion != portal.ProtocolVersion {
		t.Errorf("ProtocolVersion = %q, want %q", disc.ProtocolVersion, portal.ProtocolVersion)
	}
}

func TestClient_HubsCRUD(t *testing.T) {
	c := newClientFixture(t)
	ctx := context.Background()

	// Empty list at start.
	hubs, err := c.ListHubs(ctx)
	if err != nil {
		t.Fatalf("ListHubs: %v", err)
	}
	if len(hubs) != 0 {
		t.Fatalf("ListHubs initial = %d hubs, want 0", len(hubs))
	}

	// Add one.
	addResp, err := c.AddHub(ctx, portalclient.AddHubRequest{
		Name:        "myhub",
		HubID:       "test-hub-0000-0000-0000-000000000001",
		URL:         "http://hub.example:8080",
		ViewerToken: "viewer-tok",
		AdminToken:  "admin-tok",
	})
	if err != nil {
		t.Fatalf("AddHub: %v", err)
	}
	if len(addResp.Hubs) != 1 || addResp.Hubs[0].Name != "myhub" {
		t.Fatalf("AddHub Hubs = %+v, want one named myhub", addResp.Hubs)
	}

	// List again, expect one.
	hubs, err = c.ListHubs(ctx)
	if err != nil {
		t.Fatalf("ListHubs after add: %v", err)
	}
	if len(hubs) != 1 || hubs[0].Name != "myhub" {
		t.Fatalf("ListHubs after add = %+v", hubs)
	}

	// Patch the URL.
	newURL := "http://hub.example:9090"
	hubs, err = c.UpdateHub(ctx, portalclient.UpdateHubRequest{
		CurrentName: "myhub",
		URL:         &newURL,
	})
	if err != nil {
		t.Fatalf("UpdateHub: %v", err)
	}
	if len(hubs) != 1 || hubs[0].URL != newURL {
		t.Fatalf("UpdateHub result = %+v, want URL %q", hubs, newURL)
	}

	// Delete it.
	hubs, err = c.DeleteHub(ctx, "myhub")
	if err != nil {
		t.Fatalf("DeleteHub: %v", err)
	}
	if len(hubs) != 0 {
		t.Fatalf("DeleteHub result = %+v, want empty", hubs)
	}
}

// TestClient_DeviceAuthFlow exercises the device code state machine
// against a hand-written stub server. The stub returns
// authorization_pending on the first poll and a token on the second,
// matching the documented RFC 8628 state transition.
func TestClient_DeviceAuthFlow(t *testing.T) {
	var pollCount int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/oauth/device", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(portalclient.DeviceAuthorization{
			DeviceCode:      "dev-code-abc",
			UserCode:        "WDJB-MJHT",
			VerificationURI: "http://stub/device",
			ExpiresIn:       900,
			Interval:        5,
		})
	})
	mux.HandleFunc("POST /api/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := atomic.AddInt32(&pollCount, 1)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(portalclient.DeviceTokenResponse{
			AccessToken: "minted-access-token",
			TokenType:   "Bearer",
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c := portalclient.New(ts.URL, "")
	ctx := context.Background()

	auth, err := c.StartDeviceAuth(ctx)
	if err != nil {
		t.Fatalf("StartDeviceAuth: %v", err)
	}
	if auth.DeviceCode != "dev-code-abc" || auth.UserCode != "WDJB-MJHT" {
		t.Fatalf("StartDeviceAuth body = %+v", auth)
	}

	// First poll: pending.
	if _, err := c.PollDeviceToken(ctx, auth.DeviceCode); !errors.Is(err, portalclient.ErrAuthorizationPending) {
		t.Fatalf("first poll err = %v, want ErrAuthorizationPending", err)
	}

	// Second poll: token.
	tok, err := c.PollDeviceToken(ctx, auth.DeviceCode)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if tok.AccessToken != "minted-access-token" || tok.TokenType != "Bearer" {
		t.Fatalf("token = %+v", tok)
	}
}

// TestClient_DeviceAuthDenied checks that access_denied maps onto the
// sentinel error so callers can branch on errors.Is.
func TestClient_DeviceAuthDenied(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c := portalclient.New(ts.URL, "")
	if _, err := c.PollDeviceToken(context.Background(), "any"); !errors.Is(err, portalclient.ErrAccessDenied) {
		t.Fatalf("err = %v, want ErrAccessDenied", err)
	}
}

func TestClient_Unauthorized(t *testing.T) {
	c := newClientFixture(t)
	c.BearerToken = "wrong"

	_, err := c.ListHubs(context.Background())
	if err == nil {
		t.Fatal("ListHubs with wrong bearer: want error, got nil")
	}
	httpErr, ok := err.(*portalclient.HTTPError)
	if !ok {
		t.Fatalf("err type = %T, want *HTTPError: %v", err, err)
	}
	if httpErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", httpErr.StatusCode)
	}
}

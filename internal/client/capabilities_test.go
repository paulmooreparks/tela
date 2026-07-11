package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wellKnownHandler returns an http.HandlerFunc that serves the given
// body and status on GET /.well-known/tela. Any other path 404s so a
// misrouted request surfaces as a test failure rather than a false pass.
func wellKnownHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/tela" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestFetchHubCapabilities_CapabilityPresent(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200,
		`{"hub_directory":"/api/hubs","capabilities":["per-service-access-control"]}`))
	defer srv.Close()

	caps, err := fetchHubCapabilities(srv.URL, "")
	if err != nil {
		t.Fatalf("fetchHubCapabilities returned error: %v", err)
	}
	if !hubHasCapability(caps, "per-service-access-control") {
		t.Errorf("caps %v missing per-service-access-control", caps)
	}
}

// TestFetchHubCapabilities_Pre015Shape covers a hub that responds 200
// with a well-known body that omits the capabilities field entirely
// (the pre-0.15 shape). The call succeeds and returns an empty/nil list.
func TestFetchHubCapabilities_Pre015Shape(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200, `{"hub_directory":"/api/hubs"}`))
	defer srv.Close()

	caps, err := fetchHubCapabilities(srv.URL, "")
	if err != nil {
		t.Fatalf("fetchHubCapabilities returned error: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("caps = %v, want empty", caps)
	}
	if hubHasCapability(caps, "per-service-access-control") {
		t.Errorf("pre-0.15 hub should not advertise per-service-access-control")
	}
}

func TestFetchHubCapabilities_Unreachable(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200, `{"capabilities":[]}`))
	url := srv.URL
	srv.Close() // close before dialing so the connection is refused

	if _, err := fetchHubCapabilities(url, ""); err == nil {
		t.Fatal("fetchHubCapabilities against a closed server should error")
	}
}

func TestFetchHubCapabilities_NonOK(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(500, `{"capabilities":[]}`))
	defer srv.Close()

	if _, err := fetchHubCapabilities(srv.URL, ""); err == nil {
		t.Fatal("fetchHubCapabilities against a 500 response should error")
	}
}

func TestFetchHubCapabilities_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200, `{"capabilities": not-json`))
	defer srv.Close()

	if _, err := fetchHubCapabilities(srv.URL, ""); err == nil {
		t.Fatal("fetchHubCapabilities against a malformed body should error")
	}
}

// TestFetchHubCapabilities_WSScheme is the regression test for the
// scheme-conversion bug: cmdAdminAccessGrant always passes a ws:// or
// wss:// hub URL, and fetchHubCapabilities must route it through
// wsToHTTP before dialing. This test passes the ws:// form of an
// httptest.NewServer URL; it fails against the pre-fix code (which
// dialed the ws:// URL directly and got "unsupported protocol scheme")
// and passes after the fix.
func TestFetchHubCapabilities_WSScheme(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200,
		`{"capabilities":["per-service-access-control"]}`))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	caps, err := fetchHubCapabilities(wsURL, "")
	if err != nil {
		t.Fatalf("fetchHubCapabilities(%q) returned error: %v", wsURL, err)
	}
	if !hubHasCapability(caps, "per-service-access-control") {
		t.Errorf("caps %v missing per-service-access-control", caps)
	}
}

func TestHubHasCapability(t *testing.T) {
	cases := []struct {
		name string
		caps []string
		want bool
	}{
		{"present", []string{"a", "per-service-access-control", "b"}, true},
		{"absent", []string{"a", "b"}, false},
		{"empty", []string{}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hubHasCapability(tc.caps, "per-service-access-control"); got != tc.want {
				t.Errorf("hubHasCapability(%v) = %v, want %v", tc.caps, got, tc.want)
			}
		})
	}
}

func TestCheckServicesCapability_CapableHub(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200,
		`{"capabilities":["per-service-access-control"]}`))
	defer srv.Close()

	if err := checkServicesCapability(srv.URL, ""); err != nil {
		t.Fatalf("checkServicesCapability against a capable hub: %v", err)
	}
}

func TestCheckServicesCapability_IncapableHub(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200, `{"hub_directory":"/api/hubs"}`))
	defer srv.Close()

	err := checkServicesCapability(srv.URL, "")
	if err == nil {
		t.Fatal("checkServicesCapability against an incapable hub should error")
	}
	if !strings.Contains(err.Error(), "does not support per-service access control") {
		t.Errorf("error %q missing 'does not support per-service access control'", err)
	}
	if !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error %q does not name the hub URL %q", err, srv.URL)
	}
}

func TestCheckServicesCapability_UnreachableHub(t *testing.T) {
	srv := httptest.NewServer(wellKnownHandler(200, `{"capabilities":[]}`))
	url := srv.URL
	srv.Close()

	err := checkServicesCapability(url, "")
	if err == nil {
		t.Fatal("checkServicesCapability against an unreachable hub should error")
	}
	if !strings.Contains(err.Error(), "could not probe hub capabilities") {
		t.Errorf("error %q missing 'could not probe hub capabilities'", err)
	}
	// The unreachable-hub wording must be textually distinct from the
	// incapable-hub wording so operators can tell the two failures apart.
	if strings.Contains(err.Error(), "does not support per-service access control") {
		t.Errorf("unreachable-hub error %q should not use the incapable-hub wording", err)
	}
}

package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/paulmooreparks/tela/internal/certpin"
	"github.com/paulmooreparks/tela/internal/credstore"
)

// withTempCredstore points the credstore-related env vars at a temp
// dir for the duration of the test, so the test does not touch the
// developer's real credentials.
func withTempCredstore(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".tela"), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tela"), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
}

// startTLSEchoServer stands up a tiny TLS WebSocket server that
// accepts a single upgrade and immediately closes. Returns the wss
// URL plus the SHA-256 SPKI pin of its leaf certificate so tests can
// configure or compare pins against the actual server.
func startTLSEchoServer(t *testing.T) (wssURL, pin string, cleanup func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c.Close()
	}))
	pin = certpin.Capture(srv.Certificate())
	u, err := url.Parse(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("parse server URL: %v", err)
	}
	u.Scheme = "wss"
	return u.String(), pin, srv.Close
}

// TestPinAwareDialer_AcceptsMatchingPin verifies that when credstore
// has a pin recorded for a hub URL, and the hub presents a cert whose
// SPKI matches that pin, the WebSocket dial succeeds.
func TestPinAwareDialer_AcceptsMatchingPin(t *testing.T) {
	withTempCredstore(t)
	wssURL, pin, cleanup := startTLSEchoServer(t)
	defer cleanup()

	if err := credstore.SetPin(wssURL, pin); err != nil {
		t.Fatalf("SetPin: %v", err)
	}

	dialer := pinAwareDialer(wssURL)
	// Bypass CA chain validation so the test exercises the pin path,
	// not the chain validator (httptest's self-signed cert is not in
	// the system trust store).
	dialer.TLSClientConfig.InsecureSkipVerify = true
	conn, _, err := dialer.Dial(wssURL, nil)
	if err != nil {
		t.Fatalf("Dial with matching pin failed: %v", err)
	}
	conn.Close()
}

// TestPinAwareDialer_RefusesMismatchedPin verifies that when
// credstore has a pin that does NOT match the hub's certificate, the
// dial fails with an error wrapping certpin.ErrMismatch.
func TestPinAwareDialer_RefusesMismatchedPin(t *testing.T) {
	withTempCredstore(t)
	wssURL, _, cleanup := startTLSEchoServer(t)
	defer cleanup()

	wrongPin := "sha256:" + strings.Repeat("ab", 32)
	if err := credstore.SetPin(wssURL, wrongPin); err != nil {
		t.Fatalf("SetPin: %v", err)
	}

	dialer := pinAwareDialer(wssURL)
	dialer.TLSClientConfig.InsecureSkipVerify = true
	_, _, err := dialer.Dial(wssURL, nil)
	if err == nil {
		t.Fatal("Dial with mismatched pin should have failed")
	}
	if !errors.Is(err, certpin.ErrMismatch) {
		t.Errorf("Dial error %v does not wrap certpin.ErrMismatch", err)
	}
}

// TestPinAwareDialer_TOFUWithoutPin verifies that with no pin
// configured, the dial succeeds (TOFU mode). The fingerprint is
// logged at INFO level via the standard logger but the connection is
// not refused.
func TestPinAwareDialer_TOFUWithoutPin(t *testing.T) {
	withTempCredstore(t)
	wssURL, _, cleanup := startTLSEchoServer(t)
	defer cleanup()

	dialer := pinAwareDialer(wssURL)
	dialer.TLSClientConfig.InsecureSkipVerify = true
	conn, _, err := dialer.Dial(wssURL, nil)
	if err != nil {
		t.Fatalf("TOFU dial without pin should succeed: %v", err)
	}
	conn.Close()
}

// startTLSStatusServer stands up a TLS HTTP server that serves a
// 200 OK response on any GET. Returns the https URL and the leaf
// cert pin so HTTP-client tests can assert pin enforcement against
// real TLS handshakes.
func startTLSStatusServer(t *testing.T) (httpsURL, pin string, cleanup func()) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	pin = certpin.Capture(srv.Certificate())
	return srv.URL, pin, srv.Close
}

// TestPinAwareHTTPClient_AcceptsMatchingPin verifies that
// pinAwareHTTPClient honors the credstore pin and lets a matching
// HTTP request succeed.
func TestPinAwareHTTPClient_AcceptsMatchingPin(t *testing.T) {
	withTempCredstore(t)
	httpsURL, pin, cleanup := startTLSStatusServer(t)
	defer cleanup()

	if err := credstore.SetPin(httpsURL, pin); err != nil {
		t.Fatalf("SetPin: %v", err)
	}

	client := pinAwareHTTPClient(httpsURL, 5*time.Second)
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	resp, err := client.Get(httpsURL)
	if err != nil {
		t.Fatalf("Get with matching pin failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestPinAwareHTTPClient_RefusesMismatchedPin verifies that an HTTP
// request fails when the configured pin does not match the server's
// certificate, with the error wrapping certpin.ErrMismatch.
func TestPinAwareHTTPClient_RefusesMismatchedPin(t *testing.T) {
	withTempCredstore(t)
	httpsURL, _, cleanup := startTLSStatusServer(t)
	defer cleanup()

	wrongPin := "sha256:" + strings.Repeat("ab", 32)
	if err := credstore.SetPin(httpsURL, wrongPin); err != nil {
		t.Fatalf("SetPin: %v", err)
	}

	client := pinAwareHTTPClient(httpsURL, 5*time.Second)
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	_, err := client.Get(httpsURL)
	if err == nil {
		t.Fatal("Get with mismatched pin should have failed")
	}
	if !errors.Is(err, certpin.ErrMismatch) {
		t.Errorf("Get error %v does not wrap certpin.ErrMismatch", err)
	}
}

// TestPinAwareHTTPClient_TOFUWithoutPin verifies that with no pin
// configured, the HTTP request succeeds (TOFU mode).
func TestPinAwareHTTPClient_TOFUWithoutPin(t *testing.T) {
	withTempCredstore(t)
	httpsURL, _, cleanup := startTLSStatusServer(t)
	defer cleanup()

	client := pinAwareHTTPClient(httpsURL, 5*time.Second)
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	resp, err := client.Get(httpsURL)
	if err != nil {
		t.Fatalf("TOFU Get without pin should succeed: %v", err)
	}
	resp.Body.Close()
}

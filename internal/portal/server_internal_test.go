package portal

// server_internal_test.go covers internal helpers that are not
// reachable from the public package_test surface: the writeStoreError
// translation table, the bearerToken header parser, and a few other
// small helpers. These exist as a white-box supplement to the
// end-to-end tests in server_test.go.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteStoreError_TranslationTable drives every documented store
// error sentinel through writeStoreError and verifies the resulting
// HTTP status code matches DESIGN-portal.md section 7. Includes a
// fallthrough case for an unknown error to verify it maps to 500.
func TestWriteStoreError_TranslationTable(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"unauthorized", ErrUnauthorized, http.StatusUnauthorized},
		{"forbidden", ErrForbidden, http.StatusForbidden},
		{"hub not found", ErrHubNotFound, http.StatusNotFound},
		{"hub exists", ErrHubExists, http.StatusConflict},
		{"invalid input", ErrInvalidInput, http.StatusBadRequest},
		{"unknown error", errors.New("something exploded"), http.StatusInternalServerError},
		{"wrapped unauthorized", fmt.Errorf("wrap: %w", ErrUnauthorized), http.StatusUnauthorized},
	}
	server := &Server{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			server.writeStoreError(rec, c.err)
			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), `"error"`) {
				t.Errorf("response body missing error field: %s", rec.Body.String())
			}
		})
	}
}

// TestBearerToken_ParserCases covers the bearerToken header parser
// directly. The end-to-end tests already cover the happy path; this
// adds the malformed-input cases that don't need a live server.
func TestBearerToken_ParserCases(t *testing.T) {
	cases := []struct {
		name      string
		headerVal string
		wantToken string
		wantOK    bool
	}{
		{"missing header", "", "", false},
		{"wrong scheme basic", "Basic abc123", "", false},
		{"wrong scheme lowercase bearer", "bearer abc123", "", false},
		{"empty token after bearer", "Bearer ", "", false},
		{"valid bearer", "Bearer abc123", "abc123", true},
		{"valid with embedded spaces", "Bearer abc 123", "abc 123", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if c.headerVal != "" {
				r.Header.Set("Authorization", c.headerVal)
			}
			tok, ok := bearerToken(r)
			if tok != c.wantToken || ok != c.wantOK {
				t.Errorf("bearerToken = (%q, %v), want (%q, %v)", tok, ok, c.wantToken, c.wantOK)
			}
		})
	}
}

// TestDecodeJSONBody_RejectsUnknownFields verifies that the JSON
// decoder is configured to reject unknown fields. This catches
// client typos at the protocol layer instead of silently ignoring
// them, which is the standard pre-1.0 strictness rule.
func TestDecodeJSONBody_RejectsUnknownFields(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"x","unknown":"y"}`))
	var dst struct {
		Name string `json:"name"`
	}
	if err := decodeJSONBody(r, &dst); err == nil {
		t.Error("expected error for unknown field, got nil")
	}
}

func TestDecodeJSONBody_AcceptsValidBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"hello"}`))
	var dst struct {
		Name string `json:"name"`
	}
	if err := decodeJSONBody(r, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "hello" {
		t.Errorf("decoded Name = %q, want %q", dst.Name, "hello")
	}
}

func TestDecodeJSONBody_RejectsMissingBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Body = nil
	var dst struct{}
	if err := decodeJSONBody(r, &dst); err == nil {
		t.Error("expected error for nil body, got nil")
	}
}

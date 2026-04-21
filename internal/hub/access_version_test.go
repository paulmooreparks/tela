package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resetAccessVersions wipes the in-memory counter map. Tests that touch
// the counter call this first so they don't see state left behind by
// earlier tests.
func resetAccessVersions() {
	accessVersionMu.Lock()
	defer accessVersionMu.Unlock()
	accessVersions = map[string]uint64{}
}

func TestAccessVersion_EnsureInitializesAndHolds(t *testing.T) {
	resetAccessVersions()
	if got := ensureAccessVersion("alice"); got != 1 {
		t.Fatalf("first ensure = %d, want 1", got)
	}
	if got := ensureAccessVersion("alice"); got != 1 {
		t.Fatalf("second ensure must not bump, got %d", got)
	}
	if got := currentAccessVersion("alice"); got != 1 {
		t.Fatalf("current = %d, want 1", got)
	}
}

func TestAccessVersion_BumpIsMonotonic(t *testing.T) {
	resetAccessVersions()
	ensureAccessVersion("alice")
	if got := bumpAccessVersion("alice"); got != 2 {
		t.Fatalf("first bump = %d, want 2", got)
	}
	if got := bumpAccessVersion("alice"); got != 3 {
		t.Fatalf("second bump = %d, want 3", got)
	}
}

func TestAccessVersion_RenameCarriesCounterAndBumps(t *testing.T) {
	resetAccessVersions()
	ensureAccessVersion("alice")
	bumpAccessVersion("alice") // alice is now 2

	renameAccessVersion("alice", "alice2")

	if got := currentAccessVersion("alice"); got != 0 {
		t.Errorf("old id should be cleared, got %d", got)
	}
	// Rename is itself a mutation so the new id's version is the
	// previous +1 (3), not the raw carried value (2).
	if got := currentAccessVersion("alice2"); got != 3 {
		t.Errorf("new id version = %d, want 3 (carried + 1 for the rename)", got)
	}
}

func TestAccessVersion_DeleteClears(t *testing.T) {
	resetAccessVersions()
	ensureAccessVersion("alice")
	bumpAccessVersion("alice")

	deleteAccessVersion("alice")

	if got := currentAccessVersion("alice"); got != 0 {
		t.Errorf("deleted id must report 0, got %d", got)
	}
}

func TestParseIfMatch_AcceptedForms(t *testing.T) {
	cases := []struct {
		header string
		want   uint64
		ok     bool
	}{
		{`"42"`, 42, true},
		{`W/"17"`, 17, true},
		{`7`, 7, true},
		{``, 0, false},
		{`*`, 0, false},
		{`"garbage"`, 0, false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(""))
		if tc.header != "" {
			req.Header.Set("If-Match", tc.header)
		}
		got, ok := parseIfMatch(req)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseIfMatch(%q) = (%d, %v), want (%d, %v)", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSetETag_EmitsQuotedStrongForm(t *testing.T) {
	w := httptest.NewRecorder()
	setETag(w, 42)
	if got := w.Header().Get("ETag"); got != `"42"` {
		t.Errorf("ETag = %q, want \"42\"", got)
	}
}

func TestSetETag_SkipsZero(t *testing.T) {
	w := httptest.NewRecorder()
	setETag(w, 0)
	if got := w.Header().Get("ETag"); got != "" {
		t.Errorf("ETag for zero should be empty, got %q", got)
	}
}

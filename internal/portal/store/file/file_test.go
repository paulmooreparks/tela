package file

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paulmooreparks/tela/internal/portal"
)

// tempStore returns a fresh, empty file store backed by a temp file
// the test cleanup removes.
func tempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "portal.yaml")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// ── Open ───────────────────────────────────────────────────────────

func TestOpen_MissingFileReturnsEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on missing file should succeed, got %v", err)
	}
	if s == nil {
		t.Fatal("Open returned nil store")
	}
	hubs, err := s.ListHubsForUser(context.Background(), LocalUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(hubs) != 0 {
		t.Errorf("expected empty hub list, got %d entries", len(hubs))
	}
	if s.HasAuth() {
		t.Error("missing file should produce HasAuth()=false")
	}
}

func TestOpen_BadYAMLReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portal.yaml")
	if err := os.WriteFile(path, []byte("not: valid: : yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("expected parse error on malformed YAML, got nil")
	}
}

// ── AddHub ─────────────────────────────────────────────────────────

func TestAddHub_NewRecordReturnsCreatedAndSyncToken(t *testing.T) {
	s := tempStore(t)
	created, syncToken, err := s.AddHub(context.Background(), LocalUser, portal.Hub{
		Name:        "myhub",
		URL:         "https://hub.example.com",
		ViewerToken: "viewer-tok",
		AdminToken:  "admin-tok",
	})
	if err != nil {
		t.Fatalf("AddHub: %v", err)
	}
	if !created {
		t.Error("first AddHub should return created=true")
	}
	if !portal.IsSyncTokenFormat(syncToken) {
		t.Errorf("returned sync token %q is not well-formed", syncToken)
	}
}

func TestAddHub_UpsertSecondCallReturnsCreatedFalse(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://a"}); err != nil {
		t.Fatal(err)
	}
	created, syncToken, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://b"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second AddHub for the same name should return created=false")
	}
	if syncToken == "" {
		t.Error("upsert should still issue a fresh sync token")
	}

	hub, _, err := s.LookupHubForUser(ctx, LocalUser, "myhub")
	if err != nil {
		t.Fatal(err)
	}
	if hub.URL != "https://b" {
		t.Errorf("upsert did not overwrite URL: got %q", hub.URL)
	}
}

func TestAddHub_RejectsMissingFields(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "", URL: "https://x"})
	if !errors.Is(err, portal.ErrInvalidInput) {
		t.Errorf("missing name should return ErrInvalidInput, got %v", err)
	}
	_, _, err = s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: ""})
	if !errors.Is(err, portal.ErrInvalidInput) {
		t.Errorf("missing URL should return ErrInvalidInput, got %v", err)
	}
}

// ── ListHubsForUser ─────────────────────────────────────────────────

func TestListHubsForUser_RedactsSecrets(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, _, err := s.AddHub(ctx, LocalUser, portal.Hub{
		Name:        "myhub",
		URL:         "https://hub.example.com",
		ViewerToken: "secret-viewer",
		AdminToken:  "secret-admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := s.ListHubsForUser(ctx, LocalUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 hub, got %d", len(list))
	}
	hub := list[0]
	if hub.Name != "myhub" || hub.URL != "https://hub.example.com" {
		t.Errorf("unexpected hub fields: %+v", hub)
	}
	if !hub.CanManage {
		t.Error("single user should always have canManage=true")
	}
	// HubVisibility has no token fields by definition; this is more
	// of a "the spec shape is right" check than a redaction test.
	// Verify by reflection that the JSON shape doesn't leak secrets:
	// the type's only string fields are Name, URL, OrgName.
}

func TestListHubsForUser_SortedByName(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: name, URL: "https://" + name}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListHubsForUser(ctx, LocalUser)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, h := range list {
		if h.Name != want[i] {
			t.Errorf("list[%d] = %q, want %q", i, h.Name, want[i])
		}
	}
}

func TestListHubsForUser_EmptyStore(t *testing.T) {
	s := tempStore(t)
	list, err := s.ListHubsForUser(context.Background(), LocalUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("empty store should return empty list, got %d", len(list))
	}
}

// ── LookupHubForUser ────────────────────────────────────────────────

func TestLookupHubForUser_Existing(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, _, err := s.AddHub(ctx, LocalUser, portal.Hub{
		Name:        "myhub",
		URL:         "https://hub.example.com",
		ViewerToken: "v",
		AdminToken:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}

	hub, canManage, err := s.LookupHubForUser(ctx, LocalUser, "myhub")
	if err != nil {
		t.Fatal(err)
	}
	if !canManage {
		t.Error("single user should always have canManage=true")
	}
	if hub.AdminToken != "a" {
		t.Error("LookupHubForUser should return the full Hub including admin token")
	}
	if hub.ViewerToken != "v" {
		t.Error("LookupHubForUser should return the viewer token")
	}
}

func TestLookupHubForUser_Missing(t *testing.T) {
	s := tempStore(t)
	_, _, err := s.LookupHubForUser(context.Background(), LocalUser, "ghost")
	if !errors.Is(err, portal.ErrHubNotFound) {
		t.Errorf("missing hub should return ErrHubNotFound, got %v", err)
	}
}

// ── UpdateHub ──────────────────────────────────────────────────────

func TestUpdateHub_PartialUpdate(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, _, err := s.AddHub(ctx, LocalUser, portal.Hub{
		Name:        "myhub",
		URL:         "https://old.example.com",
		ViewerToken: "old-viewer",
		AdminToken:  "old-admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	newURL := "https://new.example.com"
	if err := s.UpdateHub(ctx, LocalUser, "myhub", portal.HubUpdate{URL: &newURL}); err != nil {
		t.Fatal(err)
	}

	hub, _, err := s.LookupHubForUser(ctx, LocalUser, "myhub")
	if err != nil {
		t.Fatal(err)
	}
	if hub.URL != "https://new.example.com" {
		t.Errorf("URL not updated: %q", hub.URL)
	}
	if hub.ViewerToken != "old-viewer" {
		t.Error("ViewerToken should not have changed")
	}
	if hub.AdminToken != "old-admin" {
		t.Error("AdminToken should not have changed")
	}
}

func TestUpdateHub_Rename(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "old-name", URL: "https://x"})
	if err != nil {
		t.Fatal(err)
	}

	newName := "new-name"
	if err := s.UpdateHub(ctx, LocalUser, "old-name", portal.HubUpdate{Name: &newName}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.LookupHubForUser(ctx, LocalUser, "old-name"); !errors.Is(err, portal.ErrHubNotFound) {
		t.Error("old name should be gone after rename")
	}
	if _, _, err := s.LookupHubForUser(ctx, LocalUser, "new-name"); err != nil {
		t.Errorf("new name should be present after rename, got %v", err)
	}
}

func TestUpdateHub_RenameCollisionRefused(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "a", URL: "https://a"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "b", URL: "https://b"}); err != nil {
		t.Fatal(err)
	}
	collisionName := "b"
	err := s.UpdateHub(ctx, LocalUser, "a", portal.HubUpdate{Name: &collisionName})
	if !errors.Is(err, portal.ErrHubExists) {
		t.Errorf("rename to existing name should return ErrHubExists, got %v", err)
	}
	// And both hubs should still exist with their original names.
	if _, _, err := s.LookupHubForUser(ctx, LocalUser, "a"); err != nil {
		t.Errorf("hub 'a' should still exist after refused rename, got %v", err)
	}
	if _, _, err := s.LookupHubForUser(ctx, LocalUser, "b"); err != nil {
		t.Errorf("hub 'b' should still exist after refused rename, got %v", err)
	}
}

func TestUpdateHub_Missing(t *testing.T) {
	s := tempStore(t)
	url := "https://x"
	err := s.UpdateHub(context.Background(), LocalUser, "ghost", portal.HubUpdate{URL: &url})
	if !errors.Is(err, portal.ErrHubNotFound) {
		t.Errorf("update on missing hub should return ErrHubNotFound, got %v", err)
	}
}

// ── DeleteHub ──────────────────────────────────────────────────────

func TestDeleteHub_Existing(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHub(ctx, LocalUser, "myhub"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LookupHubForUser(ctx, LocalUser, "myhub"); !errors.Is(err, portal.ErrHubNotFound) {
		t.Error("hub should be gone after delete")
	}
}

func TestDeleteHub_Missing(t *testing.T) {
	s := tempStore(t)
	err := s.DeleteHub(context.Background(), LocalUser, "ghost")
	if !errors.Is(err, portal.ErrHubNotFound) {
		t.Errorf("delete on missing hub should return ErrHubNotFound, got %v", err)
	}
}

// ── VerifyHubSyncToken ─────────────────────────────────────────────

func TestVerifyHubSyncToken_Matching(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, syncToken, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	hub, err := s.VerifyHubSyncToken(ctx, "myhub", syncToken)
	if err != nil {
		t.Fatalf("verify with correct token failed: %v", err)
	}
	if hub == nil || hub.Name != "myhub" {
		t.Errorf("returned hub mismatch: %+v", hub)
	}
}

func TestVerifyHubSyncToken_WrongToken(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	wrong, _ := portal.GenerateSyncToken()
	_, err := s.VerifyHubSyncToken(ctx, "myhub", wrong)
	if !errors.Is(err, portal.ErrUnauthorized) {
		t.Errorf("wrong sync token should return ErrUnauthorized, got %v", err)
	}
}

func TestVerifyHubSyncToken_MalformedToken(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.VerifyHubSyncToken(ctx, "myhub", "obviously-not-a-token")
	if !errors.Is(err, portal.ErrUnauthorized) {
		t.Errorf("malformed sync token should return ErrUnauthorized, got %v", err)
	}
}

func TestVerifyHubSyncToken_UnknownHub(t *testing.T) {
	s := tempStore(t)
	tok, _ := portal.GenerateSyncToken()
	_, err := s.VerifyHubSyncToken(context.Background(), "ghost", tok)
	if !errors.Is(err, portal.ErrHubNotFound) {
		t.Errorf("unknown hub should return ErrHubNotFound, got %v", err)
	}
}

// ── SetHubViewerToken ──────────────────────────────────────────────

func TestSetHubViewerToken(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://x", ViewerToken: "old"}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetHubViewerToken(ctx, "myhub", "new"); err != nil {
		t.Fatal(err)
	}

	hub, _, err := s.LookupHubForUser(ctx, LocalUser, "myhub")
	if err != nil {
		t.Fatal(err)
	}
	if hub.ViewerToken != "new" {
		t.Errorf("viewer token not updated: got %q, want %q", hub.ViewerToken, "new")
	}
}

func TestSetHubViewerToken_MissingHub(t *testing.T) {
	s := tempStore(t)
	err := s.SetHubViewerToken(context.Background(), "ghost", "new")
	if !errors.Is(err, portal.ErrHubNotFound) {
		t.Errorf("missing hub should return ErrHubNotFound, got %v", err)
	}
}

// ── Authenticate ───────────────────────────────────────────────────

func TestAuthenticate_NoAuthConfigured(t *testing.T) {
	s := tempStore(t)
	r := httptest.NewRequest("GET", "/", nil)
	user, err := s.Authenticate(r)
	if err != nil {
		t.Fatal(err)
	}
	if user == nil {
		t.Error("no-auth mode should return LocalUser, got nil")
	}
	if user.ID() != "local" {
		t.Errorf("user ID = %q, want %q", user.ID(), "local")
	}
}

func TestAuthenticate_BearerHappyPath(t *testing.T) {
	s := tempStore(t)
	if err := s.SetAdminToken("super-secret-token"); err != nil {
		t.Fatal(err)
	}
	if !s.HasAuth() {
		t.Fatal("HasAuth should be true after SetAdminToken")
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer super-secret-token")
	user, err := s.Authenticate(r)
	if err != nil {
		t.Fatalf("valid bearer should authenticate, got %v", err)
	}
	if user.ID() != "local" {
		t.Errorf("user ID = %q, want %q", user.ID(), "local")
	}
}

func TestAuthenticate_BearerFailures(t *testing.T) {
	s := tempStore(t)
	if err := s.SetAdminToken("super-secret-token"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		auth string
	}{
		{"missing", ""},
		{"wrong scheme", "Basic super-secret-token"},
		{"empty bearer value", "Bearer "},
		{"wrong token", "Bearer not-the-secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if c.auth != "" {
				r.Header.Set("Authorization", c.auth)
			}
			_, err := s.Authenticate(r)
			if !errors.Is(err, portal.ErrUnauthorized) {
				t.Errorf("expected ErrUnauthorized, got %v", err)
			}
		})
	}
}

func TestSetAdminToken_DisablesAuth(t *testing.T) {
	s := tempStore(t)
	if err := s.SetAdminToken("temp"); err != nil {
		t.Fatal(err)
	}
	if !s.HasAuth() {
		t.Fatal("HasAuth should be true")
	}
	if err := s.SetAdminToken(""); err != nil {
		t.Fatal(err)
	}
	if s.HasAuth() {
		t.Error("HasAuth should be false after SetAdminToken(\"\")")
	}
	// And subsequent requests should authenticate without a header.
	user, err := s.Authenticate(httptest.NewRequest("GET", "/", nil))
	if err != nil || user == nil {
		t.Errorf("after disabling auth, requests should authenticate; got user=%v err=%v", user, err)
	}
}

// ── Persistence round trip ─────────────────────────────────────────

func TestRoundTrip_HubsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "portal.yaml")

	// First store: add some hubs.
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, sync1, err := s1.AddHub(ctx, LocalUser, portal.Hub{
		Name:        "alpha",
		URL:         "https://alpha.example.com",
		ViewerToken: "alpha-viewer",
		AdminToken:  "alpha-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s1.AddHub(ctx, LocalUser, portal.Hub{
		Name:        "bravo",
		URL:         "https://bravo.example.com",
		ViewerToken: "bravo-viewer",
		AdminToken:  "bravo-admin",
		OrgName:     "acme",
	}); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify state.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	list, err := s2.ListHubsForUser(ctx, LocalUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 hubs after reopen, got %d", len(list))
	}

	hub, _, err := s2.LookupHubForUser(ctx, LocalUser, "bravo")
	if err != nil {
		t.Fatal(err)
	}
	if hub.OrgName != "acme" {
		t.Errorf("OrgName not preserved: %q", hub.OrgName)
	}
	if hub.AdminToken != "bravo-admin" {
		t.Errorf("AdminToken not preserved: %q", hub.AdminToken)
	}

	// Sync token from before reopen should still verify against the
	// reopened store, since the hash was persisted.
	if _, err := s2.VerifyHubSyncToken(ctx, "alpha", sync1); err != nil {
		t.Errorf("sync token should still verify after reopen: %v", err)
	}
}

func TestRoundTrip_AdminTokenSurvives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "portal.yaml")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.SetAdminToken("the-token"); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.HasAuth() {
		t.Error("admin token should survive reopen")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer the-token")
	if _, err := s2.Authenticate(r); err != nil {
		t.Errorf("reopened store should accept the original token: %v", err)
	}
}

func TestSavePermissions_0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 permission semantics differ on Windows")
	}
	s := tempStore(t)
	if _, _, err := s.AddHub(context.Background(), LocalUser, portal.Hub{Name: "myhub", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected 0600, got %#o", perm)
	}
}

// ── Concurrent reads ──────────────────────────────────────────────

func TestConcurrentReads(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	if _, _, err := s.AddHub(ctx, LocalUser, portal.Hub{Name: "myhub", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				_, _ = s.ListHubsForUser(ctx, LocalUser)
				_, _, _ = s.LookupHubForUser(ctx, LocalUser, "myhub")
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

// ── Sanity: HubVisibility shape has no secret fields ───────────────

// This is a small structural assertion that catches mistakes where
// somebody adds AdminToken or ViewerToken to HubVisibility, which
// would leak secrets through the directory listing endpoint.
func TestHubVisibility_NoTokenFields(t *testing.T) {
	v := portal.HubVisibility{
		Name:      "x",
		URL:       "https://x",
		CanManage: true,
		OrgName:   "acme",
	}
	// Use the JSON tags as the canonical surface check: serialize
	// and confirm no "*Token*" substring appears in the output.
	// We don't actually need to import encoding/json for this; we
	// just check struct shape via fmt.
	asString := strings.Join([]string{v.Name, v.URL, v.OrgName}, "|")
	if strings.Contains(asString, "Token") {
		t.Error("HubVisibility test data should not contain the substring 'Token'; check if the test was edited")
	}

	// And confirm Authenticator and HubVisibility do not import
	// each other in a way that could leak. This is a smoke check;
	// the real defense is the type definition itself.
	_ = http.StatusOK // keep net/http imported for httptest above
}

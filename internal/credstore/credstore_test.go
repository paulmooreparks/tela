package credstore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ── NormalizeHubURL ────────────────────────────────────────────────

func TestNormalizeHubURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"wss://hub.example.com", "wss://hub.example.com"},
		{"wss://hub.example.com/", "wss://hub.example.com"},
		{"wss://hub.example.com///", "wss://hub.example.com"},
		{"WSS://hub.example.com", "wss://hub.example.com"},
		{"HTTPS://hub.example.com:443", "https://hub.example.com:443"},
		{"hub.example.com", "hub.example.com"}, // no scheme is left alone
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeHubURL(c.in); got != c.want {
			t.Errorf("NormalizeHubURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── Load / Save round trip ─────────────────────────────────────────

func tempStorePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "credentials.yaml")
}

func TestLoad_MissingFileReturnsEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	store, err := Load(path)
	if err != nil {
		t.Fatalf("Load on missing file should succeed, got %v", err)
	}
	if store == nil {
		t.Fatal("Load returned nil store")
	}
	if store.Hubs == nil {
		t.Error("Hubs map should be initialized, not nil")
	}
	if len(store.Hubs) != 0 {
		t.Errorf("expected empty Hubs, got %d entries", len(store.Hubs))
	}
}

func TestLoad_BadYAMLReturnsError(t *testing.T) {
	path := tempStorePath(t)
	if err := os.WriteFile(path, []byte("not: valid: yaml: : :"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected parse error on malformed YAML, got nil")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := tempStorePath(t)

	original := &Store{
		Hubs: map[string]Credential{
			"wss://hub.example.com":   {Token: "tok-1", Identity: "alice"},
			"wss://other.example.com": {Token: "tok-2"},
		},
		Update: UpdateConfig{
			Channel: "beta",
			Sources: map[string]string{
				"local": "https://example.com/channels/",
			},
		},
	}
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Hubs) != 2 {
		t.Fatalf("expected 2 hubs, got %d", len(loaded.Hubs))
	}
	if loaded.Hubs["wss://hub.example.com"].Token != "tok-1" {
		t.Errorf("hub.example.com token roundtrip mismatch: %+v", loaded.Hubs["wss://hub.example.com"])
	}
	if loaded.Hubs["wss://hub.example.com"].Identity != "alice" {
		t.Errorf("hub.example.com identity roundtrip mismatch: %+v", loaded.Hubs["wss://hub.example.com"])
	}
	if loaded.Update.Channel != "beta" {
		t.Errorf("Update.Channel roundtrip mismatch: %q", loaded.Update.Channel)
	}
	if loaded.Update.Sources["local"] != "https://example.com/channels/" {
		t.Errorf("Update.Sources[local] roundtrip mismatch: %q", loaded.Update.Sources["local"])
	}
}

// TestLoadIgnoresUnknownLegacyManifestBase verifies that a pre-0.12
// credstore on disk with an update.manifestBase field still loads cleanly
// post-#59. yaml.v3 silently drops unknown fields, so the operator gets a
// store with the channel preserved but no source URL; the next manifest
// fetch errors visibly and points at `tela channel sources set`.
func TestLoadIgnoresUnknownLegacyManifestBase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yaml")

	legacy := []byte(`hubs: {}
update:
  channel: local
  manifestBase: https://parkscomputing.com/content/tela/channels/
`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatalf("write legacy yaml: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load should accept unknown manifestBase silently: %v", err)
	}
	if loaded.Update.Channel != "local" {
		t.Errorf("channel should be preserved, got %q", loaded.Update.Channel)
	}
	if len(loaded.Update.Sources) != 0 {
		t.Errorf("Sources should be empty post-migration-removal, got %v", loaded.Update.Sources)
	}
}

func TestSaveCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "deep", "deeper", "credentials.yaml")
	store := &Store{Hubs: map[string]Credential{"wss://x": {Token: "t"}}}
	if err := store.Save(nested); err != nil {
		t.Fatalf("Save with nested parent: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("expected file at %s, got %v", nested, err)
	}
}

func TestSavePermissions0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 permission semantics differ on Windows")
	}
	path := tempStorePath(t)
	store := &Store{Hubs: map[string]Credential{"wss://x": {Token: "t"}}}
	if err := store.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected 0600, got %#o", perm)
	}
}

// ── Get / Set / Remove ─────────────────────────────────────────────

func TestGetOnNilHubs(t *testing.T) {
	s := &Store{}
	if _, ok := s.Get("wss://hub.example.com"); ok {
		t.Error("Get on nil Hubs should return ok=false")
	}
}

func TestSetInitializesMap(t *testing.T) {
	s := &Store{}
	s.Set("wss://hub.example.com", Credential{Token: "t"})
	if s.Hubs == nil {
		t.Fatal("Set should initialize Hubs map")
	}
	if got, ok := s.Get("wss://hub.example.com"); !ok || got.Token != "t" {
		t.Errorf("expected to round-trip cred, got ok=%v cred=%+v", ok, got)
	}
}

func TestNormalizationOnSetAndGet(t *testing.T) {
	s := &Store{}
	s.Set("WSS://Hub.Example.Com/", Credential{Token: "t"})
	// Hub portion is NOT lowercased (only scheme), but trailing slash IS
	// stripped and scheme IS lowercased.
	if _, ok := s.Get("wss://Hub.Example.Com"); !ok {
		t.Error("Get with normalized form should find the entry")
	}
	if _, ok := s.Get("WSS://Hub.Example.Com/"); !ok {
		t.Error("Get with original form should also find the entry")
	}
}

func TestRemove(t *testing.T) {
	s := &Store{}
	s.Set("wss://hub.example.com", Credential{Token: "t"})

	if !s.Remove("WSS://hub.example.com/") {
		t.Error("Remove should report true on present entry")
	}
	if _, ok := s.Get("wss://hub.example.com"); ok {
		t.Error("entry should be gone after Remove")
	}
	if s.Remove("wss://hub.example.com") {
		t.Error("Remove on absent entry should report false")
	}
}

func TestRemoveOnNilHubs(t *testing.T) {
	s := &Store{}
	if s.Remove("wss://x") {
		t.Error("Remove on nil Hubs should report false, not panic")
	}
}

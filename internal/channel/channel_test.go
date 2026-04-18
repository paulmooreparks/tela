package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── Pure helpers ───────────────────────────────────────────────────

func TestIsKnown(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"dev", true},
		{"beta", true},
		{"stable", true},
		{"", false},
		{"DEV", false}, // case-sensitive on purpose; Normalize lowercases
		{"nightly", false},
		{"prod", false},
	}
	for _, c := range cases {
		if got := IsKnown(c.name); got != c.want {
			t.Errorf("IsKnown(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResolveBase(t *testing.T) {
	cases := []struct {
		name    string
		channel string
		sources map[string]string
		want    string
	}{
		{"built-in with no sources map", Dev, nil, DefaultManifestBase},
		{"built-in with empty sources map", Beta, map[string]string{}, DefaultManifestBase},
		{"built-in with empty string in sources", Stable, map[string]string{"stable": ""}, DefaultManifestBase},
		{"built-in overridden by sources", Stable, map[string]string{"stable": "https://mirror.example.com/"}, "https://mirror.example.com/"},
		{"custom channel with sources entry", "internal", map[string]string{"internal": "https://internal.example.com/"}, "https://internal.example.com/"},
		{"custom channel missing from sources returns empty", "internal", nil, ""},
		{"custom channel with empty sources value returns empty", "internal", map[string]string{"internal": ""}, ""},
		{"unrelated sources entries don't match", Dev, map[string]string{"internal": "https://internal.example.com/"}, DefaultManifestBase},
		{"multiple overrides only the matching one wins", Stable, map[string]string{"dev": "https://d/", "stable": "https://s/", "beta": "https://b/"}, "https://s/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveBase(c.channel, c.sources)
			if got != c.want {
				t.Errorf("ResolveBase(%q, %v) = %q, want %q", c.channel, c.sources, got, c.want)
			}
		})
	}
}

func TestInferFromVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Dev channel.
		{"v0.11.0-dev.11", "dev"},
		{"0.11.0-dev.1", "dev"},
		{"V0.11.0-DEV.1", "dev"},

		// Beta channel.
		{"v0.11.0-beta.1", "beta"},
		{"v0.11.0-beta.42", "beta"},

		// Stable channel (no prerelease suffix).
		{"v0.11.0", "stable"},
		{"v1.2.3", "stable"},
		{"0.11.0", "stable"},

		// Custom channels.
		{"v0.11.0-local.32", "local"},
		{"v0.11.0-experiment.3", "experiment"},
		{"v0.11.0-nightly.5", "nightly"},
		{"v0.11.0-feature-mux.1", "feature-mux"},

		// Build metadata is stripped before inference.
		{"v0.11.0-beta.1+build42", "beta"},
		{"v0.11.0-local.32+abc123", "local"},
		{"v0.11.0+build42", "stable"},

		// Whitespace tolerance.
		{"  v0.11.0-beta.1  ", "beta"},

		// Bad inputs: caller falls back to its own default.
		{"", ""},
		{"dev", ""},   // not a semver shape
		{"v0.11", ""}, // too few parts
		{"not a version", ""},
		{"v0.11.0-!invalid!.1", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := InferFromVersion(c.in)
			if got != c.want {
				t.Errorf("InferFromVersion(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMigrateManifestBase(t *testing.T) {
	t.Run("no-op when manifestBase empty", func(t *testing.T) {
		mb := ""
		sources := map[string]string{}
		changed := MigrateManifestBase("local", &mb, &sources)
		if changed {
			t.Error("empty manifestBase should be a no-op")
		}
		if len(sources) != 0 {
			t.Errorf("sources should be untouched, got %v", sources)
		}
	})

	t.Run("built-in channel discards manifestBase", func(t *testing.T) {
		mb := "https://parkscomputing.com/content/tela/channels/"
		var sources map[string]string
		changed := MigrateManifestBase("stable", &mb, &sources)
		if !changed {
			t.Error("built-in migration should report changed=true")
		}
		if mb != "" {
			t.Errorf("manifestBase should be cleared, got %q", mb)
		}
		if sources != nil && len(sources) != 0 {
			t.Errorf("sources should not be populated for built-in, got %v", sources)
		}
	})

	t.Run("custom channel copies manifestBase into sources", func(t *testing.T) {
		mb := "https://parkscomputing.com/content/tela/channels/"
		var sources map[string]string
		changed := MigrateManifestBase("local", &mb, &sources)
		if !changed {
			t.Error("custom migration should report changed=true")
		}
		if mb != "" {
			t.Errorf("manifestBase should be cleared, got %q", mb)
		}
		if sources["local"] != "https://parkscomputing.com/content/tela/channels/" {
			t.Errorf("sources[local] wrong: got %q", sources["local"])
		}
	})

	t.Run("existing sources entry wins; manifestBase still discarded", func(t *testing.T) {
		mb := "https://old.example.com/"
		sources := map[string]string{"local": "https://new.example.com/"}
		changed := MigrateManifestBase("local", &mb, &sources)
		if !changed {
			t.Error("should report changed=true because manifestBase was cleared")
		}
		if mb != "" {
			t.Errorf("manifestBase should be cleared, got %q", mb)
		}
		if sources["local"] != "https://new.example.com/" {
			t.Errorf("existing sources entry should win; got %q", sources["local"])
		}
	})

	t.Run("empty channel with manifestBase clears but does not populate", func(t *testing.T) {
		mb := "https://somewhere.example.com/"
		var sources map[string]string
		changed := MigrateManifestBase("", &mb, &sources)
		if !changed {
			t.Error("should report changed=true because manifestBase was cleared")
		}
		if mb != "" {
			t.Errorf("manifestBase should be cleared, got %q", mb)
		}
		if sources != nil && len(sources) != 0 {
			t.Errorf("sources should not be populated with empty channel name, got %v", sources)
		}
	})

	t.Run("nil sources pointer with built-in channel is safe", func(t *testing.T) {
		mb := "https://x/"
		var sources map[string]string
		changed := MigrateManifestBase("dev", &mb, &sources)
		if !changed {
			t.Error("should report changed=true")
		}
		if sources != nil {
			t.Errorf("sources should still be nil for built-in, got %v", sources)
		}
	})
}

func TestDefaultBases_ContainsAllBuiltins(t *testing.T) {
	for _, name := range []string{Dev, Beta, Stable} {
		if _, ok := DefaultBases[name]; !ok {
			t.Errorf("DefaultBases is missing built-in channel %q", name)
		}
	}
	// Spot-check: custom names don't accidentally leak in.
	if _, ok := DefaultBases["internal"]; ok {
		t.Error("DefaultBases should not contain custom channel names")
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dev", "dev"},
		{"DEV", "dev"},
		{"  Beta  ", "beta"},
		{"STABLE", "stable"},
		{"", DefaultChannel},
		{"garbage", "garbage"},          // valid custom name -- preserved
		{"nightly", "nightly"},          // valid custom name -- preserved
		{"My Channel!", DefaultChannel}, // invalid -- falls back
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestManifestURL(t *testing.T) {
	cases := []struct {
		base, name, want string
	}{
		{"", "dev", DefaultManifestBase + "dev.json"},
		{"", "", DefaultManifestBase + DefaultChannel + ".json"},
		{"https://example.com/ch", "beta", "https://example.com/ch/beta.json"},
		{"https://example.com/ch/", "beta", "https://example.com/ch/beta.json"},
		{"https://example.com/ch/", "STABLE", "https://example.com/ch/stable.json"},
		{"https://example.com/ch/", "garbage", "https://example.com/ch/garbage.json"},
		{"https://example.com/ch/", "My Channel!", "https://example.com/ch/" + DefaultChannel + ".json"},
	}
	for _, c := range cases {
		if got := ManifestURL(c.base, c.name); got != c.want {
			t.Errorf("ManifestURL(%q, %q) = %q, want %q", c.base, c.name, got, c.want)
		}
	}
}

func TestBinaryURL(t *testing.T) {
	t.Run("nil receiver returns empty", func(t *testing.T) {
		var m *Manifest
		if got := m.BinaryURL("anything"); got != "" {
			t.Errorf("nil receiver should return \"\", got %q", got)
		}
	})
	t.Run("empty downloadBase returns empty", func(t *testing.T) {
		m := &Manifest{}
		if got := m.BinaryURL("tela"); got != "" {
			t.Errorf("empty downloadBase should return \"\", got %q", got)
		}
	})
	t.Run("appends slash if missing", func(t *testing.T) {
		m := &Manifest{DownloadBase: "https://example.com/v1"}
		want := "https://example.com/v1/tela-linux-amd64"
		if got := m.BinaryURL("tela-linux-amd64"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("does not double slash", func(t *testing.T) {
		m := &Manifest{DownloadBase: "https://example.com/v1/"}
		want := "https://example.com/v1/tela-linux-amd64"
		if got := m.BinaryURL("tela-linux-amd64"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// ── Validate ───────────────────────────────────────────────────────

func validManifest() *Manifest {
	return &Manifest{
		Channel:      "dev",
		Version:      "v0.5.0-dev.1",
		Tag:          "v0.5.0-dev.1",
		DownloadBase: "https://example.com/v0.5.0-dev.1/",
		Binaries: map[string]BinaryEntry{
			"tela-linux-amd64": {
				SHA256: strings.Repeat("0", 64),
				Size:   12345,
			},
		},
	}
}

func TestValidate(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var m *Manifest
		if err := m.Validate(); err == nil {
			t.Fatal("nil manifest should fail to validate")
		}
	})

	t.Run("happy path", func(t *testing.T) {
		if err := validManifest().Validate(); err != nil {
			t.Fatalf("valid manifest rejected: %v", err)
		}
	})

	mutators := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"invalid channel", func(m *Manifest) { m.Channel = "My Channel!" }},
		{"missing version", func(m *Manifest) { m.Version = "" }},
		{"missing tag", func(m *Manifest) { m.Tag = "" }},
		{"missing downloadBase", func(m *Manifest) { m.DownloadBase = "" }},
		{"no binaries", func(m *Manifest) { m.Binaries = nil }},
		{"empty binaries", func(m *Manifest) { m.Binaries = map[string]BinaryEntry{} }},
		{"sha256 wrong length", func(m *Manifest) {
			m.Binaries["tela-linux-amd64"] = BinaryEntry{SHA256: "abcd", Size: 1}
		}},
		{"sha256 not hex", func(m *Manifest) {
			m.Binaries["tela-linux-amd64"] = BinaryEntry{SHA256: strings.Repeat("z", 64), Size: 1}
		}},
		{"size zero", func(m *Manifest) {
			m.Binaries["tela-linux-amd64"] = BinaryEntry{SHA256: strings.Repeat("0", 64), Size: 0}
		}},
		{"size negative", func(m *Manifest) {
			m.Binaries["tela-linux-amd64"] = BinaryEntry{SHA256: strings.Repeat("0", 64), Size: -1}
		}},
	}
	for _, c := range mutators {
		t.Run(c.name, func(t *testing.T) {
			m := validManifest()
			c.mutate(m)
			if err := m.Validate(); err == nil {
				t.Fatalf("%s should fail Validate, but passed", c.name)
			}
		})
	}
}

// ── VerifyReader ───────────────────────────────────────────────────

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestVerifyReader_Success(t *testing.T) {
	body := []byte("hello world, this is a test binary payload")
	expected := sha256Hex(body)
	var dst bytes.Buffer
	if err := VerifyReader(&dst, bytes.NewReader(body), expected, int64(len(body))); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if !bytes.Equal(dst.Bytes(), body) {
		t.Fatal("destination did not receive original bytes")
	}
}

func TestVerifyReader_HashMismatch(t *testing.T) {
	body := []byte("hello world")
	bad := strings.Repeat("0", 64) // wrong but well-formed hash
	var dst bytes.Buffer
	err := VerifyReader(&dst, bytes.NewReader(body), bad, int64(len(body)))
	if err == nil {
		t.Fatal("expected sha256 mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("expected sha256 mismatch in error, got %v", err)
	}
}

func TestVerifyReader_SizeMismatch(t *testing.T) {
	body := []byte("hello world")
	expected := sha256Hex(body)
	var dst bytes.Buffer
	err := VerifyReader(&dst, bytes.NewReader(body), expected, int64(len(body)+1))
	if err == nil {
		t.Fatal("expected size mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "size mismatch") {
		t.Errorf("expected size mismatch in error, got %v", err)
	}
}

func TestVerifyReader_SizeZeroSkipsCheck(t *testing.T) {
	// expectedSize <= 0 means "do not check size", only hash.
	body := []byte("hello world")
	expected := sha256Hex(body)
	var dst bytes.Buffer
	if err := VerifyReader(&dst, bytes.NewReader(body), expected, 0); err != nil {
		t.Fatalf("expected success when expectedSize is 0, got %v", err)
	}
}

func TestVerifyReader_HashIsCaseInsensitive(t *testing.T) {
	body := []byte("hello")
	expected := strings.ToUpper(sha256Hex(body))
	var dst bytes.Buffer
	if err := VerifyReader(&dst, bytes.NewReader(body), expected, int64(len(body))); err != nil {
		t.Errorf("upper-case hex hash should match, got %v", err)
	}
}

// ── Fetcher ────────────────────────────────────────────────────────

// fetcherTestServer returns an httptest.Server that serves a manifest
// for /dev.json built from the supplied template, and an atomic counter
// the test can read to confirm cache behavior.
func fetcherTestServer(t *testing.T, body []byte, status int) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func validManifestBytes() []byte {
	return []byte(fmt.Sprintf(`{
		"channel": "dev",
		"version": "v0.5.0-dev.1",
		"tag": "v0.5.0-dev.1",
		"publishedAt": "2026-04-07T00:00:00Z",
		"downloadBase": "https://example.com/v0.5.0-dev.1/",
		"binaries": {
			"tela-linux-amd64": { "sha256": %q, "size": 12345 }
		}
	}`, strings.Repeat("0", 64)))
}

func TestFetcher_GetURL_Success(t *testing.T) {
	srv, hits := fetcherTestServer(t, validManifestBytes(), 0)
	f := &Fetcher{Base: srv.URL + "/", CacheTTL: time.Minute}
	m, err := f.Get("dev")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.Channel != "dev" || m.Version != "v0.5.0-dev.1" {
		t.Errorf("unexpected manifest: %+v", m)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("expected 1 server hit, got %d", got)
	}
}

func TestFetcher_GetURL_CachesWithinTTL(t *testing.T) {
	srv, hits := fetcherTestServer(t, validManifestBytes(), 0)
	f := &Fetcher{Base: srv.URL + "/", CacheTTL: time.Hour}
	for i := 0; i < 5; i++ {
		if _, err := f.Get("dev"); err != nil {
			t.Fatalf("Get #%d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("expected 1 server hit (cached), got %d", got)
	}
}

func TestFetcher_Fetch_BypassesCache(t *testing.T) {
	srv, hits := fetcherTestServer(t, validManifestBytes(), 0)
	f := &Fetcher{Base: srv.URL + "/", CacheTTL: time.Hour}
	if _, err := f.Get("dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Fetch(srv.URL + "/dev.json"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("expected 2 server hits (one cached + one bypass), got %d", got)
	}
}

func TestFetcher_StaleOnFailure(t *testing.T) {
	// One server, one URL. It serves a good manifest first, then flips
	// to 500. The Fetcher must serve the stale cached entry rather than
	// surface the second-call failure to the caller. The cache key is
	// the URL, so we cannot rebind Base mid-test or we'd just look up a
	// different cache entry.
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(validManifestBytes())
	}))
	t.Cleanup(srv.Close)

	f := &Fetcher{Base: srv.URL + "/", CacheTTL: time.Millisecond}
	first, err := f.Get("dev")
	if err != nil {
		t.Fatalf("prime: %v", err)
	}

	time.Sleep(5 * time.Millisecond) // ensure TTL elapsed
	fail.Store(true)

	stale, err := f.Get("dev")
	if err != nil {
		t.Fatalf("expected stale fall-back, got error: %v", err)
	}
	if stale != first {
		t.Error("stale fetch returned a different object than the cached one")
	}
}

func TestFetcher_RejectsInvalidManifest(t *testing.T) {
	bad := []byte(`{"channel": "dev"}`) // missing required fields
	srv, _ := fetcherTestServer(t, bad, 0)
	f := &Fetcher{Base: srv.URL + "/"}
	_, err := f.Get("dev")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestFetcher_RejectsHTTPError(t *testing.T) {
	srv, _ := fetcherTestServer(t, nil, http.StatusNotFound)
	f := &Fetcher{Base: srv.URL + "/"}
	_, err := f.Get("dev")
	if err == nil {
		t.Fatal("expected HTTP 404 error, got nil")
	}
}

func TestFetcher_TruncatesGiantBody(t *testing.T) {
	// fetch() caps the read at 1 MiB. A body larger than that should
	// either parse-fail (if truncated mid-document) or simply not OOM.
	// Both outcomes are acceptable; the contract is "do not blow up".
	huge := bytes.Repeat([]byte("x"), 2<<20)
	srv, _ := fetcherTestServer(t, huge, 0)
	f := &Fetcher{Base: srv.URL + "/"}
	_, err := f.Get("dev")
	if err == nil {
		t.Fatal("expected parse error from truncated giant body, got nil")
	}
}

// ── Misc sanity ─────────────────────────────────────────────────────

func TestErrorWrapping(t *testing.T) {
	// Validate that fetch errors propagate enough context to be
	// debuggable -- specifically, the URL should appear in the message.
	srv, _ := fetcherTestServer(t, nil, http.StatusNotFound)
	f := &Fetcher{Base: srv.URL + "/"}
	_, err := f.Get("dev")
	if err == nil || !strings.Contains(err.Error(), "dev.json") {
		t.Errorf("expected URL in error, got %v", err)
	}
	// Use errors package to silence the lint about unused import in
	// case future changes drop the strings.Contains usage above.
	_ = errors.Is
	_ = io.EOF
}

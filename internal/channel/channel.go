// Package channel implements the client side of Tela's release channel
// model. A channel is a named pointer (dev / beta / stable) that resolves
// to a single tag plus the binaries published under that tag. Each Tela
// binary fetches its channel manifest on self-update, compares its current
// version, and downloads the named binary verifying its SHA-256 against
// the manifest before swapping it in.
//
// The full channel model is documented in RELEASE-PROCESS.md and the
// JSON schema lives at channels/schema.json.
package channel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Names of the three known channels. Anything else is rejected by Validate.
const (
	Dev    = "dev"
	Beta   = "beta"
	Stable = "stable"
)

// DefaultChannel is the channel used when no explicit channel is set in
// configuration. Until 1.0 the only channel that exists is dev.
const DefaultChannel = Dev

// DefaultManifestBase is the upstream URL prefix where the official Tela
// channel manifests are hosted. Each channel's manifest lives at
// {DefaultManifestBase}{channel}.json. Operators who self-host a fork can
// override this via configuration.
const DefaultManifestBase = "https://github.com/paulmooreparks/tela/releases/download/channels/"

// DefaultBases is the baked-in map of built-in channel names to their
// upstream base URLs. Custom channel names have no entry here and must be
// supplied by configuration (update.sources).
//
// The sources data model (see DESIGN-channel-sources.md) treats this as
// the fallback consulted when a channel name is not in the host's own
// sources map. Config stays sparse: fresh installs have no sources entry
// at all and still work for dev/beta/stable because the lookup falls
// through to this map.
//
// Operators who want to override a built-in (e.g. point stable at an
// internal mirror) add an entry to update.sources; the resolver prefers
// that over the baked-in default.
var DefaultBases = map[string]string{
	Dev:    DefaultManifestBase,
	Beta:   DefaultManifestBase,
	Stable: DefaultManifestBase,
}

// ResolveBase returns the base URL to use for the named channel given the
// host's own sources map. The lookup order is:
//  1. sources[name] if present and non-empty
//  2. DefaultBases[name] if name is a built-in
//  3. empty string (caller should treat as "unknown channel")
//
// Sources entries with empty-string values are treated the same as "not
// present": they signal "use the baked-in default if one exists" and
// otherwise fall through to step 2 or 3.
func ResolveBase(name string, sources map[string]string) string {
	if v, ok := sources[name]; ok && v != "" {
		return v
	}
	return DefaultBases[name]
}

// InferFromVersion returns the channel a binary logically belongs to given
// its own version string. The version shape dictates the channel:
//
//	vX.Y.0-dev.N            → "dev"
//	vX.Y.0-beta.N           → "beta"
//	vX.Y.Z (no prerelease)  → "stable"
//	vX.Y.0-{name}.N         → "{name}"  (custom channels)
//
// Inputs that do not parse as a semver-shaped version (empty, "dev" bare,
// malformed) return the empty string; callers treat that as "no inference
// possible" and fall back to whatever default they'd use otherwise.
//
// This is the default-channel source for a freshly-downloaded binary that
// has no saved channel preference yet. It solves the "beta binary updated
// itself off the dev channel because dev was the compile-time default" bug
// by deriving the default from what the binary actually is, not from a
// compile-time constant.
func InferFromVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Strip leading 'v' if present; semver allows both.
	if v[0] == 'v' || v[0] == 'V' {
		v = v[1:]
	}
	// Strip build metadata ("+..." suffix) before anything else; it never
	// affects the channel.
	if plusIdx := strings.IndexByte(v, '+'); plusIdx >= 0 {
		v = v[:plusIdx]
	}
	// Everything before the first '-' is the MAJOR.MINOR.PATCH core.
	// Everything after is the prerelease identifier.
	dashIdx := strings.IndexByte(v, '-')
	if dashIdx < 0 {
		// No prerelease suffix: this is a stable version like 0.10.1.
		// Validate the core loosely; if it doesn't look like X.Y.Z we
		// can't infer anything.
		if !looksLikeSemverCore(v) {
			return ""
		}
		return Stable
	}
	core := v[:dashIdx]
	prerelease := v[dashIdx+1:]
	if !looksLikeSemverCore(core) {
		return ""
	}
	// The prerelease is dot-separated identifiers. The first identifier is
	// the channel name; subsequent identifiers are the counter. Example:
	// "beta.1" → channel "beta"; "local.32" → channel "local".
	firstIdent := prerelease
	if dotIdx := strings.IndexByte(prerelease, '.'); dotIdx >= 0 {
		firstIdent = prerelease[:dotIdx]
	}
	firstIdent = strings.ToLower(firstIdent)
	if !IsValid(firstIdent) {
		return ""
	}
	return firstIdent
}

// looksLikeSemverCore returns true when s is a plausible MAJOR.MINOR.PATCH
// string. Very loose; we don't enforce full semver validity here because
// the goal is to extract a channel hint, not validate the version itself.
func looksLikeSemverCore(s string) bool {
	// Must contain at least two dots (X.Y.Z) and only digits/dots.
	if strings.Count(s, ".") < 2 {
		return false
	}
	for _, r := range s {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// MigrateManifestBase applies the pre-0.12 update.manifestBase → update.sources
// migration in place. If manifestBase is empty, the function is a no-op.
// Otherwise:
//
//   - When the channel is a built-in (dev / beta / stable), manifestBase is
//     discarded. The baked-in default takes over. The operator's original
//     override is lost; that is the accepted tradeoff for getting off the
//     old shape cleanly.
//   - When the channel is a custom name, manifestBase is moved into
//     sources[channel] unless sources[channel] is already set (in which
//     case the existing sources entry wins and manifestBase is simply
//     discarded).
//
// In all cases *manifestBase is cleared to the empty string so the legacy
// field will be omitted from the next on-disk write (omitempty kicks in).
// Returns true when the function made any change, suitable for a one-time
// log notice at the call site.
//
// This helper is scheduled for removal in 0.13 together with the
// ManifestBase fields on the three config structs that still carry it.
// See GitHub issue #59 for the deletion checklist.
func MigrateManifestBase(channelName string, manifestBase *string, sources *map[string]string) bool {
	if manifestBase == nil || *manifestBase == "" {
		return false
	}
	if channelName != "" && !IsKnown(channelName) {
		if *sources == nil {
			*sources = map[string]string{}
		}
		if _, exists := (*sources)[channelName]; !exists {
			(*sources)[channelName] = *manifestBase
		}
	}
	*manifestBase = ""
	return true
}

// BinaryEntry describes one binary listed in a channel manifest.
type BinaryEntry struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is the parsed form of one channel manifest. The wire shape is
// documented in channels/schema.json. Field names match the schema exactly.
type Manifest struct {
	Channel      string                 `json:"channel"`
	Version      string                 `json:"version"`
	Tag          string                 `json:"tag"`
	PublishedAt  time.Time              `json:"publishedAt"`
	DownloadBase string                 `json:"downloadBase"`
	Binaries     map[string]BinaryEntry `json:"binaries"`
}

// IsKnown reports whether name is one of the three standard channel names.
// Custom channel names are valid when paired with a manifestBase override;
// use IsValid to check whether a name is acceptable at all.
func IsKnown(name string) bool {
	switch name {
	case Dev, Beta, Stable:
		return true
	}
	return false
}

// IsValid reports whether name is a non-empty string containing only
// lowercase letters, digits, and hyphens. Both standard names (dev, beta,
// stable) and custom names (e.g. "local", "nightly") are valid.
func IsValid(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

// Normalize returns the canonical channel name for an input string. Empty
// or invalid values fall back to DefaultChannel.
func Normalize(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if !IsValid(name) {
		return DefaultChannel
	}
	return name
}

// ManifestURL returns the URL of the named channel's manifest given a base
// URL. The base may or may not end in a slash. If base is empty,
// DefaultManifestBase is used.
func ManifestURL(base, name string) string {
	if base == "" {
		base = DefaultManifestBase
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + Normalize(name) + ".json"
}

// BinaryURL returns the absolute download URL for a binary listed in this
// manifest. It joins the manifest's DownloadBase with the binary name. The
// returned URL is suitable for an HTTP GET; the caller is responsible for
// verifying the SHA-256 against the matching BinaryEntry.
func (m *Manifest) BinaryURL(binaryName string) string {
	if m == nil {
		return ""
	}
	base := m.DownloadBase
	if base == "" {
		return ""
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + binaryName
}

// Validate returns an error if the manifest is missing required fields or
// contains values that do not match the schema. It does not contact the
// network.
func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if !IsValid(m.Channel) {
		return fmt.Errorf("invalid channel name %q (use lowercase letters, digits, hyphens)", m.Channel)
	}
	if m.Version == "" {
		return fmt.Errorf("manifest missing version")
	}
	if m.Tag == "" {
		return fmt.Errorf("manifest missing tag")
	}
	if m.DownloadBase == "" {
		return fmt.Errorf("manifest missing downloadBase")
	}
	if len(m.Binaries) == 0 {
		return fmt.Errorf("manifest lists no binaries")
	}
	for name, b := range m.Binaries {
		if len(b.SHA256) != 64 {
			return fmt.Errorf("binary %s: sha256 must be 64 hex chars, got %d", name, len(b.SHA256))
		}
		if _, err := hex.DecodeString(b.SHA256); err != nil {
			return fmt.Errorf("binary %s: sha256 not hex: %w", name, err)
		}
		if b.Size <= 0 {
			return fmt.Errorf("binary %s: size must be > 0", name)
		}
	}
	return nil
}

// Fetcher fetches and caches channel manifests over HTTP. A single Fetcher
// is safe for concurrent use. Manifests are cached per URL for CacheTTL;
// stale entries are still served if a refresh fails.
type Fetcher struct {
	// Base is the URL prefix used by Get when given only a channel name.
	// If empty, DefaultManifestBase is used.
	Base string

	// CacheTTL controls how long a successful fetch is reused. If zero,
	// DefaultCacheTTL is used.
	CacheTTL time.Duration

	// HTTPClient is the client used for outbound requests. If nil, a
	// 15-second-timeout default client is used.
	HTTPClient *http.Client

	mu    sync.Mutex
	cache map[string]*cacheEntry
}

// DefaultCacheTTL is how long a fetched manifest is cached when the
// Fetcher's CacheTTL is left at zero.
const DefaultCacheTTL = 5 * time.Minute

type cacheEntry struct {
	manifest *Manifest
	fetched  time.Time
}

// Get returns the manifest for the named channel. The result may come from
// the in-memory cache. Call Fetch to bypass the cache.
func (f *Fetcher) Get(name string) (*Manifest, error) {
	url := ManifestURL(f.Base, name)
	return f.GetURL(url)
}

// GetURL returns the manifest for an explicit URL. Mostly useful when an
// operator overrides the manifest location per-binary.
func (f *Fetcher) GetURL(url string) (*Manifest, error) {
	ttl := f.CacheTTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	f.mu.Lock()
	if f.cache == nil {
		f.cache = make(map[string]*cacheEntry)
	}
	if e, ok := f.cache[url]; ok && time.Since(e.fetched) < ttl {
		m := e.manifest
		f.mu.Unlock()
		return m, nil
	}
	f.mu.Unlock()

	m, err := f.fetch(url)
	if err != nil {
		// Serve stale on failure rather than blocking the caller.
		f.mu.Lock()
		if e, ok := f.cache[url]; ok {
			stale := e.manifest
			f.mu.Unlock()
			return stale, nil
		}
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Lock()
	f.cache[url] = &cacheEntry{manifest: m, fetched: time.Now()}
	f.mu.Unlock()
	return m, nil
}

// Fetch unconditionally retrieves and parses the manifest at url, bypassing
// the cache. The cache is updated on success.
func (f *Fetcher) Fetch(url string) (*Manifest, error) {
	m, err := f.fetch(url)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.cache == nil {
		f.cache = make(map[string]*cacheEntry)
	}
	f.cache[url] = &cacheEntry{manifest: m, fetched: time.Now()}
	f.mu.Unlock()
	return m, nil
}

func (f *Fetcher) fetch(url string) (*Manifest, error) {
	client := f.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", url, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", url, err)
	}
	return &m, nil
}

// CompareVersions compares two Tela version strings of the form
// vX.Y.Z[-pre.N] and returns -1, 0, or 1. A "dev" version is treated as
// less than any real release so that dev builds do not falsely report
// themselves as up-to-date relative to a channel tag.
func CompareVersions(a, b string) int {
	norm := func(s string) []int {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
		parts := strings.FieldsFunc(s, func(r rune) bool {
			return r == '.' || r == '-'
		})
		nums := make([]int, len(parts))
		for i, p := range parts {
			if n, err := strconv.Atoi(p); err == nil {
				nums[i] = n
			} else {
				// Non-numeric segment (e.g. "dev", "local", "beta"): map to
				// a small negative value so pre-release sorts below numeric.
				nums[i] = -1
			}
		}
		return nums
	}
	pa, pb := norm(a), norm(b)
	length := len(pa)
	if len(pb) > length {
		length = len(pb)
	}
	for i := 0; i < length; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// IsNewer reports whether candidate is strictly newer than current.
// Returns false when either string is "dev" (development builds are
// not version-comparable).
func IsNewer(candidate, current string) bool {
	if candidate == "dev" || current == "dev" {
		return false
	}
	return CompareVersions(candidate, current) > 0
}

// VerifyReader streams r through a SHA-256 hash and into w, returning an
// error if the hash does not match expected (lowercase hex). The compare
// happens after the full body is read so callers can use io.Copy with a
// temp file. Caller is responsible for closing w.
func VerifyReader(w io.Writer, r io.Reader, expected string, expectedSize int64) error {
	h := sha256.New()
	mw := io.MultiWriter(w, h)
	n, err := io.Copy(mw, r)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if expectedSize > 0 && n != expectedSize {
		return fmt.Errorf("size mismatch: got %d bytes, manifest says %d", n, expectedSize)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch: got %s, manifest says %s", got, expected)
	}
	return nil
}

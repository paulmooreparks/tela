package main

// portal_sources.go is the TelaVisor side of the portal-source
// concept. A "portal source" is one portal endpoint TV knows how to
// talk to: the embedded loopback portal that ships inside TV (always
// present), plus any number of remote portals the user has signed into
// via the OAuth 2.0 device authorization grant (RFC 8628; spec:
// tela/DESIGN-portal.md section 6.3).
//
// Any number of sources may be enabled at once. Phase 5b feeds every
// enabled source into portalaggregate.Merge to produce the unified
// hub/agent views shown in Infrastructure mode.
//
// The remote-source list is persisted at
// ~/.tela/portal-sources.yaml. The embedded source is synthesized at
// every PortalListSources call from the live portalEmbed so the
// bearer token tracks the per-process value.
//
// On-disk format (post-5a):
//
//	embeddedEnabled: true
//	remote:
//	  - name: portal.example.com
//	    kind: remote
//	    url: https://portal.example.com
//	    bearerToken: <token>
//	    enabled: true
//	    addedAt: 2025-01-01T00:00:00Z
//
// Migration: pre-5a files have an activeName field. On first load of
// an old file the migration logic converts it:
//   - activeName refers to a remote: that remote becomes enabled=true,
//     all others and the embedded source become enabled=false.
//   - activeName is empty (embedded was selected): embedded becomes
//     enabled=true, all remotes become enabled=true (they are still
//     registered so keep them accessible).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/paulmooreparks/tela/internal/portalclient"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"gopkg.in/yaml.v3"
)

// PortalSourceKind distinguishes the in-process embedded portal from
// remote portals signed in via OAuth. The frontend uses this to label
// rows ("Local" vs the portal hostname).
type PortalSourceKind string

const (
	PortalSourceKindEmbedded PortalSourceKind = "embedded"
	PortalSourceKindRemote   PortalSourceKind = "remote"
)

// PortalSource is one portal endpoint TV can talk to. The embedded
// source is synthesized at runtime; remote sources are persisted to
// portal-sources.yaml. BearerToken is omitted from JSON so it never
// leaks into the DOM.
type PortalSource struct {
	Name        string           `yaml:"name" json:"name"`
	Kind        PortalSourceKind `yaml:"kind" json:"kind"`
	URL         string           `yaml:"url" json:"url"`
	BearerToken string           `yaml:"bearerToken" json:"-"`
	Enabled     bool             `yaml:"enabled" json:"enabled"`
	AddedAt     string           `yaml:"addedAt,omitempty" json:"addedAt,omitempty"`
}

// portalSourcesFile is the on-disk shape.
//
// EmbeddedEnabled controls whether the embedded in-process portal is
// included in merge operations. A nil pointer means the file predates
// 5a and migration is needed. ActiveName is present only for reading
// pre-5a files during migration; it is always cleared before writing.
type portalSourcesFile struct {
	// ActiveName is the pre-5a "single active source" field. Read
	// only during migration; never written after migration.
	ActiveName string `yaml:"activeName,omitempty"`

	// EmbeddedEnabled controls whether the always-present embedded
	// portal participates in merge. Nil means the file predates 5a.
	EmbeddedEnabled *bool `yaml:"embeddedEnabled,omitempty"`

	Remote []PortalSource `yaml:"remote"`
}

// portalSourcesPath is the absolute path of the persisted file.
func portalSourcesPath() string {
	return filepath.Join(telaConfigDir(), "portal-sources.yaml")
}

// embeddedSourceName is the synthetic name returned for the
// in-process portal. Reserved: a remote portal MUST NOT use this name.
const embeddedSourceName = "Local"

// portalSourcesMu guards reads and writes of the persisted file.
var portalSourcesMu sync.Mutex

// loadPortalSources reads the persisted file and applies migration if
// needed. A missing file returns zero value without error.
func loadPortalSources() (portalSourcesFile, error) {
	var out portalSourcesFile
	data, err := os.ReadFile(portalSourcesPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fresh install: embedded enabled by default.
			t := true
			out.EmbeddedEnabled = &t
			return out, nil
		}
		return out, fmt.Errorf("portal sources: read: %w", err)
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("portal sources: parse: %w", err)
	}
	// Migration: EmbeddedEnabled == nil means this is a pre-5a file.
	if out.EmbeddedEnabled == nil {
		out = migratePortalSources(out)
		// Persist the migrated file immediately so subsequent loads
		// skip migration. Ignore write errors here; migration is
		// best-effort and the in-memory state is correct either way.
		_ = savePortalSources(out)
	}
	return out, nil
}

// migratePortalSources converts a pre-5a file (has ActiveName, no
// EmbeddedEnabled) to the 5a format.
func migratePortalSources(old portalSourcesFile) portalSourcesFile {
	embEnabled := old.ActiveName == "" || old.ActiveName == embeddedSourceName

	t := embEnabled
	old.EmbeddedEnabled = &t

	for i := range old.Remote {
		if old.ActiveName != "" && old.ActiveName != embeddedSourceName {
			old.Remote[i].Enabled = (old.Remote[i].Name == old.ActiveName)
		} else {
			// Embedded was active: keep all registered remotes enabled.
			old.Remote[i].Enabled = true
		}
	}
	// Clear the migration field so it is not written back.
	old.ActiveName = ""
	return old
}

// savePortalSources writes the file atomically (temp + rename) at
// 0600 because the bearer tokens are credentials.
func savePortalSources(file portalSourcesFile) error {
	path := portalSourcesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("portal sources: mkdir: %w", err)
	}
	data, err := yaml.Marshal(&file)
	if err != nil {
		return fmt.Errorf("portal sources: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".portal-sources-*.tmp")
	if err != nil {
		return fmt.Errorf("portal sources: temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("portal sources: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal sources: close: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal sources: chmod: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal sources: rename: %w", err)
	}
	return nil
}

// embeddedIsEnabled reports whether the embedded portal is currently
// marked as enabled in the persisted file. Callers must hold
// portalSourcesMu or pass the already-loaded file.
func embeddedIsEnabledIn(file portalSourcesFile) bool {
	if file.EmbeddedEnabled == nil {
		return true // safe default; migration should have run already
	}
	return *file.EmbeddedEnabled
}

// embeddedSource returns the synthetic PortalSource for the
// in-process portal, or nil if the embedded portal failed to start.
func (a *App) embeddedSource() *PortalSource {
	if a.portal == nil {
		return nil
	}
	return &PortalSource{
		Name:        embeddedSourceName,
		Kind:        PortalSourceKindEmbedded,
		URL:         a.portal.endpointURL,
		BearerToken: a.portal.bearerToken,
		Enabled:     true, // populated by PortalListSources with real value
	}
}

// portalClientForSource returns a configured *portalclient.Client
// pointing at the named source, or an error if the name is unknown.
// "" or embeddedSourceName routes to the embedded portal. Used by
// per-hub admin calls (5d) and by the firstEnabledSourceName shim.
func (a *App) portalClientForSource(name string) (*portalclient.Client, error) {
	if name == "" || name == embeddedSourceName {
		emb := a.embeddedSource()
		if emb == nil {
			return nil, errors.New("portal sources: embedded portal not running")
		}
		return portalclient.New(emb.URL, emb.BearerToken), nil
	}
	portalSourcesMu.Lock()
	defer portalSourcesMu.Unlock()
	file, err := loadPortalSources()
	if err != nil {
		return nil, err
	}
	for _, s := range file.Remote {
		if s.Name == name {
			return portalclient.New(s.URL, s.BearerToken), nil
		}
	}
	return nil, fmt.Errorf("portal sources: %q not found", name)
}

// firstEnabledSourceName returns the name of the first enabled
// source. Embedded "Local" wins if enabled; otherwise the first
// enabled remote is returned. Returns embeddedSourceName if nothing
// is enabled (so callers get a graceful empty result rather than an
// error).
//
// This is a 5a shim. Phase 5b replaces the single-source calls in
// app.go with enabledPortalClients() + portalaggregate.Merge.
func (a *App) firstEnabledSourceName() (string, error) {
	portalSourcesMu.Lock()
	file, err := loadPortalSources()
	portalSourcesMu.Unlock()
	if err != nil {
		return embeddedSourceName, err
	}
	if embeddedIsEnabledIn(file) {
		return embeddedSourceName, nil
	}
	for _, s := range file.Remote {
		if s.Enabled {
			return s.Name, nil
		}
	}
	// Nothing enabled: fall back to embedded so callers still function.
	return embeddedSourceName, nil
}

// enabledPortalClients returns a client for every currently enabled
// source, keyed by source name. Used by Phase 5b to feed
// portalaggregate.Merge.
func (a *App) enabledPortalClients() (map[string]*portalclient.Client, error) {
	portalSourcesMu.Lock()
	file, err := loadPortalSources()
	portalSourcesMu.Unlock()
	if err != nil {
		return nil, err
	}

	out := make(map[string]*portalclient.Client)

	if embeddedIsEnabledIn(file) {
		emb := a.embeddedSource()
		if emb != nil {
			out[embeddedSourceName] = portalclient.New(emb.URL, emb.BearerToken)
		}
	}
	for _, s := range file.Remote {
		if s.Enabled {
			out[s.Name] = portalclient.New(s.URL, s.BearerToken)
		}
	}
	return out, nil
}

// ── Wails-bound methods ────────────────────────────────────────────

// PortalListSources returns every portal source TV knows about. The
// embedded source is always first if it is running. Bearer tokens
// are stripped via the json:"-" tag on PortalSource.BearerToken.
func (a *App) PortalListSources() ([]PortalSource, error) {
	portalSourcesMu.Lock()
	file, err := loadPortalSources()
	portalSourcesMu.Unlock()
	if err != nil {
		return nil, err
	}

	var out []PortalSource
	if emb := a.embeddedSource(); emb != nil {
		emb.Enabled = embeddedIsEnabledIn(file)
		out = append(out, *emb)
	}
	out = append(out, file.Remote...)
	return out, nil
}

// PortalSetSourceEnabled enables or disables a portal source by name.
// Use embeddedSourceName ("Local") to toggle the embedded portal.
func (a *App) PortalSetSourceEnabled(name string, enabled bool) error {
	portalSourcesMu.Lock()
	defer portalSourcesMu.Unlock()
	file, err := loadPortalSources()
	if err != nil {
		return err
	}
	if name == "" || name == embeddedSourceName {
		file.EmbeddedEnabled = &enabled
		return savePortalSources(file)
	}
	for i, s := range file.Remote {
		if s.Name == name {
			file.Remote[i].Enabled = enabled
			return savePortalSources(file)
		}
	}
	return fmt.Errorf("portal sources: %q not found", name)
}

// PortalRemoveSource deletes a remote source by name. Removing the
// embedded source is forbidden. Removing an enabled source is allowed;
// no automatic fallback occurs.
func (a *App) PortalRemoveSource(name string) error {
	if name == "" || name == embeddedSourceName {
		return errors.New("portal sources: cannot remove the embedded source")
	}
	portalSourcesMu.Lock()
	defer portalSourcesMu.Unlock()
	file, err := loadPortalSources()
	if err != nil {
		return err
	}
	kept := file.Remote[:0]
	removed := false
	for _, s := range file.Remote {
		if s.Name == name {
			removed = true
			continue
		}
		kept = append(kept, s)
	}
	if !removed {
		return fmt.Errorf("portal sources: %q not found", name)
	}
	file.Remote = kept
	return savePortalSources(file)
}

// PortalDeviceAuthStart begins the device code flow against a remote
// portal. It calls Discover to verify protocol support, then POST
// /api/oauth/device to mint a fresh device code, then opens
// VerificationURI in the system browser.
//
// rawURL accepts both bare hostnames and full http(s):// URLs.
func (a *App) PortalDeviceAuthStart(rawURL string) (PortalDeviceAuthStartResult, error) {
	var out PortalDeviceAuthStartResult
	baseURL, err := normalizePortalURL(rawURL)
	if err != nil {
		return out, err
	}

	c := portalclient.New(baseURL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	disc, err := c.Discover(ctx)
	if err != nil {
		return out, fmt.Errorf("portal sources: discover %s: %w", baseURL, err)
	}
	if !supportsProtocol1(disc) {
		return out, fmt.Errorf("portal sources: %s reports protocol %q; TelaVisor requires 1.0", baseURL, disc.ProtocolVersion)
	}

	auth, err := c.StartDeviceAuth(ctx)
	if err != nil {
		return out, fmt.Errorf("portal sources: start device auth: %w", err)
	}

	browseTo := auth.VerificationURIComplete
	if browseTo == "" {
		browseTo = auth.VerificationURI
	}
	if a.ctx != nil && browseTo != "" {
		wailsRuntime.BrowserOpenURL(a.ctx, browseTo)
	}

	out = PortalDeviceAuthStartResult{
		BaseURL:         baseURL,
		DeviceCode:      auth.DeviceCode,
		UserCode:        auth.UserCode,
		VerificationURI: browseTo,
		ExpiresIn:       auth.ExpiresIn,
		Interval:        auth.Interval,
	}
	return out, nil
}

// PortalDeviceAuthStartResult is the JSON-friendly shape returned to
// the frontend.
type PortalDeviceAuthStartResult struct {
	BaseURL         string `json:"baseURL"`
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURI string `json:"verificationURI"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

// PortalDeviceAuthComplete polls /api/oauth/token until the user
// authorizes, denies, or the device code expires. On success it
// persists a new PortalSource (enabled=true) under sourceName.
func (a *App) PortalDeviceAuthComplete(sourceName, baseURL, deviceCode string, intervalSeconds int) error {
	sourceName = strings.TrimSpace(sourceName)
	if sourceName == "" {
		return errors.New("portal sources: a name is required")
	}
	if sourceName == embeddedSourceName {
		return fmt.Errorf("portal sources: %q is reserved for the embedded source", embeddedSourceName)
	}
	if baseURL == "" || deviceCode == "" {
		return errors.New("portal sources: baseURL and deviceCode are required")
	}
	if intervalSeconds <= 0 {
		intervalSeconds = 5
	}

	c := portalclient.New(baseURL, "")
	deadline := time.Now().Add(20 * time.Minute)
	interval := time.Duration(intervalSeconds) * time.Second

	for {
		if time.Now().After(deadline) {
			return errors.New("portal sources: device code expired")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		tok, err := c.PollDeviceToken(ctx, deviceCode)
		cancel()

		switch {
		case err == nil:
			return a.savePortalSourceFromToken(sourceName, baseURL, tok.AccessToken)
		case errors.Is(err, portalclient.ErrAuthorizationPending):
			// fall through to sleep
		case errors.Is(err, portalclient.ErrSlowDown):
			interval += 5 * time.Second
		case errors.Is(err, portalclient.ErrAccessDenied):
			return errors.New("portal sources: user denied authorization")
		case errors.Is(err, portalclient.ErrExpiredToken):
			return errors.New("portal sources: device code expired before authorization")
		default:
			return fmt.Errorf("portal sources: poll: %w", err)
		}

		select {
		case <-time.After(interval):
		case <-a.ctx.Done():
			return a.ctx.Err()
		}
	}
}

// savePortalSourceFromToken persists a freshly authorized remote
// source with Enabled=true. A name collision is treated as an upsert.
func (a *App) savePortalSourceFromToken(name, baseURL, bearer string) error {
	portalSourcesMu.Lock()
	defer portalSourcesMu.Unlock()
	file, err := loadPortalSources()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	updated := false
	for i, s := range file.Remote {
		if s.Name == name {
			file.Remote[i].URL = baseURL
			file.Remote[i].BearerToken = bearer
			file.Remote[i].AddedAt = now
			file.Remote[i].Enabled = true
			updated = true
			break
		}
	}
	if !updated {
		file.Remote = append(file.Remote, PortalSource{
			Name:        name,
			Kind:        PortalSourceKindRemote,
			URL:         baseURL,
			BearerToken: bearer,
			Enabled:     true,
			AddedAt:     now,
		})
	}
	return savePortalSources(file)
}

// ── helpers ────────────────────────────────────────────────────────

// normalizePortalURL accepts a hostname or URL and returns a clean
// scheme://host[:port] base URL. Bare hostnames default to https.
func normalizePortalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("portal sources: URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("portal sources: invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("portal sources: URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("portal sources: URL has no host")
	}
	// Drop path/query/fragment; the portal lives at the root.
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// supportsProtocol1 returns true if the discovery response advertises
// protocol 1.0 either as ProtocolVersion or in SupportedVersions.
func supportsProtocol1(disc portalclient.Discovery) bool {
	if disc.ProtocolVersion == "1.0" {
		return true
	}
	for _, v := range disc.SupportedVersions {
		if v == "1.0" {
			return true
		}
	}
	return false
}

package main

// portal_sources.go is the TelaVisor side of the portal-source
// concept. A "portal source" is one portal endpoint TV knows how to
// talk to: the embedded loopback portal that ships inside TV (always
// present, always selected by default), plus any number of remote
// portals the user has signed into via the OAuth 2.0 device
// authorization grant (RFC 8628; spec: tela/DESIGN-portal.md
// section 6.3).
//
// The remote-source list is persisted at
// ~/.tela/portal-sources.yaml and is loaded eagerly into memory at
// startup. The embedded source is *not* in the file: it is
// synthesized at every PortalListSources call from the live
// portalEmbed so the bearer token tracks the per-process value.
//
// The Wails-bound methods at the bottom of this file are what the
// frontend dialog calls. The frontend lives separately and lands in
// a follow-up commit alongside the Infrastructure-mode rewire that
// actually consumes the active source.

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
// portal-sources.yaml. BearerToken is omitted from JSON exposed to
// the frontend so it never leaks into the DOM.
type PortalSource struct {
	Name        string           `yaml:"name" json:"name"`
	Kind        PortalSourceKind `yaml:"kind" json:"kind"`
	URL         string           `yaml:"url" json:"url"`
	BearerToken string           `yaml:"bearerToken" json:"-"`
	AddedAt     string           `yaml:"addedAt,omitempty" json:"addedAt,omitempty"`
}

// portalSourcesFile is the on-disk shape. ActiveName is the name of
// the source the user has currently selected; an empty value means
// "use the embedded source." Remote is the list of signed-in remote
// portals.
type portalSourcesFile struct {
	ActiveName string         `yaml:"activeName,omitempty"`
	Remote     []PortalSource `yaml:"remote"`
}

// portalSourcesPath is the absolute path of the persisted file.
func portalSourcesPath() string {
	return filepath.Join(telaConfigDir(), "portal-sources.yaml")
}

// embeddedSourceName is the synthetic name returned for the
// in-process portal. Reserved: a remote portal MUST NOT use this name.
const embeddedSourceName = "Local"

// portalSourcesMu guards reads and writes of the persisted file. The
// file is small and rarely written, so a single mutex is sufficient.
var portalSourcesMu sync.Mutex

// loadPortalSources reads the persisted file. A missing file returns
// the zero value (no remote sources, no active selection) without an
// error so first launch is friction-free.
func loadPortalSources() (portalSourcesFile, error) {
	var out portalSourcesFile
	data, err := os.ReadFile(portalSourcesPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, fmt.Errorf("portal sources: read: %w", err)
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("portal sources: parse: %w", err)
	}
	return out, nil
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

// embeddedSource returns the synthetic source for the in-process
// portal, or nil if the embedded portal failed to start.
func (a *App) embeddedSource() *PortalSource {
	if a.portal == nil {
		return nil
	}
	return &PortalSource{
		Name:        embeddedSourceName,
		Kind:        PortalSourceKindEmbedded,
		URL:         a.portal.endpointURL,
		BearerToken: a.portal.bearerToken,
	}
}

// portalClientForSource returns a configured *portalclient.Client
// pointing at the named source, or an error if the name is unknown.
// Looking up "" or embeddedSourceName returns the embedded source.
// This is the function the Infrastructure-mode rewire will call to
// build the client it issues every admin call through.
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

// ── Wails-bound methods ────────────────────────────────────────────

// PortalListSources returns every portal source TV knows about. The
// embedded source is always first if it is running. Bearer tokens
// are stripped via the json:"-" tag on PortalSource.BearerToken.
func (a *App) PortalListSources() ([]PortalSource, error) {
	var out []PortalSource
	if emb := a.embeddedSource(); emb != nil {
		out = append(out, *emb)
	}
	portalSourcesMu.Lock()
	file, err := loadPortalSources()
	portalSourcesMu.Unlock()
	if err != nil {
		return out, err
	}
	out = append(out, file.Remote...)
	return out, nil
}

// PortalActiveSource returns the name of the currently selected
// source. An empty string means the embedded source.
func (a *App) PortalActiveSource() (string, error) {
	portalSourcesMu.Lock()
	defer portalSourcesMu.Unlock()
	file, err := loadPortalSources()
	if err != nil {
		return "", err
	}
	if file.ActiveName == "" {
		return embeddedSourceName, nil
	}
	return file.ActiveName, nil
}

// PortalSetActiveSource records which source the user has selected.
// The Infrastructure-mode rewire reads this on every list/admin call
// to know which client to use.
func (a *App) PortalSetActiveSource(name string) error {
	portalSourcesMu.Lock()
	defer portalSourcesMu.Unlock()
	file, err := loadPortalSources()
	if err != nil {
		return err
	}
	if name == embeddedSourceName {
		file.ActiveName = ""
	} else {
		// Verify it exists.
		found := false
		for _, s := range file.Remote {
			if s.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("portal sources: %q not found", name)
		}
		file.ActiveName = name
	}
	return savePortalSources(file)
}

// PortalRemoveSource deletes a remote source by name. Removing the
// embedded source is forbidden (the embedded portal lives for the
// lifetime of the TV process). If the removed source was active, the
// embedded source becomes active.
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
	if file.ActiveName == name {
		file.ActiveName = ""
	}
	return savePortalSources(file)
}

// PortalDeviceAuthStart begins the device code flow against a remote
// portal. It calls Discover to verify the URL implements protocol
// 1.0, then POST /api/oauth/device to mint a fresh device code, then
// opens VerificationURI (or VerificationURIComplete if present) in
// the system browser. Returns the user code, the verification URL,
// and the device code so the frontend can display the code to the
// user and poll PortalDeviceAuthComplete.
//
// rawURL accepts both bare hostnames and full http(s):// URLs; the
// helper normalizes a missing scheme to https.
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
// the frontend. DeviceCode is opaque to the user but the frontend
// must hold it and pass it back on PortalDeviceAuthComplete.
type PortalDeviceAuthStartResult struct {
	BaseURL         string `json:"baseURL"`
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURI string `json:"verificationURI"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

// PortalDeviceAuthComplete polls /api/oauth/token in a loop until
// the user authorizes the request, denies it, or the device code
// expires. On success it persists a new PortalSource (using sourceName
// as the user-visible label) and marks it active. The poll budget is
// the device code TTL plus a small grace period.
//
// Polling happens server-side (in this Go method) rather than in the
// frontend so the frontend dialog only has to await one promise. The
// trade-off is that the dialog cannot show fine-grained polling
// state, but the device code TTL is short enough (15 minutes) that
// blocking is acceptable.
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
// source. Called from PortalDeviceAuthComplete on the success path.
// A name collision with an existing remote source is treated as an
// upsert: the URL and bearer token are replaced. The new source
// becomes active.
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
			AddedAt:     now,
		})
	}
	file.ActiveName = name
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

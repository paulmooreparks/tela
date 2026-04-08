package main

// portal_embed.go runs an in-process Tela portal alongside the
// TelaVisor desktop app. The portal is the same internal/portal Server
// that telaportal serves as a standalone binary, backed by the same
// file store, listening on a loopback port. A bearer token generated
// at process start guards every call; both the port and the token are
// written to ~/.tela/run/portal-endpoint.json so the TelaVisor frontend
// (and any future local tools that want to talk to it) can find and
// authenticate to it.
//
// This is the foundation for TV's Infrastructure mode rewire onto the
// portal API. Today nothing in TV calls into here -- the embed runs
// alongside the existing direct-hub-admin code path. Subsequent
// commits will move TV's hub admin calls onto the portal client and
// delete the direct path. Spec: tela/DESIGN-portal.md sections 6.2
// and 6.3, and the project memory project_tv_portal_architecture.md.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/paulmooreparks/tela/internal/credstore"
	"github.com/paulmooreparks/tela/internal/portal"
	"github.com/paulmooreparks/tela/internal/portal/store/file"
	"gopkg.in/yaml.v3"
)

// portalEndpoint is the JSON shape of ~/.tela/run/portal-endpoint.json.
// Other local processes (the TV frontend, future tela CLI subcommands,
// scripts) read this file to learn where the embedded portal is and
// what bearer token to present. Mirrors the role of control.json for
// the tela client.
type portalEndpoint struct {
	URL         string `json:"url"`         // http://127.0.0.1:PORT
	BearerToken string `json:"bearerToken"` // present this on the Authorization header
	StorePath   string `json:"storePath"`   // absolute path to portal.yaml
	StartedAt   string `json:"startedAt"`   // RFC 3339 UTC
	Pid         int    `json:"pid"`
}

// portalEmbed holds the running embedded portal: store, server,
// listener, and the bearer token guarding every call. Construct via
// startEmbeddedPortal; cleanup happens via stop() (which the App calls
// from its shutdown hook).
type portalEmbed struct {
	mu          sync.Mutex
	server      *http.Server
	listener    net.Listener
	store       *file.Store
	storePath   string
	endpointURL string
	bearerToken string
}

// embeddedPortalStorePath returns the on-disk YAML path the embedded
// portal reads and writes. Lives next to the rest of the tela config
// (hubs.yaml, profiles/, run/control.json) so all four binaries
// continue to share one config tree.
func embeddedPortalStorePath() string {
	return filepath.Join(telaConfigDir(), "portal.yaml")
}

// embeddedPortalEndpointPath returns the path of the endpoint file.
// Mirrors the control.json convention used by the tela client.
func embeddedPortalEndpointPath() string {
	return filepath.Join(telaConfigDir(), "run", "portal-endpoint.json")
}

// generatePortalBearerToken returns a 64-hex-char random token used
// to authenticate calls to the embedded portal. Generated fresh per
// process start so a previous instance's token cannot reach a new
// instance even if the endpoint file was not cleaned up.
func generatePortalBearerToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// startEmbeddedPortal opens (or initializes) the file store, mints a
// fresh bearer token, installs the token into the store as the auth
// credential, binds a loopback listener, mounts the portal handler,
// writes the endpoint file, and starts serving in a goroutine. The
// returned portalEmbed should be stopped via stop() at shutdown.
func startEmbeddedPortal(ctx context.Context) (*portalEmbed, error) {
	storePath := embeddedPortalStorePath()
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		return nil, fmt.Errorf("portal embed: create config dir: %w", err)
	}

	store, err := file.Open(storePath)
	if err != nil {
		return nil, fmt.Errorf("portal embed: open store %s: %w", storePath, err)
	}

	// Import any hubs the user already had configured under the
	// pre-portal layout (~/.tela/hubs.yaml + credentials.yaml). The
	// import is idempotent: hubs already present in the embedded store
	// are left alone, so re-running TV does not duplicate or overwrite
	// records. This is what makes the rewire of Infrastructure mode
	// onto the portal client friction-free for existing users.
	if n, err := importLegacyHubs(ctx, store); err != nil {
		fmt.Fprintf(os.Stderr, "[telavisor] portal embed: legacy hub import failed: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "[telavisor] portal embed: imported %d hub(s) from legacy config\n", n)
	}

	bearer, err := generatePortalBearerToken()
	if err != nil {
		return nil, fmt.Errorf("portal embed: generate bearer token: %w", err)
	}
	// SetAdminToken installs the token as the file store's bearer
	// credential. Every portal call MUST present this token in the
	// Authorization header; the file store rejects others as
	// ErrUnauthorized. Note that this *replaces* whatever the
	// portal.yaml file may already carry; the embedded portal is
	// always token-gated by the per-process token, not by whatever
	// the operator may have configured for telaportal CLI use.
	if err := store.SetAdminToken(bearer); err != nil {
		return nil, fmt.Errorf("portal embed: install bearer token: %w", err)
	}

	srv := portal.NewServer(store)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("portal embed: bind loopback listener: %w", err)
	}

	endpointURL := "http://" + listener.Addr().String()

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	pe := &portalEmbed{
		server:      httpSrv,
		listener:    listener,
		store:       store,
		storePath:   storePath,
		endpointURL: endpointURL,
		bearerToken: bearer,
	}

	if err := pe.writeEndpointFile(); err != nil {
		listener.Close()
		return nil, err
	}

	go func() {
		if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "[telavisor] portal embed: serve error: %v\n", err)
		}
	}()

	fmt.Fprintf(os.Stderr, "[telavisor] embedded portal: store=%s url=%s\n", storePath, endpointURL)
	return pe, nil
}

// writeEndpointFile writes the endpoint JSON atomically (temp file +
// rename) so a reader cannot observe a partial write. The file is
// 0600 because the bearer token is a credential.
func (p *portalEmbed) writeEndpointFile() error {
	endpointPath := embeddedPortalEndpointPath()
	if err := os.MkdirAll(filepath.Dir(endpointPath), 0o700); err != nil {
		return fmt.Errorf("portal embed: create run dir: %w", err)
	}

	body := portalEndpoint{
		URL:         p.endpointURL,
		BearerToken: p.bearerToken,
		StorePath:   p.storePath,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		Pid:         os.Getpid(),
	}
	data, err := json.MarshalIndent(&body, "", "  ")
	if err != nil {
		return fmt.Errorf("portal embed: marshal endpoint: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(endpointPath), ".portal-endpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("portal embed: create temp endpoint file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("portal embed: write temp endpoint file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal embed: close temp endpoint file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal embed: chmod temp endpoint file: %w", err)
	}
	if err := os.Rename(tmpPath, endpointPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal embed: rename endpoint file: %w", err)
	}
	return nil
}

// stop shuts down the HTTP server and removes the endpoint file.
// Idempotent. Called from App.shutdown so the file does not survive
// the process exit and confuse the next launch.
func (p *portalEmbed) stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.server.Shutdown(shutdownCtx)
		p.server = nil
	}
	if p.listener != nil {
		_ = p.listener.Close()
		p.listener = nil
	}
	_ = os.Remove(embeddedPortalEndpointPath())
}

// importLegacyHubs walks the pre-portal hub config (~/.tela/hubs.yaml
// for the alias-to-URL map and the credstore for per-URL admin tokens)
// and writes anything missing into the embedded portal store. Returns
// the number of new records imported.
//
// Idempotent: hubs already present in the store (matched by name) are
// left alone, so the function is safe to call on every TV launch.
// That is the intended call site: a one-shot import each startup,
// short-circuited as soon as the store has caught up.
//
// The legacy layout has two sources for one hub. hubs.yaml maps a
// short name to a URL; credentials.yaml maps a URL to an admin token.
// We join the two on URL to produce {name, url, adminToken} triples.
// Hubs that have a token but no alias get a synthesized name from the
// URL host. Hubs that have an alias but no token are imported with an
// empty admin token (the user can paste one in later); they will fail
// the admin proxy until a token is supplied but they remain visible
// in the directory, which matches today's TV behavior.
func importLegacyHubs(ctx context.Context, store *file.Store) (int, error) {
	credStore, _ := credstore.Load(credstore.UserPath())

	type legacyHub struct {
		name        string
		url         string
		adminToken  string
		fromAliases bool
	}
	hubs := make(map[string]*legacyHub) // keyed by url

	// hubs.yaml: short name -> URL
	hubsFile := filepath.Join(telaConfigDir(), "hubs.yaml")
	if data, err := os.ReadFile(hubsFile); err == nil {
		var cfg struct {
			Hubs map[string]string `yaml:"hubs"`
		}
		if err := yaml.Unmarshal(data, &cfg); err == nil {
			for name, hubURL := range cfg.Hubs {
				canonical := canonicalHubURL(hubURL)
				hubs[canonical] = &legacyHub{
					name:        name,
					url:         canonical,
					fromAliases: true,
				}
			}
		}
	}

	// credentials.yaml: URL -> token. Joins onto hubs.yaml on URL,
	// creating a fresh entry with a host-derived name when the URL
	// has no alias.
	if credStore != nil {
		for hubURL, cred := range credStore.Hubs {
			canonical := canonicalHubURL(hubURL)
			if existing, ok := hubs[canonical]; ok {
				existing.adminToken = cred.Token
				continue
			}
			hubs[canonical] = &legacyHub{
				name:       hostFromURL(canonical),
				url:        canonical,
				adminToken: cred.Token,
			}
		}
	}

	if len(hubs) == 0 {
		return 0, nil
	}

	// Skip names already present in the embedded store. Listing as
	// the local user is fine: the file store treats every request as
	// the local operator.
	existing, err := store.ListHubsForUser(ctx, file.LocalUser)
	if err != nil {
		return 0, fmt.Errorf("list existing hubs: %w", err)
	}
	have := make(map[string]bool, len(existing))
	for _, h := range existing {
		have[h.Name] = true
	}

	imported := 0
	for _, h := range hubs {
		if h.name == "" || h.url == "" {
			continue
		}
		if have[h.name] {
			continue
		}
		_, _, err := store.AddHub(ctx, file.LocalUser, portal.Hub{
			Name:       h.name,
			URL:        h.url,
			AdminToken: h.adminToken,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[telavisor] portal embed: import hub %q: %v\n", h.name, err)
			continue
		}
		imported++
	}

	// Once everything is migrated, delete the legacy hubs.yaml so the
	// next launch does not re-import. Pre-1.0 policy: no compat shims;
	// the portal store is now the single source of truth for the hub
	// directory. The credstore entries stay for now because the
	// Clients-mode profile path still references them by URL; that
	// migration is a separate concern.
	if imported > 0 || len(hubs) > 0 {
		_ = os.Remove(hubsFile)
	}
	return imported, nil
}

// canonicalHubURL normalizes a hub URL to the http(s) form the portal
// store will use. Legacy TV stores hubs as wss://host or ws://host;
// the portal admin proxy needs https:// or http://. The two are the
// same hub, just different schemes; collapse them so the credstore
// join works regardless of which scheme the user originally typed.
func canonicalHubURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "wss://") {
		raw = "https://" + strings.TrimPrefix(raw, "wss://")
	} else if strings.HasPrefix(raw, "ws://") {
		raw = "http://" + strings.TrimPrefix(raw, "ws://")
	}
	return strings.TrimSuffix(raw, "/")
}

// hostFromURL extracts a host[:port] from a URL for use as a hub
// short name when no alias is configured. Falls back to the raw input
// if parsing fails so the import never produces an empty name.
func hostFromURL(rawURL string) string {
	s := canonicalHubURL(rawURL)
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

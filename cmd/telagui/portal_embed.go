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
	"sync"
	"time"

	"github.com/paulmooreparks/tela/internal/portal"
	"github.com/paulmooreparks/tela/internal/portal/store/file"
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

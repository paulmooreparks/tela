// Package main is the telaportal binary entry point. telaportal is a
// single-user Tela portal: it implements the portal protocol from
// DESIGN-portal.md backed by a YAML file on disk, suitable for an
// individual operator who wants their own directory of hubs without
// running a multi-tenant SaaS portal like Awan Saya.
//
// Almost all the behavior lives in internal/portal and
// internal/portal/store/file. This file is a thin shim that wires the
// two together, mounts the resulting handler on a localhost listener,
// and waits for SIGINT/SIGTERM. The same package layout (cmd shim ->
// internal package) is used by telad and telahubd so the test harness
// in internal/teststack can drive the portal directly without spawning
// a subprocess.
//
// Usage:
//
//	telaportal [-config path] [-listen addr]
//
// Defaults: config is ~/.tela/portal.yaml on Unix and %APPDATA%\tela\portal.yaml
// on Windows; listen is 127.0.0.1:8780. The default listen address is
// loopback-only because the file store has no authentication unless
// the operator has configured an admin token; running on a public
// interface without an admin token would expose the directory to
// anyone who can reach the port.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/paulmooreparks/tela/internal/portal"
	"github.com/paulmooreparks/tela/internal/portal/store/file"
)

// version is set at build time via -ldflags. Production builds set
// this from the channel tag in release.yml; local builds default to
// "dev". The portal binary does not yet wire self-update, so this is
// only used in startup logging today.
var version = "dev"

func main() {
	fs := flag.NewFlagSet("telaportal", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to portal.yaml (default: ~/.tela/portal.yaml)")
	listenAddr := fs.String("listen", "127.0.0.1:8780", "TCP address to listen on")
	_ = fs.Parse(os.Args[1:])

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(telaConfigDir(), "portal.yaml")
	}

	store, err := file.Open(cfgPath)
	if err != nil {
		log.Fatalf("telaportal: open store %s: %v", cfgPath, err)
	}

	srv := portal.NewServer(store)
	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if !store.HasAuth() {
		log.Printf("telaportal: WARNING no admin token configured; serving without authentication. "+
			"Bind to a non-loopback address only after setting an admin token in %s.", cfgPath)
	}
	log.Printf("telaportal %s: store=%s listen=%s", version, cfgPath, *listenAddr)

	// Bring the listener up in a goroutine so the main goroutine can
	// wait on signal delivery.
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("telaportal: received %s, shutting down", sig)
	case err := <-errCh:
		log.Fatalf("telaportal: listen: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("telaportal: shutdown: %v", err)
	}
}

// telaConfigDir returns the platform-appropriate tela config
// directory. Matches the convention used by tela, telad, and
// internal/credstore so all four binaries share one directory tree.
func telaConfigDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "tela")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tela")
}

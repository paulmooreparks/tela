package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
)

// cmdSelfUpdate implements "telachand update". It fetches the channel manifest,
// finds the telachand binary for the current platform, downloads it, verifies
// the SHA-256, and replaces the running executable.
func cmdSelfUpdate(args []string) {
	fs := flag.NewFlagSet("telachand update", flag.ExitOnError)
	configPath := fs.String("config", envOrDefault("TELACHAND_CONFIG", ""), "Config file path (env: TELACHAND_CONFIG)")
	chOverride := fs.String("channel", "", "Override channel for this run. Accepts any valid channel name (dev, beta, stable, or custom).")
	dryRun := fs.Bool("dry-run", false, "Show what would happen without modifying the binary")
	fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		os.Exit(1)
	}

	ch := cfg.Update.Channel
	if *chOverride != "" {
		ch = channel.Normalize(*chOverride)
		if !channel.IsValid(ch) {
			fmt.Fprintf(os.Stderr, "error: invalid channel name %q (use lowercase letters, digits, hyphens)\n", *chOverride)
			os.Exit(1)
		}
	}

	base := cfg.Update.Base

	fetcher := &channel.Fetcher{Base: base}
	m, err := fetcher.Get(ch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: fetch %s manifest: %v\n", ch, err)
		os.Exit(1)
	}

	fmt.Printf("Channel:  %s\n", m.Channel)
	fmt.Printf("Current:  %s\n", version)
	fmt.Printf("Latest:   %s\n", m.Version)

	if m.Version == version && version != "dev" {
		fmt.Println("Already up to date.")
		return
	}

	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	binaryName := fmt.Sprintf("telachand-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)

	entry, ok := m.Binaries[binaryName]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: manifest has no binary named %s\n", binaryName)
		os.Exit(1)
	}

	dlURL := m.BinaryURL(binaryName)
	fmt.Printf("Download: %s (%d bytes)\n", dlURL, entry.Size)

	if *dryRun {
		fmt.Println("Dry run: no changes made.")
		return
	}

	if err := downloadAndReplace(dlURL, entry.SHA256, entry.Size); err != nil {
		fmt.Fprintf(os.Stderr, "error: update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK: telachand updated to %s. Restart the process for the new binary.\n", m.Version)
}

// downloadAndReplace downloads the binary at dlURL, verifies its SHA-256 and
// size, then replaces the running executable with the new binary.
func downloadAndReplace(dlURL, sha256hex string, size int64) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(dlURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	// Write to a temp file in the same directory so the rename is atomic
	// (same filesystem volume).
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, "telachand-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := channel.VerifyReader(tmp, resp.Body, sha256hex, size); err != nil {
		tmp.Close()
		return fmt.Errorf("verify: %w", err)
	}
	tmp.Close()

	if runtime.GOOS != "windows" {
		os.Chmod(tmpPath, 0755)
	}

	if runtime.GOOS == "windows" {
		oldPath := exe + ".old"
		os.Remove(oldPath)
		if err := os.Rename(exe, oldPath); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}
		if err := os.Rename(tmpPath, exe); err != nil {
			os.Rename(oldPath, exe) // rollback
			return fmt.Errorf("install new binary: %w", err)
		}
		go func() {
			for range 10 {
				if os.Remove(oldPath) == nil {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
	} else {
		if err := os.Rename(tmpPath, exe); err != nil {
			return fmt.Errorf("install new binary: %w", err)
		}
	}

	log.Printf("updated telachand binary at %s", exe)
	return nil
}

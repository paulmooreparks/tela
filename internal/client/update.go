// update.go -- "tela update" CLI subcommand
//
// Self-updates the on-disk tela binary from the channel manifest. Mirrors
// 'telad update' and 'telahubd update' so every Tela binary has the same
// one-command self-update story.
//
// On Windows the running exe cannot be overwritten in place, so the
// staging file is renamed into a sibling .old before the new binary is
// moved into position; the .old file is removed best-effort on the next
// invocation. On Unix the rename is atomic.

package client

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
)

func cmdUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	chOverride := fs.String("channel", "", "Override channel for this run (dev|beta|stable). Does not modify the persistent preference.")
	dryRun := fs.Bool("dry-run", false, "Show what would be downloaded without modifying the binary")
	fs.Parse(args)

	// Resolve channel: -channel override > stored preference > default.
	ch, base := loadClientChannel()
	if *chOverride != "" {
		ch = channel.Normalize(*chOverride)
		if !channel.IsKnown(ch) {
			fmt.Fprintf(os.Stderr, "Error: unknown channel %q (expected dev|beta|stable)\n", *chOverride)
			os.Exit(1)
		}
	}

	fetcher := &channel.Fetcher{Base: base}
	m, err := fetcher.Get(ch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: fetch %s manifest: %v\n", ch, err)
		os.Exit(1)
	}

	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	binaryName := fmt.Sprintf("tela-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
	entry, ok := m.Binaries[binaryName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s manifest %s has no binary named %s\n", ch, m.Version, binaryName)
		os.Exit(1)
	}

	fmt.Printf("Channel:  %s\n", m.Channel)
	fmt.Printf("Current:  %s\n", version)
	fmt.Printf("Latest:   %s\n", m.Version)

	if m.Version == version && version != "dev" {
		fmt.Println("Already up to date.")
		return
	}

	if *dryRun {
		fmt.Printf("Dry run: would download %s and replace the running binary.\n", binaryName)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot find executable path: %v\n", err)
		os.Exit(1)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot resolve executable path: %v\n", err)
		os.Exit(1)
	}

	dlURL := m.BinaryURL(binaryName)
	fmt.Printf("Source:   %s\n", dlURL)
	fmt.Printf("Size:     %d bytes\n", entry.Size)
	fmt.Printf("Expected: sha256:%s\n", entry.SHA256)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(dlURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: download: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: download returned HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".tela-update-*.tmp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: create temp file: %v\n", err)
		os.Exit(1)
	}
	tmpPath := tmp.Name()
	cleanup := func() { os.Remove(tmpPath) }

	if err := channel.VerifyReader(tmp, resp.Body, entry.SHA256, entry.Size); err != nil {
		tmp.Close()
		cleanup()
		fmt.Fprintf(os.Stderr, "Error: verify download: %v\n", err)
		os.Exit(1)
	}
	tmp.Close()
	if runtime.GOOS != "windows" {
		os.Chmod(tmpPath, 0o755)
	}

	if runtime.GOOS == "windows" {
		// Can't overwrite a running .exe; rename it aside first.
		oldPath := exe + ".old"
		os.Remove(oldPath)
		if err := os.Rename(exe, oldPath); err != nil {
			cleanup()
			fmt.Fprintf(os.Stderr, "Error: rename current binary: %v\n", err)
			os.Exit(1)
		}
		if err := os.Rename(tmpPath, exe); err != nil {
			// Rollback so the user is not left with no tela.exe.
			os.Rename(oldPath, exe)
			cleanup()
			fmt.Fprintf(os.Stderr, "Error: install new binary: %v\n", err)
			os.Exit(1)
		}
		// Clean up the .old file in the background. The OS may keep it
		// open briefly while the current process winds down; retry a
		// few times then give up. Cosmetic only.
		go func(p string) {
			for range 10 {
				if os.Remove(p) == nil {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}(oldPath)
	} else {
		if err := os.Rename(tmpPath, exe); err != nil {
			cleanup()
			fmt.Fprintf(os.Stderr, "Error: install new binary: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("OK: tela updated to %s\n", m.Version)
}

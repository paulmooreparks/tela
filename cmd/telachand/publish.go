package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
)

// cmdPublish implements "telachand publish". It scans the files/ subdirectory
// of the data directory, computes SHA-256 and size for every file, and writes
// a channel manifest JSON to {data}/{channel}.json.
func cmdPublish(args []string) {
	fs := flag.NewFlagSet("telachand publish", flag.ExitOnError)
	channelName := fs.String("channel", "dev", "Channel to publish (dev, beta, stable)")
	tag := fs.String("tag", "", "Release tag written into the manifest (e.g. v0.10.0-dev.1)")
	baseURL := fs.String("base-url", "", "Override the download base URL (default: publicURL from config)")
	configPath := fs.String("config", envOrDefault("TELACHAND_CONFIG", ""), "Config file path (env: TELACHAND_CONFIG)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: telachand publish -channel <name> -tag <tag> [-base-url <url>] [-config <path>]")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *tag == "" {
		fmt.Fprintln(os.Stderr, "error: -tag is required")
		fs.Usage()
		os.Exit(1)
	}
	if !channel.IsValid(*channelName) {
		fmt.Fprintf(os.Stderr, "error: invalid channel name %q (use lowercase letters, digits, hyphens)\n", *channelName)
		os.Exit(1)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Resolve the download base URL. The manifest's DownloadBase is the URL
	// prefix that clients append a binary file name to in order to download.
	// We always point it at this server's /files/ path.
	publicBase := *baseURL
	if publicBase == "" {
		publicBase = cfg.PublicURL
	}
	if publicBase == "" {
		fmt.Fprintln(os.Stderr, "error: set publicURL in the config file or pass -base-url")
		os.Exit(1)
	}
	downloadBase := strings.TrimRight(publicBase, "/") + "/files/"

	filesDir := filepath.Join(cfg.Data, "files")
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		log.Fatalf("read files dir %s: %v", filesDir, err)
	}

	binaries := make(map[string]channel.BinaryEntry)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(filesDir, name)
		sha, size, err := hashFile(path)
		if err != nil {
			log.Fatalf("hash %s: %v", name, err)
		}
		binaries[name] = channel.BinaryEntry{SHA256: sha, Size: size}
		fmt.Printf("  %-44s  %s...  %d bytes\n", name, sha[:16], size)
	}

	if len(binaries) == 0 {
		log.Fatalf("no files found in %s", filesDir)
	}

	m := channel.Manifest{
		Channel:      *channelName,
		Version:      *tag,
		Tag:          *tag,
		PublishedAt:  time.Now().UTC(),
		DownloadBase: downloadBase,
		Binaries:     binaries,
	}
	if err := m.Validate(); err != nil {
		log.Fatalf("manifest validation failed: %v", err)
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		log.Fatalf("marshal manifest: %v", err)
	}
	out = append(out, '\n')

	if err := os.MkdirAll(cfg.Data, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	manifestPath := filepath.Join(cfg.Data, *channelName+".json")
	if err := os.WriteFile(manifestPath, out, 0644); err != nil {
		log.Fatalf("write manifest: %v", err)
	}

	fmt.Printf("\npublished %s channel manifest\n", *channelName)
	fmt.Printf("  tag:      %s\n", m.Tag)
	fmt.Printf("  binaries: %d\n", len(m.Binaries))
	fmt.Printf("  base:     %s\n", m.DownloadBase)
	fmt.Printf("  manifest: %s\n", manifestPath)
}

// hashFile computes the SHA-256 and byte size of the file at path.
func hashFile(path string) (sha256hex string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err = io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

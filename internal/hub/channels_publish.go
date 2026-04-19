// channels_publish.go -- "telahubd channels publish" CLI subcommand.
//
// Scans the files/ subdirectory of channels.data, computes SHA-256 and
// size for each binary, and writes a channel manifest JSON to
// {data}/{channel}.json. Identical wire format to the GitHub-hosted
// channel manifests and to what telachand publish produced.

package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
	"github.com/paulmooreparks/tela/internal/service"
)

// cmdChannels dispatches "telahubd channels <subcommand>". Today the only
// subcommand is "publish", but the wrapper exists so we can add list /
// remove / verify operations later without breaking the CLI shape.
func cmdChannels(args []string) {
	if len(args) == 0 {
		printChannelsUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "publish":
		cmdChannelsPublish(args[1:])
	case "help", "-h", "-?", "-help", "--help":
		printChannelsUsage()
	default:
		fmt.Fprintf(os.Stderr, "telahubd channels: unknown subcommand %q\n\n", args[0])
		printChannelsUsage()
		os.Exit(2)
	}
}

func printChannelsUsage() {
	fmt.Fprint(os.Stderr, `telahubd channels - Self-hosted release channel server

Usage:
  telahubd channels publish -channel <name> -tag <tag> [-base-url <url>] [-config <path>]

Subcommands:
  publish     Scan channels.data/files/ and write {channel}.json

Run 'telahubd channels <subcommand> -h' for subcommand-specific help.
`)
}

// cmdChannelsPublish implements `telahubd channels publish`. It loads the
// hub's config to find channels.data and channels.publicURL, scans
// {data}/files/, hashes every file, and writes {data}/{channel}.json.
func cmdChannelsPublish(args []string) {
	fs := flag.NewFlagSet("telahubd channels publish", flag.ExitOnError)
	channelName := fs.String("channel", "dev", "Channel to publish (dev, beta, stable, or any custom name)")
	tag := fs.String("tag", "", "Release tag written into the manifest (e.g. v0.12.0-dev.1)")
	baseURL := fs.String("base-url", "", "Override the download base URL (default: channels.publicURL from config)")
	configPath := fs.String("config", "", "Path to telahubd YAML config (default: platform-standard path)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: telahubd channels publish -channel <name> -tag <tag> [-base-url <url>] [-config <path>]")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *tag == "" {
		fmt.Fprintln(os.Stderr, "error: -tag is required")
		fs.Usage()
		os.Exit(1)
	}
	if !channel.IsValid(*channelName) {
		fmt.Fprintf(os.Stderr, "error: invalid channel name %q (lowercase letters, digits, hyphens only)\n", *channelName)
		os.Exit(1)
	}

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = service.BinaryConfigPath("telahubd")
	}
	cfg, err := loadHubConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config %s: %v\n", cfgPath, err)
		os.Exit(1)
	}

	if !cfg.Channels.Enabled {
		fmt.Fprintf(os.Stderr, "warning: channels.enabled is false in %s; the published manifest will exist on disk but will not be served until you enable channel hosting\n", cfgPath)
	}
	dataDir := cfg.Channels.Data
	if dataDir == "" {
		fmt.Fprintf(os.Stderr, "error: channels.data is not set in %s\n", cfgPath)
		os.Exit(1)
	}

	// Resolve the download base URL. Manifests embed this as the URL
	// prefix that clients append a binary file name to in order to fetch
	// it. We always point it at this hub's /channels/files/ path.
	publicBase := *baseURL
	if publicBase == "" {
		publicBase = cfg.Channels.PublicURL
	}
	if publicBase == "" {
		fmt.Fprintln(os.Stderr, "error: set channels.publicURL in the config or pass -base-url")
		os.Exit(1)
	}
	downloadBase := strings.TrimRight(publicBase, "/") + "/files/"

	filesDir := filepath.Join(dataDir, "files")
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read files dir %s: %v\n", filesDir, err)
		os.Exit(1)
	}

	binaries := make(map[string]channel.BinaryEntry)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(filesDir, name)
		sha, size, err := hashChannelFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: hash %s: %v\n", name, err)
			os.Exit(1)
		}
		binaries[name] = channel.BinaryEntry{SHA256: sha, Size: size}
		fmt.Printf("  %-44s  %s...  %d bytes\n", name, sha[:16], size)
	}

	if len(binaries) == 0 {
		fmt.Fprintf(os.Stderr, "error: no files found in %s\n", filesDir)
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "error: manifest validation failed: %v\n", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: marshal manifest: %v\n", err)
		os.Exit(1)
	}
	out = append(out, '\n')

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error: create data dir: %v\n", err)
		os.Exit(1)
	}
	manifestPath := filepath.Join(dataDir, *channelName+".json")
	if err := os.WriteFile(manifestPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write manifest: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\npublished %s channel manifest\n", *channelName)
	fmt.Printf("  tag:      %s\n", m.Tag)
	fmt.Printf("  binaries: %d\n", len(m.Binaries))
	fmt.Printf("  base:     %s\n", m.DownloadBase)
	fmt.Printf("  manifest: %s\n", manifestPath)
}

// hashChannelFile computes the SHA-256 and byte size of the file at path.
func hashChannelFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

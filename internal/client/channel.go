// channel.go -- "tela channel" subcommand
//
// Reads or writes the tela client's own release channel preference. The
// preference is stored in the user credential store (~/.tela/credentials.yaml
// on Unix, %APPDATA%\tela\credentials.yaml on Windows) so it persists
// across invocations without a separate config file.
//
// Hub and agent channels are managed separately via `tela admin hub channel`
// and `tela admin agent channel`.

package client

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
	"github.com/paulmooreparks/tela/internal/cliflag"
	"github.com/paulmooreparks/tela/internal/credstore"
)

func cmdChannel(args []string) {
	// Dispatch on the first positional arg. Keeping flag parsing in the
	// per-subcommand handlers means `tela channel -h` and future flags
	// work uniformly without a top-level fs.Parse that would reject
	// anything not pre-registered.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "set":
			setClientChannel(args[1:])
			return
		case "show":
			showChannelManifest(args[1:])
			return
		case "download":
			downloadChannelBinary(args[1:])
			return
		case "sources":
			clientChannelSources(args[1:])
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown channel command: %s\n\n", args[0])
			printChannelUsage()
			os.Exit(1)
		}
	}
	// No subcommand. Help flags are consumed by showClientChannel.
	showClientChannel(args)
}

// clientChannelSources dispatches the `tela channel sources [list|set|remove]`
// subcommand. With no further arg, equivalent to `list`.
func clientChannelSources(args []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "list":
			listClientSources(args[1:])
			return
		case "set":
			setClientSource(args[1:])
			return
		case "remove":
			removeClientSource(args[1:])
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown sources command: %s\n\n", args[0])
			printChannelUsage()
			os.Exit(1)
		}
	}
	listClientSources(args)
}

// listClientSources prints every channel name the client knows about, with
// its resolved base URL and whether that came from the baked-in defaults,
// a sources-map override, or a custom (non-built-in) entry.
func listClientSources(args []string) {
	fs := flag.NewFlagSet("channel sources", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	fs.Parse(args)
	if wantHelp() {
		printChannelUsage()
		return
	}
	store, err := credstore.Load(credstore.UserPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load credential store: %v\n", err)
		os.Exit(1)
	}
	seen := map[string]bool{}
	fmt.Printf("%-15s  %s\n", "CHANNEL", "BASE URL")
	for _, name := range []string{channel.Dev, channel.Beta, channel.Stable} {
		base := channel.ResolveBase(name, store.Update.Sources)
		suffix := "  (built-in default)"
		if v, ok := store.Update.Sources[name]; ok && v != "" {
			suffix = "  (override)"
			_ = v
		}
		fmt.Printf("%-15s  %s%s\n", name, base, suffix)
		seen[name] = true
	}
	// Sort custom keys for stable output.
	var customNames []string
	for name := range store.Update.Sources {
		if !seen[name] {
			customNames = append(customNames, name)
		}
	}
	for i := 1; i < len(customNames); i++ {
		for j := i; j > 0 && customNames[j-1] > customNames[j]; j-- {
			customNames[j-1], customNames[j] = customNames[j], customNames[j-1]
		}
	}
	for _, name := range customNames {
		fmt.Printf("%-15s  %s  (custom)\n", name, store.Update.Sources[name])
	}
}

// setClientSource adds or overrides a per-channel source URL in the
// client credential store.
func setClientSource(args []string) {
	fs := flag.NewFlagSet("channel sources set", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	fs.Parse(permuteArgs(fs, args))
	if wantHelp() {
		printChannelUsage()
		return
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Error: 'sources set' requires <name> <url>")
		os.Exit(1)
	}
	name := strings.TrimSpace(strings.ToLower(fs.Arg(0)))
	if !channel.IsValid(name) {
		fmt.Fprintf(os.Stderr, "Error: invalid channel name %q (use lowercase letters, digits, hyphens)\n", name)
		os.Exit(1)
	}
	base := strings.TrimRight(strings.TrimSpace(fs.Arg(1)), "/")
	// Allow operators to paste a full manifest URL by accident.
	if strings.HasSuffix(base, ".json") {
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[:i]
		}
	}
	path := credstore.UserPath()
	store, err := credstore.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load credential store: %v\n", err)
		os.Exit(1)
	}
	if store.Update.Sources == nil {
		store.Update.Sources = map[string]string{}
	}
	store.Update.Sources[name] = base
	if err := store.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save credential store: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Set source for channel %s: %s\n", name, base)
}

// removeClientSource removes a per-channel source URL from the client
// credential store. Built-in channel names continue to work via baked-in
// defaults; custom names become unresolvable and the next operation
// against them errors with guidance.
func removeClientSource(args []string) {
	fs := flag.NewFlagSet("channel sources remove", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	fs.Parse(permuteArgs(fs, args))
	if wantHelp() {
		printChannelUsage()
		return
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: 'sources remove' requires <name>")
		os.Exit(1)
	}
	name := strings.TrimSpace(strings.ToLower(fs.Arg(0)))
	path := credstore.UserPath()
	store, err := credstore.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load credential store: %v\n", err)
		os.Exit(1)
	}
	if _, exists := store.Update.Sources[name]; !exists {
		fmt.Fprintf(os.Stderr, "Error: no source entry for channel %q\n", name)
		os.Exit(1)
	}
	if name == channel.Normalize(store.Update.Channel) && !channel.IsKnown(name) {
		fmt.Fprintf(os.Stderr, "Note: %q is the currently selected channel and has no baked-in default; updates will fail until you set a source for it or switch channels.\n", name)
	}
	delete(store.Update.Sources, name)
	if err := store.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save credential store: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed source for channel %s\n", name)
}

func printChannelUsage() {
	fmt.Fprintf(os.Stderr, `tela channel -- client release channel

Usage:
  tela channel                             Show the current channel and latest version on it
  tela channel set <channel>               Switch the client's release channel (dev, beta, stable, or custom)
  tela channel set <ch> -manifest-base URL Override the upstream manifest URL prefix
  tela channel show [-channel <ch>]        Print the parsed channel manifest
  tela channel download <binary> [opts]    Download and verify a binary from the channel manifest
  tela channel sources [list]              List known channel sources (built-ins + overrides + custom)
  tela channel sources set <name> <url>    Add or override a per-channel base URL
  tela channel sources remove <name>       Remove a per-channel base URL

Download options:
  -channel <ch>      Channel to download from (default: client's configured channel)
  -o <path>          Output path (default: ./<binary>)
  -force             Overwrite the output path if it exists

Help:
  -h, -?, -help      Show this help (works after any subcommand too, e.g. "tela channel set -h").

Examples:
  tela channel download telad-linux-amd64
  tela channel download telahubd-linux-amd64 -channel beta -o /usr/local/bin/telahubd
  tela channel download telavisor-windows-amd64-setup.exe -channel stable

The preference is stored in %s.
Hub and agent channels are managed separately via:
  tela admin hub channel [set <ch>]
  tela admin agent channel -machine <id> [set <ch>]
`, credstore.UserPath())
}

// loadClientChannel returns the configured channel name and the resolved
// base URL from the user credential store. The base is looked up via
// channel.ResolveBase against the credstore's sources map, falling back
// to channel.DefaultBases for built-in channel names.
func loadClientChannel() (string, string) {
	store, err := credstore.Load(credstore.UserPath())
	if err != nil || store == nil {
		name := channel.DefaultChannel
		return name, channel.ResolveBase(name, nil)
	}
	name := channel.Normalize(store.Update.Channel)
	return name, channel.ResolveBase(name, store.Update.Sources)
}

func showClientChannel(args []string) {
	fs := flag.NewFlagSet("tela channel", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	fs.Parse(permuteArgs(fs, args))

	if wantHelp() {
		printChannelUsage()
		return
	}

	ch, base := loadClientChannel()
	manifestURL := channel.ManifestURL(base, ch)
	fmt.Printf("  channel:         %s\n", ch)
	fmt.Printf("  manifest:        %s\n", manifestURL)
	fmt.Printf("  current version: %s\n", version)

	fetcher := &channel.Fetcher{Base: base}
	m, err := fetcher.Get(ch)
	if err != nil {
		fmt.Printf("  latest version:  unavailable (%s)\n", err)
		return
	}
	state := "up to date"
	if m.Version != version && version != "dev" {
		state = "update available"
	}
	fmt.Printf("  latest version:  %s  (%s)\n", m.Version, state)
}

func setClientChannel(args []string) {
	fs := flag.NewFlagSet("channel set", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	manifestBase := fs.String("manifest-base", "", "Override the upstream manifest URL prefix")
	fs.Parse(permuteArgs(fs, args))

	if wantHelp() {
		printChannelUsage()
		return
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: 'set' requires a channel name (dev, beta, stable, or a custom channel)")
		os.Exit(1)
	}
	name := strings.TrimSpace(strings.ToLower(fs.Arg(0)))
	if !channel.IsValid(name) {
		fmt.Fprintf(os.Stderr, "Error: invalid channel name %q (use lowercase letters, digits, hyphens)\n", name)
		os.Exit(1)
	}

	path := credstore.UserPath()
	store, err := credstore.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load credential store: %v\n", err)
		os.Exit(1)
	}
	store.Update.Channel = name
	if *manifestBase != "" {
		base := strings.TrimRight(*manifestBase, "/")
		// If the user passed a full manifest URL (e.g. .../channels/local.json)
		// instead of just the base directory, strip the filename component so
		// ManifestURL doesn't double it.
		if strings.HasSuffix(base, ".json") {
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[:i]
			}
		}
		if store.Update.Sources == nil {
			store.Update.Sources = map[string]string{}
		}
		store.Update.Sources[name] = base
	}
	if err := store.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save credential store: %v\n", err)
		os.Exit(1)
	}

	resolved := channel.ResolveBase(name, store.Update.Sources)
	fmt.Printf("Client channel set to %s\n", name)
	fmt.Printf("  manifest: %s\n", channel.ManifestURL(resolved, name))
}

// resolveChannel returns the channel name (with command-line override) and
// the manifest base URL configured in the credential store. Used by both
// `show` and `download` so they share the same channel-resolution rules.
func resolveChannel(override string) (string, string) {
	cur, base := loadClientChannel()
	if override != "" {
		cur = channel.Normalize(override)
	}
	return cur, base
}

func showChannelManifest(args []string) {
	fs := flag.NewFlagSet("channel show", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	chName := fs.String("channel", "", "Channel to show (default: client's configured channel)")
	fs.Parse(permuteArgs(fs, args))

	if wantHelp() {
		printChannelUsage()
		return
	}

	ch, base := resolveChannel(*chName)
	fetcher := &channel.Fetcher{Base: base}
	m, err := fetcher.Get(ch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: fetch %s manifest: %v\n", ch, err)
		os.Exit(1)
	}

	fmt.Printf("Channel:     %s\n", m.Channel)
	fmt.Printf("Version:     %s\n", m.Version)
	fmt.Printf("Tag:         %s\n", m.Tag)
	fmt.Printf("Published:   %s\n", m.PublishedAt.Format(time.RFC3339))
	fmt.Printf("Source:      %s\n", channel.ManifestURL(base, ch))
	fmt.Println()
	fmt.Println("Binaries:")
	// Sort the names for stable output.
	names := make([]string, 0, len(m.Binaries))
	for name := range m.Binaries {
		names = append(names, name)
	}
	// Simple insertion sort -- few entries, no point importing sort just for this.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	for _, name := range names {
		b := m.Binaries[name]
		fmt.Printf("  %-40s  %12d bytes  sha256:%s\n", name, b.Size, b.SHA256)
	}
}

func downloadChannelBinary(args []string) {
	fs := flag.NewFlagSet("channel download", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	chName := fs.String("channel", "", "Channel to download from (default: client's configured channel)")
	out := fs.String("o", "", "Output path (default: ./<binary>)")
	force := fs.Bool("force", false, "Overwrite the output path if it exists")
	fs.Parse(permuteArgs(fs, args))

	if wantHelp() {
		printChannelUsage()
		return
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: 'download' requires a binary name (e.g. telad-linux-amd64)")
		fmt.Fprintln(os.Stderr, "Run 'tela channel show' to list available binaries on the current channel.")
		os.Exit(1)
	}
	binaryName := fs.Arg(0)

	ch, base := resolveChannel(*chName)
	fetcher := &channel.Fetcher{Base: base}
	m, err := fetcher.Get(ch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: fetch %s manifest: %v\n", ch, err)
		os.Exit(1)
	}
	entry, ok := m.Binaries[binaryName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s manifest %s has no binary named %q\n", ch, m.Version, binaryName)
		fmt.Fprintln(os.Stderr, "Run 'tela channel show' to list available binaries.")
		os.Exit(1)
	}

	dest := *out
	if dest == "" {
		dest = binaryName
	}
	if _, err := os.Stat(dest); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "Error: %s already exists (use -force to overwrite)\n", dest)
		os.Exit(1)
	}

	dlURL := m.BinaryURL(binaryName)
	fmt.Printf("Channel:   %s\n", m.Channel)
	fmt.Printf("Version:   %s\n", m.Version)
	fmt.Printf("Source:    %s\n", dlURL)
	fmt.Printf("Size:      %d bytes\n", entry.Size)
	fmt.Printf("Expected:  sha256:%s\n", entry.SHA256)
	fmt.Println()

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("GET", dlURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: build request: %v\n", err)
		os.Exit(1)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: download: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: download returned HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	// Write to a sibling tmp file in the destination's directory so the
	// rename is atomic on the same filesystem. The verify step writes
	// into the tmp file via VerifyReader and we only rename on success.
	dir := filepath.Dir(dest)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".tela-download-*.tmp")
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

	// Best-effort exec bit on Unix when downloading something that's not
	// obviously not an executable.
	if runtime.GOOS != "windows" && !strings.HasSuffix(binaryName, ".deb") &&
		!strings.HasSuffix(binaryName, ".rpm") && !strings.HasSuffix(binaryName, ".tar.gz") {
		os.Chmod(tmpPath, 0o755)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		// On Windows, an existing destination may need to be removed first.
		if *force {
			os.Remove(dest)
			if err2 := os.Rename(tmpPath, dest); err2 == nil {
				err = nil
			}
		}
		if err != nil {
			cleanup()
			fmt.Fprintf(os.Stderr, "Error: install to %s: %v\n", dest, err)
			os.Exit(1)
		}
	}

	fmt.Printf("OK: %s downloaded to %s (sha256 verified)\n", m.Version, dest)
}

// channel.go -- "telahubd channel" subcommand
//
// Reads or writes the hub's release channel preference. The preference is
// stored in the hub's YAML config file (telahubd.yaml) under the "update"
// key, mirroring the structure used by "telad channel" in the agent package
// and "tela channel" in the client credential store.
//
// The -config flag defaults to the platform-standard path
// (service.BinaryConfigPath), so an operator rarely needs to pass it
// explicitly. Channel management via the hub admin API
// (PATCH /api/admin/update) is also available.

package hub

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
	"github.com/paulmooreparks/tela/internal/cliflag"
	"github.com/paulmooreparks/tela/internal/service"
)

func cmdHubChannel(args []string) {
	// Dispatch on the first positional arg. We intentionally do NOT parse
	// flags at this level, because `telahubd channel -config <path>` should
	// flow into the default (show) handler whose FlagSet knows -config;
	// a top-level fs.Parse would die on unknown flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "set":
			setHubChannel(args[1:])
			return
		case "show":
			showHubChannelManifest(args[1:])
			return
		case "sources":
			hubChannelSources(args[1:])
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown channel command: %s\n\n", args[0])
			printHubChannelUsage()
			os.Exit(1)
		}
	}
	// No subcommand. Help flags and -config are consumed by showHubChannel.
	showHubChannel(args)
}

// hubChannelSources dispatches `telahubd channel sources [list|set|remove]`.
func hubChannelSources(args []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "list":
			listHubSources(args[1:])
			return
		case "set":
			setHubSource(args[1:])
			return
		case "remove":
			removeHubSource(args[1:])
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown sources command: %s\n\n", args[0])
			printHubChannelUsage()
			os.Exit(1)
		}
	}
	listHubSources(args)
}

// listHubSources prints every channel name the hub knows about with the
// resolved base URL and origin (built-in default / override / custom).
func listHubSources(args []string) {
	fs := flag.NewFlagSet("telahubd channel sources", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", "", "Path to telahubd.yaml (default: platform-standard path)")
	fs.Parse(hubPermuteArgs(fs, args))
	if wantHelp() {
		printHubChannelUsage()
		return
	}
	cfg, _, err := loadHubChannelConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config: %v\n", err)
		os.Exit(1)
	}
	sources := cfg.Update.Sources
	seen := map[string]bool{}
	fmt.Printf("%-15s  %s\n", "CHANNEL", "BASE URL")
	for _, name := range []string{channel.Dev, channel.Beta, channel.Stable} {
		base := channel.ResolveBase(name, sources)
		suffix := "  (built-in default)"
		if v, ok := sources[name]; ok && v != "" {
			suffix = "  (override)"
			_ = v
		}
		fmt.Printf("%-15s  %s%s\n", name, base, suffix)
		seen[name] = true
	}
	var customNames []string
	for name := range sources {
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
		fmt.Printf("%-15s  %s  (custom)\n", name, sources[name])
	}
}

// setHubSource writes a per-channel base URL into the hub's YAML.
func setHubSource(args []string) {
	fs := flag.NewFlagSet("telahubd channel sources set", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", "", "Path to telahubd.yaml (default: platform-standard path)")
	fs.Parse(hubPermuteArgs(fs, args))
	if wantHelp() {
		printHubChannelUsage()
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
	if strings.HasSuffix(base, ".json") {
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[:i]
		}
	}
	cfg, path, err := loadHubChannelConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", path, err)
		os.Exit(1)
	}
	if cfg.Update.Sources == nil {
		cfg.Update.Sources = map[string]string{}
	}
	cfg.Update.Sources[name] = base
	if err := writeHubConfig(path, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write config %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("Set source for channel %s: %s\n", name, base)
	fmt.Printf("  config: %s\n", path)
}

// removeHubSource removes a per-channel base URL from the hub's YAML.
func removeHubSource(args []string) {
	fs := flag.NewFlagSet("telahubd channel sources remove", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", "", "Path to telahubd.yaml (default: platform-standard path)")
	fs.Parse(hubPermuteArgs(fs, args))
	if wantHelp() {
		printHubChannelUsage()
		return
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: 'sources remove' requires <name>")
		os.Exit(1)
	}
	name := strings.TrimSpace(strings.ToLower(fs.Arg(0)))
	cfg, path, err := loadHubChannelConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", path, err)
		os.Exit(1)
	}
	if _, exists := cfg.Update.Sources[name]; !exists {
		fmt.Fprintf(os.Stderr, "Error: no source entry for channel %q\n", name)
		os.Exit(1)
	}
	if name == channel.Normalize(cfg.Update.Channel) && !channel.IsKnown(name) {
		fmt.Fprintf(os.Stderr, "Note: %q is the currently selected channel and has no baked-in default; updates will fail until you set a source for it or switch channels.\n", name)
	}
	delete(cfg.Update.Sources, name)
	if err := writeHubConfig(path, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write config %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("Removed source for channel %s\n", name)
}

func printHubChannelUsage() {
	fmt.Fprintf(os.Stderr, `telahubd channel -- hub release channel

Usage:
  telahubd channel [-config <path>]                            Show the current channel and latest version
  telahubd channel set <channel> [-config <path>]              Switch the hub's release channel
  telahubd channel set <ch> -manifest-base URL                 Override the upstream manifest URL prefix
  telahubd channel show [-channel <ch>]                        Print the full parsed channel manifest
  telahubd channel sources [list] [-config <path>]             List known channel sources
  telahubd channel sources set <name> <url> [-config <path>]   Add or override a per-channel base URL
  telahubd channel sources remove <name> [-config <path>]      Remove a per-channel base URL

Options:
  -config <path>      Path to telahubd.yaml. Defaults to %s.
  -manifest-base URL  Base URL for a self-hosted channel server.
                      Provide the directory URL; the channel name and .json
                      are appended automatically (e.g. https://example.com/channels).

Help:
  -h, -?, -help       Show this help. Works after any subcommand too
                      (e.g. "telahubd channel set -h").

The preference is stored in the hub's YAML config file under "update.channel".
Channel changes can also be applied remotely over the admin API:
  PATCH /api/admin/update   {"channel":"beta"}
`, service.BinaryConfigPath("telahubd"))
}

// loadHubChannelConfig reads the hub config from the given path, falling
// back to the platform-standard path when pathOverride is empty. Returns
// (cfg, effectivePath, err).
func loadHubChannelConfig(pathOverride string) (*hubConfig, string, error) {
	path := pathOverride
	if path == "" {
		path = service.BinaryConfigPath("telahubd")
	}
	cfg, err := loadHubConfig(path)
	if err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}

// showHubChannel prints a short summary: current channel, manifest URL,
// current version, and latest-published version on that channel. When
// pathOverride is empty it reads the platform-standard config file.
func showHubChannel(args []string) {
	fs := flag.NewFlagSet("channel", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", "", "Path to telahubd.yaml (default: platform-standard path)")
	fs.Parse(hubPermuteArgs(fs, args))

	if wantHelp() {
		printHubChannelUsage()
		return
	}

	cfg, path, err := loadHubChannelConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", path, err)
		os.Exit(1)
	}

	// Stash the loaded config globally so hubChannel() can read from it.
	globalCfgMu.Lock()
	globalCfg = cfg
	globalCfgMu.Unlock()

	ch, base := hubChannel()
	fmt.Printf("  channel:         %s\n", ch)
	fmt.Printf("  manifest:        %s\n", channel.ManifestURL(base, ch))
	fmt.Printf("  current version: %s\n", version)

	m, err := hubChannelFetcher.GetURL(channel.ManifestURL(base, ch))
	if err != nil {
		fmt.Printf("  latest version:  unavailable (%s)\n", err)
		return
	}
	state := "up to date"
	if channel.ShouldOfferUpdate(version, m.Channel, m.Version) {
		state = "update available"
	} else if m.Version != version && version != "dev" {
		state = "ahead of channel HEAD"
	}
	fmt.Printf("  latest version:  %s  (%s)\n", m.Version, state)
}

func setHubChannel(args []string) {
	fs := flag.NewFlagSet("telahubd channel set", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", "", "Path to telahubd.yaml (default: platform-standard path)")
	manifestBase := fs.String("manifest-base", "", "Override the upstream manifest URL prefix")
	fs.Parse(hubPermuteArgs(fs, args))

	if wantHelp() {
		printHubChannelUsage()
		return
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: 'set' requires a channel name (dev, beta, stable, or a custom channel)")
		fs.Usage()
		os.Exit(1)
	}
	name := strings.TrimSpace(strings.ToLower(fs.Arg(0)))
	if !channel.IsValid(name) {
		fmt.Fprintf(os.Stderr, "Error: invalid channel name %q (use lowercase letters, digits, hyphens)\n", name)
		os.Exit(1)
	}

	cfg, path, err := loadHubChannelConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", path, err)
		os.Exit(1)
	}

	cfg.Update.Channel = name
	if *manifestBase != "" {
		base := strings.TrimRight(*manifestBase, "/")
		// If the user passed a full manifest URL (e.g. .../channels/local.json)
		// instead of just the base directory, strip the filename component.
		if strings.HasSuffix(base, ".json") {
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[:i]
			}
		}
		if cfg.Update.Sources == nil {
			cfg.Update.Sources = map[string]string{}
		}
		cfg.Update.Sources[name] = base
	}

	if err := writeHubConfig(path, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write config %s: %v\n", path, err)
		os.Exit(1)
	}

	resolved := channel.ResolveBase(name, cfg.Update.Sources)
	fmt.Printf("Hub channel set to %s\n", name)
	fmt.Printf("  manifest: %s\n", channel.ManifestURL(resolved, name))
	fmt.Printf("  config:   %s\n", path)
	fmt.Println("  Restart the hub (telahubd service restart) to pick up the new channel for background operations.")
}

// showHubChannelManifest prints the full parsed manifest for the given
// channel (or the configured one if -channel is omitted). Mirrors
// "tela channel show" and "telad channel show".
func showHubChannelManifest(args []string) {
	fs := flag.NewFlagSet("telahubd channel show", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	chName := fs.String("channel", "", "Channel to show (default: hub's configured channel)")
	configPath := fs.String("config", "", "Path to telahubd.yaml (default: platform-standard path)")
	fs.Parse(hubPermuteArgs(fs, args))

	if wantHelp() {
		printHubChannelUsage()
		return
	}

	cfg, path, err := loadHubChannelConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", path, err)
		os.Exit(1)
	}
	globalCfgMu.Lock()
	globalCfg = cfg
	globalCfgMu.Unlock()

	ch, base := hubChannel()
	if *chName != "" {
		ch = channel.Normalize(*chName)
	}

	m, err := hubChannelFetcher.GetURL(channel.ManifestURL(base, ch))
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
	names := make([]string, 0, len(m.Binaries))
	for name := range m.Binaries {
		names = append(names, name)
	}
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

// hubPermuteArgs reorders args so flags precede positional arguments,
// allowing "telahubd channel set <name> -flag value" and "-flag value <name>".
// Mirrors permuteAgentArgs in the agent package.
func hubPermuteArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if bv, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bv.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

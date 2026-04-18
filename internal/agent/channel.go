// channel.go -- "telad channel" subcommand
//
// Reads or writes the agent's release channel preference. The preference is
// stored in the agent's YAML config file (telad.yaml) under the "update"
// key, mirroring the structure used by "tela channel" in the credential store.
//
// Channel management via the hub admin API is also available:
//   tela admin agent channel -machine <id> [set <ch>]

package agent

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
	"github.com/paulmooreparks/tela/internal/cliflag"
	"gopkg.in/yaml.v3"
)

func cmdAgentChannel(args []string) {
	// Dispatch on the first positional arg. We intentionally do NOT parse
	// flags at this level, because `telad channel -config <path>` should
	// flow into the default (show) handler whose FlagSet knows -config;
	// a top-level fs.Parse would die on unknown flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "set":
			setAgentChannel(args[1:])
			return
		case "show":
			showAgentChannelManifest(args[1:])
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown channel command: %s\n\n", args[0])
			printAgentChannelUsage()
			os.Exit(1)
		}
	}
	// No subcommand. Help flags and -config are consumed by showAgentChannel.
	showAgentChannel(args)
}

func printAgentChannelUsage() {
	fmt.Fprint(os.Stderr, `telad channel -- agent release channel

Usage:
  telad channel [-config <path>]                  Show the current channel and latest version
  telad channel set <channel> [-config <path>]    Switch the agent's release channel
  telad channel set <ch> -manifest-base URL       Override the upstream manifest URL prefix
  telad channel show [-channel <ch>]              Print the full parsed channel manifest

Options:
  -config <path>      Path to telad.yaml (env: TELAD_CONFIG). Required for set.
  -manifest-base URL  Base URL for a self-hosted channel server.
                      Provide the directory URL; the channel name and .json
                      are appended automatically (e.g. https://example.com/channels).

Help:
  -h, -?, -help       Show this help. Works after any subcommand too
                      (e.g. "telad channel set -h").

The preference is stored in the agent's YAML config file under "update.channel".
Hub and agent channels can also be managed remotely via:
  tela admin agent channel -machine <id> [set <ch>]
`)
}

func showAgentChannel(args []string) {
	fs := flag.NewFlagSet("telad channel", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", envOrDefault("TELAD_CONFIG", ""), "Path to telad.yaml (env: TELAD_CONFIG)")
	fs.Parse(permuteAgentArgs(fs, args))

	if wantHelp() {
		printAgentChannelUsage()
		return
	}

	// Load the config if given so agentChannel() returns the configured
	// channel instead of the compiled-in default.
	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		setActiveConfig(cfg, *configPath)
	}

	ch, base := agentChannel()
	fmt.Printf("  channel:         %s\n", ch)
	fmt.Printf("  manifest:        %s\n", channel.ManifestURL(base, ch))
	fmt.Printf("  current version: %s\n", version)

	m, err := agentChannelFetcher.GetURL(channel.ManifestURL(base, ch))
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

func setAgentChannel(args []string) {
	fs := flag.NewFlagSet("telad channel set", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", envOrDefault("TELAD_CONFIG", ""), "Path to telad.yaml (env: TELAD_CONFIG)")
	manifestBase := fs.String("manifest-base", "", "Override the upstream manifest URL prefix")
	fs.Parse(permuteAgentArgs(fs, args))

	if wantHelp() {
		printAgentChannelUsage()
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

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: -config is required (or set TELAD_CONFIG)")
		os.Exit(1)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", *configPath, err)
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

	data, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: marshal config: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*configPath, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write config %s: %v\n", *configPath, err)
		os.Exit(1)
	}

	resolved := channel.ResolveBase(name, cfg.Update.Sources)
	fmt.Printf("Agent channel set to %s\n", name)
	fmt.Printf("  manifest: %s\n", channel.ManifestURL(resolved, name))
	fmt.Printf("  config:   %s\n", *configPath)
}

// showAgentChannelManifest prints the full parsed manifest for the given
// channel (or the configured one if -channel is omitted). Mirrors the
// client's "tela channel show" for operator parity on the agent side.
func showAgentChannelManifest(args []string) {
	fs := flag.NewFlagSet("telad channel show", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	chName := fs.String("channel", "", "Channel to show (default: agent's configured channel)")
	configPath := fs.String("config", envOrDefault("TELAD_CONFIG", ""), "Path to telad.yaml (env: TELAD_CONFIG)")
	fs.Parse(permuteAgentArgs(fs, args))

	if wantHelp() {
		printAgentChannelUsage()
		return
	}

	// Load the config if given so agentChannel() returns the configured
	// channel. Without it we fall back to whatever setActiveConfig has
	// already stashed, or the default.
	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		setActiveConfig(cfg, *configPath)
	}

	ch, base := agentChannel()
	if *chName != "" {
		ch = channel.Normalize(*chName)
	}

	m, err := agentChannelFetcher.GetURL(channel.ManifestURL(base, ch))
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

// permuteAgentArgs reorders args so flags precede positional arguments,
// allowing "telad channel set <name> -flag value" and "-flag value <name>".
// Mirrors the permuteArgs helper in the client package.
func permuteAgentArgs(fs *flag.FlagSet, args []string) []string {
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
		// Check if this flag takes a value (i.e., it's defined and not bool).
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			// value embedded: -flag=value
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

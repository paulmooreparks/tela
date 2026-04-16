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
	"fmt"
	"os"
	"strings"

	"flag"

	"github.com/paulmooreparks/tela/internal/channel"
	"gopkg.in/yaml.v3"
)

func cmdAgentChannel(args []string) {
	if len(args) == 0 {
		showAgentChannel()
		return
	}
	switch args[0] {
	case "set":
		setAgentChannel(args[1:])
	case "help", "-h", "--help":
		printAgentChannelUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown channel command: %s\n\n", args[0])
		printAgentChannelUsage()
		os.Exit(1)
	}
}

func printAgentChannelUsage() {
	fmt.Fprintf(os.Stderr, `telad channel -- agent release channel

Usage:
  telad channel [-config <path>]                Show the current channel
  telad channel set <channel> [-config <path>]  Switch the agent's release channel
  telad channel set <ch> -manifest-base URL     Override the upstream manifest URL prefix

Options:
  -config <path>      Path to telad.yaml (env: TELAD_CONFIG). Required for set.
  -manifest-base URL  Base URL for a self-hosted channel server.
                      Provide the directory URL; the channel name and .json
                      are appended automatically (e.g. https://example.com/channels).

The preference is stored in the agent's YAML config file under "update.channel".
Hub and agent channels can also be managed remotely via:
  tela admin agent channel -machine <id> [set <ch>]
`)
}

func showAgentChannel() {
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
	configPath := fs.String("config", envOrDefault("TELAD_CONFIG", ""), "Path to telad.yaml (env: TELAD_CONFIG)")
	manifestBase := fs.String("manifest-base", "", "Override the upstream manifest URL prefix")
	fs.Parse(permuteAgentArgs(fs, args))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: 'set' requires a channel name (dev|beta|stable|<custom>)")
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
		cfg.Update.ManifestBase = base
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

	fmt.Printf("Agent channel set to %s\n", name)
	fmt.Printf("  manifest: %s\n", channel.ManifestURL(cfg.Update.ManifestBase, name))
	fmt.Printf("  config:   %s\n", *configPath)
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

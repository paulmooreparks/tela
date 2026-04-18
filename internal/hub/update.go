// update.go -- "telahubd update" CLI subcommand
//
// Self-updates the on-disk telahubd binary from the channel manifest,
// then exits. Useful for bootstrapping a new hub or bridging an old
// binary onto the channel system without going through the admin API.
//
// This is a thin CLI wrapper over the same hubDownloadAndStage path the
// admin API uses; the only difference is the entry point.

package hub

import (
	"flag"
	"fmt"
	"os"

	"github.com/paulmooreparks/tela/internal/channel"
	"github.com/paulmooreparks/tela/internal/cliflag"
)

func printUpdateUsage() {
	fmt.Fprint(os.Stderr, `telahubd update -- self-update the telahubd binary

Usage:
  telahubd update [-config <path>] [-channel <ch>] [-dry-run]

Options:
  -config <path>    Path to telahubd YAML config file. Reads configured channel.
  -channel <ch>     Override channel for this run. Does not modify the config file.
  -dry-run          Show what would be downloaded without modifying the binary.
  -h, -?, -help     Show this help.

The channel preference is stored in telahubd.yaml under "update.channel"
(set with "telahubd channel set <ch>").
`)
}

func cmdSelfUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", "", "Path to telahubd YAML config file. Used to read the configured channel.")
	chOverride := fs.String("channel", "", "Override channel for this run. Does not modify the config file.")
	dryRun := fs.Bool("dry-run", false, "Show what would be downloaded without modifying the binary")
	fs.Parse(args)

	if wantHelp() {
		printUpdateUsage()
		return
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "Error: unexpected argument %q. Use -h for help.\n", fs.Arg(0))
		os.Exit(1)
	}

	// Load the config so hubChannel() returns the configured channel.
	if *configPath != "" {
		cfg, err := loadHubConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		globalCfgMu.Lock()
		globalCfg = cfg
		globalCfgMu.Unlock()
	}

	// If no saved channel preference, infer from the binary's own version.
	// Stash on globalCfg so hubChannel() returns the inferred value.
	// Persists nothing to disk.
	globalCfgMu.Lock()
	if (globalCfg == nil || globalCfg.Update.Channel == "") && *chOverride == "" {
		if inferred := channel.InferFromVersion(version); inferred != "" {
			if globalCfg == nil {
				globalCfg = &hubConfig{}
			}
			globalCfg.Update.Channel = inferred
		}
	}
	globalCfgMu.Unlock()

	if *chOverride != "" {
		ch := channel.Normalize(*chOverride)
		if !channel.IsValid(ch) {
			fmt.Fprintf(os.Stderr, "Error: invalid channel %q (use lowercase letters, digits, hyphens)\n", *chOverride)
			os.Exit(1)
		}
		globalCfgMu.Lock()
		if globalCfg == nil {
			globalCfg = &hubConfig{}
		}
		globalCfg.Update.Channel = ch
		globalCfgMu.Unlock()
	}

	ch, base := hubChannel()
	if base == "" {
		fmt.Fprintf(os.Stderr, "Error: channel %q has no source URL; run 'telahubd channel sources set %s <url>' or switch to a built-in channel.\n", ch, ch)
		os.Exit(1)
	}
	m, err := hubChannelFetcher.GetURL(channel.ManifestURL(base, ch))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: fetch %s manifest: %v\n", ch, err)
		os.Exit(1)
	}

	fmt.Printf("Channel:  %s\n", m.Channel)
	fmt.Printf("Current:  %s\n", version)
	fmt.Printf("Latest:   %s\n", m.Version)

	if m.Version == version && version != "dev" {
		fmt.Println("Already up to date.")
		return
	}

	// Downgrade refusal: the channel's HEAD is older than what's running.
	if version != "dev" && !channel.IsNewer(m.Version, version) {
		fmt.Fprintf(os.Stderr, "Error: latest version on %s is %s, older than currently running %s.\n", ch, m.Version, version)
		fmt.Fprintln(os.Stderr, "telahubd update refuses cross-channel downgrades. To install an older release, download it from the release host by hand.")
		os.Exit(1)
	}

	if *dryRun {
		fmt.Println("Dry run: would download and stage", m.Version)
		return
	}

	if err := hubDownloadAndStage(m.Version); err != nil {
		fmt.Fprintf(os.Stderr, "Error: update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK: telahubd updated to %s. Restart the process for the new binary to take effect.\n", m.Version)
}

// update.go -- "telad update" CLI subcommand
//
// Self-updates the on-disk telad binary from the channel manifest, then
// exits. Useful for bootstrapping a new box or bridging an old binary
// onto the channel system without going through the hub admin API.
//
// This is a thin CLI wrapper over the same downloadAndStageUpdate path
// the management protocol uses; the only difference is the entry point.

package agent

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/paulmooreparks/tela/internal/channel"
)

func cmdSelfUpdate(args []string) {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintf(os.Stderr, `telad update -- self-update the telad binary

Usage:
  telad update [-config <path>] [-channel <ch>] [-dry-run]

Options:
  -config <path>    Path to telad.yaml (env: TELAD_CONFIG). Reads configured channel.
  -channel <ch>     Override channel for this run. Does not modify the config file.
  -dry-run          Show what would be downloaded without modifying the binary.
`)
		return
	}

	fs := flag.NewFlagSet("update", flag.ExitOnError)
	configPath := fs.String("config", envOrDefault("TELAD_CONFIG", ""), "Path to YAML config file (env: TELAD_CONFIG). Used to read the configured channel.")
	chOverride := fs.String("channel", "", "Override channel for this run. Does not modify the config file.")
	dryRun := fs.Bool("dry-run", false, "Show what would be downloaded without modifying the binary")
	fs.Parse(args)

	// Load the config (if any) so we honor the configured channel.
	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		setActiveConfig(cfg, *configPath)
	}

	// Apply the channel override after loadConfig so it wins.
	if *chOverride != "" {
		ch := channel.Normalize(*chOverride)
		if !channel.IsValid(ch) {
			fmt.Fprintf(os.Stderr, "Error: invalid channel %q (use lowercase letters, digits, hyphens)\n", *chOverride)
			os.Exit(1)
		}
		// Stash the override on the active config so agentChannel() picks it up.
		activeConfigMu.Lock()
		if activeConfig == nil {
			activeConfig = &configFile{}
		}
		activeConfig.Update.Channel = ch
		activeConfigMu.Unlock()
	}

	ch, base := agentChannel()
	m, err := agentChannelFetcher.GetURL(channel.ManifestURL(base, ch))
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

	if *dryRun {
		fmt.Println("Dry run: would download and stage", m.Version)
		return
	}

	lg := log.New(os.Stderr, "", 0)
	if err := downloadAndStageUpdate(lg, m.Version, "", ""); err != nil {
		fmt.Fprintf(os.Stderr, "Error: update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK: telad updated to %s. Restart the process for the new binary to take effect.\n", m.Version)
}

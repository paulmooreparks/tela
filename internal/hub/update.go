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
)

func cmdSelfUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to telahubd YAML config file. Used to read the configured channel.")
	chOverride := fs.String("channel", "", "Override channel for this run (dev|beta|stable). Does not modify the config file.")
	dryRun := fs.Bool("dry-run", false, "Show what would be downloaded without modifying the binary")
	fs.Parse(args)

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

	if *chOverride != "" {
		ch := channel.Normalize(*chOverride)
		if !channel.IsKnown(ch) {
			fmt.Fprintf(os.Stderr, "Error: unknown channel %q (expected dev|beta|stable)\n", *chOverride)
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

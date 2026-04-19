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
	"github.com/paulmooreparks/tela/internal/cliflag"
)

func printUpdateUsage() {
	fmt.Fprint(os.Stderr, `telad update -- self-update the telad binary

Usage:
  telad update [-config <path>] [-channel <ch>] [-dry-run]

Options:
  -config <path>    Path to telad.yaml (env: TELAD_CONFIG). Reads configured channel.
  -channel <ch>     Override channel for this run. Does not modify the config file.
  -dry-run          Show what would be downloaded without modifying the binary.
  -h, -?, -help     Show this help.
`)
}

func cmdSelfUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	wantHelp := cliflag.Help(fs)
	configPath := fs.String("config", envOrDefault("TELAD_CONFIG", ""), "Path to YAML config file (env: TELAD_CONFIG). Used to read the configured channel.")
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

	// Load the config (if any) so we honor the configured channel.
	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: load config %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		setActiveConfig(cfg, *configPath)
	}

	// If there's no saved channel preference, infer from the binary's own
	// version string. Stash that on the active config so agentChannel()
	// returns the inferred value. Persists nothing to disk.
	activeConfigMu.Lock()
	if (activeConfig == nil || activeConfig.Update.Channel == "") && *chOverride == "" {
		if inferred := channel.InferFromVersion(version); inferred != "" {
			if activeConfig == nil {
				activeConfig = &configFile{}
			}
			activeConfig.Update.Channel = inferred
		}
	}
	activeConfigMu.Unlock()

	// Apply the channel override after loadConfig so it wins.
	if *chOverride != "" {
		ch := channel.Normalize(*chOverride)
		if !channel.IsValid(ch) {
			fmt.Fprintf(os.Stderr, "Error: invalid channel %q (use lowercase letters, digits, hyphens)\n", *chOverride)
			os.Exit(1)
		}
		activeConfigMu.Lock()
		if activeConfig == nil {
			activeConfig = &configFile{}
		}
		activeConfig.Update.Channel = ch
		activeConfigMu.Unlock()
	}

	ch, base := agentChannel()
	if base == "" {
		fmt.Fprintf(os.Stderr, "Error: channel %q has no source URL; run 'telad channel sources set %s <url>' or switch to a built-in channel.\n", ch, ch)
		os.Exit(1)
	}
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

	// Downgrade refusal: the channel's HEAD is older than what's running.
	// Skipped when we are crossing channels -- switching release lines is
	// an explicit declaration of intent to follow the new channel's HEAD,
	// and semver ordering across parallel prerelease lineages (e.g. local.N
	// vs dev.N) is meaningless anyway.
	if version != "dev" && !channel.IsCrossChannel(version, m.Channel) && !channel.IsNewer(m.Version, version) {
		fmt.Fprintf(os.Stderr, "Error: latest version on %s is %s, older than currently running %s.\n", ch, m.Version, version)
		fmt.Fprintln(os.Stderr, "telad update refuses same-channel downgrades. To install an older release, download it from the release host by hand.")
		os.Exit(1)
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

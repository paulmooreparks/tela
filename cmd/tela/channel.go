// channel.go -- "tela channel" subcommand
//
// Reads or writes the tela client's own release channel preference. The
// preference is stored in the user credential store (~/.tela/credentials.yaml
// on Unix, %APPDATA%\tela\credentials.yaml on Windows) so it persists
// across invocations without a separate config file.
//
// Hub and agent channels are managed separately via `tela admin hub channel`
// and `tela admin agent channel`.

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/paulmooreparks/tela/internal/channel"
	"github.com/paulmooreparks/tela/internal/credstore"
)

func cmdChannel(args []string) {
	if len(args) == 0 {
		showClientChannel()
		return
	}

	switch args[0] {
	case "set":
		setClientChannel(args[1:])
	case "help", "-h", "--help":
		printChannelUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown channel command: %s\n\n", args[0])
		printChannelUsage()
		os.Exit(1)
	}
}

func printChannelUsage() {
	fmt.Fprintf(os.Stderr, `tela channel -- client release channel

Usage:
  tela channel                             Show the current channel and latest version on it
  tela channel set <dev|beta|stable>       Switch the client's release channel
  tela channel set <ch> -manifest-base URL Override the upstream manifest URL prefix

The preference is stored in %s.
Hub and agent channels are managed separately via:
  tela admin hub channel [set <ch>]
  tela admin agent channel -machine <id> [set <ch>]
`, credstore.UserPath())
}

// loadClientChannel returns the configured channel name and optional
// manifest base override from the user credential store.
func loadClientChannel() (string, string) {
	store, err := credstore.Load(credstore.UserPath())
	if err != nil || store == nil {
		return channel.DefaultChannel, ""
	}
	return channel.Normalize(store.Update.Channel), store.Update.ManifestBase
}

func showClientChannel() {
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
	manifestBase := fs.String("manifest-base", "", "Override the upstream manifest URL prefix")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: 'set' requires a channel name (dev|beta|stable)")
		os.Exit(1)
	}
	name := strings.TrimSpace(strings.ToLower(fs.Arg(0)))
	if !channel.IsKnown(name) {
		fmt.Fprintf(os.Stderr, "Error: unknown channel %q (expected dev|beta|stable)\n", name)
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
		store.Update.ManifestBase = *manifestBase
	}
	if err := store.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save credential store: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Client channel set to %s\n", name)
	fmt.Printf("  manifest: %s\n", channel.ManifestURL(store.Update.ManifestBase, name))
}

// Command telachand is the Tela Channel Daemon: a lightweight HTTP server
// that hosts Tela release channel manifests and binary files. Deploy it
// when you want a self-hosted alternative to the default GitHub release
// channel, or when you need to serve custom builds through the Tela tunnel.
//
// Clients (tela, telad, telahubd, TelaVisor) configure their update base
// URL to point at this server's listen address. The manifest and binary
// file formats are identical to the GitHub-hosted channel, so no client
// changes are required.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/paulmooreparks/tela/internal/service"
	"github.com/paulmooreparks/tela/internal/telelog"
)

// version is set at build time via -ldflags. Production builds set this
// from the channel tag in release.yml; local builds default to "dev".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "service" {
		handleServiceCommand()
		return
	}

	if service.IsWindowsService() {
		runAsWindowsService()
		return
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Printf("telachand %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
			os.Exit(0)
		case "help", "-h", "--help":
			printUsage()
			os.Exit(0)
		case "publish":
			cmdPublish(os.Args[2:])
			return
		case "update":
			cmdSelfUpdate(os.Args[2:])
			return
		}
	}

	fs := flag.NewFlagSet("telachand", flag.ExitOnError)
	configPath := fs.String("config", envOrDefault("TELACHAND_CONFIG", ""), "Path to YAML config file (env: TELACHAND_CONFIG)")
	fs.Usage = printUsage
	fs.Parse(os.Args[1:])

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	telelog.Init("telachand", os.Stderr)

	stopCh := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		close(stopCh)
	}()

	runServer(cfg, stopCh)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `telachand - Tela Channel Daemon

Usage:
  telachand [-config <path>]
  telachand publish -channel <name> -tag <tag> [-base-url <url>] [-config <path>]
  telachand service <install|uninstall|start|stop|restart|status|run> [-config <path>] [--user]
  telachand update [-channel <name>] [-dry-run] [-config <path>]
  telachand version

Hosts Tela release channel manifests and binary files. Configure tela,
telad, telahubd, and TelaVisor to use this server as their update base
URL instead of the default GitHub release channel.

Options:
  -config <path>  Path to YAML config file (env: TELACHAND_CONFIG)
`)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

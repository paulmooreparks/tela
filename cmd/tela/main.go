// Package main is the tela client binary entry point. Almost everything
// lives in internal/client now -- this file is a thin shim that hands
// off to client.Main() so the binary's behavior is unchanged.
//
// The split exists so the test harness in internal/teststack can call
// client.Connect() directly without spawning a subprocess.
package main

import "github.com/paulmooreparks/tela/internal/client"

// version is set at build time via -ldflags. Production builds set this
// from the channel tag in release.yml; local builds default to "dev".
// We immediately push the value into the client package so its own log
// lines and self-update logic see the same string.
var version = "dev"

func main() {
	client.SetVersion(version)
	client.Main()
}

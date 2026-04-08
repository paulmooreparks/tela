// Package main is the telahubd binary entry point. Almost everything
// lives in internal/hub now -- this file is a thin shim that hands off
// to hub.Main() so the binary's behavior is unchanged.
//
// The split exists so the test harness in internal/teststack can call
// hub.Run() directly without spawning a subprocess.
package main

import "github.com/paulmooreparks/tela/internal/hub"

// version is set at build time via -ldflags. Production builds set this
// from the channel tag in release.yml; local builds default to "dev".
// We immediately push the value into the hub package so its own log
// lines and self-update logic see the same string.
var version = "dev"

func main() {
	hub.SetVersion(version)
	hub.Main()
}

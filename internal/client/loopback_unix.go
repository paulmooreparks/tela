//go:build !windows

package client

// ensureLoopbackAlias is a no-op on Unix systems. Any address in
// 127.0.0.0/8 is routable on the loopback interface by default on
// Linux and macOS.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Unix systems.
func removeLoopbackAlias(addr string) error { return nil }

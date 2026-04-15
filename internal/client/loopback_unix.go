//go:build !windows

package client

// ensureLoopbackAlias is a no-op on Unix systems. Any address in
// 127.0.0.0/8 is routable on the loopback interface by default on
// Linux and macOS.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Unix systems.
func removeLoopbackAlias(addr string) error { return nil }

// loopbackPort returns the local port for a specific loopback listener.
// On Linux and macOS, a more-specific address bind correctly takes
// precedence over a wildcard 0.0.0.0:port listener, so the real
// service port is used directly.
func loopbackPort(remote uint16) uint16 { return remote }

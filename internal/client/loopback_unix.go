//go:build !windows

package client

// ensureLoopbackAlias is a no-op on Unix systems. Any address in
// 127.0.0.0/8 is routable on the loopback interface by default on
// Linux and macOS.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Unix systems.
func removeLoopbackAlias(addr string) error { return nil }

// wildcardBound always returns false on Unix. On Linux and macOS, a
// more-specific address bind (e.g. 127.88.x.x:port) correctly takes
// precedence over a wildcard 0.0.0.0:port listener, so no pre-bind
// conflict detection is needed.
func wildcardBound(port uint16) bool { return false }

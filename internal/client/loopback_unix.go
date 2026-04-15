//go:build !windows

package client

// ensureLoopbackAlias is a no-op on Unix systems. Any address in
// 127.0.0.0/8 is routable on the loopback interface by default on
// Linux and macOS.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Unix systems.
func removeLoopbackAlias(addr string) error { return nil }

// loopbackBind returns the address and port to bind for a service on Unix.
// Uses the machine's specific loopback address and the real service port.
// On Linux and macOS a more-specific bind takes precedence over a wildcard
// 0.0.0.0:port listener, so no port offset is needed.
func loopbackBind(m portMapping) (string, uint16) {
	if m.bindAddr != "" {
		return m.bindAddr, m.remote
	}
	return "127.0.0.1", m.remote
}

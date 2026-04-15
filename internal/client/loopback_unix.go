//go:build !windows

package client

// ensureLoopbackAlias is a no-op on Unix systems.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Unix systems.
func removeLoopbackAlias(addr string) error { return nil }

// loopbackBind returns the address and port to bind for a service on Unix.
func loopbackBind(m portMapping) (string, uint16) { return "127.0.0.1", m.remote + 10000 }

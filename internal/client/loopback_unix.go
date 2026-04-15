//go:build !windows

package client

// ensureLoopbackAlias is a no-op on Unix systems.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Unix systems.
func removeLoopbackAlias(addr string) error { return nil }

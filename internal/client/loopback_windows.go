package client

// ensureLoopbackAlias is a no-op on Windows. Modern Windows routes the
// full 127.0.0.0/8 loopback range without requiring explicit aliases.
// The previous implementation tried to add netsh aliases, which required
// elevation and violated Tela's no-admin-required design.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Windows.
func removeLoopbackAlias(addr string) error { return nil }

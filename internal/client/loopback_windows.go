package client

// ensureLoopbackAlias is a no-op on Windows.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Windows.
func removeLoopbackAlias(addr string) error { return nil }

// loopbackBind returns the address and port to bind for a service on Windows.
func loopbackBind(m portMapping) (string, uint16) { return "127.0.0.1", m.remote + 10000 }

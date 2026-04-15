package client

// ensureLoopbackAlias is a no-op on Windows.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Windows.
func removeLoopbackAlias(addr string) error { return nil }

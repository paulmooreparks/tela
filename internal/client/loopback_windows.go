package client

// ensureLoopbackAlias is a no-op on Windows. Modern Windows routes the
// full 127.0.0.0/8 loopback range without requiring explicit aliases.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Windows.
func removeLoopbackAlias(addr string) error { return nil }

// loopbackBind returns the address and port to bind for a service on Windows.
//
// Uses 127.0.0.1 with port+10000 instead of the machine's specific loopback
// address and real service port. Two Windows conditions make same-port binding
// unreliable even when the more-specific bind succeeds:
//
//   - Kernel-level drivers (Remote Desktop rdpwsx.dll) intercept connections
//     below Winsock. They do not appear in any socket table and cannot be
//     detected by a post-bind probe.
//
//   - Windows OpenSSH binds :::port (IPv6 dual-stack), which is invisible to
//     IPv4 socket enumeration and intercepts IPv4 connections anyway.
//
// Using port+10000 on 127.0.0.1 sidesteps both: 10022 is not intercepted by
// the SSH dual-stack socket, and 13389 is not touched by the RDP driver.
func loopbackBind(m portMapping) (string, uint16) {
	return "127.0.0.1", m.remote + 10000
}

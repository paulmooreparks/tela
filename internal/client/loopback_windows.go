package client

// ensureLoopbackAlias is a no-op on Windows. Modern Windows routes the
// full 127.0.0.0/8 loopback range without requiring explicit aliases.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Windows.
func removeLoopbackAlias(addr string) error { return nil }

// loopbackPortOffset is added to the remote service port when binding a
// specific loopback address on Windows.
//
// On Windows, two conditions make it impossible to receive connections on
// the same port number as a local service, even when a more-specific socket
// binding succeeds:
//
//   - Kernel-level drivers (Remote Desktop's rdpwsx.dll, SMB, NetBIOS)
//     intercept connections below Winsock before they reach any socket.
//     They do not appear in GetExtendedTcpTable and cannot be detected.
//
//   - Windows OpenSSH and other services bind to IPv6 dual-stack sockets
//     (:::port) that also accept IPv4. GetExtendedTcpTable (AF_INET) does
//     not see these.
//
// Post-bind probes using plain TCP produce false negatives: the probe reaches
// tela's socket while real protocol clients (mstsc, ssh) are intercepted.
//
// Shifting to port+offset sidesteps all of these. Port 10022 is not
// intercepted by any kernel driver and is unlikely to conflict with any
// running service.
const loopbackPortOffset = 10000

// loopbackPort returns the local port to use when binding a specific
// loopback address for a service whose remote port is remote.
// On Windows this always applies the offset to avoid kernel-level
// interception and socket routing ambiguity.
func loopbackPort(remote uint16) uint16 {
	return remote + loopbackPortOffset
}

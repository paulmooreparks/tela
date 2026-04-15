package client

import (
	"net"
	"time"
)

// ensureLoopbackAlias is a no-op on Windows. Modern Windows routes the
// full 127.0.0.0/8 loopback range without requiring explicit aliases.
// The previous implementation tried to add netsh aliases, which required
// elevation and violated Tela's no-admin-required design.
func ensureLoopbackAlias(addr string) error { return nil }

// removeLoopbackAlias is a no-op on Windows.
func removeLoopbackAlias(addr string) error { return nil }

// listenerShadowed verifies that ln actually receives connections sent to addr.
// On Windows, a socket bound to 0.0.0.0:port with SO_EXCLUSIVEADDRUSE takes
// routing precedence over more-specific binds; net.Listen succeeds but
// connections are silently delivered to the wildcard socket instead. This
// function detects that by accepting one probe connection and checking whether
// our listener gets it.
func listenerShadowed(ln net.Listener, addr string) bool {
	captured := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
		captured <- struct{}{}
	}()
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return true
	}
	c.Close()
	select {
	case <-captured:
		return false // our listener received it
	case <-time.After(200 * time.Millisecond):
		return true // wildcard socket intercepted it
	}
}

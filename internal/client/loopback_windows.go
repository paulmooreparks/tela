package client

import (
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	loopbackAliasesMu sync.Mutex
	loopbackAliases   = make(map[string]bool) // addresses we have added
)

// ensureLoopbackAlias adds a loopback address alias on Windows so that
// addresses outside 127.0.0.1 in 127.0.0.0/8 are reachable. On
// Windows only 127.0.0.1 is routable by default; additional addresses
// must be added explicitly.
//
// The alias is added via "netsh interface ip add address" on the
// Loopback Pseudo-Interface. This requires elevation. If the command
// fails (not elevated), ensureLoopbackAlias logs a warning and returns
// the error so the caller can fall back to 127.0.0.1.
//
// Aliases are tracked so removeLoopbackAlias can clean them up on
// disconnect.
func ensureLoopbackAlias(addr string) error {
	if addr == "127.0.0.1" {
		return nil
	}

	loopbackAliasesMu.Lock()
	defer loopbackAliasesMu.Unlock()

	if loopbackAliases[addr] {
		return nil // already added this session
	}

	// Check if the address is already reachable (e.g. from a previous
	// tela session that was killed without cleanup, or added manually).
	if isLoopbackReachable(addr) {
		loopbackAliases[addr] = true
		return nil
	}

	// Try adding the alias. This requires elevation.
	cmd := exec.Command("netsh", "interface", "ip", "add", "address",
		"Loopback Pseudo-Interface 1", addr, "255.255.255.255")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[loopback] failed to add alias %s (elevation required): %v: %s",
			addr, err, strings.TrimSpace(string(out)))
		return err
	}

	loopbackAliases[addr] = true
	log.Printf("[loopback] added alias %s on Loopback Pseudo-Interface 1", addr)
	return nil
}

// removeLoopbackAlias removes a previously added loopback alias.
func removeLoopbackAlias(addr string) error {
	if addr == "127.0.0.1" {
		return nil
	}

	loopbackAliasesMu.Lock()
	defer loopbackAliasesMu.Unlock()

	if !loopbackAliases[addr] {
		return nil
	}

	cmd := exec.Command("netsh", "interface", "ip", "delete", "address",
		"Loopback Pseudo-Interface 1", addr)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[loopback] failed to remove alias %s: %v: %s",
			addr, err, strings.TrimSpace(string(out)))
		return err
	}

	delete(loopbackAliases, addr)
	log.Printf("[loopback] removed alias %s", addr)
	return nil
}

// isLoopbackReachable checks whether an address is already bound or
// reachable by attempting a short TCP dial.
func isLoopbackReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr+":0", 100*time.Millisecond)
	if err == nil {
		conn.Close()
		return true
	}
	// If we get "connection refused" the address is routable (nothing
	// is listening, but the OS knows about it). If we get "network
	// unreachable" or similar, the address is not routed.
	return strings.Contains(err.Error(), "refused")
}

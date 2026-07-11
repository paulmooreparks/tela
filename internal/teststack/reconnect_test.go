package teststack

import (
	"testing"
	"time"

	"github.com/paulmooreparks/tela/internal/hub"
)

// TestStackHandshakeOnReconnect verifies that severing a client's session
// from the hub causes the client to redial and complete a NEW hub-side
// session pairing (a new WireGuard handshake) within its normal reconnect
// backoff window. Tracks GitHub #11.
//
// The disruption is server-side and surgical: the hub closes the client
// session's WebSocket via DisconnectClientSessionForTesting, which
// simulates a dropped connection, an operator kick, or a mid-session hub
// restart without killing the agent process, the client process, or the
// client's own connection. Reconnection is proven at the
// session-establishment layer through existing hub telemetry: sessionCount
// transitions 1 -> 0 -> 1, and the machine's "client-connect" history
// event count reaches 2 (each successful pairSession records exactly one
// such event, so a second event is a distinct new pairing, not a stale
// reused one). Both sides mint fresh per-session X25519 keypairs and bring
// up a new WireGuard device on every pairing, so this pair of signals is a
// reliable proxy for a genuinely new handshake.
//
// What this test does NOT cover, and does not imply coverage of:
//   - Real byte-echo through the reconnected tunnel. The same in-process
//     gvisor limitation that TestStackClientConnectsAndBindsListener
//     documents applies; no forwarded TCP flow is opened here (see
//     ConnectTunnelOnly), so actual data-plane resumption is not asserted.
//   - Direct-P2P reconnect (STUN/hole-punch, tryDirectUpgrade). The harness
//     cannot drive real NAT traversal in one process.
//   - UDP-relay reconnect (tryUDPUpgrade). The reconnect proven here is over
//     the WebSocket-relayed WireGuard path that every session starts on.
//   - Bridge-federated reconnect (Hub-A/Hub-B). This card is single-hub.
//
// No time.Sleep-based synchronization appears below: every wait is a
// bounded poll against a real hub-observed signal.
func TestStackHandshakeOnReconnect(t *testing.T) {
	stack := New(t)
	// Empirically (verified by running this test), the no-forwarding path
	// still trips the goroutine-leak check, so this test opts out. The
	// leak is NOT the gvisor netstack worker that the forwarding path
	// leaks (no forwarded flow is opened here); it is the hub-side
	// agent-join timeout guard. Every client connect spawns a goroutine in
	// handleConnect (hub.go:1499) that does an uncancellable 30s time.Sleep
	// before checking whether the agent joined the session. It exits about
	// 30s after connect, far outside Close()'s 2s leak-check grace window.
	// This test establishes two sessions (initial + reconnect), so two such
	// guards are still sleeping at teardown, two goroutines past the
	// tolerance. This is the same "hub-side handleConnect retry goroutine
	// (a 30s time.Sleep)" class the SkipLeakCheck doc already names, reached
	// here through the ordinary connect path rather than the forwarding
	// hang. See SkipLeakCheck in stack.go.
	stack.SkipLeakCheck()
	stack.AddMachine("barn", []uint16{22})
	stack.WaitAgentRegistered("barn", 5*time.Second)

	stack.ConnectTunnelOnly("barn")

	// First session pairs. Near-instant in-process; no backoff on the
	// first connect.
	stack.WaitSessionCount("barn", 1, 5*time.Second)
	stack.WaitHistoryEventCount("barn", "client-connect", 1, 5*time.Second)

	// Sever the session from the hub side.
	if !hub.DisconnectClientSessionForTesting("barn") {
		t.Fatal(`DisconnectClientSessionForTesting: no active session for "barn"`)
	}

	// Hub-observed teardown: handleDisconnect runs synchronously off the
	// closed-socket read error, so sessionCount drops to 0 quickly.
	stack.WaitSessionCount("barn", 0, 5*time.Second)

	// The client redials after reconnectDelay(0, maxDelay): a 3s base with
	// +/-25% jitter, so 2.25-3.75s typical. A 15s budget leaves roughly 4x
	// margin for CI scheduling jitter without masking a real regression: a
	// hung reconnect still fails well inside 15s, a healthy one completes
	// in well under 5s.
	stack.WaitSessionCount("barn", 1, 15*time.Second)
	stack.WaitHistoryEventCount("barn", "client-connect", 2, 15*time.Second)
}

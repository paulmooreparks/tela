// Package relay defines the v1 relay frame header shared by all Tela binaries.
//
// Wire layout (7 bytes, big-endian):
//
//	+-------+-------+-------+-------+-------+-------+-------+
//	| magic |  hop  | flags |      session_id (4 bytes)      |
//	+-------+-------+-------+-------+-------+-------+-------+
//	| WireGuard ciphertext ...                               |
//	+--------------------------------------------------------+
//
// magic: 0x54 ('T'). A receiver that sees any other byte on a binary relay
// message rejects the frame.
//
// hop: TTL. The first hub in the chain sets this to its configured maxHops
// (default DefaultMaxHops). Each subsequent forwarding hub decrements by one
// before relaying. Clients and agents send hop=0; the hub overwrites it.
// A hub that receives a frame with hop=0 from another hub drops it.
//
// flags: bit 0 selects frame type: 0=DATA, 1=CONTROL. Bits 1-7 are reserved
// and must be zero in v1; receivers must ignore reserved bits they do not
// understand.
//
// session_id: identifies the session within this connection. In v1 each
// WebSocket carries one session, so this is constant for the connection's
// lifetime. Initiating side assigns the ID (random uint32).
package relay

import (
	"encoding/binary"
	"time"
)

const (
	// Magic is the first byte of every v1 relay frame.
	Magic = byte(0x54)

	// HeaderLen is the fixed size of the relay frame header in bytes.
	HeaderLen = 7

	// FlagData marks a DATA frame (WireGuard ciphertext follows).
	FlagData = byte(0x00)

	// FlagControl marks a CONTROL frame (in-band session control follows).
	FlagControl = byte(0x01)

	// DefaultMaxHops is the hop count written by the first hub in the chain
	// when no per-bridge maxHops is configured.
	DefaultMaxHops = uint8(8)

	// ControlKeepaliveReq is the payload byte for a keepalive request CONTROL frame.
	ControlKeepaliveReq = byte(0x01)

	// ControlKeepaliveResp is the payload byte for a keepalive response CONTROL frame.
	ControlKeepaliveResp = byte(0x02)

	// KeepaliveInterval is how often a session keepalive request is sent.
	KeepaliveInterval = 30 * time.Second

	// KeepaliveTimeout is how long to wait for a keepalive response before
	// declaring the session dead and tearing it down.
	KeepaliveTimeout = 45 * time.Second
)

// ForwardFrame validates and advances the hop count in a relay frame for
// hub-to-hub or hub-to-peer forwarding.
//
// If hop in the incoming frame is 0, the hub is the first relay for this
// frame and sets hop to maxHops before decrementing. This handles the
// common case where clients and agents send hop=0 (the hub overwrites it).
//
// After decrement, if hop reaches 0 the frame is dropped (returns nil, false).
// This prevents forwarding loops.
//
// Returns a new slice with the updated header prepended to the original
// payload, and true on success. Returns nil, false if the frame is too
// short, the magic byte is wrong, or the TTL is exhausted.
func ForwardFrame(data []byte, maxHops uint8) ([]byte, bool) {
	if len(data) < HeaderLen || data[0] != Magic {
		return nil, false
	}

	hop := data[1]
	if hop == 0 {
		// First relay: hub sets the TTL.
		hop = maxHops
	}
	hop--
	if hop == 0 {
		return nil, false // TTL exhausted
	}

	out := make([]byte, len(data))
	copy(out, data)
	out[1] = hop
	return out, true
}

// BuildDataFrame returns a new slice containing the v1 DATA frame header
// followed by payload. hop should be 0 when called by clients or agents
// (the hub overwrites it). sessionID is the caller's session identifier.
func BuildDataFrame(payload []byte, hop uint8, sessionID uint32) []byte {
	out := make([]byte, HeaderLen+len(payload))
	out[0] = Magic
	out[1] = hop
	out[2] = FlagData
	binary.BigEndian.PutUint32(out[3:7], sessionID)
	copy(out[HeaderLen:], payload)
	return out
}

// BuildControlFrame returns a new slice containing the v1 CONTROL frame header
// followed by payload. hop should be 0 when called by clients or agents
// (the hub overwrites it). sessionID is the caller's session identifier.
func BuildControlFrame(payload []byte, hop uint8, sessionID uint32) []byte {
	out := make([]byte, HeaderLen+len(payload))
	out[0] = Magic
	out[1] = hop
	out[2] = FlagControl
	binary.BigEndian.PutUint32(out[3:7], sessionID)
	copy(out[HeaderLen:], payload)
	return out
}

// ParseHeader parses the 7-byte relay frame header from data.
// Returns the header fields and the remaining payload.
// ok is false if data is too short or the magic byte is wrong.
func ParseHeader(data []byte) (hop, flags byte, sessionID uint32, payload []byte, ok bool) {
	if len(data) < HeaderLen || data[0] != Magic {
		return 0, 0, 0, nil, false
	}
	return data[1], data[2], binary.BigEndian.Uint32(data[3:7]), data[HeaderLen:], true
}

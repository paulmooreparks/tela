package wsbind

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"github.com/paulmooreparks/tela/internal/relay"
)

// buildSTUNSuccess crafts a STUN Binding Success Response (msgType 0x0101)
// echoing txID and carrying a single IPv4 XOR-MAPPED-ADDRESS attribute for
// ip:port. The returned bytes decode cleanly through parseSTUNResponse. It is
// shared by the Tier A parse tests and the Tier B STUNDiscover responder.
func buildSTUNSuccess(txID []byte, ip net.IP, port uint16) []byte {
	ip4 := ip.To4()
	attr := make([]byte, 12) // 2 type + 2 len + 8 value
	binary.BigEndian.PutUint16(attr[0:2], 0x0020)
	binary.BigEndian.PutUint16(attr[2:4], 8)
	attr[4] = 0x00 // reserved
	attr[5] = 0x01 // IPv4 family
	xPort := port ^ uint16(stunMagicCookie>>16)
	binary.BigEndian.PutUint16(attr[6:8], xPort)
	xIP := binary.BigEndian.Uint32(ip4) ^ stunMagicCookie
	binary.BigEndian.PutUint32(attr[8:12], xIP)

	resp := make([]byte, 20+len(attr))
	binary.BigEndian.PutUint16(resp[0:2], 0x0101) // Binding Success Response
	binary.BigEndian.PutUint16(resp[2:4], uint16(len(attr)))
	binary.BigEndian.PutUint32(resp[4:8], stunMagicCookie)
	copy(resp[8:20], txID)
	copy(resp[20:], attr)
	return resp
}

// --- Tier A: pure unit, no sockets ---

// TestParseSTUNResponse_Valid covers matrix item 1: a well-formed Binding
// Success Response with a matching transaction ID yields the reflexive
// IPv4 address.
func TestParseSTUNResponse_Valid(t *testing.T) {
	txID := []byte("0123456789ab")
	resp := buildSTUNSuccess(txID, net.IPv4(1, 2, 3, 4), 1234)

	got, err := parseSTUNResponse(resp, txID)
	if err != nil {
		t.Fatalf("parseSTUNResponse: %v", err)
	}
	if got != "1.2.3.4:1234" {
		t.Fatalf("reflexive address = %q, want %q", got, "1.2.3.4:1234")
	}
}

// TestParseSTUNResponse_Errors covers matrix item 2: five distinct negative
// cases each return an error and no address.
func TestParseSTUNResponse_Errors(t *testing.T) {
	txID := []byte("0123456789ab")
	base := buildSTUNSuccess(txID, net.IPv4(1, 2, 3, 4), 1234)

	t.Run("wrong msgType", func(t *testing.T) {
		bad := append([]byte(nil), base...)
		binary.BigEndian.PutUint16(bad[0:2], 0x0001) // Binding Request, not Success Response
		if _, err := parseSTUNResponse(bad, txID); err == nil {
			t.Fatal("expected error on wrong msgType")
		}
	})

	t.Run("wrong magic cookie", func(t *testing.T) {
		bad := append([]byte(nil), base...)
		binary.BigEndian.PutUint32(bad[4:8], 0xDEADBEEF)
		if _, err := parseSTUNResponse(bad, txID); err == nil {
			t.Fatal("expected error on magic cookie mismatch")
		}
	})

	t.Run("wrong transaction ID", func(t *testing.T) {
		if _, err := parseSTUNResponse(base, []byte("ffffffffffff")); err == nil {
			t.Fatal("expected error on transaction ID mismatch")
		}
	})

	t.Run("too short", func(t *testing.T) {
		if _, err := parseSTUNResponse(base[:19], txID); err == nil {
			t.Fatal("expected error on sub-20-byte response")
		}
	})

	t.Run("missing XOR-MAPPED-ADDRESS", func(t *testing.T) {
		// A 20-byte header with no attributes: valid type/cookie/txID but no
		// address attribute present.
		noAttr := make([]byte, 20)
		binary.BigEndian.PutUint16(noAttr[0:2], 0x0101)
		binary.BigEndian.PutUint16(noAttr[2:4], 0)
		binary.BigEndian.PutUint32(noAttr[4:8], stunMagicCookie)
		copy(noAttr[8:20], txID)
		if _, err := parseSTUNResponse(noAttr, txID); err == nil {
			t.Fatal("expected error when XOR-MAPPED-ADDRESS is absent")
		}
	})
}

// TestNew covers matrix item 7: New returns a Bind with a non-zero sessionID,
// a RecvCh of the requested capacity, and open == false before Open.
func TestNew(t *testing.T) {
	const bufSize = 16
	b := New(nil, bufSize)

	if b.sessionID == 0 {
		t.Fatal("sessionID is zero; expected a random non-zero identifier")
	}
	if got := cap(b.RecvCh); got != bufSize {
		t.Fatalf("RecvCh capacity = %d, want %d", got, bufSize)
	}
	if b.open {
		t.Fatal("open is true before Open() was called")
	}
}

// TestUpgradeUDP_BadTokenLen covers matrix item 8: a wrong-length token is
// rejected before any socket is opened or any UDP state is mutated.
func TestUpgradeUDP_BadTokenLen(t *testing.T) {
	b := New(nil, 4)

	err := b.UpgradeUDP("127.0.0.1", 12345, []byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for a 3-byte token")
	}
	if b.UDPActive() {
		t.Fatal("UDPActive() is true after a rejected UpgradeUDP")
	}
	if b.udpConn != nil {
		t.Fatal("udpConn was opened despite the token being rejected")
	}
}

// TestClose_Idempotent covers matrix item 9: Close is safe before Open and
// safe to call twice.
func TestClose_Idempotent(t *testing.T) {
	b := New(nil, 4)

	if err := b.Close(); err != nil {
		t.Fatalf("Close before Open returned error: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

// TestConnAdapters covers matrix item 10: the trivial conn.Bind adapters.
func TestConnAdapters(t *testing.T) {
	b := New(nil, 4)

	if got := b.BatchSize(); got != 1 {
		t.Fatalf("BatchSize() = %d, want 1", got)
	}
	if err := b.SetMark(42); err != nil {
		t.Fatalf("SetMark returned error: %v", err)
	}
	ep, err := b.ParseEndpoint("anything")
	if err != nil {
		t.Fatalf("ParseEndpoint returned error: %v", err)
	}
	if ep == nil {
		t.Fatal("ParseEndpoint returned a nil endpoint")
	}
}

// TestParseHeaderRoundTrip is a small sanity check that the relay helpers the
// matrix relies on frame and parse a payload symmetrically, guarding the
// framed/unframed assertions in the Tier B WS tests.
func TestParseHeaderRoundTrip(t *testing.T) {
	payload := []byte("wireguard-ciphertext")
	framed := relay.BuildDataFrame(payload, 0, 0x11223344)
	_, flags, sid, got, ok := relay.ParseHeader(framed)
	if !ok {
		t.Fatal("ParseHeader returned not-ok on a BuildDataFrame frame")
	}
	if flags != relay.FlagData {
		t.Fatalf("flags = 0x%02x, want FlagData", flags)
	}
	if sid != 0x11223344 {
		t.Fatalf("sessionID = 0x%08x, want 0x11223344", sid)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

package wsbind

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/paulmooreparks/tela/internal/relay"
)

// newUDPListener binds a loopback UDP socket on an ephemeral port and closes
// it at test end. Used as the Bind's own socket, the fake hub, the fake peer,
// and the fake STUN server across the Tier B matrix.
func newUDPListener(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func udpAddr(c *net.UDPConn) *net.UDPAddr { return c.LocalAddr().(*net.UDPAddr) }

// recvWithin waits up to d for a datagram on ch. It is the positive-path
// event wait; negative paths pass a short window and assert !ok.
func recvWithin(ch <-chan []byte, d time.Duration) ([]byte, bool) {
	select {
	case b := <-ch:
		return b, true
	case <-time.After(d):
		return nil, false
	}
}

// hubDataFrame builds a hub-relayed datagram: [token][relay header][payload].
func hubDataFrame(token, payload []byte) []byte {
	hdr := relay.BuildDataFrame(payload, 0, 0)
	out := make([]byte, 0, len(token)+len(hdr))
	out = append(out, token...)
	out = append(out, hdr...)
	return out
}

// startFakeHub answers the UpgradeUDP probe handshake. On each [token]["PROBE"]
// it publishes the client's source address on the returned channel and replies
// [replyToken][replyWord]. Passing a mismatched token or a non-"READY" word
// drives the negative handshake cases.
func startFakeHub(t *testing.T, replyToken []byte, replyWord string) (*net.UDPConn, <-chan *net.UDPAddr) {
	t.Helper()
	hub := newUDPListener(t)
	addrCh := make(chan *net.UDPAddr, 1)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := hub.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n >= tokenLen && string(buf[tokenLen:n]) == probeWord {
				select {
				case addrCh <- addr:
				default:
				}
				reply := append(append([]byte{}, replyToken...), []byte(replyWord)...)
				hub.WriteToUDP(reply, addr)
			}
		}
	}()
	return hub, addrCh
}

func testToken() []byte { return []byte{1, 2, 3, 4, 5, 6, 7, 8} }

// TestUpgradeUDP_HappyPath covers matrix item 11: probe/READY round trip
// activates UDP, and a subsequent hub-tagged frame arrives on RecvCh stripped.
func TestUpgradeUDP_HappyPath(t *testing.T) {
	b, _, cleanup := newBind(t, 8)
	defer cleanup()
	defer b.Close()

	token := testToken()
	hub, addrCh := startFakeHub(t, token, readyWord)

	if err := b.UpgradeUDP("127.0.0.1", udpAddr(hub).Port, token); err != nil {
		t.Fatalf("UpgradeUDP: %v", err)
	}
	if !b.UDPActive() {
		t.Fatal("UDPActive() false after a successful upgrade")
	}

	var clientAddr *net.UDPAddr
	select {
	case clientAddr = <-addrCh:
	case <-time.After(2 * time.Second):
		t.Fatal("hub never observed the probe")
	}

	payload := []byte("relayed-wg")
	if _, err := hub.WriteToUDP(hubDataFrame(token, payload), clientAddr); err != nil {
		t.Fatalf("hub data send: %v", err)
	}
	got, ok := recvWithin(b.RecvCh, 2*time.Second)
	if !ok {
		t.Fatal("relayed datagram never arrived on RecvCh")
	}
	if string(got) != "relayed-wg" {
		t.Fatalf("RecvCh payload = %q, want %q", got, "relayed-wg")
	}
}

// TestUpgradeUDP_ProbeTimeout covers matrix item 12: a silent hub yields an
// error after the 2s probe deadline; UDP stays inactive. Real-timer negative
// path, run in parallel with the other two.
func TestUpgradeUDP_ProbeTimeout(t *testing.T) {
	t.Parallel()
	b, _, cleanup := newBind(t, 4)
	defer cleanup()

	hub := newUDPListener(t) // never replies
	start := time.Now()
	err := b.UpgradeUDP("127.0.0.1", udpAddr(hub).Port, testToken())
	if err == nil {
		t.Fatal("expected a probe-timeout error")
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("UpgradeUDP returned after %s; expected it to wait out the ~2s deadline", elapsed)
	}
	if b.UDPActive() {
		t.Fatal("UDPActive() true after a probe timeout")
	}
}

// TestUpgradeUDP_TokenMismatch covers matrix item 13: a READY reply carrying
// the wrong token is rejected.
func TestUpgradeUDP_TokenMismatch(t *testing.T) {
	b, _, cleanup := newBind(t, 4)
	defer cleanup()

	hub, _ := startFakeHub(t, []byte{9, 9, 9, 9, 9, 9, 9, 9}, readyWord)
	if err := b.UpgradeUDP("127.0.0.1", udpAddr(hub).Port, testToken()); err == nil {
		t.Fatal("expected a token-mismatch error")
	}
	if b.UDPActive() {
		t.Fatal("UDPActive() true after a token mismatch")
	}
}

// TestUpgradeUDP_MalformedReply covers matrix item 14: a wrong-length reply is
// rejected.
func TestUpgradeUDP_MalformedReply(t *testing.T) {
	b, _, cleanup := newBind(t, 4)
	defer cleanup()

	hub, _ := startFakeHub(t, testToken(), "READ") // 4-byte word, wrong length
	if err := b.UpgradeUDP("127.0.0.1", udpAddr(hub).Port, testToken()); err == nil {
		t.Fatal("expected a malformed-reply error")
	}
	if b.UDPActive() {
		t.Fatal("UDPActive() true after a malformed reply")
	}
}

// TestUDPReader_BadMagicDropped covers matrix item 15: a hub-sourced frame
// with a bad magic byte is dropped, not delivered to RecvCh.
func TestUDPReader_BadMagicDropped(t *testing.T) {
	b, _, cleanup := newBind(t, 8)
	defer cleanup()
	defer b.Close()

	bindSock := newUDPListener(t)
	hub := newUDPListener(t)
	token := testToken()
	// Field injection happens-before the reader goroutine starts (race-safe).
	b.udpConn = bindSock
	b.udpHubAddr = udpAddr(hub)
	b.udpToken = token
	go b.udpReader()

	bad := append(append([]byte{}, token...), make([]byte, relay.HeaderLen+8)...)
	bad[tokenLen] = 0x00 // not relay.Magic
	if _, err := hub.WriteToUDP(bad, udpAddr(bindSock)); err != nil {
		t.Fatalf("hub send: %v", err)
	}
	if _, ok := recvWithin(b.RecvCh, 200*time.Millisecond); ok {
		t.Fatal("bad-magic frame reached RecvCh; it must be dropped")
	}
}

// TestUDPReader_TPunchActivatesDirect covers matrix item 16: a TPUNCH from a
// non-hub source records directAddr and unblocks AttemptDirect.
func TestUDPReader_TPunchActivatesDirect(t *testing.T) {
	b, _, cleanup := newBind(t, 8)
	defer cleanup()
	defer b.Close()

	bindSock := newUDPListener(t)
	hub := newUDPListener(t)
	peer := newUDPListener(t)
	b.udpConn = bindSock
	b.udpHubAddr = udpAddr(hub)
	b.udpToken = testToken()
	go b.udpReader()

	// The peer answers the bind's TPUNCH probe with its own TPUNCH.
	go func() {
		buf := make([]byte, 1500)
		n, addr, err := peer.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) == directProbeMsg {
			peer.WriteToUDP([]byte(directProbeMsg), addr)
		}
	}()

	if err := b.AttemptDirect(udpAddr(peer).String()); err != nil {
		t.Fatalf("AttemptDirect: %v", err)
	}
	if !b.DirectActive() {
		t.Fatal("DirectActive() false after a successful punch")
	}
	b.directMu.RLock()
	got := b.directAddr
	b.directMu.RUnlock()
	if got == nil || got.Port != udpAddr(peer).Port {
		t.Fatalf("directAddr = %v, want peer port %d", got, udpAddr(peer).Port)
	}
}

// TestUDPReader_DirectDatagramDelivered covers matrix item 17: once direct is
// active, a header-framed datagram from the known peer arrives on RecvCh.
func TestUDPReader_DirectDatagramDelivered(t *testing.T) {
	b, _, cleanup := newBind(t, 8)
	defer cleanup()
	defer b.Close()

	bindSock := newUDPListener(t)
	peer := newUDPListener(t)
	b.udpConn = bindSock
	b.directAddr = udpAddr(peer)
	b.directActive = true
	go b.udpReader()

	payload := []byte("direct-wg-payload")
	// sessionID chosen so bytes 4-7 do not collide with the STUN magic cookie.
	frame := relay.BuildDataFrame(payload, 0, 0x22334455)
	if _, err := peer.WriteToUDP(frame, udpAddr(bindSock)); err != nil {
		t.Fatalf("peer send: %v", err)
	}
	got, ok := recvWithin(b.RecvCh, 2*time.Second)
	if !ok {
		t.Fatal("direct datagram never arrived on RecvCh")
	}
	if string(got) != "direct-wg-payload" {
		t.Fatalf("RecvCh payload = %q, want %q", got, "direct-wg-payload")
	}
}

// TestUDPReader_UnknownSourceDropped covers matrix item 18: a packet from an
// unknown source that is neither STUN nor TPUNCH is dropped from every channel.
func TestUDPReader_UnknownSourceDropped(t *testing.T) {
	b, _, cleanup := newBind(t, 8)
	defer cleanup()
	defer b.Close()

	bindSock := newUDPListener(t)
	hub := newUDPListener(t)
	stranger := newUDPListener(t)
	b.udpConn = bindSock
	b.udpHubAddr = udpAddr(hub)
	go b.udpReader()

	// 30 bytes: bytes 4-7 are 0xABABABAB (not the STUN cookie), length is not 6
	// (not TPUNCH), and the source is neither hub nor direct peer.
	junk := make([]byte, 30)
	for i := range junk {
		junk[i] = 0xAB
	}
	if _, err := stranger.WriteToUDP(junk, udpAddr(bindSock)); err != nil {
		t.Fatalf("stranger send: %v", err)
	}
	if _, ok := recvWithin(b.RecvCh, 200*time.Millisecond); ok {
		t.Fatal("unknown-source packet reached RecvCh")
	}
	if n := len(b.stunCh); n != 0 {
		t.Fatalf("stunCh received %d packets; want 0", n)
	}
	if n := len(b.punchCh); n != 0 {
		t.Fatalf("punchCh received %d signals; want 0", n)
	}
}

// TestAttemptDirect_Timeout covers matrix item 19: a peer that never answers
// TPUNCH yields an error after the 5s punch timeout. Real-timer negative path.
func TestAttemptDirect_Timeout(t *testing.T) {
	t.Parallel()
	b, _, cleanup := newBind(t, 4)
	defer cleanup()

	bindSock := newUDPListener(t)
	peer := newUDPListener(t) // drains implicitly, never answers
	b.udpConn = bindSock

	start := time.Now()
	err := b.AttemptDirect(udpAddr(peer).String())
	if err == nil {
		t.Fatal("expected a hole-punch timeout error")
	}
	if elapsed := time.Since(start); elapsed < 4*time.Second {
		t.Fatalf("AttemptDirect returned after %s; expected it to wait out the ~5s timeout", elapsed)
	}
	if b.DirectActive() {
		t.Fatal("DirectActive() true after a punch timeout")
	}
}

// TestSend_DirectPriority covers matrix item 20: with both direct and UDP
// relay active, Send routes to the direct peer and never touches the hub socket.
func TestSend_DirectPriority(t *testing.T) {
	b, _, cleanup := newBind(t, 4)
	defer cleanup()

	bindSock := newUDPListener(t)
	peer := newUDPListener(t)
	hub := newUDPListener(t)
	b.udpConn = bindSock
	b.directActive = true
	b.directAddr = udpAddr(peer)
	b.udpActive = true
	b.udpHubAddr = udpAddr(hub)
	b.udpToken = testToken()

	if err := b.Send([][]byte{[]byte("priority-wg")}, &endpoint{}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The direct peer receives a [header][payload] frame.
	buf := make([]byte, 1500)
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	n, _, err := peer.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("peer ReadFromUDP: %v", err)
	}
	_, flags, _, body, ok := relay.ParseHeader(buf[:n])
	if !ok || flags != relay.FlagData || string(body) != "priority-wg" {
		t.Fatalf("peer frame malformed: ok=%v flags=0x%02x body=%q", ok, flags, body)
	}

	// The hub relay socket receives nothing.
	if err := hub.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if n2, _, err := hub.ReadFromUDP(buf); err == nil {
		t.Fatalf("hub relay socket received %d bytes; the direct path must not touch it", n2)
	}
}

// TestSend_UDPWriteFailFallsBackToWS covers matrix item 21: a failed UDP-relay
// write falls back to WebSocket and flips udpActive false.
func TestSend_UDPWriteFailFallsBackToWS(t *testing.T) {
	b, peer, cleanup := newBind(t, 4)
	defer cleanup()

	bindSock := newUDPListener(t)
	hub := newUDPListener(t)
	b.udpActive = true
	b.udpConn = bindSock
	b.udpHubAddr = udpAddr(hub)
	b.udpToken = testToken()
	bindSock.Close() // force WriteToUDP to fail

	if err := b.Send([][]byte{[]byte("fallback-wg")}, &endpoint{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mt, msg := readWSMessage(t, peer, 2*time.Second)
	if mt != websocket.BinaryMessage {
		t.Fatalf("message type = %d, want BinaryMessage", mt)
	}
	_, flags, sid, body, ok := relay.ParseHeader(msg)
	if !ok || flags != relay.FlagData || sid != b.sessionID || string(body) != "fallback-wg" {
		t.Fatalf("WS frame malformed: ok=%v flags=0x%02x sid=0x%08x body=%q", ok, flags, sid, body)
	}
	if b.UDPActive() {
		t.Fatal("udpActive still true after a UDP write failure")
	}
}

// TestSend_UDPHealthTimeoutFallsBackToWS covers matrix item 22: a forged stale
// lastUDPRecv against a recent lastUDPSend routes Send through WebSocket. No
// real 60-second wait: the timestamps are stored directly.
func TestSend_UDPHealthTimeoutFallsBackToWS(t *testing.T) {
	b, peer, cleanup := newBind(t, 4)
	defer cleanup()

	bindSock := newUDPListener(t)
	hub := newUDPListener(t)
	b.udpActive = true
	b.udpConn = bindSock
	b.udpHubAddr = udpAddr(hub)
	b.udpToken = testToken()
	atomic.StoreInt64(&b.lastUDPRecv, time.Now().Add(-2*udpHealthTimeout).UnixNano())
	atomic.StoreInt64(&b.lastUDPSend, time.Now().UnixNano())

	if err := b.Send([][]byte{[]byte("healthfail-wg")}, &endpoint{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mt, msg := readWSMessage(t, peer, 2*time.Second)
	if mt != websocket.BinaryMessage {
		t.Fatalf("message type = %d, want BinaryMessage", mt)
	}
	_, flags, _, body, ok := relay.ParseHeader(msg)
	if !ok || flags != relay.FlagData || string(body) != "healthfail-wg" {
		t.Fatalf("WS frame malformed: ok=%v flags=0x%02x body=%q", ok, flags, body)
	}
	if b.UDPActive() {
		t.Fatal("udpActive still true after a UDP health timeout")
	}
}

// TestSTUNDiscover_HappyPath covers matrix item 26: STUNDiscover against a
// local fake responder returns the parsed reflexive address.
func TestSTUNDiscover_HappyPath(t *testing.T) {
	b, _, cleanup := newBind(t, 4)
	defer cleanup()
	defer b.Close()

	bindSock := newUDPListener(t)
	b.udpConn = bindSock
	go b.udpReader()

	stun := newUDPListener(t)
	b.stunAddr = udpAddr(stun).String()

	go func() {
		buf := make([]byte, 1500)
		n, addr, err := stun.ReadFromUDP(buf)
		if err != nil || n < 20 {
			return
		}
		txID := append([]byte{}, buf[8:20]...)
		resp := buildSTUNSuccess(txID, net.IPv4(203, 0, 113, 5), 55555)
		stun.WriteToUDP(resp, addr)
	}()

	got, err := b.STUNDiscover()
	if err != nil {
		t.Fatalf("STUNDiscover: %v", err)
	}
	if got != "203.0.113.5:55555" {
		t.Fatalf("reflexive address = %q, want %q", got, "203.0.113.5:55555")
	}
}

// TestSTUNDiscover_Timeout covers matrix item 27: a silent STUN responder
// yields an error after the 3s timeout. Real-timer negative path.
func TestSTUNDiscover_Timeout(t *testing.T) {
	t.Parallel()
	b, _, cleanup := newBind(t, 4)
	defer cleanup()
	defer b.Close()

	bindSock := newUDPListener(t)
	b.udpConn = bindSock
	go b.udpReader()

	stun := newUDPListener(t) // never replies
	b.stunAddr = udpAddr(stun).String()

	start := time.Now()
	if _, err := b.STUNDiscover(); err == nil {
		t.Fatal("expected a STUN timeout error")
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("STUNDiscover returned after %s; expected it to wait out the ~3s timeout", elapsed)
	}
}

package wsbind

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/paulmooreparks/tela/internal/relay"
)

// newWSPair stands up an httptest WebSocket server that accepts a single
// upgrade and returns the client-side and server-side *websocket.Conn plus a
// cleanup func. The client side is the Bind's transport; the server side
// stands in for the hub/peer end so tests can inspect what the Bind wrote.
func newWSPair(t *testing.T) (client, server *websocket.Conn, cleanup func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	serverCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverCh <- c
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial WebSocket: %v", err)
	}

	select {
	case server = <-serverCh:
	case <-time.After(2 * time.Second):
		client.Close()
		srv.Close()
		t.Fatal("server-side upgrade did not complete")
	}

	cleanup = func() {
		client.Close()
		server.Close()
		srv.Close()
	}
	return client, server, cleanup
}

// newBind constructs a Bind over a fresh WS pair. The returned peer conn is
// the server-side end (the fake hub/peer). Used by every Tier B test that
// needs a real, if mostly inert, WebSocket leg.
func newBind(t *testing.T, bufSize int) (b *Bind, peer *websocket.Conn, cleanup func()) {
	t.Helper()
	client, server, cleanup := newWSPair(t)
	return New(client, bufSize), server, cleanup
}

// readWSMessage reads one message from conn with a bounded deadline, failing
// the test rather than hanging if nothing arrives.
func readWSMessage(t *testing.T, conn *websocket.Conn, budget time.Duration) (msgType int, data []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(budget)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	mt, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	return mt, p
}

// TestSendWS_And_ControlFrame_AreFramed covers matrix item 23's framed half:
// sendWS and SendControlFrame each land on the peer as a binary relay frame
// that ParseHeader accepts, with the expected flags and sessionID.
func TestSendWS_And_ControlFrame_AreFramed(t *testing.T) {
	b, peer, cleanup := newBind(t, 4)
	defer cleanup()

	t.Run("sendWS produces a DATA frame", func(t *testing.T) {
		payload := []byte("wg-datagram")
		if err := b.sendWS([][]byte{payload}); err != nil {
			t.Fatalf("sendWS: %v", err)
		}
		mt, data := readWSMessage(t, peer, 2*time.Second)
		if mt != websocket.BinaryMessage {
			t.Fatalf("message type = %d, want BinaryMessage", mt)
		}
		_, flags, sid, body, ok := relay.ParseHeader(data)
		if !ok {
			t.Fatal("ParseHeader not-ok on sendWS output")
		}
		if flags != relay.FlagData {
			t.Fatalf("flags = 0x%02x, want FlagData", flags)
		}
		if sid != b.sessionID {
			t.Fatalf("sessionID = 0x%08x, want 0x%08x", sid, b.sessionID)
		}
		if string(body) != "wg-datagram" {
			t.Fatalf("payload = %q, want %q", body, "wg-datagram")
		}
	})

	t.Run("SendControlFrame produces a CONTROL frame", func(t *testing.T) {
		if err := b.SendControlFrame([]byte{relay.ControlKeepaliveReq}); err != nil {
			t.Fatalf("SendControlFrame: %v", err)
		}
		mt, data := readWSMessage(t, peer, 2*time.Second)
		if mt != websocket.BinaryMessage {
			t.Fatalf("message type = %d, want BinaryMessage", mt)
		}
		_, flags, sid, body, ok := relay.ParseHeader(data)
		if !ok {
			t.Fatal("ParseHeader not-ok on SendControlFrame output")
		}
		if flags != relay.FlagControl {
			t.Fatalf("flags = 0x%02x, want FlagControl", flags)
		}
		if sid != b.sessionID {
			t.Fatalf("sessionID = 0x%08x, want 0x%08x", sid, b.sessionID)
		}
		if len(body) != 1 || body[0] != relay.ControlKeepaliveReq {
			t.Fatalf("control payload = %v, want [%d]", body, relay.ControlKeepaliveReq)
		}
	})
}

// TestSendText_IsUnframedJSON covers matrix item 23's unframed half: SendText
// rides the WS text channel as raw JSON, carrying no relay header. This pins
// the framed/unframed split (see resolved open_question 7731).
func TestSendText_IsUnframedJSON(t *testing.T) {
	b, peer, cleanup := newBind(t, 4)
	defer cleanup()

	msg := []byte(`{"type":"keepalive"}`)
	if err := b.SendText(msg); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	mt, data := readWSMessage(t, peer, 2*time.Second)
	if mt != websocket.TextMessage {
		t.Fatalf("message type = %d, want TextMessage", mt)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("payload does not decode as JSON: %v", err)
	}
	if decoded["type"] != "keepalive" {
		t.Fatalf("decoded type = %v, want keepalive", decoded["type"])
	}
	if _, _, _, _, ok := relay.ParseHeader(data); ok {
		t.Fatal("relay.ParseHeader accepted an unframed TextMessage; the split is not pinned")
	}
}

// drainWS reads and discards messages from conn until it errors (conn closed),
// so the Bind's writes never block on a full send buffer. It does NOT feed the
// keepalive response channel; callers that want responses wire that separately.
func drainWS(conn *websocket.Conn) {
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

// TestStartSessionKeepalive_HappyPath covers matrix item 24: with a short
// injected interval/timeout and keepalive responses flowing, closeFn is never
// called.
func TestStartSessionKeepalive_HappyPath(t *testing.T) {
	b, peer, cleanup := newBind(t, 4)
	defer cleanup()

	var closed int32
	sawReq := make(chan struct{}, 1)

	interval := 20 * time.Millisecond
	timeout := 80 * time.Millisecond
	respCh, stop := b.StartSessionKeepalive(func() { atomic.AddInt32(&closed, 1) }, interval, timeout)
	defer stop()

	// Peer side: read each keepalive request frame and immediately feed the
	// response channel, keeping the loop's waiting flag reset before timeout.
	go func() {
		for {
			mt, data, err := peer.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.BinaryMessage {
				if _, flags, _, body, ok := relay.ParseHeader(data); ok &&
					flags == relay.FlagControl && len(body) == 1 && body[0] == relay.ControlKeepaliveReq {
					select {
					case sawReq <- struct{}{}:
					default:
					}
					respCh <- struct{}{}
				}
			}
		}
	}()

	// Confirm the round trip actually happened at least once.
	select {
	case <-sawReq:
	case <-time.After(2 * time.Second):
		t.Fatal("no keepalive request observed on the WS peer end")
	}

	// Observe several timeout windows; closeFn must never fire while responses flow.
	time.Sleep(250 * time.Millisecond)
	if n := atomic.LoadInt32(&closed); n != 0 {
		t.Fatalf("closeFn called %d times on the happy path; want 0", n)
	}
}

// TestStartSessionKeepalive_Characterization covers matrix item 25: a
// two-aspect characterization of the tela-69 timeout defect.
//
// Aspect (a) pins current behavior: bind.go's keepalive loop resends and
// resets its own lastReqAt on every tick, so with interval < timeout (the
// production ratio) time.Since(lastReqAt) is always ~interval and the
// `waiting && elapsed >= timeout` branch is unreachable. closeFn therefore
// never fires even across many timeout windows with no responses. When
// tela-69 lands and the loop starts the clock once per outstanding request,
// this aspect flips to asserting closeFn fires within the timeout budget.
//
// Aspect (b) proves the close mechanism itself works: with interval >=
// timeout, the elapsed check can succeed and closeFn fires exactly once.
func TestStartSessionKeepalive_Characterization(t *testing.T) {
	t.Run("interval<timeout never fires (tela-69)", func(t *testing.T) {
		b, peer, cleanup := newBind(t, 4)
		defer cleanup()
		drainWS(peer) // read requests, never respond

		var closed int32
		interval := 50 * time.Millisecond
		timeout := 100 * time.Millisecond
		_, stop := b.StartSessionKeepalive(func() { atomic.AddInt32(&closed, 1) }, interval, timeout)
		defer stop()

		// Five timeout windows with zero responses. Per tela-69, closeFn is
		// unreachable at this ratio and must stay uncalled.
		time.Sleep(500 * time.Millisecond)
		if n := atomic.LoadInt32(&closed); n != 0 {
			t.Fatalf("closeFn fired %d times at interval<timeout; tela-69 says it must stay 0", n)
		}
	})

	t.Run("interval>=timeout fires exactly once", func(t *testing.T) {
		b, peer, cleanup := newBind(t, 4)
		defer cleanup()
		drainWS(peer) // read requests, never respond

		var closed int32
		fired := make(chan struct{}, 1)
		interval := 100 * time.Millisecond
		timeout := 50 * time.Millisecond
		_, stop := b.StartSessionKeepalive(func() {
			atomic.AddInt32(&closed, 1)
			select {
			case fired <- struct{}{}:
			default:
			}
		}, interval, timeout)
		defer stop()

		select {
		case <-fired:
		case <-time.After(2 * time.Second):
			t.Fatal("closeFn never fired at interval>=timeout")
		}
		// The goroutine returns after closeFn, so a second call is impossible;
		// confirm the count stays at one over a further window.
		time.Sleep(250 * time.Millisecond)
		if n := atomic.LoadInt32(&closed); n != 1 {
			t.Fatalf("closeFn fired %d times; want exactly 1", n)
		}
	})
}

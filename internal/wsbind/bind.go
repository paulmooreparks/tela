/*
  Package wsbind implements a WireGuard conn.Bind that transports
  WireGuard datagrams as binary WebSocket messages.

  This is the key adapter that allows WireGuard to operate over
  the existing Tela Hub WebSocket relay. The Hub sees only opaque
  binary messages — it cannot decrypt or inspect the contents.

  Architecture (DESIGN.md §6.8):
    WireGuard device ←→ wsBind ←→ WebSocket ←→ Hub relay ←→ WebSocket ←→ wsBind ←→ WireGuard device

  Thread safety:
    - Sends are serialized via writeMu (gorilla/websocket requires single writer).
    - Receives are delivered through a buffered channel from an external reader goroutine.
    - Close is idempotent and safe to call from any goroutine.
*/
package wsbind

import (
	"errors"
	"log"
	"net/netip"
	"sync"

	"github.com/gorilla/websocket"
	"golang.zx2c4.com/wireguard/conn"
)

// Bind implements conn.Bind for WireGuard-over-WebSocket transport.
// The caller must feed received binary WebSocket messages into RecvCh.
type Bind struct {
	ws      *websocket.Conn
	writeMu sync.Mutex // gorilla/websocket requires serialized writes
	RecvCh  chan []byte // binary WG datagrams from the reader goroutine
	closed  chan struct{}
	once    sync.Once
}

// New creates a Bind using the given WebSocket connection.
// bufSize controls the receive channel buffer depth.
func New(ws *websocket.Conn, bufSize int) *Bind {
	return &Bind{
		ws:     ws,
		RecvCh: make(chan []byte, bufSize),
		closed: make(chan struct{}),
	}
}

// Send writes WireGuard datagrams to the WebSocket as binary messages.
func (b *Bind) Send(bufs [][]byte, ep conn.Endpoint) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	for _, buf := range bufs {
		if err := b.ws.WriteMessage(websocket.BinaryMessage, buf); err != nil {
			return err
		}
	}
	return nil
}

// Open returns the receive function. Port is ignored (we don't use UDP).
func (b *Bind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	return []conn.ReceiveFunc{b.receiveFunc}, 0, nil
}

// receiveFunc blocks until a WireGuard datagram arrives on RecvCh.
func (b *Bind) receiveFunc(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
	select {
	case data, ok := <-b.RecvCh:
		if !ok {
			return 0, errors.New("bind closed")
		}
		n := copy(bufs[0], data)
		sizes[0] = n
		eps[0] = &endpoint{}
		return 1, nil
	case <-b.closed:
		return 0, errors.New("bind closed")
	}
}

// Close shuts down the bind. Safe to call multiple times.
func (b *Bind) Close() error {
	b.once.Do(func() {
		close(b.closed)
		log.Printf("[wsbind] closed")
	})
	return nil
}

// SetMark is a no-op (SO_MARK is irrelevant for WebSocket transport).
func (b *Bind) SetMark(mark uint32) error { return nil }

// ParseEndpoint returns a static endpoint — there's only one peer per WebSocket.
func (b *Bind) ParseEndpoint(s string) (conn.Endpoint, error) {
	return &endpoint{}, nil
}

// BatchSize returns 1 (no batching over WebSocket).
func (b *Bind) BatchSize() int { return 1 }

// SendText sends a text (JSON control) message through the same WebSocket,
// serialized with WireGuard datagram sends to avoid concurrent writes.
func (b *Bind) SendText(data []byte) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	return b.ws.WriteMessage(websocket.TextMessage, data)
}

// endpoint is a trivial implementation — one peer per WebSocket.
type endpoint struct{}

func (e *endpoint) ClearSrc()            {}
func (e *endpoint) SrcToString() string   { return "" }
func (e *endpoint) DstToString() string   { return "ws" }
func (e *endpoint) DstToBytes() []byte    { return nil }
func (e *endpoint) DstIP() netip.Addr     { return netip.Addr{} }
func (e *endpoint) SrcIP() netip.Addr     { return netip.Addr{} }

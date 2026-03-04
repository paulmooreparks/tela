/*
  Package wsbind implements a WireGuard conn.Bind that transports
  WireGuard datagrams as binary WebSocket messages, with an optional
  upgrade to UDP relay for better performance.

  Architecture:
    WireGuard device ←→ wsBind ←→ WebSocket (or UDP) ←→ Hub relay

  Transport modes:
    1. WebSocket (default) — works through any HTTP proxy / Cloudflare.
    2. UDP relay (upgrade)  — eliminates TCP-over-TCP. Hub relays raw
       UDP datagrams tagged with an 8-byte session token.

  Thread safety:
    - WebSocket sends serialized via writeMu.
    - UDP sends are inherently goroutine-safe (single socket).
    - Receives from both transports merge into RecvCh.
    - Close is idempotent; bind supports Close→Open cycles.
*/
package wsbind

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.zx2c4.com/wireguard/conn"
)

const (
	tokenLen  = 8
	probeWord = "PROBE"
	readyWord = "READY"
)

// Bind implements conn.Bind for WireGuard-over-WebSocket transport
// with optional UDP relay upgrade.
//
// WireGuard calls Close() then Open() during device state transitions
// (e.g. Up). The bind must support this cycle — Close/Open are
// re-entrant and reset the receive path each time.
type Bind struct {
	ws      *websocket.Conn
	writeMu sync.Mutex // gorilla/websocket requires serialized writes
	RecvCh  chan []byte // binary WG datagrams from the reader goroutine(s)
	mu      sync.Mutex // protects closed, open
	closed  chan struct{}
	open    bool

	// UDP relay (activated by UpgradeUDP)
	udpMu      sync.RWMutex
	udpConn    *net.UDPConn
	udpHubAddr *net.UDPAddr
	udpToken   []byte // 8-byte session token, prepended to outgoing UDP
	udpActive  bool
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

// Send writes WireGuard datagrams via UDP (if upgraded) or WebSocket.
func (b *Bind) Send(bufs [][]byte, ep conn.Endpoint) error {
	b.udpMu.RLock()
	useUDP := b.udpActive
	udpConn := b.udpConn
	hubAddr := b.udpHubAddr
	token := b.udpToken
	b.udpMu.RUnlock()

	if useUDP && udpConn != nil {
		for _, buf := range bufs {
			pkt := make([]byte, tokenLen+len(buf))
			copy(pkt, token)
			copy(pkt[tokenLen:], buf)
			if _, err := udpConn.WriteToUDP(pkt, hubAddr); err != nil {
				// UDP write failed — fall back to WebSocket for this batch
				log.Printf("[wsbind] UDP send failed, falling back to WebSocket: %v", err)
				b.udpMu.Lock()
				b.udpActive = false
				b.udpMu.Unlock()
				return b.sendWS(bufs)
			}
		}
		return nil
	}

	return b.sendWS(bufs)
}

// sendWS writes datagrams through the WebSocket.
func (b *Bind) sendWS(bufs [][]byte) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	for _, buf := range bufs {
		if err := b.ws.WriteMessage(websocket.BinaryMessage, buf); err != nil {
			return err
		}
	}
	return nil
}

// UpgradeUDP attempts to switch from WebSocket to UDP relay.
// hubHost is the hub's hostname, hubPort is the UDP port, and token
// is the 8-byte session token assigned by the hub.
//
// Returns nil on success (UDP is now active). On failure, returns an
// error and the bind continues using WebSocket transparently.
func (b *Bind) UpgradeUDP(hubHost string, hubPort int, token []byte) error {
	if len(token) != tokenLen {
		return fmt.Errorf("token must be %d bytes, got %d", tokenLen, len(token))
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", hubHost, hubPort))
	if err != nil {
		return fmt.Errorf("resolve hub UDP: %w", err)
	}

	udpConn, err := net.ListenUDP("udp", nil) // ephemeral local port
	if err != nil {
		return fmt.Errorf("listen UDP: %w", err)
	}

	// Send probe
	probe := make([]byte, tokenLen+len(probeWord))
	copy(probe, token)
	copy(probe[tokenLen:], probeWord)
	if _, err := udpConn.WriteToUDP(probe, addr); err != nil {
		udpConn.Close()
		return fmt.Errorf("probe send: %w", err)
	}

	// Wait for READY (2 second timeout)
	udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := udpConn.ReadFromUDP(buf)
	if err != nil {
		udpConn.Close()
		return fmt.Errorf("probe timeout: %w", err)
	}

	// Verify: [token]["READY"]
	expect := tokenLen + len(readyWord)
	if n != expect || string(buf[tokenLen:n]) != readyWord {
		udpConn.Close()
		return fmt.Errorf("unexpected probe response (%d bytes)", n)
	}
	// Verify token matches
	for i := 0; i < tokenLen; i++ {
		if buf[i] != token[i] {
			udpConn.Close()
			return fmt.Errorf("probe response token mismatch")
		}
	}

	// Clear deadline for ongoing reads
	udpConn.SetReadDeadline(time.Time{})

	// Activate UDP
	b.udpMu.Lock()
	b.udpConn = udpConn
	b.udpHubAddr = addr
	b.udpToken = make([]byte, tokenLen)
	copy(b.udpToken, token)
	b.udpActive = true
	b.udpMu.Unlock()

	// Start UDP reader — feeds into the same RecvCh as WebSocket
	go b.udpReader()

	log.Printf("[wsbind] upgraded to UDP relay (%s:%d)", hubHost, hubPort)
	return nil
}

// UDPActive reports whether the bind is currently using UDP.
func (b *Bind) UDPActive() bool {
	b.udpMu.RLock()
	defer b.udpMu.RUnlock()
	return b.udpActive
}

// udpReader reads from the UDP socket and delivers WG datagrams to RecvCh.
func (b *Bind) udpReader() {
	buf := make([]byte, 1500)
	for {
		n, _, err := b.udpConn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed
		}
		if n <= tokenLen {
			continue // too short — drop
		}
		// Strip token prefix, deliver WG datagram
		datagram := make([]byte, n-tokenLen)
		copy(datagram, buf[tokenLen:n])
		select {
		case b.RecvCh <- datagram:
		default:
			// buffer full — drop
		}
	}
}

// Open returns the receive function.
// WireGuard calls Open after Close during state transitions, so this
// resets the closed channel to allow receiveFunc to work again.
func (b *Bind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Reset the closed channel so receiveFunc unblocks
	b.closed = make(chan struct{})
	b.open = true
	log.Printf("[wsbind] opened")
	return []conn.ReceiveFunc{b.receiveFunc}, 0, nil
}

// receiveFunc blocks until a WireGuard datagram arrives on RecvCh.
func (b *Bind) receiveFunc(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()

	select {
	case data, ok := <-b.RecvCh:
		if !ok {
			return 0, errors.New("bind closed")
		}
		n := copy(bufs[0], data)
		sizes[0] = n
		eps[0] = &endpoint{}
		return 1, nil
	case <-closed:
		return 0, errors.New("bind closed")
	}
}

// Close signals all receiveFunc goroutines to stop and closes the UDP
// socket if active. Safe to call multiple times.
func (b *Bind) Close() error {
	b.mu.Lock()
	if b.open {
		close(b.closed)
		b.open = false
		log.Printf("[wsbind] closed")
	}
	b.mu.Unlock()

	// Close UDP socket if active
	b.udpMu.Lock()
	if b.udpConn != nil {
		b.udpConn.Close()
		b.udpConn = nil
		b.udpActive = false
	}
	b.udpMu.Unlock()

	return nil
}

// SetMark is a no-op (SO_MARK is irrelevant for WebSocket/UDP relay transport).
func (b *Bind) SetMark(mark uint32) error { return nil }

// ParseEndpoint returns a static endpoint — there's only one peer per bind.
func (b *Bind) ParseEndpoint(s string) (conn.Endpoint, error) {
	return &endpoint{}, nil
}

// BatchSize returns 1 (no batching).
func (b *Bind) BatchSize() int { return 1 }

// SendText sends a text (JSON control) message through the WebSocket,
// serialized with WireGuard datagram sends to avoid concurrent writes.
func (b *Bind) SendText(data []byte) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	return b.ws.WriteMessage(websocket.TextMessage, data)
}

// endpoint is a trivial implementation — one peer per bind.
type endpoint struct{}

func (e *endpoint) ClearSrc()            {}
func (e *endpoint) SrcToString() string   { return "" }
func (e *endpoint) DstToString() string   { return "ws" }
func (e *endpoint) DstToBytes() []byte    { return nil }
func (e *endpoint) DstIP() netip.Addr     { return netip.Addr{} }
func (e *endpoint) SrcIP() netip.Addr     { return netip.Addr{} }

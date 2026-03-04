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
	crand "crypto/rand"
	"encoding/binary"
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

	// Phase 3: STUN + direct tunnel
	stunMagicCookie = 0x2112A442
	stunServer      = "stun.l.google.com:19302"
	directProbeMsg  = "TPUNCH"
	stunTimeout     = 3 * time.Second
	punchTimeout    = 5 * time.Second
	punchInterval   = 100 * time.Millisecond
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

	// Direct tunnel (Phase 3: STUN + hole punch)
	directMu     sync.RWMutex
	directAddr   *net.UDPAddr // peer's actual UDP address
	directActive bool

	// STUN / hole-punch signaling
	stunCh  chan []byte      // STUN binding responses routed here by udpReader
	punchCh chan *net.UDPAddr // hole-punch probe signals from udpReader
}

// New creates a Bind using the given WebSocket connection.
// bufSize controls the receive channel buffer depth.
func New(ws *websocket.Conn, bufSize int) *Bind {
	return &Bind{
		ws:      ws,
		RecvCh:  make(chan []byte, bufSize),
		closed:  make(chan struct{}),
		stunCh:  make(chan []byte, 2),
		punchCh: make(chan *net.UDPAddr, 1),
	}
}

// Send writes WireGuard datagrams. Priority: direct → UDP relay → WebSocket.
func (b *Bind) Send(bufs [][]byte, ep conn.Endpoint) error {
	// Check direct tunnel first (Phase 3)
	b.directMu.RLock()
	useDirect := b.directActive
	dAddr := b.directAddr
	b.directMu.RUnlock()

	b.udpMu.RLock()
	useUDP := b.udpActive
	udpConn := b.udpConn
	hubAddr := b.udpHubAddr
	token := b.udpToken
	b.udpMu.RUnlock()

	// 1. Direct (raw WG datagrams to peer, no token prefix)
	if useDirect && dAddr != nil && udpConn != nil {
		allOk := true
		for _, buf := range bufs {
			if _, err := udpConn.WriteToUDP(buf, dAddr); err != nil {
				log.Printf("[wsbind] direct send failed, falling back: %v", err)
				b.directMu.Lock()
				b.directActive = false
				b.directMu.Unlock()
				allOk = false
				break
			}
		}
		if allOk {
			return nil
		}
		// Direct failed — fall through to relay
	}

	// 2. UDP relay (token-prefixed datagrams via hub)
	if useUDP && udpConn != nil {
		for _, buf := range bufs {
			pkt := make([]byte, tokenLen+len(buf))
			copy(pkt, token)
			copy(pkt[tokenLen:], buf)
			if _, err := udpConn.WriteToUDP(pkt, hubAddr); err != nil {
				log.Printf("[wsbind] UDP send failed, falling back to WebSocket: %v", err)
				b.udpMu.Lock()
				b.udpActive = false
				b.udpMu.Unlock()
				return b.sendWS(bufs)
			}
		}
		return nil
	}

	// 3. WebSocket (always available)
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

// udpReader reads from the UDP socket, classifies packets by source
// and content, and routes them to the appropriate handler.
//
// Packet classification:
//  1. From hub address → relayed WG datagram (strip token prefix)
//  2. STUN magic cookie at bytes 4-7 → STUN binding response → stunCh
//  3. "TPUNCH" → hole-punch probe → record peer addr, signal punchCh
//  4. From known direct peer → raw WG datagram → RecvCh
func (b *Bind) udpReader() {
	buf := make([]byte, 1500)
	for {
		n, addr, err := b.udpConn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed
		}

		// 1. From hub relay? (token-prefixed WG datagram)
		b.udpMu.RLock()
		hubAddr := b.udpHubAddr
		b.udpMu.RUnlock()
		if hubAddr != nil && addr.IP.Equal(hubAddr.IP) && addr.Port == hubAddr.Port {
			if n <= tokenLen {
				continue
			}
			datagram := make([]byte, n-tokenLen)
			copy(datagram, buf[tokenLen:n])
			select {
			case b.RecvCh <- datagram:
			default:
			}
			continue
		}

		// 2. STUN binding response? (magic cookie at bytes 4-7)
		if n >= 20 && binary.BigEndian.Uint32(buf[4:8]) == stunMagicCookie {
			pkt := make([]byte, n)
			copy(pkt, buf[:n])
			select {
			case b.stunCh <- pkt:
			default:
			}
			continue
		}

		// 3. Hole-punch probe?
		if n == len(directProbeMsg) && string(buf[:n]) == directProbeMsg {
			punchAddr := &net.UDPAddr{
				IP:   make(net.IP, len(addr.IP)),
				Port: addr.Port,
			}
			copy(punchAddr.IP, addr.IP)
			// Record peer address immediately (enables receiving direct WG packets)
			b.directMu.Lock()
			b.directAddr = punchAddr
			b.directMu.Unlock()
			// Signal AttemptDirect that the path is open
			select {
			case b.punchCh <- punchAddr:
			default:
			}
			continue
		}

		// 4. Direct WG datagram from known peer?
		b.directMu.RLock()
		dAddr := b.directAddr
		b.directMu.RUnlock()
		if dAddr != nil && addr.IP.Equal(dAddr.IP) && addr.Port == dAddr.Port {
			datagram := make([]byte, n)
			copy(datagram, buf[:n])
			select {
			case b.RecvCh <- datagram:
			default:
			}
			continue
		}
		// Unknown source — drop
	}
}

// STUNDiscover performs a STUN Binding Request (RFC 5389) on the existing
// UDP socket and returns the server-reflexive address (public IP:port).
// This must be called after UpgradeUDP succeeds.
func (b *Bind) STUNDiscover() (string, error) {
	b.udpMu.RLock()
	udpConn := b.udpConn
	b.udpMu.RUnlock()
	if udpConn == nil {
		return "", errors.New("no UDP socket for STUN")
	}

	// Build STUN Binding Request
	txID := make([]byte, 12)
	if _, err := crand.Read(txID); err != nil {
		return "", fmt.Errorf("generate STUN txID: %w", err)
	}

	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:2], 0x0001) // Binding Request
	binary.BigEndian.PutUint16(req[2:4], 0)       // Message Length (no attrs)
	binary.BigEndian.PutUint32(req[4:8], stunMagicCookie)
	copy(req[8:20], txID)

	stunAddr, err := net.ResolveUDPAddr("udp4", stunServer)
	if err != nil {
		return "", fmt.Errorf("resolve STUN server: %w", err)
	}

	if _, err := udpConn.WriteToUDP(req, stunAddr); err != nil {
		return "", fmt.Errorf("STUN send: %w", err)
	}

	// Wait for response (routed by udpReader to stunCh)
	select {
	case resp := <-b.stunCh:
		return parseSTUNResponse(resp, txID)
	case <-time.After(stunTimeout):
		return "", errors.New("STUN timeout")
	}
}

// parseSTUNResponse extracts the XOR-MAPPED-ADDRESS from a STUN Binding
// Success Response and returns it as "IP:port".
func parseSTUNResponse(resp []byte, txID []byte) (string, error) {
	if len(resp) < 20 {
		return "", errors.New("STUN response too short")
	}

	msgType := binary.BigEndian.Uint16(resp[0:2])
	if msgType != 0x0101 { // Binding Success Response
		return "", fmt.Errorf("unexpected STUN type: 0x%04x", msgType)
	}

	if binary.BigEndian.Uint32(resp[4:8]) != stunMagicCookie {
		return "", errors.New("STUN magic cookie mismatch")
	}

	for i := 0; i < 12; i++ {
		if resp[8+i] != txID[i] {
			return "", errors.New("STUN transaction ID mismatch")
		}
	}

	msgLen := int(binary.BigEndian.Uint16(resp[2:4]))
	if 20+msgLen > len(resp) {
		return "", errors.New("STUN message length exceeds packet")
	}
	attrs := resp[20 : 20+msgLen]

	for len(attrs) >= 4 {
		attrType := binary.BigEndian.Uint16(attrs[0:2])
		attrLen := int(binary.BigEndian.Uint16(attrs[2:4]))
		if 4+attrLen > len(attrs) {
			break
		}
		attrVal := attrs[4 : 4+attrLen]

		if attrType == 0x0020 && attrLen >= 8 { // XOR-MAPPED-ADDRESS
			if attrVal[1] == 0x01 { // IPv4
				xPort := binary.BigEndian.Uint16(attrVal[2:4])
				port := xPort ^ uint16(stunMagicCookie>>16)
				xIP := binary.BigEndian.Uint32(attrVal[4:8])
				ip := xIP ^ stunMagicCookie
				return fmt.Sprintf("%d.%d.%d.%d:%d",
					byte(ip>>24), byte(ip>>16), byte(ip>>8), byte(ip), port), nil
			}
		}

		padded := (attrLen + 3) &^ 3
		attrs = attrs[4+padded:]
	}

	return "", errors.New("no XOR-MAPPED-ADDRESS in STUN response")
}

// AttemptDirect initiates UDP hole punching to the given peer address.
// It sends probe packets and waits for a response from the peer.
// On success, the bind routes WG datagrams directly to the peer.
// On failure, returns an error and the bind continues using UDP relay or WebSocket.
func (b *Bind) AttemptDirect(peerAddrStr string) error {
	peerAddr, err := net.ResolveUDPAddr("udp", peerAddrStr)
	if err != nil {
		return fmt.Errorf("resolve peer: %w", err)
	}

	b.udpMu.RLock()
	udpConn := b.udpConn
	b.udpMu.RUnlock()
	if udpConn == nil {
		return errors.New("no UDP socket for hole punch")
	}

	// Drain any stale punch signals
	for len(b.punchCh) > 0 {
		<-b.punchCh
	}

	probe := []byte(directProbeMsg)
	timeout := time.After(punchTimeout)
	ticker := time.NewTicker(punchInterval)
	defer ticker.Stop()

	log.Printf("[wsbind] hole-punching → %s", peerAddr)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("hole punch timeout (%s)", peerAddr)
		case <-ticker.C:
			udpConn.WriteToUDP(probe, peerAddr)
		case actualAddr := <-b.punchCh:
			b.directMu.Lock()
			b.directAddr = actualAddr
			b.directActive = true
			b.directMu.Unlock()
			log.Printf("[wsbind] direct tunnel active → %s", actualAddr)
			return nil
		}
	}
}

// DirectActive reports whether the bind is using a direct peer-to-peer path.
func (b *Bind) DirectActive() bool {
	b.directMu.RLock()
	defer b.directMu.RUnlock()
	return b.directActive
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

	// Reset direct tunnel state
	b.directMu.Lock()
	b.directActive = false
	b.directAddr = nil
	b.directMu.Unlock()

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

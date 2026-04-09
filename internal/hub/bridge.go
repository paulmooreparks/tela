package hub

// Bridge session lifecycle for hub-to-hub transit (DESIGN-relay-gateway.md, sections 3-5).
//
// Topology (Shape B, session-aware forwarding):
//
//	Client ---- Hub-A ---- Hub-B ---- Agent
//	        leg 1     bridge    leg 2
//
// Hub-A maintains one outbound WebSocket to Hub-B per bridged session (v1
// one-WS-per-session model). The bridge leg always uses WebSocket in v1;
// the client-to-Hub-A leg negotiates UDP relay independently.
//
// When a client requests a machine listed in Hub-A's bridges config:
//  1. Hub-A opens an outbound WS to Hub-B and sends a connect message
//     forwarding the client's WG public key and the bridge token.
//  2. Hub-B runs its normal session setup and sends ready with Hub-B's
//     session index (which determines both ends' WireGuard addresses).
//  3. Hub-A sends ready to the client using Hub-B's session index.
//  4. Hub-A creates a leg-1 UDP token for client-to-Hub-A UDP relay.
//  5. Hub-A cross-links the client and bridge WS connections for relay.
//  6. A runBridgeReader goroutine forwards Hub-B messages to the client.
//     The existing handleWSConnection loop forwards client messages to Hub-B.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/paulmooreparks/tela/internal/relay"
)

// bridgeConfig describes one hub-to-hub bridge in telahubd.yaml.
type bridgeConfig struct {
	HubID    string   `yaml:"hubId"`
	URL      string   `yaml:"url"`               // wss://... WebSocket URL of the destination hub
	Token    string   `yaml:"token"`             // connect token on the destination hub
	MaxHops  uint8    `yaml:"maxHops,omitempty"` // relay TTL; 0 means relay.DefaultMaxHops
	Machines []string `yaml:"machines"`          // machine names reachable via this bridge
}

// BridgeConfig is the exported name for bridgeConfig. It lets callers such
// as the in-process test harness (internal/teststack) construct bridge
// entries without a YAML round-trip.
type BridgeConfig = bridgeConfig

func (bc *bridgeConfig) effectiveMaxHops() uint8 {
	if bc.MaxHops > 0 {
		return bc.MaxHops
	}
	return relay.DefaultMaxHops
}

// ── Bridge directory ────────────────────────────────────────────────

// bridgeDir maps machine name to the bridge config that provides access to it.
// Populated at startup from hubConfig.Bridges; read by handleConnect.
var (
	bridgeDirMu sync.RWMutex
	bridgeDir   = make(map[string]*bridgeConfig)
)

// initBridgeDir rebuilds the bridge machine directory from config.
// Called once in applyHubConfig.
func initBridgeDir(cfg *hubConfig) {
	bridgeDirMu.Lock()
	defer bridgeDirMu.Unlock()
	bridgeDir = make(map[string]*bridgeConfig)
	for i := range cfg.Bridges {
		bc := &cfg.Bridges[i]
		for _, m := range bc.Machines {
			bridgeDir[m] = bc
		}
	}
	if len(bridgeDir) > 0 {
		log.Printf("[hub] bridge directory: %d machines across %d bridge(s)", len(bridgeDir), len(cfg.Bridges))
	}
}

// ── Bridge session setup ────────────────────────────────────────────

// handleBridgeConnect is called by handleConnect when the requested machine
// is in the bridge directory rather than the local machines map. It runs the
// full bridge session setup: dial Hub-B, exchange signaling, pair legs.
func handleBridgeConnect(clientSC *safeConn, state *wsState, msg *signalingMsg, bc *bridgeConfig) {
	machineName := msg.MachineID

	// Hub-A auth: the client must have connect permission on Hub-A.
	if globalAuth.isEnabled() && !globalAuth.canConnect(msg.Token, machineName) {
		id := globalAuth.identityID(msg.Token)
		if id == "" {
			id = "unknown"
		}
		log.Printf("[hub] bridge connect denied: %s (identity: %s)", machineName, id)
		sendError(clientSC, "Access denied")
		return
	}

	// Open outbound WebSocket to Hub-B (bridge leg).
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	bridgeConn, _, err := dialer.Dial(bc.URL, nil)
	if err != nil {
		log.Printf("[hub] bridge dial %s: %v", bc.URL, err)
		sendError(clientSC, "Bridge unavailable")
		return
	}

	// Send connect to Hub-B, forwarding the client's WG public key.
	connectReq := map[string]any{
		"type":      "connect",
		"machineId": machineName,
		"wgPubKey":  msg.WGPubKey,
		"token":     bc.Token,
	}
	if msg.Ports != nil {
		connectReq["ports"] = msg.Ports
	}
	if err := bridgeConn.WriteJSON(connectReq); err != nil {
		bridgeConn.Close()
		log.Printf("[hub] bridge connect send: %v", err)
		sendError(clientSC, "Bridge connect failed")
		return
	}

	// Signaling phase: consume Hub-B messages until "ready" (30 s timeout).
	// Hub-B runs normal session setup internally; we just wait for the result.
	bridgeConn.SetReadDeadline(time.Now().Add(30 * time.Second))
	sessionIdx := 0
	for {
		msgType, data, err := bridgeConn.ReadMessage()
		if err != nil {
			bridgeConn.Close()
			log.Printf("[hub] bridge signaling: %v", err)
			sendError(clientSC, "Bridge session setup failed")
			return
		}
		if msgType != websocket.TextMessage {
			continue // stray binary before pairing; ignore
		}
		var bMsg map[string]any
		if err := json.Unmarshal(data, &bMsg); err != nil {
			continue
		}
		switch bMsg["type"] {
		case "ready":
			if idx, ok := bMsg["sessionIdx"].(float64); ok {
				sessionIdx = int(idx)
			}
			// Exit signaling loop.
		case "error":
			bridgeConn.Close()
			errMsg, _ := bMsg["message"].(string)
			log.Printf("[hub] bridge: Hub-B error for %s: %s", machineName, errMsg)
			sendError(clientSC, errMsg)
			return
		case "udp-offer":
			// Hub-B offers UDP relay on the bridge leg. v1 stays on WebSocket.
			continue
		default:
			continue
		}
		if bMsg["type"] == "ready" {
			break
		}
	}
	bridgeConn.SetReadDeadline(time.Time{}) // clear deadline for data phase

	// Wrap bridge connection in safeConn for serialized writes.
	bridgeSC := newSafeConn(bridgeConn)

	// Generate Hub-A session ID and leg-1 UDP relay token.
	sidBytes := make([]byte, 8)
	rand.Read(sidBytes)
	sessionID := hex.EncodeToString(sidBytes)

	const legOneTokenLen = 8
	clientToken := make([]byte, legOneTokenLen)
	rand.Read(clientToken)
	clientTokenHex := hex.EncodeToString(clientToken)

	// Register the leg-1 UDP token. PeerTokenHex is empty for bridge sessions;
	// runUDPRelay writes directly to PeerWS (the bridge WS) when PeerTokenHex == "".
	udpSessionsMu.Lock()
	udpSessions[clientTokenHex] = &udpSession{
		PeerTokenHex: "", // no peer UDP token; PeerWS is the bridge WS
		PeerWS:       bridgeSC,
		Role:         "client",
		MachineID:    machineName,
		CreatedAt:    time.Now(),
	}
	udpSessionsMu.Unlock()

	// Update client state and cross-link with bridge WS.
	state.Role = "client"
	state.MachineName = machineName
	state.WGPubKey = msg.WGPubKey
	state.SessionID = sessionID
	state.Paired = true

	wsStatesMu.Lock()
	state.Peer = bridgeSC
	state.BridgeWS = bridgeSC
	wsStates[bridgeConn] = &wsState{
		Role:      "bridge",
		MachineID: machineName,
		SessionID: sessionID,
		Paired:    true,
		Peer:      clientSC,
	}
	wsStatesMu.Unlock()

	// Send ready to client. Use Hub-B's sessionIdx so both WireGuard
	// endpoints (client and agent) use the same 10.77.{idx} subnet.
	ready, _ := json.Marshal(map[string]any{"type": "ready", "sessionIdx": sessionIdx})
	clientSC.WriteMessage(websocket.TextMessage, ready)

	// Send leg-1 udp-offer to client (client <-> Hub-A UDP relay).
	offer := map[string]any{"type": "udp-offer", "port": udpPort, "token": clientTokenHex}
	if udpHost != "" {
		offer["host"] = udpHost
	}
	offerData, _ := json.Marshal(offer)
	clientSC.WriteMessage(websocket.TextMessage, offerData)

	recordEvent(machineName, "client-connect-bridge", fmt.Sprintf("via=%s session=%s", bc.HubID, sessionID[:8]))
	log.Printf("[hub] bridge session started: %s via %s session=%s", machineName, bc.HubID, sessionID[:8])

	// Launch the bridge-to-client relay goroutine.
	go runBridgeReader(bridgeSC, clientSC, sessionID, machineName, clientTokenHex, bc)
}

// runBridgeReader relays messages from Hub-B to the client and handles
// session teardown when the bridge leg closes. The client-to-Hub-B
// direction is handled by the existing handleWSConnection relay loop
// (clientState.Peer = bridgeSC).
func runBridgeReader(bridgeSC, clientSC *safeConn, sessionID, machineName, clientTokenHex string, bc *bridgeConfig) {
	defer func() {
		bridgeSC.Close()

		// Remove leg-1 UDP token.
		udpSessionsMu.Lock()
		delete(udpSessions, clientTokenHex)
		udpSessionsMu.Unlock()

		// Unregister bridge WS.
		wsStatesMu.Lock()
		delete(wsStates, bridgeSC.Conn)
		wsStatesMu.Unlock()

		recordEvent(machineName, "client-disconnect-bridge", fmt.Sprintf("session=%s", sessionID[:8]))
		log.Printf("[hub] bridge session ended: %s session=%s", machineName, sessionID[:8])

		// Signal client that the session is over.
		clientSC.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, "bridge session ended"))
		clientSC.Close()
	}()

	maxHops := bc.effectiveMaxHops()

	for {
		msgType, data, err := bridgeSC.ReadMessage()
		if err != nil {
			return // bridge WS closed; teardown via defer
		}

		switch msgType {
		case websocket.BinaryMessage:
			// Relay frame: validate and advance the hop count.
			forwarded, ok := relay.ForwardFrame(data, maxHops)
			if !ok {
				if len(data) > 0 && data[0] != relay.Magic {
					log.Printf("[hub] bridge reader: bad magic 0x%02x from Hub-B for %s", data[0], machineName)
				} else {
					log.Printf("[hub] bridge reader: frame dropped (TTL) for %s", machineName)
				}
				continue
			}
			clientSC.WriteMessage(websocket.BinaryMessage, forwarded)

		case websocket.TextMessage:
			var bMsg map[string]any
			if err := json.Unmarshal(data, &bMsg); err != nil {
				continue
			}
			switch bMsg["type"] {
			case "session-end":
				// Hub-B tore down the agent session.
				return
			case "udp-offer":
				// Bridge leg UDP offer; ignored in v1.
				continue
			default:
				// Forward all other signaling to client (e.g. wg-pubkey, peer-endpoint).
				clientSC.WriteMessage(websocket.TextMessage, data)
			}
		}
	}
}

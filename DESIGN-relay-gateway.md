# Relay Gateway Design

This document specifies the relay gateway: hub-to-hub transit bridging
for Tela. It is the contract that the Phase 4 implementation follows.
Read ACCESS-MODEL.md for the token/permission model and DESIGN.md
section 6.8 for the single-hop relay and transport upgrade cascade
that this design generalizes.

## 1. Overview

Tela's gateway primitive recurs at every layer of the stack:

| Layer | Name | What it does |
|-------|------|-------------|
| L7 | Path gateway | telad reverse-proxies HTTP requests by URL path prefix to local services |
| L4 inbound | Bridge gateway | telad forwards TCP connections from the tunnel to internal hosts |
| L4 outbound | Upstream gateway | telad opens outbound TCP connections to external services on behalf of the tunnel |
| L3 single-hop | Relay gateway (today) | Hub relays opaque WireGuard ciphertext between client and agent |
| L3 multi-hop | Relay gateway (this design) | Hub relays ciphertext to an agent registered on a different hub |

The unifying rule: a node in the middle forwards without inspecting
beyond what its layer requires. The hub is blind to the WireGuard
payload today. A bridging hub remains blind: it forwards ciphertext
from one leg to the other, never decrypting, never interpreting.

### 1.1 Why this is a 1.0 deliverable

The v1 wire format has not shipped yet. Adding a hop-count field to
the relay frame header is free right now and expensive later (requires
a v2 wire bump and a backward-compatibility burden). The relay gateway
also unlocks the fleet-tier scenarios the project implicitly promises:
managed service providers, geographic distribution, air-gap traversal,
acquisition mergers. Without hub-to-hub transit, "how does Tela scale
beyond one hub" has no answer.

## 2. Wire Format: v1 Relay Frame Header

### 2.1 Replacing the speculative header in DESIGN.md section 6.3

DESIGN.md section 6.3 defined a 12-byte frame header (version,
session_id, channel_id, type, payload_length) that was never
implemented. The actual relay path forwards raw binary WebSocket
messages and token-prefixed UDP datagrams with no Tela-specific
framing. This design replaces section 6.3 with the header defined
below. The old header is retired; it was aspirational and no code
depends on it.

### 2.2 Header layout

Every relayed datagram (WebSocket binary frame or UDP relay packet)
carries a 7-byte prefix immediately before the WireGuard ciphertext:

```
 0       1       2       3       4       5       6
+-------+-------+-------+-------+-------+-------+-------+
| magic |  hop  | flags |      session_id (4 bytes)      |
+-------+-------+-------+-------+-------+-------+-------+
| WireGuard ciphertext ...                               |
+--------------------------------------------------------+
```

All multi-byte fields are big-endian (network byte order). The header
is fixed-size, fixed-layout, with no TLV extensions and no optional
fields. Every binary that speaks v1 produces and consumes exactly
these 7 bytes. If v2 needs more fields, it gets a new fixed layout
selected by a different magic byte.

### 2.3 Field definitions

| Field | Size | Semantics |
|-------|------|-----------|
| `magic` | 1 byte | `0x54` (ASCII "T"). Identifies this as a Tela relay frame. WireGuard message types are 1-4; STUN's first two bits are `00`; TPUNCH starts with `0x54` but only appears at byte 0 of a UDP packet, whereas the frame header follows the 8-byte UDP relay token. No collision on any transport. A receiver that sees a first byte other than `0x54` on a WebSocket binary message rejects the connection with a protocol error. |
| `hop` | 1 byte | Hop count (TTL). The first hub in the chain sets this (default 8, configurable per bridge via `maxHops` in `telahubd.yaml`). Clients do not set the hop count; the hub overwrites it on first relay. Each subsequent forwarding hub decrements by 1 before relaying. A hub that receives a datagram with hop=0 drops it and logs a warning. The agent does not decrement. |
| `flags` | 1 byte | Bit 0: frame type. `0` = DATA (WireGuard ciphertext follows), `1` = CONTROL (in-band control payload follows; see section 3.7). Bits 1-7 are reserved and must be 0 in v1. A receiver must ignore reserved bits it does not understand (forward-compatible). |
| `session_id` | 4 bytes | Session identity within this connection. In v1, each WebSocket connection carries exactly one session, so this field is constant for the life of the connection. The field is reserved for future connection multiplexing: multiple sessions sharing a single WebSocket, demultiplexed by session_id. The initiating side of a connection assigns session IDs (same convention as HTTP/2 stream IDs). |

### 2.4 Scope

The frame header is **end-to-end**: every participant on the relay
path (tela client, telahubd, telad agent) prepends the header on send
and parses it on receive. There is no unframed mode. Single-hop relay
is the degenerate case where the hop count starts at 8 and the hub
decrements to 7 before forwarding to the agent. The agent sees hop=7,
extracts the WireGuard payload, and feeds it to its WireGuard stack.

### 2.5 Transport integration

**WebSocket path.** Binary messages on the paired relay WebSocket are
`[7-byte header][WireGuard datagram]`. The hub reads the header,
decrements hop if forwarding, writes the updated header + payload to
the peer's WebSocket.

**UDP relay path.** UDP packets on the relay port are
`[8-byte token][7-byte header][WireGuard datagram]`. The hub strips
the token, reads the header, decrements hop if forwarding, prepends
the peer's token + updated header, and sends to the peer's UDP
address. The token prefix is outside the frame header because it is a
hub-local routing artifact, not an end-to-end property.

**Direct tunnel path.** Once both peers have established a direct UDP
path via STUN hole-punching, they exchange raw
`[7-byte header][WireGuard datagram]` packets with no token prefix.
The header is still present (hop count is meaningful for logging and
diagnostics even on a direct path) but hops are not decremented since
no hub is in the middle.

### 2.6 Version detection and rejection

A hub that receives a WebSocket binary message whose first byte is not
`0x54` closes the WebSocket with status 1002 (protocol error) and
logs `"relay frame: unsupported magic byte 0xNN"`. This produces a
loud, actionable failure when a pre-v1 binary (which sends raw
WireGuard bytes starting with 0x01-0x04) connects to a v1 hub.

Post-1.0, this check is the permanent v1 discriminator. There is no
fallback to unframed relay. Older clients must upgrade.

## 3. Bridge Session Lifecycle

### 3.1 Topology

```
Client ---- Hub-A ---- Hub-B ---- Agent
        leg 1     bridge    leg 2
```

Hub-A is the **bridging hub** (the hub the client connects to).
Hub-B is the **destination hub** (the hub where the agent is
registered). The client and agent are unaware of each other's hub
identity. Each sees a normal single-hub session from its perspective.

### 3.2 Session-aware forwarding (Shape B)

The bridging hub maintains full session state on both legs
independently. It does not transparently pipe bytes between the client
and the destination hub. Instead:

- **Leg 1 (client to Hub-A):** Hub-A manages the client's session the
  same way it manages any client session today. It holds the
  `clientSession` entry, manages UDP relay tokens for the client side,
  and participates in transport upgrade negotiation with the client.

- **Bridge (Hub-A to Hub-B):** Hub-A opens an outbound WebSocket to
  Hub-B and authenticates with a stored connect token. From Hub-B's
  perspective, Hub-A looks like a normal client. Hub-A sends a
  `connect` message for the target machine. Hub-B runs its normal
  session setup (session-request to agent, session-join, key exchange).

- **Leg 2 (Hub-B to agent):** Hub-B manages the agent's per-session
  WebSocket the same way it does today. UDP relay tokens, transport
  upgrade, everything normal.

- **Pairing on Hub-A:** Hub-A pairs the two legs internally. Inbound
  data from leg 1 (client) is forwarded to the bridge WebSocket after
  decrementing the hop count. Inbound data from the bridge (Hub-B
  side) is forwarded to the client's WebSocket after decrementing the
  hop count.

### 3.3 Why Shape B over Shape A (transparent bridging)

Shape A (transparent pipe) would prevent the client from negotiating
transport upgrades with Hub-A, because the client would think it is
talking directly to Hub-B (which it cannot reach by UDP or STUN). The
entire transport upgrade cascade (WebSocket, UDP relay, direct tunnel)
would be permanently unavailable on bridged sessions.

Shape B preserves independent transport negotiation on each leg. The
client can upgrade to UDP relay or direct tunnel with Hub-A. Hub-A
can independently upgrade its connection to Hub-B. The agent can
upgrade with Hub-B. Three legs, three independent transport decisions.

### 3.4 WireGuard key exchange across the bridge

The WireGuard key exchange is end-to-end between client and agent.
Hub-A and Hub-B never see private keys and cannot decrypt the tunnel
payload. The signaling flow:

1. Client sends `connect` to Hub-A with its WG public key.
2. Hub-A determines the machine is reachable through Hub-B (see
   section 4). Hub-A opens an outbound connection to Hub-B if one is
   not already open.
3. Hub-A sends `connect` to Hub-B for the target machine, forwarding
   the client's WG public key.
4. Hub-B runs normal session setup: sends `session-request` to the
   agent, agent opens a per-session WebSocket and sends
   `session-join`.
5. Hub-B sends `session-start` to the agent with the client's WG
   public key.
6. Agent generates an ephemeral WG keypair and sends `wg-pubkey` back
   to Hub-B.
7. Hub-B relays `wg-pubkey` to Hub-A (on the bridge WebSocket).
8. Hub-A relays `wg-pubkey` to the client.
9. WireGuard Noise_IK handshake proceeds as framed binary messages
   through the relay path. Neither hub can decrypt the handshake or
   the subsequent data.

### 3.5 Session teardown

Either side can initiate teardown:

- Client disconnects from Hub-A: Hub-A sends `session-end` to Hub-B
  on the bridge connection. Hub-B tears down the agent session.
- Agent disconnects from Hub-B: Hub-B sends `session-end` to Hub-A
  on the bridge connection. Hub-A tears down the client session.
- Hub-A or Hub-B restarts: the broken WebSocket triggers teardown on
  the surviving hub. The client or agent reconnects and re-establishes
  the session via normal signaling.

### 3.6 Hop count semantics on a bridged session

```
Client --> Hub-A (sets hop=4) --> Hub-B (hop=3) --> Agent (hop=3)
```

Hub-A sets hop from its bridge config (`maxHops`, default 8). Hub-B
decrements to 3 when forwarding to the agent. The agent does not
decrement. In a chained bridge (Hub-A to Hub-B to Hub-C), each hub
decrements once. A session that passes through N hubs reaches the
agent at hop=(maxHops - N); if it were forwarded again and hop
reached 0, the next hub would drop it.

The `maxHops` field in the bridge config lets the operator match the
hop limit to the known topology. A two-hub fabric can set `maxHops: 2`
so any misconfigured loop dies after two hops instead of eight.

### 3.7 In-band CONTROL frames

CONTROL frames (flags bit 0 = 1) carry session-level signaling on
the relay data path. They travel end-to-end through the same frame
header as DATA frames and are subject to the same hop-count
decrement.

v1 defines one CONTROL frame type:

**Session keepalive.** The client (or any hub in the chain) sends a
CONTROL frame with a 1-byte payload: `0x01` (keepalive request) or
`0x02` (keepalive response). The far end echoes a response. If no
keepalive response arrives within the session keepalive timeout
(default 45 seconds), the session is considered dead and torn down
on the side that detected the timeout.

Session keepalive is distinct from WebSocket ping/pong:

- WebSocket ping/pong is **per-connection, hop-by-hop**. It tells you
  the TCP link to the next hub is alive. It says nothing about the
  end-to-end session health.
- Session keepalive is **per-session, end-to-end**. It travels the
  full relay path (client to agent, through all hubs). If any link
  in the chain is broken, the keepalive stops arriving and the
  session times out.

On bridged sessions, this distinction matters: a WebSocket ping/pong
between Hub-A and Hub-B succeeds even if Hub-B has lost contact with
the agent. The in-band keepalive detects the actual session failure.

Future CONTROL frame types (flow control, graceful session drain,
etc.) will be assigned from the remaining payload-byte space. v1
implementations must ignore CONTROL frames with unknown payload
types.

### 3.8 Handshake and retransmission across bridged hops

The WireGuard Noise_IK handshake is end-to-end between the client
and the agent. Retransmission of handshake messages is WireGuard's
responsibility, not the relay's; the relay path is a stateless
byte-forwarder. Each hop validates the 7-byte frame header,
decrements the TTL, and forwards the payload unchanged. A dropped
handshake datagram on any hop is indistinguishable from a dropped
datagram on a direct path: WireGuard's handshake retry timer on the
originator fires, a fresh handshake initiation is sent, and the
relay path carries it.

This is the same property the single-hop relay has today. The
multi-hop relay inherits it without additional state. The
consequence: a bridging hub can restart mid-session without data
loss for any session whose WireGuard handshake has already
completed (the tunnel's internal keepalive will re-establish the
path through whichever transport is currently available) and with
at most one retry for sessions whose handshake is in flight at
restart time.

## 4. Directory: "Reachable Through" Field

### 4.1 Hub status extension

The hub's `/api/status` response gains an optional field per machine:

```json
{
  "machines": [
    {
      "id": "prod-db",
      "agentConnected": true,
      "reachableThrough": {
        "hubId": "a66db6c0-...",
        "hubName": "prod-asia",
        "hubUrl": "https://prod-asia.example.com"
      }
    }
  ]
}
```

When `reachableThrough` is present, the machine is not directly
registered on this hub. Clients must connect through the named hub
(or through a bridging hub that has an outbound connection to it).

When `reachableThrough` is absent (the common case), the machine is
registered directly on this hub. No bridging is needed.

### 4.2 Portal fleet aggregation

The portal's `/api/fleet/agents` response already carries hub
identity per agent. The `reachableThrough` field, if present on the
upstream hub's status, is passed through verbatim. Clients and TV use
it to decide whether a session will be bridged.

### 4.3 How a hub learns about remote machines

A hub can learn about machines on other hubs in two ways:

1. **Static configuration.** The operator adds a `bridges` section to
   `telahubd.yaml` listing the destination hubs and the machines
   reachable through them. This is the v1 mechanism.

2. **Portal-mediated discovery (future).** A hub queries a portal's
   fleet API to discover machines across all hubs the portal knows
   about. This is a post-v1 enhancement; the directory schema
   supports it but the discovery logic is not part of this design.

### 4.4 Static bridge configuration

```yaml
bridges:
  - hubId: a66db6c0-...
    url: wss://prod-asia.example.com
    token: <connect-token-on-destination-hub>
    maxHops: 4          # default 8 if omitted
    machines:
      - prod-db
      - prod-web
```

The bridging hub advertises these machines in its own `/api/status`
with the `reachableThrough` field pointing back at the destination
hub. Clients connecting to a bridged machine trigger the bridge
session lifecycle described in section 3.

## 5. Authorization

### 5.1 No new permission category

The bridging hub holds a regular token on the destination hub. The
token has `connect` scope (and optionally `manage` scope) for the
relevant machines. The destination hub authorizes the bridging hub
the same way it authorizes any client. Owner and admin tokens bypass
machine-level ACL checks as documented in ACCESS-MODEL.md.

### 5.2 Example

Hub-A bridges sessions to Hub-B for machines `prod-db` and
`prod-web`. Hub-B's config:

```yaml
auth:
  tokens:
    - id: hub-a-bridge
      token: <bridge-token>
  machines:
    prod-db:
      connectTokens:
        - <bridge-token>
    prod-web:
      connectTokens:
        - <bridge-token>
```

Hub-A's config:

```yaml
bridges:
  - hubId: <hub-b-id>
    url: wss://hub-b.example.com
    token: <bridge-token>
    machines:
      - prod-db
      - prod-web
```

### 5.3 Client authorization on the bridging hub

The client must also have `connect` permission for the bridged
machine on Hub-A. Both hubs independently authorize: Hub-A checks the
client's token, Hub-B checks Hub-A's bridge token. A client that can
connect to `prod-db` on Hub-A does not automatically gain access to
`prod-db` on Hub-B (the bridge token controls that, and the operator
grants it separately).

### 5.4 TLS trust on the outbound bridge dial

Hub-A dials the destination hub over `wss://` and inherits the
system CA trust store for certificate validation. This matches the
client dial model today. When cert pinning lands (see
[ROADMAP-1.0.md](ROADMAP-1.0.md), issue #23), the bridge dial should
use the same pinning mechanism clients use: the destination hub's
certificate fingerprint stored alongside the bridge config, TOFU on
first dial, explicit operator confirmation on change. Designed so
both dial paths share one implementation; the bridge is just another
caller of the pinning-aware dialer. v1 ships with system-CA trust
only; cert pinning will be added to bridge dials in the same commit
series that adds it to client dials.

## 6. Transport Upgrade on Bridged Sessions

### 6.1 Independent legs

Each leg of a bridged session negotiates transport independently:

| Leg | Transport options | Negotiated with |
|-----|------------------|----------------|
| Client to Hub-A | WS, UDP relay, direct tunnel | Hub-A |
| Hub-A to Hub-B | WS, UDP relay | Hub-B |
| Hub-B to Agent | WS, UDP relay, direct tunnel | Hub-B |

Hub-A to Hub-B does not attempt direct tunnel (STUN hole-punching)
because both are server-class hosts with stable addresses; the UDP
relay upgrade is sufficient and simpler.

### 6.2 UDP relay on the bridge leg (post-v1)

v1 keeps the bridge leg on WebSocket unconditionally. Hub-B may send
a `udp-offer` as part of its normal signaling, but Hub-A ignores it
on the bridge leg. A future release can upgrade the bridge leg to
UDP relay using the same mechanism a client and hub use: Hub-B sends
`udp-offer` with a token and port, Hub-A probes, and on success both
sides switch WireGuard datagrams to UDP. The frame header is
identical across transports (section 2.5), so no wire-format change
is required to add this later.

### 6.3 Latency profile

A single-hop session: client to hub to agent. Latency = client-hub +
hub-agent.

A bridged session: client to Hub-A to Hub-B to agent. Latency =
client-Hub-A + Hub-A-Hub-B + Hub-B-agent. The extra hop adds one
network round trip. If Hub-A and Hub-B are in the same datacenter or
region, the added latency is negligible. If they are geographically
distant, the client should prefer the hub closest to the agent (the
directory protocol gives the client enough information to choose).

## 7. Audit Logging

### 7.1 Bridging hub (Hub-A) logs

- Session forwarded: client identity, destination hub identity,
  target machine, timestamp.
- Session ended: duration, bytes relayed per leg, teardown reason.
- Bridge connection lifecycle: outbound connect to Hub-B, auth
  success/failure, disconnect/reconnect events.

### 7.2 Destination hub (Hub-B) logs

Hub-B logs the session under the bridging hub's token identity, the
same way it logs any client session. No special treatment. The audit
entries are queryable through the existing `/api/history` endpoint.

### 7.3 No cross-hub join

Each hub logs independently. There is no cross-hub correlation ID in
v1. An operator investigating a bridged session reads Hub-A's log to
find the destination hub and timestamp, then reads Hub-B's log for
the corresponding session. A future enhancement could add a
correlation ID to the signaling messages, but v1 does not require it.

## 8. Implementation Notes

### 8.1 Outbound connection management

Each bridged session opens a new outbound WebSocket to Hub-B (one
connection per session, matching the existing one-WS-per-session
model). The connection is established lazily on first use with a
15-second dial handshake timeout. If the dial fails, Hub-A sends
the client an `error` message naming the bridge as unavailable and
the session terminates; there is no retry at the bridge layer. The
client is free to reconnect, which opens a fresh bridge dial.

Future optimization: multiplex multiple sessions over a single
persistent bridge connection, demultiplexed by `session_id` in the
frame header. This would let a hub pair amortize one TCP+TLS
handshake across many sessions and reconnect with exponential
backoff independently of any one session's lifetime. The wire
format already reserves the bits for this; see 8.2.

### 8.2 Session ID in the frame header

In v1, `session_id` in the 7-byte frame header is constant for the
life of a WebSocket (one session per connection). Hubs and peers
do not use it for routing; it exists so that the future
multiplexing optimization (see 8.1) can be added without a
wire-format change.

### 8.3 Existing code changes

The relay frame header must be added to:

- `internal/hub/hub.go`: the session pairing and relay paths
  (`pairSession`, `relayBinaryMessage`, UDP relay read/write loops).
- `internal/agent/agent.go`: the per-session WebSocket read/write
  loops and the UDP relay adapter.
- `internal/client/client.go`: the client-side WebSocket and UDP
  relay read/write paths.

The bridge session lifecycle is new code in `internal/hub/`:

- Outbound connection manager (connect to destination hubs, reconnect,
  heartbeat).
- Bridge session state (pair two half-sessions, forward signaling and
  data).
- Static bridge config loading from `telahubd.yaml`.
- `/api/status` extension for `reachableThrough`.

### 8.4 Test strategy

The existing `internal/teststack` package runs hub, agent, and client
in-process. Extend it with a second hub instance and a bridge config
to test the full bridged session lifecycle including key exchange,
data relay, and session teardown. The in-process harness cannot test
UDP relay or direct tunnel (known limitation documented in
`stack_test.go`), so bridged UDP relay testing requires a separate
integration test with real network sockets.

## 9. What This Design Explicitly Excludes

- **Routing protocols.** No BGP, no link-state, no spanning tree. The
  bridging hub discovers destinations from static config (v1) or
  portal directory (future). Simplicity over automation.

- **Identity federation.** A token on Hub-A does not grant access on
  Hub-B. The bridge token is a separate credential. See section 5.3.

- **Mesh agent-to-agent routing.** The data path is client to hub (to
  hub) to agent. Agents do not route for other agents. This is a
  deliberate architectural constraint, not an oversight.

- **Connection multiplexing (v1).** One WebSocket per session on the
  bridge leg. The `session_id` field in the frame header reserves
  space for future multiplexing without a wire format change.

- **Cross-hub correlation IDs.** Each hub logs independently. See
  section 7.3.

## 10. Resolved Design Decisions

These were open questions during the design review. Decisions are
recorded here for traceability.

1. **Who sets the initial hop count?** The first hub in the chain
   sets it from its bridge config (`maxHops`, default 8). The hub
   has a better view of the fabric topology than the client.
   Configurable per bridge entry in `telahubd.yaml`.

2. **Lazy or eager bridge connections?** Lazy. Connect on first use.
   Avoids wasting resources when bridges are rarely used. A failed
   bridge connection surfaces as a session error to the client, which
   is the right place for the operator to notice it.

3. **In-band CONTROL frames for session keepalive?** Yes. WebSocket
   ping/pong is per-connection and hop-by-hop; it cannot detect
   end-to-end session failures on bridged paths. CONTROL frames
   carry session-level keepalive end-to-end. See section 3.7.

4. **Self-bridge detection at config load.** A `bridges:` entry
   that points at the hub's own `hubId` is a misconfiguration: a
   session routed through it loops back into the same hub and
   drains TTL before any useful forwarding. `initBridgeDir` in
   `internal/hub/bridge.go` compares each bridge's `hubId` against
   the hub's own at startup; a match logs a WARNING and the entry
   is skipped. URL-based matching is not attempted because the
   hub's external URL is not always knowable at startup (reverse
   proxies, multi-address binds); `hubId` equality is the reliable
   signal and catches the common copy-paste misconfiguration.

5. **WebSocket ping/pong on the bridge leg.** The outbound bridge
   WebSocket installs a `SetPongHandler` that resets a 30-second
   read deadline, and a pinger goroutine sends a ping every 10
   seconds (matching the cadence of `runKeepalive` on the inbound
   control WebSocket). A middlebox that silently drops the TCP
   connection without tearing it down now surfaces as a read error
   within the pong timeout rather than only via the 45-second
   in-band session keepalive. The in-band keepalive (3.7) remains
   the authoritative end-to-end health signal; the WS-level
   ping/pong is the per-hop dead-connection detector.

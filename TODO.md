# Tela — Roadmap

## Phase 1: Production Basics ✅
- [x] Client auto-reconnect (tela loops like telad on disconnect)
- [x] Connection token auth (`-token` flag on both sides, hub validates)
- [x] Hub `/status` JSON endpoint (registered machines, active sessions)

## Phase 2: UDP Relay ✅
- [x] Hub UDP listener alongside WebSocket (port 41820, configurable via `HUB_UDP_PORT`)
- [x] Post-signaling upgrade: both sides independently switch WG datagrams to UDP
- [x] Automatic fallback to WebSocket if UDP blocked (probe timeout → stays on WS)
- [x] Asymmetric mode: hub bridges UDP↔WebSocket when only one side upgrades

## Phase 3: Direct Tunnel ← recommended next
- [ ] STUN-based public endpoint discovery
- [ ] Hub exchanges endpoints between peers
- [ ] Simultaneous UDP hole punching
- [ ] WireGuard endpoint roaming to direct path
- [ ] Fallback cascade: direct UDP → UDP relay → WebSocket

**Why this is next:** Phases 1 and 2 made the relay path fast and resilient.
Phase 3 removes the relay from the data path entirely for peers that can
reach each other directly — the biggest remaining latency and bandwidth win.
The UDP relay infrastructure from Phase 2 provides the signaling and fallback
scaffolding that hole punching builds on.

## Later
- [ ] Mesh networking (multi-peer)
- [ ] DNS integration (machine-name resolution)
- [ ] ACL policies
- [ ] Mobile clients (iOS/Android)
- [ ] GUI (system tray / menu bar)
- [ ] OIDC/SSO authentication (layer on top of token auth)
- [ ] Audit logging
- [ ] Multiple simultaneous sessions per machine

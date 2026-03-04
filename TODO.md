# Tela — Roadmap

## Phase 1: Production Basics
- [x] Client auto-reconnect (tela loops like telad on disconnect)
- [x] Connection token auth (`-token` flag on both sides, hub validates)
- [x] Hub `/status` JSON endpoint (registered machines, active sessions)

## Phase 2: UDP Relay
- [ ] Hub UDP listener alongside WebSocket
- [ ] Post-signaling upgrade: both sides switch WG datagrams to UDP
- [ ] Automatic fallback to WebSocket if UDP blocked (corporate firewalls, captive portals)

## Phase 3: Direct Tunnel
- [ ] STUN-based public endpoint discovery
- [ ] Hub exchanges endpoints between peers
- [ ] Simultaneous UDP hole punching
- [ ] WireGuard endpoint roaming to direct path
- [ ] Fallback cascade: direct UDP → UDP relay → WebSocket

## Later
- [ ] Mesh networking (multi-peer)
- [ ] DNS integration (machine-name resolution)
- [ ] ACL policies
- [ ] Mobile clients (iOS/Android)
- [ ] GUI (system tray / menu bar)
- [ ] OIDC/SSO authentication (layer on top of token auth)
- [ ] Audit logging

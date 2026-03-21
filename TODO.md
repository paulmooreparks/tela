# Tela - Roadmap

## Phase 1: Production Basics ✅
- [x] Client auto-reconnect (tela loops like telad on disconnect)
- [x] Connection token auth (`-token` flag on both sides, hub validates)
- [x] Hub `/status` JSON endpoint (registered machines, active sessions)

## Phase 2: UDP Relay ✅
- [x] Hub UDP listener alongside WebSocket (port 41820, configurable via `TELAHUBD_UDP_PORT`)
- [x] Post-signaling upgrade: both sides independently switch WG datagrams to UDP
- [x] Automatic fallback to WebSocket if UDP blocked (probe timeout → stays on WS)
- [x] Asymmetric mode: hub bridges UDP↔WebSocket when only one side upgrades

## Phase 3: Direct Tunnel ✅
- [x] STUN-based public endpoint discovery (RFC 5389 Binding Request via `stun.l.google.com:19302`)
- [x] Hub exchanges endpoints between peers (relayed as `peer-endpoint` JSON over paired WS)
- [x] Simultaneous UDP hole punching (`TPUNCH` probes, 100ms interval, 5s timeout)
- [x] WireGuard endpoint roaming to direct path (wsBind routes raw datagrams to peer)
- [x] Fallback cascade: direct UDP → UDP relay → WebSocket

**Note:** Hole punching requires both peers to be on separate NATs (standard
scenario). Same-NAT tests fail because most routers don't support hairpin NAT.
When peers are on the same LAN, the relay path is already low-latency.

## Awan Saya
- [x] "How it Works" section on the Awan Saya landing page is missing a step about creating/deploying a hub. A user must have a hub before they can begin step 1 (installing the agent)

## Later
- [ ] Mesh networking (multi-peer)
- [x] ACL policies (per-machine token-based RBAC; advanced attribute-based policies are future work)
- [ ] Mobile clients (iOS/Android)
- [x] GUI (system tray / menu bar) -- TelaVisor with system tray support
- [ ] OIDC/SSO authentication (layer on top of token auth)
- [ ] Audit logging
- [x] Multiple simultaneous sessions per machine
- [ ] Direct tunnel liveness detection (fall back to relay if direct path goes stale)
- [ ] OAuth2 Device Authorization Grant for portal login (RFC 8628)

## File Sharing
- [x] Native file sharing protocol (list, read, write, delete, mkdir, rename, move)
- [x] Live file change notifications (fsnotify + subscribe)
- [x] tela files CLI (ls, get, put, rm, mkdir, rename, mv, info)
- [x] TelaVisor Files tab with Explorer-style browser
- [x] Drag-and-drop move support

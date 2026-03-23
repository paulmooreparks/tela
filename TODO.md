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

## TelaVisor Enhancements

### High value, low effort
- [ ] Remote management UI (tela remote add/remove/list) in Settings
- [ ] Credential viewer in Settings (show stored hub tokens, remove individual entries)
- [ ] Pairing code generation in Hubs mode (type, expiration, machine scope)
- [ ] Service install/uninstall UI (install current profile as OS service, show status, start/stop)

### Medium value, medium effort
- [ ] Gateway-aware service display (show "Open in browser" link for gateway services)
- [ ] Hub console embed (view hub console within Hubs mode without opening a browser)
- [ ] Per-profile MTU override (instead of global-only setting)

### Lower priority
- [ ] Hub explorer (browse any hub's machines and services without adding to profile)
- [ ] Connection history (show recent sessions from /api/history in Status or Hubs view)
- [ ] Profile export as CLI command (copy equivalent tela connect command to clipboard)

## Agents Mode (TelaVisor)
- [x] Agent list sidebar with status and version
- [x] Agent detail cards (info, services, file share, update comparison)
- [x] Pair Agent button (generate register pairing codes)
- [ ] Live event channel from telad (log feed, status updates)
- [ ] Remote log viewing (view/download telad logs through the tunnel)
- [ ] Remote config pull/push (edit telad.yaml through the file share channel)
- [ ] Remote agent restart (management command through the tunnel)
- [ ] Remote agent stop (management command through the tunnel)
- [ ] Remote agent update with watchdog rollback
- [ ] Unregister agent from hub

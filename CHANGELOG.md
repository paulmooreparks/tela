# Changelog

All notable changes to Tela are documented in this file. The format
is based on [Keep a Changelog](https://keepachangelog.com/), and
this project adheres to [Semantic Versioning](https://semver.org/)
starting from 1.0.

Pre-1.0 releases use `MAJOR.MINOR.PATCH-channel.N` tags. The
changelog is organized by minor version (0.5, 0.6, 0.7) since
patch-level dev builds are too granular to list individually.

## [Unreleased]

### Added
- User-level autostart for both tela and telad:
  `tela service install --user` and `telad service install --user`
  register autostart tasks that run at login without admin/root
  privileges. Windows uses Scheduled Tasks, Linux uses systemd
  --user units, macOS uses LaunchAgents. TelaVisor shows both
  system service and user autostart options in Client Settings.

## [0.7] - 2026-04-10 (in development)

The "hardening" release. Focus on polish, onboarding reliability,
and preparation for a public stable release.

### Added
- File Share Mount card in Profiles tab with live preview showing
  which machines will appear as folders under the mount point
- Mount directory name sanitization using `filepath.Localize` for
  platform-safe WebDAV directory names
- Portal `GET /api/hub-token/{hubName}` endpoint for credential
  synchronization between portal and local credential store
- Automatic credential sync: TelaVisor writes hub tokens to the
  local credential store before launching `tela connect`
- File-share-only connections: machines with file sharing enabled
  are automatically included in profile connections when the mount
  is enabled, even without selected TCP services

### Fixed
- `telahubd update` panic when run without `-config` flag (nil
  pointer dereference in `hubChannel()`)
- Credential store sync mapped WSS URLs to portal hub names
  incorrectly, causing 401 errors on fresh installs
- Client Settings "Installed Tools" did not refresh after saving
  a new binary path (async save race)

## [0.6] - 2026-04-09

The "relay gateway" release. Hub-to-hub transit bridging, portal
onboarding, and fleet management.

### Added
- **Relay gateway**: hub-to-hub transit bridging for WireGuard
  tunnels. A bridging hub forwards opaque ciphertext between a
  client connected to Hub-A and an agent registered on Hub-B.
  End-to-end encryption is preserved; bridging hubs cannot inspect
  tunnel payloads.
- v1 relay frame header (7-byte prefix on all relay paths): magic
  byte, hop TTL, flags, session ID
- In-band session keepalive (CONTROL frames) for end-to-end session
  health detection, distinct from WebSocket ping/pong
- Static bridge configuration in `telahubd.yaml` with per-bridge
  `maxHops` and machine lists
- `reachableThrough` field in `/api/status` for bridged machines
- Bridge session lifecycle tests in `internal/teststack`
- Portal public-hub proxy endpoints (`/api/hub-status/`,
  `/api/hub-history/`) so TelaVisor can read hub status through the
  portal without direct hub credentials
- Hub `/api/admin/status` and `/api/admin/history` aliases for admin
  proxy access when viewer tokens are unavailable
- TV polish: Enter key activates default button on all dialogs,
  remote portal rename, connect tooltip follows the connect button
- Credentials page explains portal-managed credentials when a remote
  source is active
- Portal identity model (DESIGN-identity.md): stable UUIDs for hubs,
  agents, machines, portals, and profiles
- Portal protocol 1.1: identity fields on all directory and fleet
  endpoints
- `internal/portal` package: embedded portal server with file-backed
  store, admin proxy, fleet aggregation, conformance tests
- `internal/portalclient` package: typed Go client for the portal
  protocol including OAuth 2.0 device authorization grant
- `internal/portalaggregate` package: merge hub and agent views
  across multiple portal sources
- TelaVisor portal sources: sign into remote portals (Awan Saya)
  via OAuth device code flow, manage multiple sources
- TelaVisor Infrastructure mode rewired onto the portal client:
  hub management, agent fleet view, access control, tokens, history
- Profile UUIDs, `hubId` on connections, profile migration command
- Per-machine `machineRegistrationId` for stable agent identity
- Tela Design Language (TDL) for consistent visual identity

### Fixed
- Hub identity deduplication and URL rendering
- Machine name case preservation in Status tab headers
- Hub Settings "Online" indicator treating error responses as truthy
- Data race on `udpPort` (changed to `atomic.Int32`)
- Awan Saya fleet API using wrong JSON field names (`online` instead
  of `agentConnected`, `version` instead of `agentVersion`)
- Awan Saya hub-status and fleet proxies only using viewer token,
  failing when only admin token was stored (now falls back to admin)

### Changed
- Build workflow gated on CI success (no more publishing binaries
  from code that fails the race detector)

## [0.5] - 2026-04-08

### Added
- Release channel system: dev, beta, and stable channels with
  manifest-based self-update
- Promotion workflow for dev-to-beta and beta-to-stable
- First unit tests: `internal/channel`, `internal/credstore`,
  `permuteArgs`

### Changed
- Renamed workflows: Release to Build, Promote release to Promote

## [0.4] - 2026-04-07

### Added
- CI workflow: build, vet, test, gofmt, go mod tidy on every push
- ROADMAP-1.0.md: living checklist for 1.0 readiness
- `gofmt` enforced across the tree

## [0.3] and earlier

Initial development. WireGuard userspace tunnels via gVisor netstack,
three-binary architecture (tela, telad, telahubd), UDP relay with
WebSocket fallback, direct P2P via STUN hole-punching, token-based
RBAC, file sharing with WebDAV mount, TelaVisor desktop GUI, Awan
Saya portal integration.

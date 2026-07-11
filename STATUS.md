# Tela - Implementation Status

Traceability matrix mapping each DESIGN.md section to current implementation status.

**Legend:**
- ✅ **Done**: Implemented (at least POC-level working code)
- 🔶 **Partial**: Some aspects implemented, gaps remain
- ⬜ **Not started**: No implementation yet
- 🔮 **Future**: Awan Saya scope or Phase 3+
- 📄 **Doc-only**: Informational section, no implementation required

Last updated: 2026-07-11

---

## §0-§3 - Identity, Goals, Philosophy

These sections are design guidance; "implementation" means the codebase reflects the principles.

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §0 | Purpose of This Document | 📄 | - |
| §0.1 | Glossary | 📄 | - |
| §1.1 | Name | 📄 | - |
| §1.2 | Positioning | 📄 | - |
| §1.3 | What Tela Is | 📄 | - |
| §1.4 | License | ✅ | Apache 2.0 in repo |
| §2.1 | Goals | 🔶 | Outbound-only tunneling works; multiplexing, protocol-agnostic channels, multi-platform agents not yet |
| §2.2 | Non-Goals | 📄 | - |
| §3.1-3.4 | Design Philosophy & Invariants | 📄 | Guiding principles; no code artifact |

---

## §4 - Architecture Overview

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §4.1 | Components | 🔶 | `telad` (Go agent ✅), `telahubd` (Go hub ✅), `tela` (Go client ✅), Web (landing page + downloads ✅), CLI (✅), Awan Saya (🔮) |
| §4.2 | Data Flow: Agent Connection | ✅ | `telad`: outbound WS, register, reconnect, token auth |
| §4.2 | Data Flow: Browser Orchestration | 🔶 | Landing page with OS-detected download links; no login, session tokens, or machine list |
| §4.2 | Data Flow: Client Connection | ✅ | `tela`: WS connect, WireGuard tunnel, auto-bind local listeners, reconnect |
| §4.2 | Data Flow: Multiplexed Channels | 🔮 | One WS per session by design. DESIGN §6.2 fixes single-channel as the v1 shape; multiplexing is reserved for a hypothetical v2 (the relay frame reserves a `session_id` field for it) |
| §4.3 | Component Interaction Model | 🔶 | Client→Hub and Agent→Hub paths work; Browser→Hub auth/session path not implemented |

---

## §5 - MeshCentral Integration Boundary

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §5.1 | Components Reused | ⬜ | POC is written from scratch; no MeshCentral code integrated |
| §5.2 | Components Replaced | 📄 | Design guidance for when integration happens |
| §5.3 | Rationale | 📄 | - |

---

## §6 - Protocol Specification

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §6.1 | Transport (TLS 1.3, WSS, binary) | 🔶 | WSS via Cloudflare tunnel; internal plain WS; binary frames for WG datagrams, JSON for control |
| §6.2 | Multiplexing (v1: single-channel) | 🔮 | Single-channel shipped as the v1 design (one WS = one session); DESIGN §6.2 reserves multiplexing for a hypothetical v2. A mux experiment landed and was reverted; the `session_id` field is held open for it |
| §6.3 | Frame Format (7-byte relay header) | ✅ | 7-byte big-endian header (magic + hop + flags + session_id) on every relayed binary message, `internal/relay/frame.go`; produced and consumed by all binaries. The earlier 12-byte draft was never built and is retired (DESIGN §6.3) |
| §6.4 | Control Messages (v1) | ✅ | Frozen v1 set implemented: `register`/`registered`, `connect`, `session-request`/`session-join`/`session-start`/`wg-pubkey`/`ready`, `udp-offer`/`peer-endpoint`, `session-end`, `error`, `mgmt-request`/`mgmt-response`, `keepalive`. Envelope is `controlMessage` (agent) and `signalingMsg` (hub); receivers ignore unknown fields |
| §6.5 | Tokens (v1) | ✅ | Static hex shared secrets, ACL-keyed, `crypto/subtle` compare, revoke-by-config-removal per DESIGN §6.5. The 0.16 cycle added lifecycle metadata (`issuedAt`/`expiresAt`/`revokedAt` in `internal/hub/auth.go`) plus admin revoke/expiry/rotate (`internal/hub/admin_api.go`, issue #24). Structured/signed tokens remain a deferred 1.x item, not a v1 requirement |
| §6.6 | Backward Compatibility (1.x policy) | ✅ | `protocolVersion` negotiation shipped on `register`/`connect` (absent treated as `1`), `internal/hub/protocol_version_test.go`. The 1.x compatibility contract (covered surfaces, additive-only rules, cross-version matrix) is specified in DESIGN §6.6 |
| §6.7 | Error Handling | 🔶 | Basic error logging; no structured error types or per-channel error handling |
| §6.8 | WireGuard L3 Transport | ✅ | wireguard-go + gVisor netstack on both sides, ephemeral keypairs, zero-admin, hub is zero-knowledge relay |
| §6.8 | UDP Relay Mode | ✅ | Hub UDP relay (port 41820), probe/ready handshake, asymmetric fallback (UDP↔WS bridging), auto-fallback on timeout, `udpHost` advertisement for proxy/tunnel setups |
| §6.8 | Direct Tunnel (P2P) | ✅ | STUN discovery (RFC 5389), endpoint exchange via hub, simultaneous UDP hole punching, fallback cascade (direct → relay → WS) |

---

## §7 - Tela Agent

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §7.1 | Responsibilities | 🔶 | Outbound WS ✅, register ✅, TCP proxy ✅, reconnect ✅, token auth ✅, multi-port ✅, heartbeat 🔶 (WS ping/pong 20s/45s), metadata ✅ (displayName, hostname, os, tags, location, owner via YAML), service discovery ⬜, policy ⬜ |
| §7.2 | Implementation (C/C++, static binary) | 🔶 | Agent is Go (telad), not C/C++. Static binary ✅, cross-compiled ✅ |
| §7.3 | Concurrency Model (libuv) | ⬜ | N/A. Go runtime, not libuv |
| §7.4 | Configuration (`telad.yaml`) | ✅ | YAML config file with `-config` flag. Multi-machine, per-machine token override, rich metadata fields. System path: `%ProgramData%\Tela\telad.yaml` / `/etc/tela/telad.yaml` |
| §7.5 | Logging | 🔶 | Console logging with `[telad]` prefix; no structured/rotated logs |
| §7.6 | Updates | ✅ | Channel-based self-update via `internal/channel`. CLI: `telad channel` (show/set/show-manifest) and `telad update` (self-update). Mgmt actions: `update`, `update-status`, `update-channel`. Verifies SHA-256 against the channel manifest before swapping. See [RELEASE-PROCESS.md](RELEASE-PROCESS.md). |

---

## §8 - Tela Hub

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §8.1 | Responsibilities | 🔶 | Accepts agents/clients ✅, brokers sessions ✅, routes data ✅, token validation ✅ (with issue/expiry/revocation metadata), UDP relay ✅, `/status` API ✅, admin REST API ✅, metadata ✅ (stored and served via register message). No browser user auth |
| §8.2 | Implementation (Go) | ✅ | `telahubd`: Go, `gorilla/websocket`, no Node.js, no MeshCentral |
| §8.3 | Storage (SQLite / Postgres) | 🔶 | Auth config persisted to YAML (hot-reload); session/machine state is in-memory only |
| §8.4 | REST API | 🔶 | Hub `/status` ✅, `/api/history` ✅, `/api/admin/*` ✅ (token/ACL management); no `/api/v1/*` endpoints |
| §8.5 | Multiplexing | 🔮 | Single-channel by design (DESIGN §6.2); channel multiplexing reserved for a hypothetical v2 |
| §8.6 | Logging & Observability | 🔶 | Console logging; no structured logs or metrics |
| §8.7 | Updates | ✅ | Channel-based self-update via `internal/channel`. CLI: `telahubd channel` (show/set/show-manifest) and `telahubd update` (self-update). Admin API: `GET /api/admin/update` (status), `PATCH /api/admin/update` (set channel), `POST /api/admin/update` (trigger). Verifies SHA-256 before swapping. |

---

## §9 - Tela Web (Browser UI)

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §9.1 | Responsibilities (auth, machine list, token, download) | 🔶 | Client download with OS detection ✅; hub console shows machine list ✅, services ✅, session history ✅. No auth, no session token flow |
| §9.2 | Non-Responsibilities | ✅ | Browser is not in data path |
| §9.3 | Helper Launch Reality | 🔶 | Download + manual run ✅; no `tela://` URI scheme; no port-back reporting |
| §9.3 | Constraints (vanilla JS) | ✅ | Landing page is vanilla JS |

---

## §10 - Tela Helper / Client

Note: DESIGN.md describes a "Helper" (Go binary, TCP bridge). The current implementation evolved beyond this: `tela` is the client binary that establishes a full WireGuard L3 tunnel and auto-binds local listeners. It subsumes the Helper role while adding encrypted tunneling.

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §10.1 | Responsibilities | ✅ | WS connect ✅, WG tunnel ✅, TCP bind ✅, bidirectional pipe ✅, TCP_NODELAY ✅, reconnect ✅, token auth ✅, cert pinning ✅ (SHA-256 SPKI on the hub-bound WS and HTTPS dials, `internal/certpin`, TOFU, `tela pin`). Single session per connection (see §6.2) |
| §10.2 | Non-Responsibilities | ✅ | Client stores nothing, needs no admin, doesn't install |
| §10.3 | Distribution & Signing | 🔶 | Three release channels (dev/beta/stable) ✅ with channel manifests, SHA-256 verification, and `tela channel` + `tela update` CLI (plus matching subcommands on telad and telahubd). Custom channel names are also supported for self-hosted channel daemons. GitHub Releases for Windows/Linux/macOS amd64+arm64 ✅. NSIS Windows installer ✅. .deb/.rpm packages ✅. macOS .tar.gz ✅. No code signing yet (Authenticode for Windows, Developer ID for macOS) -- still on the 1.0 blocker list. |
| §10.4 | Data Path | ✅ | `client → tela → WireGuard → Hub → WireGuard → telad → service` working, with optional UDP relay |
| §10.5 | Fallback Modes (in-browser) | ⬜ | No in-browser RDP/SSH client |

---

## §11 - Tela CLI

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §11.1 | Purpose & Rationale | 🔶 | `tela` binary serves as both client and proto-CLI |
| §11.2 | Core Commands | 🔶 | `login`/`logout` ✅, `machines` ✅, `services` ✅, `status` ✅, `connect` ✅ (with `-services`, `-ports`, `-profile` flags), `admin` ✅ (noun-based: `access`, `tokens`, `portals`, `agent`, `hub`, plus `rotate`/`pair-code`), `channel` ✅ (`show`, `set`, `download`), `update` ✅ (channel-aware self-update with SHA-256 verification), `pair` ✅, `pin` ✅ (`tela pin <hub-url> [fingerprint]`, TOFU cert pinning), `mount` ✅ (WebDAV), `files` ✅, `profile` ✅, `remote` ✅, `service` ✅. Portal-based hub name resolution ✅. Local `hubs.yaml` fallback ✅. |

---

## §12 - Security Model

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §12.1 | Identity (Ed25519 agent keys) | 🔶 | Stable `agentId` and `machineRegistrationId` UUIDs shipped (persisted in `telad.state`, carried on `register`; see DESIGN-identity.md and §6.4.2). No Ed25519 keypair yet: agents are not cryptographically authenticated, only ACL-token authenticated |
| §12.2 | Certificate Pinning | 🔶 | SHA-256 SPKI pinning shipped for the `tela` client (hub-bound WS + HTTPS) and the hub-to-hub bridge dial via `internal/certpin`, with TOFU on first connect and `tela pin`; pins stored per hub in `credentials.yaml` / `hubs.yaml` / `telahubd.yaml`. Agent-side (`telad`) and TelaVisor-portal pinning are deferred post-1.0 (ROADMAP-1.0, issue #23) |
| §12.3 | Session Tokens | 🔶 | Named token identities with role-based ACL (owner/admin/user/viewer) ✅; auto-generated `console-viewer` token ✅; per-machine register/connect ACLs ✅; env-var bootstrap ✅; admin REST API ✅; hot-reload ✅. The 0.16 cycle added `issuedAt`/`expiresAt`/`revokedAt` metadata and admin revoke/expiry/rotate (issue #24). Still hex shared secrets, not signed or single-use JWTs |
| §12.4 | Transport Security (TLS 1.3) | 🔶 | TLS via Cloudflare + Caddy (direct); internal hub↔agent is plain WS |
| §12.5 | E2E Encryption | ✅ | WireGuard provides full E2E encryption (Curve25519 + ChaCha20-Poly1305). Hub is zero-knowledge relay |
| §12.6 | Threat Model | 📄 | Design guidance |

---

## §13 - Authentication

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §13.1 | Tela Standalone (bcrypt, SQLite, cookies, TOTP) | 🔶 | Named token identities with RBAC ✅; YAML-persisted auth config ✅; admin REST API ✅; `tela admin` CLI ✅; env-var bootstrap ✅. No bcrypt/cookies/TOTP (spec vision), but functional token-based auth is complete |
| §13.2 | Awan Saya (SSO) | 🔮 | - |

---

## §14 - End-to-End Usage Flow

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §14.1 | Setup (Tela Standalone) | 🔶 | Hub deployed ✅, Cloudflare Tunnel ✅, Caddy direct ✅, agent registered ✅, token auth + ACL ✅, env-var bootstrap ✅, remote admin ✅, `telad service install` ✅, `telahubd service install` ✅. No browser-based user creation |
| §14.2 | Accessing from Locked-Down Laptop | ✅ | Full path validated: download tela → run → SSH ✅, RDP ✅ (via WireGuard L3 tunnel) |
| §14.3 | In-Browser Fallback | ⬜ | - |

---

## §15 - Literate Coding Standards

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §15.1 | Narrative Header Blocks | ✅ | All Go and JS files have descriptive purpose/architecture headers |
| §15.2 | Inline Intent Comments | 🔶 | Present in critical sections (wsBind, hub relay, netstack) |
| §15.3 | Embedded Protocol Excerpts | ⬜ | DESIGN §6 is now the frozen, authoritative v1 protocol, but source files do not yet embed excerpts of it |
| §15.4 | "DO NOT MODIFY" Markers | ⬜ | - |
| §15.5 | Explicit Invariants | ⬜ | - |
| §15.6 | No Hidden Magic | ✅ | Code is straightforward |
| §15.7 | Language-Specific Conventions | 🔶 | Go conventions followed; JS hub is simple |

---

## §16 - LLM Agent Guardrails

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §16.1-16.3 | Hard Rules, Allowed Changes, Human Review | 📄 | Process guidance; enforced by workflow, not code |

---

## §17 - Roadmap

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §17.1 | Phase 1 - Minimum Viable Fabric | 🔶 | Core WG tunnel works, reconnect ✅, token auth ✅, multi-port ✅, UDP relay ✅. Protocol/mux/full-auth not started |
| §17.2 | Phase 2 - Fabric Extensions | ⬜ | - |
| §17.3 | Phase 3 - Substrate for Awan Saya | 🔮 | - |
| §17.4 | Phase 4 - Awan Saya v0.1 | 🔮 | - |

---

## §18 - Awan Saya Architecture

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §18.1 | Three Connectivity Models | 🔶 | Model A (direct) demonstrated via Cloudflare Tunnel + Caddy direct; Models B & C are Awan Saya scope |
| §18.2 | Hub Registry | 🔶 | Tela now ships an in-tree portal implementing the hub registry/discovery contract (DESIGN-portal.md): `internal/portal` + file-backed `internal/portal/store/file`, the standalone `cmd/telaportal` daemon, and TelaVisor Portal mode. Awan Saya remains the multi-tenant Postgres reference. Persistent relay-connection registration (Model B) stays Awan Saya scope |
| §18.3 | Relay Protocol | 🔮 | - |
| §18.4 | What Changes in Tela (Nothing) | 📄 | - |
| §18.5 | Eliminating External Dependencies | 🔮 | - |
| §18.6 | Analogy Summary | 📄 | - |

---

## §19 - Risks & Mitigations

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §19.1-19.2 | Risks & Mitigations | 📄 | Ongoing awareness items |

---

## §20 - Testing Strategy

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §20.1 | Protocol Conformance Tests | 🔶 | Unit tests across `certpin`, `channel`, `client`, `credstore`, `hub`, `portal`. Protocol-level coverage in `internal/hub`: `protocol_version_test.go`, `session_tokens_test.go`, `auth_test.go`, `admin_api_test.go`, `access_version_test.go`, `services_test.go`. Not yet an exhaustive conformance corpus |
| §20.2 | Integration Tests | 🔶 | `internal/teststack` runs a hub and agent in-process on localhost and drives registration/status assertions; bridged-session end-to-end tests live in `stack_test.go`. Pushing real bytes through a full client tunnel in-harness is not yet automated |
| §20.3 | Regression Suite | 🔶 | The unit and integration tests run in CI (`go test -race -count=1 ./...`) on every push and PR, so they guard against regressions. No separately curated regression corpus beyond that |
| §20.4 | What Is Not Tested in Tela Core | 📄 | - |

---

## Summary

| Status | Count | Meaning |
|--------|-------|---------|
| ✅ Done | 23 | Working implementation |
| 🔶 Partial | 32 | Covers some aspects, gaps remain |
| ⬜ Not started | 8 | No implementation |
| 🔮 Future | 8 | Awan Saya / v2 / Phase 3+ |
| 📄 Doc-only | 15 | No code artifact needed |

### What's working now (POC)

1. **WireGuard L3 tunnel**: E2E encrypted, zero-admin, zero-install on both sides (gVisor netstack)
2. **UDP relay**: Eliminates TCP-over-TCP; auto-fallback to WebSocket; asymmetric bridging; `udpHost` advertisement for proxy/tunnel deployments
3. **Multi-port forwarding**: telad advertises ports, tela auto-binds local listeners
4. **Token auth with RBAC**: Named identities (owner/admin/user), per-machine ACLs, env-var bootstrap for Docker, remote management via `tela admin` CLI and admin REST API, hot-reload (no restart needed)
5. **Auto-reconnect**: Both tela and telad reconnect on disconnect
6. **Cross-platform client**: Windows, Linux, macOS (Intel + ARM) binaries served from hub
7. **Dual ingress**: Cloudflare Tunnel + Caddy direct
8. **Hub /status API**: JSON endpoint for monitoring
9. **Direct tunnel (P2P)**: STUN hole punching with automatic fallback cascade (direct → UDP relay → WebSocket)
10. **CLI remote management**: `tela remote add` configures a hub directory, `tela remote remove` removes it (`tela login`/`tela logout` kept as deprecated aliases)
11. **Remote-based hub name resolution**: Short hub names resolved via configured remotes' `/api/hubs` with local `hubs.yaml` fallback
12. **File sharing**: Native file transfer protocol (list, read, write, delete, mkdir, rename, move) with live change notifications, sandboxed per machine, accessible via `tela files` CLI and TelaVisor Files tab
13. **Frozen v1 wire protocol**: 7-byte relay frame header (`internal/relay/frame.go`), a fixed v1 control-message set, and `protocolVersion` negotiation (absent treated as `1`), all specified authoritatively in DESIGN §6
14. **Certificate pinning**: SHA-256 SPKI pinning on the client's hub-bound WS and HTTPS dials and on the hub-to-hub bridge, via `internal/certpin`, with TOFU on first connect and `tela pin`
15. **Session-token lifecycle**: `issuedAt`/`expiresAt`/`revokedAt` metadata plus admin revoke, expiry, and rotation on top of the RBAC token model (issue #24)
16. **Automated test suite**: unit tests across `certpin`, `channel`, `client`, `credstore`, `hub`, and `portal`, plus the in-process `internal/teststack` hub+agent integration harness, all run under `go test -race` in CI
17. **In-tree portal**: `internal/portal` with a file-backed store, the standalone `cmd/telaportal` daemon, and TelaVisor Portal mode implement the hub registry/discovery contract without Awan Saya

### Biggest gaps to 1.0

1. **Code signing** (§10.3): no Authenticode (Windows) or Developer ID (macOS) signing yet; still on the 1.0 blocker list
2. **Browser-based user auth** (§13.1): token-based CLI/API auth is complete; the spec's bcrypt + cookies + TOTP browser flow is not built
3. **Ed25519 agent identity** (§12.1): stable `agentId`/`machineRegistrationId` UUIDs shipped, but agents still have no cryptographic keypair and are authenticated only by ACL token
4. **Agent-side and TelaVisor cert pinning** (§12.2): the client and hub-bridge dials pin; the `telad` agent dial and TelaVisor portal pinning are deferred post-1.0
5. **Internal-hop transport security** (§12.4, §6.1): TLS is terminated at the edge; the internal hub-to-agent hop is plain WS
6. **End-to-end tunnel tests** (§20.2): `teststack` covers registration and bridged sessions, but pushing real bytes through a client tunnel in-harness is not yet automated

Deliberately out of v1 scope (not gaps): channel multiplexing (§6.2), which is reserved for a hypothetical v2, and structured/signed session tokens (§6.5, issue #24), which are a planned 1.x enhancement rather than a v1 requirement.

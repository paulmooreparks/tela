# Tela — Implementation Status

Traceability matrix mapping each DESIGN.md section to current implementation status.

**Legend:**
- ✅ **Done** — Implemented (at least POC-level working code)
- 🔶 **Partial** — Some aspects implemented, gaps remain
- ⬜ **Not started** — No implementation yet
- 🔮 **Future** — Awan Satu scope or Phase 3+
- 📄 **Doc-only** — Informational section, no implementation required

Last updated: 2026-03-05

---

## §0–§3 — Identity, Goals, Philosophy

These sections are design guidance; "implementation" means the codebase reflects the principles.

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §0 | Purpose of This Document | 📄 | — |
| §0.1 | Glossary | 📄 | — |
| §1.1 | Name | 📄 | — |
| §1.2 | Positioning | 📄 | — |
| §1.3 | What Tela Is | 📄 | — |
| §1.4 | License | ✅ | Apache 2.0 in repo |
| §2.1 | Goals | 🔶 | Outbound-only tunneling works; multiplexing, protocol-agnostic channels, multi-platform agents not yet |
| §2.2 | Non-Goals | 📄 | — |
| §3.1–3.4 | Design Philosophy & Invariants | 📄 | Guiding principles; no code artifact |

---

## §4 — Architecture Overview

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §4.1 | Components | 🔶 | `telad` (Go agent ✅), `telahubd` (Go hub ✅), `tela` (Go client ✅), Web (landing page + downloads ✅), CLI (⬜), Awan Satu (🔮) |
| §4.2 | Data Flow — Agent Connection | ✅ | `telad`: outbound WS, register, reconnect, token auth |
| §4.2 | Data Flow — Browser Orchestration | 🔶 | Landing page with OS-detected download links; no login, session tokens, or machine list |
| §4.2 | Data Flow — Client Connection | ✅ | `tela`: WS connect, WireGuard tunnel, auto-bind local listeners, reconnect |
| §4.2 | Data Flow — Multiplexed Channels | ⬜ | One WS per session, no channel multiplexing |
| §4.3 | Component Interaction Model | 🔶 | Client→Hub and Agent→Hub paths work; Browser→Hub auth/session path not implemented |

---

## §5 — MeshCentral Integration Boundary

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §5.1 | Components Reused | ⬜ | POC is written from scratch; no MeshCentral code integrated |
| §5.2 | Components Replaced | 📄 | Design guidance for when integration happens |
| §5.3 | Rationale | 📄 | — |

---

## §6 — Protocol Specification

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §6.1 | Transport (TLS 1.3, WSS, binary) | 🔶 | WSS via Cloudflare tunnel; internal plain WS; binary frames for WG datagrams, JSON for control |
| §6.2 | Multiplexing | ⬜ | No channel mux; one WS = one session |
| §6.3 | Frame Format (12-byte header) | ⬜ | POC uses raw WS messages, no Tela frame header |
| §6.4 | Control Messages | 🔶 | POC uses `register`/`connect`/`ready`/`session-start`/`wg-pubkey`/`udp-offer`/`error`; spec defines `hello`/`welcome`/`ready`/`open`/`close`/`error`/`heartbeat` |
| §6.5 | Session Tokens (JWT) | ⬜ | Token auth exists (`-token` flag) but it's a shared secret, not signed JWTs |
| §6.6 | Backward Compatibility | ⬜ | No versioning in POC messages |
| §6.7 | Error Handling | 🔶 | Basic error logging; no structured error types or per-channel error handling |
| §6.8 | WireGuard L3 Transport | ✅ | wireguard-go + gVisor netstack on both sides, ephemeral keypairs, zero-admin, hub is zero-knowledge relay |
| §6.8 | UDP Relay Mode | ✅ | Hub UDP relay (port 41820), probe/ready handshake, asymmetric fallback (UDP↔WS bridging), auto-fallback on timeout |
| §6.8 | Direct Tunnel (P2P) | ✅ | STUN discovery (RFC 5389), endpoint exchange via hub, simultaneous UDP hole punching, fallback cascade (direct → relay → WS) |

---

## §7 — Tela Agent

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §7.1 | Responsibilities | 🔶 | Outbound WS ✅, register ✅, TCP proxy ✅, reconnect ✅, token auth ✅, multi-port ✅, heartbeat ⬜, service discovery ⬜, policy ⬜, metadata ⬜ |
| §7.2 | Implementation (C/C++, static binary) | 🔶 | Agent is Go (telad), not C/C++. Static binary ✅, cross-compiled ✅ |
| §7.3 | Concurrency Model (libuv) | ⬜ | N/A — Go runtime, not libuv |
| §7.4 | Configuration (`telad.yaml`) | ✅ | YAML config file with `-config` flag. Multi-machine, per-machine token override, rich metadata fields. System path: `%ProgramData%\Tela\telad.yaml` / `/etc/tela/telad.yaml` |
| §7.5 | Logging | 🔶 | Console logging with `[telad]` prefix; no structured/rotated logs |
| §7.6 | Updates | ⬜ | No update mechanism |

---

## §8 — Tela Hub

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §8.1 | Responsibilities | 🔶 | Accepts agents/clients ✅, brokers sessions ✅, routes data ✅, token validation ✅, UDP relay ✅, `/status` API ✅. No user auth, no session tokens, no metadata |
| §8.2 | Implementation (Go) | ✅ | `telahubd` — Go, `gorilla/websocket`, no Node.js, no MeshCentral |
| §8.3 | Storage (SQLite / Postgres) | ⬜ | In-memory only; no persistence |
| §8.4 | REST API | 🔶 | Hub `/status` endpoint ✅; Portal `/api/hubs` endpoint ✅; no `/api/v1/*` endpoints |
| §8.5 | Multiplexing | ⬜ | No channel multiplexing |
| §8.6 | Logging & Observability | 🔶 | Console logging; no structured logs or metrics |
| §8.7 | Updates | ⬜ | No update mechanism |

---

## §9 — Tela Web (Browser UI)

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §9.1 | Responsibilities (auth, machine list, token, download) | 🔶 | Client download with OS detection ✅; no auth, no machine list, no session token flow |
| §9.2 | Non-Responsibilities | ✅ | Browser is not in data path |
| §9.3 | Helper Launch Reality | 🔶 | Download + manual run ✅; no `tela://` URI scheme; no port-back reporting |
| §9.3 | Constraints (vanilla JS) | ✅ | Landing page is vanilla JS |

---

## §10 — Tela Helper / Client

Note: DESIGN.md describes a "Helper" (Go binary, TCP bridge). The current implementation evolved beyond this: `tela` is the client binary that establishes a full WireGuard L3 tunnel and auto-binds local listeners. It subsumes the Helper role while adding encrypted tunneling.

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §10.1 | Responsibilities | ✅ | WS connect ✅, WG tunnel ✅, TCP bind ✅, bidirectional pipe ✅, TCP_NODELAY ✅, reconnect ✅, token auth ✅. No cert pinning, no session token, no channel lifecycle |
| §10.2 | Non-Responsibilities | ✅ | Client stores nothing, needs no admin, doesn't install |
| §10.3 | Distribution & Signing | 🔶 | Served from Hub ✅ (Windows/Linux/macOS Intel+ARM). No code signing |
| §10.4 | Data Path | ✅ | `client → tela → WireGuard → Hub → WireGuard → telad → service` working, with optional UDP relay |
| §10.5 | Fallback Modes (in-browser) | ⬜ | No in-browser RDP/SSH client |

---

## §11 — Tela CLI

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §11.1 | Purpose & Rationale | 🔶 | `tela` binary serves as both client and proto-CLI |
| §11.2 | Core Commands | 🔶 | `login`/`logout` ✅, `machines` ✅, `services` ✅, `status` ✅, `connect` ✅. Portal-based hub name resolution ✅. Local `hubs.yaml` fallback ✅. |

---

## §12 — Security Model

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §12.1 | Identity (Ed25519 agent keys) | ⬜ | No agent identity; machineId is a plain string |
| §12.2 | Certificate Pinning | ⬜ | Neither agent nor client validates cert fingerprint |
| §12.3 | Session Tokens | 🔶 | Shared-secret token auth (`-token` flag) ✅; not JWTs, not single-use |
| §12.4 | Transport Security (TLS 1.3) | 🔶 | TLS via Cloudflare + Caddy (direct); internal hub↔agent is plain WS |
| §12.5 | E2E Encryption | ✅ | WireGuard provides full E2E encryption (Curve25519 + ChaCha20-Poly1305). Hub is zero-knowledge relay |
| §12.6 | Threat Model | 📄 | Design guidance |

---

## §13 — Authentication

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §13.1 | Tela Standalone (bcrypt, SQLite, cookies, TOTP) | 🔶 | Shared-secret token auth exists; no bcrypt/SQLite/cookies/TOTP |
| §13.2 | Awan Satu (SSO) | 🔮 | — |

---

## §14 — End-to-End Usage Flow

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §14.1 | Setup (Tela Standalone) | 🔶 | Hub deployed ✅, Cloudflare Tunnel ✅, Caddy direct ✅, agent registered ✅. No user creation, no provisioning tokens |
| §14.2 | Accessing from Locked-Down Laptop | ✅ | Full path validated: download tela → run → SSH ✅, RDP ✅ (via WireGuard L3 tunnel) |
| §14.3 | In-Browser Fallback | ⬜ | — |

---

## §15 — Literate Coding Standards

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §15.1 | Narrative Header Blocks | ✅ | All Go and JS files have descriptive purpose/architecture headers |
| §15.2 | Inline Intent Comments | 🔶 | Present in critical sections (wsBind, hub relay, netstack) |
| §15.3 | Embedded Protocol Excerpts | ⬜ | No frozen protocol to embed yet |
| §15.4 | "DO NOT MODIFY" Markers | ⬜ | — |
| §15.5 | Explicit Invariants | ⬜ | — |
| §15.6 | No Hidden Magic | ✅ | Code is straightforward |
| §15.7 | Language-Specific Conventions | 🔶 | Go conventions followed; JS hub is simple |

---

## §16 — LLM Agent Guardrails

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §16.1–16.3 | Hard Rules, Allowed Changes, Human Review | 📄 | Process guidance; enforced by workflow, not code |

---

## §17 — Roadmap

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §17.1 | Phase 1 — Minimum Viable Fabric | 🔶 | Core WG tunnel works, reconnect ✅, token auth ✅, multi-port ✅, UDP relay ✅. Protocol/mux/full-auth not started |
| §17.2 | Phase 2 — Fabric Extensions | ⬜ | — |
| §17.3 | Phase 3 — Substrate for Awan Satu | 🔮 | — |
| §17.4 | Phase 4 — Awan Satu v0.1 | 🔮 | — |

---

## §18 — Awan Satu Architecture

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §18.1 | Three Connectivity Models | 🔶 | Model A (direct) demonstrated via Cloudflare Tunnel + Caddy direct; Models B & C are Awan Satu scope |
| §18.2 | Hub Registry | 🔮 | — |
| §18.3 | Relay Protocol | 🔮 | — |
| §18.4 | What Changes in Tela (Nothing) | 📄 | — |
| §18.5 | Eliminating External Dependencies | 🔮 | — |
| §18.6 | Analogy Summary | 📄 | — |

---

## §19 — Risks & Mitigations

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §19.1–19.2 | Risks & Mitigations | 📄 | Ongoing awareness items |

---

## §20 — Testing Strategy

| Section | Title | Status | Notes |
|---------|-------|--------|-------|
| §20.1 | Protocol Conformance Tests | ⬜ | No test suite |
| §20.2 | Integration Tests | ⬜ | Manual testing only |
| §20.3 | Regression Suite | ⬜ | — |
| §20.4 | What Is Not Tested in Tela Core | 📄 | — |

---

## Summary

| Status | Count | Meaning |
|--------|-------|---------|
| ✅ Done | 17 | Working implementation |
| 🔶 Partial | 19 | POC covers some aspects |
| ⬜ Not started | 19 | No implementation |
| 🔮 Future | 8 | Awan Satu / Phase 3+ |
| 📄 Doc-only | 14 | No code artifact needed |

### What's working now (POC)

1. **WireGuard L3 tunnel** — E2E encrypted, zero-admin, zero-install on both sides (gVisor netstack)
2. **UDP relay** — Eliminates TCP-over-TCP; auto-fallback to WebSocket; asymmetric bridging
3. **Multi-port forwarding** — telad advertises ports, tela auto-binds local listeners
4. **Token auth** — Shared-secret `-token` flag on both sides, hub validates
5. **Auto-reconnect** — Both tela and telad reconnect on disconnect
6. **Cross-platform client** — Windows, Linux, macOS (Intel + ARM) binaries served from hub
7. **Dual ingress** — Cloudflare Tunnel (`tela.awansatu.net`) + Caddy direct (`tela-local.awansatu.net`)
8. **Hub /status API** — JSON endpoint for monitoring
9. **Direct tunnel (P2P)** — STUN hole punching with automatic fallback cascade (direct → UDP relay → WebSocket)
10. **CLI login/logout** — `tela login` authenticates with a portal, `tela logout` removes credentials
11. **Portal-based hub name resolution** — Short hub names resolved via portal `/api/hubs` with local `hubs.yaml` fallback

### Biggest gaps to Phase 1 spec

1. **Channel multiplexing** (§6.2) — One WS per session; spec requires muxed channels
2. **Framed binary protocol** (§6.3) — Raw WS messages; spec requires 12-byte frame header
3. **Full authentication** (§13.1) — Shared-secret tokens; spec requires bcrypt + cookies + TOTP
4. **Session tokens / JWT** (§6.5) — No signed token issuance; client connects with machineId + shared secret
5. **Agent identity** (§12.1) — No Ed25519 keys; agents identified by string only
6. **Certificate pinning** (§12.2) — Neither side validates Hub cert fingerprint
7. **REST API** (§8.4) — Only `/status`; spec requires full `/api/v1/*`
8. **Test suite** (§20) — No automated tests
9. **Multiple simultaneous sessions** — One session per machine at a time

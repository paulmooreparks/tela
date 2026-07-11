# Tela 1.0 Readiness

This is the living document tracking what is required to ship Tela 1.0. It is not a feature roadmap (see [TODO.md](TODO.md) for that) — it is a hardening, polish, and operational-readiness checklist.

The premise: once we ship 1.0, we will maintain backward compatibility religiously. That means any cruft, any half-finished surface, any unversioned protocol field becomes a permanent maintenance burden. The goal of this document is to make sure that what gets locked in at 1.0 is what we actually want to live with.

**Status as of 2026-07-11:** Roughly **0.75 to 0.8** of a 1.0 release. Since the April snapshot: cert pinning shipped (scoped), structured session tokens shipped, the protocol froze (protocolVersion negotiation plus the v1 spec in DESIGN.md section 6), per-service access control landed and its client-side guard was fixed end to end, the portal architecture shipped (internal/portal, telaportal, TelaVisor Portal mode), the test harness exists, and every scope decision below carries a written ruling. The remaining mass is the test suites, code signing, audit retention, observability, the user-facing book chapters, and the launch surface.

Granular work is tracked in GitHub Issues under [milestone `1.0`](https://github.com/paulmooreparks/tela/milestone/2). This document holds the narrative and design rationale; the milestone holds state. When a task ships, its issue closes; keep this doc for the "why" around each track, not the "what's left."

---

## Where Tela stands today

### What is real and works

- **Core protocol and tunnel.** WireGuard userspace via gVisor netstack — no TUN, no admin, both sides outbound. Solid.
- **UDP relay** with auto-fallback to WebSocket.
- **Direct P2P** with STUN hole-punching and the full fallback cascade.
- **Three-binary architecture** (`tela`, `telad`, `telahubd`) is clean. One Go module, no runtime dependencies.
- **Cross-platform service install** (Windows SCM, systemd, launchd).
- **Token-based RBAC** with owner/admin/user/viewer roles and per-machine ACLs (register/connect/manage). Hot-reload of auth changes works.
- **Unified `/api/admin/access` API** is RESTful and clean. The legacy verb-based duplicates are gone.
- **Pairing codes** for onboarding without copy-pasting hex tokens.
- **File sharing** including the WebDAV mount (no kernel drivers).
- **TelaVisor** has Infrastructure mode at sysadmin parity: hub management (logs, software update, restart, version badges), agent management (same), per-identity access view, tokens with pairing codes. TDL gives a coherent visual identity.
- **Awan Saya** as a portal layer: hub directory, multi-org access, per-org dashboard, hub administration, agent fleet view, demo mode for unauthenticated visitors, responsive layout down to phone widths.
- **Self-update** for hub and agent, including the OS service manager case (no orphan processes).
- **Documentation** is unusually thorough for a pre-1.0 project: DESIGN.md, REFERENCE.md, the DESIGN-* set, ACCESS-MODEL.md, CONFIGURATION.md, STATUS.md, TelaVisor.md, multiple how-tos, the Tela Design Language spec, and a STATUS.md traceability matrix.

---

## Blockers (must ship for 1.0)

These are non-negotiable. A 1.0 without them would be embarrassing or actively unsafe.

### Tests

The harness exists and the security-critical paths (auth store, admin API, wsbind transport, WG reconnect) now carry real suites, and a CI coverage gate keeps them from silently eroding before 1.0 locks the access model and wire protocol.

Already done:
- [x] `internal/teststack`: the in-process hub + agent + client harness (`New(t)` returns a running triple with cleanup), with smoke tests for registration, client connect with listener bind, multi-machine, protocol version reporting, bridged sessions, and reset-between-runs. Both #6 acceptance boxes are now closed: `Stack.Close()` runs a goroutine-leak check (baseline captured in `NewWithConfig`, enforced in CI), and two other suites consume the harness over real HTTP (`internal/hub/admin_api_e2e_test.go` and `internal/portal/teststack_test.go`).
- [x] `permuteArgs` flag reordering tests (`cmd/tela/admin_test.go`, 16 sub-tests)
- [x] `internal/channel` tests: manifest parsing/validation, URL helpers, Fetcher cache + stale-on-failure, VerifyReader (94.4% coverage)
- [x] `internal/credstore` tests: round-trip, normalization, permission bits, edge cases (62.7% coverage)
- [x] `internal/certpin` tests (pin parse/normalize/verify, TOFU capture) and `internal/client` pinning + capability-probe tests
- [x] `internal/hub` session-token lifecycle and protocol-version tests
- [x] `internal/portal`, `store/file`, `portalclient`, and `portalaggregate` tests, including the portal conformance harness

The security-boundary test wave is complete:
- [x] [#7](https://github.com/paulmooreparks/tela/issues/7): Auth store unit tests
- [x] [#8](https://github.com/paulmooreparks/tela/issues/8): Admin API endpoint tests
- [x] [#9](https://github.com/paulmooreparks/tela/issues/9): Ring buffer history tests
- [x] [#10](https://github.com/paulmooreparks/tela/issues/10): Portal registration and sync-token flow tests
- [x] [#11](https://github.com/paulmooreparks/tela/issues/11): WireGuard handshake-on-reconnect end-to-end test
- [x] [#12](https://github.com/paulmooreparks/tela/issues/12): wsbind transport test (WS, UDP relay, direct UDP)
- [x] [#13](https://github.com/paulmooreparks/tela/issues/13): Coverage gate (release criterion; tracks the others)

Coverage floors for the security-critical package set (`internal/hub`, `internal/wsbind`, `internal/certpin`, `internal/credstore`, `internal/client`) are enforced in CI via `tools/covcheck` against `tools/covcheck/floors.yaml`; the 1.0 milestone can point at `.github/workflows/ci.yml` going red on any regression below the recorded floor. `internal/client`'s floor is currently an anti-deletion backstop rather than a true erosion gate, because package-level dilution makes a higher number meaningless there; card tela-71 makes its security paths properly gateable and then ratchets the floor.

### Release engineering — **complete**

All release-engineering blockers shipped. Summary of what landed:

- GitHub Actions workflow building Linux/macOS/Windows binaries for amd64 and arm64 on tag push (`release.yml`), with reproducible builds (pinned Go version, `-trimpath`, deterministic ldflags), checksum files, and release notes generated from commit messages
- CI workflow runs build, vet, test, gofmt, and `go mod tidy` checks on every push and PR (`ci.yml`); cross-compile sanity check across all 6 target triples
- Three release channels (dev / beta / stable) with manifest-based self-update, documented promotion model (`RELEASE-PROCESS.md`), and atomic tag-reservation via the GitHub API
- Promotion workflow (`promote.yml`) wired to `release.yml` via `workflow_call`
- Self-update CLI on every binary with `channel` and `update` subcommands accepting any valid channel name (including custom channels)
- Self-update API on telahubd with matching agent management proxy
- Channel selectors in TelaVisor and Awan Saya

### Code signing

Windows SmartScreen flags unsigned binaries with a full-screen warning; macOS Gatekeeper blocks unsigned binaries entirely. For 1.0 we sign both platforms.

Open issues:
- [#14](https://github.com/paulmooreparks/tela/issues/14) — Authenticode certificate for Windows binaries
- [#15](https://github.com/paulmooreparks/tela/issues/15) — Apple Developer ID + notarization for macOS binaries
- [#16](https://github.com/paulmooreparks/tela/issues/16) — Document the signing process reproducibly
- [#17](https://github.com/paulmooreparks/tela/issues/17) — CI integration for tag-push releases

### Protocol freeze (complete)

The freeze landed in the 0.16 cycle. `register` and `connect` carry `protocolVersion: 1` with pre-0.16 binaries treated as v1 (#18); DESIGN.md section 6 is the frozen v1 wire specification with full message schemas and the relay frame referenced to DESIGN-relay-gateway.md section 2 (#19); section 6.6 is the 1.x backward-compatibility policy with the covered and not-covered surfaces spelled out (#20); section 6.2 records single-channel as the v1 shape with the mux experiment preserved as a deferred sketch and `session_id` reserved for a hypothetical v2 (#21); section 6.9 records the no-public-Go-API decision, Option B, everything stays `internal/` (#22). All five issues are closed. From here, wire changes are additive-only per 6.6 until a v2 bump.

### Cert pinning — **shipped (scoped) for 1.0**

Tela and TelaVisor used to trust whatever TLS certificate the hub presented. Pinning closes the MITM class of attack by tying a remote entry to a specific certificate fingerprint, with TOFU on first connect and explicit refusal on mismatch.

What ships at 1.0 (PRs #72 and #73): SHA-256 SPKI pinning on `tela CLI -> hub` (every hub-bound WebSocket and HTTPS path), and on the bridge gateway's `telahubd -> destination hub` dial. Pin storage is per-hub in `~/.tela/credentials.yaml`, `hubs.yaml`, and `telahubd.yaml` (under `bridges[]`). CLI surface: `tela pin <hub-url> [fingerprint]` and `tela login -pin`. TOFU on first connect captures and logs the fingerprint; mismatch refuses the connection with `certpin.ErrMismatch`.

What is deliberately **out of scope** for 1.0: `TelaVisor -> portal` pinning and `portal -> hub` pinning enforced by the portal. The reasoning, including the HPKP retrospective and the Awan Saya commercial considerations, is documented in [DESIGN-cert-pinning-portal.md](DESIGN-cert-pinning-portal.md). Short version: defaulting to pinned would brick legitimate users on corporate networks with MITM proxies, every cert rotation would become a coordinated rollout, and the threat (rogue CA mints a cert for the portal's domain) is mitigated more effectively by Certificate Transparency monitoring on the portal-operator side than by client-side pinning. The portal -> hub leg is authenticated at the application layer by the admin token, which is rotatable, scoped, and revocable -- a strictly better surface than a pinned TLS leaf cert for that hop.

Issue [#23](https://github.com/paulmooreparks/tela/issues/23) closes against this scope. Optional opt-in TelaVisor-side pinning for self-hosted portal deployments is reachable post-1.0 without protocol changes; see DESIGN-cert-pinning-portal.md §6.

### Session tokens (shipped in the 0.16 cycle)

Token entries now carry optional `issuedAt`, `expiresAt`, and `revokedAt` metadata; every auth check denies revoked or expired tokens per request; `POST /api/admin/tokens/{id}/revoke` revokes without deleting (audit trail preserved, last-owner 409 guard); rotation refreshes `issuedAt` and clears `revokedAt`, doubling as the unrevoke path; `tela admin tokens` gained `-expires`, `revoke`, and lifecycle columns; TelaVisor's Access tab surfaces the lifecycle with revoke and rotate-to-re-enable. Pre-0.16 entries migrate transparently and keep `issuedAt` blank rather than synthesized. Issue [#24](https://github.com/paulmooreparks/tela/issues/24) is closed. The remaining token ambitions from the original sketch (scope claims beyond role, signed token formats) are 1.x material under the additive rules, not 1.0 debt.

### Audit log retention

The hub's event history is an in-memory ring buffer with undocumented retention, no persistence across restart, and no way to ship events to an external aggregator. For 1.0: documented retention story, optional on-disk persistence, pluggable log-shipping, and a compliance-reviewer-friendly writeup of what is and is not in the log.

Tracked as a single bundled issue: [#25](https://github.com/paulmooreparks/tela/issues/25).

---

## Scope decisions for 1.0

These are the ambitions a reader can reasonably project onto Tela. Each one needed a written decision before 1.0, because the difference between "deferred" and "non-goal" is the difference between a healthy roadmap and a permanent expectations gap. The premise of this document is that whatever ships at 1.0 becomes a permanent maintenance burden; the same applies to whatever Tela promises *not* to be.

**As of 2026-07-11, every entry below carries its decision.** For each item: what it is, the decision and its reasoning, and what the post-1.0 path looks like where it is deferred.

### Routed mesh networking
- [x] Decision: **non-goal for 1.0.** Post-1.0 only if real-world utility is demonstrated against the existing transport upgrade cascade.
- Tela has direct peer-to-peer as a transport tier, but it is not a routed mesh in the Tailscale or Nebula sense. Most traffic still travels client to hub to agent. The [introduction](book/src/introduction.md) and the [glossary](book/src/glossary.md) explicitly disclaim mesh routing under the leaf-spine framing.
- The current lean is non-goal. The use cases that make Tailscale's mesh worth its complexity (peer-to-peer LAN performance, NAT traversal independence) are mostly already covered by Tela's existing WS -> UDP relay -> direct P2P fallback. The use cases that mesh would *additionally* unlock (agent-to-agent routing without a hub on the data path, multi-hop topologies built out of agents) need to be demonstrated against real demand before the design work is justified.
- **Distinct from the relay gateway, which is in scope for 1.0** (see *Important: Relay gateway* below). The relay gateway moves traffic between hubs while preserving the spine-and-leaves shape and the blind-relay property end to end. Mesh moves traffic between agents without a hub on the data path. They are not the same feature and the mesh decision does not constrain the relay gateway design.
- If post-1.0 work on mesh is ever picked up: sketch what it would take (routing table, multi-hop key exchange, NAT traversal beyond the current STUN cascade) and what `protocolVersion` would carry it.

### Hub federation
- [x] Decision (2026-07-11): **deferred to 1.x.** Tracked as [#49](https://github.com/paulmooreparks/tela/issues/49), closed by this writeup.
- Today, identity is hub-scoped. A user with a token on hub A has no relationship to hub B unless they hold a separate token there. The portal layer (Awan Saya) papers over this for end users but does not fix it at the protocol level.
- What deferral means: through the 1.x line, the portal contract ([DESIGN-portal.md](DESIGN-portal.md)) is the one place cross-hub identity is reconciled, and operators who need cross-hub identity run a portal. Federation stays on the table as a protocol-level feature for a future cycle, not a rejected ambition.
- The definition work owed when federation is picked up: the cross-hub trust model, the identity assertion format, revocation propagation between hubs, and what all three do to the access model. None of that lands before the access model freezes at 1.0, which is exactly why it waits: federation designed under freeze pressure would lock in the wrong shapes.

### Single sign-on (SSO/OIDC/SAML)
- [x] Decision (2026-07-11): **the portal's job, permanently.** The hub will only ever speak its own token format. Tracked as [#50](https://github.com/paulmooreparks/tela/issues/50), closed by this writeup.
- The hub today uses bearer tokens issued by an admin. There is no integration with external identity providers (OpenID Connect, Security Assertion Markup Language, Lightweight Directory Access Protocol). The team-cloud and fleet tiers in the introduction lean on this; a real organization will not provision users by hand.
- Why the hub never grows an identity-provider dependency: the hub is the security boundary of the system, and its trust model has to stay small enough to audit end to end. Every IdP integration imports someone else's token semantics, claim mapping, clock behavior, and revocation latency into that boundary. The portal is where organizational identity already lives (accounts, orgs, teams, memberships), so protocol-wise it is the natural place for OIDC to land when demand materializes: the portal authenticates the human against the IdP and provisions hub tokens through the admin API, which is a surface the hub already exposes and already audits.
- The security model document (#42) owes a paragraph setting this expectation for fleet-tier evaluators: hub-native OIDC is not coming, and the supported pattern is IdP at the portal, tokens at the hub.

### Kernel TUN mode (exit node, full IP routing)
- [x] Decision (2026-07-11): **deferred post-1.0, with the two-mode plan below as the published path.** TCP-only userspace operation is an intentional 1.0 tradeoff, not an oversight: it is the property that buys zero-admin, zero-driver installation on both ends. Tracked as [#51](https://github.com/paulmooreparks/tela/issues/51), closed by this writeup.
- Tela currently runs WireGuard entirely in userspace via gVisor netstack. This is the property that eliminates the need for admin rights, kernel drivers, and TUN devices. It also limits Tela to TCP-only tunneling with no OS-level network interface.
- An optional kernel TUN mode would use wireguard-go's `tun.CreateTUN()` instead of `netstack.CreateNetTUN()`. The wireguard-go library already ships both constructors. The result: a real network interface, full IP routing (UDP, ICMP, multicast), exit-node support (route all system traffic through a remote machine), and kernel-speed packet processing. The cost: one-time admin/root elevation to create the TUN device.
- **The two-mode model.** Userspace mode (default, no admin, TCP only) and kernel mode (opt-in, elevation required, full IP). Same hub, same agent protocol, same access model, same management tools. A fleet could mix both modes. A user could run userspace on a locked-down corporate laptop and kernel mode on a home machine.
- **Why this matters.** The combination of both modes covers two populations that no single tool covers today: users who cannot install Tailscale on their corporate laptop (userspace mode), and users who want Tailscale's capabilities but self-hosted with their own hub and management UI (kernel mode).
- **Implementation scope.** A `-tun` flag on `tela` and `telad` that switches the constructor. The real work is platform-specific route table management, DNS interception for exit-node mode, elevation prompts, and graceful TUN interface cleanup on exit.
- **Android client.** An Android client would likely use kernel mode by default (Android's VpnService API provides a TUN interface without root). This is a natural extension of the same two-mode model.
- User-facing docs state the tradeoff plainly: Tela is TCP-only by design in userspace mode, and this roadmap item is the pointer for the kernel-mode plan.

### Multi-tenant hub
- [x] Decision (2026-07-11): **deferred to 1.x.** Tracked as [#52](https://github.com/paulmooreparks/tela/issues/52), closed by this writeup.
- Today the answer to "I have multiple teams" is "run multiple hubs." This is fine at small scale and starts to bite at the fleet tier. A multi-tenant hub would let one process serve multiple isolated organizations with separate identity, ACL, and history surfaces.
- Through 1.x, the recommended scaling pattern stays hub-per-tenant with the portal layer stitching hubs together for end users. Process-boundary isolation is also the stronger isolation story: a tenant cannot leak into another tenant's config, audit log, or session table when they do not share a process.
- The definition work owed when multi-tenancy is picked up: what isolation means for config, storage, the audit log, and the admin API, plus the migration path from "one hub per team" to "tenants on a shared hub." The audit-log retention work (#25) and any storage changes should keep this in peripheral vision so 1.x multi-tenancy does not require unwinding them.

### Portal architecture: one protocol, many hosts
- [x] Decision (2026-07-11): **ratified as built. TelaVisor is the local portal.** The extraction ran the plan below to completion: the protocol spec is [DESIGN-portal.md](DESIGN-portal.md), the `internal/portal` package with the pluggable file store shipped, `telaportal` exists as the fourth binary, and TelaVisor gained Portal mode. Tracked as [#53](https://github.com/paulmooreparks/tela/issues/53), closed by this writeup; the contract spec issue ([#47](https://github.com/paulmooreparks/tela/issues/47)) reduces to publishing DESIGN-portal.md in the book.
- Residuals tracked separately, not blockers for this decision: the Awan Saya device-code endpoints and approval page, the Awan Saya identity-amendment migration, and the `store/postgres` adapter for `internal/portal`.
- **The question, as originally posed.** The portal existed as one thing: Awan Saya, a Node.js + PostgreSQL web app. There was no portal protocol spec, no Go portal package, and no way for TelaVisor to "be" a portal for personal use. A natural-feeling user request is "let TelaVisor host the portal so I have one-stop shopping that scales from solo to enterprise without two implementations." The naive answer (embed Awan Saya in TelaVisor) is wrong. The right answer requires extracting a portal protocol and a portal service from Awan Saya first.
- **The shape.** Three layers stacked, currently bundled in Awan Saya:
  1. **Directory protocol.** `/.well-known/tela`, `/api/hubs`, the sync-token registration flow. This is the portal contract -- a small wire format that any portal must speak.
  2. **Identity store.** Accounts, organizations, teams, hub memberships, permissions. Today this is Postgres-backed in Awan Saya. A single-user portal does not need any of this beyond "the user."
  3. **Frontend.** The HTML/CSS/JS users see. Today this is the Awan Saya web UI. TelaVisor's UI is a separate frontend that talks to hub admin proxies directly.
- **The plan if we pursue this** (sketched here so we do not have to re-derive it later):
  1. **Write the portal protocol spec first.** A single document under `book/src/architecture/portal-protocol.md` (or `DESIGN-portal.md`) that enumerates the directory endpoints, request/response shapes, auth model, sync-token flow, and the contract any portal implementation must satisfy. ~10 routes, an afternoon of work. **Nothing else can start until this exists** because the spec is what prevents two implementations from drifting.
  2. **Extract `internal/portal`** from Awan Saya into the tela repo as a Go package implementing only the protocol, with pluggable storage. Reference stores: `internal/portal/store/file` (JSON or YAML on disk, single-user, zero deps) and `internal/portal/store/postgres` (multi-org, what Awan Saya already does).
  3. **Decide where the portal service runs.** Three viable hosts that all use the same `internal/portal` package and the same wire protocol:
     - A new fourth binary, working name `telaportal`, for standalone deployments.
     - Folded into telahubd as `telahubd portal serve` for small deployments that want hub plus portal in one process.
     - As a goroutine inside TelaVisor for the personal-use case ("Portal mode", see below).
  4. **TelaVisor gains a Portal mode** alongside Clients and Infrastructure. In personal-use mode it runs the file-backed portal store as a goroutine and exposes the portal HTTP API on `127.0.0.1:NNNN` so other local clients can use it. In team mode it acts as a portal *client* against a remote portal service (Awan Saya, or a self-hosted `telaportal`). The UI is the same in both cases because both speak the same portal HTTP API.
  5. **Awan Saya becomes a portal frontend, not a portal implementation.** The HTML/CSS/JS in `awansatu/www/` continues to exist for users who want a browser; it now talks to the same Go portal service the desktop client talks to. The Postgres + multi-org code becomes a `store/postgres` implementation of the same `internal/portal` interface.
- **The TelaVisor mode rename.** If Portal becomes a top-nav mode, the current "Infrastructure" name needs revisiting. Two reasonable framings:
  - **Clients / Operations / Directory**: each label says exactly what the mode does without metaphor. "Operations" for hub and agent admin (the current Infrastructure mode); "Directory" for portal pages, hub list, org/team/account, hub memberships.
  - **Clients / Infrastructure / Fleet**: leans into the fabric framing but "Fleet" is overloaded ("many agents" vs "many hubs") and probably worse.
  Lean toward Clients / Operations / Directory unless a better label set turns up during the actual work.
- **Why this is the right cut, in one paragraph.** Tela's architecture is "many small things speaking small wire protocols." The hub does not bundle the agent; the agent does not bundle the client. Adding a portal that *actually* bundles the directory, the multi-org store, the web frontend, and the desktop frontend into one process would be the first time the project broke its own composition rule. Extracting the portal service the same way -- small Go package, swappable storage backend, multiple frontends -- keeps the rule and gets the "TelaVisor is one-stop shopping" outcome at the same time, *for free*, because the desktop frontend is just one of the things that can talk to the portal API. The personal-use case where the portal service runs inside TelaVisor is then a deployment topology, not a fork in the codebase.
- **What this depends on.** This decision interacts with **Hub federation** and **Multi-tenant hub** above. With federation deferred to 1.x, the portal is the only place cross-hub identity is reconciled through the 1.x line, which is why the protocol spec carries the weight it does. With multi-tenant hub also deferred, the portal-stitching-many-hubs pattern is the recommended scaling story for at least 1.x.
- **How it actually went.** The spec was written first (DESIGN-portal.md), exactly as planned, and the extraction followed: `internal/portal` with the file store, the conformance harness, `telaportal`, and TelaVisor Portal mode. The `telahubd portal serve` host and the `store/postgres` adapter remain unbuilt options within the same architecture. The mode-rename question resolved itself: Portal shipped as a third mode alongside Clients and Infrastructure, and no rename proved necessary.

### Local names (deterministic loopback addresses, `.tela` DNS)
- [x] Decision (2026-07-11): **deferred to 1.x.** Previously untracked in this roadmap; recorded here so the 1.0 scope statement is complete.
- The design ([DESIGN-local-names.md](DESIGN-local-names.md)) gives each connected machine a deterministic loopback address in 127.88.0.0/16 (Layer 1) and a stable name via an embedded DNS resolver (Layer 2), so users reach services at names and standard ports instead of `localhost:NNNNN`.
- Layer 1 was implemented and then reverted in the 0.10 cycle after Windows loopback shadowing broke local services ([DESIGN-service-routing.md](DESIGN-service-routing.md) is the post-mortem). Re-landing a reverted feature late in the 1.0 cycle is risk without a forcing function, and nothing in the frozen 1.0 surfaces depends on it.
- Port-number ergonomics remain the 1.0 behavior. The design doc is retained as the 1.x plan; its open questions (name suffix, collision policy, coexistence with port bindings) get answered when the work is picked up, and its stale status markers are being corrected separately.

---

## Important (seriously hurts 1.0 if not done)

### Relay gateway (hub-to-hub transit) — **shipped in v0.6**

Tracked: [#26](https://github.com/paulmooreparks/tela/issues/26). Shipped 2026-04-09 as "The relay gateway release" (see [CHANGELOG.md](CHANGELOG.md)). The design doc is [DESIGN-relay-gateway.md](DESIGN-relay-gateway.md); the implementation is in `internal/relay/frame.go` (7-byte wire format) and `internal/hub/bridge.go` (bridge session lifecycle, static bridge config, `reachableThrough` directory field). Bridged-session end-to-end tests live in `internal/teststack/stack_test.go`.

The shape: a hub forwards opaque WireGuard ciphertext on behalf of a client whose target agent is registered with a *different* hub. The WireGuard handshake remains end to end; bridging hubs stay blind to the payload the same way the destination hub is blind today. This is the missing rung of the gateway ladder Tela already implements at every other layer of the stack: path gateway (Layer 7), bridge gateway (Layer 4 inbound), upstream gateway (Layer 4 outbound), single-hop relay gateway (Layer 3, the hub), multi-hop relay gateway (Layer 3, this feature).

What it explicitly does not include:

- **Routing protocols.** The bridging hub knows about the destination hub via static config (and, post-v1, the portal directory). It does not learn topology dynamically. No Border Gateway Protocol (BGP), no spanning tree, no link-state algorithm.
- **Identity federation.** A user with a token on Hub A does not automatically have a token on Hub B. Hub A's bridging token is its own credential. The four federation-shaped scope decisions above are unaffected.
- **Mesh agent-to-agent routing.** The data path is still client to hub (to hub) to agent. See the **Routed mesh networking** scope decision above for why mesh is not in scope and why the relay gateway is not the same thing.

Remaining work tracked against #26 before it closes: a book chapter teaching the gateway family with the relay gateway as a shipped instance. The two implementation gaps flagged in the design-doc critical review (self-bridge detection at config load, WS ping/pong on the bridge leg) have landed.

### telaproject.org launch site (in scope for 1.0, ruled 2026-07-11)

The 1.0 launch ships with the telaproject.org landing site and the book's move under a `/book/` path, per [DESIGN-telaproject-site.md](DESIGN-telaproject-site.md). Previously untracked here; the operator ruled it into 1.0 scope. The site build, the canonical shared TDL stylesheet extraction, and the one-shot migration tied to the v1.0.0 cut are tracked on the project board; the design doc's open questions (canonical host, download detection, feed) get resolved at spec time.

### In-browser fallback
- [x] Decision (2026-07-11): **removed.** The in-browser RDP/SSH fallback client described in early DESIGN.md drafts was never built and is struck from the design; the DESIGN.md sections that described it now carry removal notes. The tela client is already zero-admin and zero-install, which was the itch the fallback was meant to scratch. Tracked as [#28](https://github.com/paulmooreparks/tela/issues/28), closed by this writeup.

### Structured logging

Tracked: [#29](https://github.com/paulmooreparks/tela/issues/29) — migrate from `log.Printf` to `log/slog`, with `telelog` becoming a `slog.Handler`.

### Operational fixes

- [x] [#30](https://github.com/paulmooreparks/tela/issues/30): Graceful HTTP shutdown shipped in the 0.12 cycle (`shutdownTimeout` config, drain logging, two-signal escalation); closed
- [#31](https://github.com/paulmooreparks/tela/issues/31) — Rate limiting on the admin API (token bucket per identity per endpoint)
- [#32](https://github.com/paulmooreparks/tela/issues/32) — Hot reload for non-auth config, or documented restart requirement

### Per-service access control (shipped, guard fixed)

The `services:` filter on `connect` grants shipped in the 0.15 cycle (the access-model schema-freeze forcing function): no filter means all services, a filter restricts the client to the named services, enforced on the hub at session setup, filtered by service name rather than port. The client-side capability guard that refuses service-scoped grants against pre-0.15 hubs was found non-functional during board reconciliation (the probe dialed the raw `ws://` URL over HTTP, refusing every grant unconditionally) and fixed in the 0.16 cycle with fail-closed semantics and a regression test. ACCESS-MODEL.md documents the filter and the guard. Issue [#27](https://github.com/paulmooreparks/tela/issues/27) is closed; COMPAT-0.15.md tracks the remaining operator-warning boxes (release-note boilerplate, book upgrade note).

### Agent identity

Tracked: [#33](https://github.com/paulmooreparks/tela/issues/33) — each agent generates an Ed25519 keypair on first start; hub TOFU's the public key on first registration and verifies signed registration messages thereafter. Continuity even if the bootstrap token leaks.

### Metrics

Tracked: [#34](https://github.com/paulmooreparks/tela/issues/34) — hub `/metrics` endpoint (Prometheus format by default, JSON alternative via `Accept` header) covering active sessions, bytes relayed, handshake failures, reconnect rates, transport-tier distribution, and admin API call counts.

---

## Polish (needed but not blockers)

### Cross-platform validation

Tracked: [#35](https://github.com/paulmooreparks/tela/issues/35) — TelaVisor on macOS and Linux (afternoon each), plus BSD decision.

### TelaVisor UX

Tracked: [#36](https://github.com/paulmooreparks/tela/issues/36) — update-button confirm dialog, QR code for pair flow, retake outdated screenshot.

### Awan Saya UX

Tracked: [#37](https://github.com/paulmooreparks/tela/issues/37) — mobile UX pass, demo banner distinctiveness.

### CLI polish

Tracked: [#38](https://github.com/paulmooreparks/tela/issues/38) — help text consistency, standard `-v`/`--verbose`, documented exit codes. Note: help-flag harmonization (`-h`, `-?`, `-help`, `--help` at every subcommand) already landed in commit 4e78fef.

### Per-service post-connect hooks

- [x] Decision (2026-07-11): **not in 1.0.** Wrapper scripting around `tela connect` is the documented pattern, and the `hooks:` namespace under each service in the profile YAML is reserved now (see CONFIGURATION.md) so the feature can land in a 1.x cycle without a schema migration. If it ever ships, all three guardrails below land in the same commit. Tracked as [#39](https://github.com/paulmooreparks/tela/issues/39), closed by this writeup. The analysis below is retained as the design record.
- **The idea.** Each service entry in a connection profile gets an optional `hooks:` list. Each hook specifies a `when` (`connected` / `disconnected` / `error`), a `run` mode (`once-per-session` / `once-per-process` / `on-every-connect`), an optional `detach: true` for long-running processes, an optional `timeout`, and a `command:` array. When the listener for the service comes up (or goes down, or errors), tela runs the configured commands. Use cases: auto-launch a terminal/RDP/database client against the new local listener, run a health check against the tunneled service to confirm end-to-end reachability, send a desktop notification, kick off provisioning scripts.
- **Why it might be worth doing.** The thing every user does *after* a successful connection is a manual ritual: open a terminal and run `ssh localhost -p 10022`, fire up `mstsc /v:localhost:13389`, point a database GUI at `localhost:15432`. Profiles already capture the connection shape but stop at "the listener is up." A post-connect hook closes the loop. The architectural fit is clean: profiles already drive `tela connect`, the reconnect loop already knows when a session transitions to "connected", and the schema change is one new field on the existing service block.
- **Why it might not be worth doing.** Everything in the previous paragraph can be solved by scripting outside tela today: write a wrapper shell script, run `tela connect -profile ... &`, wait for the listener (poll, or sleep), then run your post-connect commands. The feature buys ergonomics, not capability. The cost is non-trivial because executing arbitrary user-supplied commands is a category of feature with sharp edges (see below).
- **The sharp edges that make this risky if done naively.** Most network tools that ship `PostUp`/`PostDown`-style hooks (Tailscale, OpenVPN, autossh, wg-quick) generate the same class of bug: hooks run when the user did not expect them, hooks run again on reconnect when they shouldn't, hooks hang and block teardown, hooks fail silently with no UI surface, hooks receive untrusted input from the wire and become a command-injection vector, and profile import becomes equivalent to running an attacker-supplied script.
- **The three guardrails this would need on day one.** If we ever do build it:
  1. **Off by default per profile, opt-in via explicit trust.** A profile loaded from disk MUST NOT execute hooks unless the user has run `tela profile trust <name>` after reviewing the hook commands. Imported profiles arrive untrusted. Trust state is sha256-keyed on the hook block so editing the profile invalidates trust. `tela connect -no-hooks` disables hooks for one run regardless. This kills the "I imported a profile and it ran a script" attack.
  2. **Argv mode, not shell strings.** `command: ["wt.exe", "ssh", "-p", "${TELA_LOCAL_PORT}", "user@localhost"]`, never `command: "wt.exe ssh -p ${TELA_LOCAL_PORT} user@localhost"`. Variable expansion is a literal substitute on argv elements only, not a shell parse. No `$()`, no backticks, no word splitting. This kills the entire shell-injection attack surface in one rule.
  3. **Documented run semantics with hard timeouts.** Default run mode is `once-per-session` (fires when the listener first comes up after a connect, does not re-fire on reconnect). Foreground hooks have a 30-second default timeout; tela kills them on timeout and logs a clear error. Long-running hooks must declare `detach: true` explicitly. Detached hooks survive teardown; foreground hooks are killed on teardown. Every hook execution gets a log line with start/exit/duration so users can debug.
- **What it should NOT be allowed to do.** Run as root or with elevated privileges (the hook runs as the user who ran `tela`, period). Receive untrusted input from the wire that becomes part of a command (machine name, hub name, etc. are passed via env vars, not interpolated into argv elements that bypass the literal-substitute rule). Modify the profile YAML on disk. Talk to the hub on its own (if a hook needs hub access, it goes through `tela admin` like everything else).
- **Where the field belongs.** Client side, in the connection profile YAML, under each service. NOT on the agent side -- the agent does not know what the client wants to do after the tunnel is up.
- **Decision.** Not committed for 1.0. If it ships at all it ships as a polish feature after the bigger blockers, with all three guardrails in the same commit. If it does not ship, document explicitly that scripting around `tela connect` is the recommended pattern. Either way, reserve the `hooks:` namespace in the profile schema design now so the field is available without a schema migration later.

### Installers

Tracked: [#40](https://github.com/paulmooreparks/tela/issues/40) — refined Windows installer, notarized macOS `.dmg`/`.pkg`, Linux `.AppImage` (alongside the already-built `.deb`/`.rpm`), and package-manager distribution via Homebrew, Chocolatey, winget, and apt.

---

## Documentation gaps

### User-facing docs

- [#41](https://github.com/paulmooreparks/tela/issues/41) — **Troubleshooting guide**: "tela won't connect, what now?" The doc users need most after their first failure.
- [#42](https://github.com/paulmooreparks/tela/issues/42) — **Security model document**: threat model, what Tela protects against, what it does not. Essential for a security tool.
- [#43](https://github.com/paulmooreparks/tela/issues/43) — **Deployment hardening guide**: safely deploying a hub on the public internet.
- [#44](https://github.com/paulmooreparks/tela/issues/44) — **Upgrade and migration guide**: per-release "what's new / what to watch out for" doc.
- [#45](https://github.com/paulmooreparks/tela/issues/45) — **Fleet operations playbook**: the operational practices that distinguish "I have Tela installed" from "I run Tela for a team."

### Process docs

Already done:
- [x] CHANGELOG.md with semver discipline (kept up to date per release)
- [x] CONTRIBUTING.md (how to build, how to test, how to submit a PR)

Open:
- [#46](https://github.com/paulmooreparks/tela/issues/46) — SECURITY.md (responsible disclosure policy, supported versions, contact)

Also added since this roadmap was first written:
- [x] DEVELOPMENT.md — internal development workflow, branching model, custom-channel dogfooding recipe, label taxonomy, local-tag conventions (see commit history)

### Third-party integration

- [#47](https://github.com/paulmooreparks/tela/issues/47) — **Writing a portal**: contract spec for `/.well-known/tela`, `/api/hubs`, admin proxy patterns, so someone could build a competing or specialized portal without reading Awan Saya source.
- [#48](https://github.com/paulmooreparks/tela/issues/48) — **Embedding Tela**: per the Go API decision (#22, Option B), this issue will document explicitly that there is no stable Go embedding story at 1.0; embedders use the CLI, wire protocol, or fork.

---

## Suggested execution order

This is the order I would do things in if I were running the project, biased toward unblocking everything else first and leaving polish for the end. Status as of 2026-07-11: steps 1 through 7 and 12 are done; the remaining work is steps 8 through 11 plus the launch surface.

1. **CI and automated releases**: done. See "Release engineering" above.
2. **Test harness for end-to-end scenarios**: done in substance (`internal/teststack`); leak-detection and adoption residuals ride the test wave.
3. **Tests for the auth and access paths**: done. The security-boundary suites (#7, #8) and the rest of the test wave (#9 through #12) landed, and #13 wired the CI coverage gate (`tools/covcheck`) that keeps the security-critical packages from eroding.
4. **Per-service access control**: done, including the fixed client-side guard.
5. **Cert pinning**: done, scoped per DESIGN-cert-pinning-portal.md.
6. **Relay gateway design and v1 wire format alignment**: done; shipped in the 0.6 cycle with the TTL field in the frozen frame.
7. **Protocol freeze with version field and v1 spec**: done. DESIGN.md section 6 is the frozen reference.
8. **Code signing for Windows and macOS**: next. Certificate procurement is the long pole; start it before anything else in this list.
9. **Security model document and troubleshooting guide**: the two most important user-facing docs still missing.
10. **Relay gateway book chapter**: the last item holding #26 open; docs work, parallel with everything.
11. **Then the rest**: audit retention, structured logging, metrics, rate limiting, agent identity, installers, package managers, UX polish, and the telaproject.org launch site.
12. **Make and publish the scope decisions**: done 2026-07-11. Routed mesh, hub federation, SSO/OIDC, kernel TUN, multi-tenant hub, portal architecture, in-browser fallback, post-connect hooks, and local names all carry written decisions in the scope-decisions section above. This was the step that converts ambient ambiguity into a permanent commitment, and it landed before the tag.

---

## How to use this document

This roadmap holds the narrative and design rationale. [Milestone `1.0`](https://github.com/paulmooreparks/tela/milestone/2) holds the state.

- When a section describes work, each piece that ships through a PR gets a GitHub Issue. State lives there (open/closed, assignees, PR linkage), not in this doc.
- When a section describes a decision, the decision writeup happens in an issue, and the prose here captures the reasoning. If the decision changes, update the prose; the old reasoning is preserved via git history.
- Add new narrative sections as the scope evolves. Do not delete rationale that turns out to be wrong — strike it through and explain why.
- The `1.0` milestone should be empty (no open issues) before tagging `v1.0.0`.
- The "Important" and "Polish" sections can carry issues past 1.0 if they are not security-critical; move their milestone to `1.1` or `2.0` rather than closing without completing.
- When all blockers are closed, raise the tag, write the release notes, and start treating backward compatibility as sacred.

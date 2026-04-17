# Tela 1.0 Readiness

This is the living document tracking what is required to ship Tela 1.0. It is not a feature roadmap (see [TODO.md](TODO.md) for that) — it is a hardening, polish, and operational-readiness checklist.

The premise: once we ship 1.0, we will maintain backward compatibility religiously. That means any cruft, any half-finished surface, any unversioned protocol field becomes a permanent maintenance burden. The goal of this document is to make sure that what gets locked in at 1.0 is what we actually want to live with.

**Status as of 2026-04-17:** Roughly **0.6 — 0.7** of a 1.0 release. The hard architectural work is mostly done. The testable, shippable, signable, supportable infrastructure around it is mostly not done.

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

Tela has solid architecture but almost no automated test coverage. The security-critical paths (auth store, admin API, wsbind transport, WG reconnect) need tests before 1.0 locks the access model and wire protocol.

Already done:
- [x] `permuteArgs` flag reordering tests (`cmd/tela/admin_test.go`, 16 sub-tests)
- [x] `internal/channel` tests: manifest parsing/validation, URL helpers, Fetcher cache + stale-on-failure, VerifyReader (94.4% coverage)
- [x] `internal/credstore` tests: round-trip, normalization, permission bits, edge cases (62.7% coverage)

Open issues:
- [#6](https://github.com/paulmooreparks/tela/issues/6) — Test harness: in-process hub + agent + client end-to-end
- [#7](https://github.com/paulmooreparks/tela/issues/7) — Auth store unit tests
- [#8](https://github.com/paulmooreparks/tela/issues/8) — Admin API endpoint tests
- [#9](https://github.com/paulmooreparks/tela/issues/9) — Ring buffer history tests
- [#10](https://github.com/paulmooreparks/tela/issues/10) — Portal registration and sync-token flow tests
- [#11](https://github.com/paulmooreparks/tela/issues/11) — WireGuard handshake-on-reconnect end-to-end test
- [#12](https://github.com/paulmooreparks/tela/issues/12) — wsbind transport test (WS, UDP relay, direct UDP)
- [#13](https://github.com/paulmooreparks/tela/issues/13) — Coverage gate (release criterion; tracks the others)

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

### Protocol freeze

The wire protocol is currently unversioned and the Go package surface has not been deliberated. Once 1.0 ships, both get locked in for the entire 1.x line. Two forcing-function decisions have to land before protocol freeze, not after.

**Channel multiplexing: decision made — single-channel in v1.** The mux experiment on `main` (commits efeafac → 14a731f) was reverted after dogfood issues. V1 ships single-channel; multiplexing is deferred to a hypothetical v2 if ever. Writeup tracked as an issue.

**Public Go API surface: decision made — Option B, everything stays `internal/`.** The 1.x compat promise covers CLI flags, config YAML, wire protocol, REST API, and channel manifest schema. It does NOT cover Go `internal/` packages. Documentation writeup tracked as an issue.

Open issues:
- [#18](https://github.com/paulmooreparks/tela/issues/18) — Add `protocolVersion` field to register and connect control messages (forcing function, priority:high)
- [#19](https://github.com/paulmooreparks/tela/issues/19) — Document v1 wire format as the frozen specification (forcing function, priority:high)
- [#20](https://github.com/paulmooreparks/tela/issues/20) — Document backward-compatibility policy for 1.x
- [#21](https://github.com/paulmooreparks/tela/issues/21) — Document channel multiplexing as non-goal for v1
- [#22](https://github.com/paulmooreparks/tela/issues/22) — Document the "no public Go API at 1.0" decision

### Cert pinning

Today `tela` and TelaVisor trust whatever TLS certificate the hub presents. Pinning closes the MITM class of attack by tying a remote entry to a specific certificate fingerprint, with TOFU on first connect and explicit confirmation on change.

Tracked as a single bundled issue with full scope: [#23](https://github.com/paulmooreparks/tela/issues/23).

### Session tokens

Shared-secret tokens today carry no identity claim, no expiry, no scope, and can only be revoked by editing YAML and restarting. For 1.0: structured tokens (identity, role, iat, exp, scope), centralized revocation checked per request, rotation without config edits, and clean migration from the existing format.

Tracked as a single bundled issue: [#24](https://github.com/paulmooreparks/tela/issues/24).

### Audit log retention

The hub's event history is an in-memory ring buffer with undocumented retention, no persistence across restart, and no way to ship events to an external aggregator. For 1.0: documented retention story, optional on-disk persistence, pluggable log-shipping, and a compliance-reviewer-friendly writeup of what is and is not in the log.

Tracked as a single bundled issue: [#25](https://github.com/paulmooreparks/tela/issues/25).

---

## Scope decisions for 1.0

These are the four ambitions a reader can reasonably project onto Tela that the project has not committed to in writing. Each one needs a written decision before 1.0, because the difference between "deferred" and "non-goal" is the difference between a healthy roadmap and a permanent expectations gap. The premise of this document is that whatever ships at 1.0 becomes a permanent maintenance burden; the same applies to whatever Tela promises *not* to be.

For each item: **what it is, why it is or is not in 1.0, and what the post-1.0 path looks like if it is deferred.**

### Routed mesh networking
- [x] Decision: **non-goal for 1.0.** Post-1.0 only if real-world utility is demonstrated against the existing transport upgrade cascade.
- Tela has direct peer-to-peer as a transport tier, but it is not a routed mesh in the Tailscale or Nebula sense. Most traffic still travels client to hub to agent. The [introduction](book/src/introduction.md) and the [glossary](book/src/glossary.md) explicitly disclaim mesh routing under the leaf-spine framing.
- The current lean is non-goal. The use cases that make Tailscale's mesh worth its complexity (peer-to-peer LAN performance, NAT traversal independence) are mostly already covered by Tela's existing WS -> UDP relay -> direct P2P fallback. The use cases that mesh would *additionally* unlock (agent-to-agent routing without a hub on the data path, multi-hop topologies built out of agents) need to be demonstrated against real demand before the design work is justified.
- **Distinct from the relay gateway, which is in scope for 1.0** (see *Important: Relay gateway* below). The relay gateway moves traffic between hubs while preserving the spine-and-leaves shape and the blind-relay property end to end. Mesh moves traffic between agents without a hub on the data path. They are not the same feature and the mesh decision does not constrain the relay gateway design.
- If post-1.0 work on mesh is ever picked up: sketch what it would take (routing table, multi-hop key exchange, NAT traversal beyond the current STUN cascade) and what `protocolVersion` would carry it.

### Hub federation

Tracked: [#49](https://github.com/paulmooreparks/tela/issues/49) — decision pending (deferred to 1.x, or non-goal forever).
- Today, identity is hub-scoped. A user with a token on hub A has no relationship to hub B unless they hold a separate token there. The portal layer (Awan Saya) papers over this for end users but does not fix it at the protocol level.
- If deferred: define what federation means at the protocol level (cross-hub trust, identity assertion format, revocation propagation) and what it would do to the access model.
- If non-goal: document that federation is the portal's job, not the hub's, and that operators who need it should run a portal.

### Single sign-on (SSO/OIDC/SAML)

Tracked: [#50](https://github.com/paulmooreparks/tela/issues/50) — decision pending (deferred to 1.x, or non-goal forever).
- The hub today uses bearer tokens issued by an admin. There is no integration with external identity providers (OpenID Connect, Security Assertion Markup Language, Lightweight Directory Access Protocol). The team-cloud and fleet tiers in the introduction lean on this; a real organization will not provision users by hand.
- If deferred: pick one identity protocol to support first (OIDC is the obvious candidate), and document the contract (which claims map to which roles, how revocation works, how the hub validates tokens).
- If non-goal: document that SSO is the portal's job and the hub will only ever speak its own token format, and explain why.

### Kernel TUN mode (exit node, full IP routing)

Tracked: [#51](https://github.com/paulmooreparks/tela/issues/51) — decision leaning "deferred post-1.0 with a sketched plan"; formal writeup still owed.
- Tela currently runs WireGuard entirely in userspace via gVisor netstack. This is the property that eliminates the need for admin rights, kernel drivers, and TUN devices. It also limits Tela to TCP-only tunneling with no OS-level network interface.
- An optional kernel TUN mode would use wireguard-go's `tun.CreateTUN()` instead of `netstack.CreateNetTUN()`. The wireguard-go library already ships both constructors. The result: a real network interface, full IP routing (UDP, ICMP, multicast), exit-node support (route all system traffic through a remote machine), and kernel-speed packet processing. The cost: one-time admin/root elevation to create the TUN device.
- **The two-mode model.** Userspace mode (default, no admin, TCP only) and kernel mode (opt-in, elevation required, full IP). Same hub, same agent protocol, same access model, same management tools. A fleet could mix both modes. A user could run userspace on a locked-down corporate laptop and kernel mode on a home machine.
- **Why this matters.** The combination of both modes covers two populations that no single tool covers today: users who cannot install Tailscale on their corporate laptop (userspace mode), and users who want Tailscale's capabilities but self-hosted with their own hub and management UI (kernel mode).
- **Implementation scope.** A `-tun` flag on `tela` and `telad` that switches the constructor. The real work is platform-specific route table management, DNS interception for exit-node mode, elevation prompts, and graceful TUN interface cleanup on exit.
- **Android client.** An Android client would likely use kernel mode by default (Android's VpnService API provides a TUN interface without root). This is a natural extension of the same two-mode model.
- If deferred past 1.x: document explicitly that Tela is TCP-only by design and that the userspace tradeoff is intentional, with a pointer to this roadmap item for the kernel-mode plan.

### Multi-tenant hub

Tracked: [#52](https://github.com/paulmooreparks/tela/issues/52) — decision pending (deferred to 1.x, or non-goal forever).
- Today the answer to "I have multiple teams" is "run multiple hubs." This is fine at small scale and starts to bite at the fleet tier. A multi-tenant hub would let one process serve multiple isolated organizations with separate identity, ACL, and history surfaces.
- If deferred: define what isolation means (config, storage, audit log, admin API) and how operators would migrate from "one hub per team" to "tenants on a shared hub."
- If non-goal: document that the project's recommended scaling pattern is hub-per-tenant, and that the portal layer is what stitches them together for end users.

### Portal architecture: one protocol, many hosts

Tracked: [#53](https://github.com/paulmooreparks/tela/issues/53) (priority:high) — decision pending; first deliverable if pursued is the portal protocol spec ([#47](https://github.com/paulmooreparks/tela/issues/47)).
- **The question.** Today the portal exists as one thing: Awan Saya, a Node.js + PostgreSQL web app. There is no portal protocol spec, no Go portal package, and no way for TelaVisor to "be" a portal for personal use. A natural-feeling user request is "let TelaVisor host the portal so I have one-stop shopping that scales from solo to enterprise without two implementations." The naive answer (embed Awan Saya in TelaVisor) is wrong. The right answer requires extracting a portal protocol and a portal service from Awan Saya first.
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
- **What this depends on.** This decision interacts with **Hub federation** and **Multi-tenant hub** above. If federation is non-goal, the portal becomes the only place cross-hub identity is reconciled, which raises the stakes for the protocol spec. If multi-tenant hub is deferred, the portal-stitching-many-hubs pattern remains the recommended scaling story for at least 1.x.
- **The first concrete step when we come back.** Write the portal protocol spec doc. Not code. The spec is what prevents the two-implementation drift. Once it exists, the extraction work is mechanical.

---

## Important (seriously hurts 1.0 if not done)

### Relay gateway (hub-to-hub transit)

Tracked: [#26](https://github.com/paulmooreparks/tela/issues/26) (priority:high) — wire-format forcing function via the TTL hop count field.

The generalization of Tela's existing gateway primitive to the relay layer. A hub forwards opaque WireGuard ciphertext on behalf of a client whose target agent is registered with a *different* hub. The WireGuard handshake remains end to end between the original client and the destination agent. The bridging hub is blind to the payload, the same way the destination hub is blind to the payload today. This is the missing rung of the gateway ladder Tela already implements at every other layer of the stack: path gateway (Layer 7), bridge gateway (Layer 4 inbound), upstream gateway (Layer 4 outbound), single-hop relay gateway (Layer 3, today's hub), multi-hop relay gateway (Layer 3, *this item*).

Why this is a 1.0 deliverable rather than post-1.0 work: the architectural shape is forced now. Once the v1 wire format ships without any hop-count or routing-aware field, the relay gateway becomes harder to add later without a v2 wire bump. Forcing the design through now keeps the protocol freeze honest. The feature also unlocks the multi-customer managed-service-provider, air-gap traversal, geographic-distribution, and acquisition-merger scenarios that the introduction's *fleet* tier implicitly promises but that no other feature in the fabric actually delivers. It is the strongest single answer to "how does Tela scale beyond one hub" the project has.

What it explicitly does not include:

- **Routing protocols.** The bridging hub knows about the destination hub via the directory protocol. It does not learn topology dynamically. No Border Gateway Protocol (BGP), no spanning tree, no link-state algorithm.
- **Identity federation.** A user with a token on Hub A does not automatically have a token on Hub B. Hub A's bridging token is its own credential. The four federation-shaped scope decisions above are unaffected.
- **Mesh agent-to-agent routing.** The data path is still client to hub (to hub) to agent. See the **Routed mesh networking** scope decision above for why mesh is not in scope and why the relay gateway is not the same thing.

### In-browser fallback

Tracked: [#28](https://github.com/paulmooreparks/tela/issues/28) (priority:low) — decide ship-or-remove. Suggested default: document removal.

### Structured logging

Tracked: [#29](https://github.com/paulmooreparks/tela/issues/29) — migrate from `log.Printf` to `log/slog`, with `telelog` becoming a `slog.Handler`.

### Operational fixes

- [#30](https://github.com/paulmooreparks/tela/issues/30) — Graceful HTTP shutdown (`server.Shutdown(ctx)` with drain timeout)
- [#31](https://github.com/paulmooreparks/tela/issues/31) — Rate limiting on the admin API (token bucket per identity per endpoint)
- [#32](https://github.com/paulmooreparks/tela/issues/32) — Hot reload for non-auth config, or documented restart requirement

### Per-service access control

Tracked: [#27](https://github.com/paulmooreparks/tela/issues/27) (priority:high) — access-model schema-freeze forcing function.

Today the `connect` permission is all-or-nothing at the machine level. If alice has `connect` on `barn`, she sees every port the agent exposes -- SSH, RDP, Jellyfin, a local admin panel, everything. There is no way to say "alice can reach Jellyfin but not SSH." The workaround (register the same physical machine under multiple names with different port sets) works but is ugly and fragile.

This needs to be fixed before 1.0 because the access model is getting frozen. Adding service-scoped grants after 1.0 is an API and config schema change that has to be backward compatible -- much harder than doing it now. The model adds an optional `services:` filter to `connect` grants; no filter means all services (backward compatible), a filter restricts the client to the named services at session setup time. Filtering is by service name, not port; enforced on the hub, not the agent. Design detail and scope in the issue.

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

Tracked: [#39](https://github.com/paulmooreparks/tela/issues/39) (priority:low) — decision item, not a commitment. Captured here so the schema design can reserve the `hooks:` namespace under each service in the profile YAML even if the feature does not ship for 1.0.
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

This is the order I would do things in if I were running the project, biased toward unblocking everything else first and leaving polish for the end.

1. **CI and automated releases** — without this, every other improvement is fragile
2. **Test harness for end-to-end scenarios** — unlocks confident refactoring of everything else
3. **Tests for the auth and access paths** — these are the security boundary
4. **Per-service access control** — access model schema change; must land before the model freezes at 1.0, not after
5. **Cert pinning** — the trust anchor that everything else relies on
6. **Relay gateway design and v1 wire format alignment** — hub-to-hub transit is the defining feature for the *fleet* tier and the only 1.0 item that forces specific bits into the wire format (the TTL field). Design must be done before the protocol freeze, not after, or it has to wait for v2.
7. **Protocol freeze with version field and v1 spec** — once locked, you can iterate freely
8. **Code signing for Windows and macOS** — without this, downloads are scary
9. **Security model document and troubleshooting guide** — the two most important user-facing docs you don't have yet
10. **Relay gateway implementation** — once the wire format and design are locked in step 6, the implementation work (hub outbound mode, directory schema, audit log entries, documentation chapter) can run in parallel with the polish steps
11. **Then start polishing** — mobile UX, installers, package managers, structured logging, metrics, rate limiting, graceful shutdown, agent identity
12. **Make and publish the scope decisions** — routed mesh, hub federation, SSO/OIDC, multi-tenant hub. Deferred or rejected, but written down. This is the step that converts ambient ambiguity into a permanent commitment, and it has to happen before the tag, not after, or the decisions get made by accident in the first issue thread that asks for one of them.

---

## How to use this document

This roadmap holds the narrative and design rationale. [Milestone `1.0`](https://github.com/paulmooreparks/tela/milestone/2) holds the state.

- When a section describes work, each piece that ships through a PR gets a GitHub Issue. State lives there (open/closed, assignees, PR linkage), not in this doc.
- When a section describes a decision, the decision writeup happens in an issue, and the prose here captures the reasoning. If the decision changes, update the prose; the old reasoning is preserved via git history.
- Add new narrative sections as the scope evolves. Do not delete rationale that turns out to be wrong — strike it through and explain why.
- The `1.0` milestone should be empty (no open issues) before tagging `v1.0.0`.
- The "Important" and "Polish" sections can carry issues past 1.0 if they are not security-critical; move their milestone to `1.1` or `2.0` rather than closing without completing.
- When all blockers are closed, raise the tag, write the release notes, and start treating backward compatibility as sacred.

# Tela 1.0 Readiness

This is the living document tracking what is required to ship Tela 1.0. It is not a feature roadmap (see [TODO.md](TODO.md) for that) — it is a hardening, polish, and operational-readiness checklist.

The premise: once we ship 1.0, we will maintain backward compatibility religiously. That means any cruft, any half-finished surface, any unversioned protocol field becomes a permanent maintenance burden. The goal of this document is to make sure that what gets locked in at 1.0 is what we actually want to live with.

**Status as of 2026-04-07:** Roughly **0.6 — 0.7** of a 1.0 release. The hard architectural work is mostly done. The testable, shippable, signable, supportable infrastructure around it is mostly not done.

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
- [ ] Set up a test infrastructure that can spin up a hub in-process, register an agent, connect a client, push bytes through, and tear down cleanly
- [ ] Auth store unit tests: `canRegister`, `canConnect`, `canViewMachine`, `canManage` edge cases, including wildcard ACL behavior
- [ ] Admin API endpoint tests: token CRUD, access PUT/DELETE, agent management proxy, hub log retrieval, hub update flow
- [ ] Ring buffer tests: wrap-around, snapshot ordering, concurrent writes
- [x] `permuteArgs` flag reordering tests (`cmd/tela/admin_test.go`, 16 sub-tests)
- [x] `internal/channel` tests: manifest parsing/validation, URL helpers, Fetcher cache + stale-on-failure, VerifyReader (94.4% coverage)
- [x] `internal/credstore` tests: round-trip, normalization, permission bits, edge cases (62.7% coverage)
- [ ] Portal registration and sync token flow tests
- [ ] WireGuard handshake-on-reconnect end-to-end test
- [ ] wsbind transport test (WS, UDP relay, direct UDP)
- [ ] Coverage gate: at least the security-critical paths must be tested before 1.0. Aim for meaningful coverage of `internal/auth`, `internal/wsbind`, and the admin API surface.

### Release engineering
- [x] GitHub Actions workflow that builds Linux/macOS/Windows binaries for amd64 and arm64 on tag push (`release.yml`)
- [x] Reproducible builds (pinned Go version, `-trimpath`, deterministic ldflags)
- [x] Checksum file (`SHA256SUMS.txt`) generated and published per release
- [x] Release notes generated from commit messages (`generate_release_notes: true`)
- [x] Versioned binary naming convention enforced (`{tool}-{goos}-{goarch}{ext}`) so the existing self-update code keeps working
- [x] CI workflow runs build, vet, test, gofmt, and `go mod tidy` checks on every push and PR (`ci.yml`)
- [x] Cross-compile sanity check across all 6 target triples on every push
- [x] Tag schedule and semver discipline established. Three release channels (dev / beta / stable) with documented promotion model. See [RELEASE-PROCESS.md](RELEASE-PROCESS.md).
- [x] Channel manifests (`dev.json`, `beta.json`, `stable.json`) hosted on a rolling `channels` GitHub Release. Every binary follows its configured channel via `internal/channel` and verifies SHA-256 against the manifest before installing.
- [x] Promotion workflow (`promote.yml`) wired to `release.yml` via `workflow_call` so promoted tags actually build (the GITHUB_TOKEN tag-push restriction is sidestepped).
- [x] Tag race fix: a `compute-version` job at the top of `release.yml` reserves the dev counter atomically against the GitHub API, eliminating the silent force-overwrite class of bug.
- [x] Self-update CLI on every binary: `tela update`, `telad update`, `telahubd update`. All accept `-channel` and `-dry-run`.
- [x] Self-update API on telahubd: `GET /api/admin/update` (status), `PATCH /api/admin/update` (set channel), `POST /api/admin/update` (trigger). Same shape mirrored on the agent management proxy via `update-status` and `update-channel` mgmt actions.
- [x] Channel selectors in TelaVisor (Hub Settings, Agent Settings, Application Settings) and Awan Saya (hub and fleet management cards).

### Code signing
- [ ] Authenticode certificate for Windows binaries (telavisor.exe, telad.exe, tela.exe, telahubd.exe)
- [ ] Apple Developer ID for macOS binaries, plus notarization for the TelaVisor app bundle
- [ ] Document the signing process so it can be reproduced from a clean machine
- [ ] CI integration so signing happens automatically on tag push

### Protocol freeze
- [ ] Add a `protocolVersion` field to the initial control message (`register` and `connect`)
- [ ] Document the v1 wire format in `DESIGN.md` as the frozen specification
- [ ] Hub rejects connections with an unknown major version with a clear error message
- [ ] Old client / new hub negotiation: client gets a structured error, not a silent disconnect
- [ ] Backward-compat policy documented: minor/patch versions must remain wire-compatible after 1.0
- [ ] **Channel multiplexing decision baked into v1.** Either ship the 12-byte channel-ID frame header from DESIGN.md §6.3 in v1 (one WebSocket per machine, multiplexed channels for sessions and control), or document explicitly that v1 is single-channel and multiplexing is deferred to a `protocolVersion = 2` upgrade path. This is wire format. It cannot be revisited after 1.0 without a major version bump, so it has to be decided before the freeze.
- [ ] **Public Go API surface decision.** Either promote a stable subset out of `internal/` into a public package (candidates: a subset of `internal/channel`, `internal/credstore`, and the wire protocol types), or document explicitly that there is no stable Go API at 1.0 and embedders must use the wire protocol. Whichever path you choose, write it down. The default of "everything is `internal/`" is itself a permanent decision once 1.0 ships.

### Cert pinning
- [ ] Optional cert pinning in the credential store and `hubs.yaml` (pin the hub's TLS leaf or root SHA256)
- [ ] First-connect TOFU (trust on first use) with a visible fingerprint shown to the user
- [ ] Cert change detection on subsequent connects, with explicit user confirmation required to accept
- [ ] CLI flag and `tela remote add` flow that supports specifying a pin at the time of remote registration
- [ ] TelaVisor surfaces the pinned fingerprint in the hub settings view

### Session tokens
- [ ] Replace static shared-secret tokens with structured tokens that carry: identity, role, issued-at, expires-at, optional scope
- [ ] Centralized revocation: hub maintains a revocation list, checked on every authenticated request
- [ ] Token rotation flow that does not require a hub config edit
- [ ] Backward-compat: existing static tokens migrate cleanly to the new format on first hub start

### Audit log retention
- [ ] Concrete retention story for the hub history ring buffer: documented default size, documented behavior on overflow, documented behavior across restarts
- [ ] Optional on-disk persistence so events survive a hub restart, with a configuration knob to enable it
- [ ] Optional log-shipping hook (webhook, syslog, or file with rotation) so operators can ship the audit stream to external aggregators
- [ ] Document what is in the audit log and what is not, so a compliance reviewer can answer the retention question without reading source

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
- [ ] Decision: deferred to 1.x, or non-goal forever.
- Today, identity is hub-scoped. A user with a token on hub A has no relationship to hub B unless they hold a separate token there. The portal layer (Awan Saya) papers over this for end users but does not fix it at the protocol level.
- If deferred: define what federation means at the protocol level (cross-hub trust, identity assertion format, revocation propagation) and what it would do to the access model.
- If non-goal: document that federation is the portal's job, not the hub's, and that operators who need it should run a portal.

### Single sign-on (SSO/OIDC/SAML)
- [ ] Decision: deferred to 1.x, or non-goal forever.
- The hub today uses bearer tokens issued by an admin. There is no integration with external identity providers (OpenID Connect, Security Assertion Markup Language, Lightweight Directory Access Protocol). The team-cloud and fleet tiers in the introduction lean on this; a real organization will not provision users by hand.
- If deferred: pick one identity protocol to support first (OIDC is the obvious candidate), and document the contract (which claims map to which roles, how revocation works, how the hub validates tokens).
- If non-goal: document that SSO is the portal's job and the hub will only ever speak its own token format, and explain why.

### Multi-tenant hub
- [ ] Decision: deferred to 1.x, or non-goal forever.
- Today the answer to "I have multiple teams" is "run multiple hubs." This is fine at small scale and starts to bite at the fleet tier. A multi-tenant hub would let one process serve multiple isolated organizations with separate identity, ACL, and history surfaces.
- If deferred: define what isolation means (config, storage, audit log, admin API) and how operators would migrate from "one hub per team" to "tenants on a shared hub."
- If non-goal: document that the project's recommended scaling pattern is hub-per-tenant, and that the portal layer is what stitches them together for end users.

### Portal architecture: one protocol, many hosts
- [ ] Decision needed before any portal-related work resumes.
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

The generalization of Tela's existing gateway primitive to the relay layer. A hub forwards opaque WireGuard ciphertext on behalf of a client whose target agent is registered with a *different* hub. The WireGuard handshake remains end to end between the original client and the destination agent. The bridging hub is blind to the payload, the same way the destination hub is blind to the payload today. This is the missing rung of the gateway ladder Tela already implements at every other layer of the stack: path gateway (Layer 7), bridge gateway (Layer 4 inbound), upstream gateway (Layer 4 outbound), single-hop relay gateway (Layer 3, today's hub), multi-hop relay gateway (Layer 3, *this item*).

Why this is a 1.0 deliverable rather than post-1.0 work: the architectural shape is forced now. Once the v1 wire format ships without any hop-count or routing-aware field, the relay gateway becomes harder to add later without a v2 wire bump. Forcing the design through now keeps the protocol freeze honest. The feature also unlocks the multi-customer managed-service-provider, air-gap traversal, geographic-distribution, and acquisition-merger scenarios that the introduction's *fleet* tier implicitly promises but that no other feature in the fabric actually delivers. It is the strongest single answer to "how does Tela scale beyond one hub" the project has.

What it requires:

- [ ] **Hub binary outbound mode.** `telahubd` learns to act as a client of another `telahubd`, dialing out and authenticating with a token, the same way `tela` does today. No new wire protocol; reuse the existing client-relay handshake.
- [ ] **Directory schema "reachable through" field.** A hub directory entry can carry an optional pointer naming the hub that actually hosts a given machine. Clients use this for resolution; bridging hubs use it to know where to forward sessions. Backward-compatible with directories that omit the field.
- [ ] **Session hop count (TTL).** A small field in the relay session header so that a hub receiving a session at TTL=0 refuses to bridge it. Loop prevention without a routing protocol. Must land in v1 of the wire format, not added later.
- [ ] **Authorization model: bridging hub holds a token on the destination hub.** No new permission category. The token has the existing `connect` scope for the relevant machines. The destination hub authorizes the bridging hub the same way it authorizes any client today.
- [ ] **Audit log entries on both sides.** The bridging hub logs *I forwarded a session for client C to hub Y.* The destination hub logs the session under the bridging hub's identity, the same way it logs any client session. Both are queryable through the existing audit endpoints. No cross-hub join required.
- [ ] **Documentation: a chapter (or chapter section) that teaches the gateway primitive as a family**, with the relay gateway as the new instance. Repurposes the existing path-based gateway documentation as one rung instead of the only one.

What it explicitly does not include:

- **Routing protocols.** The bridging hub knows about the destination hub via the directory protocol. It does not learn topology dynamically. No Border Gateway Protocol (BGP), no spanning tree, no link-state algorithm.
- **Identity federation.** A user with a token on Hub A does not automatically have a token on Hub B. Hub A's bridging token is its own credential. The four federation-shaped scope decisions above are unaffected.
- **Mesh agent-to-agent routing.** The data path is still client to hub (to hub) to agent. See the **Routed mesh networking** scope decision above for why mesh is not in scope and why the relay gateway is not the same thing.

### In-browser fallback
- [ ] Browser-based RDP/SSH client for cases where the user can't run binaries
- [ ] Or: explicitly remove this from the design and document the decision

### Structured logging
- [ ] Replace `log.Printf("[component] msg")` with `log/slog`
- [ ] `telelog` becomes a `slog.Handler` instead of an `io.Writer`
- [ ] Component, level, and structured fields are queryable by log aggregators

### Operational fixes
- [ ] Graceful HTTP shutdown: replace `server.Close()` with `server.Shutdown(ctx)` plus a configurable drain timeout
- [ ] Rate limiting on the admin API: token bucket per identity per endpoint
- [ ] Hot reload for non-auth config changes (port, UDP host, log level, gateway routes) — or document the restart requirement explicitly

### Agent identity
- [ ] Each agent generates an Ed25519 keypair on first start and stores it locally
- [ ] Hub stores the agent's public key on first registration (TOFU)
- [ ] Agents sign registration messages; hub verifies signatures before granting register permission
- [ ] Document the rotation flow for stolen-key recovery

### Metrics
- [ ] Hub `/metrics` endpoint (Prometheus format, or at minimum a JSON stats endpoint)
- [ ] Track: active sessions, bytes relayed, handshake failures, reconnect rates, transport tier (WS / UDP relay / direct), admin API call counts
- [ ] Document the metrics so operators can build dashboards

---

## Polish (needed but not blockers)

### Cross-platform validation
- [ ] TelaVisor exercised on macOS (Wails native build) and Linux (GTK/webkit2gtk) for at least an afternoon each, with rough edges captured and fixed
- [ ] CLI tools tested on FreeBSD if we want BSD as a tier-1 target

### TelaVisor UX
- [ ] Hub Update button gets a confirmation dialog with explicit "all sessions will drop" warning
- [ ] Pair code flow gets a TelaVisor-side QR code generator
- [ ] Retake `telavisor-update.png` (currently shows v0.3.71 era)

### Awan Saya UX
- [ ] Mobile UX pass: the stacked hub-picker + sub-nav dropdowns are functional but cramped. Consider consolidating or using a different interaction model on phones.
- [ ] Demo banner could be more visually distinct from production data

### CLI polish
- [ ] Help text consistency pass (some commands are verbose, others terse, tone varies)
- [ ] Standard `-v` / `--verbose` flag across all binaries
- [ ] Standard exit-code conventions documented and enforced

### Installers
- [ ] Windows MSI or NSIS installer for TelaVisor (Program Files, Start Menu entry, optional service install, URL handler)
- [ ] macOS .dmg or .pkg with notarization
- [ ] Linux .deb, .rpm, .AppImage
- [ ] Package manager distribution: Homebrew (macOS), Chocolatey (Windows), winget (Windows), apt (Debian/Ubuntu)

---

## Documentation gaps

### User-facing docs
- [ ] **Troubleshooting guide**: "tela won't connect, what now?" — checking outbound connectivity, hub status endpoint, credential store, firewalls, hub log, agent log. The doc users need most after their first failure.
- [ ] **Security model document**: "Tela's threat model is X, Tela protects against Y, Tela does NOT protect against Z." Essential for a security tool. Users need to know what they're getting and what they're not.
- [ ] **Deployment hardening guide**: how to deploy a hub safely on the public internet. TLS certs, log shipping, backup, monitoring, what to do if compromised.
- [ ] **Upgrade and migration guide**: per-release "what's new / what to watch out for" doc
- [ ] **Fleet operations playbook**: the operational practices that distinguish "I have Tela installed" from "I run Tela for a team." Token rotation at scale, agent onboarding workflows, hub log shipping, channel promotion playbooks, hub-down recovery, agent fleet upgrade strategy. Mostly draws on existing docs (release process, access model, troubleshooting) but has to be written down as one coherent narrative or the fleet tier of the introduction is aspirational, not real.

### Process docs
- [ ] CHANGELOG.md with semver discipline (kept up to date per release)
- [ ] CONTRIBUTING.md (how to build, how to test, how to submit a PR, code style)
- [ ] SECURITY.md (responsible disclosure policy, supported versions, contact email or PGP key)

### Third-party integration
- [ ] **Writing a portal**: contract spec for `/.well-known/tela`, `/api/hubs`, the optional admin proxy patterns, so someone could build a competing or specialized portal without reading Awan Saya source
- [ ] **Embedding Tela**: how to use the internal packages (or a stable subset) from another Go program

---

## Suggested execution order

This is the order I would do things in if I were running the project, biased toward unblocking everything else first and leaving polish for the end.

1. **CI and automated releases** — without this, every other improvement is fragile
2. **Test harness for end-to-end scenarios** — unlocks confident refactoring of everything else
3. **Tests for the auth and access paths** — these are the security boundary
4. **Cert pinning** — the trust anchor that everything else relies on
5. **Relay gateway design and v1 wire format alignment** — hub-to-hub transit is the defining feature for the *fleet* tier and the only 1.0 item that forces specific bits into the wire format (the TTL field). Design must be done before the protocol freeze, not after, or it has to wait for v2.
6. **Protocol freeze with version field and v1 spec** — once locked, you can iterate freely
7. **Code signing for Windows and macOS** — without this, downloads are scary
8. **Security model document and troubleshooting guide** — the two most important user-facing docs you don't have yet
9. **Relay gateway implementation** — once the wire format and design are locked in step 5, the implementation work (hub outbound mode, directory schema, audit log entries, documentation chapter) can run in parallel with the polish steps
10. **Then start polishing** — mobile UX, installers, package managers, structured logging, metrics, rate limiting, graceful shutdown, agent identity
11. **Make and publish the scope decisions** — routed mesh, hub federation, SSO/OIDC, multi-tenant hub. Deferred or rejected, but written down. This is the step that converts ambient ambiguity into a permanent commitment, and it has to happen before the tag, not after, or the decisions get made by accident in the first issue thread that asks for one of them.

---

## How to use this document

- This is a checklist. Tick items as they ship.
- Add new items as they are discovered. Do not delete items that turn out to be wrong — strike them through and explain why.
- Each blocker section should be empty (no unchecked boxes) before tagging 1.0.0.
- The "Important" and "Polish" sections can carry items past 1.0 if they are not security-critical.
- When all blockers are checked, raise the tag, write the release notes, and start treating backward compatibility as sacred.

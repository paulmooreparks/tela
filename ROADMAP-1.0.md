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

---

## Important (seriously hurts 1.0 if not done)

### Channel multiplexing
- [ ] Multiplex multiple logical channels over a single tunnel (one WS per machine, not per session)
- [ ] Frame format with channel IDs (the 12-byte header from DESIGN.md §6.3)
- [ ] In-tunnel control channel for management messages without spinning up a separate session

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
5. **Protocol freeze with version field and v1 spec** — once locked, you can iterate freely
6. **Code signing for Windows and macOS** — without this, downloads are scary
7. **Security model document and troubleshooting guide** — the two most important user-facing docs you don't have yet
8. **Then start polishing** — mobile UX, installers, package managers, structured logging, metrics, rate limiting, graceful shutdown, agent identity

---

## How to use this document

- This is a checklist. Tick items as they ship.
- Add new items as they are discovered. Do not delete items that turn out to be wrong — strike them through and explain why.
- Each blocker section should be empty (no unchecked boxes) before tagging 1.0.0.
- The "Important" and "Polish" sections can carry items past 1.0 if they are not security-critical.
- When all blockers are checked, raise the tag, write the release notes, and start treating backward compatibility as sacred.

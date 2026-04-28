# Changelog

All notable changes to Tela are documented in this file. The format
is based on [Keep a Changelog](https://keepachangelog.com/), and
this project adheres to [Semantic Versioning](https://semver.org/)
starting from 1.0.

Pre-1.0 releases use `MAJOR.MINOR.PATCH-channel.N` tags. The
changelog is organized by minor version (0.8, 0.9, ...) since
patch-level dev builds are too granular to list individually.

## [Unreleased]

### Added
- **TelaVisor surfaces session-token lifecycle on the Access tab.** The By identity rail renders a status pill next to the role chip: green-on-blue *expires <date>* for an upcoming expiry, red *expired* once that date has passed, red *revoked* once the entry has been revoked; revoked rows render with a strikethrough so the terminal state is unmistakable. The identity detail header gains a **Revoke...** button between Rotate token and Delete identity, plus a small lifecycle meta line under the token preview that shows *issued <date> · expires <date> · revoked <timestamp>* as applicable. Revoking opens a themed confirm modal (per the no-web-dialogs rule) that documents the audit-trail-preserving semantics and the rotate-to-re-enable path; the modal surfaces backend 409s (notably the last-owner refusal) inline. The Add Identity modal grows an optional **Expires** field that accepts the same shorthand the CLI accepts (`30d`, `4w`, `1y`, or RFC 3339); empty means no expiry. When the selected identity is revoked, the detail header swaps Rotate token for **Rotate token (re-enables)** in the primary-button style and disables the Revoke button so the operator does not have to guess the unrevoke verb. Closes the TelaVisor follow-up to #24.

- **Session tokens with revocation, expiry, and rotation metadata.** Each `tokenEntry` in `telahubd.yaml` now carries optional `issuedAt`, `expiresAt`, and `revokedAt` timestamps alongside the opaque token value. The hub's auth checks (`canRegister`, `canConnect`, `canManage`, `isOwnerOrAdmin`, `isViewer`, `identityID`) deny revoked or past-expiry tokens immediately; new entries (created via `tela admin tokens add` or pairing) get `issuedAt` at creation time, while pre-0.16 entries on disk keep `issuedAt` absent (a synthesized "loaded today" timestamp would be misleading; rotating such an entry populates the field with the rotation time, which is genuinely accurate). New `POST /api/admin/tokens/{id}/revoke` endpoint marks an identity revoked without deleting it, preserving the audit trail; the existing rotate endpoint now refreshes `issuedAt` and clears any prior `revokedAt` so rotation doubles as the unrevoke path. Last-owner guard on revoke (409 Conflict) matches the existing role-demotion guard. Idempotent second call on the same identity returns 200 with `status: already-revoked`. CLI: `tela admin tokens add -expires <RFC3339|30d|4w|1y>` records an expiry at issue time; `tela admin tokens revoke <id>` drives the new endpoint; `tela admin tokens list` gains STATUS / EXPIRES / ISSUED columns. Closes #24.

- **TLS certificate pinning extended to the remaining `tela` CLI hub-bound HTTP paths.** The `tela admin` REST calls (every `/api/admin/*` operation), `tela machines`/`tela services`/`tela status`'s `/api/status` fetch, and `tela pair`'s `/api/pair` POST now use the same pin-aware HTTP client introduced in v0.16. With a pin configured for the hub URL, mismatched certificates fail the request with the same `certpin.ErrMismatch` error the WebSocket dialer surfaces; with no pin configured, the captured fingerprint is logged on first use so the operator can pin it via `tela pin`. New shared `pinAwareHTTPClient` helper (alongside `pinAwareDialer`) and a `pinVerifier` factory consolidate the TLS-config plumbing so future hub-bound HTTP code only needs the helper, not its own boilerplate. Admin clients are cached per hub URL via `sync.Map` so successive `tela admin` calls share a warm TLS connection. Three new unit tests in `internal/client/pin_test.go` exercise the matching-pin / mismatched-pin / TOFU paths through `pinAwareHTTPClient` against an `httptest.NewTLSServer`. Closes the remaining "wire it through the rest of the HTTP clients" follow-up from #23.

- **TLS certificate pinning for hub connections.** `tela connect`'s WebSocket dial and the hub-to-hub bridge dial now consult an optional TLS certificate pin recorded per hub. The pin is the SHA-256 hash of the leaf certificate's Subject Public Key Info (SPKI), formatted as `sha256:<lowercase hex>`; pinning the SPKI rather than the whole certificate survives renewal-with-same-key, which is the desired behavior. With a pin configured, the dialer refuses connections whose presented certificate's SPKI does not match. With no pin configured (TOFU mode), the dialer succeeds and logs the captured fingerprint plus the exact `tela pin <hub-url> <fingerprint>` command to enforce it on next dial. New `internal/certpin` package implements `Capture`, `Parse`, `Normalize`, and `Verify`; new `tela pin <hub-url> [fingerprint]` subcommand inspects, sets, or clears the pin; new `-pin` flag on `tela login` records the pin at credential-creation time. Pin storage is per-hub under credstore (`~/.tela/credentials.yaml`), `hubs.yaml` (each hub entry), and `telahubd.yaml` (each `bridges[]` entry). The bridge dial uses the same `certpin` package so client and bridge dials share one implementation per DESIGN-relay-gateway.md §5.4. Closes part of #23 (the storage + dial-path mechanism + CLI surface; TelaVisor UI for pin management is a follow-up).
- **`hubs.yaml` schema gains a `pin` field.** Each hub entry is now a structured object `{ url:..., pin:... }` rather than a bare URL string. The pre-0.16 flat shape (`hubs: { name: url-string }`) is still accepted on read and migrated transparently for one release; new entries written by `tela login` use the structured shape so they can carry a pin. The legacy flat shape is removed at 1.0.

- **Protocol freeze track.** DESIGN.md section 6 has been rewritten to document the v1 wire protocol as the frozen specification, replacing the v0.4 aspirational text it carried since the project's early days. The shipped control message set (`register`, `connect`, `session-request`, `session-join`, `session-start`, `wg-pubkey`, `udp-offer`, `peer-endpoint`, `session-end`, `error`, `mgmt-request`/`mgmt-response`, `keepalive`) is now documented with full JSON schemas; the relay frame header (already specified in DESIGN-relay-gateway.md section 2) is referenced as authoritative; multiplexing is documented as a v1 non-goal with the `session_id` field reserved for a hypothetical v2; the static-token model is documented with a forward pointer to the issue #24 rework; the 1.x backward-compatibility policy is spelled out (covered surface, hard rules, additive policy, deprecation procedure, cross-version compat matrix); and the no-public-Go-API decision is documented as DESIGN.md section 6.9. Closes #19, #20, #21, #22.
- **Wire-protocol version negotiation.** Agents and clients now send `protocolVersion: 1` on their `register` and `connect` messages. The hub records the value per agent and surfaces it on `/api/status` as `machines[].protocolVersion`. Pre-0.16 binaries that omit the field are treated as `protocolVersion: 1` per the v1.x cross-version compat policy; the field is fully additive and backward-compatible. Closes #18.

### Changed
- DESIGN.md section 6.3 (Frame Format) now points at DESIGN-relay-gateway.md section 2 as the authoritative source for the 7-byte v1 relay frame header layout. The earlier 12-byte aspirational struct in this section was never implemented.
- DESIGN.md section 6.5 (Tokens) now describes the actual static-secret token model rather than the JWT/browser-flow aspiration. The replacement structured-token system is tracked under issue #24.

## [0.15] - 2026-04-27

### Added
- TelaVisor has a new **Updates** tab in Clients mode (between Files and Client Settings) that consolidates everything related to local-binary update lifecycle. The tab header carries a *Check automatically* checkbox and a *Check Now* button; the body has a Release Channel card with the channel dropdown that drives both TelaVisor and the tela CLI (single shared preference in `~/.tela/credentials.yaml`), and an Installed Tools card with the full versions table for TelaVisor, the tela CLI, and any local telad/telahubd binaries. Channel Sources (custom channel URLs that feed every channel dropdown in the app) stays in Client Settings and the Updates tab links out to it. The topbar warning indicator continues to open its own update modal; indicator and tab share state so installing from either surface clears the other.
- TelaVisor has a new **Access** peer tab (between Agents and Remotes) that replaces the old Tokens and Access sub-views under Hubs. The page has two projections of the same data: **By machine** shows a machine rail and an identity matrix for the selected machine; **By identity** shows an identity rail and a machine matrix for the selected identity. The view toggle persists across sessions. Both views read and write a shared pending-change set, so toggling between them never loses staged work. The toolbar has Undo, Save, Add Identity, Pair Code, Rescan services, and Export audit; the By identity detail header has Rename, Change role, Rotate token, and Delete identity.
- Batched save with optimistic concurrency on the Access page: toggling checkboxes or clicking Revoke stages changes into a pending set without touching the server. Save commits the whole batch in parallel, each write carrying the identity's version as `If-Match`. A 412 on any row opens a conflict modal listing the affected rows with Reload (discard pending, load fresh), Discard my changes (drop pending, stay in place), and a muted "force overwrite instead" link that retries the conflicting writes without the precondition behind a second confirmation.
- Per-row services display on the Access matrix is a compact one-line cell. Up to three service chips render inline; longer filters collapse the overflow into a `+N more` link that opens a small read-only popover with the full list. The popover never edits; Edit... in the actions column is the dedicated edit path. The Edit... modal has "Allow all services" / "Restrict to specific services" radio buttons; restricted mode renders a scrollable checkbox list built from the machine's advertised services, or a free-text input for the wildcard `*` machine.
- Wildcard ACL inheritance is now visible in the Access matrix. The hub cascades connect and manage from the wildcard `*` ACL to every machine that has no explicit grant; specific-machine rows for those identities now render the inherited cells as checked-and-disabled with a tooltip pointing the operator at the wildcard row, and the services column shows the wildcard's filter with a `via *` marker. The cascade metadata comes from the hub itself (new `wildcardInherited` and `wildcardInheritedServices` fields on every accessEntry), not from client-side cascade rules, so future protocol changes flow through without a UI update.
- Optimistic concurrency control on the hub's admin access endpoints. Every identity carries a per-identity monotonic version counter; `GET /api/admin/access` and `GET /api/admin/access/{id}` return the counter in the JSON body and the `ETag` response header. Mutating requests (`PUT`, `PATCH`, `DELETE` on the access resource and its `/machines/{id}` sub-resource, plus `POST /api/admin/rotate/{id}`) honor an `If-Match: "<version>"` precondition and return `412 Precondition Failed` with the current server-side state in the response body when the client's version is stale. The new Access UI in TelaVisor uses this to detect conflicts on batched saves; third-party clients that omit the header continue to work unchanged.
- Role change on `PATCH /api/admin/access/{id}`. The patch handler now accepts an optional `role` field (one of `owner`, `admin`, `viewer`, or the empty string for `user`) alongside the existing `id` field (rename). Either or both may be set. A last-owner guard returns `409 Conflict` on any attempt to demote the sole owner, so the hub cannot be left in a state with no one who can manage it. The new TelaVisor Access page's **Change role** action drives this endpoint, including an Owner option behind a second confirmation for promotions.
- Portal admin proxy forwards `If-Match` (and `If-None-Match`) request headers through to upstream hubs, and exposes the upstream `ETag` response header to callers. This closes the conditional-request contract end to end for embedded-portal and remote-portal TelaVisor setups alike.
- Export audit on the Access page opens a native OS save dialog (matching how the log pane's Save works) instead of routing through the browser's default download path.
- Agent pairing flows on TelaVisor's Agents tab. The previous *Pair Agent* button (which only generated a code despite the name) is replaced by two single-purpose buttons in the sidebar footer. **Generate...** opens a modal with a hub picker, machine name, and expiration; the result modal shows the code in large monospace next to a pre-filled `telad pair -hub <ws-url> -code <code> -machine <name>` command line, both with copy buttons, so the operator can paste the command straight into chat or SSH on the agent host. **Redeem...** is new: when telad is installed locally it runs `telad pair` against the local binary so a developer workstation can register an agent here without dropping to a shell. When telad is missing the modal opens in a bail-out state with an *Open Updates...* shortcut to the Updates tab and a disclaimer that remote-agent redemption has to happen on the agent host. The two short labels are paired with a sidebar caption ("Generate a pair code for an agent on another machine, or redeem one to register an agent here.") and tooltips that carry the long form. Closes the latent bug in the prior code path that sent the wrong field name (`machines: [name]` instead of `machineId: name`) on register-type pair-code requests.
- TDL annotation text primitives section in [TELA-DESIGN-LANGUAGE.md](TELA-DESIGN-LANGUAGE.md) documents the canonical classes (`.section-desc`, `.hint`, `.empty-hint`, `.loading`, `.tools-service-label`, `.update-note`) that carry the muted-and-subordinate look used everywhere annotation copy appears, with the rule that any new annotation-style class needs a global CSS rule at the top of the stylesheet rather than only a scoped descendant rule. Several painful regressions traced to exactly this gap; the convention is now explicit.

- Tela project site at [telaproject.org](https://telaproject.org/). The root URL is now a proper project landing page with a hero, value proposition, version map, download pointer, and community links, not the stable book's introduction chapter. The book moves under `/book/` with per-channel editions at `/book/beta/` and `/book/dev/` and archived stables at `/book/archive/vX.Y.Z/`. The canonical Tela Design Language stylesheet is extracted to `/tdl.css` and consumed by the landing page, ready for TelaVisor and Awan Saya to migrate to on their own schedules. The old `paulmooreparks.github.io/tela/*` URLs redirect to `telaproject.org/*` automatically; deep-linked book pages that used to live at the root redirect to 404 with a smart hint page that suggests the `/book/` prefix. See [DESIGN-telaproject-site.md](DESIGN-telaproject-site.md) for the full design.

- Per-channel book deployments. The documentation site now publishes three independently versioned editions to GitHub Pages, one per release channel: stable at `paulmooreparks.github.io/tela/` (the default for any visitor), beta at `/beta/`, dev at `/dev/`. Each edition is rebuilt from the tag that defines its channel and is labelled with the exact tag it was built from, so stable readers never see dev docs that describe unreleased behavior. Non-stable editions carry a banner at the top of every page linking back to stable; every edition carries a small "Other editions" switcher in the sidebar footer. Stable releases are also archived to `/archive/vX.Y.Z/`, with an `/archive/` landing page listing every frozen edition. See [DESIGN-book-versioning.md](DESIGN-book-versioning.md) for the full design.

- Docker-first distribution for `telahubd`. A new `Dockerfile.telahubd` at the repo root builds the hub from source onto `gcr.io/distroless/static-debian12:nonroot`; the release workflow publishes multi-arch (linux/amd64 + linux/arm64) images to `ghcr.io/paulmooreparks/telahubd` under the `:stable`, `:latest`, `:beta`, and `:v<version>` tags. Dev-channel builds do not publish Docker images to keep the registry tidy; operators who want to run a dev build via Docker rebuild the Dockerfile locally from the dev tag's source.
- `telahubd health` subcommand that probes `http://127.0.0.1:<port>/.well-known/tela` and exits 0 when healthy. Used by the Docker image's `HEALTHCHECK` directive so `docker ps` reports container health without a shell in the distroless base image.
- Three copy-paste `docker-compose` templates under `deploy/docker/`: `minimal` (LAN / dev), `caddy` (production with auto-Let's Encrypt), `nginx` (for operators with existing nginx). Each template is a working file plus a fully-written reverse-proxy config (Caddyfile, nginx.conf) with the WebSocket upgrade handling spelled out.
- Book's hub install chapter rewritten to lead with Docker; the native-binary install is kept intact but demoted to an "alternative" section. Authentication and TLS subsections now call out which steps a Docker install has already handled.

### Fixed
- Relay gateway: `initBridgeDir` now rejects self-bridges at startup. A `bridges:` entry whose `hubId` matches the hub's own logs a WARNING and is skipped, so a misconfigured entry that would loop sessions back into the same hub (draining TTL before any useful forwarding) fails loudly at load time instead of silently consuming every session routed through it. URL-based matching is intentionally not attempted; `hubId` equality is the reliable signal and catches the copy-paste case.
- Relay gateway: outbound bridge WebSocket now installs `SetPongHandler` and a 10-second ping cadence (matching `runKeepalive` on the inbound control WS), with a 30-second pong-timeout read deadline. A middlebox that silently drops the bridge TCP connection without tearing it down now surfaces as a read error within the pong window rather than only via the 45-second in-band session keepalive. The in-band keepalive remains the authoritative end-to-end health signal; the WS-level ping/pong is the per-hop dead-connection detector.
- Topbar Mount button in TelaVisor now turns green reliably after auto-mount comes up. Previously the button state was re-checked once, three seconds after the `tela:connected` event; if the auto-mount's WebDAV port took longer than that to start listening (not uncommon under cold-start / slow I/O), the port probe failed and the button stayed grey forever, even though the mount was active. Clicking the button in that state correctly dispatched to the stop path -- which was the observed symptom: "Mount stopped" in the log despite the button looking like no mount was running. The check now polls across an ~8-second window (500ms, 1.5s, 3s, 5s, 8s) so the button turns green as soon as the port is listening; cheap TCP dial per probe, no-op once the button is already green.
- `telahubd -config /path/to/file` treats a non-existent file as a first-start signal instead of fatal-erroring during `loadHubConfig`. The bootstrap path (env-var or auto) writes the config to the supplied path on first successful start. Previously a `docker run` against an empty volume crashed immediately; now it bootstraps cleanly.
- `autoBootstrapAuth` banner now reports where the token was persisted and how to retrieve it later via `telahubd user show-owner`. Previously the banner said "it will not be displayed again" with no follow-up pointer, leaving operators who missed the first boot line without a recovery breadcrumb. Persist failures (unwritable `/data`, permission issues on a bind mount) now log two `WARNING` lines instead of being silently swallowed.

### Fixed
- `tela update`, `telad update`, `telahubd update`, the hub admin `POST /api/admin/update` endpoint, the agent `update` mgmt action, and TelaVisor's local-binary install paths now bypass the channel-manifest cache. Previously a 5-minute in-process cache could make an install path read a stale manifest and refuse a freshly-published tag (or, for TV-mediated agent updates, send the agent a version that did not match what the agent saw on its own fetch). Status displays still use the cached path; only the install side fetches fresh.
- Modal padding lives on `.modal-body` itself instead of on `.modal-dialog form`, so any state shape inside a modal (forms, bail-out states, result rows, settings groups, copyable command blocks) gets the 20px gutter automatically. Previously the Redeem Agent Code modal's *telad-not-installed* bail-out state (a sibling of the form, not a form itself) rendered edge-to-edge against the dialog edge, and other modals had to wrap content in a `.modal-body-padded` div to get the inset back. The Add Hub modal opts out with a new `.modal-body-flush` modifier because its full-width tab bar needs to span the dialog edges. Documented in the Modals section of [TELA-DESIGN-LANGUAGE.md](TELA-DESIGN-LANGUAGE.md) so the rule survives the next contributor.
- Redeem Agent Code's "is telad installed?" check is now deterministic and goes through three sources, in order, most-authoritative to least: hub-mediated comms (read the local control file to discover which hub telad registered with, then hit the hub's new `GET /api/admin/agents/{machine}` lightweight presence endpoint; `agentConnected=true` means telad's outbound WebSocket session is live, which is the actual "telad is alive" API answer rather than just "the process started at some point"); the OS service manager (`service.QueryStatus` for system services, `service.QueryUserStatus` for user-mode autostart, both deterministic platform calls); and finally the configured Binary Location. Service-config JSON reads are demoted to fallback-only and used solely by `locateTelad` when an executable path is needed. The result is cached for five seconds and concurrent callers coalesce onto a single in-flight probe, so N Agents-sidebar paints aligned on the TTL boundary produce exactly one network round-trip. The probe runs under a 2-second budget so a slow portal or hub cannot block the UI's button-enable check; the underlying calls keep their own timeouts and their goroutines complete in the background without writing back. PATH scanning is intentionally not part of the sequence; if telad is installed in some directory not recorded in any of those sources, TV does not pretend to know about it. Previously the modal short-circuited to the *not installed* bail-out whenever telad was missing from the Binary Location, contradicting the Installed Tools table on the Updates tab right next door.
- Hub admin API: `GET /api/admin/agents/{machine}` returns a minimal presence object (id, agentConnected, lastSeen) instead of forcing callers to download the full `/api/status` payload to read a single boolean. Viewers who do not hold `canViewMachine` for the target see the same 404 an unregistered machine produces, so the endpoint cannot be used to enumerate. Used by TelaVisor's HasLocalTelad check and available to third-party admin clients.
- HasLocalTelad probe now logs every tier's outcome on each cache-miss probe, not just the winning tier. The line reads `hub=<outcome> service=<outcome> user-service=<outcome> bin-path=<outcome> -> <answer> (<winner>)`. The hub tier has five possible outcomes -- `yes`, `offline` (hub answered but agentConnected=false), `unreachable` (hub query errored or timed out), `no-control-file` (telad never wrote a control file locally, so we couldn't even try), and the always-filled-in-by-cascade states; the OS tiers have three (`yes`, `no`, `skip`). An operator seeing "hub=unreachable service=yes" knows the network path failed and the cascade fell through, while "hub=offline service=yes" is a telad that crashed locally with the service still registered. Previously the operator had to guess which tier answered from the single-tag log.
- HasLocalTelad cache is explicitly invalidated after `InstallBinary("telad")` and after a successful `RedeemAgentCode`, so the Redeem / Generate button state reflects reality immediately instead of waiting out the TTL. Invalidation bumps a generation counter atomically; an in-flight probe captures the pre-bump generation at start and, on completion, discards its result rather than caching it if the counter has advanced (meaning an invalidation landed while the probe was network-blocked on a pre-change view). Invalidation itself does not wait on the probe mutex, so it runs immediately even when a slow hub probe is stuck -- the cache reflects the user's action the moment it happens, not after the probe times out.
- Admin-proxy call helper has a `adminProxyCallCtx` variant that honors the caller's context instead of creating a fresh 30-second timeout internally. The HasLocalTelad probe uses this with its 2-second budget, so when the probe select fires on the budget the underlying HTTP request is actually cancelled via context rather than orphaned to run against the hub for the full 30 seconds with no one listening to the result. The previous `time.After`-based timeout also leaked its Timer under rapid calls; `context.WithTimeout` + `defer cancel()` avoids that.
- Portal lookup helper has a `resolveHubNameByURLCtx` variant that honors the caller's context. Without this, the HasLocalTelad probe could orphan the portal `ListHubs` call for up to ten seconds past the presence budget: the outer `teladOnlineViaHub` select returned, but the inner `resolveHubNameByURL` kept running against its own `context.WithTimeout(context.Background(), 10*time.Second)`. Every network call on the probe path now inherits the same 2-second budget, so the goroutine exits promptly.
- Presence cache invalidation is now atomic from readers' perspective. Previously the invalidator bumped `teladPresenceGen` before acquiring `teladPresenceMu`, leaving a brief window where a reader could acquire the mutex first, see the still-populated timestamp, and return the stale cached value. Moving the gen bump inside the mutex region closes the window: readers see either the pre- or post-invalidate state, never the in-between.

### Changed
- The hub's "default update" message pushed to registering agents now carries an `update.sources` map instead of the legacy `manifestBase` scalar. Agents merge the map into their local sources, with local entries winning on conflict so a deliberately-set agent-side URL is never overwritten silently.
- The legacy `manifestBase` field on `PATCH /api/admin/update` request bodies and on the agent `update-channel` mgmt action is now redirected into `sources[channel]` server-side. The `-manifest-base` CLI flag on `tela admin hub channel set` and `tela admin agent ... channel set` continues to work; the operator-facing API shape is unchanged.

### Removed
- The Tokens and Access entries under the Hubs admin sub-nav in TelaVisor are gone; both paths are now handled by the new Access peer tab. Operators who relied on the old flow can find every action on the new page: create a token through Add Identity, rotate or delete a token through the By identity detail header, grant or revoke per-machine access through the matrix.
- The Installed Tools card and the TelaVisor self-update group are gone from Client Settings and Application Settings respectively. Both are replaced by the new Updates tab; the auto-check checkbox moved to the Updates tab header (re-labeled *Check automatically* since the underlying check covers tela CLI too), and the channel selector + Check Now button have a single home rather than living in two places at once.
- The deprecated `update.manifestBase` config field is gone from `telad.yaml`, `telahubd.yaml`, and `~/.tela/credentials.yaml`, along with the in-process `MigrateManifestBase` migration helper that ported pre-0.12 configs to the `update.sources` map shape on first load. Operators upgrading directly from pre-0.12 to 0.13+ should run `tela channel sources set <channel> <url>` (or its `telad`/`telahubd` equivalents) before relying on the channel; an old config still parses (yaml.v3 silently drops unknown fields), but a custom channel previously pointed at by `manifestBase` will fail its next manifest fetch with an empty URL. Closes #59.

## [0.12.0] - 2026-04-20

### Added
- `telahubd channel` subcommand, bringing the hub into parity with `tela channel` and `telad channel`. Shows the current channel and latest version, switches the channel (`telahubd channel set <name>`), prints the full parsed manifest (`telahubd channel show`). The `-config` flag defaults to the platform-standard config path (`/etc/tela/telahubd.yaml` or `%ProgramData%\Tela\telahubd.yaml`).
- `telad channel show [-channel <ch>]` prints the full parsed manifest for the agent, mirroring the client and hub.
- Release channel sources: the legacy `update.manifestBase` scalar is replaced by `update.sources: map[channel]url` across `telad.yaml`, `telahubd.yaml`, and `~/.tela/credentials.yaml`. Pre-0.12 configs migrate automatically on first load. `channel sources list`, `channel sources set <name> <url>`, and `channel sources remove <name>` subcommands on all three binaries.
- Hub admin API: `GET /api/admin/update/sources`, `PUT /api/admin/update/sources/{name}`, `DELETE /api/admin/update/sources/{name}` for managing the hub's channel sources remotely.
- Agent mgmt actions: `channel-sources-list`, `channel-sources-set`, `channel-sources-remove` reach remote agents through the hub-mediated management protocol.
- `tela admin hub channel sources ...` and `tela admin agent <hub> <machine> channel sources ...` passthroughs to manage a remote hub's or agent's sources from the CLI.
- TelaVisor: Channel Sources cards on Client Settings, Hub Settings, and Agent Settings. Per-target dropdowns show the union of built-in channels plus the target's own sources; agent dropdowns additionally surface hub-side sources as suggestions with an explicit push-on-select flow.
- First-run channel inference: binaries whose version string matches `vX.Y.0-{channel}.N` default to that channel on first self-update, instead of silently following `stable` and potentially downgrading.
- Downgrade refusal on `update`: both the local `update` subcommand and the hub/agent admin update endpoints compare the channel head's semver against the running binary and refuse to install an older version. Fixes the silent downgrade class of bugs where switching channels could move a binary backwards.
- `telahubd` self-hosts release channels in-process under `/channels/`. A new `channels:` config block (enabled, data, publicURL) mounts `GET /channels/{name}.json` for manifests and `GET /channels/files/{channel}/{binary}` for binary downloads, plus directory listings at `/channels/files/` and `/channels/files/{channel}/` for browsing. Accompanied by the `telahubd channels publish` subcommand that scans `channels.data/files/{channel}/`, hashes the binaries, and writes `channels.data/{name}.json`.
- Remote publishing via admin API: `PUT /api/admin/channels/files/{channel}/{name}` uploads binaries into a per-channel subdirectory and `POST /api/admin/channels/publish` hashes them and writes the manifest. Lets a build pipeline publish to a self-hosted channel server over HTTPS with no SSH or file-share mount. Owner/admin auth required, 500 MiB per-file cap.

### Fixed
- `telahubd` now drains in-flight requests on shutdown instead of dropping them. `server.Shutdown(ctx)` replaces the previous `server.Close()` call, with a configurable drain timeout (`shutdownTimeout` in `telahubd.yaml`, default 30s) and log lines showing how many requests are in flight at drain start and how long the drain took. A second SIGINT during the drain escalates to immediate exit. Fixes #30.
- Foreground `telahubd` now reads the platform-standard config file (`/etc/tela/telahubd.yaml` on Linux/macOS, `%ProgramData%\Tela\telahubd.yaml` on Windows) when `-config` is not given and no `./data/telahubd.yaml` is present. Previously, running `sudo telahubd user bootstrap` followed by `sudo telahubd` would auto-generate a second owner token because foreground mode never looked at the system config path.
- `telahubd service install` refuses to overwrite a system config file that already has tokens (e.g. one written by `telahubd user bootstrap`), instead of silently destroying them. Operators who want to reconfigure should edit the file and restart the service.
- `tela update help` and `telahubd update help` no longer silently run the update. Any stray positional argument on an `update` command now errors with "use -h for help".
- `tela update -channel <custom>` and `telahubd update -channel <custom>` accept custom channel names (matching the validator used by `channel set`). Previously they rejected anything outside dev|beta|stable even though the rest of the channel tooling has supported custom channels.

### Changed
- Help flags are now consistent across all three binaries: `-h`, `-?`, `-help`, and `--help` trigger help at every command and subcommand level (e.g. `tela channel set -h`). The bare `help` keyword still works at the top level (`tela help`, `telad help`, `telahubd help`) but no longer runs commands by accident when passed as a positional argument.
- `telahubd service install -www` now defaults to empty (serve the embedded hub console). The previous default of `./www` wrote a confusing absolute path into the generated config. Operators who want to serve custom static files pass `-www /path/to/dir` explicitly.
- Book: rewrote the hub install walkthrough with a proxy-first deployment-model table, corrected ordering (`service install` before `user bootstrap`), and added an Apache httpd section alongside Caddy, nginx, and Cloudflare Tunnel.

### Removed
- `telachand` binary. Channel hosting is now a feature of `telahubd` itself; see the Added entry above. Operators who had a telachand deployment can point their hub's `channels.data` at the old telachand data directory unchanged. The `update.manifestBase` scalar field is kept for one release cycle for migration purposes; it is scheduled for removal in 0.13 (GitHub issue #59).

## [0.10.1] - 2026-04-17

### Added
- `tela channel` subcommand: `tela channel` shows the current channel and latest version, `tela channel set <name>` changes the channel, `tela channel show -channel <name>` inspects any channel's manifest.
- Custom channel sources in TelaVisor Application Settings: add, edit, and remove manifest base URLs for self-hosted channels alongside the built-in GitHub channels.
- Hub-pushed update defaults: hubs can set a default `update.channel` and `update.manifestBase` that agents inherit on registration when they have no explicit channel configured.
- TDL sidebar version badges in TelaVisor: green checkmark when current, amber up-arrow when an update is available.
- Agent file shares card in TelaVisor Infrastructure mode: view configured file shares per agent.

### Fixed
- Version comparison for update-available checks: agents and hubs ahead of the channel (e.g. local dev builds on a stale manifest) no longer incorrectly show as outdated. Uses proper semantic version comparison instead of string inequality.
- UDP relay health check: idle sessions no longer fall back to WebSocket after 60 seconds of inactivity. Fallback now only triggers when the session is actively sending via UDP but receiving nothing back, keeping idle sessions on the faster UDP path.
- TelaVisor window position restore on startup.
- `telachand` now serves any valid custom channel name, not just dev/beta/stable.

## [0.10] - 2026-04-15

The "multi-share and loopback" release. Named file shares, reliable port
binding on Windows, and TelaVisor file browser share navigation.

### Added
- `telachand`: new Tela Channel Daemon binary. Hosts channel manifests and binary files over HTTP so operators can run a self-hosted alternative to the default GitHub release channel. Supports `publish` (scan a files directory, compute SHA-256s, write a manifest), `service install/start/stop/status` (system and user autostart on all platforms), `update` (self-update from any channel), and the same YAML config and service patterns as the other Tela binaries. Configure tela/telad/telahubd/TelaVisor to point their update base URL at a running `telachand` instance.
- Multiple named file shares per agent machine: replace the single `fileShare` config with a `shares` list, each with a `name` and `path`. WebDAV mount paths change from `/machine/path` to `/machine/share/path`. `tela files` subcommands gain a required `-share` flag.
- `list-shares` protocol operation returns available shares on a machine, used by `tela files info` and the WebDAV machine directory listing.
- TelaVisor Files tab supports named shares: clicking a machine shows its shares as folder entries; opening a share enters it. Machines with a single share skip the intermediate folder view. All file operations pass the share name.

### Fixed
- Port binding reverted to `127.0.0.1`: removes the per-machine 127.88.x.x loopback address scheme that caused Windows loopback shadowing (local SSH and RDP intercepting tunnel connections). When a service port is already in use, tela tries `port+10000`, then `port+10001`, `port+10002`, and so on until a free port is found, so no service is skipped due to port conflicts between simultaneously connected machines. TelaVisor reads bound ports from the control API instead of parsing log output, and shows the actual bound port for each service. A service that truly cannot bind shows "Unavailable" rather than "Connecting..." so the distinction between a port conflict and a tunnel not yet established is visible.
- Copy buttons in TelaVisor Status tab now work (used Wails clipboard API; fixed HTML attribute encoding for the onclick handler).

### Changed
- `fileShare` (singular) in telad config is deprecated; it is accepted and synthesized as a share named `legacy` with a startup warning. It will be removed in 1.0.

## [0.9] - 2026-04-14

The "release discipline" release. Stable baseline with comprehensive
documentation accuracy pass and release process formalization.

### Added
- User-level autostart for both tela and telad:
  `tela service install --user` and `telad service install --user`
  register autostart tasks that run at login without admin/root
  privileges. Windows uses Scheduled Tasks, Linux uses systemd
  --user units, macOS uses LaunchAgents. TelaVisor shows both
  system service and user autostart options in Client Settings.
- Control API `/tunnels` endpoint listing all connected machines
  (used by WebDAV mount to discover file-sharing machines)

### Fixed
- UDP relay auto-fallback to WebSocket: if no UDP data arrives
  for 60 seconds (dead NAT mapping, unreliable hairpin, or
  blocked path), the client automatically switches WireGuard
  traffic to WebSocket so handshakes complete and the tunnel
  recovers without user intervention
- UDP probe cascade: the client now tries the offered host, the
  WS peer IP, and the URL hostname in order, finding a working
  UDP path without configuration (handles Docker, LAN, and remote
  scenarios automatically)
- UDP session reaper killing active sessions after 5 minutes
  (regression from identity model changes: reaper looked up machine
  by display name instead of composite key, never found it, and
  deleted the UDP relay token)
- File share mount not listing machines connected without TCP
  services (mount queried `/services` which only has port-mapped
  machines; now queries `/tunnels`)

## [0.8] - 2026-04-10

The "hardening" release. Focus on polish, onboarding reliability,
and preparation for a stable release.

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

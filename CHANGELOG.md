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
- Docker-first distribution for `telahubd`. A new `Dockerfile.telahubd` at the repo root builds the hub from source onto `gcr.io/distroless/static-debian12:nonroot`; the release workflow publishes multi-arch (linux/amd64 + linux/arm64) images to `ghcr.io/paulmooreparks/telahubd` under the `:stable`, `:latest`, `:beta`, and `:v<version>` tags. Dev-channel builds do not publish Docker images to keep the registry tidy; operators who want to run a dev build via Docker rebuild the Dockerfile locally from the dev tag's source.
- `telahubd health` subcommand that probes `http://127.0.0.1:<port>/.well-known/tela` and exits 0 when healthy. Used by the Docker image's `HEALTHCHECK` directive so `docker ps` reports container health without a shell in the distroless base image.
- Three copy-paste `docker-compose` templates under `book/src/howto/hub-docker/`: `minimal` (LAN / dev), `caddy` (production with auto-Let's Encrypt), `nginx` (for operators with existing nginx). Each template is a working file plus a fully-written reverse-proxy config (Caddyfile, nginx.conf) with the WebSocket upgrade handling spelled out.
- Book's hub install chapter rewritten to lead with Docker; the native-binary install is kept intact but demoted to an "alternative" section. Authentication and TLS subsections now call out which steps a Docker install has already handled.

### Fixed
- `telahubd -config /path/to/file` treats a non-existent file as a first-start signal instead of fatal-erroring during `loadHubConfig`. The bootstrap path (env-var or auto) writes the config to the supplied path on first successful start. Previously a `docker run` against an empty volume crashed immediately; now it bootstraps cleanly.
- `autoBootstrapAuth` banner now reports where the token was persisted and how to retrieve it later via `telahubd user show-owner`. Previously the banner said "it will not be displayed again" with no follow-up pointer, leaving operators who missed the first boot line without a recovery breadcrumb. Persist failures (unwritable `/data`, permission issues on a bind mount) now log two `WARNING` lines instead of being silently swallowed.

### Fixed
- `tela update`, `telad update`, `telahubd update`, the hub admin `POST /api/admin/update` endpoint, the agent `update` mgmt action, and TelaVisor's local-binary install paths now bypass the channel-manifest cache. Previously a 5-minute in-process cache could make an install path read a stale manifest and refuse a freshly-published tag (or, for TV-mediated agent updates, send the agent a version that did not match what the agent saw on its own fetch). Status displays still use the cached path; only the install side fetches fresh.

### Changed
- The hub's "default update" message pushed to registering agents now carries an `update.sources` map instead of the legacy `manifestBase` scalar. Agents merge the map into their local sources, with local entries winning on conflict so a deliberately-set agent-side URL is never overwritten silently.
- The legacy `manifestBase` field on `PATCH /api/admin/update` request bodies and on the agent `update-channel` mgmt action is now redirected into `sources[channel]` server-side. The `-manifest-base` CLI flag on `tela admin hub channel set` and `tela admin agent ... channel set` continues to work; the operator-facing API shape is unchanged.

### Removed
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

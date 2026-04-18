# Channel sources: design

This document is the authoritative design for the channel-sources rework tracked as [issue #55](https://github.com/paulmooreparks/tela/issues/55), targeting the `0.12` release.

It covers the data model for per-host channel configuration, first-run channel inference from binary version, CLI and API surfaces, the retirement of `telachand`, TelaVisor's integration points, downgrade protection on `update`, and the codification of the API-first architectural principle that motivates the choices below.

Narrative documentation for end-users (how to configure self-hosted channels, how to run an internal mirror) comes later as a book chapter; this document speaks to maintainers and integrators.

---

## 1. Motivation

Two bugs filed against the `0.11` line share a single root cause:

- [#4](https://github.com/paulmooreparks/tela/issues/4) — TelaVisor surfaces a remote agent's channel status with a URL derived from TelaVisor's own `manifestBase`, not the agent's, so switching TelaVisor to a custom channel breaks status-read on remote agents pointed at different channels.
- [#54](https://github.com/paulmooreparks/tela/issues/54) — a freshly-downloaded `v0.11.0-beta.1` binary, run with no config, defaults to the `dev` channel because that is the compile-time fallback. First `telahubd update` silently downgrades across channels to `v0.11.0-dev.11`.

Both symptoms reduce to the same model defect: the config stores a single scalar `update.manifestBase` attached to the currently-selected channel. There is no concept of "what channels this host knows about and where to reach them." Switching the channel either loses the base entirely (orphaning a custom source) or leaves a stale base that points nowhere when paired with a different channel name.

A patch that clears `manifestBase` on channel change (`ApplyBasePatch` in the abandoned branch for #4) addresses one corner. It does not address the broader class of problems around channel discoverability, enterprise-controlled channel hosting, hub-to-agent policy flow, or the first-run default.

The fix is a data-model redesign. The rest of this document specifies it.

## 2. Architectural principle: API-first

Before the specification, the rule that shapes every choice below:

> **API-first, CLI-second, UI-last.**
> All Tela behavior is implemented as daemon APIs. CLI tools (`tela`, `telad`, `telahubd`) are shells over those APIs. TelaVisor is a UI shell over the CLI's APIs and the hub/agent HTTP APIs. TelaVisor contains no business logic of its own — it never parses CLI output, never reads log files, never scrapes process state. If a TelaVisor feature requires it to look at tool output instead of calling an API, that is a bug in the API surface, not a license to scrape. Raise the gap as an issue, extend the API, then consume it from TelaVisor.

This rule lands in `CLAUDE.md` and `DEVELOPMENT.md` alongside this design and governs the shape of everything below. It forces every feature to surface a typed API, which is what lets the `tela` CLI and TelaVisor be peers over the same control surface rather than one of them (typically TV) hoarding behavior that operators then can't script.

Direct consequence for this rework: every operation described below has a corresponding admin API endpoint or agent mgmt action. TelaVisor invokes those. The CLI invokes those. Neither depends on the other's output.

## 3. Data model

### 3.1 The `sources` map

Each Tela tool's configuration (`telad.yaml`, `telahubd.yaml`, and the client credential store used by `tela` and TelaVisor) gains a new optional field under the existing `update:` block:

```yaml
update:
  channel: stable
  sources:
    stable: https://releases.orga.com/tela/       # override baked-in default
    internal: https://internal.orga.com/tela/     # add custom channel
```

Rules:

- Keys are channel names, matching `channel.IsValid` (lowercase letters, digits, hyphens).
- Values are base URLs with a trailing slash recommended but not required. URLs may be empty strings.
- The map itself is optional. A host with no `sources` key uses only baked-in defaults.
- An empty string value is treated the same as "absent": the lookup falls through to the baked-in default for that name. This lets operators document that they rely on the built-in for a given channel without committing to the URL.

### 3.2 Baked-in defaults

Each binary ships with compiled-in base URLs for the three standard channels. Today those are GitHub Releases; they could change at build time in a fork.

```
channel.DefaultBases = map[string]string{
    "dev":    "https://github.com/paulmooreparks/tela/releases/download/channels/",
    "beta":   "https://github.com/paulmooreparks/tela/releases/download/channels/",
    "stable": "https://github.com/paulmooreparks/tela/releases/download/channels/",
}
```

The map is canonical in `internal/channel`. Each binary picks it up through the existing `channel` package; no per-binary duplication.

### 3.3 Lookup order

Resolving a base URL for channel `X`:

1. `sources.X` if present and non-empty → use that URL.
2. `channel.DefaultBases[X]` if `X` is a built-in → use that URL.
3. Error: `unknown channel 'X'`. All channel-requiring operations (`update`, `channel show`, `channel download`) fail with this message.

Consequence: adding a custom channel requires at least a sources entry. Using a built-in channel requires nothing. The config is sparse by construction.

### 3.4 Pre-1.0 migration from `manifestBase`

Existing configs (pre-0.12) use `update.manifestBase: <url>` paired with `update.channel: <name>`. On first load by a 0.12 binary:

1. If `manifestBase` is unset, nothing to do.
2. If `manifestBase` is set and `channel` is one of the built-ins (`dev`, `beta`, `stable`): discard `manifestBase`. The baked-in default takes over. Log a one-line notice identifying the removal.
3. If `manifestBase` is set and `channel` is a custom name: populate `sources[channel] = manifestBase` unless the same key is already set with a different value (in which case keep the existing `sources` entry and discard `manifestBase`). Log a one-line notice.
4. Remove the `manifestBase` field from the config object. Next write omits it.
5. Write the migrated config immediately so the transformation is visible on disk before any further operation.

The migration code is written once and removed entirely before the `0.12` beta is cut. This is consistent with the pre-1.0 "delete cruft aggressively" rule in `CLAUDE.md`.

### 3.5 The `channel` field's initial value

A freshly-installed binary with no config file has no `update.channel`. On first write, the binary infers the default from its own version string:

| Version shape | Inferred channel |
|---|---|
| `vX.Y.0-dev.N` | `dev` |
| `vX.Y.0-beta.N` | `beta` |
| `vX.Y.Z` (no prerelease suffix) | `stable` |
| `vX.Y.0-{name}.N` for any other `{name}` | `{name}` |

Implementation: a single helper `channel.InferFromVersion(v string) string` in `internal/channel`. Each binary's main-path calls this exactly once at config-creation time.

Inference applies only on first run. Once `update.channel` is persisted, subsequent runs honor the saved value regardless of binary version. This preserves operator intent if a binary is rebuilt at a different channel.

For the custom-inferred case (e.g., `v0.12.0-local.3`): the inferred channel name (`local`) may not be in `sources`. The config is written with the inferred channel and no sources entry. Subsequent channel-requiring operations fail with `unknown channel 'local'; run 'telahubd channel sources set local <url>' to enable`. The binary's primary function (relay/agent/client) continues to work.

### 3.6 Lockdown (deferred to 1.x)

The lockdown story — "hub enforces a fleet-wide policy that agents cannot override" — is explicitly deferred to 1.x. The `sources` map offers three operator-side mechanisms that cover most enterprise needs without new fields:

1. Override a built-in's URL to point at an internal mirror (`sources.stable: https://internal/mirror/`).
2. Host only the channels the fleet should follow, on a hub's `channels:` directory; agents pointed at that hub's channel base get 404 on removed channels.
3. Set agents' `sources` to point solely at the hub's channel endpoint so they cannot reach GitHub at all.

1.0 ships without a disabled-channels field. When the real fleet lockdown feature is designed in 1.x, it will have full hub-enforcement semantics and a considered allow-list-vs-deny-list shape. Adding a placeholder now would commit to a shape before we have the use cases in hand.

## 4. First-run and downgrade protection on `update`

### 4.1 Downgrade refusal

`update` commands on every binary refuse to proceed when the channel's `latestVersion` is older than the currently-running `currentVersion` by semver:

> `latest version on {channel} is v0.11.0-beta.1, older than currently running v0.11.0. Use -version vX.Y.Z to select an explicit version if you intend to downgrade.`

This rule applies to:

- `tela update`
- `telad update`
- `telahubd update`
- `tela admin hub update -hub <name>`
- `tela admin agent update -machine <id>`

### 4.2 Explicit-version downgrade

The same `update` commands accept `-version vX.Y.Z`. When a specific version is named, no semver comparison gates proceed: the request is an explicit operator choice and the command honors it. This is how you deliberately move backward.

`tela channel download <binary> -version vX.Y.Z` remains available for pulling a specific artifact without activating it; that path is unchanged.

### 4.3 Interaction with channel inference

Inference at first run sets `update.channel` to match the binary's version-string shape. A binary built for `beta` defaults to `beta`, never to `dev`. First `update` follows that channel; if `latestVersion == currentVersion` the command reports "already up to date" and exits 0.

Combined: a beta binary's first run cannot silently downgrade across channels, because it defaults to beta, and downgrade across channels requires an explicit `-channel dev -version vX.Y.Z` invocation.

## 5. CLI surface

### 5.1 Per-binary

Each of `tela`, `telad`, `telahubd` exposes:

```
<binary> channel                                  Show current channel, resolved URL, latest version
<binary> channel set <name> [-config <path>]      Switch selection
<binary> channel show [-channel <name>]           Print manifest for a channel
<binary> channel sources                          List known sources (shows built-ins and overrides)
<binary> channel sources set <name> <url>         Add or override a source URL
<binary> channel sources remove <name>            Remove a source override (baked-in still applies)
<binary> channel -h | -? | -help | --help         Print help (already consistent from the 0.11 harmonization)
```

There is no `channel set -manifest-base` shorthand. The two operations are separate: first `channel sources set`, then `channel set`. Terse aliasing proliferates ways to do the same thing and was explicitly rejected during design.

### 5.2 Admin passthroughs on `tela`

```
tela admin hub channel                                 (proxies telahubd channel on a remote hub)
tela admin hub channel set <name>
tela admin hub channel show [-channel <name>]
tela admin hub channel sources
tela admin hub channel sources set <name> <url>
tela admin hub channel sources remove <name>

tela admin agent channel -machine <id>                 (proxies telad channel on a remote agent via the hub mgmt API)
tela admin agent channel set <name> -machine <id>
tela admin agent channel show [-channel <name>] -machine <id>
tela admin agent channel sources -machine <id>
tela admin agent channel sources set <name> <url> -machine <id>
tela admin agent channel sources remove <name> -machine <id>
```

### 5.3 Downgrade-protected update commands

```
<binary> update [-channel <name>] [-dry-run]                       Refuses to downgrade
<binary> update -version vX.Y.Z [-channel <name>] [-dry-run]       Explicit version; proceeds either direction

tela admin hub update [-version vX.Y.Z]
tela admin agent update -machine <id> [-version vX.Y.Z]
```

## 6. API surface

### 6.1 Hub admin API

```
GET    /api/admin/update                                    Current channel + status (existing)
PATCH  /api/admin/update                                    Change channel (existing, behavior updated)
POST   /api/admin/update                                    Trigger update (existing, adds downgrade refusal)

GET    /api/admin/update/sources                            List hub's sources map
PUT    /api/admin/update/sources/{name}  {"base":"..."}     Set or override a source
DELETE /api/admin/update/sources/{name}                     Remove a source override
```

`GET /api/admin/update` response shape gains a `sources` key carrying the hub's current map, so a client can render the known-channels list in one round trip:

```json
{
  "channel": "stable",
  "manifestUrl": "https://releases.orga.com/tela/stable.json",
  "currentVersion": "v0.12.0",
  "latestVersion": "v0.12.0",
  "updateAvailable": false,
  "sources": { "stable": "https://releases.orga.com/tela/", "internal": "https://internal.orga.com/" }
}
```

Every endpoint is gated by the existing `manage` permission on the hub. No new permission category.

### 6.2 Agent mgmt actions

The hub-mediated agent management protocol gains three new actions alongside the existing `update`, `update-status`, and `update-channel`:

```
channel-sources-list                        → {sources: {name: base, ...}}
channel-sources-set    {name, base}         → {ok: true}
channel-sources-remove {name}               → {ok: true}
```

Payloads and responses are wrapped in the existing mgmt-request/mgmt-response envelope.

`update-status` response adds a `sources` field for symmetry with the hub's endpoint.

## 7. TelaVisor integration

### 7.1 Card placement

- **Client Settings → Channel sources card** (new location). Edits the client credstore's `update.sources` map via the tela client's admin surface or directly against the credstore file. Shared with the `tela` CLI on the same machine.
- **Hub Settings → Channel sources card** (new). Edits the hub's sources map via `PUT/DELETE /api/admin/update/sources/{name}`. Gated on the active token having hub-level manage rights.
- **Agent Settings → Channel sources card** (new). Edits an agent's sources map via the `channel-sources-set`/`channel-sources-remove` mgmt actions. Gated on the caller having per-agent manage rights.
- **Management cards on hubs and agents** (existing). Release-channel dropdown + Update button only. Dropdown options populated from the target's sources map plus built-ins.

Application Settings contains only TelaVisor-about-itself settings: self-update channel, logging, binary path. The old "Channel sources" card there moves to Client Settings. Applications Settings does not edit any per-tool configuration.

### 7.2 Dropdown population on Management cards

- **Hub Management card dropdown:** `{dev, beta, stable} ∪ {hub's sources.keys()}`.
- **Agent Management card dropdown:** `{dev, beta, stable} ∪ {agent's sources.keys()} ∪ {hub's sources.keys() as suggestions}`.

Suggestions that live on the hub but not on the agent appear in the dropdown with a distinguishing affordance — italic, a subtle trailing tag like `(from hub)`, or an indicator in the per-row info. Suggestions that exist on both sides (same name) render normally, with the agent's URL taking precedence on lookup.

### 7.3 Selecting a channel

When the operator picks a dropdown option:

- **Built-in channel or source already on the target:** single API call to `PATCH /api/admin/update` (for hubs) or `update-channel` mgmt action (for agents). Channel is set.
- **Hub-suggested source that the target does not yet have:** confirmation dialog shows exactly what will happen — "Register source `internal` = `https://internal.orga.com/` on this agent, then switch its channel to `internal`?" On confirmation, TelaVisor issues two calls in sequence: push the source via the sources API, then switch the channel via `update-channel`. One dialog, two operations.
- **Source URL conflict (target has `internal` but with a different URL):** target-wins by default. TelaVisor's dialog text surfaces both URLs and offers an explicit "Overwrite target's existing source" checkbox. Unchecked: the push is skipped and the existing target source is used. Checked: the push replaces the target's entry.

No background sync, no drift detection, no periodic reconciliation. Every change is a deliberate operator action with a visible dialog.

### 7.4 What TelaVisor never does

Per the API-first principle: TelaVisor never parses the output of `tela`, `telad`, or `telahubd`. Every piece of information surfaced in a card comes from an HTTP or mgmt API response. If a future TV feature has no corresponding API, a new issue is filed against the API gap and TV waits.

## 8. Channel hosting on telahubd

`telachand` as a separate binary is retired. Channel manifest hosting becomes a telahubd subsystem.

### 8.1 Deletions

- `cmd/telachand/` removed.
- `internal/channeld/` (or its current location) moves to `internal/hubchannels/`.
- Release workflow drops `telachand-linux-*`, `telachand-darwin-*`, `telachand-windows-*` from the build matrix.
- `telachand` references in docs updated to point at the hub-hosted equivalent.
- `telachand` entries in channel manifests removed.

### 8.2 telahubd's `channels:` config section

```yaml
# telahubd.yaml
channels:
  dir: /var/lib/tela/channels        # local filesystem directory containing manifests and files
  publicBase: https://hub.example.com/channels/  # what URL agents should reach these at (optional; derived from hub's publicURL otherwise)
```

When `channels.dir` is set, telahubd mounts two route families on its existing HTTP mux:

- `GET /channels/{name}.json` serves `{dir}/{name}.json` verbatim, with `Content-Type: application/json`.
- `GET /channels/{name}/files/{file}` serves `{dir}/{name}/files/{file}` with appropriate binary `Content-Type`.

Access control: `/channels/` routes are public read (no auth) by default, since channel manifests are meant to be read by unauthenticated binaries bootstrapping themselves. An operator who wants to restrict can configure a reverse-proxy rule in front of the hub.

Directory layout mirrors exactly what `telachand publish` produces today — no shape change, just a different host process.

### 8.3 `telahubd channels publish` subcommand

```
telahubd channels publish --channel <name> --files <dir> [--version vX.Y.Z] [--publish-dir <dir>] [--base-url <url>]
```

Scans `--files` for binaries, computes SHA-256s, writes `{publish-dir}/{channel}.json` and copies the files into `{publish-dir}/{channel}/files/`. If `--publish-dir` is omitted, uses the hub config's `channels.dir`. `--base-url` defaults to `{publicBase}/{channel}/files/` if `publicBase` is set.

This is the same publishing logic that lives in today's `telachand publish`, relocated. Operators with existing build scripts point them at the hub's `channels.dir` and swap `telachand publish` for `telahubd channels publish` in the pipeline.

### 8.4 Operator story

A hub operator who wants to host internal channels for their fleet:

1. Adds `channels.dir: /var/lib/tela/channels/` and `channels.publicBase: https://hub.example.com/channels/` to `/etc/tela/telahubd.yaml`.
2. Runs `telahubd service restart`.
3. Runs `telahubd channels publish --channel stable --files /build/output/` from their CI job to publish a new release.
4. Points agents at the hub's channel base: `telad channel sources set stable https://hub.example.com/channels/`.
5. Agents fetch manifests and binaries from the hub. No external internet access required past that.

Standalone channel hosting without a hub is not supported. Tela's architecture has a hub as the management plane; if you have no hub, you do not have a deployment.

## 9. Semver comparison details

`channel.IsNewer(a, b)` already implements Go's semver prerelease ordering (alphanumeric identifiers compared lexically in ASCII order; numeric-only identifiers compared numerically). The downgrade-protection gate reuses it directly: refuse when `!IsNewer(latest, current)` after establishing that `latest != current`.

Edge case: a `local.N`-suffixed binary compared against a `dev.N`-suffixed latest. `local > dev` alphabetically, so the local binary is "newer" and an `update` against dev refuses. The operator is expected to use `-channel dev -version vX.Y.N` to deliberately move to a specific dev build.

## 10. Migration and rollout

- Pre-1.0, no compat shim. The `manifestBase` field migration code described in 3.4 runs once per host and is deleted before the `0.12` beta tag.
- Every binary in the 0.12 release carries identical migration logic. Hub binaries, agent binaries, and client credstore loading all recognize the old shape and rewrite.
- CHANGELOG under `[Unreleased]` lists every user-visible change: new config field name, retired binary, new CLI subcommand, downgrade refusal.
- REFERENCE.md gains the new CLI surface. CONFIGURATION.md updates the update-block schema.
- Book chapter (`book/src/architecture/channel-sources.md` or equivalent) written after the implementation lands.

## 11. Implementation plan

The execution checklist lives on [issue #55](https://github.com/paulmooreparks/tela/issues/55). This design doc is the authoritative reference the checklist points at. Changes to the design happen here via PR; issue checklist items track work items against the agreed design.

## 12. Open questions

All resolved during the design conversation. This section retained for future changes — any reopening should first add its question here, then get resolved into the body.

## 13. Appendix: the abandoned `ApplyBasePatch` approach

A partial fix for #4 (`channel.ApplyBasePatch`, a helper that cleared `manifestBase` when switching to a built-in channel) was drafted and tested on the branch `4-telavisor-uses-its-own-manifestbase-when-resolving-remote-agenthub-channel-status` before this design replaced it. That branch has been deleted. The approach addressed one symptom while leaving the underlying data-model defect intact, and would have become dead code on landing the rework. The 12 table-driven test cases in `internal/channel/channel_test.go` for `ApplyBasePatch` are discarded with the branch.

No code from that branch ships.

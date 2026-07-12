# Release Process

Tela releases move through a three-channel pipeline: dev, beta, and stable.
The [Self-Update and Release Channels](../howto/channels.md) chapter covers
the user-facing side. This chapter covers the internal model for operators
and maintainers who need to cut a release, promote a channel, or issue a
hotfix, plus how to run a fully self-hosted channel server.

## Channels

A channel is a named pointer that resolves to a single tag. Self-update on
every Tela binary follows its configured channel.

| Channel | Purpose | Cadence | Audience | Risk |
|---------|---------|---------|----------|------|
| **dev** | Latest unstable build. Every push to `main` produces a new dev build. | Per commit | Maintainers, contributors, dogfood rigs | Highest. May break, may have half-finished features. |
| **beta** | Promoted dev builds ready for wider exposure. Cut by hand when a dev build is ready. | Days to weeks | Early adopters, staging deployments, dev hubs | Moderate. Real bugs surface here. |
| **stable** | Promoted beta builds that have been exercised in beta. The default for new installations after 1.0. | Weeks to months | Production deployments, public hubs, package managers | Low. Bug fixes only between minor versions. |

Pre-1.0, every binary defaults to `dev`. The channel mechanism works for
all three channels today, but `dev` is the appropriate default while the
project is moving fast. Post-1.0, TelaVisor and the Tela binaries default
to `stable`, and opting into beta or dev becomes a deliberate choice.

What changes at 1.0 is the meaning of `stable`, not its existence. Pre-1.0,
a stable tag is the build most ready for promotion, with no compatibility
promise. Post-1.0 it carries the backward-compatibility guarantees
described below.

Users change channel through TelaVisor's Updates tab, via the
`channel set` subcommand of any binary, or by editing the `update.channel`
field in the hub or agent YAML config.

## Tag Naming

Tela uses semantic versioning with prerelease suffixes for non-stable
channels.

| Channel | Tag Form | Example |
|---------|----------|---------|
| dev | `vMAJOR.MINOR.0-dev.PATCH` | `v0.16.0-dev.15` |
| beta | `vMAJOR.MINOR.0-beta.N` | `v0.16.0-beta.1` |
| stable | `vMAJOR.MINOR.PATCH` | `v0.16.0`, `v0.15.0` |

The `MAJOR.MINOR` portion comes from the `VERSION` file at the repository
root. It is the next stable version that maintainers are working toward.
When `VERSION` says `0.16`, dev builds are `v0.16.0-dev.N` and the next
stable will be `v0.16.0`.

After a stable release, `VERSION` is bumped to the next minor in a
follow-up commit to `main`. That push itself produces the new series' first
dev build (for example, `v0.17.0-dev.1`).

Semver compares prerelease versions in the correct order:

```
v0.16.0-dev.5 < v0.16.0-dev.15 < v0.16.0-beta.1 < v0.16.0-beta.3 < v0.16.0 < v0.16.1
```

## Promotion

Promotion is always manual. There is no automatic dev-to-beta or
beta-to-stable. A maintainer reviews what is on the source channel, decides
it is ready, and runs the promotion workflow.

Promotion happens via `.github/workflows/promote.yml`, triggered manually
with three inputs:

- **`source_tag`**: the existing tag being promoted (for example,
  `v0.16.0-dev.15`)
- **`target_channel`**: `beta` or `stable`
- **`target_version`**: required only for stable promotions (for example,
  `v0.16.0`)

The workflow validates the source tag, computes the new tag name
(auto-incremented for beta, maintainer-chosen for stable), creates the new
tag pointing at the same commit, and pushes it. The tag push triggers
`release.yml` to build and publish. Promotion never rebuilds from a
different commit: the beta and the dev build it came from are the same
source, and likewise for stable.

Everything flows forward: a fix lands on `main` first, soaks on dev, gets
promoted to beta, soaks again, gets promoted to stable. Hotfixes are the
one exception, covered below.

## Channel Manifests

Each channel has a JSON manifest hosted as a release asset on a rolling
`channels` GitHub Release. The manifest is the canonical answer to "what
version is current on this channel?"

```
https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
https://github.com/paulmooreparks/tela/releases/download/channels/beta.json
https://github.com/paulmooreparks/tela/releases/download/channels/stable.json
```

Schema:

```json
{
  "channel": "dev",
  "version": "v0.16.0-dev.15",
  "tag": "v0.16.0-dev.15",
  "publishedAt": "2026-07-11T12:00:00Z",
  "downloadBase": "https://github.com/paulmooreparks/tela/releases/download/v0.16.0-dev.15/",
  "binaries": {
    "tela-linux-amd64":       { "sha256": "abc...", "size": 12345678 },
    "tela-windows-amd64.exe": { "sha256": "def...", "size": 12345678 },
    "telad-linux-amd64":      { "sha256": "ghi...", "size": 12345678 }
  }
}
```

The schema is part of Tela's public API after 1.0. Adding new optional
fields is a minor-version change; renaming or removing existing fields is a
major-version change.

## What release.yml Does

The release workflow runs in three cases:

1. **Push to `main`**: produces a dev build, tagged
   `v{VERSION}.0-dev.{PATCH}`, and updates `dev.json`. The workflow has a
   paths filter, so pushes that touch only documentation do not produce a
   build; docs-only changes ride along with the next code-triggered tag.
2. **Push of a tag matching `v*-beta*`**: produces a beta build and updates
   `beta.json`.
3. **Push of a tag matching `v*` without a prerelease suffix**: produces a
   stable build and updates `stable.json`.

In all three cases the workflow builds Linux, macOS, and Windows binaries
for amd64 and arm64, generates SHA-256 checksums and the per-release
manifest, and creates or updates the GitHub Release for that tag. For
TelaVisor specifically, the workflow also builds `.deb` and `.rpm` packages
and a Windows NSIS installer; the CLI binaries (`tela`, `telad`,
`telahubd`) are distributed as plain executables. Every tag also rebuilds
the corresponding edition of this book.

## Cadence

Pre-1.0:

- **Dev**: every commit. No promise of stability.
- **Beta**: cut on demand when a dev build deserves wider exposure.
- **Stable**: cut on demand when a beta is ready. Pre-1.0 stable releases
  carry no backward-compatibility promise; that begins at `v1.0.0`.

Post-1.0:

- **Dev**: every commit.
- **Beta**: roughly every two weeks when there is meaningful work on
  `main`.
- **Stable**: patch releases as needed for bug fixes; minor releases
  roughly monthly when there is enough new functionality; major releases
  rare and deliberate, with a long beta phase and an upgrade guide.

These are guidelines, not promises.

## Backward-Compatibility Commitments

After 1.0:

- The wire protocol is frozen for `1.x`. Adding new optional fields is
  allowed; removing or renaming fields is a major-version change.
- The public CLI surface (command names, flag names, output formats) is
  frozen for `1.x`. Adding new commands or flags is allowed; removing them
  is a major-version change.
- The hub admin REST API is frozen for `1.x`. Adding new endpoints is
  allowed; removing or breaking existing ones is a major-version change.
- Config file schemas (`telahubd.yaml`, `telad.yaml`, `hubs.yaml`, profile
  YAML) are frozen for `1.x`. New optional fields are allowed; removing or
  renaming required fields is a major-version change.
- The channel manifest schema is frozen for `1.x`.

A bug fix to a `1.x` line will never introduce a breaking change. If a fix
requires breaking compatibility, it ships in `2.0`, not `1.x`.

Pre-1.0: nothing is frozen. Cruft and broken shapes are removed
aggressively.

## Deprecation Policy

When a feature is deprecated in a `1.x` release:

1. The feature continues to work unchanged in all subsequent `1.x`
   releases.
2. The deprecation is announced in the release notes and marked in the
   relevant docs.
3. The CLI emits a warning to stderr when the deprecated feature is used.
4. The feature is removed in the next major release (`2.0`).

A feature deprecated in `1.5` works in `1.5`, `1.6`, `1.7`, and is removed
in `2.0`.

## End-of-Life Policy

Each major version is supported with security fixes and critical bug fixes
for 12 months after the next major version ships. When `2.0` ships, `1.x`
continues to receive fixes for 12 months, after which only `2.x` is
supported. The end-of-life date for the previous major is announced in the
release notes for the new major.

## Quick Reference for Maintainers

**Cut a beta from a dev build:**

```
GitHub -> Actions -> Promote -> Run workflow
  source_tag:     v0.16.0-dev.15
  target_channel: beta
  target_version: (leave empty)
```

This creates `v0.16.0-beta.{N+1}` and triggers the beta release build.

**Cut a stable from a beta build:**

```
GitHub -> Actions -> Promote -> Run workflow
  source_tag:     v0.16.0-beta.3
  target_channel: stable
  target_version: v0.16.0
```

This creates `v0.16.0` and triggers the stable release build. After it
completes, bump `VERSION` to `0.17` in a follow-up commit so dev builds
start counting toward the next minor.

**Cut a hotfix:**

```bash
git checkout v0.16.0
git checkout -b hotfix/v0.16.x
git cherry-pick <fix-commit>
git tag v0.16.1
git push origin hotfix/v0.16.x v0.16.1
```

The tag push triggers a stable release build for `v0.16.1`. Then merge the
cherry-picked commit forward into `main` so it is not lost.

## Self-Hosted Channels on telahubd

Any `telahubd` hub can serve release channel manifests and binary downloads
in-process. Enable the `channels:` block in `telahubd.yaml` and the hub
mounts public `/channels/` routes alongside the rest of its HTTP surface.
The wire format matches the GitHub-hosted channels exactly, so clients
pointed at a self-hosted channel with `update.sources[<name>]` fetch and
verify manifests through the same code path they use for the public
channels.

Use cases:

- Air-gapped or firewall-restricted networks where GitHub is unreachable
- Distributing custom or private builds that never enter the public
  pipeline
- Staging a release internally before pushing it to the public channel
- Developer workflows where every local build becomes immediately available
  for self-update across a local fleet

### Enable the Channel Server

Add a `channels:` block to `telahubd.yaml`:

```yaml
# telahubd.yaml
channels:
  enabled: true
  data: /var/lib/telahubd/channels
  publicURL: https://hub.example.net/channels
```

| Field | Purpose |
|-------|---------|
| `enabled` | Mount `/channels/` routes when true |
| `data` | Directory holding `{channel}.json` files at the root and binaries under `files/` |
| `publicURL` | External URL prefix written into generated manifests. Used by `telahubd channels publish` as the `downloadBase` source. |

Restart the hub. The new routes are:

- `GET /channels/{name}.json`: channel manifest
- `GET /channels/files/`: directory listing of all channels
- `GET /channels/files/{channel}/`: directory listing of one channel
- `GET /channels/files/{channel}/{binary}`: binary download
- `GET /channels/`: health and status JSON

Each channel has its own subdirectory under `files/`, so parallel publishes
to different channels do not overwrite each other.

The endpoints are public (no auth, wildcard CORS) by design. Release
manifests are world-readable. Do not place anything in `channels.data` that
you would not want served.

### Populate the Files Directory

Drop binaries into `{data}/files/{channel}/` using the same naming
convention as GitHub release assets:

```
{data}/files/
  dev/
    tela-linux-amd64
    tela-windows-amd64.exe
    telad-linux-amd64
    ...
  local/
    tela-linux-amd64
    ...
```

Only include the binaries you want to distribute on each channel. The
manifest lists whatever is present in that channel's directory; clients
look up their own platform entry.

### Publish a Manifest

After placing binaries, generate the manifest:

```bash
telahubd channels publish -channel dev -tag v0.16.0-dev.15
```

Output:

```
  tela-linux-amd64            a1b2c3d4e5f6...  12345678 bytes
  tela-windows-amd64.exe      b2c3d4e5f6a1...  13456789 bytes
  ...

published dev channel manifest
  tag:      v0.16.0-dev.15
  binaries: 9
  base:     https://hub.example.net/channels/files/
  manifest: /var/lib/telahubd/channels/dev.json
```

The manifest is live immediately, with no hub restart. Each channel has its
own manifest; you can maintain the three standard channels and any named
custom channels simultaneously.

The manifest tag is arbitrary. For local dev builds, a short git hash or a
timestamp works, since `dev`-versioned binaries always update regardless of
semver comparison.

### Publishing from a Separate Build Machine

`telahubd channels publish` runs on the same host as the hub and reads
`channels.data` from the hub's config file. When your build pipeline lives
elsewhere, use the HTTPS admin API instead:

- `PUT /api/admin/channels/files/{channel}/{binary}` uploads a file into
  `channels.data/files/{channel}/`. The request body is the file bytes.
  Owner or admin token required. 500 MiB max per file.
- `POST /api/admin/channels/publish` with `{"channel":"...","tag":"..."}`
  hashes everything under `channels.data/files/{channel}/` and writes the
  manifest. It returns the manifest JSON for verification.

Upload each binary, then call `/publish` once. No SSH, tunnel, or
file-share mount is needed on the build host.

Reference implementations live under `scripts/` in the tela repo:
`scripts/publish-channel.ps1` (PowerShell, for Windows) and
`scripts/publish-channel.sh` (bash, for Linux and macOS). Both
cross-compile the CLI binaries, bundle TelaVisor via `wails build`, and run
the upload and publish round-trip against any hub with channels hosting
enabled. Configuration comes from `scripts/publish.env` (gitignored):

```
TELA_PUBLISH_HUB_URL=https://hub.example.net
TELA_PUBLISH_TOKEN=<owner-or-admin-token>
```

Get the owner token with `telahubd user show-owner` on the hub. See
`scripts/publish.env.example` for all supported keys.

Two pitfalls worth knowing:

- **Version string vs. code version.** A binary's version string is set at
  build time and does not prove which features its code has. If a publish
  returns 404 against a hub whose banner reports a version that should
  have the channels admin API, the hub is probably running a binary built
  from an older commit; update the hub binary first. A quick check that
  the endpoint exists: an unauthenticated
  `POST /api/admin/channels/publish` should return 401, not 404.
- **Counter drift.** The per-channel build counter in
  `scripts/{channel}-build-counter` increments on every run, including
  failed ones, so version tags never collide even across failed attempts.
  This is intentional.

### Point Binaries at the Self-Hosted Channel

Each binary has a `channel sources` subcommand that writes into its
config's `update.sources` map:

```bash
telad channel sources set dev https://hub.example.net/channels/
telahubd channel sources set dev https://hub.example.net/channels/
tela channel sources set dev https://hub.example.net/channels/
```

Or edit the YAML directly:

```yaml
# telad.yaml, telahubd.yaml, or credentials.yaml
update:
  channel: dev
  sources:
    dev: https://hub.example.net/channels/
```

After this, `tela update`, `telad update`, `telahubd update`, and the
TelaVisor update buttons all pull from your hub. Verify with
`tela channel`, which prints the manifest URL it will use.

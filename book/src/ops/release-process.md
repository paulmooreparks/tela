# Release process

This chapter covers how Tela releases are produced, channeled, and promoted. If you are a user, the [Self-update and release channels](../howto/channels.md) chapter is the practical guide; this chapter is the internal model for operators and maintainers cutting a release.

## Channels

Tela ships through three release channels. A channel is a named pointer that resolves to a single tag. Self-update on every Tela binary follows its configured channel.

| Channel | Purpose | Cadence | Audience | Risk |
|---------|---------|---------|----------|------|
| **dev** | Latest unstable build. Every push to `main` produces a new dev build. | Per commit | Maintainers, contributors, dogfood rigs | Highest. May break, may have half-finished features. |
| **beta** | Promoted dev builds that have soaked. Cut by hand when a dev build has been clean for a while. | Days to weeks | Early adopters, staging deployments, dev hubs | Moderate. Real bugs surface here. |
| **stable** | Promoted beta builds that have been exercised in beta. The default for new installations after 1.0. | Weeks to months | Production deployments, public hubs, package managers | Low. Bug fixes only between minor versions. |

Pre-1.0, every binary defaults to `dev`. The channel mechanism works for all three channels today, but `dev` is the appropriate default while the project is moving fast and `stable` is not yet the load-bearing public face it will be after 1.0.

Post-1.0, TelaVisor and the Tela binaries default to `stable`. New installations get the conservative line by default; opting into beta or dev becomes a deliberate choice.

What changes at 1.0 is the meaning of `stable`, not its existence. Pre-1.0, a stable tag is "the most soaked thing we have, with no compatibility promise yet." Post-1.0 it carries the backward-compatibility guarantees described below.

Users can change channel through TelaVisor's Application Settings, through `tela channel set <name>`, or by editing the `update.channel` field in their hub or agent YAML config.

## Tag naming

Tela uses semantic versioning with prerelease suffixes for non-stable channels.

| Channel | Tag form | Example |
|---------|----------|---------|
| dev | `vMAJOR.MINOR.0-dev.PATCH` | `v0.4.0-dev.42` |
| beta | `vMAJOR.MINOR.0-beta.N` | `v0.4.0-beta.3` |
| stable | `vMAJOR.MINOR.PATCH` | `v0.4.0`, `v0.4.1`, `v1.0.0` |

The `MAJOR.MINOR` portion comes from the `VERSION` file at the repository root. It is the next stable version that maintainers are working toward. When `VERSION` says `0.4`, dev builds are `v0.4.0-dev.N` and the next stable will be `v0.4.0`.

After cutting a stable release, bump `VERSION` to the next minor (for example, `0.4` to `0.5`). This resets the dev counter for the next development cycle.

Semver compares prerelease versions in the correct order:

```
v0.4.0-dev.5 < v0.4.0-dev.42 < v0.4.0-beta.1 < v0.4.0-beta.3 < v0.4.0 < v0.4.1
```

## Branches

Three branches mirror the three channels.

| Branch | Channel | Who can push | Trigger |
|--------|---------|--------------|---------|
| `main` | dev | Maintainers, contributors via PR | Auto-tag on every push, builds dev release |
| `beta` | beta | Maintainers only, fast-forward only | Tag push triggers a beta release build |
| `release` | stable | Maintainers only, fast-forward only | Tag push triggers a stable release build |

Branches flow forward only: `main` to `beta` to `release`. A fix lands on `main` first, soaks, gets promoted to `beta`, soaks again, gets promoted to `release`. There is no shortcut.

Hotfixes are the exception. If a critical bug is in a stable release, a fix can be cherry-picked from `main` directly to a `hotfix/v0.4.x` branch off the stable tag, tagged as `v0.4.1`, and immediately released. The same fix must then be merged forward into `beta` and `main` to prevent drift.

The `beta` and `release` branches exist so anyone reading the GitHub branch list can see what is currently on each channel, but `promote.yml` does not require them: it tags commits directly. The forward-only flow is policy; the branches are bookkeeping.

## Promotion

Promotion is always manual. There is no automatic dev-to-beta or beta-to-stable. A maintainer reviews what is on the source channel, decides it is ready, and runs the promotion workflow.

Promotion happens via `.github/workflows/promote.yml`, triggered manually with three inputs:

- **`source_tag`** -- the existing tag being promoted (e.g. `v0.4.0-dev.42`)
- **`target_channel`** -- `beta` or `stable`
- **`target_version`** -- required only for stable promotions (e.g. `v0.4.0`)

The workflow validates the source tag, computes the new tag name (auto-incremented for beta, user-chosen for stable), creates the new tag pointing at the same commit, and pushes it. The tag push triggers `release.yml` to build and publish.

## Channel manifests

Each channel has a JSON manifest hosted as a release asset on a special `channels` GitHub Release. The manifest is the canonical answer to "what version is current on this channel?"

```
https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
https://github.com/paulmooreparks/tela/releases/download/channels/beta.json
https://github.com/paulmooreparks/tela/releases/download/channels/stable.json
```

Schema:

```json
{
  "channel": "dev",
  "version": "v0.4.0-dev.42",
  "tag": "v0.4.0-dev.42",
  "publishedAt": "2026-04-08T12:00:00Z",
  "downloadBase": "https://github.com/paulmooreparks/tela/releases/download/v0.4.0-dev.42/",
  "binaries": {
    "tela-linux-amd64":       { "sha256": "abc...", "size": 12345678 },
    "tela-windows-amd64.exe": { "sha256": "def...", "size": 12345678 },
    "telad-linux-amd64":      { "sha256": "ghi...", "size": 12345678 }
  }
}
```

The schema is part of Tela's public API after 1.0. Adding new optional fields is a minor-version change; renaming or removing existing fields is a major-version change.

## What `release.yml` does

The release workflow runs in three cases:

1. **Push to `main`** -- produces a dev build, tagged `v{VERSION}.0-dev.{PATCH}`, and updates `dev.json`.
2. **Push of a tag matching `v*-beta*`** -- produces a beta build and updates `beta.json`.
3. **Push of a tag matching `v*` without a prerelease suffix** -- produces a stable build and updates `stable.json`.

In all three cases the workflow builds Linux, macOS, and Windows binaries for amd64 and arm64, generates SHA256 checksums and the per-release manifest, and creates or updates the GitHub Release for that tag. For TelaVisor specifically, the workflow also builds `.deb` and `.rpm` packages and a Windows NSIS installer; the CLI binaries (`tela`, `telad`, `telahubd`) are distributed as plain executables only.

## Cadence

Pre-1.0:

- **Dev**: every commit. No promise of stability.
- **Beta**: cut on demand when a dev build deserves wider exposure. No fixed cadence.
- **Stable**: cut on demand when a beta has soaked. Pre-1.0 stable releases carry no backward-compatibility promise -- that begins at `v1.0.0`. Use them as "the most-soaked thing we have," not as a long-term support line.

Post-1.0:

- **Dev**: every commit.
- **Beta**: roughly every two weeks when there is meaningful work on `main`.
- **Stable**: patch releases as needed for bug fixes; minor releases roughly monthly when there is enough new functionality; major releases rare, deliberate, with a long beta phase and an upgrade guide.

These are guidelines, not promises.

## Backward-compatibility commitments

After 1.0:

- The wire protocol is frozen for `1.x`. Adding new optional fields is allowed; removing or renaming fields is a major-version change.
- The public CLI surface (command names, flag names, output formats) is frozen for `1.x`. Adding new commands or flags is allowed; removing them is a major-version change.
- The hub admin REST API is frozen for `1.x`. Adding new endpoints is allowed; removing or breaking existing ones is a major-version change.
- Config file schemas (`telahubd.yaml`, `telad.yaml`, `hubs.yaml`, profile YAML) are frozen for `1.x`. New optional fields are allowed; removing or renaming required fields is a major-version change.
- The channel manifest schema is frozen for `1.x`.

A bug fix to a `1.x` line will never introduce a breaking change. If a fix requires breaking compatibility, it ships in `2.0`, not `1.x`.

Pre-1.0: nothing is frozen. Cruft and broken shapes are removed aggressively.

## Deprecation policy

When a feature is deprecated in a `1.x` release:

1. The feature continues to work unchanged in all subsequent `1.x` releases.
2. The deprecation is announced in the release notes and marked in the relevant docs.
3. The CLI emits a warning to stderr when the deprecated feature is used.
4. The feature is removed in the next major release (`2.0`).

A feature deprecated in `1.5` works in `1.5`, `1.6`, `1.7`, and is removed in `2.0`.

## End-of-life policy

Each major version is supported with security fixes and critical bug fixes for 12 months after the next major version ships. When `2.0` ships, `1.x` continues to receive fixes for 12 months, after which only `2.x` is supported. The end-of-life date for the previous major is announced in the release notes for the new major.

## Quick reference for maintainers

**Cut a beta from a dev build:**

```
GitHub -> Actions -> Promote -> Run workflow
  source_tag:     v0.4.0-dev.42
  target_channel: beta
  target_version: (leave empty)
```

This creates `v0.4.0-beta.{N+1}` and triggers the beta release build.

**Cut a stable from a beta build:**

```
GitHub -> Actions -> Promote -> Run workflow
  source_tag:     v0.4.0-beta.3
  target_channel: stable
  target_version: v0.4.0
```

This creates `v0.4.0` and triggers the stable release build. After it completes, bump `VERSION` to `0.5` in a follow-up commit so dev builds start counting toward the next minor.

**Cut a hotfix:**

```bash
git checkout v0.4.0
git checkout -b hotfix/v0.4.x
git cherry-pick <fix-commit>
git tag v0.4.1
git push origin hotfix/v0.4.x v0.4.1
```

The tag push triggers a stable release build for `v0.4.1`. Then merge the cherry-picked commit forward into `main` so it is not lost.

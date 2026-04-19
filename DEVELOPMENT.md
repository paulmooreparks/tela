# Development workflow

This file captures how Tela is developed: branching, PRs, issue tracking, dogfooding via custom channels, and commit conventions. It is maintainer-facing. Outside contributors should read [CONTRIBUTING.md](CONTRIBUTING.md) first; this document augments it with the internal process that runs alongside.

The rules here are guidelines, not tripwires. The aim is to cut the probability of shipping a bad commit to `main` without turning solo development into a ceremony.

## Solo-maintainer assumptions

Today the only person with push access is `@paulmooreparks`. `main` is not branch-protected on GitHub. No PR-review gate. This document is the discipline.

If another maintainer joins, revisit: branch protection, required CI, required review, and whether the "trivial changes land direct" rule should stay.

## Branching model

Three long-lived branches from [RELEASE-PROCESS.md](RELEASE-PROCESS.md):

- `main` — dev channel, every commit builds a `v{VERSION}.0-dev.N` release via GitHub Actions.
- `beta` — beta channel, fast-forward only, tagged promotions.
- `release` — stable channel, fast-forward only, tagged promotions.

Short-lived branches for development work:

- `feature/<slug>` — new capability or cross-cutting refactor.
- `fix/<slug>` — non-trivial bug fix (see rubric below).
- `docs/<slug>` — documentation-only work that is too large to land as a drive-by.

Slugs use kebab-case and should be short (`feature/session-mux`, `fix/hub-config-discovery`, not `feature/multiplex-all-agent-wireguard-sessions-over-a-single-control-websocket`).

## When to branch, PR, and dogfood

| Change shape | Branch? | PR? | Custom channel dogfood? |
|---|---|---|---|
| Typo / one-liner / doc sweep touching one file | no | no | no |
| Bug fix, <30 lines, one package, no protocol or wire-format change | optional | optional | no |
| Bug fix touching protocol, wire format, or more than one package | **yes** | **yes** | **yes (at least `local`)** |
| Feature or refactor of any size | **yes** | **yes** | **yes** |
| Architectural experiment (new transport, new auth model, new storage backend) | **yes** | **yes** | **yes, in its own named feature channel** |

The rubric biases toward "when in doubt, branch and PR." A PR against yourself is still valuable: it forces you to read the whole diff as one unit, drops a reviewable surface if anyone else ever looks at the repo, and gives a natural home for the "Fixes #N" link.

## Why custom channels matter here

The publish-dev.ps1 script already treats `.vscode/channel.txt` as the active channel name and writes binaries to `channels/{channel}/files/` with a per-channel build counter. The channel name flows into the binary's version string (`v{VERSION}.0-{channel}.{N}`) and into the manifest's `downloadBase`.

That infrastructure is what lets you dogfood a feature branch without touching `main`'s dev channel:

1. Cut `feature/session-mux` from `main`.
2. Edit `.vscode/channel.txt` to `session-mux`.
3. Run the build task. First run auto-creates `.vscode/session-mux-build-counter`, `channels\session-mux\files\`, and `channels\session-mux.json`, with binaries tagged `v0.11.0-session-mux.1`.
4. On any test hub/agent: `telahubd channel set session-mux -manifest-base https://parkscomputing.com/content/tela/channels/` (same pattern for telad and tela).
5. Run `telahubd update`. The box pulls the feature-branch binary.
6. Soak for a day or a week, depending on the risk.
7. If it survives, squash-merge the branch to `main`. Delete `channels/session-mux/` and `.vscode/session-mux-build-counter`; retire the channel.
8. If it dies (see the `mux` experiment in commits `efeafac` → `14a731f`), throw away the branch and the channel. `main`'s dev channel is untouched and nothing downstream of you had to see the broken code.

Remember to switch `.vscode/channel.txt` back to `local` (or whatever you usually run on) before your next general-purpose build, otherwise you'll keep bumping the feature-branch counter for unrelated work.

## Local build tags (optional but useful)

`publish-dev.ps1` already embeds a unique version string (`v{VERSION}.0-{channel}.{N}`) into the binary via `-ldflags`. You can also create a matching git tag locally:

```bash
git tag v0.11.0-local.31
```

Lightweight tag, no annotation, never pushed. Benefits:

- `git checkout v0.11.0-local.29` to roll back.
- `git log v0.11.0-local.29..v0.11.0-local.30 --oneline` to see what is in a build.
- `git bisect` between two known-good/bad builds.

Rules:

- Use `git push` without `--tags` or `--follow-tags`. The default only pushes commits, not tags. Local tags stay local.
- Filter your listings: `git tag -l | grep -v '\-[a-z].*\.[0-9]'` to hide local-channel tags, or `git tag -l '*-local.*'` to see just them.

If you want this automated, add `git tag $TAG` at the end of `publish-dev.ps1` after the manifest is written.

## Issue tracking on GitHub

Every non-trivial change gets an issue first, even if the issue and the fix ship in the same session. Issues are the durable record; commit messages are too terse to carry context, and the book doesn't cover bugs at all.

### Filing

`gh issue create --title "..." --label "type:X,component:Y" --body "..."`

Bodies should include: symptom, reproduction if applicable, root cause or suspected root cause, what needs to change, and acceptance criteria. See [#4](https://github.com/paulmooreparks/tela/issues/4) and [#5](https://github.com/paulmooreparks/tela/issues/5) for templates.

### Label taxonomy

Three orthogonal axes. Issues should carry one `type:*`, one or more `component:*`, and optionally a `priority:*` (absence means normal priority).

**Type** (what kind of change):

| Label | Use when |
|---|---|
| `type:bug` | Something that used to work or was expected to work, does not. |
| `type:feature` | New capability or enhancement to existing behavior. |
| `type:refactor` | Code restructuring with no intended behavior change. |
| `type:docs` | Book, repo-root .md files, CLI usage strings, DESIGN docs. |
| `type:infra` | CI, build scripts, release process, dev tooling. |

**Component** (what it touches). Pick all that apply; cross-cutting changes may have several.

- `component:tela` — tela client CLI
- `component:telad` — telad agent daemon
- `component:telahubd` — telahubd hub server
- `component:telavisor` — TelaVisor desktop GUI
- `component:awansaya` — Awan Saya portal (cross-repo; issues about AS tracked here when they affect the tela protocol)
- `component:book` — mdBook under `book/`
- `component:protocol` — wire protocol, channel manifest schema, portal spec

**Priority:**

- `priority:critical` — ship-blocker, a 1.0 gate, or a production outage. Drop everything.
- `priority:high` — next in queue, before starting any new work.
- (no label) — normal. Default.
- `priority:low` — would be nice, no urgency.

Kept from GitHub defaults: `good first issue`, `help wanted`, `duplicate`, `wontfix`.

Removed from defaults: `bug`, `enhancement`, `documentation`, `invalid`, `question`. Replaced by the `type:*` set above.

### Linking commits and issues

Use `Fixes #N`, `Closes #N`, or `Refs #N` in commit messages or PR descriptions. `Fixes`/`Closes` on a commit that lands on `main` auto-closes the issue when pushed.

## PR conventions

When a branch is PR-worthy by the rubric above:

1. Open the PR as draft if the work is mid-stream, ready-for-review once the diff reflects the intended change.
2. Title follows Conventional Commits shape used on `main`: `feat:`, `fix:`, `docs:`, `refactor:`, `chore:`, `perf:`. The shape matches the final commit message, because squash-merge preserves the PR title.
3. Description: what, why, how to test, any issue it closes. A screenshot helps for UI changes.
4. Squash-merge to `main` by default. Preserve individual commits only when the branch genuinely benefits from history (rare for short-lived branches).
5. Delete the branch after merging.

## Commit-message style on `main`

From the existing history:

- `feat:` for new capabilities (`feat: agent channel subcommand, TDL sidebar badges, channel source edit/remove`).
- `fix:` for bug fixes (`fix: revert mux, improve UDP relay health check`).
- `docs:` for documentation-only changes (`docs: prep book and changelog for v0.10.1 stable`).
- `refactor:` for code restructure without behavior change.
- `chore:` for dependency bumps and the like.

Short imperative summary on the first line. Wrap the body at 72-ish columns. Mention the issue being fixed. Keep the `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` trailer on Claude-assisted commits.

## The revert / mux case study

The recent mux experiment is the canonical "why bother with branches" story. [efeafac](https://github.com/paulmooreparks/tela/commit/efeafac) landed a big transport-layer change straight on `main` — multiplexing all agent WireGuard sessions over a single control WebSocket. It broke real things in dogfood, and [14a731f](https://github.com/paulmooreparks/tela/commit/14a731f) reverted it, which then required [c4c01c0](https://github.com/paulmooreparks/tela/commit/c4c01c0) to scrub the documentation references the book had already accumulated about the (no-longer-existent) feature.

Post-mortem: this was a textbook "architectural experiment" by the rubric above. With the current workflow:

1. Branch: `feature/session-mux`.
2. Channel: `session-mux` (new directory and manifest on parkscomputing.com).
3. Point the dev hub at the session-mux channel. Run for a week.
4. The breakage shows up in dogfood, on that channel, not on `main`'s dev channel.
5. Branch gets discarded; `channels/session-mux/` directory gets deleted; `main` was never touched.

Total saved work: the revert commit, the doc-scrub commit, and the mental load of reasoning about what's in `main` that shouldn't be.

## When discipline can relax

- Release-time chores (CHANGELOG edits, version bumps, book intro edits that precede a stable tag) land directly on `main`. The promotion workflow depends on them being there.
- Typo fixes discovered while editing a file for another reason can ride along.
- Anything that would need a branch longer to create and merge than the actual change takes to write.

## When discipline tightens

Three situations where the rules above are not enough:

1. **Anything on the wire-protocol surface.** Even a one-line change to the channel manifest schema, a WebSocket message type, or the relay framing: branch, PR, custom channel dogfood on at least one real hub-agent-client triple before merging.
2. **Anything that touches auth or token handling.** Same reason: silent security regressions are the worst kind.
3. **Anything that changes a YAML config file's shape.** Operators notice these. Branch + PR + update CHANGELOG + update [CONFIGURATION.md](CONFIGURATION.md) in the same commit.

## API-first: the discipline side

The architectural rule (`CLAUDE.md > API-first, CLI-second, UI-last`) has a matching discipline. The rule says every feature ships as a typed daemon API first, with the CLI and UI layered over. The discipline:

- When adding a TelaVisor feature, start by asking "what API call produces this data?" or "what API call writes this change?" If the answer is "there isn't one and I can scrape the CLI's output," stop. File an issue for the API gap against the relevant daemon (`component:telahubd`, `component:telad`, or `component:protocol`). Wait for the API to land, then build the TelaVisor feature against it.
- Same rule when adding a `tela admin` command against a remote hub or agent: if you cannot implement it through the admin HTTP API or the agent mgmt-action surface, file an API-gap issue first.
- CLI-only parsing of CLI output is also forbidden. If `tela admin access` needs to know what the hub knows, it calls a hub endpoint. Not another CLI wrapped in `exec.Command`.
- Reading a daemon's log files to discover state is never the answer. If something is worth knowing, it is worth an endpoint.

This rule exists because the alternative — TelaVisor or `tela` commands scraping tool output — has failed in every project that has tried it. Output formats shift, CLIs become load-bearing APIs by accident, and every cross-tool interaction becomes fragile. The rule is strict on purpose. When in doubt, propose the API endpoint first and ask.

**When in doubt, file an issue labeled `type:feature` + the relevant `component:*` and call out the API gap in the body.** This also forces the daemon's API surface to stay first-class, which is what makes the `tela` CLI viable as the fleet-management tool it is becoming.

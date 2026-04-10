# CLAUDE.md - Tela Project Context

## Onboarding

At the start of each conversation, read these files to understand the project
context, current status, and design direction:

| File | Purpose |
|------|---------|
| `CLAUDE.md` | Build commands, coding conventions, API style, review items |
| `DESIGN.md` | Architecture, protocol spec, component design, roadmap |
| `DESIGN-remote-admin.md` | Agent/hub management protocol, implementation status |
| `DESIGN-file-sharing.md` | File sharing protocol and implementation |
| `DESIGN-portal.md` | Portal protocol specification (the wire contract every Tela portal must implement) |
| `STATUS.md` | Traceability matrix mapping design sections to implementation |
| `book/src/guide/telavisor.md` | TelaVisor desktop client (canonical chapter, comprehensive). The repo-root `TelaVisor.md` is a short overview pointing here. |
| `TELA-DESIGN-LANGUAGE.md` | Visual design language shared across all Tela products |
| `ACCESS-MODEL.md` | Token-based RBAC, permissions, ACL model |
| `CONFIGURATION.md` | Config file formats for all three binaries |
| `REFERENCE.md` | CLI reference for tela, telad, and telahubd |
| `RELEASE-PROCESS.md` | Channel model (dev/beta/stable), promotion, manifests |
| `ROADMAP-1.0.md` | 1.0 readiness checklist (blockers, important, polish) |
| `TODO.md` | Current task list and priorities |

Do not read all files upfront in every conversation. Read `CLAUDE.md` always.
Read the others when the conversation topic requires them (e.g., read
`DESIGN-remote-admin.md` when working on agent management, read
`book/src/guide/telavisor.md` when working on the GUI). Use your judgment
to decide which files are relevant.

## Project Overview

Tela is a FOSS encrypted remote-access fabric using WireGuard tunnels.
Three binaries, one Go module (`github.com/paulmooreparks/tela`):

| Binary | Path | Role |
|--------|------|------|
| `tela` | `cmd/tela/` | Client CLI (connects to machines via hub, mounts file shares) |
| `telad` | `cmd/telad/` | Agent daemon (registers machines with hub, exposes local services) |
| `telahubd` | `cmd/telahubd/` | Hub server (HTTP + WebSocket + UDP relay on a single port) |

Supporting packages:
- `internal/channel/` -- Release channel manifest fetcher, validator, and SHA-256 stream verifier. Used by every binary's self-update path.
- `internal/credstore/` -- User-level credential store (`~/.tela/credentials.yaml` on Unix, `%APPDATA%\tela\credentials.yaml` on Windows). Stores hub tokens and the client's own channel preference.
- `internal/service/` -- Cross-platform OS service management (Windows SCM, systemd, launchd)
- `internal/telelog/` -- Logging primitives shared across binaries
- `internal/wsbind/` -- WebSocket transport binding helpers
- `console/` -- Embedded static files for the hub's web console
- `cmd/telagui/` -- TelaVisor desktop GUI (Wails v2: Go backend + vanilla JS frontend)

GitHub Actions workflows:
- `.github/workflows/ci.yml` -- Build, vet, test, gofmt, mod tidy, cross-compile sanity check on every push and PR
- `.github/workflows/release.yml` -- Build matrix that produces dev/beta/stable releases. Compute-version job at the top reserves the tag atomically, downstream jobs all build against it. Publishes to a rolling `channels` GitHub Release.
- `.github/workflows/promote.yml` -- Manual `workflow_dispatch` that promotes a dev tag to beta or a beta tag to stable, then invokes release.yml via `workflow_call` to build the new tag.

Related project: **Awan Saya** (`c:\Users\paul\source\repos\awansatu`) -- portal/registry that discovers and lists hubs. Changes to tela's auth, portal sync, or API contracts may affect Awan Saya.

## Wirint Style

- Do not use emdash
- Do not use a style of writing that would even require emdash or semicolons.
- Do not use curly quotes, single or double. Use ' or " as appropriate instead.
- Do not use a salesy, marketing style of writing, unless instructed to do so. Simply be factual instead.
- Write in the style of a technical writer producing exact documentation, unless instructed otherwise.
- Be very clear and thorough in technical writing. Do not leave out steps.
- Spell out the first abbeviation usage in a document unless you can be reasonably sure the abbreviation is known in context (e.g., TCP and UDP are known, FOSS is not necessarily known)

## Editorial Workflow (markdown sweeps)

The user acts as editor on all markdown files in this repo (the mdBook under
`book/`, plus top-level docs like `README.md`, `REFERENCE.md`, `DESIGN.md`,
etc.) by leaving inline HTML comments tagged with an `EDIT:` marker. These
comments are invisible in rendered output but greppable in source.

When the user says "sweep edits" (or similar: "act on the EDIT notes",
"process editorial notes", optionally scoped to a path), do this:

1. Grep for `EDIT:` across the requested scope (default: whole repo).
2. Work through each note in file order. For each one:
   - Read enough surrounding context to understand the intent.
   - Make the change in the prose.
   - Delete the `<!-- EDIT: ... -->` comment in the same edit.
   - For block-level rewrites delimited by `EDIT/REWRITE START` /
     `EDIT/REWRITE END`, replace everything between the markers (inclusive
     of the markers) with the rewritten prose.
3. If a note is ambiguous, contradicts another note, or asks a question
   (`EDIT/ASK:`), stop and ask the user before changing anything. Do not
   guess.
4. Apply the project's writing style rules (no emdash, no curly quotes, no
   marketing tone, factual technical-writer voice) to the rewritten prose,
   even if the original violated them.
5. Report which notes were acted on and which were skipped (and why) at the
   end of the sweep.

Marker conventions the user will use:

| Marker | Meaning |
|--------|---------|
| `<!-- EDIT: ... -->` | General note, change as described |
| `<!-- EDIT/REWRITE: ... -->` | Replace the adjacent paragraph as described |
| `<!-- EDIT/REWRITE START ... --> ... <!-- EDIT/REWRITE END -->` | Replace everything between the markers |
| `<!-- EDIT/CUT: ... -->` | Delete the adjacent block; reason follows |
| `<!-- EDIT/MOVE: ... -->` | Relocate the adjacent block; destination follows |
| `<!-- EDIT/FACT: ... -->` | Verify a factual claim against the code or other docs |
| `<!-- EDIT/TONE: ... -->` | Rewrite for tone without changing meaning |
| `<!-- EDIT/ASK: ... -->` | Question for the assistant; answer, do not silently change |
| `<!-- EDIT/TODO: ... -->` | Work to be done that the user has not yet specified fully |

Do not add `EDIT:` notes yourself. They are an editorial channel from user
to assistant, not the other way around. If you have questions about prose,
ask in chat.

## Documentation: book is canonical, repo-root files are overviews

Tela's documentation has been deliberately inverted. The book under `book/`
is the canonical, comprehensive, narrative source for everything about
how to set up, configure, run, use, and operate Tela. Repo-root markdown
files (`README.md`, `TelaVisor.md`, the `DESIGN-*.md` family, etc.) are
*either* short overviews that point at the book, *or* tabular reference
data with a legitimate non-book audience (`REFERENCE.md`, `CONFIGURATION.md`,
`ACCESS-MODEL.md`, `CHANGELOG.md`, `SECURITY.md`, `CONTRIBUTING.md`).

The convention:

- **Narrative documentation lives in `book/src/`.** Chapters are written
  natively in book voice with their own structure, screenshots, and
  worked examples. They are not generated from anywhere else and do not
  use `{{#include}}` to pull from a repo-root file.
- **Reference data lives at the repo root.** Tabular CLI references,
  configuration schemas, access model formal definitions, and process
  docs (changelog, security, contributing) stay as canonical files at
  the repo root and continue to be `{{#include}}`-d into the book where
  the book wants them.
- **Repo-root overview files are short on purpose.** They point at the
  book chapter for the substance and contain only what a code reader
  needs to know without opening the book: a one-paragraph description,
  pointers to where things live in the source tree, build instructions,
  license note. If an overview is growing past 50-60 lines, the new
  content probably belongs in the book instead.
- **Screenshots referenced by book chapters live in `book/src/screens/`**
  so mdBook copies them into the published site automatically. Book
  chapters reference them as relative paths (`../screens/foo.png`).
  Repo-root files that need the same image use a relative path from
  their location (`book/src/screens/foo.png`). No absolute GitHub raw
  URLs except as a last resort for files that need to render outside
  of any project context.
- **When a feature changes**, update the book chapter, then check
  whether the repo-root overview needs an edit too. If you find
  yourself making the same edit in both places, the overview is too
  long and should be slimmed down to point at the book instead.

The inversion is gradual. Files are moved into the book as they need
substantial updates, not in a big-bang migration. Today, TelaVisor is
the prototype: the book chapter at `book/src/guide/telavisor.md` is the
canonical reference, and `TelaVisor.md` at the repo root is an overview
pointing at it. Other narrative files (`USE-CASES.md`, `IMPLEMENTATION.md`,
the narrative parts of `DESIGN.md`) follow the same pattern when they
get touched next.

## Build & Verify

```bash
go build ./...          # Build all three binaries
go vet ./...            # Static analysis
go test ./...           # Run unit tests
gofmt -l .              # Should print nothing -- the tree must be gofmt-clean
```

All three binaries must compile cleanly after any change. Always run all four
checks before considering work complete. CI runs the same set on Linux with
`-race` enabled; race-clean tests are required for merge.

To rebuild TelaVisor specifically (the JS/HTML/CSS get bundled into the Go
binary at build time, not at runtime, so editing the frontend requires a
rebuild):

```bash
cd cmd/telagui && wails build
```

The output is at `cmd/telagui/build/bin/telavisor.exe` (Windows) or the
equivalent path on other platforms.

## Guiding Principles

### Pre-1.0: no cruft, no backward compatibility

Tela has not shipped 1.0 yet. There is no backward compatibility burden, and
there will not be one until 1.0. Until then:

- **Delete duplicate code paths.** When a new shape replaces an old one,
  remove the old one in the same change. Do not leave the old behind "for
  now," do not mark it deprecated, do not keep it as a fallback.
- **Delete old API endpoints, CLI subcommands, config field names, message
  types, and database columns the moment they have a successor.** Do not
  introduce compat shims, dual-write code, or versioned wrappers.
- **Rename freely.** If a name is wrong, fix it everywhere in one commit.
  No aliases, no `// renamed from X` comments.
- **Migrate data, do not version it.** If a config or schema changes shape,
  write the migration and delete the old shape.

After 1.0, the rules invert: deprecation will be slow and deliberate, and
backward compatibility will be maintained religiously. The reason this
matters now is that any cruft left in the tree at 1.0 becomes a permanent
maintenance burden. Cut it before it gets locked in.

### Secure by default (OpenBSD philosophy)
Systems ship locked down. Operators must take deliberate action to open them up.
- Hubs auto-generate an owner token on first start (never run open by default)
- Admin API requires owner/admin auth unconditionally, even on open hubs
- Config files written with 0600, directories with 0700 (Unix); 0644/0755 on Windows (SYSTEM account needs read access for services)
- Tokens compared with `crypto/subtle.ConstantTimeCompare`
- WebSocket origin checking when auth is enabled
- CORS: wildcard for public endpoints (status/history), echo-origin for admin

### Zero-knowledge relay
The hub never inspects encrypted tunnel payloads. WireGuard encryption is
end-to-end between agent and client.

### No TUN, no root
Agents use userspace WireGuard via gVisor netstack. No admin/root privileges
required for tunnel operation.

## API Design

All REST APIs must follow Fielding's original REST architectural style, not
the colloquial "REST" that amounts to RPC-over-HTTP with JSON. Specifically:

- Resources are nouns, not verbs. Use `/api/admin/access/{id}`, not
  `/api/admin/grant-access`.
- HTTP methods carry the semantics: GET reads, PUT replaces, PATCH updates,
  DELETE removes, POST creates. Do not use POST for operations that map
  naturally to other verbs.
- Sub-resources model relationships: `/api/admin/access/{id}/machines/{m}`
  represents the permissions for identity `{id}` on machine `{m}`.
- PUT is idempotent and replaces the resource. PATCH is a partial update.
- Responses should use appropriate status codes (201 Created, 404 Not Found,
  409 Conflict, etc.), not 200 with an error field.
- Avoid action-oriented endpoints like `/api/admin/rotate/{id}`. Prefer
  PATCH or PUT on the resource with the intended state change in the body.

Tela is pre-release. There is no backward compatibility burden yet. When
the right shape becomes obvious, delete the cruft and migrate to the new
shape rather than carrying both.

## Architecture Notes

### Auth model
Token-based RBAC with four roles: `owner`, `admin`, `user` (default), `viewer`.
Machine permissions (register, connect, manage) control per-machine access.
Wildcard `*` applies to all machines. Owner/admin tokens bypass all permission checks.
The unified `/api/admin/access` endpoint joins tokens and permissions into a single
resource view. See [ACCESS-MODEL.md](ACCESS-MODEL.md) for the full explanation and
`cmd/telahubd/admin_api.go` for the implementation.

### Session addressing
Each session gets a /24 subnet: `10.77.{idx}.1` (agent) / `10.77.{idx}.2` (client).
Session index is monotonically incrementing per machine (1-254 max).

### Config precedence (telahubd)
1. Environment variables (highest)
2. YAML config file (`-config` flag or auto-detected)
3. Built-in defaults (lowest)

### Hot reload
Admin API changes take effect immediately via `authStore.reload()` and are
persisted to YAML. No hub restart needed for token/ACL/portal changes.

### Portal architecture (spec written, extraction not yet started)
The portal protocol is now specified in [DESIGN-portal.md](DESIGN-portal.md).
It is the wire-level contract every Tela portal must implement: ten or so
endpoints, two auth modes, a documented JSON shape per response. Awan Saya
matches this spec; the planned `internal/portal` Go package will too.
The spec carves the portal contract out from the SaaS surface (accounts,
orgs, billing, signup) so a single-user portal can implement only the
contract.

The full plan for extraction lives under "Portal architecture: one
protocol, many hosts" in ROADMAP-1.0.md and goes: extract `internal/portal`
with pluggable storage, add a TelaVisor "Portal mode" that runs the
file-backed store in-process, keep Awan Saya as the Postgres reference
implementation. The spec has four open questions in section 14 that need
to be resolved before extraction. Read DESIGN-portal.md before touching
any portal-related code.

### Release channels
Tela ships through three channels: `dev` (every commit to main), `beta`
(promoted from dev on demand), `stable` (promoted from beta on demand).
Each channel is described by a JSON manifest hosted as an asset on a
rolling `channels` GitHub Release: `dev.json`, `beta.json`, `stable.json`.
The manifest names the current tag for that channel and lists every
binary published under it with its SHA-256 and size.

Every Tela binary fetches its configured channel manifest on self-update,
compares its current version, downloads the appropriate binary from the
manifest's `downloadBase`, and verifies the SHA-256 before installing.
The fetcher and verifier live in `internal/channel`. The configured
channel for telad and telahubd is in their YAML config under `update.channel`;
for the tela client and TelaVisor it's in `~/.tela/credentials.yaml` under
`update.channel`.

Self-update is exposed three ways on every binary so users can pick their
preferred ergonomics:
1. CLI: `tela update`, `telad update`, `telahubd update`. All accept
   `-channel <name>` and `-dry-run`.
2. API: `GET /api/admin/update` (status), `PATCH /api/admin/update` (set
   channel), `POST /api/admin/update` (trigger update) on telahubd.
   Same shape on the agent management proxy via the `update-status` and
   `update-channel` mgmt actions.
3. UI: Channel dropdowns in TelaVisor (Hub Settings, Agent Settings,
   Application Settings) and Awan Saya (hub and fleet management cards).

See [RELEASE-PROCESS.md](RELEASE-PROCESS.md) for the model end-to-end
and `internal/channel/channel.go` for the implementation.

## Coding Conventions

- Go 1.24+, standard library preferred over external dependencies
- Minimal dependencies: gorilla/websocket, wireguard-go, gvisor, yaml.v3
- Errors: `fmt.Errorf("context: %w", err)` wrapping pattern
- Logging: `log.Printf("[component] message")` with bracketed component prefix
- Flag parsing: `flag.NewFlagSet` per subcommand, `envOrDefault` for env fallback
- `permuteArgs` allows flags after positional args in tela admin commands
- Config persistence: `writeHubConfig` for YAML, `service.SaveConfig` for JSON
- Mutex conventions: `sync.RWMutex` for read-heavy state (`globalCfgMu`, `machinesMu`)
- Constant-time comparison for all token checks (`crypto/subtle`)
- **Output style:** Print only actionable information. Do NOT include reassurance messages like "no permission issues" or explanations of internal mechanics. Users expect features to work; they do not want output confirming that known issues were fixed.

# git management

- Clean up tmpclaude* files periodically so that they do not clog the file system

## Changelog maintenance

Maintain `CHANGELOG.md` with every commit that changes user-visible behavior.
Internal refactors, CI tweaks, and code cleanup do not need entries.

Rules:
1. New entries go under an `[Unreleased]` section at the top of the file.
2. Use subsections: `### Added`, `### Fixed`, `### Changed`, `### Removed`.
3. Each entry is one line, plain language, no commit hashes.
4. When a version is promoted to beta or stable, replace `[Unreleased]`
   with `[X.Y] - YYYY-MM-DD` and add a fresh `[Unreleased]` above it.
5. If the current commit is user-visible and you are creating the commit,
   include the changelog update in the same commit.

## Remaining Review Items

These are architectural improvements identified during a comprehensive code review.
They are larger refactoring tasks, not urgent fixes.

### C1: Extract admin HTTP helpers
The admin API handlers repeat the same CORS + Content-Type + JSON encode pattern
on every response path. Extract a helper like `adminJSON(w, r, status, payload)`
to reduce boilerplate in `cmd/telahubd/admin_api.go`.

### C3: Unify local and remote user management
`telahubd user` (local config file) and `tela admin` (remote REST API) are
parallel implementations of the same operations. Consider whether the local
CLI could call the admin API when the hub is running, falling back to direct
config manipulation when stopped.

### O1: Use `net/url` for URL construction
Several places build URLs with string concatenation (e.g., portal registration,
admin API calls). Use `net/url.URL` and `url.JoinPath` for correctness with
trailing slashes and special characters.

### O2: Structured logging
Replace `log.Printf` with structured logging (e.g., `log/slog`) so log output
can be filtered and parsed by log aggregation tools.

### O3: Graceful HTTP shutdown
`telahubd` calls `server.Close()` on shutdown which drops in-flight requests.
Use `server.Shutdown(ctx)` with a timeout for graceful drain.

### O4: Rate limiting on admin API
Admin API endpoints have no rate limiting. A misconfigured client could
overwhelm the hub with token/ACL mutations and config file writes.

### O5: Test coverage
No unit or integration tests exist. Priority areas:
- Auth store (canRegister, canConnect, canViewMachine edge cases)
- Admin API endpoints (token CRUD, grant/revoke, portal management)
- Ring buffer history (wrap-around, snapshot ordering)
- `permuteArgs` flag reordering
- Portal registration and sync token flow

## Completed Review Items (for reference)

These were implemented during the review sessions:

| ID | Description | Status |
|----|-------------|--------|
| D1 | Auto-bootstrap auth (secure by default) | Done |
| D2 | Admin API requires auth unconditionally | Done |
| D3 | Startup warning for open mode | Done |
| D4 | Restrictive CORS on admin endpoints | Done |
| D5 | WebSocket origin checking | Done |
| D6 | Config file permissions 0600 | Done |
| D7 | Secure cookie flag | Done |
| D8 | Data directory permissions 0700 | Done |
| S1 | Deprecate ?token= query param | Done |
| S2 | Restrict viewer token injection to console page | Done |
| S4 | Constant-time token comparison everywhere | Done |
| S5 | URL-encode query params in CLI | Done |
| S6 | isOwnerOrAdmin returns false when auth disabled | Done |
| E1 | Ring buffer for history events | Done |
| E2 | sync.Pool for UDP relay buffers | Done |
| E3 | Eliminate string conversion in static serving | Done |
| E5 | RWMutex for read-only config access | Done |
| E6 | Shared HTTP client in tela admin CLI | Done |
| O6 | Monotonic session index counter | Done |
| U1 | Remove 3389 default port from telad | Done |
| U2 | Reject sessions beyond 254 limit | Done |
| U3 | Graceful signal handling in telad | Done |
| U6 | Verbose relay logging flag | Done |

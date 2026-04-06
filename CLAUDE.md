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
| `STATUS.md` | Traceability matrix mapping design sections to implementation |
| `TelaVisor.md` | TelaVisor desktop client layout, features, and UX |
| `TELA-DESIGN-LANGUAGE.md` | Visual design language shared across all Tela products |
| `ACCESS-MODEL.md` | Token-based RBAC, permissions, ACL model |
| `CONFIGURATION.md` | Config file formats for all three binaries |
| `REFERENCE.md` | CLI reference for tela, telad, and telahubd |
| `TODO.md` | Current task list and priorities |

Do not read all files upfront in every conversation. Read `CLAUDE.md` always.
Read the others when the conversation topic requires them (e.g., read
`DESIGN-remote-admin.md` when working on agent management, read `TelaVisor.md`
when working on the GUI). Use your judgment to decide which files are relevant.

## Project Overview

Tela is a FOSS encrypted remote-access fabric using WireGuard tunnels.
Three binaries, one Go module (`github.com/paulmooreparks/tela`):

| Binary | Path | Role |
|--------|------|------|
| `tela` | `cmd/tela/` | Client CLI (connects to machines via hub, mounts file shares) |
| `telad` | `cmd/telad/` | Agent daemon (registers machines with hub, exposes local services) |
| `telahubd` | `cmd/telahubd/` | Hub server (HTTP + WebSocket + UDP relay on a single port) |

Supporting packages:
- `internal/service/` -- Cross-platform OS service management (Windows SCM, systemd, launchd)
- `console/` -- Embedded static files for the hub's web console

Related project: **Awan Saya** (`c:\Users\paul\source\repos\awansatu`) -- portal/registry that discovers and lists hubs. Changes to tela's auth, portal sync, or API contracts may affect Awan Saya.

## Wirint Style

- Do not use emdash
- Do not use a style of writing that would even require emdash or semicolons.
- Do not use curly quotes, single or double. Use ' or " as appropriate instead.
- Do not use a salesy, marketing style of writing, unless instructed to do so. Simply be factual instead.
- Write in the style of a technical writer producing exact documentation, unless instructed otherwise.
- Be very clear and thorough in technical writing. Do not leave out steps.
- Spell out the first abbeviation usage in a document unless you can be reasonably sure the abbreviation is known in context (e.g., TCP and UDP are known, FOSS is not necessarily known)

## Build & Verify

```bash
go build ./...          # Build all three binaries
go vet ./...            # Static analysis
go test ./...           # Run tests (if any)
```

All three binaries must compile cleanly after any change. Always run both
`go build ./...` and `go vet ./...` before considering work complete.

## Guiding Principles

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

Legacy endpoints that predate this convention (e.g., `/api/admin/grant`,
`/api/admin/revoke`) remain for backward compatibility but should not be
used as a pattern for new endpoints.

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

# TelaGUI Roadmap

## Who TelaGUI is for

TelaGUI targets users who:

- Use Tela regularly (not just trying it once)
- Manage a handful of hubs and machines (1-10 hubs, up to ~50 machines)
- Want a point-and-click workflow instead of typing CLI commands
- May or may not be the person who set up the infrastructure
- Are comfortable with technical tools but don't want to live in a terminal

These are developers connecting to dev/staging environments, IT staff accessing remote machines for support, small-team admins who wear the ops hat part-time, and power users who prefer a GUI over memorizing flags.

TelaGUI is not for fleet-scale ops (that's Awan Saya's territory) or for first-time dabblers (the CLI is simpler for a one-off connection).

## Current state

TelaGUI today handles the core connection workflow:

- Store hub credentials (manual entry, pairing codes, Docker token extraction)
- Browse hubs, machines, and services
- Select services and assign local ports
- Connect/disconnect with one click
- Real-time status via WebSocket (service bound, tunnel active, connection count)
- Connection profiles (create, switch, import, export, YAML preview)
- Terminal output, command log, verbose mode
- Auto-update (self-update for standalone installs, defers to package manager otherwise)
- System tray with minimize-to-tray on close

## Roadmap

### Phase 1: Polish the connection experience

These improve what TelaGUI already does.

**Connection diagnostics.** When a service fails to connect, show why. "Hub unreachable," "token rejected," "machine not registered," "port conflict on localhost:5432." Today the user has to read terminal output to figure this out. Surface errors in the Status tab with actionable messages.

**Service bookmarks.** After connecting, let the user click a service row to open it. SSH opens a terminal emulator (or the system default SSH client). HTTP opens the browser at `localhost:<port>`. RDP opens mstsc. PostgreSQL copies the connection string. This turns TelaGUI from "a tool that sets up tunnels" into "a tool that gets me to my services."

**Connection history.** Track which profiles were connected, when, for how long. Show this in a History tab or as a timeline on the Status tab. Useful for "when did I last connect to staging?" and for ops awareness.

**Multi-profile connections.** Today you connect one profile at a time. Allow connecting multiple profiles simultaneously (or at least warn clearly when switching). Some users have a "work" profile and a "home lab" profile and want both active.

**Quick connect.** A way to connect to a single service without setting up a full profile. Right-click a machine in the sidebar, pick a service, connect. Good for one-off access.

### Phase 2: Lightweight admin features

These add value for users who also manage the infrastructure they connect to. Gate these behind the token's role: if the stored token is admin or owner, show admin features. If it's a user or viewer token, hide them.

**Token management.** View, create, and revoke tokens on a hub. Today this requires `tela admin` CLI commands. A "Tokens" panel in the hub detail view lets the user manage identities without a terminal.

**ACL management.** Grant and revoke machine access for tokens. "Give 'bob' access to 'staging-db'." This is the most common admin operation and it currently requires CLI commands or SSH into the hub.

**Pairing code generation.** Generate one-time pairing codes from the GUI. Today you can paste a code to connect, but generating one requires CLI access to the hub. An admin should be able to click "Generate Code" and hand it to a colleague.

**Hub configuration viewer.** Show the hub's current configuration (auth mode, portal registrations, relay settings). Read-only at first, editable later. Useful for "is auth enabled on this hub?" without SSH.

**Machine detail panel.** Expand the machine view beyond "list of services." Show agent version, OS, hostname, uptime, tags, location, session count, last seen. This information is already in the hub status API but TelaGUI doesn't display most of it.

### Phase 3: Operational awareness

These turn TelaGUI from a connection tool into a lightweight monitoring dashboard for the hubs and machines the user cares about.

**Notifications.** Desktop notifications when an agent goes offline, a hub becomes unreachable, or a session drops unexpectedly. Configurable per hub or per machine. Uses the system tray icon.

**Hub health indicators.** On the Status tab or a new Overview tab, show hub health at a glance: number of machines online/offline, active session count, relay mode (WebSocket/UDP/direct). A green/yellow/red indicator per hub.

**Session log.** View recent sessions on a hub: who connected, to which machine, when, how long. This uses the hub's /api/history endpoint which already exists.

**Service availability timeline.** For each service, show a simple uptime bar: when was the agent online and the service reachable? Helps answer "is barn SSH flaky or is it just me?"

### Phase 4: Power user features

These serve users who spend significant time in TelaGUI.

**SSH terminal.** Built-in terminal for SSH services. When connected to a machine that exposes SSH, click "Open Terminal" and get a shell in a TelaGUI tab. Uses the Tela tunnel (localhost port) so it works through the existing WireGuard encryption. The terminal could use xterm.js in the Wails WebView.

**Port forwarding rules.** Persistent rules like "always map barn:22 to localhost:50022." Today port assignments are automatic and can shift between sessions. Let users pin specific local ports for services they script against or bookmark.

**Startup profiles.** "When TelaGUI launches, auto-connect this profile." Already partially implemented (auto-connect setting), but could expand to multiple profiles or conditional logic (connect to work on weekdays, home lab on weekends).

**Keyboard shortcuts.** Ctrl+1 through Ctrl+7 for tabs. Ctrl+Shift+C to connect, Ctrl+Shift+D to disconnect. Ctrl+K for quick connect search.

**Theming.** Light mode. The current dark theme is good, but some users will want light. A theme toggle in Settings.

## What stays out of TelaGUI

These belong in Awan Saya, not TelaGUI:

- Organization and team management
- Billing and subscription tiers
- Multi-user dashboards and shared views
- Fleet-scale monitoring (50+ hubs, hundreds of machines)
- Log aggregation across hubs
- Deployment automation
- Compliance and audit features
- AI-powered queries

TelaGUI is a personal tool. It manages one user's connections and gives that user visibility into the infrastructure they touch. Awan Saya is the multi-tenant platform for organizations.

## Technical notes

- TelaGUI is a Wails v2 app (Go backend, vanilla JS frontend)
- The frontend is currently ES5 vanilla JS with no framework. This works for the current scope. If Phase 3-4 features push the UI complexity past what vanilla JS handles cleanly, consider adding a lightweight framework (Preact or Svelte)
- Admin features (Phase 2) should use the existing `tela admin` HTTP API endpoints. TelaGUI already has the hub URL and token stored. It just needs to call the admin endpoints
- SSH terminal (Phase 4) can use xterm.js in the WebView, connecting to the local tunnel port. The Go backend handles the SSH session via `golang.org/x/crypto/ssh`
- All admin features should be gated on the token's role. The hub status API returns the caller's role in the response. If the token is viewer or user, hide admin panels

## Versioning and release

TelaGUI follows the tela release cycle (v0.2.x auto-built on push to main). The roadmap phases are not tied to specific version numbers. Features ship incrementally as they're ready.

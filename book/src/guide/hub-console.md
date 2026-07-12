# Hub Web Console

The hub ships with a built-in web console served at its HTTP address. Point
a browser at `http://hub.example.com:PORT/` (or `https://` if TLS is
configured) and the console loads automatically.

No separate installation is required. The console is embedded in the
`telahubd` binary.

## Sections

### Machines

The Machines section lists every registered agent and its services. Each
row shows the machine name, registered services (name and port), current
status, and active session count.

Status indicators:

| Indicator | Meaning |
|-----------|---------|
| Green dot | Online: agent connected within the last 30 seconds |
| Yellow dot | Stale: agent has not sent a keepalive recently |
| No dot | Offline |

Click the **Refresh** button to reload from the hub. The "last updated"
timestamp shows when data was last fetched.

### Recent Activity

The Recent Activity section shows the most recent connection events
(sessions opened, sessions closed, agent registrations), up to the hub's
in-memory history capacity of 100 events. Each entry shows the timestamp,
event type, machine name, and client address. The history buffer is held in
memory and resets when the hub restarts.

### Pairing (Admin Only)

The console shows a Pairing section when its session is authorized as owner
or admin. It generates one-time pairing codes without requiring
`tela admin pair-code` on the command line.

Fields:

| Field | Options | Description |
|-------|---------|-------------|
| Type | Connect, Register | Connect codes are for users; register codes are for new agents |
| Expiration | 10 minutes, 1 hour, 24 hours, 7 days | How long the code remains valid |
| Machine scope | Machine ID or `*` | Which machine(s) the code grants access to |

After clicking **Generate Code**, the console displays the short code and
the redemption command to give to the recipient. The code is single-use and
cannot be regenerated.

### Download

When a stable or beta release has been published, the Download section
appears with direct links to the `tela` client binary for each supported
platform and architecture.

### CLI Quick Reference

A brief reminder of the most common `tela` commands, for operators sharing
hub access with users who are not yet familiar with the client.

## Authentication

The hub injects a read-only viewer token into the console page at load
time, so the Machines and Recent Activity data is visible without any login
step. The viewer token grants no session or admin privileges.

Administrative operations are not the console's job. To manage tokens,
permissions, agents, or pairing at full fidelity, use `tela admin` from a
terminal or TelaVisor's Infrastructure mode, both of which authenticate
with your own owner or admin token. Passing a token in the URL query string
is deprecated and should not be used; query strings leak into logs and
browser history.

## Theme

The console supports light, dark, and system-preference themes. The toggle
is in the top navigation bar. The preference is stored in browser local
storage.

## When to Use the Console vs. the CLI

The console is convenient for checking machine status and recent activity
at a glance from any browser, with zero setup. For management tasks
(tokens, permissions, agent configuration, updates, pairing codes), use
`tela admin` from a terminal or TelaVisor.

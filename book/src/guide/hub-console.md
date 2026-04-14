# Hub web console

The hub ships with a built-in web console served at its HTTP address. Point a browser at `http://hub.example.com:PORT/` (or `https://` if TLS is configured) and the console loads automatically.

No separate installation is required. The console is embedded in the `telahubd` binary.

## Sections

### Machines

The Machines section lists every registered agent and its services. Each row shows the machine name, registered services (name and port), current status, and active session count.

Status indicators:

| Indicator | Meaning |
|-----------|---------|
| Green dot | Online -- agent connected within the last 30 seconds |
| Yellow dot | Stale -- agent has not sent a keepalive recently |
| No dot | Offline |

Click the **Refresh** button to reload from the hub. The "last updated" timestamp shows when data was last fetched.

### Recent Activity

The Recent Activity section shows the last 200 connection events: sessions opened, sessions closed, and agent registrations. Each entry shows the timestamp, event type, machine name, and client address.

### Pairing (admin only)

Administrators see a Pairing section not visible to other users. It generates one-time pairing codes without requiring `tela admin pair-code` on the command line.

Fields:

| Field | Options | Description |
|-------|---------|-------------|
| Type | Connect, Register | Connect codes are for users; register codes are for new agents |
| Expiration | 10 minutes, 1 hour, 24 hours, 7 days | How long the code remains valid |
| Machine scope | Machine ID or `*` | Which machine(s) the code grants access to |

After clicking **Generate Code**, the console displays the short code and the redemption command to give to the recipient. The code is single-use and cannot be regenerated.

### Download

When a stable or beta release has been published to the GitHub Release channel, the Download section appears with direct links to the `tela` client binary for each supported platform and architecture.

### CLI Quick Reference

A brief reminder of the most common `tela` commands, for operators sharing hub access with users who are not yet familiar with the client.

## Authentication

The hub injects a viewer token into the console page at load time. This token has the `viewer` role and allows read-only access to the Machines and Recent Activity data without any login step.

The Pairing section appears only when the browser presents a token with `owner` or `admin` role. You can authenticate at a higher level by appending `?token=<admin-token>` to the console URL.

## Theme

The console supports light, dark, and system-preference themes. The toggle is in the top navigation bar. The preference is stored in browser local storage.

## When to use the console vs. the CLI

The console is convenient for checking machine status at a glance and for generating pairing codes without terminal access. For anything beyond those two tasks -- managing tokens, changing permissions, viewing agent configuration, or triggering updates -- use `tela admin` from a terminal.

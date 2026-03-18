# TelaGUI

TelaGUI is a desktop client for Tela. It wraps the `tela` command-line tool in a graphical interface, handling connection profiles, hub credentials, and real-time tunnel status without requiring terminal access.

TelaGUI runs on Windows, Linux, and macOS. It is built with [Wails v2](https://wails.io/), using Go for the backend and plain JavaScript for the frontend.

## What TelaGUI does

TelaGUI manages the full lifecycle of connecting to remote services through Tela hubs:

1. **Store hub credentials.** Add hubs by URL and token, or use a one-time pairing code. Credentials are stored in the same credential store that `tela login` uses.
2. **Select services.** Browse machines registered on each hub, see which are online, and check the services you want to connect to.
3. **Connect with one click.** TelaGUI saves your selections as a connection profile, launches `tela connect -profile`, and monitors the process.
4. **Monitor tunnel status.** The Status tab shows each selected service with its remote port, local port, and current state (Not connected, Listening, or Active with connection count). Status updates arrive in real time over tela's WebSocket control API.
5. **Manage multiple profiles.** Create, rename, delete, import, and export profiles. Each profile is a standalone YAML file compatible with `tela connect -profile`.

## Tabs

### Status

Displays the current connection state and a table of all selected services grouped by machine. Each row shows:

- **Service** -- the service name (e.g., SSH, RDP, Web)
- **Remote** -- the port on the remote machine (e.g., :22)
- **Local** -- the localhost address bound by tela (e.g., localhost:50022)
- **Status** -- Not connected, Listening (port bound, no active tunnels), or Active (with a count of open connections)

Status indicators update in real time. When you connect to a service (e.g., `ssh localhost -p 50022`), the status changes from Listening to Active. When you disconnect, it returns to Listening.

### Profiles

The Profiles tab is where you configure what to connect to. The left sidebar lists hubs and their machines. Selecting a machine shows its available services with checkboxes.

Check the services you want, and TelaGUI assigns conflict-free local ports. Your selections persist automatically. The profile is a standard YAML file that you can also use directly with `tela connect -profile`.

Hub-level checkboxes let you include or exclude an entire hub's services from the profile.

### Terminal

Live output from the `tela` process. This is the same output you would see running `tela connect -profile` in a terminal. You can toggle verbose mode, freeze scrolling, copy the output, or save it to a file.

### Command Log

Records the CLI commands that TelaGUI generates. Each entry shows a timestamp, a description, and the exact command. Use the Copy button to reproduce any operation in a terminal.

### Hubs

Add and remove hub credentials. You can enter a hub URL and token manually, paste a one-time pairing code, or extract a token from a local Docker container running telahubd.

### Settings

- **Auto-connect** -- connect automatically when TelaGUI starts
- **Reconnect on drop** -- reconnect if the connection drops
- **Minimize behavior** -- minimize to system tray or taskbar
- **Start minimized** -- launch hidden in the system tray
- **Minimize on close** -- close button minimizes instead of quitting
- **Auto-check updates** -- check for new releases on startup
- **Verbose logging** -- enable verbose output by default
- **CLI path** -- shows where the tela binary is installed

### About

Version information, project links, license, and dependency credits.

## System tray

When configured to minimize to the system tray, TelaGUI places an icon in the notification area. Left-click or double-click the icon to show the window. Right-click for a menu with Show and Quit options.

## Automatic updates

TelaGUI checks GitHub releases for new versions. If an update is available, a button appears in the top bar. Clicking it downloads the update and restages the binary. The tela CLI binary is updated independently and stored in the platform's local application directory.

If TelaGUI was installed via a package manager (winget, Chocolatey, apt, brew), the self-update button is hidden. Use the package manager to update instead.

## Building from source

TelaGUI requires [Wails v2](https://wails.io/docs/gettingstarted/installation) and its prerequisites (Go 1.24+, Node.js, platform WebView2/webkit2gtk).

```bash
cd cmd/telagui
wails build
```

The output binary is in `cmd/telagui/build/bin/`.

For development with live reload:

```bash
cd cmd/telagui
wails dev
```

## How TelaGUI works with tela

TelaGUI does not implement tunneling itself. It manages connection profiles and launches the `tela` CLI as a subprocess. The relationship:

1. TelaGUI writes a profile YAML file with your selected hubs, machines, services, and local port assignments.
2. TelaGUI runs `tela connect -profile <path>` as a child process.
3. tela opens a local control API (HTTP + WebSocket on a random localhost port with a random token).
4. TelaGUI connects to tela's control WebSocket to receive real-time events (`service_bound`, `tunnel_activity`, `connection_state`).
5. When you click Disconnect, TelaGUI signals the tela process to shut down gracefully.

The profile YAML that TelaGUI writes is the same format documented in [REFERENCE.md](REFERENCE.md). You can use it interchangeably with the CLI.

## Profile storage

Profiles are stored in the user's application data directory:

| Platform | Path |
|----------|------|
| Windows | `%APPDATA%\tela\profiles\` |
| Linux | `~/.tela/profiles/` |
| macOS | `~/.tela/profiles/` |

Each profile is a YAML file (e.g., `telagui.yaml`). The default profile is named `telagui`.

## Configuration

Settings are stored in `telagui-settings.yaml` alongside the profiles directory. Settings take effect immediately and persist across restarts.

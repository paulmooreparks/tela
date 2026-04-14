# Run Tela as an OS service

## What you are setting up

When you run `telad` or `telahubd` from a terminal, they stop when the terminal closes. That is fine for testing but not for production. A server that reboots at 3 AM should bring its tunnel back up automatically, without anyone logging in and running a command.

This chapter covers installing `telad` and `telahubd` as native OS services so they start at boot, restart on failure, and survive logouts. The mechanism is the platform's own service manager -- Windows Service Control Manager (SCM), systemd on Linux, launchd on macOS -- which means standard service management tools (`sc`, `systemctl`, `launchctl`) all work on these processes.

By the end of this chapter, `telad` (or `telahubd`, or both) will:

- Start automatically when the machine boots, before any user logs in
- Restart automatically if the process crashes
- Accept `start`, `stop`, `restart`, and `status` commands from the OS service manager
- Persist its configuration in the service metadata so no external config file needs to be present at startup

The `tela` client also supports user-level autostart (starts at login, not at boot) for cases where you want a persistent client tunnel tied to your login session rather than to the machine. That is covered at the end of the chapter.

## How it works

Each binary stores its runtime configuration in the service metadata (Windows registry, systemd config, launchd plist). This eliminates filesystem permission issues and keeps everything in one place.

Configuration can be:
1. **Loaded from a YAML file** (for structured multi-machine setups)
2. **Embedded inline** (for simple single-machine deployments)

When you run `service install`, the binary encodes the configuration and registers it with the OS service manager. The service just runs `<binary> service run`, which loads the configuration from metadata (or falls back to a YAML file if present).

**To reconfigure:**
- Edit the YAML config file (if one exists) and run `service restart`, or
- Reinstall with new parameters using `service install`

---

## telad

### Install

Two installation modes are available:

**Mode 1: From a config file (recommended for complex setups)**

```bash
# Windows: run from an elevated (Administrator) prompt.
# Linux/macOS: use sudo.

telad service install -config telad.yaml
```

The config file is validated, embedded in service metadata, and a reference copy is retained on disk at (for example) `C:\ProgramData\Tela\telad.yaml` or `/etc/tela/telad.yaml`.

Make sure your `telad.yaml` includes the hub URL, auth token, and machine definitions before installing. See [Run an agent](telad.md) for the full config format and authentication setup.

**Mode 2: Inline configuration (recommended for simple setups)**

```bash
telad service install -hub ws://your-hub:8080 -machine barn -ports "22:SSH,3389:RDP"
```

Configuration is passed as command-line flags and stored inline. No external file is needed. Ideal for single-machine deployments without additional setup.

### Config file format

```yaml
# telad.yaml - register machines with the hub.
hub: wss://tela.example.com
token: my-secret-token        # optional auth token

machines:
  - name: workstation
    hostname: workstation
    os: windows
    services:
      - port: 3389
        proto: tcp
        name: RDP
        description: Remote Desktop
      - port: 22
        proto: tcp
        name: SSH
    target: 127.0.0.1          # where to forward traffic (default)
```

### Manage

```bash
telad service start       # Start the service
telad service stop        # Stop the service
telad service restart     # Stop + start (after editing config)
telad service status      # Show current state
telad service uninstall   # Remove the service and config
```

---

## telahubd

### Install

You can either provide an existing config file or let the installer generate one from flags:

```bash
# Option 1: from a config file
telahubd service install -config telahubd.yaml

# Option 2: generate from flags
telahubd service install -name myhub -port 80 -udp-port 41820
```

### Config file format

```yaml
# telahubd.yaml - hub server configuration.
port: 80            # HTTP + WebSocket listen port
udpPort: 41820      # UDP relay port
name: "My Hub"      # Display name (optional)
```

> Authentication (tokens, access control lists) is managed separately via `telahubd user bootstrap` (for the first owner token) and `tela admin` commands (for subsequent identities). You do not need to edit auth configuration in the YAML file manually. See [Run a hub on the public internet](hub.md) for details.

> **Bootstrap ordering:** Run `telahubd user bootstrap` **before** `telahubd service install` if you want the installed config to already contain auth tokens. If you install the service first and then bootstrap, the bootstrap writes directly to the system config path (`/etc/tela/telahubd.yaml` or `%ProgramData%\Tela\telahubd.yaml`).

> Environment variables (`TELAHUBD_PORT`, `TELAHUBD_UDP_PORT`, `TELAHUBD_NAME`) always override the config file.

### Manage

```bash
telahubd service start
telahubd service stop
telahubd service restart
telahubd service status
telahubd service uninstall
```

---

## Platform details

### Windows

The service is registered with the Service Control Manager (SCM) using auto-start and automatic restart on failure (5 s, 5 s, 30 s delays, reset after 24 h). Administrator privileges are required for all operations except `service status`.

### Linux (systemd)

A unit file is written to `/etc/systemd/system/<name>.service`, enabled on boot, and set to restart on failure. Root is required for install/start/stop.

### macOS (launchd)

A plist is written to `/Library/LaunchDaemons/com.tela.<name>.plist` with `RunAtLoad` and `KeepAlive` enabled. Root is required.

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| "administrator privileges required" | Run from an elevated prompt / use `sudo` |
| "service __ is already installed" | Run `service uninstall` first |
| Service starts but exits immediately | Check the YAML config for errors; review logs |
| Config changes not taking effect | Run `service restart` after editing |

**Log locations:**
- **Windows:** Event Viewer → Application
- **Linux:** `journalctl -u telad` or `journalctl -u telahubd`
- **macOS:** `/var/log/telad.log` or `/var/log/telahubd.log`

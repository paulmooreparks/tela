# Running Tela as an OS Service

Both **telad** (daemon) and **telahubd** (hub) can run as native OS services
on Windows, Linux, and macOS. The service machinery is built into the binary; no wrapper scripts needed.

## How it works

Each binary reads its runtime configuration from a YAML file in a
system-wide directory:

| OS      | Config directory            |
|---------|------------------------------|
| Windows | `%ProgramData%\Tela\`        |
| Linux   | `/etc/tela/`                 |
| macOS   | `/etc/tela/`                 |

When you run `service install`, the binary copies (or generates) a YAML
config to that directory and registers itself with the OS service manager.
The service just runs `<binary> service run`, which loads the config from the
standard location.

**To reconfigure**: edit the YAML file and restart the service. No
reinstallation required.

---

## telad

### Install

```bash
# Copy your telad.yaml to the system config dir and register the service.
# Windows: run from an elevated (Administrator) prompt.
# Linux/macOS: use sudo.

telad service install -config telad.yaml
```

The config file will be copied to (for example) `C:\ProgramData\Tela\telad.yaml`
or `/etc/tela/telad.yaml`.

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

You can either provide an existing config file or let the installer generate
one from flags:

```bash
# Option 1: from a config file
telahubd service install -config telahubd.yaml

# Option 2: generate from flags
telahubd service install -name myhub -port 8080 -udp-port 41820 -www /opt/tela/www
```

### Config file format

```yaml
# telahubd.yaml - hub server configuration.
port: 8080            # HTTP + WebSocket listen port
udpPort: 41820        # UDP relay port
name: "My Hub"        # Display name (optional)
wwwDir: ./www         # Static file directory
```

> **Note:** Environment variables (`HUB_PORT`, `HUB_UDP_PORT`, `HUB_NAME`,
> `HUB_WWW_DIR`) always override the config file, for backward compatibility.

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

The service is registered with the **Service Control Manager (SCM)** using
auto-start and automatic restart on failure (5 s, 5 s, 30 s delays, reset
after 24 h). Administrator privileges are required for all operations except
`service status`.

### Linux (systemd)

A unit file is written to `/etc/systemd/system/<name>.service`, enabled
on boot, and set to restart on failure. Root is required for install/start/stop.

### macOS (launchd)

A plist is written to `/Library/LaunchDaemons/com.tela.<name>.plist` with
`RunAtLoad` and `KeepAlive` enabled. Root is required.

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

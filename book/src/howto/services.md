# Run Tela as an OS Service

When you run `telad` or `telahubd` from a terminal, they stop when the
terminal closes. That is fine for testing but not for production. A server
that reboots at 3 AM should bring its tunnel back up automatically, without
anyone logging in and running a command.

This chapter covers installing `telad` and `telahubd` as native OS services
so they start at boot, restart on failure, and survive logouts. The
mechanism is the platform's own service manager: Windows Service Control
Manager (SCM), systemd on Linux, launchd on macOS. Standard service
management tools (`sc`, `systemctl`, `launchctl`) all work on these
processes.

The `tela` client also supports installation as a service (for an always-on
tunnel that starts at boot) and user-level autostart (starts at login).
Both are most conveniently set up from TelaVisor's Client Settings tab; the
CLI equivalent is `tela service install -config <profile.yaml>`.

## How It Works

Each binary stores its runtime configuration in the service metadata
(Windows registry, systemd unit, launchd plist). Configuration can be
loaded from a YAML file or embedded inline from command-line flags. When
you run `service install`, the binary encodes the configuration and
registers it with the OS service manager. The service itself just runs
`<binary> service run`, which loads the configuration from metadata, or
falls back to a YAML file if present.

To reconfigure a service, edit the YAML config file (if one exists) and run
`service restart`, or reinstall with new parameters using
`service install`.

## telad

Two installation modes are available. Both require elevation (an
Administrator prompt on Windows, `sudo` on Linux and macOS).

**Mode 1: from a config file**, for structured multi-machine setups:

```bash
telad service install -config telad.yaml
```

The config file is validated, embedded in the service metadata, and a
reference copy is retained on disk at the platform-standard path
(`/etc/tela/telad.yaml` or `%ProgramData%\Tela\telad.yaml`). Make sure the
file includes the hub URL, token, and machine definitions before
installing; see [Run an Agent](telad.md) for the config format and
authentication setup.

**Mode 2: inline configuration**, for simple single-machine deployments:

```bash
telad service install -hub wss://hub.example.com -machine barn -ports "22:SSH,3389:RDP"
```

Configuration is passed as command-line flags and stored inline in the
service metadata. No external file is needed.

Manage the service with the same subcommand family:

```bash
telad service start
telad service stop
telad service restart     # after editing the config
telad service status
telad service uninstall   # removes the service and its stored config
```

## telahubd

Provide an existing config file or let the installer generate one from
flags:

```bash
# Option 1: from a config file
telahubd service install -config telahubd.yaml

# Option 2: generate from flags
telahubd service install -name myhub -port 8080 -udp-port 41820
```

Authentication (tokens, access control lists) is managed separately, via
`telahubd user bootstrap` for the first owner token and `tela admin`
commands for subsequent identities. You do not need to edit auth
configuration in the YAML file manually. See
[Run a Hub on the Public Internet](hub.md) for the full deployment
walkthrough, including the ordering between `service install` and
`user bootstrap`.

Environment variables (`TELAHUBD_PORT`, `TELAHUBD_UDP_PORT`,
`TELAHUBD_NAME`) always override the config file.

Manage the service:

```bash
telahubd service start
telahubd service stop
telahubd service restart
telahubd service status
telahubd service uninstall
```

## Platform Details

### Windows

The service is registered with the Service Control Manager using auto-start
and automatic restart on failure (5 s, 5 s, 30 s delays, reset after 24 h).
Administrator privileges are required for all operations except
`service status`.

### Linux (systemd)

A unit file is written to `/etc/systemd/system/<name>.service`, enabled on
boot, and set to restart on failure. Root is required for install, start,
and stop.

### macOS (launchd)

A plist is written to `/Library/LaunchDaemons/com.tela.<name>.plist` with
`RunAtLoad` and `KeepAlive` enabled. Root is required.

## Troubleshooting

| Symptom | Likely Cause |
|---|---|
| "administrator privileges required" | Run from an elevated prompt or use `sudo` |
| "service is already installed" | Run `service uninstall` first |
| Service starts but exits immediately | Check the YAML config for errors; review the logs |
| Config changes not taking effect | Run `service restart` after editing |

Log locations:

- **Windows:** Event Viewer, under Application
- **Linux:** `journalctl -u telad` or `journalctl -u telahubd`
- **macOS:** `/var/log/telad.log` or `/var/log/telahubd.log`

# Appendix A: CLI reference

Flags, subcommands, environment variables, and config schemas for `tela`,
`telad`, and `telahubd`. For narrative explanations, see the User Guide and
How-to Guides.

---

## tela

The client CLI. Opens WireGuard tunnels to machines through a hub and binds
local TCP listeners for their services. Requires no admin rights or kernel drivers.

### `tela connect`

```bash
tela connect -hub <hub> -machine <machine> [flags]
tela connect -profile <name>
```

| Flag | Env var | Description |
|------|---------|-------------|
| `-hub <url\|name>` | `TELA_HUB` | Hub URL (`wss://...`) or short name |
| `-machine <name>` | `TELA_MACHINE` | Machine name |
| `-token <hex>` | `TELA_TOKEN` | Hub auth token |
| `-ports <spec>` | | Comma-separated ports or `local:remote` pairs |
| `-services <names>` | | Comma-separated service names (resolved via hub API) |
| `-profile <name>` | `TELA_PROFILE` | Named connection profile |
| `-mtu <n>` | `TELA_MTU` | WireGuard tunnel MTU (default 1100) |
| `-v` | | Verbose logging |

When neither `-ports` nor `-services` is specified, all ports the agent
advertises are forwarded. Each machine gets a deterministic loopback address
at `localhost:PORT`; each service binds at its configured local port, or a fallback port if that is taken.

### `tela machines`

```bash
tela machines -hub <hub> [-token <token>]
```

### `tela services`

```bash
tela services -hub <hub> -machine <machine> [-token <token>]
```

### `tela status`

```bash
tela status -hub <hub> [-token <token>]
```

### `tela remote`

```bash
tela remote add <name> <portal-url>   # add a hub directory remote
tela remote remove <name>
tela remote list
```

### `tela profile`

```bash
tela profile list
tela profile show <name>
tela profile create <name>
tela profile delete <name>
```

### `tela pair`

```bash
tela pair -hub <hub-url> -code <code>
```

Exchanges a pairing code for a hub token and stores it in the credential store.

### `tela admin`

Remote hub management. Requires an owner or admin token.

Token resolution order: `-token` flag > `TELA_OWNER_TOKEN` > `TELA_TOKEN` > credential store.

**access** -- unified identity and per-machine permissions view

```bash
tela admin access [-hub <hub>] [-token <token>]
tela admin access grant <id> <machine> <perms>    # perms: connect,register,manage
tela admin access revoke <id> <machine>
tela admin access rename <id> <new-id>
tela admin access remove <id>
```

**tokens** -- token identity CRUD

```bash
tela admin tokens list
tela admin tokens add <id> [-role owner|admin]
tela admin tokens remove <id>
tela admin rotate <id>                             # regenerate a token
```

**portals** -- portal registrations on the hub

```bash
tela admin portals list
tela admin portals add <name> -portal-url <url>
tela admin portals remove <name>
```

**pair-code** -- one-time onboarding codes

```bash
tela admin pair-code <machine> [-type connect|register] [-expires <duration>] [-machines <list>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-type` | `connect` | `connect` (for users) or `register` (for agents) |
| `-expires` | `10m` | Duration: `10m`, `1h`, `24h`, `7d` |
| `-machines` | `*` | Comma-separated machine IDs (connect type only) |

**agent** -- remote management of `telad` through the hub

```bash
tela admin agent list
tela admin agent config -machine <id>
tela admin agent set -machine <id> <json>
tela admin agent logs -machine <id> [-n 100]
tela admin agent restart -machine <id>
tela admin agent update -machine <id> [-version <v>]
tela admin agent channel -machine <id>
tela admin agent channel -machine <id> set <dev|beta|stable>
```

**hub** -- lifecycle management of the hub itself

```bash
tela admin hub status
tela admin hub logs [-n 100]
tela admin hub restart
tela admin hub update [-version <v>]
tela admin hub channel
tela admin hub channel set <dev|beta|stable>
```

### `tela channel`

```bash
tela channel                                          # show current channel and latest version
tela channel set <dev|beta|stable>
tela channel set <ch> -manifest-base <url>            # override manifest URL prefix
tela channel show [-channel <ch>]                     # print the channel manifest
tela channel download <binary> [-channel <ch>] [-o <path>] [-force]
```

### `tela update`

```bash
tela update                              # update from the configured channel
tela update -channel <dev|beta|stable>   # one-shot channel override
tela update -dry-run
```

### `tela files`

File operations on machines with file sharing enabled. Requires an active
`tela connect` session.

| Command | Description |
|---------|-------------|
| `tela files ls -machine <m> [path]` | List files and directories |
| `tela files get -machine <m> <remote> [-o <local>]` | Download a file |
| `tela files put -machine <m> <local> [remote-name]` | Upload a file |
| `tela files rm -machine <m> <path>` | Delete a file |
| `tela files mkdir -machine <m> <path>` | Create a directory |
| `tela files rename -machine <m> <path> <new-name>` | Rename (new name only, not a path) |
| `tela files mv -machine <m> <src> <dst>` | Move within the share |
| `tela files info -machine <m>` | Show share status (file count, total size) |

### `tela mount`

Starts a WebDAV server exposing file shares from connected machines. Requires
an active `tela connect` session.

```bash
tela mount                     # start WebDAV server on port 18080
tela mount -port 9999
tela mount -mount T:           # Windows: map drive letter
tela mount -mount ~/tela       # macOS/Linux: mount to directory
```

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `18080` | WebDAV listen port |
| `-mount` | (none) | Drive letter (Windows `T:`) or directory path |

When `-mount` is omitted, the WebDAV server starts but no OS mount is
performed. Manual mount commands:

```bash
net use T: http://localhost:18080/            # Windows
mount_webdav http://localhost:18080/ /Volumes/tela  # macOS
gio mount dav://localhost:18080/              # Linux (GNOME)
```

### `tela service`

Manage `tela` as a native OS service for always-on tunnel scenarios.

```bash
tela service install -config <profile.yaml>
tela service start
tela service stop
tela service restart
tela service status
tela service uninstall
```

Config location when installed as a service:

| Platform | Path |
|----------|------|
| Linux/macOS | `/etc/tela/tela.yaml` |
| Windows | `%ProgramData%\Tela\tela.yaml` |

### `tela version`

```bash
tela version
```

### Connection profile schema

Profiles define multiple hub/machine connections that launch in parallel with
`tela connect -profile <name>`.

Profile location:

| Platform | Path |
|----------|------|
| Linux/macOS | `~/.tela/profiles/<name>.yaml` |
| Windows | `%APPDATA%\tela\profiles\<name>.yaml` |

Schema:

```yaml
id: ""                                # stable UUID, generated on first load
name: "work-servers"                  # human-readable label (informational)
mtu: 1100                             # WireGuard MTU for all connections in this profile
mount:
  mount: "T:"                         # drive letter (Windows) or directory path
  port: 18080                         # WebDAV listen port
  auto: false                         # auto-mount on connect
dns:
  loopback_prefix: "127.88"           # first two octets of the loopback range
connections:
  - hub: wss://hub.example.com        # hub URL or short name
    hubId: ""                         # stable hub UUID (populated lazily)
    machine: web01
    agentId: ""                       # stable agent UUID (populated lazily)
    token: ${WEB_TOKEN}               # ${VAR} expansion is supported
    address: ""                       # override loopback address (must be in 127.0.0.0/8)
    services:
      - remote: 22                    # forward by port number
        local: 2201                   # optional local port remap
      - name: postgres                # forward by service name (resolved via hub API)
```

**Top-level fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `id` | No | Stable UUID; generated automatically on first load |
| `name` | No | Human-readable profile label |
| `mtu` | No | WireGuard MTU override for all connections (default 1100) |
| `mount` | No | WebDAV mount settings |
| `mount.mount` | No | Drive letter (e.g. `T:`) or directory path |
| `mount.port` | No | WebDAV listen port (default 18080) |
| `mount.auto` | No | Auto-mount on connect (default false) |
| `dns.loopback_prefix` | No | First two octets of loopback range (default `127.88`) |
| `connections` | Yes | List of hub+machine connections |

**Connection entry fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `hub` | Yes | Hub URL or short name |
| `hubId` | No | Stable hub UUID; populated lazily, do not set manually |
| `machine` | Yes | Machine name |
| `agentId` | No | Stable agent UUID; populated lazily, do not set manually |
| `token` | No | Auth token; `${VAR}` references are expanded from the environment |
| `address` | No | Loopback address override (must be in `127.0.0.0/8`) |
| `services` | No | Port/service filter; omit to forward all ports |
| `services[].remote` | * | Remote port number |
| `services[].local` | No | Local port override (defaults to remote) |
| `services[].name` | * | Service name resolved via hub API |

\* Each service entry needs either `remote` or `name`, not both.

### Hub name resolution

When `-hub` is a short name (not `ws://` or `wss://`), `tela` resolves it in order:

1. Configured remotes (via `tela remote add`): queries each remote's `/api/hubs`. First match wins.
2. Local `hubs.yaml` fallback.
3. Error if unresolved.

### Environment variables

| Variable | Description |
|----------|-------------|
| `TELA_HUB` | Default hub URL or alias |
| `TELA_MACHINE` | Default machine ID |
| `TELA_TOKEN` | Default auth token |
| `TELA_OWNER_TOKEN` | Owner/admin token (preferred by `tela admin`) |
| `TELA_PROFILE` | Default connection profile name |
| `TELA_MTU` | WireGuard tunnel MTU (default 1100) |
| `TELA_MOUNT_PORT` | WebDAV listen port for `tela mount` (default 18080) |

### Config and credential storage

| File | Platform | Path |
|------|----------|------|
| Credentials | Linux/macOS | `~/.tela/credentials.yaml` |
| | Windows | `%APPDATA%\tela\credentials.yaml` |
| Remotes config | Linux/macOS | `~/.tela/config.yaml` |
| | Windows | `%APPDATA%\tela\config.yaml` |
| Hub aliases | Linux/macOS | `~/.tela/hubs.yaml` |
| | Windows | `%APPDATA%\tela\hubs.yaml` |
| Connection profiles | Linux/macOS | `~/.tela/profiles/<name>.yaml` |
| | Windows | `%APPDATA%\tela\profiles\<name>.yaml` |

Token lookup order: `-token` flag > `TELA_TOKEN` env var > credential store.

```bash
tela login wss://hub.example.com    # store a token
tela logout wss://hub.example.com   # remove stored credentials
```

---

## telad

The agent daemon. Registers machines with a hub and forwards TCP connections
to local services.

### Flags

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `-config <path>` | `TELAD_CONFIG` | (none) | Path to YAML config file |
| `-hub <url>` | `TELA_HUB` | (none) | Hub WebSocket URL |
| `-machine <name>` | `TELA_MACHINE` | (none) | Machine name for hub registry |
| `-token <hex>` | `TELA_TOKEN` | (none) | Hub auth token |
| `-ports <spec>` | `TELAD_PORTS` | (none) | Comma-separated port specs (see below) |
| `-target-host <host>` | `TELAD_TARGET_HOST` | `127.0.0.1` | Target host for services (gateway mode) |
| `-mtu <n>` | `TELAD_MTU` | `1100` | WireGuard tunnel MTU |
| `-v` | | | Verbose logging |

### Port spec format

```
port[:name[:description]]
```

Examples: `22`, `22:SSH`, `22:SSH:OpenSSH server`, `22:SSH,3389:RDP`

### Config file (`telad.yaml`)

```yaml
hub: wss://hub.example.com
token: <default-token>

update:
  channel: dev     # dev | beta | stable

machines:
  - name: web01
    displayName: "Web Server 01"
    hostname: web01.internal   # override OS hostname (useful in containers)
    os: linux                  # defaults to runtime OS
    tags: [production, web]
    location: "US-East"
    owner: ops-team
    target: 127.0.0.1          # set to a remote IP for gateway mode
    token: <override>          # per-machine token override
    services:
      - port: 22
        name: SSH
        description: "OpenSSH server"
    # ports: [22, 3389]        # alternative to services; generates minimal entries
```

**Machine fields**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Machine ID in the hub registry |
| `displayName` | No | Human-friendly name for UIs |
| `hostname` | No | Overrides `os.Hostname()` |
| `os` | No | OS identifier; defaults to `runtime.GOOS` |
| `tags` | No | Arbitrary string tags |
| `location` | No | Physical or logical location string |
| `owner` | No | Owner identifier string |
| `target` | No | Target host; defaults to `127.0.0.1` |
| `token` | No | Per-machine token (overrides top-level `token`) |
| `ports` | * | Simple port list, e.g. `[22, 3389]` |
| `services` | * | Detailed service descriptors (port, name, description) |
| `gateway` | No | Path-based HTTP reverse proxy config (see below) |
| `upstreams` | No | Dependency forwarding config (see below) |
| `shares` | No | Named file share list (see below) |

\* Either `ports` or `services` is required. If both are present, `services` takes precedence.

### File share config

```yaml
shares:
  - name: shared
    path: /home/shared       # absolute path; created on startup if missing
    writable: false
    maxFileSize: 50MB
    maxTotalSize: 1GB
    allowDelete: false
    allowedExtensions: []    # empty = all allowed
    blockedExtensions: [".exe", ".bat", ".cmd", ".ps1", ".sh"]
  - name: uploads
    path: /home/uploads
    writable: true
    allowDelete: true
```

Each entry in `shares` is a named share. Clients navigate to a share by name before browsing files.

| Field | Default | Description |
|-------|---------|-------------|
| `name` | (required) | Share name shown to clients |
| `path` | (required) | Absolute path to the shared directory |
| `writable` | `false` | Allow uploads, mkdir, rename, move |
| `maxFileSize` | `50MB` | Per-file upload limit |
| `maxTotalSize` | (none) | Total directory size limit |
| `allowDelete` | `false` | Allow deletion (requires `writable: true`) |
| `allowedExtensions` | `[]` | Whitelist; empty means all allowed |
| `blockedExtensions` | see above | Blacklist; applied after allowlist |

The deprecated `fileShare:` (singular) key is accepted and synthesized as a share named `legacy`. It will be removed in 1.0.

### Upstream config

```yaml
upstreams:
  - port: 41000
    name: service1
    target: localhost:41000
  - port: 1433
    name: db
    target: int-db.local:1433
```

| Field | Required | Description |
|-------|----------|-------------|
| `port` | Yes | Local port to listen on |
| `target` | Yes | Address to forward to (`host:port`) |
| `name` | No | Label for logging |

### Gateway config

```yaml
gateway:
  port: 8080
  routes:
    - path: /api/
      target: 4000
    - path: /metrics/
      target: 4100
    - path: /
      target: 3000
```

| Field | Required | Description |
|-------|----------|-------------|
| `port` | Yes | Port to listen on inside the tunnel |
| `routes[].path` | Yes | URL path prefix; longest match wins |
| `routes[].target` | Yes | Local port to proxy to |

### `telad service` subcommands

| Command | Description |
|---------|-------------|
| `telad service install -config <path>` | Install as an OS service from config file |
| `telad service install -hub <url> -machine <name> -ports <spec>` | Install with inline config |
| `telad service start` | Start the service |
| `telad service stop` | Stop the service |
| `telad service restart` | Restart the service |
| `telad service status` | Show current state |
| `telad service uninstall` | Remove the service |

Config location when installed as a service:

| Platform | Path |
|----------|------|
| Linux/macOS | `/etc/tela/telad.yaml` |
| Windows | `%ProgramData%\Tela\telad.yaml` |

### `telad update`

```bash
telad update                              # update from the configured channel
telad update -channel <dev|beta|stable>   # one-shot channel override
telad update -dry-run                     # show what would happen
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `TELAD_CONFIG` | (none) | Path to YAML config file |
| `TELA_HUB` | (none) | Hub WebSocket URL |
| `TELA_MACHINE` | (none) | Machine name |
| `TELA_TOKEN` | (none) | Hub auth token |
| `TELAD_PORTS` | (none) | Comma-separated port specs |
| `TELAD_TARGET_HOST` | `127.0.0.1` | Target host for services |
| `TELAD_MTU` | `1100` | WireGuard tunnel MTU |

### Credential store

Store a token so it does not need to appear in config files or shell history:

```bash
sudo telad login -hub wss://hub.example.com   # Linux/macOS (requires elevation)
telad login -hub wss://hub.example.com         # Windows (run as Administrator)
telad logout -hub wss://hub.example.com
```

| Platform | User-level | System-level |
|----------|-----------|--------------|
| Linux/macOS | `~/.tela/credentials.yaml` | `/etc/tela/credentials.yaml` |
| Windows | `%APPDATA%\tela\credentials.yaml` | `%ProgramData%\Tela\credentials.yaml` |

Token lookup order: `-token` flag > `TELA_TOKEN` env var > system credential store > user credential store.

---

## telahubd

The hub server. Listens for WebSocket connections from agents and clients,
relays encrypted traffic, and serves the admin API and web console.

### Flags

| Flag | Description |
|------|-------------|
| `-config <path>` | Path to YAML config file |
| `-v` | Verbose logging |

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `TELAHUBD_PORT` | `80` | HTTP+WS listen port |
| `TELAHUBD_UDP_PORT` | `41820` | UDP relay port |
| `TELAHUBD_UDP_HOST` | (empty) | Public IP or hostname advertised in UDP offers (set when behind a proxy that does not forward UDP) |
| `TELAHUBD_NAME` | (empty) | Display name for this hub |
| `TELAHUBD_WWW_DIR` | (empty) | Serve console from disk instead of the embedded filesystem |
| `TELA_OWNER_TOKEN` | (empty) | Bootstrap owner token on first startup; ignored if tokens already exist |
| `TELAHUBD_PORTAL_URL` | (empty) | Portal URL for auto-registration on first startup |
| `TELAHUBD_PORTAL_TOKEN` | (empty) | Portal admin token for registration (used once, not persisted) |
| `TELAHUBD_PUBLIC_URL` | (empty) | Hub's own public URL for portal registration |

### Config file (`telahubd.yaml`)

```yaml
port: 80
udpPort: 41820
udpHost: ""        # set when behind a proxy that does not forward UDP
name: myhub
wwwDir: ""         # omit to use embedded console

update:
  channel: dev     # dev | beta | stable

auth:
  tokens:
    - id: alice
      token: <hex>
      hubRole: owner   # owner | admin | viewer | "" (user)
  machines:
    "*":
      registerToken: <hex>
      connectTokens: [<hex>]
      manageTokens: [<hex>]
    barn:
      registerToken: <hex>
      connectTokens: [<hex>]
      manageTokens: [<hex>]
```

Precedence: environment variables override YAML, YAML overrides built-in defaults.

Config file location when running as a service:

| Platform | Path |
|----------|------|
| Linux/macOS | `/etc/tela/telahubd.yaml` |
| Windows | `%ProgramData%\Tela\telahubd.yaml` |

### `telahubd user` subcommands

Local token management on the hub machine. All subcommands accept `-config <path>`.

| Command | Description |
|---------|-------------|
| `telahubd user bootstrap` | Generate the first owner token (printed once) |
| `telahubd user add <id> [-role owner\|admin]` | Add a token identity |
| `telahubd user list [-json]` | List identities |
| `telahubd user grant <id> <machine>` | Grant connect access to a machine |
| `telahubd user revoke <id> <machine>` | Revoke connect access |
| `telahubd user rotate <id>` | Regenerate the token for an identity |
| `telahubd user remove <id>` | Remove an identity |
| `telahubd user show-owner` | Print the owner token |
| `telahubd user show-viewer` | Print the console viewer token |

Changes take effect immediately. No hub restart required.

### `telahubd portal` subcommands

| Command | Description |
|---------|-------------|
| `telahubd portal add <name> <url>` | Register the hub with a portal |
| `telahubd portal list [-json]` | List portal registrations |
| `telahubd portal remove <name>` | Remove a portal registration |
| `telahubd portal sync` | Push viewer token to all registered portals |

### `telahubd service` subcommands

| Command | Description |
|---------|-------------|
| `telahubd service install -config <path>` | Install as an OS service |
| `telahubd service start` | Start the service |
| `telahubd service stop` | Stop the service |
| `telahubd service restart` | Restart the service |
| `telahubd service uninstall` | Remove the service |

### `telahubd update`

```bash
telahubd update                              # update from the configured channel
telahubd update -channel <dev|beta|stable>   # one-shot channel override
telahubd update -dry-run                     # show what would happen
```

### Firewall requirements

| Port | Protocol | Notes |
|------|----------|-------|
| 443 (or configured port) | TCP | WebSocket connections from `tela` and `telad` |
| 41820 (or `TELAHUBD_UDP_PORT`) | UDP | Optional; improves latency. Set `TELAHUBD_UDP_HOST` when behind a proxy. |

No inbound ports are needed on machines running `telad`.

### Admin API

All admin endpoints require an owner or admin token via `Authorization: Bearer <token>`.

**Unified access (identity + per-machine permissions)**

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/admin/access` | List all identities with permissions |
| GET | `/api/admin/access/{id}` | Get one identity |
| PATCH | `/api/admin/access/{id}` | Rename: `{"id":"new-name"}` |
| DELETE | `/api/admin/access/{id}` | Remove identity and all ACL entries |
| PUT | `/api/admin/access/{id}/machines/{m}` | Set permissions: `{"permissions":["connect","manage"]}` |
| DELETE | `/api/admin/access/{id}/machines/{m}` | Revoke all permissions on a machine |

**Token management**

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/admin/tokens` | List token identities |
| POST | `/api/admin/tokens` | Add a token identity (returns full token once) |
| DELETE | `/api/admin/tokens/{id}` | Remove a token identity |
| POST | `/api/admin/rotate/{id}` | Regenerate a token |

**Portal management**

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/admin/portals` | List portal registrations |
| POST | `/api/admin/portals` | Add or update a portal registration |
| DELETE | `/api/admin/portals/{name}` | Remove a portal registration |

**Agent management and pairing**

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET/POST | `/api/admin/agents/{machine}/{action}` | Proxy management request to agent |
| POST | `/api/admin/pair-code` | Generate a pairing code |
| POST | `/api/pair` | Exchange a pairing code for a token (no auth required) |

**Self-update**

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/admin/update` | Channel, current version, latest version, update available |
| PATCH | `/api/admin/update` | Set channel: `{"channel":"beta"}` |
| POST | `/api/admin/update` | Trigger update to channel HEAD |

**Public endpoints**

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/api/status` | viewer+ | Machines, services, session status |
| GET | `/api/history` | viewer+ | Recent connection events |
| GET | `/.well-known/tela` | none | Hub discovery (RFC 8615) |
| GET | `/api/hubs` | viewer+ | Hub listing for portal/CLI resolution |

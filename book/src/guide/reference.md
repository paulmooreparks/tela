# Appendix A: CLI reference

Flags, subcommands, environment variables, and config schemas for `tela`,
`telad`, and `telahubd`. For narrative explanations, see the User Guide and
How-to Guides.

---

## tela

The client CLI. Opens WireGuard tunnels to machines through a hub and binds
local TCP listeners for their services. Requires no admin rights or kernel drivers.

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
| `fileShare` | No | File sharing config (see below) |

\* Either `ports` or `services` is required. If both are present, `services` takes precedence.

### File share config

```yaml
fileShare:
  enabled: true
  directory: /home/shared    # absolute path; created on startup if missing
  writable: false
  maxFileSize: 50MB
  maxTotalSize: 1GB
  allowDelete: false
  allowedExtensions: []      # empty = all allowed
  blockedExtensions: [".exe", ".bat", ".cmd", ".ps1", ".sh"]
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable file sharing for this machine |
| `directory` | (required) | Absolute path to the shared directory |
| `writable` | `false` | Allow uploads, mkdir, rename, move |
| `maxFileSize` | `50MB` | Per-file upload limit |
| `maxTotalSize` | (none) | Total directory size limit |
| `allowDelete` | `false` | Allow deletion (requires `writable: true`) |
| `allowedExtensions` | `[]` | Whitelist; empty means all allowed |
| `blockedExtensions` | see above | Blacklist; applied after allowlist |

### Upstream config

Forwards local ports to configurable targets, letting you rewire service
dependencies without changing service code.

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

Path-based HTTP reverse proxy that routes requests to different local services
by URL prefix.

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
| `fileShare` | No | File sharing config (see below) |

\* Either `ports` or `services` is required. If both are present, `services` takes precedence.

### File share config

```yaml
fileShare:
  enabled: true
  directory: /home/shared    # absolute path; created on startup if missing
  writable: false
  maxFileSize: 50MB
  maxTotalSize: 1GB
  allowDelete: false
  allowedExtensions: []      # empty = all allowed
  blockedExtensions: [".exe", ".bat", ".cmd", ".ps1", ".sh"]
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable file sharing for this machine |
| `directory` | (required) | Absolute path to the shared directory |
| `writable` | `false` | Allow uploads, mkdir, rename, move |
| `maxFileSize` | `50MB` | Per-file upload limit |
| `maxTotalSize` | (none) | Total directory size limit |
| `allowDelete` | `false` | Allow deletion (requires `writable: true`) |
| `allowedExtensions` | `[]` | Whitelist; empty means all allowed |
| `blockedExtensions` | see above | Blacklist; applied after allowlist |

### Upstream config

Forwards local ports to configurable targets, letting you rewire service
dependencies without changing service code.

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

Path-based HTTP reverse proxy that routes requests to different local services
by URL prefix.

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
| `telahubd user add <id> [-role owner\|admin\|viewer]` | Add a token identity |
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
```

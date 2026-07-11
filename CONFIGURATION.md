# Tela configuration reference

This document describes the configuration files used by the Tela stack:

- Local CLI config files used by `tela` (portal login + hub aliases)
- Daemon config used by `telad`
- Hub config used by `telahubd`
- Portal hub directory config used by Awan Saya

If you’re specifically looking for how to create/edit `hubs.yaml`, start at: **`hubs.yaml` (hub aliases)**.

## Where configs live (by OS)

### `tela` CLI config directory

`tela` stores its local configuration in:

- **Windows:** `%APPDATA%\tela\`
- **Linux/macOS:** `~/.tela/`

Files in this directory:

- `config.yaml` (portal login)
- `hubs.yaml` (local hub alias fallback)

## `hubs.yaml` (hub aliases)

**Purpose:** Local, offline fallback mapping from a short hub name (alias) to a WebSocket URL.

**When it's used:** Only when the `-hub` value is **not** a `ws://` or `wss://` URL and the remote lookup fails/unavailable.

Resolution order in the CLI:

1. If `-hub` starts with `ws://` or `wss://` → use as-is.
2. Else try configured remotes (requires `config.yaml` from `tela remote add`).
3. Else fall back to `hubs.yaml`.

**File location:**

- Windows: `%APPDATA%\tela\hubs.yaml`
- Linux/macOS: `~/.tela/hubs.yaml`

**Schema:**

```yaml
hubs:
  <alias>:
    url: <ws-or-wss-url>
    pin: sha256:<hex>          # optional TLS SPKI pin
```

- `hubs` is a mapping/dictionary.
- Alias lookup is **case-sensitive** (e.g. `OwlsNest` and `owlsnest` are different).
- The `url` is used **exactly as written**. (Unlike portal entries, it is not converted from `https://` → `wss://`.)
- `pin`, when set, is the SHA-256 hash of the hub's TLS leaf certificate's Subject Public Key Info (SPKI), formatted as `sha256:<lowercase hex>`. The dialer refuses connections whose presented certificate does not match this pin. Set with `tela login <url> -pin <fingerprint>` or `tela pin <url> <fingerprint>`. See the *credentials.yaml* section below for the full pin model.

**Example (local dev):**

```yaml
hubs:
  local:
    url: ws://localhost
  gohub-local:
    url: ws://localhost
```

**Example (production with TLS pin):**

```yaml
hubs:
  owlsnest:
    url: wss://tela.awansaya.net
    pin: sha256:1a2b3c4d5e6f7890abcdef0123456789abcdef0123456789abcdef0123456789
  gohub:
    url: wss://gohub.parkscomputing.com
```

**Pre-0.16 flat shape (still accepted on read):**

```yaml
hubs:
  owlsnest: wss://tela.awansaya.net
```

The flat `<alias>: <url>` shape is migrated transparently when read. New entries written by `tela login` or by editing the file should use the structured shape so they can carry a `pin` field.

### Creating `hubs.yaml`

1. Create the config directory:
   - Windows (PowerShell): `mkdir $env:APPDATA\tela -Force`
   - Linux/macOS: `mkdir -p ~/.tela`
2. Create the file named `hubs.yaml` in that directory.
3. Add a `hubs:` mapping (see examples above).

### Editing tips

- Prefer `wss://` for Internet-reachable hubs (TLS).
- Use `ws://` only for local/testing.
- Keep aliases short and stable (they’re what you pass to `tela ... -hub <alias>`).

## `config.yaml` (hub directory remotes)

**Purpose:** Stores remote credentials and discovered endpoints so `tela` can resolve hub names.

**File location:**

- Windows: `%APPDATA%\tela\config.yaml`
- Linux/macOS: `~/.tela/config.yaml`

**How it's created:** `tela remote add <name> <url>` discovers endpoints via `/.well-known/tela` (RFC 8615), prompts for a token, and writes this file.

**Schema:**

```yaml
remotes:
  awansaya:
    url: https://awansaya.net
    token: ""                # empty token = open-mode remote
    hub_directory: /api/hubs # discovered via /.well-known/tela
```

Notes:

- `url` should be `http(s)://...`.
- `token` is optional; if present it's sent as `Authorization: Bearer <token>`.
- `hub_directory` is auto-populated during `tela remote add` via the well-known endpoint. If `/.well-known/tela` is unavailable, defaults to `/api/hubs`.

## `credentials.yaml` (credential store)

**Purpose:** Stores hub authentication tokens so you don't need to pass `-token` on every command.

**File locations:**

- User-level: `%APPDATA%\tela\credentials.yaml` (Windows) or `~/.tela/credentials.yaml` (Unix)
- System-level: `%ProgramData%\Tela\credentials.yaml` (Windows) or `/etc/tela/credentials.yaml` (Unix)

**How it's created:** `tela login <hub-url>` or `telad login -hub <hub-url>` (telad requires elevation).

**Schema:**

```yaml
hubs:
  wss://hub.example.com:
    token: 7bf042ceb070136fec15fdd49797c486225fbe62b6cfd3bb4649f04b32446d62
    identity: alice
    pin: sha256:1a2b3c4d5e6f7890abcdef0123456789abcdef0123456789abcdef0123456789

# Optional: which release channel the tela client (and TelaVisor) follows
# for self-update. Accepts dev (default), beta, stable, or a custom channel
# name. Hub and agent channels are configured separately in their own YAML
# files.
update:
  channel: dev
  # sources:                                   # optional per-channel URL overrides
  #   dev: https://my-fork.example.com/channels/
```

Notes:

- The `hubs` mapping stores credentials by hub URL (normalized: trailing slashes removed, schemes lowercased).
- `token` is required; `identity` and `pin` are optional.
- `pin`, when set, is the SHA-256 hash of the hub's TLS leaf certificate's Subject Public Key Info (SPKI), formatted as `sha256:<lowercase hex>`. The dialer refuses connections whose presented certificate's SPKI does not match. Pinning the SPKI (rather than the whole certificate) survives certificate renewal with the same key, which is the desired behavior. Manage with `tela login <url> -pin <fingerprint>` or the standalone `tela pin <url> [fingerprint]` command (no fingerprint = inspect, fingerprint = set, `-clear` = remove). On a successful TOFU connect to a hub with no pin configured, `tela connect` logs the captured fingerprint and the exact `tela pin ...` command to enforce it.
- File permissions: 0600 (user-level) or 0644 (system-level, for SYSTEM account read access).
- The `update` block is read by `tela channel`, `tela update`, and TelaVisor's
  Application Settings → Release channel selector. It is the *client's* channel
  preference; hubs and agents have their own under their respective YAML files.
- Set or change with `tela channel set <name>` (no need to edit by hand).

**Using the credential store:**

1. Store a token:
   ```bash
   tela login wss://hub.example.com
   # Prompts for token and optional identity
   ```

2. Subsequent commands find the token automatically:
   ```bash
   tela connect -hub wss://hub.example.com -machine barn -ports 22:SSH
   # No -token flag needed
   ```

3. Remove a stored credential:
   ```bash
   tela logout wss://hub.example.com
   ```

**Token lookup precedence:**

1. `-token` flag (explicit)
2. `TELA_TOKEN` environment variable
3. Credential store (user then system)

`telad login` stores in the system credential store (requires elevation) and persists across service restarts.

## Connection profiles (`profiles/<name>.yaml`)

**Purpose:** Defines one or more hub/machine connections that `tela connect -profile <name>` opens in parallel, each with its own WireGuard tunnel and auto-reconnect.

**File locations:**

- Windows: `%APPDATA%\tela\profiles\<name>.yaml`
- Linux/macOS: `~/.tela/profiles/<name>.yaml`

An explicit file path can also be passed: `tela connect -profile /path/to/profile.yaml`

**How it's created:** `tela profile create <name>`, or by writing the file directly.

**Schema:**

```yaml
id: my-profile              # optional: stable identifier for this profile
name: "My Profile"          # optional: display name
mtu: 1100                   # optional: WireGuard tunnel MTU (default 1100)

connections:
  - hub: wss://hub.example.com    # or a short name resolved via a configured remote
    machine: web01
    token: ${WEB_TOKEN}           # ${VAR} expansion is supported; omit if stored in credentials.yaml
    services:                     # omit to forward all ports the agent advertises
      - remote: 22                # forward by port number
        local: 2201               # optional: remap to a different local port (defaults to remote)
      - name: postgres            # forward by service name (resolved via hub API at connect time)

# Optional: start a WebDAV mount when the profile connects
mount:
  mount: T:                 # drive letter (Windows) or directory path (macOS/Linux)
  port: 18080               # WebDAV listen port (default 18080)
  auto: true                # mount automatically on connect

# Optional: DNS configuration
dns:
  loopback_prefix: "127.88"  # prefix used by 'tela dns hosts' to generate /etc/hosts entries; does NOT control port binding
```

**Top-level fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `id` | No | Stable identifier for this profile |
| `name` | No | Display name |
| `mtu` | No | WireGuard tunnel MTU; overrides the `-mtu` flag default of 1100 |
| `connections` | Yes | List of hub/machine connections |
| `mount` | No | WebDAV mount to start automatically on connect |
| `dns` | No | DNS configuration. `loopback_prefix` is used by `tela dns hosts` to generate /etc/hosts entries for named access; it does not control where services bind. |

**Connection entry fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `hub` | Yes | Hub WebSocket URL (`wss://...`) or short name |
| `hubId` | No | Stable hub UUID; populated lazily by `tela`, do not set manually |
| `machine` | Yes | Machine name as registered with the hub |
| `agentId` | No | Stable agent UUID; populated lazily by `tela`, do not set manually |
| `token` | No | Auth token; omit if stored in `credentials.yaml` |
| `address` | No | Override the loopback address for this machine (must be in `127.0.0.0/8`) |
| `services` | No | Port or service filter; omit to forward everything the agent advertises |
| `services[].remote` | * | Remote port number to forward |
| `services[].local` | No | Local port to bind (defaults to `remote`) |
| `services[].name` | * | Service name to resolve via the hub API |

\* Each service entry needs either `remote` or `name`, not both.

**Mount fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `mount` | No | Drive letter (Windows `T:`) or directory path to mount |
| `port` | No | WebDAV listen port (default 18080) |
| `auto` | No | If true, mount automatically when the profile connects |

**Notes:**

- Profile YAML supports `${VAR}` expansion so tokens can stay out of the file.
- Multiple connections in one profile open in parallel; each reconnects independently on disconnect.
- The default profile can be set with the `TELA_PROFILE` environment variable.

## `telad.yaml` (daemon / agent config)

**Purpose:** Runs one `telad` process that can register one or more machines to a hub.

**Where it’s used:**

- Running directly: `telad -config telad.yaml`
- Service mode: the service reads from the system-wide path (see below)

**Top-level schema:**

```yaml
hub: ws://localhost
token: ""         # optional default token for all machines

# Optional: which release channel telad's self-update follows.
# Accepts dev (default), beta, stable, or a custom channel name.
# See RELEASE-PROCESS.md for the channel model.
update:
  channel: dev
  # sources:                                                # optional per-channel URL overrides
  #   dev: https://my-fork.example.com/channels/

machines:
  - name: barn
    # ports: [22, 3389]
    # services: [{ port: 22, name: SSH }]
    # target: 127.0.0.1
```

**Update block (`update:`)**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `channel` | string | `dev` | Release channel for self-update: `dev`, `beta`, `stable`, or a custom channel name. |
| `sources` | map[name]url | (none) | Per-channel manifest base URL overrides. Built-in channels (`dev`, `beta`, `stable`) fall back to the baked-in GitHub releases URL when absent. Custom channel names require an entry here (or in the `channel sources` CLI) to resolve. |

> **Removed in 0.13:** The pre-0.12 `manifestBase` scalar field is no
> longer recognised. yaml.v3 silently ignores unknown fields on load,
> so an old config still parses, but a custom channel pointed at by
> `manifestBase` will fail its next manifest fetch with an empty URL.
> Migrate by writing a `sources` entry (or running `tela channel
> sources set <channel> <url>`) before upgrading from 0.12 to 0.13+.

The configured channel is read by the `telad update` CLI subcommand, the
`telad channel` CLI subcommand (show / set / show-manifest), the
`update` and `update-channel` mgmt actions, and TelaVisor's Agent
Settings → Release channel dropdown.

**Machine fields:**

- `name` (required): machine ID registered with the hub.
- `displayName` (optional): nicer name for UIs.
- `hostname` (optional): overrides OS hostname (useful in containers).
- `os` (optional): defaults to the runtime OS (`windows`, `linux`, `darwin`, …).
- `tags` (optional): list of strings.
- `location` (optional): free-form string.
- `owner` (optional): free-form string.
- `target` (optional): where the real services run; defaults to `127.0.0.1`.
- `token` (optional): per-machine token override; defaults to top-level `token`.
- Either `ports` or `services` is required:
  - `ports`: list of TCP ports (e.g. `[22, 3389]`).
  - `services`: list of service descriptors (below).

**Service descriptor schema:**

```yaml
services:
  - port: 22
    proto: tcp
    name: SSH
    description: OpenSSH
```

Notes:

- If you provide `services` but omit `ports`, `telad` derives `ports` automatically.
- If you provide `ports` but omit `services`, `telad` generates minimal service entries (port-only).

**File share configuration:**

Each machine can expose one or more sandboxed directories for file transfer through the WireGuard tunnel. File sharing is off by default and must be explicitly enabled.

```yaml
shares:
  - name: docs
    path: /home/shared/docs
    writable: true
    maxFileSize: 50MB
    maxTotalSize: 1GB
    allowDelete: false
    allowedExtensions: []
    blockedExtensions: [".exe", ".bat", ".cmd", ".ps1", ".sh"]
  - name: uploads
    path: /home/shared/uploads
    writable: true
    allowDelete: true
```

Share fields:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | (required) | Share name. Used in WebDAV paths (`/machine/share/path`) and the `-share NAME` flag on `tela files` commands. |
| `path` | string | (required) | Absolute path to the shared directory. Created on startup if missing. |
| `writable` | bool | `false` | Allows clients to upload files, create directories, rename, and move |
| `maxFileSize` | string | `50MB` | Maximum size of a single uploaded file. Supports KB, MB, GB suffixes. |
| `maxTotalSize` | string | (none) | Maximum total size of all files in the shared directory |
| `allowDelete` | bool | `false` | Allows clients to delete files. Requires `writable: true`. |
| `allowedExtensions` | []string | `[]` | Whitelist of file extensions. Empty means all extensions are allowed (subject to `blockedExtensions`). |
| `blockedExtensions` | []string | see above | Blacklist of file extensions. Applied after `allowedExtensions`. |

**Deprecated:** The `fileShare` (singular) key is still accepted and is synthesized as a share named `legacy`. It will be removed in 1.0. Migrate to the `shares` list.

```yaml
# Deprecated -- use shares instead
fileShare:
  enabled: true
  directory: /home/shared
```

**Example (two machines):**

```yaml
hub: wss://tela.awansaya.net
token: "shared-secret"

machines:
  - name: barn
    displayName: Barn (Windows)
    os: windows
    tags: ["lab", "rdp"]
    ports: [3389]
    target: 192.168.1.10

  - name: nas
    displayName: NAS
    os: linux
    services:
      - port: 22
        name: SSH
      - port: 445
        name: SMB
    target: 192.168.1.50
```

## `telahubd.yaml` (hub server config)

**Purpose:** Configures the Go hub server (`telahubd`).

**Schema:**

```yaml
hubId: ""            # optional: stable identifier for this hub instance
port: 80
udpPort: 41820
udpHost: ""          # public IP/hostname for UDP relay (when behind proxy)
name: owlsnest
wwwDir: ""           # omit to use the embedded console

# Optional: how long graceful shutdown waits for in-flight requests to
# finish after SIGTERM (or a context cancel from a test harness). A
# second signal during the drain forces immediate exit. Accepts any
# Go duration literal: "30s", "2m", "500ms".
shutdownTimeout: 30s

# Optional: which release channel telahubd's self-update follows.
# Accepts dev (default), beta, stable, or a custom channel name.
# See RELEASE-PROCESS.md for the channel model.
update:
  channel: dev
  # sources:                                   # optional per-channel URL overrides
  #   dev: https://my-fork.example.com/channels/

# Optional: turn this hub into a self-hosted release channel server.
# When enabled, telahubd mounts /channels/{name}.json and
# /channels/files/{channel}/{binary} from the directory below. Each
# channel has its own subdirectory under files/. Replaces the
# standalone telachand daemon. See "Self-hosted release channel server"
# below for the full description.
channels:
  enabled: false
  data: /var/lib/telahubd/channels
  publicURL: https://hub.example.net/channels

auth:
  tokens:
    - id: alice
      token: <hex-string>
      hubRole: owner         # "owner" | "admin" | "viewer" | "" (user)
    - id: bob
      token: <hex-string>
      hubRole: ""            # regular user
  machines:
    "*":                     # wildcard - applies to all machines
      registerToken: <token> # only this token may register
      connectTokens:         # tokens allowed to connect
        - <token>
      manageTokens:          # tokens allowed to manage (config, logs, restart)
        - <token>
    barn:
      registerToken: <token>
      connectTokens:
        - <token>
      manageTokens:
        - <token>

# Portal registrations (managed via 'telahubd portal' or 'tela admin portals')
portals:
  awansaya:                        # portal name (key)
    url: https://awansaya.net      # portal base URL
    syncToken: <hex>               # per-hub sync token returned by portal on registration
    hubDirectory: /api/hubs        # hub directory endpoint (discovered via /.well-known/tela)
    # token is the portal admin token used only during registration; not persisted

# Hub bridging (experimental): forward specific machines to a remote hub
bridges:
  - hubId: remote-hub              # identifier of the remote hub
    url: wss://remote-hub.example.com
    token: <token>                 # auth token on the remote hub
    pin: sha256:1a2b3c4d...         # optional TLS SPKI pin for the destination hub
    maxHops: 3                     # maximum relay hops (default 0 = unlimited)
    machines: [web01, db01]        # machines to bridge to the remote hub
```

The `bridges[].pin` field, when set, is the SHA-256 SPKI pin (`sha256:<lowercase hex>`) of the destination hub's TLS leaf certificate. The bridge dial refuses connections whose certificate's SPKI does not match. With no pin configured, the standard CA chain validation applies; the captured fingerprint is logged on the first successful bridge dial so the operator can copy it back into this config to enforce pinning. See [DESIGN-relay-gateway.md](DESIGN-relay-gateway.md) §5.4 for the design discussion.

### Core fields

- `port`, `udpPort`, `udpHost`, `name`, `wwwDir`: same as the corresponding env vars.
- Precedence: **env vars override YAML**, and YAML overrides built-in defaults.
- Supported env vars: `TELAHUBD_PORT`, `TELAHUBD_UDP_PORT`, `TELAHUBD_UDP_HOST`, `TELAHUBD_NAME`, `TELAHUBD_WWW_DIR`.
- Portal-related env vars: `TELAHUBD_PORTAL_URL`, `TELAHUBD_PORTAL_TOKEN`, `TELAHUBD_PUBLIC_URL`.
- `udpHost`: when the hub is behind a proxy or tunnel (e.g. Cloudflare) that doesn't forward UDP, set this to the hub's real public IP or a DNS name that resolves to it. The hub includes this in `udp-offer` messages so clients send UDP to the right address.

### Auth block (`auth:`)

For a conceptual overview of how tokens, roles, and machine permissions work together, see [ACCESS-MODEL.md](ACCESS-MODEL.md).

When `auth:` is absent or has no tokens, the hub runs in **open mode** (no authentication, same behavior as before auth was added). When tokens are present, **every** register and connect request must carry a valid Bearer token.

**`auth.tokens`**: list of token identities:

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | Human-friendly label (e.g. `alice`, `ci-bot`) |
| `token` | yes | Hex secret (64-char recommended). Generated by `tela admin tokens add` or `openssl rand -hex 32` |
| `hubRole` | no | `"owner"` \| `"admin"` \| `"viewer"` \| `""` (regular user) |
| `issuedAt` | no | RFC 3339 timestamp the token was created. Pre-0.16 entries on disk lack this field and keep it absent; new entries (created via `tela admin tokens add` or pairing) get it at creation time. Rotating a pre-0.16 entry populates this field with the rotation time. |
| `expiresAt` | no | RFC 3339 timestamp after which the token stops authenticating. Absent means no expiry. |
| `revokedAt` | no | RFC 3339 timestamp the token was revoked. Set by `POST /api/admin/tokens/{id}/revoke`; cleared by a subsequent rotate. The entry stays in this list so the audit trail is preserved. |

**`auth.machines`**: per-machine access control:

| Field | Required | Description |
|-------|----------|-------------|
| `registerToken` | no | If set, only this token may register (or re-register) this machine |
| `connectTokens` | no | List of tokens allowed to connect to this machine |
| `manageTokens` | no | List of tokens allowed to manage this machine (view/edit config, view logs, restart) |

Use `"*"` as the machine key for a wildcard rule that applies to all machines. Owner and admin role tokens implicitly have manage access to all machines.

### Auth evaluation order

1. If `auth.tokens` is empty → open mode, allow everything. (Note: on first startup with no tokens, the hub auto-generates an owner token, so open mode requires deliberate configuration.)
2. Incoming request must carry a valid token via `Authorization: Bearer <token>` header (or cookie for browser sessions).
3. Tokens with `revokedAt` set or with `expiresAt` in the past are denied immediately, before any role or machine-permission check.
4. Owner/admin tokens bypass per-machine checks.
5. For `register`: check `machines[machineId].registerToken` then `machines["*"].registerToken`.
6. For `connect`: check `machines[machineId].connectTokens` then `machines["*"].connectTokens`.

### Environment variable bootstrap (`TELA_OWNER_TOKEN`)

For Docker deployments where you don't have shell access to the hub, you can bootstrap authentication via an environment variable:

1. Generate a token locally:
   ```bash
   openssl rand -hex 32
   ```
2. Set `TELA_OWNER_TOKEN` in your Docker Compose environment:
   ```yaml
   environment:
     - TELA_OWNER_TOKEN=<your-generated-token>
   ```
3. On first startup (when no tokens exist in the config), the hub automatically:
   - Creates an `owner` identity with the provided token
   - Adds a wildcard `*` machine ACL granting the owner full access
   - Persists the config to disk
4. On subsequent startups, the env var is ignored (tokens already exist).

Once bootstrapped, use `tela admin` commands to manage tokens remotely. No shell access to the hub is needed.

`tela admin` sub-commands resolve the auth token in this order: `-token` flag > `TELA_OWNER_TOKEN` env var > `TELA_TOKEN` env var.

### Console viewer token

When auth is enabled, the hub auto-generates a `console-viewer` identity with the `viewer` role at startup. This token is injected into the built-in web console so it can call `/api/status` without manual configuration. The viewer role grants read-only access to status endpoints but cannot register machines or manage tokens.

### Docker config persistence

In Docker deployments, the hub persists its YAML config at `/app/data/telahubd.yaml` on a named volume (`hub-data`). This ensures auth config survives container recreation.

### Admin REST API

When auth is enabled, the hub exposes admin endpoints for remote management. All admin endpoints require an owner or admin token.

**Unified access API** (recommended). Each access entry represents one identity and its per-machine permissions:

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/admin/access` | List all identities with their per-machine permissions |
| `GET` | `/api/admin/access/{id}` | Get one identity's access entry |
| `PATCH` | `/api/admin/access/{id}` | Update identity (rename: `{"id":"new-name"}`) |
| `DELETE` | `/api/admin/access/{id}` | Remove identity and scrub all ACL references |
| `PUT` | `/api/admin/access/{id}/machines/{m}` | Set permissions: `{"permissions":["connect","manage"]}` |
| `DELETE` | `/api/admin/access/{id}/machines/{m}` | Revoke all permissions on a machine |

**Token management:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/admin/tokens` | List all token identities (token values are previewed, not exposed) |
| `POST` | `/api/admin/tokens` | Add a new token identity (returns the full token once) |
| `DELETE` | `/api/admin/tokens/{id}` | Remove a token identity and clean up its ACL references |

Changes made through the admin API take effect immediately (hot-reload) with no hub restart needed. See the `tela admin access` CLI commands for the corresponding client interface.

**Using a config file:**

- `telahubd -config telahubd.yaml`

### Service-mode config location

When running as an OS service, `telad` and `telahubd` read their YAML from a system-wide directory:

- Windows: `%ProgramData%\Tela\telad.yaml` and `%ProgramData%\Tela\telahubd.yaml`
- Linux/macOS: `/etc/tela/telad.yaml` and `/etc/tela/telahubd.yaml`

## Self-hosted release channel server

Self-hosted release channel hosting is a feature of `telahubd` itself as
of 0.12. A dedicated `telachand` daemon is no longer needed; enable the
`channels:` block in `telahubd.yaml` to have the hub serve channel
manifests and binary downloads under `/channels/`.

**Config block** (add to `telahubd.yaml`):

```yaml
channels:
  enabled: true
  data: /var/lib/telahubd/channels    # holds {channel}.json and files/{channel}/{binary}
  publicURL: https://hub.example.net/channels
```

**Field reference:**

| Field | Default | Description |
|-------|---------|-------------|
| `channels.enabled` | `false` | Mount `/channels/` routes when true |
| `channels.data` | (none) | Directory holding manifests at the root and binaries under `files/` |
| `channels.publicURL` | (none) | External URL prefix written into generated manifests as `downloadBase`. Required for `telahubd channels publish` unless `-base-url` is passed on the command line. |

**URL layout:**

| Path | Served content |
|------|----------------|
| `GET /channels/{channel}.json` | Manifest file written by `telahubd channels publish` |
| `GET /channels/files/` | Directory listing of all channels that have any binaries uploaded |
| `GET /channels/files/{channel}/` | Directory listing of binaries for that channel |
| `GET /channels/files/{channel}/{binary}` | Binary file under `{data}/files/{channel}/` |

Each channel has its own subdirectory under `files/`, so two channels
can hold different binaries under the same filename without collision.

Endpoints are public (no auth, CORS wildcard) by design — release
manifests are world-readable. Do not put anything in `channels.data`
you would not want served.

**Publishing remotely** (owner/admin auth required):

| Path | Method | Purpose |
|------|--------|---------|
| `/api/admin/channels/files/{channel}/{name}` | PUT | Upload a binary (request body = file bytes). Writes atomically to `channels.data/files/{channel}/{name}`. 500 MiB max. |
| `/api/admin/channels/publish` | POST | Hash everything in `channels.data/files/{channel}/` and write `{channel}.json`. Body: `{"channel":"local","tag":"v0.12.0-local.1","baseUrl":"..."}`. `baseUrl` is optional and defaults to `channels.publicURL/files/{channel}/`. |

A build pipeline running on a separate host PUTs each binary to the
upload endpoint, then POSTs to `/publish` to regenerate the manifest.
No SSH, tunnel, or file-share mount is needed — the hub's admin auth
is the only credential. See `.vscode/publish-dev.ps1` in the tela
repo for a reference implementation.

**Pointing tela, telad, or telahubd at a self-hosted channel server:**

Set `update.sources[<channel>]` in each binary's config (or in
`credentials.yaml` for the `tela` client and TelaVisor):

```yaml
# telad.yaml, telahubd.yaml, or credentials.yaml
update:
  channel: mychannel
  sources:
    mychannel: https://hub.example.net/channels/
```

Or use the `channel sources` subcommand, which is available on all three
binaries and accepts the same shape:

```bash
telahubd channel sources set mychannel https://hub.example.net/channels/
telad channel sources set mychannel https://hub.example.net/channels/
tela channel sources set mychannel https://hub.example.net/channels/
```

## Awan Saya: `portal/config.json` (hub directory)

**Repo:** Awan Saya

**File location:** `awansaya/www/portal/config.json`

**Purpose:** The portal’s hub directory, served at `GET /api/hubs`.

**Schema:**

```json
{
  "hubs": [
    { "name": "owlsnest", "url": "https://tela.awansaya.net", "viewerToken": "<hex>" }
  ]
}
```

Notes:

- `url` is the hub's public URL. The portal server uses it to proxy status requests.
- `viewerToken` (optional) is the hub's viewer token. The portal server includes it when proxying `/api/status` and `/api/history` so that auth-enabled hubs return data. Tokens are never exposed to the browser.
- When `tela` resolves hubs via a portal, it converts `https://` → `wss://` (and `http://` → `ws://`) automatically.
- You can manage this file by:
  - Using the portal UI "Add Hub" form (which calls `POST /api/hubs`), or
  - Editing it directly.
- Set `AWANSAYA_API_TOKEN` on the portal server (via a `.env` file) to require `Authorization: Bearer <token>` for adding/removing hubs. Reading the hub directory (`GET /api/hubs`) is always open.

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
  <alias>: <ws-or-wss-url>
```

- `hubs` is a mapping/dictionary.
- Alias lookup is **case-sensitive** (e.g. `OwlsNest` and `owlsnest` are different).
- The URL is used **exactly as written**. (Unlike portal entries, it is not converted from `https://` → `wss://`.)

**Example (local dev):**

```yaml
hubs:
  local: ws://localhost:8080
  gohub-local: ws://localhost:8080
```

**Example (production):**

```yaml
hubs:
  owlsnest: wss://tela.awansaya.net
  gohub: wss://gohub.parkscomputing.com
```

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

## `telad.yaml` (daemon / agent config)

**Purpose:** Runs one `telad` process that can register one or more machines to a hub.

**Where it’s used:**

- Running directly: `telad -config telad.yaml`
- Service mode: the service reads from the system-wide path (see below)

**Top-level schema:**

```yaml
hub: ws://localhost:8080
token: ""         # optional default token for all machines

machines:
  - name: barn
    # ports: [22, 3389]
    # services: [{ port: 22, name: SSH }]
    # target: 127.0.0.1
```

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
port: 8080
udpPort: 41820
name: owlsnest
wwwDir: ./www

auth:
  tokens:
    - id: alice
      token: <hex-string>
      hubRole: owner         # "owner" | "admin" | "" (user)
    - id: bob
      token: <hex-string>
      hubRole: ""            # regular user
  machines:
    "*":                     # wildcard - applies to all machines
      registerToken: <token> # only this token may register
      connectTokens:         # tokens allowed to connect
        - <token>
    barn:
      registerToken: <token>
      connectTokens:
        - <token>
        - <token>
```

### Core fields

- `port`, `udpPort`, `name`, `wwwDir`: same as the corresponding env vars.
- Precedence: **env vars override YAML**, and YAML overrides built-in defaults.
- Supported env vars: `HUB_PORT`, `HUB_UDP_PORT`, `HUB_NAME`, `HUB_WWW_DIR`.

### Auth block (`auth:`)

When `auth:` is absent or has no tokens, the hub runs in **open mode** (no authentication, same behavior as before auth was added). When tokens are present, **every** register and connect request must carry a valid Bearer token.

**`auth.tokens`**: list of token identities:

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | Human-friendly label (e.g. `alice`, `ci-bot`) |
| `token` | yes | Hex secret (64-char recommended). Generated by `tela admin add-token` or `openssl rand -hex 32` |
| `hubRole` | no | `"owner"` \| `"admin"` \| `"viewer"` \| `""` (regular user) |

**`auth.machines`**: per-machine access control:

| Field | Required | Description |
|-------|----------|-------------|
| `registerToken` | no | If set, only this token may register (or re-register) this machine |
| `connectTokens` | no | List of tokens allowed to connect to this machine |

Use `"*"` as the machine key for a wildcard rule that applies to all machines.

### Auth evaluation order

1. If `auth.tokens` is empty → open mode, allow everything.
2. Incoming request must carry a valid token (`Authorization: Bearer <token>` header or `?token=` query parameter).
3. Owner/admin tokens bypass per-machine checks.
4. For `register`: check `machines[machineId].registerToken` then `machines["*"].registerToken`.
5. For `connect`: check `machines[machineId].connectTokens` then `machines["*"].connectTokens`.

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

When auth is enabled, the hub exposes admin endpoints for remote token and ACL management. All admin endpoints require an owner or admin token.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/admin/tokens` | List all token identities (token values are previewed, not exposed) |
| `POST` | `/api/admin/tokens` | Add a new token identity (returns the full token once) |
| `DELETE` | `/api/admin/tokens?id=<id>` | Remove a token identity and clean up its ACL references |
| `POST` | `/api/admin/grant` | Grant a token connect access to a machine |
| `POST` | `/api/admin/revoke` | Revoke a token's connect access to a machine |
| `POST` | `/api/admin/rotate/<id>` | Regenerate a token (old token stops working immediately) |

Changes made through the admin API take effect immediately (hot-reload) with no hub restart needed. See the `tela admin` CLI section below for the corresponding client commands.

**Using a config file:**

- `telahubd -config telahubd.yaml`

### Service-mode config location

When running as an OS service, `telad` and `telahubd` read their YAML from a system-wide directory:

- Windows: `%ProgramData%\Tela\telad.yaml` and `%ProgramData%\Tela\telahubd.yaml`
- Linux/macOS: `/etc/tela/telad.yaml` and `/etc/tela/telahubd.yaml`

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

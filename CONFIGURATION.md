# Tela configuration reference

This document describes the configuration files used by the Tela stack:

- Local CLI config files used by `tela` (portal login + hub aliases)
- Daemon config used by `telad`
- Hub config used by `telahubd`
- Portal hub directory config used by Awan Satu

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

**When it’s used:** Only when the `-hub` value is **not** a `ws://` or `wss://` URL and the portal lookup fails/unavailable.

Resolution order in the CLI:

1. If `-hub` starts with `ws://` or `wss://` → use as-is.
2. Else try portal lookup (requires `config.yaml` from `tela login`).
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
  owlsnest: wss://tela.awansatu.net
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

## `config.yaml` (portal login)

**Purpose:** Stores portal credentials so `tela` can resolve hub names via the portal’s `/api/hubs` endpoint.

**File location:**

- Windows: `%APPDATA%\tela\config.yaml`
- Linux/macOS: `~/.tela/config.yaml`

**How it’s created:** `tela login <portal-url>` prompts for a token and writes this file.

**Schema:**

```yaml
portal:
  url: https://awansatu.net
  token: ""   # empty token = open-mode portal
```

Notes:

- `url` should be `http(s)://...`.
- `token` is optional; if present it’s sent as `Authorization: Bearer <token>`.

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
hub: wss://tela.awansatu.net
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
```

Notes:

- Precedence: **env vars override YAML**, and YAML overrides built-in defaults.
- Supported env vars: `HUB_PORT`, `HUB_UDP_PORT`, `HUB_NAME`, `HUB_WWW_DIR`.

**Using a config file:**

- `telahubd -config telahubd.yaml`

### Service-mode config location

When running as an OS service, `telad` and `telahubd` read their YAML from a system-wide directory:

- Windows: `%ProgramData%\Tela\telad.yaml` and `%ProgramData%\Tela\telahubd.yaml`
- Linux/macOS: `/etc/tela/telad.yaml` and `/etc/tela/telahubd.yaml`

## Awan Satu: `portal/config.json` (hub directory)

**Repo:** Awan Satu

**File location:** `awansatu/www/portal/config.json`

**Purpose:** The portal’s hub directory, served at `GET /api/hubs`.

**Schema:**

```json
{
  "hubs": [
    { "name": "owlsnest", "url": "https://tela.awansatu.net" }
  ]
}
```

Notes:

- `url` should be an `http(s)://...` URL that a browser can reach.
- When `tela` resolves hubs via a portal, it converts `https://` → `wss://` (and `http://` → `ws://`) automatically.
- You can manage this file by:
  - Editing it directly, or
  - Using the portal UI (which calls `POST /api/hubs` and `DELETE /api/hubs`).
- Set `TELA_API_TOKEN` on the portal server to require `Authorization: Bearer <token>`.

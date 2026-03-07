> **⚠️ HISTORICAL DOCUMENT — POC ONLY**
> This document describes the original Node.js proof-of-concept (`hub.js`). The current implementation uses `telahubd` (Go). For current deployment, see [README.md](../README.md), [IMPLEMENTATION.md](../IMPLEMENTATION.md), and the [`howto/`](../howto/) directory.

# Tela — Secure Remote Access via WireGuard over WebSocket

Tela tunnels TCP services (SSH, RDP, HTTP, etc.) through a WireGuard L3
tunnel relayed over WebSocket, with an optional UDP fast path. No admin
privileges required on either end.

## Architecture

```mermaid
graph LR
    Client["Native Client<br/>(SSH, RDP, …)"]
    Tela["tela<br/>localhost listener<br/>10.77.0.2/24"]
    Hub["hub.js (legacy)<br/>relay"]
    UDP["UDP 41820<br/>(optional relay)"]
    Telad["telad<br/>WireGuard agent<br/>10.77.0.1/24"]
    Services["Local Services<br/>(SSH, RDP, …)"]

    Client -->|TCP| Tela
    Tela -->|wss| Hub
    Hub -->|ws| Telad
    Hub -.->|UDP| UDP
    Telad --> Services

    linkStyle 1 stroke:#00b894
    linkStyle 2 stroke:#00b894

    subgraph WireGuard Tunnel
        direction LR
        Tela -.-|"gVisor netstack · E2E encrypted"| Telad
    end
```

### Components

| Component | Language | Role |
|-----------|----------|------|
| **tela** | Go | Client — WireGuard endpoint, auto-binds localhost listeners for each advertised port |
| **telad** | Go | Agent/daemon — WireGuard endpoint, forwards tunnel traffic to local services |
| **hub.js** | Node.js | *(Legacy POC relay — replaced by `telahubd` in Go)* |
| **serve.js** | Node.js | Test web server (for quick smoke tests only) |

### Security

- **End-to-end encryption:** WireGuard (Curve25519 + ChaCha20-Poly1305) between tela and telad. The hub sees only ciphertext.
- **Token auth:** Named token identities with role-based ACL (owner/admin/user). Per-machine access control. Remote management via `tela admin` CLI and admin REST API. Env-var bootstrap for Docker (`TELA_OWNER_TOKEN`). When no tokens configured, hub runs in open mode (backward compatible).
- **No admin/TUN:** Both sides use gVisor netstack — pure userspace, no elevated privileges.

## Prerequisites

- **Go 1.24+** (to build tela and telad)
- **Node.js 20+** (to run hub.js)
- **Docker + Docker Compose** (for production deployment)

## Quick Start — Local Development

### 1. Build the Go binaries

```bash
go build -o tela.exe ./cmd/tela      # Windows
go build -o telad.exe ./cmd/telad    # Windows
# On Linux/macOS, omit the .exe extension
```

### 2. Start the hub

```bash
cd poc
npm install
node hub.js
```

Output: `[hub] HTTP on :8080  ·  UDP relay on :41820`

### 3. Start telad (on the machine with services)

```bash
./telad -hub ws://localhost:8080 -machine mybox -ports "22,3389"
```

telad registers with the hub, generates its WireGuard keypair, and waits for a client.

### 4. Start tela (on your laptop)

```bash
./tela connect -hub ws://localhost:8080 -machine mybox
```

tela connects, completes the WireGuard handshake, and prints the local port bindings:

```
[tela] listening 127.0.0.1:22   → mybox:22
[tela] listening 127.0.0.1:3389 → mybox:3389
```

### 5. Connect

```bash
ssh localhost          # SSH
mstsc /v:localhost     # RDP (Windows Pro/Enterprise only)
```

## Production Deployment (Docker)

The repo root contains a `docker-compose.yml` with three services:
**caddy** (TLS reverse proxy), **hub** (WebSocket + UDP relay), and
**telad** (WireGuard agent).

```bash
# Set your Cloudflare API token for DNS-01 ACME
export CLOUDFLARE_API_TOKEN=your_token_here

# Build and start
docker compose up --build -d

# Run tela on your laptop
./tela connect -hub wss://tela-local.example.com -machine barn
```

See `IMPLEMENTATION.md` §8 for the full Docker Compose skeleton and Caddyfile.

## CLI Reference

### tela (client)

Subcommand-based CLI. Run `tela` with no arguments for usage.

**Hub name resolution** — the `-hub` flag accepts a full URL (`wss://...`) or
a short hub name. Short names are resolved by: (1) querying configured remotes
added via `tela remote add`, (2) falling back to a local `hubs.yaml` file.

**Environment variables** — set these to avoid repeating flags:

| Variable | Description |
|----------|-------------|
| `TELA_HUB` | Default hub URL or name (overridden by `-hub`) |
| `TELA_MACHINE` | Default machine ID (overridden by `-machine`) |
| `TELA_TOKEN` | Default auth token (overridden by `-token`) |
| `TELA_OWNER_TOKEN` | Preferred by `tela admin` commands (falls back to `TELA_TOKEN`) |

#### tela connect

Open a WireGuard tunnel to a registered machine.

```
tela connect -hub <url> -machine <name> [-token <secret>]

# With env vars:
export TELA_HUB=wss://hub.example.com TELA_MACHINE=barn
tela connect
```

| Flag | Default | Description |
|------|---------|-------------|
| `-hub` | `$TELA_HUB` | Hub WebSocket URL (`ws://` or `wss://`) or hub name |
| `-machine` | `$TELA_MACHINE` | Machine name to connect to |
| `-token` | `$TELA_TOKEN` | Authentication token |
| `-services` | | Comma-separated service names, resolved via hub API (e.g. `ssh,rdp`) |
| `-ports` | | Comma-separated `local:remote` port mappings |
| `-profile` | | Named connection profile from `~/.tela/profiles/<name>.yaml` |

#### tela remote

Manage hub directory remotes.

```
tela remote add awansaya https://awansaya.net
tela remote list
tela remote remove awansaya
```

`tela remote add` prompts for an API token (press Enter for open-mode directories).
Stores remote URL and token in `%APPDATA%\tela\config.yaml` (Windows) or `~/.tela/config.yaml`.

Once configured, `-hub` accepts short hub names (e.g., `myhub`) that are
resolved via each remote's `/api/hubs` endpoint. Local `hubs.yaml` is used as
a fallback if all remotes are unreachable.

#### tela login (deprecated)

Alias for `tela remote add portal <url>`. Prints a deprecation notice.

```
tela login https://your-portal.example
```

#### tela logout (deprecated)

Alias for `tela remote remove portal`. Prints a deprecation notice.

```
tela logout
```

#### tela machines

List machines registered on the hub.

```
tela machines -hub <url> [-token <secret>] [-json]
tela machines              # uses $TELA_HUB
```

#### tela services

List services advertised by machines.

```
tela services -hub <url> -machine <name> [-token <secret>] [-json]
tela services              # uses $TELA_HUB + $TELA_MACHINE
```

#### tela status

Show a summary of hub status (machine counts, session counts).

```
tela status -hub <url> [-token <secret>] [-json]
tela status                # uses $TELA_HUB
```

#### tela admin

Remote hub auth management. All admin sub-commands require an owner or admin token.

```
tela admin <sub-command> [options]
```

| Sub-command | Description |
|------------|-------------|
| `list-tokens` | List all token identities on the hub |
| `add-token <id> [-role admin]` | Add a new token identity (prints the token once) |
| `remove-token <id>` | Remove a token identity |
| `grant <id> <machineId>` | Grant connect access to a machine |
| `revoke <id> <machineId>` | Revoke connect access to a machine |
| `rotate <id>` | Regenerate token for an identity |

All sub-commands accept `-hub` and `-token` flags.
Token resolution order: `-token` flag > `TELA_OWNER_TOKEN` env var > `TELA_TOKEN` env var.

Examples:

```bash
tela admin list-tokens -hub wss://myhub -token <owner-token>
tela admin add-token alice -hub wss://myhub -token <owner-token>
tela admin add-token bob -hub wss://myhub -token <owner-token> -role admin
tela admin grant alice barn -hub wss://myhub -token <owner-token>
tela admin revoke alice barn -hub wss://myhub -token <owner-token>
tela admin rotate alice -hub wss://myhub -token <owner-token>
tela admin remove-token alice -hub wss://myhub -token <owner-token>
```

### telad (agent)

**Config-file mode** (recommended for production and multi-machine):

```yaml
# telad.yaml
hub: ws://hub:8080
token: secret              # optional, shared default

machines:
  - name: barn
    ports: [22, 3389]
    target: host.docker.internal

  - name: nas
    ports: [22, 445]
    target: 192.168.1.50
```

```
telad -config telad.yaml
```

**Single-machine mode** (flags):

```
telad -hub <url> -machine <name> -ports <list> [-target-host <host>] [-token <secret>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `$TELA_CONFIG` | Path to YAML config file |
| `-hub` | `$TELA_HUB` | Hub WebSocket URL |
| `-machine` | `$TELA_MACHINE` | Machine name to register as |
| `-ports` | `$TELA_PORTS` or `3389` | Comma-separated ports to advertise |
| `-target-host` | `$TELA_TARGET_HOST` or `127.0.0.1` | Host where services are running |
| `-token` | `$TELA_TOKEN` | Authentication token |

### hub.js

```
HUB_PORT=8080 HUB_UDP_PORT=41820 HUB_TOKEN=secret node hub.js
```

| Env var | Default | Description |
|---------|---------|-------------|
| `HUB_PORT` | `8080` | HTTP/WebSocket listen port |
| `HUB_UDP_PORT` | `41820` | UDP relay port for WireGuard datagrams |
| `HUB_TOKEN` | (none) | Shared authentication token |

### serve.js (test only)

```
node serve.js [port]
```

Default port: `3000`. Serves a static test page.

## Glossary

| Term | Definition |
|------|------------|
| **Hub** | Central relay server (`hub.js`). Routes control + data between agents and clients. |
| **Hub Console** | Web dashboard served at the hub root URL. Shows live machine/service status. |
| **Agent (telad)** | Daemon running on a machine that registers with the hub and exposes services. |
| **Client (tela)** | CLI tool that connects to a machine through the hub and opens a WireGuard tunnel. |
| **Machine** | A named endpoint registered by an agent (e.g. `barn`). |
| **Service** | A network port advertised by an agent (e.g. SSH/22, RDP/3389). |
| **Session** | An active tunnel between a client and a machine. |

## What This Proves

- WireGuard L3 tunneling over WebSocket works for real protocols (SSH, RDP)
- gVisor netstack eliminates the need for TUN interfaces or admin privileges
- The outbound-only hub relay model works across NATs and firewalls
- UDP relay provides a fast path when available, with automatic WS fallback
- Asymmetric UDP mode (one side UDP, other side WS) works via hub bridging
- Token-based RBAC with per-machine ACLs secures hub access without external dependencies
- Remote auth management via REST API + CLI works for Docker deployments (no shell access needed)
- Auto-reconnect keeps sessions resilient
- Hub Console provides live visibility into registered machines and services

## What's Next

- Binary multiplexed framing (DESIGN.md §6.3)
- Multiple simultaneous sessions per machine

See `TODO.md` for the full roadmap.

## Troubleshooting

**"Machine not found"** — Start telad before tela. The machine’s daemon must register first.

**"auth failed"** — Token mismatch. Ensure `-token` matches a valid token in the hub's auth config. For the POC `hub.js`, the token must match `HUB_TOKEN`. For `telahubd`, the token must be a valid identity in the hub's YAML config.

**Connection hangs after pairing** — Check that the WireGuard handshake completes. Look for `[wg] handshake complete` in the logs. If missing, there may be a WebSocket framing issue.

**RDP black screen / NLA error** — Ensure RDP is enabled (Windows Pro/Enterprise only). Tela uses a WireGuard tunnel so NLA/CredSSP works correctly (unlike L4 TCP proxies).

**UDP relay not upgrading** — The hub's UDP port (41820) must be reachable from the client. If behind NAT without port forwarding, the system falls back to WebSocket automatically.

**Port already in use** — Another process is using that port. tela will report the conflict at startup.

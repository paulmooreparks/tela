# How to deploy a Tela Hub

A **Tela Hub** is the rendezvous + relay point that:

- accepts outbound connections from `tela` (clients) and `telad` (daemons)
- pairs them into sessions
- relays **end-to-end encrypted** tunnel traffic (the Hub never sees plaintext)
- exposes lightweight status endpoints used by portals (`/api/status`, `/api/history`)
- serves a built-in web console for quick status checks

## Hub server: `telahubd`

`telahubd` is the Go-native hub server. Single binary, no runtime dependencies. It serves HTTP, WebSocket relay, and UDP relay on one process.

### Download

Pre-built binaries for Windows, Linux, and macOS are available on the [GitHub Releases](https://github.com/paulmooreparks/tela/releases) page.

### Build from source

```bash
go build -o telahubd ./cmd/telahubd
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HUB_PORT` | `8080` | HTTP + WebSocket listen port |
| `HUB_UDP_PORT` | `41820` | UDP relay port |
| `HUB_UDP_HOST` | *(empty)* | Public IP/hostname advertised in UDP offers (for proxy/tunnel setups) |
| `HUB_NAME` | *(empty)* | Display name shown in portal and `/api/status` |
| `HUB_WWW_DIR` | `./www` | Directory for static console files |

### Run locally

```bash
# Minimal - listens on :8080 (HTTP+WS) and :41820 (UDP)
telahubd

# With a display name
HUB_NAME=myhub telahubd

# Custom ports
HUB_PORT=9090 HUB_UDP_PORT=9091 telahubd

# Behind Cloudflare/proxy — advertise real IP for UDP relay
HUB_UDP_HOST=myhost.example.com telahubd
```

### Run with Docker

The Tela repo includes a ready-made Dockerfile at `docker/gohub/Dockerfile`:

```bash
docker build -f docker/gohub/Dockerfile -t telahubd .
docker run -d --name telahubd \
  -p 8080:8080 \
  -p 41820:41820/udp \
  -e HUB_NAME=myhub \
  -e HUB_UDP_HOST=myhost.example.com \
  telahubd
```

Or use Docker Compose (see the `gohub` service in `docker-compose.yml`):

```bash
docker compose up -d --build gohub
```

The Docker Compose setup uses a named volume (`hub-data`) to persist the hub config at `/app/data/telahubd.yaml`, so auth configuration survives container recreation.

### Verify

```bash
# Check hub status
curl http://localhost:8080/api/status

# Check session history
curl http://localhost:8080/api/history

# Check version
telahubd version
```

## Enable authentication (recommended)

By default, the hub runs in **open mode**. Any agent or client can connect without credentials. To lock it down, enable token-based authentication.

### Docker deployment (env-var bootstrap)

The simplest path for Docker:

```bash
# 1. Generate an owner token on your local machine
openssl rand -hex 32

# 2. Add to docker-compose.yml environment block:
#    - TELA_OWNER_TOKEN=<your-token>

# 3. Redeploy
docker compose up --build -d
```

On first startup the hub creates an `owner` identity and a `console-viewer` identity (viewer role for the built-in web console), persists them, and is ready for remote management.

### Bare-metal / direct deployment

If running `telahubd` directly (not in Docker), you can either:

1. Set `TELA_OWNER_TOKEN` as an environment variable before starting, or
2. Create a `telahubd.yaml` config file with an `auth:` block (see [CONFIGURATION.md](../CONFIGURATION.md))

### Managing tokens remotely with `tela admin`

Once the owner token exists, manage everything from any workstation:

```bash
# List identities on the hub
tela admin list-tokens -hub wss://your-hub.example.com -token <owner-token>

# Add a user identity
tela admin add-token alice -hub wss://your-hub.example.com -token <owner-token>
# → Save the printed token!

# Add an admin
tela admin add-token bob -hub wss://your-hub.example.com -token <owner-token> -role admin

# Grant connect access to a machine
tela admin grant alice barn -hub wss://your-hub.example.com -token <owner-token>

# Revoke access
tela admin revoke alice barn -hub wss://your-hub.example.com -token <owner-token>

# Rotate a compromised token
tela admin rotate alice -hub wss://your-hub.example.com -token <owner-token>

# Remove an identity entirely
tela admin remove-token alice -hub wss://your-hub.example.com -token <owner-token>
```

All changes take effect immediately (hot-reload). No hub restart required.

### Using `telad` with auth

When the hub has auth enabled, agents must provide a valid token:

```yaml
# telad.yaml
hub: wss://your-hub.example.com
token: "<token-for-this-agent>"

machines:
  - name: barn
    ports: [22, 3389]
```

```bash
telad -config telad.yaml
```

Or with a flag: `telad -hub wss://... -machine barn -ports "22,3389" -token <token>`

### Using `tela` (client) with auth

```bash
tela connect -hub wss://your-hub.example.com -machine barn -token <your-token>

# Or set env vars:
export TELA_HUB=wss://your-hub.example.com
export TELA_TOKEN=<your-token>
tela connect -machine barn
```

## What must be reachable

Minimum (required):

- **Inbound TCP** for **HTTPS + WebSockets** from both `tela` (clients) and `telad` (daemons).
  - Publish on **TCP 443** when exposing to the Internet.
  - Your reverse proxy/tunnel must support WebSocket upgrades end-to-end.

Optional:

- **Inbound UDP** on the hub's UDP relay port (default `41820`) if you want UDP relay.
  - If you only expose the hub via a TCP-only tunnel, UDP relay will not work; the system will fall back to WebSockets.

## Publish with TLS (recommended)

Publish the hub behind something that terminates TLS and supports WebSockets:

- Caddy, nginx, HAProxy, Cloudflare Tunnel, etc.
- Ensure WebSocket upgrade headers are preserved.

Typical shape:

- Public: `https://your-hub.example.com` (TCP 443)
- Internal: hub container / process on TCP 8080

### Example: Cloudflare Tunnel

Add an ingress rule to your cloudflared config:

```yaml
- hostname: your-hub.example.com
  service: http://localhost:8080
```

Then create a CNAME DNS record pointing `your-hub.example.com` to your tunnel.

### Example: Caddy reverse proxy

```caddyfile
your-hub.example.com {
    reverse_proxy localhost:8080
}
```

### Example: Docker Compose with Caddy + Cloudflare DNS

See the `docker-compose.yml` and `docker/caddy/Caddyfile` in the Tela repo for a complete example using Caddy with DNS-01 TLS and Cloudflare Tunnel.

## Register with a hub directory

Once the hub is reachable, add it to a hub directory (such as Awan Saya) so users and the CLI can find it by short name:

1. Open the portal dashboard and click **Add Hub**.
2. Enter a short name (e.g., `myhub`), the hub's public URL (e.g., `https://your-hub.example.com`), and optionally a **viewer token** (so the portal can proxy hub status server-side).
3. The hub will appear in the portal dashboard and be resolvable by the CLI:
   ```bash
   tela remote add myportal https://your-portal.example
   tela machines -hub myhub
   tela connect -hub myhub -machine mybox
   ```

## Verify from outside

From a machine on the Internet (or at least outside your LAN), verify:

- `GET https://<hub>/api/status` returns JSON with hub info.
- `GET https://<hub>/api/history` returns event history.
- Portal shows the hub card with status (validates CORS + reachability).

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `telad` never appears | Hub unreachable or WebSocket upgrade blocked | Confirm the hub URL is reachable externally (TCP 443 + WS) |
| Portal cards stay empty | Portal missing viewer token, or hub unreachable from portal server | Ensure the hub entry in the portal includes a valid viewer token |
| UDP relay not working | TCP-only tunnel or firewall | Confirm UDP `HUB_UDP_PORT` is open inbound on the hub and outbound from both sides |
| "Machine not found" | Machine isn't registered | Run `tela machines -hub <hub>` to list available machines; confirm `telad` is running and connected |



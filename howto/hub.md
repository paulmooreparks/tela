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

Pre-built binaries for Windows, Linux, and macOS are available on the [GitHub Releases](https://github.com/paulmooreparks/tela/releases) page, or from the [Awan Satu download section](https://awansatu.net/#download).

### Build from source

```bash
go build -o telahubd ./cmd/telahubd
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HUB_PORT` | `8080` | HTTP + WebSocket listen port |
| `HUB_UDP_PORT` | `41820` | UDP relay port |
| `HUB_NAME` | *(empty)* | Display name shown in portal and `/api/status` |
| `HUB_WWW_DIR` | `./www` | Directory for static console files |

### Run locally

```bash
# Minimal — listens on :8080 (HTTP+WS) and :41820 (UDP)
telahubd

# With a display name
HUB_NAME=myhub telahubd

# Custom ports
HUB_PORT=9090 HUB_UDP_PORT=9091 telahubd
```

### Run with Docker

The Tela repo includes a ready-made Dockerfile at `docker/gohub/Dockerfile`:

```bash
docker build -f docker/gohub/Dockerfile -t telahubd .
docker run -d --name telahubd \
  -p 8080:8080 \
  -p 41820:41820/udp \
  -e HUB_NAME=myhub \
  telahubd
```

Or use Docker Compose (see the `gohub` service in `docker-compose.yml`):

```bash
docker compose up -d --build gohub
```

### Verify

```bash
# Check hub status
curl http://localhost:8080/api/status

# Check session history
curl http://localhost:8080/api/history

# Check version
telahubd version
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

## Register with a portal

Once the hub is reachable, add it to an Awan Satu portal so users and the CLI can find it by short name:

1. Open the portal and click **Add Hub**.
2. Enter a short name (e.g., `myhub`) and the hub's public URL (e.g., `https://your-hub.example.com`).
3. The hub will appear in the portal dashboard and be resolvable by the CLI:
   ```bash
   tela login https://awansatu.net
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
| Portal cards stay empty | CORS or connectivity | Confirm `https://<hub>/api/status` is reachable from the browser and returns CORS headers |
| UDP relay not working | TCP-only tunnel or firewall | Confirm UDP `HUB_UDP_PORT` is open inbound on the hub and outbound from both sides |
| "Machine not found" | Machine isn't registered | Run `tela machines -hub <hub>` to list available machines; confirm `telad` is running and connected |



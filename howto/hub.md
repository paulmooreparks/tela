# How to deploy a Tela Hub (and what must be reachable)

A **Tela Hub** is the rendezvous + relay point that:

- accepts outbound connections from `tela` (clients) and `telad` (agents)
- pairs them into sessions
- relays **end-to-end encrypted** tunnel traffic (the Hub never sees plaintext)
- exposes lightweight status endpoints used by portals (`/api/status`, `/api/history`)

Implementation note: the current hub server implementation in this repo is `poc/hub.js`. The product-level networking requirements below are the same regardless of the hub’s implementation language.

## What must be reachable

Minimum (required):

- **Inbound TCP** for **HTTPS + WebSockets** from both `tela` (clients) and `telad` (agents).
  - Publish on **TCP 443** when exposing to the Internet.
  - Your reverse proxy/tunnel must support WebSocket upgrades end-to-end.

Optional:

- **Inbound UDP** on the hub’s UDP relay port (default `41820`) if you want UDP relay.
  - If you only expose the hub via a TCP-only tunnel, UDP relay will not work; the system will fall back to WebSockets.

## Local run (developer / LAN)

Run the hub directly:

- The reference hub listens on `HUB_PORT` (default `8080`) for HTTP+WebSockets.
- UDP relay binds `HUB_UDP_PORT` (default `41820`).

If you’re using `docker compose`, publish the hub port(s) that match your setup.

## Publish with TLS (recommended)

Publish the hub behind something that terminates TLS and supports WebSockets:

- Caddy, nginx, HAProxy, Cloudflare Tunnel, etc.
- Ensure WebSocket upgrade headers are preserved.

Typical shape:

- Public: `https://your-hub.example.com` (TCP 443)
- Internal: hub container / process on TCP 8080

## Verify

From a machine on the Internet (or at least outside your LAN), verify:

- `GET https://<hub>/api/status`
- `GET https://<hub>/api/history`

If you’re using Awan Satu:

- open the portal and confirm the hub card loads (this validates browser-to-hub reachability + CORS).

## Troubleshooting notes

- If `telad` never appears: confirm it can reach the hub URL and that the hub is reachable externally (TCP 443 + WebSockets).
- If portal cards stay empty: confirm `https://<hub>/api/status` is reachable from the browser network and returns CORS headers.
- If you expected UDP but see WS-only behavior: confirm UDP `HUB_UDP_PORT` is open inbound on the hub and outbound from both sides.

# Networking & ports

This doc makes the **reachability assumptions** explicit for Tela.

## Quick matrix

| Component | Needs inbound from Internet | Needs outbound | Default ports / protocols |
|----------|------------------------------|---------------|---------------------------|
| Hub (`telahubd`) | Yes | No (special) | Public: TCP 443 for HTTPS+WebSockets; Optional: UDP 41820 for UDP relay. The hub listens on `HUB_PORT` (default `8080`) and `HUB_UDP_PORT` (default `41820`). |
| Daemon (`telad`) | No | Yes | Outbound WebSocket to hub (`ws://` / `wss://`); optional outbound UDP to hub `HUB_UDP_PORT` |
| Client (`tela`) | No | Yes | Outbound WebSocket to hub (`ws://` / `wss://`); optional outbound UDP to hub `HUB_UDP_PORT` |
| Portal (browser UI) | n/a | Yes | Browser fetches `https://<hub>/api/status` and `https://<hub>/api/history` (cross-origin) |

## Hub requirements

The Hub is the only component that typically needs **inbound** connectivity.

Minimum:

- Inbound TCP for **HTTPS + WebSockets**.
  - The hub serves HTTPS + WebSockets on a single public origin (typically TCP 443).
  - Implementation note: the hub serves HTTP+WS on a single port (`HUB_PORT`, default `8080`) and is commonly published on 443 via a reverse proxy / tunnel.
  - In most deployments you publish this via a reverse proxy / tunnel on **TCP 443**.
- The Hub must allow WebSocket upgrade end-to-end (reverse proxy must forward `Upgrade` / `Connection` headers).

Optional (performance / transport):

- Inbound UDP `HUB_UDP_PORT` (default `41820`) to enable the hub’s UDP relay.
  - If this is not reachable (e.g., you only expose the hub via a TCP-only tunnel), sessions still work via WebSockets; they may just be slower.

Portal visibility:

- For Awan Satu (or any browser-based portal) to display hub cards/metrics, the hub must expose:
  - `GET /api/status` (and/or `/status`)
  - `GET /api/history`
- Cross-origin portal fetches require CORS. The hub replies with `Access-Control-Allow-Origin: *` for these endpoints.

## Daemon (`telad`) requirements

`telad` is designed to work in “outbound-only” environments, but it still has two key reachability needs:

1) **Outbound to the Hub**

- Must be able to establish a long-lived WebSocket connection to the hub URL in `telad.yaml` (example: `hub: ws://hub:8080`).

2) **Reachability to the services it exposes**

- Endpoint pattern (daemon runs on the target host): services are usually on `localhost`.
- Gateway/bridge pattern (daemon runs somewhere else): the daemon host must be able to reach the target's service ports.
  - Example: `target: host.docker.internal` bridges from a containerized daemon to services running on the Docker host.

Optional:

- If UDP relay is enabled on the hub, `telad` may also send UDP to the hub’s `HUB_UDP_PORT`.

## Client (`tela`) requirements

- Outbound WebSocket to the hub.
- Optional outbound UDP to hub `HUB_UDP_PORT` when UDP relay is enabled.

Local binding:

- The client typically binds a loopback listener like `127.0.0.1:<port>` so local apps (SSH/RDP/etc.) can connect.
  - This is “inbound” only from the local machine, not from the Internet.

## Checklist (copy/paste)

When something “can’t connect”, check these in order:

- Hub is reachable on TCP 443 (or wherever you publish `HUB_PORT`).
- Reverse proxy supports WebSockets.
- Daemon can reach the hub URL from where it runs.
- Daemon can reach its `target` host and the service ports behind it.
- If you expect UDP relay: hub UDP port reachable + outbound UDP allowed from client/daemon.

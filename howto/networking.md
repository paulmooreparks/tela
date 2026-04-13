# Networking & ports

This doc makes the **reachability assumptions** explicit for Tela.

## Quick matrix

| Component | Needs inbound from Internet | Needs outbound | Default ports / protocols |
|----------|------------------------------|---------------|---------------------------|
| Hub (`telahubd`) | Yes | No (special) | Public: TCP 443 for HTTPS+WebSockets; Optional: UDP 41820 for UDP relay. The hub listens on `HUB_PORT` (default `80`) and `HUB_UDP_PORT` (default `41820`). |
| Daemon (`telad`) | No | Yes | Outbound WebSocket to hub (`ws://` / `wss://`); optional outbound UDP to hub `HUB_UDP_PORT` |
| Client (`tela`) | No | Yes | Outbound WebSocket to hub (`ws://` / `wss://`); optional outbound UDP to hub `HUB_UDP_PORT` |
| Portal (browser UI) | n/a | Yes | Browser fetches `https://<hub>/api/status` and `https://<hub>/api/history` (cross-origin) |

## Hub requirements

The Hub is the only component that typically needs **inbound** connectivity.

Minimum:

- Inbound TCP for **HTTPS + WebSockets**.
  - The hub serves HTTPS + WebSockets on a single public origin (typically TCP 443).
  - Implementation note: the hub serves HTTP+WS on a single port (`HUB_PORT`, default `80`) and is commonly published on 443 via a reverse proxy / tunnel.
  - In most deployments you publish this via a reverse proxy / tunnel on **TCP 443**.
- The Hub must allow WebSocket upgrade end-to-end (reverse proxy must forward `Upgrade` / `Connection` headers).

Optional (performance / transport):

- Inbound UDP `HUB_UDP_PORT` (default `41820`) to enable the hub's UDP relay.
  - If this is not reachable (e.g., you only expose the hub via a TCP-only tunnel), sessions still work via WebSockets; they may just be slower.
  - If the hub's domain resolves to a proxy (e.g. Cloudflare), set `HUB_UDP_HOST` to the real public IP or a DNS name that resolves directly, and forward UDP on your router. Without this, clients send UDP to the proxy and it's silently dropped.

Portal visibility:

- For Awan Saya (or any browser-based portal) to display hub cards/metrics, the hub must expose:
  - `GET /api/status` (and/or `/status`)
  - `GET /api/history`
- Cross-origin portal fetches require CORS. The hub replies with `Access-Control-Allow-Origin: *` for these endpoints.

## Daemon (`telad`) requirements

`telad` is designed to work in “outbound-only” environments, but it still has two key reachability needs:

1) **Outbound to the Hub**

- Must be able to establish a long-lived WebSocket connection to the hub URL in `telad.yaml` (example: `hub: ws://hub` or `hub: wss://hub.example.com`).

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

## Topology and addressing

These questions come up often from people evaluating Tela against mesh VPNs or traditional VPNs. The short answers are here; the architecture chapter has the longer rationale.

### Does Tela create an L3 network?

Not in the sense that a mesh VPN does. Tela creates per-session point-to-point WireGuard tunnels. Each session gets its own /24 from the `10.77.0.0/16` range: `10.77.{idx}.1` on the agent side, `10.77.{idx}.2` on the client side. The session index is assigned by the hub, increments monotonically per machine, and maxes out at 254 (one machine can serve up to 254 simultaneous client sessions).

Critically, these addresses exist only inside gVisor's userspace network stack. They never appear as host interfaces, routing table entries, or ARP entries on either machine. There is no risk of collision with your LAN's `10.77.x.x` subnet or anyone else's private addressing, because Tela's addresses are not visible to the host network at all.

### Does it clash with my existing IP addressing?

No. Because Tela runs WireGuard in userspace through gVisor, the `10.77.x.x` session addresses are internal to the process. The host operating system sees no new interfaces, no new routes, and no new neighbors. A machine with a LAN IP of `10.77.5.100` has no conflict with a Tela session using `10.77.5.0/24`.

### How do I find and reach services? Is there DNS?

You do not use tunnel-internal IP addresses or DNS to reach services through Tela. The workflow is:

1. You tell `tela` (or TelaVisor) which machine on which hub you want to connect to, and which services on that machine you want.
2. Each machine gets a deterministic loopback address in the `127.88.0.0/16` range, computed from the hub URL and machine name. Services bind on their real remote ports at that address (for example, `127.88.42.17:22` for SSH, `127.88.42.17:5432` for PostgreSQL).
3. You point your SSH client, browser, or database tool at that address and port.

The address is stable: the same machine always gets the same loopback IP across sessions and profiles. `tela status` lists the current bindings. TelaVisor shows them in the Status tab. If a service port is blocked by a system listener (for example, RDP on `0.0.0.0:3389`), the port is offset by 10000 while the address stays the same.

### Can I ping through the tunnel?

No. Tela tunnels TCP only. Internet Control Message Protocol (ICMP), which carries `ping` and `traceroute`, does not travel through the tunnel. This also means no UDP services. If your application uses UDP (SIP, QUIC, game protocols), it will not work through a Tela tunnel today.

### Can agents talk to each other?

Not directly. Tela does not route between agents. To get data from machine A to machine B, you need a client on the path: `tela` connects to A, gets the data, and separately connects to B to send it. There is no agent-to-agent tunnel without a client in the middle. The hub-to-hub relay gateway planned for 1.0 addresses hub federation, not agent-to-agent routing.

### Does Tela support IPv6?

The WireGuard session addressing is IPv4 (`10.77.x.x`). The control channel between agents, clients, and the hub (WebSocket or UDP relay) works over whatever IP version the hub is reachable on. End-to-end IPv6 service tunneling is not currently supported; the gVisor netstack inside the agent and client uses IPv4 for the tunnel. IPv6 is on the long-term list but is not a 1.0 requirement.

### How many clients can connect to one agent simultaneously?

Up to 254. The session index is an 8-bit counter; session index 0 is reserved, leaving 1-254 for active sessions. Attempting a 255th session is rejected by the hub. In practice, the bottleneck is usually the agent machine's bandwidth or the services behind it, not the session limit.

---

## Checklist (copy/paste)

When something “can’t connect”, check these in order:

- Hub is reachable on TCP 443 (or wherever you publish `HUB_PORT`).
- Reverse proxy supports WebSockets.
- Daemon can reach the hub URL from where it runs.
- Daemon can reach its `target` host and the service ports behind it.
- If you expect UDP relay: hub UDP port reachable + outbound UDP allowed from client/daemon.

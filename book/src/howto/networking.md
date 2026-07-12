# Networking Caveats

Tela is designed to work through firewalls and Network Address Translation
(NAT) without special configuration on the agent or client side. The hub is
the only component that needs an inbound port. Both agents and clients
connect to the hub outbound, over a standard HTTPS or WebSocket connection.
In most environments that is all you need to know.

Read this chapter when the defaults do not hold: `telad` is running and the
hub is reachable but connections are not working, or you are deploying Tela
into a network with strict firewall rules, proxies, or unusual topology.
The second half answers the topology and addressing questions that come up
when evaluating Tela against mesh VPNs.

## Quick Matrix

| Component | Needs Inbound from Internet | Needs Outbound | Default Ports and Protocols |
|----------|------------------------------|---------------|---------------------------|
| Hub (`telahubd`) | Yes | No | TCP for HTTP and WebSocket (`TELAHUBD_PORT`, default `80`, typically published on 443 via a reverse proxy); optional UDP relay (`TELAHUBD_UDP_PORT`, default `41820`) |
| Agent (`telad`) | No | Yes | Outbound WebSocket to the hub (`ws://` or `wss://`); optional outbound UDP to the hub's UDP port |
| Client (`tela`) | No | Yes | Outbound WebSocket to the hub; optional outbound UDP to the hub's UDP port |
| Portal (browser UI) | n/a | Yes | Browser fetches `https://<hub>/api/status` and `https://<hub>/api/history` cross-origin |

## Hub Requirements

The hub is the only component that needs inbound connectivity.

Minimum: inbound TCP for HTTPS and WebSockets. The hub serves HTTP and
WebSocket on a single port (`TELAHUBD_PORT`, default `80`) and is commonly
published on 443 by a reverse proxy. The reverse proxy must forward
`Upgrade` and `Connection` headers, or WebSocket upgrades fail and nothing
can connect.

Optional, for performance: inbound UDP on `TELAHUBD_UDP_PORT` (default
`41820`) to enable the hub's UDP relay. If this port is not reachable (for
example, the hub is only exposed through a TCP-only tunnel), sessions still
work over WebSockets; they are just slower. If the hub's domain resolves to
a proxy (for example, Cloudflare), set `TELAHUBD_UDP_HOST` to an address
that resolves directly to the hub and forward UDP on your router. Without
this, clients send UDP to the proxy and it is silently dropped.

For a browser-based portal to display hub cards and metrics, the hub must
expose `GET /api/status` and `GET /api/history`. Cross-origin portal
fetches require Cross-Origin Resource Sharing (CORS); the hub replies with
`Access-Control-Allow-Origin: *` for these endpoints.

## Agent Requirements

`telad` works in outbound-only environments, but it has two reachability
needs:

1. **Outbound to the hub.** It must be able to establish a long-lived
   WebSocket connection to the hub URL in `telad.yaml`.
2. **Reachability to the services it exposes.** In the endpoint pattern
   (agent on the target host), services are usually on `localhost`. In the
   gateway pattern (agent on a different host), the agent host must be able
   to reach the target's service ports; `target: host.docker.internal`, for
   example, bridges from a containerized agent to services on the Docker
   host.

If the UDP relay is enabled on the hub, `telad` may also send UDP to the
hub's UDP port.

## Client Requirements

The client needs an outbound WebSocket to the hub, plus optional outbound
UDP when the UDP relay is enabled.

Locally, the client binds a loopback listener on `127.0.0.1` at each
service's configured local port so local applications (SSH, Remote Desktop
Protocol (RDP) clients, database tools) can connect. If a port is taken,
the client tries the port plus 10000, then plus 10001, and so on until a
free port is found. The bound port is shown in the `tela connect` output
and in TelaVisor's Status tab. This listener is local-only, not inbound
from the internet.

## Topology and Addressing

These questions come up often from people evaluating Tela against mesh
Virtual Private Networks (VPNs) or traditional VPNs. The short answers are
here; the [Design Rationale](../architecture/design.md) section has the
longer rationale.

### Does Tela Create an L3 Network?

Not in the sense that a mesh VPN does. Tela creates per-session
point-to-point WireGuard tunnels. Each session gets its own /24 from the
`10.77.0.0/16` range: `10.77.{idx}.1` on the agent side, `10.77.{idx}.2` on
the client side. The session index is assigned by the hub, increments
monotonically per machine, and maxes out at 254 (one machine can serve up
to 254 simultaneous client sessions).

Critically, these addresses exist only inside gVisor's userspace network
stack. They never appear as host interfaces, routing table entries, or
Address Resolution Protocol (ARP) entries on either machine.

### Does It Clash with My Existing IP Addressing?

No. Because Tela runs WireGuard in userspace through gVisor, the
`10.77.x.x` session addresses are internal to the process. The host
operating system sees no new interfaces, no new routes, and no new
neighbors. A machine with a LAN IP of `10.77.5.100` has no conflict with a
Tela session using `10.77.5.0/24`.

### How Do I Find and Reach Services? Is There DNS?

You do not use tunnel-internal IP addresses or DNS to reach services
through Tela. The workflow is:

1. You tell `tela` (or TelaVisor) which machine on which hub you want to
   connect to, and which services on that machine you want.
2. `tela` binds each service at `localhost:PORT` on your machine: the
   configured local port, the service's native port, or a fallback port
   starting at the port plus 10000 when the first choice is in use.
3. You point your SSH client, browser, or database tool at
   `localhost:PORT`.

`tela connect` and `tela status` print the bound address and port for each
service. TelaVisor shows them in the Status tab. To pin a service to a
specific local port across reconnects, set `local:` on that service in your
profile. To get name-based access (`ssh barn.tela`), see the
[DNS Names](../guide/profiles.md#dns-names) section of the Connection
Profiles chapter.

### Can I Ping Through the Tunnel?

No. Tela tunnels TCP only. Internet Control Message Protocol (ICMP), which
carries `ping` and `traceroute`, does not travel through the tunnel. This
also means no UDP services: if your application uses UDP (SIP, QUIC, game
protocols), it will not work through a Tela tunnel today.

### Can Agents Talk to Each Other?

Not directly. Tela does not route between agents. To get data from machine
A to machine B, you need a client on the path: `tela` connects to A, gets
the data, and separately connects to B to send it. There is no
agent-to-agent tunnel without a client in the middle. The hub-to-hub relay
gateway addresses hub bridging, not agent-to-agent routing.

### Does Tela Support IPv6?

The WireGuard session addressing is IPv4 (`10.77.x.x`). The control channel
between agents, clients, and the hub (WebSocket or UDP relay) works over
whatever IP version the hub is reachable on. End-to-end IPv6 service
tunneling is not currently supported; the gVisor netstack inside the agent
and client uses IPv4 for the tunnel. IPv6 is on the long-term list but is
not a 1.0 requirement.

### How Many Clients Can Connect to One Agent Simultaneously?

Up to 254. The session index is an 8-bit counter; session index 0 is
reserved, leaving 1 through 254 for active sessions. A 255th session is
rejected by the hub. In practice, the bottleneck is usually the agent
machine's bandwidth or the services behind it, not the session limit.

## Checklist

When something cannot connect, check these in order:

1. The hub is reachable on TCP 443 (or wherever you publish
   `TELAHUBD_PORT`).
2. The reverse proxy forwards WebSocket upgrades.
3. The agent can reach the hub URL from where it runs.
4. The agent can reach its `target` host and the service ports behind it.
5. If you expect the UDP relay: the hub's UDP port is reachable inbound,
   and outbound UDP is allowed from both the client and the agent.

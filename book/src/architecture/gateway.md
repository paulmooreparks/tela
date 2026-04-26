# Gateways

A gateway in Tela is a forwarding node: a component in the middle of the path that lets traffic keep moving without changing what the traffic means. The rule is the same at every layer: forward without inspecting beyond what the layer requires.

This rule is not a policy choice. It is a structural property of the design. A relay that cannot read the payload cannot leak it, cannot alter it, and cannot be coerced into filtering it. The hub applies this rule at the WireGuard layer, forwarding opaque ciphertext. The bridge agent applies it at the TCP layer, forwarding raw streams. The path gateway applies it at the HTTP layer, reading only the URL path and nothing else. The same primitive recurs at four places in the architecture.

| Instance | Layer | Component | What it forwards | Content visibility |
|----------|-------|-----------|-----------------|-------------------|
| Path gateway | HTTP | `telad` | HTTP requests, routed by URL path to local services | URL path only |
| Bridge gateway | TCP | `telad` (bridge mode) | TCP streams from the tunnel to LAN-reachable machines | None |
| Upstream gateway | TCP | `telad` | Outbound dependency calls rerouted to different targets | None |
| Relay gateway (single-hop) | WireGuard | `telahubd` | Opaque WireGuard ciphertext between a paired client and agent | None |
| Relay gateway (multi-hop) | WireGuard | `telahubd` | Opaque WireGuard ciphertext between a client and an agent registered on a *different* hub | None |

A fifth instance, the multi-hop relay gateway, bridges sessions across more than one hub. It is the same primitive as the single-hop relay applied recursively: a hub that receives a paired session forwards it to an agent registered with a different hub, remaining blind to the payload at every hop. Each forwarding hub decrements a one-byte hop count (TTL) in the shared 7-byte frame header and drops the frame if the count reaches zero, which prevents forwarding loops without a routing protocol. The operator configures which destination hubs a bridging hub will dial via a `bridges:` section in `telahubd.yaml`; the bridging hub advertises the reachable machines through its own `/api/status` with a `reachableThrough` pointer so clients know which hub they are really talking to. For the full specification see [DESIGN-relay-gateway.md](https://github.com/paulmooreparks/tela/blob/main/DESIGN-relay-gateway.md).

## Why the rule matters

Every instance of the gateway primitive is content-blind except where the layer requires it. The path gateway is the one exception: it must read the URL path to route correctly. It reads nothing else. It does not authenticate, it does not transform, and it does not inspect request bodies or responses. Authentication is the hub's job, enforced before the session is established. Application-level auth is the application's job.

This division of responsibility is what makes each gateway instance composable. A path gateway behind a relay gateway (the hub) behind a multi-hop relay has additive security properties at each layer. The blind-relay property of the hub does not require the path gateway to be blind; it requires only that each component know its layer and nothing else.

## The path gateway

The path gateway is the instance users encounter most often. It is an HTTP reverse proxy that runs inside `telad` on a single tunnel port. It matches incoming HTTP requests by URL path prefix and forwards them to local services.

Without it, exposing a multi-service application through Tela means registering each service as a separate port. The connecting client gets separate local listeners for each port, and the application must know how to find its own dependencies. A web frontend that makes API calls to `/api/` cannot assume the API is reachable at the same origin unless something sits in front and routes by path. That something is usually nginx or Caddy, added as infrastructure that has nothing to do with the application itself.

The gateway eliminates that extra component. `telad` itself becomes the reverse proxy, configured in the same YAML that already describes the machine's services:

```yaml
machines:
  - name: barn
    services:
      - port: 5432
        name: postgres
        proto: tcp
    gateway:
      port: 8080
      routes:
        - path: /api/
          target: 4000
        - path: /metrics/
          target: 4100
        - path: /
          target: 3000
```

This registers two tunnel-exposed services: the gateway on port 8080 and PostgreSQL on port 5432. The three HTTP services (3000, 4000, 4100) are internal to the machine and not exposed individually. The gateway port is registered with the hub as a service named `gateway` with `proto: http`, so clients and TelaVisor can display it like any other service.

Routes are matched by longest prefix first. A request to `/api/users` matches `/api/` before `/`. A request to `/` that does not match any longer prefix falls through to the root route.

The gateway does not terminate TLS (the WireGuard tunnel already provides end-to-end encryption), does not load balance, does not transform requests or responses, and does not authenticate (that is the hub's job). It is a transparent path router.

## Why no changes to the hub or client

The gateway is entirely contained within `telad`. The hub sees the gateway port as another port in the registration, no different from any other service. The client connects to port 8080 like any other port. No protocol changes are required, and no hub or client changes are needed.

This is a consequence of the same principle: each component knows its layer. The hub knows it is relaying WireGuard packets. The client knows it is forwarding TCP to a local listener. Neither needs to know that port 8080 on a particular machine happens to be a path-routing proxy rather than a direct service.

For a configuration reference and deployment walkthrough, see the [Set up a path-based gateway](../howto/gateway.md) how-to guide.

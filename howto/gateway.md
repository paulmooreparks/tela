# How to use the Tela gateway (path-based reverse proxy)

The Tela **gateway** documented in this chapter is a built-in HTTP reverse proxy inside `telad`. It exposes one tunnel port and routes incoming HTTP requests to different local services based on URL path. It replaces the "I need an nginx in front of my services" piece of a typical microservice deployment.

## The gateway primitive

A *gateway* in Tela is the name for a class of forwarding primitives the project uses at several layers of the stack. The rule is the same in every instance: a node in the middle takes traffic that is going somewhere and lets it keep going, without changing what the traffic means. The node forwards. The node does not inspect beyond what the layer it operates at requires.

Today, Tela ships four gateways:

- **Path gateway (Layer 7).** The path-based HTTP reverse proxy inside `telad`. Routes requests by URL path to different local services. Content-aware (it must read the path) but transparent in every other respect. *This chapter is about this gateway.*
- **Bridge gateway (Layer 4 inbound).** A `telad` instance running on a gateway machine that forwards tunnel TCP traffic to services on other LAN-reachable machines, instead of services on the same host. Content-blind. Documented as the *gateway agent* or *bridge agent* deployment pattern in [howto/telad.md](telad.md).
- **Upstream gateway (Layer 4 outbound).** A configurable rerouting layer in `telad` that takes a local service's outbound dependency calls (e.g. `localhost:5432`) and forwards them to a different target. Content-blind. Documented in the [REFERENCE.md upstreams section](../REFERENCE.md#upstreams-dependency-routing).
- **Relay gateway (Layer 3, single-hop).** The hub itself. Forwards opaque WireGuard ciphertext between a paired client and agent. Content-blind end to end (the blind-relay property). Documented in [DESIGN.md](../DESIGN.md).

A fifth gateway, the **multi-hop relay gateway** that bridges sessions across more than one hub, is on the [1.0 roadmap](../ROADMAP-1.0.md) under *Important: Relay gateway*. It is the same primitive as the existing single-hop relay gateway, applied recursively so that one hub can forward a session to an agent registered with a different hub. The WireGuard handshake remains end to end between the original client and the destination agent. The bridging hub is blind to the payload, the same way today's hub is blind to the payload.

The rest of this chapter is a how-to for the path gateway specifically. Read it for the Layer 7 instance; the other gateways have their own chapters or reference sections.

## When you want a gateway

Use a gateway when you have several HTTP services on one machine and you want to reach all of them through a single tunnel port. Typical examples:

- A web frontend, a REST API, and a metrics endpoint, all served from the same host
- A multi-page web app with backend services on different ports
- A development stack you want to demo to a colleague through one URL

You do **not** need a gateway when:

- You only have one HTTP service. Just expose it as a normal service.
- Your services use TCP, not HTTP. The gateway only proxies HTTP. Expose them as normal TCP services.
- You already use nginx or Caddy in production and you want to keep that as your edge proxy. The gateway is for tunnel-internal routing, not for public HTTPS termination.

## What a gateway looks like to a user

Without a gateway, a developer connecting to a multi-service app gets one local port per service:

```
localhost:3000  -> web UI
localhost:4000  -> REST API
localhost:4100  -> metrics
```

The browser opens `localhost:3000` and tries to call the API. The API is on a different origin (`localhost:4000`), so the browser refuses (CORS), or the UI has to be configured with the API URL, or there has to be an extra proxy layer somewhere.

With a gateway, the developer gets one local port:

```
localhost:8080  -> gateway
```

Opening `localhost:8080/` serves the UI. The UI calls `localhost:8080/api/users`. The gateway sees the `/api/` prefix and proxies the request to the local API service. Same origin. No CORS. No extra config.

## Configuring the gateway

Gateway configuration lives in the `telad.yaml` file under each machine, alongside the `services:` list. A minimal example:

```yaml
hub: wss://your-hub.example.com
token: "<your-agent-token>"

machines:
  - name: launchpad
    target: 127.0.0.1
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

What this declares:

- A machine named `launchpad`
- One direct TCP service: PostgreSQL on port 5432 (exposed through the tunnel like any normal service)
- A gateway listening on port 8080 with three routes:
  - `/api/...` proxies to local port 4000
  - `/metrics/...` proxies to local port 4100
  - `/` (the catch-all) proxies to local port 3000

The HTTP services on ports 3000, 4000, and 4100 are **not** in the `services:` list. They are private to the machine and reachable only through the gateway. The tunnel exposes only port 8080 (the gateway) and port 5432 (PostgreSQL).

### Field reference

| Field | Required | Description |
|-------|----------|-------------|
| `gateway.port` | Yes | Port the gateway listens on inside the WireGuard tunnel. Does not need to match any local service port. |
| `gateway.routes` | Yes | List of routes, each mapping a URL path prefix to a local target port. |
| `routes[].path` | Yes | URL path prefix to match (e.g. `/api/`, `/admin/`, `/`). |
| `routes[].target` | Yes | Local TCP port to forward matched requests to (e.g. `4000`). |

### Route matching

Routes are matched by **longest path prefix first**. Order in the YAML does not matter; `telad` sorts them at startup. A route with `path: /` is the catch-all and matches any request not handled by a more specific route.

For example, with these routes:

```yaml
routes:
  - path: /
    target: 3000
  - path: /api/v2/
    target: 4002
  - path: /api/
    target: 4000
```

A request to `/api/v2/users` matches `/api/v2/` (target 4002), not `/api/` (which is shorter) and not `/` (which is even shorter).

A request to `/api/health` matches `/api/` (target 4000) because `/api/v2/` is not a prefix.

A request to `/about` matches `/` (target 3000).

## Connecting to a gateway

The gateway shows up to clients as a normal service named `gateway`. List it like any other service in your connection profile:

```yaml
# ~/.tela/profiles/launchpad.yaml
connections:
  - hub: wss://your-hub.example.com
    machine: launchpad
    services:
      - name: gateway
      - name: postgres
```

Then connect:

```bash
tela connect -profile launchpad
```

You will see:

```
localhost:8080  -> gateway (launchpad)
localhost:5432  -> postgres (launchpad)
```

Open `http://localhost:8080/` in a browser. The gateway serves the UI from local port 3000. API calls to `/api/...` are routed to local port 4000. Metrics calls to `/metrics/...` are routed to local port 4100.

### Renaming the local port

If `8080` clashes with something already running on your machine, override the local port the same way you do for any service:

```yaml
connections:
  - hub: wss://your-hub.example.com
    machine: launchpad
    services:
      - name: gateway
        local: 18080
      - name: postgres
        local: 15432
```

Now `localhost:18080` reaches the gateway. The gateway port on the agent side is still 8080.

### Direct access alongside the gateway

You can connect to a gateway **and** an underlying service directly at the same time. To get direct API access for `curl`/Postman/debugging, list the API as a normal service in the agent's `services:` list (in addition to the gateway), then include both in your profile:

```yaml
# telad.yaml on the agent
machines:
  - name: launchpad
    services:
      - port: 4000
        name: api
        proto: http
    gateway:
      port: 8080
      routes:
        - path: /api/
          target: 4000
        - path: /
          target: 3000
```

```yaml
# client profile
connections:
  - hub: wss://your-hub.example.com
    machine: launchpad
    services:
      - name: gateway
      - name: api
        local: 14000
```

Now you have:

- `localhost:8080/` -- the UI through the gateway
- `localhost:8080/api/users` -- the API through the gateway (path-routed)
- `localhost:14000/users` -- the API directly (bypassing the gateway)

This is useful when you want the browser experience for normal use and the direct port for debugging.

## Cross-environment scenarios

The gateway becomes especially useful when you maintain the same application across multiple environments (dev, staging, prod) on different hubs. Each environment runs its own `telad` with its own gateway config. A developer who wants to compare two environments side by side can connect to both:

```yaml
connections:
  - hub: wss://prod-hub.example.com
    machine: launchpad
    services:
      - name: gateway

  - hub: wss://staging-hub.example.com
    machine: launchpad
    services:
      - name: gateway
        local: 18080
```

Now `localhost:8080` is the prod app and `localhost:18080` is the staging app. Two browser tabs, two environments, identical URL structure inside each tab. The gateway routes are part of each environment's `telad.yaml`, so the routing logic stays with the environment.

## What the gateway does not do

The gateway is intentionally minimal. It does **not**:

- Terminate TLS. The WireGuard tunnel already provides end-to-end encryption between the client and `telad`. Adding TLS inside the tunnel would be redundant.
- Authenticate users. Connection-level auth is handled by Tela's hub tokens and ACLs. Application-level auth (login forms, OAuth, JWT) is the application's responsibility, the same as it would be without Tela.
- Load-balance. Each `telad` instance serves one machine. There is nothing to balance across.
- Transform requests or responses. It is a transparent proxy. The request the browser sends is the request the local service receives, except that the `Host` header is rewritten to the local target.
- Proxy WebSockets. WebSocket upgrade is not supported in the gateway itself. If you need WebSocket access to a service, expose it as a normal service alongside the gateway.
- Replace a production internet-facing reverse proxy. For internet-facing TLS termination, rate limiting, web application firewall rules, and load balancing, you still want nginx, Caddy, Traefik, or a managed edge service. The gateway is for the path inside the tunnel.

## Troubleshooting

**The gateway port shows up but requests return 502 or "connection refused".**

The gateway accepted the request but could not reach the local target service. Check that the target port (e.g. `4000`) is actually listening on `127.0.0.1` on the agent machine. If the service is in a Docker container, make sure the container's port is published to the host or that `target` points at `host.docker.internal`. If the service is bound to a specific interface (not `0.0.0.0` or `127.0.0.1`), the gateway will not reach it.

**The browser hits the wrong route.**

Remember that matching is by longest path prefix. If you intend for `/api/users` to match `/api/` but it is matching `/`, your `/api/` route is missing the trailing slash, or one of your other routes is incorrectly more specific. Check the agent's logs (`telad service logs` if running as a service, or stderr otherwise) for the route table that telad logs at startup.

**The gateway port is not in the connection's local listeners.**

Verify the client profile lists `gateway` as a service. The gateway is exposed by name, not by port number.

**A service that worked as a normal service stops working when moved behind the gateway.**

Make sure the service is not in the `services:` list anymore (or is intentionally exposed both ways for direct access). If both a normal service entry on port 4000 and a gateway route to port 4000 exist, the client may end up connecting to the wrong one depending on profile order.

## See also

- [REFERENCE.md, Gateway section](../REFERENCE.md#gateway-path-based-reverse-proxy) -- field-by-field configuration reference
- [DESIGN-gateway.md](../DESIGN-gateway.md) -- design rationale, comparison to alternatives, implementation notes
- [howto/telad.md](telad.md) -- general `telad` configuration including the unrelated **gateway/bridge agent** deployment pattern

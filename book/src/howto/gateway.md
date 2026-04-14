# Set up a path-based gateway

## What you are setting up

Picture a development machine running three HTTP services on different ports: a React frontend on port 3000, a REST API on port 4000, and a metrics endpoint on port 4100. Without a gateway, a colleague connecting through Tela would get three separate loopback bindings -- one per service port -- and the browser would see them as three different origins, triggering Cross-Origin Resource Sharing (CORS) issues every time the frontend calls the API.

The path-based gateway solves this by exposing a single tunnel port (for example, 8080) that routes incoming HTTP requests to the right local service based on the URL path prefix. Your colleague connects to one address and one port. The browser sends all requests -- frontend, API calls, metrics -- to the same origin. No CORS. No extra configuration on the application side.

When this chapter is done, a client connecting to your machine will see:

```
Services available:
  127.88.x.x:8080  → HTTP
```

Requests to `http://127.88.x.x:8080/` go to the frontend. Requests to `http://127.88.x.x:8080/api/` go to the API. Requests to `http://127.88.x.x:8080/metrics/` go to the metrics endpoint. The routing is defined in your `telad.yaml` and takes effect without restarting anything except `telad`.

The gateway is built into `telad`. It requires a few lines of YAML -- no separate binary, no nginx, no Caddy inside the tunnel.

For the design rationale and the broader gateway primitive family, see the [Gateways](../architecture/gateway.md) chapter in the Design Rationale section.

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

Without a gateway, a developer connecting to a multi-service app gets one loopback address per machine, but one binding per service port:

```
127.88.x.x:3000  → port 3000
127.88.x.x:4000  → port 4000
127.88.x.x:4100  → port 4100
```

The browser opens `http://127.88.x.x:3000` and tries to call the API. The API is on a different origin (`127.88.x.x:4000`) -- same host, different port, which still triggers Cross-Origin Resource Sharing (CORS) in the browser. The UI has to be configured with the API URL, or there has to be an extra proxy layer somewhere.

With a gateway, the developer gets one binding:

```
127.88.x.x:8080  → HTTP
```

Opening `http://127.88.x.x:8080/` serves the UI. The UI calls `/api/users`. The gateway sees the `/api/` prefix and proxies the request to the local API service. Same origin. No CORS. No extra config.

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
Services available:
  127.88.x.x:8080  → HTTP
  127.88.x.x:5432  → port 5432
```

Port labels come from the well-known port table (22=SSH, 80/8080=HTTP, 3389=RDP, etc.). Ports not in the table show as `port N`.

Open `http://127.88.x.x:8080/` in a browser. The gateway serves the UI from local port 3000. API calls to `/api/...` are routed to local port 4000. Metrics calls to `/metrics/...` are routed to local port 4100.

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

Now the gateway is at `http://127.88.x.x:18080/` instead of port 8080. The gateway port on the agent side is still 8080.

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

- `http://127.88.x.x:8080/` -- the UI through the gateway
- `http://127.88.x.x:8080/api/users` -- the API through the gateway (path-routed)
- `http://127.88.x.x:14000/users` -- the API directly (bypassing the gateway)

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

Each hub+machine pair gets a unique deterministic loopback address, so prod and staging appear at different `127.88.x.x` addresses and never conflict on the same port. The `local: 18080` override is optional -- use it if you want port-based disambiguation rather than navigating by address. Open two browser tabs, one for each address, and both show the same URL path structure since the gateway routes are defined in each environment's `telad.yaml`.

## What the gateway does not do

The gateway is intentionally minimal. It does **not**:

- Terminate TLS. The WireGuard tunnel already provides end-to-end encryption between the client and `telad`. Adding TLS inside the tunnel would be redundant.
- Authenticate users. Connection-level auth is handled by Tela's hub tokens and access control lists. Application-level auth (login forms, OAuth, JWT) is the application's responsibility, the same as it would be without Tela.
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

- [Gateways](../architecture/gateway.md) -- design rationale and the broader gateway primitive family
- [Run an agent](telad.md) -- general `telad` configuration including the bridge agent deployment pattern
- [Upstreams](../guide/upstreams.md) -- the outbound dependency routing counterpart to the path gateway

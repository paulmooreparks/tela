# The path gateway

The path gateway is a built-in HTTP reverse proxy inside `telad`. It exposes one tunnel port and routes incoming HTTP requests to different local services based on URL path prefix, eliminating the need for a separate nginx, Caddy, or Traefik instance for tunnel-internal routing.

## When to use a gateway

Use a gateway when you have several HTTP services on one machine and want to reach all of them through a single tunnel port. Common examples:

- A web frontend, a REST API, and a metrics endpoint running on the same host
- A multi-page web app with backend services on different ports
- A development stack you want accessible through one URL

You do not need a gateway when you have only one HTTP service (just expose it as a normal service), when your services use TCP rather than HTTP (expose them as normal TCP services), or when you already use a reverse proxy in front of your services and want to keep it as the edge.

## How it works

Without a gateway, a client connecting to a multi-service application gets one local port per service:

```
localhost:3000  -> web UI
localhost:4000  -> REST API
localhost:4100  -> metrics
```

The browser opens `localhost:3000` and calls the API on a different origin (`localhost:4000`). That is a cross-origin request, which means either CORS headers, a hardcoded API URL in the UI code, or an extra proxy layer somewhere.

With a gateway, the client gets one port:

```
localhost:8080  -> gateway
```

The browser opens `localhost:8080/`. The UI calls `/api/users`. The gateway sees the `/api/` prefix and proxies the request to the local API service. Same origin. No CORS. No extra configuration.

## Configuration

Gateway configuration lives in `telad.yaml` under each machine, alongside the `services:` list:

```yaml
hub: wss://your-hub.example.com
token: "<your-agent-token>"

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

This declares one direct TCP service (PostgreSQL on port 5432, exposed through the tunnel as usual) and a gateway listening on port 8080 with three routes. The HTTP services on ports 3000, 4000, and 4100 are not in the `services:` list -- they are private to the machine and reachable only through the gateway. The tunnel exposes port 8080 and port 5432.

### Field reference

| Field | Required | Description |
|-------|----------|-------------|
| `gateway.port` | Yes | Port the gateway listens on inside the WireGuard tunnel. Does not need to match any local service port. |
| `gateway.routes` | Yes | List of routes, each mapping a URL path prefix to a local target port. |
| `routes[].path` | Yes | URL path prefix to match (e.g. `/api/`, `/admin/`, `/`). |
| `routes[].target` | Yes | Local TCP port to proxy matched requests to. |

### Route matching

Routes are matched by longest path prefix first. The order in the YAML file does not matter; `telad` sorts them at startup. A route with `path: /` matches any request not claimed by a more specific route.

With these routes:

```yaml
routes:
  - path: /
    target: 3000
  - path: /api/v2/
    target: 4002
  - path: /api/
    target: 4000
```

A request to `/api/v2/users` matches `/api/v2/` (target 4002). A request to `/api/health` matches `/api/` (target 4000). A request to `/about` matches `/` (target 3000).

## Connecting through a gateway

The gateway appears to clients as a service named `gateway`. Use it in a connection profile like any other service:

```yaml
# ~/.tela/profiles/barn.yaml
connections:
  - hub: wss://your-hub.example.com
    machine: barn
    services:
      - name: gateway
      - name: postgres
```

```bash
tela connect -profile barn
```

Output:

```
localhost:8080  -> gateway (barn)
localhost:5432  -> postgres (barn)
```

If port 8080 conflicts with something local, override it:

```yaml
services:
  - name: gateway
    local: 18080
```

### Direct access alongside the gateway

You can expose a service both through the gateway (for browser access) and as a direct service (for tools like `curl` or Postman). Add it to the agent's `services:` list as well as the gateway routes, then include it in the profile:

```yaml
# telad.yaml
machines:
  - name: barn
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
# profile
connections:
  - hub: wss://your-hub.example.com
    machine: barn
    services:
      - name: gateway
      - name: api
        local: 14000
```

Now `localhost:8080/api/...` reaches the API through the gateway, and `localhost:14000/...` reaches it directly.

## Cross-environment use

When you maintain the same application across several environments, each running its own `telad`, a profile can connect to multiple gateways simultaneously:

```yaml
connections:
  - hub: wss://prod-hub.example.com
    machine: app
    services:
      - name: gateway

  - hub: wss://staging-hub.example.com
    machine: app
    services:
      - name: gateway
        local: 18080
```

`localhost:8080` is the prod application and `localhost:18080` is staging. The routing logic stays in each environment's `telad.yaml`, not in the client profile.

## Limitations

The gateway does not terminate TLS (the WireGuard tunnel already provides end-to-end encryption). It does not authenticate users (that is the hub's token and ACL layer). It does not load-balance across instances. It does not proxy WebSocket connections -- if you need WebSocket access to a service, expose it as a separate service alongside the gateway. It is not a replacement for an internet-facing reverse proxy with TLS termination, rate limiting, or WAF rules.

For the design rationale and the relationship between the path gateway and the other gateway primitives in Tela, see [Gateways](../architecture/gateway.md) in the Design Rationale section. For a step-by-step setup walkthrough and troubleshooting, see [Set up a path-based gateway](../howto/gateway.md).

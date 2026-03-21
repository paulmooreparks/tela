# Tela Gateway: Path-Based Reverse Proxy in telad

## The problem

You have three microservices running on a machine behind a firewall:

- A web frontend on port 3000
- A REST API on port 4000
- A health/metrics agent on port 4100
- A PostgreSQL database on port 5432

Today, to expose these through Tela, you register one machine with four ports. A connecting user gets four local ports. Their browser hits `localhost:3000` for the UI, and the UI makes API calls to... where? The browser does not know that `localhost:4000` is the API. The UI code must be configured with the API URL, or the deployment must include a reverse proxy (nginx, Caddy, Traefik) that sits in front of the services and routes by path.

Every microservice deployment behind Tela needs this reverse proxy. It is infrastructure boilerplate that has nothing to do with the application. It adds a container, a config file, and a failure mode.

The gateway eliminates this. telad itself becomes the reverse proxy.


## What the gateway does

The gateway is an HTTP reverse proxy that runs inside telad on a single port, exposed through the WireGuard tunnel. It matches incoming HTTP requests by URL path prefix and forwards them to local services. It is configured in the telad YAML alongside the existing service declarations.

The gateway does NOT:

- Terminate TLS (the tunnel is already encrypted end-to-end by WireGuard)
- Do load balancing (each telad instance serves one machine)
- Transform requests or responses (it is a transparent proxy)
- Authenticate users (the hub's token/ACL system controls who can connect; application-level auth is the application's job)
- Handle WebSocket upgrade (initially; this could be added later)
- Replace a full API gateway for production internet-facing traffic (it is for tunnel-internal routing only)


## How you configure it

### telad YAML: before

```yaml
hub: wss://test.demo.tela.sh
machines:
  - name: launchpad
    ports: [3000, 4000, 4100, 5432]
    services:
      - port: 3000
        name: http
        proto: http
      - port: 4000
        name: api
        proto: http
      - port: 4100
        name: metrics
        proto: http
      - port: 5432
        name: postgres
        proto: tcp
```

This registers four ports. A connecting client gets four local listeners. The UI service must know how to reach the API service, which means either:

1. The UI includes a built-in reverse proxy (couples routing to the app).
2. An nginx container sits in front of everything (adds infrastructure).
3. The UI hardcodes `localhost:4000` as the API URL (breaks when ports are remapped).

### telad YAML: with gateway

```yaml
hub: wss://test.demo.tela.sh
machines:
  - name: launchpad
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

This registers two tunnel-exposed services:

- Port 8080: the gateway (HTTP, path-routed to local services)
- Port 5432: PostgreSQL (TCP, direct passthrough, unchanged from today)

The gateway port (8080) does not need to match any local service port. It is a new listener on the netstack that telad manages. Requests to `GET /api/users` on port 8080 are proxied to `localhost:4000/api/users`. Requests to `GET /` are proxied to `localhost:3000/`.

The three HTTP services (3000, 4000, 4100) no longer appear in the `services` list. They are internal to the machine. Only the gateway port and the PostgreSQL port are exposed through the tunnel.


## How it works, step by step

### Step 1: telad starts

telad reads the YAML config. For the `launchpad` machine, it finds:

- One service entry (postgres on 5432): handled exactly as today.
- One gateway block: parsed into a `gatewayConfig` struct.

The gateway config produces a list of routes, sorted by path length (longest first) for prefix matching. Each route maps a path prefix to a local target port.

### Step 2: telad registers with the hub

The registration message includes the gateway port (8080) in the machine's port list, alongside any direct services (5432). The hub sees two ports for this machine.

The gateway port is also registered as a service with `proto: http` and `name: gateway`. The hub's status API reports it like any other service:

```json
{
  "id": "launchpad",
  "services": [
    {"port": 8080, "name": "gateway", "proto": "http"},
    {"port": 5432, "name": "postgres", "proto": "tcp"}
  ]
}
```

### Step 3: client connects

A user runs `tela connect -profile launchpad-test`. Their profile contains:

```yaml
connections:
  - hub: wss://test.demo.tela.sh
    machine: launchpad
    services: [gateway, postgres]
```

tela resolves `gateway` to port 8080 and `postgres` to port 5432 via the hub's status API. It creates two local listeners:

```
localhost:8080  -> tunnel -> 10.77.{idx}.1:8080 (gateway)
localhost:5432  -> tunnel -> 10.77.{idx}.1:5432 (postgres)
```

The user opens `http://localhost:8080` in their browser. They see the Launchpad UI.

### Step 4: browser makes requests

The browser loads the UI from `localhost:8080/`. The Launchpad UI makes API calls to `/api/deployments`. Because the UI is served from the same origin (`localhost:8080`), the API calls go to `localhost:8080/api/deployments`. No CORS issues. No separate API URL to configure.

The request flow:

```
Browser
  -> GET http://localhost:8080/api/deployments
  -> tela client listener on 127.0.0.1:8080
  -> WireGuard tunnel (encrypted)
  -> telad netstack on 10.77.{idx}.1:8080
  -> gateway route matching: /api/ -> target 4000
  -> telad dials localhost:4000
  -> proxies request to localhost:4000/api/deployments
  -> response flows back through the same path
```

### Step 5: direct service access (optional)

A developer who wants direct database access has `postgres` in their profile. They connect pgAdmin to `localhost:5432`. This bypasses the gateway entirely -- it is a direct TCP tunnel to the PostgreSQL port, exactly as Tela works today.

If a developer also wants direct API access (bypassing the gateway), they add the API service to the same connection:

```yaml
connections:
  - hub: wss://test.demo.tela.sh
    machine: launchpad
    services: [gateway, postgres, api]
```

This gives them three local ports:

- `localhost:8080` -- gateway (path-routed access to UI, API, and metrics)
- `localhost:5432` -- PostgreSQL (direct TCP for pgAdmin, DBeaver, etc.)
- `localhost:4000` -- API (direct HTTP, bypassing the gateway)

The `gateway` is a service name like any other. It resolves to port 8080 because that is what telad registered. Direct API access is opt-in. The default path is through the gateway.


## The cross-environment scenario

This is where the gateway becomes essential for Tela's story.

### The setup

Three environments, each running the same Launchpad stack:

| Environment | Hub | Machine | Gateway | DB |
|-------------|-----|---------|---------|-----|
| Dev | (local Docker) | launchpad | localhost:8080 | localhost:5432 |
| Test | wss://test.demo.tela.sh | launchpad | tunnel:8080 | tunnel:5432 |
| Prod | wss://prod.demo.tela.sh | launchpad | tunnel:8080 | tunnel:5432 |

### Scenario: "The API works in dev but not in test"

The developer wants to compare the test API's behavior against their local dev API.

Without the gateway, accessing the test environment requires managing individual ports, SSH tunnels, or VPN configs. With the gateway, the developer connects to both environments:

```yaml
connections:
  - hub: wss://test.demo.tela.sh
    machine: launchpad
    services:
      - name: gateway
      - name: api
        local: 14000
  - hub: wss://dev.demo.tela.sh
    machine: launchpad
    services:
      - name: gateway
        local: 18080
```

Now the developer has:

- `localhost:8080` -- test Launchpad (UI + API + metrics, all routed through test gateway)
- `localhost:14000` -- test API directly (for curl, Postman, debugging)
- `localhost:18080` -- dev Launchpad (UI + API + metrics, all routed through dev gateway)

They open both dashboards side by side. They hit the same API endpoint on both environments and compare the responses. No code changes, no infrastructure changes.

The test gateway routes `/api/` to the test API. The dev gateway routes `/api/` to the dev API. Each gateway is a self-contained unit. The developer sees the full application in each environment through a single port.

What this does NOT do: it does not let you point the test UI at the dev API through a single port. Each gateway's routes are fixed by the telad config. To remix routes across environments (test UI + dev API on one port), you would need the client-side gateway feature described below.

### Scenario: "Mix-and-match gateway"

What if the tela client could also run a local gateway? Not just telad, but tela itself.

The developer's profile:

```yaml
connections:
  - hub: wss://test.demo.tela.sh
    machine: launchpad
    services: [gateway]
  - hub: wss://dev.demo.tela.sh
    machine: launchpad
    services:
      - remote: 4000
        local: 14000

gateway:
  port: 9090
  routes:
    - path: /api/
      target: 14000
    - path: /
      target: 8080
```

Now `localhost:9090` serves:
- `/` from the test UI (via test gateway on 8080)
- `/api/` from the dev API (via dev tunnel on 14000)

The browser hits one URL. The routing is transparent. The developer is debugging a cross-environment issue without changing any code, any config, or any infrastructure.

This is the thing that no other tool does.


## How it compares to alternatives

### vs. nginx / Caddy / Traefik

These are reverse proxies that run as separate processes or containers. They require:

- A config file (nginx.conf, Caddyfile, docker labels)
- A running process
- Network configuration (Docker networking, host networking, ports)
- Maintenance (updates, restarts, log rotation)

The Tela gateway eliminates all of this for tunnel-internal routing. The config lives in the telad YAML that you already have. The process is telad, which is already running. The network is the WireGuard tunnel, which is already set up. There is nothing new to deploy, configure, or maintain.

For internet-facing production traffic with TLS termination, rate limiting, WAF rules, and load balancing, you still want a real reverse proxy. The Tela gateway is not a replacement for that. It is a replacement for the "I just need path routing inside my tunnel" use case.

### vs. ngrok

ngrok exposes one port per tunnel. To expose a multi-service application, you run multiple ngrok tunnels and manage the URLs manually. There is no path-based routing. There is no cross-environment mixing.

ngrok's paid plans offer edge routing, but this is for internet-facing traffic with ngrok's CDN. It does not help with connecting a dev frontend to a staging backend.

### vs. Tailscale / WireGuard VPN

Tailscale gives you a flat network. Every machine can reach every other machine. But there is no routing layer -- your application still needs its own reverse proxy if you want path-based routing to multiple services.

Tailscale Funnel exposes services to the internet, but again, one service per funnel. No path routing.

### vs. HashiCorp Boundary

Boundary provides session-scoped, identity-aware access to infrastructure. It is an access management layer, not a routing layer. You connect to a specific target (host + port), not to a path-routed gateway. Cross-environment mixing would require multiple Boundary sessions and manual port management.

### The unique value

Tela with the gateway is the only tool that provides:

1. Encrypted tunnel access to services behind firewalls (like all the above)
2. Path-based routing to multiple services through a single port (like nginx, but without nginx)
3. Cross-environment mixing through profile-based routing (unique to Tela)
4. Client-side gateway for remixing routes across environments (unique to Tela)

Point 3 and 4 are what make the gateway a competitive differentiator, not just a convenience feature.


## Implementation design

### New types in telad

```go
// gatewayConfig is parsed from the telad YAML.
type gatewayConfig struct {
    Port   uint16         `yaml:"port"`
    Routes []gatewayRoute `yaml:"routes"`
}

// gatewayRoute maps a URL path prefix to a local target port.
type gatewayRoute struct {
    Path   string `yaml:"path"`   // URL prefix, e.g. "/api/"
    Target uint16 `yaml:"target"` // local port, e.g. 4000
}
```

These are added to `machineConfig`:

```go
type machineConfig struct {
    // ... existing fields ...
    Gateway *gatewayConfig `yaml:"gateway,omitempty"`
}
```

### Gateway startup in telad

During `handleSession`, after the netstack and WireGuard device are up, telad starts the gateway listener if configured:

```go
if fsCfg != nil {
    cleanupFileShare = startFileShareListener(lg, tnet, sessionAgentIP, fsCfg)
}

if gw != nil {
    cleanupGateway = startGatewayListener(lg, tnet, sessionAgentIP, gw)
}
```

### startGatewayListener

This function:

1. Creates an HTTP reverse proxy for each route target.
2. Listens on `sessionAgentIP:gatewayPort` on the netstack.
3. Accepts HTTP connections, matches path prefix, proxies to the matching local target.

```go
func startGatewayListener(lg *log.Logger, tnet *netstack.Net,
    agentIP string, cfg *gatewayConfig) func() {

    // Sort routes by path length, longest first (most specific match wins)
    routes := make([]gatewayRoute, len(cfg.Routes))
    copy(routes, cfg.Routes)
    sort.Slice(routes, func(i, j int) bool {
        return len(routes[i].Path) > len(routes[j].Path)
    })

    // Build reverse proxies for each target
    type compiledRoute struct {
        prefix string
        proxy  *httputil.ReverseProxy
    }
    var compiled []compiledRoute
    for _, r := range routes {
        targetURL, _ := url.Parse(
            fmt.Sprintf("http://127.0.0.1:%d", r.Target))
        proxy := httputil.NewSingleHostReverseProxy(targetURL)
        compiled = append(compiled, compiledRoute{
            prefix: r.prefix,
            proxy:  proxy,
        })
    }

    // HTTP handler: match longest prefix, proxy to target
    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        for _, cr := range compiled {
            if strings.HasPrefix(r.URL.Path, cr.prefix) {
                cr.proxy.ServeHTTP(w, r)
                return
            }
        }
        http.Error(w, "no route matched", http.StatusBadGateway)
    })

    // Listen on netstack
    listenAddr := netip.AddrPortFrom(
        netip.MustParseAddr(agentIP), cfg.Port)
    listener, err := tnet.ListenTCPAddrPort(listenAddr)
    if err != nil {
        lg.Printf("[gateway] listen failed on %s: %v", listenAddr, err)
        return func() {}
    }

    server := &http.Server{Handler: handler}
    go server.Serve(listener)

    lg.Printf("[gateway] listening on %s (%d routes)", listenAddr, len(compiled))
    for _, r := range routes {
        lg.Printf("[gateway]   %s -> localhost:%d", r.Path, r.Target)
    }

    return func() {
        server.Close()
        listener.Close()
    }
}
```

This is approximately 60 lines of implementation code. The `httputil.ReverseProxy` from the standard library handles:

- Forwarding request headers
- Forwarding response headers
- Streaming response bodies
- Hop-by-hop header removal
- X-Forwarded-For injection

### Registration changes

The gateway port must be included in the registration so the hub and connecting clients know about it.

In the registration builder (inside `runMultiMachine`), add the gateway port to the ports list and add a service descriptor for it:

```go
if mc.Gateway != nil {
    reg.Ports = append(reg.Ports, mc.Gateway.Port)
    reg.Services = append(reg.Services, serviceDescriptor{
        Port:  mc.Gateway.Port,
        Name:  "gateway",
        Proto: "http",
        Description: "Path-based reverse proxy",
    })
}
```

No hub changes needed. The hub already handles arbitrary ports and services.

### Client-side gateway (tela)

The client-side gateway is a separate feature that can come later. It requires:

1. A `gateway` block in the profile YAML.
2. A local HTTP listener (not on the netstack, on 127.0.0.1).
3. The same path-matching and proxying logic, but targeting local tunnel ports instead of local service ports.

The profile parser in `cmd/tela/main.go` would need to recognize the `gateway` block and start a local HTTP server after all tunnels are established.

This is not required for the initial implementation. The telad-side gateway alone provides the core value. The client-side gateway is an enhancement for the cross-environment remixing scenario.


## What changes in the codebase

### Files to modify

| File | Change |
|------|--------|
| `cmd/telad/main.go` | Add `Gateway` field to `machineConfig`. Parse gateway config. Pass it to `handleSession`. Include gateway port in registration. |
| `cmd/telad/gateway.go` | New file. `startGatewayListener` function (~80 lines). |

### Files that do NOT change

| File | Why |
|------|-----|
| `cmd/tela/main.go` | The client does not know or care about the gateway. It connects to port 8080 like any other port. The gateway is transparent. |
| `cmd/telahubd/main.go` | The hub does not know or care about the gateway. It sees port 8080 as another port in the registration. |
| `cmd/tela/control.go` | No changes needed. The control API reports bound services as before. |
| `cmd/telagui/app.go` | TelaVisor connects to services by port. The gateway port is just another service. |

This is the key architectural property: the gateway is entirely contained within telad. No protocol changes. No hub changes. No client changes. It is a local routing feature that happens to be exposed through the tunnel.


## Example: Launchpad deployment

### Dev environment (user's laptop)

`docker-compose.yml`:

```yaml
services:
  ui:
    build: ./ui
    ports: ["3000:3000"]
  api:
    build: ./api
    ports: ["4000:4000"]
    depends_on: [db]
  agent:
    build: ./agent
    ports: ["4100:4100"]
    depends_on: [api]
  db:
    image: postgres:16-alpine
    ports: ["5432:5432"]
    environment:
      POSTGRES_DB: launchpad
      POSTGRES_USER: launchpad
      POSTGRES_PASSWORD: launchpad
    volumes: [pgdata:/var/lib/postgresql/data]
volumes:
  pgdata:
```

`telad.yaml` (run alongside Docker):

```yaml
hub: wss://dev.demo.tela.sh
token: <developer-token>
machines:
  - name: launchpad
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

The developer runs:

```
docker compose up -d
telad -config telad.yaml
```

Their Launchpad instance is now accessible to anyone with a token for the dev hub.

### Test environment (barn)

Same stack, different hub, different data:

```yaml
hub: wss://test.demo.tela.sh
token: <test-token>
machines:
  - name: launchpad
    services:
      - port: 5432
        name: postgres
        proto: tcp
    gateway:
      port: 8080
      routes:
        - path: /api/
          target: 14000
        - path: /metrics/
          target: 14100
        - path: /
          target: 13000
```

(Ports are offset to 13000/14000/14100 because prod is on the same machine using 3000/4000/4100.)

### Prod environment (barn)

```yaml
hub: wss://prod.demo.tela.sh
token: <prod-token>
machines:
  - name: launchpad
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

### User profiles

**Viewer profile (prod only):**

```yaml
connections:
  - hub: wss://prod.demo.tela.sh
    machine: launchpad
    services: [gateway]
```

Result: `localhost:8080` serves the prod Launchpad dashboard. The user can view deployments, services, and logs. They cannot access the database or any individual service directly. The hub ACL enforces this -- their token only grants `viewer` access.

**Developer profile (dev + test):**

```yaml
connections:
  - hub: wss://dev.demo.tela.sh
    machine: launchpad
    services: [gateway, postgres]
  - hub: wss://test.demo.tela.sh
    machine: launchpad
    services:
      - name: gateway
        local: 18080
      - name: postgres
        local: 15432
```

Result:

```
localhost:8080   -> dev Launchpad (UI + API + metrics, all routed)
localhost:5432   -> dev PostgreSQL
localhost:18080  -> test Launchpad (UI + API + metrics, all routed)
localhost:15432  -> test PostgreSQL
```

Two full environments, two ports each, one tela process. No VPN. No SSH tunnels. No nginx.

**Ops profile (prod + test monitoring):**

```yaml
connections:
  - hub: wss://prod.demo.tela.sh
    machine: launchpad
    services: [gateway]
  - hub: wss://test.demo.tela.sh
    machine: launchpad
    services:
      - name: gateway
        local: 18080
```

Result: prod on 8080, test on 18080. The ops engineer watches both dashboards side by side.


## WebSocket support (future)

The initial implementation proxies HTTP only. WebSocket upgrade requests would fail because `httputil.ReverseProxy` does not handle the `Connection: Upgrade` handshake.

To add WebSocket support, the gateway handler would detect the `Upgrade: websocket` header and switch to a bidirectional TCP copy (the same `io.Copy` pattern used by `proxyToTarget`). This is approximately 20 additional lines of code. It is not needed for Launchpad (which uses REST), but would be needed for applications with real-time features (chat, live dashboards, collaborative editing).


## Testing plan

1. **Unit test**: gateway route matching with various path prefixes, edge cases (trailing slashes, overlapping prefixes, root-only routes).
2. **Integration test**: start telad with gateway config, connect tela, make HTTP requests through the tunnel, verify correct routing.
3. **Cross-environment test**: connect to two gateways on different hubs, verify independent routing.
4. **Error cases**: target service not running (502), no matching route (502), gateway port conflict, malformed config.


## Summary

The gateway is a small feature (one new file, approximately 80-100 lines of Go) with large impact. It:

- Eliminates the need for nginx/Caddy/Traefik in tunnel-internal deployments
- Makes multi-service applications work through a single tunnel port
- Enables the cross-environment debugging scenario that is unique to Tela
- Requires no changes to the hub, the client, or the protocol
- Lays the groundwork for the client-side gateway (profile-level route remixing)

The implementation builds on existing patterns in telad (netstack listeners, `httputil.ReverseProxy` from the standard library) and requires no new dependencies.

# Set Up a Path-Based Gateway

This walkthrough configures a path gateway on a real development stack and
verifies it end to end. The concept, the full field reference, and the
route-matching rules are in [The Path Gateway](../guide/gateway.md); this
chapter is the worked example.

## The Starting Point

A development machine named `launchpad` runs three HTTP services on
different ports: a React frontend on port 3000, a REST API on port 4000,
and a metrics endpoint on port 4100. It also runs PostgreSQL on port 5432.
The machine already has `telad` registered with a hub (see
[Run an Agent](telad.md) if it does not).

Without a gateway, a colleague connecting through Tela gets three separate
loopback bindings, one per HTTP port, and the browser treats them as three
different origins. Every frontend call to the API is a cross-origin
request, which means Cross-Origin Resource Sharing (CORS) headers, a
configurable API URL in the UI code, or an extra proxy.

When this walkthrough is done, the colleague sees one binding:

```
Services available:
  localhost:8080   → HTTP
  localhost:5432   → port 5432
```

Requests to `http://localhost:8080/` reach the frontend, `/api/...` reaches
the API, and `/metrics/...` reaches the metrics endpoint, all on one
origin. No CORS. No changes to the application.

## Step 1: Add the Gateway to telad.yaml

On `launchpad`, extend the machine entry with a `gateway:` block:

```yaml
hub: wss://hub.example.com
token: "<agent-token>"

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

The three HTTP services are deliberately not in the `services:` list. They
are private to the machine and reachable only through the gateway. The
tunnel exposes port 8080 (the gateway) and port 5432 (PostgreSQL). Routes
match by longest path prefix, so `/api/v2/users` would go to a `/api/v2/`
route before `/api/`, regardless of the order in the file.

Restart `telad` to apply the change. At startup it logs the route table it
loaded, which is worth a glance the first time.

## Step 2: Add the Gateway to the Client Profile

The gateway appears to clients as a service named `gateway`. On the
connecting machine:

```yaml
# ~/.tela/profiles/launchpad.yaml
connections:
  - hub: wss://hub.example.com
    machine: launchpad
    services:
      - name: gateway
      - name: postgres
```

```bash
tela connect -profile launchpad
```

The output lists `localhost:8080 → HTTP`. If port 8080 is taken on the
client machine, either accept the fallback port the output shows or pin a
different one with `local: 18080` on the gateway service entry.

## Step 3: Verify

Open `http://localhost:8080/` in a browser: the frontend loads from local
port 3000 on `launchpad`. Open the browser's network inspector and trigger
an API call: `/api/...` requests return from the API on port 4000, same
origin, no CORS preflight failures. `curl http://localhost:8080/metrics/`
returns the metrics endpoint.

## Optional: Direct Access for Debugging

To also reach the API directly with `curl` or Postman, bypassing the
gateway, expose it as a named service alongside the gateway and give it an
explicit local port in the profile:

```yaml
# telad.yaml addition
    services:
      - port: 5432
        name: postgres
        proto: tcp
      - port: 4000
        name: api
        proto: http
```

```yaml
# profile addition
    services:
      - name: gateway
      - name: postgres
      - name: api
        local: 14000
```

Now `http://localhost:8080/api/users` goes through the gateway and
`http://localhost:14000/users` hits the API directly.

## Optional: The Same App in Two Environments

When the same application runs in several environments, each with its own
`telad` and gateway config, one profile can connect to both side by side:

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

Give the second gateway an explicit `local:` port so you are not relying on
fallback behavior, then open one browser tab per port. The URL path
structure is identical in both, because the routing lives in each
environment's `telad.yaml`, not in the client profile.

## Troubleshooting

**The gateway port shows up but requests return 502 or "connection
refused".** The gateway accepted the request but could not reach the local
target service. Check that the target port (for example 4000) is actually
listening on `127.0.0.1` on the agent machine. If the service runs in a
Docker container, publish the container's port to the host or point
`target` at `host.docker.internal`. If the service is bound to a specific
non-loopback interface, the gateway will not reach it.

**The browser hits the wrong route.** Matching is by longest path prefix.
If `/api/users` is landing on the `/` route, the `/api/` route is probably
missing its trailing slash, or another route is unexpectedly more specific.
Check the route table `telad` logs at startup (via
`journalctl -u telad`, the Windows Event Viewer, or
`tela admin agent logs -machine launchpad`).

**The gateway port is not in the connection's local listeners.** Verify
the client profile lists `gateway` as a service. The gateway is exposed by
name, not by port number.

**A service that worked as a normal service stops working when moved
behind the gateway.** Make sure the service is no longer in the
`services:` list, or is intentionally exposed both ways as in the
direct-access setup above. If a normal service entry on port 4000 and a
gateway route to port 4000 both exist unintentionally, the client may
connect to the wrong one depending on profile order.

**A WebSocket-dependent app misbehaves behind the gateway.** Expose that
service directly as a TCP service alongside the gateway and connect to it
on its own port.

## See Also

- [The Path Gateway](../guide/gateway.md): concept, configuration
  reference, and route-matching rules
- [Run an Agent](telad.md): general `telad` configuration, including the
  bridge deployment pattern
- [Upstreams](../guide/upstreams.md): the outbound dependency-routing
  counterpart to the path gateway

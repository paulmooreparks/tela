# Upstreams

An upstream is a TCP forwarding rule inside `telad` that intercepts a local service's outbound dependency calls and routes them to a configurable target. A service calls `localhost:5432` expecting to reach its database; `telad` listens on that port and forwards the connection to wherever the database actually is.

Upstreams start when `telad` starts and run independently of any tunnel session. They provide a dispatch layer that you can change by editing a YAML file, without touching application code, containers, or environment variables.

## Configuration

Upstreams are declared per machine in `telad.yaml`:

```yaml
machines:
  - name: barn
    ports: [8080]
    upstreams:
      - port: 5432
        target: db.internal:5432
        name: postgres

      - port: 6379
        target: cache.internal:6379
        name: redis
```

`telad` binds port 5432 and port 6379 on all interfaces immediately on startup. Any process on the machine that connects to those ports (including via `localhost`) gets forwarded to the respective targets.

### Field reference

| Field | Required | Description |
|-------|----------|-------------|
| `port` | Yes | Local port to listen on. `telad` binds `0.0.0.0:<port>`. |
| `target` | Yes | Address to forward connections to, in `host:port` form. |
| `name` | No | Human-readable label used in log output. |

## What upstreams are for

The typical use case is service-to-service dependency routing in development and staging environments.

A web service configured to connect to `localhost:5432` works against a local database in development. In staging, the database is on a separate machine at `db.staging.internal:5432`. Without upstreams, changing environments means changing the application's configuration, rebuilding a container, or updating environment variables.

With an upstream, the application configuration stays the same in every environment. You change the `target` in `telad.yaml` and restart `telad`. The application never knows the database moved.

```yaml
# telad.yaml on the staging machine
upstreams:
  - port: 5432
    target: db.staging.internal:5432
    name: postgres
```

The application calls `localhost:5432`. `telad` forwards to `db.staging.internal:5432`. No application change required.

## Upstreams through a Tela tunnel

The upstream `target` field accepts any reachable `host:port`, including the deterministic loopback addresses that `tela connect` assigns to remote machines. When a machine runs both `telad` (as an agent registering its own services) and `tela` (as a client connected to a remote machine), an upstream can bridge the two.

For example:

- Machine A runs `telad` and exposes a service on port 8080.
- Machine B runs `tela connect` to machine A. The service on machine A becomes reachable on machine B at `localhost:PORT` -- for example, `localhost:8080` if that port is free, or `localhost:18080` if it is taken. Use `tela status` on machine B to find the exact port.
- Machine B also runs `telad` with an upstream: `port: 8080, target: localhost:8080` (substitute the actual bound port).
- Any application on machine B that calls `localhost:8080` reaches the service on machine A through the tunnel.

This is an advanced pattern. For most cases, direct service exposure through the tunnel is simpler.

## Upstreams are not gateways

Upstreams and the path gateway are both forwarding primitives in `telad`, but they operate differently:

- The **upstream** intercepts outbound calls from services running on the agent machine and routes them to a dependency. It is invisible to the services using it.
- The **path gateway** accepts inbound HTTP connections through the WireGuard tunnel and routes them to local services by URL path. It is visible to connecting clients as a named service.

Use an upstream when a service needs to reach a dependency at a different address than it expects. Use a gateway when clients connecting through Tela need to reach multiple HTTP services through one tunnel port.

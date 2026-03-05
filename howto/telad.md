# How to set up `telad` (machines + services + connectivity)

`telad` is the agent that registers one or more “machines” with a Hub and exposes services (SSH/RDP/etc.) to clients.

This doc focuses on **what must be reachable** and how that changes between deployment patterns.

## Two deployment patterns

### 1) Endpoint agent (direct)

- `telad` runs on the machine that actually hosts the services.
- Services are usually reachable on `localhost`.

Connectivity:

- `telad` needs **outbound** connectivity to the Hub (`ws://` or `wss://`).
- No inbound Internet ports required on the endpoint.

### 2) Gateway / bridge agent

- `telad` runs on a gateway (VM/container) and “points at” a target machine.
- Services must be reachable from the gateway.

Connectivity:

- `telad` needs **outbound** connectivity to the Hub.
- The gateway host must be able to reach the target host on the service ports.

A common Docker variant is bridging from an agent container to services running on the Docker host:

- `target: host.docker.internal`

## Config basics

Example `telad.yaml`:

```yaml
hub: wss://your-hub.example.com

machines:
  - name: barn
    os: windows
    services:
      - port: 22
        proto: tcp
        name: SSH
      - port: 3389
        proto: tcp
        name: RDP
    target: host.docker.internal
```

Notes:

- `hub:` must be reachable from where `telad` runs.
  - For local development (no TLS), a `ws://localhost:8080` hub URL is typical.
- If `target:` is omitted, `telad` assumes the services are local to the agent host.

## Service reachability checklist

For each declared service:

- Verify the service is listening on the target host.
- Verify the `telad` host can reach `target:<port>`.
  - In gateway mode, this is the most common failure.

## UDP relay (optional)

If the Hub advertises UDP relay, `telad` may send UDP to the hub’s UDP port.

- If UDP is blocked, sessions still work via WebSockets.

## Quick troubleshooting

- Machine never appears in hub status:
  - check the hub URL in `hub:` (DNS + firewall)
  - check the Hub is actually reachable from the agent network
- Services show but connect fails:
  - in gateway mode, confirm reachability from agent → target on the service port

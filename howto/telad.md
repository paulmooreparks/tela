# How to set up `telad` (machines + services + connectivity)

`telad` is the daemon that registers one or more "machines" with a Hub and exposes services (SSH/RDP/etc.) to clients.

This doc focuses on configuration, authentication, and **what must be reachable** across deployment patterns.

## Two deployment patterns

### 1) Endpoint daemon (direct)

- `telad` runs on the machine that actually hosts the services.
- Services are usually reachable on `localhost`.

Connectivity:

- `telad` needs **outbound** connectivity to the Hub (`ws://` or `wss://`).
- No inbound Internet ports required on the endpoint.

### 2) Gateway / bridge daemon

- `telad` runs on a gateway (VM/container) and "points at" a target machine.
- Services must be reachable from the gateway.

Connectivity:

- `telad` needs **outbound** connectivity to the Hub.
- The gateway host must be able to reach the target host on the service ports.

A common Docker variant is bridging from a daemon container to services running on the Docker host:

- `target: host.docker.internal`

## Config basics

Example `telad.yaml`:

```yaml
hub: wss://your-hub.example.com
token: "<token-for-this-agent>"

machines:
  - name: barn
    os: windows
    services:
      - port: 22
        name: SSH
      - port: 3389
        name: RDP
    target: host.docker.internal
```

Notes:

- `hub:` must be reachable from where `telad` runs.
  - For local development (no TLS), a `ws://localhost` hub URL is typical.
- `token:` is required when the hub has authentication enabled (recommended for any Internet-facing hub). This is a token generated with `telahubd user add` or `tela admin add-token`.
- If `target:` is omitted, `telad` assumes the services are local to the daemon host.

### Quick-start with flags

Instead of a config file, you can pass everything on the command line:

```bash
telad -hub wss://your-hub.example.com -machine barn -ports "22:SSH,3389:RDP" -token <token>
```

For production, prefer a config file and run `telad` as an OS service (see [services.md](services.md)).

## Authentication

If the hub has authentication enabled (which is recommended), `telad` must present a valid token to connect.

### Getting a token for telad

From any workstation with the hub's owner token:

```bash
# Create an identity for this agent
tela admin add-token barn-agent -hub wss://your-hub.example.com -token <owner-token>
# → Save the printed token

# Grant the agent permission to register the machine
tela admin grant barn-agent barn -hub wss://your-hub.example.com -token <owner-token>
```

Or directly on the hub machine:

```bash
telahubd user add barn-agent
telahubd user grant barn-agent barn
```

### Providing the token

**Config file** (recommended):

```yaml
hub: wss://your-hub.example.com
token: "<barn-agent-token>"

machines:
  - name: barn
    ports: [22, 3389]
```

**Command line:**

```bash
telad -hub wss://your-hub.example.com -machine barn -ports "22,3389" -token <barn-agent-token>
```

**Environment variable:**

```bash
export TELA_TOKEN=<barn-agent-token>
telad -hub wss://your-hub.example.com -machine barn -ports "22,3389"
```

## Running as an OS service

`telad` can run as a native service on Windows, Linux, and macOS:

```bash
# Install (copies config to system path)
telad service install -config telad.yaml

# Manage
telad service start
telad service stop
telad service restart
telad service uninstall
```

See [services.md](services.md) for platform-specific details and troubleshooting.

## Service reachability checklist

For each declared service:

- Verify the service is listening on the target host.
- Verify the `telad` host can reach `target:<port>`.
  - In gateway mode, this is the most common failure.

## UDP relay (optional)

If the Hub advertises UDP relay, `telad` may send UDP to the hub's UDP port.

- If UDP is blocked, sessions still work via WebSockets.

## Quick troubleshooting

- Machine never appears in hub status:
  - Check the hub URL in `hub:` (DNS + firewall).
  - Check the Hub is actually reachable from the daemon's network.
  - If the hub has auth enabled, check that `token:` is set and the token is valid.
- `telad` logs "auth_required" or "forbidden":
  - The token is missing, expired, or does not have permission to register this machine. Use `tela admin list-tokens` to verify the identity exists, and `tela admin grant` to grant machine access.
- Services show but connect fails:
  - In gateway mode, confirm reachability from daemon → target on the service port.

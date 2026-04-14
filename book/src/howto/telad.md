# Run an agent

## What you are setting up

The agent (`telad`) is the daemon that runs on -- or near -- the machine you want to reach. It makes an outbound connection to the hub, registers the machine under a name you choose, and tells the hub which TCP ports to expose to connecting clients. No inbound ports are required on the agent machine.

Picture a Linux server named `barn` sitting on a private network behind a router. It has SSH on port 22 and a Postgres database on port 5432. Without Tela, reaching those services from the outside requires a VPN, a bastion host, or an open inbound port. With Tela, you install `telad` on `barn`, point it at your hub, and declare which ports to expose. From that moment, any client with the right token can connect to `barn`'s services through the hub -- from anywhere, without any firewall changes on `barn`'s network.

By the end of this chapter you will have:

- `telad` installed and configured with a `telad.yaml`
- A machine registered with the hub under a name like `barn`
- One or more services exposed through the tunnel (SSH, RDP, or any TCP service)
- An agent token that scopes the agent's access to just what it needs
- `telad` running as a managed OS service so it survives reboots

The chapter covers two deployment patterns: the endpoint pattern (agent runs directly on the target machine, which is the most common case) and the gateway pattern (agent runs on a separate machine and forwards to LAN-reachable targets, which is useful for containers, Docker hosts, or machines you cannot install software on).

## Two deployment patterns

### 1) Endpoint daemon (direct)

- `telad` runs on the machine that actually hosts the services.
- Services are usually reachable on `localhost`.

Connectivity:

- `telad` needs **outbound** connectivity to the hub (`ws://` or `wss://`).
- No inbound Internet ports required on the endpoint.

### 2) Gateway / bridge daemon

- `telad` runs on a gateway (VM/container) and "points at" a target machine.
- Services must be reachable from the gateway.

Connectivity:

- `telad` needs **outbound** connectivity to the hub.
- The gateway host must be able to reach the target host on the service ports.

A common Docker variant is bridging from a daemon container to services running on the Docker host:

- `target: host.docker.internal`

## Config basics

Example `telad.yaml`:

```yaml
hub: wss://your-hub.example.com
token: "<agent-token>"   # user-role token with register permission; NOT the owner token

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
- `token:` is required when the hub has authentication enabled (recommended for any Internet-facing hub). This is an agent token -- a user-role token with register permission on this machine -- generated with `tela admin tokens add` (or `telahubd user add` on the hub machine directly). Do not use the hub's owner token here.
- If `target:` is omitted, `telad` assumes the services are local to the daemon host.

### Quick-start with flags

Instead of a config file, you can pass everything on the command line:

```bash
telad -hub wss://your-hub.example.com -machine barn -ports "22:SSH,3389:RDP" -token <agent-token>
```

For production, prefer a config file and run `telad` as an OS service (see [Run Tela as an OS service](services.md)).

## Authentication

If the hub has authentication enabled (which is recommended), `telad` must present a valid token to connect.

### Getting a token for telad

From any workstation with the hub's owner token:

```bash
# Create an identity for this agent
tela admin tokens add barn-agent -hub wss://your-hub.example.com -token <owner-token>
# → Save the printed token

# Grant the agent permission to register the machine
tela admin access grant barn-agent barn register -hub wss://your-hub.example.com -token <owner-token>
```

Or directly on the hub machine (when the hub is stopped):

```bash
telahubd user add barn-agent
telahubd user grant barn-agent barn
```

Note: `telahubd user grant` creates a machine access control list entry for "barn" with no `registerToken` restriction, which means any known identity (including barn-agent) can register that machine. It also explicitly grants barn-agent connect access to "barn". To restrict registration to a specific token only, use `tela admin access grant barn-agent barn register` via the admin API instead.

### Providing the token

**Credential store** (recommended for long-lived agents):

On the agent machine (requires elevation):

```bash
telad login -hub wss://your-hub.example.com
# Prompts for token and optional identity
# Stores in system credential store (survives service restart)
```

The token is now automatically found whenever `telad` connects to that hub.

**Config file** (recommended for YAML-based deployments):

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

**Token lookup precedence:**

1. `-token` flag (explicit)
2. `TELA_TOKEN` environment variable
3. Per-machine token in config file
4. Top-level token in config file
5. Credential store by hub URL

## Running as an OS service

`telad` can run as a native service on Windows, Linux, and macOS. Configuration is stored securely in the service metadata (no file permission issues).

Two installation modes:

**Mode 1: From a config file**

```bash
telad service install -config telad.yaml
```

The configuration is validated and stored in service metadata. A reference copy is retained on disk for manual editing.

**Mode 2: Inline configuration (recommended for simple setups)**

```bash
telad service install -hub ws://your-hub:8080 -machine barn -ports "22:SSH,3389:RDP"
```

Configuration is passed as command-line flags and stored inline. No external file needed. Ideal for single-machine deployments.

**Manage the service:**

```bash
telad service start
telad service stop
telad service restart
telad service status
telad service uninstall
```

**Reconfigure:** Edit the YAML config file (if one exists) and run `telad service restart`, or reinstall with new parameters.

See [Run Tela as an OS service](services.md) for platform-specific details and troubleshooting.

## Service reachability checklist

For each declared service:

- Verify the service is listening on the target host.
- Verify the `telad` host can reach `target:<port>`.
  - In gateway mode, this is the most common failure.

## UDP relay (optional)

If the hub advertises UDP relay, `telad` may send UDP to the hub's UDP port.

- If UDP is blocked, sessions still work via WebSockets.

## Quick troubleshooting

- Machine never appears in hub status:
  - Check the hub URL in `hub:` (DNS + firewall).
  - Check the hub is actually reachable from the daemon's network.
  - If the hub has auth enabled, check that `token:` is set and the token is valid.
- `telad` logs "auth_required" or "forbidden":
  - The token is missing, expired, or does not have permission to register this machine. Use `tela admin tokens list` to verify the identity exists, and `tela admin access grant` to grant machine access.
- Services show but connect fails:
  - In gateway mode, confirm reachability from daemon to target on the service port.

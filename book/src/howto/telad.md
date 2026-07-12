# Run an Agent

The agent (`telad`) is the daemon that runs on, or near, the machine you
want to reach. It makes an outbound connection to the hub, registers the
machine under a name you choose, and tells the hub which TCP ports to
expose to connecting clients. No inbound ports are required on the agent
machine.

Picture a Linux server named `barn` sitting on a private network behind a
router. It has SSH on port 22 and a PostgreSQL database on port 5432.
Without Tela, reaching those services from the outside requires a VPN, a
bastion host, or an open inbound port. With Tela, you install `telad` on
`barn`, point it at your hub, and declare which ports to expose. From that
moment, any client with the right token can connect to `barn`'s services
through the hub, from anywhere, without any firewall changes on `barn`'s
network.

This chapter takes you through configuring the agent, getting it a properly
scoped token, and choosing between the two deployment patterns. Running the
agent as a managed OS service is covered in
[Run Tela as an OS Service](services.md).

## Two Deployment Patterns

**Endpoint agent (the common case).** `telad` runs on the machine that
hosts the services, and the services are reachable on `localhost`. The
agent needs outbound connectivity to the hub (`ws://` or `wss://`) and
nothing else.

**Gateway, or bridge, agent.** `telad` runs on a separate machine (a VM, a
container, a small box at the site) and forwards to services on other
machines it can reach over the local network. Each machine entry in the
config carries a `target:` address pointing at the real host. Use this when
you cannot install software on the target machine, or when one agent should
front many devices. The gateway host must be able to reach each target on
the declared service ports, which makes it the most common failure point;
verify that reachability first when troubleshooting.

A common Docker variant is bridging from an agent container to services
running on the Docker host, using `target: host.docker.internal`.

## Configuration

A minimal endpoint-agent `telad.yaml`:

```yaml
hub: wss://hub.example.com
token: "<agent-token>"   # user-role token with register permission; NOT the owner token

machines:
  - name: barn
    services:
      - port: 22
        name: SSH
      - port: 5432
        name: postgres
```

A gateway-agent variant, fronting a NAS elsewhere on the LAN:

```yaml
hub: wss://hub.example.com
token: "<agent-token>"

machines:
  - name: nas
    target: 192.168.1.50
    services:
      - port: 22
        name: SSH
```

Notes on the fields:

- `hub:` must be reachable from where `telad` runs. For local development
  without TLS, a `ws://localhost:8080` hub URL is typical.
- `token:` is required when the hub has authentication enabled, which is
  the default and the right choice for any internet-facing hub. Use a
  dedicated agent token, not the hub's owner token; see
  [Authentication](#authentication) below.
- `target:` defaults to `127.0.0.1`. Set it to a remote address for the
  gateway pattern.
- `services:` can also be written as a bare port list, `ports: [22, 5432]`,
  which registers the ports without names. Named services display better
  in clients and are required for per-service access grants.

The full machine schema (display names, tags, location, per-machine tokens,
gateways, upstreams, file shares) is in
[Appendix A: CLI Reference](../guide/reference.md).

### Quick Start with Flags

Instead of a config file, you can pass everything on the command line. The
`-ports` flag accepts bare ports or `port:name` pairs:

```bash
telad -hub wss://hub.example.com -machine barn -ports "22:SSH,5432:postgres" -token <agent-token>
```

For production, prefer a config file and run `telad` as an OS service.

## Authentication

If the hub has authentication enabled, `telad` must present a valid token
to connect.

### Getting a Token for telad

From any workstation with the hub's owner token:

```bash
# Create an identity for this agent
tela admin tokens add barn-agent -hub wss://hub.example.com -token <owner-token>
# Save the printed token

# Grant the agent permission to register the machine
tela admin access grant barn-agent barn register -hub wss://hub.example.com -token <owner-token>
```

Alternatively, generate a one-time register pairing code
(`tela admin pair-code barn -type register`) and redeem it on the agent
host with `telad pair`; see [Credentials and Pairing](../guide/credentials.md).

You can also create the identity directly on the hub machine with
`telahubd user add barn-agent` followed by
`telahubd user grant barn-agent barn`. Note that `telahubd user grant`
creates a machine ACL entry without a register-token restriction, which
means any known identity can register that machine. To restrict
registration to one specific token, use
`tela admin access grant barn-agent barn register` via the admin API
instead.

### Providing the Token

**Credential store** (recommended for long-lived agents). On the agent
machine, with elevation:

```bash
sudo telad login -hub wss://hub.example.com
# Prompts for token and optional identity
```

The token lands in the system credential store and survives service
restarts. From then on `telad` finds it automatically whenever it connects
to that hub.

**Config file** (recommended for YAML-based deployments). Set the
top-level `token:` field, or a per-machine `token:` override.

**Command line.** Pass `-token <value>`.

**Environment variable.** Set `TELA_TOKEN`.

Token lookup precedence:

1. `-token` flag
2. `TELA_TOKEN` environment variable
3. Per-machine token in the config file
4. Top-level token in the config file
5. Credential store, by hub URL

## Running as an OS Service

For production, install `telad` as a native service so it starts at boot
and restarts on failure:

```bash
telad service install -config telad.yaml
telad service start
```

See [Run Tela as an OS Service](services.md) for the two installation
modes, platform details, and service troubleshooting.

## UDP Relay

If the hub advertises a UDP relay, `telad` sends UDP to the hub's UDP port
for faster transport. If UDP is blocked on the agent's network, sessions
still work over the WebSocket transport; they are just slower.

## Troubleshooting

**The machine never appears in hub status.** Check the hub URL in `hub:`
(DNS and firewall), confirm the hub is reachable from the agent's network,
and if the hub has auth enabled, confirm the token is set and valid.

**`telad` logs "auth_required" or "forbidden".** The token is missing,
rotated away, or does not have permission to register this machine. Use
`tela admin tokens list` to verify the identity exists and
`tela admin access grant` to grant register permission on the machine.

**The machine shows up but connecting to a service fails.** Verify the
service is actually listening on the target host. In the gateway pattern,
verify the agent host can reach `target:<port>`; that hop is the most
common failure.

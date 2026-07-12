# Production Access

Your production infrastructure runs on cloud VMs or bare metal with no
inbound ports open. Today, getting to a machine requires a bastion host, a
VPN, or punching a hole in the firewall. All of those need ongoing
maintenance, invite shared-credential problems, and usually end up granting
broader access than intended ("connect to the VPN, now you can reach
everything").

With Tela, each production VM runs `telad` as an OS service, makes an
outbound connection to a dedicated production hub, and exposes only the
specific ports the team needs. Access is per-machine and per-identity: the
on-call engineer has SSH to the web servers, the DBA has the database port,
and neither has the other's access.

```
Services available:
  localhost:22     → SSH          (web-01)
  localhost:10022  → SSH          (web-02)
  localhost:5432   → port 5432    (db-01)
```

No bastion. No VPN. No shared credentials. When a team member leaves, their
identity is removed from the hub and their access ends immediately; nothing
changes on the production machines.

## Topology

- **A dedicated hub per environment.** Separate hubs for production and
  staging are the simplest possible control boundary: different owner
  tokens, different identity lists, no way to cross by accident. Deploy the
  production hub with TLS and a service manager per
  [Run a Hub on the Public Internet](../howto/hub.md).
- **An endpoint agent on every VM**, installed as an OS service
  ([Run an Agent](../howto/telad.md),
  [Run Tela as an OS Service](../howto/services.md)). Use the gateway
  pattern only for machines that genuinely cannot run `telad`, and treat
  any such gateway host as critical infrastructure: isolated, and
  allowlisted to specific targets and ports.
- **Minimal exposure.** A web server exposes port 22. A database server
  exposes 22 and 5432. Nothing exposes a port range.

## The Access Model

One agent identity per machine, one operator identity per human, grants
that mirror actual responsibilities:

```bash
# Agents: one identity per VM, register permission on its own machine only
tela admin tokens add agent-web01 -hub wss://prod-hub.example.com
tela admin access grant agent-web01 prod-web01 register -hub wss://prod-hub.example.com

# Operators: connect grants per machine, per person
tela admin tokens add alice -hub wss://prod-hub.example.com
tela admin access grant alice prod-web01 connect -hub wss://prod-hub.example.com
tela admin access grant alice prod-db01 connect -hub wss://prod-hub.example.com
```

Two production-grade refinements:

- **Per-service grants.** When a machine exposes several services, a
  connect grant can be narrowed to named services, so the DBA reaches
  PostgreSQL but not SSH:

  ```bash
  tela admin access grant dba prod-db01 connect -services postgres -hub wss://prod-hub.example.com
  ```

- **Rotation and audit.** Rotate any credential you suspect with
  `tela admin rotate <id>` (the old token dies instantly, permissions
  survive), and review recent session events with the hub's history
  (`/api/history`, the hub console, or TelaVisor's History view).

## The Operator Workflow

Operators store their token once (`tela login wss://prod-hub.example.com`),
keep a profile per environment with pinned `local:` ports, and connect on
demand:

```bash
tela connect -profile prod
ssh -p 22 localhost          # web-01, per the profile's port layout
psql -h localhost -p 5432 -U postgres
```

Pinned local ports matter more here than anywhere else: muscle memory and
shell history should never depend on which fallback port a machine happened
to get.

## Pitfalls Specific to This Scenario

- Tela moves the network perimeter; it does not replace host hardening.
  Patching, strong SSH authentication, and TLS on the database itself all
  still apply. The hop from `telad` to the local service is plain TCP, so
  services that carry credentials should bring their own encryption.
- Verify egress: production VMs with strict outbound firewalls need to
  allow the WebSocket connection to the hub (and, optionally, UDP to the
  hub's relay port).
- Resist the temptation to grant `'*'` connect to operators "for now."
  Wildcards are for the personal-cloud scenario; in production the
  per-machine grant list is the audit trail of who was supposed to reach
  what.

# Appendix F: Glossary

## Agent

A running instance of `telad` that registers one or more machines with a
hub and forwards TCP connections from clients to local services. An agent
initiates an outbound WebSocket connection to the hub and keeps it open; no
inbound port is needed on the agent's host.

Two deployment patterns:

- **Endpoint agent**: `telad` runs on the same host as the services it
  exposes. Each machine entry points to `127.0.0.1`.
- **Gateway agent** (bridge): `telad` runs on a separate host that can
  reach internal targets. Each machine entry points to a different IP on
  the local network, letting one agent represent many machines.

See also: [Machine](#machine), [Hub](#hub).

---

## Channel

The release track a Tela binary follows for self-updates. Three channels
exist:

| Channel | Description |
|---------|-------------|
| `dev` | Built from every commit to `main`. Most current, least tested. |
| `beta` | Promoted from `dev` on demand. Stabilized builds for early adopters. |
| `stable` | Promoted from `beta`. Recommended for production. |

Each binary has its own channel setting. The `tela` client and TelaVisor
share the setting in `credentials.yaml` (`update.channel`). `telad` and
`telahubd` each have it in their own YAML config. Operators can also define
custom channels served from their own infrastructure.

See [Release Process](ops/release-process.md).

---

## Connect Permission

A machine permission that allows a token to open a client session
(`tela connect`) to a specific machine. Multiple tokens may hold connect
permission on the same machine, and a connect grant can optionally be
narrowed to specific named services. Owner and admin role tokens implicitly
have connect access to all machines without an explicit grant.

See also: [Machine Permission](#machine-permission), [Role](#role).

---

## Credential Store

A per-user file (`credentials.yaml`) that stores hub tokens so you do not
need to pass `-token` on every command. Written by `tela login`; read
automatically by `tela` and TelaVisor. Stored at:

- Windows: `%APPDATA%\tela\credentials.yaml`
- Linux/macOS: `~/.tela/credentials.yaml`

A system-level store (`/etc/tela/credentials.yaml` on Unix,
`%ProgramData%\Tela\credentials.yaml` on Windows) serves `telad` running as
an OS service.

See also: [Token](#token).

---

## Fabric

The interconnection layer that lets endpoints reach each other without each
endpoint knowing the topology. Tela is a fabric in the leaf-spine sense:
the hub is the spine, agents and clients are the leaves, and most traffic
travels client to hub to agent. Direct peer-to-peer connections are
negotiated when the network allows, but they are an optimization rather
than the default path.

Tela is not a routed mesh in the Tailscale, Nebula, or ZeroTier sense.

---

## File Share

An optional feature of `telad` that exposes one or more sandboxed
directories on the agent host for file transfer over the WireGuard tunnel.
Disabled by default. Configured per machine under the `shares:` list in
`telad.yaml`.

See [File Sharing](guide/file-sharing.md).

---

## Fleet

A collection of groups. A fleet may contain a single group (a simple
single-hub deployment) or many groups across multiple sites, environments,
or customers. The fleet is the unit of reasoning for operators who manage
infrastructure at scale. Portals support fleet-scale deployments by listing
multiple hubs in a single directory, letting clients resolve any hub by
name without knowing its URL in advance.

See also: [Group](#group), [Portal](#portal).

---

## Group

One hub (`telahubd`) together with all the agents (`telad`) connected to
it. A group is the basic operational unit of a Tela deployment. The analogy
is a carrier battle group: the hub is the carrier, and the agents are the
support vessels operating under it. A single-hub deployment is one group.
A larger deployment, where separate hubs serve different environments or
customer sites, is a fleet of groups.

See also: [Hub](#hub), [Fleet](#fleet).

---

## Hub

A running instance of `telahubd`. The hub is the coordination point for the
fabric: it accepts WebSocket connections from agents and clients, relays
encrypted WireGuard traffic between them, enforces access control, and
serves the web console and admin API.

The hub never decrypts tunnel payloads. WireGuard encryption is end-to-end
between agent and client; the hub sees only ciphertext.

See also: [Agent](#agent), [Zero-Knowledge Relay](#zero-knowledge-relay).

---

## Hub Alias

A short name mapped to a hub WebSocket URL in `hubs.yaml` (for local
fallback) or via a portal remote (for network-resolved lookup). Aliases let
you write `-hub work` instead of `-hub wss://hub.example.com`. Alias lookup
is case-sensitive.

See [Appendix B: Configuration File Reference](guide/configuration.md).

---

## Identity

The human-readable name attached to a token (for example, `alice`,
`prod-web01-agent`, `ci-bot`). Identity names appear in the hub console,
CLI output, and access listings. The name has no security function; the
token value is the credential. You can rename an identity with
`tela admin access rename` without affecting the underlying token or
permissions.

See also: [Token](#token).

---

## Machine

A logical endpoint registered by an agent with a hub. A machine has a name
(the ID used in `-machine` flags and access grants), an optional display
name, and a list of exposed services. One `telad` process can register
multiple machines. A machine is what operators connect to; it is not
necessarily a physical host.

See also: [Service](#service), [Agent](#agent).

---

## Machine Permission

A per-machine authorization entry that controls what a token can do on a
specific machine. Three permissions exist:

| Permission | What It Allows |
|-----------|---------------|
| `register` | Register an agent for this machine. One token holds this per machine. |
| `connect` | Open a client session to this machine. Multiple tokens may hold this. |
| `manage` | Send management commands (config, logs, restart, update) to this machine's agent. Multiple tokens may hold this. |

Permissions are granted with `tela admin access grant`. A wildcard machine
ID of `*` applies connect and manage to all machines; register is always
granted per machine. Owner and admin role tokens bypass all permission
checks.

See [Appendix C: Access Model](architecture/access-model.md).

---

## Manage Permission

A machine permission that allows a token to send management commands to a
machine's agent through the hub: read and write config, fetch logs, restart
the agent, trigger self-update. Owner and admin role tokens have implicit
manage access to all machines.

See also: [Machine Permission](#machine-permission).

---

## Open Mode

The state the hub operates in when no tokens are configured. In open mode,
every API call is permitted without authentication. The hub auto-generates
an owner token on first startup specifically to prevent accidental open
mode. Open mode requires deliberate configuration (removing all tokens from
the config), and the hub logs a warning when running in it.

---

## Pair Code

A short-lived, single-use code generated by the hub
(`tela admin pair-code`) that lets a new agent or client authenticate
without a pre-shared token. When the pair code is redeemed, the hub issues
a permanent token and the redeeming side stores it. Codes expire after 10
minutes by default; the generating administrator can set any expiry up to
7 days.

See [Credentials and Pairing](guide/credentials.md).

---

## Portal

A web service that maintains a directory of hubs, usually with a dashboard
and identity layered on top. The `tela` client resolves hub aliases through
a portal registered with `tela remote add`. The portal protocol is a
documented wire contract; `telaportal` is Tela's own single-user
implementation, and Awan Saya is the multi-organization reference
implementation.

See [Hub Directories and Portals](guide/directories.md) and
[Appendix D: Portal Protocol](architecture/portal-protocol.md).

---

## Register Permission

A machine permission that allows a token to register an agent for a
specific machine. One token holds the register permission per machine at a
time; granting it to another identity replaces the previous holder. Unlike
connect and manage, register does not cascade from the wildcard `*` entry.

See also: [Machine Permission](#machine-permission).

---

## Role

A label on a token that controls hub-level API access. Four roles exist:

| Role | Hub-Level Access | Machine-Level Access |
|------|-----------------|---------------------|
| `owner` | Full access including owner management | Implicit access to all machines |
| `admin` | Full access except owner-only operations | Implicit access to all machines |
| `user` | No admin API access | Only what machine permissions explicitly grant |
| `viewer` | Read-only: `/api/status`, `/api/history` | None |

The default role when none is specified is `user`. Owner and admin can be
set at creation time with the `-role` flag; viewer is assigned by changing
an existing identity's role.

See [Appendix C: Access Model](architecture/access-model.md).

---

## Service

A TCP port exposed by a machine. A service has a port number, an optional
name (for example, `SSH`, `postgres`), and an optional protocol label. When
a client connects to a machine, the tunnel maps each service port to a
local port on the client's loopback interface.

See also: [Machine](#machine).

---

## Session

A single client connection to a machine. Each session gets a /24 subnet on
the `10.77.0.0/16` range: the agent side is `10.77.{idx}.1` and the client
side is `10.77.{idx}.2`. These addresses exist only inside the userspace
network stack. The session index is monotonically incrementing per machine,
up to 254 concurrent sessions.

---

## TelaVisor

The desktop graphical user interface (GUI) for Tela. Built with Wails v2
(Go backend, vanilla JavaScript frontend). Provides connection management,
profiles, a file browser, and full hub, agent, and access administration in
a native window. Released builds are published for Windows (installer),
Linux (`.deb`, `.rpm`, and bare binary), and macOS (`.app` bundle).

See [TelaVisor](guide/telavisor.md).

---

## Token

A 64-character hexadecimal string (32 random bytes) that serves as the
authentication credential for a hub. Tokens are shown in full only once, at
creation or rotation time. The hub stores the full value for comparison;
the admin API returns only an 8-character preview afterward.

Token lookup order for CLI commands: `-token` flag, then the `TELA_TOKEN`
environment variable, then the credential store.

See also: [Identity](#identity), [Role](#role),
[Credential Store](#credential-store).

---

## UDP Relay

The middle transport tier for WireGuard traffic. The hub listens on a UDP
port (default 41820) and relays WireGuard packets between agent and client.
Faster than the WebSocket relay; used when a direct peer-to-peer path
cannot be negotiated but UDP to the hub is open. If UDP is blocked, the
fabric falls back automatically to the WebSocket relay.

See also: [WebSocket Relay](#websocket-relay).

---

## Upstream

A TCP forwarding rule inside `telad` that intercepts outbound dependency
calls from services on the agent machine and routes them to a configurable
target. The service connects to a local port (`localhost:5432`); `telad`
listens there and forwards to wherever the dependency actually lives.
Declared per machine under `upstreams:` in `telad.yaml`.

See [Upstreams](guide/upstreams.md).

---

## WebSocket Relay

The always-available transport for WireGuard traffic. The hub relays
encrypted WireGuard packets between agent and client over the same
persistent WebSocket connection used for signaling. Works through HTTP
proxies and corporate firewalls that block UDP. Slower than the UDP relay
for high-throughput workloads.

See also: [UDP Relay](#udp-relay).

---

## Zero-Knowledge Relay

The property that the hub relays WireGuard-encrypted traffic without being
able to decrypt it. WireGuard keys are held only by agents and clients. The
hub sees ciphertext. This means a compromised hub cannot read tunnel
payloads, only disrupt connectivity.

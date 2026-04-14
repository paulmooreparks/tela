# Appendix F: Glossary

## Agent

A running instance of `telad` that registers one or more machines with a hub and
forwards TCP connections from clients to local services. An agent initiates an
outbound WebSocket connection to the hub and keeps it open; no inbound port is
needed on the agent's host.

Two deployment patterns:

- **Endpoint agent**: `telad` runs on the same host as the services it exposes.
  Each machine entry points to `127.0.0.1`.
- **Gateway agent** (bridge): `telad` runs on a separate host that can reach
  internal targets. Each machine entry points to a different IP on the local
  network, letting one agent represent many machines.

See also: [Machine](#machine), [Hub](#hub).

---

## Channel

The release track a Tela binary follows for self-updates. Three channels exist:

| Channel | Description |
|---------|-------------|
| `dev` | Built from every commit to `main`. Most current, least tested. |
| `beta` | Promoted from `dev` on demand. Stabilized builds for early adopters. |
| `stable` | Promoted from `beta`. Recommended for production. |

Each binary has its own channel setting. The `tela` client and TelaVisor share
the setting in `credentials.yaml` (`update.channel`). `telad` and `telahubd`
each have it in their own YAML config.

See [Release process](../ops/release-process.md).

---

## Connect permission

A machine permission that allows a token to open a client session (`tela connect`)
to a specific machine. Multiple tokens may hold connect permission on the same
machine. Owner and admin role tokens implicitly have connect access to all
machines without an explicit grant.

See also: [Machine permission](#machine-permission), [Role](#role).

---

## Credential store

A per-user file (`credentials.yaml`) that stores hub tokens so you do not need
to pass `-token` on every command. Written by `tela login`; read automatically
by `tela` and TelaVisor. Stored at:

- Windows: `%APPDATA%\tela\credentials.yaml`
- Linux/macOS: `~/.tela/credentials.yaml`

Token lookup order: `-token` flag > `TELA_TOKEN` environment variable >
credential store (user, then system).

---

## Fabric

The interconnection layer that lets endpoints reach each other without each
endpoint knowing the topology. Tela is a fabric in the leaf-spine sense: the
hub is the spine, agents and clients are the leaves, and most traffic travels
client to hub to agent. Direct peer-to-peer connections are negotiated when
the network allows, but they are an optimization rather than the default path.

Tela is not a routed mesh in the Tailscale, Nebula, or ZeroTier sense.

---

## File share

An optional feature of `telad` that exposes a sandboxed directory on the
agent host for file transfer over the WireGuard tunnel. Disabled by default.
Configured per machine under the `fileShare:` block in `telad.yaml`.

See [File sharing](../howto/file-sharing.md).

---

## Hub

A running instance of `telahubd`. The hub is the coordination point for the
fabric: it accepts WebSocket connections from agents and clients, relays
encrypted WireGuard traffic between them, enforces access control, and serves
the web console and admin API.

The hub never decrypts tunnel payloads. WireGuard encryption is end-to-end
between agent and client; the hub sees only ciphertext.

See also: [Agent](#agent), [Zero-knowledge relay](#zero-knowledge-relay).

---

## Hub alias

A short name mapped to a hub WebSocket URL in `hubs.yaml` (for local fallback)
or via a portal remote (for network-resolved lookup). Aliases let you write
`-hub owlsnest` instead of `-hub wss://tela.awansaya.net`. Alias lookup is
case-sensitive.

See [Configuration](../guide/configuration.md).

---

## Identity

The human-readable name attached to a token (for example, `alice`,
`prod-web01-agent`, `ci-bot`). Identity names appear in the hub console, CLI
output, and access listings. The name has no security function; the token value
is the credential. You can rename an identity with `tela admin access rename`
without affecting the underlying token or permissions.

See also: [Token](#token).

---

## Machine

A logical endpoint registered by an agent with a hub. A machine has a name
(the ID used in `-machine` flags and access grants), an optional display name,
and a list of exposed services. One `telad` process can register multiple
machines. A machine is what operators connect to; it is not necessarily a
physical host.

See also: [Service](#service), [Agent](#agent).

---

## Machine permission

A per-machine authorization entry that controls what a token can do on a
specific machine. Three permissions exist:

| Permission | What it allows |
|-----------|---------------|
| `register` | Register an agent for this machine. Only one token may hold this per machine. |
| `connect` | Open a client session to this machine. Multiple tokens may hold this. |
| `manage` | Send management commands (config, logs, restart) to this machine's agent. Multiple tokens may hold this. |

Permissions are granted with `tela admin access grant` and can use the wildcard
`*` to apply to all machines. Owner and admin role tokens bypass all permission
checks.

See [Access model](access-model.md).

---

## Manage permission

A machine permission that allows a token to send management commands to a
machine's agent through the hub: read and write config, stream logs, restart
the agent. Owner and admin role tokens have implicit manage access to all
machines.

See also: [Machine permission](#machine-permission).

---

## Open mode

The state the hub operates in when no tokens are configured. In open mode,
every API call is permitted without authentication. The hub auto-generates an
owner token on first startup specifically to prevent accidental open mode. Open
mode requires deliberate configuration (removing all tokens from the config).

---

## Pair code

A short-lived, single-use code generated by the hub (`tela admin pair-code`)
that lets a new agent or client authenticate without a pre-shared token. When
the pair code is used, the hub issues a permanent token and saves it. Pair codes
expire; their default lifetime is configurable.

See [Hub administration](admin.md).

---

## Portal

A web service that maintains a directory of hubs. The `tela` client can resolve
hub aliases through a portal using `tela remote add`. The portal protocol is a
documented wire contract; Awan Saya is the reference implementation.

See [Portal protocol](portal-protocol.md).

---

## Register permission

A machine permission that allows a token to register an agent for a specific
machine. Only one token may hold the register permission per machine at a time.
If a second token tries to register the same machine name with a different
credential, the hub rejects it.

See also: [Machine permission](#machine-permission).

---

## Role

A label on a token that controls hub-level API access. Four roles exist:

| Role | Hub-level access | Machine-level access |
|------|-----------------|---------------------|
| `owner` | Full access including owner management | Implicit access to all machines |
| `admin` | Full access except owner-only operations | Implicit access to all machines |
| `user` | No admin API access | Only what machine permissions explicitly grant |
| `viewer` | Read-only: `/api/status`, `/api/history` | None |

The default role when none is specified is `user`. Roles are set at token
creation time with the `-role` flag.

See [Access model](access-model.md).

---

## Service

A TCP port exposed by a machine. A service has a port number, an optional
name (for example, `SSH`, `Postgres`), and an optional protocol label.
When a client connects to a machine, the tunnel maps each service port to a
local address on the client's loopback interface.

See also: [Machine](#machine).

---

## Session

A single client connection to a machine. Each session gets a /24 subnet on
the `10.77.0.0/16` range: the agent side is `10.77.{idx}.1` and the client
side is `10.77.{idx}.2`. Session index is monotonically incrementing per
machine, up to 254 concurrent sessions.

---

## TelaVisor

The desktop graphical user interface (GUI) for Tela. Built with Wails v2 (Go
backend, vanilla JavaScript frontend). Provides hub browsing, machine listing,
connection management, agent management, and hub administration in a native
window. Available on Windows; macOS and Linux builds require building from
source.

See [TelaVisor](telavisor.md).

---

## Token

A 64-character hexadecimal string (32 random bytes) that serves as the
authentication credential for a hub. Tokens are shown in full only once, at
creation or rotation time. The hub stores the full value for comparison;
the admin API returns only an 8-character preview afterward.

Token lookup order for CLI commands: `-token` flag > `TELA_TOKEN` environment
variable > credential store.

See also: [Identity](#identity), [Role](#role), [Credential store](#credential-store).

---

## UDP relay

The fallback transport for WireGuard traffic when a direct peer-to-peer path
cannot be negotiated. The hub listens on a UDP port (default 41820) and relays
WireGuard packets between agent and client. If UDP is blocked, `tela` falls
back automatically to WebSocket relay.

See also: [WebSocket relay](#websocket-relay).

---

## Upstream

A named hub alias stored in a `telad` config, enabling `telad` to register the
same machines with more than one hub. Upstreams let one agent be reachable
through multiple independent hubs without running multiple `telad` processes.

See [Upstreams](upstreams.md).

---

## WebSocket relay

The primary transport for WireGuard traffic. The hub relays encrypted WireGuard
packets between agent and client over the same persistent WebSocket connection
used for signaling. Works through HTTP proxies and corporate firewalls that
block UDP. Slower than UDP relay for high-throughput workloads.

See also: [UDP relay](#udp-relay).

---

## Zero-knowledge relay

The property that the hub relays WireGuard-encrypted traffic without being able
to decrypt it. WireGuard keys are held only by agents and clients. The hub sees
ciphertext. This means a compromised hub cannot read tunnel payloads, only
disrupt connectivity.

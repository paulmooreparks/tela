# Introduction

Tela is a connectivity fabric. It is a small set of programs that lets one
machine reach a TCP service on another machine through an encrypted tunnel,
without either side opening an inbound port, installing a VPN client, loading
a kernel driver, or running anything as root or Administrator. Remote desktop
is the use case that I initially created it for, but remote desktop is just one
application that runs on the fabric, not the point of it.

The point of the fabric is that the same three pieces scale from a single
laptop reaching a single home server all the way up to a fleet of machines
managed by a team, and the scaling does not require switching tools or
rearchitecting anything. The pieces are: an agent (`telad`) that runs on the
machine you want to reach, a hub (`telahubd`) that brokers connections, and
a client (`tela`) that runs on the machine you want to reach from. Each is a
single static binary with no runtime dependencies. They run on Windows,
Linux, and macOS.

## What makes the fabric different

A handful of properties define the design and run through every chapter of
this book.

### Outbound-only on both ends

The agent and the client both make outbound
connections to the hub. Neither needs an inbound firewall rule, port
forwarding, dynamic DNS, or a static IP. The hub is the only component that
needs a public address, and it only needs one inbound TCP port.

### No kernel driver, no admin rights

Tela runs WireGuard entirely in
userspace through gVisor's network stack. There is no TUN device, no kernel
module, and no Administrator or root requirement on either the agent or the
client. This is the property that lets Tela work on a managed corporate
laptop where you cannot install a VPN, and on a locked-down server where you
cannot load drivers.

### The hub is a blind relay

All encryption is end to end between the agent
and the client. The hub forwards opaque WireGuard ciphertext and cannot read
session contents. A compromised hub leaks metadata, not data.

### Any TCP service

Tela tunnels arbitrary TCP. Secure Shell (SSH), Remote
Desktop Protocol (RDP), HTTP, PostgreSQL, Server Message Block (SMB), Virtual
Network Computing (VNC), or anything else that runs over TCP travels through
the same tunnel without the hub having to understand the protocol.

### Three transports, automatic fallback

The fabric tries direct
peer-to-peer first, falls back to a User Datagram Protocol (UDP) relay
through the hub, and falls back again to a WebSocket relay over Transport
Layer Security (TLS). Whichever transport is active, the WireGuard payload
is the same and the hub still cannot decrypt it.

### One binary per role, no runtime dependencies

`tela`, `telad`, and
`telahubd` are each a single executable. There is no installer, no package
to register with the operating system unless you choose to run them as
services, and no shared library to deploy alongside them.

## What grows on top of the fabric

Connectivity is the substrate. Everything else in this book is something
that the project has built on top of it, in the same repository, with the
same release process.

- **Token-based access control** with four roles (owner, admin, user,
  viewer) and per-machine permissions for register, connect, and manage.
- **One-time pairing codes** that replace 64-character hex tokens for
  onboarding new users and new agents.
- **Remote administration** of agents and hubs through the same wire as
  data traffic, so you do not need shell access to the host running an agent
  or a hub to manage it.
- **File sharing** through a sandboxed directory on each agent, with
  upload, download, rename, move, and delete operations available from the
  command line, the desktop client, or a Web Distributed Authoring and
  Versioning (WebDAV) mount.
- **A path-based gateway** built into the agent that exposes multiple local
  services through a single tunnel port, replacing the need for a separate
  reverse proxy in many deployments.
- **Upstream rerouting** that lets a service's outbound dependency calls be
  redirected to a different machine or environment by editing a configuration
  file rather than the code.
- **TelaVisor**, a desktop graphical interface that wraps the client and
  exposes the management features without requiring terminal access.
- **Self-update through release channels** (dev, beta, stable) with signed
  manifests, so every binary can update itself in place without an external
  package manager.
- **A hub directory protocol** that lets a portal list and discover hubs,
  with [Awan Saya](https://awansaya.net) as the reference implementation.

These features are not bolted on. They share the protocol, the access model,
the configuration system, and the release pipeline of the fabric itself.

## How far it scales

The same three binaries cover a wide range of deployments. The shape of the
deployment changes; the protocol does not.

| Tier | What it looks like |
|------|---------------------|
| **Solo remote access** | One agent on a home machine, one hub on a small virtual server, one client on a laptop. A few minutes from download to first connection. |
| **Personal cloud** | Several agents across home and office, file sharing enabled, a desktop client for non-terminal users, optional WebDAV mount. |
| **Team cloud** | Named identities, per-machine permissions, pairing codes for onboarding, remote admin from the desktop client, audit history on the hub. |
| **Fleet** | Multiple hubs registered with a portal, identities and permissions managed centrally, agents updating themselves through release channels, lifecycle controls from the portal. |

The honest limit of this book and the project as it stands today is the
word *mesh* in the network-engineering sense. The protocol is mesh-capable,
and clients can negotiate direct peer-to-peer connections when the network
allows it, but Tela is not a routed mesh in the way that some competitors
are. Most traffic still travels client to hub to agent. If your design
requires agent-to-agent routing without the hub on the path, that is a
property to evaluate carefully against the chapters in the
[Architecture](architecture/design.md) section.

## How this book is organized

Tela is the substrate. This book documents the substrate first and the
features built on top of it second.

- The [Getting Started](getting-started/what-tela-is.md) section is a fast
  path from "I have never heard of Tela" to "I have a working tunnel."
- The [User Guide](guide/three-binaries.md) section is the reference for the
  three binaries, the configuration files, and the desktop and portal
  clients.
- The [How-to Guides](howto/channels.md) section is a set of focused
  walkthroughs for the most common operational tasks.
- The [Use Cases](use-cases/personal-cloud.md) section walks through six
  concrete deployment scenarios with the access model and the deployment
  pattern for each.
- The [Architecture](architecture/design.md) section is the design and
  protocol documentation for readers who want to understand what is
  happening under the hood, or who are evaluating Tela for a use case the
  book has not anticipated.
- The [Operations](ops/release-process.md) section covers the release
  process, the design-to-implementation status matrix, and the roadmap to
  1.0.

The book is generated from the Markdown files in the
[tela](https://github.com/paulmooreparks/tela) repository on every push to
`main`, so it never drifts from the code that ships.

## Conventions

- The three binaries are `tela` (client), `telad` (agent or daemon), and
  `telahubd` (hub or relay).
- "TelaVisor" is the desktop graphical interface built on top of `tela`.
- "Awan Saya" is the multi-organization web portal that talks to multiple
  hubs.
- Code, file paths, command-line flags, and configuration keys are in
  `monospace`.
- Mermaid diagrams render natively in the HTML output.

## License

Apache License 2.0. See the
[LICENSE](https://github.com/paulmooreparks/tela/blob/main/LICENSE) file in
the repository.

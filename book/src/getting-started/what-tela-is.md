# What Tela is

As mentioned in the introduction, Tela is a connectivity fabric. The basic operational unit in Tela is a *group*: one hub and all the agents connected to it. A collection of groups is a *fleet*.

## What it solves

The classic remote-access problem looks like this. You have a machine
somewhere: a workstation, a server, a Supervisory Control and Data
Acquisition (SCADA) gateway, a Raspberry Pi. You want to reach a service on
it. Secure Shell (SSH), Remote Desktop Protocol (RDP), PostgreSQL, an HTTP
application programming interface (API), a Server Message Block (SMB)
share, anything that speaks TCP. You are not on the same network. There is
a firewall in the way. You don't control the firewall. You can't open
inbound ports. You don't want a vendor-locked cloud service. You don't want
a kernel-mode VPN that requires admin rights to install.

Most existing solutions force a tradeoff:

| Solution | The tax |
|----------|---------|
| Traditional VPN | Admin to install on the client, inbound firewall rules on the server, often a kernel driver. |
| SSH port forwarding | Requires SSH access to a publicly reachable jump host. |
| Vendor cloud services (TeamViewer, AnyDesk) | Opaque agent, per-seat pricing, lock-in. |
| Kernel-mode WireGuard | `CAP_NET_ADMIN` or root, plus a TUN device and inbound firewall rules. |
| Mesh VPN (Tailscale, Nebula, ZeroTier) | TUN device, vendor agent, often blocked on managed corporate endpoints. |

Tela takes the security guarantees of WireGuard and removes the deployment
friction.

## What makes Tela different

A handful of properties define the design and run through every chapter of
this book.

### Outbound-only on both ends

The agent and the client both make outbound connections to the hub.
Neither needs an inbound firewall rule, port forwarding, dynamic Domain
Name System (DNS), or a static internet protocol (IP) address. The hub is
the only component that needs a public address, and it only needs one
inbound TCP port.

### No kernel driver, no admin rights

Tela runs WireGuard entirely in userspace through gVisor's network stack.
There is no TUN device, no kernel module, and no Administrator or root
requirement on either the agent or the client. This is the property that
lets Tela work on a managed corporate laptop where you cannot install a
VPN, and on a locked-down server where you cannot load drivers.

### The hub is a blind relay

All encryption is end to end between the agent and the client. The hub
forwards opaque WireGuard ciphertext and cannot read session contents. A
compromised hub leaks metadata, not data.

### Any TCP service

Tela tunnels arbitrary TCP. SSH, RDP, HTTP, PostgreSQL, SMB, Virtual
Network Computing (VNC), or anything else that runs over TCP travels
through the same tunnel without the hub having to understand the protocol.

### Three transports, automatic fallback

The fabric tries direct peer-to-peer first, falls back to a User Datagram
Protocol (UDP) relay through the hub, and falls back again to a WebSocket
relay over Transport Layer Security (TLS). Whichever transport is active,
the WireGuard payload is the same and the hub still cannot decrypt it.

### One binary per role, no runtime dependencies

`tela`, `telad`, and `telahubd` are each a single executable. There is no
installer, no package to register with the operating system unless you
choose to run them as services, and no shared library to deploy alongside
them.

## What grows on top of the fabric

Connectivity is the substrate. Everything else in this book is something
the project has built on top of it, in the same repository, with the same
release process.

- **Token-based access control** with four roles (owner, admin, user,
  viewer) and per-machine permissions for register, connect, and manage.
- **One-time pairing codes** that replace 64-character hex tokens for
  onboarding new users and new agents.
- **Remote administration** of agents and hubs through the same wire as
  data traffic, so you do not need shell access to the host running an
  agent or a hub to manage it.
- **File sharing** through a sandboxed directory on each agent, with
  upload, download, rename, move, and delete operations available from the
  command line, the desktop client, or a Web Distributed Authoring and
  Versioning (WebDAV) mount.
- **Gateways**, a family of forwarding primitives that Tela uses at several
  layers of the stack: a path-based HTTP reverse proxy in the agent for
  routing one tunnel port to several local services, a bridge-mode agent
  for fronting services on other LAN-reachable machines, outbound
  dependency rerouting for service-to-service calls, and the hub itself as
  a relay gateway for opaque WireGuard ciphertext between a client and an
  agent. They share one rule: forward without inspecting beyond what the
  layer requires. The 1.0 roadmap extends the family with a multi-hop
  relay gateway that bridges sessions across more than one hub.
- **TelaVisor**, a desktop graphical interface that wraps the client and
  exposes the management features without requiring terminal access.
- **Self-update through release channels** (dev, beta, stable) with signed
  manifests, so every binary can update itself in place without an external
  package manager.
- **A hub directory protocol** that lets a portal list and discover hubs.

These features are not bolted on. They share the protocol, the access
model, the configuration system, and the release pipeline of the fabric
itself.

## What it is not

The word *fabric* invites projection, so a few explicit non-goals are worth
naming up front.

- **Not a mesh VPN.** There is no overlay network with auto-discovery and
  no agent-to-agent routing as a first-class feature. You connect to one
  machine at a time. See *A note on the word fabric* below.
- **Not a multi-tenant SaaS.** You run the hub yourself. A portal can
  aggregate multiple hubs, but each hub still runs under its own operator's
  control.
- **Not a transport for arbitrary IP traffic.** It tunnels TCP services,
  one machine at a time. No UDP services, no Internet Control Message
  Protocol (ICMP), no full-network IP routing.
- **Not a replacement for SSH.** It is a way to *get* SSH (or RDP, or
  PostgreSQL) onto your laptop without configuring port forwarding or VPNs.

The [Topology and addressing](../howto/networking.md#topology-and-addressing)
section in the Networking chapter answers specific questions about IP
addressing, clash avoidance, discoverability, ICMP, agent-to-agent routing,
and session limits.

## Why three binaries

The split is deliberate.

- **`telahubd`** is the only binary that needs to be publicly reachable.
  Everything about its job is "be the meeting point." It cannot read what
  flows through it.
- **`telad`** lives on the machine you want to reach. Its job is to
  register with a hub and unwrap the encrypted tunnel into a local TCP
  connection.
- **`tela`** lives on the machine you connect from. Its job is to dial a
  hub, set up the encrypted tunnel, and bind a local TCP listener that
  forwards through the tunnel.

This is the WireGuard model expressed as three small daemons. The agent
and the client are peers. The hub is a router with no keys. The roles map
directly to the operational reality: the agent runs as a service on a
machine you own and rarely touch, the client runs on demand on a laptop
you carry around, the hub runs on a small virtual server with a public
address. They have different lifecycles, different threat models, and
different update cadences. Bundling them would force shared concerns where
there are none.

## A note on the word *fabric*

Tela is a fabric in the leaf-spine sense, not a mesh in the Tailscale
sense. The hub is the spine. The agents and clients are the leaves. Most
traffic travels client to hub to agent, the same way a leaf-spine data
center fabric routes most traffic leaf to spine to leaf. Clients and
agents can negotiate direct peer-to-peer connections when the network
allows it, but those connections are an optimization, not the default, and
they do not turn Tela into a routed mesh in the way that Tailscale, Nebula,
or ZeroTier are. If your design requires agent-to-agent routing without
the hub on the data path as a first-class feature, that is a property to
evaluate carefully against the chapters in the
[Design Rationale](../architecture/design.md) section. The
[glossary](../glossary.md) has the longer history of the word and the prior
art that justifies it.

---

For the architectural details, see [Why a connectivity fabric](../architecture/design.md).
For installation, see [Installation](installation.md).

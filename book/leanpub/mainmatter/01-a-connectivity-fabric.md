{mainmatter}

# A Connectivity Fabric

This chapter is the *why*.

Most people meet Tela the same way: they have a machine somewhere, they want
to reach a service on it from somewhere else, and every existing tool taxes
them for the privilege. The tax has many forms. A virtual private network
(VPN) wants administrator rights and a kernel driver on both ends. A reverse
tunnel wants a paid account and a per-tunnel pricing tier. A bastion host
wants a static internet protocol (IP) address and an inbound firewall rule.
A "zero trust" platform wants a TUN device and a vendor agent. A cloud
provider wants a credit card and a region selection. Each of these is a
reasonable design for the problem the vendor was actually solving, which is
not always the problem the user has.

The problem most users have is small and stubborn. There is a machine.
There is a service on it. There is another machine. The first machine is
behind a router that does not forward ports, or behind a corporate proxy
that only allows outbound HTTPS, or behind a hotel network that mangles
everything it does not understand. The second machine is a managed laptop
that will not let the user install kernel drivers. The user does not want
to rent compute, sign a contract, or learn a new orchestration tool. The
user wants the service.

## What changed

Until recently, secure tunnels meant kernel-level networking. WireGuard was
a kernel module. OpenVPN was a kernel-level virtual interface. Even the
"userspace" alternatives needed at least a TUN device, which on most
operating systems requires root or Administrator. A normal user on a normal
locked-down laptop could not install a tunnel without help from someone with
elevated rights. That ruled out an enormous fraction of the people who
needed one.

Two things changed at roughly the same time. First, the WireGuard project
released a pure-Go reference implementation that could run entirely in
userspace if it had a network stack to plug into. Second, Google's gVisor
project shipped a userspace transmission control protocol / internet
protocol (TCP/IP) stack, originally for sandboxing containers, that exposed
exactly the right interface for WireGuard to plug into. The combination
meant that a single binary, with no special privileges, could speak the
WireGuard protocol fluently. There was no longer a kernel-level barrier
between an ordinary user and an end-to-end encrypted tunnel.

Tela is the project that took that combination and built the management
plane around it.

## Three small daemons, one role each

Tela could have been one big binary that wears all the hats. It is not, on
purpose. It is three small binaries with one job each:

- `tela` is the **client**. It runs on the machine you are reaching from.
  It connects outbound to a hub, negotiates a tunnel to a remote machine,
  and binds a local TCP port that your normal tools (`ssh`, `mstsc`, `psql`)
  connect to as if the remote service were on `localhost`.
- `telad` is the **agent**. It runs on the machine you are reaching to.
  It connects outbound to the same hub, registers a named machine and a set
  of local services, and accepts encrypted tunnel sessions from authorized
  clients.
- `telahubd` is the **hub**. It is the only piece that needs an inbound
  port. It pairs clients with agents, relays opaque WireGuard ciphertext
  between them, and serves a small administrative interface.

Each binary is a single static executable with no runtime dependencies. The
split is not architectural elegance for its own sake. It maps directly to
the operational reality. The agent runs as a service on a machine you own
and rarely touch. The client runs on demand on a laptop you carry around.
The hub runs on a small virtual server that has a public address. They have
different lifecycles, different threat models, and different update
cadences. Bundling them would force shared concerns where there are none.

## What grows on top

The fabric is not the whole story. The same repository, the same release
process, and the same protocol carry a set of features that turn "I have a
tunnel" into "I am running this for myself, my team, or my organization."
Token-based access control with named identities and per-machine
permissions. One-time pairing codes for onboarding without copying long hex
strings. Remote administration of agents and hubs through the same wire as
data traffic. Sandboxed file sharing with a Web Distributed Authoring and
Versioning (WebDAV) mount. A path-based gateway that exposes multiple
services through a single tunnel port. Self-update with signed release
channels. A desktop graphical interface and a multi-organization web
portal.

These features are not bolted on. They share the protocol, the access
model, and the configuration system of the fabric itself. Read together,
they look like the management plane of a managed cloud service, except
the compute is yours, the network is yours, and the encryption keys never
leave your machines.

## How to read this book

The book is organized in three loose movements.

The first is **getting up and running**: the architecture in one paragraph,
installing the binaries, making your first connection, and understanding
what just happened. If you only read this part, you should be able to use
Tela to do real work.

The second is **the operator's guide**: everything about running Tela in
production. The hub on the public internet, the agent on managed servers,
the access control model, file sharing, gateways, the desktop client, the
multi-organization portal, release channels, and the day-to-day operations
of a fleet. If you read this part, you should be able to deploy Tela for a
team or an organization.

The third is the **architecture and deep dives**: the protocol
specification, the security model, the deployment recipes, the
troubleshooting playbook, and the roadmap. If you read this part, you
should be able to debug, extend, or fork Tela.

You do not have to read the chapters in order. Most stand alone. The
glossary at the back of the book is the fastest way to look up any term
that shows up before its chapter.

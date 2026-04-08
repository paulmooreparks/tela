# Introduction

Tela is a FOSS cloud system without the IaaS. It is a remote-access fabric,
a fleet management plane, a desktop client, a multi-organization web portal,
and a release engineering pipeline. The pieces compose into something
shaped like a managed cloud service for reaching and operating machines you
already own -- workstations, servers, edge devices, lab boxes, anything that
speaks TCP -- without renting compute from anyone or surrendering encryption
keys to a third party.

The transport layer uses WireGuard for end-to-end encryption, gVisor's network
stack for userspace networking, and a small relay hub to broker encrypted
sessions between agents and clients across firewalls and NATs. No TUN devices,
no kernel modules, no inbound ports on either endpoint, no root or
Administrator on the machines you connect to or from. The hub is a blind
relay; it cannot read what flows through it.

The management layer covers everything that turns "I have an encrypted tunnel"
into "I am running a fleet": token-based RBAC, per-machine permissions,
remote agent and hub management, file sharing, path-based gateways,
self-update through release channels, a desktop GUI (TelaVisor), and a
multi-org portal (Awan Saya). All free, all open source, all yours to run.

This book is the canonical reference. It is generated from the Markdown files
in the [tela](https://github.com/paulmooreparks/tela) repository on every
push to `main`, so it never drifts from the code that ships.

If you are new to Tela, start with [What Tela is](getting-started/what-tela-is.md)
and [Installation](getting-started/installation.md).

If you have a specific task in mind, the [How-to Guides](howto/channels.md)
section is a set of focused walkthroughs for the most common things people
need to do.

If you want to understand how Tela works under the hood, the
[Architecture](architecture/design.md) section is the design and protocol
documentation.

If you are deciding whether Tela fits your situation, the
[Use Cases](use-cases/personal-cloud.md) section walks through six concrete
scenarios with the deployment and access model for each.

## Conventions

- The three binaries are `tela` (client), `telad` (agent / daemon), and
  `telahubd` (hub / relay).
- "TelaVisor" is the desktop GUI built on top of `tela`.
- "Awan Saya" is the multi-org web portal that talks to multiple hubs.
- Code, file paths, command-line flags, and configuration keys are in
  `monospace`.
- Mermaid diagrams render natively in the HTML output.

## License

Apache License 2.0. See the
[LICENSE](https://github.com/paulmooreparks/tela/blob/main/LICENSE) file in
the repository.

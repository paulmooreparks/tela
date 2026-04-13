# Introduction

Tela is a connectivity fabric. It is a small set of programs that lets one
machine reach a TCP service on another machine through an encrypted tunnel,
without either side opening an inbound port, installing a virtual private
network (VPN) client, loading a kernel driver, or running anything as root
or Administrator. Remote desktop is the use case I built it for first.
Remote desktop is just one application that runs on the fabric, not the
point of it.

The point of the fabric is that the same three pieces scale from a single
laptop reaching a single home server all the way up to a fleet of machines
managed by a team, and the scaling does not require switching tools or
rearchitecting anything. The pieces are an agent (`telad`) that runs on the
machine you want to reach, a hub (`telahubd`) that brokers connections, and
a client (`tela`) that runs on the machine you want to reach from. Each is
a single static binary with no runtime dependencies. They run on Windows,
Linux, and macOS.

| Tier | What it looks like |
|------|---------------------|
| **Solo remote access** | One agent, one hub, one client. A few minutes from download to first connection. |
| **Personal cloud** | Several agents at home and work, file sharing, a desktop client for non-terminal users. |
| **Team cloud** | Named identities, per-machine permissions, pairing codes for onboarding, audit history, remote admin from the desktop client. |
| **Fleet** | Multiple hubs registered with a directory, identities and permissions managed centrally, agents updating themselves through release channels. |

The next chapter, [What Tela is](getting-started/what-tela-is.md), covers
the substrate properties, the design tradeoffs against existing tools, and
the things Tela is explicitly not trying to be. It is the concept primer.
After that, [Installation](getting-started/installation.md) and
[First connection](getting-started/first-connection.md) get you to a working
tunnel.

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
- The [Operations](ops/release-process.md) section covers the release
  process for hub and agent operators.
- The [Design Rationale](architecture/design.md) section answers the
  *why* questions: why three small daemons rather than one, why the hub
  is a blind relay, why the gateway is a primitive that recurs at four
  layers, why the access model has the four roles it does. Read it after
  the body of the book if you want to understand the project's design
  decisions and the alternatives that were considered and rejected.
- The [Appendices](guide/reference.md) collect the reference data: the
  command-line reference, the configuration file reference, and the
  glossary. Use them as lookups, not as reading.

The book is generated from the Markdown files in the
[tela](https://github.com/paulmooreparks/tela) repository on every push to
`main`, so it never drifts from the code that ships.

## Conventions

- The three binaries are `tela` (client), `telad` (agent or daemon), and
  `telahubd` (hub or relay).
- "TelaVisor" is the desktop graphical interface built on top of `tela`.
- A "hub directory" is anything that responds to the small Tela directory
  protocol; a "portal" is a directory plus extras (dashboard, identity,
  audit). See [Hub directories and portals](guide/directories.md).
- Code, file paths, command-line flags, and configuration keys are in
  `monospace`.
- Mermaid diagrams render natively in the HTML output.

## License

Apache License 2.0. See the
[LICENSE](https://github.com/paulmooreparks/tela/blob/main/LICENSE) file in
the repository.

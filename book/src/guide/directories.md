# Hub Directories and Portals

A single hub is enough for one team in one place. Real organizations end up
with several: per environment, per customer, per region, per acquisition.
The fabric handles this with a small directory protocol that lets a client
resolve hub names instead of memorizing URLs, and an optional portal layer
that adds dashboards and visibility on top of the directory.

## The Directory Protocol

Tela ships a hub directory protocol as part of the fabric, not as a separate
product. Two endpoints define it:

- **`/.well-known/tela`** is the discovery endpoint, following Request for
  Comments (RFC) 8615 (well-known Uniform Resource Identifiers). A client
  fetches it to discover where the directory's other endpoints live and
  what authentication they expect.
- **`/api/hubs`** is the directory itself: a list of hubs registered with
  this directory, each with a name, a public Uniform Resource Locator
  (URL), and optional metadata.

That is the whole protocol. Anything that responds correctly on those two
endpoints is a hub directory, regardless of what else it does. The full
wire contract is specified in
[Appendix D: Portal Protocol](../architecture/portal-protocol.md).

## Adding a Directory as a Remote

On the client side, a hub directory is added as a **remote**:

```bash
tela remote add work https://directory.example.com
```

Once a remote is registered, the client resolves short hub names through
it before falling back to the local `hubs.yaml` file:

```bash
tela machines -hub myhub                # short name resolved via remote
tela connect -hub myhub -machine prod-web01
```

The client does not change otherwise. The same `tela connect` command works
whether the user typed a full URL or a name that resolved through a
directory. A client can register more than one remote: a self-hosted
directory for internal hubs and a managed directory for cross-organization
or customer hubs, with the same `tela` binary talking to both.

## Listing a Hub in a Directory

On the hub side, a hub registers itself with a directory through the
`telahubd portal` subcommand:

```bash
telahubd portal add work https://directory.example.com
telahubd portal list
telahubd portal remove work
```

The `portal add` command discovers the directory's endpoints via
`/.well-known/tela`, registers the hub through the directory's API, and
stores the association in the hub's configuration. From that point on, any
client whose remote points at the same directory can find the hub by
name.

## What a Portal Adds on Top

The directory protocol is the floor. A **portal** is a directory plus
whatever extras the operator wants to layer on. Typical additions:

- **A multi-hub dashboard.** Status, agents, sessions, and history
  aggregated across every hub the user has access to, in one browser tab.
- **Identity beyond the hub.** Personal application programming interface
  (API) tokens issued by the portal, often tied to an external identity
  provider, that the client uses to authenticate against the portal itself
  rather than against each individual hub.
- **Multi-organization access control.** Users belong to organizations,
  organizations have teams, teams own hubs and agents. The portal becomes
  the place where membership and permissions live.
- **Web-based hub and agent administration** parallel to TelaVisor's
  Infrastructure mode but accessible from any browser.
- **Channel selectors** for hub and agent self-update, the same controls
  exposed in TelaVisor.
- **Activity logging and audit trails** that span multiple hubs.

A portal does not weaken the underlying hubs. Each hub still authenticates
and authorizes connections on its own, with its own tokens and its own
access control list. The portal handles discovery, identity, and
visibility, not trust delegation.

## Three Ways to Run One

The protocol is the contract; where the server comes from is up to you.

| Option | What It Is | When to Pick It |
|--------|------------|-----------------|
| **`telaportal`** | Tela's own standalone portal binary. Single-user, file-backed storage, zero external dependencies. Implements the full portal protocol. | You want name resolution and a personal dashboard for your own hubs without operating anything heavier than the hubs themselves. |
| **Your own implementation** | Any service that answers `/.well-known/tela` and `/api/hubs` correctly. | You already have an internal service catalog or portal and want Tela hubs listed in it. |
| **A managed portal** | A vendor-operated directory and dashboard. **Awan Saya** is one such option, available on request. | Fleet visibility, multi-organization access control, and onboarding speed matter more than self-hosting. |

The CLI does not care which one a remote points at. The same
`tela remote add` command and the same name-resolution path work for all
three.

## When You Need a Directory at All

If you are running a single hub for personal use, you do not need a
directory or a portal. The hub stands alone, the client connects to it by
URL, and the rest of this book applies as written. The directory layer
becomes useful when:

- You have more than one hub and users start asking which one to connect
  to.
- You are providing remote access as a service across multiple customers.
- You want fleet-wide visibility from one screen instead of clicking
  through each hub's console in turn.
- You want to manage onboarding centrally instead of distributing tokens
  out of band for every hub.

If none of those apply yet, skip this chapter and come back when one of
them does.

# Distributed Teams

Your engineering team is spread across cities and time zones. You have
shared development and staging infrastructure (databases, internal HTTP
services, build servers) that team members need to reach from home offices,
co-working spaces, and laptops on the road.

A team VPN works, but it requires a VPN server, client configuration on
every laptop, and it grants access to a whole network rather than to
specific services. Tela inverts that: each shared resource registers itself
with a hub under a named identity, and each team member gets a token scoped
to exactly the machines their role needs.

```
Services available:
  localhost:5432   → port 5432    (dev-db)
  localhost:22     → SSH          (dev-build)
  localhost:8080   → HTTP         (staging-app)
```

A new hire redeems a pairing code with one command and immediately has the
right access. When someone leaves, removing one identity ends their access
to everything at once.

## Topology

Pick a hub strategy first, because it defines your isolation boundaries:

- **One hub per environment** (`dev`, `staging`, `prod`) is the right
  default for a single organization. Different environments get different
  identity lists and cannot leak into each other.
- **One hub per site** fits teams organized around physical locations.
- **One hub per customer** is the MSP pattern; see
  [MSP and IT Support](msp-it-support.md).

Shared machines run endpoint agents as OS services. A machine that hosts
several internal HTTP tools is a good candidate for a
[path gateway](../guide/gateway.md), so the team reaches all of them
through one port and one origin. For sites with hardware that cannot run
`telad` (printers, appliances), one bridge agent per site can front them;
see the gateway pattern in [Run an Agent](../howto/telad.md).

## The Access Model

The shape that scales: one agent identity per shared machine, one identity
per human, connect grants that follow roles.

```bash
# A shared dev database
tela admin tokens add agent-dev-db01 -hub wss://dev-hub.example.com
tela admin access grant agent-dev-db01 dev-db01 register -hub wss://dev-hub.example.com

# A developer
tela admin tokens add alice -hub wss://dev-hub.example.com
tela admin access grant alice dev-db01 connect -hub wss://dev-hub.example.com
tela admin access grant alice dev-build connect -hub wss://dev-hub.example.com
```

For the dev hub specifically, a wildcard connect grant
(`tela admin access grant alice '*' connect`) is a defensible convenience:
every developer reaches every dev machine, including ones added next month.
Keep staging grants explicit, and production grants strictly explicit (see
[Production Access](production-access.md)).

**Onboarding** runs on pairing codes, not copied tokens. An admin runs
`tela admin pair-code dev-db01` (or generates one from TelaVisor's Access
tab), sends the short-lived code over chat, and the new hire runs the
printed `tela pair` command. The permanent token lands directly in their
credential store; nobody ever handles the raw value. See
[Credentials and Pairing](../guide/credentials.md).

**Offboarding** is `tela admin access remove <id>`, one command per hub.

## Shared Profiles

Build one profile per environment with pinned `local:` ports and
distribute the YAML file itself (it contains no secrets when tokens live in
the credential store). Team members drop it into `~/.tela/profiles/`, or
import it through TelaVisor's Profiles tab, and everyone's `dev-db` is on
the same local port, which keeps connection strings in wikis and `.env`
examples true for the whole team.

Machine naming is part of the same discipline: stable role-based names
(`staging-web02`, not an IP or a pet name) keep profiles, grants, and
conversations unambiguous.

## Pitfalls Specific to This Scenario

- Corporate networks and hotel Wi-Fi sometimes proxy or block WebSockets.
  Publish the hub on standard HTTPS (port 443) behind a reverse proxy, per
  [Run a Hub on the Public Internet](../howto/hub.md), and connections
  survive almost any network a laptop lands on.
- Do not share one identity across the team. Individual identities cost
  nothing and are the difference between "revoke Bob" and "rotate the
  token everyone uses and redistribute it."
- If developers can list a machine but not connect, the machine grant is
  missing; `tela admin access` shows the whole matrix at a glance.

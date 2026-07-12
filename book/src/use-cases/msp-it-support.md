# MSP and IT Support

You provide managed IT services or remote support to multiple customers.
Each customer has Windows workstations and servers you need to reach for
maintenance, troubleshooting, and remote desktop sessions. Today that means
asking customers to open RDP to the internet, maintaining per-customer VPN
configurations, or paying per-seat for a remote-access product.

With Tela, you run a small hub per customer. An agent on each customer
machine makes an outbound connection to that hub, so nothing changes on the
customer's firewall. Your technicians connect through the hub with
individual tokens, which means you know who accessed which machine and
when, and revoking a departed technician takes one command per hub.

```
Services available:
  localhost:3389   → RDP          (acme-desktop-01)
  localhost:13389  → RDP          (acme-desktop-02)
  localhost:22     → SSH          (acme-server-01)
```

## Topology

**One hub per customer** is the recommended isolation model. Each customer
gets its own hub (its own owner token, identity list, and history), so a
compromise or misconfiguration at one customer cannot bleed into another,
and offboarding a whole customer is decommissioning one hub. A single
shared hub for many customers is possible but demands strict naming and
much more careful grant hygiene; prefer isolation.

Run the hubs on infrastructure you control, published at customer-specific
URLs (`wss://acme-hub.example.com`), per
[Run a Hub on the Public Internet](../howto/hub.md).

On the customer side, two agent options:

- **An endpoint agent on each machine**, installed as an OS service. Expose
  only what support needs: RDP on workstations, SSH on servers.
- **A customer-site gateway** when you cannot install software on the
  endpoints: one `telad` on a small box at the site, with a machine entry
  per target (`target: 192.168.1.10`). The gateway box becomes critical
  infrastructure; isolate it and allowlist its reachable targets.

Register-type pairing codes make site onboarding scriptable: generate one
per machine (`tela admin pair-code ws-01 -type register`), and the
installation script at the customer site runs `telad pair` instead of
embedding a long-lived token.

## The Access Model

One identity per technician, per hub. Never a shared "support" token.

```bash
tela admin tokens add tech-bob -hub wss://acme-hub.example.com
tela admin access grant tech-bob ws-01 connect -hub wss://acme-hub.example.com
tela admin access grant tech-bob srv-01 connect -hub wss://acme-hub.example.com
```

For a technician who services everything at one customer, a wildcard
connect grant on that customer's hub is reasonable; the hub boundary is
already doing the isolation work. What individual identities buy you:

- The hub history attributes every session to a person, which is what a
  customer asks for after an incident.
- Offboarding a technician is `tela admin access remove tech-bob` on each
  customer hub, and their access is gone everywhere it existed.
- A leaked laptop is `tela admin rotate tech-bob`, not a fleet-wide
  credential rollover.

Managing a dozen customer hubs from one screen is what TelaVisor's
Infrastructure mode is for: every hub you hold credentials for appears in
the Hubs and Agents tabs, and the Access tab edits any hub's grant matrix.
For fleet-wide visibility beyond that, a portal aggregates all your hubs
into one dashboard; see
[Hub Directories and Portals](../guide/directories.md).

## The Technician Workflow

Technicians store one credential per customer hub (`tela login`), keep one
profile per customer with pinned `local:` ports, and connect on demand:

```bash
tela connect -profile acme
mstsc /v:localhost:3389        # acme-desktop-01
mstsc /v:localhost:13389       # acme-desktop-02
```

## Pitfalls Specific to This Scenario

- Tela transports RDP; it does not authenticate it. Windows login policy,
  NLA, and account lockout on the customer machines still apply and still
  matter.
- Customer sites with strict egress filtering need outbound HTTPS to your
  hub URL allowed; that is the only network requirement on their side.
- Keep a naming convention (`<customer>-<role><nn>`) even though hubs are
  per-customer. Profiles, history entries, and support tickets all read
  better for it.

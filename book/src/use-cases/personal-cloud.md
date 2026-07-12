# Personal Cloud

You have several machines at home behind a residential router: a Network
Attached Storage (NAS) device, a development workstation, a media server.
The router performs NAT and you either cannot or do not want to open
inbound ports. From a coffee shop or a corporate office, you currently have
no way to reach any of them.

Tela solves this with a hub on a small public VM (a $5/month server is
plenty). Each home machine runs `telad`, which makes an outbound connection
to the hub and registers itself. Your laptop runs `tela` and connects
through the hub to whichever machine you need:

```
Services available:
  localhost:22     → SSH          (workstation)
  localhost:10022  → SSH          (NAS)
  localhost:5000   → port 5000    (NAS web UI)
  localhost:8096   → port 8096    (media server)
```

Nothing changes on your home router. No ports are forwarded. The home
machines only make outbound connections.

## Topology

One hub, one profile, a handful of machines. Two ways to run the agents:

- **An agent on each machine** (the endpoint pattern) is the default
  choice. Each machine registers itself and exposes its own services.
- **One gateway agent for the house** works when some machines cannot run
  `telad` (an appliance NAS, a smart device). Run `telad` on one
  always-on box and give it a machine entry per target with a `target:`
  LAN address. See the gateway pattern in [Run an Agent](../howto/telad.md).

Setup mechanics are covered once, in the how-tos:
[Run a Hub on the Public Internet](../howto/hub.md) for the hub,
[Run an Agent](../howto/telad.md) for each machine, and
[Run Tela as an OS Service](../howto/services.md) so everything survives
reboots.

## The Access Model

Even for a single user, do not run everything on the owner token. Create
one agent identity with register permission per machine, and one user
identity for yourself with connect permission on all machines:

```bash
tela admin tokens add workstation-agent -hub wss://hub.example.com
tela admin access grant workstation-agent workstation register -hub wss://hub.example.com

tela admin tokens add me -hub wss://hub.example.com
tela admin access grant me '*' connect -hub wss://hub.example.com
```

The wildcard grant covers machines you add later, so onboarding a new home
machine never requires touching your own token. Store your token once with
`tela login` and forget about `-token` flags.

## One Profile for the Whole House

A single connection profile opens tunnels to every machine at once, and
explicit `local:` ports keep the layout stable when two machines expose the
same service:

```yaml
# ~/.tela/profiles/home.yaml
connections:
  - hub: wss://hub.example.com
    machine: workstation
    services:
      - remote: 22
  - hub: wss://hub.example.com
    machine: nas
    services:
      - remote: 22
        local: 10022
      - remote: 5000
  - hub: wss://hub.example.com
    machine: media
    services:
      - remote: 8096
```

Two additions earn their keep in this scenario:

- **File shares.** Enable a share on the NAS
  ([File Sharing](../guide/file-sharing.md)) and add a `mount:` block to
  the profile so the share appears as a drive letter whenever you connect.
- **DNS names.** `tela dns hosts` gives every machine a stable name
  (`ssh nas.tela`), which beats memorizing which fallback port the NAS
  landed on. See [Connection Profiles](../guide/profiles.md#dns-names).

## Pitfalls Specific to This Scenario

- The last hop from `telad` to the service is plain TCP. On the endpoint
  pattern that hop never leaves the machine; on the gateway pattern it
  crosses your LAN, so prefer services with their own encryption (SSH,
  HTTPS) for anything sensitive.
- Expose only the ports you actually use. A NAS admin UI you touch twice a
  year can stay unexposed until you need it; adding a service later is one
  line of YAML and a `telad` restart.
- If a machine never appears in `tela machines`, check the agent's outbound
  path to the hub first; residential networks rarely block it, but
  guest-network isolation can.

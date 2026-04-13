# Remote administration

Managing an agent or hub means changing its configuration, viewing its logs, restarting it, or updating it. There are several ways to implement this capability. Tela routes all management commands through the hub's existing admin API, rather than adding a direct management channel to each agent. This chapter explains why.

## The outbound-only constraint

The agent is designed to open no inbound ports. It connects outbound to the hub's WebSocket endpoint on startup and holds that connection. Nothing connects to the agent; the agent connects to everything it needs.

Adding a direct management channel to the agent would require the agent to listen for management connections. That means an inbound port, and an inbound port means the agent machine needs to be reachable from wherever the administrator is working. That is the problem Tela was designed to eliminate.

The management protocol is therefore built on the connection the agent already has: the control WebSocket to the hub. When an administrator sends a management command through the hub's admin API, the hub forwards the command to the target agent's control connection and returns the response. The agent never opens a new listener.

## The access model is in the hub

The hub holds the access model: token roles, per-machine permissions, ownership. When a management command arrives at the hub's admin API, it is authenticated and authorized by the same machinery that governs data connections. An admin token that grants connect permission on a machine does not automatically grant manage permission; the permissions are distinct.

If management commands went directly to agents, each agent would need its own access model. Tokens would need to be provisioned per agent. Revocation would require touching every agent individually. The hub's role as the single point of access enforcement would be bypassed.

By routing management through the hub, the hub's access model covers management operations without additional machinery.

## The portal composes naturally

A portal like Awan Saya aggregates multiple hubs. It knows which hubs belong to which organization and which accounts have access to which hubs. It does not have direct network access to individual agent machines, nor should it.

The portal authenticates to each hub once, with an admin token. Through each hub's management API, the portal can reach any agent registered to that hub. The portal's trust relationship is hub-to-hub, not portal-to-every-agent. This means:

- The portal needs one credential per hub, not one credential per agent.
- The hub enforces its own access model before forwarding commands.
- A compromised portal cannot reach agents on a hub it does not have credentials for.

If direct agent access were the design, a portal aggregating a thousand agents would need direct network paths and credentials for a thousand machines. The hub-mediated model means the portal needs credentials to dozens of hubs.

## The audit trail is centralized

When a management command passes through the hub, the hub records it: which identity issued the command, which machine it targeted, what the action was, and when. The agent records it locally as well. The hub log is the authoritative record for all commands that touched a given machine, regardless of whether they originated from the CLI, TelaVisor, or a portal.

Direct agent access would produce logs scattered across every agent machine, with no central record of who did what across a fleet.

## What the protocol looks like

The management protocol adds two message types to the control WebSocket the agent already maintains:

- `mgmt-request`: hub to agent, carrying the action and its payload
- `mgmt-response`: agent to hub, carrying the result

The hub maintains a pending-request map and returns the agent's response to the HTTP caller, with a 30-second timeout if the agent does not respond. From the caller's perspective, the hub admin API call is synchronous.

Supported actions are `config-get`, `config-set`, `restart`, `logs`, and `update`. The agent advertises management support during registration. Agents that predate the management protocol do not receive requests and do not need to be updated before the hub is.

For the full API reference, see [Appendix A: CLI reference](../guide/reference.md) and [Appendix B: Configuration file reference](../guide/configuration.md).

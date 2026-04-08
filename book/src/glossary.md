# Glossary

## Fabric

The woven interconnection layer that lets endpoints reach each other without
each endpoint having to know the topology. The term predates mesh networking
by decades and does not require peer-to-peer routing. Established prior art:

- **Switch fabric** is the backplane of a chassis switch. Strictly
  hierarchical, never peer-to-peer.
- **Leaf-spine fabric** (Cisco ACI, Arista, Virtual Extensible LAN [VXLAN]
  with Ethernet Virtual Private Network [EVPN]) is a two-tier hub-and-spoke
  topology. Every leaf talks to every spine; leaves never talk to each other
  directly. Vendors call it a fabric anyway, and the industry agrees.
- **Storage fabric** (Fibre Channel) is switched, not meshed. Hosts talk to
  targets through a fabric of switches.
- **Service Fabric** (Microsoft) is a service orchestrator. No mesh routing
  of any kind.

Tela is a fabric in the leaf-spine sense. The hub is the spine, the agents
and clients are the leaves, and most traffic travels client to hub to agent.
Direct peer-to-peer connections between clients and agents are negotiated
when the network allows them, but they are an optimization, not the default.
Tela is not a routed mesh in the Tailscale, Nebula, or ZeroTier sense, and
the design does not aspire to become one without an explicit scope decision
(see the *Scope decisions for 1.0* section of the
[roadmap](https://github.com/paulmooreparks/tela/blob/main/ROADMAP-1.0.md)).

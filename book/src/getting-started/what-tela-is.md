# What Tela is

Tela is a remote-access fabric. It lets you reach TCP services on a remote
machine through an encrypted WireGuard tunnel, without opening any inbound
firewall ports on either end, without installing kernel drivers, and without
running anything as root or Administrator.

## What it solves

The classic remote-access problem looks like this: you have a machine
somewhere -- a workstation, a server, a SCADA gateway, a Raspberry Pi -- and
you want to reach a service on it (SSH, RDP, Postgres, an HTTP API, a SMB
share, anything that speaks TCP). You are not on the same network. There is
a firewall in the way. You don't control the firewall. You can't open
inbound ports. You don't want to use a vendor-locked cloud service. You
don't want a kernel-mode VPN that requires admin rights to install.

Most of the existing solutions force a tradeoff:

- A traditional VPN requires admin to install on the client and inbound
  firewall rules on the server.
- SSH port forwarding requires SSH access to a publicly reachable jump host.
- A vendor cloud service (TeamViewer, AnyDesk, etc) works but you give up
  control, run an opaque agent, and pay per seat.
- WireGuard kernel-mode requires `CAP_NET_ADMIN` or root, plus a TUN device
  and inbound rules.

Tela gets all of the security guarantees of WireGuard with none of the
deployment friction:

- WireGuard runs **in userspace** via gVisor's network stack. No TUN device,
  no kernel module, no admin/root.
- Both sides connect **outbound** to a shared hub. No inbound firewall rules
  anywhere except on the hub itself.
- The hub is a **blind relay**. It cannot read tunnel contents -- WireGuard
  encryption is end-to-end between the agent and the client.
- The whole stack is **three small static Go binaries** with no runtime
  dependencies.

## What it is not

- It is not a mesh VPN like Tailscale or Nebula. There is no overlay network
  with auto-discovery; you connect to one machine at a time.
- It is not a multi-tenant SaaS. You run the hub yourself.
- It is not a transport for arbitrary IP traffic. It tunnels TCP services,
  one machine at a time.
- It is not a replacement for SSH for shell sessions; it is a way to *get*
  SSH (or RDP, or Postgres) onto your laptop without configuring port
  forwarding or VPNs.

## Why three binaries

The split is deliberate.

- **`telahubd`** is the only binary that needs to be publicly reachable.
  Everything about its job is "be the meeting point." It cannot read what
  flows through it.
- **`telad`** lives on the machine you want to reach. Its job is to register
  with a hub and unwrap the encrypted tunnel into a local TCP connection.
- **`tela`** lives on the machine you connect from. Its job is to dial a hub,
  set up the encrypted tunnel, and bind a local TCP listener that forwards
  through the tunnel.

This is the WireGuard model expressed as three small daemons:
the agent and the client are peers; the hub is a router with no keys.

For the architectural details see [Design overview](../architecture/design.md).
For installation see [Installation](installation.md).

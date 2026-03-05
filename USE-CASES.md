# Tela — Use Cases

## Overview

Tela is a connectivity fabric that provides outbound-only, encrypted TCP tunnels between machines, with no installation, no admin privileges, and no inbound ports required on either end.

This document describes concrete scenarios where Tela's design gives it a meaningful advantage over existing tools.

---

## Deployment Patterns (Important)

Tela supports two common real-world deployment patterns. Most use cases below work with either; the right choice depends on operational constraints and your threat model.

### Pattern A: Endpoint Agent (Canonical)

- `telad` runs directly on the machine that hosts the services.
- Best when you control the machine and can run a long-lived daemon.
- Strongest “last hop” story: `telad` to the local service is usually `localhost`.

### Pattern B: Gateway / Bridge Agent

- `telad` runs on a gateway (VM/container/bastion) that can reach one or more target machines over the local network.
- Best when you cannot install on targets (locked-down endpoints, appliances) or want to minimize software on sensitive hosts.
- Tradeoff: the hop from `telad` to the target service is normal LAN/VPC TCP, so you rely on segmentation and service-level security.

**Example (your current Barn setup):** `telad` runs in Docker and forwards to the Windows host via `host.docker.internal` while presenting the host as a machine named `barn`.

---

## 1. Personal Cloud / Homelab Remote Access

**Scenario:** You have machines at home (NAS, media server, dev workstation) and want to reach them from anywhere: a hotel, a coffee shop, a corporate laptop that won't let you install a VPN.

**How-to (at a glance):**

- Pick a deployment pattern: **Endpoint agent** (telad runs on the target) or **Gateway/bridge** (telad runs on a reachable gateway).
- Run a hub that both sides can reach (public `wss://...` is typical).
- Run `telad` to register your machine/services.
- Download `tela` on the client machine and run `tela machines` then `tela connect`.

Detailed HOWTO: `howto/personal-cloud.md`

**How Tela works here:**

- `telad` runs on your home machines, connecting outbound to your hub. No port forwarding, no dynamic DNS.
- From any machine, you download `tela`, run it, and get an SSH or RDP session to your home machine.
- Works through any network that allows outbound HTTPS, including locked-down corporate Wi-Fi.

**Compared to alternatives:**

| Solution | Friction |
|----------|----------|
| **Tailscale / ZeroTier** | Requires installing a client that creates a TUN device. Blocked on managed corporate laptops. |
| **Cloudflare Tunnel** | Great for HTTP, awkward for raw TCP (SSH/RDP). Requires a CF account and DNS delegation. |
| **WireGuard (raw)** | Needs admin/root on both ends, port forwarding or a VPS, manual key management. |
| **ngrok** | Per-tunnel pricing, HTTP-focused, no persistent multi-service model. |
| **tela** | Zero-install single binary, no TUN, no admin, outbound-only from the home machine. |

---

## 2. Distributed Development Teams

**Scenario:** A dev team spans multiple offices and remote workers. Developers need to access shared dev/staging machines (databases, test servers, CI runners) without a full corporate VPN.

**How-to (at a glance):**

- Decide whether you deploy `telad` on every server (endpoint pattern) or on a site gateway (bridge pattern).
- Stand up one hub per environment (`dev`, `staging`, `prod`) or per site.
- Register machines/services with `telad`.
- Developers use `tela machines/services/connect` to reach SSH/RDP/DB ports.

Detailed HOWTO: `howto/distributed-teams.md`

**How Tela works here:**

- `telad` runs on each dev/staging machine, registering outbound to the hub. IT doesn't need to open inbound ports or manage VPN concentrators.
- Developers download `tela` and connect to machines by name. No IP addresses, no config files, no WireGuard key exchange ceremonies.
- **Service-level granularity.** Unlike a VPN that exposes the whole network, Tela exposes only declared services (SSH:22, Postgres:5432, etc.). Closer to a zero-trust model.
- **Contractor access.** Give a contractor access to one hub. They download `tela`, connect, see only what they're allowed to. Revoke access by removing them. No VPN client to uninstall.

**Compared to alternatives:**

| Solution | Friction |
|----------|----------|
| **Teleport** | Full-featured but heavy — requires deploying a proxy, installing `tsh`, certificate infrastructure. |
| **Tailscale** | Excellent, but requires TUN/admin and a Tailscale account per device. Not viable on managed corporate machines. |
| **SSH jump hosts** | Only works for SSH. Tela tunnels any TCP service. |

---

## 3. IoT / Edge Device Management

**Scenario:** You deploy devices (Raspberry Pi, industrial controllers, kiosks) on customer sites behind NATs and firewalls you don't control. You need to SSH in for maintenance.

**How-to (at a glance):**

- Run a hub reachable from the public Internet (or reachable from your clients).
- Install and run `telad` on each device (or run a gateway `telad` inside the customer site).
- Expose SSH (and any other needed ports) as services.
- Use `tela connect` from your laptop and SSH to localhost.

Detailed HOWTO: `howto/iot-edge.md`

**How Tela works here:**

- `telad` is a static Go binary. Cross-compile for ARM, drop it on the device, point it at your hub. It connects outbound and stays registered.
- No need to convince the customer to open firewall ports.
- The hub console shows all devices, their status, services, and last-seen time: a lightweight fleet view.

**Compared to alternatives:**

| Solution | Friction |
|----------|----------|
| **Balena / Particle** | Full IoT platforms with their own OS layers. Overkill if you just need remote SSH. |
| **SSH reverse tunnels** | Fragile, no multiplexing, no dashboard, manual management. |
| **Tailscale** | Needs TUN support on the device OS; not always available on minimal embedded Linux. |

---

## 4. Production Service Access (Bastion Replacement)

**Scenario:** A small team runs production services (databases, internal APIs) on cloud VMs. Today they use SSH bastion hosts or VPN to access them.

**How-to (at a glance):**

- Prefer the **endpoint agent** pattern for production VMs (least moving parts).
- Expose only required ports (SSH, DB, admin HTTP) as services.
- Restrict and rotate hub credentials.
- Operators use `tela` to connect to services without opening inbound ports.

Detailed HOWTO: `howto/production-access.md`

**How Tela works here:**

- `telad` on each VM exposes only the declared ports. No bastion host to maintain.
- Audit trail via hub history (who connected, when, to what).
- WireGuard encryption end-to-end — the hub never sees plaintext.
- Works on any cloud, bare metal, or on-prem. No vendor lock-in.

**Compared to alternatives:**

| Solution | Friction |
|----------|----------|
| **AWS SSM / GCP IAP** | Vendor-locked. Only works within that cloud provider. |
| **Bastion hosts** | Single point of failure, SSH-only, key management burden. |
| **HashiCorp Boundary** | Conceptually similar (identity-based access), but much heavier to deploy and operate. |

---

## 5. MSP / IT Support

**Scenario:** A managed service provider supports dozens of small businesses, each with a few machines needing periodic maintenance.

**How-to (at a glance):**

- Use one hub per customer (simplest isolation) or one hub with strict naming/tagging.
- Deploy `telad` on customer machines (endpoint) or at the customer edge (gateway).
- Expose RDP/SSH and any required admin ports.
- Technicians use `tela` on-demand from anywhere.

Detailed HOWTO: `howto/msp-it-support.md`

**How Tela works here:**

- Install `telad` on each customer's machines. Each customer gets a hub (or shares one with per-customer machine tagging).
- Customer machines are behind NATs the MSP can't control. Outbound-only is essential.
- Zero-install client means the MSP tech can connect from any machine — their own laptop, a customer's workstation, a hotel business center.

**Compared to alternatives:**

| Solution | Friction |
|----------|----------|
| **TeamViewer / AnyDesk** | Proprietary, per-seat licensing, privacy concerns, screen-sharing-focused rather than service-level access. |
| **ConnectWise** | Expensive, vendor-locked, complex setup. |
| **MeshCentral** | Excellent for this use case. Tela's advantage: cleaner separation (fabric vs. platform), zero-install client, service-level (not screen-level) model. |

---

## 6. Education / Lab Environments

**Scenario:** A university runs a computer lab with specialized software. Students need to access lab machines remotely.

**How-to (at a glance):**

- Stand up one hub per lab (or per course) and register lab machines.
- Expose RDP/VNC/SSH as services.
- Students download `tela` and connect to assigned machines.
- Use naming conventions to keep assignment simple.

Detailed HOWTO: `howto/education-labs.md`

**How Tela works here:**

- `telad` on each lab machine. Students download `tela`, connect, get RDP or VNC to their assigned machine.
- No VPN infrastructure needed. Works through student home networks.
- Hub console shows which machines are available and in use.

---

## Where Tela Is Uniquely Positioned

No existing tool provides this exact combination:

1. **Zero-install, no-admin client** — works on locked-down machines
2. **Protocol-agnostic TCP tunneling** — not just SSH, not just HTTP
3. **Outbound-only agents** — works behind any NAT/firewall
4. **End-to-end WireGuard encryption** — hub never sees plaintext
5. **Lightweight, single-binary deployment** — no infrastructure to maintain beyond the hub

Tailscale comes closest but requires system-level installation. Cloudflare Tunnel is HTTP-focused. Teleport and Boundary are heavy. MeshCentral is screen-sharing-centric. Tela occupies the gap between "just use SSH" and "deploy an enterprise zero-trust platform."

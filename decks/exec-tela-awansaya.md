---
marp: true
title: Tela + Awan Saya — Secure Remote Access Without VPN
description: Executive overview for IT leadership
paginate: true
size: 16:9
theme: default
---

<!--
Export in VS Code:
1) Install "Marp for VS Code"
2) Open this file
3) Run "Marp: Export slide deck..."
-->

<style>
section {
  font-size: 28px;
}
.lead {
  font-size: 1.25em;
}
.small {
  font-size: 0.8em;
}
.center {
  text-align: center;
}
</style>

# Tela + Awan Saya
## Secure remote access without VPN friction

<div class="lead">

**Tela** = connectivity fabric
**Awan Saya** = platform layer

</div>

---

# Executive summary

- IT teams need access to systems that are **private, segmented, and locked down**
- Traditional approaches often mean **too much network access** or **too much operational overhead**
- **Tela** provides narrow access to specific TCP services
- **Awan Saya** adds multi-hub visibility, discovery, access control, and onboarding on top

**Bottom line:** simpler remote access, smaller blast radius, less VPN friction.

---

# The problem

- Teams are distributed
- Infrastructure is behind NAT and firewalls
- Corporate endpoints often block admin installs, drivers, and TUN devices
- Security teams want **least privilege**, not flat network access

**Result:** remote access is slow to roll out and hard to control.

---

# Why existing approaches fall short

- **VPNs / mesh VPNs**
  Often require admin rights, drivers, or broad network trust

- **Bastions / jump hosts**
  Add infrastructure and create choke points

- **HTTP tunnels**
  Great for web apps, awkward for raw TCP services

- **Large ZT platforms**
  Powerful, but often heavy and expensive for small or mid-sized teams

---

# Tela in one slide

**Tela gives users secure access to TCP services without requiring a traditional VPN.**

- End-to-end encrypted userspace WireGuard tunnel
- Hub relays **ciphertext only**
- Client and agent are both **outbound-only**
- No admin privileges or TUN devices required
- Existing tools keep working through `localhost`

**Path:**
App → localhost → `tela` → hub → `telad` → target service

---

# Why Tela is different

1. **Zero-install, no-admin client**
2. **Protocol-agnostic TCP tunneling**
3. **Outbound-only connectivity**
4. **End-to-end encryption through a blind relay**
5. **Single-binary, lightweight deployment**

This combination is what makes Tela practical in locked-down environments.

---

# Where it fits best

- **Developer access** to staging or production systems
- **Bastion replacement** for SSH, RDP, and database access
- **MSP / IT support** for customer environments behind NAT
- **IoT and edge** deployments in networks you do not control
- **Training labs / classrooms** without VPN rollout overhead

---

# Operating model

## Two supported patterns

- **Endpoint agent**
  `telad` runs on each managed machine

- **Gateway / bridge agent**
  `telad` runs on a gateway that can reach internal targets

## Control surface stays small

- Expose only the specific service ports you want
- Avoid exposing an entire subnet or network segment

---

# Security view

- **Encryption:** end-to-end WireGuard tunnel
- **Exposure:** outbound-only from both sides
- **Authentication:** token-based today; rotate and manage as secrets
- **Segmentation:** one hub per environment, site, or customer is straightforward
- **Auditability:** hubs expose connection history and status APIs

For leadership, the key idea is simple: **less network exposure, tighter scope, easier review**.

---

# Awan Saya’s role

Tela is the engine.

Awan Saya adds the platform features around it:

- Multi-hub dashboard
- Hub directory and name resolution
- Easier onboarding
- Shared view of machines, services, and sessions
- Foundation for centralized auth and RBAC

**Analogy:** Tela : Awan Saya :: git : GitHub

---

# What the platform changes for users

Without a platform layer:

- users need to know specific hub URLs
- onboarding is manual
- multi-hub visibility is fragmented

With Awan Saya:

- users log in once
- hubs can be discovered by name
- one dashboard shows fleet-wide status

---

# Adoption path

## Start narrow

- One hub
- One team
- Two or three services

## Prove value

- Faster access setup
- Fewer inbound firewall changes
- Smaller blast radius than VPN

## Then scale

- One hub per environment, site, or customer
- Add Awan Saya for centralized visibility and discovery

---

# Recommended first pilot

Choose one:

1. **Developer access to staging**
2. **Production bastion replacement**
3. **MSP support into customer networks**

Pilot success metrics:

- time to onboard a user
- number of inbound rules avoided
- time to reach a target machine or service
- auditability of who connected and when

---

# Next steps

- Stand up a hub
- Validate reachability and audit endpoints
- Connect one or two target services
- Onboard a small user group
- Add Awan Saya when multi-hub discovery and visibility become useful

## Goal

Move from ad hoc remote access to a **repeatable connectivity fabric + platform model**.


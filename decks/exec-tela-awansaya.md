---
marp: true
title: Tela + Awan Saya — Secure Remote Access Without VPN
description: Executive overview for IT leadership
paginate: true
size: 16:9
---

<!--
How to export (VS Code):
1) Install extension: "Marp for VS Code" (Marp Team)
2) Open this file
3) Ctrl+Shift+P → "Marp: Export slide deck..." → PPTX (or PDF/HTML)
-->

# Tela + Awan Saya
## Secure remote access to TCP services — without VPN friction

**Tela:** connectivity fabric (engine)

**Awan Saya:** platform layer (portal + hub directory)

---

# What IT teams are up against

- Remote work + contractors + distributed teams are normal
- Critical systems sit behind NAT, firewalls, and segmented networks
- Endpoints are increasingly locked down (no admin, no drivers, no VPN clients)
- Traditional approaches widen blast radius ("full network" VPN) or add brittle infrastructure (bastions)

**Result:** access becomes slow, risky, and operationally expensive.

---

# Why the status quo breaks down

- **VPN / mesh VPNs**: great when you can install and create a TUN device; often blocked on managed corporate devices
- **Bastion / jump hosts**: SSH-centric, adds a choke point to maintain, and still requires opening inbound paths somewhere
- **HTTP-only tunnels**: strong for web apps; awkward for raw TCP (RDP/SSH/DB)
- **Enterprise ZT access platforms**: powerful, but heavy to deploy and operate for many teams

IT needs something that works in locked-down reality.

---

# Tela in one slide

**Tela provides secure remote access to TCP services (SSH, RDP, HTTP, etc.)**

- **End-to-end encrypted** userspace WireGuard tunnel between `tela` (client) and `telad` (agent)
- Hub relays **ciphertext only** (never sees plaintext)
- **Outbound-only** from both client and agent (works through HTTPS/WebSockets)
- **No admin privileges or TUN devices required** on either end
- Local apps connect to `127.0.0.1:<port>` (SSH/RDP/DB clients work unchanged)

Simple path:

`App → localhost → tela → (wss) hub → telad → local service`

---

# What makes Tela different

No existing tool hits this exact combination:

1) **Zero-install, no-admin client** (works on locked-down machines)
2) **Protocol-agnostic TCP tunneling** (not just SSH, not just HTTP)
3) **Outbound-only agents** (works behind NAT/firewalls you don’t control)
4) **End-to-end WireGuard encryption** (hub is a blind relay)
5) **Lightweight single-binary deployment** (minimal moving parts)

---

# Use cases that map to IT pain

- **Distributed dev teams**: access dev/staging/prod machines by name; expose only needed services
- **Production access (bastion replacement)**: SSH/DB/admin ports without opening inbound ports on prod VMs
- **MSP / IT support**: reach customer endpoints behind NAT without screen-sharing tooling or per-seat licensing
- **IoT / edge management**: maintain devices on customer sites you don’t control; persistent outbound registration
- **Education / labs**: students connect to assigned lab machines without VPN infrastructure
- **Personal cloud / homelab**: same mechanics; validates the model in hostile networks (hotel, café, corporate Wi‑Fi)

---

# Operational model (keeps blast radius small)

**Two deployment patterns (both supported):**

- **Endpoint agent (canonical)**: `telad` runs on each machine that hosts services (strongest last-hop story)
- **Gateway / bridge agent**: `telad` runs on a gateway that can reach targets over LAN/VPC (useful for locked-down endpoints)

**Service-level granularity**

- You expose only declared ports (e.g., `22`, `3389`, `5432`) instead of a whole network segment.

---

# Security & controls (what an exec should care about)

- **Encryption:** WireGuard end-to-end; hub relays opaque ciphertext
- **Auth:** token-based authentication supported (treat as secrets; rotate)
- **Network exposure:** agents and clients are outbound-only; no inbound ports required on managed machines
- **Environment boundaries:** simplest control is dedicated hubs per environment/customer
- **Audit trail:** hubs expose `/api/history` (who connected, when, to what)

---

# Awan Saya: the platform layer for Tela

Tela is the tool you run.

Awan Saya turns it into a service:

- **Portal dashboard**: one view across many hubs (machines, services, sessions)
- **Hub directory API**: `GET /api/hubs` enables short hub names in the CLI
- **Onboarding**: one login, then connect by hub name
- **Federation model**: any hub exposing the standard API can be registered
- **SSO & RBAC**: planned (centralized authentication and access control)

Analogy used in the docs:

**Tela : Awan Saya :: git : GitHub**

---

# How Awan Saya makes Tela easy to use

1) Stand up one or more hubs (each hub must be reachable over HTTPS/WebSockets)
2) Register hubs in Awan Saya’s directory (today: file-backed `www/portal/config.json`)
3) Users do a one-time login, then operate by name:

- `tela login https://awansaya.net`
- `tela machines -hub owlsnest`
- `tela connect -hub owlsnest -machine barn`

Portal reality check:

- The portal runs in the browser and fetches `/api/status` and `/api/history` directly from each hub.

---

# A practical adoption path

- **Start small:** one hub + one team + 2–3 services (SSH/RDP/DB)
- **Prove outcomes:** faster access, fewer inbound rules, smaller blast radius than VPN
- **Scale out:** one hub per environment/site/customer; add Awan Saya for multi-hub visibility and hub name resolution
- **Standardize:** move from ad-hoc access to a repeatable “connectivity fabric + platform” model

---

# What to do next

- Pick an initial use case:
  - production bastion replacement, or
  - developer access to staging, or
  - MSP customer support
- Decide deployment pattern (endpoint vs gateway/bridge)
- Stand up a hub and validate reachability (`/api/status`, `/api/history`)
- Register the hub in Awan Saya and onboard 3–5 users


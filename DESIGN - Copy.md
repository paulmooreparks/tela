# **Tela — Design, Architecture, and Agent‑Centric Development Strategy**
### *FOSS Remote‑Access & Connectivity Fabric (SEA‑rooted, Global‑ready)*
### *Version 0.3 — Authoritative Specification*

---

## **0. Purpose**
Tela is a **FOSS connectivity fabric** that provides:

- outbound‑only tunnels
- multiplexed TCP channels
- zero‑install client access
- browser‑based local‑port bridging
- a stable substrate for future cloud services

This document defines:

- architecture
- protocol
- security model
- MeshCentral integration boundaries
- browser TCP bridge mechanism
- agent concurrency model
- LLM agent guardrails
- roadmap

This is the **canonical reference** for all human and LLM contributors.

---

# **1. Identity & Positioning**

## **1.1 Name**
**Tela** — Filipino for *fabric*.
Represents a woven mesh of nodes, tunnels, and services.

## **1.2 Role in the ecosystem**
Tela is the **engine**.
Awan Satu is the **platform**.

Tela : Awan Satu :: Docker : Kubernetes
Tela : Awan Satu :: WireGuard : Tailscale
Tela : Awan Satu :: git : GitHub

Tela is stable, minimal, self‑hostable.
Awan Satu is orchestrated, multi‑service, monetizable.

---

# **2. Goals & Non‑Goals**

## **2.1 Goals**
- Zero‑install client access
- Outbound‑only connectivity
- Cross‑platform agent
- Protocol‑agnostic TCP proxying
- Browser TCP bridge
- Long‑term backward compatibility
- Minimal dependencies
- Serve as substrate for Awan Satu

## **2.2 Non‑Goals**
- No dashboards or orchestration in Tela
- No identity providers (belongs to Awan Satu)
- No trendy frameworks
- No microservices
- No protocol churn

---

# **3. Architecture Overview**

## **3.1 Components**
- **Tela Agent** — C/C++ single binary
- **Tela Hub** — Node.js LTS, MeshCentral‑derived
- **Tela Web** — Vanilla JS
- **Tela CLI** — Go static binary
- **Awan Satu** — future control plane

## **3.2 Data Flow**
1. Agent → outbound TLS+WebSocket → Hub
2. Browser → WebSocket → Hub
3. Hub multiplexes channels
4. Browser exposes ephemeral `localhost:<port>`
5. Native clients connect locally
6. Traffic flows:
   `client → browser → hub → agent → target service`

---

# **4. MeshCentral Integration Boundary**

MeshCentral is a mature, stable, battle‑tested remote‑access system. Tela will **reuse**, not fork, the following:

### **4.1 Components reused from MeshCentral**
- WebSocket multiplexing engine
- Agent connection lifecycle
- Basic session broker logic
- Core agent transport code
- Minimal parts of the Hub’s device registry

### **4.2 Components replaced or rewritten**
- Entire UI (Tela Web is new)
- Browser TCP bridge (MeshCentral does not have this)
- Local‑client ephemeral port mechanism
- Service exposure API
- Metadata/tagging system
- Authentication model (Tela uses local auth; AS uses SSO)
- Protocol spec (Tela freezes v1; MeshCentral evolves faster)

### **4.3 Rationale**
MeshCentral solves the hardest parts:
- stable agent transport
- cross‑platform C/C++ agent
- WebSocket multiplexing
- NAT traversal

Tela builds a **general‑purpose fabric** on top of these primitives.

---

# **5. Browser TCP Bridge (Critical Component)**

Browsers **cannot bind TCP ports**.
Tela must provide a mechanism that *appears* to expose `localhost:<port>`.

Three viable approaches exist:

## **5.1 Preferred: Localhost Helper (Tiny Local Relay)**
A tiny helper binary (1–2 MB, Go or C) runs on the locked‑down machine *without installation*:

- downloaded on demand
- runs as a user process
- binds `localhost:<port>`
- forwards TCP → WebSocket → Hub → Agent

### Why this is acceptable:
- No admin rights required
- No installation required
- No drivers or services
- Works on all browsers
- Predictable behavior

This is the **most reliable** and **least hacky** solution.

## **5.2 Optional: Browser Extension**
Pros:
- No helper binary
- Can bind localhost via Native Messaging

Cons:
- Requires installation
- Not allowed on locked‑down corporate laptops
- Breaks the “zero‑install” promise

This is a fallback, not the primary path.

## **5.3 Optional: WebRTC DataChannel + WebAssembly TCP shim**
Pros:
- Pure browser
- No helper

Cons:
- Cannot bind localhost
- Requires in‑browser RDP/SSH clients
- Higher latency

This is used for **in‑browser fallback**, not local‑client mode.

---

# **6. Protocol Specification (Authoritative)**

## **6.1 Transport**
- TLS 1.3
- WebSocket (wss://)
- Multiplexed binary frames

## **6.2 Frame Header (DO NOT MODIFY)**
```c
/*
  WARNING: DO NOT MODIFY THIS STRUCT.
  This struct is serialized over the wire.
  Adding, removing, or reordering fields breaks protocol compatibility.
*/
typedef struct {
    uint8_t version;
    uint32_t session_id;
    uint16_t channel_id;
    uint8_t type;
} tela_frame_header_t;
```

## **6.3 Control Messages**
- JSON
- Additive only
- Unknown fields ignored

## **6.4 Backward Compatibility**
- 18‑month support window
- No breaking changes
- No field removals
- No renames

---

# **7. Tela Agent Design**

## **7.1 Responsibilities**
- Outbound TLS+WebSocket
- Heartbeats
- Session initiation
- TCP proxying
- Local service discovery
- Policy enforcement

## **7.2 Concurrency Model (Clarified)**
Tela Agent uses:

- **libuv** event loop (same as Node.js)
- **One main loop** for:
  - WebSocket I/O
  - control messages
  - channel multiplexing
- **Worker threads** for:
  - TCP socket read/write
  - optional compression
  - optional encryption

### Invariants:
- No async/await abstractions
- No refactoring to promises/futures
- No thread‑per‑connection model
- No lock‑heavy designs

## **7.3 Security Model (Expanded)**

### **Agent Identity**
- Each agent generates a long‑lived Ed25519 keypair
- Public key registered with Hub
- Private key stored locally

### **Hub Identity**
- Hub presents TLS certificate
- Agent pins the Hub’s certificate fingerprint

### **Provisioning**
- One‑time provisioning token
- Token exchanged for session token
- Token invalidated immediately

### **Session Security**
- All traffic encrypted via TLS
- Optional E2E encryption via WebCrypto

### **Threat Model**
Protects against:
- MITM
- replay attacks
- rogue agents
- compromised Hub

Does not protect against:
- compromised endpoint machines

---

# **8. Tela Hub Design**

## **8.1 Responsibilities**
- Agent registry
- Session broker
- Multiplexing
- Ephemeral port allocation
- REST API

## **8.2 Authentication (Standalone Tela)**
Before Awan Satu exists, Tela Hub uses:

### **Local Auth Model**
- bcrypt‑hashed passwords
- stored in SQLite
- session cookies (HTTP‑only, secure)
- optional TOTP

### **Rationale**
- Simple
- Self‑contained
- No external dependencies

Awan Satu will later replace this with SSO.

---

# **9. Tela Web (Browser Client)**

## **9.1 Responsibilities**
- Login
- Machine list
- Session initiation
- Local‑client mode (via helper)
- In‑browser fallback

## **9.2 Constraints**
- No frameworks
- No build tools
- No dependency creep

---

# **10. Tela CLI (Rationale)**

The CLI is written in **Go** because:

- static binaries
- instant cross‑compilation
- no runtime dependencies
- excellent for small utilities
- avoids coupling CLI to Hub or Agent codebases

The CLI is intentionally **not** part of the core runtime.

---

# **11. End‑to‑End Usage Flow (Before Awan Satu)**

## **11.1 Setup**
1. Deploy Tela Hub on VPS
2. Put behind Cloudflare Tunnel
3. Create local Tela user

## **11.2 Register Windows machine**
1. Install Tela Agent
2. Provide Hub URL + provisioning token
3. Agent appears in Tela Web

## **11.3 Access from locked‑down laptop**
1. Open browser → `https://tela.yourdomain.com`
2. Log in
3. Select machine
4. Choose **RDP (local client)**
5. Browser downloads tiny helper
6. Helper binds `localhost:<port>`
7. Run `mstsc /v:localhost:<port>`

Traffic flows:
`mstsc → helper → browser → hub → agent → Windows RDP`

---

# **12. Tela as Substrate for Awan Satu**

Tela provides:
- universal agent
- outbound tunnels
- multiplexed channels
- service exposure
- metadata
- identity at machine level

Awan Satu orchestrates:
- identity
- RBAC
- service catalogs
- hosting
- databases
- monitoring
- backups
- automation
- billing

Tela is the Docker‑layer.
Awan Satu is the Kubernetes‑layer.

---

# **13. Roadmap (Updated)**

## **13.1 Minimum Viable Fabric (0–3 months)**
- Outbound‑only agent
- Multiplexed channels
- Session broker
- Browser TCP bridge (helper‑based)
- Minimal UI
- CLI (basic)
- Windows + Linux agents

## **13.2 Fabric Extensions (3–6 months)**
- macOS agent
- SFTP + file transfer
- Metadata + tagging
- Service exposure API
- In‑browser RDP/SSH fallback

## **13.3 Substrate for Awan Satu (6–12 months)**
- HA Hub
- Postgres backend
- Node grouping
- Capability discovery
- Metrics + logs channels

## **13.4 Awan Satu v0.1 (12–18 months)**
- Identity (Cloudflare Access)
- Provisioning tokens
- Machine inventory
- Session initiation
- Basic RBAC

---

# **14. LLM Agent Guardrails**

LLM agents must:

- follow this document strictly
- preserve all invariants
- avoid creative refactors
- avoid dependency additions
- maintain backward compatibility
- treat warnings as hard constraints

Human maintainers must review:

- protocol changes
- security logic
- concurrency logic
- public API changes


# **Tela — Design, Architecture, and Agent‑Centric Development Strategy**

### *FOSS Remote‑Access & Connectivity Fabric (SEA‑rooted, Global‑ready)*

### *Version 0.4 — Authoritative Specification*

---

## **0. Purpose of This Document**

Tela is a **FOSS connectivity fabric** that provides outbound‑only tunnels, multiplexed TCP channels, zero‑install client access, browser‑mediated orchestration, helper‑mediated local TCP bridging, and a stable substrate for future cloud services.

This document defines the architecture, protocol, security model, MeshCentral integration boundary, browser TCP bridge mechanism, agent concurrency model, literate coding standards, LLM agent guardrails, and roadmap.

This document is **authoritative**.
LLM agents must treat it as the **single source of truth**.
Any deviation must be explicitly approved by a human maintainer.

---

## **0.1 Glossary**

This glossary defines terms as they are used in Tela.

| Term | Definition (Tela meaning) |
| --- | --- |
| **Agent** | The long‑lived process on a managed machine that makes an outbound connection to the Hub and proxies traffic to local services (e.g., RDP/SSH/HTTP). |
| **Browser UI / Web** | The web page the user opens for login and session setup. It orchestrates sessions but is not in the data path. |
| **Certificate pinning** | The agent/helper refuses to connect unless the Hub presents the expected TLS certificate fingerprint, preventing MITM even when a proxy can issue “valid” certificates. |
| **Channel** | A logical stream within a single WebSocket connection (e.g., one proxied TCP connection) identified by a channel ID and managed via `open`/`close`. |
| **Connectivity fabric (fabric)** | The stable substrate that provides secure, outbound‑only connectivity and multiplexed tunnels between endpoints. |
| **Control plane** | Authentication, session creation, token issuance, device registry/metadata, and other “decisions” (not bulk data transport). |
| **Data plane** | The high‑volume tunneled bytes (e.g., an RDP stream) flowing between helper↔agent via the Hub. |
| **Ephemeral port / localhost binding** | A temporary `127.0.0.1:<port>` listener created by the helper so native clients can connect without installing anything system‑wide. |
| **Fingerprint (certificate fingerprint)** | A short hash derived from the Hub’s TLS certificate (or public key) used for pinning; must match the expected value exactly. |
| **Frame** | One protocol unit on the wire: a fixed header plus a payload (control JSON, TCP data, or heartbeat). |
| **Helper** | A short‑lived user‑mode binary that connects outbound to the Hub, authenticates with a session token, binds `localhost:<port>`, and forwards TCP traffic. |
| **Hub** | The central relay/coordinator that accepts agent and helper connections, brokers sessions, allocates channels, and routes frames between parties. |
| **HTTP** | Hypertext Transfer Protocol; commonly tunneled to validate connectivity (e.g., a static page). |
| **Locked‑down environment** | A network or workstation that restricts inbound ports, VPNs, and/or installation, but still allows outbound HTTPS/WebSocket traffic. |
| **Mesh** | A connected set of agents and sessions forming a network of reachable services through the Hub (not necessarily peer‑to‑peer). |
| **MITM (man‑in‑the‑middle)** | An attacker (or intercepting proxy) that attempts to observe/modify traffic between endpoints by inserting itself between client and server. |
| **Multiplexing** | Carrying multiple channels (control + multiple TCP streams) over a single WebSocket connection using a framing header and channel IDs. |
| **NAT** | Network Address Translation; a common reason inbound connections to a machine are not possible without port forwarding. |
| **Orchestration layer** | The browser‑based workflow that creates sessions and provides the helper download/run instructions; it does not forward tunneled TCP bytes. |
| **Outbound‑only** | Agents/helpers initiate connections out to the Hub; no inbound ports are required on managed machines. |
| **RDP** | Microsoft Remote Desktop Protocol (typically TCP/3389); a primary example of a tunneled service. |
| **SSH** | Secure Shell (typically TCP/22); commonly used for remote terminals and file transfer (SCP/SFTP). |
| **SFTP** | SSH File Transfer Protocol; a file transfer protocol that runs over SSH. |
| **Session** | A bounded period where a helper is authorized to connect and proxy traffic to a specific agent/service using a short‑lived token. |
| **Session token** | A short‑lived, single‑use credential issued by the Hub that allows a helper (not an agent) to authenticate for a specific session. |
| **Service** | A TCP endpoint on the agent machine that Tela can proxy (e.g., `127.0.0.1:3389` for RDP, `127.0.0.1:22` for SSH). |
| **TCP** | Transmission Control Protocol; Tela tunnels TCP streams (RDP/SSH/HTTP/etc.) without needing to understand the application protocol. |
| **TLS** | Transport Layer Security; provides encrypted and authenticated transport. Tela uses TLS 1.3 for Hub connections. |
| **Tunnel** | The end‑to‑end forwarding path `client → helper → Hub → agent → service` that makes a remote service appear local. |
| **VNC** | Virtual Network Computing; another example of a TCP service that can be tunneled. |
| **WebSocket (WS/WSS)** | A persistent full‑duplex connection over HTTP(S). Tela uses `wss://` for secure transport once deployed. |

# **1. Identity & Positioning**

## **1.1 Name**

**Tela** — Filipino for *fabric*.
Represents a woven mesh of nodes, tunnels, and services.
Short, global‑friendly, SEA‑rooted, and brandable.

## **1.2 Positioning**

Tela is the **engine**. Awan Satu is the **platform**.

- **Tela : Awan Satu :: Docker : Kubernetes**
- **Tela : Awan Satu :: WireGuard : Tailscale**
- **Tela : Awan Satu :: git : GitHub**

Tela is the runtime, the connectivity substrate, the stable, boring, long‑lived layer.
Awan Satu is the orchestrator, the identity and policy layer, the multi‑service cloud platform.

Tela must remain small, stable, and protocol‑frozen so Awan Satu can evolve rapidly above it.

## **1.3 What Tela Is**

Tela is a **connectivity fabric**, not a remote desktop tool.
Remote desktop is simply the first module that runs on the fabric.

Tela's core value proposition: **access any machine or service from any locked‑down environment with zero installation** — via outbound‑only agents, browser orchestration, a helper binary for local TCP binding, multiplexed WebSocket channels, and protocol‑agnostic tunneling. This combination does not exist in any current tool.
## **1.4 License**

Tela is released under the **Apache License 2.0**.

Apache 2.0 is chosen because:

- Permissive enough to encourage adoption and contribution.
- Includes an explicit patent grant (protects contributors and users).
- Compatible with Awan Satu being a proprietary or differently‑licensed commercial layer.
- Widely understood in the FOSS ecosystem.

**All contributions to the Tela project must be licensed under Apache 2.0.** The Awan Satu platform may use a different license.

---

# **2. Goals & Non‑Goals**

## **2.1 Goals**

Tela must:

- Provide **zero‑install client access** for RDP, SSH, SFTP, VNC, HTTP, and arbitrary TCP services.
- Use **outbound‑only connectivity** from agents (no inbound ports, no VPN).
- Support **Windows, Linux, and macOS** agents.
- Provide a **browser‑based orchestration layer**.
- Provide a **helper binary** that exposes ephemeral `localhost:<port>` endpoints.
- Support **protocol‑agnostic multiplexed TCP channels**.
- Maintain **long‑term backward compatibility**.
- Use **minimal dependencies** and "boring tech."
- Serve as the **substrate for Awan Satu**.

## **2.2 Non‑Goals**

Tela must not:

- Implement dashboards, RBAC, or identity providers (Awan Satu's domain).
- Become a monolithic remote‑desktop suite.
- Adopt trendy frameworks or complex build systems.
- Introduce microservices.
- Allow protocol churn or dependency creep.
- Become a general cloud platform (Tela is the substrate, not the platform).

---

# **3. Design Philosophy & Invariants**

## **3.1 Stability First**

Tela must be stable enough to run for 10+ years with minimal changes: frozen protocol structures, additive‑only control messages, strict backward compatibility, minimal dependencies, predictable behavior.

## **3.2 Boring Technology**

C/C++ for the agent, Node.js LTS for the Hub, vanilla JS for the browser, Go for the CLI. These choices minimize churn and maximize longevity.

## **3.3 Agent‑Centric Development**

Tela assumes LLM agents will contribute code. Literate coding is mandatory, invariants must be explicit, "DO NOT MODIFY" markers must be used, concurrency models must be locked down, and protocol structures must be immutable.

## **3.4 Guiding Invariants**

Tela must always remain:

- outbound‑only
- protocol‑agnostic
- dependency‑minimal
- backward‑compatible
- agent‑centric
- browser‑accessible
- platform‑neutral
- stable

These invariants shape every architectural decision.

---

# **4. Architecture Overview**

## **4.1 Components**

- **Tela Agent** — C/C++ single static binary. Runs on managed machines.
- **Tela Hub** — Node.js LTS server, MeshCentral‑derived. Central coordination point.
- **Tela Web** — Vanilla JS browser UI. Orchestration only.
- **Tela Helper** — Tiny signed Go binary. Binds localhost, forwards TCP.
- **Tela CLI** — Go static binary. Administrative tool.
- **Awan Satu** — Future control plane. Not part of Tela core.

## **4.2 Data Flow**

### Agent Connection

1. Agent opens outbound TLS+WebSocket to Hub.
2. Agent registers identity and capabilities.
3. Agent maintains heartbeats and reconnect logic.

### Browser Orchestration

1. User logs into Tela Web.
2. User selects a machine.
3. Browser requests a session token from Hub.
4. Browser provides helper download and a run command with pre‑filled arguments (or triggers `tela://` URI if registered).
5. User runs the helper.

### Helper Connection

1. Helper validates Hub certificate fingerprint.
2. Helper opens its own WebSocket to Hub.
3. Helper authenticates using the session token.
4. Helper binds `localhost:<port>`.
5. Native client (mstsc/ssh/etc.) connects to that port.
6. Helper forwards TCP → Hub → Agent → target service.

### Multiplexed Channels

All traffic flows through a single WebSocket per endpoint (Agent ↔ Hub, Helper ↔ Hub). Each WebSocket carries a control channel, heartbeat channel, multiple TCP data channels, and optional file transfer channels.

## **4.3 Component Interaction Model**

Tela uses a **three‑party interaction model**:

| Path | Purpose |
|------|---------|
| **Browser → Hub** | Auth, session creation, token issuance, UI |
| **Helper → Hub** | TCP forwarding, session token auth, local binding |
| **Agent → Hub** | Service proxying, capability reporting, heartbeats |

**The browser never carries data traffic. It only orchestrates.**
Once the helper is launched, the browser can close.

---

# **5. MeshCentral Integration Boundary**

MeshCentral is a mature, stable, widely deployed remote‑access system with a proven agent transport layer. Tela builds on selected MeshCentral components to avoid reinventing complex, battle‑tested functionality.

This section defines exactly what Tela reuses, what Tela replaces, and why — so the Hub does not become an accidental fork.

## **5.1 Components Reused**

Tela reuses only parts that are stable, protocol‑agnostic, dependency‑minimal, well‑tested, and unlikely to change:

- **WebSocket multiplexing engine** — channel framing, lifecycle, binary data handling, flow control. Tela layers its own protocol on top.
- **Agent connection lifecycle** — persistent WebSocket logic, reconnect, heartbeat, basic registration.
- **Minimal device registry logic** — agent ID storage, basic metadata, online/offline tracking. Not the full device management model.
- **Core agent transport code** — socket abstraction, platform‑specific networking, TLS handling, WebSocket framing. Everything above the transport layer is replaced.

## **5.2 Components Replaced**

Tela replaces all MeshCentral components that are UI‑heavy, RDP‑specific, opinionated, or tied to MeshCentral's identity/device management model:

- **Entire Web UI** — Tela Web is new, minimal, vanilla JS.
- **Browser TCP bridge** — MeshCentral does not have a local‑client TCP bridge. Tela's helper architecture is original.
- **Local‑client ephemeral port mechanism** — unique to Tela.
- **Service exposure API** — MeshCentral is RDP‑centric; Tela exposes arbitrary TCP services.
- **Metadata & tagging system** — new, lightweight model.
- **Authentication model** — Tela uses local auth (standalone) or SSO (Awan Satu).
- **Protocol specification** — Tela freezes v1 for long‑term stability; MeshCentral evolves faster.

## **5.3 Rationale**

MeshCentral solves the hardest problems — cross‑platform C/C++ agent, stable WebSocket transport, NAT traversal, multiplexing, reconnect logic, TLS handling — and is modular enough that Tela can reuse the transport layer while replacing the UI, control plane, identity model, and service model without forking the entire project.

---

# **6. Protocol Specification**

This section is **authoritative**. All implementations must follow it exactly.

## **6.1 Transport**

- TLS 1.3
- WebSocket (wss://)
- Binary frames only
- No compression (avoids complexity and side‑channel attacks)

WebSocket is chosen because it works through corporate proxies, locked‑down networks, and Cloudflare Tunnel, and is supported by all browsers and MeshCentral's transport layer.

**Compression caveat:** Disabling compression means all tunneled data traverses the wire at full size. For bandwidth‑heavy protocols like RDP, this is acceptable because RDP applies its own compression internally. For other protocols, the bandwidth cost of no compression is the explicit tradeoff for simplicity and side‑channel resistance. If this becomes a bottleneck in production, compression may be revisited as an opt‑in feature in a future phase, applied only to the data payload (never to headers or control messages).

## **6.2 Multiplexing**

One WebSocket, many logical channels. Each channel carries a single TCP stream or control stream.

### Channel Types

- Control
- Heartbeat
- TCP data
- File transfer (Phase 2)
- Metrics/logs (Phase 3)

### Channel Lifecycle

1. Hub allocates channel ID.
2. Hub sends `open` control message.
3. Agent or Helper acknowledges.
4. Data flows.
5. Either side sends `close`.
6. Channel is freed.

## **6.3 Frame Format (Immutable)**

This is the **frozen wire format**. It must never be changed in a breaking way.

```c
/*
  WARNING: DO NOT MODIFY THIS STRUCT.
  This struct is serialized over the wire.
  Adding, removing, or reordering fields breaks protocol compatibility.

  Wire format rules:
    - All multi-byte fields are BIG-ENDIAN (network byte order).
    - Packed, no padding between fields.
    - Total header size: 12 bytes.
*/
#pragma pack(push, 1)
typedef struct {
    uint8_t  version;        // Protocol version (must remain 1 for v1)
    uint32_t session_id;     // Unique per session
    uint16_t channel_id;     // Multiplexed stream ID
    uint8_t  type;           // Frame type (control, data, heartbeat)
    uint32_t payload_length; // Length of payload following this header, in bytes
} tela_frame_header_t;
#pragma pack(pop)
```

### Frame Types

- `0x01` — Control
- `0x02` — Data
- `0x03` — Heartbeat

### Wire Encoding Rules

- All multi-byte integers (`session_id`, `channel_id`, `payload_length`) are serialized in **big-endian** (network byte order).
- The header is **packed** with no alignment padding. Total on-wire size: **12 bytes**.
- `payload_length` specifies the exact number of bytes following the header. The receiver must read exactly this many bytes before parsing the next frame.

## **6.4 Control Messages**

JSON, additive‑only, tolerant of unknown fields.

### Agent Handshake

Agent → Hub:
```json
{ "type": "hello", "agentId": "abc123", "version": 1, "publicKey": "..." }
```

Hub → Agent:
```json
{ "type": "welcome", "hubId": "hub01" }
```

Agent → Hub:
```json
{ "type": "ready" }
```

Agent authentication uses Ed25519 signatures (see §12.1). No session token is involved.

### Helper Handshake

Helper → Hub:
```json
{ "type": "hello", "sessionToken": "xyz789", "version": 1 }
```

Hub → Helper:
```json
{ "type": "welcome", "assignedPort": 0 }
```

Helper → Hub:
```json
{ "type": "ready" }
```

Helper authentication uses the single‑use session token issued by the browser flow (see §6.5). The token is invalidated immediately after validation.

### Required Messages

`hello`, `welcome`, `ready`, `open`, `close`, `error`, `heartbeat`

### Optional Messages

`capabilities`, `metadata`, `serviceList`, `fileTransferInit` (Phase 2)

## **6.5 Session Tokens**

Session tokens are short‑lived, single‑use JWTs (HS256 or EdDSA) containing: user ID, agent ID, expiration, nonce, permissions.

### Token Lifecycle

1. Browser requests token from Hub.
2. Hub validates user session and issues signed token.
3. Browser provides token to the helper (via command‑line arguments or `tela://` URI).
4. Helper connects to Hub with token.
5. Hub validates token.
6. Token is invalidated immediately.

## **6.6 Backward Compatibility**

### Hard Rules

- No field removals.
- No field renames.
- No reordering of struct fields.
- No change to binary frame header.
- No change to handshake sequence.
- No change to channel lifecycle.

### Additive Changes (Allowed)

New control message types, new optional fields, new channel types, new capabilities.

### Deprecation Policy

- 18‑month support window for old agents.
- Hub must support multiple protocol versions.
- Agents must ignore unknown fields.

## **6.7 Error Handling**

### Error Types

`protocolError`, `authError`, `channelError`, `internalError`

### Behavior

- Errors must not close the WebSocket unless fatal.
- Channels may be closed individually.
- Hub logs all errors.

---

# **7. Tela Agent**

The agent is the core runtime component that runs on every managed machine.

## **7.1 Responsibilities**

- Open and maintain outbound TLS+WebSocket connection to Hub.
- Reconnect automatically; validate Hub certificate fingerprint; maintain heartbeats.
- Participate in session establishment; open channels on Hub request.
- Proxy TCP traffic to local services (RDP on 3389, SSH on 22, etc.).
- Discover local services (scan common ports or read local config).
- Enforce policy (which services may be exposed, which ports may be forwarded).
- Report metadata (OS, hostname, uptime, capabilities, tags).

## **7.2 Implementation**

- **Language:** C/C++
- **Build:** Single static binary, no dynamic linking.
- **Dependencies:** OpenSSL or mbedTLS (TLS), libuv (event loop). Nothing else.
- **Platforms:** Windows, Linux (primary); macOS (Phase 2).

## **7.3 Concurrency Model**

The agent uses a **strict, invariant concurrency model**.

### Event Loop

**libuv** event loop handles: WebSocket I/O, control messages, channel multiplexing, heartbeat scheduling, reconnection logic.

### Worker Threads

Used **only** for CPU‑bound work: optional compression (future), optional encryption (future).

All I/O — including TCP socket read/write — is handled by the libuv event loop, not by worker threads. libuv is designed for non‑blocking async I/O; duplicating TCP handling in worker threads would introduce unnecessary complexity and race conditions.

### Concurrency Invariants

These rules must never be violated:

- No async/await abstractions.
- No thread‑per‑connection model.
- No lock‑heavy designs.
- No refactoring to promises/futures.
- No changes to libuv event loop structure.
- No new concurrency primitives without human approval.

## **7.4 Configuration**

A simple, static config file (`tela.conf`) containing: Hub URL, Hub certificate fingerprint, agent ID, allowed services, optional tags.

The agent must not fetch remote configs, auto‑update configs, or modify its own config. All changes require human action.

## **7.5 Logging**

Connection events, errors, channel lifecycle events, service access events. Local only, plaintext, rotated automatically. No remote log upload in Tela core.

## **7.6 Updates**

Manual, explicit, version‑pinned. Awan Satu may later introduce remote updates and staged rollouts. Tela core remains manual.

---

# **8. Tela Hub**

The Hub is the central coordination point. It is intentionally lightweight and minimal.

## **8.1 Responsibilities**

- Accept agent and helper connections.
- Authenticate users (local auth in Tela standalone).
- Issue and validate session tokens.
- Broker sessions; allocate channels; manage channel lifecycle.
- Multiplex and route traffic between helper and agent.
- Maintain basic device metadata.
- Expose a minimal REST API.

The Hub is **not** an identity provider, dashboard engine, policy engine, orchestration system, or multi‑tenant platform. Those belong to Awan Satu.

## **8.2 Implementation**

- **Language:** Node.js LTS
- **Base:** MeshCentral's transport and multiplexing core.
- **Dependency constraints:** No frontend frameworks, no ORMs, no complex dependency trees, no microservices, no build systems.

## **8.3 Storage**

### SQLite (Tela Standalone)

Users, agent registry, metadata, session logs. Simple, reliable, dependency‑minimal, perfect for single‑node deployments.

### Postgres (Phase 3+)

Multi‑node deployments, HA, multi‑tenant environments. Required for HA Hub (Phase 3) before Awan Satu exists. Tela’s single‑node standalone mode remains SQLite‑based.

## **8.4 REST API**

- `/api/v1/login`
- `/api/v1/machines`
- `/api/v1/session`
- `/api/v1/token`

Additive‑only changes. 12‑month deprecation window.

## **8.5 Multiplexing**

Uses MeshCentral's multiplexing engine with Tela's protocol layered on top. Allocates channel IDs, routes frames, enforces channel lifecycle, detects errors, closes channels cleanly. Channel routing: Helper ↔ Hub ↔ Agent. No browser involvement in data path.

## **8.6 Logging & Observability**

Agent connections, helper connections, authentication events, session creation, channel lifecycle, errors. Future metrics channels (Phase 3): active sessions, channel counts, bandwidth usage, agent health.

## **8.7 Updates**

Manual, explicit, version‑pinned. Awan Satu may later introduce rolling updates and multi‑node orchestration.

---

# **9. Tela Web**

## **9.1 Responsibilities**

The browser UI performs **orchestration only**:

- Authenticate user.
- Display machine list.
- Request session token from Hub.
- Provide a one‑click download button for the helper binary.
- Display clear instructions for the user to run the helper manually.
- Pass connection parameters (Hub URL, session token, target agent ID, target service) via:
  - command‑line arguments shown to the user, or
  - a `tela://` custom URI scheme if the helper has been registered (optional, not required).
- Display ephemeral `localhost:<port>` once the helper reports back via a short‑lived polling endpoint or WebSocket.
- Optionally provide in‑browser fallback clients (SSH/RDP/terminal).

## **9.2 Non‑Responsibilities**

The browser must **never**: proxy TCP traffic, hold open a data channel, participate in multiplexing, or remain open for the session to continue. Once the helper is running, the browser can close.

## **9.3 Helper Launch Reality**

Browsers **cannot launch executables directly**. The primary UX is:

1. User clicks "Connect" in Tela Web.
2. Browser downloads the helper binary (or uses a cached copy).
3. Browser displays: a run command with pre‑filled arguments, or triggers via `tela://` URI scheme if registered.
4. User executes the helper.
5. Helper binds `localhost:<port>` and reports the port back to the browser page.
6. Browser displays the port; user connects with their native client.

This is honest and reliable. The `tela://` URI scheme is a convenience optimization, not a requirement. First‑time users download and run manually. Repeat users may register the URI scheme for one‑click launch.

## **9.3 Constraints**

No frameworks. No build tools. No dependency creep. Vanilla JS only.

---

# **10. Tela Helper**

The helper is the key to Tela's zero‑install local‑client access. Implemented in **Go** (same toolchain as Tela CLI; produces small static binaries with no runtime dependencies).

## **10.1 Responsibilities**

- Validate Hub certificate fingerprint.
- Open its own outbound TLS+WebSocket connection to Hub.
- Authenticate using the session token.
- Bind `localhost:<port>`.
- Accept TCP connections from native clients (mstsc, ssh, sftp, vnc, etc.).
- Forward TCP → WebSocket → Hub → Agent.
- Handle channel lifecycle.
- Self‑terminate when session ends.
- Best‑effort cleanup after exit (see §10.3 Execution Model for platform caveats).

## **10.2 Non‑Responsibilities**

The helper must **not**: store credentials, store configuration, persist any state, modify system settings, require admin rights, install itself, run as a service, or write to protected directories. It must be a pure user‑mode, ephemeral process.

## **10.3 Distribution & Signing**

### Source

Served directly by the Tela Hub over HTTPS:

- `/helper/windows/tela-helper.exe`
- `/helper/linux/tela-helper`
- `/helper/macos/tela-helper`

### Version Matching

The helper must be version‑matched to the Hub. Hub v1.0 only serves helper v1.0. This prevents protocol mismatches.

### Code Signing

- **Windows:** Authenticode signature.
- **macOS:** Notarized and signed with Developer ID.
- **Linux:** Detached GPG signature.

Unsigned helpers will trigger SmartScreen/Gatekeeper blocks.

### Execution Model

Downloaded to a temporary directory, executed directly, receives arguments from the browser or CLI.

**Cleanup:** On exit, the helper attempts to delete its own binary. On Linux/macOS this is straightforward. On Windows, a running process cannot delete its own executable; the helper uses a best‑effort deferred‑delete mechanism (e.g., a short‑lived batch wrapper or `MoveFileEx` with `MOVEFILE_DELAY_UNTIL_REBOOT`). Cleanup failure is non‑fatal — stale helper binaries in temp directories are harmless.

## **10.4 Data Path**

```
native client → helper → Hub → Agent → target service
```

The browser is not in the data path. It only requests the session token, launches the helper, and displays the port.

## **10.5 Fallback Modes**

If the helper cannot execute (locked‑down environment blocks downloads or execution):

- Browser launches in‑browser RDP/SSH client via WebRTC DataChannel + WebAssembly.
- Performance is lower; no local‑client mode.
- User is informed that local‑client mode is unavailable.

---

# **11. Tela CLI**

## **11.1 Purpose & Rationale**

A Go static binary for administrative and automation workflows.

Go is chosen because: static binaries, instant cross‑compilation, no runtime dependencies, excellent for small utilities, and — critically — avoids coupling the CLI to either the Hub (Node.js) or Agent (C/C++) codebases.

The CLI is intentionally not part of the core runtime.

## **11.2 Core Commands**

Phase 1:

- `tela login` — authenticate with a Hub, store session cookie.
- `tela machines` — list registered machines and their online/offline status.
- `tela connect <machineId> <service>` — request a session token, launch the helper, and display the assigned `localhost:<port>`.
- `tela status` — show current Hub connection and active sessions.

Phase 2+:

- `tela services <machineId>` — list exposed services on a machine.
- `tela transfer <machineId> <localPath> <remotePath>` — file transfer (Phase 2).
- `tela config` — manage CLI configuration (Hub URL, stored credentials).
- `tela helper` — run the helper directly (for scripted/headless workflows).

The CLI must remain additive‑only. No subcommand removals or renames after release.

---

# **12. Security Model**

Tela's security model is intentionally simple, strong, and long‑lived. This section is the **unified security reference** across agent, helper, and Hub.

## **12.1 Identity**

### Agent Identity

- Each agent generates a long‑lived **Ed25519 keypair**.
- Private key stored locally.
- Public key registered with Hub.
- Used for agent authentication.

### Helper Identity

- Authenticated via session token.
- No persistent identity.
- No long‑term keys.

### Hub Identity

- TLS certificate.
- Fingerprint pinned by agent and helper.

## **12.2 Certificate Pinning**

Both agent and helper must: validate Hub certificate fingerprint, refuse to connect if mismatched, log all failures. This prevents MITM attacks even if TLS infrastructure is compromised.

## **12.3 Session Tokens**

Short‑lived, single‑use, signed by Hub, passed from browser → helper, validated by Hub, invalidated immediately after use. See §6.5 for full specification.

## **12.4 Transport Security**

All traffic encrypted via TLS 1.3 over WebSocket.

## **12.5 E2E Encryption**

Optional E2E encryption via WebCrypto may be added in **Phase 2** for untrusted Hub environments and multi‑tenant Awan Satu deployments. **Not part of the MVP.**

## **12.6 Threat Model**

### Protects Against

- **MITM** — TLS 1.3 + certificate pinning prevents interception by third parties.
- **Replay attacks** — single‑use, short‑lived session tokens with nonces.
- **Unregistered agents** — only agents with Ed25519 keys registered at the Hub can connect. Does *not* protect against a registered agent whose key has been stolen.
- **Forged certificates** — certificate pinning rejects certificates from a different key. Does *not* protect against compromise of the Hub's actual private key.
- **Compromised networks** — all traffic is encrypted end‑to‑hop via TLS.

### Fails Closed In Presence Of

- **TLS‑intercepting corporate proxies** — certificate pinning will cause connection failure (correct security behavior). Users in these environments must use the in‑browser fallback.

### Does Not Protect Against

- Compromised endpoint machines.
- Compromised user accounts (credential theft).
- Agents with stolen registered keys.
- Hub private key compromise.
- Local malware on either end.
- Physical access attacks.

---

# **13. Authentication**

## **13.1 Tela Standalone (Before Awan Satu)**

Tela Hub includes a minimal local authentication system:

- bcrypt‑hashed passwords
- SQLite storage
- Secure HTTP‑only cookies
- Optional TOTP

Intentionally simple and self‑contained. No external dependencies.

## **13.2 Awan Satu (Future)**

Awan Satu replaces local auth with: Cloudflare Access, OIDC, SAML, enterprise identity providers. Tela Hub becomes a **resource server**, not an identity provider.

---

# **14. End‑to‑End Usage Flow**

## **14.1 Setup (Tela Standalone)**

1. Deploy Tela Hub on a VPS.
2. Put it behind Cloudflare Tunnel.
3. Create local Tela user.
4. Install Tela Agent on target machines.
5. Register agents using provisioning tokens.

## **14.2 Accessing a Machine from a Locked‑Down Laptop**

1. User opens browser → `https://tela.yourdomain.com`.
2. User logs in.
3. User selects a machine.
4. Browser requests session token from Hub.
5. Browser downloads helper binary (or uses cached copy).
6. User runs the helper with the provided command / arguments (or clicks `tela://` URI if registered).
7. Helper validates Hub certificate.
8. Helper opens WebSocket to Hub.
9. Helper binds `localhost:<port>`.
10. User runs `mstsc /v:localhost:<port>` or `ssh localhost -p <port>`.
11. Traffic flows: `client → helper → Hub → Agent → target service`.

Browser can close once the helper is running. No VPN. No admin rights.

## **14.3 In‑Browser Fallback**

If the helper cannot run, the browser launches an in‑browser RDP/SSH client. Performance is lower. No local‑client mode.

---

# **15. Literate Coding Standards**

Tela requires **literate coding** to ensure clarity, maintainability, safe LLM contributions, explicit invariants, and long‑term stability. These standards apply to **all** Tela components.

## **15.1 Narrative Header Blocks**

Every source file must begin with a narrative header describing: purpose, architectural role, invariants, constraints, security boundaries, backward‑compatibility requirements, and warnings for LLM agents.

```c
/*
  tela_agent_connection.c
  Purpose: Manages outbound TLS+WebSocket connection to Tela Hub.

  Invariants:
    - Must use libuv event loop.
    - Must not introduce new concurrency primitives.
    - Must not modify handshake sequence.
    - Must validate Hub certificate fingerprint.

  Security:
    - Handles agent identity and TLS pinning.
    - Any change requires human review.

  WARNING:
    DO NOT MODIFY handshake logic or frame header.
*/
```

## **15.2 Inline Intent Comments**

Comments must explain **why**, not just **what**.

```c
// We use a single write buffer here to avoid partial frame writes,
// which would break channel framing and cause desync.
```

## **15.3 Embedded Protocol Excerpts**

Files that implement protocol logic must embed relevant protocol excerpts so LLM agents cannot drift:

```c
// Protocol v1 frame header (DO NOT MODIFY):
// #pragma pack(push, 1)
// typedef struct {
//     uint8_t  version;
//     uint32_t session_id;
//     uint16_t channel_id;
//     uint8_t  type;
//     uint32_t payload_length;
// } tela_frame_header_t;
// #pragma pack(pop)
// Wire: big-endian, packed, 12 bytes total.
```

## **15.4 "DO NOT MODIFY" Markers**

Used for public APIs, protocol structures, security‑critical code, and concurrency primitives:

```c
// WARNING: DO NOT MODIFY THIS FUNCTION.
// Called by Hub during session establishment.
// Changing behavior breaks backward compatibility.
```

## **15.5 Explicit Invariants**

Every file must list invariants that LLM agents must not violate:

```c
// Invariants:
// - Must run on libuv loop thread.
// - Must not block.
// - Must not allocate large buffers on stack.
// - Must not introduce recursion.
```

## **15.6 No Hidden Magic**

All behavior must be explicit. No implicit state machines. No "clever" code.

## **15.7 Language‑Specific Conventions**

The examples above use C. Equivalent standards apply to all Tela languages:

### Node.js / JavaScript (Hub, Web)

Use JSDoc block comments at the top of every file:

```js
/**
 * tela_session_broker.js
 * Purpose: Allocates channels and routes traffic between helper and agent.
 *
 * Invariants:
 *   - Must not introduce external dependencies.
 *   - Must not modify channel lifecycle sequence.
 *   - Must not change REST API response shapes.
 *
 * Security:
 *   - Validates session tokens. Any change requires human review.
 *
 * WARNING: DO NOT MODIFY token validation logic.
 */
```

### Go (CLI, Helper)

Use Go doc comments at the package and function level:

```go
// Package helper implements the Tela local TCP bridge.
//
// Invariants:
//   - Must validate Hub certificate fingerprint before connecting.
//   - Must not persist any state to disk.
//   - Must not require admin/root privileges.
//
// WARNING: DO NOT MODIFY the session token validation flow.
package helper
```

---

# **16. LLM Agent Guardrails**

Tela assumes LLM agents will contribute code. Strict guardrails are required.

## **16.1 Hard Rules**

LLM agents must **not**:

- Modify protocol structs.
- Rename public functions.
- Change handshake sequences.
- Introduce new dependencies.
- Refactor concurrency models.
- Alter security logic.
- Modify file layout.
- Change naming conventions.
- Remove warnings or invariants.

## **16.2 Allowed Changes**

LLM agents may:

- Add new optional control messages.
- Add new channel types.
- Add new helper features.
- Add new metadata fields.
- Add new CLI commands.
- Add new documentation.
- Fix bugs in non‑frozen, non‑security‑critical code.
- Improve comments.

All changes must be **additive** and **non‑breaking**.

## **16.3 Human Review Requirements**

Human maintainers must review:

- Protocol changes.
- Security logic.
- Concurrency logic.
- Public API changes.
- Helper distribution logic.
- Certificate pinning logic.

---

# **17. Roadmap**

## **17.1 Phase 1 — Minimum Viable Fabric (0–3 months)**

This phase delivers the core Tela substrate. The 3‑month timeline is feasible because core transport, multiplexing, agent lifecycle, and reconnect logic are inherited from MeshCentral (see §5). Net‑new work is marked with †.

- Outbound‑only agent (Windows + Linux) — MeshCentral‑derived transport + Tela protocol layer†
- Multiplexed WebSocket channels — MeshCentral‑derived
- Session broker†
- Browser orchestration†
- Helper binary (signed, ephemeral, direct‑to‑Hub)†
- Local‑client mode (RDP/SSH/SFTP/VNC via localhost)†
- Minimal Tela Web UI†
- Minimal Tela CLI†
- SQLite‑backed Hub†
- Certificate pinning
- Session tokens†
- Protocol conformance test suite†

## **17.2 Phase 2 — Fabric Extensions (3–6 months)**

- macOS agent
- Metadata + tagging
- Service exposure API
- File transfer channels
- In‑browser RDP/SSH fallback
- Helper improvements (auto‑cleanup, better UX)
- Capability discovery
- Improved logging
- E2E encryption (optional)

## **17.3 Phase 3 — Substrate for Awan Satu (6–12 months)**

- Postgres backend
- HA Hub
- Multi‑node Hub clustering
- Metrics/logs channels
- Node grouping
- Service catalogs (read‑only)

## **17.4 Phase 4 — Awan Satu v0.1 (12–18 months)**

- Cloudflare Access SSO
- RBAC
- Provisioning tokens
- Machine inventory
- Session initiation
- Multi‑tenant isolation
- Billing foundations

Tela remains stable and unchanged except for additive features.

---

# **18. Risks & Mitigations**

## **18.1 Agentic Coding Risks**

- **Protocol drift** → mitigated by literate coding, embedded protocol excerpts, DO NOT MODIFY markers.
- **Security regressions** → mitigated by human review requirements, explicit threat model.
- **Concurrency bugs** → mitigated by strict invariants, locked concurrency model.
- **Dependency creep** → mitigated by hard rules, boring tech philosophy.

## **18.2 Platform Risks**

- **Scope creep** → mitigated by Tela/Awan Satu separation.
- **Identity complexity** → mitigated by deferring to Cloudflare Access.
- **MeshCentral drift** → mitigated by explicit integration boundary (§5).
- **Helper execution blocks** → mitigated by in‑browser fallback (§10.5).

---

# **19. Testing Strategy**

Tela’s emphasis on stability, backward compatibility, and 18‑month support windows requires a structured testing approach.

## **19.1 Protocol Conformance Tests**

A dedicated test suite must validate:

- Frame serialization/deserialization (byte order, packing, payload length).
- Handshake sequences (agent and helper variants).
- Channel lifecycle (open, data, close, error).
- Unknown‑field tolerance in control messages.
- Backward compatibility with older protocol versions.

These tests are **mandatory before any release** and must run against every supported platform.

## **19.2 Integration Tests**

- Agent → Hub → Helper end‑to‑end tunnel establishment.
- Session token lifecycle (issue, use, invalidate).
- Certificate pinning validation (accept correct, reject wrong).
- Reconnect/heartbeat behavior.
- Channel multiplexing under load.

## **19.3 Regression Suite**

- Every bug fix must include a regression test.
- Every protocol change must include conformance tests for the new behavior.
- Tests must cover Windows and Linux at minimum; macOS added in Phase 2.

## **19.4 What Is Not Tested in Tela Core**

- UI testing (Tela Web is minimal; manual verification is acceptable for Phase 1).
- Performance benchmarking (deferred to Phase 3).
- Multi‑tenant isolation (Awan Satu’s domain).


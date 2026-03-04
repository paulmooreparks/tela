# Tela / Awan Satu — Local Implementation & Deployment Runbook (POC)

This document is a **practical runbook** for implementing and running Tela locally, exposing a Hub for internet access, and starting a POC path toward Awan Satu.

It complements the authoritative specification in `DESIGN.md`.

## Scope (what this runbook covers)

- Running a Hub as a container on a workstation/home server.
- Exposing the Hub securely over **`wss://`**.
- Two ingress modes:
  - **Direct public exposure** (port forward + DNS)
  - **Cloudflare Tunnel** (no inbound ports)
- How to keep Cloudflare **optional** (not required by architecture).
- Key security implications (especially **certificate pinning** vs TLS termination).

## Non-goals (what this runbook does not cover)

- Full Tela Hub implementation details (auth, tokens, multiplex framing).
- Full Awan Satu features (RBAC, policy, SSO, dashboards).
- High availability / multi-hub clustering.

---

## 1) Mental model: what must be reachable

Even though agents/helpers are **outbound-only**, they must connect to something reachable.

- The Hub (or the TLS endpoint in front of it) must be reachable at a stable address:
  - `wss://tela.yourdomain.example/...`
  - usually on TCP **443**

A simple reference topology:

```
Native client ─▶ tela (localhost listener) ──wss──▶ hub ◀──ws── telad ─▶ local service
                  │ WireGuard L3 tunnel (encrypted, gVisor netstack)  │
                  └───── 10.77.0.2/24 ◀──────────────────────────▶ 10.77.0.1/24 ─┘
```

A practical rule:

- Expose **one** thing publicly: **TCP 443** to a TLS endpoint.
- Everything else stays internal.

---

## 2) Recommended container shape (Cloudflare optional)

To keep Cloudflare optional, keep your internal deployment consistent and swap only the ingress method.

**Current setup (3 containers):**

- `hub` — Node.js application, listens on `:8080` (HTTP/WS) and `:41820` (UDP relay).
- `caddy` — reverse proxy with auto TLS (DNS-01 via Cloudflare API), terminates TLS on `:443`.
- `telad` — Go WireGuard agent, connects to hub over internal `ws://hub:8080`.
- optional: `cloudflared` points at `caddy` for tunnel ingress.

This yields:

- Direct mode: Internet → `caddy:443` (via port-forward) → `hub:8080`
- Tunnel mode: Internet → Cloudflare → `cloudflared` → `caddy:443` → `hub:8080`

---

## 3) TLS (`wss://`) and reverse proxy choices

### Option A: Caddy (recommended for “boring simple” TLS)

Pros:
- Very small config.
- Automatic Let’s Encrypt.

Cons:
- Requires inbound reachability for HTTP-01/ALPN challenges (direct mode) unless you use DNS-based issuance.

### Option B: Nginx

Pros:
- Common and well understood.

Cons:
- More manual certificate management.

### Option C: Terminate TLS at Cloudflare only

Pros:
- Easy in tunnel mode.

Cons:
- Creates a hard interaction with strict certificate pinning (see §6).

---

## 4) Ingress mode 1 — Direct public exposure (no tunnel)

This is the most Cloudflare-independent option.

### Requirements

- You control your router/firewall.
- You have a public IPv4 (or IPv6) address with inbound connectivity.
- You can forward TCP 443 to the workstation/server.

### Steps (high level)

1. Choose a hostname, e.g. `tela.example.com`.
2. Point DNS at your public IP.
   - If the IP can change, use dynamic DNS.
3. Port-forward **TCP 443** on your router -> the machine running the reverse proxy.
4. Run the reverse proxy container on the host, publishing `443:443`.
5. Configure proxy -> hub forwarding.
6. Agents/helpers connect to `wss://tela.example.com/...`.

### Notes

- If you are behind CGNAT, you may not be able to do this reliably.
- Keep the hub off the public internet directly; publish only the proxy.

---

## 5) Ingress mode 2 — Cloudflare Tunnel (optional convenience)

This is the easiest way to avoid inbound firewall changes.

### What you get

- A stable DNS name.
- No inbound ports.
- Easy TLS termination.

### What you trade

- You now rely on Cloudflare for *connectivity*, not just DNS.
- Strict certificate pinning becomes tricky if the client pins what Cloudflare presents.

### Steps (high level)

1. Run `cloudflared` (host or container).
2. Configure it to forward the hostname to your local reverse proxy.
3. Keep your local reverse proxy config the same as direct mode.

Practical pattern:

- `cloudflared` -> `https://proxy:443`
- `proxy` -> `http://hub:8080` (or `ws://hub:8080` depending on your hub endpoints)

---

## 6) Certificate pinning vs Cloudflare (important)

Tela’s design includes **certificate pinning** for agent and helper.

### Why Cloudflare conflicts with naive pinning

- If Cloudflare is in front, the client’s TLS connection terminates at Cloudflare.
- The certificate the client sees is Cloudflare-managed and can rotate.
- Pinning that leaf certificate is fragile.

### Ways to stay aligned with the design while keeping Cloudflare optional

Pick one approach explicitly and document it as a phase decision:

1) **Direct mode is the “pinned” mode; tunnel mode is allowed but not pinned** (Phase 1 pragmatic)
- Agent/helper enforces pinning only when connecting directly to your proxy.
- When connecting via tunnel, rely on normal TLS + session tokens.

2) **Pin a stable key you control (preferred long term)**
- Terminate TLS on a proxy you control in direct mode.
- For tunnel mode, pinning must be re-thought (e.g., pin an application-layer public key and do a secondary handshake that proves hub identity independent of TLS termination).

3) **Avoid TLS termination at Cloudflare for agent/helper paths**
- Keep Cloudflare only for the browser UI, not the data-plane endpoints.
- Requires additional routing/hostnames and is more complex.

For a POC, option (1) is often the fastest while preserving the long-term direction.

---

## 7) Ports and routing conventions

### Recommended

- Public: **443/tcp** only
- Internal:
  - Proxy listens on 443
  - Hub listens on a private port (e.g., 8080)

### Why 443

- It’s the least blocked egress from locked-down networks.
- It’s the normal home for `https://` and `wss://`.

---

## 8) Docker Compose skeleton (proxy + hub)

**Current production setup** — see `docker-compose.yml` in the repo root:

```yaml
services:
  caddy:
    # Caddy with cloudflare-dns plugin for DNS-01 ACME
    build:
      context: ./docker/caddy
    ports:
      - "443:443"
    volumes:
      - ./docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro
    environment:
      - CLOUDFLARE_API_TOKEN=${CLOUDFLARE_API_TOKEN}
    depends_on:
      - hub

  hub:
    build:
      context: .
      dockerfile: docker/hub/Dockerfile
    ports:
      - "3000:8080"          # HTTP + WebSocket
      - "41820:41820/udp"    # UDP relay for WireGuard datagrams
    environment:
      - HUB_PORT=8080
      - HUB_UDP_PORT=41820

  telad:
    build:
      context: .
      dockerfile: docker/hub/Dockerfile
    entrypoint: ["/usr/local/bin/telad"]
    command: ["-config", "/etc/tela/telad.yaml"]
    volumes:
      - ./poc/telad.yaml:/etc/tela/telad.yaml:ro
    depends_on:
      - hub
```

Example `Caddyfile` (DNS-01 with Cloudflare for direct-access TLS):

```caddyfile
tela-local.awansatu.net {
  reverse_proxy hub:8080
  tls {
    dns cloudflare {env.CLOUDFLARE_API_TOKEN}
  }
}
```

Notes:
- Caddy automatically handles WebSocket upgrade when `reverse_proxy` is used.
- The `hub` image is a multi-stage Docker build that also compiles `tela` and `telad` (Go binaries).
- UDP port 41820 must be published for the WireGuard UDP relay optimisation.
- The `telad` service reuses the same image but overrides the entrypoint.

---

## 9) Add Cloudflare Tunnel (optional)

A typical pattern is to add a `cloudflared` service that targets your local proxy.

Conceptually:

- `cloudflared` listens inside your network and creates an outbound tunnel.
- Cloudflare maps `tela.example.com` -> that tunnel.

If you do this, try to keep the local origin stable:

- Tunnel forwards to `http://proxy:80` or `https://proxy:443`.

---

## 10) Practical POC workflow (current state)

The POC has evolved well beyond the original Node.js prototype.
The live data path is now:

```
tela.exe (Go, WireGuard client) ──wss──▶ hub.js (Node.js relay) ◀──ws── telad (Go, WireGuard agent)
                                              ▲
                                         UDP 41820 (optional relay)
```

### Running locally (development)

1. **Build Go binaries:** `go build ./cmd/tela && go build ./cmd/telad`
2. **Start the hub:** `node poc/hub.js` (listens on `:8080` HTTP/WS + `:41820` UDP)
3. **Start telad:** `./telad -hub ws://localhost:8080 -machine mybox -ports "22,3389"`
4. **Start tela:** `./tela -hub ws://localhost:8080 -machine mybox`
5. Connect to `localhost:<advertised-port>` for SSH/RDP — traffic flows through the WireGuard tunnel.

### Running via Docker (production)

```bash
docker compose up --build -d
./tela connect -hub wss://tela-local.awansatu.net -machine barn
```

### Remaining iteration targets

- Binary multiplexed framing (DESIGN.md §6.3)
- Multiple simultaneous sessions per machine

---

## 11) VPS vs home-hosting (what changes)

If you host the Hub on a VPS:

- You usually don’t need Cloudflare Tunnel.
- You typically don’t need port forwarding.
- You still want the same `proxy + hub` shape.

If you host at home:

- Direct mode requires port-forwarding.
- Tunnel mode avoids port-forwarding.

Either way, keeping the same internal deployment shape reduces churn.

---

## 12) Quick decision checklist

- Do you want to open inbound ports?
  - Yes -> Direct mode + Let’s Encrypt
  - No -> Cloudflare Tunnel
- Do you require strict certificate pinning in the earliest POC?
  - Yes -> Prefer direct mode for agent/helper paths
  - Not yet -> Tunnel mode is acceptable, document the pinning caveat
- Do you need the Hub to run on your workstation?
  - Not required; any machine you control works (home server, VPS, etc.)

---

## 13) Latency optimisation roadmap

Interactive protocols (SSH, RDP) are sensitive to per-keystroke latency.
The relay path `client → helper → Cloudflare → hub → agent → service`
adds multiple hops and framing layers. Below are the known contributors
and their mitigations, roughly ordered from easiest to hardest.

### 13.1 TCP_NODELAY (implemented)

Nagle's algorithm batches small TCP writes into larger segments, adding up
to 40 ms of delay per write on each TCP socket in the path. Disabling it
with `TCP_NODELAY` is the single biggest quick-win for interactive feel.

**Status:** Applied in `poc/agent.js` (`setNoDelay(true)`) and
`helper/main.go` (`SetNoDelay(true)`) as of the current build.

### 13.2 Cloudflare round-trip

Every WS frame transits Cloudflare edge → origin and back. This adds one
RTT to every data exchange, typically 10–50 ms depending on edge
proximity. There is no way to reduce this while using the tunnel.

**Mitigation:**

- **Direct mode (done)** — Caddy with DNS-01 (Cloudflare API) serves
  `tela-local.awansatu.net` with valid Let's Encrypt TLS, bypassing the
  Cloudflare edge entirely. This is the recommended path for local and
  LAN-adjacent use.
- **Tunnel mode** — still available via `tela.awansatu.net` through
  Cloudflare Tunnel for scenarios where inbound ports are blocked.
- Long-term: allow the Hub to advertise both URLs and let the client
  prefer the direct path automatically.

### 13.3 WebSocket framing overhead

Each TCP segment is wrapped in a WS frame (2–14 bytes header + masking on
client-to-server direction). For bulk transfers this is negligible, but
for many tiny SSH packets it multiplies syscalls and copies.

**Mitigation (future):**

- **Binary multiplexed framing** as specified in DESIGN.md §6.3 — a thin
  12-byte Tela frame header replaces per-message WS framing, and multiple
  logical channels share a single WS connection.
- **Per-message-deflate** (`permessage-deflate` WS extension) can compress
  repetitive terminal output but adds CPU cost; benchmark before enabling.

### 13.4 Double relay (hub as pure relay)

The Hub receives every byte from one side and writes it to the other.
For a Node.js hub, each hop passes through the event loop and `ws`
library buffers.

**Current mitigation — UDP relay (done):**

The hub now offers a UDP port (41820) alongside WebSocket. When both
peers upgrade, WireGuard datagrams bypass the WS framing layer entirely.
When only one side upgrades (asymmetric mode), the hub bridges
UDP↔WebSocket so the faster side still benefits. This eliminates the
TCP-over-TCP overhead that was the biggest contributor to interactive
latency via the relay.

**Future mitigation:**

- **Native hub data-plane** — rewrite the relay in Go where `splice(2)`
  / zero-copy IO can eliminate userspace copies for the remaining WS path.
- **Peer-to-peer direct tunnel (done)** — STUN-based hole punching lets
  tela and telad establish a direct UDP path, removing the hub from the
  data plane entirely. Implemented in Phase 3 with automatic fallback
  cascade (direct → UDP relay → WebSocket).

### 13.5 Summary table

| Optimisation          | Latency saved   | Effort  | Status       |
|-----------------------|-----------------|---------|--------------|
| TCP_NODELAY           | up to ~40 ms    | trivial | **done**     |
| Direct (skip CF)      | 10–50 ms RTT    | low     | **done** (Caddy + DNS-01) |
| UDP relay             | TCP-over-TCP    | medium  | **done** (hub port 41820) |
| P2P direct connect    | full relay hop   | high    | **done** (STUN + hole punch) |
| Binary framing        | syscall overhead | medium  | design-phase |
| Native hub data-plane | copy overhead    | high    | future       |

---

## 14) Where to record environment-specific details

As you implement, keep a small private note (not necessarily in git) with:

- Domain name(s)
- DNS provider
- Whether you use direct or tunnel ingress
- Public IP / dynamic DNS settings
- Which ports are forwarded (if any)
- Certificate strategy (Let’s Encrypt vs self-signed vs Cloudflare)

That keeps the repo portable and avoids baking your personal details into docs.

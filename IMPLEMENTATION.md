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
Native client -> helper (localhost:<port>) -> wss://tela.example -> hub -> agent -> local service
```

A practical rule:

- Expose **one** thing publicly: **TCP 443** to a TLS endpoint.
- Everything else stays internal.

---

## 2) Recommended container shape (Cloudflare optional)

To keep Cloudflare optional, keep your internal deployment consistent and swap only the ingress method.

**Recommended:**

- `hub` (application) listens on an internal HTTP/WS port (e.g., `:8080`).
- `proxy` (reverse proxy) terminates TLS on `:443` and forwards to `hub`.
- optional: `cloudflared` points at the `proxy`.

This yields:

- Direct mode: Internet -> `proxy:443` (via port-forward)
- Tunnel mode: Internet -> Cloudflare -> `cloudflared` -> `proxy:443`

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

This skeleton is intentionally generic. Adapt the `hub` service to your actual implementation.

```yaml
services:
  proxy:
    image: caddy:2
    ports:
      - "443:443"
      - "80:80"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      - hub

  hub:
    # Replace this with the real Tela Hub image when it exists.
    # For a POC hub, you can build a Node-based container that listens on :8080.
    image: node:20-alpine
    working_dir: /app
    volumes:
      - ./hub:/app:ro
    command: ["node", "hub.js"]
    environment:
      - HUB_PORT=8080

volumes:
  caddy_data:
  caddy_config:
```

Example `Caddyfile` that supports WebSockets:

```caddyfile
tela.example.com {
  reverse_proxy hub:8080
}
```

Notes:
- Caddy automatically handles WebSocket upgrade when reverse_proxy is used.
- If you split HTTP UI paths vs WS paths later, you can route by path prefix.

---

## 9) Add Cloudflare Tunnel (optional)

A typical pattern is to add a `cloudflared` service that targets your local proxy.

Conceptually:

- `cloudflared` listens inside your network and creates an outbound tunnel.
- Cloudflare maps `tela.example.com` -> that tunnel.

If you do this, try to keep the local origin stable:

- Tunnel forwards to `http://proxy:80` or `https://proxy:443`.

---

## 10) Practical POC workflow (today)

Your current POC in `poc/` demonstrates the core data path (helper localhost bridge + hub relay + agent TCP proxy).

A pragmatic next step for “internet-ready POC” (without redesigning everything at once) is:

1. Put a reverse proxy in front of the POC hub.
2. Serve it as `wss://...`.
3. Keep the helper and agent connecting to the proxy URL.

Then iterate toward the design:

- Wrap all data/control into the framed multiplex protocol.
- Add helper session token auth.
- Add agent identity.

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

## 13) Where to record environment-specific details

As you implement, keep a small private note (not necessarily in git) with:

- Domain name(s)
- DNS provider
- Whether you use direct or tunnel ingress
- Public IP / dynamic DNS settings
- Which ports are forwarded (if any)
- Certificate strategy (Let’s Encrypt vs self-signed vs Cloudflare)

That keeps the repo portable and avoids baking your personal details into docs.

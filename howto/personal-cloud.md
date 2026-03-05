# HOWTO — Personal Cloud / Homelab Remote Access (Tela)

This guide shows how to use Tela to reach home machines (NAS, dev box, media server) from anywhere, including locked-down networks, without opening inbound ports on the home network.

It covers two supported deployment patterns:

- **Pattern A (Endpoint agent):** `telad` runs directly on each home machine.
- **Pattern B (Gateway/bridge agent):** `telad` runs on a gateway (VM/container) that can reach target machines.

---

## What you’ll end up with

- A reachable **Hub** URL (example: `wss://gohub.parkscomputing.com` or your own hostname).
- One or more registered **Machines** with a list of **Services** (SSH, RDP, HTTP, etc.).
- On-demand access from any client using the `tela` CLI.

---

## Prerequisites

### Network and hosting

- A machine to run the Hub (Linux VM, home server, or any host that can accept inbound HTTPS *or* is reachable via a tunnel/reverse proxy).
- A public URL for the Hub (recommended). Tela works best when the Hub is reachable via `wss://`.

### Software

- Hub: `telahubd` (Go binary), typically run via Docker Compose.
- Agent: `telad` (run on endpoints or a gateway).
- Client: `tela` (download/run on-demand from GitHub Releases, or build from source).

---

## Step 1 — Run a Hub

There are many ways to publish a hub. Start with the simplest for your setup.

### Option 1: Quick demo (single host, Docker Compose)

On the machine where you want to run the hub:

1. Clone this repo.
2. Run:

```bash
docker compose up --build -d
```

This brings up:

- `gohub` (`telahubd` — HTTP + WebSocket + hub console)

Verify locally:

- Hub console: `http://<host>:3002/`
- Hub API: `http://<host>:3002/api/status`

If you’re using a public hostname + TLS, your browser URL will typically be `https://.../` and `tela` will connect using `wss://...`.

### Option 2: Publish the hub through a tunnel or reverse proxy

If you cannot open inbound ports, you can still make the hub reachable using an outbound tunnel (for example, Cloudflare Tunnel) or by running the hub on a small public VM.

Requirements:

- The outside world must be able to reach the hub’s `/` and `/api/*` endpoints over HTTPS.
- WebSockets must be supported (most tunnels/proxies support this).

---
## Step 1.5 — Enable authentication (recommended)

By default the hub runs in open mode. For any Internet-exposed hub, you should enable token-based auth.

### Quick path (Docker)

```bash
# Generate an owner token on your local machine
openssl rand -hex 32

# Add to docker-compose.yml environment:
#   - TELA_OWNER_TOKEN=<your-token>

# Redeploy
docker compose up --build -d
```

The hub creates an `owner` identity on first startup, then you manage everything remotely:

```bash
# From any workstation with tela installed:
tela admin add-token barn-agent -hub wss://YOUR-HUB-HOSTNAME -token <owner-token>
# → save the printed token for use in telad.yaml

tela admin grant barn-agent barn -hub wss://YOUR-HUB-HOSTNAME -token <owner-token>
```

See [hub.md](hub.md) for the full list of `tela admin` commands.

---
## Step 2 — Register a home machine (choose a pattern)

### Pattern A — Run `telad` on the home machine (recommended)

Use this when you can run `telad` directly on the machine that hosts the services.

1. Decide which services you want to expose (common examples):
   - SSH (22)
   - RDP (3389)
   - HTTP admin UI (443/8443/etc.)

2. Start `telad` using flags (example):

```bash
./telad -hub wss://YOUR-HUB-HOSTNAME -machine barn -ports "22,3389" -token <agent-token>
```

3. Verify from another machine:

```bash
./tela machines -hub wss://YOUR-HUB-HOSTNAME -token <your-token>
./tela services -hub wss://YOUR-HUB-HOSTNAME -machine barn -token <your-token>
```

Notes:

- For production, prefer using a config file and running `telad` as a service.
- Keep service exposure minimal: only the ports you need.
- If the hub has auth enabled, the token must be a valid identity token (generated via `tela admin add-token`).

### Pattern B — Run `telad` on a gateway that can reach the home machines

Use this when the target machine is locked down or you want to minimize installed software on the target.

You’ll run `telad` on a gateway host that can reach the target over the LAN.

1. Put the gateway on the same network as the target(s).
2. Configure one machine entry per target. For each entry, set:
   - `name`: stable identifier (example: `nas`)
   - `services`: list of ports to expose
   - `target`: the LAN IP or hostname of the target

Example config snippet:

```yaml
hub: wss://YOUR-HUB-HOSTNAME
machines:
  - name: nas
    services:
      - port: 22
        proto: tcp
        name: SSH
    target: 192.168.1.50
```

Start `telad` with the config file:

```bash
./telad -config telad.yaml
```

#### Variant: Docker “host bridge” (your Barn-style setup)

If `telad` runs in a container and you want to expose services on the Docker host, use:

- `target: host.docker.internal`

This is a convenient dev/test pattern because you don’t need to install `telad` on the host OS.

---

## Step 3 — Connect from a client machine (zero-install)

On the machine you’re using to connect *from*:

1. Download `tela` from the latest GitHub Release.
2. Verify the checksum (recommended):

- Linux/macOS:

```bash
sha256sum tela
```

- Windows PowerShell:

```powershell
Get-FileHash .\tela.exe -Algorithm SHA256
```

3. List machines:

```bash
./tela machines -hub wss://YOUR-HUB-HOSTNAME
```

4. Connect:

```bash
./tela connect -hub wss://YOUR-HUB-HOSTNAME -machine barn
```

This binds one or more `localhost` ports on your client machine.

---

## Step 4 — Use the service (SSH / RDP)

### SSH

After `tela connect`:

```bash
ssh localhost
```

### RDP (Windows)

After `tela connect`:

```powershell
mstsc /v:localhost
```

---

## Security notes

- Tela provides end-to-end encryption for tunneled traffic (hub relays ciphertext).
- The **last hop** from `telad` to the service is plain TCP unless the service protocol is encrypted (SSH, HTTPS, etc.).
  - Endpoint pattern keeps last hop local to the machine.
  - Gateway pattern puts last hop on your LAN/VPC; use segmentation and strong service auth.
- Expose only the ports you actually need.

---

## Troubleshooting

### I can’t see my machine in `tela machines`

- Confirm `telad` is running and connecting to the correct hub URL.
- Check the hub console `/` to see if the machine shows up.
- Confirm the hub is reachable from the agent host (outbound HTTPS/WS allowed).

### `tela` connects but SSH/RDP fails

- Confirm the target service is actually listening on the target machine.
- If you’re using gateway pattern, confirm the gateway can reach the target IP/port.
- If using Docker host-bridge, confirm the service is reachable from the container via `host.docker.internal`.

# Tela POC — TCP Tunneling via WebSocket Relay

Proof-of-concept that tunnels arbitrary TCP traffic through a WebSocket relay hub. Default test uses HTTP; also works with RDP, SSH, or any TCP service.

## Architecture

```
client → helper (localhost:8000) → WebSocket → hub → WebSocket → agent → target service (localhost:3000)
```

Four Node.js processes. One TCP tunnel. No auth, no TLS, no multiplexing.

## Prerequisites

- Node.js 18+ (LTS)

## Setup

```bash
cd poc
npm install
```

## Quick Test — HTTP (Recommended First Test)

Open **four terminals** in the `poc/` directory:

### Terminal 1 — Web Server (target service)

```bash
npm run serve
```

Output: `[serve] static web server on http://127.0.0.1:3000`

### Terminal 2 — Hub

```bash
npm run hub
```

Output: `[hub] listening on ws://0.0.0.0:8080`

### Terminal 3 — Agent

```bash
npm run agent
```

Output: `[agent] registered as: my-pc — waiting for session`

### Terminal 4 — Helper

```bash
npm run helper
```

Output: `[helper] >>> Open: http://localhost:8000`

### Open Browser

Navigate to **http://localhost:8000**

You should see the "Tela Tunnel Works" page. The traffic flowed:

```
browser → helper (port 8000) → WebSocket → hub (port 8080) → WebSocket → agent → web server (port 3000)
```

## RDP Test (Windows)

Same setup, but change the agent and helper ports:

```powershell
# Terminal 1 — Hub
npm run hub

# Terminal 2 — Agent (target: RDP on port 3389)
node agent.js ws://localhost:8080 my-pc 3389

# Terminal 3 — Helper (expose on port 13389)
node helper.js ws://localhost:8080 my-pc 13389

# Terminal 4 — Connect
mstsc /v:localhost:13389
```

RDP must be enabled on the machine (Windows Pro/Enterprise/Education only).

## Running Across Machines

### Machine A (target) — runs the service + agent

```bash
node serve.js                                   # or have RDP/SSH enabled
node agent.js ws://HUB_IP:8080 my-pc 3000       # port of the target service
```

### Machine B (anywhere) — runs hub

```bash
node hub.js
```

### Machine C (your laptop) — runs helper

```bash
node helper.js ws://HUB_IP:8080 my-pc 8000
# Then open http://localhost:8000
```

## CLI Arguments

### agent.js

```
node agent.js [hubUrl] [machineId] [targetPort] [targetHost]
```

| Arg | Default | Description |
|-----|---------|-------------|
| hubUrl | `ws://localhost:8080` | Hub WebSocket URL |
| machineId | `my-pc` | Machine identifier |
| targetPort | `3000` | Port of the local service to tunnel |
| targetHost | `127.0.0.1` | Host of the local service |

### helper.js

```
node helper.js [hubUrl] [machineId] [localPort]
```

| Arg | Default | Description |
|-----|---------|-------------|
| hubUrl | `ws://localhost:8080` | Hub WebSocket URL |
| machineId | `my-pc` | Machine identifier |
| localPort | `8000` | Local port to bind for client connections |

### hub.js

```
HUB_PORT=8080 node hub.js
```

### serve.js

```
node serve.js [port]
```

Default port: `3000`

## What This Proves

- WebSocket can carry TCP traffic (HTTP, RDP, SSH, etc.)
- The hub relay model works (outbound-only agent connectivity)
- Helper-based localhost binding works
- Protocol-agnostic tunneling is viable
- Native clients connect normally to the tunnel

## What This Skips

- TLS / encryption
- Authentication / session tokens
- Multiplexing (one session per WebSocket pair)
- Browser UI for orchestration
- Certificate pinning
- Protocol framing (raw binary relay, no `tela_frame_header_t`)
- Multiple simultaneous sessions
- Reconnect after session ends (agent reconnects to hub; helper exits)

## Troubleshooting

**"Agent not found"** — Start the agent before the helper. The agent must register first.

**Connection refused on port 8000** — Helper hasn't bound yet. Wait for the `>>> Open:` message.

**Page doesn't load / hangs** — Ensure the web server is running (`npm run serve`) and the agent's target port matches (default: 3000).

**RDP black screen** — Ensure RDP is enabled: Settings → System → Remote Desktop → On. Windows Home does not support inbound RDP.

**Port already in use** — Another process is using that port. Change it via CLI args or env vars.

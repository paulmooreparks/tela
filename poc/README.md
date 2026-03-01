# Tela POC — RDP over WebSocket Tunnel

Proof-of-concept that tunnels RDP traffic through a WebSocket relay hub.

## Architecture

```
mstsc → helper (localhost:13389) → WebSocket → hub → WebSocket → agent → localhost:3389
```

Three Node.js processes. One TCP tunnel. No auth, no TLS, no multiplexing.

## Prerequisites

- Node.js 18+ (LTS)
- A machine with RDP enabled (Windows Remote Desktop)

## Setup

```powershell
cd poc
npm install
```

## Running (All on One Machine)

Open **three terminals** in the `poc/` directory.

### Terminal 1 — Hub

```powershell
npm run hub
```

Output: `[hub] listening on ws://0.0.0.0:8080`

### Terminal 2 — Agent

```powershell
npm run agent
```

Output: `[agent] registered as: my-pc — waiting for session`

### Terminal 3 — Helper

```powershell
npm run helper
```

Output: `[helper] >>> Run: mstsc /v:localhost:13389`

### Terminal 4 — Connect

```powershell
mstsc /v:localhost:13389
```

You should see the RDP login screen for the local machine.

## Running Across Machines

### Machine A (RDP target) — runs agent

```powershell
node agent.js ws://HUB_IP:8080 my-pc
```

### Machine B (anywhere) — runs hub

```powershell
node hub.js
```

### Machine C (your laptop) — runs helper

```powershell
node helper.js ws://HUB_IP:8080 my-pc 13389
mstsc /v:localhost:13389
```

## Environment Variables

All components accept env vars as alternatives to CLI args:

| Variable | Default | Used by |
|----------|---------|---------|
| `HUB_PORT` | `8080` | hub |
| `HUB_URL` | `ws://localhost:8080` | agent, helper |
| `MACHINE_ID` | `my-pc` | agent, helper |
| `RDP_HOST` | `127.0.0.1` | agent |
| `RDP_PORT` | `3389` | agent |
| `LOCAL_PORT` | `13389` | helper |

## What This Proves

- WebSocket can carry RDP traffic at usable speed
- Hub relay model works (outbound-only agent connectivity)
- Helper-based localhost binding works
- `mstsc` connects normally to the tunnel

## What This Skips

- TLS / encryption
- Authentication / session tokens
- Multiplexing (one session per WebSocket pair)
- Browser UI
- Certificate pinning
- Protocol framing (raw binary relay)
- Multiple simultaneous sessions
- Reconnect after session ends (agent reconnects to hub; helper exits)

## Troubleshooting

**"Agent not found"** — Start the agent before the helper.

**Connection refused on 13389** — Helper hasn't bound the port yet. Wait for the `>>> Run: mstsc` message.

**RDP black screen / disconnect** — Ensure RDP is enabled on the agent machine. On Windows: Settings → System → Remote Desktop → On.

**Port 3389 in use** — Another RDP session may be active. Only one RDP connection at a time per machine (Windows Home limitation).

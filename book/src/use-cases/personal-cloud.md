# Personal cloud

## The scenario

You have several machines at home behind a residential router: a Network Attached Storage (NAS) device, a development workstation, a media server. Your router performs NAT and you either cannot or do not want to open inbound ports. From a coffee shop or a corporate office, you currently have no way to reach any of them.

Tela solves this with a hub that lives on a small public VM (a $5/month server is plenty). Each home machine runs `telad`, which makes an outbound connection to the hub and registers itself. Your laptop runs `tela` and connects through the hub to whichever machine you need.

When this is working, your laptop will have local ports for each home machine's services:

```
Services available:
  localhost:22     → SSH          (workstation)
  localhost:10022  → SSH          (NAS)
  localhost:5000   → port 5000    (NAS web UI)
  localhost:8096   → port 8096    (media server)
```

Use the port shown in the output to connect. To pin a service to a specific local port across reconnects, set `local:` on that service in your profile.

Nothing changes on your home router. No ports are forwarded. The home machines only make outbound connections.

## Prerequisites

### Network and hosting

- A machine to run the hub (Linux VM, home server, or any host that can accept inbound HTTPS or is reachable via a reverse proxy).
- A public URL for the hub (recommended). Tela works best when the hub is reachable via `wss://`.

### Software

- Hub: the `telahubd` binary.
- Agent: the `telad` binary (run on endpoints or a gateway).
- Client: the `tela` binary.

---

## Step 1 - Run a hub

See [Run a hub on the public internet](../howto/hub.md) for the full deployment walkthrough, including TLS configuration and service installation. For a quick test on a host with a public address:

```bash
telahubd
```

The hub prints an owner token on first start. Save it. It listens on port 80 (HTTP + WebSocket) and 41820 (UDP relay) by default.

---

## Step 2 - Set up authentication

Create tokens for each agent and user:

```bash
# Agent token (one per machine that will register with the hub)
tela admin tokens add barn-agent -hub wss://hub.example.com -token <owner-token>
# Save the printed token -- this is <agent-token> used in telad (Step 3)

# Grant the agent permission to register its machine
tela admin access grant barn-agent barn register -hub wss://hub.example.com -token <owner-token>

# User token (for the person connecting from client machines)
tela admin tokens add alice -hub wss://hub.example.com -token <owner-token>
# Save the printed token -- this is <your-token> used with tela connect (Step 4)
tela admin access grant alice barn connect -hub wss://hub.example.com -token <owner-token>
```

See [Run a hub on the public internet](../howto/hub.md) for the full list of `tela admin` commands.

---

## Step 3 - Register a home machine (choose a pattern)

### Pattern A - Run `telad` on the home machine (recommended)

Use this when you can run `telad` directly on the machine that hosts the services.

1. Decide which services to expose (common examples):
   - SSH (22)
   - RDP (3389)
   - HTTP admin UI (8080, 8443, etc.)

2. Start `telad`:

```bash
telad -hub wss://hub.example.com -machine barn -ports "22,3389" -token <agent-token>
```

3. Verify from another machine:

```bash
tela machines -hub wss://hub.example.com -token <your-token>
tela services -hub wss://hub.example.com -machine barn -token <your-token>
```

Notes:

- For persistent access, prefer a config file and run `telad` as a service.
- Keep service exposure minimal: only the ports you need.
- The token must be a valid agent token with `register` access to the machine.

### Pattern B - Run `telad` on a gateway that can reach the home machines

Use this when the target machine is locked down or you want to minimize installed software on the target.

1. Put the gateway on the same network as the target(s).
2. Configure one machine entry per target:

```yaml
hub: wss://hub.example.com
token: "<agent-token>"
machines:
  - name: nas
    services:
      - port: 22
        name: SSH
    target: 192.168.1.50
```

3. Start `telad` with the config file:

```bash
telad -config telad.yaml
```

---

## Step 4 - Connect from a client machine

On the machine you're connecting from:

1. Download `tela` from the latest GitHub Release.
2. List machines:

```bash
tela machines -hub wss://hub.example.com -token <your-token>
```

3. Connect:

```bash
tela connect -hub wss://hub.example.com -machine barn -token <your-token>
```

The client prints the local address bound for each service. Use that address to connect.

---

## Step 5 - Use the service (SSH / RDP)

### SSH

After `tela connect`:

```bash
ssh -p PORT localhost
```

Use the port shown in the `tela connect` output.

### RDP (Windows)

After `tela connect`:

```powershell
mstsc /v:localhost:PORT
```

Use the port shown in the `tela connect` output.

---

## Security notes

- Tela provides end-to-end encryption for tunneled traffic (hub relays ciphertext).
- The last hop from `telad` to the service is plain TCP unless the service protocol is encrypted (SSH, HTTPS, etc.).
  - Endpoint pattern keeps last hop local to the machine.
  - Gateway pattern puts last hop on your LAN; use segmentation and strong service authentication.
- Expose only the ports you actually need.

---

## Troubleshooting

### I can't see my machine in `tela machines`

- Confirm `telad` is running and connecting to the correct hub URL.
- Check the hub console at `/` to see if the machine shows up.
- Confirm the hub is reachable from the agent host (outbound HTTPS/WebSocket allowed).
- If auth is enabled, confirm the agent token is valid and has been granted `register` access to the machine.

### `tela` connects but SSH/RDP fails

- Confirm the target service is listening on the target machine.
- If using gateway pattern, confirm the gateway can reach the target IP and port.

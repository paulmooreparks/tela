# Distributed teams

A distributed engineering team that needs to access shared development and staging resources (SSH, databases, internal HTTP services) without a VPN and without opening inbound ports on the infrastructure.

## Design goals for teams

- Avoid distributing IP addresses and per-machine VPN configs.
- Expose only the services teams need (service-level access, not full-network access).
- Keep onboarding simple (download one binary, connect).

---

## Step 0 - Pick a hub strategy

Common approaches:

- **One hub per environment**: `dev`, `staging`, `prod`.
- **One hub per site**: `office-a`, `office-b`, `cloud`.
- **One hub per customer/tenant** (for MSP-like setups).

Start with **one hub per environment** if you have a single organization.

---

## Step 1 - Run the hub(s)

See [Run a hub on the public internet](../howto/hub.md) for the full hub deployment guide, including TLS setup and cloud firewall rules. For a quick start on a host with a public address:

```bash
telahubd
```

The hub prints an owner token on first start. Save it. Make each hub reachable over `wss://` (public VM or reverse proxy). Ensure WebSockets work.

---

## Step 2 - Set up authentication

Create tokens for agents and developers on each hub:

```bash
# Create agent tokens (one per telad instance)
tela admin tokens add telad-dev-db01 -hub wss://dev-hub.example.com -token <owner-token>
# Save the printed token -- this is <agent-token> used in telad on dev-db01 (Step 3)

tela admin tokens add telad-staging-win01 -hub wss://staging-hub.example.com -token <staging-owner-token>
# Save the printed token -- this is <agent-token> used in telad on staging-win01 (Step 3)

# Grant each agent permission to register its machine
tela admin access grant telad-dev-db01 dev-db01 register -hub wss://dev-hub.example.com -token <owner-token>

# Create a developer token
tela admin tokens add alice -hub wss://dev-hub.example.com -token <owner-token>
# Save the printed token -- give it to Alice for use with tela connect (Step 4)
tela admin access grant alice dev-db01 connect -hub wss://dev-hub.example.com -token <owner-token>
```

See [Run a hub on the public internet](../howto/hub.md) for the full list of `tela admin` commands.

---

## Step 3 - Register machines with `telad`

### Pattern A - Endpoint agents (recommended)

Run `telad` on each machine you want to expose.

Example (a Linux server exposing SSH and Postgres):

```bash
telad -hub wss://dev-hub.example.com -machine dev-db01 -ports "22,5432" -token <agent-token>
```

Example (a Windows staging box exposing RDP):

```powershell
telad.exe -hub wss://staging-hub.example.com -machine staging-win01 -ports "3389" -token <agent-token>
```

### Pattern B - Site gateway (bridge agent)

Run `telad` on a gateway VM that can reach internal targets.

Example `telad.yaml`:

```yaml
hub: wss://dev-hub.example.com
token: "<agent-token>"
machines:
  - name: dev-db01
    services:
      - port: 22
        name: SSH
      - port: 5432
        name: Postgres
    target: 10.10.0.15
  - name: dev-admin
    services:
      - port: 8443
        name: Admin UI
    target: 10.10.0.25
```

Run:

```bash
telad -config telad.yaml
```

---

## Step 4 - Developer workflow with `tela`

On a developer laptop:

1. Download `tela` from GitHub Releases and verify checksums.
2. List machines:

```bash
tela machines -hub wss://dev-hub.example.com -token <your-token>
```

3. List services on a machine:

```bash
tela services -hub wss://dev-hub.example.com -machine dev-db01 -token <your-token>
```

4. Connect:

```bash
tela connect -hub wss://dev-hub.example.com -machine dev-db01 -token <your-token>
```

5. Use tools against the local address shown in the output:

- SSH:

```bash
ssh 127.88.x.x
```

- Postgres (example):

```bash
psql -h 127.88.x.x -U postgres
```

**Tip:** Set environment variables to avoid repeating flags:

```bash
export TELA_HUB=wss://dev-hub.example.com
export TELA_TOKEN=<your-token>
tela machines
tela connect -machine dev-db01
```

---

## Operational guidance

### Naming conventions

- Prefer stable names: `env-roleNN` (example: `staging-web02`).
- Avoid embedding IPs in names.

### Least privilege

- Expose only required ports.
- Prefer encrypted service protocols (SSH, TLS).

### Split dev/staging/prod

- Separate hubs are the simplest isolation boundary.

---

## Troubleshooting

### A machine is "online" but the service doesn't work

- Endpoint pattern: verify the service is listening on that machine.
- Gateway pattern: verify the gateway can reach `target:port`.

### WebSocket blocked

- If developers can't reach `wss://` due to corporate proxies, ensure the hub is accessible over standard HTTPS ports and that WebSockets are allowed.

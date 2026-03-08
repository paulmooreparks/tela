# HOWTO - Distributed Development Teams (Tela)

This guide shows how to use Tela for a distributed engineering team to access dev/staging resources (SSH, databases, internal HTTP admin panels) without a VPN and without opening inbound ports.

It covers:

- **Pattern A (Endpoint agent):** `telad` on each server/workstation.
- **Pattern B (Gateway/bridge agent):** `telad` on a site gateway that reaches internal targets.

---

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

Recommendation:

- Start with **one hub per environment** if you have a single organization.

---

## Step 1 - Run the hub(s)

On each hub host:

```bash
docker compose up --build -d
```

Make each hub reachable over `wss://` (public VM, reverse proxy, or tunnel). Ensure WebSockets work.

---

## Step 2 - Register machines/services with `telad`

### Pattern A - Endpoint agents (recommended)

Run `telad` on each machine you want to expose.

Example (a Linux server exposing SSH and Postgres):

```bash
./telad -hub wss://DEV-HUB -machine dev-db01 -ports "22,5432"
```

Example (a Windows staging box exposing RDP):

```powershell
.\telad.exe -hub wss://STAGING-HUB -machine staging-win01 -ports "3389"
```

### Pattern B - Site gateway (bridge agent)

Run `telad` on a gateway VM that can reach internal targets.

Example `telad.yaml`:

```yaml
hub: wss://DEV-HUB
machines:
  - name: dev-db01
    services:
      - port: 22
        proto: tcp
        name: SSH
      - port: 5432
        proto: tcp
        name: Postgres
    target: 10.10.0.15
  - name: dev-admin
    services:
      - port: 8443
        proto: tcp
        name: Admin UI
    target: 10.10.0.25
```

Run:

```bash
./telad -config telad.yaml
```

---

## Step 3 - Developer workflow with `tela`

On a developer laptop:

1. Download `tela` from GitHub Releases and verify checksums.
2. List machines:

```bash
./tela machines -hub wss://DEV-HUB
```

3. List services:

```bash
./tela services -hub wss://DEV-HUB -machine dev-db01
```

4. Connect:

```bash
./tela connect -hub wss://DEV-HUB -machine dev-db01
```

5. Use tools against localhost:

- SSH:

```bash
ssh localhost
```

- Postgres (example):

```bash
psql -h localhost -U postgres
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

### A machine is “online” but service doesn’t work

- Endpoint pattern: verify service is listening on that machine.
- Gateway pattern: verify gateway can reach `target:port`.

### WebSocket blocked

- If devs can’t reach `wss://` due to proxies, ensure the hub is accessible over standard HTTPS ports and that WebSockets are allowed.

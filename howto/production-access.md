# HOWTO - Production Service Access (Bastion Replacement) (Tela)

This guide shows how to use Tela to access production services (SSH, databases, internal admin panels) without a traditional bastion host and without opening inbound ports on production machines.

The key idea is: production machines connect outbound to a hub, operators connect inbound to the hub, and traffic is tunneled end-to-end.

---

## Strong recommendation for production

- Prefer **Pattern A (Endpoint agent)** on each production VM.
- Expose the smallest possible set of services.
- Use a dedicated hub for production.
- Treat hub and agent credentials as secrets.

---

## Step 1 - Stand up a production hub

1. Deploy the hub on hardened infrastructure.
2. Publish it as `wss://PROD-HUB`.
3. Ensure:
   - HTTPS/TLS is valid
   - WebSockets work
   - `/api/status` is reachable

A simple starting point:

```bash
docker compose up --build -d
```

In real production you’ll typically also:

- Run behind a reverse proxy
- Add monitoring
- Add access control (token-based today; stronger identity later)

---

## Step 2 - Register production machines with `telad`

### Pattern A - Endpoint agent

On each production VM, run `telad`.

Example (web server exposing SSH only):

```bash
./telad -hub wss://PROD-HUB -machine prod-web01 -ports "22"
```

Example (database server exposing SSH + DB port):

```bash
./telad -hub wss://PROD-HUB -machine prod-db01 -ports "22,5432"
```

Guidance:

- If you need DB access, consider requiring TLS on the DB itself.
- Avoid exposing wide port ranges.

### Pattern B - Gateway/bridge agent (use sparingly)

Use only when endpoints cannot run `telad`.

In that case, the gateway becomes a critical asset:

- It must be isolated.
- It must be tightly allowlisted (targets/ports).

---

## Step 3 - Operator workflow

On an operator machine:

1. Download `tela` and verify checksum.
2. List machines:

```bash
./tela machines -hub wss://PROD-HUB
```

3. Connect to a machine:

```bash
./tela connect -hub wss://PROD-HUB -machine prod-web01
```

4. Use tools via localhost:

- SSH:

```bash
ssh localhost
```

- Database (example):

```bash
psql -h localhost -U postgres
```

---

## Security notes (production)

- Tela encrypts the tunnel end-to-end; the hub relays ciphertext.
- Production hardening is still necessary:
  - Patch systems
  - Strong SSH auth
  - Least privilege
  - Audit access
- Separate hubs per environment are the simplest control boundary.

---

## Troubleshooting

### Operators can reach hub but no machines appear

- Confirm `telad` is running on the production VM.
- Confirm egress from the VM allows outbound HTTPS/WebSockets to the hub.

### Service reachable locally on the server but not via Tela

- Confirm the service is listed by `tela services`.
- Confirm the correct port is exposed in `telad`.

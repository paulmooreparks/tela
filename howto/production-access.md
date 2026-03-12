# HOWTO - Production Service Access (Bastion Replacement) (Tela)

This guide shows how to use Tela to access production services (SSH, databases, internal admin panels) without a traditional bastion host and without opening inbound ports on production machines.

The key idea is: production machines connect outbound to a hub, operators connect inbound to the hub, and traffic is tunneled end-to-end.

---

## Strong recommendation for production

- Prefer **Pattern A (Endpoint agent)** on each production VM.
- Expose the smallest possible set of services.
- Use a dedicated hub for production.
- **Always enable authentication** — treat hub and agent tokens as secrets.

---

## Step 1 - Stand up a production hub

1. Deploy the hub on hardened infrastructure.
2. Publish it as `wss://PROD-HUB`.
3. Ensure:
   - HTTPS/TLS is valid
   - WebSockets work
   - `/api/status` is reachable

See [hub.md](hub.md) for the full deployment guide, including TLS setup with Caddy and cloud firewall rules.

A simple starting point:

```bash
docker compose up --build -d
```

In real production you'll typically also:

- Run behind a reverse proxy (Caddy, nginx, or Cloudflare Tunnel)
- Add monitoring
- Enable token-based authentication (see next step)

---

## Step 1.5 - Enable authentication (required for production)

Production hubs must have authentication enabled. For Docker deployments:

```bash
# Generate an owner token
openssl rand -hex 32

# Add to docker-compose.yml environment:
#   - TELA_OWNER_TOKEN=<your-token>

# Redeploy
docker compose up --build -d
```

For bare-metal deployments:

```bash
telahubd user bootstrap
# → Save the owner token
```

Then create agent and operator identities:

```bash
# Create agent tokens (one per production machine)
tela admin add-token agent-web01 -hub wss://PROD-HUB -token <owner-token>
tela admin add-token agent-db01 -hub wss://PROD-HUB -token <owner-token>

# Grant machine registration access
tela admin grant agent-web01 prod-web01 -hub wss://PROD-HUB -token <owner-token>
tela admin grant agent-db01 prod-db01 -hub wss://PROD-HUB -token <owner-token>

# Create operator identities
tela admin add-token alice -hub wss://PROD-HUB -token <owner-token>
tela admin grant alice prod-web01 -hub wss://PROD-HUB -token <owner-token>
tela admin grant alice prod-db01 -hub wss://PROD-HUB -token <owner-token>
```

See [hub.md](hub.md) for the full list of `tela admin` commands.

---

## Step 2 - Register production machines with `telad`

### Pattern A - Endpoint agent

On each production VM, run `telad` with a config file:

```yaml
# telad.yaml
hub: wss://PROD-HUB
token: "<agent-web01-token>"

machines:
  - name: prod-web01
    ports: [22]
```

```bash
./telad -config telad.yaml
```

Or with flags (quick start):

```bash
./telad -hub wss://PROD-HUB -machine prod-web01 -ports "22" -token <agent-token>
```

For persistent operation, install as a service:

```bash
telad service install -config telad.yaml
telad service start
```

See [services.md](services.md) for platform-specific details.

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
./tela machines -hub wss://PROD-HUB -token <your-token>
```

3. Connect to a machine:

```bash
./tela connect -hub wss://PROD-HUB -machine prod-web01 -token <your-token>
```

**Tip:** Set environment variables to avoid repeating flags:

```bash
export TELA_HUB=wss://PROD-HUB
export TELA_TOKEN=<your-token>
./tela machines
./tela connect -machine prod-web01
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
  - Least privilege — grant connect access only to the machines each operator needs
  - Audit access — check `/api/history` on the hub
  - Rotate tokens periodically — use `tela admin rotate`
- Separate hubs per environment are the simplest control boundary.

---

## Troubleshooting

### Operators can reach hub but no machines appear

- Confirm `telad` is running on the production VM.
- Confirm egress from the VM allows outbound HTTPS/WebSockets to the hub.
- If auth is enabled, confirm the agent token is valid and has been granted access to register the machine.

### Service reachable locally on the server but not via Tela

- Confirm the service is listed by `tela services -hub <hub> -machine <machine> -token <token>`.
- Confirm the correct port is exposed in `telad`.

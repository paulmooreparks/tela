# Production access

## The scenario

Your production infrastructure runs on cloud VMs or bare metal with no inbound ports open. Today, getting to a machine requires a bastion host, a VPN, or punching a hole in the firewall. Any of those approaches requires ongoing maintenance, introduces a shared-credential problem, and often ends up with broader access than intended ("connect to the VPN, now you can reach everything").

With Tela, each production VM runs `telad` as an OS service. It makes an outbound connection to a dedicated production hub and registers itself, exposing only the specific ports the team needs -- SSH, a database port, an admin panel. Access is controlled per-machine and per-identity: the on-call engineer has SSH access to the web servers, the DBA has database access, neither has access to the other's machines.

When a team member needs to connect, they run `tela connect` with their profile. They get a local address for each machine they have access to:

```
Services available:
  127.88.x.x:22    → SSH          (web-01)
  127.88.y.y:22    → SSH          (web-02)
  127.88.z.z:5432  → port 5432    (db-01)
```

No bastion. No VPN. No shared credentials. If a team member leaves, their identity is removed from the hub and their access ends immediately -- nothing else changes on the production machines.

## Strong recommendation for production

- Prefer **Pattern A (Endpoint agent)** on each production VM.
- Expose the smallest possible set of services.
- Use a dedicated hub for production.
- Always enable authentication. Treat hub and agent tokens as secrets.

---

## Step 1 - Stand up a production hub

See [Run a hub on the public internet](../howto/hub.md) for the full deployment guide, including TLS setup with a reverse proxy and cloud firewall rules. For a quick start on hardened infrastructure:

```bash
telahubd
```

The hub prints an owner token on first start. Save it. Publish the hub as `wss://prod-hub.example.com`.

Verify:
- HTTPS/TLS is valid
- WebSockets work
- `/api/status` is reachable

---

## Step 2 - Set up authentication

Create tokens for each production machine and each operator:

```bash
# Create agent tokens (one per production machine)
tela admin tokens add agent-web01 -hub wss://prod-hub.example.com -token <owner-token>
# Save the printed token -- this is <agent-web01-token> used in telad on prod-web01 (Step 3)
tela admin tokens add agent-db01 -hub wss://prod-hub.example.com -token <owner-token>
# Save the printed token -- this is <agent-db01-token> used in telad on prod-db01 (Step 3)

# Grant each agent permission to register its machine
tela admin access grant agent-web01 prod-web01 register -hub wss://prod-hub.example.com -token <owner-token>
tela admin access grant agent-db01 prod-db01 register -hub wss://prod-hub.example.com -token <owner-token>

# Create operator tokens
tela admin tokens add alice -hub wss://prod-hub.example.com -token <owner-token>
# Save the printed token -- give it to Alice for use with tela connect (Step 4)
tela admin access grant alice prod-web01 connect -hub wss://prod-hub.example.com -token <owner-token>
tela admin access grant alice prod-db01 connect -hub wss://prod-hub.example.com -token <owner-token>
```

See [Run a hub on the public internet](../howto/hub.md) for the full list of `tela admin` commands.

---

## Step 3 - Register production machines with `telad`

### Pattern A - Endpoint agent

On each production VM, run `telad` with a config file:

```yaml
# telad.yaml
hub: wss://prod-hub.example.com
token: "<agent-web01-token>"

machines:
  - name: prod-web01
    ports: [22]
```

```bash
telad -config telad.yaml
```

Or with flags (quick start):

```bash
telad -hub wss://prod-hub.example.com -machine prod-web01 -ports "22" -token <agent-token>
```

For persistent operation, install as a service:

```bash
telad service install -config telad.yaml
telad service start
```

See [Run Tela as an OS service](../howto/services.md) for platform-specific details.

Guidance:

- If you need database access, require TLS on the database itself.
- Avoid exposing wide port ranges.

### Pattern B - Gateway/bridge agent (use sparingly)

Use only when endpoints cannot run `telad`. The gateway becomes a critical asset: it must be isolated and tightly allowlisted to specific targets and ports.

---

## Step 4 - Operator workflow

On an operator machine:

1. Download `tela` and verify the checksum.
2. List machines:

```bash
tela machines -hub wss://prod-hub.example.com -token <your-token>
```

3. Connect to a machine:

```bash
tela connect -hub wss://prod-hub.example.com -machine prod-web01 -token <your-token>
```

4. Use tools against the local address shown in the output:

- SSH:

```bash
ssh 127.88.x.x
```

- Database (example):

```bash
psql -h 127.88.x.x -U postgres
```

**Tip:** Set environment variables to avoid repeating flags:

```bash
export TELA_HUB=wss://prod-hub.example.com
export TELA_TOKEN=<your-token>
tela machines
tela connect -machine prod-web01
```

---

## Security notes (production)

- Tela encrypts the tunnel end-to-end; the hub relays ciphertext.
- Production hardening is still necessary:
  - Patch systems
  - Strong SSH authentication
  - Least privilege -- grant `connect` access only to the machines each operator needs
  - Audit access -- check `/api/history` on the hub
  - Rotate tokens periodically -- use `tela admin rotate`
- Separate hubs per environment are the simplest control boundary.

---

## Troubleshooting

### Operators can reach hub but no machines appear

- Confirm `telad` is running on the production VM.
- Confirm egress from the VM allows outbound HTTPS/WebSockets to the hub.
- If auth is enabled, confirm the agent token is valid and has been granted `register` access to the machine.

### Service reachable locally on the server but not via Tela

- Confirm the service is listed by `tela services -hub <hub> -machine <machine> -token <token>`.
- Confirm the correct port is exposed in `telad`.

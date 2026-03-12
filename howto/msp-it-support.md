# HOWTO - MSP / IT Support (Tela)

This guide shows how an IT support provider can use Tela to reach customer machines (SSH, RDP, internal admin UIs) without asking customers to open inbound ports.

---

## Recommended topology

For MSP-style support, there are two common models:

1. **One hub per customer** (recommended isolation)
2. **One hub for multiple customers** (requires careful naming/tagging and stricter access controls)

This guide assumes **one hub per customer**.

---

## Step 1 - Deploy a hub for a customer

1. Deploy the hub on infrastructure you control.
2. Publish a customer-specific URL (example: `wss://acme-hub.example.com`).
3. Ensure WebSockets work.

See [hub.md](hub.md) for the full hub deployment guide, including TLS setup and firewall rules.

---

## Step 1.5 - Enable authentication (recommended)

Enable token-based auth on each customer hub:

```bash
# On the hub machine (or via Docker env var TELA_OWNER_TOKEN)
telahubd user bootstrap
# → Save the owner token

# Create an agent identity for the customer's machines
tela admin add-token acme-agent -hub wss://acme-hub.example.com -token <owner-token>
tela admin grant acme-agent ws-01 -hub wss://acme-hub.example.com -token <owner-token>
tela admin grant acme-agent srv-01 -hub wss://acme-hub.example.com -token <owner-token>

# Create technician identities
tela admin add-token tech-bob -hub wss://acme-hub.example.com -token <owner-token>
tela admin grant tech-bob ws-01 -hub wss://acme-hub.example.com -token <owner-token>
tela admin grant tech-bob srv-01 -hub wss://acme-hub.example.com -token <owner-token>
```

See [hub.md](hub.md) for the full list of `tela admin` commands.

---

## Step 2 - Register customer machines

### Pattern A - Endpoint agent (preferred)

On each customer machine:

- Run `telad` and expose only required ports.

Example (Windows workstation, RDP only):

```powershell
.\telad.exe -hub wss://acme-hub.example.com -machine ws-01 -ports "3389" -token <agent-token>
```

Example (Linux server, SSH only):

```bash
./telad -hub wss://acme-hub.example.com -machine srv-01 -ports "22" -token <agent-token>
```

For persistent deployment, install `telad` as an OS service (see [services.md](services.md)).

### Pattern B - Customer-site gateway

Use this when you can't install `telad` on endpoints.

- Run `telad` on a small gateway device that can reach internal targets.
- Configure one machine entry per target with `target: <LAN IP>`.

Example `telad.yaml`:

```yaml
hub: wss://acme-hub.example.com
token: "<agent-token>"
machines:
  - name: ws-01
    ports: [3389]
    target: 192.168.1.10
  - name: srv-01
    ports: [22]
    target: 192.168.1.20
```

---

## Step 3 - Technician workflow

On the tech's machine:

1. Download `tela` (on-demand) and verify checksum.
2. List machines:

```bash
./tela machines -hub wss://acme-hub.example.com -token <tech-token>
```

3. Connect:

```bash
./tela connect -hub wss://acme-hub.example.com -machine ws-01 -token <tech-token>
```

4. Use RDP:

```powershell
mstsc /v:localhost
```

---

## Operational guidance

- Use naming conventions (customer + role + number).
- Expose only what you need.
- Prefer encrypted service protocols.
- Treat the gateway (if used) as critical infrastructure.
- Use separate tokens per technician so access can be revoked individually.

---

## Troubleshooting

### RDP opens but can't log in

- Tela only transports TCP. Windows auth policies still apply.

### Endpoint agent can't connect out

- Check customer firewall allows outbound HTTPS.

### `telad` logs "auth_required"

- Check that the `-token` flag or `token:` config field is set and the token is valid.
- Verify the identity has been granted access to register the machine.

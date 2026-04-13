# HOWTO - MSP / IT Support (Tela)

This guide shows how an IT support provider can use Tela to reach customer machines (SSH, RDP, internal admin UIs) without asking customers to open inbound ports.

---

## Recommended topology

For MSP-style support, there are two common models:

1. **One hub per customer** (recommended isolation)
2. **One hub for multiple customers** (requires careful naming and stricter access controls)

This guide assumes **one hub per customer**.

---

## Step 1 - Deploy a hub for a customer

1. Deploy the hub on infrastructure you control.
2. Publish a customer-specific URL (example: `wss://acme-hub.example.com`).
3. Ensure WebSockets work.

See [Run a hub on the public internet](hub.md) for the full hub deployment guide, including TLS setup and firewall rules.

---

## Step 2 - Set up authentication

The hub prints an owner token on first start. Save it, then create identities for the customer's machines and your technicians:

```bash
# Create an agent token for the customer's machines
tela admin tokens add acme-agent -hub wss://acme-hub.example.com -token <owner-token>
# Save the printed token -- it is not shown again

# Grant the agent permission to register each machine
tela admin access grant acme-agent ws-01 register -hub wss://acme-hub.example.com -token <owner-token>
tela admin access grant acme-agent srv-01 register -hub wss://acme-hub.example.com -token <owner-token>

# Create technician tokens (one per technician so access can be revoked individually)
tela admin tokens add tech-bob -hub wss://acme-hub.example.com -token <owner-token>
tela admin access grant tech-bob ws-01 connect -hub wss://acme-hub.example.com -token <owner-token>
tela admin access grant tech-bob srv-01 connect -hub wss://acme-hub.example.com -token <owner-token>
```

See [Run a hub on the public internet](hub.md) for the full list of `tela admin` commands.

---

## Step 3 - Register customer machines

### Pattern A - Endpoint agent (preferred)

On each customer machine, run `telad` and expose only required ports.

Example (Windows workstation, RDP only):

```powershell
telad.exe -hub wss://acme-hub.example.com -machine ws-01 -ports "3389" -token <agent-token>
```

Example (Linux server, SSH only):

```bash
telad -hub wss://acme-hub.example.com -machine srv-01 -ports "22" -token <agent-token>
```

For persistent deployment, install `telad` as an OS service (see [Run Tela as an OS service](services.md)).

### Pattern B - Customer-site gateway

Use this when you can't install `telad` on individual endpoints. Run `telad` on a small gateway device that can reach internal targets, and configure one machine entry per target.

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

## Step 4 - Technician workflow

On the technician's machine:

1. Download `tela` and verify the checksum.
2. List machines:

```bash
tela machines -hub wss://acme-hub.example.com -token <tech-token>
```

3. Connect:

```bash
tela connect -hub wss://acme-hub.example.com -machine ws-01 -token <tech-token>
```

4. Use the local address shown in the output. For RDP:

```powershell
mstsc /v:127.88.x.x
```

---

## Operational guidance

- Use naming conventions (customer + role + number).
- Expose only what you need.
- Prefer encrypted service protocols.
- Treat the gateway (if used) as critical infrastructure.

---

## Troubleshooting

### RDP opens but can't log in

- Tela only transports TCP. Windows authentication policies still apply.

### Endpoint agent can't connect out

- Check the customer firewall allows outbound HTTPS.

### `telad` logs "auth_required"

- Check that the `-token` flag or `token:` config field is set and the token is valid.
- Verify the identity has been granted `register` access to the machine.

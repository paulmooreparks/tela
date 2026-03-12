# HOWTO - IoT / Edge Device Management (Tela)

This guide shows how to use Tela to manage devices deployed behind NAT/firewalls you don't control (Raspberry Pi, kiosks, industrial controllers). The primary goal is reliable outbound-only SSH (and optionally web admin ports) without requiring port forwards.

---

## Choose a deployment pattern

- **Pattern A (Endpoint agent)**: run `telad` on each device.
- **Pattern B (Site gateway / bridge)**: run one `telad` at the customer site that can reach many devices.

Pattern A is simplest per-device. Pattern B reduces software footprint on devices but increases the importance of gateway hardening.

---

## Step 1 - Run a hub reachable from anywhere

Deploy the hub somewhere reachable over HTTPS/WebSockets.

Quick start:

```bash
docker compose up --build -d
```

Publish it as `wss://YOUR-HUB`.

See [hub.md](hub.md) for the full hub deployment guide, including TLS and firewall setup.

---

## Step 1.5 - Enable authentication (recommended)

IoT devices on remote networks should always use authenticated connections:

```bash
# On the hub machine (or via Docker env var TELA_OWNER_TOKEN)
telahubd user bootstrap
# → Save the owner token

# Create an identity for each device (or one shared agent identity)
tela admin add-token device-agent -hub wss://YOUR-HUB -token <owner-token>
tela admin grant device-agent kiosk-001 -hub wss://YOUR-HUB -token <owner-token>
tela admin grant device-agent kiosk-002 -hub wss://YOUR-HUB -token <owner-token>

# Create an operator identity
tela admin add-token operator -hub wss://YOUR-HUB -token <owner-token>
tela admin grant operator kiosk-001 -hub wss://YOUR-HUB -token <owner-token>
tela admin grant operator kiosk-002 -hub wss://YOUR-HUB -token <owner-token>
```

See [hub.md](hub.md) for the full list of `tela admin` commands.

---

## Step 2 - Endpoint pattern: install and run `telad` on a device

### 2.1 Install `telad`

Options:

- Download a prebuilt `telad` from GitHub Releases (recommended).
- Or build from source on a build machine and copy the binary.

### 2.2 Create a minimal config

Example `telad.yaml` on the device:

```yaml
hub: wss://YOUR-HUB
token: "<device-agent-token>"
machines:
  - name: kiosk-001
    services:
      - port: 22
        name: SSH
    target: 127.0.0.1
```

Run:

```bash
./telad -config telad.yaml
```

### 2.3 Run as a service (recommended)

For persistent operation, install `telad` as a service:

```bash
telad service install -config telad.yaml
telad service start
```

See [services.md](services.md) for platform-specific details.

---

## Step 3 - Site gateway pattern (bridge many devices)

Run one gateway VM/device at the site.

Example `telad.yaml`:

```yaml
hub: wss://YOUR-HUB
token: "<device-agent-token>"
machines:
  - name: kiosk-001
    services:
      - port: 22
        name: SSH
    target: 192.168.10.21
  - name: kiosk-002
    services:
      - port: 22
        name: SSH
    target: 192.168.10.22
```

Run on the gateway:

```bash
./telad -config telad.yaml
```

Hardening guidance for gateways:

- Put the gateway in a dedicated subnet.
- Allowlist only required egress (hub URL).
- Allowlist only required internal targets/ports.

---

## Step 4 - Operator workflow with `tela`

From your laptop:

1. Download `tela` from GitHub Releases.
2. Verify the checksum.
3. List machines:

```bash
./tela machines -hub wss://YOUR-HUB -token <operator-token>
```

4. Connect to a device:

```bash
./tela connect -hub wss://YOUR-HUB -machine kiosk-001 -token <operator-token>
```

5. SSH to localhost:

```bash
ssh localhost
```

---

## Troubleshooting

### Device flaps online/offline

- Check device power/network stability.
- Check whether outbound HTTPS is allowed.

### `telad` logs "auth_required"

- Check that the `token:` field is set in `telad.yaml` and the token is valid.
- Verify the identity has been granted access to the machine: `tela admin grant <id> <machine> ...`

### SSH connects but auth fails

- Tela is only the transport. SSH auth is still handled by the device's SSH server.

### Gateway can't reach targets

- Confirm routing and firewall rules inside the site.
- Validate `target` addresses from the gateway itself.

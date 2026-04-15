# IoT and edge devices

## The scenario

You have devices deployed in the field: Raspberry Pis running sensor software, kiosks at retail locations, industrial controllers at manufacturing sites, point-of-sale terminals at customer premises. These devices sit behind NATs and firewalls that you do not control and cannot configure. Getting SSH access to any of them for maintenance currently requires coordinating with the site's IT team to open a port, or shipping the device back, or driving out.

With Tela, each device runs `telad` and makes an outbound connection to a central hub. From that point, you can SSH into any registered device from your workstation without any firewall changes at the site. The hub never has access to the device's filesystem or credentials -- it only relays the encrypted tunnel.

When you need to reach a device fleet, your workstation sees:

```
Services available:
  localhost:22     → SSH          (kiosk-store-042)
  localhost:10022  → SSH          (kiosk-store-107)
  localhost:8080   → HTTP         (controller-plant-a)
```

Devices that go offline (power loss, network interruption) reconnect automatically when they come back. You get consistent SSH access regardless of where a device is deployed or what the local network looks like.

## Choose a deployment pattern

- **Pattern A (Endpoint agent)**: run `telad` on each device.
- **Pattern B (Site gateway / bridge)**: run one `telad` at the customer site that can reach many devices.

Pattern A is simplest per device. Pattern B reduces software footprint on devices but increases the importance of gateway hardening.

---

## Step 1 - Run a hub reachable from anywhere

See [Run a hub on the public internet](../howto/hub.md) for the full deployment guide, including TLS and firewall setup. For a quick start on a host with a public address:

```bash
telahubd
```

The hub prints an owner token on first start. Save it. Publish the hub as `wss://hub.example.com`.

---

## Step 2 - Set up authentication

IoT devices on remote networks should always use authenticated connections:

```bash
# Create an agent token (one per device, or one shared identity)
tela admin tokens add device-agent -hub wss://hub.example.com -token <owner-token>
# Save the printed token -- this is <device-agent-token> used in telad.yaml on each device (Step 3)

# Grant the agent permission to register each device
tela admin access grant device-agent kiosk-001 register -hub wss://hub.example.com -token <owner-token>
tela admin access grant device-agent kiosk-002 register -hub wss://hub.example.com -token <owner-token>

# Create an operator token
tela admin tokens add operator -hub wss://hub.example.com -token <owner-token>
# Save the printed token -- this is <operator-token> used with tela connect (Step 5)
tela admin access grant operator kiosk-001 connect -hub wss://hub.example.com -token <owner-token>
tela admin access grant operator kiosk-002 connect -hub wss://hub.example.com -token <owner-token>
```

See [Run a hub on the public internet](../howto/hub.md) for the full list of `tela admin` commands.

---

## Step 3 - Endpoint pattern: install and run `telad` on a device

### 3.1 Install `telad`

Download a prebuilt `telad` from GitHub Releases and copy the binary to the device.

### 3.2 Create a minimal config

Example `telad.yaml` on the device:

```yaml
hub: wss://hub.example.com
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
telad -config telad.yaml
```

### 3.3 Run as a service (recommended)

For persistent operation, install `telad` as a service:

```bash
telad service install -config telad.yaml
telad service start
```

See [Run Tela as an OS service](../howto/services.md) for platform-specific details.

---

## Step 4 - Site gateway pattern (bridge many devices)

Run one gateway VM or device at the site. Configure one machine entry per target.

Example `telad.yaml`:

```yaml
hub: wss://hub.example.com
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
telad -config telad.yaml
```

Hardening guidance for gateways:

- Put the gateway in a dedicated subnet.
- Allowlist only required egress (hub URL).
- Allowlist only required internal targets and ports.

---

## Step 5 - Operator workflow with `tela`

From your laptop:

1. Download `tela` from GitHub Releases and verify the checksum.
2. List machines:

```bash
tela machines -hub wss://hub.example.com -token <operator-token>
```

3. Connect to a device:

```bash
tela connect -hub wss://hub.example.com -machine kiosk-001 -token <operator-token>
```

4. SSH to the address shown in the output:

```bash
ssh -p PORT localhost
```

---

## Troubleshooting

### Device flaps online/offline

- Check device power and network stability.
- Check whether outbound HTTPS is allowed from the device.

### `telad` logs "auth_required"

- Check that the `token:` field is set in `telad.yaml` and the token is valid.
- Verify the identity has been granted `register` access to the machine.

### SSH connects but authentication fails

- Tela is only the transport. SSH authentication is still handled by the device's SSH server.

### Gateway can't reach targets

- Confirm routing and firewall rules inside the site.
- Validate `target` addresses from the gateway host itself.

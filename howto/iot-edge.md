# HOWTO — IoT / Edge Device Management (Tela)

This guide shows how to use Tela to manage devices deployed behind NAT/firewalls you don’t control (Raspberry Pi, kiosks, industrial controllers). The primary goal is reliable outbound-only SSH (and optionally web admin ports) without requiring port forwards.

---

## Choose a deployment pattern

- **Pattern A (Endpoint agent)**: run `telad` on each device.
- **Pattern B (Site gateway / bridge)**: run one `telad` at the customer site that can reach many devices.

Pattern A is simplest per-device. Pattern B reduces software footprint on devices but increases the importance of gateway hardening.

---

## Step 1 — Run a hub reachable from anywhere

Deploy the hub somewhere reachable over HTTPS/WebSockets.

Quick start:

```bash
docker compose up --build -d
```

Publish it as `wss://YOUR-HUB`.

---

## Step 2 — Endpoint pattern: install and run `telad` on a device

### 2.1 Install `telad`

Options:

- Download a prebuilt `telad` from GitHub Releases (recommended).
- Or build from source on a build machine and copy the binary.

### 2.2 Create a minimal config

Example `telad.yaml` on the device:

```yaml
hub: wss://YOUR-HUB
machines:
  - name: kiosk-001
    services:
      - port: 22
        proto: tcp
        name: SSH
    target: 127.0.0.1
```

Run:

```bash
./telad -config telad.yaml
```

### 2.3 Run as a service (recommended)

For Linux devices, use your init system (systemd, OpenRC, etc.) to keep `telad` running and restarting.

Key settings:

- Restart on failure
- Log capture
- Start after networking

---

## Step 3 — Site gateway pattern (bridge many devices)

Run one gateway VM/device at the site.

Example `telad.yaml`:

```yaml
hub: wss://YOUR-HUB
machines:
  - name: kiosk-001
    services:
      - port: 22
        proto: tcp
        name: SSH
    target: 192.168.10.21
  - name: kiosk-002
    services:
      - port: 22
        proto: tcp
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

## Step 4 — Operator workflow with `tela`

From your laptop:

1. Download `tela` from GitHub Releases.
2. Verify the checksum.
3. List machines:

```bash
./tela machines -hub wss://YOUR-HUB
```

4. Connect to a device:

```bash
./tela connect -hub wss://YOUR-HUB -machine kiosk-001
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

### SSH connects but auth fails

- Tela is only the transport. SSH auth is still handled by the device’s SSH server.

### Gateway can’t reach targets

- Confirm routing and firewall rules inside the site.
- Validate `target` addresses from the gateway itself.

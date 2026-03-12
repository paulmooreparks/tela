# How to deploy a Tela Hub

A **Tela Hub** is the rendezvous + relay point that:

- accepts outbound connections from `tela` (clients) and `telad` (daemons)
- pairs them into sessions
- relays **end-to-end encrypted** tunnel traffic (the Hub never sees plaintext)
- exposes lightweight status endpoints used by portals (`/api/status`, `/api/history`)
- serves a built-in web console for quick status checks

## Hub server: `telahubd`

`telahubd` is the Go-native hub server. Single binary, no runtime dependencies. It serves HTTP, WebSocket relay, and UDP relay on one process.

### Install (bare-metal)

Pre-built binaries for Windows, Linux, and macOS are available on the [GitHub Releases](https://github.com/paulmooreparks/tela/releases) page.

#### Linux (amd64)

```bash
# Download
curl -Lo telahubd https://github.com/paulmooreparks/tela/releases/latest/download/telahubd-linux-amd64
chmod +x telahubd
sudo mv telahubd /usr/local/bin/

# Bootstrap auth (creates /etc/tela/telahubd.yaml with an owner token)
sudo telahubd user bootstrap
# → SAVE THE PRINTED TOKEN — it will not be shown again.

# Start the hub
sudo telahubd
```

For ARM64 (Raspberry Pi, AWS Graviton, etc.), replace `amd64` with `arm64` in the download URL.

To run as a systemd service instead of in the foreground:

```bash
# Install the service (creates /etc/systemd/system/telahubd.service)
sudo telahubd service install -name myhub -port 80

# Start it
sudo telahubd service start

# Check logs
journalctl -u telahubd -f
```

#### macOS (Apple Silicon)

```bash
# Download
curl -Lo telahubd https://github.com/paulmooreparks/tela/releases/latest/download/telahubd-darwin-arm64
chmod +x telahubd
sudo mv telahubd /usr/local/bin/

# Bootstrap auth
sudo telahubd user bootstrap

# Start the hub
telahubd
```

For Intel Macs, replace `arm64` with `amd64` in the download URL.

To run as a launchd service:

```bash
sudo telahubd service install -name myhub -port 80
sudo telahubd service start
```

#### Windows (amd64)

```powershell
# Download (PowerShell)
Invoke-WebRequest -Uri https://github.com/paulmooreparks/tela/releases/latest/download/telahubd-windows-amd64.exe -OutFile telahubd.exe

# Bootstrap auth (creates %ProgramData%\Tela\telahubd.yaml)
.\telahubd.exe user bootstrap
# → SAVE THE PRINTED TOKEN

# Start the hub
.\telahubd.exe
```

To install as a Windows service (run from an elevated/Administrator prompt):

```powershell
.\telahubd.exe service install -name myhub -port 80
.\telahubd.exe service start
```

### Build from source

```bash
go build -o telahubd ./cmd/telahubd
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HUB_PORT` | `80` | HTTP + WebSocket listen port |
| `HUB_UDP_PORT` | `41820` | UDP relay port |
| `HUB_UDP_HOST` | *(empty)* | Public IP/hostname advertised in UDP offers (for proxy/tunnel setups) |
| `HUB_NAME` | *(empty)* | Display name shown in portal and `/api/status` |
| `HUB_WWW_DIR` | `./www` | Directory for static console files |

### Run locally

```bash
# Minimal - listens on :80 (HTTP+WS) and :41820 (UDP)
telahubd

# With a display name
HUB_NAME=myhub telahubd

# Custom ports
HUB_PORT=9090 HUB_UDP_PORT=9091 telahubd

# Behind Cloudflare/proxy — advertise real IP for UDP relay
HUB_UDP_HOST=myhost.example.com telahubd
```

### Run with Docker

The Tela repo includes a ready-made Dockerfile at `docker/gohub/Dockerfile`:

```bash
docker build -f docker/gohub/Dockerfile -t telahubd .
docker run -d --name telahubd \
  -p 80:80 \
  -p 41820:41820/udp \
  -e HUB_NAME=myhub \
  -e HUB_UDP_HOST=myhost.example.com \
  telahubd
```

Or use Docker Compose (see the `gohub` service in `docker-compose.yml`):

```bash
docker compose up -d --build gohub
```

The Docker Compose setup uses a named volume (`hub-data`) to persist the hub config at `/app/data/telahubd.yaml`, so auth configuration survives container recreation.

### Verify

```bash
# Check hub status
curl http://localhost/api/status

# Check session history
curl http://localhost/api/history

# Check version
telahubd version
```

## Enable authentication (recommended)

By default, the hub runs in **open mode**. Any agent or client can connect without credentials. To lock it down, enable token-based authentication.

### Docker deployment (env-var bootstrap)

The simplest path for Docker:

```bash
# 1. Generate an owner token on your local machine
openssl rand -hex 32

# 2. Add to docker-compose.yml environment block:
#    - TELA_OWNER_TOKEN=<your-token>

# 3. Redeploy
docker compose up --build -d
```

On first startup the hub creates an `owner` identity and a `console-viewer` identity (viewer role for the built-in web console), persists them, and is ready for remote management.

### Bare-metal / direct deployment

If running `telahubd` directly (not in Docker), use `user bootstrap` to generate the first owner token:

```bash
sudo telahubd user bootstrap
```

This creates `/etc/tela/telahubd.yaml` (or `%ProgramData%\Tela\telahubd.yaml` on Windows) with an owner identity and a wildcard machine ACL. See the [Install (bare-metal)](#install-bare-metal) section above for the full curl-to-running walkthrough.

Alternatively, set `TELA_OWNER_TOKEN` as an environment variable before starting, or create a config file manually (see [CONFIGURATION.md](../CONFIGURATION.md)).

### Managing tokens remotely with `tela admin`

Once the owner token exists, manage everything from any workstation:

```bash
# List identities on the hub
tela admin list-tokens -hub wss://your-hub.example.com -token <owner-token>

# Add a user identity
tela admin add-token alice -hub wss://your-hub.example.com -token <owner-token>
# → Save the printed token!

# Add an admin
tela admin add-token bob -hub wss://your-hub.example.com -token <owner-token> -role admin

# Grant connect access to a machine
tela admin grant alice barn -hub wss://your-hub.example.com -token <owner-token>

# Revoke access
tela admin revoke alice barn -hub wss://your-hub.example.com -token <owner-token>

# Rotate a compromised token
tela admin rotate alice -hub wss://your-hub.example.com -token <owner-token>

# Remove an identity entirely
tela admin remove-token alice -hub wss://your-hub.example.com -token <owner-token>
```

All changes take effect immediately (hot-reload). No hub restart required.

### Managing portals remotely with `tela admin`

Register your hub with a portal directory (like Awan Saya) from any workstation:

```bash
# Register hub with a portal
tela admin add-portal awansaya -hub wss://your-hub.example.com -token <owner-token> \
  -portal-url https://awansaya.net -portal-token <portal-admin-token>

# List portal registrations
tela admin list-portals -hub wss://your-hub.example.com -token <owner-token>

# Remove a portal registration
tela admin remove-portal awansaya -hub wss://your-hub.example.com -token <owner-token>
```

Note: `-token` is the **hub's** owner token (authenticates you to the hub). `-portal-token` is the **portal's** admin API token (used by the hub to register with the portal). The `-hub-url` flag is optional — it defaults to the HTTPS form of `-hub`.

### Using `telad` with auth

When the hub has auth enabled, agents must provide a valid token:

```yaml
# telad.yaml
hub: wss://your-hub.example.com
token: "<token-for-this-agent>"

machines:
  - name: barn
    ports: [22, 3389]
```

```bash
telad -config telad.yaml
```

Or with a flag: `telad -hub wss://... -machine barn -ports "22,3389" -token <token>`

### Using `tela` (client) with auth

```bash
tela connect -hub wss://your-hub.example.com -machine barn -token <your-token>

# Or set env vars:
export TELA_HUB=wss://your-hub.example.com
export TELA_TOKEN=<your-token>
tela connect -machine barn
```

## What must be reachable

| Port | Protocol | Required | Purpose |
|------|----------|----------|---------|
| 443 | TCP | Yes | HTTPS + WebSockets (clients and daemons connect here) |
| 80 | TCP | Yes* | ACME HTTP-01 challenge (Let's Encrypt cert issuance) and HTTP→HTTPS redirect |
| 41820 | UDP | Optional | UDP relay for faster WireGuard transport (falls back to WebSocket if blocked) |

\* Port 80 is required by Caddy for automatic certificate issuance. If you use DNS-01 challenges or bring your own certificate, you can skip it.

### Open firewall ports (cloud VMs)

Cloud VMs block inbound traffic by default. You must explicitly allow the ports above in your provider's firewall/security group.

**Azure (Network Security Group):**

```bash
az network nsg rule create --resource-group <rg> --nsg-name <nsg> \
  --name AllowTela --priority 1010 --direction Inbound \
  --access Allow --protocol Tcp --destination-port-ranges 80 443

az network nsg rule create --resource-group <rg> --nsg-name <nsg> \
  --name AllowTelaUDP --priority 1020 --direction Inbound \
  --access Allow --protocol Udp --destination-port-ranges 41820
```

Or in the Azure Portal: VM → Networking → Add inbound port rule.

**AWS (Security Group):**

```bash
aws ec2 authorize-security-group-ingress --group-id <sg-id> \
  --ip-permissions \
  IpProtocol=tcp,FromPort=80,ToPort=80,IpRanges='[{CidrIp=0.0.0.0/0}]' \
  IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges='[{CidrIp=0.0.0.0/0}]'

aws ec2 authorize-security-group-ingress --group-id <sg-id> \
  --ip-permissions \
  IpProtocol=udp,FromPort=41820,ToPort=41820,IpRanges='[{CidrIp=0.0.0.0/0}]'
```

Or in the AWS Console: EC2 → Security Groups → Edit inbound rules.

**GCP (Firewall rule):**

```bash
gcloud compute firewall-rules create allow-tela \
  --allow tcp:80,tcp:443,udp:41820 \
  --target-tags tela-hub
```

Then add the `tela-hub` network tag to your VM instance.

**Self-hosted / bare metal:** Ensure `ufw`, `iptables`, or your router forwards these ports to the hub machine.

## Publish with TLS (recommended)

Running the hub without TLS (`ws://`) works for local development, but production hubs should use TLS (`wss://`). This protects hub authentication tokens in transit and is required by browsers for the hub console over HTTPS.

The recommended approach is **Caddy** as a reverse proxy — it handles TLS certificates automatically via Let's Encrypt, supports WebSocket upgrade out of the box, and requires minimal configuration.

### Prerequisites

1. A **DNS A record** pointing your hub's hostname to the VM's public IP:
   ```
   myhub.example.com  →  203.0.113.42
   ```
2. **Ports 80 and 443** open inbound (see [firewall section](#open-firewall-ports-cloud-vms) above).
3. telahubd running on a local port (e.g., 8080) that Caddy will proxy to.

### Step 1: Move telahubd to a local port

Since Caddy needs ports 80 and 443, move telahubd to a non-privileged port that only Caddy can reach:

```bash
# Edit the hub config
sudo vi /etc/tela/telahubd.yaml
# Change: port: 8080

# Restart the service
sudo telahubd service stop
sudo telahubd service start

# Verify it's listening locally
curl http://localhost:8080/api/status
```

### Step 2: Install Caddy

**Debian / Ubuntu:**

```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install caddy
```

**RHEL / Fedora:**

```bash
sudo dnf install 'dnf-command(copr)'
sudo dnf copr enable @caddy/caddy
sudo dnf install caddy
```

**macOS:**

```bash
brew install caddy
```

### Step 3: Configure Caddy

```bash
sudo tee /etc/caddy/Caddyfile << 'EOF'
myhub.example.com {
    reverse_proxy localhost:8080
}
EOF
```

Replace `myhub.example.com` with your hub's actual hostname.

That's the entire config. Caddy automatically:
- Obtains a Let's Encrypt TLS certificate
- Renews it before expiry
- Redirects HTTP → HTTPS
- Proxies WebSocket upgrade headers

### Step 4: Start Caddy

```bash
sudo systemctl enable caddy
sudo systemctl restart caddy
```

### Step 5: Verify

```bash
# From any machine on the Internet
curl https://myhub.example.com/api/status

# Open the hub console in a browser
# https://myhub.example.com/

# Connect with the CLI
tela connect -hub wss://myhub.example.com -machine barn
telad -hub wss://myhub.example.com -machine barn -ports 22,3389
```

### Alternative: Cloudflare Tunnel (zero inbound ports)

If you don't want to expose any inbound ports, Cloudflare Tunnel makes an outbound connection to Cloudflare's edge, which terminates TLS and proxies traffic back to your hub.

```bash
# Install cloudflared
# See https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/

# Create a tunnel and configure ingress (~/.cloudflared/config.yml):
tunnel: <tunnel-id>
ingress:
  - hostname: myhub.example.com
    service: http://localhost:80
  - service: http_status:404

# Route DNS and run
cloudflared tunnel route dns my-hub myhub.example.com
cloudflared tunnel run my-hub
```

With Cloudflare Tunnel, telahubd can stay on port 80 (default) since Caddy isn't needed. Note that Cloudflare Tunnel is TCP-only — the UDP relay (port 41820) won't work through it, and sessions will use WebSocket transport instead.

### Alternative: nginx + certbot

```bash
sudo apt install nginx certbot python3-certbot-nginx

sudo tee /etc/nginx/sites-available/tela-hub << 'EOF'
server {
    listen 80;
    server_name myhub.example.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
EOF

sudo ln -s /etc/nginx/sites-available/tela-hub /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx

# Obtain TLS certificate (adds HTTPS config automatically)
sudo certbot --nginx -d myhub.example.com
```

## Register with a hub directory

Once the hub is reachable, add it to a hub directory (such as Awan Saya) so users and the CLI can find it by short name.

### Option A: CLI (recommended)

From any workstation with the hub's owner token:

```bash
tela admin add-portal awansaya \
  -hub wss://your-hub.example.com \
  -token <hub-owner-token> \
  -portal-url https://awansaya.net \
  -portal-token <portal-admin-token>
```

The hub will register itself with the portal, exchange viewer tokens for status proxying, and store a scoped sync token so future viewer-token updates happen automatically. The portal admin token is used once and is **not** stored on the hub.

### Option B: Portal dashboard

1. Open the portal dashboard and click **Add Hub**.
2. Enter a short name (e.g., `myhub`), the hub's public URL (e.g., `https://your-hub.example.com`), and optionally a **viewer token** (so the portal can proxy hub status server-side).

### After registration

The hub will appear in the portal dashboard and be resolvable by the CLI:

```bash
tela remote add myportal https://your-portal.example
tela machines -hub myhub
tela connect -hub myhub -machine mybox
```

## Verify from outside

From a machine on the Internet (or at least outside your LAN), verify:

- `GET https://<hub>/api/status` returns JSON with hub info.
- `GET https://<hub>/api/history` returns event history.
- Portal shows the hub card with status (validates CORS + reachability).

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `telad` never appears | Hub unreachable or WebSocket upgrade blocked | Confirm the hub URL is reachable externally (TCP 443 + WS) |
| Portal cards stay empty | Portal missing viewer token, or hub unreachable from portal server | Ensure the hub entry in the portal includes a valid viewer token |
| UDP relay not working | TCP-only tunnel or firewall | Confirm UDP `HUB_UDP_PORT` is open inbound on the hub and outbound from both sides |
| "Machine not found" | Machine isn't registered | Run `tela machines -hub <hub>` to list available machines; confirm `telad` is running and connected |



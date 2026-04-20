# Run a hub on the public internet

## What you are setting up

The hub is a single Linux (or Windows, or macOS) server sitting on the public internet with an inbound port open. It does not need to be powerful -- a $5/month virtual machine (VM) works fine. It runs `telahubd`, which serves a single endpoint that handles both WebSocket connections from agents and clients and a UDP relay for faster WireGuard transport.

Every agent and every client in your Tela deployment points at this hub. They connect outbound to it; it brokers the WireGuard sessions between them. It never decrypts tunnel traffic.

By the end of this chapter you will have:

- `telahubd` running either as a Docker container (recommended) or as a managed OS service
- A reverse proxy terminating TLS on port 443, typically Caddy with an auto-issued Let's Encrypt certificate
- An owner token secured and ready to use for administration
- UDP port 41820 open for faster tunnels (optional but worth doing)
- Optionally, the hub registered with a portal directory so clients can find it by name

The hub's public URL will be `wss://hub.example.com`. Agents use that URL in their `telad.yaml`. Clients use it in their connection profiles. Nothing else needs to change when you add new machines; they all find the hub the same way.

This chapter takes you from "I ran through the [First connection](../getting-started/first-connection.md) walkthrough" to a production-grade deployment with TLS, authentication, and a supervisor (Docker or the OS service manager) that keeps the hub running.

## Hub server: `telahubd`

`telahubd` is the Go-native hub server. Single binary, no runtime dependencies. It serves HTTP, WebSocket relay, and UDP relay on one process.

Two install paths are supported. Pick whichever suits the host.

- **Docker (recommended).** `docker compose up -d` with one of the ready-made templates. TLS via Caddy with automatic Let's Encrypt, three commands from a fresh VM to a running hub with a valid certificate. This is the default path the chapter walks through.
- **Native binary (alternative).** Download, install as an OS service, register with the service manager. Still fully supported and appropriate for operators who cannot or do not want to run Docker.

### Install: Docker (recommended)

Prerequisites: Docker Engine and Docker Compose plugin. Any modern Docker install ships both; `docker compose version` confirms the plugin is present.

This walkthrough deploys the Caddy-fronted production template. Caddy terminates TLS with an auto-issued Let's Encrypt certificate, telahubd runs behind it on the internal Docker network, and the UDP relay port is published directly from the host to telahubd because Caddy is TCP-only. Three templates are maintained in the tela repo under [`deploy/docker/`](https://github.com/paulmooreparks/tela/tree/main/deploy/docker); the "Choosing a different template" section below covers the other two.

#### Step 1. Point DNS

Point an `A` record for your hub's hostname (for example `hub.example.com`) at the Docker host's public IP. Let's Encrypt needs this to resolve correctly before it will issue the certificate; if DNS is wrong, the first `docker compose up` appears to hang on the Caddy logs at the ACME challenge step.

#### Step 2. Open firewall ports

Three inbound ports on the Docker host:

| Port | Protocol | Purpose |
|------|----------|---------|
| 80 | TCP | Let's Encrypt HTTP-01 challenge and the 301 to HTTPS. Caddy can be switched to DNS-01 if port 80 must stay closed; see the Caddy docs. |
| 443 | TCP | Hub HTTPS and WebSocket. |
| 41820 | UDP | UDP relay tier. See "The UDP gotcha" below. |

#### Step 3. Pull the compose template

Download the Caddy-fronted production template and its companions into a working directory on the Docker host:

```bash
mkdir -p /srv/telahubd && cd /srv/telahubd
BASE=https://raw.githubusercontent.com/paulmooreparks/tela/main/deploy/docker
curl -Lo docker-compose.yml "$BASE/docker-compose.caddy.yml"
curl -Lo Caddyfile          "$BASE/Caddyfile"
curl -Lo .env.example       "$BASE/.env.example"
cp .env.example .env
```

#### Step 4. Fill in `.env`

Edit `.env` and set at least two values:

```ini
TELA_OWNER_TOKEN=<run: openssl rand -hex 32>
HUB_DOMAIN=hub.example.com
```

Optionally also set `TELAHUBD_NAME` (display name shown in TelaVisor and portal listings) and `TELAHUBD_UDP_HOST` (only needed if the UDP relay path reaches the hub through a different hostname than `HUB_DOMAIN`).

Do not commit `.env` anywhere public; it contains the owner token.

#### Step 5. Bring it up

```bash
docker compose up -d
```

Caddy takes roughly a minute on first start to issue the Let's Encrypt certificate. `docker compose logs -f caddy` shows the ACME exchange; the final log line is `certificate obtained successfully` on `hub.example.com`.

Once the certificate is issued, verify:

```bash
curl https://hub.example.com/.well-known/tela
```

The response is a small JSON document with `hubId` and `protocolVersion` fields. If that is what you see, the hub is live.

#### Step 6. Confirm the owner token

The token is whatever you put in `.env`. To use it from a workstation:

```bash
tela login https://hub.example.com
# paste the token when prompted
```

If you left `TELA_OWNER_TOKEN` blank in `.env`, telahubd auto-generated one on first boot and logged it. Retrieve it either from `docker compose logs telahubd` (the boot banner prints the token once) or on demand:

```bash
docker exec telahubd telahubd user show-owner -config /data/telahubd.yaml
```

#### The UDP gotcha

Every compose template in this chapter publishes UDP port 41820 with the `/udp` suffix:

```yaml
ports:
  - "41820:41820/udp"
```

Without the suffix, Docker exposes only the TCP side. telahubd does not listen on TCP 41820, so the mapping silently does nothing, and every relay session falls back to WebSocket-over-TCP. The hub still works but round-trip latency roughly doubles and throughput is cut in half on sessions that would otherwise hole-punch to UDP.

If adapting one of the templates and sessions feel slow, `docker port <container-name>` should report `41820/udp`. If it reports `41820/tcp` instead, the suffix was dropped.

#### Choosing a different template

The Caddy template suits most production deployments. Two alternatives live alongside it:

| Template | Topology | When to pick it |
|----------|----------|-----------------|
| `docker-compose.caddy.yml` + `Caddyfile` | telahubd + Caddy with auto-Let's Encrypt | Production with a public hostname. This walkthrough. |
| `docker-compose.minimal.yml` | telahubd alone on port 80, no TLS | LAN-only dev or test. Never the public internet. |
| `docker-compose.nginx.yml` + `nginx.conf` | telahubd + nginx, bring your own certs | Operators who already run nginx and manage certificates via certbot, cert-manager, or similar. |

Switch templates by downloading a different compose file in step 3 above and re-running `docker compose up -d`. The `telahubd-data` named volume is reused across templates, so config and tokens persist when you switch topology.

Browse all three on GitHub: [tela/deploy/docker/](https://github.com/paulmooreparks/tela/tree/main/deploy/docker).

#### Upgrading

Docker-based upgrades use `docker pull` and a compose restart, not `telahubd update`:

```bash
docker compose pull
docker compose up -d
```

The named volume for `/data` survives container recreation, so config and tokens are preserved. To pin to a specific version instead of tracking `:stable`, edit the `image:` line in the compose file to `ghcr.io/paulmooreparks/telahubd:v0.13.0` or any other published tag.

### Install: native binary (alternative)

Pre-built binaries for Windows, Linux, and macOS are available on the [GitHub Releases](https://github.com/paulmooreparks/tela/releases) page. Choose this path if Docker is unavailable on the host, if you are running on Windows Server without Docker Desktop, or if you prefer integrating with the host's service manager directly.

The install flow has five steps. Do them in this order. The service install step writes a clean config file; the bootstrap step adds the owner token to that file; the service-start step reads the populated config. Running them out of order either duplicates tokens or leaves you starting the hub against a blank config.

#### Step 1. Pick a deployment model

| Model | `telahubd` port | Public port | TLS | Notes |
|-------|-----------------|-------------|-----|-------|
| **Caddy reverse proxy (recommended)** | 8080 | 443 | Automatic via Let's Encrypt | One-line Caddyfile. Simplest production setup. |
| **nginx + certbot** | 8080 | 443 | Let's Encrypt via certbot | Common on existing web servers. |
| **Apache httpd + certbot** | 8080 | 443 | Let's Encrypt via certbot | Needs `mod_proxy`, `mod_proxy_http`, `mod_proxy_wstunnel`, and certbot. |
| **Cloudflare Tunnel** | 80 | 443 (Cloudflare edge) | Terminated at Cloudflare | No inbound ports required. UDP relay unavailable. |
| **Direct (dev / private networks only)** | 80 | 80 | None | Tokens travel in plaintext over `ws://`. Do not use for production. |

`telahubd` binds its port on all interfaces, so for any of the proxy models above you must block external access to that port at the firewall. Only the reverse proxy should be able to reach it, over localhost. Proxy setup details live in [Publish with TLS](#publish-with-tls-recommended) further down. Decide the port now because `service install` in step 3 writes that port into the config.

#### Step 2. Download the binary

Replace `amd64` with `arm64` for ARM hardware (Raspberry Pi, AWS Graviton, Apple Silicon). On macOS Apple Silicon use `darwin-arm64`; on Intel Macs use `darwin-amd64`.

**Linux:**

```bash
curl -Lo telahubd https://github.com/paulmooreparks/tela/releases/latest/download/telahubd-linux-amd64
chmod +x telahubd
sudo mv telahubd /usr/local/bin/
```

**macOS:**

```bash
curl -Lo telahubd https://github.com/paulmooreparks/tela/releases/latest/download/telahubd-darwin-arm64
chmod +x telahubd
sudo mv telahubd /usr/local/bin/
```

**Windows (elevated PowerShell):**

```powershell
New-Item -ItemType Directory -Force "C:\Program Files\Tela" | Out-Null
Invoke-WebRequest -Uri https://github.com/paulmooreparks/tela/releases/latest/download/telahubd-windows-amd64.exe `
  -OutFile "C:\Program Files\Tela\telahubd.exe"
```

Add `C:\Program Files\Tela` to the system `PATH` so later commands resolve the binary. The service install step below records the absolute path in the Windows service definition regardless, so `PATH` is only needed for interactive use.

#### Step 3. Install the OS service

This writes a fresh YAML config to the platform-standard location and registers the service with the OS. No tokens are written yet.

| Platform | Config file written |
|----------|--------------------|
| Linux, macOS | `/etc/tela/telahubd.yaml` |
| Windows | `%ProgramData%\Tela\telahubd.yaml` |

Use the port you picked in step 1 (`8080` if you are putting a proxy in front, `80` for direct or Cloudflare Tunnel). If you omit `-name`, you can set a display name later by editing the config.

**Linux / macOS:**

```bash
sudo telahubd service install -name myhub -port 8080
```

**Windows (elevated):**

```powershell
.\telahubd.exe service install -name myhub -port 8080
```

If the file at the path above already exists with tokens (for example, because you ran `user bootstrap` first), `service install` refuses to overwrite it. The error message tells you to re-run with an explicit `-config` flag pointing at the existing file:

```bash
sudo telahubd service install -config /etc/tela/telahubd.yaml
```

That keeps the existing tokens and just registers the OS service. If you took this path, skip step 4 (the tokens are already there) and continue to step 5. To change the port or hub name after the fact, stop the service, edit the YAML file directly, and start it again.

#### Step 4. Bootstrap the owner token

This adds an `owner` identity to the config file from step 3 and prints the token once. Save it immediately. You use this token to register agents, run `tela admin`, and sign into TelaVisor as an administrator.

**Linux / macOS:**

```bash
sudo telahubd user bootstrap
```

**Windows (elevated):**

```powershell
.\telahubd.exe user bootstrap
```

The token will not be shown again. Store it in a password manager. For day-to-day agent and client connections, create lower-privilege tokens with `tela admin tokens add` (see [Authentication](#authentication) below).

#### Step 5. Start the service

**Linux / macOS:**

```bash
sudo telahubd service start

# Follow logs
sudo journalctl -u telahubd -f        # systemd (Linux)
sudo tail -f /var/log/telahubd.log    # launchd (macOS)
```

**Windows (elevated):**

```powershell
.\telahubd.exe service start
Get-Content "C:\ProgramData\Tela\telahubd.log" -Tail 20 -Wait
```

Verify the hub is listening locally:

```bash
curl http://localhost:8080/api/status   # (or port 80 for direct/Cloudflare deployments)
```

You should see a JSON response with `hub`, `version`, and connection counts. If you picked a proxy model, continue to [Publish with TLS](#publish-with-tls-recommended) to configure it. If you picked direct, the hub is already reachable on port 80 and you can skip ahead to [Register with a hub directory](#register-with-a-hub-directory).

### Running in the foreground (dev only)

For local testing, you can skip the service install and run `telahubd` directly from a terminal. It looks for a config in this order:

1. The `-config` path passed on the command line, if any.
2. `./data/telahubd.yaml` relative to the current working directory.
3. The platform-standard path (`/etc/tela/telahubd.yaml` on Linux/macOS, `%ProgramData%\Tela\telahubd.yaml` on Windows).

If none of those exist, `telahubd` generates a fresh owner token, writes `./data/telahubd.yaml` relative to the current working directory, and prints the token to stdout.

```bash
sudo telahubd              # uses /etc/tela/telahubd.yaml if it exists
telahubd -config my.yaml   # explicit config path
```

Do not start the service and run `telahubd` in the foreground at the same time. Both try to bind the same listening port, and the second one will fail.

### Build from source

```bash
go build -o telahubd ./cmd/telahubd
```

### Environment variables

Environment variables override the YAML config file at runtime, useful for container deployments or quick experiments without editing `/etc/tela/telahubd.yaml`.

| Variable | Default | Description |
|----------|---------|-------------|
| `TELAHUBD_PORT` | `80` | HTTP + WebSocket listen port |
| `TELAHUBD_UDP_PORT` | `41820` | UDP relay port |
| `TELAHUBD_UDP_HOST` | *(empty)* | Public IP/hostname advertised in UDP offers (for proxy/tunnel setups) |
| `TELAHUBD_NAME` | *(empty)* | Display name shown in portal and `/api/status` |
| `TELAHUBD_WWW_DIR` | *(empty)* | Serve hub console from disk instead of embedded files |
| `TELA_OWNER_TOKEN` | *(empty)* | Bootstrap owner token on first startup; ignored if tokens already exist |
| `TELAHUBD_PORTAL_URL` | *(empty)* | Portal URL for auto-registration on first startup |
| `TELAHUBD_PORTAL_TOKEN` | *(empty)* | Portal admin token for auto-registration |
| `TELAHUBD_PUBLIC_URL` | *(empty)* | This hub's own public URL, used when registering with a portal |

```bash
TELAHUBD_PORT=9090 TELAHUBD_UDP_PORT=9091 telahubd
TELAHUBD_UDP_HOST=myhost.example.com telahubd    # advertise real IP for UDP
```

## Authentication

> **Docker install**: the Docker walkthrough above already set the owner token via `TELA_OWNER_TOKEN` in `.env` and captured it to your password manager. Skip to [Managing tokens remotely with `tela admin`](#managing-tokens-remotely-with-tela-admin) below.

The owner token generated by `telahubd user bootstrap` in step 4 of the native install flow is the highest-privilege credential on the hub. An identity with the owner role can add and remove all other identities, change permissions, restart the hub, and perform every administrative operation. Treat it like a root password: store it in a password manager or secrets vault, do not paste it into scripts or shell history, and do not distribute it to agents or end users.

In normal operation, the owner token is used only from a trusted administrator workstation to run `tela admin` commands. Day-to-day agent connections and user connections use tokens you create with `tela admin tokens add`, which carry the `user` role and are scoped to specific machines via the access control list.

If you need an open hub (no authentication), remove all tokens from the config file and restart. The hub will log a warning when running in open mode.

### Alternatives to `user bootstrap`

The `user bootstrap` step is one way to install the owner token. Two alternatives:

- **Hand-author the YAML file.** See [Appendix B: Configuration file reference](../guide/configuration.md) for the shape. Useful when the token is managed by a secrets provisioning tool.
- **`TELA_OWNER_TOKEN` env var (foreground only).** When the variable is set and the config has no tokens, `telahubd` writes it into the config on first startup. The env var is only visible to the running process, so this works for `telahubd` launched directly in a shell (or a container with the variable set at runtime). Services launched by systemd, launchd, or Windows SCM do not inherit shell environment variables, so the env-var path does not apply to `service start`; use `user bootstrap` there.

### Managing tokens remotely with `tela admin`

Once the owner token exists, manage everything from any workstation:

```bash
# List identities on the hub
tela admin tokens list -hub wss://your-hub.example.com -token <owner-token>

# Add a user identity
tela admin tokens add alice -hub wss://your-hub.example.com -token <owner-token>
# → Save the printed token!

# Add an admin
tela admin tokens add bob -hub wss://your-hub.example.com -token <owner-token> -role admin

# Grant connect access to a machine
tela admin access grant alice barn connect -hub wss://your-hub.example.com -token <owner-token>

# Revoke access
tela admin access revoke alice barn -hub wss://your-hub.example.com -token <owner-token>

# Rotate a compromised token
tela admin rotate alice -hub wss://your-hub.example.com -token <owner-token>

# Remove an identity entirely
tela admin tokens remove alice -hub wss://your-hub.example.com -token <owner-token>
```

All changes take effect immediately (hot-reload). No hub restart required.

### Managing portals remotely with `tela admin`

Register your hub with a portal directory (like Awan Saya) from any workstation:

```bash
# Register hub with a portal
tela admin portals add awansaya -hub wss://your-hub.example.com -token <owner-token> \
  -portal-url https://awansaya.net

# List portal registrations
tela admin portals list -hub wss://your-hub.example.com -token <owner-token>

# Remove a portal registration
tela admin portals remove awansaya -hub wss://your-hub.example.com -token <owner-token>
```

### Using `telad` with auth

When the hub has auth enabled, agents must present a valid token. Do not use
the owner token here. Create a dedicated agent identity with `tela admin tokens
add` (user role) and grant it register permission on the relevant machine. See
[Run an agent](telad.md) for the full setup.

```yaml
# telad.yaml
hub: wss://your-hub.example.com
token: "<agent-token>"   # user-role token with register permission on this machine

machines:
  - name: barn
    ports: [22, 3389]
```

```bash
telad -config telad.yaml
```

Or with a flag: `telad -hub wss://... -machine barn -ports "22,3389" -token <agent-token>`

### Using `tela` (client) with auth

Client connections use a user-role token with connect permission on the target
machine. Do not use the owner token for routine client connections. Create a
dedicated identity for each user or workstation with `tela admin tokens add`.

```bash
tela connect -hub wss://your-hub.example.com -machine barn -token <user-token>

# Or set env vars:
export TELA_HUB=wss://your-hub.example.com
export TELA_TOKEN=<user-token>
tela connect -machine barn
```

## What must be reachable

| Port | Protocol | Required | Purpose |
|------|----------|----------|---------|
| 443 | TCP | Yes | HTTPS + WebSockets (clients and daemons connect here) |
| 80 | TCP | Yes* | ACME HTTP-01 challenge (Let's Encrypt cert issuance) and HTTP to HTTPS redirect |
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

> **Docker install**: the Docker walkthrough above already configured Caddy and Let's Encrypt via `docker-compose.caddy.yml`. Skip to [Register with a hub directory](#register-with-a-hub-directory) below. The subsections here apply to native installs that need a separately-managed reverse proxy.

Running the hub without TLS (`ws://`) works for local development, but production hubs should use TLS (`wss://`). This protects hub authentication tokens in transit and is required by browsers for the hub console over HTTPS.

The recommended approach is **Caddy** as a reverse proxy. It handles TLS certificates automatically via Let's Encrypt, supports WebSocket upgrade out of the box, and requires minimal configuration.

### Prerequisites

1. A **DNS A record** pointing your hub's hostname to the VM's public IP:
   ```
   myhub.example.com  →  203.0.113.42
   ```
2. **Ports 80 and 443** open inbound (see [firewall section](#open-firewall-ports-cloud-vms) above).
3. `telahubd` running on a local port (`8080` if you followed step 3 of the install flow above) that the proxy will forward to. Verify:
   ```bash
   curl http://localhost:8080/api/status
   ```
   If you installed with `-port 80` instead, stop the service, edit `/etc/tela/telahubd.yaml` to change `port: 8080`, and start it again.

### Step 1: Install Caddy

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

**Red Hat Enterprise Linux (RHEL) / Fedora:**

```bash
sudo dnf install 'dnf-command(copr)'
sudo dnf copr enable @caddy/caddy
sudo dnf install caddy
```

**macOS:**

```bash
brew install caddy
```

### Step 2: Configure Caddy

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
- Redirects HTTP to HTTPS
- Proxies WebSocket upgrade headers

### Step 3: Start Caddy

```bash
sudo systemctl enable caddy
sudo systemctl restart caddy
```

### Step 4: Verify

```bash
# From any machine on the Internet
curl https://myhub.example.com/api/status

# Open the hub console in a browser
# https://myhub.example.com/

# Connect with the CLI
tela connect -hub wss://myhub.example.com -machine barn -token <your-token>
telad -hub wss://myhub.example.com -machine barn -ports 22,3389 -token <agent-token>
```

### Alternative: nginx + certbot

Use this if you already run nginx on the server. Replace step 1 (Install Caddy) onwards with:

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

The `proxy_set_header Upgrade` and `Connection "upgrade"` lines are required; without them the WebSocket upgrade fails silently and agents cannot connect.

### Alternative: Apache httpd + certbot

Use this if you already run Apache on the server. You need three modules enabled: `proxy`, `proxy_http`, and `proxy_wstunnel` (the last one carries WebSocket traffic, which `proxy_http` alone cannot).

```bash
sudo apt install apache2 certbot python3-certbot-apache
sudo a2enmod proxy proxy_http proxy_wstunnel rewrite ssl

sudo tee /etc/apache2/sites-available/tela-hub.conf << 'EOF'
<VirtualHost *:80>
    ServerName myhub.example.com

    ProxyPreserveHost On

    # WebSocket upgrade: forward /ws* and Upgrade-bearing requests to wstunnel.
    RewriteEngine On
    RewriteCond %{HTTP:Upgrade} websocket [NC]
    RewriteCond %{HTTP:Connection} upgrade [NC]
    RewriteRule ^/?(.*) "ws://127.0.0.1:8080/$1" [P,L]

    # Plain HTTP traffic (REST API, console static files).
    ProxyPass        / http://127.0.0.1:8080/
    ProxyPassReverse / http://127.0.0.1:8080/
</VirtualHost>
EOF

sudo a2ensite tela-hub
sudo apache2ctl configtest && sudo systemctl reload apache2

# Obtain TLS certificate (adds HTTPS VirtualHost automatically)
sudo certbot --apache -d myhub.example.com
```

On RHEL / Fedora, replace `a2enmod`/`a2ensite` with editing `/etc/httpd/conf.modules.d/` and `/etc/httpd/conf.d/`, and use `systemctl reload httpd`.

### Alternative: Cloudflare Tunnel (zero inbound ports)

If you do not want to expose any inbound ports, Cloudflare Tunnel makes an outbound connection to Cloudflare's edge, which terminates TLS and proxies traffic back to your hub. With Cloudflare Tunnel `telahubd` can stay on port 80 (the direct-deployment default from step 3 of the install flow), so skip the port-8080 change above.

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

Cloudflare Tunnel is TCP-only, so the UDP relay (port 41820) cannot pass through it and sessions will use WebSocket transport instead.

## Register with a hub directory

Once the hub is reachable, add it to a hub directory (such as Awan Saya) so users and the CLI can find it by short name.

### Option A: CLI (recommended)

From any workstation with the hub's owner token:

```bash
tela admin portals add awansaya \
  -hub wss://your-hub.example.com \
  -token <hub-owner-token> \
  -portal-url https://awansaya.net
```

The hub will register itself with the portal, exchange viewer tokens for status proxying, and store a scoped sync token so future viewer-token updates happen automatically.

### Option B: Portal dashboard

1. Open the portal dashboard and click **Add Hub**.
2. Enter a short name (e.g., `myhub`), the hub's public URL (e.g., `https://your-hub.example.com`), and optionally a **viewer token** (so the portal can proxy hub status server-side).

### After registration

The hub will appear in the portal dashboard and be resolvable by the CLI:

```bash
tela remote add myportal https://your-portal.example
tela machines -hub myhub -token <your-token>
tela connect -hub myhub -machine mybox -token <your-token>
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
| Portal shows "Auth Error" for a hub | Viewer token out of sync or missing | Run `telahubd portal sync` on the hub, or restart the hub service |
| Portal cards stay empty | Portal missing viewer token, or hub unreachable from portal server | Ensure the hub entry in the portal includes a valid viewer token |
| `telad` connects but "auth_required" | Hub has auth enabled, agent has no token | Add a `token:` field to `telad.yaml` or pass `-token` on the command line |
| UDP relay not working | TCP-only tunnel or firewall | Confirm UDP `TELAHUBD_UDP_PORT` is open inbound on the hub and outbound from both sides |
| "Machine not found" | Machine isn't registered | Run `tela machines -hub <hub>` to list available machines; confirm `telad` is running and connected |

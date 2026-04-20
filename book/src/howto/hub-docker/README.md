# Docker Compose templates for telahubd

Copy-paste starting points for running `telahubd` in Docker. Pick the
file that matches your deployment topology, fill in `.env`, run
`docker compose up -d`, and you are done.

## Files in this directory

| File | Topology |
|------|----------|
| `docker-compose.minimal.yml` | telahubd alone on port 80. LAN / dev only. No TLS. |
| `docker-compose.caddy.yml` | telahubd + [Caddy](https://caddyserver.com) reverse proxy with automatic Let's Encrypt. Recommended for production. |
| `docker-compose.nginx.yml` | telahubd + nginx. For operators who already run nginx. Bring your own certs. |
| `Caddyfile` | Companion config for the Caddy template. |
| `nginx.conf` | Companion config for the nginx template. Includes the WebSocket upgrade handling. |
| `.env.example` | Template for `.env`. Copy to `.env` and fill in at least `TELA_OWNER_TOKEN`. |

Every template uses the same image (`ghcr.io/paulmooreparks/telahubd:stable`)
and the same `telahubd-data` named volume for config and tokens, so
switching between topologies is a matter of editing the compose file
you use -- the persistent state carries over unchanged.

## The single most-missed gotcha: UDP

Every one of these compose files publishes UDP port `41820` with the
`/udp` suffix:

```yaml
ports:
  - "41820:41820/udp"
```

Without the suffix, Docker exposes only the TCP side. telahubd does
not listen on TCP 41820, so the mapping silently does nothing, and
every relay session falls back to WebSocket-over-TCP. The hub keeps
working but round-trip latency roughly doubles.

If you adapt one of these templates and sessions feel slow,
`docker port <container-name>` should report `41820/udp`. If it
reports `41820/tcp` instead, you lost the suffix.

## Upgrading

Docker-based upgrades use `docker pull`, not `telahubd update`:

```sh
docker compose pull
docker compose up -d
```

Named volumes survive container recreation, so config and tokens are
preserved. See the top-level book chapter on the release process for
the channel tag model and how to pin to a specific version.

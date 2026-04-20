# Docker-first telahubd distribution: design

This document is the authoritative design for [issue #58](https://github.com/paulmooreparks/tela/issues/58), targeting the `0.13` release.

It covers why telahubd (and only telahubd) gets a first-class Docker image, how the image is structured and published, how an operator gets from a fresh host to a running hub, and what the self-update story looks like inside a container. Narrative how-tos for end operators come later as a book chapter; this document speaks to maintainers and integrators.

---

## 1. Motivation

The current hub install page reads as a matrix of operating system by release channel. That shape is right for endpoint-role binaries (`tela`, `telad`) because they bind local ports on the machine they expose and have to coexist with whatever else the user is running there. It is the wrong first impression for a server-role daemon: most operators want to be told "here is the image, run it" and then get on with configuring the reverse proxy.

`telahubd` is an ideal Docker-first candidate:

- Single static Go binary, no native dependencies beyond a CA certificate bundle.
- Runs as a service on someone else's box, not on the user's laptop.
- Almost always fronted by a reverse proxy (Caddy, nginx, Cloudflare Tunnel).
- Upgrade cadence is operator-driven, not end-user-driven. "docker pull and restart" is a natural fit.

`tela` and `telad` are not candidates for a primary Docker path:

- Endpoint-role: they need to bind local ports on the machine they are exposing.
- `tela` needs DNS setup access for local-name resolution on macOS and Linux.
- `telad` runs as a system service on Windows and as a systemd unit on Linux; the operating system's service manager is the right packaging.
- Native binary distribution (`brew`, `apt`, `.msi`, or a plain binary drop) matches how operators already deploy endpoint software.

TelaVisor is also not a candidate: it is a desktop GUI, and a container is not a meaningful distribution format for a GUI.

This design scopes the Docker work for `telahubd` only. Dockerfiles for `telad` may appear later as stretch goals; they are explicitly out of scope here.

## 2. Image architecture

Multi-stage source build. Final image is `gcr.io/distroless/static-debian12:nonroot` with the `telahubd` binary at `/usr/local/bin/telahubd`.

```dockerfile
FROM golang:1.25-alpine AS build
# go.mod / go.sum cached as a separate layer
# go build -ldflags '-s -w -X main.version=$TELA_VERSION' -trimpath

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/telahubd /usr/local/bin/telahubd
WORKDIR /data
VOLUME ["/data"]
EXPOSE 80
EXPOSE 41820/udp
ENTRYPOINT ["/usr/local/bin/telahubd"]
CMD ["-config", "/data/telahubd.yaml"]
```

Properties:

- `CGO_ENABLED=0`, fully static.
- Runs as the distroless `nonroot` user (uid 65532). No root inside the container.
- No shell, no package manager, no writable `/`. `docker exec sh` does not work; if an operator needs that, they must use a one-off debug image.
- Size around 18 MB. Distroless carries the CA bundle, `/etc/passwd` entries for `nobody` and `nonroot`, and tzdata.

The single binary is the entire payload. Everything telahubd embeds (the hub console, the channel package, the admin API) is already in the compiled binary.

## 3. Tag strategy

Every release from `release.yml` publishes a multi-architecture manifest to `ghcr.io/paulmooreparks/telahubd` with several tags:

| Tag | Points at |
|---|---|
| `:dev` | most recent dev build |
| `:beta` | most recent beta |
| `:stable` | most recent stable |
| `:latest` | alias for `:stable` |
| `:v0.13.0` | specific stable version |
| `:v0.13.0-beta.1` | specific beta |
| `:v0.13.0-dev.3` | specific dev build (see 12.1 on whether we publish these at all) |

The mental model for operators matches what they already know from the native channel manifests:

- "Give me the current stable" -> `:stable` or `:latest`.
- "Pin to a specific version" -> `:v0.13.0`.
- "Follow dev" -> `:dev`.

The channel-manifest tag strategy maps directly to Docker tags. No new concepts for operators who have already read the release-process chapter.

## 4. Self-update model

Native binaries and Docker containers have different upgrade models, and telahubd honors both.

**Native path:** `telahubd update` fetches the channel manifest, verifies the SHA-256 of the new binary, atomically swaps it into place, and restarts the service. This is what the channel-sources rework (#55) is for.

**Docker path:** operators run `docker pull ghcr.io/paulmooreparks/telahubd:stable` followed by `docker compose up -d telahubd` (or `docker restart tela-telahubd` without compose). The container restarts against the same `/data` volume; telahubd reads the persisted config and carries on. Graceful shutdown (#30, shipped in 0.12) drains in-flight requests via `shutdownTimeout` on SIGTERM, so `docker stop` is clean.

The two paths are parallel, not competing. The channel-manifest system drives native self-update; Docker tags drive Docker updates. `telahubd update` run inside a container is a no-op in practice (the binary lives on a read-only layer) and will eventually be made to detect that context and print a "you are inside a container; use docker pull" message. That is a polish item, not blocking 0.13.

Automated Docker updates (Watchtower, Diun, Argo CD) are out of scope for Tela. Docker-native tooling handles that class of problem better than anything we could bake into the image.

## 5. Publishing pipeline

GitHub Container Registry (`ghcr.io`) is the canonical home. Docker Hub mirror can be added later on demand; GHCR alone is sufficient for 0.13.

`release.yml` gains a `docker-publish` job that runs after the native-binary matrix succeeds:

- `docker/setup-buildx-action@v3`
- `docker/login-action@v3` using the job's `GITHUB_TOKEN`
- `docker/build-push-action@v6` with:
  - `context: .`
  - `file: Dockerfile.telahubd`
  - `platforms: linux/amd64,linux/arm64`
  - `build-args: TELA_VERSION=${tag}`
  - `tags: <computed set>`
  - `push: true`
  - `cache-from: type=registry,ref=ghcr.io/paulmooreparks/telahubd:buildcache`
  - `cache-to: type=registry,ref=ghcr.io/paulmooreparks/telahubd:buildcache,mode=max`

The tag set is computed in a preceding shell step:

- Always: `:${VERSION}`.
- If channel is dev: also `:dev` (subject to 12.1).
- If channel is beta: also `:beta`.
- If channel is stable: also `:stable` and `:latest`.

Image signing via `cosign` is worth doing later but is out of scope for 0.13. It opens questions about key management, rotation, and downstream verification that deserve their own design.

## 6. First-run bootstrap

The container needs a path from "fresh image pull" to "running hub with an owner token" without manual `docker exec` gymnastics. Three options, in decreasing order of preference:

**Option 1 (recommended): env-var owner token on first start.** Pass `TELA_OWNER_TOKEN=<hex>` to `docker run` or set it in `.env` for `docker compose`. The hub's existing `bootstrapFromEnv` path seeds the owner identity on a blank config and persists it to `/data/telahubd.yaml`. Subsequent restarts honor the persisted token; the env var becomes redundant but harmless.

Preferred because it is one step for the operator, does not require log scraping or a container shell, and fits infrastructure-as-code workflows where the operator generates the token up front and stores it in a secrets manager.

**Option 2: explicit bootstrap via a one-off run.** `docker run --rm -v telahubd-data:/data telahubd:stable user bootstrap` writes the token to the volume and prints it once to stdout. The operator captures the token from the printed line, then starts the server with the same volume. Two-step but explicit and suitable for operators who do not want to generate tokens themselves.

Works because the image's `ENTRYPOINT` is the binary itself, so any subcommand composes through `docker run --rm <image> <subcommand>`.

**Option 3 (fallback): auto-bootstrap on first start.** If no config file exists and no `TELA_OWNER_TOKEN` env var is set, telahubd's `autoBootstrapAuth` generates an owner token, writes it to `/data/telahubd.yaml`, and logs a line identifying the token. The operator reads it from `docker logs` before the container is recycled.

Workable but awkward. If the first start happened while the operator was configuring something else and the log line scrolled past, recovery is `docker exec <container> telahubd user show-owner`, which requires a shell the distroless image does not provide. Operators in this situation must use a debug image variant or recreate the container with the env-var path.

All three paths stay functional. The book and the compose templates default to option 1. Option 3 exists as a safety net; it is not the recommended path.

This design does require one small fix to the foreground-mode Main() path: `-config <file>` where `<file>` does not exist must be treated as a first-start signal (carry the path forward so bootstrap writes there) rather than a fatal error. Pre-#58, running the CMD with an empty volume hit `log.Fatalf` during `loadHubConfig`. Fixed as part of Phase 1.

## 7. State layout

`/data` is the persistent state directory. Contents once the hub is running:

- `telahubd.yaml` -- the config file: auth tokens, portal registrations, channels config, bridges, shutdown timeout.
- Implementation detail: the history ring buffer is currently in-memory only and does not land on disk. A future release may spill it to `/data/history/`; the volume accommodates that already.

Mount options:

- **Named volume (recommended):** `docker volume create telahubd-data` and then `-v telahubd-data:/data`. Docker's volume init copies the image's directory ownership (uid 65532) onto the volume, so writes from the nonroot user work out of the box.
- **Bind mount** (for operators who want to edit `telahubd.yaml` on the host): `-v /srv/telahubd:/data`. The host directory must be readable and writable by uid 65532. The book documents `sudo install -d -o 65532 -g 65532 /srv/telahubd` as the one-time setup step.

What survives container recreation: everything in `/data` if the volume is reused. In-memory state (session list, live agent WebSockets, the history ring buffer) does not survive, which is by design. telahubd is restart-safe; agents reconnect within seconds and clients retry.

## 8. Ports and the UDP gotcha

Two ports, both expose with care:

- **80 (HTTP + WebSocket):** fronted by a reverse proxy in production. The compose templates show Caddy and nginx configurations. For a local LAN test, `-p 8080:80` with direct connection works fine.
- **41820/udp (UDP relay):** must carry the `/udp` suffix on `docker run -p`. Without it, Docker exposes only the TCP side, which telahubd does not listen on, and the entire UDP relay tier silently fails to work. All relay traffic falls back to WebSocket-over-TCP. The hub still functions, but latency roughly doubles and throughput is cut in half on sessions that would otherwise hole-punch to UDP.

This is the single most likely misconfiguration. Every Docker example in the book, every compose template, and every `docker run` line includes the `/udp` suffix. The troubleshooting section gains an entry titled "My agents all fall back to WebSocket relay" that says to check this first.

A future enhancement can have telahubd log a warning when it detects UDP is unreachable (e.g. port is bound but the listening socket never sees packets during the first five minutes of uptime). Out of scope for 0.13; filed as a follow-up if the workaround proves insufficient.

TLS termination is the reverse proxy's job. `telahubd` does not terminate TLS itself. Every production compose template puts a proxy in front. A "no proxy, unencrypted, direct-to-hub" template is not provided; it would only get copy-pasted into production.

## 9. Compose topology

Three templates under `deploy/docker/`. Placed outside `book/` because these are runtime deployment artifacts (YAML files an operator downloads and edits), not book chapters; the `book/` tree is reserved for documentation. The book chapter cross-links to the files from the repo root via `raw.githubusercontent.com` URLs (for `curl`) and to the tree for browsing.

**`docker-compose.minimal.yml`** -- telahubd alone on port 80. For LAN-only dev or test deployments. Demonstrates env-var bootstrap, a named volume, and the UDP port mapping. No proxy, no TLS.

**`docker-compose.caddy.yml`** -- telahubd plus Caddy. Caddy's config reverse-proxies `https://hub.example.com` to `telahubd:80` with automatic Let's Encrypt. Recommended for production deployments where the operator owns the domain and can point DNS at the Docker host. UDP port is published directly from telahubd to the host; Caddy only handles TCP.

**`docker-compose.nginx.yml`** -- telahubd plus nginx. For operators who already run nginx. Provides a ready-to-paste config with the WebSocket upgrade-header map and the idle-timeout bump that long-lived agent connections need. Certificates are brought by the operator (host-level certbot, cert-manager, etc.); nginx does not issue its own.

Each template is a working file: `docker compose up -d` against it, with a populated `.env`, produces a running hub.

A fourth template, `docker-compose.full.yml` -- telahubd plus Caddy plus Awan Saya portal as one-box deployment -- was originally planned and is filed as a follow-up. It waits on Awan Saya publishing an image to a container registry; today AS deploys via its own `docker-compose.yml` from the `awansaya` repo. Once an AS image lands on ghcr.io the fourth template becomes a 20-line addition to this directory.

## 10. Install-page rewrite

`book/src/howto/hub.md` is restructured around the deployment model rather than the binary distribution format:

1. "Docker" is the primary path. The first thing a hub operator sees.
2. Native installation becomes an "Alternative: native binary" section further down, aimed at operators who cannot or do not want to run Docker.
3. The existing OS-by-channel matrix is preserved in the native section; no content is deleted, only reordered and re-weighted.

The book's landing page and the repo README get a matching one-line adjustment pointing Docker operators at the new chapter.

## 11. Non-goals

- **Dockerizing `tela` or `telad` as a primary distribution path.** Both remain native. A community-contributed Dockerfile.telad for operators running agents alongside containerized services is a reasonable stretch goal for a later release.
- **Dockerizing TelaVisor.** Desktop GUI. Package-manager distribution (DMG, MSI, AppImage) is the right answer.
- **Image signing via cosign.** Separate design; key management, rotation, and downstream verification all deserve explicit choices. Worth doing, not in 0.13.
- **Docker Hub mirror.** GHCR is sufficient. Can be added later without any protocol change if demand materializes.
- **Bundling the channel-manifest self-update path into the Docker flow.** The two distribution channels remain parallel. Operators who deploy via Docker use `docker pull`; operators who deploy native binaries use `telahubd update`.

## 12. Open questions

### 12.1 Do dev-channel builds publish a Docker image at all?

Every commit to `main` cuts a `v0.13.0-dev.N` tag via `release.yml`. Publishing a Docker image for every dev build means `ghcr.io` accumulates short-lived tags forever. Three approaches:

- **Publish only for beta and stable.** `:dev` floats to a periodic snapshot (daily, or on a manual trigger). Simplest.
- **Publish for every dev build but auto-prune old dev tags.** Keep the last 10 dev tags; drop older ones via a GHCR cleanup action. Preserves the ability to pin to a specific dev build but keeps the registry tidy.
- **Publish only when the release workflow's tag push matches `-dev.*` and the commit message carries a `docker` label.** Opt-in per-commit. More control, more operator friction.

Recommend the first approach unless dogfooding against `dev` via Docker becomes a real use case. For dev dogfooding on native hosts, the channel-manifest flow already works; Docker does not add much.

### 12.2 Healthcheck shape

Dockerfile can `HEALTHCHECK`. Simplest would be `curl -fsSL http://localhost/.well-known/tela`, but distroless has no `curl`. Three options:

- Add a `telahubd health` subcommand that returns 0 if the local hub responds to `/.well-known/tela`, 1 otherwise. About fifteen lines of Go, keeps the image self-contained.
- No healthcheck in the image; operators add one at compose level with a sidecar.
- Bake a tiny `wget` static binary into a runtime layer. Bloats the image.

Recommend the first approach. Cleanest, no external dependencies, works under any orchestrator that honors `HEALTHCHECK`.

### 12.3 Signal handling under distroless

distroless has no init system. `telahubd` runs as PID 1. Go's runtime forwards signals via `signal.Notify`, so `docker stop` delivers SIGTERM correctly. Zombie reaping is not an issue because telahubd does not fork. No `tini` or similar required.

Verified on the Phase 1 smoke test; called out here so no one gets clever about adding init wrappers later.

### 12.4 TelaVisor connecting to a Dockerized hub

Expected to work unchanged. TV talks HTTPS through whatever reverse proxy sits in front of the hub; the hub's backing binary being a container instead of a native process is invisible. Confirm during Phase 4 testing that the full operator walkthrough (TV -> Caddy -> telahubd -> agent) works end to end with no TV-side code changes.

### 12.5 Logging

telahubd logs to stderr. Docker captures both stdout and stderr into the container log stream. No changes required. Noted here so no one files a bug that says "logs should go to stdout" and gets it merged without this context.

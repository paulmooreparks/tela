# Tela Hub — POC Docker Image (multi-stage)
#
# Stage 1: Compile the telad (daemon) binary for Linux.
# Stage 2: Node.js hub serving HTTP + WebSocket.
#
# Client and agent downloads are served from GitHub Releases, not this image.
#
# Build context must be the repo root:
#   docker build -f docker/hub/Dockerfile -t tela-hub-poc .

# ── Stage 1: Build telad (Linux) ───────────────────────────────────

FROM golang:1.24-alpine AS go-builder

ARG VERSION=dev

WORKDIR /src

# Copy root Go module (cmd/ + internal/ are under this module)
COPY go.mod go.sum ./
RUN go mod download

# Copy all Go source
COPY internal/ ./internal/
COPY cmd/ ./cmd/

# Build only the telad binary needed inside this container
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/telad ./cmd/telad

# ── Stage 2: Node.js hub ───────────────────────────────────────────

FROM node:20-alpine

LABEL org.opencontainers.image.title="tela-hub-poc" \
      org.opencontainers.image.description="Tela POC Hub — HTTP + WebSocket + client downloads" \
      org.opencontainers.image.source="https://github.com/paulmooreparks/tela"

RUN apk add --no-cache tini curl

WORKDIR /app

# Copy package manifest first for better layer caching
COPY poc/package.json poc/package-lock.json ./
RUN npm ci --omit=dev

# Copy application code
COPY poc/hub.js ./
COPY poc/www/ ./www/

# Copy telad daemon binary (used by the telad service in this container)
COPY --from=go-builder /out/telad /usr/local/bin/telad

# Environment defaults
ENV HUB_PORT=8080

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD curl -f http://localhost:8080/ || exit 1

# Use tini as init so signals propagate correctly
ENTRYPOINT ["/sbin/tini", "--"]

CMD ["node", "hub.js"]

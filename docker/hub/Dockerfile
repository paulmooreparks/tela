# Tela Hub — POC Docker Image (multi-stage)
#
# Stage 1: Compile telad (daemon) and tela (client) Go binaries.
# Stage 2: Node.js hub serving HTTP + WebSocket + tela client downloads.
#
# The hub serves client binaries at:
#   /tela/windows/tela.exe
#   /tela/linux/tela
#   /tela/macos/tela          (amd64)
#   /tela/macos-arm64/tela    (Apple Silicon)
#
# Build context must be the repo root:
#   docker build -f docker/hub/Dockerfile -t tela-hub-poc .

# ── Stage 1: Build telad + tela Go binaries ────────────────────────

FROM golang:1.24-alpine AS go-builder

ARG VERSION=dev

WORKDIR /src

# Copy root Go module (cmd/ + internal/ are under this module)
COPY go.mod go.sum ./
RUN go mod download

# Copy all Go source
COPY internal/ ./internal/
COPY cmd/ ./cmd/

# Build telad for each target platform (agent daemon)
RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/telad/windows/telad.exe ./cmd/telad \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/telad/linux/telad ./cmd/telad \
 && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/telad/macos/telad ./cmd/telad \
 && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/telad/macos-arm64/telad ./cmd/telad

# Build tela client for each target platform (downloaded by end users)
RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/tela/windows/tela.exe ./cmd/tela \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/tela/linux/tela ./cmd/tela \
 && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/tela/macos/tela ./cmd/tela \
 && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/tela/macos-arm64/tela ./cmd/tela

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

# Copy tela client binaries for download
COPY --from=go-builder /out/tela/windows/    ./www/tela/windows/
COPY --from=go-builder /out/tela/linux/      ./www/tela/linux/
COPY --from=go-builder /out/tela/macos/      ./www/tela/macos/
COPY --from=go-builder /out/tela/macos-arm64/ ./www/tela/macos-arm64/

# Copy telad agent binaries for download
COPY --from=go-builder /out/telad/windows/    ./www/telad/windows/
COPY --from=go-builder /out/telad/linux/      ./www/telad/linux/
COPY --from=go-builder /out/telad/macos/      ./www/telad/macos/
COPY --from=go-builder /out/telad/macos-arm64/ ./www/telad/macos-arm64/

# Copy telad daemon binary (used by the telad service in this container)
COPY --from=go-builder /out/telad/linux/telad /usr/local/bin/telad

# Environment defaults
ENV HUB_PORT=8080

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD curl -f http://localhost:8080/ || exit 1

# Use tini as init so signals propagate correctly
ENTRYPOINT ["/sbin/tini", "--"]

CMD ["node", "hub.js"]

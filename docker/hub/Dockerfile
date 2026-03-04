# Tela Hub — POC Docker Image (multi-stage)
#
# Stage 1: Compile Go helper binaries for Windows, Linux, macOS.
# Stage 2: Node.js hub serving HTTP + WebSocket + helper downloads.
#
# The hub serves helper binaries at:
#   /helper/windows/tela-helper.exe
#   /helper/linux/tela-helper
#   /helper/macos/tela-helper          (amd64)
#   /helper/macos-arm64/tela-helper    (Apple Silicon)
#
# Build context must be the repo root:
#   docker build -f docker/hub/Dockerfile -t tela-hub-poc .

# ── Stage 1a: Build Go helper binaries (plain TCP tunnel) ──────────

FROM golang:1.24-alpine AS helper-builder

WORKDIR /src

# Copy Go module files first for layer caching
COPY helper/go.mod helper/go.sum ./
RUN go mod download

# Copy source
COPY helper/*.go ./

# Build for each target platform (static, stripped)
RUN GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o /out/windows/tela-helper.exe . \
 && GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o /out/linux/tela-helper       . \
 && GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o /out/macos/tela-helper        . \
 && GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o /out/macos-arm64/tela-helper  .

# ── Stage 1b: Build WireGuard agent + helper (L3 tunnel) ───────────

FROM golang:1.24-alpine AS wg-builder

WORKDIR /src

# Copy root Go module (cmd/ + internal/ are under this module)
COPY go.mod go.sum ./
RUN go mod download

# Copy all Go source
COPY internal/ ./internal/
COPY cmd/ ./cmd/

# Build WG agent (Linux only — runs inside Docker)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
    -o /out/tela-agent ./cmd/tela-agent

# Build WG helper for each target platform
RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" \
    -o /out/helper-wg/windows/tela-helper-wg.exe ./cmd/tela-helper \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
    -o /out/helper-wg/linux/tela-helper-wg ./cmd/tela-helper \
 && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" \
    -o /out/helper-wg/macos/tela-helper-wg ./cmd/tela-helper \
 && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" \
    -o /out/helper-wg/macos-arm64/tela-helper-wg ./cmd/tela-helper

# ── Stage 2: Node.js hub ───────────────────────────────────────────

FROM node:20-alpine

LABEL org.opencontainers.image.title="tela-hub-poc" \
      org.opencontainers.image.description="Tela POC Hub — HTTP + WebSocket + helper downloads" \
      org.opencontainers.image.source="https://github.com/paulmooreparks/tela"

RUN apk add --no-cache tini curl

WORKDIR /app

# Copy package manifest first for better layer caching
COPY poc/package.json poc/package-lock.json ./
RUN npm ci --omit=dev

# Copy application code
COPY poc/hub.js ./
COPY poc/www/ ./www/

# Copy helper binaries from build stage into www/ so the static
# server exposes them at /helper/<platform>/tela-helper[.exe]
COPY --from=helper-builder /out/windows/    ./www/helper/windows/
COPY --from=helper-builder /out/linux/      ./www/helper/linux/
COPY --from=helper-builder /out/macos/      ./www/helper/macos/
COPY --from=helper-builder /out/macos-arm64/ ./www/helper/macos-arm64/

# Copy WG helper binaries for download
COPY --from=wg-builder /out/helper-wg/windows/    ./www/helper-wg/windows/
COPY --from=wg-builder /out/helper-wg/linux/      ./www/helper-wg/linux/
COPY --from=wg-builder /out/helper-wg/macos/      ./www/helper-wg/macos/
COPY --from=wg-builder /out/helper-wg/macos-arm64/ ./www/helper-wg/macos-arm64/

# Copy WG agent binary (used by agent-rdp-wg service)
COPY --from=wg-builder /out/tela-agent /usr/local/bin/tela-agent

# Environment defaults
ENV HUB_PORT=8080

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD curl -f http://localhost:8080/ || exit 1

# Use tini as init so signals propagate correctly
ENTRYPOINT ["/sbin/tini", "--"]

CMD ["node", "hub.js"]

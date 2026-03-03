# Tela Hub — POC Docker Image
#
# Runs the combined HTTP + WebSocket hub on HUB_PORT (default 8080).
# HTTP serves the static site from www/.
# WebSocket handles agent/helper relay on the same port.
#
# Only one port is exposed; Cloudflare Tunnel (or a reverse proxy)
# provides the public TLS endpoint.
#
# Build context must be the repo root so that poc/ is available:
#   docker build -f docker/hub/Dockerfile -t tela-hub-poc .

FROM node:20-alpine

LABEL org.opencontainers.image.title="tela-hub-poc" \
      org.opencontainers.image.description="Tela POC Hub — HTTP + WebSocket on one port" \
      org.opencontainers.image.source="https://github.com/paulmooreparks/tela"

RUN apk add --no-cache tini curl

WORKDIR /app

# Copy package manifest first for better layer caching
COPY poc/package.json poc/package-lock.json ./
RUN npm ci --omit=dev

# Copy application code
COPY poc/hub.js ./
COPY poc/www/ ./www/

# Environment defaults
ENV HUB_PORT=8080

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD curl -f http://localhost:8080/ || exit 1

# Use tini as init so signals propagate correctly
ENTRYPOINT ["/sbin/tini", "--"]

CMD ["node", "hub.js"]

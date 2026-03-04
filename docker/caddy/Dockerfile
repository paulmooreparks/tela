# Custom Caddy build with Cloudflare DNS module
# Required for DNS-01 ACME challenge (no port 80 needed)
FROM caddy:2-builder AS builder

RUN xcaddy build \
    --with github.com/caddy-dns/cloudflare

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy

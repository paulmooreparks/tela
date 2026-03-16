# Tela Go Hub (telahubd) -- multi-stage Docker image
#
# Stage 1: Compile telahubd for Linux amd64.
# Stage 2: Minimal Alpine image with the binary (console files are embedded).
#
# Build context must be the repo root:
#   docker build -f docker/gohub/Dockerfile -t tela-gohub .

# -- Stage 1: Build telahubd --

FROM golang:1.24-alpine AS builder

ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY internal/ ./internal/
COPY cmd/ ./cmd/
COPY console/ ./console/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/telahubd ./cmd/telahubd

# -- Stage 2: Runtime --

FROM alpine:3.20

LABEL org.opencontainers.image.title="tela-gohub" \
      org.opencontainers.image.description="Tela Go Hub -- HTTP + WebSocket + UDP relay" \
      org.opencontainers.image.source="https://github.com/paulmooreparks/tela"

RUN apk add --no-cache tini curl

WORKDIR /app

COPY --from=builder /out/telahubd /usr/local/bin/telahubd

ENV TELAHUBD_PORT=8080
ENV TELAHUBD_UDP_PORT=41820

EXPOSE 8080
EXPOSE 41820/udp

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD curl -f http://localhost:8080/api/status || exit 1

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["telahubd"]

#!/bin/sh
set -e

# ── SSH setup ────────────────────────────────────────────────────────
SSH_KEY_DIR=/app/data/ssh
mkdir -p "$SSH_KEY_DIR"

# Generate host keys once, persist in the data volume
if [ ! -f "$SSH_KEY_DIR/ssh_host_ed25519_key" ]; then
  ssh-keygen -A
  cp /etc/ssh/ssh_host_* "$SSH_KEY_DIR/"
  echo "[entrypoint] SSH host keys generated"
fi
cp "$SSH_KEY_DIR"/ssh_host_* /etc/ssh/

# Allow root login with password
sed -i 's/#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config

# Set root password from env or generate a random one
if [ -n "$SSH_PASSWORD" ]; then
  echo "root:$SSH_PASSWORD" | chpasswd
else
  RANDOM_PASS=$(head -c 16 /dev/urandom | base64 | tr -d '=/+' | head -c 12)
  echo "root:$RANDOM_PASS" | chpasswd
  echo "[entrypoint] SSH root password: $RANDOM_PASS"
fi

/usr/sbin/sshd
echo "[entrypoint] sshd started"

# ── Tela remote config (pre-seed for awansaya.net) ───────────────────
TELA_CONFIG_DIR=/root/.tela
mkdir -p "$TELA_CONFIG_DIR"

if [ ! -f "$TELA_CONFIG_DIR/config.yaml" ]; then
  cat > "$TELA_CONFIG_DIR/config.yaml" <<EOF
remotes:
  awansaya:
    url: https://awansaya.net
    token: "${AWANSAYA_TOKEN:-}"
    hub_directory: /api/hubs
EOF
  echo "[entrypoint] tela remote 'awansaya' pre-configured (https://awansaya.net)"
fi

# ── telad (agent) ────────────────────────────────────────────────────
# Generate telad.yaml at startup so the hub token is injected from env.
cat > /app/telad.yaml <<EOF
hub: ws://localhost:${HUB_PORT:-8080}
token: "${TELA_OWNER_TOKEN:-}"

machines:
  - name: ${HUB_NAME:-gohub}
    hostname: ${HUB_NAME:-gohub}
    os: linux
    services:
      - port: 22
        proto: tcp
        name: SSH
        description: Hub shell access
    target: 127.0.0.1
EOF

# Start telad with auto-restart (wait for telahubd to be listening)
(
  sleep 3
  while true; do
    echo "[entrypoint] starting telad"
    telad -config /app/telad.yaml || true
    echo "[entrypoint] telad exited, restarting in 5s..."
    sleep 5
  done
) &

# ── telahubd (main process) ─────────────────────────────────────────
exec telahubd

#!/usr/bin/env bash
# publish-channel.sh
#
# Reference implementation for publishing Tela binaries to a self-hosted
# channel server from Linux or macOS. Mirrors publish-channel.ps1
# byte-for-byte in behaviour: cross-compiles tela/telad/telahubd for
# Linux amd64 and Windows amd64, builds TelaVisor via wails for the
# host platform, uploads everything to the hub over HTTPS via the
# channels admin API, and triggers the hub to write the manifest.
#
# Prerequisites:
#   - Go 1.24+ and the tela source tree
#   - wails CLI in PATH if you want to publish TelaVisor (optional)
#   - bash 4+, curl, and awk (all stock on any modern Linux or macOS)
#   - A running hub with channels.enabled=true in telahubd.yaml
#   - Owner or admin token for that hub
#
# Configuration
# -------------
# Reads scripts/publish.env (KEY=VALUE, # for comments, one entry per
# line). Process environment variables of the same name win over the
# file. Required keys:
#
#   TELA_PUBLISH_HUB_URL   Base URL of the target hub, no trailing slash
#   TELA_PUBLISH_TOKEN     Owner or admin token for the hub
#
# Optional:
#   TELA_PUBLISH_CHANNEL   Default: reads scripts/channel.txt
#   TELA_PUBLISH_REPO_ROOT Default: this script's parent directory
#   TELA_PUBLISH_DIST_DIR  Default: <repo>/dist
#
# TelaVisor naming
# ----------------
# TelaVisor is built for the host platform (macOS / Linux). The uploaded
# filename is telavisor-{os}-{arch} to match how other Tela binaries are
# named. If wails is not in PATH the TelaVisor step is skipped silently;
# CLI binaries and the manifest publish still succeed.
#
# See book/src/ops/release-process.md for the full runbook, including
# the first-time bootstrap procedure for a brand new hub.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Configuration loader ────────────────────────────────────────────
# Simple line-based parser: tolerant of comments, blank lines, and a
# single layer of surrounding single or double quotes around values.
# Does NOT do shell expansion, so $VAR references in values stay literal.
load_publish_env() {
    local env_file="$SCRIPT_DIR/publish.env"
    [[ -f "$env_file" ]] || return 0
    while IFS= read -r line || [[ -n "$line" ]]; do
        line="${line#"${line%%[![:space:]]*}"}"   # ltrim
        line="${line%"${line##*[![:space:]]}"}"   # rtrim
        [[ -z "$line" || "${line:0:1}" == "#" ]] && continue
        local key="${line%%=*}"
        local val="${line#*=}"
        key="${key%"${key##*[![:space:]]}"}"      # rtrim key
        val="${val#"${val%%[![:space:]]*}"}"      # ltrim val
        # Strip one layer of quoting.
        if [[ "${val:0:1}" == '"' && "${val: -1}" == '"' ]] ||
           [[ "${val:0:1}" == "'" && "${val: -1}" == "'" ]]; then
            val="${val:1:-1}"
        fi
        # Only set if not already in the environment (process env wins).
        if [[ -z "${!key:-}" ]]; then
            export "$key=$val"
        fi
    done <"$env_file"
}

load_publish_env

HUB_URL="${TELA_PUBLISH_HUB_URL:-}"
TOKEN="${TELA_PUBLISH_TOKEN:-}"
REPO_ROOT="${TELA_PUBLISH_REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
DIST="${TELA_PUBLISH_DIST_DIR:-$REPO_ROOT/dist}"

if [[ -z "$HUB_URL" ]]; then
    echo "error: TELA_PUBLISH_HUB_URL is required (set it in $SCRIPT_DIR/publish.env or the environment)" >&2
    exit 1
fi
HUB_URL="${HUB_URL%/}"

# ── Channel selection ──────────────────────────────────────────────
CHANNEL="${TELA_PUBLISH_CHANNEL:-}"
if [[ -z "$CHANNEL" ]]; then
    channel_file="$SCRIPT_DIR/channel.txt"
    [[ -f "$channel_file" ]] || echo "local" >"$channel_file"
    CHANNEL="$(tr -d '[:space:]' <"$channel_file")"
fi
if ! [[ "$CHANNEL" =~ ^[A-Za-z0-9-]+$ ]]; then
    echo "error: invalid channel name '$CHANNEL' (use [A-Za-z0-9-] only)" >&2
    exit 1
fi

# ── Per-channel build counter and version tag ──────────────────────
BASE="$(tr -d '[:space:]' <"$REPO_ROOT/VERSION")"
COUNTER_FILE="$SCRIPT_DIR/$CHANNEL-build-counter"
N=1
if [[ -f "$COUNTER_FILE" ]]; then
    N=$(( $(<"$COUNTER_FILE") + 1 ))
fi
echo "$N" >"$COUNTER_FILE"
TAG="v${BASE}.0-${CHANNEL}.${N}"

mkdir -p "$DIST"

# ── Detect host platform (for TelaVisor naming) ────────────────────
host_os="$(uname -s)"
host_arch="$(uname -m)"
case "$host_os" in
    Linux)  host_goos="linux"  ;;
    Darwin) host_goos="darwin" ;;
    *)      host_goos="" ;;
esac
case "$host_arch" in
    x86_64|amd64) host_goarch="amd64" ;;
    arm64|aarch64) host_goarch="arm64" ;;
    *) host_goarch="" ;;
esac

# ── Build: CLI binaries (linux/amd64 + windows/amd64) ──────────────
export CGO_ENABLED=0
cli_binaries=(tela telad telahubd)

echo
echo "==> Building $TAG CLI for linux/amd64"
(
    cd "$REPO_ROOT"
    export GOOS=linux GOARCH=amd64
    for bin in "${cli_binaries[@]}"; do
        echo "    $bin-linux-amd64"
        go build -trimpath -ldflags="-s -w -X main.version=$TAG" \
            -o "$DIST/$bin-linux-amd64" "./cmd/$bin"
    done
)

echo
echo "==> Building $TAG CLI for windows/amd64"
(
    cd "$REPO_ROOT"
    export GOOS=windows GOARCH=amd64
    for bin in "${cli_binaries[@]}"; do
        echo "    $bin-windows-amd64.exe"
        go build -trimpath -ldflags="-s -w -X main.version=$TAG" \
            -o "$DIST/$bin-windows-amd64.exe" "./cmd/$bin"
    done
)

# ── Build: TelaVisor (host platform via wails) ─────────────────────
tv_file=""
if command -v wails >/dev/null 2>&1 && [[ -n "$host_goos" && -n "$host_goarch" ]]; then
    echo
    echo "==> Building $TAG TelaVisor for $host_goos/$host_goarch"
    (
        cd "$REPO_ROOT/cmd/telagui"
        wails build -ldflags "-X main.version=$TAG"
    )
    # wails output path varies by platform. Most common:
    #   linux:  build/bin/telavisor
    #   darwin: build/bin/TelaVisor.app/Contents/MacOS/telavisor
    # For our upload we always want a single file named with the
    # standard platform suffix. Pick whichever exists.
    tv_file="telavisor-$host_goos-$host_goarch"
    local_candidates=(
        "$REPO_ROOT/cmd/telagui/build/bin/telavisor"
        "$REPO_ROOT/cmd/telagui/build/bin/TelaVisor.app/Contents/MacOS/telavisor"
    )
    for candidate in "${local_candidates[@]}"; do
        if [[ -f "$candidate" ]]; then
            cp "$candidate" "$DIST/$tv_file"
            break
        fi
    done
    if [[ ! -f "$DIST/$tv_file" ]]; then
        echo "warning: wails build succeeded but expected output not found; skipping TelaVisor upload" >&2
        tv_file=""
    fi
else
    echo "note: wails not in PATH or host platform not recognised; skipping TelaVisor build" >&2
fi

# ── Verify token before uploading ──────────────────────────────────
if [[ -z "$TOKEN" ]]; then
    echo "error: build succeeded but no TELA_PUBLISH_TOKEN configured (add it to $SCRIPT_DIR/publish.env or the environment)" >&2
    exit 1
fi

# ── Upload to the hub ──────────────────────────────────────────────
echo
echo "==> Uploading binaries to $HUB_URL/api/admin/channels/files/"

all_files=(
    tela-linux-amd64
    telad-linux-amd64
    telahubd-linux-amd64
    tela-windows-amd64.exe
    telad-windows-amd64.exe
    telahubd-windows-amd64.exe
)
[[ -n "$tv_file" ]] && all_files+=("$tv_file")

for f in "${all_files[@]}"; do
    src="$DIST/$f"
    if [[ ! -f "$src" ]]; then
        echo "error: missing build output: $src" >&2
        exit 1
    fi
    size=$(wc -c <"$src" | tr -d ' ')
    printf "    %-44s  %s bytes\n" "$f" "$size"
    curl -sSfL --fail-with-body \
        -X PUT \
        -H "Authorization: Bearer $TOKEN" \
        -H "Content-Type: application/octet-stream" \
        --data-binary "@$src" \
        "$HUB_URL/api/admin/channels/files/$f" >/dev/null
done

# ── Trigger manifest publish ───────────────────────────────────────
echo
echo "==> Publishing $CHANNEL channel manifest (tag: $TAG)"

publish_body=$(printf '{"channel":"%s","tag":"%s"}' "$CHANNEL" "$TAG")
resp=$(curl -sSfL --fail-with-body \
    -X POST \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    --data "$publish_body" \
    "$HUB_URL/api/admin/channels/publish")

echo "$resp"
echo
echo "==> Done. $CHANNEL channel updated to $TAG"
echo "    public: $HUB_URL/channels/$CHANNEL.json"

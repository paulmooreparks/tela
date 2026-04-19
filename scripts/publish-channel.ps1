# publish-channel.ps1
#
# Reference implementation for publishing Tela binaries to a self-hosted
# channel server. Builds tela/telad/telahubd for Linux amd64 and
# Windows amd64, builds TelaVisor for Windows amd64, uploads everything
# to the hub over HTTPS via the channels admin API, and triggers the
# hub to write the manifest.
#
# Maintainers running their own hub can copy this script, set a few
# environment variables, and run it from anywhere. No SSH, file share,
# or OneDrive sync required.
#
# Prerequisites:
#   - Go 1.24+ and the tela source tree
#   - wails CLI in PATH (TelaVisor frontend bundler)
#   - PowerShell 5.1+ (ships with Windows) or PowerShell 7
#   - A running hub with channels.enabled=true in telahubd.yaml
#   - Owner or admin token for that hub
#
# Configuration
# -------------
# The script reads a .env-style file next to itself (publish.env) or,
# equivalently, process environment variables of the same name. Env
# vars win over the file so CI or ad-hoc overrides work.
#
# Required keys:
#   TELA_PUBLISH_HUB_URL   Base URL of the target hub. No trailing slash.
#                          Example: https://hub.example.net
#   TELA_PUBLISH_TOKEN     Owner/admin token. Get it from the hub with:
#                            telahubd user show-owner
#                          or, on a Dockerised hub:
#                            docker exec <container> telahubd user show-owner \
#                              -config /app/data/telahubd.yaml
#
# Optional keys:
#   TELA_PUBLISH_CHANNEL   Target channel name. Default: reads
#                          .<script-dir>/channel.txt (created with "local"
#                          if absent). Must match [A-Za-z0-9-].
#   TELA_PUBLISH_REPO_ROOT Path to the tela repo. Default: script's
#                          parent directory (so scripts/ sits at repo root).
#   TELA_PUBLISH_DIST_DIR  Where go and wails write build output.
#                          Default: <repo>/dist
#
# The build tag is vX.Y.0-{channel}.N where X.Y comes from the repo's
# VERSION file and N is a per-channel build counter stored in
# <script-dir>/{channel}-build-counter.
#
# Usage
# -----
#   1. Copy publish.env.example next to this script as publish.env and
#      fill in the hub URL and token.
#   2. Run:  pwsh ./scripts/publish-channel.ps1
#      (or the older Windows PowerShell 5.1: powershell .\scripts\publish-channel.ps1)
#
# Bootstrap note
# --------------
# For the very first publish against a brand new hub, the hub's
# telahubd binary must already support /api/admin/channels/* endpoints
# (Tela 0.12 or later). If your hub is running an older telahubd, you
# cannot use this script against it until the hub is upgraded. See the
# "Bootstrapping a self-hosted channel pipeline" section of the book's
# release-process chapter for the chicken-and-egg workaround.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# ── Configuration loader ────────────────────────────────────────────
function Load-PublishEnv {
    $envPath = Join-Path $PSScriptRoot 'publish.env'
    $config = @{}
    if (-not (Test-Path $envPath)) { return $config }
    foreach ($line in (Get-Content $envPath)) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith('#')) { continue }
        $eq = $trimmed.IndexOf('=')
        if ($eq -lt 1) { continue }
        $key = $trimmed.Substring(0, $eq).Trim()
        $val = $trimmed.Substring($eq + 1).Trim()
        if (($val.StartsWith('"') -and $val.EndsWith('"')) -or
            ($val.StartsWith("'") -and $val.EndsWith("'"))) {
            $val = $val.Substring(1, $val.Length - 2)
        }
        $config[$key] = $val
    }
    return $config
}

function Get-PublishConfig($cfg, $key, $default) {
    $fromEnv = [System.Environment]::GetEnvironmentVariable($key, 'Process')
    if ($fromEnv) { return $fromEnv }
    if ($cfg.ContainsKey($key) -and $cfg[$key]) { return $cfg[$key] }
    return $default
}

$PUBLISH_ENV = Load-PublishEnv

$HUB_URL  = Get-PublishConfig $PUBLISH_ENV 'TELA_PUBLISH_HUB_URL' $null
$TOKEN    = Get-PublishConfig $PUBLISH_ENV 'TELA_PUBLISH_TOKEN'   $null
$ROOT     = Get-PublishConfig $PUBLISH_ENV 'TELA_PUBLISH_REPO_ROOT' (Resolve-Path "$PSScriptRoot\..").Path
$DIST     = Get-PublishConfig $PUBLISH_ENV 'TELA_PUBLISH_DIST_DIR'  "$ROOT\dist"

if (-not $HUB_URL) {
    throw "TELA_PUBLISH_HUB_URL is required. Add it to $PSScriptRoot\publish.env or set it in the process environment."
}
$HUB_URL = $HUB_URL.TrimEnd('/')

# ── Channel selection ───────────────────────────────────────────────
$CHANNEL_FILE = "$PSScriptRoot\channel.txt"
$CHANNEL = Get-PublishConfig $PUBLISH_ENV 'TELA_PUBLISH_CHANNEL' $null
if (-not $CHANNEL) {
    if (-not (Test-Path $CHANNEL_FILE)) { Set-Content $CHANNEL_FILE "local" }
    $CHANNEL = (Get-Content $CHANNEL_FILE -Raw).Trim()
}
if ($CHANNEL -notmatch '^[A-Za-z0-9-]+$') {
    throw "Invalid channel name '$CHANNEL'. Use [A-Za-z0-9-] only."
}

# ── Per-channel build counter and version tag ────────────────────────
$BASE = (Get-Content "$ROOT\VERSION" -Raw).Trim()
$COUNTER_FILE = "$PSScriptRoot\$CHANNEL-build-counter"
$N = 1
if (Test-Path $COUNTER_FILE) {
    $N = [int](Get-Content $COUNTER_FILE -Raw) + 1
}
Set-Content $COUNTER_FILE $N
$TAG = "v$BASE.0-$CHANNEL.$N"

New-Item -ItemType Directory -Force -Path $DIST | Out-Null

# ── Build ───────────────────────────────────────────────────────────
$env:CGO_ENABLED = "0"
$cliBinaries = @("tela", "telad", "telahubd")

Push-Location $ROOT
try {
    Write-Host ""
    Write-Host "==> Building $TAG CLI for linux/amd64" -ForegroundColor Cyan
    $env:GOOS = "linux"; $env:GOARCH = "amd64"
    foreach ($bin in $cliBinaries) {
        Write-Host "    $bin-linux-amd64"
        go build -trimpath -ldflags="-s -w -X main.version=$TAG" -o "$DIST\$bin-linux-amd64" "./cmd/$bin"
        if ($LASTEXITCODE -ne 0) { throw "go build $bin (linux/amd64) failed" }
    }

    Write-Host ""
    Write-Host "==> Building $TAG CLI for windows/amd64" -ForegroundColor Cyan
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    foreach ($bin in $cliBinaries) {
        Write-Host "    $bin-windows-amd64.exe"
        go build -trimpath -ldflags="-s -w -X main.version=$TAG" -o "$DIST\$bin-windows-amd64.exe" "./cmd/$bin"
        if ($LASTEXITCODE -ne 0) { throw "go build $bin (windows/amd64) failed" }
    }
    Remove-Item Env:GOOS; Remove-Item Env:GOARCH; Remove-Item Env:CGO_ENABLED
} finally {
    Pop-Location
}

Write-Host ""
Write-Host "==> Building $TAG TelaVisor for windows/amd64" -ForegroundColor Cyan
Push-Location "$ROOT\cmd\telagui"
wails build -ldflags "-X main.version=$TAG"
if ($LASTEXITCODE -ne 0) { throw "wails build telavisor failed" }
Pop-Location
Copy-Item "$ROOT\cmd\telagui\build\bin\telavisor.exe" "$DIST\telavisor-windows-amd64.exe" -Force

# ── Upload to the hub ───────────────────────────────────────────────
if (-not $TOKEN) {
    throw "Build succeeded but no TELA_PUBLISH_TOKEN configured. Add it to $PSScriptRoot\publish.env or set `$env:TELA_PUBLISH_TOKEN."
}

Write-Host ""
Write-Host "==> Uploading binaries to $HUB_URL/api/admin/channels/files/$CHANNEL/" -ForegroundColor Cyan

$allFiles = @(
    "tela-linux-amd64",
    "telad-linux-amd64",
    "telahubd-linux-amd64",
    "tela-windows-amd64.exe",
    "telad-windows-amd64.exe",
    "telahubd-windows-amd64.exe",
    "telavisor-windows-amd64.exe"
)

$headers = @{ Authorization = "Bearer $TOKEN" }

foreach ($f in $allFiles) {
    $src = "$DIST\$f"
    if (-not (Test-Path $src)) { throw "Missing build output: $src" }
    Write-Host ("    {0,-44}  {1} bytes" -f $f, (Get-Item $src).Length)
    $url = "$HUB_URL/api/admin/channels/files/$CHANNEL/$f"
    try {
        Invoke-WebRequest -Uri $url -Method Put -InFile $src -Headers $headers -ContentType 'application/octet-stream' -UseBasicParsing | Out-Null
    } catch {
        throw "upload $f failed: $($_.Exception.Message)"
    }
}

# ── Publish the manifest ────────────────────────────────────────────
Write-Host ""
Write-Host "==> Publishing $CHANNEL channel manifest (tag: $TAG)" -ForegroundColor Cyan

$publishBody = @{ channel = $CHANNEL; tag = $TAG } | ConvertTo-Json -Compress
$publishUrl  = "$HUB_URL/api/admin/channels/publish"
try {
    $resp = Invoke-RestMethod -Uri $publishUrl -Method Post -Headers $headers -ContentType 'application/json' -Body $publishBody -UseBasicParsing
} catch {
    throw "publish failed: $($_.Exception.Message)"
}

if ($resp.binaries) {
    foreach ($prop in $resp.binaries.PSObject.Properties) {
        $info = $prop.Value
        Write-Host ("  {0,-44}  {1}...  {2} bytes" -f $prop.Name, $info.sha256.Substring(0,16), $info.size)
    }
}

Write-Host ""
Write-Host "==> Done. $CHANNEL channel updated to $TAG" -ForegroundColor Green
Write-Host "    manifest: $($resp.manifestPath)"
Write-Host "    base:     $($resp.downloadBase)"
Write-Host "    public:   $HUB_URL/channels/$CHANNEL.json"
Write-Host ""

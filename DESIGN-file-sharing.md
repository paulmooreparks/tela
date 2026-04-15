# Tela File Sharing -- Design Document

## Overview

Tela File Sharing adds a sandboxed file transfer channel to the existing
WireGuard tunnel between `tela` (client) and `telad` (agent). Files flow
through the same E2E-encrypted tunnel that carries TCP service traffic.
The hub remains a zero-knowledge relay.

The agent operator declares a shared directory in `telad.yaml`. Authorized
clients can list, download, and (optionally) upload files within that
directory. No access is granted outside the declared directory. File
sharing is off by default and must be explicitly enabled per machine.

This feature is native to the Tela protocol. It does not depend on SSH,
SFTP, or any other service running on the agent machine.

## Design principles

**Secure by default.** File sharing is disabled unless the agent operator
adds a `shares` list to the machine config. No flags, no environment
variables can enable it implicitly.

**Sandboxed.** All file operations are confined to a single declared
directory. Path traversal outside the sandbox is rejected at the protocol
level (server-side validation) and never delegated to OS-level permissions
alone.

**Minimal surface.** The protocol supports seven operations: list, read,
write, delete, mkdir, rename, and move, plus a subscribe operation for
live change notifications. No chmod, no symlink resolution.

**Operator-controlled.** The agent operator (the person running `telad`)
controls what is shared, who can access it, whether writes are allowed,
and how much space can be consumed. The client cannot negotiate broader
access than the operator has configured.

**Zero-knowledge relay.** File contents are encrypted inside the WireGuard
tunnel. The hub relays opaque ciphertext. This is identical to how TCP
service traffic works today.

## Agent configuration

### 3.1 telad.yaml schema

```yaml
machines:
  - name: barn
    ports: [22, 3389]
    shares:
      - name: files
        path: /home/shared             # absolute path, required
        writable: false                # default: false (read-only)
        maxFileSize: 100MB             # per-file upload limit, default: 50MB
        maxTotalSize: 1GB              # total directory size limit, default: none
        allowDelete: false             # default: false
        allowedExtensions: []          # empty = all allowed; e.g. [".txt", ".log", ".yaml"]
        blockedExtensions: [".exe", ".bat", ".cmd", ".ps1", ".sh"]
```

The `fileShare:` (singular) key is still accepted and is synthesized as a share named `legacy`. It will be removed at 1.0.

### 3.2 Field reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | (required) | Share name. Used in WebDAV paths (`/machine/share/path`) and the `-share NAME` flag on `tela files` commands. |
| `path` | string | (required) | Absolute path to the shared directory. Created on startup if it does not exist (0700 permissions on Unix, standard permissions on Windows). telad refuses to start if the path is a system directory. |
| `writable` | bool | `false` | Allows clients to upload files. When false, only list and download are permitted. |
| `maxFileSize` | string | `50MB` | Maximum size of a single uploaded file. Supports KB, MB, GB suffixes. |
| `maxTotalSize` | string | (none) | Maximum total size of all files in the shared directory. Uploads that would exceed this limit are rejected. When unset, no total limit is enforced. |
| `allowDelete` | bool | `false` | Allows clients to delete files. Requires `writable: true`. |
| `allowedExtensions` | []string | `[]` | Whitelist of file extensions. Empty means all extensions are allowed (subject to `blockedExtensions`). |
| `blockedExtensions` | []string | see above | Blacklist of file extensions. Applied after `allowedExtensions`. Default blocks common executable extensions. |

### 3.3 Directory rules

- Each share path must be an absolute path.
- telad creates the directory on startup if it does not exist (with 0700
  permissions on Unix, standard permissions on Windows).
- telad validates that no share path is a system directory (`/`, `/etc`,
  `C:\Windows`, `C:\`, etc.).
- Symlinks inside the shared directory are not followed. A file operation
  on a symlink returns an error.
- Hidden files (dot-prefix on Unix, hidden attribute on Windows) are
  included in listings by default.

### 3.4 Go struct

```go
type shareConfig struct {
    Name              string   `yaml:"name"`
    Path              string   `yaml:"path"`
    Writable          bool     `yaml:"writable,omitempty"`
    MaxFileSize       string   `yaml:"maxFileSize,omitempty"`       // "50MB", "1GB", etc.
    MaxTotalSize      string   `yaml:"maxTotalSize,omitempty"`
    AllowDelete       bool     `yaml:"allowDelete,omitempty"`
    AllowedExtensions []string `yaml:"allowedExtensions,omitempty"`
    BlockedExtensions []string `yaml:"blockedExtensions,omitempty"`
}
```

Added to `machineConfig`:

```go
type machineConfig struct {
    // ... existing fields ...
    Shares    []shareConfig   `yaml:"shares,omitempty"`
    FileShare *fileShareConfig `yaml:"fileShare,omitempty"` // deprecated: synthesized as shares[{name:"legacy"}]
}
```

## Capability advertisement

File sharing capability is advertised during session setup using the
existing control message flow. No new message types are required for
negotiation.

### 4.1 Agent registration

The agent's `register` message gains an optional `capabilities` field:

```json
{
  "type": "register",
  "machineId": "barn",
  "ports": [22, 3389],
  "services": [...],
  "capabilities": {
    "shares": [
      {"name": "files", "writable": false, "maxFileSize": 104857600}
    ]
  }
}
```

The hub stores this in the machine entry metadata and includes it in
`/api/status` responses. Clients and UIs (TelaVisor, hub console, portal)
can display file sharing availability without establishing a session.

### 4.2 Session establishment

During session setup, the agent's `wg-pubkey` response includes the
`capabilities` field. The client uses this to determine whether file
operations are available for the active session.

No separate handshake is needed. The client either uses the file sharing
channel or it does not.

### 4.3 Hub passthrough

The hub does not interpret capabilities. It stores them as opaque metadata
for the status API and relays them during session signaling. No hub code
changes are required beyond passing the field through.

## Protocol

File sharing uses a simple request/response protocol over a dedicated TCP
connection inside the WireGuard tunnel. The agent listens on a fixed port
(`17377`, mnemonic: "tela" on a phone keypad = 8352, but 17377 avoids
conflicts) within the tunnel's virtual IP space.

### 5.1 Why a dedicated port, not a new message type

The existing binary relay carries WireGuard datagrams. Multiplexing file
transfer into the same stream would require framing changes. A TCP
connection inside the tunnel avoids this entirely and gets congestion
control, flow control, and ordering for free.

This is the same pattern used for service forwarding: the client dials a
TCP port on the agent's tunnel IP. The only difference is that the file
sharing port is handled by telad itself rather than forwarded to a local
service.

### 5.2 Transport

- Client dials `10.77.{N}.1:17377` through the gVisor netstack (same
  mechanism used for service forwarding).
- The connection is inside the WireGuard tunnel, so it inherits E2E
  encryption.
- The agent accepts the connection only when at least one share is configured.

### 5.3 Message format

JSON-line protocol. Each message is a single line of JSON terminated by
`\n`. Binary file data is transmitted as raw bytes following a header that
specifies the length.

#### Request envelope

```json
{"op": "<operation>", "path": "<relative-path>", ...operation-specific fields}
```

#### Response envelope

```json
{"ok": true, ...operation-specific fields}
{"ok": false, "error": "<message>"}
```

### 5.4 Operations

#### LIST

List files and directories at a path.

Request:
```json
{"op": "list", "path": ""}
{"op": "list", "path": "subdir"}
```

Response:
```json
{
  "ok": true,
  "entries": [
    {"name": "readme.txt", "size": 1234, "modTime": "2026-03-20T10:30:00Z", "isDir": false},
    {"name": "logs",       "size": 0,    "modTime": "2026-03-19T08:00:00Z", "isDir": true}
  ]
}
```

#### READ

Download a file.

Request:
```json
{"op": "read", "path": "readme.txt"}
```

Response (success):
```json
{"ok": true, "size": 1234, "modTime": "2026-03-20T10:30:00Z", "checksum": "sha256:<hex>"}
```
Followed immediately by `size` bytes of raw file data.

Response (error):
```json
{"ok": false, "error": "file not found"}
```

#### WRITE

Upload a file. Only available when `writable: true`.

Request:
```json
{"op": "write", "path": "config-backup.yaml", "size": 5678, "checksum": "sha256:<hex>"}
```
Followed immediately by `size` bytes of raw file data.

Response:
```json
{"ok": true, "size": 5678}
```

The agent validates:
- `writable` is enabled
- The file extension is allowed
- The file size does not exceed `maxFileSize`
- The total directory size after write does not exceed `maxTotalSize`
- The path does not escape the sandbox
- The path does not reference a symlink
- The checksum matches after receiving all bytes

If any check fails, the agent returns an error and discards the partial
data.

If a file with the same name already exists, it is overwritten.

#### DELETE

Delete a file. Only available when `allowDelete: true`.

Request:
```json
{"op": "delete", "path": "old-log.txt"}
```

Response:
```json
{"ok": true}
```

Both files and empty directories can be deleted. Deleting a non-empty directory returns an error.

#### MKDIR

Create a directory. Only available when `writable: true`.

Request:
```json
{"op": "mkdir", "path": "subdir/new-folder"}
```

Response:
```json
{"ok": true}
```

The agent validates:
- `writable` is enabled
- The path does not escape the sandbox
- The path does not reference a symlink
- The parent directory exists

#### RENAME

Rename a file or directory. Only available when `writable: true`. The
new name must be a simple filename (not a path). The item stays in the
same directory.

Request:
```json
{"op": "rename", "path": "old-name.txt", "newName": "new-name.txt"}
```

Response:
```json
{"ok": true}
```

The agent validates:
- `writable` is enabled
- The source path exists and does not escape the sandbox
- The new name does not contain path separators
- The new name's extension is allowed by extension filters

#### MOVE

Move a file or directory to a different location within the shared
directory. Only available when `writable: true`.

Request:
```json
{"op": "move", "path": "logs/old-log.txt", "destination": "archive/old-log.txt"}
```

Response:
```json
{"ok": true}
```

The agent validates:
- `writable` is enabled
- Both source path and destination path are within the sandbox
- The destination's parent directory exists
- The destination file's extension is allowed by extension filters
- Neither path references a symlink

#### SUBSCRIBE

Subscribe to live file change notifications for the shared directory.
The agent watches the directory for changes using filesystem
notifications (fsnotify) and streams events to the client.

Request:
```json
{"op": "subscribe"}
```

Response (initial acknowledgment):
```json
{"ok": true}
```

The agent then sends event messages as changes occur:
```json
{"event": "created", "path": "new-file.txt", "isDir": false}
{"event": "modified", "path": "config.yaml", "isDir": false}
{"event": "deleted", "path": "old-log.txt", "isDir": false}
{"event": "renamed", "path": "report.txt", "isDir": false}
```

The subscription remains active until the client closes the connection.

### 5.5 Path validation (security-critical)

Every path received from the client is validated before any filesystem
operation:

1. The path is cleaned with `filepath.Clean`.
2. The path is joined to the sandbox root with `filepath.Join(root, path)`.
3. The result is checked with `filepath.Rel(root, joined)` to confirm it
   does not escape the sandbox. If `Rel` returns a path starting with
   `..`, the request is rejected.
4. `os.Lstat` (not `os.Stat`) is used to detect symlinks. If the target
   is a symlink, the request is rejected.
5. On Windows, UNC paths, drive-letter prefixes, and alternate data
   streams (`:`) in the client-supplied path are rejected.

This validation runs on every operation, including LIST. There is no
caching of validated paths.

### 5.6 Chunked transfer and tunnel-safe sizing

File data (both READ and WRITE) is transferred in chunks rather than as
a single raw stream. This avoids silent packet drops in constrained
network environments.

#### The problem

The WireGuard tunnel runs inside gVisor netstack, which runs over
WebSocket or UDP relay. On WSL2, the layered virtual networking (WSL2
NIC + WireGuard + relay transport) silently drops TCP segments above
a certain effective size. This manifests as SSH key exchange failures
and will affect file transfers identically. Native Windows and native
Linux are not affected.

#### Chunk protocol

For READ responses and WRITE requests, file data is sent in chunks
with explicit framing:

```
JSON header line (includes total size, checksum)
CHUNK <length>\n
<length bytes of data>
CHUNK <length>\n
<length bytes of data>
...
CHUNK 0\n
```

The maximum chunk size is **16384 bytes (16 KB)**. This keeps each
chunk well within the effective MTU of all known tunnel configurations,
including WSL2. The sender may use smaller chunks. A zero-length chunk
signals end-of-data.

The receiver validates:
- Total bytes received matches the `size` in the header
- SHA-256 checksum of all received data matches the `checksum` in the
  header

If the stream stalls (no data received for 30 seconds), the receiver
closes the connection and reports a timeout error.

#### Why not raw streaming

A raw stream of `size` bytes works when the TCP stack reliably delivers
all segments. On WSL2, segments above the effective MTU are dropped
without error. With raw streaming, the receiver blocks forever waiting
for bytes that will never arrive. Chunked framing with timeouts
makes the failure detectable and reportable.

### 5.7 Port constant

```go
const FileSharePort = 17377
```

The port is not configurable. It is a protocol constant, like WireGuard's
default 51820. This simplifies client implementation and avoids
negotiation.

## Hub RBAC integration

### 6.1 Permission model

File sharing piggybacks on the existing `connect` permission. If a token
can connect to a machine, it can use file sharing on that machine (subject
to the agent's `shares` config).

Rationale: file sharing is a feature of the tunnel, not a separate
service. The agent operator controls what is shared and whether writes are
allowed. The hub controls who can establish a tunnel. Adding a separate
`canTransferFiles` permission would create a matrix explosion (connect +
files, connect without files, files without connect) for limited benefit.

### 6.2 Future: per-identity write restriction

A future enhancement could allow the hub to restrict specific tokens to
read-only file access even when the agent allows writes:

```yaml
auth:
  machines:
    barn:
      connectTokens: [alice-token, bob-token]
      readOnlyFileTokens: [bob-token]   # future
```

This is not part of the initial implementation.

## Client interface

### 7.1 CLI: `tela files`

```bash
# List files on a connected machine
tela files ls -machine barn -share files
tela files ls -machine barn -share files subdir/

# Download a file
tela files get -machine barn -share files readme.txt
tela files get -machine barn -share files readme.txt -o /local/path/readme.txt

# Upload a file (if writable)
tela files put -machine barn -share files localfile.txt
tela files put -machine barn -share files localfile.txt remote-name.txt

# Delete a file (if allowDelete)
tela files rm -machine barn -share files old-log.txt

# Create a directory (if writable)
tela files mkdir -machine barn -share files subdir/new-folder

# Rename a file or directory (if writable)
tela files rename -machine barn -share files old-name.txt new-name.txt

# Move a file or directory (if writable)
tela files mv -machine barn -share files logs/old-log.txt archive/old-log.txt

# Show file sharing status for a machine
tela files info -machine barn
```

### 7.2 How `tela files` connects

`tela files` requires an active tunnel. It does not establish its own
connection. The user runs `tela connect` first (or uses a profile), then
runs `tela files` in a separate terminal.

To find the tunnel, `tela files` connects to the tela control API (the
same localhost WebSocket that TelaVisor uses) and queries the active
session for the target machine's tunnel IP. Then it dials
`10.77.{N}.1:17377` through the netstack.

Alternative: `tela files` could accept `-hub` and `-machine` and establish
its own short-lived tunnel. This is more convenient but adds complexity.
Defer to the second iteration.

### 7.3 Profile integration

Connection profiles can declare file sharing preferences:

```yaml
connections:
  - hub: myhub
    machine: barn
    token: ${TOKEN}
    services:
      - remote: 22
    fileShare: true    # auto-connect to file share channel when available
```

When `fileShare: true` is set, `tela connect` prints the file share
status alongside the service bindings:

```
Connected to barn via myhub
  SSH:  localhost:22 -> barn:22
  Files: barn/files:/home/shared (read-only, 47 files)
```

## TelaVisor integration

### 8.1 Immediate (Phase 1)

**File browser panel.** A new tab in Clients mode (alongside Status and
Profiles) that shows the shared directory as a file list when connected to
a machine with file sharing enabled.

- Displays file name, size, modification time
- Download button per file (saves via system file dialog)
- Upload button (when writable) via system file dialog
- Delete button (when allowDelete) with confirmation
- Breadcrumb navigation for subdirectories
- Drag-and-drop upload (when writable)

**Capability indicator.** The machine list in Profiles and the Machines
view in Hubs mode show a file-sharing icon when a machine advertises the
capability. The icon indicates read-only or read-write.

**Status integration.** The Status tab shows file sharing availability
alongside service status:

```
barn (connected)
  SSH         localhost:22     Listening
  RDP         localhost:3389   Listening
  File Share  /home/shared     Read-only, 47 files
```

### 8.2 Later (Phase 2)

**Config push/pull.** In Agents mode (when it ships), a "Configuration"
panel that:
- Downloads the remote `telad.yaml` via the file share channel
- Shows it in an editor
- Saves it back (if writable)
- Offers a "Restart Agent" button (via SSH or a future management channel)

**Log retrieval.** A "Remote Logs" tab that downloads log files from the
shared directory. The agent operator configures a share pointing at a
logs directory, and TelaVisor provides a log viewer.

**Binary deployment.** In the Settings binary manager, an "Update Remote
Agent" button that uploads a new `telad` binary to the shared directory
and (via a future management channel) triggers a restart.

### 8.3 Command log integration

All file operations appear in the Commands tab of the log panel:

```
[GET]  files/list  barn:""           200  12 entries
[GET]  files/read  barn:"config.yaml"  200  2.3 KB
[PUT]  files/write barn:"backup.yaml"  200  1.1 KB
```

## Awan Saya integration

### 9.1 Portal dashboard

The portal's hub status cards already show machines and services. Add a
file-sharing indicator to each machine card:

```
barn (online)
  SSH :22  |  RDP :3389  |  Files (read-only)
```

The capability data comes from the hub's `/api/status` response, which
already passes through machine metadata. No new portal-to-hub API calls
are needed.

### 9.2 Web-based file browser (future)

The portal could offer a browser-based file manager for machines with file
sharing enabled. This would require the portal to proxy file operations
through the hub or establish its own tunnel. This is a significant
undertaking and belongs in a later Awan Saya phase.

### 9.3 Org-level file sharing policies (future)

When the org/team/account model is in place, Awan Saya could enforce
org-level policies on file sharing:

- "No file sharing on production hubs"
- "Read-only file sharing only"
- "Max 10MB file size across all hubs in this org"

These would be enforced at the hub level via sync from the portal. This
is a platform feature, not a Tela core feature.

### 9.4 Audit trail

File operations can be logged to the hub's `/api/history` event stream:

```json
{
  "type": "file-read",
  "machine": "barn",
  "identity": "alice",
  "path": "config.yaml",
  "size": 2345,
  "timestamp": "2026-03-20T14:30:00Z"
}
```

This flows through the existing history ring buffer and appears in the
portal dashboard, hub console, and TelaVisor command log.

## Use cases

### 10.1 Tela management

**Config backup and restore.** An operator shares the `/etc/tela/`
directory on each agent. They can pull configs for backup, push updated
configs, and (with a service restart) reconfigure agents remotely. No SSH
required.

```yaml
# telad.yaml on each managed machine
machines:
  - name: web01
    ports: [22, 8080]
    shares:
      - name: config
        path: /etc/tela
        writable: true
        allowedExtensions: [".yaml", ".yml"]
        maxFileSize: 1MB
```

**Log collection.** An operator shares the log directory. TelaVisor or a
script pulls logs from multiple machines for analysis.

```yaml
shares:
  - name: logs
    path: /var/log/tela
    writable: false
```

**Binary deployment.** An operator shares a staging directory. They upload
new telad binaries, then SSH in (or use a future management command) to
swap and restart.

```yaml
shares:
  - name: staging
    path: /opt/tela/staging
    writable: true
    allowedExtensions: []
    blockedExtensions: []    # allow binaries in the staging dir
    maxFileSize: 200MB
```

### 10.2 Developer workflows

**Sharing build artifacts.** A developer connects to a staging machine
and uploads a build artifact for testing:

```bash
tela files put -machine staging -share staging api-server-v2.3.tar.gz
```

**Pulling database dumps.** A DBA connects to a database machine that
shares a dump directory and downloads a backup:

```bash
tela files get -machine db01 -share dumps nightly-backup-2026-03-20.sql.gz
```

**Sharing configuration between environments.** Pull the config from
production, edit locally, push to staging:

```bash
tela files get -machine prod-web -share config app-config.yaml
# edit locally
tela files put -machine staging-web -share config app-config.yaml
```

### 10.3 IT support / MSP

**Collecting diagnostics.** A support technician connects to a customer
machine. The agent shares a diagnostics directory where scheduled tasks
drop system info, event logs, and health reports:

```yaml
shares:
  - name: diagnostics
    path: C:\TelaDiagnostics
    writable: false
    maxTotalSize: 500MB
```

The tech downloads what they need without asking the customer to email
files or use a separate file sharing service.

**Deploying patches or scripts.** The tech uploads a fix to the staging
directory, then runs it via RDP or SSH:

```yaml
shares:
  - name: staging
    path: C:\TelaStaging
    writable: true
    maxFileSize: 50MB
    allowedExtensions: [".msi", ".msp", ".zip"]
```

### 10.4 IoT / edge

**Firmware updates.** An IoT device shares a firmware staging directory.
The operator uploads a firmware image and triggers an update:

```bash
tela files put -machine sensor-42 -share firmware firmware-v3.1.bin
ssh user@localhost "sudo /opt/update-firmware.sh"
```

**Log retrieval from constrained devices.** Devices that cannot run a
log aggregator share their log directory read-only. The operator pulls
logs on demand:

```bash
tela files ls -machine sensor-42 -share logs
tela files get -machine sensor-42 -share logs syslog.txt
```

### 10.5 Education / labs

**Distributing assignments.** An instructor shares a read-only directory
with course materials. Students connect to their assigned lab machine and
download the files they need.

**Collecting submissions.** Students connect to a submission machine that
has a write-only shared directory (writable, no delete, no list... though
the current design requires list, a future `listable: false` option
could support drop-box semantics).

### 10.6 General file exchange

**Ad hoc file sharing between any two Tela-connected machines.** Any
machine running telad with file sharing enabled becomes a lightweight file
server for authorized Tela users. No separate file sharing service, no
cloud storage, no email attachments. The files stay on the machines and
travel through the encrypted tunnel.

## Implementation plan

### Phase 1: Agent-side file server

1. Add `shareConfig` struct and `shares:` YAML parsing to `telad`.
2. Validate each share path on startup (exists, not a system dir,
   permissions).
3. Start a TCP listener on port 17377 inside the gVisor netstack when
   at least one share is configured.
4. Implement the four operations (list, read, write, delete) with path
   validation per share.
5. Add `capabilities` field to the registration and `wg-pubkey` control
   messages.

### Phase 2: Client CLI

1. Add `tela files` subcommand (ls, get, put, rm, info).
2. Connect to the active tunnel's file share port via the control API.
3. Handle checksums, progress display, and error reporting.

### Phase 3: TelaVisor file browser

1. Add a Files tab in Clients mode.
2. Implement list, download, upload, delete via the Go backend.
3. Show file sharing capability in machine lists and status views.

### Phase 4: Hub and portal integration

1. Pass `capabilities` through `/api/status`.
2. Add file operation events to the history ring buffer.
3. Show file sharing indicators in the hub console and portal dashboard.

### Phase 5: Advanced features (deferred)

- Resume interrupted transfers
- Directory upload/download (tar stream)
- `listable: false` (drop-box mode)
- Per-identity write restrictions at the hub level
- Progress reporting via the control WebSocket
- Transfer rate limiting

## Security checklist

Before shipping, verify:

- [x] Path traversal: `../`, absolute paths, symlinks, UNC paths, Windows
  alternate data streams all rejected
- [x] Sandbox escape: no operation can read or write outside the declared
  directory
- [x] Size limits: `maxFileSize` and `maxTotalSize` enforced before writing
  any bytes to disk
- [x] Extension filtering: `allowedExtensions` and `blockedExtensions`
  applied to all write and delete operations
- [x] Symlink rejection: `os.Lstat` used everywhere, symlinks never
  followed
- [x] System directory rejection: telad refuses to start with common
  system paths as the shared directory
- [x] Checksum validation: uploaded files validated after receipt, corrupt
  files discarded
- [ ] Constant-time comparison: not applicable (no secrets in the file
  protocol), but validate that token auth on the tunnel is unchanged
- [x] Writable/delete flags: operations rejected when the corresponding
  flag is false
- [x] No implicit enable: file sharing requires explicit `enabled: true`
  in the config
- [x] Resource exhaustion: connection limit on the file share listener
  (e.g., max 4 concurrent file operations per session)
- [x] Timeout: idle file share connections closed after 60 seconds
- [x] Log all operations: every list, read, write, delete logged with
  identity, path, size, and result
- [x] Chunk timeout: transfers that stall for 30 seconds are terminated
  with a clear error, not left hanging
- [ ] WSL2 testing: verify list, read, write, and delete all work from
  a WSL2 client connecting through a tela tunnel (WSL2's virtual NIC
  silently drops oversized TCP segments)

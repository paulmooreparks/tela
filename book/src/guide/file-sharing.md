# File sharing

Tela file sharing lets authorized clients browse, download, upload, rename, move, and delete files on a remote machine through the same encrypted WireGuard tunnel that carries TCP service traffic. No SSH, no SFTP, and no separate credentials are required beyond a Tela token with connect permission on the machine.

File sharing is off by default and must be explicitly enabled per machine by the agent operator.

## Enabling file sharing

Add a `shares` list to a machine in `telad.yaml`. Each entry defines one shared directory with its own name and access controls.

```yaml
machines:
  - name: barn
    ports: [22, 3389]
    shares:
      - name: files
        path: /home/shared
```

`telad` creates each share directory on startup if it does not exist. Each path must be absolute. `telad` refuses to start if any share path is a system directory (`/`, `/etc`, `C:\Windows`, and similar).

If you are upgrading from an older configuration that used `fileShare:` (singular), that key is still accepted and is synthesized as a share named `legacy`. It will be removed at 1.0. Migrate to the `shares` list.

### Configuration reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Share name. Used in WebDAV paths (`/machine/share/path`) and the `-share NAME` flag on `tela files` commands. |
| `path` | string | required | Absolute path to the shared directory. |
| `writable` | bool | `false` | Allows clients to upload files and create directories. When false, only list and download are available. |
| `allowDelete` | bool | `false` | Allows clients to delete files and empty directories. Requires `writable: true`. |
| `maxFileSize` | string | `50MB` | Maximum size of a single uploaded file. Accepts `KB`, `MB`, and `GB` suffixes. |
| `maxTotalSize` | string | none | Maximum total size of all files in the shared directory. Uploads that would exceed this limit are rejected. |
| `allowedExtensions` | []string | `[]` | Whitelist of file extensions. Empty means all extensions are allowed, subject to `blockedExtensions`. |
| `blockedExtensions` | []string | see below | Blacklist of file extensions. By default blocks `.exe`, `.bat`, `.cmd`, `.ps1`, and `.sh`. Applied after `allowedExtensions`. |

### A read-only log share

```yaml
shares:
  - name: logs
    path: /var/log/app
    writable: false
```

### A writable staging area

```yaml
shares:
  - name: staging
    path: /opt/staging
    writable: true
    allowDelete: true
    maxFileSize: 200MB
    maxTotalSize: 2GB
    allowedExtensions: [".zip", ".tar.gz", ".yaml"]
```

### Multiple shares on one machine

```yaml
shares:
  - name: logs
    path: /var/log/app
    writable: false
  - name: uploads
    path: /opt/uploads
    writable: true
    allowDelete: true
    maxFileSize: 50MB
```

## Access from the CLI

The `tela files` subcommand provides operations on connected machines. An active tunnel must be established with `tela connect` first.

```bash
# List files in a share
tela files ls -machine barn -share files
tela files ls -machine barn -share files subdir/

# Download a file
tela files get -machine barn -share files report.pdf
tela files get -machine barn -share files report.pdf -o /local/report.pdf

# Upload a file (requires writable: true)
tela files put -machine barn -share files localfile.txt
tela files put -machine barn -share files localfile.txt remote-name.txt

# Delete a file (requires allowDelete: true)
tela files rm -machine barn -share files old-log.txt

# Create a directory (requires writable: true)
tela files mkdir -machine barn -share files archive/2026

# Rename a file or directory (requires writable: true)
tela files rename -machine barn -share files old-name.txt new-name.txt

# Move a file or directory (requires writable: true)
tela files mv -machine barn -share files logs/jan.txt archive/2026/jan.txt

# Show file sharing status for a machine (lists all shares)
tela files info -machine barn
```

## Mounting as a local drive

`tela mount` starts a WebDAV server that exposes Tela file shares as a local drive. Each connected machine with file sharing enabled appears as a top-level folder, with each share as a subfolder inside it (`/machine/share/path`).

```bash
# Windows: mount as drive letter T:
tela mount -mount T:

# macOS/Linux: mount to a directory
tela mount -mount ~/tela
```

No kernel drivers or third-party software are required. On Windows this uses the built-in WebDAV client (WebClient service). On macOS and Linux it uses the OS WebDAV mount support.

## Access from TelaVisor

The Files tab in TelaVisor provides a graphical file browser for machines with file sharing enabled. It shows file name, size, and modification time. You can download files via the system file dialog, upload files (when `writable: true`), delete files (when `allowDelete: true`), navigate subdirectories with breadcrumb navigation, and drag and drop files to upload.

The machine list in the Connections view shows a file-sharing indicator when a machine advertises the capability, distinguishing between read-only and read-write configurations.

## Security

File sharing uses the existing `connect` permission. A token that can connect to a machine can use file sharing on that machine. No separate permission is required.

All file operations are sandboxed to the declared directory. Path traversal is rejected at the protocol level: the server validates every client-supplied path using `filepath.Rel` to confirm it cannot escape the sandbox, and uses `os.Lstat` to reject symlinks. No file operation is delegated to OS-level permissions alone.

The shared directory is never accessible without an active authenticated Tela session. File contents travel inside the WireGuard tunnel as ciphertext. The hub sees nothing different from any other tunnel traffic.

For the design rationale behind these choices, see [File sharing](../architecture/file-sharing.md) in the Design Rationale section.

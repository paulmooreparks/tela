# File sharing

Tela file sharing adds a sandboxed file transfer channel to the existing WireGuard tunnel between client and agent. Files flow through the same end-to-end encrypted connection that carries TCP service traffic. The hub remains a zero-knowledge relay: it sees opaque ciphertext regardless of whether the tunnel is carrying an SSH session or a file download.

## Why not SSH or SFTP

The obvious alternative is to forward port 22 through the tunnel and use SFTP. That works, but it requires SSH to be installed and running on the target machine, the user to have shell credentials, and either a separate SFTP client or a tool that speaks SFTP. On Windows machines that expose only RDP, SSH is often absent. On locked-down servers, credentials may not exist for the operating user.

A native file transfer channel removes all of those prerequisites. If `telad` is running and file sharing is enabled, any authorized Tela client can transfer files without SSH, without separate credentials, and without any software beyond `tela` itself.

## The design principles

**Secure by default.** File sharing is disabled unless the agent operator adds a `fileShare` block to the machine config. No flag, no environment variable, and no runtime prompt can enable it implicitly. The operator must take a deliberate action.

**Sandboxed.** All file operations are confined to a single declared directory. Path traversal outside the sandbox is rejected by the server using `filepath.Rel` to detect any attempt to escape, and `os.Lstat` to detect symlinks. No operation is delegated to OS-level permissions alone.

**Operator-controlled.** The agent operator controls what is shared, whether writes are allowed, whether deletes are allowed, what file extensions are permitted, and how much space can be consumed. The client cannot negotiate broader access than the operator has configured.

**Minimal surface.** The protocol supports eight operations: list, read, write, delete, mkdir, rename, move, and subscribe (for live change notifications). No chmod, no symlink resolution, no arbitrary shell access.

**Zero-knowledge relay.** File contents travel inside the WireGuard tunnel as ciphertext. The hub sees nothing different from any other tunnel traffic.

## Why a dedicated port, not a new message type

The existing transport carries WireGuard datagrams. Multiplexing file transfer into the same stream would require framing changes across the entire protocol. A TCP connection on a fixed port inside the tunnel avoids this entirely and inherits congestion control, flow control, and ordering from TCP for free.

This is the same pattern used for service forwarding: the client dials a TCP port on the agent's tunnel IP. File sharing uses port 17377, which `telad` handles directly rather than forwarding to a local service.

## The permission model

File sharing piggybacks on the existing `connect` permission. A token that can connect to a machine can use file sharing on that machine, subject to the agent's `fileShare` configuration. A separate `canTransferFiles` permission would create a combinatorial matrix (connect with files, connect without files, files without connect) for limited practical benefit. The agent operator already controls the meaningful distinctions: writable or read-only, delete allowed or not, which extensions are permitted.

## Chunked transfer

File data is sent in 16 KB chunks with explicit framing rather than as a raw byte stream. The reason is a real failure mode: on WSL2, the layered virtual networking stack (WSL2 network interface, WireGuard, relay transport) silently drops TCP segments above a certain effective size. A raw stream stalls without error when this happens. Chunked framing with a 30-second stall timeout makes the failure detectable and reportable instead of leaving the transfer hanging indefinitely.

Each chunk is preceded by a `CHUNK <length>` header line. A zero-length chunk signals end-of-data. Both the sender and receiver validate a SHA-256 checksum against the total transfer.

## Access from the client

The `tela files` subcommand provides a CLI interface: `ls`, `get`, `put`, `rm`, `mkdir`, `rename`, `mv`, and `info`. It requires an active tunnel established with `tela connect` and dials the file share port through the same netstack that handles service traffic.

The TelaVisor Files tab provides a graphical file browser for the same operations, with drag-and-drop upload, breadcrumb navigation, and real-time directory updates via the subscribe operation.

The `tela mount` command starts a WebDAV server that exposes Tela file shares as a local drive. On Windows, `tela mount -mount T:` maps a drive letter. On macOS and Linux, `tela mount -mount ~/tela` mounts to a directory. Each connected machine with file sharing enabled appears as a top-level folder.

For the full configuration reference, see [Appendix B: Configuration file reference](../guide/configuration.md).

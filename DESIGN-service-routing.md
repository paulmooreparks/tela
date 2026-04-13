# Service Routing Design

> **Superseded.** The core addressing problem described here was solved
> by the deterministic loopback addressing feature (Phase 1) documented
> in [DESIGN-local-names.md](DESIGN-local-names.md). Each machine now
> gets a stable `127.88.x.x` address computed from the hub URL and
> machine name, and services bind on their real ports. The DNS-based
> hostname resolution described below is planned as Phase 2. This
> document is retained for historical context.

Stable hostnames for tunneled services. Phase 2 of local name resolution.
Does not affect the wire format or the relay protocol.

## Problem (solved by Phase 1)

Previously, a tunneled service bound to `localhost:{dynamically-assigned-port}`.
The user ran `mstsc /v localhost:13390` or `ssh -p 10022 localhost`.
The port number changed when the profile was edited or ports were
reassigned. There was no stable address for a tunneled service.

Phase 1 solved the addressing half: each machine now gets a deterministic
`127.88.x.x` loopback IP so services use real ports without clashing.
The remaining problem is the name half: `ssh user@127.88.42.17` is
stable but not human-memorable.

The goal: `mstsc /v work-laptop-rdp` or `ssh owlsnest-ssh` with no
port number, surviving reconnects and profile changes.

## Approach: per-service loopback addresses

Windows, Linux, and macOS all support binding to addresses in the
`127.0.0.0/8` range beyond `127.0.0.1`. Each tunneled service gets
its own loopback IP and listens on the service's native port:

| Service | Loopback IP | Port | Hostname |
|---------|------------|------|----------|
| work-laptop RDP | 127.0.0.2 | 3389 | work-laptop-rdp |
| barn RDP | 127.0.0.3 | 3389 | barn-rdp |
| owlsnest SSH | 127.0.0.4 | 22 | owlsnest-ssh |

`mstsc /v work-laptop-rdp` resolves to `127.0.0.2:3389` via local
name resolution. tela's listener on that address tunnels the
connection to the correct machine. No port number. RDP NLA/CredSSP
sees the hostname as the SPN.

## Implementation layers

### 1. Deterministic loopback IP allocation

Assign IPs from `127.0.0.0/8` deterministically per
(hub, machine, service) tuple. A stable hash (e.g. the low 23 bits
of a SHA-256 of the tuple, mapped into `127.0.1.0/17` to avoid
`127.0.0.0` and `127.0.0.1`) ensures the same service always gets
the same IP across reconnects and profile reloads.

Range choice matters: `127.0.0.1` is reserved (the host itself).
`127.0.0.0` is the network address. The rest of `127.0.0.0/8`
(over 16 million addresses) is available. A subnet like
`127.0.1.0 - 127.127.255.254` gives plenty of room without
colliding with common loopback usage.

### 2. Bind to specific loopback IP and native port

tela's listener for each service binds to `{allocated-IP}:{native-port}`
instead of `127.0.0.1:{random-port}`. The profile YAML records the
allocation so it is stable across sessions.

### 3. Name resolution

The allocated IP needs a hostname. Three options, in order of
increasing complexity:

**Option A: hosts file.** Write entries to the system hosts file
(`/etc/hosts` on Unix, `%SystemRoot%\System32\drivers\etc\hosts` on
Windows). Simple, universally supported, no extra infrastructure.
Requires elevated privileges on Windows and root on Unix. tela
already runs unprivileged, so this would require a privileged helper
or the user running tela with elevation.

**Option B: platform DNS stub.** Register names with the OS-level
DNS resolver (e.g. `netsh interface ip add dns` on Windows,
`systemd-resolved` on Linux, `scutil` on macOS). Avoids touching the
hosts file but is platform-specific and fragile.

**Option C: built-in DNS resolver.** tela runs a small DNS server on
`127.0.0.1:53` (or a high port with an OS-level forwarder) that
answers queries for `*.tela.local` or a similar suffix. Most
portable, no elevation needed if using a high port, but requires the
user to configure their DNS to delegate the suffix to tela's
resolver.

**Recommendation:** Option C (built-in DNS resolver). No elevation
required, no hosts-file fragility, portable across platforms. tela
runs a small DNS server that answers queries for a dedicated suffix
(e.g. `*.tela.local`). The user configures their OS to delegate that
suffix to tela's resolver (a one-time setup step). Options A and B
are fallbacks for environments where running a local DNS listener is
not feasible.

### 4. Cleanup

On disconnect, tela removes its hosts-file entries (or deregisters
DNS names). On crash, stale entries persist until the next connect
(which overwrites them) or until the user manually cleans up. A
marker comment in the hosts file (e.g. `# tela-managed`) makes
cleanup greppable.

## Considerations

### RDP NLA/CredSSP and SPN

RDP with Network Level Authentication derives the Service Principal
Name (SPN) from the target hostname. When connecting to
`work-laptop-rdp`, the SPN is
`TERMSRV/work-laptop-rdp`. This must match the
remote machine's identity. If the remote machine's actual hostname is
`WORK-LAPTOP`, the SPN mismatch causes CredSSP to fail unless:

- The user saves credentials under the tunneled hostname, or
- The remote machine is configured to accept the alternative SPN, or
- NLA is configured to skip SPN verification for loopback addresses.

This is the same challenge Tailscale faces with MagicDNS hostnames.
Their solution: users save RDP credentials under the Tailscale
hostname. The same approach works here.

### Port conflicts

If the native port is already in use on the allocated loopback IP
(unlikely for `127.0.x.y` addresses that nothing else binds to, but
possible), tela should fall back to a different port and log a
warning. The hostname still resolves; the user just gets a non-default
port in that edge case.

### IPv6

The `127.0.0.0/8` approach is IPv4-only. On IPv6, the loopback
address is `::1` (a single address, no range). If IPv6-only loopback
is needed, the approach shifts to unique local addresses
(`fd00::/8`) or the DNS stub resolver (Option C), which can return
AAAA records pointing at any address.

For v1, IPv4 loopback is sufficient. Tela's existing WireGuard
tunnels use IPv4 (`10.77.0.0/16`).

### Profile integration

The allocated loopback IP and hostname should be visible in the
profile YAML and in TelaVisor's Status tab. The Status tab already
shows `localhost:{port}` for each service; it would show
`work-laptop-rdp` (clickable for HTTP services) instead.

### Interaction with the mount (WebDAV)

The tela mount feature exposes file shares as a WebDAV mount point.
If service routing allocates a loopback IP per machine, the mount
could bind its WebDAV listener to that IP on port 80 or 443,
enabling `\\work-laptop-files\share` UNC paths on Windows. This is
a natural extension but not part of the initial implementation.

## Non-goals for v1 of this feature

- **Automatic certificate generation.** HTTPS services behind the
  tunnel would need TLS certificates for the local hostname. This is
  a separate concern (local CA or mkcert integration) and not
  required for RDP, SSH, or plain HTTP.

- **Split DNS for organizational domains.** Resolving
  `prod-db.example.com` to a tunneled loopback address requires
  intercepting DNS queries for `example.com`. This is a DNS proxy
  feature, not a service routing feature. Out of scope.

- **WireGuard TUN integration.** Tela deliberately avoids TUN devices
  (no admin required). Service routing via loopback addresses
  preserves this property. A future TUN mode could assign real
  virtual IPs from the `10.77.0.0/16` range, but that is a different
  feature with different tradeoffs.

# Local name resolution for tunneled services

## Status

Proposed for 1.0. Not yet implemented.

## The problem

When a user connects to a machine through Tela, services are bound to
`localhost` on an arbitrary port:

```
localhost:10022  -> dev-vm SSH
localhost:15432  -> dev-vm PostgreSQL
localhost:20022  -> staging-db SSH
localhost:25432  -> staging-db PostgreSQL
```

This works, but the ergonomics are poor:

- `ssh -p 10022 paul@localhost` instead of `ssh paul@dev-vm`.
- Database tools need per-environment connection strings with non-standard
  ports.
- Ports are arbitrary and change if another profile already claimed that
  number.
- Users must remember or look up the port mapping for every service on
  every machine.

The goal is to let users use machine names and standard service ports:

```
ssh paul@dev-vm            # port 22, the default
psql -h staging-db         # port 5432, the default
```

## Design

The feature has two layers. Either layer is useful on its own; together
they eliminate all manual configuration.

### Layer 1: Virtual loopback addresses

Each connected machine gets a unique loopback IP address from the
`127.88.0.0/16` range. The address is deterministic: derived from a hash
of the hub URL and machine name, so the same machine always gets the same
address across sessions and across profiles.

```
127.88.1.1   -> dev-vm
127.88.1.2   -> staging-db
127.88.2.1   -> prod-api
```

Services on that machine bind to their real remote ports on that address:

```
127.88.1.1:22    -> dev-vm SSH
127.88.1.1:5432  -> dev-vm PostgreSQL
127.88.1.2:22    -> staging-db SSH
127.88.1.2:5432  -> staging-db PostgreSQL
```

Port conflicts disappear. Two machines that both expose SSH on port 22
get different loopback IPs, so both bind to `:22` without conflict.

#### Stable upstreams for reverse proxies

Because the address is derived from a hash, it is the same every time
the profile connects. This makes loopback addresses directly usable as
upstream targets in nginx, Caddy, HAProxy, Traefik, or any other
reverse proxy running on the same machine:

```nginx
upstream dev_api {
    server 127.88.1.1:8080;    # always dev-vm's HTTP service
}
upstream staging_api {
    server 127.88.1.2:8080;    # always staging-db's HTTP service
}
```

With the current port-remapped model (`127.0.0.1:18080`), the port can
change if another profile claims that number first, breaking the proxy
config. With loopback addresses, the upstream target is deterministic
and never changes unless the hub URL or machine name changes.

When Layer 2 (DNS) is active, proxy configs can use names instead:

```nginx
upstream dev_api {
    server dev-vm.tela:8080;
}
```

No Tela-specific proxy integration is required. Any software that can
connect to a TCP address on the loopback interface works with Layer 1.
Any software that can resolve DNS works with Layer 2.

#### Address assignment

The address is computed as:

```
hash = SHA-256(hub_url + "/" + machine_name)
octet3 = (hash[0] as uint16 << 8 | hash[1] as uint16) % 255 + 1  // 1-255
octet4 = (hash[2] as uint16 << 8 | hash[3] as uint16) % 255 + 1  // 1-255
address = 127.88.{octet3}.{octet4}
```

This gives 65,025 unique addresses (255 * 255), which is far more than
the 254-session limit per machine. Collisions are theoretically possible
but vanishingly unlikely for any realistic fleet size. If a collision does
occur, `tela` detects it at bind time and falls back to the existing
arbitrary-port-on-127.0.0.1 behavior for the colliding machine, with a
warning in the log.

#### Clash avoidance and configurable range

The entire `127.0.0.0/8` block is loopback (RFC 1122). In practice,
almost nothing uses any address other than `127.0.0.1`. The `127.88`
prefix was chosen because no known software claims a systematic range
inside the loopback block. Docker, Kubernetes, WSL2, Tailscale, Nebula,
ZeroTier, and common local development tools all use non-loopback
private ranges (`10.x`, `172.x`, `100.64.x`, `169.254.x`).

The realistic clash scenario is another tool that uses the same
virtual-loopback trick, or a user who has manually aliased addresses in
the `127.88.x.x` range for their own purposes.

If a clash occurs, the fallback is layered. The design prefers staying
in the loopback-address model as long as possible. Dropping to
port-remapped `127.0.0.1` is a last resort, not a first response:

1. **Global prefix override.** The address prefix is configurable so
   users can move the entire range away from `127.88`. This is the
   first thing to try when a clash is discovered:
2. **Per-machine override.** The profile YAML `address` field lets the
   user pin a specific machine to a different loopback address (see
   "Integration with profiles" below). Useful when only one or two
   machines collide and moving the whole range is overkill.
3. **Bind-time detection (last resort).** Before binding a service,
   `tela` attempts a non-blocking connect to the target address and
   port. If something is already listening and no override is
   configured, `tela` falls back to the old behavior (arbitrary port
   on `127.0.0.1`) for that specific service and logs a warning with
   the suggestion to configure a different prefix or per-machine
   address. Other services on the same machine that did not clash
   still use their assigned loopback address.

```yaml
dns:
  loopback_prefix: "127.77"    # default: "127.88"
```

The prefix must be `127.{1-254}`. The two remaining octets are still
derived from the hash. Changing the prefix changes every assigned
address, so users who have written SSH config or hosts file entries
against the old range need to update them. `tela dns status` prints
the active prefix.

The same setting is exposed in TelaVisor under Application Settings
(Local DNS section) and via the CLI:

```
tela dns prefix             # show current prefix
tela dns prefix 127.88      # set new prefix
```

The `127.0.0.0/8` range is entirely loopback on all three platforms.
Binding to `127.88.x.x` does not require creating a new interface or
adding a route. On Linux and macOS, any address in `127.0.0.0/8` is
reachable immediately. On Windows, only `127.0.0.1` is reachable by
default; additional loopback addresses require adding them with
`netsh interface ip add address "Loopback" 127.88.x.x 255.255.255.255`
or equivalent. This requires elevation.

#### Platform behavior

| Platform | Loopback behavior | Elevation needed |
|----------|-------------------|-----------------|
| Linux    | Any `127.x.x.x` works out of the box | No |
| macOS    | Any `127.x.x.x` works out of the box | No |
| Windows  | Only `127.0.0.1` by default; must add aliases | Yes (one-time setup) |

On Windows, `tela` can either:

1. Prompt for elevation once to add the loopback aliases, then run
   unprivileged afterward. The aliases persist across reboots.
2. Fall back to the current port-on-127.0.0.1 model if the user declines
   elevation, with a message explaining the tradeoff.

When `tela` is installed as a service (which already runs elevated), it
can manage loopback aliases without prompting.

#### Cleanup

When a profile disconnects, `tela` removes the loopback aliases it
created (Windows only; Linux and macOS need no cleanup). If `tela` is
killed without cleanup, stale aliases are harmless: they point at
addresses nothing is listening on. The next `tela connect` re-adds them.

### Layer 2: Local DNS resolver

A small DNS resolver runs inside the `tela` process, listening on
`127.0.0.1:15353` (a non-privileged port above 1024). It answers DNS
queries for connected machine names by returning the virtual loopback
address from Layer 1. All other queries are forwarded to the system's
upstream DNS resolver.

```
dig @127.0.0.1 -p 15353 dev-vm
;; ANSWER SECTION:
dev-vm.    0    IN    A    127.88.1.1
```

The resolver is opt-in. It is not useful until the system is configured
to use it, and configuring the system resolver is an intrusive change
that users should make deliberately.

#### Configuring the system to use Tela's resolver

Three approaches, in order of increasing integration:

**Option A: Per-application configuration (no system changes)**

Tools that support custom DNS or hosts-file-like config can be pointed
at Tela directly. For SSH, this is `~/.ssh/config` with `ProxyCommand`
or `Match exec` directives. This is what most users should start with.

**Option B: Stub zone in the system resolver**

Configure the system resolver to forward a specific domain suffix
(for example, `.tela` or `.tela.local`) to `127.0.0.1:15353`. Machine
names become `dev-vm.tela`. This is a one-time system configuration
change:

| Platform | Method |
|----------|--------|
| Linux (systemd-resolved) | `resolvectl domain tela` pointing at `127.0.0.1:15353` |
| Linux (NetworkManager) | dnsmasq plugin with `server=/tela/127.0.0.1#15353` |
| macOS | `/etc/resolver/tela` file containing `nameserver 127.0.0.1` and `port 15353` |
| Windows | NRPT rule via PowerShell: `Add-DnsClientNrptRule -Namespace ".tela" -NameServers "127.0.0.1:15353"` |

After this, `ssh paul@dev-vm.tela` resolves automatically. The `.tela`
suffix is configurable in TelaVisor settings and in the profile YAML.

**Option C: Primary resolver (maximum integration)**

Make `tela` the primary DNS resolver on `127.0.0.1:53`. This requires
elevation and replaces the system's resolver entirely (Tela forwards
non-Tela queries upstream). This is the most transparent option
(`ssh paul@dev-vm` works with no suffix) but the most intrusive. It is
appropriate for dedicated Tela workstations or kiosks, not for general
use.

This design document recommends Option B as the default and documents
Option A and C as alternatives. Option C is not recommended for most
users and may not be implemented in 1.0.

### Integration with profiles

The profile YAML gets a new top-level field:

```yaml
dns:
  enabled: true              # default: true when Layer 2 is available
  suffix: tela               # default: "tela"; used for stub-zone resolution
  port: 15353                # default: 15353
  loopback_prefix: "127.88"  # default: "127.88"; must be 127.{1-254}
```

The `tela` process starts the DNS resolver when a profile with
`dns.enabled: true` connects. The resolver shuts down on disconnect.

Machine-level address overrides are allowed in the profile for cases
where the deterministic hash collides or the user wants a specific
address:

```yaml
connections:
  - hub: wss://work.example.com
    machine: dev-vm
    address: 127.88.10.1    # override the hash-assigned address
```

### Integration with TelaVisor

TelaVisor exposes the feature in two places:

1. **Status tab.** Each service line shows the resolved address
   (`127.88.1.1:22`) instead of or in addition to the `localhost:NNNNN`
   binding. If DNS is configured, the name (`dev-vm.tela`) is shown
   as well.

2. **Application Settings.** A "Local DNS" section with:
   - Enable/disable toggle
   - Port field (default 15353)
   - Domain suffix field (default `tela`)
   - A "Configure system resolver" button that runs the appropriate
     platform command (with elevation prompt on Windows)

### Integration with the tela CLI

New subcommand: `tela dns`.

```
tela dns status           # show resolver state, port, suffix
tela dns configure        # one-time system resolver setup (interactive)
tela dns unconfigure      # reverse the system resolver setup
```

`tela status` output adds the resolved address and DNS name for each
service when the resolver is running:

```
  SSH      dev-vm.tela:22  (127.88.1.1:22)   Listening
  postgres dev-vm.tela:5432 (127.88.1.1:5432) Listening
```

## What changes in the codebase

| Area | Change | Status |
|------|--------|--------|
| `internal/client/` | Loopback address computation, `bindLoopbackListener`, profile YAML `dns` and `address` fields | Done |
| `internal/client/` | Windows loopback alias management (`loopback_windows.go`, `loopback_unix.go`) | Done |
| `internal/client/control.go` | `BoundService.BindAddr` and `service_bound` event includes `bindAddr` | Done |
| `cmd/telagui/app.go` | `LoopbackAddr` Wails binding for frontend address computation | Done |
| `cmd/telagui/frontend/` | Status tab, Profiles tab, YAML preview use addresses instead of remapped ports | Done |
| Awan Saya profile builder | `loopbackAddr()` JS, address display, YAML without `local:` lines, port-clash logic removed | Done |
| `cmd/telad/` | No changes. The agent is unaware of client-side name resolution | N/A |
| `cmd/telahubd/` | No changes. The hub is unaware of client-side name resolution | N/A |

The feature is entirely client-side. No protocol changes, no hub
changes, no agent changes.

## Rollout

### Phase 1: Virtual loopback addresses (Layer 1)

- Deterministic address assignment from `127.88.0.0/16`
- Services bind to real ports on assigned addresses
- Windows loopback alias management
- `tela status` shows addresses
- TelaVisor Status tab shows addresses
- Profile YAML `address` override

This phase alone eliminates the port-conflict problem and lets users
write one-time SSH config or hosts file entries that never change.

### Phase 2: Local DNS resolver (Layer 2)

- Embedded DNS resolver on `127.0.0.1:15353`
- Forward non-Tela queries to upstream
- `tela dns` subcommand for system resolver configuration
- TelaVisor Application Settings DNS section
- Profile YAML `dns` block

This phase eliminates manual configuration entirely for users who
run the one-time system resolver setup.

## Open questions

1. **Domain suffix.** Is `.tela` the right default, or should it be
   `.tela.local` to avoid any future collision with a real TLD? The
   `.local` suffix is reserved for mDNS by RFC 6762, which could cause
   conflicts on networks with Bonjour/Avahi. A plain `.tela` suffix
   is not a registered TLD today.

2. **Multi-profile resolution.** If two profiles connect simultaneously
   and both include a machine called `dev-vm` on different hubs, the
   DNS resolver sees a name collision. Options: first-connected wins,
   hub-qualified names (`dev-vm.workhub.tela`), or reject the second
   profile with an error.

3. **Existing port-forwarding mode.** Should the old `localhost:NNNNN`
   bindings continue alongside the new loopback addresses (for backward
   compatibility during 0.x), or should the new mode replace them
   entirely? The existing mode is still useful for tools that do not
   support custom DNS (rare but possible).

4. **Windows Loopback Adapter.** An alternative to `netsh` alias
   management on Windows is installing a Loopback Adapter via
   `devcon.exe` or `pnputil`. This survives reboots more reliably but
   requires a device driver. Evaluate whether the `netsh` approach is
   sufficient.

# How to: Self-update and release channels

This walkthrough covers Tela's release channel system end to end: how to inspect
which channel a binary is on, how to switch channels, how to update binaries,
and how to bootstrap a fresh box. It assumes you already have at least one
Tela binary installed somewhere on your `PATH`.

For the design model behind channels (what they are, how promotion works, when
to cut a beta or a stable), see [RELEASE-PROCESS.md](../RELEASE-PROCESS.md).

## The mental model in one paragraph

Tela ships through three channels. **dev** updates on every commit to main.
**beta** is a soaked dev build that a maintainer chose to graduate. **stable**
is a soaked beta build, the conservative line. Each channel is described by a
JSON manifest hosted on GitHub Releases that names the current tag and lists
every binary published under that tag with its SHA-256. Every Tela binary --
the `tela` client, `telad` agent, `telahubd` hub, and TelaVisor desktop app --
follows whichever channel it's configured for, fetches the matching manifest,
and verifies SHA-256 against the manifest entry before installing an update.
You can switch a binary's channel at any time, and the channel is per-binary,
not global -- you can run a `dev` agent against a `stable` hub.

## Inspecting channels

### From the command line

```bash
tela channel
```

prints the current client's channel, the manifest URL, the running version, and
the latest version on that channel:

```text
  channel:         dev
  manifest:        https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
  current version: v0.6.0-dev.7
  latest version:  v0.6.0-dev.8  (update available)
```

To inspect a channel without switching to it:

```bash
tela channel show -channel beta
```

That prints the parsed channel manifest: every binary on that channel with its
size and SHA-256.

### For a remote hub

```bash
tela admin hub channel -hub <hub-name>
```

prints the same shape but for the hub at `<hub-name>` instead of the local
client. Requires an owner or admin token on the hub.

### For a remote agent

```bash
tela admin agent channel -hub <hub-name> -machine <machine-id>
```

The hub forwards the request to the named agent and returns its channel and
version state.

### From TelaVisor

The same information appears in three places, as `Release channel` rows
in:

- **Hub Settings → Management** (per-hub)
- **Agent Settings → Management** (per-agent)
- **Application Settings → Updates** (TelaVisor's own preference)

The dropdowns are channel selectors and the trailing status text shows the
current/latest versions, exactly like the CLI output.

### From Awan Saya

The portal also has channel rows in the Hub and Agent management cards. Same
shape, gated on having the manage permission on the hub or agent.

## Switching channels

### The client (and TelaVisor)

```bash
tela channel set beta
```

writes the preference to the user credential store
(`~/.tela/credentials.yaml` on Unix, `%APPDATA%\tela\credentials.yaml` on
Windows). Both the `tela` CLI and TelaVisor read from this file, so the next
time either runs `update` it follows the new channel.

You can also change it from TelaVisor's Application Settings → Updates →
Release channel dropdown.

### A hub

```bash
tela admin hub channel set beta -hub <hub-name>
```

PATCHes `/api/admin/update` on the hub. The hub persists `update.channel` to
its YAML config. The change takes effect on the next self-update; the
currently running binary is not affected.

You can also change it from TelaVisor's Hub Settings → Management → Release
channel dropdown, or from the equivalent dropdown in Awan Saya's hub
management card.

### An agent

```bash
tela admin agent channel -hub <hub-name> -machine <machine-id> set beta
```

The hub forwards the `update-channel` mgmt action to the agent, which persists
`update.channel` to its `telad.yaml`. Same UX in TelaVisor's Agent Settings.

## Updating

Three ways to update, all read from the same channel manifest. Pick whichever
fits the box.

### Self-update via the binary's own CLI

```bash
tela update                           # update the running tela client
telad update                          # update the on-disk telad binary
telahubd update                       # update the on-disk telahubd binary
```

All three accept `-channel <name>` (one-shot override) and `-dry-run` (show
what would happen without modifying the binary). For `telad` and `telahubd`,
the `-config <path>` flag selects which YAML config file's channel to honor.

The download is verified against the channel manifest's SHA-256 before being
written. On Windows the running `.exe` is renamed to `.exe.old` before the
new binary is moved into place; the `.old` file is removed in the background.
On Unix the rename is atomic.

For `telad` and `telahubd` running as managed OS services, the binary is
swapped in place but the running process is not killed. Restart the service
manually for the new binary to take effect:

```bash
sudo systemctl restart telad           # systemd
sudo launchctl kickstart -k system/com.tela.telad   # launchd
sc stop telad && sc start telad        # Windows SCM
```

### Self-update via the admin API

```bash
tela admin hub update -hub <hub-name>
tela admin agent update -hub <hub-name> -machine <machine-id>
```

The hub or agent downloads the new binary from its configured channel,
verifies it, and restarts. For agents the restart goes through whatever
process supervision they're under (Docker, Windows SCM, systemd, launchd, or
none). For hubs the same applies.

### Self-update from TelaVisor

The Software row in each Management card has an `Update to vX.Y.Z` button
when the binary is behind. Clicking it triggers the same admin-API path as
above and polls the binary's reported version until it changes, so the
table reflects the actual installed version.

For locally installed services, the Installed Tools card on Client Settings
has Update buttons that delegate to the elevated service process (TelaVisor
itself does not need to be elevated to update an elevated service binary --
the running service updates itself from the inside, then the process supervisor
restarts it against the new binary).

## Bootstrapping a fresh box

The first time you put Tela on a machine, you don't have a `tela`/`telad`/
`telahubd` binary yet, so you can't use any of the self-update commands. You
need to download one binary by hand, then let it self-update from the channel
manifest forever after.

### One-liner from a Linux shell

```bash
curl -fsSL https://github.com/paulmooreparks/tela/releases/download/channels/dev.json \
  | python3 -c 'import json,sys; m=json.load(sys.stdin); print(m["downloadBase"]+"telad-linux-amd64")' \
  | xargs curl -fLO
chmod +x telad-linux-amd64
sudo mv telad-linux-amd64 /usr/local/bin/telad
```

Replace `dev.json` with `beta.json` or `stable.json` to bootstrap from a
different channel. Replace `telad-linux-amd64` with whichever binary you want
(`tela-linux-arm64`, `telahubd-darwin-amd64`, etc).

### One-liner from PowerShell

```powershell
$m = Invoke-RestMethod https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
Invoke-WebRequest ($m.downloadBase + 'tela-windows-amd64.exe') -OutFile C:\Users\paul\bin\tela.exe
```

### From an existing tela on a different box

If you already have one machine with `tela` installed, the easiest way to put
a binary on a *new* machine is to download it from the existing one and copy
it over:

```bash
tela channel download telad-linux-amd64 -o telad
scp telad newhost:/tmp/telad
ssh newhost 'sudo mv /tmp/telad /usr/local/bin/telad && sudo chmod +x /usr/local/bin/telad'
```

After the bridge, every subsequent update on the new box is just `telad update`.

## Verifying a download by hand

Every download Tela does internally is SHA-256-verified against the channel
manifest, but if you want to verify a download yourself (because you fetched
it with `wget` or out of habit), every release also publishes a `SHA256SUMS.txt`
asset alongside the binaries:

```bash
curl -fLO https://github.com/paulmooreparks/tela/releases/download/v0.6.0-dev.8/SHA256SUMS.txt
curl -fLO https://github.com/paulmooreparks/tela/releases/download/v0.6.0-dev.8/telad-linux-amd64
sha256sum -c SHA256SUMS.txt --ignore-missing
```

## What happens during an update, in detail

For an interactive `tela update`:

1. Read the configured channel from the user credential store.
2. Fetch the channel manifest (5-minute in-process cache).
3. Look up the entry for `tela-{goos}-{goarch}{ext}` in the manifest.
4. Compare current version against `manifest.version`. If equal and the
   running binary is not a `dev` build, exit "already up to date."
5. Download the binary from `manifest.downloadBase + binary-name`.
6. Stream the body through `channel.VerifyReader`, which writes to a sibling
   tmp file in the destination directory while computing a SHA-256 hash and
   counting bytes. If the hash or size does not match the manifest entry,
   delete the tmp file and exit non-zero.
7. On Unix, rename tmp → destination atomically. On Windows, rename current
   binary to `.old`, rename tmp → destination, then remove `.old` in the
   background.
8. Print `OK: tela updated to vX.Y.Z`.

The same steps happen for `telad update` and `telahubd update`, and for the
admin-API-driven updates with the difference that the new binary is staged
and the running process exits, leaving the OS service manager (or the user)
to relaunch.

## When things go wrong

### "fetch dev manifest: HTTP 404"

The channel manifest URL did not return a manifest. Either the manifest base
URL is wrong (you set a `manifestBase` override that points nowhere), or
GitHub is having a bad day. Check the URL printed by `tela channel`.

### "verify download: sha256 mismatch"

The downloaded binary did not match the manifest entry. This is the safety
net working: a corrupted download or a manifest/asset mismatch will fail here
rather than installing a bad binary. The tmp file is removed automatically.
Try again. If it persists, the manifest itself may be stale -- run
`tela channel show` to inspect.

### "requested version vX.Y.Z is not the current vA.B.C on channel <ch>"

You asked for a specific version that is not the channel's current HEAD.
Channels are always-current pointers, not version pins. To get an older or
newer version, switch channels (or set a custom `manifestBase`). Pre-1.0
there is no other way to pin.

### TelaVisor's Update button shows "pre-channel build"

The hub or agent is running a binary from before the channel system was
added. Update it via the legacy path first (run `telahubd update` or
`telad update` from a shell on the box, or use the bootstrap one-liner
above), and the channel-aware UI will start working on the next page load.

## Related

- [RELEASE-PROCESS.md](../RELEASE-PROCESS.md) -- the channel model and how
  promotions work
- [REFERENCE.md](../REFERENCE.md) -- full CLI reference for `tela channel`,
  `tela update`, `telad update`, `telahubd update`, `tela admin hub channel`,
  `tela admin agent channel`
- [CONFIGURATION.md](../CONFIGURATION.md) -- the `update.channel` and
  `update.manifestBase` fields in `telad.yaml`, `telahubd.yaml`, and
  `credentials.yaml`

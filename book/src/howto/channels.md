# Self-Update and Release Channels

Once you have Tela binaries deployed across more than one machine, you face
a maintenance question: how do you keep them up to date without logging
into every machine and running a download by hand?

Tela's answer is self-update through a release channel system. Each binary
(`tela`, `telad`, `telahubd`, TelaVisor) knows which channel it is
following (`dev`, `beta`, or `stable`), fetches the channel's JSON manifest
from GitHub Releases, and updates itself in place. The update is verified
against the manifest's SHA-256 before anything is written to disk. Agents
and hubs can be updated remotely through the hub's management protocol,
without SSH access to the machine.

This chapter covers how to check what channel any binary is on, switch a
binary to a different channel, trigger an update from the command line, the
admin API, or TelaVisor, and bootstrap a fresh machine that does not yet
have any Tela binary installed.

The commands below assume at least one Tela binary is already installed and
on your `PATH`. To get the first binary onto a machine, see
[Bootstrapping a Fresh Box](#bootstrapping-a-fresh-box) below.

For the design model behind channels (what they are, how promotion works,
when to cut a beta or a stable), see the
[Release Process](../ops/release-process.md) chapter in the Operations
section.

## The Mental Model in One Paragraph

Tela ships through three channels. **dev** updates on every commit to main.
**beta** is a dev build that a maintainer judged ready for promotion.
**stable** is a beta build promoted to the conservative line. Each channel
is described by a JSON manifest hosted on GitHub Releases that names the
current tag and lists every binary published under that tag with its
SHA-256. Every Tela binary follows whichever channel it is configured for,
fetches the matching manifest, and verifies the SHA-256 against the
manifest entry before installing an update. You can switch a binary's
channel at any time, and the channel is per-binary, not global: you can run
a `dev` agent against a `stable` hub.

## Inspecting Channels

### From the Command Line

```bash
tela channel
```

prints the current client's channel, the manifest URL, the running version,
and the latest version on that channel:

```text
  channel:         dev
  manifest:        https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
  current version: v0.16.0-dev.14
  latest version:  v0.16.0-dev.15  (update available)
```

To inspect a channel without switching to it:

```bash
tela channel show -channel beta
```

That prints the parsed channel manifest: every binary on that channel with
its size and SHA-256.

### For a Remote Hub

```bash
tela admin hub channel -hub <hub-name>
```

prints the same shape but for the hub at `<hub-name>` instead of the local
client. Requires an owner or admin token on the hub.

### For a Remote Agent

```bash
tela admin agent channel -hub <hub-name> -machine <machine-id>
```

The hub forwards the request to the named agent and returns its channel and
version state.

### From TelaVisor

The same information appears in three places:

- The **Updates tab** (Clients mode) shows the channel that TelaVisor and
  the `tela` CLI follow on this machine, in the Release Channel card.
- The **Hubs tab** (Infrastructure mode) shows each hub's channel in the
  Management section of Hub Settings.
- The **Agents tab** (Infrastructure mode) shows each agent's channel in
  the Management card of the agent detail panel.

The dropdowns are channel selectors, and the trailing status text shows the
current and latest versions, exactly like the CLI output.

### From a Portal

Portals such as Awan Saya show the same channel rows in their hub and agent
management cards, gated on having the manage permission on the hub or
agent.

## Switching Channels

### The Client (and TelaVisor)

```bash
tela channel set beta
```

writes the preference to the user credential store
(`~/.tela/credentials.yaml` on Unix, `%APPDATA%\tela\credentials.yaml` on
Windows). Both the `tela` CLI and TelaVisor read from this file, so the
next time either runs `update` it follows the new channel.

You can also change it from the Release Channel dropdown on TelaVisor's
Updates tab.

### A Hub

From any workstation with an owner or admin token:

```bash
tela admin hub channel set beta -hub <hub-name>
```

This sends `PATCH /api/admin/update` to the hub. The hub persists
`update.channel` to its YAML config. The change takes effect on the next
self-update; the currently running binary is not affected.

Directly on the hub machine, you can do the same without an admin token:

```bash
sudo telahubd channel set beta
```

This writes `update.channel` in the hub's YAML config (the
platform-standard path is the default, so you rarely need `-config`).
Restart the hub service for background update checks to pick up the new
channel. Run `telahubd channel -h` for the full subcommand list, including
`telahubd channel show`, which prints the full parsed manifest.

You can also change it from the Release Channel dropdown in TelaVisor's
Hub Settings, or from the equivalent dropdown in a portal's hub management
card.

### An Agent

From any workstation with permissions:

```bash
tela admin agent channel -hub <hub-name> -machine <machine-id> set beta
```

The hub forwards the `update-channel` management action to the agent, which
persists `update.channel` to its `telad.yaml`. The same control is in the
Management card of TelaVisor's agent detail panel.

Directly on the agent machine:

```bash
sudo telad channel set beta -config /etc/tela/telad.yaml
```

Or set `TELAD_CONFIG` in the environment and drop the flag. Run
`telad channel -h` for the full subcommand list, including
`telad channel show`, which prints the full parsed manifest.

## Updating

Three ways to update, all reading from the same channel manifest. Pick
whichever fits the box.

### Self-Update via the Binary's Own CLI

```bash
tela update                           # update the running tela client
telad update                          # update the on-disk telad binary
telahubd update                       # update the on-disk telahubd binary
```

All three accept `-channel <name>` (one-shot override, accepts any valid
channel name including custom ones), `-dry-run` (show what would happen
without modifying the binary), and `-h` / `-?` / `-help` / `--help` (print
usage). For `telad` and `telahubd`, the `-config <path>` flag selects which
YAML config file's channel to honor.

The download is verified against the channel manifest's SHA-256 before
being written. On Windows the running `.exe` is renamed to `.exe.old`
before the new binary is moved into place; the `.old` file is removed in
the background. On Unix the rename is atomic.

For `telad` and `telahubd` running as managed OS services, the binary is
swapped in place but the running process is not killed. Restart the service
manually for the new binary to take effect:

```bash
sudo systemctl restart telad           # systemd
sudo launchctl kickstart -k system/com.tela.telad   # launchd
sc stop telad && sc start telad        # Windows SCM
```

### Self-Update via the Admin API

```bash
tela admin hub update -hub <hub-name>
tela admin agent update -hub <hub-name> -machine <machine-id>
```

The hub or agent downloads the new binary from its configured channel,
verifies it, and restarts. The restart goes through whatever process
supervision the binary is under (Docker, Windows SCM, systemd, launchd, or
none).

### Self-Update from TelaVisor

The Software row in each Management card (Hub Settings for hubs, the agent
detail panel for agents) has an `Update to vX.Y.Z` button when the binary
is behind. Clicking it triggers the same admin-API path as above and polls
the binary's reported version until it changes, so the display reflects the
actual installed version.

For locally installed binaries and services, the Installed Tools table on
the Updates tab has Update buttons. TelaVisor itself does not need to be
elevated to update an elevated service binary; the running service updates
itself from the inside, then the process supervisor restarts it against the
new binary.

## Bootstrapping a Fresh Box

The first time you put Tela on a machine, you do not have a binary yet, so
you cannot use any of the self-update commands. Download one binary by
hand, then let it self-update from the channel manifest forever after.

### One-Liner from a Linux Shell

```bash
curl -fsSL https://github.com/paulmooreparks/tela/releases/download/channels/dev.json \
  | python3 -c 'import json,sys; m=json.load(sys.stdin); print(m["downloadBase"]+"telad-linux-amd64")' \
  | xargs curl -fLO
chmod +x telad-linux-amd64
sudo mv telad-linux-amd64 /usr/local/bin/telad
```

Replace `dev.json` with `beta.json` or `stable.json` to bootstrap from a
different channel. Replace `telad-linux-amd64` with whichever binary you
want (`tela-linux-arm64`, `telahubd-darwin-amd64`, and so on).

### One-Liner from PowerShell

```powershell
$m = Invoke-RestMethod https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
Invoke-WebRequest ($m.downloadBase + 'tela-windows-amd64.exe') -OutFile tela.exe
```

### From an Existing tela on a Different Box

If you already have one machine with `tela` installed, the easiest way to
put a binary on a new machine is to download it from the existing one and
copy it over:

```bash
tela channel download telad-linux-amd64 -o telad
scp telad newhost:/tmp/telad
ssh newhost 'sudo mv /tmp/telad /usr/local/bin/telad && sudo chmod +x /usr/local/bin/telad'
```

After the transfer, every subsequent update on the new box is just
`telad update`.

## Verifying a Download by Hand

Every download Tela performs internally is SHA-256-verified against the
channel manifest, but if you want to verify a download yourself (because
you fetched it with `wget` or out of habit), every release also publishes a
`SHA256SUMS.txt` asset alongside the binaries:

```bash
curl -fLO https://github.com/paulmooreparks/tela/releases/download/v0.16.0-dev.15/SHA256SUMS.txt
curl -fLO https://github.com/paulmooreparks/tela/releases/download/v0.16.0-dev.15/telad-linux-amd64
sha256sum -c SHA256SUMS.txt --ignore-missing
```

## What Happens During an Update, in Detail

For an interactive `tela update`:

1. Read the configured channel from the user credential store.
2. Fetch the channel manifest (5-minute in-process cache).
3. Look up the entry for `tela-{goos}-{goarch}{ext}` in the manifest.
4. Compare the current version against `manifest.version`. If equal and the
   running binary is not a `dev` build, exit "already up to date."
5. Download the binary from `manifest.downloadBase + binary-name`.
6. Stream the body through the verifier, which writes to a sibling tmp file
   in the destination directory while computing a SHA-256 hash and counting
   bytes. If the hash or size does not match the manifest entry, delete the
   tmp file and exit non-zero.
7. On Unix, rename tmp to destination atomically. On Windows, rename the
   current binary to `.old`, rename tmp to destination, then remove `.old`
   in the background.
8. Print `OK: tela updated to vX.Y.Z`.

The same steps happen for `telad update` and `telahubd update`. For
admin-API-driven updates the difference is that the new binary is staged
and the running process exits, leaving the OS service manager (or the user)
to relaunch it.

## When Things Go Wrong

### "fetch dev manifest: HTTP 404"

The channel manifest URL did not return a manifest. Either the manifest
base URL is wrong (you set a `sources[<channel>]` override that points
nowhere), or GitHub is having a bad day. Check the URL printed by
`tela channel`.

### "verify download: sha256 mismatch"

The downloaded binary did not match the manifest entry. This is the safety
net working: a corrupted download or a manifest/asset mismatch fails here
rather than installing a bad binary. The tmp file is removed automatically.
Try again. If it persists, the manifest itself may be stale; run
`tela channel show` to inspect it.

### "requested version vX.Y.Z is not the current vA.B.C on channel"

You asked for a specific version that is not the channel's current HEAD.
Channels are always-current pointers, not version pins. To get an older or
newer version, switch channels (or set a custom `sources[<channel>]` URL).
Pre-1.0 there is no other way to pin.

### TelaVisor's Update Button Shows "pre-channel build"

The hub or agent is running a binary from before the channel system was
added. Update it once from a shell on the box (`telahubd update` or
`telad update`, or the bootstrap one-liner above), and the channel-aware UI
will start working on the next page load.

## Related

- [Release Process](../ops/release-process.md): the channel model,
  promotion, and running a self-hosted channel server on telahubd
- [Appendix A: CLI Reference](../guide/reference.md): full CLI reference
  for `tela channel`, `tela update`, `telad update`, `telahubd update`,
  `telahubd channels publish`, `tela admin hub channel`, and
  `tela admin agent channel`
- [Appendix B: Configuration File Reference](../guide/configuration.md):
  the `update.channel`, `update.sources`, and `channels:` fields in
  `telad.yaml`, `telahubd.yaml`, and `credentials.yaml`

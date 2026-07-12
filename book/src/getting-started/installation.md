# Installation

Tela ships through three release channels (`dev`, `beta`, `stable`). Only
the first download is manual. Once any one binary is installed, every
subsequent update is one command:

```bash
tela update
telad update
telahubd update
```

Pick whichever channel you want to follow. The examples below use `stable`;
substitute `beta` or `dev` to follow a faster channel.

## Linux / macOS

Pull the latest binary from a channel manifest:

```bash
# Replace 'stable' with 'beta' or 'dev' as desired.
# Replace 'tela-linux-amd64' with the binary you want.
curl -fsSL https://github.com/paulmooreparks/tela/releases/download/channels/stable.json \
  | python3 -c 'import json,sys; m=json.load(sys.stdin); print(m["downloadBase"]+"tela-linux-amd64")' \
  | xargs curl -fLO

chmod +x tela-linux-amd64
sudo mv tela-linux-amd64 /usr/local/bin/tela
```

For `telad` and `telahubd`, repeat with the matching binary name.

## Windows

From PowerShell:

```powershell
$m = Invoke-RestMethod https://github.com/paulmooreparks/tela/releases/download/channels/stable.json
Invoke-WebRequest ($m.downloadBase + 'tela-windows-amd64.exe') -OutFile C:\Users\$env:USERNAME\bin\tela.exe
```

Make sure `C:\Users\<you>\bin` is on your `PATH`.

## TelaVisor (Desktop GUI)

For Windows, download the NSIS installer from any release page or directly
from the channel manifest:

```powershell
$m = Invoke-RestMethod https://github.com/paulmooreparks/tela/releases/download/channels/stable.json
Invoke-WebRequest ($m.downloadBase + 'telavisor-windows-amd64-setup.exe') -OutFile TelaVisor-Setup.exe
.\TelaVisor-Setup.exe
```

For Linux, the channel manifest also lists `.deb` and `.rpm` packages and a
bare binary. For macOS, a `.tar.gz` of the `.app` bundle.

## Channels

| Channel | What It Is | Tag Form |
|---------|------------|----------|
| `dev`   | Latest unstable build, every commit to main | `v0.16.0-dev.15` |
| `beta`  | Promoted dev build ready for wider exposure | `v0.16.0-beta.1` |
| `stable`| Promoted beta build, the conservative line | `v0.16.0`, `v0.15.0` |

The model is documented in [Release Process](../ops/release-process.md).

## Verifying Downloads

Every download Tela performs internally is SHA-256-verified against the
channel manifest before being installed. To verify a manual download by
hand, every release also publishes a `SHA256SUMS.txt` asset:

```bash
curl -fLO https://github.com/paulmooreparks/tela/releases/download/v0.16.0/SHA256SUMS.txt
sha256sum -c SHA256SUMS.txt --ignore-missing
```

## Next Steps After Downloading

Downloading the binary is the first step, not the last. What to do next
depends on which binary you installed:

| Binary | Next Step |
|--------|-----------|
| `telahubd` | Follow [Run a Hub on the Public Internet](../howto/hub.md). The walkthrough covers picking a deployment model (Caddy, nginx, Apache, Cloudflare Tunnel, or direct), installing the OS service, bootstrapping the owner token, and configuring the reverse proxy. |
| `telad` | Follow [Run an Agent](../howto/telad.md) to register a machine with a hub. |
| `tela` client | Follow [First Connection](./first-connection.md) to pair with a hub and open your first tunnel. |
| TelaVisor | Launch the app after install; it walks you through pairing on first run. |

## After Bootstrapping

Every Tela binary has an `update` subcommand that follows the configured
channel. Once you have one of them installed, you no longer need to think
about manual downloads:

```bash
tela update
sudo telad update
sudo telahubd update
```

To switch channels:

```bash
tela channel set beta              # client (and TelaVisor)
sudo telad channel set beta        # agent (writes to telad.yaml)
sudo telahubd channel set beta     # hub (writes to telahubd.yaml)
```

For a one-shot override that does not persist, pass `-channel <name>` to
the `update` subcommand: `sudo telahubd update -channel beta`. Any valid
channel name works (dev, beta, stable, or a custom channel you have
configured).

For the full picture see the
[Self-Update and Release Channels](../howto/channels.md) how-to.

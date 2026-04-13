# Installation

Tela ships through three release channels (`dev`, `beta`, `stable`). Once
any one binary is installed, every subsequent update is one command:

```bash
tela update
telad update
telahubd update
```

The bootstrap step is the only one that needs a manual download. Pick whichever
channel you want to follow.

## Linux / macOS

Pull the latest binary from a channel manifest:

```bash
# Replace 'dev' with 'beta' or 'stable' as desired.
# Replace 'tela-linux-amd64' with the binary you want.
curl -fsSL https://github.com/paulmooreparks/tela/releases/download/channels/dev.json \
  | python3 -c 'import json,sys; m=json.load(sys.stdin); print(m["downloadBase"]+"tela-linux-amd64")' \
  | xargs curl -fLO

chmod +x tela-linux-amd64
sudo mv tela-linux-amd64 /usr/local/bin/tela
```

For `telad` and `telahubd`, repeat with the matching binary name.

## Windows

From PowerShell:

```powershell
$m = Invoke-RestMethod https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
Invoke-WebRequest ($m.downloadBase + 'tela-windows-amd64.exe') -OutFile C:\Users\$env:USERNAME\bin\tela.exe
```

Make sure `C:\Users\<you>\bin` is on your `PATH`.

## TelaVisor (desktop GUI)

For Windows, download the NSIS installer from any release page or directly
from the channel manifest:

```powershell
$m = Invoke-RestMethod https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
Invoke-WebRequest ($m.downloadBase + 'telavisor-windows-amd64-setup.exe') -OutFile TelaVisor-Setup.exe
.\TelaVisor-Setup.exe
```

For Linux, the channel manifest also contains `.deb`, `.rpm`, and a bare
binary. For macOS, a `.tar.gz` of the `.app` bundle.

## Channels

| Channel | What it is | Tag form |
|---------|------------|----------|
| `dev`   | Latest unstable build, every commit to main | `v0.8.0-dev.42` |
| `beta`  | Promoted dev build that has soaked | `v0.8.0-beta.3` |
| `stable`| Promoted beta build, the conservative line | `v0.8.0`, `v0.6.1` |

The model is documented in [Release process](../ops/release-process.md).

## Verifying downloads

Every download Tela does internally is SHA-256-verified against the channel
manifest before being installed. If you want to verify a manual download by
hand, every release also publishes a `SHA256SUMS.txt` asset:

```bash
curl -fLO https://github.com/paulmooreparks/tela/releases/download/v0.8.0-dev.8/SHA256SUMS.txt
sha256sum -c SHA256SUMS.txt --ignore-missing
```

## After bootstrapping

Every Tela binary has a `update` subcommand that follows the configured
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
sudo telahubd update -channel beta # hub one-shot, doesn't persist
# or to persist, edit telahubd.yaml under update.channel
```

For the full picture see the [Self-update and release channels](../howto/channels.md)
how-to.

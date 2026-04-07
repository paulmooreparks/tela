# Channel manifests

This directory holds the schema reference for Tela's release channel manifests.

The actual live manifests are not stored in the repository. They are published as
release assets on a special `channels` GitHub Release at:

```
https://github.com/paulmooreparks/tela/releases/download/channels/dev.json
https://github.com/paulmooreparks/tela/releases/download/channels/beta.json
https://github.com/paulmooreparks/tela/releases/download/channels/stable.json
```

Each manifest is a single JSON document that names the current tag for that
channel and lists every binary published under that tag with its SHA-256 and
size. Self-update on every Tela binary fetches its channel manifest, compares
the current version to the manifest's `version`, and downloads the named
binaries if newer.

See [`schema.json`](schema.json) for the canonical shape and
[`dev.json.example`](dev.json.example) for a worked example.

The lifecycle of a manifest is described in [RELEASE-PROCESS.md](../RELEASE-PROCESS.md).

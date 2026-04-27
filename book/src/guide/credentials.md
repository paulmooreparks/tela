# Credentials and pairing

Tela stores hub tokens in a local credential file so you do not need to pass a `-token` flag on every command. This chapter explains how credentials are stored, how to add and remove them, and how one-time pairing codes let administrators onboard users and agents without distributing 64-character hex tokens by hand.

## The credential store

The credential store is a YAML file at:

- **Linux / macOS:** `~/.tela/credentials.yaml`
- **Windows:** `%APPDATA%\tela\credentials.yaml`

It is written with 0600 permissions (owner read/write only). It maps hub URLs to tokens. When `tela` or `telad` needs a token for a hub and none is provided on the command line, it looks here first.

`telad` running as an OS service uses a system-level credential store instead:

- **Linux:** `/etc/tela/credentials.yaml`
- **Windows:** `%ProgramData%\Tela\credentials.yaml`

Writing to the system store requires administrator or root privileges.

## Storing credentials

```bash
tela login wss://hub.example.com
# Token: (paste token, press Enter)
# Identity (press Enter to skip): alice
```

`tela login` prompts for a token and an optional identity label, then stores both in the user credential store. Once stored, any `tela` command targeting that hub finds the token automatically.

For `telad` running as a service:

```bash
sudo telad login -hub wss://hub.example.com
# Token: (paste token, press Enter)
```

`telad login` requires elevated privileges because it writes to the system credential store.

## Removing credentials

```bash
tela logout wss://hub.example.com
sudo telad logout -hub wss://hub.example.com
```

## Pinning a hub's TLS certificate

Tokens prove that the bearer is allowed to talk to a hub. They do not prove that the connection is *actually reaching that hub* and not a rogue proxy in the middle. Tela addresses that with TLS certificate pinning: a per-hub fingerprint stored alongside the token, checked on every `tela connect`, refused on mismatch.

The fingerprint is the SHA-256 hash of the hub's TLS leaf certificate's Subject Public Key Info (SPKI), formatted as `sha256:<lowercase hex>`. Pinning the SPKI rather than the whole certificate means certificate renewal with the same key does not break the pin. Key rotation does break the pin, which is the point: a key rotation is exactly the case where the operator should re-confirm the change.

### First connect (TOFU)

The first time `tela connect` succeeds against a hub with no pin configured, it logs the captured fingerprint and the exact command to enforce it next time:

```
hub wss://hub.example.com presented certificate sha256:1a2b...3456 (no pin configured;
run 'tela pin wss://hub.example.com sha256:1a2b...3456' to enforce it)
```

If you want the connection refused on any future certificate change, copy the fingerprint and pin it:

```bash
tela pin wss://hub.example.com sha256:1a2b...3456
```

### Inspect, change, or remove a pin

```bash
tela pin wss://hub.example.com                       # show the current pin
tela pin wss://hub.example.com sha256:newfingerprint # update (use after a deliberate key rotation)
tela pin wss://hub.example.com -clear                # remove (return to plain CA-chain trust)
```

### Pinning at credential-creation time

```bash
tela login wss://hub.example.com -pin sha256:1a2b...3456
```

Useful when the operator gives you both the token and the expected fingerprint up front (out-of-band, e.g. a chat message or a printed handout for an air-gapped onboarding).

### What a pin mismatch looks like

```
$ tela connect -hub wss://hub.example.com -machine prod-db
websocket dial failed: ... certpin: presented certificate does not match
pinned fingerprint: got sha256:9999..., want sha256:1a2b...
```

The connection is refused before any token, machine name, or other secret is sent. If this happens unexpectedly, do not update the pin until you have confirmed the change with the hub operator out-of-band.

## Pairing codes

A pairing code is a short, single-use code that a hub administrator generates for a user or agent. The recipient redeems it for a permanent token without ever seeing or handling the raw token value. Codes expire between 10 minutes and 7 days after generation.

### Generating a code (administrator)

```bash
# Generate a connect code for a user (grants connect access to machine barn)
tela admin pair-code barn -hub wss://hub.example.com -token <owner-token>

# Generate a connect code that expires in 24 hours
tela admin pair-code barn -hub wss://hub.example.com -token <owner-token> -expires 24h

# Generate a register code for a new agent
tela admin pair-code barn -hub wss://hub.example.com -token <owner-token> -type register

# Generate a code granting access to all machines
tela admin pair-code barn -hub wss://hub.example.com -token <owner-token> -machines '*'
```

The command prints the code and the corresponding redemption command to give to the recipient:

```
Generated pairing code: ABCD-1234
Expires: 2026-04-15T10:30:00Z

Client pairing command:
  tela pair -hub wss://hub.example.com -code ABCD-1234
```

Codes can also be generated from TelaVisor's Access tab, using the *Pair Code...* button in the toolbar, for administrators who prefer a graphical interface.

### Redeeming a code (user)

```bash
tela pair -hub wss://hub.example.com -code ABCD-1234
```

`tela pair` contacts the hub, exchanges the code for a permanent token, and stores the token in the user credential store. The code is consumed on redemption and cannot be used again.

After pairing, the user can connect without a `-token` flag:

```bash
tela connect -hub wss://hub.example.com -machine barn
```

### Redeeming a code (agent)

```bash
sudo telad pair -hub wss://hub.example.com -code ABCD-1234
```

`telad pair` stores the resulting token in the system credential store. The agent then connects to the hub without a token in its config file.

For administrators who prefer a graphical interface and run `telad` on the same machine as TelaVisor (developer workstations, single-host setups), the Agents tab's *Redeem...* button opens a dialog that runs the same `telad pair` invocation under the hood. The button only handles the local case; agents on remote hosts must redeem on the host itself, since the resulting token has to land in that host's credential store.

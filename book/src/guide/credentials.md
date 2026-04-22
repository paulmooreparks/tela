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

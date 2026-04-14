# Hub administration

The `tela admin` subcommand manages a hub's tokens, access permissions, agent lifecycle, and portal registrations from the command line. All operations require a token with owner or admin role. Changes take effect immediately and persist to the hub's configuration file. No hub restart is needed.

## Authentication

Every `tela admin` command requires a hub URL and a token:

```bash
tela admin tokens list -hub wss://hub.example.com -token <token>
```

If the token is omitted, `tela` resolves it in this order:

1. `-token` flag
2. `TELA_OWNER_TOKEN` environment variable
3. `TELA_TOKEN` environment variable
4. Credential store -- the token stored by `tela login` for the hub URL

In practice, you log in once and omit the token flag on every subsequent command:

```bash
tela login wss://hub.example.com
# Token: (paste owner token, press Enter)

tela admin tokens list -hub wss://hub.example.com
```

The `-hub` flag accepts a short name if you have configured remotes, but the full URL is always accepted.

## Concepts

A hub's authorization state has two parts: identities (tokens) and permissions.

An **identity** is a named token. It has a role: `owner`, `admin`, or `user` (the default). Owner and admin tokens bypass all machine permission checks. User tokens are subject to per-machine access control. A `viewer` role exists but is reserved for the hub's auto-generated console token; it cannot be assigned when creating tokens.

**Machine permissions** determine what a user-role token can do on a specific machine: `connect`, `register`, and `manage`. These are stored as entries in the access control list. A wildcard machine ID of `*` applies the permission to all machines.

The `tokens` resource manages identities. The `access` resource manages the permissions attached to those identities. The `rotate` command replaces the secret value of a token without changing its identity or permissions.

For the formal definition of roles and permissions, see [Appendix C: Access model](../architecture/access-model.md).

## Tokens

```bash
# List all identities
tela admin tokens list -hub wss://hub.example.com

# Add a new identity (default role: user)
tela admin tokens add <id> -hub wss://hub.example.com
# Add with elevated role
tela admin tokens add <id> -hub wss://hub.example.com -role admin

# Remove an identity
tela admin tokens remove <id> -hub wss://hub.example.com
```

`tokens add` prints the token value once and never again. Copy it before closing the terminal. If you lose it, use `rotate` to issue a new one.

`tokens remove` deletes the identity and all its machine permissions. There is no soft delete or recovery.

The default role for a new identity is `user`.

### Roles

| Role | Description |
|------|-------------|
| `owner` | Full access to all hub operations, including owner-only actions |
| `admin` | Full access to all hub operations except owner-only actions |
| `user` | Access to machines governed by per-machine permissions |
| `viewer` | Read-only access to machines they have connect permission on |

## Access

The `access` resource provides a unified view of identities and their per-machine permissions.

```bash
# List all identities and their permissions
tela admin access -hub wss://hub.example.com

# Grant permissions to an identity on a machine
tela admin access grant <id> <machine> <perms> -hub wss://hub.example.com

# Grant permissions on all machines
tela admin access grant <id> '*' connect -hub wss://hub.example.com

# Revoke all permissions for an identity on a machine
tela admin access revoke <id> <machine> -hub wss://hub.example.com

# Rename an identity
tela admin access rename <id> <new-id> -hub wss://hub.example.com

# Remove an identity and all its permissions
tela admin access remove <id> -hub wss://hub.example.com
```

Permissions are specified as a comma-separated list. Valid values are `connect`, `register`, and `manage`.

```bash
# Grant connect and register on a specific machine
tela admin access grant alice barn connect,register -hub wss://hub.example.com
```

A `*` machine ID grants the permission on every machine, including ones registered after the grant is made.

## Rotate

`rotate` generates a new secret value for an existing identity without changing its name, role, or permissions. Use it to revoke a leaked token while keeping the identity intact.

```bash
tela admin rotate <id> -hub wss://hub.example.com
```

The new token value is printed once. The old token stops working immediately.

## Pair codes

A pairing code is a short, single-use code that lets you onboard a user or agent without distributing a raw token. The recipient redeems the code to receive a permanent token.

```bash
# Generate a connect code for machine barn (default expiry 24h)
tela admin pair-code barn -hub wss://hub.example.com

# Set a custom expiry
tela admin pair-code barn -hub wss://hub.example.com -expires 48h

# Generate a register code for a new agent
tela admin pair-code barn -hub wss://hub.example.com -type register

# Grant access to all machines
tela admin pair-code barn -hub wss://hub.example.com -machines '*'
```

The output includes the code and the redemption command to give to the recipient:

```
Generated pairing code: ABCD-1234
Expires: 2026-04-15T10:30:00Z

Client pairing command:
  tela pair -hub wss://hub.example.com -code ABCD-1234
```

Codes expire between 10 minutes and 7 days after generation. The `-expires` flag accepts Go duration syntax: `10m`, `24h`, `7d`.

For how users and agents redeem codes, see [Credentials and pairing](credentials.md).

## Agent

The `agent` resource lets you inspect and manage remote `telad` instances through the hub, without a direct connection to the agent machine.

```bash
# List registered agents
tela admin agent list -hub wss://hub.example.com

# Show an agent's configuration
tela admin agent config -machine barn -hub wss://hub.example.com

# Update an agent's configuration
tela admin agent set -machine barn -hub wss://hub.example.com '<json>'

# View agent logs
tela admin agent logs -machine barn -hub wss://hub.example.com
tela admin agent logs -machine barn -hub wss://hub.example.com -n 200

# Restart an agent
tela admin agent restart -machine barn -hub wss://hub.example.com

# Trigger a self-update
tela admin agent update -machine barn -hub wss://hub.example.com
tela admin agent update -machine barn -hub wss://hub.example.com -version v0.9.1

# Show the agent's current release channel
tela admin agent channel -machine barn -hub wss://hub.example.com

# Set the agent's release channel
tela admin agent channel -machine barn -hub wss://hub.example.com set stable
```

Agent management commands are forwarded through the hub to the agent and wait for a response. If the agent is offline or does not respond within 30 seconds, the command returns an error.

## Hub

The `hub` resource manages the hub itself.

```bash
# Show hub status
tela admin hub status -hub wss://hub.example.com

# View hub logs
tela admin hub logs -hub wss://hub.example.com
tela admin hub logs -hub wss://hub.example.com -n 200

# Restart the hub
tela admin hub restart -hub wss://hub.example.com

# Trigger a self-update
tela admin hub update -hub wss://hub.example.com
tela admin hub update -hub wss://hub.example.com -version v0.9.1

# Show the current release channel
tela admin hub channel -hub wss://hub.example.com

# Set the release channel
tela admin hub channel set stable -hub wss://hub.example.com
```

## Portals

Portals are external registries that list hubs for discovery. The `portals` resource manages which portals a hub is registered with.

```bash
# List registered portals
tela admin portals list -hub wss://hub.example.com

# Add a portal
tela admin portals add <name> -portal-url <url> -hub wss://hub.example.com

# Remove a portal
tela admin portals remove <name> -hub wss://hub.example.com
```

Portal changes take effect immediately. The hub begins syncing with a newly added portal without a restart.

## Flag placement

All `tela admin` subcommands accept flags after positional arguments. Both of these are equivalent:

```bash
tela admin tokens add alice -hub wss://hub.example.com -role admin
tela admin tokens add -hub wss://hub.example.com -role admin alice
```

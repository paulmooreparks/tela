# Tela Access Model

This document explains how authentication and authorization work in Tela. It covers tokens, roles, machine permissions, and how they interact. It also describes the unified access API that presents all of these as a single resource.

## The three concepts

Tela's access model has three concepts. Each one answers a different question.

| Concept | Question it answers | Where it lives |
|---------|---------------------|----------------|
| **Token** | "Who are you?" | A 64-character hex secret. Presented in the `Authorization: Bearer` header on every request. |
| **Role** | "What class of operations can you perform on the hub?" | A label attached to the token: `owner`, `admin`, `user`, or `viewer`. |
| **Machine permission** | "What can you do on a specific machine?" | An entry in the machine ACL: `register`, `connect`, or `manage`. |

These three concepts form a hierarchy. A token proves identity. The role on that token controls hub-level access. Machine permissions control what that token can do on each individual machine.

## Tokens

A token is a credential. It is a 64-character hex string (32 random bytes) that acts as both the authentication secret and the lookup key. Each token has:

- **ID**: A human-readable name (e.g., "alice", "paul-laptop", "barn-agent"). This is what you see in the UI and CLI. It has no security function.
- **Token value**: The secret. Stored in the hub's config file. Never shown in full after creation (the API returns only an 8-character preview).
- **Role**: One of four values (see below).

Tokens are created with `tela admin add-token` (remote) or `telahubd user add` (local). The pairing flow also creates tokens automatically.

When auth is enabled (at least one token exists), every API request must include a valid token. When no tokens exist, the hub runs in open mode and all operations are permitted.

## Roles

A role is a label on a token that controls hub-level API access. There are four roles:

| Role | Hub-level access | Machine-level access |
|------|-----------------|---------------------|
| **owner** | Full access to all admin endpoints. Can create/remove other owners. | Implicit access to all machines for all operations. No explicit grants needed. |
| **admin** | Full access to all admin endpoints except owner-only operations. | Implicit access to all machines for all operations. No explicit grants needed. |
| **user** | Cannot call admin endpoints. Can connect, register, and manage machines only as granted by machine permissions. | Only the machines and operations explicitly granted. |
| **viewer** | Read-only access to `/api/status` and `/api/history`. Can see all machines. Cannot connect, register, or manage. | None. View only. |

The default role is `user` (when no role is specified at token creation).

Key point: owner and admin tokens bypass all machine permission checks. They can connect to, register, and manage any machine. You never need to grant explicit machine permissions to an owner or admin token.

## Machine permissions

Machine permissions answer "what can this token do on this specific machine?" There are three:

| Permission | What it allows |
|-----------|---------------|
| **register** | The token can register an agent (telad) for this machine. Registration means the agent connects to the hub and announces itself as available. Only one token can hold the register permission per machine. |
| **connect** | The token can open a client session (tela connect) to this machine. Multiple tokens can have connect permission on the same machine. |
| **manage** | The token can send management commands (config-get, config-set, logs, restart) to this machine's agent through the hub. Multiple tokens can have manage permission on the same machine. |

Machine permissions are stored per machine in the hub's config file. The machine ID can be a specific name (e.g., "barn") or the wildcard `*` which applies to all machines.

### Example

```yaml
auth:
  tokens:
    - id: owner
      token: abc123...
      hubRole: owner
    - id: alice
      token: def456...
    - id: barn-agent
      token: ghi789...

  machines:
    "*":
      connectTokens:
        - def456...    # alice can connect to any machine
    barn:
      registerToken: ghi789...   # only barn-agent can register as "barn"
      manageTokens:
        - def456...    # alice can manage barn
```

In this example:
- **owner** can do anything (implicit, no grants needed).
- **alice** (user role) can connect to any machine (wildcard connect), and can manage barn specifically.
- **barn-agent** (user role) can register as "barn" but cannot connect to or manage anything.

## How the pieces interact

When a request arrives at the hub, evaluation proceeds in order:

1. **Is auth enabled?** If no tokens are configured, everything is allowed (open mode).
2. **Is the token valid?** Look up the token value. If not found, reject.
3. **What is the role?** If owner or admin, allow the operation (no further checks needed for machine access).
4. **Is the token a viewer?** If the operation is read-only status, allow. Otherwise reject.
5. **Does the token have the required machine permission?** Check the machine-specific ACL first, then the wildcard `*` ACL. If the token appears in the relevant list (connectTokens, manageTokens, or registerToken), allow.

```
Request arrives
    |
    v
Auth enabled? --no--> Allow
    |
   yes
    |
    v
Token valid? --no--> 401 Unauthorized
    |
   yes
    |
    v
Owner or admin? --yes--> Allow
    |
    no
    |
    v
Viewer + read-only? --yes--> Allow (status/history only)
    |
    no
    |
    v
Machine permission granted? --yes--> Allow
    |
    no
    |
    v
Deny (403 Forbidden)
```

## The unified access API

The two concepts of tokens and machine permissions are stored in different sections of the hub's config file (`auth.tokens` and `auth.machines`), but they are exposed through a single unified API: `/api/admin/access`.

Each access entry joins an identity with its role and all of its per-machine permissions, so callers do not have to fetch tokens and ACLs separately and reconcile them by matching token values:

```
GET /api/admin/access

{
  "access": [
    {
      "id": "owner",
      "role": "owner",
      "tokenPreview": "abc123...",
      "machines": [
        {"machineId": "*", "permissions": ["register", "connect", "manage"]}
      ],
      "version": 1
    },
    {
      "id": "alice",
      "role": "user",
      "tokenPreview": "def456...",
      "machines": [
        {"machineId": "*", "permissions": ["connect"]},
        {"machineId": "barn", "permissions": ["manage"]}
      ],
      "version": 7,
      "wildcardInherited": ["connect"]
    },
    {
      "id": "barn-agent",
      "role": "user",
      "tokenPreview": "ghi789...",
      "machines": [
        {"machineId": "barn", "permissions": ["register"]}
      ],
      "version": 1
    }
  ]
}
```

`version` is a per-identity monotonic counter the hub bumps on every mutation. Clients pass it back as `If-Match: "<version>"` on subsequent mutations; the hub returns `412 Precondition Failed` when the value is stale, with the current entry in the response body so the caller can diff. The same value also rides on the `ETag` response header. Clients that omit `If-Match` skip the check (force overwrite).

`wildcardInherited` (and the optional `wildcardInheritedServices`) report which permissions the wildcard `*` ACL cascades to every machine that has no explicit grant. The hub's `canConnect` and `canManage` check the wildcard as a fallback; surfacing the cascade here lets clients render the effective per-machine state without re-implementing the cascade rules. Owner, admin, and viewer identities receive role-based implicit access rather than ACL-based cascade; for them the field is empty and the implicit grant is represented by a synthetic `*` entry in `machines`.

The CLI equivalent:

```
$ tela admin access
IDENTITY      ROLE     MACHINES
owner         owner    * (all permissions)
alice         user     *: connect | barn: manage
barn-agent    user     barn: register
```

The unified access API is the recommended way to view and modify permissions. The full endpoint reference:

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/admin/access` | List all access entries |
| GET | `/api/admin/access/{id}` | Get one entry (with `ETag`) |
| PATCH | `/api/admin/access/{id}` | Rename identity (`id`) and/or change role (`role`); honors `If-Match`. A last-owner guard returns `409 Conflict` on any attempt to demote the sole owner. |
| DELETE | `/api/admin/access/{id}` | Remove identity and all permissions; honors `If-Match` |
| PUT | `/api/admin/access/{id}/machines/{m}` | Set permissions on a machine; honors `If-Match` |
| DELETE | `/api/admin/access/{id}/machines/{m}` | Revoke all permissions on a machine; honors `If-Match` |

## Common tasks

**Grant a user connect access to a machine:**

```bash
tela admin access grant alice barn connect
```

**Grant connect and manage access in one call:**

```bash
tela admin access grant alice barn connect,manage
```

**See who has access to what:**

```bash
tela admin access
```

**Rename a cryptic auto-generated identity:**

```bash
tela admin access rename paired-user-1773817343 paul-laptop
```

**Revoke all of alice's access to barn:**

```bash
tela admin access revoke alice barn
```

**Remove an identity entirely (deletes the token and all permissions):**

```bash
tela admin access remove alice
```

## What the config file looks like

The hub stores tokens and machine permissions in separate YAML sections. The unified access API joins them at query time but does not change the storage format.

```yaml
auth:
  tokens:
    - id: owner
      token: a1b2c3d4...
      hubRole: owner
    - id: console-viewer
      token: e5f6a7b8...
      hubRole: viewer
    - id: alice
      token: c9d0e1f2...

  machines:
    "*":
      connectTokens:
        - c9d0e1f2...
    barn:
      registerToken: 11223344...
      manageTokens:
        - c9d0e1f2...
```

The `machines` map uses raw token values (not identity names) because the hub must perform constant-time comparison during authentication. The access API translates between token values and identity names so you never need to work with raw tokens directly.

# Portal protocol specification

This document is the wire-level contract every Tela portal must implement.
Portals are independent processes that aggregate hubs into a directory and
proxy authenticated administrative requests through to the hubs they list.
Awan Saya is one implementation; the planned `internal/portal` Go package
will be another. Both speak the protocol described here.

The protocol carves out the **portal contract** from the **identity
implementation**. The contract is small (about ten endpoints, two auth
modes, a JSON shape per response) and stable enough to write down. The
identity implementation -- accounts, organizations, teams, billing,
self-service signup -- is out of scope and lives in whatever store an
implementation chooses to pair with the protocol.

Status: **draft, version 1.0 shape decided, extraction not yet started**.
The four open questions in the first draft of this spec were resolved
on 2026-04-08 (see section 13). The decisions are baked into sections
2, 4, 5, and 11. Awan Saya and the `telahubd` outbound portal client
in `internal/hub/hub.go` currently implement the legacy shape; both
will be migrated to the new shape in the same commit that introduces
the `internal/portal` Go package. Pre-1.0 the spec is still mutable;
post-1.0 it follows the version negotiation and backward-compatibility
rules in section 2.

Discussion of why a portal exists at all, the scaling story, and how
TelaVisor is expected to host the protocol in personal-use mode lives in
ROADMAP-1.0.md under "Portal architecture: one protocol, many hosts."
This document is the contract; that document is the rationale.

---

## 1. Roles

Three actors participate in the protocol:

| Role | What it does | Example |
|------|--------------|---------|
| **Portal** | The HTTP service that hosts the directory. Stores hub records, authenticates clients, and proxies admin requests through to the hubs it lists. | Awan Saya, `telaportal` (planned), TelaVisor in Portal mode (planned). |
| **Hub** | A `telahubd` instance that registers itself with one or more portals so users can discover it without knowing its URL up front. | Any production hub. |
| **Client** | Anything that talks to the portal as a user. Typically a browser running the portal's web UI, or TelaVisor in Infrastructure mode. | Awan Saya web UI, TelaVisor. |

The portal speaks two distinct authentication modes for two distinct sets
of endpoints:

- **User auth** for the directory query endpoints. The user is whoever the
  portal's identity store says they are. The protocol does not prescribe
  how user auth works; sessions, cookies, OAuth, hardcoded admin -- all
  legal. The protocol only requires that "this request is from user X"
  is determinable.
- **Hub sync auth** for the hub-driven `/api/hubs/sync` endpoint. The hub
  presents a sync token issued at registration time. This is an
  authentication mode independent of user auth.

A portal MAY also serve unauthenticated discovery (`/.well-known/tela`)
and any other endpoints it wants outside the protocol's scope.

---

## 2. Discovery and version negotiation: `/.well-known/tela`

A portal MUST serve a JSON document at `/.well-known/tela` that names
where the hub directory lives and which portal protocol versions the
portal speaks. This is the only well-known endpoint Tela defines and
is the entry point any client uses when given a portal URL. It serves
two purposes: directory discovery and protocol version negotiation.

### Request

```
GET /.well-known/tela HTTP/1.1
Host: portal.example.com
Accept: application/json
```

No authentication. Portals MAY serve this with `Cache-Control: public,
max-age=86400` or similar long cache directives because the value rarely
changes.

### Response

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{
  "hub_directory": "/api/hubs",
  "protocolVersion": "1.0",
  "supportedVersions": ["1.0"]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `hub_directory` | string | yes | Path on the same origin where the portal serves the hub directory endpoints (section 3). MUST be a relative path beginning with `/`. Implementations SHOULD use `/api/hubs` as the conventional default; clients MUST honor whatever value the portal returns. |
| `protocolVersion` | string | yes (post-1.0) | The portal protocol version the portal recommends clients use. Major.minor semver string. The portal MUST select this from its `supportedVersions` list. Pre-1.0 portals MAY ship `"0.x"` to mark themselves as in development. |
| `supportedVersions` | array of strings | yes (post-1.0) | The full set of portal protocol versions this portal speaks. MUST be non-empty. MUST contain `protocolVersion`. Newer portals supporting older clients list multiple versions here. |

### Version semantics

Versions follow standard semver discipline applied to a wire protocol:

- **Major version bump** (`1.x` → `2.x`) signals a breaking change.
  Clients written for major version N MUST refuse to operate against
  a portal whose `supportedVersions` does not include any `N.*` entry.
- **Minor version bump** (`1.0` → `1.1`) signals an additive change:
  new optional fields, new endpoints, new optional query parameters.
  A client written against `1.0` MUST work against any `1.x` portal,
  ignoring fields and endpoints it does not understand.
- **Within a single major version, the portal protocol is strictly
  additive.** Removing fields, renaming fields, changing field types,
  or changing the semantics of existing fields are all forbidden and
  require a major version bump. Adding new optional fields, adding
  new endpoints, and adding new optional query parameters are allowed
  in a minor version bump.

### Negotiation rule

A client MUST:

1. Fetch `/.well-known/tela` at session start.
2. Read `supportedVersions` and select the **highest** version it
   understands (where "highest" is by semver ordering).
3. Use that version's shapes and rules for the rest of the session.
4. Refuse to operate (with a clear error to the user) if no version in
   `supportedVersions` matches a major version the client supports.

A client SHOULD NOT re-fetch `/.well-known/tela` mid-session unless it
has reason to believe the portal has been upgraded.

### Fallback

If `/.well-known/tela` is not served (HTTP 404, network error, malformed
JSON), clients MUST fall back to:

- `hub_directory: "/api/hubs"` (the conventional default)
- `protocolVersion: "0"` (the unversioned legacy contract, equivalent to
  the shape this document describes minus the post-1.0 negotiation rules)

This preserves compatibility with portals that predate this document.
A client that has fallen back to `protocolVersion: "0"` MUST NOT assume
any field beyond what was documented in the legacy contract.

`telahubd`'s reference client implements the discovery + fallback in
`internal/hub/hub.go` `discoverHubDirectory()`.

### Parallel with the hub wire format

The same negotiation pattern is the obvious answer for the **hub wire
protocol** (the WebSocket protocol between agents/clients and the hub).
ROADMAP-1.0.md "Protocol freeze" calls this out as a 1.0 blocker for
the hub side. The two protocols are independent and version
independently, but they should share the same discipline: well-known
discovery surface, additive-only minor versions, breaking changes
require a major bump and a refusal-to-talk on mismatch.

---

## 3. Hub directory: `{hub_directory}` endpoints

The hub directory is a small REST resource. The path prefix is whatever
`/.well-known/tela` returns; in the conventional case that is `/api/hubs`,
and the rest of this document uses that path for clarity. A portal that
returns a different prefix MUST serve the same shapes under that prefix.

### 3.1 GET /api/hubs -- list visible hubs

Returns the list of hubs the authenticated user can see. The portal
applies whatever visibility rules its identity store dictates: in Awan
Saya, that is org/team membership; in a single-user portal, the user
sees every hub.

Request:

```
GET /api/hubs HTTP/1.1
Authorization: <user auth, implementation-specific>
```

Response:

```json
{
  "hubs": [
    {
      "name": "myhub",
      "url": "https://hub.example.com",
      "canManage": true,
      "orgName": "acme"
    }
  ]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Short hub name. Unique within the portal. Used as the addressable identifier in proxy paths. |
| `url` | string | yes | Public hub URL. Either `https://...` (HTTP+WSS) or `http://...` (HTTP+WS). The hub's own admin API and WebSocket endpoint live under this URL. |
| `canManage` | bool | yes | True when the authenticated user has admin or owner permission on this hub. Drives whether the client surfaces management actions in its UI. |
| `orgName` | string | no | Free-form display label for the organizational scope this hub belongs to, if the portal models orgs. May be `null` or omitted. Single-user portals can return `null` everywhere. |

Authentication failures return `401 Unauthorized` with the standard error
shape (section 7). An empty hub list is `200 OK` with `{"hubs": []}`, not
an error.

### 3.2 POST /api/hubs -- register or update a hub

Adds a new hub to the portal directory or updates an existing one with
the same `name`. This endpoint is called from two distinct contexts:

1. **Hub-initiated bootstrap.** The hub itself runs `registerWithPortal`
   from `telahubd`, presenting an admin token issued by an out-of-band
   means (typically a portal admin paste). The portal verifies the
   admin token, creates a hub record, and returns a fresh sync token.
2. **User-initiated add.** A logged-in user adds a hub through the
   portal UI by entering its URL and a viewer token. No admin token is
   involved; the portal authenticates the user via its session.

Request:

```http
POST /api/hubs HTTP/1.1
Content-Type: application/json
Authorization: Bearer <admin-token>          # context 1
Authorization: <user session>                # context 2

{
  "name": "myhub",
  "url": "https://hub.example.com",
  "viewerToken": "<optional 64-char hex>",
  "adminToken": "<optional, context 2 only>"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Short hub name. Must be unique within the portal. Maximum length is implementation-defined (Awan Saya enforces 255). |
| `url` | string | yes | Public hub URL. Maximum length is implementation-defined (Awan Saya enforces 2048). |
| `viewerToken` | string | no | The hub's `console-viewer` role token, if the portal will host a web console for the hub. |
| `adminToken` | string | no | The hub's owner or admin token. The portal stores this so it can proxy admin requests later (section 4); the protocol does NOT echo it back in any response. Portals MUST treat stored admin tokens as secrets. |

Response:

```json
{
  "hubs": [ { "name": "...", "url": "...", "canManage": true, "orgName": null } ],
  "syncToken": "hubsync_AbC123...",
  "updated": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `hubs` | array | yes | The user's full hub list after the registration, in the same shape as `GET /api/hubs`. |
| `syncToken` | string | when context 1 | A fresh sync token the hub will use for `PATCH /api/hubs/sync` (section 3.3). MUST start with the prefix `hubsync_` so clients can distinguish it from other token classes. The portal stores its hash; the cleartext is returned exactly once. Portals MAY omit this field for context-2 calls (user-initiated adds). |
| `updated` | bool | no | True when the registration upserted an existing record rather than creating a new one. Default false. |

A hub that is registered a second time with the same `name` MUST be
upserted (the portal updates `url`, `viewerToken`, and the stored admin
token, and issues a new sync token). The hub then learns the new sync
token from the response and persists it. This is how a hub recovers
from losing its sync token: re-register with the same admin token.

Authorization failures: `401 Unauthorized` if no valid auth, `403
Forbidden` if the user is authenticated but not authorized to add a hub
under the requested scope (e.g. organization quota reached).

### 3.3 PATCH /api/hubs/sync -- hub pushes its viewer token

Authenticated by the per-hub **sync token**, not by user session. This
endpoint is the only one in the protocol that uses sync auth; it exists
so a hub can refresh its viewer token at the portal without involving a
user.

Request:

```http
PATCH /api/hubs/sync HTTP/1.1
Content-Type: application/json
Authorization: Bearer hubsync_AbC123...

{ "name": "myhub", "viewerToken": "<new 64-char hex>" }
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | The hub name as registered. |
| `viewerToken` | yes | The new console-viewer token the portal should store. |

Response:

```json
{ "ok": true }
```

The portal MUST verify the sync token using a timing-safe comparison
against the hash it stored during registration. Mismatched tokens
return `401`. Unknown hub names return `404`.

This endpoint MUST NOT accept user auth. A user wishing to update a
hub's viewer token does so through `PATCH /api/hubs` (section 3.4),
which is user-authenticated.

### 3.4 PATCH /api/hubs -- user updates a hub record

User-authenticated update of any field on an existing hub the user can
manage. The body is a partial update; only the fields present are
changed.

Request:

```http
PATCH /api/hubs HTTP/1.1
Content-Type: application/json
Authorization: <user session>

{
  "currentName": "myhub",
  "name": "myhub-renamed",
  "url": "https://hub.example.com",
  "viewerToken": "...",
  "adminToken": "..."
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `currentName` | yes | The current name of the hub to update. |
| `name` | no | New hub name. |
| `url` | no | New hub URL. |
| `viewerToken` | no | New viewer token. |
| `adminToken` | no | New admin token (stored as a secret; never echoed back). |

Response: same shape as `GET /api/hubs`, reflecting the post-update list.

### 3.5 DELETE /api/hubs -- user removes a hub

```http
DELETE /api/hubs?name=myhub HTTP/1.1
Authorization: <user session>
```

The hub name is passed as a query parameter, not in the request body, so
clients can use `DELETE` without a body. A portal MAY accept the name in
a JSON body too, but the query-parameter form is normative.

Authorization MUST be more restrictive than read access: only hub owners,
organization owners, or platform admins can delete (in Awan Saya, hub
admins explicitly cannot delete). The exact rule is implementation-
defined; the protocol only requires that delete is gated tighter than
read.

Response: same shape as `GET /api/hubs`, reflecting the post-delete list.

---

## 4. Admin proxy: `/api/hub-admin/{hubName}/{operation}`

A portal MUST expose an HTTP proxy that lets authenticated users invoke
the hub's admin API without having direct network reachability or
needing the hub's admin token. The portal holds the admin token (stored
during registration) and forwards the request on the user's behalf.

The proxy URL is:

```
{portal-base-url}/api/hub-admin/{hubName}/{operation}
```

Where:

- `{hubName}` is the short hub name (URL-encoded if it contains special
  characters).
- `{operation}` is the hub admin path **without** the leading
  `/api/admin/` prefix. Examples: `access`, `agents/barn/logs`,
  `update`, `pair-code`, `tokens`, `restart`. The portal MUST internally
  prepend `/api/admin/` before forwarding to the hub.

The portal MUST NOT accept the legacy double-prefix form
`/api/hub-admin/{hubName}/api/admin/{operation}`. Clients MUST use the
short form. This is the canonical shape for portal protocol version 1.0
and onward; portals advertising `protocolVersion: "0"` (legacy fallback
per section 2) used the double-prefix form.

The reason: the portal's `/api/hub-admin/` namespace and the hub's
`/api/admin/` namespace are unrelated paths that happened to share a
prefix string. Carrying both in one URL was a coincidence of how the
two projects independently organized their admin endpoints, not a
structural relationship. The shorter form decouples the portal URL
shape from the hub URL shape: if the hub ever moved its admin API to
a different path, the portal proxy URL would not change.

### 4.1 Method passthrough

The proxy MUST forward the original HTTP method unchanged. The Tela hub
admin API uses real REST verbs (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`),
and downgrading any of them collapses semantics. In particular,
`PATCH /api/hub-admin/myhub/api/admin/update` is how a user changes a
hub's release channel through the portal, and any portal that folds
PATCH into POST breaks that path.

### 4.2 Body and query string passthrough

The proxy MUST forward the original request body byte-for-byte for
methods other than `GET` and `HEAD`. The proxy MUST also preserve the
original query string. The portal MUST set `Authorization: Bearer
<storedAdminToken>` on the outbound request and MUST NOT pass through
the inbound `Authorization` header.

### 4.3 Response passthrough

The proxy MUST return the upstream response status code and body
unchanged. It SHOULD set `Content-Type: application/json` and
`Cache-Control: no-cache` on the response.

### 4.4 Authorization

A portal MUST require user auth on every proxy call and MUST verify that
the user has `canManage` on the named hub before forwarding. A user
without manage permission gets `403 Forbidden`. A user calling a hub
they cannot see at all gets `404 Not Found`. The portal MUST NOT leak
the existence of a hub to users who cannot see it.

### 4.5 Failure modes

| Condition | Status |
|-----------|--------|
| User not authenticated | 401 |
| Hub does not exist OR user cannot see it | 404 |
| User can see hub but lacks `canManage` | 403 |
| Portal has no admin token stored for this hub | 400 with body `{"error":"no admin token stored for this hub"}` |
| Hub is unreachable / network error | 502 with body `{"error":"hub unreachable"}` |
| Hub returned a status code | passthrough (the portal does not interpret the upstream response) |

---

## 5. Fleet aggregation: `GET /api/fleet/agents`

A portal MUST expose an aggregated view of every agent across every hub
the user can manage. This is the endpoint TelaVisor and the Awan Saya web
UI use to populate the cross-hub Agents tab.

This is the **only** aggregation endpoint in the protocol. Per-agent
actions (restart, update, logs, config-get, config-set, update-status,
update-channel, etc.) go through the generic admin proxy (section 4),
not through a fleet-specific URL. The aggregation lives in the protocol
because it does work no client can replicate efficiently in a single
call: the portal already holds per-hub viewer tokens, already iterates
the user's hubs to compute the directory list, and is the natural place
to handle per-hub timeouts as a unit. Pushing that work to clients
would force every client (TelaVisor, Awan Saya, future frontends) to
reimplement iteration, token lookup, and timeout handling.

### Request

```
GET /api/fleet/agents HTTP/1.1
Authorization: <user session>
```

Optional query parameters:

| Parameter | Description |
|-----------|-------------|
| `orgId` | Restrict the response to hubs in the given org scope. Implementation-defined; portals that do not model orgs MAY ignore this parameter. |

### Response

```json
{
  "agents": [
    {
      "id": "barn",
      "hub": "myhub",
      "hubUrl": "https://hub.example.com",
      "online": true,
      "version": "v0.6.0-dev.42",
      "hostname": "barn.local",
      "os": "linux",
      "displayName": "Barn",
      "tags": ["lab"],
      "location": "garage",
      "owner": null,
      "lastSeen": "2026-04-08T03:14:00Z",
      "sessionCount": 0,
      "services": [{"port": 22, "name": "SSH"}],
      "capabilities": {"fileShare": true}
    }
  ]
}
```

The portal MUST iterate the user's manageable hubs, query each hub's
`/api/status` endpoint with the stored viewer token, and merge the
`machines` arrays into a flat list. Each agent record MUST include the
`hub` field naming the hub it belongs to and the `hubUrl` field giving
the hub's URL. All other fields are passthroughs from the hub status
shape and the portal MUST NOT modify them; if a hub is unreachable, the
portal SHOULD log and skip it (returning agents from the reachable hubs
rather than failing the whole request).

A portal MAY add additional fields to each agent record, but clients
MUST tolerate unknown fields and MUST NOT break if a portal omits any
optional field.

### Per-agent actions go through the admin proxy

To send a management action to a specific agent, use the admin proxy
(section 4):

```http
POST /api/hub-admin/myhub/agents/barn/restart HTTP/1.1
Content-Type: application/json
Authorization: <user session>

{}
```

This forwards to the hub's `POST /api/admin/agents/barn/restart`. Known
actions include `config-get`, `config-set`, `logs`, `restart`, `update`,
`update-status`, `update-channel`. Future actions added to the hub work
without portal changes because the proxy is generic.

---

## 6. Authentication summary

| Endpoint | Auth |
|----------|------|
| `/.well-known/tela` | none |
| `GET /api/hubs` | user |
| `POST /api/hubs` (hub bootstrap) | hub admin token |
| `POST /api/hubs` (user add) | user |
| `PATCH /api/hubs/sync` | hub sync token (`hubsync_*`) |
| `PATCH /api/hubs` | user |
| `DELETE /api/hubs` | user |
| `/api/hub-admin/{name}/...` | user, gated on `canManage` |
| `GET /api/fleet/agents` | user |

The protocol does not prescribe **how** user auth works. Awan Saya uses
session cookies + a CSRF check; a single-user portal could use a static
admin token in a config file; TelaVisor in Portal mode would treat the
local desktop user as authenticated by virtue of their OS session. Any
of these are legal as long as the portal can answer "is this request
authorized for this user, on this hub, for this operation?"

---

## 7. Error shape

All error responses MUST be JSON with at least an `error` field:

```json
{ "error": "human-readable message" }
```

Status codes follow standard REST conventions:

| Code | Meaning |
|------|---------|
| 400 | Bad request (malformed body, missing required fields) |
| 401 | Authentication required or failed |
| 403 | Authenticated but not authorized for this operation |
| 404 | Resource not found, OR resource exists but the user cannot see it (do not leak existence) |
| 409 | Conflict (e.g. registering a hub name that already exists, in older portals that do not support upsert) |
| 502 | Upstream hub unreachable |
| 5xx | Portal-side error |

Portals MAY add additional fields to error responses (e.g. `code`,
`details`) but MUST always include `error`.

---

## 8. Sync token format

Sync tokens issued by `POST /api/hubs` (section 3.2) MUST start with the
prefix `hubsync_` so clients can distinguish them from user session
tokens, hub admin tokens, viewer tokens, and pair codes. The remainder
SHOULD be at least 32 bytes of cryptographic randomness, encoded in a
URL-safe alphabet.

The portal MUST store only the SHA-256 hash of the sync token, not the
cleartext. The cleartext is returned exactly once in the registration
response and the hub MUST persist it to its `update.portals[name].syncToken`
field for use in `PATCH /api/hubs/sync` (section 3.3).

If a hub loses its sync token, the recovery procedure is to re-register
with `POST /api/hubs` and a fresh admin token: the portal upserts the
record and issues a new sync token, which the hub stores.

---

## 9. CORS and origin policy

A portal SHOULD reject cross-origin state-changing requests (`POST`,
`PUT`, `PATCH`, `DELETE`) unless the request origin is on an explicit
allowlist. Awan Saya does this via an `isOriginAllowed` check; the
protocol does not prescribe the allowlist format.

`/.well-known/tela` and `GET /api/hubs` SHOULD be CORS-permissive
(`Access-Control-Allow-Origin: *`) so any client can discover and read.

---

## 10. What is **not** in the protocol

The following are explicitly NOT part of the portal protocol. They are
SaaS concerns of specific implementations and have no place in any
client that talks to the portal:

- **Account / user lifecycle.** Sign up, password reset, email
  verification, MFA, account deletion. Awan Saya implements these under
  `/api/sign-up`, `/api/me/*`, `/api/forgot-password`, `/api/admin/*`.
  None of those routes are part of this spec; a single-user portal does
  not implement them.
- **Organization, team, and membership management.** Inviting users to
  hubs, switching the active org, granting support access. Awan Saya
  implements `/api/hubs/{name}/invitations`, `/api/hubs/{name}/members`,
  `/api/me/organization`, etc. Out of scope.
- **Billing, plans, and tier limits.** Awan Saya enforces a `max_hubs`
  per organization; that is policy on top of the protocol, not the
  protocol.
- **Audit logging.** Portals MAY log activity, but no API surface for
  reading audit logs is part of the protocol.
- **The hub's own admin API.** Portals proxy to it (section 4) but they
  do not extend or reinterpret it. Anything addressed in the hub's
  `internal/hub/admin_api.go` belongs to that surface, not this one.

A portal that implements only the routes in this spec is a valid Tela
portal. Awan Saya is a valid Tela portal that ALSO implements the SaaS
surface above. A future TelaVisor Portal mode would be a valid Tela
portal that omits the SaaS surface entirely.

---

## 11. Conformance checklist

To call yourself a Tela portal, you must:

- [ ] Serve `/.well-known/tela` (section 2) including `protocolVersion`
      and `supportedVersions` fields
- [ ] Honor the version negotiation rule: refuse clients whose major
      version is not in `supportedVersions`, treat the protocol as
      strictly additive within a major version
- [ ] Serve `GET /api/hubs` (section 3.1) returning the documented shape
- [ ] Serve `POST /api/hubs` (section 3.2) supporting both hub-bootstrap
      and user-add contexts, returning a `hubsync_*` sync token in the
      bootstrap context
- [ ] Serve `PATCH /api/hubs/sync` (section 3.3) authenticated by sync
      token, with timing-safe comparison
- [ ] Serve `PATCH /api/hubs` and `DELETE /api/hubs?name=` (sections 3.4,
      3.5)
- [ ] Serve `/api/hub-admin/{hubName}/{operation}` (section 4) where
      `{operation}` is the hub admin path **without** the `/api/admin/`
      prefix, preserving method, body, and query string; gated on
      `canManage`. Refuse the legacy double-prefix form.
- [ ] Serve `GET /api/fleet/agents` (section 5) returning the merged
      cross-hub agent list
- [ ] Return errors in the documented JSON shape (section 7)
- [ ] Store sync tokens as SHA-256 hashes only (section 8)

You MAY implement additional endpoints, but a client written against
this spec MUST work against your portal without knowing about them.

You MUST NOT implement any of the following endpoints, which were
considered and removed during the 1.0 spec finalization (see section
13 for the rationale):

- `POST /api/fleet/agents/{hub}/{machine}/{action}` -- use the admin
  proxy at `POST /api/hub-admin/{hub}/agents/{machine}/{action}` instead.
- `POST /api/hubs/{hubName}/pair-code` -- use the admin proxy at
  `POST /api/hub-admin/{hubName}/pair-code` instead.
- `POST /api/hub-admin/{hubName}/api/admin/{operation}` (the legacy
  double-prefix admin proxy form) -- use the short form
  `/api/hub-admin/{hubName}/{operation}` instead.

---

## 12. Reference implementations

| Implementation | Status | Storage | Identity model |
|----------------|--------|---------|----------------|
| Awan Saya | Production | PostgreSQL | Multi-org with accounts, organizations, teams, and hub memberships. |
| `internal/portal` (Go) | Planned | Pluggable | TBD: file-backed for personal use, postgres for parity with Awan Saya. |

The `telahubd` outbound portal client lives in `internal/hub/hub.go`:
- `discoverHubDirectory()` — reads `/.well-known/tela` (section 2)
- `registerWithPortal()` — POST `/api/hubs` (section 3.2)
- `syncViewerTokenToPortals()` — PATCH `/api/hubs/sync` (section 3.3)

These functions are the canonical client and any new portal MUST keep
them working.

---

## 13. Resolved decisions

This section records the four open questions the first draft of this
spec deferred and the decisions made before the `internal/portal`
extraction was scheduled. The decisions are *baked into* the rest of
this document; this section exists to document the rationale so the
reasoning is preserved.

### 13.1 Protocol versioning: yes, on `/.well-known/tela`

**Decision.** The portal protocol gains a version field on
`/.well-known/tela` (section 2). Two new fields, `protocolVersion`
and `supportedVersions`, are required post-1.0. Pre-1.0 fallback for
portals that do not yet ship the fields is explicit and documented.

**Why this and not the alternatives.** Three options were on the
table: (A) no version field, strict additive-only rule post-1.0; (B)
version field on `/.well-known/tela` only, used for discovery-time
negotiation; (C) version field on every response, plus discovery.

Option B was chosen because `/.well-known/tela` is already the right
place for capability discovery in any HTTP API (RFC 8615), the
negotiation happens once per session rather than on every call, and
it future-proofs the protocol without polluting every response shape.
Option A leaves no graceful upgrade path for breaking changes; option
C protects against a non-existent failure mode (a portal silently
upgrading mid-session) at the cost of a field on every response.

The same pattern is the obvious answer for the **hub wire protocol**
under ROADMAP-1.0.md "Protocol freeze." The two protocols are
independent and version independently, but they should share the same
discipline.

### 13.2 Admin proxy URL shape: short form only

**Decision.** The proxy URL is
`/api/hub-admin/{hubName}/{operation}` where `{operation}` is the hub
admin path **without** the `/api/admin/` prefix. The legacy
double-prefix form is forbidden in protocol version 1.0 and onward
(section 4).

**Why this and not the alternatives.** Two options were on the table:
(A) keep the double-prefix form as historical accident, document the
duplication as incidental; (B) strip the prefix and forbid the legacy
form pre-1.0.

Option B was chosen because the no-cruft pre-1.0 policy in CLAUDE.md
exists for exactly this kind of cleanup. The double-prefix form was a
coincidence of how two projects independently organized their admin
namespaces, not a structural relationship. Decoupling the portal URL
shape from the hub URL shape now means the hub can move its admin API
later without breaking portal clients. The migration cost is bounded
and small (server, two frontends, two client shims, one commit).

### 13.3 Fleet aggregation stays its own endpoint, per-action duplicate goes

**Decision.** `GET /api/fleet/agents` (section 5) stays as the cross-
hub aggregation endpoint. `POST /api/fleet/agents/{hub}/{m}/{action}`
is **deleted** from the spec; per-agent actions go through the generic
admin proxy at `POST /api/hub-admin/{hub}/agents/{m}/{action}`.

**Why this and not the alternatives.** Three options were on the
table: (A) keep both families, delete the per-action duplicate; (B)
fold everything under `/api/hub-admin/`, delete `/api/fleet/`; (C)
promote fleet to a generalized `/api/aggregates/` namespace.

Option A was chosen because the aggregation endpoint provides real
value the admin proxy cannot match in a single call (server-side hub
iteration, per-hub viewer-token lookup, per-hub timeout handling),
and the per-action endpoint provides no value over the generic proxy.
The "fleet vs hub-admin" split is a clean conceptual rule:
**aggregate = fleet, single = hub-admin**. Option B would force every
client (TelaVisor, Awan Saya, future frontends) to reimplement
iteration and timeout handling. Option C is YAGNI -- one aggregation
exists today, designing a namespace for hypothetical future
aggregations is over-engineering.

If a second aggregation appears (cross-hub session list, cross-hub
history view, etc.), revisit whether `/api/fleet/` should be renamed
to `/api/aggregates/` or whether the second aggregation gets its own
family. Don't pre-decide that now.

### 13.4 Pair code goes through the generic admin proxy

**Decision.** The dedicated `POST /api/hubs/{hubName}/pair-code`
endpoint is **deleted** from the spec. Pair-code generation is one
instance of the generic admin proxy: clients call
`POST /api/hub-admin/{hubName}/pair-code` and the portal forwards to
the hub's `POST /api/admin/pair-code`.

**Why this and not the alternatives.** Three options were on the
table: (A) keep the dedicated endpoint, document it as canonical,
forbid pair-code through the proxy; (B) delete the dedicated endpoint,
fold pair-code into the generic proxy like every other admin
operation; (C) keep both as equivalent.

Option B was chosen because the whole point of the generic admin
proxy is that it's generic. Every hub admin endpoint should be
reachable through it. The dedicated endpoint existed for historical
reasons (pair-code shipped before the proxy was generalized) and the
no-cruft policy says to clean that up before 1.0 freezes the surface.
The "pair-code is special, it deserves its own URL" justification
does not hold up: every hub admin endpoint is special to somebody;
none of the others got promoted to dedicated portal URLs. If
portal-side policy ever needs to be added (rate limits, TTL caps),
the right place is middleware on the admin proxy that matches the
specific path, not a parallel endpoint.

### 13.5 Implementation status

These four decisions are baked into sections 2, 4, 5, and 11 of this
spec. The `telahubd` outbound portal client in `internal/hub/hub.go`
already matches sections 2, 3.2, and 3.3 (the legacy "no version
fallback" path). The Awan Saya server already matches the legacy
shapes. Both will need updates when the `internal/portal` extraction
work begins:

- `telahubd` learns to read `protocolVersion` and `supportedVersions`
  from `/.well-known/tela` and to refuse portals whose major version
  it does not support.
- Awan Saya server gets the new `/.well-known/tela` fields, the
  short-form admin proxy URL, and loses the per-action fleet endpoint
  and the dedicated pair-code endpoint.
- Awan Saya web UI and TelaVisor's `adminAPICall` shim and the
  `portal-shared.js` helpers all switch to the short-form proxy URL.
- Awan Saya web UI's pair-code call switches from the dedicated
  endpoint to the generic admin proxy form.

These migrations land in the same commit that introduces
`internal/portal`. Pre-1.0 we don't carry both shapes; the cleanup is
the point of doing this work before the freeze.

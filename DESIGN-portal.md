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

Status: **draft, not yet implemented in the Tela tree**. The current Awan
Saya server matches this spec, and the `telahubd` outbound portal client
in `internal/hub/hub.go` (`registerWithPortal`, `discoverHubDirectory`,
`syncViewerTokenToPortals`) matches this spec. New implementations must
match it too. Pre-1.0 the spec is mutable; post-1.0 it follows the same
backward-compatibility rules as the hub admin API.

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

## 2. Discovery: `/.well-known/tela`

A portal MUST serve a JSON document at `/.well-known/tela` that names
where the hub directory lives. This is the only well-known endpoint Tela
defines and is the entry point any client uses when given a portal URL.

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

{ "hub_directory": "/api/hubs" }
```

The single field `hub_directory` is the path on the same origin where the
portal serves the hub directory endpoints (section 3). It MUST be a
relative path beginning with `/`. Implementations SHOULD use `/api/hubs`
as the conventional default; clients MUST honor whatever value the
portal returns.

### Fallback

If `/.well-known/tela` is not served (HTTP 404, network error, malformed
JSON), clients MUST fall back to `/api/hubs`. This preserves
compatibility with portals that predate this document.

`telahubd`'s reference client implements the fallback in
`internal/hub/hub.go` `discoverHubDirectory()`.

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
shape (section 5). An empty hub list is `200 OK` with `{"hubs": []}`, not
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

## 4. Admin proxy: `/api/hub-admin/{hubName}/{path}`

A portal MUST expose an HTTP proxy that lets authenticated users invoke
the hub's admin API without having direct network reachability or
needing the hub's admin token. The portal holds the admin token (stored
during registration) and forwards the request on the user's behalf.

The proxy URL is:

```
{portal-base-url}/api/hub-admin/{hubName}/{adminPath}
```

Where:

- `{hubName}` is the short hub name (URL-encoded if it contains special
  characters).
- `{adminPath}` is the **full hub admin path including the leading
  `/api/admin/`**, e.g. `/api/admin/access`, `/api/admin/agents/barn/logs`,
  `/api/admin/update`.

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

## 5. Fleet aggregation: `/api/fleet/agents`

A portal MUST expose an aggregated view of every agent across every hub
the user can manage. This is the endpoint TelaVisor and the Awan Saya web
UI use to populate the cross-hub Agents tab.

### 5.1 GET /api/fleet/agents

Request:

```
GET /api/fleet/agents HTTP/1.1
Authorization: <user session>
```

Optional query parameters:

| Parameter | Description |
|-----------|-------------|
| `orgId` | Restrict the response to hubs in the given org scope. Implementation-defined; portals that do not model orgs MAY ignore this parameter. |

Response:

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

### 5.2 POST /api/fleet/agents/{hub}/{machine}/{action}

Sends a management action to a specific agent. This is a convenience
wrapper around the admin proxy (section 4) that targets the agent
management proxy (`/api/admin/agents/{machine}/{action}`) at the hub.

Request:

```http
POST /api/fleet/agents/myhub/barn/restart HTTP/1.1
Content-Type: application/json
Authorization: <user session>

{}
```

The body is forwarded to the hub's `POST /api/admin/agents/barn/restart`
endpoint. Action names are passed through unchanged; the portal does not
maintain an allowlist. Known actions include `config-get`, `config-set`,
`logs`, `restart`, `update`, `update-status`, `update-channel`. Future
actions added to the hub work without portal changes.

Authorization: same rules as section 4 (user must have `canManage` on
the hub).

Response: passthrough from the hub.

---

## 6. Pairing codes: `/api/hubs/{hubName}/pair-code`

A portal MUST expose a pair-code generation endpoint that proxies to the
hub's `/api/admin/pair-code` (in `internal/hub/pair.go`). This is what
TelaVisor and the Awan Saya web UI use to generate one-time onboarding
codes for new agents and clients.

### Request

```http
POST /api/hubs/myhub/pair-code HTTP/1.1
Content-Type: application/json
Authorization: <user session>

{
  "type": "register",
  "machineId": "barn",
  "machines": ["barn"],
  "ttl": "10m"
}
```

The body is forwarded to the hub's pair-code endpoint. The portal MUST
require `canManage` on the named hub. The portal MAY add a TTL cap or
other policy on top.

### Response

Passthrough from the hub.

---

## 7. Authentication summary

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
| `POST /api/fleet/agents/{hub}/{m}/{action}` | user, gated on `canManage` |
| `POST /api/hubs/{name}/pair-code` | user, gated on `canManage` |

The protocol does not prescribe **how** user auth works. Awan Saya uses
session cookies + a CSRF check; a single-user portal could use a static
admin token in a config file; TelaVisor in Portal mode would treat the
local desktop user as authenticated by virtue of their OS session. Any
of these are legal as long as the portal can answer "is this request
authorized for this user, on this hub, for this operation?"

---

## 8. Error shape

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

## 9. Sync token format

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

## 10. CORS and origin policy

A portal SHOULD reject cross-origin state-changing requests (`POST`,
`PUT`, `PATCH`, `DELETE`) unless the request origin is on an explicit
allowlist. Awan Saya does this via an `isOriginAllowed` check; the
protocol does not prescribe the allowlist format.

`/.well-known/tela` and `GET /api/hubs` SHOULD be CORS-permissive
(`Access-Control-Allow-Origin: *`) so any client can discover and read.

---

## 11. What is **not** in the protocol

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

## 12. Conformance checklist

To call yourself a Tela portal, you must:

- [ ] Serve `/.well-known/tela` (section 2)
- [ ] Serve `GET /api/hubs` (section 3.1) returning the documented shape
- [ ] Serve `POST /api/hubs` (section 3.2) supporting both hub-bootstrap
      and user-add contexts, returning a `hubsync_*` sync token in the
      bootstrap context
- [ ] Serve `PATCH /api/hubs/sync` (section 3.3) authenticated by sync
      token, with timing-safe comparison
- [ ] Serve `PATCH /api/hubs` and `DELETE /api/hubs?name=` (sections 3.4,
      3.5)
- [ ] Serve `/api/hub-admin/{hubName}/{adminPath}` (section 4) preserving
      method, body, and query string; gated on `canManage`
- [ ] Serve `GET /api/fleet/agents` (section 5.1) and `POST /api/fleet/
      agents/{hub}/{machine}/{action}` (section 5.2)
- [ ] Serve `POST /api/hubs/{hubName}/pair-code` (section 6)
- [ ] Return errors in the documented JSON shape (section 8)
- [ ] Store sync tokens as SHA-256 hashes only (section 9)

You MAY implement additional endpoints, but a client written against
this spec MUST work against your portal without knowing about them.

---

## 13. Reference implementations

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

## 14. Open questions

These are flagged for resolution before extracting `internal/portal`.
None blocks writing this spec, but each has to be answered before code
lands.

1. **Does the protocol need versioning?** Today there is no
   `protocolVersion` field on any portal endpoint. If the spec evolves
   pre-1.0 we just change it; post-1.0 we need either a wire version or
   a strict backward-compatibility rule. ROADMAP-1.0.md "Protocol freeze"
   already calls this out for the hub wire format; the portal protocol
   needs the same treatment.
2. **Should the proxy paths normalize to a tighter shape?** The current
   `/api/hub-admin/{hubName}/api/admin/...` URL has `/api/admin/` in it
   twice, which is awkward. A cleaner shape would be
   `/api/hub-admin/{hubName}/{adminPath}` where `adminPath` does not
   include the `/api/admin/` prefix. Pre-1.0 we can change this; the
   trade-off is that every existing client must be updated in the same
   commit.
3. **Should fleet aggregation move under hub-admin?** Fleet endpoints
   exist as a convenience and could plausibly be expressed as repeated
   admin proxy calls. Keeping them as a distinct family makes the common
   case (TelaVisor's cross-hub Agents tab) cheap; folding them into the
   admin proxy makes the protocol smaller. Decide before extraction.
4. **Pair-code endpoint location.** Section 6 puts pair-code under
   `/api/hubs/{hubName}/pair-code` (matching Awan Saya today), but it
   could equally well live under `/api/hub-admin/{hubName}/api/admin/pair-code`
   via the generic proxy. Two endpoints for the same operation is cruft.
   Decide before extraction.

These will be resolved when the spec moves from "draft" to "stable" as
part of the `internal/portal` extraction work.

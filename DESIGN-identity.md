# Identity model

This document specifies how Tela identifies hubs, machines, agent
installations, and portals across the fabric. It is the design that
underpins cross-portal deduplication, rename survival, and the
correct behavior of TelaVisor when the same hub is reachable through
multiple portal sources.

Status: **approved on 2026-04-09, ready for implementation**. The
six open questions raised in the first draft were resolved by the
user; the resolutions are baked into sections 5, 7, and 10 and the
decisions in section 10 are recorded as resolved decisions rather
than open questions. No code has been written against this
document yet; Phase 2 of section 11 begins implementation.

This document is a sibling of DESIGN-portal.md and a prerequisite
for portal protocol version 1.1, which adds the identity fields to
the wire format. The protocol amendment is described in section 6
and will land as an edit to DESIGN-portal.md as the first commit
of Phase 3.

---

## 1. Why

Today, hubs are identified by URL, machines by user-chosen name, and
portals not at all. Every one of these is wrong.

URLs are mutable. An operator can change a hub's domain, move it
behind a different reverse proxy, or replace `wss://` with `https://`.
Each change breaks every system that treats the URL as identity:
profile YAMLs, dashboard reconciliation, portal directories,
credstore lookups. We have already hit this twice in dogfooding
this week. The fix has been schema gymnastics each time.

Machine names are user-chosen labels. Two different machines on
different hubs can both be named `barn`. The same machine can be
renamed `stable` next week. Treating the name as identity makes
"is this the same machine?" unanswerable.

Portals have no identifier at all. TelaVisor distinguishes them by
URL, which inherits all of the URL-as-identity problems plus a new
one: a single portal might be reachable at multiple URLs (Awan Saya
is currently reachable at both `awansaya.net` and `awansatu.net`,
which is how the Remotes tab ended up with a stale entry pointing
at the old domain).

The cost of getting identity wrong compounds. Cross-portal
deduplication is impossible without a stable identifier per entity.
Audit trails that survive renames are impossible. The "is this the
same hub I added last month, just at a new URL?" question is
unanswerable. Every feature that wants to reason about an entity's
identity has to invent its own ad-hoc fingerprinting scheme.

The fix is the same fix every distributed system eventually adopts:
**every unique entity gets a UUID assigned at creation time, and
that UUID is the only identity. Names, URLs, and labels are display
metadata.**

---

## 2. Principles

These rules apply to every entity in the fabric.

1. **Every unique entity has a UUID.** Hubs, machines, agent
   installations, and portals each get one. The UUID is created
   when the entity is created and is never reassigned.

2. **IDs are immutable. Names and URLs are mutable.** Once
   assigned, an ID is stable for the lifetime of the entity. Names,
   URLs, display labels, organizational scope, and admin tokens are
   all metadata that can change without affecting identity.

3. **IDs are not secret.** They appear in unauthenticated discovery
   endpoints. They are not credentials. Anyone who can talk to the
   hub can learn its ID, and that is fine.

4. **IDs are not migration data.** Pre-1.0, hubs, agents, and
   portals that exist today do not have IDs. The migration plan is
   to destroy and rebuild, not to retrofit. There will be no
   migration code, no compatibility shims, no `if id == ""`
   fallback paths.

5. **The system never invents identity.** If two portals report
   different IDs for what looks like "the same hub" because the
   operator forgot to copy state when moving the hub, the system
   treats them as two different hubs. There is no fingerprinting,
   no fuzzy matching. The user can manually merge by destroying
   one record and re-pointing.

6. **Display metadata is per-portal.** A hub's `name` is whatever
   the portal calls it. Two portals can call the same hub
   different names. The hub's ID is global; its name is not.

7. **Naming convention is camelCase, everywhere it can be.** Wire
   formats (JSON, YAML), config field names, and prose all use
   camelCase: `hubId`, `agentId`, `machineRegistrationId`,
   `portalId`, `profileId`. The legacy snake_case fields in the
   file store (`admin_token_hash`, `viewer_token`,
   `sync_token_hash`, `org_name`) are renamed to camelCase as
   part of this stretch (see Phase 3). The legacy `machineID`
   form in JSON responses is renamed to `machineId`. Go struct
   fields follow Go convention (`HubID`, `AgentID`, etc., with
   `ID` capitalized) because gofmt/golint require it; the JSON
   and YAML tags on those fields are still camelCase. There is
   no `_id` suffix anywhere except in legacy fields scheduled
   for removal.

---

## 3. Entities and identifiers

Five entities need identity. Each gets a UUID assigned at creation
time and persisted in the entity's local state.

| Entity | ID field | Assigned by | Persisted in | Lifetime | What it means |
|---|---|---|---|---|---|
| **Hub** | `hubId` | `telahubd` at first start | `telahubd.yaml` | Forever | One physical or logical Tela hub instance. Survives URL change, rename, redeployment, portal re-registration. |
| **Agent installation** | `agentId` | `telad` at first start | `~/.tela/telad.state` (separate from `telad.yaml`) | Forever per install | One physical telad installation on one physical machine. Survives rename, hub re-registration, registration with additional hubs. |
| **Machine registration** | `machineRegistrationId` | hub when an `agentId` first registers with it | hub state | Per (hub, agent) pair | The hub's record of one agent's relationship with this hub. A new machineRegistrationId is generated when a fresh agentId registers; the same agentId reconnecting reuses the existing record. |
| **Portal** | `portalId` | portal at first start | portal store (file YAML or AS database) | Forever | One portal instance. Survives URL change and rename. |
| **Profile** | `profileId` | TelaVisor when a profile is created | the profile YAML file's top-level field | Forever | One TV connection profile (e.g. "Home Cloud"). Survives rename, file move, future cross-device sync. |

Four notes on the table.

**`agentId` vs `machineRegistrationId` is the load-bearing
distinction.** The agentId says "this is the same telad install I
saw before." The machineRegistrationId says "this is the same
relationship between that telad install and this hub." The current
code conflates the two: today, when a hub receives a register
message, it identifies the agent by the `Name` field from the
agent's machineConfig (today's "machineID" string). That value is a
user-chosen label, not an identity. After this change, the hub
identifies the agent by `agentId` (sent on register), creates a
machineRegistrationId for the new (hub, agent) pair if one does
not exist, and treats `Name` as a display label.

**The current "machineID" string becomes a display name.** Code
that today reads `machineID` as an identifier needs to switch to
either `agentId` (for cross-hub correlation) or
`machineRegistrationId` (for "this specific record on this
specific hub"). The string formerly known as `machineID` (e.g.
`barn`, `gohub`, `staging`) becomes the human-readable display
name and is no longer load-bearing for identity.

**Cross-hub agent correlation is intentional but not automatic.**
If the same telad install registers with two hubs, both
registrations carry the same agentId. TelaVisor can correlate
them and surface the relationship in the UI ("this agent is
registered with hub A as 'barn' and hub B as 'stable'"). It does
NOT silently merge them into one row. See section 7 for the dedup
rules and the linked-row UI pattern.

**Profiles get UUIDs because they are entities the user reasons
about over time.** A profile is "Home Cloud" or "Work VPN" — a
named bundle of hub connections, machines, services, and mount
config. The user renames profiles, moves them between machines,
and (in the future) syncs them across devices. Without an ID,
the file name is the only identifier, which means renaming
breaks every reference (autostart entries, mount table, the
"default profile" setting). With a profileId, the user-visible
name is just metadata.

---

## 4. Format

UUIDs are RFC 4122 version 4, lowercase hex with dashes:

```
550e8400-e29b-41d4-a716-446655440000
```

Generators MUST use a cryptographically secure source of randomness
(`crypto/rand` in Go, equivalent in other languages). Validators
MUST accept any well-formed RFC 4122 UUID regardless of version
number, but generators SHOULD emit v4.

Why v4 and not v7 (time-ordered)? Time ordering would let us
extract creation timestamps from IDs, which is occasionally useful
for forensics but mostly redundant with the timestamps the
backing store already records. v4 is simpler, has wider language
support, and does not leak timing information. The cost of v4
("not sortable") does not matter for our use case because IDs are
looked up by exact match, never sorted or range-scanned.

Wire and config field names follow Principle 7 (camelCase
everywhere). On the wire (JSON and YAML), the field is named
`hubId`, `agentId`, `machineRegistrationId`, `portalId`, or
`profileId` — the entity name plus `Id` in camelCase. There is no
`_id` suffix and no all-caps `ID` form on the wire. The legacy
`machineID` form in `/api/status` machine entries is renamed to
`machineId` as part of this stretch.

Where context is unambiguous (e.g. a directory entry where `id`
clearly refers to the entry's hub), the field MAY be named simply
`id`. Where context is ambiguous (e.g. a fleet aggregation entry
which carries both hub and agent context), the field MUST use the
qualified form (`hubId`, `agentId`).

Go struct fields follow Go convention (`HubID`, `AgentID`,
`MachineRegistrationID`, `PortalID`, `ProfileID`) because gofmt
and standard Go linters require ID/URL acronyms to be all-caps in
identifiers. The JSON and YAML tags on these fields are still
camelCase, so the wire format stays consistent regardless of what
the Go side calls them internally.

---

## 5. Generation and persistence per binary

### 5.1 telahubd

`hubId` is generated on first start if absent and persisted to
`telahubd.yaml`. The generation is silent: log one line at info
level (`[hub] generated hubId: <uuid>`) and continue startup.

```yaml
# telahubd.yaml
hubId: 550e8400-e29b-41d4-a716-446655440000
port: 8080
name: "Home Cloud"
# ... rest of config unchanged
```

The field is added to the `hubConfig` struct in
`internal/hub/hub.go`. The Go field is `HubID string` with the
yaml tag `hubId` (per Principle 7: Go convention requires the
all-caps field name, the wire format stays camelCase via the
tag). It is serialized first in the YAML for human readability.

If the operator deletes `hubId` from the YAML, the next start
generates a new one. This is the documented way to rotate identity
(equivalent to "this is now a different hub, even though I am
running the same binary on the same machine"). It is intentionally
manual; there is no `telahubd rotate-id` CLI command, because
rotating identity is rare and weird and a CLI command would
suggest it is a normal operation.

### 5.2 telad

`agentId` is generated on first start if absent. It lives in a
**separate state file** alongside `telad.yaml`, not inside the
config file:

```
~/.tela/telad.yaml      # operator config (edit freely)
~/.tela/telad.state     # system state (do not edit)
```

```yaml
# telad.state
agentId: 660e8400-e29b-41d4-a716-446655440001
```

The separation is the same pattern Linux daemons use for runtime
state vs config. Operators can edit, version-control, and copy
`telad.yaml` without worrying about copying identity between
machines. The state file is created with 0600 permissions and
lives in the same directory as the YAML config.

The `agentId` is sent in the register message when telad
connects to a hub. The hub uses it as the primary identity key
for the machine record (see section 5.3).

If the operator deletes `telad.state` (or copies `telad.yaml` to
a new machine without copying `telad.state`), the next start
generates a fresh agentId, which means hubs will create a fresh
machine record for the new install. This is the documented way
to "this is a different agent now."

### 5.3 hub-side machine record

The hub-side `machineEntry` struct in `internal/hub/hub.go`
gains two new fields:

- `agentId`: the agentId the agent presented on first
  registration. Stored once, never updated. Used to recognize
  the same agent reconnecting.
- `machineRegistrationId`: a UUID the hub generates when it
  first sees a new agentId. This is the hub-local primary key for
  the machine record.

The existing `name` field (today's "machineID") becomes a display
label. It is no longer used for identity matching. Renaming a
machine in `telad.yaml` changes the displayed name on the next
register; the machineRegistrationId and agentId are unchanged,
so the hub reuses the existing record.

The hub does NOT cryptographically authenticate the claimed
agentId. Authority is established by the existing token check
(the agent must present a token authorized to register the named
machine). The agentId is metadata; an attacker who could forge
another agent's agentId would already need the corresponding
register token, which is the real authority. See section 12 for
the explicit non-goal.

### 5.4 portal

`portalId` is generated on first start if absent and persisted in
the portal's storage layer. For the file-backed store
(`internal/portal/store/file`), this means a top-level field in
`portal.yaml`:

```yaml
# portal.yaml
portalId: 770e8400-e29b-41d4-a716-446655440002
adminTokenHash: ...
hubs:
  - ...
```

Note that this stretch also renames the file store's legacy
snake_case YAML fields (`admin_token_hash`, `viewer_token`,
`sync_token_hash`, `org_name`) to camelCase
(`adminTokenHash`, `viewerToken`, `syncTokenHash`, `orgName`)
per Principle 7. The destroy-and-rebuild policy makes this free:
no migration code, since the store gets rebuilt from scratch.

For Awan Saya, the portalId lives in a new `portal_metadata`
table or a column on an existing config table; AS picks the
schema shape that fits its conventions.

The portalId is exposed in `/.well-known/tela` (see section 6.1)
so clients can identify portals across URL changes. Per the
section 10 resolved decisions, the portalId is hidden from the
TV UI by default and only surfaces in debug/diagnostics views.

### 5.5 TelaVisor profile

`profileId` is generated when a profile YAML is created (either
by the New Profile button in the Profiles tab or by the first
launch when TV creates the default profile) and stored as the
top-level `profileId` field in the profile YAML:

```yaml
# ~/.tela/profiles/Home Cloud.yaml
profileId: 880e8400-e29b-41d4-a716-446655440003
name: "Home Cloud"
mount:
  enabled: true
  mountPoint: "T:"
  port: 18080
connections:
  - hubId: 550e8400-e29b-41d4-a716-446655440000
    machineRegistrationId: aa0e8400-e29b-41d4-a716-446655440004
    services:
      - name: SSH
        local: 10022
        remote: 22
      - name: RDP
        local: 13389
        remote: 3389
```

Note that `connections[]` keys hubs and machines by their UUIDs,
not by URL or display name. This is the schema change that fixes
the wss-vs-https reconciliation bug we hit during the portal
extraction stretch. The display labels ("gohub.parkscomputing.com",
"barn") are looked up at runtime via the active portal source,
not stored in the profile.

Renaming a profile in TV updates the file name and the `name`
field; the profileId is unchanged. The TelaVisor settings
"default profile" field stores a profileId, not a name, so
renaming does not break the default-profile setting. Future
cross-device profile sync will key on profileId.

If the user deletes `profileId` from the YAML, the next load
generates a new one. (This is the same pattern as hubId
rotation: rare, manual, undocumented as a workflow.)

---

## 6. Wire-level changes (portal protocol 1.1)

This section sketches the changes that will land as an edit to
DESIGN-portal.md. Approval of this document implies approval of
the protocol bump.

### 6.1 Discovery endpoints gain identity

`GET /.well-known/tela` on a **portal** gains `portalId`:

```json
{
  "hub_directory": "/api/hubs",
  "protocolVersion": "1.1",
  "supportedVersions": ["1.1"],
  "portalId": "770e8400-e29b-41d4-a716-446655440002"
}
```

`GET /.well-known/tela` on a **hub** (a separate document with the
same path served by `telahubd`) gains `hubId`:

```json
{
  "protocolVersion": "1.1",
  "supportedVersions": ["1.1"],
  "hubId": "550e8400-e29b-41d4-a716-446655440000"
}
```

Note that hubs do not currently serve `/.well-known/tela`. This
spec adds that endpoint. It is unauthenticated and is the
canonical way for a portal to learn a hub's ID during the
register flow.

### 6.2 `/api/status` on hub gains hubId

The top-level shape adds `hubId`:

```json
{
  "hubId": "550e8400-e29b-41d4-a716-446655440000",
  "hubName": "Home Cloud",
  "machines": [...]
}
```

Each machine entry inside `machines[]` adds `agentId` and
`machineRegistrationId`, alongside the existing `id` field which
is now explicitly a display name:

```json
{
  "id": "barn",
  "agentId": "660e8400-e29b-41d4-a716-446655440001",
  "machineRegistrationId": "880e8400-e29b-41d4-a716-446655440003",
  "hostname": "BARN",
  "agentConnected": true,
  ...
}
```

The existing `id` field is preserved for display compatibility
but loses its identity role. Code that needs identity reads
`agentId` (for cross-hub correlation) or `machineRegistrationId`
(for "this hub's view of this machine").

### 6.3 Portal directory gains hubId

`GET /api/hubs` directory entries gain `id` (the hub's hubId):

```json
{
  "hubs": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "name": "gohub",
      "url": "https://hub.example.com",
      "canManage": true,
      "orgName": ""
    }
  ]
}
```

The portal learns the hubId during the register flow. For a hub
that registers itself with the portal, the hub presents its hubId
in the register request body. For a user-add (the operator types
a URL into the Add Remote dialog), the portal calls
`/.well-known/tela` on the hub, reads the hubId, and stores it.

A portal that cannot learn a hub's hubId (for example, because
the hub does not yet implement protocol 1.1) MUST refuse to add
the hub. Pre-1.0 destroy-and-rebuild policy: 1.0 hubs are dead.

### 6.4 Fleet aggregation gains agentId

`GET /api/fleet/agents` entries gain `agentId` and
`machineRegistrationId`, alongside the existing hub context:

```json
{
  "agents": [
    {
      "id": "barn",
      "agentId": "660e8400-e29b-41d4-a716-446655440001",
      "machineRegistrationId": "880e8400-e29b-41d4-a716-446655440003",
      "hub": "gohub",
      "hubId": "550e8400-e29b-41d4-a716-446655440000",
      "hubUrl": "https://hub.example.com",
      ...
    }
  ]
}
```

This is the shape TV's cross-source aggregation reads (section 7).
Note that `hub` (the hub's name) is preserved alongside `hubId`
for display. Identity is `hubId` and `agentId`; `hub` and `id`
are display labels.

### 6.5 Hub register flow

When `telahubd` registers itself with a portal via `POST /api/hubs`,
the request body gains `hubId`:

```json
{
  "name": "gohub",
  "url": "https://hub.example.com",
  "viewerToken": "...",
  "adminToken": "...",
  "hubId": "550e8400-e29b-41d4-a716-446655440000"
}
```

The portal stores it on the hub record. The hub's name and URL
can change later via `PATCH /api/hubs`; the hubId never does.

When `telad` registers with a hub via the WebSocket signaling
flow, the register message gains `agentId`. The pre-existing
`machineId` field (which was actually a user-chosen name) is
preserved as a display label:

```json
{
  "type": "register",
  "machineName": "barn",
  "agentId": "660e8400-e29b-41d4-a716-446655440001",
  "token": "...",
  ...
}
```

Note the rename: the legacy `machineId` field on the register
message becomes `machineName` to reflect what it actually is. The
hub uses `agentId` to look up an existing machine record. If none
exists, it creates one with a fresh `machineRegistrationId`. The
`machineName` field is stored on the record as a display label
and is not used for matching.

### 6.6 Version negotiation

Bump `protocolVersion` from `"1.0"` to `"1.1"` everywhere. Bump
`supportedVersions` to `["1.1"]` (single entry; we are not
maintaining backward compat). The version negotiation logic in
DESIGN-portal.md section 2 is unchanged and already handles
single-version sets correctly.

A 1.0 client talking to a 1.1 portal sees `supportedVersions:
["1.1"]` and refuses (its required version is not present). A
1.1 client talking to a 1.0 portal sees `supportedVersions:
["1.0"]` and refuses. Clean break.

---

## 7. Dedup rules in TelaVisor

This is the section the rest of the design exists to enable. With
identity in place, TV can aggregate hubs and agents across enabled
portal sources without ambiguity.

### 7.0 Shared aggregation package

The aggregation logic does NOT live in TelaVisor's `cmd/telagui`
package. It lives in a new shared package
`internal/portalaggregate` so future tools (the `tela` CLI's
planned `tela hubs ls` subcommand, TelaBoard's fleet view, any
other consumer that lists across portal sources) can reuse it.
The aggregation package depends only on `internal/portalclient`
and the protocol types in `internal/portal`.

The package exposes three types and one function:

```go
package portalaggregate

// MergedHub is one hub seen across N enabled portal sources,
// keyed by hubId. Display fields are picked per section 7.3.
type MergedHub struct {
    HubID       string         // the global identity
    Name        string         // display name picked per 7.3
    URL         string         // hub URL from highest-privilege source
    Sources     []HubSource    // every source that knows this hub
    PreferredSource string     // source name to use for admin actions
}

// HubSource records one source's view of a hub.
type HubSource struct {
    SourceName string
    Name       string  // what THIS source calls the hub
    URL        string  // what THIS source advertises
    CanManage  bool
    OrgName    string
}

// MergedAgent is one (hubId, agentId) pair seen across enabled
// sources. Two registrations of the same agent on different hubs
// are TWO MergedAgents (not one), but they share the same agentId
// and the LinkedAgentIds field exposes the relationship.
type MergedAgent struct {
    AgentID                string   // the global identity
    HubID                  string   // which hub this registration is on
    MachineRegistrationID  string
    DisplayName            string   // the per-hub display name
    Online                 bool
    Hostname               string
    OS                     string
    Services               []ServiceInfo
    LinkedAgentIds         []string // OTHER MergedAgents (different hubId, same agentId)
    Sources                []string // source names that advertise this registration
}

// Merge fetches ListHubs and FleetAgents from every client in
// `clients`, deduplicates by hubId and (hubId, agentId), and
// returns the merged views. Errors from individual clients do
// not fail the whole call; the unreachable source is recorded
// on the result so callers can mark stale rows.
func Merge(ctx context.Context, clients map[string]*portalclient.Client) (Result, error)

type Result struct {
    Hubs            []MergedHub
    Agents          []MergedAgent
    UnreachableSources []string
}
```

TelaVisor consumes this package and renders the merged shapes in
the Hubs, Agents, and Profile views. The aggregation logic stays
in one place, has its own unit tests, and is reusable.

### 7.1 Sources are enabled, not active

The current "active source" model goes away. A user can have any
number of portal sources enabled simultaneously. The Remotes tab
shows checkboxes (or toggles) for each source. The "Local"
embedded portal is always enabled and cannot be disabled.

When the user enables a new remote source (via Add Remote in the
Remotes tab), it joins the enabled set. When they disable it, TV
stops querying it but does not delete the credentials.

### 7.2 Hub aggregation by hubId

`ListHubs` is called against every enabled source. Results are
grouped by `hubId`. One row per unique hubId.

For each row, TV remembers:

- The hubId (the primary key)
- A display name (chosen per section 7.3)
- The hub's current URL (taken from the highest-privilege source
  that knows it)
- A `sources[]` list: which enabled sources advertise this hub,
  and what `canManage` they report

The Hubs tab dropdown shows one entry per hubId. A small badge
on each entry lists the source names (e.g. `Local • awansaya`).

### 7.3 Display name selection

When multiple sources call the same hub by different names, TV
picks one to display. The rule:

1. If the embedded source (Local) advertises the hub, use Local's
   name.
2. Otherwise, use the name from the source the user added first
   (sorted by `addedAt` timestamp from `portal-sources.yaml`).

The user MAY override per-hub by setting a local nickname in TV.
Local nicknames are stored in TV's settings, not in any portal,
and are private to that TV install.

### 7.4 Machine aggregation by (hubId, agentId), with linked-row UI

`FleetAgents` is called against every enabled source. Results are
keyed by `(hubId, agentId)`. One row per unique pair. A single
agent registered with two hubs appears as **two rows by default**
(one per hub), each carrying the same `agentId` and a populated
`LinkedAgentIds` list pointing at the other rows.

The hard part is making the relationship between linked rows
**very obvious** in the UI without merging them. This is a solved
problem in several mainstream apps; the resolved pattern below
borrows from the best parts of each.

#### Prior art

- **Linear cross-references** show a small chevron + count chip
  on issues that reference each other. Hover expands a popover
  listing the linked items. Linked items share a colored left
  border.
- **Notion linked databases** show a small "linked" icon next to
  entries that originate elsewhere; hovering reveals the source.
- **GitHub linked PRs/issues** show a "Linked pull requests"
  panel with each linked item carrying a small status indicator.
- **JetBrains IDE duplicate-code warnings** show a gutter icon
  on each duplicate; clicking lists the other locations and
  jumps between them.
- **Apple Mail / Outlook conversations** group threaded messages
  with a count badge; clicking expands the thread inline.
- **Plex / Jellyfin "available on N servers"** show a server-
  count badge; the badge is unobtrusive but always visible.

The common ingredients across all of these:

1. A small visible indicator on every linked instance (chip,
   badge, or icon) that shows the count of linked instances.
2. A click or hover reveals the linked instances in context.
3. A visual grouping signal (colored left border, shared
   background tint, or connecting bracket) so the relationship
   is visible at a glance during scrolling.
4. NO automatic merging; each row keeps its per-context fields.

#### TV's adopted pattern

For the Agents tab and any other view that lists machines:

1. **Link chip on every linked row.** Each row in the agents
   list with `LinkedAgentIds` non-empty gets a small inline
   chip immediately after the agent display name:

   ```
   [barn]   ⛓ Linked × 2
   ```

   The chip uses a link icon (U+26D3 broken chain or similar),
   the word "Linked", and the count INCLUDING this row (so two
   linked rows show "× 2", three show "× 3", etc.). The chip is
   clickable.

2. **Colored left border on linked rows.** Each linked group
   gets a color picked deterministically from the agentId hash
   (so "barn" on hub-A and "barn" on hub-B get the same color
   no matter what order they appear in). The color is a 3px
   left border on the row, subtle but always visible. Different
   linked groups get different colors. Unlinked rows have no
   border.

3. **Click the chip to highlight linked rows.** Clicking the
   chip on row X scrolls the next linked row into view and
   pulses its background once (300ms ease). A second click
   advances to the next linked row, cycling through the group.
   This is the JetBrains "next duplicate" pattern.

4. **Hover the chip to preview.** Hovering the chip shows a
   small popover with one line per linked row:

   ```
   ⛓ Also registered with:
      • staging  (hub-name = devhub.awansaya.net)
   ```

   No clicks needed for the user to learn what the link means.

5. **Same pattern for the Hubs tab when a hub is reachable via
   multiple sources.** A hub row with `len(Sources) > 1` gets a
   chip "Sources × 2" and the same colored left border. Hover
   shows the source names. The chip is informational; the user
   does not pick which source the row came from (the merged
   row already represents all of them).

6. **The link chip styling lives in the shared style.css** as a
   `.tdl-link-chip` class so the same chip can appear in any
   view that needs it (Hubs, Agents, the Profile editor's hub
   list, future cross-source views).

This is a TDL extension. The chip and left-border colors should
match the existing TDL accent palette; the colored borders for
linked groups pick from a small fixed palette (5-8 colors) so
the visual variety is bounded and the colors stay accessible
against both light and dark themes.

#### What this is NOT

- **NOT automatic merging.** Two linked rows stay as two rows.
  A future "collapse linked agents" toggle in TV settings could
  add an opt-in merged view, but that is a follow-up, not part
  of this stretch.
- **NOT a separate "linked agents" panel.** The links live
  inline with the rows, not in a side panel. Side panels are
  the wrong shape for this; they hide the relationship until
  the user clicks something.
- **NOT a tooltip-only signal.** The chip and the colored border
  are always visible. Hover-only signals are easy to miss and
  fail on touch devices.

### 7.5 Per-hub admin source resolution

Admin actions (token rotation, restart, channel switch, etc.)
happen against ONE source at a time. When multiple sources can
manage the same hub, TV picks which one to use:

1. **Per-hub preferred source** (user-set, persisted in TV
   settings). If set, use it.
2. **Highest-privilege source** (any source with `canManage: true`
   wins; ties broken by source priority).
3. **Source priority**: Local > first-added remote > later
   remotes. (Local wins because it has the operator's own admin
   token; remote portals may have admin tokens with narrower
   scope.)

Rule (2) is the friction-free default. Rule (1) is the power-user
override. The UI exposes (1) via a small "manage via X" dropdown
on each hub row, defaulted to whatever rule (2) picked.

### 7.6 What happens when a source disappears

If an enabled source becomes unreachable (network error, portal
down), TV keeps the cached hub and agent rows from previous calls
and marks them with a "stale: source unreachable" indicator. The
user can still view the rows but cannot perform admin actions
against the unreachable source. If a hub was only known via the
unreachable source, the row goes stale entirely; if it was also
known via a reachable source, the row stays live.

### 7.7 What happens when a hub's hubId changes

A hub's hubId never changes for the lifetime of the hub. If TV
sees a hub at the same URL with a different hubId than it had
before, that means:

- The operator destroyed and rebuilt the hub (correct: new
  identity), OR
- Two different hubs are sharing a URL (operator misconfiguration)

TV treats the new hubId as a new hub. The old row is removed
from the next refresh of the source that reported the change.

---

## 8. What renames and URL changes mean

Renames and URL changes are first-class operations. None of them
affect identity.

| Operation | Identity affected | Display affected |
|---|---|---|
| Hub operator changes the hub's display name in `telahubd.yaml` | None | Hub's `name` field on next portal sync |
| Hub operator changes the hub's URL (new domain, new port) | None | Hub's `url` field on next portal sync |
| Operator renames a machine in `telad.yaml` (changes the `machineName` field) | None | Machine's display name on next register |
| User renames a hub in a portal directory (e.g. AS web UI rename) | None | That portal's `name` for the hub; other portals unaffected |
| User sets a local nickname in TV | None | TV's display only; not propagated anywhere |
| User renames a profile in TelaVisor | None | Profile YAML file name and `name` field; the `profileId` is unchanged so default-profile and autostart references survive |
| User renames a portal in TV's Remotes tab | None | The local source nickname; other TV installs unaffected; the `portalId` is unchanged |
| Operator destroys a hub state directory and reinstalls | **New hubId** | New row in TV |
| Operator destroys a telad state file and reinstalls | **New agentId** | New row per hub the agent registers with |
| User deletes `profileId` from a profile YAML | **New profileId** | Default-profile and autostart references break (intended: this is "fork the profile") |

The bottom two rows are "this is now a different entity," which
is the intended semantics of state-directory destruction.

---

## 9. The destroy-and-rebuild migration

Pre-1.0 policy: no compat shims, no `if id == ""` fallbacks. The
existing fabric is destroyed and rebuilt. The user has explicitly
opted into this.

**This is the section the user is most anxious about, and rightly
so.** The user lives and dies by Tela; the fabric runs production
SSH/RDP/file-share access to their actual machines. Losing access
to a hub mid-rebuild is real risk. The walkthrough below is
written assuming the user follows it interactively with me at the
keyboard, asking questions at each step. I will not run any
destructive command without confirming first.

### 9.1 Pre-flight checklist (do these before any teardown)

1. **Inventory the fabric**: list every running telahubd, every
   running telad, every machine reachable via Tela today, and
   every Awan Saya hub registration. I will help generate the
   list from `/api/status` calls before we start.
2. **Identify the alternate access path** to each machine. For
   every machine that is currently only reachable via Tela,
   identify a backup access path (direct SSH on the LAN, RDP
   over the LAN, the Docker host's CLI, the home/cloud
   provider's web console, physical access). The teardown
   takes Tela offline; you need to be able to reach machines
   without it during the rebuild window.
3. **Take a full backup of `~/.tela/`** (TV's config dir) on
   the Windows workstation. tar/zip the whole tree. Same for
   `/etc/telahubd/` and `/var/lib/telahubd/` (or the Windows
   equivalents) on every hub host. Same for `/etc/telad/` (or
   the equivalent) on every agent host. The backup is the
   rollback plan: if anything goes wrong, restore the tree
   and downgrade the binary.
4. **Note the current binary version on each host.** We need
   to be able to roll back to exactly the right version per
   the rollback plan in 9.4.
5. **Verify the new binaries build cleanly** on the user's
   workstation BEFORE any teardown begins. Phase 2 through
   Phase 5 commits all land first; Phase 6 (this section) does
   not start until everything compiles.
6. **Verify Awan Saya is reachable and the user is signed in
   to its web UI** in a browser. The re-add step uses the AS
   web UI; we want to confirm the path works before taking
   things down.
7. **Pick a window with no scheduled remote work.** The
   teardown-rebuild loop on a small fabric (two hubs, two
   agents, one TV install) is probably 30 to 60 minutes if
   nothing goes wrong, longer if something does.

### 9.2 Migration sequence

I will walk through this step by step interactively. Each
numbered step waits for the user's confirmation before
proceeding. No batching, no "run these five commands and let me
know."

1. **TV is shut down.** Embedded portal stops, endpoint file
   removed. (`Stop-Process telavisor` on the workstation.)
2. **Each `telad` is stopped.** Use the OS service manager.
   Verify each one is fully stopped before proceeding to the
   next.
3. **Each `telahubd` is stopped.** Same as above.
4. **Each `telad.state` file is deleted.** This is the only
   file that needs deleting on the agent side; `telad.yaml`
   stays as-is. After this step, restarting telad would
   generate a fresh agentId.
5. **The `hubId` field is deleted from each `telahubd.yaml`.**
   This is the only edit to telahubd config. After this step,
   restarting telahubd would generate a fresh hubId.
6. **The Awan Saya hub registrations are dropped** from the
   AS database. I will help write the SQL or use the AS web
   UI delete-hub flow, whichever the user prefers.
7. **TV's portal-related files are deleted from `~/.tela/`**:
   `portal.yaml`, `portal-sources.yaml`, and any leftover
   `hubs.yaml` or `credentials.yaml` files. Profile files
   stay (we will migrate them in step 12).
8. **Each binary is upgraded** to the version that implements
   1.1. This is the version we built in Phases 2 through 5.
9. **Each `telahubd` is started.** It generates a fresh hubId,
   logs it, and starts serving. Verify each one is healthy via
   its `/api/status` endpoint before proceeding.
10. **Each `telad` is started.** It generates a fresh agentId,
    registers with its hub. The hub creates a fresh
    machineRegistrationId. Verify each agent shows up in the
    hub's `/api/status` machines list.
11. **The user re-adds each hub to AS** via the AS web UI. AS
    discovers each hub's hubId via `/.well-known/tela` and
    stores it.
12. **TV is started.** The embedded portal comes up. The
    embedded portal store is empty (we deleted `portal.yaml`),
    but TV's hubs are reachable directly because TV's embedded
    portal will have learned them from the upgraded telahubd
    via the new direct-add path. The user re-adds AS to TV via
    the Remotes tab.
13. **TV's profile YAML is migrated** by the one-shot helper.
    The helper reads `~/.tela/profiles/*.yaml`, prompts the
    user to map each `connections[].hub` URL to a hubId from
    the active sources, and writes the new shape. The helper
    is a separate Go binary that gets deleted from the working
    tree after the user runs it; it does not ship with TV.
14. **Smoke tests** per section 11 Phase 6.

### 9.3 What to expect during the window

- **Steps 1 through 7 take Tela offline.** During this window,
  every machine that depended on Tela for remote access is
  unreachable via Tela. The user uses the alternate access
  paths from 9.1 step 2 if anything needs attention.
- **Step 9 brings hubs back online but with no agents.** Each
  hub has a fresh hubId and an empty machine list.
- **Step 10 reattaches the agents to the new hub identity.**
  After step 10, the fabric is functional again, just with
  new identities.
- **Steps 11 and 12 are TV-side reconnection** and do not
  affect the fabric.
- **Step 13 unblocks the Profile editor** and is the last
  thing before the user can use TV normally again.

### 9.4 Rollback plan

If anything goes seriously wrong between steps 1 and 10, the
rollback is:

1. **Stop the upgraded binaries.**
2. **Restore the `~/.tela/` backup** on the workstation.
3. **Restore the per-hub state directory backup** on each
   hub host.
4. **Restore the per-agent state directory backup** on each
   agent host.
5. **Reinstall the old binary version** on each host (use
   the binary version recorded in 9.1 step 4).
6. **Restart everything in original order.** The fabric is
   back to where it was at the start of step 1.

The window for "restore and roll back" is the period after
step 1 and before step 10. After step 10 the fabric is on the
new identity model and rolling back means losing the new
machineRegistrationIds (the old ones are gone from the new
hub state). Rolling back from after step 10 still works but
the user has to re-pair each machine with the restored hub.
Rolling back from after step 11 (AS re-add) requires also
dropping the new AS rows.

After step 14 (smoke tests pass) there is no rollback. The
fabric is on the new model.

### 9.5 What I (the assistant) commit to

- I do not run a destructive command without confirming first
  and showing the exact command I am about to run.
- I do not "let me also do X while we're here" mid-walkthrough.
  Anything that is not in this list waits for a separate
  conversation.
- If a step fails unexpectedly, I stop and we diagnose
  together. I do not retry blindly.
- If the user asks "wait, can we go back to where we were?",
  the answer is yes (with the rollback plan from 9.4) until
  we cross step 14.
- The migration helper for step 13 is written and tested
  during Phase 5e (section 11), BEFORE we start any teardown.
  I will not write it ad hoc during the window.

---

## 10. Resolved decisions

The six open questions in the first draft of this document were
resolved by the user on 2026-04-09. The resolutions are recorded
here for traceability and so future sessions know which design
choices are locked in.

### 10.1 Telad state file location: Option B (separate state file)

`telad.yaml` is operator config; `telad.state` is system state.
The two files live side by side in the same directory. The state
file is created with 0600 permissions and contains the agentId
plus any other future per-install state. Operators can edit,
copy, and version-control `telad.yaml` without worrying about
identity collision; the state file is never copied between
machines.

Section 5.2 has the full details.

### 10.2 Same agent on multiple hubs: two rows with linked-row UI

Two rows by default, one per (hub, agent) pair. The link between
them is made obvious by the linked-row UI pattern in section 7.4:
a visible link chip on every linked row showing the count, a
deterministic colored left border on linked groups, click to
cycle between linked rows, hover to preview the linked rows.
The pattern is borrowed from Linear cross-references, JetBrains
duplicate-code warnings, and Apple Mail conversations; the
adopted shape combines the best parts of each.

A future "collapse linked agents" toggle could opt into a merged
view, but the default is two rows with the linked-row UI. The
linked-row chip styling lives in the shared style.css as a TDL
extension class so the same chip can appear in any view that
needs it (Hubs, Agents, Profile editor, future cross-source
views).

Section 7.4 has the full UX research and the adopted pattern.

### 10.3 Per-hub preferred source: visible dropdown

Each hub row in the Hubs tab gets a small "manage via X"
dropdown, defaulted to the auto-picked highest-privilege source
per the rules in section 7.5. The dropdown is always visible
(not hidden behind a right-click menu) so the relationship is
obvious without exploration. Most users will accept the default;
power users override per-hub when they need to.

### 10.4 portalId: hidden by default

The portalId is necessary for distinguishing portal instances
across URL changes, but it is not a user-facing concept. It is
hidden entirely from the normal TV UI and surfaces only in:

- Debug/diagnostics views (a future "fabric inspector" or
  similar troubleshooting screen).
- Log lines that need to identify a specific portal instance.
- Anywhere a developer needs it during implementation.

The Remotes tab shows portal NAMES (the user-set local nickname
or the host part of the URL). It does not show portalIds.

### 10.5 hubId rotation: documented but not a CLI command

Deleting the `hubId` field from `telahubd.yaml` and restarting
the hub generates a fresh hubId. This is documented in section
5.1 as the way to rotate identity, but there is no
`telahubd rotate-id` CLI command. The rationale is in section
5.1: rotating identity is rare and weird; a CLI command would
suggest it is a normal operation.

The same pattern applies to agentId rotation (delete
`telad.state`) and profileId rotation (delete `profileId` from
the profile YAML). Documented but not commanded.

### 10.6 Hub does NOT cryptographically authenticate the claimed agentId

The hub trusts whatever agentId the agent presents in the
register message. Authority is established by the existing
token-based authorization: the agent must present a token that
authorizes it to register the named machine. The agentId is
identity, not authority.

A malicious agent that forges another agent's agentId would
still need a valid register token for that machine, which is
the real authority barrier. If the attacker has the token, they
could already impersonate the machine; agentId forgery does not
make things worse.

This is recorded as an explicit non-goal in section 12. A future
spec could add cryptographic agentId binding (the agent holds a
keypair, the agentId is the hash of the public key, the hub
verifies a signature on the register message), but it is out of
scope for this stretch and would be a much larger
implementation.

---

## 11. Order of work

This stretch is large. Phases below are ordered so each one is
independently testable and reviewable. Each phase ends with a
build/vet/test/smoke-test loop and a commit.

### Phase 1 (this document)

1a. Draft `DESIGN-identity.md` (this file). **DONE.**

1b. User reviews and answers section 10 questions; the doc is
edited to bake in the answers. **DONE on 2026-04-09.** This
edited document is committed as the first commit of the stretch.

### Phase 2: telahubd and telad identity generation

2a. **telahubd hubId.** Add `HubID` field to `hubConfig`,
generate on first start, expose in `/api/status`. Add
`/.well-known/tela` endpoint to telahubd (this endpoint does
not exist on hubs today). One commit.

2b. **telad agentId.** Add agentId generation and persistence
to a separate `~/.tela/telad.state` file per section 5.2. Send
`agentId` in the register message. Rename the existing
`machineId` field on the register message to `machineName` per
section 6.5. One commit.

2c. **Hub-side machine record by agentId.** Register handler
keys machine records by `agentId` instead of `machineName`.
Generates `machineRegistrationId` on first registration of a
new agentId. Exposes both in `/api/status` machine entries.
Renames the legacy `machineID` JSON field to `machineId` in
status responses per Principle 7. One commit.

### Phase 3: portal protocol 1.1, file store rename, naming convention sweep

3a. **Spec amendment.** Amend `DESIGN-portal.md` to bump to 1.1,
add identity fields to all relevant endpoints, update the
conformance checklist, document the version negotiation
break. One commit.

3b. **File store identity + camelCase rename.** `internal/portal/store/file`:
persist `hubId` per hub and `portalId` at the top level.
Generate `portalId` on first open. Rename legacy snake_case
YAML fields (`admin_token_hash`, `viewer_token`,
`sync_token_hash`, `org_name`) to camelCase
(`adminTokenHash`, `viewerToken`, `syncTokenHash`, `orgName`)
per Principle 7. The destroy-and-rebuild policy makes this
free; no migration code. One commit.

3c. **Portal protocol implementation.** `internal/portal`:
directory entries carry `id`, status response carries `hubId`,
fleet entries carry `agentId` and `machineRegistrationId`.
Conformance tests updated to assert on identity fields.
Rejects hubs without a hubId on register. One commit.

3d. **Portal client.** `internal/portalclient`: typed shapes
carry the new fields. Round-trip tests cover identity
preservation across the wire. One commit.

3e. **Aggregation package.** New `internal/portalaggregate`
package per section 7.0. `Merge`, `MergedHub`, `MergedAgent`,
`HubSource`, `Result` types. Unit tests against synthetic
fixtures (no live portals) covering the dedup rules in
sections 7.2 through 7.6. One commit.

### Phase 4: Awan Saya 1.1

4a. **AS schema and discovery.** Add `hub_id` column to hubs
table, generate `portal_id` and store in a new
`portal_metadata` row (or existing config table; AS picks
the schema shape). Bump `/.well-known/tela` to 1.1, populate
`portalId`. One commit. (Note: AS internal SQL columns can
stay snake_case because that is the SQL convention; only
the JSON wire fields need to be camelCase.)

4b. **AS register flow.** Learn `hubId` from the register
request body and store it. Refuse 1.0 hubs (no hubId, no
add). One commit.

4c. **AS user-add flow.** When the user types a hub URL into
the Add Hub dialog, AS discovers the hub's `hubId` via
`/.well-known/tela` and stores it. Refuses hubs that do
not serve `/.well-known/tela` or that report 1.0. One
commit.

### Phase 5: TelaVisor cross-source aggregation and Profile UUIDs

5a. **Enabled-sources model.** Replace `PortalActiveSource`
with `PortalEnabledSources`. The Remotes tab gains checkboxes
per source (Local always enabled, not removable). One commit.

5b. **TV consumes the aggregation package.** `GetKnownHubs`
calls `portalaggregate.Merge` instead of querying one source.
The frontend renders `MergedHub` rows with the source badge
from the linked-row TDL class. One commit.

5c. **Agent aggregation and linked-row UI.** `GetAgentList`
calls `portalaggregate.Merge` and returns `MergedAgent` rows
including `LinkedAgentIds`. The frontend renders the link
chip and colored left border per section 7.4. New
`.tdl-link-chip` class in style.css. One commit.

5d. **Per-hub preferred source.** Per section 7.5: each hub
row has a "manage via X" dropdown defaulted to the auto-picked
highest-privilege source. Persisted in TV settings. One
commit.

5e. **Profile UUIDs.** Add `profileId` top-level field to
profile YAMLs, generate on first save of a new profile.
Migrate the `connections[].hub` URL field to
`connections[].hubId` and `connections[].machineRegistrationId`.
The TelaVisor settings "default profile" field stores a
profileId. One commit.

5f. **One-shot profile migration helper.** Standalone Go
program in `cmd/tela-profile-migrate/` that reads each old
profile YAML, prompts the user to map each `connections[].hub`
URL to a hubId from the active sources (or auto-maps where
unambiguous), and writes the new shape. The helper is
deleted from the working tree after Phase 6 confirms it
worked. One commit (and a follow-up deletion commit).

### Phase 6: destroy and rebuild (interactive walkthrough)

This phase is run interactively per section 9. The assistant
walks the user through each step, confirms before destructive
commands, and stops to diagnose if anything fails. The phase
produces no commits (it is operational, not code), but does
verify all the commits from Phases 2 through 5 work end to
end against the user's actual fabric.

6a. Pre-flight checklist (section 9.1).

6b. Migration sequence steps 1 through 14 (section 9.2).

6c. Smoke test 1: verify that registering a hub with Local and
adding it to AS too results in one row in the TV Hubs tab
with two source badges.

6d. Smoke test 2: verify that registering the same telad install
with two hubs results in two rows in the TV Agents tab, both
tagged with the same agentId, with the linked-row chip
visible and clickable.

6e. Smoke test 3: verify that renaming a hub in AS does not
duplicate the row in TV (proves hubId is the dedup key, not
name).

6f. Smoke test 4: verify that destroying a telad install and
reinstalling it produces a new row (proves agentId rotation
works).

6g. Smoke test 5: verify that renaming a profile in TV does not
break the default-profile setting (proves profileId works).

6h. Delete the migration helper from the working tree.

---

## 12. What is NOT in this document

These are deliberate omissions, either out of scope or deferred.

- **Cryptographic identity binding**: agentIds and hubIds are not
  signed. Anyone with network access to a hub can present a
  forged agentId in a register request. The existing token-based
  auth is the only barrier. Resolved as a non-goal in section
  10.6.

- **Audit trail of identity events**: this design does not specify
  how identity-related events (hub generated, agent rotated, hub
  re-registered) are logged or surfaced. That is a follow-up.

- **TelaBoard and other downstream consumers**: this document
  only specifies what telahubd, telad, the portal protocol, and
  TelaVisor need. TelaBoard and other consumers will pick up the
  new fields when they are next touched.

- **Fingerprinting concerns**: hubIds and portalIds are stable
  identifiers exposed on unauthenticated endpoints. They could
  in principle be used as fingerprints to track hub deployments
  across IP address or domain changes. We accept this; rotating
  is documented in 5.1.

- **Multi-tenant identity at the portal**: AS organizations,
  teams, and accounts already have their own identity model
  (covered in `awansatu/DESIGN-access-model.md`). This document
  does not touch that. The portal protocol's identity fields
  apply to hubs and agents only; account identity stays inside
  AS.

- **Sweeping every other YAML file in the codebase to camelCase**:
  Phase 3b renames the file store's snake_case fields because
  they are touched anyway during the identity work. Other files
  with lingering snake_case (if any) are NOT swept as part of
  this stretch; they get fixed opportunistically when next
  touched, per the cleanup pattern from the portal extraction
  stretch.

- **Cross-device profile sync**: profile UUIDs unblock this, but
  the actual sync mechanism (where profiles live, who is
  authoritative, conflict resolution) is a separate design.
  The profileId is a prerequisite, not a delivery.

---

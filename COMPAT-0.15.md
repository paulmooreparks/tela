# Compatibility Warnings for Tela 0.15

This file tracks every behavior change in the 0.15 release that needs a
loud, prominent warning for operators upgrading from 0.14. Entries here
flow into the v0.15.0 release notes, the CHANGELOG, the book's upgrade
guide, and (where appropriate) runtime warnings emitted by the binaries
on version-skew detection.

Tela is pre-1.0 so breaking changes are allowed. That does not mean they
can be silent. Every item here must be reviewed before cutting v0.15.0
stable so the release notes are complete.

Maintenance: add entries as the work lands. Each entry needs enough
detail that an operator reading only the release notes understands what
changed, why, whether they are affected, and what to do.

---

## #27 Per-service access control (connect permission gains services filter)

### What changed

Connect-permission tokens can now carry an optional `services` filter. A
token with the filter sees only the named services when it connects to
the agent; services not in the filter are invisible to the session.

- Config: `auth.machines.<name>.connectTokens[*].services: [name, ...]`
  (optional; absent means all services, matching 0.14 behavior).
- Admin API: `PUT /api/admin/access/{id}/machines/{m}` accepts a
  `services` list alongside `permissions`.
- CLI: `tela admin access grant ... connect --services <names...>` is
  the new filter syntax.

### Who is affected

Operators running a hub older than 0.15.0 while their `tela` or
TelaVisor admin client is 0.15.0+.

### Compat status

**Config compatibility: clean.** Existing `connectTokens` entries
without a `services` field continue to work exactly as before. No
migration needed.

**Admin API compatibility: silent failure risk.** An older hub
(running 0.14 or earlier) receiving a `PUT /api/admin/access/...`
request with a `services` list in the body will **silently ignore the
unknown field** and grant unfiltered access. The operator believes
they granted "Alice connect only to Jellyfin," but Alice in fact has
connect to every service on that machine.

**This is the security-relevant gotcha.** The client thinks it
restricted access; the hub effectively broadened it.

### Mitigation

Mitigation is split between design-time enforcement and the release
note itself.

1. **Client-side version check.** The 0.15+ `tela admin access grant
   --services ...` invocation must refuse to send the request unless
   the target hub advertises a sufficient capability. The hub's
   `/.well-known/tela` response includes `protocolVersion`; if it is
   below a documented threshold, the client errors out with:

   > Error: the hub at <URL> is running a version that does not support
   > per-service access control. Upgrade the hub to v0.15.0 or later,
   > or re-run the grant without --services. Running this command
   > against an older hub would silently grant all-service access.

   No `--force` flag. Operators who want the unfiltered grant run the
   command without `--services`.

2. **Release note warning.** The v0.15.0 release notes call out this
   class of failure explicitly:

   > **Security-relevant upgrade order.** If you use the new
   > `--services` filter in `tela admin access grant`, upgrade your
   > hub to 0.15.0 *first*, then your client. A 0.15.0 client talking
   > to a 0.14 hub will refuse to send service-filtered grants; this
   > is deliberate. Do not work around it by downgrading the client
   > or editing the request by hand.

3. **Book upgrade guide** (issue #44 when it lands) repeats the
   same warning in the access-model chapter.

### Status

- [ ] Client-side version check implemented in `tela admin access grant`
- [ ] Release-note boilerplate drafted for v0.15.0
- [ ] Book chapter updated to describe the new filter and the upgrade
      order

---

<!-- Add additional compat entries below as the 0.15 cycle lands more
     schema or behavior changes. Template:

## #<issue> <short title>

### What changed

### Who is affected

### Compat status

### Mitigation

### Status

- [ ] ...
-->

# PR B Mockups: Session-Token UI in TelaVisor's Access Tab

These are static HTML mockups for the TelaVisor follow-up to PR #75 (#24
backend). They render with `cmd/telagui/frontend/src/style.css` so the
chrome matches the live app exactly. They're throwaway: they render
real markup against the real stylesheet to make the design legible
before wiring it into `app.js` and `index.html`.

## Files

| File | Surface |
|------|---------|
| `01-identity-rail.html` | Access page **By identity** view: rail with new status pills, detail header with **Revoke** button, expiry/issued meta line |
| `02-add-identity-modal.html` | Add Identity modal with the new optional **Expires** field |
| `03-revoke-confirm-modal.html` | Confirmation modal for the Revoke action |

Open any file directly in a browser. They each `<link>` the live
stylesheet via a relative path.

## What the mockups show

### Status pill on the identity rail

Active identities render unchanged. Identities with `revokedAt` set
get a `chip-cap-no` (red) "revoked" pill. Identities with `expiresAt`
in the past get the same pill labelled "expired". Identities with a
future `expiresAt` get a muted `chip-cap-info` pill labelled
"expires Apr 30" (short date) so an upcoming expiry is visible at a
glance from the rail.

### Identity detail header

A new **Revoke** button sits between **Rotate token** and **Delete
identity**, styled `btn btn-sm` (the same neutral chrome as Rotate;
Revoke is reversible via Rotate, so it does not warrant `btn-danger`
styling). When the identity is already revoked, the same button
renders as **Revoked** in muted form and is disabled, with the meta
line carrying a "revoked Apr 28 14:02 UTC" line and a hint pointing
the operator at Rotate to re-enable.

A new meta line under the token preview shows:

- `issued <relative-date>` (always)
- `· expires <date>` (when `expiresAt` is set)
- `· revoked <relative-date>` (when `revokedAt` is set, with
  line-through on the role chip and token preview)

### Add Identity modal

A new optional **Expires** input under Role accepts the same shapes
the CLI accepts: empty (no expiry), an RFC 3339 timestamp, or a
relative shorthand (`30d`, `4w`, `1y`). A small `.hint` line under
the input documents the accepted formats. The value is parsed
client-side and sent as `expiresAt` (RFC 3339 string) on the
`AdminCreateToken` call.

### Revoke confirmation modal

Standard themed modal (per the no-web-dialogs rule). Body explains
that the entry stays in config so the audit trail is preserved, and
that Rotate token re-enables a revoked identity. Last-owner case is
handled by the backend (409); the frontend surfaces the response
message in the modal's error state if the call fails.

## Out of scope for the PR

- Token-list export (audit-trail CSV) — already shipped in v0.15 and
  doesn't change.
- A separate "unrevoke" verb — rotation is the unrevoke path per the
  backend design.
- Hub-config-file YAML migration UX — happens silently on first load.

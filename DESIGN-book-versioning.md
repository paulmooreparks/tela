# Book versioning across release channels: design

This document specifies how the Tela documentation site at
`https://paulmooreparks.github.io/tela/` is split into three independently
versioned editions, one per release channel, so that every user reading the
docs reads the edition that matches the binary they are actually running.

Narrative user-facing documentation for this feature is not required: the
behavior is visible in the URL layout and the banner at the top of each
non-stable edition. This document speaks to maintainers and to whoever
owns the release process.

---

## 1. Motivation

The book is deployed by `.github/workflows/docs.yml` on every push to
`main`. One branch, one build, one deploy. The site at
`https://paulmooreparks.github.io/tela/` always reflects the tip of `main`.

This produces two recurring problems:

1. **The label lies.** `book/src/introduction.md` and
   `book/src/sidebar-version.js` carry hand-edited version strings
   (`v0.12.0` today). The release process updates them only at the moment
   a stable tag is cut. Between stable cuts, `main` is running ahead on the
   next dev cycle, so the book is labeled `v0.12.0` while the content
   describes `0.13` behavior. Any user landing on the site during that
   window reads docs for a version that is not yet released.
2. **Stable users get dev docs.** A user running `v0.12.0` stable who
   reads the book today sees documentation that has moved on to describe
   `0.13`-series behavior, including features they do not have and
   defaults that do not apply to them. There is no way for a stable user
   to reach the documentation that matches their binary.

The root cause is that the book is a single artifact built from a single
ref, and the release channels that govern the binaries do not govern the
docs.

The fix is to produce three independent book editions, one per channel,
deployed to three URLs on the same GitHub Pages site, with each deploy
triggered by the event that defines that channel.

## 2. Goals

- Stable is the default: `https://paulmooreparks.github.io/tela/` always
  serves the stable edition, unchanged from today's behavior for the
  majority of readers.
- Beta and dev are special cases, reachable at predictable URLs:
  `https://paulmooreparks.github.io/tela/beta/` and
  `https://paulmooreparks.github.io/tela/dev/`.
- Each edition's version label is correct at build time. No hand-edited
  version strings in source.
- Promotion moves the docs with the binary. When dev promotes to beta,
  the beta edition updates. When beta promotes to stable, the stable
  edition updates.
- A deploy to one channel never clobbers another channel's edition.
- Users landing on a non-stable edition know it, so a confused user on
  the beta URL does not file a bug against behavior that only exists in
  beta.

## 3. Non-goals

- No deep cross-edition navigation. A page in the stable edition does
  not link to the same page in the dev edition. Each edition is
  self-contained at the page level. Top-level cross-edition
  discoverability is provided by the edition switcher in the sidebar
  footer (see section 7).
- No per-channel search index merge. Each edition has its own search
  index, scoped to its own content.
- No archive for dev or beta. Every dev and beta tag overwrites its
  channel's edition. Only stable releases are archived (see section 4.2).

## 4. URL layout

GitHub Pages serves whichever file matches the request path. The
`gh-pages` branch holds the published tree directly:

```
gh-pages/
  index.html           <- current stable book entry point
  book/...             <- current stable book assets
  introduction.html
  (every other current-stable book file)
  beta/
    index.html         <- beta book entry point
    (every other beta book file)
  dev/
    index.html         <- dev book entry point
    (every other dev book file)
  archive/
    index.html         <- landing page listing archived stable editions
    v0.12.0/           <- frozen copy of the v0.12.0 stable book
    v0.13.0/           <- frozen copy of the v0.13.0 stable book
    ...
```

Requests resolve:

| Request | Served from |
|---|---|
| `…/tela/` | `gh-pages/index.html` (current stable) |
| `…/tela/getting-started/install.html` | `gh-pages/getting-started/install.html` (current stable) |
| `…/tela/beta/` | `gh-pages/beta/index.html` (beta) |
| `…/tela/beta/getting-started/install.html` | `gh-pages/beta/getting-started/install.html` (beta) |
| `…/tela/dev/` | `gh-pages/dev/index.html` (dev) |
| `…/tela/archive/` | `gh-pages/archive/index.html` (archive menu) |
| `…/tela/archive/v0.12.0/` | `gh-pages/archive/v0.12.0/index.html` (frozen v0.12.0 stable) |

Each edition is a complete, independent mdBook output. There is no
redirect layer and no shared root. A reader who bookmarks
`…/tela/getting-started/install.html` keeps reading the current stable
docs as stable is promoted forward. A reader who bookmarks
`…/tela/archive/v0.12.0/getting-started/install.html` keeps reading the
v0.12.0 docs forever.

### 4.1 `site-url` per edition

mdBook's `output.html.site-url` controls how absolute paths resolve
inside generated HTML (e.g., `<link rel="stylesheet" href="/tela/…">`).
Each edition must be built with a `site-url` that matches its deploy
location:

| Edition | `site-url` |
|---|---|
| stable (current) | `/tela/` |
| beta | `/tela/beta/` |
| dev | `/tela/dev/` |
| archived stable | `/tela/archive/vX.Y.Z/` |

This is set at build time by the docs workflow via
`MDBOOK_OUTPUT__HTML__SITE_URL`, overriding whatever is in `book.toml`.
Source-controlled `book.toml` stays on `/tela/` (stable) so local
`mdbook serve` keeps working for authors.

### 4.2 Stable archive

Every stable release is archived under `/tela/archive/vX.Y.Z/` at the
moment it is cut, in addition to becoming the new root edition. The
archive is write-once: once written, a version directory is never
touched again.

Archive contents are a full mdBook output, independent of the root
edition, built with `site-url = /tela/archive/vX.Y.Z/` so internal
links resolve inside the frozen subtree. An archived edition carries
no channel banner (it is a stable release) and its version label shows
the tag it was built from.

An `/tela/archive/index.html` landing page lists every archived
version, newest first, with release date and a link. The page is
regenerated by the stable-deploy job of `docs.yml` by enumerating
`gh-pages/archive/v*/` directories before deploy. Authoring happens in
the workflow, not in `book/src/`: the archive index is a site
artifact, not a book chapter.

Only stable tags archive. Dev and beta tags overwrite their rolling
edition and leave no history on the site.

#### 4.2.1 Size budget and headroom

GitHub Pages soft-limits a site to 1 GB. A full book build is
currently around 15-25 MB, driven mostly by screenshots. At 25 MB per
archived edition, headroom is roughly 40 stable releases before the
warning threshold. At Tela's expected release cadence, that is many
years of runway.

Monitoring is manual: when `gh-pages` approaches 500 MB, re-evaluate.
Likely mitigations, in order of least to most effort:

1. Prune pre-1.0 archives once 1.0 ships (they lose reference value).
2. Share a screenshot asset tree across archives (higher effort,
   requires rewriting image paths in each archived build).
3. Move archived editions off GitHub Pages to a separate static host,
   leaving only a redirect list at `/archive/`. The operator has
   other hosting available, so this is the fallback if the site
   outgrows GitHub Pages.

None of these are near-term concerns; the design keeps all archives on
GitHub Pages until the site size warrants action.

## 5. Build and deploy pipeline

The book is rebuilt and redeployed on three kinds of event, each
writing only to its own subtree on `gh-pages`:

| Trigger | Ref used | Edition built | Deploy target |
|---|---|---|---|
| Push of tag matching `v[0-9]+.[0-9]+.[0-9]+` (stable) | that tag | stable (twice) | `gh-pages` root AND `gh-pages/archive/vX.Y.Z/` (both preserve siblings) |
| Push of tag matching `v*-beta.*` | that tag | beta | `gh-pages/beta/` |
| Push of tag matching `v*-dev.*` | that tag | dev | `gh-pages/dev/` |

All three are handled by a single workflow, `.github/workflows/docs.yml`,
which replaces the current docs workflow. The `main`-push trigger is
dropped: docs no longer deploy on every commit to `main`, because every
commit to `main` already cuts a dev tag (via `release.yml`), and that
dev tag is what triggers the dev book build.

The stable trigger runs the build step twice (once with
`site-url = /tela/`, once with `site-url = /tela/archive/vX.Y.Z/`),
deploys the root copy, deploys the archive copy, and regenerates
`/archive/index.html` from the resulting directory listing. All three
writes occur in a single workflow run so the archive index always
matches what is on disk.

### 5.1 Why trigger on tags, not on branch pushes

Tying docs to tags keeps the three editions in lockstep with the three
binary channels. The stable edition can never drift ahead of the stable
binary, because the tag that publishes the stable binary is also the
tag that publishes the stable book. Promotion (dev → beta or beta →
stable) creates a new tag that points at the same commit, so
`promote.yml` pushing a new tag kicks `docs.yml` the same way it kicks
`release.yml`. Docs move with the binary, by construction.

It also means `docs.yml` never runs off `main` directly. A typo
committed to `main` does not reach the site until that commit is cut as
a dev tag, at which point it lands in `/dev/` only.

### 5.2 Sibling preservation

Each deploy writes to a specific subdirectory and must leave the other
editions alone. With the `peaceiris/actions-gh-pages` action, this is
`keep_files: true` plus `destination_dir: beta` (or `dev`, or
`archive/vX.Y.Z`) for the channel and archive builds. For the stable
root build, `destination_dir` is unset (root) and `keep_files: true`
preserves `beta/`, `dev/`, and `archive/`.

Without `keep_files: true`, the default behavior wipes everything that
was not in the current upload, which would destroy every other edition
on every deploy.

## 6. Version labeling at build time

The hand-edited strings in `introduction.md` and `sidebar-version.js`
are removed and replaced by placeholders that the workflow substitutes
at build time from the tag it is building.

### 6.1 Placeholder conventions

- `book/src/introduction.md` replaces its current line
  `*This edition documents Tela v0.12.0.*` with
  `*This edition documents Tela __TELA_BOOK_VERSION__.*`
- `book/src/sidebar-version.js` replaces
  `footer.textContent = 'v0.12.0';` with
  `footer.textContent = '__TELA_BOOK_VERSION__';`

A small preprocessing step in `docs.yml` runs `sed -i` against these
files before `mdbook build`, substituting `__TELA_BOOK_VERSION__` with
the tag name that triggered the run (for example `v0.13.0-beta.3`).

The placeholders live on `main`. No human edits them. The old release
process step of "update the version string before cutting stable" goes
away; `RELEASE-PROCESS.md` is amended to reflect that.

### 6.2 Tag name format in the book

The label shows the raw tag with its leading `v`, as the binaries do
when they print their version. Examples:

- stable: `Tela v0.13.0`
- beta: `Tela v0.13.0-beta.3`
- dev: `Tela v0.14.0-dev.42`

No special formatting per channel. The channel banner (next section)
carries the "this is beta/dev" signal.

## 7. Channel banner and edition switcher

Two separate mechanisms carry cross-edition awareness: a banner at the
top of non-stable editions (signals context), and an edition switcher
in the sidebar footer of every edition (enables navigation).

### 7.1 Channel banner (non-stable editions only)

Beta, dev, and archived-stable editions carry a banner at the top of
every page explaining what they are. The current stable edition has no
banner; it is the default and needs no disclaimer.

The banner is injected by `book/src/channel-banner.js`, gated on a
templated flag substituted at build time:

```js
// channel-banner.js  (source, before substitution)
(function () {
    var channel = '__TELA_BOOK_CHANNEL__'; // 'stable', 'beta', 'dev', or 'archive'
    if (channel === 'stable') return;
    // inject a styled <div> at the top of .content with a per-channel message
})();
```

For beta:

> You are reading the **beta** documentation for Tela v0.13.0-beta.3.
> The stable release is documented at
> [paulmooreparks.github.io/tela](https://paulmooreparks.github.io/tela/).

For dev:

> You are reading the **dev** documentation for Tela v0.14.0-dev.42.
> This describes behavior that has not yet shipped in a release. The
> stable release is documented at
> [paulmooreparks.github.io/tela](https://paulmooreparks.github.io/tela/).

For archived stable:

> You are reading the archived documentation for Tela v0.12.0, a
> previous stable release. The current stable release is documented at
> [paulmooreparks.github.io/tela](https://paulmooreparks.github.io/tela/).

Styling follows the Tela Design Language: warning-amber left border on
dev, heads-up-blue left border on beta, neutral-grey left border on
archive. CSS lives in `book/src/channel-banner.css`. Both files are
added via `additional-css`/`additional-js` in `book.toml`.

### 7.2 Edition switcher (every edition, including stable)

Every edition's sidebar footer carries a small "Other editions" block
below the version string:

```
v0.13.0
Other editions: beta · dev · archive
```

The block lives in `book/src/sidebar-version.js` (renamed conceptually
to the sidebar-footer script). It renders three links:

| Link | Target |
|---|---|
| `stable` (shown on beta/dev/archive editions) | `/tela/` |
| `beta` (shown on stable/dev/archive editions) | `/tela/beta/` |
| `dev` (shown on stable/beta/archive editions) | `/tela/dev/` |
| `archive` (shown on every edition) | `/tela/archive/` |

Links are absolute paths rooted at `/tela/` so they work identically
regardless of which edition the reader is currently in, and regardless
of which page they are on (no per-page mapping required). The switcher
lands on each target edition's index page. From there the reader
navigates within that edition as usual.

The switcher omits the link to the current edition (for example, no
"stable" link when the reader is already reading stable). This is
driven by the same `__TELA_BOOK_CHANNEL__` flag used by the banner, so
all per-edition variation lives in two script files and is substituted
at build time.

The switcher is deliberately low-key: small font, grouped with the
version string. Readers who want to compare editions can find it;
readers who do not want to think about channels can ignore it. This
parallels the convention in Rust and Python docs, which expose edition
pickers in a similar footer position.

## 8. Landing page and root

Because stable lives at the root, there is no separate landing page
needed. `…/tela/` IS the stable book's introduction, as it is today.

Discoverability of the other editions is provided by the sidebar-footer
edition switcher (section 7.2), not by content inside the introduction.
Readers who want beta, dev, or an archived release find the link in the
sidebar footer of every page; readers who do not care about channels
are not distracted by banner text inside the intro prose.

The `…/tela/archive/` URL serves a generated index page listing every
archived stable edition with release date. That page is the canonical
entry point for users looking for older documentation; the sidebar
switcher links to it as well.

## 9. First deploy and bootstrap

On the first run after this design lands, the `gh-pages` branch looks
like today's site: stable content at the root, no `beta/`, no `dev/`.

Bootstrap sequence:

1. Merge the `docs.yml` rewrite, the placeholder changes in
   `introduction.md` and `sidebar-version.js`, and the channel-banner
   assets.
2. Push a dev tag (`v0.14.0-dev.N+1`) on the next commit to `main`. The
   new workflow builds the dev edition and deploys to `gh-pages/dev/`.
3. Promote the current beta (or cut a fresh one if none exists). The
   workflow builds the beta edition and deploys to `gh-pages/beta/`.
4. Promote the next stable when it is ready. The workflow rebuilds the
   stable edition at the root with the new label.

After bootstrap, all three editions are live and stay live, each
updated by its own channel's tag pushes.

### 9.1 Backfill the current stable

Today's live stable content is `v0.12.0` material, which is correct but
hand-labeled. The first stable deploy under this design happens on the
next stable tag (`v0.13.0`). Between the design landing and `v0.13.0`
cutting, the root URL still shows the current (correctly-labeled-for-
v0.12.0) site.

If we want to retrofit the current stable URL to be workflow-built
rather than hand-edited, we can push a `workflow_dispatch` run against
tag `v0.12.0` immediately after merging the design. That rebuilds the
stable edition from the `v0.12.0` source tree, with the workflow
substituting `v0.12.0` into the placeholders and omitting the banner.
The source tree at `v0.12.0` does not yet have the placeholders, so
this retrofit requires either a patch release on top of `v0.12.0` or
accepting that the retrofit builds against a tree that predates the
placeholder change (the `sed` does nothing, and the hardcoded
`v0.12.0` remains, which happens to be correct). The latter is
acceptable; we do not need a patch release for a one-time retrofit.

## 10. Interaction with release.yml and promote.yml

`release.yml` already builds on tag pushes. It is not affected by this
design; it keeps doing what it does.

`promote.yml` pushes a new tag pointing at the same commit as its
source tag. That tag push fires both `release.yml` (via `workflow_call`
from inside `promote.yml`) and `docs.yml` (via its own
`on.push.tags` trigger). Docs and binaries move together.

No changes to either workflow are strictly required for this design.
`docs.yml` is the only file that changes.

## 11. Release process changes

`RELEASE-PROCESS.md` currently mentions updating `introduction.md` and
`sidebar-version.js` as a manual step before cutting stable. That step
is removed. The placeholders are now permanent; the workflow does the
substitution. This is the only release-process change.

The broader point, already in `RELEASE-PROCESS.md`, that promotion is
manual and a maintainer decides when to cut stable, is unchanged.

## 12. Open questions

1. **Search across channels.** A reader on stable who searches for a
   feature that only exists in dev gets no results. Is that correct?
   (The design says yes: stable-only search keeps the stable book
   self-consistent.) If we want cross-channel search, it is a separate
   feature requiring a merged Lunr index, and is explicitly out of
   scope here.
2. **Dev edition retention on dead commits.** If a dev tag cut from
   `main` is later reverted (the feature is backed out), the dev edition
   still shows the reverted content until the next dev tag is cut. Is
   that acceptable? (Probably yes: the next dev tag is typically within
   hours, and the reverted content is obviously-provisional by being on
   the dev edition.) No action needed unless this becomes a problem.
3. **Hotfix branches.** Hotfix branches (like `hotfix/v0.13.x`) cut
   `v0.13.0-beta.N` tags that advance the beta book. When the hotfix
   beta is promoted to `v0.13.1` stable, the stable book advances to
   match. This is the intended flow; it is noted here because hotfixes
   are the only case where the beta and stable editions might describe
   nearly-identical content with a trivial version delta.
4. **Search-engine indexing.** Search engines that already indexed the
   old root URL's content will continue to hit the stable URL as the
   stable edition evolves. Dev and beta are unlikely to be indexed
   aggressively. If this becomes a problem (engines surfacing beta docs
   to users searching for stable features), add `<meta name="robots"
   content="noindex">` to beta and dev editions via a workflow
   substitution. Not doing that today; flag only.

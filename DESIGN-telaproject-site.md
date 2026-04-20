# telaproject.org: project home site

This document is the design for the Tela project's permanent public home at
`https://telaproject.org/`, targeted to ship with `v1.0.0`. It covers DNS
and custom-domain setup, URL layout, the landing-page content and toolchain,
the version map, required changes to the existing book pipeline, source-tree
layout, and the one-time migration from `paulmooreparks.github.io/tela/`.

The outcome: a proper project home page with a hero, value proposition,
download CTA, version map, and community pointers at `/`; the book and
its per-channel editions continue to live at `/book/` and its subpaths
under the same domain, sharing one `gh-pages` branch.

---

## 1. Motivation

Tela currently has no public home. `paulmooreparks.github.io/tela/` serves
the stable book's introduction chapter as if it were the project's front
page. A reader landing there sees `# Introduction` and a table of tiers,
not a project overview, not a download link, not a list of what documentation
exists, not a way to get from "I heard about Tela" to "I have it running."

The book is excellent at what it is. It is bad at being a landing page:

- The front page is a chapter, not a home. There is no hero, no CTA, no
  "here is Tela in three sentences and how to try it."
- There is no download surface. Users have to navigate into the book to
  find that binaries exist and where they come from.
- The version map is an internal concern of the docs site
  (`/archive/index.html`), invisible unless a reader types that URL.
- The URL carries the maintainer's personal GitHub handle, which is not
  the project's identity.

`telaproject.org` was registered to be that home. It lets us:

1. Separate "the Tela project" from "Paul's personal GitHub," which is
   the correct long-term identity for an OSS project that hopes to outlive
   its founder's attention.
2. Put a real landing page at `/` that sells the project in 10 seconds
   and routes readers to download, docs, or source.
3. Give the book a proper URL (`/book/`) and let the version map become
   a first-class navigation element.
4. Add other project surfaces over time (release-notes page, download
   page, a community page, a hosted public hub status page, whatever)
   without reshaping URLs later.

`v1.0.0` is the correct cut because:

- Pre-1.0, URLs are allowed to break. Post-1.0 they become external
  contract and must not move. Moving the book from `/` to `/book/` is
  trivial now and impossible after.
- A 1.0 launch deserves a landing page. Hacker News, Reddit, and
  every other attention-surface will link to the home URL first, the
  docs URL second. That home URL should be `telaproject.org/`, not a
  book chapter.

---

## 2. Goals

- `telaproject.org/` serves a landing page whose sole purpose is to
  convert an interested reader into a running binary or an informed
  reader, in that order.
- `telaproject.org/book/` serves the current stable book. The book's
  per-channel editions live at `/book/beta/`, `/book/dev/`, and
  `/book/archive/vX.Y.Z/`, preserving the DESIGN-book-versioning model.
- The landing page carries a visible version map: current stable, current
  beta, current dev, and a link to the archived stable editions.
- The landing page includes a download CTA for the current stable binary
  on each supported platform.
- The site is visually part of the Tela family (follows TDL).
- The site is fully static. No runtime dependencies, no build server,
  no CMS, no analytics beyond whatever GitHub Pages offers.
- Migration from `paulmooreparks.github.io/tela/` is handled by GitHub
  Pages' automatic custom-domain redirect. External links surviving from
  pre-1.0 will 404 on a specific book page (book moved from `/` to
  `/book/`), which is acceptable pre-1.0 and locked in post-1.0.
- HTTPS is automatic via Let's Encrypt, provisioned by GitHub Pages.

## 3. Non-goals

- No multi-repo aggregation. `telaproject.org/` is served from the `tela`
  repo's `gh-pages` branch. Awan Saya, TelaBoard, and other projects
  that may someday live under the Tela umbrella do not publish to
  `telaproject.org/*` from this design. Their homes live elsewhere
  (their own subdomains, their own repos' Pages sites, etc.).
- No framework. The landing page is hand-written HTML and CSS. No
  static-site generator (no Hugo, no Astro, no Next), no build step
  beyond the sed pass already in `docs.yml` and a copy of
  `site/*` into `gh-pages`.
- No analytics or tracking code. If we want download counts, we get them
  from GitHub Release download statistics; if we want traffic numbers
  we use the Pages traffic graph.
- No redirect layer for old `paulmooreparks.github.io/tela/*` URLs.
  GitHub auto-redirects the domain, which covers the canonical move;
  individual book pages that moved into `/book/` will 404 during the
  transition. Pre-1.0 breakage is acceptable.
- No separate "www" subdomain as canonical. `telaproject.org/` is
  canonical; `www.telaproject.org/` redirects to the apex.
- No subdomain split for the book (`book.telaproject.org`). Keeping
  everything under one apex simplifies DNS, TLS, and cross-linking.

## 4. Domain and DNS

### 4.1 GitHub Pages custom-domain configuration

One field change in the repo:

- Settings → Pages → Custom domain: `telaproject.org`
- Enforce HTTPS: checked

This writes a `CNAME` file containing `telaproject.org` into the
`gh-pages` branch, which GitHub Pages uses as the domain binding. The
file is preserved across deploys because the docs workflow uses
`peaceiris/actions-gh-pages` with `keep_files: true`.

### 4.2 DNS records at the registrar

Two records at the domain registrar:

| Host | Type | Value | Purpose |
|------|------|-------|---------|
| `@` (apex, `telaproject.org`) | `A` (x4) | `185.199.108.153`, `185.199.109.153`, `185.199.110.153`, `185.199.111.153` | Points apex at GitHub Pages' anycast edge |
| `www` | `CNAME` | `paulmooreparks.github.io` | Points www at Pages; GitHub redirects to canonical apex |

If the registrar supports `ALIAS` / `ANAME` on the apex, use that instead
of the four A records with value `paulmooreparks.github.io`. Behavior is
identical; ALIAS is just less fragile if GitHub changes its edge IPs.

### 4.3 TLS certificate

GitHub Pages provisions a Let's Encrypt certificate for both `telaproject.org`
and `www.telaproject.org` automatically once DNS resolves correctly. This
takes up to 24 hours on first setup; during that window the site works
over HTTP and the padlock is absent. The "Enforce HTTPS" toggle becomes
available after provisioning completes; flip it then.

## 5. URL layout

After migration:

| URL | Served from | Notes |
|-----|-------------|-------|
| `telaproject.org/` | `gh-pages:/index.html` | Landing page (new) |
| `telaproject.org/book/` | `gh-pages:/book/index.html` | Current stable book |
| `telaproject.org/book/beta/` | `gh-pages:/book/beta/index.html` | Current beta book |
| `telaproject.org/book/dev/` | `gh-pages:/book/dev/index.html` | Current dev book |
| `telaproject.org/book/archive/` | `gh-pages:/book/archive/index.html` | Archive landing |
| `telaproject.org/book/archive/v1.0.0/` | `gh-pages:/book/archive/v1.0.0/index.html` | Archived stable |
| `telaproject.org/versions.json` | `gh-pages:/versions.json` | Machine-readable version map (see §7) |
| `www.telaproject.org/*` | Redirect | GitHub Pages auto-redirects to apex |
| `paulmooreparks.github.io/tela/*` | Redirect | GitHub Pages auto-redirects to `telaproject.org/*` |

### 5.1 `site-url` per edition

Each edition's `site-url` changes to match the new mount:

| Edition | `site-url` (before) | `site-url` (after) |
|---------|---------------------|---------------------|
| stable | `/tela/` | `/book/` |
| beta | `/tela/beta/` | `/book/beta/` |
| dev | `/tela/dev/` | `/book/dev/` |
| archived stable | `/tela/archive/vX.Y.Z/` | `/book/archive/vX.Y.Z/` |

The existing edition-switcher links in `sidebar-version.js` also change:

| Link | `href` (before) | `href` (after) |
|------|-----------------|-----------------|
| stable | `/tela/` | `/book/` |
| beta | `/tela/beta/` | `/book/beta/` |
| dev | `/tela/dev/` | `/book/dev/` |
| archive | `/tela/archive/` | `/book/archive/` |

And the channel-banner's "stable release" link:

```
'<a href="/book/">telaproject.org/book</a>'
```

All of these are mechanical substitutions in `.github/workflows/docs.yml`
and the two `book/src/*.js` files. No runtime behavior changes.

## 6. Landing page

### 6.1 Content sections

The landing page is a single scrollable HTML document with these
sections in order:

1. **Topbar.** Tela wordmark (left), navigation links (right): "Docs",
   "Download", "GitHub", "Release notes". Sticky on scroll.
2. **Hero.** One sentence tagline (see §6.4 for copy). Primary CTA
   button: "Download v1.0.0" (deep-linked to current-stable release
   asset on the current platform, detected from User-Agent). Secondary
   CTA: "Read the book".
3. **What Tela is.** One paragraph, three bullets. Derived from
   `README.md`'s top section. Kept short.
4. **Three binaries.** A compact card grid: `tela` (client), `telad`
   (agent), `telahubd` (hub). Each card: one-line description, a
   "Docs →" link deep into the book.
5. **Version map.** A three-column panel showing current stable,
   beta, and dev, each with version number and a "Read docs" link.
   Archive link below (full list of past stables).
6. **Download.** Platform picker (Windows, macOS, Linux) with direct
   download links for the current stable binaries. Shows SHA256. Links
   to package-manager instructions (winget, brew, apt, etc.) once those
   land.
7. **Community and source.** Three links: GitHub repo, issues, discussions
   (or wherever community conversation happens). Small, quiet.
8. **Footer.** License (GPL v3 or whatever), copyright line, link to
   privacy/terms if any.

No cookie banner (no cookies used). No "subscribe to our newsletter."

### 6.2 Source-tree layout

A new directory `site/` at the repo root, sibling to `book/`:

```
site/
  index.html
  style.css          <- landing-page styles (TDL-derived)
  assets/
    logo.svg
    screenshot-telavisor.png
    ...
```

Source controls the shape; the docs workflow copies `site/*` into
`gh-pages` root alongside the stable book's `site-url`-adjusted output.

### 6.3 Tech stack

Plain HTML and CSS. No JavaScript framework. One small script file
(`site/versions.js`) fetches `/versions.json` and hydrates the
version-map panel. The same script drives User-Agent-based platform
detection for the download CTA. Graceful degradation: if JS is disabled,
the version map falls back to generic "see /book/archive/ for the list"
text and the download CTA links to the GitHub Release page.

### 6.4 TDL extraction

The landing page becomes the third consumer of the Tela Design Language,
after TelaVisor and Awan Saya. TDL is currently defined as a
specification (`TELA-DESIGN-LANGUAGE.md`) plus a self-contained reference
renderer (`cmd/telagui/mockups/tdl-reference.html`); each consumer
reimplements the spec in its own stylesheet. Two consumers was workable;
three is where copy-paste stops.

This work therefore includes extracting a canonical shared stylesheet
at `site/tdl.css` (or a more neutral location if another home makes
sense, see open question below). The canonical file:

- Implements every primitive named in `TELA-DESIGN-LANGUAGE.md`:
  `.btn`, `.status`, `.chrome-btn`, `.brand-link`, plus cards,
  typography, color tokens, and spacing scale.
- Contains only TDL primitives. Application-specific styles remain in
  each consumer's own stylesheet (`site/style.css` for the landing
  page, TelaVisor's frontend stylesheet, Awan Saya's stylesheet).
- Is copy-free: every consumer `<link>`s or imports it directly. If
  consumers cannot share a URL (TelaVisor runs offline; Awan Saya runs
  on a different host), each vendors a snapshot and tags the version
  they copied from.

The landing page's CSS therefore splits into two files:

- `site/tdl.css`: canonical TDL primitives (new, extracted from the
  spec and the TelaVisor/Awan Saya implementations).
- `site/style.css`: landing-page-specific layout, grid, hero copy,
  version-map panel, footer.

After extraction, TelaVisor and Awan Saya should migrate off their
copies to `tdl.css` on their own schedules. That migration is not
blocking for the landing page: the extracted file is the source of
truth from day one, and the consumers catch up as they get touched
next. The book's `channel-banner.css` is small enough (~30 lines)
that it can fold into `tdl.css` as a TDL primitive (`.banner`) in
the same work; or remain as-is if the scope gets unwieldy.

**TDL-to-landing mapping** (the four categories map cleanly; disjoint-
location rule holds):

| TDL primitive | Landing-page usage |
|---|---|
| `.brand-link` | Topbar "Tela" wordmark (top-left) |
| `.chrome-btn` | Topbar nav ("Docs", "Download", "GitHub", "Release notes") |
| `.btn` | Hero CTAs; per-platform download buttons; card "Docs →" links |
| `.status` | Version-map pills (current stable / beta / dev) |
| Card, typography, tokens | "What Tela is" section, three-binaries cards, footer |

**Open question on home for `tdl.css`:**
The extracted file could live under `site/`, under a neutral top-level
`tdl/` directory, or under `cmd/telagui/tdl/` (co-located with the
reference renderer). Arguments:

- `site/tdl.css` is fine if the landing page is the only static-HTML
  consumer; TelaVisor and Awan Saya would import from a URL or vendor
  a copy.
- `tdl/tdl.css` at the repo root signals "this is a shared contract,
  not owned by any one app," at the cost of one more top-level
  directory.
- `cmd/telagui/tdl/` colocates with the existing reference renderer
  but buries TDL inside the GUI binary's tree, implying ownership.

Leaning toward the neutral top-level `tdl/` as the right signal, but
flagging for decision during implementation.

### 6.5 Copy

Proposed hero tagline (final copy TBD when landing page is built; this
is the design intent):

> **Tela**
> Encrypted remote access from a single binary, on any network, without
> root, without a VPN client, without opening a port.

"What Tela is" paragraph (from existing `README.md` cut down):

> Tela is a connectivity fabric. Three small programs (client, agent,
> hub) let one machine reach a service on another through an encrypted
> WireGuard tunnel, without either side opening an inbound port or
> running anything as root. Scales from a single laptop to a fleet of
> machines managed by a team, using the same three binaries.

The design calls out that the landing page should not restate the book.
Links are the work of the page; prose is the work of the book.

## 7. Version map

A machine-readable file at `gh-pages:/versions.json` describes every
edition currently published on the site:

```json
{
  "generatedAt": "2026-04-20T13:00:00Z",
  "current": {
    "stable": {
      "version": "v1.0.0",
      "url": "/book/",
      "releasedAt": "2026-05-15"
    },
    "beta": {
      "version": "v1.1.0-beta.1",
      "url": "/book/beta/",
      "releasedAt": "2026-05-20"
    },
    "dev": {
      "version": "v1.1.0-dev.3",
      "url": "/book/dev/",
      "releasedAt": "2026-05-21"
    }
  },
  "archive": [
    {
      "version": "v0.12.0",
      "url": "/book/archive/v0.12.0/",
      "releasedAt": "2026-04-20"
    }
  ]
}
```

The docs workflow regenerates `versions.json` as part of every deploy
by enumerating `gh-pages` directory state, the same way it regenerates
`book/archive/index.html` today. The generator expands to produce both
artifacts from a single walk.

The landing page's version-map panel reads this file at page load. The
book's archive index continues to exist at `/book/archive/index.html`
as today, but is now one of two consumers of the same directory walk.

## 8. Build pipeline changes

Affected files:

1. `.github/workflows/docs.yml`:
   - Change `site-url` values to match the new mount points (`/book/`,
     `/book/beta/`, `/book/dev/`, `/book/archive/vX.Y.Z/`).
   - Change `destination_dir` values for peaceiris deploys:
     stable-to-root becomes stable-to-`book/`, beta becomes
     `destination_dir: book/beta`, dev becomes `destination_dir: book/dev`.
     Archive becomes `destination_dir: book/archive/vX.Y.Z`.
   - Add a new step at the end of the stable-deploy path that copies
     `site/*` from the checked-out source into `gh-pages` root and
     regenerates `versions.json`. This step also runs on beta and dev
     deploys so that `versions.json` stays current (only the relevant
     channel entry is updated; the others are preserved from existing
     state).
   - Keep `keep_files: true` everywhere so sibling subtrees survive.

2. `book/src/sidebar-version.js`:
   - Switcher link hrefs change to `/book/`, `/book/beta/`, `/book/dev/`,
     `/book/archive/`.

3. `book/src/channel-banner.js`:
   - "stable release is documented at" link changes to `/book/` and the
     text label changes to `telaproject.org/book` (or similar).

4. `book/book.toml`:
   - Default `site-url` stays on `/tela/` for local dev? Actually no:
     update it to `/book/` so `mdbook serve` still resolves assets
     sensibly locally. Local serve mounts at `http://localhost:3000/`,
     so this matters only for absolute-path references in generated
     HTML, which the workflow overrides anyway.

5. `site/` (new directory): contains `index.html`, `style.css`, assets,
   and `versions.js`.

6. `.github/workflows/release.yml` (optional follow-up):
   - Have `release.yml` invoke `docs.yml` via `workflow_call` after a
     tag is created, so docs always rebuild automatically on tag push.
     (Today a tag created by `GITHUB_TOKEN` does not fire `docs.yml`'s
     on-push-tag trigger, as we hit during the v0.14.0-dev.2 bootstrap.)

## 9. Migration plan

This is a one-shot operation tied to the `v1.0.0` cut. It happens in
this order:

1. **Land the design.** Merge `DESIGN-telaproject-site.md` (this file)
   into main.
2. **Build the landing page.** Land `site/index.html` and supporting
   assets on main, with placeholders for dynamic bits (version map
   hydrates at page load, download CTA is a static "current stable"
   link that gets swapped during the cut). No deployment yet; the file
   just exists in the repo.
3. **Rewrite `docs.yml`.** Change `site-url` values and
   `destination_dir` targets; add the `site/*` copy step and the
   `versions.json` generator. Do not deploy yet; the workflow is still
   tag-triggered, and no new tag is cut until §5.
4. **Update `sidebar-version.js` and `channel-banner.js`.** Change
   hrefs and labels.
5. **Cut `v1.0.0-dev.N+1` from main.** This tag exercises the new
   pipeline end-to-end against a dev deploy. Verify
   `paulmooreparks.github.io/tela/dev/` (still on the old domain)
   lands correctly at `…/tela/book/dev/` via the new paths. Fix
   anything broken.
6. **Flip DNS.** Register `telaproject.org` with GitHub Pages (Settings
   → Pages → Custom domain). Point DNS at Pages as in §4.2. Wait for
   Pages to provision TLS.
7. **Cut `v1.0.0` stable.** Promotion workflow produces the stable
   tag. `docs.yml` publishes stable to `/book/`, copies `site/*` to
   root, regenerates `versions.json`. The landing page goes live at
   `telaproject.org/`.
8. **Verify.** All four URLs respond with correct content. Old
   `paulmooreparks.github.io/tela/*` redirects to
   `telaproject.org/*`.

Until step 7, the existing `paulmooreparks.github.io/tela/` site
continues to serve the old layout (book at root) because no stable
deploy with the new `site-url` has run. The new layout lands with the
1.0 cut, atomically.

## 10. URL redirects

GitHub Pages handles one kind of redirect automatically: custom-domain
rebinding. Once the CNAME is in place, requests to
`paulmooreparks.github.io/tela/anything` return 301 to
`telaproject.org/anything`.

This means `paulmooreparks.github.io/tela/getting-started/install.html`
redirects to `telaproject.org/getting-started/install.html`, which
after the migration serves 404 because the book's pages moved to
`/book/*`. That is the one unavoidable breakage. Mitigations:

- Accept it. Pre-1.0 URLs are not contract. Anyone who bookmarked the
  old path gets a 404; they can type `/book/` and find what they want.
- We could publish a static shim tree at `gh-pages:/` containing
  meta-refresh HTML files for every book page that maps to
  `/book/<same-path>`. That is hundreds of files and is maintenance
  overhead (every time a book page is renamed, the shim drifts).
  Rejected.
- We could add a 404 page at `gh-pages:/404.html` that includes a JS
  hint: "Looking for a book page? Try /book + your path." Cheap; does
  not fix bookmarks, but helps users who arrive via a broken link.
  **Recommended.**

Post-1.0, the `/book/…` paths are contract and never move again.

## 11. Source-tree and release-process impact

`site/` is a new top-level directory. It is checked into the repo
alongside `book/`, and is the source of truth for the landing page.
Edits happen there. The CI workflow (`.github/workflows/docs.yml`) is
the only thing that deploys it, so the contract is the same as for the
book: source changes land in main, a tag cut publishes them.

`RELEASE-PROCESS.md` gains a one-line note that the landing page and
version map are rebuilt by the docs workflow on every tag push, same
mechanism as the book editions.

Nothing else in the release process changes. `VERSION` bumping, tag
naming, promotion workflow, channel manifests, binary publishing all
work identically.

## 12. Post-1.0 versioning

Once 1.0 ships and `telaproject.org/` is public, the URL contract is:

- `telaproject.org/` is always a landing page. Never a book chapter.
- `telaproject.org/book/` is always the current stable book. Content
  changes over time; the URL is stable.
- `telaproject.org/book/archive/vX.Y.Z/` is permanent. Once a stable
  release is archived here, the URL does not move.
- `telaproject.org/versions.json` is a public, documented endpoint;
  its schema is frozen at 1.0. Additions to the JSON are allowed
  (new fields); removals or renames are a major-version change on
  the JSON schema, which is separate from the project's semver.

These are the minimum commitments that let external sites link to Tela
without fearing URL rot.

## 13. Open questions

1. **Canonical host: apex or `www`?** §4 picks apex. Industry convention
   is increasingly apex-as-canonical. Flagging in case we have a reason
   to prefer `www`.
2. **Download detection on the landing page.** User-Agent sniffing is
   imperfect (Linux arm64 vs Linux amd64, for example). Fallback UX:
   show a manual platform picker below the "Download for Linux" button
   with explicit amd64/arm64 options. Good enough?
3. **Package-manager surfaces.** The landing page should link to winget,
   brew, apt, etc. once those exist. Those are tracked as a separate
   1.0 item. When they land, the download section grows a tabbed
   "Manual download | Package managers" layout. Not blocking 1.0 for
   this design.
4. **Hosted public hub.** The landing page could host a "try Tela in
   your browser" demo against a public hub we run. Out of scope for
   1.0; adding it later does not require URL changes.
5. **`book.toml` canonical URL.** mdBook's `git-repository-url` and
   `edit-url-template` point at GitHub `main`. Should they be rewritten
   per edition to point at the tag? Today they always point at main,
   which means clicking "edit this page" from an archived v0.12.0 page
   opens the current main source, not the source at the tag. Cosmetic
   issue; not blocking. Decide later.
6. **Cross-linking from book to landing.** Today the book has no link
   back to the project home (because the project home is the book).
   After migration, should every book page have a small "← telaproject.org"
   link in the topbar? Probably yes. Implementation: edit the mdBook
   theme's default header template, or inject via JS. Decide during
   landing-page build.
7. **RSS or Atom feed for releases.** Several users have asked for
   machine-readable release announcements. Trivial to generate from
   the channel manifests during the docs build and publish at
   `/releases.xml`. Adds zero runtime cost. Include in 1.0 or defer?

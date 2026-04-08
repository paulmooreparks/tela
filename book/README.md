# Tela documentation site

This directory contains the [mdBook](https://rust-lang.github.io/mdBook/)
source for the Tela documentation site published at
<https://paulmooreparks.github.io/tela/>.

## Structure

- `book.toml` -- mdBook configuration
- `src/SUMMARY.md` -- the table of contents (the spine of the book)
- `src/**/*.md` -- chapter sources

Most chapter files in `src/` are thin wrappers that pull the canonical
markdown from the repository root via mdBook's `{{#include}}` preprocessor.
For example, `src/guide/reference.md` is just:

```markdown
{{#include ../../../REFERENCE.md}}
```

This means the docs and the source code are atomically updated together: a
PR that adds a new CLI command and a new section in `REFERENCE.md` also
updates the published docs site on the next deploy. There is no second
source of truth.

A few chapters (introduction, the getting-started chapters, the brief
Awan Saya overview, contributing) are written natively for the book and live
only in `src/`.

## Building locally

```bash
# Install mdBook (one time, requires Rust toolchain or a download)
cargo install mdbook

# Then from the repo root:
cd book
mdbook serve --open
```

`mdbook serve` watches the source files and rebuilds on every change. Open
the URL it prints in your browser; the page reloads automatically as you
edit.

## CI deployment

`.github/workflows/docs.yml` builds the book on every push to main that
touches `book/**` or any `*.md` file, and publishes it to GitHub Pages.

## Adding a new chapter

1. Create the markdown file under `src/` in the appropriate subdirectory.
   If it should pull from a canonical doc at the repo root, use
   `{{#include ../../../FILENAME.md}}`. Otherwise just write it directly.
2. Add a line to `src/SUMMARY.md` referencing the new file. The position
   in `SUMMARY.md` determines the position in the navigation tree.
3. Commit. The next push to main will publish.

## Editing an existing chapter

If the chapter is a `{{#include ...}}` wrapper, edit the canonical file at
the repo root. The book picks up the change automatically.

If the chapter is native to the book, edit the file under `src/` directly.

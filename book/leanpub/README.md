# Tela: the book

This directory holds the manuscript for "Tela: A Connectivity Fabric" in
[Leanpub](https://leanpub.com/) format.

The book and the documentation site share source. Most chapters here are
either written natively for the book or pull from the canonical Markdown
files at the repository root via `Manuscript.md` includes (the same model
as `book/src/`). When the documentation gets updated, the book draft gets
updated automatically.

## Layout

- `Book.txt` -- the Leanpub manifest. Lists each chapter file in order.
  This is the file Leanpub reads when it builds a preview or a release.
- `manuscript/` -- the chapter files, in book order.
- `frontmatter/` -- title page, copyright, dedication, preface.
- `backmatter/` -- appendices, glossary, acknowledgments.

## Status

Draft. The chapter outline is in place; many chapters are stubs that point
at the corresponding doc page. The book is meant to be published iteratively
on Leanpub as Tela approaches 1.0, with early readers buying in at the first
chapter and getting all updates as more chapters are written.

## Building locally

Leanpub builds the book on its servers; there is no local build step. To
preview a chapter, just open the markdown file in any viewer that handles
mdBook-style markdown.

To preview the *site* version of the same content, see `book/README.md`.

## When the book is ready to publish

1. Create a Leanpub account and a new book at <https://leanpub.com/>.
2. Connect Leanpub to this repository (Leanpub supports GitHub directly).
3. Point Leanpub at this directory (`book/leanpub/`).
4. Click "Preview" on Leanpub to generate a PDF / EPUB / MOBI from the
   current manuscript.
5. When the first chapters are good enough to share, click "Publish."
6. Iterate.

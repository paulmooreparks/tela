# Contributing

Tela is an early-stage project moving fast. Contributions are welcome, but
the bar is real: the code base has a "no cruft, no backward compatibility
until 1.0" policy that drives a lot of the decisions, and PRs need to land
clean (build, vet, gofmt, race-clean tests, no stray files).

## Setting Up a Dev Environment

```bash
git clone https://github.com/paulmooreparks/tela
cd tela
go build ./...
go vet ./...
go test ./...
gofmt -l .          # should print nothing
```

Continuous integration also enforces per-package test coverage floors on
the security-critical packages; `go test ./...` passing locally is the main
signal, and the coverage gate output in CI tells you exactly which package
regressed if it trips.

For TelaVisor specifically:

```bash
cd cmd/telagui
wails build         # outputs to ./build/bin/
```

You will need [Wails v2](https://wails.io/) installed.

## What to Read First

- [CLAUDE.md](https://github.com/paulmooreparks/tela/blob/main/CLAUDE.md):
  the project's guiding principles, coding conventions, API style, and the
  list of architectural review items
- [Why a Connectivity Fabric](architecture/design.md): the design
  rationale for the core architecture
- [ROADMAP-1.0.md](https://github.com/paulmooreparks/tela/blob/main/ROADMAP-1.0.md):
  the 1.0 readiness checklist (anything unticked is fair game)
- [STATUS.md](https://github.com/paulmooreparks/tela/blob/main/STATUS.md):
  the traceability matrix from design sections to implementation

## Filing Issues

Use the
[GitHub issue tracker](https://github.com/paulmooreparks/tela/issues). For
security issues, follow
[SECURITY.md](https://github.com/paulmooreparks/tela/blob/main/SECURITY.md)
rather than filing a public issue.

## Pre-1.0 Ground Rules

- No backward-compatibility shims. If a name or shape is wrong, fix it
  everywhere in one commit.
- Delete duplicate code paths. When a new shape replaces an old one, the
  old one goes in the same change.
- No "deprecated" markings yet. Pre-1.0 there is no deprecation; there is
  only the right shape and the wrong shape.

After 1.0, the rules invert: deprecation will be slow and deliberate, and
backward compatibility will be maintained religiously. Anything left in the
tree at 1.0 becomes a permanent maintenance burden, so we cut aggressively
now.

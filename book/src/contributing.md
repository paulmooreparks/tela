# Contributing

Tela is an early-stage project moving fast. Contributions are welcome but
the bar is real: the code base has a "no cruft, no backward compatibility
until 1.0" policy that drives a lot of the decisions, and PRs need to land
clean (build, vet, gofmt, race-clean tests, no stray files).

## Setting up a dev environment

```bash
git clone https://github.com/paulmooreparks/tela
cd tela
go build ./...
go vet ./...
go test ./...
gofmt -l .          # should print nothing
```

For TelaVisor specifically:

```bash
cd cmd/telagui
wails build         # outputs to ./build/bin/telavisor.exe
```

You will need [Wails v2](https://wails.io/) installed.

## What to read first

- [CLAUDE.md](https://github.com/paulmooreparks/tela/blob/main/CLAUDE.md) --
  the project's guiding principles, coding conventions, API style, and the
  list of architectural review items
- [Why a connectivity fabric](architecture/design.md) -- the design
  rationale for the core architecture
- [Status: design vs implementation](ops/status.md) -- the live
  traceability matrix from design sections to implementation
- [Roadmap to 1.0](ops/roadmap.md) -- the 1.0 readiness checklist (anything
  unticked there is fair game)

## Filing issues

Use the [GitHub issue tracker](https://github.com/paulmooreparks/tela/issues).
For security issues, see [SECURITY.md](https://github.com/paulmooreparks/tela/blob/main/SECURITY.md)
once it exists (it is on the 1.0 blocker list).

## Pre-1.0 ground rules

- No backward-compatibility shims. If a name or shape is wrong, fix it
  everywhere in one commit.
- Delete duplicate code paths. When a new shape replaces an old one, the
  old one goes in the same change.
- No "deprecated" markings yet. Pre-1.0 there is no deprecation; there is
  only "the right shape" and "the wrong shape."

After 1.0, the rules invert: deprecation will be slow and deliberate, and
backward compatibility will be maintained religiously. Anything left in the
tree at 1.0 becomes a permanent maintenance burden, so we cut aggressively
now.

# Contributing to Tela

Tela is open source under the Apache 2.0 license. Contributions are
welcome.

## Building from Source

Tela is a single Go module with three binaries:

```bash
# Build all three binaries
go build ./...

# Run the full check suite (must pass before submitting a PR)
go build ./...
go vet ./...
go test ./...
gofmt -l .          # should print nothing
```

Requirements:
- Go 1.24 or later
- No CGO dependencies (pure Go, cross-compiles cleanly)

To build TelaVisor (the desktop GUI):

```bash
cd cmd/telagui
wails build
```

This requires [Wails v2](https://wails.io/) and its platform
prerequisites (a C compiler and platform WebView SDK).

## Project Layout

| Path | What it is |
|------|------------|
| `cmd/tela/` | Client CLI binary |
| `cmd/telad/` | Agent daemon binary |
| `cmd/telahubd/` | Hub server binary |
| `cmd/telagui/` | TelaVisor desktop GUI (Wails v2) |
| `internal/` | Shared packages (not a public API) |
| `book/` | mdBook documentation source |

## Code Style

- Standard `gofmt` formatting. The CI rejects unformatted code.
- `go vet` must pass with no warnings.
- Errors: `fmt.Errorf("context: %w", err)` wrapping pattern.
- Logging: `log.Printf("[component] message")` with bracketed prefix.
- Minimal dependencies. Prefer the standard library.
- Constant-time comparison for all token checks (`crypto/subtle`).
- No backward-compatibility shims. Tela is pre-1.0; when a name or
  shape is wrong, fix it everywhere in one commit.

## Submitting Changes

1. Fork the repository and create a branch from `main`.
2. Make your changes. Keep commits focused and well-described.
3. Run the full check suite (build, vet, test, gofmt).
4. Open a pull request against `main`.

For larger changes, open an issue first to discuss the approach.

## Tests

The test suite runs on every push via GitHub Actions. CI runs on
Linux with `-race` enabled, so race-clean code is required.

The `internal/teststack` package provides an in-process test harness
that spins up a hub and agent for integration tests. See
`internal/teststack/stack_test.go` for examples.

## Documentation

User-facing documentation lives in the `book/` directory (mdBook).
Reference data (CLI reference, configuration, access model) lives
at the repo root and is included into the book via `{{#include}}`.

When changing user-visible behavior, update the relevant documentation
in the same commit.

## Reporting Issues

Use [GitHub Issues](https://github.com/paulmooreparks/tela/issues)
for bug reports and feature requests. For security vulnerabilities,
see [SECURITY.md](SECURITY.md).

# TelaVisor

TelaVisor is the desktop graphical interface for Tela. It wraps the `tela`
command-line tool in a window with menus, dialogs, panels, and a file
browser, so you can manage connections, hubs, agents, profiles, files, and
credentials without ever opening a terminal. It runs on Windows, Linux,
and macOS.

![TelaVisor connected to a hub](book/src/screens/telavisor-status-connected.png)

## In one paragraph

TelaVisor stores hub credentials, builds connection profiles, launches the
`tela` process to open encrypted tunnels, monitors tunnel state in real
time, browses remote file shares, and provides a full administration
interface for the hubs and agents you operate. It is a control surface
around the same `tela` CLI you can use directly; profiles, credentials,
and configuration are interchangeable between the two.

## Where the documentation lives

The complete TelaVisor reference (every tab, every panel, every dialog,
every workflow, with screenshots for each) is in the book chapter at
[guide/telavisor.html](https://telaproject.org/book/guide/telavisor.html).
Read it there, not here. The book chapter is the canonical source.

This file is a front-porch overview kept short on purpose so it stays in
sync with the book chapter without separately drifting. If you find
yourself wanting to add depth here, add it to the book chapter instead.

## Quick reference for code readers

If you are reading the source and need to know where things live without
opening the book:

| Concern | Location |
|---------|----------|
| Wails project | [cmd/telagui/](cmd/telagui/) |
| Go backend | [cmd/telagui/app.go](cmd/telagui/app.go) and friends |
| Frontend (HTML/CSS/JS) | [cmd/telagui/frontend/](cmd/telagui/frontend/) |
| Build | `cd cmd/telagui && wails build` |
| Output binary | `cmd/telagui/build/bin/telavisor` (or `.exe`) |
| Profile storage | `%APPDATA%\tela\profiles\` (Windows), `~/.tela/profiles/` (Unix) |
| Settings | `telavisor-settings.yaml` in the Tela config directory |
| Credentials (shared with `tela` CLI) | `%APPDATA%\tela\credentials.yaml` (Windows), `~/.tela/credentials.yaml` (Unix) |

The frontend is bundled into the Go binary at build time, not at runtime.
Editing HTML, CSS, or JS requires a `wails build` to take effect.

## License and design language

Apache 2.0, same as the rest of Tela. TelaVisor is also the reference
implementation of the [Tela Design Language](TELA-DESIGN-LANGUAGE.md), the
visual language shared across all Tela products.

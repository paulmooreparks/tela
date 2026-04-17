// Package cliflag hosts small helpers shared across the tela, telad, and
// telahubd command-line entry points. The aim is not a CLI framework;
// it is a place for one- or two-function utilities that would otherwise
// get copy-pasted into every package that parses subcommand flags.
package cliflag

import "flag"

// Help registers -h, -?, -help, and --help on fs as help-request flags.
// It returns a getter that reports whether any of them was set after
// fs.Parse. Callers use it to decide whether to print usage and return
// at the current command level.
//
// Only -h appears as a first-class flag in usage output; the others are
// registered with empty usage strings so they act as aliases without
// cluttering flag listings. (In practice, most Tela commands print their
// own usage text and never call fs.PrintDefaults, so even the aliases
// rarely surface.)
//
// Go's flag package treats single-dash and double-dash long flags as the
// same flag, so registering "help" covers both -help and --help.
func Help(fs *flag.FlagSet) func() bool {
	h := fs.Bool("h", false, "Show help")
	q := fs.Bool("?", false, "")
	hp := fs.Bool("help", false, "")
	return func() bool { return *h || *q || *hp }
}

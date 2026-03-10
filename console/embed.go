// Package console embeds the hub console static files.
package console

import "embed"

//go:embed www/*
var FS embed.FS

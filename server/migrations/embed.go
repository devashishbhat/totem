// Package migrations embeds the goose SQL migrations so that the server binary
// can apply them itself, with no goose CLI on the deploy host.
package migrations

import "embed"

// FS holds every .sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS

// Package migrations embeds the goose SQL migrations so binaries and tests
// can apply them without depending on the working directory.
package migrations

import "embed"

// FS holds every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS

// Package migrations embeds the authoritative SQL migrations (SCHEMA.md) so the
// SQLite adapter can auto-migrate without depending on the working directory.
package migrations

import "embed"

// FS holds every *.sql migration, applied in lexical filename order.
//
//go:embed *.sql
var FS embed.FS

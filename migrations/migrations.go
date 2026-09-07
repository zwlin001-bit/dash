package migrations

import "embed"

// FS embeds all SQL migration files for standalone execution.
//
//go:embed *.sql
var FS embed.FS

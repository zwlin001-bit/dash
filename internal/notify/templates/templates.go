package templates

import "embed"

// FS embeds built-in default notification templates.
//
//go:embed *.tmpl
var FS embed.FS

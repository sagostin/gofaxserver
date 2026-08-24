// Package web embeds the built Vue SPA. The placeholder index.html is
// replaced by the real build output (frontend/ → vite build).
package web

import "embed"

//go:embed all:dist
var Dist embed.FS

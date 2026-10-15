// Package web holds the files embedded in the binary: HTML templates and
// static assets (icons, fonts, the stylesheet built by Tailwind, app.js).
package web

import "embed"

//go:embed templates static
var FS embed.FS

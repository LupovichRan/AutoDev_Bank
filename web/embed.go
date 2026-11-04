// Package web embeds the built Svelte PWA (web/dist) into the bank binary.
//
// `./dev run` and `./dev ci` run `npm --prefix web run build` first, which
// replaces the committed placeholder dist/index.html with the real app.
package web

import "embed"

// Dist holds the built static files; serve it via fs.Sub(Dist, "dist").
//
//go:embed all:dist
var Dist embed.FS

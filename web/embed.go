// Package web embeds the built SPA so the Go binary can serve it in production.
//
// The dist/ directory is produced by `npm run build` and is gitignored; a
// .gitkeep sentinel is committed so this `//go:embed` directive always finds
// the directory on a fresh checkout (it will just be near-empty until the SPA
// is built).
package web

import "embed"

//go:embed all:dist
var Dist embed.FS

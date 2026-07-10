// Package ui embeds the built SPA into the phebs binary.
package ui

import "embed"

// Dist holds the Vite production build. A committed placeholder keeps
// `go build` green; T5.1 wires the real build.
//
//go:embed all:dist
var Dist embed.FS

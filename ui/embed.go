//go:build ui

// Package ui embeds the built SPA into the phebs binary.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the production SPA build (make ui, then build with -tags ui).
func FS() (fs.FS, error) { return fs.Sub(dist, "dist") }

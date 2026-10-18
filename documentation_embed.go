//go:build ui

package embeddeddocs

import (
	"embed"
	"io/fs"
)

// Explicit patterns keep fixtures, design handoffs, receipts and local files out.
//
//go:embed README.md PLAN.md docs/*.md docs/*.svg docs/config.example.yaml docs/guides/*.md
var embedded embed.FS

func documentationFiles() fs.FS { return embedded }

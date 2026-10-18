//go:build !ui

package embeddeddocs

import "io/fs"

func documentationFiles() fs.FS { return nil }

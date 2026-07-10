//go:build !ui

// Package ui embeds the built SPA into the phebs binary.
package ui

import (
	"io/fs"
	"testing/fstest"
)

// FS returns a placeholder page. Without the ui build tag no npm output is
// embedded, so `go build ./...` stays green on a fresh clone; `make dev`
// and `make build` use -tags ui with the real bundle.
func FS() (fs.FS, error) {
	const page = `<!doctype html>
<html><body style="font-family:system-ui;margin:4rem">
<h1>phebs</h1>
<p>This binary was built without the embedded UI.
Use <code>make dev</code> or <code>make build</code>, or develop the UI live
with <code>cd ui && npm run dev</code> (proxies /api to :3070).</p>
</body></html>`
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(page)}}, nil
}

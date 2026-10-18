//go:build ui

package embeddeddocs

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedDocumentationScopeAndIndex(t *testing.T) {
	files := documentationFiles()
	var count, total int
	if err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if name == "history.txt" || strings.HasPrefix(name, "docs/fixtures/") || strings.HasPrefix(name, "docs/design_handoff_") || strings.HasPrefix(name, "spike/") {
			t.Errorf("excluded bytes embedded: %s", name)
		}
		data, readErr := fs.ReadFile(files, name)
		count++
		total += len(data)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"history.txt", "docs/fixtures/change-workbench/README.md", "docs/design_handoff_phebs_brand_and_ui/README.md", "spike/t475/release/receipts/template.json"} {
		if _, err := fs.Stat(files, excluded); err == nil {
			t.Errorf("excluded path embedded: %s", excluded)
		}
	}
	started := time.Now()
	handler, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	renderTime := time.Since(started)
	cache := handler.(documentationHandler)
	var payload, capacity int
	for _, page := range cache {
		payload += len(page.body)
		capacity += cap(page.body)
	}
	t.Logf("embedded documentation: %d files, %d source bytes, %d cached pages, %d cached payload bytes, %d cached byte capacity, startup render %s", count, total, len(cache), payload, capacity, renderTime)
	for _, name := range []string{"README.md", "PLAN.md", "docs/README.md", "docs/MANUAL.md", "docs/guides/GETTING_STARTED.md", "docs/guides/WORKFLOWS.md", "docs/phebs-architecture.svg", "docs/config.example.yaml", "schemas/", "spike/t304/README.md"} {
		response := documentationRequest(handler, http.MethodGet, Prefix+name)
		if response.Code != http.StatusOK {
			t.Errorf("embedded-index journey %s = %d", name, response.Code)
		}
	}
	manual := documentationRequest(handler, http.MethodGet, Prefix+"docs/MANUAL.md").Body.String()
	if !strings.Contains(manual, `href="./guides/GETTING_STARTED.md"`) {
		t.Fatal("manual's tracked relative guide link was rewritten or lost")
	}
}

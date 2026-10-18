//go:build ui

package main

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	documentation "github.com/bmeddeb/phebs"
)

func TestEmbeddedDocumentationLinksResolve(t *testing.T) {
	root := repositoryRoot(t)
	handler, err := documentation.NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	var sources, local, embedded, repositoryOnly int
	var failures []string
	for _, source := range trackedFiles(t, root, "*.md") {
		if !embeddedDocument(source) || !strings.HasSuffix(source, ".md") {
			continue
		}
		sources++
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source)))
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range documentTargets(data) {
			resolved, isLocal, err := resolveDocumentTarget(root, source, target)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s -> %q: %v", source, target, err))
				continue
			}
			if !isLocal {
				continue
			}
			local++
			name := resolved
			if name == "." {
				name = ""
			}
			base := &url.URL{Scheme: "https", Host: "docs.invalid", Path: documentation.Prefix + source}
			link, err := url.Parse(html.UnescapeString(target))
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s -> %q: invalid browser URL", source, target))
				continue
			}
			browserURL := base.ResolveReference(link)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, browserURL.RequestURI(), nil))
			if response.Code == http.StatusFound && name == "" {
				location := response.Header().Get("Location")
				response = httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, location, nil))
			}
			if response.Code != http.StatusOK {
				failures = append(failures, fmt.Sprintf("%s -> %q (%s): HTTP %d", source, target, resolved, response.Code))
				continue
			}
			if embeddedDocument(resolved) {
				embedded++
				if strings.Contains(response.Body.String(), "<h1>Repository-only reference</h1>") {
					failures = append(failures, fmt.Sprintf("%s -> %q: allowed document is repository-only", source, target))
				}
			} else {
				repositoryOnly++
				if name != "" && !strings.Contains(response.Body.String(), "<h1>Repository-only reference</h1>") {
					failures = append(failures, fmt.Sprintf("%s -> %q: excluded reference is not labelled", source, target))
				}
			}
		}
	}
	sort.Strings(failures)
	if len(failures) != 0 {
		t.Fatalf("embedded-document link contract failures:\n%s", strings.Join(failures, "\n"))
	}
	if sources == 0 || embedded == 0 || repositoryOnly == 0 {
		t.Fatalf("vacuous link census: sources=%d embedded=%d repository-only=%d", sources, embedded, repositoryOnly)
	}
	t.Logf("T24.1 link census: %d embedded Markdown sources, %d local links: %d embedded, %d explicitly repository-only", sources, local, embedded, repositoryOnly)
}

func embeddedDocument(name string) bool {
	if name == "README.md" || name == "PLAN.md" || name == "docs/config.example.yaml" {
		return true
	}
	if !strings.HasPrefix(name, "docs/") || strings.HasPrefix(name, "docs/fixtures/") || strings.HasPrefix(name, "docs/design_handoff_phebs_brand_and_ui/") {
		return false
	}
	return path.Ext(name) == ".md" || path.Ext(name) == ".svg"
}

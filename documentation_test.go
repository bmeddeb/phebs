package embeddeddocs

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDocumentationPagesAndRepositoryOnlyReferences(t *testing.T) {
	files := fstest.MapFS{
		"README.md": {Data: []byte("# phebs\n[Manual](docs/MANUAL.md#usage)\n")},
		"PLAN.md":   {Data: []byte("# Architecture\n")},
		"docs/README.md": {Data: []byte("# Documentation\n[Manual](MANUAL.md)\n" +
			"[Record](../spike/retained.json)\n[Schemas](../schemas/)\n" +
			"[Excluded](fixtures/neutral/README.md)\n" +
			"<img src=\"diagram-only.gif\">\n")},
		"docs/MANUAL.md": {Data: []byte("# Manual\n## Usage\n[Guide](guides/USE.md)\n" +
			"[Plan](../PLAN.md)\n![Architecture](architecture.svg)\n[Config](config.example.yaml)\n" +
			"\n| Key | Value |\n| --- | --- |\n| GFM | ~~old~~ |\n\n- [x] complete\n\n```sh\necho phebs\n```\n")},
		"docs/guides/USE.md":       {Data: []byte("# Use phebs\n[Manual](../MANUAL.md#usage)\n")},
		"docs/architecture.svg":    {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>Architecture</text></svg>`)},
		"docs/config.example.yaml": {Data: []byte("server:\n  listen: :3070\n")},
	}
	handler, err := newHandler(files)
	if err != nil {
		t.Fatal(err)
	}
	for name, file := range files {
		t.Run(name, func(t *testing.T) {
			recorder := documentationRequest(handler, http.MethodGet, Prefix+name)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
			if strings.HasSuffix(name, ".md") {
				if !strings.Contains(recorder.Body.String(), "<!doctype html>") || !strings.Contains(recorder.Body.String(), "<main>") {
					t.Fatal("Markdown did not render in the branded shell")
				}
			} else if !bytes.Equal(recorder.Body.Bytes(), file.Data) {
				t.Fatal("static file bytes changed")
			}
			head := documentationRequest(handler, http.MethodHead, Prefix+name)
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != recorder.Header().Get("Content-Length") {
				t.Fatalf("HEAD = %+v", head)
			}
		})
	}
	manual := documentationRequest(handler, http.MethodGet, Prefix+"docs/MANUAL.md").Body.String()
	for _, markup := range []string{`id="usage"`, `href="guides/USE.md"`, `href="../PLAN.md"`, `<table tabindex="0">`, `<pre tabindex="0">`, `<del>old</del>`, `type="checkbox"`, `src="architecture.svg"`} {
		if !strings.Contains(manual, markup) {
			t.Errorf("rendered manual is missing %q", markup)
		}
	}
	for _, name := range []string{"spike/retained.json", "schemas/", "docs/fixtures/neutral/README.md", "docs/diagram-only.gif"} {
		value := documentationRequest(handler, http.MethodGet, Prefix+name)
		if value.Code != http.StatusOK || !strings.Contains(value.Body.String(), "Repository-only reference") || !strings.Contains(value.Body.String(), publicRepository+"/") {
			t.Errorf("repository reference %s = %d %s", name, value.Code, value.Body.String())
		}
	}
	for _, name := range []string{"history.txt", ".git/config", "docs/unknown.md", "../README.md", "docs/../PLAN.md", "https://evil.invalid/"} {
		if value := documentationRequest(handler, http.MethodGet, Prefix+name); value.Code != http.StatusNotFound {
			t.Errorf("unknown/unclean reference %s = %d", name, value.Code)
		}
	}
	index := documentationRequest(handler, http.MethodGet, Prefix)
	if index.Code != http.StatusFound || index.Header().Get("Location") != Prefix+"docs/README.md" {
		t.Fatalf("index = %d %s", index.Code, index.Header().Get("Location"))
	}
	if value := documentationRequest(handler, http.MethodPost, Prefix+"README.md"); value.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", value.Code)
	}
	if value := documentationRequest(handler, http.MethodGet, Prefix+"docs/architecture.svg"); !strings.HasPrefix(value.Header().Get("Content-Security-Policy"), "sandbox;") {
		t.Fatal("SVG response is not sandboxed")
	}
	// Serving cannot reopen or re-render the input after startup.
	files["docs/MANUAL.md"].Data = []byte("changed")
	if got := documentationRequest(handler, http.MethodGet, Prefix+"docs/MANUAL.md").Body.String(); got != manual {
		t.Fatal("request re-read a source document")
	}
}

func TestDocumentationRefusesActiveContentAndOffsiteImages(t *testing.T) {
	files := fstest.MapFS{"README.md": {Data: []byte("# Safe\n\n" +
		"<script>alert('raw')</script>\n\n" +
		"[javascript](JaVaScRiPt:alert%281%29) " +
		"[entity](java&#x73;cript:alert%281%29) " +
		"[file](file:///etc/passwd) [data](data:text/html,evil)\n\n" +
		"![remote](https://tracking.invalid/image.svg) " +
		"![data image](data:image/svg+xml;base64,PHN2Zz4=)\n\n" +
		"[HTTPS](https://example.com/docs) [Mail](mailto:help@example.com)\n")}}
	handler, err := newHandler(files)
	if err != nil {
		t.Fatal(err)
	}
	response := documentationRequest(handler, http.MethodGet, Prefix+"README.md")
	body := response.Body.String()
	for _, unsafe := range []string{"<script", "alert('raw')", "JaVaScRiPt:", "java&#x73;cript:", "file:///", "data:text", "tracking.invalid", "data:image", "<img"} {
		if strings.Contains(body, unsafe) {
			t.Errorf("active or fetching markup remains: %q", unsafe)
		}
	}
	for _, safe := range []string{`href="https://example.com/docs"`, `href="mailto:help@example.com"`, "[Image: remote]", "[Image: data image]"} {
		if !strings.Contains(body, safe) {
			t.Errorf("safe output missing: %q", safe)
		}
	}
	if policy := response.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "default-src 'none'") || !strings.Contains(policy, "base-uri 'none'") {
		t.Fatalf("CSP = %s", policy)
	}
}

func TestDocumentationPreparationFailuresAndNoEmbed(t *testing.T) {
	for _, files := range []fs.FS{fstest.MapFS{"unexpected.go": {Data: []byte("package hidden")}}, fstest.MapFS{"README.md": {Mode: fs.ModeSymlink}}} {
		if _, err := newHandler(files); err == nil {
			t.Fatalf("unexpected file admission: %+v", files)
		}
	}
	handler, err := newHandler(nil)
	if err != nil || documentationRequest(handler, http.MethodGet, Prefix+"README.md").Code != http.StatusNotFound {
		t.Fatalf("non-UI build = %v", err)
	}
}

func documentationRequest(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, target, nil))
	return response
}

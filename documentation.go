// Package embeddeddocs serves the build's embedded project documentation.
package embeddeddocs

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Prefix preserves repository-relative links beneath one authenticated route.
const Prefix = "/documentation/"

const publicRepository = "https://github.com/bmeddeb/phebs"

type page struct {
	body        []byte
	contentType string
}

type documentationHandler map[string]page

var htmlReference = regexp.MustCompile(`(?i)\b(?:href|src)\s*=\s*["']([^"']+)["']`)

// NewHandler renders each Markdown document once, before the server starts.
// The caller must protect this handler with the ordinary authentication chain.
func NewHandler() (http.Handler, error) { return newHandler(documentationFiles()) }

func newHandler(files fs.FS) (http.Handler, error) {
	if files == nil {
		return http.NotFoundHandler(), nil
	}
	markdown := goldmark.New(goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	pages := make(documentationHandler)
	references := make(map[string]bool)
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("nonregular embedded documentation: %s", name)
		}
		source, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read documentation %s: %w", name, err)
		}
		var typ string
		switch path.Ext(name) {
		case ".md":
			document := markdown.Parser().Parse(text.NewReader(source))
			var inactive []ast.Node
			if err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
				if !entering {
					return ast.WalkContinue, nil
				}
				record := func(destination []byte) {
					if target, directory := localReference(name, destination); target != "" {
						references[target] = references[target] || directory
					}
				}
				switch node := node.(type) {
				case *ast.Link:
					record(node.Destination)
					if !safeDestination(node.Destination, false) {
						inactive = append(inactive, node)
					}
				case *ast.Image:
					record(node.Destination)
					target, _ := localReference(name, node.Destination)
					_, imageErr := fs.Stat(files, target)
					// Only embedded SVGs load; other images remain named placeholders.
					if !safeDestination(node.Destination, true) || path.Ext(target) != ".svg" || imageErr != nil {
						inactive = append(inactive, node)
					}
				case *ast.AutoLink:
					if !safeDestination(node.URL(source), false) {
						inactive = append(inactive, node)
					}
				case *ast.HTMLBlock:
					for index := 0; index < node.Lines().Len(); index++ {
						line := node.Lines().At(index)
						for _, reference := range htmlReference.FindAllSubmatch(line.Value(source), -1) {
							record(reference[1])
						}
					}
				case *ast.RawHTML:
					for index := 0; index < node.Segments.Len(); index++ {
						segment := node.Segments.At(index)
						for _, reference := range htmlReference.FindAllSubmatch(segment.Value(source), -1) {
							record(reference[1])
						}
					}
				}
				return ast.WalkContinue, nil
			}); err != nil {
				return fmt.Errorf("inspect documentation %s: %w", name, err)
			}
			for _, node := range inactive {
				parent := node.Parent()
				if image, ok := node.(*ast.Image); ok {
					parent.ReplaceChild(parent, node, ast.NewString(append(append([]byte("[Image: "), image.Text(source)...), ']')))
				} else if link, ok := node.(*ast.AutoLink); ok {
					parent.ReplaceChild(parent, node, ast.NewString(link.Label(source)))
				} else {
					for child := node.FirstChild(); child != nil; {
						next := child.NextSibling()
						parent.InsertBefore(parent, node, child)
						child = next
					}
					parent.RemoveChild(parent, node)
				}
			}
			var rendered bytes.Buffer
			if err := markdown.Renderer().Render(&rendered, source, document); err != nil {
				return fmt.Errorf("render documentation %s: %w", name, err)
			}
			// Scrollable code and tables must be keyboard-reachable without JavaScript.
			body := bytes.ReplaceAll(rendered.Bytes(), []byte("<pre>"), []byte(`<pre tabindex="0">`))
			body = bytes.ReplaceAll(body, []byte("<table>"), []byte(`<table tabindex="0">`))
			source = shell(name, body)
			typ = "text/html; charset=utf-8"
		case ".svg":
			typ = "image/svg+xml"
		case ".yaml":
			typ = "application/yaml; charset=utf-8"
		default:
			return fmt.Errorf("unsupported embedded documentation: %s", name)
		}
		pages[name] = page{source, typ}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("prepare documentation: %w", err)
	}
	for name, directory := range references {
		if _, embedded := pages[name]; embedded {
			continue
		}
		kind := "blob"
		if directory {
			kind = "tree"
		}
		reference := publicRepository + "/" + kind + "/main/" + (&url.URL{Path: name}).EscapedPath()
		body := `<h1>Repository-only reference</h1><p><code>` + html.EscapeString(name) +
			`</code> is not included in this binary.</p><p><a href="` + html.EscapeString(reference) +
			`">Open in the public repository</a>.</p>`
		pages[name] = page{shell(name, []byte(body)), "text/html; charset=utf-8"}
	}
	return pages, nil
}

func (pages documentationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == Prefix {
		http.Redirect(w, r, Prefix+"docs/README.md", http.StatusFound)
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	value, found := pages[name]
	if !strings.HasPrefix(r.URL.Path, Prefix) || !fs.ValidPath(name) || !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", value.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(value.body)))
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; font-src data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if value.contentType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; font-src data:")
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(value.body)
	}
}

func safeDestination(destination []byte, image bool) bool {
	raw := html.UnescapeString(string(destination))
	if strings.ContainsAny(raw, "\\\x00\r\n\t") || strings.TrimSpace(raw) != raw {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if image {
		return parsed.Scheme == "" && parsed.Host == "" && !strings.HasPrefix(raw, "//")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "", "http", "https", "mailto":
		return true
	default:
		return false
	}
}

func localReference(source string, destination []byte) (string, bool) {
	if !safeDestination(destination, false) {
		return "", false
	}
	parsed, err := url.Parse(html.UnescapeString(string(destination)))
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" {
		return "", false
	}
	target := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(parsed.Path, "/") {
		target = path.Join(path.Dir(source), target)
	}
	target = path.Clean(target)
	if !fs.ValidPath(target) || target == "." {
		return "", false
	}
	return target, strings.HasSuffix(parsed.Path, "/")
}

func shell(name string, body []byte) []byte {
	return []byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` +
		html.EscapeString(name) + ` · phebs</title><link rel="icon" href="` + Prefix + `docs/phebs-mark.svg"><style>` + pageStyle + `</style></head><body><header><a href="/" aria-label="phebs code search">phebs</a></header><main>` +
		string(body) + `</main></body></html>`)
}

const pageStyle = `:root{color-scheme:light dark;--bg:#fbfcff;--fg:#242b38;--muted:#535d70;--line:#d0d6e2;--link:#1749b8;--code:#eef1f7}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.65 system-ui,-apple-system,sans-serif}header,main{width:min(100% - 40px,72ch);margin-inline:auto}header{padding-block:24px;border-bottom:1px solid var(--line)}header a{font-size:22px;font-weight:700;text-decoration:none}main{padding-block:24px 64px;overflow-wrap:anywhere}h1,h2,h3,h4{line-height:1.25;text-wrap:balance;margin-block:1.8em .65em;scroll-margin-top:16px}h1{font-size:2rem;margin-top:0}h2{font-size:1.5rem}h3{font-size:1.15rem}a{color:var(--link);text-underline-offset:.2em}a:hover{text-decoration-thickness:2px}:focus-visible{outline:3px solid var(--link);outline-offset:4px}pre,code{font-family:ui-monospace,"SF Mono",Menlo,monospace}code{background:var(--code);padding:.1em .25em;font-size:.88em}pre{background:var(--code);padding:16px;overflow:auto;white-space:pre;max-width:100%;font-size:.88em}pre code{padding:0;font-size:inherit}table{display:block;max-width:100%;overflow:auto;border-collapse:collapse;font-size:.9em}th,td{padding:8px 12px;border:1px solid var(--line);vertical-align:top;text-align:left}blockquote{margin-inline:0;padding-inline:16px;border-inline-start:1px solid var(--line);color:var(--muted)}img{max-width:100%;height:auto}hr{border:0;border-top:1px solid var(--line);margin-block:32px}li+li{margin-top:.35em}::selection{background:var(--link);color:var(--bg)}@media(prefers-color-scheme:dark){:root{--bg:#131820;--fg:#e2e8f2;--muted:#b4bfd0;--line:#49566b;--link:#9bbcff;--code:#202938}}@media(max-width:480px){header,main{width:calc(100% - 32px)}h1{font-size:1.75rem}}`

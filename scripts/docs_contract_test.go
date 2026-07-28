package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	markdownLinkPattern = regexp.MustCompile(`\]\(\s*(?:<([^>]+)>|([^\s)]+))`)
	htmlLinkPattern     = regexp.MustCompile(`(?i)\b(?:href|src)\s*=\s*["']([^"']+)["']`)
	inlineCodePattern   = regexp.MustCompile("`[^`]*`")
	schemePattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

func TestTrackedMarkdownLinksResolve(t *testing.T) {
	root := repositoryRoot(t)
	files := trackedFiles(t, root, "*.md")

	var failures []string
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, target := range documentTargets(data) {
			resolved, local, err := resolveDocumentTarget(root, name, target)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s -> %q: %v", name, target, err))
				continue
			}
			if !local {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(resolved))); err != nil {
				failures = append(failures, fmt.Sprintf("%s -> %q (%s): %v", name, target, resolved, err))
			}
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		t.Fatalf("tracked document links do not resolve:\n%s", strings.Join(failures, "\n"))
	}
}

func TestDocumentationMapReachesTrackedDocs(t *testing.T) {
	root := repositoryRoot(t)
	allMarkdown := trackedFiles(t, root, "*.md")
	tracked := make(map[string]struct{}, len(allMarkdown))
	for _, name := range allMarkdown {
		tracked[name] = struct{}{}
	}

	const start = "docs/README.md"
	reached := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, target := range documentTargets(data) {
			resolved, local, err := resolveDocumentTarget(root, name, target)
			if err != nil || !local {
				continue
			}
			if _, ok := tracked[resolved]; !ok || reached[resolved] {
				continue
			}
			reached[resolved] = true
			queue = append(queue, resolved)
		}
	}

	var unreachable []string
	for _, name := range allMarkdown {
		if strings.HasPrefix(name, "docs/") && !reached[name] {
			unreachable = append(unreachable, name)
		}
	}
	if len(unreachable) > 0 {
		t.Fatalf("docs/README.md does not reach these tracked documents:\n%s", strings.Join(unreachable, "\n"))
	}
}

func TestSealedT111TreeDigest(t *testing.T) {
	root := repositoryRoot(t)
	files := trackedFiles(t, root, "spike/t111")

	tree := sha256.New()
	_, _ = tree.Write([]byte("phebs-t111-sealed-tree-v1\x00"))
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read sealed file %s: %v", name, err)
		}
		fileDigest := sha256.Sum256(data)
		_, _ = tree.Write([]byte(name))
		_, _ = tree.Write([]byte{0})
		_, _ = tree.Write([]byte(hex.EncodeToString(fileDigest[:])))
		_, _ = tree.Write([]byte{0})
	}
	got := "sha256:" + hex.EncodeToString(tree.Sum(nil))
	const want = "sha256:d8f962f33d7c9fd3f21f70e696bd65b66d1c6ce88768462e72c9591a63108cc2"
	if got != want {
		t.Fatalf("sealed T11.1 tree changed: got %s, want %s", got, want)
	}
}

func TestCompletedBacklogArchiveDigest(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs", "BACKLOG_COMPLETED.md"))
	if err != nil {
		t.Fatalf("read completed backlog archive: %v", err)
	}
	digest := sha256.Sum256(data)
	got := "sha256:" + hex.EncodeToString(digest[:])
	const want = "sha256:b493f6c5be3aae54291b70e72f55e9e652deb9a5676f8bce9b41be6d2b44c971"
	if got != want {
		t.Fatalf("completed backlog archive changed: got %s, want %s", got, want)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func trackedFiles(t *testing.T, root string, pathspecs ...string) []string {
	t.Helper()
	args := []string{"ls-files", "-z", "--"}
	args = append(args, pathspecs...)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("list tracked files: %v", err)
	}
	raw := strings.Split(string(output), "\x00")
	files := make([]string, 0, len(raw))
	for _, name := range raw {
		if name != "" {
			files = append(files, filepath.ToSlash(name))
		}
	}
	sort.Strings(files)
	return files
}

func documentTargets(data []byte) []string {
	content := stripFencedCode(string(data))
	content = inlineCodePattern.ReplaceAllString(content, "")
	var targets []string
	for _, match := range markdownLinkPattern.FindAllStringSubmatch(content, -1) {
		target := match[1]
		if target == "" {
			target = match[2]
		}
		targets = append(targets, target)
	}
	for _, match := range htmlLinkPattern.FindAllStringSubmatch(content, -1) {
		targets = append(targets, match[1])
	}
	return targets
}

func stripFencedCode(content string) string {
	var output strings.Builder
	var fence string
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence == "" {
			switch {
			case strings.HasPrefix(trimmed, "```"):
				fence = "```"
				continue
			case strings.HasPrefix(trimmed, "~~~"):
				fence = "~~~"
				continue
			}
			output.WriteString(line)
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			fence = ""
		}
	}
	return output.String()
}

func resolveDocumentTarget(root, source, target string) (string, bool, error) {
	target = strings.TrimSpace(target)
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") || schemePattern.MatchString(target) {
		return "", false, nil
	}
	if parsed, err := url.PathUnescape(target); err == nil {
		target = parsed
	}
	if cut := strings.IndexAny(target, "?#"); cut >= 0 {
		target = target[:cut]
	}
	if target == "" {
		return "", false, nil
	}

	var absolute string
	if strings.HasPrefix(target, "/") {
		absolute = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(target, "/")))
	} else {
		absolute = filepath.Join(root, filepath.Dir(filepath.FromSlash(source)), filepath.FromSlash(target))
	}
	absolute = filepath.Clean(absolute)
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return "", true, err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", true, fmt.Errorf("target escapes repository")
	}
	return filepath.ToSlash(relative), true, nil
}

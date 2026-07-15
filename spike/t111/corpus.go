package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/modfile"
)

var (
	gitExecutable          = "git"
	goExecutable           = "go"
	restrictedToolPath     = os.Getenv("PATH")
	harnessGoHome          string
	harnessGoPath          string
	harnessGoCache         string
	harnessGoTmp           string
	harnessTelemetry       string
	harnessModuleCacheRoot string
)

// CorpusEntry pins one system to an exact commit. corpus.lock.json is the
// committed source of truth; clones land in spike/t111/corpus/ (gitignored).
type CorpusEntry struct {
	Name             string                   `json:"name"`
	GitURL           string                   `json:"git_url"`
	Commit           string                   `json:"commit"`
	Note             string                   `json:"note,omitempty"`
	ExcludedGitlinks []CorpusGitlinkExclusion `json:"excluded_gitlinks,omitempty"`
	GoBuildTags      []string                 `json:"go_build_tags,omitempty"`
}

// CorpusGitlinkExclusion is a reviewed boundary in the benchmark population.
// Both the path and the gitlink object ID must match the pinned parent tree;
// moving or updating the gitlink therefore invalidates the lock instead of
// silently changing which content the harness omits.
type CorpusGitlinkExclusion struct {
	Path     string `json:"path"`
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

func loadCorpus(lockPath string) ([]CorpusEntry, error) {
	b, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read corpus lock: %w", err)
	}
	var entries []CorpusEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("parse corpus lock: %w", err)
	}
	seen := make(map[string]bool, len(entries))
	for i := range entries {
		if err := validateCorpusEntry(entries[i]); err != nil {
			return nil, fmt.Errorf("corpus lock entry %d: %w", i+1, err)
		}
		if seen[entries[i].Name] {
			return nil, fmt.Errorf("corpus lock entry %d: duplicate system %q", i+1, entries[i].Name)
		}
		seen[entries[i].Name] = true
	}
	return entries, nil
}

func validateCorpusEntry(entry CorpusEntry) error {
	if entry.Name == "" || entry.GitURL == "" {
		return fmt.Errorf("name and git_url are required")
	}
	if !isLowerHexObjectID(entry.Commit) {
		return fmt.Errorf("commit %q is not a full lowercase SHA-1 object ID", entry.Commit)
	}
	seenPaths := make(map[string]bool, len(entry.ExcludedGitlinks))
	for _, exclusion := range entry.ExcludedGitlinks {
		clean := pathpkg.Clean(exclusion.Path)
		if clean != exclusion.Path || clean == "." || pathpkg.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("invalid excluded gitlink path %q", exclusion.Path)
		}
		if !isLowerHexObjectID(exclusion.ObjectID) {
			return fmt.Errorf("excluded gitlink %q has invalid object ID %q", exclusion.Path, exclusion.ObjectID)
		}
		if strings.TrimSpace(exclusion.Reason) == "" {
			return fmt.Errorf("excluded gitlink %q has no review rationale", exclusion.Path)
		}
		if seenPaths[exclusion.Path] {
			return fmt.Errorf("duplicate excluded gitlink path %q", exclusion.Path)
		}
		seenPaths[exclusion.Path] = true
	}
	seenTags := make(map[string]bool, len(entry.GoBuildTags))
	for _, tag := range entry.GoBuildTags {
		if tag == "" || strings.ContainsAny(tag, ", \t\r\n") {
			return fmt.Errorf("invalid Go build tag %q", tag)
		}
		for _, r := range tag {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '.' {
				return fmt.Errorf("invalid Go build tag %q", tag)
			}
		}
		if seenTags[tag] {
			return fmt.Errorf("duplicate Go build tag %q", tag)
		}
		seenTags[tag] = true
	}
	return nil
}

func isLowerHexObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// syncCorpus materializes each entry at its pinned commit via a shallow
// fetch. Idempotent: an existing checkout at the right commit is left alone.
func syncCorpus(entries []CorpusEntry, dir string) error {
	for _, e := range entries {
		dst := filepath.Join(dir, e.Name)
		if err := verifyCorpus(dst, e.Commit); err == nil {
			fmt.Printf("corpus %-18s ok at %s\n", e.Name, e.Commit[:12])
			continue
		}
		if _, statErr := os.Stat(filepath.Join(dst, ".git")); statErr == nil {
			if cur, err := gitOut(dst, "rev-parse", "--verify", "HEAD^{commit}"); err == nil && cur == e.Commit {
				return fmt.Errorf("%s: pinned checkout is dirty; preserve or remove local changes before sync", e.Name)
			}
			status, statusErr := gitOut(dst, "status", "--porcelain=v1", "--untracked-files=all")
			if statusErr != nil {
				return fmt.Errorf("%s: inspect existing checkout: %w", e.Name, statusErr)
			}
			if status != "" {
				return fmt.Errorf("%s: checkout is dirty; preserve or remove local changes before sync:\n%s", e.Name, status)
			}
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
		if _, err := os.Stat(filepath.Join(dst, ".git")); err != nil {
			if _, err := gitOut(dst, "init", "-q"); err != nil {
				return fmt.Errorf("%s: init: %w", e.Name, err)
			}
		}
		fmt.Printf("corpus %-18s fetching %s…\n", e.Name, e.Commit[:12])
		if _, err := gitOut(dst, "fetch", "--depth", "1", e.GitURL, e.Commit); err != nil {
			return fmt.Errorf("%s: fetch: %w", e.Name, err)
		}
		if _, err := gitOut(dst, "checkout", "-q", "--force", e.Commit); err != nil {
			return fmt.Errorf("%s: checkout: %w", e.Name, err)
		}
		if err := verifyCorpus(dst, e.Commit); err != nil {
			return fmt.Errorf("%s: verify synced corpus: %w", e.Name, err)
		}
	}
	return nil
}

// verifyCorpus proves that root is the repository pinned by the corpus lock
// and that extraction would observe precisely that commit's tracked tree plus
// no untracked inputs. Callers check both before and after extraction so a Go
// command (or a concurrent edit) cannot silently change the measured corpus.
func verifyCorpus(root, pinnedCommit string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("absolute corpus path: %w", err)
	}
	repoRoot, err := gitOut(rootAbs, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("locate repository: %w", err)
	}
	repoAbs, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("absolute repository path: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve corpus path: %w", err)
	}
	repoReal, err := filepath.EvalSymlinks(repoAbs)
	if err != nil {
		return fmt.Errorf("resolve repository path: %w", err)
	}
	if rootReal != repoReal {
		return fmt.Errorf("corpus path %q is inside repository %q, not its root", rootReal, repoReal)
	}

	head, err := gitOut(rootReal, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}
	pinned, err := gitOut(rootReal, "rev-parse", "--verify", pinnedCommit+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve pinned commit %q: %w", pinnedCommit, err)
	}
	if head != pinned || head != pinnedCommit {
		return fmt.Errorf("HEAD %s does not exactly match pinned commit %s", head, pinnedCommit)
	}
	status, err := gitOut(rootReal, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect corpus status: %w", err)
	}
	if status != "" {
		return fmt.Errorf("corpus has tracked or untracked changes:\n%s", status)
	}
	return nil
}

// snapshotCorpus materializes exact blob objects from the pinned recursive
// tree. It intentionally does not use git archive, export attributes, or the
// working tree. Gitlinks are rejected because their content is not identified
// by the parent tree alone. In-tree symlinks are created only after every blob
// path and target has been checked to remain inside the snapshot.
func snapshotCorpus(root string, entry CorpusEntry) (snapshot string, cleanup func(), err error) {
	if err := verifyCorpus(root, entry.Commit); err != nil {
		return "", nil, err
	}
	entries, err := recursiveTree(root, entry.Commit, entry.ExcludedGitlinks)
	if err != nil {
		return "", nil, err
	}
	snapshot, err = os.MkdirTemp("", "t111-corpus-*")
	if err != nil {
		return "", nil, fmt.Errorf("create corpus snapshot: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(snapshot) }

	if err := materializeTree(root, snapshot, entries); err != nil {
		cleanup()
		return "", nil, err
	}
	return snapshot, cleanup, nil
}

type treeEntry struct {
	mode, objectType, objectID, path string
}

func recursiveTree(root, commit string, exclusions []CorpusGitlinkExclusion) ([]treeEntry, error) {
	expected := make(map[string]CorpusGitlinkExclusion, len(exclusions))
	for _, exclusion := range exclusions {
		expected[exclusion.Path] = exclusion
	}
	raw, err := gitBytes(root, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return nil, fmt.Errorf("list pinned tree: %w", err)
	}
	items := bytes.Split(raw, []byte{0})
	entries := make([]treeEntry, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if len(item) == 0 {
			continue
		}
		parts := bytes.SplitN(item, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed ls-tree record %q", item)
		}
		meta := strings.Fields(string(parts[0]))
		if len(meta) != 3 {
			return nil, fmt.Errorf("malformed ls-tree metadata %q", parts[0])
		}
		name := string(parts[1])
		if clean := pathpkg.Clean(name); clean != name || clean == "." || pathpkg.IsAbs(clean) ||
			clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("unsafe pinned tree path %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate pinned tree path %q", name)
		}
		seen[name] = true
		entry := treeEntry{mode: meta[0], objectType: meta[1], objectID: meta[2], path: name}
		if entry.mode == "160000" || entry.objectType == "commit" {
			exclusion, ok := expected[name]
			if !ok {
				return nil, fmt.Errorf("gitlink %s is unsupported and not explicitly excluded: recursive content is not pinned by the parent blob tree", name)
			}
			if entry.mode != "160000" || entry.objectType != "commit" || entry.objectID != exclusion.ObjectID {
				return nil, fmt.Errorf("excluded gitlink %s changed: got %s %s %s, want 160000 commit %s", name, entry.mode, entry.objectType, entry.objectID, exclusion.ObjectID)
			}
			delete(expected, name)
			continue
		}
		if _, declared := expected[name]; declared {
			return nil, fmt.Errorf("excluded gitlink %s is now %s %s, not a gitlink", name, entry.mode, entry.objectType)
		}
		if entry.objectType != "blob" || (entry.mode != "100644" && entry.mode != "100755" && entry.mode != "120000") {
			return nil, fmt.Errorf("unsupported pinned tree entry %s (%s %s)", name, entry.mode, entry.objectType)
		}
		entries = append(entries, entry)
	}
	if len(expected) != 0 {
		missing := make([]string, 0, len(expected))
		for name := range expected {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("declared gitlink exclusions are absent from pinned tree: %s", strings.Join(missing, ", "))
	}
	return entries, nil
}

func materializeTree(repoRoot, snapshot string, entries []treeEntry) error {
	cmd := exec.Command(gitExecutable, "cat-file", "--batch")
	cmd.Dir = repoRoot
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open cat-file input: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open cat-file output: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cat-file: %w", err)
	}
	reader := bufio.NewReader(stdout)
	type symlink struct{ path, target string }
	var symlinks []symlink
	fail := func(cause error) error {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return cause
	}
	for _, entry := range entries {
		content, err := batchBlob(stdin, reader, entry.objectID)
		if err != nil {
			return fail(fmt.Errorf("read pinned blob %s (%s): %w", entry.path, entry.objectID, err))
		}
		if entry.mode == "120000" {
			target := string(content)
			resolved := pathpkg.Clean(pathpkg.Join(pathpkg.Dir(entry.path), target))
			if target == "" || pathpkg.IsAbs(target) || resolved == ".." || strings.HasPrefix(resolved, "../") {
				return fail(fmt.Errorf("symlink %s escapes snapshot or has an empty target: %q", entry.path, target))
			}
			symlinks = append(symlinks, symlink{path: entry.path, target: target})
			continue
		}
		dst := filepath.Join(snapshot, filepath.FromSlash(entry.path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(fmt.Errorf("create parent for %s: %w", entry.path, err))
		}
		mode := os.FileMode(0o644)
		if entry.mode == "100755" {
			mode = 0o755
		}
		if err := os.WriteFile(dst, content, mode); err != nil {
			return fail(fmt.Errorf("write %s: %w", entry.path, err))
		}
	}
	if err := stdin.Close(); err != nil {
		return fail(fmt.Errorf("close cat-file input: %w", err))
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("cat-file: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	for _, link := range symlinks {
		dst := filepath.Join(snapshot, filepath.FromSlash(link.path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("create parent for symlink %s: %w", link.path, err)
		}
		if err := os.Symlink(filepath.FromSlash(link.target), dst); err != nil {
			return fmt.Errorf("create symlink %s: %w", link.path, err)
		}
	}
	return nil
}

func batchBlob(stdin io.Writer, reader *bufio.Reader, objectID string) ([]byte, error) {
	if _, err := fmt.Fprintln(stdin, objectID); err != nil {
		return nil, err
	}
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != objectID || fields[1] != "blob" {
		return nil, fmt.Errorf("unexpected cat-file header %q", strings.TrimSpace(header))
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 || size > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("invalid blob size %q", fields[2])
	}
	content := make([]byte, int(size))
	if _, err := io.ReadFull(reader, content); err != nil {
		return nil, err
	}
	terminator, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if terminator != '\n' {
		return nil, fmt.Errorf("invalid cat-file blob terminator %q", terminator)
	}
	return content, nil
}

func validateFacts(entry CorpusEntry, facts []Fact, readBlob func(string) ([]byte, error)) error {
	if len(facts) == 0 {
		return fmt.Errorf("extractor emitted no facts")
	}
	validPredicates := map[string]bool{
		"DECLARES_OPERATION": true, "DECLARES_FIELD": true,
		"IMPLEMENTS_SERVICE": true, "CALLS_OPERATION": true,
		"REFERENCES_PROTO_FIELD": true,
		"K8S_WORKLOAD":           true, "WORKLOAD_RUNS_IMAGE": true,
		"K8S_SERVICE_DNS": true, "IMAGE_BUILD_CONTEXT": true,
		"SERVICE_NAME_LITERAL": true,
		"EXTRACTION_FAILURE":   true, "LOAD_ERRORS": true,
	}
	validTiers := map[string]bool{tierExact: true, tierDerived: true, tierHeuristic: true, tierUnresolved: true}
	validRoles := map[string]bool{roleVendor: true, roleMock: true, roleGenerated: true, roleTest: true, roleProduction: true}
	cache := map[string][]byte{}
	for i, fact := range facts {
		if fact.Predicate == "EXTRACTION_FAILURE" || fact.Predicate == "LOAD_ERRORS" {
			return fmt.Errorf("fact %d: extractor diagnostic %s in %s (%s): %s", i+1, fact.Predicate, fact.Subject, fact.Path, fact.Object)
		}
		if !validPredicates[fact.Predicate] {
			return fmt.Errorf("fact %d: unsupported predicate %q", i+1, fact.Predicate)
		}
		if !validTiers[fact.Tier] {
			return fmt.Errorf("fact %d: invalid confidence tier %q", i+1, fact.Tier)
		}
		if !validRoles[fact.CodeRole] {
			return fmt.Errorf("fact %d: invalid code role %q", i+1, fact.CodeRole)
		}
		if fact.Subject == "" || fact.Object == "" || fact.RuleID == "" {
			return fmt.Errorf("fact %d: predicate, subject, object, and rule ID must be nonempty", i+1)
		}
		if err := fact.validateAtom(); err != nil {
			return fmt.Errorf("fact %d: %w", i+1, err)
		}
		if fact.SchemaVersion != schemaVersion || fact.ExtractorVersion != extractorVersion ||
			fact.AdapterConfigDigest != adapterConfigDigest {
			return fmt.Errorf("fact %d: stale extractor provenance", i+1)
		}
		if fact.System != entry.Name || fact.Commit != entry.Commit {
			return fmt.Errorf("fact %d: occurrence is %s@%s, want %s@%s", i+1, fact.System, fact.Commit, entry.Name, entry.Commit)
		}
		clean := pathpkg.Clean(fact.Path)
		if clean != fact.Path || clean == "." || pathpkg.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("fact %d: invalid repository-relative path %q", i+1, fact.Path)
		}
		content, ok := cache[clean]
		if !ok {
			var err error
			content, err = readBlob(clean)
			if err != nil {
				return fmt.Errorf("fact %d: read pinned blob %s: %w", i+1, clean, err)
			}
			cache[clean] = content
		}
		if got := blobDigest(content); fact.BlobDigest != got {
			return fmt.Errorf("fact %d: blob digest %s does not match pinned blob %s", i+1, fact.BlobDigest, got)
		}
		if fact.StartByte < 0 || fact.EndByte <= fact.StartByte || fact.EndByte > len(content) {
			return fmt.Errorf("fact %d: span [%d,%d) is outside %s (%d bytes)", i+1, fact.StartByte, fact.EndByte, clean, len(content))
		}
		wantStartLine := lineAtOffset(content, fact.StartByte)
		wantEndLine := lineAtOffset(content, fact.EndByte)
		if fact.StartLine != wantStartLine || fact.EndLine != wantEndLine {
			return fmt.Errorf("fact %d: lines %d-%d do not match span-derived lines %d-%d", i+1,
				fact.StartLine, fact.EndLine, wantStartLine, wantEndLine)
		}
	}
	return nil
}

func validateSnapshotFacts(entry CorpusEntry, snapshot string, facts []Fact) error {
	return validateFacts(entry, facts, func(path string) ([]byte, error) {
		return os.ReadFile(filepath.Join(snapshot, filepath.FromSlash(path)))
	})
}

// verifyPinnedFacts is the read-only audit path: evidence bytes come directly
// from the pinned Git object rather than from a working-tree pathname.
func verifyPinnedFacts(entry CorpusEntry, root string, facts []Fact) error {
	if err := verifyCorpus(root, entry.Commit); err != nil {
		return err
	}
	entries, err := recursiveTree(root, entry.Commit, entry.ExcludedGitlinks)
	if err != nil {
		return err
	}
	byPath := make(map[string]treeEntry, len(entries))
	for _, treeEntry := range entries {
		byPath[treeEntry.path] = treeEntry
	}
	return validateFacts(entry, facts, func(path string) ([]byte, error) {
		return readPinnedSourceBlob(root, path, byPath)
	})
}

func readPinnedSourceBlob(root, sourcePath string, entries map[string]treeEntry) ([]byte, error) {
	current := sourcePath
	seen := make(map[string]struct{})
	for {
		if _, duplicate := seen[current]; duplicate {
			return nil, fmt.Errorf("pinned source symlink cycle at %s", sourcePath)
		}
		seen[current] = struct{}{}
		entry, ok := entries[current]
		if !ok {
			return nil, fmt.Errorf("pinned source %s resolves to absent path %s", sourcePath, current)
		}
		if entry.objectType != "blob" {
			return nil, fmt.Errorf("pinned source %s resolves to non-blob %s", sourcePath, current)
		}
		content, err := gitBytes(root, "cat-file", "blob", entry.objectID)
		if err != nil {
			return nil, err
		}
		switch entry.mode {
		case "100644", "100755":
			return content, nil
		case "120000":
			if !utf8.Valid(content) {
				return nil, fmt.Errorf("pinned source symlink %s has a non-UTF-8 target", current)
			}
			target := string(content)
			next := pathpkg.Clean(pathpkg.Join(pathpkg.Dir(current), target))
			if target == "" || strings.ContainsRune(target, '\x00') || pathpkg.IsAbs(target) || next == "." || next == ".." || strings.HasPrefix(next, "../") {
				return nil, fmt.Errorf("pinned source symlink %s escapes the tree via %q", current, target)
			}
			current = next
		default:
			return nil, fmt.Errorf("pinned source %s resolves to unsupported mode %s", sourcePath, entry.mode)
		}
	}
}

func configureHarnessTools() (func(), error) {
	originalPath := os.Getenv("PATH")
	goPath, goVersion, goDigest, err := resolveExecutable("go", "version")
	if err != nil {
		return nil, err
	}
	gitPath, gitVersion, gitDigest, err := resolveExecutable("git", "--version")
	if err != nil {
		return nil, err
	}
	toolDir, err := os.MkdirTemp("", "t111-tools-*")
	if err != nil {
		return nil, fmt.Errorf("create restricted tool path: %w", err)
	}
	goState, err := os.MkdirTemp("", "t111-go-state-*")
	if err != nil {
		_ = os.RemoveAll(toolDir)
		return nil, fmt.Errorf("create isolated Go state: %w", err)
	}
	for _, name := range []string{"home", "gopath", "build-cache", "tmp", "telemetry"} {
		if err := os.Mkdir(filepath.Join(goState, name), 0o700); err != nil {
			_ = os.RemoveAll(goState)
			_ = os.RemoveAll(toolDir)
			return nil, fmt.Errorf("create isolated Go %s directory: %w", name, err)
		}
	}
	cleanup := func() {
		_ = os.Setenv("PATH", originalPath)
		_ = os.RemoveAll(goState)
		_ = os.RemoveAll(toolDir)
		restrictedToolPath = originalPath
		harnessGoHome = ""
		harnessGoPath = ""
		harnessGoCache = ""
		harnessGoTmp = ""
		harnessTelemetry = ""
		harnessModuleCacheRoot = ""
	}
	if err := os.Symlink(goPath, filepath.Join(toolDir, "go")); err != nil {
		cleanup()
		return nil, fmt.Errorf("link exact go executable: %w", err)
	}
	if err := os.Symlink(gitPath, filepath.Join(toolDir, "git")); err != nil {
		cleanup()
		return nil, fmt.Errorf("link exact git executable: %w", err)
	}
	gitExecutable = gitPath
	goExecutable = goPath
	restrictedToolPath = toolDir
	harnessGoHome = filepath.Join(goState, "home")
	harnessGoPath = filepath.Join(goState, "gopath")
	harnessGoCache = filepath.Join(goState, "build-cache")
	harnessGoTmp = filepath.Join(goState, "tmp")
	harnessTelemetry = filepath.Join(goState, "telemetry")
	moduleCacheRoot, err := filepath.Abs(defaultModuleCacheRoot)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("resolve dedicated module cache: %w", err)
	}
	harnessModuleCacheRoot = moduleCacheRoot
	// Installation paths do not affect extraction semantics. Versions and
	// executable content do, so those—not machine-local paths—key provenance.
	identity := fmt.Sprintf("go_version=%q;go_digest=%s;git_version=%q;git_digest=%s",
		goVersion, goDigest, gitVersion, gitDigest)
	setToolchainIdentity(identity)
	if err := os.Setenv("PATH", restrictedToolPath); err != nil {
		cleanup()
		return nil, fmt.Errorf("restrict process PATH: %w", err)
	}
	return cleanup, nil
}

func resolveExecutable(name string, versionArgs ...string) (path, version, digest string, err error) {
	path, err = exec.LookPath(name)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve %s executable: %w", name, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", "", fmt.Errorf("absolute %s executable: %w", name, err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve %s executable symlinks: %w", name, err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", fmt.Errorf("hash %s executable: %w", name, err)
	}
	cmd := exec.Command(path, versionArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", "", fmt.Errorf("identify %s executable: %w: %s", name, err, out)
	}
	return path, strings.TrimSpace(string(out)), blobDigest(binary), nil
}

// validateLocalReplaces rejects host-dependent module inputs. A local replace
// is permitted only when its fully resolved target exists inside the immutable
// snapshot.
func validateLocalReplaces(snapshotRoot, modDir string) error {
	content, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return fmt.Errorf("parse go.mod: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(snapshotRoot)
	if err != nil {
		return fmt.Errorf("resolve snapshot root: %w", err)
	}
	for _, replacement := range parsed.Replace {
		if replacement.New.Version != "" {
			continue
		}
		if filepath.IsAbs(replacement.New.Path) {
			return fmt.Errorf("local replace %s => %s is absolute", replacement.Old.Path, replacement.New.Path)
		}
		target := filepath.Clean(filepath.Join(modDir, filepath.FromSlash(replacement.New.Path)))
		targetReal, err := filepath.EvalSymlinks(target)
		if err != nil {
			return fmt.Errorf("resolve local replace %s => %s: %w", replacement.Old.Path, replacement.New.Path, err)
		}
		rel, err := filepath.Rel(rootReal, targetReal)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("local replace %s => %s escapes the immutable snapshot", replacement.Old.Path, replacement.New.Path)
		}
		info, err := os.Stat(targetReal)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("local replace %s => %s is not an in-snapshot directory", replacement.Old.Path, replacement.New.Path)
		}
	}
	return nil
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command(gitExecutable, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	s := string(out)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s, nil
}

func gitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command(gitExecutable, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %v: %w: %s", args, err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("git %v: %w", args, err)
	}
	return out, nil
}

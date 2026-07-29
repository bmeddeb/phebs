package t301

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	neutralRepository = "example.com/neutral/monorepo"
	bulkNamePadding   = 78
)

type treeEntry struct {
	mode, kind, oid, name string
}

// GenerateRepository creates a bare deterministic Git repository without
// expanding the synthetic bulk tree into a worktree.
func GenerateRepository(
	ctx context.Context,
	repoDir string,
	irrelevantFiles int,
) (Corpus, error) {
	if irrelevantFiles <= CurrentCorpusFileLimit {
		return Corpus{}, fmt.Errorf(
			"irrelevant file count %d does not exceed current %d-file limit",
			irrelevantFiles, CurrentCorpusFileLimit,
		)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return Corpus{}, err
	}
	if _, err := runGit(ctx, repoDir, nil, "init", "--bare", "--quiet"); err != nil {
		return Corpus{}, err
	}
	irrelevantOID, err := writeBlob(ctx, repoDir, []byte("irrelevant bulk content\n"))
	if err != nil {
		return Corpus{}, err
	}
	bulkEntries := make([]treeEntry, 0, irrelevantFiles)
	var inventoryPathBytes int64
	for index := 0; index < irrelevantFiles; index++ {
		name := fmt.Sprintf(
			"irrelevant_%06d_%s.txt",
			index,
			strings.Repeat("x", bulkNamePadding),
		)
		bulkEntries = append(bulkEntries, treeEntry{
			mode: "100644", kind: "blob", oid: irrelevantOID, name: name,
		})
		inventoryPathBytes += int64(len("bulk/") + len(name))
	}
	bulkTree, err := writeTree(ctx, repoDir, bulkEntries)
	if err != nil {
		return Corpus{}, err
	}

	headTree, selectedCount, selectedPathBytes, err := buildRevisionTree(
		ctx, repoDir, bulkTree, "HEAD", false, "",
	)
	if err != nil {
		return Corpus{}, err
	}
	head, err := writeCommit(ctx, repoDir, headTree, "", "head")
	if err != nil {
		return Corpus{}, err
	}
	branchTree, _, _, err := buildRevisionTree(
		ctx, repoDir, bulkTree, "BRANCH", false, "",
	)
	if err != nil {
		return Corpus{}, err
	}
	branch, err := writeCommit(ctx, repoDir, branchTree, head, "branch")
	if err != nil {
		return Corpus{}, err
	}
	tagTree, _, _, err := buildRevisionTree(
		ctx, repoDir, bulkTree, "TAG", false, "",
	)
	if err != nil {
		return Corpus{}, err
	}
	tag, err := writeCommit(ctx, repoDir, tagTree, branch, "tag")
	if err != nil {
		return Corpus{}, err
	}
	missingTree, _, _, err := buildRevisionTree(
		ctx, repoDir, bulkTree, "MISSING", true, "",
	)
	if err != nil {
		return Corpus{}, err
	}
	missing, err := writeCommit(ctx, repoDir, missingTree, tag, "missing scope")
	if err != nil {
		return Corpus{}, err
	}
	symlinkTree, _, _, err := buildRevisionTree(
		ctx, repoDir, bulkTree, "SYMLINK", false, "symlink",
	)
	if err != nil {
		return Corpus{}, err
	}
	symlink, err := writeCommit(ctx, repoDir, symlinkTree, missing, "selected symlink")
	if err != nil {
		return Corpus{}, err
	}
	gitlinkTree, _, _, err := buildRevisionTree(
		ctx, repoDir, bulkTree, "GITLINK", false, "gitlink:"+head,
	)
	if err != nil {
		return Corpus{}, err
	}
	gitlink, err := writeCommit(ctx, repoDir, gitlinkTree, symlink, "selected gitlink")
	if err != nil {
		return Corpus{}, err
	}
	for ref, oid := range map[string]string{
		"refs/heads/main":    head,
		"refs/heads/release": branch,
		"refs/tags/v1":       tag,
		"refs/heads/missing": missing,
	} {
		if _, err := runGit(ctx, repoDir, nil, "update-ref", ref, oid); err != nil {
			return Corpus{}, err
		}
	}
	if _, err := runGit(
		ctx, repoDir, nil, "symbolic-ref", "HEAD", "refs/heads/main",
	); err != nil {
		return Corpus{}, err
	}

	corpus := Corpus{
		Schema: SchemaVersion, Repository: neutralRepository,
		Head: head, Branch: branch, Tag: tag, MissingScopeCommit: missing,
		SymlinkCommit: symlink, GitlinkCommit: gitlink,
		Revisions: []Revision{
			{Selector: "HEAD", Branch: "HEAD", Commit: head},
			{Selector: "release", Branch: "refs/heads/release", Commit: branch},
			{Selector: "v1", Branch: "refs/tags/v1", Commit: tag},
		},
		RegularFiles:       irrelevantFiles + selectedCount,
		InventoryPathBytes: inventoryPathBytes + selectedPathBytes,
	}
	corpus.GitObjectBytes, err = regularBytes(filepath.Join(repoDir, "objects"))
	if err != nil {
		return Corpus{}, err
	}
	corpus.Digest, err = CorpusDigest(corpus)
	if err != nil {
		return Corpus{}, err
	}
	if corpus.RegularFiles <= CurrentCorpusFileLimit ||
		corpus.InventoryPathBytes <= CurrentInventoryByteCap {
		return Corpus{}, fmt.Errorf(
			"generated corpus does not exceed both limits: files=%d paths=%d",
			corpus.RegularFiles, corpus.InventoryPathBytes,
		)
	}
	return corpus, nil
}

func buildRevisionTree(
	ctx context.Context,
	repoDir,
	bulkTree,
	label string,
	omitContract bool,
	special string,
) (string, int, int64, error) {
	files := map[string][]byte{
		"services/payments/src/main.go": []byte(
			"package payments\n\nconst T301_FOCUSED_NEEDLE_" + label +
				" = \"payments\"\n",
		),
		"services/payments/src/handler.go": []byte(
			"package payments\n\nfunc HandlePayment() string { return \"ok\" }\n",
		),
		"services/payments/generated/client.go": []byte(
			"package generated\n\n// generated from contracts/payment.proto\n",
		),
		"services/payments/go.mod": []byte(
			"module example.com/neutral/monorepo/services/payments\n\ngo 1.26\n",
		),
		"services/shipping/src/caller.go": []byte(
			"package shipping\n\n// repository-overlay caller outside focused scope\n",
		),
		"services/shipping/go.mod": []byte(
			"module example.com/neutral/monorepo/services/shipping\n\ngo 1.26\n",
		),
	}
	if !omitContract {
		files["contracts/payment.proto"] = []byte(
			"syntax = \"proto3\";\npackage neutral.payment.v1;\nservice Payment { rpc Get(GetRequest) returns (GetResponse); }\nmessage GetRequest {}\nmessage GetResponse {}\n",
		)
	}
	root, err := treeFromFiles(ctx, repoDir, files, bulkTree, special)
	if err != nil {
		return "", 0, 0, err
	}
	var pathBytes int64
	for filePath := range files {
		pathBytes += int64(len(filePath))
	}
	return root, len(files), pathBytes, nil
}

func treeFromFiles(
	ctx context.Context,
	repoDir string,
	files map[string][]byte,
	bulkTree,
	special string,
) (string, error) {
	type node struct {
		files map[string]treeEntry
		dirs  map[string]*node
	}
	root := &node{files: make(map[string]treeEntry), dirs: make(map[string]*node)}
	for filePath, content := range files {
		parts := strings.Split(filePath, "/")
		current := root
		for _, component := range parts[:len(parts)-1] {
			if current.dirs[component] == nil {
				current.dirs[component] = &node{
					files: make(map[string]treeEntry), dirs: make(map[string]*node),
				}
			}
			current = current.dirs[component]
		}
		oid, err := writeBlob(ctx, repoDir, content)
		if err != nil {
			return "", err
		}
		name := parts[len(parts)-1]
		current.files[name] = treeEntry{
			mode: "100644", kind: "blob", oid: oid, name: name,
		}
	}
	src := root.dirs["services"].dirs["payments"].dirs["src"]
	switch {
	case special == "symlink":
		oid, err := writeBlob(ctx, repoDir, []byte("main.go"))
		if err != nil {
			return "", err
		}
		src.files["alias.go"] = treeEntry{
			mode: "120000", kind: "blob", oid: oid, name: "alias.go",
		}
	case strings.HasPrefix(special, "gitlink:"):
		src.files["vendor"] = treeEntry{
			mode: "160000", kind: "commit",
			oid: strings.TrimPrefix(special, "gitlink:"), name: "vendor",
		}
	}

	var materialize func(*node) (string, error)
	materialize = func(current *node) (string, error) {
		entries := make([]treeEntry, 0, len(current.files)+len(current.dirs))
		for _, entry := range current.files {
			entries = append(entries, entry)
		}
		for name, child := range current.dirs {
			oid, err := materialize(child)
			if err != nil {
				return "", err
			}
			entries = append(entries, treeEntry{
				mode: "040000", kind: "tree", oid: oid, name: name,
			})
		}
		return writeTree(ctx, repoDir, entries)
	}
	for name, oid := range map[string]string{"bulk": bulkTree} {
		root.files[name] = treeEntry{
			mode: "040000", kind: "tree", oid: oid, name: name,
		}
	}
	return materialize(root)
}

func writeBlob(ctx context.Context, repoDir string, content []byte) (string, error) {
	return runGit(ctx, repoDir, content, "hash-object", "-w", "--stdin")
}

func writeTree(ctx context.Context, repoDir string, entries []treeEntry) (string, error) {
	slicesSortEntries(entries)
	var input bytes.Buffer
	for _, entry := range entries {
		_, _ = fmt.Fprintf(
			&input, "%s %s %s\t%s%c",
			entry.mode, entry.kind, entry.oid, entry.name, byte(0),
		)
	}
	return runGit(ctx, repoDir, input.Bytes(), "mktree", "-z")
}

func writeCommit(
	ctx context.Context,
	repoDir,
	tree,
	parent,
	message string,
) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	return runGit(ctx, repoDir, nil, args...)
}

func runGit(
	ctx context.Context,
	repoDir string,
	input []byte,
	args ...string,
) (string, error) {
	full := append([]string{"-C", repoDir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Stdin = bytes.NewReader(input)
	environment := make([]string, 0, len(os.Environ())+9)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			environment = append(environment, value)
		}
	}
	cmd.Env = append(environment,
		"GIT_AUTHOR_NAME=t301",
		"GIT_AUTHOR_EMAIL=t301@example.invalid",
		"GIT_AUTHOR_DATE=2001-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=t301",
		"GIT_COMMITTER_EMAIL=t301@example.invalid",
		"GIT_COMMITTER_DATE=2001-01-01T00:00:00Z",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_OPTIONAL_LOCKS=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func slicesSortEntries(entries []treeEntry) {
	// Git tree order treats directory names as if they end in slash. The
	// generated names do not share file/directory prefixes, so byte ordering
	// is equivalent here.
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].name < entries[right].name
	})
}

func parseIntEnv(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func cleanResultPath(value string) (string, error) {
	if value == "" {
		return "", errors.New("result path is required")
	}
	cleaned := filepath.Clean(value)
	if !filepath.IsAbs(cleaned) {
		return "", errors.New("result path must be absolute")
	}
	return cleaned, nil
}

func regularBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(
		filePath string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

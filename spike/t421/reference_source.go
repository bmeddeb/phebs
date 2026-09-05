package t421

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // Exact Git SHA-1 object compatibility, not a security primitive.
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const maxReferenceCommitBytes = 4 << 20

type executionReferenceSource struct {
	root         executionCheckoutInspector
	source       string
	tree         string
	entries      []executionCheckoutEntry
	configSHA256 string
}

// createExecutionReferenceSource copies only exact regular source blobs into a
// caller-owned private directory. Its fresh object store contains one commit
// and its complete tree, never source Git configuration, alternates, or history.
// The caller owns cleanup of destination and its private parent on every error.
func createExecutionReferenceSource(
	ctx context.Context, origin executionCheckoutInspector, source, destination string,
) (_ executionReferenceSource, retErr error) {
	if err := ctx.Err(); err != nil {
		return executionReferenceSource{}, fmt.Errorf("reference source canceled: %w", err)
	}
	if !validCommit(source) || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return executionReferenceSource{}, errors.New("reference source identity or destination is invalid")
	}
	parent := filepath.Dir(destination)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent || destination == origin.root ||
		strings.HasPrefix(destination, origin.root+string(filepath.Separator)) ||
		strings.HasPrefix(origin.root, destination+string(filepath.Separator)) {
		return executionReferenceSource{}, errors.New("reference source destination is not isolated")
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return executionReferenceSource{}, errors.New("reference source parent is not private")
	}
	rawTree, err := origin.run(ctx, maxCheckoutInventoryBytes, "ls-tree", "-rz", "--full-tree", source)
	if err != nil {
		return executionReferenceSource{}, err
	}
	entries, err := executionCheckoutEntries(rawTree)
	if err != nil {
		return executionReferenceSource{}, err
	}
	commit, err := origin.run(ctx, maxReferenceCommitBytes, "cat-file", "commit", source)
	if err != nil || gitSHA1ObjectID("commit", commit) != source {
		return executionReferenceSource{}, errors.New("reference source raw commit identity differs")
	}
	firstLine, _, ok := strings.Cut(string(commit), "\n")
	tree, hasTree := strings.CutPrefix(firstLine, "tree ")
	if !ok || !hasTree || !validCommit(tree) {
		return executionReferenceSource{}, errors.New("reference source commit tree is invalid")
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return executionReferenceSource{}, errors.New("reference source destination already exists or cannot be created")
	}
	input, err := os.OpenRoot(origin.root)
	if err != nil {
		return executionReferenceSource{}, errors.New("reference source cannot open origin")
	}
	defer func() { retErr = errors.Join(retErr, input.Close()) }()
	output, err := os.OpenRoot(destination)
	if err != nil {
		return executionReferenceSource{}, errors.New("reference source cannot open destination")
	}
	defer func() { retErr = errors.Join(retErr, output.Close()) }()
	var paths, index strings.Builder
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return executionReferenceSource{}, fmt.Errorf("reference source copy canceled: %w", err)
		}
		size, err := copyExecutionReferenceFile(input, output, entry, maxCheckoutBytes-total)
		if err != nil {
			return executionReferenceSource{}, err
		}
		total += size
		quoted := strconv.Quote(filepath.Join(destination, filepath.FromSlash(entry.path)))
		row := entry.mode + " " + entry.object + "\t" + entry.path + "\x00"
		if len(quoted)+1 > maxCheckoutInventoryBytes-paths.Len() || len(row) > maxCheckoutInventoryBytes-index.Len() {
			return executionReferenceSource{}, errors.New("reference source batch input exceeds its bound")
		}
		paths.WriteString(quoted)
		paths.WriteByte('\n')
		index.WriteString(row)
	}
	reference := executionReferenceSource{
		root:   executionCheckoutInspector{root: destination, git: origin.git, digest: origin.digest},
		source: source, tree: tree, entries: entries,
	}
	if _, err := reference.root.run(ctx, 4096, "init", "--quiet", "--template=", "--object-format=sha1"); err != nil {
		return executionReferenceSource{}, err
	}
	objects, err := reference.root.runInput(ctx, strings.NewReader(paths.String()), int64(len(entries)*41),
		"hash-object", "-w", "--no-filters", "--stdin-paths")
	if err != nil {
		return executionReferenceSource{}, err
	}
	objectRows := strings.SplitN(string(objects), "\n", len(entries)+2)
	if len(objectRows) != len(entries)+1 || objectRows[len(entries)] != "" {
		return executionReferenceSource{}, errors.New("reference source blob publication is incomplete")
	}
	for index, entry := range entries {
		if objectRows[index] != entry.object {
			return executionReferenceSource{}, errors.New("reference source published blob differs")
		}
	}
	if _, err := reference.root.runInput(ctx, strings.NewReader(index.String()), 0, "update-index", "-z", "--index-info"); err != nil {
		return executionReferenceSource{}, err
	}
	writtenTree, err := reference.root.run(ctx, 64, "write-tree")
	if err != nil || string(writtenTree) != tree+"\n" {
		return executionReferenceSource{}, errors.New("reference source published tree differs")
	}
	writtenCommit, err := reference.root.runInput(ctx, bytes.NewReader(commit), 64, "hash-object", "-w", "-t", "commit", "--stdin")
	if err != nil || string(writtenCommit) != source+"\n" {
		return executionReferenceSource{}, errors.New("reference source published commit differs")
	}
	shallow, err := output.OpenFile(".git/shallow", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return executionReferenceSource{}, errors.New("reference source cannot create shallow boundary")
	}
	_, writeErr := io.WriteString(shallow, source+"\n")
	if closeErr := shallow.Close(); writeErr != nil || closeErr != nil {
		return executionReferenceSource{}, errors.New("reference source cannot write shallow boundary")
	}
	if _, err := reference.root.run(ctx, 0, "update-ref", "--no-deref", "HEAD", source); err != nil {
		return executionReferenceSource{}, err
	}
	config, err := readReferenceControl(output, ".git/config", 64<<10)
	if err != nil {
		return executionReferenceSource{}, err
	}
	reference.configSHA256 = SHA256(config)
	if err := reference.verify(ctx); err != nil {
		return executionReferenceSource{}, err
	}
	return reference, nil
}

func copyExecutionReferenceFile(input, output *os.Root, entry executionCheckoutEntry, remaining int64) (int64, error) {
	for parent := filepath.Dir(entry.path); parent != "."; parent = filepath.Dir(parent) {
		info, err := input.Lstat(parent)
		if err != nil || !info.IsDir() {
			return 0, errors.New("reference source ancestor is not a regular directory")
		}
	}
	before, err := input.Lstat(entry.path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 0 ||
		before.Size() > maxCheckoutFileBytes || before.Size() > remaining {
		return 0, errors.New("reference source file is irregular or exceeds its byte bound")
	}
	mode := fs.FileMode(0o600)
	if entry.mode == "100755" {
		mode = 0o755
	}
	if before.Mode().Perm()&0o111 != mode&0o111 {
		return 0, errors.New("reference source executable mode differs")
	}
	if err := output.MkdirAll(filepath.Dir(entry.path), 0o700); err != nil {
		return 0, errors.New("reference source cannot create tracked directory")
	}
	source, err := input.Open(entry.path)
	if err != nil {
		return 0, errors.New("reference source cannot open tracked file")
	}
	opened, err := source.Stat()
	if err != nil || !sameCheckoutFile(before, opened) {
		_ = source.Close()
		return 0, errors.New("reference source file changed before copy")
	}
	destination, err := output.OpenFile(entry.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		_ = source.Close()
		return 0, errors.New("reference source cannot create tracked file")
	}
	//nolint:gosec // Hash exactly the raw Git blob while copying bounded bytes.
	digest := sha1.New()
	_, _ = fmt.Fprintf(digest, "blob %d\x00", before.Size())
	written, copyErr := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(source, before.Size()+1))
	after, statErr := source.Stat()
	sourceCloseErr := source.Close()
	modeErr := destination.Chmod(mode)
	destinationCloseErr := destination.Close()
	current, pathErr := input.Lstat(entry.path)
	if copyErr != nil || statErr != nil || sourceCloseErr != nil || modeErr != nil || destinationCloseErr != nil ||
		pathErr != nil || written != before.Size() || !sameCheckoutFile(before, after) || !sameCheckoutFile(before, current) ||
		fmt.Sprintf("%x", digest.Sum(nil)) != entry.object {
		return 0, errors.New("reference source copied file differs from exact source blob")
	}
	return written, nil
}

func (reference executionReferenceSource) verify(ctx context.Context) error {
	if !validCommit(reference.source) || !validCommit(reference.tree) || len(reference.entries) == 0 {
		return errors.New("reference source authority is absent")
	}
	root, err := os.OpenRoot(reference.root.root)
	if err != nil {
		return errors.New("reference source cannot open private source")
	}
	config, configErr := readReferenceControl(root, ".git/config", 64<<10)
	shallow, shallowErr := readReferenceControl(root, ".git/shallow", 64)
	controlErr := error(nil)
	for _, path := range []string{".git/info/grafts", ".git/info/attributes", ".git/info/exclude", ".git/objects/info/alternates", ".git/config.worktree"} {
		if _, err := root.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			controlErr = errors.New("reference source has unexpected Git controls")
			break
		}
	}
	if closeErr := root.Close(); configErr != nil || shallowErr != nil || controlErr != nil || closeErr != nil ||
		SHA256(config) != reference.configSHA256 || string(shallow) != reference.source+"\n" {
		return errors.New("reference source private Git controls changed")
	}
	for _, check := range []struct{ expression, expected string }{
		{"HEAD", reference.source}, {"HEAD^{tree}", reference.tree},
	} {
		value, err := reference.root.run(ctx, 64, "rev-parse", "--verify", check.expression)
		if err != nil || string(value) != check.expected+"\n" {
			return errors.New("reference source HEAD or tree changed")
		}
	}
	commit, err := reference.root.run(ctx, maxReferenceCommitBytes, "cat-file", "commit", reference.source)
	if err != nil || gitSHA1ObjectID("commit", commit) != reference.source {
		return errors.New("reference source raw commit changed")
	}
	rawTree, err := reference.root.run(ctx, maxCheckoutInventoryBytes, "ls-tree", "-rz", "--full-tree", reference.source)
	if err != nil {
		return err
	}
	entries, err := executionCheckoutEntries(rawTree)
	if err != nil || !slices.Equal(entries, reference.entries) {
		return errors.New("reference source tree inventory changed")
	}
	if err := reference.root.inventory(ctx, reference.entries); err != nil {
		return err
	}
	return inspectCheckoutFiles(ctx, reference.root.root, reference.entries)
}

func readReferenceControl(root *os.Root, path string, limit int64) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, errors.New("reference source Git control is irregular or over bound")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, errors.New("reference source cannot read Git control")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	if closeErr := file.Close(); readErr != nil || closeErr != nil || int64(len(raw)) > limit {
		return nil, errors.New("reference source Git control read failed or exceeded its bound")
	}
	return raw, nil
}

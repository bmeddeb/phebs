package t421

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // The frozen Git object format is SHA-1, not a security primitive.
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

const (
	maxCheckoutInventoryBytes = 64 << 20
	maxCheckoutEntries        = 100_000
	maxCheckoutFileBytes      = 256 << 20
	maxCheckoutBytes          = 2 << 30
)

// InspectExecutionCheckout measures the explicitly selected plan -> integration
// -> source lineage and the raw source checkout. It refuses ignored inputs too:
// run it on a dedicated clean checkout, with tools/build outputs kept outside it.
// This observation is NOT build provenance or a CheckoutAdmissionBinding. The
// eventual tool verifier must bind exact reference builds and the launcher must
// separately protect/revalidate its inputs; this function holds no mutation lock.
func InspectExecutionCheckout(
	ctx context.Context,
	repositoryRoot, gitBinary, planSource, integratedMain, source string,
) (ExecutionCommits, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionCommits{}, fmt.Errorf("checkout admission canceled: %w", err)
	}
	for _, commit := range []string{planSource, integratedMain, source} {
		if !validCommit(commit) {
			return ExecutionCommits{}, errors.New("checkout admission requires full lowercase commit identities")
		}
	}
	if !filepath.IsAbs(repositoryRoot) || !filepath.IsAbs(gitBinary) {
		return ExecutionCommits{}, errors.New("checkout admission requires absolute checkout and Git paths")
	}
	root, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return ExecutionCommits{}, errors.New("checkout admission cannot resolve checkout")
	}
	gitBinary, err = filepath.EvalSymlinks(gitBinary)
	if err != nil {
		return ExecutionCommits{}, errors.New("checkout admission cannot resolve Git")
	}
	digest, err := executableidentity.Digest(gitBinary)
	if err != nil {
		return ExecutionCommits{}, errors.New("checkout admission cannot admit Git executable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	inspection := executionCheckoutInspector{root: root, git: gitBinary, digest: digest}
	return inspectExecutionCheckout(ctx, inspection, planSource, integratedMain, source)
}

func inspectExecutionCheckout(ctx context.Context, inspection executionCheckoutInspector, planSource, integratedMain, source string) (ExecutionCommits, error) {
	before, err := inspection.authority(ctx, planSource, integratedMain, source)
	if err != nil {
		return ExecutionCommits{}, err
	}
	tree, err := inspection.run(ctx, maxCheckoutInventoryBytes, "ls-tree", "-rz", "--full-tree", source)
	if err != nil {
		return ExecutionCommits{}, err
	}
	entries, err := executionCheckoutEntries(tree)
	if err != nil {
		return ExecutionCommits{}, err
	}
	if err := inspection.inventory(ctx, entries); err != nil {
		return ExecutionCommits{}, err
	}
	if err := inspectCheckoutFiles(ctx, inspection.root, entries); err != nil {
		return ExecutionCommits{}, err
	}
	if err := inspection.inventory(ctx, entries); err != nil {
		return ExecutionCommits{}, err
	}
	after, err := inspection.authority(ctx, planSource, integratedMain, source)
	if err != nil {
		return ExecutionCommits{}, err
	}
	if before != after || inspection.checkGit(ctx) != nil {
		return ExecutionCommits{}, errors.New("checkout admission authority changed during inspection")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionCommits{}, fmt.Errorf("checkout admission canceled: %w", err)
	}
	return after, nil
}

type executionCheckoutInspector struct {
	root, git, digest string
	custody           *ExecutionGitCustody
}

func (inspection executionCheckoutInspector) checkGit(ctx context.Context) error {
	if inspection.custody != nil {
		identity, path, err := inspection.custody.Check(ctx)
		if err != nil || identity.SHA256 != inspection.digest || path != inspection.git {
			return ErrExecutionGitCustody
		}
		return nil
	}
	return executableidentity.Verify(inspection.git, inspection.digest)
}

// No status/diff command is used: those can invoke checkout-local filters and
// trust index stat caches. Each child gets a closed environment regardless of
// the admitted executable's basename. No private child diagnostic is returned.
func (inspection executionCheckoutInspector) run(ctx context.Context, limit int64, args ...string) ([]byte, error) {
	return inspection.runInput(ctx, nil, limit, args...)
}

// runInput also supports bounded native Git writes into newly owned reference
// metadata; ordinary checkout inspection always supplies nil input.
func (inspection executionCheckoutInspector) runInput(ctx context.Context, input io.Reader, limit int64, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checkout admission canceled: %w", err)
	}
	if err := inspection.checkGit(ctx); err != nil {
		return nil, errors.New("checkout admission Git executable changed")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, inspection.git, append([]string{
		"--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false",
		"-c", "core.hooksPath=" + os.DevNull, "-c", "core.commitGraph=false",
	}, args...)...)
	command.Dir = inspection.root
	command.Stdin = input
	command.Env = []string{
		"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC",
		"GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
	}
	if inspection.custody != nil {
		private := filepath.Dir(inspection.custody.Directory())
		environment, err := inspection.custody.Environment(ctx, private, private)
		if err != nil {
			return nil, ErrExecutionGitCustody
		}
		command.Env = environment
	}
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	output := checkoutCommandOutput{remaining: limit, cancel: cancel}
	command.Stdout = &output
	runErr := command.Run()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checkout admission Git deadline: %w", err)
	}
	if runErr != nil {
		return nil, errors.New("checkout admission Git read failed or exceeded its bound")
	}
	return output.buffer.Bytes(), nil
}

// Using Cmd's writer pump lets WaitDelay close an inherited output pipe on
// cancellation. Reading StdoutPipe before Wait would bypass that deadline.
type checkoutCommandOutput struct {
	buffer    bytes.Buffer
	remaining int64
	cancel    context.CancelFunc
}

func (output *checkoutCommandOutput) Write(raw []byte) (int, error) {
	if int64(len(raw)) > output.remaining {
		output.cancel()
		return 0, errors.New("checkout admission Git output exceeds its bound")
	}
	output.remaining -= int64(len(raw))
	return output.buffer.Write(raw)
}

func (inspection executionCheckoutInspector) authority(
	ctx context.Context, planSource, integratedMain, source string,
) (ExecutionCommits, error) {
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "--show-toplevel"}, inspection.root},
		{[]string{"rev-parse", "--is-shallow-repository"}, "false"},
		{[]string{"rev-parse", "--show-object-format"}, "sha1"},
		{[]string{"rev-parse", "--verify", "HEAD"}, source},
	} {
		output, err := inspection.run(ctx, 4096, check.args...)
		if err != nil {
			return ExecutionCommits{}, err
		}
		if string(output) != check.want+"\n" {
			return ExecutionCommits{}, errors.New("checkout admission root, HEAD, or object database differs")
		}
	}
	grafts, err := inspection.run(ctx, 4096, "rev-parse", "--path-format=absolute", "--git-path", "info/grafts")
	if err != nil {
		return ExecutionCommits{}, err
	}
	graftPath := strings.TrimSuffix(string(grafts), "\n")
	if !filepath.IsAbs(graftPath) {
		return ExecutionCommits{}, errors.New("checkout admission graft path is invalid")
	}
	if _, err := os.Lstat(graftPath); !errors.Is(err, fs.ErrNotExist) {
		return ExecutionCommits{}, errors.New("checkout admission refuses grafts or unavailable graft state")
	}
	var trees [3]string
	for index, commit := range []string{planSource, integratedMain, source} {
		// Peeling a tag must not turn a supplied non-commit identity into authority.
		kind, err := inspection.run(ctx, 64, "cat-file", "-t", commit)
		if err != nil || string(kind) != "commit\n" {
			return ExecutionCommits{}, errors.New("checkout admission identity is not an available commit")
		}
		output, err := inspection.run(ctx, 64, "rev-parse", "--verify", commit+"^{tree}")
		if err != nil {
			return ExecutionCommits{}, err
		}
		trees[index] = strings.TrimSuffix(string(output), "\n")
		if !validCommit(trees[index]) {
			return ExecutionCommits{}, errors.New("checkout admission tree identity is invalid")
		}
	}
	for _, pair := range [][2]string{{planSource, integratedMain}, {integratedMain, source}} {
		if _, err := inspection.run(ctx, 0, "merge-base", "--is-ancestor", pair[0], pair[1]); err != nil {
			return ExecutionCommits{}, err
		}
	}
	return ExecutionCommits{
		IntegratedMainCommit: integratedMain, IntegratedMainTree: trees[1],
		T422SourceCommit: source, T422SourceTree: trees[2], CleanTree: true,
		IntegratedMainDescendsFromPlanSource: true, SourceDescendsFromIntegratedMain: true,
	}, nil
}

type executionCheckoutEntry struct{ path, object, mode string }

func executionCheckoutEntries(raw []byte) ([]executionCheckoutEntry, error) {
	records := strings.SplitN(string(raw), "\x00", maxCheckoutEntries+2)
	if len(records) < 2 || records[len(records)-1] != "" || len(records)-1 > maxCheckoutEntries {
		return nil, errors.New("checkout admission tree inventory is empty, truncated, or over bound")
	}
	entries := make([]executionCheckoutEntry, 0, len(records)-1)
	prior := ""
	for _, record := range records[:len(records)-1] {
		header, path, ok := strings.Cut(record, "\t")
		fields := strings.SplitN(header, " ", 4)
		if !ok || len(fields) != 3 || fields[1] != "blob" ||
			(fields[0] != "100644" && fields[0] != "100755") || !validCommit(fields[2]) ||
			!fs.ValidPath(path) || path == "." || strings.ContainsAny(path, "\\\r\n") || path <= prior {
			return nil, errors.New("checkout admission tree contains an unsupported entry")
		}
		for part := range strings.SplitSeq(path, "/") {
			if strings.EqualFold(part, ".git") {
				return nil, errors.New("checkout admission tree contains Git metadata")
			}
		}
		entries = append(entries, executionCheckoutEntry{path: path, object: fields[2], mode: fields[0]})
		prior = path
	}
	return entries, nil
}

func (inspection executionCheckoutInspector) inventory(ctx context.Context, entries []executionCheckoutEntry) error {
	index, err := inspection.run(ctx, maxCheckoutInventoryBytes, "ls-files", "-v", "--stage", "-z")
	if err != nil {
		return err
	}
	records := strings.SplitN(string(index), "\x00", len(entries)+2)
	if len(records) != len(entries)+1 || records[len(records)-1] != "" {
		return errors.New("checkout admission index differs from the source tree")
	}
	for index, entry := range entries {
		if records[index] != "H "+entry.mode+" "+entry.object+" 0\t"+entry.path {
			return errors.New("checkout admission index is changed, hidden, or unmerged")
		}
	}
	for _, args := range [][]string{
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "-z"},
	} {
		output, err := inspection.run(ctx, 0, args...)
		if err != nil {
			return err
		}
		if len(output) != 0 {
			return errors.New("checkout admission refuses untracked or ignored inputs")
		}
	}
	return nil
}

func inspectCheckoutFiles(ctx context.Context, path string, entries []executionCheckoutEntry) (retErr error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return errors.New("checkout admission cannot open source root")
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("checkout admission file inspection canceled: %w", err)
		}
		for parent := filepath.Dir(entry.path); parent != "."; parent = filepath.Dir(parent) {
			info, err := root.Lstat(parent)
			if err != nil || !info.IsDir() {
				return errors.New("checkout admission tracked ancestor is not a directory")
			}
		}
		size, err := inspectCheckoutFile(root, entry, maxCheckoutBytes-total)
		if err != nil {
			return err
		}
		total += size
	}
	return nil
}

func inspectCheckoutFile(root *os.Root, entry executionCheckoutEntry, remaining int64) (int64, error) {
	before, err := root.Lstat(entry.path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 0 ||
		before.Size() > maxCheckoutFileBytes || before.Size() > remaining {
		return 0, errors.New("checkout admission tracked file is unavailable, irregular, or over bound")
	}
	wantExecutable := fs.FileMode(0)
	if entry.mode == "100755" {
		wantExecutable = 0o111
	}
	if before.Mode().Perm()&0o111 != wantExecutable {
		return 0, errors.New("checkout admission tracked executable mode differs")
	}
	file, err := root.Open(entry.path)
	if err != nil {
		return 0, errors.New("checkout admission cannot open tracked file")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !sameCheckoutFile(before, opened) {
		_ = file.Close()
		return 0, errors.New("checkout admission tracked file changed before hashing")
	}
	//nolint:gosec // Compare raw Git blob identity, never a filter or stat-cache result.
	digest := sha1.New()
	_, _ = fmt.Fprintf(digest, "blob %d\x00", before.Size())
	written, readErr := io.Copy(digest, io.LimitReader(file, before.Size()+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, pathErr := root.Lstat(entry.path)
	if readErr != nil || statErr != nil || closeErr != nil || pathErr != nil || written != before.Size() ||
		!sameCheckoutFile(before, after) || !sameCheckoutFile(before, current) ||
		fmt.Sprintf("%x", digest.Sum(nil)) != entry.object {
		return 0, errors.New("checkout admission raw tracked file differs from its source blob")
	}
	return written, nil
}

func sameCheckoutFile(left, right fs.FileInfo) bool {
	return os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

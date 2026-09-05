package t421

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// AuthoredExecutionRevision contains actual independently checked Git outputs,
// not fixture object IDs. It is source-free evidence, not a transferable input
// admission or permission to resume an author in another process.
type AuthoredExecutionRevision struct {
	Name         string                 `json:"name"`
	Commit       string                 `json:"commit"`
	Tree         string                 `json:"tree"`
	ParentCommit string                 `json:"parent_commit,omitempty"`
	Manifest     AuthoredSourceManifest `json:"manifest"`
}

// ExecutionCorpusAuthor advances one new private bare repository A -> B -> A.
// It holds no writable source FD between commands and never authors a future
// revision. The launcher still owns the source mutation lease, enclosing volume
// and parent-held tool/input custody; this object is not a freeze binding.
// Close releases its directory descriptor, never deletes retained source.
type ExecutionCorpusAuthor struct {
	mu        sync.Mutex
	lifetime  context.Context
	source    *corpusAuthorSource
	directory string
	root      *os.File
	rootInfo  os.FileInfo
	volume    [2]int32
	config    string
	previous  AuthoredExecutionRevision
	next      int
	closed    bool
	err       error
	// Only the public constructor installs the real, fail-closed author
	// bootstrap. Tiny tests install an actual controller/client transport.
	gitPath         string
	start           func(context.Context, *exec.Cmd) (dispatchadmission.Handle, error)
	requestRevision string
	requestUsed     bool
	planFile        *os.File
	planInfo        os.FileInfo
	planPath        string
}

// NewExecutionCorpusAuthor accepts bounded canonical frozen V3 plan bytes,
// never caller-created expected identities. A live explicit author bootstrap
// must already be installed. Its owning parent must bind the plan and new
// source root before permitting any command; bootstrap alone is not that proof.
// Plan validation regenerates the frozen expected census but authors no Git.
func NewExecutionCorpusAuthor(ctx context.Context, planBytes []byte, parent string) (*ExecutionCorpusAuthor, error) {
	if ctx == nil || ctx.Err() != nil || len(planBytes) > MaxPlanBytes ||
		dispatchadmission.RequireAuthorBootstrap() != nil || !executionGitPrivateDirectory(parent) {
		return nil, ErrExecutionCorpusAuthor
	}
	plan, err := DecodePlan(planBytes)
	if err != nil || plan.Schema != PlanV3Schema || ctx.Err() != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	source, err := newCorpusAuthorSource(ctx, plan)
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	author, err := newExecutionCorpusAuthor(ctx, source, parent, dispatchadmission.ProductionTool("git"), dispatchadmission.StartAuthor)
	if author != nil {
		author.lifetime = dispatchadmission.ProcessContext()
	}
	return author, err
}

func newExecutionCorpusAuthor(ctx context.Context, source *corpusAuthorSource, parent, gitPath string,
	start func(context.Context, *exec.Cmd) (dispatchadmission.Handle, error),
) (*ExecutionCorpusAuthor, error) {
	if ctx == nil || ctx.Err() != nil || source == nil || start == nil ||
		!executionGitPrivateDirectory(parent) || !executionGitAbsolutePath(gitPath) {
		return nil, ErrExecutionCorpusAuthor
	}
	directory, err := os.MkdirTemp(parent, "t422-source-")
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	author := &ExecutionCorpusAuthor{lifetime: ctx, source: source, directory: directory, gitPath: gitPath, start: start}
	author.root, err = t4013.OpenHostImage(directory)
	if err == nil {
		author.rootInfo, err = author.root.Stat()
	}
	if err == nil {
		author.volume, err = inputCustodyVolume(author.root)
	}
	if err != nil || author.checkRoot(ctx) != nil {
		author.err = ErrExecutionCorpusAuthor
		_ = author.Close()
		return author, ErrExecutionCorpusAuthor
	}
	return author, nil
}

// Directory is private cleanup/input-custody information, not returned evidence.
func (author *ExecutionCorpusAuthor) Directory() string {
	if author == nil {
		return ""
	}
	return author.directory
}

func (author *ExecutionCorpusAuthor) Close() error {
	if author == nil {
		return nil
	}
	author.mu.Lock()
	defer author.mu.Unlock()
	if !author.closed {
		author.closed = true
		if author.root != nil && author.root.Close() != nil {
			author.err = ErrExecutionCorpusAuthor
		}
		if author.planFile != nil && author.planFile.Close() != nil {
			author.err = ErrExecutionCorpusAuthor
		}
	}
	return author.err
}

// AuthorNext admits exactly four direct Git commands for A and three for each
// later revision. There is no retry, resume, alternate ref, or preauthored B.
// Failures are sticky and retain the source root; no success manifest escapes.
func (author *ExecutionCorpusAuthor) AuthorNext(ctx context.Context, name string) (AuthoredExecutionRevision, error) {
	if author == nil {
		return AuthoredExecutionRevision{}, ErrExecutionCorpusAuthor
	}
	author.mu.Lock()
	defer author.mu.Unlock()
	if ctx != nil && author.lifetime != nil {
		operation, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(author.lifetime, cancel)
		defer func() { stop(); cancel() }()
		if author.lifetime.Err() != nil {
			cancel()
		}
		ctx = operation
	}
	fail := func() (AuthoredExecutionRevision, error) {
		author.err = ErrExecutionCorpusAuthor
		return AuthoredExecutionRevision{}, errors.Join(ErrExecutionCorpusAuthor, corpusAuthorContextError(ctx))
	}
	if author.checkRoot(ctx) != nil || author.next >= len(author.source.revisions) ||
		name != author.source.revisions[author.next].Name ||
		author.requestRevision != "" && (author.requestUsed || name != author.requestRevision) {
		return fail()
	}
	if author.requestRevision != "" {
		author.requestUsed = true
	}
	if author.next == 0 {
		if _, err := author.runOutput(ctx, 0, 4096); err != nil {
			return fail()
		}
		config, err := author.readControl("config", 4096)
		if err != nil {
			return fail()
		}
		author.config = SHA256(config)
	}
	if author.checkContinuity(ctx, author.previous.Commit) != nil || author.importRevision(ctx) != nil {
		return fail()
	}
	output, err := author.runOutput(ctx, 2, 82)
	if err != nil {
		return fail()
	}
	physical := author.source.revisions[author.next]
	if string(output) != physical.ExpectedCommit+"\n"+physical.ExpectedTree+"\n" {
		return fail()
	}
	identity, err := author.verifyInventory(ctx)
	if err != nil || author.checkContinuity(ctx, physical.ExpectedCommit) != nil {
		return fail()
	}
	commitBytes, err := canonicalGitCommitBytesFor(physical.ExpectedTree, author.previous.Commit, physical.CommitMessage, author.source.recipe)
	if err != nil || gitSHA1ObjectID("commit", commitBytes) != physical.ExpectedCommit {
		return fail()
	}
	result := AuthoredExecutionRevision{Name: name, Commit: physical.ExpectedCommit, Tree: physical.ExpectedTree,
		ParentCommit: author.previous.Commit, Manifest: author.source.manifest(physical, identity.Inventory, SHA256(commitBytes))}
	author.previous = result
	author.next++
	return result, nil
}

func corpusAuthorContextError(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	return ctx.Err()
}

func (author *ExecutionCorpusAuthor) checkRoot(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || author.closed || author.err != nil || author.root == nil || author.rootInfo == nil {
		return ErrExecutionCorpusAuthor
	}
	canonical, err := filepath.EvalSymlinks(author.directory)
	if err != nil || canonical != author.directory {
		return ErrExecutionCorpusAuthor
	}
	info, err := os.Lstat(author.directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || !inputCustodyOwned(info) || !os.SameFile(info, author.rootInfo) {
		return ErrExecutionCorpusAuthor
	}
	current, err := author.root.Stat()
	if err != nil || !os.SameFile(info, current) {
		return ErrExecutionCorpusAuthor
	}
	volume, err := inputCustodyVolume(author.root)
	if err != nil || volume != author.volume || author.checkPlan(ctx) != nil {
		return ErrExecutionCorpusAuthor
	}
	return nil
}

// Native continuity adds no Git command. This exact author uses only symbolic
// HEAD and a single loose main ref; packed refs/alternates/replacements are not
// admitted. The coordinator's source lease excludes other writers; these checks
// are drift detection, not a malicious same-user filesystem sandbox.
func (author *ExecutionCorpusAuthor) checkContinuity(ctx context.Context, commit string) error {
	if author.checkRoot(ctx) != nil {
		return ErrExecutionCorpusAuthor
	}
	for _, path := range []string{"refs", "refs/heads", "refs/tags", "objects", "objects/info"} {
		info, err := os.Lstat(filepath.Join(author.directory, path))
		if err != nil || !info.IsDir() || !inputCustodyOwned(info) {
			return ErrExecutionCorpusAuthor
		}
	}
	for _, path := range []string{"packed-refs", "objects/info/alternates", "objects/info/http-alternates", "refs/replace", "commondir", "shallow", "hooks"} {
		if _, err := os.Lstat(filepath.Join(author.directory, path)); !errors.Is(err, os.ErrNotExist) {
			return ErrExecutionCorpusAuthor
		}
	}
	head, err := author.readControl("HEAD", 64)
	if err != nil || string(head) != "ref: refs/heads/main\n" {
		return ErrExecutionCorpusAuthor
	}
	config, err := author.readControl("config", 4096)
	if err != nil || SHA256(config) != author.config {
		return ErrExecutionCorpusAuthor
	}
	if commit == "" {
		if _, err := os.Lstat(filepath.Join(author.directory, "refs/heads/main")); !errors.Is(err, os.ErrNotExist) {
			return ErrExecutionCorpusAuthor
		}
	} else {
		ref, err := author.readControl("refs/heads/main", 41)
		if err != nil || string(ref) != commit+"\n" {
			return ErrExecutionCorpusAuthor
		}
	}
	for _, item := range []struct{ directory, entry string }{{"refs", ""}, {"refs/heads", "main"}, {"refs/tags", ""}} {
		file, err := t4013.OpenHostImage(filepath.Join(author.directory, item.directory))
		if err != nil {
			return ErrExecutionCorpusAuthor
		}
		entries, readErr := file.ReadDir(3)
		closeErr := file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
			return ErrExecutionCorpusAuthor
		}
		if item.directory == "refs" {
			if len(entries) != 2 {
				return ErrExecutionCorpusAuthor
			}
			left, right := entries[0].Name(), entries[1].Name()
			if left != "heads" && left != "tags" || right != "heads" && right != "tags" || left == right {
				return ErrExecutionCorpusAuthor
			}
		} else if item.entry == "" || commit == "" {
			if len(entries) != 0 {
				return ErrExecutionCorpusAuthor
			}
		} else if len(entries) != 1 || entries[0].Name() != item.entry {
			return ErrExecutionCorpusAuthor
		}
	}
	return nil
}

func (author *ExecutionCorpusAuthor) readControl(path string, maximum int64) (result []byte, retErr error) {
	file, err := t4013.OpenHostImage(filepath.Join(author.directory, path))
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	defer func() {
		if file.Close() != nil {
			result, retErr = nil, ErrExecutionCorpusAuthor
		}
	}()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || !inputCustodyOwned(before) || before.Size() > maximum {
		return nil, ErrExecutionCorpusAuthor
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	after, statErr := file.Stat()
	pathInfo, pathErr := os.Lstat(filepath.Join(author.directory, path))
	if err != nil || statErr != nil || pathErr != nil || len(raw) > int(maximum) ||
		!inputCustodySame(before, after) || !inputCustodySame(after, pathInfo) {
		return nil, ErrExecutionCorpusAuthor
	}
	return raw, nil
}

// This is the single source-owned author dispatch site, not four invented
// subcommand sites. Pipe handling and Wait stay with the command owner.
func (author *ExecutionCorpusAuthor) startGit(ctx context.Context, command *exec.Cmd) (dispatchadmission.Handle, error) {
	if author.checkRoot(ctx) != nil {
		return dispatchadmission.Handle{}, ErrExecutionCorpusAuthor
	}
	return author.start(ctx, command)
}

func (author *ExecutionCorpusAuthor) command(ctx context.Context, index int) (*exec.Cmd, context.CancelFunc, error) {
	if author.checkRoot(ctx) != nil || index < 0 || index >= len(correctedAuthorGitCommands()) {
		return nil, nil, ErrExecutionCorpusAuthor
	}
	args := correctedAuthorGitCommands()[index].NormalizedArgv
	for i, value := range args {
		if value == "@source" {
			args[i] = author.directory
		}
	}
	commandCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(commandCtx, author.gitPath, args...)
	command.Dir = author.directory
	command.WaitDelay = time.Second
	if err := prepareReferenceCommand(command); err != nil {
		cancel()
		return nil, nil, ErrExecutionCorpusAuthor
	}
	command.Stderr = &checkoutCommandOutput{remaining: 8 << 10, cancel: cancel}
	return command, cancel, nil
}

func (author *ExecutionCorpusAuthor) runOutput(ctx context.Context, index, maximum int) ([]byte, error) {
	command, cancel, err := author.command(ctx, index)
	if err != nil {
		return nil, err
	}
	defer cancel()
	output := &checkoutCommandOutput{remaining: int64(maximum), cancel: cancel}
	command.Stdout = output
	handle, err := author.startGit(ctx, command)
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	if err := handle.Wait(); err != nil || ctx.Err() != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	return output.buffer.Bytes(), nil
}

func (author *ExecutionCorpusAuthor) importRevision(ctx context.Context) error {
	command, cancel, err := author.command(ctx, 1)
	if err != nil {
		return err
	}
	defer cancel()
	input, err := command.StdinPipe()
	if err != nil {
		return ErrExecutionCorpusAuthor
	}
	defer func() { _ = input.Close() }()
	command.Stdout = &checkoutCommandOutput{remaining: 4096, cancel: cancel}
	handle, err := author.startGit(ctx, command)
	if err != nil {
		return ErrExecutionCorpusAuthor
	}
	joined := false
	defer func() {
		if !joined {
			cancel()
			_ = handle.Wait()
		}
	}()
	writer := bufio.NewWriterSize(input, 64<<10)
	writeErr := author.source.writeRevision(ctx, writer, author.next, author.previous.Commit)
	if writeErr == nil {
		writeErr = writer.Flush()
	}
	closeErr := input.Close()
	if writeErr != nil || closeErr != nil {
		cancel()
	}
	waitErr := handle.Wait()
	joined = true
	if writeErr != nil || closeErr != nil || waitErr != nil || ctx.Err() != nil {
		return ErrExecutionCorpusAuthor
	}
	return nil
}

func (author *ExecutionCorpusAuthor) verifyInventory(ctx context.Context) (sourceTreeIdentity, error) {
	command, cancel, err := author.command(ctx, 3)
	if err != nil {
		return sourceTreeIdentity{}, err
	}
	defer cancel()
	output, err := command.StdoutPipe()
	if err != nil {
		return sourceTreeIdentity{}, ErrExecutionCorpusAuthor
	}
	defer func() { _ = output.Close() }()
	handle, err := author.startGit(ctx, command)
	if err != nil {
		return sourceTreeIdentity{}, ErrExecutionCorpusAuthor
	}
	joined := false
	defer func() {
		if !joined {
			cancel()
			_ = handle.Wait()
		}
	}()
	identity, readErr := author.source.verifyInventory(ctx, output, author.next)
	if readErr != nil {
		cancel()
	}
	waitErr := handle.Wait()
	joined = true
	if readErr != nil || waitErr != nil || ctx.Err() != nil {
		return sourceTreeIdentity{}, ErrExecutionCorpusAuthor
	}
	return identity, nil
}

func (source *corpusAuthorSource) manifest(physical PhysicalRevision, inventory SetIdentity, commitSHA256 string) AuthoredSourceManifest {
	recipe := source.recipe
	return AuthoredSourceManifest{
		Schema: recipe.AuthoredManifestSchema, BaseCommit: physical.BaseCommit, BaseTree: physical.BaseTree,
		Overlay:                 SetIdentity{Records: recipe.OverlayRecords, FramedBytes: recipe.OverlayFramedBytes, SHA256: recipe.OverlaySHA256},
		GeneratedMappingRecords: recipe.GeneratedMappingRecords, GeneratedMappingPath: recipe.GeneratedMappingPath,
		GeneratedMappingMode: recipe.GeneratedMappingMode, GeneratedMappingBytes: recipe.GeneratedMappingBytes,
		GeneratedMappingSHA256: recipe.GeneratedMappingSHA256, TypedInputRecords: recipe.TypedInputRecords,
		TypedInputKind: recipe.TypedInputKind, TypedInputPath: recipe.TypedInputPath, TypedInputMode: recipe.TypedInputMode,
		TypedInputBytes: recipe.TypedInputBytes, TypedInputSHA256: recipe.TypedInputSHA256, TypedInputBlobOID: recipe.TypedInputBlobOID,
		AddedRegularFiles: recipe.OverlayRecords + source.profile.GeneratedMapping.RegularFiles + source.profile.TypedIndex.RegularFiles,
		RegularFiles:      source.profile.Physical.CombinedRegularFiles, TreeInventory: inventory,
		TreeObjectID: physical.ExpectedTree, CommitBytesSHA256: commitSHA256,
	}
}

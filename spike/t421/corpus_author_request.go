package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	ExecutionCorpusAuthorRequestSchema    = "t422-author-request-v1"
	MaxExecutionCorpusAuthorRequestBytes  = 16 << 10
	MaxExecutionCorpusAuthorResponseBytes = 4 << 10
)

// ExecutionCorpusSourceIdentity is PRIVATE request/custody information, never
// returned ceremony evidence. Mutable directory timestamps are deliberately
// excluded: the parent's source mutation lease supplies single-owner custody.
type ExecutionCorpusSourceIdentity struct {
	Device     int32    `json:"device"`
	Inode      uint64   `json:"inode"`
	Generation uint32   `json:"generation"`
	Volume     [2]int32 `json:"volume"`
}

// ExecutionCorpusSourceObservation observes a selected private directory. It
// does not prove ownership of prior child results, bind its objects, or issue
// admission. The owning parent retains the actual root and mutation lease.
type ExecutionCorpusSourceObservation struct {
	Identity     ExecutionCorpusSourceIdentity
	ConfigSHA256 string
}

type ExecutionCorpusAuthorRequest struct {
	Schema         string                         `json:"schema"`
	PlanPath       string                         `json:"plan_path"`
	PlanSHA256     string                         `json:"plan_sha256"`
	SourcePath     string                         `json:"source_path"`
	SourceIdentity ExecutionCorpusSourceIdentity  `json:"source_identity"`
	Revision       string                         `json:"revision"`
	Previous       *ExecutionCorpusAuthorResponse `json:"previous"`
}

// ExecutionCorpusAuthorResponse contains source-free measured output. A public
// response is not authority to resume: only the parent's authenticated next
// request binds the response it actually received from its owned child.
type ExecutionCorpusAuthorResponse struct {
	Result       AuthoredExecutionRevision `json:"result"`
	ConfigSHA256 string                    `json:"config_sha256"`
}

func ObserveExecutionCorpusSource(ctx context.Context, path string) (_ ExecutionCorpusSourceObservation, retErr error) {
	author, err := openCorpusAuthorRoot(ctx, path)
	if err != nil {
		return ExecutionCorpusSourceObservation{}, err
	}
	defer func() {
		if author.Close() != nil {
			retErr = ErrExecutionCorpusAuthor
		}
	}()
	identity, err := corpusAuthorNativeIdentity(author.rootInfo, author.volume)
	if err != nil {
		return ExecutionCorpusSourceObservation{}, err
	}
	observation := ExecutionCorpusSourceObservation{Identity: identity}
	if _, err := os.Lstat(filepath.Join(path, "config")); !errors.Is(err, os.ErrNotExist) {
		raw, err := author.readControl("config", 4096)
		if err != nil {
			return ExecutionCorpusSourceObservation{}, err
		}
		observation.ConfigSHA256 = SHA256(raw)
	}
	if author.checkRoot(ctx) != nil {
		return ExecutionCorpusSourceObservation{}, ErrExecutionCorpusAuthor
	}
	return observation, nil
}

func openCorpusAuthorRoot(ctx context.Context, path string) (*ExecutionCorpusAuthor, error) {
	if ctx == nil || ctx.Err() != nil || !executionGitPrivateDirectory(path) {
		return nil, ErrExecutionCorpusAuthor
	}
	root, err := t4013.OpenHostImage(path)
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	author := &ExecutionCorpusAuthor{lifetime: ctx, directory: path, root: root}
	author.rootInfo, err = root.Stat()
	if err == nil {
		author.volume, err = inputCustodyVolume(root)
	}
	if err != nil || author.checkRoot(ctx) != nil {
		_ = author.Close()
		return nil, ErrExecutionCorpusAuthor
	}
	return author, nil
}

func decodeCorpusAuthorRequest(raw []byte, expected [32]byte) (ExecutionCorpusAuthorRequest, error) {
	if len(raw) == 0 || len(raw) > MaxExecutionCorpusAuthorRequestBytes || expected == ([32]byte{}) || sha256.Sum256(raw) != expected {
		return ExecutionCorpusAuthorRequest{}, ErrExecutionCorpusAuthor
	}
	var request ExecutionCorpusAuthorRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !corpusAuthorJSONEOF(decoder) ||
		request.Schema != ExecutionCorpusAuthorRequestSchema || !executionGitAbsolutePath(request.PlanPath) ||
		!executionGitAbsolutePath(request.SourcePath) || request.PlanPath == request.SourcePath ||
		!validExecutionSHA256(request.PlanSHA256) || request.SourceIdentity.Inode == 0 ||
		request.Revision != "a" && request.Revision != "b" && request.Revision != "a-return" ||
		(request.Revision == "a") != (request.Previous == nil) {
		return ExecutionCorpusAuthorRequest{}, ErrExecutionCorpusAuthor
	}
	want, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
	if err != nil || !bytes.Equal(raw, want) {
		return ExecutionCorpusAuthorRequest{}, ErrExecutionCorpusAuthor
	}
	return request, nil
}

func corpusAuthorJSONEOF(decoder *json.Decoder) bool {
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func corpusAuthorCanonical(value any, maximum int) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) >= maximum {
		return nil, ErrExecutionCorpusAuthor
	}
	return append(raw, '\n'), nil
}

// OpenExecutionCorpusAuthorRequest authenticates the bounded canonical request
// BEFORE opening a named path. It adopts only a parent-created exact root and
// a protected canonical V3 plan, then independently verifies prior continuity.
// It creates no directory and authorizes exactly the requested revision once.
func OpenExecutionCorpusAuthorRequest(ctx context.Context, raw []byte) (*ExecutionCorpusAuthor, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	expected, err := dispatchadmission.AuthorInputSHA256()
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	request, err := decodeCorpusAuthorRequest(raw, expected)
	if err != nil {
		return nil, err
	}
	planFile, planInfo, planRaw, err := readCorpusAuthorPlan(ctx, request.PlanPath, request.PlanSHA256)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			_ = planFile.Close()
		}
	}()
	plan, err := DecodePlan(planRaw)
	if err != nil || plan.Schema != PlanV3Schema || ctx.Err() != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	source, err := newCorpusAuthorSource(ctx, plan)
	if err != nil {
		return nil, err
	}
	author, err := resumeCorpusAuthor(ctx, source, request, dispatchadmission.ProductionTool("git"), dispatchadmission.StartAuthor)
	if err != nil {
		return nil, err
	}
	author.lifetime = dispatchadmission.ProcessContext()
	author.planFile, author.planInfo, author.planPath = planFile, planInfo, request.PlanPath
	retained = true
	if author.checkPlan(ctx) != nil {
		_ = author.Close()
		return nil, ErrExecutionCorpusAuthor
	}
	return author, nil
}

func resumeCorpusAuthor(ctx context.Context, source *corpusAuthorSource, request ExecutionCorpusAuthorRequest,
	gitPath string, start func(context.Context, *exec.Cmd) (dispatchadmission.Handle, error),
) (*ExecutionCorpusAuthor, error) {
	author, err := openCorpusAuthorRoot(ctx, request.SourcePath)
	if err != nil {
		return nil, err
	}
	fail := func() (*ExecutionCorpusAuthor, error) { _ = author.Close(); return nil, ErrExecutionCorpusAuthor }
	identity, err := corpusAuthorNativeIdentity(author.rootInfo, author.volume)
	if err != nil || identity != request.SourceIdentity || source == nil || start == nil || !executionGitAbsolutePath(gitPath) {
		return fail()
	}
	author.source, author.gitPath, author.start, author.requestRevision = source, gitPath, start, request.Revision
	for author.next < len(source.revisions) && source.revisions[author.next].Name != request.Revision {
		author.next++
	}
	if author.next == len(source.revisions) {
		return fail()
	}
	if author.next == 0 {
		if request.Previous != nil {
			return fail()
		}
		entries, err := author.root.ReadDir(1)
		if !errors.Is(err, io.EOF) || len(entries) != 0 {
			return fail()
		}
	} else {
		if request.Previous == nil || !validExecutionSHA256(request.Previous.ConfigSHA256) {
			return fail()
		}
		previous := request.Previous.Result
		physical := source.revisions[author.next-1]
		parent := ""
		if author.next > 1 {
			parent = source.revisions[author.next-2].ExpectedCommit
		}
		raw, err := canonicalGitCommitBytesFor(physical.ExpectedTree, parent, physical.CommitMessage, source.recipe)
		if err != nil || previous.Name != physical.Name || previous.Commit != physical.ExpectedCommit ||
			previous.Tree != physical.ExpectedTree || previous.ParentCommit != parent ||
			previous.Manifest != source.manifest(physical, physical.ExpectedTreeInventory, SHA256(raw)) {
			return fail()
		}
		author.previous, author.config = previous, request.Previous.ConfigSHA256
		if author.checkContinuity(ctx, previous.Commit) != nil {
			return fail()
		}
	}
	return author, nil
}

func readCorpusAuthorPlan(ctx context.Context, path, digest string) (*os.File, os.FileInfo, []byte, error) {
	if ctx == nil || ctx.Err() != nil || !executionGitAbsolutePath(path) || !validExecutionSHA256(digest) {
		return nil, nil, nil, ErrExecutionCorpusAuthor
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return nil, nil, nil, ErrExecutionCorpusAuthor
	}
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return nil, nil, nil, ErrExecutionCorpusAuthor
	}
	fail := func() (*os.File, os.FileInfo, []byte, error) {
		_ = file.Close()
		return nil, nil, nil, ErrExecutionCorpusAuthor
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !inputCustodyProtected(info) || info.Size() <= 0 || info.Size() > MaxPlanBytes {
		return fail()
	}
	raw, err := io.ReadAll(executionInputReader{ctx, io.LimitReader(file, MaxPlanBytes+1)})
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || statErr != nil || pathErr != nil || len(raw) > MaxPlanBytes || SHA256(raw) != digest ||
		!inputCustodySame(info, after) || !inputCustodySame(info, current) || ctx.Err() != nil {
		return fail()
	}
	return file, info, raw, nil
}

func (author *ExecutionCorpusAuthor) checkPlan(ctx context.Context) error {
	if author.planFile == nil {
		return nil // The original in-process constructor owns already-decoded plan bytes.
	}
	if ctx == nil || ctx.Err() != nil {
		return ErrExecutionCorpusAuthor
	}
	canonical, err := filepath.EvalSymlinks(author.planPath)
	if err != nil || canonical != author.planPath {
		return ErrExecutionCorpusAuthor
	}
	info, err := author.planFile.Stat()
	current, pathErr := os.Lstat(author.planPath)
	if err != nil || pathErr != nil || !inputCustodyProtected(info) || !inputCustodySame(author.planInfo, info) || !inputCustodySame(info, current) {
		return ErrExecutionCorpusAuthor
	}
	return nil
}

// AuthorRequested writes only the authenticated one-shot revision. A response
// remains bounded source-free output; it grants no cross-process authority.
func (author *ExecutionCorpusAuthor) AuthorRequested(ctx context.Context) ([]byte, error) {
	if author == nil || author.requestRevision == "" {
		return nil, ErrExecutionCorpusAuthor
	}
	result, err := author.AuthorNext(ctx, author.requestRevision)
	if err != nil {
		return nil, err
	}
	return corpusAuthorCanonical(ExecutionCorpusAuthorResponse{Result: result, ConfigSHA256: author.config}, MaxExecutionCorpusAuthorResponseBytes)
}

// Package indexer builds zoekt shards via a same-SHA zoekt-git-index child
// (Epic 3). The child is compiled from this module's go.mod
// (`go build github.com/sourcegraph/zoekt/cmd/zoekt-git-index`, pinned by the
// go.mod tool directive), so reader/writer shard skew is structurally
// impossible (PLAN §1.1). The child process is the OOM isolation boundary.
package indexer

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/executableidentity"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

const (
	zoektModuleVersion = "v0.0.0-20260709064101-33f1f18af292"
	zoektModuleSum     = "h1:HsoQyVl9olsjSqA0YddekTbCJOsfn9bUeu//bLg6fa8="
)

// FindBinary locates zoekt-git-index: env override, next to our executable,
// ./bin beside the executable (the `make build` layout), then PATH.
func FindBinary() (string, error) {
	candidate, err := findBinary()
	if err != nil {
		return "", err
	}
	if err := executableidentity.Verify(candidate, os.Getenv("PHEBS_ZOEKT_GIT_INDEX_SHA256")); err != nil {
		return "", fmt.Errorf("verify zoekt-git-index identity: %w", err)
	}
	return candidate, nil
}

func findBinary() (string, error) {
	var candidate string
	var err error
	if p := os.Getenv("PHEBS_ZOEKT_GIT_INDEX"); p != "" {
		candidate, err = executablePath(p)
		if err != nil {
			return "", err
		}
		return candidate, VerifyBinaryPin(candidate)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{
			filepath.Join(dir, "zoekt-git-index"),
			filepath.Join(dir, "bin", "zoekt-git-index"),
		} {
			if resolved, err := executablePath(p); err == nil {
				if pinErr := VerifyBinaryPin(resolved); pinErr == nil {
					return resolved, nil
				}
			}
		}
	}
	p, err := exec.LookPath("zoekt-git-index")
	if err != nil {
		return "", err
	}
	candidate, err = executablePath(p)
	if err != nil {
		return "", err
	}
	return candidate, VerifyBinaryPin(candidate)
}

// VerifyBinaryPin rejects a child whose embedded module identity differs from
// the zoekt reader linked into this process. This turns the same-module build
// convention into a runtime admission fence for overrides and PATH binaries.
func VerifyBinaryPin(binary string) error {
	candidate, err := buildinfo.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read zoekt-git-index build identity: %w", err)
	}
	if candidate.Path != "github.com/sourcegraph/zoekt/cmd/zoekt-git-index" ||
		candidate.Main.Path != "github.com/sourcegraph/zoekt" {
		return fmt.Errorf(
			"zoekt-git-index has unexpected package/module identity %q/%q",
			candidate.Path, candidate.Main.Path,
		)
	}
	want := "github.com/sourcegraph/zoekt@" + zoektModuleVersion + " " +
		zoektModuleSum
	linked, err := linkedZoektModuleIdentity()
	if err != nil {
		return err
	}
	if linked != want {
		return fmt.Errorf(
			"embedded zoekt pin %s differs from linked reader %s", want, linked,
		)
	}
	got := moduleIdentity(candidate.Main)
	if got != want {
		return fmt.Errorf(
			"zoekt-git-index module identity %s differs from linked reader %s",
			got, want,
		)
	}
	return nil
}

func linkedZoektModuleIdentity() (string, error) {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("read linked zoekt module identity")
	}
	if build.Main.Path == "github.com/sourcegraph/zoekt" {
		return moduleIdentity(build.Main), nil
	}
	for _, dependency := range build.Deps {
		if dependency.Path == "github.com/sourcegraph/zoekt" {
			return moduleIdentity(*dependency), nil
		}
	}
	// External-package test binaries can link only the indexer seams exercised
	// by that test and omit the zoekt reader dependency from their final build
	// metadata. Their child remains fenced by the embedded pin, whose separate
	// test binds it to go.mod and go.sum. Production binaries stay fail closed.
	if strings.HasSuffix(build.Path, ".test") {
		return "github.com/sourcegraph/zoekt@" + zoektModuleVersion + " " +
			zoektModuleSum, nil
	}
	return "", errors.New("linked zoekt module identity is absent")
}

func moduleIdentity(module debug.Module) string {
	if module.Replace != nil {
		module = *module.Replace
	}
	return module.Path + "@" + module.Version + " " + module.Sum
}

// The index child changes its working directory to the bare mirror. Resolve
// configured relative paths before that chdir, and reject paths that cannot
// be executed, so successful startup proves later index jobs can launch.
func executablePath(candidate string) (string, error) {
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve zoekt-git-index path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve zoekt-git-index binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect zoekt-git-index binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("zoekt-git-index binary is not an executable regular file")
	}
	return resolved, nil
}

type Indexer struct {
	DataDir string // mirrors under DataDir/repos, shards under DataDir/index
	Bin     string // zoekt-git-index path (FindBinary)
	// BinSHA256, when set, is rechecked immediately before the whole-repository
	// index child launches. Ordinary callers retain the environment fence.
	BinSHA256 string
	// FocusedBin is the same-module phebs-focused-index child. It is required
	// only for repositories with a configured analysis unit.
	FocusedBin string
	Store      store.Store
	// Verbose forwards the child indexer's line-oriented stdout/stderr and
	// parent phase transitions to Logger. Failure diagnostics retain only a
	// bounded tail regardless of this setting.
	Verbose bool
	// Logger receives verbose indexing output. Nil uses log.Default().
	Logger *log.Logger
	// Revisions is the validated per-repository selector -> full Git ref
	// allowlist. HEAD is always implicit and is never present in this map.
	Revisions map[string]map[string]string
	// AnalysisUnits is the validated repository-keyed semantic scope. Its
	// presence selects the focused child and manifest-bound publication.
	AnalysisUnits map[string]analysisunit.Scope
	// AdmitDerived runs after the exact no-op fence and before any staging
	// directory or child is created. T35.3 uses it for hard-watermark refusal.
	AdmitDerived func(context.Context, int64) error

	// OnIndexed, when set, runs once the indexed state is known current — the
	// index→candidate chain hook, mirroring how sync chains index. It also runs
	// on an unchanged-HEAD short circuit: if persisting the successor job failed
	// after a prior index commit, the retried index job can repair the missing
	// chain without rebuilding the shard.
	OnIndexed func(ctx context.Context, repoName, commit string) error
}

// Handle adapts Index to the store.Runner: the job target is the repo name.
func (ix *Indexer) Handle(ctx context.Context, job store.Job) error {
	return ix.Index(ctx, store.Repo{Name: job.Target}, job.Force)
}

// Index runs the child builder over the repo's bare mirror and records the
// exact revision set on success. Repositories without an allowlist retain the
// child's HEAD-only default.
func (ix *Indexer) Index(ctx context.Context, repo store.Repo, force bool) error {
	target := repo.Name
	dir, err := sync.SafeRepoDir(ix.DataDir, target)
	if err != nil {
		return fmt.Errorf("index %s: %w", target, err)
	}
	unlock, err := repowork.LockContext(ctx, dir)
	if err != nil {
		return fmt.Errorf("index %s: lock mirror: %w", target, err)
	}
	defer unlock()

	fresh, err := ix.Store.GetRepo(ctx, target)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("index %s: reload repo: %w", target, err)
	}
	if fresh.Deleting {
		return nil
	}
	if fresh.Name != target {
		return fmt.Errorf("index %s: stored repository name mismatch", target)
	}
	repo = *fresh

	indexDir := filepath.Join(ix.DataDir, "index")
	// A prior attempt may have committed the repository row while losing the
	// SetRepoIndexedState response and the immediate ambiguity reread. Archive
	// restore also deliberately omits reconstructible search lifecycle controls.
	// Resolve either case against durable store authority before a retry/no-op
	// can build over or bypass its selected complete publication.
	storedRevisions := storedWholeRevisions(repo)
	if len(storedRevisions) > 0 || focusedindex.IsPublishing(indexDir, repo.Name) {
		if _, recoverErr := focusedindex.RecoverSearchPublication(
			ctx, indexDir, repo.Name, storedRevisions,
		); recoverErr != nil {
			return fmt.Errorf(
				"index %s: recover search publication: %w",
				repo.Name, recoverErr,
			)
		}
	}

	head, err := sync.Head(ctx, dir)
	if err != nil {
		return fmt.Errorf("index %s: resolve HEAD: %w", repo.Name, err)
	}
	revisions, err := ix.resolveRevisions(ctx, dir, repo.Name, head)
	if err != nil {
		return fmt.Errorf("index %s: resolve revisions: %w", repo.Name, err)
	}
	unit, err := ix.desiredAnalysisUnit(repo.Name)
	if err != nil {
		return fmt.Errorf("index %s: analysis unit: %w", repo.Name, err)
	}
	if !force && head != "" && head == repo.IndexedCommitHash &&
		revisionsEqual(revisions, repo.IndexedRevisions, repo.IndexedCommitHash) &&
		analysisunit.EqualState(unit, repo.IndexedAnalysisUnit) &&
		!focusedindex.IsPublishing(filepath.Join(ix.DataDir, "index"), repo.Name) {
		ix.verbosef("index %s: already current at %s; skipping child", repo.Name, head)
		return ix.afterIndexed(ctx, repo.Name, head) // T3.2: shards current; repair/confirm the chain
	}
	if ix.AdmitDerived != nil {
		if err := ix.AdmitDerived(ctx, 0); err != nil {
			return fmt.Errorf("index %s: derived-artifact admission: %w", repo.Name, err)
		}
	}
	if unit == nil {
		reactivated, reactivateErr := focusedindex.ReactivatePriorSearchGeneration(
			ctx, indexDir, repo.Name, revisions,
		)
		if reactivateErr != nil {
			return fmt.Errorf("index %s: reactivate prior search generation: %w", repo.Name, reactivateErr)
		}
		if reactivated {
			ix.verbosef("index %s: reactivated retained prior search generation", repo.Name)
			return ix.commitPublishedIndex(ctx, repo, head, revisions, unit, indexDir)
		}
	}

	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("index %s: %w", repo.Name, err)
	}
	workspace, stageDir, err := focusedindex.NewBuildWorkspace(indexDir)
	if err != nil {
		return fmt.Errorf("index %s: create staging workspace: %w", repo.Name, err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	var sourceManifest repositoryindex.SourceManifest
	var sourceStageDir string
	if unit == nil {
		sourceStageDir = filepath.Join(workspace, "source")
		ix.verbosef(
			"index %s: starting repository source census revisions=%d",
			repo.Name, len(revisions),
		)
		sourceManifest, err = repositoryindex.BuildSourceGeneration(
			ctx, dir, sourceStageDir, repo.Name, revisions,
		)
		if err != nil {
			return fmt.Errorf("index %s: source generation: %w", repo.Name, err)
		}
		ix.verbosef(
			"index %s: source generation owners=%d placements=%d members=%d bytes=%d",
			repo.Name, sourceManifest.OwnerCount,
			sourceManifest.PlacementCount, len(sourceManifest.Members),
			sourceManifest.EncodedMemberBytes,
		)
		if ix.AdmitDerived != nil {
			reservation, reservationErr := focusedindex.SearchGenerationReservation(sourceManifest)
			if reservationErr != nil {
				return fmt.Errorf("index %s: search reservation: %w", repo.Name, reservationErr)
			}
			if err := ix.AdmitDerived(ctx, reservation); err != nil {
				return fmt.Errorf(
					"index %s: measured search-generation admission (%d bytes): %w",
					repo.Name, reservation, err,
				)
			}
		}
		batchReads, fallbackReads, countErr := focusedindex.SearchGenerationReaderCounts(sourceManifest)
		if countErr != nil {
			return fmt.Errorf("index %s: search reader accounting: %w", repo.Name, countErr)
		}
		ix.verbosef(
			"index %s: search reader mode=go_git files_offered=%d batch_reads=%d fallback_reads=%d",
			repo.Name, fallbackReads, batchReads, fallbackReads,
		)
	}
	// zoekt.name makes shard repo names equal store names, which the T4.1
	// RepoSet pre-pass depends on; the child reads it from the repo config
	if _, err := sync.GitConfig(ctx, dir, "zoekt.name", repo.Name); err != nil {
		return fmt.Errorf("index %s: set zoekt.name: %w", repo.Name, err)
	}
	// Always -incremental=false. phebs's own short-circuit above (indexed hash
	// == HEAD) already skips redundant runs, so by the time we invoke the
	// child a real build is wanted. Leaving zoekt's own incremental skip on
	// silently no-ops a force job when HEAD and the on-disk shard are unchanged.
	childName := "zoekt-git-index"
	var cmd *exec.Cmd
	resultPath := filepath.Join(workspace, "result.json")
	if unit != nil {
		if ix.FocusedBin == "" {
			return fmt.Errorf("index %s: focused-index child is unavailable", repo.Name)
		}
		scope := ix.AnalysisUnits[repo.Name]
		requestPath := filepath.Join(workspace, "request.json")
		if err := focusedindex.WriteControlFile(requestPath, focusedindex.Request{
			Schema:    focusedindex.RequestSchema,
			RepoDir:   dir,
			OutputDir: stageDir,
			Scope:     scope,
			Revisions: revisions,
		}); err != nil {
			return fmt.Errorf("index %s: write focused request: %w", repo.Name, err)
		}
		childName = "phebs-focused-index"
		cmd = exec.CommandContext(
			ctx, ix.FocusedBin, "-request", requestPath, "-result", resultPath,
		)
	} else {
		args := []string{
			"-index", stageDir,
			"-incremental=false",
			"-submodules=false",
			"-file_limit=2097152",
			"-shard_limit=104857600",
			"-max_trigram_count=20000",
		}
		if len(revisions) > 1 {
			branches := make([]string, 0, len(revisions))
			for _, revision := range revisions {
				branches = append(branches, revision.Branch)
			}
			args = append(args, "-branches="+strings.Join(branches, ","))
		}
		cmd = exec.CommandContext(ctx, ix.Bin, append(args, dir)...)
		cmd.Env = goGitChildEnvironment(os.Environ())
	}
	ix.verbosef(
		"index %s: starting %s revisions=%d force=%t",
		repo.Name, childName, len(revisions), force,
	)
	start := time.Now()
	out := newChildOutput(ix.logger(), fmt.Sprintf("index %s: %s: ", repo.Name, childName), ix.Verbose)
	cmd.Stdout, cmd.Stderr = out, out
	expectedSHA256 := os.Getenv("PHEBS_ZOEKT_GIT_INDEX_SHA256")
	if unit != nil {
		expectedSHA256 = os.Getenv("PHEBS_FOCUSED_INDEX_SHA256")
	} else if ix.BinSHA256 != "" {
		expectedSHA256 = ix.BinSHA256
	}
	if err := executableidentity.Verify(cmd.Path, expectedSHA256); err != nil {
		return fmt.Errorf("index %s: verify %s identity before launch: %w", repo.Name, childName, err)
	}
	runErr := cmd.Run()
	out.Flush()
	if runErr != nil {
		err := runErr
		wrapped := fmt.Errorf("index %s: %s: %w\n%s", repo.Name, childName, err, out.String())
		if ctx.Err() != nil {
			return fmt.Errorf("%v: %w", wrapped, ctx.Err())
		}
		return classifyChild(wrapped, err, out.String())
	}
	duration := time.Since(start)
	indexDuration.Observe(duration.Seconds())
	releaseMutation, err := focusedindex.AcquireMutationLock(ctx, indexDir)
	if err != nil {
		return fmt.Errorf("index %s: acquire publication lock: %w", repo.Name, err)
	}
	defer releaseMutation()
	if unit != nil {
		result, err := focusedindex.ReadResult(resultPath)
		if err != nil {
			return fmt.Errorf("index %s: validate focused result: %w", repo.Name, err)
		}
		generation, generationErr := focusedindex.GenerationDigest(ix.AnalysisUnits[repo.Name], revisions)
		if generationErr != nil {
			return fmt.Errorf("index %s: focused generation: %w", repo.Name, generationErr)
		}
		if result.Repository != repo.Name || result.UnitDigest != unit.Digest ||
			result.GenerationDigest != generation || result.OutOfUnitBlobReads != 0 {
			return fmt.Errorf("index %s: focused child result identity mismatch", repo.Name)
		}
		stagedManifest, err := focusedindex.ValidateStage(
			stageDir, repo.Name, unit, revisions,
		)
		if err != nil {
			return fmt.Errorf("index %s: validate focused stage: %w", repo.Name, err)
		}
		if result.ManifestDigest != stagedManifest.Digest ||
			result.ShardCount != len(stagedManifest.Members) {
			return fmt.Errorf("index %s: focused result disagrees with staged manifest", repo.Name)
		}
		var stagedShardBytes int64
		for _, member := range stagedManifest.Members {
			info, statErr := os.Lstat(filepath.Join(stageDir, member.Name))
			if statErr != nil {
				return fmt.Errorf(
					"index %s: inspect staged focused member %q: %w",
					repo.Name, member.Name, statErr,
				)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf(
					"index %s: staged focused member %q is not regular",
					repo.Name, member.Name,
				)
			}
			stagedShardBytes += info.Size()
		}
		if result.ShardBytes != stagedShardBytes {
			return fmt.Errorf("index %s: focused byte count disagrees with staged manifest", repo.Name)
		}
		focusedBlobReads.Observe(float64(result.OpenedBlobCount))
		focusedBlobBytes.Observe(float64(result.OpenedBlobBytes))
		ix.verbosef(
			"index %s: focused reader blobs=%d bytes=%d out_of_unit=%d shards=%d",
			repo.Name, result.OpenedBlobCount, result.OpenedBlobBytes,
			result.OutOfUnitBlobReads, result.ShardCount,
		)
		if err := focusedindex.PublishFocused(
			ctx, indexDir, stageDir, repo.Name, unit, revisions,
		); err != nil {
			return ix.failPublication(ctx, repo.Name, indexDir, err, false)
		}
	} else if err := focusedindex.PublishWholeGeneration(
		ctx, indexDir, stageDir, sourceStageDir, repo.Name, revisions,
		sourceManifest,
	); err != nil {
		return ix.failPublication(ctx, repo.Name, indexDir, err, true)
	}
	totalShardBytes := dirBytes(indexDir)
	shardBytes.Set(totalShardBytes)
	ix.verbosef(
		"index %s: %s complete duration=%s total_shard_bytes=%.0f",
		repo.Name, childName, duration.Round(time.Millisecond), totalShardBytes,
	)
	return ix.commitPublishedIndex(ctx, repo, head, revisions, unit, indexDir)
}

func (ix *Indexer) commitPublishedIndex(
	ctx context.Context,
	repo store.Repo,
	head string,
	revisions []store.IndexedRevision,
	unit *analysisunit.State,
	indexDir string,
) error {
	if err := ix.Store.SetRepoIndexedState(
		ctx, repo.Name, head, revisions, unit, time.Now().UTC(),
	); err != nil {
		// The child has already replaced the shard. If the DB commit fails,
		// remove both sides of the claimed state so search cannot serve revision
		// B while MCP defaults to the previously recorded revision A.
		stateErr := fmt.Errorf("index %s: record state: %w", repo.Name, err)
		freshState, readErr := ix.Store.GetRepo(ctx, repo.Name)
		if readErr == nil && freshState.IndexedCommitHash == head &&
			revisionsEqual(revisions, freshState.IndexedRevisions, freshState.IndexedCommitHash) &&
			analysisunit.EqualState(unit, freshState.IndexedAnalysisUnit) {
			finishErr := finishCommittedPublication(indexDir, repo.Name, unit)
			return errors.Join(stateErr, wrapIfError("finish ambiguously committed publication", finishErr))
		}
		if readErr != nil && unit == nil {
			// The commit outcome is still unknown. Preserve both publication
			// controls so the next retry/startup can select the candidate or
			// prior generation from the durable row; rolling back here would
			// destroy the only evidence needed to resolve that ambiguity.
			return errors.Join(
				stateErr,
				wrapIfError("reload ambiguous index state", readErr),
			)
		}
		if unit == nil {
			rollbackErr := focusedindex.RollbackSearchPublication(ctx, indexDir, repo.Name)
			rootPath := filepath.Join(indexDir, focusedindex.SearchGenerationRootName(repo.Name))
			_, rootErr := os.Lstat(rootPath)
			var clearErr error
			if errors.Is(rootErr, os.ErrNotExist) {
				clearErr = ix.Store.ClearRepoIndexState(ctx, repo.Name)
			}
			return errors.Join(
				stateErr, wrapIfError("reload ambiguous index state", readErr),
				wrapIfError("restore prior search generation", rollbackErr),
				wrapIfError("clear index state without rollback", clearErr),
			)
		}
		clearErr := ix.Store.ClearRepoIndexState(ctx, repo.Name)
		removeErr := focusedindex.RemoveRepository(ctx, indexDir, repo.Name)
		return errors.Join(stateErr, wrapIfError("reload ambiguous index state", readErr),
			wrapIfError("clear index state", clearErr),
			wrapIfError("remove uncommitted shards", removeErr))
	}
	if err := finishCommittedPublication(indexDir, repo.Name, unit); err != nil {
		return fmt.Errorf("index %s: expose committed publication: %w", repo.Name, err)
	}
	ix.verbosef("index %s: committed index state at %s", repo.Name, head)
	return ix.afterIndexed(ctx, repo.Name, head)
}

func finishCommittedPublication(
	indexDir, repository string, unit *analysisunit.State,
) error {
	var retireErr error
	if unit != nil && unit.SearchIndexPosture == analysisunit.SearchIndexFocused {
		retireErr = focusedindex.RetireSearchGenerationRoot(indexDir, repository)
	}
	if retireErr != nil {
		return retireErr
	}
	return focusedindex.FinishPublication(indexDir, repository)
}

func storedWholeRevisions(repo store.Repo) []store.IndexedRevision {
	if len(repo.IndexedRevisions) == 0 && repo.IndexedCommitHash != "" {
		return []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: repo.IndexedCommitHash,
		}}
	}
	return append([]store.IndexedRevision(nil), repo.IndexedRevisions...)
}

func (ix *Indexer) failPublication(
	ctx context.Context,
	repository, indexDir string,
	cause error,
	whole bool,
) error {
	if !focusedindex.IsPublishing(indexDir, repository) {
		return fmt.Errorf("index %s: publish: %w", repository, cause)
	}
	if whole {
		rollbackErr := focusedindex.RollbackSearchPublication(ctx, indexDir, repository)
		rootPath := filepath.Join(indexDir, focusedindex.SearchGenerationRootName(repository))
		_, rootErr := os.Lstat(rootPath)
		var clearErr error
		if errors.Is(rootErr, os.ErrNotExist) {
			clearErr = ix.Store.ClearRepoIndexState(ctx, repository)
		}
		return errors.Join(
			fmt.Errorf("index %s: publish: %w", repository, cause),
			wrapIfError("restore prior search generation", rollbackErr),
			wrapIfError("clear index state without rollback", clearErr),
		)
	}
	clearErr := ix.Store.ClearRepoIndexState(ctx, repository)
	removeErr := focusedindex.RemoveRepository(ctx, indexDir, repository)
	return errors.Join(
		fmt.Errorf("index %s: publish: %w", repository, cause),
		wrapIfError("clear interrupted index state", clearErr),
		wrapIfError("remove interrupted publication", removeErr),
	)
}

func goGitChildEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if key == "ZOEKT_DISABLE_CATFILE_BATCH" {
			continue
		}
		result = append(result, value)
	}
	return append(result, "ZOEKT_DISABLE_CATFILE_BATCH=true")
}

func (ix *Indexer) desiredAnalysisUnit(repository string) (*analysisunit.State, error) {
	scope, configured := ix.AnalysisUnits[repository]
	if !configured {
		return nil, nil
	}
	if scope.Repository != repository {
		return nil, errors.New("repository key does not match scope identity")
	}
	return scope.State()
}

// ReconcileAnalysisUnits queues a rebuild when configuration and the last
// atomically committed index state disagree. It never clears the old state:
// the previous complete publication remains authoritative until the index job
// commits its replacement.
func ReconcileAnalysisUnits(
	ctx context.Context,
	st store.Store,
	scopes map[string]analysisunit.Scope,
) (int, error) {
	repositories, err := st.ListRepos(ctx)
	if err != nil {
		return 0, err
	}
	enqueued := 0
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return enqueued, err
		}
		if repository.Deleting {
			continue
		}
		var desired *analysisunit.State
		if scope, configured := scopes[repository.Name]; configured {
			if scope.Repository != repository.Name {
				return enqueued, fmt.Errorf(
					"repository %q analysis-unit key mismatch",
					repository.Name,
				)
			}
			desired, err = scope.State()
			if err != nil {
				return enqueued, fmt.Errorf(
					"repository %q analysis unit: %w",
					repository.Name, err,
				)
			}
		}
		if analysisunit.EqualState(desired, repository.IndexedAnalysisUnit) {
			continue
		}
		if _, err := st.EnqueuePending(
			ctx, store.JobIndex, repository.Name,
			repository.IndexedCommitHash != "",
		); err != nil {
			return enqueued, fmt.Errorf(
				"enqueue analysis-unit rebuild for %s: %w",
				repository.Name, err,
			)
		}
		enqueued++
	}
	return enqueued, nil
}

func (ix *Indexer) logger() *log.Logger {
	if ix.Logger != nil {
		return ix.Logger
	}
	return log.Default()
}

func (ix *Indexer) verbosef(format string, args ...any) {
	if ix.Verbose {
		ix.logger().Printf(format, args...)
	}
}

func (ix *Indexer) resolveRevisions(ctx context.Context, dir, repoName, head string) ([]store.IndexedRevision, error) {
	resolved := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: head}}
	configured := ix.Revisions[repoName]
	selectors := make([]string, 0, len(configured))
	for selector := range configured {
		selectors = append(selectors, selector)
	}
	sort.Strings(selectors)
	for _, selector := range selectors {
		ref := configured[selector]
		commit, err := sync.ResolveCommit(ctx, dir, ref)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", selector, ref, err)
		}
		branch := ref
		if strings.HasPrefix(ref, "refs/tags/") {
			// zoekt's go-git reader otherwise resolves an annotated tag to the
			// tag object and rejects it as a non-commit. Keep the user-facing
			// selector stable while storing the exact peeled shard branch name.
			branch += "^{commit}"
		}
		resolved = append(resolved, store.IndexedRevision{Selector: selector, Branch: branch, Commit: commit})
	}
	return resolved, nil
}

// revisionsEqual accepts legacy HEAD-only rows with no indexed_revisions field
// so an upgrade does not rebuild every shard. Any configured or malformed
// multi-revision state requires a fresh atomic publication.
func revisionsEqual(want, got []store.IndexedRevision, legacyHead string) bool {
	if len(got) == 0 && len(want) == 1 {
		return want[0] == (store.IndexedRevision{Selector: "HEAD", Branch: "HEAD", Commit: legacyHead})
	}
	if len(want) != len(got) {
		return false
	}
	bySelector := make(map[string]store.IndexedRevision, len(got))
	for _, revision := range got {
		if revision.Selector == "" || revision.Branch == "" || revision.Commit == "" || bySelector[revision.Selector].Selector != "" {
			return false
		}
		bySelector[revision.Selector] = revision
	}
	for _, revision := range want {
		if bySelector[revision.Selector] != revision {
			return false
		}
	}
	return true
}

func (ix *Indexer) afterIndexed(ctx context.Context, repoName, commit string) error {
	if ix.OnIndexed == nil {
		return nil
	}
	if err := ix.OnIndexed(ctx, repoName, commit); err != nil {
		return fmt.Errorf("index %s: chain post-index work: %w", repoName, err)
	}
	return nil
}

func wrapIfError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

// classifyChild tags child-builder failures per the T3.3 taxonomy: SIGKILL
// means the OOM reaper got it (the whole point of the process boundary);
// integrity complaints mean a corrupt shard.
func classifyChild(wrapped, raw error, output string) error {
	var ee *exec.ExitError
	if errors.As(raw, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL {
			return store.WithClass(store.ClassOOM, wrapped)
		}
	}
	lower := strings.ToLower(output)
	if strings.Contains(lower, "corrupt") || strings.Contains(lower, "checksum mismatch") {
		return store.WithClass(store.ClassCorrupt, wrapped)
	}
	return wrapped
}

func dirBytes(dir string) float64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !fi.IsDir() {
			total += fi.Size()
		}
	}
	return float64(total)
}

var (
	indexDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "phebs_index_duration_seconds",
		Help:    "Wall time of successful zoekt-git-index child runs.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 12), // 100ms .. ~3.4min
	})
	shardBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "phebs_index_shard_bytes",
		Help: "Total bytes of shard files under $DATA/index.",
	})
	focusedBlobReads = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "phebs_focused_index_opened_blobs",
		Help:    "Git blobs opened at the trusted focused-index reader boundary.",
		Buckets: prometheus.ExponentialBuckets(1, 2, 20),
	})
	focusedBlobBytes = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "phebs_focused_index_opened_blob_bytes",
		Help:    "Git blob bytes opened at the trusted focused-index reader boundary.",
		Buckets: prometheus.ExponentialBuckets(1024, 2, 24),
	})
)

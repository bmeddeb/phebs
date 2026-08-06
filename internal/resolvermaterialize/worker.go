package resolvermaterialize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

type Store interface {
	GetRepo(context.Context, string) (*store.Repo, error)
	GetResolverCatalogPublication(
		context.Context, string,
	) (*store.ResolverCatalogPublication, error)
	PublishResolverCatalog(context.Context, store.ResolverCatalogPublication) error
	ClearResolverCatalogPublication(context.Context, string) error
	ResolverCatalogPublicationCurrent(
		context.Context, store.ResolverCatalogPublication,
	) (bool, error)
	LatestExtractionDomainOutcome(
		context.Context, store.ExtractionScope,
	) (*store.ExtractionDomainOutcome, error)
	ListAssertions(context.Context, store.AssertionQuery) ([]store.Assertion, error)
	EnsureJobSuccessor(context.Context, store.Job, bool) (*store.Job, error)
}

type ManifestProvider interface {
	extract.CandidateManifestProvider
	extract.CandidateManifestGenerationProvider
}

type Worker struct {
	dataDir   string
	root      string
	store     Store
	manifests ManifestProvider
	registry  *Registry
	readBlob  BlobReader
	// OnPublished runs only after an exact current resolver publication is
	// strict-open. A failure keeps the durable resolver job retryable while
	// leaving the already-complete resolver generation intact.
	OnPublished func(context.Context, string) error

	cacheMu sync.Mutex
	cache   map[string]*resolvercatalog.Publication
}

func NewWorker(
	dataDir string,
	state Store,
	manifests ManifestProvider,
	registry *Registry,
) (*Worker, error) {
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return nil, errors.New("resolver worker data directory must be absolute")
	}
	if state == nil || manifests == nil {
		return nil, errors.New("resolver worker state and candidate provider are required")
	}
	if err := registry.validate(); err != nil {
		return nil, err
	}
	return &Worker{
		dataDir: dataDir,
		root:    filepath.Join(dataDir, "resolver-catalogs"),
		store:   state, manifests: manifests, registry: registry,
		readBlob: gitobj.ReadBlob,
		cache:    make(map[string]*resolvercatalog.Publication),
	}, nil
}

func (worker *Worker) Handle(ctx context.Context, job store.Job) error {
	err := worker.handle(ctx, job)
	if err == nil {
		return nil
	}
	wrapped := store.WithClass(
		store.ClassExtract,
		fmt.Errorf("resolver catalog %s: %w", job.Target, err),
	)
	if errors.Is(err, resolvercatalog.ErrLimit) ||
		errors.Is(err, resolvercatalog.ErrNondeterministicPublication) {
		return store.WithTerminal(wrapped)
	}
	return wrapped
}

func (worker *Worker) handle(ctx context.Context, job store.Job) error {
	if worker == nil || worker.store == nil || worker.manifests == nil ||
		worker.registry == nil || worker.readBlob == nil || worker.cache == nil {
		return errors.New("resolver worker is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	repoDir, err := reposync.SafeRepoDir(worker.dataDir, job.Target)
	if err != nil {
		return fmt.Errorf("mirror path: %w", err)
	}
	unlock, err := repowork.LockContext(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("lock mirror: %w", err)
	}
	defer unlock()

	workCtx, cancel := context.WithTimeout(ctx, MaterializationTimeout)
	defer cancel()
	handled, err := worker.reconcileMarked(workCtx, job)
	if err != nil {
		return fmt.Errorf("reconcile marked publication: %w", err)
	}
	if handled {
		return nil
	}
	repository, err := worker.store.GetRepo(workCtx, job.Target)
	if errors.Is(err, store.ErrNotFound) {
		worker.forget(job.Target)
		return nil
	}
	if err != nil {
		return fmt.Errorf("load repository: %w", err)
	}
	if repository == nil || repository.Name != job.Target {
		return errors.New("repository store returned a mismatched row")
	}
	if repository.Deleting || repository.IndexedCommitHash == "" {
		worker.forget(repository.Name)
		return nil
	}
	if repository.IndexedAnalysisUnit != nil {
		if err := repository.IndexedAnalysisUnit.Validate(repository.Name); err != nil {
			return fmt.Errorf("committed analysis unit: %w", err)
		}
	}
	if err := gitobj.RejectAlternates(repoDir); err != nil {
		return fmt.Errorf("resolver mirror object boundary: %w", err)
	}
	request := extract.CandidateManifestRequest{
		Repository:   repository.Name,
		Commit:       repository.IndexedCommitHash,
		AnalysisUnit: analysisunit.CloneState(repository.IndexedAnalysisUnit),
		Domains:      worker.registry.CandidateDomains(),
	}
	pointer, err := worker.manifests.CandidateManifestGeneration(workCtx, request)
	if err != nil {
		return fmt.Errorf("candidate generation: %w", err)
	}
	if pointer.ManifestDigest == "" || pointer.ControlRevision == 0 {
		return errors.New("candidate generation has no durable control identity")
	}
	declarations, err := worker.currentDeclarations(
		workCtx, repository, pointer,
	)
	if err != nil {
		return err
	}
	if len(declarations) == 0 {
		// Candidate fan-out may arrive before declaration extraction. It is a
		// successful no-op; the declaration outcome transaction creates the
		// crash-safe successor that can first publish a useful catalog.
		return nil
	}
	identityDeclarations := make(
		[]resolvercatalog.DeclarationPublication, len(declarations),
	)
	for index, declaration := range declarations {
		identityDeclarations[index] = resolvercatalog.DeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
		}
	}
	identity, err := resolvercatalog.NewIdentity(
		repository.Name, repository.IndexedCommitHash,
		unitDigest(repository.IndexedAnalysisUnit), pointer.ManifestDigest,
		identityDeclarations, worker.registry.Packs(),
	)
	if err != nil {
		return fmt.Errorf("resolver generation identity: %w", err)
	}

	current, currentErr := worker.store.GetResolverCatalogPublication(
		workCtx, repository.Name,
	)
	if currentErr != nil &&
		!errors.Is(currentErr, store.ErrNotFound) &&
		!errors.Is(currentErr, store.ErrInvalidResolverCatalogPublication) {
		return fmt.Errorf("load resolver pointer: %w", currentErr)
	}
	if errors.Is(currentErr, store.ErrInvalidResolverCatalogPublication) {
		if err := worker.queueRecoveryAndClear(workCtx, job); err != nil {
			return fmt.Errorf("retire invalid resolver pointer: %w", err)
		}
		return nil
	}
	if current != nil && !job.Force && pointerMatchesIdentity(*current, identity) {
		authorityCurrent, err := worker.store.ResolverCatalogPublicationCurrent(
			workCtx, *current,
		)
		if err != nil {
			return err
		}
		if authorityCurrent {
			state := StateFromStore(*current)
			if cached := worker.cached(repository.Name); cached != nil &&
				reflect.DeepEqual(cached.State(), state) && cached.Current() {
				return worker.afterPublish(workCtx, repository.Name)
			}
			publication, openErr := resolvercatalog.Open(
				workCtx, worker.root, state,
			)
			if openErr == nil {
				worker.remember(repository.Name, publication)
				return worker.afterPublish(workCtx, repository.Name)
			}
			if err := workCtx.Err(); err != nil {
				return err
			}
			if errors.Is(openErr, resolvercatalog.ErrCatalogIO) {
				return fmt.Errorf(
					"open current resolver catalog: %w", openErr,
				)
			}
			// Invalid derived bytes are rebuilt below; the store transaction keeps
			// the prior pointer invisible under the publication marker.
		}
	}

	if err := ensureCatalogRoot(worker.root); err != nil {
		return err
	}
	manifest, err := worker.manifests.OpenCandidateManifest(workCtx, request)
	if err != nil {
		return fmt.Errorf("open candidate manifest: %w", err)
	}
	prepared, err := Build(workCtx, BuildRequest{
		Root: worker.root, RepositoryDir: repoDir,
		Identity: identity, Registry: worker.registry, Manifest: manifest,
		Declarations: declarations, Assertions: worker.store,
		ReadBlob: worker.readBlob,
	})
	if err != nil {
		return err
	}
	defer func() { _ = prepared.Discard() }()
	// Install creates the publication marker before the guarded store commit.
	// The current claim may already be on its final attempt, so persist an
	// independent successor before crossing that crash boundary. An existing
	// forced successor keeps its force; otherwise the successful-publication
	// path pays one bounded exact-current no-op turn.
	if err := worker.ensureJobSuccessor(workCtx, job, false); err != nil {
		return fmt.Errorf("enqueue resolver publication recovery: %w", err)
	}
	state, err := prepared.Publish(
		workCtx,
		func(commitCtx context.Context, state resolvercatalog.State) error {
			return worker.store.PublishResolverCatalog(
				commitCtx, storeFromState(state),
			)
		},
	)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			current, currentErr := worker.currentCatalogAuthority(
				workCtx, repository.Name,
			)
			if currentErr != nil {
				return markSuccessorRetry(currentErr)
			}
			if current {
				return markSuccessorRetry(fmt.Errorf(
					"%w: repository %q rejected a second manifest for current authority: %w",
					resolvercatalog.ErrNondeterministicPublication,
					repository.Name, err,
				))
			}
		}
		return markSuccessorRetry(err)
	}
	publication, err := resolvercatalog.Open(workCtx, worker.root, state)
	if err != nil {
		return markSuccessorRetry(fmt.Errorf(
			"open published resolver catalog: %w", err,
		))
	}
	worker.remember(repository.Name, publication)
	return worker.afterPublish(workCtx, repository.Name)
}

func (worker *Worker) afterPublish(ctx context.Context, repository string) error {
	if worker.OnPublished == nil {
		return nil
	}
	return worker.OnPublished(ctx, repository)
}

func (worker *Worker) currentDeclarations(
	ctx context.Context,
	repository *store.Repo,
	candidate extract.CandidateManifestPointerIdentity,
) ([]DeclarationInput, error) {
	result := make([]DeclarationInput, 0, len(worker.registry.adapters))
	for _, current := range worker.registry.adapters {
		scope := store.ExtractionScope{
			Repository: repository.Name,
			Commit:     repository.IndexedCommitHash,
			UnitDigest: unitDigest(repository.IndexedAnalysisUnit),
			Domain:     current.declarationDomain,
		}
		outcome, err := worker.store.LatestExtractionDomainOutcome(ctx, scope)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf(
				"load %s declaration outcome: %w", current.protocol, err,
			)
		}
		if outcome == nil || outcome.Disposition != store.DomainOutcomePublished ||
			outcome.RunID == "" ||
			outcome.Generation.CandidateManifestDigest != candidate.ManifestDigest ||
			outcome.Generation.CandidatePolicyDigest != candidate.PolicyDigest ||
			outcome.Generation.CandidateControlRevision != candidate.ControlRevision {
			continue
		}
		result = append(result, DeclarationInput{
			Protocol: current.protocol,
			Domain:   current.declarationDomain,
			RunID:    outcome.RunID,
			GenerationDigest: store.ComputeExtractionGenerationDigest(
				outcome.Generation,
			),
		})
	}
	slices.SortFunc(result, func(left, right DeclarationInput) int {
		if left.Domain < right.Domain {
			return -1
		}
		if left.Domain > right.Domain {
			return 1
		}
		return 0
	})
	return result, nil
}

func (worker *Worker) reconcileMarked(
	ctx context.Context,
	job store.Job,
) (bool, error) {
	repository := job.Target
	publishing, err := resolvercatalog.Publishing(worker.root, repository)
	if err != nil {
		return false, fmt.Errorf("inspect resolver publication marker: %w", err)
	}
	if !publishing {
		return false, nil
	}
	publication, openErr := resolvercatalog.OpenMarked(
		ctx, worker.root, repository, worker.registry.Packs(),
	)
	if openErr == nil {
		if err := worker.ensureJobSuccessor(ctx, job, false); err != nil {
			return false, fmt.Errorf(
				"enqueue marked resolver publication recovery: %w", err,
			)
		}
		state := publication.State()
		commitErr := worker.store.PublishResolverCatalog(
			ctx, storeFromState(state),
		)
		if commitErr == nil {
			if err := resolvercatalog.ClearPublishing(worker.root, repository); err != nil {
				return false, markSuccessorRetry(err)
			}
			if err := resolvercatalog.CleanupRetiredContext(
				ctx, worker.root, publication.Manifest(),
			); err != nil {
				return false, markSuccessorRetry(err)
			}
			worker.remember(repository, publication)
			return true, nil
		}
		if !errors.Is(commitErr, store.ErrConflict) {
			return false, markSuccessorRetry(commitErr)
		}
		current, currentErr := worker.currentCatalogAuthority(ctx, repository)
		if currentErr != nil {
			return false, markSuccessorRetry(currentErr)
		}
		if current {
			return false, markSuccessorRetry(fmt.Errorf(
				"%w: repository %q marked manifest conflicts with current authority: %w",
				resolvercatalog.ErrNondeterministicPublication,
				repository, commitErr,
			))
		}
	} else {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if errors.Is(openErr, resolvercatalog.ErrCatalogIO) {
			return false, fmt.Errorf("open marked resolver catalog: %w", openErr)
		}
	}
	// The claimed job may be on its final attempt. Persist an independent forced
	// successor before clearing any derived authority, then let that successor
	// perform the rebuild. This bounds recovery to one extra queue turn without
	// a process-death or reap gap.
	if err := worker.queueRecoveryAndClear(ctx, job); err != nil {
		return false, err
	}
	return true, nil
}

func (worker *Worker) queueRecoveryAndClear(
	ctx context.Context,
	job store.Job,
) error {
	repository := job.Target
	if err := worker.ensureJobSuccessor(ctx, job, true); err != nil {
		return fmt.Errorf("enqueue resolver-catalog recovery: %w", err)
	}
	if err := worker.store.ClearResolverCatalogPublication(ctx, repository); err != nil {
		return markSuccessorRetry(err)
	}
	if err := resolvercatalog.RemoveRepository(ctx, worker.root, repository); err != nil {
		return markSuccessorRetry(err)
	}
	worker.forget(repository)
	return nil
}

func markSuccessorRetry(err error) error {
	if err == nil || store.IsSuccessorRetry(err) {
		return err
	}
	return store.WithSuccessorRetry(err)
}

func (worker *Worker) ensureJobSuccessor(
	ctx context.Context,
	job store.Job,
	force bool,
) error {
	_, err := worker.store.EnsureJobSuccessor(ctx, job, force)
	// A transport error can follow a committed queue transaction. The marker is
	// safe when it did not commit too: the runner preserves external work and
	// touches a successor only when its exact lease provenance exists.
	return markSuccessorRetry(err)
}

func (worker *Worker) currentCatalogAuthority(
	ctx context.Context,
	repository string,
) (bool, error) {
	current, err := worker.store.GetResolverCatalogPublication(ctx, repository)
	if errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, store.ErrInvalidResolverCatalogPublication) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load conflicting resolver pointer: %w", err)
	}
	if current == nil {
		return false, nil
	}
	currentAuthority, err := worker.store.ResolverCatalogPublicationCurrent(
		ctx, *current,
	)
	if err != nil {
		return false, fmt.Errorf("check conflicting resolver authority: %w", err)
	}
	return currentAuthority, nil
}

func ensureCatalogRoot(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create resolver catalog root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("resolver catalog root is not a real directory")
	}
	return nil
}

func unitDigest(unit *analysisunit.State) string {
	if unit == nil {
		return ""
	}
	return unit.Digest
}

func pointerMatchesIdentity(
	pointer store.ResolverCatalogPublication,
	identity resolvercatalog.Identity,
) bool {
	if pointer.Repository != identity.Repository ||
		pointer.HeadCommit != identity.Commit ||
		pointer.UnitDigest != identity.UnitDigest ||
		pointer.DeclarationSetDigest != identity.DeclarationSetDigest ||
		pointer.CandidateManifestDigest != identity.CandidateManifestDigest ||
		pointer.SourceLanePolicy != identity.SourceLanePolicy ||
		pointer.ResolverPackSetDigest != identity.ResolverPackSetDigest ||
		pointer.CatalogPolicyDigest != identity.CatalogPolicyDigest ||
		pointer.GenerationDigest != identity.GenerationDigest ||
		len(pointer.Declarations) != len(identity.Declarations) ||
		len(pointer.ResolverPacks) != len(identity.ResolverPacks) {
		return false
	}
	for index, declaration := range identity.Declarations {
		stored := pointer.Declarations[index]
		if stored.Domain != declaration.Domain || stored.RunID != declaration.RunID ||
			stored.GenerationDigest != declaration.GenerationDigest {
			return false
		}
	}
	for index, pack := range identity.ResolverPacks {
		stored := pointer.ResolverPacks[index]
		if stored.Name != pack.Name || stored.Version != pack.Version {
			return false
		}
	}
	return true
}

// StateFromStore reconstructs the exact immutable resolver-catalog authority
// named by its durable current row. Callers must separately perform the
// ResolverCatalogPublicationCurrent fence after any derived work.
func StateFromStore(pointer store.ResolverCatalogPublication) resolvercatalog.State {
	declarations := make(
		[]resolvercatalog.DeclarationPublication, len(pointer.Declarations),
	)
	for index, declaration := range pointer.Declarations {
		declarations[index] = resolvercatalog.DeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
		}
	}
	packs := make([]resolvercatalog.ResolverPack, len(pointer.ResolverPacks))
	for index, pack := range pointer.ResolverPacks {
		packs[index] = resolvercatalog.ResolverPack{
			Name: pack.Name, Version: pack.Version,
		}
	}
	return resolvercatalog.State{
		Schema:     resolvercatalog.StateSchema,
		Repository: pointer.Repository, Commit: pointer.HeadCommit,
		UnitDigest: pointer.UnitDigest, Declarations: declarations,
		DeclarationSetDigest:    pointer.DeclarationSetDigest,
		CandidateManifestDigest: pointer.CandidateManifestDigest,
		SourceLanePolicy:        pointer.SourceLanePolicy,
		ResolverPacks:           packs,
		ResolverPackSetDigest:   pointer.ResolverPackSetDigest,
		CatalogPolicyDigest:     pointer.CatalogPolicyDigest,
		GenerationDigest:        pointer.GenerationDigest,
		ManifestDigest:          pointer.ManifestDigest, Manifest: pointer.ManifestPath,
	}
}

func storeFromState(state resolvercatalog.State) store.ResolverCatalogPublication {
	declarations := make(
		[]store.ResolverCatalogDeclarationPublication, len(state.Declarations),
	)
	for index, declaration := range state.Declarations {
		declarations[index] = store.ResolverCatalogDeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
		}
	}
	packs := make([]store.ResolverCatalogPack, len(state.ResolverPacks))
	for index, pack := range state.ResolverPacks {
		packs[index] = store.ResolverCatalogPack{
			Name: pack.Name, Version: pack.Version,
		}
	}
	return store.ResolverCatalogPublication{
		Repository: state.Repository, HeadCommit: state.Commit,
		UnitDigest: state.UnitDigest, Declarations: declarations,
		DeclarationSetDigest:    state.DeclarationSetDigest,
		CandidateManifestDigest: state.CandidateManifestDigest,
		SourceLanePolicy:        state.SourceLanePolicy, ResolverPacks: packs,
		ResolverPackSetDigest: state.ResolverPackSetDigest,
		CatalogPolicyDigest:   state.CatalogPolicyDigest,
		GenerationDigest:      state.GenerationDigest,
		ManifestDigest:        state.ManifestDigest, ManifestPath: state.Manifest,
	}
}

func (worker *Worker) cached(repository string) *resolvercatalog.Publication {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	return worker.cache[repository]
}

func (worker *Worker) remember(
	repository string,
	publication *resolvercatalog.Publication,
) {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	worker.cache[repository] = publication
}

func (worker *Worker) forget(repository string) {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	delete(worker.cache, repository)
}

// EnqueueBackfill closes the first-publication upgrade gap. Queue uniqueness
// makes this idempotent; candidate/declaration transactions own every later
// crash-safe freshness successor.
func EnqueueBackfill(ctx context.Context, state store.Store) error {
	repositories, err := state.ListRepos(ctx)
	if err != nil {
		return fmt.Errorf("backfill resolver catalogs: list repositories: %w", err)
	}
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if repository.Deleting || repository.IndexedCommitHash == "" {
			continue
		}
		if err := store.EnqueuePending(
			ctx, state, store.JobResolverCatalog, repository.Name, false,
		); err != nil {
			return fmt.Errorf(
				"backfill resolver catalog for %s: %w", repository.Name, err,
			)
		}
	}
	return nil
}

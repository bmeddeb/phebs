package callerexecute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

// Store is the exact state boundary used by the direct caller-leaf worker.
type Store interface {
	store.CallerLeafStore
	GetRepo(context.Context, string) (*store.Repo, error)
	GetCandidateManifestPublication(
		context.Context, string,
	) (*store.CandidateManifestPublication, error)
	GetResolverCatalogPublication(
		context.Context, string,
	) (*store.ResolverCatalogPublication, error)
	ResolverCatalogPublicationCurrent(
		context.Context, store.ResolverCatalogPublication,
	) (bool, error)
	EnsureJobSuccessor(context.Context, store.Job, bool) (*store.Job, error)
}

type authority struct {
	repository *store.Repo
	candidate  *store.CandidateManifestPublication
	resolver   *store.ResolverCatalogPublication
	semantic   callerleaf.GenerationIdentity
	stored     store.CallerGenerationIdentity
	request    extract.CandidateManifestRequest
}

type resolverOpen func(
	context.Context,
	string,
	resolvercatalog.State,
	[]resolvermaterialize.Protocol,
) (*resolvercatalog.Publication, map[resolvermaterialize.Protocol]*resolvermaterialize.CallerResolverView, error)

type directResolverOpen func(
	context.Context,
	*authority,
	string,
) (*gocaller.DirectResolver, error)

type pairExecute func(context.Context, ExecuteRequest) error

type pairInstall func(
	context.Context,
	*callerleaf.Prepared,
	*callerleaf.ArtifactInventory,
) (*callerleaf.Publication, error)

type cachedResolvers struct {
	key         string
	publication *resolvercatalog.Publication
	values      map[string]*gocaller.DirectResolver
}

type validationCache struct {
	key      string
	pairs    map[string]struct{}
	complete bool
}

// Worker executes one unsettled pair at a time and drains canonical pairs
// within one bounded claim. The resolver projection and successful artifact
// validations are process-bounded to one generation; they never accumulate
// across repositories.
type Worker struct {
	dataDir      string
	root         string
	resolverRoot string
	store        Store
	manifests    extract.CandidateCallerPlanProvider
	registry     *Registry
	readBlob     BlobReader
	open         resolverOpen
	resolve      directResolverOpen
	execute      pairExecute
	install      pairInstall

	cacheMu     sync.Mutex
	resolvers   cachedResolvers
	validations validationCache
}

// Root returns the caller-owned derived-artifact root for one data directory.
func Root(dataDir string) string { return filepath.Join(dataDir, "caller-leaves") }

func NewWorker(
	dataDir string,
	state Store,
	manifests extract.CandidateCallerPlanProvider,
	registry *Registry,
) (*Worker, error) {
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return nil, errors.New("caller worker data directory must be absolute")
	}
	if state == nil || manifests == nil {
		return nil, errors.New("caller worker state and candidate provider are required")
	}
	if err := registry.validate(); err != nil {
		return nil, err
	}
	worker := &Worker{
		dataDir: dataDir, root: Root(dataDir),
		resolverRoot: filepath.Join(dataDir, "resolver-catalogs"),
		store:        state, manifests: manifests, registry: registry,
		readBlob: gitobj.ReadBlob, open: resolvermaterialize.OpenCallerResolvers,
		execute: ExecutePair,
		install: func(
			ctx context.Context,
			prepared *callerleaf.Prepared,
			inventory *callerleaf.ArtifactInventory,
		) (*callerleaf.Publication, error) {
			return prepared.InstallWithInventory(ctx, inventory)
		},
	}
	worker.resolve = worker.resolverFor
	return worker, nil
}

func (worker *Worker) Handle(ctx context.Context, job store.Job) error {
	err := worker.handle(ctx, job)
	if err == nil {
		return nil
	}
	wrapped := store.WithClass(
		store.ClassExtract,
		fmt.Errorf("caller leaf %s: %w", job.Target, err),
	)
	if errors.Is(err, callerleaf.ErrLimit) ||
		errors.Is(err, callerleaf.ErrNondeterministic) {
		return store.WithTerminal(wrapped)
	}
	return wrapped
}

func withSuccessorRetry(err error, queued bool) error {
	if err == nil || !queued {
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
	// The queue transaction may have committed before returning an operational
	// error. Marking the uncertainty is safe even when no row was created: the
	// runner's provenance-aware transition then fails/requeues only the active
	// claim unless the exact lease-owned successor exists.
	return store.WithSuccessorRetry(err)
}

func (worker *Worker) handle(ctx context.Context, job store.Job) error {
	if worker == nil || worker.store == nil || worker.manifests == nil ||
		worker.registry == nil || worker.readBlob == nil || worker.open == nil ||
		worker.resolve == nil || worker.execute == nil || worker.install == nil {
		return errors.New("caller worker is not initialized")
	}
	if job.Kind != store.JobCallerLeaf || job.Target == "" {
		return errors.New("caller worker received the wrong job kind")
	}
	workCtx, cancel := context.WithTimeout(ctx, callerleaf.WorkerTimeout)
	defer cancel()

	current, err := worker.currentAuthority(workCtx, job.Target)
	if err != nil || current == nil {
		return err
	}
	if job.Force {
		worker.forgetValidation(current.semantic.Digest)
	}
	if admission, getErr := worker.store.GetCallerGenerationAdmission(
		workCtx, current.stored,
	); getErr == nil && admission != nil && worker.generationValidated(current.semantic.Digest) {
		return nil
	} else if getErr != nil && !errors.Is(getErr, store.ErrNotFound) &&
		!errors.Is(getErr, store.ErrInvalidCallerLeafState) {
		return getErr
	}

	repoDir, err := reposync.SafeRepoDir(worker.dataDir, job.Target)
	if err != nil {
		return fmt.Errorf("mirror path: %w", err)
	}
	unlock, err := repowork.LockContext(workCtx, repoDir)
	if err != nil {
		return fmt.Errorf("lock mirror: %w", err)
	}
	defer unlock()

	current, err = worker.currentAuthority(workCtx, job.Target)
	if err != nil || current == nil {
		return err
	}
	if err := gitobj.RejectAlternates(repoDir); err != nil {
		return fmt.Errorf("caller mirror object boundary: %w", err)
	}
	plan, err := worker.manifests.OpenCandidateCallerPlan(workCtx, current.request)
	if err != nil {
		return fmt.Errorf("open candidate caller plan: %w", err)
	}
	if plan == nil || plan.Identity() != current.semantic.CandidateManifestDigest ||
		plan.CandidateControlRevision() != current.stored.CandidateControlRevision {
		return errors.New("candidate caller plan has stale control authority")
	}
	pairs, _, err := ExpectedPairs(plan, current.semantic, worker.registry)
	if err != nil {
		return fmt.Errorf("expected caller pairs: %w", err)
	}
	storePairs := make([]store.CallerLeafPair, len(pairs))
	for index := range pairs {
		storePairs[index] = storePair(pairs[index])
	}
	storeSetDigest, err := store.ComputeCallerLeafPairSetDigest(
		current.stored, storePairs,
	)
	if err != nil {
		return err
	}
	semanticPairs := make([]callerleaf.PairIdentity, len(pairs))
	for index := range pairs {
		semanticPairs[index] = pairs[index].Identity
	}
	semanticSetDigest, err := callerleaf.PairSetDigest(current.semantic, semanticPairs)
	if err != nil || semanticSetDigest != storeSetDigest {
		return errors.New("caller pair-set identity differs across artifact and store writers")
	}

	admission, admissionErr := worker.store.GetCallerGenerationAdmission(
		workCtx, current.stored,
	)
	if admissionErr != nil && !errors.Is(admissionErr, store.ErrNotFound) {
		if errors.Is(admissionErr, store.ErrInvalidCallerLeafState) {
			return worker.recoverGeneration(workCtx, job, current.stored)
		}
		return admissionErr
	}
	outcomes, err := worker.store.ListCallerLeafOutcomes(workCtx, current.stored)
	if err != nil {
		if errors.Is(err, store.ErrInvalidCallerLeafState) {
			return worker.recoverGeneration(workCtx, job, current.stored)
		}
		return err
	}
	settled, recovered, err := worker.validateOutcomes(
		workCtx, job, current, pairs, outcomes,
	)
	if err != nil {
		return withSuccessorRetry(err, recovered)
	}
	if recovered {
		return nil
	}
	if admission != nil {
		if settled != len(pairs) || store.ValidateCallerGenerationAdmission(
			*admission, storePairs, outcomes,
		) != nil {
			return worker.recoverGeneration(workCtx, job, current.stored)
		}
		worker.markGenerationValidated(current.semantic.Digest)
		return nil
	}
	if settled == len(pairs) {
		if err := worker.ensureJobSuccessor(workCtx, job, false); err != nil {
			return fmt.Errorf("enqueue caller admission recovery: %w", err)
		}
		disposition := admissionDisposition(outcomes)
		err := worker.store.RecordCallerGenerationAdmission(
			workCtx, job,
			store.CallerGenerationAdmission{
				Generation: current.stored, Disposition: disposition,
				PeakOpenFiles: callerleaf.MaxOpenFiles,
			},
			storePairs,
		)
		if errors.Is(err, store.ErrConflict) {
			return nil
		}
		if err != nil {
			return store.WithSuccessorRetry(
				fmt.Errorf("record settled caller admission: %w", err),
			)
		}
		worker.markGenerationValidated(current.semantic.Digest)
		return nil
	}

	outcomeByDigest := make(map[string]store.CallerLeafOutcome, len(outcomes))
	aggregate := callerleaf.AggregateReceipt{}
	aggregateExceeded := false
	for _, outcome := range outcomes {
		outcomeByDigest[outcome.Pair.PairDigest] = outcome
		if outcome.Disposition != store.CallerLeafSucceeded {
			continue
		}
		if aggregateExceeded || outcome.Receipt == nil {
			return worker.recoverGeneration(workCtx, job, current.stored)
		}
		if addErr := aggregate.Add(artifactReceipt(*outcome.Receipt)); addErr != nil {
			if !errors.Is(addErr, callerleaf.ErrLimit) {
				return addErr
			}
			aggregateExceeded = true
		}
	}
	var inventory *callerleaf.ArtifactInventory
	if !aggregateExceeded {
		inventory, err = callerleaf.OpenArtifactInventory(
			workCtx, worker.root, current.semantic.Repository,
		)
		if err != nil {
			return fmt.Errorf("inventory caller artifacts: %w", err)
		}
		defer func() { _ = inventory.Close() }()
	}
	contentRefused := aggregateExceeded
	resolverByProtocol := make(map[string]*gocaller.DirectResolver)
	successorQueued := false
	for index := range pairs {
		next := &pairs[index]
		if _, ok := outcomeByDigest[next.Identity.Digest]; ok {
			continue
		}
		if contentRefused {
			outcome, queued, recordErr := worker.recordTerminalRefusal(
				workCtx, job, current, *next,
			)
			successorQueued = successorQueued || queued
			if recordErr != nil {
				return withSuccessorRetry(recordErr, successorQueued)
			}
			outcomeByDigest[next.Identity.Digest] = outcome
			continue
		}
		if !inventory.CanPrepare(next.Identity.Digest) {
			return withSuccessorRetry(callerleaf.ErrCapacity, successorQueued)
		}
		resolver := resolverByProtocol[next.Adapter.Protocol]
		if resolver == nil {
			resolver, err = worker.resolve(workCtx, current, next.Adapter.Protocol)
			if err != nil {
				return withSuccessorRetry(
					fmt.Errorf("open caller resolver: %w", err), successorQueued,
				)
			}
			resolverByProtocol[next.Adapter.Protocol] = resolver
		}
		outcome, queued, stop, runErr := worker.executeAndRecordPair(
			workCtx, job, repoDir, plan, current, *next, resolver, inventory,
		)
		successorQueued = successorQueued || queued
		if runErr != nil {
			return withSuccessorRetry(runErr, successorQueued)
		}
		if stop {
			return nil
		}
		outcomeByDigest[next.Identity.Digest] = outcome
		if outcome.Disposition == store.CallerLeafSucceeded {
			if outcome.Receipt == nil {
				return worker.recoverGeneration(workCtx, job, current.stored)
			}
			if addErr := aggregate.Add(artifactReceipt(*outcome.Receipt)); addErr != nil {
				if !errors.Is(addErr, callerleaf.ErrLimit) {
					return withSuccessorRetry(addErr, successorQueued)
				}
				contentRefused = true
			}
		}
	}
	outcomes = make([]store.CallerLeafOutcome, len(pairs))
	for index := range pairs {
		outcome, ok := outcomeByDigest[pairs[index].Identity.Digest]
		if !ok {
			return withSuccessorRetry(
				errors.New("caller outcome set is not an exact subset of expected pairs"),
				successorQueued,
			)
		}
		outcomes[index] = outcome
	}
	disposition := admissionDisposition(outcomes)
	if !successorQueued {
		if err := worker.ensureJobSuccessor(workCtx, job, false); err != nil {
			return fmt.Errorf("enqueue caller admission recovery: %w", err)
		}
		successorQueued = true
	}
	err = worker.store.RecordCallerGenerationAdmission(
		workCtx, job,
		store.CallerGenerationAdmission{
			Generation: current.stored, Disposition: disposition,
			PeakOpenFiles: callerleaf.MaxOpenFiles,
		},
		storePairs,
	)
	if errors.Is(err, store.ErrConflict) {
		return nil
	}
	if err != nil {
		return withSuccessorRetry(
			fmt.Errorf("record caller generation admission: %w", err),
			successorQueued,
		)
	}
	worker.markGenerationValidated(current.semantic.Digest)
	return nil
}

func (worker *Worker) recordTerminalRefusal(
	ctx context.Context,
	job store.Job,
	current *authority,
	pair ExpectedPair,
) (store.CallerLeafOutcome, bool, error) {
	outcome := store.CallerLeafOutcome{
		Generation: current.stored, Pair: storePair(pair),
		Disposition: store.CallerLeafTerminalGenerationRefusal,
	}
	if err := worker.ensureJobSuccessor(ctx, job, false); err != nil {
		return outcome, false, fmt.Errorf("enqueue terminal caller refusal: %w", err)
	}
	if err := worker.store.RecordCallerLeafOutcome(ctx, job, outcome); err != nil {
		return outcome, true, err
	}
	return outcome, true, nil
}

func (worker *Worker) executeAndRecordPair(
	ctx context.Context,
	job store.Job,
	repositoryDir string,
	plan extract.CandidateCallerPlan,
	current *authority,
	pair ExpectedPair,
	resolver *gocaller.DirectResolver,
	inventory *callerleaf.ArtifactInventory,
) (outcome store.CallerLeafOutcome, queued, stop bool, resultErr error) {
	stage, err := callerleaf.NewStageWithInventory(
		worker.root, current.semantic, pair.Identity, inventory,
	)
	if err != nil {
		return outcome, false, false, err
	}
	defer func() { _ = stage.Discard() }()
	err = worker.execute(ctx, ExecuteRequest{
		RepositoryDir: repositoryDir, Plan: plan, Pair: pair,
		Protocol:              pair.Adapter.Protocol,
		ResolverCatalogDigest: current.semantic.ResolverManifestDigest,
		Resolver:              resolver, ReadBlob: worker.readBlob, Stage: stage,
	})
	terminal := errors.Is(err, callerleaf.ErrLimit)
	if err != nil && !terminal {
		return outcome, false, false, err
	}
	if !terminal {
		var prepared *callerleaf.Prepared
		prepared, err = stage.Seal()
		if errors.Is(err, callerleaf.ErrLimit) {
			terminal = true
		} else if err != nil {
			return outcome, false, false, err
		}
		if !terminal {
			defer func() { _ = prepared.Discard() }()
			if err = worker.ensureJobSuccessor(ctx, job, false); err != nil {
				return outcome, false, false, fmt.Errorf(
					"enqueue caller publication recovery: %w", err,
				)
			}
			queued = true
			var publication *callerleaf.Publication
			publication, err = worker.install(ctx, prepared, inventory)
			if errors.Is(err, callerleaf.ErrNondeterministic) {
				exactPath, pathErr := callerleaf.ArtifactPath(
					worker.root, current.semantic.Repository, prepared.Receipt(),
				)
				if pathErr != nil {
					return outcome, true, false, pathErr
				}
				divergent := false
				if _, statErr := os.Lstat(exactPath); statErr == nil {
					if _, openErr := callerleaf.Open(
						ctx, worker.root, current.semantic, pair.Identity,
						prepared.Receipt(), nil,
					); openErr == nil {
						// Install saw a different pair-prefix sibling before it
						// reached this valid exact target.
						divergent = true
					} else if errors.Is(openErr, callerleaf.ErrInvalidArtifact) {
						if queueErr := worker.ensureJobSuccessor(
							ctx, job, true,
						); queueErr != nil {
							return outcome, true, false, queueErr
						}
						if removeErr := callerleaf.RemoveArtifact(
							worker.root, current.semantic.Repository, prepared.Receipt(),
						); removeErr != nil {
							return outcome, true, false, removeErr
						}
						worker.forgetValidation(current.semantic.Digest)
						return outcome, true, true, nil
					} else {
						// Operational exact-target I/O retains every byte. Returning
						// the error lets the runner merge attempt/backoff state into
						// the already ensured successor.
						return outcome, true, false, openErr
					}
				} else if errors.Is(statErr, os.ErrNotExist) {
					divergent = true
				} else {
					return outcome, true, false, statErr
				}
				if !divergent {
					return outcome, true, false, errors.New(
						"caller artifact divergence could not be classified",
					)
				}
				// Install observed a distinct content artifact for this immutable
				// pair, either alone or alongside the valid exact target. Preserve
				// those bytes and durably refuse only this pair as nondeterministic.
				outcome = store.CallerLeafOutcome{
					Generation: current.stored, Pair: storePair(pair),
					Disposition: store.CallerLeafTerminalGenerationRefusal,
				}
				if err = worker.store.RecordCallerLeafOutcome(ctx, job, outcome); err != nil {
					return outcome, true, false, err
				}
				return outcome, true, false, nil
			}
			if errors.Is(err, callerleaf.ErrLimit) {
				outcome = store.CallerLeafOutcome{
					Generation: current.stored, Pair: storePair(pair),
					Disposition: store.CallerLeafTerminalGenerationRefusal,
				}
				if err = worker.store.RecordCallerLeafOutcome(ctx, job, outcome); err != nil {
					return outcome, true, false, err
				}
				return outcome, true, false, nil
			}
			if err != nil {
				return outcome, true, false, err
			}
			receipt := publication.Receipt()
			outcome = store.CallerLeafOutcome{
				Generation: current.stored, Pair: storePair(pair),
				Disposition: store.CallerLeafSucceeded,
				Receipt:     storeReceipt(receipt),
			}
			if err = worker.store.RecordCallerLeafOutcome(ctx, job, outcome); err != nil {
				return outcome, true, false, err
			}
			worker.markPairValidated(current.semantic.Digest, pair.Identity.Digest)
			return outcome, true, false, nil
		}
	}
	if err = worker.ensureJobSuccessor(ctx, job, false); err != nil {
		return outcome, false, false, fmt.Errorf(
			"enqueue terminal caller recovery: %w", err,
		)
	}
	outcome = store.CallerLeafOutcome{
		Generation: current.stored, Pair: storePair(pair),
		Disposition: store.CallerLeafTerminalGenerationRefusal,
	}
	if err = worker.store.RecordCallerLeafOutcome(ctx, job, outcome); err != nil {
		return outcome, true, false, err
	}
	return outcome, true, false, nil
}

func (worker *Worker) currentAuthority(
	ctx context.Context,
	repository string,
) (*authority, error) {
	repo, err := worker.store.GetRepo(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if repo == nil || repo.Name != repository {
		return nil, errors.New("caller store returned a mismatched repository")
	}
	if repo.Deleting || repo.IndexedCommitHash == "" {
		return nil, nil
	}
	if repo.IndexedAnalysisUnit != nil {
		if err := repo.IndexedAnalysisUnit.Validate(repo.Name); err != nil {
			return nil, err
		}
	}
	candidatePointer, err := worker.store.GetCandidateManifestPublication(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	resolverPointer, err := worker.store.GetResolverCatalogPublication(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	resolverCurrent, err := worker.store.ResolverCatalogPublicationCurrent(
		ctx, *resolverPointer,
	)
	if err != nil {
		return nil, err
	}
	if !resolverCurrent {
		return nil, nil
	}
	semantic, err := GenerationIdentity(GenerationAuthority{
		Repository: repo, Candidate: candidatePointer, Resolver: resolverPointer,
	}, worker.registry)
	if err != nil {
		return nil, nil
	}
	stored := storeGeneration(semantic, *candidatePointer, *resolverPointer)
	if stored.Digest != semantic.Digest ||
		store.ComputeCallerGenerationDigest(stored) != semantic.Digest {
		return nil, errors.New("caller generation identity differs across artifact and store writers")
	}
	return &authority{
		repository: repo, candidate: candidatePointer, resolver: resolverPointer,
		semantic: semantic, stored: stored,
		request: extract.CandidateManifestRequest{
			Repository: repository, Commit: repo.IndexedCommitHash,
			AnalysisUnit: analysisunit.CloneState(repo.IndexedAnalysisUnit),
			Domains:      worker.registry.CandidateDomains(),
		},
	}, nil
}

func storeGeneration(
	semantic callerleaf.GenerationIdentity,
	candidate store.CandidateManifestPublication,
	resolver store.ResolverCatalogPublication,
) store.CallerGenerationIdentity {
	return store.CallerGenerationIdentity{
		Repository: semantic.Repository, HeadCommit: semantic.HeadCommit,
		UnitDigest:               semantic.UnitDigest,
		DeclarationSetDigest:     semantic.DeclarationSetDigest,
		CandidateManifestDigest:  semantic.CandidateManifestDigest,
		CandidatePolicyDigest:    semantic.CandidatePolicyDigest,
		CandidateControlRevision: candidate.ControlRevision,
		ResolverGenerationDigest: semantic.ResolverGenerationDigest,
		ResolverManifestDigest:   semantic.ResolverManifestDigest,
		ResolverControlRevision:  resolver.ControlRevision,
		ResolverWriterSchema:     resolver.WriterSchema,
		SourceLanePolicy:         semantic.SourceLanePolicy,
		CallerPolicyDigest:       semantic.CallerPolicyDigest,
		ExtractorSetDigest:       semantic.ExtractorSetDigest,
		Digest:                   semantic.Digest,
	}
}

func storePair(pair ExpectedPair) store.CallerLeafPair {
	leaf := pair.Identity.Leaf
	return store.CallerLeafPair{
		Domain:             pair.Identity.Domain,
		ExtractorVersion:   pair.Identity.ExtractorVersion,
		LeafAdapterVersion: pair.Identity.LeafAdapterVersion,
		LeafOrdinal:        leaf.Ordinal, LeafPrefix: leaf.Prefix,
		LeafPrefixBits: leaf.PrefixBits, CandidateMemberName: leaf.Name,
		CandidateRecordCount:   leaf.RecordCount,
		CandidateDeclaredBytes: leaf.DeclaredBytes,
		CandidateContentBytes:  leaf.ContentBytes,
		CandidateContentDigest: leaf.ContentDigest,
		PairDigest:             pair.Identity.Digest,
	}
}

func storeReceipt(receipt callerleaf.Receipt) *store.CallerLeafArtifactReceipt {
	return &store.CallerLeafArtifactReceipt{
		ArtifactName: receipt.Name, ArtifactCount: 1,
		ResultCount:     receipt.ResultCount,
		AbstentionCount: receipt.AbstentionCount,
		CanonicalBytes:  receipt.ContentBytes, StagingBytes: receipt.StagingBytes,
		ContentDigest: receipt.ContentDigest, MetadataDigest: receipt.MetadataDigest,
		ExcludedGoTestRecords: receipt.ExcludedGoTestRecords,
		SourceBlobReads:       receipt.SourceBlobReads,
		SourceBlobBytes:       receipt.SourceBlobBytes,
		OutOfLeafReads:        receipt.OutOfLeafReads,
	}
}

func artifactReceipt(receipt store.CallerLeafArtifactReceipt) callerleaf.Receipt {
	return callerleaf.Receipt{
		Name:            receipt.ArtifactName,
		RecordCount:     receipt.ResultCount + receipt.AbstentionCount,
		ResultCount:     receipt.ResultCount,
		AbstentionCount: receipt.AbstentionCount,
		ContentBytes:    receipt.CanonicalBytes, ContentDigest: receipt.ContentDigest,
		MetadataDigest: receipt.MetadataDigest, StagingBytes: receipt.StagingBytes,
		ExcludedGoTestRecords: receipt.ExcludedGoTestRecords,
		SourceBlobReads:       receipt.SourceBlobReads,
		SourceBlobBytes:       receipt.SourceBlobBytes,
		OutOfLeafReads:        receipt.OutOfLeafReads,
	}
}

func (worker *Worker) validateOutcomes(
	ctx context.Context,
	job store.Job,
	current *authority,
	pairs []ExpectedPair,
	outcomes []store.CallerLeafOutcome,
) (int, bool, error) {
	if len(outcomes) > len(pairs) {
		return 0, true, worker.recoverGeneration(ctx, job, current.stored)
	}
	expected := make(map[string]ExpectedPair, len(pairs))
	for _, pair := range pairs {
		expected[pair.Identity.Digest] = pair
	}
	for _, outcome := range outcomes {
		pair, ok := expected[outcome.Pair.PairDigest]
		if !ok || outcome.Pair != storePair(pair) {
			return 0, true, worker.recoverGeneration(ctx, job, current.stored)
		}
		if outcome.Disposition == store.CallerLeafTerminalGenerationRefusal {
			continue
		}
		if outcome.Disposition != store.CallerLeafSucceeded || outcome.Receipt == nil {
			return 0, true, worker.recoverGeneration(ctx, job, current.stored)
		}
		if worker.pairValidated(current.semantic.Digest, pair.Identity.Digest) {
			continue
		}
		receipt := artifactReceipt(*outcome.Receipt)
		receiptValid := callerleaf.ValidateReceipt(
			current.semantic, pair.Identity, receipt,
		) == nil
		_, err := callerleaf.Open(
			ctx, worker.root, current.semantic, pair.Identity, receipt, nil,
		)
		if err == nil {
			worker.markPairValidated(current.semantic.Digest, pair.Identity.Digest)
			continue
		}
		if !errors.Is(err, callerleaf.ErrInvalidArtifact) &&
			!errors.Is(err, os.ErrNotExist) {
			return 0, false, err
		}
		if queueErr := worker.ensureJobSuccessor(ctx, job, true); queueErr != nil {
			return 0, false, queueErr
		}
		if receiptValid {
			if removeErr := callerleaf.RemoveArtifact(
				worker.root, current.semantic.Repository, receipt,
			); removeErr != nil {
				return 0, true, fmt.Errorf(
					"remove invalid caller artifact: %w", removeErr,
				)
			}
		}
		if clearErr := worker.store.ClearCallerLeafOutcome(
			ctx, current.stored, outcome.Pair,
		); clearErr != nil {
			return 0, true, fmt.Errorf(
				"clear invalid caller outcome: %w", clearErr,
			)
		}
		worker.forgetValidation(current.semantic.Digest)
		return 0, true, nil
	}
	return len(outcomes), false, nil
}

func admissionDisposition(
	outcomes []store.CallerLeafOutcome,
) store.CallerGenerationAdmissionDisposition {
	results, abstentions := 0, 0
	canonical, staged := int64(0), int64(0)
	for _, outcome := range outcomes {
		if outcome.Disposition != store.CallerLeafSucceeded || outcome.Receipt == nil {
			return store.CallerGenerationTerminalGenerationRefusal
		}
		results += outcome.Receipt.ResultCount
		abstentions += outcome.Receipt.AbstentionCount
		canonical += outcome.Receipt.CanonicalBytes
		staged += outcome.Receipt.StagingBytes
	}
	if results > callerleaf.MaxAggregateResultRecords ||
		abstentions > callerleaf.MaxAggregateAbstentionRecords ||
		canonical > callerleaf.MaxAggregateCanonicalBytes ||
		staged > callerleaf.MaxAggregateStagingBytes {
		return store.CallerGenerationTerminalGenerationRefusal
	}
	return store.CallerGenerationAdmitted
}

func (worker *Worker) recoverGeneration(
	ctx context.Context,
	job store.Job,
	generation store.CallerGenerationIdentity,
) error {
	if err := worker.ensureJobSuccessor(ctx, job, true); err != nil {
		return err
	}
	if err := worker.store.ClearCallerLeafGeneration(ctx, generation); err != nil {
		// The marker transfers retry state into the forced pending successor and
		// fails that successor atomically if this was the final allowed attempt.
		return store.WithSuccessorRetry(
			fmt.Errorf("clear invalid caller generation: %w", err),
		)
	}
	worker.forgetValidation(generation.Digest)
	return nil
}

func (worker *Worker) resolverFor(
	ctx context.Context,
	current *authority,
	protocol string,
) (*gocaller.DirectResolver, error) {
	key := current.semantic.Digest + "\x00" + current.resolver.ManifestDigest
	worker.cacheMu.Lock()
	cache := worker.resolvers
	worker.cacheMu.Unlock()
	if cache.key == key && cache.publication != nil && cache.publication.Current() {
		if value := cache.values[protocol]; value != nil {
			return value, nil
		}
	}
	protocols := make([]resolvermaterialize.Protocol, 0, len(worker.registry.adapters))
	for _, adapter := range worker.registry.adapters {
		value := resolvermaterialize.Protocol(adapter.Protocol)
		if !slices.Contains(protocols, value) {
			protocols = append(protocols, value)
		}
	}
	publication, views, err := worker.open(
		ctx, worker.resolverRoot, resolverState(*current.resolver), protocols,
	)
	if err != nil {
		return nil, err
	}
	values := make(map[string]*gocaller.DirectResolver, len(views))
	for currentProtocol, view := range views {
		if view == nil || view.GenerationDigest() != current.resolver.GenerationDigest ||
			view.ManifestDigest() != current.resolver.ManifestDigest || view.Resolver() == nil {
			return nil, errors.New("caller resolver view has stale authority")
		}
		values[string(currentProtocol)] = view.Resolver()
	}
	worker.cacheMu.Lock()
	worker.resolvers = cachedResolvers{
		key: key, publication: publication, values: values,
	}
	worker.cacheMu.Unlock()
	value := values[protocol]
	if value == nil {
		return nil, errors.New("caller resolver view omitted a configured protocol")
	}
	return value, nil
}

func resolverState(pointer store.ResolverCatalogPublication) resolvercatalog.State {
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
		packs[index] = resolvercatalog.ResolverPack{Name: pack.Name, Version: pack.Version}
	}
	return resolvercatalog.State{
		Schema:     resolvercatalog.StateSchema,
		Repository: pointer.Repository, Commit: pointer.HeadCommit,
		UnitDigest: pointer.UnitDigest, Declarations: declarations,
		DeclarationSetDigest:    pointer.DeclarationSetDigest,
		CandidateManifestDigest: pointer.CandidateManifestDigest,
		SourceLanePolicy:        pointer.SourceLanePolicy, ResolverPacks: packs,
		ResolverPackSetDigest: pointer.ResolverPackSetDigest,
		CatalogPolicyDigest:   pointer.CatalogPolicyDigest,
		GenerationDigest:      pointer.GenerationDigest,
		ManifestDigest:        pointer.ManifestDigest, Manifest: pointer.ManifestPath,
	}
}

func (worker *Worker) pairValidated(generation, pair string) bool {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		return false
	}
	_, ok := worker.validations.pairs[pair]
	return ok
}

func (worker *Worker) markPairValidated(generation, pair string) {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		worker.validations = validationCache{
			key: generation, pairs: make(map[string]struct{}),
		}
	}
	worker.validations.pairs[pair] = struct{}{}
}

func (worker *Worker) generationValidated(generation string) bool {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	return worker.validations.key == generation && worker.validations.complete
}

func (worker *Worker) markGenerationValidated(generation string) {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		worker.validations = validationCache{
			key: generation, pairs: make(map[string]struct{}),
		}
	}
	worker.validations.complete = true
}

func (worker *Worker) forgetValidation(generation string) {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key == generation {
		worker.validations = validationCache{}
	}
}

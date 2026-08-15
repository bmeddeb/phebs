package callerexecute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

var ErrPartitionedCallerUnavailable = errors.New("partitioned caller upstream is unavailable")

// Store is the exact state boundary used by the direct caller-leaf worker.
type Store interface {
	store.CallerLeafStore
	store.CallerGenerationPublicationStore
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
	key         string
	pairs       map[string]*callerleaf.Publication
	settled     bool
	publication *callerpublication.Publication
}

// Worker executes one unsettled pair at a time and drains canonical pairs
// within one bounded claim. The resolver projection and successful artifact
// validations are process-bounded to one generation; they never accumulate
// across repositories.
type Worker struct {
	dataDir      string
	root         string
	resolverRoot string
	timeout      time.Duration
	store        Store
	manifests    extract.CandidateCallerPlanProvider
	registry     *Registry
	readBlob     BlobReader
	open         resolverOpen
	resolve      directResolverOpen
	execute      pairExecute
	install      pairInstall
	publications *callerpublication.Registry

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
	return NewWorkerWithPublicationRegistry(
		dataDir, state, manifests, registry,
		callerpublication.NewRegistry(Root(dataDir)),
	)
}

// NewWorkerWithPublicationRegistry shares the process-wide complete-generation
// lease registry with startup reconciliation and future authorized readers.
func NewWorkerWithPublicationRegistry(
	dataDir string,
	state Store,
	manifests extract.CandidateCallerPlanProvider,
	registry *Registry,
	publications *callerpublication.Registry,
) (*Worker, error) {
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return nil, errors.New("caller worker data directory must be absolute")
	}
	if state == nil || manifests == nil || publications == nil {
		return nil, errors.New("caller worker state and candidate provider are required")
	}
	if err := registry.validate(); err != nil {
		return nil, err
	}
	worker := &Worker{
		dataDir: dataDir, root: Root(dataDir),
		resolverRoot: filepath.Join(dataDir, "resolver-catalogs"),
		timeout:      callerleaf.WorkerTimeout,
		store:        state, manifests: manifests, registry: registry,
		publications: publications,
		readBlob:     gitobj.ReadBlob, open: resolvermaterialize.OpenCallerResolvers,
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
		worker.resolve == nil || worker.execute == nil || worker.install == nil ||
		worker.publications == nil {
		return errors.New("caller worker is not initialized")
	}
	if worker.timeout <= 0 || worker.timeout > callerleaf.WorkerTimeout {
		return errors.New("caller worker timeout must be a positive production tightening")
	}
	if job.Kind != store.JobCallerLeaf || job.Target == "" {
		return errors.New("caller worker received the wrong job kind")
	}

	// Bound the pointer/control preflight independently. The immutable-mirror
	// wait that follows is serialization behind fetch, indexing, candidate
	// planning, and aggregate extraction; it is not caller-leaf execution and
	// must not consume the caller's fixed work budget. The runner continues to
	// heartbeat the lease while LockContext waits, and parent cancellation or
	// lease loss still interrupts the wait without a caller state mutation.
	preflightCtx, cancelPreflight := context.WithTimeout(ctx, worker.timeout)
	defer cancelPreflight()

	current, err := worker.currentAuthority(preflightCtx, job.Target)
	if errors.Is(err, ErrPartitionedCallerUnavailable) {
		if clearErr := worker.store.ClearCallerGenerationPublication(preflightCtx, job.Target); clearErr != nil &&
			!errors.Is(clearErr, store.ErrNotFound) {
			return clearErr
		}
		return worker.publications.Retire(preflightCtx, job.Target)
	}
	if err != nil || current == nil {
		return err
	}
	if err := worker.publications.ActivateRepository(
		preflightCtx, job.Target,
	); err != nil {
		return fmt.Errorf("activate caller publication repository: %w", err)
	}
	if job.Force {
		worker.forgetValidation(current.semantic.Digest)
	}
	if admission, getErr := worker.store.GetCallerGenerationAdmission(
		preflightCtx, current.stored,
	); getErr == nil && admission != nil {
		switch admission.Disposition {
		case store.CallerGenerationAdmitted:
			currentPublication, publicationErr := worker.completePublicationCurrent(
				preflightCtx, current,
			)
			if publicationErr != nil {
				if deterministicPublicationFailure(publicationErr) {
					return worker.handlePublicationFailure(preflightCtx, job, current)
				}
				return publicationErr
			}
			if currentPublication {
				return nil
			}
			if cached := worker.cachedGenerationPublication(
				current.semantic.Digest,
			); cached != nil {
				return worker.republishCachedGeneration(
					preflightCtx, job, current, cached,
				)
			}
		case store.CallerGenerationTerminalGenerationRefusal:
			if worker.generationSettled(current.semantic.Digest) {
				return nil
			}
		}
	} else if getErr != nil && !errors.Is(getErr, store.ErrNotFound) &&
		!errors.Is(getErr, store.ErrInvalidCallerLeafState) {
		return getErr
	}

	repoDir, err := reposync.SafeRepoDir(worker.dataDir, job.Target)
	if err != nil {
		return fmt.Errorf("mirror path: %w", err)
	}
	cancelPreflight()
	unlock, err := repowork.LockContext(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("lock mirror: %w", err)
	}
	defer unlock()

	// Caller-leaf execution owns a fresh, fixed budget only after the mirror is
	// fenced. This preserves the five-minute cap while preventing an unrelated
	// admitted holder from spending the caller's retry attempts on lock wait.
	workCtx, cancelWork := context.WithTimeout(ctx, worker.timeout)
	defer cancelWork()

	current, err = worker.currentAuthority(workCtx, job.Target)
	if errors.Is(err, ErrPartitionedCallerUnavailable) {
		if clearErr := worker.store.ClearCallerGenerationPublication(workCtx, job.Target); clearErr != nil &&
			!errors.Is(clearErr, store.ErrNotFound) {
			return clearErr
		}
		return worker.publications.Retire(workCtx, job.Target)
	}
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
		if admission.Disposition == store.CallerGenerationAdmitted {
			return worker.publishAdmittedGeneration(
				workCtx, job, current, pairs, outcomes,
			)
		}
		if err := worker.retireUnavailablePublication(
			workCtx, current.semantic.Repository,
		); err != nil {
			return err
		}
		worker.markGenerationSettled(current.semantic.Digest)
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
		if disposition == store.CallerGenerationAdmitted {
			return worker.publishAdmittedGeneration(
				workCtx, job, current, pairs, outcomes,
			)
		}
		if err := worker.retireUnavailablePublication(
			workCtx, current.semantic.Repository,
		); err != nil {
			return err
		}
		worker.markGenerationSettled(current.semantic.Digest)
		return nil
	}

	outcomeByDigest := make(map[string]store.CallerLeafOutcome, len(outcomes))
	aggregate := callerleaf.AggregateReceipt{}
	aggregateExceeded := false
	var aggregateRefusal *pipelinerefusal.Receipt
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
			aggregateRefusal = callerRefusal(
				addErr, pipelinerefusal.StageCallerGenerationAdmission,
			)
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
				workCtx, job, current, *next, aggregateRefusal,
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
			workCtx, ctx, job, repoDir, plan, current, *next, resolver, inventory,
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
			if addErr := aggregate.Add(artifactReceipt(*outcome.Receipt)); addErr != nil &&
				!errors.Is(addErr, callerleaf.ErrLimit) {
				return withSuccessorRetry(addErr, successorQueued)
			}
		}
		// One replayed pair per turn: the outcome above is durable and
		// executeAndRecordPair ensured the pending successor before touching
		// any artifact, so completing this job now hands the remaining pairs
		// a fresh worker deadline and attempt budget instead of starting the
		// next leaf replay against whatever remains of this turn's. Terminal
		// no-content refusals above still drain in one turn; the successor
		// turn recomputes the aggregate from stored outcomes, so a pair that
		// crossed the aggregate cap here refuses later pairs there.
		return nil
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
	if disposition == store.CallerGenerationAdmitted {
		return worker.publishAdmittedGeneration(
			workCtx, job, current, pairs, outcomes,
		)
	}
	if err := worker.retireUnavailablePublication(
		ctx, current.semantic.Repository,
	); err != nil {
		return withSuccessorRetry(err, successorQueued)
	}
	worker.markGenerationSettled(current.semantic.Digest)
	return nil
}

func (worker *Worker) publishAdmittedGeneration(
	ctx context.Context,
	job store.Job,
	current *authority,
	expected []ExpectedPair,
	outcomes []store.CallerLeafOutcome,
) error {
	outcomeByDigest := make(map[string]store.CallerLeafOutcome, len(outcomes))
	for _, outcome := range outcomes {
		if _, exists := outcomeByDigest[outcome.Pair.PairDigest]; exists {
			return worker.recoverGeneration(ctx, job, current.stored)
		}
		outcomeByDigest[outcome.Pair.PairDigest] = outcome
	}
	pairs := make([]callerpublication.PairReceipt, len(expected))
	for index, pair := range expected {
		outcome, ok := outcomeByDigest[pair.Identity.Digest]
		if !ok || outcome.Disposition != store.CallerLeafSucceeded ||
			outcome.Receipt == nil || outcome.Pair != storePair(pair) {
			return worker.recoverGeneration(ctx, job, current.stored)
		}
		pairs[index] = callerpublication.PairReceipt{
			Pair: pair.Identity, Receipt: artifactReceipt(*outcome.Receipt),
		}
	}
	manifest, err := callerpublication.BuildManifest(current.semantic, pairs)
	if err != nil {
		return fmt.Errorf("build complete caller manifest: %w", err)
	}
	if err := worker.ensureJobSuccessor(ctx, job, false); err != nil {
		return fmt.Errorf("enqueue complete caller publication recovery: %w", err)
	}
	prepared, err := callerpublication.PrepareWithValidated(
		ctx, worker.root, manifest,
		worker.validatedPairs(current.semantic.Digest),
	)
	if errors.Is(err, callerpublication.ErrPublishing) {
		return worker.recoverMarkedAdmittedGeneration(
			ctx, job, current, manifest,
		)
	}
	if err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("prepare complete caller publication: %w", err),
		)
	}
	defer func() { _ = prepared.Discard() }()
	publication, err := prepared.Publish(
		ctx,
		func(commitCtx context.Context, state callerpublication.State) error {
			if !reflect.DeepEqual(state, manifest.State()) {
				return errors.New("caller publication stage returned another state")
			}
			return worker.store.PublishCallerGeneration(
				commitCtx, job, storePublication(current.stored, manifest),
			)
		},
	)
	if err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("publish complete caller generation: %w", err),
		)
	}
	if err := worker.publications.Observe(ctx, publication); err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("observe complete caller generation: %w", err),
		)
	}
	worker.markPublication(current.semantic.Digest, publication)
	publicationCurrent, err := worker.completePublicationCurrent(ctx, current)
	if err != nil {
		return store.WithSuccessorRetry(err)
	}
	if !publicationCurrent {
		return store.WithSuccessorRetry(errors.New(
			"complete caller publication lost authority after commit",
		))
	}
	return nil
}

func (worker *Worker) recoverMarkedAdmittedGeneration(
	ctx context.Context,
	job store.Job,
	current *authority,
	manifest callerpublication.Manifest,
) error {
	publication, err := callerpublication.RecoverPublishing(
		ctx, worker.root, current.semantic.Repository,
		worker.registry.ExtractorIdentities(),
		func(commitCtx context.Context, state callerpublication.State) error {
			if !reflect.DeepEqual(state, manifest.State()) {
				return fmt.Errorf(
					"%w: marked state differs from the admitted generation",
					callerpublication.ErrInvalidManifest,
				)
			}
			return worker.store.PublishCallerGeneration(
				commitCtx, job, storePublication(current.stored, manifest),
			)
		},
	)
	if err != nil {
		if !deterministicPublicationFailure(err) {
			return store.WithSuccessorRetry(
				fmt.Errorf("recover marked caller publication: %w", err),
			)
		}
		if queueErr := worker.ensureJobSuccessor(ctx, job, true); queueErr != nil {
			return queueErr
		}
		if abandonErr := callerpublication.AbandonPublishing(
			ctx, worker.root, current.semantic.Repository, nil,
		); abandonErr != nil {
			return store.WithSuccessorRetry(abandonErr)
		}
		worker.forgetValidation(current.semantic.Digest)
		return nil
	}
	if err := worker.publications.Observe(ctx, publication); err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("observe recovered caller generation: %w", err),
		)
	}
	worker.markPublication(current.semantic.Digest, publication)
	publicationCurrent, err := worker.completePublicationCurrent(ctx, current)
	if err != nil {
		return store.WithSuccessorRetry(err)
	}
	if !publicationCurrent {
		return store.WithSuccessorRetry(errors.New(
			"recovered caller publication lost authority after commit",
		))
	}
	return nil
}

func (worker *Worker) republishCachedGeneration(
	ctx context.Context,
	job store.Job,
	current *authority,
	publication *callerpublication.Publication,
) error {
	if publication == nil || !publication.Current() {
		return errors.New("cached caller publication is no longer current")
	}
	if err := worker.ensureJobSuccessor(ctx, job, false); err != nil {
		return fmt.Errorf("enqueue cached caller publication recovery: %w", err)
	}
	if err := worker.store.PublishCallerGeneration(
		ctx, job, storePublication(current.stored, publication.Manifest()),
	); err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("republish cached caller generation: %w", err),
		)
	}
	if err := worker.publications.Observe(ctx, publication); err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("observe cached caller generation: %w", err),
		)
	}
	worker.markPublication(current.semantic.Digest, publication)
	publicationCurrent, err := worker.completePublicationCurrent(ctx, current)
	if err != nil {
		return store.WithSuccessorRetry(err)
	}
	if !publicationCurrent {
		return store.WithSuccessorRetry(errors.New(
			"cached caller publication lost authority after commit",
		))
	}
	return nil
}

func (worker *Worker) recordTerminalRefusal(
	ctx context.Context,
	job store.Job,
	current *authority,
	pair ExpectedPair,
	refusal *pipelinerefusal.Receipt,
) (store.CallerLeafOutcome, bool, error) {
	if refusal == nil || pipelinerefusal.Validate(*refusal) != nil ||
		refusal.GenerationKind != pipelinerefusal.GenerationCaller {
		return store.CallerLeafOutcome{}, false, errors.New(
			"terminal caller refusal is not classified",
		)
	}
	outcome := store.CallerLeafOutcome{
		Generation: current.stored, Pair: storePair(pair),
		Disposition: store.CallerLeafTerminalGenerationRefusal,
		Refusal:     refusal,
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
	executionCtx context.Context,
	durableCtx context.Context,
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
	err = worker.execute(executionCtx, ExecuteRequest{
		RepositoryDir: repositoryDir, Plan: plan, Pair: pair,
		Protocol:              pair.Adapter.Protocol,
		ResolverCatalogDigest: current.semantic.ResolverManifestDigest,
		Resolver:              resolver, ReadBlob: worker.readBlob, Stage: stage,
	})
	terminal := errors.Is(err, callerleaf.ErrLimit)
	var refusal *pipelinerefusal.Receipt
	if terminal {
		refusal = callerRefusal(err, pipelinerefusal.StageCallerPairExecution)
	}
	if err != nil && !terminal {
		return outcome, false, false, err
	}
	if !terminal {
		var prepared *callerleaf.Prepared
		prepared, err = stage.Seal()
		if errors.Is(err, callerleaf.ErrLimit) {
			terminal = true
			refusal = callerRefusal(err, pipelinerefusal.StageCallerArtifactSeal)
		} else if err != nil {
			return outcome, false, false, err
		}
		if !terminal {
			defer func() { _ = prepared.Discard() }()
			if err = worker.ensureJobSuccessor(durableCtx, job, false); err != nil {
				return outcome, false, false, fmt.Errorf(
					"enqueue caller publication recovery: %w", err,
				)
			}
			queued = true
			var publication *callerleaf.Publication
			publication, err = worker.install(durableCtx, prepared, inventory)
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
						durableCtx, worker.root, current.semantic, pair.Identity,
						prepared.Receipt(), nil,
					); openErr == nil {
						// Install saw a different pair-prefix sibling before it
						// reached this valid exact target.
						divergent = true
					} else if errors.Is(openErr, callerleaf.ErrInvalidArtifact) {
						if queueErr := worker.ensureJobSuccessor(
							durableCtx, job, true,
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
					Refusal: callerClassifiedRefusal(
						err, pipelinerefusal.StageCallerArtifactInstall,
						pipelinerefusal.ClassificationInvalid,
					),
				}
				if err = worker.store.RecordCallerLeafOutcome(durableCtx, job, outcome); err != nil {
					return outcome, true, false, err
				}
				return outcome, true, false, nil
			}
			if errors.Is(err, callerleaf.ErrLimit) {
				outcome = store.CallerLeafOutcome{
					Generation: current.stored, Pair: storePair(pair),
					Disposition: store.CallerLeafTerminalGenerationRefusal,
					Refusal: callerRefusal(
						err, pipelinerefusal.StageCallerArtifactInstall,
					),
				}
				if err = worker.store.RecordCallerLeafOutcome(durableCtx, job, outcome); err != nil {
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
			if err = worker.store.RecordCallerLeafOutcome(durableCtx, job, outcome); err != nil {
				return outcome, true, false, err
			}
			worker.markPairValidated(
				current.semantic.Digest, pair.Identity.Digest, publication,
			)
			return outcome, true, false, nil
		}
	}
	if err = worker.ensureJobSuccessor(durableCtx, job, false); err != nil {
		return outcome, false, false, fmt.Errorf(
			"enqueue terminal caller recovery: %w", err,
		)
	}
	outcome = store.CallerLeafOutcome{
		Generation: current.stored, Pair: storePair(pair),
		Disposition: store.CallerLeafTerminalGenerationRefusal,
		Refusal:     refusal,
	}
	if err = worker.store.RecordCallerLeafOutcome(durableCtx, job, outcome); err != nil {
		return outcome, true, false, err
	}
	return outcome, true, false, nil
}

func callerRefusal(
	cause error,
	stage pipelinerefusal.Stage,
) *pipelinerefusal.Receipt {
	closed := pipelinerefusal.At(cause, stage, pipelinerefusal.GenerationCaller)
	receipt, present := pipelinerefusal.From(closed)
	if !present {
		return nil
	}
	return &receipt
}

func callerClassifiedRefusal(
	cause error,
	stage pipelinerefusal.Stage,
	classification pipelinerefusal.Classification,
) *pipelinerefusal.Receipt {
	closed := pipelinerefusal.Classified(
		cause, stage, pipelinerefusal.GenerationCaller,
		classification, pipelinerefusal.DimensionUnknown,
	)
	receipt, present := pipelinerefusal.From(closed)
	if !present {
		return nil
	}
	return &receipt
}

func (worker *Worker) currentAuthority(
	ctx context.Context,
	repository string,
) (*authority, error) {
	current, err := currentAuthority(ctx, worker.store, worker.registry, repository)
	if err != nil || current == nil {
		return current, err
	}
	partitioned, ok := any(worker.store).(store.PartitionedEvidenceStore)
	if !ok {
		return current, nil
	}
	observation, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, filepath.Join(worker.dataDir, "observations"), repository,
	)
	if err != nil {
		return nil, errors.Join(ErrPartitionedCallerUnavailable, err)
	}
	adapters := worker.registry.Adapters()
	required := make([]downstreamauthority.DomainIdentity, len(adapters))
	domains := make([]candidate.DownstreamDomainAuthority, 0, len(adapters))
	for index, adapter := range adapters {
		required[index] = downstreamauthority.DomainIdentity{Domain: adapter.Domain, Version: adapter.Version}
		domain, domainErr := extractionpublication.CurrentDomainAuthority(
			ctx, partitioned, repository, adapter.Domain,
		)
		if errors.Is(domainErr, store.ErrNotFound) {
			continue
		}
		if domainErr != nil || domain.Version != adapter.Version {
			return nil, errors.Join(ErrPartitionedCallerUnavailable, domainErr)
		}
		domains = append(domains, domain)
	}
	upstream, err := downstreamauthority.BuildRequired(observation, required, domains)
	if err != nil || downstreamauthority.RequireUsable(upstream) != nil {
		return nil, errors.Join(ErrPartitionedCallerUnavailable, err)
	}
	semantic := current.semantic
	semantic.Upstream, err = json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	semantic.UpstreamDigest = upstream.Digest
	semantic, err = callerleaf.NewGenerationIdentity(semantic)
	if err != nil {
		return nil, err
	}
	current.semantic = semantic
	current.stored = storeGeneration(semantic, *current.candidate, *current.resolver)
	if current.stored.Digest != semantic.Digest ||
		store.ComputeCallerGenerationDigest(current.stored) != semantic.Digest {
		return nil, errors.New("partitioned caller identity differs across artifact and store writers")
	}
	return current, nil
}

// authorityStore is the read-only subset needed to reconstruct the exact
// caller generation selected by current repository, candidate, and resolver
// authority. Workers and product readers share this function so neither can
// accidentally drift from the other's generation fence.
type authorityStore interface {
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
}

func currentAuthority(
	ctx context.Context,
	state authorityStore,
	registry *Registry,
	repository string,
) (*authority, error) {
	if state == nil || registry == nil {
		return nil, errors.New("caller generation authority is not configured")
	}
	repo, err := state.GetRepo(ctx, repository)
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
	candidatePointer, err := state.GetCandidateManifestPublication(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	resolverPointer, err := state.GetResolverCatalogPublication(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	resolverCurrent, err := state.ResolverCatalogPublicationCurrent(
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
	}, registry)
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
			Domains:      registry.CandidateDomains(),
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
		UpstreamDigest:           semantic.UpstreamDigest,
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

func storePairIdentity(pair callerleaf.PairIdentity) store.CallerLeafPair {
	leaf := pair.Leaf
	return store.CallerLeafPair{
		Domain: pair.Domain, ExtractorVersion: pair.ExtractorVersion,
		LeafAdapterVersion: pair.LeafAdapterVersion,
		LeafOrdinal:        leaf.Ordinal, LeafPrefix: leaf.Prefix,
		LeafPrefixBits: leaf.PrefixBits, CandidateMemberName: leaf.Name,
		CandidateRecordCount:   leaf.RecordCount,
		CandidateDeclaredBytes: leaf.DeclaredBytes,
		CandidateContentBytes:  leaf.ContentBytes,
		CandidateContentDigest: leaf.ContentDigest,
		PairDigest:             pair.Digest,
	}
}

func storePublication(
	generation store.CallerGenerationIdentity,
	manifest callerpublication.Manifest,
) store.CallerGenerationPublication {
	pairs := make([]store.CallerGenerationPairPublication, len(manifest.Pairs))
	for index, pair := range manifest.Pairs {
		receipt := storeReceipt(pair.Receipt)
		pairs[index] = store.CallerGenerationPairPublication{
			Pair: storePairIdentity(pair.Pair), Receipt: *receipt,
			RecordCount: pair.Receipt.RecordCount,
		}
	}
	return store.CallerGenerationPublication{
		Generation: generation, Pairs: pairs,
		PairSetDigest:   manifest.PairSetDigest,
		PairCount:       manifest.Aggregate.PairCount,
		ArtifactCount:   manifest.Aggregate.ArtifactCount,
		ResultCount:     manifest.Aggregate.ResultCount,
		AbstentionCount: manifest.Aggregate.AbstentionCount,
		CanonicalBytes:  manifest.Aggregate.CanonicalBytes,
		StagingBytes:    manifest.Aggregate.StagingBytes,
		PeakOpenFiles:   manifest.Aggregate.PeakOpenFiles,
		ManifestDigest:  manifest.Digest,
		ManifestPath:    manifest.State().Manifest,
	}
}

func publicationState(
	current *authority,
	pointer store.CallerGenerationPublication,
) (callerpublication.State, error) {
	return publicationStateFromSummary(current, callerPublicationSummary(pointer))
}

func publicationStateFromSummary(
	current *authority,
	pointer store.CallerGenerationPublicationSummary,
) (callerpublication.State, error) {
	if current == nil || pointer.Generation != current.stored {
		return callerpublication.State{}, errors.New(
			"caller publication points at another generation",
		)
	}
	state := callerpublication.State{
		Schema: callerpublication.StateSchema, Generation: current.semantic,
		PairSetDigest: pointer.PairSetDigest,
		Aggregate: callerleaf.AggregateReceipt{
			PairCount: pointer.PairCount, ArtifactCount: pointer.ArtifactCount,
			ResultCount:     pointer.ResultCount,
			AbstentionCount: pointer.AbstentionCount,
			CanonicalBytes:  pointer.CanonicalBytes,
			StagingBytes:    pointer.StagingBytes,
			PeakOpenFiles:   pointer.PeakOpenFiles,
		},
		ManifestDigest: pointer.ManifestDigest, Manifest: pointer.ManifestPath,
	}
	if err := callerpublication.ValidateState(state); err != nil {
		return callerpublication.State{}, err
	}
	return state, nil
}

func publicationMatchesSummary(
	current *authority,
	publication *callerpublication.Publication,
	pointer store.CallerGenerationPublicationSummary,
) bool {
	if publication == nil || current == nil {
		return false
	}
	// State is the scalar projection already captured by the cold-opened
	// publication. Avoid Manifest here: cloning it and converting its pair
	// receipts would allocate two complete P-element slices on every warm job.
	state := publication.State()
	aggregate := state.Aggregate
	return reflect.DeepEqual(state.Generation, current.semantic) &&
		current.stored == pointer.Generation &&
		state.PairSetDigest == pointer.PairSetDigest &&
		aggregate.PairCount == pointer.PairCount &&
		aggregate.ArtifactCount == pointer.ArtifactCount &&
		aggregate.ResultCount == pointer.ResultCount &&
		aggregate.AbstentionCount == pointer.AbstentionCount &&
		aggregate.CanonicalBytes == pointer.CanonicalBytes &&
		aggregate.StagingBytes == pointer.StagingBytes &&
		aggregate.PeakOpenFiles == pointer.PeakOpenFiles &&
		state.ManifestDigest == pointer.ManifestDigest &&
		state.Manifest == pointer.ManifestPath
}

func deterministicPublicationFailure(err error) bool {
	return errors.Is(err, callerpublication.ErrInvalidManifest) ||
		errors.Is(err, callerleaf.ErrInvalidArtifact) ||
		errors.Is(err, store.ErrInvalidCallerGenerationPublication) ||
		errors.Is(err, os.ErrNotExist)
}

func (worker *Worker) completePublicationCurrent(
	ctx context.Context,
	current *authority,
) (bool, error) {
	pointer, err := worker.store.GetCallerGenerationPublicationSummary(
		ctx, current.semantic.Repository,
	)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	storeCurrent, err := worker.store.
		CallerGenerationPublicationSummaryAuthorityCurrent(ctx, *pointer)
	if err != nil || !storeCurrent {
		return false, err
	}
	state, err := publicationStateFromSummary(current, *pointer)
	if err != nil {
		return false, fmt.Errorf("caller publication state: %w", err)
	}
	lease, err := worker.publications.Acquire(ctx, state)
	var publication *callerpublication.Publication
	if err == nil {
		defer func() { _ = lease.Release() }()
		publication = lease.Publication()
	} else if errors.Is(err, callerpublication.ErrRegistryConflict) {
		publication, err = callerpublication.Open(ctx, worker.root, state)
		marked := false
		if errors.Is(err, callerpublication.ErrPublishing) {
			publication, err = callerpublication.OpenPublishing(
				ctx, worker.root, state,
			)
			marked = err == nil
		}
		if err == nil {
			storeCurrent, err = worker.store.CallerGenerationPublicationSummaryCurrent(
				ctx, *pointer,
			)
			if err == nil && !storeCurrent {
				return false, nil
			}
			if err == nil && marked {
				err = callerpublication.ClearPublishing(worker.root, state)
			}
			if err == nil {
				err = worker.publications.Observe(ctx, publication)
			}
		}
	} else if errors.Is(err, callerpublication.ErrPublishing) {
		publication, err = callerpublication.OpenPublishing(ctx, worker.root, state)
		if err == nil {
			storeCurrent, err = worker.store.CallerGenerationPublicationSummaryCurrent(
				ctx, *pointer,
			)
			if err == nil && !storeCurrent {
				return false, nil
			}
			if err == nil {
				err = callerpublication.ClearPublishing(worker.root, state)
			}
			if err == nil {
				err = worker.publications.Observe(ctx, publication)
			}
		}
	}
	if err != nil {
		return false, fmt.Errorf("open caller publication: %w", err)
	}
	if !publicationMatchesSummary(current, publication, *pointer) {
		return false, fmt.Errorf(
			"%w: filesystem manifest differs from the store pointer",
			callerpublication.ErrInvalidManifest,
		)
	}
	storeCurrent, err = worker.store.CallerGenerationPublicationSummaryCurrent(ctx, *pointer)
	if err != nil || !storeCurrent || !publication.Current() {
		return false, err
	}
	worker.markPublication(current.semantic.Digest, publication)
	return true, nil
}

func (worker *Worker) recoverPublication(
	ctx context.Context,
	job store.Job,
	repository, generation string,
) error {
	if err := worker.ensureJobSuccessor(ctx, job, true); err != nil {
		return err
	}
	if err := worker.store.ClearCallerGenerationPublication(ctx, repository); err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("clear invalid caller publication: %w", err),
		)
	}
	if err := worker.publications.Retire(ctx, repository); err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("retire invalid caller publication: %w", err),
		)
	}
	worker.forgetValidation(generation)
	return nil
}

func (worker *Worker) handlePublicationFailure(
	ctx context.Context,
	job store.Job,
	current *authority,
) error {
	marked, err := callerpublication.Publishing(
		worker.root, current.semantic.Repository,
	)
	if err != nil {
		return err
	}
	if !marked {
		return worker.recoverPublication(
			ctx, job, current.semantic.Repository, current.semantic.Digest,
		)
	}
	_, err = callerpublication.OpenMarked(
		ctx, worker.root, current.semantic.Repository,
	)
	if err == nil {
		// A valid different marker is recoverable only through the exact active
		// job lease after its expected pair set is reconstructed under the mirror
		// lock. Leave every byte intact for the forced successor.
		if queueErr := worker.ensureJobSuccessor(ctx, job, true); queueErr != nil {
			return queueErr
		}
		worker.forgetValidation(current.semantic.Digest)
		return nil
	}
	if errors.Is(err, callerpublication.ErrPublicationIO) {
		return err
	}
	pointer, pointerErr := worker.store.GetCallerGenerationPublication(
		ctx, current.semantic.Repository,
	)
	var keep *callerpublication.State
	if pointerErr == nil {
		state, stateErr := publicationState(current, *pointer)
		if stateErr == nil {
			keep = &state
		}
	} else if !errors.Is(pointerErr, store.ErrNotFound) &&
		!errors.Is(pointerErr, store.ErrInvalidCallerGenerationPublication) {
		return pointerErr
	}
	if queueErr := worker.ensureJobSuccessor(ctx, job, true); queueErr != nil {
		return queueErr
	}
	if abandonErr := callerpublication.AbandonPublishing(
		ctx, worker.root, current.semantic.Repository, keep,
	); abandonErr != nil {
		return store.WithSuccessorRetry(abandonErr)
	}
	worker.forgetValidation(current.semantic.Digest)
	if keep == nil {
		return worker.recoverPublication(
			ctx, job, current.semantic.Repository, current.semantic.Digest,
		)
	}
	return nil
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
		if worker.validatedPair(current.semantic.Digest, pair.Identity.Digest) != nil {
			continue
		}
		receipt := artifactReceipt(*outcome.Receipt)
		receiptValid := callerleaf.ValidateReceipt(
			current.semantic, pair.Identity, receipt,
		) == nil
		publication, err := callerleaf.Open(
			ctx, worker.root, current.semantic, pair.Identity, receipt, nil,
		)
		if err == nil {
			worker.markPairValidated(
				current.semantic.Digest, pair.Identity.Digest, publication,
			)
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
	if err := worker.retireUnavailablePublication(
		ctx, generation.Repository,
	); err != nil {
		return store.WithSuccessorRetry(
			fmt.Errorf("retire invalid caller publication: %w", err),
		)
	}
	worker.forgetValidation(generation.Digest)
	return nil
}

// retireUnavailablePublication never lets a failed proposal remove the files
// of a different store-current generation. Upstream transitions normally clear
// that pointer atomically; this final check also protects repair and test seams
// where an older publication remains authoritative.
func (worker *Worker) retireUnavailablePublication(
	ctx context.Context,
	repository string,
) error {
	pointer, err := worker.store.GetCallerGenerationPublication(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return worker.publications.Retire(ctx, repository)
	}
	if err != nil {
		return err
	}
	current, err := worker.store.CallerGenerationPublicationCurrent(ctx, *pointer)
	if err != nil {
		return err
	}
	if current {
		return nil
	}
	return worker.publications.Retire(ctx, repository)
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
			AuthoritySchema:  declaration.AuthoritySchema,
			PlanDigest:       declaration.PlanDigest,
			RootDigest:       declaration.RootDigest,
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

func (worker *Worker) validatedPair(
	generation, pair string,
) *callerleaf.Publication {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		return nil
	}
	publication := worker.validations.pairs[pair]
	if publication == nil || !publication.Current() {
		return nil
	}
	return publication
}

func (worker *Worker) markPairValidated(
	generation, pair string,
	publication *callerleaf.Publication,
) {
	if publication == nil {
		return
	}
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		worker.validations = validationCache{
			key: generation, pairs: make(map[string]*callerleaf.Publication),
		}
	}
	worker.validations.pairs[pair] = publication
}

func (worker *Worker) validatedPairs(
	generation string,
) map[string]*callerleaf.Publication {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		return nil
	}
	result := make(map[string]*callerleaf.Publication, len(worker.validations.pairs))
	for pair, publication := range worker.validations.pairs {
		if publication != nil && publication.Current() {
			result[pair] = publication
		}
	}
	return result
}

func (worker *Worker) generationSettled(generation string) bool {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	return worker.validations.key == generation && worker.validations.settled
}

func (worker *Worker) markGenerationSettled(generation string) {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		worker.validations = validationCache{
			key: generation, pairs: make(map[string]*callerleaf.Publication),
		}
	}
	worker.validations.settled = true
	worker.validations.publication = nil
}

func (worker *Worker) cachedGenerationPublication(
	generation string,
) *callerpublication.Publication {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation || worker.validations.publication == nil ||
		!worker.validations.publication.Current() {
		return nil
	}
	return worker.validations.publication
}

func (worker *Worker) markPublication(
	generation string,
	publication *callerpublication.Publication,
) {
	if publication == nil {
		return
	}
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key != generation {
		worker.validations = validationCache{
			key: generation, pairs: make(map[string]*callerleaf.Publication),
		}
	}
	worker.validations.settled = true
	worker.validations.publication = publication
}

func (worker *Worker) forgetValidation(generation string) {
	worker.cacheMu.Lock()
	defer worker.cacheMu.Unlock()
	if worker.validations.key == generation {
		worker.validations = validationCache{}
	}
}

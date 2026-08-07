// Package extract runs pure evidence extractors over immutable indexed
// repository snapshots. Extractors receive only the capability-neutral SDK;
// this package owns Git access, provenance, bounded staging, and the guarded
// atomic publication transition.
package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	candidatepkg "github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	evidenceChunkSize   = 256
	evidenceChunkSchema = "t20-fact-chunk-v1"
	abortTimeout        = 5 * time.Second
	maxFactTextBytes    = 64 << 10
	// The T20.1 target has 10,010 source-granular call facts / 20,020 rows.
	// T20.3's frozen admission target is 25,000 rows, so the worker's
	// independent fact ceiling is half that row budget: one occurrence and one
	// assertion per emitted fact.
	maxFactsPerRun    = 12_500
	maxCorpusRunBytes = int64(512 << 20)
	lineIndexBlock    = 4 << 10
)

// Capability-neutral API aliases keep the registry surface compact without
// exposing this capable harness package to extractor implementations.
type Corpus = sdk.Corpus
type Extractor = sdk.Extractor

// RepoGetter is the slice of store.Store the worker needs.
type RepoGetter interface {
	GetRepo(ctx context.Context, name string) (*store.Repo, error)
}

// Worker drives extraction jobs. NewCorpus must fence the same bare mirror
// path as fetch/index/delete before it constructs a corpus. An injected Now
// function must be safe for concurrent calls from extractor-owned read paths.
type Worker struct {
	Repos            RepoGetter
	Evidence         store.EvidenceStore
	NewCorpus        CorpusFactory
	Manifests        CandidateManifestProvider
	Extractors       []Extractor
	Now              func() time.Time
	OperationReports ExtractionOperationSink
	Diagnostics      bool
	ExtractorDetails bool

	// schedulingLimits is a package-test seam that may only tighten the
	// production caps. Runtime configuration cannot expand these bounds.
	schedulingLimits *domainSchedulingLimits
}

var errStaleRun = errors.New("stale extraction run")

// Handle adapts the worker to store.Runner: the job target is the repo name.
func (w *Worker) Handle(
	ctx context.Context,
	job store.Job,
) (result error) {
	now := time.Now
	if w != nil && w.Now != nil {
		now = w.Now
	}
	operation := newOperationRecorder(job, now)
	defer func() {
		operation.completeRemaining(operationReasonForError(result))
		if w != nil {
			w.emitOperationReport(operation.snapshot())
		}
	}()

	extractors, err := w.validate()
	if err != nil {
		return store.WithClass(store.ClassExtract, fmt.Errorf("extract %s: %w", job.Target, err))
	}
	limits, err := effectiveDomainSchedulingLimits(w.schedulingLimits)
	if err != nil {
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: scheduler: %w", job.Target, err))
	}
	operation.registerDomains(extractors, limits)
	policyDigest := ""
	if provider, ok := w.Manifests.(interface{ PolicyDigest() string }); ok {
		policyDigest = provider.PolicyDigest()
		operation.bindPolicy(policyDigest)
	}

	// Production candidate admission has a pointer-only phase. A previously
	// published extraction run binds the exact candidate manifest digest in
	// InventoryPolicy, so an unchanged database pointer proves this job is a
	// no-op without opening publication bytes or taking the mirror lock.
	//
	// A concurrent index/delete after this read owns a queued successor. This
	// path consumes no mirror or candidate bytes, so returning against the
	// prior complete generation cannot publish stale work.
	pointerStarted := now()
	preflight := extractionPreflightDiagnostic{Repository: job.Target}
	current, pointerIdentity, currentErr := w.candidateManifestCurrent(
		ctx, job.Target, extractors, operation, job.Force, &preflight,
	)
	operation.addPointerWork(nonnegativeDuration(pointerStarted, now()))
	preflight.StrictOpenRequired = currentErr == nil && !current
	if preflight.PointerResult == "" {
		preflight.PointerResult = candidatePointerDiagnosticResult(currentErr)
	}
	if w.Diagnostics {
		w.logDiagnostic("extraction preflight", preflight)
	}
	if currentErr != nil {
		operation.completeRemaining(operationReason(currentErr))
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: candidate preflight: %w", job.Target, currentErr))
	}
	if current {
		operation.completeRemaining(OperationReasonAlreadyCurrent)
		return nil
	}

	// Lock before loading the repository row. Fetch, indexing, mirror deletion,
	// and corpus reads now share one critical section, so a worker cannot pin a
	// row and then race removal or mutation of the corresponding object store.
	// The wait uses the job context, not the extraction budget: queueing behind
	// a long index or fetch of the same mirror is not extraction work, and the
	// runner's lease heartbeat keeps the claim alive while blocked here.
	lockStarted := now()
	unlock, err := w.NewCorpus.Lock(ctx, job.Target)
	operation.addMirrorLockWait(nonnegativeDuration(lockStarted, now()))
	if err != nil {
		operation.completeRemaining(operationReason(err))
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: lock corpus: %w", job.Target, err))
	}
	if unlock == nil {
		operation.completeRemaining(OperationReasonFailed)
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: corpus lock returned no release function", job.Target))
	}
	mirrorReleased := false
	releaseMirror := func() {
		if mirrorReleased {
			return
		}
		unlock()
		mirrorReleased = true
	}
	defer releaseMirror()

	// Both deadlines are fixed once the mirror is fenced. Per-domain contexts
	// are children clipped to these absolute deadlines; neither a later domain
	// nor a successor attempt inside this job can extend them.
	budgetStarted := time.Now()
	aggregateDeadline := budgetStarted.Add(limits.AggregateTimeout)
	mirrorDeadline := budgetStarted.Add(limits.MirrorTimeout)
	aggregateCtx, cancelAggregate := context.WithDeadline(ctx, aggregateDeadline)
	defer cancelAggregate()
	mirrorCtx, cancelMirror := context.WithDeadline(aggregateCtx, mirrorDeadline)
	defer cancelMirror()
	domainWorkDeadline := mirrorDeadline.Add(
		-limits.AbortTimeout - limits.OutcomeTimeout,
	)

	repo, err := w.Repos.GetRepo(mirrorCtx, job.Target)
	if errors.Is(err, store.ErrNotFound) {
		operation.completeRemaining(OperationReasonNotReady)
		return nil
	}
	if err != nil {
		operation.completeRemaining(operationReason(err))
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: load repo: %w", job.Target, err))
	}
	if repo.Deleting || repo.IndexedCommitHash == "" {
		// Not ready: deletion owns the mirror, or the next successful index will
		// chain a fresh extraction job.
		operation.bindRepository(repo)
		operation.completeRemaining(OperationReasonNotReady)
		return nil
	}
	operation.bindRepository(repo)
	if repo.Name != job.Target {
		operation.completeRemaining(OperationReasonFailed)
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: stored repository name is %q", job.Target, repo.Name))
	}
	if err := checkCommit(repo.IndexedCommitHash); err != nil {
		operation.completeRemaining(OperationReasonFailed)
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: indexed revision: %w", repo.Name, err))
	}

	commit := repo.IndexedCommitHash
	corpus := w.NewCorpus.New(repo.Name, commit)
	if corpus == nil || corpus.RepoName() != repo.Name || corpus.Commit() != commit {
		operation.completeRemaining(OperationReasonFailed)
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: corpus factory returned mismatched provenance", repo.Name))
	}

	inventoryPolicy := corpusInventoryPolicy
	boundaries := emptyGitlinkInventory()
	var candidateManifest CandidateManifest
	if w.Manifests != nil {
		openStarted := now()
		candidateManifest, err = w.Manifests.OpenCandidateManifest(
			mirrorCtx,
			manifestRequest(
				repo.Name, commit, repo.IndexedAnalysisUnit, extractors),
		)
		operation.addStrictOpen(nonnegativeDuration(openStarted, now()))
		if err != nil {
			err = candidateStrictOpenFailure(err)
			return w.recordManifestOpenOutcomes(
				mirrorCtx, repo, extractors, policyDigest, pointerIdentity,
				operation, err,
			)
		}
		inventoryPolicy, boundaries, err = validateCandidateManifest(candidateManifest)
		if err != nil {
			err = candidateStrictValidationFailure(err)
			return w.recordManifestOpenOutcomes(
				mirrorCtx, repo, extractors, policyDigest, pointerIdentity,
				operation, err,
			)
		}
		operation.bindManifest(candidateManifest.Identity())
		if control, ok := candidateManifest.(CandidateManifestControl); ok {
			operation.bindControlRevision(control.CandidateControlRevision())
		}
		if _, production := w.Manifests.(CandidateManifestGenerationProvider); production {
			control, ok := candidateManifest.(CandidateManifestControl)
			if !ok || control.CandidateControlRevision() == 0 {
				err = candidateStrictValidationFailure(errors.New(
					"strict candidate manifest has no durable control revision"))
				return w.recordManifestOpenOutcomes(
					mirrorCtx, repo, extractors, policyDigest, pointerIdentity,
					operation, err,
				)
			}
			if pointerIdentity.ControlRevision != 0 &&
				(control.CandidateControlRevision() !=
					pointerIdentity.ControlRevision ||
					candidateManifest.Identity() !=
						pointerIdentity.ManifestDigest) {
				operation.completeRemaining(OperationReasonStale)
				return nil
			}
		}
		if w.Diagnostics {
			strictOpen := extractionStrictOpenDiagnostic{
				Repository: repo.Name, StrictOpenMS: operation.report.StrictOpenMS,
			}
			if control, ok := candidateManifest.(CandidateManifestControl); ok {
				strictOpen.ControlRevision = control.CandidateControlRevision()
			}
			if provider, ok := candidateManifest.(CandidateManifestDiagnosticsProvider); ok {
				strictOpen.Manifest = provider.CandidateManifestDiagnostics()
			}
			w.logDiagnostic("extraction strict open", strictOpen)
		}
	}

	// Resolve the durable state of every configured domain before starting any
	// work. Never-attempted generations sort ahead of retryable generations;
	// retryables use their durable attempt time, so restart cannot reset
	// fairness or let one slow domain repeatedly jump an untried peer.
	var domainErrs []error
	var terminalErrs []error
	continuationProgress := false
	scheduled := make([]scheduledDomain, 0, len(extractors))
	var nonRunnable []schedulerDomainDiagnostic
	if w.Diagnostics {
		nonRunnable = make([]schedulerDomainDiagnostic, 0, len(extractors))
	}
	for index, ex := range extractors {
		domainOperation := operation.domain(index)
		if err := mirrorCtx.Err(); err != nil {
			domainOperation.complete(operationReason(err))
			domainErrs = append(domainErrs, err)
			break
		}
		scope := extractionScope(repo, ex.domain)
		generation, typedApplicable, typedPresent, generationErr :=
			extractionGeneration(
				candidateManifest, ex, inventoryPolicy, boundaries,
				policyDigest,
			)
		if generationErr != nil {
			generationErr = candidateStrictValidationFailure(generationErr)
			domainOperation.complete(operationReason(generationErr))
			disposition, controlFailure := classifyDomainOutcome(generationErr)
			if w.Diagnostics {
				nonRunnable = append(nonRunnable, schedulerDomainDiagnostic{
					Domain: ex.domain, State: string(disposition),
				})
			}
			if recordErr := w.recordDomainOutcome(
				mirrorCtx, scope, generation, disposition, controlFailure, "",
				domainOperation, generationErr, "",
			); recordErr != nil {
				if errors.Is(recordErr, errStaleRun) {
					operation.completeRemaining(OperationReasonStale)
					return nil
				}
				domainErrs = append(domainErrs, recordErr)
			} else if disposition ==
				store.DomainOutcomeTerminalGenerationRefusal {
				continuationProgress = true
				terminalErrs = append(terminalErrs, generationErr)
			} else if !disposition.Settled() {
				domainErrs = append(domainErrs, generationErr)
			} else {
				continuationProgress = true
			}
			continue
		}
		latest, latestErr := w.Evidence.LatestExtractionDomainOutcome(
			mirrorCtx, scope,
		)
		var exactLatest *store.ExtractionDomainOutcome
		if latestErr == nil {
			if latest == nil {
				return store.WithClass(store.ClassExtract,
					fmt.Errorf("extract %s: %s: latest outcome returned nil",
						repo.Name, ex.domain))
			}
			if store.SameExtractionGeneration(
				latest.Generation, generation,
			) && latest.Disposition.Settled() &&
				(!job.Force ||
					latest.Disposition != store.DomainOutcomePublished) {
				domainOperation.complete(OperationReasonAlreadyCurrent)
				if w.Diagnostics {
					nonRunnable = append(nonRunnable, schedulerDomainDiagnostic{
						Domain: ex.domain, State: OperationReasonAlreadyCurrent,
						TypedInputPresent: diagnosticBool(
							typedApplicable, typedPresent,
						),
					})
				}
				w.logOutcome(
					scope, latest.Generation, latest.Disposition,
					OperationReasonAlreadyCurrent, latest.Disposition,
				)
				continue
			}
			if store.SameExtractionGeneration(latest.Generation, generation) {
				exactLatest = latest
			}
		} else if !errors.Is(latestErr, store.ErrNotFound) {
			domainOperation.complete(operationReason(latestErr))
			return store.WithClass(store.ClassExtract,
				fmt.Errorf("extract %s: %s: latest outcome: %w",
					repo.Name, ex.domain, latestErr))
		}
		if scope.UnitDigest != "" && typedApplicable && !typedPresent {
			domainOperation.complete(OperationReasonTypedInputAbsent)
			if w.Diagnostics {
				nonRunnable = append(nonRunnable, schedulerDomainDiagnostic{
					Domain: ex.domain,
					State: string(
						store.DomainOutcomeUnavailablePrerequisite,
					),
					TypedInputPresent: diagnosticBool(true, false),
				})
			}
			if err := w.recordDomainOutcome(
				mirrorCtx, scope, generation,
				store.DomainOutcomeUnavailablePrerequisite, false, "",
				domainOperation, nil, outcomeDisposition(exactLatest),
			); err != nil {
				if errors.Is(err, errStaleRun) {
					operation.completeRemaining(OperationReasonStale)
					return nil
				}
				domainErrs = append(domainErrs, err)
			} else {
				continuationProgress = true
			}
			continue
		}

		task := scheduledDomain{
			index: index, extractor: ex, scope: scope, generation: generation,
			previousDisposition: outcomeDisposition(exactLatest),
			diagnosticState:     "never_attempted",
			typedApplicable:     typedApplicable,
			typedPresent:        typedPresent,
		}
		if exactLatest != nil &&
			(exactLatest.Disposition == store.DomainOutcomeRetryableFailure ||
				job.Force &&
					exactLatest.Disposition == store.DomainOutcomePublished) {
			task.previousRunID = exactLatest.RunID
			attempt, attemptErr := w.Evidence.LatestExtractionAttempt(
				mirrorCtx, scope,
			)
			if attemptErr != nil &&
				(!errors.Is(attemptErr, store.ErrNotFound) ||
					exactLatest.RunID != "") {
				domainOperation.complete(operationReason(attemptErr))
				return store.WithClass(store.ClassExtract,
					fmt.Errorf("extract %s: %s: latest attempt: %w",
						repo.Name, ex.domain, attemptErr))
			}
			if attemptErr == nil && attempt == nil {
				domainOperation.complete(OperationReasonFailed)
				return store.WithClass(store.ClassExtract,
					fmt.Errorf("extract %s: %s: latest attempt returned nil",
						repo.Name, ex.domain))
			}
			attemptIsCurrent := attemptErr == nil && attempt != nil &&
				(exactLatest.RunID != "" ||
					!exactLatest.RecordedAt.IsZero() &&
						attempt.StartedAt.After(exactLatest.RecordedAt))
			if attemptIsCurrent {
				if attempt == nil || attempt.RunID == "" ||
					attempt.Repo != scope.Repository ||
					attempt.Commit != scope.Commit ||
					attempt.UnitDigest != scope.UnitDigest ||
					attempt.Domain != scope.Domain ||
					attempt.Extractor != ex.version ||
					attempt.StartedAt.IsZero() {
					domainOperation.complete(OperationReasonFailed)
					return store.WithClass(store.ClassExtract,
						fmt.Errorf("extract %s: %s: latest attempt does not match scope",
							repo.Name, ex.domain))
				}
				task.previousRunID = attempt.RunID
				task.retryable = true
				task.attemptedAt = attempt.StartedAt
				task.diagnosticState = "retryable"
				if exactLatest.Disposition == store.DomainOutcomePublished {
					task.diagnosticState = "forced_rerun"
				}
			}
		}
		scheduled = append(scheduled, task)
	}
	scheduleDomains(scheduled)
	scheduleErr := validateScheduledDomains(scheduled, limits)
	w.logSchedule(repo.Name, scheduled, nonRunnable, domainWorkDeadline)

	startedDomains := 0
	stagedRows := 0
	deferred := make([]scheduledDomain, 0)
	yieldAfterDeferrals := false
	if scheduleErr != nil {
		deferred = append(deferred, scheduled...)
		domainErrs = append(domainErrs, scheduleErr)
		if w.Diagnostics {
			w.logDiagnostic("domain scheduler deferral", schedulerDeferralDiagnostic{
				Repository: repo.Name, Trigger: "scheduler_identity",
				Position: 1, Remaining: len(scheduled),
			})
		}
	}
	for position, task := range scheduled {
		if scheduleErr != nil {
			break
		}
		if err := ctx.Err(); err != nil {
			domainErrs = append(domainErrs, err)
			if w.Diagnostics {
				w.logDiagnostic("domain scheduler deferral", schedulerDeferralDiagnostic{
					Repository: repo.Name, Trigger: "canceled",
					Position: position + 1, Remaining: len(scheduled) - position,
				})
			}
			break
		}
		remainingStageRows := limits.MaxStagedRows - stagedRows
		if mirrorCtx.Err() != nil ||
			time.Until(domainWorkDeadline) < limits.MinimumStartBudget ||
			startedDomains >= limits.MaxSerialDomains ||
			remainingStageRows <= 0 {
			trigger := "mirror_deadline"
			switch {
			case ctx.Err() != nil:
				trigger = "canceled"
			case mirrorCtx.Err() == nil && time.Until(domainWorkDeadline) < limits.MinimumStartBudget:
				trigger = "insufficient_time"
			case mirrorCtx.Err() == nil && startedDomains >= limits.MaxSerialDomains:
				trigger = "max_serial_domains"
			case mirrorCtx.Err() == nil && remainingStageRows <= 0:
				trigger = "aggregate_staged_rows"
			}
			if w.Diagnostics {
				w.logDiagnostic("domain scheduler deferral", schedulerDeferralDiagnostic{
					Repository: repo.Name, Trigger: trigger,
					Position: position + 1, Remaining: len(scheduled) - position,
				})
			}
			deferred = append(deferred, scheduled[position:]...)
			domainErrs = append(domainErrs, errExtractionAggregateBudget)
			break
		}

		domainOperation := operation.domain(task.index)
		domainStagedRows := min(
			limits.MaxDomainStagedRows, remainingStageRows,
		)
		domainOperation.setStagedRowLimit(domainStagedRows)
		domainDeadline := earlierDeadline(
			time.Now().Add(limits.DomainTimeout), domainWorkDeadline,
		)
		domainCtx, cancelDomain := context.WithDeadline(
			mirrorCtx, domainDeadline,
		)
		attempt := domainRunAttempt{}
		runErr := w.runOne(
			domainCtx, task.extractor, task.scope, corpus,
			candidateManifest, inventoryPolicy, boundaries,
			task.generation, domainOperation, domainStagedRows,
			limits.AbortTimeout, &attempt, task.previousDisposition,
		)
		cancelDomain()
		startedDomains++
		stagedRows += attempt.stagedRows
		if runErr == nil {
			continuationProgress = true
		} else {
			if errors.Is(runErr, errStaleRun) {
				// A guarded publish proved the repository was deleted or advanced.
				// The deleting workflow or successor index event owns the next step;
				// retrying this obsolete job would only create queue churn.
				operation.completeRemaining(OperationReasonStale)
				return nil
			}
			switch {
			case errors.Is(runErr, errExtractionDomainBudget):
				domainOperation.complete(OperationReasonDomainBudget)
			case ctx.Err() != nil &&
				(errors.Is(runErr, context.Canceled) ||
					errors.Is(runErr, context.DeadlineExceeded)):
				domainOperation.complete(operationReason(ctx.Err()))
			case errors.Is(runErr, context.DeadlineExceeded):
				if domainDeadline.Equal(domainWorkDeadline) {
					domainOperation.complete(OperationReasonAggregateBudget)
				} else {
					domainOperation.complete(OperationReasonDomainBudget)
				}
			}
			disposition, controlFailure := classifyDomainOutcome(runErr)
			if recordErr := w.recordDomainOutcome(
				mirrorCtx, task.scope, task.generation,
				disposition, controlFailure, attempt.runID,
				domainOperation, runErr, task.previousDisposition,
			); recordErr != nil {
				if errors.Is(recordErr, errStaleRun) {
					operation.completeRemaining(OperationReasonStale)
					return nil
				}
				domainErrs = append(
					domainErrs,
					errors.Join(runErr, recordErr),
				)
			} else if disposition ==
				store.DomainOutcomeTerminalGenerationRefusal {
				continuationProgress = true
				terminalErrs = append(terminalErrs, runErr)
			} else if !disposition.Settled() {
				if attempt.runID != "" {
					continuationProgress = true
				}
				domainErrs = append(domainErrs, runErr)
			} else {
				continuationProgress = true
			}
		}
	}

	// The aggregate budget intentionally outlives the mirror budget. Release
	// the mirror first, then spend only that fixed reserve persisting retryable
	// deferrals for work that could not start.
	releaseMirror()
	cancelMirror()
	if scheduleErr == nil && continuationProgress {
		for _, task := range deferred {
			if !task.retryable {
				yieldAfterDeferrals = true
				break
			}
		}
	}
	for _, task := range deferred {
		domainOperation := operation.domain(task.index)
		domainOperation.complete(OperationReasonAggregateBudget)
		if recordErr := w.recordDomainOutcome(
			aggregateCtx, task.scope, task.generation,
			store.DomainOutcomeRetryableFailure, false, task.previousRunID,
			domainOperation, nil, task.previousDisposition,
		); recordErr != nil {
			if errors.Is(recordErr, errStaleRun) {
				operation.completeRemaining(OperationReasonStale)
				return nil
			}
			yieldAfterDeferrals = false
			domainErrs = append(domainErrs, recordErr)
		}
	}
	if len(domainErrs) > 0 {
		joined := errors.Join(domainErrs...)
		operation.completeRemaining(operationReasonForError(joined))
		result := error(fmt.Errorf("extract %s: %w", repo.Name, joined))
		if len(deferred) > 0 && yieldAfterDeferrals {
			result = store.WithYield(result)
		}
		return closePipelineRefusal(store.WithClass(store.ClassExtract, result))
	}
	if len(terminalErrs) > 0 {
		return closePipelineRefusal(store.WithClass(store.ClassExtract, store.WithTerminal(
			fmt.Errorf("extract %s: %w", repo.Name, errors.Join(terminalErrs...)),
		)))
	}
	return nil
}

func (w *Worker) candidateManifestCurrent(
	ctx context.Context,
	target string,
	extractors []registeredExtractor,
	operation *operationRecorder,
	force bool,
	preflight *extractionPreflightDiagnostic,
) (bool, CandidateManifestPointerIdentity, error) {
	generationProvider, generationOK := w.Manifests.(CandidateManifestGenerationProvider)
	legacyProvider, legacyOK := w.Manifests.(CandidateManifestIdentityProvider)
	if !generationOK && !legacyOK {
		return false, CandidateManifestPointerIdentity{}, nil
	}
	repo, err := w.Repos.GetRepo(ctx, target)
	if errors.Is(err, store.ErrNotFound) {
		preflight.PointerResult = "missing"
		operation.completeRemaining(OperationReasonNotReady)
		return true, CandidateManifestPointerIdentity{}, nil
	}
	if err != nil {
		return false, CandidateManifestPointerIdentity{},
			fmt.Errorf("load repo: %w", err)
	}
	if repo == nil {
		return false, CandidateManifestPointerIdentity{},
			errors.New("repository store returned nil")
	}
	operation.bindRepository(repo)
	if repo.Deleting || repo.IndexedCommitHash == "" {
		preflight.PointerResult = "missing"
		operation.completeRemaining(OperationReasonNotReady)
		return true, CandidateManifestPointerIdentity{}, nil
	}
	if repo.Name != target {
		return false, CandidateManifestPointerIdentity{},
			fmt.Errorf("stored repository name is %q", repo.Name)
	}
	if err := checkCommit(repo.IndexedCommitHash); err != nil {
		return false, CandidateManifestPointerIdentity{},
			fmt.Errorf("indexed revision: %w", err)
	}
	request := manifestRequest(
		repo.Name, repo.IndexedCommitHash,
		repo.IndexedAnalysisUnit, extractors,
	)
	if !generationOK {
		identity, identityErr := legacyProvider.CandidateManifestIdentity(
			ctx, request,
		)
		if identityErr != nil {
			preflight.PointerResult = candidatePointerDiagnosticResult(identityErr)
			return false, CandidateManifestPointerIdentity{},
				fmt.Errorf("candidate manifest identity: %w", identityErr)
		}
		operation.bindManifest(identity)
		preflight.PointerResult = "current"
		inventoryPolicy, policyErr :=
			candidateManifestInventoryPolicy(identity)
		if policyErr != nil {
			return false, CandidateManifestPointerIdentity{}, policyErr
		}
		if force {
			return false, CandidateManifestPointerIdentity{}, nil
		}
		for _, ex := range extractors {
			last, latestErr := w.Evidence.LatestPublishedRun(
				ctx, extractionScope(repo, ex.domain),
			)
			if errors.Is(latestErr, store.ErrNotFound) {
				return false, CandidateManifestPointerIdentity{}, nil
			}
			if latestErr != nil {
				return false, CandidateManifestPointerIdentity{},
					fmt.Errorf("%s: latest run: %w", ex.domain, latestErr)
			}
			if last == nil {
				return false, CandidateManifestPointerIdentity{},
					fmt.Errorf("%s: latest run returned nil", ex.domain)
			}
			if last.Extractor != ex.version ||
				last.Coverage.InventoryPolicy != inventoryPolicy ||
				last.Coverage.CandidateManifestDigest != identity {
				return false, CandidateManifestPointerIdentity{}, nil
			}
			preflight.SettledDomains++
		}
		preflight.SettledComplete = true
		return true, CandidateManifestPointerIdentity{}, nil
	}
	identity, err := generationProvider.CandidateManifestGeneration(
		ctx,
		request,
	)
	if err != nil {
		preflight.PointerResult = candidatePointerDiagnosticResult(err)
		return false, CandidateManifestPointerIdentity{},
			fmt.Errorf("candidate manifest identity: %w", err)
	}
	if !validSHA256(identity.ManifestDigest) ||
		!validSHA256(identity.PolicyDigest) ||
		identity.ControlRevision == 0 {
		return false, CandidateManifestPointerIdentity{}, errors.New(
			"candidate manifest pointer identity is incomplete")
	}
	preflight.PointerResult = "current"
	preflight.ControlRevision = identity.ControlRevision
	operation.bindControlRevision(identity.ControlRevision)
	operation.bindManifest(identity.ManifestDigest)
	inventoryPolicy, err := candidateManifestInventoryPolicy(
		identity.ManifestDigest,
	)
	if err != nil {
		return false, CandidateManifestPointerIdentity{}, err
	}
	for _, ex := range extractors {
		scope := extractionScope(repo, ex.domain)
		latest, latestErr := w.Evidence.LatestExtractionDomainOutcome(
			ctx, scope,
		)
		if errors.Is(latestErr, store.ErrNotFound) {
			return false, identity, nil
		}
		if latestErr != nil {
			return false, CandidateManifestPointerIdentity{},
				fmt.Errorf("%s: latest outcome: %w", ex.domain, latestErr)
		}
		if latest == nil {
			return false, CandidateManifestPointerIdentity{},
				fmt.Errorf("%s: latest outcome returned nil", ex.domain)
		}
		if !latest.Disposition.Settled() ||
			(force &&
				latest.Disposition == store.DomainOutcomePublished) ||
			latest.Generation.Extractor != ex.version ||
			latest.Generation.InventoryPolicy != inventoryPolicy ||
			latest.Generation.CandidateManifestDigest !=
				identity.ManifestDigest ||
			latest.Generation.CandidatePolicyDigest !=
				identity.PolicyDigest ||
			latest.Generation.CandidateControlRevision !=
				identity.ControlRevision {
			return false, identity, nil
		}
		preflight.SettledDomains++
		w.logOutcome(
			scope, latest.Generation, latest.Disposition,
			OperationReasonAlreadyCurrent, latest.Disposition,
		)
	}
	preflight.SettledComplete = true
	return true, identity, nil
}

func extractionScope(repo *store.Repo, domain string) store.ExtractionScope {
	scope := store.ExtractionScope{Domain: domain}
	if repo == nil {
		return scope
	}
	scope.Repository = repo.Name
	scope.Commit = repo.IndexedCommitHash
	if repo.IndexedAnalysisUnit != nil {
		scope.UnitDigest = repo.IndexedAnalysisUnit.Digest
	}
	return scope
}

func extractionGeneration(
	manifest CandidateManifest,
	ex registeredExtractor,
	inventoryPolicy string,
	boundaries gitlinkInventory,
	policyDigest string,
) (store.ExtractionGenerationIdentity, bool, bool, error) {
	generation := store.ExtractionGenerationIdentity{
		Extractor:       ex.version,
		InventoryPolicy: inventoryPolicy,
	}
	dependencyFields := []string{
		"phebs-extraction-dependency-v1",
		inventoryPolicy,
		boundaries.digest,
	}
	if manifest == nil {
		generation.DependencyDigest =
			extractionDependencyDigest(dependencyFields...)
		generation.Digest =
			store.ComputeExtractionGenerationDigest(generation)
		return generation, false, false, nil
	}
	if !validSHA256(manifest.Identity()) {
		return generation, false, false, fmt.Errorf(
			"%w: candidate manifest identity is invalid",
			candidatepkg.ErrInvalidManifest,
		)
	}
	if !validSHA256(policyDigest) {
		// Compatibility manifests used by isolated tests predate the production
		// shared-policy provider. Derive a stable bounded identity; production
		// always supplies the actual candidate policy digest.
		policyDigest = extractionDependencyDigest(
			"compat-candidate-policy-v1", manifest.Identity(),
		)
	}
	controlRevision := uint64(1)
	if control, ok := manifest.(CandidateManifestControl); ok {
		controlRevision = control.CandidateControlRevision()
	}
	if controlRevision == 0 {
		return generation, false, false, fmt.Errorf(
			"%w: candidate control revision is missing",
			candidatepkg.ErrInvalidManifest,
		)
	}
	generation.CandidateManifestDigest = manifest.Identity()
	generation.CandidatePolicyDigest = policyDigest
	generation.CandidateControlRevision = controlRevision
	generation.DependencyDigest = extractionDependencyDigest(
		"candidate-control-generation-v1",
		manifest.Identity(),
		policyDigest,
		fmt.Sprintf("%d", controlRevision),
	)
	generation.Digest =
		store.ComputeExtractionGenerationDigest(generation)
	scope, err := manifest.DomainScope(ex.domain, ex.version)
	if err != nil {
		return generation, false, false, fmt.Errorf(
			"%w: candidate domain scope: %v",
			candidatepkg.ErrInvalidManifest, err,
		)
	}
	typed, err := manifest.TypedInput(
		ex.domain, ex.version, analysisunit.TypedIndexKindSCIP,
	)
	if err != nil {
		return generation, false, false, fmt.Errorf(
			"%w: candidate typed input: %v",
			candidatepkg.ErrInvalidManifest, err,
		)
	}
	_, typedPresent, err := normalizeManifestTypedInput(typed)
	if err != nil {
		return generation, false, false, fmt.Errorf(
			"%w: candidate typed input: %v",
			candidatepkg.ErrInvalidManifest, err,
		)
	}
	generation.TypedInputKind = typed.Kind
	generation.TypedInputObjectID = typed.ObjectID
	generation.TypedInputPresent = typedPresent
	dependencyFields = append(
		dependencyFields,
		scope.ManifestDigest,
		scope.UnitDigest,
		string(scope.Plane),
		scope.CorpusDigest,
		scope.CandidateDigest,
		typed.Kind,
		typed.ObjectID,
		fmt.Sprintf("%t", typedPresent),
	)
	generation.DependencyDigest =
		extractionDependencyDigest(dependencyFields...)
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	return generation, typed.Kind != "", typedPresent, nil
}

func extractionDependencyDigest(fields ...string) string {
	hash := sha256.New()
	for _, field := range fields {
		_, _ = fmt.Fprintf(hash, "%d:", len(field))
		_, _ = hash.Write([]byte(field))
		_, _ = hash.Write([]byte{';'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func classifyDomainOutcome(
	err error,
) (store.DomainOutcomeDisposition, bool) {
	switch {
	case errors.Is(err, candidatepkg.ErrInvalidManifest):
		return store.DomainOutcomeTerminalGenerationRefusal, true
	case store.IsTerminal(err), isOperationLimitRefusal(err):
		return store.DomainOutcomeTerminalGenerationRefusal, false
	default:
		return store.DomainOutcomeRetryableFailure, false
	}
}

func candidateStrictOpenFailure(err error) error {
	if errors.Is(err, candidatepkg.ErrInvalidManifest) {
		return pipelinerefusal.Classified(
			err,
			pipelinerefusal.StageCandidateStrictOpen,
			pipelinerefusal.GenerationCandidate,
			pipelinerefusal.ClassificationInvalid,
			pipelinerefusal.DimensionUnknown,
		)
	}
	return pipelinerefusal.At(
		err,
		pipelinerefusal.StageCandidateStrictOpen,
		pipelinerefusal.GenerationCandidate,
	)
}

func candidateStrictValidationFailure(err error) error {
	return candidateStrictOpenFailure(errors.Join(
		candidatepkg.ErrInvalidManifest, err,
	))
}

func extractionBoundaryFailure(err error, stage pipelinerefusal.Stage) error {
	return pipelinerefusal.At(
		err, stage, pipelinerefusal.GenerationExtractionDomain,
	)
}

// closePipelineRefusal makes the closed projection the rendered outer error
// while retaining every taxonomy/terminal/cause wrapper below it. This keeps
// errors.Is/errors.As behavior without allowing a repository or raw child
// diagnostic added by an enclosing formatter to enter the job error string.
func closePipelineRefusal(err error) error {
	receipt, ok := pipelinerefusal.From(err)
	if !ok {
		return err
	}
	return pipelinerefusal.Wrap(err, receipt)
}

func outcomeDisposition(
	outcome *store.ExtractionDomainOutcome,
) store.DomainOutcomeDisposition {
	if outcome == nil {
		return ""
	}
	return outcome.Disposition
}

func (w *Worker) recordDomainOutcome(
	ctx context.Context,
	scope store.ExtractionScope,
	generation store.ExtractionGenerationIdentity,
	disposition store.DomainOutcomeDisposition,
	controlFailure bool,
	runID string,
	operation *domainOperationRecorder,
	failure error,
	previous store.DomainOutcomeDisposition,
) error {
	outcome := store.ExtractionDomainOutcome{
		Scope:                   scope,
		Disposition:             disposition,
		Generation:              generation,
		CandidateControlFailure: controlFailure,
		RunID:                   runID,
		ReceiptSchema:           store.ExtractionOutcomeReceiptSchema,
		Receipt: encodeExtractionDomainOutcomeReceipt(
			operation, disposition, failure,
		),
	}
	if err := w.Evidence.RecordExtractionDomainOutcome(ctx, outcome); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("%s: outcome generation changed: %w",
				scope.Domain, errStaleRun)
		}
		return fmt.Errorf("%s: record %s outcome: %w",
			scope.Domain, disposition, err)
	}
	w.logOutcome(
		scope, generation, disposition, operation.snapshot().Reason, previous,
	)
	return nil
}

func (w *Worker) recordManifestOpenOutcomes(
	ctx context.Context,
	repo *store.Repo,
	extractors []registeredExtractor,
	policyDigest string,
	pointer CandidateManifestPointerIdentity,
	operation *operationRecorder,
	openErr error,
) error {
	disposition, controlFailure := classifyDomainOutcome(openErr)
	operation.completeRemaining(operationReason(openErr))
	// Retryable strict-open failures must not replace a previously settled
	// domain outcome. There is no trustworthy new candidate generation to bind
	// a durable outcome to until the strict open succeeds.
	if !disposition.Settled() {
		return closePipelineRefusal(store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: open candidate manifest: %w",
				repo.Name, openErr)))
	}
	if !validSHA256(pointer.ManifestDigest) ||
		!validSHA256(pointer.PolicyDigest) ||
		pointer.ControlRevision == 0 {
		return closePipelineRefusal(store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: open candidate manifest: %w",
				repo.Name, openErr)))
	}
	inventoryPolicy, policyErr := candidateManifestInventoryPolicy(
		pointer.ManifestDigest,
	)
	if policyErr != nil {
		policyErr = candidateStrictOpenFailure(policyErr)
		return closePipelineRefusal(store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: candidate identity: %w",
				repo.Name, policyErr)))
	}
	if policyDigest == "" {
		policyDigest = pointer.PolicyDigest
	}
	var retryable []error
	for index, ex := range extractors {
		domainOperation := operation.domain(index)
		domainOperation.complete(operationReason(openErr))
		generation := store.ExtractionGenerationIdentity{
			CandidateManifestDigest:  pointer.ManifestDigest,
			CandidatePolicyDigest:    policyDigest,
			CandidateControlRevision: pointer.ControlRevision,
			Extractor:                ex.version,
			InventoryPolicy:          inventoryPolicy,
			DependencyDigest: extractionDependencyDigest(
				"candidate-control-refusal-v1",
				pointer.ManifestDigest,
				policyDigest,
				fmt.Sprintf("%d", pointer.ControlRevision),
			),
		}
		generation.Digest =
			store.ComputeExtractionGenerationDigest(generation)
		if err := w.recordDomainOutcome(
			ctx, extractionScope(repo, ex.domain), generation,
			disposition, controlFailure, "", domainOperation, openErr, "",
		); err != nil {
			if errors.Is(err, errStaleRun) {
				if disposition.Settled() {
					operation.completeRemaining(OperationReasonStale)
					return nil
				}
				return closePipelineRefusal(store.WithClass(store.ClassExtract,
					fmt.Errorf("extract %s: open candidate manifest: %w",
						repo.Name, openErr)))
			}
			retryable = append(retryable, err)
		}
	}
	if len(retryable) > 0 {
		return closePipelineRefusal(store.WithClass(store.ClassExtract,
			fmt.Errorf("extract %s: persist manifest outcomes: %w",
				repo.Name, errors.Join(openErr, errors.Join(retryable...)))))
	}
	return closePipelineRefusal(store.WithClass(store.ClassExtract, store.WithTerminal(
		fmt.Errorf("extract %s: open candidate manifest: %w",
			repo.Name, openErr),
	)))
}

type registeredExtractor struct {
	extractor Extractor
	domain    string
	version   string
}

type domainRunAttempt struct {
	runID      string
	stagedRows int
}

func (w *Worker) validate() ([]registeredExtractor, error) {
	if w.Repos == nil || w.Evidence == nil || w.NewCorpus == nil {
		return nil, errors.New("worker repositories, evidence store, and corpus factory are required")
	}
	if len(w.Extractors) > maxSerialExtractionDomains {
		return nil, fmt.Errorf(
			"%d extractors exceed the %d-domain scheduler bound",
			len(w.Extractors), maxSerialExtractionDomains,
		)
	}
	seen := make(map[string]struct{}, len(w.Extractors))
	registered := make([]registeredExtractor, 0, len(w.Extractors))
	for i, ex := range w.Extractors {
		if ex == nil {
			return nil, fmt.Errorf("extractor %d is nil", i)
		}
		domain, version, err := extractorMetadata(ex)
		if err != nil {
			return nil, fmt.Errorf("extractor %d: %w", i, err)
		}
		if !validToken(domain) || !validToken(version) {
			return nil, fmt.Errorf("extractor %d has invalid domain or version", i)
		}
		if _, duplicate := seen[domain]; duplicate {
			return nil, fmt.Errorf("duplicate extractor domain %q", domain)
		}
		seen[domain] = struct{}{}
		registered = append(registered, registeredExtractor{extractor: ex, domain: domain, version: version})
	}
	return registered, nil
}

func extractorMetadata(ex Extractor) (domain, version string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("metadata panic (%T)", recovered)
		}
	}()
	return ex.Domain(), ex.Version(), nil
}

func validToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if r == '-' || r == '.' || r == '_' || r >= '0' && r <= '9' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		return false
	}
	return true
}

// runOne stages streaming batches under one run and performs exactly one
// guarded publish. Any extractor, validation, staging, cancellation, or
// coverage failure aborts the run; prior published evidence remains visible.
func (w *Worker) runOne(
	ctx context.Context,
	ex registeredExtractor,
	scope store.ExtractionScope,
	corpus Corpus,
	candidateManifest CandidateManifest,
	inventoryPolicy string,
	boundaries gitlinkInventory,
	generation store.ExtractionGenerationIdentity,
	operation *domainOperationRecorder,
	maxStagedRows int,
	abortBudget time.Duration,
	attempt *domainRunAttempt,
	previous store.DomainOutcomeDisposition,
) (err error) {
	if scope.Repository != corpus.RepoName() ||
		scope.Commit != corpus.Commit() ||
		scope.Domain != ex.domain {
		return fmt.Errorf("%s: extraction scope does not match corpus provenance", ex.domain)
	}
	if attempt == nil {
		return fmt.Errorf("%s: extraction attempt recorder is required", ex.domain)
	}

	var run *store.ExtractionRun
	beginRun := func() error {
		stageStarted := operation.started()
		started, beginErr := w.Evidence.BeginExtractionRun(
			ctx, scope, ex.version,
		)
		operation.addStaging(operation.elapsed(stageStarted))
		if beginErr != nil {
			return fmt.Errorf("%s: begin: %w", ex.domain,
				extractionBoundaryFailure(
					beginErr, pipelinerefusal.StageEvidenceStaging,
				))
		}
		if started == nil || started.ID == "" {
			return fmt.Errorf("%s: begin: %w", ex.domain,
				extractionBoundaryFailure(
					errors.New("begin returned no run identity"),
					pipelinerefusal.StageEvidenceStaging,
				))
		}
		run = started
		attempt.runID = run.ID
		return nil
	}
	defer func() {
		if err == nil || run == nil {
			return
		}
		// Cleanup survives job cancellation but is time-bounded. A failed abort
		// leaves an invisible staged run for the stale-run sweeper, never a
		// partially published replacement.
		abortCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), abortBudget,
		)
		defer cancel()
		abortStarted := operation.started()
		if abortErr := w.Evidence.AbortExtractionRun(abortCtx, run.ID); abortErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: abort: %w", ex.domain, abortErr))
		}
		operation.addAbort(operation.elapsed(abortStarted))
		if w.Diagnostics {
			diagnostics.Logf("extract %s: %s aborted", corpus.RepoName(), ex.domain)
		}
	}()

	verifiedCorpus := newVerifiedCorpus(corpus)
	verifiedCorpus.operation = operation
	var sink *runSink
	resourcesClosed := false
	closeResources := func() {
		if resourcesClosed {
			return
		}
		cleanupStarted := operation.started()
		if sink != nil {
			sink.Close()
		}
		verifiedCorpus.Close()
		operation.addCleanup(operation.elapsed(cleanupStarted))
		resourcesClosed = true
	}
	defer func() {
		closeResources()
		operation.capture(verifiedCorpus, sink)
		if sink != nil {
			attempt.stagedRows = sink.operationSnapshot().stagedRows
		}
		operation.completeIfEmpty(operationReason(err))
	}()

	if w.Diagnostics {
		diagnostics.Logf("extract %s: %s inventory started", corpus.RepoName(), ex.domain)
	}
	inventoryStarted := operation.started()
	if candidateManifest == nil {
		err = verifiedCorpus.Inventory(ctx, ex.extractor.Candidate)
	} else {
		err = verifiedCorpus.InventoryCandidateManifest(
			ctx, candidateManifest, ex.domain, ex.version,
			ex.extractor.Candidate,
		)
	}
	operation.addInventory(operation.elapsed(inventoryStarted))
	if err != nil {
		// Inventory is the admission gate, not a run start: rejected manifest
		// members must never create staged evidence or attempt markers.
		return fmt.Errorf("%s: inventory corpus: %w", ex.domain,
			extractionBoundaryFailure(
				err, pipelinerefusal.StageDomainInventory,
			))
	}
	if w.Diagnostics {
		diagnostics.Logf(
			"extract %s: %s inventory complete: files=%d candidates=%d",
			corpus.RepoName(), ex.domain,
			verifiedCorpus.corpusFileCount, len(verifiedCorpus.candidates),
		)
	}
	if err := beginRun(); err != nil {
		return err
	}

	sink = newRunSink(
		ctx, w.Evidence, run.ID, corpus.RepoName(), corpus.Commit(),
		ex.version, verifiedCorpus, operation, maxStagedRows,
	)
	if w.Diagnostics {
		diagnostics.Logf("extract %s: %s extractor started", corpus.RepoName(), ex.domain)
	}
	extractorStarted := operation.started()
	extractorCtx := ctx
	if w.ExtractorDetails {
		extractorCtx = sdk.WithDiagnosticCounters(ctx)
	}
	coverage, extractErr := callExtractor(
		extractorCtx, ex.extractor, verifiedCorpus, sink.Emit,
	)
	operation.addExtractor(operation.elapsed(extractorStarted))
	operation.captureCoverage(coverage, w.ExtractorDetails)
	closeResources()
	if extractErr != nil {
		sinkFailure := sink.failure()
		if sinkFailure != nil {
			extractErr = extractionBoundaryFailure(
				errors.Join(extractErr, sinkFailure),
				pipelinerefusal.StageEvidenceStaging,
			)
		} else {
			extractErr = extractionBoundaryFailure(
				extractErr, pipelinerefusal.StageExtractorExecution,
			)
		}
		return fmt.Errorf("%s: extract: %w", ex.domain, extractErr)
	}
	if err := sink.Finish(); err != nil {
		return fmt.Errorf("%s: stage: %w", ex.domain,
			extractionBoundaryFailure(
				err, pipelinerefusal.StageEvidenceStaging,
			))
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: canceled before publish: %w", ex.domain,
			extractionBoundaryFailure(
				err, pipelinerefusal.StageFinalPublication,
			))
	}
	stats, err := verifiedCorpus.Stats()
	if err != nil {
		return fmt.Errorf("%s: corpus coverage: %w", ex.domain,
			extractionBoundaryFailure(
				err, pipelinerefusal.StageExtractorExecution,
			))
	}
	if candidateManifest != nil && stats.unitDigest != scope.UnitDigest {
		return fmt.Errorf("%s: coverage: %w", ex.domain,
			extractionBoundaryFailure(
				errors.New("candidate coverage unit does not match extraction scope"),
				pipelinerefusal.StageExtractorExecution,
			))
	}
	// Boundary inventory comes from the trusted corpus, never the extractor.
	// A corpus without the capability (in-memory test corpora) has no
	// gitlinks by construction, which the canonical empty inventory states.
	if candidateManifest == nil {
		if boundaryAware, ok := corpus.(boundaryCorpus); ok {
			boundaries = boundaryAware.gitlinkBoundaries()
		}
	}
	manifest, err := coverageManifestForPolicy(
		coverage, sink.atomCount, sink.assertionCount, sink.unresolvedCount,
		stats, boundaries, inventoryPolicy)
	if err != nil {
		return fmt.Errorf("%s: coverage: %w", ex.domain,
			extractionBoundaryFailure(
				err, pipelinerefusal.StageExtractorExecution,
			))
	}
	operation.capture(verifiedCorpus, sink)
	operation.complete(successfulOperationReason(stats, sink.factCount))
	outcome := store.ExtractionDomainOutcome{
		Scope:         scope,
		Disposition:   store.DomainOutcomePublished,
		Generation:    generation,
		RunID:         run.ID,
		ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt: encodeExtractionDomainOutcomeReceipt(
			operation, store.DomainOutcomePublished, nil,
		),
	}
	publicationStarted := operation.started()
	if err := w.Evidence.PublishExtractionRunWithOutcome(
		ctx, run.ID, manifest, outcome,
	); err != nil {
		operation.addPublication(operation.elapsed(publicationStarted))
		if errors.Is(err, store.ErrConflict) {
			stale, checkErr := w.runBecameStale(ctx, scope)
			if checkErr != nil {
				return fmt.Errorf("%s: publish: %w", ex.domain,
					extractionBoundaryFailure(
						errors.Join(err, checkErr),
						pipelinerefusal.StageFinalPublication,
					))
			}
			if stale {
				return fmt.Errorf("%s: %w", ex.domain, errStaleRun)
			}
		}
		return fmt.Errorf("%s: publish: %w", ex.domain,
			extractionBoundaryFailure(
				err, pipelinerefusal.StageFinalPublication,
			))
	}
	operation.addPublication(operation.elapsed(publicationStarted))
	if w.Diagnostics {
		operation.capture(verifiedCorpus, sink)
		w.logDiagnostic("extractor complete", operation.snapshot())
	}
	w.logOutcome(
		scope, generation, store.DomainOutcomePublished,
		operation.snapshot().Reason, previous,
	)
	if w.Diagnostics {
		diagnostics.Logf("extract %s: %s published", corpus.RepoName(), ex.domain)
	}
	return nil
}

func callExtractor(ctx context.Context, ex Extractor, corpus Corpus, emit sdk.Emit) (coverage sdk.Coverage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("extractor panic (%T)", recovered)
		}
	}()
	return ex.Extract(ctx, corpus, emit)
}

func (w *Worker) runBecameStale(ctx context.Context, scope store.ExtractionScope) (bool, error) {
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()
	repo, err := w.Repos.GetRepo(checkCtx, scope.Repository)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	current := extractionScope(repo, scope.Domain)
	return repo.Deleting ||
		current.Commit != scope.Commit ||
		current.UnitDigest != scope.UnitDigest, nil
}

type runSink struct {
	ctx           context.Context
	evidence      store.EvidenceStore
	runID         string
	repo          string
	commit        string
	version       string
	corpus        *verifiedCorpus
	operation     *domainOperationRecorder
	maxStagedRows int

	mu              sync.Mutex
	closed          bool
	err             error
	facts           []sdk.Fact
	factCount       int
	chunkSequence   uint64
	stagedChunkIDs  []string
	stagedChunks    map[string]struct{}
	atomIDs         map[string]struct{}
	assertionIDs    map[string]struct{}
	atomCount       int
	assertionCount  int
	unresolvedCount int
	stagedRows      int
}

func newRunSink(
	ctx context.Context,
	evidence store.EvidenceStore,
	runID, repo, commit, version string,
	corpus *verifiedCorpus,
	operation *domainOperationRecorder,
	maxStagedRows int,
) *runSink {
	return &runSink{
		ctx: ctx, evidence: evidence, runID: runID, repo: repo, commit: commit, version: version, corpus: corpus,
		operation:      operation,
		maxStagedRows:  maxStagedRows,
		facts:          make([]sdk.Fact, 0, evidenceChunkSize),
		stagedChunks:   make(map[string]struct{}),
		atomIDs:        make(map[string]struct{}),
		assertionIDs:   make(map[string]struct{}),
		stagedChunkIDs: make([]string, 0, (maxFactsPerRun+evidenceChunkSize-1)/evidenceChunkSize),
	}
}

func (s *runSink) Emit(fact sdk.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("emit after extractor returned")
	}
	if s.err != nil {
		return s.err
	}
	if err := s.ctx.Err(); err != nil {
		s.err = err
		return err
	}
	if err := validateFact(fact); err != nil {
		s.err = err
		return err
	}
	source, content, lineIndex, ok := s.corpus.Source(fact.Path)
	if !ok {
		s.err = fmt.Errorf("fact %q does not cite the current trusted corpus read", fact.Path)
		return s.err
	}
	if fact.Atom.BlobDigest != source.digest || fact.Atom.EndByte > source.length {
		s.err = fmt.Errorf("fact %q does not match its trusted blob digest/span", fact.Path)
		return s.err
	}
	wantStartLine := sourceLine(content, lineIndex, fact.Atom.StartByte)
	wantEndLine := sourceLine(content, lineIndex, fact.Atom.EndByte-1)
	if fact.StartLine != wantStartLine || fact.EndLine != wantEndLine {
		s.err = fmt.Errorf("fact %q line span does not match its trusted byte span", fact.Path)
		return s.err
	}
	if s.factCount >= maxFactsPerRun {
		s.err = measuredOperationLimitError(
			fmt.Sprintf("run exceeds %d-fact limit", maxFactsPerRun),
			pipelinerefusal.DimensionFacts,
			int64(s.factCount+1), int64(maxFactsPerRun),
		)
		return s.err
	}

	s.facts = append(s.facts, fact)
	s.factCount++
	if len(s.facts) == evidenceChunkSize {
		s.err = s.flushLocked()
	}
	return s.err
}

type sourceRecord struct {
	digest string
	length int
}

// verifiedCorpus binds extractor output to actual trusted reads without
// retaining aggregate blob contents. Its bounded inventory stores path
// identities, its read ledger stores digest and byte length, and only the
// currently read immutable blob remains available for fact validation.
type verifiedCorpus struct {
	inner     Corpus
	repo      string
	commit    string
	operation *domainOperationRecorder

	mu                sync.Mutex
	closed            bool
	readCount         int
	readBytes         int64
	sources           map[string]sourceRecord
	currentPath       string
	currentContent    string
	currentLineIndex  []int
	corpusFileCount   int
	inventoryComplete bool
	enumerated        map[string]struct{}
	enumeratedOrder   []string
	plannedEntries    map[string]treeRecord
	candidates        map[string]struct{}
	domainScope       CandidateManifestScope
	typedInput        treeRecord
	typedInputKind    string
	typedInputPresent bool
	manifestBound     bool
	scoped            bool
	pathInScope       func(string) bool
	excludedFiles     int
	excludedRequired  int
	excludedBytes     int64
	attributionOnce   sync.Once
	attributionSource sdk.AttributionSource
	attributionErr    error
}

func newVerifiedCorpus(inner Corpus) *verifiedCorpus {
	return &verifiedCorpus{
		inner: inner, repo: inner.RepoName(), commit: inner.Commit(),
		sources: make(map[string]sourceRecord), enumerated: make(map[string]struct{}),
		candidates: make(map[string]struct{}),
	}
}

func (c *verifiedCorpus) RepoName() string { return c.repo }
func (c *verifiedCorpus) Commit() string   { return c.commit }

func (c *verifiedCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	if visit == nil {
		return errors.New("walk verified corpus: nil visitor")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("corpus used after extractor returned")
	}
	if !c.inventoryComplete {
		c.mu.Unlock()
		return errors.New("corpus inventory is incomplete")
	}
	paths := append([]string(nil), c.enumeratedOrder...)
	c.mu.Unlock()
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(filePath); err != nil {
			return err
		}
	}
	return nil
}

func (c *verifiedCorpus) Inventory(ctx context.Context, candidate func(string) bool) error {
	if candidate == nil {
		return errors.New("nil candidate predicate")
	}
	paths := make([]string, 0)
	enumerated := make(map[string]struct{})
	candidates := make(map[string]struct{})
	pathBytes := 0
	unreadable := 0
	visit := func(filePath string, isCandidate bool) error {
		if len(paths)+unreadable >= maxCorpusFiles {
			return measuredOperationLimitError(
				fmt.Sprintf("corpus inventory exceeds %d-file limit", maxCorpusFiles),
				pipelinerefusal.DimensionInventoryFiles,
				int64(len(paths)+unreadable+1), int64(maxCorpusFiles),
			)
		}
		if len(filePath) > maxCorpusInventoryPathBytes-pathBytes {
			return measuredOperationLimitError(
				fmt.Sprintf(
					"corpus inventory exceeds %d-byte aggregate path limit",
					maxCorpusInventoryPathBytes,
				),
				pipelinerefusal.DimensionInventoryPathBytes,
				int64(pathBytes+len(filePath)),
				int64(maxCorpusInventoryPathBytes),
			)
		}
		pathBytes += len(filePath)
		if pathErr := checkCorpusPath(filePath); pathErr != nil {
			// A regular file whose name cannot be represented safely contributes
			// to the published corpus file count but is never enumerated:
			// extractors cannot see or Read it. Only a candidate with such a
			// name is a coverage gap, and that fails closed.
			if isCandidate {
				return fmt.Errorf("candidate path is not readable: %w", pathErr)
			}
			unreadable++
			return nil
		}
		if _, duplicate := enumerated[filePath]; duplicate {
			return fmt.Errorf("corpus inventory repeats path %q", filePath)
		}
		enumerated[filePath] = struct{}{}
		paths = append(paths, filePath)
		if isCandidate {
			candidates[filePath] = struct{}{}
		}
		return nil
	}
	var err error
	if trusted, ok := c.inner.(inventoryCorpus); ok {
		err = trusted.WalkInventory(
			ctx,
			func(filePath string) (bool, error) {
				return callCandidate(candidate, filePath)
			},
			visit,
		)
	} else {
		err = c.inner.WalkFiles(ctx, func(filePath string) error {
			isCandidate, candErr := callCandidate(candidate, filePath)
			if candErr != nil {
				return candErr
			}
			return visit(filePath, isCandidate)
		})
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("corpus used after extractor returned")
	}
	c.enumerated = enumerated
	c.enumeratedOrder = paths
	c.candidates = candidates
	c.corpusFileCount = len(paths) + unreadable
	c.inventoryComplete = true
	return nil
}

func (c *verifiedCorpus) InventoryCandidateManifest(
	ctx context.Context,
	manifest CandidateManifest,
	domain, version string,
	candidate func(string) bool,
) error {
	if manifest == nil {
		return errors.New("nil candidate manifest")
	}
	if candidate == nil {
		return errors.New("nil candidate predicate")
	}
	paths := make([]string, 0)
	enumerated := make(map[string]struct{})
	entries := make(map[string]treeRecord)
	candidates := make(map[string]struct{})
	seen := make(map[string]struct{})
	pathBytes := 0
	scope, err := manifest.DomainScope(domain, version)
	if err != nil {
		return fmt.Errorf("candidate manifest %s scope: %w", domain, err)
	}
	if scope.ManifestDigest != manifest.Identity() ||
		scope.CorpusFileCount < 0 ||
		scope.CorpusFileCount > candidatepkg.MaxCorpusEntries ||
		scope.CorpusDeclaredBytes < 0 ||
		!validSHA256(scope.CorpusDigest) ||
		scope.CandidateFileCount < 0 ||
		scope.RequiredFileCount < 0 ||
		scope.RequiredFileCount > scope.CandidateFileCount ||
		scope.CandidateDeclaredBytes < 0 ||
		!validSHA256(scope.CandidateDigest) {
		return fmt.Errorf(
			"candidate manifest %s has invalid scope summary", domain)
	}
	focused := scope.UnitDigest != "" && scope.Plane == candidatepkg.PlaneLocal
	manifestFiles := 0
	manifestRequired := 0
	var manifestDeclaredBytes int64
	excludedFiles := 0
	excludedRequired := 0
	var excludedBytes int64
	err = manifest.ForEachRepositoryFile(
		ctx,
		domain,
		version,
		func(input CandidateManifestFile) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry, err := normalizeManifestFile(input)
			if err != nil {
				return fmt.Errorf("candidate manifest %s record: %w", domain, err)
			}
			if _, duplicate := seen[entry.path]; duplicate {
				return fmt.Errorf(
					"candidate manifest %s repeats record %q",
					domain, entry.path)
			}
			seen[entry.path] = struct{}{}
			if manifestFiles >= maxCorpusFiles {
				return measuredOperationLimitError(
					fmt.Sprintf(
						"candidate manifest %s exceeds %d-file extraction limit",
						domain, maxCorpusFiles,
					),
					pipelinerefusal.DimensionInventoryFiles,
					int64(manifestFiles+1), int64(maxCorpusFiles),
				)
			}
			if len(entry.path) > maxCorpusInventoryPathBytes-pathBytes {
				return measuredOperationLimitError(
					fmt.Sprintf(
						"candidate manifest %s exceeds %d-byte aggregate path limit",
						domain, maxCorpusInventoryPathBytes,
					),
					pipelinerefusal.DimensionInventoryPathBytes,
					int64(pathBytes+len(entry.path)),
					int64(maxCorpusInventoryPathBytes),
				)
			}
			pathBytes += len(entry.path)
			required, err := callCandidate(candidate, entry.path)
			if err != nil {
				return err
			}
			if required != input.Required {
				return fmt.Errorf(
					"candidate manifest %s required ledger disagrees for %q",
					domain, entry.path)
			}
			manifestFiles++
			if required {
				manifestRequired++
			}
			if entry.size > math.MaxInt64-manifestDeclaredBytes {
				return fmt.Errorf(
					"candidate manifest %s declared bytes overflow", domain)
			}
			manifestDeclaredBytes += entry.size
			if focused && input.SourceLane == candidatepkg.SourceLaneGoTest {
				excludedFiles++
				if required {
					excludedRequired++
				}
				excludedBytes += entry.size
				return nil
			}
			enumerated[entry.path] = struct{}{}
			entries[entry.path] = entry
			paths = append(paths, entry.path)
			if required {
				candidates[entry.path] = struct{}{}
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	if manifestFiles != scope.CandidateFileCount ||
		manifestRequired != scope.RequiredFileCount ||
		manifestDeclaredBytes != scope.CandidateDeclaredBytes {
		return fmt.Errorf(
			"candidate manifest %s replay disagrees with scope summary", domain)
	}
	typed, err := manifest.TypedInput(
		domain, version, analysisunit.TypedIndexKindSCIP,
	)
	if err != nil {
		return fmt.Errorf("candidate manifest %s typed input: %w", domain, err)
	}
	typedEntry, typedPresent, err := normalizeManifestTypedInput(typed)
	if err != nil {
		return fmt.Errorf("candidate manifest %s typed input: %w", domain, err)
	}
	corpusFileCount := scope.CorpusFileCount
	if corpusFileCount < manifestFiles {
		return fmt.Errorf(
			"candidate manifest %s plans %d paths from a %d-file corpus",
			domain, manifestFiles, corpusFileCount)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("corpus used after extractor returned")
	}
	c.enumerated = enumerated
	c.enumeratedOrder = paths
	c.plannedEntries = entries
	c.candidates = candidates
	c.domainScope = scope
	c.typedInput = typedEntry
	c.typedInputKind = typed.Kind
	c.typedInputPresent = typedPresent
	c.manifestBound = true
	c.scoped = focused
	c.pathInScope = manifest.PathInScope
	c.excludedFiles = excludedFiles
	c.excludedRequired = excludedRequired
	c.excludedBytes = excludedBytes
	c.corpusFileCount = corpusFileCount
	c.inventoryComplete = true
	return nil
}

func callCandidate(candidate func(string) bool, filePath string) (isCandidate bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("candidate predicate panic (%T)", recovered)
		}
	}()
	return candidate(filePath), nil
}

func (c *verifiedCorpus) Read(ctx context.Context, filePath string) (sdk.Blob, error) {
	return c.read(ctx, filePath, MaxBlobBytes, c.inner.Read)
}

func (c *verifiedCorpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return sdk.SCIPInput{}, errors.New("corpus used after extractor returned")
	}
	if !c.inventoryComplete {
		c.mu.Unlock()
		return sdk.SCIPInput{}, errors.New("read before complete corpus inventory")
	}
	typedPath := c.typedInput.path
	typedPresent := c.typedInputPresent
	manifestBound := c.manifestBound
	fromManifest := c.typedInputKind == analysisunit.TypedIndexKindSCIP
	_, legacyPresent := c.enumerated[scipIndexPath]
	c.mu.Unlock()
	if manifestBound && !fromManifest {
		return sdk.SCIPInput{}, errors.New(
			"candidate domain does not declare a SCIP typed input",
		)
	}
	if fromManifest && !typedPresent {
		return sdk.SCIPInput{Path: typedPath, Present: false}, nil
	}
	inner, ok := c.inner.(sdk.SCIPCorpus)
	if !ok {
		return sdk.SCIPInput{}, errors.New("corpus does not support bounded SCIP index reads")
	}
	if !fromManifest {
		typedPath = scipIndexPath
		if !legacyPresent {
			return sdk.SCIPInput{Path: typedPath, Present: false}, nil
		}
	}
	blob, err := c.read(ctx, typedPath, MaxSCIPIndexBytes, func(ctx context.Context, _ string) (sdk.Blob, error) {
		input, readErr := inner.ReadSCIPIndex(ctx)
		if readErr != nil {
			return sdk.Blob{}, readErr
		}
		if !input.Present {
			return sdk.Blob{}, fmt.Errorf(
				"corpus typed input %q disappeared after inventory", typedPath)
		}
		if input.Path != typedPath {
			return sdk.Blob{}, fmt.Errorf(
				"corpus typed input path changed from %q to %q",
				typedPath, input.Path)
		}
		return input.Blob, nil
	})
	if err != nil {
		if !fromManifest && errors.Is(err, store.ErrNotFound) {
			return sdk.SCIPInput{Path: typedPath, Present: false}, nil
		}
		return sdk.SCIPInput{}, err
	}
	return sdk.SCIPInput{Path: typedPath, Blob: blob, Present: true}, nil
}

func (c *verifiedCorpus) SCIPDocumentInScope(filePath string) bool {
	if checkCorpusPath(filePath) != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.inventoryComplete {
		return false
	}
	if !c.scoped {
		return true
	}
	return c.pathInScope != nil && c.pathInScope(filePath)
}

func (c *verifiedCorpus) SCIPDocumentIncluded(filePath string) bool {
	if checkCorpusPath(filePath) != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.inventoryComplete {
		return false
	}
	return !c.scoped ||
		candidatepkg.SourceLaneForPath(filePath) == candidatepkg.SourceLaneBase
}

func (c *verifiedCorpus) AttributionSource(ctx context.Context) (sdk.AttributionSource, error) {
	c.attributionOnce.Do(func() {
		c.attributionSource, c.attributionErr = loadAttributionSource(ctx, c)
	})
	return c.attributionSource, c.attributionErr
}

func (c *verifiedCorpus) containsRegular(ctx context.Context, filePath string) (bool, error) {
	if err := checkCorpusPath(filePath); err != nil {
		return false, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false, errors.New("corpus used after extractor returned")
	}
	if !c.inventoryComplete {
		c.mu.Unlock()
		return false, errors.New("lookup before complete corpus inventory")
	}
	_, planned := c.enumerated[filePath]
	scoped := c.scoped
	pathInScope := c.pathInScope
	c.mu.Unlock()
	if planned {
		return true, nil
	}
	if scoped && (pathInScope == nil || !pathInScope(filePath)) {
		return false, nil
	}
	if exact, ok := c.inner.(exactTreeCorpus); ok {
		_, present, err := exact.lookupRegular(ctx, filePath)
		return present, err
	}
	// A corpus without the trusted exact-lookup capability is an isolated
	// in-memory corpus whose WalkFiles result is its complete tree.
	return false, nil
}

func (c *verifiedCorpus) anyRegularUnder(ctx context.Context, root string) (bool, error) {
	if err := checkCorpusPath(root); err != nil {
		return false, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false, errors.New("corpus used after extractor returned")
	}
	if !c.inventoryComplete {
		c.mu.Unlock()
		return false, errors.New("lookup before complete corpus inventory")
	}
	for filePath := range c.enumerated {
		if filePath != root && pathWithinRoot(filePath, root) {
			c.mu.Unlock()
			return true, nil
		}
	}
	scoped := c.scoped
	pathInScope := c.pathInScope
	c.mu.Unlock()
	if scoped && (pathInScope == nil || !pathInScope(root)) {
		return false, nil
	}
	if exact, ok := c.inner.(exactTreeCorpus); ok {
		return exact.anyRegularUnder(ctx, root)
	}
	return false, nil
}

func (c *verifiedCorpus) read(
	ctx context.Context,
	filePath string,
	maxBytes int64,
	read func(context.Context, string) (sdk.Blob, error),
) (sdk.Blob, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return sdk.Blob{}, errors.New("corpus used after extractor returned")
	}
	if !c.inventoryComplete {
		c.mu.Unlock()
		return sdk.Blob{}, errors.New("read before complete corpus inventory")
	}
	_, enumerated := c.enumerated[filePath]
	typed := c.typedInputPresent && c.typedInput.path == filePath
	if !enumerated && !typed {
		c.mu.Unlock()
		return sdk.Blob{}, fmt.Errorf("read path %q was not in corpus inventory", filePath)
	}
	manifestEntry, fromManifest := c.plannedEntries[filePath]
	if typed {
		manifestEntry = c.typedInput
		fromManifest = true
	}
	if c.readCount >= maxCorpusFiles*4 {
		c.mu.Unlock()
		return sdk.Blob{}, measuredOperationLimitError(
			fmt.Sprintf("corpus exceeds %d-read limit", maxCorpusFiles*4),
			pipelinerefusal.DimensionSourceReadAttempts,
			int64(c.readCount+1), int64(maxCorpusFiles*4),
		)
	}
	c.readCount++
	c.mu.Unlock()

	var blob sdk.Blob
	var err error
	readStarted := c.operation.started()
	if exact, ok := c.inner.(exactTreeCorpus); ok && fromManifest {
		blob, err = exact.readManifestBlob(ctx, manifestEntry, maxBytes)
	} else {
		blob, err = read(ctx, filePath)
	}
	c.operation.addOpenedSource(c.operation.elapsed(readStarted))
	if err != nil {
		return sdk.Blob{}, err
	}
	if int64(len(blob.Content)) > maxBytes {
		return sdk.Blob{}, measuredOperationLimitError(
			fmt.Sprintf("corpus blob %q exceeds byte limit", filePath),
			pipelinerefusal.DimensionSourceBlobBytes,
			int64(len(blob.Content)), maxBytes,
		)
	}
	if fromManifest && int64(len(blob.Content)) != manifestEntry.size {
		return sdk.Blob{}, fmt.Errorf(
			"corpus blob %q has size %d, manifest declares %d",
			filePath, len(blob.Content), manifestEntry.size,
		)
	}
	digest := sha256.Sum256([]byte(blob.Content))
	wantDigest := "sha256:" + hex.EncodeToString(digest[:])
	if blob.Digest != wantDigest {
		return sdk.Blob{}, fmt.Errorf("corpus blob %q has invalid trusted digest", filePath)
	}
	record := sourceRecord{digest: wantDigest, length: len(blob.Content)}
	lineIndex := buildLineIndex(blob.Content)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return sdk.Blob{}, errors.New("corpus used after extractor returned")
	}
	if _, exists := c.sources[filePath]; !exists && int64(len(blob.Content)) > maxCorpusRunBytes-c.readBytes {
		return sdk.Blob{}, measuredOperationLimitError(
			fmt.Sprintf(
				"corpus exceeds %d-byte aggregate read limit", maxCorpusRunBytes,
			),
			pipelinerefusal.DimensionSourceReadBytes,
			c.readBytes+int64(len(blob.Content)), maxCorpusRunBytes,
		)
	}
	if previous, ok := c.sources[filePath]; ok && previous != record {
		return sdk.Blob{}, fmt.Errorf("corpus blob %q changed during extraction", filePath)
	}
	if _, ok := c.sources[filePath]; !ok && len(c.sources) >= maxCorpusFiles {
		return sdk.Blob{}, measuredOperationLimitError(
			fmt.Sprintf("corpus exceeds %d-read-file limit", maxCorpusFiles),
			pipelinerefusal.DimensionSourceReadFiles,
			int64(len(c.sources)+1), int64(maxCorpusFiles),
		)
	}
	if _, exists := c.sources[filePath]; !exists {
		c.readBytes += int64(len(blob.Content))
	}
	c.sources[filePath] = record
	c.currentPath = filePath
	c.currentContent = blob.Content
	c.currentLineIndex = lineIndex
	return blob, nil
}

func (c *verifiedCorpus) Source(filePath string) (sourceRecord, string, []int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.sources[filePath]
	if !ok || c.currentPath != filePath {
		return sourceRecord{}, "", nil, false
	}
	return record, c.currentContent, c.currentLineIndex, true
}

// buildLineIndex stores the newline count at sparse block boundaries. Fact
// validation then scans at most one small block per endpoint instead of
// rescanning a multi-megabyte source from byte zero for every emitted fact.
func buildLineIndex(content string) []int {
	index := make([]int, len(content)/lineIndexBlock+1)
	newlines := 0
	for block := range index {
		index[block] = newlines
		start := block * lineIndexBlock
		end := start + lineIndexBlock
		if end > len(content) {
			end = len(content)
		}
		newlines += strings.Count(content[start:end], "\n")
	}
	return index
}

func sourceLine(content string, index []int, offset int) int {
	block := offset / lineIndexBlock
	start := block * lineIndexBlock
	return 1 + index[block] + strings.Count(content[start:offset], "\n")
}

func (c *verifiedCorpus) Close() {
	c.mu.Lock()
	c.closed = true
	c.currentContent = ""
	c.currentLineIndex = nil
	c.mu.Unlock()
}

type corpusStats struct {
	corpusFileCount    int
	candidateFileCount int
	readFileCount      int
	readBytes          int64
	sourceScopeDigest  string
	scopePosture       string
	manifestDigest     string
	unitDigest         string
	plane              candidatepkg.Plane
	scopeCorpusFiles   int
	scopeCorpusBytes   int64
	scopeCorpusDigest  string
	plannedFileCount   int
	plannedRequired    int
	plannedBytes       int64
	plannedDigest      string
	excludedFiles      int
	excludedRequired   int
	excludedBytes      int64
	typedInputKind     string
	typedInputPath     string
	typedInputObjectID string
	typedInputBytes    int64
	typedInputDigest   string
	typedInputPresent  bool
}

type verifiedCorpusOperationSnapshot struct {
	corpusFiles    int
	candidateFiles int
	readAttempts   int
	readFiles      int
	readBytes      int64
	plannedBytes   int64
	excludedFiles  int
	excludedBytes  int64
}

func (c *verifiedCorpus) operationSnapshot() verifiedCorpusOperationSnapshot {
	if c == nil {
		return verifiedCorpusOperationSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return verifiedCorpusOperationSnapshot{
		corpusFiles:    c.corpusFileCount,
		candidateFiles: len(c.candidates),
		readAttempts:   c.readCount,
		readFiles:      len(c.sources),
		readBytes:      c.readBytes,
		plannedBytes:   c.domainScope.CandidateDeclaredBytes,
		excludedFiles:  c.excludedFiles,
		excludedBytes:  c.excludedBytes,
	}
}

func (c *verifiedCorpus) Stats() (corpusStats, error) {
	c.mu.Lock()
	attributionSource := c.attributionSource
	c.mu.Unlock()
	if validated, ok := attributionSource.(interface{ validationError() error }); ok {
		if err := validated.validationError(); err != nil {
			return corpusStats{}, fmt.Errorf(
				"attribution tree validation: %w", err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.inventoryComplete {
		return corpusStats{}, errors.New("corpus inventory never completed")
	}
	for filePath := range c.candidates {
		if _, read := c.sources[filePath]; !read {
			return corpusStats{}, fmt.Errorf("candidate %q was not read", filePath)
		}
	}
	paths := make([]string, 0, len(c.sources))
	for filePath := range c.sources {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, filePath := range paths {
		source := c.sources[filePath]
		_, _ = fmt.Fprintf(hash, "%d:%s;%d:%s;%d;", len(filePath), filePath,
			len(source.digest), source.digest, source.length)
	}
	typedDigest := ""
	if c.typedInputPresent {
		typedSource, read := c.sources[c.typedInput.path]
		if !read {
			return corpusStats{}, fmt.Errorf(
				"typed input %q was not read", c.typedInput.path)
		}
		typedDigest = typedSource.digest
	}
	posture := "whole-repository"
	if c.domainScope.UnitDigest != "" {
		posture = "repository-overlay"
		if c.domainScope.Plane == candidatepkg.PlaneLocal {
			posture = "focused-local"
		}
	}
	return corpusStats{
		corpusFileCount: c.corpusFileCount, candidateFileCount: len(c.candidates),
		readFileCount: len(paths),
		readBytes:     c.readBytes, sourceScopeDigest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		scopePosture:       posture,
		manifestDigest:     c.domainScope.ManifestDigest,
		unitDigest:         c.domainScope.UnitDigest,
		plane:              c.domainScope.Plane,
		scopeCorpusFiles:   c.domainScope.CorpusFileCount,
		scopeCorpusBytes:   c.domainScope.CorpusDeclaredBytes,
		scopeCorpusDigest:  c.domainScope.CorpusDigest,
		plannedFileCount:   c.domainScope.CandidateFileCount,
		plannedRequired:    c.domainScope.RequiredFileCount,
		plannedBytes:       c.domainScope.CandidateDeclaredBytes,
		plannedDigest:      c.domainScope.CandidateDigest,
		excludedFiles:      c.excludedFiles,
		excludedRequired:   c.excludedRequired,
		excludedBytes:      c.excludedBytes,
		typedInputKind:     c.typedInputKind,
		typedInputPath:     c.typedInput.path,
		typedInputObjectID: c.typedInput.oid,
		typedInputBytes:    c.typedInput.size,
		typedInputDigest:   typedDigest,
		typedInputPresent:  c.typedInputPresent,
	}, nil
}

type runSinkOperationSnapshot struct {
	facts        int
	atoms        int
	assertions   int
	unresolved   int
	stagedChunks int
	stagedRows   int
}

func (s *runSink) operationSnapshot() runSinkOperationSnapshot {
	if s == nil {
		return runSinkOperationSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return runSinkOperationSnapshot{
		facts: s.factCount, atoms: s.atomCount,
		assertions: s.assertionCount, unresolved: s.unresolvedCount,
		stagedChunks: len(s.stagedChunkIDs),
		stagedRows:   s.stagedRows,
	}
}

func (s *runSink) failure() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *runSink) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *runSink) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.err = s.flushLocked()
	return s.err
}

func (s *runSink) flushLocked() error {
	if len(s.facts) == 0 {
		return nil
	}
	stageStarted := s.operation.started()
	defer func() {
		s.operation.addStaging(s.operation.elapsed(stageStarted))
	}()
	chunk := buildFactChunk(s.chunkSequence, s.facts)
	if err := s.stageChunkLocked(chunk); err != nil {
		return err
	}
	s.facts = s.facts[:0]
	return nil
}

// buildFactChunk assigns a content-derived identity to one exact ordered
// transport unit. The sequence is part of the identity, so equal extractor
// inputs produce the same ordered IDs while a reordered stream cannot alias.
func buildFactChunk(sequence uint64, facts []sdk.Fact) sdk.FactChunk {
	chunk := sdk.FactChunk{
		Schema: evidenceChunkSchema, Sequence: sequence,
		Facts: append([]sdk.Fact(nil), facts...),
	}
	chunk.ID = computeFactChunkID(chunk)
	return chunk
}

func computeFactChunkID(chunk sdk.FactChunk) string {
	hash := sha256.New()
	writeChunkField(hash, chunk.Schema)
	_, _ = fmt.Fprintf(hash, "%d;%d;", chunk.Sequence, len(chunk.Facts))
	for _, fact := range chunk.Facts {
		for _, value := range []string{
			fact.Atom.SchemaVersion, fact.Atom.BlobDigest, fact.Atom.RuleID,
			fact.Atom.AdapterConfigDigest, fact.Atom.FactFingerprint,
			fact.Assertion.Predicate, fact.Assertion.Subject, fact.Assertion.Object,
			fact.Assertion.Lineage, fact.Assertion.Tier, fact.Assertion.CodeRole,
			fact.Assertion.Detail, fact.Path,
		} {
			writeChunkField(hash, value)
		}
		_, _ = fmt.Fprintf(hash, "%d;%d;%d;%d;",
			fact.Atom.StartByte, fact.Atom.EndByte, fact.StartLine, fact.EndLine)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type chunkHashWriter interface {
	Write([]byte) (int, error)
}

func writeChunkField(hash chunkHashWriter, value string) {
	_, _ = fmt.Fprintf(hash, "%d:", len(value))
	_, _ = hash.Write([]byte(value))
	_, _ = hash.Write([]byte{';'})
}

// stageChunkLocked validates and stages one self-contained chunk. An exact
// replay is a no-op before it reaches the store or worker counters; the store's
// content-keyed rows independently make a transaction retry idempotent.
func (s *runSink) stageChunkLocked(chunk sdk.FactChunk) error {
	if chunk.Schema != evidenceChunkSchema {
		return errors.New("fact chunk has unsupported schema")
	}
	if len(chunk.Facts) == 0 || len(chunk.Facts) > evidenceChunkSize {
		return fmt.Errorf("fact chunk has invalid size %d", len(chunk.Facts))
	}
	if chunk.ID != computeFactChunkID(chunk) {
		return errors.New("fact chunk has invalid content identity")
	}
	if _, staged := s.stagedChunks[chunk.ID]; staged {
		return nil
	}
	if chunk.Sequence != s.chunkSequence {
		return fmt.Errorf("fact chunk sequence %d does not follow %d", chunk.Sequence, s.chunkSequence)
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	for _, fact := range chunk.Facts {
		if err := validateFact(fact); err != nil {
			return fmt.Errorf("fact chunk contains invalid fact: %w", err)
		}
	}

	atoms := make([]store.EvidenceAtom, 0, len(chunk.Facts))
	assocs := make([]store.SnapshotEvidence, 0, len(chunk.Facts))
	asserts := make([]store.Assertion, 0, len(chunk.Facts))
	for _, fact := range chunk.Facts {
		atom := store.EvidenceAtom{
			SchemaVersion: fact.Atom.SchemaVersion, BlobDigest: fact.Atom.BlobDigest,
			StartByte: fact.Atom.StartByte, EndByte: fact.Atom.EndByte,
			RuleID: fact.Atom.RuleID, ExtractorVersion: s.version,
			AdapterConfigDigest: fact.Atom.AdapterConfigDigest,
			FactFingerprint:     fact.Atom.FactFingerprint,
		}
		atom.ID = store.ComputeAtomID(atom)
		atoms = append(atoms, atom)
		assocs = append(assocs, store.SnapshotEvidence{
			AtomID: atom.ID, Repo: s.repo, Commit: s.commit, Path: fact.Path,
			StartLine: fact.StartLine, EndLine: fact.EndLine,
			VisibilityScope: "repo:" + s.repo,
		})
		asserts = append(asserts, store.Assertion{
			Predicate: fact.Assertion.Predicate, Subject: fact.Assertion.Subject,
			Object: fact.Assertion.Object, Lineage: fact.Assertion.Lineage,
			Tier: fact.Assertion.Tier, CodeRole: fact.Assertion.CodeRole,
			Repo: s.repo, Supporting: []string{atom.ID}, Detail: fact.Assertion.Detail,
		})
	}
	chunkStagedRows := len(assocs) + len(asserts)
	if s.stagedRows+chunkStagedRows > s.maxStagedRows {
		return pipelinerefusal.Measure(
			fmt.Errorf(
				"%w: domain would exceed %d staged rows",
				errExtractionDomainBudget, s.maxStagedRows,
			),
			pipelinerefusal.DimensionStagedRows,
			int64(s.stagedRows+chunkStagedRows), int64(s.maxStagedRows),
		)
	}
	if err := s.evidence.AddEvidenceChunk(
		s.ctx, s.runID, chunk.ID, len(chunk.Facts), atoms, assocs, asserts,
	); err != nil {
		return err
	}
	s.stagedRows += chunkStagedRows

	for i, atom := range atoms {
		if _, exists := s.atomIDs[atom.ID]; !exists {
			s.atomIDs[atom.ID] = struct{}{}
			s.atomCount++
		}
		assertionID := store.ComputeAssertionID(asserts[i])
		if _, exists := s.assertionIDs[assertionID]; !exists {
			s.assertionIDs[assertionID] = struct{}{}
			s.assertionCount++
			if asserts[i].Tier == store.TierUnresolved {
				s.unresolvedCount++
			}
		}
	}
	s.stagedChunks[chunk.ID] = struct{}{}
	s.stagedChunkIDs = append(s.stagedChunkIDs, chunk.ID)
	s.chunkSequence++
	return nil
}

func validateFact(f sdk.Fact) error {
	if err := checkCorpusPath(f.Path); err != nil {
		return err
	}
	if f.StartLine <= 0 || f.EndLine < f.StartLine {
		return fmt.Errorf("fact %q has invalid line span", f.Path)
	}
	if f.Atom.StartByte < 0 || f.Atom.EndByte <= f.Atom.StartByte || f.Atom.EndByte > int(MaxBlobBytes) {
		return fmt.Errorf("fact %q has invalid byte span", f.Path)
	}
	if !validToken(f.Atom.SchemaVersion) || !validToken(f.Atom.RuleID) ||
		f.Atom.AdapterConfigDigest == "" || f.Atom.FactFingerprint == "" {
		return fmt.Errorf("fact %q has incomplete atom provenance", f.Path)
	}
	if !validSHA256(f.Atom.BlobDigest) ||
		(f.Atom.AdapterConfigDigest != "none" && !validSHA256(f.Atom.AdapterConfigDigest)) {
		return fmt.Errorf("fact %q has invalid digest provenance", f.Path)
	}
	for _, field := range []struct{ name, value string }{
		{"predicate", f.Assertion.Predicate}, {"subject", f.Assertion.Subject},
		{"object", f.Assertion.Object}, {"lineage", f.Assertion.Lineage},
		{"code role", f.Assertion.CodeRole}, {"fact fingerprint", f.Atom.FactFingerprint},
		{"detail", f.Assertion.Detail},
	} {
		if len(field.value) > maxFactTextBytes || !utf8.ValidString(field.value) {
			return fmt.Errorf("fact %q %s is not bounded UTF-8", f.Path, field.name)
		}
	}
	if f.Assertion.Predicate == "" || f.Assertion.Subject == "" || f.Assertion.Object == "" {
		return fmt.Errorf("fact %q has incomplete assertion", f.Path)
	}
	if !validToken(f.Assertion.Predicate) {
		return fmt.Errorf("fact %q has invalid predicate", f.Path)
	}
	switch f.Assertion.CodeRole {
	case "", "production", "test", "mock", "vendor", "generated":
	default:
		return fmt.Errorf("fact %q has invalid code role %q", f.Path, f.Assertion.CodeRole)
	}
	switch f.Assertion.Tier {
	case store.TierExact, store.TierDerived, store.TierHeuristic, store.TierUnresolved:
	default:
		return fmt.Errorf("fact %q has invalid confidence tier %q", f.Path, f.Assertion.Tier)
	}
	return nil
}

func validSHA256(value string) bool {
	digest, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(digest) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32 && strings.ToLower(digest) == digest
}

// corpusInventoryPolicy is the inventory contract stamped on every manifest
// this worker publishes: gitlink boundaries are counted and digest-bound
// (T19.8), and candidate symlinks are extractor-gated aliases to same-commit
// final regular candidates. Legacy runs without this generation have unknown
// symlink policy and are replaced even when commit and extractor version
// match.
const corpusInventoryPolicy = "gitlink-boundary-v2"

func coverageManifest(
	coverage sdk.Coverage,
	atomCount, assertionCount, unresolvedCount int,
	stats corpusStats,
	boundaries gitlinkInventory,
) (store.CoverageManifest, error) {
	return coverageManifestForPolicy(
		coverage, atomCount, assertionCount, unresolvedCount,
		stats, boundaries, corpusInventoryPolicy)
}

func coverageManifestForPolicy(
	coverage sdk.Coverage,
	atomCount, assertionCount, unresolvedCount int,
	stats corpusStats,
	boundaries gitlinkInventory,
	inventoryPolicy string,
) (store.CoverageManifest, error) {
	if len(coverage.Failures) != 0 {
		return store.CoverageManifest{}, fmt.Errorf("extractor reported %d failure(s); refusing partial publication", len(coverage.Failures))
	}
	if coverage.UnresolvedCount != unresolvedCount {
		return store.CoverageManifest{}, fmt.Errorf(
			"reported unresolved count %d does not match %d emitted unresolved assertions",
			coverage.UnresolvedCount, unresolvedCount)
	}
	if len(coverage.Protocols) > 64 {
		return store.CoverageManifest{}, errors.New("more than 64 coverage protocols")
	}
	protocols := append([]string(nil), coverage.Protocols...)
	sort.Strings(protocols)
	for i, protocol := range protocols {
		if !validToken(protocol) || i > 0 && protocols[i-1] == protocol {
			return store.CoverageManifest{}, errors.New("invalid or duplicate coverage protocol")
		}
	}
	if boundaries.count < 0 || boundaries.digest == "" ||
		len(boundaries.samplePaths) > maxGitlinkSamplePaths ||
		len(boundaries.samplePaths) > boundaries.count && boundaries.count > 0 ||
		boundaries.count == 0 && len(boundaries.samplePaths) > 0 {
		return store.CoverageManifest{}, errors.New("gitlink boundary inventory is inconsistent")
	}
	if inventoryPolicy == "" || len(inventoryPolicy) > 128 ||
		strings.TrimSpace(inventoryPolicy) != inventoryPolicy {
		return store.CoverageManifest{}, errors.New("inventory policy is invalid")
	}
	manifest := store.CoverageManifest{
		Protocols:              protocols,
		CorpusFileCount:        stats.corpusFileCount,
		CandidateFileCount:     stats.candidateFileCount,
		ReadFileCount:          stats.readFileCount,
		ReadBytes:              stats.readBytes,
		SourceScopeDigest:      stats.sourceScopeDigest,
		UnresolvedCount:        unresolvedCount,
		AssertionCount:         assertionCount,
		AtomCount:              atomCount,
		InventoryPolicy:        inventoryPolicy,
		GitlinkCount:           boundaries.count,
		GitlinkDigest:          boundaries.digest,
		GitlinkSamplePaths:     append([]string(nil), boundaries.samplePaths...),
		GitlinkSampleTruncated: boundaries.sampleTruncated,
	}
	if stats.manifestDigest == "" {
		return manifest, nil
	}
	manifest.ScopePosture = stats.scopePosture
	manifest.CandidateManifestDigest = stats.manifestDigest
	manifest.CandidatePlane = string(stats.plane)
	manifest.ScopeCorpusFileCount = stats.scopeCorpusFiles
	manifest.ScopeCorpusDeclaredBytes = stats.scopeCorpusBytes
	manifest.ScopeCorpusDigest = stats.scopeCorpusDigest
	manifest.PlannedFileCount = stats.plannedFileCount
	manifest.PlannedRequiredFileCount = stats.plannedRequired
	manifest.PlannedDeclaredBytes = stats.plannedBytes
	manifest.PlannedScopeDigest = stats.plannedDigest
	manifest.ExcludedSourceFileCount = stats.excludedFiles
	manifest.ExcludedSourceRequiredCount = stats.excludedRequired
	manifest.ExcludedSourceDeclaredBytes = stats.excludedBytes
	manifest.ExcludedSCIPDocumentCount = coverage.ExcludedSCIPDocuments
	manifest.ExcludedSCIPDefinitionCount = coverage.ExcludedSCIPDefinitions
	manifest.ExcludedSCIPOccurrenceCount = coverage.ExcludedSCIPOccurrences
	if stats.typedInputKind != "" {
		manifest.TypedInputKind = stats.typedInputKind
		manifest.TypedInputPath = stats.typedInputPath
		manifest.TypedInputObjectID = stats.typedInputObjectID
		manifest.TypedInputDeclaredBytes = stats.typedInputBytes
		manifest.TypedInputDigest = stats.typedInputDigest
		manifest.TypedInputPresent = stats.typedInputPresent
	}
	return manifest, nil
}

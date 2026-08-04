package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	candidatepkg "github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	ExtractionOperationSchema  = "phebs-extraction-operation-v1"
	MaxExtractionOperationSize = 64 << 10

	OperationReasonAlreadyCurrent    = "already_current"
	OperationReasonNotReady          = "not_ready"
	OperationReasonStale             = "stale"
	OperationReasonNoCandidates      = "no_candidates"
	OperationReasonTypedInputAbsent  = "typed_input_absent"
	OperationReasonLimitRefusal      = "limit_refusal"
	OperationReasonPublishedEmpty    = "published_empty"
	OperationReasonPublishedNonempty = "published_nonempty"
	OperationReasonAggregateBudget   = "aggregate_budget"
	OperationReasonDomainBudget      = "domain_budget"
	OperationReasonCanceled          = "canceled"
	OperationReasonFailed            = "failed"
)

// ExtractionOperationSink is the existing operational-log seam made
// injectable for deterministic verification. A sink failure is deliberately
// advisory: it never changes extraction, publication, or retry disposition.
type ExtractionOperationSink func(report []byte) error

// ExtractionOperationReport is one bounded, non-authoritative operational
// receipt for a repository extraction job. Durations are diagnostics only;
// no field participates in freshness, publication, proof, or evidence
// identity.
type ExtractionOperationReport struct {
	Schema                   string                      `json:"schema"`
	Repository               string                      `json:"repository"`
	IndexedHead              string                      `json:"indexed_head,omitempty"`
	UnitDigest               string                      `json:"unit_digest,omitempty"`
	CandidateManifestDigest  string                      `json:"candidate_manifest_digest,omitempty"`
	CandidateControlRevision uint64                      `json:"candidate_control_revision,omitempty"`
	PolicyDigest             string                      `json:"policy_digest,omitempty"`
	Attempt                  ExtractionOperationAttempt  `json:"attempt"`
	QueueWaitMS              int64                       `json:"queue_wait_ms"`
	MirrorLockWaitMS         int64                       `json:"mirror_lock_wait_ms"`
	PointerWorkMS            int64                       `json:"pointer_work_ms"`
	StrictOpenMS             int64                       `json:"strict_open_ms"`
	TotalMS                  int64                       `json:"total_ms"`
	Domains                  []ExtractionOperationDomain `json:"domains,omitempty"`
	Truncated                bool                        `json:"truncated,omitempty"`
}

// ExtractionOperationAttempt names the queue attempt without exposing the
// runner lease token.
type ExtractionOperationAttempt struct {
	JobID  string `json:"job_id,omitempty"`
	Number int    `json:"number"`
	Force  bool   `json:"force,omitempty"`
}

// ExtractionOperationDomain contains only bounded phase durations, aggregate
// counts/bytes/limits, and a frozen generic completion reason. It never carries
// source paths, content, samples, or raw extractor diagnostics.
type ExtractionOperationDomain struct {
	Domain               string                          `json:"domain"`
	ExtractorVersion     string                          `json:"extractor_version"`
	Reason               string                          `json:"reason"`
	InventoryMS          int64                           `json:"inventory_ms"`
	OpenedSourceMS       int64                           `json:"opened_source_ms"`
	ExtractorMS          int64                           `json:"extractor_ms"`
	StagingMS            int64                           `json:"staging_ms"`
	PublicationMS        int64                           `json:"publication_ms"`
	AbortMS              int64                           `json:"abort_ms"`
	CleanupMS            int64                           `json:"cleanup_ms"`
	Counts               ExtractionOperationDomainCounts `json:"counts"`
	Bytes                ExtractionOperationDomainBytes  `json:"bytes"`
	Limits               ExtractionOperationDomainLimits `json:"limits"`
	ExtractorDiagnostics []sdk.DiagnosticCounter         `json:"extractor_diagnostics,omitempty"`
}

type ExtractionOperationDomainCounts struct {
	CorpusFiles             int `json:"corpus_files"`
	CandidateFiles          int `json:"candidate_files"`
	ExcludedSourceFiles     int `json:"excluded_source_files"`
	ExcludedSCIPDocuments   int `json:"excluded_scip_documents"`
	ExcludedSCIPDefinitions int `json:"excluded_scip_definitions"`
	ExcludedSCIPOccurrences int `json:"excluded_scip_occurrences"`
	OpenedSourceAttempts    int `json:"opened_source_attempts"`
	OpenedSourceFiles       int `json:"opened_source_files"`
	Facts                   int `json:"facts"`
	Atoms                   int `json:"atoms"`
	Assertions              int `json:"assertions"`
	Unresolved              int `json:"unresolved"`
	StagedChunks            int `json:"staged_chunks"`
	StagedRows              int `json:"staged_rows"`
}

type ExtractionOperationDomainBytes struct {
	PlannedDeclared        int64 `json:"planned_declared"`
	ExcludedSourceDeclared int64 `json:"excluded_source_declared"`
	OpenedSource           int64 `json:"opened_source"`
}

type ExtractionOperationDomainLimits struct {
	CorpusFiles            int   `json:"corpus_files"`
	OpenedSourceAttempts   int   `json:"opened_source_attempts"`
	OpenedSourceFiles      int   `json:"opened_source_files"`
	OpenedSourceBytes      int64 `json:"opened_source_bytes"`
	Facts                  int   `json:"facts"`
	SourceBlobBytes        int64 `json:"source_blob_bytes"`
	TypedInputBytes        int64 `json:"typed_input_bytes"`
	AggregateWallMS        int64 `json:"aggregate_wall_ms"`
	MirrorLockMS           int64 `json:"mirror_lock_ms"`
	DomainWallMS           int64 `json:"domain_wall_ms"`
	AbortWallMS            int64 `json:"abort_wall_ms"`
	OutcomeWallMS          int64 `json:"outcome_wall_ms"`
	MaxSerialDomains       int   `json:"max_serial_domains"`
	SchedulerIdentityBytes int   `json:"scheduler_identity_bytes"`
	AggregateStagedRows    int   `json:"aggregate_staged_rows"`
	DomainStagedRows       int   `json:"domain_staged_rows"`
}

// ExtractionDomainOutcomeReceipt is the bounded durable diagnostic committed
// with one authoritative disposition. It freezes only work completed before
// the store transition; publication/abort and enclosing-job timing remain in
// the later T30.6a report.
type ExtractionDomainOutcomeReceipt struct {
	Schema           string                          `json:"schema"`
	Domain           string                          `json:"domain"`
	ExtractorVersion string                          `json:"extractor_version"`
	Disposition      store.DomainOutcomeDisposition  `json:"disposition"`
	Reason           string                          `json:"reason"`
	InventoryMS      int64                           `json:"inventory_ms"`
	OpenedSourceMS   int64                           `json:"opened_source_ms"`
	ExtractorMS      int64                           `json:"extractor_ms"`
	StagingMS        int64                           `json:"staging_ms"`
	Counts           ExtractionOperationDomainCounts `json:"counts"`
	Bytes            ExtractionOperationDomainBytes  `json:"bytes"`
	Limits           ExtractionOperationDomainLimits `json:"limits"`
}

type extractionOperationMinimal struct {
	Schema                   string                     `json:"schema"`
	Repository               string                     `json:"repository"`
	IndexedHead              string                     `json:"indexed_head,omitempty"`
	UnitDigest               string                     `json:"unit_digest,omitempty"`
	CandidateManifestDigest  string                     `json:"candidate_manifest_digest,omitempty"`
	CandidateControlRevision uint64                     `json:"candidate_control_revision,omitempty"`
	PolicyDigest             string                     `json:"policy_digest,omitempty"`
	Attempt                  ExtractionOperationAttempt `json:"attempt"`
	DomainCount              int                        `json:"domain_count"`
	Truncated                bool                       `json:"truncated"`
}

type operationRecorder struct {
	mu      sync.Mutex
	now     func() time.Time
	started time.Time
	report  ExtractionOperationReport
	domains []*domainOperationRecorder
}

type domainOperationRecorder struct {
	mu     sync.Mutex
	now    func() time.Time
	report ExtractionOperationDomain
}

func newOperationRecorder(
	job store.Job,
	now func() time.Time,
) *operationRecorder {
	if now == nil {
		now = time.Now
	}
	started := now()
	queueWait := time.Duration(0)
	if job.ClaimedAt != nil && !job.CreatedAt.IsZero() {
		eligibleAt := job.CreatedAt
		if job.NotBefore != nil && job.NotBefore.After(eligibleAt) {
			eligibleAt = *job.NotBefore
		}
		queueWait = nonnegativeDuration(eligibleAt, *job.ClaimedAt)
	}
	return &operationRecorder{
		now: now, started: started,
		report: ExtractionOperationReport{
			Schema:     ExtractionOperationSchema,
			Repository: job.Target,
			Attempt: ExtractionOperationAttempt{
				JobID: job.ID, Number: job.Attempts + 1, Force: job.Force,
			},
			QueueWaitMS: durationMilliseconds(queueWait),
		},
	}
}

func (operation *operationRecorder) registerDomains(
	extractors []registeredExtractor,
	scheduling domainSchedulingLimits,
) {
	operation.mu.Lock()
	defer operation.mu.Unlock()
	operation.domains = make([]*domainOperationRecorder, len(extractors))
	for index, extractor := range extractors {
		operation.domains[index] = &domainOperationRecorder{
			now: operation.now,
			report: ExtractionOperationDomain{
				Domain: extractor.domain, ExtractorVersion: extractor.version,
				Limits: ExtractionOperationDomainLimits{
					CorpusFiles:            maxCorpusFiles,
					OpenedSourceAttempts:   maxCorpusFiles * 4,
					OpenedSourceFiles:      maxCorpusFiles,
					OpenedSourceBytes:      maxCorpusRunBytes,
					Facts:                  maxFactsPerRun,
					SourceBlobBytes:        MaxBlobBytes,
					TypedInputBytes:        MaxSCIPIndexBytes,
					AggregateWallMS:        durationMilliseconds(scheduling.AggregateTimeout),
					MirrorLockMS:           durationMilliseconds(scheduling.MirrorTimeout),
					DomainWallMS:           durationMilliseconds(scheduling.DomainTimeout),
					AbortWallMS:            durationMilliseconds(scheduling.AbortTimeout),
					OutcomeWallMS:          durationMilliseconds(scheduling.OutcomeTimeout),
					MaxSerialDomains:       scheduling.MaxSerialDomains,
					SchedulerIdentityBytes: scheduling.MaxSchedulerBytes,
					AggregateStagedRows:    scheduling.MaxStagedRows,
					DomainStagedRows:       scheduling.MaxDomainStagedRows,
				},
			},
		}
	}
}

func (operation *operationRecorder) domain(
	index int,
) *domainOperationRecorder {
	operation.mu.Lock()
	defer operation.mu.Unlock()
	if index < 0 || index >= len(operation.domains) {
		return nil
	}
	return operation.domains[index]
}

func (operation *operationRecorder) bindRepository(repo *store.Repo) {
	if repo == nil {
		return
	}
	operation.mu.Lock()
	defer operation.mu.Unlock()
	operation.report.Repository = repo.Name
	operation.report.IndexedHead = repo.IndexedCommitHash
	if repo.IndexedAnalysisUnit != nil {
		operation.report.UnitDigest = repo.IndexedAnalysisUnit.Digest
	}
}

func (operation *operationRecorder) bindPolicy(digest string) {
	operation.mu.Lock()
	operation.report.PolicyDigest = digest
	operation.mu.Unlock()
}

func (operation *operationRecorder) bindManifest(digest string) {
	operation.mu.Lock()
	operation.report.CandidateManifestDigest = digest
	operation.mu.Unlock()
}

func (operation *operationRecorder) bindControlRevision(revision uint64) {
	operation.mu.Lock()
	operation.report.CandidateControlRevision = revision
	operation.mu.Unlock()
}

func (operation *operationRecorder) addMirrorLockWait(elapsed time.Duration) {
	operation.mu.Lock()
	operation.report.MirrorLockWaitMS += durationMilliseconds(elapsed)
	operation.mu.Unlock()
}

func (operation *operationRecorder) addPointerWork(elapsed time.Duration) {
	operation.mu.Lock()
	operation.report.PointerWorkMS += durationMilliseconds(elapsed)
	operation.mu.Unlock()
}

func (operation *operationRecorder) addStrictOpen(elapsed time.Duration) {
	operation.mu.Lock()
	operation.report.StrictOpenMS += durationMilliseconds(elapsed)
	operation.mu.Unlock()
}

func (operation *operationRecorder) completeRemaining(reason string) {
	operation.mu.Lock()
	domains := append([]*domainOperationRecorder(nil), operation.domains...)
	operation.mu.Unlock()
	for _, domain := range domains {
		domain.completeIfEmpty(reason)
	}
}

func (operation *operationRecorder) snapshot() ExtractionOperationReport {
	operation.mu.Lock()
	report := operation.report
	report.TotalMS = durationMilliseconds(
		nonnegativeDuration(operation.started, operation.now()),
	)
	domains := append([]*domainOperationRecorder(nil), operation.domains...)
	operation.mu.Unlock()
	report.Domains = make([]ExtractionOperationDomain, len(domains))
	for index, domain := range domains {
		report.Domains[index] = domain.snapshot()
	}
	return report
}

func (domain *domainOperationRecorder) started() time.Time {
	if domain == nil {
		return time.Time{}
	}
	return domain.now()
}

func (domain *domainOperationRecorder) elapsed(started time.Time) time.Duration {
	if domain == nil || started.IsZero() {
		return 0
	}
	return nonnegativeDuration(started, domain.now())
}

func (domain *domainOperationRecorder) addInventory(elapsed time.Duration) {
	if domain == nil {
		return
	}
	domain.addDuration(&domain.report.InventoryMS, elapsed)
}

func (domain *domainOperationRecorder) addOpenedSource(elapsed time.Duration) {
	if domain == nil {
		return
	}
	domain.addDuration(&domain.report.OpenedSourceMS, elapsed)
}

func (domain *domainOperationRecorder) addExtractor(elapsed time.Duration) {
	if domain == nil {
		return
	}
	domain.addDuration(&domain.report.ExtractorMS, elapsed)
}

func (domain *domainOperationRecorder) addStaging(elapsed time.Duration) {
	if domain == nil {
		return
	}
	domain.addDuration(&domain.report.StagingMS, elapsed)
}

func (domain *domainOperationRecorder) addPublication(elapsed time.Duration) {
	if domain == nil {
		return
	}
	domain.addDuration(&domain.report.PublicationMS, elapsed)
}

func (domain *domainOperationRecorder) addAbort(elapsed time.Duration) {
	if domain == nil {
		return
	}
	domain.addDuration(&domain.report.AbortMS, elapsed)
}

func (domain *domainOperationRecorder) addCleanup(elapsed time.Duration) {
	if domain == nil {
		return
	}
	domain.addDuration(&domain.report.CleanupMS, elapsed)
}

func (domain *domainOperationRecorder) setStagedRowLimit(limit int) {
	if domain == nil || limit <= 0 {
		return
	}
	domain.mu.Lock()
	domain.report.Limits.DomainStagedRows = limit
	domain.mu.Unlock()
}

func (domain *domainOperationRecorder) addDuration(
	field *int64,
	elapsed time.Duration,
) {
	if domain == nil {
		return
	}
	domain.mu.Lock()
	*field += durationMilliseconds(elapsed)
	domain.mu.Unlock()
}

func (domain *domainOperationRecorder) capture(
	corpus *verifiedCorpus,
	sink *runSink,
) {
	if domain == nil {
		return
	}
	var corpusSnapshot verifiedCorpusOperationSnapshot
	if corpus != nil {
		corpusSnapshot = corpus.operationSnapshot()
	}
	var sinkSnapshot runSinkOperationSnapshot
	if sink != nil {
		sinkSnapshot = sink.operationSnapshot()
	}
	domain.mu.Lock()
	domain.report.Counts.CorpusFiles = corpusSnapshot.corpusFiles
	domain.report.Counts.CandidateFiles = corpusSnapshot.candidateFiles
	domain.report.Counts.ExcludedSourceFiles = corpusSnapshot.excludedFiles
	domain.report.Counts.OpenedSourceAttempts = corpusSnapshot.readAttempts
	domain.report.Counts.OpenedSourceFiles = corpusSnapshot.readFiles
	domain.report.Counts.Facts = sinkSnapshot.facts
	domain.report.Counts.Atoms = sinkSnapshot.atoms
	domain.report.Counts.Assertions = sinkSnapshot.assertions
	domain.report.Counts.Unresolved = sinkSnapshot.unresolved
	domain.report.Counts.StagedChunks = sinkSnapshot.stagedChunks
	domain.report.Counts.StagedRows = sinkSnapshot.stagedRows
	domain.report.Bytes.PlannedDeclared = corpusSnapshot.plannedBytes
	domain.report.Bytes.ExcludedSourceDeclared = corpusSnapshot.excludedBytes
	domain.report.Bytes.OpenedSource = corpusSnapshot.readBytes
	domain.mu.Unlock()
}

func (domain *domainOperationRecorder) captureCoverage(
	coverage sdk.Coverage,
	includeExtractorDetails bool,
) {
	if domain == nil {
		return
	}
	domain.mu.Lock()
	domain.report.Counts.ExcludedSCIPDocuments =
		coverage.ExcludedSCIPDocuments
	domain.report.Counts.ExcludedSCIPDefinitions =
		coverage.ExcludedSCIPDefinitions
	domain.report.Counts.ExcludedSCIPOccurrences =
		coverage.ExcludedSCIPOccurrences
	if includeExtractorDetails {
		domain.report.ExtractorDiagnostics = boundedDiagnosticCounters(
			coverage.Diagnostics,
		)
	}
	domain.mu.Unlock()
}

func boundedDiagnosticCounters(
	counters []sdk.DiagnosticCounter,
) []sdk.DiagnosticCounter {
	const maxCounters = 32
	if len(counters) == 0 || len(counters) > maxCounters {
		return nil
	}
	result := make([]sdk.DiagnosticCounter, 0, len(counters))
	seen := make(map[string]struct{}, len(counters))
	for _, counter := range counters {
		if counter.Value < 0 || !validDiagnosticCounterName(counter.Name) {
			return nil
		}
		if _, duplicate := seen[counter.Name]; duplicate {
			return nil
		}
		seen[counter.Name] = struct{}{}
		result = append(result, counter)
	}
	return result
}

func validDiagnosticCounterName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for index, current := range []byte(name) {
		lowercase := current >= 'a' && current <= 'z'
		digit := index > 0 && current >= '0' && current <= '9'
		underscore := index > 0 && current == '_'
		if lowercase || digit || underscore {
			continue
		}
		return false
	}
	return true
}

func (domain *domainOperationRecorder) complete(reason string) {
	if domain == nil || !validOperationReason(reason) {
		return
	}
	domain.mu.Lock()
	domain.report.Reason = reason
	domain.mu.Unlock()
}

func (domain *domainOperationRecorder) completeIfEmpty(reason string) {
	if domain == nil || !validOperationReason(reason) {
		return
	}
	domain.mu.Lock()
	if domain.report.Reason == "" {
		domain.report.Reason = reason
	}
	domain.mu.Unlock()
}

func (domain *domainOperationRecorder) snapshot() ExtractionOperationDomain {
	if domain == nil {
		return ExtractionOperationDomain{}
	}
	domain.mu.Lock()
	defer domain.mu.Unlock()
	return domain.report
}

func encodeExtractionDomainOutcomeReceipt(
	domain *domainOperationRecorder,
	disposition store.DomainOutcomeDisposition,
) string {
	report := domain.snapshot()
	receipt := ExtractionDomainOutcomeReceipt{
		Schema:           store.ExtractionOutcomeReceiptSchema,
		Domain:           report.Domain,
		ExtractorVersion: report.ExtractorVersion,
		Disposition:      disposition,
		Reason:           report.Reason,
		InventoryMS:      report.InventoryMS,
		OpenedSourceMS:   report.OpenedSourceMS,
		ExtractorMS:      report.ExtractorMS,
		StagingMS:        report.StagingMS,
		Counts:           report.Counts,
		Bytes:            report.Bytes,
		Limits:           report.Limits,
	}
	data, err := json.Marshal(receipt)
	if err == nil && len(data) <= store.MaxExtractionOutcomeReceiptBytes {
		return string(data)
	}
	// Every field above is scalar or a fixed struct, so this path is defensive
	// against a future accidental expansion. Keep publication independent of
	// diagnostics with a tiny valid schema-bearing fallback.
	return `{"schema":"` + store.ExtractionOutcomeReceiptSchema + `"}`
}

func validOperationReason(reason string) bool {
	switch reason {
	case OperationReasonAlreadyCurrent, OperationReasonNotReady,
		OperationReasonStale, OperationReasonNoCandidates,
		OperationReasonTypedInputAbsent, OperationReasonLimitRefusal,
		OperationReasonPublishedEmpty, OperationReasonPublishedNonempty,
		OperationReasonAggregateBudget, OperationReasonDomainBudget,
		OperationReasonCanceled, OperationReasonFailed:
		return true
	default:
		return false
	}
}

func operationReasonForError(err error) string {
	switch {
	case err == nil:
		return OperationReasonFailed
	case errors.Is(err, errExtractionAggregateBudget):
		return OperationReasonAggregateBudget
	case errors.Is(err, errExtractionDomainBudget):
		return OperationReasonDomainBudget
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return OperationReasonCanceled
	case errors.Is(err, errStaleRun):
		return OperationReasonStale
	case isOperationLimitRefusal(err):
		return OperationReasonLimitRefusal
	default:
		return OperationReasonFailed
	}
}

func operationReason(err error) string {
	reason := operationReasonForError(err)
	if reason != OperationReasonFailed || err == nil {
		return reason
	}
	if errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, candidatepkg.ErrPublishing) {
		return OperationReasonNotReady
	}
	return OperationReasonFailed
}

func successfulOperationReason(stats corpusStats, facts int) string {
	switch {
	case facts > 0:
		return OperationReasonPublishedNonempty
	case stats.typedInputKind != "" && !stats.typedInputPresent:
		return OperationReasonTypedInputAbsent
	case stats.candidateFileCount == 0:
		return OperationReasonNoCandidates
	default:
		return OperationReasonPublishedEmpty
	}
}

func isOperationLimitRefusal(err error) bool {
	return errors.Is(err, errOperationLimitRefusal) ||
		errors.Is(err, gitobj.ErrTooLarge) ||
		errors.Is(err, codenav.ErrSemanticLimit) ||
		errors.Is(err, codenav.ErrHoverTooLarge) ||
		errors.Is(err, candidatepkg.ErrCorpusTooLarge) ||
		errors.Is(err, candidatepkg.ErrCandidateTooLarge) ||
		errors.Is(err, compat.ErrLimit)
}

var errOperationLimitRefusal = errors.New("extraction limit refusal")

var (
	extractionOperationDurationBuckets = prometheus.ExponentialBuckets(
		0.1, 2, 15, // 100ms .. ~27.3min, beyond the 15-minute extraction budget.
	)
	extractionOperationsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "phebs_extraction_operations_total",
		Help: "Repository extraction jobs that emitted an operational receipt.",
	})
	extractionOperationDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "phebs_extraction_operation_duration_seconds",
			Help:    "Wall time of repository extraction jobs.",
			Buckets: extractionOperationDurationBuckets,
		},
	)
	extractionOperationDomainsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "phebs_extraction_operation_domains_total",
			Help: "Extraction domain outcomes by frozen generic reason.",
		},
		[]string{"reason"},
	)
)

func operationLimitError(message string) error {
	return fmt.Errorf("%w: %s", errOperationLimitRefusal, message)
}

func encodeExtractionOperation(
	report ExtractionOperationReport,
) ([]byte, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	if len(data) <= MaxExtractionOperationSize {
		return data, nil
	}
	minimal := extractionOperationMinimal{
		Schema: report.Schema, Repository: report.Repository,
		IndexedHead: report.IndexedHead, UnitDigest: report.UnitDigest,
		CandidateManifestDigest:  report.CandidateManifestDigest,
		CandidateControlRevision: report.CandidateControlRevision,
		PolicyDigest:             report.PolicyDigest, Attempt: report.Attempt,
		DomainCount: len(report.Domains), Truncated: true,
	}
	data, err = json.Marshal(minimal)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxExtractionOperationSize {
		return nil, errors.New("minimal extraction operation exceeds report cap")
	}
	return data, nil
}

func (worker *Worker) emitOperationReport(report ExtractionOperationReport) {
	extractionOperationsTotal.Inc()
	extractionOperationDuration.Observe(
		float64(report.TotalMS) / float64(time.Second/time.Millisecond),
	)
	for _, domain := range report.Domains {
		if validOperationReason(domain.Reason) {
			extractionOperationDomainsTotal.WithLabelValues(domain.Reason).Inc()
		}
	}
	if !worker.Diagnostics && worker.OperationReports == nil {
		return
	}
	data, err := encodeExtractionOperation(report)
	if err != nil {
		diagnostics.Logf("encode extraction operation: %v", err)
		return
	}
	sink := worker.OperationReports
	if sink == nil {
		sink = func(report []byte) error {
			diagnostics.Logf("extraction operation: %s", report)
			return nil
		}
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				diagnostics.Logf("extraction operation sink panic (%T)", recovered)
			}
		}()
		if err := sink(data); err != nil {
			diagnostics.Logf("extraction operation sink error (%T)", err)
		}
	}()
}

func durationMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}

func nonnegativeDuration(start, end time.Time) time.Duration {
	if start.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

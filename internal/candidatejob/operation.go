package candidatejob

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	CandidateOperationSchema  = "phebs-candidate-operation-v1"
	MaxCandidateOperationSize = 32 << 10
)

type CandidateOperationSink func(report []byte) error

type CandidateOperationAttempt struct {
	JobID  string `json:"job_id,omitempty"`
	Number int    `json:"number"`
	Force  bool   `json:"force,omitempty"`
}

type CandidatePlaneSummary struct {
	Records        int   `json:"records"`
	Members        int   `json:"members"`
	CanonicalBytes int64 `json:"canonical_bytes"`
	DeclaredBytes  int64 `json:"declared_bytes"`
}

type CandidateOperationPlanes struct {
	Repository CandidatePlaneSummary `json:"repository"`
	Local      CandidatePlaneSummary `json:"local"`
	Caller     CandidatePlaneSummary `json:"caller"`
}

type CandidateTypedInputSummary struct {
	Configured    int   `json:"configured"`
	Present       int   `json:"present"`
	DeclaredBytes int64 `json:"declared_bytes"`
}

// CandidateOperationReport is one bounded, source-free planning receipt.
// Canonical counts come from the manifest already built or strictly reused;
// no reporting path opens a member or scans a directory.
type CandidateOperationReport struct {
	Schema                 string                     `json:"schema"`
	Repository             string                     `json:"repository"`
	Commit                 string                     `json:"commit,omitempty"`
	UnitDigest             string                     `json:"unit_digest,omitempty"`
	PolicyDigest           string                     `json:"policy_digest,omitempty"`
	GenerationDigest       string                     `json:"generation_digest,omitempty"`
	ManifestDigest         string                     `json:"manifest_digest,omitempty"`
	ControlRevision        uint64                     `json:"control_revision,omitempty"`
	Attempt                CandidateOperationAttempt  `json:"attempt"`
	Decision               string                     `json:"decision"`
	Outcome                string                     `json:"outcome"`
	QueueWaitMS            int64                      `json:"queue_wait_ms"`
	MirrorLockWaitMS       int64                      `json:"mirror_lock_wait_ms"`
	TreeMS                 int64                      `json:"tree_ms"`
	SpoolingMS             int64                      `json:"spooling_ms"`
	ExternalSortMS         int64                      `json:"external_sort_ms"`
	PeakSpoolBytes         int64                      `json:"peak_spool_bytes"`
	PublishMS              int64                      `json:"publish_ms"`
	FingerprintMS          int64                      `json:"fingerprint_ms"`
	DatabaseCommitMS       int64                      `json:"database_commit_ms"`
	MarkerFinishMS         int64                      `json:"marker_finish_ms"`
	TotalMS                int64                      `json:"total_ms"`
	DeclaredSourceBytes    int64                      `json:"declared_source_bytes"`
	Planes                 CandidateOperationPlanes   `json:"planes"`
	TypedInput             CandidateTypedInputSummary `json:"typed_input"`
	ManifestSummaryPresent bool                       `json:"manifest_summary_present"`
}

type candidateOperationRecorder struct {
	enabled bool
	now     func() time.Time
	started time.Time
	report  CandidateOperationReport
}

func newCandidateOperation(job store.Job, now func() time.Time) *candidateOperationRecorder {
	if now == nil {
		now = time.Now
	}
	return &candidateOperationRecorder{
		enabled: true, now: now, started: now(),
		report: CandidateOperationReport{
			Schema: CandidateOperationSchema, Repository: job.Target,
			Decision: "preflight",
			Attempt: CandidateOperationAttempt{
				JobID: job.ID, Number: job.Attempts + 1, Force: job.Force,
			},
			QueueWaitMS: candidateQueueWaitMS(job),
		},
	}
}

func (operation *candidateOperationRecorder) snapshot(outcome string) CandidateOperationReport {
	report := operation.report
	report.Outcome = outcome
	report.TotalMS = candidateDurationMS(operation.now().Sub(operation.started))
	return report
}

func (operation *candidateOperationRecorder) timestamp() time.Time {
	if operation == nil || !operation.enabled || operation.now == nil {
		return time.Time{}
	}
	return operation.now()
}

func (operation *candidateOperationRecorder) bindRepository(repo *store.Repo) {
	if operation == nil || !operation.enabled || repo == nil {
		return
	}
	operation.report.Repository = repo.Name
	operation.report.Commit = repo.IndexedCommitHash
	if repo.IndexedAnalysisUnit != nil {
		operation.report.UnitDigest = repo.IndexedAnalysisUnit.Digest
	}
}

func (operation *candidateOperationRecorder) bindExpected(expected candidate.Expected) {
	if operation == nil || !operation.enabled {
		return
	}
	operation.report.PolicyDigest = expected.PolicyDigest
	operation.report.GenerationDigest = expected.GenerationDigest
	operation.report.ManifestDigest = expected.ManifestDigest
}

func (operation *candidateOperationRecorder) bindManifest(manifest candidate.Manifest) {
	if operation == nil || !operation.enabled {
		return
	}
	operation.report.ManifestDigest = manifest.Digest
	operation.report.ManifestSummaryPresent = true
	operation.report.DeclaredSourceBytes = manifest.Corpus.RegularDeclaredBytes
	operation.report.Planes.Repository = summarizeArtifacts(manifest.RepositoryMembers)
	for _, projection := range manifest.LocalProjections {
		addPlaneSummary(&operation.report.Planes.Local, summarizeArtifacts(projection.Members))
	}
	caller := make([]candidate.Artifact, len(manifest.CallerLeaves))
	for index, leaf := range manifest.CallerLeaves {
		caller[index] = leaf.Artifact
	}
	operation.report.Planes.Caller = summarizeArtifacts(caller)
	operation.report.TypedInput.Configured = len(manifest.TypedInputs)
	for _, input := range manifest.TypedInputs {
		if input.Present {
			operation.report.TypedInput.Present++
			operation.report.TypedInput.DeclaredBytes += input.DeclaredBytes
		}
	}
}

func summarizeArtifacts(artifacts []candidate.Artifact) CandidatePlaneSummary {
	result := CandidatePlaneSummary{Members: len(artifacts)}
	for _, artifact := range artifacts {
		result.Records += artifact.RecordCount
		result.CanonicalBytes += artifact.ContentBytes
		result.DeclaredBytes += artifact.DeclaredBytes
	}
	return result
}

func addPlaneSummary(destination *CandidatePlaneSummary, current CandidatePlaneSummary) {
	destination.Records += current.Records
	destination.Members += current.Members
	destination.CanonicalBytes += current.CanonicalBytes
	destination.DeclaredBytes += current.DeclaredBytes
}

func candidateQueueWaitMS(job store.Job) int64 {
	if job.ClaimedAt == nil || job.CreatedAt.IsZero() {
		return 0
	}
	eligible := job.CreatedAt
	if job.NotBefore != nil && job.NotBefore.After(eligible) {
		eligible = *job.NotBefore
	}
	if job.ClaimedAt.Before(eligible) {
		return 0
	}
	return candidateDurationMS(job.ClaimedAt.Sub(eligible))
}

func candidateDurationMS(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}

func (worker *Worker) emitOperation(report CandidateOperationReport) (result error) {
	if worker == nil || (!worker.Diagnostics && worker.OperationReports == nil &&
		worker.OperationReportFailure == nil) {
		return nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		diagnostics.Logf("encode candidate operation: %v", err)
		return worker.failOperationReport(errors.New("encode candidate operation"))
	}
	if len(data) > MaxCandidateOperationSize {
		diagnostics.Logf("encode candidate operation: report exceeds %d bytes", MaxCandidateOperationSize)
		return worker.failOperationReport(errors.New("candidate operation report exceeds its bound"))
	}
	sink := worker.OperationReports
	if sink == nil {
		sink = func(report []byte) error {
			diagnostics.Logf("candidate operation: %s", report)
			return nil
		}
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				diagnostics.Logf("candidate operation sink panic (%T)", recovered)
				result = worker.failOperationReport(errors.New("candidate operation sink panicked"))
			}
		}()
		if err := sink(data); err != nil {
			diagnostics.Logf("candidate operation sink error (%T)", err)
			result = worker.failOperationReport(errors.New("candidate operation sink failed"))
		}
	}()
	return result
}

func (worker *Worker) failOperationReport(err error) (result error) {
	if worker == nil || worker.OperationReportFailure == nil || err == nil {
		return nil
	}
	result = err
	defer func() {
		if recovered := recover(); recovered != nil {
			diagnostics.Logf("candidate operation failure callback panic (%T)", recovered)
			result = errors.New("candidate operation failure callback panicked")
		}
	}()
	worker.OperationReportFailure(err)
	return result
}

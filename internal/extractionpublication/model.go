// Package extractionpublication executes bounded extraction partitions and
// installs one complete T40.9 domain root as the only current authority.
package extractionpublication

import (
	"context"
	"errors"
	"fmt"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	ScheduleStage            = "extraction-partitions"
	ScheduleChunkItems       = 1
	ScheduleMaxAttempts      = 5
	ScheduleRepositoryTokens = 1
	ScheduleClassConcurrency = 2

	GenerationSchema       = "phebs-extraction-partition-generation-v1"
	DomainSchema           = "phebs-extraction-partition-domain-v1"
	PointerSchema          = "phebs-extraction-domain-pointer-v1"
	BindingSchema          = "phebs-extraction-partition-schedule-binding-v1"
	CompletionSchema       = "phebs-extraction-partition-completion-v1"
	AuthorityBindingSchema = "phebs-extraction-authority-binding-v1"

	MaxGenerationControlBytes int64 = 64 << 10
	MaxPointerBytes           int64 = 8 << 10
	MaxCompletionBytes        int64 = 4 << 10
	MaxDomains                      = 64
)

var (
	ErrInvalid = errors.New("invalid partitioned extraction publication")
	ErrStale   = errors.New("partitioned extraction publication is stale")
	ErrLimit   = errors.New("partitioned extraction publication limit exceeded")
)

// DomainPlan binds one pre-admitted T40.9 plan to the invisible evidence run
// that receives its T40.7 chunks. RunID is opaque and is never authority by
// itself; Publisher closes it under the validated root.
type DomainPlan struct {
	Schema string                     `json:"schema"`
	RunID  string                     `json:"run_id"`
	Plan   candidate.DomainResultPlan `json:"plan"`
}

// Generation is the small, source-free execution inventory. Plans live in
// separate bounded files; this control carries only their identities and
// global schedule ranges.
type Generation struct {
	Schema     string             `json:"schema"`
	Repository string             `json:"repository"`
	Digest     string             `json:"digest"`
	Domains    []DomainDescriptor `json:"domains"`
	WorkItems  int                `json:"work_items"`
}

type DomainDescriptor struct {
	Domain       string `json:"domain"`
	Version      string `json:"version"`
	PlanDigest   string `json:"plan_digest"`
	PlanName     string `json:"plan_name"`
	RunID        string `json:"run_id"`
	StartOrdinal int    `json:"start_ordinal"`
	EndOrdinal   int    `json:"end_ordinal"`
}

type Pointer struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	Domain           string `json:"domain"`
	GenerationDigest string `json:"generation_digest"`
	PlanDigest       string `json:"plan_digest"`
	RootDigest       string `json:"root_digest"`
	RootName         string `json:"root_name"`
}

// completionControl is mutable non-authority progress. One bit per expected
// result avoids rereading the settled prefix after each completion; the final
// root builder still validates every result exactly once.
type completionControl struct {
	Schema     string `json:"schema"`
	PlanDigest string `json:"plan_digest"`
	Expected   int    `json:"expected"`
	Count      int    `json:"count"`
	Bits       []byte `json:"bits"`
}

// PlanningAuthority is the small pointer-level identity needed to recognize
// an exact durable execution generation before reopening corpus-wide controls.
type PlanningAuthority struct {
	Repository                  string `json:"repository"`
	CandidateManifestDigest     string `json:"candidate_manifest_digest"`
	CandidateGenerationDigest   string `json:"candidate_generation_digest"`
	CandidatePolicyDigest       string `json:"candidate_policy_digest"`
	SourceGenerationDigest      string `json:"source_generation_digest"`
	ObservationGenerationDigest string `json:"observation_generation_digest"`
}

type authorityBinding struct {
	Schema    string            `json:"schema"`
	Authority PlanningAuthority `json:"authority"`
	Target    string            `json:"target"`
}

type scheduleBinding struct {
	Schema             string `json:"schema"`
	Repository         string `json:"repository"`
	ScheduleGeneration string `json:"schedule_generation"`
	TargetGeneration   string `json:"target_generation"`
	PriorSchedule      string `json:"prior_schedule,omitempty"`
}

// PartitionLease is the only content capability passed to an executor. The
// source owns its concrete representation and must constrain it to exactly
// one T40.8 partition. Release is always called.
type PartitionLease interface {
	Corpus() sdk.Corpus
	CandidateRecords() int
	TypedSourceRecords() int
	TypedInput() *candidate.SparseTypedInput
	MemberBytes() int64
	Members() int64
	Release()
}

// Source opens one selected partition after the complete reservation set was
// admitted. It must not inspect or lease content during Reconcile.
type Source interface {
	AcquirePartition(
		context.Context,
		candidate.DomainResultPlan,
		int,
	) (PartitionLease, error)
}

// Executor stages evidence through the T40.7 chunk path and returns only the
// bounded result contribution. It must treat the reservation as a hard cap.
type Executor interface {
	ExecutePartition(
		context.Context,
		candidate.DomainResultPlan,
		int,
		PartitionLease,
		string,
	) (candidate.PartitionResultSpec, error)
}

// Publisher performs the authoritative evidence/root transition. The call is
// idempotent for an exact plan/root/run triple and must refuse a stale upstream
// generation. Filesystem current.json is written only after this succeeds.
type Publisher interface {
	PublishDomain(
		context.Context,
		candidate.DomainResultPlan,
		candidate.DomainResultRoot,
		string,
	) error
}

// Fence re-proves source, observation, candidate, and schedule authority
// under the caller's short publication lock.
type Fence interface {
	FenceDomain(
		context.Context,
		FenceRequest,
	) (release func(), err error)
}

type FenceRequest struct {
	Chunk store.GenerationChunk
	Plan  candidate.DomainResultPlan
}

type Status struct {
	Repository string         `json:"repository"`
	Generation string         `json:"generation"`
	Domains    []DomainStatus `json:"domains"`
}

type DomainStatus struct {
	Domain         string `json:"domain"`
	PlanDigest     string `json:"plan_digest"`
	RootDigest     string `json:"root_digest,omitempty"`
	Disposition    string `json:"disposition,omitempty"`
	Current        bool   `json:"current"`
	Expected       int    `json:"expected"`
	Settled        int    `json:"settled"`
	Failed         int    `json:"failed"`
	RetryExhausted int    `json:"retry_exhausted"`
}

// Progress is the bounded operational projection of the current extraction
// schedule. It reads two schedule rows, one small generation control, and at
// most MaxDomains current pointers; it never opens partition results, candidate
// members, source content, or evidence rows.
type ExtractionProgress struct {
	State          string `json:"state"`
	Total          int    `json:"total_partitions"`
	Materialized   int    `json:"materialized"`
	Pending        int    `json:"pending"`
	Running        int    `json:"running"`
	Succeeded      int    `json:"succeeded"`
	Failed         int    `json:"failed"`
	Domains        int    `json:"domains"`
	CurrentDomains int    `json:"current_domains"`
}

// Progress keeps the internal reader name concise. The defined wire type is
// deliberately named ExtractionProgress so Huma gives it a distinct OpenAPI
// component from observationpublication.Progress.
type Progress = ExtractionProgress

func ValidateProgress(value Progress) error {
	if value.State == "unavailable" {
		if value != (Progress{State: "unavailable"}) {
			return invalid("unavailable progress")
		}
		return nil
	}
	if value.State != string(store.GenerationScheduleActive) &&
		value.State != string(store.GenerationScheduleSuperseded) &&
		value.State != string(store.GenerationScheduleSettled) && value.State != "current" {
		return invalid("progress state")
	}
	if value.Total <= 0 || value.Total > MaxDomains*candidate.MaxDomainResultPartitions ||
		value.Materialized < 0 || value.Materialized > value.Total*ScheduleMaxAttempts || value.Pending < 0 ||
		value.Running < 0 || value.Succeeded < 0 || value.Failed < 0 ||
		value.Pending+value.Running > value.Materialized ||
		value.Succeeded+value.Failed > value.Total ||
		value.Domains <= 0 || value.Domains > MaxDomains ||
		value.CurrentDomains < 0 || value.CurrentDomains > value.Domains {
		return invalid("progress counts")
	}
	if value.State == "current" && (value.Materialized < value.Total || value.Pending != 0 ||
		value.Running != 0 || value.Failed != 0 || value.Succeeded != value.Total ||
		value.CurrentDomains != value.Domains) {
		return invalid("current progress")
	}
	return nil
}

// RetryFailureReason is deliberately closed and source-free.
const RetryFailureReason = "attempts_exhausted"

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalid, message) }

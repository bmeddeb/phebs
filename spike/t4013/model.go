// Package t4013 defines the source-free T40.13 convergence plan and receipt.
// Production packages must not import spike packages.
package t4013

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	PlanSchema                 = "t4013-neutral-convergence-plan-v1"
	PlanSchemaV2               = "t4013-neutral-convergence-plan-v2"
	PlanSchemaV3               = "t4013-neutral-convergence-plan-v3"
	PlanSchemaV4               = "t4013-neutral-convergence-plan-v4"
	PlanSchemaV5               = "t4013-neutral-convergence-plan-v5"
	PlanSchemaV6               = "t4013-neutral-convergence-plan-v6"
	PlanSchemaV7               = "t4013-neutral-convergence-plan-v7"
	PlanSchemaV8               = "t4013-neutral-convergence-plan-v8"
	PlanSchemaV9               = "t4013-neutral-convergence-plan-v9"
	PlanSchemaV10              = "t4013-neutral-convergence-plan-v10"
	ObservationSchema          = "t4013-neutral-convergence-observation-v1"
	ObservationSchemaV2        = "t4013-neutral-convergence-observation-v2"
	ObservationSchemaV3        = "t4013-neutral-convergence-observation-v3"
	ObservationSchemaV4        = "t4013-neutral-convergence-observation-v4"
	ObservationSchemaV5        = "t4013-neutral-convergence-observation-v5"
	ObservationSchemaV6        = "t4013-neutral-convergence-observation-v6"
	ObservationSchemaV7        = "t4013-neutral-convergence-observation-v7"
	ObservationSchemaV8        = "t4013-neutral-convergence-observation-v8"
	ObservationSchemaV9        = "t4013-neutral-convergence-observation-v9"
	ObservationSchemaV10       = "t4013-neutral-convergence-observation-v10"
	ReceiptSchema              = "t4013-neutral-convergence-receipt-v1"
	ReceiptSchemaV2            = "t4013-neutral-convergence-receipt-v2"
	ReceiptSchemaV3            = "t4013-neutral-convergence-receipt-v3"
	ReceiptSchemaV4            = "t4013-neutral-convergence-receipt-v4"
	ReceiptSchemaV5            = "t4013-neutral-convergence-receipt-v5"
	ReceiptSchemaV6            = "t4013-neutral-convergence-receipt-v6"
	ReceiptSchemaV7            = "t4013-neutral-convergence-receipt-v7"
	ReceiptSchemaV8            = "t4013-neutral-convergence-receipt-v8"
	ReceiptSchemaV9            = "t4013-neutral-convergence-receipt-v9"
	ReceiptSchemaV10           = "t4013-neutral-convergence-receipt-v10"
	MaxPlanBytes               = 64 << 10
	MaxObservationBytes        = 256 << 10
	MaxReceiptBytes            = 256 << 10
	legacyStoppedPlanDigest    = "sha256:13863ed6e0e19e3edf5cbaa2e6d2f79eef645341661a5d61c0066f7f009974a0"
	legacyStoppedSourceCommit  = "b1b4e808e1987b3bf28e4afac21cc83b72aa27f2"
	legacyStoppedReceiptDigest = "sha256:873c373353c540d05e61b243b63befd781e7280b4ec52c0ddd4ef074661e4c85"
	maxConvergenceTransitions  = 32
	convergenceNotInspected    = "not_inspected"
)

const (
	httpReason409Stale         = "409_stale"
	httpReason409ControlAbsent = "409_control_absent"
	httpReason500Store         = "500_store"
	httpReason500Projection    = "500_projection"
	httpReason500Control       = "500_projection_control"
	httpReason500Publication   = "500_projection_publication"
	httpReason500Planning      = "500_projection_planning"
	httpReason500Schedule      = "500_projection_schedule"
	httpReason500Response      = "500_projection_response"
	httpReason401Unauthorized  = "401_unauthorized"
	httpReason403Forbidden     = "403_forbidden"
	httpReason404NotFound      = "404_not_found"
	httpReason503Unavailable   = "503_unavailable"
	httpReasonOther            = "status_other"
)

var phaseOrder = []string{
	"preflight", "cold", "warm_noop", "delta_b", "return_a", "interruption",
	"stale_worker", "pressure", "archive_restore", "collection", "authorized_query", "teardown",
}

var checkNames = []string{
	"physical_owner_search_publication",
	"effective_blob_reader_receipts",
	"derived_partition_settlement",
	"explicit_absence_and_unsupported_syntax",
	"cold_exact_oracle",
	"warm_noop_reuse",
	"one_partition_delta",
	"a_to_b_to_a_identity",
	"interruption_recovery",
	"stale_worker_fence",
	"pressure_admission_and_collection",
	"archive_restore_authority",
	"bounded_collection",
	"authorization_first_queries",
	"small_service_control",
	"phase_accounting_complete",
}

var failureClasses = []string{
	"environment", "admission", "search", "pipeline", "recovery", "lifecycle", "archive_restore", "authorization", "oracle", "execution",
}

type Plan struct {
	Schema        string                `json:"schema"`
	FrozenOn      string                `json:"frozen_on"`
	SourceCommit  string                `json:"source_commit"`
	HostToolchain []HostToolObservation `json:"host_toolchain,omitempty"`
	Inputs        []InputBinding        `json:"inputs"`
	PhaseOrder    []string              `json:"phase_order"`
	Safety        SafetyEnvelope        `json:"safety_envelope"`
	StopRules     []StopRule            `json:"stop_rules"`
	Claims        PlanClaims            `json:"claims"`
}

type InputBinding struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
	SHA256 string `json:"sha256"`
}

type SafetyEnvelope struct {
	MinimumMemoryBytes          int64 `json:"minimum_memory_bytes"`
	MinimumAvailableDiskBytes   int64 `json:"minimum_available_disk_bytes"`
	MaximumTotalWallMS          int64 `json:"maximum_total_wall_ms"`
	MaximumPeakRSSBytes         int64 `json:"maximum_peak_rss_bytes"`
	MaximumDataAllocatedBytes   int64 `json:"maximum_data_allocated_bytes"`
	MaximumRetriesPerUnit       int   `json:"maximum_retries_per_unit"`
	ServerHealthDeadlineMS      int64 `json:"server_health_deadline_ms,omitempty"`
	FullConvergenceDeadlineMS   int64 `json:"full_convergence_deadline_ms,omitempty"`
	RevalidationDeadlineMS      int64 `json:"revalidation_deadline_ms,omitempty"`
	PressureTargetUsedPercent   int   `json:"pressure_target_used_percent,omitempty"`
	MaximumPressureBallastBytes int64 `json:"maximum_pressure_ballast_bytes,omitempty"`
}

type StopRule struct {
	Decision string `json:"decision"`
	Trigger  string `json:"trigger"`
}

type PlanClaims struct {
	Neutral                    bool `json:"neutral"`
	SourceFreeReceipt          bool `json:"source_free_receipt"`
	RaisesProductionBound      bool `json:"raises_production_bound"`
	EstablishesTargetSLO       bool `json:"establishes_target_slo"`
	EstablishesAccuracy        bool `json:"establishes_accuracy"`
	EstablishesCompleteness    bool `json:"establishes_completeness"`
	AuthorizesRelease          bool `json:"authorizes_release"`
	AuthorizesPrivateRerun     bool `json:"authorizes_private_rerun"`
	EstablishesMigration       bool `json:"establishes_migration"`
	EstablishesDecommissioning bool `json:"establishes_decommissioning"`
}

type Observation struct {
	Schema           string                       `json:"schema"`
	MeasuredOn       string                       `json:"measured_on"`
	Outcome          string                       `json:"outcome"`
	Environment      EnvironmentObservation       `json:"environment"`
	HostToolchain    []HostToolObservation        `json:"host_toolchain,omitempty"`
	Toolchain        []ToolchainObservation       `json:"toolchain"`
	ServerStartups   []ServerStartupObservation   `json:"server_startups,omitempty"`
	ConvergenceWaits []ConvergenceWaitObservation `json:"convergence_waits,omitempty"`
	Profiles         []ProfileObservation         `json:"profiles"`
	BlobReaders      []BlobReaderObservation      `json:"blob_readers"`
	Service          ServiceControlObservation    `json:"service_control"`
	Explicit         ExplicitStateObservation     `json:"explicit_states"`
	Phases           []PhaseObservation           `json:"phases"`
	Checks           []CheckObservation           `json:"checks"`
	Failures         []FailureObservation         `json:"failures"`
	Decision         DecisionObservation          `json:"decision"`
	Teardown         TeardownObservation          `json:"teardown"`
}

// ServerStartupObservation retains only bounded, source-free readiness facts.
// The raw startup log, configuration, paths, credentials, and process output
// remain in custody and are destroyed after the ceremony.
type ServerStartupObservation struct {
	Profile         string `json:"profile"`
	Label           string `json:"label"`
	Outcome         string `json:"outcome"`
	LastStage       string `json:"last_stage"`
	LastHealthClass string `json:"last_health_class"`
	HealthAttempts  int64  `json:"health_attempts"`
	WallMS          int64  `json:"wall_ms"`
	PeakRSSBytes    int64  `json:"peak_rss_bytes"`
	GitChildren     int64  `json:"git_children"`
	IndexChildren   int64  `json:"index_children"`
	OtherChildren   int64  `json:"other_children"`
	LogBytes        int64  `json:"log_bytes"`
	LogSHA256       string `json:"log_sha256"`
}

// ConvergenceWaitObservation retains only a closed stage and digests of
// bounded source-free controls. It never retains repository paths, source
// bytes, HTTP bodies, credentials, or raw process output.
type ConvergenceWaitObservation struct {
	Profile                     string                             `json:"profile"`
	Label                       string                             `json:"label"`
	Revision                    string                             `json:"revision"`
	Outcome                     string                             `json:"outcome"`
	FirstStage                  string                             `json:"first_stage,omitempty"`
	LastStage                   string                             `json:"last_stage"`
	Attempts                    int64                              `json:"attempts"`
	ProgressChanges             int64                              `json:"progress_changes"`
	StageChanges                int64                              `json:"stage_changes,omitempty"`
	FirstProgressSHA256         string                             `json:"first_progress_sha256"`
	LastProgressSHA256          string                             `json:"last_progress_sha256"`
	LastProgressChangeWallMS    int64                              `json:"last_progress_change_wall_ms,omitempty"`
	ObservationProgress         *ObservationProgressObservation    `json:"observation_progress,omitempty"`
	ObservationProgressWallMS   int64                              `json:"observation_progress_wall_ms,omitempty"`
	InspectionTransitions       []ConvergenceTransitionObservation `json:"inspection_transitions,omitempty"`
	LastSuccessfulProbeSHA256   string                             `json:"last_successful_probe_sha256,omitempty"`
	LastSuccessfulProbeWallMS   int64                              `json:"last_successful_probe_wall_ms,omitempty"`
	LastInspectionStage         string                             `json:"last_inspection_stage,omitempty"`
	LastInspectionClass         string                             `json:"last_inspection_class,omitempty"`
	LastInspectionHTTPStatus    int                                `json:"last_inspection_http_status,omitempty"`
	LastInspectionHTTPReason    string                             `json:"last_inspection_http_reason,omitempty"`
	LastInspectionSHA256        string                             `json:"last_inspection_sha256,omitempty"`
	LastInspectionWallMS        int64                              `json:"last_inspection_wall_ms,omitempty"`
	RepositoryIndexFailureClass string                             `json:"repository_index_failure_class,omitempty"`
	TransitionLimitExceeded     bool                               `json:"transition_limit_exceeded,omitempty"`
	DeadlineMS                  int64                              `json:"deadline_ms"`
	WallMS                      int64                              `json:"wall_ms"`
}

// ConvergenceTransitionObservation retains only changes in the closed
// inspection stage/failure class and its bounded progress digest. It excludes
// raw errors, paths, repository/source identities, responses, and process output.
type ConvergenceTransitionObservation struct {
	WallMS         int64  `json:"wall_ms"`
	Stage          string `json:"stage"`
	Class          string `json:"class"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	HTTPReason     string `json:"http_reason,omitempty"`
	ProgressSHA256 string `json:"progress_sha256"`
}

// ObservationProgressObservation is a source-free projection of the bounded
// observation progress response already read by the exact ceremony oracle.
// Repository identities, generation digests, timestamps, paths, and errors
// remain private.
type ObservationProgressObservation struct {
	State                       string `json:"state"`
	PlanningState               string `json:"planning_state,omitempty"`
	PlanningPending             int    `json:"planning_pending,omitempty"`
	PlanningRunning             int    `json:"planning_running,omitempty"`
	PlanningSucceeded           int    `json:"planning_succeeded,omitempty"`
	PlanningFailed              int    `json:"planning_failed,omitempty"`
	ScheduleState               string `json:"schedule_state,omitempty"`
	ScheduleTotalPartitions     int    `json:"schedule_total_partitions,omitempty"`
	ScheduleMaterialized        int    `json:"schedule_materialized,omitempty"`
	SchedulePending             int    `json:"schedule_pending,omitempty"`
	ScheduleRunning             int    `json:"schedule_running,omitempty"`
	ScheduleSucceeded           int    `json:"schedule_succeeded,omitempty"`
	ScheduleFailed              int    `json:"schedule_failed,omitempty"`
	PublicationState            string `json:"publication_state,omitempty"`
	PublicationRecordCount      int    `json:"publication_record_count,omitempty"`
	PublicationObservedCount    int    `json:"publication_observed_count,omitempty"`
	PublicationUnsupportedCount int    `json:"publication_unsupported_count,omitempty"`
}

type ToolchainObservation struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// HostToolObservation binds a source-free ceremony to an exact executable and
// its bounded public version identity without retaining a host filesystem path.
type HostToolObservation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type EnvironmentObservation struct {
	OS                       string `json:"os"`
	Arch                     string `json:"arch"`
	MemoryBytes              int64  `json:"memory_bytes"`
	FilesystemTotalBytes     int64  `json:"filesystem_total_bytes"`
	FilesystemAvailableBytes int64  `json:"filesystem_available_bytes"`
	InitialUsedPercent       int    `json:"initial_used_percent"`
}

type ProfileObservation struct {
	Name                  string `json:"name"`
	RegularFiles          uint64 `json:"regular_files"`
	PhysicalOwners        uint64 `json:"physical_owners"`
	EligibleGoFiles       uint64 `json:"eligible_go_files"`
	IDLCandidates         uint64 `json:"idl_candidates"`
	DeclaredSourceBytes   uint64 `json:"declared_source_bytes"`
	SearchPublished       bool   `json:"search_published"`
	ApplicablePartitions  int    `json:"applicable_partitions"`
	SettledPartitions     int    `json:"settled_partitions"`
	PublishedDomains      int    `json:"published_domains"`
	RelationshipPublished bool   `json:"relationship_published"`
}

type BlobReaderObservation struct {
	Profile            string `json:"profile"`
	Revision           string `json:"revision"`
	Mode               string `json:"mode"`
	FilesOffered       uint64 `json:"files_offered"`
	BatchReads         uint64 `json:"batch_reads"`
	FallbackReads      uint64 `json:"fallback_reads"`
	SilentDisablement  bool   `json:"silent_disablement"`
	UnexpectedFallback bool   `json:"unexpected_fallback"`
}

type ServiceControlObservation struct {
	AcceptedServices      int  `json:"accepted_services"`
	Memberships           int  `json:"memberships"`
	DistinctPaths         int  `json:"distinct_paths"`
	UnownedPrefixes       int  `json:"unowned_prefixes"`
	WithinV2PathLimit     bool `json:"within_v2_path_limit"`
	ExactMembershipOracle bool `json:"exact_membership_oracle"`
	ExactUnownedOracle    bool `json:"exact_unowned_oracle"`
}

type ExplicitStateObservation struct {
	AbsentTypedInputs      int    `json:"absent_typed_inputs"`
	UnavailableDomains     int    `json:"unavailable_domains"`
	UnsupportedSyntaxFacts uint64 `json:"unsupported_syntax_facts"`
	GapFacts               uint64 `json:"gap_facts"`
	NoSilentEmpty          bool   `json:"no_silent_empty"`
}

type PhaseObservation struct {
	Name             string       `json:"name"`
	Outcome          string       `json:"outcome"`
	Metrics          PhaseMetrics `json:"metrics"`
	AuthorityChanged bool         `json:"authority_changed"`
	OracleExact      bool         `json:"oracle_exact"`
}

type PhaseMetrics struct {
	WallMS                    int64 `json:"wall_ms"`
	PeakRSSBytes              int64 `json:"peak_rss_bytes"`
	DataLogicalBytes          int64 `json:"data_logical_bytes"`
	DataAllocatedBytes        int64 `json:"data_allocated_bytes"`
	GitChildren               int64 `json:"git_children"`
	IndexChildren             int64 `json:"index_children"`
	OtherChildren             int64 `json:"other_children"`
	ControlReads              int64 `json:"control_reads"`
	MemberReads               int64 `json:"member_reads"`
	PublicationWrites         int64 `json:"publication_writes"`
	PublicationTransactions   int64 `json:"publication_transactions"`
	OrchestrationTransactions int64 `json:"orchestration_transactions"`
	Retries                   int64 `json:"retries"`
	ReusedControls            int64 `json:"reused_controls"`
	ReusedMembers             int64 `json:"reused_members"`
}

type CheckObservation struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

type FailureObservation struct {
	Phase string `json:"phase"`
	Class string `json:"class"`
	Code  string `json:"code"`
}

type DecisionObservation struct {
	Selected      string `json:"selected"`
	Reason        string `json:"reason"`
	Substantiated bool   `json:"substantiated"`
}

type TeardownObservation struct {
	Completed             bool `json:"completed"`
	DerivedDataRetained   bool `json:"derived_data_retained"`
	ScratchSourceRetained bool `json:"scratch_source_retained"`
}

type Receipt struct {
	Schema           string                       `json:"schema"`
	PlanDigest       string                       `json:"plan_digest"`
	SourceCommit     string                       `json:"source_commit"`
	MeasuredOn       string                       `json:"measured_on"`
	Outcome          string                       `json:"outcome"`
	Environment      EnvironmentObservation       `json:"environment"`
	HostToolchain    []HostToolObservation        `json:"host_toolchain,omitempty"`
	Toolchain        []ToolchainObservation       `json:"toolchain"`
	ServerStartups   []ServerStartupObservation   `json:"server_startups,omitempty"`
	ConvergenceWaits []ConvergenceWaitObservation `json:"convergence_waits,omitempty"`
	Profiles         []ProfileObservation         `json:"profiles"`
	BlobReaders      []BlobReaderObservation      `json:"blob_readers"`
	Service          ServiceControlObservation    `json:"service_control"`
	Explicit         ExplicitStateObservation     `json:"explicit_states"`
	Phases           []PhaseObservation           `json:"phases"`
	Checks           []CheckObservation           `json:"checks"`
	Failures         []FailureObservation         `json:"failures"`
	Decision         DecisionObservation          `json:"decision"`
	Teardown         TeardownObservation          `json:"teardown"`
	Claims           ReceiptClaims                `json:"claims"`
}

type ReceiptClaims struct {
	MechanicsEvidenceOnly      bool `json:"mechanics_evidence_only"`
	EstablishesTargetSLO       bool `json:"establishes_target_slo"`
	EstablishesServiceScale    bool `json:"establishes_service_scale"`
	EstablishesAccuracy        bool `json:"establishes_accuracy"`
	EstablishesCompleteness    bool `json:"establishes_completeness"`
	AuthorizesRelease          bool `json:"authorizes_release"`
	AuthorizesPrivateRerun     bool `json:"authorizes_private_rerun"`
	EstablishesMigration       bool `json:"establishes_migration"`
	EstablishesDecommissioning bool `json:"establishes_decommissioning"`
}

func BuildReceipt(planBytes, observationBytes []byte, planDigest string) ([]byte, error) {
	plan, err := DecodePlan(planBytes)
	if err != nil {
		return nil, err
	}
	if digest(planBytes) != planDigest {
		return nil, errors.New("T40.13 plan digest differs from the frozen bytes")
	}
	observation, err := DecodeObservation(observationBytes)
	if err != nil {
		return nil, err
	}
	receipt := Receipt{
		Schema: receiptSchemaForPlan(plan), PlanDigest: planDigest, SourceCommit: plan.SourceCommit,
		MeasuredOn: observation.MeasuredOn, Outcome: observation.Outcome,
		Environment: observation.Environment, HostToolchain: observation.HostToolchain,
		Toolchain: observation.Toolchain, ServerStartups: observation.ServerStartups,
		ConvergenceWaits: observation.ConvergenceWaits,
		Profiles:         observation.Profiles,
		BlobReaders:      observation.BlobReaders, Service: observation.Service,
		Explicit: observation.Explicit, Phases: observation.Phases, Checks: observation.Checks,
		Failures: observation.Failures, Decision: observation.Decision, Teardown: observation.Teardown,
		Claims: ReceiptClaims{MechanicsEvidenceOnly: true},
	}
	if err := ValidateReceipt(receipt, plan); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxReceiptBytes {
		return nil, errors.New("T40.13 receipt exceeds its fixed byte bound")
	}
	return encoded, nil
}

func DecodePlan(raw []byte) (Plan, error) {
	if len(raw) == 0 || len(raw) > MaxPlanBytes {
		return Plan{}, errors.New("T40.13 plan is outside its fixed byte bound")
	}
	var value Plan
	if err := decodeStrict(raw, &value); err != nil {
		return Plan{}, fmt.Errorf("decode T40.13 plan: %w", err)
	}
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func DecodeObservation(raw []byte) (Observation, error) {
	if len(raw) == 0 || len(raw) > MaxObservationBytes {
		return Observation{}, errors.New("T40.13 observation is outside its fixed byte bound")
	}
	var value Observation
	if err := decodeStrict(raw, &value); err != nil {
		return Observation{}, fmt.Errorf("decode T40.13 observation: %w", err)
	}
	if err := ValidateObservation(value); err != nil {
		return Observation{}, err
	}
	return value, nil
}

func MarshalObservation(value Observation) ([]byte, error) {
	if err := ValidateObservation(value); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxObservationBytes {
		return nil, errors.New("T40.13 observation exceeds its fixed byte bound")
	}
	return encoded, nil
}

func ValidatePlan(value Plan) error {
	if !slices.Contains([]string{PlanSchema, PlanSchemaV2, PlanSchemaV3, PlanSchemaV4, PlanSchemaV5, PlanSchemaV6, PlanSchemaV7, PlanSchemaV8, PlanSchemaV9, PlanSchemaV10}, value.Schema) ||
		!date(value.FrozenOn) || !hexIdentity(value.SourceCommit, 40) ||
		!slices.Equal(value.PhaseOrder, phaseOrder) || len(value.Inputs) != 4 || len(value.StopRules) != 4 {
		return errors.New("T40.13 plan identity or fixed inventory is invalid")
	}
	if value.Schema == PlanSchema && len(value.HostToolchain) != 0 {
		return errors.New("T40.13 v1 plan unexpectedly binds a host toolchain")
	}
	if value.Schema == PlanSchemaV2 || value.Schema == PlanSchemaV3 ||
		value.Schema == PlanSchemaV4 || value.Schema == PlanSchemaV5 || value.Schema == PlanSchemaV6 ||
		value.Schema == PlanSchemaV7 || value.Schema == PlanSchemaV8 || value.Schema == PlanSchemaV9 || value.Schema == PlanSchemaV10 {
		if err := validateHostToolchain(value.HostToolchain); err != nil {
			return err
		}
	}
	if !slices.Equal(value.Inputs, expectedInputs) || !slices.Equal(value.StopRules, frozenStopRules) {
		return errors.New("T40.13 frozen inputs or stop rules changed")
	}
	wantSafety := frozenSafety
	switch value.Schema {
	case PlanSchemaV3:
		wantSafety = frozenSafetyV3
	case PlanSchemaV4:
		wantSafety = frozenSafetyV4
	case PlanSchemaV5:
		wantSafety = frozenSafetyV5
	case PlanSchemaV6:
		wantSafety = frozenSafetyV6
	case PlanSchemaV7:
		wantSafety = frozenSafetyV7
	case PlanSchemaV8:
		wantSafety = frozenSafetyV8
	case PlanSchemaV9:
		wantSafety = frozenSafetyV9
	case PlanSchemaV10:
		wantSafety = frozenSafetyV10
	}
	if value.Safety != wantSafety {
		return errors.New("T40.13 frozen safety envelope changed")
	}
	for _, input := range value.Inputs {
		if input.Path == "" || input.Schema == "" || !digestIdentity(input.SHA256) {
			return errors.New("T40.13 input binding is invalid")
		}
	}
	safety := value.Safety
	if safety.MinimumMemoryBytes < 16<<30 || safety.MinimumAvailableDiskBytes < 32<<30 ||
		safety.MaximumTotalWallMS <= 0 || safety.MaximumPeakRSSBytes <= 0 ||
		safety.MaximumDataAllocatedBytes <= 0 || safety.MaximumRetriesPerUnit < 1 || safety.MaximumRetriesPerUnit > 5 ||
		(value.Schema == PlanSchemaV3 || value.Schema == PlanSchemaV4 || value.Schema == PlanSchemaV5 || value.Schema == PlanSchemaV6 || value.Schema == PlanSchemaV7 || value.Schema == PlanSchemaV8 || value.Schema == PlanSchemaV9 || value.Schema == PlanSchemaV10) &&
			safety.ServerHealthDeadlineMS <= 0 ||
		value.Schema != PlanSchemaV3 && value.Schema != PlanSchemaV4 && value.Schema != PlanSchemaV5 && value.Schema != PlanSchemaV6 && value.Schema != PlanSchemaV7 && value.Schema != PlanSchemaV8 && value.Schema != PlanSchemaV9 && value.Schema != PlanSchemaV10 &&
			safety.ServerHealthDeadlineMS != 0 ||
		(value.Schema == PlanSchemaV4 || value.Schema == PlanSchemaV5 || value.Schema == PlanSchemaV6 || value.Schema == PlanSchemaV7 || value.Schema == PlanSchemaV8 || value.Schema == PlanSchemaV9 || value.Schema == PlanSchemaV10) && (safety.FullConvergenceDeadlineMS <= 0 ||
			safety.RevalidationDeadlineMS <= 0 || safety.RevalidationDeadlineMS > safety.FullConvergenceDeadlineMS) ||
		value.Schema != PlanSchemaV4 && value.Schema != PlanSchemaV5 && value.Schema != PlanSchemaV6 && value.Schema != PlanSchemaV7 && value.Schema != PlanSchemaV8 && value.Schema != PlanSchemaV9 && value.Schema != PlanSchemaV10 &&
			(safety.FullConvergenceDeadlineMS != 0 || safety.RevalidationDeadlineMS != 0) {
		return errors.New("T40.13 safety envelope is invalid")
	}
	if value.Schema == PlanSchemaV10 {
		if safety.PressureTargetUsedPercent < 80 || safety.PressureTargetUsedPercent >= 90 ||
			safety.MaximumPressureBallastBytes <= 0 ||
			safety.MaximumPressureBallastBytes > safety.MaximumDataAllocatedBytes {
			return errors.New("T40.13 pressure exercise envelope is invalid")
		}
	} else if safety.PressureTargetUsedPercent != 0 || safety.MaximumPressureBallastBytes != 0 {
		return errors.New("T40.13 historical safety envelope acquired pressure controls")
	}
	wantDecisions := []string{"continue", "reduce", "cohort_experiment", "p6_investigation"}
	for index, rule := range value.StopRules {
		if rule.Decision != wantDecisions[index] || rule.Trigger == "" || len(rule.Trigger) > 512 {
			return errors.New("T40.13 stop rule is invalid")
		}
	}
	claims := value.Claims
	if !claims.Neutral || !claims.SourceFreeReceipt || claims.RaisesProductionBound ||
		claims.EstablishesTargetSLO || claims.EstablishesAccuracy || claims.EstablishesCompleteness ||
		claims.AuthorizesRelease || claims.AuthorizesPrivateRerun || claims.EstablishesMigration ||
		claims.EstablishesDecommissioning {
		return errors.New("T40.13 plan claims are invalid")
	}
	return nil
}

func ValidateObservation(value Observation) error {
	if !slices.Contains([]string{
		ObservationSchema, ObservationSchemaV2, ObservationSchemaV3, ObservationSchemaV4, ObservationSchemaV5, ObservationSchemaV6, ObservationSchemaV7, ObservationSchemaV8, ObservationSchemaV9, ObservationSchemaV10,
	}, value.Schema) ||
		!date(value.MeasuredOn) ||
		(value.Outcome != "completed" && value.Outcome != "stopped") {
		return errors.New("T40.13 observation identity is invalid")
	}
	if value.Schema == ObservationSchema && len(value.HostToolchain) != 0 {
		return errors.New("T40.13 v1 observation unexpectedly binds a host toolchain")
	}
	if value.Schema == ObservationSchemaV2 || value.Schema == ObservationSchemaV3 ||
		value.Schema == ObservationSchemaV4 || value.Schema == ObservationSchemaV5 || value.Schema == ObservationSchemaV6 ||
		value.Schema == ObservationSchemaV7 || value.Schema == ObservationSchemaV8 || value.Schema == ObservationSchemaV9 || value.Schema == ObservationSchemaV10 {
		if err := validateHostToolchain(value.HostToolchain); err != nil {
			return err
		}
	}
	if value.Environment.OS == "" || value.Environment.Arch == "" || value.Environment.MemoryBytes <= 0 ||
		value.Environment.FilesystemTotalBytes <= 0 || value.Environment.FilesystemAvailableBytes < 0 ||
		value.Environment.FilesystemAvailableBytes > value.Environment.FilesystemTotalBytes ||
		value.Environment.InitialUsedPercent < 0 || value.Environment.InitialUsedPercent > 100 {
		return errors.New("T40.13 environment observation is invalid")
	}
	if err := validateToolchainObservation(value.Toolchain, value.Outcome == "completed"); err != nil {
		return err
	}
	if value.Schema != ObservationSchemaV3 && value.Schema != ObservationSchemaV4 &&
		value.Schema != ObservationSchemaV5 && value.Schema != ObservationSchemaV6 && value.Schema != ObservationSchemaV7 && value.Schema != ObservationSchemaV8 && value.Schema != ObservationSchemaV9 && value.Schema != ObservationSchemaV10 &&
		len(value.ServerStartups) != 0 {
		return errors.New("T40.13 pre-v3 observation unexpectedly retains startup diagnostics")
	}
	if value.Schema == ObservationSchemaV3 || value.Schema == ObservationSchemaV4 ||
		value.Schema == ObservationSchemaV5 || value.Schema == ObservationSchemaV6 || value.Schema == ObservationSchemaV7 || value.Schema == ObservationSchemaV8 || value.Schema == ObservationSchemaV9 || value.Schema == ObservationSchemaV10 {
		if err := validateServerStartups(value.ServerStartups, value.Schema == ObservationSchemaV10); err != nil {
			return err
		}
	}
	if value.Schema != ObservationSchemaV4 && value.Schema != ObservationSchemaV5 && value.Schema != ObservationSchemaV6 &&
		value.Schema != ObservationSchemaV7 && value.Schema != ObservationSchemaV8 && value.Schema != ObservationSchemaV9 && value.Schema != ObservationSchemaV10 &&
		len(value.ConvergenceWaits) != 0 {
		return errors.New("T40.13 pre-v4 observation unexpectedly retains convergence waits")
	}
	if value.Schema == ObservationSchemaV4 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, 0); err != nil {
			return err
		}
	}
	if value.Schema == ObservationSchemaV5 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, 1); err != nil {
			return err
		}
	}
	if value.Schema == ObservationSchemaV6 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, 2); err != nil {
			return err
		}
	}
	if value.Schema == ObservationSchemaV7 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, 3); err != nil {
			return err
		}
	}
	if value.Schema == ObservationSchemaV8 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, 4); err != nil {
			return err
		}
	}
	if value.Schema == ObservationSchemaV9 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, 5); err != nil {
			return err
		}
	}
	if value.Schema == ObservationSchemaV10 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, 6); err != nil {
			return err
		}
	}
	if len(value.Profiles) != 2 || value.Profiles[0].Name != "structural-2m-v1" ||
		value.Profiles[1].Name != "semantic-262144-v1" {
		return errors.New("T40.13 profile observation inventory is invalid")
	}
	for _, profile := range value.Profiles {
		if profile.RegularFiles == 0 || profile.PhysicalOwners == 0 || profile.DeclaredSourceBytes == 0 ||
			profile.ApplicablePartitions < 0 || profile.SettledPartitions < 0 || profile.PublishedDomains < 0 ||
			profile.SettledPartitions > profile.ApplicablePartitions {
			return errors.New("T40.13 profile observation is invalid")
		}
	}
	for _, reader := range value.BlobReaders {
		if reader.Profile == "" || !slices.Contains([]string{"a", "b", "a-return"}, reader.Revision) ||
			reader.Mode != "go_git" || reader.FilesOffered == 0 || reader.BatchReads != 0 ||
			reader.FallbackReads != reader.FilesOffered || reader.SilentDisablement || reader.UnexpectedFallback {
			return errors.New("T40.13 blob-reader observation is invalid")
		}
	}
	if len(value.Phases) != len(phaseOrder) || len(value.Checks) != len(checkNames) {
		return errors.New("T40.13 phase or check inventory is incomplete")
	}
	for index, phase := range value.Phases {
		if phase.Name != phaseOrder[index] || !slices.Contains([]string{"succeeded", "failed", "not_run"}, phase.Outcome) ||
			!nonnegativeMetrics(phase.Metrics) {
			return errors.New("T40.13 phase observation is invalid")
		}
	}
	for index, check := range value.Checks {
		if check.Name != checkNames[index] {
			return errors.New("T40.13 check order differs from the frozen inventory")
		}
	}
	for _, failure := range value.Failures {
		if !slices.Contains(phaseOrder, failure.Phase) || !slices.Contains(failureClasses, failure.Class) ||
			failure.Code == "" || len(failure.Code) > 128 || strings.ContainsAny(failure.Code, "/\\: ") {
			return errors.New("T40.13 failure observation is invalid")
		}
	}
	if !slices.Contains([]string{"continue", "reduce", "cohort_experiment", "p6_investigation", "unclassified"}, value.Decision.Selected) ||
		value.Decision.Reason == "" || len(value.Decision.Reason) > 256 {
		return errors.New("T40.13 decision observation is invalid")
	}
	if value.Decision.Selected == "unclassified" && value.Decision.Substantiated ||
		value.Decision.Selected != "unclassified" && !value.Decision.Substantiated {
		return errors.New("T40.13 decision evidence state is invalid")
	}
	return nil
}

func ValidateReceipt(value Receipt, plan Plan) error {
	return validateReceipt(value, plan, false)
}

func validateReceipt(value Receipt, plan Plan, exactLegacyStoppedReceipt bool) error {
	if value.Schema != receiptSchemaForPlan(plan) || !digestIdentity(value.PlanDigest) || value.SourceCommit != plan.SourceCommit ||
		!date(value.MeasuredOn) || (value.Outcome != "completed" && value.Outcome != "stopped") {
		return errors.New("T40.13 receipt identity is invalid")
	}
	observation := Observation{
		Schema: observationSchemaForPlan(plan), MeasuredOn: value.MeasuredOn, Outcome: value.Outcome,
		Environment: value.Environment, HostToolchain: value.HostToolchain,
		Toolchain: value.Toolchain, ServerStartups: value.ServerStartups,
		ConvergenceWaits: value.ConvergenceWaits,
		Profiles:         value.Profiles, BlobReaders: value.BlobReaders,
		Service: value.Service, Explicit: value.Explicit, Phases: value.Phases, Checks: value.Checks,
		Failures: value.Failures, Decision: value.Decision, Teardown: value.Teardown,
	}
	if err := ValidateObservation(observation); err != nil {
		return err
	}
	if !slices.Equal(value.HostToolchain, plan.HostToolchain) {
		return errors.New("T40.13 receipt host toolchain differs from the frozen plan")
	}
	if plan.Schema == PlanSchemaV3 || plan.Schema == PlanSchemaV4 || plan.Schema == PlanSchemaV5 || plan.Schema == PlanSchemaV6 || plan.Schema == PlanSchemaV7 || plan.Schema == PlanSchemaV8 || plan.Schema == PlanSchemaV9 || plan.Schema == PlanSchemaV10 {
		for _, startup := range value.ServerStartups {
			if startup.Outcome == "deadline" && startup.WallMS < plan.Safety.ServerHealthDeadlineMS {
				return errors.New("T40.13 startup deadline observation precedes the frozen deadline")
			}
		}
	}
	if plan.Schema == PlanSchemaV4 || plan.Schema == PlanSchemaV5 || plan.Schema == PlanSchemaV6 || plan.Schema == PlanSchemaV7 || plan.Schema == PlanSchemaV8 || plan.Schema == PlanSchemaV9 || plan.Schema == PlanSchemaV10 {
		for _, wait := range value.ConvergenceWaits {
			if wait.DeadlineMS != plan.Safety.FullConvergenceDeadlineMS &&
				wait.DeadlineMS != plan.Safety.RevalidationDeadlineMS {
				return errors.New("T40.13 convergence wait differs from the frozen deadlines")
			}
			if wait.Outcome == "deadline" && wait.WallMS < wait.DeadlineMS {
				return errors.New("T40.13 convergence deadline observation precedes its frozen deadline")
			}
		}
	}
	if value.Outcome == "stopped" && value.Phases[0].Outcome == "succeeded" &&
		len(value.Toolchain) == 0 &&
		(!exactLegacyStoppedReceipt || !isLegacyStoppedReceiptIdentity(value)) {
		return errors.New("T40.13 stopped receipt lacks the preflight executable identities")
	}
	if value.Outcome == "completed" {
		if value.Environment.MemoryBytes < plan.Safety.MinimumMemoryBytes ||
			value.Environment.FilesystemAvailableBytes < plan.Safety.MinimumAvailableDiskBytes {
			return errors.New("T40.13 completed receipt crossed a frozen host prerequisite")
		}
		if len(value.Failures) != 0 || value.Decision.Selected != "continue" ||
			!value.Teardown.Completed || value.Teardown.DerivedDataRetained || value.Teardown.ScratchSourceRetained {
			return errors.New("completed T40.13 receipt has a failure, non-continue decision, or incomplete teardown")
		}
		for _, phase := range value.Phases {
			if phase.Outcome != "succeeded" || !phase.OracleExact {
				return errors.New("completed T40.13 receipt has an incomplete phase")
			}
		}
		for _, check := range value.Checks {
			if !check.Passed {
				return errors.New("completed T40.13 receipt has a failed check")
			}
		}
		if err := validateCompleted(value, plan); err != nil {
			return err
		}
	} else if err := validateStopped(value); err != nil {
		return err
	}
	claims := value.Claims
	if !claims.MechanicsEvidenceOnly || claims.EstablishesTargetSLO || claims.EstablishesServiceScale ||
		claims.EstablishesAccuracy || claims.EstablishesCompleteness || claims.AuthorizesRelease ||
		claims.AuthorizesPrivateRerun || claims.EstablishesMigration || claims.EstablishesDecommissioning {
		return errors.New("T40.13 receipt claims are invalid")
	}
	return nil
}

func observationSchemaForPlan(plan Plan) string {
	if plan.Schema == PlanSchemaV10 {
		return ObservationSchemaV10
	}
	if plan.Schema == PlanSchemaV9 {
		return ObservationSchemaV9
	}
	if plan.Schema == PlanSchemaV8 {
		return ObservationSchemaV8
	}
	if plan.Schema == PlanSchemaV7 {
		return ObservationSchemaV7
	}
	if plan.Schema == PlanSchemaV6 {
		return ObservationSchemaV6
	}
	if plan.Schema == PlanSchemaV5 {
		return ObservationSchemaV5
	}
	if plan.Schema == PlanSchemaV4 {
		return ObservationSchemaV4
	}
	if plan.Schema == PlanSchemaV3 {
		return ObservationSchemaV3
	}
	if plan.Schema == PlanSchemaV2 {
		return ObservationSchemaV2
	}
	return ObservationSchema
}

func receiptSchemaForPlan(plan Plan) string {
	if plan.Schema == PlanSchemaV10 {
		return ReceiptSchemaV10
	}
	if plan.Schema == PlanSchemaV9 {
		return ReceiptSchemaV9
	}
	if plan.Schema == PlanSchemaV8 {
		return ReceiptSchemaV8
	}
	if plan.Schema == PlanSchemaV7 {
		return ReceiptSchemaV7
	}
	if plan.Schema == PlanSchemaV6 {
		return ReceiptSchemaV6
	}
	if plan.Schema == PlanSchemaV5 {
		return ReceiptSchemaV5
	}
	if plan.Schema == PlanSchemaV4 {
		return ReceiptSchemaV4
	}
	if plan.Schema == PlanSchemaV3 {
		return ReceiptSchemaV3
	}
	if plan.Schema == PlanSchemaV2 {
		return ReceiptSchemaV2
	}
	return ReceiptSchema
}

func validateHostToolchain(values []HostToolObservation) error {
	want := []string{"go", "go-compile", "go-link", "git", "surreal"}
	if len(values) != len(want) {
		return errors.New("T40.13 host toolchain identity inventory is incomplete")
	}
	for index, name := range want {
		value := values[index]
		if value.Name != name || !boundedVersion(value.Version) || !digestIdentity(value.SHA256) {
			return errors.New("T40.13 host toolchain identity is invalid")
		}
	}
	return nil
}

func boundedVersion(value string) bool {
	if len(value) == 0 || len(value) > 192 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func validateToolchainObservation(values []ToolchainObservation, required bool) error {
	want := []string{"phebs", "zoekt-git-index", "phebs-focused-index", "buf"}
	if len(values) == 0 && !required {
		return nil
	}
	if len(values) != len(want) {
		return errors.New("T40.13 toolchain digest inventory is incomplete")
	}
	for index, name := range want {
		if values[index].Name != name || !digestIdentity(values[index].SHA256) {
			return errors.New("T40.13 toolchain digest identity is invalid")
		}
	}
	return nil
}

func validateServerStartups(values []ServerStartupObservation, allowPressureRestart bool) error {
	if len(values) > 16 {
		return errors.New("T40.13 startup diagnostic inventory exceeds its bound")
	}
	profiles := []string{"structural-2m-v1", "semantic-262144-v1"}
	labels := []string{
		"cold", "warm-noop", "interruption-first", "interruption-restart",
		"stale-worker", "archive-restore", "authorized-query",
	}
	if allowPressureRestart {
		labels = append(labels, "pressure-restart")
	}
	outcomes := []string{"healthy", "deadline", "exited", "canceled", "inspector_error"}
	stages := []string{
		"process_started", "config_loaded", "data_directory_ready", "store_opened",
		"authority_recovery_complete", "artifact_recovery_complete",
		"scheduler_recovery_complete", "searcher_ready", "http_ready", "unreported",
	}
	healthClasses := []string{"ok", "transport", "status", "response", "context", "not_attempted"}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := value.Profile + "\x00" + value.Label
		if !slices.Contains(profiles, value.Profile) || !slices.Contains(labels, value.Label) ||
			!slices.Contains(outcomes, value.Outcome) || !slices.Contains(stages, value.LastStage) ||
			!slices.Contains(healthClasses, value.LastHealthClass) || value.HealthAttempts < 0 ||
			value.HealthAttempts > 1_000_000 || value.WallMS < 0 || value.PeakRSSBytes < 0 ||
			value.GitChildren < 0 || value.IndexChildren < 0 || value.OtherChildren < 0 ||
			value.LogBytes < 0 || value.LogBytes > maxStartupLogBytes || !digestIdentity(value.LogSHA256) {
			return errors.New("T40.13 startup diagnostic is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("T40.13 startup diagnostic identity is duplicated")
		}
		seen[key] = struct{}{}
		if value.Outcome == "healthy" {
			if value.LastStage != "http_ready" || value.LastHealthClass != "ok" || value.HealthAttempts == 0 {
				return errors.New("T40.13 healthy startup diagnostic is incoherent")
			}
		} else if value.LastHealthClass == "ok" {
			return errors.New("T40.13 failed startup diagnostic reports healthy")
		}
	}
	return nil
}

func validateConvergenceWaits(values []ConvergenceWaitObservation, detailVersion int) error {
	if len(values) > 16 {
		return errors.New("T40.13 convergence wait inventory exceeds its bound")
	}
	profiles := []string{"structural-2m-v1", "semantic-262144-v1"}
	labels := []string{
		"cold", "warm-noop", "delta-b", "return-a", "interruption-restart",
		"stale-worker", "pressure", "archive-restore", "collection",
		"authorized-query-semantic", "authorized-query-structural",
	}
	revisions := []string{"a", "b", "a-return"}
	outcomes := []string{"converged", "deadline", "canceled"}
	if detailVersion >= 2 {
		outcomes = append(outcomes, "server_exited", "diagnostic_limit")
	}
	if detailVersion >= 5 {
		outcomes = append(outcomes, "repository_index_terminal")
	}
	stages := []string{
		"repository_visibility", "repository_index", "source_generation",
		"search_generation", "observation_publication", "extraction_publication",
		"relationship_publication", "service_census", "complete",
	}
	if detailVersion >= 2 {
		stages = append(stages, convergenceNotInspected)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := value.Profile + "\x00" + value.Label + "\x00" + value.Revision
		if !slices.Contains(profiles, value.Profile) || !slices.Contains(labels, value.Label) ||
			!slices.Contains(revisions, value.Revision) || !slices.Contains(outcomes, value.Outcome) ||
			!slices.Contains(stages, value.LastStage) || value.Attempts < 0 || value.Attempts > 1_000_000 ||
			detailVersion < 2 && value.Attempts == 0 || value.ProgressChanges < 0 ||
			value.Attempts == 0 && value.ProgressChanges != 0 ||
			value.Attempts > 0 && value.ProgressChanges >= value.Attempts ||
			value.LastStage != convergenceNotInspected &&
				(!digestIdentity(value.FirstProgressSHA256) || !digestIdentity(value.LastProgressSHA256)) ||
			value.DeadlineMS <= 0 || value.WallMS < 0 {
			return errors.New("T40.13 convergence wait diagnostic is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("T40.13 convergence wait identity is duplicated")
		}
		seen[key] = struct{}{}
		if value.Outcome == "converged" && value.LastStage != "complete" ||
			value.Outcome != "converged" && value.LastStage == "complete" {
			return errors.New("T40.13 convergence wait outcome is incoherent")
		}
		if detailVersion == 0 {
			if value.FirstStage != "" || value.StageChanges != 0 || value.LastProgressChangeWallMS != 0 ||
				value.ObservationProgress != nil || value.ObservationProgressWallMS != 0 ||
				len(value.InspectionTransitions) != 0 || value.LastSuccessfulProbeSHA256 != "" ||
				value.LastSuccessfulProbeWallMS != 0 || hasLastInspection(value) || value.TransitionLimitExceeded {
				return errors.New("T40.13 v4 convergence wait acquired v5 diagnostics")
			}
			continue
		}
		if detailVersion >= 2 && value.LastStage == convergenceNotInspected {
			if err := validateConvergenceWithoutSuccessfulProbe(value, stages, detailVersion); err != nil {
				return err
			}
			continue
		}
		if !slices.Contains(stages, value.FirstStage) || value.StageChanges < 0 ||
			value.StageChanges > value.ProgressChanges || value.LastProgressChangeWallMS < 0 ||
			value.LastProgressChangeWallMS > value.WallMS || value.ObservationProgressWallMS < 0 ||
			value.ObservationProgressWallMS > value.WallMS {
			return errors.New("T40.13 detailed convergence timing is invalid")
		}
		if value.ProgressChanges == 0 {
			if value.FirstProgressSHA256 != value.LastProgressSHA256 || value.StageChanges != 0 ||
				value.FirstStage != value.LastStage || value.LastProgressChangeWallMS != 0 {
				return errors.New("T40.13 unchanged convergence progress is incoherent")
			}
		} else if value.LastProgressChangeWallMS == 0 {
			return errors.New("T40.13 changed convergence progress lacks its change time")
		}
		if value.StageChanges == 0 && value.FirstStage != value.LastStage {
			return errors.New("T40.13 convergence stage identity changed without accounting")
		}
		if value.ObservationProgress == nil {
			if value.ObservationProgressWallMS != 0 {
				return errors.New("T40.13 absent observation progress has a wall time")
			}
		} else if err := validateObservationProgress(*value.ObservationProgress); err != nil {
			return err
		}
		if detailVersion < 2 {
			if len(value.InspectionTransitions) != 0 || value.LastSuccessfulProbeSHA256 != "" ||
				value.LastSuccessfulProbeWallMS != 0 || hasLastInspection(value) || value.TransitionLimitExceeded {
				return errors.New("T40.13 v5 convergence wait acquired v6 diagnostics")
			}
			continue
		}
		if err := validateConvergenceTransitions(value, stages, detailVersion); err != nil {
			return err
		}
	}
	return nil
}

func validateConvergenceTransitions(
	value ConvergenceWaitObservation,
	stages []string,
	detailVersion int,
) error {
	classes := []string{"pending", "transport", "status", "response", "control", "complete"}
	if detailVersion >= 5 {
		classes = append(classes, "terminal")
	}
	if len(value.InspectionTransitions) == 0 || len(value.InspectionTransitions) > maxConvergenceTransitions ||
		int64(len(value.InspectionTransitions)) > value.Attempts ||
		!digestIdentity(value.LastSuccessfulProbeSHA256) ||
		value.LastSuccessfulProbeSHA256 != value.LastProgressSHA256 ||
		value.LastSuccessfulProbeWallMS <= 0 || value.LastSuccessfulProbeWallMS > value.WallMS ||
		value.TransitionLimitExceeded != (value.Outcome == "diagnostic_limit") ||
		value.TransitionLimitExceeded && (len(value.InspectionTransitions) != maxConvergenceTransitions ||
			value.Attempts <= maxConvergenceTransitions) {
		return errors.New("T40.13 convergence transition inventory is outside its bound")
	}
	var previous ConvergenceTransitionObservation
	var firstSuccessful, lastSuccessful *ConvergenceTransitionObservation
	for index, transition := range value.InspectionTransitions {
		if transition.Stage == convergenceNotInspected || !slices.Contains(stages, transition.Stage) ||
			!slices.Contains(classes, transition.Class) ||
			!validTransitionHTTPDiagnostic(transition, detailVersion) ||
			!digestIdentity(transition.ProgressSHA256) || transition.WallMS < 0 ||
			transition.WallMS > value.WallMS ||
			index > 0 && transition.WallMS < previous.WallMS ||
			index > 0 && transition.Stage == previous.Stage && transition.Class == previous.Class &&
				transition.HTTPStatus == previous.HTTPStatus && transition.HTTPReason == previous.HTTPReason &&
				transition.ProgressSHA256 == previous.ProgressSHA256 {
			return errors.New("T40.13 convergence transition is invalid")
		}
		if transition.Class == "pending" || transition.Class == "complete" {
			current := transition
			if firstSuccessful == nil {
				firstSuccessful = &current
			}
			lastSuccessful = &current
		}
		previous = transition
	}
	last := value.InspectionTransitions[len(value.InspectionTransitions)-1]
	if !value.TransitionLimitExceeded && (firstSuccessful == nil || lastSuccessful == nil) ||
		firstSuccessful != nil && firstSuccessful.Stage != value.FirstStage ||
		!value.TransitionLimitExceeded &&
			(lastSuccessful.Stage != value.LastStage ||
				lastSuccessful.ProgressSHA256 != value.LastSuccessfulProbeSHA256) {
		return errors.New("T40.13 convergence transition stage fence is invalid")
	}
	if value.Outcome == "converged" && last.Class != "complete" ||
		value.Outcome != "converged" && last.Class == "complete" {
		return errors.New("T40.13 convergence transition outcome is incoherent")
	}
	if detailVersion >= 5 &&
		((value.Outcome == "repository_index_terminal") != (last.Class == "terminal")) {
		return errors.New("T40.13 repository-index terminal transition is incoherent")
	}
	if err := validateRepositoryIndexFailureClass(value, detailVersion); err != nil {
		return err
	}
	if err := validateLastInspection(value, stages, detailVersion, last); err != nil {
		return err
	}
	return nil
}

func validateConvergenceWithoutSuccessfulProbe(
	value ConvergenceWaitObservation,
	stages []string,
	detailVersion int,
) error {
	if value.FirstStage != convergenceNotInspected || value.FirstProgressSHA256 != "" ||
		value.LastProgressSHA256 != "" || value.ProgressChanges != 0 || value.StageChanges != 0 ||
		value.LastProgressChangeWallMS != 0 || value.ObservationProgress != nil ||
		value.ObservationProgressWallMS != 0 || value.LastSuccessfulProbeSHA256 != "" ||
		value.LastSuccessfulProbeWallMS != 0 {
		return errors.New("T40.13 convergence wait invents a successful probe")
	}
	if value.Attempts == 0 {
		if value.Outcome != "server_exited" || len(value.InspectionTransitions) != 0 ||
			hasLastInspection(value) || value.TransitionLimitExceeded {
			return errors.New("T40.13 uninspected convergence wait is incoherent")
		}
		return nil
	}
	if len(value.InspectionTransitions) == 0 || len(value.InspectionTransitions) > maxConvergenceTransitions ||
		int64(len(value.InspectionTransitions)) > value.Attempts ||
		value.TransitionLimitExceeded != (value.Outcome == "diagnostic_limit") ||
		value.TransitionLimitExceeded && (len(value.InspectionTransitions) != maxConvergenceTransitions ||
			value.Attempts <= maxConvergenceTransitions) {
		return errors.New("T40.13 unsuccessful convergence transition inventory is outside its bound")
	}
	classes := []string{"transport", "status", "response", "control"}
	if detailVersion >= 5 {
		classes = append(classes, "terminal")
	}
	var previous ConvergenceTransitionObservation
	for index, transition := range value.InspectionTransitions {
		if transition.Stage == convergenceNotInspected || !slices.Contains(stages, transition.Stage) ||
			!slices.Contains(classes, transition.Class) ||
			!validTransitionHTTPDiagnostic(transition, detailVersion) || !digestIdentity(transition.ProgressSHA256) ||
			transition.WallMS < 0 || transition.WallMS > value.WallMS ||
			index > 0 && transition.WallMS < previous.WallMS ||
			index > 0 && transition.Stage == previous.Stage && transition.Class == previous.Class &&
				transition.HTTPStatus == previous.HTTPStatus && transition.HTTPReason == previous.HTTPReason &&
				transition.ProgressSHA256 == previous.ProgressSHA256 {
			return errors.New("T40.13 unsuccessful convergence transition is invalid")
		}
		previous = transition
	}
	if err := validateLastInspection(
		value, stages, detailVersion, value.InspectionTransitions[len(value.InspectionTransitions)-1],
	); err != nil {
		return err
	}
	last := value.InspectionTransitions[len(value.InspectionTransitions)-1]
	if detailVersion >= 5 &&
		((value.Outcome == "repository_index_terminal") != (last.Class == "terminal")) {
		return errors.New("T40.13 unsuccessful repository-index terminal transition is incoherent")
	}
	if err := validateRepositoryIndexFailureClass(value, detailVersion); err != nil {
		return err
	}
	return nil
}

func validateRepositoryIndexFailureClass(value ConvergenceWaitObservation, detailVersion int) error {
	if detailVersion < 6 {
		if value.RepositoryIndexFailureClass != "" {
			return errors.New("T40.13 historical convergence wait acquired a terminal failure class")
		}
		return nil
	}
	if value.Outcome == "repository_index_terminal" {
		if value.RepositoryIndexFailureClass != "lease_heartbeat" &&
			value.RepositoryIndexFailureClass != "other" {
			return errors.New("T40.13 repository-index terminal failure class is invalid")
		}
		return nil
	}
	if value.RepositoryIndexFailureClass != "" {
		return errors.New("T40.13 nonterminal convergence wait has a terminal failure class")
	}
	return nil
}

func hasLastInspection(value ConvergenceWaitObservation) bool {
	return value.LastInspectionStage != "" || value.LastInspectionClass != "" ||
		value.LastInspectionHTTPStatus != 0 || value.LastInspectionHTTPReason != "" ||
		value.LastInspectionSHA256 != "" || value.LastInspectionWallMS != 0 ||
		value.RepositoryIndexFailureClass != ""
}

func validTransitionHTTPDiagnostic(transition ConvergenceTransitionObservation, detailVersion int) bool {
	if detailVersion < 3 {
		return transition.HTTPStatus == 0 && transition.HTTPReason == ""
	}
	if transition.Class != "status" {
		return transition.HTTPStatus == 0 && transition.HTTPReason == ""
	}
	return validHTTPDiagnostic(transition.HTTPStatus, transition.HTTPReason, detailVersion)
}

func validHTTPDiagnostic(status int, reason string, detailVersion int) bool {
	if status < 100 || status > 599 {
		return false
	}
	switch reason {
	case httpReason409Stale, httpReason409ControlAbsent:
		return status == 409
	case httpReason500Store:
		return status == 500
	case httpReason500Projection:
		return detailVersion <= 3 && status == 500
	case httpReason500Control, httpReason500Publication, httpReason500Planning,
		httpReason500Schedule, httpReason500Response:
		return detailVersion >= 4 && status == 500
	case httpReason401Unauthorized:
		return status == 401
	case httpReason403Forbidden:
		return status == 403
	case httpReason404NotFound:
		return status == 404
	case httpReason503Unavailable:
		return status == 503
	case httpReasonOther:
		return true
	default:
		return false
	}
}

func validateLastInspection(
	value ConvergenceWaitObservation,
	stages []string,
	detailVersion int,
	lastTransition ConvergenceTransitionObservation,
) error {
	if detailVersion < 3 {
		if hasLastInspection(value) {
			return errors.New("T40.13 pre-v7 convergence wait acquired last-inspection diagnostics")
		}
		return nil
	}
	classes := []string{"pending", "transport", "status", "response", "control", "complete"}
	if detailVersion >= 5 {
		classes = append(classes, "terminal")
	}
	if !slices.Contains(stages, value.LastInspectionStage) ||
		value.LastInspectionStage == convergenceNotInspected ||
		!slices.Contains(classes, value.LastInspectionClass) ||
		!digestIdentity(value.LastInspectionSHA256) || value.LastInspectionWallMS <= 0 ||
		value.LastInspectionWallMS > value.WallMS || value.LastInspectionWallMS < lastTransition.WallMS {
		return errors.New("T40.13 last completed inspection is invalid")
	}
	if value.LastInspectionClass == "status" {
		if !validHTTPDiagnostic(value.LastInspectionHTTPStatus, value.LastInspectionHTTPReason, detailVersion) {
			return errors.New("T40.13 last HTTP inspection diagnostic is invalid")
		}
	} else if value.LastInspectionHTTPStatus != 0 || value.LastInspectionHTTPReason != "" {
		return errors.New("T40.13 non-status inspection retained HTTP diagnostics")
	}
	matchesTimelineTail := value.LastInspectionStage == lastTransition.Stage &&
		value.LastInspectionClass == lastTransition.Class &&
		value.LastInspectionHTTPStatus == lastTransition.HTTPStatus &&
		value.LastInspectionHTTPReason == lastTransition.HTTPReason &&
		value.LastInspectionSHA256 == lastTransition.ProgressSHA256
	if matchesTimelineTail == value.TransitionLimitExceeded {
		return errors.New("T40.13 last completed inspection is invalid")
	}
	return nil
}

func validateObservationProgress(value ObservationProgressObservation) error {
	if !slices.Contains([]string{"current", "building", "failed", "stale", "unavailable"}, value.State) ||
		!optionalProgressState(value.PlanningState) || !optionalProgressState(value.ScheduleState) ||
		!optionalPublicationState(value.PublicationState) ||
		value.PlanningPending < 0 || value.PlanningRunning < 0 || value.PlanningSucceeded < 0 ||
		value.PlanningFailed < 0 || value.ScheduleTotalPartitions < 0 || value.ScheduleMaterialized < 0 ||
		value.SchedulePending < 0 || value.ScheduleRunning < 0 || value.ScheduleSucceeded < 0 ||
		value.ScheduleFailed < 0 || value.PublicationRecordCount < 0 ||
		value.PublicationObservedCount < 0 || value.PublicationUnsupportedCount < 0 {
		return errors.New("T40.13 observation progress projection is invalid")
	}
	if value.PlanningState == "" &&
		(value.PlanningPending != 0 || value.PlanningRunning != 0 ||
			value.PlanningSucceeded != 0 || value.PlanningFailed != 0) {
		return errors.New("T40.13 absent planning progress has counters")
	}
	if value.PlanningState != "" &&
		(value.PlanningPending > 1 || value.PlanningRunning > 1 ||
			value.PlanningSucceeded > 1 || value.PlanningFailed > 1 ||
			value.PlanningPending+value.PlanningRunning+
				value.PlanningSucceeded+value.PlanningFailed > 1) {
		return errors.New("T40.13 planning progress counters are incoherent")
	}
	if value.ScheduleState == "" {
		if value.ScheduleTotalPartitions != 0 || value.ScheduleMaterialized != 0 ||
			value.SchedulePending != 0 || value.ScheduleRunning != 0 ||
			value.ScheduleSucceeded != 0 || value.ScheduleFailed != 0 {
			return errors.New("T40.13 absent schedule progress has counters")
		}
	} else if value.ScheduleTotalPartitions < 1 ||
		value.ScheduleTotalPartitions > store.MaxGenerationChunks ||
		value.ScheduleMaterialized > value.ScheduleTotalPartitions*observationpublication.ScheduleMaxAttempts ||
		value.SchedulePending+value.ScheduleRunning > value.ScheduleMaterialized ||
		value.ScheduleSucceeded+value.ScheduleFailed > value.ScheduleTotalPartitions {
		return errors.New("T40.13 schedule progress counters are incoherent")
	}
	if value.ScheduleState == "settled" &&
		(value.SchedulePending != 0 || value.ScheduleRunning != 0 || value.ScheduleFailed != 0 ||
			value.ScheduleSucceeded != value.ScheduleTotalPartitions) ||
		value.ScheduleState == "failed" &&
			(value.SchedulePending != 0 || value.ScheduleRunning != 0 || value.ScheduleFailed == 0 ||
				value.ScheduleSucceeded+value.ScheduleFailed != value.ScheduleTotalPartitions) {
		return errors.New("T40.13 schedule progress state is incoherent")
	}
	if value.PlanningState == "active" && (value.PlanningSucceeded != 0 || value.PlanningFailed != 0) ||
		value.PlanningState == "settled" &&
			(value.PlanningPending != 0 || value.PlanningRunning != 0 ||
				value.PlanningSucceeded != 1 || value.PlanningFailed != 0) ||
		value.PlanningState == "failed" &&
			(value.PlanningPending != 0 || value.PlanningRunning != 0 ||
				value.PlanningSucceeded != 0 || value.PlanningFailed != 1) {
		return errors.New("T40.13 planning progress state is incoherent")
	}
	if value.PublicationState == "" &&
		(value.PublicationRecordCount != 0 || value.PublicationObservedCount != 0 ||
			value.PublicationUnsupportedCount != 0) {
		return errors.New("T40.13 absent publication progress has counters")
	}
	if value.PublicationState != "" &&
		(value.PublicationRecordCount > observationpublication.MaxGenerationRecords ||
			value.PublicationObservedCount+value.PublicationUnsupportedCount != value.PublicationRecordCount) {
		return errors.New("T40.13 publication progress counters are incoherent")
	}
	switch value.State {
	case "current":
		if value.PublicationState != "current" {
			return errors.New("T40.13 current observation progress lacks current publication authority")
		}
	case "building":
		if value.PlanningState != "active" && value.ScheduleState != "active" {
			return errors.New("T40.13 building observation progress lacks active work")
		}
	case "failed":
		if value.PlanningState != "failed" && value.ScheduleState != "failed" {
			return errors.New("T40.13 failed observation progress lacks failed work")
		}
	case "unavailable":
		if value.PublicationState == "current" ||
			value.PlanningState == "active" || value.PlanningState == "failed" ||
			value.ScheduleState == "active" || value.ScheduleState == "failed" {
			return errors.New("T40.13 unavailable observation progress retains selected work")
		}
	}
	return nil
}

func optionalProgressState(value string) bool {
	return value == "" || slices.Contains([]string{"active", "settled", "failed"}, value)
}

func optionalPublicationState(value string) bool {
	return value == "" || slices.Contains([]string{"current", "stale"}, value)
}

func validateStopped(value Receipt) error {
	if len(value.Failures) != 1 || value.Decision.Selected == "continue" ||
		!value.Teardown.Completed || value.Teardown.DerivedDataRetained || value.Teardown.ScratchSourceRetained {
		return errors.New("stopped T40.13 receipt lacks one failure, a stop decision, or teardown")
	}
	failure := value.Failures[0]
	failed := -1
	for index, phase := range value.Phases {
		if phase.Outcome == "failed" {
			if failed >= 0 {
				return errors.New("stopped T40.13 receipt has an invalid failed-phase inventory")
			}
			failed = index
		}
	}
	if failed < 0 || value.Phases[failed].Name != failure.Phase {
		return errors.New("stopped T40.13 failure does not bind the failed phase")
	}
	for index := 0; index < failed; index++ {
		if value.Phases[index].Outcome != "succeeded" {
			return errors.New("stopped T40.13 receipt has an incomplete settled prefix")
		}
	}
	for index := failed + 1; index < len(value.Phases)-1; index++ {
		if value.Phases[index].Outcome != "not_run" {
			return errors.New("stopped T40.13 receipt ran work after its failed phase")
		}
	}
	last := len(value.Phases) - 1
	if failed != last && value.Phases[last].Outcome != "succeeded" ||
		failed == last && value.Phases[last].Outcome != "failed" {
		return errors.New("stopped T40.13 receipt lacks successful teardown accounting")
	}
	wantDecision, wantReason := "unclassified", "failed_phase_measurement_unavailable"
	switch failure.Code {
	case "review_ceiling_crossed":
		if failure.Class != "environment" {
			return errors.New("T40.13 review ceiling failure class is invalid")
		}
		wantDecision, wantReason = "cohort_experiment", "frozen_review_ceiling_crossed"
	case "direct_recovery_failed":
		if failure.Class != "recovery" || failure.Phase != "interruption" && failure.Phase != "archive_restore" {
			return errors.New("T40.13 recovery failure identity is invalid")
		}
		wantDecision, wantReason = "p6_investigation", "direct_recovery_failed"
	case "production_pressure_gate_refused":
		if failure.Class != "lifecycle" || failure.Phase != "pressure" {
			return errors.New("T40.13 pressure failure identity is invalid")
		}
		wantDecision, wantReason = "reduce", "production_pressure_gate_refused"
	case "exact_gate_failed":
		if failure.Class != "oracle" {
			return errors.New("T40.13 exact-gate failure identity is invalid")
		}
		wantDecision, wantReason = "reduce", "exact_mechanics_oracle_failed"
	case "failed_phase_measurement_unavailable":
		if failure.Class != "oracle" {
			return errors.New("T40.13 unclassified failure identity is invalid")
		}
	case "operational_failure":
		if failure.Class != "execution" {
			return errors.New("T40.13 operational failure identity is invalid")
		}
		wantReason = "operational_failure"
	case "convergence_deadline_expired":
		if failure.Class != "execution" || len(value.ConvergenceWaits) == 0 ||
			value.ConvergenceWaits[len(value.ConvergenceWaits)-1].Outcome != "deadline" {
			return errors.New("T40.13 convergence deadline failure identity is invalid")
		}
		wantReason = "convergence_deadline_expired"
	case "server_exited_during_convergence":
		if failure.Class != "execution" || len(value.ConvergenceWaits) == 0 ||
			value.ConvergenceWaits[len(value.ConvergenceWaits)-1].Outcome != "server_exited" {
			return errors.New("T40.13 convergence server-exit identity is invalid")
		}
		wantReason = "server_exited_during_convergence"
	case "convergence_transition_limit_exceeded":
		if failure.Class != "oracle" || len(value.ConvergenceWaits) == 0 ||
			value.ConvergenceWaits[len(value.ConvergenceWaits)-1].Outcome != "diagnostic_limit" ||
			!value.ConvergenceWaits[len(value.ConvergenceWaits)-1].TransitionLimitExceeded {
			return errors.New("T40.13 convergence transition-limit identity is invalid")
		}
		wantReason = "convergence_transition_limit_exceeded"
	case "repository_index_terminal":
		if failure.Class != "execution" || len(value.ConvergenceWaits) == 0 ||
			value.ConvergenceWaits[len(value.ConvergenceWaits)-1].Outcome != "repository_index_terminal" {
			return errors.New("T40.13 repository-index terminal identity is invalid")
		}
		wantReason = "repository_index_terminal"
	default:
		return errors.New("T40.13 stopped failure code is not frozen")
	}
	if value.Decision.Selected != wantDecision || value.Decision.Reason != wantReason {
		return errors.New("T40.13 stopped decision does not match its frozen failure rule")
	}
	return nil
}

func isLegacyStoppedReceiptIdentity(value Receipt) bool {
	return value.PlanDigest == legacyStoppedPlanDigest &&
		value.SourceCommit == legacyStoppedSourceCommit && value.MeasuredOn == "2026-08-08" &&
		value.Outcome == "stopped" && len(value.Toolchain) == 0 && len(value.Failures) == 1 &&
		value.Failures[0] == (FailureObservation{
			Phase: "cold", Class: "oracle", Code: "failed_phase_measurement_unavailable",
		}) && value.Decision == (DecisionObservation{
		Selected: "unclassified", Reason: "failed_phase_measurement_unavailable", Substantiated: false,
	})
}

func DecodeReceipt(raw []byte, plan Plan) (Receipt, error) {
	if len(raw) == 0 || len(raw) > MaxReceiptBytes {
		return Receipt{}, errors.New("T40.13 receipt is outside its fixed byte bound")
	}
	var value Receipt
	if err := decodeStrict(raw, &value); err != nil {
		return Receipt{}, fmt.Errorf("decode T40.13 receipt: %w", err)
	}
	if err := validateReceipt(value, plan, PlanDigest(raw) == legacyStoppedReceiptDigest); err != nil {
		return Receipt{}, err
	}
	return value, nil
}

func validateCompleted(value Receipt, plan Plan) error {
	if plan.Schema == PlanSchemaV3 || plan.Schema == PlanSchemaV4 || plan.Schema == PlanSchemaV5 || plan.Schema == PlanSchemaV6 || plan.Schema == PlanSchemaV7 || plan.Schema == PlanSchemaV8 || plan.Schema == PlanSchemaV9 || plan.Schema == PlanSchemaV10 {
		want := [][2]string{
			{"structural-2m-v1", "cold"}, {"semantic-262144-v1", "cold"},
			{"structural-2m-v1", "warm-noop"}, {"semantic-262144-v1", "interruption-first"},
			{"semantic-262144-v1", "interruption-restart"}, {"semantic-262144-v1", "stale-worker"},
			{"structural-2m-v1", "archive-restore"}, {"semantic-262144-v1", "authorized-query"},
		}
		if plan.Schema == PlanSchemaV10 {
			want = slices.Insert(want, 6, [2]string{"structural-2m-v1", "pressure-restart"})
		}
		if len(value.ServerStartups) != len(want) {
			return errors.New("T40.13 completed receipt lacks the exact startup inventory")
		}
		for index, identity := range want {
			startup := value.ServerStartups[index]
			if startup.Profile != identity[0] || startup.Label != identity[1] || startup.Outcome != "healthy" {
				return errors.New("T40.13 completed receipt startup inventory is invalid")
			}
		}
	}
	if plan.Schema == PlanSchemaV4 || plan.Schema == PlanSchemaV5 || plan.Schema == PlanSchemaV6 || plan.Schema == PlanSchemaV7 || plan.Schema == PlanSchemaV8 || plan.Schema == PlanSchemaV9 || plan.Schema == PlanSchemaV10 {
		want := [][3]string{
			{"structural-2m-v1", "cold", "a"}, {"semantic-262144-v1", "cold", "a"},
			{"structural-2m-v1", "warm-noop", "a"}, {"structural-2m-v1", "delta-b", "b"},
			{"structural-2m-v1", "return-a", "a-return"}, {"semantic-262144-v1", "interruption-restart", "a"},
			{"semantic-262144-v1", "stale-worker", "a"}, {"structural-2m-v1", "pressure", "a-return"},
			{"structural-2m-v1", "archive-restore", "a-return"}, {"structural-2m-v1", "collection", "a-return"},
			{"semantic-262144-v1", "authorized-query-semantic", "a"},
			{"structural-2m-v1", "authorized-query-structural", "a-return"},
		}
		if len(value.ConvergenceWaits) != len(want) {
			return errors.New("T40.13 completed receipt lacks the exact convergence wait inventory")
		}
		for index, identity := range want {
			wait := value.ConvergenceWaits[index]
			if wait.Profile != identity[0] || wait.Label != identity[1] || wait.Revision != identity[2] ||
				wait.Outcome != "converged" {
				return errors.New("T40.13 completed receipt convergence wait inventory is invalid")
			}
		}
	}
	structural, semantic := value.Profiles[0], value.Profiles[1]
	if structural.RegularFiles != 2_000_002 || structural.PhysicalOwners != 2_000_002 ||
		structural.EligibleGoFiles != 2_000_000 || structural.IDLCandidates != 0 ||
		structural.DeclaredSourceBytes != 9_216_000_076 || !structural.SearchPublished ||
		structural.ApplicablePartitions == 0 || structural.SettledPartitions != structural.ApplicablePartitions ||
		structural.PublishedDomains == 0 || !structural.RelationshipPublished {
		return errors.New("T40.13 structural profile did not converge exactly")
	}
	if semantic.RegularFiles != 294_914 || semantic.PhysicalOwners != 294_914 ||
		semantic.EligibleGoFiles != 262_144 || semantic.IDLCandidates != 32_768 ||
		semantic.DeclaredSourceBytes != 146_800_716 || !semantic.SearchPublished ||
		semantic.ApplicablePartitions == 0 || semantic.SettledPartitions != semantic.ApplicablePartitions ||
		semantic.PublishedDomains == 0 || !semantic.RelationshipPublished {
		return errors.New("T40.13 semantic profile did not converge exactly")
	}
	if len(value.BlobReaders) != 4 {
		return errors.New("T40.13 completed receipt lacks four exact search-generation reader receipts")
	}
	wantReaders := []struct {
		profile, revision, mode string
		files                   uint64
	}{
		{"structural-2m-v1", "a", "go_git", 2_000_002},
		{"structural-2m-v1", "b", "go_git", 2_000_002},
		{"structural-2m-v1", "a-return", "go_git", 2_000_002},
		{"semantic-262144-v1", "a", "go_git", 294_914},
	}
	for index, want := range wantReaders {
		got := value.BlobReaders[index]
		if got.Profile != want.profile || got.Revision != want.revision || got.Mode != want.mode ||
			got.FilesOffered != want.files || got.FallbackReads != want.files {
			return errors.New("T40.13 blob-reader receipt order is invalid")
		}
	}
	service := value.Service
	if service.AcceptedServices != 100 || service.Memberships != 100 || service.DistinctPaths != 201 ||
		service.DistinctPaths != service.Memberships+service.UnownedPrefixes || service.UnownedPrefixes != 101 ||
		!service.WithinV2PathLimit || !service.ExactMembershipOracle ||
		!service.ExactUnownedOracle {
		return errors.New("T40.13 small service control is invalid")
	}
	if value.Explicit.AbsentTypedInputs < 1 || value.Explicit.AbsentTypedInputs != value.Explicit.UnavailableDomains ||
		value.Explicit.UnsupportedSyntaxFacts != 16_384 || value.Explicit.GapFacts != 131_072 ||
		!value.Explicit.NoSilentEmpty {
		return errors.New("T40.13 explicit gap accounting is incomplete")
	}
	var peakRSS, maxAllocated, totalWall int64
	for _, phase := range value.Phases {
		totalWall += phase.Metrics.WallMS
		peakRSS = max(peakRSS, phase.Metrics.PeakRSSBytes)
		maxAllocated = max(maxAllocated, phase.Metrics.DataAllocatedBytes)
	}
	if totalWall > plan.Safety.MaximumTotalWallMS || peakRSS > plan.Safety.MaximumPeakRSSBytes ||
		maxAllocated > plan.Safety.MaximumDataAllocatedBytes {
		return errors.New("T40.13 completed receipt crossed a frozen safety ceiling")
	}
	noop := value.Phases[2]
	if noop.Metrics.GitChildren != 0 || noop.Metrics.IndexChildren != 0 ||
		noop.Metrics.PublicationWrites != 0 || noop.Metrics.PublicationTransactions != 0 ||
		noop.AuthorityChanged || noop.Metrics.ReusedControls == 0 {
		return errors.New("T40.13 warm no-op was not an exact reuse")
	}
	return nil
}

func PlanDigest(raw []byte) string { return digest(raw) }

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func nonnegativeMetrics(value PhaseMetrics) bool {
	return value.WallMS >= 0 && value.PeakRSSBytes >= 0 && value.DataLogicalBytes >= 0 &&
		value.DataAllocatedBytes >= 0 && value.GitChildren >= 0 && value.IndexChildren >= 0 &&
		value.OtherChildren >= 0 && value.ControlReads >= 0 && value.MemberReads >= 0 &&
		value.PublicationWrites >= 0 && value.PublicationTransactions >= 0 &&
		value.OrchestrationTransactions >= 0 && value.Retries >= 0 &&
		value.ReusedControls >= 0 && value.ReusedMembers >= 0
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestIdentity(value string) bool {
	return strings.HasPrefix(value, "sha256:") && hexIdentity(strings.TrimPrefix(value, "sha256:"), 64)
}

func hexIdentity(value string, size int) bool {
	if len(value) != size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func date(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

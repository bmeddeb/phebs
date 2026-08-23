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

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	PlanSchema           = "t4013-neutral-convergence-plan-v1"
	PlanSchemaV2         = "t4013-neutral-convergence-plan-v2"
	PlanSchemaV3         = "t4013-neutral-convergence-plan-v3"
	PlanSchemaV4         = "t4013-neutral-convergence-plan-v4"
	PlanSchemaV5         = "t4013-neutral-convergence-plan-v5"
	PlanSchemaV6         = "t4013-neutral-convergence-plan-v6"
	PlanSchemaV7         = "t4013-neutral-convergence-plan-v7"
	PlanSchemaV8         = "t4013-neutral-convergence-plan-v8"
	PlanSchemaV9         = "t4013-neutral-convergence-plan-v9"
	PlanSchemaV10        = "t4013-neutral-convergence-plan-v10"
	PlanSchemaV11        = "t4013-neutral-convergence-plan-v11"
	PlanSchemaV12        = "t4013-neutral-convergence-plan-v12"
	PlanSchemaV13        = "t4013-neutral-convergence-plan-v13"
	PlanSchemaV14        = "t4013-neutral-convergence-plan-v14"
	PlanSchemaV15        = "t4013-neutral-convergence-plan-v15"
	PlanSchemaV16        = "t4013-neutral-convergence-plan-v16"
	PlanSchemaV17        = "t4013-neutral-convergence-plan-v17"
	PlanSchemaV18        = "t4013-neutral-convergence-plan-v18"
	PlanSchemaV19        = "t4013-neutral-convergence-plan-v19"
	PlanSchemaV20        = "t4013-neutral-convergence-plan-v20"
	PlanSchemaV21        = "t4013-neutral-convergence-plan-v21"
	PlanSchemaV22        = "t4013-neutral-convergence-plan-v22"
	PlanSchemaV23        = "t4013-neutral-convergence-plan-v23"
	PlanSchemaV24        = "t4013-neutral-convergence-plan-v24"
	PlanSchemaV25        = "t4013-neutral-convergence-plan-v25"
	ObservationSchema    = "t4013-neutral-convergence-observation-v1"
	ObservationSchemaV2  = "t4013-neutral-convergence-observation-v2"
	ObservationSchemaV3  = "t4013-neutral-convergence-observation-v3"
	ObservationSchemaV4  = "t4013-neutral-convergence-observation-v4"
	ObservationSchemaV5  = "t4013-neutral-convergence-observation-v5"
	ObservationSchemaV6  = "t4013-neutral-convergence-observation-v6"
	ObservationSchemaV7  = "t4013-neutral-convergence-observation-v7"
	ObservationSchemaV8  = "t4013-neutral-convergence-observation-v8"
	ObservationSchemaV9  = "t4013-neutral-convergence-observation-v9"
	ObservationSchemaV10 = "t4013-neutral-convergence-observation-v10"
	ObservationSchemaV11 = "t4013-neutral-convergence-observation-v11"
	ObservationSchemaV12 = "t4013-neutral-convergence-observation-v12"
	ObservationSchemaV13 = "t4013-neutral-convergence-observation-v13"
	ObservationSchemaV14 = "t4013-neutral-convergence-observation-v14"
	ObservationSchemaV15 = "t4013-neutral-convergence-observation-v15"
	ObservationSchemaV16 = "t4013-neutral-convergence-observation-v16"
	ObservationSchemaV17 = "t4013-neutral-convergence-observation-v17"
	ObservationSchemaV18 = "t4013-neutral-convergence-observation-v18"
	ObservationSchemaV19 = "t4013-neutral-convergence-observation-v19"
	ObservationSchemaV20 = "t4013-neutral-convergence-observation-v20"
	ObservationSchemaV21 = "t4013-neutral-convergence-observation-v21"
	ObservationSchemaV22 = "t4013-neutral-convergence-observation-v22"
	ObservationSchemaV23 = "t4013-neutral-convergence-observation-v23"
	ObservationSchemaV24 = "t4013-neutral-convergence-observation-v24"
	ObservationSchemaV25 = "t4013-neutral-convergence-observation-v25"

	// observationDetailV15 is the convergence-wait detail version introduced
	// by the V15 schemas: extraction schedule authority takes precedence over
	// the repository-keyed job projection. Keep every "detailVersion >= 11"
	// comparison on this name so the next version bumps one constant.
	observationDetailV15 = 11
	// observationDetailV16 records the closed relationship failure boundary
	// and the repeated source-free terminal-pair confirmation.
	observationDetailV16 = 12
	// observationDetailV17 records the caller-leaf job projection beside the
	// caller-generation probe: a dead caller pipeline is a typed terminal and
	// a live requeued publisher holds the terminal stop.
	observationDetailV17         = 13
	ReceiptSchema                = "t4013-neutral-convergence-receipt-v1"
	ReceiptSchemaV2              = "t4013-neutral-convergence-receipt-v2"
	ReceiptSchemaV3              = "t4013-neutral-convergence-receipt-v3"
	ReceiptSchemaV4              = "t4013-neutral-convergence-receipt-v4"
	ReceiptSchemaV5              = "t4013-neutral-convergence-receipt-v5"
	ReceiptSchemaV6              = "t4013-neutral-convergence-receipt-v6"
	ReceiptSchemaV7              = "t4013-neutral-convergence-receipt-v7"
	ReceiptSchemaV8              = "t4013-neutral-convergence-receipt-v8"
	ReceiptSchemaV9              = "t4013-neutral-convergence-receipt-v9"
	ReceiptSchemaV10             = "t4013-neutral-convergence-receipt-v10"
	ReceiptSchemaV11             = "t4013-neutral-convergence-receipt-v11"
	ReceiptSchemaV12             = "t4013-neutral-convergence-receipt-v12"
	ReceiptSchemaV13             = "t4013-neutral-convergence-receipt-v13"
	ReceiptSchemaV14             = "t4013-neutral-convergence-receipt-v14"
	ReceiptSchemaV15             = "t4013-neutral-convergence-receipt-v15"
	ReceiptSchemaV16             = "t4013-neutral-convergence-receipt-v16"
	ReceiptSchemaV17             = "t4013-neutral-convergence-receipt-v17"
	ReceiptSchemaV18             = "t4013-neutral-convergence-receipt-v18"
	ReceiptSchemaV19             = "t4013-neutral-convergence-receipt-v19"
	ReceiptSchemaV20             = "t4013-neutral-convergence-receipt-v20"
	ReceiptSchemaV21             = "t4013-neutral-convergence-receipt-v21"
	ReceiptSchemaV22             = "t4013-neutral-convergence-receipt-v22"
	ReceiptSchemaV23             = "t4013-neutral-convergence-receipt-v23"
	ReceiptSchemaV24             = "t4013-neutral-convergence-receipt-v24"
	ReceiptSchemaV25             = "t4013-neutral-convergence-receipt-v25"
	MaxPlanBytes                 = 64 << 10
	MaxObservationBytes          = 256 << 10
	MaxReceiptBytes              = 256 << 10
	legacyStoppedPlanDigest      = "sha256:13863ed6e0e19e3edf5cbaa2e6d2f79eef645341661a5d61c0066f7f009974a0"
	legacyStoppedSourceCommit    = "b1b4e808e1987b3bf28e4afac21cc83b72aa27f2"
	legacyStoppedReceiptDigest   = "sha256:873c373353c540d05e61b243b63befd781e7280b4ec52c0ddd4ef074661e4c85"
	maxConvergenceTransitions    = 32
	maxCallerRefusalObservations = 32
	convergenceNotInspected      = "not_inspected"
)

type ceremonySchemaSet struct {
	plan        string
	observation string
	receipt     string
}

// ceremonySchemaLadder is the one ordered registry for the three coupled
// serialized contracts. Index zero is deliberately invalid so the array index
// is also the schema version.
var ceremonySchemaLadder = [...]ceremonySchemaSet{
	{},
	{PlanSchema, ObservationSchema, ReceiptSchema},
	{PlanSchemaV2, ObservationSchemaV2, ReceiptSchemaV2},
	{PlanSchemaV3, ObservationSchemaV3, ReceiptSchemaV3},
	{PlanSchemaV4, ObservationSchemaV4, ReceiptSchemaV4},
	{PlanSchemaV5, ObservationSchemaV5, ReceiptSchemaV5},
	{PlanSchemaV6, ObservationSchemaV6, ReceiptSchemaV6},
	{PlanSchemaV7, ObservationSchemaV7, ReceiptSchemaV7},
	{PlanSchemaV8, ObservationSchemaV8, ReceiptSchemaV8},
	{PlanSchemaV9, ObservationSchemaV9, ReceiptSchemaV9},
	{PlanSchemaV10, ObservationSchemaV10, ReceiptSchemaV10},
	{PlanSchemaV11, ObservationSchemaV11, ReceiptSchemaV11},
	{PlanSchemaV12, ObservationSchemaV12, ReceiptSchemaV12},
	{PlanSchemaV13, ObservationSchemaV13, ReceiptSchemaV13},
	{PlanSchemaV14, ObservationSchemaV14, ReceiptSchemaV14},
	{PlanSchemaV15, ObservationSchemaV15, ReceiptSchemaV15},
	{PlanSchemaV16, ObservationSchemaV16, ReceiptSchemaV16},
	{PlanSchemaV17, ObservationSchemaV17, ReceiptSchemaV17},
	{PlanSchemaV18, ObservationSchemaV18, ReceiptSchemaV18},
	{PlanSchemaV19, ObservationSchemaV19, ReceiptSchemaV19},
	{PlanSchemaV20, ObservationSchemaV20, ReceiptSchemaV20},
	{PlanSchemaV21, ObservationSchemaV21, ReceiptSchemaV21},
	{PlanSchemaV22, ObservationSchemaV22, ReceiptSchemaV22},
	{PlanSchemaV23, ObservationSchemaV23, ReceiptSchemaV23},
	{PlanSchemaV24, ObservationSchemaV24, ReceiptSchemaV24},
	{PlanSchemaV25, ObservationSchemaV25, ReceiptSchemaV25},
}

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
	MaximumPrePressureBytes     int64 `json:"maximum_pre_pressure_allocated_bytes,omitempty"`
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
	Interruption     *InterruptionObservation     `json:"interruption,omitempty"`
	AuthorizedQuery  *AuthorizedQueryObservation  `json:"authorized_query,omitempty"`
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

const authorizedQuerySchemaV1 = "t4013-authorized-query-v1"

// AuthorizedQueryObservation is a bounded source-free failure projection.
// It names only the frozen profile and endpoint class; URLs, repository names,
// response bodies, credentials, and transport errors remain in custody.
type AuthorizedQueryObservation struct {
	Schema   string `json:"schema"`
	Profile  string `json:"profile"`
	Query    string `json:"query"`
	Class    string `json:"class"`
	Status   int    `json:"status,omitempty"`
	Attempts int    `json:"attempts"`
}

const (
	interruptionSchemaV1      = "t4013-interruption-v1"
	interruptionSchemaV2      = "t4013-interruption-v2"
	interruptionFateCollected = "collected"
	interruptionFateRequeued  = "requeued"
)

// InterruptionObservation retains the exact source-free lifecycle lease used
// to stop the first restored server and the last closed harness substage. Raw
// process output, repository/source identities, paths, and lease ownership
// remain private and are destroyed with custody.
type InterruptionObservation struct {
	Schema                  string `json:"schema,omitempty"`
	LastSubstage            string `json:"last_substage,omitempty"`
	TriggerStage            string `json:"trigger_stage,omitempty"`
	TriggerGenerationSHA256 string `json:"trigger_generation_sha256,omitempty"`
	TriggerChunkSHA256      string `json:"trigger_chunk_sha256,omitempty"`
	TriggerScheduleSHA256   string `json:"trigger_schedule_sha256,omitempty"`
	TriggerAttempt          *int   `json:"trigger_attempt,omitempty"`
	TriggerWallMS           int64  `json:"trigger_wall_ms,omitempty"`
	// TriggerRecoveredState is the trigger chunk's observed post-restart
	// status. The graceful stop drain may settle or release the selected
	// lease before process exit, so the receipt records what the restart
	// actually recovered instead of asserting an interrupted lease.
	TriggerRecoveredState string `json:"trigger_recovered_state,omitempty"`
	// The last V18 exact-inspector projection makes a no-trigger stop
	// attributable without retaining private responses or raw errors.
	LastProgressStage  string `json:"last_progress_stage,omitempty"`
	LastProgressClass  string `json:"last_progress_class,omitempty"`
	LastProgressSHA256 string `json:"last_progress_sha256,omitempty"`
	LastProgressWallMS int64  `json:"last_progress_wall_ms,omitempty"`
	ProgressChanges    int64  `json:"progress_changes,omitempty"`
	// Lifecycle snapshots are bounded last-cycle facts for the generation
	// schedule owner. They make collection directly observable without
	// retaining paths, timestamps, cursors, or store rows.
	RecoveryLifecycle    *InterruptionLifecycleObservation `json:"recovery_lifecycle,omitempty"`
	ConvergenceLifecycle *InterruptionLifecycleObservation `json:"convergence_lifecycle,omitempty"`
}

type InterruptionLifecycleObservation struct {
	State        string                 `json:"state"`
	Completeness lifecycle.Completeness `json:"completeness"`
	Scanned      int                    `json:"scanned"`
	Deleted      int                    `json:"deleted"`
	Backlog      bool                   `json:"backlog"`
}

// The versioned interruption substage inventories are closed and ordered.
// The setter and validator select the same inventory, so a call-site typo can
// cost precision but can never make a stopped observation unsealable; V21 and
// earlier retain the order they froze.
var interruptionSubstagesV21 = []string{
	"not_started", "backup_start", "backup_revalidation", "backup_create",
	"backup_stop", "restore", "restored_boundary", "first_start",
	"delta_trigger", "active_lease_wait", "first_stop", "source_return",
	"restart_start", "restart_convergence", "recovery_verification",
	"teardown", "complete",
}

var interruptionSubstagesV22 = []string{
	"not_started", "backup_start", "backup_revalidation", "backup_create",
	"backup_stop", "restore", "restored_boundary", "first_start",
	"delta_trigger", "active_lease_wait", "first_stop", "source_return",
	"restart_start", "recovery_verification", "restart_convergence",
	"partial_verification", "teardown", "complete",
}

func interruptionSubstagesForSchema(schema string) []string {
	if observationSchemaVersion(schema) >= 22 {
		return interruptionSubstagesV22
	}
	return interruptionSubstagesV21
}

func interruptionSubstageIndex(schema, name string) int {
	return slices.Index(interruptionSubstagesForSchema(schema), name)
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
	Profile                           string                             `json:"profile"`
	Label                             string                             `json:"label"`
	Revision                          string                             `json:"revision"`
	Outcome                           string                             `json:"outcome"`
	FirstStage                        string                             `json:"first_stage,omitempty"`
	LastStage                         string                             `json:"last_stage"`
	Attempts                          int64                              `json:"attempts"`
	ProgressChanges                   int64                              `json:"progress_changes"`
	StageChanges                      int64                              `json:"stage_changes,omitempty"`
	FirstProgressSHA256               string                             `json:"first_progress_sha256"`
	LastProgressSHA256                string                             `json:"last_progress_sha256"`
	LastProgressChangeWallMS          int64                              `json:"last_progress_change_wall_ms,omitempty"`
	ObservationProgress               *ObservationProgressObservation    `json:"observation_progress,omitempty"`
	ObservationProgressWallMS         int64                              `json:"observation_progress_wall_ms,omitempty"`
	ExtractionProgress                *ExtractionProgressObservation     `json:"extraction_progress,omitempty"`
	ExtractionProgressWallMS          int64                              `json:"extraction_progress_wall_ms,omitempty"`
	ExtractionTiming                  *ExtractionTimingObservation       `json:"extraction_timing,omitempty"`
	CallerProgress                    *CallerProgressObservation         `json:"caller_progress,omitempty"`
	CallerProgressWallMS              int64                              `json:"caller_progress_wall_ms,omitempty"`
	InspectionTransitions             []ConvergenceTransitionObservation `json:"inspection_transitions,omitempty"`
	LastSuccessfulProbeSHA256         string                             `json:"last_successful_probe_sha256,omitempty"`
	LastSuccessfulProbeWallMS         int64                              `json:"last_successful_probe_wall_ms,omitempty"`
	LastInspectionStage               string                             `json:"last_inspection_stage,omitempty"`
	LastInspectionClass               string                             `json:"last_inspection_class,omitempty"`
	LastInspectionHTTPStatus          int                                `json:"last_inspection_http_status,omitempty"`
	LastInspectionHTTPReason          string                             `json:"last_inspection_http_reason,omitempty"`
	LastInspectionSHA256              string                             `json:"last_inspection_sha256,omitempty"`
	LastInspectionWallMS              int64                              `json:"last_inspection_wall_ms,omitempty"`
	RepositoryIndexFailureClass       string                             `json:"repository_index_failure_class,omitempty"`
	RelationshipFailureClass          string                             `json:"relationship_failure_class,omitempty"`
	RelationshipTerminalConfirmations int64                              `json:"relationship_terminal_confirmations,omitempty"`
	TransitionLimitExceeded           bool                               `json:"transition_limit_exceeded,omitempty"`
	DeadlineMS                        int64                              `json:"deadline_ms"`
	WallMS                            int64                              `json:"wall_ms"`
}

// ExtractionTimingObservation aggregates source-free per-attempt phase
// timings. It intentionally retains no repository, path, content, error, or
// per-partition identity.
type ExtractionTimingObservation struct {
	Schema               string                     `json:"schema,omitempty"`
	Attempts             int64                      `json:"attempts"`
	Completed            int64                      `json:"completed"`
	Failed               int64                      `json:"failed"`
	TerminalRefusals     int64                      `json:"terminal_refusals"`
	Reused               int64                      `json:"reused"`
	SourceAcquireTotalMS int64                      `json:"source_acquire_total_ms"`
	SourceAcquireMaxMS   int64                      `json:"source_acquire_max_ms"`
	ExecutorTotalMS      int64                      `json:"executor_total_ms"`
	ExecutorMaxMS        int64                      `json:"executor_max_ms"`
	ResultTotalMS        int64                      `json:"result_total_ms"`
	ResultMaxMS          int64                      `json:"result_max_ms"`
	AssemblyTotalMS      int64                      `json:"assembly_total_ms"`
	AssemblyMaxMS        int64                      `json:"assembly_max_ms"`
	RuntimeTotalMS       int64                      `json:"runtime_total_ms"`
	RuntimeMaxMS         int64                      `json:"runtime_max_ms"`
	SchedulerSettled     int64                      `json:"scheduler_settled"`
	SchedulerTotalMS     int64                      `json:"scheduler_total_ms"`
	SchedulerMaxMS       int64                      `json:"scheduler_max_ms"`
	Refusals             []ExtractionRefusalSummary `json:"refusals,omitempty"`
	UnknownRefusals      int64                      `json:"unknown_refusals,omitempty"`
	DomainTimings        []ExtractionDomainTiming   `json:"domain_timings,omitempty"`
}

const (
	extractionTimingSchemaV2      = "t4013-extraction-timing-v2"
	extractionTimingSchemaV3      = "t4013-extraction-timing-v3"
	maxExtractionRefusalSummaries = 32
	maxExtractionTimingDomains    = 65 // 64 production domains plus one historical unknown bucket.
)

// ExtractionRefusalSummary is a bounded, source-free rollup of terminal
// partition refusals. Identity and raw error text remain excluded.
type ExtractionRefusalSummary struct {
	Stage          string `json:"stage"`
	GenerationKind string `json:"generation_kind"`
	Classification string `json:"classification"`
	Dimension      string `json:"dimension"`
	Limit          int64  `json:"limit"`
	MaxObserved    int64  `json:"max_observed"`
	Count          int64  `json:"count"`
}

// ExtractionDomainTiming retains a bounded per-domain attempt distribution.
// Fixed wall buckets and closed failure classes make a deadline population
// distinguishable without retaining partition identity or raw errors.
type ExtractionDomainTiming struct {
	Domain             string `json:"domain"`
	Attempts           int64  `json:"attempts"`
	Completed          int64  `json:"completed"`
	Failed             int64  `json:"failed"`
	TerminalRefusals   int64  `json:"terminal_refusals"`
	Reused             int64  `json:"reused"`
	DeadlineFailures   int64  `json:"deadline_failures"`
	CanceledFailures   int64  `json:"canceled_failures"`
	OtherFailures      int64  `json:"other_failures"`
	UnknownFailures    int64  `json:"unknown_failures"`
	ExecutorLT1S       int64  `json:"executor_lt_1s"`
	ExecutorLT10S      int64  `json:"executor_lt_10s"`
	ExecutorLT60S      int64  `json:"executor_lt_60s"`
	ExecutorLT240S     int64  `json:"executor_lt_240s"`
	ExecutorLT300S     int64  `json:"executor_lt_300s"`
	ExecutorGE300S     int64  `json:"executor_ge_300s"`
	ExecutorUnbucketed int64  `json:"executor_unbucketed"`
	ExecutorTotalMS    int64  `json:"executor_total_ms"`
	ExecutorMaxMS      int64  `json:"executor_max_ms"`
}

func extractionRefusalSummaryKey(value ExtractionRefusalSummary) string {
	return fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%020d",
		value.Stage, value.GenerationKind, value.Classification, value.Dimension, value.Limit,
	)
}

// ConvergenceTransitionObservation retains only changes in the closed
// inspection stage/failure class and its bounded progress digest. It excludes
// raw errors, paths, repository/source identities, responses, and process output.
type ConvergenceTransitionObservation struct {
	WallMS                   int64  `json:"wall_ms"`
	Stage                    string `json:"stage"`
	Class                    string `json:"class"`
	HTTPStatus               int    `json:"http_status,omitempty"`
	HTTPReason               string `json:"http_reason,omitempty"`
	RelationshipFailureClass string `json:"relationship_failure_class,omitempty"`
	ProgressSHA256           string `json:"progress_sha256"`
	FirstProgressSHA256      string `json:"first_progress_sha256,omitempty"`
	ProgressChanges          int64  `json:"progress_changes,omitempty"`
	LastProgressChangeWallMS int64  `json:"last_progress_change_wall_ms,omitempty"`
}

// ObservationProgressObservation is a source-free projection of the bounded
// observation progress response already read by the exact ceremony oracle.
// Repository identities, generation digests, timestamps, paths, and errors
// remain private.
type ObservationProgressObservation struct {
	State                        string `json:"state"`
	SelectedVersion              string `json:"selected_version,omitempty"`
	PlanningState                string `json:"planning_state,omitempty"`
	PlanningPending              int    `json:"planning_pending,omitempty"`
	PlanningRunning              int    `json:"planning_running,omitempty"`
	PlanningSucceeded            int    `json:"planning_succeeded,omitempty"`
	PlanningFailed               int    `json:"planning_failed,omitempty"`
	RefusalStage                 string `json:"refusal_stage,omitempty"`
	RefusalGenerationKind        string `json:"refusal_generation_kind,omitempty"`
	RefusalClassification        string `json:"refusal_classification,omitempty"`
	RefusalDimension             string `json:"refusal_dimension,omitempty"`
	RefusalObserved              int64  `json:"refusal_observed,omitempty"`
	RefusalLimit                 int64  `json:"refusal_limit,omitempty"`
	ScheduleState                string `json:"schedule_state,omitempty"`
	ScheduleTotalPartitions      int    `json:"schedule_total_partitions,omitempty"`
	ScheduleMaterialized         int    `json:"schedule_materialized,omitempty"`
	SchedulePending              int    `json:"schedule_pending,omitempty"`
	ScheduleRunning              int    `json:"schedule_running,omitempty"`
	ScheduleSucceeded            int    `json:"schedule_succeeded,omitempty"`
	ScheduleFailed               int    `json:"schedule_failed,omitempty"`
	PublicationState             string `json:"publication_state,omitempty"`
	PublicationRecordCount       int    `json:"publication_record_count,omitempty"`
	PublicationObservedCount     int    `json:"publication_observed_count,omitempty"`
	PublicationUnsupportedCount  int    `json:"publication_unsupported_count,omitempty"`
	PublicationSourceSegments    int    `json:"publication_source_segments,omitempty"`
	PublicationInventorySegments int    `json:"publication_inventory_segments,omitempty"`
	PublicationEncodedBytes      int64  `json:"publication_encoded_bytes,omitempty"`
	PublicationObservationBytes  int64  `json:"publication_observation_bytes,omitempty"`
}

// ExtractionProgressObservation is the closed ceremony projection of the
// current extraction job, partition schedule, domain authority, and an
// optional validated pipeline refusal. It contains no repository identity,
// generation digest, worker, timestamp, path, or raw error text.
type ExtractionProgressObservation struct {
	State                 string `json:"state"`
	JobState              string `json:"job_state"`
	JobAttempts           int    `json:"job_attempts"`
	Total                 int    `json:"total_partitions"`
	Materialized          int    `json:"materialized"`
	Pending               int    `json:"pending"`
	Running               int    `json:"running"`
	Succeeded             int    `json:"succeeded"`
	Failed                int    `json:"failed"`
	Domains               int    `json:"domains"`
	CurrentDomains        int    `json:"current_domains"`
	RefusalStage          string `json:"refusal_stage,omitempty"`
	RefusalGenerationKind string `json:"refusal_generation_kind,omitempty"`
	RefusalClassification string `json:"refusal_classification,omitempty"`
	RefusalDimension      string `json:"refusal_dimension,omitempty"`
	RefusalObserved       int64  `json:"refusal_observed,omitempty"`
	RefusalLimit          int64  `json:"refusal_limit,omitempty"`
}

type ToolchainObservation struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// HostToolObservation binds a source-free ceremony to an exact executable and
// its bounded public version identity without retaining a host filesystem path.
type HostToolObservation struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	PathSHA256 string `json:"path_sha256,omitempty"`
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
	Interruption     *InterruptionObservation     `json:"interruption,omitempty"`
	AuthorizedQuery  *AuthorizedQueryObservation  `json:"authorized_query,omitempty"`
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
		Interruption:     cloneInterruptionObservation(observation.Interruption),
		AuthorizedQuery:  cloneAuthorizedQueryObservation(observation.AuthorizedQuery),
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
	if value.Schema == ObservationSchemaV25 {
		canonical, err := MarshalObservation(value)
		if err != nil || !bytes.Equal(raw, canonical) {
			return Observation{}, errors.Join(err, errors.New("T40.13 V25 observation is not canonical"))
		}
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

// planSchemaVersion returns the ordinal of a known plan schema and 0 for an
// unknown one. Version gates compare ordinals so the next plan version
// inherits modern behavior from one constant instead of hand-edited chains.
func planSchemaVersion(schema string) int {
	for version := 1; version < len(ceremonySchemaLadder); version++ {
		if ceremonySchemaLadder[version].plan == schema {
			return version
		}
	}
	return 0
}

func observationSchemaVersion(schema string) int {
	for version := 1; version < len(ceremonySchemaLadder); version++ {
		if ceremonySchemaLadder[version].observation == schema {
			return version
		}
	}
	return 0
}

func receiptSchemaVersion(schema string) int {
	for version := 1; version < len(ceremonySchemaLadder); version++ {
		if ceremonySchemaLadder[version].receipt == schema {
			return version
		}
	}
	return 0
}

func convergenceDetailVersion(observationVersion int) int {
	switch {
	case observationVersion >= 21:
		return observationDetailV17
	case observationVersion >= 16:
		return observationDetailV16
	case observationVersion >= 4:
		return observationVersion - 4
	default:
		return -1
	}
}

func ValidatePlan(value Plan) error {
	if planSchemaVersion(value.Schema) == 0 ||
		!date(value.FrozenOn) || !hexIdentity(value.SourceCommit, 40) ||
		!slices.Equal(value.PhaseOrder, phaseOrder) || len(value.Inputs) != 4 || len(value.StopRules) != 4 {
		return errors.New("T40.13 plan identity or fixed inventory is invalid")
	}
	if value.Schema == PlanSchema && len(value.HostToolchain) != 0 {
		return errors.New("T40.13 v1 plan unexpectedly binds a host toolchain")
	}
	if planSchemaVersion(value.Schema) >= 2 {
		if err := validateHostToolchain(value.HostToolchain, planSchemaVersion(value.Schema) >= 25); err != nil {
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
	case PlanSchemaV11:
		wantSafety = frozenSafetyV11
	case PlanSchemaV12:
		wantSafety = frozenSafetyV12
	case PlanSchemaV13:
		wantSafety = frozenSafetyV13
	case PlanSchemaV14:
		wantSafety = frozenSafetyV14
	case PlanSchemaV15:
		wantSafety = frozenSafetyV15
	case PlanSchemaV16:
		wantSafety = frozenSafetyV16
	case PlanSchemaV17:
		wantSafety = frozenSafetyV17
	case PlanSchemaV18:
		wantSafety = frozenSafetyV18
	case PlanSchemaV19:
		wantSafety = frozenSafetyV19
	case PlanSchemaV20:
		wantSafety = frozenSafetyV20
	case PlanSchemaV21:
		wantSafety = frozenSafetyV21
	case PlanSchemaV22:
		wantSafety = frozenSafetyV22
	case PlanSchemaV23:
		wantSafety = frozenSafetyV23
	case PlanSchemaV24:
		wantSafety = frozenSafetyV24
	case PlanSchemaV25:
		wantSafety = frozenSafetyV25
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
		(planSchemaVersion(value.Schema) >= 3) &&
			safety.ServerHealthDeadlineMS <= 0 ||
		planSchemaVersion(value.Schema) < 3 &&
			safety.ServerHealthDeadlineMS != 0 ||
		(planSchemaVersion(value.Schema) >= 4) && (safety.FullConvergenceDeadlineMS <= 0 ||
			safety.RevalidationDeadlineMS <= 0 || safety.RevalidationDeadlineMS > safety.FullConvergenceDeadlineMS) ||
		planSchemaVersion(value.Schema) < 4 &&
			(safety.FullConvergenceDeadlineMS != 0 || safety.RevalidationDeadlineMS != 0) {
		return errors.New("T40.13 safety envelope is invalid")
	}
	if planSchemaVersion(value.Schema) >= 10 {
		if safety.PressureTargetUsedPercent < 80 || safety.PressureTargetUsedPercent >= 90 ||
			safety.MaximumPressureBallastBytes <= 0 ||
			safety.MaximumPressureBallastBytes > safety.MaximumDataAllocatedBytes {
			return errors.New("T40.13 pressure exercise envelope is invalid")
		}
	} else if safety.PressureTargetUsedPercent != 0 || safety.MaximumPressureBallastBytes != 0 {
		return errors.New("T40.13 historical safety envelope acquired pressure controls")
	}
	if planSchemaVersion(value.Schema) >= 25 {
		if safety.MaximumPrePressureBytes <= 0 ||
			safety.MaximumPrePressureBytes > safety.MaximumDataAllocatedBytes {
			return errors.New("T40.13 pre-pressure allocation envelope is invalid")
		}
	} else if safety.MaximumPrePressureBytes != 0 {
		return errors.New("T40.13 historical safety envelope acquired a pre-pressure allocation ceiling")
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
	version := observationSchemaVersion(value.Schema)
	if version == 0 ||
		!date(value.MeasuredOn) ||
		(value.Outcome != "completed" && value.Outcome != "stopped") {
		return errors.New("T40.13 observation identity is invalid")
	}
	if version == 1 && len(value.HostToolchain) != 0 {
		return errors.New("T40.13 v1 observation unexpectedly binds a host toolchain")
	}
	if version >= 2 {
		if err := validateHostToolchain(value.HostToolchain, version >= 25); err != nil {
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
	if version < 3 && len(value.ServerStartups) != 0 {
		return errors.New("T40.13 pre-v3 observation unexpectedly retains startup diagnostics")
	}
	if version >= 3 {
		if err := validateServerStartups(
			value.ServerStartups,
			version >= 10,
			version >= 11,
		); err != nil {
			return err
		}
	}
	if version < 4 && len(value.ConvergenceWaits) != 0 {
		return errors.New("T40.13 pre-v4 observation unexpectedly retains convergence waits")
	}
	if version >= 4 {
		if err := validateConvergenceWaits(value.ConvergenceWaits, convergenceDetailVersion(version)); err != nil {
			return err
		}
	}
	if err := validateInterruptionObservation(value); err != nil {
		return err
	}
	if err := validateAuthorizedQueryObservation(value); err != nil {
		return err
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
		Interruption:     cloneInterruptionObservation(value.Interruption),
		AuthorizedQuery:  cloneAuthorizedQueryObservation(value.AuthorizedQuery),
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
	if planSchemaVersion(plan.Schema) >= 3 {
		for _, startup := range value.ServerStartups {
			if startup.Outcome == "deadline" && startup.WallMS < plan.Safety.ServerHealthDeadlineMS {
				return errors.New("T40.13 startup deadline observation precedes the frozen deadline")
			}
		}
	}
	if planSchemaVersion(plan.Schema) >= 4 {
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

func cloneInterruptionObservation(value *InterruptionObservation) *InterruptionObservation {
	if value == nil {
		return nil
	}
	result := *value
	if value.TriggerAttempt != nil {
		attempt := *value.TriggerAttempt
		result.TriggerAttempt = &attempt
	}
	if value.RecoveryLifecycle != nil {
		recovery := *value.RecoveryLifecycle
		result.RecoveryLifecycle = &recovery
	}
	if value.ConvergenceLifecycle != nil {
		convergence := *value.ConvergenceLifecycle
		result.ConvergenceLifecycle = &convergence
	}
	return &result
}

func cloneAuthorizedQueryObservation(value *AuthorizedQueryObservation) *AuthorizedQueryObservation {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func validateAuthorizedQueryObservation(value Observation) error {
	if observationSchemaVersion(value.Schema) < 23 {
		if value.AuthorizedQuery != nil {
			return errors.New("T40.13 historical observation acquired authorized-query diagnostics")
		}
		return nil
	}
	queryFailure := false
	projectionAllowed := false
	for _, failure := range value.Failures {
		queryFailure = queryFailure || failure.Code == "authorized_query_failed"
		projectionAllowed = projectionAllowed || failure.Phase == "authorized_query" &&
			slices.Contains([]string{
				"authorized_query_failed",
				"failed_phase_measurement_unavailable",
				"review_ceiling_crossed",
			}, failure.Code)
	}
	if value.Outcome == "completed" || !projectionAllowed {
		if value.AuthorizedQuery != nil {
			return errors.New("T40.13 observation retained unrelated authorized-query failure diagnostics")
		}
		if queryFailure {
			return errors.New("T40.13 authorized-query failure lacks its diagnostic projection")
		}
		return nil
	}
	observation := value.AuthorizedQuery
	if observation == nil {
		if queryFailure {
			return errors.New("T40.13 authorized-query failure lacks its diagnostic projection")
		}
		return nil
	}
	if observation.Schema != authorizedQuerySchemaV1 ||
		!slices.Contains([]string{"structural-2m-v1", "semantic-262144-v1"}, observation.Profile) ||
		!slices.Contains([]string{
			"unauthorized_repositories", "search", "services", "relationships", "citation",
		}, observation.Query) ||
		!slices.Contains([]string{"transport", "status", "response"}, observation.Class) ||
		observation.Attempts < 1 || observation.Attempts > 3 {
		return errors.New("T40.13 authorized-query failure projection is invalid")
	}
	if observation.Class == "status" {
		if observation.Status < 100 || observation.Status > 599 {
			return errors.New("T40.13 authorized-query status projection is invalid")
		}
	} else if observation.Status != 0 {
		return errors.New("T40.13 non-status authorized-query projection retained a status")
	}
	return nil
}

func validateInterruptionObservation(value Observation) error {
	version := observationSchemaVersion(value.Schema)
	if version < 17 {
		if value.Interruption != nil {
			return errors.New("T40.13 historical observation acquired interruption diagnostics")
		}
		return nil
	}
	if len(value.Phases) != len(phaseOrder) {
		return errors.New("T40.13 interruption phase inventory is incomplete")
	}
	interruption := value.Interruption
	wantInterruptionSchema := interruptionSchemaV1
	if version >= 22 {
		wantInterruptionSchema = interruptionSchemaV2
	}
	if interruption == nil || interruption.Schema != wantInterruptionSchema ||
		interruptionSubstageIndex(value.Schema, interruption.LastSubstage) < 0 {
		return errors.New("T40.13 interruption diagnostic identity is invalid")
	}
	if version < 22 && (interruption.TriggerScheduleSHA256 != "" ||
		interruption.RecoveryLifecycle != nil || interruption.ConvergenceLifecycle != nil) {
		return errors.New("T40.13 historical interruption acquired V22 evidence")
	}
	hasProgress := interruption.LastProgressStage != ""
	if version == 17 &&
		(hasProgress || interruption.LastProgressClass != "" ||
			interruption.LastProgressSHA256 != "" || interruption.LastProgressWallMS != 0 ||
			interruption.ProgressChanges != 0) {
		return errors.New("T40.13 v17 observation acquired v18 interruption progress")
	}
	if hasProgress {
		stages := []string{
			"repository_visibility", "repository_index", "source_generation",
			"search_generation", "observation_publication", "extraction_publication",
			"caller_generation", "relationship_publication", "service_census", "complete",
		}
		classes := []string{
			"pending", "transport", "status", "response", "control", "terminal", "complete",
		}
		if version < 18 ||
			!slices.Contains(stages, interruption.LastProgressStage) ||
			!slices.Contains(classes, interruption.LastProgressClass) ||
			!digestIdentity(interruption.LastProgressSHA256) ||
			interruption.LastProgressWallMS <= 0 || interruption.ProgressChanges < 0 {
			return errors.New("T40.13 interruption progress evidence is invalid")
		}
	} else if interruption.LastProgressClass != "" || interruption.LastProgressSHA256 != "" ||
		interruption.LastProgressWallMS != 0 || interruption.ProgressChanges != 0 {
		return errors.New("T40.13 interruption progress evidence is incomplete")
	}
	substageIndex := interruptionSubstageIndex(value.Schema, interruption.LastSubstage)
	hasTrigger := interruption.TriggerGenerationSHA256 != ""
	wantTrigger := substageIndex >= interruptionSubstageIndex(value.Schema, "first_stop")
	if hasTrigger != wantTrigger {
		return errors.New("T40.13 interruption trigger differs from its terminal substage")
	}
	if hasTrigger {
		if interruption.TriggerStage != extractionpublication.ScheduleStage ||
			!digestIdentity(interruption.TriggerGenerationSHA256) ||
			!digestIdentity(interruption.TriggerChunkSHA256) || interruption.TriggerAttempt == nil ||
			(version >= 22 && !digestIdentity(interruption.TriggerScheduleSHA256)) ||
			*interruption.TriggerAttempt < 0 ||
			*interruption.TriggerAttempt >= extractionpublication.ScheduleMaxAttempts ||
			interruption.TriggerWallMS <= 0 {
			return errors.New("T40.13 interruption trigger evidence is invalid")
		}
	} else if interruption.TriggerStage != "" || interruption.TriggerChunkSHA256 != "" ||
		interruption.TriggerScheduleSHA256 != "" ||
		interruption.TriggerAttempt != nil || interruption.TriggerWallMS != 0 {
		return errors.New("T40.13 interruption trigger evidence is incomplete")
	}
	// The recovered state exists exactly from the recovery-verification
	// boundary on, and must be a settled-or-recovered (never running) status:
	// the restart is only proven when the interrupted lease's fate was
	// observed.
	recoveredIndex := interruptionSubstageIndex(value.Schema, "recovery_verification")
	switch {
	case substageIndex < recoveredIndex:
		if interruption.TriggerRecoveredState != "" {
			return errors.New("T40.13 interruption recovery evidence precedes its substage")
		}
	case substageIndex > recoveredIndex || interruption.TriggerRecoveredState != "":
		validStates := []string{
			string(store.GenerationChunkDone), string(store.GenerationChunkFailed),
			string(store.GenerationChunkCanceled), interruptionFateCollected,
		}
		if version < 24 {
			validStates = append(validStates, string(store.GenerationChunkPending))
		} else {
			validStates = append(validStates, interruptionFateRequeued)
		}
		if !slices.Contains(validStates, interruption.TriggerRecoveredState) {
			return errors.New("T40.13 interruption recovery evidence is invalid")
		}
		if version < 22 && interruption.TriggerRecoveredState == interruptionFateCollected {
			return errors.New("T40.13 historical interruption acquired collected recovery evidence")
		}
	}
	if version >= 22 {
		requireRecovery := substageIndex > recoveredIndex
		if (interruption.RecoveryLifecycle != nil) != requireRecovery ||
			interruption.RecoveryLifecycle != nil &&
				validateInterruptionLifecycle(*interruption.RecoveryLifecycle, version >= 23) != nil {
			return errors.New("T40.13 interruption recovery lifecycle evidence is invalid")
		}
		convergenceIndex := interruptionSubstageIndex(value.Schema, "restart_convergence")
		requireConvergence := substageIndex > convergenceIndex
		if (interruption.ConvergenceLifecycle != nil) != requireConvergence ||
			interruption.ConvergenceLifecycle != nil &&
				validateInterruptionLifecycle(*interruption.ConvergenceLifecycle, version >= 23) != nil {
			return errors.New("T40.13 interruption convergence lifecycle evidence is invalid")
		}
	}
	// A substage at or past restart convergence proves the restart happened,
	// so its startup observation must have been recorded — the neutral-28
	// receipt could not establish this.
	restartEvidenceIndex := interruptionSubstageIndex(value.Schema, "restart_convergence")
	if version >= 22 {
		restartEvidenceIndex = recoveredIndex
	}
	if substageIndex >= restartEvidenceIndex {
		restartObserved := false
		for _, startup := range value.ServerStartups {
			if startup.Label == "interruption-restart" {
				restartObserved = true
				break
			}
		}
		if !restartObserved {
			return errors.New("T40.13 interruption restart startup observation is absent")
		}
	}
	phase := value.Phases[5]
	switch phase.Outcome {
	case "not_run":
		if interruption.LastSubstage != "not_started" || hasTrigger {
			return errors.New("T40.13 unrun interruption retained execution evidence")
		}
	case "failed":
		if interruption.LastSubstage == "not_started" || interruption.LastSubstage == "complete" {
			return errors.New("T40.13 failed interruption lacks its terminal substage")
		}
	case "succeeded":
		if interruption.LastSubstage != "complete" || !hasTrigger ||
			interruption.TriggerWallMS > phase.Metrics.WallMS {
			return errors.New("T40.13 completed interruption lacks exact trigger evidence")
		}
	}
	return nil
}

func validateInterruptionLifecycle(value InterruptionLifecycleObservation, allowErrorProgress bool) error {
	if !slices.Contains([]string{"not_run", "ok", "error"}, value.State) ||
		!slices.Contains([]lifecycle.Completeness{
			lifecycle.Exact, lifecycle.LowerBound, lifecycle.Unavailable,
		}, value.Completeness) ||
		value.Scanned < 0 || value.Scanned > lifecycle.MaxCandidatesPerTick ||
		value.Deleted < 0 || value.Deleted > lifecycle.MaxDeletesPerTick {
		return errors.New("T40.13 interruption lifecycle projection is invalid")
	}
	switch value.State {
	case "ok":
		if value.Completeness == lifecycle.Unavailable {
			return errors.New("T40.13 successful interruption lifecycle projection is unavailable")
		}
		return nil
	case "error":
		if value.Completeness != lifecycle.Unavailable {
			return errors.New("T40.13 failed interruption lifecycle projection is available")
		}
		if allowErrorProgress {
			return nil
		}
	}
	if value.Completeness != lifecycle.Unavailable || value.Scanned != 0 ||
		value.Deleted != 0 || value.Backlog {
		return errors.New("T40.13 unavailable interruption lifecycle projection has values")
	}
	return nil
}

func observationSchemaForPlan(plan Plan) string {
	version := planSchemaVersion(plan.Schema)
	if version > 0 {
		return ceremonySchemaLadder[version].observation
	}
	return ObservationSchema
}

func receiptSchemaForPlan(plan Plan) string {
	version := planSchemaVersion(plan.Schema)
	if version > 0 {
		return ceremonySchemaLadder[version].receipt
	}
	return ReceiptSchema
}

func validateHostToolchain(values []HostToolObservation, includeAssembler bool) error {
	want := []string{"go", "go-compile", "go-link", "git", "surreal"}
	if includeAssembler {
		want = append(want,
			"go-asm", "git-core", "git-tools", "go-tools", "go-root",
			"sandbox-exec", "sh", "du", "ps", "pgrep", "sysctl",
		)
	}
	if len(values) != len(want) {
		return errors.New("T40.13 host toolchain identity inventory is incomplete")
	}
	for index, name := range want {
		value := values[index]
		if value.Name != name || !boundedVersion(value.Version) || !digestIdentity(value.SHA256) ||
			includeAssembler && !digestIdentity(value.PathSHA256) ||
			!includeAssembler && value.PathSHA256 != "" {
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

func validateServerStartups(
	values []ServerStartupObservation,
	allowPressureRestart bool,
	allowInterruptionBackup bool,
) error {
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
	if allowInterruptionBackup {
		labels = append(labels, "interruption-backup")
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
	if detailVersion >= 7 {
		labels = append(labels, "interruption-backup")
	}
	revisions := []string{"a", "b", "a-return"}
	outcomes := []string{"converged", "deadline", "canceled"}
	if detailVersion >= 2 {
		outcomes = append(outcomes, "server_exited", "diagnostic_limit")
	}
	if detailVersion >= 5 {
		outcomes = append(outcomes, "repository_index_terminal")
	}
	if detailVersion >= 8 {
		outcomes = append(outcomes, "extraction_bound_refusal", "extraction_job_terminal")
	}
	if detailVersion >= 10 {
		outcomes = append(outcomes, "observation_bound_refusal", "observation_terminal",
			"extraction_schedule_terminal", "caller_generation_bound_refusal",
			"caller_generation_terminal", "relationship_bound_refusal",
			"relationship_terminal")
	}
	stages := []string{
		"repository_visibility", "repository_index", "source_generation",
		"search_generation", "observation_publication", "extraction_publication",
		"relationship_publication", "service_census", "complete",
	}
	if detailVersion >= 2 {
		stages = append(stages, convergenceNotInspected)
	}
	if detailVersion >= 10 {
		stages = append(stages, "caller_generation")
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
		if err := validateRelationshipFailure(value, detailVersion); err != nil {
			return err
		}
		if value.Outcome == "converged" && value.LastStage != "complete" ||
			value.Outcome != "converged" && value.LastStage == "complete" {
			return errors.New("T40.13 convergence wait outcome is incoherent")
		}
		if detailVersion == 0 {
			if value.FirstStage != "" || value.StageChanges != 0 || value.LastProgressChangeWallMS != 0 ||
				value.ObservationProgress != nil || value.ObservationProgressWallMS != 0 ||
				value.ExtractionProgress != nil || value.ExtractionProgressWallMS != 0 || value.ExtractionTiming != nil ||
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
		} else if err := validateObservationProgress(*value.ObservationProgress, detailVersion); err != nil {
			return err
		}
		if detailVersion >= 10 {
			hasExpectedBound := value.ObservationProgress != nil &&
				expectedObservationProgressBoundRefusal(*value.ObservationProgress)
			if (value.Outcome == "observation_bound_refusal") != hasExpectedBound &&
				(value.Outcome == "observation_bound_refusal" || value.Outcome == "observation_terminal") {
				return errors.New("T40.13 observation terminal outcome is incoherent")
			}
		}
		if detailVersion < 8 {
			if value.ExtractionProgress != nil || value.ExtractionProgressWallMS != 0 {
				return errors.New("T40.13 historical convergence wait acquired extraction diagnostics")
			}
		} else if value.ExtractionProgress == nil {
			if value.ExtractionProgressWallMS != 0 ||
				value.Outcome == "extraction_bound_refusal" || value.Outcome == "extraction_job_terminal" ||
				value.Outcome == "extraction_schedule_terminal" {
				return errors.New("T40.13 extraction terminal outcome lacks progress")
			}
		} else {
			if value.ExtractionProgressWallMS <= 0 || value.ExtractionProgressWallMS > value.WallMS ||
				validateExtractionProgress(*value.ExtractionProgress) != nil {
				return errors.New("T40.13 extraction progress is invalid")
			}
			hasExpectedBound := expectedExtractionBoundTerminal(
				*value.ExtractionProgress, detailVersion,
			)
			if (value.Outcome == "extraction_bound_refusal") != hasExpectedBound &&
				(value.Outcome == "extraction_bound_refusal" || value.Outcome == "extraction_job_terminal") {
				return errors.New("T40.13 extraction terminal outcome is incoherent")
			}
			// Coherence is enforced only when the outcome claims the terminal:
			// waits stopped for other reasons (diagnostic_limit, deadline,
			// server_exited) legitimately retain a terminal-shaped final probe
			// as evidence, and historical V12-V14 receipts sealed without a
			// job-terminal coherence check.
			if detailVersion >= observationDetailV15 && value.Outcome == "extraction_job_terminal" &&
				!expectedExtractionJobTerminal(*value.ExtractionProgress, detailVersion) {
				return errors.New("T40.13 extraction job terminal outcome is incoherent")
			}
			if detailVersion >= 10 && value.Outcome == "extraction_schedule_terminal" &&
				!expectedExtractionScheduleTerminal(*value.ExtractionProgress, detailVersion) {
				return errors.New("T40.13 extraction schedule terminal outcome is incoherent")
			}
		}
		if detailVersion < 9 {
			if value.ExtractionTiming != nil {
				return errors.New("T40.13 historical convergence wait acquired extraction timing")
			}
		} else if value.ExtractionTiming != nil {
			if err := validateExtractionTiming(*value.ExtractionTiming); err != nil {
				return err
			}
		}
		if detailVersion < observationDetailV17 {
			if value.CallerProgress != nil || value.CallerProgressWallMS != 0 {
				return errors.New("T40.13 historical convergence wait acquired caller diagnostics")
			}
		} else {
			if value.CallerProgress != nil &&
				(value.CallerProgressWallMS <= 0 || value.CallerProgressWallMS > value.WallMS ||
					validateCallerProgress(*value.CallerProgress) != nil) {
				return errors.New("T40.13 caller progress is invalid")
			}
			// Coherence is enforced only when the outcome claims the terminal
			// (deadline/canceled/server-exit stops legitimately retain any
			// final probe): a V17 caller terminal must carry the caller job
			// projection, and an active job row refutes it.
			if value.Outcome == "caller_generation_terminal" &&
				!expectedCallerGenerationTerminal(value.CallerProgress) {
				return errors.New("T40.13 caller generation terminal outcome is incoherent")
			}
			if value.Outcome == "caller_generation_bound_refusal" &&
				!expectedCallerGenerationBoundRefusal(value.CallerProgress) {
				return errors.New("T40.13 caller generation bound-refusal outcome is incoherent")
			}
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

// CallerProgressObservation is the caller-generation probe reduced to its
// source-free projection: publication state, settled-pair summary, and the
// repository-keyed caller-leaf job and its resolver-catalog predecessor beside
// them.
type CallerProgressObservation struct {
	State                 string                     `json:"state"`
	GenerationDigestValid bool                       `json:"generation_digest_valid"`
	PartitionState        string                     `json:"partition_state,omitempty"`
	SettledPairCount      int                        `json:"settled_pair_count"`
	SucceededPairCount    int                        `json:"succeeded_pair_count"`
	RefusedPairCount      int                        `json:"refused_pair_count"`
	TotalPairCount        *int                       `json:"total_pair_count,omitempty"`
	Refusals              []CallerRefusalObservation `json:"refusals,omitempty"`
	JobState              string                     `json:"job_state,omitempty"`
	JobAttempts           int                        `json:"job_attempts,omitempty"`
	ResolverJobState      string                     `json:"resolver_job_state,omitempty"`
	ResolverJobAttempts   int                        `json:"resolver_job_attempts,omitempty"`
}

type CallerRefusalObservation struct {
	Stage          pipelinerefusal.Stage          `json:"stage"`
	GenerationKind pipelinerefusal.GenerationKind `json:"generation_kind"`
	Classification pipelinerefusal.Classification `json:"classification"`
	Dimension      pipelinerefusal.Dimension      `json:"dimension"`
	Observed       int64                          `json:"observed"`
	Limit          int64                          `json:"limit"`
	OutcomeCount   int                            `json:"outcome_count"`
}

func validateCallerProgress(value CallerProgressObservation) error {
	succeededAndRefused, err := checkedAddInt64(
		int64(value.SucceededPairCount), int64(value.RefusedPairCount),
	)
	if err != nil {
		return errors.New("T40.13 caller progress counters overflowed")
	}
	if !slices.Contains([]string{"current", "missing", "stale", "failed", ""}, value.State) ||
		!slices.Contains([]string{"", "complete", "partial", "unavailable"}, value.PartitionState) ||
		value.SettledPairCount < 0 || value.SucceededPairCount < 0 || value.RefusedPairCount < 0 ||
		succeededAndRefused > int64(value.SettledPairCount) ||
		(value.TotalPairCount != nil && *value.TotalPairCount < value.SettledPairCount) ||
		!slices.Contains([]string{"", "pending", "claimed", "running", "done", "failed", "canceled"}, value.JobState) ||
		value.JobAttempts < 0 || value.JobAttempts > 1_000_000 ||
		(value.JobState == "" && value.JobAttempts != 0) ||
		!slices.Contains([]string{"", "pending", "claimed", "running", "done", "failed", "canceled"}, value.ResolverJobState) ||
		value.ResolverJobAttempts < 0 || value.ResolverJobAttempts > 1_000_000 ||
		(value.ResolverJobState == "" && value.ResolverJobAttempts != 0) ||
		len(value.Refusals) > maxCallerRefusalObservations {
		return errors.New("T40.13 caller progress projection is invalid")
	}
	var refused int64
	for _, summary := range value.Refusals {
		receipt := pipelinerefusal.Receipt{
			Schema: pipelinerefusal.Schema, Stage: summary.Stage,
			GenerationKind: summary.GenerationKind,
			Classification: summary.Classification, Dimension: summary.Dimension,
			Observed: summary.Observed, Limit: summary.Limit,
		}
		if pipelinerefusal.Validate(receipt) != nil ||
			receipt.GenerationKind != pipelinerefusal.GenerationCaller ||
			summary.OutcomeCount < 0 ||
			int64(summary.OutcomeCount) > int64(value.RefusedPairCount)-refused {
			return errors.New("T40.13 caller refusal projection is invalid")
		}
		refused, err = checkedAddInt64(refused, int64(summary.OutcomeCount))
		if err != nil {
			return errors.New("T40.13 caller refusal aggregate overflowed")
		}
	}
	if len(value.Refusals) > 0 && refused != int64(value.RefusedPairCount) {
		return errors.New("T40.13 caller refusal inventory is incomplete")
	}
	return nil
}

// expectedCallerGenerationTerminal mirrors the V21 classifier: the admission
// commits before the publication transaction, so a live caller-leaf job row
// (pending/claimed/running) refutes a caller terminal — the publisher is in a
// requeue backoff window, not dead.
func expectedCallerGenerationTerminal(progress *CallerProgressObservation) bool {
	if progress == nil || callerProgressJobActive(*progress) {
		return false
	}
	switch progress.State {
	case "current":
		return !progress.GenerationDigestValid || !callerProgressAllSucceeded(*progress)
	case "missing", "stale":
		return callerProgressAllSucceeded(*progress) || callerProgressJobDead(*progress)
	case "failed":
		return !expectedCallerGenerationBoundRefusal(progress)
	default:
		return true
	}
}

func expectedCallerGenerationBoundRefusal(progress *CallerProgressObservation) bool {
	if progress == nil || progress.State != "failed" || progress.PartitionState != "complete" ||
		progress.TotalPairCount == nil || *progress.TotalPairCount != progress.SettledPairCount ||
		progress.RefusedPairCount <= 0 ||
		len(progress.Refusals) == 0 {
		return false
	}
	succeededAndRefused, err := checkedAddInt64(
		int64(progress.SucceededPairCount), int64(progress.RefusedPairCount),
	)
	if err != nil || succeededAndRefused != int64(progress.SettledPairCount) {
		return false
	}
	refused := 0
	for _, refusal := range progress.Refusals {
		updated, err := checkedAddInt64(int64(refused), int64(refusal.OutcomeCount))
		if err != nil {
			return false
		}
		refused = int(updated)
		if refusal.Stage != pipelinerefusal.StageCallerGenerationAdmission ||
			refusal.GenerationKind != pipelinerefusal.GenerationCaller ||
			refusal.Classification != pipelinerefusal.ClassificationLimit ||
			refusal.Dimension != pipelinerefusal.DimensionCallerGenerationAbstentions ||
			refusal.Observed <= refusal.Limit ||
			refusal.Limit != callerleaf.MaxAggregateAbstentionRecords ||
			refused > progress.RefusedPairCount {
			return false
		}
	}
	return refused == progress.RefusedPairCount
}

func callerProgressAllSucceeded(progress CallerProgressObservation) bool {
	return progress.PartitionState == "complete" && progress.TotalPairCount != nil &&
		*progress.TotalPairCount == progress.SettledPairCount &&
		progress.SucceededPairCount == progress.SettledPairCount &&
		progress.RefusedPairCount == 0
}

// The resolver-catalog job is the caller pipeline's immediate upstream: an
// active resolver holds a caller terminal exactly like an active caller job
// (the successor may not be minted yet), and a dead resolver is a dead caller
// pipeline even though no caller job for the awaited generation ever existed.
func callerProgressJobActive(progress CallerProgressObservation) bool {
	active := []string{"pending", "claimed", "running"}
	return slices.Contains(active, progress.JobState) ||
		slices.Contains(active, progress.ResolverJobState)
}

func callerProgressJobDead(progress CallerProgressObservation) bool {
	return progress.JobState == "failed" || progress.JobState == "canceled" ||
		progress.ResolverJobState == "failed" || progress.ResolverJobState == "canceled"
}

func validateExtractionTiming(value ExtractionTimingObservation) error {
	attempts, err := checkedSumInt64(value.Completed, value.Failed, value.TerminalRefusals)
	if err != nil {
		return errors.New("T40.13 extraction timing count overflowed")
	}
	runtimeParts, err := checkedSumInt64(
		value.SourceAcquireTotalMS, value.ExecutorTotalMS, value.ResultTotalMS,
		value.AssemblyTotalMS,
	)
	if err != nil {
		return errors.New("T40.13 extraction timing wall aggregate overflowed")
	}
	if value.Attempts <= 0 || value.Completed < 0 || value.Failed < 0 ||
		value.TerminalRefusals < 0 || value.Reused < 0 ||
		attempts != value.Attempts ||
		value.Reused > value.Attempts ||
		value.SourceAcquireTotalMS < 0 || value.SourceAcquireMaxMS < 0 ||
		value.ExecutorTotalMS < 0 || value.ExecutorMaxMS < 0 ||
		value.ResultTotalMS < 0 || value.ResultMaxMS < 0 ||
		value.AssemblyTotalMS < 0 || value.AssemblyMaxMS < 0 ||
		value.RuntimeTotalMS < 0 || value.RuntimeMaxMS < 0 ||
		value.SchedulerSettled < 0 || value.SchedulerTotalMS < 0 || value.SchedulerMaxMS < 0 ||
		value.SourceAcquireMaxMS > value.SourceAcquireTotalMS ||
		value.ExecutorMaxMS > value.ExecutorTotalMS ||
		value.ResultMaxMS > value.ResultTotalMS ||
		value.AssemblyMaxMS > value.AssemblyTotalMS ||
		value.RuntimeMaxMS > value.RuntimeTotalMS ||
		value.SchedulerSettled > value.Attempts || value.SchedulerMaxMS > value.SchedulerTotalMS ||
		runtimeParts > value.RuntimeTotalMS {
		return errors.New("T40.13 extraction timing aggregate is invalid")
	}
	if value.Schema == "" {
		if len(value.Refusals) != 0 || value.UnknownRefusals != 0 || len(value.DomainTimings) != 0 {
			return errors.New("T40.13 legacy extraction timing acquired refusal details")
		}
		return nil
	}
	if (value.Schema != extractionTimingSchemaV2 && value.Schema != extractionTimingSchemaV3) ||
		value.UnknownRefusals < 0 ||
		len(value.Refusals) > maxExtractionRefusalSummaries {
		return errors.New("T40.13 extraction refusal aggregate is invalid")
	}
	refusalCount := value.UnknownRefusals
	lastKey, lastLimitKey := "", ""
	for _, refusal := range value.Refusals {
		receipt := pipelinerefusal.Receipt{
			Schema:         pipelinerefusal.Schema,
			Stage:          pipelinerefusal.Stage(refusal.Stage),
			GenerationKind: pipelinerefusal.GenerationKind(refusal.GenerationKind),
			Classification: pipelinerefusal.Classification(refusal.Classification),
			Dimension:      pipelinerefusal.Dimension(refusal.Dimension),
			Observed:       refusal.MaxObserved, Limit: refusal.Limit,
		}
		key := extractionRefusalSummaryKey(refusal)
		limitKey := refusal.Stage + "\x00" + refusal.GenerationKind + "\x00" +
			refusal.Classification + "\x00" + refusal.Dimension
		if refusal.Count <= 0 || refusal.Classification == string(pipelinerefusal.ClassificationUnknown) ||
			pipelinerefusal.Validate(receipt) != nil || (lastKey != "" && key <= lastKey) ||
			(lastLimitKey == limitKey) {
			return errors.New("T40.13 extraction refusal summary is invalid")
		}
		lastKey, lastLimitKey = key, limitKey
		refusalCount, err = checkedAddInt64(refusalCount, refusal.Count)
		if err != nil {
			return errors.New("T40.13 extraction refusal aggregate overflowed")
		}
	}
	if refusalCount != value.TerminalRefusals {
		return errors.New("T40.13 extraction refusal aggregate is incomplete")
	}
	if value.Schema == extractionTimingSchemaV2 {
		if len(value.DomainTimings) != 0 {
			return errors.New("T40.13 v2 extraction timing acquired domain attribution")
		}
		return nil
	}
	return validateExtractionDomainTimings(value)
}

func validateExtractionDomainTimings(value ExtractionTimingObservation) error {
	if len(value.DomainTimings) == 0 || len(value.DomainTimings) > maxExtractionTimingDomains {
		return errors.New("T40.13 v3 extraction timing lacks bounded domain attribution")
	}
	var attempts, completed, failed, terminal, reused, executorTotal int64
	executorMax := int64(0)
	lastDomain := ""
	for _, domain := range value.DomainTimings {
		domainAttempts, err := checkedSumInt64(domain.Completed, domain.Failed, domain.TerminalRefusals)
		failureClasses, failureErr := checkedSumInt64(
			domain.DeadlineFailures, domain.CanceledFailures,
			domain.OtherFailures, domain.UnknownFailures,
		)
		buckets, bucketErr := checkedSumInt64(
			domain.ExecutorLT1S, domain.ExecutorLT10S, domain.ExecutorLT60S,
			domain.ExecutorLT240S, domain.ExecutorLT300S, domain.ExecutorGE300S,
			domain.ExecutorUnbucketed,
		)
		if err != nil || failureErr != nil || bucketErr != nil {
			return errors.New("T40.13 extraction domain aggregate overflowed")
		}
		if !validExtractionTimingDomain(domain.Domain) ||
			(lastDomain != "" && domain.Domain <= lastDomain) ||
			domain.Attempts <= 0 || domain.Completed < 0 || domain.Failed < 0 ||
			domain.TerminalRefusals < 0 || domain.Reused < 0 ||
			domainAttempts != domain.Attempts ||
			domain.Reused > domain.Attempts ||
			domain.DeadlineFailures < 0 || domain.CanceledFailures < 0 ||
			domain.OtherFailures < 0 || domain.UnknownFailures < 0 ||
			failureClasses != domain.Failed ||
			domain.ExecutorLT1S < 0 || domain.ExecutorLT10S < 0 || domain.ExecutorLT60S < 0 ||
			domain.ExecutorLT240S < 0 || domain.ExecutorLT300S < 0 || domain.ExecutorGE300S < 0 ||
			domain.ExecutorUnbucketed < 0 ||
			buckets != domain.Attempts ||
			domain.ExecutorTotalMS < 0 || domain.ExecutorMaxMS < 0 ||
			domain.ExecutorMaxMS > domain.ExecutorTotalMS {
			return errors.New("T40.13 extraction domain timing is invalid")
		}
		lastDomain = domain.Domain
		attempts, err = checkedAddInt64(attempts, domain.Attempts)
		if err != nil {
			return errors.New("T40.13 extraction domain attempt aggregate overflowed")
		}
		completed, err = checkedAddInt64(completed, domain.Completed)
		if err != nil {
			return errors.New("T40.13 extraction domain completed aggregate overflowed")
		}
		failed, err = checkedAddInt64(failed, domain.Failed)
		if err != nil {
			return errors.New("T40.13 extraction domain failure aggregate overflowed")
		}
		terminal, err = checkedAddInt64(terminal, domain.TerminalRefusals)
		if err != nil {
			return errors.New("T40.13 extraction domain terminal aggregate overflowed")
		}
		reused, err = checkedAddInt64(reused, domain.Reused)
		if err != nil {
			return errors.New("T40.13 extraction domain reuse aggregate overflowed")
		}
		executorTotal, err = checkedAddInt64(executorTotal, domain.ExecutorTotalMS)
		if err != nil {
			return errors.New("T40.13 extraction domain timing aggregate overflowed")
		}
		executorMax = max(executorMax, domain.ExecutorMaxMS)
	}
	if attempts != value.Attempts || completed != value.Completed || failed != value.Failed ||
		terminal != value.TerminalRefusals || reused != value.Reused ||
		executorTotal != value.ExecutorTotalMS || executorMax != value.ExecutorMaxMS {
		return errors.New("T40.13 extraction domain timing does not reconcile")
	}
	return nil
}

func validExtractionTimingDomain(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, current := range value {
		if (current >= 'a' && current <= 'z') || (current >= '0' && current <= '9') || current == '-' {
			continue
		}
		return false
	}
	return true
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
			(detailVersion < observationDetailV16 && transition.RelationshipFailureClass != "") ||
			(detailVersion >= observationDetailV16 && transition.RelationshipFailureClass != "" &&
				(transition.Stage != "relationship_publication" ||
					!validRelationshipFailureClass(transition.RelationshipFailureClass))) ||
			!validTransitionHTTPDiagnostic(transition, detailVersion) ||
			!digestIdentity(transition.ProgressSHA256) || transition.WallMS < 0 ||
			transition.WallMS > value.WallMS ||
			index > 0 && transition.WallMS < previous.WallMS ||
			index > 0 && transition.Stage == previous.Stage && transition.Class == previous.Class &&
				transition.HTTPStatus == previous.HTTPStatus && transition.HTTPReason == previous.HTTPReason &&
				transition.RelationshipFailureClass == previous.RelationshipFailureClass &&
				(detailVersion >= 9 || transition.ProgressSHA256 == previous.ProgressSHA256) ||
			validateTransitionProgress(transition, value.WallMS, detailVersion) != nil {
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
	if detailVersion >= observationDetailV16 &&
		last.RelationshipFailureClass != value.RelationshipFailureClass {
		return errors.New("T40.13 relationship failure transition fence is invalid")
	}
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
	terminalOutcome := value.Outcome == "repository_index_terminal"
	if detailVersion >= 8 {
		terminalOutcome = terminalOutcome || value.Outcome == "extraction_bound_refusal" ||
			value.Outcome == "extraction_job_terminal"
	}
	if detailVersion >= 10 {
		terminalOutcome = terminalOutcome || value.Outcome == "observation_bound_refusal" ||
			value.Outcome == "observation_terminal" || value.Outcome == "extraction_schedule_terminal" ||
			value.Outcome == "caller_generation_bound_refusal" ||
			value.Outcome == "caller_generation_terminal" ||
			value.Outcome == "relationship_bound_refusal" ||
			value.Outcome == "relationship_terminal"
	}
	if detailVersion >= 5 && terminalOutcome != (last.Class == "terminal") &&
		!unconfirmedTerminalStop(detailVersion, terminalOutcome, value.Outcome, last.Stage) {
		return errors.New("T40.13 terminal transition is incoherent")
	}
	if err := validateRepositoryIndexFailureClass(value, detailVersion); err != nil {
		return err
	}
	if err := validateLastInspection(value, stages, detailVersion, last); err != nil {
		return err
	}
	return nil
}

// unconfirmedTerminalStop permits the one torn shape the second-probe
// confirmation windows can produce: a probe classified terminal, the executor
// deliberately waiting for its identical confirming re-probe, and the wait
// stopped first by an external cause. The final terminal-shaped transition is
// legitimate evidence there; only an outcome that CLAIMS the terminal still
// requires a terminal-shaped final transition. The exemption is gated to
// fresh V21 evidence (V15..V20 keep their sealed historical semantics — the
// old executor's seal-time validation refused this shape, so its rejection
// stays a fence proving any such earlier artifact inauthentic) and to the
// three stages that actually run confirmation windows; immediate-settling
// terminals can never legitimately produce the torn shape.
func unconfirmedTerminalStop(detailVersion int, terminalOutcome bool, outcome, lastStage string) bool {
	if detailVersion < observationDetailV17 || terminalOutcome {
		return false
	}
	if !slices.Contains([]string{
		"extraction_publication", "relationship_publication", "caller_generation",
	}, lastStage) {
		return false
	}
	switch outcome {
	case "deadline", "canceled", "server_exited", "diagnostic_limit":
		return true
	}
	return false
}

func validateConvergenceWithoutSuccessfulProbe(
	value ConvergenceWaitObservation,
	stages []string,
	detailVersion int,
) error {
	if value.FirstStage != convergenceNotInspected || value.FirstProgressSHA256 != "" ||
		value.LastProgressSHA256 != "" || value.ProgressChanges != 0 || value.StageChanges != 0 ||
		value.LastProgressChangeWallMS != 0 || value.LastSuccessfulProbeSHA256 != "" ||
		value.LastSuccessfulProbeWallMS != 0 {
		return errors.New("T40.13 convergence wait invents a successful probe")
	}
	if value.Attempts == 0 {
		if value.Outcome != "server_exited" || len(value.InspectionTransitions) != 0 ||
			hasLastInspection(value) || value.TransitionLimitExceeded || value.ExtractionTiming != nil ||
			value.ObservationProgress != nil || value.ObservationProgressWallMS != 0 ||
			value.ExtractionProgress != nil || value.ExtractionProgressWallMS != 0 {
			return errors.New("T40.13 uninspected convergence wait is incoherent")
		}
		return nil
	}
	observationTerminal := detailVersion >= 10 &&
		(value.Outcome == "observation_bound_refusal" || value.Outcome == "observation_terminal")
	extractionTerminal := detailVersion >= 8 &&
		(value.Outcome == "extraction_bound_refusal" || value.Outcome == "extraction_job_terminal") ||
		detailVersion >= 10 && value.Outcome == "extraction_schedule_terminal"
	if observationTerminal {
		if value.ObservationProgress == nil || value.ObservationProgressWallMS <= 0 ||
			value.ObservationProgressWallMS > value.WallMS ||
			validateObservationProgress(*value.ObservationProgress, detailVersion) != nil ||
			value.ExtractionProgress != nil || value.ExtractionProgressWallMS != 0 {
			return errors.New("T40.13 unsuccessful observation terminal wait is incoherent")
		}
		hasLimit := expectedObservationProgressBoundRefusal(*value.ObservationProgress)
		if (value.Outcome == "observation_bound_refusal") != hasLimit {
			return errors.New("T40.13 unsuccessful observation terminal outcome is incoherent")
		}
	} else if extractionTerminal {
		if value.ExtractionProgress == nil || value.ExtractionProgressWallMS <= 0 ||
			value.ExtractionProgressWallMS > value.WallMS ||
			validateExtractionProgress(*value.ExtractionProgress) != nil ||
			value.ObservationProgress != nil || value.ObservationProgressWallMS != 0 {
			return errors.New("T40.13 unsuccessful extraction terminal wait is incoherent")
		}
		hasRefusal := expectedExtractionBoundTerminal(*value.ExtractionProgress, detailVersion)
		if value.Outcome == "extraction_schedule_terminal" {
			if !expectedExtractionScheduleTerminal(*value.ExtractionProgress, detailVersion) {
				return errors.New("T40.13 unsuccessful extraction schedule terminal outcome is incoherent")
			}
		} else if value.Outcome == "extraction_job_terminal" {
			if !expectedExtractionJobTerminal(*value.ExtractionProgress, detailVersion) {
				return errors.New("T40.13 unsuccessful extraction job terminal outcome is incoherent")
			}
		} else if (value.Outcome == "extraction_bound_refusal") != hasRefusal {
			return errors.New("T40.13 unsuccessful extraction terminal outcome is incoherent")
		}
	} else if value.ObservationProgress != nil || value.ObservationProgressWallMS != 0 ||
		value.ExtractionProgress != nil || value.ExtractionProgressWallMS != 0 {
		return errors.New("T40.13 unsuccessful non-terminal wait retained progress")
	}
	if detailVersion < 9 && value.ExtractionTiming != nil {
		return errors.New("T40.13 historical unsuccessful wait acquired extraction timing")
	}
	if detailVersion >= 9 && value.ExtractionTiming != nil {
		if err := validateExtractionTiming(*value.ExtractionTiming); err != nil {
			return err
		}
	}
	if detailVersion < observationDetailV17 {
		if value.CallerProgress != nil || value.CallerProgressWallMS != 0 {
			return errors.New("T40.13 historical convergence wait acquired caller diagnostics")
		}
	} else {
		if value.CallerProgress != nil &&
			(value.CallerProgressWallMS <= 0 || value.CallerProgressWallMS > value.WallMS ||
				validateCallerProgress(*value.CallerProgress) != nil) {
			return errors.New("T40.13 caller progress is invalid")
		}
		if value.Outcome == "caller_generation_terminal" &&
			!expectedCallerGenerationTerminal(value.CallerProgress) {
			return errors.New("T40.13 caller generation terminal outcome is incoherent")
		}
		if value.Outcome == "caller_generation_bound_refusal" &&
			!expectedCallerGenerationBoundRefusal(value.CallerProgress) {
			return errors.New("T40.13 caller generation bound-refusal outcome is incoherent")
		}
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
			(detailVersion < observationDetailV16 && transition.RelationshipFailureClass != "") ||
			(detailVersion >= observationDetailV16 && transition.RelationshipFailureClass != "" &&
				(transition.Stage != "relationship_publication" ||
					!validRelationshipFailureClass(transition.RelationshipFailureClass))) ||
			!validTransitionHTTPDiagnostic(transition, detailVersion) || !digestIdentity(transition.ProgressSHA256) ||
			transition.WallMS < 0 || transition.WallMS > value.WallMS ||
			index > 0 && transition.WallMS < previous.WallMS ||
			index > 0 && transition.Stage == previous.Stage && transition.Class == previous.Class &&
				transition.HTTPStatus == previous.HTTPStatus && transition.HTTPReason == previous.HTTPReason &&
				transition.RelationshipFailureClass == previous.RelationshipFailureClass &&
				(detailVersion >= 9 || transition.ProgressSHA256 == previous.ProgressSHA256) ||
			validateTransitionProgress(transition, value.WallMS, detailVersion) != nil {
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
	if detailVersion >= observationDetailV16 &&
		last.RelationshipFailureClass != value.RelationshipFailureClass {
		return errors.New("T40.13 unsuccessful relationship failure transition fence is invalid")
	}
	terminalOutcome := value.Outcome == "repository_index_terminal" || observationTerminal || extractionTerminal
	if detailVersion >= 10 {
		terminalOutcome = terminalOutcome || value.Outcome == "caller_generation_bound_refusal" ||
			value.Outcome == "caller_generation_terminal" ||
			value.Outcome == "relationship_bound_refusal" ||
			value.Outcome == "relationship_terminal"
	}
	if detailVersion >= 5 && terminalOutcome != (last.Class == "terminal") &&
		!unconfirmedTerminalStop(detailVersion, terminalOutcome, value.Outcome, last.Stage) {
		return errors.New("T40.13 unsuccessful terminal transition is incoherent")
	}
	if err := validateRepositoryIndexFailureClass(value, detailVersion); err != nil {
		return err
	}
	return nil
}

func validateTransitionProgress(
	transition ConvergenceTransitionObservation,
	waitWallMS int64,
	detailVersion int,
) error {
	if detailVersion < 9 {
		if transition.FirstProgressSHA256 != "" || transition.ProgressChanges != 0 ||
			transition.LastProgressChangeWallMS != 0 {
			return errors.New("T40.13 historical transition acquired coalesced progress")
		}
		return nil
	}
	if !digestIdentity(transition.FirstProgressSHA256) || transition.ProgressChanges < 0 {
		return errors.New("T40.13 coalesced transition progress is invalid")
	}
	if transition.ProgressChanges == 0 {
		if transition.FirstProgressSHA256 != transition.ProgressSHA256 ||
			transition.LastProgressChangeWallMS != 0 {
			return errors.New("T40.13 unchanged coalesced transition is incoherent")
		}
		return nil
	}
	if transition.LastProgressChangeWallMS < transition.WallMS ||
		transition.LastProgressChangeWallMS > waitWallMS {
		return errors.New("T40.13 coalesced transition change time is invalid")
	}
	return nil
}

func validRelationshipFailureClass(value string) bool {
	return slices.Contains([]string{
		relationshipFailureCurrentControl,
		relationshipFailureAuthorityGap,
		relationshipFailureAuthorityDrift,
		relationshipFailureSuccessorAbsent,
		relationshipFailureSuccessorLost,
	}, value)
}

func validateRelationshipFailure(value ConvergenceWaitObservation, detailVersion int) error {
	if detailVersion < observationDetailV16 {
		if value.RelationshipFailureClass != "" || value.RelationshipTerminalConfirmations != 0 {
			return errors.New("T40.13 historical convergence wait acquired relationship failure detail")
		}
		return nil
	}
	if value.RelationshipFailureClass != "" &&
		(value.LastInspectionStage != "relationship_publication" ||
			!validRelationshipFailureClass(value.RelationshipFailureClass)) {
		return errors.New("T40.13 relationship failure classification is invalid")
	}
	terminal := value.Outcome == "relationship_terminal"
	if (terminal || value.Outcome == "relationship_bound_refusal") &&
		value.RelationshipFailureClass == "" {
		return errors.New("T40.13 relationship terminal classification is absent")
	}
	if terminal {
		if value.RelationshipTerminalConfirmations != 2 || value.Attempts < 2 {
			return errors.New("T40.13 relationship terminal confirmation is invalid")
		}
		return nil
	}
	if value.RelationshipTerminalConfirmations != 0 {
		return errors.New("T40.13 non-terminal relationship wait acquired terminal confirmation")
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

func validateObservationProgress(value ObservationProgressObservation, detailVersion int) error {
	planningTotal, err := checkedSumInt64(
		int64(value.PlanningPending), int64(value.PlanningRunning),
		int64(value.PlanningSucceeded), int64(value.PlanningFailed),
	)
	if err != nil {
		return errors.New("T40.13 planning progress aggregate overflowed")
	}
	scheduleAttemptBound, err := checkedMulInt64(
		int64(value.ScheduleTotalPartitions), int64(observationpublication.ScheduleMaxAttempts),
	)
	if err != nil {
		return errors.New("T40.13 schedule progress aggregate overflowed")
	}
	schedulePendingRunning, err := checkedAddInt64(
		int64(value.SchedulePending), int64(value.ScheduleRunning),
	)
	if err != nil {
		return errors.New("T40.13 schedule progress aggregate overflowed")
	}
	scheduleSucceededFailed, err := checkedAddInt64(
		int64(value.ScheduleSucceeded), int64(value.ScheduleFailed),
	)
	if err != nil {
		return errors.New("T40.13 schedule progress aggregate overflowed")
	}
	publicationObservedUnsupported, err := checkedAddInt64(
		int64(value.PublicationObservedCount), int64(value.PublicationUnsupportedCount),
	)
	if err != nil {
		return errors.New("T40.13 publication progress aggregate overflowed")
	}
	hasV2Fields := value.SelectedVersion != "" || value.RefusalStage != "" ||
		value.RefusalGenerationKind != "" || value.RefusalClassification != "" ||
		value.RefusalDimension != "" || value.RefusalObserved != 0 || value.RefusalLimit != 0 ||
		value.PublicationSourceSegments != 0 || value.PublicationInventorySegments != 0 ||
		value.PublicationEncodedBytes != 0 || value.PublicationObservationBytes != 0
	if detailVersion < 10 && hasV2Fields {
		return errors.New("T40.13 historical observation progress acquired v2 diagnostics")
	}
	if detailVersion >= 10 && value.SelectedVersion != "v2" {
		return errors.New("T40.13 selected observation progress route is invalid")
	}
	if !slices.Contains([]string{"current", "building", "failed", "stale", "unavailable"}, value.State) ||
		!optionalProgressState(value.PlanningState) || !optionalProgressState(value.ScheduleState) ||
		!optionalPublicationState(value.PublicationState) ||
		value.PlanningPending < 0 || value.PlanningRunning < 0 || value.PlanningSucceeded < 0 ||
		value.PlanningFailed < 0 || value.ScheduleTotalPartitions < 0 || value.ScheduleMaterialized < 0 ||
		value.SchedulePending < 0 || value.ScheduleRunning < 0 || value.ScheduleSucceeded < 0 ||
		value.ScheduleFailed < 0 || value.PublicationRecordCount < 0 ||
		value.PublicationObservedCount < 0 || value.PublicationUnsupportedCount < 0 ||
		value.PublicationSourceSegments < 0 ||
		value.PublicationSourceSegments > sourcepartition.MaxSegments ||
		value.PublicationInventorySegments < 0 ||
		value.PublicationInventorySegments > observationpublication.MaxInventorySegmentsV2 ||
		value.PublicationEncodedBytes < 0 ||
		value.PublicationEncodedBytes > observationpublication.MaxGenerationBytes ||
		value.PublicationObservationBytes < 0 ||
		value.PublicationObservationBytes > observationpublication.MaxGenerationBytes {
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
			planningTotal > 1) {
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
		int64(value.ScheduleMaterialized) > scheduleAttemptBound ||
		schedulePendingRunning > int64(value.ScheduleMaterialized) ||
		scheduleSucceededFailed > int64(value.ScheduleTotalPartitions) {
		return errors.New("T40.13 schedule progress counters are incoherent")
	}
	if value.ScheduleState == "settled" &&
		(value.SchedulePending != 0 || value.ScheduleRunning != 0 || value.ScheduleFailed != 0 ||
			value.ScheduleSucceeded != value.ScheduleTotalPartitions) ||
		value.ScheduleState == "failed" &&
			(value.SchedulePending != 0 || value.ScheduleRunning != 0 || value.ScheduleFailed == 0 ||
				scheduleSucceededFailed != int64(value.ScheduleTotalPartitions)) {
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
	maxRecords := observationpublication.MaxGenerationRecords
	if detailVersion >= 10 {
		maxRecords = observationpublication.MaxInventoryRecordsV2
	}
	if value.PublicationState != "" &&
		(value.PublicationRecordCount > maxRecords ||
			publicationObservedUnsupported != int64(value.PublicationRecordCount)) {
		return errors.New("T40.13 publication progress counters are incoherent")
	}
	hasRefusal := value.RefusalStage != "" || value.RefusalGenerationKind != "" ||
		value.RefusalClassification != "" || value.RefusalDimension != "" ||
		value.RefusalObserved != 0 || value.RefusalLimit != 0
	if hasRefusal {
		receipt := pipelinerefusal.Receipt{
			Schema:         pipelinerefusal.Schema,
			Stage:          pipelinerefusal.Stage(value.RefusalStage),
			GenerationKind: pipelinerefusal.GenerationKind(value.RefusalGenerationKind),
			Classification: pipelinerefusal.Classification(value.RefusalClassification),
			Dimension:      pipelinerefusal.Dimension(value.RefusalDimension),
			Observed:       value.RefusalObserved, Limit: value.RefusalLimit,
		}
		if detailVersion < 10 || value.State != "failed" || value.PlanningState != "failed" ||
			pipelinerefusal.Validate(receipt) != nil ||
			receipt.Stage != pipelinerefusal.StageObservationPublication ||
			receipt.GenerationKind != pipelinerefusal.GenerationObservation {
			return errors.New("T40.13 observation refusal projection is invalid")
		}
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

func expectedObservationProgressBoundRefusal(value ObservationProgressObservation) bool {
	return expectedObservationBoundRefusal(pipelinerefusal.Receipt{
		Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.Stage(value.RefusalStage),
		GenerationKind: pipelinerefusal.GenerationKind(value.RefusalGenerationKind),
		Classification: pipelinerefusal.Classification(value.RefusalClassification),
		Dimension:      pipelinerefusal.Dimension(value.RefusalDimension),
		Observed:       value.RefusalObserved, Limit: value.RefusalLimit,
	})
}

func validateExtractionProgress(value ExtractionProgressObservation) error {
	progress := extractionpublication.Progress{
		State: value.State, Total: value.Total, Materialized: value.Materialized,
		Pending: value.Pending, Running: value.Running, Succeeded: value.Succeeded,
		Failed: value.Failed, Domains: value.Domains, CurrentDomains: value.CurrentDomains,
	}
	if extractionpublication.ValidateProgress(progress) != nil ||
		!slices.Contains([]string{"", "pending", "claimed", "running", "done", "failed", "canceled"}, value.JobState) ||
		value.JobState == "" && value.State != string(store.GenerationScheduleSettled) && value.State != "current" ||
		value.JobAttempts < 0 || value.JobAttempts > 1_000_000 {
		return errors.New("T40.13 extraction progress projection is invalid")
	}
	hasRefusal := value.RefusalStage != "" || value.RefusalGenerationKind != "" ||
		value.RefusalClassification != "" || value.RefusalDimension != "" ||
		value.RefusalObserved != 0 || value.RefusalLimit != 0
	if !hasRefusal {
		return nil
	}
	receipt := pipelinerefusal.Receipt{
		Schema:         pipelinerefusal.Schema,
		Stage:          pipelinerefusal.Stage(value.RefusalStage),
		GenerationKind: pipelinerefusal.GenerationKind(value.RefusalGenerationKind),
		Classification: pipelinerefusal.Classification(value.RefusalClassification),
		Dimension:      pipelinerefusal.Dimension(value.RefusalDimension),
		Observed:       value.RefusalObserved, Limit: value.RefusalLimit,
	}
	if value.JobState != "failed" && value.JobState != "canceled" ||
		pipelinerefusal.Validate(receipt) != nil ||
		receipt.Classification != pipelinerefusal.ClassificationLimit {
		return errors.New("T40.13 extraction refusal projection is invalid")
	}
	return nil
}

func expectedExtractionScheduleTerminal(
	value ExtractionProgressObservation,
	detailVersion int,
) bool {
	succeededFailed, err := checkedAddInt64(int64(value.Succeeded), int64(value.Failed))
	if err != nil {
		return false
	}
	if value.State != string(store.GenerationScheduleSettled) || value.Failed <= 0 ||
		value.Pending != 0 || value.Running != 0 ||
		succeededFailed != int64(value.Total) {
		return false
	}
	if detailVersion < 11 {
		return value.JobState != string(store.StatusFailed) &&
			value.JobState != string(store.StatusCanceled)
	}
	return !expectedExtractionBoundRefusal(value)
}

func expectedExtractionJobTerminal(
	value ExtractionProgressObservation,
	detailVersion int,
) bool {
	if value.JobState != string(store.StatusFailed) && value.JobState != string(store.StatusCanceled) ||
		expectedExtractionBoundRefusal(value) {
		return false
	}
	if detailVersion < observationDetailV15 {
		return !expectedExtractionScheduleTerminal(value, detailVersion)
	}
	// V15: a current schedule proves the repository-keyed job trigger was
	// superseded by success, and an active schedule still has live scheduler
	// actors that can settle and publish without the job. Every other state
	// (unavailable, settled awaiting promotion, superseded, weak counters)
	// leaves no actor to finish the pipeline, so the terminal job row is
	// conclusive; the settled-failed complete shape stays the richer
	// schedule terminal.
	if value.State == "current" || value.State == string(store.GenerationScheduleActive) {
		return false
	}
	return !expectedExtractionScheduleTerminal(value, detailVersion)
}

func optionalProgressState(value string) bool {
	return value == "" || slices.Contains([]string{"active", "settled", "failed"}, value)
}

func optionalPublicationState(value string) bool {
	return value == "" || slices.Contains([]string{"current", "stale"}, value)
}

// modernPipelineStopMismatch is the shared identity guard for every V14/V15
// pipeline stop reason: one site to change on the next receipt version
// instead of one hand-edited guard per case arm.
func modernPipelineStopMismatch(value Receipt, failure FailureObservation, waitOutcome string) bool {
	return receiptSchemaVersion(value.Schema) < 14 ||
		failure.Class != "pipeline" || len(value.ConvergenceWaits) == 0 ||
		value.ConvergenceWaits[len(value.ConvergenceWaits)-1].Outcome != waitOutcome
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
	case "lifecycle_cycle_deadline_expired":
		if receiptSchemaVersion(value.Schema) < 23 || failure.Class != "lifecycle" || failure.Phase != "collection" {
			return errors.New("T40.13 lifecycle deadline failure identity is invalid")
		}
		wantDecision, wantReason = "cohort_experiment", "frozen_collection_review_ceiling_crossed"
	case "pressure_recovery_deadline_expired":
		if receiptSchemaVersion(value.Schema) < 23 || failure.Class != "environment" || failure.Phase != "pressure" {
			return errors.New("T40.13 pressure recovery deadline failure identity is invalid")
		}
		wantReason = "pressure_recovery_deadline_expired"
	case "authorized_query_failed":
		if receiptSchemaVersion(value.Schema) < 23 || failure.Class != "authorization" ||
			failure.Phase != "authorized_query" || value.AuthorizedQuery == nil {
			return errors.New("T40.13 authorized-query failure identity is invalid")
		}
		wantReason = "authorized_query_failed"
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
	case "interruption_trigger_unsatisfiable":
		if receiptSchemaVersion(value.Schema) < 17 ||
			failure.Class != "execution" ||
			failure.Phase != "interruption" || value.Interruption == nil ||
			value.Interruption.LastSubstage != "active_lease_wait" {
			return errors.New("T40.13 interruption-trigger failure identity is invalid")
		}
		wantReason = "interruption_trigger_unsatisfiable"
	case "interruption_trigger_deadline":
		if receiptSchemaVersion(value.Schema) < 18 || failure.Class != "execution" ||
			failure.Phase != "interruption" || value.Interruption == nil ||
			value.Interruption.LastSubstage != "active_lease_wait" ||
			value.Interruption.LastProgressStage == "" {
			return errors.New("T40.13 interruption-trigger deadline identity is invalid")
		}
		wantReason = "interruption_trigger_deadline"
	case "interruption_progress_terminal":
		if receiptSchemaVersion(value.Schema) < 18 || failure.Class != "pipeline" ||
			failure.Phase != "interruption" || value.Interruption == nil ||
			value.Interruption.LastSubstage != "active_lease_wait" ||
			value.Interruption.LastProgressClass != "terminal" {
			return errors.New("T40.13 interruption progress terminal identity is invalid")
		}
		wantReason = "interruption_progress_terminal"
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
	case "observation_production_bound_refused":
		if modernPipelineStopMismatch(value, failure, "observation_bound_refusal") {
			return errors.New("T40.13 observation bound-refusal identity is invalid")
		}
		wantDecision, wantReason = "reduce", "observation_production_bound_refused"
	case "observation_terminal":
		if modernPipelineStopMismatch(value, failure, "observation_terminal") {
			return errors.New("T40.13 observation terminal identity is invalid")
		}
		wantReason = "observation_terminal"
	case "extraction_production_bound_refused":
		if modernPipelineStopMismatch(value, failure, "extraction_bound_refusal") {
			return errors.New("T40.13 extraction bound-refusal identity is invalid")
		}
		wantDecision, wantReason = "reduce", "extraction_production_bound_refused"
	case "extraction_job_terminal":
		if modernPipelineStopMismatch(value, failure, "extraction_job_terminal") {
			return errors.New("T40.13 extraction terminal identity is invalid")
		}
		wantReason = "extraction_job_terminal"
	case "extraction_schedule_terminal":
		if modernPipelineStopMismatch(value, failure, "extraction_schedule_terminal") {
			return errors.New("T40.13 extraction schedule terminal identity is invalid")
		}
		wantReason = "extraction_schedule_terminal"
	case "caller_generation_production_bound_refused":
		if modernPipelineStopMismatch(value, failure, "caller_generation_bound_refusal") {
			return errors.New("T40.13 caller generation bound-refusal identity is invalid")
		}
		wantDecision, wantReason = "reduce", "caller_generation_production_bound_refused"
	case "caller_generation_terminal":
		if modernPipelineStopMismatch(value, failure, "caller_generation_terminal") {
			return errors.New("T40.13 caller generation terminal identity is invalid")
		}
		wantReason = "caller_generation_terminal"
	case "relationship_production_bound_refused":
		if modernPipelineStopMismatch(value, failure, "relationship_bound_refusal") {
			return errors.New("T40.13 relationship bound-refusal identity is invalid")
		}
		wantDecision, wantReason = "reduce", "relationship_production_bound_refused"
	case "relationship_terminal":
		if modernPipelineStopMismatch(value, failure, "relationship_terminal") {
			return errors.New("T40.13 relationship terminal identity is invalid")
		}
		wantReason = "relationship_terminal"
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
	if planSchemaVersion(plan.Schema) >= 25 {
		if len(value.Checks) != len(checkNames) || !value.Checks[len(value.Checks)-1].Passed {
			return errors.New("T40.13 completed receipt lacks complete phase accounting")
		}
	}
	if planSchemaVersion(plan.Schema) >= 3 {
		want := [][2]string{
			{"structural-2m-v1", "cold"}, {"semantic-262144-v1", "cold"},
			{"structural-2m-v1", "warm-noop"}, {"semantic-262144-v1", "interruption-first"},
			{"semantic-262144-v1", "interruption-restart"}, {"semantic-262144-v1", "stale-worker"},
			{"structural-2m-v1", "archive-restore"}, {"semantic-262144-v1", "authorized-query"},
		}
		if planSchemaVersion(plan.Schema) >= 10 {
			want = slices.Insert(want, 6, [2]string{"structural-2m-v1", "pressure-restart"})
		}
		if planSchemaVersion(plan.Schema) >= 11 {
			want = slices.Insert(want, 3, [2]string{"semantic-262144-v1", "interruption-backup"})
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
	if planSchemaVersion(plan.Schema) >= 4 {
		want := [][3]string{
			{"structural-2m-v1", "cold", "a"}, {"semantic-262144-v1", "cold", "a"},
			{"structural-2m-v1", "warm-noop", "a"}, {"structural-2m-v1", "delta-b", "b"},
			{"structural-2m-v1", "return-a", "a-return"}, {"semantic-262144-v1", "interruption-restart", "a"},
			{"semantic-262144-v1", "stale-worker", "a"}, {"structural-2m-v1", "pressure", "a-return"},
			{"structural-2m-v1", "archive-restore", "a-return"}, {"structural-2m-v1", "collection", "a-return"},
			{"semantic-262144-v1", "authorized-query-semantic", "a"},
			{"structural-2m-v1", "authorized-query-structural", "a-return"},
		}
		if planSchemaVersion(plan.Schema) >= 11 {
			want = slices.Insert(want, 5, [3]string{"semantic-262144-v1", "interruption-backup", "a"})
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
		var err error
		totalWall, err = checkedAddInt64(totalWall, phase.Metrics.WallMS)
		if err != nil {
			return errors.New("T40.13 completed receipt wall aggregation overflowed")
		}
		peakRSS = max(peakRSS, phase.Metrics.PeakRSSBytes)
		maxAllocated = max(maxAllocated, phase.Metrics.DataAllocatedBytes)
	}
	if totalWall > plan.Safety.MaximumTotalWallMS || peakRSS > plan.Safety.MaximumPeakRSSBytes ||
		maxAllocated > plan.Safety.MaximumDataAllocatedBytes ||
		prePressureAllocationCrossed(plan, value.Phases) {
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

func prePressureAllocationCrossed(plan Plan, phases []PhaseObservation) bool {
	if planSchemaVersion(plan.Schema) < 25 {
		return false
	}
	for _, phase := range phases {
		if phase.Name == "pressure" {
			break
		}
		if phase.Metrics.DataAllocatedBytes > plan.Safety.MaximumPrePressureBytes {
			return true
		}
	}
	return false
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

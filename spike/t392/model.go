// Package t392 builds the source-free public receipt for T39.2 from one
// separately frozen private operating-envelope plan and one closed
// source-free observation. Production packages must not import spike packages.
package t392

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
)

const (
	PrivatePlanSchema  = "t392-private-authorized-operating-plan-v1"
	ObservationSchema  = "t392-source-free-operating-observation-v1"
	ReceiptSchema      = "t392-authorized-target-operating-envelope-v1"
	MaxPrivatePlanSize = 256 << 10
	MaxObservationSize = 128 << 10
	MaxReceiptSize     = 64 << 10
	LifecycleOwners    = 13
	LifecycleMaxBytes  = 16 << 10
)

var phaseOrder = []string{
	"preflight", "cold", "incremental", "no_op", "query", "recovery",
	"restore", "retention", "teardown",
}

var failureClasses = []string{
	"authority", "physical_index", "pipeline", "query",
	"recovery_restore", "lifecycle_capacity", "environment",
}

var requiredToolNames = []string{"git", "go", "surreal", "zoekt-git-index"}

type PrivatePlan struct {
	Schema          string                 `json:"schema"`
	CommitmentNonce string                 `json:"commitment_nonce"`
	FrozenAt        string                 `json:"frozen_at"`
	Authorization   PrivateAuthorization   `json:"authorization"`
	Target          PrivateTarget          `json:"target"`
	Artifact        PrivateArtifact        `json:"artifact"`
	Tools           []PrivateTool          `json:"tools"`
	Host            PrivateHost            `json:"host"`
	Authority       PrivateAuthority       `json:"authority"`
	Thresholds      PrivateThresholds      `json:"thresholds"`
	QueryBattery    []PrivateQuery         `json:"query_battery"`
	PhaseOrder      []string               `json:"phase_order"`
	FailureClasses  []string               `json:"failure_classes"`
	StopConditions  []PrivateStopCondition `json:"stop_conditions"`
	Teardown        PrivateTeardown        `json:"teardown"`
	Safety          PrivateSafety          `json:"safety"`
}

type PrivateAuthorization struct {
	ApprovedAt string `json:"approved_at"`
	ExpiresAt  string `json:"expires_at"`
	Approver   string `json:"approver"`
	Custodian  string `json:"custodian"`
}

type PrivateTarget struct {
	Repository   string `json:"repository"`
	BaseCommit   string `json:"base_commit"`
	TargetCommit string `json:"target_commit"`
	MirrorPath   string `json:"mirror_path"`
}

type PrivateArtifact struct {
	PhebsCommit  string `json:"phebs_commit"`
	BinarySHA256 string `json:"binary_sha256"`
	ConfigSHA256 string `json:"config_sha256"`
}

type PrivateTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type PrivateHost struct {
	Identifier               string `json:"identifier"`
	OperatingSystem          string `json:"operating_system"`
	Architecture             string `json:"architecture"`
	LogicalCPUs              int    `json:"logical_cpus"`
	MemoryBytes              int64  `json:"memory_bytes"`
	DataVolumeAvailableBytes int64  `json:"data_volume_available_bytes"`
}

type PrivateAuthority struct {
	CatalogPath        string `json:"catalog_path"`
	CatalogSHA256      string `json:"catalog_sha256"`
	CatalogVersion     string `json:"catalog_version"`
	Services           int    `json:"services"`
	Memberships        int    `json:"memberships"`
	UnownedPaths       int    `json:"unowned_paths"`
	MaxAcceptedFanout  int    `json:"max_accepted_fanout"`
	CanonicalBytes     int    `json:"canonical_bytes"`
	AuthorityConfirmed bool   `json:"authority_confirmed"`
}

type PrivateThresholds struct {
	TotalWallMS                 int64 `json:"total_wall_ms"`
	ColdWallMS                  int64 `json:"cold_wall_ms"`
	ColdPeakRSSBytes            int64 `json:"cold_peak_rss_bytes"`
	ColdShardAllocatedBytes     int64 `json:"cold_shard_allocated_bytes"`
	ColdDerivedAllocatedBytes   int64 `json:"cold_derived_allocated_bytes"`
	ColdAvailabilityMS          int64 `json:"cold_availability_ms"`
	IncrementalCatchupMS        int64 `json:"incremental_catchup_ms"`
	IncrementalPeakRSSBytes     int64 `json:"incremental_peak_rss_bytes"`
	NoOpWallMS                  int64 `json:"no_op_wall_ms"`
	QueryColdMaxWallMS          int64 `json:"query_cold_max_wall_ms"`
	QueryWarmMaxWallMS          int64 `json:"query_warm_max_wall_ms"`
	RecoveryWallMS              int64 `json:"recovery_wall_ms"`
	RestoreWallMS               int64 `json:"restore_wall_ms"`
	RetentionWallMS             int64 `json:"retention_wall_ms"`
	MinimumAvailableDataBytes   int64 `json:"minimum_available_data_bytes"`
	MaximumLifecycleStatusBytes int   `json:"maximum_lifecycle_status_bytes"`
	MaximumFinalFailureCount    int   `json:"maximum_final_failure_count"`
}

type PrivateQuery struct {
	ID         string `json:"id"`
	Class      string `json:"class"`
	Scope      string `json:"scope"`
	ServiceKey string `json:"service_key,omitempty"`
	Query      string `json:"query"`
}

type PrivateStopCondition struct {
	ID        string `json:"id"`
	Class     string `json:"class"`
	Condition string `json:"condition"`
}

type PrivateTeardown struct {
	Owner     string `json:"owner"`
	Deadline  string `json:"deadline"`
	Procedure string `json:"procedure"`
}

type PrivateSafety struct {
	DedicatedDataDirectory    bool `json:"dedicated_data_directory"`
	RawArtifactsOutsideRepo   bool `json:"raw_artifacts_outside_repository"`
	NoPlanMutationAfterFreeze bool `json:"no_plan_mutation_after_freeze"`
	DirectTopologyOnly        bool `json:"direct_topology_only"`
	NoAutomaticAuthority      bool `json:"no_automatic_authority"`
}

type Observation struct {
	Schema      string                 `json:"schema"`
	MeasuredOn  string                 `json:"measured_on"`
	PhebsCommit string                 `json:"phebs_commit"`
	Outcome     string                 `json:"outcome"`
	TotalWallMS int64                  `json:"total_wall_ms"`
	Preflight   Preflight              `json:"preflight"`
	Authority   AuthorityObservation   `json:"authority"`
	Cold        ColdObservation        `json:"cold"`
	Incremental IncrementalObservation `json:"incremental"`
	NoOp        NoOpObservation        `json:"no_op"`
	Query       QueryObservation       `json:"query"`
	Recovery    RecoveryObservation    `json:"recovery"`
	Restore     RestoreObservation     `json:"restore"`
	Retention   RetentionObservation   `json:"retention"`
	Failures    []FailureObservation   `json:"failures"`
	Topology    TopologyDecision       `json:"topology"`
	Teardown    TeardownObservation    `json:"teardown"`
}

type Preflight struct {
	Outcome            string `json:"outcome"`
	ArtifactExact      bool   `json:"artifact_exact"`
	ConfigExact        bool   `json:"config_exact"`
	SourceExact        bool   `json:"source_exact"`
	CatalogExact       bool   `json:"catalog_exact"`
	HostExact          bool   `json:"host_exact"`
	AuthorityConfirmed bool   `json:"authority_confirmed"`
	ToolsExact         int    `json:"tools_exact"`
	AuthorizationValid bool   `json:"authorization_valid"`
}

type AuthorityObservation struct {
	Services          int `json:"services"`
	Memberships       int `json:"memberships"`
	UnownedPaths      int `json:"unowned_paths"`
	MaxAcceptedFanout int `json:"max_accepted_fanout"`
	CanonicalBytes    int `json:"canonical_bytes"`
}

type ColdObservation struct {
	Outcome               string `json:"outcome"`
	WallMS                int64  `json:"wall_ms"`
	PeakRSSBytes          int64  `json:"peak_rss_bytes"`
	ShardAllocatedBytes   int64  `json:"shard_allocated_bytes"`
	ShardCount            int    `json:"shard_count"`
	DerivedAllocatedBytes int64  `json:"derived_allocated_bytes"`
	SourceMemberBytes     int64  `json:"source_member_bytes"`
	SourceMembers         int    `json:"source_members"`
	ObservationBytes      int64  `json:"observation_bytes"`
	ObservationRecords    int    `json:"observation_records"`
	RelationshipBytes     int64  `json:"relationship_bytes"`
	RelationshipRecords   int    `json:"relationship_records"`
	AvailabilityMS        int64  `json:"availability_ms"`
	Current               bool   `json:"current"`
}

type IncrementalObservation struct {
	Outcome                       string `json:"outcome"`
	WallMS                        int64  `json:"wall_ms"`
	PeakRSSBytes                  int64  `json:"peak_rss_bytes"`
	ChangedFiles                  int    `json:"changed_files"`
	ShardAllocatedBytesAfter      int64  `json:"shard_allocated_bytes_after"`
	SourceGenerationChanged       bool   `json:"source_generation_changed"`
	SearchGenerationChanged       bool   `json:"search_generation_changed"`
	ObservationGenerationChanged  bool   `json:"observation_generation_changed"`
	RelationshipGenerationChecked bool   `json:"relationship_generation_checked"`
	RelationshipGenerationChanged bool   `json:"relationship_generation_changed"`
	UnaffectedServicesExact       int    `json:"unaffected_services_exact"`
	Current                       bool   `json:"current"`
}

type NoOpObservation struct {
	Outcome           string `json:"outcome"`
	WallMS            int64  `json:"wall_ms"`
	IndexChildren     int    `json:"index_children"`
	GitChildren       int    `json:"git_children"`
	NewScheduleChunks int    `json:"new_schedule_chunks"`
	ShardBytesDelta   int64  `json:"shard_bytes_delta"`
	GenerationChanges int    `json:"generation_changes"`
	DurableWrites     int    `json:"durable_writes"`
}

type QueryObservation struct {
	Outcome             string    `json:"outcome"`
	QueryCount          int       `json:"query_count"`
	Cold                QueryPass `json:"cold"`
	Warm                QueryPass `json:"warm"`
	ResultSetsEqual     bool      `json:"result_sets_equal"`
	AuthorityReceipts   int       `json:"authority_receipts"`
	FinalFencesPassed   int       `json:"final_fences_passed"`
	AuthorizationPassed int       `json:"authorization_passed"`
}

type QueryPass struct {
	Completed   int   `json:"completed"`
	Failed      int   `json:"failed"`
	TotalWallMS int64 `json:"total_wall_ms"`
	MaxWallMS   int64 `json:"max_wall_ms"`
}

type RecoveryObservation struct {
	Outcome                   string `json:"outcome"`
	InterruptedPhase          string `json:"interrupted_phase"`
	WallMS                    int64  `json:"wall_ms"`
	PriorPublicationPreserved bool   `json:"prior_publication_preserved"`
	PartialInvisible          bool   `json:"partial_invisible"`
	EventualCurrent           bool   `json:"eventual_current"`
}

type RestoreObservation struct {
	Outcome                   string `json:"outcome"`
	WallMS                    int64  `json:"wall_ms"`
	PreciousAuthorityExact    bool   `json:"precious_authority_exact"`
	DerivedGenerationsExact   int    `json:"derived_generations_exact"`
	DerivedGenerationsOmitted int    `json:"derived_generations_omitted"`
	EventualCurrent           bool   `json:"eventual_current"`
}

type RetentionObservation struct {
	Outcome              string `json:"outcome"`
	WallMS               int64  `json:"wall_ms"`
	OwnersObserved       int    `json:"owners_observed"`
	StatusBytes          int    `json:"status_bytes"`
	CapacityAvailable    bool   `json:"capacity_available"`
	AvailableBeforeBytes int64  `json:"available_before_bytes"`
	AvailableAfterBytes  int64  `json:"available_after_bytes"`
	SweepTurns           int    `json:"sweep_turns"`
	DeletedRows          int    `json:"deleted_rows"`
	DeletedGenerations   int    `json:"deleted_generations"`
	ProtectedRoots       int    `json:"protected_roots"`
}

type FailureObservation struct {
	Phase    string `json:"phase"`
	Class    string `json:"class"`
	Terminal bool   `json:"terminal"`
}

type TopologyDecision struct {
	Direct  string `json:"direct"`
	Cohorts string `json:"cohorts"`
	P6      string `json:"p6"`
	Reason  string `json:"reason"`
}

type TeardownObservation struct {
	Outcome             string `json:"outcome"`
	WallMS              int64  `json:"wall_ms"`
	SourceCopyRetained  bool   `json:"source_copy_retained"`
	DerivedDataRetained bool   `json:"derived_data_retained"`
	Custody             string `json:"custody"`
}

type Claims struct {
	SourceFree                    bool `json:"source_free"`
	OneEnvironmentOnly            bool `json:"one_environment_only"`
	EstablishesGeneralSLO         bool `json:"establishes_general_slo"`
	EstablishesAccuracy           bool `json:"establishes_accuracy"`
	AuthorizesRelease             bool `json:"authorizes_release"`
	EstablishesMigrationComplete  bool `json:"establishes_migration_complete"`
	EstablishesDecommissionSafety bool `json:"establishes_decommission_safety"`
}

type Receipt struct {
	Schema        string                 `json:"schema"`
	MeasuredOn    string                 `json:"measured_on"`
	PhebsCommit   string                 `json:"phebs_commit"`
	RunCommitment string                 `json:"run_commitment"`
	Outcome       string                 `json:"outcome"`
	TotalWallMS   int64                  `json:"total_wall_ms"`
	Preflight     Preflight              `json:"preflight"`
	Authority     AuthorityObservation   `json:"authority"`
	Cold          ColdObservation        `json:"cold"`
	Incremental   IncrementalObservation `json:"incremental"`
	NoOp          NoOpObservation        `json:"no_op"`
	Query         QueryObservation       `json:"query"`
	Recovery      RecoveryObservation    `json:"recovery"`
	Restore       RestoreObservation     `json:"restore"`
	Retention     RetentionObservation   `json:"retention"`
	Failures      []FailureObservation   `json:"failures"`
	Topology      TopologyDecision       `json:"topology"`
	Teardown      TeardownObservation    `json:"teardown"`
	Claims        Claims                 `json:"claims"`
}

func PlanDigest(plan []byte) string { return digest(plan) }

// FinalizeCandidate fills only the administrative fields that remain pending
// until explicit approval. It does not alter target, artifact, authority,
// query, threshold, safety, or teardown procedure content.
func FinalizeCandidate(candidate []byte, nonce, approvedAt, expiresAt, approver string) ([]byte, error) {
	if len(candidate) == 0 || len(candidate) > MaxPrivatePlanSize {
		return nil, errors.New("private candidate size is outside the fixed bound")
	}
	plan, err := decodeStrict[PrivatePlan](candidate)
	if err != nil {
		return nil, fmt.Errorf("decode private candidate: %w", err)
	}
	if plan.Authority.AuthorityConfirmed ||
		!strings.HasPrefix(plan.CommitmentNonce, "PENDING_") ||
		!strings.HasPrefix(plan.FrozenAt, "PENDING_") ||
		!strings.HasPrefix(plan.Authorization.ApprovedAt, "PENDING_") ||
		!strings.HasPrefix(plan.Authorization.ExpiresAt, "PENDING_") ||
		!strings.HasPrefix(plan.Authorization.Approver, "PENDING_") ||
		!strings.HasPrefix(plan.Teardown.Deadline, "PENDING_") {
		return nil, errors.New("candidate is not in the one-time pending-approval state")
	}
	plan.CommitmentNonce = nonce
	plan.FrozenAt = approvedAt
	plan.Authorization.ApprovedAt = approvedAt
	plan.Authorization.ExpiresAt = expiresAt
	plan.Authorization.Approver = approver
	plan.Authority.AuthorityConfirmed = true
	plan.Teardown.Deadline = expiresAt
	if err := ValidatePrivatePlan(plan); err != nil {
		return nil, fmt.Errorf("validate finalized private plan: %w", err)
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode finalized private plan: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxPrivatePlanSize {
		return nil, errors.New("finalized private plan exceeds the fixed bound")
	}
	return encoded, nil
}

func BuildReceipt(planBytes, observationBytes []byte, frozenPlanDigest string) ([]byte, error) {
	if len(planBytes) == 0 || len(planBytes) > MaxPrivatePlanSize {
		return nil, errors.New("private plan size is outside the fixed bound")
	}
	if len(observationBytes) == 0 || len(observationBytes) > MaxObservationSize {
		return nil, errors.New("observation size is outside the fixed bound")
	}
	if got := PlanDigest(planBytes); got != frozenPlanDigest {
		return nil, fmt.Errorf("private plan digest %s does not match frozen digest", got)
	}
	plan, err := decodeStrict[PrivatePlan](planBytes)
	if err != nil {
		return nil, fmt.Errorf("decode private plan: %w", err)
	}
	if err := ValidatePrivatePlan(plan); err != nil {
		return nil, fmt.Errorf("validate private plan: %w", err)
	}
	observation, err := decodeStrict[Observation](observationBytes)
	if err != nil {
		return nil, fmt.Errorf("decode observation: %w", err)
	}
	if err := validateObservation(plan, observation); err != nil {
		return nil, fmt.Errorf("validate observation: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-t392-target-operating-run-v1\x00"))
	_, _ = hash.Write(planBytes)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(observationBytes)
	receipt := Receipt{
		Schema: ReceiptSchema, MeasuredOn: observation.MeasuredOn,
		PhebsCommit:   observation.PhebsCommit,
		RunCommitment: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		Outcome:       observation.Outcome, TotalWallMS: observation.TotalWallMS,
		Preflight: observation.Preflight, Authority: observation.Authority,
		Cold: observation.Cold, Incremental: observation.Incremental,
		NoOp: observation.NoOp, Query: observation.Query,
		Recovery: observation.Recovery, Restore: observation.Restore,
		Retention: observation.Retention, Failures: observation.Failures,
		Topology: observation.Topology, Teardown: observation.Teardown,
		Claims: Claims{SourceFree: true, OneEnvironmentOnly: true},
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxReceiptSize {
		return nil, fmt.Errorf("receipt size %d exceeds %d", len(encoded), MaxReceiptSize)
	}
	return encoded, nil
}

// DecodeReceipt strictly decodes and validates a retained public receipt.
func DecodeReceipt(data []byte) (Receipt, error) {
	if len(data) == 0 || len(data) > MaxReceiptSize {
		return Receipt{}, errors.New("receipt size is outside the fixed bound")
	}
	value, err := decodeStrict[Receipt](data)
	if err != nil {
		return Receipt{}, fmt.Errorf("decode receipt: %w", err)
	}
	if err := ValidateReceipt(value); err != nil {
		return Receipt{}, fmt.Errorf("validate receipt: %w", err)
	}
	return value, nil
}

// ValidateReceipt enforces the closed public shape without requiring private
// thresholds or identities.
func ValidateReceipt(value Receipt) error {
	if value.Schema != ReceiptSchema || !isSHA256(value.RunCommitment) ||
		!isLowerHex(value.PhebsCommit, 40) || value.TotalWallMS < 0 {
		return errors.New("receipt identity is invalid")
	}
	if _, err := time.Parse("2006-01-02", value.MeasuredOn); err != nil {
		return errors.New("receipt measured_on must be YYYY-MM-DD")
	}
	if value.Outcome != "completed" && value.Outcome != "stopped" {
		return errors.New("receipt outcome is invalid")
	}
	if value.Authority.Services < 1 || value.Authority.Services > 4_000 ||
		value.Authority.Memberships < 1 || value.Authority.Memberships > 20_000 ||
		value.Authority.UnownedPaths < 0 || value.Authority.UnownedPaths > 12_000 ||
		value.Authority.MaxAcceptedFanout < 1 || value.Authority.MaxAcceptedFanout > 20 ||
		value.Authority.CanonicalBytes < 1 || value.Authority.CanonicalBytes > 5<<20 {
		return errors.New("receipt authority is outside the production envelope")
	}
	completed := value.Outcome == "completed"
	for _, current := range []string{
		value.Preflight.Outcome, value.Cold.Outcome, value.Incremental.Outcome,
		value.NoOp.Outcome, value.Query.Outcome, value.Recovery.Outcome,
		value.Restore.Outcome, value.Retention.Outcome,
	} {
		if err := outcome(current); err != nil {
			return err
		}
	}
	if !nonnegative(
		value.Cold.WallMS, value.Cold.PeakRSSBytes, value.Cold.ShardAllocatedBytes,
		int64(value.Cold.ShardCount), value.Cold.DerivedAllocatedBytes,
		value.Cold.SourceMemberBytes, int64(value.Cold.SourceMembers),
		value.Cold.ObservationBytes, int64(value.Cold.ObservationRecords),
		value.Cold.RelationshipBytes, int64(value.Cold.RelationshipRecords),
		value.Cold.AvailabilityMS, value.Incremental.WallMS,
		value.Incremental.PeakRSSBytes, int64(value.Incremental.ChangedFiles),
		value.Incremental.ShardAllocatedBytesAfter,
		int64(value.Incremental.UnaffectedServicesExact), value.NoOp.WallMS,
		int64(value.NoOp.IndexChildren), int64(value.NoOp.GitChildren),
		int64(value.NoOp.NewScheduleChunks), value.NoOp.ShardBytesDelta,
		int64(value.NoOp.GenerationChanges), int64(value.NoOp.DurableWrites),
		value.Recovery.WallMS, value.Restore.WallMS,
		int64(value.Restore.DerivedGenerationsExact),
		int64(value.Restore.DerivedGenerationsOmitted), value.Retention.WallMS,
		int64(value.Retention.OwnersObserved), int64(value.Retention.StatusBytes),
		value.Retention.AvailableBeforeBytes, value.Retention.AvailableAfterBytes,
		int64(value.Retention.SweepTurns), int64(value.Retention.DeletedRows),
		int64(value.Retention.DeletedGenerations), int64(value.Retention.ProtectedRoots),
	) {
		return errors.New("receipt contains a negative count, byte value, or duration")
	}
	if completed && (value.Preflight.Outcome != "succeeded" || !value.Preflight.ArtifactExact ||
		!value.Preflight.ConfigExact || !value.Preflight.SourceExact || !value.Preflight.CatalogExact ||
		!value.Preflight.HostExact || !value.Preflight.AuthorityConfirmed ||
		!value.Preflight.AuthorizationValid || value.Preflight.ToolsExact != len(requiredToolNames)) {
		return errors.New("completed receipt lacks exact successful preflight")
	}
	if completed && (value.Cold.Outcome != "succeeded" || !value.Cold.Current ||
		value.Cold.ShardCount < 1 || value.Cold.SourceMembers < 1 ||
		value.Incremental.Outcome != "succeeded" || !value.Incremental.Current ||
		value.Incremental.ChangedFiles < 1 || !value.Incremental.SourceGenerationChanged ||
		!value.Incremental.SearchGenerationChanged || !value.Incremental.ObservationGenerationChanged ||
		!value.Incremental.RelationshipGenerationChecked) {
		return errors.New("completed receipt lacks current cold or incremental authority")
	}
	if completed && (value.NoOp.Outcome != "succeeded" || value.NoOp.IndexChildren != 0 ||
		value.NoOp.GitChildren != 0 || value.NoOp.NewScheduleChunks != 0 ||
		value.NoOp.ShardBytesDelta != 0 || value.NoOp.GenerationChanges != 0 ||
		value.NoOp.DurableWrites != 0) {
		return errors.New("completed receipt lacks exact no-op zeros")
	}
	if completed && (value.Query.Outcome != "succeeded" || value.Query.QueryCount < 3 ||
		value.Query.Cold.Completed != value.Query.QueryCount || value.Query.Cold.Failed != 0 ||
		value.Query.Warm.Completed != value.Query.QueryCount || value.Query.Warm.Failed != 0 ||
		!value.Query.ResultSetsEqual || value.Query.AuthorityReceipts != value.Query.QueryCount*2 ||
		value.Query.FinalFencesPassed != value.Query.QueryCount*2 ||
		value.Query.AuthorizationPassed != value.Query.QueryCount*2) {
		return errors.New("completed receipt lacks query equality, authority, or final fences")
	}
	if completed && (value.Recovery.Outcome != "succeeded" ||
		!slices.Contains([]string{"source", "search", "observation", "relationship"}, value.Recovery.InterruptedPhase) ||
		!value.Recovery.PriorPublicationPreserved || !value.Recovery.PartialInvisible ||
		!value.Recovery.EventualCurrent || value.Restore.Outcome != "succeeded" ||
		!value.Restore.PreciousAuthorityExact || !value.Restore.EventualCurrent ||
		value.Restore.DerivedGenerationsExact+value.Restore.DerivedGenerationsOmitted == 0) {
		return errors.New("completed receipt lacks recovery or restore authority")
	}
	if completed && (value.Retention.Outcome != "succeeded" ||
		value.Retention.OwnersObserved != LifecycleOwners ||
		value.Retention.StatusBytes > LifecycleMaxBytes || !value.Retention.CapacityAvailable) {
		return errors.New("completed receipt lacks bounded lifecycle evidence")
	}
	if err := validateFailures(value.Failures, completed); err != nil {
		return err
	}
	if !completed && len(value.Failures) == 0 {
		return errors.New("stopped receipt lacks a classified failure")
	}
	if err := validateTopology(value.Topology, completed); err != nil {
		return err
	}
	if err := validateTeardown(value.Teardown); err != nil {
		return err
	}
	wantClaims := Claims{SourceFree: true, OneEnvironmentOnly: true}
	if value.Claims != wantClaims {
		return errors.New("receipt claims differ from the fixed negative claims")
	}
	return nil
}

func ValidatePrivatePlan(plan PrivatePlan) error {
	if plan.Schema != PrivatePlanSchema || !diverseNonce(plan.CommitmentNonce) {
		return errors.New("private plan schema or nonce is invalid")
	}
	frozen, err := time.Parse(time.RFC3339, plan.FrozenAt)
	if err != nil {
		return errors.New("frozen_at must be RFC3339")
	}
	approved, err := time.Parse(time.RFC3339, plan.Authorization.ApprovedAt)
	if err != nil {
		return errors.New("authorization.approved_at must be RFC3339")
	}
	expires, err := time.Parse(time.RFC3339, plan.Authorization.ExpiresAt)
	if err != nil || frozen.Before(approved) || !expires.After(frozen) {
		return errors.New("authorization must precede freeze and remain valid")
	}
	for label, value := range map[string]string{
		"approver": plan.Authorization.Approver, "custodian": plan.Authorization.Custodian,
		"repository": plan.Target.Repository, "mirror": plan.Target.MirrorPath,
		"host": plan.Host.Identifier, "os": plan.Host.OperatingSystem,
		"arch": plan.Host.Architecture, "catalog_path": plan.Authority.CatalogPath,
		"catalog_version": plan.Authority.CatalogVersion,
		"teardown_owner":  plan.Teardown.Owner, "teardown_procedure": plan.Teardown.Procedure,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 8<<10 {
			return fmt.Errorf("%s must be nonempty and bounded", label)
		}
	}
	if !gitID(plan.Target.BaseCommit) || !gitID(plan.Target.TargetCommit) ||
		plan.Target.BaseCommit == plan.Target.TargetCommit || !isLowerHex(plan.Artifact.PhebsCommit, 40) ||
		!isSHA256(plan.Artifact.BinarySHA256) || !isSHA256(plan.Artifact.ConfigSHA256) ||
		!isSHA256(plan.Authority.CatalogSHA256) {
		return errors.New("target, artifact, config, or catalog identity is invalid")
	}
	if len(plan.Tools) != len(requiredToolNames) {
		return errors.New("tool identity set is incomplete")
	}
	toolNames := make([]string, 0, len(plan.Tools))
	for _, tool := range plan.Tools {
		if strings.TrimSpace(tool.Version) == "" || len(tool.Version) > 8<<10 || !isSHA256(tool.SHA256) {
			return errors.New("tool identity is invalid")
		}
		toolNames = append(toolNames, tool.Name)
	}
	slices.Sort(toolNames)
	wantTools := slices.Clone(requiredToolNames)
	slices.Sort(wantTools)
	if !slices.Equal(toolNames, wantTools) {
		return errors.New("tool names differ from the frozen set")
	}
	if plan.Host.LogicalCPUs < 1 || plan.Host.MemoryBytes < 1 || plan.Host.DataVolumeAvailableBytes < 1 {
		return errors.New("host capacity is invalid")
	}
	if err := validateThresholds(plan.Thresholds); err != nil {
		return err
	}
	if plan.Thresholds.MinimumAvailableDataBytes > plan.Host.DataVolumeAvailableBytes {
		return errors.New("minimum available capacity exceeds frozen host capacity")
	}
	if plan.Authority.Services < 1 || plan.Authority.Services > 4_000 ||
		plan.Authority.Memberships < 1 || plan.Authority.Memberships > 20_000 ||
		plan.Authority.UnownedPaths < 0 || plan.Authority.UnownedPaths > 12_000 ||
		plan.Authority.MaxAcceptedFanout < 1 || plan.Authority.MaxAcceptedFanout > 20 ||
		plan.Authority.CanonicalBytes < 1 || plan.Authority.CanonicalBytes > 5<<20 ||
		!plan.Authority.AuthorityConfirmed {
		return errors.New("frozen catalog authority is outside the production envelope")
	}
	if err := validateQueryBattery(plan.QueryBattery); err != nil {
		return err
	}
	if !slices.Equal(plan.PhaseOrder, phaseOrder) || !sameSet(plan.FailureClasses, failureClasses) {
		return errors.New("phase order or failure classes differ from the closed contract")
	}
	if len(plan.StopConditions) == 0 || len(plan.StopConditions) > 64 {
		return errors.New("stop conditions must contain 1..64 entries")
	}
	seenStops := map[string]struct{}{}
	for _, stop := range plan.StopConditions {
		if stop.ID == "" || len(stop.ID) > 128 || !slices.Contains(failureClasses, stop.Class) ||
			strings.TrimSpace(stop.Condition) == "" || len(stop.Condition) > 8<<10 {
			return errors.New("stop condition is invalid")
		}
		if _, duplicate := seenStops[stop.ID]; duplicate {
			return errors.New("duplicate stop condition")
		}
		seenStops[stop.ID] = struct{}{}
	}
	deadline, err := time.Parse(time.RFC3339, plan.Teardown.Deadline)
	if err != nil || !deadline.After(frozen) || deadline.After(expires) {
		return errors.New("teardown deadline must be after freeze and within authorization")
	}
	if !plan.Safety.DedicatedDataDirectory || !plan.Safety.RawArtifactsOutsideRepo ||
		!plan.Safety.NoPlanMutationAfterFreeze || !plan.Safety.DirectTopologyOnly ||
		!plan.Safety.NoAutomaticAuthority {
		return errors.New("all private safety fences must be true")
	}
	return nil
}

func validateThresholds(value PrivateThresholds) error {
	positive := []int64{
		value.TotalWallMS, value.ColdWallMS, value.ColdPeakRSSBytes,
		value.ColdShardAllocatedBytes, value.ColdDerivedAllocatedBytes,
		value.ColdAvailabilityMS, value.IncrementalCatchupMS,
		value.IncrementalPeakRSSBytes, value.NoOpWallMS,
		value.QueryColdMaxWallMS, value.QueryWarmMaxWallMS,
		value.RecoveryWallMS, value.RestoreWallMS, value.RetentionWallMS,
		value.MinimumAvailableDataBytes,
	}
	for _, current := range positive {
		if current <= 0 {
			return errors.New("every operating threshold must be positive")
		}
	}
	if value.MaximumLifecycleStatusBytes != LifecycleMaxBytes || value.MaximumFinalFailureCount != 0 {
		return errors.New("lifecycle status and completed failure thresholds must remain fixed")
	}
	return nil
}

func validateQueryBattery(queries []PrivateQuery) error {
	if len(queries) < 3 || len(queries) > 64 {
		return errors.New("query battery must contain 3..64 entries")
	}
	classes, scopes, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, query := range queries {
		if query.ID == "" || len(query.ID) > 128 || ids[query.ID] ||
			!slices.Contains([]string{"broad", "literal", "path", "symbol", "regexp"}, query.Class) ||
			!slices.Contains([]string{"all_code", "service", "relationship"}, query.Scope) ||
			strings.TrimSpace(query.Query) == "" || len(query.Query) > 8<<10 ||
			(query.Scope == "service" || query.Scope == "relationship") != (query.ServiceKey != "") {
			return errors.New("query battery contains an invalid entry")
		}
		ids[query.ID], classes[query.Class], scopes[query.Scope] = true, true, true
	}
	for _, required := range []string{"broad", "literal", "path", "symbol"} {
		if !classes[required] {
			return fmt.Errorf("query battery omits class %q", required)
		}
	}
	for _, required := range []string{"all_code", "service", "relationship"} {
		if !scopes[required] {
			return fmt.Errorf("query battery omits scope %q", required)
		}
	}
	return nil
}

func validateObservation(plan PrivatePlan, value Observation) error {
	if value.Schema != ObservationSchema || value.PhebsCommit != plan.Artifact.PhebsCommit {
		return errors.New("observation schema or phebs commit differs from the plan")
	}
	if _, err := time.Parse("2006-01-02", value.MeasuredOn); err != nil {
		return errors.New("measured_on must be YYYY-MM-DD")
	}
	if value.Outcome != "completed" && value.Outcome != "stopped" {
		return errors.New("outcome must be completed or stopped")
	}
	completed := value.Outcome == "completed"
	if value.TotalWallMS < 0 || completed && value.TotalWallMS > plan.Thresholds.TotalWallMS {
		return errors.New("total wall time violates the frozen threshold")
	}
	if err := validatePreflight(plan, value.Preflight); err != nil {
		return err
	}
	if completed && value.Preflight.Outcome != "succeeded" {
		return errors.New("completed run requires a successful preflight")
	}
	if value.Authority != (AuthorityObservation{
		Services: plan.Authority.Services, Memberships: plan.Authority.Memberships,
		UnownedPaths:      plan.Authority.UnownedPaths,
		MaxAcceptedFanout: plan.Authority.MaxAcceptedFanout,
		CanonicalBytes:    plan.Authority.CanonicalBytes,
	}) {
		return errors.New("observed catalog authority differs from the frozen plan")
	}
	if err := validateCold(plan, value.Cold, completed); err != nil {
		return err
	}
	if err := validateIncremental(plan, value.Incremental, completed); err != nil {
		return err
	}
	if err := validateNoOp(plan, value.NoOp, completed); err != nil {
		return err
	}
	if err := validateQueries(plan, value.Query, completed); err != nil {
		return err
	}
	if err := validateRecovery(plan, value.Recovery, completed); err != nil {
		return err
	}
	if err := validateRestore(plan, value.Restore, completed); err != nil {
		return err
	}
	if err := validateRetention(plan, value.Retention, completed); err != nil {
		return err
	}
	if err := validateFailures(value.Failures, completed); err != nil {
		return err
	}
	if !completed && len(value.Failures) == 0 {
		return errors.New("stopped run must retain at least one classified failure")
	}
	if err := validateTopology(value.Topology, completed); err != nil {
		return err
	}
	if err := validateTeardown(value.Teardown); err != nil {
		return err
	}
	return nil
}

func validatePreflight(plan PrivatePlan, value Preflight) error {
	if err := outcome(value.Outcome); err != nil || value.ToolsExact < 0 || value.ToolsExact > len(plan.Tools) {
		return errors.New("preflight observation is invalid")
	}
	if value.Outcome == "succeeded" && (!value.ArtifactExact || !value.ConfigExact ||
		!value.SourceExact || !value.CatalogExact || !value.HostExact || !value.AuthorityConfirmed ||
		!value.AuthorizationValid || value.ToolsExact != len(plan.Tools)) {
		return errors.New("successful preflight must prove every frozen identity")
	}
	return nil
}

func validateCold(plan PrivatePlan, value ColdObservation, completed bool) error {
	if err := outcome(value.Outcome); err != nil || !nonnegative(
		value.WallMS, value.PeakRSSBytes, value.ShardAllocatedBytes,
		int64(value.ShardCount), value.DerivedAllocatedBytes, value.SourceMemberBytes,
		int64(value.SourceMembers), value.ObservationBytes, int64(value.ObservationRecords),
		value.RelationshipBytes, int64(value.RelationshipRecords), value.AvailabilityMS,
	) {
		return errors.New("cold observation is invalid")
	}
	if value.Outcome == "succeeded" && (!value.Current || value.ShardCount < 1 || value.SourceMembers < 1) {
		return errors.New("successful cold run must publish current nonempty source/search authority")
	}
	if completed && (value.Outcome != "succeeded" || value.WallMS > plan.Thresholds.ColdWallMS ||
		value.PeakRSSBytes > plan.Thresholds.ColdPeakRSSBytes ||
		value.ShardAllocatedBytes > plan.Thresholds.ColdShardAllocatedBytes ||
		value.DerivedAllocatedBytes > plan.Thresholds.ColdDerivedAllocatedBytes ||
		value.AvailabilityMS > plan.Thresholds.ColdAvailabilityMS) {
		return errors.New("cold observation violates the completed-run envelope")
	}
	return nil
}

func validateIncremental(plan PrivatePlan, value IncrementalObservation, completed bool) error {
	if err := outcome(value.Outcome); err != nil || !nonnegative(value.WallMS, value.PeakRSSBytes,
		int64(value.ChangedFiles), value.ShardAllocatedBytesAfter, int64(value.UnaffectedServicesExact)) {
		return errors.New("incremental observation is invalid")
	}
	if value.Outcome == "succeeded" && (!value.Current || value.ChangedFiles < 1 ||
		!value.SourceGenerationChanged || !value.SearchGenerationChanged ||
		!value.ObservationGenerationChanged || !value.RelationshipGenerationChecked) {
		return errors.New("successful incremental run must transition source/search/observation and check relationship authority")
	}
	if completed && (value.Outcome != "succeeded" || value.WallMS > plan.Thresholds.IncrementalCatchupMS ||
		value.PeakRSSBytes > plan.Thresholds.IncrementalPeakRSSBytes) {
		return errors.New("incremental observation violates the completed-run envelope")
	}
	return nil
}

func validateNoOp(plan PrivatePlan, value NoOpObservation, completed bool) error {
	if err := outcome(value.Outcome); err != nil || !nonnegative(value.WallMS, int64(value.IndexChildren),
		int64(value.GitChildren), int64(value.NewScheduleChunks), value.ShardBytesDelta,
		int64(value.GenerationChanges), int64(value.DurableWrites)) {
		return errors.New("no-op observation is invalid")
	}
	if value.Outcome == "succeeded" && (value.IndexChildren != 0 || value.GitChildren != 0 ||
		value.NewScheduleChunks != 0 || value.ShardBytesDelta != 0 ||
		value.GenerationChanges != 0 || value.DurableWrites != 0) {
		return errors.New("successful no-op must retain exact zero work counters")
	}
	if completed && (value.Outcome != "succeeded" || value.WallMS > plan.Thresholds.NoOpWallMS) {
		return errors.New("no-op observation violates the completed-run envelope")
	}
	return nil
}

func validateQueries(plan PrivatePlan, value QueryObservation, completed bool) error {
	if err := outcome(value.Outcome); err != nil || value.QueryCount != len(plan.QueryBattery) {
		return errors.New("query observation differs from the frozen battery")
	}
	for _, pass := range []QueryPass{value.Cold, value.Warm} {
		if !nonnegative(int64(pass.Completed), int64(pass.Failed), pass.TotalWallMS, pass.MaxWallMS) ||
			pass.Completed+pass.Failed > value.QueryCount || pass.MaxWallMS > pass.TotalWallMS {
			return errors.New("query pass is incomplete")
		}
	}
	if value.Outcome == "succeeded" && (value.Cold.Failed != 0 || value.Warm.Failed != 0 ||
		value.Cold.Completed != value.QueryCount || value.Warm.Completed != value.QueryCount ||
		!value.ResultSetsEqual || value.AuthorityReceipts != value.QueryCount*2 ||
		value.FinalFencesPassed != value.QueryCount*2 || value.AuthorizationPassed != value.QueryCount*2) {
		return errors.New("successful query battery lacks equality, authority, or final fences")
	}
	if completed && (value.Outcome != "succeeded" || value.Cold.MaxWallMS > plan.Thresholds.QueryColdMaxWallMS ||
		value.Warm.MaxWallMS > plan.Thresholds.QueryWarmMaxWallMS) {
		return errors.New("query observation violates the completed-run envelope")
	}
	return nil
}

func validateRecovery(plan PrivatePlan, value RecoveryObservation, completed bool) error {
	if err := outcome(value.Outcome); err != nil || !slices.Contains(
		[]string{"source", "search", "observation", "relationship"}, value.InterruptedPhase,
	) || value.WallMS < 0 {
		return errors.New("recovery observation is invalid")
	}
	if value.Outcome == "succeeded" && (!value.PriorPublicationPreserved || !value.PartialInvisible || !value.EventualCurrent) {
		return errors.New("successful recovery must preserve prior authority and hide partial state")
	}
	if completed && (value.Outcome != "succeeded" || value.WallMS > plan.Thresholds.RecoveryWallMS) {
		return errors.New("recovery observation violates the completed-run envelope")
	}
	return nil
}

func validateRestore(plan PrivatePlan, value RestoreObservation, completed bool) error {
	if err := outcome(value.Outcome); err != nil || !nonnegative(value.WallMS,
		int64(value.DerivedGenerationsExact), int64(value.DerivedGenerationsOmitted)) {
		return errors.New("restore observation is invalid")
	}
	if value.Outcome == "succeeded" && (!value.PreciousAuthorityExact || !value.EventualCurrent ||
		value.DerivedGenerationsExact+value.DerivedGenerationsOmitted == 0) {
		return errors.New("successful restore must preserve precious authority and converge")
	}
	if completed && (value.Outcome != "succeeded" || value.WallMS > plan.Thresholds.RestoreWallMS) {
		return errors.New("restore observation violates the completed-run envelope")
	}
	return nil
}

func validateRetention(plan PrivatePlan, value RetentionObservation, completed bool) error {
	if err := outcome(value.Outcome); err != nil || !nonnegative(value.WallMS,
		int64(value.OwnersObserved), int64(value.StatusBytes), value.AvailableBeforeBytes,
		value.AvailableAfterBytes, int64(value.SweepTurns), int64(value.DeletedRows),
		int64(value.DeletedGenerations), int64(value.ProtectedRoots)) {
		return errors.New("retention observation is invalid")
	}
	if value.Outcome == "succeeded" && (value.OwnersObserved != LifecycleOwners ||
		value.StatusBytes > plan.Thresholds.MaximumLifecycleStatusBytes || !value.CapacityAvailable) {
		return errors.New("successful retention observation is incomplete")
	}
	if completed && (value.Outcome != "succeeded" || value.WallMS > plan.Thresholds.RetentionWallMS ||
		value.AvailableAfterBytes < plan.Thresholds.MinimumAvailableDataBytes) {
		return errors.New("retention observation violates the completed-run envelope")
	}
	return nil
}

func validateFailures(values []FailureObservation, completed bool) error {
	if len(values) > 64 || completed && len(values) != 0 {
		return errors.New("completed run must retain an empty bounded failure census")
	}
	for _, value := range values {
		if !slices.Contains(phaseOrder, value.Phase) || !slices.Contains(failureClasses, value.Class) {
			return errors.New("failure census contains an open phase or class")
		}
	}
	return nil
}

func validateTopology(value TopologyDecision, completed bool) error {
	if !slices.Contains([]string{
		"direct_within_frozen_environment",
		"direct_failed_reopen_cohorts",
		"direct_failed_reopen_p6",
		"stopped_before_decision",
	}, value.Reason) ||
		!slices.Contains([]string{"continue", "stop"}, value.Direct) ||
		!slices.Contains([]string{"not_triggered", "reopen"}, value.Cohorts) ||
		!slices.Contains([]string{"not_triggered", "reopen"}, value.P6) {
		return errors.New("topology decision is invalid")
	}
	if completed && (value.Direct != "continue" || value.Cohorts != "not_triggered" || value.P6 != "not_triggered") {
		return errors.New("completed direct run must retain untriggered cohort and P6 alternatives")
	}
	if completed && value.Reason != "direct_within_frozen_environment" {
		return errors.New("completed run must retain the direct frozen-environment reason")
	}
	want := map[string][3]string{
		"direct_within_frozen_environment": {"continue", "not_triggered", "not_triggered"},
		"direct_failed_reopen_cohorts":     {"stop", "reopen", "not_triggered"},
		"direct_failed_reopen_p6":          {"stop", "not_triggered", "reopen"},
		"stopped_before_decision":          {"stop", "not_triggered", "not_triggered"},
	}[value.Reason]
	if value.Direct != want[0] || value.Cohorts != want[1] || value.P6 != want[2] {
		return errors.New("topology decision and reason disagree")
	}
	return nil
}

func validateTeardown(value TeardownObservation) error {
	if value.WallMS < 0 || !slices.Contains([]string{"destroyed", "retained_under_authorization"}, value.Outcome) ||
		!slices.Contains([]string{"released", "renewed"}, value.Custody) || value.DerivedDataRetained {
		return errors.New("teardown or custody is invalid")
	}
	if value.Outcome == "destroyed" && (value.SourceCopyRetained || value.Custody != "released") {
		return errors.New("destroyed teardown must release source custody")
	}
	if value.Outcome == "retained_under_authorization" && (!value.SourceCopyRetained || value.Custody != "renewed") {
		return errors.New("retained source requires renewed custody")
	}
	return nil
}

func decodeStrict[T any](data []byte) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return value, err
	}
	return value, nil
}

func outcome(value string) error {
	if !slices.Contains([]string{"succeeded", "failed", "stopped", "not_run"}, value) {
		return fmt.Errorf("unknown outcome %q", value)
	}
	return nil
}

func nonnegative(values ...int64) bool {
	for _, value := range values {
		if value < 0 {
			return false
		}
	}
	return true
}

func sameSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left, right = slices.Clone(left), slices.Clone(right)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func isSHA256(value string) bool {
	return strings.HasPrefix(value, "sha256:") && isLowerHex(strings.TrimPrefix(value, "sha256:"), 64)
}

func gitID(value string) bool { return isLowerHex(value, 40) || isLowerHex(value, 64) }

func isLowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func diverseNonce(value string) bool {
	if !isLowerHex(value, 64) {
		return false
	}
	decoded, _ := hex.DecodeString(value)
	unique := map[byte]struct{}{}
	for _, current := range decoded {
		unique[current] = struct{}{}
	}
	return len(unique) >= 16
}

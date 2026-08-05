// Package t322 builds the source-free public receipt for T32.2 from one
// separately frozen private plan and one source-free observation. Production
// packages must not import spike packages.
package t322

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	PrivatePlanSchema  = "t322-private-authorized-plan-v1"
	ObservationSchema  = "t322-source-free-observation-v1"
	ReceiptSchema      = "t322-authorized-whole-monorepo-baseline-v1"
	MaxPrivatePlanSize = 256 << 10
	MaxObservationSize = 256 << 10
	MaxReceiptSize     = 64 << 10
)

var failureClasses = []string{
	"physical_index", "candidate", "extraction", "relationship",
	"store_retention", "environment",
}

var phaseOrder = []string{
	"clone_fetch", "index", "restart", "search", "candidate",
	"extraction", "recovery", "retention", "teardown",
}

var requiredRetainedFields = []string{
	"clone_fetch.posture", "clone_fetch.wall_ms", "index.wall_ms",
	"index.peak_rss_bytes", "index.shard_bytes", "index.shard_count",
	"index.publication", "restart.wall_ms", "restart.index_child_runs",
	"restart.shard_bytes_delta", "search.cold", "search.warm",
	"candidate.phases", "candidate.peak_spool_bytes", "extraction.phases",
	"extraction.outcomes", "recovery", "retention.delta", "failures",
	"teardown",
}

var allowedQueryClasses = stringSet(
	"broad", "literal", "path", "symbol", "regexp",
)

var requiredQueryClasses = []string{"broad", "literal", "path", "symbol"}

var allowedDomains = stringSet(
	"grpc-caller", "grpc-consumer", "kafka-consumer", "kafka-producer",
	"proto-contract", "scip-proto-field", "scip-thrift-field",
	"thrift-caller", "thrift-consumer", "thrift-contract",
)

var allowedReasons = stringSet(
	"already_current", "not_ready", "stale", "no_candidates",
	"typed_input_absent", "limit_refusal", "published_empty",
	"published_nonempty", "aggregate_budget", "domain_budget", "canceled",
	"failed",
)

var allowedRetentionComponents = stringSet(
	"extraction_run", "snapshot_evidence", "assertion", "evidence_atom",
	"extraction_attempt", "extraction_domain_outcome",
	"evidence_pin[kind=proof-bundle:<bundle_id>]",
	"evidence_pin[kind=investigation-artifact:<artifact_id>]",
	"evidence_pin[kind=<other exact store-accepted value>]", "proof_bundle",
	"caller_generation_publication", "caller_generation_admission",
	"caller_leaf_outcome", "investigation", "investigation_revision",
	"investigation_change_brief", "investigation_workbench_mutation",
	"investigation_workbench_disposition", "investigation_run",
	"investigation_run_event", "investigation_run_artifact",
	"investigation_artifact_owner", "investigation_artifact_owner_release",
	"investigation_artifact_retention_override", "investigation_decision",
	"investigation_disposition", "investigation_baseline_designation",
	"investigation_grant", "investigation_cursor", "investigation_creation",
	"investigation_consumer_snapshot", "investigation_consumer_edge_ledger",
	"investigation_review_projection", "investigation_review_item",
	"investigation_dossier", "investigation_watch",
	"investigation_watch_revision", "candidate_manifest_publication",
	"$DATA/candidates managed publication files",
	"repo indexed analysis-unit/revision state",
	"$DATA/index focused publication files", "resolver_catalog_publication",
	"$DATA/resolver-catalogs package-owned files",
	"$DATA/caller-leaves managed manifests and leaf artifacts",
	"connection_sync_job", "indexing_job", "repo_fetch_job",
	"candidate_manifest_job", "extraction_job", "resolver_catalog_job",
	"caller_leaf_job", "investigation_run_job",
)

var allowedCompleteness = stringSet("exact", "lower_bound", "unavailable")
var allowedByteKinds = stringSet(
	"logical_encoded", "canonical_content", "canonical_receipt",
	"apparent_file", "physical_database",
)

type PrivatePlan struct {
	Schema                      string                 `json:"schema"`
	CommitmentNonce             string                 `json:"commitment_nonce"`
	FrozenAt                    string                 `json:"frozen_at"`
	Authorization               PrivateAuthorization   `json:"authorization"`
	Target                      PrivateTarget          `json:"target"`
	Artifact                    PrivateArtifact        `json:"artifact"`
	Host                        PrivateHost            `json:"host"`
	Ceilings                    PrivateCeilings        `json:"ceilings"`
	QueryBattery                []PrivateQuery         `json:"query_battery"`
	CloneFetchPosture           string                 `json:"clone_fetch_posture"`
	ExpectedExtractionDomains   []string               `json:"expected_extraction_domains"`
	FailureClasses              []string               `json:"failure_classes"`
	RetainedScalarFields        []string               `json:"retained_scalar_fields"`
	PhaseOrder                  []string               `json:"phase_order"`
	StopConditions              []PrivateStopCondition `json:"stop_conditions"`
	Teardown                    PrivateTeardown        `json:"teardown"`
	ProvisionalPacksOffForIndex bool                   `json:"provisional_packs_off_for_index"`
	OneExtractionPackAtATime    bool                   `json:"one_extraction_pack_at_a_time"`
	RawArtifactsOutsideRepo     bool                   `json:"raw_artifacts_outside_repository"`
}

type PrivateAuthorization struct {
	ApprovedAt string `json:"approved_at"`
	ExpiresAt  string `json:"expires_at"`
	Approver   string `json:"approver"`
	Custodian  string `json:"custodian"`
}

type PrivateTarget struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	MirrorPath string `json:"mirror_path"`
}

type PrivateArtifact struct {
	PhebsCommit  string `json:"phebs_commit"`
	BinarySHA256 string `json:"binary_sha256"`
	ConfigSHA256 string `json:"config_sha256"`
}

type PrivateHost struct {
	Identifier               string `json:"identifier"`
	OperatingSystem          string `json:"operating_system"`
	Architecture             string `json:"architecture"`
	LogicalCPUs              int    `json:"logical_cpus"`
	MemoryBytes              int64  `json:"memory_bytes"`
	DataVolumeAvailableBytes int64  `json:"data_volume_available_bytes"`
}

type PrivateCeilings struct {
	TotalWallMS               int64 `json:"total_wall_ms"`
	IndexWallMS               int64 `json:"index_wall_ms"`
	IndexPeakRSSBytes         int64 `json:"index_peak_rss_bytes"`
	IndexShardBytes           int64 `json:"index_shard_bytes"`
	RestartWallMS             int64 `json:"restart_wall_ms"`
	SearchColdMaxWallMS       int64 `json:"search_cold_max_wall_ms"`
	SearchWarmMaxWallMS       int64 `json:"search_warm_max_wall_ms"`
	CandidateWallMS           int64 `json:"candidate_wall_ms"`
	CandidatePeakSpoolBytes   int64 `json:"candidate_peak_spool_bytes"`
	ExtractionWallMS          int64 `json:"extraction_wall_ms"`
	RecoveryWallMS            int64 `json:"recovery_wall_ms"`
	RetentionWallMS           int64 `json:"retention_wall_ms"`
	MinimumAvailableDataBytes int64 `json:"minimum_available_data_bytes"`
}

type PrivateQuery struct {
	ID    string `json:"id"`
	Class string `json:"class"`
	Query string `json:"query"`
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

type Observation struct {
	Schema      string                  `json:"schema"`
	MeasuredOn  string                  `json:"measured_on"`
	PhebsCommit string                  `json:"phebs_commit"`
	Outcome     string                  `json:"outcome"`
	TotalWallMS int64                   `json:"total_wall_ms"`
	CloneFetch  CloneFetchObservation   `json:"clone_fetch"`
	Index       IndexObservation        `json:"index"`
	Restart     RestartObservation      `json:"restart"`
	Search      SearchObservation       `json:"search"`
	Candidate   CandidateObservation    `json:"candidate"`
	Extractions []ExtractionObservation `json:"extractions"`
	Recovery    RecoveryObservation     `json:"recovery"`
	Retention   RetentionObservation    `json:"retention"`
	Failures    []FailureObservation    `json:"failures"`
	Teardown    TeardownObservation     `json:"teardown"`
}

type CloneFetchObservation struct {
	Posture string `json:"posture"`
	WallMS  int64  `json:"wall_ms"`
}

type IndexObservation struct {
	Outcome      string `json:"outcome"`
	WallMS       int64  `json:"wall_ms"`
	PeakRSSBytes int64  `json:"peak_rss_bytes"`
	ShardBytes   int64  `json:"shard_bytes"`
	ShardCount   int    `json:"shard_count"`
	Publication  string `json:"publication"`
}

type RestartObservation struct {
	Outcome           string `json:"outcome"`
	WallMS            int64  `json:"wall_ms"`
	AlreadyCurrent    bool   `json:"already_current"`
	IndexChildRuns    int    `json:"index_child_runs"`
	ShardBytesDelta   int64  `json:"shard_bytes_delta"`
	ShardMTimeChanges int    `json:"shard_mtime_changes"`
}

type SearchObservation struct {
	Outcome         string     `json:"outcome"`
	QueryCount      int        `json:"query_count"`
	Cold            SearchPass `json:"cold"`
	Warm            SearchPass `json:"warm"`
	ResultSetsEqual bool       `json:"result_sets_equal"`
}

type SearchPass struct {
	Completed   int   `json:"completed"`
	Failed      int   `json:"failed"`
	TotalWallMS int64 `json:"total_wall_ms"`
	MaxWallMS   int64 `json:"max_wall_ms"`
}

type CandidateObservation struct {
	Outcome             string          `json:"outcome"`
	Decision            string          `json:"decision"`
	QueueWaitMS         int64           `json:"queue_wait_ms"`
	MirrorLockWaitMS    int64           `json:"mirror_lock_wait_ms"`
	Phases              CandidatePhases `json:"phases"`
	PeakSpoolBytes      int64           `json:"peak_spool_bytes"`
	TotalMS             int64           `json:"total_ms"`
	DeclaredSourceBytes int64           `json:"declared_source_bytes"`
	RepositoryRecords   int             `json:"repository_records"`
	LocalRecords        int             `json:"local_records"`
	CallerRecords       int             `json:"caller_records"`
}

type CandidatePhases struct {
	TreeMS           int64 `json:"tree_ms"`
	SpoolingMS       int64 `json:"spooling_ms"`
	ExternalSortMS   int64 `json:"external_sort_ms"`
	PublishMS        int64 `json:"publish_ms"`
	FingerprintMS    int64 `json:"fingerprint_ms"`
	DatabaseCommitMS int64 `json:"database_commit_ms"`
	MarkerFinishMS   int64 `json:"marker_finish_ms"`
}

type ExtractionObservation struct {
	SerialOrdinal  int              `json:"serial_ordinal"`
	Domain         string           `json:"domain"`
	Outcome        string           `json:"outcome"`
	Reason         string           `json:"reason"`
	QueueWaitMS    int64            `json:"queue_wait_ms"`
	MirrorLockMS   int64            `json:"mirror_lock_wait_ms"`
	PointerWorkMS  int64            `json:"pointer_work_ms"`
	StrictOpenMS   int64            `json:"strict_open_ms"`
	Phases         ExtractionPhases `json:"phases"`
	TotalMS        int64            `json:"total_ms"`
	CandidateFiles int              `json:"candidate_files"`
	OpenedFiles    int              `json:"opened_source_files"`
	OpenedBytes    int64            `json:"opened_source_bytes"`
	Facts          int              `json:"facts"`
	Unresolved     int              `json:"unresolved"`
}

type ExtractionPhases struct {
	InventoryMS    int64 `json:"inventory_ms"`
	OpenedSourceMS int64 `json:"opened_source_ms"`
	ExtractorMS    int64 `json:"extractor_ms"`
	StagingMS      int64 `json:"staging_ms"`
	PublicationMS  int64 `json:"publication_ms"`
	AbortMS        int64 `json:"abort_ms"`
	CleanupMS      int64 `json:"cleanup_ms"`
}

type RecoveryObservation struct {
	Outcome                   string `json:"outcome"`
	InterruptedPhase          string `json:"interrupted_phase"`
	WallMS                    int64  `json:"wall_ms"`
	PriorPublicationPreserved bool   `json:"prior_publication_preserved"`
	EventualCurrent           bool   `json:"eventual_current"`
}

type RetentionObservation struct {
	Outcome             string                    `json:"outcome"`
	WallMS              int64                     `json:"wall_ms"`
	ComponentsObserved  int                       `json:"components_observed"`
	UnchangedComponents int                       `json:"unchanged_components"`
	Changed             []RetentionComponentDelta `json:"changed"`
	DataVolumeBefore    RetentionDataVolume       `json:"data_volume_before"`
	DataVolumeAfter     RetentionDataVolume       `json:"data_volume_after"`
}

type RetentionComponentDelta struct {
	Component   string               `json:"component"`
	CountBefore RetentionMetric      `json:"count_before"`
	CountAfter  RetentionMetric      `json:"count_after"`
	Bytes       []RetentionByteDelta `json:"bytes"`
}

type RetentionMetric struct {
	Value        *int64 `json:"value"`
	Completeness string `json:"completeness"`
}

type RetentionByteDelta struct {
	Kind   string          `json:"kind"`
	Before RetentionMetric `json:"before"`
	After  RetentionMetric `json:"after"`
}

type RetentionDataVolume struct {
	TotalBytes     RetentionMetric `json:"total_bytes"`
	AvailableBytes RetentionMetric `json:"available_bytes"`
}

type FailureObservation struct {
	Phase    string `json:"phase"`
	Class    string `json:"class"`
	Terminal bool   `json:"terminal"`
}

type TeardownObservation struct {
	Outcome             string `json:"outcome"`
	WallMS              int64  `json:"wall_ms"`
	SourceCopyRetained  bool   `json:"source_copy_retained"`
	DerivedDataRetained bool   `json:"derived_data_retained"`
}

type Claims struct {
	SourceFree                    bool `json:"source_free"`
	EstablishesSLO                bool `json:"establishes_slo"`
	EstablishesAccuracy           bool `json:"establishes_accuracy"`
	SelectsTopology               bool `json:"selects_topology"`
	AuthorizesMultiServiceRelease bool `json:"authorizes_multi_service_release"`
}

type Receipt struct {
	Schema        string                  `json:"schema"`
	MeasuredOn    string                  `json:"measured_on"`
	PhebsCommit   string                  `json:"phebs_commit"`
	RunCommitment string                  `json:"run_commitment"`
	Outcome       string                  `json:"outcome"`
	TotalWallMS   int64                   `json:"total_wall_ms"`
	CloneFetch    CloneFetchObservation   `json:"clone_fetch"`
	Index         IndexObservation        `json:"index"`
	Restart       RestartObservation      `json:"restart"`
	Search        SearchObservation       `json:"search"`
	Candidate     CandidateObservation    `json:"candidate"`
	Extractions   []ExtractionObservation `json:"extractions"`
	Recovery      RecoveryObservation     `json:"recovery"`
	Retention     RetentionObservation    `json:"retention"`
	Failures      []FailureObservation    `json:"failures"`
	Teardown      TeardownObservation     `json:"teardown"`
	Claims        Claims                  `json:"claims"`
}

func PlanDigest(planBytes []byte) string {
	sum := sha256.Sum256(planBytes)
	return "sha256:" + hex.EncodeToString(sum[:])
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
	if err := validatePrivatePlan(plan); err != nil {
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
	_, _ = hash.Write([]byte("phebs-t322-run-commitment-v1\x00"))
	_, _ = hash.Write(planBytes)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(observationBytes)
	receipt := Receipt{
		Schema: ReceiptSchema, MeasuredOn: observation.MeasuredOn,
		PhebsCommit:   observation.PhebsCommit,
		RunCommitment: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		Outcome:       observation.Outcome, TotalWallMS: observation.TotalWallMS,
		CloneFetch: observation.CloneFetch, Index: observation.Index,
		Restart: observation.Restart, Search: observation.Search,
		Candidate: observation.Candidate, Extractions: observation.Extractions,
		Recovery: observation.Recovery, Retention: observation.Retention,
		Failures: observation.Failures, Teardown: observation.Teardown,
		Claims: Claims{SourceFree: true},
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode receipt: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxReceiptSize {
		return nil, fmt.Errorf("receipt size %d exceeds %d", len(encoded), MaxReceiptSize)
	}
	return encoded, nil
}

func validatePrivatePlan(plan PrivatePlan) error {
	if plan.Schema != PrivatePlanSchema {
		return fmt.Errorf("schema %q is not %q", plan.Schema, PrivatePlanSchema)
	}
	if !isDiverseNonce(plan.CommitmentNonce) {
		return errors.New("commitment_nonce must be 32 diverse random bytes in lowercase hex")
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
	if err != nil || !expires.After(frozen) || frozen.Before(approved) {
		return errors.New("authorization must precede freeze and remain valid after it")
	}
	for name, value := range map[string]string{
		"authorization.approver":  plan.Authorization.Approver,
		"authorization.custodian": plan.Authorization.Custodian,
		"target.repository":       plan.Target.Repository,
		"target.mirror_path":      plan.Target.MirrorPath,
		"host.identifier":         plan.Host.Identifier,
		"host.operating_system":   plan.Host.OperatingSystem,
		"host.architecture":       plan.Host.Architecture,
		"teardown.owner":          plan.Teardown.Owner,
		"teardown.procedure":      plan.Teardown.Procedure,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 8<<10 {
			return fmt.Errorf("%s must be nonempty and bounded", name)
		}
	}
	if !isLowerHex(plan.Target.Commit, 40) && !isLowerHex(plan.Target.Commit, 64) {
		return errors.New("target.commit must be an exact lowercase Git object ID")
	}
	if !isLowerHex(plan.Artifact.PhebsCommit, 40) ||
		!isSHA256(plan.Artifact.BinarySHA256) || !isSHA256(plan.Artifact.ConfigSHA256) {
		return errors.New("artifact commit and SHA-256 identities are invalid")
	}
	if plan.Host.LogicalCPUs <= 0 || plan.Host.MemoryBytes <= 0 ||
		plan.Host.DataVolumeAvailableBytes <= 0 {
		return errors.New("host capacity fields must be positive")
	}
	if err := validateCeilings(plan.Ceilings); err != nil {
		return err
	}
	if plan.Ceilings.MinimumAvailableDataBytes > plan.Host.DataVolumeAvailableBytes {
		return errors.New("minimum available-data ceiling exceeds preregistered host capacity")
	}
	if plan.CloneFetchPosture != "excluded" && plan.CloneFetchPosture != "measured" {
		return errors.New("clone_fetch_posture must be excluded or measured")
	}
	if len(plan.QueryBattery) == 0 || len(plan.QueryBattery) > 256 {
		return errors.New("query_battery must contain 1..256 entries")
	}
	queryIDs, queryClasses := map[string]struct{}{}, map[string]struct{}{}
	for _, query := range plan.QueryBattery {
		if query.ID == "" || len(query.ID) > 128 || strings.TrimSpace(query.Query) == "" ||
			len(query.Query) > 8<<10 || !allowedQueryClasses[query.Class] {
			return errors.New("query_battery contains an invalid entry")
		}
		if _, duplicate := queryIDs[query.ID]; duplicate {
			return fmt.Errorf("duplicate query id %q", query.ID)
		}
		queryIDs[query.ID], queryClasses[query.Class] = struct{}{}, struct{}{}
	}
	for _, class := range requiredQueryClasses {
		if _, present := queryClasses[class]; !present {
			return fmt.Errorf("query_battery omits required class %q", class)
		}
	}
	if len(plan.ExpectedExtractionDomains) == 0 || len(plan.ExpectedExtractionDomains) > 16 {
		return errors.New("expected_extraction_domains must contain 1..16 domains")
	}
	seenDomains := map[string]struct{}{}
	for _, domain := range plan.ExpectedExtractionDomains {
		if !allowedDomains[domain] {
			return fmt.Errorf("unsupported extraction domain %q", domain)
		}
		if _, duplicate := seenDomains[domain]; duplicate {
			return fmt.Errorf("duplicate extraction domain %q", domain)
		}
		seenDomains[domain] = struct{}{}
	}
	if !sameStringSet(plan.FailureClasses, failureClasses) {
		return errors.New("failure_classes must contain the six frozen classes")
	}
	if !containsStrings(plan.RetainedScalarFields, requiredRetainedFields) {
		return errors.New("retained_scalar_fields omits a required field")
	}
	if !slices.Equal(plan.PhaseOrder, phaseOrder) {
		return errors.New("phase_order does not match the frozen sequence")
	}
	if len(plan.StopConditions) == 0 || len(plan.StopConditions) > 64 {
		return errors.New("stop_conditions must contain 1..64 entries")
	}
	stopIDs := map[string]struct{}{}
	for _, stop := range plan.StopConditions {
		if stop.ID == "" || len(stop.ID) > 128 || !slices.Contains(failureClasses, stop.Class) ||
			strings.TrimSpace(stop.Condition) == "" || len(stop.Condition) > 8<<10 {
			return errors.New("stop_conditions contains an invalid entry")
		}
		if _, duplicate := stopIDs[stop.ID]; duplicate {
			return fmt.Errorf("duplicate stop condition %q", stop.ID)
		}
		stopIDs[stop.ID] = struct{}{}
	}
	deadline, err := time.Parse(time.RFC3339, plan.Teardown.Deadline)
	if err != nil || !deadline.After(frozen) || deadline.After(expires) {
		return errors.New("teardown.deadline must be RFC3339 after freeze and within authorization")
	}
	if !plan.ProvisionalPacksOffForIndex || !plan.OneExtractionPackAtATime ||
		!plan.RawArtifactsOutsideRepo {
		return errors.New("privacy and phase-order safety booleans must be true")
	}
	return nil
}

func validateCeilings(ceilings PrivateCeilings) error {
	values := []int64{
		ceilings.TotalWallMS, ceilings.IndexWallMS, ceilings.IndexPeakRSSBytes,
		ceilings.IndexShardBytes, ceilings.RestartWallMS,
		ceilings.SearchColdMaxWallMS, ceilings.SearchWarmMaxWallMS,
		ceilings.CandidateWallMS, ceilings.CandidatePeakSpoolBytes,
		ceilings.ExtractionWallMS, ceilings.RecoveryWallMS,
		ceilings.RetentionWallMS, ceilings.MinimumAvailableDataBytes,
	}
	for _, value := range values {
		if value <= 0 {
			return errors.New("every ceiling must be positive")
		}
	}
	return nil
}

func validateObservation(plan PrivatePlan, observation Observation) error {
	if observation.Schema != ObservationSchema {
		return fmt.Errorf("schema %q is not %q", observation.Schema, ObservationSchema)
	}
	if _, err := time.Parse("2006-01-02", observation.MeasuredOn); err != nil {
		return errors.New("measured_on must be YYYY-MM-DD")
	}
	if observation.PhebsCommit != plan.Artifact.PhebsCommit {
		return errors.New("phebs_commit does not match the frozen artifact")
	}
	if observation.Outcome != "completed" && observation.Outcome != "stopped" {
		return errors.New("outcome must be completed or stopped")
	}
	if observation.TotalWallMS < 0 ||
		(observation.Outcome == "completed" && observation.TotalWallMS > plan.Ceilings.TotalWallMS) {
		return errors.New("total wall measurement violates the completed-run ceiling")
	}
	if observation.CloneFetch.Posture != plan.CloneFetchPosture || observation.CloneFetch.WallMS < 0 ||
		(observation.CloneFetch.Posture == "excluded" && observation.CloneFetch.WallMS != 0) {
		return errors.New("clone/fetch observation does not match the frozen posture")
	}
	if err := validateOutcome(observation.Index.Outcome); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if !nonnegative(
		observation.Index.WallMS, observation.Index.PeakRSSBytes,
		observation.Index.ShardBytes, int64(observation.Index.ShardCount),
	) || !stringIn(observation.Index.Publication, "visible", "unavailable", "not_run") {
		return errors.New("index observation is invalid")
	}
	if observation.Index.Outcome == "succeeded" &&
		(observation.Index.Publication != "visible" || observation.Index.ShardCount <= 0) {
		return errors.New("successful index must have a visible nonempty publication")
	}
	if observation.Outcome == "completed" &&
		(observation.Index.WallMS > plan.Ceilings.IndexWallMS ||
			observation.Index.PeakRSSBytes > plan.Ceilings.IndexPeakRSSBytes ||
			observation.Index.ShardBytes > plan.Ceilings.IndexShardBytes) {
		return errors.New("index measurement violates a completed-run ceiling")
	}
	if err := validateOutcome(observation.Restart.Outcome); err != nil || !nonnegative(
		observation.Restart.WallMS, int64(observation.Restart.IndexChildRuns),
		observation.Restart.ShardBytesDelta, int64(observation.Restart.ShardMTimeChanges),
	) {
		return errors.New("restart observation is invalid")
	}
	if observation.Restart.Outcome == "succeeded" &&
		(!observation.Restart.AlreadyCurrent || observation.Restart.IndexChildRuns != 0 ||
			observation.Restart.ShardBytesDelta != 0 || observation.Restart.ShardMTimeChanges != 0) {
		return errors.New("successful restart must prove a child-free untouched no-op")
	}
	if observation.Outcome == "completed" && observation.Restart.WallMS > plan.Ceilings.RestartWallMS {
		return errors.New("restart measurement violates the completed-run ceiling")
	}
	if err := validateSearch(plan, observation.Search, observation.Outcome); err != nil {
		return err
	}
	if err := validateCandidate(plan, observation.Candidate, observation.Outcome); err != nil {
		return err
	}
	if len(observation.Extractions) != len(plan.ExpectedExtractionDomains) {
		return errors.New("extractions do not match the frozen domain count")
	}
	for index, extraction := range observation.Extractions {
		if extraction.SerialOrdinal != index+1 || extraction.Domain != plan.ExpectedExtractionDomains[index] {
			return errors.New("extractions are not in the frozen one-at-a-time order")
		}
		if err := validateExtraction(plan, extraction, observation.Outcome); err != nil {
			return fmt.Errorf("extraction %q: %w", extraction.Domain, err)
		}
	}
	if err := validateRecovery(plan, observation.Recovery, observation.Outcome); err != nil {
		return err
	}
	if err := validateRetention(plan, observation.Retention, observation.Outcome); err != nil {
		return err
	}
	if len(observation.Failures) > 64 {
		return errors.New("failure census exceeds 64 entries")
	}
	failurePhases := map[string]struct{}{}
	for _, failure := range observation.Failures {
		if !slices.Contains(phaseOrder, failure.Phase) || !slices.Contains(failureClasses, failure.Class) {
			return errors.New("failure census contains an open or invalid class")
		}
		failurePhases[failure.Phase] = struct{}{}
	}
	requiredFailurePhases := map[string]string{
		"index": observation.Index.Outcome, "restart": observation.Restart.Outcome,
		"search": observation.Search.Outcome, "candidate": observation.Candidate.Outcome,
		"recovery": observation.Recovery.Outcome, "retention": observation.Retention.Outcome,
	}
	for _, extraction := range observation.Extractions {
		if extraction.Outcome == "failed" || extraction.Outcome == "stopped" {
			requiredFailurePhases["extraction"] = extraction.Outcome
		}
	}
	for phase, outcome := range requiredFailurePhases {
		if outcome != "failed" && outcome != "stopped" {
			continue
		}
		if _, classified := failurePhases[phase]; !classified {
			return fmt.Errorf("%s outcome %q lacks a fixed failure classification", phase, outcome)
		}
	}
	if observation.Outcome == "stopped" && len(observation.Failures) == 0 {
		return errors.New("stopped run must classify at least one failure")
	}
	if err := validateTeardown(observation.Teardown); err != nil {
		return err
	}
	if observation.Outcome == "completed" {
		if observation.Index.Outcome != "succeeded" || observation.Restart.Outcome != "succeeded" ||
			observation.Search.Outcome != "succeeded" || observation.Candidate.Outcome != "succeeded" ||
			observation.Recovery.Outcome != "succeeded" || observation.Retention.Outcome != "succeeded" {
			return errors.New("completed run has an incomplete required phase")
		}
		for _, extraction := range observation.Extractions {
			if extraction.Outcome != "succeeded" {
				return errors.New("completed run has an incomplete extraction")
			}
			if extraction.Reason != "published_empty" && extraction.Reason != "published_nonempty" {
				return errors.New("completed run extraction did not publish a first-run outcome")
			}
		}
		if observation.Candidate.Decision == "not_ready" {
			return errors.New("completed run candidate planning remained not ready")
		}
	}
	return nil
}

func validateSearch(plan PrivatePlan, search SearchObservation, runOutcome string) error {
	if err := validateOutcome(search.Outcome); err != nil || search.QueryCount != len(plan.QueryBattery) ||
		search.QueryCount <= 0 {
		return errors.New("search observation does not match the frozen battery")
	}
	for _, pass := range []SearchPass{search.Cold, search.Warm} {
		if !nonnegative(int64(pass.Completed), int64(pass.Failed), pass.TotalWallMS, pass.MaxWallMS) ||
			pass.MaxWallMS > pass.TotalWallMS {
			return errors.New("search pass has inconsistent counts or timing")
		}
		if search.Outcome == "not_run" {
			if pass.Completed != 0 || pass.Failed != 0 || pass.TotalWallMS != 0 || pass.MaxWallMS != 0 {
				return errors.New("not-run search pass must contain zero observations")
			}
		} else if pass.Completed+pass.Failed != search.QueryCount {
			return errors.New("search pass does not account for the frozen query battery")
		}
	}
	if search.Outcome == "succeeded" &&
		(search.Cold.Failed != 0 || search.Warm.Failed != 0 || !search.ResultSetsEqual) {
		return errors.New("successful search must complete equal cold/warm result sets")
	}
	if runOutcome == "completed" &&
		(search.Cold.MaxWallMS > plan.Ceilings.SearchColdMaxWallMS ||
			search.Warm.MaxWallMS > plan.Ceilings.SearchWarmMaxWallMS) {
		return errors.New("search measurement violates a completed-run ceiling")
	}
	return nil
}

func validateCandidate(plan PrivatePlan, candidate CandidateObservation, runOutcome string) error {
	if err := validateOutcome(candidate.Outcome); err != nil ||
		!stringIn(candidate.Decision, "warm_noop", "cold_reuse", "marker_recovery", "repair", "rebuild", "not_ready") ||
		!nonnegative(
			candidate.QueueWaitMS, candidate.MirrorLockWaitMS, candidate.Phases.TreeMS,
			candidate.Phases.SpoolingMS, candidate.Phases.ExternalSortMS,
			candidate.Phases.PublishMS, candidate.Phases.FingerprintMS,
			candidate.Phases.DatabaseCommitMS, candidate.Phases.MarkerFinishMS,
			candidate.PeakSpoolBytes, candidate.TotalMS, candidate.DeclaredSourceBytes,
			int64(candidate.RepositoryRecords), int64(candidate.LocalRecords),
			int64(candidate.CallerRecords),
		) {
		return errors.New("candidate observation is invalid")
	}
	if runOutcome == "completed" &&
		(candidate.TotalMS > plan.Ceilings.CandidateWallMS ||
			candidate.PeakSpoolBytes > plan.Ceilings.CandidatePeakSpoolBytes) {
		return errors.New("candidate measurement violates a completed-run ceiling")
	}
	return nil
}

func validateExtraction(plan PrivatePlan, extraction ExtractionObservation, runOutcome string) error {
	if !allowedDomains[extraction.Domain] || !allowedReasons[extraction.Reason] {
		return errors.New("domain or reason is outside the closed vocabulary")
	}
	if err := validateOutcome(extraction.Outcome); err != nil {
		return err
	}
	if !nonnegative(
		extraction.QueueWaitMS, extraction.MirrorLockMS, extraction.PointerWorkMS,
		extraction.StrictOpenMS, extraction.Phases.InventoryMS,
		extraction.Phases.OpenedSourceMS, extraction.Phases.ExtractorMS,
		extraction.Phases.StagingMS, extraction.Phases.PublicationMS,
		extraction.Phases.AbortMS, extraction.Phases.CleanupMS, extraction.TotalMS,
		int64(extraction.CandidateFiles), int64(extraction.OpenedFiles),
		extraction.OpenedBytes, int64(extraction.Facts), int64(extraction.Unresolved),
	) {
		return errors.New("negative extraction scalar")
	}
	if runOutcome == "completed" && extraction.TotalMS > plan.Ceilings.ExtractionWallMS {
		return errors.New("measurement violates the completed-run ceiling")
	}
	return nil
}

func validateRecovery(plan PrivatePlan, recovery RecoveryObservation, runOutcome string) error {
	if err := validateOutcome(recovery.Outcome); err != nil ||
		!stringIn(recovery.InterruptedPhase, "index", "candidate", "extraction") || recovery.WallMS < 0 {
		return errors.New("recovery observation is invalid")
	}
	if recovery.Outcome == "succeeded" && (!recovery.PriorPublicationPreserved || !recovery.EventualCurrent) {
		return errors.New("successful recovery must preserve prior authority and become current")
	}
	if runOutcome == "completed" && recovery.WallMS > plan.Ceilings.RecoveryWallMS {
		return errors.New("recovery measurement violates the completed-run ceiling")
	}
	return nil
}

func validateRetention(plan PrivatePlan, retention RetentionObservation, runOutcome string) error {
	if err := validateOutcome(retention.Outcome); err != nil || retention.WallMS < 0 ||
		retention.ComponentsObserved != 52 || retention.UnchangedComponents < 0 ||
		retention.UnchangedComponents+len(retention.Changed) != retention.ComponentsObserved {
		return errors.New("retention observation does not account for all 52 components")
	}
	seen := map[string]struct{}{}
	for _, delta := range retention.Changed {
		if !allowedRetentionComponents[delta.Component] {
			return fmt.Errorf("unknown retention component %q", delta.Component)
		}
		if _, duplicate := seen[delta.Component]; duplicate {
			return fmt.Errorf("duplicate retention component %q", delta.Component)
		}
		seen[delta.Component] = struct{}{}
		if err := validateMetric(delta.CountBefore); err != nil {
			return err
		}
		if err := validateMetric(delta.CountAfter); err != nil {
			return err
		}
		byteKinds := map[string]struct{}{}
		for _, bytes := range delta.Bytes {
			if !allowedByteKinds[bytes.Kind] {
				return fmt.Errorf("unknown byte kind %q", bytes.Kind)
			}
			if _, duplicate := byteKinds[bytes.Kind]; duplicate {
				return fmt.Errorf("duplicate byte kind %q", bytes.Kind)
			}
			byteKinds[bytes.Kind] = struct{}{}
			if err := validateMetric(bytes.Before); err != nil {
				return err
			}
			if err := validateMetric(bytes.After); err != nil {
				return err
			}
			if bytes.Kind == "physical_database" &&
				(bytes.Before.Completeness != "unavailable" || bytes.After.Completeness != "unavailable") {
				return errors.New("physical database attribution must remain unavailable")
			}
		}
		changed := !reflect.DeepEqual(delta.CountBefore, delta.CountAfter)
		for _, bytes := range delta.Bytes {
			changed = changed || !reflect.DeepEqual(bytes.Before, bytes.After)
		}
		if !changed {
			return fmt.Errorf("component %q is listed without a delta", delta.Component)
		}
	}
	for _, volume := range []RetentionDataVolume{retention.DataVolumeBefore, retention.DataVolumeAfter} {
		if err := validateMetric(volume.TotalBytes); err != nil {
			return err
		}
		if err := validateMetric(volume.AvailableBytes); err != nil {
			return err
		}
	}
	if runOutcome == "completed" && retention.WallMS > plan.Ceilings.RetentionWallMS {
		return errors.New("retention measurement violates the completed-run ceiling")
	}
	if runOutcome == "completed" {
		available := retention.DataVolumeAfter.AvailableBytes
		if available.Completeness != "exact" || available.Value == nil ||
			*available.Value < plan.Ceilings.MinimumAvailableDataBytes {
			return errors.New("completed run lacks the frozen minimum exact available capacity")
		}
	}
	return nil
}

func validateMetric(metric RetentionMetric) error {
	if !allowedCompleteness[metric.Completeness] {
		return fmt.Errorf("unknown completeness %q", metric.Completeness)
	}
	if metric.Completeness == "unavailable" {
		if metric.Value != nil {
			return errors.New("unavailable metric must omit value")
		}
		return nil
	}
	if metric.Value == nil || *metric.Value < 0 {
		return errors.New("available metric must carry a nonnegative value")
	}
	return nil
}

func validateTeardown(teardown TeardownObservation) error {
	if teardown.WallMS < 0 || !stringIn(teardown.Outcome, "destroyed", "retained_under_authorization", "failed") {
		return errors.New("teardown observation is invalid")
	}
	if teardown.Outcome == "destroyed" && (teardown.SourceCopyRetained || teardown.DerivedDataRetained) {
		return errors.New("destroyed teardown cannot retain source or derived data")
	}
	if teardown.Outcome == "failed" {
		return errors.New("failed teardown cannot produce a checked-in receipt")
	}
	return nil
}

func validateOutcome(outcome string) error {
	if !stringIn(outcome, "succeeded", "failed", "stopped", "not_run") {
		return fmt.Errorf("unknown outcome %q", outcome)
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

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func stringIn(value string, allowed ...string) bool {
	return slices.Contains(allowed, value)
}

func nonnegative(values ...int64) bool {
	for _, value := range values {
		if value < 0 {
			return false
		}
	}
	return true
}

func isLowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isSHA256(value string) bool {
	return strings.HasPrefix(value, "sha256:") && isLowerHex(strings.TrimPrefix(value, "sha256:"), 64)
}

func isDiverseNonce(value string) bool {
	if !isLowerHex(value, 64) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return false
	}
	unique := map[byte]struct{}{}
	for _, current := range decoded {
		unique[current] = struct{}{}
	}
	return len(unique) >= 16
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	copyGot, copyWant := slices.Clone(got), slices.Clone(want)
	slices.Sort(copyGot)
	slices.Sort(copyWant)
	return slices.Equal(copyGot, copyWant)
}

func containsStrings(got, required []string) bool {
	set := make(map[string]struct{}, len(got))
	for _, value := range got {
		set[value] = struct{}{}
	}
	for _, value := range required {
		if _, present := set[value]; !present {
			return false
		}
	}
	return true
}

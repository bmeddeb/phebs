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
)

const (
	PlanSchema          = "t4013-neutral-convergence-plan-v1"
	ObservationSchema   = "t4013-neutral-convergence-observation-v1"
	ReceiptSchema       = "t4013-neutral-convergence-receipt-v1"
	MaxPlanBytes        = 64 << 10
	MaxObservationBytes = 256 << 10
	MaxReceiptBytes     = 256 << 10
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
	"environment", "admission", "search", "pipeline", "recovery", "lifecycle", "archive_restore", "authorization", "oracle",
}

type Plan struct {
	Schema       string         `json:"schema"`
	FrozenOn     string         `json:"frozen_on"`
	SourceCommit string         `json:"source_commit"`
	Inputs       []InputBinding `json:"inputs"`
	PhaseOrder   []string       `json:"phase_order"`
	Safety       SafetyEnvelope `json:"safety_envelope"`
	StopRules    []StopRule     `json:"stop_rules"`
	Claims       PlanClaims     `json:"claims"`
}

type InputBinding struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
	SHA256 string `json:"sha256"`
}

type SafetyEnvelope struct {
	MinimumMemoryBytes        int64 `json:"minimum_memory_bytes"`
	MinimumAvailableDiskBytes int64 `json:"minimum_available_disk_bytes"`
	MaximumTotalWallMS        int64 `json:"maximum_total_wall_ms"`
	MaximumPeakRSSBytes       int64 `json:"maximum_peak_rss_bytes"`
	MaximumDataAllocatedBytes int64 `json:"maximum_data_allocated_bytes"`
	MaximumRetriesPerUnit     int   `json:"maximum_retries_per_unit"`
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
	Schema      string                    `json:"schema"`
	MeasuredOn  string                    `json:"measured_on"`
	Outcome     string                    `json:"outcome"`
	Environment EnvironmentObservation    `json:"environment"`
	Profiles    []ProfileObservation      `json:"profiles"`
	BlobReaders []BlobReaderObservation   `json:"blob_readers"`
	Service     ServiceControlObservation `json:"service_control"`
	Explicit    ExplicitStateObservation  `json:"explicit_states"`
	Phases      []PhaseObservation        `json:"phases"`
	Checks      []CheckObservation        `json:"checks"`
	Failures    []FailureObservation      `json:"failures"`
	Decision    DecisionObservation       `json:"decision"`
	Teardown    TeardownObservation       `json:"teardown"`
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
	Selected string `json:"selected"`
	Reason   string `json:"reason"`
}

type TeardownObservation struct {
	Completed             bool `json:"completed"`
	DerivedDataRetained   bool `json:"derived_data_retained"`
	ScratchSourceRetained bool `json:"scratch_source_retained"`
}

type Receipt struct {
	Schema       string                    `json:"schema"`
	PlanDigest   string                    `json:"plan_digest"`
	SourceCommit string                    `json:"source_commit"`
	MeasuredOn   string                    `json:"measured_on"`
	Outcome      string                    `json:"outcome"`
	Environment  EnvironmentObservation    `json:"environment"`
	Profiles     []ProfileObservation      `json:"profiles"`
	BlobReaders  []BlobReaderObservation   `json:"blob_readers"`
	Service      ServiceControlObservation `json:"service_control"`
	Explicit     ExplicitStateObservation  `json:"explicit_states"`
	Phases       []PhaseObservation        `json:"phases"`
	Checks       []CheckObservation        `json:"checks"`
	Failures     []FailureObservation      `json:"failures"`
	Decision     DecisionObservation       `json:"decision"`
	Teardown     TeardownObservation       `json:"teardown"`
	Claims       ReceiptClaims             `json:"claims"`
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
		Schema: ReceiptSchema, PlanDigest: planDigest, SourceCommit: plan.SourceCommit,
		MeasuredOn: observation.MeasuredOn, Outcome: observation.Outcome,
		Environment: observation.Environment, Profiles: observation.Profiles,
		BlobReaders: observation.BlobReaders, Service: observation.Service,
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
	if value.Schema != PlanSchema || !date(value.FrozenOn) || !hexIdentity(value.SourceCommit, 40) ||
		!slices.Equal(value.PhaseOrder, phaseOrder) || len(value.Inputs) != 4 || len(value.StopRules) != 4 {
		return errors.New("T40.13 plan identity or fixed inventory is invalid")
	}
	if !slices.Equal(value.Inputs, expectedInputs) || !slices.Equal(value.StopRules, frozenStopRules) {
		return errors.New("T40.13 frozen inputs or stop rules changed")
	}
	if value.Safety != frozenSafety {
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
		safety.MaximumDataAllocatedBytes <= 0 || safety.MaximumRetriesPerUnit < 1 || safety.MaximumRetriesPerUnit > 5 {
		return errors.New("T40.13 safety envelope is invalid")
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
	if value.Schema != ObservationSchema || !date(value.MeasuredOn) ||
		(value.Outcome != "completed" && value.Outcome != "stopped") {
		return errors.New("T40.13 observation identity is invalid")
	}
	if value.Environment.OS == "" || value.Environment.Arch == "" || value.Environment.MemoryBytes <= 0 ||
		value.Environment.FilesystemTotalBytes <= 0 || value.Environment.FilesystemAvailableBytes < 0 ||
		value.Environment.FilesystemAvailableBytes > value.Environment.FilesystemTotalBytes ||
		value.Environment.InitialUsedPercent < 0 || value.Environment.InitialUsedPercent > 100 {
		return errors.New("T40.13 environment observation is invalid")
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
	if !slices.Contains([]string{"continue", "reduce", "cohort_experiment", "p6_investigation"}, value.Decision.Selected) ||
		value.Decision.Reason == "" || len(value.Decision.Reason) > 256 {
		return errors.New("T40.13 decision observation is invalid")
	}
	return nil
}

func ValidateReceipt(value Receipt, plan Plan) error {
	if value.Schema != ReceiptSchema || !digestIdentity(value.PlanDigest) || value.SourceCommit != plan.SourceCommit ||
		!date(value.MeasuredOn) || (value.Outcome != "completed" && value.Outcome != "stopped") {
		return errors.New("T40.13 receipt identity is invalid")
	}
	observation := Observation{
		Schema: ObservationSchema, MeasuredOn: value.MeasuredOn, Outcome: value.Outcome,
		Environment: value.Environment, Profiles: value.Profiles, BlobReaders: value.BlobReaders,
		Service: value.Service, Explicit: value.Explicit, Phases: value.Phases, Checks: value.Checks,
		Failures: value.Failures, Decision: value.Decision, Teardown: value.Teardown,
	}
	if err := ValidateObservation(observation); err != nil {
		return err
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
	} else if len(value.Failures) == 0 || value.Decision.Selected == "continue" || !value.Teardown.Completed ||
		value.Teardown.DerivedDataRetained || value.Teardown.ScratchSourceRetained {
		return errors.New("stopped T40.13 receipt lacks a failure, stop decision, or teardown")
	}
	claims := value.Claims
	if !claims.MechanicsEvidenceOnly || claims.EstablishesTargetSLO || claims.EstablishesServiceScale ||
		claims.EstablishesAccuracy || claims.EstablishesCompleteness || claims.AuthorizesRelease ||
		claims.AuthorizesPrivateRerun || claims.EstablishesMigration || claims.EstablishesDecommissioning {
		return errors.New("T40.13 receipt claims are invalid")
	}
	return nil
}

func DecodeReceipt(raw []byte, plan Plan) (Receipt, error) {
	if len(raw) == 0 || len(raw) > MaxReceiptBytes {
		return Receipt{}, errors.New("T40.13 receipt is outside its fixed byte bound")
	}
	var value Receipt
	if err := decodeStrict(raw, &value); err != nil {
		return Receipt{}, fmt.Errorf("decode T40.13 receipt: %w", err)
	}
	if err := ValidateReceipt(value, plan); err != nil {
		return Receipt{}, err
	}
	return value, nil
}

func validateCompleted(value Receipt, plan Plan) error {
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

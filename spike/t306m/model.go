// Package t306m retains the neutral storage model used by the T30.6m
// historical-publication retention decision. It describes current ownership
// and the neutral status-summary envelope; it does not authorize deletion.
package t306m

import (
	"math"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	Schema = "t306m-historical-retention-decision-v3"

	SelectedPosture                   = "unbounded"
	WarningCode                       = "unbounded_historical_publication_retention"
	ProofBundlePositiveLifetimeEffect = "deletes the expired bundle and exactly its proof-bundle:<bundle_id> evidence pins but no extraction evidence; the independent evidence sweep may later reclaim newly unpinned superseded evidence when otherwise eligible"

	RelationSelected   = "selected_t306m_unbounded_retention"
	RelationExisting   = "existing_owner_lifecycle_unchanged"
	RelationConfigured = "configured_owner_lifecycle_unchanged"
	RelationMixed      = "mixed_owner_lifecycles_not_selected_by_t306m"
	RelationIncidental = "incidental_growth_requires_separate_decision"

	// StatusReportedIdentityLimit is the maximum count represented exactly.
	// StatusScanIdentityLimit includes one sentinel identity so collectors
	// added after T30.6o can distinguish exact-at-cap from a labeled lower
	// bound without scanning on.
	StatusReportedIdentityLimit               = 4_096
	StatusScanIdentityLimit                   = StatusReportedIdentityLimit + 1
	StatusResponseByteLimit                   = 64 << 10
	StatusComponentCount                      = 52
	StatusAggregateReportedIdentityAllocation = StatusReportedIdentityLimit
	StatusAggregateScanIdentityAllocation     = StatusAggregateReportedIdentityAllocation + StatusComponentCount

	// EvidenceAdmissionRows is the exact association-plus-assertion ceiling
	// retained by the T20 current-writer receipt. The T30.6m gate checks this
	// value against that immutable receipt rather than importing store-private
	// implementation constants.
	EvidenceAdmissionRows = 25_000
)

type Definition struct {
	Term    string `json:"term"`
	Meaning string `json:"meaning"`
}

type Owner struct {
	Name               string          `json:"name"`
	Scope              string          `json:"scope"`
	Components         []string        `json:"components"`
	DecisionRelation   string          `json:"decision_relation"`
	Retention          string          `json:"retention"`
	Protection         string          `json:"protection"`
	BackupRestore      string          `json:"backup_restore"`
	CountMetric        string          `json:"count_metric"`
	ByteMetric         string          `json:"byte_metric"`
	Bounds             []CapacityBound `json:"bounds,omitempty"`
	Accumulating       bool            `json:"accumulating"`
	DeletionAuthorized bool            `json:"deletion_authorized"`
}

type CapacityBound struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
	Unit  string `json:"unit"`
	Kind  string `json:"kind"`
}

type GrowthPoint struct {
	HistoricalTransitions                  int   `json:"historical_transitions"`
	EvidenceAdmissionRowsPerDomain         int64 `json:"evidence_admission_rows_per_domain"`
	CandidateFailedTransitionResidueSets   int64 `json:"candidate_failed_transition_residue_sets"`
	CallerIncompleteGenerationSuccessBytes int64 `json:"caller_incomplete_generation_success_bytes"`
}

type DurableJobGrowthPoint struct {
	Repositories                       int   `json:"repositories"`
	ResyncsPerCommonYear               int64 `json:"resyncs_per_common_year"`
	IndexingJobRowsPerCommonYear       int64 `json:"indexing_job_rows_per_common_year"`
	ConnectionSyncJobRowsPerCommonYear int64 `json:"connection_sync_job_rows_per_common_year"`
}

type DurableJobGrowthReceipt struct {
	DefaultResyncInterval string                  `json:"default_resync_interval"`
	Assumptions           []string                `json:"assumptions"`
	ExcludedClaims        []string                `json:"excluded_claims"`
	Points                []DurableJobGrowthPoint `json:"points"`
}

type StatusMetric struct {
	Value        *int64 `json:"value"`
	Unit         string `json:"unit"`
	Completeness string `json:"completeness"`
}

type StatusSummary struct {
	ScannedIdentities int          `json:"scanned_identities"`
	Count             StatusMetric `json:"count"`
	Bytes             StatusMetric `json:"bytes"`
	Truncated         bool         `json:"truncated"`
	WarningCode       string       `json:"warning_code"`
}

type StatusRetentionControl struct {
	ConfigKey              string `json:"config_key"`
	DefaultState           string `json:"default_state"`
	PositiveLifetimeEffect string `json:"positive_lifetime_effect"`
}

type StatusContract struct {
	Endpoint                            string                 `json:"endpoint"`
	Authorization                       string                 `json:"authorization"`
	InventoryScope                      string                 `json:"inventory_scope"`
	ReportedIdentityLimitPerSummary     int                    `json:"reported_identity_limit_per_summary"`
	ScanIdentityLimitPerSummary         int                    `json:"scan_identity_limit_per_summary"`
	ComponentCoverage                   string                 `json:"component_coverage"`
	ComponentScanAllocation             string                 `json:"component_scan_allocation"`
	AggregateReportedIdentityAllocation int                    `json:"aggregate_reported_identity_allocation"`
	AggregateScanIdentityAllocation     int                    `json:"aggregate_scan_identity_allocation"`
	EncodedResponseByteLimit            int                    `json:"encoded_response_byte_limit"`
	CompletenessVocabulary              []string               `json:"completeness_vocabulary"`
	PhysicalDatabaseBytes               StatusMetric           `json:"physical_database_bytes"`
	WarningCode                         string                 `json:"warning_code"`
	WarningHeader                       string                 `json:"warning_header"`
	ByteMetricKinds                     []string               `json:"byte_metric_kinds"`
	ByteMetricCombinationRule           string                 `json:"byte_metric_combination_rule"`
	ProofBundleRetentionControl         StatusRetentionControl `json:"proof_bundle_retention_control"`
	EmptySample                         StatusSummary          `json:"empty_sample"`
	CapPlusOneSample                    StatusSummary          `json:"cap_plus_one_sample"`
	InvalidByteMetricSample             StatusSummary          `json:"invalid_byte_metric_sample"`
}

type ImplementationTicket struct {
	Ticket    string   `json:"ticket"`
	Purpose   string   `json:"purpose"`
	Changes   []string `json:"changes"`
	Excluded  []string `json:"excluded"`
	FitsOnePR bool     `json:"fits_one_pr"`
	SizeProof []string `json:"size_proof"`
}

type ImplementationProof struct {
	Tickets             []ImplementationTicket `json:"tickets"`
	FitsOnePR           bool                   `json:"fits_one_pr"`
	DownstreamGate      string                 `json:"downstream_gate"`
	FutureBoundedPolicy string                 `json:"future_bounded_policy"`
}

type Results struct {
	Schema           string                  `json:"schema"`
	Decision         string                  `json:"decision"`
	CleanupChanged   bool                    `json:"cleanup_changed"`
	Reason           string                  `json:"reason"`
	EscapeHatch      []string                `json:"escape_hatch"`
	Definitions      []Definition            `json:"definitions"`
	Owners           []Owner                 `json:"owners"`
	Growth           []GrowthPoint           `json:"growth"`
	DurableJobGrowth DurableJobGrowthReceipt `json:"durable_job_growth"`
	Status           StatusContract          `json:"status_contract"`
	Implementation   ImplementationProof     `json:"implementation_proof"`
}

// SummarizeStatus models one fixed-work summary after component collection.
// T30.6o still owns the fixed-work allocation across a multi-component owner.
// Values are already owner-defined logical or apparent bytes; the model never
// labels them as physical SurrealDB allocation.
func SummarizeStatus(values []int64) StatusSummary {
	scanned := len(values)
	if scanned > StatusScanIdentityLimit {
		scanned = StatusScanIdentityLimit
	}
	reported := scanned
	truncated := reported > StatusReportedIdentityLimit
	if truncated {
		reported = StatusReportedIdentityLimit
	}
	countCompleteness := "exact"
	if truncated {
		countCompleteness = "lower_bound"
	}
	count := metric(int64(reported), "identities", countCompleteness)

	byteCompleteness := countCompleteness
	var bytes int64
	validBytes := true
	for _, value := range values[:reported] {
		if value < 0 || value > math.MaxInt64-bytes {
			validBytes = false
			break
		}
		bytes += value
	}
	byteMetric := metric(bytes, "bytes", byteCompleteness)
	if !validBytes {
		byteMetric = unavailableMetric("bytes")
	}
	return StatusSummary{
		ScannedIdentities: scanned,
		Count:             count,
		Bytes:             byteMetric,
		Truncated:         truncated,
		WarningCode:       WarningCode,
	}
}

func metric(value int64, unit, completeness string) StatusMetric {
	owned := value
	return StatusMetric{Value: &owned, Unit: unit, Completeness: completeness}
}

func unavailableMetric(unit string) StatusMetric {
	return StatusMetric{Unit: unit, Completeness: "unavailable"}
}

func RetainedResults() Results {
	capPlusOne := make([]int64, StatusScanIdentityLimit)
	for index := range capPlusOne {
		capPlusOne[index] = 1 << 20
	}

	return Results{
		Schema:         Schema,
		Decision:       SelectedPosture,
		CleanupChanged: false,
		Reason:         "No measured rollback depth justifies destructive default eviction of historical publications, and a generation count would not bound their physical bytes across durable evidence pins, caller leases, shared atoms, and independent database/filesystem owners. The adjacent proof-bundle owner remains under its existing configured lifecycle; this selection does not override it or justify unbounded job or Investigation/Workbench growth. Any new lifecycle or retention policy requires a separate decision.",
		EscapeHatch: []string{
			"monitor and expand or relocate server.data_dir",
			"take a verified backup before a supported repository removal",
			"supported repository removal reclaims derived files and makes non-quarantined unpinned evidence sweepable; pins and retention-quarantined evidence remain",
			"retention-quarantined evidence has no supported deletion procedure in this release and requires a separately reviewed administrator resolution",
		},
		Definitions: []Definition{
			{Term: "historical_evidence_scope", Meaning: "a published exact repository/commit/unit/domain scope that is not the live current scope"},
			{Term: "durable_pin", Meaning: "any store-accepted evidence_pin retention owner, including proof, checkpoint, and Investigation state, restored with the database"},
			{Term: "active_lease", Meaning: "a process-local caller-publication lifetime guard, not a durable history pin"},
			{Term: "backup_snapshot", Meaning: "an external immutable copy that does not pin live installation state"},
			{Term: "current", Meaning: "state satisfying the complete live repository, unit, policy, pointer, and publication fences, not merely the newest timestamp"},
			{Term: "decision_relation", Meaning: "whether an owner is selected by the T30.6m unbounded-retention decision, has an unchanged existing/configured lifecycle, or is incidental growth requiring a separate decision"},
		},
		Owners: []Owner{
			{Name: "evidence_publications", Scope: "repository/commit/unit/domain", Components: []string{"extraction_run", "snapshot_evidence", "assertion", "evidence_atom"}, DecisionRelation: RelationSelected, Retention: "historical published and pinned superseded runs are unbounded; quarantined runs require administrator resolution; sweep-eligible backlog remains until bounded maintenance drains it", Protection: "live current scope, every durable evidence pin, and retention quarantine", BackupRestore: "database export and restore retain runs, associations, assertions, atoms, and pins", CountMetric: "all run statuses plus association, assertion, and distinct shared atom rows, partitioned as current/historical published, pinned, quarantined, and sweep backlog", ByteMetric: "unavailable for physical database attribution", Accumulating: true},
			{Name: "extraction_attempts", Scope: "repository/commit/unit/domain", Components: []string{"extraction_attempt"}, DecisionRelation: RelationSelected, Retention: "exact-scope attempts are unbounded", Protection: "attempt history for exact extraction scopes", BackupRestore: "database export and restore retain attempts", CountMetric: "attempt rows", ByteMetric: "unavailable for physical database attribution", Accumulating: true},
			{Name: "extraction_outcomes", Scope: "repository/domain", Components: []string{"extraction_domain_outcome"}, DecisionRelation: RelationExisting, Retention: "latest-only per repository/domain", Protection: "latest live-scope failure diagnostic", BackupRestore: "database export imports outcomes; restore selectively clears candidate-control outcomes with candidate authority", CountMetric: "outcome rows by disposition", ByteMetric: "logical encoded receipt bytes; physical database attribution unavailable", Bounds: []CapacityBound{
				{Name: "receipt_bytes_per_outcome", Value: store.MaxExtractionOutcomeReceiptBytes, Unit: "bytes", Kind: "enforced"},
			}, Accumulating: false},
			{Name: "evidence_pins", Scope: "run/kind", Components: []string{"evidence_pin[kind=proof-bundle:<bundle_id>]", "evidence_pin[kind=investigation-artifact:<artifact_id>]", "evidence_pin[kind=<other exact store-accepted value>]"}, DecisionRelation: RelationMixed, Retention: "pin rows follow their owning existing lifecycle rather than an intrinsic global policy; proof pins accumulate while proof retention is disabled and a positive proof TTL releases only its exact namespace", Protection: "proof, checkpoint, Investigation, and any other store-accepted owner lifecycle", BackupRestore: "database export and restore retain every pin kind", CountMetric: "pin rows and distinct pinned runs partitioned into proof-bundle, investigation-artifact, and other exact kind namespaces", ByteMetric: "unavailable for physical database attribution", Accumulating: true},
			{Name: "proof_bundles", Scope: "immutable bundle", Components: []string{"proof_bundle"}, DecisionRelation: RelationConfigured, Retention: "proof_bundles.retention defaults to disabled (empty or 0), so bundles accumulate; a positive TTL expires from latest successful materialization through the existing sweeper, which deletes the expired bundle and exactly its proof-bundle:<bundle_id> evidence pins but no extraction evidence; the independent evidence sweep may later reclaim newly unpinned superseded evidence when otherwise eligible", Protection: "immutable bundle identity until its configured lifecycle expires it", BackupRestore: "database export and restore retain bundles and their pins", CountMetric: "bundle rows plus referenced-run counts; exact proof pin rows remain reported by the evidence_pins owner", ByteMetric: "logical canonical content bytes; physical database attribution unavailable", Accumulating: true},
			{Name: "durable_job_history", Scope: "queue-table/target/auto-id", Components: []string{"connection_sync_job", "indexing_job", "repo_fetch_job", "candidate_manifest_job", "extraction_job", "resolver_catalog_job", "caller_leaf_job", "investigation_run_job"}, DecisionRelation: RelationIncidental, Retention: "pending coalescing bounds only one pending target slot; claim releases that slot, and done, failed, and canceled rows have no deletion or pruning path", Protection: "pending and active queue correctness; terminal history has no separately selected retention policy", BackupRestore: "database export and restore retain job rows; startup legacy reconciliation currently reads all eight tables", CountMetric: "rows partitioned by exact table and status", ByteMetric: "unavailable for physical database attribution", Accumulating: true},
			{Name: "investigation_workbench_rows", Scope: "investigation/revision/run/artifact/review/watch", Components: []string{"investigation", "investigation_revision", "investigation_change_brief", "investigation_workbench_mutation", "investigation_workbench_disposition", "investigation_run", "investigation_run_event", "investigation_run_artifact", "investigation_artifact_owner", "investigation_artifact_owner_release", "investigation_artifact_retention_override", "investigation_decision", "investigation_disposition", "investigation_baseline_designation", "investigation_grant", "investigation_cursor", "investigation_creation", "investigation_consumer_snapshot", "investigation_consumer_edge_ledger", "investigation_review_projection", "investigation_review_item", "investigation_dossier", "investigation_watch", "investigation_watch_revision"}, DecisionRelation: RelationIncidental, Retention: "semantic history and current projections have no aggregate or runtime-wired sweep; the package-local single-artifact sweep has no production caller and does not bound the other component tables", Protection: "Investigation, Workbench, artifact-owner, review, dossier, authorization, and watch lifecycles", BackupRestore: "database export and restore retain all component rows", CountMetric: "rows partitioned by all 24 exact component tables and owner-specific lifecycle state; investigation_run_job is reported only under durable_job_history", ByteMetric: "unavailable for physical database attribution", Accumulating: true},
			{Name: "candidate_artifacts", Scope: "repository/generation", Components: []string{"candidate_manifest_publication", "$DATA/candidates managed publication files"}, DecisionRelation: RelationSelected, Retention: "one current authority; failed partial generation residue can accumulate until a later successful cleanup or repository removal", Protection: "current store pointer; incomplete residue has no authority", BackupRestore: "excluded and rebuilt", CountMetric: "current and noncurrent managed manifest/member identities", ByteMetric: "apparent file bytes", Bounds: []CapacityBound{
				{Name: "local_projection_artifacts_per_valid_publication", Value: int64(candidate.MaxLocalProjectionArtifacts), Unit: "identities", Kind: "enforced"},
				{Name: "local_projection_content_bytes_per_valid_publication", Value: candidate.MaxLocalProjectionContentBytes, Unit: "bytes", Kind: "enforced"},
			}, Accumulating: true},
			{Name: "focused_indexes", Scope: "repository", Components: []string{"repo indexed analysis-unit/revision state", "$DATA/index focused publication files"}, DecisionRelation: RelationExisting, Retention: "current and reader transition only", Protection: "current manifest and process-local reader references", BackupRestore: "validated marker-free physical focused publications may be archived; restore re-fences authority and whole indexes otherwise rebuild", CountMetric: "current manifest/member identities", ByteMetric: "apparent file bytes", Accumulating: false},
			{Name: "resolver_catalogs", Scope: "installation/repository", Components: []string{"resolver_catalog_publication", "$DATA/resolver-catalogs package-owned files"}, DecisionRelation: RelationExisting, Retention: "current replacement transition plus package-owned stages and residue; lifecycle inventories refuse at their operational scan bound", Protection: "current store pointer", BackupRestore: "validated marker-free physical publications may be archived, then authority is cleared and re-fenced", CountMetric: "all package-owned installation-root entries and apparent bytes, with per-repository current manifest/member identities reported separately", ByteMetric: "current canonical member bytes and all package-owned apparent file bytes reported separately", Bounds: []CapacityBound{
				{Name: "member_count_per_publication", Value: int64(resolvercatalog.MaxMembers), Unit: "identities", Kind: "enforced"},
				{Name: "canonical_bytes_per_publication", Value: resolvercatalog.MaxCatalogContentBytes, Unit: "bytes", Kind: "enforced"},
				{Name: "catalog_root_inventory_scan_entries", Value: int64(resolvercatalog.MaxDirectoryEntries), Unit: "identities", Kind: "enforced_operational"},
				{Name: "modeled_clean_replacement_disk_bytes", Value: resolvercatalog.MaxPublicationDiskBytes, Unit: "bytes", Kind: "design"},
			}, Accumulating: false},
			{Name: "caller_rows", Scope: "repository/generation/pair", Components: []string{"caller_generation_publication", "caller_generation_admission", "caller_leaf_outcome"}, DecisionRelation: RelationSelected, Retention: "admissions and outcomes are unbounded across generations", Protection: "current generation and latest live-generation terminal diagnostics", BackupRestore: "database export contains rows, but restore deliberately clears caller pointers, admissions, and outcomes", CountMetric: "generation publications, admissions, and pair outcomes", ByteMetric: "canonical bytes from successful receipts where available", Accumulating: true},
			{Name: "caller_artifacts", Scope: "repository/generation/pair", Components: []string{"$DATA/caller-leaves managed manifests and leaf artifacts"}, DecisionRelation: RelationSelected, Retention: "successful incomplete-generation residue is unbounded until directory capacity refuses new work", Protection: "current complete publication and active process-local leases", BackupRestore: "only validated unambiguous marker-free complete bytes may be archived; historical coverage is not promised, incomplete residue is omitted, and restore clears authority", CountMetric: "managed manifests and leaf artifacts", ByteMetric: "canonical receipt bytes and apparent file bytes", Bounds: []CapacityBound{
				{Name: "repository_directory_entries", Value: int64(callerleaf.MaxRepositoryDirectoryEntries), Unit: "identities", Kind: "enforced"},
				{Name: "canonical_bytes_per_admitted_complete_generation", Value: callerleaf.MaxAggregateCanonicalBytes, Unit: "bytes", Kind: "enforced"},
				{Name: "incomplete_cap_plus_one_success_bytes", Value: callerleaf.MaxAggregateCanonicalBytes + callerleaf.MaxCanonicalBytesPerPair, Unit: "bytes", Kind: "derived"},
			}, Accumulating: true},
		},
		Growth: []GrowthPoint{
			growthPoint(1),
			growthPoint(10),
			growthPoint(100),
		},
		DurableJobGrowth: DurableJobGrowthReceipt{
			DefaultResyncInterval: (config.Sync{}).ResyncEvery().String(),
			Assumptions: []string{
				"365-day common year",
				"default sync.resync_interval",
				"one healthy continuously draining remote connection",
				"each resync tick produces one connection_sync_job row and one indexing_job row per repository whether or not repository content changed",
				"each modeled job reaches a terminal state before the next tick, so pending coalescing does not suppress the next row",
			},
			ExcludedClaims: []string{
				"no time-to-degradation or capacity-exhaustion estimate",
				"no aggregate downstream rate for repo_fetch_job, candidate_manifest_job, extraction_job, resolver_catalog_job, caller_leaf_job, or investigation_run_job",
			},
			Points: []DurableJobGrowthPoint{
				durableJobGrowthPoint(1),
				durableJobGrowthPoint(10),
				durableJobGrowthPoint(100),
			},
		},
		Status: StatusContract{
			Endpoint:                            "GET /api/retention-status",
			Authorization:                       "administrator only",
			InventoryScope:                      "the twelve declared groups cover T30.6 publication and adjacent persisted domains, not every database table; audit, analytics, authentication, and other installation state retain their separately documented lifecycles",
			ReportedIdentityLimitPerSummary:     StatusReportedIdentityLimit,
			ScanIdentityLimitPerSummary:         StatusScanIdentityLimit,
			ComponentCoverage:                   "every declared owner component; evidence_pin rows additionally partition into proof-bundle, investigation-artifact, and other exact kind namespaces",
			ComponentScanAllocation:             "one non-transferable fair share of the endpoint's 4,096 aggregate reported-identity allocation plus one private sentinel per component; the first 40 components reserve 79 reported plus one sentinel identity and the remaining 12 reserve 78 plus one, totaling 4,148 scan identities; T30.6o performs zero inventory scans",
			AggregateReportedIdentityAllocation: StatusAggregateReportedIdentityAllocation,
			AggregateScanIdentityAllocation:     StatusAggregateScanIdentityAllocation,
			EncodedResponseByteLimit:            StatusResponseByteLimit,
			CompletenessVocabulary:              []string{"exact", "lower_bound", "unavailable"},
			PhysicalDatabaseBytes:               unavailableMetric("bytes"),
			WarningCode:                         WarningCode,
			WarningHeader:                       "X-Phebs-Warning-Code",
			ByteMetricKinds: []string{
				"logical_encoded",
				"canonical_content",
				"canonical_receipt",
				"apparent_file",
				"physical_database",
			},
			ByteMetricCombinationRule: "ordered metrics are independent and non-combinable across kinds; never sum them",
			ProofBundleRetentionControl: StatusRetentionControl{
				ConfigKey:              "proof_bundles.retention",
				DefaultState:           "disabled",
				PositiveLifetimeEffect: ProofBundlePositiveLifetimeEffect,
			},
			EmptySample:             SummarizeStatus(nil),
			CapPlusOneSample:        SummarizeStatus(capPlusOne),
			InvalidByteMetricSample: SummarizeStatus([]int64{-1}),
		},
		Implementation: ImplementationProof{
			Tickets: []ImplementationTicket{
				{
					Ticket:  "T30.6n",
					Purpose: "bounded durable-job read paths and startup legacy-migration repair",
					Changes: []string{
						"replace RepoStatuses lifetime indexing-job materialization with bounded latest-per-live-repository work",
						"stop both the first upgraded store Open and steady-state Open from loading and sorting terminal history across all eight job tables",
						"permit schema or latest-projection work needed for bounded reads, with bounded resumable reconstruction of legacy latest state",
						"report legacy latest state as explicitly partial or unavailable until bounded resumable reconstruction completes",
						"cover terminal-history growth while preserving pending, active, retry, successor, and repository-removal behavior",
					},
					Excluded: []string{
						"job-row deletion or retention policy",
						"cross-owner status or warning",
						"first-open full-history index build or backfill",
					},
					FitsOnePR: true,
					SizeProof: []string{
						"one queue subsystem",
						"two lifetime-history consumers",
						"one active-row migration fence",
						"table-driven coverage across all eight job kinds",
					},
				},
				{
					Ticket:  "T30.6o",
					Purpose: "authorization-first retention-status shell, fixed registry and budgets, and unconditional warning",
					Changes: []string{
						"fixed registry and scan budgets for all twelve owners and fifty-two declared components",
						"administrator-only fixed-shape retention status with independent completeness labels",
						"administrator authorization completes before any store or filesystem inventory or cache touch; unauthorized requests consume none of the 52-component scan budget",
						"all collectors report explicitly unavailable and perform zero inventory scans until their follow-up ticket lands",
						"unconditional capacity warning before store.Open and in every status response",
						"fixed-work component allocation, neutral cap-plus-one, and authorization tests",
					},
					Excluded: []string{
						"retained-owner writer, lifecycle, cleanup, or data mutations",
						"evidence, proof, job, Investigation, manifest, or artifact deletion",
						"new retention configuration",
						"backup or restore mutation",
						"store or filesystem inventory collectors and inventory-cache population",
					},
					FitsOnePR: true,
					SizeProof: []string{
						"fixed registry of twelve owners and fifty-two declared components",
						"one fixed response, pre-inventory administrator authorization, and warning surface",
						"one shared fixed-work budget and completeness model",
						"table-driven registry, authorization, zero-scan, and response-bound coverage",
						"no store or filesystem collector implementation",
					},
				},
				{
					Ticket:  "T30.6p",
					Purpose: "bounded core SurrealDB retention-status collectors",
					Changes: []string{
						"bounded collectors for evidence_publications, extraction_attempts, extraction_outcomes, evidence_pins, proof_bundles, durable_job_history, and caller_rows",
						"one bespoke evidence-graph classifier plus generic bounded SurrealDB table summaries",
						"bounded, resumable, nonblocking install and backfill for any separately justified bounded-query index",
						"replace only these seven owners' unavailable shell values with independently labeled exact, lower_bound, or unavailable summaries",
					},
					Excluded: []string{
						"Investigation/Workbench or filesystem publication collectors",
						"retained-owner writer, lifecycle, cleanup, or data mutations",
						"first-open full-history index build or backfill",
					},
					FitsOnePR: true,
					SizeProof: []string{
						"seven owners and twenty-one declared components",
						"one bespoke evidence-graph classifier",
						"one generic bounded SurrealDB table-summary path",
						"table-driven bounds and completeness coverage",
						"no filesystem adapters",
					},
				},
				{
					Ticket:  "T30.6q",
					Purpose: "bounded Investigation and Workbench retention-status collector",
					Changes: []string{
						"bounded collector for investigation_workbench_rows across its exact twenty-four component tables and owner lifecycle state",
						"bounded, resumable, nonblocking install and backfill for any separately justified bounded-query index",
						"replace only the Investigation/Workbench owner's unavailable shell value with independently labeled exact, lower_bound, or unavailable summaries",
					},
					Excluded: []string{
						"other SurrealDB or filesystem publication collectors",
						"Investigation/Workbench writer, lifecycle, cleanup, or data mutations",
						"first-open full-history index build or backfill",
					},
					FitsOnePR: true,
					SizeProof: []string{
						"one owner and twenty-four exact component tables",
						"one bounded generic table-summary path plus one owner-lifecycle classifier",
						"table-driven exact-table, lifecycle-state, bounds, and completeness coverage",
					},
				},
				{
					Ticket:  "T30.6r",
					Purpose: "bounded derived-publication authority and filesystem collectors completing retention status",
					Changes: []string{
						"bounded collectors for candidate_artifacts, focused_indexes, resolver_catalogs, and caller_artifacts",
						"reconcile current store authority with candidate, focused-index, resolver-catalog, and caller-leaf filesystem inventories",
						"bound every installation-root and repository-directory scan with explicit cap-plus-one and independently labeled completeness",
						"replace the final four owners' unavailable shell values and complete the twelve-owner retention-status surface",
					},
					Excluded: []string{
						"publication writer, lifecycle, cleanup, or data mutations",
						"unbounded directory walk or filesystem inventory",
						"new retention configuration or deletion",
					},
					FitsOnePR: true,
					SizeProof: []string{
						"four owners and seven declared components",
						"four fixed filesystem adapters plus bounded store-authority lookups",
						"explicit directory-scan budgets and cap-plus-one behavior",
						"table-driven authority, inventory-bound, and completeness coverage",
					},
				},
			},
			FitsOnePR:           false,
			DownstreamGate:      "T30.7 depends on completion through T30.6r",
			FutureBoundedPolicy: "requires a new ADR plus separate evidence/pin, proof-bundle, durable-job, Investigation/Workbench, caller-row, caller-filesystem/lease, and restore-enablement tickets",
		},
	}
}

func growthPoint(transitions int) GrowthPoint {
	return GrowthPoint{
		HistoricalTransitions:                transitions,
		EvidenceAdmissionRowsPerDomain:       int64(transitions * EvidenceAdmissionRows),
		CandidateFailedTransitionResidueSets: int64(transitions),
		CallerIncompleteGenerationSuccessBytes: int64(transitions) *
			(callerleaf.MaxAggregateCanonicalBytes + callerleaf.MaxCanonicalBytesPerPair),
	}
}

func durableJobGrowthPoint(repositories int) DurableJobGrowthPoint {
	const resyncsPerCommonYear = int64(365 * 24)
	return DurableJobGrowthPoint{
		Repositories:                       repositories,
		ResyncsPerCommonYear:               resyncsPerCommonYear,
		IndexingJobRowsPerCommonYear:       int64(repositories) * resyncsPerCommonYear,
		ConnectionSyncJobRowsPerCommonYear: resyncsPerCommonYear,
	}
}

// Package t393 retains the source-free security and lifecycle gate for T39.3.
// Production packages must not import spike packages.
package t393

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/spike/t354"
	"github.com/bmeddeb/phebs/spike/t391"
	"github.com/bmeddeb/phebs/spike/t392"
)

const (
	Schema          = "t393-security-lifecycle-gate-v1"
	MaxReceiptBytes = 64 << 10

	T391ReceiptSHA256     = "sha256:301d01bccd1ed35b0b539cacdf624cda586e4fbf0267d5c2fe886e097b5501c6"
	T392ReceiptSHA256     = "sha256:20e95fc8e7bf11fb9ae317aa57aeda2966fbf1c866d812f37fadb11aabd26a41"
	T354ReceiptSHA256     = "sha256:74af703392474a7993edd94f6ee71a7db9eddd5547cf3f4258228a1749f203c3"
	RetainedReceiptSHA256 = "sha256:8ccfabee2ed675f5539d1921fdce3280a947f22f51eaf94816eb702b8e694051"
)

type Receipt struct {
	Schema      string         `json:"schema"`
	Outcome     string         `json:"outcome"`
	CompletedOn string         `json:"completed_on"`
	Inputs      []InputBinding `json:"inputs"`
	PriorStop   PriorStop      `json:"t392_stop_authority"`
	Cases       []CaseResult   `json:"cases"`
	Review      ReviewPolicy   `json:"independent_review"`
	Stop        StopPolicy     `json:"stop_policy"`
	Teardown    TeardownResult `json:"teardown"`
	Claims      ReceiptClaims  `json:"claims"`
}

type InputBinding struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
	SHA256 string `json:"sha256"`
}

type PriorStop struct {
	Outcome          string `json:"outcome"`
	FailurePhase     string `json:"failure_phase"`
	FailureClass     string `json:"failure_class"`
	TerminalFailures int    `json:"terminal_failures"`
	LaterPhases      string `json:"later_phases"`
	PreservedAsStop  bool   `json:"preserved_as_stop"`
	Superseded       bool   `json:"superseded"`
}

type CaseResult struct {
	Name       string   `json:"name"`
	Outcome    string   `json:"outcome"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type ReviewPolicy struct {
	Required                       bool   `json:"required"`
	ImplementerMayApprove          bool   `json:"implementer_may_approve"`
	MergeRequiresIndependentReview bool   `json:"merge_requires_independent_review"`
	ReceiptSelfCertifiesReview     bool   `json:"receipt_self_certifies_review"`
	Authority                      string `json:"authority"`
}

type StopPolicy struct {
	FailedNegativeCaseStopsRelease bool `json:"failed_negative_case_stops_release"`
	WarningIsApproval              bool `json:"warning_is_approval"`
	SkippedCaseIsPass              bool `json:"skipped_case_is_pass"`
	ThresholdRaiseAllowed          bool `json:"threshold_raise_allowed"`
}

type TeardownResult struct {
	Outcome               string `json:"outcome"`
	Scope                 string `json:"scope"`
	DerivedDataRetained   bool   `json:"derived_data_retained"`
	CredentialsRetained   bool   `json:"credentials_retained"`
	NeighborDataPreserved bool   `json:"neighbor_data_preserved"`
	Gate                  string `json:"gate"`
}

type ReceiptClaims struct {
	SourceFree                    bool `json:"source_free"`
	T392StopPreserved             bool `json:"t392_stop_preserved"`
	EstablishesSecurityComplete   bool `json:"establishes_security_complete"`
	EstablishesGeneralSLO         bool `json:"establishes_general_slo"`
	EstablishesAccuracy           bool `json:"establishes_accuracy"`
	AuthorizesRelease             bool `json:"authorizes_release"`
	EstablishesMigrationComplete  bool `json:"establishes_migration_complete"`
	EstablishesDecommissionSafety bool `json:"establishes_decommission_safety"`
	ChangesProductionBehavior     bool `json:"changes_production_behavior"`
}

var inputSpecs = []InputBinding{
	{Path: "spike/t391/results.json", Schema: t391.Schema, SHA256: T391ReceiptSHA256},
	{Path: "spike/t392/results.json", Schema: t392.ReceiptSchema, SHA256: T392ReceiptSHA256},
	{Path: "spike/t354/results.json", Schema: t354.Schema, SHA256: T354ReceiptSHA256},
}

func Build(root string) (Receipt, error) {
	inputs, prior, err := readInputs(root)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		Schema: Schema, Outcome: "passed", CompletedOn: "2026-08-06",
		Inputs: inputs, PriorStop: prior, Cases: gateCases(),
		Review: ReviewPolicy{
			Required: true, MergeRequiresIndependentReview: true,
			Authority: "outside_receipt",
		},
		Stop: StopPolicy{FailedNegativeCaseStopsRelease: true},
		Teardown: TeardownResult{
			Outcome: "passed", Scope: "dedicated_ephemeral_gate_workspace",
			NeighborDataPreserved: true,
			Gate:                  "spike/t393.TestEphemeralGateTeardownDestroysDedicatedArtifacts",
		},
		Claims: ReceiptClaims{SourceFree: true, T392StopPreserved: true},
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func ValidateReceipt(receipt Receipt) error {
	if receipt.Schema != Schema || receipt.Outcome != "passed" || receipt.CompletedOn != "2026-08-06" {
		return errors.New("T39.3 receipt identity is invalid")
	}
	if !slices.Equal(receipt.Inputs, inputSpecs) {
		return errors.New("T39.3 input bindings differ from the frozen set")
	}
	wantStop := PriorStop{
		Outcome: "stopped", FailurePhase: "incremental", FailureClass: "pipeline",
		TerminalFailures: 1, LaterPhases: "not_run", PreservedAsStop: true,
	}
	if receipt.PriorStop != wantStop {
		return fmt.Errorf("T39.2 stop authority changed: %+v", receipt.PriorStop)
	}
	if !reflect.DeepEqual(receipt.Cases, gateCases()) {
		return errors.New("T39.3 security/lifecycle matrix changed")
	}
	wantReview := ReviewPolicy{
		Required: true, MergeRequiresIndependentReview: true, Authority: "outside_receipt",
	}
	if receipt.Review != wantReview {
		return errors.New("T39.3 independent-review policy changed")
	}
	if receipt.Stop != (StopPolicy{FailedNegativeCaseStopsRelease: true}) {
		return errors.New("T39.3 stop policy changed")
	}
	wantTeardown := TeardownResult{
		Outcome: "passed", Scope: "dedicated_ephemeral_gate_workspace",
		NeighborDataPreserved: true,
		Gate:                  "spike/t393.TestEphemeralGateTeardownDestroysDedicatedArtifacts",
	}
	if receipt.Teardown != wantTeardown {
		return errors.New("T39.3 teardown result changed")
	}
	wantClaims := ReceiptClaims{SourceFree: true, T392StopPreserved: true}
	if receipt.Claims != wantClaims {
		return errors.New("T39.3 negative claims changed")
	}
	return nil
}

func Marshal(receipt Receipt) ([]byte, error) {
	if err := ValidateReceipt(receipt); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxReceiptBytes {
		return nil, fmt.Errorf("T39.3 receipt bytes %d exceed %d", len(encoded), MaxReceiptBytes)
	}
	return encoded, nil
}

func Decode(data []byte) (Receipt, error) {
	if len(data) == 0 || len(data) > MaxReceiptBytes {
		return Receipt{}, errors.New("T39.3 receipt size is outside the fixed bound")
	}
	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, err
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Receipt{}, err
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func readInputs(root string) ([]InputBinding, PriorStop, error) {
	inputs := slices.Clone(inputSpecs)
	var prior PriorStop
	for _, input := range inputs {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			return nil, PriorStop{}, err
		}
		if Digest(content) != input.SHA256 {
			return nil, PriorStop{}, fmt.Errorf("input %s digest changed", input.Path)
		}
		switch input.Path {
		case "spike/t391/results.json":
			if _, err := t391.Decode(content); err != nil {
				return nil, PriorStop{}, fmt.Errorf("validate T39.1 input: %w", err)
			}
		case "spike/t392/results.json":
			receipt, err := t392.DecodeReceipt(content)
			if err != nil {
				return nil, PriorStop{}, fmt.Errorf("validate T39.2 input: %w", err)
			}
			terminal := 0
			for _, failure := range receipt.Failures {
				if failure.Terminal {
					terminal++
				}
			}
			if len(receipt.Failures) != 1 || receipt.NoOp.Outcome != "not_run" ||
				receipt.Query.Outcome != "not_run" || receipt.Recovery.Outcome != "not_run" ||
				receipt.Restore.Outcome != "not_run" || receipt.Retention.Outcome != "not_run" {
				return nil, PriorStop{}, errors.New("T39.2 stopped phase authority changed")
			}
			prior = PriorStop{
				Outcome: receipt.Outcome, FailurePhase: receipt.Failures[0].Phase,
				FailureClass: receipt.Failures[0].Class, TerminalFailures: terminal,
				LaterPhases: "not_run", PreservedAsStop: true,
			}
		case "spike/t354/results.json":
			var envelope struct {
				Schema  string `json:"schema"`
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal(content, &envelope); err != nil ||
				envelope.Schema != t354.Schema || envelope.Outcome != "completed" {
				return nil, PriorStop{}, errors.New("T35.4 lifecycle input changed")
			}
		}
	}
	return inputs, prior, nil
}

func gateCases() []CaseResult {
	return []CaseResult{
		{
			Name: "authorization_non_disclosure", Outcome: "passed",
			Assertions: []string{"hidden_service_names_and_counts_absent", "authorization_precedes_repository_state_and_filesystem", "operational_denials_remain_classified"},
			Gates:      []string{"internal/api.TestServiceDirectoryHiddenAndAbsentPrecedeInputAndStateWork", "internal/relationshippublication.TestAuthorizationPrecedesRepositoryValidationAndFilesystem", "internal/api.TestExactCallerMapPreservesOperationalAuthorizationErrors"},
		},
		{
			Name: "shared_and_cross_service_authority", Outcome: "passed",
			Assertions: []string{"shared_paths_remain_explicit_many_to_many", "cross_service_edges_retain_both_placements", "unowned_and_nonaccepted_claims_do_not_become_authority"},
			Gates:      []string{"internal/servicecatalog.TestCatalogAllowsExactManyToManyAndCrossServicePrefixes", "internal/relationshippublication.TestBuildProjectsSharedUnownedAndTargetAuthorityAtomically", "internal/api.TestRelationshipServiceAuthorizationGapAndRetainedProofShape"},
		},
		{
			Name: "revocation_cursor_and_proof_reuse", Outcome: "passed",
			Assertions: []string{"revocation_suppresses_inflight_page", "cursor_binds_principal_revision_and_incarnation", "proof_read_reauthorizes"},
			Gates:      []string{"internal/api.TestExactCallerMapSuppressesPageWhenAuthorizationIsLostMidRequest", "internal/api.TestExactCallerMapCursorBindsTamperQueryPrincipalAndRevision", "internal/api.TestServiceDirectoryCursorBindsSnapshotAndIncarnation", "internal/api.TestProofBundleAdminMemberIsolationAndReadReauthorization"},
		},
		{
			Name: "partial_and_stale_roots", Outcome: "passed",
			Assertions: []string{"invalid_v2_never_falls_back", "partial_generation_never_moves_pointer", "stale_gap_transition_fails_final_fence"},
			Gates:      []string{"internal/search.TestServiceSearchRefusesInvalidV2WithoutLegacyFallback", "internal/relationshippublication.TestRecoveryValidatesCompleteGenerationBeforePointerSwap", "internal/api.TestExactCallerMapRejectsGapTransitionAtResultFence"},
		},
		{
			Name: "malicious_catalog", Outcome: "passed",
			Assertions: []string{"unknown_duplicate_and_wrong_type_fields_refused", "limits_checked_before_collection_growth", "cycles_overlap_and_invalid_roles_refused"},
			Gates:      []string{"internal/servicecatalog.TestDecodeStrictClosedWire", "internal/servicecatalog.TestDecodeStopsBeforeCollectionGrowthPastLimits", "internal/servicecatalog.TestCatalogSemanticRefusals"},
		},
		{
			Name: "malicious_source_and_git", Outcome: "passed",
			Assertions: []string{"replacement_refs_ignored", "alternates_and_symlinked_shards_refused", "missing_object_error_has_no_identity", "oversized_blob_refused_before_git_read"},
			Gates:      []string{"internal/sourcepartition.TestBatchReaderIgnoresReplacementRefs", "internal/sourcepartition.TestBatchReaderCancellationAndExternalAlternatesFailClosed", "internal/sourcepartition.TestBatchReaderClassifiesMissingWithoutIdentityLeak", "internal/sourcepartition.TestPlannerRefusesOversizedSelectedBlobBeforeGitRead", "internal/search.TestOpenRejectsSymlinkedShard"},
		},
		{
			Name: "disk_pressure_and_admission", Outcome: "passed",
			Assertions: []string{"collect_refuse_resume_hysteresis_exact", "unknown_capacity_refuses_new_pressure_work", "data_root_identity_and_symlinks_fail_closed"},
			Gates:      []string{"internal/lifecycle.TestGateWatermarksProjectionAndHysteresis", "internal/lifecycle.TestGateUnavailableAndRootHardening", "internal/api.TestLifecycleStatusAuthorizesBeforeSourceAndReturnsBoundedSnapshot"},
		},
		{
			Name: "pin_and_lease_retention", Outcome: "passed",
			Assertions: []string{"current_and_rollback_floor_preserved", "active_reader_lease_blocks_collection", "proof_expiry_releases_only_owned_pins"},
			Gates:      []string{"internal/relationshippublication.TestLifecyclePreservesCurrentRollbackFloorAndReaderLease", "internal/observationpublication.TestLifecyclePreservesCurrentRollbackFloorAndLease", "internal/store.TestProofBundleExpiryReleasesOnlyOwnedPins"},
		},
		{
			Name: "bounded_sweep", Outcome: "passed",
			Assertions: []string{"owner_rotation_restart_fair", "failure_localized_to_owner", "query_delete_and_trim_budgets_enforced", "running_lease_protected"},
			Gates:      []string{"internal/lifecycle.TestControllerPersistsFairRotationAcrossRestartAndLocalizesFailure", "internal/lifecycle.TestGenerationOwnerHonorsAggregateQueryBudgetAndBackupLock", "internal/lifecycle.TestJobOwnerTrimIsBounded", "internal/store.TestGenerationLifecycleProtectsCurrentRollbackFloorAndRunningLease"},
		},
		{
			Name: "backup_restore_and_teardown", Outcome: "passed",
			Assertions: []string{"precious_authority_restored_exact", "composite_generation_validated_or_omitted", "dedicated_artifacts_and_credentials_destroyed", "neighbor_data_preserved"},
			Gates:      []string{"internal/recovery.TestLiveBackupRestoreAndStartupExactSearchRecovery", "internal/relationshippublication.TestArchiveRoundTripPreservesExactCompositeAuthority", "spike/t393.TestEphemeralGateTeardownDestroysDedicatedArtifacts"},
		},
	}
}

func Digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

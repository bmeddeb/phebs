// Package t354 retains the source-free closure receipt for Epic 35. It binds
// the neutral T32.3 profiles to named production-path recovery gates without
// retaining repositories, source paths, query text, or operator data.
package t354

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/bmeddeb/phebs/spike/t323"
)

const (
	Schema                  = "t354-lifecycle-recovery-receipt-v1"
	NeutralReceiptSHA256    = "sha256:899492dcfe2f768de7e75003ff5d420655cbfeb8c44d9a76505bf6d6b8dededd"
	LifecycleStatusSchema   = "phebs-lifecycle-status-v1"
	LifecycleStatusMaxBytes = 16 << 10
)

type Receipt struct {
	Schema         string        `json:"schema"`
	Outcome        string        `json:"outcome"`
	NeutralInput   NeutralInput  `json:"neutral_input"`
	Profiles       []Profile     `json:"profiles"`
	Cases          []Case        `json:"cases"`
	OperatorStatus OperatorGate  `json:"operator_status"`
	Claims         ReceiptClaims `json:"claims"`
}

type NeutralInput struct {
	Schema        string `json:"schema"`
	ReceiptSHA256 string `json:"receipt_sha256"`
	Synthetic     bool   `json:"synthetic"`
}

type Profile struct {
	Name          string `json:"name"`
	Services      int    `json:"services"`
	Memberships   int    `json:"memberships"`
	FileRecords   int    `json:"file_records"`
	MaxPathFanout int    `json:"max_path_fanout"`
}

type Case struct {
	Name       string   `json:"name"`
	Outcome    string   `json:"outcome"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type OperatorGate struct {
	Schema           string `json:"schema"`
	Authorization    string `json:"authorization"`
	MaximumBytes     int    `json:"maximum_bytes"`
	RawErrors        bool   `json:"raw_errors"`
	SourceIdentities bool   `json:"source_identities"`
}

type ReceiptClaims struct {
	SourceFree           bool `json:"source_free"`
	NoFalseCurrent       bool `json:"no_false_current"`
	NoSLO                bool `json:"no_slo"`
	NoReleaseAuthority   bool `json:"no_release_authority"`
	NoScaleQualification bool `json:"no_scale_qualification"`
}

func Build(neutralReceipt []byte) (Receipt, error) {
	if digest(neutralReceipt) != NeutralReceiptSHA256 {
		return Receipt{}, errors.New("T32.3 neutral receipt digest changed")
	}
	var neutral struct {
		Schema string `json:"schema"`
		Claims struct {
			Synthetic bool `json:"synthetic"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(neutralReceipt, &neutral); err != nil {
		return Receipt{}, err
	}
	if neutral.Schema != t323.ReceiptSchema || !neutral.Claims.Synthetic {
		return Receipt{}, errors.New("T32.3 input is not the frozen synthetic receipt")
	}
	profiles := make([]Profile, 0, 2)
	for _, count := range []int{1_000, 5_000} {
		profile, err := t323.GenerateLoadProfile(count)
		if err != nil {
			return Receipt{}, err
		}
		profiles = append(profiles, Profile{
			Name: profile.Name, Services: profile.Cardinality.Services,
			Memberships:   profile.Cardinality.Memberships,
			FileRecords:   profile.Cardinality.Files,
			MaxPathFanout: profile.Cardinality.MaxPathFanout,
		})
	}
	return Receipt{
		Schema: Schema, Outcome: "completed",
		NeutralInput: NeutralInput{
			Schema: neutral.Schema, ReceiptSHA256: NeutralReceiptSHA256, Synthetic: true,
		},
		Profiles: profiles,
		Cases: []Case{
			{Name: "catalog_churn", Outcome: "passed", Assertions: []string{"current_and_service_roots_preserved", "count_excess_collected"}, Gates: []string{"TestCatalogLifecycleRetainsCurrentAndTwoRollbackGenerations", "TestCatalogLifecycleProtectsActiveServiceCatalog"}},
			{Name: "commit_coalescing", Outcome: "passed", Assertions: []string{"superseded_generation_never_reactivated", "settled_siblings_unchanged"}, Gates: []string{"TestGenerationScheduleRetryCoalescingAndStaleFence"}},
			{Name: "interrupted_stage", Outcome: "passed", Assertions: []string{"prior_publication_remains_current", "startup_reconciler_owns_stage"}, Gates: []string{"TestIndexStateFailureRemovesUncommittedShard"}},
			{Name: "reader_lease", Outcome: "passed", Assertions: []string{"replacement_waits_for_final_lease", "result_generation_not_relabeled"}, Gates: []string{"TestServiceSearchRetiresReaderOnlyAfterActiveLeaseReleases"}},
			{Name: "proof_pin_release", Outcome: "passed", Assertions: []string{"bundle_readable_while_retained", "release_removes_only_owned_pins"}, Gates: []string{"TestProofBundleExpiryReleasesOnlyOwnedPins", "TestInvestigationRetentionArtifactSweepReleasesOnlyItsOwnPins"}},
			{Name: "pressure_hysteresis", Outcome: "passed", Assertions: []string{"collect_at_80", "refuse_at_90", "resume_below_75"}, Gates: []string{"TestGateWatermarksProjectionAndHysteresis"}},
			{Name: "bounded_sweep", Outcome: "passed", Assertions: []string{"one_of_13_owners_per_turn", "64_candidates_16_deletes_16_queries"}, Gates: []string{"TestControllerPersistsFairRotationAcrossRestartAndLocalizesFailure"}},
			{Name: "restart", Outcome: "passed", Assertions: []string{"rotation_cursor_persisted", "failed_owner_cursor_preserved"}, Gates: []string{"TestLifecycleCursorCompareAndSwap", "TestControllerPersistsFairRotationAcrossRestartAndLocalizesFailure"}},
			{Name: "backup_restore", Outcome: "passed", Assertions: []string{"lifecycle_cursor_byte_equal", "precious_authority_preserved"}, Gates: []string{"TestLiveBackupRestoreAndStartupExactSearchRecovery"}},
			{Name: "operator_status", Outcome: "passed", Assertions: []string{"authorization_before_source", "fixed_source_free_snapshot"}, Gates: []string{"TestLifecycleStatusAuthorizesBeforeSourceAndReturnsBoundedSnapshot", "TestStatusMonitorIsBoundedSourceFreeAndKeepsLastOwnerResult"}},
		},
		OperatorStatus: OperatorGate{
			Schema: LifecycleStatusSchema, Authorization: "administrator_first",
			MaximumBytes: LifecycleStatusMaxBytes, RawErrors: false, SourceIdentities: false,
		},
		Claims: ReceiptClaims{
			SourceFree: true, NoFalseCurrent: true, NoSLO: true,
			NoReleaseAuthority: true, NoScaleQualification: true,
		},
	}, nil
}

func Marshal(receipt Receipt) ([]byte, error) {
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

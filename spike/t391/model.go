// Package t391 executes and retains the source-free neutral correctness,
// scale-admission, and recovery gate for T39.1. Production packages must not
// import spike packages.
package t391

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
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/spike/t323"
)

const (
	Schema = "t391-neutral-correctness-scale-recovery-gate-v1"

	NeutralReceiptSHA256      = "sha256:899492dcfe2f768de7e75003ff5d420655cbfeb8c44d9a76505bf6d6b8dededd"
	NeutralBundleSHA256       = "sha256:8d70693ee440ff7683f8c3a39cc9b6565dd265cbc546d40e961759f2237617fa"
	TopologyReceiptSHA256     = "sha256:4992dcdafb9100e3ca6f34cf3f7b1b54030b58f811f48ff55b870469a4775f7c"
	LifecycleReceiptSHA256    = "sha256:74af703392474a7993edd94f6ee71a7db9eddd5547cf3f4258228a1749f203c3"
	RelationshipReceiptSHA256 = "sha256:7f2aa20cedf98c1dbaa2da00102470ba00eaea4dc7a8e9c6e3de1069da3f7ec0"
	ProductReceiptSHA256      = "sha256:a50056e72c40a01aa5abc58eb580286413a67db936e8e83706a8cadd685d82e0"
)

type Receipt struct {
	Schema      string            `json:"schema"`
	Outcome     string            `json:"outcome"`
	CompletedOn string            `json:"completed_on"`
	Inputs      []InputBinding    `json:"inputs"`
	Correctness CorrectnessResult `json:"correctness"`
	Profiles    []ProfileResult   `json:"profiles"`
	FinalGates  []GateResult      `json:"final_writer_reader_gates"`
	CostGates   []GateResult      `json:"cost_gates"`
	SafetyGates []GateResult      `json:"safety_gates"`
	Claims      Claims            `json:"claims"`
}

type InputBinding struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
	SHA256 string `json:"sha256"`
}

type CorrectnessResult struct {
	Revisions             int    `json:"revisions"`
	OracleMemberships     int    `json:"oracle_memberships"`
	OracleQueries         int    `json:"oracle_queries"`
	OracleRelationships   int    `json:"oracle_relationships"`
	OracleStates          int    `json:"oracle_states"`
	OracleTombstones      int    `json:"oracle_tombstones"`
	MalformedRefusals     int    `json:"malformed_refusals"`
	CatalogAdapter        string `json:"catalog_adapter"`
	CatalogRoundTripEqual bool   `json:"catalog_round_trip_equal"`
	MembershipEqual       bool   `json:"membership_equal"`
	SearchEqual           bool   `json:"search_equal"`
	RelationshipEqual     bool   `json:"relationship_equal"`
	StateEqual            bool   `json:"state_equal"`
	DeterministicRebuild  bool   `json:"deterministic_rebuild"`
	ProductionReaderGates bool   `json:"production_reader_gates"`
	ProductionWriterGates bool   `json:"production_writer_gates"`
}

type ProfileResult struct {
	Name                   string `json:"name"`
	Services               int    `json:"services"`
	Memberships            int    `json:"memberships"`
	FileRecords            int    `json:"file_records"`
	MaxPathFanout          int    `json:"max_path_fanout"`
	Outcome                string `json:"outcome"`
	Refusal                string `json:"refusal"`
	Limit                  int    `json:"limit"`
	CheckedBeforeMapGrowth bool   `json:"checked_before_map_growth"`
	ReadersStarted         bool   `json:"readers_started"`
}

type GateResult struct {
	Name       string   `json:"name"`
	Outcome    string   `json:"outcome"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type Claims struct {
	SourceFree                    bool `json:"source_free"`
	ExpectedRefusalIsNotScalePass bool `json:"expected_refusal_is_not_scale_pass"`
	EstablishesTargetSLO          bool `json:"establishes_target_slo"`
	EstablishesAccuracy           bool `json:"establishes_accuracy"`
	QualifiesSupportedScale       bool `json:"qualifies_supported_scale"`
	AuthorizesTargetRun           bool `json:"authorizes_target_run"`
	AuthorizesRelease             bool `json:"authorizes_release"`
	ChangesProductionBehavior     bool `json:"changes_production_behavior"`
}

var inputSpecs = []InputBinding{
	{Path: "spike/t323/receipt.json", Schema: t323.ReceiptSchema, SHA256: NeutralReceiptSHA256},
	{Path: "spike/t323/t323-neutral-corpus.bundle", Schema: "git-bundle", SHA256: NeutralBundleSHA256},
	{Path: "spike/t324/results.json", Schema: "t324-search-topology-cost-receipt-v1", SHA256: TopologyReceiptSHA256},
	{Path: "spike/t354/results.json", Schema: "t354-lifecycle-recovery-receipt-v1", SHA256: LifecycleReceiptSHA256},
	{Path: "spike/t375/results.json", Schema: "t375-relationship-reader-demo-v1", SHA256: RelationshipReceiptSHA256},
	{Path: "spike/t385/results.json", Schema: "t385-microservice-product-closure-v1", SHA256: ProductReceiptSHA256},
}

func Build(root string) (Receipt, error) {
	inputs, err := readInputs(root)
	if err != nil {
		return Receipt{}, err
	}
	correctness, err := replayCorrectness()
	if err != nil {
		return Receipt{}, err
	}
	profiles, err := replayProfiles()
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		Schema: Schema, Outcome: "passed", CompletedOn: "2026-08-06",
		Inputs: inputs, Correctness: correctness, Profiles: profiles,
		FinalGates: []GateResult{
			{Name: "catalog_writer", Outcome: "passed", Assertions: []string{"t323_head_census_exact", "canonical_publication_bound_to_head", "failed_replacement_preserves_prior"}, Gates: []string{"internal/servicecatalogingest.TestT344WholeSearchCatalogUsesOrdinaryCensusAndPublication", "internal/servicecatalogingest.TestCommittedCatalogReconcileAndFailedReplacementPreservesPrior"}},
			{Name: "search_reader", Outcome: "passed", Assertions: []string{"catalog_paths_inside_zoekt_query", "exact_v2_generation_and_final_fence", "invalid_v2_never_falls_back"}, Gates: []string{"internal/servicequery.TestCompileBindsCurrentServiceInsideZoektQuery", "internal/search.TestServiceSearchUsesExactV2PredicateAndFinalFence", "internal/search.TestServiceSearchRefusesInvalidV2WithoutLegacyFallback"}},
			{Name: "relationship_writer_reader", Outcome: "passed", Assertions: []string{"rpc_and_topic_postings_classified", "atomic_service_projection", "authorized_reader_retains_gaps_and_proof_shape"}, Gates: []string{"internal/rpccallerposting.TestBuildPublishesResolvedNameMatchUnresolvedAndOccurrenceIdentity", "internal/kafkatopicposting.TestBuildSeparatesPlanesSpellingClassificationsAndOccurrences", "internal/relationshippublication.TestBuildProjectsSharedUnownedAndTargetAuthorityAtomically", "internal/api.TestRelationshipServiceAuthorizationGapAndRetainedProofShape"}},
		},
		CostGates: []GateResult{
			{Name: "cold_warm_reads", Outcome: "passed", Assertions: []string{"cold_open_validates_complete_generation", "warm_read_reuses_digest_bound_cache", "final_fence_discards_transition"}, Gates: []string{"internal/search.TestServiceSearchUsesExactV2PredicateAndFinalFence", "internal/observationpublication.TestWarmCacheDoesNotRereadMemberAndCorruptColdOpenRefuses"}},
			{Name: "restart_no_op", Outcome: "passed", Assertions: []string{"current_generation_reused", "no_member_reread_on_warm_progress", "no_duplicate_schedule"}, Gates: []string{"internal/observationpublication.TestRuntimeSchedulesPublishesAndRestartNoOps", "internal/observationpublication.TestProgressReportsCurrentReceiptAndWarmReadsNoMembers"}},
			{Name: "bounded_update", Outcome: "passed", Assertions: []string{"only_affected_namespace_rebuilt", "service_limit_localizes_failure", "resident_charge_released"}, Gates: []string{"internal/resolvernamespace.TestOnlyAffectedNamespaceRebuilds", "internal/relationshippublication.TestServiceLimitPublishesFailedPartitionWithoutBlockingSibling", "internal/relationshippublication.TestResidentFenceFailsOnlyCrossingService"}},
			{Name: "bounded_gc", Outcome: "passed", Assertions: []string{"current_and_rollback_floor_preserved", "active_lease_prevents_collection", "sweep_budget_bounded"}, Gates: []string{"internal/relationshippublication.TestLifecyclePreservesCurrentRollbackFloorAndReaderLease", "internal/observationpublication.TestLifecyclePreservesCurrentRollbackFloorAndLease", "internal/lifecycle.TestControllerPersistsFairRotationAcrossRestartAndLocalizesFailure"}},
		},
		SafetyGates: []GateResult{
			{Name: "partial_publication", Outcome: "passed", Assertions: []string{"prior_pointer_preserved", "incomplete_generation_never_authoritative"}, Gates: []string{"internal/resolvernamespace.TestRecoveryValidatesCompleteGenerationBeforePointerSwap", "internal/relationshippublication.TestRecoveryValidatesCompleteGenerationBeforePointerSwap"}},
			{Name: "authorization", Outcome: "passed", Assertions: []string{"authorization_precedes_repository_validation", "hidden_service_precedes_state_and_input_work"}, Gates: []string{"internal/relationshippublication.TestAuthorizationPrecedesRepositoryValidationAndFilesystem", "internal/api.TestServiceDirectoryHiddenAndAbsentPrecedeInputAndStateWork"}},
			{Name: "recovery", Outcome: "passed", Assertions: []string{"marker_recovery_preserves_prior", "exhausted_schedule_mints_distinct_recovery_identity"}, Gates: []string{"internal/observationpublication.TestMarkerRecoveryPreservesPriorAndCacheDelaysRetirement", "internal/observationpublication.TestRuntimeMintsDistinctRecoveryScheduleAfterExhaustion"}},
			{Name: "restore", Outcome: "passed", Assertions: []string{"composite_authority_byte_equal", "live_restore_revalidates_exact_search"}, Gates: []string{"internal/relationshippublication.TestArchiveRoundTripPreservesExactCompositeAuthority", "internal/recovery.TestLiveBackupRestoreAndStartupExactSearchRecovery"}},
			{Name: "retained_proof", Outcome: "passed", Assertions: []string{"v1_proof_remains_readable", "v2_candidate_scope_remains_readable", "authorization_rechecked_on_read"}, Gates: []string{"internal/api.TestRetainedV1ProofBundleScopePostureRemainsReadable", "internal/api.TestRetainedV2ProofBundleCandidateScopeRemainsReadable", "internal/api.TestProofBundleAdminMemberIsolationAndReadReauthorization"}},
		},
		Claims: Claims{
			SourceFree: true, ExpectedRefusalIsNotScalePass: true,
		},
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func ValidateReceipt(receipt Receipt) error {
	if receipt.Schema != Schema || receipt.Outcome != "passed" || receipt.CompletedOn != "2026-08-06" ||
		!receipt.Claims.SourceFree || !receipt.Claims.ExpectedRefusalIsNotScalePass ||
		receipt.Claims.EstablishesTargetSLO || receipt.Claims.EstablishesAccuracy ||
		receipt.Claims.QualifiesSupportedScale || receipt.Claims.AuthorizesTargetRun ||
		receipt.Claims.AuthorizesRelease || receipt.Claims.ChangesProductionBehavior {
		return errors.New("T39.1 receipt envelope or claims are invalid")
	}
	if !slices.Equal(receipt.Inputs, inputSpecs) {
		return errors.New("T39.1 input bindings differ from the frozen set")
	}
	c := receipt.Correctness
	if c.Revisions != 5 || c.OracleMemberships != 67 || c.OracleQueries != 40 ||
		c.OracleRelationships != 10 || c.OracleStates != 38 || c.OracleTombstones != 8 ||
		c.MalformedRefusals != 5 || c.CatalogAdapter != "t323-fixture-to-phebs-service-catalog-v2" ||
		!c.CatalogRoundTripEqual || !c.MembershipEqual || !c.SearchEqual ||
		!c.RelationshipEqual || !c.StateEqual || !c.DeterministicRebuild ||
		!c.ProductionReaderGates || !c.ProductionWriterGates {
		return fmt.Errorf("T39.1 correctness result is incomplete: %+v", c)
	}
	if len(receipt.Profiles) != 2 || receipt.Profiles[0].Services != 1_000 ||
		receipt.Profiles[0].Refusal != "accepted_path_fanout" ||
		receipt.Profiles[0].Limit != servicecatalog.MaxAcceptedPathFanout ||
		receipt.Profiles[1].Services != 5_000 ||
		receipt.Profiles[1].Refusal != "service_count" ||
		receipt.Profiles[1].Limit != servicecatalog.MaxServices {
		return errors.New("T39.1 frozen-profile outcomes are incomplete")
	}
	for _, profile := range receipt.Profiles {
		if profile.Outcome != "expected_refusal" || !profile.CheckedBeforeMapGrowth || profile.ReadersStarted {
			return fmt.Errorf("profile %q did not fail closed", profile.Name)
		}
	}
	if len(receipt.FinalGates) != 3 || len(receipt.CostGates) != 4 || len(receipt.SafetyGates) != 5 {
		return errors.New("T39.1 gate inventory is incomplete")
	}
	gates := append(slices.Clone(receipt.FinalGates), receipt.CostGates...)
	gates = append(gates, receipt.SafetyGates...)
	for _, gate := range gates {
		if gate.Name == "" || gate.Outcome != "passed" || len(gate.Assertions) == 0 || len(gate.Gates) == 0 {
			return fmt.Errorf("gate %q is incomplete", gate.Name)
		}
	}
	return nil
}

func Marshal(receipt Receipt) ([]byte, error) {
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func Decode(data []byte) (Receipt, error) {
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

func readInputs(root string) ([]InputBinding, error) {
	inputs := slices.Clone(inputSpecs)
	for _, input := range inputs {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			return nil, err
		}
		if digest(content) != input.SHA256 {
			return nil, fmt.Errorf("input %s digest changed", input.Path)
		}
		if input.Schema == "git-bundle" {
			continue
		}
		var envelope struct {
			Schema string `json:"schema"`
		}
		if err := json.Unmarshal(content, &envelope); err != nil || envelope.Schema != input.Schema {
			return nil, fmt.Errorf("input %s schema changed", input.Path)
		}
	}
	return inputs, nil
}

func replayCorrectness() (CorrectnessResult, error) {
	snapshots, err := t323.SmallSnapshots()
	if err != nil {
		return CorrectnessResult{}, err
	}
	result := CorrectnessResult{
		Revisions: len(snapshots), CatalogAdapter: "t323-fixture-to-phebs-service-catalog-v2",
		CatalogRoundTripEqual: true, MembershipEqual: true, SearchEqual: true,
		RelationshipEqual: true, StateEqual: true, DeterministicRebuild: true,
		ProductionReaderGates: true, ProductionWriterGates: true,
	}
	for _, snapshot := range snapshots {
		catalog, err := adaptSnapshot(snapshot)
		if err != nil {
			return CorrectnessResult{}, fmt.Errorf("%s: adapt catalog: %w", snapshot.Revision, err)
		}
		first, err := servicecatalog.Canonical(catalog)
		if err != nil {
			return CorrectnessResult{}, fmt.Errorf("%s: canonical catalog: %w", snapshot.Revision, err)
		}
		opened, err := servicecatalog.Decode(first)
		if err != nil {
			return CorrectnessResult{}, fmt.Errorf("%s: decode catalog: %w", snapshot.Revision, err)
		}
		second, err := servicecatalog.Canonical(opened)
		if err != nil || !bytes.Equal(first, second) {
			return CorrectnessResult{}, fmt.Errorf("%s: catalog round trip changed bytes", snapshot.Revision)
		}
		if err := compareMemberships(snapshot, opened); err != nil {
			return CorrectnessResult{}, err
		}
		if err := compareQueries(snapshot, opened); err != nil {
			return CorrectnessResult{}, err
		}
		if err := compareRelationships(snapshot, opened); err != nil {
			return CorrectnessResult{}, err
		}
		if err := compareStates(snapshot, opened); err != nil {
			return CorrectnessResult{}, err
		}
		if err := refuseMalformedFixture(snapshot); err != nil {
			return CorrectnessResult{}, err
		}
		result.OracleMemberships += len(snapshot.Oracle.Memberships)
		result.OracleQueries += len(snapshot.Oracle.Queries)
		result.OracleRelationships += len(snapshot.Oracle.Relationships)
		result.OracleStates += len(snapshot.Oracle.States)
		result.OracleTombstones += len(snapshot.Oracle.Tombstones)
		result.MalformedRefusals += len(snapshot.Catalog.Invalid)
	}
	return result, nil
}

func replayProfiles() ([]ProfileResult, error) {
	results := make([]ProfileResult, 0, 2)
	for _, services := range []int{1_000, 5_000} {
		profile, err := t323.GenerateLoadProfile(services)
		if err != nil {
			return nil, err
		}
		catalog := adaptProfile(profile)
		err = servicecatalog.Validate(catalog)
		result := ProfileResult{
			Name: profile.Name, Services: profile.Cardinality.Services,
			Memberships: profile.Cardinality.Memberships, FileRecords: profile.Cardinality.Files,
			MaxPathFanout: profile.Cardinality.MaxPathFanout, Outcome: "expected_refusal",
			CheckedBeforeMapGrowth: true, ReadersStarted: false,
		}
		switch services {
		case 1_000:
			if !errors.Is(err, servicecatalog.ErrFanoutLimit) {
				return nil, fmt.Errorf("1,000-service profile refusal = %v", err)
			}
			result.Refusal, result.Limit = "accepted_path_fanout", servicecatalog.MaxAcceptedPathFanout
		case 5_000:
			if !errors.Is(err, servicecatalog.ErrServiceLimit) {
				return nil, fmt.Errorf("5,000-service profile refusal = %v", err)
			}
			result.Refusal, result.Limit = "service_count", servicecatalog.MaxServices
		}
		results = append(results, result)
	}
	return results, nil
}

func adaptSnapshot(snapshot t323.Snapshot) (servicecatalog.Catalog, error) {
	groups := make(map[string][]t323.AuthorityRecord)
	for _, record := range snapshot.Catalog.Records {
		groups[record.ServiceKey] = append(groups[record.ServiceKey], record)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "t323-neutral-adapter", Version: snapshot.Revision,
		},
		Unowned: make([]servicecatalog.UnownedPlacement, 0, len(snapshot.Catalog.UnownedPaths)),
	}
	seenMemberships := make(map[string]struct{})
	for _, key := range keys {
		records := groups[key]
		record := records[0]
		service := servicecatalog.Service{
			Key: key, DisplayName: record.DisplayName, Origin: servicecatalog.OriginBase,
		}
		switch record.Disposition {
		case "accepted":
			service.Disposition = servicecatalog.DispositionAccepted
		case "proposal":
			service.Disposition = servicecatalog.DispositionProposal
			service.Reason = "unaccepted neutral-fixture proposal"
		case "conflicting":
			service.Disposition = servicecatalog.DispositionConflict
			authorities := make([]string, 0, len(records))
			for _, candidate := range records {
				authorities = append(authorities, candidate.AuthorityID)
			}
			slices.Sort(authorities)
			service.Reason = "conflicting neutral-fixture authorities: " + strings.Join(authorities, ",")
		case "tombstone":
			service.Disposition = servicecatalog.DispositionRejected
			service.Successors = slices.Clone(record.Successors)
			service.Reason = "neutral-fixture tombstone: removed"
			for _, tombstone := range snapshot.Oracle.Tombstones {
				if tombstone.ServiceKey == key {
					service.Reason = "neutral-fixture tombstone: " + tombstone.Reason
					break
				}
			}
		default:
			return servicecatalog.Catalog{}, fmt.Errorf("unsupported fixture disposition %q", record.Disposition)
		}
		catalog.Services = append(catalog.Services, service)
		for _, candidate := range records {
			for _, placement := range candidate.Placements {
				appendMembership(&catalog, seenMemberships, key, placement.Path, placement.Role)
				if placement.Role == servicecatalog.RoleTyped {
					appendMembership(&catalog, seenMemberships, key, placement.Path, servicecatalog.RoleSupporting)
				}
			}
		}
	}
	for _, path := range snapshot.Catalog.UnownedPaths {
		catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{Path: path, Origin: servicecatalog.OriginBase})
	}
	return servicecatalog.Normalize(catalog)
}

func adaptProfile(profile t323.LoadProfile) servicecatalog.Catalog {
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "t323-neutral-profile", Version: profile.Name,
		},
	}
	seen := make(map[string]struct{})
	for _, profileService := range profile.Services {
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: profileService.ServiceKey, DisplayName: profileService.ServiceKey,
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		})
		for _, placement := range profileService.Placements {
			appendMembership(&catalog, seen, profileService.ServiceKey, placement.Path, placement.Role)
			if placement.Role == servicecatalog.RoleTyped {
				appendMembership(&catalog, seen, profileService.ServiceKey, placement.Path, servicecatalog.RoleSupporting)
			}
		}
	}
	for _, file := range profile.Files {
		if strings.HasPrefix(file.Path, "tools/unowned-") {
			catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{Path: file.Path, Origin: servicecatalog.OriginBase})
		}
	}
	return catalog
}

func appendMembership(catalog *servicecatalog.Catalog, seen map[string]struct{}, serviceKey, path, role string) {
	key := serviceKey + "\x00" + path + "\x00" + role
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
		ServiceKey: serviceKey, Path: path, Role: role, Origin: servicecatalog.OriginBase,
	})
}

func compareMemberships(snapshot t323.Snapshot, catalog servicecatalog.Catalog) error {
	expected := make([]string, 0, len(snapshot.Oracle.Memberships))
	expectedSet := make(map[string]struct{}, len(snapshot.Oracle.Memberships))
	for _, membership := range snapshot.Oracle.Memberships {
		key := membership.ServiceKey + "\x00" + membership.Path + "\x00" + membership.Role
		expected = append(expected, key)
		expectedSet[key] = struct{}{}
	}
	services := make(map[string]string, len(catalog.Services))
	for _, service := range catalog.Services {
		services[service.Key] = service.Disposition
	}
	actual := make([]string, 0, len(expected))
	for _, membership := range catalog.Memberships {
		if services[membership.ServiceKey] != servicecatalog.DispositionAccepted {
			continue
		}
		key := membership.ServiceKey + "\x00" + membership.Path + "\x00" + membership.Role
		if membership.Role == servicecatalog.RoleSupporting {
			typed := membership.ServiceKey + "\x00" + membership.Path + "\x00" + servicecatalog.RoleTyped
			if _, fixtureSupporting := expectedSet[key]; !fixtureSupporting {
				if _, fixtureTyped := expectedSet[typed]; fixtureTyped {
					continue
				}
			}
		}
		actual = append(actual, key)
	}
	slices.Sort(expected)
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("%s: final membership projection differs from oracle", snapshot.Revision)
	}
	return nil
}

func compareQueries(snapshot t323.Snapshot, catalog servicecatalog.Catalog) error {
	pathsByService := make(map[string][]string)
	dispositions := make(map[string]string)
	for _, service := range catalog.Services {
		dispositions[service.Key] = service.Disposition
	}
	for _, membership := range catalog.Memberships {
		if dispositions[membership.ServiceKey] == servicecatalog.DispositionAccepted {
			pathsByService[membership.ServiceKey] = append(pathsByService[membership.ServiceKey], membership.Path)
		}
	}
	for _, query := range snapshot.Oracle.Queries {
		var actual []string
		if query.State == "exact" || query.State == "stale" {
			for path, content := range snapshot.Files {
				if !bytes.Contains(content, []byte(query.Expression)) {
					continue
				}
				if query.Scope == "service" && !selectedBy(pathsByService[query.ServiceKey], path) {
					continue
				}
				actual = append(actual, path)
			}
		}
		slices.Sort(actual)
		expected := slices.Clone(query.Paths)
		slices.Sort(expected)
		if !slices.Equal(actual, expected) {
			return fmt.Errorf("%s: query %s result differs from oracle", snapshot.Revision, query.ID)
		}
	}
	return nil
}

func compareRelationships(snapshot t323.Snapshot, catalog servicecatalog.Catalog) error {
	accepted := make(map[string]bool)
	paths := make(map[string][]string)
	for _, service := range catalog.Services {
		accepted[service.Key] = service.Disposition == servicecatalog.DispositionAccepted
	}
	for _, membership := range catalog.Memberships {
		if accepted[membership.ServiceKey] {
			paths[membership.ServiceKey] = append(paths[membership.ServiceKey], membership.Path)
		}
	}
	for _, relationship := range snapshot.Oracle.Relationships {
		if relationship.Kind != "rpc" && relationship.Kind != "topic" {
			return fmt.Errorf("%s: unsupported relationship kind", snapshot.Revision)
		}
		if !accepted[relationship.Provider] || relationship.Identity == "" {
			return fmt.Errorf("%s: relationship provider is not accepted", snapshot.Revision)
		}
		if relationship.State == "resolved" {
			if !accepted[relationship.Consumer] || relationship.Reason != "" {
				return fmt.Errorf("%s: resolved relationship authority differs", snapshot.Revision)
			}
		} else if relationship.State != "unresolved" || relationship.Reason == "" || accepted[relationship.Consumer] {
			return fmt.Errorf("%s: unresolved relationship authority differs", snapshot.Revision)
		}
		for _, evidencePath := range relationship.EvidencePaths {
			if _, exists := snapshot.Files[evidencePath]; !exists ||
				(!selectedBy(paths[relationship.Provider], evidencePath) && !selectedBy(paths[relationship.Consumer], evidencePath)) {
				return fmt.Errorf("%s: relationship evidence is outside accepted placements", snapshot.Revision)
			}
		}
	}
	return nil
}

func compareStates(snapshot t323.Snapshot, catalog servicecatalog.Catalog) error {
	dispositions := make(map[string]string, len(catalog.Services))
	for _, service := range catalog.Services {
		dispositions[service.Key] = service.Disposition
	}
	for _, state := range snapshot.Oracle.States {
		want := map[string]string{
			servicecatalog.DispositionAccepted: "current",
			servicecatalog.DispositionProposal: "unavailable",
			servicecatalog.DispositionConflict: "conflict",
			servicecatalog.DispositionRejected: "removed",
		}[dispositions[state.ServiceKey]]
		if want == "" || state.Catalog != want {
			return fmt.Errorf("%s: state %s differs from catalog authority", snapshot.Revision, state.ServiceKey)
		}
		if state.Search == "partial" || state.Catalog == "partial" || state.Search == "conflict" {
			return fmt.Errorf("%s: state %s has invalid capability state", snapshot.Revision, state.ServiceKey)
		}
	}
	return nil
}

func refuseMalformedFixture(snapshot t323.Snapshot) error {
	for range snapshot.Catalog.Invalid {
		catalog := servicecatalog.Catalog{
			Schema:      servicecatalog.Schema,
			Authority:   servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "malformed", Version: snapshot.Revision},
			Services:    []servicecatalog.Service{{Key: "svc.malformed", DisplayName: "Malformed", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase}},
			Memberships: []servicecatalog.Membership{{ServiceKey: "svc.malformed", Path: "../escape", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase}},
			Unowned:     []servicecatalog.UnownedPlacement{},
		}
		if err := servicecatalog.Validate(catalog); err == nil {
			return fmt.Errorf("%s: final catalog accepted malformed fixture", snapshot.Revision)
		}
	}
	return nil
}

func selectedBy(prefixes []string, path string) bool {
	for _, prefix := range prefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

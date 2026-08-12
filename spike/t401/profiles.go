package t401

import (
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"slices"
)

const partitionItems = uint64(4_096)

var frozenControls = []fileContent{
	{Path: ".phebs/t401-profile.txt", Kind: "control", Content: []byte("t401-neutral-scale-envelope-v1\n")},
	{Path: "go.mod", Kind: "control", Content: []byte("module example.invalid/t401-neutral\n\ngo 1.26\n")},
}

func FrozenProfiles() ([]Profile, error) {
	structural, err := structuralProfile("frozen")
	if err != nil {
		return nil, err
	}
	semantic, err := semanticProfile("frozen")
	if err != nil {
		return nil, err
	}
	return []Profile{structural, semantic}, nil
}

func ProjectionProfile(kind string) (Profile, error) {
	var profile Profile
	var err error
	switch kind {
	case "structural":
		profile, err = structuralProfile("projection")
	case "semantic":
		profile, err = semanticProfile("projection")
	default:
		return Profile{}, fmt.Errorf("unknown T40.1 projection kind %q", kind)
	}
	if err != nil {
		return Profile{}, err
	}
	frozen, err := mapFrozenProfile(kind)
	if err != nil {
		return Profile{}, err
	}
	profile.ParentProfileDigest, err = ProfileDigest(frozen)
	if err != nil {
		return Profile{}, err
	}
	if err := ValidateProfile(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

// StructuralPartitionProfile is the bounded throughput fixture: exactly one
// full 4,096-record candidate partition with the frozen structural profile's
// 4,608-byte Go blobs and 512 reuse classes. It remains a synthetic projection
// bound to the frozen parent and is not a scale result.
func StructuralPartitionProfile() (Profile, error) {
	profile, err := ProjectionProfile("structural")
	if err != nil {
		return Profile{}, err
	}
	profile.Name = "structural-partition-throughput-v1"
	profile.Shape.GoFileBytes = 4_608
	if err := finishProfile(&profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func BuildEnvelope() (Envelope, error) {
	profiles, err := FrozenProfiles()
	if err != nil {
		return Envelope{}, err
	}
	oracles := make([]Oracle, 0, len(profiles))
	for _, profile := range profiles {
		oracle, err := DeriveOracle(profile)
		if err != nil {
			return Envelope{}, err
		}
		oracles = append(oracles, oracle)
	}
	envelope := Envelope{
		Schema: EnvelopeSchema, Profiles: profiles, Oracles: oracles,
		FailurePoints: FrozenFailurePoints(), BlobReaderTrial: FrozenBlobReaderTrial(),
		HistoricalDiagnostic: FrozenHistoricalDiagnostic(),
		Claims:               EnvelopeClaims{Synthetic: true, GiantAuthoringExplicit: true},
	}
	if err := ValidateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func FrozenHistoricalDiagnostic() HistoricalDiagnostic {
	return HistoricalDiagnostic{
		Artifact: "spike/large-monorepo-20260806/REPORT.md",
		SHA256:   "sha256:b4b7231603c9a67f1f09b722117aa48a49bb674acb3ffff53ce6b28b26be570b",
		Outcome:  "unknown",
		Scope:    "unfrozen_source_free_engineering_diagnostic",
	}
}

func FrozenFailurePoints() []FailurePoint {
	return []FailurePoint{
		{Name: "before_git_init", Boundary: "git_init", LeavesTarget: false},
		{Name: "after_git_init", Boundary: "git_init", LeavesTarget: false},
		{Name: "before_revision_a", Boundary: "revision_a", LeavesTarget: false},
		{Name: "after_revision_a", Boundary: "revision_a", LeavesTarget: false},
		{Name: "before_revision_b", Boundary: "revision_b", LeavesTarget: false},
		{Name: "after_revision_b", Boundary: "revision_b", LeavesTarget: false},
		{Name: "before_revision_a_return", Boundary: "revision_a_return", LeavesTarget: false},
		{Name: "after_revision_a_return", Boundary: "revision_a_return", LeavesTarget: false},
		{Name: "before_git_verify", Boundary: "git_verify", LeavesTarget: false},
		{Name: "before_metadata", Boundary: "metadata", LeavesTarget: false},
		{Name: "before_publish", Boundary: "publish", LeavesTarget: false},
	}
}

func FrozenBlobReaderTrial() BlobReaderTrial {
	return BlobReaderTrial{
		Schema:          "t401-zoekt-blob-reader-trial-v1",
		BinaryAuthority: "same_verified_go_mod_version_and_checksum",
		Fixture:         "small_structural_projection",
		Modes: []ReaderMode{
			{Name: "go_git_default", EnvironmentName: "ZOEKT_DISABLE_CATFILE_BATCH", Environment: "true", Production: true},
			{Name: "cat_file_batch_candidate", EnvironmentName: "ZOEKT_DISABLE_CATFILE_BATCH", Environment: "false", Production: false},
		},
		Queries: []string{"T401", "padding"},
		Checks: []string{
			"index_bytes_observed", "indexed_content_equal", "query_results_equal",
			"child_and_git_rss", "child_and_git_descriptors", "cancellation",
			"missing_object_classification", "corrupt_object_classification",
		},
		GiantMeasurement: "explicit_not_run", ProductionSelected: false,
	}
}

func structuralProfile(scale string) (Profile, error) {
	shape := ProfileShape{
		Cells: 10_000, EligibleGoPathsPerCell: 200, CallerPathsPerCell: 100,
		GoFileBytes: 4_608, GoBlobReuseClasses: 512, ControlFiles: uint64(len(frozenControls)),
	}
	name := StructuralProfileName
	if scale == "projection" {
		shape = ProfileShape{
			Cells: 64, EligibleGoPathsPerCell: 64, CallerPathsPerCell: 32,
			GoFileBytes: 512, GoBlobReuseClasses: 512, ControlFiles: uint64(len(frozenControls)),
		}
		name = "structural-projection-v1"
	}
	goFiles, err := multiply(shape.Cells, shape.EligibleGoPathsPerCell)
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{
		Schema: ProfileSchema, Name: name, Kind: "structural", Scale: scale,
		Seed: "t401-neutral-structural-v1", Generator: frozenGenerator(), Shape: shape,
		Families: []TemplateFamily{{
			Name: "metadata_reused_go", Plane: "go", Suffix: ".go",
			Outcome: "structural", Inputs: goFiles,
		}},
		Transitions: frozenTransitions(), Claims: frozenProfileClaims(scale),
	}
	if err := finishProfile(&profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func semanticProfile(scale string) (Profile, error) {
	shape := ProfileShape{
		Cells: 4_096, EligibleGoPathsPerCell: 64, CallerPathsPerCell: 32,
		GoFileBytes: 512, GoBlobReuseClasses: 262_144, IDLFileBytes: 384,
		ControlFiles: uint64(len(frozenControls)),
	}
	goFamilyInputs, idlFamilyInputs := uint64(65_536), uint64(8_192)
	name := SemanticProfileName
	if scale == "projection" {
		shape = ProfileShape{
			Cells: 8, EligibleGoPathsPerCell: 4, CallerPathsPerCell: 2,
			GoFileBytes: 512, GoBlobReuseClasses: 32, IDLFileBytes: 384,
			ControlFiles: uint64(len(frozenControls)),
		}
		goFamilyInputs, idlFamilyInputs = 8, 2
		name = "semantic-projection-v1"
	}
	profile := Profile{
		Schema: ProfileSchema, Name: name, Kind: "semantic", Scale: scale,
		Seed: "t401-neutral-semantic-v1", Generator: frozenGenerator(), Shape: shape,
		Families: []TemplateFamily{
			{Name: "go_rpc_resolved", Plane: "go", Suffix: ".go", Outcome: "supported", Inputs: goFamilyInputs, RecordsPerInput: 1, CanonicalBytesPerInput: 757},
			{Name: "go_kafka_literal", Plane: "go", Suffix: ".go", Outcome: "supported", Inputs: goFamilyInputs, RecordsPerInput: 1, CanonicalBytesPerInput: 1_011},
			{Name: "go_dynamic_call", Plane: "go", Suffix: ".go", Outcome: "gap", Reason: "dynamic_receiver", Inputs: goFamilyInputs, RecordsPerInput: 1, GapsPerInput: 1, CanonicalBytesPerInput: 816},
			{Name: "go_dynamic_topic", Plane: "go", Suffix: ".go", Outcome: "gap", Reason: "unresolved-ident", Inputs: goFamilyInputs, RecordsPerInput: 1, GapsPerInput: 1, CanonicalBytesPerInput: 997},
			{Name: "proto_supported", Plane: "idl", Suffix: ".proto", Outcome: "supported", Inputs: idlFamilyInputs, RecordsPerInput: 1, StagingRowsPerInput: 2, CanonicalBytesPerInput: 731},
			{Name: "proto_unresolved_type", Plane: "idl", Suffix: ".proto", Outcome: "gap", Reason: "DECLARATION_NOT_FOUND", Inputs: idlFamilyInputs, RecordsPerInput: 2, GapsPerInput: 1, StagingRowsPerInput: 4, CanonicalBytesPerInput: 1_726},
			{Name: "thrift_supported", Plane: "idl", Suffix: ".thrift", Outcome: "supported", Inputs: idlFamilyInputs, RecordsPerInput: 1, StagingRowsPerInput: 2, CanonicalBytesPerInput: 739},
			{Name: "thrift_typedef_cycle", Plane: "idl", Suffix: ".thrift", Outcome: "gap", Reason: "TYPEDEF_CHAIN_LIMIT", Inputs: idlFamilyInputs, RecordsPerInput: 2, GapsPerInput: 1, StagingRowsPerInput: 4, CanonicalBytesPerInput: 1_587},
		},
		Adapters:    semanticAdapters(),
		Transitions: frozenTransitions(), Claims: frozenProfileClaims(scale),
	}
	if err := finishProfile(&profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func finishProfile(profile *Profile) error {
	aggregate, err := deriveAggregate(*profile)
	if err != nil {
		return err
	}
	profile.Aggregate = aggregate
	profile.Adapters = adaptersFor(profile.Kind)
	profile.Dimensions = dimensionsFor(aggregate, profile.Kind)
	profile.ExpectedOutcomes = expectationsFor(*profile)
	profile.Lanes = lanesFor(*profile)
	if profile.Scale == "projection" && profile.ParentProfileDigest == "" {
		return nil
	}
	return ValidateProfile(*profile)
}

func ValidateProfile(profile Profile) error {
	if profile.Schema != ProfileSchema || (profile.Kind != "structural" && profile.Kind != "semantic") ||
		(profile.Scale != "frozen" && profile.Scale != "projection") || profile.Name == "" || profile.Seed == "" ||
		profile.Generator != frozenGenerator() || profile.Shape.Cells == 0 ||
		profile.Shape.EligibleGoPathsPerCell == 0 || profile.Shape.CallerPathsPerCell == 0 ||
		profile.Shape.CallerPathsPerCell > profile.Shape.EligibleGoPathsPerCell ||
		profile.Shape.GoFileBytes == 0 || profile.Shape.GoBlobReuseClasses == 0 ||
		profile.Shape.ControlFiles != uint64(len(frozenControls)) || len(profile.Families) == 0 ||
		!slices.Equal(profile.Transitions, frozenTransitions()) || !validProfileClaims(profile.Claims, profile.Scale) {
		return errors.New("profile envelope is invalid")
	}
	if !reflect.DeepEqual(profile.Adapters, adaptersFor(profile.Kind)) {
		return errors.New("profile adapter expectations differ from the frozen set")
	}
	if !reflect.DeepEqual(profile.Lanes, lanesFor(profile)) {
		return errors.New("profile lane projections differ from the frozen set")
	}
	if profile.Scale == "frozen" && profile.ParentProfileDigest != "" ||
		profile.Scale == "projection" && !validDigest(profile.ParentProfileDigest) {
		return errors.New("profile parent binding is invalid")
	}
	seen := map[string]struct{}{}
	var goInputs, idlInputs uint64
	for _, family := range profile.Families {
		if family.Name == "" || family.Inputs == 0 ||
			(family.Plane != "go" && family.Plane != "idl") ||
			(family.Outcome != "structural" && family.Outcome != "supported" && family.Outcome != "gap") ||
			family.Outcome == "gap" && family.Reason == "" || family.Outcome != "gap" && family.Reason != "" {
			return fmt.Errorf("template family %q is invalid", family.Name)
		}
		if family.Outcome == "structural" && (family.RecordsPerInput != 0 || family.GapsPerInput != 0 ||
			family.StagingRowsPerInput != 0 || family.CanonicalBytesPerInput != 0) ||
			family.Outcome == "supported" && (family.RecordsPerInput == 0 || family.GapsPerInput != 0 || family.CanonicalBytesPerInput == 0) ||
			family.Outcome == "gap" && (family.RecordsPerInput == 0 || family.GapsPerInput == 0 || family.CanonicalBytesPerInput == 0) ||
			family.Plane == "go" && family.StagingRowsPerInput != 0 ||
			family.Plane == "idl" && family.StagingRowsPerInput != family.RecordsPerInput*2 {
			return fmt.Errorf("template family %q record cardinality is invalid", family.Name)
		}
		if _, duplicate := seen[family.Name]; duplicate {
			return fmt.Errorf("duplicate template family %q", family.Name)
		}
		seen[family.Name] = struct{}{}
		var err error
		if family.Plane == "go" {
			if family.Suffix != ".go" {
				return fmt.Errorf("go family %q has invalid suffix", family.Name)
			}
			goInputs, err = add(goInputs, family.Inputs)
		} else {
			if family.Suffix != ".proto" && family.Suffix != ".thrift" {
				return fmt.Errorf("IDL family %q has invalid suffix", family.Name)
			}
			idlInputs, err = add(idlInputs, family.Inputs)
		}
		if err != nil {
			return err
		}
	}
	wantGo, err := multiply(profile.Shape.Cells, profile.Shape.EligibleGoPathsPerCell)
	if err != nil || goInputs != wantGo {
		return errors.New("template families do not cover every eligible Go input")
	}
	if profile.Kind == "structural" && (idlInputs != 0 || profile.Shape.IDLFileBytes != 0) ||
		profile.Kind == "semantic" && (idlInputs == 0 || profile.Shape.IDLFileBytes == 0) {
		return errors.New("profile IDL shape is invalid")
	}
	want, err := deriveAggregate(profile)
	if err != nil {
		return err
	}
	if profile.Aggregate != want || !slices.Equal(profile.Dimensions, dimensionsFor(want, profile.Kind)) ||
		!reflect.DeepEqual(profile.ExpectedOutcomes, expectationsFor(profile)) {
		return errors.New("profile aggregates or dimensions differ from the frozen shape")
	}
	for _, dimension := range profile.Dimensions {
		if dimension.Name == "" || dimension.Baseline > dimension.Admitted || dimension.Admitted == 0 {
			return fmt.Errorf("dimension %q is invalid", dimension.Name)
		}
		if err := CheckDimension(dimension.Name, dimension.Baseline, dimension.Admitted); err != nil {
			return err
		}
	}
	if profile.Scale == "frozen" {
		switch profile.Kind {
		case "structural":
			if profile.Name != StructuralProfileName || want.EligibleGoFiles != 2_000_000 ||
				want.PhysicalOwners != 2_000_002 || want.RegularFiles != 2_000_002 ||
				want.RepositoryLanePlacements != 2_000_000 ||
				want.CallerLanePlacements != 1_000_000 || want.UniqueGoBlobs != 512 ||
				want.DeclaredRepositoryGoBytes <= 8<<30 {
				return errors.New("frozen structural envelope is incomplete")
			}
		case "semantic":
			if profile.Name != SemanticProfileName || want.UniqueGoBlobs != 262_144 ||
				want.IDLCandidates != 32_768 {
				return errors.New("frozen semantic envelope is incomplete")
			}
		}
	}
	return nil
}

func semanticAdapters() []AdapterExpectation {
	return []AdapterExpectation{
		{Family: "go_rpc_resolved", Adapter: "caller", Protocol: "grpc", ImportPath: "example.invalid/rpc", ClientType: "Client", Method: "Ping", Operation: "/neutral.Service/Ping", State: "resolved"},
		{Family: "go_kafka_literal", Adapter: "kafka", Plane: "producer", State: "resolved"},
		{Family: "go_dynamic_call", Adapter: "caller", Protocol: "grpc", ImportPath: "example.invalid/rpc", ClientType: "Client", Method: "Ping", Operation: "/neutral.Service/Ping", State: "dynamic", Reason: "dynamic_receiver"},
		{Family: "go_dynamic_topic", Adapter: "kafka", Plane: "producer", State: "dynamic", Reason: "unresolved-ident"},
	}
}

func adaptersFor(kind string) []AdapterExpectation {
	if kind == "semantic" {
		return semanticAdapters()
	}
	return []AdapterExpectation{}
}

func lanesFor(profile Profile) []LaneProjection {
	repository := LaneProjection{
		Name: "repository", Selection: "all_eligible_go_paths_v1", Unit: "path_placement",
		Placements:                profile.Aggregate.RepositoryLanePlacements,
		UniqueGoBlobs:             profile.Aggregate.UniqueGoBlobs,
		DeclaredBytes:             profile.Aggregate.DeclaredRepositoryGoBytes,
		LogicalBytes:              profile.Aggregate.DeclaredRepositoryGoBytes,
		ProductionCandidateParity: true,
	}
	callerUnique := profile.Aggregate.CallerLanePlacements
	if profile.Kind == "structural" {
		seen := make([]bool, profile.Shape.GoBlobReuseClasses)
		for cell := uint64(0); cell < profile.Shape.Cells; cell++ {
			for offset := uint64(0); offset < profile.Shape.CallerPathsPerCell; offset++ {
				ordinal := cell*profile.Shape.EligibleGoPathsPerCell + offset
				seen[ordinal%profile.Shape.GoBlobReuseClasses] = true
			}
		}
		callerUnique = 0
		for _, present := range seen {
			callerUnique += boolUint(present)
		}
	}
	callerBytes := profile.Aggregate.CallerLanePlacements * profile.Shape.GoFileBytes
	caller := LaneProjection{
		Name: "caller", Selection: "synthetic_first_offsets_per_cell_v1", Unit: "path_placement",
		Placements: profile.Aggregate.CallerLanePlacements, UniqueGoBlobs: callerUnique,
		DeclaredBytes: callerBytes, LogicalBytes: callerBytes,
		ProductionCandidateParity: false,
	}
	return []LaneProjection{repository, caller}
}

func expectationsFor(profile Profile) []StageExpectation {
	maxPlacements := ceilingDivision(profile.Aggregate.EligibleGoFiles, profile.Aggregate.UniqueGoBlobs)
	planning := StageExpectation{
		Stage: "source_partition_planning", Outcome: "pass", Class: "admitted",
		Dimension: "blob_placements", Observed: maxPlacements, Limit: 4_096,
	}
	observation := StageExpectation{
		Stage: "observation_publication", Outcome: "pass", Class: "admitted",
		Dimension: "generation_records", Observed: profile.Aggregate.UniqueGoBlobs, Limit: 250_000,
	}
	if observation.Observed > observation.Limit {
		observation.Outcome = "refused"
		observation.Class = "dimension_limit"
	}
	result := []StageExpectation{planning, observation}
	notRunClass := "not_executed_by_t401_envelope"
	if observation.Outcome == "refused" {
		notRunClass = "blocked_by_observation_refusal"
	}
	for _, stage := range []string{
		"candidate_strict_open", "domain_inventory", "extractor_execution", "evidence_staging", "final_publication",
	} {
		result = append(result, StageExpectation{
			Stage: stage, Outcome: "not_run", Class: notRunClass, Dimension: "not_observed",
		})
	}
	return result
}

func deriveAggregate(profile Profile) (Aggregate, error) {
	goFiles, err := multiply(profile.Shape.Cells, profile.Shape.EligibleGoPathsPerCell)
	if err != nil {
		return Aggregate{}, err
	}
	caller, err := multiply(profile.Shape.Cells, profile.Shape.CallerPathsPerCell)
	if err != nil {
		return Aggregate{}, err
	}
	var idl, observationRecords, observationGaps, extractorFacts, extractorGaps uint64
	var extractorRows, observationBytes, extractorBytes uint64
	for _, family := range profile.Families {
		if family.Plane == "idl" {
			idl, err = add(idl, family.Inputs)
			if err != nil {
				return Aggregate{}, err
			}
		}
		familyFacts, multiplyErr := multiply(family.Inputs, family.RecordsPerInput)
		if multiplyErr != nil {
			return Aggregate{}, multiplyErr
		}
		familyGaps, multiplyErr := multiply(family.Inputs, family.GapsPerInput)
		if multiplyErr != nil {
			return Aggregate{}, multiplyErr
		}
		familyRows, multiplyErr := multiply(family.Inputs, family.StagingRowsPerInput)
		if multiplyErr != nil {
			return Aggregate{}, multiplyErr
		}
		familyBytes, multiplyErr := multiply(family.Inputs, family.CanonicalBytesPerInput)
		if multiplyErr != nil {
			return Aggregate{}, multiplyErr
		}
		if family.Plane == "go" {
			observationRecords, err = add(observationRecords, familyFacts)
			if err == nil {
				observationGaps, err = add(observationGaps, familyGaps)
			}
			if err == nil {
				observationBytes, err = add(observationBytes, familyBytes)
			}
		} else {
			extractorFacts, err = add(extractorFacts, familyFacts)
			if err == nil {
				extractorGaps, err = add(extractorGaps, familyGaps)
			}
			if err == nil {
				extractorRows, err = add(extractorRows, familyRows)
			}
			if err == nil {
				extractorBytes, err = add(extractorBytes, familyBytes)
			}
		}
		if err != nil {
			return Aggregate{}, err
		}
	}
	eligibleOwners, err := add(goFiles, idl)
	if err != nil {
		return Aggregate{}, err
	}
	regular, err := add(eligibleOwners, profile.Shape.ControlFiles)
	if err != nil {
		return Aggregate{}, err
	}
	declared, err := multiply(goFiles, profile.Shape.GoFileBytes)
	if err != nil {
		return Aggregate{}, err
	}
	idlBytes, err := multiply(idl, profile.Shape.IDLFileBytes)
	if err != nil {
		return Aggregate{}, err
	}
	logical, err := add(declared, idlBytes)
	if err != nil {
		return Aggregate{}, err
	}
	logical, err = add(logical, controlBytes())
	if err != nil {
		return Aggregate{}, err
	}
	partitions := ceilingDivision(eligibleOwners, partitionItems)
	return Aggregate{
		PhysicalOwners: regular, RegularFiles: regular, EligibleGoFiles: goFiles,
		IDLCandidates: idl, ControlFiles: profile.Shape.ControlFiles,
		RepositoryLanePlacements: goFiles, CallerLanePlacements: caller,
		UniqueGoBlobs: profile.Shape.GoBlobReuseClasses, UniqueIDLBlobs: idl,
		DeclaredRepositoryGoBytes: declared, LogicalSourceBytes: logical,
		ControlBytes: controlBytes(), ModeledDomainPartitions: partitions, ModeledDomainMembers: partitions,
		DomainPartitionModel: "neutral_input_chunks_4096_v1",
		ObservationRecords:   observationRecords, ObservationGaps: observationGaps,
		ExtractorFacts: extractorFacts, ExtractorGapFacts: extractorGaps, ExtractorStagingRows: extractorRows,
		CanonicalObservationBytes: observationBytes, CanonicalExtractorBytes: extractorBytes,
		CanonicalResultByteModel: "sum_per_input_sourceobservation_or_sdk_fact_array_v1",
	}, nil
}

func dimensionsFor(aggregate Aggregate, kind string) []Dimension {
	uniqueAdmitted := aggregate.UniqueGoBlobs
	if kind == "structural" {
		uniqueAdmitted++ // B adds one temporary blob while retaining every A template.
	}
	return []Dimension{
		{Name: "physical_owners", Baseline: aggregate.PhysicalOwners, Admitted: aggregate.PhysicalOwners},
		{Name: "repository_lane_placements", Baseline: aggregate.RepositoryLanePlacements, Admitted: aggregate.RepositoryLanePlacements},
		{Name: "caller_lane_placements", Baseline: aggregate.CallerLanePlacements, Admitted: aggregate.CallerLanePlacements},
		{Name: "unique_go_blobs", Baseline: aggregate.UniqueGoBlobs, Admitted: uniqueAdmitted},
		{Name: "declared_repository_go_bytes", Baseline: aggregate.DeclaredRepositoryGoBytes, Admitted: aggregate.DeclaredRepositoryGoBytes},
		{Name: "logical_source_bytes", Baseline: aggregate.LogicalSourceBytes, Admitted: aggregate.LogicalSourceBytes},
		{Name: "idl_candidates", Baseline: aggregate.IDLCandidates, Admitted: max(uint64(1), aggregate.IDLCandidates)},
		{Name: "control_files", Baseline: aggregate.ControlFiles, Admitted: aggregate.ControlFiles},
		{Name: "modeled_domain_partitions", Baseline: aggregate.ModeledDomainPartitions, Admitted: aggregate.ModeledDomainPartitions},
		{Name: "modeled_domain_members", Baseline: aggregate.ModeledDomainMembers, Admitted: aggregate.ModeledDomainMembers},
		{Name: "observation_records", Baseline: aggregate.ObservationRecords, Admitted: max(uint64(1), aggregate.ObservationRecords)},
		{Name: "observation_gap_classifications", Baseline: aggregate.ObservationGaps, Admitted: max(uint64(1), aggregate.ObservationGaps)},
		{Name: "extractor_facts", Baseline: aggregate.ExtractorFacts, Admitted: max(uint64(1), aggregate.ExtractorFacts)},
		{Name: "extractor_gap_facts", Baseline: aggregate.ExtractorGapFacts, Admitted: max(uint64(1), aggregate.ExtractorGapFacts)},
		{Name: "extractor_staging_rows", Baseline: aggregate.ExtractorStagingRows, Admitted: max(uint64(1), aggregate.ExtractorStagingRows)},
		{Name: "canonical_observation_bytes", Baseline: aggregate.CanonicalObservationBytes, Admitted: max(uint64(1), aggregate.CanonicalObservationBytes)},
		{Name: "canonical_extractor_bytes", Baseline: aggregate.CanonicalExtractorBytes, Admitted: max(uint64(1), aggregate.CanonicalExtractorBytes)},
		{Name: "transition_changed_files", Baseline: 1, Admitted: 1},
	}
}

func frozenGenerator() GeneratorIdentity {
	return GeneratorIdentity{
		Name: "phebs-t401-fast-import-author", Version: "1.0.0", GitObjectFormat: "sha1",
		PathOrder: "ascii_lexicographic", PathLayout: "control_then_kind_bucket_ordinal_v1",
		TemplatePolicy:  "fixed_width_family_semantics_with_comment_padding_v1",
		BlobReusePolicy: "structural_ordinal_modulo_classes_semantic_unique_v1",
		Timestamp:       1_785_974_400,
		AuthorName:      "phebs-t401-fixture-author", AuthorEmail: "fixture@example.invalid",
	}
}

func frozenTransitions() []Transition {
	return []Transition{
		{Revision: "a", Posture: "baseline", ChangedRegularFiles: 0, TreeRelation: "baseline"},
		{Revision: "b", Parent: "a", Posture: "bounded_delta", ChangedRegularFiles: 1, TreeRelation: "different_from_a"},
		{Revision: "a-return", Parent: "b", Posture: "content_return", ChangedRegularFiles: 1, TreeRelation: "equal_to_a"},
	}
}

func frozenProfileClaims(scale string) ProfileClaims {
	return ProfileClaims{Synthetic: true, ExplicitAuthoringOnly: scale == "frozen"}
}

func validProfileClaims(claims ProfileClaims, scale string) bool {
	return claims.Synthetic && claims.ExplicitAuthoringOnly == (scale == "frozen") &&
		!claims.EstablishesTargetSLO && !claims.EstablishesAccuracy &&
		!claims.QualifiesSupportedScale && !claims.AuthorizesRelease
}

func mapFrozenProfile(kind string) (Profile, error) {
	profiles, err := FrozenProfiles()
	if err != nil {
		return Profile{}, err
	}
	for _, profile := range profiles {
		if profile.Kind == kind {
			return profile, nil
		}
	}
	return Profile{}, errors.New("frozen profile kind is absent")
}

func failurePointNames() []string {
	points := FrozenFailurePoints()
	names := make([]string, 0, len(points))
	for _, point := range points {
		names = append(names, point.Name)
	}
	return names
}

func validFailurePoint(name string) bool {
	if name == "" {
		return true
	}
	return slices.Contains(failurePointNames(), name)
}

func equalBlobReaderTrial(left, right BlobReaderTrial) bool {
	return reflect.DeepEqual(left, right)
}

func controlBytes() uint64 {
	var total uint64
	for _, file := range frozenControls {
		total += uint64(len(file.Content))
	}
	return total
}

func multiply(left, right uint64) (uint64, error) {
	high, low := bits.Mul64(left, right)
	if high != 0 {
		return 0, errors.New("T40.1 profile arithmetic overflow")
	}
	return low, nil
}

func add(left, right uint64) (uint64, error) {
	value, carry := bits.Add64(left, right, 0)
	if carry != 0 {
		return 0, errors.New("T40.1 profile arithmetic overflow")
	}
	return value, nil
}

func ceilingDivision(value, divisor uint64) uint64 {
	return value/divisor + boolUint(value%divisor != 0)
}

func boolUint(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

type fileContent struct {
	Path    string
	Kind    string
	Family  string
	Content []byte
}

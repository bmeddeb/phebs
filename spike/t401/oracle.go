package t401

import (
	"errors"
	"fmt"
	"slices"
)

// DeriveOracle calculates expected corpus and transition results from only the
// closed profile input. It deliberately does not accept a Phebs generation,
// Git manifest, index, extraction output, or publication receipt.
func DeriveOracle(profile Profile) (Oracle, error) {
	if err := ValidateProfile(profile); err != nil {
		return Oracle{}, err
	}
	digest, err := ProfileDigest(profile)
	if err != nil {
		return Oracle{}, err
	}
	aggregate, err := deriveOracleAggregate(profile)
	if err != nil {
		return Oracle{}, err
	}
	families := make([]OracleFamily, 0, len(profile.Families))
	for _, family := range profile.Families {
		expectation := OracleFamily{
			Name: family.Name, Inputs: family.Inputs, Outcome: family.Outcome, Reason: family.Reason,
		}
		expectation.Facts, err = multiply(family.Inputs, family.RecordsPerInput)
		if err != nil {
			return Oracle{}, err
		}
		expectation.Gaps, err = multiply(family.Inputs, family.GapsPerInput)
		if err != nil {
			return Oracle{}, err
		}
		expectation.StagingRows, err = multiply(family.Inputs, family.StagingRowsPerInput)
		if err != nil {
			return Oracle{}, err
		}
		expectation.CanonicalBytes, err = multiply(family.Inputs, family.CanonicalBytesPerInput)
		if err != nil {
			return Oracle{}, err
		}
		families = append(families, expectation)
	}
	bUnique := aggregate.UniqueGoBlobs
	if profile.Kind == "structural" {
		bUnique++
	}
	oracle := Oracle{
		Schema: OracleSchema, Profile: profile.Name, ProfileDigest: digest,
		Aggregate: aggregate, Dimensions: oracleDimensions(aggregate, profile.Kind),
		ExpectedOutcomes: oracleExpectations(aggregate), Families: families,
		Revisions: []OracleRevision{
			{Revision: "a", RegularFiles: aggregate.RegularFiles, UniqueGoBlobs: aggregate.UniqueGoBlobs, ExpectedTree: "baseline"},
			{Revision: "b", RegularFiles: aggregate.RegularFiles, UniqueGoBlobs: bUnique, ChangedFiles: 1, ExpectedTree: "different_from_a"},
			{Revision: "a-return", RegularFiles: aggregate.RegularFiles, UniqueGoBlobs: aggregate.UniqueGoBlobs, ChangedFiles: 1, ExpectedTree: "equal_to_a"},
		},
	}
	for _, point := range FrozenFailurePoints() {
		oracle.Failures = append(oracle.Failures, OracleFailure{
			Point: point.Name, Class: "injected", Cleanup: "final_target_absent_resumable_or_unstarted",
		})
	}
	if err := ValidateOracle(oracle, profile); err != nil {
		return Oracle{}, err
	}
	return oracle, nil
}

func ValidateOracle(oracle Oracle, profile Profile) error {
	digest, err := ProfileDigest(profile)
	if err != nil {
		return err
	}
	wantAggregate, err := deriveOracleAggregate(profile)
	if err != nil {
		return err
	}
	wantDimensions := oracleDimensions(wantAggregate, profile.Kind)
	wantOutcomes := oracleExpectations(wantAggregate)
	if profile.Aggregate != wantAggregate ||
		!slices.Equal(profile.Dimensions, wantDimensions) ||
		!slices.Equal(profile.ExpectedOutcomes, wantOutcomes) {
		return errors.New("profile and independent oracle derivations differ")
	}
	if oracle.Schema != OracleSchema || oracle.Profile != profile.Name || oracle.ProfileDigest != digest ||
		oracle.Aggregate != wantAggregate || !slices.Equal(oracle.Dimensions, wantDimensions) ||
		!slices.Equal(oracle.ExpectedOutcomes, wantOutcomes) ||
		len(oracle.Families) != len(profile.Families) || len(oracle.Revisions) != 3 ||
		len(oracle.Failures) != len(FrozenFailurePoints()) {
		return errors.New("independent oracle envelope is invalid")
	}
	var observationRecords, observationGaps, extractorFacts, extractorGaps uint64
	var extractorRows, observationBytes, extractorBytes uint64
	for index, family := range oracle.Families {
		input := profile.Families[index]
		if family.Name != input.Name || family.Inputs != input.Inputs || family.Outcome != input.Outcome ||
			family.Reason != input.Reason || family.Facts != input.Inputs*input.RecordsPerInput ||
			family.Gaps != input.Inputs*input.GapsPerInput ||
			family.StagingRows != input.Inputs*input.StagingRowsPerInput ||
			family.CanonicalBytes != input.Inputs*input.CanonicalBytesPerInput ||
			family.Outcome == "structural" && (family.Facts != 0 || family.Gaps != 0 ||
				family.StagingRows != 0 || family.CanonicalBytes != 0) {
			return fmt.Errorf("oracle family %q is invalid", family.Name)
		}
		if input.Plane == "go" {
			observationRecords += family.Facts
			observationGaps += family.Gaps
			observationBytes += family.CanonicalBytes
		} else {
			extractorFacts += family.Facts
			extractorGaps += family.Gaps
			extractorRows += family.StagingRows
			extractorBytes += family.CanonicalBytes
		}
	}
	if observationRecords != oracle.Aggregate.ObservationRecords || observationGaps != oracle.Aggregate.ObservationGaps ||
		extractorFacts != oracle.Aggregate.ExtractorFacts || extractorGaps != oracle.Aggregate.ExtractorGapFacts ||
		extractorRows != oracle.Aggregate.ExtractorStagingRows ||
		observationBytes != oracle.Aggregate.CanonicalObservationBytes || extractorBytes != oracle.Aggregate.CanonicalExtractorBytes ||
		profile.Kind == "semantic" && (observationGaps == 0 || extractorGaps == 0) {
		return errors.New("oracle family results differ from aggregate results")
	}
	wantRevisions := []OracleRevision{
		{Revision: "a", RegularFiles: wantAggregate.RegularFiles, UniqueGoBlobs: wantAggregate.UniqueGoBlobs, ExpectedTree: "baseline"},
		{Revision: "b", RegularFiles: wantAggregate.RegularFiles, UniqueGoBlobs: wantAggregate.UniqueGoBlobs, ChangedFiles: 1, ExpectedTree: "different_from_a"},
		{Revision: "a-return", RegularFiles: wantAggregate.RegularFiles, UniqueGoBlobs: wantAggregate.UniqueGoBlobs, ChangedFiles: 1, ExpectedTree: "equal_to_a"},
	}
	if profile.Kind == "structural" {
		wantRevisions[1].UniqueGoBlobs++
	}
	if !slices.Equal(oracle.Revisions, wantRevisions) {
		return errors.New("oracle revision expectations are invalid")
	}
	for index, failure := range oracle.Failures {
		if failure.Point != FrozenFailurePoints()[index].Name || failure.Class != "injected" ||
			failure.Cleanup != "final_target_absent_resumable_or_unstarted" {
			return errors.New("oracle failure expectations are invalid")
		}
	}
	return nil
}

func oracleExpectations(aggregate Aggregate) []StageExpectation {
	maximumPlacements := ceilingDivision(aggregate.EligibleGoFiles, aggregate.UniqueGoBlobs)
	planning := StageExpectation{
		Stage: "source_partition_planning", Outcome: "pass", Class: "admitted",
		Dimension: "blob_placements", Observed: maximumPlacements, Limit: 4_096,
	}
	observation := StageExpectation{
		Stage: "observation_publication", Outcome: "pass", Class: "admitted",
		Dimension: "generation_records", Observed: aggregate.UniqueGoBlobs, Limit: 250_000,
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

// deriveOracleAggregate is intentionally separate from the author's profile
// accumulator. The duplicate arithmetic is the independent check: changing an
// author counter or file traversal cannot silently change this expectation.
func deriveOracleAggregate(profile Profile) (Aggregate, error) {
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
	regular, err := add(eligibleOwners, uint64(len(frozenControls)))
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
		IDLCandidates: idl, ControlFiles: uint64(len(frozenControls)),
		RepositoryLanePlacements: goFiles, CallerLanePlacements: caller,
		UniqueGoBlobs: profile.Shape.GoBlobReuseClasses, UniqueIDLBlobs: idl,
		DeclaredRepositoryGoBytes: declared, LogicalSourceBytes: logical, ControlBytes: controlBytes(),
		ModeledDomainPartitions: partitions, ModeledDomainMembers: partitions,
		DomainPartitionModel: "neutral_input_chunks_4096_v1",
		ObservationRecords:   observationRecords, ObservationGaps: observationGaps,
		ExtractorFacts: extractorFacts, ExtractorGapFacts: extractorGaps, ExtractorStagingRows: extractorRows,
		CanonicalObservationBytes: observationBytes, CanonicalExtractorBytes: extractorBytes,
		CanonicalResultByteModel: "sum_per_input_sourceobservation_or_sdk_fact_array_v1",
	}, nil
}

func oracleDimensions(aggregate Aggregate, kind string) []Dimension {
	uniqueAdmitted := aggregate.UniqueGoBlobs
	if kind == "structural" {
		uniqueAdmitted++
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

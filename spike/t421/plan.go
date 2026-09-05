package t421

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/resolverinput"
)

const (
	frozenDate = "2026-09-02"
)

func BuildPlan(sourceCommit string) (Plan, error) {
	plan, err := buildPlanV1(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	plan.Schema = PlanV2Schema
	if err := applyContractCorrection(&plan); err != nil {
		return Plan{}, err
	}
	if err := validatePlan(plan, &plan.Revisions); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// buildPlanV1 preserves the superseded contract and its retained canonical bytes.
// New authoring uses BuildPlan; v1 remains available only for exact validation.
func buildPlanV1(sourceCommit string) (Plan, error) {
	if !validCommit(sourceCommit) {
		return Plan{}, errors.New("T42.1 plan requires one exact source commit")
	}
	corpus, err := BuildCombinedCorpus()
	if err != nil {
		return Plan{}, err
	}
	oracle, err := BuildIndependentOracle()
	if err != nil {
		return Plan{}, err
	}
	authoredOracle, err := deriveAuthoredOracle(corpus.Catalog)
	if err != nil {
		return Plan{}, err
	}
	if authoredOracle.Catalog != oracle.Catalog ||
		authoredOracle.Memberships != oracle.Memberships ||
		authoredOracle.Placements != oracle.Placements ||
		authoredOracle.UnownedPrefixes != oracle.UnownedPrefixes ||
		authoredOracle.ServiceQueries != oracle.ServiceQueries {
		return Plan{}, errors.New("T42.1 authored and independent logical oracles differ")
	}
	bCatalog, err := independentCatalogIdentity(combinedServices / 2)
	if err != nil {
		return Plan{}, err
	}
	for index, revision := range corpus.LogicalRevisions {
		wantCatalog := oracle.Catalog
		if index == 1 {
			wantCatalog = bCatalog
		}
		if revision.Catalog != wantCatalog || revision.Memberships != oracle.Memberships ||
			revision.Placements != oracle.Placements || revision.UnownedPrefixes != oracle.UnownedPrefixes ||
			revision.ServiceQueries != oracle.ServiceQueries {
			return Plan{}, errors.New("T42.1 authored and independent logical revision states differ")
		}
	}
	authoredRelationships, err := authoredRelationshipFamilies()
	if err != nil {
		return Plan{}, err
	}
	if !reflect.DeepEqual(authoredRelationships, oracle.Relationships) {
		return Plan{}, errors.New("T42.1 authored and independent relationship oracles differ")
	}
	revisions, err := frozenRevisionHistory(corpus.Profile, corpus.LogicalRevisions)
	if err != nil {
		return Plan{}, err
	}
	readerProbe, err := frozenReaderProbe()
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Schema: PlanSchema, FrozenOn: frozenDate, SourceCommit: sourceCommit,
		Inputs: frozenInputs(), Profile: corpus.Profile, Oracle: oracle,
		Revisions:       revisions,
		PhaseStates:     frozenPhaseStates(),
		PhaseOrder:      frozenPhaseOrder(),
		PhaseDeadlines:  frozenPhaseDeadlines(),
		FailurePoints:   frozenFailurePoints(),
		ReaderProbe:     readerProbe,
		SafetyEnvelope:  frozenSafetyEnvelope(),
		WorkEnvelope:    frozenWorkEnvelope(corpus.Profile),
		MeterPolicy:     frozenMeterPolicy(),
		ToolPolicy:      frozenToolPolicy(),
		SealPolicy:      frozenSealPolicy(),
		StopRules:       frozenStopRules(),
		Teardown:        frozenTeardownRule(),
		ReceiptContract: frozenReceiptContract(),
		Claims:          frozenClaims(),
	}
	if err := validatePlan(plan, &revisions); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func ValidateFrozenPlan(plan Plan) error {
	var build func(string) (Plan, error)
	switch plan.Schema {
	case PlanSchema:
		build = buildPlanV1
	case PlanV2Schema:
		build = BuildPlan
	case PlanV3Schema:
		build = BuildPlanV3
	default:
		return errors.New("T42.1 plan schema is unknown")
	}
	want, err := build(plan.SourceCommit)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(plan, want) {
		return errors.New("T42.1 plan differs from the frozen generators")
	}
	return nil
}

func ValidatePlan(plan Plan) error {
	return validatePlan(plan, nil)
}

func validatePlan(plan Plan, knownRevisions *RevisionHistory) error {
	if !knownPlanSchema(plan.Schema) || plan.FrozenOn != frozenDate || !validCommit(plan.SourceCommit) {
		return errors.New("T42.1 plan identity is invalid")
	}
	if err := validateInputBindings(plan.Inputs); err != nil {
		return err
	}
	if !reflect.DeepEqual(plan.Inputs, frozenInputs()) {
		return errors.New("T42.1 input bindings differ from the frozen authorities")
	}
	if err := validateCombinedProfile(plan.Profile); err != nil {
		return err
	}
	if err := validateOracle(plan.Oracle, plan.Profile, plan.Schema); err != nil {
		return err
	}
	wantRevisions := RevisionHistory{}
	if knownRevisions == nil {
		var err error
		wantRevisions, err = revisionHistoryForScope(plan.Profile, plan.Revisions.Logical, correctedPlanSemantics(plan.Schema))
		if err != nil {
			return err
		}
	} else {
		wantRevisions = *knownRevisions
	}
	if len(plan.Revisions.Physical) != 3 ||
		plan.Revisions.SourceRecipe != wantRevisions.SourceRecipe ||
		!reflect.DeepEqual(plan.Revisions.Physical, wantRevisions.Physical) ||
		len(plan.Revisions.Logical) != 3 ||
		plan.Revisions.Logical[0].Name != "a" || plan.Revisions.Logical[1].Name != "b" ||
		plan.Revisions.Logical[2].Name != "a-return" ||
		plan.Revisions.Logical[1].ChangedLogicalServices != 1 ||
		plan.Revisions.Logical[0].SemanticSHA256 != plan.Revisions.Logical[2].SemanticSHA256 ||
		plan.Revisions.Logical[0].CatalogLogicalSHA256 == plan.Revisions.Logical[2].CatalogLogicalSHA256 {
		return errors.New("T42.1 revision history is not the frozen A-B-A shape")
	}
	supportedInputs := plan.Profile.Pipeline.SupportedGoFiles + plan.Profile.Pipeline.SupportedIDLFiles
	if correctedPlanSemantics(plan.Schema) {
		supportedInputs = plan.Profile.Pipeline.SupportedGoFiles
	}
	for _, revision := range plan.Revisions.Physical {
		if !validSetIdentity(revision.ExpectedTreeInventory) ||
			revision.ExpectedTreeInventory.Records != plan.Profile.Physical.CombinedRegularFiles ||
			!validSetIdentity(revision.ExpectedObservationInputInventory) ||
			revision.ExpectedObservationInputInventory.Records != supportedInputs {
			return errors.New("T42.1 physical revision inventories are not exact")
		}
		if len(revision.ExpectedCandidateInventories) != len(plan.Profile.Pipeline.ExtractionDomains) {
			return errors.New("T42.1 candidate inventory is incomplete")
		}
		for index, domain := range plan.Profile.Pipeline.ExtractionDomains {
			inventory := revision.ExpectedCandidateInventories[index]
			if inventory.Domain != domain.Domain || inventory.Candidates.Records != domain.CandidateRecords ||
				inventory.Candidates.FramedBytes == 0 || !validDigest(inventory.Candidates.SHA256) {
				return errors.New("T42.1 candidate inventory differs from the production path policy")
			}
		}
	}
	physicalA, physicalB, physicalAReturn := plan.Revisions.Physical[0], plan.Revisions.Physical[1], plan.Revisions.Physical[2]
	if physicalA.ExpectedTreeInventory == physicalB.ExpectedTreeInventory ||
		physicalA.ExpectedObservationInputInventory == physicalB.ExpectedObservationInputInventory ||
		physicalA.ExpectedTreeInventory != physicalAReturn.ExpectedTreeInventory ||
		physicalA.ExpectedObservationInputInventory != physicalAReturn.ExpectedObservationInputInventory ||
		!reflect.DeepEqual(physicalA.ExpectedCandidateInventories, physicalAReturn.ExpectedCandidateInventories) {
		return errors.New("T42.1 physical inventory identities are not the exact A-B-A shape")
	}
	for _, revision := range plan.Revisions.Logical {
		if !validDigest(revision.CatalogLogicalSHA256) || !validDigest(revision.SemanticSHA256) ||
			revision.CatalogSource.Schema != catalogSourceSchema ||
			revision.CatalogSource.Records != plan.Profile.Logical.TotalServiceRecords+plan.Profile.Logical.Memberships+plan.Profile.Logical.CatalogUnownedEntries ||
			revision.CatalogSource.Bytes == 0 || !validDigest(revision.CatalogSource.SHA256) ||
			!validSetIdentity(revision.Catalog) || !validSetIdentity(revision.Memberships) ||
			!validSetIdentity(revision.Placements) || !validSetIdentity(revision.UnownedPrefixes) ||
			!validSetIdentity(revision.ServiceQueries) {
			return errors.New("T42.1 logical revision identity is invalid")
		}
	}
	a, b, aReturn := plan.Revisions.Logical[0], plan.Revisions.Logical[1], plan.Revisions.Logical[2]
	if a.Catalog != plan.Oracle.Catalog || a.Memberships != plan.Oracle.Memberships ||
		a.Placements != plan.Oracle.Placements || a.UnownedPrefixes != plan.Oracle.UnownedPrefixes ||
		a.ServiceQueries != plan.Oracle.ServiceQueries || a.Catalog == b.Catalog ||
		a.Memberships != b.Memberships || a.Placements != b.Placements ||
		a.UnownedPrefixes != b.UnownedPrefixes || a.ServiceQueries != b.ServiceQueries ||
		a.Catalog != aReturn.Catalog || a.Memberships != aReturn.Memberships ||
		a.Placements != aReturn.Placements || a.UnownedPrefixes != aReturn.UnownedPrefixes ||
		a.ServiceQueries != aReturn.ServiceQueries {
		return errors.New("T42.1 logical revision state identities are not the exact A-B-A shape")
	}
	if plan.Revisions.Logical[0].CatalogSource.SHA256 == plan.Revisions.Logical[1].CatalogSource.SHA256 ||
		plan.Revisions.Logical[0].CatalogSource.SHA256 == plan.Revisions.Logical[2].CatalogSource.SHA256 {
		return errors.New("T42.1 logical catalog source identities are not revision-specific")
	}
	if err := validatePlanExecutionContract(plan); err != nil {
		return err
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		return err
	}
	if len(raw) > MaxPlanBytes {
		return fmt.Errorf("T42.1 plan exceeds its frozen byte bound: observed=%d limit=%d", len(raw), MaxPlanBytes)
	}
	if plan.Schema == PlanV3Schema && len(raw) > MaxPlanV3AuthorBytes {
		return fmt.Errorf("T42.1 V3 plan exceeds its authoring headroom target: observed=%d limit=%d", len(raw), MaxPlanV3AuthorBytes)
	}
	return rejectSourceBearingPlan(raw)
}

func validSetIdentity(value SetIdentity) bool {
	return value.Records > 0 && value.FramedBytes > 0 && validDigest(value.SHA256)
}

func validateCombinedProfile(profile CombinedProfile) error {
	physical, logical, overlay := profile.Physical, profile.Logical, profile.Overlay
	mapping, typed, pipeline, bytes := profile.GeneratedMapping, profile.TypedIndex, profile.Pipeline, profile.Bytes
	if (profile.Schema != combinedProfileSchema && profile.Schema != combinedProfileV2Schema) || profile.Name != "combined-2m-10k-v1" ||
		profile.Seed != "t421-neutral-combined-v1" ||
		physical.StructuralPhysicalOwners != 2_000_002 || physical.StructuralRegularFiles != 2_000_002 ||
		physical.StructuralEligibleGoFiles != 2_000_000 || physical.StructuralControlFiles != 2 ||
		physical.StructuralNonCandidateFiles != structuralNonCandidateFiles ||
		physical.UniqueStructuralBlobs != 512 || physical.StructuralModeledPartitions != 489 ||
		physical.CombinedModeledPartitions != 56 ||
		physical.CombinedPhysicalOwners != physical.CombinedRegularFiles ||
		physical.CombinedRegularFiles != physical.StructuralRegularFiles+overlay.RegularFiles+mapping.RegularFiles+typed.RegularFiles ||
		physical.CombinedEligibleGoFiles != combinedEligibleGoFiles ||
		physical.CombinedControlFiles != physical.StructuralControlFiles+mapping.RegularFiles ||
		physical.CombinedUniqueContentsA != physical.UniqueStructuralBlobs+physical.StructuralControlFiles+overlay.DistinctContents+mapping.RegularFiles+typed.RegularFiles ||
		physical.CombinedUniqueContentsB != physical.CombinedUniqueContentsA+1 ||
		physical.CombinedUniqueContentsAR != physical.CombinedUniqueContentsA ||
		physical.StructuralUnownedFiles != physical.StructuralPhysicalOwners {
		return errors.New("T42.1 physical profile differs from the frozen shape")
	}
	if logical.AcceptedFloor != 8_000 || logical.AcceptedServices != 10_000 ||
		logical.TotalServiceRecords != 10_000 || logical.Memberships != 60_000 ||
		logical.DistinctPaths != 31_602 || logical.UnownedPaths != 102 ||
		logical.CatalogDistinctSelectors != 31_605 || logical.CatalogUnownedEntries != 105 ||
		logical.StructuralUnownedPrefixes != 3 || logical.AcceptedPhysicalFiles != 31_500 ||
		logical.UnownedPhysicalFiles != 2_000_104 ||
		logical.CatalogUnownedEntries != logical.UnownedPaths+logical.StructuralUnownedPrefixes ||
		logical.AcceptedPhysicalFiles+logical.UnownedPhysicalFiles != physical.CombinedRegularFiles ||
		logical.DistinctServicePathClaims != 50_000 || logical.DuplicateRoleMemberships != 10_000 ||
		logical.InheritedPlacementClaimCopies != 0 ||
		logical.MaxRolesPerServicePathClaim != 2 || logical.MaxAcceptedPathFanout != 20 ||
		logical.PotentialCartesianOwnerPairs != physical.CombinedRegularFiles*logical.AcceptedServices ||
		logical.MaterializedCartesianOwnerPairs != 0 ||
		!reflect.DeepEqual(logical.RoleMemberships, []Count{
			{Name: "generated", Count: 20_000}, {Name: "primary", Count: 10_000},
			{Name: "shared", Count: 10_000}, {Name: "supporting", Count: 10_000},
			{Name: "typed", Count: 10_000},
		}) {
		return errors.New("T42.1 logical profile differs from the frozen shape")
	}
	if overlay.Algorithm != "t411-paths-with-member-local-t421-relationships-v2" ||
		overlay.RegularFiles+mapping.RegularFiles+typed.RegularFiles != logical.DistinctPaths || overlay.GoFiles+overlay.IDLFiles != overlay.RegularFiles ||
		overlay.IDLFiles != logical.AcceptedServices || overlay.DistinctContents != overlay.RegularFiles ||
		overlay.RelationshipFiles != 30_000 || overlay.PathModeContentSet.Records != overlay.RegularFiles ||
		overlay.PathModeContentSet.FramedBytes <= bytes.OverlayLogicalSourceBytes ||
		!validDigest(overlay.PathModeContentSet.SHA256) {
		return errors.New("T42.1 overlay profile differs from the frozen shape")
	}
	if mapping.Schema != generatedMappingSchema || mapping.Records != 10_000 || mapping.RegularFiles != 1 ||
		mapping.ContentBytes != 1_940_048 || !validDigest(mapping.ContentSHA256) {
		return errors.New("T42.1 generated mapping control differs from the frozen shape")
	}
	if typed.Kind != typedIndexKind || typed.Path != typedIndexPath || typed.RegularFiles != 1 ||
		typed.ContentBytes != 2 || typed.ContentSHA256 != "sha256:102b51b9765a56a3e899f7cf0ee38e5251f9c503b357b330a49183eb7b155604" {
		return errors.New("T42.1 typed input differs from the frozen shape")
	}
	wantPipeline := PipelineProfile{
		SupportedGoFiles: combinedEligibleGoFiles, SupportedIDLFiles: 10_000, UnsupportedSourceFiles: 0,
		ProtoMessages: 20_000, ProtoServices: 10_000, ProtoOperations: 10_100,
		ResolverDeclarationRecords: 10_100, ResolverDeclarationLimit: 25_000,
		ResolverDeclarationHeadroom: 14_900, GeneratedMappings: 10_000,
		GeneratedMappingLimit: 25_000, GeneratedMappingHeadroom: 15_000,
		GeneratedDescriptors: 10_100, RPCCallPostings: 10_999,
		KafkaProducerPostings: 500, KafkaConsumerPostings: 9_500,
		RelationshipProjections: 20_999, ServiceReferences: 31_998,
		ResolverFixedReadsPerBuild: 1, ResolverModuleReadsPerBuild: 1,
		ResolverGeneratedReadsPerBuild: 10_000, ResolverBlobReadsPerBuild: 10_002,
		ResolverBlobBytesPerBuild:  9_776_093,
		CandidateRepositoryMembers: 8, CandidateCallerLeaves: 8, MaximumCallerLeafRecords: 2_773,
		ExtractionDomains: frozenExtractionDomains(),
	}
	if !reflect.DeepEqual(pipeline, wantPipeline) {
		return errors.New("T42.1 pipeline profile differs from the frozen shape")
	}
	var domainPartitions uint64
	var typedScopeBytes uint64
	for _, domain := range pipeline.ExtractionDomains {
		if err := validateFrozenExtractionDomain(domain); err != nil {
			return fmt.Errorf("T42.1 extraction domain %q: %w", domain.Domain, err)
		}
		if domain.TypedScopeEncodedBytes > math.MaxUint64-typedScopeBytes {
			return errors.New("T42.1 typed-scope byte total overflows")
		}
		typedScopeBytes += domain.TypedScopeEncodedBytes
		domainPartitions += domain.ApplicablePartitions
	}
	if domainPartitions != physical.CombinedModeledPartitions ||
		typedScopeBytes > candidate.MaxSparseAggregateScopeBytes {
		return errors.New("T42.1 extraction partition total is not exact")
	}
	observationBytes := (2_000_000-structuralNonCandidateFiles)*4_608 + bytes.OverlayLogicalSourceBytes
	if profile.Schema == combinedProfileV2Schema {
		observationBytes -= bytes.OverlayIDLBytes
	}
	if bytes.StructuralDeclaredGoBytes != 9_216_000_000 ||
		bytes.StructuralLogicalSourceBytes != 9_216_000_076 ||
		bytes.StructuralUniqueContentBytes != 512*4_608+76 ||
		bytes.StructuralNonCandidateBytes != structuralNonCandidateFiles*4_608 ||
		bytes.CombinedObservationInputBytes != observationBytes ||
		bytes.CombinedNonObservationBytes != bytes.CombinedLogicalSourceBytes-bytes.CombinedObservationInputBytes ||
		bytes.OverlayGeneratedGoBytes != 7_836_000 ||
		bytes.OverlayGeneratedGoBytes+bytes.OverlayServiceGoBytes+bytes.OverlayNeutralGoBytes != bytes.OverlayGoBytes ||
		bytes.OverlayGoBytes+bytes.OverlayIDLBytes != bytes.OverlayLogicalSourceBytes ||
		bytes.GeneratedMappingControlBytes != mapping.ContentBytes ||
		bytes.TypedInputRegularFiles != typed.RegularFiles || bytes.TypedInputLogicalBytes != typed.ContentBytes ||
		bytes.TypedInputUniqueContentBytes != typed.ContentBytes ||
		bytes.CombinedLogicalSourceBytes != bytes.StructuralLogicalSourceBytes+bytes.OverlayLogicalSourceBytes+bytes.GeneratedMappingControlBytes+bytes.TypedInputLogicalBytes ||
		bytes.CombinedUniqueContentBytesA != bytes.StructuralUniqueContentBytes+bytes.OverlayLogicalSourceBytes+bytes.GeneratedMappingControlBytes+bytes.TypedInputUniqueContentBytes ||
		bytes.CombinedUniqueContentBytesB != bytes.CombinedUniqueContentBytesA+4_608 ||
		bytes.CombinedUniqueContentBytesAR != bytes.CombinedUniqueContentBytesA ||
		bytes.InputFixtureContentBytes != 1_835_100 ||
		bytes.CatalogLogicalBytes == 0 || bytes.CatalogMeasurementRootBytes == 0 ||
		bytes.CatalogMeasurementPathBytes != uint64(len(combinedSourcePath)) ||
		bytes.CatalogServiceMemberBytes == 0 || bytes.CatalogPlacementMemberBytes == 0 ||
		bytes.CatalogServiceMemberBytes+bytes.CatalogPlacementMemberBytes != bytes.CatalogMemberBytes ||
		bytes.CatalogInheritedClaimBytes != 0 ||
		bytes.CatalogMeasurementRootBytes+bytes.CatalogMemberBytes != bytes.CatalogMeasurementEncodedBytes ||
		bytes.AddedCartesianSourceBytes != 0 || bytes.AllocatedBytesMeasured {
		return errors.New("T42.1 byte accounting differs from the frozen shape")
	}
	return nil
}

func validateFrozenExtractionDomain(domain ExtractionDomainProfile) error {
	limits := candidate.FrozenDomainResultLimitsV2()
	if domain.ResultPlanSchema == candidate.DomainResultPlanSchemaV3 {
		limits = candidate.FrozenDomainResultLimitsV3()
	} else if domain.ResultPlanSchema != candidate.DomainResultPlanSchemaV2 {
		return errors.New("result plan schema is invalid")
	}
	if domain.MaximumRecordsPerPartition == 0 || domain.MaximumRecordsPerPartition > candidate.MaxRecordsPerArtifact ||
		domain.MemberPartitions > uint64(limits.MemberPartitions) ||
		domain.TypedPartitions > uint64(limits.TypedPartitions) ||
		domain.ApplicablePartitions != domain.MemberPartitions+domain.TypedPartitions ||
		domain.ApplicablePartitions > uint64(limits.Partitions) ||
		domain.CandidateRecords > domain.MemberPartitions*domain.MaximumRecordsPerPartition ||
		len(domain.Partitions) != int(domain.ApplicablePartitions) ||
		domain.Availability == "empty" && domain.ApplicablePartitions != 0 ||
		domain.Availability == "admitted" && domain.ApplicablePartitions == 0 {
		return errors.New("partition inventory exceeds production limits")
	}
	if domain.TypedPartitions == 0 {
		if domain.TypedScopeRecords != 0 || domain.TypedScopePathBytes != 0 ||
			domain.TypedScopeEncodedBytes != 0 || domain.TypedScopeRevisions != nil {
			return errors.New("untyped domain retains a typed scope")
		}
	} else if domain.TypedScopeRecords == 0 || domain.TypedScopeRecords > candidate.PartitionMaxTypedScopeRecords ||
		domain.TypedScopePathBytes == 0 || domain.TypedScopePathBytes > candidate.PartitionMaxTypedScopePathBytes ||
		domain.TypedScopeEncodedBytes == 0 || domain.TypedScopeEncodedBytes > candidate.MaxSparseTypedScopeBytes ||
		!validTypedScopeRevisions(domain.TypedScopeRevisions) {
		return errors.New("typed scope exceeds production limits or lacks exact revision identity")
	}

	quotas := candidate.FrozenExtractionPartitionQuotas()
	partitionBytes := limits.CanonicalBytes
	partitionEncodedBytes := limits.EncodedBytes
	if limits.PartitionCanonicalBytes > 0 {
		partitionBytes = limits.PartitionCanonicalBytes
		partitionEncodedBytes = limits.PartitionEncodedBytes
	}
	partitionLimit := ResultTotals{
		Facts: int64(quotas.Facts), Rows: int64(quotas.Rows), References: int64(quotas.References),
		CanonicalBytes: partitionBytes, EncodedBytes: partitionEncodedBytes,
		MemberBytes: limits.MemberBytes, Members: 1,
	}
	aggregateLimit := ResultTotals{
		Facts: limits.Facts, Rows: limits.Rows, References: limits.References,
		CanonicalBytes: limits.CanonicalBytes, EncodedBytes: limits.EncodedBytes,
		MemberBytes: limits.AggregateMemberBytes, Members: limits.Members,
	}
	var reserved, expected ResultTotals
	var sourceStart uint64
	shape := newIdentityBuilder("t421-extraction-partition-shape-v1/" + domain.Domain)
	for ordinal, partition := range domain.Partitions {
		if partition.Ordinal != uint64(ordinal) || partition.SourceStart != sourceStart ||
			!resultTotalsWithin(partition.Expected, partition.Reservation) ||
			!resultTotalsWithin(partition.Reservation, partitionLimit) ||
			!addResultTotalsWithin(&reserved, partition.Reservation, aggregateLimit) ||
			!addResultTotalsWithin(&expected, partition.Expected, aggregateLimit) {
			return errors.New("partition shape or result reservation is invalid")
		}
		switch partition.Kind {
		case candidate.PartitionKindCandidateMember:
			if partition.MemberOrdinal < 0 || partition.SourceEnd <= partition.SourceStart ||
				partition.AdmittedRecords != partition.SourceEnd-partition.SourceStart ||
				partition.AdmittedRecords > domain.MaximumRecordsPerPartition ||
				(partition.MemberRecordStart != 0 || partition.MemberRecordEnd != 0) &&
					partition.MemberRecordEnd-partition.MemberRecordStart != partition.AdmittedRecords ||
				partition.Reservation.MemberBytes == 0 || partition.Reservation.Members != 1 {
				return errors.New("candidate-member partition is invalid")
			}
			sourceStart = partition.SourceEnd
		case candidate.PartitionKindTypedInput:
			if uint64(ordinal) < domain.MemberPartitions || partition.MemberOrdinal != -1 ||
				partition.SourceEnd != partition.SourceStart || partition.AdmittedRecords != 0 ||
				partition.MemberRecordStart != 0 || partition.MemberRecordEnd != 0 ||
				partition.Reservation.MemberBytes != 0 || partition.Reservation.Members != 0 {
				return errors.New("typed-input partition is invalid")
			}
		default:
			return errors.New("partition kind is invalid")
		}
		if err := shape.add(partition); err != nil {
			return err
		}
	}
	if sourceStart != domain.CandidateRecords || reserved != domain.Reserved || expected != domain.Expected ||
		shape.finish() != domain.PartitionShape {
		return errors.New("partition aggregate or identity is not exact")
	}
	return nil
}

func validTypedScopeRevisions(values []TypedScopeRevision) bool {
	if len(values) != 3 || values[0].PhysicalRevision != "a" || values[1].PhysicalRevision != "b" ||
		values[2].PhysicalRevision != "a-return" || values[0].SHA256 != values[2].SHA256 ||
		values[0].DescriptorContentSHA256 != values[2].DescriptorContentSHA256 ||
		values[0].SHA256 == values[1].SHA256 {
		return false
	}
	for _, value := range values {
		if !validDigest(value.SHA256) || !validDigest(value.DescriptorContentSHA256) {
			return false
		}
	}
	return true
}

func resultTotalsWithin(value, limit ResultTotals) bool {
	return value.Facts >= 0 && value.Rows >= 0 && value.References >= 0 &&
		value.CanonicalBytes >= 0 && value.EncodedBytes >= 0 && value.MemberBytes >= 0 && value.Members >= 0 &&
		value.Facts <= limit.Facts && value.Rows <= limit.Rows && value.References <= limit.References &&
		value.CanonicalBytes <= limit.CanonicalBytes && value.EncodedBytes <= limit.EncodedBytes &&
		value.MemberBytes <= limit.MemberBytes && value.Members <= limit.Members
}

func addResultTotalsWithin(total *ResultTotals, value, limit ResultTotals) bool {
	remaining := ResultTotals{
		Facts: limit.Facts - total.Facts, Rows: limit.Rows - total.Rows,
		References:     limit.References - total.References,
		CanonicalBytes: limit.CanonicalBytes - total.CanonicalBytes,
		EncodedBytes:   limit.EncodedBytes - total.EncodedBytes,
		MemberBytes:    limit.MemberBytes - total.MemberBytes, Members: limit.Members - total.Members,
	}
	if !resultTotalsWithin(value, remaining) {
		return false
	}
	addResultTotals(total, value)
	return true
}

func validateOracle(oracle Oracle, profile CombinedProfile, schema string) error {
	if oracle.Schema != OracleSchema || !oracle.Independent || oracle.ConsumesPhebsResults ||
		oracle.Catalog.Records != profile.Logical.TotalServiceRecords ||
		oracle.Memberships.Records != profile.Logical.Memberships ||
		oracle.Placements.Records != profile.Logical.CatalogDistinctSelectors ||
		oracle.UnownedPrefixes.Records != profile.Logical.StructuralUnownedPrefixes ||
		oracle.ServiceQueries.Records != profile.Logical.AcceptedServices {
		return errors.New("T42.1 independent oracle counts differ from the profile")
	}
	for _, identity := range []SetIdentity{
		oracle.Catalog, oracle.Memberships, oracle.Placements, oracle.UnownedPrefixes, oracle.ServiceQueries,
	} {
		if identity.Records == 0 || identity.FramedBytes == 0 || !validDigest(identity.SHA256) {
			return errors.New("T42.1 independent oracle identity is invalid")
		}
	}
	queryCases := frozenQueryCases()
	if correctedPlanSemantics(schema) {
		queryCases = correctedQueryCases()
	}
	if !reflect.DeepEqual(oracle.QueryCases, queryCases) || len(oracle.Relationships) != 4 {
		return errors.New("T42.1 query or relationship inventory is incomplete")
	}
	productIdentity, rpcIdentity, err := independentProductProjectionIdentity()
	if err != nil {
		return err
	}
	if oracle.ProductRelationships.RPCProjections != 10_999 ||
		oracle.ProductRelationships.KafkaProducerProjections != 500 ||
		oracle.ProductRelationships.KafkaConsumerProjections != 9_500 ||
		oracle.ProductRelationships.TotalProjections != 20_999 ||
		oracle.ProductRelationships.ServiceReferences != 31_998 ||
		oracle.ProductRelationships.KafkaPairOraclePosture != "semantic_pairs_only_not_product_cooccurrence" ||
		oracle.ProductRelationships.Canonicalization != "family_order=chain,layered_dag,bounded_fanout,hotspot;provider,slot,consumer;hotspot_producer_before_consumers;runtime_generation_ids_excluded" ||
		oracle.ProductRelationships.GlobalCallerPolicy != "callerexecute-catalog-wide-direct-resolver-v1" ||
		oracle.ProductRelationships.CallerCandidateRecords != 21_603 ||
		!reflect.DeepEqual(oracle.ProductRelationships.CallerLeaves, frozenCallerLeafProfiles()) ||
		oracle.ProductRelationships.ExpectedRPCProjections != rpcIdentity ||
		oracle.ProductRelationships.ExpectedProjections != productIdentity {
		return errors.New("T42.1 product relationship oracle differs from the frozen shape")
	}
	wantFamilies := []struct {
		name, seed, protocol string
		edges, in, out       uint64
		acyclic              bool
	}{
		{name: "chain", seed: "t421-chain-rpc-v1", protocol: "grpc", edges: 9_999, in: 1, out: 1, acyclic: true},
		{name: "layered_dag", seed: "t421-layered-dag-rpc-v1", protocol: "grpc", edges: 200, in: 2, out: 2, acyclic: true},
		{name: "bounded_fanout", seed: "t421-bounded-fanout-rpc-v2", protocol: "grpc", edges: 800, in: 8, out: 8},
		{name: "hotspot", seed: "t421-hotspot-kafka-v1", protocol: "kafka", edges: 9_500, in: 1, out: 19, acyclic: true},
	}
	for index, family := range oracle.Relationships {
		want := wantFamilies[index]
		if family.Name != want.name || family.Seed != want.seed ||
			!reflect.DeepEqual(family.Protocols, []string{want.protocol}) ||
			family.SemanticPairEdges != want.edges || family.MaxInDegree != want.in ||
			family.MaxOutDegree != want.out || family.Acyclic != want.acyclic ||
			family.ExpectedEdges.Records != family.SemanticPairEdges || family.ExpectedEdges.FramedBytes == 0 ||
			!validDigest(family.ExpectedEdges.SHA256) {
			return fmt.Errorf("T42.1 relationship family %q differs from the frozen shape", family.Name)
		}
	}
	return nil
}

func frozenInputs() []InputBinding {
	return []InputBinding{
		{Name: "t4013_neutral47_freeze", Kind: "sha256", Identity: "sha256:43ec1d76a48a741398bb65728aaecf3caf96be40f74c52b6d0d7cc619e29df07"},
		{Name: "t4013_neutral47_package", Kind: "sha256", Identity: "sha256:7130d80bd6c4b59ae8d4cfe0fdefd456d6287a6aef35781577b53ce2acb6c2e0"},
		{Name: "t4013_neutral47_plan", Kind: "sha256", Identity: "sha256:44fe9383a2011fbe0be15460e96294cf011b4e01da4c7e4e6e7965281295c810"},
		{Name: "t4013_neutral47_receipt", Kind: "sha256", Identity: "sha256:3f6a45fccb47119518041a6ec87cabfa3c596bf5fd3d261dcbaaf48c9d20522b"},
		{Name: "t4013_neutral47_source", Kind: "git_commit", Identity: "fb88c1d7fed7f32c1c3dd07303268366535cfa0c"},
		{Name: "t401_envelope", Kind: "sha256", Identity: "sha256:92cce848e6e42942c24e2fa066968571fb5693252b7b41b7a91c889881fe7f94"},
		{Name: "t401_reproducibility", Kind: "sha256", Identity: "sha256:b7b0491af659007eb8e903279ca63c6f8178878a8af114a9af0cd407e52ccb1a"},
		{Name: "t401_structural_manifest", Kind: "sha256", Identity: "sha256:4ae92b8efa58d459fe8fa10ba23c5cedad3adc7b2dddbd7618ea8d96c306604b"},
		{Name: "t401_structural_oracle", Kind: "sha256", Identity: "sha256:8974c843fb9a9bcdb8864367f5e42394d97069058e87481d9d7e8f21e77944df"},
		{Name: "t401_structural_profile", Kind: "sha256", Identity: "sha256:4227b0a75cc6a2cf1120e5d9e4c228fe23c0dbc2261313f513b6ae809364d430"},
		{Name: "t401_structural_receipt", Kind: "sha256", Identity: "sha256:bd80bef34f61f35c2f701d0877d4c013ec3c7d0ce62ec3756b32b7a4f103b2c2"},
		{Name: "t4110_implementation", Kind: "git_commit", Identity: "7a06e5dc24d1c9b5370ebf6111fd6aa926eb6b07"},
		{Name: "t4110_integrated_main", Kind: "git_commit", Identity: "d92b6673db6d4b582c2223536fe52358629ae60e"},
		{Name: "t4110_results", Kind: "sha256", Identity: "sha256:e751ea4c16284a5f3e69e7b7dde3b2bcaa9274f242d1cf4914bc2757c3b2e680"},
		{Name: "t411_envelope", Kind: "sha256", Identity: "sha256:99ec8a3dc79537bf1db842234f6fe054abd03c9af7503987f78c5530fdfd525f"},
		{Name: "t411_receipt", Kind: "sha256", Identity: "sha256:c9a30ab63960fee682558a04e79b66f1d1fcf2b9a7f2bfc2e3a012139291dc55"},
		{Name: "t411_target_profile", Kind: "sha256", Identity: "sha256:f54f6c634dea5ce780df1f82591d876ddd229e125444549c1288d7ee4483cf91"},
	}
}

func frozenRevisionHistory(profile CombinedProfile, logical []LogicalRevision) (RevisionHistory, error) {
	return revisionHistoryForScope(profile, logical, false)
}

func revisionHistoryForScope(profile CombinedProfile, logical []LogicalRevision, goOnly bool) (RevisionHistory, error) {
	recipe := SourceRecipe{
		Schema:          "t421-combined-source-recipe-v1",
		Composition:     "t401-owner-and-byte-preserving-nonsource-adapter-plus-disjoint-generated-control-and-t421-overlay-v3",
		GitObjectFormat: "sha1", PathOrder: "ascii_lexicographic", FileMode: regularFileMode,
		AuthorName: "phebs-t421-fixture-author", AuthorEmail: "fixture@example.invalid",
		CommitterName: "phebs-t421-fixture-author", CommitterEmail: "fixture@example.invalid",
		Timestamp: 1_788_307_200, Timezone: "+0000",
		ObjectHeaderPolicy:            "git-canonical-object-header-v1",
		CommitMessagePrefix:           "fixture: T42.2 combined ",
		StructuralNonCandidateRecords: structuralNonCandidateFiles,
		StructuralNonCandidateRule:    "structural-go-ordinals-1-through-1999999-replace-dot-go-with-dot-txt-v1",
		OverlayRecords:                profile.Overlay.PathModeContentSet.Records,
		OverlayFramedBytes:            profile.Overlay.PathModeContentSet.FramedBytes,
		OverlaySHA256:                 profile.Overlay.PathModeContentSet.SHA256,
		GeneratedMappingRecords:       profile.GeneratedMapping.Records,
		GeneratedMappingPath:          resolverinput.GeneratedFromSnapshotPath,
		GeneratedMappingMode:          regularFileMode,
		GeneratedMappingBytes:         profile.GeneratedMapping.ContentBytes,
		GeneratedMappingSHA256:        profile.GeneratedMapping.ContentSHA256,
		TypedInputRecords:             profile.TypedIndex.RegularFiles,
		TypedInputKind:                profile.TypedIndex.Kind,
		TypedInputPath:                profile.TypedIndex.Path,
		TypedInputMode:                regularFileMode,
		TypedInputBytes:               profile.TypedIndex.ContentBytes,
		TypedInputSHA256:              profile.TypedIndex.ContentSHA256,
		TypedInputBlobOID:             gitSHA1ObjectID("blob", []byte{0x0a, 0x00}),
		CatalogSourcePolicy:           "sealed-external-canonical-json-per-logical-revision-v1",
		TreeEntryEncoding:             "git-tree-entry-mode-space-path-nul-raw-oid-v1",
		CommitHeaderOrder:             "tree,parent*,author,committer,blank,body",
		IdentityEncoding:              "utf8-name-angle-email-space-timestamp-space-timezone-v1",
		CommitBodyTerminator:          "single-lf",
		ParentPolicy:                  "a:none;b:a;a-return:b",
		AuthoredManifestSchema:        "t422-authored-source-manifest-v1",
		TreeInventoryCanonicalization: "t421-expected-combined-tree-inventory-v1-domain-then-all-leaves-as-be64-length-framed-path-mode-decimal-bytes-git-sha1-blob-oid-in-ascii-path-order",
	}
	base := []struct {
		name, parent, posture, commit, tree string
		changed                             uint64
	}{
		{name: "a", posture: "baseline", commit: "b548a84f91ff295f4ce7c6fa226b6901f6177a9e", tree: "96b33ec020abad515767d23b0ab0a3c12933ae22"},
		{name: "b", parent: "a", posture: "bounded_delta", changed: 1, commit: "73644b82d26a7e4eb354f7293d583ea577816318", tree: "f58ccffd268a5cf4bc40dcd9d2c5a64476589aec"},
		{name: "a-return", parent: "b", posture: "content_return", changed: 1, commit: "abf9ec6f3849e3f4708e31592f5eb72ddf6759db", tree: "96b33ec020abad515767d23b0ab0a3c12933ae22"},
	}
	sourceIdentities, err := expectedCombinedSourceIdentitiesForScope(profile, goOnly)
	if err != nil {
		return RevisionHistory{}, err
	}
	physical := make([]PhysicalRevision, 0, len(base))
	previousCommitRecipe := ""
	previousExpectedCommit := ""
	for _, value := range base {
		identity, ok := sourceIdentities[value.name]
		if !ok {
			return RevisionHistory{}, errors.New("T42.1 expected source identity is absent")
		}
		treeRecipe := recipeDigest(
			"t421-source-tree-recipe-v1", recipe.Schema, recipe.Composition,
			recipe.GitObjectFormat, recipe.PathOrder, recipe.FileMode, value.tree,
			recipe.TreeEntryEncoding, fmt.Sprint(recipe.StructuralNonCandidateRecords), recipe.StructuralNonCandidateRule,
			fmt.Sprint(recipe.OverlayRecords), fmt.Sprint(recipe.OverlayFramedBytes),
			recipe.OverlaySHA256, fmt.Sprint(recipe.GeneratedMappingRecords),
			recipe.GeneratedMappingPath, recipe.GeneratedMappingMode,
			fmt.Sprint(recipe.GeneratedMappingBytes), recipe.GeneratedMappingSHA256,
			fmt.Sprint(recipe.TypedInputRecords), recipe.TypedInputKind,
			recipe.TypedInputPath, recipe.TypedInputMode,
			fmt.Sprint(recipe.TypedInputBytes), recipe.TypedInputSHA256, recipe.TypedInputBlobOID,
		)
		message := recipe.CommitMessagePrefix + value.name
		candidateInventoriesSHA256, err := receiptSHA256(identity.CandidateInventories)
		if err != nil {
			return RevisionHistory{}, err
		}
		commitBytes, err := canonicalGitCommitBytesFor(identity.TreeOID, previousExpectedCommit, message, recipe)
		if err != nil {
			return RevisionHistory{}, err
		}
		expectedCommit := gitSHA1ObjectID("commit", commitBytes)
		commitRecipe := recipeDigest(
			"t421-source-commit-recipe-v1", value.name, value.parent, value.commit,
			treeRecipe, previousCommitRecipe, recipe.AuthorName, recipe.AuthorEmail,
			recipe.CommitterName, recipe.CommitterEmail, recipe.ObjectHeaderPolicy,
			recipe.CommitHeaderOrder, recipe.IdentityEncoding, recipe.CommitBodyTerminator,
			recipe.ParentPolicy, recipe.AuthoredManifestSchema,
			fmt.Sprint(recipe.Timestamp), recipe.Timezone, message,
			identity.Inventory.SHA256, identity.ObservationInputInventory.SHA256, candidateInventoriesSHA256,
			identity.TreeOID, expectedCommit,
		)
		physical = append(physical, PhysicalRevision{
			Name: value.name, Parent: value.parent, Posture: value.posture,
			ChangedPhysicalFiles: value.changed, BaseCommit: value.commit, BaseTree: value.tree,
			CommitMessage: message, SourceTreeRecipeSHA256: treeRecipe,
			SourceCommitRecipeSHA256: commitRecipe, ExpectedTreeInventory: identity.Inventory,
			ExpectedObservationInputInventory: identity.ObservationInputInventory,
			ExpectedCandidateInventories:      slices.Clone(identity.CandidateInventories),
			ExpectedTree:                      identity.TreeOID, ExpectedCommit: expectedCommit,
		})
		previousCommitRecipe = commitRecipe
		previousExpectedCommit = expectedCommit
	}
	return RevisionHistory{SourceRecipe: recipe, Physical: physical, Logical: slices.Clone(logical)}, nil
}

func recipeDigest(domain string, values ...string) string {
	digest := sha256.New()
	for _, value := range append([]string{domain}, values...) {
		_, _ = fmt.Fprintf(digest, "%d:", len(value))
		_, _ = digest.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func frozenPhaseOrder() []string {
	return []string{
		"preflight", "cold", "warm_noop", "physical_delta_b", "logical_delta_b",
		"return_a", "stale_lease", "process_restart", "pressure_80", "pressure_90", "pressure_75",
		"archive_restore", "lifecycle_collection", "product_queries", "teardown",
	}
}

func frozenPhaseDeadlines() []PhaseDeadline {
	health := uint64(15 * 60 * 1_000)
	convergence := uint64(4 * 60 * 60 * 1_000)
	revalidation := uint64(20 * 60 * 1_000)
	deadlines := make([]PhaseDeadline, 0, len(frozenPhaseOrder()))
	for _, phase := range frozenPhaseOrder() {
		deadline := revalidation
		switch phase {
		case "preflight", "teardown":
			deadline = health
		case "cold", "physical_delta_b", "logical_delta_b", "return_a", "stale_lease", "process_restart",
			"archive_restore", "lifecycle_collection":
			deadline = convergence
		}
		deadlines = append(deadlines, PhaseDeadline{Phase: phase, DeadlineMS: deadline})
	}
	return deadlines
}

func frozenWorkEnvelope(profile CombinedProfile) WorkEnvelope {
	exact := func(value uint64) CounterBound { return CounterBound{Minimum: value, Maximum: value} }
	upTo := func(value uint64) CounterBound { return CounterBound{Maximum: value} }
	positive := func(value uint64) CounterBound { return CounterBound{Minimum: 1, Maximum: value} }
	rows := make([]PhaseWorkBounds, len(frozenPhaseOrder()))
	roles := []string{"git", "hdiutil", "phebs", "surreal", "t422-author", "zoekt-git-index"}
	for index, phase := range frozenPhaseOrder() {
		bounds := make([]RoleBound, len(roles))
		for roleIndex, role := range roles {
			bounds[roleIndex] = RoleBound{Name: role}
		}
		rows[index] = PhaseWorkBounds{Phase: phase, ChildProcessRoles: bounds}
	}
	row := func(phase string) *PhaseWorkBounds {
		return &rows[slices.IndexFunc(rows, func(value PhaseWorkBounds) bool { return value.Phase == phase })]
	}
	role := func(phase, name string, minimum, maximum uint64) {
		bounds := row(phase).ChildProcessRoles
		index := slices.IndexFunc(bounds, func(value RoleBound) bool { return value.Name == name })
		bounds[index].Minimum, bounds[index].Maximum = minimum, maximum
	}
	regular := profile.Physical.CombinedRegularFiles
	supported := profile.Pipeline.SupportedGoFiles + profile.Pipeline.SupportedIDLFiles
	partitions := profile.Physical.CombinedModeledPartitions
	members := regular + partitions
	jobs := (supported + partitions) * frozenSafetyEnvelope().MaximumRetriesPerUnit
	cacheMembers := profile.Logical.AcceptedServices + 1
	lifecycleMembers := 2 * members
	lifecycleControlReads := uint64(4_096)
	cache := func(value *PhaseWorkBounds) {
		value.CacheRootReads = upTo(cacheMembers)
		value.CacheMemberReads = upTo(cacheMembers)
		value.CacheLookups = upTo(2 * cacheMembers)
		value.CacheHits = upTo(2 * cacheMembers)
		value.CacheMisses = upTo(2 * cacheMembers)
	}
	fullPass := func(phase string, changedPhysical, changedLogical uint64, uniqueBytes uint64) {
		value := row(phase)
		value.PhysicalCorpusPasses = exact(1)
		value.ChangedPhysicalFiles = exact(changedPhysical)
		value.ChangedLogicalServices = exact(changedLogical)
		value.GitReads = positive(regular)
		value.IndexFiles = exact(regular)
		value.ObservationParses = exact(supported)
		value.SourceLogicalBytes = positive(profile.Bytes.CombinedLogicalSourceBytes)
		value.SourceUniqueBytes = exact(uniqueBytes)
		value.ApplicablePartitions = exact(profile.Physical.CombinedModeledPartitions)
		value.PublishedDomains = exact(uint64(len(frozenReceiptContract().ExtractionDomains)))
		value.ControlReads = positive(7 + 3*64 + partitions)
		value.MemberReads = positive(members)
		value.JobAttempts = positive(jobs)
		value.StoreTransactions = positive(100_000)
		value.StoreRows = positive(100_000 * 512)
		cache(value)
		value.PublicationWrites = positive(64)
		value.RelationshipBuildAttempts = exact(1)
		value.CombinedPhysicalOwners = exact(regular)
		value.LogicalMemberships = exact(profile.Logical.Memberships)
		value.RelationshipProjections = exact(profile.Pipeline.RelationshipProjections)
		value.ResolverBlobBytes = exact(profile.Pipeline.ResolverBlobBytesPerBuild)
		value.ResolverBlobReads = exact(profile.Pipeline.ResolverBlobReadsPerBuild)
		value.ServiceReferences = exact(profile.Pipeline.ServiceReferences)
		value.ServiceRows = exact(profile.Logical.TotalServiceRecords)
		value.UnsupportedSourceFiles = exact(profile.Pipeline.UnsupportedSourceFiles)
	}
	fullPass("cold", regular, profile.Logical.AcceptedServices, profile.Bytes.CombinedUniqueContentBytesA)
	role("cold", "git", 1, 64)
	role("cold", "phebs", 1, 1)
	role("cold", "surreal", 1, 1)
	role("cold", "t422-author", 1, 1)
	role("cold", "zoekt-git-index", 1, 64)
	fullPass("physical_delta_b", 1, 0, profile.Bytes.CombinedUniqueContentBytesB)
	role("physical_delta_b", "git", 1, 64)
	role("physical_delta_b", "t422-author", 1, 1)
	role("physical_delta_b", "zoekt-git-index", 1, 64)
	row("physical_delta_b").LifecycleOwnerTurns = exact(2)
	row("physical_delta_b").LifecycleDeleted = exact(1)
	fullPass("return_a", 1, 1, profile.Bytes.CombinedUniqueContentBytesAR)
	role("return_a", "git", 1, 64)
	role("return_a", "t422-author", 1, 1)
	role("return_a", "zoekt-git-index", 1, 64)
	role("process_restart", "phebs", 1, 1)
	role("process_restart", "surreal", 1, 1)

	warm := row("warm_noop")
	warm.MemberReads = upTo(members)
	warm.JobAttempts = upTo(1_000)
	warm.StoreTransactions = upTo(1_000)
	warm.StoreRows = upTo(1_000 * 512)
	cache(warm)

	logical := row("logical_delta_b")
	logical.ChangedLogicalServices = exact(1)
	logical.MemberReads = upTo(members)
	logical.JobAttempts = positive(6 * profile.Logical.AcceptedServices)
	logical.StoreTransactions = positive(170)
	logical.StoreRows = positive(170 * 512)
	cache(logical)
	logical.PublicationWrites = positive(64)
	logical.RelationshipBuildAttempts = exact(1)
	logical.LogicalMemberships = upTo(profile.Logical.Memberships)
	logical.RelationshipProjections = exact(profile.Pipeline.RelationshipProjections)
	logical.ResolverBlobBytes = exact(profile.Pipeline.ResolverBlobBytesPerBuild)
	logical.ResolverBlobReads = exact(profile.Pipeline.ResolverBlobReadsPerBuild)
	logical.ServiceReferences = exact(profile.Pipeline.ServiceReferences)
	logical.ServiceRows = upTo(profile.Logical.TotalServiceRecords)
	for _, phase := range []string{"stale_lease", "process_restart"} {
		value := row(phase)
		value.MemberReads = upTo(members)
		value.ControlReads = upTo(lifecycleControlReads)
		value.JobAttempts = positive(jobs)
		value.StoreTransactions = positive(1_000)
		value.StoreRows = positive(1_000 * 512)
		cache(value)
	}

	for _, phase := range []string{"pressure_80", "pressure_90", "pressure_75", "product_queries"} {
		value := row(phase)
		value.MemberReads = upTo(lifecycleMembers)
		value.ControlReads = upTo(lifecycleControlReads)
		value.StoreTransactions = upTo(1_000)
		value.StoreRows = upTo(1_000 * 512)
		cache(value)
	}
	pressurePrepare := row("pressure_80")
	pressurePrepare.LifecycleOwnerTurns = positive(4_096)
	pressurePrepare.LifecycleDeleted = positive(4_096 * uint64(lifecycle.MaxDeletesPerTick))
	pressureRecovery := row("pressure_75")
	pressureRecovery.LifecycleOwnerTurns = positive(4_096)
	pressureRecovery.LifecycleDeleted = upTo(4_096 * uint64(lifecycle.MaxDeletesPerTick))

	restore := row("archive_restore")
	restore.MemberReads = upTo(lifecycleMembers)
	restore.ControlReads = upTo(lifecycleControlReads)
	restore.JobAttempts = upTo(jobs)
	restore.StoreTransactions = positive(100_000)
	restore.StoreRows = positive(100_000 * 512)
	restore.PublicationWrites = positive(64)
	restore.LogicalMemberships = upTo(profile.Logical.Memberships)
	restore.ServiceRows = upTo(profile.Logical.TotalServiceRecords)
	role("archive_restore", "phebs", 3, 3)
	role("archive_restore", "surreal", 3, 3)

	collection := row("lifecycle_collection")
	collection.MemberReads = upTo(lifecycleMembers)
	collection.ControlReads = upTo(lifecycleControlReads)
	collection.StoreTransactions = positive(100_000)
	collection.StoreRows = positive(100_000 * 512)
	collection.LifecycleOwnerTurns = positive(4_096)
	collection.LifecycleDeleted = positive(4_096 * uint64(lifecycle.MaxDeletesPerTick))
	row("product_queries").MemberReads = upTo(members + profile.Pipeline.ServiceReferences)
	role("teardown", "hdiutil", 1, 1)
	for _, state := range frozenPhaseStates() {
		if state.Phase == "preflight" || state.Phase == "teardown" {
			continue
		}
		count := uint64(0)
		for _, action := range []string{
			state.SourceAction, state.SearchAction, state.ObservationAction,
			state.CatalogAction, state.RelationshipAction,
		} {
			if action == "reuse" {
				count++
			}
		}
		row(state.Phase).ReuseDecisions = exact(count)
	}

	return WorkEnvelope{
		Schema:                         "t422-phase-work-envelope-v1",
		MaximumRetriesPerUnit:          frozenSafetyEnvelope().MaximumRetriesPerUnit,
		MaximumStoreRowsPerTransaction: 512,
		MaximumAggregatePartitions:     partitions,
		MaximumLifecycleDeletesPerTurn: uint64(lifecycle.MaxDeletesPerTick),
		MaximumDataLogicalBytes:        128 << 30,
		MaximumChildProcessesPerPhase:  132,
		ChildProcessRoles:              roles,
		LifecycleOwners:                frozenLifecycleOwners(),
		Phases:                         rows,
	}
}

func frozenLifecycleOwners() []string {
	return []string{
		lifecycle.CatalogOwner,
		lifecycle.JobOwner,
		lifecycle.GenerationScheduleOwner,
		lifecycle.InvestigationOwner,
		lifecycle.ObservationOwner,
		lifecycle.ObservationV2Owner,
		lifecycle.PartialStageOwner,
		lifecycle.ProofOwner,
		lifecycle.ReaderOwner,
		lifecycle.RelationshipOwner,
		lifecycle.ResolverOwner,
		lifecycle.SearchOwner,
		lifecycle.TombstoneOwner,
		lifecycle.SourceOwner,
	}
}

func correctedLifecycleOwners() []string {
	owners := append(frozenLifecycleOwners(), lifecycle.CatalogV3Owner, lifecycle.RelationshipV3Owner)
	slices.Sort(owners)
	return owners
}

func frozenSealPolicy() SealPolicy {
	return SealPolicy{
		Schema: "t422-source-free-seal-policy-v1", KeyAlgorithm: "ssh-ed25519",
		SignerIdentity:                       "phebs-t422-ceremony",
		FreezeSignatureNamespace:             "phebs-t422-freeze",
		SourceVerificationSignatureNamespace: "phebs-t422-source-verification",
		ReturnedSignatureNamespace:           "phebs-t422-returned",
		TrustRoot:                            "out_of_band_reviewed_ssh_sha256_fingerprint",
		PackageDigestAlgorithm:               "sha256",
		SignatureInputPolicy:                 "ssh-keygen-Y-sign-exact-file-bytes-v1",
		SignerFingerprintPolicy:              "SHA256-base64-no-padding",
		ArchiveFormat:                        "ustar-gzip-owner-zero-mtime-zero-v1",
		InventoryOrder:                       "ascii-lexicographic",
		EntryTypePolicy:                      "regular-files-only-no-links",
		ManifestSchema:                       "t422-source-free-package-manifest-v1",
		ManifestRequiredFields: []string{
			"schema", "plan_sha256", "execution_freeze_sha256", "results_sha256",
			"signer_fingerprint", "source_free",
		},
		SourceVerificationSchema:           "t422-source-verification-v1",
		SourceVerificationCanonicalization: "canonical-json-source-free-actual-git-object-verification-v1",
		SourceVerificationRequiredFields: []string{
			"schema", "plan_sha256", "execution_freeze_sha256", "revision_results_sha256",
			"exact_inventory_sha256", "revisions", "source_free",
		},
		SourceVerificationRevisionFields: []string{
			"name", "base_commit", "base_tree", "tree_inventory", "tree_oid",
			"observation_input_inventory", "candidate_inventories",
			"typed_input_kind", "typed_input_path", "typed_input_mode", "typed_input_bytes",
			"typed_input_sha256", "typed_input_blob_oid", "raw_commit_sha256", "commit_oid",
		},
		SourceVerificationChecks: []string{
			"canonical_source_free_bytes", "exact_base_commit_and_tree", "full_recursive_tree_inventory",
			"recompute_observation_input_inventory", "recompute_candidate_inventories_with_frozen_production_policies",
			"verify_typed_input_blob_tuple", "git_tree_oid", "raw_commit_bytes_and_oid",
			"plan_freeze_and_revision_results_binding",
		},
		ChecksumLineFormat: "lowercase-hex-two-spaces-basename-lf",
		ChecksumCoverage: []string{
			"allowed_signers", "execution-freeze.json", "execution-freeze.json.sig",
			"manifest.json", "plan.json", "results.json", "signer.pub",
			"source-verification.json", "source-verification.json.sig",
		},
		ChecksumExclusions:  []string{"SHA256SUMS", "SHA256SUMS.sig"},
		RequireUniqueSigner: true,
		MaximumPackageBytes: 4 << 20, MaximumExpandedBytes: 4 << 20,
		ExactInventory: []string{
			"SHA256SUMS", "SHA256SUMS.sig", "allowed_signers", "execution-freeze.json",
			"execution-freeze.json.sig", "manifest.json", "plan.json", "results.json", "signer.pub",
			"source-verification.json", "source-verification.json.sig",
		},
	}
}

func frozenMeterPolicy() MeterPolicy {
	return MeterPolicy{
		Schema:                "t422-combined-meter-policy-v1",
		Authority:             "t4013-v30-phase-meter-plus-t4110-receipt-accounting",
		PhaseReset:            "zero_after_prior_phase_quiet_boundary;finish_once;teardown_separate",
		CounterAggregation:    "phase_local_unsigned_event_sum;max_fields_are_per-unit_high-water",
		ByteGaugeSemantics:    "phase_high_water_v1:logical=max_coherent_bounded_regular_file_apparent_bytes;allocated=max_coherent_filesystem_blocks_for_data_custody_including_ballast;sample_phase_start,after_each_custody_mutation,at_each_pressure_or_lifecycle_capacity_checkpoint,and_phase_finish;any_required_sample_unavailable_is_fail_closed;receipt_records_max_not_endpoint",
		ProcessGaugeSemantics: "peak_rss=max_coherent_root_and_descendant_resident_bytes_during_phase;children=distinct_executed_descendants",
		CacheSemantics:        "lookups=hits+misses;misses=root_loads+member_loads;each_load_requires_one_same-phase_validation",
		StoreSemantics:        "transactions=committed_or_attempted_write_transactions;rows=rows_submitted;maximum_rows_records_largest_single_transaction",
		LifecycleSemantics:    "owner_turns=completed_or_error owner attempts;deleted=durably_removed_units;maximum_deletes_records_largest_single_turn",
		RequiredMetricsSHA256: recipeDigest("t422-required-metrics-v1", receiptMetricNames...),
	}
}

func frozenPhaseStates() []PhaseState {
	return []PhaseState{
		{Phase: "preflight", SourceAction: "not_started", SearchAction: "not_started", ObservationAction: "not_started", CatalogAction: "not_started", RelationshipAction: "not_started", ExpectedCurrentAuthority: "none"},
		{Phase: "cold", PhysicalRevision: "a", LogicalRevision: "a", SourceAction: "author", SearchAction: "publish", ObservationAction: "publish", CatalogAction: "publish_actual_source_binding", RelationshipAction: "publish", ExpectedCurrentAuthority: "physical:a/logical:a"},
		{Phase: "warm_noop", PhysicalRevision: "a", LogicalRevision: "a", SourceAction: "reuse", SearchAction: "reuse", ObservationAction: "reuse", CatalogAction: "reuse", RelationshipAction: "reuse", ExpectedCurrentAuthority: "physical:a/logical:a"},
		{Phase: "physical_delta_b", PhysicalRevision: "b", LogicalRevision: "a", SourceAction: "advance_one_file", SearchAction: "replace_with_old_generation_reader_lease", ObservationAction: "replace", CatalogAction: "rebind_actual_source", RelationshipAction: "replace", ExpectedCurrentAuthority: "physical:b/logical:a"},
		{Phase: "logical_delta_b", PhysicalRevision: "b", LogicalRevision: "b", SourceAction: "reuse", SearchAction: "reuse", ObservationAction: "reuse", CatalogAction: "replace_one_service_with_chunk_recovery", RelationshipAction: "replace", ExpectedCurrentAuthority: "physical:b/logical:b"},
		{Phase: "return_a", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "return_one_file", SearchAction: "replace_content_return", ObservationAction: "replace_content_return", CatalogAction: "replace_content_return", RelationshipAction: "replace_content_return_with_marker_recovery", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "stale_lease", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "reuse", SearchAction: "reuse", ObservationAction: "reuse_with_stale_partition_lease_recovery", CatalogAction: "reuse", RelationshipAction: "reuse", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "process_restart", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "reuse", SearchAction: "reuse", ObservationAction: "reuse_with_checkpointed_hard_restart", CatalogAction: "reuse", RelationshipAction: "reuse", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "pressure_80", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "reuse", SearchAction: "collect_noncurrent_then_revalidate", ObservationAction: "collect_noncurrent_then_revalidate", CatalogAction: "collect_noncurrent_then_revalidate", RelationshipAction: "collect_noncurrent_then_revalidate", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "pressure_90", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "reuse", SearchAction: "revalidate", ObservationAction: "revalidate", CatalogAction: "revalidate", RelationshipAction: "revalidate", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "pressure_75", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "reuse", SearchAction: "revalidate", ObservationAction: "revalidate", CatalogAction: "revalidate", RelationshipAction: "revalidate", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "archive_restore", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "restore_exact", SearchAction: "restore_exact", ObservationAction: "restore_exact", CatalogAction: "restore_actual_source_binding", RelationshipAction: "restore_exact", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "lifecycle_collection", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "reuse", SearchAction: "collect_noncurrent", ObservationAction: "collect_noncurrent", CatalogAction: "collect_noncurrent", RelationshipAction: "collect_noncurrent", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "product_queries", PhysicalRevision: "a-return", LogicalRevision: "a-return", SourceAction: "reuse", SearchAction: "query", ObservationAction: "revalidate", CatalogAction: "query", RelationshipAction: "query", ExpectedCurrentAuthority: "physical:a-return/logical:a-return"},
		{Phase: "teardown", SourceAction: "remove_scratch", SearchAction: "remove_derived", ObservationAction: "remove_derived", CatalogAction: "remove_derived", RelationshipAction: "remove_derived", ExpectedCurrentAuthority: "destroyed"},
	}
}

func frozenFailurePoints() []FailurePoint {
	return []FailurePoint{
		{Name: "partial_service_activation", Phase: "logical_delta_b", Boundary: "after_nonterminal_activation_chunk_commit_before_next_claim", Occurrence: 1, Trigger: "catalog_activation_exact_control_v1", TargetDomain: "catalog_activation_chunk", TargetKind: "service_member", TargetOrdinal: 9, TargetServiceOrdinal: 5_000, TargetServiceKeySHA256: SHA256([]byte(serviceKey(5_000))), TargetSourceStart: 4_608, TargetSourceEnd: 5_120, TargetMemberOrdinal: 9, TargetMemberStart: 0, TargetMemberEnd: 512, TargetBindingRecipe: "stable=sha256(native_selector,domain,generation,schedule,plan,unit);binding=sha256(phase,failure,boundary,stable,prior_authority,final_authority)-v4", PartialResidue: "one_committed_nonterminal_activation_chunk", PriorAuthority: "physical:b/logical:a", FinalAuthority: "physical:b/logical:b", RecoveryAction: "resume_activation_schedule_then_relationship_publication", RecoveryDeadlineMS: 4 * 60 * 60 * 1_000, LeavesTarget: true},
		{Name: "interrupted_publication", Phase: "return_a", Boundary: "after_marker_owned_generation_install_before_current_pointer", Occurrence: 1, Trigger: "relationship_publication_exact_control_v1", TargetDomain: "relationship_generation", TargetKind: "relationship_publication", TargetMemberOrdinal: -1, TargetBindingRecipe: "stable=sha256(native_selector,domain,generation,schedule,plan,unit);binding=sha256(phase,failure,boundary,stable,prior_authority,final_authority)-v4", PartialResidue: "one_complete_marker_owned_noncurrent_relationship_generation", PriorAuthority: "physical:b/logical:b", FinalAuthority: "physical:a-return/logical:a-return", RecoveryAction: "recover_marker_owned_then_publish_exact_relationship_root", RecoveryDeadlineMS: 4 * 60 * 60 * 1_000, LeavesTarget: true},
		{Name: "stale_partition_lease", Phase: "stale_lease", Boundary: "after_partition_job_lease_cutoff_before_stale_requeue", Occurrence: 1, Trigger: "partition_job_stale_lease_exact_control_v1", TargetDomain: "grpc-caller", TargetKind: candidate.PartitionKindCandidateMember, TargetOrdinal: 6, TargetCallerPrefix: "110", TargetSourceStart: 16_126, TargetSourceEnd: 18_830, TargetMemberOrdinal: 6, TargetBindingRecipe: "stable=sha256(native_selector,domain,generation,schedule,plan,unit);binding=sha256(phase,failure,boundary,stable,prior_authority,final_authority)-v4", PartialResidue: "one_stale_claimed_partition_job", PriorAuthority: "physical:a-return/logical:a-return", FinalAuthority: "physical:a-return/logical:a-return", RecoveryAction: "fence_stale_lease_requeue_once_then_complete", RecoveryDeadlineMS: 4 * 60 * 60 * 1_000, LeavesTarget: true},
		{Name: "checkpointed_hard_restart", Phase: "process_restart", Boundary: "after_partition_result_install_and_directory_sync_before_domain_assembly", Occurrence: 1, Trigger: "partition_checkpoint_exact_control_v2", TargetDomain: "proto-contract", TargetKind: candidate.PartitionKindCandidateMember, TargetOrdinal: 2, TargetSourceStart: 4_096, TargetSourceEnd: 6_144, TargetMemberOrdinal: 1, TargetMemberStart: 0, TargetMemberEnd: 2_048, TargetBindingRecipe: "stable=sha256(native_selector,domain,generation,schedule,plan,unit);binding=sha256(phase,failure,boundary,stable,prior_authority,final_authority)-v4", PartialResidue: "one_durable_partition_result_without_completion_or_root", PriorAuthority: "physical:a-return/logical:a-return", FinalAuthority: "physical:a-return/logical:a-return", RecoveryAction: "hard_kill_restart_reap_same_chunk_reuse_checkpoint_then_complete_generation", RecoveryDeadlineMS: 4 * 60 * 60 * 1_000, LeavesTarget: true},
	}
}

func frozenSafetyEnvelope() SafetyEnvelope {
	return SafetyEnvelope{
		MinimumMemoryBytes: 24 << 30, MinimumAvailableDiskBytes: 120 << 30,
		PressureVolumeBytes: 96 << 30,
		MaximumTotalWallMS:  18 * 60 * 60 * 1_000, MaximumPeakRSSBytes: 20 << 30,
		MaximumDataAllocatedBytes: 96 << 30, MaximumRetriesPerUnit: 5,
		ServerHealthDeadlineMS:      15 * 60 * 1_000,
		FullConvergenceDeadlineMS:   4 * 60 * 60 * 1_000,
		RevalidationDeadlineMS:      20 * 60 * 1_000,
		PressureUsedPercents:        []uint64{80, 90, 75},
		MaximumPressureBallastBytes: 80 << 30,
		MinimumPrePressureUsedBytes: 8 << 30,
		MaximumPrePressureUsedBytes: 68 << 30,
		MinimumPrePressureBytes:     8 << 30,
		MaximumPrePressureBytes:     68 << 30,
		PressureSameVolumeRequired:  true,
		PressureBallastFormula:      "collect_noncurrent_until_live_used_and_allocated_bytes_are_within_8_68_gib;no_padding;target_used_bytes-live_used_bytes;ballast_included_in_data_allocated_gauge",
		MinimumPressureMarginBytes:  8 << 30,
	}
}

func frozenToolPolicy() ToolPolicy {
	return ToolPolicy{
		DigestAlgorithm:       "sha256-executed-regular-file-v1",
		ExecutionFreezeSchema: ExecutionFreezeSchema, ExecutionProfileSchema: ExecutionProfileSchema,
		MaximumExecutionFreezeBytes: MaxExecutionFreezeBytes,
		RequireCleanCommit:          true,
		FreezeBeforeExecution:       true,
		PressureVolumePreparation: "hdiutil_sparse_apfs_exact_96_gib_on_admitted_backing_volume;" +
			"data_and_ballast_on_mounted_volume;statfs_identity_equal;detach_and_remove_image_after_teardown",
		BufModulePath:      "github.com/bufbuild/buf",
		BufModuleVersion:   "v1.72.0",
		BufModuleSum:       "h1:VMmGFtCLrxyS2wkpghExmhhiqJDdmc8DcwAvsGJGJ94=",
		BufBuildRecipe:     "go-build-trimpath-exact-module-graph:github.com/bufbuild/buf/cmd/buf",
		ZoektModulePath:    "github.com/sourcegraph/zoekt",
		ZoektModuleVersion: "v0.0.0-20260709064101-33f1f18af292",
		ZoektModuleSum:     "h1:HsoQyVl9olsjSqA0YddekTbCJOsfn9bUeu//bLg6fa8=",
		ZoektBuildRecipe:   "go-build-trimpath-exact-module-graph:github.com/sourcegraph/zoekt/cmd/zoekt-git-index",
		RequiredTools: []string{
			"buf", "git", "go", "hdiutil", "phebs", "phebs-focused-index", "ssh-keygen", "surreal",
			"t422-author", "t422-execute", "zoekt-git-index",
		},
		RequiredHostFields: []string{
			"backing_available_disk_bytes", "backing_total_disk_bytes", "backing_volume_identity",
			"ballast_volume_identity", "data_volume_identity", "goarch", "goos", "logical_cpus",
			"memory_bytes", "os_build_version", "os_product_version", "pressure_available_disk_bytes", "pressure_total_disk_bytes",
			"pressure_allocation_unit_bytes", "volume_identity_method",
		},
	}
}

func frozenStopRules() []StopRule {
	return []StopRule{
		{Priority: 1, Decision: "p6_investigation", Trigger: "typed_topology_stop_with_materialized_cartesian_owner_pairs_gt_zero_or_direct_recovery_topology_limit_and_clean_teardown"},
		{Priority: 2, Decision: "cohort_experiment", Trigger: "typed_resource_stop_at_one_frozen_runtime_ceiling_after_exact_completed_prefix_and_clean_teardown"},
		{Priority: 3, Decision: "continue", Trigger: "all_exact_functional_phases_and_clean_teardown_passed_and_no_frozen_ceiling_crossed"},
		{Priority: 4, Decision: "reduce", Trigger: "fallback_for_any_prerequisite_bound_authority_correctness_or_unclassified_refusal"},
	}
}

func frozenTeardownRule() TeardownRule {
	return TeardownRule{
		StopDescendants: true, CloseStore: true, RemoveDerivedCustody: true,
		RemoveScratchSource: true, RequireZeroChildren: true, RetainSourceFreeOnly: true,
	}
}

func frozenReceiptContract() ReceiptContract {
	return ReceiptContract{
		Schema: ReceiptSchema, MaximumBytes: MaxReceiptBytes,
		PhaseOutcomes:       []string{"not_run", "passed", "stopped"},
		RequiredSections:    slices.Clone(receiptSectionNames),
		RequiredMetrics:     slices.Clone(receiptMetricNames),
		StoppedSuffixNotRun: true, TeardownAlwaysRuns: true,
		TeardownPhaseOutcome:     "attempted",
		TeardownOutcomes:         []string{"clean", "failed"},
		TeardownFailureSchema:    "t422-teardown-failure-v1",
		ExecutionAdmissionSchema: "t422-freeze-admission-v1",
		ExecutionAdmissionOrder:  "verify_freeze_signature_and_checkout_before_first_phase_meter_or_child_launch",
		TypedFailureOnly:         true, CanonicalSourceFree: true,
		FailureObservationSchema:        "t422-failure-observation-v2",
		StateObservationSchema:          "t422-observed-phase-state-v4",
		StateProjectionSchema:           "t422-phase-native-state-projection-v2",
		StateProjectionCanonicalization: "canonical-json-semantic-projection-digest-plus-source-authority-recipe-and-native-authority-reader-bindings-v4",
		TransitionSchema:                "t422-exact-transition-v1",
		QueryTransportSchema:            "t422-query-transport-result-v2",
		MaterializedOwnerPairPhases: []string{
			"cold", "physical_delta_b", "logical_delta_b", "return_a",
			"stale_lease", "process_restart", "pressure_80", "pressure_90",
			"pressure_75", "archive_restore", "lifecycle_collection", "product_queries",
		},
		ExtractionDomains: []string{
			"grpc-caller", "grpc-consumer", "kafka-consumer", "kafka-producer", "proto-contract",
			"scip-proto-field", "thrift-caller", "thrift-consumer", "thrift-contract",
		},
	}
}

func frozenClaims() Claims {
	return Claims{
		Neutral: true, SourceFree: true, IndependentOracle: true,
		ChangesProductionBehavior: true,
		Gate2V2:                   "NOT_ESTABLISHED", ReleasePosture: "DO_NOT_RELEASE",
	}
}

func validateInputBindings(bindings []InputBinding) error {
	if !slices.IsSortedFunc(bindings, func(left, right InputBinding) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	}) {
		return errors.New("T42.1 input bindings are not ordered")
	}
	for _, binding := range bindings {
		if binding.Name == "" || binding.Kind == "sha256" && !validDigest(binding.Identity) ||
			binding.Kind == "git_commit" && !validCommit(binding.Identity) ||
			binding.Kind != "sha256" && binding.Kind != "git_commit" {
			return errors.New("T42.1 input binding is invalid")
		}
	}
	return nil
}

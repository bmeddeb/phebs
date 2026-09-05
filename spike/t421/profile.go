package t421

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/resolverinput"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/spike/t401"
	"github.com/bmeddeb/phebs/spike/t411"
)

const (
	combinedProfileSchema = "t421-combined-corpus-profile-v1"
	combinedProfileName   = "combined-2m-10k-v1"
	combinedProfileSeed   = "t421-neutral-combined-v1"
	overlayAlgorithm      = "t411-paths-with-member-local-t421-relationships-v2"

	combinedAuthorityID         = "t421-neutral-authority"
	combinedAuthorityA          = "a-v1"
	combinedAuthorityB          = "b-v1"
	combinedAuthorityAReturn    = "a-return-v1"
	combinedSemanticAuthority   = "semantic-v1"
	combinedOverrideID          = "t421-physical-complement"
	combinedOverrideVersion     = "v1"
	combinedSourcePath          = "/t421/catalog.json"
	catalogMeasurementCommit    = "0000000000000000000000000000000000000000"
	regularFileMode             = "100644"
	generatedMappingSchema      = "t421-generated-mapping-control-v1"
	typedIndexPath              = "index.scip"
	typedIndexKind              = "scip"
	catalogSourceSchema         = "t421-canonical-catalog-source-v1"
	retainedStructuralProfile   = "sha256:4227b0a75cc6a2cf1120e5d9e4c228fe23c0dbc2261313f513b6ae809364d430"
	structuralNonCandidateFiles = 1_999_999
	combinedEligibleGoFiles     = 21_601
)

// CombinedCorpus is the bounded in-memory control plane for the T42 gate. It
// retains the logical catalog, but never the 2,031,604-file corpus or any
// service-count-by-file-count expansion.
type CombinedCorpus struct {
	Profile          CombinedProfile
	Catalog          servicecatalog.Catalog
	LogicalRevisions []LogicalRevision
}

type overlaySummary struct {
	profile                  OverlayProfile
	combinedDistinctContents uint64
	inputFixtureContentBytes uint64
	goBytes                  uint64
	generatedGoBytes         uint64
	serviceGoBytes           uint64
	neutralGoBytes           uint64
	idlBytes                 uint64
	pathMap                  map[string]string
}

type logicalSummary struct {
	acceptedServices          uint64
	totalServices             uint64
	memberships               uint64
	distinctPaths             uint64
	unownedPaths              uint64
	catalogDistinctSelectors  uint64
	catalogUnownedEntries     uint64
	structuralUnownedPrefixes uint64
	distinctClaims            uint64
	duplicateRoleMemberships  uint64
	maxRolesPerClaim          uint64
	maxAcceptedFanout         uint64
	acceptedPhysicalPaths     uint64
	unownedPhysicalFiles      uint64
	roles                     []Count
}

// BuildCombinedCorpus composes the exact frozen T40 structural profile with
// the exact frozen T41 target catalog. Only the 31,600-file overlay is walked;
// the two-million-file structural corpus remains represented by its frozen
// profile and independent retained authority.
func BuildCombinedCorpus() (CombinedCorpus, error) {
	structural, err := frozenStructuralProfile()
	if err != nil {
		return CombinedCorpus{}, err
	}
	target, err := frozenTargetCorpus()
	if err != nil {
		return CombinedCorpus{}, err
	}
	mapping, mappingContent, err := buildGeneratedMappingControl()
	if err != nil {
		return CombinedCorpus{}, err
	}
	typedIndex, typedIndexContent := buildTypedIndexControl()
	overlay, err := summarizeOverlay(structural, target.Files, mappingContent, typedIndexContent)
	if err != nil {
		return CombinedCorpus{}, err
	}
	catalog, err := transformCatalog(target.Catalog, overlay.pathMap)
	if err != nil {
		return CombinedCorpus{}, err
	}
	logical, err := summarizeLogical(catalog)
	if err != nil {
		return CombinedCorpus{}, err
	}
	if err := validateCombinedShape(structural, overlay.profile, logical); err != nil {
		return CombinedCorpus{}, err
	}

	combinedRegularFiles := structural.Aggregate.RegularFiles + overlay.profile.RegularFiles + mapping.RegularFiles + typedIndex.RegularFiles
	extractionDomains := frozenExtractionDomains()
	var combinedModeledPartitions uint64
	for _, domain := range extractionDomains {
		combinedModeledPartitions += domain.ApplicablePartitions
	}
	if logical.acceptedPhysicalPaths > combinedRegularFiles {
		return CombinedCorpus{}, errors.New("T42.1 accepted paths exceed combined physical files")
	}
	censusDigest, err := combinedCensusDigest(structural, overlay.profile.PathModeContentSet, mapping, typedIndex)
	if err != nil {
		return CombinedCorpus{}, err
	}
	revisions, baseGeneration, err := buildLogicalRevisions(
		catalog, censusDigest, combinedRegularFiles, logical.acceptedPhysicalPaths,
	)
	if err != nil {
		return CombinedCorpus{}, err
	}
	inheritedClaimCopies, inheritedClaimBytes, err := inheritedPlacementClaims(baseGeneration)
	if err != nil {
		return CombinedCorpus{}, err
	}

	potentialPairs, err := checkedMultiply(combinedRegularFiles, logical.acceptedServices)
	if err != nil {
		return CombinedCorpus{}, fmt.Errorf("T42.1 potential Cartesian owner pairs: %w", err)
	}
	profile := CombinedProfile{
		Schema: combinedProfileSchema,
		Name:   combinedProfileName,
		Seed:   combinedProfileSeed,
		Physical: PhysicalProfile{
			StructuralPhysicalOwners:    structural.Aggregate.PhysicalOwners,
			StructuralRegularFiles:      structural.Aggregate.RegularFiles,
			StructuralEligibleGoFiles:   structural.Aggregate.EligibleGoFiles,
			StructuralNonCandidateFiles: structuralNonCandidateFiles,
			StructuralControlFiles:      structural.Aggregate.ControlFiles,
			UniqueStructuralBlobs:       structural.Aggregate.UniqueGoBlobs,
			StructuralModeledPartitions: structural.Aggregate.ModeledDomainPartitions,
			CombinedModeledPartitions:   combinedModeledPartitions,
			CombinedPhysicalOwners:      combinedRegularFiles,
			CombinedRegularFiles:        combinedRegularFiles,
			CombinedEligibleGoFiles:     combinedEligibleGoFiles,
			CombinedControlFiles:        structural.Aggregate.ControlFiles + mapping.RegularFiles,
			CombinedUniqueContentsA:     overlay.combinedDistinctContents,
			CombinedUniqueContentsB:     overlay.combinedDistinctContents + 1,
			CombinedUniqueContentsAR:    overlay.combinedDistinctContents,
			StructuralUnownedFiles:      structural.Aggregate.RegularFiles,
		},
		Logical: LogicalProfile{
			AcceptedFloor:                   t411.AcceptedServiceFloor,
			AcceptedServices:                logical.acceptedServices,
			TotalServiceRecords:             logical.totalServices,
			Memberships:                     logical.memberships,
			DistinctPaths:                   logical.distinctPaths,
			UnownedPaths:                    logical.unownedPaths,
			CatalogDistinctSelectors:        logical.catalogDistinctSelectors,
			CatalogUnownedEntries:           logical.catalogUnownedEntries,
			StructuralUnownedPrefixes:       logical.structuralUnownedPrefixes,
			AcceptedPhysicalFiles:           logical.acceptedPhysicalPaths,
			UnownedPhysicalFiles:            logical.unownedPhysicalFiles,
			DistinctServicePathClaims:       logical.distinctClaims,
			DuplicateRoleMemberships:        logical.duplicateRoleMemberships,
			InheritedPlacementClaimCopies:   inheritedClaimCopies,
			MaxRolesPerServicePathClaim:     logical.maxRolesPerClaim,
			MaxAcceptedPathFanout:           logical.maxAcceptedFanout,
			RoleMemberships:                 logical.roles,
			PotentialCartesianOwnerPairs:    potentialPairs,
			MaterializedCartesianOwnerPairs: 0,
		},
		Overlay:          overlay.profile,
		GeneratedMapping: mapping,
		TypedIndex:       typedIndex,
		Pipeline: PipelineProfile{
			SupportedGoFiles: combinedEligibleGoFiles, SupportedIDLFiles: 10_000,
			UnsupportedSourceFiles: 0,
			ProtoMessages:          20_000, ProtoServices: 10_000, ProtoOperations: 10_100,
			ResolverDeclarationRecords:  10_100,
			ResolverDeclarationLimit:    uint64(resolvermaterialize.MaxDeclarationRecords),
			ResolverDeclarationHeadroom: uint64(resolvermaterialize.MaxDeclarationRecords - 10_100),
			GeneratedMappings:           10_000,
			GeneratedMappingLimit:       uint64(resolvermaterialize.MaxGeneratedMappings),
			GeneratedMappingHeadroom:    uint64(resolvermaterialize.MaxGeneratedMappings - 10_000),
			GeneratedDescriptors:        10_100,
			RPCCallPostings:             10_999, KafkaProducerPostings: 500,
			KafkaConsumerPostings: 9_500, RelationshipProjections: 20_999,
			ServiceReferences:          31_998,
			ResolverFixedReadsPerBuild: 1, ResolverModuleReadsPerBuild: 1,
			ResolverGeneratedReadsPerBuild: 10_000, ResolverBlobReadsPerBuild: 10_002,
			ResolverBlobBytesPerBuild:  9_776_093,
			CandidateRepositoryMembers: 8,
			CandidateCallerLeaves:      8,
			MaximumCallerLeafRecords:   2_773,
			ExtractionDomains:          extractionDomains,
		},
		Bytes: ByteAccounting{
			StructuralDeclaredGoBytes:      structural.Aggregate.DeclaredRepositoryGoBytes,
			StructuralLogicalSourceBytes:   structural.Aggregate.LogicalSourceBytes,
			StructuralUniqueContentBytes:   structural.Aggregate.UniqueGoBlobs*structural.Shape.GoFileBytes + structural.Aggregate.ControlBytes,
			StructuralNonCandidateBytes:    structuralNonCandidateFiles * structural.Shape.GoFileBytes,
			CombinedObservationInputBytes:  structural.Aggregate.DeclaredRepositoryGoBytes - structuralNonCandidateFiles*structural.Shape.GoFileBytes + overlay.goBytes + overlay.idlBytes,
			OverlayGoBytes:                 overlay.goBytes,
			OverlayGeneratedGoBytes:        overlay.generatedGoBytes,
			OverlayServiceGoBytes:          overlay.serviceGoBytes,
			OverlayNeutralGoBytes:          overlay.neutralGoBytes,
			OverlayIDLBytes:                overlay.idlBytes,
			OverlayLogicalSourceBytes:      overlay.goBytes + overlay.idlBytes,
			GeneratedMappingControlBytes:   uint64(len(mappingContent)),
			TypedInputRegularFiles:         typedIndex.RegularFiles,
			TypedInputLogicalBytes:         uint64(len(typedIndexContent)),
			TypedInputUniqueContentBytes:   uint64(len(typedIndexContent)),
			CombinedLogicalSourceBytes:     structural.Aggregate.LogicalSourceBytes + overlay.goBytes + overlay.idlBytes + uint64(len(mappingContent)+len(typedIndexContent)),
			CombinedUniqueContentBytesA:    structural.Aggregate.UniqueGoBlobs*structural.Shape.GoFileBytes + structural.Aggregate.ControlBytes + overlay.goBytes + overlay.idlBytes + uint64(len(mappingContent)+len(typedIndexContent)),
			CombinedUniqueContentBytesB:    (structural.Aggregate.UniqueGoBlobs+1)*structural.Shape.GoFileBytes + structural.Aggregate.ControlBytes + overlay.goBytes + overlay.idlBytes + uint64(len(mappingContent)+len(typedIndexContent)),
			CombinedUniqueContentBytesAR:   structural.Aggregate.UniqueGoBlobs*structural.Shape.GoFileBytes + structural.Aggregate.ControlBytes + overlay.goBytes + overlay.idlBytes + uint64(len(mappingContent)+len(typedIndexContent)),
			InputFixtureContentBytes:       overlay.inputFixtureContentBytes,
			CatalogLogicalBytes:            uint64(baseGeneration.Root.LogicalBytes),
			CatalogMeasurementRootBytes:    uint64(baseGeneration.Root.RootBytes),
			CatalogMeasurementPathBytes:    uint64(len(combinedSourcePath)),
			CatalogServiceMemberBytes:      descriptorBytes(baseGeneration.Root.ServiceMembers),
			CatalogPlacementMemberBytes:    descriptorBytes(baseGeneration.Root.PlacementMembers),
			CatalogInheritedClaimBytes:     inheritedClaimBytes,
			CatalogMemberBytes:             uint64(baseGeneration.Root.EncodedMemberBytes),
			CatalogMeasurementEncodedBytes: uint64(baseGeneration.Root.EncodedBytes),
			AddedCartesianSourceBytes:      0,
			AllocatedBytesMeasured:         false,
		},
	}
	profile.Bytes.CombinedNonObservationBytes = profile.Bytes.CombinedLogicalSourceBytes - profile.Bytes.CombinedObservationInputBytes
	return CombinedCorpus{Profile: profile, Catalog: catalog, LogicalRevisions: revisions}, nil
}

func frozenExtractionDomains() []ExtractionDomainProfile {
	memberMaximum := uint64(candidate.MaxRecordsPerArtifact)
	caller := callerPartitionProfiles()
	grpcCaller := withExpectedResults(caller, map[int]ResultTotals{
		6: reservation(165, 330, 165, 176_694, 176_694),
	})
	goV2 := goPartitionProfiles(false)
	grpcConsumer := withExpectedResults(goV2, map[int]ResultTotals{
		0: reservation(1_243, 2_486, 1_243, 1_006_841, 1_006_841),
		1: reservation(2_047, 4_094, 2_047, 1_657_079, 1_657_079),
		2: reservation(2_047, 4_094, 2_047, 1_657_079, 1_657_079),
		3: reservation(2_047, 4_094, 2_047, 1_657_079, 1_657_079),
		4: reservation(2_047, 4_094, 2_047, 1_657_079, 1_657_079),
		5: reservation(1_519, 3_038, 1_519, 1_231_241, 1_231_241),
	})
	kafkaConsumer := withExpectedResults(goV2, map[int]ResultTotals{
		0: reservation(611, 1_222, 611, 533_294, 533_294),
		1: reservation(1_946, 3_892, 1_946, 1_697_968, 1_697_968),
		2: reservation(1_946, 3_892, 1_946, 1_697_968, 1_697_968),
		3: reservation(1_945, 3_890, 1_945, 1_697_096, 1_697_096),
		4: reservation(1_946, 3_892, 1_946, 1_697_968, 1_697_968),
		5: reservation(1_106, 2_212, 1_106, 965_178, 965_178),
	})
	goV3 := withExpectedResults(goPartitionProfiles(true), map[int]ResultTotals{
		0: reservation(33, 66, 33, 28_683, 28_683),
		1: reservation(102, 204, 102, 88_362, 88_362),
		2: reservation(102, 204, 102, 88_362, 88_362),
		3: reservation(103, 206, 103, 89_227, 89_227),
		4: reservation(102, 204, 102, 88_362, 88_362),
		5: reservation(58, 116, 58, 50_306, 50_306),
	})
	proto := withExpectedResults(protoPartitionProfiles(), map[int]ResultTotals{
		0: reservation(8_292, 16_584, 8_292, 7_021_783, 7_021_783),
		1: reservation(8_192, 16_384, 8_192, 6_912_150, 6_912_150),
		2: reservation(8_192, 16_384, 8_192, 6_912_150, 6_912_150),
		3: reservation(8_192, 16_384, 8_192, 6_912_150, 6_912_150),
		4: reservation(7_232, 14_464, 7_232, 6_102_231, 6_102_231),
	})
	scip := withExpectedResults(scipPartitionProfiles(), nil)
	callerNoFacts := withExpectedResults(caller, nil)
	goNoFacts := withExpectedResults(goV2, nil)
	result := []ExtractionDomainProfile{
		{Domain: "grpc-caller", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "admitted", CandidateRecords: 21_603, MaximumRecordsPerPartition: memberMaximum, MemberPartitions: 8, TypedPartitions: 1, TypedScopeRecords: 21_603, TypedScopePathBytes: 725_170, TypedScopeEncodedBytes: 3_123_135, TypedScopeRevisions: typedScopeRevisions("sha256:d75aa49d01dd6b23aed0569b2842c1892b9a45135c0e556df07ba15779e469c9", "sha256:a3f11740fd20ca5f7ade9c2e9732eed102efa2c383b01d458c1187f1b7d26043"), Partitions: grpcCaller},
		{Domain: "grpc-consumer", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "admitted", CandidateRecords: 21_601, MaximumRecordsPerPartition: memberMaximum, MemberPartitions: 6, Partitions: grpcConsumer},
		{Domain: "kafka-consumer", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "admitted", CandidateRecords: 21_601, MaximumRecordsPerPartition: memberMaximum, MemberPartitions: 6, Partitions: kafkaConsumer},
		{Domain: "kafka-producer", ResultPlanSchema: candidate.DomainResultPlanSchemaV3, Availability: "admitted", CandidateRecords: 21_601, MaximumRecordsPerPartition: memberMaximum, MemberPartitions: 6, Partitions: slices.Clone(goV3)},
		{Domain: "proto-contract", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "admitted", CandidateRecords: 10_000, MaximumRecordsPerPartition: 2_048, MemberPartitions: 5, Partitions: slices.Clone(proto)},
		{Domain: "scip-proto-field", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "admitted", CandidateRecords: 31_601, MaximumRecordsPerPartition: memberMaximum, MemberPartitions: 8, TypedPartitions: 1, TypedScopeRecords: 31_601, TypedScopePathBytes: 1_055_136, TypedScopeEncodedBytes: 4_562_879, TypedScopeRevisions: typedScopeRevisions("sha256:e4d398c6301efe6c3df57cdd0ac582315c76147c1ab381526f5ecbe5fcb2d494", "sha256:661c8775b1cf082a047bc8fde6bd6f8704bee245ae6b36286f75543776a02ddb"), Partitions: slices.Clone(scip)},
		{Domain: "thrift-caller", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "admitted", CandidateRecords: 21_603, MaximumRecordsPerPartition: memberMaximum, MemberPartitions: 8, TypedPartitions: 1, TypedScopeRecords: 21_603, TypedScopePathBytes: 725_170, TypedScopeEncodedBytes: 3_123_135, TypedScopeRevisions: typedScopeRevisions("sha256:d75aa49d01dd6b23aed0569b2842c1892b9a45135c0e556df07ba15779e469c9", "sha256:a3f11740fd20ca5f7ade9c2e9732eed102efa2c383b01d458c1187f1b7d26043"), Partitions: callerNoFacts},
		{Domain: "thrift-consumer", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "admitted", CandidateRecords: 21_601, MaximumRecordsPerPartition: memberMaximum, MemberPartitions: 6, Partitions: slices.Clone(goNoFacts)},
		{Domain: "thrift-contract", ResultPlanSchema: candidate.DomainResultPlanSchemaV2, Availability: "empty", MaximumRecordsPerPartition: 2_048},
	}
	for index := range result {
		result[index].ApplicablePartitions = result[index].MemberPartitions + result[index].TypedPartitions
		builder := newIdentityBuilder("t421-extraction-partition-shape-v1/" + result[index].Domain)
		for _, partition := range result[index].Partitions {
			_ = builder.add(partition)
			addResultTotals(&result[index].Reserved, partition.Reservation)
			addResultTotals(&result[index].Expected, partition.Expected)
		}
		result[index].PartitionShape = builder.finish()
	}
	return result
}

func typedScopeRevisions(a, b string) []TypedScopeRevision {
	return []TypedScopeRevision{
		{PhysicalRevision: "a", SHA256: a, DescriptorContentSHA256: a},
		{PhysicalRevision: "b", SHA256: b, DescriptorContentSHA256: b},
		{PhysicalRevision: "a-return", SHA256: a, DescriptorContentSHA256: a},
	}
}

func withExpectedResults(values []ExtractionPartitionProfile, semantic map[int]ResultTotals) []ExtractionPartitionProfile {
	result := slices.Clone(values)
	for index := range result {
		result[index].Expected.MemberBytes = result[index].Reservation.MemberBytes
		result[index].Expected.Members = result[index].Reservation.Members
		addResultTotals(&result[index].Expected, semantic[index])
	}
	return result
}

func addResultTotals(total *ResultTotals, value ResultTotals) {
	total.Facts += value.Facts
	total.Rows += value.Rows
	total.References += value.References
	total.CanonicalBytes += value.CanonicalBytes
	total.EncodedBytes += value.EncodedBytes
	total.MemberBytes += value.MemberBytes
	total.Members += value.Members
}

func reservation(facts, rows, references, canonical, encoded int64) ResultTotals {
	return ResultTotals{Facts: facts, Rows: rows, References: references, CanonicalBytes: canonical, EncodedBytes: encoded}
}

func callerPartitionProfiles() []ExtractionPartitionProfile {
	weights := []uint64{2_725, 2_709, 2_622, 2_629, 2_677, 2_764, 2_704, 2_773}
	prefixes := []string{"000", "001", "010", "011", "100", "101", "110", "111"}
	reservations := []ResultTotals{
		reservation(6_200, 12_400, 12_400, 8_464_713, 8_464_713),
		reservation(6_164, 12_327, 12_327, 8_415_012, 8_415_012),
		reservation(5_965, 11_930, 11_930, 8_144_762, 8_144_762),
		reservation(5_981, 11_962, 11_962, 8_166_506, 8_166_506),
		reservation(6_090, 12_182, 12_182, 8_315_609, 8_315_609),
		reservation(6_289, 12_577, 12_577, 8_585_860, 8_585_860),
		reservation(6_152, 12_304, 12_304, 8_399_480, 8_399_480),
		reservation(6_309, 12_618, 12_618, 8_613_816, 8_613_816),
	}
	memberBytes := []int64{1_012_177, 1_006_466, 974_062, 976_567, 994_449, 1_027_267, 1_004_210, 1_030_362}
	result := make([]ExtractionPartitionProfile, 0, 9)
	var sourceStart uint64
	for index, weight := range weights {
		reservations[index].MemberBytes, reservations[index].Members = memberBytes[index], 1
		result = append(result, ExtractionPartitionProfile{Ordinal: uint64(index), Kind: "candidate_member", MemberOrdinal: int64(index), CallerPrefix: prefixes[index], SourceStart: sourceStart, SourceEnd: sourceStart + weight, AdmittedRecords: weight, Reservation: reservations[index]})
		sourceStart += weight
	}
	result = append(result, ExtractionPartitionProfile{Ordinal: 8, Kind: "typed_input", MemberOrdinal: -1, SourceStart: sourceStart, SourceEnd: sourceStart, Reservation: reservation(2, 4, 4, 3_106, 3_106)})
	return result
}

func goPartitionProfiles(v3 bool) []ExtractionPartitionProfile {
	weights := []uint64{2_288, 4_096, 4_096, 4_096, 4_096, 2_929}
	members := []int64{2, 3, 4, 5, 6, 7}
	reservations := []ResultTotals{
		reservation(5_206, 10_412, 10_412, 7_108_239, 7_108_239),
		reservation(9_321, 18_641, 18_641, 12_725_240, 12_725_240),
		reservation(9_321, 18_641, 18_641, 12_725_240, 12_725_240),
		reservation(9_320, 18_641, 18_641, 12_725_240, 12_725_240),
		reservation(9_320, 18_640, 18_640, 12_725_240, 12_725_240),
		reservation(6_664, 13_329, 13_329, 9_099_665, 9_099_665),
	}
	if v3 {
		reservations = []ResultTotals{
			reservation(12_500, 25_000, 20_000, 28_432_957, 28_432_957),
			reservation(12_500, 25_000, 20_000, 50_900_960, 50_900_960),
			reservation(12_500, 25_000, 20_000, 50_900_960, 50_900_960),
			reservation(12_500, 25_000, 20_000, 50_900_960, 50_900_960),
			reservation(12_500, 25_000, 20_000, 50_900_960, 50_900_960),
			reservation(12_500, 25_000, 20_000, 36_398_659, 36_398_659),
		}
	}
	memberBytes := []int64{1_427_016, 1_607_680, 1_607_680, 1_607_680, 1_607_680, 1_145_081}
	result := make([]ExtractionPartitionProfile, len(weights))
	var sourceStart uint64
	for index, weight := range weights {
		reservations[index].MemberBytes, reservations[index].Members = memberBytes[index], 1
		result[index] = ExtractionPartitionProfile{Ordinal: uint64(index), Kind: "candidate_member", MemberOrdinal: members[index], SourceStart: sourceStart, SourceEnd: sourceStart + weight, AdmittedRecords: weight, Reservation: reservations[index]}
		sourceStart += weight
	}
	return result
}

func protoPartitionProfiles() []ExtractionPartitionProfile {
	result := []ExtractionPartitionProfile{
		{Ordinal: 0, Kind: "candidate_member", MemberOrdinal: 0, SourceEnd: 2_048, MemberRecordEnd: 2_048, AdmittedRecords: 2_048, Reservation: reservation(10_067, 20_133, 20_000, 13_743_896, 13_743_896)},
		{Ordinal: 1, Kind: "candidate_member", MemberOrdinal: 0, SourceStart: 2_048, SourceEnd: 4_096, MemberRecordStart: 2_048, MemberRecordEnd: 4_096, AdmittedRecords: 2_048, Reservation: reservation(10_067, 20_133, 20_000, 13_743_896, 13_743_896)},
		{Ordinal: 2, Kind: "candidate_member", MemberOrdinal: 1, SourceStart: 4_096, SourceEnd: 6_144, MemberRecordEnd: 2_048, AdmittedRecords: 2_048, Reservation: reservation(10_066, 20_133, 20_000, 13_743_895, 13_743_895)},
		{Ordinal: 3, Kind: "candidate_member", MemberOrdinal: 1, SourceStart: 6_144, SourceEnd: 8_192, MemberRecordStart: 2_048, MemberRecordEnd: 4_096, AdmittedRecords: 2_048, Reservation: reservation(10_066, 20_132, 20_000, 13_743_895, 13_743_895)},
		{Ordinal: 4, Kind: "candidate_member", MemberOrdinal: 2, SourceStart: 8_192, SourceEnd: 10_000, MemberRecordEnd: 1_808, AdmittedRecords: 1_808, Reservation: reservation(8_886, 17_773, 18_304, 12_133_282, 12_133_282)},
	}
	memberBytes := []int64{1_187_840, 1_187_840, 1_187_840, 1_187_840, 1_427_016}
	for index := range result {
		result[index].Reservation.MemberBytes, result[index].Reservation.Members = memberBytes[index], 1
	}
	return result
}

func scipPartitionProfiles() []ExtractionPartitionProfile {
	reservations := []ResultTotals{
		reservation(6_371, 12_742, 12_742, 8_698_118, 8_698_118),
		reservation(6_371, 12_742, 12_742, 8_698_118, 8_698_118),
		reservation(6_371, 12_742, 12_742, 8_698_118, 8_698_118),
		reservation(6_371, 12_741, 12_741, 8_698_118, 8_698_118),
		reservation(6_371, 12_741, 12_741, 8_698_117, 8_698_117),
		reservation(6_371, 12_741, 12_741, 8_698_117, 8_698_117),
		reservation(6_370, 12_741, 12_741, 8_698_117, 8_698_117),
		reservation(4_555, 9_111, 9_111, 6_219_918, 6_219_918),
	}
	memberBytes := []int64{1_187_840, 1_187_840, 1_427_016, 1_607_680, 1_607_680, 1_607_680, 1_607_680, 1_145_081}
	result := make([]ExtractionPartitionProfile, 0, 9)
	var sourceStart uint64
	for index := range 8 {
		weight := uint64(4_096)
		if index == 7 {
			weight = 2_929
		}
		reservations[index].MemberBytes, reservations[index].Members = memberBytes[index], 1
		result = append(result, ExtractionPartitionProfile{Ordinal: uint64(index), Kind: "candidate_member", MemberOrdinal: int64(index), SourceStart: sourceStart, SourceEnd: sourceStart + weight, AdmittedRecords: weight, Reservation: reservations[index]})
		sourceStart += weight
	}
	result = append(result, ExtractionPartitionProfile{Ordinal: 8, Kind: "typed_input", MemberOrdinal: -1, SourceStart: sourceStart, SourceEnd: sourceStart, Reservation: reservation(1, 3, 3, 2_123, 2_123)})
	return result
}

func buildTypedIndexControl() (TypedIndexProfile, []byte) {
	raw := []byte{0x0a, 0x00} // deterministic protobuf: scip.Index{Metadata: &scip.Metadata{}}
	return TypedIndexProfile{
		Kind: typedIndexKind, Path: typedIndexPath, RegularFiles: 1,
		ContentBytes: uint64(len(raw)), ContentSHA256: SHA256(raw),
	}, raw
}

func descriptorBytes(descriptors []servicecatalogv3.MemberDescriptor) uint64 {
	var total uint64
	for _, descriptor := range descriptors {
		total += uint64(descriptor.ContentBytes)
	}
	return total
}

func inheritedPlacementClaims(generation servicecatalogv3.Generation) (uint64, uint64, error) {
	var descriptorClaims, decodedClaims, claimBytes uint64
	for _, descriptor := range generation.Root.PlacementMembers {
		descriptorClaims += uint64(descriptor.PreludeClaims)
	}
	for _, encoded := range generation.Members {
		if encoded.Kind != "placement" {
			continue
		}
		var member servicecatalogv3.PlacementMember
		if err := json.Unmarshal(encoded.Content, &member); err != nil {
			return 0, 0, fmt.Errorf("decode T42.1 placement member: %w", err)
		}
		for _, placement := range member.Inherited {
			decodedClaims += uint64(len(placement.Claims))
			for _, claim := range placement.Claims {
				raw, err := json.Marshal(claim)
				if err != nil {
					return 0, 0, fmt.Errorf("measure T42.1 inherited claim: %w", err)
				}
				claimBytes += uint64(len(raw))
			}
		}
	}
	if decodedClaims != descriptorClaims {
		return 0, 0, errors.New("T42.1 inherited placement claims disagree with descriptors")
	}
	return decodedClaims, claimBytes, nil
}

// WalkOverlay streams the transformed T41 overlay in canonical path order.
// The supplied byte slice is valid for the duration of the callback; this
// function retains neither transformed contents nor a second fixture array.
func WalkOverlay(visit func(path string, content []byte) error) error {
	if visit == nil {
		return errors.New("T42.1 overlay visitor is nil")
	}
	target, err := frozenTargetCorpus()
	if err != nil {
		return err
	}
	return walkOverlayFiles(target.Files, func(_, path string, _, content []byte, _ bool) error {
		return visit(path, content)
	})
}

// WalkCombinedAdditions streams the generated mapping control and transformed
// overlay in the exact path order used by the combined source recipe.
func WalkCombinedAdditions(visit func(path string, content []byte) error) error {
	if visit == nil {
		return errors.New("T42.1 combined-additions visitor is nil")
	}
	_, mapping, err := buildGeneratedMappingControl()
	if err != nil {
		return err
	}
	_, typedIndex := buildTypedIndexControl()
	target, err := frozenTargetCorpus()
	if err != nil {
		return err
	}
	controls := []struct {
		path    string
		content []byte
	}{
		{path: resolverinput.GeneratedFromSnapshotPath, content: mapping},
		{path: typedIndexPath, content: typedIndex},
	}
	control := 0
	err = walkOverlayFiles(target.Files, func(_, path string, _, content []byte, _ bool) error {
		for control < len(controls) && controls[control].path < path {
			if err := visit(controls[control].path, controls[control].content); err != nil {
				return err
			}
			control++
		}
		return visit(path, content)
	})
	if err != nil {
		return err
	}
	for ; control < len(controls); control++ {
		if err := visit(controls[control].path, controls[control].content); err != nil {
			return err
		}
	}
	return nil
}

func buildGeneratedMappingControl() (GeneratedMappingProfile, []byte, error) {
	mappings := make([]resolverinput.GeneratedFromMapping, combinedServices)
	for service := range combinedServices {
		mappings[service] = resolverinput.GeneratedFromMapping{
			Protocol:              "grpc",
			GeneratedPath:         fmt.Sprintf("services/service-%05d/api_grpc.pb.go", service),
			GeneratorRelativePath: fmt.Sprintf("contracts/service-%05d/api.proto", service),
			DeclarationPath:       fmt.Sprintf("contracts/service-%05d/api.proto", service),
		}
	}
	raw, err := json.Marshal(resolverinput.GeneratedFromSnapshot{
		Version: resolverinput.GeneratedFromSnapshotVersion, Mappings: mappings,
	})
	if err != nil {
		return GeneratedMappingProfile{}, nil, err
	}
	if len(raw) != 1_940_048 {
		return GeneratedMappingProfile{}, nil, fmt.Errorf("T42.1 generated mapping bytes = %d, want 1940048", len(raw))
	}
	return GeneratedMappingProfile{
		Schema: generatedMappingSchema, Records: uint64(len(mappings)), RegularFiles: 1,
		ContentBytes: uint64(len(raw)), ContentSHA256: SHA256(raw),
	}, raw, nil
}

func frozenTargetCorpus() (t411.Corpus, error) {
	target, err := t411.BuildTargetCorpus()
	if err != nil {
		return t411.Corpus{}, fmt.Errorf("build frozen T41 target corpus: %w", err)
	}
	if target.ProfileDigest != t411.RetainedTargetProfileSHA256 {
		return t411.Corpus{}, errors.New("T42.1 T41 target profile identity drifted")
	}
	return target, nil
}

func frozenStructuralProfile() (t401.Profile, error) {
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		return t401.Profile{}, fmt.Errorf("build frozen T40 profiles: %w", err)
	}
	for _, profile := range profiles {
		if profile.Name == t401.StructuralProfileName {
			if err := t401.ValidateProfile(profile); err != nil {
				return t401.Profile{}, fmt.Errorf("validate frozen T40 structural profile: %w", err)
			}
			digest, err := t401.ProfileDigest(profile)
			if err != nil || digest != retainedStructuralProfile {
				return t401.Profile{}, errors.New("T42.1 T40 structural profile identity drifted")
			}
			return profile, nil
		}
	}
	return t401.Profile{}, errors.New("T42.1 frozen T40 structural profile is absent")
}

func summarizeOverlay(
	structural t401.Profile,
	files []t411.FixtureFile,
	mappingControl, typedIndexControl []byte,
) (overlaySummary, error) {
	digest := sha256.New()
	contentDigests := make(map[[sha256.Size]byte]struct{}, len(files))
	combinedDigests := make(map[[sha256.Size]byte]struct{}, len(files)+514)
	for ordinal := range structural.Aggregate.UniqueGoBlobs {
		_, content, err := t401.FrozenStructuralGoFixture(structural, ordinal)
		if err != nil {
			return overlaySummary{}, err
		}
		if bytes.Count(content, []byte("T401Fixture")) != 1 {
			return overlaySummary{}, errors.New("T42.1 structural query marker is not exact")
		}
		combinedDigests[sha256.Sum256(content)] = struct{}{}
	}
	_, goMod, err := t401.FrozenCallerControlFixture(structural)
	if err != nil {
		return overlaySummary{}, err
	}
	combinedDigests[sha256.Sum256(goMod)] = struct{}{}
	combinedDigests[sha256.Sum256([]byte(t401.EnvelopeSchema+"\n"))] = struct{}{}
	combinedDigests[sha256.Sum256(mappingControl)] = struct{}{}
	combinedDigests[sha256.Sum256(typedIndexControl)] = struct{}{}
	pathMap := make(map[string]string, len(files))
	var records, framedBytes, inputBytes, goBytes, generatedGoBytes, serviceGoBytes, neutralGoBytes, idlBytes, relationships uint64
	err = walkOverlayFiles(files, func(originalPath, path string, original, content []byte, relationship bool) error {
		pathMap[originalPath] = path
		inputBytes += uint64(len(original))
		records++
		framedBytes += writeFrame(digest, []byte(path))
		framedBytes += writeFrame(digest, []byte(regularFileMode))
		framedBytes += writeFrame(digest, content)
		contentDigests[sha256.Sum256(content)] = struct{}{}
		combinedDigests[sha256.Sum256(content)] = struct{}{}
		switch {
		case strings.HasSuffix(path, ".go"):
			goBytes += uint64(len(content))
			switch {
			case strings.HasPrefix(originalPath, "generated/service-"):
				generatedGoBytes += uint64(len(content))
			case strings.HasPrefix(path, "services/service-"):
				serviceGoBytes += uint64(len(content))
			default:
				neutralGoBytes += uint64(len(content))
			}
		case strings.HasSuffix(path, ".proto"), strings.HasSuffix(path, ".thrift"):
			idlBytes += uint64(len(content))
		default:
			return fmt.Errorf("T42.1 overlay path %q is neither Go nor IDL", path)
		}
		if relationship {
			relationships++
		}
		return nil
	})
	if err != nil {
		return overlaySummary{}, err
	}
	return overlaySummary{
		profile: OverlayProfile{
			Algorithm:         overlayAlgorithm,
			RegularFiles:      records,
			GoFiles:           countOverlaySuffix(pathMap, ".go"),
			IDLFiles:          records - countOverlaySuffix(pathMap, ".go"),
			DistinctContents:  uint64(len(contentDigests)),
			RelationshipFiles: relationships,
			PathModeContentSet: SetIdentity{
				Records: records, FramedBytes: framedBytes,
				SHA256: "sha256:" + hex.EncodeToString(digest.Sum(nil)),
			},
		},
		combinedDistinctContents: uint64(len(combinedDigests)),
		inputFixtureContentBytes: inputBytes,
		goBytes:                  goBytes,
		generatedGoBytes:         generatedGoBytes,
		serviceGoBytes:           serviceGoBytes,
		neutralGoBytes:           neutralGoBytes,
		idlBytes:                 idlBytes,
		pathMap:                  pathMap,
	}, nil
}

func walkOverlayFiles(
	files []t411.FixtureFile,
	visit func(originalPath, combinedPath string, original, content []byte, relationship bool) error,
) error {
	seen := make(map[string]struct{}, len(files))
	generated := make([]int, relationshipServiceCount)
	for index := range generated {
		generated[index] = -1
	}
	previous := ""
	emit := func(file t411.FixtureFile) error {
		combinedPath, content, relationship, err := combinedOverlayFile(file.Path, file.Content)
		if err != nil {
			return fmt.Errorf("transform T42.1 overlay %q: %w", file.Path, err)
		}
		if combinedPath == "" || len(content) == 0 {
			return fmt.Errorf("T42.1 overlay %q produced an empty path or content", file.Path)
		}
		wantPath := file.Path
		if strings.HasPrefix(file.Path, "generated/") && strings.HasSuffix(file.Path, "/client.pb.go") {
			service, err := parseRelationshipServicePath(file.Path, "generated/service-", "/client.pb.go")
			if err != nil {
				return err
			}
			wantPath = fmt.Sprintf("services/service-%05d/api_grpc.pb.go", service)
		}
		if combinedPath != wantPath {
			return fmt.Errorf("T42.1 overlay path %q became %q, want %q", file.Path, combinedPath, wantPath)
		}
		if _, duplicate := seen[combinedPath]; duplicate {
			return fmt.Errorf("T42.1 overlay repeats transformed path %q", combinedPath)
		}
		if previous != "" && combinedPath <= previous {
			return fmt.Errorf("T42.1 overlay path order is not canonical at %q", combinedPath)
		}
		seen[combinedPath] = struct{}{}
		previous = combinedPath
		if err := visit(file.Path, combinedPath, file.Content, content, relationship); err != nil {
			return err
		}
		return nil
	}
	for index, file := range files {
		if strings.HasPrefix(file.Path, "generated/service-") && strings.HasSuffix(file.Path, "/client.pb.go") {
			service, err := parseRelationshipServicePath(file.Path, "generated/service-", "/client.pb.go")
			if err != nil || generated[service] >= 0 {
				return errors.New("T42.1 generated service fixture is invalid or duplicated")
			}
			generated[service] = index
			continue
		}
		if strings.HasPrefix(file.Path, "services/service-") && strings.HasSuffix(file.Path, "/main.go") {
			service, err := parseRelationshipServicePath(file.Path, "services/service-", "/main.go")
			if err != nil || generated[service] < 0 {
				return errors.New("T42.1 service fixture lacks its generated peer")
			}
			if err := emit(files[generated[service]]); err != nil {
				return err
			}
		}
		if err := emit(file); err != nil {
			return err
		}
	}
	if slices.Contains(generated, -1) {
		return errors.New("T42.1 generated service fixture inventory is incomplete")
	}
	return nil
}

func countOverlaySuffix(paths map[string]string, suffix string) uint64 {
	var count uint64
	for _, path := range paths {
		if strings.HasSuffix(path, suffix) {
			count++
		}
	}
	return count
}

func transformCatalog(input servicecatalog.Catalog, pathMap map[string]string) (servicecatalog.Catalog, error) {
	catalog := cloneCatalog(input)
	catalog.Authority = servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: combinedAuthorityID, Version: combinedAuthorityA,
	}
	catalog.Override = &servicecatalog.OperatorOverride{
		ID: combinedOverrideID, Version: combinedOverrideVersion,
	}
	for index := range catalog.Memberships {
		path, ok := pathMap[catalog.Memberships[index].Path]
		if !ok {
			return servicecatalog.Catalog{}, fmt.Errorf("T42.1 membership path %q has no overlay file", catalog.Memberships[index].Path)
		}
		catalog.Memberships[index].Path = path
	}
	for index := range catalog.Unowned {
		path, ok := pathMap[catalog.Unowned[index].Path]
		if !ok {
			return servicecatalog.Catalog{}, fmt.Errorf("T42.1 unowned path %q has no overlay file", catalog.Unowned[index].Path)
		}
		catalog.Unowned[index].Path = path
	}
	for _, path := range []string{".phebs", "go.mod", "structural"} {
		catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
			Path: path, Origin: servicecatalog.OriginOverride,
		})
	}
	catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
		Path: resolverinput.GeneratedFromSnapshotPath, Origin: servicecatalog.OriginBase,
	})
	catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
		Path: typedIndexPath, Origin: servicecatalog.OriginBase,
	})
	sortCatalog(&catalog)
	if err := servicecatalogv3.ValidateCatalog(catalog); err != nil {
		return servicecatalog.Catalog{}, fmt.Errorf("validate transformed T42.1 catalog: %w", err)
	}
	return catalog, nil
}

func summarizeLogical(catalog servicecatalog.Catalog) (logicalSummary, error) {
	dispositions := make(map[string]string, len(catalog.Services))
	var accepted uint64
	for _, service := range catalog.Services {
		dispositions[service.Key] = service.Disposition
		if service.Disposition == servicecatalog.DispositionAccepted {
			accepted++
		}
	}
	type claimKey struct{ service, path string }
	claims := make(map[claimKey]map[string]struct{}, len(catalog.Memberships))
	fanout := make(map[string]map[string]struct{})
	distinctPaths := make(map[string]struct{}, len(catalog.Memberships)+len(catalog.Unowned))
	roleCounts := map[string]uint64{}
	for _, membership := range catalog.Memberships {
		key := claimKey{service: membership.ServiceKey, path: membership.Path}
		roles := claims[key]
		if roles == nil {
			roles = map[string]struct{}{}
			claims[key] = roles
		}
		roles[membership.Role] = struct{}{}
		roleCounts[membership.Role]++
		distinctPaths[membership.Path] = struct{}{}
		if dispositions[membership.ServiceKey] == servicecatalog.DispositionAccepted {
			services := fanout[membership.Path]
			if services == nil {
				services = map[string]struct{}{}
				fanout[membership.Path] = services
			}
			services[membership.ServiceKey] = struct{}{}
		}
	}
	for _, unowned := range catalog.Unowned {
		distinctPaths[unowned.Path] = struct{}{}
	}
	var overlayUnowned, structuralPrefixes uint64
	for _, unowned := range catalog.Unowned {
		switch unowned.Origin {
		case servicecatalog.OriginBase:
			overlayUnowned++
		case servicecatalog.OriginOverride:
			structuralPrefixes++
		}
	}
	var maxRoles, maxFanout uint64
	for _, roles := range claims {
		maxRoles = max(maxRoles, uint64(len(roles)))
	}
	for _, services := range fanout {
		maxFanout = max(maxFanout, uint64(len(services)))
	}
	roles := make([]Count, 0, 5)
	for _, name := range []string{
		servicecatalog.RoleGenerated, servicecatalog.RolePrimary, servicecatalog.RoleShared,
		servicecatalog.RoleSupporting, servicecatalog.RoleTyped,
	} {
		roles = append(roles, Count{Name: name, Count: roleCounts[name]})
	}
	return logicalSummary{
		acceptedServices:          accepted,
		totalServices:             uint64(len(catalog.Services)),
		memberships:               uint64(len(catalog.Memberships)),
		distinctPaths:             uint64(len(fanout)) + overlayUnowned,
		unownedPaths:              overlayUnowned,
		catalogDistinctSelectors:  uint64(len(distinctPaths)),
		catalogUnownedEntries:     uint64(len(catalog.Unowned)),
		structuralUnownedPrefixes: structuralPrefixes,
		distinctClaims:            uint64(len(claims)),
		duplicateRoleMemberships:  uint64(len(catalog.Memberships) - len(claims)),
		maxRolesPerClaim:          maxRoles,
		maxAcceptedFanout:         maxFanout,
		acceptedPhysicalPaths:     uint64(len(fanout)),
		unownedPhysicalFiles:      2_000_104,
		roles:                     roles,
	}, nil
}

func buildLogicalRevisions(
	base servicecatalog.Catalog,
	censusDigest string,
	combinedFiles, acceptedFiles uint64,
) ([]LogicalRevision, servicecatalogv3.Generation, error) {
	a, err := logicalCatalogForRevision(base, "a")
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	b, err := logicalCatalogForRevision(base, "b")
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	aReturn, err := logicalCatalogForRevision(base, "a-return")
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}

	aGeneration, err := buildCatalogGeneration(a, censusDigest, combinedFiles, acceptedFiles)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, fmt.Errorf("build T42.1 logical revision a: %w", err)
	}
	bGeneration, err := buildCatalogGeneration(b, censusDigest, combinedFiles, acceptedFiles)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, fmt.Errorf("build T42.1 logical revision b: %w", err)
	}
	aReturnGeneration, err := buildCatalogGeneration(aReturn, censusDigest, combinedFiles, acceptedFiles)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, fmt.Errorf("build T42.1 logical revision a-return: %w", err)
	}
	aSemantic, err := semanticCatalogDigest(a, censusDigest, combinedFiles, acceptedFiles)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	bSemantic, err := semanticCatalogDigest(b, censusDigest, combinedFiles, acceptedFiles)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	if aSemantic == bSemantic || aGeneration.Root.Digest == bGeneration.Root.Digest ||
		aGeneration.Root.Digest == aReturnGeneration.Root.Digest {
		return nil, servicecatalogv3.Generation{}, errors.New("T42.1 logical revisions do not carry distinct authority/content fences")
	}
	aSource, err := catalogSourceIdentity(a)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	bSource, err := catalogSourceIdentity(b)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	aReturnSource, err := catalogSourceIdentity(aReturn)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	aOracle, err := deriveAuthoredOracle(a)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	bOracle, err := deriveAuthoredOracle(b)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	aReturnOracle, err := deriveAuthoredOracle(aReturn)
	if err != nil {
		return nil, servicecatalogv3.Generation{}, err
	}
	revisions := []LogicalRevision{
		logicalRevision("a", "", "baseline", 0, aGeneration.Root.LogicalDigest, aSemantic, aSource, aOracle),
		logicalRevision("b", "a", "bounded_delta", 1, bGeneration.Root.LogicalDigest, bSemantic, bSource, bOracle),
		logicalRevision("a-return", "b", "content_return", 1, aReturnGeneration.Root.LogicalDigest, aSemantic, aReturnSource, aReturnOracle),
	}
	return revisions, aGeneration, nil
}

// The authoring identity and protected runtime catalogs use this same fixed
// logical transition; neither creates a new physical generation here.
func logicalCatalogForRevision(base servicecatalog.Catalog, revision string) (servicecatalog.Catalog, error) {
	if len(base.Services) == 0 {
		return servicecatalog.Catalog{}, errors.New("T42.1 logical catalog is empty")
	}
	catalog := cloneCatalog(base)
	switch revision {
	case "a":
		catalog.Authority.Version = combinedAuthorityA
	case "b":
		catalog.Authority.Version = combinedAuthorityB
		catalog.Services[len(catalog.Services)/2].DisplayName += "-b"
	case "a-return":
		catalog.Authority.Version = combinedAuthorityAReturn
	default:
		return servicecatalog.Catalog{}, errors.New("T42.1 logical revision is unknown")
	}
	return catalog, nil
}

func logicalRevision(
	name, parent, posture string,
	changed uint64,
	logicalSHA256, semanticSHA256 string,
	source CatalogSourceProfile,
	oracle Oracle,
) LogicalRevision {
	return LogicalRevision{
		Name: name, Parent: parent, Posture: posture, ChangedLogicalServices: changed,
		CatalogLogicalSHA256: logicalSHA256, SemanticSHA256: semanticSHA256, CatalogSource: source,
		Catalog: oracle.Catalog, Memberships: oracle.Memberships, Placements: oracle.Placements,
		UnownedPrefixes: oracle.UnownedPrefixes, ServiceQueries: oracle.ServiceQueries,
	}
}

func catalogSourceIdentity(catalog servicecatalog.Catalog) (CatalogSourceProfile, error) {
	raw, err := json.Marshal(catalog)
	if err != nil {
		return CatalogSourceProfile{}, fmt.Errorf("encode T42.1 catalog source: %w", err)
	}
	return CatalogSourceProfile{
		Schema:  catalogSourceSchema,
		Records: uint64(len(catalog.Services) + len(catalog.Memberships) + len(catalog.Unowned)),
		Bytes:   uint64(len(raw)), SHA256: SHA256(raw),
	}, nil
}

func buildCatalogGeneration(
	catalog servicecatalog.Catalog,
	censusDigest string,
	combinedFiles, acceptedFiles uint64,
) (servicecatalogv3.Generation, error) {
	if combinedFiles > uint64(^uint(0)>>1) || acceptedFiles > combinedFiles {
		return servicecatalogv3.Generation{}, errors.New("T42.1 source census count exceeds platform integer range")
	}
	binding := servicecatalogv3.Binding{
		Repository: t401.RepositoryName,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator,
			Path: combinedSourcePath,
			// This build measures exact-width root/member bytes only. T42.2
			// replaces the zero commit with the authored combined Git OID.
			Commit:            catalogMeasurementCommit,
			CensusDigest:      censusDigest,
			FileCount:         int(combinedFiles),
			AcceptedFileCount: int(acceptedFiles),
			UnownedFileCount:  int(combinedFiles - acceptedFiles),
		},
		Authority: catalog.Authority,
		Override:  catalog.Override,
	}
	generation, err := servicecatalogv3.Build(binding, catalog)
	if err != nil {
		return servicecatalogv3.Generation{}, err
	}
	if err := servicecatalogv3.ValidateGeneration(generation); err != nil {
		return servicecatalogv3.Generation{}, err
	}
	return generation, nil
}

func semanticCatalogDigest(
	catalog servicecatalog.Catalog,
	censusDigest string,
	combinedFiles, acceptedFiles uint64,
) (string, error) {
	semantic := cloneCatalog(catalog)
	semantic.Authority.Version = combinedSemanticAuthority
	generation, err := buildCatalogGeneration(semantic, censusDigest, combinedFiles, acceptedFiles)
	if err != nil {
		return "", fmt.Errorf("build T42.1 authority-neutral semantic catalog: %w", err)
	}
	return generation.Root.LogicalDigest, nil
}

func combinedCensusDigest(
	structural t401.Profile,
	overlay SetIdentity,
	mapping GeneratedMappingProfile,
	typed TypedIndexProfile,
) (string, error) {
	profileDigest, err := t401.ProfileDigest(structural)
	if err != nil {
		return "", fmt.Errorf("digest frozen T40 structural profile: %w", err)
	}
	framed := sha256.New()
	writeFrame(framed, []byte("t421-combined-source-census-v1"))
	writeFrame(framed, []byte(profileDigest))
	writeFrame(framed, []byte(overlay.SHA256))
	writeFrame(framed, []byte(resolverinput.GeneratedFromSnapshotPath))
	writeFrame(framed, []byte(regularFileMode))
	writeFrame(framed, []byte(mapping.ContentSHA256))
	writeFrame(framed, []byte(fmt.Sprint(mapping.ContentBytes)))
	writeFrame(framed, []byte(typed.Kind))
	writeFrame(framed, []byte(typed.Path))
	writeFrame(framed, []byte(regularFileMode))
	writeFrame(framed, []byte(typed.ContentSHA256))
	writeFrame(framed, []byte(fmt.Sprint(typed.ContentBytes)))
	return "sha256:" + hex.EncodeToString(framed.Sum(nil)), nil
}

func validateCombinedShape(structural t401.Profile, overlay OverlayProfile, logical logicalSummary) error {
	if structural.Aggregate.PhysicalOwners != 2_000_002 || structural.Aggregate.RegularFiles != 2_000_002 ||
		structural.Aggregate.EligibleGoFiles != 2_000_000 || structural.Aggregate.ControlFiles != 2 ||
		structural.Aggregate.UniqueGoBlobs != 512 || structural.Shape.GoFileBytes != 4_608 ||
		structural.Aggregate.ControlBytes != 76 {
		return errors.New("T42.1 frozen structural shape drifted")
	}
	if overlay.RegularFiles != 31_600 || overlay.GoFiles != 21_600 || overlay.IDLFiles != 10_000 ||
		overlay.RegularFiles != overlay.GoFiles+overlay.IDLFiles || overlay.DistinctContents == 0 ||
		overlay.RelationshipFiles == 0 || overlay.PathModeContentSet.Records != overlay.RegularFiles ||
		!validDigest(overlay.PathModeContentSet.SHA256) {
		return errors.New("T42.1 overlay shape drifted")
	}
	if logical.acceptedServices != 10_000 || logical.totalServices != 10_000 ||
		logical.memberships != 60_000 || logical.distinctPaths != 31_602 || logical.unownedPaths != 102 ||
		logical.catalogDistinctSelectors != 31_605 || logical.catalogUnownedEntries != 105 ||
		logical.structuralUnownedPrefixes != 3 || logical.unownedPhysicalFiles != 2_000_104 ||
		logical.distinctClaims != 50_000 || logical.duplicateRoleMemberships != 10_000 ||
		logical.maxRolesPerClaim != 2 || logical.maxAcceptedFanout != 20 ||
		logical.acceptedPhysicalPaths != 31_500 {
		return errors.New("T42.1 transformed logical shape drifted")
	}
	return nil
}

func cloneCatalog(input servicecatalog.Catalog) servicecatalog.Catalog {
	result := input
	result.Services = slices.Clone(input.Services)
	for index := range result.Services {
		result.Services[index].Successors = slices.Clone(input.Services[index].Successors)
	}
	result.Memberships = slices.Clone(input.Memberships)
	result.Unowned = slices.Clone(input.Unowned)
	if input.Override != nil {
		override := *input.Override
		result.Override = &override
	}
	return result
}

func sortCatalog(catalog *servicecatalog.Catalog) {
	sort.Slice(catalog.Services, func(i, j int) bool {
		return catalog.Services[i].Key < catalog.Services[j].Key
	})
	sort.Slice(catalog.Memberships, func(i, j int) bool {
		left, right := catalog.Memberships[i], catalog.Memberships[j]
		return left.ServiceKey < right.ServiceKey || left.ServiceKey == right.ServiceKey &&
			(left.Path < right.Path || left.Path == right.Path &&
				(left.Role < right.Role || left.Role == right.Role && left.Origin < right.Origin))
	})
	sort.Slice(catalog.Unowned, func(i, j int) bool {
		left, right := catalog.Unowned[i], catalog.Unowned[j]
		return left.Path < right.Path || left.Path == right.Path && left.Origin < right.Origin
	})
}

func writeFrame(destination hash.Hash, raw []byte) uint64 {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write(raw)
	return uint64(len(length) + len(raw))
}

func checkedMultiply(left, right uint64) (uint64, error) {
	if right != 0 && left > ^uint64(0)/right {
		return 0, errors.New("uint64 multiplication overflow")
	}
	return left * right, nil
}

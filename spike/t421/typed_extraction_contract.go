package t421

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// V6 is prospective. It inherits V5 without selecting the formal executor.
const typedSourceReadPolicy = "typed-source-offers-v6:metadata-only-index-0a00;grpc-caller=C-1+1+1+generated+go;thrift-caller=C-1+1+1+2*go;thrift-consumer=2*C;scip=1;other-domains=C;extraction236017*5;observation21601*5-if-needed;resolver10002*3;caller11601*3;full1352899;restore1244894;every-reread-charged;no-attempt-increase-or-arbitrary-SCIP-claim"

const typedScopeEvidenceGroupingPolicy = "typed-extraction-v6:policy=sha256(utf8(phebs-extraction-typed-scope-reservations-v1)+NUL+169-fact-policy);typed-weight=max(1,validated-scope-records);member-weight=max(1,admitted-records);unchanged-aggregate-and-partition-caps;grpc-caller-1.6.0-typed-owns-fallback;10999-source-edge-gaps-no-attribution-snapshot;members-read-without-emission;historical-V1-V5-exact"

func callerRestorePlanSemantics(schema string) bool {
	return schema == PlanV5Schema || schema == PlanV6Schema
}

// BuildPlanV6 constructs an unsealed plan; construction grants no execution.
func BuildPlanV6(sourceCommit string) (Plan, error) {
	plan, err := BuildPlanV5(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	if err = applyTypedExtractionCorrection(&plan); err != nil {
		return Plan{}, err
	}
	if err = validatePlan(plan, &plan.Revisions); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func applyTypedExtractionCorrection(plan *Plan) error {
	if plan == nil || plan.Schema != PlanV5Schema || plan.Correction == nil ||
		plan.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV5Schema || plan.ReceiptContract.Schema != ReceiptV5Schema {
		return errors.New("V6 typed extraction requires complete V5")
	}
	if err := validatePlanExecutionContract(*plan); err != nil {
		return fmt.Errorf("validate V5 typed-extraction preimage: %w", err)
	}
	plan.Schema = PlanV6Schema
	plan.ToolPolicy.ExecutionFreezeSchema = ExecutionFreezeV6Schema
	plan.ReceiptContract.Schema = ReceiptV6Schema
	plan.Profile.Pipeline.ExtractionDomains = typedScopeExtractionDomains()
	plan.Oracle.ProductRelationships.CallerLeaves = typedScopeCallerLeafProfiles()
	correction := *plan.Correction
	correction.EvidenceGroupingPolicy = typedScopeEvidenceGroupingPolicy
	correction.RequiredReadiness = append(slices.Clone(correction.RequiredReadiness), typedScopeEvidenceGroupingPolicy, typedSourceReadPolicy, typedScopeCallerUnitPolicy)
	plan.Correction = &correction
	plan.WorkEnvelope.Phases = slices.Clone(plan.WorkEnvelope.Phases)
	for index := range plan.WorkEnvelope.Phases {
		phase := &plan.WorkEnvelope.Phases[index]
		switch phase.Phase {
		case "cold", "physical_delta_b", "return_a", "archive_restore":
			maximum, err := typedExtractionSourceReadMaximum(plan.Profile, phase.Phase != "archive_restore")
			if err != nil {
				return err
			}
			phase.GitReads.Maximum = maximum
		}
	}
	digest, err := inspectionInventorySHA256(*plan, planTailReadinessTransitions(plan.Schema))
	if err != nil {
		return err
	}
	correction.InspectionInventorySHA256 = digest
	return applyCorrectedPhaseReadMaximums(&plan.WorkEnvelope, *plan)
}

// Fixed combined-corpus allocations from the existing versioned allocator;
// measured emissions never change an aggregate or per-partition safety cap.
func typedScopeExtractionDomains() []ExtractionDomainProfile {
	caller := []ResultTotals{
		reservation(4624, 9248, 9878, 4232552, 4232552),
		reservation(4596, 9193, 9820, 4207700, 4207700),
		reservation(4448, 8896, 9503, 4072569, 4072569),
		reservation(4460, 8920, 9529, 4083442, 4083442),
		reservation(4541, 9083, 9702, 4157997, 4157997),
		reservation(4690, 9379, 10019, 4293128, 4293128),
		reservation(4588, 9175, 9801, 4199934, 4199934),
		reservation(4705, 9410, 10052, 4307107, 4307107),
		reservation(12500, 25000, 20000, 33554435, 33554435),
	}
	scip := []ResultTotals{
		reservation(4751, 9502, 10150, 4349196, 4349196),
		reservation(4751, 9502, 10150, 4349196, 4349196),
		reservation(4751, 9502, 10150, 4349196, 4349196),
		reservation(4751, 9501, 10150, 4349196, 4349196),
		reservation(4751, 9501, 10149, 4349196, 4349196),
		reservation(4751, 9501, 10149, 4349196, 4349196),
		reservation(4750, 9501, 10149, 4349196, 4349196),
		reservation(3396, 6794, 7257, 3110057, 3110057),
		reservation(12500, 25000, 20000, 33554435, 33554435),
	}
	domains := storeBoundExtractionDomains()
	for index := range domains {
		domain := &domains[index]
		var reservations []ResultTotals
		switch domain.Domain {
		case "grpc-caller", "thrift-caller":
			reservations = caller
		case "scip-proto-field":
			reservations = scip
		default:
			continue
		}
		domain.Reserved, domain.Expected = ResultTotals{}, ResultTotals{}
		shape := newIdentityBuilder("t421-extraction-partition-shape-v1/" + domain.Domain)
		for ordinal := range domain.Partitions {
			partition := &domain.Partitions[ordinal]
			allocation := reservations[ordinal]
			allocation.MemberBytes = partition.Reservation.MemberBytes
			allocation.Members = partition.Reservation.Members
			partition.Reservation = allocation
			partition.Expected = ResultTotals{MemberBytes: allocation.MemberBytes, Members: allocation.Members}
			if domain.Domain == "grpc-caller" && partition.Kind == "typed_input" {
				partition.Expected = reservation(10999, 21998, 10999, 11782378, 11782378)
			}
			addResultTotals(&domain.Reserved, partition.Reservation)
			addResultTotals(&domain.Expected, partition.Expected)
			_ = shape.add(*partition)
		}
		domain.PartitionShape = shape.finish()
	}
	return domains
}

// This bound is closed over the frozen metadata-only SCIP input and exact
// selected source inventories. It is not an allowance for arbitrary indexes.
func typedExtractionSourceReadMaximum(profile CombinedProfile, observation bool) (uint64, error) {
	if profile.Pipeline.SupportedGoFiles != 21601 || profile.Pipeline.GeneratedMappings != 10000 ||
		profile.Pipeline.ResolverBlobReadsPerBuild != 10002 || profile.TypedIndex.ContentBytes != 2 ||
		profile.TypedIndex.ContentSHA256 != "sha256:102b51b9765a56a3e899f7cf0ee38e5251f9c503b357b330a49183eb7b155604" ||
		!reflect.DeepEqual(profile.Pipeline.ExtractionDomains, typedScopeExtractionDomains()) {
		return 0, errors.New("V6 source-read derivation requires exact combined inputs")
	}
	goFiles, generated := profile.Pipeline.SupportedGoFiles, profile.Pipeline.GeneratedMappings
	var extraction uint64
	for _, domain := range profile.Pipeline.ExtractionDomains {
		units := domain.CandidateRecords
		switch domain.Domain {
		case "grpc-caller":
			units += goFiles + generated + 1
		case "thrift-caller":
			units += 2*goFiles + 1
		case "thrift-consumer":
			units *= 2
		case "scip-proto-field":
			units = 1
		}
		extraction += units
	}
	if extraction != 236017 {
		return 0, errors.New("V6 extraction source population changed")
	}
	maximum := extraction*5 + (profile.Pipeline.ResolverBlobReadsPerBuild+goFiles-generated)*3
	if observation {
		maximum += goFiles * 5
	}
	return maximum, nil
}

// T50.3a supplies an empty-but-valid consumer-unit projection for this corpus.
// It replaces the absent-callback reason (36 ASCII bytes) with no_unit_mapping
// (15 bytes) in each resolved posting's Detail. Counts, other fields and safety
// caps stay exact. V1-V5 retain their original wire oracle; V6 is unsealed.
const typedScopeCallerUnitPolicy = "caller-unit-reason-v6:consumer_unit_projection_unavailable(36)->no_unit_mapping(15);21-fewer-ASCII-bytes-per-resolved-posting;10999-postings;historical-V1-V5-exact"
const typedScopeCallerCanonicalBytes = 21_425_064

func typedScopeCallerLeafProfiles() []CallerLeafProfile {
	leaves := frozenCallerLeafProfiles()
	delta := uint64(len("consumer_unit_projection_unavailable") - len("no_unit_mapping"))
	for index := range leaves {
		leaves[index].CanonicalBytes -= delta * leaves[index].ResolvedPostings
		leaves[index].EncodedBytes -= delta * leaves[index].ResolvedPostings
	}
	return leaves
}
func callerProfileByteTotals(leaves []CallerLeafProfile) (canonical, encoded uint64) {
	for _, leaf := range leaves {
		canonical += leaf.CanonicalBytes
		encoded += leaf.EncodedBytes
	}
	return canonical, encoded
}

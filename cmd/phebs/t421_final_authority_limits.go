package main

import (
	"math"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const (
	// The fixed cold control path comprises search (4), source manifest (1),
	// five three-control observation authority reads (15), candidate manifest
	// and sparse root (2), relationship pointer/root/fences (4), resolver/RPC/
	// Kafka roots (3), and caller filesystem admission/fences (8).
	t421FinalColdFixedControlReads = 4 + 1 + 5*3 + 2 + 4 + 3 + 8
	// Each extraction domain opens its plan/root/current controls and sparse
	// domain index. A present typed input adds its separate scope control.
	t421FinalControlReadsPerDomain = 3

	// The fixed maximum store path comprises repository (2), selector (2),
	// selected state including both possible preimages (4), activation (6),
	// catalog roots (6), candidate pointer fences (4), and caller (8).
	t421FinalMaximumFixedStoreReads = 2 + 2 + 4 + 6 + 6 + 4 + 8
	t421FinalStoreReadsPerDomain    = 6
	t421FinalStoreReadsPerAdapter   = 3
)

// t421FinalReadLimitSum preserves room for the ledger's exact limit+1 refusal
// sentinel. It is used for both sums and products so a future owning bound
// cannot silently wrap or produce the unrepresentable MaxUint64 limit.
type t421FinalReadLimitSum struct {
	value uint64
	valid bool
}

func newT421FinalReadLimitSum() t421FinalReadLimitSum {
	return t421FinalReadLimitSum{valid: true}
}

func (sum *t421FinalReadLimitSum) add(values ...uint64) {
	for _, value := range values {
		if !sum.valid || value >= math.MaxUint64-sum.value {
			sum.valid = false
			return
		}
		sum.value += value
	}
}

func (sum *t421FinalReadLimitSum) addProduct(values ...uint64) {
	product := uint64(1)
	for _, value := range values {
		if value != 0 && product > math.MaxUint64/value {
			sum.valid = false
			return
		}
		product *= value
	}
	sum.add(product)
}

// t421FinalAuthorityMaximumReadLimits derives the native admission ceiling
// from each owning reader's maximum accepted shape. It is deliberately larger
// than a frozen phase total, but contains no topology-derived slack terms.
func t421FinalAuthorityMaximumReadLimits() (readaccounting.Counts, bool) {
	controls := newT421FinalReadLimitSum()
	controls.add(t421FinalColdFixedControlReads)
	controls.addProduct(t421FinalControlReadsPerDomain, extractionpublication.MaxDomains)
	controls.add(extractionpublication.MaxDomains) // typed-scope controls
	controls.add(
		relationshippublication.MaxServiceMembersV3,
		relationshippublication.RepositoryBuckets,
	) // relationship semantic member files
	controls.add(resolvernamespace.MaxNamespaces)
	controls.add(rpccallerposting.MaxMembers, kafkatopicposting.MaxMembers)

	storeReads := newT421FinalReadLimitSum()
	storeReads.add(t421FinalMaximumFixedStoreReads)
	storeReads.addProduct(t421FinalStoreReadsPerDomain, extractionpublication.MaxDomains)
	storeReads.addProduct(t421FinalStoreReadsPerAdapter, callerleaf.MaxCallerDomains)
	storeReads.add(servicecatalogv3.MaxMembers)

	members := newT421FinalReadLimitSum()
	// Source projection plus its independent content proof.
	members.addProduct(2, repositoryindex.MaxOwners)
	// Cold candidate validation reads the repository and caller populations,
	// the bounded external-sort merge runs, and the final projection once.
	// Exact F rejects focused mode, so local projections are inadmissible.
	members.add(candidate.MaxWholeRepositoryStrictOpenMemberVisits())
	// Every extraction projection replays every physical candidate member for
	// each accepted result partition, including repeated execution subranges.
	members.addProduct(
		extractionpublication.MaxDomains,
		candidate.MaxDomainResultPartitions,
		candidate.MaxRecordsPerArtifact,
	)
	// Catalog decoding observes service, membership, placement, and inherited
	// placement rows. Each placement member can repeat the complete prefix set.
	members.add(
		servicecatalogv3.MaxTotalServices,
		servicecatalogv3.MaxMemberships,
		servicecatalogv3.MaxDistinctPaths,
	)
	members.addProduct(servicecatalogv3.MaxMembers, servicecatalogv3.MaxDistinctPaths)
	// Relationship semantic members, complete component postings, and caller
	// leaf records are independent member populations.
	members.add(resolvernamespace.MaxRecords)
	members.addProduct(
		relationshippublication.MaxProjectionRecords,
		relationshippublication.MaxProjectionBucketsV3,
	)
	members.add(
		relationshippublication.MaxServicesV3,
		rpccallerposting.MaxPostings,
		kafkatopicposting.MaxPostings,
		callerleaf.MaxAggregateCoveredCandidates,
	)

	if !controls.valid || !storeReads.valid || !members.valid {
		return readaccounting.Counts{}, false
	}
	return readaccounting.Counts{
		ControlFileReads:  controls.value,
		StoreReadAttempts: storeReads.value,
		MemberVisits:      members.value,
	}, true
}

package extractionpublication

import (
	"errors"
	"math"
	"sort"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
)

// BuildReservedPlan assigns every partition an explicit share of the frozen
// T40.9 aggregate before any content lease exists. Under the historical
// policy (typedScopeReservations false) shares are proportional to admitted
// candidate records and typed-only work has weight one. Under the corrected
// typed-scope policy a typed partition's weight is its validated
// TypedScope.Records with a minimum of one, so real typed emission is no
// longer reserved a proportional sliver of the domain aggregate. Every mode
// keeps the unchanged T40.8 per-partition quotas, the deterministic
// largest-remainder allocation and the fail-closed sink. Member bytes are the
// already-validated candidate-control sizes rather than an estimate.
func BuildReservedPlan(
	domain *candidate.SparseDomain,
	authority candidate.DomainResultAuthority,
	typedScopeReservations bool,
) (candidate.DomainResultPlan, error) {
	if domain == nil {
		return candidate.DomainResultPlan{}, invalid("reserved plan domain")
	}
	partitions := domain.Partitions()
	reservations := make([]candidate.DomainResultTotals, len(partitions))
	weights := reservationWeights(partitions, typedScopeReservations)
	for index, partition := range partitions {
		if partition.Member != nil {
			reservations[index].MemberBytes = partition.Member.ContentBytes
			reservations[index].Members = 1
		}
	}
	limits := candidate.FrozenDomainResultLimitsV2()
	buildPlan := candidate.BuildDomainResultPlanV2
	if domain.Domain() == candidate.DomainResultPlanV3Domain {
		limits = candidate.FrozenDomainResultLimitsV3()
		buildPlan = candidate.BuildDomainResultPlanV3
	}
	if err := measureCandidateMemberBytes(partitions, limits.AggregateMemberBytes); err != nil {
		return candidate.DomainResultPlan{}, err
	}
	quotas := candidate.FrozenExtractionPartitionQuotas()
	assignReserved(weights, int64(quotas.Facts), limits.Facts, func(index int, value int64) {
		reservations[index].Facts = value
	})
	assignReserved(weights, int64(quotas.Rows), limits.Rows, func(index int, value int64) {
		reservations[index].Rows = value
	})
	assignReserved(weights, int64(quotas.References), limits.References, func(index int, value int64) {
		reservations[index].References = value
	})
	partitionCanonicalBytes := limits.CanonicalBytes
	if limits.PartitionCanonicalBytes != 0 {
		partitionCanonicalBytes = limits.PartitionCanonicalBytes
	}
	assignReserved(weights, partitionCanonicalBytes, limits.CanonicalBytes, func(index int, value int64) {
		reservations[index].CanonicalBytes = value
	})
	partitionEncodedBytes := limits.EncodedBytes
	if limits.PartitionEncodedBytes != 0 {
		partitionEncodedBytes = limits.PartitionEncodedBytes
	}
	assignReserved(weights, partitionEncodedBytes, limits.EncodedBytes, func(index int, value int64) {
		reservations[index].EncodedBytes = value
	})
	plan, err := buildPlan(domain, authority, reservations)
	if err != nil {
		if errors.Is(err, candidate.ErrInvalidDomainResult) {
			return candidate.DomainResultPlan{}, errors.Join(ErrLimit, err)
		}
		return candidate.DomainResultPlan{}, err
	}
	return plan, nil
}

// reservationWeights gives every partition its explicit allocation weight. The
// historical weight is the admitted candidate record count with a minimum of
// one, so typed-only work carries weight one. Under the corrected typed-scope
// policy a typed partition is weighed by its validated TypedScope.Records,
// again with a minimum of one; member partitions keep the admitted count.
func reservationWeights(
	partitions []candidate.ExtractionPartition,
	typedScopeReservations bool,
) []int64 {
	weights := make([]int64, len(partitions))
	for index, partition := range partitions {
		weight := int64(partition.AdmittedRecords)
		if weight == 0 {
			weight = 1
		}
		if typedScopeReservations && partition.TypedScope != nil {
			weight = int64(partition.TypedScope.Records)
			if weight < 1 {
				weight = 1
			}
		}
		weights[index] = weight
	}
	return weights
}

func measureCandidateMemberBytes(partitions []candidate.ExtractionPartition, limit int64) error {
	total := int64(0)
	for _, partition := range partitions {
		if partition.Member == nil {
			continue
		}
		if partition.Member.ContentBytes < 0 || partition.Member.ContentBytes > math.MaxInt64-total {
			return errors.Join(ErrLimit, candidate.ErrInvalidDomainResult)
		}
		total += partition.Member.ContentBytes
	}
	if total > limit {
		return errors.Join(
			ErrLimit,
			pipelinerefusal.Measure(
				candidate.ErrInvalidDomainResult,
				pipelinerefusal.DimensionCandidateMemberBytes,
				total, limit,
			),
		)
	}
	return nil
}

type reservedRemainder struct {
	index     int
	remainder int64
}

func assignReserved(
	weights []int64,
	perPartition,
	aggregate int64,
	set func(int, int64),
) {
	if len(weights) == 0 {
		return
	}
	values := make([]int64, len(weights))
	active := make([]int, len(weights))
	for index := range weights {
		active[index] = index
	}
	remaining := aggregate
	for remaining > 0 && len(active) > 0 {
		weightTotal := int64(0)
		for _, index := range active {
			weightTotal += weights[index]
		}
		if weightTotal <= 0 {
			break
		}
		allocated := int64(0)
		remainders := make([]reservedRemainder, 0, len(active))
		next := active[:0]
		for _, index := range active {
			capacity := perPartition - values[index]
			if capacity <= 0 {
				continue
			}
			share, remainder := proportionalShare(remaining, weights[index], weightTotal)
			if share > capacity {
				share = capacity
			}
			values[index] += share
			allocated += share
			if values[index] < perPartition {
				next = append(next, index)
				remainders = append(remainders, reservedRemainder{index: index, remainder: remainder})
			}
		}
		remaining -= allocated
		if remaining <= 0 || len(next) == 0 {
			break
		}
		if allocated == 0 {
			sort.SliceStable(remainders, func(i, j int) bool {
				if remainders[i].remainder != remainders[j].remainder {
					return remainders[i].remainder > remainders[j].remainder
				}
				return remainders[i].index < remainders[j].index
			})
			for _, value := range remainders {
				if remaining == 0 {
					break
				}
				values[value.index]++
				remaining--
			}
		}
		active = append([]int(nil), next...)
	}
	for index, value := range values {
		set(index, value)
	}
}

func proportionalShare(total, weight, weightTotal int64) (int64, int64) {
	if total <= math.MaxInt64/weight {
		product := total * weight
		return product / weightTotal, product % weightTotal
	}
	quotient, remainder := total/weightTotal, total%weightTotal
	return quotient*weight + remainder*weight/weightTotal,
		(remainder * weight) % weightTotal
}

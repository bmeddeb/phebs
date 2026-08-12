package extractionpublication

import (
	"errors"
	"math"
	"sort"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
)

// BuildReservedPlan assigns every partition an explicit share of the frozen
// T40.9 aggregate before any content lease exists. Shares are proportional to
// admitted candidate records (typed-only work has weight one), capped by the
// unchanged T40.8 per-partition quotas, and deterministic under exact replay.
// Member bytes are the already-validated candidate-control sizes rather than
// an estimate.
func BuildReservedPlan(
	domain *candidate.SparseDomain,
	authority candidate.DomainResultAuthority,
) (candidate.DomainResultPlan, error) {
	if domain == nil {
		return candidate.DomainResultPlan{}, invalid("reserved plan domain")
	}
	partitions := domain.Partitions()
	reservations := make([]candidate.DomainResultTotals, len(partitions))
	weights := make([]int64, len(partitions))
	for index, partition := range partitions {
		weight := int64(partition.AdmittedRecords)
		if weight == 0 {
			weight = 1
		}
		weights[index] = weight
		if partition.Member != nil {
			reservations[index].MemberBytes = partition.Member.ContentBytes
			reservations[index].Members = 1
		}
	}
	limits := candidate.FrozenDomainResultLimitsV2()
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
	assignReserved(weights, limits.CanonicalBytes, limits.CanonicalBytes, func(index int, value int64) {
		reservations[index].CanonicalBytes = value
	})
	assignReserved(weights, limits.EncodedBytes, limits.EncodedBytes, func(index int, value int64) {
		reservations[index].EncodedBytes = value
	})
	plan, err := candidate.BuildDomainResultPlanV2(domain, authority, reservations)
	if err != nil {
		if errors.Is(err, candidate.ErrInvalidDomainResult) {
			return candidate.DomainResultPlan{}, errors.Join(ErrLimit, err)
		}
		return candidate.DomainResultPlan{}, err
	}
	return plan, nil
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

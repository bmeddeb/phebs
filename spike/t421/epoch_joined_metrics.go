package t421

import "math/bits"

// executionJoinedWorkMetrics is only the exact producer-joined subset of a
// receipt. Uncovered phase slots and every other ReceiptMetrics field remain
// unavailable, not observed zero.
type executionJoinedWorkMetrics struct {
	Metrics [15]ReceiptMetrics
	Covered [15]bool
}

func addExecutionJoinedMetric[T ~uint64](target *T, value uint64) bool {
	sum, carry := bits.Add64(uint64(*target), value, 0)
	if carry != 0 {
		return false
	}
	*target = T(sum)
	return true
}

func validExecutionJoinedWorkRecord(plan Plan, record executionJoinedWorkRecord) bool {
	server := record.Producer <= 6
	if !validExecutionHandoffRecord(plan, record) || !record.Attempts.Complete || !record.Attempts.SourceBound || !record.Attempts.ObservationBound ||
		!record.Attempts.PublicationBound || !record.Attempts.ResolverBound || !record.Attempts.RelationshipBound ||
		!record.Attempts.Cache.Complete || !record.Attempts.Cache.complete() ||
		!record.Attempts.SourceCensus.Complete || !record.Attempts.SourceCensus.complete() ||
		!record.Attempts.CatalogCensus.Complete || !record.Attempts.CatalogCensus.complete() ||
		!record.Attempts.UnsupportedSource.Complete || !record.Attempts.UnsupportedSource.complete(plan, record.Producer) {
		return false
	}
	if server {
		if !record.Attempts.AttemptBound || !record.Attempts.Lifecycle.Bound || !record.Attempts.Lifecycle.Complete ||
			!record.Attempts.Reuse.Bound || !record.Attempts.Reuse.Complete ||
			!record.IndexOffers.Complete || !record.IndexOffers.coherent() {
			return false
		}
	} else if record.Attempts.AttemptBound || record.Attempts.Lifecycle != (ExecutionLifecycleObservation{}) ||
		record.Attempts.UnsupportedSource != (ExecutionUnsupportedSourceObservation{Complete: true}) ||
		record.Attempts.Reuse != (ExecutionReuseObservation{}) || record.IndexOffers != (ExecutionIndexObservation{}) {
		return false
	}
	owned := [15]bool{}
	for _, phase := range executionProducerPhases(record.Producer) {
		index := phase - 1
		owned[index] = true
		attempt := record.Attempts.Phases[index]
		if attempt.Retries > attempt.JobAttempts || attempt.UnsupportedSourceFiles != 0 ||
			attempt.SourceUniqueBytes > attempt.SourceLogicalBytes ||
			validateTotalAgainstMeasuredMaximum("producer retries", attempt.Retries, attempt.JobAttempts, attempt.MaxRetriesUnit) != nil ||
			record.Attempts.CatalogCensus.ClosedChildren[index] != attempt.CensusChildren ||
			server && !validExecutionJoinedReusePhase(record.Attempts.Reuse.Phases[index]) || !server &&
			(attempt.JobAttempts != 0 || attempt.Retries != 0 || attempt.MaxRetriesUnit != 0) {
			return false
		}
		if server {
			lifecycle := record.Attempts.Lifecycle.Phases[index]
			if lifecycle.FailedTicks > lifecycle.ReturnedTicks || lifecycle.OwnerTurns > lifecycle.ReturnedTicks ||
				validateTotalAgainstMeasuredMaximum("producer lifecycle deletion", lifecycle.Deleted, lifecycle.OwnerTurns, lifecycle.MaxDeleted) != nil {
				return false
			}
		}
	}
	for phase := range owned {
		if owned[phase] {
			continue
		}
		if record.Attempts.Phases[phase] != (ExecutionAttemptCount{}) ||
			record.Attempts.Lifecycle.Phases[phase] != (ExecutionLifecycleCount{}) ||
			record.Attempts.Cache.Phases[phase] != (ExecutionCacheCount{}) ||
			record.Attempts.SourceCensus.Started[phase] != 0 || record.Attempts.SourceCensus.Finished[phase] != 0 ||
			record.Attempts.SourceCensus.Succeeded[phase] != 0 || record.Attempts.SourceCensus.RegularOwners[phase] != 0 ||
			record.Attempts.CatalogCensus.Started[phase] != 0 || record.Attempts.CatalogCensus.Finished[phase] != 0 ||
			record.Attempts.CatalogCensus.ClosedChildren[phase] != 0 ||
			record.Attempts.UnsupportedSource.Reports[phase] != 0 ||
			record.Attempts.Reuse.Phases[phase] != (ExecutionReusePhase{}) ||
			record.IndexOffers.Phases[phase] != (ExecutionIndexOfferCount{}) {
			return false
		}
	}
	return true
}

func validExecutionJoinedReusePhase(value ExecutionReusePhase) bool {
	if !value.Complete {
		return false
	}
	for _, decision := range []ExecutionReuseDecision{value.Source, value.Search, value.Observation, value.Catalog, value.Relationship} {
		if _, valid := executionReuseCount(decision); !valid {
			return false
		}
	}
	return true
}

func executionReuseCount(value ExecutionReuseDecision) (uint64, bool) {
	switch value {
	case "":
		return 0, true
	case ExecutionReuseCurrent, ExecutionReuseReactivated:
		return 1, true
	default:
		return 0, false
	}
}

// receiptMetrics adds producer 4 and producer 5 independently into phase
// eight. No producer-local maximum or counter is overwritten by another.
func (work executionJoinedWork) receiptMetrics(plan Plan) (executionJoinedWorkMetrics, error) {
	var out executionJoinedWorkMetrics
	if !work.complete() {
		return out, errExecutionAttempts
	}
	for _, record := range work.Records {
		if !validExecutionJoinedWorkRecord(plan, record) {
			return executionJoinedWorkMetrics{}, errExecutionAttempts
		}
		for _, phase := range executionProducerPhases(record.Producer) {
			index := phase - 1
			if !addExecutionWorkRecordMetrics(&out.Metrics[index], record, int(index)) {
				return executionJoinedWorkMetrics{}, errExecutionAttempts
			}
			out.Covered[index] = true
		}
	}
	return out, nil
}

// Add only actual retained quantities, independent of coverage/acceptance.
func addExecutionWorkRecordMetrics(metric *ReceiptMetrics, record executionJoinedWorkRecord, index int) bool {
	attempt := record.Attempts.Phases[index]
	cache := record.Attempts.Cache.Phases[index]
	lifecycle := record.Attempts.Lifecycle.Phases[index]
	indexOffer := record.IndexOffers.Phases[index]
	cleanup := record.Attempts.Handoff.Cleanup[index]
	values := []struct {
		target *CountMetric
		value  uint64
	}{
		{&metric.JobAttempts, attempt.JobAttempts}, {&metric.Retries, attempt.Retries},
		{&metric.GitReads, attempt.SourceBlobAttempts}, {&metric.ObservationParses, attempt.ObservationParses},
		{&metric.PublicationWrites, attempt.PublicationWrites}, {&metric.ResolverBlobReads, attempt.ResolverBlobReads},
		{&metric.RelationshipBuildAttempts, attempt.RelationshipBuildAttempts},
		{&metric.RelationshipProjections, attempt.RelationshipProjections},
		{&metric.ServiceReferences, attempt.ServiceReferences},
		{&metric.CensusChildren, attempt.CensusChildren}, {&metric.CensusRecords, attempt.CensusRecords},
		{&metric.CacheLookups, cache.Lookups}, {&metric.CacheHits, cache.Hits}, {&metric.CacheMisses, cache.Misses},
		{&metric.CacheRootReads, cache.RootReads}, {&metric.CacheMemberReads, cache.MemberReads},
		{&metric.CacheRootValidations, cache.RootValidations}, {&metric.CacheMemberValidations, cache.MemberValidations},
		{&metric.LifecycleOwnerTurns, lifecycle.OwnerTurns}, {&metric.LifecycleDeleted, lifecycle.Deleted},
		{&metric.LifecycleOwnerTurns, cleanup.Turns}, {&metric.LifecycleDeleted, cleanup.Deleted},
		{&metric.ControlReads, cleanup.StoreReadAttempts},
		{&metric.UnsupportedSourceFiles, attempt.UnsupportedSourceFiles}, {&metric.IndexFiles, indexOffer.Offers},
	}
	for _, value := range values {
		if !addExecutionJoinedMetric(value.target, value.value) {
			return false
		}
	}
	for _, value := range []struct {
		target *Bytes
		value  uint64
	}{
		{&metric.ResolverBlobBytes, attempt.ResolverBlobBytes},
		{&metric.SourceLogicalBytes, attempt.SourceLogicalBytes},
		{&metric.SourceUniqueBytes, attempt.SourceUniqueBytes},
	} {
		if !addExecutionJoinedMetric(value.target, value.value) {
			return false
		}
	}
	metric.MaxRetriesUnit = max(metric.MaxRetriesUnit, CountMetric(attempt.MaxRetriesUnit))
	metric.MaxLifecycleDeletesTurn = max(metric.MaxLifecycleDeletesTurn, CountMetric(lifecycle.MaxDeleted), CountMetric(cleanup.MaxDeleted))
	if index == 3 {
		for _, sweep := range record.Attempts.Handoff.Retention {
			if sweep.Attempt == 0 {
				continue
			}
			if sweep.Deleted < 0 || !addExecutionJoinedMetric(&metric.LifecycleOwnerTurns, 1) ||
				!addExecutionJoinedMetric(&metric.LifecycleDeleted, uint64(sweep.Deleted)) {
				return false
			}
			metric.MaxLifecycleDeletesTurn = max(metric.MaxLifecycleDeletesTurn, CountMetric(sweep.Deleted))
		}
	}
	if record.Producer <= 6 {
		reuse := record.Attempts.Reuse.Phases[index]
		lanes := []struct {
			target *CountMetric
			value  ExecutionReuseDecision
		}{
			{&metric.SourceReuseDecisions, reuse.Source}, {&metric.SearchReuseDecisions, reuse.Search},
			{&metric.ObservationReuseDecisions, reuse.Observation}, {&metric.CatalogReuseDecisions, reuse.Catalog},
			{&metric.RelationshipReuseDecisions, reuse.Relationship},
		}
		for _, lane := range lanes {
			count, valid := executionReuseCount(lane.value)
			if !valid || !addExecutionJoinedMetric(lane.target, count) || !addExecutionJoinedMetric(&metric.ReuseDecisions, count) {
				return false
			}
		}
	}
	return true
}

package extract

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	extractionTimeout             = 15 * time.Minute
	extractionMirrorTimeout       = 14*time.Minute + 50*time.Second
	extractionDomainTimeout       = 5 * time.Minute
	extractionOutcomeTimeout      = 5 * time.Second
	extractionMinimumStartBudget  = time.Second
	maxSerialExtractionDomains    = 16
	maxExtractionSchedulerBytes   = 64 << 10
	maxExtractionStagedRows       = 100_000
	maxDomainExtractionStagedRows = maxFactsPerRun * 2
)

var (
	errExtractionAggregateBudget = errors.New("aggregate extraction budget exhausted")
	errExtractionDomainBudget    = errors.New("domain extraction budget exhausted")
)

// domainSchedulingLimits is deliberately private. Tests may tighten a worker's
// limits, but production callers cannot expand the frozen aggregate bounds.
type domainSchedulingLimits struct {
	AggregateTimeout    time.Duration
	MirrorTimeout       time.Duration
	DomainTimeout       time.Duration
	AbortTimeout        time.Duration
	OutcomeTimeout      time.Duration
	MinimumStartBudget  time.Duration
	MaxSerialDomains    int
	MaxSchedulerBytes   int
	MaxStagedRows       int
	MaxDomainStagedRows int
}

var productionDomainSchedulingLimits = domainSchedulingLimits{
	AggregateTimeout:    extractionTimeout,
	MirrorTimeout:       extractionMirrorTimeout,
	DomainTimeout:       extractionDomainTimeout,
	AbortTimeout:        abortTimeout,
	OutcomeTimeout:      extractionOutcomeTimeout,
	MinimumStartBudget:  extractionMinimumStartBudget,
	MaxSerialDomains:    maxSerialExtractionDomains,
	MaxSchedulerBytes:   maxExtractionSchedulerBytes,
	MaxStagedRows:       maxExtractionStagedRows,
	MaxDomainStagedRows: maxDomainExtractionStagedRows,
}

func effectiveDomainSchedulingLimits(
	override *domainSchedulingLimits,
) (domainSchedulingLimits, error) {
	limits := productionDomainSchedulingLimits
	if override != nil {
		limits = *override
	}
	production := productionDomainSchedulingLimits
	if limits.AggregateTimeout <= 0 ||
		limits.AggregateTimeout > production.AggregateTimeout ||
		limits.MirrorTimeout <= 0 ||
		limits.MirrorTimeout > production.MirrorTimeout ||
		limits.DomainTimeout <= 0 ||
		limits.DomainTimeout > production.DomainTimeout ||
		limits.AbortTimeout <= 0 ||
		limits.AbortTimeout > production.AbortTimeout ||
		limits.OutcomeTimeout <= 0 ||
		limits.OutcomeTimeout > production.OutcomeTimeout ||
		limits.MinimumStartBudget <= 0 ||
		limits.MinimumStartBudget > production.MinimumStartBudget ||
		limits.MaxSerialDomains <= 0 ||
		limits.MaxSerialDomains > production.MaxSerialDomains ||
		limits.MaxSchedulerBytes <= 0 ||
		limits.MaxSchedulerBytes > production.MaxSchedulerBytes ||
		limits.MaxStagedRows <= 0 ||
		limits.MaxStagedRows > production.MaxStagedRows ||
		limits.MaxDomainStagedRows <= 0 ||
		limits.MaxDomainStagedRows > production.MaxDomainStagedRows {
		return domainSchedulingLimits{}, errors.New(
			"domain scheduling limits must be positive production-tightening values",
		)
	}
	if limits.MirrorTimeout > limits.AggregateTimeout ||
		limits.DomainTimeout >= limits.AggregateTimeout ||
		limits.AbortTimeout+limits.OutcomeTimeout+
			limits.MinimumStartBudget >=
			limits.MirrorTimeout ||
		limits.MaxDomainStagedRows > limits.MaxStagedRows {
		return domainSchedulingLimits{}, errors.New(
			"domain scheduling limits have inconsistent aggregate bounds",
		)
	}
	return limits, nil
}

type scheduledDomain struct {
	index               int
	extractor           registeredExtractor
	scope               store.ExtractionScope
	generation          store.ExtractionGenerationIdentity
	previousRunID       string
	previousDisposition store.DomainOutcomeDisposition
	attemptedAt         time.Time
	retryable           bool
	diagnosticState     string
	typedApplicable     bool
	typedPresent        bool
}

func scheduleDomains(domains []scheduledDomain) {
	sort.SliceStable(domains, func(i, j int) bool {
		left, right := domains[i], domains[j]
		if left.retryable != right.retryable {
			return !left.retryable
		}
		if left.retryable && !left.attemptedAt.Equal(right.attemptedAt) {
			if left.attemptedAt.IsZero() {
				return true
			}
			if right.attemptedAt.IsZero() {
				return false
			}
			return left.attemptedAt.Before(right.attemptedAt)
		}
		return left.index < right.index
	})
}

func scheduledDomainIdentityBytes(domain scheduledDomain) int {
	generation := domain.generation
	return len(domain.extractor.domain) +
		len(domain.extractor.version) +
		len(domain.scope.Repository) +
		len(domain.scope.Commit) +
		len(domain.scope.UnitDigest) +
		len(domain.scope.Domain) +
		len(domain.previousRunID) +
		len(generation.CandidateManifestDigest) +
		len(generation.CandidatePolicyDigest) +
		len(generation.Extractor) +
		len(generation.InventoryPolicy) +
		len(generation.TypedInputKind) +
		len(generation.TypedInputObjectID) +
		len(generation.TypedInputDigest) +
		len(generation.DependencyDigest) +
		len(generation.Digest) +
		256
}

func validateScheduledDomains(
	domains []scheduledDomain,
	limits domainSchedulingLimits,
) error {
	bytes := 0
	for _, domain := range domains {
		bytes += scheduledDomainIdentityBytes(domain)
		if bytes > limits.MaxSchedulerBytes {
			return fmt.Errorf(
				"%w: scheduler identity exceeds %d bytes",
				errExtractionAggregateBudget, limits.MaxSchedulerBytes,
			)
		}
	}
	return nil
}

func earlierDeadline(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

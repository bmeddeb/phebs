// Package retentionstatus collects the bounded store/filesystem portion of
// the retention-status surface without depending on the HTTP wire package.
package retentionstatus

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	CandidatePhysicalEntryObservationLimit = 32_768
	FocusedPhysicalEntryObservationLimit   = 32_768
	ResolverPhysicalEntryObservationLimit  = 32_768
	CallerPhysicalEntryObservationLimit    = 65_536
	AggregatePhysicalEntryObservationLimit = 163_840
	AggregateStatOperationLimit            = 2_048
	AggregateManifestMetadataByteLimit     = 64 << 20
	MaxQueuedCallerRepositoryDirectories   = 256
	ReadDirBatchSize                       = 256
	// MaxSimultaneousDescriptors includes Go's platform directory-iterator
	// duplicates and rooted traversal handles in addition to the three handles
	// retained directly by the collector.
	MaxSimultaneousDescriptors = 5

	derivedComponentCount       = 7
	derivedStoreRequestCount    = 4
	maxReportedPerComponent     = 78
	maxAggregateReported        = 546
	maxAggregateScanned         = 553
	dataVolumeDiagnosticSubject = "data_volume"
	maxRetainedDescriptors      = 3
)

type Completeness string

const (
	Exact       Completeness = "exact"
	LowerBound  Completeness = "lower_bound"
	Unavailable Completeness = "unavailable"
)

type Metric struct {
	Value        *int64
	Completeness Completeness
}

type ByteKind string

const (
	CanonicalContent ByteKind = "canonical_content"
	CanonicalReceipt ByteKind = "canonical_receipt"
	ApparentFile     ByteKind = "apparent_file"
)

type ByteMetric struct {
	Kind   ByteKind
	Metric Metric
}

type ComponentResult struct {
	Component         store.RetentionComponent
	ScannedIdentities int
	Count             Metric
	ByteMetrics       []ByteMetric
	Truncated         bool
	Err               error
	Diagnostics       []error
}

type DataVolumeResult struct {
	TotalBytes     Metric
	AvailableBytes Metric
	Diagnostics    []error
}

type Collection struct {
	Components []ComponentResult
	DataVolume DataVolumeResult
}

type Source interface {
	CollectRetention(
		context.Context,
		[]store.RetentionComponentRequest,
	) (Collection, error)
}

type Collector struct {
	dataDir string
	store   store.DerivedRetentionStore
	policy  collectorPolicy
	statFS  statFSFunc
	hooks   collectorHooks
}

var _ Source = (*Collector)(nil)

func New(dataDir string, source store.DerivedRetentionStore) *Collector {
	return &Collector{
		dataDir: dataDir,
		store:   source,
		policy:  defaultCollectorPolicy(),
		statFS:  platformStatFS,
	}
}

type collectorPolicy struct {
	componentObservations map[store.RetentionComponent]int
	aggregateObservations int
	aggregateStats        int
	aggregateMetadata     int64
	queuedCallerDirs      int
	readDirBatch          int
	maxDescriptors        int
}

func defaultCollectorPolicy() collectorPolicy {
	return collectorPolicy{
		componentObservations: map[store.RetentionComponent]int{
			store.RetentionCandidateFiles:  CandidatePhysicalEntryObservationLimit,
			store.RetentionFocusedFiles:    FocusedPhysicalEntryObservationLimit,
			store.RetentionResolverFiles:   ResolverPhysicalEntryObservationLimit,
			store.RetentionCallerArtifacts: CallerPhysicalEntryObservationLimit,
		},
		aggregateObservations: AggregatePhysicalEntryObservationLimit,
		aggregateStats:        AggregateStatOperationLimit,
		aggregateMetadata:     AggregateManifestMetadataByteLimit,
		queuedCallerDirs:      MaxQueuedCallerRepositoryDirectories,
		readDirBatch:          ReadDirBatchSize,
		maxDescriptors:        maxRetainedDescriptors,
	}
}

type collectorHooks struct {
	afterDirectoryOpen   func(string)
	beforeDirectoryOpen  func(string)
	beforeNestedOpen     func(string)
	afterNestedOpen      func(string)
	beforeRegularOpen    func(string)
	afterRegularOpen     func(string)
	beforeRegularRead    func(string)
	afterEntryInfo       func(string)
	beforeDirectoryClose func(string)
	fileCloseError       func(string) error
	descriptorDelta      func(int)
	metadataObserved     func(int64)
}

type statFSResult struct {
	Blocks          uint64
	AvailableBlocks uint64
	BlockSize       uint64
}

type statFSFunc func(*os.File) (statFSResult, error)

func metric(value int64, completeness Completeness) Metric {
	copy := value
	return Metric{Value: &copy, Completeness: completeness}
}

func unavailableMetric() Metric {
	return Metric{Completeness: Unavailable}
}

func byteMetric(kind ByteKind, observed Metric) ByteMetric {
	return ByteMetric{Kind: kind, Metric: observed}
}

func validateRequests(
	requests []store.RetentionComponentRequest,
) (map[store.RetentionComponent]store.RetentionComponentRequest, error) {
	if len(requests) != derivedComponentCount {
		return nil, fmt.Errorf(
			"collect derived retention: %d requests is not %d",
			len(requests), derivedComponentCount,
		)
	}
	allowed := map[store.RetentionComponent]struct{}{
		store.RetentionCandidatePublication:   {},
		store.RetentionCandidateFiles:         {},
		store.RetentionFocusedRepositoryState: {},
		store.RetentionFocusedFiles:           {},
		store.RetentionResolverPublication:    {},
		store.RetentionResolverFiles:          {},
		store.RetentionCallerArtifacts:        {},
	}
	indexed := make(map[store.RetentionComponent]store.RetentionComponentRequest, len(requests))
	reported, scanned := 0, 0
	for _, request := range requests {
		if _, ok := allowed[request.Component]; !ok {
			return nil, fmt.Errorf(
				"collect derived retention: unsupported component %q",
				request.Component,
			)
		}
		if _, duplicate := indexed[request.Component]; duplicate {
			return nil, fmt.Errorf(
				"collect derived retention: duplicate component %q",
				request.Component,
			)
		}
		if request.ReportedIdentities <= 0 ||
			request.ReportedIdentities > maxReportedPerComponent ||
			request.ScanIdentities != request.ReportedIdentities+1 {
			return nil, fmt.Errorf(
				"collect derived retention: invalid allocation for %q",
				request.Component,
			)
		}
		indexed[request.Component] = request
		reported += request.ReportedIdentities
		scanned += request.ScanIdentities
	}
	if reported > maxAggregateReported || scanned > maxAggregateScanned {
		return nil, errors.New("collect derived retention: aggregate allocation exceeds fixed budget")
	}
	return indexed, nil
}

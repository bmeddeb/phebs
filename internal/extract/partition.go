package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// EvidencePartitionExecutor is the production T40.7 staging adapter. The
// supplied corpus is already restricted by the source owner to one T40.8
// partition; this adapter independently inventories that capability, runs the
// matching pure extractor, validates every emitted fact, and appends only
// content-addressed 256-fact chunks under the caller's invisible run.
type EvidencePartitionExecutor struct {
	Evidence   store.EvidenceStore
	Extractors []Extractor
}

func (executor *EvidencePartitionExecutor) ExecutePartition(
	ctx context.Context,
	plan candidate.DomainResultPlan,
	ordinal int,
	lease extractionpublication.PartitionLease,
	runID string,
) (result candidate.PartitionResultSpec, resultErr error) {
	allowTerminalResult := false
	defer func() {
		if resultErr == nil || !allowTerminalResult || result.Disposition != "" {
			return
		}
		disposition, _ := classifyDomainOutcome(resultErr)
		if disposition != store.DomainOutcomeTerminalGenerationRefusal {
			return
		}
		result = candidate.PartitionResultSpec{
			Disposition: candidate.PartitionResultTerminalRefusal,
			Reason:      "deterministic_refusal",
		}
		if !store.IsTerminal(resultErr) {
			resultErr = store.WithTerminal(resultErr)
		}
	}()
	if executor == nil || executor.Evidence == nil || lease == nil || runID == "" ||
		ordinal < 0 || ordinal >= len(plan.Expected) {
		return candidate.PartitionResultSpec{}, errors.New("partition executor configuration is invalid")
	}
	accounting, ok := executor.Evidence.(store.EvidenceChunkAccountingStore)
	if !ok {
		return candidate.PartitionResultSpec{}, errors.New("partition executor requires exact chunk accounting")
	}
	if err := candidate.ValidateDomainResultPlanControl(plan); err != nil {
		return candidate.PartitionResultSpec{}, err
	}
	extractor, err := executor.extractor(plan.Domain, plan.ExtractorVersion)
	if err != nil {
		return candidate.PartitionResultSpec{}, err
	}
	corpus := lease.Corpus()
	typed := lease.TypedInput()
	if corpus == nil || corpus.RepoName() != plan.Repository ||
		lease.CandidateRecords() != plan.Expected[ordinal].SourceEnd-plan.Expected[ordinal].SourceStart {
		return candidate.PartitionResultSpec{}, errors.New("partition lease crosses its planned source range")
	}
	if lease.TypedSourceRecords() < 0 ||
		(typed == nil && lease.TypedSourceRecords() != 0) ||
		(typed != nil && lease.TypedSourceRecords() > candidate.PartitionMaxTypedScopeRecords) {
		return candidate.PartitionResultSpec{}, errors.New("partition lease has an invalid typed source scope")
	}
	reservation := plan.Expected[ordinal].Reservation
	if lease.MemberBytes() < 0 || lease.Members() < 0 ||
		lease.MemberBytes() > reservation.MemberBytes || lease.Members() > reservation.Members {
		return candidate.PartitionResultSpec{}, errors.New("partition lease exceeds its member reservation")
	}
	allowTerminalResult = true
	verified := newVerifiedCorpus(corpus)
	verified.typedPartition = typed != nil
	operation := &domainOperationRecorder{now: time.Now}
	candidatePredicate := extractor.extractor.Candidate
	if typed != nil {
		candidatePredicate = func(string) bool { return false }
	}
	if err := verified.Inventory(ctx, candidatePredicate); err != nil {
		return candidate.PartitionResultSpec{}, deterministicPartitionFailure(ctx, err)
	}
	if typed != nil {
		documentScope, ok := corpus.(sdk.SCIPDocumentScope)
		if !ok {
			return candidate.PartitionResultSpec{}, store.WithTerminal(errors.New(
				"typed partition corpus has no admitted document scope",
			))
		}
		verified.mu.Lock()
		verified.typedInput = treeRecord{
			path: typed.Path, oid: typed.ObjectID, size: typed.DeclaredBytes,
		}
		verified.typedInputKind = typed.Kind
		verified.typedInputPresent = typed.Present
		verified.manifestBound = true
		verified.scoped = true
		verified.pathInScope = documentScope.SCIPDocumentInScope
		verified.mu.Unlock()
	}
	if verified.corpusFileCount != lease.CandidateRecords()+lease.TypedSourceRecords() {
		return candidate.PartitionResultSpec{}, errors.New("partition corpus inventory disagrees with its source range")
	}
	maxFacts := minInt64(
		reservation.Facts,
		reservation.References,
		reservation.Rows/2,
		maxFactsPerRun,
	)
	sink := newRunSink(
		ctx, executor.Evidence, runID, corpus.RepoName(), corpus.Commit(),
		plan.ExtractorVersion, verified, operation, int(minInt64(reservation.Rows, math.MaxInt)),
	)
	sink.accounting = accounting
	sink.maxFacts = int(maxFacts)
	sink.maxReferences = reservation.References
	sink.maxCanonical = reservation.CanonicalBytes
	sink.maxEncoded = reservation.EncodedBytes
	sink.chunkNamespace = plan.Digest + ":" + fmt.Sprint(ordinal)
	defer func() {
		sink.Close()
		verified.Close()
	}()
	coverage, extractErr := callExtractor(ctx, extractor.extractor, verified, sink.Emit)
	if extractErr != nil {
		if sinkErr := sink.failure(); sinkErr != nil {
			return candidate.PartitionResultSpec{}, errors.Join(extractErr, sinkErr)
		}
		return candidate.PartitionResultSpec{}, deterministicPartitionFailure(ctx, extractErr)
	}
	if err := sink.Finish(); err != nil {
		return candidate.PartitionResultSpec{}, err
	}
	stats, err := verified.Stats()
	if err != nil {
		return candidate.PartitionResultSpec{}, deterministicPartitionFailure(ctx, err)
	}
	if len(coverage.Failures) != 0 || coverage.UnresolvedCount != sink.unresolvedCount ||
		(typed == nil && stats.readFileCount != lease.CandidateRecords()) ||
		(typed != nil && (stats.readFileCount < 1 ||
			stats.readFileCount > lease.TypedSourceRecords()+1)) {
		return candidate.PartitionResultSpec{}, store.WithTerminal(
			errors.New("partition extractor returned incomplete coverage"),
		)
	}
	totals := candidate.DomainResultTotals{
		Facts: int64(sink.factCount), Rows: int64(sink.stagedRows),
		References:     int64(sink.stagedReferences),
		CanonicalBytes: sink.canonicalBytes, EncodedBytes: sink.encodedBytes,
		MemberBytes: lease.MemberBytes(), Members: lease.Members(),
	}
	disposition := candidate.PartitionResultSuccess
	if totals == (candidate.DomainResultTotals{}) {
		disposition = candidate.PartitionResultEmpty
	}
	return candidate.PartitionResultSpec{
		Disposition: disposition, Totals: totals,
		ContentDigest: partitionContentDigest(sink.stagedChunkIDs),
	}, nil
}

func deterministicPartitionFailure(ctx context.Context, err error) error {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || store.IsTerminal(err) {
		return err
	}
	return store.WithTerminal(err)
}

func (executor *EvidencePartitionExecutor) extractor(domain, version string) (registeredExtractor, error) {
	for _, value := range executor.Extractors {
		if value == nil {
			continue
		}
		candidateDomain, candidateVersion, err := extractorMetadata(value)
		if err != nil {
			return registeredExtractor{}, err
		}
		if candidateDomain == domain && candidateVersion == version {
			return registeredExtractor{extractor: value, domain: domain, version: version}, nil
		}
	}
	return registeredExtractor{}, fmt.Errorf("partition extractor %s@%s is not registered", domain, version)
}

func minInt64(values ...int64) int64 {
	result := int64(math.MaxInt64)
	for _, value := range values {
		if value < result {
			result = value
		}
	}
	return result
}

func partitionContentDigest(chunks []string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-extraction-partition-content-v1"))
	_, _ = hash.Write([]byte{0})
	for _, chunk := range chunks {
		_, _ = fmt.Fprintf(hash, "%d:", len(chunk))
		_, _ = hash.Write([]byte(chunk))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

var _ extractionpublication.Executor = (*EvidencePartitionExecutor)(nil)

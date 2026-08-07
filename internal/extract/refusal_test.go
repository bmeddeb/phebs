package extract

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	candidatepkg "github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

type uncontrolledGenerationManifestProvider struct {
	pointer  CandidateManifestPointerIdentity
	manifest CandidateManifest
}

func (provider uncontrolledGenerationManifestProvider) PolicyDigest() string {
	return provider.pointer.PolicyDigest
}

func (provider uncontrolledGenerationManifestProvider) CandidateManifestGeneration(
	context.Context,
	CandidateManifestRequest,
) (CandidateManifestPointerIdentity, error) {
	return provider.pointer, nil
}

func (provider uncontrolledGenerationManifestProvider) OpenCandidateManifest(
	context.Context,
	CandidateManifestRequest,
) (CandidateManifest, error) {
	return provider.manifest, nil
}

func TestExtractionMeasuredLimitsAdmitExactAndRefuseOneOver(t *testing.T) {
	dimensions := []pipelinerefusal.Dimension{
		pipelinerefusal.DimensionInventoryFiles,
		pipelinerefusal.DimensionInventoryPathBytes,
		pipelinerefusal.DimensionFacts,
		pipelinerefusal.DimensionStagedRows,
		pipelinerefusal.DimensionSymlinkDepth,
		pipelinerefusal.DimensionLookupRecords,
		pipelinerefusal.DimensionLookupPathBytes,
		pipelinerefusal.DimensionTreeRecordBytes,
		pipelinerefusal.DimensionSourceBlobBytes,
		pipelinerefusal.DimensionSourceReadAttempts,
		pipelinerefusal.DimensionSourceReadBytes,
		pipelinerefusal.DimensionSourceReadFiles,
	}
	const limit = int64(17)
	for _, dimension := range dimensions {
		dimension := dimension
		t.Run(string(dimension), func(t *testing.T) {
			for _, observed := range []int64{1, limit} {
				if err := measuredOperationLimitError(
					"private /origin/path", dimension, observed, limit,
				); err != nil {
					t.Fatalf("observed %d at limit %d refused: %v", observed, limit, err)
				}
			}
			err := measuredOperationLimitError(
				"private /origin/path", dimension, limit+1, limit,
			)
			if !errors.Is(err, errOperationLimitRefusal) {
				t.Fatalf("one-over error = %v, want operation limit marker", err)
			}
			var measurement *pipelinerefusal.Measurement
			if !errors.As(err, &measurement) || measurement == nil ||
				measurement.Dimension != dimension ||
				measurement.Observed != limit+1 || measurement.Limit != limit {
				t.Fatalf("one-over measurement = %+v", measurement)
			}
			if strings.Contains(err.Error(), "/origin/path") {
				t.Fatalf("measurement leaked private input: %v", err)
			}
		})
	}
}

func TestExtractionBoundaryReceiptsAreClosedAndCausePreserving(t *testing.T) {
	privateCause := errors.New("private /origin/path object deadbeef child stderr")
	tests := []struct {
		name           string
		failure        func() error
		stage          pipelinerefusal.Stage
		generation     pipelinerefusal.GenerationKind
		classification pipelinerefusal.Classification
		dimension      pipelinerefusal.Dimension
		observed       int64
		limit          int64
		disposition    store.DomainOutcomeDisposition
		reason         string
	}{
		{
			name: "candidate strict-open",
			failure: func() error {
				return candidateStrictOpenFailure(errors.Join(
					privateCause, candidatepkg.ErrInvalidManifest,
				))
			},
			stage:          pipelinerefusal.StageCandidateStrictOpen,
			generation:     pipelinerefusal.GenerationCandidate,
			classification: pipelinerefusal.ClassificationInvalid,
			dimension:      pipelinerefusal.DimensionUnknown,
			disposition:    store.DomainOutcomeTerminalGenerationRefusal,
			reason:         OperationReasonFailed,
		},
		{
			name: "domain inventory",
			failure: func() error {
				measured := pipelinerefusal.Measure(
					errors.Join(privateCause, operationLimitError("private inventory")),
					pipelinerefusal.DimensionInventoryFiles, 200_001, 200_000,
				)
				return extractionBoundaryFailure(
					measured, pipelinerefusal.StageDomainInventory,
				)
			},
			stage:          pipelinerefusal.StageDomainInventory,
			generation:     pipelinerefusal.GenerationExtractionDomain,
			classification: pipelinerefusal.ClassificationLimit,
			dimension:      pipelinerefusal.DimensionInventoryFiles,
			observed:       200_001, limit: 200_000,
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
			reason:      OperationReasonLimitRefusal,
		},
		{
			name: "extractor attribution lookup",
			failure: func() error {
				measured := pipelinerefusal.Measure(
					errors.Join(privateCause, errOperationLimitRefusal),
					pipelinerefusal.DimensionLookupRecords, 100_001, 100_000,
				)
				return extractionBoundaryFailure(
					measured, pipelinerefusal.StageExtractorExecution,
				)
			},
			stage:          pipelinerefusal.StageExtractorExecution,
			generation:     pipelinerefusal.GenerationExtractionDomain,
			classification: pipelinerefusal.ClassificationLimit,
			dimension:      pipelinerefusal.DimensionLookupRecords,
			observed:       100_001, limit: 100_000,
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
			reason:      OperationReasonLimitRefusal,
		},
		{
			name: "evidence staging",
			failure: func() error {
				measured := pipelinerefusal.Measure(
					errors.Join(privateCause, errExtractionDomainBudget),
					pipelinerefusal.DimensionStagedRows, 25_001, 25_000,
				)
				return extractionBoundaryFailure(
					measured, pipelinerefusal.StageEvidenceStaging,
				)
			},
			stage:          pipelinerefusal.StageEvidenceStaging,
			generation:     pipelinerefusal.GenerationExtractionDomain,
			classification: pipelinerefusal.ClassificationLimit,
			dimension:      pipelinerefusal.DimensionStagedRows,
			observed:       25_001, limit: 25_000,
			disposition: store.DomainOutcomeRetryableFailure,
			reason:      OperationReasonDomainBudget,
		},
		{
			name: "extractor execution",
			failure: func() error {
				return extractionBoundaryFailure(
					privateCause, pipelinerefusal.StageExtractorExecution,
				)
			},
			stage:          pipelinerefusal.StageExtractorExecution,
			generation:     pipelinerefusal.GenerationExtractionDomain,
			classification: pipelinerefusal.ClassificationUnknown,
			dimension:      pipelinerefusal.DimensionUnknown,
			disposition:    store.DomainOutcomeRetryableFailure,
			reason:         OperationReasonFailed,
		},
		{
			name: "final publication",
			failure: func() error {
				return extractionBoundaryFailure(
					privateCause, pipelinerefusal.StageFinalPublication,
				)
			},
			stage:          pipelinerefusal.StageFinalPublication,
			generation:     pipelinerefusal.GenerationExtractionDomain,
			classification: pipelinerefusal.ClassificationUnknown,
			dimension:      pipelinerefusal.DimensionUnknown,
			disposition:    store.DomainOutcomeRetryableFailure,
			reason:         OperationReasonFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := test.failure()
			if !errors.Is(failure, privateCause) {
				t.Fatal("closed refusal lost its errors.Is cause")
			}
			if strings.Contains(failure.Error(), privateCause.Error()) {
				t.Fatalf("closed refusal leaked private cause: %v", failure)
			}
			projected, ok := pipelinerefusal.From(failure)
			want := pipelinerefusal.Receipt{
				Schema: pipelinerefusal.Schema,
				Stage:  test.stage, GenerationKind: test.generation,
				Classification: test.classification, Dimension: test.dimension,
				Observed: test.observed, Limit: test.limit,
			}
			if !ok || !reflect.DeepEqual(projected, want) {
				t.Fatalf("projected refusal = %+v, present=%t, want %+v", projected, ok, want)
			}

			domain := testOutcomeDomain(t, test.reason)
			raw := encodeExtractionDomainOutcomeReceipt(
				domain, test.disposition, failure,
			)
			if strings.Contains(raw, privateCause.Error()) {
				t.Fatalf("durable receipt leaked private cause: %s", raw)
			}
			var receipt ExtractionDomainOutcomeReceipt
			if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Refusal == nil || !reflect.DeepEqual(*receipt.Refusal, want) ||
				!validCertificateOutcomeReceipt(receipt) {
				t.Fatalf("durable refusal receipt = %+v", receipt)
			}
		})
	}
}

func TestExtractionOutcomeReceiptRetainsLegacyV1Shape(t *testing.T) {
	domain := testOutcomeDomain(t, OperationReasonFailed)
	raw := encodeExtractionDomainOutcomeReceipt(
		domain, store.DomainOutcomeRetryableFailure, nil,
	)
	if strings.Contains(raw, `"refusal"`) {
		t.Fatalf("legacy-compatible receipt grew an absent refusal: %s", raw)
	}
	var receipt ExtractionDomainOutcomeReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Refusal != nil || !validCertificateOutcomeReceipt(receipt) {
		t.Fatalf("legacy-compatible receipt = %+v", receipt)
	}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != raw {
		t.Fatalf("legacy v1 round trip changed bytes:\n%s\n%s", raw, canonical)
	}
}

func TestCandidateStrictOpenPersistsClosedDomainReceipt(t *testing.T) {
	repo := &store.Repo{
		Name: "private.invalid/origin", IndexedCommitHash: unitCommit,
	}
	evidence := newMemoryEvidence()
	worker := &Worker{Evidence: evidence}
	extractors := []registeredExtractor{{
		domain: "proto-contract", version: "1.0.0",
	}}
	operation := newOperationRecorder(store.Job{Target: repo.Name}, time.Now)
	operation.registerDomains(extractors, productionDomainSchedulingLimits)
	pointer := CandidateManifestPointerIdentity{
		ManifestDigest:  "sha256:" + strings.Repeat("a", 64),
		PolicyDigest:    "sha256:" + strings.Repeat("b", 64),
		ControlRevision: 1,
	}
	privateCause := errors.New("private /origin/path object deadbeef")
	openErr := candidateStrictOpenFailure(errors.Join(
		privateCause, candidatepkg.ErrInvalidManifest,
	))
	err := worker.recordManifestOpenOutcomes(
		context.Background(), repo, extractors, pointer.PolicyDigest,
		pointer, operation, openErr,
	)
	if !store.IsTerminal(err) || !errors.Is(err, privateCause) ||
		!errors.Is(err, candidatepkg.ErrInvalidManifest) ||
		strings.Contains(err.Error(), repo.Name) ||
		strings.Contains(err.Error(), privateCause.Error()) {
		t.Fatalf("strict-open error = %v", err)
	}
	outcome, outcomeErr := evidence.LatestExtractionDomainOutcome(
		context.Background(), extractionScope(repo, "proto-contract"),
	)
	if outcomeErr != nil {
		t.Fatal(outcomeErr)
	}
	var receipt ExtractionDomainOutcomeReceipt
	if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Refusal == nil ||
		receipt.Refusal.Stage != pipelinerefusal.StageCandidateStrictOpen ||
		receipt.Refusal.GenerationKind != pipelinerefusal.GenerationCandidate ||
		receipt.Refusal.Classification != pipelinerefusal.ClassificationInvalid {
		t.Fatalf("strict-open durable receipt = %+v", receipt)
	}
}

func TestCandidateStrictOpenRefusesMissingDurableControlAsInvalid(t *testing.T) {
	repo := &store.Repo{
		Name: "private.invalid/origin", IndexedCommitHash: unitCommit,
	}
	evidence := newMemoryEvidence()
	manifest := validMemoryCandidateManifest()
	provider := uncontrolledGenerationManifestProvider{
		pointer: CandidateManifestPointerIdentity{
			ManifestDigest:  manifest.Identity(),
			PolicyDigest:    "sha256:" + strings.Repeat("f", 64),
			ControlRevision: 1,
		},
		manifest: manifest,
	}
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Manifests: provider,
		Extractors: []Extractor{unitExtractor{
			domain: "proto-contract", version: "v1",
		}},
	}

	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	refusal, ok := pipelinerefusal.From(err)
	if err == nil || !ok ||
		refusal.Stage != pipelinerefusal.StageCandidateStrictOpen ||
		refusal.GenerationKind != pipelinerefusal.GenerationCandidate ||
		refusal.Classification != pipelinerefusal.ClassificationInvalid ||
		!errors.Is(err, candidatepkg.ErrInvalidManifest) ||
		strings.Contains(err.Error(), repo.Name) {
		t.Fatalf("missing-control refusal = %+v, present=%t, error=%v", refusal, ok, err)
	}
	outcome, outcomeErr := evidence.LatestExtractionDomainOutcome(
		context.Background(), extractionScope(repo, "proto-contract"),
	)
	if outcomeErr != nil {
		t.Fatal(outcomeErr)
	}
	projected := outcomeRefusal(t, outcome)
	if projected == nil || *projected != refusal {
		t.Fatalf("durable missing-control refusal = %+v, want %+v", projected, refusal)
	}
}

func testOutcomeDomain(t *testing.T, reason string) *domainOperationRecorder {
	t.Helper()
	operation := newOperationRecorder(store.Job{}, time.Now)
	operation.registerDomains(
		[]registeredExtractor{{domain: "proto-contract", version: "1.0.0"}},
		productionDomainSchedulingLimits,
	)
	domain := operation.domain(0)
	domain.complete(reason)
	return domain
}

func outcomeRefusal(
	t *testing.T,
	outcome *store.ExtractionDomainOutcome,
) *pipelinerefusal.Receipt {
	t.Helper()
	if outcome == nil {
		return nil
	}
	var receipt ExtractionDomainOutcomeReceipt
	if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt.Refusal
}

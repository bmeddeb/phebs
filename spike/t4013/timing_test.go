package t4013

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
)

func TestPartitionTimingCursorAggregatesOnlySourceFreeDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	line := `2026/08/12 12:00:00 extraction partition timing: {"schema":"phebs-extraction-partition-timing-v1","identity":"sha256:` +
		strings.Repeat("a", 64) + `","generation":"sha256:` + strings.Repeat("b", 64) +
		`","attempt":1,"outcome":"completed","reused":false,"source_acquire_ms":10,"executor_ms":20,"result_ms":5,"assembly_ms":3,"total_ms":40}` + "\n"
	if err := os.WriteFile(path, []byte("unrelated private log\n"+line), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newPartitionTimingCursor(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	reports, err := cursor.poll()
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %+v, error=%v", reports, err)
	}
	var observation ExtractionTimingObservation
	mustAddPartitionTiming(t, &observation, reports[0])
	if observation.Attempts != 1 || observation.Completed != 1 ||
		observation.ExecutorTotalMS != 20 || observation.RuntimeMaxMS != 40 ||
		validateExtractionTiming(observation) != nil {
		t.Fatalf("timing observation = %+v", observation)
	}
	if _, found, err := parsePartitionTimingLine([]byte("private source path")); err != nil || found {
		t.Fatalf("unrelated line found=%t error=%v", found, err)
	}
	invalid := reports[0]
	invalid.Schema = extractionpublication.PartitionTimingSchema + "-future"
	if extractionpublication.ValidatePartitionTimingReport(invalid) == nil {
		t.Fatal("future timing schema was accepted")
	}
}

func TestPartitionTimingAggregatesMeasuredAndUnknownRefusals(t *testing.T) {
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	report := extractionpublication.PartitionTimingReport{
		Schema:   extractionpublication.PartitionTimingSchemaV2,
		Identity: digestA, Generation: digestB, Outcome: "terminal_refusal",
		RefusalStage:          pipelinerefusal.StageEvidenceStaging,
		RefusalGenerationKind: pipelinerefusal.GenerationExtractionDomain,
		RefusalClassification: pipelinerefusal.ClassificationLimit,
		RefusalDimension:      pipelinerefusal.DimensionFacts,
		RefusalObserved:       12_501, RefusalLimit: 12_500,
	}
	var observation ExtractionTimingObservation
	mustAddPartitionTiming(t, &observation, report)
	report.Identity = digestB
	report.RefusalObserved = 12_503
	mustAddPartitionTiming(t, &observation, report)
	report.Identity = digestA
	report.RefusalStage = pipelinerefusal.StageUnknown
	report.RefusalGenerationKind = pipelinerefusal.GenerationUnknown
	report.RefusalClassification = pipelinerefusal.ClassificationUnknown
	report.RefusalDimension = pipelinerefusal.DimensionUnknown
	report.RefusalObserved, report.RefusalLimit = 0, 0
	mustAddPartitionTiming(t, &observation, report)
	if len(observation.Refusals) != 1 || observation.Refusals[0].Count != 2 ||
		observation.Refusals[0].MaxObserved != 12_503 || observation.UnknownRefusals != 1 ||
		observation.TerminalRefusals != 3 || validateExtractionTiming(observation) != nil {
		t.Fatalf("refusal aggregate = %+v", observation)
	}

	incomplete := observation
	incomplete.UnknownRefusals = 0
	if validateExtractionTiming(incomplete) == nil {
		t.Fatal("incomplete terminal-refusal aggregate was accepted")
	}
	mixed := observation
	mixed.Refusals = append(mixed.Refusals, mixed.Refusals[0])
	mixed.Refusals[1].Limit++
	mixed.Refusals[1].MaxObserved++
	if validateExtractionTiming(mixed) == nil {
		t.Fatal("mixed-limit refusal summaries were accepted")
	}

	var mixedVersion ExtractionTimingObservation
	legacy := report
	legacy.Schema = extractionpublication.PartitionTimingSchemaV1
	legacy.RefusalStage, legacy.RefusalGenerationKind = "", ""
	legacy.RefusalClassification, legacy.RefusalDimension = "", ""
	legacy.RefusalObserved, legacy.RefusalLimit = 0, 0
	mustAddPartitionTiming(t, &mixedVersion, legacy)
	current := report
	current.Identity = digestB
	current.RefusalStage = pipelinerefusal.StageEvidenceStaging
	current.RefusalGenerationKind = pipelinerefusal.GenerationExtractionDomain
	current.RefusalClassification = pipelinerefusal.ClassificationLimit
	current.RefusalDimension = pipelinerefusal.DimensionFacts
	current.RefusalObserved, current.RefusalLimit = 12_501, 12_500
	mustAddPartitionTiming(t, &mixedVersion, current)
	if mixedVersion.UnknownRefusals != 1 || len(mixedVersion.Refusals) != 1 ||
		validateExtractionTiming(mixedVersion) != nil {
		t.Fatalf("mixed-version refusal aggregate = %+v", mixedVersion)
	}
}

func TestPartitionTimingV3AttributesClosedFailuresAndWallBuckets(t *testing.T) {
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	reports := []extractionpublication.PartitionTimingReport{
		{
			Schema:   extractionpublication.PartitionTimingSchemaV3,
			Identity: digestA, Generation: digestB, Domain: "grpc-caller",
			Outcome: "failed", FailureClass: extractionpublication.PartitionFailureDeadline,
			ExecutorMS: 300_002, TotalMS: 300_010,
		},
		{
			Schema:   extractionpublication.PartitionTimingSchemaV3,
			Identity: digestB, Generation: digestA, Domain: "grpc-caller",
			Outcome: "completed", ExecutorMS: 12_000, TotalMS: 12_010,
		},
		{
			Schema:   extractionpublication.PartitionTimingSchemaV3,
			Identity: digestA, Generation: digestB, Domain: "kafka-producer",
			Outcome: "terminal_refusal", ExecutorMS: 2_000, TotalMS: 2_010,
			RefusalStage:          pipelinerefusal.StageEvidenceStaging,
			RefusalGenerationKind: pipelinerefusal.GenerationExtractionDomain,
			RefusalClassification: pipelinerefusal.ClassificationLimit,
			RefusalDimension:      pipelinerefusal.DimensionFacts,
			RefusalObserved:       769, RefusalLimit: 768,
		},
	}
	var observation ExtractionTimingObservation
	for _, report := range reports {
		if err := extractionpublication.ValidatePartitionTimingReport(report); err != nil {
			t.Fatal(err)
		}
		mustAddPartitionTiming(t, &observation, report)
	}
	if observation.Schema != extractionTimingSchemaV3 || len(observation.DomainTimings) != 2 ||
		validateExtractionTiming(observation) != nil {
		t.Fatalf("v3 timing aggregate = %+v", observation)
	}
	grpc := observation.DomainTimings[0]
	if grpc.Domain != "grpc-caller" || grpc.Attempts != 2 || grpc.Failed != 1 ||
		grpc.DeadlineFailures != 1 || grpc.ExecutorLT60S != 1 || grpc.ExecutorGE300S != 1 {
		t.Fatalf("gRPC timing = %+v", grpc)
	}
	kafka := observation.DomainTimings[1]
	if kafka.Domain != "kafka-producer" || kafka.TerminalRefusals != 1 ||
		kafka.ExecutorLT10S != 1 {
		t.Fatalf("Kafka timing = %+v", kafka)
	}

	corrupt := observation
	corrupt.DomainTimings = append([]ExtractionDomainTiming(nil), observation.DomainTimings...)
	corrupt.DomainTimings[0].DeadlineFailures = 0
	if validateExtractionTiming(corrupt) == nil {
		t.Fatal("unreconciled v3 failure class was accepted")
	}
}

func TestPartitionTimingV3UpgradeRetainsHistoricalAttemptsAsUnknown(t *testing.T) {
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	var observation ExtractionTimingObservation
	mustAddPartitionTiming(t, &observation, extractionpublication.PartitionTimingReport{
		Schema:   extractionpublication.PartitionTimingSchemaV2,
		Identity: digestA, Generation: digestB, Outcome: "failed", ExecutorMS: 20, TotalMS: 25,
	})
	mustAddPartitionTiming(t, &observation, extractionpublication.PartitionTimingReport{
		Schema:   extractionpublication.PartitionTimingSchemaV3,
		Identity: digestB, Generation: digestA, Domain: "grpc-caller",
		Outcome: "completed", ExecutorMS: 30, TotalMS: 35,
	})
	if observation.Schema != extractionTimingSchemaV3 || len(observation.DomainTimings) != 2 ||
		observation.DomainTimings[1].Domain != "unknown" ||
		observation.DomainTimings[1].UnknownFailures != 1 ||
		observation.DomainTimings[1].ExecutorUnbucketed != 1 ||
		validateExtractionTiming(observation) != nil {
		t.Fatalf("mixed v2/v3 timing = %+v", observation)
	}
}

func mustAddPartitionTiming(
	t *testing.T,
	observation *ExtractionTimingObservation,
	report extractionpublication.PartitionTimingReport,
) {
	t.Helper()
	if err := addPartitionTiming(observation, report); err != nil {
		t.Fatal(err)
	}
}

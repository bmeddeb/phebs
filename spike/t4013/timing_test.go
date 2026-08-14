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
	addPartitionTiming(&observation, reports[0])
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
	addPartitionTiming(&observation, report)
	report.Identity = digestB
	report.RefusalObserved = 12_503
	addPartitionTiming(&observation, report)
	report.Identity = digestA
	report.RefusalStage = pipelinerefusal.StageUnknown
	report.RefusalGenerationKind = pipelinerefusal.GenerationUnknown
	report.RefusalClassification = pipelinerefusal.ClassificationUnknown
	report.RefusalDimension = pipelinerefusal.DimensionUnknown
	report.RefusalObserved, report.RefusalLimit = 0, 0
	addPartitionTiming(&observation, report)
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
	addPartitionTiming(&mixedVersion, legacy)
	current := report
	current.Identity = digestB
	current.RefusalStage = pipelinerefusal.StageEvidenceStaging
	current.RefusalGenerationKind = pipelinerefusal.GenerationExtractionDomain
	current.RefusalClassification = pipelinerefusal.ClassificationLimit
	current.RefusalDimension = pipelinerefusal.DimensionFacts
	current.RefusalObserved, current.RefusalLimit = 12_501, 12_500
	addPartitionTiming(&mixedVersion, current)
	if mixedVersion.UnknownRefusals != 1 || len(mixedVersion.Refusals) != 1 ||
		validateExtractionTiming(mixedVersion) != nil {
		t.Fatalf("mixed-version refusal aggregate = %+v", mixedVersion)
	}
}

package t4013

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
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

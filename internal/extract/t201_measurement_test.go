package extract

import "testing"

func TestT201ProductionExtractionCeilings(t *testing.T) {
	if maxFactsPerRun != 5_000 ||
		maxCorpusFiles != 200_000 ||
		maxCorpusInventoryPathBytes != 16<<20 ||
		maxCorpusPathBytes != 4_096 ||
		MaxBlobBytes != 10<<20 ||
		MaxSCIPIndexBytes != 64<<20 {
		t.Fatalf("T20.1 extraction ceilings changed; review and remeasure")
	}
}

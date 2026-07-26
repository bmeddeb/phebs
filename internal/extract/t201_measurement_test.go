package extract

import "testing"

func TestT202ProductionExtractionCeilings(t *testing.T) {
	if maxFactsPerRun != 12_500 ||
		evidenceChunkSize != 256 ||
		maxCorpusFiles != 200_000 ||
		maxCorpusInventoryPathBytes != 16<<20 ||
		maxCorpusPathBytes != 4_096 ||
		MaxBlobBytes != 10<<20 ||
		MaxSCIPIndexBytes != 64<<20 {
		t.Fatalf("T20.2 extraction ceilings changed; review and remeasure")
	}
}

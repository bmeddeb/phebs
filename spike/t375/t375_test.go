package t375

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedReceiptMatchesNeutralRelationshipDemo(t *testing.T) {
	first, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("T37.5 receipt is not deterministic")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t375/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatalf("retained T37.5 receipt differs: want %s", first)
	}
	receipt := Build()
	if receipt.Outcome != "completed" || len(receipt.Cases) != 6 || receipt.Topology.AuthorizedRepositories != 3 || receipt.Reader.MaximumPageRows != 100 || !receipt.Claims.SourceFree || !receipt.Claims.NoAccuracy || !receipt.Claims.NoCompleteness || !receipt.Claims.NoRuntimeTopology || !receipt.Claims.NoSLO || !receipt.Claims.NoMigrationSafety || !receipt.Claims.NoReleaseAuthority {
		t.Fatalf("unexpected T37.5 receipt: %+v", receipt)
	}
}

// T46.1 retired live gate-name checks; the receipt bytes remain pinned above.

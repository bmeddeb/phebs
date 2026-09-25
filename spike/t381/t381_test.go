package t381

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedReceiptMatchesNeutralServiceOverview(t *testing.T) {
	first, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("T38.1 receipt is not deterministic")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t381/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatalf("retained T38.1 receipt differs: want %s", first)
	}
	receipt := Build()
	if receipt.Outcome != "completed" || len(receipt.Cases) != 5 || len(receipt.States) != 10 || receipt.Reader.FirstPageBindings != 3 || receipt.Reader.PageRows != 25 || !receipt.Claims.SourceFree || !receipt.Claims.NoAccuracy || !receipt.Claims.NoCompleteness || !receipt.Claims.NoRuntimeTopology || !receipt.Claims.NoScale || !receipt.Claims.NoSLO || !receipt.Claims.NoMigrationSafety || !receipt.Claims.NoReleaseAuthority {
		t.Fatalf("unexpected T38.1 receipt: %+v", receipt)
	}
}

// T46.1 retired live gate-name checks; the receipt bytes remain pinned above.

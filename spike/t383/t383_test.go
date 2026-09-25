package t383

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedReceiptMatchesServiceAwareWorkbench(t *testing.T) {
	first, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("T38.3 receipt is not deterministic")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t383/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatalf("retained T38.3 receipt differs: want %s", first)
	}
	receipt := Build()
	if receipt.Outcome != "completed" || len(receipt.Cases) != 5 || len(receipt.States) != 9 || receipt.Reader.SnapshotsPerRead != 2 || receipt.Reader.RowsPerSnapshot != 50 || receipt.Reader.RetainedBindings != 0 || !receipt.Claims.SourceFree || !receipt.Claims.NoImplicitWrite || !receipt.Claims.NoTaskCompletion || !receipt.Claims.NoAccuracy || !receipt.Claims.NoCompleteness || !receipt.Claims.NoRuntimeTopology || !receipt.Claims.NoScale || !receipt.Claims.NoSLO || !receipt.Claims.NoMigrationSafety || !receipt.Claims.NoDecommissionSafety || !receipt.Claims.NoReleaseAuthority {
		t.Fatalf("unexpected T38.3 receipt: %+v", receipt)
	}
}

// T46.1 retired live gate-name checks; the receipt bytes remain pinned above.

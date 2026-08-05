package t354

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedReceiptMatchesFrozenNeutralInput(t *testing.T) {
	root := repositoryRoot(t)
	neutral, err := os.ReadFile(filepath.Join(root, "spike/t323/receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := Build(neutral)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	secondReceipt, err := Build(neutral)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(secondReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("T35.4 receipt is not deterministic")
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t354/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatal("retained T35.4 receipt differs from the deterministic model")
	}
	if len(receipt.Cases) != 10 || len(receipt.Profiles) != 2 ||
		receipt.Profiles[0].Services != 1_000 || receipt.Profiles[1].Services != 5_000 ||
		!receipt.Claims.SourceFree || !receipt.Claims.NoFalseCurrent ||
		!receipt.Claims.NoSLO || !receipt.Claims.NoReleaseAuthority ||
		!receipt.Claims.NoScaleQualification {
		t.Fatalf("unexpected T35.4 receipt: %+v", receipt)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

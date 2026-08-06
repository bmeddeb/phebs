package t384

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetainedReceiptMatchesMCPMicroserviceParity(t *testing.T) {
	first, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("T38.4 receipt is not deterministic")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t384/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatalf("retained T38.4 receipt differs: want %s", first)
	}
	receipt := Build()
	if receipt.Outcome != "completed" || receipt.Tools.ParityToolCount != 6 ||
		receipt.Tools.CompleteReadToolCount != 18 || len(receipt.Cases) != 5 ||
		receipt.Bounds.ImpactResponseBytes != 8<<20 ||
		!receipt.Claims.SourceFree || !receipt.Claims.NoWrite ||
		!receipt.Claims.NoTaskCompletion || !receipt.Claims.NoDecisionAuthority ||
		!receipt.Claims.NoAccuracy || !receipt.Claims.NoCompleteness ||
		!receipt.Claims.NoRuntimeTopology || !receipt.Claims.NoMigrationSafety ||
		!receipt.Claims.NoDecommissionSafety || !receipt.Claims.NoReleaseAuthority {
		t.Fatalf("unexpected T38.4 receipt: %+v", receipt)
	}
}

func TestMCPMicroserviceParityReceiptGateNamesResolve(t *testing.T) {
	receipt := Build()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	definitions := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, item := range receipt.Cases {
			for _, gate := range item.Gates {
				if bytes.Contains(raw, []byte("func "+gate+"(")) {
					definitions[gate] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range receipt.Cases {
		for _, gate := range item.Gates {
			if !definitions[gate] {
				t.Errorf("receipt gate %q does not resolve", gate)
			}
		}
	}
}

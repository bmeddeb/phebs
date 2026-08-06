package t383

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestServiceAwareWorkbenchReceiptGateNamesResolve(t *testing.T) {
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
		if !strings.HasSuffix(entry.Name(), "_test.go") && !strings.HasSuffix(entry.Name(), ".test.tsx") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, item := range receipt.Cases {
			for _, gate := range item.Gates {
				if bytes.Contains(raw, []byte("func "+gate+"(")) || bytes.Contains(raw, []byte("test('"+gate+"'")) {
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

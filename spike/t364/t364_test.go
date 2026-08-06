package t364

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetainedReceiptMatchesNeutralMultiPackModel(t *testing.T) {
	first, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("T36.4 receipt is not deterministic")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t364/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, retained) {
		t.Fatalf("retained T36.4 receipt differs: want %s", string(firstBytes))
	}
	if first.SharedParses != 1 || len(first.Packs) != 4 || len(first.Cases) != 7 ||
		!first.Claims.SourceFree || !first.Claims.NoAccuracy || !first.Claims.NoCompleteness ||
		!first.Claims.NoSLO || !first.Claims.NoReleaseAuthority || !first.Claims.NoExtractorPromotion {
		t.Fatalf("unexpected T36.4 receipt: %+v", first)
	}
	for _, pack := range first.Packs {
		if pack.Evidence < 1 || pack.Resolved < 1 {
			t.Fatalf("neutral pack is empty: %+v", pack)
		}
	}
}

func TestReceiptGateNamesResolveToExecutableTests(t *testing.T) {
	receipt, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	definitions := make(map[string]bool)
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

package t391

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestNeutralGateReplaysFinalCatalogAndFrozenProfiles(t *testing.T) {
	root := repositoryRoot(t)
	receipt, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Correctness.MembershipEqual || !receipt.Correctness.SearchEqual ||
		!receipt.Correctness.RelationshipEqual || !receipt.Correctness.StateEqual {
		t.Fatalf("correctness replay did not pass: %+v", receipt.Correctness)
	}
	if receipt.Profiles[0].MaxPathFanout <= servicecatalog.MaxAcceptedPathFanout ||
		receipt.Profiles[1].Services <= servicecatalog.MaxServices {
		t.Fatalf("frozen profiles no longer cross their recorded limits: %+v", receipt.Profiles)
	}
}

func TestRetainedReceiptIsDeterministicAndSourceFree(t *testing.T) {
	root := repositoryRoot(t)
	firstReceipt, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Marshal(firstReceipt)
	if err != nil {
		t.Fatal(err)
	}
	secondReceipt, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(secondReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two T39.1 gate replays produced different receipt bytes")
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t391/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatal("retained T39.1 receipt differs from deterministic replay")
	}
	if _, err := Decode(retained); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/Users/", "phebs-private", "password", "clone_url", "repository_name", "query_text", "raw_error"} {
		if strings.Contains(string(retained), forbidden) {
			t.Fatalf("retained receipt contains forbidden source or operator field %q", forbidden)
		}
	}
}

func TestReceiptRejectsUnknownFieldsAndScalePromotion(t *testing.T) {
	root := repositoryRoot(t)
	receipt, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Claims.QualifiesSupportedScale = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted a synthetic supported-scale claim")
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t391/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	open := append([]byte(nil), retained[:len(retained)-2]...)
	open = append(open, []byte(",\n  \"target_slo_ms\": 1\n}\n")...)
	if _, err := Decode(open); err == nil {
		t.Fatal("strict decoder accepted an unknown target claim")
	}
}

func TestNamedProductionGatesExist(t *testing.T) {
	root := repositoryRoot(t)
	receipt, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	gates := append([]GateResult{}, receipt.FinalGates...)
	gates = append(gates, receipt.CostGates...)
	gates = append(gates, receipt.SafetyGates...)
	for _, gate := range gates {
		for _, qualified := range gate.Gates {
			separator := strings.LastIndexByte(qualified, '.')
			if separator < 1 || separator == len(qualified)-1 {
				t.Fatalf("gate %q has invalid test identity %q", gate.Name, qualified)
			}
			packagePath, testName := qualified[:separator], qualified[separator+1:]
			if !testFunctionExists(t, filepath.Join(root, filepath.FromSlash(packagePath)), testName) {
				t.Fatalf("gate %q names missing test %s", gate.Name, qualified)
			}
		}
	}
}

func testFunctionExists(t *testing.T, directory, name string) bool {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && function.Name.Name == name {
				return true
			}
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

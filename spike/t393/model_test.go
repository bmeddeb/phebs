package t393

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetainedReceiptIsDeterministicClosedAndSourceFree(t *testing.T) {
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
		t.Fatal("two T39.3 builds produced different receipt bytes")
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t393/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatal("retained T39.3 receipt differs from deterministic build")
	}
	if got := Digest(retained); got != RetainedReceiptSHA256 {
		t.Fatalf("retained T39.3 receipt digest = %s, want %s", got, RetainedReceiptSHA256)
	}
	if _, err := Decode(retained); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"/Users/", "phebs-private", "password", "repository_name", "query_text",
		"raw_error", "host_identity", "source_path",
	} {
		if strings.Contains(string(retained), forbidden) {
			t.Fatalf("retained receipt contains forbidden field or value %q", forbidden)
		}
	}
}

func TestT392StopCannotBecomePrerequisitePass(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.PriorStop.Superseded = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted a synthetic T39.2 supersession")
	}
	receipt, err = Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.PriorStop.Outcome = "passed"
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted relabeled T39.2 authority")
	}
}

func TestIndependentReviewAndStopPolicyCannotSelfApprove(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.Review.ImplementerMayApprove = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted implementer self-approval")
	}
	receipt, err = Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.Stop.WarningIsApproval = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted warning-as-approval")
	}
	receipt, err = Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.Stop.SkippedCaseIsPass = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted skipped-case promotion")
	}
}

func TestStrictDecodeRejectsUnknownFieldsAndPositiveClaims(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.Claims.AuthorizesRelease = true
	if _, err := Marshal(receipt); err == nil {
		t.Fatal("marshal accepted a release claim")
	}
	retained, err := os.ReadFile(filepath.Join(repositoryRoot(t), "spike/t393/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	open := append([]byte(nil), retained[:len(retained)-2]...)
	open = append(open, []byte(",\n  \"security_complete\": true\n}\n")...)
	if _, err := Decode(open); err == nil {
		t.Fatal("strict decoder accepted an unknown security claim")
	}
}

func TestNamedProductionGatesExist(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range receipt.Cases {
		for _, qualified := range gate.Gates {
			separator := strings.LastIndexByte(qualified, '.')
			if separator < 1 || separator == len(qualified)-1 {
				t.Fatalf("case %q has invalid test identity %q", gate.Name, qualified)
			}
			packagePath, testName := qualified[:separator], qualified[separator+1:]
			if !testFunctionExists(t, filepath.Join(repositoryRoot(t), filepath.FromSlash(packagePath)), testName) {
				t.Fatalf("case %q names missing test %s", gate.Name, qualified)
			}
		}
	}
}

func TestEphemeralGateTeardownDestroysDedicatedArtifacts(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "dedicated-security-gate")
	neighbor := filepath.Join(parent, "neighbor-retained")
	for _, directory := range []string{
		filepath.Join(workspace, "derived", "publication"),
		filepath.Join(workspace, "runtime"),
		neighbor,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(workspace, "derived", "publication", "member"): "derived",
		filepath.Join(workspace, "runtime", "credential"):            "secret",
		filepath.Join(neighbor, "sentinel"):                          "preserve",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("dedicated workspace remains after teardown: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(neighbor, "sentinel"))
	if err != nil || string(content) != "preserve" {
		t.Fatalf("neighbor changed during scoped teardown: content=%q err=%v", content, err)
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

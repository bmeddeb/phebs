package t39r1

import (
	"bytes"
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
		t.Fatal("two T39.R1 builds produced different receipt bytes")
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t39r1/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatal("retained T39.R1 receipt differs from deterministic build")
	}
	if got := Digest(retained); got != RetainedSHA256 {
		t.Fatalf("retained T39.R1 receipt digest = %s, want %s", got, RetainedSHA256)
	}
	if _, err := Decode(retained); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"/Users/", "phebs-private", "password", "repository_name", "query_text",
		"raw_error", "host_identity", "source_path", "service_key",
	} {
		if strings.Contains(string(retained), forbidden) {
			t.Fatalf("retained receipt contains forbidden field or value %q", forbidden)
		}
	}
}

func TestResolutionChangesOnlyBoundedSerializationBoundary(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Diagnosis.RequiredChange != "bounded_serialization_boundary" ||
		receipt.Diagnosis.AdmissionChange || receipt.Diagnosis.RetryChange ||
		receipt.Diagnosis.TimeoutLimitRaised || receipt.Diagnosis.AttemptLimitRaised {
		t.Fatalf("unexpected contention decision: %+v", receipt.Diagnosis)
	}
	if receipt.Resolution.PreflightBudgetMilliseconds != 300000 ||
		receipt.Resolution.ExecutionBudgetMilliseconds != 300000 ||
		receipt.Resolution.MaxAttempts != 3 {
		t.Fatalf("production limits drifted: %+v", receipt.Resolution)
	}
}

func TestClosureCannotAuthorizeOrSupersedeTargetRun(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Rerun.T392Superseded || receipt.Rerun.RerunAuthorized ||
		receipt.Claims.AuthorizesTargetRerun || receipt.Claims.SupersedesStoppedRun ||
		receipt.Claims.EstablishesSLO || receipt.Claims.EstablishesRelease {
		t.Fatalf("T39.R1 created forbidden authority: %+v %+v", receipt.Rerun, receipt.Claims)
	}
	if len(receipt.Rerun.RequiredAuthorities) != 5 {
		t.Fatalf("rerun authorities = %v", receipt.Rerun.RequiredAuthorities)
	}
	receipt.Rerun.RerunAuthorized = true
	if err := Validate(receipt); err == nil {
		t.Fatal("validator accepted rerun authority")
	}
}

func TestStrictDecodeRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	retained, err := os.ReadFile(filepath.Join(repositoryRoot(t), "spike/t39r1/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	open := append([]byte(nil), retained[:len(retained)-2]...)
	open = append(open, []byte(",\n  \"rerun\": true\n}\n")...)
	if _, err := Decode(open); err == nil {
		t.Fatal("strict decoder accepted an unknown rerun claim")
	}
	if _, err := Decode(append(retained, []byte("{}")...)); err == nil {
		t.Fatal("strict decoder accepted a trailing value")
	}
}

func TestProductionPackagesDoNotImportT39R1(t *testing.T) {
	root := repositoryRoot(t)
	files := token.NewFileSet()
	for _, directory := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			parsed, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range parsed.Imports {
				if spec.Path.Value == `"github.com/bmeddeb/phebs/spike/t39r1"` {
					t.Fatalf("production file %s imports T39.R1", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
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

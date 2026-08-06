package t394

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
		t.Fatal("two T39.4 builds produced different receipt bytes")
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t394/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatal("retained T39.4 receipt differs from deterministic build")
	}
	if got := Digest(retained); got != RetainedReceiptSHA256 {
		t.Fatalf("retained T39.4 receipt digest = %s, want %s", got, RetainedReceiptSHA256)
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

func TestUnsealedProtocolsStopBeforeMeasurement(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "stopped_before_unsealing" || !receipt.Policy.StopBeforeUnsealing {
		t.Fatalf("unexpected stop shape: outcome=%q policy=%+v", receipt.Outcome, receipt.Policy)
	}
	if receipt.Authority.MeasurementAuthorized || receipt.Authority.PredictionsUnsealed ||
		receipt.Authority.TargetEvidenceAccessed || receipt.Authority.ExistingPilotScopeBroadened {
		t.Fatalf("unsealed protocol gained authority or access: %+v", receipt.Authority)
	}
	for _, gate := range receipt.Gates {
		if gate.Outcome != "not_run" || gate.ValueState != "unmeasured" {
			t.Fatalf("gate %q reinterpreted missing measurement: %+v", gate.Name, gate)
		}
	}
}

func TestT392StopAndNegativeClaimsCannotBePromoted(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.PriorStop.Superseded = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted synthetic T39.2 supersession")
	}
	receipt, err = Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.Claims.EstablishesAccuracy = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted an accuracy claim")
	}
	receipt, err = Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt.Policy.UnderpoweredOrInconclusiveIsPass = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted inconclusive-as-pass")
	}
}

func TestCorrectionAndOwnerRoutingCostsStayMandatory(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Policy.CorrectionsCounted || !receipt.Policy.OwnerRoutingCounted {
		t.Fatal("workflow policy excluded corrections or owner routing")
	}
	receipt.Policy.CorrectionsCounted = false
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted omitted correction cost")
	}
}

func TestStrictDecodeRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	retained, err := os.ReadFile(filepath.Join(repositoryRoot(t), "spike/t394/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	open := append([]byte(nil), retained[:len(retained)-2]...)
	open = append(open, []byte(",\n  \"accuracy_pass\": true\n}\n")...)
	if _, err := Decode(open); err == nil {
		t.Fatal("strict decoder accepted an unknown accuracy claim")
	}
	if _, err := Decode(append(retained, []byte("{}")...)); err == nil {
		t.Fatal("strict decoder accepted a trailing value")
	}
}

func TestProductionPackagesDoNotImportT394(t *testing.T) {
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
				if spec.Path.Value == `"github.com/bmeddeb/phebs/spike/t394"` {
					t.Fatalf("production file %s imports T39.4", path)
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

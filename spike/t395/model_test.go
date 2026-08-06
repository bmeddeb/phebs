package t395

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
		t.Fatal("two T39.5 builds produced different receipt bytes")
	}
	retained, err := os.ReadFile(filepath.Join(root, "spike/t395/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatal("retained T39.5 receipt differs from deterministic build")
	}
	if got := Digest(retained); got != RetainedReceiptSHA256 {
		t.Fatalf("retained T39.5 receipt digest = %s, want %s", got, RetainedReceiptSHA256)
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

func TestStoppedGatesCannotAuthorizeReleaseOrContinuation(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "stop" || receipt.Validation.GateStatus != "NOT_ESTABLISHED" ||
		receipt.Validation.ReleaseDecision != "DO_NOT_RELEASE" {
		t.Fatalf("unexpected validation decision: %+v", receipt.Validation)
	}
	if receipt.Continuation.Status != "not_eligible" || receipt.Continuation.Decision != "none" ||
		receipt.Continuation.ActingPrincipal != "none" || receipt.Continuation.PositiveContinuationAuthorized {
		t.Fatalf("invalid continuation decision: %+v", receipt.Continuation)
	}
	receipt.Claims.AuthorizesRelease = true
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted release authority")
	}
}

func TestEveryPackRemainsExperimentalDark(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Packs) != 10 {
		t.Fatalf("pack decisions = %d, want 10", len(receipt.Packs))
	}
	for _, pack := range receipt.Packs {
		if pack.Status != "experimental_dark" || pack.Decision != "do_not_release" {
			t.Fatalf("pack %q was promoted: %+v", pack.PackID, pack)
		}
	}
	if !receipt.Lifecycle.DefaultDark || receipt.Lifecycle.SuspensionAppliesNow ||
		receipt.Lifecycle.RollbackRewritesHistory {
		t.Fatalf("invalid lifecycle posture: %+v", receipt.Lifecycle)
	}
}

func TestSuspensionRollbackExpiryAndRevalidationAreClosed(t *testing.T) {
	receipt, err := Build(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Lifecycle.FutureSuspensionTriggers) != 5 ||
		len(receipt.Lifecycle.RevalidationRequirements) != 8 ||
		receipt.Lifecycle.Expiry != "no_applicable_validation_exists" ||
		receipt.Lifecycle.Rollback == "" {
		t.Fatalf("incomplete lifecycle decision: %+v", receipt.Lifecycle)
	}
	receipt.Lifecycle.FutureSuspensionTriggers = receipt.Lifecycle.FutureSuspensionTriggers[:4]
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted an incomplete suspension policy")
	}
}

func TestStrictDecodeRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	retained, err := os.ReadFile(filepath.Join(repositoryRoot(t), "spike/t395/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	open := append([]byte(nil), retained[:len(retained)-2]...)
	open = append(open, []byte(",\n  \"released\": true\n}\n")...)
	if _, err := Decode(open); err == nil {
		t.Fatal("strict decoder accepted an unknown release claim")
	}
	if _, err := Decode(append(retained, []byte("{}")...)); err == nil {
		t.Fatal("strict decoder accepted a trailing value")
	}
}

func TestProductionPackagesDoNotImportT395(t *testing.T) {
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
				if spec.Path.Value == `"github.com/bmeddeb/phebs/spike/t395"` {
					t.Fatalf("production file %s imports T39.5", path)
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

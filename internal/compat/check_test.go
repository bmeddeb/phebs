package compat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/idlpreflight"
)

var (
	testChecker     *Checker
	checkerSetupErr error
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "phebs-buf-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bin := filepath.Join(dir, "buf")
	command := exec.Command("go", "build", "-o", bin, "github.com/bufbuild/buf/cmd/buf")
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := command.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build pinned buf: %v\n%s", err, output)
		os.Exit(1)
	}
	testChecker, checkerSetupErr = New(bin)
	os.Exit(m.Run())
}

func requireChecker(t *testing.T) *Checker {
	t.Helper()
	if checkerSetupErr != nil {
		t.Skipf("host cannot enforce compatibility sandbox: %v", checkerSetupErr)
	}
	return testChecker
}

func TestCheckerReportsWireBreakWithStableFieldIdentity(t *testing.T) {
	checker := requireChecker(t)
	request := Request{
		Lineage: "contract_scip_package_v1_1234",
		Before:  []File{{Path: "shop/cart.proto", Content: "syntax = \"proto3\";\npackage shop;\nmessage Cart { int32 count = 1; }\n"}},
		After:   []File{{Path: "shop/cart.proto", Content: "syntax = \"proto3\";\npackage shop;\nmessage Cart { string count = 1; }\n"}},
	}
	prepared, err := Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first, err := checker.Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := checker.Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("identical input changed result:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if !reflect.DeepEqual(first.Before, prepared.Before) ||
		!reflect.DeepEqual(first.After, prepared.After) {
		t.Fatalf(
			"pure preparation changed Check commitments: prepared=%+v result=%+v",
			prepared,
			first,
		)
	}
	if WireLimits().Policy != Policy ||
		WirePolicyDigest() !=
			"sha256:2dd268728078121506483957b90ae93f147a926f255d0ce8db36b95c16412816" {
		t.Fatal("WIRE policy contract is unstable")
	}
	if first.Compatible || first.Run.Engine != "buf" || first.Run.Version != Version ||
		first.Run.Policy != Policy || first.Run.ExitCode != 100 || first.Run.Result != "breaking" {
		t.Fatalf("verdict/run = %+v", first)
	}
	if len(first.Violations) != 1 {
		t.Fatalf("violations = %+v", first.Violations)
	}
	violation := first.Violations[0]
	if violation.Rule != "FIELD_WIRE_COMPATIBLE_TYPE" || violation.Snapshot != "after" ||
		violation.Path != "shop/cart.proto" || violation.StartLine != 3 || violation.Field == nil ||
		*violation.Field != (FieldIdentity{Lineage: request.Lineage, Message: "shop.Cart", Number: 1}) {
		t.Fatalf("violation = %+v", violation)
	}
	if len(first.AffectedFields) != 1 || first.AffectedFields[0] != *violation.Field {
		t.Fatalf("affected fields = %+v", first.AffectedFields)
	}
	if len(first.Run.Arguments) == 0 || first.Run.Arguments[0] != "breaking" ||
		strings.Contains(strings.Join(first.Run.Arguments, " "), "generate") {
		t.Fatalf("unsafe or incomplete invocation provenance: %v", first.Run.Arguments)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "int32 count") || strings.Contains(string(encoded), "string count") ||
		strings.Contains(string(encoded), "phebs-buf-") {
		t.Fatalf("result retained source or host temp path: %s", encoded)
	}
}

func TestCompatibilityPreflightKeepsFrozenRejectionBytes(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "token limit",
			source: strings.Repeat("x ", idlpreflight.MaxTokens+1),
			want: fmt.Sprintf(
				"source exceeds %d-token parser limit",
				idlpreflight.MaxTokens,
			),
		},
		{
			name:   "structural depth",
			source: strings.Repeat("{", idlpreflight.MaxStructuralDepth+1),
			want: fmt.Sprintf(
				"source exceeds %d-level structural-depth limit",
				idlpreflight.MaxStructuralDepth,
			),
		},
		{
			name:   "block comment",
			source: "/*",
			want:   "unterminated block comment",
		},
		{
			name:   "string",
			source: `"`,
			want:   "unterminated string literal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateProtoComplexity(context.Background(), test.source)
			if err == nil || err.Error() != test.want {
				t.Fatalf("validateProtoComplexity() = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCheckerAcceptsWireCompatibleRename(t *testing.T) {
	checker := requireChecker(t)
	result, err := checker.Check(context.Background(), Request{
		Lineage: "contract_scip_package_v1_rename",
		Before:  []File{{Path: "cart.proto", Content: "syntax = \"proto3\"; message Cart { string old_name = 1; }\n"}},
		After:   []File{{Path: "cart.proto", Content: "syntax = \"proto3\"; message Cart { string new_name = 1; }\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compatible || len(result.Violations) != 0 || len(result.AffectedFields) != 0 || result.Run.ExitCode != 0 {
		t.Fatalf("compatible rename = %+v", result)
	}
}

func TestCheckerRejectsUnsafeOrUnboundedInputsBeforeExec(t *testing.T) {
	checker := requireChecker(t)
	for _, test := range []struct {
		name    string
		request Request
		want    error
	}{
		{name: "empty lineage", request: Request{Before: []File{{Path: "x.proto", Content: "syntax=\"proto3\";"}}}, want: ErrInvalidInput},
		{name: "traversal", request: Request{Lineage: "lineage", Before: []File{{Path: "../x.proto", Content: "syntax=\"proto3\";"}}}, want: ErrInvalidInput},
		{name: "non proto", request: Request{Lineage: "lineage", Before: []File{{Path: "x.sh", Content: "exit 1"}}}, want: ErrInvalidInput},
		{name: "duplicate", request: Request{Lineage: "lineage", Before: []File{{Path: "x.proto"}, {Path: "x.proto"}}}, want: ErrInvalidInput},
		{name: "no files", request: Request{Lineage: "lineage"}, want: ErrInvalidInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := checker.Check(context.Background(), test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSandboxCommandPinsIsolationAndResourceGuards(t *testing.T) {
	checker := requireChecker(t)
	root := t.TempDir()
	command, err := sandboxCommand(root, checker.bin, []string{"breaking", "../after", "--against", "../before"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.Args, " ")
	for _, required := range []string{"breaking", "ulimit -t 10"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sandbox command lacks %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, "generate") {
		t.Fatalf("sandbox command contains generator capability: %s", joined)
	}
	if runtime.GOOS == "darwin" {
		for _, required := range []string{"(deny network*)", "(deny file-write*)", root} {
			if !strings.Contains(joined, required) {
				t.Fatalf("Seatbelt command lacks %q: %s", required, joined)
			}
		}
	} else {
		for _, required := range []string{"--unshare-all", "--ro-bind", "ulimit -v 524288"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("bubblewrap command lacks %q: %s", required, joined)
			}
		}
	}
}

func TestCheckerValidationRejectsUnpinnedBinary(t *testing.T) {
	requireChecker(t)
	wrong, err := New("/usr/bin/false")
	if err != nil {
		t.Skipf("false is not a usable executable on this host: %v", err)
	}
	if err := wrong.Validate(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("validation error = %v, want unavailable", err)
	}
}

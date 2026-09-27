package provider

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/scip-code/scip/bindings/go/scip"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

func TestAllSourceDocumentsRequireInventory(t *testing.T) {
	for _, change := range []string{"missing", "changed-bytes", "changed-digest", "wrong-execpath", "wrong-repository", "extra-inactive"} {
		t.Run(change, func(t *testing.T) {
			i, s, p, _, _ := fixture(t)
			roots, _ := configuredRoots(p, s)
			switch change {
			case "missing":
				p.Documents[0].Path = "missing.go"
				p.Documents[0].ExecPath = "missing.go"
			case "changed-bytes":
				p.Documents[0].Bytes++
			case "changed-digest":
				p.Documents[0].SHA256 = strings.Repeat("d", 64)
			case "wrong-execpath":
				p.Documents[0].ExecPath = "other.go"
			case "wrong-repository":
				p.Documents[0].Repository = "external+"
			case "extra-inactive":
				p.Documents = append(p.Documents, planner.Document{ID: "inactive", Kind: "source", Path: "inactive_windows.s", ExecPath: "inactive_windows.s", SHA256: strings.Repeat("d", 64), Bytes: 1})
			}
			if _, _, _, e := mapPlan(context.Background(), i, s, p, roots); !errors.Is(e, typedindex.Stale) {
				t.Fatal(e)
			}
		})
	}
	i, s, p, _, _ := fixture(t)
	roots, _ := configuredRoots(p, s)
	if _, _, _, e := mapPlan(context.Background(), i, s, p, roots); e != nil {
		t.Fatal(e)
	}
}
func TestMappingEarlyBoundsAndCancellation(t *testing.T) {
	i, s, p, _, _ := fixture(t)
	roots, _ := configuredRoots(p, s)
	tooMany := p
	tooMany.Units = make([]planner.Unit, typedindex.MaxBundleUnits+1)
	if _, _, _, e := mapPlan(context.Background(), i, s, tooMany, roots); !errors.Is(e, typedindex.Capacity) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, e := mapPlan(ctx, i, s, p, roots); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func generatedFixture(t *testing.T, enabled bool) (Invocation, Selection, planner.Plan, launcher.NativeGoFiles, []byte, []byte) {
	t.Helper()
	i, s, p, m, raw := fixture(t)
	generated := []byte("package lib\nconst Generated = 1\n")
	p.Documents = append(p.Documents, planner.Document{ID: "generated", Kind: "generated", Path: "lib/generated.go", ExecPath: "bazel-out/cfg/bin/lib/generated.go", Producer: p.Targets[0].Configured, SHA256: strings.TrimPrefix(hash(generated), "sha256:"), Bytes: len(generated)})
	p.Units[0].GoFiles = append(p.Units[0].GoFiles, "generated")
	p.Units[0].CompiledGoFiles = append(p.Units[0].CompiledGoFiles, "generated")
	sealRaw(&p)
	m.MappingSHA256 = p.MappingSHA256
	m.DocumentsSHA256 = p.DocumentsSHA256
	m.Packages[0].GoFiles = append(m.Packages[0].GoFiles, launcher.ExecRoot+"/bazel-out/cfg/bin/lib/generated.go")
	slices.Sort(m.Packages[0].GoFiles)
	if enabled {
		d := i.Profile.Definition()
		d.Schema = typedindex.GeneratedProfileSchema
		d.Config = typedindex.GeneratedConfig()
		b, _ := json.Marshal(d)
		profile, e := typedindex.DecodeProfile(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		r := typedindex.NewRequest(s.Source, profile, 1, identity(s.Targets), "run")
		b, _ = json.Marshal(r)
		a, e := typedindex.Admit(context.Background(), typedindex.Authority{Enabled: true, Administrator: true, Source: s.Source, Profile: typedindex.Epoch{Number: 1, Digest: profile.Digest()}, UniverseDigest: r.UniverseDigest}, profile, b)
		if e != nil {
			t.Fatal(e)
		}
		i.Profile = profile
		i.Parent = a
		i.Allowance.PlanningDigest = a.Digest()
	}
	return i, s, p, m, raw, generated
}
func TestGeneratedPayloadAndReducedOmission(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "omit", true: "sealed"}[enabled], func(t *testing.T) {
			i, s, p, m, raw, generated := generatedFixture(t, enabled)
			i = executing(t, i, s, p)
			var events []string
			o := neutralOperations(t, s, p, m, raw, &events)
			o.read = func(name string, _ int64) ([]byte, error) {
				if name == launcher.ExecRoot+"/bazel-out/cfg/bin/lib/generated.go" {
					return generated, nil
				}
				return proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///scratch/workspace", TextDocumentEncoding: scip.TextEncoding_UTF8, ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7", Arguments: scipArguments(s, []string{"example.test/lib"})}}, Documents: []*scip.Document{{Language: "go", RelativePath: "lib/lib.go"}, {Language: "go", RelativePath: "../bazel-output/execroot/_main/bazel-out/cfg/bin/lib/generated.go"}}})
			}
			b, e := run(context.Background(), i, o)
			if e != nil {
				t.Fatal(e)
			}
			r, e := decode[Result](b, typedsandbox.OutputBytes)
			if e != nil {
				t.Fatal(e)
			}
			if enabled {
				if len(r.Generated) != 1 {
					t.Fatal("missing generated payload")
				}
				for name, got := range r.Generated {
					if !typedindex.IsGeneratedPath(name) || string(got) != string(generated) {
						t.Fatal(name)
					}
				}
			} else if len(r.Generated) != 0 || r.Documents[1].State != "generated_omitted" {
				t.Fatal("omission not explicit")
			}
			if enabled {
				// Every generated byte (including JSON base64 expansion) spends the
				// same remaining worker allowance as plans and raw driver evidence.
				i.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes - int64(len(b))
				if exact, err := run(context.Background(), i, o); err != nil || len(exact) != len(b) {
					t.Fatal("exact aggregate allowance", err)
				}
				i.Allowance.WorkerBytesUsed++
				if _, err := run(context.Background(), i, o); !errors.Is(err, typedindex.Capacity) {
					t.Fatal("aggregate allowance overflow", err)
				}
				o.read = func(string, int64) ([]byte, error) { return []byte("wrong"), nil }
				if _, e = generatedBytes(context.Background(), r.Documents, p, o.read); !errors.Is(e, typedindex.Stale) {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestBoundedFailureEvidencePreservesFirstCause(t *testing.T) {
	i, s, p, m, raw := fixture(t)
	var events []string
	o := neutralOperations(t, s, p, m, raw, &events)
	o.plan = func(context.Context, []string) (planner.Plan, error) {
		return planner.Plan{}, planningFailureWith(typedindex.ExecutionFailed, func(_ string, s *unix.Statfs_t) error { s.Bfree = 11; s.Ffree = 7; return nil })
	}
	o.verify = func(_ context.Context, p planner.Plan, _ []original) error {
		if p.Version == "" {
			return context.Canceled
		}
		return nil
	}
	b, e := run(context.Background(), i, o)
	if !errors.Is(e, typedindex.ExecutionFailed) {
		t.Fatal(e)
	}
	r, e := decode[Result](b, typedsandbox.OutputBytes)
	if e != nil || r.Failure == nil || r.Failure.PlanningScratch == nil || !r.Failure.PlanningScratch.Available || r.Failure.PlanningScratch.FreeBlocks != 11 || r.Failure.PlanningScratch.FreeInodes != 7 || !r.Failure.FinalVerificationFailed || r.Failure.Reason != typedindex.ExecutionFailed {
		t.Fatal(r.Failure, e)
	}
	diagnostic := &ProcessDiagnostic{Comm: "compiler", State: "S", PPID: 2, PGID: 2, SID: 2, NoNewPrivs: true, CapPrm: "0000000000000000"}
	o.plan = func(context.Context, []string) (planner.Plan, error) {
		return planner.Plan{}, &QuiescenceError{Process: diagnostic}
	}
	o.quiesce = func() error { return &QuiescenceError{Process: diagnostic} }
	b, e = run(context.Background(), i, o)
	if e == nil {
		t.Fatal("quiescence passed")
	}
	r, e = decode[Result](b, typedsandbox.OutputBytes)
	if e != nil || r.Failure.PlanningProcess == nil || r.Failure.FinalProcess == nil || !r.Failure.FinalQuiescenceFailed {
		t.Fatal(e, r.Failure)
	}
}

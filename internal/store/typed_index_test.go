package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

type typedFixture struct {
	s       *Surreal
	repo    string
	profile typedindex.Profile
	intent  TypedIndexIntent
	unit    typedindex.PackageUnitID
	targets []typedindex.PlannedTarget
}

func typedTestJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func newTypedFixture(t *testing.T, s *Surreal, name string) *typedFixture {
	t.Helper()
	repo := "example.invalid/" + name
	ctx := t.Context()
	if e := s.UpsertRepo(ctx, Repo{Name: repo}); e != nil {
		t.Fatal(e)
	}
	if e := s.SetRepoIndexed(ctx, repo, strings.Repeat("a", 40), time.Now()); e != nil {
		t.Fatal(e)
	}
	tool := typedindex.Tool{Version: "0.2.7", Digest: typedDigest([]byte("tool"))}
	p, e := typedindex.DecodeProfile(ctx, typedTestJSON(t, typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: typedDigest([]byte("bundle")), ImageDigest: typedDigest([]byte("image"))}))
	if e != nil {
		t.Fatal(e)
	}
	unit, _ := typedindex.NewPackageUnitID(typedDigest([]byte("unit")))
	targets := []typedindex.PlannedTarget{{ID: typedDigest([]byte("target")), Dependencies: []string{}, Units: []typedindex.PackageUnitID{unit}}}
	intent, e := s.InstallTypedProfile(ctx, repo, p, typedDigest(typedTestJSON(t, targets)), 0)
	if e != nil {
		t.Fatal(e)
	}
	return &typedFixture{s, repo, p, intent, unit, targets}
}
func (f *typedFixture) enqueue(t *testing.T, key string) []byte {
	t.Helper()
	source, e := f.s.GetTypedSource(t.Context(), f.repo)
	if e != nil {
		t.Fatal(e)
	}
	raw := typedTestJSON(t, typedindex.NewRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, key))
	if _, e = f.s.EnqueueTypedIndex(t.Context(), f.repo, raw); e != nil {
		t.Fatal(e)
	}
	return raw
}
func (f *typedFixture) claim(t *testing.T) GenerationChunk {
	t.Helper()
	ctx := t.Context()
	spec, e := f.s.TypedIndexSchedule(ctx, f.repo)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.EnqueueGenerationSchedule(ctx, spec); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ExpandGenerationSchedule(ctx, f.repo, spec.Stage, spec.Generation); e != nil {
		t.Fatal(e)
	}
	chunk, e := f.s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "typed-worker")
	if e != nil || chunk == nil {
		t.Fatalf("claim: %+v %v", chunk, e)
	}
	return *chunk
}
func (f *typedFixture) plan(t *testing.T, parent typedindex.Admission) typedindex.PackagePlan {
	t.Helper()
	p, e := typedindex.SealPackagePlan(t.Context(), parent, typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema, ParentRequestDigest: parent.Digest(), Targets: f.targets, Units: []typedindex.PlannedUnit{{ID: f.unit, Imports: []typedindex.PackageUnitID{}, Documents: []string{"a.go"}}}, Documents: []typedindex.PlannedDocument{{Member: "a", Path: "a.go", Unit: f.unit, Bytes: 1, Digest: typedDigest([]byte("a"))}}})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func (f *typedFixture) seal(t *testing.T, chunk GenerationChunk) (typedindex.Admission, typedindex.PackagePlan) {
	t.Helper()
	w, e := f.s.BeginTypedIndex(t.Context(), chunk)
	if e != nil {
		t.Fatal(e)
	}
	f.custodyInputs(t, chunk)
	if e = f.s.AdvanceTypedIndex(t.Context(), chunk, TypedPreflight); e != nil {
		t.Fatal(e)
	}
	p := f.plan(t, w.Parent)
	a, e := f.s.SealTypedIndexPlan(t.Context(), chunk, p)
	if e != nil {
		t.Fatal(e)
	}
	return a, p
}
func (f *typedFixture) bundle(t *testing.T, a typedindex.Admission, p typedindex.PackagePlan) typedindex.Bundle {
	t.Helper()
	data, e := proto.Marshal(&scip.Index{Metadata: &scip.Metadata{Version: scip.ProtocolVersion_UnspecifiedProtocolVersion, ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}, ProjectRoot: "file:///workspace", TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{{RelativePath: "a.go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: "scip-go gomod example.test v1 A#", SymbolRoles: 1}}, Symbols: []*scip.SymbolInformation{{Symbol: "scip-go gomod example.test v1 A#", Documentation: []string{"A"}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	b, e := typedindex.BuildBundle(t.Context(), a, p, []typedindex.UnitOutcome{{Unit: f.unit, State: typedindex.UnitComplete}}, []typedindex.MemberInput{{Name: "a", SCIP: data}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestTypedIndexDurableFlow(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "typed-flow")
	raw := f.enqueue(t, "one")
	chunk := f.claim(t)
	if _, e := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, typedindex.Bundle{}); e == nil {
		t.Fatal("publish before begin accepted")
	}
	w, e := s.BeginTypedIndex(ctx, chunk)
	if e != nil {
		t.Fatal(e)
	}
	if w.Stage != TypedPreflight {
		t.Fatal(w.Stage)
	}
	if e = s.AdvanceTypedIndex(ctx, chunk, TypedExecution); e == nil {
		t.Fatal("stage skip accepted")
	}
	a, p := f.seal(t, chunk)
	if _, e = s.EnqueueTypedIndex(ctx, f.repo, raw); e != nil {
		t.Fatal(e)
	}
	status, e := s.GetTypedIndexStatus(ctx, f.repo)
	if e != nil || status.Desired != a.Digest() {
		t.Fatalf("parent retry switched successor: %+v %v", status, e)
	}
	bundle := f.bundle(t, a, p)
	f.advancePublication(t, chunk, bundle)
	if _, e = s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{Epoch: 9}, bundle); e == nil {
		t.Fatal("pointer mismatch accepted")
	}
	pointer, e := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.ResolveTypedIndexCurrent(ctx, f.repo)
	if e != nil || got != pointer {
		t.Fatalf("current: %+v %v", got, e)
	}
	if e = s.CompleteGenerationChunk(ctx, chunk); e != nil {
		t.Fatal(e)
	}
	f.enqueue(t, "replacement")
	replacement := f.claim(t)
	if _, e = s.BeginTypedIndex(ctx, replacement); e != nil {
		t.Fatal(e)
	}
	if e = s.FailTypedIndex(ctx, replacement, typedindex.Capacity); e != nil {
		t.Fatal(e)
	}
	got, e = s.ResolveTypedIndexCurrent(ctx, f.repo)
	if e != nil || got != pointer {
		t.Fatalf("failed replacement retired current: %+v %v", got, e)
	}
	status, e = s.GetTypedIndexStatus(ctx, f.repo)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CancelTypedIndex(ctx, f.repo, status.Desired); e != nil {
		t.Fatal(e)
	}
	if e = s.AdvanceTypedIndex(ctx, replacement, TypedPreflight); !errors.Is(e, typedindex.Canceled) {
		t.Fatalf("canceled lease advanced: %v", e)
	}
	got, e = s.ResolveTypedIndexCurrent(ctx, f.repo)
	if e != nil || got != pointer {
		t.Fatal("cancellation retired current")
	}
	if e = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()); e != nil {
		t.Fatal(e)
	}
	status, e = s.GetTypedIndexStatus(ctx, f.repo)
	if e != nil || !status.Stale || status.Current != nil {
		t.Fatalf("source change hid durable status: %+v %v", status, e)
	}
	f.enqueue(t, "new-source")
	status, e = s.GetTypedIndexStatus(ctx, f.repo)
	if e != nil || !status.Stale || status.Current != nil || status.Desired == "" {
		t.Fatalf("fresh request failed behind stale current: %+v %v", status, e)
	}
	if e = s.ClearTypedIndexForRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveTypedIndexCurrent(ctx, f.repo); !errors.Is(e, ErrNotFound) {
		t.Fatalf("restored pointer available: %v", e)
	}
	if _, e = s.EnqueueTypedIndex(ctx, f.repo, raw); !errors.Is(e, typedindex.Disabled) {
		t.Fatalf("restore resumed without revalidation: %v", e)
	}
}
func TestTypedIndexFencesAndResume(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "typed-resume")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	a, p := f.seal(t, chunk)
	retry, e := s.RetryGenerationChunk(ctx, chunk, "execution_failed", time.Now().Add(-time.Second))
	if e != nil || retry == nil {
		t.Fatalf("retry: %+v %v", retry, e)
	}
	if e = s.AdvanceTypedIndex(ctx, chunk, TypedExecution); e == nil {
		t.Fatal("old lease advanced")
	}
	next, e := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "next-worker")
	if e != nil || next == nil {
		t.Fatalf("reclaim: %+v %v", next, e)
	}
	work, e := s.BeginTypedIndex(ctx, *next)
	if e != nil || !work.Resume || work.Admission.Digest() != a.Digest() {
		t.Fatalf("resume: %+v %v", work, e)
	}
	f.custodyInputs(t, *next)
	if e = s.AdvanceTypedIndex(ctx, *next, TypedPreflight); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SealTypedIndexPlan(ctx, *next, p); e != nil {
		t.Fatal(e)
	}
	if _, e = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, 0); !errors.Is(e, typedindex.Stale) {
		t.Fatal("profile CAS not fenced")
	}
	if _, e = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, 1); e != nil {
		t.Fatal(e)
	}
	if e = s.AdvanceTypedIndex(ctx, *next, TypedExecution); e == nil {
		t.Fatal("old profile lease advanced")
	}
	if e = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("a", 40), time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.AdvanceTypedIndex(ctx, *next, TypedExecution); e == nil {
		t.Fatal("source ABA accepted")
	}
}

func TestTypedIndexIndependentFences(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	for _, mode := range []string{"source-ABA", "profile-ABA", "remove-readd", "cancel", "superseded-desire", "wrong-lease"} {
		t.Run(mode, func(t *testing.T) {
			f := newTypedFixture(t, s, "typed-"+mode)
			f.enqueue(t, "first")
			chunk := f.claim(t)
			work, err := s.BeginTypedIndex(ctx, chunk)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "source-ABA":
				if err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()); err != nil {
					t.Fatal(err)
				}
				if err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("a", 40), time.Now()); err != nil {
					t.Fatal(err)
				}
			case "profile-ABA":
				def := f.profile.Definition()
				def.Tools.Driver.Version = "other"
				other, err := typedindex.DecodeProfile(ctx, typedTestJSON(t, def))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.InstallTypedProfile(ctx, f.repo, other, f.intent.UniverseDigest, 1); err != nil {
					t.Fatal(err)
				}
				if _, err = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, 2); err != nil {
					t.Fatal(err)
				}
			case "remove-readd":
				if err = s.SetRepoDeleting(ctx, f.repo, true); err != nil {
					t.Fatal(err)
				}
				if err = s.DeleteRepo(ctx, f.repo); err != nil {
					t.Fatal(err)
				}
				if err = s.UpsertRepo(ctx, Repo{Name: f.repo}); err != nil {
					t.Fatal(err)
				}
				if err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("a", 40), time.Now()); err != nil {
					t.Fatal(err)
				}
				if _, err = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, 0); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if err = s.CancelTypedIndex(ctx, f.repo, work.Admission.Digest()); err != nil {
					t.Fatal(err)
				}
			case "superseded-desire":
				f.enqueue(t, "second")
			case "wrong-lease":
				chunk.LeaseToken = "forged"
			}
			if err = s.AdvanceTypedIndex(ctx, chunk, TypedPreflight); err == nil {
				t.Fatalf("%s accepted old worker", mode)
			}
			if mode != "wrong-lease" {
				_ = s.FailGenerationChunk(ctx, chunk, "typed_stale")
			}
		})
	}
}

func TestTypedIndexCoalescingAndInputRefusal(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "typed-coalesce")
	raw := f.enqueue(t, "first")
	if _, err := s.EnqueueTypedIndex(ctx, f.repo, append([]byte(" \n"), raw...)); err != nil {
		t.Fatal(err)
	}
	oldSpec, err := s.TypedIndexSchedule(ctx, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempts := range []int{1, 2, 4, 8} {
		override := oldSpec
		override.MaxAttempts = attempts
		if _, err := s.EnqueueGenerationSchedule(ctx, override); err == nil {
			t.Fatalf("caller changed owned retry policy to %d", attempts)
		}
	}
	f.enqueue(t, "second")
	if _, err = s.EnqueueGenerationSchedule(ctx, oldSpec); err == nil {
		t.Fatal("late coordinator reinstated old root")
	}
	jobs, err := s.ListJobs(ctx, JobTypedIndex, StatusPending)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("coalescing: %+v %v", jobs, err)
	}
	if _, err = s.EnqueueTypedIndex(ctx, f.repo, raw); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("superseded intent revived: %v", err)
	}
	var request typedindex.Request
	if err = json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	request.ProfileEpoch++
	if _, err = s.EnqueueTypedIndex(ctx, f.repo, typedTestJSON(t, request)); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("browser authority override: %v", err)
	}
	chunk := f.claim(t)
	if _, err = s.BeginTypedIndex(ctx, chunk); err != nil {
		t.Fatal(err)
	}
	f.custodyInputs(t, chunk)
	if err = s.AdvanceTypedIndex(ctx, chunk, TypedPreflight); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SealTypedIndexPlan(ctx, chunk, typedindex.PackagePlan{}); err == nil {
		t.Fatal("missing sealed plan accepted")
	}
	if err = s.AdvanceTypedIndex(ctx, chunk, TypedPlanning); err == nil {
		t.Fatal("planning skipped binding")
	}
	if err = s.FailTypedIndex(ctx, chunk, typedindex.Refusal("/private/raw output")); err == nil {
		t.Fatal("raw diagnostic accepted")
	}
}

package relationshippublication

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

type selectedRecoveryTestStoreV3 struct {
	*runtimeTestStoreV3
	lease         store.GenerationStaleLeaseTransition
	leaseErr      error
	leaseCancel   context.CancelFunc
	leaseCalls    int
	recoveryCalls int
	recoveryErr   error
}

func (state *selectedRecoveryTestStoreV3) ReadGenerationStaleLeaseTransition(
	ctx context.Context,
	request store.GenerationStaleLeaseTransitionRequest,
) (store.GenerationStaleLeaseTransition, error) {
	state.leaseCalls++
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 2); err != nil {
		return store.GenerationStaleLeaseTransition{}, err
	}
	if state.leaseCancel != nil {
		state.leaseCancel()
	}
	if request.Point != store.GenerationStaleLeaseTransitionCheckpointHit ||
		request.ChunkIdentity != state.lease.ChunkIdentity {
		return store.GenerationStaleLeaseTransition{}, store.ErrGenerationLeaseLost
	}
	return state.lease, state.leaseErr
}

func (state *selectedRecoveryTestStoreV3) RecoverRelationshipPublicationV3(
	_ context.Context,
	_, _, _, _ string,
	_, _ uint64,
	_ string,
) error {
	state.recoveryCalls++
	return state.recoveryErr
}

type selectedRecoveryFixtureV3 struct {
	runtime  *Runtime
	state    *selectedRecoveryTestStoreV3
	chunk    store.GenerationChunk
	expected PublicationTransitionTargetV3
	base     string
	target   string
}

func newSelectedRecoveryFixtureV3(t *testing.T) selectedRecoveryFixtureV3 {
	t.Helper()
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	repository := fixture.chunk.Repository
	publishLifecycleGenerationV3(t, fixture.runtime.relationshipRoot(), repository, "b", "prior-run", &testPublishPinsV3{})
	catalog, generation := relationshipCatalogV3Test(t, repository, 2)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	state := &selectedRecoveryTestStoreV3{runtimeTestStoreV3: &runtimeTestStoreV3{
		runtimeHandleCleanupStore: fixture.store,
		candidate: &store.ServiceCatalogV3Candidate{
			Generation: generation, ControlRevision: summary.CatalogControlRevision,
			PublishedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		},
		summary: summary, states: states,
	}}
	fixture.runtime.Store = state
	if current, err := fixture.runtime.ReconcileV3(t.Context(), repository); err != nil || current || len(state.enqueues) != 1 {
		t.Fatalf("reconcile selected target: current=%t err=%v", current, err)
	}
	binding, err := fixture.runtime.readRuntimeBindingV3(repository, state.enqueues[0].Generation)
	if err != nil {
		t.Fatal(err)
	}
	chunk := publicationTransitionClaimedChunkV3(t, binding)
	state.lease = store.GenerationStaleLeaseTransition{
		Point:      store.GenerationStaleLeaseTransitionCheckpointHit,
		Repository: repository, Stage: chunk.Stage, Generation: chunk.Generation,
		ResourceClass: chunk.ResourceClass, ScheduleDigest: chunk.ScheduleDigest,
		ScheduleStatus: store.GenerationScheduleActive, ChunkIdentity: chunk.Identity,
		Offset: chunk.Offset, Length: chunk.Length, Attempt: chunk.Attempt,
		Priority: chunk.Priority, ChunkStatus: store.GenerationChunkRunning, Leased: true,
		PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest(chunk.LeaseToken),
	}
	var expected PublicationTransitionTargetV3
	stop := errors.New("selected marker hit")
	fixture.runtime.AfterV3MarkerInstall = func(ctx context.Context, target PublicationTransitionTargetV3) error {
		expected = target
		_, counts, err := readPublicationTransitionTestV3(t, ctx, fixture.runtime.relationshipRoot(), target.Request)
		if err != nil || counts.ControlFileReads != PublicationTransitionControlFileReadsV3 {
			t.Fatalf("selected hit: counts=%+v err=%v", counts, err)
		}
		return stop
	}
	if err := fixture.runtime.HandleV3(t.Context(), chunk); !errors.Is(err, stop) {
		t.Fatalf("selected marker interruption: %v", err)
	}
	base, err := RepositoryRootV3(fixture.runtime.relationshipRoot(), repository)
	if err != nil {
		t.Fatal(err)
	}
	target, err := GenerationPathV3(fixture.runtime.relationshipRoot(), repository, expected.Request.TargetGenerationDigest)
	if err != nil {
		t.Fatal(err)
	}
	return selectedRecoveryFixtureV3{fixture.runtime, state, chunk, expected, base, target}
}

func TestRecoverSelectedV3CommitsBeforeObserver(t *testing.T) {
	for _, failReport := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "report failure"}[failReport], func(t *testing.T) {
			fixture := newSelectedRecoveryFixtureV3(t)
			// Exercise the documented external fence after HandleV3 unwound. The
			// continuation cannot reacquire even the runtime's shared lock.
			release, err := focusedindex.AcquireExclusiveMutationLock(t.Context(), filepath.Join(fixture.runtime.DataDir, "index"))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			fixture.runtime.Acquire = func(context.Context) (func(), error) {
				t.Fatal("selected recovery reacquired the shared mutation lock")
				return nil, ErrInvalid
			}
			beforePins := len(fixture.state.pinCalls)
			publication, err := OpenGenerationV3(t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
				fixture.expected.Request.TargetGenerationDigest, fixture.expected.Request.TargetRootDigest)
			if err != nil {
				t.Fatal(err)
			}
			wantPinWrites := len(publication.rootValue.Authority.Upstream.Domains)
			var observed int
			reportErr := errors.New("selected report failed")
			recovered, err := fixture.runtime.RecoverSelectedV3(t.Context(), fixture.chunk, fixture.expected,
				func(ctx context.Context, target PublicationTransitionRecoveryTargetV3) error {
					observed++
					if target.Repository != fixture.chunk.Repository ||
						target.TargetGenerationDigest != fixture.expected.Request.TargetGenerationDigest ||
						target.TargetRootDigest != fixture.expected.Request.TargetRootDigest ||
						target.FormerStageName != fixture.expected.Request.FormerStageName {
						t.Fatalf("recovered target = %+v", target)
					}
					request := fixture.expected.Request
					request.Point = PublicationTransitionRecoveredV3
					_, counts, readErr := readPublicationTransitionTestV3(t, ctx, fixture.runtime.relationshipRoot(), request)
					if readErr != nil || counts != (readaccounting.Counts{ControlFileReads: 5}) {
						t.Fatalf("recovered R = %+v, %v", counts, readErr)
					}
					if failReport {
						return reportErr
					}
					return nil
				})
			if !recovered || (failReport && !errors.Is(err, reportErr)) || (!failReport && err != nil) ||
				observed != 1 || fixture.state.recoveryCalls != 1 || fixture.state.leaseCalls != 1 || len(fixture.state.pinCalls)-beforePins != wantPinWrites {
				t.Fatalf("selected recovery: durable=%t err=%v observed=%d recovery=%d lease=%d", recovered, err, observed,
					fixture.state.recoveryCalls, fixture.state.leaseCalls)
			}
			// A failed report cannot recreate the already consumed marker or be
			// replayed as another successful recovery. Ordinary recovery stays a no-op.
			if again, err := fixture.runtime.RecoverSelectedV3(t.Context(), fixture.chunk, fixture.expected,
				func(context.Context, PublicationTransitionRecoveryTargetV3) error { observed++; return nil }); again || err == nil || observed != 1 {
				t.Fatalf("selected replay = %t, %v, observed=%d", again, err, observed)
			}
			if again, err := RecoverV3(t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository, fixture.state); again || err != nil {
				t.Fatalf("ordinary no-marker recovery changed = %t, %v", again, err)
			}
		})
	}
}

func TestRecoverSelectedV3RefusesBeforeMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *selectedRecoveryFixtureV3)
	}{
		{"repository", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.expected.Request.Repository = "example.com/other/repo"
		}},
		{"plan", func(_ *testing.T, f *selectedRecoveryFixtureV3) { f.expected.PlanDigest = fixedDigest("a") }},
		{"schedule", func(_ *testing.T, f *selectedRecoveryFixtureV3) { f.expected.ScheduleDigest = fixedDigest("a") }},
		{"attempt", func(_ *testing.T, f *selectedRecoveryFixtureV3) { f.chunk.Attempt++ }},
		{"point", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.expected.Request.Point = PublicationTransitionRecoveredV3
		}},
		{"prior generation", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.expected.Request.PriorGenerationDigest = fixedDigest("a")
		}},
		{"prior root", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.expected.Request.PriorRootDigest = fixedDigest("a")
		}},
		{"target generation", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.expected.Request.TargetGenerationDigest = fixedDigest("a")
		}},
		{"target root", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.expected.Request.TargetRootDigest = fixedDigest("a")
		}},
		{"stage", func(_ *testing.T, f *selectedRecoveryFixtureV3) { f.expected.Request.FormerStageName += "x" }},
		{"missing marker", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			selectedRecoveryRemoveV3(t, filepath.Join(f.base, "publishing.json"))
		}},
		{"replaced marker", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			marker, _, err := readPublicationMarkerV3(t.Context(), f.runtime.relationshipRoot(), f.chunk.Repository)
			if err != nil {
				t.Fatal(err)
			}
			marker.StageName += "x"
			selectedRecoveryWriteJSONV3(t, filepath.Join(f.base, "publishing.json"), marker)
		}},
		{"replaced prior", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			pointer, err := ReadPointerV3(t.Context(), f.runtime.relationshipRoot(), f.chunk.Repository)
			if err != nil {
				t.Fatal(err)
			}
			pointer.RootDigest = fixedDigest("a")
			selectedRecoveryWriteJSONV3(t, filepath.Join(f.base, "current.json"), pointer)
		}},
		{"missing installed target", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			if err := os.Rename(f.target, f.target+"-retained"); err != nil {
				t.Fatal(err)
			}
		}},
		{"target moved back to stage", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			if err := os.Rename(f.target, filepath.Join(f.base, f.expected.Request.FormerStageName)); err != nil {
				t.Fatal(err)
			}
		}},
		{"corrupt target", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			selectedRecoveryWriteJSONV3(t, filepath.Join(f.target, "root.json"), "invalid")
		}},
		{"stage residue", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			if err := os.Mkdir(filepath.Join(f.base, f.expected.Request.FormerStageName), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"temporary residue", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			selectedRecoveryWriteJSONV3(t, filepath.Join(f.base, "publishing.json.tmp"), "residue")
		}},
		{"binding changed", func(t *testing.T, f *selectedRecoveryFixtureV3) {
			selectedRecoveryWriteJSONV3(t, f.runtime.runtimeBindingPathV3(f.chunk.Repository, f.chunk.Generation), "invalid")
		}},
		{"lease token", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.state.lease.PrivateLeaseTokenDigest = store.GenerationLeaseTokenDigest(strings.Repeat("b", 32))
		}},
		{"lease absent", func(_ *testing.T, f *selectedRecoveryFixtureV3) { f.state.lease.Leased = false }},
		{"attempt requeued", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.state.lease.Priority = store.GenerationPriorityStale
		}},
		{"schedule settled", func(_ *testing.T, f *selectedRecoveryFixtureV3) {
			f.state.lease.ScheduleStatus = store.GenerationScheduleSettled
		}},
		{"schedule replaced", func(_ *testing.T, f *selectedRecoveryFixtureV3) { f.state.lease.ScheduleDigest = fixedDigest("a") }},
		{"lease read failure", func(_ *testing.T, f *selectedRecoveryFixtureV3) { f.state.leaseErr = store.ErrGenerationLeaseLost }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSelectedRecoveryFixtureV3(t)
			test.mutate(t, &fixture)
			before := selectedRecoveryControlsV3(t, fixture.base)
			pins := len(fixture.state.pinCalls)
			recovered, err := fixture.runtime.RecoverSelectedV3(t.Context(), fixture.chunk, fixture.expected,
				func(context.Context, PublicationTransitionRecoveryTargetV3) error {
					t.Fatal("refused recovery reported success")
					return nil
				})
			if recovered || err == nil || fixture.state.recoveryCalls != 0 || len(fixture.state.pinCalls) != pins ||
				!reflect.DeepEqual(before, selectedRecoveryControlsV3(t, fixture.base)) {
				t.Fatalf("refusal mutated recovery: durable=%t err=%v recovery=%d", recovered, err, fixture.state.recoveryCalls)
			}
		})
	}
}

func TestRecoverSelectedV3CancellationAndPinFailure(t *testing.T) {
	for _, point := range []string{"before", "lease", "pin"} {
		t.Run(point, func(t *testing.T) {
			fixture := newSelectedRecoveryFixtureV3(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if point == "before" {
				cancel()
			}
			if point == "lease" {
				fixture.state.leaseCancel = cancel
			}
			if point == "pin" {
				fixture.state.recoveryErr = store.ErrConflict
			}
			before := selectedRecoveryControlsV3(t, fixture.base)
			recovered, err := fixture.runtime.RecoverSelectedV3(ctx, fixture.chunk, fixture.expected,
				func(context.Context, PublicationTransitionRecoveryTargetV3) error {
					t.Fatal("failed recovery reported success")
					return nil
				})
			wantCalls := 0
			if point == "pin" {
				wantCalls = 1
			}
			if recovered || err == nil || fixture.state.recoveryCalls != wantCalls ||
				!reflect.DeepEqual(before, selectedRecoveryControlsV3(t, fixture.base)) {
				t.Fatalf("failed recovery custody: durable=%t err=%v calls=%d", recovered, err, fixture.state.recoveryCalls)
			}
			if point != "pin" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation = %v", err)
			}
		})
	}
}

func TestRecoverSelectedV3CanceledReportRetainsCommit(t *testing.T) {
	fixture := newSelectedRecoveryFixtureV3(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	recovered, err := fixture.runtime.RecoverSelectedV3(ctx, fixture.chunk, fixture.expected,
		func(context.Context, PublicationTransitionRecoveryTargetV3) error { cancel(); return nil })
	if !recovered || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled committed report = %t, %v", recovered, err)
	}
	request := fixture.expected.Request
	request.Point = PublicationTransitionRecoveredV3
	if _, _, err := readPublicationTransitionTestV3(t, t.Context(), fixture.runtime.relationshipRoot(), request); err != nil {
		t.Fatalf("cancellation lost durable recovery: %v", err)
	}
}

func TestRecoverSelectedV3InvalidBoundary(t *testing.T) {
	fixture := newSelectedRecoveryFixtureV3(t)
	observer := func(context.Context, PublicationTransitionRecoveryTargetV3) error {
		t.Fatal("invalid selected boundary invoked observer")
		return nil
	}
	before := selectedRecoveryControlsV3(t, fixture.base)
	//nolint:staticcheck // Deliberately exercise the public nil-context refusal.
	if recovered, err := fixture.runtime.RecoverSelectedV3(nil, fixture.chunk, fixture.expected, observer); recovered || !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil context = %t, %v", recovered, err)
	}
	if recovered, err := fixture.runtime.RecoverSelectedV3(t.Context(), fixture.chunk, fixture.expected, nil); recovered || !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil observer = %t, %v", recovered, err)
	}
	var absent *Runtime
	if recovered, err := absent.RecoverSelectedV3(t.Context(), fixture.chunk, fixture.expected, observer); recovered || !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil runtime = %t, %v", recovered, err)
	}
	fixture.runtime.Store = fixture.state.runtimeTestStoreV3
	if recovered, err := fixture.runtime.RecoverSelectedV3(t.Context(), fixture.chunk, fixture.expected, observer); recovered || !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing selected store capability = %t, %v", recovered, err)
	}
	if fixture.state.recoveryCalls != 0 || !reflect.DeepEqual(before, selectedRecoveryControlsV3(t, fixture.base)) {
		t.Fatal("invalid public input mutated marker custody")
	}
}

func selectedRecoveryControlsV3(t *testing.T, base string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, name := range []string{"current.json", "publishing.json", "publishing.json.tmp"} {
		raw, err := os.ReadFile(filepath.Join(base, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		result[name] = string(raw)
	}
	return result
}

func selectedRecoveryWriteJSONV3(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func selectedRecoveryRemoveV3(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

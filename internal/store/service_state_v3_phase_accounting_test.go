//go:build darwin || linux

package store

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// This is a deliberately isolated native store-cost gate, not a logical phase
// or server rehearsal. Real predecessor construction is UNMEASURED TEST SETUP.
// Measurement includes the selected connection's startup, catalog publication,
// both state schedules, actual member-nine release/reclaim, store selector
// publication and the final preimage handoff. It excludes worker heartbeat,
// HTTP/control/report loops, real relationship construction and other writers.
func TestServiceStateV3LogicalPhaseAccountingNative(t *testing.T) {
	if os.Getenv("PHEBS_TEST_LOGICAL_STATE_ACCOUNTING_NATIVE") != "1" {
		t.Skip("set PHEBS_TEST_LOGICAL_STATE_ACCOUNTING_NATIVE=1 for isolated native cost gate")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Fatal("native accounting gate requested but surreal binary not installed")
	}
	ctx := noRemovalContext(t)
	runtime, stop, err := startEngine(ctx, "memory")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	seed, err := openLocalRoot(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = seed.Close(context.Background()) })
	const repository = "example.com/neutral/logical-state-accounting"
	const servicesCount = 10_000
	const changedIndex = 5_000
	commit := strings.Repeat("7", 40)
	search := selectorTestDigest("8")
	seedServiceCatalogV3RepoContext(ctx, t, seed, repository, commit)
	services := make([]servicecatalog.Service, servicesCount)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%05d", index), DisplayName: fmt.Sprintf("Service %05d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	build := func(version string) servicecatalogv3.Generation {
		generation := serviceStateV3Generation(t, repository, commit, version, services)
		catalog, err := generation.Catalog()
		if err != nil {
			t.Fatal(err)
		}
		binding := generation.Root.Binding
		binding.Source.FileCount, binding.Source.AcceptedFileCount = servicesCount, servicesCount
		generation, err = servicecatalogv3.Build(binding, catalog)
		if err != nil {
			t.Fatal(err)
		}
		return generation
	}
	first := build("logical-state-a")
	if err := seed.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{serviceStateV3Reconcile, serviceStateV3Activate} {
		var begin ServiceStateV3Begin
		if phase == serviceStateV3Reconcile {
			begin, err = seed.BeginServiceStateV3Reconcile(ctx, repository)
		} else {
			begin, err = seed.BeginServiceStateV3Activation(ctx, repository, search)
		}
		if err != nil {
			t.Fatal(err)
		}
		runServiceStateV3PlanContext(ctx, t, seed, begin)
	}
	target := func(state *Surreal, relationship string) ServiceRuntimeTarget {
		pointer, err := state.GetServiceCatalogV3CandidatePointer(ctx, repository)
		if err != nil {
			t.Fatal(err)
		}
		summary, err := state.GetServiceStateV3SummaryPoint(ctx, repository)
		if err != nil || summary.LiveServiceCount != servicesCount || summary.TombstoneCount != 0 {
			t.Fatalf("native target summary=%+v err=%v", summary, err)
		}
		return ServiceRuntimeTarget{
			CatalogRootDigest: pointer.RootDigest, CatalogControlRevision: pointer.ControlRevision,
			StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
			SearchGenerationDigest: search, RelationshipGenerationDigest: relationship, RelationshipRootDigest: relationship,
		}
	}
	reference := func(value ServiceRuntimeTarget) ServiceCatalogV3RelationshipReference {
		return ServiceCatalogV3RelationshipReference{
			Repository: repository, CatalogRootDigest: value.CatalogRootDigest, CatalogControlRevision: value.CatalogControlRevision,
			StateControlRevision: value.StateControlRevision, StateSummaryDigest: value.StateSummaryDigest,
			RelationshipGenerationDigest: value.RelationshipGenerationDigest, RelationshipRootDigest: value.RelationshipRootDigest,
		}
	}
	priorTarget := target(seed, selectorTestDigest("a"))
	if err := seed.PinServiceCatalogV3RelationshipReference(ctx, reference(priorTarget)); err != nil {
		t.Fatal(err)
	}
	prior, err := seed.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{Repository: repository, Target: priorTarget})
	if err != nil {
		t.Fatal(err)
	}
	// Store tests cannot import focusedindex (which imports store). This is
	// its existing native lock primitive/path, not proof of the cmd wrapper.
	lockPath := filepath.Join(t.TempDir(), ".phebs-index-publication.lock")
	drain := func(state *Surreal, selected ServiceRuntimeSelector) (ServiceStateV3PreimageDrain, error) {
		lock := flock.New(lockPath, flock.SetPermissions(0o600))
		defer func() { _ = lock.Close() }()
		locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
		if err != nil || !locked {
			if err == nil {
				err = fmt.Errorf("native lock not acquired")
			}
			return ServiceStateV3PreimageDrain{}, fmt.Errorf("exclusive test custody: %w", err)
		}
		defer func() { _ = lock.Unlock() }()
		return state.DrainServiceStateV3PreimageHandoff(ctx, selected)
	}
	if cleared, err := drain(seed, prior); err != nil || !cleared.Done || cleared.Deleted != 0 {
		t.Fatalf("pre-measurement native empty preimage confirmation=%+v/%v", cleared, err)
	}
	services[changedIndex].DisplayName += " logical B"
	next := build("logical-state-b")
	descriptor := next.Root.ServiceMembers[ServiceStateV3ActivationTransitionTargetOffset]
	if len(next.Root.ServiceMembers) != 20 || descriptor.Ordinal != 9 || descriptor.Records != 512 ||
		descriptor.First > services[changedIndex].Key || descriptor.Last < services[changedIndex].Key {
		t.Fatalf("single changed service is not in actual full member nine: %+v", descriptor)
	}
	t.Log("measurement starts after real predecessor setup and empty preimage confirmation; no preceding phase is claimed")
	// This larger TEST-ONLY observer budget measures the complete component
	// even if 171 cannot fit. It grants no production/admission authority.
	controller, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: 40, Transactions: 2}},
		Phases:    []storeaccounting.Phase{{ID: 1, Transactions: 1024, Rows: 1024 * 512}},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, controller, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 1}}, AckTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	file, wire, err := transport.Open(2)
	if err != nil {
		t.Fatal(err)
	}
	client, err := storeaccounting.NewClient(ctx, file, wire)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	owner, err := newStoreCallOwner(client)
	if err != nil {
		t.Fatal(err)
	}
	type familyCount struct{ Calls, Transactions, Rows uint64 }
	families := map[string]familyCount{}
	defer func() {
		prefix, prefixErr := controller.Snapshot()
		t.Logf("accepted_prefix transactions=%d rows=%d maximum_rows=%d complete=%t error=%v", prefix.Transactions, prefix.Rows, prefix.MaximumRows, prefix.Complete, prefixErr)
		keys := make([]string, 0, len(families))
		for name := range families {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		for _, name := range keys {
			t.Logf("family=%s operations=%d transactions=%d rows=%d", name, families[name].Calls, families[name].Transactions, families[name].Rows)
		}
	}()
	measure := func(name string, operation func() error) familyCount {
		t.Helper()
		before, err := controller.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		callErr := operation()
		after, snapshotErr := controller.Snapshot()
		delta := familyCount{Calls: 1, Transactions: after.Transactions - before.Transactions, Rows: after.Rows - before.Rows}
		count := families[name]
		count.Calls += delta.Calls
		count.Transactions += delta.Transactions
		count.Rows += delta.Rows
		families[name] = count
		if callErr != nil || snapshotErr != nil {
			t.Fatalf("family=%s error=%v snapshot=%v accepted_transactions=%d accepted_rows=%d", name, callErr, snapshotErr, after.Transactions, after.Rows)
		}
		return delta
	}
	var state *Surreal
	measure("selected_connection_initialization", func() error {
		state, err = openExistingLocalWithOwner(ctx, runtime.Endpoint, "root", "root", "phebs", "phebs", owner)
		return err
	})
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	measure("catalog_publish", func() error { return state.PublishServiceCatalogV3Candidate(ctx, next) })
	var activation ServiceStateV3Begin
	var held GenerationChunk
	for _, phase := range []string{serviceStateV3Reconcile, serviceStateV3Activate} {
		var begin ServiceStateV3Begin
		measure(phase+"_begin", func() error {
			if phase == serviceStateV3Reconcile {
				begin, err = state.BeginServiceStateV3Reconcile(ctx, repository)
			} else {
				begin, err = state.BeginServiceStateV3Activation(ctx, repository, search)
				activation = begin
			}
			return err
		})
		if begin.Noop || begin.Plan == nil || begin.Plan.RemovalChunks != 0 || begin.Plan.TotalChunks != 21 {
			t.Fatalf("native no-removal layout not proven: %+v", begin.Plan)
		}
		measure(phase+"_expand", func() error {
			schedule := begin.Schedule
			for schedule.NextOffset < schedule.TotalItems {
				schedule, err = state.ExpandGenerationSchedule(ctx, schedule.Repository, schedule.Stage, schedule.Generation)
				if err != nil {
					return err
				}
			}
			return nil
		})
		applied, unchanged, replayed := 0, 0, 0
		for attempts := 0; ; attempts++ {
			if attempts > begin.Plan.TotalChunks+1 {
				t.Fatal("bounded serial schedule did not settle")
			}
			var chunk *GenerationChunk
			measure(phase+"_claim", func() error {
				chunk, err = state.ClaimGenerationChunk(ctx, GenerationResourceCPU, "logical-state-accounting")
				return err
			})
			var result ServiceStateV3ChunkResult
			delta := measure(phase+"_process", func() error {
				result, err = state.ProcessServiceStateV3Chunk(ctx, *chunk)
				return err
			})
			applied += result.Applied
			if result.Applied == 0 && !result.Settled && chunk.Identity != held.Identity {
				unchanged++
				count := families[phase+"_process_unchanged_subset"]
				count.Calls++
				count.Transactions += delta.Transactions
				count.Rows += delta.Rows
				families[phase+"_process_unchanged_subset"] = count
			}
			if phase == serviceStateV3Activate && chunk.Offset == ServiceStateV3ActivationTransitionTargetOffset && held.Identity == "" {
				held = *chunk
				measure("activation_hit_read", func() error {
					_, err := state.ReadServiceStateV3ActivationTransition(ctx, ServiceStateV3ActivationTransitionRequest{
						Point: ServiceStateV3ActivationTransitionHit, ExpectedSelector: prior,
						PlanDigest: begin.Plan.Digest, ScheduleDigest: begin.Schedule.Digest, UnitDigest: held.Identity,
					})
					return err
				})
				measure("activation_release", func() error { return state.ReleaseGenerationChunk(ctx, *chunk, "native accounting target release") })
				continue
			}
			if held.Identity != "" && chunk.Identity == held.Identity {
				if chunk.Attempt != held.Attempt || chunk.LeaseToken == held.LeaseToken || delta.Transactions != 0 {
					t.Fatal("native replay changed attempt or rewrote already-applied state")
				}
				replayed++
			}
			measure(phase+"_complete", func() error { return state.CompleteGenerationChunk(ctx, *chunk) })
			settled, err := state.GetGenerationSchedule(ctx, repository, begin.Schedule.Stage)
			if err != nil {
				t.Fatal(err)
			}
			if settled.Status == GenerationScheduleSettled {
				if settled.Failed != 0 || settled.Succeeded != 21 {
					t.Fatalf("native schedule not clean: %+v", settled)
				}
				break
			}
		}
		if applied != 1 || unchanged != 19 || (phase == serviceStateV3Activate && replayed != 1) {
			t.Fatalf("phase=%s applied=%d unchanged=%d replayed=%d", phase, applied, unchanged, replayed)
		}
		measure(phase+"_settled_noop", func() error {
			var again ServiceStateV3Begin
			if phase == serviceStateV3Reconcile {
				again, err = state.BeginServiceStateV3Reconcile(ctx, repository)
			} else {
				again, err = state.BeginServiceStateV3Activation(ctx, repository, search)
			}
			if err == nil && !again.Noop {
				return fmt.Errorf("settled begin was not a no-op")
			}
			return err
		})
	}
	nextTarget := target(state, selectorTestDigest("b"))
	measure("relationship_reference_pin", func() error { return state.PinServiceCatalogV3RelationshipReference(ctx, reference(nextTarget)) })
	var selected ServiceRuntimeSelector
	measure("selector_publish", func() error {
		selected, err = state.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
			Repository: repository, ExpectedControlRevision: prior.ControlRevision, ExpectedDigest: prior.Digest, Target: nextTarget,
		})
		return err
	})
	measure("activation_recovered_read", func() error {
		_, err := state.ReadServiceStateV3ActivationTransition(ctx, ServiceStateV3ActivationTransitionRequest{
			Point: ServiceStateV3ActivationTransitionRecovered, ExpectedSelector: selected,
			PlanDigest: activation.Plan.Digest, ScheduleDigest: activation.Schedule.Digest, UnitDigest: held.Identity,
		})
		return err
	})
	measure("preimage_handoff", func() error {
		result, err := drain(state, selected)
		if err == nil && (!result.Done || result.Deleted != 2) {
			return fmt.Errorf("logical preimage handoff=%+v", result)
		}
		return err
	})
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := owner.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	final, err := controller.Snapshot()
	if err != nil || !final.Complete || final.Producers[0].Calls != 0 || final.MaximumRows > 512 {
		t.Fatalf("final selected accounting=%+v/%v", final, err)
	}
	t.Logf("isolated_selected_store transactions=%d rows=%d maximum_rows=%d; unchanged subsets overlap process totals", final.Transactions, final.Rows, final.MaximumRows)
	if final.Transactions > 171 {
		t.Fatalf("isolated selected store alone exceeds prospective logical phase171: actual=%d", final.Transactions)
	}
}

//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// Provisioning owns the fresh image/mount; this test never mounts or removes it.
var inputNativePressureVolume = flag.String("typed-input-pressure-volume", "", "fresh private neutral ext4 pressure volume")

type nativePressureStep struct {
	Name      string                             `json:"name"`
	Observed  typedworkspace.CapacityObservation `json:"observed"`
	Projected lifecycle.Capacity                 `json:"projected"`
}

type nativeInputPressureProof struct {
	f                  *fixture
	volume             *nativePressureVolume
	budget             typedworkspace.OwnerBudget
	host               typedsandbox.HostCapacity
	hostBudget         typedsandbox.HostScratchBudget
	steps              []nativePressureStep
	begins, phaseCount int
	launches           *int
	allowances         [2]typedsandbox.Allowance
}

func nativeInputPressureFixture(t *testing.T, ctx context.Context, f *fixture) *nativeInputPressureProof {
	t.Helper()
	volume := nativePressureOpen(t, ctx, *inputNativePressureVolume)
	inventory, err := typedindex.DecodeInventory(ctx, f.raw, hash(f.raw))
	if err != nil {
		t.Fatal(err)
	}
	observed := volume.Observe(t, ctx)
	budget, err := typedworkspace.DeriveOwnerBudget(ctx, inventory, observed.BlockSize)
	if err != nil {
		t.Fatal(err)
	}
	volume.RequireBudget(t, ctx, budget)
	host, err := typedsandbox.ObserveHostScratch(ctx, "")
	if err != nil || host.Held || host.Overflow || len(host.Names) != 0 || observed.Device == host.Capacity.Device {
		t.Fatal("separate empty native backing required", err)
	}
	hostBudget, err := typedsandbox.DeriveHostScratchBudget(host.Capacity.BlockSize)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has not started its controller. Bind the mounted workspace
	// before its first real observation; the source/tool fixture stays outside it.
	if f.c.ready || f.c.workspace.Inode != 0 {
		t.Fatal("pressure fixture already started")
	}
	f.c.config.Workspace = volume.Workspace
	return &nativeInputPressureProof{f: f, volume: volume, budget: budget, host: host.Capacity, hostBudget: hostBudget}
}

func (p *nativeInputPressureProof) trackNative(c *Controller, launches *int) {
	p.launches = launches
	begin, run := c.native.begin, c.native.run
	c.native.begin = func(ctx context.Context, planning, attempt string) (typedsandbox.Allowance, error) {
		p.begins++
		return begin(ctx, planning, attempt)
	}
	c.native.run = func(ctx context.Context, o typedsandbox.Options, a typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		if p.phaseCount >= len(p.allowances) {
			return typedsandbox.Result{}, errors.New("extra pressure-proof phase")
		}
		p.allowances[p.phaseCount] = o.Allowance
		p.phaseCount++
		return run(ctx, o, a)
	}
}

func (p *nativeInputPressureProof) measure(ctx context.Context, name string, observed typedworkspace.CapacityObservation, pressure lifecycle.Pressure) error {
	if !sameBase(p.f.c.workspace, observed) {
		return errors.New("pressure workspace changed")
	}
	measured, err := p.f.c.gates[observed.Device].CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(observed.TotalBytes), AvailableBytes: int64(observed.AvailableBytes), UsedBytes: int64(observed.TotalBytes - observed.AvailableBytes)}, p.budget.Bytes)
	if measured.Pressure != pressure || (pressure == lifecycle.PressureRefuse && !errors.Is(err, lifecycle.ErrPressureRefusal)) || (pressure != lifecycle.PressureRefuse && err != nil) {
		return fmt.Errorf("unexpected %s pressure: %w", name, errors.Join(err, ErrHeld))
	}
	p.steps = append(p.steps, nativePressureStep{name, observed, measured})
	return nil
}

// Called on the actual scheduler lease/heartbeat goroutine: return errors and
// keep test assertions on the caller after Scheduler.Run has joined.
func (p *nativeInputPressureProof) beforeExecution(ctx context.Context, chunk store.GenerationChunk) error {
	for _, step := range []struct {
		name     string
		percent  int
		pressure lifecycle.Pressure
	}{{"collect", 80, lifecycle.PressureCollect}, {"hard", 90, lifecycle.PressureRefuse}, {"partial_relief", 77, lifecycle.PressureRefuse}} {
		observed, err := p.volume.setProjected(ctx, p.budget.Bytes, step.percent)
		if err != nil {
			return err
		}
		// Prepare itself must measure/admit and refuse before any immutable custody
		// or native allowance. Its durable preflight attempt is an allowed prefix.
		_, err = p.f.c.Prepare(ctx, chunk, p.f.source, p.f.raw)
		if !store.IsDeferral(err) || !errors.Is(err, lifecycle.ErrPressureRefusal) {
			return errors.New("pressure preparation was not deferred")
		}
		if err = p.measure(ctx, step.name, observed, step.pressure); err != nil {
			return err
		}
		work, err := p.f.s.BeginTypedIndex(ctx, chunk)
		if err != nil || work.Stage != store.TypedPreflight || work.Custody != nil || work.Growth != nil || work.PlanDigest != "" || work.RootDigest != "" || p.begins != 0 || *p.launches != 0 {
			return errors.New("refused pressure attempt grew or launched")
		}
		if _, err = p.f.s.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
			return errors.New("refused pressure holder")
		}
		entries, err := os.ReadDir(p.volume.Workspace)
		if err != nil || len(entries) != 1 || entries[0].Name() != ".phebs-index-publication.lock" {
			return errors.New("refused pressure physical custody")
		}
		currents, err := p.f.s.ScanTypedIndexControls(ctx, store.TypedIndexCurrents, "", 1)
		if err != nil || len(currents.Rows) != 0 || currents.Next != "" {
			return errors.New("refused pressure publication")
		}
	}
	observed, err := p.volume.clear(ctx)
	if err != nil {
		return err
	}
	// Recover the same live controller through production observe/admit before
	// any diagnostic gate read can clear its latch. No holder is acquired here.
	release, err := p.f.c.enter(ctx)
	if err != nil {
		return err
	}
	workspace, host, err := p.f.c.observe(ctx)
	if err == nil {
		_, err = p.f.c.admit(ctx, workspace, host, p.budget, p.hostBudget)
	}
	release()
	if err != nil {
		return fmt.Errorf("production pressure recovery: %w", err)
	}
	if err = p.measure(ctx, "recovered", observed, lifecycle.PressureNormal); err != nil {
		return err
	}
	if p.steps[len(p.steps)-1].Projected.UsedPercent >= lifecycle.ResumeWatermarkPercent {
		return errors.New("prospective budget did not recover")
	}
	return nil
}

func (p *nativeInputPressureProof) finish(t *testing.T, ctx context.Context, f *fixture, current store.TypedIndexCurrentCustody, out Outcome, readCurrent func() error) {
	t.Helper()
	if p.begins != 1 || p.phaseCount != 2 || *p.launches != 2 || p.allowances[0].Validate() != nil || p.allowances[1].Validate() != nil || p.allowances[0].Start != p.allowances[1].Start || p.allowances[0].Deadline != p.allowances[1].Deadline || p.allowances[0].Deadline-p.allowances[0].Start != int64(typedsandbox.WallLimit) {
		t.Fatal("pressure proof refreshed native allowance")
	}
	for _, report := range out.Reports {
		if report.ExitCode != 0 || !report.Removed || report.StopReason != "" || !report.Resources.LimitsVerified {
			t.Fatal("pressure native phase not verified")
		}
	}
	// Ballast targets actual 90% usage here; the gate still measures the full
	// prospective budget. No zero-estimate gate observation clears its latch.
	observed := p.volume.SetProjected(t, ctx, 0, 90)
	if nativePressurePercent(observed, 0) != 90 {
		t.Fatal("current protection lacks actual filesystem pressure")
	}
	if err := p.measure(ctx, "current_pressure", observed, lifecycle.PressureRefuse); err != nil {
		t.Fatal(err)
	}
	controller, err := lifecycle.NewController(f.s, LifecycleOwner{f.c})
	if err != nil {
		t.Fatal(err)
	}
	turns := 0
	tick := func() lifecycle.OwnerResult {
		t.Helper()
		p.volume.Observe(t, ctx)
		result := controller.Tick(ctx)
		turns++
		if result.Deleted < 0 || result.Deleted > 16 {
			t.Fatal("selected lifecycle mutation cap")
		}
		return result
	}
	protected := func() {
		t.Helper()
		selected := false
		for range 16 {
			result := tick()
			if result.Err != nil {
				t.Fatal("current lifecycle", result.Err)
			}
			retirement, e := f.s.InspectTypedIndexRetirement(ctx, current.PlanningDigest)
			confirmed, ce := f.s.ResolveTypedIndexCurrentCustody(ctx, current.Parent.Request().Source.Repository)
			if e != nil || !retirement.Protected() || retirement.Collecting() || ce != nil || !reflect.DeepEqual(current, confirmed) || readCurrent() != nil || *p.launches != 2 || p.begins != 1 {
				t.Fatal("pressure changed protected current", errors.Join(e, ce))
			}
			cursor, e := decodeLifecycleCursor(result.Cursor)
			if e != nil {
				t.Fatal(e)
			}
			if cursor.After == current.PlanningDigest && cursor.Root == "" {
				if result.Deleted != 0 || result.Completeness != lifecycle.LowerBound {
					t.Fatal("current root was not protected")
				}
				selected = true
				break
			}
		}
		if !selected {
			t.Fatal("current root was not selected")
		}
	}
	protected()
	if err = f.s.CancelTypedIndex(ctx, current.Parent.Request().Source.Repository, current.PlanningDigest); err != nil {
		t.Fatal(err)
	}
	protected() // A canceled desired request does not remove current authority.
	p.volume.Clear(t, ctx)
	id := typedworkspace.OwnerIdentity{PlanningDigest: current.PlanningDigest, AttemptDigest: current.AttemptDigest, ChunkIdentity: current.ChunkIdentity, LeaseDigest: current.LeaseDigest, Request: current.Parent.Request()}
	pin, err := typedworkspace.OpenOwnerPublication(ctx, f.c.config.Workspace, id, current.Parent, current.Admission, current.Pointer.Binding.PlanDigest, current.Pointer.RootDigest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pin.Close() })
	release, err := f.c.enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = f.s.DeleteRepo(ctx, current.Parent.Request().Source.Repository)
	release()
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	for range 16 {
		result := tick()
		if result.Err != nil {
			cursor, e := decodeLifecycleCursor(result.Cursor)
			retirement, re := f.s.InspectTypedIndexRetirement(ctx, current.PlanningDigest)
			if e != nil || re != nil || !retirement.Collecting() || !result.AdvanceOnError || cursor.After != current.PlanningDigest || result.Deleted != 0 || !errors.Is(result.Err, ErrHeld) {
				t.Fatal("unexpected pinned retirement refusal", result.Err)
			}
			pinned = true
			break
		}
	}
	if !pinned {
		t.Fatal("reader pin did not block selected physical drain")
	}
	if err = pin.Close(); err != nil {
		t.Fatal(err)
	}
	resumed := false
	for range 16 {
		result := tick()
		if result.Err != nil {
			t.Fatal("partial drain", result.Err)
		}
		inspected, e := typedworkspace.InspectDrainOwner(ctx, f.c.config.Workspace, drainBase(f.c.workspace), drainAuthority(current.Custody))
		if e != nil {
			t.Fatal(e)
		}
		if inspected.Resume && !inspected.Absent {
			resumed = true
			break
		}
	}
	if !resumed {
		t.Fatal("collecting physical prefix not observed")
	}
	fresh, err := New(f.c.config)
	if err != nil {
		t.Fatal(err)
	}
	freshRun := fresh.native.run
	fresh.native.run = func(ctx context.Context, o typedsandbox.Options, a typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		*p.launches++
		return freshRun(ctx, o, a)
	}
	if err = fresh.Startup(ctx); err != nil || !fresh.ready || *p.launches != 2 {
		t.Fatal("collecting prefix restart", err)
	}
	f.c = fresh
	controller, err = lifecycle.NewController(f.s, LifecycleOwner{fresh})
	if err != nil {
		t.Fatal(err)
	}
	drained, tombstones := false, 0
	for range 12000 {
		result := tick()
		if result.Err != nil {
			t.Fatal("bounded selected drain", result.Err)
		}
		entries, e := os.ReadDir(f.c.config.Workspace)
		if e != nil {
			t.Fatal(e)
		}
		if len(entries) == 1 && entries[0].Name() == ".phebs-index-publication.lock" {
			drained, tombstones, e = nativePressureControlsDrained(ctx, f.s, current.Parent.Request().Source.Repository)
			if e != nil {
				t.Fatal(e)
			}
			if drained {
				break
			}
		}
	}
	host, err := typedsandbox.ObserveHostScratch(ctx, "")
	if !drained || tombstones < 0 || tombstones > 2 || err != nil || host.Held || host.Overflow || len(host.Names) != 0 || *p.launches != 2 || p.begins != 1 {
		t.Fatal("pressure lifecycle cleanup", err)
	}
	record := map[string]any{"schema": "phebs-typed-neutral-pressure-lifecycle-v1", "steps": p.steps, "workspace_budget": p.budget, "host_observed": p.host, "host_budget": p.hostBudget, "source": current.Parent.Request().Source, "profile_digest": current.Parent.Request().ProfileDigest, "inventory_digest": current.Parent.Request().BundleDigest, "planning_digest": current.PlanningDigest, "execution_digest": current.Admission.Digest(), "root_digest": current.Pointer.RootDigest, "allowances": p.allowances, "phase_reports": out.Reports, "native_launches": *p.launches, "native_allowances": p.begins, "current_protected": true, "canceled_current_protected": true, "routed_content_unchanged": true, "reader_pin_refused": pinned, "collecting_restart": resumed, "retirement": "fixture_repository_deletion", "lifecycle_turns": turns, "retained_parent_tombstones": tombstones, "workspace_drained": drained, "native_absent": true}
	raw := encode(t, record)
	if len(raw) > acceptanceMaxReceipt {
		t.Fatal("pressure receipt bound")
	}
	t.Logf("native pressure receipt: %s", raw)
}

func nativePressureControlsDrained(ctx context.Context, s *store.Surreal, repo string) (bool, int, error) {
	complete, tombstones := true, 0
	for _, kind := range []store.TypedIndexControlKind{store.TypedIndexRequests, store.TypedIndexPlans, store.TypedIndexAttempts, store.TypedIndexStates, store.TypedIndexCurrents, store.TypedIndexIntents} {
		page, err := s.ScanTypedIndexControls(ctx, kind, "", 8)
		if err != nil {
			return false, 0, err
		}
		if page.Next != "" {
			return false, 0, errors.New("unexpected pressure control count")
		}
		for _, row := range page.Rows {
			if kind == store.TypedIndexRequests && row.Parent && row.ID == row.Root && row.State == "collecting" && row.Repository == repo {
				tombstones++
				continue
			}
			complete = false
		}
	}
	if tombstones > 2 {
		return false, 0, errors.New("unexpected pressure parent tombstones")
	}
	return complete, tombstones, nil
}

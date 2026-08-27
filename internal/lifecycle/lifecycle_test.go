package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

type generationStoreFunc func(
	context.Context, string, int, int, int,
) (store.GenerationLifecycleSweep, error)

func (fn generationStoreFunc) SweepGenerationScheduleLifecycle(
	ctx context.Context, cursor string, scan, deletes, retained int,
) (store.GenerationLifecycleSweep, error) {
	return fn(ctx, cursor, scan, deletes, retained)
}

type memoryJobStore struct {
	deletedBefore []*time.Time
	pages         []store.JobLifecyclePage
}

func (memory *memoryJobStore) ScanJobLifecycle(
	_ context.Context, _ store.JobKind, _ string, _ int,
) (store.JobLifecyclePage, error) {
	if len(memory.pages) == 0 {
		return store.JobLifecyclePage{}, nil
	}
	page := memory.pages[0]
	memory.pages = memory.pages[1:]
	return page, nil
}

func (memory *memoryJobStore) DeleteOldestTerminalJobs(
	_ context.Context, _ store.JobKind, before *time.Time, limit int,
) (int, error) {
	memory.deletedBefore = append(memory.deletedBefore, before)
	if before == nil {
		return limit, nil
	}
	return 0, nil
}

func TestGateWatermarksProjectionAndHysteresis(t *testing.T) {
	used := int64(790)
	gate := &Gate{dataDir: t.TempDir()}
	gate.probe = func(context.Context, string) (Capacity, error) {
		return Capacity{TotalBytes: 1_000, AvailableBytes: 1_000 - used, UsedBytes: used}, nil
	}

	capacity, err := gate.Check(t.Context(), 0)
	if err != nil || capacity.Pressure != PressureNormal || capacity.UsedPercent != 79 {
		t.Fatalf("normal capacity = %+v, %v", capacity, err)
	}
	used = 800
	capacity, err = gate.Check(t.Context(), 0)
	if err != nil || capacity.Pressure != PressureCollect || capacity.UsedPercent != 80 {
		t.Fatalf("soft capacity = %+v, %v", capacity, err)
	}
	used = 700
	capacity, err = gate.Check(t.Context(), 200)
	if !errors.Is(err, ErrPressureRefusal) || capacity.Pressure != PressureRefuse {
		t.Fatalf("projected hard capacity = %+v, %v", capacity, err)
	}
	used = 760
	capacity, err = gate.Check(t.Context(), 0)
	if !errors.Is(err, ErrPressureRefusal) || capacity.Pressure != PressureRefuse {
		t.Fatalf("latched capacity = %+v, %v", capacity, err)
	}
	used = 740
	capacity, err = gate.Check(t.Context(), 0)
	if err != nil || capacity.Pressure != PressureNormal {
		t.Fatalf("resumed capacity = %+v, %v", capacity, err)
	}
}

func TestGateUnavailableAndRootHardening(t *testing.T) {
	gate := &Gate{dataDir: t.TempDir(), probe: func(context.Context, string) (Capacity, error) {
		return Capacity{}, errors.New("unavailable")
	}}
	capacity, err := gate.Check(t.Context(), 0)
	if !errors.Is(err, ErrCapacityUnavailable) || capacity.Pressure != PressureUnavailable {
		t.Fatalf("unavailable capacity = %+v, %v", capacity, err)
	}

	realRoot := t.TempDir()
	symlink := filepath.Join(t.TempDir(), "data")
	if err := os.Symlink(realRoot, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeCapacity(t.Context(), symlink); err == nil {
		t.Fatal("ProbeCapacity accepted a symlink data root")
	}
}

func TestStatusMonitorIsBoundedSourceFreeAndKeepsLastOwnerResult(t *testing.T) {
	var calls []string
	owners := []Owner{
		recordingOwner{name: "alpha", calls: &calls},
		recordingOwner{name: "bravo", calls: &calls},
	}
	monitor, err := NewStatusMonitor(true, owners)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 8, 5, 20, 0, 0, 0, time.UTC)
	monitor.now = func() time.Time { return fixed }
	monitor.ObserveOwner(OwnerResult{
		Owner: "bravo", Completeness: LowerBound, Scanned: 64,
		Deleted: 16, More: true,
	})
	monitor.ObserveOwner(OwnerResult{
		Owner: "bravo", Completeness: Unavailable,
		Err: errors.New("private path /do/not/expose"),
	})
	monitor.ObserveCapacity(Capacity{
		TotalBytes: 1_000, UsedBytes: 810, UsedPercent: 81,
		Pressure: PressureCollect,
	}, nil)

	status := monitor.Snapshot()
	if err := ValidateStatus(status); err != nil {
		t.Fatal(err)
	}
	if status.Owners[0].State != "not_run" || status.Owners[1].State != "error" ||
		status.Owners[1].Scanned != 0 || status.Owners[1].Deleted != 0 ||
		status.Owners[1].AttemptedAt == nil ||
		status.Capacity.UsedPercent == nil || *status.Capacity.UsedPercent != 81 {
		t.Fatalf("lifecycle status = %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private")) || len(encoded) > 16<<10 {
		t.Fatalf("status leaked an error or exceeded bound: %d bytes: %s", len(encoded), encoded)
	}
}

type memoryCursorStore struct {
	mu        sync.Mutex
	values    map[string]string
	revisions map[string]uint64
}

func newMemoryCursorStore() *memoryCursorStore {
	return &memoryCursorStore{values: map[string]string{}, revisions: map[string]uint64{}}
}

func (store *memoryCursorStore) GetLifecycleCursor(
	_ context.Context, key string,
) (string, uint64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.values[key], store.revisions[key], nil
}

func (store *memoryCursorStore) CompareAndSwapLifecycleCursor(
	_ context.Context, key string, expected uint64, value string,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.revisions[key] != expected {
		return ErrCursorConflict
	}
	store.values[key] = value
	store.revisions[key]++
	return nil
}

type recordingOwner struct {
	name  string
	calls *[]string
	fail  bool
}

type retryOnceRecordingOwner struct {
	name     string
	calls    *[]string
	errOnce  bool
	moreOnce bool
}

type blockingOwner struct {
	started chan<- time.Time
	release <-chan struct{}
}

func (blockingOwner) Name() string { return "blocking" }

func (owner blockingOwner) Sweep(
	_ context.Context, attemptedAt time.Time, cursor string, _ Limits,
) OwnerResult {
	owner.started <- attemptedAt
	<-owner.release
	return OwnerResult{Cursor: cursor, Completeness: Exact}
}

type blockingRecordingOwner struct {
	name    string
	calls   *[]string
	turn    int
	blockAt int
	started chan<- struct{}
	release <-chan struct{}
}

func (owner *blockingRecordingOwner) Name() string { return owner.name }

func (owner *blockingRecordingOwner) Sweep(
	_ context.Context, _ time.Time, cursor string, _ Limits,
) OwnerResult {
	*owner.calls = append(*owner.calls, owner.name+":"+cursor)
	owner.turn++
	if owner.turn == owner.blockAt {
		owner.started <- struct{}{}
		<-owner.release
	}
	return OwnerResult{Cursor: cursor + "x", Scanned: 1, Deleted: 1, Completeness: Exact}
}

func TestStatusMonitorKeepsSweepStartAcrossStraddledFence(t *testing.T) {
	started := make(chan time.Time, 1)
	release := make(chan struct{})
	owner := blockingOwner{started: started, release: release}
	controller, err := NewController(newMemoryCursorStore(), owner)
	if err != nil {
		t.Fatal(err)
	}
	beforeFence := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	controller.now = func() time.Time { return beforeFence }
	result := make(chan OwnerResult, 1)
	go func() { result <- controller.Tick(t.Context()) }()
	if got := <-started; !got.Equal(beforeFence) {
		t.Fatalf("sweep started at %s, want %s", got, beforeFence)
	}

	monitor, err := NewStatusMonitor(true, []Owner{owner})
	if err != nil {
		t.Fatal(err)
	}
	monitor.now = func() time.Time { return beforeFence.Add(time.Hour) }
	close(release)
	monitor.ObserveOwner(<-result)
	got := monitor.Snapshot().Owners[0].AttemptedAt
	if got == nil || !got.Equal(beforeFence) {
		t.Fatalf("published attempt = %v, want sweep start %s", got, beforeFence)
	}
}

type lifecycleObservationPins struct{}

func (lifecycleObservationPins) Pinned(string, string) bool { return false }

func TestObservationOwnerIdentitylessCollectionAdvancesWithoutPendingLoop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "observations")
	for _, digit := range []string{"1", "2"} {
		repositoryRoot := filepath.Join(root, strings.Repeat(digit, 64))
		if err := os.MkdirAll(filepath.Join(
			repositoryRoot, "collecting-"+strings.Repeat(digit, 64),
		), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	owner := ObservationGenerationOwner{
		Root: root, Pins: lifecycleObservationPins{},
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
	}
	store := newMemoryCursorStore()
	controller, err := NewController(store, owner)
	if err != nil {
		t.Fatal(err)
	}
	for turn, wantCursor := range []string{strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("1", 64)} {
		result := controller.Tick(t.Context())
		if result.Err != nil || result.Scanned != 1 || result.Deleted != 0 ||
			!result.More || result.Cursor != wantCursor {
			t.Fatalf("orphan collecting turn %d = %+v", turn, result)
		}
	}

	// A single identity-less root must not advertise perpetual pending work,
	// and its unchanged durable cursor remains valid because the candidate was
	// inspected and deliberately retained.
	singleRoot := filepath.Join(t.TempDir(), "observations")
	digest := strings.Repeat("3", 64)
	if err := os.MkdirAll(filepath.Join(singleRoot, digest, "collecting-"+digest), 0o700); err != nil {
		t.Fatal(err)
	}
	singleStore := newMemoryCursorStore()
	singleController, err := NewController(singleStore, ObservationGenerationOwner{
		Root: singleRoot, Pins: lifecycleObservationPins{},
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for turn := range 2 {
		result := singleController.Tick(t.Context())
		if result.Err != nil || result.Scanned != 1 || result.Deleted != 0 ||
			result.More || result.Cursor != digest || result.Completeness != Exact {
			t.Fatalf("single orphan collecting turn %d = %+v", turn, result)
		}
	}
}

func (owner recordingOwner) Name() string { return owner.name }

func (owner recordingOwner) Sweep(
	_ context.Context, _ time.Time, cursor string, _ Limits,
) OwnerResult {
	*owner.calls = append(*owner.calls, owner.name+":"+cursor)
	if owner.fail {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: errors.New("owner failed")}
	}
	return OwnerResult{Cursor: cursor + "x", Scanned: 1, Deleted: 1, Completeness: Exact}
}

func (owner *retryOnceRecordingOwner) Name() string { return owner.name }

func (owner *retryOnceRecordingOwner) Sweep(
	_ context.Context, _ time.Time, cursor string, _ Limits,
) OwnerResult {
	*owner.calls = append(*owner.calls, owner.name+":"+cursor)
	if owner.errOnce {
		owner.errOnce = false
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: errors.New("owner failed")}
	}
	if owner.moreOnce {
		owner.moreOnce = false
		return OwnerResult{Cursor: cursor + "x", Scanned: 1, More: true, Completeness: LowerBound}
	}
	return OwnerResult{Cursor: cursor + "x", Scanned: 1, Deleted: 1, Completeness: Exact}
}

func TestControllerPersistsFairRotationAcrossRestartAndLocalizesFailure(t *testing.T) {
	store := newMemoryCursorStore()
	var calls []string
	owners := []Owner{
		recordingOwner{name: "alpha", calls: &calls},
		recordingOwner{name: "bravo", calls: &calls, fail: true},
		recordingOwner{name: "charlie", calls: &calls},
	}
	for range 4 {
		controller, err := NewController(store, owners...)
		if err != nil {
			t.Fatal(err)
		}
		_ = controller.Tick(t.Context())
	}
	want := []string{"alpha:", "bravo:", "charlie:", "alpha:x"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("owner calls = %v, want %v", calls, want)
	}
	if got := store.values["owner:bravo"]; got != "" {
		t.Fatalf("failed owner cursor = %q, want unchanged", got)
	}
}

func TestRunnerBacklogDelayAcceleratesOnlyPressureRecovery(t *testing.T) {
	for _, test := range []struct {
		name     string
		backlog  time.Duration
		pressure bool
		want     time.Duration
	}{
		{name: "ordinary backlog", backlog: DefaultBacklogDelay, want: DefaultBacklogDelay},
		{name: "pressure recovery", backlog: DefaultBacklogDelay, pressure: true, want: DefaultPressureRecoveryDelay},
		{name: "short configured retry", backlog: time.Millisecond, pressure: true, want: time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runnerBacklogDelay(test.backlog, test.pressure); got != test.want {
				t.Fatalf("delay = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRunnerUsesPressureRecoveryDelayUntilCleanCycle(t *testing.T) {
	var calls []string
	controller, err := NewController(newMemoryCursorStore(), recordingOwner{name: "owner", calls: &calls})
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	gate := &Gate{dataDir: t.TempDir()}
	gate.probe = func(context.Context, string) (Capacity, error) {
		checks++
		used := int64(700)
		if checks == 1 {
			used = 800
		}
		return Capacity{TotalBytes: 1_000, AvailableBytes: 1_000 - used, UsedBytes: used}, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan OwnerResult, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, controller, gate, DefaultIdleInterval, DefaultBacklogDelay, func(result OwnerResult) {
			results <- result
		}, nil)
	}()

	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for range 3 {
		select {
		case <-results:
		case <-deadline.C:
			t.Fatal("runner did not use the pressure-recovery delay")
		}
	}
	select {
	case result := <-results:
		t.Fatalf("runner did not idle after the clean recovery cycle: %+v", result)
	case <-time.After(500 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after pressure recovery")
	}
}

func TestRunnerCompletesAProcessObservedCycleBeforeIdle(t *testing.T) {
	store := newMemoryCursorStore()
	store.values[rotationCursorKey] = "bravo"
	var calls []string
	owners := []Owner{
		recordingOwner{name: "alpha", calls: &calls},
		recordingOwner{name: "bravo", calls: &calls},
		recordingOwner{name: "charlie", calls: &calls},
	}
	controller, err := NewController(store, owners...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan OwnerResult, 4)
	done := make(chan struct{})
	reported := 0
	go func() {
		defer close(done)
		Run(ctx, controller, nil, time.Hour, time.Millisecond, func(result OwnerResult) {
			reported++
			results <- result
			if reported == cap(results) {
				cancel()
			}
		}, nil)
	}()

	for range cap(results) {
		select {
		case <-results:
		case <-time.After(time.Second):
			t.Fatal("runner idled at the end of a durable rotation suffix")
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after the observed cycle")
	}
	want := []string{"charlie:", "alpha:", "bravo:", "charlie:x"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("owner calls = %v, want %v", calls, want)
	}
}

func TestRunnerCompletesFourteenOwnerCycleFromEveryDurableCursor(t *testing.T) {
	names := []string{
		CatalogOwner,
		JobOwner,
		GenerationScheduleOwner,
		InvestigationOwner,
		ObservationOwner,
		ObservationV2Owner,
		PartialStageOwner,
		ProofOwner,
		ReaderOwner,
		RelationshipOwner,
		ResolverOwner,
		SearchOwner,
		TombstoneOwner,
		SourceOwner,
	}
	for _, cursor := range append([]string{""}, names...) {
		testName := cursor
		if testName == "" {
			testName = "empty"
		}
		t.Run(testName, func(t *testing.T) {
			store := newMemoryCursorStore()
			store.values[rotationCursorKey] = cursor
			owners := make([]Owner, 0, len(names))
			for _, name := range names {
				owners = append(owners, StaticOwner{OwnerName: name, Completeness: Exact})
			}
			controller, err := NewController(store, owners...)
			if err != nil {
				t.Fatal(err)
			}

			next := 0
			for index, name := range names {
				if name == cursor {
					next = index + 1
					if next == len(names) {
						next = 0
					}
					break
				}
			}
			want := append([]string(nil), names[next:]...)
			if next != 0 {
				want = append(want, names...)
			}
			if len(want) > 27 {
				t.Fatalf("clean cycle turns = %d; want at most 27", len(want))
			}

			ctx, cancel := context.WithCancel(t.Context())
			results := make(chan OwnerResult, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, controller, nil, time.Hour, time.Millisecond, func(result OwnerResult) {
					results <- result
				}, nil)
			}()

			got := make([]string, 0, len(want))
			for range want {
				select {
				case result := <-results:
					got = append(got, result.Owner)
				case <-time.After(time.Second):
					cancel()
					t.Fatal("runner idled before completing the fresh cycle")
				}
			}
			var extra string
			select {
			case result := <-results:
				extra = result.Owner
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runner did not stop after the fresh cycle")
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("owner order = %v, want %v", got, want)
			}
			if extra != "" {
				t.Fatalf("runner did not idle after the fresh cycle; next owner = %s", extra)
			}
		})
	}
}

func TestRunnerCompletesFreshCycleAfterPressureRecovery(t *testing.T) {
	for _, test := range []struct {
		name          string
		rotation      string
		normalAfter   int
		unavailableAt int
		want          []string
	}{
		{name: "middle owner", normalAfter: 2},
		{name: "final owner", normalAfter: 3},
		{
			name: "unavailable at cycle end", normalAfter: 4, unavailableAt: 3,
			want: []string{
				"alpha:", "bravo:", "charlie:", "alpha:x", "bravo:x", "charlie:x",
				"alpha:xx", "bravo:xx", "charlie:xx",
			},
		},
		{name: "unavailable at cycle end without prior pressure", normalAfter: 1, unavailableAt: 3},
		{name: "unavailable at middle owner without prior pressure", normalAfter: 1, unavailableAt: 2},
		{
			name: "normal then unavailable at cycle end", rotation: "bravo",
			normalAfter: 2, unavailableAt: 4,
			want: []string{
				"charlie:", "alpha:", "bravo:", "charlie:x",
				"alpha:x", "bravo:x", "charlie:xx", "alpha:xx",
				"bravo:xx", "charlie:xxx",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryCursorStore()
			store.values[rotationCursorKey] = test.rotation
			var calls []string
			controller, err := NewController(store,
				recordingOwner{name: "alpha", calls: &calls},
				recordingOwner{name: "bravo", calls: &calls},
				recordingOwner{name: "charlie", calls: &calls},
			)
			if err != nil {
				t.Fatal(err)
			}
			capacityChecks := 0
			gate := &Gate{dataDir: t.TempDir()}
			gate.probe = func(context.Context, string) (Capacity, error) {
				capacityChecks++
				if capacityChecks == test.unavailableAt {
					return Capacity{}, errors.New("capacity unavailable")
				}
				used := int64(800)
				if capacityChecks >= test.normalAfter {
					used = 700
				}
				return Capacity{
					TotalBytes: 1_000, AvailableBytes: 1_000 - used, UsedBytes: used,
				}, nil
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := test.want
			if want == nil {
				want = []string{"alpha:", "bravo:", "charlie:", "alpha:x", "bravo:x", "charlie:x"}
			}
			results := make(chan OwnerResult, len(want)+1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, controller, gate, time.Hour, time.Millisecond, func(result OwnerResult) {
					results <- result
				}, nil)
			}()

			for range len(want) {
				select {
				case <-results:
				case <-time.After(time.Second):
					t.Fatal("runner idled before completing the fresh recovery cycle")
				}
			}
			select {
			case result := <-results:
				t.Fatalf("runner did not idle after the fresh recovery cycle: %+v", result)
			case <-time.After(50 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runner did not stop after the fresh recovery cycle")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("owner calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestRunnerCompletesCycleStartedAfterMidSweepPressureRecovery(t *testing.T) {
	var calls []string
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	first := &blockingRecordingOwner{
		name: "alpha", calls: &calls, blockAt: 2, started: started, release: release,
	}
	controller, err := NewController(newMemoryCursorStore(), first,
		recordingOwner{name: "bravo", calls: &calls},
		recordingOwner{name: "charlie", calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	used := int64(800)
	gate := &Gate{dataDir: t.TempDir()}
	gate.probe = func(context.Context, string) (Capacity, error) {
		return Capacity{TotalBytes: 1_000, AvailableBytes: 1_000 - used, UsedBytes: used}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan OwnerResult, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, controller, gate, time.Hour, time.Millisecond, func(result OwnerResult) {
			results <- result
		}, nil)
	}()
	for range 3 {
		select {
		case <-results:
		case <-time.After(time.Second):
			t.Fatal("runner did not observe the pressure cycle")
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("next cycle did not begin under pressure")
	}
	used = 700
	close(release)
	for range 6 {
		select {
		case <-results:
		case <-time.After(time.Second):
			t.Fatal("runner idled before a wholly post-recovery cycle")
		}
	}
	select {
	case result := <-results:
		t.Fatalf("runner did not idle after the wholly post-recovery cycle: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after pressure recovery")
	}
	want := []string{
		"alpha:", "bravo:", "charlie:",
		"alpha:x", "bravo:x", "charlie:x",
		"alpha:xx", "bravo:xx", "charlie:xx",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("owner calls = %v, want %v", calls, want)
	}
}

func TestRunnerPressureRecoveryWaitsForDrainedNormalCycle(t *testing.T) {
	for _, test := range []struct {
		name string
		more bool
		err  bool
		want []string
	}{
		{
			name: "backlog", more: true,
			want: []string{
				"charlie:", "alpha:", "bravo:", "charlie:x", "alpha:x",
				"bravo:x", "charlie:xx", "alpha:xx", "bravo:xx", "charlie:xxx",
				"alpha:xxx", "bravo:xxx", "charlie:xxxx",
			},
		},
		{
			name: "error", err: true,
			want: []string{
				"charlie:", "alpha:", "bravo:", "charlie:x", "alpha:x",
				"bravo:", "charlie:xx", "alpha:xx", "bravo:x", "charlie:xxx",
				"alpha:xxx", "bravo:xx", "charlie:xxxx",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryCursorStore()
			store.values[rotationCursorKey] = "bravo"
			var calls []string
			middle := &retryOnceRecordingOwner{
				name: "bravo", calls: &calls, moreOnce: test.more, errOnce: test.err,
			}
			controller, err := NewController(store,
				recordingOwner{name: "alpha", calls: &calls}, middle,
				recordingOwner{name: "charlie", calls: &calls},
			)
			if err != nil {
				t.Fatal(err)
			}
			capacityChecks := 0
			gate := &Gate{dataDir: t.TempDir()}
			gate.probe = func(context.Context, string) (Capacity, error) {
				capacityChecks++
				if capacityChecks == 7 {
					return Capacity{}, errors.New("capacity unavailable")
				}
				used := int64(700)
				if capacityChecks == 1 {
					used = 800
				}
				return Capacity{
					TotalBytes: 1_000, AvailableBytes: 1_000 - used, UsedBytes: used,
				}, nil
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			results := make(chan OwnerResult, len(test.want)+1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, controller, gate, time.Hour, time.Millisecond, func(result OwnerResult) {
					results <- result
				}, nil)
			}()

			for range len(test.want) {
				select {
				case <-results:
				case <-time.After(time.Second):
					t.Fatal("runner idled before a drained wholly-normal recovery cycle")
				}
			}
			select {
			case result := <-results:
				t.Fatalf("runner did not idle after the drained recovery cycle: %+v", result)
			case <-time.After(50 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runner did not stop after the drained recovery cycle")
			}
			if !reflect.DeepEqual(calls, test.want) {
				t.Fatalf("owner calls = %v, want %v", calls, test.want)
			}
		})
	}
}

func TestRunnerRetriesFailedCycleEndWithoutIdle(t *testing.T) {
	store := newMemoryCursorStore()
	var calls []string
	owners := []Owner{
		recordingOwner{name: "alpha", calls: &calls},
		recordingOwner{name: "bravo", calls: &calls, fail: true},
	}
	controller, err := NewController(store, owners...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan OwnerResult, 3)
	reported := 0
	go Run(ctx, controller, nil, time.Hour, time.Millisecond, func(result OwnerResult) {
		reported++
		results <- result
		if reported == cap(results) {
			cancel()
		}
	}, nil)
	for range cap(results) {
		select {
		case <-results:
		case <-time.After(time.Second):
			t.Fatal("runner idled after the cycle-ending owner failed")
		}
	}
	if want := []string{"alpha:", "bravo:", "alpha:x"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("owner calls = %v, want %v", calls, want)
	}
}

func TestRunnerRetriesAfterMiddleOwnerCycleDemand(t *testing.T) {
	tests := []struct {
		name      string
		errOnce   bool
		moreOnce  bool
		wantCalls []string
	}{
		{
			name:      "error once",
			errOnce:   true,
			wantCalls: []string{"alpha:", "bravo:", "charlie:", "alpha:x", "bravo:", "charlie:x"},
		},
		{
			name:      "more once",
			moreOnce:  true,
			wantCalls: []string{"alpha:", "bravo:", "charlie:", "alpha:x", "bravo:x", "charlie:x"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryCursorStore()
			var calls []string
			middle := &retryOnceRecordingOwner{
				name: "bravo", calls: &calls,
				errOnce: test.errOnce, moreOnce: test.moreOnce,
			}
			controller, err := NewController(store,
				recordingOwner{name: "alpha", calls: &calls},
				middle,
				recordingOwner{name: "charlie", calls: &calls},
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			results := make(chan OwnerResult, len(test.wantCalls)+1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, controller, nil, time.Hour, time.Millisecond, func(result OwnerResult) {
					results <- result
				}, nil)
			}()

			for range len(test.wantCalls) {
				select {
				case <-results:
				case <-time.After(time.Second):
					t.Fatal("runner idled before completing a clean cycle")
				}
			}
			select {
			case result := <-results:
				t.Fatalf("runner did not idle after the clean cycle: %+v", result)
			case <-time.After(50 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runner did not stop after the clean cycle")
			}
			if !reflect.DeepEqual(calls, test.wantCalls) {
				t.Fatalf("owner calls = %v, want %v", calls, test.wantCalls)
			}
		})
	}
}

type advancingFailureOwner struct{}

func (advancingFailureOwner) Name() string { return "advancing-failure" }

func (advancingFailureOwner) Sweep(
	_ context.Context, _ time.Time, _ string, _ Limits,
) OwnerResult {
	return OwnerResult{
		Cursor: "isolated", Scanned: 1, More: true,
		AdvanceOnError: true, Completeness: Unavailable,
		Err: errors.New("isolated namespace failure"),
	}
}

func TestControllerPersistsExplicitIsolatedCursorOnError(t *testing.T) {
	store := newMemoryCursorStore()
	controller, err := NewController(store, advancingFailureOwner{})
	if err != nil {
		t.Fatal(err)
	}
	result := controller.Tick(t.Context())
	if result.Err == nil || !result.AdvanceOnError {
		t.Fatalf("isolated failure result = %+v", result)
	}
	if got := store.values["owner:advancing-failure"]; got != "isolated" {
		t.Fatalf("isolated failure cursor = %q, want isolated", got)
	}
}

func TestControllerRejectsDuplicateOwners(t *testing.T) {
	store := newMemoryCursorStore()
	var calls []string
	_, err := NewController(store,
		recordingOwner{name: "same", calls: &calls},
		recordingOwner{name: "same", calls: &calls},
	)
	if err == nil {
		t.Fatal("NewController accepted duplicate owners")
	}
}

func TestGenerationOwnerHonorsAggregateQueryBudgetAndBackupLock(t *testing.T) {
	called := false
	owner := GenerationOwner{
		Store: generationStoreFunc(func(
			_ context.Context, _ string, scan, deletes, retained int,
		) (store.GenerationLifecycleSweep, error) {
			called = true
			if scan != MaxOwnerQueriesPerTick-1 || deletes != MaxDeletesPerTick ||
				retained != GenerationScheduleRetained {
				t.Fatalf("generation limits = scan %d deletes %d retained %d", scan, deletes, retained)
			}
			return store.GenerationLifecycleSweep{}, nil
		}),
		Acquire: func(context.Context) (func(), error) {
			return func() {}, nil
		},
	}
	result := owner.Sweep(t.Context(), time.Now().UTC(), "", DefaultLimits())
	if result.Err != nil || !called {
		t.Fatalf("generation owner = %+v, called=%t", result, called)
	}

	called = false
	owner.Acquire = func(context.Context) (func(), error) {
		return nil, context.DeadlineExceeded
	}
	result = owner.Sweep(t.Context(), time.Now().UTC(), "", DefaultLimits())
	if !errors.Is(result.Err, context.DeadlineExceeded) || called {
		t.Fatalf("backup lock refusal = %+v, called=%t", result, called)
	}
}

func TestJobOwnerPersistsPhasesAndCompletesAllTables(t *testing.T) {
	memory := &memoryJobStore{}
	owner := JobOwnerImpl{
		Store:   memory,
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
	}
	cursor := ""
	var result OwnerResult
	for turn := 0; turn < len(lifecycleJobKinds)*2; turn++ {
		result = owner.Sweep(t.Context(), time.Now().UTC(), cursor, DefaultLimits())
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		cursor = result.Cursor
	}
	if result.More || result.Completeness != LowerBound || cursor != "" {
		t.Fatalf("completed job lifecycle = %+v", result)
	}
	if len(memory.deletedBefore) != len(lifecycleJobKinds) {
		t.Fatalf("age passes = %d, want %d", len(memory.deletedBefore), len(lifecycleJobKinds))
	}
}

func TestJobOwnerTrimIsBounded(t *testing.T) {
	memory := &memoryJobStore{}
	owner := JobOwnerImpl{
		Store:   memory,
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
	}
	cursor, err := encodeJobCursor(jobCursor{Phase: "trim", Remaining: 20})
	if err != nil {
		t.Fatal(err)
	}
	result := owner.Sweep(t.Context(), time.Now().UTC(), cursor, DefaultLimits())
	if result.Err != nil || result.Deleted != MaxDeletesPerTick || !result.More {
		t.Fatalf("first trim = %+v", result)
	}
	state, err := decodeJobCursor(result.Cursor)
	if err != nil || state.Remaining != 4 {
		t.Fatalf("trim cursor = %+v, %v", state, err)
	}
}

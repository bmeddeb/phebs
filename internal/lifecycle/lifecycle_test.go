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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

const (
	t422LifecycleParkPath      = "/api/t422/lifecycle/park"
	t422LifecycleNormalDrive   = "/api/t422/lifecycle/drive-normal"
	t422LifecycleRecoveryDrive = "/api/t422/lifecycle/drive-recovery"
	t422LifecycleFreshDrive    = "/api/t422/lifecycle/drive-fresh"
	t422LifecycleNormalRead    = "/api/t422/lifecycle/normal-cycle"
	t422LifecycleCollectRead   = "/api/t422/lifecycle/pressure-80"
	t422LifecycleRefuseRead    = "/api/t422/lifecycle/pressure-90"
	t422LifecycleLatchedRead   = "/api/t422/lifecycle/pressure-75"
	t422LifecycleRecoveryRead  = "/api/t422/lifecycle/recovery-cycle"
	t422LifecycleResumedRead   = "/api/t422/lifecycle/recovered-normal"
	t422LifecycleFreshRead     = "/api/t422/lifecycle/fresh-cycle"
	t422LifecycleFenceHeader   = "X-Phebs-T422-Ballast-Unix-Nano"
	t422LifecycleReadBytes     = 16 << 10
	t422LifecycleEventBytes    = 1 << 10
)

var errT422LifecycleControl = errors.New("T42.2 lifecycle control refused")

// One source-owned sequence shares the existing runner, cursor and Gate. The
// pressure epoch retains one collector through 80/90/75; the restored epoch
// has its own collector. No command reopens ordinary owners or resumes timers.
type t422LifecycleControl struct {
	ctx       context.Context
	launch    *t422SemanticLaunch
	runner    *lifecycle.RunnerControl
	collector *lifecycle.CycleCollector
	mu        sync.Mutex
	step      uint8
	busy      bool
	err       error
	cycle     lifecycle.CycleObservation
	names     []string
	sink      func([]byte) error
	operation string
	phase     uint32
	prefix    [3]t422LifecyclePrefix
}

type t422LifecyclePrefix struct {
	ReturnedTicks, OwnerTurns, Deleted, MaxDeleted uint64
}

type t422LifecycleEvent struct {
	Schema          string `json:"schema"`
	Epoch           uint64 `json:"epoch"`
	Phase           uint32 `json:"phase"`
	ReturnedTick    uint64 `json:"returned_tick"`
	Owner           string `json:"owner,omitempty"`
	AttemptedAtNano int64  `json:"attempted_at_unix_nano,omitempty"`
	Scanned         int    `json:"scanned"`
	Deleted         int    `json:"deleted"`
	LogicalBytes    int64  `json:"logical_bytes"`
	RootBytes       int64  `json:"root_bytes"`
	MemberBytes     int64  `json:"member_bytes"`
	Completeness    string `json:"completeness"`
	Failed          bool   `json:"failed"`
	OwnerTurns      uint64 `json:"owner_turns"`
	TotalDeleted    uint64 `json:"total_deleted"`
	MaxDeleted      uint64 `json:"max_deleted"`
}

func newT422LifecycleControl(ctx context.Context, launch *t422SemanticLaunch, owners []lifecycle.Owner) (*t422LifecycleControl, error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || err != nil ||
		!launch.matches(current) || len(owners) != 16 {
		return nil, errT422LifecycleControl
	}
	control := &t422LifecycleControl{ctx: ctx, launch: launch, runner: lifecycle.NewRunnerControl(),
		sink: t4013ExactReportSink("exact lifecycle turn: ")}
	for _, owner := range owners {
		if owner == nil {
			return nil, errT422LifecycleControl
		}
		control.names = append(control.names, owner.Name())
	}
	if launch.request.ServerEpoch == 4 || launch.request.ServerEpoch == 5 {
		control.collector, err = lifecycle.NewCycleCollector(owners, lifecycle.MaxCycleObservationTurns)
		if err != nil {
			return nil, errT422LifecycleControl
		}
	}
	return control, nil
}

func t422LifecycleCommand(path string) bool {
	return path == t422LifecycleParkPath || path == t422LifecycleNormalDrive ||
		path == t422LifecycleRecoveryDrive || path == t422LifecycleFreshDrive
}

func t422LifecycleRead(path string) bool {
	switch path {
	case t422LifecycleNormalRead, t422LifecycleCollectRead, t422LifecycleRefuseRead,
		t422LifecycleLatchedRead, t422LifecycleRecoveryRead, t422LifecycleResumedRead, t422LifecycleFreshRead:
		return true
	}
	return false
}

// A fence is only the authenticated parent's timestamp, never evidence that
// ballast was created, truncated or removed. The parent separately owns that
// native mutation and must preserve a monotonic fence after prior capacity.
func t422LifecycleRequest(request *http.Request, command bool) (time.Time, error) {
	if request == nil || request.URL == nil || request.URL.Path != request.URL.EscapedPath() ||
		request.URL.RawQuery != "" || request.URL.ForceQuery || request.ContentLength != 0 || len(request.TransferEncoding) != 0 ||
		command && request.Method != http.MethodPost || !command && request.Method != http.MethodGet {
		return time.Time{}, errT422LifecycleControl
	}
	path := request.URL.Path
	if command && !t422LifecycleCommand(path) || !command && !t422LifecycleRead(path) {
		return time.Time{}, errT422LifecycleControl
	}
	wantsFence := path == t422LifecycleCollectRead || path == t422LifecycleRefuseRead ||
		path == t422LifecycleLatchedRead || path == t422LifecycleRecoveryDrive
	values := request.Header.Values(t422LifecycleFenceHeader)
	if !wantsFence {
		if len(values) != 0 {
			return time.Time{}, errT422LifecycleControl
		}
		return time.Time{}, nil
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 19 {
		return time.Time{}, errT422LifecycleControl
	}
	nanoseconds, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || nanoseconds <= 0 || strconv.FormatInt(nanoseconds, 10) != values[0] {
		return time.Time{}, errT422LifecycleControl
	}
	fence := time.Unix(0, nanoseconds).UTC()
	if fence.After(time.Now()) {
		return time.Time{}, errT422LifecycleControl
	}
	return fence, nil
}

func (control *t422LifecycleControl) expected(path string, phase uint32) bool {
	if control.step == 0 {
		return path == t422LifecycleParkPath && phase == control.launch.initial.Phase
	}
	switch control.launch.request.ServerEpoch {
	case 4:
		switch control.step {
		case 1:
			return path == t422LifecycleNormalDrive && phase == 9
		case 2:
			return path == t422LifecycleNormalRead && phase == 9
		case 3:
			return path == t422LifecycleCollectRead && phase == 9
		case 4:
			return path == t422LifecycleRefuseRead && phase == 10
		case 5:
			return path == t422LifecycleLatchedRead && phase == 11
		case 6:
			return path == t422LifecycleRecoveryDrive && phase == 11
		case 7:
			return path == t422LifecycleRecoveryRead && phase == 11
		case 8:
			return path == t422LifecycleResumedRead && phase == 11
		}
	case 5:
		return control.step == 1 && path == t422LifecycleFreshDrive && phase == 13 ||
			control.step == 2 && path == t422LifecycleFreshRead && phase == 13
	}
	return false
}

func (control *t422LifecycleControl) current(ctx context.Context, drained bool) bool {
	if ctx == nil || ctx.Err() != nil || control.ctx.Err() != nil {
		return false
	}
	control.mu.Lock()
	failed := control.err != nil
	control.mu.Unlock()
	if failed {
		return false
	}
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	current, err := dispatchadmission.ProductionSemanticState()
	return ok && err == nil && control.launch.sameRequest(admitted, current) &&
		(!drained || admitted.OrdinaryOwnersDrained && current.OrdinaryOwnersDrained)
}

func (control *t422LifecycleControl) begin(ctx context.Context, path string) error {
	if !control.current(ctx, path != t422LifecycleParkPath) {
		return control.stop()
	}
	admitted := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	control.mu.Lock()
	valid := control.err == nil && !control.busy && control.expected(path, admitted.Phase)
	if valid {
		control.busy = true
		control.operation, control.phase = path, admitted.Phase
	}
	control.mu.Unlock()
	if !valid {
		return control.stop()
	}
	return nil
}

// ObserveOwner records actual returned Tick facts once, before encoding or
// synchronous sink work. A Tick failing before Sweep has no owner turn. The
// existing runner owns delivery even when cancellation follows returned work;
// a native panic before Tick returns remains unavailable, never a zero proof.
func (control *t422LifecycleControl) ObserveOwner(result lifecycle.OwnerResult) {
	defer func() {
		if recover() != nil {
			_ = control.stop()
		}
	}()
	control.mu.Lock()
	index := -1
	switch control.operation {
	case t422LifecycleNormalDrive:
		index = 0
	case t422LifecycleRecoveryDrive:
		index = 1
	case t422LifecycleFreshDrive:
		index = 2
	}
	if !control.busy || index < 0 || control.prefix[index].ReturnedTicks == uint64(lifecycle.MaxCycleObservationTurns) {
		control.mu.Unlock()
		_ = control.stop()
		return
	}
	prefix := &control.prefix[index]
	prefix.ReturnedTicks++
	event := t422LifecycleEvent{Schema: "phebs-t422-lifecycle-turn-v1", Epoch: control.launch.request.ServerEpoch,
		Phase: control.phase, ReturnedTick: prefix.ReturnedTicks, Scanned: result.Scanned, Deleted: result.Deleted,
		LogicalBytes: result.LogicalBytes, RootBytes: result.RootBytes, MemberBytes: result.MemberBytes,
		Completeness: string(result.Completeness), Failed: result.Err != nil}
	known := slices.Contains(control.names, result.Owner)
	if known {
		event.Owner = result.Owner
	}
	valid := (known || result.Owner == "") && result.Scanned >= 0 && result.Deleted >= 0 &&
		result.LogicalBytes >= 0 && result.RootBytes >= 0 && result.MemberBytes >= 0 &&
		(result.Completeness == lifecycle.Exact || result.Completeness == lifecycle.LowerBound || result.Completeness == lifecycle.Unavailable)
	if !result.AttemptedAt.IsZero() && known {
		event.AttemptedAtNano = result.AttemptedAt.UnixNano()
		prefix.OwnerTurns++
		if result.Deleted >= 0 && uint64(result.Deleted) <= math.MaxUint64-prefix.Deleted {
			prefix.Deleted += uint64(result.Deleted)
			prefix.MaxDeleted = max(prefix.MaxDeleted, uint64(result.Deleted))
		} else {
			valid = false
		}
	} else if !result.AttemptedAt.IsZero() || result.Err == nil || result.Scanned != 0 || result.Deleted != 0 ||
		result.LogicalBytes != 0 || result.RootBytes != 0 || result.MemberBytes != 0 {
		valid = false
	}
	event.OwnerTurns, event.TotalDeleted, event.MaxDeleted = prefix.OwnerTurns, prefix.Deleted, prefix.MaxDeleted
	control.mu.Unlock()
	if !valid {
		_ = control.stop()
		return
	}
	raw, err := json.Marshal(event)
	if err != nil || len(raw) > t422LifecycleEventBytes || control.sink(raw) != nil ||
		result.Scanned > lifecycle.MaxCandidatesPerTick || result.Deleted > lifecycle.MaxDeletesPerTick || result.Deleted > result.Scanned {
		_ = control.stop()
	}
}

func (control *t422LifecycleControl) complete(ctx context.Context, path string, resultErr error) error {
	if resultErr != nil || !control.current(ctx, path != t422LifecycleParkPath) {
		return control.stop()
	}
	control.mu.Lock()
	admitted := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	valid := control.err == nil && control.busy && control.expected(path, admitted.Phase)
	if valid {
		control.step++
		control.busy = false
	}
	control.mu.Unlock()
	if !valid {
		return control.stop()
	}
	return nil
}

func (control *t422LifecycleControl) stop() error {
	control.mu.Lock()
	first := control.err == nil
	control.err = errT422LifecycleControl
	control.mu.Unlock()
	if first {
		control.launch.fail(errT422LifecycleControl)
	}
	return errT422LifecycleControl
}

func (control *t422LifecycleControl) operationContext(caller context.Context, path string) (context.Context, func()) {
	maximum := 20 * time.Minute
	if path == t422LifecycleFreshDrive || path == t422LifecycleFreshRead {
		maximum = 4 * time.Hour
	}
	deadline := time.Now().Add(maximum)
	if earlier, ok := control.ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	ctx, cancel := context.WithDeadline(caller, deadline)
	joined := make(chan struct{})
	stop := context.AfterFunc(control.ctx, func() { cancel(); close(joined) })
	if control.ctx.Err() != nil {
		cancel()
	}
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			if !stop() {
				<-joined
			}
		})
	}
}

// This handler is inside ordinary API authentication and outside exact R.
// Native drives remain in the original request owner and never replace its
// context with Background or an unmetered copy of an exact-read context.
func (control *t422LifecycleControl) command(writer http.ResponseWriter, request *http.Request) {
	principal, authenticated := auth.PrincipalFromContext(request.Context())
	fence, err := t422LifecycleRequest(request, true)
	if err != nil || !authenticated || !t421ExactReadLegacyPrincipal(principal) ||
		len(request.Header.Values(t421ExactReadActivationHeader)) != 0 || len(request.Header.Values(t421ExactReadOrdinalHeader)) != 0 {
		_ = control.stop()
		http.Error(writer, "lifecycle control refused", http.StatusConflict)
		return
	}
	path := request.URL.Path
	ctx, cancel := control.operationContext(request.Context(), path)
	defer cancel()
	completed := false
	defer func() {
		if !completed {
			_ = control.stop()
		}
	}()
	if err := control.begin(ctx, path); err != nil {
		http.Error(writer, "lifecycle control refused", http.StatusConflict)
		return
	}
	var cycle lifecycle.CycleObservation
	switch path {
	case t422LifecycleParkPath:
		err = control.runner.Park(ctx)
	case t422LifecycleNormalDrive:
		cycle, err = control.runner.DriveNormal(ctx, control.collector)
	case t422LifecycleRecoveryDrive:
		cycle, err = control.runner.DrivePressure75Recovery(ctx, control.collector, fence)
	case t422LifecycleFreshDrive:
		cycle, err = control.runner.DriveFresh(ctx, control.collector)
	}
	if err != nil || !control.current(ctx, path != t422LifecycleParkPath) {
		http.Error(writer, "lifecycle control refused", http.StatusConflict)
		return
	}
	control.mu.Lock()
	control.cycle = cycle
	control.mu.Unlock()
	const response = "{\"status\":\"complete\"}"
	writer.Header().Set("Content-Type", "application/json")
	written, err := writer.Write([]byte(response))
	if err != nil || written != len(response) {
		return
	}
	completed = control.complete(ctx, path, nil) == nil
}

func (control *t422LifecycleControl) read(request *http.Request) func(context.Context) ([]byte, func(error), error) {
	if _, err := t422LifecycleRequest(request, false); err != nil {
		return nil
	}
	return func(caller context.Context) ([]byte, func(error), error) {
		path := request.URL.Path
		ctx, cancel := control.operationContext(caller, path)
		if err := control.begin(ctx, path); err != nil {
			cancel()
			return nil, nil, err
		}
		finish := func(err error) {
			defer cancel()
			_ = control.complete(ctx, path, err)
		}
		fence, err := t422LifecycleRequest(request, false)
		if err != nil {
			return nil, finish, control.stop()
		}
		var value any
		switch path {
		case t422LifecycleNormalRead, t422LifecycleRecoveryRead, t422LifecycleFreshRead:
			control.mu.Lock()
			value = control.cycle
			control.mu.Unlock()
		case t422LifecycleCollectRead:
			value, err = control.runner.ReadPressure80Collect(ctx, control.collector, fence)
		case t422LifecycleRefuseRead:
			value, err = control.runner.ReadPressure90Refusal(ctx, control.collector, fence)
		case t422LifecycleLatchedRead:
			value, err = control.runner.ReadPressure75Refusal(ctx, control.collector, fence)
		case t422LifecycleResumedRead:
			value, err = control.runner.ReadPressure75Normal(ctx, control.collector)
		}
		if err != nil || !control.current(ctx, true) {
			return nil, finish, control.stop()
		}
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > t422LifecycleReadBytes {
			return nil, finish, control.stop()
		}
		return raw, finish, nil
	}
}

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t422SelectorCleanupSchema = "t422-selector-handoff-cleanup-v1"
	t422SelectorCleanupPath   = "/api/t422/selector-handoff/cleanup"
)

var errT422SelectorCleanup = errors.New("T42.2 selector handoff cleanup refused")

// One scalar selector survives a successful F tail. Cleanup never starts from
// a caller-supplied selector or while ordinary owners can still use its prior.
type t422SelectorCleanupControl struct {
	ctx                                 context.Context
	launch                              *t422SemanticLaunch
	store                               *store.Surreal
	acquireExclusive                    func(context.Context) (func(), error)
	sink                                func([]byte) error
	mu                                  sync.Mutex
	phase                               uint32
	selected                            store.ServiceRuntimeSelector
	captured, armed, busy, done, failed bool
}

type t422SelectorCleanupObservation struct {
	Schema                string `json:"schema"`
	Phase                 uint32 `json:"phase"`
	InputSHA256           string `json:"input_sha256"`
	SelectedRuntimeSHA256 string `json:"selected_runtime_sha256"`
	Turns                 uint64 `json:"turns"`
	Deleted               uint64 `json:"deleted"`
	MaxDeleted            uint64 `json:"max_deleted"`
	StoreReadAttempts     uint64 `json:"store_read_attempts"`
	StoreWriteAttempts    uint64 `json:"store_write_attempts"`
	Done                  bool   `json:"done"`
	Failed                bool   `json:"failed"`
}

func t422SelectorCleanupLimits(phase uint32) (turns, deleted uint64) {
	// The fixed corpus has 10,000 keys. Physical changes replace every row;
	// logical B changes one service and cold creates no predecessor snapshot.
	switch phase {
	case 2:
		return 1, 0
	case 4, 6:
		return (10000 + 1 + 15) / 16, 10000 + 1
	case 5:
		return 1, 2
	default:
		return 0, 0
	}
}

func newT422SelectorCleanupControl(ctx context.Context, launch *t422SemanticLaunch, state *store.Surreal,
	acquireExclusive func(context.Context) (func(), error),
) (*t422SelectorCleanupControl, error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || state == nil || acquireExclusive == nil ||
		launch.request.SelectorHandoffCleanup != t422SelectorCleanupSchema || launch.request.ServerEpoch > 3 ||
		err != nil || !launch.matches(current) {
		return nil, errT422SelectorCleanup
	}
	return &t422SelectorCleanupControl{ctx: ctx, launch: launch, store: state, acquireExclusive: acquireExclusive,
		sink: t4013ExactReportSink("exact selector cleanup turn: ")}, nil
}

func (control *t422SelectorCleanupControl) stop() error {
	control.mu.Lock()
	first := !control.failed
	control.failed = true
	control.mu.Unlock()
	if first {
		control.launch.fail(errT422SelectorCleanup)
	}
	return errT422SelectorCleanup
}

func (control *t422SelectorCleanupControl) current(ctx context.Context, phase uint32) bool {
	if ctx == nil || ctx.Err() != nil || control.ctx.Err() != nil {
		return false
	}
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	current, err := dispatchadmission.ProductionSemanticState()
	return ok && err == nil && admitted.Phase == phase && admitted.OrdinaryOwnersDrained &&
		current.OrdinaryOwnersDrained && control.launch.sameRequest(admitted, current)
}

// Called with the selector already read and last-confirmed by native F. No I/O
// or permit is added here; only the successful report continuation arms it.
func (control *t422SelectorCleanupControl) captureFinal(ctx context.Context, selected store.ServiceRuntimeSelector) error {
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	if !ok {
		return control.stop()
	}
	if turns, _ := t422SelectorCleanupLimits(admitted.Phase); turns == 0 {
		return nil
	}
	if !control.current(ctx, admitted.Phase) || selected.Repository != control.launch.request.Repository {
		return control.stop()
	}
	control.mu.Lock()
	valid := !control.failed && !control.busy && (!control.captured || control.done && control.phase < admitted.Phase)
	if valid {
		control.selected, control.phase = selected, admitted.Phase
		control.captured, control.armed, control.done = true, false, false
	}
	control.mu.Unlock()
	if !valid {
		return control.stop()
	}
	return nil
}

func (control *t422SelectorCleanupControl) finalTail(ctx context.Context, prior func(error)) (func(error), error) {
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	if !ok {
		return prior, control.stop()
	}
	if turns, _ := t422SelectorCleanupLimits(admitted.Phase); turns == 0 {
		return prior, nil
	}
	return func(cause error) {
		if prior != nil {
			prior(cause)
		}
		valid := cause == nil && control.current(ctx, admitted.Phase)
		control.mu.Lock()
		valid = valid && !control.failed && control.captured && !control.armed && !control.done && control.phase == admitted.Phase
		if valid {
			control.armed = true
		}
		control.mu.Unlock()
		if !valid {
			_ = control.stop()
		}
	}, nil
}

func (control *t422SelectorCleanupControl) command(writer http.ResponseWriter, request *http.Request) {
	complete := false
	defer func() {
		if !complete {
			_ = control.stop()
		}
	}()
	principal, authenticated := auth.PrincipalFromContext(request.Context())
	admitted, ok := request.Context().Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	if !ok || !authenticated || !t421ExactReadLegacyPrincipal(principal) || !t422SemanticRequestRoute(request) ||
		request.URL.Path != t422SelectorCleanupPath || !control.current(request.Context(), admitted.Phase) {
		http.Error(writer, "selector cleanup refused", http.StatusConflict)
		return
	}
	control.mu.Lock()
	valid := !control.failed && control.armed && !control.busy && !control.done && control.phase == admitted.Phase
	selected := control.selected
	if valid {
		control.busy = true
	}
	control.mu.Unlock()
	if !valid {
		http.Error(writer, "selector cleanup refused", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(control.ctx, cancel)
	defer func() { stop(); cancel() }()
	value := t422SelectorCleanupObservation{Schema: t422SelectorCleanupSchema, Phase: admitted.Phase,
		InputSHA256: "sha256:" + hex.EncodeToString(admitted.InputSHA256[:]), SelectedRuntimeSHA256: selected.Digest}
	turnLimit, deleteLimit := t422SelectorCleanupLimits(admitted.Phase)
	for value.Turns < turnLimit && !value.Done {
		turnCtx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{
			StoreReadAttempts:  store.ServiceStateV3PreimageHandoffStoreReadMaximum,
			StoreWriteAttempts: store.ServiceStateV3PreimageHandoffStoreWriteMaximum})
		if err != nil {
			return
		}
		value.Turns++
		var result store.ServiceStateV3PreimageDrain
		release, err := control.acquireExclusive(turnCtx)
		if err == nil {
			result, err = control.store.DrainServiceStateV3PreimageHandoff(turnCtx, selected)
			release()
		}
		counts, accountingErr := ledger.Finish()
		value.StoreReadAttempts += counts.StoreReadAttempts
		value.StoreWriteAttempts += counts.StoreWriteAttempts
		if result.Deleted >= 0 {
			value.Deleted += uint64(result.Deleted)
			value.MaxDeleted = max(value.MaxDeleted, uint64(result.Deleted))
		}
		value.Done = result.Done
		value.Failed = err != nil || accountingErr != nil || !control.current(ctx, admitted.Phase) ||
			result.Deleted < 0 || result.Deleted > store.ServiceStateV3PreimageHandoffDeleteLimit || value.Deleted > deleteLimit ||
			!result.Done && result.Deleted == 0
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil || control.sink(append(raw, '\n')) != nil || value.Failed {
			http.Error(writer, "selector cleanup refused", http.StatusConflict)
			return
		}
	}
	if !value.Done || !control.current(ctx, admitted.Phase) {
		http.Error(writer, "selector cleanup refused", http.StatusConflict)
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	raw = append(raw, '\n')
	writer.Header().Set("Content-Type", "application/json")
	n, err := writer.Write(raw)
	if err != nil || n != len(raw) || !control.current(ctx, admitted.Phase) {
		return
	}
	control.mu.Lock()
	complete = !control.failed && control.busy && control.armed && !control.done
	if complete {
		control.done, control.busy = true, false
	}
	control.mu.Unlock()
}

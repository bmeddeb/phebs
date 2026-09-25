package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/search"
)

const (
	t422SearchWarmSchema = "t422-search-warm-v1"
	t422SearchWarmPath   = "/api/t422/search/warm"
	t422SearchWarmPhase  = uint32(14)
)

var errT422SearchWarm = errors.New("T42.2 search warm refused")

// t422SearchWarmer is the searcher's cache warm. It is an interface only so
// the control can be tested without a 19-GB generation.
type t422SearchWarmer interface {
	WarmSelectedWholeRepository(context.Context, string) (search.WholeWarmObservation, error)
}

// One warm per restored launch, before the phase-14 query corridor. It moves
// the cache-owned whole-repository fills a first all-code and a first selected
// service query would start into one bounded parent command; queries stay exact.
type t422SearchWarmControl struct {
	ctx                context.Context
	launch             *t422SemanticLaunch
	searcher           t422SearchWarmer
	isCurrent          func(context.Context) bool // test seam; nil uses current
	mu                 sync.Mutex
	busy, done, failed bool
}

type t422SearchWarmObservation struct {
	Schema                         string `json:"schema"`
	Phase                          uint32 `json:"phase"`
	SharedValidated                bool   `json:"shared_validated"`
	SharedExactSHA256              string `json:"shared_exact_sha256,omitempty"`
	SelectedSearchGenerationSHA256 string `json:"selected_search_generation_sha256"`
	ElapsedMS                      uint64 `json:"elapsed_ms"`
}

func newT422SearchWarmControl(ctx context.Context, launch *t422SemanticLaunch, searcher t422SearchWarmer) (*t422SearchWarmControl, error) {
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || searcher == nil ||
		launch.request.SearchWarm != t422SearchWarmSchema || launch.request.ServerEpoch != 5 {
		return nil, errT422SearchWarm
	}
	return &t422SearchWarmControl{ctx: ctx, launch: launch, searcher: searcher}, nil
}

func (control *t422SearchWarmControl) stop() error {
	control.mu.Lock()
	first := !control.failed
	control.failed = true
	control.mu.Unlock()
	if first {
		control.launch.fail(errT422SearchWarm)
	}
	return errT422SearchWarm
}

func (control *t422SearchWarmControl) current(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil || control.ctx.Err() != nil {
		return false
	}
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	current, err := dispatchadmission.ProductionSemanticState()
	return ok && err == nil && admitted.Phase == t422SearchWarmPhase && admitted.OrdinaryOwnersDrained &&
		current.OrdinaryOwnersDrained && control.launch.sameRequest(admitted, current)
}

func t422SearchWarmRequest(request *http.Request) bool {
	return request != nil && request.URL != nil && request.Method == http.MethodPost &&
		request.URL.Path == t422SearchWarmPath && request.URL.Path == request.URL.EscapedPath() &&
		request.URL.RawQuery == "" && !request.URL.ForceQuery && request.URL.Fragment == "" &&
		request.ContentLength == 0 && len(request.TransferEncoding) == 0 &&
		len(request.Header.Values(t421ExactReadActivationHeader)) == 0 &&
		len(request.Header.Values(t421ExactReadOrdinalHeader)) == 0
}

// command authenticates the parent, then warms. Any refusal or failure is
// terminal for the launch.
func (control *t422SearchWarmControl) command(writer http.ResponseWriter, request *http.Request) {
	principal, authenticated := auth.PrincipalFromContext(request.Context())
	if !t422SearchWarmRequest(request) || !authenticated || !t421ExactReadLegacyPrincipal(principal) {
		_ = control.stop()
		http.Error(writer, "search warm refused", http.StatusConflict)
		return
	}
	control.warm(writer, request.Context())
}

// warm blocks while both cache-owned fills complete or the product's own
// warming timeout expires, then answers with source-free identities.
func (control *t422SearchWarmControl) warm(writer http.ResponseWriter, ctx context.Context) {
	current := control.current
	if control.isCurrent != nil {
		current = control.isCurrent
	}
	control.mu.Lock()
	valid := !control.failed && !control.busy && !control.done
	control.busy = valid
	control.mu.Unlock()
	if !valid || !current(ctx) {
		_ = control.stop()
		http.Error(writer, "search warm refused", http.StatusConflict)
		return
	}
	warmCtx, cancel := context.WithTimeout(ctx, search.WholeGenerationWarmingTimeout)
	defer cancel()
	started := time.Now()
	observed, err := control.searcher.WarmSelectedWholeRepository(warmCtx, control.launch.request.Repository)
	elapsed := time.Since(started)
	if err != nil || observed.Repository != control.launch.request.Repository ||
		observed.SelectedSearchDigest == "" || !current(ctx) {
		log.Printf("T42.2 search warm refused after %s: %v", elapsed.Round(time.Millisecond), err)
		_ = control.stop()
		http.Error(writer, "search warm refused", http.StatusConflict)
		return
	}
	raw, err := json.Marshal(t422SearchWarmObservation{
		Schema: t422SearchWarmSchema, Phase: t422SearchWarmPhase,
		SharedValidated: observed.SharedValidated, SharedExactSHA256: observed.SharedExactDigest,
		SelectedSearchGenerationSHA256: observed.SelectedSearchDigest, ElapsedMS: uint64(elapsed.Milliseconds()),
	})
	if err != nil {
		_ = control.stop()
		http.Error(writer, "search warm refused", http.StatusConflict)
		return
	}
	control.mu.Lock()
	control.busy, control.done = false, true
	control.mu.Unlock()
	log.Printf("T42.2 search warm: %s", raw)
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(append(raw, '\n'))
}

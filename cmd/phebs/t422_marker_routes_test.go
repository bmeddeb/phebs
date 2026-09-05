package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// This fixture exercises source-owned selection only. It installs no live
// bootstrap, constructs no genuine marker control and cannot perform native R.
func t422MarkerRouteLaunch(t *testing.T) (*t422SemanticLaunch, dispatchadmission.ProductionSemanticSnapshot) {
	t.Helper()
	raw, current := t422SemanticTestRequest(t)
	raw = bytes.Replace(raw, []byte(`"server_epoch":1`), []byte(`"server_epoch":3`), 1)
	current.Phase, current.InputSHA256 = 6, sha256.Sum256(raw)
	launch, err := decodeT422SemanticLaunch(raw, current)
	if err != nil {
		t.Fatal(err)
	}
	return launch, current
}

func TestT422MarkerPhaseRouting(t *testing.T) {
	launch, initial := t422MarkerRouteLaunch(t)
	for phase := uint32(0); phase <= 16; phase++ {
		current := initial
		current.Phase = phase
		selected, err := t422MarkerPhaseRoute(launch, current)
		if selected != (phase == 6) || (err == nil) != (phase >= 6 && phase <= 8) {
			t.Fatalf("phase %d route = %t / %v", phase, selected, err)
		}
	}
	for _, mutate := range []func(*dispatchadmission.ProductionSemanticSnapshot){
		func(current *dispatchadmission.ProductionSemanticSnapshot) { current.Mode = "" },
		func(current *dispatchadmission.ProductionSemanticSnapshot) { current.InputSHA256[0] ^= 1 },
		func(current *dispatchadmission.ProductionSemanticSnapshot) { current.ProducerID++ },
	} {
		current := initial
		mutate(&current)
		if selected, err := t422MarkerPhaseRoute(launch, current); selected || err == nil {
			t.Fatal("unbound producer selected a marker route")
		}
	}
	for _, epoch := range []uint64{0, 1, 2, 4, 5, 6} {
		other := *launch
		other.request.ServerEpoch = epoch
		if selected, err := t422MarkerPhaseRoute(&other, initial); selected || err == nil {
			t.Fatal("another epoch selected a marker route")
		}
	}
}

func TestT422MarkerReadRoutesAreClosed(t *testing.T) {
	launch, _ := t422MarkerRouteLaunch(t)
	state := &t421ExactReadAccountingState{semantic: launch, marker: &t422MarkerControl{launch: launch}}
	for _, path := range []string{t422MarkerHitPath, t422MarkerRecoveredPath} {
		if state.markerRead(httptest.NewRequest(http.MethodGet, path, nil)) == nil {
			t.Fatal("fixed native route is absent")
		}
		for _, request := range []*http.Request{
			httptest.NewRequest(http.MethodPost, path, nil),
			httptest.NewRequest(http.MethodGet, path+"?target=caller", nil),
			httptest.NewRequest(http.MethodGet, path+"?", nil),
			httptest.NewRequest(http.MethodGet, path, bytes.NewBufferString("caller target")),
		} {
			if state.markerRead(request) != nil {
				t.Fatal("open native route payload admitted")
			}
		}
	}
	for _, path := range []string{t421ExactFinalAuthorityPath, "/api/t422/return-a-marker/other", "/api/t422/return-a-marker/%68it"} {
		if state.markerRead(httptest.NewRequest(http.MethodGet, path, nil)) != nil {
			t.Fatal("unknown or encoded native operation admitted")
		}
	}
	request := httptest.NewRequest(http.MethodGet, t422MarkerHitPath, nil)
	request.TransferEncoding = []string{"chunked"}
	if state.markerRead(request) != nil || state.markerRead(nil) != nil {
		t.Fatal("chunked or absent native request admitted")
	}
	request.TransferEncoding = nil
	state.semantic = nil
	if state.markerRead(request) != nil {
		t.Fatal("ordinary mode gained a native route")
	}
	other := *launch
	state.semantic = &other
	if state.markerRead(request) != nil {
		t.Fatal("another launch gained this control's routes")
	}
}

// Exercise the same response finalizer used by ServeHTTP with an actual
// bounded ledger and writer. This is transport ordering, not native C5 proof.
func TestT422MarkerCompletionAfterFullReadTail(t *testing.T) {
	for _, mode := range []string{"complete", "short-write", "accounting", "incomplete", "commit", "report", "report-panic", "completion-panic", "native-failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 5, StoreReadAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			count := uint64(5)
			if mode == "accounting" {
				count++
			}
			_ = readaccounting.Charge(ctx, readaccounting.ControlFileRead, count)
			var events []string
			var state *t421ExactReadAccountingState
			capture := &t421ExactReadTestCapture{}
			state = t421NewExactReadAccountingState(func(raw []byte) error {
				events = append(events, "report")
				if !state.active {
					t.Fatal("shared request released before reporting")
				}
				// With one available store attempt this would succeed if the
				// ledger had not already closed. It performs no store I/O.
				if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err == nil {
					t.Fatal("report ran before ledger Finish")
				}
				if mode == "report-panic" {
					panic("private sink failure")
				}
				if mode == "report" {
					return errors.New("private sink failure")
				}
				return capture.report(raw)
			}, func(err error) {
				state.markFailed() // Main's shared native-hook failure latch.
				capture.fail(err)
			})
			state.active, state.nextOrdinal = true, 2
			var writer http.ResponseWriter = &t421ExactReadEventWriter{ResponseRecorder: httptest.NewRecorder(), events: &events}
			if mode == "short-write" {
				writer = &t421ExactReadPartialWriter{header: make(http.Header)}
			}
			body := []byte(`{"native":"bounded"}`)
			written, writeErr := writer.Write(body)
			completed := writeErr == nil && written == len(body)
			var terminalErr error
			if !completed {
				terminalErr = errT421ExactReadResponse
			}
			if mode == "incomplete" {
				completed = false
			}
			var commit func() error
			if mode == "commit" {
				commit = func() error { return errors.New("private commit failure") }
			}
			finished := 0
			var completionErr error
			state.finishRead(writer, ledger, 1, completed, "complete", terminalErr, commit, func(err error) {
				finished++
				completionErr = err
				if !state.active || len(events) == 0 || events[len(events)-1] != "report" {
					t.Fatal("completion did not follow report while the request remained owned")
				}
				events = append(events, "completion")
				if mode == "completion-panic" {
					panic("private native completion failure")
				}
				if mode == "native-failure" {
					state.fail(errT422MarkerControl)
				}
			})
			_, failures := capture.snapshot()
			wantFailure := mode != "complete"
			wantCompletionErr := !slices.Contains([]string{"complete", "completion-panic", "native-failure"}, mode)
			if finished != 1 || state.active || state.nextOrdinal != 2 || state.failed != wantFailure ||
				(len(failures) == 1) != wantFailure || (completionErr != nil) != wantCompletionErr {
				t.Fatalf("completion=%d/%v state=%+v failures=%v", finished, completionErr, state, failures)
			}
		})
	}
}

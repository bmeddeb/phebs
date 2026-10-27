package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func t422PublicationTestControl(t *testing.T) (*t422CheckpointRecoveryControl, store.GenerationChunk) {
	t.Helper()
	base, _ := t422StaleTestControl(t)
	base.launch.request.Repository = "test/repo"
	c := &t422CheckpointRecoveryControl{t422StaleControl: base, input: t422CheckpointRecoveryFixture()}
	h := c.input.Hit
	return c, store.GenerationChunk{Repository: base.launch.request.Repository, Stage: extractionpublication.ScheduleStage, ResourceClass: store.GenerationResourceExtraction, Generation: h.ScheduleGeneration, ScheduleDigest: h.ScheduleDigest, Identity: h.ChunkIdentity, Offset: int64(c.input.Offset), Length: 1}
}
func TestT422CheckpointPublicationDiagnosticParity(t *testing.T) {
	control, _ := t422PublicationTestControl(t)
	if control.publicationHandler(nil) != nil {
		t.Fatal("diagnostic wrapper admitted a missing handler")
	}
	for _, mode := range []string{"selected", "other_chunk", "wrong_attempt", "healthy", "panic_observer"} {
		t.Run(mode, func(t *testing.T) {
			control, chunk := t422PublicationTestControl(t)
			sentinel := errors.New("private /credential/path payload")
			previous := log.Writer()
			defer log.SetOutput(previous)
			var output bytes.Buffer
			log.SetOutput(&output)
			if mode == "other_chunk" {
				chunk.Offset++
			}
			if mode == "wrong_attempt" {
				chunk.Attempt++
			}
			firstFlags := readaccounting.PublicationFailureFlags{ResultsPresent: true, StorePredicatesAvailable: true,
				RunEligible: true, CandidateCurrent: true, RunOwned: true}
			next := func(ctx context.Context, _ store.GenerationChunk, _ generationscheduler.Budget) error {
				if mode == "healthy" {
					return nil
				}
				readaccounting.NotePublicationFailure(ctx, readaccounting.PublicationFailureStoreCount, sentinel, firstFlags)
				readaccounting.NotePublicationFailure(ctx, readaccounting.PublicationFailureAssembly, context.Canceled, readaccounting.PublicationFailureFlags{})
				return sentinel
			}
			if mode == "panic_observer" {
				log.SetOutput(t422ChunkLogWriter(func([]byte) (int, error) { panic("log sink") }))
			}
			accounted, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
			if err != nil {
				t.Fatal(err)
			}
			err = control.publicationHandler(next)(accounted, chunk, generationscheduler.Budget{})
			if mode == "healthy" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err != sentinel {
				t.Fatal("original handler error identity changed")
			}
			counts, finishErr := ledger.Finish()
			if finishErr != nil || counts != (readaccounting.Counts{}) {
				t.Fatal("diagnostic changed accounting")
			}
			selected := mode == "selected" || mode == "panic_observer"
			if selected {
				f := control.publicationFailure
				if f == nil || f.Stage != readaccounting.PublicationFailureStoreCount || f.Cause != "other" || f.Flags != firstFlags || !f.logged {
					t.Fatal("missing first selected publication diagnostic", f)
				}
			} else if control.publicationFailure != nil || output.Len() != 0 {
				t.Fatal("unselected/healthy path gained diagnostics")
			}
			if mode == "selected" && (strings.Contains(output.String(), "private /credential") || strings.Count(output.String(), "first failure:") != 1) {
				t.Fatal("unsafe or duplicate diagnostic output")
			}
			if mode == "selected" && !strings.Contains(output.String(), "store_predicates_available=true run_eligible=true candidate_current=true counts_exact=false run_owned=true marked_single=false") {
				t.Fatal("selected diagnostic omitted closed predicate labels")
			}
			if control.err != nil || control.failure != nil || control.recovered.reported || control.finalSeen {
				t.Fatal("diagnostic granted authority or changed failure state")
			}
		})
	}
}
func TestT422CheckpointPublicationBlockedLogDoesNotHoldControlOrLaterCaller(t *testing.T) {
	control, chunk := t422PublicationTestControl(t)
	previous := log.Writer()
	defer log.SetOutput(previous)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var handlerLock sync.Mutex
	var invocations atomic.Int32
	var returned atomic.Bool
	log.SetOutput(t422ChunkLogWriter(func(raw []byte) (int, error) {
		if !returned.Load() || !handlerLock.TryLock() {
			t.Error("diagnostic logged before original handler defers")
		} else {
			handlerLock.Unlock()
		}
		if !control.mu.TryLock() {
			t.Error("diagnostic held selected control lock")
		} else {
			control.mu.Unlock()
		}
		close(entered)
		<-release
		return len(raw), nil
	}))
	sentinel := errors.New("private native error")
	wrapped := control.publicationHandler(func(ctx context.Context, _ store.GenerationChunk, _ generationscheduler.Budget) error {
		handlerLock.Lock()
		defer handlerLock.Unlock()
		if invocations.Add(1) == 1 {
			readaccounting.NotePublicationFailure(ctx, readaccounting.PublicationFailureStoreQuery, sentinel, readaccounting.PublicationFailureFlags{})
		} else {
			readaccounting.NotePublicationFailure(ctx, readaccounting.PublicationFailureStoreIdentity, context.Canceled, readaccounting.PublicationFailureFlags{ResultsPresent: true, SingleResult: true})
		}
		returned.Store(true)
		return sentinel
	})
	first := make(chan error, 1)
	go func() { first <- wrapped(t.Context(), chunk, generationscheduler.Budget{}) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first advisory log absent")
	}
	second := make(chan error, 1)
	go func() { second <- wrapped(t.Context(), chunk, generationscheduler.Budget{}) }()
	select {
	case err := <-second:
		if err != sentinel {
			t.Fatal("later error changed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("later caller blocked on first log")
	}
	control.mu.Lock()
	firstStage, firstCause, firstFlags := control.publicationFailure.Stage, control.publicationFailure.Cause, control.publicationFailure.Flags
	control.mu.Unlock()
	if firstStage != readaccounting.PublicationFailureStoreQuery || firstCause != "other" || firstFlags != (readaccounting.PublicationFailureFlags{}) {
		t.Fatal("later concurrent failure replaced first record")
	}
	unblock()
	if err := <-first; err != sentinel {
		t.Fatal("first error changed")
	}
}

func TestT422CheckpointPublicationCauseLabelsAreClosed(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{store.ErrConflict, "store_conflict"},
		{store.WithDeferral(errors.New("private deferral")), "store_deferral"},
		{store.WithTerminal(errors.New("private terminal")), "store_terminal"},
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline"},
		{extractionpublication.ErrInvalid, "runtime_invalid"}, {extractionpublication.ErrStale, "runtime_stale"},
		{store.ErrGenerationLeaseLost, "store_lease"}, {errors.New("private payload"), "other"},
	} {
		if got := t422PublicationCause(test.err); got != test.want {
			t.Fatal("closed cause differs", got, test.want)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestT422ChunkReportsBindingIsOptional(t *testing.T) {
	ordinary := &generationscheduler.Scheduler{}
	bindT422ExactChunkReports(false, nil, ordinary)
	if ordinary.ChunkReports != nil || ordinary.ChunkReportFailure != nil {
		t.Fatal("ordinary or historical T40 mode acquired T42 chunk reporting")
	}
	// Disabled binding must not replace an existing advisory sink.
	called := false
	ordinary.ChunkReports = func([]byte) error { called = true; return nil }
	bindT422ExactChunkReports(false, nil, ordinary)
	if err := ordinary.ChunkReports(nil); err != nil || !called {
		t.Fatal("disabled binding changed advisory reporting")
	}
	for _, missing := range []string{"failure latch", "scheduler"} {
		t.Run(missing, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("incomplete exact binding was accepted")
				}
			}()
			if missing == "failure latch" {
				bindT422ExactChunkReports(true, nil, ordinary)
			} else {
				bindT422ExactChunkReports(true, func(error) {}, nil)
			}
		})
	}
}

// This drives the real Scheduler.Run and production log sink, not a direct
// call to the failure callback. No database, server or ceremony is launched.
func TestT422ChunkReportFailureStopsAndJoinsScheduler(t *testing.T) {
	for _, resource := range []store.GenerationResourceClass{
		store.GenerationResourceIO, store.GenerationResourceCPU,
		store.GenerationResourceMemory, store.GenerationResourceExtraction,
	} {
		for _, event := range []string{"started", "settled"} {
			t.Run(string(resource)+"/"+event, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				failed := make(chan error, 1)
				fail := func(err error) {
					select {
					case failed <- err:
						cancel()
					default:
					}
				}
				state := &t422ChunkReportStore{}
				var handled atomic.Int32
				scheduler := &generationscheduler.Scheduler{
					Store: state,
					Classes: map[store.GenerationResourceClass]generationscheduler.Class{
						resource: {
							Concurrency: 1,
							Budget:      generationscheduler.Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
							Handle: func(context.Context, store.GenerationChunk, generationscheduler.Budget) error {
								handled.Add(1)
								return nil
							},
						},
					},
				}
				bindT422ExactChunkReports(true, fail, scheduler)
				previous := log.Writer()
				t.Cleanup(func() { log.SetOutput(previous) })
				var reports []generationscheduler.ChunkLifecycleReport
				log.SetOutput(t422ChunkLogWriter(func(raw []byte) (int, error) {
					const prefix = "generation chunk lifecycle: "
					_, body, ok := strings.Cut(string(raw), prefix)
					if !ok {
						return len(raw), nil // unrelated bounded diagnostics
					}
					var report generationscheduler.ChunkLifecycleReport
					if err := json.Unmarshal([]byte(body), &report); err != nil {
						t.Errorf("invalid report: %v", err)
						return 0, err
					}
					reports = append(reports, report)
					if report.Event == event {
						return 0, errors.New("private sink cause must not escape")
					}
					return len(raw), nil
				}))
				if err := scheduler.Run(ctx); err != nil {
					t.Fatal(err)
				}
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatalf("scheduler stopped without synchronous failure cancellation: %v", ctx.Err())
				}
				wantWork, wantReports := int32(0), 1
				if event == "settled" {
					wantWork, wantReports = 1, 2
				}
				if handled.Load() != wantWork || state.completed.Load() != wantWork || len(reports) != wantReports {
					t.Fatalf("handled/completed/reports = %d/%d/%d; want %d/%d/%d",
						handled.Load(), state.completed.Load(), len(reports), wantWork, wantWork, wantReports)
				}
				for _, report := range reports {
					if report.Schema != generationscheduler.ChunkLifecycleSchema || report.Identity != "chunk" || report.Generation != "generation" {
						t.Fatalf("production sink changed report: %+v", report)
					}
				}
				terminal := serverTerminalError(http.ErrServerClosed, nil, nil, failed)
				if terminal == nil || strings.Contains(terminal.Error(), "private sink cause") {
					t.Fatalf("missing or source-bearing terminal failure: %v", terminal)
				}
			})
		}
	}
}

type t422ChunkLogWriter func([]byte) (int, error)

func (write t422ChunkLogWriter) Write(raw []byte) (int, error) { return write(raw) }

type t422ChunkReportStore struct {
	store.GenerationSchedulerStore
	claimed   atomic.Bool
	completed atomic.Int32
}

func (*t422ChunkReportStore) ExpandNextGenerationSchedule(context.Context, store.GenerationResourceClass) (*store.GenerationSchedule, error) {
	return nil, store.ErrNotFound
}

func (*t422ChunkReportStore) ReapStaleGenerationChunks(context.Context, store.GenerationResourceClass, time.Duration) (int, error) {
	return 0, nil
}

func (state *t422ChunkReportStore) ClaimGenerationChunk(context.Context, store.GenerationResourceClass, string) (*store.GenerationChunk, error) {
	if !state.claimed.CompareAndSwap(false, true) {
		return nil, store.ErrNotFound
	}
	return &store.GenerationChunk{Identity: "chunk", Generation: "generation", Stage: "stage", Attempt: 1}, nil
}

func (state *t422ChunkReportStore) CompleteGenerationChunk(context.Context, store.GenerationChunk) error {
	state.completed.Add(1)
	return nil
}

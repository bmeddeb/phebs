package extractionpublication

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestRuntimePublicReadAccounting(t *testing.T) {
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
	runtime, repository := fixture.reconciler.Runtime, fixture.request.Authority.Repository
	before := fixture.snapshot(t)
	domains, partitions := uint64(len(fixture.generation.Domains)), uint64(fixture.generation.WorkItems)
	// Native file reads are observed here. This fixture's schedule double does
	// not observe SDK attempts; the real-store integration separately checks
	// Progress's two query attempts. No API/HTTP or caller-owned inspector
	// context propagation is established by these direct Runtime calls.
	for _, test := range []struct {
		name          string
		read          func(context.Context) (any, error)
		files         uint64
		scheduleCalls int64
	}{
		{
			name: "current",
			read: func(ctx context.Context) (any, error) {
				return runtime.Current(ctx, repository, fixture.domain.Plan.Domain)
			},
			// Pointer, generation, selected domain plan, selected root.
			files: 4,
		},
		{
			name: "status",
			read: func(ctx context.Context) (any, error) {
				return runtime.Status(ctx, repository, fixture.request.GenerationDigest)
			},
			// Generation + each domain's plan/root/Current(4) + each result.
			files: 1 + 6*domains + partitions,
		},
		{
			name: "progress",
			read: func(ctx context.Context) (any, error) { return runtime.Progress(ctx, repository) },
			// Schedule binding, generation, and each current pointer.
			files: 2 + domains, scheduleCalls: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			want, err := test.read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, refuse := range []bool{false, true} {
				name, limit := "complete", test.files
				if refuse {
					name, limit = "last_control_refuses", limit-1
				}
				t.Run(name, func(t *testing.T) {
					fixture.schedules.reads.Store(0)
					ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: limit})
					if err != nil {
						t.Fatal(err)
					}
					got, readErr := test.read(ctx)
					counts, accountingErr := ledger.Finish()
					if counts != (readaccounting.Counts{ControlFileReads: test.files}) {
						t.Fatalf("control events=%+v, want %d", counts, test.files)
					}
					wantCalls := test.scheduleCalls
					if refuse {
						if !errors.Is(readErr, readaccounting.ErrLimit) || !errors.Is(accountingErr, readaccounting.ErrLimit) {
							t.Fatalf("last control refusal became success: read=%v accounting=%v", readErr, accountingErr)
						}
						if wantCalls != 0 {
							wantCalls-- // No confirmation read after the refused pointer.
						}
					} else if readErr != nil || accountingErr != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("scoped inspection changed the result: got=%+v want=%+v read=%v accounting=%v", got, want, readErr, accountingErr)
					}
					if calls := fixture.schedules.reads.Load(); calls != wantCalls {
						t.Fatalf("schedule method calls=%d, want %d (not SDK events)", calls, wantCalls)
					}
					if !reflect.DeepEqual(before, fixture.snapshot(t)) {
						t.Fatal("public inspection changed controls, schedules, evidence, or source work")
					}
				})
			}
		})
	}
}

func TestRuntimeStatusAccountingPreservesInvalidCurrentDisposition(t *testing.T) {
	for _, missing := range []bool{true, false} {
		name := "corrupt_pointer"
		if missing {
			name = "missing_pointer"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
			runtime, repository := fixture.reconciler.Runtime, fixture.request.Authority.Repository
			pointerPath := runtime.currentPath(repository, fixture.domain.Plan.Domain)
			if missing {
				if err := os.Remove(pointerPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(pointerPath, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := fixture.snapshot(t)
			want, err := runtime.Status(t.Context(), repository, fixture.request.GenerationDigest)
			if err != nil || len(want.Domains) != 1 || want.Domains[0].Current {
				t.Fatalf("unscoped invalid-current status=%+v, %v", want, err)
			}
			// Generation + plan/result/root + one unsuccessful current pointer.
			wantFiles := uint64(1 + 3*len(fixture.generation.Domains) + fixture.generation.WorkItems)
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: wantFiles})
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := runtime.Status(ctx, repository, fixture.request.GenerationDigest)
			counts, accountingErr := ledger.Finish()
			if readErr != nil || accountingErr != nil || !reflect.DeepEqual(got, want) ||
				counts != (readaccounting.Counts{ControlFileReads: wantFiles}) {
				t.Fatalf("invalid-current inspection=%+v, %v events=%+v accounting=%v", got, readErr, counts, accountingErr)
			}
			if !reflect.DeepEqual(before, fixture.snapshot(t)) {
				t.Fatal("invalid-current inspection mutated the fixture")
			}
		})
	}
}

func TestReadBoundedContextCountsControlAttempts(t *testing.T) {
	for _, test := range []struct {
		name    string
		present bool
		limit   int64
		wantErr bool
	}{
		{name: "regular", present: true, limit: 2},
		{name: "missing", limit: 2, wantErr: true},
		{name: "oversized", present: true, limit: 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.json")
			if test.present {
				if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 1})
			if err != nil {
				t.Fatal(err)
			}
			raw, readErr := readBoundedContext(ctx, path, test.limit)
			counts, accountingErr := ledger.Finish()
			if (readErr != nil) != test.wantErr || !test.wantErr && !bytes.Equal(raw, []byte("{}")) {
				t.Fatalf("control read = %q, %v", raw, readErr)
			}
			if accountingErr != nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
				t.Fatalf("control attempt events = %+v, %v", counts, accountingErr)
			}
		})
	}
}

func TestReadBoundedContextLimitPrecedesFilesystemAccess(t *testing.T) {
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := readBoundedContext(ctx, filepath.Join(t.TempDir(), "missing.json"), 2)
	counts, accountingErr := ledger.Finish()
	if !errors.Is(readErr, readaccounting.ErrLimit) || errors.Is(readErr, os.ErrNotExist) ||
		!errors.Is(accountingErr, readaccounting.ErrLimit) || counts != (readaccounting.Counts{ControlFileReads: 1}) {
		t.Fatalf("limit did not refuse before the missing-file read: read=%v accounting=%v", readErr, accountingErr)
	}
}

func TestOpenGenerationContextMetadataRefusalDoesNotChargeControlRead(t *testing.T) {
	runtime := &Runtime{Root: t.TempDir()}
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	_, openErr := runtime.openGenerationContext(ctx, filepath.Join(runtime.Root, "absent"), "example.invalid/read-accounting", digest("target", nil))
	counts, accountingErr := ledger.Finish()
	if !errors.Is(openErr, os.ErrNotExist) || accountingErr != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("directory-only refusal = %v events=%+v accounting=%v", openErr, counts, accountingErr)
	}
}

func TestRecoveryPreparationControlLimitRefusesBeforeMutation(t *testing.T) {
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
	before := fixture.snapshot(t)
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, preparationErr := fixture.reconciler.PrepareRecovery(ctx, fixture.request)
	counts, accountingErr := ledger.Finish()
	if prepared != nil || !errors.Is(preparationErr, readaccounting.ErrLimit) ||
		!errors.Is(accountingErr, readaccounting.ErrLimit) || counts != (readaccounting.Counts{ControlFileReads: 1}) {
		t.Fatalf("preparation limit = %+v, %v events=%+v accounting=%v", prepared, preparationErr, counts, accountingErr)
	}
	if !reflect.DeepEqual(before, fixture.snapshot(t)) {
		t.Fatal("control-limit refusal changed native controls or evidence")
	}
}

func TestRecoveryPreparationLateControlLimitPreservesCommittedSuccessor(t *testing.T) {
	for _, mode := range []string{RecoveryPreparationScheduleOnly, RecoveryPreparationCheckpoint} {
		t.Run(mode, func(t *testing.T) {
			// This unit fixture supplies actual native controls and a scheduler
			// double. The separate real-store test owns query/write-attempt proof.
			fixture := newRecoveryPreparationFixture(t, mode)
			if len(fixture.generation.Domains) != 1 || fixture.generation.WorkItems != 1 {
				t.Fatal("late-read boundary needs exactly one domain and partition")
			}
			before := fixture.snapshot(t)
			wantFiles := uint64(13)
			if mode == RecoveryPreparationCheckpoint {
				wantFiles++
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: wantFiles - 1})
			if err != nil {
				t.Fatal(err)
			}
			// Refuse the final successor-binding read, after native enqueue.
			// The owner makes no second preparation call and performs no rollback.
			prepared, preparationErr := fixture.reconciler.PrepareRecovery(ctx, fixture.request)
			counts, accountingErr := ledger.Finish()
			if prepared != nil || !errors.Is(preparationErr, readaccounting.ErrLimit) ||
				!errors.Is(accountingErr, readaccounting.ErrLimit) || counts != (readaccounting.Counts{ControlFileReads: wantFiles}) {
				t.Fatalf("late refusal = %+v, %v events=%+v accounting=%v", prepared, preparationErr, counts, accountingErr)
			}
			after := fixture.snapshot(t)
			if !reflect.DeepEqual(before.publications, after.publications) || before.acquired != after.acquired ||
				before.released != after.released || before.executions != after.executions || before.published != after.published ||
				before.begins != after.begins || before.aborts != after.aborts || after.enqueues != 1 {
				t.Fatal("late accounting refusal changed evidence, repeated source work, or retried enqueue")
			}
			current := after.schedules[scheduleKey(fixture.request.Authority.Repository, ScheduleStage)]
			wantGeneration := recoveryGeneration(fixture.request.GenerationDigest, fixture.request.PriorScheduleDigest)
			if store.ValidateGenerationSchedule(current) != nil || current.Generation != wantGeneration ||
				current.Digest == fixture.request.PriorScheduleDigest || current.Status != store.GenerationScheduleActive {
				t.Fatalf("late accounting refusal discarded the committed successor: %+v", current)
			}
			binding, err := fixture.reconciler.Runtime.readBinding(fixture.request.Authority.Repository, wantGeneration)
			if err != nil || binding.TargetGeneration != fixture.request.GenerationDigest || binding.PriorSchedule != fixture.request.PriorScheduleDigest {
				t.Fatalf("committed successor binding changed: %+v, %v", binding, err)
			}
			rootPath := filepath.Join(fixture.resultDirectory(), rootName())
			completionPath := filepath.Join(fixture.resultDirectory(), completionName())
			pointerPath := fixture.reconciler.Runtime.currentPath(fixture.request.Authority.Repository, fixture.domain.Plan.Domain)
			for path, raw := range before.files {
				if mode == RecoveryPreparationCheckpoint && (path == rootPath || path == completionPath || path == pointerPath) {
					continue
				}
				if !bytes.Equal(after.files[path], raw) || after.modes[path] != before.modes[path] {
					t.Fatalf("late refusal changed preserved control: %s", filepath.Base(path))
				}
			}
			if mode == RecoveryPreparationCheckpoint {
				if _, exists := after.files[rootPath]; exists {
					t.Fatal("late refusal rolled back checkpoint root removal")
				}
				if _, exists := after.files[pointerPath]; exists {
					t.Fatal("late refusal rolled back checkpoint pointer removal")
				}
				completion, err := readCompletionControl(completionPath, fixture.domain.Plan)
				if err != nil || completion.Count != completion.Expected-1 || completion.Bits[0]&1 != 0 {
					t.Fatalf("late refusal lost the committed checkpoint bitmap: %+v, %v", completion, err)
				}
			}
		})
	}
}

func TestWriteBindingContextCountsOnlyExistingControlRead(t *testing.T) {
	const repository = "example.invalid/read-accounting"
	for _, existing := range []bool{false, true} {
		name := "new_binding"
		if existing {
			name = "existing_binding"
		}
		t.Run(name, func(t *testing.T) {
			runtime := &Runtime{Root: t.TempDir()}
			if err := os.MkdirAll(runtime.repositoryDirectory(repository), 0o700); err != nil {
				t.Fatal(err)
			}
			target, prior := digest("target", nil), digest("prior", nil)
			binding := scheduleBinding{
				Schema: BindingSchema, Repository: repository,
				ScheduleGeneration: recoveryGeneration(target, prior), TargetGeneration: target, PriorSchedule: prior,
			}
			if existing {
				// The production binding writer installs this exact immutable
				// control. This is not a retry of a terminal preparation.
				if err := runtime.writeBinding(binding); err != nil {
					t.Fatal(err)
				}
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 1})
			if err != nil {
				t.Fatal(err)
			}
			writeErr := runtime.writeBindingContext(ctx, binding)
			counts, accountingErr := ledger.Finish()
			want := readaccounting.Counts{}
			if existing {
				want.ControlFileReads = 1
			}
			if writeErr != nil || accountingErr != nil || counts != want {
				t.Fatalf("binding write=%v events=%+v want=%+v accounting=%v", writeErr, counts, want, accountingErr)
			}
			got, err := runtime.readBinding(repository, binding.ScheduleGeneration)
			if err != nil || got != binding {
				t.Fatalf("binding changed: %+v, %v", got, err)
			}
		})
	}
}

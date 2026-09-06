package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func newReadAccountingStore(t *testing.T) *Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	state, err := OpenLocalMemory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := state.Close(ctx); err != nil {
			t.Errorf("close read accounting store: %v", err)
		}
	})
	return state
}

func TestPreparationStoreReadAccountingCountsSuccessfulAndAbsentQueries(t *testing.T) {
	state := newReadAccountingStore(t)
	const repository = "example.invalid/read-accounting"
	if err := state.UpsertRepo(t.Context(), Repo{Name: repository, CloneURL: "https://" + repository}); err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{StoreReadAttempts: 6})
	if err != nil {
		t.Fatal(err)
	}
	if repo, err := state.GetRepo(ctx, repository); err != nil || repo.Name != repository {
		t.Fatalf("stored repository: %+v, %v", repo, err)
	}
	if repositories, err := state.ListRepos(ctx); err != nil || len(repositories) != 1 {
		t.Fatalf("repository inventory: %+v, %v", repositories, err)
	}
	for _, test := range []struct {
		name string
		read func() error
	}{
		{name: "absent_repository", read: func() error { _, err := state.GetRepo(ctx, "example.invalid/absent"); return err }},
		{name: "absent_candidate", read: func() error { _, err := state.GetCandidateManifestPublication(ctx, repository); return err }},
		{name: "absent_domain", read: func() error {
			_, err := state.GetPartitionedExtractionDomain(ctx, repository, "proto-contract")
			return err
		}},
		{name: "absent_schedule", read: func() error { _, err := state.GetGenerationSchedule(ctx, repository, "extraction"); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.read(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("read refusal = %v, want not found", err)
			}
		})
	}
	if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 6}) {
		t.Fatalf("actual read queries = %+v, %v", counts, err)
	}
	// The observer neither creates nor changes ordinary successful/absent rows.
	if _, err := state.GetRepo(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
}

func TestPreparationStoreAccountingRefusesBeforeSDKCall(t *testing.T) {
	const repository = "example.invalid/read-accounting"
	// A nil SDK handle proves denied charges return before calling it. These
	// are valid method inputs; ordinary visibility remains a real-store test.
	state := &Surreal{}
	for _, test := range []struct {
		name string
		call func(context.Context) error
	}{
		{name: "repository", call: func(ctx context.Context) error { _, err := state.GetRepo(ctx, repository); return err }},
		{name: "repository_inventory", call: func(ctx context.Context) error { _, err := state.ListRepos(ctx); return err }},
		{name: "candidate", call: func(ctx context.Context) error {
			_, err := state.GetCandidateManifestPublication(ctx, repository)
			return err
		}},
		{name: "domain", call: func(ctx context.Context) error {
			_, err := state.GetPartitionedExtractionDomain(ctx, repository, "proto-contract")
			return err
		}},
		{name: "schedule", call: func(ctx context.Context) error {
			_, err := state.GetGenerationSchedule(ctx, repository, "extraction")
			return err
		}},
		{name: "enqueue", call: func(ctx context.Context) error {
			_, err := state.EnqueueGenerationSchedule(ctx, generationSpec(repository, "sha256:"+strings.Repeat("a", 64)))
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(ctx); err == nil {
				t.Fatal("zero query-attempt budget was accepted")
			}
			want := readaccounting.Counts{StoreReadAttempts: 1}
			if test.name == "enqueue" {
				want = readaccounting.Counts{StoreWriteAttempts: 1}
			}
			// This is the ledger's refusal sentinel, not a performed SDK call;
			// Finish must reject it as a complete successful observation.
			if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
				t.Fatalf("denied attempt did not retain its refusal sentinel: %+v, %v", counts, err)
			}
		})
	}
}

func TestPreparationStoreReadAccountingIncludesEveryRetry(t *testing.T) {
	state := newReadAccountingStore(t)
	for _, test := range []struct {
		name, statement string
		limit, want     uint64
		queryError      bool
		ledgerError     bool
	}{
		{name: "success", statement: "RETURN [];", limit: 64, want: 1},
		{name: "transient_exhaustion", statement: "THROW 'injected conflict';", limit: 64, want: 64, queryError: true},
		{name: "permanent_conflict", statement: "THROW 'phebs-permanent: injected conflict';", limit: 64, want: 1, queryError: true},
		{name: "unique_is_not_read_retry", statement: "THROW 'injected unique constraint';", limit: 64, want: 1, queryError: true},
		{name: "budget_before_next_retry", statement: "THROW 'injected conflict';", limit: 3, want: 4, queryError: true, ledgerError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{StoreReadAttempts: test.limit})
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the existing native retry seam against real SurrealDB,
			// not a store double that skips error or visibility semantics.
			_, queryErr := queryGenerationSchedule[any](ctx, state.accounting, state.db, "get_schedule", test.statement, nil, storeRead())
			counts, finishErr := ledger.Finish()
			if (queryErr != nil) != test.queryError || (finishErr != nil) != test.ledgerError ||
				counts != (readaccounting.Counts{StoreReadAttempts: test.want}) {
				t.Fatalf("attempts=%+v query=%v ledger=%v", counts, queryErr, finishErr)
			}
		})
	}
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queryGenerationSchedule[any](ctx, state.accounting, state.db, "complete", "RETURN [];", nil, storeRead()); err != nil {
		t.Fatalf("unrelated scheduler operation was classified as a preparation read: %v", err)
	}
	if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("unrelated scheduler accounting = %+v, %v", counts, err)
	}
}

func TestPreparationStoreWriteAccountingIncludesEveryEnqueueAttempt(t *testing.T) {
	state := newReadAccountingStore(t)
	for index, test := range []struct {
		name, message string
		limit, want   uint64
		ledgerError   bool
	}{
		{name: "success", limit: 64, want: 1},
		{name: "transient_exhaustion", message: "injected conflict", limit: 64, want: 64},
		{name: "unique_enqueue_retry", message: "injected unique constraint", limit: 64, want: 64},
		{name: "permanent_conflict", message: "phebs-permanent: injected conflict", limit: 64, want: 1},
		{name: "budget_before_next_retry", message: "injected conflict", limit: 2, want: 3, ledgerError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := fmt.Sprintf("example.invalid/read-accounting-%d", index)
			spec := generationSpec(repository, fmt.Sprintf("sha256:%064x", index+1))
			if test.message != "" {
				// This temporary event aborts the real enqueue transaction. Its
				// rolled-back rows cannot fabricate successful scheduler state.
				requireCandidateRawQuery(t, t.Context(), state, fmt.Sprintf(`
DEFINE EVENT read_accounting_enqueue_failure ON TABLE generation_schedule
WHEN $event != 'DELETE' AND $after.repository = '%s'
THEN { THROW '%s' };`, repository, test.message), nil)
				t.Cleanup(func() {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					requireCandidateRawQuery(t, cleanupCtx, state,
						"REMOVE EVENT read_accounting_enqueue_failure ON TABLE generation_schedule;", nil)
				})
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
				StoreReadAttempts: test.limit, StoreWriteAttempts: test.limit,
			})
			if err != nil {
				t.Fatal(err)
			}
			schedule, enqueueErr := state.EnqueueGenerationSchedule(ctx, spec)
			counts, finishErr := ledger.Finish()
			if (enqueueErr != nil) != (test.message != "") || (finishErr != nil) != test.ledgerError ||
				counts != (readaccounting.Counts{StoreReadAttempts: min(test.want, test.limit), StoreWriteAttempts: test.want}) {
				t.Fatalf("attempts=%+v enqueue=%v ledger=%v", counts, enqueueErr, finishErr)
			}
			stored, readErr := state.GetGenerationSchedule(t.Context(), repository, spec.Stage)
			if test.message == "" {
				if readErr != nil || schedule == nil || stored.Digest != schedule.Digest {
					t.Fatalf("successful enqueue not retained: %+v, %v", stored, readErr)
				}
			} else if !errors.Is(readErr, ErrNotFound) {
				t.Fatalf("aborted transaction retained a schedule: %+v, %v", stored, readErr)
			}
		})
	}
}

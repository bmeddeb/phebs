package candidatejob

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestProviderReadAccountingNativeStore(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	dataDir, repository, commit := candidateGitFixture(t)
	state, err := store.OpenLocalMemory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := state.Close(ctx); err != nil {
			t.Errorf("close candidate provider store: %v", err)
		}
	})
	if err := state.UpsertRepo(t.Context(), store.Repo{Name: repository, CloneURL: "https://" + repository}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRepoIndexed(t.Context(), repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	worker, provider, err := New(dataDir, state, []extract.Extractor{protodecl.New(), gocaller.NewGRPC()})
	if err != nil {
		t.Fatal(err)
	}
	// Native Git planning and guarded publication are setup, not observed read
	// work. There is no SDK/query mock or pre-open of the provider's cache.
	if err := worker.Handle(t.Context(), store.Job{Kind: store.JobCandidate, Target: repository}); err != nil {
		t.Fatal(err)
	}
	newProvider := func(t *testing.T) *Provider {
		t.Helper()
		provider, err := NewProvider(dataDir, state, worker.policies)
		if err != nil {
			t.Fatal(err)
		}
		return provider
	}
	// This two-record native manifest is below the 512-record projection run
	// bound: strict validation decodes each artifact record, then its projected
	// record once. The noncandidate notes.txt contributes neither visit.
	cold := readaccounting.Counts{ControlFileReads: 1, StoreReadAttempts: 4, MemberVisits: 4}
	warm := readaccounting.Counts{StoreReadAttempts: 4}
	reference := readaccounting.Counts{StoreReadAttempts: 2}
	ctx, ledger := providerReadScope(t, cold)
	publication, err := provider.OpenCurrentPublication(ctx, repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertProviderReadCounts(t, ledger, cold, nil)
	diagnostics := publication.Diagnostics()
	if diagnostics.Repository.Records != 1 || diagnostics.Caller.Records != 1 || diagnostics.Local.Records != 0 || publication.State().Commit != commit {
		t.Fatalf("unexpected native publication: %+v, state=%+v", diagnostics, publication.State())
	}

	for range 2 {
		ctx, ledger := providerReadScope(t, warm)
		opened, err := provider.OpenCurrentPublication(ctx, repository, nil)
		if err != nil || opened != publication {
			t.Fatalf("warm cache = %p, %v; want %p", opened, err, publication)
		}
		assertProviderReadCounts(t, ledger, warm, nil)
		ctx, ledger = providerReadScope(t, reference)
		current, err := provider.CurrentPublicationState(ctx, repository, nil)
		if err != nil || current != publication.State() {
			t.Fatalf("compact reference = %+v, %v", current, err)
		}
		assertProviderReadCounts(t, ledger, reference, nil)
	}
	// Reusing a publication does not retain its already-closed opening scope.
	view, err := publication.Domain(protodecl.New().Domain(), protodecl.New().Version())
	if err != nil {
		t.Fatal(err)
	}
	ctx, ledger = providerReadScope(t, readaccounting.Counts{MemberVisits: 1})
	if err := view.ForEachRepositoryRecord(ctx, func(candidate.Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
	assertProviderReadCounts(t, ledger, readaccounting.Counts{MemberVisits: 1}, nil)

	for _, test := range []struct {
		name      string
		prime     bool
		reference bool
		limits    readaccounting.Counts
		want      readaccounting.Counts
		retry     readaccounting.Counts
	}{
		{name: "cold_first_pointer", want: readaccounting.Counts{StoreReadAttempts: 1}, retry: cold},
		{name: "cold_manifest", limits: readaccounting.Counts{StoreReadAttempts: 2}, want: readaccounting.Counts{StoreReadAttempts: 2, ControlFileReads: 1}, retry: cold},
		{name: "cold_member", limits: readaccounting.Counts{StoreReadAttempts: 2, ControlFileReads: 1}, want: readaccounting.Counts{StoreReadAttempts: 2, ControlFileReads: 1, MemberVisits: 1}, retry: cold},
		{name: "cold_post_open_pointer", limits: readaccounting.Counts{StoreReadAttempts: 2, ControlFileReads: 1, MemberVisits: 4}, want: readaccounting.Counts{StoreReadAttempts: 3, ControlFileReads: 1, MemberVisits: 4}, retry: cold},
		{name: "warm_first_pointer", prime: true, want: readaccounting.Counts{StoreReadAttempts: 1}, retry: warm},
		{name: "warm_final_pointer", prime: true, limits: readaccounting.Counts{StoreReadAttempts: 3}, want: warm, retry: cold},
		{name: "compact_final_pointer", prime: true, reference: true, limits: readaccounting.Counts{StoreReadAttempts: 1}, want: reference, retry: warm},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := newProvider(t)
			if test.prime {
				if _, err := selected.OpenCurrentPublication(t.Context(), repository, nil); err != nil {
					t.Fatal(err)
				}
			}
			ctx, ledger := providerReadScope(t, test.limits)
			if test.reference {
				current, err := selected.CurrentPublicationState(ctx, repository, nil)
				if !errors.Is(err, readaccounting.ErrLimit) || current != (candidate.State{}) {
					t.Fatalf("compact refusal = %+v, %v", current, err)
				}
			} else if opened, err := selected.OpenCurrentPublication(ctx, repository, nil); opened != nil || !errors.Is(err, readaccounting.ErrLimit) {
				t.Fatalf("open refusal = %p, %v", opened, err)
			}
			// Denied event counters retain the first limit+1 sentinel, not a
			// successful underlying read. The error must also remain sticky.
			assertProviderReadCounts(t, ledger, test.want, readaccounting.ErrLimit)
			ctx, ledger = providerReadScope(t, test.retry)
			opened, err := selected.OpenCurrentPublication(ctx, repository, nil)
			if err != nil || opened == nil || opened.State() != publication.State() {
				t.Fatalf("independent retry = %p, %v", opened, err)
			}
			assertProviderReadCounts(t, ledger, test.retry, nil)
		})
	}
}

func providerReadScope(t *testing.T, limits readaccounting.Counts) (context.Context, *readaccounting.Ledger) {
	t.Helper()
	ctx, ledger, err := readaccounting.Start(t.Context(), limits)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, ledger
}

func assertProviderReadCounts(t *testing.T, ledger *readaccounting.Ledger, want readaccounting.Counts, wantErr error) {
	t.Helper()
	if counts, err := ledger.Finish(); counts != want || !errors.Is(err, wantErr) {
		t.Fatalf("native provider read counts = %+v, %v; want %+v, %v", counts, err, want, wantErr)
	}
}

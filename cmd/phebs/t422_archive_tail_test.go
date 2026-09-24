package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type t422ArchiveTailScheduleTestSource struct {
	schedule *store.GenerationSchedule
	err      error
	reads    int
}

func (source *t422ArchiveTailScheduleTestSource) GetGenerationScheduleForArchiveTail(
	_ context.Context, _, stage string,
) (*store.GenerationSchedule, error) {
	source.reads++
	if stage != relationshippublication.ScheduleStageV3 {
		return nil, errors.New("wrong schedule stage")
	}
	return source.schedule, source.err
}

func TestT422ArchiveTailWaitsForCurrentInputsAndIdleSchedule(t *testing.T) {
	const repository = "example.test/archive-tail"
	bound := candidate.DownstreamDomainAuthority{
		Domain: "proto-contract", RunID: "old-run",
		PlanDigest: "old-plan", RootDigest: "old-root",
		CandidateManifestDigest: "candidate", SourceGenerationDigest: "source",
		ObservationGenerationDigest: "observation",
	}
	selected := relationshippublication.RootV3{GenerationDigest: "same-relationship"}
	selected.Authority.Upstream.Domains = []candidate.DownstreamDomainAuthority{bound}
	current := store.PartitionedExtractionDomainReference{
		Repository: repository, Domain: bound.Domain, RunID: bound.RunID,
		PlanDigest: bound.PlanDigest, RootDigest: bound.RootDigest,
		CandidateDigest:   bound.CandidateManifestDigest,
		SourceDigest:      bound.SourceGenerationDigest,
		ObservationDigest: bound.ObservationGenerationDigest,
	}
	settled := &store.GenerationSchedule{
		Status: store.GenerationScheduleSettled, Generation: "old-schedule",
	}
	for _, test := range []struct {
		name   string
		change func(*store.PartitionedExtractionDomainReference)
	}{
		{"run ID", func(value *store.PartitionedExtractionDomainReference) { value.RunID = "new-run" }},
		{"plan digest", func(value *store.PartitionedExtractionDomainReference) { value.PlanDigest = "new-plan" }},
		{"root digest", func(value *store.PartitionedExtractionDomainReference) { value.RootDigest = "new-root" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			advanced := current
			test.change(&advanced)
			source := &t422ArchiveTailScheduleTestSource{schedule: settled}
			before, idle, err := t422ArchiveTailSchedule(t.Context(), source, repository)
			if err != nil || !idle {
				t.Fatalf("old schedule = %+v, idle=%v, err=%v", before, idle, err)
			}
			after, afterIdle, err := t422ArchiveTailSchedule(t.Context(), source, repository)
			if err != nil || !afterIdle || source.reads != 2 {
				t.Fatalf("old schedule confirm = %+v, idle=%v, reads=%d, err=%v", after, afterIdle, source.reads, err)
			}
			if t422ArchiveTailReady(before, after,
				t422ArchiveTailDomainCurrent(selected.Authority.Upstream.Domains[0], advanced), afterIdle) {
				t.Fatal("old successful schedule concealed an advanced current extraction identity")
			}
		})
	}
	// No relationship rebuild is needed when the selected binding already
	// matches current inputs. An absent schedule is therefore a valid ready case.
	absent := &t422ArchiveTailScheduleTestSource{err: store.ErrNotFound}
	before, idle, err := t422ArchiveTailSchedule(t.Context(), absent, repository)
	if err != nil || !idle || before != nil {
		t.Fatalf("absent schedule = %+v, idle=%v, err=%v", before, idle, err)
	}
	after, afterIdle, err := t422ArchiveTailSchedule(t.Context(), absent, repository)
	if err != nil || !afterIdle || after != nil || absent.reads != 2 ||
		!t422ArchiveTailReady(before, after,
			t422ArchiveTailDomainCurrent(selected.Authority.Upstream.Domains[0], current), afterIdle) {
		t.Fatalf("matching current inputs with absent schedule failed: before=%+v after=%+v idle=%v/%v reads=%d err=%v",
			before, after, idle, afterIdle, absent.reads, err)
	}
}

func TestT422ArchiveTailReadBudget(t *testing.T) {
	want := readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 275}
	if got := t422ArchiveTailReadinessLimits(); got != want {
		t.Fatalf("archive tail read ceiling = %+v, want %+v", got, want)
	}
	if got := t421TailReadinessLimits(); got != (readaccounting.Counts{
		ControlFileReads: 4, StoreReadAttempts: 4,
	}) {
		t.Fatalf("legacy tail read ceiling changed: %+v", got)
	}
}

func TestT422ArchiveTailResolverUsesAuthorityDigest(t *testing.T) {
	publication := store.ResolverCatalogPublication{
		GenerationDigest: "generation", ManifestDigest: "manifest", AuthorityDigest: "authority",
	}
	resolver := resolvernamespace.Root{Authority: resolvernamespace.Authority{
		ResolverGenerationDigest: "generation", ResolverManifestDigest: "authority",
	}}
	if !t422ArchiveTailResolverMatches(publication, resolver) {
		t.Fatal("distinct manifest and authority digests must match the resolver binding")
	}
	publication.AuthorityDigest = "stale-authority"
	if t422ArchiveTailResolverMatches(publication, resolver) {
		t.Fatal("stale resolver authority was accepted")
	}
}

func TestT422ArchiveTailScheduleStatus(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   store.GenerationScheduleStatus
		failed   int
		wantIdle bool
		wantErr  bool
	}{
		{"active", store.GenerationScheduleActive, 0, false, false},
		{"settled success", store.GenerationScheduleSettled, 0, true, false},
		{"settled failure", store.GenerationScheduleSettled, 1, false, true},
		{"superseded", store.GenerationScheduleSuperseded, 0, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &t422ArchiveTailScheduleTestSource{schedule: &store.GenerationSchedule{
				Status: test.status, Failed: test.failed,
			}}
			_, idle, err := t422ArchiveTailSchedule(t.Context(), source, "example.test/archive-tail")
			if idle != test.wantIdle || (err != nil) != test.wantErr || source.reads != 1 {
				t.Fatalf("idle=%v err=%v reads=%d", idle, err, source.reads)
			}
		})
	}
}

type t422ArchiveTailCatalogStateTestSource struct {
	pointer      store.ServiceCatalogV3Pointer
	summary      servicecatalog.RepositoryState
	pointerReads int
	summaryReads int
	advance      bool
}

func (source *t422ArchiveTailCatalogStateTestSource) GetServiceCatalogV3CandidatePointer(
	_ context.Context, _ string,
) (store.ServiceCatalogV3Pointer, error) {
	source.pointerReads++
	value := source.pointer
	if source.advance && source.pointerReads == 2 {
		value.ControlRevision++
	}
	return value, nil
}

func (source *t422ArchiveTailCatalogStateTestSource) GetServiceStateV3SummaryPoint(
	_ context.Context, _ string,
) (servicecatalog.RepositoryState, error) {
	source.summaryReads++
	return source.summary, nil
}

func TestT422ArchiveTailCatalogStateChargesFourPointReads(t *testing.T) {
	const repository = "example.test/archive-tail"
	root := "sha256:" + strings.Repeat("a", 64)
	pointer := store.ServiceCatalogV3Pointer{
		Repository: repository, RootDigest: root, ControlRevision: 1,
	}
	summary := servicecatalog.RepositoryState{
		Schema: servicecatalogv3.RepositoryStateSchema, Repository: repository,
		CatalogGeneration: root, CatalogControlRevision: 1,
		ControlRevision: 2, UpdatedAt: time.Now().UTC(),
	}
	if err := servicecatalogv3.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	authority := relationshippublication.AuthorityV3{
		CatalogRootDigest: root, CatalogControlRevision: 1,
		ServiceStateSummaryDigest:   summary.SummaryDigest,
		ServiceStateControlRevision: summary.ControlRevision,
	}
	for _, advance := range []bool{false, true} {
		source := &t422ArchiveTailCatalogStateTestSource{
			pointer: pointer, summary: summary, advance: advance,
		}
		ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{StoreReadAttempts: 4})
		if err != nil {
			t.Fatal(err)
		}
		current, readErr := t422ArchiveTailCatalogStateCurrent(ctx, source, repository, authority)
		counts, finishErr := ledger.Finish()
		if readErr != nil || finishErr != nil || current == advance ||
			counts != (readaccounting.Counts{StoreReadAttempts: 4}) ||
			source.pointerReads != 2 || source.summaryReads != 2 {
			t.Fatalf("advance=%v current=%v counts=%+v reads=%d/%d errors=%v/%v",
				advance, current, counts, source.pointerReads, source.summaryReads, readErr, finishErr)
		}
	}
}

func TestT422ArchiveTailHeaderIsClosedToOrdinaryRequests(t *testing.T) {
	for _, test := range []struct {
		name, method, path, value string
		duplicate, want           bool
	}{
		{"complete", http.MethodGet, t421ExactTailReadinessPath, t422ArchiveTailValue, false, true},
		{"absent", http.MethodGet, t421ExactTailReadinessPath, "", false, false},
		{"wrong value", http.MethodGet, t421ExactTailReadinessPath, "settled-v2", false, false},
		{"duplicate", http.MethodGet, t421ExactTailReadinessPath, t422ArchiveTailValue, true, false},
		{"method", http.MethodPost, t421ExactTailReadinessPath, t422ArchiveTailValue, false, false},
		{"final", http.MethodGet, t421ExactFinalAuthorityPath, t422ArchiveTailValue, false, false},
		{"query", http.MethodGet, t421ExactTailReadinessPath + "?extra=1", t422ArchiveTailValue, false, false},
		{"empty query", http.MethodGet, t421ExactTailReadinessPath + "?", t422ArchiveTailValue, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.value != "" {
				request.Header.Set(t422ArchiveTailHeader, test.value)
			}
			if test.duplicate {
				request.Header.Add(t422ArchiveTailHeader, test.value)
			}
			if got := t422ArchiveTailRoute(request); got != test.want {
				t.Fatalf("route = %v", got)
			}
			if (&t421ExactReadAccountingState{}).archiveTailRequest(request) {
				t.Fatal("ordinary/unselected request was admitted")
			}
		})
	}
}

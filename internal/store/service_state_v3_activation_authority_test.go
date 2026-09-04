package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestServiceStateV3ActivationAuthorityReadsSelectedNinthUnit(t *testing.T) {
	state := newServiceCatalogV3InternalStore(t)
	repository := "example.com/acme/activation-authority"
	seedServiceCatalogV3Repo(t, state, repository, strings.Repeat("7", 40))
	services := make([]servicecatalog.Service, 4_609)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%05d", index), DisplayName: fmt.Sprintf("Service %d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	generation := serviceStateV3Generation(
		t, repository, strings.Repeat("7", 40), "activation-authority", services,
	)
	if err := state.PublishServiceCatalogV3Candidate(t.Context(), generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err := state.BeginServiceStateV3Reconcile(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, state, reconcile)
	search := selectorTestDigest("8")
	activation, err := state.BeginServiceStateV3Activation(t.Context(), repository, search)
	if err != nil || activation.Plan == nil || activation.Schedule == nil ||
		activation.Plan.ServiceMemberChunks <= 9 {
		t.Fatalf("activation = %+v, %v", activation, err)
	}
	runServiceStateV3Plan(t, state, activation)
	pointer, err := state.GetServiceCatalogV3CandidatePointer(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := state.GetServiceStateV3SummaryPoint(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	selector := ServiceRuntimeSelector{
		Schema: ServiceRuntimeSelectorSchema, Repository: repository, Backend: ServiceRuntimeV3,
		CatalogRootDigest: pointer.RootDigest, CatalogControlRevision: pointer.ControlRevision,
		StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
		SearchGenerationDigest: search, RelationshipGenerationDigest: selectorTestDigest("9"),
		RelationshipRootDigest: selectorTestDigest("a"), ControlRevision: 1,
		ChangedAt: time.Unix(1, 0).UTC(),
	}
	selector.Digest = serviceRuntimeSelectorDigest(selector)

	read := func(limit uint64) (ServiceStateV3ActivationAuthority, readaccounting.Counts, error) {
		ctx, ledger, startErr := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: limit},
		)
		if startErr != nil {
			t.Fatal(startErr)
		}
		value, readErr := state.ReadServiceStateV3ActivationAuthority(ctx, selector)
		counts, finishErr := ledger.Finish()
		return value, counts, errors.Join(readErr, finishErr)
	}
	want := ServiceStateV3ActivationAuthority{
		PlanDigest: activation.Plan.Digest, ScheduleDigest: activation.Schedule.Digest,
		UnitDigest: generationChunkID(activation.Schedule.Digest, 9, 0),
	}
	got, counts, err := read(3)
	if err != nil || got != want || counts != (readaccounting.Counts{StoreReadAttempts: 3}) {
		t.Fatalf("authority = %+v, counts=%+v, err=%v", got, counts, err)
	}

	// The ordinary no-op retires the current schedule pointer. The immutable
	// selected plan/schedule/unit authority must remain readable by identity.
	noop, err := state.BeginServiceStateV3Activation(t.Context(), repository, search)
	if err != nil || !noop.Noop {
		t.Fatalf("activation no-op = %+v, %v", noop, err)
	}
	got, counts, err = read(3)
	if err != nil || got != want || counts != (readaccounting.Counts{StoreReadAttempts: 3}) {
		t.Fatalf("retired-pointer authority = %+v, counts=%+v, err=%v", got, counts, err)
	}

	for limit := uint64(0); limit < 3; limit++ {
		_, counts, err := read(limit)
		if !errors.Is(err, readaccounting.ErrLimit) ||
			counts != (readaccounting.Counts{StoreReadAttempts: limit + 1}) {
			t.Fatalf("limit %d = counts %+v, err=%v", limit, counts, err)
		}
	}
}

package callerpublication

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestRegistryControlReadAccounting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(
		t, root, "github.com/acme/read-accounting", 'd',
	)
	publication := publishFixture(t, fixture)
	state := publication.State()
	registry := NewRegistry(root)

	coldCtx, coldLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 6, MemberVisits: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Acquire(coldCtx, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	counts, finishErr := coldLedger.Finish()
	wantCold := readaccounting.Counts{ControlFileReads: 6, MemberVisits: 1}
	if finishErr != nil || counts != wantCold {
		t.Fatalf("cold caller controls = %+v, %v", counts, finishErr)
	}

	warmCtx, warmLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	lease, err = registry.Acquire(warmCtx, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	counts, finishErr = warmLedger.Finish()
	if finishErr != nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
		t.Fatalf("warm caller controls = %+v, %v", counts, finishErr)
	}

	refusalCtx, refusalLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(root).Acquire(refusalCtx, state); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("caller control limit = %v", err)
	}
	counts, finishErr = refusalLedger.Finish()
	if !errors.Is(finishErr, readaccounting.ErrLimit) ||
		counts != (readaccounting.Counts{ControlFileReads: 1}) {
		t.Fatalf("caller control refusal = %+v, %v", counts, finishErr)
	}
}

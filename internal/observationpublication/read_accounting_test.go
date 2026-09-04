package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

func readAccountingInventoryFixture(t *testing.T) (string, string, InventoryAuthorityV2) {
	t.Helper()
	repositoryDirectory, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	const repository = "example/read-accounting-v2"
	transition, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	buildInventoryPublicationTransitionV2(t, transition, repositoryDirectory, repository, commit)
	if _, err := CompleteInventoryPublicationV2(t.Context(), root, repository, transition.TransitionID, nil); err != nil {
		t.Fatal(err)
	}
	// Derive the expected authority through the full production validator,
	// outside the compact-reference accounting scope. This test does not claim
	// that the full-stage source/inventory scans are instrumented.
	expected, err := CurrentInventoryAuthorityV2(t.Context(), root, repository)
	if err != nil {
		t.Fatal(err)
	}
	return root, repository, expected
}

func TestCurrentInventoryAuthorityReferenceV2ReadAccounting(t *testing.T) {
	root, repository, expected := readAccountingInventoryFixture(t)
	for _, test := range []struct {
		name  string
		limit uint64
	}{
		{name: "three_exact_reads", limit: 3},
		{name: "repeat_is_an_independent_scope", limit: 3},
		{name: "first_pointer_refuses", limit: 0},
		{name: "source_root_refuses", limit: 1},
		{name: "confirming_pointer_refuses", limit: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: test.limit})
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := CurrentInventoryAuthorityReferenceV2(ctx, root, repository)
			counts, accountingErr := ledger.Finish()
			want := readaccounting.Counts{ControlFileReads: 3}
			if test.limit < 3 {
				want.ControlFileReads = test.limit + 1 // Refusal sentinel, not executed I/O.
				if got != (InventoryAuthorityV2{}) || readErr == nil || !errors.Is(accountingErr, readaccounting.ErrLimit) {
					t.Fatalf("limited reference = %+v, %v; counts=%+v accounting=%v", got, readErr, counts, accountingErr)
				}
			} else if got != expected || readErr != nil || accountingErr != nil {
				t.Fatalf("reference = %+v, %v; want=%+v accounting=%v", got, readErr, expected, accountingErr)
			}
			if counts != want {
				t.Fatalf("reference events=%+v want=%+v", counts, want)
			}
		})
	}
	if got, err := CurrentInventoryAuthorityReferenceV2(t.Context(), root, repository); err != nil || got != expected {
		t.Fatalf("ordinary unobserved reference changed: %+v, %v", got, err)
	}
}

func TestCurrentInventoryDownstreamAuthorityV2ReadAccounting(t *testing.T) {
	root, repository, _ := readAccountingInventoryFixture(t)
	want, err := CurrentInventoryDownstreamAuthorityV2(t.Context(), root, repository)
	if err != nil {
		t.Fatal(err)
	}
	for limit := uint64(0); limit <= 3; limit++ {
		ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: limit})
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := CurrentInventoryDownstreamAuthorityV2(ctx, root, repository)
		counts, accountingErr := ledger.Finish()
		if limit < 3 {
			if got != (DownstreamAuthority{}) || !errors.Is(readErr, readaccounting.ErrLimit) ||
				!errors.Is(accountingErr, readaccounting.ErrLimit) ||
				counts != (readaccounting.Counts{ControlFileReads: limit + 1}) {
				t.Fatalf("limit %d downstream = %+v, %v; counts=%+v accounting=%v", limit, got, readErr, counts, accountingErr)
			}
			continue
		}
		if got != want || readErr != nil || accountingErr != nil ||
			counts != (readaccounting.Counts{ControlFileReads: 3}) {
			t.Fatalf("downstream = %+v, %v; counts=%+v accounting=%v", got, readErr, counts, accountingErr)
		}
	}
}

func TestCurrentInventoryAuthorityReferenceV2ReadAccountingCanceled(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	ctx, ledger, err := readaccounting.Start(canceled, readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := CurrentInventoryAuthorityReferenceV2(ctx, t.TempDir(), "example/read-accounting-v2")
	counts, accountingErr := ledger.Finish()
	if got != (InventoryAuthorityV2{}) || !errors.Is(readErr, context.Canceled) || accountingErr != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("canceled reference = %+v, %v; events=%+v accounting=%v", got, readErr, counts, accountingErr)
	}
}

func TestCurrentInventoryAuthorityReferenceV2ReadAccountingFailedReads(t *testing.T) {
	root, repository, _ := readAccountingInventoryFixture(t)
	selected, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(inventoryGenerationDirectoryV2(root, repository, selected.Current.GenerationDigest), InventoryPublicationSourceNameV2, sourcepartition.SuperRootName(repository))
	for _, test := range []struct {
		name  string
		path  string
		reads uint64
	}{
		{name: "missing_current", path: inventoryPublicationRootPathV2(root, repository), reads: 1},
		{name: "missing_selected_source", path: sourcePath, reads: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			backup := filepath.Join(t.TempDir(), "missing-control")
			if err := os.Rename(test.path, backup); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Rename(backup, test.path); err != nil {
					t.Errorf("restore fixture control: %v", err)
				}
			})
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 3})
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := CurrentInventoryAuthorityReferenceV2(ctx, root, repository)
			counts, accountingErr := ledger.Finish()
			_, ordinaryErr := CurrentInventoryAuthorityReferenceV2(t.Context(), root, repository)
			if got != (InventoryAuthorityV2{}) || readErr == nil || ordinaryErr == nil || readErr.Error() != ordinaryErr.Error() ||
				accountingErr != nil || counts != (readaccounting.Counts{ControlFileReads: test.reads}) {
				t.Fatalf("failed reference = %+v, %v; ordinary=%v counts=%+v accounting=%v", got, readErr, ordinaryErr, counts, accountingErr)
			}
		})
	}
}

func TestReadInventoryPublicationRootV2ContextAccounting(t *testing.T) {
	root, repository, _ := readAccountingInventoryFixture(t)
	expected, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := ReadInventoryPublicationRootV2Context(ctx, root, repository)
	counts, accountingErr := ledger.Finish()
	if readErr != nil || !reflect.DeepEqual(got, expected) || accountingErr != nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
		t.Fatalf("publication root = %+v, %v; counts=%+v accounting=%v", got, readErr, counts, accountingErr)
	}
	for _, test := range []struct {
		name  string
		limit uint64
	}{
		{name: "missing_root", limit: 1},
		{name: "limit_precedes_missing_root"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: test.limit})
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := ReadInventoryPublicationRootV2Context(ctx, t.TempDir(), repository)
			counts, accountingErr := ledger.Finish()
			if !reflect.DeepEqual(got, InventoryPublicationRootV2{}) || readErr == nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
				t.Fatalf("failed pointer read = %+v, %v; counts=%+v accounting=%v", got, readErr, counts, accountingErr)
			}
			if test.limit == 0 {
				if !errors.Is(readErr, readaccounting.ErrLimit) || errors.Is(readErr, os.ErrNotExist) || !errors.Is(accountingErr, readaccounting.ErrLimit) {
					t.Fatalf("limit did not precede filesystem access: %v, %v", readErr, accountingErr)
				}
			} else if !errors.Is(readErr, os.ErrNotExist) || accountingErr != nil {
				t.Fatalf("missing read did not retain its ordinary error: %v, %v", readErr, accountingErr)
			}
		})
	}
}

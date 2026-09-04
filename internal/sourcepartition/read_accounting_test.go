package sourcepartition

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestReadSuperRootContextCountsCompactControlOnly(t *testing.T) {
	_, sourceDirectory, source := superRootFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	directory, expected := buildSuperRootStage(t, sourceDirectory, source)
	if len(expected.Segments) != 1 {
		t.Fatal("fixture must contain one actual source segment")
	}
	// A compact authority read must not reopen the already validated segment.
	// Damage only this test's segment control, not the selected super-root.
	if err := os.WriteFile(filepath.Join(directory, expected.Segments[0].Directory, ManifestName(source.Repository)), []byte("invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 1})
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := ReadSuperRootContext(ctx, directory, source.Repository)
		counts, accountingErr := ledger.Finish()
		if readErr != nil || !reflect.DeepEqual(got, expected) || accountingErr != nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
			t.Fatalf("compact read %d = %+v, %v; counts=%+v accounting=%v", attempt, got, readErr, counts, accountingErr)
		}
	}
	if got, err := ReadSuperRoot(directory, source.Repository); err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("legacy wrapper changed: %+v, %v", got, err)
	}
}

func TestReadSuperRootContextFailedAttemptAndLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		present bool
		limit   uint64
	}{
		{name: "missing", limit: 1},
		{name: "invalid", present: true, limit: 1},
		{name: "denied_before_missing_read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const repository = "example/read-accounting"
			directory := t.TempDir()
			if test.present {
				if err := os.WriteFile(filepath.Join(directory, SuperRootName(repository)), []byte("invalid\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: test.limit})
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := ReadSuperRootContext(ctx, directory, repository)
			counts, accountingErr := ledger.Finish()
			if !reflect.DeepEqual(got, SuperRoot{}) || readErr == nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
				t.Fatalf("failed read = %+v, %v; counts=%+v accounting=%v", got, readErr, counts, accountingErr)
			}
			if test.limit == 0 {
				if !errors.Is(readErr, readaccounting.ErrLimit) || !errors.Is(accountingErr, readaccounting.ErrLimit) {
					t.Fatalf("read reached filesystem before budget refusal: %v, %v", readErr, accountingErr)
				}
			} else {
				_, legacyErr := ReadSuperRoot(directory, repository)
				if accountingErr != nil || legacyErr == nil || readErr.Error() != legacyErr.Error() {
					t.Fatalf("legacy failure changed: observed=%v legacy=%v accounting=%v", readErr, legacyErr, accountingErr)
				}
			}
		})
	}
}

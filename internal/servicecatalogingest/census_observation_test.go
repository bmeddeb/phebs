package servicecatalogingest

import (
	"context"
	"errors"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogCensusActualPrefix(t *testing.T) {
	const repository = "example.invalid/catalog-meter"
	data, _, commit := testMirror(t, repository, map[string]string{"README.md": "readme", "shared/schema.proto": "schema", "svc/main.go": "package main", "link": "SYMLINK:README.md"})
	for _, test := range []struct {
		name, want string
		records    uint64
		success    bool
	}{
		{"success", "BSD", 3, true}, {"gap", "BSD", 1, false},
		{"overlap", "BSD", 3, false}, {"missing_placement", "BSD", 3, false},
		{"bad_commit", "BSE", 0, false}, {"start_failure", "BN", 0, false},
		{"child_sink", "BS", 0, false}, {"records_sink", "BSD", 3, false},
		{"records_cancel", "BSD", 3, false}, {"begin_sink", "B", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var events strings.Builder
			var records uint64
			ctx, err := readaccounting.WithCatalogCensusObserver(ctx, func(event readaccounting.CatalogCensusEvent, _ uint32, n uint64) (uint32, error) {
				events.WriteByte(byte(event))
				records += n
				if test.name == "records_cancel" && event == readaccounting.CatalogCensusRecords {
					cancel()
				}
				if test.name == "child_sink" && event == readaccounting.CatalogCensusChild || test.name == "records_sink" && event == readaccounting.CatalogCensusRecords || test.name == "begin_sink" && event == readaccounting.CatalogCensusBegin {
					return 0, readaccounting.ErrEvent
				}
				return 2, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			catalog := testCatalog(commit, "Orders")
			selectedCommit, selectedData := commit, data
			switch test.name {
			case "gap":
				catalog.Unowned = nil
			case "overlap":
				catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{Path: "svc/main.go", Origin: servicecatalog.OriginBase})
			case "missing_placement":
				catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{Path: "zzz", Origin: servicecatalog.OriginBase})
			case "bad_commit":
				selectedCommit = strings.Repeat("1", 40)
			case "start_failure":
				selectedData = t.TempDir()
			}
			r := Reconciler{DataDir: selectedData}
			validate := func(candidate servicecatalog.Catalog) error {
				_, err := servicecatalog.Normalize(candidate)
				return err
			}
			if test.name == "overlap" {
				// Public normalization already rejects this shape. Exercise the
				// streaming defense through its existing validator seam only.
				validate = func(servicecatalog.Catalog) error { return nil }
			}
			result, err := r.censusValidated(ctx, repository, selectedCommit, catalog, false, validate)
			if (err == nil) != test.success || events.String() != test.want || records != test.records {
				t.Fatalf("events=%s records=%d result=%+v err=%v", events.String(), records, result, err)
			}
			if test.success && uint64(result.FileCount) != records {
				t.Fatal(result, records)
			}
			if !test.success && result.FileCount != 0 {
				t.Fatal("failed source authority retained", result)
			}
			if test.name == "records_cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogCensusV3CurrentReuse(t *testing.T) {
	const repository = "example.invalid/catalog-current"
	data, mirror, commit := testMirror(t, repository, map[string]string{"README.md": "r", "shared/schema.proto": "s", "svc/main.go": "m"})
	path := filepath.Join(t.TempDir(), "catalog.json")
	writeCatalog(t, path, testCatalog(commit, "Orders"))
	state := &v3MemoryStore{memoryStore: memoryStore{repositories: map[string]store.Repo{repository: {Name: repository, IndexedCommitHash: commit}}}}
	r := V3Reconciler{DataDir: data, Store: state, Selections: map[string]config.ServiceCatalog{repository: {Kind: servicecatalog.AuthorityCommitted, ID: "build-catalog", Path: path}}}
	var events strings.Builder
	ctx, err := readaccounting.WithCatalogCensusObserver(t.Context(), func(event readaccounting.CatalogCensusEvent, _ uint32, _ uint64) (uint32, error) {
		events.WriteByte(byte(event))
		return 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := r.ReconcileRepository(ctx, repository); err != nil || outcome != OutcomePublished || events.String() != "BSD" {
		t.Fatal(outcome, err, events.String())
	}
	if err := os.Rename(mirror, mirror+".hidden"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if outcome, err := r.ReconcileRepository(ctx, repository); err != nil || outcome != OutcomeCurrent || events.String() != "BSD" {
			t.Fatal(outcome, err, events.String())
		}
	}
}

func TestCatalogCensusChildSinkFailureJoins(t *testing.T) {
	const repository = "example.invalid/catalog-join"
	data, _, commit := testMirror(t, repository, map[string]string{"README.md": "r"})
	bin := t.TempDir()
	// This command fixture never produces a census. Its only purpose is to
	// keep the actual started PID alive until the observer refuses it.
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, err := readaccounting.WithCatalogCensusObserver(t.Context(), func(event readaccounting.CatalogCensusEvent, _ uint32, _ uint64) (uint32, error) {
		if event == readaccounting.CatalogCensusChild {
			return 0, readaccounting.ErrEvent
		}
		return 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := Reconciler{DataDir: data}
	_, err = r.census(ctx, repository, commit, testCatalog(commit, "Orders"), false)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ProcessState == nil || exitErr.Success() {
		t.Fatalf("refused child did not return its joined native Wait result: %v", err)
	}
}

func TestCatalogCensusInvalidLocalFinish(t *testing.T) {
	for _, records := range []int{-1, 1} {
		t.Run(string(rune('1'+records)), func(t *testing.T) {
			calls := 0
			ctx, err := readaccounting.WithCatalogCensusObserver(t.Context(), func(readaccounting.CatalogCensusEvent, uint32, uint64) (uint32, error) { calls++; return 2, nil })
			if err != nil {
				t.Fatal(err)
			}
			observation := catalogCensusObservation{phase: 2}
			if err := observation.finish(ctx, records); err == nil || calls != 0 {
				t.Fatal("invalid local work reached observer", calls, err)
			}
		})
	}
}

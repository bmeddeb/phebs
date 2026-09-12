package repositoryindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestSourceCensusActualOwnerBytes(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	write(t, repo, "a", "same", 0o644)
	write(t, repo, "b", "same", 0o644)
	write(t, repo, "empty", "", 0o644)
	if err := os.Symlink("a", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "first")
	first := git(t, repo, "rev-parse", "HEAD")
	write(t, repo, "a", "new", 0o644)
	git(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+first+",module")
	git(t, repo, "add", "a")
	git(t, repo, "commit", "-m", "second")
	revisions := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: git(t, repo, "rev-parse", "HEAD")}, {Selector: "before", Branch: "before", Commit: first}}
	for _, mode := range []string{"success", "writer_error", "sink_error", "canceled", "complete_sink_error", "complete_canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stage := filepath.Join(t.TempDir(), "source")
			var logical, unique uint64
			var began, ended, batches, completed int
			var regularOwners uint64
			ctx, err := readaccounting.WithSourceCensusObserver(ctx, func(event readaccounting.SourceCensusEvent, phase uint32, l, u uint64) (uint32, error) {
				switch event {
				case readaccounting.SourceCensusBegin:
					began++
				case readaccounting.SourceCensusBatch:
					batches++
					logical += l
					unique += u
					if mode == "sink_error" {
						return 0, readaccounting.ErrScope
					}
					if mode == "canceled" {
						cancel()
					}
					if mode == "writer_error" && batches == 1 {
						if err := os.Mkdir(filepath.Join(stage, SourceManifestName("example.com/test")), 0o700); err != nil {
							t.Fatal(err)
						}
					}
				case readaccounting.SourceCensusEnd:
					ended++
				case readaccounting.SourceCensusComplete:
					completed++
					regularOwners += l
					if l != 4 || u != 0 {
						t.Fatal("actual regular-owner count", l, u)
					}
					if _, err := os.Stat(filepath.Join(stage, SourceManifestName("example.com/test"))); err != nil {
						t.Fatal("success preceded manifest write", err)
					}
					if mode == "complete_sink_error" {
						return 0, readaccounting.ErrScope
					}
					if mode == "complete_canceled" {
						cancel()
					}
				}
				if event != readaccounting.SourceCensusBegin && phase != 2 {
					t.Fatal(phase)
				}
				return 2, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := BuildSourceGeneration(ctx, repo, stage, "example.com/test", revisions)
			if (err == nil) != (mode == "success") || began != 1 {
				t.Fatal(manifest, began, err)
			}
			if mode == "complete_sink_error" || mode == "complete_canceled" {
				if completed != 1 || ended != 0 || regularOwners != 4 || logical != 11 || unique != 7 {
					t.Fatal("completed work prefix lost on terminal failure", completed, ended, regularOwners, logical, unique, err)
				}
				return
			}
			if mode == "sink_error" || mode == "canceled" {
				if ended != 0 || completed != 0 || logical == 0 {
					t.Fatal(logical, unique, ended)
				}
				return
			}
			wantEnd, wantComplete := 1, 0
			if mode == "success" {
				wantEnd, wantComplete = 0, 1
			}
			if logical != 11 || unique != 7 || ended != wantEnd || completed != wantComplete {
				t.Fatal(logical, unique, ended, err)
			}
			if mode == "success" {
				if manifest.RegularOwnerCount != 4 || regularOwners != 4 || manifest.RegularDeclaredBytes != 11 || manifest.SymlinkOwnerCount != 1 || manifest.GitlinkOwnerCount != 1 {
					t.Fatal(manifest)
				}
				if _, err := WalkPublishedSource(ctx, stage, manifest.Repository, func(SourceRecord) error { return nil }); err != nil {
					t.Fatal(err)
				}
				if began != 1 || logical != 11 {
					t.Fatal("retained read counted census")
				}
				stage = filepath.Join(t.TempDir(), "repeat")
				if _, err := BuildSourceGeneration(ctx, repo, stage, manifest.Repository, revisions); err != nil {
					t.Fatal(err)
				}
				if logical != 22 || unique != 14 || began != 2 || ended != 0 || completed != 2 || regularOwners != 8 {
					t.Fatal(logical, unique, began, ended)
				}
			}
		})
	}
}

func TestSourceCensusMemberBatchesAndEmptyBytes(t *testing.T) {
	for _, count := range []int{0, 1, MaxRecordsPerMember + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			repo := t.TempDir()
			git(t, repo, "init", "-b", "main")
			content := ""
			if count > 1 {
				content = "x"
			}
			for i := 0; i < count; i++ {
				write(t, repo, fmt.Sprintf("f%05d", i), content, 0o644)
			}
			git(t, repo, "add", ".")
			git(t, repo, "commit", "--allow-empty", "-m", "members")
			var logical, unique uint64
			var begins, ends, batches int
			var regularOwners uint64
			ctx, err := readaccounting.WithSourceCensusObserver(t.Context(), func(event readaccounting.SourceCensusEvent, _ uint32, l, u uint64) (uint32, error) {
				switch event {
				case readaccounting.SourceCensusBegin:
					begins++
				case readaccounting.SourceCensusEnd:
					t.Fatal("successful census emitted failed closure")
				case readaccounting.SourceCensusComplete:
					ends++
					regularOwners += l
					if u != 0 {
						t.Fatal(u)
					}
				case readaccounting.SourceCensusBatch:
					batches++
					logical += l
					unique += u
				}
				return 2, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := BuildSourceGeneration(ctx, repo, filepath.Join(t.TempDir(), "source"), "example.com/test", []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: git(t, repo, "rev-parse", "HEAD")}})
			if err != nil || begins != 1 || ends != 1 || regularOwners != uint64(count) || manifest.RegularOwnerCount != count {
				t.Fatal(manifest, begins, ends, err)
			}
			if count <= 1 {
				if logical != 0 || unique != 0 || batches != 0 {
					t.Fatal(logical, unique, batches)
				}
				return
			}
			if logical != uint64(count) || unique != 1 || len(manifest.Members) != 2 || batches != 2 {
				t.Fatal(logical, unique, batches, manifest)
			}
		})
	}
}

func TestSourceCensusUniquenessAndFailurePrefix(t *testing.T) {
	observation := &sourceCensusObservation{phase: 2, seen: make(map[[33]byte]int64)}
	record := SourceRecord{Kind: "regular", ObjectID: strings.Repeat("1", 40), DeclaredBytes: 3}
	for range 2 {
		if err := observation.add(record); err != nil {
			t.Fatal(err)
		}
	}
	record.ObjectID = strings.Repeat("1", 64)
	if err := observation.add(record); err != nil {
		t.Fatal(err)
	}
	if observation.logical != 9 || observation.unique != 6 || len(observation.seen) != 2 {
		t.Fatal(observation)
	}
	record.DeclaredBytes = 4
	if err := observation.add(record); err == nil {
		t.Fatal("inconsistent size admitted")
	}
	if observation.logical != 9 || observation.unique != 6 {
		t.Fatal("valid classified prefix lost", observation)
	}
	ctx, err := readaccounting.WithSourceCensusObserver(t.Context(), func(event readaccounting.SourceCensusEvent, _ uint32, l, u uint64) (uint32, error) {
		if event == readaccounting.SourceCensusEnd {
			t.Fatal("invalid observation closed coverage")
		}
		if event == readaccounting.SourceCensusBatch && (l != 9 || u != 6) {
			t.Fatal(l, u)
		}
		return 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := observation.finish(ctx); !errors.Is(err, readaccounting.ErrEvent) {
		t.Fatal(err)
	}
	if ordinary, err := startSourceCensusObservation(t.Context()); err != nil || ordinary != nil {
		t.Fatal("ordinary allocated census map", ordinary, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := startSourceCensusObservation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSourceCensusCompleteOriginalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	ctx, err := readaccounting.WithSourceCensusObserver(ctx, func(readaccounting.SourceCensusEvent, uint32, uint64, uint64) (uint32, error) {
		calls++
		return 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := &sourceCensusObservation{phase: 2, succeeded: true, regularOwners: 0}
	cancel()
	if err := observation.finish(ctx); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled original context emitted successful terminal", calls, err)
	}
}

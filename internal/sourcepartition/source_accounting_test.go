package sourcepartition

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestSourcePartitionAttemptPrefix(t *testing.T) {
	repository, commit := sourceFixture(t, map[string][]byte{"a.go": []byte("package demo\nconst A=1\n"), "b.go": []byte("package demo\nconst B=2\n")})
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(t.Context(), repository, sourceDirectory, "example/source", []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifest, err := Build(t.Context(), BuildRequest{SourceDirectory: sourceDirectory, OutputDirectory: directory, Repository: source.Repository, Source: source,
		Policy: Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Open(t.Context(), directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, refuseAt := range []int{0, 1, 2} {
		calls, visits := 0, 0
		refusal := errors.New("source observer failed")
		ctx, err := readaccounting.WithSourceObserver(t.Context(), func() error {
			calls++
			if calls == refuseAt {
				return refusal
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for ordinal := range manifest.Members {
			_, err = plan.ReadPartition(ctx, repository, ordinal, func(BlobRecord, []byte) error { visits++; return nil })
			if err != nil {
				break
			}
		}
		if refuseAt == 0 {
			if err != nil || calls != 2 || visits != 2 {
				t.Fatal(calls, visits, err)
			}
		} else if !errors.Is(err, refusal) || calls != refuseAt || visits != refuseAt-1 {
			t.Fatal(calls, visits, err)
		}
	}
}

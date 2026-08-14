package candidate

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestT40R1WholeRepositoryPartitionShapeDiagnostic is an opt-in red/green
// diagnostic for the exact whole-repository gap exposed by the post-envelope
// semantic fit. It deliberately avoids the full ceremony corpus: five neutral
// records in one shared repository member are sufficient to prove whether a
// local domain's two-record execution bound reaches a nil-unit publication.
//
// The test is skipped in ordinary CI. It is expected to fail until the
// versioned execution-subrange contract is implemented, then becomes the
// focused edit-loop gate for that correction.
func TestT40R1WholeRepositoryPartitionShapeDiagnostic(t *testing.T) {
	if os.Getenv("T40R1_PARTITION_SHAPE_DIAGNOSTIC") != "1" {
		t.Skip("set T40R1_PARTITION_SHAPE_DIAGNOSTIC=1 to exercise the whole-repository subrange gate")
	}

	fixture := newGitFixture(t)
	const recordCount = 5
	for index := range recordCount {
		fixture.write(
			fmt.Sprintf("idl/service_%02d.proto", index),
			"syntax = \"proto3\";\nmessage Neutral {}\n",
		)
	}
	commit := fixture.commit("whole-repository execution subrange diagnostic")
	policies := []Policy{{
		Domain: "proto-contract", Version: "1",
		EnumerationPolicy: "proto-contract-t40r1-diagnostic-v1",
		SymlinkPolicy:     "none", Plane: PlaneLocal, MaxRecords: 2,
		Enumerate: func(value string) bool { return strings.HasSuffix(value, ".proto") },
	}}
	identities, err := PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot := t.TempDir()
	manifest, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: candidateRoot,
		Repository: fixture.repository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.UnitDigest != "" || len(manifest.LocalProjections) != 0 ||
		len(manifest.RepositoryMembers) != 1 {
		t.Fatalf("diagnostic did not retain the shared whole-repository route: unit=%q projections=%d members=%d",
			manifest.UnitDigest, len(manifest.LocalProjections), len(manifest.RepositoryMembers))
	}
	publication, err := Open(candidateRoot, Expected{
		Repository: fixture.repository, Commit: commit, Policies: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	root, err := BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := OpenSparse(
		t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "proto-contract", "1")
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	wantCounts := []int{2, 2, 1}
	gotCounts := make([]int, len(partitions))
	for index, partition := range partitions {
		gotCounts[index] = partition.AdmittedRecords
	}
	if !slices.Equal(gotCounts, wantCounts) {
		t.Fatalf("whole-repository execution shape = %v across %d partitions, want %v; focused-local-only reshaping is still bypassed",
			gotCounts, len(partitions), wantCounts)
	}

	seen := make(map[string]struct{}, recordCount)
	for ordinal := range partitions {
		visited := 0
		if err := domain.ReadPartition(t.Context(), ordinal, func(record Record) error {
			if _, duplicate := seen[record.Path]; duplicate {
				return fmt.Errorf("path %q crossed execution subranges", record.Path)
			}
			seen[record.Path] = struct{}{}
			visited++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if visited != wantCounts[ordinal] {
			t.Fatalf("partition %d visited %d records, want %d", ordinal, visited, wantCounts[ordinal])
		}
	}
	if len(seen) != recordCount {
		t.Fatalf("execution subranges covered %d records, want %d", len(seen), recordCount)
	}
}

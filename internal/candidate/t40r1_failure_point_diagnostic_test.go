package candidate

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
)

// TestT40R1WholeRepositoryPartitionShapeDiagnostic is an opt-in red/green
// diagnostic for the exact whole-repository gap exposed by the post-envelope
// semantic fit. It deliberately avoids the full ceremony corpus: five neutral
// records in one shared repository member are sufficient to prove whether a
// local domain's two-record execution bound reaches a nil-unit publication.
//
// The test is skipped in ordinary CI and remains the focused edit-loop gate
// for this contract. TestWholeRepositoryExecutionSubrangesAreExactAndAccounted
// runs the same proof in ordinary CI.
func TestT40R1WholeRepositoryPartitionShapeDiagnostic(t *testing.T) {
	if os.Getenv("T40R1_PARTITION_SHAPE_DIAGNOSTIC") != "1" {
		t.Skip("set T40R1_PARTITION_SHAPE_DIAGNOSTIC=1 to exercise the whole-repository subrange gate")
	}
	assertWholeRepositoryExecutionSubranges(t)
}

func TestWholeRepositoryExecutionSubrangesAreExactAndAccounted(t *testing.T) {
	assertWholeRepositoryExecutionSubranges(t)
}

func assertWholeRepositoryExecutionSubranges(t *testing.T) {
	t.Helper()

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
		ExecutionPartitionPolicy: WholeRepositoryExecutionSubrangePolicy,
		ExecutionMaxRecords:      2,
		Enumerate:                func(value string) bool { return strings.HasSuffix(value, ".proto") },
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
	var buildMetrics SparseBuildMetrics
	root, err := BuildSparseRoot(t.Context(), sparseDirectory, publication, &buildMetrics)
	if err != nil {
		t.Fatal(err)
	}
	member := manifest.RepositoryMembers[0]
	if buildMetrics.CandidateMembersRead != 1 || buildMetrics.CandidateBytesRead != member.ContentBytes {
		t.Fatalf("sparse build reread shared member: metrics=%+v member=%+v", buildMetrics, member)
	}
	if len(root.Domains) != 1 || root.Domains[0].CandidateMemberReadBytes != member.ContentBytes*3 {
		t.Fatalf("sparse read-cost reservation = %+v, want three full member reads", root.Domains)
	}
	var readMetrics SparseReadMetrics
	sparse, err := OpenSparse(
		t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, &readMetrics,
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
	wantStarts := []int{0, 2, 4}
	wantEnds := []int{2, 4, 5}
	if len(partitions) != len(wantCounts) {
		t.Fatalf("whole-repository execution produced %d partitions, want %d", len(partitions), len(wantCounts))
	}
	gotCounts := make([]int, len(partitions))
	for index, partition := range partitions {
		gotCounts[index] = partition.AdmittedRecords
		if partition.Member == nil || partition.Member.Name != member.Name ||
			partition.ExecutionPartitionPolicy != WholeRepositoryExecutionSubrangePolicy ||
			partition.ExecutionMaxRecords != 2 ||
			partition.MemberRecordStart != wantStarts[index] ||
			partition.MemberRecordEnd != wantEnds[index] {
			t.Fatalf("partition %d has wrong execution-subrange authority: %+v", index, partition)
		}
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
	if readMetrics.CandidateMemberReads != len(partitions) ||
		readMetrics.CandidateBytesRead != member.ContentBytes*int64(len(partitions)) {
		t.Fatalf("execution read metrics = %+v, want one full immutable member read per subrange", readMetrics)
	}
	plan, err := BuildDomainResultPlan(domain, DomainResultAuthority{
		SourceGenerationDigest:      "sha256:" + strings.Repeat("a", 64),
		ObservationGenerationDigest: "sha256:" + strings.Repeat("b", 64),
		ExtractorVersion:            "whole-repository-subrange-test-v1",
		ExtractionPolicyDigest:      "sha256:" + strings.Repeat("c", 64),
	}, make([]DomainResultTotals, len(partitions)))
	if err != nil {
		t.Fatalf("result plan rejected execution-subrange authority: %v", err)
	}
	for ordinal := range partitions {
		if plan.Expected[ordinal].PartitionDigest != partitions[ordinal].Digest {
			t.Fatalf("result plan partition %d lost execution-subrange identity", ordinal)
		}
	}

	for _, test := range []struct {
		name   string
		mutate func(*SparseDomainIndex)
	}{
		{name: "overlap", mutate: func(index *SparseDomainIndex) {
			index.Partitions[1].MemberRecordStart = 1
			index.Partitions[1].MemberRecordEnd = 3
		}},
		{name: "gap", mutate: func(index *SparseDomainIndex) {
			index.Partitions[1].MemberRecordStart = 3
			index.Partitions[1].MemberRecordEnd = 5
		}},
		{name: "wrong_bound", mutate: func(index *SparseDomainIndex) {
			index.Partitions[1].ExecutionMaxRecords = 3
		}},
		{name: "missing_authority", mutate: func(index *SparseDomainIndex) {
			index.Partitions[1].ExecutionPartitionPolicy = ""
		}},
	} {
		t.Run("reject_"+test.name, func(t *testing.T) {
			forged := domain.index
			forged.Partitions = cloneSparsePartitions(domain.index.Partitions)
			test.mutate(&forged)
			for ordinal := range forged.Partitions {
				forged.Partitions[ordinal].Digest = sparsePartitionDigest(forged.Partitions[ordinal])
			}
			forged.ScheduleDigest = sparseDomainScheduleDigest(forged)
			forged.Digest = sparseDomainDigest(forged)
			descriptor := domain.descriptor
			descriptor.DomainScheduleDigest = forged.ScheduleDigest
			descriptor.IndexDigest = forged.Digest
			if err := validateSparseDomain(forged, descriptor, manifest, true); !errors.Is(err, ErrInvalidSparseRoot) {
				t.Fatalf("forged execution-subrange authority error = %v", err)
			}
		})
	}
}

func TestFocusedLocalPackingDoesNotAcquireExecutionSubrangeAuthority(t *testing.T) {
	fixture := newGitFixture(t)
	const recordCount = 5
	for index := range recordCount {
		fixture.write(
			fmt.Sprintf("service/idl/service_%02d.proto", index),
			"syntax = \"proto3\";\nmessage Neutral {}\n",
		)
	}
	commit := fixture.commit("focused local packing remains physical")
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository, Name: "service", Primary: []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	policies := []Policy{{
		Domain: "proto-contract", Version: "1",
		EnumerationPolicy: "proto-contract-focused-physical-v1",
		SymlinkPolicy:     "none", Plane: PlaneLocal, MaxRecords: 2,
		ExecutionPartitionPolicy: WholeRepositoryExecutionSubrangePolicy,
		ExecutionMaxRecords:      2,
		Enumerate:                func(value string) bool { return strings.HasSuffix(value, ".proto") },
	}}
	identities, err := PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot := t.TempDir()
	manifest, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: candidateRoot,
		Repository: fixture.repository, Commit: commit, Unit: unit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.UnitDigest == "" || len(manifest.LocalProjections) != 1 ||
		len(manifest.LocalProjections[0].Members) != 3 {
		t.Fatalf("focused candidate packing = unit %q projections %+v", manifest.UnitDigest, manifest.LocalProjections)
	}
	publication, err := Open(candidateRoot, Expected{
		Repository: fixture.repository, Commit: commit, Unit: unit, Policies: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	root, err := BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := OpenSparse(t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "proto-contract", "1")
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	wantCounts := []int{2, 2, 1}
	if len(partitions) != len(wantCounts) {
		t.Fatalf("focused partition count = %d, want %d", len(partitions), len(wantCounts))
	}
	for ordinal, partition := range partitions {
		if partition.AdmittedRecords != wantCounts[ordinal] ||
			partition.ExecutionPartitionPolicy != "" || partition.ExecutionMaxRecords != 0 ||
			partition.MemberRecordStart != 0 || partition.MemberRecordEnd != 0 {
			t.Fatalf("focused partition %d acquired execution-subrange authority: %+v", ordinal, partition)
		}
	}
}

func TestWholeRepositoryExecutionSubrangesSplitMaximumCandidateMember(t *testing.T) {
	candidateRoot := t.TempDir()
	repository := "example.invalid/execution-subrange-maximum"
	generation := "sha256:" + strings.Repeat("d", 64)
	packer := newArtifactPacker(candidateRoot, repository, generation, "repository")
	for index := 0; index < MaxRecordsPerArtifact; index++ {
		if err := packer.add(Record{
			Schema: RecordSchema, Path: fmt.Sprintf("idl/%04d.proto", index),
			OID: strings.Repeat("a", 40), DeclaredBytes: 1, SourceLane: SourceLaneBase,
			Domains: []string{"proto-contract"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	members, err := packer.finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].RecordCount != MaxRecordsPerArtifact {
		t.Fatalf("maximum candidate member = %+v", members)
	}
	identity := PolicyIdentity{
		Domain: "proto-contract", Version: "1",
		EnumerationPolicy: "proto-contract-execution-maximum-v1",
		SymlinkPolicy:     "none", Plane: PlaneLocal,
		ExecutionPartitionPolicy: WholeRepositoryExecutionSubrangePolicy,
		ExecutionMaxRecords:      2048,
	}
	if err := validatePolicyIdentity(identity); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		Repository: repository, Digest: "sha256:" + strings.Repeat("e", 64),
		GenerationDigest: generation, Policies: []PolicyIdentity{identity},
		RepositoryMembers: members,
	}
	builder := newSparseDomainBuilder(manifest, 0, identity)
	publication := &Publication{root: candidateRoot, manifest: manifest}
	aggregatePartitions := 0
	var metrics SparseBuildMetrics
	if err := scanSparseMember(
		t.Context(), publication, sparseSourceMember{artifact: members[0]},
		[]*sparseDomainBuilder{&builder}, &metrics, &aggregatePartitions,
	); err != nil {
		t.Fatal(err)
	}
	finishSparseDomain(&builder)
	if err := validateSparseDomain(builder.index, builder.descriptor, manifest, true); err != nil {
		t.Fatal(err)
	}
	partitions := builder.index.Partitions
	if len(partitions) != 2 || aggregatePartitions != 2 ||
		partitions[0].AdmittedRecords != 2048 || partitions[1].AdmittedRecords != 2048 ||
		partitions[0].MemberRecordStart != 0 || partitions[0].MemberRecordEnd != 2048 ||
		partitions[1].MemberRecordStart != 2048 || partitions[1].MemberRecordEnd != 4096 {
		t.Fatalf("maximum execution subranges = %+v", partitions)
	}
	if metrics.CandidateMembersRead != 1 || metrics.CandidateBytesRead != members[0].ContentBytes ||
		builder.descriptor.CandidateMemberReadBytes != members[0].ContentBytes*2 {
		t.Fatalf("maximum execution cost = metrics %+v descriptor %+v", metrics, builder.descriptor)
	}
}

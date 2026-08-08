package candidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/gitobj"
)

func TestSparseRootIsDeterministicAndReadsOnlySelectedControlsAndMembers(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	fixture.write("src/other.go", "package main\n")
	fixture.write("README.md", "not admitted\n")
	commit := fixture.commit("sparse candidate")
	candidateRoot := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, nil, candidateRoot)
	publication, err := Open(candidateRoot, expected)
	if err != nil {
		t.Fatal(err)
	}

	firstDirectory := t.TempDir()
	var buildMetrics SparseBuildMetrics
	first, err := BuildSparseRoot(t.Context(), firstDirectory, publication, &buildMetrics)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(
		[]string{SparseRootFileName}, first.Domains[0].IndexName, first.Domains[1].IndexName,
	) {
		info, statErr := os.Stat(filepath.Join(firstDirectory, name))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("sparse control %q permissions = %o", name, info.Mode().Perm())
		}
	}
	wantMembers := len(manifest.RepositoryMembers) + len(manifest.CallerLeaves)
	if buildMetrics.CandidateMembersRead != wantMembers {
		t.Fatalf("candidate member reads = %d, want one pass over %d members", buildMetrics.CandidateMembersRead, wantMembers)
	}
	secondDirectory := t.TempDir()
	second, err := BuildSparseRoot(t.Context(), secondDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("sparse roots differ:\n%+v\n%+v", first, second)
	}
	for _, descriptor := range first.Domains {
		firstBytes, readErr := os.ReadFile(filepath.Join(firstDirectory, descriptor.IndexName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		secondBytes, readErr := os.ReadFile(filepath.Join(secondDirectory, descriptor.IndexName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(firstBytes, secondBytes) {
			t.Fatalf("domain index %q is nondeterministic", descriptor.IndexName)
		}
	}

	var readMetrics SparseReadMetrics
	sparse, err := OpenSparse(t.Context(), firstDirectory, candidateRoot, manifest.State(), first.Digest, &readMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if readMetrics.CandidateManifestReads != 1 || readMetrics.RootReads != 1 ||
		readMetrics.DomainIndexReads != 0 || readMetrics.CandidateMemberReads != 0 {
		t.Fatalf("shallow open metrics = %+v", readMetrics)
	}
	descriptor, err := sparse.DomainControl("go-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Availability != "admitted" || readMetrics.DomainIndexReads != 0 {
		t.Fatalf("domain control = %+v, metrics = %+v", descriptor, readMetrics)
	}
	domain, err := sparse.OpenDomain(t.Context(), "go-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	if readMetrics.DomainIndexReads != 1 || readMetrics.CandidateMemberReads != 0 {
		t.Fatalf("domain open metrics = %+v", readMetrics)
	}
	partitions := domain.Partitions()
	if len(partitions) == 0 || partitions[0].ByteKind != "local_declared" {
		t.Fatalf("local partitions = %+v", partitions)
	}
	var paths []string
	if err := domain.ReadPartition(t.Context(), 0, func(record Record) error {
		paths = append(paths, record.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if readMetrics.CandidateMemberReads != 1 || len(paths) == 0 {
		t.Fatalf("selected read metrics = %+v, paths = %v", readMetrics, paths)
	}
	page, err := domain.SchedulePage(0, MaxSchedulePagePartitions)
	if err != nil || page.DomainScheduleDigest != descriptor.DomainScheduleDigest || page.Digest == "" {
		t.Fatalf("schedule page = %+v, %v", page, err)
	}
	resultID, err := PartitionResultIdentity(partitions[0], "sha256:"+strings.Repeat("0", 64))
	if err != nil || !validDigest(resultID) {
		t.Fatalf("partition result identity = %q, %v", resultID, err)
	}
	closure, err := ClosePartitionDeadline(partitions[0])
	if err != nil || closure.Disposition != "deadline_exhausted" || !validDigest(closure.Digest) {
		t.Fatalf("deadline closure = %+v, %v", closure, err)
	}

	if _, err := Open(candidateRoot, expected); err != nil {
		t.Fatalf("v4 reader changed after sparse controls were built: %v", err)
	}
}

func TestSparseControlCreationIsExclusiveAndDoesNotFollowSymlinks(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "control")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeSparseControl(link, []byte("forged\n")); err == nil {
		t.Fatal("exclusive sparse control creation followed an existing symlink")
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "private" {
		t.Fatalf("symlink target changed to %q", raw)
	}
}

func TestSparseFocusedLocalReadsOnlyProjectionMembers(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("service/main.go", "package service\n")
	fixture.write("other/out.go", "package other\n")
	commit := fixture.commit("focused sparse candidate")
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository, Name: "service", Primary: []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, unit, candidateRoot)
	publication, err := Open(candidateRoot, expected)
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	var buildMetrics SparseBuildMetrics
	root, err := BuildSparseRoot(t.Context(), sparseDirectory, publication, &buildMetrics)
	if err != nil {
		t.Fatal(err)
	}
	wantMembers := len(manifest.CallerLeaves)
	for _, projection := range manifest.LocalProjections {
		wantMembers += len(projection.Members)
	}
	if buildMetrics.CandidateMembersRead != wantMembers {
		t.Fatalf("focused build reads = %d, want %d", buildMetrics.CandidateMembersRead, wantMembers)
	}
	sparse, err := OpenSparse(t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "go-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for ordinal := range domain.Partitions() {
		if err := domain.ReadPartition(t.Context(), ordinal, func(record Record) error {
			paths = append(paths, record.Path)
			if !record.InUnit {
				t.Fatalf("focused partition returned out-of-unit record %+v", record)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(paths, []string{"service/main.go"}) {
		t.Fatalf("focused paths = %v", paths)
	}
}

func TestSparseTypedAbsenceIsControlOnlyUnavailable(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("service/main.go", "package service\n")
	commit := fixture.commit("missing typed input")
	policies := []Policy{{
		Domain: "typed-local", Version: "1", EnumerationPolicy: "typed-local-v1",
		Plane: PlaneLocal, TypedInputs: []string{analysisunit.TypedIndexKindSCIP},
		Enumerate: func(value string) bool { return strings.HasSuffix(value, ".go") },
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
	if buildMetrics.CandidateMembersRead != 0 || root.Domains[0].PartitionCount != 0 ||
		root.Domains[0].AdmittedRecords != 0 || root.Domains[0].AdmittedDeclaredBytes != 0 {
		t.Fatalf("unavailable domain performed candidate work: root=%+v metrics=%+v", root.Domains[0], buildMetrics)
	}
	var metrics SparseReadMetrics
	sparse, err := OpenSparse(t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, &metrics)
	if err != nil {
		t.Fatal(err)
	}
	control, err := sparse.DomainControl("typed-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	if control.Availability != "unavailable" || control.UnavailableReason != "typed_input_absent" ||
		len(control.TypedInputs) != 1 || control.TypedInputs[0].Present ||
		metrics.DomainIndexReads != 0 || metrics.CandidateMemberReads != 0 {
		t.Fatalf("unavailable control = %+v, metrics = %+v", control, metrics)
	}
	domain, err := sparse.OpenDomain(t.Context(), "typed-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.SchedulePage(0, 1); !errors.Is(err, ErrSparseUnavailable) {
		t.Fatalf("unavailable schedule error = %v", err)
	}
}

func TestSparsePresentTypedOnlyInputHasScheduledPartition(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("index.scip", "typed-only-index")
	commit := fixture.commit("present typed-only input")
	policies := []Policy{{
		Domain: "typed-local", Version: "1", EnumerationPolicy: "typed-local-v1",
		Plane: PlaneLocal, TypedInputs: []string{analysisunit.TypedIndexKindSCIP},
		Enumerate: func(value string) bool { return strings.HasSuffix(value, ".go") },
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
	if root.Domains[0].Availability != "admitted" || root.Domains[0].PartitionCount != 1 {
		t.Fatalf("typed-only domain control = %+v", root.Domains[0])
	}
	sparse, err := OpenSparse(
		t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "typed-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	if len(partitions) != 1 || partitions[0].Kind != PartitionKindTypedInput ||
		partitions[0].TypedInput == nil || partitions[0].Member != nil ||
		partitions[0].AdmittedRecords != 0 || partitions[0].SourceStart != partitions[0].SourceEnd {
		t.Fatalf("typed-only partitions = %+v", partitions)
	}
	page, err := domain.SchedulePage(0, 1)
	if err != nil || len(page.Partitions) != 1 ||
		page.Partitions[0].Kind != PartitionKindTypedInput || !page.Complete {
		t.Fatalf("typed-only schedule = %+v, %v", page, err)
	}
	input, err := domain.TypedInputPartition(0)
	if err != nil || !input.Present || input.Path != "index.scip" || input.ObjectID == "" {
		t.Fatalf("typed-only work = %+v, %v", input, err)
	}
	scope, err := domain.TypedSourceScope(t.Context(), 0)
	if err != nil || scope.Records() != 0 || scope.Contains("missing.go") {
		t.Fatalf("typed-only source scope records = %d, %v", scope.Records(), err)
	}
	if err := domain.ReadPartition(t.Context(), 0, func(Record) error { return nil }); !errors.Is(err, ErrSparseTypedInput) {
		t.Fatalf("typed partition ordinary read error = %v", err)
	}
	if identity, err := PartitionResultIdentity(
		partitions[0], "sha256:"+strings.Repeat("4", 64),
	); err != nil || !validDigest(identity) {
		t.Fatalf("typed partition result identity = %q, %v", identity, err)
	}
}

func TestSparseTypedPartitionCarriesExactAdmittedDocumentScope(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("service/main.go", "package service\n")
	fixture.write("service/ignored.txt", "outside policy\n")
	fixture.write("index.scip", "typed-index")
	commit := fixture.commit("typed document scope")
	policies := []Policy{{
		Domain: "typed-local", Version: "1", EnumerationPolicy: "typed-local-v1",
		Plane: PlaneLocal, TypedInputs: []string{analysisunit.TypedIndexKindSCIP},
		Enumerate: func(value string) bool { return strings.HasSuffix(value, ".go") },
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
	domain, err := sparse.OpenDomain(t.Context(), "typed-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	typed := partitions[len(partitions)-1]
	scope, err := domain.TypedSourceScope(t.Context(), len(partitions)-1)
	if err != nil {
		t.Fatal(err)
	}
	oid, declaredBytes, admitted := scope.Source("service/main.go")
	if typed.Kind != PartitionKindTypedInput || typed.TypedScope == nil ||
		typed.TypedScope.Records != 1 || !admitted || !gitobj.IsObjectID(oid) || declaredBytes <= 0 ||
		scope.Contains("service/ignored.txt") || scope.Contains("../service/main.go") {
		t.Fatalf("typed document scope = %+v", typed)
	}
	scopePath := filepath.Join(sparseDirectory, typed.TypedScope.Name)
	raw, err := os.ReadFile(scopePath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(scopePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.TypedSourceScope(t.Context(), len(partitions)-1); err == nil {
		t.Fatal("tampered typed document scope validated")
	}
}

func TestSparseTypedDocumentScopeExactBoundaryAndOneOver(t *testing.T) {
	sources := make([]sparseTypedSource, PartitionMaxTypedScopeRecords)
	for index := range PartitionMaxTypedScopeRecords {
		path := fmt.Sprintf("source/%06d.go", index)
		sources[index].path = path
		sources[index].pathIdentity = sha256.Sum256([]byte(path))
		sources[index].objectID = strings.Repeat("a", 40)
		sources[index].declaredBytes = 1
	}
	scope, err := canonicalTypedScope(sources)
	if err != nil || !validSparseTypedScope(scope, PartitionMaxTypedScopeRecords) {
		t.Fatal("exact maximum typed scope was refused")
	}
	if len(scope) > MaxSparseTypedScopeBytes {
		t.Fatalf("maximum typed scope control bytes = %d", len(scope))
	}
	oneOver := append(slices.Clone(sources), sparseTypedSource{})
	if _, err := canonicalTypedScope(oneOver); err == nil {
		t.Fatal("one-over typed scope was admitted")
	}
}

func TestSparseTypedDocumentScopePathBytesExactBoundaryAndOneOver(t *testing.T) {
	const pathLength = 4096
	records := PartitionMaxTypedScopePathBytes / pathLength
	sources := make([]sparseTypedSource, records)
	for index := range sources {
		path := strings.Repeat("a", pathLength-8) + fmt.Sprintf("%08d", index)
		sources[index] = sparseTypedSource{
			path: path, pathIdentity: sha256.Sum256([]byte(path)),
			objectID: strings.Repeat("a", 40), declaredBytes: 1,
		}
	}
	raw, err := canonicalTypedScope(sources)
	if err != nil || !validSparseTypedScope(raw, records) || len(raw) > MaxSparseTypedScopeBytes {
		t.Fatalf("exact typed path-byte boundary was refused: bytes=%d err=%v", len(raw), err)
	}
	extraPath := "z"
	oneOver := append(slices.Clone(sources), sparseTypedSource{
		path: extraPath, pathIdentity: sha256.Sum256([]byte(extraPath)),
		objectID: strings.Repeat("a", 40), declaredBytes: 1,
	})
	if _, err := canonicalTypedScope(oneOver); err == nil {
		t.Fatal("one-over typed path-byte scope was admitted")
	}
}

func TestSparseOverlappingPlanesRetainDistinctByteKinds(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	commit := fixture.commit("overlapping planes")
	candidateRoot := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, nil, candidateRoot)
	publication, err := Open(candidateRoot, expected)
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
	local, err := sparse.OpenDomain(t.Context(), "go-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	caller, err := sparse.OpenDomain(t.Context(), "go-caller", "1")
	if err != nil {
		t.Fatal(err)
	}
	localPartitions, callerPartitions := local.Partitions(), caller.Partitions()
	if len(localPartitions) == 0 || len(callerPartitions) == 0 ||
		localPartitions[0].ByteKind != "local_declared" ||
		callerPartitions[0].ByteKind != "caller_declared" ||
		localPartitions[0].Digest == callerPartitions[0].Digest {
		t.Fatalf("overlapping plane partitions = %+v / %+v", localPartitions, callerPartitions)
	}
}

func TestSparseForgedRangesMissingMembersAndStaleRootsFailClosed(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	fixture.write("src/other.go", "package main\n")
	commit := fixture.commit("first generation")
	candidateRoot := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, nil, candidateRoot)
	publication, err := Open(candidateRoot, expected)
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	root, err := BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("forged range", func(t *testing.T) {
		directory := t.TempDir()
		if err := copySparseDirectory(sparseDirectory, directory); err != nil {
			t.Fatal(err)
		}
		descriptorIndex := slices.IndexFunc(root.Domains, func(descriptor SparseDomainDescriptor) bool {
			return descriptor.Domain == "go-local"
		})
		if descriptorIndex < 0 {
			t.Fatal("go-local sparse descriptor is missing")
		}
		descriptor := root.Domains[descriptorIndex]
		index, _, err := readSparseDomain(filepath.Join(directory, descriptor.IndexName))
		if err != nil {
			t.Fatal(err)
		}
		if index.Partitions[0].AdmittedRecords < 2 {
			t.Fatalf("fixture did not create a reducible partition: %+v", index.Partitions[0])
		}
		index.Partitions[0].AdmittedRecords--
		index.Partitions[0].SourceEnd--
		index.Partitions[0].AdmittedDeclaredBytes /= 2
		index.Partitions[0].Digest = sparsePartitionDigest(index.Partitions[0])
		index.ScheduleDigest = sparseDomainScheduleDigest(index)
		index.Digest = sparseDomainDigest(index)
		raw, err := sparseCanonical(index)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, descriptor.IndexName), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		forgedRoot := root
		forgedRoot.Domains = slices.Clone(root.Domains)
		forgedRoot.Domains[descriptorIndex].PartitionCount = len(index.Partitions)
		forgedRoot.Domains[descriptorIndex].AdmittedRecords = index.Partitions[0].AdmittedRecords
		forgedRoot.Domains[descriptorIndex].AdmittedDeclaredBytes = index.Partitions[0].AdmittedDeclaredBytes
		forgedRoot.Domains[descriptorIndex].IndexBytes = int64(len(raw))
		forgedRoot.Domains[descriptorIndex].IndexDigest = index.Digest
		forgedRoot.Domains[descriptorIndex].IndexContentDigest = sparseContentDigest(raw)
		forgedRoot.Domains[descriptorIndex].DomainScheduleDigest = index.ScheduleDigest
		forgedRoot.ScheduleDigest = sparseScheduleDigest(forgedRoot)
		forgedRoot.Digest = sparseRootDigest(forgedRoot)
		rootBytes, err := sparseCanonical(forgedRoot)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, SparseRootFileName), rootBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateSparseRoot(forgedRoot, manifest); err != nil {
			t.Fatalf("structurally rebased forged root should require authority binding: %v", err)
		}
		if err := validateSparseDomain(
			index, forgedRoot.Domains[descriptorIndex], manifest, true,
		); err != nil {
			t.Fatalf("structurally rebased forged index should require authority binding: %v", err)
		}
		if err := ValidateSparseStage(
			t.Context(), directory, manifest, root.Digest,
		); !errors.Is(err, ErrSparseStale) {
			t.Fatalf("forged range error = %v", err)
		}
	})

	t.Run("missing candidate member", func(t *testing.T) {
		sparse, err := OpenSparse(t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, nil)
		if err != nil {
			t.Fatal(err)
		}
		domain, err := sparse.OpenDomain(t.Context(), "go-local", "1")
		if err != nil {
			t.Fatal(err)
		}
		member := domain.Partitions()[0].Member.Name
		if err := os.Remove(filepath.Join(candidateRoot, member)); err != nil {
			t.Fatal(err)
		}
		if err := domain.ReadPartition(t.Context(), 0, func(Record) error { return nil }); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing member error = %v", err)
		}
	})

	t.Run("stale generation", func(t *testing.T) {
		fixture.write("src/next.go", "package main\n")
		nextCommit := fixture.commit("next generation")
		nextRoot := t.TempDir()
		nextManifest, _ := buildFixture(t, fixture, nextCommit, nil, nextRoot)
		if _, err := OpenSparse(t.Context(), sparseDirectory, nextRoot, nextManifest.State(), root.Digest, nil); err == nil {
			t.Fatal("stale sparse root opened against a successor candidate generation")
		}
	})

	t.Run("unexpected sparse authority", func(t *testing.T) {
		unexpected := "sha256:" + strings.Repeat("f", 64)
		if _, err := OpenSparse(
			t.Context(), sparseDirectory, candidateRoot, manifest.State(), unexpected, nil,
		); !errors.Is(err, ErrSparseStale) {
			t.Fatalf("unexpected root authority error = %v", err)
		}
	})
}

func TestSparsePartitionQuotasAndSchedulePagingAreFrozen(t *testing.T) {
	if !sparseAggregateWithinLimits(MaxSparseAggregatePartitions, MaxSparseAggregateIndexBytes) ||
		sparseAggregateWithinLimits(MaxSparseAggregatePartitions+1, MaxSparseAggregateIndexBytes) ||
		sparseAggregateWithinLimits(MaxSparseAggregatePartitions, MaxSparseAggregateIndexBytes+1) {
		t.Fatal("sparse aggregate partition/index bounds are not exact")
	}
	quotas := FrozenExtractionPartitionQuotas()
	base := ExtractionPartition{
		Schema: ExtractionPartitionSchema, Repository: "example.invalid/repo",
		CandidateManifestDigest:   "sha256:" + strings.Repeat("1", 64),
		CandidateGenerationDigest: "sha256:" + strings.Repeat("2", 64),
		Domain:                    "domain", Version: "1", Plane: PlaneRepository,
		Kind:    PartitionKindCandidateMember,
		Ordinal: 0, SourceStart: 0, SourceEnd: quotas.CandidateRecords,
		ByteKind: "repository_declared",
		Member: &Artifact{
			Name: "candidate-000000.ndjson", Ordinal: 0,
			RecordCount: quotas.CandidateRecords, DeclaredBytes: quotas.CandidateDeclaredBytes,
			ContentBytes: quotas.CandidateContentBytes, ContentDigest: "sha256:" + strings.Repeat("3", 64),
		},
		AdmittedRecords: quotas.CandidateRecords, AdmittedDeclaredBytes: quotas.CandidateDeclaredBytes,
		Quotas: quotas,
	}
	base.Digest = sparsePartitionDigest(base)
	if err := validateSparsePartition(base, nil, 0); err != nil {
		t.Fatalf("exact quota partition: %v", err)
	}
	for _, mutate := range []func(*ExtractionPartition){
		func(partition *ExtractionPartition) { partition.AdmittedRecords++ },
		func(partition *ExtractionPartition) { partition.Member.RecordCount++ },
		func(partition *ExtractionPartition) { partition.Member.DeclaredBytes++ },
		func(partition *ExtractionPartition) { partition.Member.ContentBytes++ },
		func(partition *ExtractionPartition) { partition.Quotas.Facts++ },
		func(partition *ExtractionPartition) { partition.Quotas.Rows++ },
		func(partition *ExtractionPartition) { partition.Quotas.References++ },
		func(partition *ExtractionPartition) { partition.Quotas.DeadlineMilliseconds++ },
	} {
		candidate := cloneSparsePartitions([]ExtractionPartition{base})[0]
		mutate(&candidate)
		candidate.Digest = sparsePartitionDigest(candidate)
		if err := validateSparsePartition(candidate, nil, 0); !errors.Is(err, ErrInvalidSparseRoot) {
			t.Fatalf("one-over partition error = %v", err)
		}
	}

	partitions := make([]ExtractionPartition, 300)
	for ordinal := range partitions {
		partitions[ordinal].Ordinal = ordinal
		partitions[ordinal].Digest = "sha256:" + strings.Repeat(string("0123456789abcdef"[ordinal%16]), 64)
	}
	domain := &SparseDomain{
		publication: &SparsePublication{root: SparseRoot{ScheduleDigest: "sha256:" + strings.Repeat("a", 64)}},
		index: SparseDomainIndex{
			Domain: "domain", Version: "1", ScheduleDigest: "sha256:" + strings.Repeat("b", 64),
			Partitions: partitions,
		},
	}
	first, err := domain.SchedulePage(0, MaxSchedulePagePartitions)
	if err != nil || len(first.Partitions) != MaxSchedulePagePartitions || first.Complete {
		t.Fatalf("first schedule page = %+v, %v", first, err)
	}
	second, err := domain.SchedulePage(first.EndOrdinal, MaxSchedulePagePartitions)
	if err != nil || len(second.Partitions) != 44 || !second.Complete || first.Digest == second.Digest {
		t.Fatalf("second schedule page = %+v, %v", second, err)
	}
}

// TestT408MaximumNeutralPartitionMeasurement is deliberately opt-in because it
// authors and indexes 4,096 source-free Git blobs before replaying the exact
// maximum candidate partition through the production sparse reader.
func TestT408MaximumNeutralPartitionMeasurement(t *testing.T) {
	if os.Getenv("T408_MEASURE") != "1" {
		t.Skip("set T408_MEASURE=1 to measure the maximum neutral sparse partition")
	}
	resultsPath := os.Getenv("T408_RESULTS_PATH")
	if !filepath.IsAbs(resultsPath) {
		t.Fatal("T408_RESULTS_PATH must be an absolute path")
	}
	fixture := newGitFixture(t)
	for index := 0; index < PartitionMaxCandidateRecords; index++ {
		fixture.write(
			fmt.Sprintf("src/p/%04d.go", index),
			"package p\n",
		)
	}
	commit := fixture.commit("maximum neutral sparse partition")
	policies := []Policy{{
		Domain: "go-repository", Version: "1", EnumerationPolicy: "go-repository-v1",
		Plane:     PlaneRepository,
		Enumerate: func(value string) bool { return strings.HasSuffix(value, ".go") },
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
	var readMetrics SparseReadMetrics
	sparse, err := OpenSparse(t.Context(), sparseDirectory, candidateRoot, manifest.State(), root.Digest, &readMetrics)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "go-repository", "1")
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	if len(partitions) != 1 || partitions[0].AdmittedRecords != PartitionMaxCandidateRecords {
		t.Fatalf("maximum partition = %+v", partitions)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	visited := 0
	if err := domain.ReadPartition(t.Context(), 0, func(Record) error {
		visited++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	rootBytes, err := os.Stat(filepath.Join(sparseDirectory, SparseRootFileName))
	if err != nil {
		t.Fatal(err)
	}
	indexBytes, err := os.Stat(filepath.Join(sparseDirectory, root.Domains[0].IndexName))
	if err != nil {
		t.Fatal(err)
	}
	receipt := struct {
		Schema                    string `json:"schema"`
		GoVersion                 string `json:"go_version"`
		GOOS                      string `json:"goos"`
		GOARCH                    string `json:"goarch"`
		GOMAXPROCS                int    `json:"gomaxprocs"`
		CandidateRecords          int    `json:"candidate_records"`
		CandidateDeclaredBytes    int64  `json:"candidate_declared_bytes"`
		CandidateContentBytes     int64  `json:"candidate_content_bytes"`
		SparseRootBytes           int64  `json:"sparse_root_bytes"`
		DomainIndexBytes          int64  `json:"domain_index_bytes"`
		BuildCandidateMemberReads int    `json:"build_candidate_member_reads"`
		BuildCandidateBytesRead   int64  `json:"build_candidate_bytes_read"`
		BuildAggregatePartitions  int    `json:"build_aggregate_partitions"`
		BuildAggregateIndexBytes  int64  `json:"build_aggregate_index_bytes"`
		AggregatePartitionLimit   int    `json:"aggregate_partition_limit"`
		AggregateIndexByteLimit   int64  `json:"aggregate_index_byte_limit"`
		OpenControlBytesRead      int64  `json:"open_control_bytes_read"`
		ReadCandidateMemberReads  int    `json:"read_candidate_member_reads"`
		ReadCandidateBytes        int64  `json:"read_candidate_bytes"`
		ReadVisitedRecords        int    `json:"read_visited_records"`
		ReadWallMilliseconds      int64  `json:"read_wall_milliseconds"`
		ReadAllocatedBytes        uint64 `json:"read_allocated_bytes"`
		DeadlineMilliseconds      int64  `json:"deadline_milliseconds"`
		CompletedWithinDeadline   bool   `json:"completed_within_deadline"`
		PrivateControlsVerified   bool   `json:"private_controls_verified"`
	}{
		Schema:    "t40.8-sparse-partition-measurement-v1",
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		GOMAXPROCS:             runtime.GOMAXPROCS(0),
		CandidateRecords:       partitions[0].AdmittedRecords,
		CandidateDeclaredBytes: partitions[0].AdmittedDeclaredBytes,
		CandidateContentBytes:  partitions[0].Member.ContentBytes,
		SparseRootBytes:        rootBytes.Size(), DomainIndexBytes: indexBytes.Size(),
		BuildCandidateMemberReads: buildMetrics.CandidateMembersRead,
		BuildCandidateBytesRead:   buildMetrics.CandidateBytesRead,
		BuildAggregatePartitions:  buildMetrics.AggregatePartitions,
		BuildAggregateIndexBytes:  buildMetrics.AggregateIndexBytes,
		AggregatePartitionLimit:   MaxSparseAggregatePartitions,
		AggregateIndexByteLimit:   MaxSparseAggregateIndexBytes,
		OpenControlBytesRead:      readMetrics.ControlBytesRead,
		ReadCandidateMemberReads:  readMetrics.CandidateMemberReads,
		ReadCandidateBytes:        readMetrics.CandidateBytesRead,
		ReadVisitedRecords:        visited, ReadWallMilliseconds: elapsed.Milliseconds(),
		ReadAllocatedBytes:      after.TotalAlloc - before.TotalAlloc,
		DeadlineMilliseconds:    FrozenExtractionPartitionQuotas().DeadlineMilliseconds,
		CompletedWithinDeadline: elapsed < PartitionDeadline,
		PrivateControlsVerified: rootBytes.Mode().Perm()&0o077 == 0 && indexBytes.Mode().Perm()&0o077 == 0,
	}
	if !receipt.CompletedWithinDeadline || receipt.ReadCandidateMemberReads != 1 ||
		receipt.ReadVisitedRecords != PartitionMaxCandidateRecords || !receipt.PrivateControlsVerified {
		t.Fatalf("maximum neutral receipt = %+v", receipt)
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultsPath, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copySparseDirectory(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}

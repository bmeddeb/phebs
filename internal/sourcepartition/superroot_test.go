package sourcepartition

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

// spreadBlobHash assigns each distinct object a distinct deterministic
// initial prefix in first-seen order so small fixtures produce several
// members without corpus-sized inputs.
func spreadBlobHash(t *testing.T) {
	t.Helper()
	previous := sourceBlobHash
	var order []string
	sourceBlobHash = func(objectID string) [sha256.Size]byte {
		hash := productionSourceBlobHash(objectID)
		index := slices.Index(order, objectID)
		if index < 0 {
			index = len(order)
			order = append(order, objectID)
		}
		hash[0] = byte(index << 4)
		return hash
	}
	t.Cleanup(func() { sourceBlobHash = previous })
}

func superRootFixture(
	t *testing.T, files map[string][]byte,
) (string, string, repositoryindex.SourceManifest) {
	t.Helper()
	repository, commit := sourceFixture(t, files)
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repository, sourceDirectory, "example/superroot",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return repository, sourceDirectory, source
}

func buildSuperRootStage(
	t *testing.T, sourceDirectory string, source repositoryindex.SourceManifest,
) (string, SuperRoot) {
	t.Helper()
	directory, root, _ := buildSuperRootStageReusing(t, sourceDirectory, source, "")
	return directory, root
}

func buildSuperRootStageReusing(
	t *testing.T, sourceDirectory string, source repositoryindex.SourceManifest,
	prior string,
) (string, SuperRoot, SuperRootBuildMetrics) {
	t.Helper()
	directory := t.TempDir()
	var metrics SuperRootBuildMetrics
	root, err := BuildSuperRoot(t.Context(), BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: directory,
		Repository: source.Repository, Source: source,
		Policy: Policy{
			Schema: PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
		PriorSuperRootDirectory: prior, SuperRootMetrics: &metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	return directory, root, metrics
}

// splitIntoTwoSegments rewrites a fresh one-segment stage as an equivalent
// two-segment stage so multi-segment validation and keyed reads are exercised
// without a multi-gigabyte corpus. Only identity fields that depend on final
// placement change; member content bytes are moved, never rewritten.
func splitIntoTwoSegments(t *testing.T, directory string, root SuperRoot) (string, SuperRoot) {
	t.Helper()
	if len(root.Segments) != 1 || root.MemberCount < 2 {
		t.Fatalf("fixture is not splittable: %+v", root)
	}
	sourceSegment := filepath.Join(directory, root.Segments[0].Directory)
	manifest, err := ReadManifest(sourceSegment, root.Repository)
	if err != nil {
		t.Fatal(err)
	}
	crafted := t.TempDir()
	pivot := len(manifest.Members) / 2
	groups := [][]Member{manifest.Members[:pivot], manifest.Members[pivot:]}
	next := root
	next.Segments = []SegmentEntry{}
	for ordinal, group := range groups {
		segmentDirectory := filepath.Join(crafted, segmentDirectoryName(ordinal))
		if err := os.Mkdir(segmentDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		segment := Manifest{
			Schema: ManifestSchema, Repository: manifest.Repository,
			SourceGenerationDigest: manifest.SourceGenerationDigest,
			RevisionCount:          manifest.RevisionCount,
			Policy:                 manifest.Policy, PolicyDigest: manifest.PolicyDigest,
			PartitionPolicy:  frozenPolicy(),
			GenerationDigest: manifest.GenerationDigest,
			Members:          make([]Member, 0, len(group)),
		}
		for local, member := range group {
			renamed := member
			renamed.Ordinal, renamed.Count = local, len(group)
			renamed.Name = memberName(manifest.Repository, manifest.GenerationDigest, local)
			raw, err := os.ReadFile(filepath.Join(sourceSegment, member.Name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(segmentDirectory, renamed.Name), raw, 0o600,
			); err != nil {
				t.Fatal(err)
			}
			segment.Members = append(segment.Members, renamed)
			segment.BlobCount += renamed.BlobCount
			segment.PlacementCount += renamed.PlacementCount
			segment.DeclaredBytes += renamed.DeclaredBytes
			segment.EncodedMemberBytes += renamed.ContentBytes
		}
		segment.Digest, err = ManifestDigest(segment)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeCanonicalJSON(
			filepath.Join(segmentDirectory, ManifestName(root.Repository)),
			segment, MaxManifestBytes,
		); err != nil {
			t.Fatal(err)
		}
		next.Segments = append(next.Segments, SegmentEntry{
			Ordinal: ordinal, Count: len(groups),
			Directory:          segmentDirectoryName(ordinal),
			ManifestDigest:     segment.Digest,
			MemberCount:        len(group),
			FirstPrefix:        group[0].Prefix,
			LastPrefix:         group[len(group)-1].Prefix,
			BlobCount:          segment.BlobCount,
			PlacementCount:     segment.PlacementCount,
			DeclaredBytes:      segment.DeclaredBytes,
			EncodedMemberBytes: segment.EncodedMemberBytes,
		})
	}
	next.Digest, err = SuperRootDigest(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCanonicalJSON(
		filepath.Join(crafted, SuperRootName(root.Repository)), next, MaxManifestBytes,
	); err != nil {
		t.Fatal(err)
	}
	return crafted, next
}

func TestBuildSuperRootIsDeterministicAndKeyedReadTouchesOnlyItsSlice(t *testing.T) {
	spreadBlobHash(t)
	repository, sourceDirectory, source := superRootFixture(t, map[string][]byte{
		"a.go":      []byte("package demo\n\nconst A = 1\n"),
		"copy/a.go": []byte("package demo\n\nconst A = 1\n"),
		"b.go":      []byte("package demo\n\nconst B = 2\n"),
		"c.go":      []byte("package demo\n\nconst C = 3\n"),
		"skip.txt":  []byte("not selected\n"),
	})
	firstDirectory, first := buildSuperRootStage(t, sourceDirectory, source)
	secondDirectory, second := buildSuperRootStage(t, sourceDirectory, source)
	if first.Digest != second.Digest || first.BlobCount != 3 ||
		first.PlacementCount != 4 || first.MemberCount != 3 ||
		len(first.Segments) != 1 {
		t.Fatalf("super roots differ: %+v %+v", first, second)
	}
	assertTreesEqual(t, firstDirectory, secondDirectory)

	crafted, craftedRoot := splitIntoTwoSegments(t, firstDirectory, first)
	if err := ValidateSuperRootStage(t.Context(), crafted, craftedRoot); err != nil {
		t.Fatal(err)
	}
	plan, err := OpenSuperRootKeyed(crafted, source.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Root().Digest != craftedRoot.Digest {
		t.Fatalf("keyed root = %+v", plan.Root())
	}

	// Corrupt everything outside the keyed slice: the other segment's control
	// and members, plus nothing the keyed read needs. The keyed read must not
	// notice, which proves it opens the root, one segment control, and one
	// member only.
	otherSegment := filepath.Join(crafted, craftedRoot.Segments[1].Directory)
	entries, err := os.ReadDir(otherSegment)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.WriteFile(
			filepath.Join(otherSegment, entry.Name()), []byte("corrupt\n"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	seen := 0
	stats, err := plan.ReadSegmentPartition(
		t.Context(), repository, 0, 0,
		func(record BlobRecord, content []byte) error {
			if record.ObjectID == "" || len(content) == 0 {
				t.Fatal("keyed reader yielded an empty blob")
			}
			seen++
			return nil
		},
	)
	if err != nil || seen == 0 || stats.Blobs != craftedRoot.Segments[0].BlobCount {
		t.Fatalf("keyed read = %+v, seen=%d, %v", stats, seen, err)
	}
	if _, err := plan.ReadSegmentPartition(
		t.Context(), repository, 1, 0, func(BlobRecord, []byte) error { return nil },
	); err == nil {
		t.Fatal("corrupted segment control still admitted a keyed read")
	}
	if err := ValidateSuperRootStage(t.Context(), crafted, craftedRoot); err == nil {
		t.Fatal("complete validation ignored a corrupted segment")
	}
	if _, err := plan.ReadSegmentPartition(
		t.Context(), repository, 2, 0, func(BlobRecord, []byte) error { return nil },
	); err == nil {
		t.Fatal("out-of-range segment was admitted")
	}
}

func TestSuperRootSmallDeltaAndABAReuseMembersByteExactly(t *testing.T) {
	spreadBlobHash(t)
	repository, commitA := sourceFixture(t, map[string][]byte{
		"a.go": []byte("package demo\n\nconst A = 1\n"),
		"b.go": []byte("package demo\n\nconst B = 2\n"),
	})
	buildSource := func(commit string) (string, repositoryindex.SourceManifest) {
		t.Helper()
		sourceDirectory := filepath.Join(t.TempDir(), "source")
		source, err := repositoryindex.BuildSourceGeneration(
			t.Context(), repository, sourceDirectory, "example/superroot-reuse",
			[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
		)
		if err != nil {
			t.Fatal(err)
		}
		return sourceDirectory, source
	}
	sourceDirectoryA, sourceA := buildSource(commitA)
	directoryA, rootA := buildSuperRootStage(t, sourceDirectoryA, sourceA)

	if err := os.WriteFile(
		filepath.Join(repository, "b.go"),
		[]byte("package demo\n\nconst B = 3\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "b.go")
	runGit(t, repository, "commit", "-m", "B")
	commitB := strings.TrimSpace(runGit(t, repository, "rev-parse", "HEAD"))
	sourceDirectoryB, sourceB := buildSource(commitB)
	directoryB, rootB, metricsB := buildSuperRootStageReusing(
		t, sourceDirectoryB, sourceB, directoryA,
	)
	if rootA.Digest == rootB.Digest {
		t.Fatal("distinct sources reused one super-root identity")
	}
	if metricsB.ReusedMembers == 0 || metricsB.WrittenMembers == 0 {
		t.Fatalf("small-delta physical reuse = %+v", metricsB)
	}
	byPrefix := func(directory string, root SuperRoot) map[string][]byte {
		result := make(map[string][]byte)
		for _, entry := range root.Segments {
			manifest, err := ReadManifest(
				filepath.Join(directory, entry.Directory), root.Repository,
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range manifest.Members {
				raw, err := os.ReadFile(
					filepath.Join(directory, entry.Directory, member.Name),
				)
				if err != nil {
					t.Fatal(err)
				}
				result[member.Prefix] = raw
			}
		}
		return result
	}
	membersA, membersB := byPrefix(directoryA, rootA), byPrefix(directoryB, rootB)
	pathsByPrefix := func(directory string, root SuperRoot) map[string]string {
		result := make(map[string]string)
		for _, entry := range root.Segments {
			segmentDirectory := filepath.Join(directory, entry.Directory)
			manifest, err := ReadManifest(segmentDirectory, root.Repository)
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range manifest.Members {
				result[member.Prefix] = filepath.Join(segmentDirectory, member.Name)
			}
		}
		return result
	}
	pathsA, pathsB := pathsByPrefix(directoryA, rootA), pathsByPrefix(directoryB, rootB)
	shared := 0
	for prefix, rawA := range membersA {
		rawB, ok := membersB[prefix]
		if !ok {
			continue
		}
		shared++
		if string(rawA) != string(rawB) {
			t.Fatalf("unchanged member %q is not byte-exact across the delta", prefix)
		}
		infoA, errA := os.Stat(pathsA[prefix])
		infoB, errB := os.Stat(pathsB[prefix])
		if errA != nil || errB != nil || !os.SameFile(infoA, infoB) {
			t.Fatalf("unchanged member %q was not physically reused: %v %v", prefix, errA, errB)
		}
	}
	if shared == 0 {
		t.Fatal("small delta shared no member")
	}

	sourceDirectoryReturned, sourceReturned := buildSource(commitA)
	if sourceReturned.Digest != sourceA.Digest {
		t.Fatalf("A-content return changed source identity: %q != %q",
			sourceReturned.Digest, sourceA.Digest)
	}
	directoryReturned, rootReturned, returnedMetrics := buildSuperRootStageReusing(
		t, sourceDirectoryReturned, sourceReturned, directoryA,
	)
	if rootReturned.Digest != rootA.Digest {
		t.Fatalf("A-B-A super root = %q, want %q", rootReturned.Digest, rootA.Digest)
	}
	assertTreesEqual(t, directoryA, directoryReturned)
	if returnedMetrics.ReusedMembers != rootReturned.MemberCount ||
		returnedMetrics.WrittenMembers != 0 {
		t.Fatalf("A-B-A physical reuse = %+v", returnedMetrics)
	}
}

func TestBuildSuperRootRefusesCorruptPriorReuseAuthority(t *testing.T) {
	spreadBlobHash(t)
	_, sourceDirectory, source := superRootFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	priorDirectory, prior := buildSuperRootStage(t, sourceDirectory, source)
	manifest, err := ReadManifest(
		filepath.Join(priorDirectory, prior.Segments[0].Directory), prior.Repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(priorDirectory, prior.Segments[0].Directory, manifest.Members[0].Name),
		[]byte("corrupt\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if _, err := BuildSuperRoot(t.Context(), BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: directory,
		Repository: source.Repository, Source: source,
		Policy:                  Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
		PriorSuperRootDirectory: priorDirectory,
	}); err == nil {
		t.Fatal("corrupt prior super-root was trusted for reuse")
	}
}

func TestPackSegmentsHonorsExactBoundsPreGrowth(t *testing.T) {
	declared := func(bytes int64) Member { return Member{DeclaredBytes: bytes, ContentBytes: 1} }
	encoded := func(bytes int64) Member { return Member{DeclaredBytes: 1, ContentBytes: bytes} }
	tests := []struct {
		name         string
		members      []Member
		wantSegments []int
		wantErr      bool
		dimension    pipelinerefusal.Dimension
		observed     int64
		limit        int64
	}{
		{
			name:         "declared bytes split exactly at the segment bound",
			members:      []Member{declared(MaxGenerationBlobBytes - 1), declared(1), declared(1)},
			wantSegments: []int{2, 1},
		},
		{
			name:         "encoded bytes split exactly at the segment bound",
			members:      []Member{encoded(MaxGenerationContentBytes - 1), encoded(1), encoded(1)},
			wantSegments: []int{2, 1},
		},
		{
			name: "exact aggregate segment count is admitted",
			members: func() []Member {
				result := make([]Member, MaxSegments)
				for index := range result {
					result[index] = declared(MaxGenerationBlobBytes)
				}
				return result
			}(),
			wantSegments: func() []int {
				result := make([]int, MaxSegments)
				for index := range result {
					result[index] = 1
				}
				return result
			}(),
		},
		{
			name: "one segment over the aggregate bound refuses pre-growth",
			members: func() []Member {
				result := make([]Member, MaxSegments+1)
				for index := range result {
					result[index] = declared(MaxGenerationBlobBytes)
				}
				return result
			}(),
			wantErr:   true,
			dimension: pipelinerefusal.DimensionAggregateSegments,
			observed:  MaxSegments + 1, limit: MaxSegments,
		},
		{
			name: "one member over the aggregate member bound refuses",
			members: func() []Member {
				result := make([]Member, MaxAggregateMembers+1)
				for index := range result {
					result[index] = declared(1)
				}
				return result
			}(),
			wantErr:   true,
			dimension: pipelinerefusal.DimensionPartitionCount,
			observed:  MaxAggregateMembers + 1, limit: MaxAggregateMembers,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			segments, err := packSegments(testCase.members)
			if !testCase.wantErr {
				if err != nil || !slices.Equal(segments, testCase.wantSegments) {
					t.Fatalf("segments = %v, %v; want %v", segments, err, testCase.wantSegments)
				}
				return
			}
			var measurement *pipelinerefusal.Measurement
			if !errors.Is(err, ErrTooLarge) || !errors.As(err, &measurement) ||
				measurement.Dimension != testCase.dimension ||
				measurement.Observed != testCase.observed ||
				measurement.Limit != testCase.limit {
				t.Fatalf("refusal = %v, measurement=%+v", err, measurement)
			}
		})
	}
	full := make([]Member, MaxPartitions)
	for index := range full {
		full[index] = declared(1)
	}
	segments, err := packSegments(full)
	if err != nil || !slices.Equal(segments, []int{MaxPartitions}) {
		t.Fatalf("member-count packing = %v, %v", segments, err)
	}
}

func TestValidateSuperRootMaximumShapeExactAndOneOver(t *testing.T) {
	repository := "example/superroot-maximum"
	sourceDigest := "sha256:" + strings.Repeat("a", 64)
	policy := Policy{
		Schema: PolicySchema, Name: "go-source", Version: "1.0.0",
		IncludeSuffixes: []string{".go"},
	}
	policyDigest, err := PolicyDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := GenerationDigest(repository, sourceDigest, policyDigest)
	if err != nil {
		t.Fatal(err)
	}
	maximum := SuperRoot{
		Schema: SuperRootSchema, Repository: repository,
		SourceGenerationDigest: sourceDigest, RevisionCount: 8,
		Policy: policy, PolicyDigest: policyDigest,
		PartitionPolicy: frozenPolicy(), AggregatePolicy: frozenAggregatePolicy(),
		GenerationDigest: generation, Segments: []SegmentEntry{},
	}
	perSegmentMembers := MaxAggregateMembers / MaxSegments
	for ordinal := 0; ordinal < MaxSegments; ordinal++ {
		base := bitPrefix([]byte{byte(ordinal << 4)}, 4)
		entry := SegmentEntry{
			Ordinal: ordinal, Count: MaxSegments,
			Directory:      segmentDirectoryName(ordinal),
			ManifestDigest: "sha256:" + strings.Repeat("b", 64),
			MemberCount:    perSegmentMembers,
			FirstPrefix:    base + "0", LastPrefix: base + "1",
			BlobCount:          perSegmentMembers * MaxBlobsPerPartition,
			PlacementCount:     perSegmentMembers * MaxPlacementsPerPartition,
			DeclaredBytes:      MaxGenerationBlobBytes,
			EncodedMemberBytes: MaxGenerationContentBytes,
		}
		maximum.Segments = append(maximum.Segments, entry)
		maximum.MemberCount += entry.MemberCount
		maximum.BlobCount += entry.BlobCount
		maximum.PlacementCount += entry.PlacementCount
		maximum.DeclaredBytes += entry.DeclaredBytes
		maximum.EncodedMemberBytes += entry.EncodedMemberBytes
	}
	if maximum.MemberCount != MaxAggregateMembers ||
		maximum.BlobCount != MaxAggregateBlobs ||
		maximum.PlacementCount != MaxAggregatePlacements ||
		maximum.DeclaredBytes != MaxAggregateDeclaredBytes ||
		maximum.EncodedMemberBytes != MaxAggregateEncodedBytes {
		t.Fatalf("maximum shape totals = %+v", maximum)
	}
	maximum.Digest, err = SuperRootDigest(maximum)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSuperRoot(maximum); err != nil {
		t.Fatalf("exact maximum shape is refused: %v", err)
	}

	reseal := func(mutate func(*SuperRoot)) SuperRoot {
		crossed := cloneSuperRoot(maximum)
		mutate(&crossed)
		digest, err := SuperRootDigest(crossed)
		if err != nil {
			t.Fatal(err)
		}
		crossed.Digest = digest
		return crossed
	}
	overs := map[string]SuperRoot{
		"segment declared bytes": reseal(func(root *SuperRoot) {
			root.Segments[0].DeclaredBytes++
			root.DeclaredBytes++
		}),
		"segment encoded bytes": reseal(func(root *SuperRoot) {
			root.Segments[0].EncodedMemberBytes++
			root.EncodedMemberBytes++
		}),
		"aggregate members": reseal(func(root *SuperRoot) {
			root.Segments[0].MemberCount++
			root.MemberCount++
		}),
		"overlapping ranges": reseal(func(root *SuperRoot) {
			root.Segments[1].FirstPrefix = root.Segments[0].LastPrefix
		}),
		"unsealed digest": func() SuperRoot {
			crossed := cloneSuperRoot(maximum)
			crossed.DeclaredBytes--
			crossed.Segments[0].DeclaredBytes--
			return crossed
		}(),
	}
	for name, crossed := range overs {
		if err := ValidateSuperRoot(crossed); err == nil {
			t.Fatalf("%s: one-over shape was admitted", name)
		}
	}
	seventeen := reseal(func(root *SuperRoot) {
		extra := root.Segments[len(root.Segments)-1]
		extra.Ordinal = len(root.Segments)
		extra.Directory = segmentDirectoryName(extra.Ordinal)
		extra.FirstPrefix = "11111111"
		extra.LastPrefix = "111111111"
		root.Segments = append(root.Segments, extra)
	})
	if err := ValidateSuperRoot(seventeen); err == nil {
		t.Fatal("seventeen segments were admitted")
	}
}

func TestSuperRootStageTamperMatrix(t *testing.T) {
	spreadBlobHash(t)
	_, sourceDirectory, source := superRootFixture(t, map[string][]byte{
		"a.go": []byte("package demo\n\nconst A = 1\n"),
		"b.go": []byte("package demo\n\nconst B = 2\n"),
	})
	built, builtRoot := buildSuperRootStage(t, sourceDirectory, source)
	crafted, root := splitIntoTwoSegments(t, built, builtRoot)
	memberName0 := func(directory string) string {
		manifest, err := ReadManifest(
			filepath.Join(directory, root.Segments[0].Directory), root.Repository,
		)
		if err != nil {
			t.Fatal(err)
		}
		return manifest.Members[0].Name
	}
	tampers := map[string]func(directory string){
		"root byte flip": func(directory string) {
			path := filepath.Join(directory, SuperRootName(root.Repository))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw[len(raw)/2] ^= 0x20
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"non-canonical root": func(directory string) {
			path := filepath.Join(directory, SuperRootName(root.Repository))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append([]byte(" "), raw...), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"extra stage artifact": func(directory string) {
			if err := os.WriteFile(
				filepath.Join(directory, "extra.json"), []byte("{}\n"), 0o600,
			); err != nil {
				t.Fatal(err)
			}
		},
		"extra segment artifact": func(directory string) {
			if err := os.WriteFile(
				filepath.Join(directory, root.Segments[0].Directory, "extra.jsonl"),
				[]byte("{}\n"), 0o600,
			); err != nil {
				t.Fatal(err)
			}
		},
		"member symlink": func(directory string) {
			segment := filepath.Join(directory, root.Segments[0].Directory)
			name := memberName0(directory)
			target := filepath.Join(segment, name)
			if err := os.Rename(target, target+".aside"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target+".aside", target); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(
				target+".aside",
				filepath.Join(segment, "aside.jsonl"),
			); err != nil {
				t.Fatal(err)
			}
		},
		"cross-segment member swap": func(directory string) {
			first := filepath.Join(
				directory, root.Segments[0].Directory, memberName0(directory),
			)
			manifest, err := ReadManifest(
				filepath.Join(directory, root.Segments[1].Directory), root.Repository,
			)
			if err != nil {
				t.Fatal(err)
			}
			second := filepath.Join(
				directory, root.Segments[1].Directory, manifest.Members[0].Name,
			)
			raw, err := os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(first, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"truncated member": func(directory string) {
			path := filepath.Join(
				directory, root.Segments[0].Directory, memberName0(directory),
			)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw[:len(raw)-2], 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"missing segment": func(directory string) {
			if err := os.RemoveAll(
				filepath.Join(directory, root.Segments[1].Directory),
			); err != nil {
				t.Fatal(err)
			}
		},
		"segment manifest swap": func(directory string) {
			first := filepath.Join(
				directory, root.Segments[0].Directory, ManifestName(root.Repository),
			)
			second := filepath.Join(
				directory, root.Segments[1].Directory, ManifestName(root.Repository),
			)
			raw, err := os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(first, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, tamper := range tampers {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			copyTree(t, crafted, directory)
			if err := ValidateSuperRootStage(t.Context(), directory, root); err != nil {
				t.Fatalf("clean copy is refused: %v", err)
			}
			tamper(directory)
			if err := ValidateSuperRootStage(t.Context(), directory, root); err == nil {
				t.Fatal("tampered stage was admitted")
			}
		})
	}
}

func TestBuildSuperRootEmptySelectionAndCancellationLeaveNoRoot(t *testing.T) {
	_, sourceDirectory, source := superRootFixture(t, map[string][]byte{
		"only.rs": []byte("fn main() {}\n"),
	})
	directory := t.TempDir()
	root, err := BuildSuperRoot(t.Context(), BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: directory,
		Repository: source.Repository, Source: source,
		Policy: Policy{
			Schema: PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil || len(root.Segments) != 0 || root.MemberCount != 0 ||
		root.BlobCount != 0 || root.DeclaredBytes != 0 {
		t.Fatalf("empty super root = %+v, %v", root, err)
	}
	if err := ValidateSuperRootStage(t.Context(), directory, root); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSuperRoot(t.Context(), directory, root); err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	canceledDirectory := t.TempDir()
	if _, err := BuildSuperRoot(canceled, BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: canceledDirectory,
		Repository: source.Repository, Source: source,
		Policy: Policy{
			Schema: PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled build = %v", err)
	}
	if _, err := os.Lstat(
		filepath.Join(canceledDirectory, SuperRootName(source.Repository)),
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled build published a root: %v", err)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		source := filepath.Join(from, entry.Name())
		destination := filepath.Join(to, entry.Name())
		if entry.IsDir() {
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			copyTree(t, source, destination)
			continue
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertTreesEqual(t *testing.T, left, right string) {
	t.Helper()
	leftEntries, err := os.ReadDir(left)
	if err != nil {
		t.Fatal(err)
	}
	rightEntries, err := os.ReadDir(right)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftEntries) != len(rightEntries) {
		t.Fatalf("tree entry counts differ under %s: %d != %d",
			left, len(leftEntries), len(rightEntries))
	}
	for index, entry := range leftEntries {
		other := rightEntries[index]
		if entry.Name() != other.Name() || entry.IsDir() != other.IsDir() {
			t.Fatalf("tree entries differ: %s != %s", entry.Name(), other.Name())
		}
		if entry.IsDir() {
			assertTreesEqual(
				t, filepath.Join(left, entry.Name()), filepath.Join(right, entry.Name()),
			)
			continue
		}
		leftRaw, err := os.ReadFile(filepath.Join(left, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		rightRaw, err := os.ReadFile(filepath.Join(right, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if string(leftRaw) != string(rightRaw) {
			t.Fatalf("artifact %s differs", entry.Name())
		}
	}
}

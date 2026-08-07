package observationpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

func buildInventorySuperPlanV2(
	t *testing.T, repository, commit, name string,
) (string, *sourcepartition.SuperPlan, sourcepartition.SuperRoot) {
	t.Helper()
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repository, sourceDirectory, name,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	planDirectory := t.TempDir()
	root, err := sourcepartition.BuildSuperRoot(t.Context(), sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: planDirectory,
		Repository: name, Source: source,
		Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourcepartition.OpenSuperRoot(t.Context(), planDirectory, root)
	if err != nil {
		t.Fatal(err)
	}
	return sourceDirectory, plan, root
}

func TestInventoryV2BuildKeyedReadRestartAndReuse(t *testing.T) {
	repository, commitA := observationFixture(t, map[string][]byte{
		"a.go":      []byte("package demo\nconst A = 1\n"),
		"copy/a.go": []byte("package demo\nconst A = 1\n"),
		"b.go":      []byte("package demo\nconst B = 2\n"),
		"bad.go":    []byte("package demo\nfunc broken(\n"),
	})
	sourceA, planA, sourceRootA := buildInventorySuperPlanV2(t, repository, commitA, "example/inventory-v2")
	outputA := t.TempDir()
	var metricsA InventoryMetricsV2
	rootA, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: outputA, RepositoryDirectory: repository,
		Plan: planA, Metrics: &metricsA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rootA.SourcePartitionRootDigest != sourceRootA.Digest || rootA.RecordCount != 3 ||
		rootA.ObservedCount != 2 || rootA.UnsupportedCount != 1 || metricsA.ReadBlobs != 3 {
		t.Fatalf("A root/metrics = %+v / %+v", rootA, metricsA)
	}
	if err := ValidateInventoryStageV2(t.Context(), outputA, rootA); err != nil {
		t.Fatal(err)
	}
	keyed, err := OpenInventoryV2Keyed(outputA, rootA.Repository)
	if err != nil {
		t.Fatal(err)
	}
	var observedID, unsupportedID string
	for segment := range sourceRootA.Segments {
		manifest, err := planA.SegmentManifest(segment)
		if err != nil {
			t.Fatal(err)
		}
		for ordinal := range manifest.Members {
			_, err := planA.ReadSegmentPartition(
				t.Context(), repository, segment, ordinal,
				func(record sourcepartition.BlobRecord, content []byte) error {
					if strings.Contains(string(content), "broken") {
						unsupportedID = record.ObjectID
					} else if observedID == "" {
						observedID = record.ObjectID
					}
					return nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	observed, observation, err := keyed.Lookup(t.Context(), observedID)
	if err != nil || observed.State != "observed" || observation == nil || observation.ContentDigest != observed.ContentDigest {
		t.Fatalf("observed lookup = %+v %+v %v", observed, observation, err)
	}
	unsupported, observation, err := keyed.Lookup(t.Context(), unsupportedID)
	if err != nil || unsupported.State != "unsupported" || observation != nil || unsupported.Reason != "parse_error" {
		t.Fatalf("unsupported lookup = %+v %+v %v", unsupported, observation, err)
	}

	// A completed output is a control-only restart no-op.
	var restartMetrics InventoryMetricsV2
	restarted, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: outputA, RepositoryDirectory: repository,
		Plan: planA, Metrics: &restartMetrics,
	})
	if err != nil || restarted.Digest != rootA.Digest || restartMetrics != (InventoryMetricsV2{}) {
		t.Fatalf("restart = %+v / %+v / %v", restarted, restartMetrics, err)
	}

	if err := os.WriteFile(filepath.Join(repository, "b.go"), []byte("package demo\nconst B = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repository, "add", "b.go")
	runObservationGit(t, repository, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repository, "rev-parse", "HEAD"))
	_, planB, _ := buildInventorySuperPlanV2(t, repository, commitB, "example/inventory-v2")
	outputB := t.TempDir()
	var metricsB InventoryMetricsV2
	rootB, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: outputB, RepositoryDirectory: repository,
		Plan: planB, PriorDirectory: outputA, Metrics: &metricsB,
	})
	if err != nil || rootB.Digest == rootA.Digest || metricsB.ReusedMembers == 0 || metricsB.ReadBlobs == 0 {
		t.Fatalf("small delta = %+v / %+v / %v", rootB, metricsB, err)
	}

	// Returning to A with A as the validated prior physically reuses every
	// unchanged member and reproduces the exact root identity.
	_, planReturned, _ := buildInventorySuperPlanV2(t, repository, commitA, "example/inventory-v2")
	outputReturned := t.TempDir()
	var returnedMetrics InventoryMetricsV2
	returned, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: outputReturned, RepositoryDirectory: repository,
		Plan: planReturned, PriorDirectory: outputA, Metrics: &returnedMetrics,
	})
	if err != nil || returned.Digest != rootA.Digest || returnedMetrics.ReusedMembers != rootA.MemberCount ||
		returnedMetrics.ReadBlobs != 0 {
		t.Fatalf("A-B-A = %+v / %+v / %v", returned, returnedMetrics, err)
	}
	_ = sourceA
}

func TestInventoryV2RefusesCorruptColdAndUnknownArtifacts(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-v2-corrupt")
	output := t.TempDir()
	root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: output, RepositoryDirectory: repository, Plan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	segmentDirectory := filepath.Join(output, root.Segments[0].Directory)
	if err := os.WriteFile(filepath.Join(segmentDirectory, inventoryMemberNameV2(0)), []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInventoryStageV2(t.Context(), output, root); err == nil {
		t.Fatal("corrupt cold member was admitted")
	}
	keyed, err := OpenInventoryV2Keyed(output, root.Repository)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := plan.SegmentManifest(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := keyed.Lookup(t.Context(), manifest.Members[0].FirstObjectID); err == nil {
		t.Fatal("corrupt keyed member was admitted")
	}
	if err := os.WriteFile(filepath.Join(output, "extra"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInventoryStageV2(t.Context(), output, root); err == nil {
		t.Fatal("unknown root artifact was admitted")
	}
}

func TestInventoryV2KeyedLookupDoesNotOpenSiblingSegment(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
		"b.go": []byte("package demo\nconst B = 2\n"),
		"c.go": []byte("package demo\nconst C = 3\n"),
		"d.go": []byte("package demo\nconst D = 4\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-v2-sparse")
	base := t.TempDir()
	root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: base, RepositoryDirectory: repository, Plan: plan,
	})
	if err != nil || root.MemberCount < 2 {
		t.Fatalf("base stage = %+v, %v", root, err)
	}
	crafted, craftedRoot, objectID := splitInventoryStageV2(t, base, root)
	if err := ValidateInventoryStageV2(t.Context(), crafted, craftedRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(crafted, craftedRoot.Segments[1].Directory, "segment.json"),
		[]byte("corrupt\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	keyed, err := OpenInventoryV2Keyed(crafted, craftedRoot.Repository)
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := keyed.Lookup(t.Context(), objectID)
	if err != nil || record.ObjectID != objectID {
		t.Fatalf("sparse keyed lookup = %+v, %v", record, err)
	}
	if err := ValidateInventoryStageV2(t.Context(), crafted, craftedRoot); err == nil {
		t.Fatal("complete validation ignored corrupt sibling segment")
	}
}

func splitInventoryStageV2(
	t *testing.T, directory string, root InventoryRootV2,
) (string, InventoryRootV2, string) {
	t.Helper()
	original, err := readInventorySegmentV2(directory, root, root.Segments[0])
	if err != nil {
		t.Fatal(err)
	}
	pivot := len(original.Members) / 2
	if pivot == 0 {
		t.Fatal("inventory segment is not splittable")
	}
	groups := [][]InventoryMemberV2{original.Members[:pivot], original.Members[pivot:]}
	crafted := t.TempDir()
	next := root
	next.Segments = []InventorySegmentEntryV2{}
	next.MemberCount, next.RecordCount, next.ObservedCount, next.UnsupportedCount = 0, 0, 0, 0
	next.EncodedMemberBytes, next.ObservationBytes, next.ObservationCount = 0, 0, 0
	next.OperationReceipt = emptyOperationReceipt()
	firstObject := ""
	for ordinal, group := range groups {
		segmentDirectory := filepath.Join(crafted, inventorySegmentDirectoryV2(ordinal))
		if err := os.MkdirAll(filepath.Join(segmentDirectory, "objects"), 0o700); err != nil {
			t.Fatal(err)
		}
		segment := InventorySegmentV2{
			Schema: InventorySegmentSchemaV2, Repository: root.Repository,
			GenerationDigest: root.GenerationDigest, SourcePartitionRootDigest: root.SourcePartitionRootDigest,
			ObservationPolicyDigest: root.ObservationPolicyDigest, Ordinal: ordinal, Count: len(groups),
			Members: []InventoryMemberV2{}, OperationReceipt: emptyOperationReceipt(),
		}
		objects := map[string]bool{}
		receipt := newReceiptAccumulator()
		for local, member := range group {
			sourcePath := filepath.Join(directory, root.Segments[0].Directory, member.Name)
			destination := filepath.Join(segmentDirectory, inventoryMemberNameV2(local))
			if err := os.Link(sourcePath, destination); err != nil {
				t.Fatal(err)
			}
			_, records, err := readMember(t.Context(), filepath.Join(directory, root.Segments[0].Directory), member.Ordinal, member.Count)
			if err != nil {
				t.Fatal(err)
			}
			member.Ordinal, member.Count, member.Name = local, len(group), inventoryMemberNameV2(local)
			segment.Members = append(segment.Members, member)
			segment.RecordCount += member.RecordCount
			segment.ObservedCount += member.ObservedCount
			segment.UnsupportedCount += member.UnsupportedCount
			segment.EncodedMemberBytes += member.ContentBytes
			for _, record := range records {
				if firstObject == "" && ordinal == 0 {
					firstObject = record.ObjectID
				}
				if err := receipt.add(record); err != nil {
					t.Fatal(err)
				}
				if record.State == "observed" && !objects[record.ObservationName] {
					objects[record.ObservationName] = true
					from := filepath.Join(directory, root.Segments[0].Directory, record.ObservationName)
					to := filepath.Join(segmentDirectory, record.ObservationName)
					if err := os.Link(from, to); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		segment.OperationReceipt = *receipt.value()
		segment.ObservationCount = len(objects)
		for name := range objects {
			info, err := os.Stat(filepath.Join(segmentDirectory, name))
			if err != nil {
				t.Fatal(err)
			}
			segment.ObservationBytes += info.Size()
		}
		segment.Digest, err = inventorySegmentDigestV2(segment)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeExclusiveCanonical(filepath.Join(segmentDirectory, "segment.json"), segment); err != nil {
			t.Fatal(err)
		}
		entry := inventorySegmentEntryV2(segment)
		next.Segments = append(next.Segments, entry)
		next.MemberCount += entry.MemberCount
		next.RecordCount += entry.RecordCount
		next.ObservedCount += entry.ObservedCount
		next.UnsupportedCount += entry.UnsupportedCount
		next.EncodedMemberBytes += entry.EncodedMemberBytes
		next.ObservationCount += entry.ObservationCount
		next.ObservationBytes += entry.ObservationBytes
		mergeOperationReceipt(&next.OperationReceipt, segment.OperationReceipt)
	}
	next.Digest, err = InventoryRootDigestV2(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveCanonical(filepath.Join(crafted, InventoryRootNameV2), next); err != nil {
		t.Fatal(err)
	}
	return crafted, next, firstObject
}

func TestInventoryV2EmptyAndAggregateRecordBound(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{"only.txt": []byte("none\n")})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-v2-empty")
	output := t.TempDir()
	root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: output, RepositoryDirectory: repository, Plan: plan,
	})
	if err != nil || root.RecordCount != 0 || len(root.Segments) != 0 {
		t.Fatalf("empty root = %+v, %v", root, err)
	}
	if err := ValidateInventoryStageV2(t.Context(), output, root); err != nil {
		t.Fatal(err)
	}

	members := make([]inventorySourceMemberV2, MaxInventorySegmentsV2+1)
	for index := range members {
		members[index].member.BlobCount = MaxInventorySegmentRecords
	}
	_, err = packInventoryMembersV2(members)
	var refusal *pipelinerefusal.Measurement
	if !errors.As(err, &refusal) || refusal.Dimension != pipelinerefusal.DimensionAggregateSegments ||
		refusal.Observed <= refusal.Limit {
		t.Fatalf("one-over aggregate refusal = %+v, %v", refusal, err)
	}
}

func TestInventoryV2InterruptedWorkRebuildsAndConcurrentWritersConverge(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
		"b.go": []byte("package demo\nconst B = 2\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-v2-restart")
	output := t.TempDir()
	root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: output, RepositoryDirectory: repository, Plan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(output, InventoryRootNameV2)); err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(output, root.Segments[0].Directory)
	if err := os.WriteFile(filepath.Join(segment, inventoryMemberNameV2(0)), []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var rebuiltMetrics InventoryMetricsV2
	rebuilt, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: output, RepositoryDirectory: repository, Plan: plan,
		Metrics: &rebuiltMetrics,
	})
	if err != nil || rebuilt.Digest != root.Digest || rebuiltMetrics.ReadBlobs == 0 {
		t.Fatalf("invalid restart rebuild = %+v / %+v / %v", rebuilt, rebuiltMetrics, err)
	}

	concurrentOutput := t.TempDir()
	type result struct {
		root InventoryRootV2
		err  error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			value, buildErr := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
				OutputDirectory: concurrentOutput, RepositoryDirectory: repository, Plan: plan,
			})
			results <- result{root: value, err: buildErr}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.root.Digest != second.root.Digest || first.root.Digest != root.Digest {
		t.Fatalf("concurrent builds = %+v / %+v", first, second)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	canceledOutput := t.TempDir()
	if _, err := BuildInventoryStageV2(canceled, InventoryBuildRequestV2{
		OutputDirectory: canceledOutput, RepositoryDirectory: repository, Plan: plan,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled build = %v", err)
	}
	if _, err := os.Stat(filepath.Join(canceledOutput, InventoryRootNameV2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled build exposed a root: %v", err)
	}
}

func TestInventoryV2CachePinsAndWarmLeaseDoesNotReread(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-v2-cache")
	output := t.TempDir()
	root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: output, RepositoryDirectory: repository, Plan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := &InventoryCacheV2{}
	first, err := cache.Acquire(t.Context(), output, root)
	if err != nil || !cache.Pinned(output, root.Digest) {
		t.Fatalf("first cache lease = %+v, pinned=%t, %v", first, cache.Pinned(output, root.Digest), err)
	}
	member := filepath.Join(output, root.Segments[0].Directory, inventoryMemberNameV2(0))
	if err := os.WriteFile(member, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	warm, err := cache.Acquire(t.Context(), output, root)
	if err != nil || warm.Publication().Root().Digest != root.Digest {
		t.Fatalf("warm cache reread immutable bytes: %v", err)
	}
	warm.Release()
	first.Release()
	if cache.Pinned(output, root.Digest) {
		t.Fatal("released inventory cache entry remained pinned")
	}
	if _, err := (&InventoryCacheV2{}).Acquire(t.Context(), output, root); err == nil {
		t.Fatal("cold cache admitted corrupt member")
	}
}

func TestInventoryV2CacheProvisionalLeasePinsLifecycleIdentity(t *testing.T) {
	digestValue := "sha256:" + strings.Repeat("a", 64)
	repository := "example/inventory-v2-provisional-cache"
	generation := "sha256:" + strings.Repeat("b", 64)
	directory := filepath.Join(t.TempDir(), "inventory")
	key := directory + "\x00" + digestValue
	entry := &inventoryCacheEntryV2{
		repository: repository, generation: generation,
		leases: 1, ready: make(chan struct{}),
	}
	cache := &InventoryCacheV2{entries: map[string]*inventoryCacheEntryV2{key: entry}}
	if !cache.Pinned(directory, digestValue) || !cache.PinnedGeneration(repository, generation) {
		t.Fatal("provisional cache lease was not a lifecycle pin")
	}
	lease := &InventoryLeaseV2{cache: cache, key: key, entry: entry}
	lease.Release()
	if cache.Pinned(directory, digestValue) || cache.PinnedGeneration(repository, generation) {
		t.Fatal("released provisional cache lease remained pinned")
	}
}

func TestInventoryV2RefusesCorruptPriorBeforeReuse(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-v2-prior")
	prior := t.TempDir()
	root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: prior, RepositoryDirectory: repository, Plan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(prior, root.Segments[0].Directory, inventoryMemberNameV2(0)),
		[]byte("corrupt\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if _, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: output, RepositoryDirectory: repository, Plan: plan,
		PriorDirectory: prior,
	}); err == nil {
		t.Fatal("corrupt prior stage was trusted for reuse")
	}
	if _, err := os.Stat(filepath.Join(output, InventoryRootNameV2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt prior exposed a root: %v", err)
	}
}

func TestInventoryV2RootAdmitsExactRecordMaximumAndRefusesOneOver(t *testing.T) {
	digestValue := "sha256:" + strings.Repeat("a", 64)
	root := InventoryRootV2{
		Schema: InventoryRootSchemaV2, Repository: "example/maximum", Language: LanguagePack,
		SourceGenerationDigest: digestValue, SourcePartitionGeneration: digestValue,
		SourcePartitionRootDigest: digestValue, PartitionPolicyDigest: digestValue,
		ObservationPolicyDigest: sourceobservation.PolicyDigest(), InventoryPolicy: frozenInventoryPolicyV2(),
		InventoryPolicyDigest: inventoryPolicyDigestV2(), GenerationDigest: digestValue,
		Segments: []InventorySegmentEntryV2{}, OperationReceipt: OperationReceipt{
			Schema: ReceiptSchema, InputBlobs: MaxInventoryRecordsV2,
			SourceBlobReads: MaxInventoryRecordsV2, UnsupportedBlobs: MaxInventoryRecordsV2,
			UnsupportedReasons: []ReasonCount{{Reason: "source_limit", Count: MaxInventoryRecordsV2}},
		},
	}
	root.GenerationDigest = inventoryGenerationDigestFieldsV2(
		root.Repository, root.SourceGenerationDigest, root.SourcePartitionGeneration,
		root.SourcePartitionRootDigest, root.PartitionPolicyDigest,
	)
	for ordinal := 0; ordinal < MaxInventorySegmentsV2; ordinal++ {
		prefix := fmt.Sprintf("%04b", ordinal)
		root.Segments = append(root.Segments, InventorySegmentEntryV2{
			Ordinal: ordinal, Count: MaxInventorySegmentsV2,
			Directory: inventorySegmentDirectoryV2(ordinal), SegmentDigest: digestValue,
			MemberCount: 1, FirstPrefix: prefix, LastPrefix: prefix,
			RecordCount: MaxInventorySegmentRecords, UnsupportedCount: MaxInventorySegmentRecords,
			EncodedMemberBytes: 1,
		})
		root.MemberCount++
		root.RecordCount += MaxInventorySegmentRecords
		root.UnsupportedCount += MaxInventorySegmentRecords
		root.EncodedMemberBytes++
	}
	root.Digest, _ = InventoryRootDigestV2(root)
	if err := ValidateInventoryRootV2(root); err != nil {
		t.Fatalf("exact maximum root = %v", err)
	}
	oneOver := root
	oneOver.RecordCount++
	if err := ValidateInventoryRootV2(oneOver); err == nil {
		t.Fatal("one-over root record count was admitted")
	}
}

package retentionstatus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestFilesystemIdentityCapAndSentinel(t *testing.T) {
	tests := []struct {
		name      string
		component store.RetentionComponent
		prepare   func(*testing.T, string, int)
		scan      func(context.Context, string, store.RetentionComponentRequest, *collectionBudget) fileScan
	}{
		{"candidate", store.RetentionCandidateFiles, prepareCandidateFiles, scanCandidateFiles},
		{"focused", store.RetentionFocusedFiles, prepareFocusedFiles, scanFocusedFiles},
		{"resolver", store.RetentionResolverFiles, prepareResolverFiles, scanResolverFiles},
		{"caller", store.RetentionCallerArtifacts, prepareCallerFiles, scanCallerFiles},
	}
	for _, test := range tests {
		for _, count := range []int{0, 1, 77, 78, 79} {
			t.Run(fmt.Sprintf("%s/%d", test.name, count), func(t *testing.T) {
				root := t.TempDir()
				test.prepare(t, root, count)
				request := store.RetentionComponentRequest{
					Component: test.component, ReportedIdentities: 78, ScanIdentities: 79,
				}
				budget := newCollectionBudget(defaultCollectorPolicy(), collectorHooks{})
				scan := test.scan(t.Context(), root, request, budget)
				result := scan.componentResult(test.component)
				wantScanned := count
				wantReported := count
				wantCompleteness := Exact
				if count == 79 {
					wantReported = 78
					wantCompleteness = LowerBound
				}
				if scan.err != nil || result.Err != nil {
					t.Fatalf("scan error = %v, result error = %v", scan.err, result.Err)
				}
				if result.ScannedIdentities != wantScanned || result.Count.Value == nil ||
					*result.Count.Value != int64(wantReported) ||
					result.Count.Completeness != wantCompleteness ||
					result.Truncated != (count == 79) {
					t.Fatalf("count result = %+v, want scanned/reported/completeness %d/%d/%s", result, wantScanned, wantReported, wantCompleteness)
				}
				apparent := findByteMetric(t, result, ApparentFile)
				if apparent.Value == nil || *apparent.Value != int64(wantReported) ||
					apparent.Completeness != wantCompleteness {
					t.Fatalf("apparent metric = %+v, want %d/%s", apparent, wantReported, wantCompleteness)
				}
			})
		}
	}
}

func TestFilesystemMissingRootIsExactZero(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	request := store.RetentionComponentRequest{
		Component:          store.RetentionCandidateFiles,
		ReportedIdentities: 78, ScanIdentities: 79,
	}
	scan := scanCandidateFiles(
		t.Context(), root, request,
		newCollectionBudget(defaultCollectorPolicy(), collectorHooks{}),
	)
	result := scan.componentResult(request.Component)
	if result.Err != nil || result.Count.Value == nil || *result.Count.Value != 0 ||
		result.Count.Completeness != Exact {
		t.Fatalf("missing-root result = %+v, want exact zero", result)
	}
}

func TestCanonicalRootRejectsNestedPath(t *testing.T) {
	root := t.TempDir()
	active := 0
	budget := newCollectionBudget(defaultCollectorPolicy(), collectorHooks{
		descriptorDelta: func(delta int) { active += delta },
	})
	anchor, err := openStableRoot(t.Context(), root, budget)
	if err != nil {
		t.Fatal(err)
	}
	_, openErr := openCanonicalRootAt(t.Context(), anchor, "resolver-catalogs/nested")
	closeErr := anchor.close()
	if openErr == nil || closeErr != nil || active != 0 {
		t.Fatalf("nested canonical root error/close/active = %v/%v/%d", openErr, closeErr, active)
	}
}

func TestFilesystemPartialBudgetsNeverClaimExactZero(t *testing.T) {
	t.Run("zero observations", func(t *testing.T) {
		root := t.TempDir()
		for index := range 3 {
			writeTestFile(t, filepath.Join(root, fmt.Sprintf("foreign-%d", index)), 1)
		}
		policy := defaultCollectorPolicy()
		policy.componentObservations[store.RetentionCandidateFiles] = 2
		policy.aggregateObservations = 2
		request := store.RetentionComponentRequest{
			Component:          store.RetentionCandidateFiles,
			ReportedIdentities: 78, ScanIdentities: 79,
		}
		result := scanCandidateFiles(
			t.Context(), root, request,
			newCollectionBudget(policy, collectorHooks{}),
		).componentResult(request.Component)
		if result.Count.Value != nil || result.Count.Completeness != Unavailable || result.Err == nil {
			t.Fatalf("zero partial result = %+v, want unavailable", result)
		}
	})

	t.Run("positive stat partial", func(t *testing.T) {
		root := t.TempDir()
		prepareCandidateFiles(t, root, 2)
		policy := defaultCollectorPolicy()
		policy.readDirBatch = 2
		// Root anchor (4), stream fence (3), one directory-read fallback
		// allowance, and one stable entry (2).
		// Names-only iteration costs no per-entry stats; the second entry stops.
		policy.aggregateStats = 10
		request := store.RetentionComponentRequest{
			Component:          store.RetentionCandidateFiles,
			ReportedIdentities: 78, ScanIdentities: 79,
		}
		result := scanCandidateFiles(
			t.Context(), root, request,
			newCollectionBudget(policy, collectorHooks{}),
		).componentResult(request.Component)
		if result.Count.Value == nil || *result.Count.Value != 1 ||
			result.Count.Completeness != LowerBound || result.Truncated ||
			len(result.Diagnostics) != 1 {
			t.Fatalf("positive partial result = %+v, want non-sentinel lower bound", result)
		}
	})
}

func TestFilesystemForeignEntriesConsumeAggregateObservationBudget(t *testing.T) {
	candidateRoot := t.TempDir()
	focusedRoot := t.TempDir()
	writeTestFile(t, filepath.Join(candidateRoot, "foreign"), 1)
	prepareFocusedFiles(t, focusedRoot, 1)
	policy := defaultCollectorPolicy()
	policy.aggregateObservations = 1
	budget := newCollectionBudget(policy, collectorHooks{})
	candidateRequest := store.RetentionComponentRequest{
		Component:          store.RetentionCandidateFiles,
		ReportedIdentities: 78, ScanIdentities: 79,
	}
	if result := scanCandidateFiles(
		t.Context(), candidateRoot, candidateRequest, budget,
	).componentResult(candidateRequest.Component); result.Count.Completeness != Unavailable || result.Err == nil {
		t.Fatalf("foreign-budget result = %+v, want unavailable without an EOF observation", result)
	}
	focusedRequest := store.RetentionComponentRequest{
		Component:          store.RetentionFocusedFiles,
		ReportedIdentities: 78, ScanIdentities: 79,
	}
	result := scanFocusedFiles(
		t.Context(), focusedRoot, focusedRequest, budget,
	).componentResult(focusedRequest.Component)
	if result.Count.Completeness != Unavailable || result.Err == nil {
		t.Fatalf("aggregate-exhausted result = %+v, want unavailable", result)
	}
}

func TestFilesystemChargesWholeMaterializedBatchBeforeIdentitySentinel(t *testing.T) {
	root := t.TempDir()
	prepareCandidateFiles(t, root, 100)
	request := store.RetentionComponentRequest{
		Component:          store.RetentionCandidateFiles,
		ReportedIdentities: 78, ScanIdentities: 79,
	}
	budget := newCollectionBudget(defaultCollectorPolicy(), collectorHooks{})
	result := scanCandidateFiles(
		t.Context(), root, request, budget,
	).componentResult(request.Component)
	if !result.Truncated || result.ScannedIdentities != 79 ||
		budget.componentObservations[request.Component] != 100 ||
		budget.aggregateObservations != 100 {
		t.Fatalf(
			"sentinel result/observations = %+v %d/%d, want truncated 79 and 100/100",
			result, budget.componentObservations[request.Component], budget.aggregateObservations,
		)
	}
}

func TestFilesystemReadBatchHonorsRemainingObservationBudget(t *testing.T) {
	tests := []struct {
		name             string
		componentLimit   int
		aggregateLimit   int
		wantObservations int
	}{
		{name: "component bound", componentLimit: 3, aggregateLimit: 10, wantObservations: 3},
		{name: "aggregate bound", componentLimit: 10, aggregateLimit: 2, wantObservations: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			prepareCandidateFiles(t, root, 10)
			request := store.RetentionComponentRequest{
				Component:          store.RetentionCandidateFiles,
				ReportedIdentities: 78, ScanIdentities: 79,
			}
			policy := defaultCollectorPolicy()
			policy.componentObservations[request.Component] = test.componentLimit
			policy.aggregateObservations = test.aggregateLimit
			budget := newCollectionBudget(policy, collectorHooks{})
			result := scanCandidateFiles(
				t.Context(), root, request, budget,
			).componentResult(request.Component)
			if budget.componentObservations[request.Component] != test.wantObservations ||
				budget.aggregateObservations != test.wantObservations ||
				result.ScannedIdentities != test.wantObservations ||
				result.Count.Value == nil || *result.Count.Value != int64(test.wantObservations) ||
				result.Count.Completeness != LowerBound || result.Truncated ||
				len(result.Diagnostics) != 1 {
				t.Fatalf(
					"bounded batch result/observations = %+v %d/%d, want lower %d and %d/%d",
					result, budget.componentObservations[request.Component],
					budget.aggregateObservations, test.wantObservations,
					test.wantObservations, test.wantObservations,
				)
			}
		})
	}
}

func TestFilesystemNamesOnlyBatchDoesNotStatForeignEntries(t *testing.T) {
	root := t.TempDir()
	for index := range 10 {
		writeTestFile(t, filepath.Join(root, fmt.Sprintf("foreign-%02d", index)), 1)
	}
	request := store.RetentionComponentRequest{
		Component:          store.RetentionCandidateFiles,
		ReportedIdentities: 78, ScanIdentities: 79,
	}
	policy := defaultCollectorPolicy()
	// Root anchor (4), stream fence (3), two directory-read fallback allowances,
	// stream close (1), and root close (1).
	// The ten foreign names consume observations but no stat operations.
	policy.aggregateStats = 11
	budget := newCollectionBudget(policy, collectorHooks{})
	result := scanCandidateFiles(
		t.Context(), root, request, budget,
	).componentResult(request.Component)
	if budget.stats != policy.aggregateStats ||
		budget.componentObservations[request.Component] != 10 ||
		result.Count.Completeness != Exact || result.Count.Value == nil ||
		*result.Count.Value != 0 || result.Err != nil {
		t.Fatalf(
			"names-only foreign result/stats/observations = %+v/%d/%d",
			result, budget.stats, budget.componentObservations[request.Component],
		)
	}
}

func TestResolverStageCountsFilesButNotDirectoryInode(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, ".phebs-resolver-catalog-stage-fixture")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(stage, "member-000.ndjson"), 1)
	writeTestFile(t, filepath.Join(stage, "manifest.json"), 1)
	request := store.RetentionComponentRequest{
		Component:          store.RetentionResolverFiles,
		ReportedIdentities: 78, ScanIdentities: 79,
	}
	result := scanResolverFiles(
		t.Context(), root, request,
		newCollectionBudget(defaultCollectorPolicy(), collectorHooks{}),
	).componentResult(request.Component)
	if result.Count.Value == nil || *result.Count.Value != 2 ||
		findMetricValue(t, result, ApparentFile) != 2 {
		t.Fatalf("resolver stage result = %+v, want two file identities", result)
	}
}

func TestFilesystemSymlinkSpecialAndIgnoredCallerControls(t *testing.T) {
	t.Run("owned symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		writeTestFile(t, target, 1)
		name := candidateid.ManifestName("github.com/acme/service")
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		request := store.RetentionComponentRequest{
			Component:          store.RetentionCandidateFiles,
			ReportedIdentities: 78, ScanIdentities: 79,
		}
		result := scanCandidateFiles(
			t.Context(), root, request,
			newCollectionBudget(defaultCollectorPolicy(), collectorHooks{}),
		).componentResult(request.Component)
		if result.Count.Completeness != Unavailable || result.Err == nil {
			t.Fatalf("symlink result = %+v, want unavailable", result)
		}
	})

	t.Run("owned directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(
			filepath.Join(root, candidateid.ManifestName("github.com/acme/service")), 0o700,
		); err != nil {
			t.Fatal(err)
		}
		request := store.RetentionComponentRequest{
			Component:          store.RetentionCandidateFiles,
			ReportedIdentities: 78, ScanIdentities: 79,
		}
		result := scanCandidateFiles(
			t.Context(), root, request,
			newCollectionBudget(defaultCollectorPolicy(), collectorHooks{}),
		).componentResult(request.Component)
		if result.Count.Completeness != Unavailable || result.Err == nil {
			t.Fatalf("special result = %+v, want unavailable", result)
		}
	})

	t.Run("caller controls ignored without stat", func(t *testing.T) {
		root := t.TempDir()
		repositoryDirectory := callerpublicationid.RepositoryDirectory("github.com/acme/service")
		directory := filepath.Join(root, repositoryDirectory)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{
			callerpublicationid.PublishingName,
			callerpublicationid.DeletingName,
			callerleafid.StagePrefix + strings.Repeat("a", 32),
		} {
			if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		request := store.RetentionComponentRequest{
			Component:          store.RetentionCallerArtifacts,
			ReportedIdentities: 78, ScanIdentities: 79,
		}
		result := scanCallerFiles(
			t.Context(), root, request,
			newCollectionBudget(defaultCollectorPolicy(), collectorHooks{}),
		).componentResult(request.Component)
		if result.Err != nil || result.Count.Value == nil || *result.Count.Value != 0 ||
			result.Count.Completeness != Exact {
			t.Fatalf("ignored-control result = %+v, want exact zero", result)
		}
	})
}

func TestFilesystemCloseFenceAndDescriptors(t *testing.T) {
	for _, count := range []int{1, 79} {
		t.Run(fmt.Sprintf("count-%d", count), func(t *testing.T) {
			root := t.TempDir()
			prepareCandidateFiles(t, root, count)
			active, peak := 0, 0
			hooks := collectorHooks{descriptorDelta: func(delta int) {
				active += delta
				peak = max(peak, active)
			}}
			request := store.RetentionComponentRequest{
				Component:          store.RetentionCandidateFiles,
				ReportedIdentities: 78, ScanIdentities: 79,
			}
			_ = scanCandidateFiles(
				t.Context(), root, request,
				newCollectionBudget(defaultCollectorPolicy(), hooks),
			)
			if active != 0 || peak > maxRetainedDescriptors {
				t.Fatalf("descriptors active/peak = %d/%d, want 0/<=%d", active, peak, maxRetainedDescriptors)
			}
		})
	}

	for _, test := range []struct {
		name    string
		prepare func(*testing.T, string, int)
		scan    func(context.Context, string, store.RetentionComponentRequest, *collectionBudget) fileScan
		kind    store.RetentionComponent
	}{
		{"nested resolver stage", func(t *testing.T, root string, _ int) {
			stage := filepath.Join(root, ".phebs-resolver-catalog-stage-descriptor")
			if err := os.Mkdir(stage, 0o700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(stage, "member.ndjson"), 1)
		}, scanResolverFiles, store.RetentionResolverFiles},
		{"nested caller repository", prepareCallerFiles, scanCallerFiles, store.RetentionCallerArtifacts},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.prepare(t, root, 1)
			active, peak := 0, 0
			hooks := collectorHooks{descriptorDelta: func(delta int) {
				active += delta
				peak = max(peak, active)
			}}
			request := store.RetentionComponentRequest{
				Component: test.kind, ReportedIdentities: 78, ScanIdentities: 79,
			}
			result := test.scan(
				t.Context(), root, request,
				newCollectionBudget(defaultCollectorPolicy(), hooks),
			).componentResult(request.Component)
			if result.Err != nil || active != 0 || peak > maxRetainedDescriptors {
				t.Fatalf("nested descriptors result/active/peak = %+v %d/%d", result, active, peak)
			}
		})
	}

	t.Run("path replacement at close", func(t *testing.T) {
		root := t.TempDir()
		prepareCandidateFiles(t, root, 1)
		replaced := false
		hooks := collectorHooks{beforeDirectoryClose: func(path string) {
			if path != root || replaced {
				return
			}
			replaced = true
			moved := path + "-moved"
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}}
		request := store.RetentionComponentRequest{
			Component:          store.RetentionCandidateFiles,
			ReportedIdentities: 78, ScanIdentities: 79,
		}
		result := scanCandidateFiles(
			t.Context(), root, request,
			newCollectionBudget(defaultCollectorPolicy(), hooks),
		).componentResult(request.Component)
		if result.Count.Value == nil || *result.Count.Value != 1 ||
			result.Count.Completeness != LowerBound || len(result.Diagnostics) != 1 {
			t.Fatalf("close-fence result = %+v, want positive lower bound with diagnostic", result)
		}
	})
}

func TestFilesystemCancellationReleasesDescriptors(t *testing.T) {
	root := t.TempDir()
	prepareCandidateFiles(t, root, 2)
	ctx, cancel := context.WithCancel(t.Context())
	active := 0
	hooks := collectorHooks{
		afterEntryInfo:  func(string) { cancel() },
		descriptorDelta: func(delta int) { active += delta },
	}
	request := store.RetentionComponentRequest{
		Component:          store.RetentionCandidateFiles,
		ReportedIdentities: 78, ScanIdentities: 79,
	}
	scan := scanCandidateFiles(
		ctx, root, request, newCollectionBudget(defaultCollectorPolicy(), hooks),
	)
	if !errors.Is(scan.err, context.Canceled) || active != 0 {
		t.Fatalf("canceled scan = err %v active %d, want context canceled/0", scan.err, active)
	}
}

func prepareCandidateFiles(t *testing.T, root string, count int) {
	t.Helper()
	base := candidateid.ArtifactBase("github.com/acme/service")
	for index := range count {
		name := fmt.Sprintf(
			"%s-v4-%s-repository-%06d.ndjson",
			base, strings.Repeat("a", 64), index,
		)
		writeTestFile(t, filepath.Join(root, name), 1)
	}
}

func prepareFocusedFiles(t *testing.T, root string, count int) {
	t.Helper()
	prefix := focusedindex.ShardPrefix(
		"github.com/acme/service", "sha256:"+strings.Repeat("a", 64),
	)
	for index := range count {
		writeTestFile(t, filepath.Join(root, fmt.Sprintf("%s-%06d.zoekt", prefix, index)), 1)
	}
}

func prepareResolverFiles(t *testing.T, root string, count int) {
	t.Helper()
	prefix := resolvercatalogid.ArtifactPrefix(
		"github.com/acme/service", "sha256:"+strings.Repeat("a", 64),
	)
	for index := range count {
		writeTestFile(t, filepath.Join(root, fmt.Sprintf("%smember-%03d.ndjson", prefix, index)), 1)
	}
}

func prepareCallerFiles(t *testing.T, root string, count int) {
	t.Helper()
	directory := filepath.Join(
		root, callerpublicationid.RepositoryDirectory("github.com/acme/service"),
	)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := range count {
		pair := fmt.Sprintf("sha256:%064x", index+1)
		content := fmt.Sprintf("sha256:%064x", index+count+1)
		writeTestFile(t, filepath.Join(directory, callerleafid.ArtifactName(pair, content)), 1)
	}
}

func writeTestFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findByteMetric(t *testing.T, result ComponentResult, kind ByteKind) Metric {
	t.Helper()
	for _, observed := range result.ByteMetrics {
		if observed.Kind == kind {
			return observed.Metric
		}
	}
	t.Fatalf("component %q has no %q metric", result.Component, kind)
	return Metric{}
}

func findMetricValue(t *testing.T, result ComponentResult, kind ByteKind) int64 {
	t.Helper()
	observed := findByteMetric(t, result, kind)
	if observed.Value == nil {
		t.Fatalf("component %q metric %q is unavailable", result.Component, kind)
	}
	return *observed.Value
}

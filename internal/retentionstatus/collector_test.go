package retentionstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

type derivedStoreFunc func(
	context.Context,
	[]store.RetentionComponentRequest,
) (store.DerivedRetentionCollection, error)

func (function derivedStoreFunc) CollectDerivedRetention(
	ctx context.Context,
	requests []store.RetentionComponentRequest,
) (store.DerivedRetentionCollection, error) {
	return function(ctx, requests)
}

func TestCollectorComposesSevenComponentsAndDataVolume(t *testing.T) {
	dataDir := t.TempDir()
	wantStoreRequests := derivedStoreRequests()
	collector := New(dataDir, derivedStoreFunc(func(
		_ context.Context,
		requests []store.RetentionComponentRequest,
	) (store.DerivedRetentionCollection, error) {
		if !reflect.DeepEqual(requests, wantStoreRequests) {
			t.Fatalf("store requests = %+v, want %+v", requests, wantStoreRequests)
		}
		return emptyDerivedStoreCollection(), nil
	}))
	collector.statFS = func(*os.File) (statFSResult, error) {
		return statFSResult{Blocks: 2, AvailableBlocks: 1, BlockSize: 4096}, nil
	}
	collection, err := collector.CollectRetention(t.Context(), derivedRequests())
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []store.RetentionComponent{
		store.RetentionCandidatePublication,
		store.RetentionCandidateFiles,
		store.RetentionFocusedRepositoryState,
		store.RetentionFocusedFiles,
		store.RetentionResolverPublication,
		store.RetentionResolverFiles,
		store.RetentionCallerArtifacts,
	}
	if len(collection.Components) != len(wantOrder) {
		t.Fatalf("components = %d, want %d", len(collection.Components), len(wantOrder))
	}
	for index, component := range collection.Components {
		if component.Component != wantOrder[index] || component.Err != nil ||
			component.Count.Value == nil || *component.Count.Value != 0 ||
			component.Count.Completeness != Exact {
			t.Fatalf("component %d = %+v, want %q exact zero", index, component, wantOrder[index])
		}
	}
	if got := findMetricValue(t, collection.Components[1], ApparentFile); got != 0 {
		t.Fatalf("candidate apparent = %d, want 0", got)
	}
	if got := findMetricValue(t, collection.Components[4], CanonicalContent); got != 0 {
		t.Fatalf("resolver canonical = %d, want 0", got)
	}
	if got := findMetricValue(t, collection.Components[6], CanonicalReceipt); got != 0 {
		t.Fatalf("caller canonical = %d, want 0", got)
	}
	if collection.DataVolume.TotalBytes.Value == nil ||
		*collection.DataVolume.TotalBytes.Value != 8192 ||
		collection.DataVolume.AvailableBytes.Value == nil ||
		*collection.DataVolume.AvailableBytes.Value != 4096 ||
		collection.DataVolume.TotalBytes.Completeness != Exact ||
		collection.DataVolume.AvailableBytes.Completeness != Exact {
		t.Fatalf("data volume = %+v, want 8192/4096 exact", collection.DataVolume)
	}
}

func TestCollectorFocusedPartialMapping(t *testing.T) {
	tests := []struct {
		name       string
		collection store.DerivedRetentionCollection
		wantValue  *int64
		wantErr    bool
	}{
		{
			name: "nonempty lower bound",
			collection: func() store.DerivedRetentionCollection {
				collection := emptyDerivedStoreCollection()
				collection.FocusedPartial = true
				collection.Results[1].Summary = store.RetentionComponentSummary{
					ScannedIdentities: 1, ReportedIdentities: 1,
				}
				collection.FocusedAuthorities = []store.RetentionFocusedRepositoryAuthority{{
					Repository: "github.com/acme/service",
				}}
				return collection
			}(),
			wantValue: int64Pointer(1),
		},
		{
			name: "zero unavailable",
			collection: func() store.DerivedRetentionCollection {
				collection := emptyDerivedStoreCollection()
				collection.FocusedPartial = true
				collection.Results[1].Err = errors.New("bounded raw prefix has no focused row")
				return collection
			}(),
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			collector := testCollector(t, test.collection)
			observed, err := collector.CollectRetention(t.Context(), derivedRequests())
			if err != nil {
				t.Fatal(err)
			}
			focused := observed.Components[2]
			if test.wantErr {
				if focused.Err == nil || focused.Count.Value != nil ||
					focused.Count.Completeness != Unavailable {
					t.Fatalf("focused result = %+v, want unavailable", focused)
				}
				return
			}
			if focused.Err != nil || focused.Count.Value == nil ||
				*focused.Count.Value != *test.wantValue ||
				focused.Count.Completeness != LowerBound || focused.Truncated ||
				len(focused.Diagnostics) != 1 {
				t.Fatalf("focused result = %+v, want non-sentinel lower bound", focused)
			}
		})
	}
}

func TestCollectorResolverCanonicalAuthority(t *testing.T) {
	t.Run("exact current plus residue", func(t *testing.T) {
		dataDir := t.TempDir()
		state, publication, canonical := installResolverFixture(t, dataDir)
		root := filepath.Join(dataDir, "resolver-catalogs")
		writeTestFile(t, filepath.Join(root,
			"phebs-resolver-catalog-"+strings.Repeat("f", 64)+"-v1-"+
				strings.Repeat("e", 64)+"-residue.ndjson"), 3)
		_ = state
		collection := emptyDerivedStoreCollection()
		collection.Results[2].Summary = store.RetentionComponentSummary{
			ScannedIdentities: 1, ReportedIdentities: 1,
		}
		collection.ResolverAuthorities = []store.ResolverCatalogPublication{publication}
		collector := testCollectorAt(dataDir, collection)
		active, peak := 0, 0
		collector.hooks.descriptorDelta = func(delta int) {
			active += delta
			peak = max(peak, active)
		}
		observed, err := collector.CollectRetention(t.Context(), derivedRequests())
		if err != nil {
			t.Fatal(err)
		}
		pointer := observed.Components[4]
		if got := findMetricValue(t, pointer, CanonicalContent); got != canonical ||
			findByteMetric(t, pointer, CanonicalContent).Completeness != Exact {
			t.Fatalf("resolver canonical = %+v, want %d exact", pointer.ByteMetrics, canonical)
		}
		files := observed.Components[5]
		if files.Count.Value == nil || *files.Count.Value != 3 {
			t.Fatalf("resolver files = %+v, want manifest/member/residue", files)
		}
		if active != 0 || peak > maxRetainedDescriptors {
			t.Fatalf("canonical descriptors active/peak = %d/%d", active, peak)
		}
	})

	t.Run("pointer without file", func(t *testing.T) {
		fixtureDir := t.TempDir()
		_, publication, _ := installResolverFixture(t, fixtureDir)
		dataDir := t.TempDir()
		collection := emptyDerivedStoreCollection()
		collection.Results[2].Summary = store.RetentionComponentSummary{
			ScannedIdentities: 1, ReportedIdentities: 1,
		}
		collection.ResolverAuthorities = []store.ResolverCatalogPublication{publication}
		observed, err := testCollectorAt(dataDir, collection).CollectRetention(
			t.Context(), derivedRequests(),
		)
		if err != nil {
			t.Fatal(err)
		}
		pointer := observed.Components[4]
		if pointer.Count.Value == nil || *pointer.Count.Value != 1 ||
			findByteMetric(t, pointer, CanonicalContent).Completeness != Unavailable ||
			len(pointer.Diagnostics) != 1 {
			t.Fatalf("pointer-without-file = %+v", pointer)
		}
		if files := observed.Components[5]; files.Count.Value == nil || *files.Count.Value != 0 {
			t.Fatalf("resolver files = %+v, want exact zero", files)
		}
	})

	t.Run("manifest without pointer", func(t *testing.T) {
		dataDir := t.TempDir()
		_, _, _ = installResolverFixture(t, dataDir)
		observed, err := testCollectorAt(
			dataDir, emptyDerivedStoreCollection(),
		).CollectRetention(t.Context(), derivedRequests())
		if err != nil {
			t.Fatal(err)
		}
		pointer := observed.Components[4]
		if pointer.Count.Value == nil || *pointer.Count.Value != 0 ||
			findMetricValue(t, pointer, CanonicalContent) != 0 {
			t.Fatalf("pointer = %+v, want exact zero", pointer)
		}
		if files := observed.Components[5]; files.Count.Value == nil || *files.Count.Value != 2 {
			t.Fatalf("resolver files = %+v, want manifest/member", files)
		}
	})

	t.Run("mismatch unavailable", func(t *testing.T) {
		dataDir := t.TempDir()
		_, publication, _ := installResolverFixture(t, dataDir)
		publication.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
		collection := emptyDerivedStoreCollection()
		collection.Results[2].Summary = store.RetentionComponentSummary{
			ScannedIdentities: 1, ReportedIdentities: 1,
		}
		collection.ResolverAuthorities = []store.ResolverCatalogPublication{publication}
		observed, err := testCollectorAt(dataDir, collection).CollectRetention(
			t.Context(), derivedRequests(),
		)
		if err != nil {
			t.Fatal(err)
		}
		pointer := observed.Components[4]
		if pointer.Count.Value == nil || *pointer.Count.Value != 1 ||
			findByteMetric(t, pointer, CanonicalContent).Completeness != Unavailable ||
			len(pointer.Diagnostics) != 1 {
			t.Fatalf("mismatched resolver pointer = %+v", pointer)
		}
	})

	t.Run("truncated lower bound", func(t *testing.T) {
		dataDir := t.TempDir()
		_, publication, _ := installResolverFixture(t, dataDir)
		collection := emptyDerivedStoreCollection()
		collection.Results[2].Summary = store.RetentionComponentSummary{
			ScannedIdentities: 79, ReportedIdentities: 78, Truncated: true,
		}
		collection.ResolverAuthorities = make([]store.ResolverCatalogPublication, 78)
		for index := range collection.ResolverAuthorities {
			collection.ResolverAuthorities[index] = publication
		}
		observed, err := testCollectorAt(dataDir, collection).CollectRetention(
			t.Context(), derivedRequests(),
		)
		if err != nil {
			t.Fatal(err)
		}
		pointer := observed.Components[4]
		canonical := findByteMetric(t, pointer, CanonicalContent)
		if pointer.Count.Value == nil || *pointer.Count.Value != 78 || !pointer.Truncated ||
			canonical.Value == nil || canonical.Completeness != LowerBound {
			t.Fatalf("truncated resolver pointer = %+v", pointer)
		}
	})

	t.Run("metadata budget retains admitted prefix", func(t *testing.T) {
		dataDir := t.TempDir()
		state, publication, canonical := installResolverFixture(t, dataDir)
		manifestInfo, err := os.Stat(filepath.Join(dataDir, "resolver-catalogs", state.Manifest))
		if err != nil {
			t.Fatal(err)
		}
		collection := emptyDerivedStoreCollection()
		collection.Results[2].Summary = store.RetentionComponentSummary{
			ScannedIdentities: 2, ReportedIdentities: 2,
		}
		collection.ResolverAuthorities = []store.ResolverCatalogPublication{
			publication, publication,
		}
		collector := testCollectorAt(dataDir, collection)
		collector.policy.aggregateMetadata = manifestInfo.Size()
		maxObserved := int64(0)
		collector.hooks.metadataObserved = func(current int64) {
			maxObserved = max(maxObserved, current)
		}
		observed, err := collector.CollectRetention(t.Context(), derivedRequests())
		if err != nil {
			t.Fatal(err)
		}
		metric := findByteMetric(t, observed.Components[4], CanonicalContent)
		if metric.Value == nil || *metric.Value != canonical ||
			metric.Completeness != LowerBound || maxObserved > collector.policy.aggregateMetadata {
			t.Fatalf(
				"metadata-bounded canonical/max = %+v/%d, want %d lower and <=%d",
				metric, maxObserved, canonical, collector.policy.aggregateMetadata,
			)
		}
	})
}

func TestCollectorCallerCanonicalIsIndependentOfResidueAndStoreFailure(t *testing.T) {
	t.Run("exact receipt with residue", func(t *testing.T) {
		dataDir := t.TempDir()
		authority := installCallerManifestFixture(t, dataDir)
		directory := filepath.Join(
			dataDir, "caller-leaves",
			callerpublicationid.RepositoryDirectory(authority.Generation.Repository),
		)
		writeTestFile(t, filepath.Join(directory,
			"leaf-v1-"+strings.Repeat("b", 64)+"-"+strings.Repeat("c", 64)+".ndjson"), 7)
		collection := emptyDerivedStoreCollection()
		collection.CallerScannedIdentities = 1
		collection.CallerAuthorities = []store.CallerGenerationPublicationSummary{authority}
		observed, err := testCollectorAt(dataDir, collection).CollectRetention(
			t.Context(), derivedRequests(),
		)
		if err != nil {
			t.Fatal(err)
		}
		caller := observed.Components[6]
		canonical := findByteMetric(t, caller, CanonicalReceipt)
		if canonical.Value == nil || authority.CanonicalBytes <= 0 ||
			*canonical.Value != authority.CanonicalBytes || canonical.Completeness != Exact ||
			caller.Count.Value == nil || *caller.Count.Value != 2 ||
			findMetricValue(t, caller, ApparentFile) <= 7 {
			t.Fatalf("caller receipt/residue result = %+v", caller)
		}
	})

	t.Run("store support failure preserves file metrics", func(t *testing.T) {
		dataDir := t.TempDir()
		root := filepath.Join(dataDir, "caller-leaves")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		prepareCallerFiles(t, root, 1)
		collection := emptyDerivedStoreCollection()
		collection.CallerErr = errors.New("caller authority unavailable")
		observed, err := testCollectorAt(dataDir, collection).CollectRetention(
			t.Context(), derivedRequests(),
		)
		if err != nil {
			t.Fatal(err)
		}
		caller := observed.Components[6]
		if caller.Err != nil || caller.Count.Value == nil || *caller.Count.Value != 1 ||
			caller.Count.Completeness != Exact ||
			findByteMetric(t, caller, ApparentFile).Completeness != Exact ||
			findByteMetric(t, caller, CanonicalReceipt).Completeness != Unavailable ||
			len(caller.Diagnostics) != 1 {
			t.Fatalf("caller support failure = %+v", caller)
		}
	})

	t.Run("extractor digest mismatch refuses canonical receipt", func(t *testing.T) {
		dataDir := t.TempDir()
		authority := installCallerManifestFixture(t, dataDir)
		authority.Generation.ExtractorSetDigest = "sha256:" + strings.Repeat("0", 64)
		collection := emptyDerivedStoreCollection()
		collection.CallerScannedIdentities = 1
		collection.CallerAuthorities = []store.CallerGenerationPublicationSummary{authority}
		observed, err := testCollectorAt(dataDir, collection).CollectRetention(
			t.Context(), derivedRequests(),
		)
		if err != nil {
			t.Fatal(err)
		}
		caller := observed.Components[6]
		if findByteMetric(t, caller, CanonicalReceipt).Completeness != Unavailable ||
			len(caller.Diagnostics) != 1 {
			t.Fatalf("extractor-mismatched caller result = %+v", caller)
		}
	})
}

func TestCollectorMaximumLeanAuthorityEnvelopeFitsStatBudget(t *testing.T) {
	dataDir := t.TempDir()
	collection := emptyDerivedStoreCollection()
	var resolverCanonical int64
	var callerCanonical int64
	for index := range 78 {
		_, resolverAuthority, canonical := installResolverFixtureForRepository(
			t, dataDir, fmt.Sprintf("github.com/acme/resolver-%02d", index),
		)
		callerAuthority := installCallerManifestFixtureForRepository(
			t, dataDir, fmt.Sprintf("github.com/acme/caller-%02d", index),
		)
		collection.ResolverAuthorities = append(
			collection.ResolverAuthorities, resolverAuthority,
		)
		collection.CallerAuthorities = append(
			collection.CallerAuthorities, callerAuthority,
		)
		resolverCanonical += canonical
		callerCanonical += callerAuthority.CanonicalBytes
	}

	for _, root := range []string{
		filepath.Join(dataDir, "candidates"),
		filepath.Join(dataDir, "index"),
	} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	prepareCandidateFiles(t, filepath.Join(dataDir, "candidates"), 79)
	prepareFocusedFiles(t, filepath.Join(dataDir, "index"), 79)
	prepareCallerFiles(t, filepath.Join(dataDir, "caller-leaves"), 1)

	for index := range collection.Results {
		collection.Results[index].Summary = store.RetentionComponentSummary{
			ScannedIdentities: 78, ReportedIdentities: 78,
		}
	}
	collection.CandidateAuthorities = make([]store.CandidateManifestPublication, 78)
	collection.FocusedAuthorities = make([]store.RetentionFocusedRepositoryAuthority, 78)
	collection.CallerScannedIdentities = 78
	if AggregateStatOperationLimit < 3_484 {
		t.Fatalf("stat operation limit = %d, want at least lean maximum 3484", AggregateStatOperationLimit)
	}

	collector := testCollectorAt(dataDir, collection)
	collector.policy.aggregateStats = 3_484
	active, peak := 0, 0
	collector.hooks.descriptorDelta = func(delta int) {
		active += delta
		peak = max(peak, active)
	}
	observed, err := collector.CollectRetention(t.Context(), derivedRequests())
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 3, 5, 6} {
		component := observed.Components[index]
		if component.Count.Value == nil || *component.Count.Value != 78 ||
			component.Count.Completeness != LowerBound || !component.Truncated {
			t.Fatalf("maximum physical component %q = %+v", component.Component, component)
		}
	}
	resolverMetric := findByteMetric(t, observed.Components[4], CanonicalContent)
	callerMetric := findByteMetric(t, observed.Components[6], CanonicalReceipt)
	if resolverMetric.Value == nil || *resolverMetric.Value != resolverCanonical ||
		resolverMetric.Completeness != Exact || callerMetric.Value == nil ||
		*callerMetric.Value != callerCanonical ||
		callerMetric.Completeness != Exact {
		t.Fatalf("maximum canonical metrics = resolver %+v caller %+v", resolverMetric, callerMetric)
	}
	if observed.DataVolume.TotalBytes.Completeness != Exact ||
		observed.DataVolume.AvailableBytes.Completeness != Exact ||
		active != 0 || peak > maxRetainedDescriptors {
		t.Fatalf("maximum envelope data/descriptors = %+v/%d/%d", observed.DataVolume, active, peak)
	}

	collector = testCollectorAt(dataDir, collection)
	collector.policy.aggregateStats = 3_483
	observed, err = collector.CollectRetention(t.Context(), derivedRequests())
	if err != nil {
		t.Fatal(err)
	}
	callerMetric = findByteMetric(t, observed.Components[6], CanonicalReceipt)
	if callerMetric.Value == nil || callerMetric.Completeness != LowerBound ||
		!errors.Is(firstComponentError(observed.Components[6]), errStatBudget) ||
		observed.DataVolume.TotalBytes.Completeness != Exact ||
		observed.DataVolume.AvailableBytes.Completeness != Exact {
		t.Fatalf("stat boundary caller/data = %+v/%+v", observed.Components[6], observed.DataVolume)
	}
}

func TestAddCanonicalBytesRejectsOverflow(t *testing.T) {
	if total, ok := addCanonicalBytes(math.MaxInt64-1, 1); !ok || total != math.MaxInt64 {
		t.Fatalf("boundary addition = %d/%v", total, ok)
	}
	for _, values := range [][2]int64{{math.MaxInt64, 1}, {-1, 0}, {0, -1}} {
		if total, ok := addCanonicalBytes(values[0], values[1]); ok || total != 0 {
			t.Fatalf("invalid addition %v = %d/%v", values, total, ok)
		}
	}
}

func TestCollectorDataRootAndStatFSFailuresStayLocalized(t *testing.T) {
	t.Run("symlink data root", func(t *testing.T) {
		realRoot := t.TempDir()
		link := filepath.Join(t.TempDir(), "data")
		if err := os.Symlink(realRoot, link); err != nil {
			t.Fatal(err)
		}
		observed, err := testCollectorAt(
			link, emptyDerivedStoreCollection(),
		).CollectRetention(t.Context(), derivedRequests())
		if err != nil {
			t.Fatal(err)
		}
		for _, index := range []int{1, 3, 5, 6} {
			if observed.Components[index].Err == nil ||
				observed.Components[index].Count.Completeness != Unavailable {
				t.Fatalf("filesystem component %d = %+v, want unavailable", index, observed.Components[index])
			}
		}
		if observed.DataVolume.TotalBytes.Completeness != Unavailable ||
			len(observed.DataVolume.Diagnostics) != 1 {
			t.Fatalf("data volume = %+v, want unavailable", observed.DataVolume)
		}
	})

	t.Run("statfs failure", func(t *testing.T) {
		collector := testCollector(t, emptyDerivedStoreCollection())
		collector.statFS = func(*os.File) (statFSResult, error) {
			return statFSResult{}, errors.New("statfs failed")
		}
		observed, err := collector.CollectRetention(t.Context(), derivedRequests())
		if err != nil {
			t.Fatal(err)
		}
		if observed.DataVolume.TotalBytes.Completeness != Unavailable ||
			observed.Components[1].Count.Completeness != Exact {
			t.Fatalf("statfs localization = data %+v candidate %+v", observed.DataVolume, observed.Components[1])
		}
	})

	t.Run("overflow", func(t *testing.T) {
		collector := testCollector(t, emptyDerivedStoreCollection())
		collector.statFS = func(*os.File) (statFSResult, error) {
			return statFSResult{Blocks: math.MaxUint64, AvailableBlocks: 1, BlockSize: 2}, nil
		}
		observed, err := collector.CollectRetention(t.Context(), derivedRequests())
		if err != nil {
			t.Fatal(err)
		}
		if observed.DataVolume.TotalBytes.Completeness != Unavailable {
			t.Fatalf("overflow data volume = %+v", observed.DataVolume)
		}
	})
}

func TestCollectorRejectsMalformedStoreAndCancellation(t *testing.T) {
	t.Run("omitted result", func(t *testing.T) {
		collection := emptyDerivedStoreCollection()
		collection.Results = collection.Results[:2]
		_, err := testCollector(t, collection).CollectRetention(t.Context(), derivedRequests())
		if err == nil {
			t.Fatal("malformed store collection accepted")
		}
	})

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := testCollector(t, emptyDerivedStoreCollection()).CollectRetention(
			ctx, derivedRequests(),
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled collector error = %v, want context canceled", err)
		}
	})

	t.Run("cancellation releases retained data anchor", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		collector := testCollector(t, emptyDerivedStoreCollection())
		active := 0
		collector.hooks.descriptorDelta = func(delta int) { active += delta }
		collector.hooks.afterDirectoryOpen = func(path string) {
			if path == collector.dataDir {
				cancel()
			}
		}
		_, err := collector.CollectRetention(ctx, derivedRequests())
		if !errors.Is(err, context.Canceled) || active != 0 {
			t.Fatalf("canceled collector error/active = %v/%d", err, active)
		}
	})
}

func TestCollectorMetadataCloseFailurePreventsExactCanonical(t *testing.T) {
	dataDir := t.TempDir()
	_, publication, _ := installResolverFixture(t, dataDir)
	collection := emptyDerivedStoreCollection()
	collection.Results[2].Summary = store.RetentionComponentSummary{
		ScannedIdentities: 1, ReportedIdentities: 1,
	}
	collection.ResolverAuthorities = []store.ResolverCatalogPublication{publication}
	collector := testCollectorAt(dataDir, collection)
	collector.hooks.fileCloseError = func(string) error { return errors.New("close failed") }
	observed, err := collector.CollectRetention(t.Context(), derivedRequests())
	if err != nil {
		t.Fatal(err)
	}
	pointer := observed.Components[4]
	if findByteMetric(t, pointer, CanonicalContent).Completeness != Unavailable ||
		len(pointer.Diagnostics) != 1 {
		t.Fatalf("close-failed canonical = %+v", pointer)
	}
}

func TestCollectorCompoundFinalRootFailureStaysWithinDiagnosticBound(t *testing.T) {
	dataDir := t.TempDir()
	for _, subroot := range []string{"candidates", "index", "resolver-catalogs", "caller-leaves"} {
		if err := os.Mkdir(filepath.Join(dataDir, subroot), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	collection := emptyDerivedStoreCollection()
	for index := range collection.Results {
		collection.Results[index].Err = errors.New("store unavailable")
	}
	collection.CallerErr = errors.New("caller unavailable")
	collector := testCollectorAt(dataDir, collection)
	replaced := false
	collector.hooks.beforeDirectoryClose = func(path string) {
		if path != filepath.Join(dataDir, "caller-leaves") || replaced {
			return
		}
		replaced = true
		moved := dataDir + "-moved"
		if err := os.Rename(dataDir, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := collector.CollectRetention(t.Context(), derivedRequests())
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := len(observed.DataVolume.Diagnostics)
	for _, component := range observed.Components {
		if component.Err != nil {
			diagnostics++
		}
		diagnostics += len(component.Diagnostics)
	}
	if diagnostics > MaxLocalizedDiagnosticsPerRequest {
		t.Fatalf("diagnostics = %d, limit %d", diagnostics, MaxLocalizedDiagnosticsPerRequest)
	}
}

func TestEmptyNestedDirectoriesRemainReachable(t *testing.T) {
	t.Run("resolver stage", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".phebs-resolver-catalog-stage-empty"), 0o700); err != nil {
			t.Fatal(err)
		}
		request := derivedRequest(store.RetentionResolverFiles)
		result := scanResolverFiles(
			t.Context(), root, request,
			newCollectionBudget(defaultCollectorPolicy(), collectorHooks{}),
		).componentResult(request.Component)
		if result.Err != nil || result.Count.Value == nil || *result.Count.Value != 0 {
			t.Fatalf("empty resolver stage = %+v, want exact zero", result)
		}
	})

	t.Run("caller repository", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root,
			callerpublicationid.RepositoryDirectory("github.com/acme/empty")), 0o700); err != nil {
			t.Fatal(err)
		}
		request := derivedRequest(store.RetentionCallerArtifacts)
		result := scanCallerFiles(
			t.Context(), root, request,
			newCollectionBudget(defaultCollectorPolicy(), collectorHooks{}),
		).componentResult(request.Component)
		if result.Err != nil || result.Count.Value == nil || *result.Count.Value != 0 {
			t.Fatalf("empty caller repository = %+v, want exact zero", result)
		}
	})
}

func TestNestedDirectoryStatExhaustionPreservesEarlierResultsAndFinalFence(t *testing.T) {
	tests := []struct {
		name           string
		exhaustedIndex int
		prepare        func(*testing.T, string)
	}{
		{
			name: "resolver stages", exhaustedIndex: 5,
			prepare: func(t *testing.T, dataDir string) {
				root := filepath.Join(dataDir, "resolver-catalogs")
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				for index := range 6 {
					if err := os.Mkdir(filepath.Join(root,
						fmt.Sprintf(".phebs-resolver-catalog-stage-%02d", index)), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "caller repositories", exhaustedIndex: 6,
			prepare: func(t *testing.T, dataDir string) {
				root := filepath.Join(dataDir, "caller-leaves")
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				for index := range 6 {
					name := callerpublicationid.RepositoryDirectory(
						fmt.Sprintf("github.com/acme/empty-%d", index),
					)
					if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			candidateRoot := filepath.Join(dataDir, "candidates")
			if err := os.Mkdir(candidateRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			prepareCandidateFiles(t, candidateRoot, 1)
			test.prepare(t, dataDir)
			collector := testCollectorAt(dataDir, emptyDerivedStoreCollection())
			collector.policy.aggregateStats = 32
			observed, err := collector.CollectRetention(t.Context(), derivedRequests())
			if err != nil {
				t.Fatal(err)
			}
			candidate := observed.Components[1]
			exhausted := observed.Components[test.exhaustedIndex]
			if candidate.Count.Value == nil || *candidate.Count.Value != 1 ||
				candidate.Count.Completeness != Exact ||
				exhausted.Count.Completeness != Unavailable || exhausted.Err == nil ||
				observed.DataVolume.TotalBytes.Completeness != Exact {
				t.Fatalf(
					"low-stat candidate/exhausted/data = %+v / %+v / %+v",
					candidate, exhausted, observed.DataVolume,
				)
			}
		})
	}
}

func TestNestedPhysicalOpenRejectsSubrootReplacement(t *testing.T) {
	for _, component := range []store.RetentionComponent{
		store.RetentionResolverFiles,
		store.RetentionCallerArtifacts,
	} {
		t.Run(string(component), func(t *testing.T) {
			dataDir := t.TempDir()
			subroot, nested, componentIndex := prepareNestedPhysicalFixture(
				t, dataDir, component, 1,
			)
			externalData := t.TempDir()
			externalSubroot, _, _ := prepareNestedPhysicalFixture(
				t, externalData, component, 4,
			)
			collector := testCollectorAt(dataDir, emptyDerivedStoreCollection())
			var restore func()
			collector.hooks.beforeNestedOpen = func(path string) {
				if path == nested && restore == nil {
					restore = replaceDirectoryWithSymlink(t, subroot, externalSubroot)
				}
			}
			observed, err := collector.CollectRetention(t.Context(), derivedRequests())
			if restore != nil {
				restore()
			}
			if err != nil {
				t.Fatal(err)
			}
			result := observed.Components[componentIndex]
			if result.Count.Completeness != Unavailable || result.Err == nil ||
				result.Count.Value != nil {
				t.Fatalf("symlink-swapped nested result = %+v, want unavailable", result)
			}
		})
	}
}

func TestNestedPhysicalOpenRejectsSubrootABA(t *testing.T) {
	for _, component := range []store.RetentionComponent{
		store.RetentionResolverFiles,
		store.RetentionCallerArtifacts,
	} {
		t.Run(string(component), func(t *testing.T) {
			dataDir := t.TempDir()
			subroot, nested, componentIndex := prepareNestedPhysicalFixture(
				t, dataDir, component, 1,
			)
			externalData := t.TempDir()
			externalSubroot, _, _ := prepareNestedPhysicalFixture(
				t, externalData, component, 4,
			)
			collector := testCollectorAt(dataDir, emptyDerivedStoreCollection())
			var restore func()
			restored := false
			collector.hooks.beforeNestedOpen = func(path string) {
				if path == nested && restore == nil {
					restore = exchangeDirectory(t, subroot, externalSubroot)
				}
			}
			collector.hooks.afterNestedOpen = func(path string) {
				if path == nested && restore != nil && !restored {
					restore()
					restored = true
				}
			}
			observed, err := collector.CollectRetention(t.Context(), derivedRequests())
			if restore != nil && !restored {
				restore()
			}
			if err != nil {
				t.Fatal(err)
			}
			result := observed.Components[componentIndex]
			if !restored || result.Count.Completeness != Unavailable || result.Err == nil {
				t.Fatalf("ABA nested result/restored = %+v/%v", result, restored)
			}
		})
	}
}

func TestRetainedDataAnchorDefeatsDataDirectoryABA(t *testing.T) {
	dataDir := t.TempDir()
	_, nested, componentIndex := prepareNestedPhysicalFixture(
		t, dataDir, store.RetentionResolverFiles, 1,
	)
	externalData := t.TempDir()
	_, _, _ = prepareNestedPhysicalFixture(
		t, externalData, store.RetentionResolverFiles, 4,
	)
	collector := testCollectorAt(dataDir, emptyDerivedStoreCollection())
	var restore func()
	restored := false
	collector.hooks.beforeNestedOpen = func(path string) {
		if path == nested && restore == nil {
			restore = exchangeDirectory(t, dataDir, externalData)
		}
	}
	collector.hooks.afterNestedOpen = func(path string) {
		if path == nested && restore != nil && !restored {
			restore()
			restored = true
		}
	}
	observed, err := collector.CollectRetention(t.Context(), derivedRequests())
	if restore != nil && !restored {
		restore()
	}
	if err != nil {
		t.Fatal(err)
	}
	result := observed.Components[componentIndex]
	if !restored || result.Count.Value == nil || *result.Count.Value != 1 ||
		result.Count.Completeness != Exact {
		t.Fatalf("data-root ABA result/restored = %+v/%v, want original exact one", result, restored)
	}
}

func TestCanonicalRootsRejectStaticSymlinkAncestors(t *testing.T) {
	for _, name := range []string{"resolver", "caller"} {
		t.Run(name, func(t *testing.T) {
			fixture := prepareCanonicalRaceFixture(t, name)
			restore := replaceDirectoryWithSymlink(
				t, fixture.staticPath, fixture.staticTarget,
			)
			collector := testCollectorAt(fixture.dataDir, fixture.collection)
			observed, err := collector.CollectRetention(t.Context(), derivedRequests())
			restore()
			if err != nil {
				t.Fatal(err)
			}
			metric := findByteMetric(
				t, observed.Components[fixture.componentIndex], fixture.byteKind,
			)
			if metric.Completeness != Unavailable || metric.Value != nil {
				t.Fatalf("static-symlink canonical metric = %+v, want unavailable", metric)
			}
		})
	}
}

func TestCanonicalRootsDefeatAncestorSwapAndRestore(t *testing.T) {
	for _, name := range []string{"resolver", "caller"} {
		t.Run(name, func(t *testing.T) {
			fixture := prepareCanonicalRaceFixture(t, name)
			collector := testCollectorAt(fixture.dataDir, fixture.collection)
			active, peak := 0, 0
			collector.hooks.descriptorDelta = func(delta int) {
				active += delta
				peak = max(peak, active)
			}
			var restore func()
			restored := false
			collector.hooks.beforeRegularOpen = func(path string) {
				if path == fixture.manifestPath && restore == nil {
					restore = exchangeDirectory(t, fixture.abaPath, fixture.abaReplacement)
				}
			}
			collector.hooks.afterRegularOpen = func(path string) {
				if path == fixture.manifestPath && restore != nil && !restored {
					restore()
					restored = true
				}
			}
			observed, err := collector.CollectRetention(t.Context(), derivedRequests())
			if restore != nil && !restored {
				restore()
			}
			if err != nil {
				t.Fatal(err)
			}
			metric := findByteMetric(
				t, observed.Components[fixture.componentIndex], fixture.byteKind,
			)
			if !restored || metric.Value == nil || *metric.Value != fixture.canonical ||
				metric.Completeness != Exact || active != 0 || peak > maxRetainedDescriptors {
				t.Fatalf(
					"ABA canonical metric/restored/descriptors = %+v/%v/%d/%d",
					metric, restored, active, peak,
				)
			}
		})
	}
}

func TestCanonicalCancellationClosesRootsAndManifest(t *testing.T) {
	fixture := prepareCanonicalRaceFixture(t, "resolver")
	collector := testCollectorAt(fixture.dataDir, fixture.collection)
	ctx, cancel := context.WithCancel(t.Context())
	active, peak := 0, 0
	collector.hooks.descriptorDelta = func(delta int) {
		active += delta
		peak = max(peak, active)
	}
	collector.hooks.beforeRegularOpen = func(path string) {
		if path == fixture.manifestPath {
			cancel()
		}
	}
	_, err := collector.CollectRetention(ctx, derivedRequests())
	if !errors.Is(err, context.Canceled) || active != 0 || peak > maxRetainedDescriptors {
		t.Fatalf("canonical cancellation error/descriptors = %v/%d/%d", err, active, peak)
	}
}

func TestCanonicalGrowthCannotExceedChargedMetadataRead(t *testing.T) {
	fixture := prepareCanonicalRaceFixture(t, "resolver")
	info, err := os.Stat(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	collector := testCollectorAt(fixture.dataDir, fixture.collection)
	collector.policy.aggregateMetadata = info.Size()
	maxObserved := int64(0)
	collector.hooks.metadataObserved = func(current int64) {
		maxObserved = max(maxObserved, current)
	}
	grown := false
	collector.hooks.beforeRegularRead = func(path string) {
		if path != fixture.manifestPath || grown {
			return
		}
		grown = true
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, writeErr := file.Write([]byte(strings.Repeat("x", 1024)))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(errors.Join(writeErr, closeErr))
		}
	}
	observed, err := collector.CollectRetention(t.Context(), derivedRequests())
	if err != nil {
		t.Fatal(err)
	}
	metric := findByteMetric(t, observed.Components[4], CanonicalContent)
	if !grown || metric.Completeness != Unavailable ||
		maxObserved != info.Size() || maxObserved > collector.policy.aggregateMetadata {
		t.Fatalf(
			"grown canonical metric/metadata = %+v/%d, want unavailable/%d",
			metric, maxObserved, info.Size(),
		)
	}
}

func emptyDerivedStoreCollection() store.DerivedRetentionCollection {
	return store.DerivedRetentionCollection{
		Results: []store.RetentionComponentResult{
			{Component: store.RetentionCandidatePublication},
			{Component: store.RetentionFocusedRepositoryState},
			{Component: store.RetentionResolverPublication},
		},
		CandidateAuthorities: []store.CandidateManifestPublication{},
		FocusedAuthorities:   []store.RetentionFocusedRepositoryAuthority{},
		ResolverAuthorities:  []store.ResolverCatalogPublication{},
		CallerAuthorities:    []store.CallerGenerationPublicationSummary{},
	}
}

func derivedRequests() []store.RetentionComponentRequest {
	return []store.RetentionComponentRequest{
		derivedRequest(store.RetentionCandidatePublication),
		derivedRequest(store.RetentionCandidateFiles),
		derivedRequest(store.RetentionFocusedRepositoryState),
		derivedRequest(store.RetentionFocusedFiles),
		derivedRequest(store.RetentionResolverPublication),
		derivedRequest(store.RetentionResolverFiles),
		derivedRequest(store.RetentionCallerArtifacts),
	}
}

func derivedStoreRequests() []store.RetentionComponentRequest {
	requests := derivedRequests()
	return []store.RetentionComponentRequest{
		requests[0], requests[2], requests[4], requests[6],
	}
}

func derivedRequest(component store.RetentionComponent) store.RetentionComponentRequest {
	return store.RetentionComponentRequest{
		Component: component, ReportedIdentities: 78, ScanIdentities: 79,
	}
}

func testCollector(
	t *testing.T,
	collection store.DerivedRetentionCollection,
) *Collector {
	t.Helper()
	return testCollectorAt(t.TempDir(), collection)
}

func testCollectorAt(
	dataDir string,
	collection store.DerivedRetentionCollection,
) *Collector {
	collector := New(dataDir, derivedStoreFunc(func(
		context.Context,
		[]store.RetentionComponentRequest,
	) (store.DerivedRetentionCollection, error) {
		return collection, nil
	}))
	collector.statFS = func(*os.File) (statFSResult, error) {
		return statFSResult{Blocks: 2, AvailableBlocks: 1, BlockSize: 4096}, nil
	}
	return collector
}

func installResolverFixture(
	t *testing.T,
	dataDir string,
) (resolvercatalog.State, store.ResolverCatalogPublication, int64) {
	t.Helper()
	return installResolverFixtureForRepository(
		t, dataDir, "github.com/acme/resolver",
	)
}

func installResolverFixtureForRepository(
	t *testing.T,
	dataDir string,
	repository string,
) (resolvercatalog.State, store.ResolverCatalogPublication, int64) {
	t.Helper()
	root := filepath.Join(dataDir, "resolver-catalogs")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	identity, err := resolvercatalog.NewIdentity(
		repository, strings.Repeat("1", 40), "",
		"sha256:"+strings.Repeat("a", 64), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := resolvercatalog.NewStage(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.AddMember(
		t.Context(), "go.ndjson", json.RawMessage(`{"pack":"neutral"}`),
		func(write func(json.RawMessage) error) error {
			return write(json.RawMessage(`{"name":"a","schema":"phebs-resolver-catalog-record-v1"}`))
		},
	); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := prepared.Publish(t.Context(), func(context.Context, resolvercatalog.State) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	publication := store.ResolverCatalogPublication{
		Repository:              state.Repository,
		HeadCommit:              state.Commit,
		UnitDigest:              state.UnitDigest,
		DeclarationSetDigest:    state.DeclarationSetDigest,
		CandidateManifestDigest: state.CandidateManifestDigest,
		SourceLanePolicy:        state.SourceLanePolicy,
		ResolverPackSetDigest:   state.ResolverPackSetDigest,
		CatalogPolicyDigest:     state.CatalogPolicyDigest,
		GenerationDigest:        state.GenerationDigest,
		ManifestDigest:          state.ManifestDigest,
		ManifestPath:            state.Manifest,
	}
	for _, declaration := range state.Declarations {
		publication.Declarations = append(
			publication.Declarations,
			store.ResolverCatalogDeclarationPublication{
				Domain: declaration.Domain, RunID: declaration.RunID,
				GenerationDigest: declaration.GenerationDigest,
			},
		)
	}
	for _, pack := range state.ResolverPacks {
		publication.ResolverPacks = append(
			publication.ResolverPacks,
			store.ResolverCatalogPack{Name: pack.Name, Version: pack.Version},
		)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(root, state.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, err := resolvercatalog.ParseRetentionManifest(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	return state, publication, canonical
}

func installCallerManifestFixture(
	t *testing.T,
	dataDir string,
) store.CallerGenerationPublicationSummary {
	t.Helper()
	return installCallerManifestFixtureForRepository(
		t, dataDir, "github.com/acme/caller",
	)
}

func installCallerManifestFixtureForRepository(
	t *testing.T,
	dataDir string,
	repository string,
) store.CallerGenerationPublicationSummary {
	t.Helper()
	digest := func(char string) string { return "sha256:" + strings.Repeat(char, 64) }
	generation, err := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository:               repository,
		HeadCommit:               strings.Repeat("1", 40),
		DeclarationSetDigest:     digest("a"),
		CandidateManifestDigest:  digest("b"),
		CandidatePolicyDigest:    digest("c"),
		ResolverGenerationDigest: digest("d"),
		ResolverManifestDigest:   digest("e"),
		Extractors: []callerleaf.ExtractorIdentity{{
			Domain: "grpc-caller", Version: "1.5.0",
			LeafAdapterVersion: callerleaf.LeafAdapterV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := callerleaf.NewPairIdentity(
		generation, "grpc-caller", "1.5.0", callerleaf.LeafDescriptor{
			Name: "candidate-caller-000000.ndjson", Ordinal: 0,
			Prefix: "00", PrefixBits: 2, RecordCount: 1,
			DeclaredBytes: 256, ContentBytes: 128, ContentDigest: digest("f"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, metadataDigest, err := callerleaf.MetadataFor(generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	contentDigest := digest("0")
	receipt := callerleaf.Receipt{
		Name:        callerleafid.ArtifactName(pair.Digest, contentDigest),
		RecordCount: 1, AbstentionCount: 1,
		ContentBytes: 17, ContentDigest: contentDigest,
		MetadataDigest: metadataDigest, StagingBytes: 17,
	}
	manifest, err := callerpublication.BuildManifest(
		generation, []callerpublication.PairReceipt{{Pair: pair, Receipt: receipt}},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	state := manifest.State()
	directory := filepath.Join(
		dataDir, "caller-leaves", callerpublicationid.RepositoryDirectory(generation.Repository),
	)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, state.Manifest), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return store.CallerGenerationPublicationSummary{
		Generation:      storedCallerGeneration(state.Generation),
		PairSetDigest:   state.PairSetDigest,
		PairCount:       state.Aggregate.PairCount,
		ArtifactCount:   state.Aggregate.ArtifactCount,
		ResultCount:     state.Aggregate.ResultCount,
		AbstentionCount: state.Aggregate.AbstentionCount,
		CanonicalBytes:  state.Aggregate.CanonicalBytes,
		StagingBytes:    state.Aggregate.StagingBytes,
		PeakOpenFiles:   state.Aggregate.PeakOpenFiles,
		ManifestDigest:  state.ManifestDigest,
		ManifestPath:    state.Manifest,
	}
}

func storedCallerGeneration(
	semantic callerleaf.GenerationIdentity,
) store.CallerGenerationIdentity {
	return store.CallerGenerationIdentity{
		Repository: semantic.Repository, HeadCommit: semantic.HeadCommit,
		UnitDigest:               semantic.UnitDigest,
		DeclarationSetDigest:     semantic.DeclarationSetDigest,
		CandidateManifestDigest:  semantic.CandidateManifestDigest,
		CandidatePolicyDigest:    semantic.CandidatePolicyDigest,
		ResolverGenerationDigest: semantic.ResolverGenerationDigest,
		ResolverManifestDigest:   semantic.ResolverManifestDigest,
		SourceLanePolicy:         semantic.SourceLanePolicy,
		CallerPolicyDigest:       semantic.CallerPolicyDigest,
		ExtractorSetDigest:       semantic.ExtractorSetDigest,
		Digest:                   semantic.Digest,
	}
}

func prepareNestedPhysicalFixture(
	t *testing.T,
	dataDir string,
	component store.RetentionComponent,
	count int,
) (string, string, int) {
	t.Helper()
	switch component {
	case store.RetentionResolverFiles:
		root := filepath.Join(dataDir, "resolver-catalogs")
		stage := filepath.Join(root, ".phebs-resolver-catalog-stage-race")
		if err := os.MkdirAll(stage, 0o700); err != nil {
			t.Fatal(err)
		}
		for index := range count {
			writeTestFile(t, filepath.Join(stage, fmt.Sprintf("member-%02d.ndjson", index)), 1)
		}
		return root, stage, 5
	case store.RetentionCallerArtifacts:
		root := filepath.Join(dataDir, "caller-leaves")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		prepareCallerFiles(t, root, count)
		nested := filepath.Join(
			root, callerpublicationid.RepositoryDirectory("github.com/acme/service"),
		)
		return root, nested, 6
	default:
		t.Fatalf("unsupported nested fixture component %q", component)
		return "", "", 0
	}
}

func replaceDirectoryWithSymlink(
	t *testing.T,
	path string,
	target string,
) func() {
	t.Helper()
	moved := path + "-retention-original"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	active := true
	return func() {
		if !active {
			return
		}
		active = false
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(moved, path); err != nil {
			t.Fatal(err)
		}
	}
}

func exchangeDirectory(
	t *testing.T,
	path string,
	replacement string,
) func() {
	t.Helper()
	original := path + "-retention-original"
	if err := os.Rename(path, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	active := true
	return func() {
		if !active {
			return
		}
		active = false
		if err := os.Rename(path, replacement); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(original, path); err != nil {
			t.Fatal(err)
		}
	}
}

type canonicalRaceFixture struct {
	dataDir        string
	collection     store.DerivedRetentionCollection
	componentIndex int
	byteKind       ByteKind
	canonical      int64
	manifestPath   string
	staticPath     string
	staticTarget   string
	abaPath        string
	abaReplacement string
}

func prepareCanonicalRaceFixture(
	t *testing.T,
	kind string,
) canonicalRaceFixture {
	t.Helper()
	switch kind {
	case "resolver":
		dataDir := t.TempDir()
		state, publication, canonical := installResolverFixture(t, dataDir)
		collection := emptyDerivedStoreCollection()
		collection.Results[2].Summary = store.RetentionComponentSummary{
			ScannedIdentities: 1, ReportedIdentities: 1,
		}
		collection.ResolverAuthorities = []store.ResolverCatalogPublication{publication}
		externalData := t.TempDir()
		externalState, _, _ := installResolverFixture(t, externalData)
		externalRoot := filepath.Join(externalData, "resolver-catalogs")
		if err := os.WriteFile(
			filepath.Join(externalRoot, externalState.Manifest), []byte("alternate\n"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(dataDir, "resolver-catalogs")
		return canonicalRaceFixture{
			dataDir: dataDir, collection: collection,
			componentIndex: 4, byteKind: CanonicalContent, canonical: canonical,
			manifestPath: filepath.Join(root, state.Manifest),
			staticPath:   root, staticTarget: externalRoot,
			abaPath: root, abaReplacement: externalRoot,
		}
	case "caller":
		dataDir := t.TempDir()
		authority := installCallerManifestFixture(t, dataDir)
		collection := emptyDerivedStoreCollection()
		collection.CallerScannedIdentities = 1
		collection.CallerAuthorities = []store.CallerGenerationPublicationSummary{authority}
		repository := callerpublicationid.RepositoryDirectory(authority.Generation.Repository)
		root := filepath.Join(dataDir, "caller-leaves")
		repositoryRoot := filepath.Join(root, repository)
		externalData := t.TempDir()
		externalAuthority := installCallerManifestFixture(t, externalData)
		externalRoot := filepath.Join(externalData, "caller-leaves")
		externalRepositoryRoot := filepath.Join(externalRoot, repository)
		if err := os.WriteFile(
			filepath.Join(externalRepositoryRoot, externalAuthority.ManifestPath),
			[]byte("alternate\n"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		return canonicalRaceFixture{
			dataDir: dataDir, collection: collection,
			componentIndex: 6, byteKind: CanonicalReceipt,
			canonical:    authority.CanonicalBytes,
			manifestPath: filepath.Join(repositoryRoot, authority.ManifestPath),
			staticPath:   repositoryRoot, staticTarget: externalRepositoryRoot,
			abaPath: root, abaReplacement: externalRoot,
		}
	default:
		t.Fatalf("unsupported canonical fixture kind %q", kind)
		return canonicalRaceFixture{}
	}
}

func int64Pointer(value int64) *int64 { return &value }

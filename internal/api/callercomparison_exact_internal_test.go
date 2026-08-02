package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestExactCallerComparisonGapAuthorityConfirmsOnceWithoutRetainedState(
	t *testing.T,
) {
	const (
		oldRepository         = "github.com/acme/authority-old"
		replacementRepository = "github.com/acme/authority-replacement"
	)
	state, callerMap, service := newExactAuthorityGapServices(
		t, oldRepository, replacementRepository,
	)
	query := CallerComparisonQuery{
		Old:         exactAuthorityTestEndpoint(oldRepository),
		Replacement: exactAuthorityTestEndpoint(replacementRepository),
		Level:       "occurrence",
	}
	page, err := service.Compare(t.Context(), query, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.MatchingRowsState != "unavailable" || page.exactSnapshot == "" ||
		page.exactAuthority == "" || len(page.exactAuthority) > callerMapCursorLimit {
		t.Fatalf("comparison gap authority page = %+v", page)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), page.exactSnapshot) ||
		strings.Contains(string(encoded), page.exactAuthority) {
		t.Fatalf("transport exposed hidden comparison authority: %s", encoded)
	}
	state.visibilityChecks = 0
	confirmation, err := service.confirmWorkbenchComparisonSnapshot(
		t.Context(), query, 10, page.exactAuthority,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.visibilityChecks != 4 {
		t.Fatalf(
			"comparison confirmation visibility checks = %d, want one initial and final check per repository",
			state.visibilityChecks,
		)
	}
	if confirmation.Snapshot != page.exactSnapshot ||
		confirmation.MatchingRowsState != "unavailable" ||
		confirmation.Old.Generation != *page.Old.Generation ||
		confirmation.Replacement.Generation != *page.Replacement.Generation {
		t.Fatalf("comparison confirmation = %+v, page = %+v", confirmation, page)
	}
	if callerMap.exact.bindingCount != 0 || len(callerMap.exact.bindings) != 0 ||
		len(callerMap.exact.indexes) != 0 {
		t.Fatalf(
			"gap confirmation retained state: bindings=%d/%d indexes=%d",
			callerMap.exact.bindingCount, len(callerMap.exact.bindings),
			len(callerMap.exact.indexes),
		)
	}
	_, err = service.confirmWorkbenchComparisonSnapshot(
		t.Context(), query, 10, page.exactAuthority+"tampered",
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)
	_, err = service.confirmWorkbenchComparisonSnapshot(
		t.Context(), query, 11, page.exactAuthority,
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)
}

func TestExactCallerComparisonGapAuthorityRejectsABAIncarnation(t *testing.T) {
	const (
		oldRepository         = "github.com/acme/comparison-aba-old"
		replacementRepository = "github.com/acme/comparison-aba-replacement"
	)
	state, _, service := newExactAuthorityGapServices(
		t, oldRepository, replacementRepository,
	)
	state.publications[oldRepository] = exactAuthorityStalePublication(
		oldRepository, "a", "1",
	)
	state.publications[replacementRepository] = exactAuthorityStalePublication(
		replacementRepository, "c", "4",
	)
	query := CallerComparisonQuery{
		Old:         exactAuthorityTestEndpoint(oldRepository),
		Replacement: exactAuthorityTestEndpoint(replacementRepository),
		Level:       "occurrence",
	}
	page, err := service.Compare(t.Context(), query, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var authority exactCallerComparisonAuthority
	if err := service.exact.decodeSigned(
		page.exactAuthority, &authority,
	); err != nil || authority.OldPublication == nil ||
		authority.ReplacementPublication == nil {
		t.Fatalf("stale comparison descriptors = %+v, %v", authority, err)
	}
	state.publications[oldRepository] = exactAuthorityStalePublication(
		oldRepository, "b", "2",
	)
	state.publications[oldRepository] = exactAuthorityStalePublication(
		oldRepository, "a", "3",
	)
	_, err = service.confirmWorkbenchComparisonSnapshot(
		t.Context(), query, 10, page.exactAuthority,
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)
}

func TestExactCallerComparisonProjectionAuthorityRejectsABAIncarnation(
	t *testing.T,
) {
	const (
		oldRepository         = "github.com/acme/projection-aba-old"
		replacementRepository = "github.com/acme/projection-aba-replacement"
	)
	state, _, service := newExactAuthorityGapServices(
		t, oldRepository, replacementRepository,
	)
	old := exactAuthorityStalePublication(oldRepository, "a", "1")
	replacement := exactAuthorityStalePublication(
		replacementRepository, "c", "4",
	)
	state.publications[oldRepository] = old
	state.publications[replacementRepository] = replacement
	describe := func(publication *store.CallerGenerationPublication) callerexecute.PublicationDescriptor {
		summary := exactAuthorityPublicationSummary(*publication)
		descriptor, err := (&callerexecute.PublicationRead{
			Availability: callerexecute.PublicationStale,
			Summary:      &summary,
			Revision:     summary.PublicationRevision,
		}).Descriptor()
		if err != nil {
			t.Fatal(err)
		}
		return descriptor
	}
	generation := func(
		publication *store.CallerGenerationPublication,
	) CallerMapGeneration {
		return CallerMapGeneration{
			State:               string(callerexecute.PublicationFailed),
			Plane:               exactCallerMapPlane,
			Repository:          publication.Generation.Repository,
			GenerationDigest:    publication.Generation.Digest,
			ManifestDigest:      publication.ManifestDigest,
			PairSetDigest:       publication.PairSetDigest,
			PublicationRevision: publication.PublicationRevision,
		}
	}
	oldDescriptor, replacementDescriptor := describe(old), describe(replacement)
	authority := exactCallerComparisonAuthority{
		Schema:                exactCallerComparisonAuthorityVersion,
		QueryDigest:           exactAuthorityDigest("d"),
		OldRepository:         oldRepository,
		ReplacementRepository: replacementRepository,
		OldVisibility:         VisibilityContext{Principal: "user:authority"},
		ReplacementVisibility: VisibilityContext{Principal: "user:authority"},
		OldRepositoryRevision: 7, ReplacementRevision: 7,
		Snapshot: exactAuthorityDigest("e"), MatchingRowsState: "unavailable",
		OldGeneration: generation(old), ReplacementGeneration: generation(replacement),
		OldProjectionFailed: true, ReplacementProjectionFailed: true,
		OldPublication:         &oldDescriptor,
		ReplacementPublication: &replacementDescriptor,
	}
	if !validExactCallerComparisonAuthority(authority) {
		t.Fatalf("projection authority is invalid: %+v", authority)
	}
	state.publications[oldRepository] = exactAuthorityStalePublication(
		oldRepository, "b", "2",
	)
	state.publications[oldRepository] = exactAuthorityStalePublication(
		oldRepository, "a", "3",
	)
	query := CallerComparisonQuery{
		Old:         exactAuthorityTestEndpoint(oldRepository),
		Replacement: exactAuthorityTestEndpoint(replacementRepository),
		Level:       "occurrence",
	}
	_, err := service.confirmWorkbenchComparisonGapSnapshot(
		t.Context(), query, protocolPack{}, protocolPack{}, authority,
		exactComparisonAuthorization{
			repository: state.repositories[oldRepository],
			visibility: authority.OldVisibility,
		},
		exactComparisonAuthorization{
			repository: state.repositories[replacementRepository],
			visibility: authority.ReplacementVisibility,
		},
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)
}

func TestExactCallerComparisonAuthorityGenerationRestoresExcludedGoTests(
	t *testing.T,
) {
	read := &callerexecute.PublicationRead{
		Availability: callerexecute.PublicationCurrent,
		ExpectedGeneration: store.CallerGenerationIdentity{
			Repository: "github.com/acme/excluded-tests",
		},
	}
	for _, test := range []struct {
		name             string
		projectionFailed bool
		count            int
	}{
		{name: "current side in gap", count: 17},
		{name: "projection failure", projectionFailed: true, count: 29},
	} {
		t.Run(test.name, func(t *testing.T) {
			generation := exactComparisonAuthorityGeneration(
				&exactCallerMapService{},
				read,
				test.projectionFailed,
				CallerMapGeneration{ExcludedGoTestRecords: test.count},
			)
			if generation.ExcludedGoTestRecords != test.count {
				t.Fatalf(
					"excluded Go-test records = %d, want %d",
					generation.ExcludedGoTestRecords,
					test.count,
				)
			}
		})
	}
}

func TestExactCallerComparisonAuthorityFitsMaximumPublicationIdentities(
	t *testing.T,
) {
	oldRepository := "o" + strings.Repeat("a", 1023)
	replacementRepository := "r" + strings.Repeat("b", 1023)
	oldBinding, oldPublication, oldGeneration := exactAuthorityMaxFixture(
		t, oldRepository, "a",
	)
	replacementBinding, replacementPublication, replacementGeneration :=
		exactAuthorityMaxFixture(t, replacementRepository, "b")
	oldVisibility := VisibilityContext{
		Principal: "user:max", AuthorizationProvider: "max-v1",
		PermissionSnapshot:         exactAuthorityDigest("c"),
		VisibleRepositorySetDigest: exactAuthorityDigest("d"),
	}
	replacementVisibility := VisibilityContext{
		Principal: "user:max", AuthorizationProvider: "max-v1",
		PermissionSnapshot:         exactAuthorityDigest("e"),
		VisibleRepositorySetDigest: exactAuthorityDigest("f"),
	}
	snapshot := exactCallerComparisonCurrentSnapshot(
		oldVisibility, replacementVisibility,
		oldBinding, replacementBinding,
		ContractCatalogClaim{}, ContractCatalogClaim{},
	)
	service := &exactCallerMapService{}
	authority := exactCallerComparisonAuthority{
		Schema:                exactCallerComparisonAuthorityVersion,
		QueryDigest:           exactAuthorityDigest("1"),
		OldRepository:         oldRepository,
		ReplacementRepository: replacementRepository,
		OldVisibility:         oldVisibility,
		ReplacementVisibility: replacementVisibility,
		OldRepositoryRevision: oldGeneration.PublicationRevision,
		ReplacementRevision:   replacementGeneration.PublicationRevision,
		Snapshot:              snapshot, MatchingRowsState: "exact",
		OldGeneration:          oldGeneration,
		ReplacementGeneration:  replacementGeneration,
		OldPublication:         &oldPublication,
		ReplacementPublication: &replacementPublication,
	}
	if !validExactCallerComparisonAuthority(authority) {
		t.Fatalf("maximum comparison authority is invalid: %+v", authority)
	}
	encoded, err := service.encodeExactAuthority(authority)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > callerMapCursorLimit {
		t.Fatalf("maximum caller comparison authority bytes = %d", len(encoded))
	}
}

func TestExactCallerComparisonCombinedScanBoundary(t *testing.T) {
	for _, test := range []struct {
		name        string
		old         int
		replacement int
		want        bool
	}{
		{name: "empty", want: true},
		{name: "exact cap on old", old: callerMapScanLimit, want: true},
		{name: "exact split cap", old: 25_000, replacement: 25_000, want: true},
		{name: "old cap plus one", old: callerMapScanLimit + 1},
		{name: "split cap plus one", old: 25_000, replacement: 25_001},
		{name: "negative old", old: -1},
		{name: "negative replacement", replacement: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := exactCallerComparisonScanAdmits(
				test.old, test.replacement,
			); got != test.want {
				t.Fatalf(
					"scan admission (%d, %d) = %t, want %t",
					test.old, test.replacement, got, test.want,
				)
			}
		})
	}
}

func TestExactCallerComparisonPositionsStopAtCapPlusOne(t *testing.T) {
	const operation = "/orders.v1.Orders/Get"
	index := &exactCallerIndex{
		records: []exactCallerRecord{
			{classification: "resolved_caller", lineage: "lineage"},
			{classification: "resolved_caller", lineage: "lineage"},
			{classification: "resolved_caller", lineage: "lineage"},
		},
		endpoints: map[exactCallerEndpointKey]exactCallerEndpointIndex{
			{protocol: "grpc", operation: operation}: {
				// The final invalid position proves the traversal returned as
				// soon as the first over-limit row established refusal.
				source: []int{0, 1, 2, 99},
			},
		},
	}
	positions, scanned := exactComparisonPositions(
		index,
		protocolPack{protocol: "protobuf"},
		CallerMapQuery{
			Endpoint:  CallerMapEndpoint{Operation: operation, Lineage: "lineage"},
			Freshness: "any", Resolution: "any",
		},
		2,
	)
	if scanned != 3 || len(positions) != 2 {
		t.Fatalf("positions/scanned = %d/%d, want 2/3", len(positions), scanned)
	}
}

func TestExactCallerComparisonChargesOtherResolvedLineages(t *testing.T) {
	const operation = "/orders.v1.Orders/Get"
	index := &exactCallerIndex{
		records: []exactCallerRecord{
			{classification: "resolved_caller", lineage: "other"},
			{classification: "resolved_caller", lineage: "other"},
			{classification: "resolved_caller", lineage: "other"},
		},
		endpoints: map[exactCallerEndpointKey]exactCallerEndpointIndex{
			{protocol: "grpc", operation: operation}: {
				source: []int{0, 1, 2, 99},
			},
		},
	}
	positions, scanned := exactComparisonPositions(
		index,
		protocolPack{protocol: "protobuf"},
		CallerMapQuery{
			Endpoint:  CallerMapEndpoint{Operation: operation, Lineage: "target"},
			Freshness: "any", Resolution: "any",
		},
		2,
	)
	if scanned != 3 || len(positions) != 0 {
		t.Fatalf("positions/scanned = %d/%d, want 0/3", len(positions), scanned)
	}
}

func TestExactCallerComparisonUnitOrderingIsStrictTotal(t *testing.T) {
	const (
		repository = "repo"
		commit     = "commit"
	)
	index := &exactCallerIndex{records: []exactCallerRecord{
		{path: "z.go", startByte: 1, recordID: "unit-a"},
		{path: "a.go", startByte: 1, recordID: "unit-b"},
		{path: "m.go", startByte: 1, recordID: "unresolved"},
	}}
	source := exactCallerComparisonSource{generation: CallerMapGeneration{
		Repository: repository, Commit: commit,
	}}
	entries := []*exactComparisonBuildEntry{
		{
			identity: exactComparisonIdentity{
				kind: exactComparisonUnit, repository: repository, unit: "a",
			},
			sourceRef: exactComparisonRef{old: true, position: 0},
			unitGroup: "resolved:a",
		},
		{
			identity: exactComparisonIdentity{
				kind: exactComparisonUnit, repository: repository, unit: "b",
			},
			sourceRef: exactComparisonRef{old: true, position: 1},
			unitGroup: "resolved:b",
		},
		{
			identity: exactComparisonIdentity{
				kind: exactComparisonUnresolved, repository: repository,
				commit: commit, path: "m.go", start: 1, end: 2,
			},
			sourceRef: exactComparisonRef{old: true, position: 2},
			unitGroup: "unavailable",
		},
	}
	less := func(left, right *exactComparisonBuildEntry) bool {
		return exactComparisonBuildEntryLess(
			left, right, "unit", index, index, source, source,
		)
	}
	for left := range entries {
		if less(entries[left], entries[left]) {
			t.Fatalf("entry %d sorts before itself", left)
		}
		for right := range entries {
			if less(entries[left], entries[right]) &&
				less(entries[right], entries[left]) {
				t.Fatalf("entries %d and %d sort before each other", left, right)
			}
			for third := range entries {
				if less(entries[left], entries[right]) &&
					less(entries[right], entries[third]) &&
					!less(entries[left], entries[third]) {
					t.Fatalf(
						"ordering is not transitive for entries %d, %d, %d",
						left, right, third,
					)
				}
			}
		}
	}

	permutations := [][3]int{
		{0, 1, 2}, {0, 2, 1}, {1, 0, 2},
		{1, 2, 0}, {2, 0, 1}, {2, 1, 0},
	}
	want := []string{"repo:a", "repo:b", "unresolved:repo@commit:m.go:1-2"}
	for _, permutation := range permutations {
		ordered := []*exactComparisonBuildEntry{
			entries[permutation[0]], entries[permutation[1]], entries[permutation[2]],
		}
		sort.Slice(ordered, func(i, j int) bool { return less(ordered[i], ordered[j]) })
		for position, entry := range ordered {
			if got := exactComparisonDisplayKey(entry.identity); got != want[position] {
				t.Fatalf(
					"permutation %v position %d = %q, want %q",
					permutation, position, got, want[position],
				)
			}
		}
	}
}

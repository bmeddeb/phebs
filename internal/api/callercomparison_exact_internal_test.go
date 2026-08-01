package api

import (
	"sort"
	"testing"
)

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

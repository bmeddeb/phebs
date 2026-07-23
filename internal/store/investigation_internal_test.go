package store

import (
	"slices"
	"testing"
	"time"
)

func TestInvestigationDomainPureRules(t *testing.T) {
	t.Run("ULID shape and uniqueness", func(t *testing.T) {
		seen := make(map[string]bool)
		for range 128 {
			id, err := newULID(time.UnixMilli(1_750_000_000_000))
			if err != nil || !validULID(id) || seen[id] {
				t.Fatalf("newULID = %q, %v; duplicate=%v", id, err, seen[id])
			}
			seen[id] = true
		}
	})

	t.Run("run transition table", func(t *testing.T) {
		rows := []struct {
			from, to RunState
			want     bool
		}{
			{RunQueued, RunEnumerating, true},
			{RunEnumerating, RunAnalyzing, true},
			{RunAnalyzing, RunPublishing, true},
			{RunPublishing, RunPublished, true},
			{RunQueued, RunFailed, true},
			{RunAnalyzing, RunCanceled, true},
			{RunQueued, RunPublishing, false},
			{RunPublished, RunFailed, false},
			{RunFailed, RunCanceled, false},
		}
		for _, row := range rows {
			if got := validRunTransition(row.from, row.to); got != row.want {
				t.Errorf("validRunTransition(%q, %q) = %v, want %v", row.from, row.to, got, row.want)
			}
		}
	})

	t.Run("artifact references canonicalize into identity", func(t *testing.T) {
		base := RunArtifact{
			Scope: "scope", RunID: "run", TerminalStatus: RunPublished,
			SnapshotManifest: "snapshot", InputManifest: "input",
			CoverageLedger: "coverage", EligibilityResult: "eligibility",
			FactReferences: []string{"b", "a", "a"}, PinReferences: []string{"z", "y"},
		}
		permuted := base
		permuted.FactReferences = []string{"a", "b"}
		permuted.PinReferences = slices.Clone(base.PinReferences)
		slices.Reverse(permuted.PinReferences)
		first, err := ComputeRunArtifactID(base)
		if err != nil {
			t.Fatal(err)
		}
		second, err := ComputeRunArtifactID(permuted)
		if err != nil || first != second {
			t.Fatalf("canonical artifact ids = %q and %q, err %v", first, second, err)
		}
	})

	t.Run("event digest survives store time precision", func(t *testing.T) {
		event := RunEvent{
			ID: "01JY0000000000000000000000", RunID: "iru_test", Sequence: 1, Attempt: 1,
			NewState: RunQueued, Actor: "actor", Reason: "created",
			Timestamp: time.Unix(1_750_000_000, 123_456_789),
		}
		before, err := contentDigest(runEventCore(event))
		if err != nil {
			t.Fatal(err)
		}
		event.Timestamp = storeTimestamp(event.Timestamp)
		after, err := contentDigest(runEventCore(event))
		if err != nil || before != after {
			t.Fatalf("event digest changed across store precision: %q != %q, %v", before, after, err)
		}
	})
}

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

	t.Run("attempt rollover is queued and bounded", func(t *testing.T) {
		previous := RunEvent{Attempt: 1, NewState: RunAnalyzing}
		retry := RunEvent{Attempt: 2, PriorState: RunAnalyzing, NewState: RunQueued}
		if !validRunEventTransition(previous, retry) {
			t.Fatal("valid retry attempt rollover was rejected")
		}
		for _, invalid := range []RunEvent{
			{Attempt: 1, PriorState: RunAnalyzing, NewState: RunQueued},
			{Attempt: 3, PriorState: RunAnalyzing, NewState: RunQueued},
			{Attempt: 2, PriorState: RunAnalyzing, NewState: RunAnalyzing},
		} {
			if validRunEventTransition(previous, invalid) {
				t.Fatalf("invalid retry transition accepted: %+v", invalid)
			}
		}
	})

	t.Run("coverage ledger reconciles and canonicalizes failures", func(t *testing.T) {
		ledger := InvestigationCoverageLedger{
			EligibleUnits: 4, Analyzed: 2, Partial: 1, Failed: 1,
			Failures: []InvestigationCoverageFailure{
				{Unit: "z", Code: "FAILED", Retryable: true},
				{Unit: "a", Code: "PARTIAL"},
			},
		}
		first, err := CanonicalInvestigationCoverageLedger(ledger)
		if err != nil {
			t.Fatal(err)
		}
		slices.Reverse(ledger.Failures)
		second, err := CanonicalInvestigationCoverageLedger(ledger)
		if err != nil || first != second {
			t.Fatalf("canonical coverage differs: %q / %q, %v", first, second, err)
		}
		empty := InvestigationCoverageLedger{}
		nilFailures, err := CanonicalInvestigationCoverageLedger(empty)
		if err != nil {
			t.Fatal(err)
		}
		empty.Failures = []InvestigationCoverageFailure{}
		emptyFailures, err := CanonicalInvestigationCoverageLedger(empty)
		if err != nil || nilFailures != emptyFailures {
			t.Fatalf("nil/empty coverage failures differ: %q / %q, %v", nilFailures, emptyFailures, err)
		}
		ledger.Analyzed++
		if _, err := CanonicalInvestigationCoverageLedger(ledger); err == nil {
			t.Fatal("unreconciled coverage was accepted")
		}
	})

	t.Run("coverage parser is closed and canonical", func(t *testing.T) {
		input := `{"failures":[],"excluded":0,"failed":0,"partial":0,"analyzed":1,"eligible_units":1,"schema_version":"investigation-coverage-ledger-v1"}`
		ledger, canonical, err := ParseInvestigationCoverageLedger(input)
		if err != nil || ledger.Analyzed != 1 ||
			canonical != `{"schema_version":"investigation-coverage-ledger-v1","eligible_units":1,"analyzed":1,"partial":0,"failed":0,"excluded":0,"failures":[]}` {
			t.Fatalf("parsed coverage = %+v, %q, %v", ledger, canonical, err)
		}
		for _, invalid := range []string{
			`{"schema_version":"investigation-coverage-ledger-v1","eligible_units":0,"analyzed":0,"partial":0,"failed":0,"excluded":0,"failures":[],"extra":true}`,
			`{"schema_version":"investigation-coverage-ledger-v1","eligible_units":0,"analyzed":0,"partial":0,"failed":0,"excluded":0,"failures":[]} {}`,
			`{"schema_version":"investigation-coverage-ledger-v1","eligible_units":1,"analyzed":0,"partial":0,"failed":0,"excluded":0,"failures":[]}`,
		} {
			if _, _, err := ParseInvestigationCoverageLedger(invalid); err == nil {
				t.Fatalf("invalid coverage accepted: %s", invalid)
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
		nilReferences := base
		nilReferences.FactReferences = nil
		nilReferences.PinReferences = nil
		emptyReferences := nilReferences
		emptyReferences.FactReferences = []string{}
		emptyReferences.PinReferences = []string{}
		nilID, err := ComputeRunArtifactID(nilReferences)
		if err != nil {
			t.Fatal(err)
		}
		emptyID, err := ComputeRunArtifactID(emptyReferences)
		if err != nil || nilID != emptyID {
			t.Fatalf("nil/empty artifact ids = %q and %q, err %v", nilID, emptyID, err)
		}
	})

	t.Run("event digest survives store second precision", func(t *testing.T) {
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

	t.Run("retention owner identity and override policy are closed", func(t *testing.T) {
		owner, err := normalizeRunArtifactRetentionOwner(RunArtifactRetentionOwner{
			ArtifactID: "ira_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Kind:       RunArtifactOwnerInvestigation, OwnerID: "investigation:one",
			AuthorizedBy: "user:owner", Reason: "active investigation",
		})
		if err != nil || owner.Key == "" || owner.ContentDigest == "" {
			t.Fatalf("normalized owner = %+v, %v", owner, err)
		}
		again, err := normalizeRunArtifactRetentionOwner(owner)
		if err != nil || again.Key != owner.Key || again.ContentDigest != owner.ContentDigest {
			t.Fatalf("owner identity is not stable: %+v, %v", again, err)
		}
		for _, policy := range []RunArtifactOverridePolicy{
			RunArtifactOverrideRevocation,
			RunArtifactOverrideMandatoryDeletion,
			RunArtifactOverrideLegalPolicy,
			RunArtifactOverrideApprovedRetention,
		} {
			if !validRunArtifactOverridePolicy(policy) {
				t.Errorf("registered override policy %q rejected", policy)
			}
		}
		if validRunArtifactOverridePolicy("operator_preference") {
			t.Fatal("unregistered override policy accepted")
		}
	})

	t.Run("authorization epochs reject transfer and regrant ABA", func(t *testing.T) {
		base := investigationAuthz{
			InvestigationID: "01JY0000000000000000000000", Owner: "user:owner",
			AuthzRevision: 2, AuthorizationGeneration: "grant-generation-1",
		}
		if !base.sameAuthorization(&base) {
			t.Fatal("equal authorization epochs differ")
		}
		for name, changed := range map[string]investigationAuthz{
			"transfer": {InvestigationID: base.InvestigationID, Owner: "user:new-owner", AuthzRevision: 3, AuthorizationGeneration: base.AuthorizationGeneration},
			"regrant":  {InvestigationID: base.InvestigationID, Owner: base.Owner, AuthzRevision: base.AuthzRevision, AuthorizationGeneration: "grant-generation-2"},
			"object":   {InvestigationID: "01JY0000000000000000000001", Owner: base.Owner, AuthzRevision: base.AuthzRevision, AuthorizationGeneration: base.AuthorizationGeneration},
		} {
			if base.sameAuthorization(&changed) {
				t.Errorf("%s did not change the authorization epoch", name)
			}
		}
	})
}

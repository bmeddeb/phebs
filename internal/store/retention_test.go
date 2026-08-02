package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func newRetentionTestStore(t *testing.T) *Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	store, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocalMemory: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func requireRetentionStatement(
	t *testing.T,
	ctx context.Context,
	store *Surreal,
	statement string,
	vars map[string]any,
) {
	t.Helper()
	results, err := surrealdb.Query[any](ctx, store.db, statement, vars)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("statement %d: %s", index, result.Error.Message)
		}
	}
}

func seedRetentionRows(
	t *testing.T,
	ctx context.Context,
	store *Surreal,
	table, prefix string,
	count int,
	content map[string]any,
) {
	t.Helper()
	ids := make([]string, count)
	for index := range count {
		ids[index] = fmt.Sprintf("%s-%04d", prefix, index)
	}
	requireRetentionStatement(t, ctx, store, `
FOR $id IN $ids {
	CREATE type::record($table, $id) CONTENT $content RETURN NONE
};`, map[string]any{
		"table":   table,
		"ids":     ids,
		"content": content,
	})
}

func retentionOutcomeContent(receipt string, recordedAt time.Time) map[string]any {
	return map[string]any{
		"repo": "example.com/retention", "commit": "commit",
		"unit_digest": "unit", "domain": "domain",
		"disposition": "retryable_failure",
		"generation": map[string]any{
			"extractor": "extractor", "inventory_policy": "inventory",
			"dependency_digest": "dependency", "digest": "generation",
		},
		"candidate_control_failure":  false,
		"receipt_schema":             ExtractionOutcomeReceiptSchema,
		"receipt":                    receipt,
		"recorded_at":                recordedAt,
		"store_schema_version":       evidenceStoreSchemaVersion,
		"evidence_migration_version": evidenceMigrationVersion,
	}
}

func retentionCallerAdmissionContent(canonicalBytes any, recordedAt time.Time) map[string]any {
	return map[string]any{
		"repository": "example.com/admission", "generation": map[string]any{},
		"generation_digest": "generation", "disposition": "admitted",
		"pair_set_digest": "set", "pair_count": 0, "artifact_count": 0,
		"result_count": 0, "abstention_count": 0,
		"canonical_bytes": canonicalBytes, "staging_bytes": int64(13),
		"peak_open_files": 0, "writer_schema": CallerLeafWriterSchema,
		"recorded_at": recordedAt,
	}
}

func coreRetentionComponents() []RetentionComponent {
	return []RetentionComponent{
		RetentionExtractionRun,
		RetentionSnapshotEvidence,
		RetentionAssertion,
		RetentionEvidenceAtom,
		RetentionExtractionAttempt,
		RetentionExtractionOutcome,
		RetentionProofBundlePin,
		RetentionInvestigationArtifactPin,
		RetentionOtherEvidencePin,
		RetentionProofBundle,
		RetentionComponent(JobSync),
		RetentionComponent(JobIndex),
		RetentionComponent(JobFetch),
		RetentionComponent(JobCandidate),
		RetentionComponent(JobExtract),
		RetentionComponent(JobResolverCatalog),
		RetentionComponent(JobCallerLeaf),
		RetentionComponent(JobInvestigate),
		RetentionCallerPublication,
		RetentionCallerAdmission,
		RetentionCallerLeafOutcome,
	}
}

func coreRetentionRequests() []RetentionComponentRequest {
	components := coreRetentionComponents()
	requests := make([]RetentionComponentRequest, len(components))
	for index, component := range components {
		reported, scanned := 79, 80
		if component == RetentionCallerPublication ||
			component == RetentionCallerAdmission ||
			component == RetentionCallerLeafOutcome {
			reported, scanned = 78, 79
		}
		requests[index] = RetentionComponentRequest{
			Component: component, ReportedIdentities: reported,
			ScanIdentities: scanned,
		}
	}
	return requests
}

func TestCoreRetentionEmptyReadyStoreIsExactZero(t *testing.T) {
	store := newRetentionTestStore(t)
	requests := coreRetentionRequests()
	results, err := store.CollectCoreRetention(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(requests) {
		t.Fatalf("results = %d, want %d", len(results), len(requests))
	}
	measurable := map[RetentionComponent]struct{}{
		RetentionExtractionOutcome: {},
		RetentionProofBundle:       {},
		RetentionCallerPublication: {},
		RetentionCallerAdmission:   {},
		RetentionCallerLeafOutcome: {},
	}
	for index, result := range results {
		if result.Component != requests[index].Component || result.Err != nil ||
			result.Summary.ScannedIdentities != 0 ||
			result.Summary.ReportedIdentities != 0 || result.Summary.Truncated {
			t.Fatalf("empty component %d = %+v", index, result)
		}
		_, hasBytes := measurable[result.Component]
		if hasBytes {
			if result.Summary.Bytes == nil || *result.Summary.Bytes != 0 {
				t.Fatalf("empty component %q bytes = %v, want exact zero", result.Component, result.Summary.Bytes)
			}
		} else if result.Summary.Bytes != nil {
			t.Fatalf("empty component %q bytes = %v, want unavailable", result.Component, *result.Summary.Bytes)
		}
	}
}

func TestCoreRetentionAllTwentyOnePlansAndJobStatuses(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()

	runStatuses := []string{"staged", "published", "superseded", "aborted", "deleting"}
	for statusIndex, status := range runStatuses {
		content := map[string]any{
			"run_id": fmt.Sprintf("retention-run-%d", statusIndex), "status": status,
			"retention_quarantined": false, "retention_phase": "",
			"store_schema_version": evidenceStoreSchemaVersion,
		}
		if status == "published" {
			content["published_key"] = "retention-published-key"
		}
		seedRetentionRows(t, ctx, store, "extraction_run", fmt.Sprintf("run-%d", statusIndex), 1, content)
	}
	seedRetentionRows(t, ctx, store, "snapshot_evidence", "snapshot", 1, map[string]any{
		"atom_id": "atom-shared", "association_key": "association-one",
	})
	seedRetentionRows(t, ctx, store, "assertion", "assertion", 1, map[string]any{
		"assertion_key": "assertion-one",
	})
	seedRetentionRows(t, ctx, store, "evidence_atom", "atom", 1, map[string]any{
		"atom_id": "atom-shared",
	})
	attemptStatuses := []string{"staged", "published", "aborted"}
	for statusIndex, status := range attemptStatuses {
		seedRetentionRows(t, ctx, store, "extraction_attempt", fmt.Sprintf("attempt-%d", statusIndex), 1, map[string]any{
			"run_id": fmt.Sprintf("attempt-one-%d", statusIndex), "status": status,
		})
	}
	outcomeDispositions := []DomainOutcomeDisposition{
		DomainOutcomePublished,
		DomainOutcomeUnavailablePrerequisite,
		DomainOutcomeTerminalGenerationRefusal,
		DomainOutcomeRetryableFailure,
	}
	outcomeBytes := int64(0)
	for dispositionIndex, disposition := range outcomeDispositions {
		receipt := fmt.Sprintf(`{"state":%q}`, disposition)
		content := retentionOutcomeContent(receipt, now)
		content["disposition"] = string(disposition)
		seedRetentionRows(t, ctx, store, "extraction_domain_outcome", fmt.Sprintf("outcome-%d", dispositionIndex), 1, content)
		outcomeBytes += int64(len(receipt))
	}
	seedRetentionRows(t, ctx, store, "evidence_pin", "proof-pin", 1, map[string]any{
		"kind": "proof-bundle:pb_one", "run_id": "proof-run",
	})
	seedRetentionRows(t, ctx, store, "evidence_pin", "investigation-pin", 1, map[string]any{
		"kind": "investigation-artifact:artifact-one", "run_id": "investigation-run",
	})
	seedRetentionRows(t, ctx, store, "evidence_pin", "other-pin", 1, map[string]any{
		"kind": "checkpoint:one", "run_id": "checkpoint-run",
	})
	proofContent := `{"schema":"proof-bundle-v1"}`
	seedRetentionRows(t, ctx, store, "proof_bundle", "proof", 1, map[string]any{
		"content": proofContent,
	})

	statuses := []JobStatus{
		StatusPending, StatusClaimed, StatusRunning,
		StatusDone, StatusFailed, StatusCanceled,
	}
	for _, kind := range durableJobKinds {
		for statusIndex, status := range statuses {
			seedRetentionRows(
				t, ctx, store, string(kind),
				fmt.Sprintf("%s-%d", kind, statusIndex), 1,
				map[string]any{
					"target": fmt.Sprintf("%s-target-%d", kind, statusIndex),
					"status": string(status), "attempts": 0,
					"created_at": now,
				},
			)
		}
	}

	digest := "sha256:" + strings.Repeat("0", 64)
	seedRetentionRows(t, ctx, store, "caller_generation_publication", "publication", 1, map[string]any{
		"repository": "example.com/publication", "generation": map[string]any{
			"repository": "example.com/publication", "head_commit": "commit",
			"unit_digest": "unit", "declaration_set_digest": "declarations",
			"candidate_manifest_digest":  "candidate-manifest",
			"candidate_policy_digest":    "candidate-policy",
			"candidate_control_revision": 1,
			"resolver_generation_digest": "resolver-generation",
			"resolver_manifest_digest":   "resolver-manifest",
			"resolver_control_revision":  1,
			"resolver_writer_schema":     "resolver-schema",
			"source_lane_policy":         "source-lane",
			"caller_policy_digest":       "caller-policy",
			"extractor_set_digest":       "extractors",
			"digest":                     "generation",
		},
		"generation_digest": "generation", "pairs": []any{},
		"pair_payload_digest": "payload", "pair_set_digest": "set",
		"pair_count": 0, "artifact_count": 0, "result_count": 0,
		"abstention_count": 0, "canonical_bytes": int64(11),
		"staging_bytes": int64(11), "peak_open_files": 0,
		"manifest_digest": "manifest", "manifest_path": "manifest.json",
		"publication_revision": 1, "publication_incarnation": digest,
		"writer_schema": CallerGenerationPublicationWriterSchema,
		"published_at":  now,
	})
	seedRetentionRows(
		t, ctx, store, "caller_generation_admission", "admission", 1,
		retentionCallerAdmissionContent(int64(13), now),
	)
	terminalAdmission := retentionCallerAdmissionContent(int64(0), now)
	terminalAdmission["repository"] = "example.com/admission-terminal"
	terminalAdmission["disposition"] = string(CallerGenerationTerminalGenerationRefusal)
	seedRetentionRows(
		t, ctx, store, "caller_generation_admission", "admission-terminal", 1,
		terminalAdmission,
	)
	seedRetentionRows(t, ctx, store, "caller_leaf_outcome", "leaf", 1, map[string]any{
		"repository": "example.com/leaf", "generation": map[string]any{},
		"generation_digest": "generation", "pair": map[string]any{},
		"domain": "grpc-caller", "leaf_prefix": "00",
		"disposition":   "succeeded",
		"receipt":       map[string]any{"canonical_bytes": int64(17)},
		"writer_schema": CallerLeafWriterSchema, "recorded_at": now,
	})
	seedRetentionRows(t, ctx, store, "caller_leaf_outcome", "leaf-terminal", 1, map[string]any{
		"repository": "example.com/leaf-terminal", "generation": map[string]any{},
		"generation_digest": "generation-terminal", "pair": map[string]any{},
		"domain": "grpc-caller", "leaf_prefix": "01",
		"disposition":   string(CallerLeafTerminalGenerationRefusal),
		"writer_schema": CallerLeafWriterSchema, "recorded_at": now,
	})

	requests := coreRetentionRequests()
	if len(requests) != 21 {
		t.Fatalf("core requests = %d, want 21", len(requests))
	}
	totalScans := 0
	for _, request := range requests {
		totalScans += request.ScanIdentities
	}
	if totalScans != 1_677 {
		t.Fatalf("core aggregate scan allocation = %d, want 1677", totalScans)
	}
	results, err := store.CollectCoreRetention(ctx, requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(requests) {
		t.Fatalf("results = %d, want %d", len(results), len(requests))
	}

	wantBytes := map[RetentionComponent]int64{
		RetentionExtractionOutcome: outcomeBytes,
		RetentionProofBundle:       int64(len(proofContent)),
		RetentionCallerPublication: 11,
		RetentionCallerAdmission:   13,
		RetentionCallerLeafOutcome: 17,
	}
	for index, result := range results {
		request := requests[index]
		if result.Component != request.Component {
			t.Fatalf("result %d component = %q, want %q", index, result.Component, request.Component)
		}
		if result.Err != nil {
			t.Fatalf("component %q: %v", result.Component, result.Err)
		}
		wantCount := int64(1)
		switch result.Component {
		case RetentionExtractionRun:
			wantCount = int64(len(runStatuses))
		case RetentionExtractionAttempt:
			wantCount = int64(len(attemptStatuses))
		case RetentionExtractionOutcome:
			wantCount = int64(len(outcomeDispositions))
		case RetentionCallerAdmission, RetentionCallerLeafOutcome:
			wantCount = 2
		default:
			if _, job := jobKindForRetentionComponent(result.Component); job {
				wantCount = int64(len(statuses))
			}
		}
		if result.Summary.ScannedIdentities != int(wantCount) ||
			result.Summary.ReportedIdentities != wantCount ||
			result.Summary.Truncated {
			t.Fatalf("component %q summary = %+v, want exact %d", result.Component, result.Summary, wantCount)
		}
		bytes, hasBytes := wantBytes[result.Component]
		if !hasBytes {
			if result.Summary.Bytes != nil {
				t.Fatalf("component %q bytes = %v, want unavailable", result.Component, *result.Summary.Bytes)
			}
			continue
		}
		if result.Summary.Bytes == nil || *result.Summary.Bytes != bytes {
			t.Fatalf("component %q bytes = %v, want %d", result.Component, result.Summary.Bytes, bytes)
		}
	}

	if len(coreRetentionPlans) != len(requests) {
		t.Fatalf("query plans = %d, want exactly %d", len(coreRetentionPlans), len(requests))
	}
}

func jobKindForRetentionComponent(component RetentionComponent) (JobKind, bool) {
	kind := JobKind(component)
	return kind, validJobKind(kind)
}

func TestCoreRetentionExactAtCapAndCapPlusOneExcludesSentinelBytes(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	request := RetentionComponentRequest{
		Component:          RetentionExtractionOutcome,
		ReportedIdentities: 79, ScanIdentities: 80,
	}
	collect := func() RetentionComponentSummary {
		t.Helper()
		results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{request})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Err != nil {
			t.Fatalf("collection = %+v", results)
		}
		return results[0].Summary
	}

	seedRetentionRows(
		t, ctx, store, "extraction_domain_outcome", "bounded-a", 2,
		retentionOutcomeContent("0", time.Now().UTC()),
	)
	under := collect()
	if under.ScannedIdentities != 2 || under.ReportedIdentities != 2 ||
		under.Truncated || under.Bytes == nil || *under.Bytes != 2 {
		t.Fatalf("under-cap summary = %+v", under)
	}

	seedRetentionRows(
		t, ctx, store, "extraction_domain_outcome", "bounded-b", 77,
		retentionOutcomeContent("0", time.Now().UTC()),
	)
	atCap := collect()
	if atCap.ScannedIdentities != 79 || atCap.ReportedIdentities != 79 ||
		atCap.Truncated || atCap.Bytes == nil || *atCap.Bytes != 79 {
		t.Fatalf("at-cap summary = %+v", atCap)
	}

	sentinel := strings.Repeat("x", 1_000)
	seedRetentionRows(
		t, ctx, store, "extraction_domain_outcome", "zz-sentinel", 1,
		retentionOutcomeContent(sentinel, time.Now().UTC()),
	)
	capPlusOne := collect()
	if capPlusOne.ScannedIdentities != 80 ||
		capPlusOne.ReportedIdentities != 79 || !capPlusOne.Truncated ||
		capPlusOne.Bytes == nil || *capPlusOne.Bytes != 79 {
		t.Fatalf("cap-plus-one summary = %+v; sentinel bytes %d must be excluded", capPlusOne, len(sentinel))
	}
}

func TestCoreRetentionPinPartitionsResistDominantNamespaces(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	seedRetentionRows(t, ctx, store, "evidence_pin", "proof-dominant", 81, map[string]any{
		"kind": "proof-bundle:dominant", "run_id": "proof-run",
	})
	seedRetentionRows(t, ctx, store, "evidence_pin", "investigation-sparse", 1, map[string]any{
		"kind": "investigation-artifact:sparse", "run_id": "investigation-run",
	})
	seedRetentionRows(t, ctx, store, "evidence_pin", "other-low", 1, map[string]any{
		"kind": "alpha-owner", "run_id": "low-string-kind",
	})
	seedRetentionRows(t, ctx, store, "evidence_pin", "other-middle", 1, map[string]any{
		"kind": "other", "run_id": "string-kind",
	})
	seedRetentionRows(t, ctx, store, "evidence_pin", "other-high", 1, map[string]any{
		"kind": "z-owner", "run_id": "high-string-kind",
	})

	requests := []RetentionComponentRequest{
		{Component: RetentionProofBundlePin, ReportedIdentities: 79, ScanIdentities: 80},
		{Component: RetentionInvestigationArtifactPin, ReportedIdentities: 79, ScanIdentities: 80},
		{Component: RetentionOtherEvidencePin, ReportedIdentities: 79, ScanIdentities: 80},
	}
	results, err := store.CollectCoreRetention(ctx, requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for _, result := range results {
		if result.Err != nil {
			t.Fatalf("component %q: %v", result.Component, result.Err)
		}
	}
	if got := results[0].Summary; got.ScannedIdentities != 80 ||
		got.ReportedIdentities != 79 || !got.Truncated {
		t.Fatalf("dominant proof partition = %+v", got)
	}
	if got := results[1].Summary; got.ScannedIdentities != 1 ||
		got.ReportedIdentities != 1 || got.Truncated {
		t.Fatalf("sparse investigation partition = %+v", got)
	}
	if got := results[2].Summary; got.ScannedIdentities != 3 ||
		got.ReportedIdentities != 3 || got.Truncated {
		t.Fatalf("other partition across all three index ranges = %+v", got)
	}
}

func TestCoreRetentionEvidencePinKindRejectsNonString(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	results, err := surrealdb.Query[any](ctx, store.db, `
CREATE evidence_pin:retention_invalid CONTENT {
	kind: ["proof-bundle:invalid"],
	member_digest: "digest",
	pin_key: "retention-invalid",
	run_id: "run"
}`, nil)
	if err == nil {
		err = retentionQueryResultsError(results)
	}
	if err == nil {
		t.Fatal("array-valued evidence_pin.kind was accepted")
	}
	requireRetentionStatement(t, ctx, store, `
CREATE evidence_pin:retention_valid CONTENT {
	kind: "proof-bundle:valid",
	member_digest: "digest",
	pin_key: "retention-valid",
	run_id: "run"
}`, nil)
}

func TestCoreRetentionReadinessAndQueryFailuresAreLocalized(t *testing.T) {
	t.Run("readiness", func(t *testing.T) {
		store := newRetentionTestStore(t)
		ctx := t.Context()
		requireRetentionStatement(t, ctx, store, "DELETE $rid RETURN NONE", map[string]any{
			"rid": evidenceMigrationStateID(),
		})
		results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{{
			Component:          RetentionExtractionAttempt,
			ReportedIdentities: 79, ScanIdentities: 80,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || !errors.Is(results[0].Err, ErrRetentionComponentUnavailable) ||
			results[0].Summary != (RetentionComponentSummary{}) {
			t.Fatalf("unready result = %+v", results)
		}
	})

	t.Run("missing pin index is unavailable", func(t *testing.T) {
		store := newRetentionTestStore(t)
		ctx := t.Context()
		requireRetentionStatement(
			t, ctx, store,
			"REMOVE INDEX evidence_pin_kind ON TABLE evidence_pin",
			nil,
		)
		results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{{
			Component: RetentionProofBundlePin, ReportedIdentities: 79, ScanIdentities: 80,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Err == nil ||
			results[0].Summary != (RetentionComponentSummary{}) {
			t.Fatalf("missing-index result = %+v", results)
		}
	})

	t.Run("one readiness failure does not hide its peer", func(t *testing.T) {
		store := newRetentionTestStore(t)
		ctx := t.Context()
		seedRetentionRows(t, ctx, store, "extraction_attempt", "available", 1, map[string]any{
			"run_id": "available", "status": "aborted",
		})
		requireRetentionStatement(
			t, ctx, store,
			"REMOVE INDEX evidence_pin_kind ON TABLE evidence_pin",
			nil,
		)
		results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{
			{Component: RetentionProofBundlePin, ReportedIdentities: 79, ScanIdentities: 80},
			{Component: RetentionExtractionAttempt, ReportedIdentities: 79, ScanIdentities: 80},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 || results[0].Err == nil || results[1].Err != nil ||
			results[1].Summary.ReportedIdentities != 1 {
			t.Fatalf("localized query results = %+v", results)
		}
	})
}

func TestCoreRetentionMissingByteMetricPreservesExactCount(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	seedRetentionRows(t, ctx, store, "proof_bundle", "missing-content", 1, map[string]any{
		"schema_version": "proof-bundle-v1",
	})
	results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{{
		Component: RetentionProofBundle, ReportedIdentities: 79, ScanIdentities: 80,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Err != nil ||
		results[0].Summary.ScannedIdentities != 1 ||
		results[0].Summary.ReportedIdentities != 1 ||
		results[0].Summary.Truncated || results[0].Summary.Bytes != nil {
		t.Fatalf("missing-byte summary = %+v, want exact count and unavailable bytes", results)
	}
}

func TestCoreRetentionMalformedStoredByteMetricPreservesExactCount(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	requireRetentionStatement(
		t, ctx, store,
		"REMOVE FIELD canonical_bytes ON caller_generation_admission",
		nil,
	)
	seedRetentionRows(
		t, ctx, store, "caller_generation_admission", "malformed", 1,
		retentionCallerAdmissionContent("not-a-number", time.Now().UTC()),
	)
	results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{{
		Component: RetentionCallerAdmission, ReportedIdentities: 78, ScanIdentities: 79,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Err != nil ||
		results[0].Summary.ScannedIdentities != 1 ||
		results[0].Summary.ReportedIdentities != 1 ||
		results[0].Summary.Truncated || results[0].Summary.Bytes != nil {
		t.Fatalf("malformed-byte summary = %+v, want exact count and unavailable bytes", results)
	}
}

func TestCoreRetentionCountsSharedAtomsOnce(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	seedRetentionRows(t, ctx, store, "evidence_atom", "shared-atom", 1, map[string]any{
		"atom_id": "ea_shared",
	})
	seedRetentionRows(t, ctx, store, "snapshot_evidence", "association-a", 1, map[string]any{
		"atom_id": "ea_shared", "association_key": "association-a",
	})
	seedRetentionRows(t, ctx, store, "snapshot_evidence", "association-b", 1, map[string]any{
		"atom_id": "ea_shared", "association_key": "association-b",
	})
	results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{
		{Component: RetentionSnapshotEvidence, ReportedIdentities: 79, ScanIdentities: 80},
		{Component: RetentionEvidenceAtom, ReportedIdentities: 79, ScanIdentities: 80},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Err != nil || results[1].Err != nil ||
		results[0].Summary.ReportedIdentities != 2 ||
		results[1].Summary.ReportedIdentities != 1 {
		t.Fatalf("shared atom summaries = %+v", results)
	}
}

type retentionPlanOperator struct {
	operator string
	attrs    map[string]any
}

func retentionPlanOperators(value any) []retentionPlanOperator {
	var operators []retentionPlanOperator
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case *[]surrealdb.QueryResult[any]:
			for _, result := range *typed {
				visit(result.Result)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		case map[string]any:
			if operator, ok := typed["operator"].(string); ok {
				attrs, _ := typed["attributes"].(map[string]any)
				operators = append(operators, retentionPlanOperator{operator: operator, attrs: attrs})
			}
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return operators
}

func requireRetentionExplain(
	t *testing.T,
	ctx context.Context,
	store *Surreal,
	statement string,
	vars map[string]any,
	wantScan, wantTableOrIndex string,
) {
	t.Helper()
	results, err := surrealdb.Query[any](ctx, store.db, statement, vars)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("explain statement %d: %s", index, result.Error.Message)
		}
	}
	operators := retentionPlanOperators(results)
	scans := 0
	for _, operator := range operators {
		if strings.Contains(operator.operator, "Sort") ||
			operator.operator == "UnionIndexScan" {
			t.Fatalf("plan contains unbounded operator %q: %+v", operator.operator, operators)
		}
		if operator.operator != wantScan {
			continue
		}
		scans++
		if fmt.Sprint(operator.attrs["limit"]) != "$scan_limit" {
			t.Fatalf("%s limit = %v, want $scan_limit: %+v", wantScan, operator.attrs["limit"], operators)
		}
		key := "table"
		if wantScan == "IndexScan" {
			key = "index"
		}
		if fmt.Sprint(operator.attrs[key]) != wantTableOrIndex {
			t.Fatalf("%s %s = %v, want %s: %+v", wantScan, key, operator.attrs[key], wantTableOrIndex, operators)
		}
	}
	if scans != 1 {
		t.Fatalf("%s scans = %d, want one: %+v", wantScan, scans, operators)
	}
}

func TestCoreRetentionPlansPushPhysicalLimitsWithoutSorts(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	for index := range 5 {
		seedRetentionRows(
			t, ctx, store, "extraction_attempt", fmt.Sprintf("plan-%d", index), 1,
			map[string]any{"run_id": fmt.Sprintf("plan-run-%d", index), "status": "aborted"},
		)
	}
	requireRetentionExplain(
		t, ctx, store,
		"SELECT id FROM type::table($table) ORDER BY id LIMIT $scan_limit EXPLAIN FULL",
		map[string]any{"table": "extraction_attempt", "scan_limit": 3},
		"TableScan", "extraction_attempt",
	)

	for _, test := range []struct {
		name  string
		where string
		vars  map[string]any
	}{
		{
			name: "proof prefix", where: "kind >= $lower AND kind < $upper",
			vars: map[string]any{"lower": "proof-bundle:", "upper": "proof-bundle;"},
		},
		{
			name: "investigation prefix", where: "kind >= $lower AND kind < $upper",
			vars: map[string]any{"lower": "investigation-artifact:", "upper": "investigation-artifact;"},
		},
		{
			name: "other before investigation", where: "kind < $upper",
			vars: map[string]any{"upper": "investigation-artifact:"},
		},
		{
			name: "other between namespaces", where: "kind >= $lower AND kind < $upper",
			vars: map[string]any{"lower": "investigation-artifact;", "upper": "proof-bundle:"},
		},
		{
			name: "other after proof", where: "kind >= $lower",
			vars: map[string]any{"lower": "proof-bundle;"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.vars["scan_limit"] = 3
			requireRetentionExplain(
				t, ctx, store,
				"SELECT id FROM evidence_pin WITH INDEX evidence_pin_kind WHERE "+test.where+" LIMIT $scan_limit EXPLAIN FULL",
				test.vars, "IndexScan", "evidence_pin_kind",
			)
		})
	}
}

func TestCoreRetentionAllocationUsesSafetyBoundsNotAPISplit(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	for _, component := range []RetentionComponent{
		RetentionExtractionAttempt,
		RetentionCallerPublication,
	} {
		results, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{{
			Component: component, ReportedIdentities: 1, ScanIdentities: 2,
		}})
		if err != nil {
			t.Fatalf("safe reduced allocation for %q: %v", component, err)
		}
		if len(results) != 1 || results[0].Err != nil ||
			results[0].Summary.ReportedIdentities != 0 {
			t.Fatalf("safe reduced allocation for %q = %+v", component, results)
		}
	}

	for _, test := range []struct {
		name     string
		reported int
		scan     int
	}{
		{name: "zero", reported: 0, scan: 1},
		{name: "above component cap", reported: 80, scan: 81},
		{name: "missing cap-plus-one sentinel", reported: 1, scan: 1},
		{name: "extra scan work", reported: 1, scan: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{{
				Component:          RetentionExtractionAttempt,
				ReportedIdentities: test.reported,
				ScanIdentities:     test.scan,
			}})
			if err == nil {
				t.Fatalf("allocation %d/%d was accepted", test.reported, test.scan)
			}
		})
	}

	overAggregate := make([]RetentionComponentRequest, len(coreRetentionComponents()))
	for index, component := range coreRetentionComponents() {
		overAggregate[index] = RetentionComponentRequest{
			Component: component, ReportedIdentities: 79, ScanIdentities: 80,
		}
	}
	if _, err := store.CollectCoreRetention(ctx, overAggregate); err == nil {
		t.Fatal("aggregate 1659/1680 allocation was accepted")
	}
}

func TestRetentionQueryResultsErrorRequiresOneEnvelope(t *testing.T) {
	var nilResults *[]surrealdb.QueryResult[[]retentionRow]
	empty := []surrealdb.QueryResult[[]retentionRow]{}
	oneEmptyResult := []surrealdb.QueryResult[[]retentionRow]{{
		Result: []retentionRow{},
	}}
	twoResults := []surrealdb.QueryResult[[]retentionRow]{{}, {}}
	for _, test := range []struct {
		name    string
		results *[]surrealdb.QueryResult[[]retentionRow]
		wantErr bool
	}{
		{name: "nil", results: nilResults, wantErr: true},
		{name: "zero envelopes", results: &empty, wantErr: true},
		{name: "one empty row set", results: &oneEmptyResult},
		{name: "two envelopes", results: &twoResults, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := retentionQueryResultsError(test.results)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %t", err, test.wantErr)
			}
		})
	}
}

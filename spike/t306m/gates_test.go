package t306m

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestT306MRetainedDecision(t *testing.T) {
	want := RetainedResults()
	got := loadResults(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained T30.6m result drifted:\ngot  %+v\nwant %+v", got, want)
	}
	if got.Schema != Schema || got.Decision != "unbounded" || got.CleanupChanged ||
		got.Implementation.FitsOnePR {
		t.Fatalf("invalid decision posture: %+v", got)
	}

	names := make([]string, 0, len(got.Owners))
	accumulating := 0
	components := 0
	for _, owner := range got.Owners {
		names = append(names, owner.Name)
		components += len(owner.Components)
		if len(owner.Components) == 0 || owner.DecisionRelation == "" {
			t.Fatalf("owner lacks structured components/relation: %+v", owner)
		}
		if owner.DeletionAuthorized {
			t.Fatalf("decision unexpectedly authorizes deletion for %q", owner.Name)
		}
		if owner.Accumulating {
			accumulating++
		}
	}
	wantNames := []string{
		"evidence_publications",
		"extraction_attempts",
		"extraction_outcomes",
		"evidence_pins",
		"proof_bundles",
		"durable_job_history",
		"investigation_workbench_rows",
		"candidate_artifacts",
		"focused_indexes",
		"resolver_catalogs",
		"caller_rows",
		"caller_artifacts",
	}
	if !slices.Equal(names, wantNames) || accumulating != 9 || components != 52 {
		t.Fatalf("owner coverage = %v / %d accumulating / %d components, want %v / 9 / 52", names, accumulating, components, wantNames)
	}
	wantRelations := map[string]string{
		"evidence_publications":        RelationSelected,
		"extraction_attempts":          RelationSelected,
		"extraction_outcomes":          RelationExisting,
		"evidence_pins":                RelationMixed,
		"proof_bundles":                RelationConfigured,
		"durable_job_history":          RelationIncidental,
		"investigation_workbench_rows": RelationIncidental,
		"candidate_artifacts":          RelationSelected,
		"focused_indexes":              RelationExisting,
		"resolver_catalogs":            RelationExisting,
		"caller_rows":                  RelationSelected,
		"caller_artifacts":             RelationSelected,
	}
	for name, relation := range wantRelations {
		if actual := ownerByName(t, got, name).DecisionRelation; actual != relation {
			t.Fatalf("owner %q relation = %q, want %q", name, actual, relation)
		}
	}
}

func TestT306MIncidentalOwnerComponents(t *testing.T) {
	results := loadResults(t)
	jobs := ownerByName(t, results, "durable_job_history")
	wantJobs := []string{
		string(store.JobSync),
		string(store.JobIndex),
		string(store.JobFetch),
		string(store.JobCandidate),
		string(store.JobExtract),
		string(store.JobResolverCatalog),
		string(store.JobCallerLeaf),
		string(store.JobInvestigate),
	}
	if !slices.Equal(jobs.Components, wantJobs) ||
		jobs.DecisionRelation != RelationIncidental || !jobs.Accumulating {
		t.Fatalf("durable job owner = %+v, want exact incidental components %v", jobs, wantJobs)
	}

	investigations := ownerByName(t, results, "investigation_workbench_rows")
	wantInvestigations := []string{
		"investigation",
		"investigation_revision",
		"investigation_change_brief",
		"investigation_workbench_mutation",
		"investigation_workbench_disposition",
		"investigation_run",
		"investigation_run_event",
		"investigation_run_artifact",
		"investigation_artifact_owner",
		"investigation_artifact_owner_release",
		"investigation_artifact_retention_override",
		"investigation_decision",
		"investigation_disposition",
		"investigation_baseline_designation",
		"investigation_grant",
		"investigation_cursor",
		"investigation_creation",
		"investigation_consumer_snapshot",
		"investigation_consumer_edge_ledger",
		"investigation_review_projection",
		"investigation_review_item",
		"investigation_dossier",
		"investigation_watch",
		"investigation_watch_revision",
	}
	if !slices.Equal(investigations.Components, wantInvestigations) ||
		investigations.DecisionRelation != RelationIncidental ||
		!investigations.Accumulating ||
		slices.Contains(investigations.Components, "investigation_run_job") {
		t.Fatalf("Investigation owner = %+v, want exact non-job components %v", investigations, wantInvestigations)
	}

	schema, err := os.ReadFile("../../internal/store/schema.surql")
	if err != nil {
		t.Fatal(err)
	}
	tablePattern := regexp.MustCompile(
		`(?m)^DEFINE TABLE IF NOT EXISTS (investigation(?:_[a-z0-9]+)*) SCHEMALESS;$`,
	)
	var schemaComponents []string
	jobFound := false
	for _, match := range tablePattern.FindAllSubmatch(schema, -1) {
		name := string(match[1])
		if name == string(store.JobInvestigate) {
			jobFound = true
			continue
		}
		schemaComponents = append(schemaComponents, name)
	}
	if !jobFound || len(schemaComponents) != 24 ||
		!slices.Equal(schemaComponents, investigations.Components) {
		t.Fatalf(
			"schema Investigation components = %v (job=%t), want exact 24 %v",
			schemaComponents, jobFound, investigations.Components,
		)
	}
}

func TestT306MProofBundleAndPinLifecycles(t *testing.T) {
	if got := (config.ProofBundles{}).RetentionFor(); got != 0 {
		t.Fatalf("default proof retention = %s, want disabled", got)
	}
	if got := (config.ProofBundles{Retention: "24h"}).RetentionFor(); got != 24*time.Hour {
		t.Fatalf("positive proof retention = %s, want 24h", got)
	}

	results := loadResults(t)
	proof := ownerByName(t, results, "proof_bundles")
	if proof.DecisionRelation != RelationConfigured || !proof.Accumulating ||
		!slices.Equal(proof.Components, []string{"proof_bundle"}) ||
		!strings.Contains(
			proof.Retention,
			"deletes the expired bundle and exactly its proof-bundle:<bundle_id> evidence pins but no extraction evidence; the independent evidence sweep may later reclaim newly unpinned superseded evidence when otherwise eligible",
		) {
		t.Fatalf("proof owner = %+v", proof)
	}
	pins := ownerByName(t, results, "evidence_pins")
	wantPins := []string{
		"evidence_pin[kind=proof-bundle:<bundle_id>]",
		"evidence_pin[kind=investigation-artifact:<artifact_id>]",
		"evidence_pin[kind=<other exact store-accepted value>]",
	}
	if pins.DecisionRelation != RelationMixed ||
		!slices.Equal(pins.Components, wantPins) {
		t.Fatalf("pin namespaces = %v, want %v", pins.Components, wantPins)
	}
}

func TestT306MDurableJobGrowthReceipt(t *testing.T) {
	defaultResync := (config.Sync{}).ResyncEvery()
	if defaultResync != time.Hour {
		t.Fatalf("default resync = %s, want 1h", defaultResync)
	}
	wantResyncs := int64((365 * 24 * time.Hour) / defaultResync)
	receipt := loadResults(t).DurableJobGrowth
	if receipt.DefaultResyncInterval != defaultResync.String() ||
		!slices.Contains(receipt.Assumptions, "one healthy continuously draining remote connection") ||
		!slices.Contains(receipt.Assumptions, "each resync tick produces one connection_sync_job row and one indexing_job row per repository whether or not repository content changed") ||
		!slices.Contains(receipt.ExcludedClaims, "no time-to-degradation or capacity-exhaustion estimate") ||
		len(receipt.Points) != 3 {
		t.Fatalf("durable-job growth receipt = %+v", receipt)
	}
	for index, repositories := range []int{1, 10, 100} {
		point := receipt.Points[index]
		if point.Repositories != repositories ||
			point.ResyncsPerCommonYear != wantResyncs ||
			point.IndexingJobRowsPerCommonYear != int64(repositories)*wantResyncs ||
			point.ConnectionSyncJobRowsPerCommonYear != wantResyncs {
			t.Fatalf("durable-job growth point %d = %+v", repositories, point)
		}
	}
}

func TestT306MFollowupSplit(t *testing.T) {
	results := loadResults(t)
	proof := results.Implementation
	componentCount := func(names ...string) int {
		total := 0
		for _, name := range names {
			total += len(ownerByName(t, results, name).Components)
		}
		return total
	}
	if componentCount(
		"evidence_publications", "extraction_attempts", "extraction_outcomes",
		"evidence_pins", "proof_bundles", "durable_job_history", "caller_rows",
	) != 21 ||
		componentCount("investigation_workbench_rows") != 24 ||
		componentCount("candidate_artifacts", "focused_indexes", "resolver_catalogs", "caller_artifacts") != 7 {
		t.Fatalf("collector component split does not equal 21 / 24 / 7")
	}
	allocationTotals := func(names ...string) (reported int, scanned int) {
		componentIndex := 0
		base := api.RetentionStatusAggregateReportedIdentityAllocation / api.RetentionStatusComponentCount
		remainder := api.RetentionStatusAggregateReportedIdentityAllocation % api.RetentionStatusComponentCount
		for _, owner := range results.Owners {
			selected := slices.Contains(names, owner.Name)
			for range owner.Components {
				componentReported := base
				if componentIndex < remainder {
					componentReported++
				}
				if selected {
					reported += componentReported
					scanned += componentReported + 1
				}
				componentIndex++
			}
		}
		return reported, scanned
	}
	qReported, qScanned := allocationTotals("investigation_workbench_rows")
	pqReported, pqScanned := allocationTotals(
		"evidence_publications", "extraction_attempts", "extraction_outcomes",
		"evidence_pins", "proof_bundles", "durable_job_history", "caller_rows",
		"investigation_workbench_rows",
	)
	if qReported != 1_894 || qScanned != 1_918 ||
		pqReported != 3_550 || pqScanned != 3_595 {
		t.Fatalf("T30.6q and combined allocations = %d/%d and %d/%d", qReported, qScanned, pqReported, pqScanned)
	}
	wantTickets := []struct {
		name      string
		sizeProof []string
	}{
		{"T30.6n", []string{
			"one queue subsystem",
			"two lifetime-history consumers",
			"one active-row migration fence",
			"table-driven coverage across all eight job kinds",
		}},
		{"T30.6o", []string{
			"fixed registry of twelve owners and fifty-two declared components",
			"one fixed response, pre-inventory administrator authorization, and warning surface",
			"one shared fixed-work budget and completeness model",
			"table-driven registry, authorization, zero-scan, and response-bound coverage",
			"no store or filesystem collector implementation",
		}},
		{"T30.6p", []string{
			"seven owners and twenty-one declared components",
			"four bounded evidence-table scans with distinct shared-atom accounting",
			"one generic bounded SurrealDB table-summary path",
			"table-driven bounds and completeness coverage",
			"no filesystem adapters",
		}},
		{"T30.6q", []string{
			"one owner and twenty-four exact component tables",
			"one fixed at-most-twenty-four-name catalog presence result plus at most twenty-four bounded record-ID table scans",
			"1,894/1,918 T30.6q and 3,550/3,595 combined reported/scanned identity bounds",
			"twenty-five T30.6q and fifty-three combined query-call bounds with eighty selected record IDs, twenty-four catalog names, and twenty-four summaries retained per request",
			"table-driven exact-table, aggregate-row, localized-failure, weak-consistency, bounds, and completeness coverage",
		}},
		{"T30.6r", []string{
			"four owners and seven declared components",
			"four fixed filesystem adapters plus bounded store-authority lookups",
			"explicit directory-scan budgets and cap-plus-one behavior",
			"table-driven authority, inventory-bound, and completeness coverage",
		}},
	}
	if proof.FitsOnePR || len(proof.Tickets) != len(wantTickets) ||
		proof.DownstreamGate != "T30.7 depends on completion through T30.6r" {
		t.Fatalf("implementation envelope = %+v", proof)
	}
	for index, want := range wantTickets {
		ticket := proof.Tickets[index]
		if ticket.Ticket != want.name || !ticket.FitsOnePR ||
			!slices.Equal(ticket.SizeProof, want.sizeProof) {
			t.Fatalf("implementation ticket %d = %+v, want %s / %v", index, ticket, want.name, want.sizeProof)
		}
	}

	n, o, p, q, r := proof.Tickets[0], proof.Tickets[1], proof.Tickets[2], proof.Tickets[3], proof.Tickets[4]
	if !slices.Contains(n.Changes, "stop both the first upgraded store Open and steady-state Open from loading and sorting terminal history across all eight job tables") ||
		!slices.Contains(n.Changes, "report legacy latest state as explicitly partial or unavailable until bounded resumable reconstruction completes") ||
		!slices.Contains(n.Excluded, "first-open full-history index build or backfill") ||
		!slices.Contains(o.Changes, "administrator authorization completes before any store or filesystem inventory or cache touch; unauthorized requests consume none of the 52-component scan budget") ||
		!slices.Contains(o.Changes, "all collectors report explicitly unavailable and perform zero inventory scans until their follow-up ticket lands") ||
		!slices.Contains(o.Excluded, "store or filesystem inventory collectors and inventory-cache population") ||
		!slices.Contains(p.Changes, "reuse existing record and evidence-pin kind indexes without an install or backfill") ||
		!slices.Contains(p.Excluded, "first-open full-history index build or backfill") ||
		!slices.Contains(q.Changes, "bounded aggregate collector for investigation_workbench_rows across its exact twenty-four component tables") ||
		!slices.Contains(q.Changes, "one server-side INFO FOR DB intersection returning at most twenty-four allowlisted table names plus at most twenty-four direct record-ID-ordered table scans with physical limit pushdown and no new index or backfill") ||
		!slices.Contains(q.Changes, "non-transferable allocation of 1,894 reported and 1,918 scanned identities; T30.6p plus T30.6q remain bounded at 3,550 reported and 3,595 scanned identities") ||
		!slices.Contains(q.Changes, "at most twenty-five T30.6q query calls and fifty-three T30.6p-plus-T30.6q calls with at most eighty selected record IDs, twenty-four catalog names, and twenty-four summaries retained per request") ||
		!slices.Contains(q.Changes, "missing tables and row-query failures remain localized while successful summaries are weakly consistent") ||
		!slices.Contains(q.Changes, "per-request bounds have no retention-specific cache or concurrency gate, so concurrent authorized requests multiply them independently") ||
		!slices.Contains(q.Excluded, "new query index, schema backfill, or first-open full-history reconstruction") ||
		!slices.Contains(r.Changes, "bound every installation-root and repository-directory scan with explicit cap-plus-one and independently labeled completeness") ||
		!slices.Contains(r.Excluded, "unbounded directory walk or filesystem inventory") {
		t.Fatalf("implementation boundaries = %+v", proof)
	}
}

func TestT306MStatusBoundAndWarning(t *testing.T) {
	contract := loadResults(t).Status
	if StatusReportedIdentityLimit != api.RetentionStatusReportedIdentityLimit ||
		StatusScanIdentityLimit != api.RetentionStatusScanIdentityLimit ||
		StatusComponentCount != api.RetentionStatusComponentCount ||
		StatusAggregateReportedIdentityAllocation != api.RetentionStatusAggregateReportedIdentityAllocation ||
		StatusAggregateScanIdentityAllocation != api.RetentionStatusAggregateScanIdentityAllocation ||
		StatusResponseByteLimit != api.RetentionStatusResponseByteLimit ||
		WarningCode != api.RetentionStatusWarningCode {
		t.Fatalf("retained and production status budgets disagree")
	}
	if contract.ReportedIdentityLimitPerSummary != StatusReportedIdentityLimit ||
		contract.ScanIdentityLimitPerSummary != StatusScanIdentityLimit ||
		contract.AggregateReportedIdentityAllocation != StatusAggregateReportedIdentityAllocation ||
		contract.AggregateScanIdentityAllocation != StatusAggregateScanIdentityAllocation ||
		contract.EncodedResponseByteLimit != StatusResponseByteLimit ||
		contract.InventoryScope != "the twelve declared groups cover T30.6 publication and adjacent persisted domains, not every database table; audit, analytics, authentication, and other installation state retain their separately documented lifecycles" ||
		contract.ComponentCoverage == "" || contract.ComponentScanAllocation == "" ||
		contract.WarningHeader != api.RetentionStatusWarningHeader ||
		contract.WarningCode != api.RetentionStatusWarningCode ||
		!slices.Equal(contract.ByteMetricKinds, []string{
			string(api.RetentionStatusByteLogicalEncoded),
			string(api.RetentionStatusByteCanonicalContent),
			string(api.RetentionStatusByteCanonicalReceipt),
			string(api.RetentionStatusByteApparentFile),
			string(api.RetentionStatusBytePhysicalDatabase),
		}) ||
		contract.ByteMetricCombinationRule != "ordered metrics are independent and non-combinable across kinds; never sum them" ||
		contract.ProofBundleRetentionControl != (StatusRetentionControl{
			ConfigKey:              "proof_bundles.retention",
			DefaultState:           "disabled",
			PositiveLifetimeEffect: ProofBundlePositiveLifetimeEffect,
		}) {
		t.Fatalf("status contract = %+v", contract)
	}
	if ProofBundlePositiveLifetimeEffect != api.RetentionStatusProofBundlePositiveLifetimeEffect {
		t.Fatal("retained and production proof-bundle lifetime effects disagree")
	}
	values := make([]int64, StatusScanIdentityLimit)
	for index := range values {
		values[index] = 1 << 20
	}

	exact := SummarizeStatus(values[:StatusReportedIdentityLimit])
	if exact.ScannedIdentities != StatusReportedIdentityLimit ||
		metricValue(t, exact.Count) != StatusReportedIdentityLimit ||
		metricValue(t, exact.Bytes) != int64(StatusReportedIdentityLimit)<<20 ||
		exact.Count.Completeness != "exact" || exact.Bytes.Completeness != "exact" ||
		exact.Truncated ||
		exact.WarningCode != WarningCode {
		t.Fatalf("exact cap summary = %+v", exact)
	}

	lowerBound := SummarizeStatus(values)
	if lowerBound.ScannedIdentities != StatusScanIdentityLimit ||
		metricValue(t, lowerBound.Count) != StatusReportedIdentityLimit ||
		metricValue(t, lowerBound.Bytes) != int64(StatusReportedIdentityLimit)<<20 ||
		lowerBound.Count.Completeness != "lower_bound" ||
		lowerBound.Bytes.Completeness != "lower_bound" || !lowerBound.Truncated ||
		lowerBound.WarningCode != WarningCode {
		t.Fatalf("cap+1 summary = %+v", lowerBound)
	}

	empty := SummarizeStatus(nil)
	if empty.ScannedIdentities != 0 || metricValue(t, empty.Count) != 0 ||
		metricValue(t, empty.Bytes) != 0 || empty.Count.Completeness != "exact" ||
		empty.Bytes.Completeness != "exact" || empty.Truncated ||
		empty.WarningCode != WarningCode {
		t.Fatalf("empty summary = %+v", empty)
	}

	for _, invalid := range [][]int64{{-1}, {math.MaxInt64, 1}} {
		summary := SummarizeStatus(invalid)
		if summary.Count.Completeness != "exact" || summary.Count.Value == nil ||
			summary.Bytes.Completeness != "unavailable" || summary.Bytes.Value != nil {
			t.Fatalf("invalid byte summary = %+v", summary)
		}
	}
}

func TestT306MProductionCapacityInputs(t *testing.T) {
	results := loadResults(t)
	assertCapacity(t, results, "extraction_outcomes", "receipt_bytes_per_outcome", store.MaxExtractionOutcomeReceiptBytes, "bytes", "enforced")
	assertCapacity(t, results, "candidate_artifacts", "local_projection_artifacts_per_valid_publication", int64(candidate.MaxLocalProjectionArtifacts), "identities", "enforced")
	assertCapacity(t, results, "candidate_artifacts", "local_projection_content_bytes_per_valid_publication", candidate.MaxLocalProjectionContentBytes, "bytes", "enforced")
	assertCapacity(t, results, "resolver_catalogs", "member_count_per_publication", int64(resolvercatalog.MaxMembers), "identities", "enforced")
	assertCapacity(t, results, "resolver_catalogs", "canonical_bytes_per_publication", resolvercatalog.MaxCatalogContentBytes, "bytes", "enforced")
	assertCapacity(t, results, "resolver_catalogs", "catalog_root_inventory_scan_entries", int64(resolvercatalog.MaxDirectoryEntries), "identities", "enforced_operational")
	assertCapacity(t, results, "resolver_catalogs", "modeled_clean_replacement_disk_bytes", resolvercatalog.MaxPublicationDiskBytes, "bytes", "design")
	assertCapacity(t, results, "caller_artifacts", "repository_directory_entries", int64(callerleaf.MaxRepositoryDirectoryEntries), "identities", "enforced")
	assertCapacity(t, results, "caller_artifacts", "canonical_bytes_per_admitted_complete_generation", callerleaf.MaxAggregateCanonicalBytes, "bytes", "enforced")
	assertCapacity(t, results, "caller_artifacts", "incomplete_cap_plus_one_success_bytes", callerleaf.MaxAggregateCanonicalBytes+callerleaf.MaxCanonicalBytesPerPair, "bytes", "derived")
}

func TestT306MEvidenceAdmissionInput(t *testing.T) {
	raw, err := os.ReadFile("../t201/results-current-writer-v7.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		AdmissionRows int `json:"admission_rows"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.AdmissionRows != EvidenceAdmissionRows {
		t.Fatalf("evidence admission = %d, want %d", receipt.AdmissionRows, EvidenceAdmissionRows)
	}
}

func loadResults(t *testing.T) Results {
	t.Helper()
	raw, err := os.ReadFile("results.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var results Results
	if err := decoder.Decode(&results); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("results.json has trailing content: %v", err)
	}
	canonical, err := json.MarshalIndent(RetainedResults(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		t.Fatal("results.json is not the canonical retained encoding")
	}
	return results
}

func metricValue(t *testing.T, metric StatusMetric) int64 {
	t.Helper()
	if metric.Value == nil {
		t.Fatalf("metric %q is unavailable: %+v", metric.Unit, metric)
	}
	return *metric.Value
}

func ownerByName(t *testing.T, results Results, name string) Owner {
	t.Helper()
	for _, owner := range results.Owners {
		if owner.Name == name {
			return owner
		}
	}
	t.Fatalf("missing owner %q", name)
	return Owner{}
}

func assertCapacity(
	t *testing.T,
	results Results,
	ownerName, boundName string,
	want int64,
	unit, kind string,
) {
	t.Helper()
	for _, owner := range results.Owners {
		if owner.Name != ownerName {
			continue
		}
		for _, bound := range owner.Bounds {
			if bound.Name == boundName {
				if bound.Value != want || bound.Unit != unit || bound.Kind != kind {
					t.Fatalf("%s/%s = %+v, want %d %s (%s)", ownerName, boundName, bound, want, unit, kind)
				}
				return
			}
		}
	}
	t.Fatalf("missing capacity bound %s/%s", ownerName, boundName)
}

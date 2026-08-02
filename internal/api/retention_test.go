package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestRetentionStatusAuthorizationPrecedesInventoryWork(t *testing.T) {
	tests := []struct {
		name    string
		isAdmin func(context.Context) bool
	}{
		{name: "missing authorization hook"},
		{name: "non-administrator", isAdmin: func(context.Context) bool { return false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			handler := api.New(api.Options{
				Version: "test",
				IsAdmin: tt.isAdmin,
				RetentionStatusSource: func(context.Context, *api.RetentionStatus) error {
					calls++
					return nil
				},
			})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/retention-status", nil),
			)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d (%s), want 403", recorder.Code, recorder.Body)
			}
			assertRetentionWarningHeader(t, recorder)
			if calls != 0 {
				t.Fatalf("inventory source calls = %d, want 0", calls)
			}
		})
	}
}

func TestRetentionStatusEmptyInstallationFreezesRegistryAndUnavailableMetrics(t *testing.T) {
	status, encoded := getRetentionStatus(t, nil)
	if status.SchemaVersion != api.RetentionStatusSchema {
		t.Fatalf("schema = %q, want %q", status.SchemaVersion, api.RetentionStatusSchema)
	}
	if status.SchemaLink != api.RetentionStatusSchemaLink {
		t.Fatalf("schema link = %q, want %q", status.SchemaLink, api.RetentionStatusSchemaLink)
	}
	if status.WarningCode != api.RetentionStatusWarningCode {
		t.Fatalf("warning = %q, want %q", status.WarningCode, api.RetentionStatusWarningCode)
	}
	if len(status.Owners) != api.RetentionStatusOwnerCount {
		t.Fatalf("owners = %d, want %d", len(status.Owners), api.RetentionStatusOwnerCount)
	}

	want := []struct {
		owner            string
		scope            string
		decisionRelation string
		accumulating     bool
		components       []string
	}{
		{"evidence_publications", "repository/commit/unit/domain", "selected_t306m_unbounded_retention", true, []string{"extraction_run", "snapshot_evidence", "assertion", "evidence_atom"}},
		{"extraction_attempts", "repository/commit/unit/domain", "selected_t306m_unbounded_retention", true, []string{"extraction_attempt"}},
		{"extraction_outcomes", "repository/domain", "existing_owner_lifecycle_unchanged", false, []string{"extraction_domain_outcome"}},
		{"evidence_pins", "run/kind", "mixed_owner_lifecycles_not_selected_by_t306m", true, []string{
			"evidence_pin[kind=proof-bundle:<bundle_id>]",
			"evidence_pin[kind=investigation-artifact:<artifact_id>]",
			"evidence_pin[kind=<other exact store-accepted value>]",
		}},
		{"proof_bundles", "immutable bundle", "configured_owner_lifecycle_unchanged", true, []string{"proof_bundle"}},
		{"durable_job_history", "queue-table/target/auto-id", "incidental_growth_requires_separate_decision", true, []string{
			"connection_sync_job", "indexing_job", "repo_fetch_job",
			"candidate_manifest_job", "extraction_job", "resolver_catalog_job",
			"caller_leaf_job", "investigation_run_job",
		}},
		{"investigation_workbench_rows", "investigation/revision/run/artifact/review/watch", "incidental_growth_requires_separate_decision", true, []string{
			"investigation", "investigation_revision", "investigation_change_brief",
			"investigation_workbench_mutation", "investigation_workbench_disposition",
			"investigation_run", "investigation_run_event", "investigation_run_artifact",
			"investigation_artifact_owner", "investigation_artifact_owner_release",
			"investigation_artifact_retention_override", "investigation_decision",
			"investigation_disposition", "investigation_baseline_designation",
			"investigation_grant", "investigation_cursor", "investigation_creation",
			"investigation_consumer_snapshot", "investigation_consumer_edge_ledger",
			"investigation_review_projection", "investigation_review_item",
			"investigation_dossier", "investigation_watch", "investigation_watch_revision",
		}},
		{"candidate_artifacts", "repository/generation", "selected_t306m_unbounded_retention", true, []string{
			"candidate_manifest_publication", "$DATA/candidates managed publication files",
		}},
		{"focused_indexes", "repository", "existing_owner_lifecycle_unchanged", false, []string{
			"repo indexed analysis-unit/revision state", "$DATA/index focused publication files",
		}},
		{"resolver_catalogs", "installation/repository", "existing_owner_lifecycle_unchanged", false, []string{
			"resolver_catalog_publication", "$DATA/resolver-catalogs package-owned files",
		}},
		{"caller_rows", "repository/generation/pair", "selected_t306m_unbounded_retention", true, []string{
			"caller_generation_publication", "caller_generation_admission", "caller_leaf_outcome",
		}},
		{"caller_artifacts", "repository/generation/pair", "selected_t306m_unbounded_retention", true, []string{
			"$DATA/caller-leaves managed manifests and leaf artifacts",
		}},
	}

	seen := make(map[string]struct{}, api.RetentionStatusComponentCount)
	componentCount := 0
	for ownerIndex, owner := range status.Owners {
		if owner.ID != want[ownerIndex].owner {
			t.Fatalf("owner %d = %q, want %q", ownerIndex, owner.ID, want[ownerIndex].owner)
		}
		if owner.Scope != want[ownerIndex].scope ||
			owner.DecisionRelation != want[ownerIndex].decisionRelation ||
			owner.Accumulating != want[ownerIndex].accumulating {
			t.Fatalf("owner %q metadata = %q/%q/%v, want %q/%q/%v", owner.ID, owner.Scope, owner.DecisionRelation, owner.Accumulating, want[ownerIndex].scope, want[ownerIndex].decisionRelation, want[ownerIndex].accumulating)
		}
		if owner.ID == "proof_bundles" {
			wantControl := api.RetentionStatusControl{
				ConfigKey:              "proof_bundles.retention",
				DefaultState:           "disabled",
				PositiveLifetimeEffect: api.RetentionStatusProofBundlePositiveLifetimeEffect,
			}
			if owner.RetentionControl == nil || *owner.RetentionControl != wantControl {
				t.Fatalf("owner %q retention control = %+v, want %+v", owner.ID, owner.RetentionControl, wantControl)
			}
		} else if owner.RetentionControl != nil {
			t.Fatalf("owner %q retention control = %+v, want nil", owner.ID, owner.RetentionControl)
		}
		if len(owner.Components) != len(want[ownerIndex].components) {
			t.Fatalf("owner %q components = %d, want %d", owner.ID, len(owner.Components), len(want[ownerIndex].components))
		}
		for componentIndex, component := range owner.Components {
			if component.ID != want[ownerIndex].components[componentIndex] {
				t.Fatalf("owner %q component %d = %q, want %q", owner.ID, componentIndex, component.ID, want[ownerIndex].components[componentIndex])
			}
			if _, duplicate := seen[component.ID]; duplicate {
				t.Fatalf("duplicate component identifier %q", component.ID)
			}
			seen[component.ID] = struct{}{}
			componentCount++
			assertUnavailableRetentionMetric(t, component.ID+" count", component.Count, "identities")
			assertUnavailableRetentionByteMetrics(
				t,
				component.ID,
				component.ByteMetrics,
				retentionByteKinds(component.ID),
			)
			if component.ScannedIdentities != 0 || component.Truncated {
				t.Fatalf("component %q work = scanned %d truncated %v, want 0/false", component.ID, component.ScannedIdentities, component.Truncated)
			}
		}
	}
	if componentCount != api.RetentionStatusComponentCount {
		t.Fatalf("components = %d, want %d", componentCount, api.RetentionStatusComponentCount)
	}
	coreComponents, investigationComponents, derivedComponents := 0, 0, 0
	for _, owner := range status.Owners {
		switch owner.ID {
		case "evidence_publications", "extraction_attempts", "extraction_outcomes",
			"evidence_pins", "proof_bundles", "durable_job_history", "caller_rows":
			coreComponents += len(owner.Components)
		case "investigation_workbench_rows":
			investigationComponents += len(owner.Components)
		case "candidate_artifacts", "focused_indexes", "resolver_catalogs", "caller_artifacts":
			derivedComponents += len(owner.Components)
		}
	}
	if coreComponents != 21 || investigationComponents != 24 || derivedComponents != 7 {
		t.Fatalf("collector split = %d/%d/%d, want 21/24/7", coreComponents, investigationComponents, derivedComponents)
	}
	assertUnavailableRetentionMetric(t, "data volume total", status.DataVolume.TotalBytes, "bytes")
	assertUnavailableRetentionMetric(t, "data volume available", status.DataVolume.AvailableBytes, "bytes")
	if !bytes.Contains(encoded, []byte(`"value":null`)) {
		t.Fatalf("encoded response does not retain explicit null unavailable values: %s", encoded)
	}
	if got := bytes.Count(encoded, []byte(`"retention_control":null`)); got != api.RetentionStatusOwnerCount-1 {
		t.Fatalf("encoded null retention controls = %d, want %d: %s", got, api.RetentionStatusOwnerCount-1, encoded)
	}
	if !bytes.Contains(encoded, []byte(`"retention_control":{"config_key":"proof_bundles.retention","default_state":"disabled","positive_lifetime_effect":"`+api.RetentionStatusProofBundlePositiveLifetimeEffect+`"}`)) {
		t.Fatalf("encoded response omitted exact proof-bundle retention control: %s", encoded)
	}
}

func TestRetentionStatusProofBundlePostureFollowsConfiguredLifetime(t *testing.T) {
	tests := []struct {
		name         string
		lifetime     time.Duration
		accumulating bool
		state        string
	}{
		{name: "disabled", accumulating: true, state: "disabled"},
		{name: "enabled", lifetime: 24 * time.Hour, state: "enabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := getRetentionStatusWithOptions(t, api.Options{
				ProofBundleRetention: tt.lifetime,
			})
			owner := status.Owners[4]
			if owner.ID != "proof_bundles" ||
				owner.Accumulating != tt.accumulating ||
				owner.RetentionControl == nil ||
				owner.RetentionControl.DefaultState != tt.state ||
				owner.RetentionControl.PositiveLifetimeEffect != api.RetentionStatusProofBundlePositiveLifetimeEffect {
				t.Fatalf("proof-bundle posture = %+v, want accumulating=%t state=%q", owner, tt.accumulating, tt.state)
			}
		})
	}
}

func TestRetentionStatusAcceptsReportedProofBundlePosture(t *testing.T) {
	status, _ := getRetentionStatus(t, func(_ context.Context, status *api.RetentionStatus) error {
		owner := &status.Owners[4]
		owner.Accumulating = false
		owner.RetentionControl.DefaultState = "enabled"
		return nil
	})
	if status.Owners[4].Accumulating ||
		status.Owners[4].RetentionControl.DefaultState != "enabled" {
		t.Fatalf("reported proof-bundle posture = %+v", status.Owners[4])
	}
}

func TestRetentionStatusAllocationIsAggregateBoundedAndCannotStarve(t *testing.T) {
	status, _ := getRetentionStatus(t, nil)
	limits := status.Limits
	if limits.ReportedIdentitiesPerSummary != api.RetentionStatusReportedIdentityLimit ||
		limits.ScanIdentitiesPerSummary != api.RetentionStatusScanIdentityLimit ||
		limits.AggregateReportedIdentityAllocation != api.RetentionStatusAggregateReportedIdentityAllocation ||
		limits.AggregateScanIdentityAllocation != api.RetentionStatusAggregateScanIdentityAllocation {
		t.Fatalf("limits = %+v, want frozen constants", limits)
	}
	reportedTotal, scanTotal := 0, 0
	minimumReported, maximumReported := math.MaxInt, 0
	componentIndex := 0
	for _, owner := range status.Owners {
		for _, component := range owner.Components {
			allocation := component.Allocation
			if allocation.ReportedIdentities <= 0 {
				t.Fatalf("component %q has no reserved report work", component.ID)
			}
			if allocation.ScanIdentities != allocation.ReportedIdentities+1 {
				t.Fatalf("component %q allocation = %+v, want one private sentinel", component.ID, allocation)
			}
			wantReported := 78
			if componentIndex < 40 {
				wantReported = 79
			}
			if allocation.ReportedIdentities != wantReported {
				t.Fatalf("component %d %q report allocation = %d, want %d", componentIndex, component.ID, allocation.ReportedIdentities, wantReported)
			}
			reportedTotal += allocation.ReportedIdentities
			scanTotal += allocation.ScanIdentities
			minimumReported = min(minimumReported, allocation.ReportedIdentities)
			maximumReported = max(maximumReported, allocation.ReportedIdentities)
			componentIndex++
		}
	}
	if reportedTotal != limits.AggregateReportedIdentityAllocation ||
		scanTotal != limits.AggregateScanIdentityAllocation {
		t.Fatalf("allocation totals = %d/%d, want %d/%d", reportedTotal, scanTotal, limits.AggregateReportedIdentityAllocation, limits.AggregateScanIdentityAllocation)
	}
	if maximumReported-minimumReported > 1 {
		t.Fatalf("allocation range = %d..%d, want fair split", minimumReported, maximumReported)
	}
}

func TestRetentionStatusEncodedResponseIsFixedAndBounded(t *testing.T) {
	first, firstEncoded := getRetentionStatus(t, nil)
	_, secondEncoded := getRetentionStatus(t, nil)
	if !bytes.Equal(firstEncoded, secondEncoded) {
		t.Fatal("empty retention status encoding is not deterministic")
	}
	const wantEmptyEncodedBytes = 19_955
	if len(firstEncoded) != wantEmptyEncodedBytes {
		t.Fatalf("empty encoded bytes = %d, want frozen %d", len(firstEncoded), wantEmptyEncodedBytes)
	}
	if len(firstEncoded) > api.RetentionStatusResponseByteLimit {
		t.Fatalf("empty encoded bytes = %d, limit %d", len(firstEncoded), api.RetentionStatusResponseByteLimit)
	}

	maximum := int64(math.MaxInt64)
	for ownerIndex := range first.Owners {
		for componentIndex := range first.Owners[ownerIndex].Components {
			component := &first.Owners[ownerIndex].Components[componentIndex]
			component.ScannedIdentities = component.Allocation.ScanIdentities
			reported := int64(component.Allocation.ReportedIdentities)
			component.Count = api.RetentionStatusMetric{Value: &reported, Unit: "identities", Completeness: api.RetentionStatusLowerBound}
			for metricIndex := range component.ByteMetrics {
				component.ByteMetrics[metricIndex].Value = &maximum
				component.ByteMetrics[metricIndex].Unit = "bytes"
				component.ByteMetrics[metricIndex].Completeness = api.RetentionStatusLowerBound
			}
			component.Truncated = true
		}
	}
	first.DataVolume.TotalBytes = api.RetentionStatusMetric{Value: &maximum, Unit: "bytes", Completeness: api.RetentionStatusLowerBound}
	first.DataVolume.AvailableBytes = api.RetentionStatusMetric{Value: &maximum, Unit: "bytes", Completeness: api.RetentionStatusLowerBound}
	_, maximumEncoded := getRetentionStatus(t, func(_ context.Context, status *api.RetentionStatus) error {
		*status = first
		return nil
	})
	const wantMaximumEncodedBytes = 20_766
	if len(maximumEncoded) != wantMaximumEncodedBytes {
		t.Fatalf("maximum encoded bytes = %d, want frozen %d", len(maximumEncoded), wantMaximumEncodedBytes)
	}
	if len(maximumEncoded) > api.RetentionStatusResponseByteLimit {
		t.Fatalf("maximum encoded bytes = %d, limit %d", len(maximumEncoded), api.RetentionStatusResponseByteLimit)
	}
}

func TestRetentionStatusSchemaLinkResolves(t *testing.T) {
	handler := api.New(api.Options{
		Version: "test",
		IsAdmin: func(context.Context) bool { return true },
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d (%s), want 200", api.RetentionStatusSchemaLink, recorder.Code, recorder.Body)
	}
	var document struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document.Components.Schemas["RetentionStatus"]; !exists {
		t.Fatalf("schema link target %q is absent", api.RetentionStatusSchemaLink)
	}
}

func TestRetentionStatusSchemaLinkAndBodyBoundIgnoreRequestHost(t *testing.T) {
	handler := api.New(api.Options{
		Version: "test",
		IsAdmin: func(context.Context) bool { return true },
	})
	request := httptest.NewRequest(http.MethodGet, api.RetentionStatusPath, nil)
	request.Host = strings.Repeat("host", api.RetentionStatusResponseByteLimit)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", recorder.Code, recorder.Body)
	}
	assertRetentionWarningHeader(t, recorder)
	const wantEmptyEncodedBytes = 19_955
	if recorder.Body.Len() != wantEmptyEncodedBytes {
		t.Fatalf("host-varied body bytes = %d, want frozen %d", recorder.Body.Len(), wantEmptyEncodedBytes)
	}
	var status api.RetentionStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.SchemaLink != api.RetentionStatusSchemaLink {
		t.Fatalf("host-varied schema link = %q, want %q", status.SchemaLink, api.RetentionStatusSchemaLink)
	}
}

func TestRetentionStatusRejectsContractDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*api.RetentionStatus)
	}{
		{
			name: "component identifier",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[0].Components[0].ID = "forged_component"
			},
		},
		{
			name: "component allocation",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[len(status.Owners)-1].Components[0].Allocation.ScanIdentities++
			},
		},
		{
			name: "component byte kind",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[0].Components[0].ByteMetrics[0].Kind = api.RetentionStatusByteApparentFile
			},
		},
		{
			name: "owner retention control",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[0].RetentionControl = &api.RetentionStatusControl{
					ConfigKey:              "forged.retention",
					DefaultState:           "enabled",
					PositiveLifetimeEffect: "delete_everything",
				}
			},
		},
		{
			name: "proof retention control does not alias registry",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[4].RetentionControl.ConfigKey = "forged.retention"
			},
		},
		{
			name: "proof retention posture is inconsistent",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[4].Accumulating = false
			},
		},
		{
			name: "proof retention state is unsupported",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[4].RetentionControl.DefaultState = "unknown"
			},
		},
		{
			name: "count beyond allocation",
			mutate: func(status *api.RetentionStatus) {
				component := &status.Owners[0].Components[0]
				value := int64(component.Allocation.ReportedIdentities + 1)
				component.Count = api.RetentionStatusMetric{
					Value: &value, Unit: "identities", Completeness: api.RetentionStatusExact,
				}
			},
		},
		{
			name: "exact count disagrees with scan",
			mutate: func(status *api.RetentionStatus) {
				component := &status.Owners[0].Components[0]
				component.ScannedIdentities = 1
				value := int64(0)
				component.Count = api.RetentionStatusMetric{
					Value: &value, Unit: "identities", Completeness: api.RetentionStatusExact,
				}
			},
		},
		{
			name: "sentinel scan without truncation",
			mutate: func(status *api.RetentionStatus) {
				component := &status.Owners[0].Components[0]
				component.ScannedIdentities = component.Allocation.ScanIdentities
				value := int64(component.Allocation.ReportedIdentities)
				component.Count = api.RetentionStatusMetric{
					Value: &value, Unit: "identities", Completeness: api.RetentionStatusLowerBound,
				}
			},
		},
		{
			name: "empty lower bound",
			mutate: func(status *api.RetentionStatus) {
				component := &status.Owners[0].Components[0]
				value := int64(0)
				component.Count = api.RetentionStatusMetric{
					Value: &value, Unit: "identities", Completeness: api.RetentionStatusLowerBound,
				}
			},
		},
		{
			name: "truncation without private sentinel",
			mutate: func(status *api.RetentionStatus) {
				status.Owners[0].Components[0].Truncated = true
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := api.New(api.Options{
				Version: "test",
				IsAdmin: func(context.Context) bool { return true },
				RetentionStatusSource: func(_ context.Context, status *api.RetentionStatus) error {
					tt.mutate(status)
					return nil
				},
			})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/retention-status", nil),
			)
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d (%s), want 500", recorder.Code, recorder.Body)
			}
			assertRetentionWarningHeader(t, recorder)
		})
	}
}

func TestRetentionStatusAcceptsHonestPartialCollectionShapes(t *testing.T) {
	status, _ := getRetentionStatus(t, func(_ context.Context, status *api.RetentionStatus) error {
		lowerBound := &status.Owners[0].Components[0]
		lowerBound.ScannedIdentities = 1
		value := int64(1)
		lowerBound.Count = api.RetentionStatusMetric{
			Value: &value, Unit: "identities", Completeness: api.RetentionStatusLowerBound,
		}
		unavailable := &status.Owners[0].Components[1]
		unavailable.ScannedIdentities = 1
		return nil
	})
	if status.Owners[0].Components[0].Count.Completeness != api.RetentionStatusLowerBound ||
		status.Owners[0].Components[0].Truncated ||
		status.Owners[0].Components[1].Count.Completeness != api.RetentionStatusUnavailable {
		t.Fatalf("partial collection shapes = %+v", status.Owners[0].Components[:2])
	}
}

func TestRetentionStatusSourceFailureRepeatsWarning(t *testing.T) {
	handler := api.New(api.Options{
		Version: "test",
		IsAdmin: func(context.Context) bool { return true },
		RetentionStatusSource: func(context.Context, *api.RetentionStatus) error {
			return context.Canceled
		},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/retention-status", nil),
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500", recorder.Code, recorder.Body)
	}
	assertRetentionWarningHeader(t, recorder)
}

func TestRetentionStatusWarningWrapsOuterMiddlewareFailures(t *testing.T) {
	handler := api.WithRetentionStatusWarning(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(w, "session load failed", http.StatusInternalServerError)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, api.RetentionStatusPath, nil),
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500", recorder.Code, recorder.Body)
	}
	assertRetentionWarningHeader(t, recorder)
}

func TestRetentionStatusInvestigationRegistryMatchesStoreSchema(t *testing.T) {
	status, _ := getRetentionStatus(t, nil)
	var registered []string
	for _, owner := range status.Owners {
		if owner.ID == "investigation_workbench_rows" {
			for _, component := range owner.Components {
				registered = append(registered, component.ID)
			}
		}
	}
	schema, err := os.ReadFile("../store/schema.surql")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(
		`(?m)^DEFINE TABLE IF NOT EXISTS (investigation(?:_[a-z0-9]+)*) SCHEMALESS;$`,
	)
	var schemaComponents []string
	jobFound := false
	for _, match := range pattern.FindAllSubmatch(schema, -1) {
		component := string(match[1])
		if component == string(store.JobInvestigate) {
			jobFound = true
			continue
		}
		schemaComponents = append(schemaComponents, component)
	}
	if !jobFound {
		t.Fatal("store schema omitted investigation_run_job")
	}
	if !slices.Equal(registered, schemaComponents) {
		t.Fatalf("registered Investigation components = %v, schema = %v", registered, schemaComponents)
	}
}

func getRetentionStatus(
	t *testing.T,
	source api.RetentionStatusSource,
) (api.RetentionStatus, []byte) {
	t.Helper()
	return getRetentionStatusWithOptions(t, api.Options{RetentionStatusSource: source})
}

func getRetentionStatusWithOptions(
	t *testing.T,
	opts api.Options,
) (api.RetentionStatus, []byte) {
	t.Helper()
	opts.Version = "test"
	opts.IsAdmin = func(context.Context) bool { return true }
	handler := api.New(opts)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/retention-status", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", recorder.Code, recorder.Body)
	}
	assertRetentionWarningHeader(t, recorder)
	var status api.RetentionStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode retention status: %v", err)
	}
	return status, recorder.Body.Bytes()
}

func assertRetentionWarningHeader(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get(api.RetentionStatusWarningHeader); got != api.RetentionStatusWarningCode {
		t.Fatalf("warning header = %q, want %q", got, api.RetentionStatusWarningCode)
	}
}

func assertUnavailableRetentionByteMetrics(
	t *testing.T,
	componentID string,
	metrics []api.RetentionStatusByteMetric,
	wantKinds []api.RetentionStatusByteKind,
) {
	t.Helper()
	if len(metrics) != len(wantKinds) {
		t.Fatalf("component %q byte metrics = %d, want %d", componentID, len(metrics), len(wantKinds))
	}
	for metricIndex, metric := range metrics {
		if metric.Kind != wantKinds[metricIndex] ||
			metric.Value != nil ||
			metric.Unit != "bytes" ||
			metric.Completeness != api.RetentionStatusUnavailable {
			t.Fatalf(
				"component %q byte metric %d = %+v, want kind %q/null bytes unavailable",
				componentID,
				metricIndex,
				metric,
				wantKinds[metricIndex],
			)
		}
	}
}

func retentionByteKinds(componentID string) []api.RetentionStatusByteKind {
	switch componentID {
	case "extraction_domain_outcome":
		return []api.RetentionStatusByteKind{
			api.RetentionStatusByteLogicalEncoded,
			api.RetentionStatusBytePhysicalDatabase,
		}
	case "proof_bundle", "resolver_catalog_publication":
		return []api.RetentionStatusByteKind{
			api.RetentionStatusByteCanonicalContent,
			api.RetentionStatusBytePhysicalDatabase,
		}
	case "caller_generation_publication", "caller_generation_admission", "caller_leaf_outcome":
		return []api.RetentionStatusByteKind{
			api.RetentionStatusByteCanonicalReceipt,
			api.RetentionStatusBytePhysicalDatabase,
		}
	case "$DATA/candidates managed publication files",
		"$DATA/index focused publication files",
		"$DATA/resolver-catalogs package-owned files":
		return []api.RetentionStatusByteKind{api.RetentionStatusByteApparentFile}
	case "$DATA/caller-leaves managed manifests and leaf artifacts":
		return []api.RetentionStatusByteKind{
			api.RetentionStatusByteCanonicalReceipt,
			api.RetentionStatusByteApparentFile,
		}
	default:
		return []api.RetentionStatusByteKind{api.RetentionStatusBytePhysicalDatabase}
	}
}

func assertUnavailableRetentionMetric(
	t *testing.T,
	name string,
	metric api.RetentionStatusMetric,
	unit string,
) {
	t.Helper()
	if metric.Value != nil || metric.Unit != unit || metric.Completeness != api.RetentionStatusUnavailable {
		t.Fatalf("%s = %+v, want null %s unavailable", name, metric, unit)
	}
}

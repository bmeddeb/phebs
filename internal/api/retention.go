package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	RetentionStatusPath                              = "/api/retention-status"
	RetentionStatusSchema                            = "phebs-retention-status-v1"
	RetentionStatusSchemaLink                        = "/api/openapi.json#/components/schemas/RetentionStatus"
	RetentionStatusWarningCode                       = "unbounded_historical_publication_retention"
	RetentionStatusWarningHeader                     = "X-Phebs-Warning-Code"
	RetentionStatusProofBundlePositiveLifetimeEffect = "deletes the expired bundle and exactly its proof-bundle:<bundle_id> evidence pins but no extraction evidence; the independent evidence sweep may later reclaim newly unpinned superseded evidence when otherwise eligible"

	RetentionStatusOwnerCount         = 12
	RetentionStatusComponentCount     = 52
	RetentionStatusCoreComponentCount = 21

	// RetentionStatusReportedIdentityLimit is the most identities one summary
	// may represent exactly. The scan limit owns one cap-plus-one sentinel so a
	// collector can distinguish exact-at-cap from a labeled lower bound.
	RetentionStatusReportedIdentityLimit = 4_096
	RetentionStatusScanIdentityLimit     = RetentionStatusReportedIdentityLimit + 1
	RetentionStatusResponseByteLimit     = 64 << 10

	// The endpoint shares one 4,096-identity reporting allocation fairly across
	// all components. Every component also owns one sentinel slot, rather than
	// multiplying the per-summary 4,097 ceiling into an unmeasured aggregate.
	RetentionStatusAggregateReportedIdentityAllocation = RetentionStatusReportedIdentityLimit
	RetentionStatusAggregateScanIdentityAllocation     = RetentionStatusAggregateReportedIdentityAllocation + RetentionStatusComponentCount
)

type RetentionStatusCompleteness string
type RetentionStatusByteKind string

const (
	RetentionStatusExact       RetentionStatusCompleteness = "exact"
	RetentionStatusLowerBound  RetentionStatusCompleteness = "lower_bound"
	RetentionStatusUnavailable RetentionStatusCompleteness = "unavailable"
)

const (
	RetentionStatusByteLogicalEncoded   RetentionStatusByteKind = "logical_encoded"
	RetentionStatusByteCanonicalContent RetentionStatusByteKind = "canonical_content"
	RetentionStatusByteCanonicalReceipt RetentionStatusByteKind = "canonical_receipt"
	RetentionStatusByteApparentFile     RetentionStatusByteKind = "apparent_file"
	RetentionStatusBytePhysicalDatabase RetentionStatusByteKind = "physical_database"
)

type RetentionStatusMetric struct {
	Value        *int64                      `json:"value"`
	Unit         string                      `json:"unit" enum:"identities,bytes"`
	Completeness RetentionStatusCompleteness `json:"completeness" enum:"exact,lower_bound,unavailable"`
}

type RetentionStatusByteMetric struct {
	Kind         RetentionStatusByteKind     `json:"kind" enum:"logical_encoded,canonical_content,canonical_receipt,apparent_file,physical_database"`
	Value        *int64                      `json:"value"`
	Unit         string                      `json:"unit" enum:"bytes"`
	Completeness RetentionStatusCompleteness `json:"completeness" enum:"exact,lower_bound,unavailable"`
}

type RetentionStatusAllocation struct {
	ReportedIdentities int `json:"reported_identities"`
	ScanIdentities     int `json:"scan_identities"`
}

type RetentionComponentStatus struct {
	ID                string                      `json:"id"`
	Allocation        RetentionStatusAllocation   `json:"allocation"`
	ScannedIdentities int                         `json:"scanned_identities"`
	Count             RetentionStatusMetric       `json:"count"`
	ByteMetrics       []RetentionStatusByteMetric `json:"byte_metrics"`
	Truncated         bool                        `json:"truncated"`
}

type RetentionStatusControl struct {
	ConfigKey              string `json:"config_key"`
	DefaultState           string `json:"default_state" enum:"disabled,enabled"`
	PositiveLifetimeEffect string `json:"positive_lifetime_effect"`
}

type RetentionOwnerStatus struct {
	ID               string                     `json:"id"`
	Scope            string                     `json:"scope"`
	DecisionRelation string                     `json:"decision_relation"`
	Accumulating     bool                       `json:"accumulating"`
	RetentionControl *RetentionStatusControl    `json:"retention_control"`
	Components       []RetentionComponentStatus `json:"components"`
}

type RetentionStatusLimits struct {
	Owners                              int `json:"owners"`
	Components                          int `json:"components"`
	ReportedIdentitiesPerSummary        int `json:"reported_identities_per_summary"`
	ScanIdentitiesPerSummary            int `json:"scan_identities_per_summary"`
	AggregateReportedIdentityAllocation int `json:"aggregate_reported_identity_allocation"`
	AggregateScanIdentityAllocation     int `json:"aggregate_scan_identity_allocation"`
	EncodedResponseByteLimit            int `json:"encoded_response_byte_limit"`
}

type RetentionDataVolumeStatus struct {
	TotalBytes     RetentionStatusMetric `json:"total_bytes"`
	AvailableBytes RetentionStatusMetric `json:"available_bytes"`
}

type RetentionStatus struct {
	SchemaLink    string                    `json:"$schema"`
	SchemaVersion string                    `json:"schema" enum:"phebs-retention-status-v1"`
	WarningCode   string                    `json:"warning_code" enum:"unbounded_historical_publication_retention"`
	Limits        RetentionStatusLimits     `json:"limits"`
	DataVolume    RetentionDataVolumeStatus `json:"data_volume"`
	Owners        []RetentionOwnerStatus    `json:"owners"`
}

// RetentionStatusSource is invoked only after administrator authorization and
// receives a fresh fixed-registry shell. Implementations replace unavailable
// metrics in place; they must preserve the registry, allocations, and response
// bound.
type RetentionStatusSource func(context.Context, *RetentionStatus) error

// RetentionStatusErrorReporter observes one localized component failure. The
// fixed v1 wire keeps the component unavailable; this hook preserves the
// operational cause without failing successful sibling summaries. A reporter
// supplied to the HTTP service must be safe for concurrent requests.
type RetentionStatusErrorReporter func(
	context.Context,
	store.RetentionComponent,
	error,
)

var coreRetentionOwnerIDs = map[string]struct{}{
	"evidence_publications": {},
	"extraction_attempts":   {},
	"extraction_outcomes":   {},
	"evidence_pins":         {},
	"proof_bundles":         {},
	"durable_job_history":   {},
	"caller_rows":           {},
}

// NewCoreRetentionStatusSource adapts the bounded store-plane collector into
// the fixed T30.6o response shell. Ordinary component failures stay localized
// as unavailable metrics; malformed collector output and request cancellation
// remain endpoint errors.
func NewCoreRetentionStatusSource(
	source store.CoreRetentionStore,
	reportError RetentionStatusErrorReporter,
) RetentionStatusSource {
	if reportError == nil {
		reportError = func(
			_ context.Context,
			component store.RetentionComponent,
			err error,
		) {
			class := "query_error"
			if errors.Is(err, store.ErrRetentionComponentUnavailable) {
				class = "not_ready"
			}
			log.Printf(
				"retention status: component=%q class=%s error=%v",
				component,
				class,
				err,
			)
		}
	}
	return func(ctx context.Context, status *RetentionStatus) error {
		if source == nil {
			return nil
		}
		requests := make([]store.RetentionComponentRequest, 0, RetentionStatusCoreComponentCount)
		components := make(map[store.RetentionComponent]*RetentionComponentStatus, RetentionStatusCoreComponentCount)
		for ownerIndex := range status.Owners {
			owner := &status.Owners[ownerIndex]
			if _, core := coreRetentionOwnerIDs[owner.ID]; !core {
				continue
			}
			for componentIndex := range owner.Components {
				component := &owner.Components[componentIndex]
				id := store.RetentionComponent(component.ID)
				if _, duplicate := components[id]; duplicate {
					return fmt.Errorf("duplicate core retention component %q", id)
				}
				components[id] = component
				requests = append(requests, store.RetentionComponentRequest{
					Component:          id,
					ReportedIdentities: component.Allocation.ReportedIdentities,
					ScanIdentities:     component.Allocation.ScanIdentities,
				})
			}
		}
		if len(requests) != RetentionStatusCoreComponentCount {
			return fmt.Errorf("core retention component count %d is not %d", len(requests), RetentionStatusCoreComponentCount)
		}

		results, err := source.CollectCoreRetention(ctx, requests)
		if err != nil {
			return err
		}
		seen := make(map[store.RetentionComponent]struct{}, len(results))
		for _, result := range results {
			component, exists := components[result.Component]
			if !exists {
				return fmt.Errorf("collector returned unknown core retention component %q", result.Component)
			}
			if _, duplicate := seen[result.Component]; duplicate {
				return fmt.Errorf("collector returned duplicate core retention component %q", result.Component)
			}
			seen[result.Component] = struct{}{}
			if result.Err != nil {
				reportError(ctx, result.Component, result.Err)
				continue
			}
			if err := applyCoreRetentionSummary(component, result.Summary); err != nil {
				return fmt.Errorf("core retention component %q: %w", result.Component, err)
			}
		}
		if len(seen) != len(requests) {
			return fmt.Errorf("collector returned %d of %d core retention components", len(seen), len(requests))
		}
		return nil
	}
}

func applyCoreRetentionSummary(
	component *RetentionComponentStatus,
	summary store.RetentionComponentSummary,
) error {
	if component == nil {
		return errors.New("component is nil")
	}
	if summary.ScannedIdentities < 0 ||
		summary.ScannedIdentities > component.Allocation.ScanIdentities {
		return errors.New("scan count exceeds component allocation")
	}
	wantReported := summary.ScannedIdentities
	wantTruncated := wantReported == component.Allocation.ScanIdentities
	if wantTruncated {
		wantReported = component.Allocation.ReportedIdentities
	}
	if summary.ReportedIdentities != int64(wantReported) ||
		summary.Truncated != wantTruncated {
		return errors.New("summary count does not match cap-plus-one allocation")
	}
	completeness := RetentionStatusExact
	if summary.Truncated {
		completeness = RetentionStatusLowerBound
	}
	count := summary.ReportedIdentities
	component.ScannedIdentities = summary.ScannedIdentities
	component.Count = RetentionStatusMetric{
		Value: &count, Unit: "identities", Completeness: completeness,
	}
	component.Truncated = summary.Truncated
	if summary.Bytes == nil {
		return nil
	}
	if *summary.Bytes < 0 {
		return errors.New("summary bytes are negative")
	}
	set := false
	for metricIndex := range component.ByteMetrics {
		metric := &component.ByteMetrics[metricIndex]
		if metric.Kind == RetentionStatusBytePhysicalDatabase {
			continue
		}
		if set {
			return errors.New("summary has multiple logical byte destinations")
		}
		value := *summary.Bytes
		metric.Value = &value
		metric.Unit = "bytes"
		metric.Completeness = completeness
		set = true
	}
	if !set {
		return errors.New("summary supplied bytes for a physical-only component")
	}
	return nil
}

// WithRetentionStatusWarning repeats the static warning on every response for
// the status path, including failures emitted by middleware that wraps or
// short-circuits the endpoint itself.
func WithRetentionStatusWarning(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == RetentionStatusPath {
			w.Header().Set(RetentionStatusWarningHeader, RetentionStatusWarningCode)
		}
		next.ServeHTTP(w, r)
	})
}

type retentionOwnerDefinition struct {
	id               string
	scope            string
	decisionRelation string
	accumulating     bool
	proofRetention   bool
	retentionControl *RetentionStatusControl
	components       []retentionComponentDefinition
}

type retentionComponentDefinition struct {
	id        string
	byteKinds []RetentionStatusByteKind
}

var proofBundleRetentionControl = RetentionStatusControl{
	ConfigKey:              "proof_bundles.retention",
	PositiveLifetimeEffect: RetentionStatusProofBundlePositiveLifetimeEffect,
}

func databaseRetentionComponent(id string) retentionComponentDefinition {
	return retentionComponent(id, RetentionStatusBytePhysicalDatabase)
}

func retentionComponent(
	id string,
	byteKinds ...RetentionStatusByteKind,
) retentionComponentDefinition {
	return retentionComponentDefinition{id: id, byteKinds: byteKinds}
}

var retentionStatusRegistry = [...]retentionOwnerDefinition{
	{id: "evidence_publications", scope: "repository/commit/unit/domain", decisionRelation: "selected_t306m_unbounded_retention", accumulating: true, components: []retentionComponentDefinition{
		databaseRetentionComponent("extraction_run"),
		databaseRetentionComponent("snapshot_evidence"),
		databaseRetentionComponent("assertion"),
		databaseRetentionComponent("evidence_atom"),
	}},
	{id: "extraction_attempts", scope: "repository/commit/unit/domain", decisionRelation: "selected_t306m_unbounded_retention", accumulating: true, components: []retentionComponentDefinition{
		databaseRetentionComponent("extraction_attempt"),
	}},
	{id: "extraction_outcomes", scope: "repository/domain", decisionRelation: "existing_owner_lifecycle_unchanged", components: []retentionComponentDefinition{
		retentionComponent(
			"extraction_domain_outcome",
			RetentionStatusByteLogicalEncoded,
			RetentionStatusBytePhysicalDatabase,
		),
	}},
	{id: "evidence_pins", scope: "run/kind", decisionRelation: "mixed_owner_lifecycles_not_selected_by_t306m", accumulating: true, components: []retentionComponentDefinition{
		databaseRetentionComponent("evidence_pin[kind=proof-bundle:<bundle_id>]"),
		databaseRetentionComponent("evidence_pin[kind=investigation-artifact:<artifact_id>]"),
		databaseRetentionComponent("evidence_pin[kind=<other exact store-accepted value>]"),
	}},
	{id: "proof_bundles", scope: "immutable bundle", decisionRelation: "configured_owner_lifecycle_unchanged", proofRetention: true, retentionControl: &proofBundleRetentionControl, components: []retentionComponentDefinition{
		retentionComponent(
			"proof_bundle",
			RetentionStatusByteCanonicalContent,
			RetentionStatusBytePhysicalDatabase,
		),
	}},
	{id: "durable_job_history", scope: "queue-table/target/auto-id", decisionRelation: "incidental_growth_requires_separate_decision", accumulating: true, components: []retentionComponentDefinition{
		databaseRetentionComponent(string(store.JobSync)),
		databaseRetentionComponent(string(store.JobIndex)),
		databaseRetentionComponent(string(store.JobFetch)),
		databaseRetentionComponent(string(store.JobCandidate)),
		databaseRetentionComponent(string(store.JobExtract)),
		databaseRetentionComponent(string(store.JobResolverCatalog)),
		databaseRetentionComponent(string(store.JobCallerLeaf)),
		databaseRetentionComponent(string(store.JobInvestigate)),
	}},
	{id: "investigation_workbench_rows", scope: "investigation/revision/run/artifact/review/watch", decisionRelation: "incidental_growth_requires_separate_decision", accumulating: true, components: []retentionComponentDefinition{
		databaseRetentionComponent("investigation"),
		databaseRetentionComponent("investigation_revision"),
		databaseRetentionComponent("investigation_change_brief"),
		databaseRetentionComponent("investigation_workbench_mutation"),
		databaseRetentionComponent("investigation_workbench_disposition"),
		databaseRetentionComponent("investigation_run"),
		databaseRetentionComponent("investigation_run_event"),
		databaseRetentionComponent("investigation_run_artifact"),
		databaseRetentionComponent("investigation_artifact_owner"),
		databaseRetentionComponent("investigation_artifact_owner_release"),
		databaseRetentionComponent("investigation_artifact_retention_override"),
		databaseRetentionComponent("investigation_decision"),
		databaseRetentionComponent("investigation_disposition"),
		databaseRetentionComponent("investigation_baseline_designation"),
		databaseRetentionComponent("investigation_grant"),
		databaseRetentionComponent("investigation_cursor"),
		databaseRetentionComponent("investigation_creation"),
		databaseRetentionComponent("investigation_consumer_snapshot"),
		databaseRetentionComponent("investigation_consumer_edge_ledger"),
		databaseRetentionComponent("investigation_review_projection"),
		databaseRetentionComponent("investigation_review_item"),
		databaseRetentionComponent("investigation_dossier"),
		databaseRetentionComponent("investigation_watch"),
		databaseRetentionComponent("investigation_watch_revision"),
	}},
	{id: "candidate_artifacts", scope: "repository/generation", decisionRelation: "selected_t306m_unbounded_retention", accumulating: true, components: []retentionComponentDefinition{
		databaseRetentionComponent("candidate_manifest_publication"),
		retentionComponent("$DATA/candidates managed publication files", RetentionStatusByteApparentFile),
	}},
	{id: "focused_indexes", scope: "repository", decisionRelation: "existing_owner_lifecycle_unchanged", components: []retentionComponentDefinition{
		databaseRetentionComponent("repo indexed analysis-unit/revision state"),
		retentionComponent("$DATA/index focused publication files", RetentionStatusByteApparentFile),
	}},
	{id: "resolver_catalogs", scope: "installation/repository", decisionRelation: "existing_owner_lifecycle_unchanged", components: []retentionComponentDefinition{
		retentionComponent(
			"resolver_catalog_publication",
			RetentionStatusByteCanonicalContent,
			RetentionStatusBytePhysicalDatabase,
		),
		retentionComponent("$DATA/resolver-catalogs package-owned files", RetentionStatusByteApparentFile),
	}},
	{id: "caller_rows", scope: "repository/generation/pair", decisionRelation: "selected_t306m_unbounded_retention", accumulating: true, components: []retentionComponentDefinition{
		retentionComponent(
			"caller_generation_publication",
			RetentionStatusByteCanonicalReceipt,
			RetentionStatusBytePhysicalDatabase,
		),
		retentionComponent(
			"caller_generation_admission",
			RetentionStatusByteCanonicalReceipt,
			RetentionStatusBytePhysicalDatabase,
		),
		retentionComponent(
			"caller_leaf_outcome",
			RetentionStatusByteCanonicalReceipt,
			RetentionStatusBytePhysicalDatabase,
		),
	}},
	{id: "caller_artifacts", scope: "repository/generation/pair", decisionRelation: "selected_t306m_unbounded_retention", accumulating: true, components: []retentionComponentDefinition{
		retentionComponent(
			"$DATA/caller-leaves managed manifests and leaf artifacts",
			RetentionStatusByteCanonicalReceipt,
			RetentionStatusByteApparentFile,
		),
	}},
}

func registerRetentionStatus(api huma.API, opts Options) {
	type retentionStatusOut struct {
		WarningCode string `header:"X-Phebs-Warning-Code"`
		Body        RetentionStatus
	}
	huma.Get(api, RetentionStatusPath, func(
		ctx context.Context,
		_ *struct{},
	) (*retentionStatusOut, error) {
		// This check deliberately fails closed when IsAdmin is absent. Nothing
		// below it may touch a collector, store, filesystem, or inventory cache.
		if opts.IsAdmin == nil || !opts.IsAdmin(ctx) {
			return nil, huma.Error403Forbidden("administrator access required")
		}
		status := newRetentionStatusShell(opts.ProofBundleRetention)
		if opts.RetentionStatusSource != nil {
			if err := opts.RetentionStatusSource(ctx, &status); err != nil {
				return nil, huma.Error500InternalServerError("retention status", err)
			}
		}
		if err := validateRetentionStatus(status); err != nil {
			return nil, huma.Error500InternalServerError("invalid retention status", err)
		}
		encodedBytes, err := encodedRetentionStatusBytes(status)
		if err != nil {
			return nil, huma.Error500InternalServerError("encode retention status", err)
		}
		if encodedBytes > RetentionStatusResponseByteLimit {
			return nil, huma.Error500InternalServerError(
				"retention status exceeds encoded response bound",
				fmt.Errorf("encoded bytes %d exceed %d", encodedBytes, RetentionStatusResponseByteLimit),
			)
		}
		return &retentionStatusOut{
			WarningCode: RetentionStatusWarningCode,
			Body:        status,
		}, nil
	})
}

func newRetentionStatusShell(proofBundleRetention time.Duration) RetentionStatus {
	status := RetentionStatus{
		SchemaLink:    RetentionStatusSchemaLink,
		SchemaVersion: RetentionStatusSchema,
		WarningCode:   RetentionStatusWarningCode,
		Limits: RetentionStatusLimits{
			Owners:                              RetentionStatusOwnerCount,
			Components:                          RetentionStatusComponentCount,
			ReportedIdentitiesPerSummary:        RetentionStatusReportedIdentityLimit,
			ScanIdentitiesPerSummary:            RetentionStatusScanIdentityLimit,
			AggregateReportedIdentityAllocation: RetentionStatusAggregateReportedIdentityAllocation,
			AggregateScanIdentityAllocation:     RetentionStatusAggregateScanIdentityAllocation,
			EncodedResponseByteLimit:            RetentionStatusResponseByteLimit,
		},
		DataVolume: RetentionDataVolumeStatus{
			TotalBytes:     unavailableRetentionMetric("bytes"),
			AvailableBytes: unavailableRetentionMetric("bytes"),
		},
		Owners: make([]RetentionOwnerStatus, 0, len(retentionStatusRegistry)),
	}
	componentIndex := 0
	for _, definition := range retentionStatusRegistry {
		accumulating := definition.accumulating
		var retentionControl *RetentionStatusControl
		if definition.retentionControl != nil {
			copied := *definition.retentionControl
			if definition.proofRetention {
				accumulating = proofBundleRetention <= 0
				if accumulating {
					copied.DefaultState = "disabled"
				} else {
					copied.DefaultState = "enabled"
				}
			}
			retentionControl = &copied
		}
		owner := RetentionOwnerStatus{
			ID:               definition.id,
			Scope:            definition.scope,
			DecisionRelation: definition.decisionRelation,
			Accumulating:     accumulating,
			RetentionControl: retentionControl,
			Components:       make([]RetentionComponentStatus, 0, len(definition.components)),
		}
		for _, componentDefinition := range definition.components {
			allocation := retentionStatusAllocation(componentIndex)
			byteMetrics := make([]RetentionStatusByteMetric, 0, len(componentDefinition.byteKinds))
			for _, kind := range componentDefinition.byteKinds {
				byteMetrics = append(byteMetrics, unavailableRetentionByteMetric(kind))
			}
			owner.Components = append(owner.Components, RetentionComponentStatus{
				ID:          componentDefinition.id,
				Allocation:  allocation,
				Count:       unavailableRetentionMetric("identities"),
				ByteMetrics: byteMetrics,
			})
			componentIndex++
		}
		status.Owners = append(status.Owners, owner)
	}
	return status
}

func retentionStatusAllocation(componentIndex int) RetentionStatusAllocation {
	reported := RetentionStatusAggregateReportedIdentityAllocation / RetentionStatusComponentCount
	if componentIndex < RetentionStatusAggregateReportedIdentityAllocation%RetentionStatusComponentCount {
		reported++
	}
	return RetentionStatusAllocation{
		ReportedIdentities: reported,
		ScanIdentities:     reported + 1,
	}
}

func unavailableRetentionMetric(unit string) RetentionStatusMetric {
	return RetentionStatusMetric{
		Unit:         unit,
		Completeness: RetentionStatusUnavailable,
	}
}

func unavailableRetentionByteMetric(kind RetentionStatusByteKind) RetentionStatusByteMetric {
	return RetentionStatusByteMetric{
		Kind:         kind,
		Unit:         "bytes",
		Completeness: RetentionStatusUnavailable,
	}
}

func validateRetentionStatus(status RetentionStatus) error {
	if status.SchemaVersion != RetentionStatusSchema {
		return fmt.Errorf("schema %q is not %q", status.SchemaVersion, RetentionStatusSchema)
	}
	if status.SchemaLink != RetentionStatusSchemaLink {
		return fmt.Errorf("schema link %q is not %q", status.SchemaLink, RetentionStatusSchemaLink)
	}
	if status.WarningCode != RetentionStatusWarningCode {
		return fmt.Errorf("warning code %q is not %q", status.WarningCode, RetentionStatusWarningCode)
	}
	wantLimits := RetentionStatusLimits{
		Owners:                              RetentionStatusOwnerCount,
		Components:                          RetentionStatusComponentCount,
		ReportedIdentitiesPerSummary:        RetentionStatusReportedIdentityLimit,
		ScanIdentitiesPerSummary:            RetentionStatusScanIdentityLimit,
		AggregateReportedIdentityAllocation: RetentionStatusAggregateReportedIdentityAllocation,
		AggregateScanIdentityAllocation:     RetentionStatusAggregateScanIdentityAllocation,
		EncodedResponseByteLimit:            RetentionStatusResponseByteLimit,
	}
	if status.Limits != wantLimits {
		return errors.New("limits do not match the frozen retention contract")
	}
	if len(status.Owners) != len(retentionStatusRegistry) {
		return fmt.Errorf("owner count %d is not %d", len(status.Owners), len(retentionStatusRegistry))
	}
	flatComponentIndex := 0
	for ownerIndex, definition := range retentionStatusRegistry {
		owner := status.Owners[ownerIndex]
		if owner.ID != definition.id {
			return fmt.Errorf("owner %d is %q, want %q", ownerIndex, owner.ID, definition.id)
		}
		if owner.Scope != definition.scope ||
			owner.DecisionRelation != definition.decisionRelation {
			return fmt.Errorf("owner %q metadata does not match the frozen contract", owner.ID)
		}
		if definition.proofRetention {
			if err := validateProofBundleRetentionPosture(owner); err != nil {
				return fmt.Errorf("owner %q retention posture: %w", owner.ID, err)
			}
		} else {
			if owner.Accumulating != definition.accumulating {
				return fmt.Errorf("owner %q accumulation metadata does not match the frozen contract", owner.ID)
			}
			if !sameRetentionControl(owner.RetentionControl, definition.retentionControl) {
				return fmt.Errorf("owner %q retention control does not match the frozen contract", owner.ID)
			}
		}
		if len(owner.Components) != len(definition.components) {
			return fmt.Errorf("owner %q component count %d is not %d", owner.ID, len(owner.Components), len(definition.components))
		}
		for componentIndex, componentDefinition := range definition.components {
			component := owner.Components[componentIndex]
			if component.ID != componentDefinition.id {
				return fmt.Errorf("owner %q component %d is %q, want %q", owner.ID, componentIndex, component.ID, componentDefinition.id)
			}
			if component.Allocation != retentionStatusAllocation(flatComponentIndex) {
				return fmt.Errorf("component %q allocation does not match the frozen contract", component.ID)
			}
			if component.ScannedIdentities < 0 || component.ScannedIdentities > component.Allocation.ScanIdentities {
				return fmt.Errorf("component %q scanned identities exceed its allocation", component.ID)
			}
			if err := validateRetentionCountSummary(component); err != nil {
				return fmt.Errorf("component %q count: %w", component.ID, err)
			}
			if len(component.ByteMetrics) != len(componentDefinition.byteKinds) {
				return fmt.Errorf(
					"component %q byte metric count %d is not %d",
					component.ID,
					len(component.ByteMetrics),
					len(componentDefinition.byteKinds),
				)
			}
			for byteIndex, kind := range componentDefinition.byteKinds {
				metric := component.ByteMetrics[byteIndex]
				if metric.Kind != kind {
					return fmt.Errorf(
						"component %q byte metric %d is %q, want %q",
						component.ID,
						byteIndex,
						metric.Kind,
						kind,
					)
				}
				if err := validateRetentionByteMetric(metric); err != nil {
					return fmt.Errorf("component %q byte metric %q: %w", component.ID, kind, err)
				}
			}
			flatComponentIndex++
		}
	}
	if err := validateRetentionMetric(status.DataVolume.TotalBytes, "bytes"); err != nil {
		return fmt.Errorf("data volume total: %w", err)
	}
	if err := validateRetentionMetric(status.DataVolume.AvailableBytes, "bytes"); err != nil {
		return fmt.Errorf("data volume available: %w", err)
	}
	return nil
}

func validateProofBundleRetentionPosture(owner RetentionOwnerStatus) error {
	control := owner.RetentionControl
	if control == nil {
		return errors.New("retention control is absent")
	}
	if control.ConfigKey != proofBundleRetentionControl.ConfigKey ||
		control.PositiveLifetimeEffect != RetentionStatusProofBundlePositiveLifetimeEffect {
		return errors.New("retention control identity does not match the frozen contract")
	}
	switch control.DefaultState {
	case "disabled":
		if !owner.Accumulating {
			return errors.New("disabled retention is not accumulating")
		}
	case "enabled":
		if owner.Accumulating {
			return errors.New("enabled retention is accumulating")
		}
	default:
		return fmt.Errorf("unsupported default state %q", control.DefaultState)
	}
	return nil
}

func validateRetentionCountSummary(component RetentionComponentStatus) error {
	if err := validateRetentionMetric(component.Count, "identities"); err != nil {
		return err
	}
	if component.Count.Value != nil &&
		*component.Count.Value > int64(component.Allocation.ReportedIdentities) {
		return errors.New("value exceeds its report allocation")
	}

	scanned := int64(component.ScannedIdentities)
	switch component.Count.Completeness {
	case RetentionStatusUnavailable:
		if component.Truncated {
			return errors.New("unavailable summary is marked truncated")
		}
	case RetentionStatusExact:
		if component.Truncated ||
			component.ScannedIdentities > component.Allocation.ReportedIdentities ||
			*component.Count.Value != scanned {
			return errors.New("exact value does not equal its bounded scan")
		}
	case RetentionStatusLowerBound:
		if component.Truncated {
			if component.ScannedIdentities != component.Allocation.ScanIdentities ||
				*component.Count.Value != int64(component.Allocation.ReportedIdentities) {
				return errors.New("invalid cap-plus-one sentinel summary")
			}
			return nil
		}
		if component.ScannedIdentities > component.Allocation.ReportedIdentities ||
			*component.Count.Value != scanned ||
			*component.Count.Value == 0 {
			return errors.New("partial lower bound does not equal its nonempty bounded scan")
		}
	}
	return nil
}

func sameRetentionControl(left, right *RetentionStatusControl) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateRetentionByteMetric(metric RetentionStatusByteMetric) error {
	return validateRetentionMetric(
		RetentionStatusMetric{
			Value:        metric.Value,
			Unit:         metric.Unit,
			Completeness: metric.Completeness,
		},
		"bytes",
	)
}

func validateRetentionMetric(metric RetentionStatusMetric, unit string) error {
	if metric.Unit != unit {
		return fmt.Errorf("unit %q is not %q", metric.Unit, unit)
	}
	switch metric.Completeness {
	case RetentionStatusUnavailable:
		if metric.Value != nil {
			return errors.New("unavailable metric has a value")
		}
	case RetentionStatusExact, RetentionStatusLowerBound:
		if metric.Value == nil {
			return errors.New("available metric has no value")
		}
		if *metric.Value < 0 {
			return errors.New("available metric is negative")
		}
	default:
		return fmt.Errorf("unsupported completeness %q", metric.Completeness)
	}
	return nil
}

func encodedRetentionStatusBytes(status RetentionStatus) (int, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(status); err != nil {
		return 0, err
	}
	return encoded.Len(), nil
}

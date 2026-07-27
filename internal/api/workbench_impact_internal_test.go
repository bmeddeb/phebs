package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	impactInvestigationID = "01J00000000000000000000021"
	impactRevisionID      = "ivr_t217"
	impactRunID           = "ir_t217"
	impactCommit          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type impactWorkbenchFake struct {
	view          *store.WorkbenchView
	compatibility *store.WorkbenchCompatibilityAnalysis
}

func (fake *impactWorkbenchFake) Read(
	_ context.Context,
	_, _ string,
) (*store.WorkbenchView, error) {
	if fake.view == nil {
		return nil, store.ErrNotFound
	}
	value := *fake.view
	return &value, nil
}

func (fake *impactWorkbenchFake) ReadCompatibility(
	_ context.Context,
	_, investigationID, revisionID, runID string,
) (*store.WorkbenchCompatibilityAnalysis, error) {
	if fake.compatibility == nil ||
		investigationID != fake.compatibility.InvestigationID ||
		revisionID != fake.compatibility.RevisionID ||
		runID != fake.compatibility.Run.ID {
		return nil, store.ErrNotFound
	}
	value := *fake.compatibility
	return &value, nil
}

type impactCatalogFake struct {
	calls  []store.ChangeBriefContractSelection
	hidden map[string]bool
}

func (fake *impactCatalogFake) OperationForProtocol(
	_ context.Context,
	protocol, repository, lineage, operation string,
) (*ContractCatalogOperation, error) {
	if fake.hidden[repository] {
		return nil, store.ErrNotFound
	}
	selection := store.ChangeBriefContractSelection{
		Protocol: protocol, Repository: repository,
		DeclarationLineage: lineage, CanonicalOperation: operation,
	}
	fake.calls = append(fake.calls, selection)
	coverage := impactCoverage(
		"atlas:"+repository,
		[]string{"proto-contract", "grpc-consumer"},
	)
	return &ContractCatalogOperation{
		SchemaVersion:      contractCatalogSchemaVersion,
		Protocol:           protocol,
		Repository:         repository,
		DeclarationLineage: lineage,
		ServiceFQN:         "shop.Cart",
		Method:             "Get",
		Operation:          operation,
		Declaration: ContractCatalogClaim{
			AssertionID: "declaration-" + repository,
			RunID:       "run-atlas",
			Predicate:   "DECLARES_OPERATION",
			Object:      operation,
			Lineage:     lineage,
			Sources:     []ContractCatalogSource{},
		},
		Implementations: []ContractCatalogRelationship{{
			Kind: "implementation", Classification: "resolved_implementation",
			Claim: ContractCatalogClaim{
				AssertionID: "implementation-" + repository,
				Sources:     []ContractCatalogSource{},
			},
		}},
		Callers: []ContractCatalogRelationship{
			{
				Kind: "caller", Classification: "resolved_caller",
				Claim: ContractCatalogClaim{
					AssertionID: "atlas-resolved-" + repository,
					Sources:     []ContractCatalogSource{},
				},
			},
			{
				Kind: "caller", Classification: "unresolved_name_match",
				Claim: ContractCatalogClaim{
					AssertionID: "name-match-" + repository,
					Sources:     []ContractCatalogSource{},
				},
			},
		},
		UnresolvedCandidates: []ContractCatalogRelationship{{
			Kind:           "unresolved_candidate",
			Classification: "extractor_abstention",
			Claim: ContractCatalogClaim{
				AssertionID: "atlas-abstention-" + repository,
				Sources:     []ContractCatalogSource{},
			},
		}},
		CoverageDigest: coverage.Digest,
		Coverage:       coverage,
	}, nil
}

type impactCallerFake struct {
	calls  []string
	pages  int
	hidden map[string]bool
}

func (fake *impactCallerFake) List(
	_ context.Context,
	query CallerMapQuery,
	_ int,
	cursor string,
) (*CallerMapPage, error) {
	if fake.hidden[query.Endpoint.Repository] {
		return nil, store.ErrNotFound
	}
	fake.calls = append(fake.calls, cursor)
	coverage := impactCoverage(
		"caller:"+query.Endpoint.Repository,
		[]string{"proto-contract", "grpc-caller"},
	)
	page := 1
	if cursor == "caller-2" {
		page = 2
	}
	complete := fake.pages <= page
	next := ""
	if !complete {
		next = "caller-" + string(rune('1'+page))
	}
	rows := []CallerMapRow{
		{
			Classification: "resolved_caller",
			Resolution:     "scip",
			Protocol:       query.Endpoint.Protocol,
			Operation:      query.Endpoint.Operation,
			Unit: CallerMapUnitAttribution{
				State: "ambiguous",
				Candidates: []CallerMapUnitCandidate{
					{ID: "unit:a"},
					{ID: "unit:b"},
				},
				CandidateTotal: 2,
			},
			Source: CallerMapSource{
				Repository: query.Endpoint.Repository,
				Commit:     impactCommit,
				Path:       "client.go",
			},
		},
		{
			Classification:   "extractor_abstention",
			Resolution:       "unresolved",
			Protocol:         query.Endpoint.Protocol,
			Operation:        query.Endpoint.Operation,
			Unit:             CallerMapUnitAttribution{State: "unattributed"},
			UnresolvedReason: "DYNAMIC_RECEIVER",
			Source: CallerMapSource{
				Repository: query.Endpoint.Repository,
				Commit:     impactCommit,
				Path:       "dynamic.go",
			},
		},
	}
	return &CallerMapPage{
		SchemaVersion: callerMapSchemaVersion,
		Query:         query,
		Declaration: ContractCatalogClaim{
			AssertionID: "caller-declaration",
			Sources:     []ContractCatalogSource{},
		},
		Rows:              rows,
		Groups:            []CallerMapGroup{},
		TotalMatchingRows: fake.pages * len(rows),
		Pagination: CallerMapPagination{
			Complete: complete, NextCursor: next,
		},
		CoverageDigest:    coverage.Digest,
		AttributionDigest: "sha256:" + strings.Repeat("b", 64),
		Coverage:          coverage,
	}, nil
}

type impactComparisonFake struct {
	calls  []string
	hidden map[string]bool
}

func (fake *impactComparisonFake) Compare(
	_ context.Context,
	query CallerComparisonQuery,
	_ int,
	cursor string,
) (*CallerComparisonPage, error) {
	if fake.hidden[query.Old.Repository] ||
		fake.hidden[query.Replacement.Repository] {
		return nil, store.ErrNotFound
	}
	fake.calls = append(fake.calls, cursor)
	coverage := impactCoverage(
		"comparison",
		[]string{"proto-contract", "grpc-caller"},
	)
	return &CallerComparisonPage{
		SchemaVersion: callerComparisonSchemaVersion,
		Query:         query,
		Old: CallerComparisonSnapshot{
			Endpoint:       query.Old,
			CoverageDigest: coverage.Digest,
		},
		Replacement: CallerComparisonSnapshot{
			Endpoint:       query.Replacement,
			CoverageDigest: coverage.Digest,
		},
		Rows: []CallerComparisonRow{{
			Level: "occurrence", Key: "caller.go:1",
			Classification: "old_only_evidence",
		}},
		TotalRows:  1,
		Pagination: CallerMapPagination{Complete: true},
		Coverage:   coverage,
	}, nil
}

type impactFieldFake struct {
	calls []string
	pages int
}

func (fake *impactFieldFake) List(
	_ context.Context,
	query FieldReferenceQuery,
	_ int,
	cursor string,
) (*FieldReferencePage, error) {
	fake.calls = append(fake.calls, cursor)
	coverage := impactCoverage(
		"fields",
		[]string{"scip-proto-field"},
	)
	page := 1
	switch cursor {
	case "field-2":
		page = 2
	case "field-3":
		page = 3
	}
	complete := fake.pages <= page
	next := ""
	if !complete {
		next = "field-" + string(rune('1'+page))
	}
	return &FieldReferencePage{
		SchemaVersion: fieldReferenceSchemaVersion,
		Query:         query,
		Rows: []FieldReferenceRow{{
			Field: query.Fields[0],
			Assertion: store.Assertion{
				ID:        "field-reference",
				Predicate: "REFERENCES_PROTO_FIELD",
			},
			Evidence: []BundleEvidence{},
		}},
		TotalRows: fake.pages,
		Pagination: CallerMapPagination{
			Complete: complete, NextCursor: next,
		},
		CoverageDigest: coverage.Digest,
		Coverage:       coverage,
		VisibilityContext: VisibilityContext{
			Principal: "user:t217",
		},
	}, nil
}

type impactResourcePack struct {
	state ResourcePlaneState
	calls int
}

func (pack *impactResourcePack) ReadResourcePlane(
	_ context.Context,
	_ ResourcePlaneContext,
) (ResourcePlanePackSnapshot, error) {
	pack.calls++
	return ResourcePlanePackSnapshot{
		State:  pack.state,
		Reason: "fixture",
		Relationships: []ResourcePlaneRelationship{{
			Kind: "uses", Subject: "service:a", Object: "resource:a",
			Classification: "resolved",
			Sources:        []ContractCatalogSource{},
		}},
	}, nil
}

func impactCoverage(
	name string,
	domains []string,
) extract.CoverageCertificate {
	digest := digestJSON(struct {
		Name    string
		Domains []string
	}{Name: name, Domains: domains})
	runs := make([]extract.CertificateRun, len(domains))
	for index, domain := range domains {
		runs[index] = extract.CertificateRun{
			Domain: domain, Status: "published",
			RunID: "run-" + domain, Commit: impactCommit,
			Fresh: true,
		}
	}
	return extract.CoverageCertificate{
		SchemaVersion:   "coverage-certificate-v1",
		Domains:         slices.Clone(domains),
		RepositoryCount: 1,
		Repositories: []extract.CertificateRepository{{
			Repository:    "github.com/acme/contracts",
			IndexedCommit: impactCommit,
			SCIPIndex:     "present",
			Runs:          runs,
		}},
		Digest: digest,
	}
}

func impactSelection(
	role store.ChangeBriefSelectionRole,
	suffix string,
) store.ChangeBriefContractSelection {
	return store.ChangeBriefContractSelection{
		Role:       role,
		Protocol:   "protobuf",
		Repository: "github.com/acme/" + suffix,
		DeclarationLineage: "proto/" + suffix +
			".proto:shop.Cart",
		CanonicalOperation: "/shop.Cart/Get",
	}
}

func impactView(
	kind store.ChangeBriefTicketKind,
	selections []store.ChangeBriefContractSelection,
) *store.WorkbenchView {
	return &store.WorkbenchView{
		Investigation: store.Investigation{
			ID:                impactInvestigationID,
			Owner:             "user:t217",
			CurrentRevisionID: impactRevisionID,
		},
		Revision: store.Revision{
			ID:              impactRevisionID,
			InvestigationID: impactInvestigationID,
			ContentDigest:   "sha256:" + strings.Repeat("c", 64),
		},
		Brief: store.ChangeBrief{
			ID:              "icb_t217",
			InvestigationID: impactInvestigationID,
			RevisionID:      impactRevisionID,
			TicketKind:      kind,
			What: store.ChangeBriefWhat{
				Selections: selections,
			},
			ContentDigest: "sha256:" + strings.Repeat("d", 64),
		},
	}
}

func impactCompatibility(
	fields []compat.FieldIdentity,
) *store.WorkbenchCompatibilityAnalysis {
	return &store.WorkbenchCompatibilityAnalysis{
		SchemaVersion:   store.WorkbenchCompatibilityAnalysisSchemaVersion,
		Status:          "published",
		InvestigationID: impactInvestigationID,
		RevisionID:      impactRevisionID,
		Run: store.Run{
			ID: impactRunID, RevisionID: impactRevisionID,
		},
		Artifact: store.RunArtifact{
			RunID:         impactRunID,
			ContentDigest: "sha256:" + strings.Repeat("e", 64),
		},
		Compatibility: &compat.CompatibilityResult{
			AffectedFields: fields,
			Violations:     []compat.Violation{},
		},
	}
}

func impactService(
	view *store.WorkbenchView,
	compatibility *store.WorkbenchCompatibilityAnalysis,
	resources *ResourcePlaneRegistry,
) (*WorkbenchImpactService, *impactCatalogFake, *impactCallerFake, *impactComparisonFake, *impactFieldFake) {
	workbench := &impactWorkbenchFake{
		view: view, compatibility: compatibility,
	}
	catalog := &impactCatalogFake{}
	callers := &impactCallerFake{pages: 1}
	comparison := &impactComparisonFake{}
	fields := &impactFieldFake{pages: 1}
	if resources == nil {
		resources = DefaultWorkbenchResourcePlanes()
	}
	return &WorkbenchImpactService{
		opts: Options{
			Principal: func(context.Context) string {
				return "user:t217"
			},
		},
		workbench:     workbench,
		catalog:       catalog,
		callers:       callers,
		comparison:    comparison,
		fields:        fields,
		compatibility: workbench,
		resources:     resources,
	}, catalog, callers, comparison, fields
}

func TestWorkbenchImpactScenarioComposition(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	replacement := impactSelection(
		store.ChangeBriefReplacement,
		"replacement",
	)
	analogous := impactSelection(store.ChangeBriefAnalogous, "analogous")
	field := compat.FieldIdentity{
		Lineage: current.DeclarationLineage,
		Message: "shop.Cart",
		Number:  1,
	}
	tests := []struct {
		name            string
		kind            store.ChangeBriefTicketKind
		selections      []store.ChangeBriefContractSelection
		compatibility   *store.WorkbenchCompatibilityAnalysis
		compatibilityID string
		wantAtlas       int
		wantCallers     int
		wantComparison  int
		wantFields      int
	}{
		{
			name: "add", kind: store.ChangeBriefAdd,
			selections: []store.ChangeBriefContractSelection{analogous},
			wantAtlas:  1,
		},
		{
			name: "modify", kind: store.ChangeBriefModify,
			selections: []store.ChangeBriefContractSelection{current},
			compatibility: impactCompatibility(
				[]compat.FieldIdentity{field},
			),
			compatibilityID: impactRunID,
			wantAtlas:       1,
			wantCallers:     1,
			wantFields:      1,
		},
		{
			name: "migrate", kind: store.ChangeBriefMigrate,
			selections: []store.ChangeBriefContractSelection{
				current,
				replacement,
			},
			wantAtlas:      2,
			wantComparison: 1,
		},
		{
			name: "retire", kind: store.ChangeBriefRetire,
			selections:  []store.ChangeBriefContractSelection{current},
			wantAtlas:   1,
			wantCallers: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, catalog, callers, comparison, fields :=
				impactService(
					impactView(test.kind, test.selections),
					test.compatibility,
					nil,
				)
			page, err := service.Read(
				context.Background(),
				"user:t217",
				WorkbenchImpactRequest{
					InvestigationID:  impactInvestigationID,
					RevisionID:       impactRevisionID,
					CompatibilityRun: test.compatibilityID,
					PageSize:         10,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			gotComparison := 0
			if page.Comparison != nil {
				gotComparison = 1
			}
			gotFields := 0
			if page.FieldReferences != nil {
				gotFields = 1
			}
			if len(page.Atlas) != test.wantAtlas ||
				len(page.Callers) != test.wantCallers ||
				gotComparison != test.wantComparison ||
				gotFields != test.wantFields ||
				len(catalog.calls) != test.wantAtlas ||
				len(callers.calls) != test.wantCallers ||
				len(comparison.calls) != test.wantComparison ||
				len(fields.calls) != test.wantFields ||
				len(page.ScenarioEmphasis) == 0 {
				t.Fatalf(
					"composition page=%+v calls atlas=%d caller=%d comparison=%d fields=%d",
					page,
					len(catalog.calls),
					len(callers.calls),
					len(comparison.calls),
					len(fields.calls),
				)
			}
			if test.kind == store.ChangeBriefAdd &&
				(len(page.Atlas[0].NameMatches) != 0 ||
					len(page.Atlas[0].ExtractorAbstentions) != 0) {
				t.Fatalf(
					"add promoted analogous callers into findings: %+v",
					page.Atlas[0],
				)
			}
		})
	}
}

func TestWorkbenchImpactKeepsEvidenceClassesSeparate(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	field := compat.FieldIdentity{
		Lineage: current.DeclarationLineage,
		Message: "shop.Cart",
		Number:  1,
	}
	service, _, _, _, _ := impactService(
		impactView(
			store.ChangeBriefModify,
			[]store.ChangeBriefContractSelection{current},
		),
		impactCompatibility([]compat.FieldIdentity{field}),
		nil,
	)
	page, err := service.Read(
		context.Background(),
		"user:t217",
		WorkbenchImpactRequest{
			InvestigationID:  impactInvestigationID,
			RevisionID:       impactRevisionID,
			CompatibilityRun: impactRunID,
			PageSize:         10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Atlas) != 1 ||
		len(page.Atlas[0].Implementations) != 1 ||
		len(page.Atlas[0].NameMatches) != 1 ||
		len(page.Atlas[0].ExtractorAbstentions) != 1 ||
		len(page.Callers) != 1 ||
		len(page.Callers[0].ResolvedCallers) != 1 ||
		len(page.Callers[0].ExtractorAbstentions) != 1 ||
		page.Callers[0].ResolvedCallers[0].Unit.State != "ambiguous" ||
		page.Callers[0].ResolvedCallers[0].Unit.CandidateTotal != 2 ||
		page.FieldReferences == nil ||
		len(page.FieldReferences.Rows) != 1 {
		t.Fatalf("evidence classes collapsed: %+v", page)
	}
	if page.Atlas[0].NameMatches[0].Classification !=
		"unresolved_name_match" ||
		page.Callers[0].ResolvedCallers[0].Classification !=
			"resolved_caller" ||
		page.Callers[0].ExtractorAbstentions[0].Classification !=
			"extractor_abstention" {
		t.Fatalf("classifications changed: %+v", page)
	}
}

func TestWorkbenchImpactPaginationPreservesPlaneAndSnapshotState(
	t *testing.T,
) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	field := compat.FieldIdentity{
		Lineage: current.DeclarationLineage,
		Message: "shop.Cart",
		Number:  1,
	}
	enabled := &impactResourcePack{state: ResourcePlaneEnabled}
	stale := &impactResourcePack{state: ResourcePlaneStale}
	registry, err := NewResourcePlaneRegistry(
		[]ResourcePlaneRegistration{
			{
				ID: "enabled", Label: "Enabled",
				State: ResourcePlaneEnabled, Pack: enabled,
			},
			{
				ID: "failed", Label: "Failed",
				State: ResourcePlaneFailed, Reason: "fixture_failed",
			},
			{
				ID: "human", Label: "Human",
				State:  ResourcePlaneHumanAsserted,
				Reason: "human_note_only",
			},
			{
				ID: "stale", Label: "Stale",
				State: ResourcePlaneEnabled, Pack: stale,
			},
			{
				ID: "unsupported", Label: "Unsupported",
				State:  ResourcePlaneUnsupported,
				Reason: "no_pack",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service, _, callers, _, fields := impactService(
		impactView(
			store.ChangeBriefModify,
			[]store.ChangeBriefContractSelection{current},
		),
		impactCompatibility([]compat.FieldIdentity{field}),
		registry,
	)
	callers.pages = 2
	fields.pages = 3
	request := WorkbenchImpactRequest{
		InvestigationID:  impactInvestigationID,
		RevisionID:       impactRevisionID,
		CompatibilityRun: impactRunID,
		PageSize:         1,
	}
	var pages []*WorkbenchImpactPage
	for {
		page, err := service.Read(
			context.Background(),
			"user:t217",
			request,
		)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, page)
		if page.Pagination.Complete {
			break
		}
		request.Cursor = page.Pagination.NextCursor
		if len(pages) > 4 {
			t.Fatal("impact pagination did not terminate")
		}
	}
	if len(pages) != 3 ||
		len(pages[0].Callers) != 1 ||
		len(pages[1].Callers) != 1 ||
		len(pages[2].Callers) != 0 ||
		pages[2].FieldReferences == nil ||
		enabled.calls != 3 ||
		stale.calls != 3 {
		t.Fatalf(
			"paged composition = pages:%d callers:%v enabled:%d stale:%d",
			len(pages),
			[]int{
				len(pages[0].Callers),
				len(pages[1].Callers),
				len(pages[2].Callers),
			},
			enabled.calls,
			stale.calls,
		)
	}
	wantStates := []ResourcePlaneState{
		ResourcePlaneEnabled,
		ResourcePlaneFailed,
		ResourcePlaneHumanAsserted,
		ResourcePlaneStale,
		ResourcePlaneUnsupported,
	}
	for index, page := range pages {
		states := make([]ResourcePlaneState, len(page.ResourcePlanes))
		for planeIndex, plane := range page.ResourcePlanes {
			states[planeIndex] = plane.State
			if plane.State != ResourcePlaneEnabled &&
				len(plane.Relationships) != 0 {
				t.Fatalf(
					"page %d plane %s contributed relationships in state %s",
					index,
					plane.ID,
					plane.State,
				)
			}
		}
		if !slices.Equal(states, wantStates) {
			t.Fatalf("page %d states = %v", index, states)
		}
		encoded, err := json.Marshal(page.AnalysisScope.Gaps)
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range []string{
			"failed",
			"human_asserted",
			"stale",
			"unsupported",
		} {
			if !strings.Contains(string(encoded), `"state":"`+state+`"`) {
				t.Fatalf(
					"page %d lost %s gap after pagination: %s",
					index,
					state,
					encoded,
				)
			}
		}
	}
	if !reflect.DeepEqual(
		callers.calls,
		[]string{"", "caller-2", ""},
	) || !reflect.DeepEqual(
		fields.calls,
		[]string{"", "field-2", "field-3"},
	) {
		t.Fatalf(
			"delegate cursors caller=%v fields=%v",
			callers.calls,
			fields.calls,
		)
	}
}

func TestWorkbenchImpactFailsClosedForHiddenSelectedRepository(
	t *testing.T,
) {
	current := impactSelection(store.ChangeBriefCurrent, "hidden")
	service, catalog, callers, comparison, _ := impactService(
		impactView(
			store.ChangeBriefRetire,
			[]store.ChangeBriefContractSelection{current},
		),
		nil,
		nil,
	)
	catalog.hidden = map[string]bool{current.Repository: true}
	callers.hidden = map[string]bool{current.Repository: true}
	comparison.hidden = map[string]bool{current.Repository: true}
	page, err := service.Read(
		context.Background(),
		"user:t217",
		WorkbenchImpactRequest{
			InvestigationID: impactInvestigationID,
			RevisionID:      impactRevisionID,
			PageSize:        10,
		},
	)
	if !errors.Is(err, store.ErrNotFound) || page != nil ||
		len(callers.calls) != 0 {
		t.Fatalf(
			"hidden exact target = page:%+v err:%v caller calls:%v",
			page,
			err,
			callers.calls,
		)
	}
}

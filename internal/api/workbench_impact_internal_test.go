package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/callerexecute"
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
	reads         int
	readHook      func(int, *store.WorkbenchView) (*store.WorkbenchView, error)
}

func (fake *impactWorkbenchFake) Read(
	_ context.Context,
	_, _ string,
) (*store.WorkbenchView, error) {
	fake.reads++
	if fake.view == nil {
		return nil, store.ErrNotFound
	}
	value := *fake.view
	if fake.readHook != nil {
		return fake.readHook(fake.reads, &value)
	}
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
	calls           []store.ChangeBriefContractSelection
	hidden          map[string]bool
	coverageMutator func(*extract.CoverageCertificate)
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
	if fake.coverageMutator != nil {
		fake.coverageMutator(&coverage)
		coverage.Digest = digestJSON(coverage)
	}
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
	calls        []string
	confirmCalls []string
	pages        int
	hidden       map[string]bool
	state        string
	snapshot     string
	authority    string
}

func (fake *impactCallerFake) List(
	_ context.Context,
	query CallerMapQuery,
	_ int,
	cursor string,
) (*CallerMapPage, error) {
	query = normalizeCallerMapQuery(query)
	if fake.hidden[query.Endpoint.Repository] {
		return nil, store.ErrNotFound
	}
	fake.calls = append(fake.calls, cursor)
	state := fake.state
	if state == "" {
		state = string(callerexecute.PublicationCurrent)
	}
	snapshot := fake.snapshot
	if snapshot == "" {
		snapshot = "caller-snapshot"
	}
	authority := fake.authority
	if authority == "" {
		authority = "caller-authority"
	}
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
	generation := CallerMapGeneration{
		State: state, Plane: exactCallerMapPlane,
		Repository: query.Endpoint.Repository, Commit: impactCommit,
		GenerationDigest:    "sha256:" + strings.Repeat("6", 64),
		ManifestDigest:      "sha256:" + strings.Repeat("7", 64),
		PairSetDigest:       "sha256:" + strings.Repeat("8", 64),
		PublicationRevision: 1,
	}
	zero := 0
	generation.PartitionProgress = &CallerMapPartitionProgress{
		State: "complete", TotalPairCount: &zero,
	}
	generation.RecordCounts = &CallerMapRecordCounts{}
	matchingState := "exact"
	declaration := &ContractCatalogClaim{
		AssertionID: "caller-declaration",
		Sources:     []ContractCatalogSource{},
	}
	total := callerMapTotal(fake.pages * len(rows))
	if state != string(callerexecute.PublicationCurrent) {
		generation.RecordCounts = nil
		generation.PartitionProgress = &CallerMapPartitionProgress{
			State: "unavailable",
		}
		matchingState = "unavailable"
		declaration = nil
		total = nil
		rows = []CallerMapRow{}
		complete = true
		next = ""
	}
	return &CallerMapPage{
		SchemaVersion:     exactCallerMapSchemaVersion,
		Query:             query,
		Declaration:       declaration,
		Rows:              rows,
		Groups:            []CallerMapGroup{},
		TotalMatchingRows: total,
		Pagination: CallerMapPagination{
			Complete: complete, NextCursor: next,
		},
		Generation: &generation,
		Scope: &AnalysisScopeProjection{
			Repository: query.Endpoint.Repository,
			Commit:     impactCommit, ScopePosture: "whole-repository",
		},
		MatchingRowsState: matchingState,
		exactSnapshot:     snapshot,
		exactAuthority:    authority,
	}, nil
}

func (fake *impactCallerFake) confirmWorkbenchCallerSnapshot(
	_ context.Context,
	query CallerMapQuery,
	_ int,
	authority string,
) (exactCallerSnapshotConfirmation, error) {
	fake.confirmCalls = append(fake.confirmCalls, authority)
	if fake.hidden[query.Endpoint.Repository] {
		return exactCallerSnapshotConfirmation{}, store.ErrNotFound
	}
	wantAuthority := fake.authority
	if wantAuthority == "" {
		wantAuthority = "caller-authority"
	}
	if authority != wantAuthority {
		return exactCallerSnapshotConfirmation{},
			huma.Error409Conflict("caller snapshot changed")
	}
	state := fake.state
	if state == "" {
		state = string(callerexecute.PublicationCurrent)
	}
	snapshot := fake.snapshot
	if snapshot == "" {
		snapshot = "caller-snapshot"
	}
	matchingState := "exact"
	if state != string(callerexecute.PublicationCurrent) {
		matchingState = "unavailable"
	}
	return exactCallerSnapshotConfirmation{
		Snapshot: snapshot, MatchingRowsState: matchingState,
		Generation: CallerMapGeneration{
			State: state, Plane: exactCallerMapPlane,
			Repository: query.Endpoint.Repository,
		},
		Scope: AnalysisScopeProjection{
			Repository:   query.Endpoint.Repository,
			ScopePosture: "whole-repository",
		},
	}, nil
}

func TestValidateWorkbenchCallerGapKeepsCurrentScopeSeparateFromMissingGeneration(
	t *testing.T,
) {
	query := normalizeCallerMapQuery(CallerMapQuery{
		Endpoint: CallerMapEndpoint{
			Protocol: "grpc", Repository: "example.com/orders",
			Operation: "/demo.Orders/Create",
		},
	})
	page := &CallerMapPage{
		SchemaVersion: exactCallerMapSchemaVersion,
		Query:         query,
		Rows:          []CallerMapRow{},
		Groups:        []CallerMapGroup{},
		Pagination:    CallerMapPagination{Complete: true},
		Generation: &CallerMapGeneration{
			State: "missing", Plane: exactCallerMapPlane,
			Repository: query.Endpoint.Repository,
			PartitionProgress: &CallerMapPartitionProgress{
				State: "unavailable",
			},
		},
		Scope: &AnalysisScopeProjection{
			Repository: query.Endpoint.Repository,
			Commit:     impactCommit, ScopePosture: "whole-repository",
		},
		MatchingRowsState: "unavailable",
		exactSnapshot:     "missing-generation-snapshot",
		exactAuthority:    "missing-generation-authority",
	}
	if err := validateWorkbenchExactCallerPage(page, query); err != nil {
		t.Fatalf("current scope beside missing generation was rejected: %v", err)
	}
}

type impactComparisonFake struct {
	calls        []string
	confirmCalls []string
	hidden       map[string]bool
	state        string
	snapshot     string
	authority    string
}

func (fake *impactComparisonFake) Compare(
	_ context.Context,
	query CallerComparisonQuery,
	_ int,
	cursor string,
) (*CallerComparisonPage, error) {
	query = normalizeCallerComparisonQuery(query)
	if fake.hidden[query.Old.Repository] ||
		fake.hidden[query.Replacement.Repository] {
		return nil, store.ErrNotFound
	}
	fake.calls = append(fake.calls, cursor)
	state := fake.state
	if state == "" {
		state = string(callerexecute.PublicationCurrent)
	}
	matchingState := "exact"
	if state != string(callerexecute.PublicationCurrent) {
		matchingState = "unavailable"
	}
	total := 1
	page := &CallerComparisonPage{
		SchemaVersion: callerComparisonSchemaVersion,
		Query:         query,
		Old: CallerComparisonSnapshot{
			Endpoint: query.Old,
		},
		Replacement: CallerComparisonSnapshot{
			Endpoint: query.Replacement,
		},
		Rows: []CallerComparisonRow{{
			Level: "occurrence", Key: "caller.go:1",
			Classification: "old_only_evidence",
		}},
		TotalRows:         &total,
		MatchingRowsState: matchingState,
		Pagination:        CallerMapPagination{Complete: true},
		exactSnapshot:     "comparison-snapshot",
		exactAuthority:    "comparison-authority",
	}
	if fake.snapshot != "" {
		page.exactSnapshot = fake.snapshot
	}
	if fake.authority != "" {
		page.exactAuthority = fake.authority
	}
	for side, endpoint := range map[string]CallerMapEndpoint{
		"old": query.Old, "replacement": query.Replacement,
	} {
		generation := &CallerMapGeneration{
			State: state, Plane: exactCallerMapPlane,
			Repository: endpoint.Repository, Commit: impactCommit,
		}
		declaration := &ContractCatalogClaim{
			AssertionID: side + "-declaration",
			Sources:     []ContractCatalogSource{},
		}
		snapshot := CallerComparisonSnapshot{
			Endpoint: endpoint, Declaration: declaration,
			Generation: generation, MatchingRowsState: matchingState,
		}
		if side == "old" {
			page.Old = snapshot
		} else {
			page.Replacement = snapshot
		}
	}
	if matchingState == "unavailable" {
		page.Rows = []CallerComparisonRow{}
		page.TotalRows = nil
		page.Old.Declaration = nil
		page.Replacement.Declaration = nil
	}
	return page, nil
}

func (fake *impactComparisonFake) confirmWorkbenchComparisonSnapshot(
	_ context.Context,
	query CallerComparisonQuery,
	_ int,
	authority string,
) (exactCallerComparisonSnapshotConfirmation, error) {
	fake.confirmCalls = append(fake.confirmCalls, authority)
	wantAuthority := fake.authority
	if wantAuthority == "" {
		wantAuthority = "comparison-authority"
	}
	if authority != wantAuthority {
		return exactCallerComparisonSnapshotConfirmation{},
			huma.Error409Conflict("comparison snapshot changed")
	}
	state := fake.state
	if state == "" {
		state = string(callerexecute.PublicationCurrent)
	}
	matchingState := "exact"
	if state != string(callerexecute.PublicationCurrent) {
		matchingState = "unavailable"
	}
	project := func(endpoint CallerMapEndpoint) CallerComparisonExactSnapshot {
		return CallerComparisonExactSnapshot{
			Endpoint: endpoint,
			Generation: CallerMapGeneration{
				State: state, Plane: exactCallerMapPlane,
				Repository: endpoint.Repository,
			},
			MatchingRowsState: matchingState,
		}
	}
	snapshot := fake.snapshot
	if snapshot == "" {
		snapshot = "comparison-snapshot"
	}
	return exactCallerComparisonSnapshotConfirmation{
		Snapshot: snapshot, MatchingRowsState: matchingState,
		Old: project(query.Old), Replacement: project(query.Replacement),
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
	state         ResourcePlaneState
	calls         int
	relationships []ResourcePlaneRelationship
	err           error
}

type impactRelationshipFake struct {
	rootCoverageCalls int
	snapshotCalls     int
	changeAfter       int
}

func (fake *impactRelationshipFake) List(
	context.Context,
	RelationshipQuery,
	int,
	string,
) (*RelationshipPage, error) {
	return nil, errors.New("retained relationship List was not expected")
}

func (fake *impactRelationshipFake) Compare(
	context.Context,
	RelationshipComparisonQuery,
	int,
	string,
) (*RelationshipComparisonPage, error) {
	return nil, errors.New("relationship comparison was not expected")
}

func (fake *impactRelationshipFake) ReadCitation(
	context.Context,
	string,
) (*RelationshipCitation, error) {
	return nil, errors.New("relationship citation was not expected")
}

func (fake *impactRelationshipFake) RootCoverage(
	_ context.Context,
	repositories []string,
) (*RelationshipProofCoverage, error) {
	fake.rootCoverageCalls++
	roots := make([]RelationshipRootReceipt, len(repositories))
	for index, repository := range repositories {
		rootDigest := "sha256:" + strings.Repeat("8", 64)
		if fake.changeAfter != 0 && fake.rootCoverageCalls >= fake.changeAfter {
			rootDigest = "sha256:" + strings.Repeat("9", 64)
		}
		roots[index] = RelationshipRootReceipt{
			Repository: repository,
			State:      "complete", Generation: "sha256:" + strings.Repeat("7", 64),
			RootDigest: rootDigest, AuthorityDigest: "sha256:" + strings.Repeat("6", 64),
		}
	}
	return &RelationshipProofCoverage{
		SchemaVersion: "phebs-relationship-proof-coverage-v1",
		Visibility: VisibilityContext{
			Principal: "user:t217", AuthorizationProvider: "fixture",
			PermissionSnapshot:         "sha256:" + strings.Repeat("5", 64),
			VisibleRepositorySetDigest: "sha256:" + strings.Repeat("4", 64),
		},
		VisibleRepositoryCount: len(repositories), ExactRootCount: len(repositories),
		State: "exact", Roots: roots, Digest: digestJSON(roots),
	}, nil
}

func (fake *impactRelationshipFake) Snapshot(
	_ context.Context,
	query RelationshipQuery,
	pageSize int,
) (*RelationshipSnapshotPage, error) {
	fake.snapshotCalls++
	if pageSize != workbenchServicePageSize {
		return nil, fmt.Errorf("snapshot page size = %d", pageSize)
	}
	root := RelationshipRootReceipt{
		Repository: query.Repositories[0], State: "complete",
		Generation:      "sha256:" + strings.Repeat("7", 64),
		RootDigest:      "sha256:" + strings.Repeat("8", 64),
		AuthorityDigest: "sha256:" + strings.Repeat("6", 64),
		ServiceKey:      query.ServiceKey, ServiceIncarnation: 3,
		ServiceGeneration: "sha256:" + strings.Repeat("3", 64),
	}
	row := RelationshipSnapshotRow{
		Repository: query.Repositories[0], ServiceKey: query.ServiceKey,
		ServiceIncarnation: 3, ServiceGeneration: root.ServiceGeneration,
		Kind: "rpc", Plane: "grpc", Class: "resolved",
		LookupKey: query.LookupKey, Participation: []string{"source"},
		CounterpartServices: []string{"checkout"},
		ProjectionDigest:    "sha256:" + strings.Repeat("2", 64),
		PostingDigest:       "sha256:" + strings.Repeat("1", 64),
		Source: RelationshipPlacement{Path: "service/client.go", Claims: []RelationshipServiceClaim{{
			ServiceKey: query.ServiceKey, Disposition: "accepted",
			Roles: []RelationshipRoleClaim{{Role: "primary", Origin: "base"}},
		}}},
		Evidence: RelationshipEvidence{
			Kind: "rpc", Plane: "grpc", Class: "resolved",
			Path: "service/client.go", ObjectID: strings.Repeat("a", 40),
			ContentDigest: "sha256:" + strings.Repeat("a", 64),
			Span:          RelationshipSpan{StartByte: 1, EndByte: 2, StartLine: 7, EndLine: 7},
			Operation:     query.LookupKey, PostingDigest: "sha256:" + strings.Repeat("1", 64),
		},
	}
	return &RelationshipSnapshotPage{
		SchemaVersion: relationshipSnapshotSchema, Query: query,
		RowsState: "nonempty", Roots: []RelationshipRootReceipt{root},
		Rows: []RelationshipSnapshotRow{row},
		Coverage: RelationshipCoverage{
			AuthorizedRepositories: 1, CompleteRoots: 1,
			ScannedReferences: 1, ReturnedRows: 1,
		},
		Caveat: relationshipCaveat,
	}, nil
}

func (pack *impactResourcePack) ReadResourcePlane(
	_ context.Context,
	_ ResourcePlaneContext,
) (ResourcePlanePackSnapshot, error) {
	pack.calls++
	if pack.err != nil {
		return ResourcePlanePackSnapshot{}, pack.err
	}
	relationships := pack.relationships
	if relationships == nil {
		relationships = []ResourcePlaneRelationship{{
			Kind: "uses", Subject: "service:a", Object: "resource:a",
			Classification: "resolved",
			Sources: []ContractCatalogSource{
				impactResourceSource("github.com/acme/contracts", "resource"),
			},
		}}
	}
	return ResourcePlanePackSnapshot{
		State:         pack.state,
		Reason:        "fixture",
		Relationships: relationships,
	}, nil
}

func impactResourceSource(
	repository, suffix string,
) ContractCatalogSource {
	return ContractCatalogSource{
		Repository:  repository,
		Commit:      impactCommit,
		Path:        "src/" + suffix + ".go",
		StartByte:   1,
		EndByte:     2,
		StartLine:   1,
		EndLine:     1,
		AssertionID: "assertion-" + suffix,
		RunID:       "run-" + suffix,
		AtomID:      "atom-" + suffix,
	}
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

func TestWorkbenchImpactUsesSharedExactCallerServices(t *testing.T) {
	_, exactCallers, _ := newExactAuthorityGapServices(
		t, "github.com/acme/workbench-impact-secret",
	)
	wantSecret := [32]byte{1, 2, 3, 4}
	exactCallers.exact.secret = wantSecret
	workbench := &workbenchAPIFake{}
	opts := exactCallers.exact.opts
	opts.Workbench = workbench
	opts.ContractCatalog = &ContractCatalogService{}
	opts.CallerMap = exactCallers
	opts.CallerComparison = nil
	opts.FieldReferences = &FieldReferenceService{}
	service := NewWorkbenchImpactService(opts)
	if service == nil {
		t.Fatal("Workbench Impact did not construct with the exact Caller Map")
	}
	exactComparison, ok := service.comparison.(*CallerComparisonService)
	if !ok || service.callers != exactCallers ||
		exactComparison.exact != exactCallers.exact {
		t.Fatalf(
			"Workbench Impact readers = callers %p comparison %T, want shared exact caller service",
			service.callers, service.comparison,
		)
	}
	if service.cursorSecret != wantSecret ||
		service.cursorSecret == ([32]byte{}) {
		t.Fatalf(
			"Workbench Impact HMAC secret = %x, want known nonzero shared secret",
			service.cursorSecret,
		)
	}
	if !slices.Contains(
		apiCapabilities(Options{CallerComparison: exactComparison}),
		callerComparisonCapability,
	) {
		t.Fatal("explicit public comparison was absent from version capabilities")
	}
	legacy := opts
	legacy.CallerMap = &CallerMapService{}
	if NewWorkbenchImpactService(legacy) != nil {
		t.Fatal("Workbench Impact admitted the legacy Caller Map")
	}
	separate := opts
	separate.CallerComparison = &CallerComparisonService{
		exact: &exactCallerMapService{},
	}
	if NewWorkbenchImpactService(separate) != nil {
		t.Fatal("Workbench Impact admitted a separately keyed comparison engine")
	}
}

func TestWorkbenchImpactComposesExactServiceSnapshotsWithoutRetainedBindings(
	t *testing.T,
) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	replacement := impactSelection(store.ChangeBriefReplacement, "replacement")
	service, _, _, _, _ := impactService(
		impactView(store.ChangeBriefMigrate, []store.ChangeBriefContractSelection{
			current, replacement,
		}),
		nil,
		nil,
	)
	relationships := &impactRelationshipFake{}
	service.opts.Relationships = relationships
	service.relationships = relationships
	page, err := service.Read(
		t.Context(),
		"user:t217",
		WorkbenchImpactRequest{
			InvestigationID: impactInvestigationID,
			RevisionID:      impactRevisionID,
			Filters: WorkbenchImpactFilters{
				ServiceRepository: "github.com/acme/current",
				SourceService:     "cart-v1",
				TargetService:     "cart-v2",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.ServiceImpact == nil || page.ServiceImpact.Source == nil ||
		page.ServiceImpact.Target == nil ||
		page.ServiceImpact.Source.Snapshot.Query.ServiceKey != "cart-v1" ||
		page.ServiceImpact.Target.Snapshot.Query.ServiceKey != "cart-v2" ||
		page.ServiceImpact.Source.Snapshot.Query.LookupKey !=
			current.CanonicalOperation ||
		page.ServiceImpact.Target.Snapshot.Query.LookupKey !=
			replacement.CanonicalOperation ||
		page.ServiceImpact.Authority.Digest == "" ||
		relationships.snapshotCalls != 2 ||
		relationships.rootCoverageCalls != 3 {
		t.Fatalf(
			"service impact = %+v snapshots=%d coverage=%d",
			page.ServiceImpact,
			relationships.snapshotCalls,
			relationships.rootCoverageCalls,
		)
	}
	if !strings.Contains(page.Caveat, "no implicit write") {
		t.Fatalf("service-aware caveat = %q", page.Caveat)
	}
	suggestions := rawSuggestionsFromImpact(*page)
	if !slices.ContainsFunc(suggestions, func(value workbenchChecklistRawSuggestion) bool {
		return value.kind == "review_affected_service" &&
			strings.HasPrefix(value.selectionRule, "t38.3:") &&
			len(value.evidence) == 1 &&
			value.evidence[0].Plane == "service_relationship"
	}) {
		t.Fatalf("service-aware checklist suggestions = %+v", suggestions)
	}
}

func TestWorkbenchImpactRejectsServiceRootChangeAtFinalFence(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	service, _, _, _, _ := impactService(
		impactView(store.ChangeBriefModify, []store.ChangeBriefContractSelection{current}),
		nil,
		nil,
	)
	relationships := &impactRelationshipFake{changeAfter: 3}
	service.opts.Relationships = relationships
	service.relationships = relationships
	page, err := service.Read(
		t.Context(),
		"user:t217",
		WorkbenchImpactRequest{
			InvestigationID: impactInvestigationID,
			RevisionID:      impactRevisionID,
			Filters: WorkbenchImpactFilters{
				ServiceRepository: "github.com/acme/current",
				SourceService:     "cart-v1",
			},
		},
	)
	if page != nil || err == nil || !strings.Contains(
		err.Error(), "authority changed",
	) {
		t.Fatalf("changed service authority = %+v, %v", page, err)
	}
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

func TestWorkbenchImpactKeepsCallerGenerationGapsTyped(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	service, _, callers, _, _ := impactService(
		impactView(
			store.ChangeBriefRetire,
			[]store.ChangeBriefContractSelection{current},
		),
		nil,
		nil,
	)
	callers.state = string(callerexecute.PublicationStale)
	page, err := service.Read(
		context.Background(),
		"user:t217",
		WorkbenchImpactRequest{
			InvestigationID: impactInvestigationID,
			RevisionID:      impactRevisionID,
			PageSize:        10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Callers) != 1 ||
		page.Callers[0].MatchingRowsState != "unavailable" ||
		page.Callers[0].Generation.State !=
			string(callerexecute.PublicationStale) ||
		page.Callers[0].TotalMatchingRows != nil ||
		page.Callers[0].Declaration != nil ||
		len(page.Callers[0].ResolvedCallers) != 0 ||
		len(page.Callers[0].ExtractorAbstentions) != 0 {
		t.Fatalf("caller gap was promoted to evidence: %+v", page.Callers)
	}
	for _, coverage := range page.AnalysisScope.Coverage {
		if coverage.Capability == "contract-caller-map" {
			t.Fatalf("repository overlay became focused coverage: %+v", coverage)
		}
	}
	wantGap := false
	for _, gap := range page.AnalysisScope.Gaps {
		if gap.Capability == "contract-caller-map" &&
			gap.State == string(callerexecute.PublicationStale) &&
			gap.Code == "caller_generation_stale" {
			wantGap = true
		}
	}
	if !wantGap {
		t.Fatalf("typed caller gap absent: %+v", page.AnalysisScope)
	}
}

func TestWorkbenchImpactKeepsComparisonGapJointAndUnclassified(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	replacement := impactSelection(store.ChangeBriefReplacement, "replacement")
	service, _, _, comparison, _ := impactService(
		impactView(
			store.ChangeBriefMigrate,
			[]store.ChangeBriefContractSelection{current, replacement},
		),
		nil,
		nil,
	)
	comparison.state = string(callerexecute.PublicationMissing)
	page, err := service.Read(
		context.Background(),
		"user:t217",
		WorkbenchImpactRequest{
			InvestigationID: impactInvestigationID,
			RevisionID:      impactRevisionID,
			PageSize:        10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Comparison == nil ||
		page.Comparison.MatchingRowsState != "unavailable" ||
		page.Comparison.TotalRows != nil || len(page.Comparison.Rows) != 0 ||
		!page.Comparison.Pagination.Complete ||
		page.Comparison.Pagination.NextCursor != "" {
		t.Fatalf("comparison gap became a classification: %+v", page.Comparison)
	}
	gapCount := 0
	for _, gap := range page.AnalysisScope.Gaps {
		if gap.Capability == "contract-caller-comparison" &&
			gap.Code == "caller_generation_missing" {
			gapCount++
		}
	}
	if gapCount != 2 {
		t.Fatalf("comparison side gaps = %d, want 2: %+v", gapCount, page.AnalysisScope)
	}
}

func TestWorkbenchImpactFinalRevisionFence(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	newService := func() (*WorkbenchImpactService, *impactWorkbenchFake) {
		service, _, _, _, _ := impactService(
			impactView(
				store.ChangeBriefRetire,
				[]store.ChangeBriefContractSelection{current},
			),
			nil,
			nil,
		)
		return service, service.workbench.(*impactWorkbenchFake)
	}
	request := WorkbenchImpactRequest{
		InvestigationID: impactInvestigationID,
		RevisionID:      impactRevisionID,
		PageSize:        10,
	}
	t.Run("access revoked", func(t *testing.T) {
		service, workbench := newService()
		workbench.readHook = func(
			call int,
			view *store.WorkbenchView,
		) (*store.WorkbenchView, error) {
			if call == 2 {
				return nil, store.ErrNotFound
			}
			return view, nil
		}
		page, err := service.Read(
			context.Background(), "user:t217", request,
		)
		if page != nil || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("revoked final authority = page:%+v err:%v", page, err)
		}
	})
	t.Run("same id content changed", func(t *testing.T) {
		service, workbench := newService()
		workbench.readHook = func(
			call int,
			view *store.WorkbenchView,
		) (*store.WorkbenchView, error) {
			if call == 2 {
				view.Brief.ContentDigest = "sha256:" + strings.Repeat("f", 64)
			}
			return view, nil
		}
		page, err := service.Read(
			context.Background(), "user:t217", request,
		)
		var statusError interface{ GetStatus() int }
		if page != nil || !errors.As(err, &statusError) ||
			statusError.GetStatus() != http.StatusConflict {
			t.Fatalf("changed final authority = page:%+v err:%v", page, err)
		}
	})
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
		[]string{"", "caller-2"},
	) || !reflect.DeepEqual(
		fields.calls,
		[]string{"", "field-2", "field-3"},
	) || !reflect.DeepEqual(
		callers.confirmCalls,
		[]string{"caller-authority"},
	) {
		t.Fatalf(
			"delegate cursors caller=%v confirms=%v fields=%v",
			callers.calls,
			callers.confirmCalls,
			fields.calls,
		)
	}
}

func TestWorkbenchImpactCursorUsesNormalizedFilterIdentity(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	service, _, callers, _, _ := impactService(
		impactView(
			store.ChangeBriefModify,
			[]store.ChangeBriefContractSelection{current},
		),
		nil,
		nil,
	)
	callers.pages = 2
	request := WorkbenchImpactRequest{
		InvestigationID: impactInvestigationID,
		RevisionID:      impactRevisionID,
		PageSize:        1,
	}
	first, err := service.Read(
		context.Background(), "user:t217", request,
	)
	if err != nil || first.Pagination.Complete ||
		first.Pagination.NextCursor == "" {
		t.Fatalf("first page = %+v err=%v", first, err)
	}
	request.Cursor = first.Pagination.NextCursor
	request.Filters = WorkbenchImpactFilters{
		Freshness:  "any",
		Resolution: "any",
		Ordering:   "source",
		Level:      "occurrence",
	}
	second, err := service.Read(
		context.Background(), "user:t217", request,
	)
	if err != nil || !second.Pagination.Complete ||
		len(second.Callers) != 1 ||
		second.Callers[0].Query.Freshness != "any" ||
		second.Callers[0].Query.Resolution != "any" ||
		second.Callers[0].Query.Ordering != "source" ||
		!reflect.DeepEqual(callers.calls, []string{"", "caller-2"}) {
		t.Fatalf(
			"normalized continuation = page:%+v err:%v calls:%v",
			second, err, callers.calls,
		)
	}
}

func TestWorkbenchImpactRejectsCompletedCallerTransition(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	field := compat.FieldIdentity{
		Lineage: current.DeclarationLineage,
		Message: "shop.Cart",
		Number:  1,
	}
	service, _, callers, _, fields := impactService(
		impactView(
			store.ChangeBriefModify,
			[]store.ChangeBriefContractSelection{current},
		),
		impactCompatibility([]compat.FieldIdentity{field}),
		nil,
	)
	fields.pages = 2
	request := WorkbenchImpactRequest{
		InvestigationID:  impactInvestigationID,
		RevisionID:       impactRevisionID,
		CompatibilityRun: impactRunID,
		PageSize:         1,
	}
	first, err := service.Read(
		context.Background(), "user:t217", request,
	)
	if err != nil || first.Pagination.Complete ||
		first.Pagination.NextCursor == "" {
		t.Fatalf("first page = %+v err=%v", first, err)
	}
	callers.snapshot = "transitioned-caller-snapshot"
	request.Cursor = first.Pagination.NextCursor
	page, err := service.Read(
		context.Background(), "user:t217", request,
	)
	var statusError interface{ GetStatus() int }
	if page != nil || !errors.As(err, &statusError) ||
		statusError.GetStatus() != http.StatusConflict ||
		len(callers.calls) != 1 || len(callers.confirmCalls) != 1 {
		t.Fatalf(
			"transition = page:%+v err:%v list:%v confirm:%v",
			page, err, callers.calls, callers.confirmCalls,
		)
	}
}

func TestWorkbenchImpactRejectsForgedEarlyStreamCompletion(t *testing.T) {
	current := impactSelection(store.ChangeBriefCurrent, "current")
	service, _, callers, _, _ := impactService(
		impactView(
			store.ChangeBriefModify,
			[]store.ChangeBriefContractSelection{current},
		),
		nil,
		nil,
	)
	callers.pages = 2
	request := WorkbenchImpactRequest{
		InvestigationID: impactInvestigationID,
		RevisionID:      impactRevisionID,
		PageSize:        1,
	}
	first, err := service.Read(
		context.Background(), "user:t217", request,
	)
	if err != nil || first.Pagination.Complete ||
		first.Pagination.NextCursor == "" {
		t.Fatalf("first page = %+v err=%v", first, err)
	}
	payload, signature, ok := strings.Cut(
		first.Pagination.NextCursor, ".",
	)
	if !ok {
		t.Fatal("signed Workbench cursor has no signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var forged workbenchImpactCursor
	if err := json.Unmarshal(raw, &forged); err != nil {
		t.Fatal(err)
	}
	key := workbenchCallerStreamKey(current)
	stream := forged.Streams[key]
	if stream.Complete || stream.Next == "" {
		t.Fatalf("first caller stream = %+v", stream)
	}
	stream.Complete = true
	stream.Next = ""
	forged.Streams[key] = stream
	forgedRaw, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	request.Cursor = base64.RawURLEncoding.EncodeToString(forgedRaw) +
		"." + signature
	page, err := service.Read(
		context.Background(), "user:t217", request,
	)
	var statusError interface{ GetStatus() int }
	if page != nil || !errors.As(err, &statusError) ||
		statusError.GetStatus() != http.StatusBadRequest ||
		len(callers.calls) != 1 {
		t.Fatalf(
			"forged completion = page:%+v err:%v caller calls:%v",
			page, err, callers.calls,
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

func TestWorkbenchImpactResourcePlanesAreBoundedAndVisibilityScoped(
	t *testing.T,
) {
	visibleRelationship := ResourcePlaneRelationship{
		Kind: "uses", Subject: "service:visible", Object: "topic:orders",
		Classification: "resolved",
		Sources: []ContractCatalogSource{
			impactResourceSource("github.com/acme/visible", "visible"),
		},
	}
	hiddenRelationship := ResourcePlaneRelationship{
		Kind: "uses", Subject: "service:hidden", Object: "topic:secret",
		Classification: "resolved",
		Sources: []ContractCatalogSource{
			impactResourceSource("github.com/acme/hidden", "hidden"),
		},
	}
	pack := &impactResourcePack{
		state: ResourcePlaneEnabled,
		relationships: []ResourcePlaneRelationship{
			visibleRelationship,
			hiddenRelationship,
		},
	}
	registry, err := NewResourcePlaneRegistry(
		[]ResourcePlaneRegistration{{
			ID: "bounded", Label: "Bounded",
			State: ResourcePlaneEnabled, Pack: pack,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	service, _, _, _, _ := impactService(
		impactView(
			store.ChangeBriefAdd,
			[]store.ChangeBriefContractSelection{
				impactSelection(store.ChangeBriefAnalogous, "analogous"),
			},
		),
		nil,
		registry,
	)
	service.opts.Visible = func(context.Context) func(store.Repo) bool {
		return func(repository store.Repo) bool {
			return repository.Name != "github.com/acme/hidden"
		}
	}
	read := func() (*WorkbenchImpactPage, []byte) {
		t.Helper()
		page, err := service.Read(
			context.Background(),
			"user:t217",
			WorkbenchImpactRequest{
				InvestigationID: impactInvestigationID,
				RevisionID:      impactRevisionID,
				PageSize:        10,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		return page, encoded
	}
	withHidden, hiddenBytes := read()
	if len(withHidden.ResourcePlanes) != 1 ||
		len(withHidden.ResourcePlanes[0].Relationships) != 1 ||
		withHidden.ResourcePlanes[0].Relationships[0].Subject !=
			visibleRelationship.Subject {
		t.Fatalf(
			"hidden relationship was not filtered: %+v",
			withHidden.ResourcePlanes,
		)
	}
	pack.relationships = []ResourcePlaneRelationship{visibleRelationship}
	_, baselineBytes := read()
	if string(hiddenBytes) != string(baselineBytes) {
		t.Fatalf(
			"hidden resource relationship changed page bytes:\nwith=%s\nwithout=%s",
			hiddenBytes,
			baselineBytes,
		)
	}

	tooMany := make(
		[]ResourcePlaneRelationship,
		resourcePlaneMaxRelationships+1,
	)
	for index := range tooMany {
		tooMany[index] = visibleRelationship
		tooMany[index].Subject = fmt.Sprintf("service:%03d", index)
	}
	pack.relationships = tooMany
	bounded, _ := read()
	if bounded.ResourcePlanes[0].State != ResourcePlaneFailed ||
		bounded.ResourcePlanes[0].Reason !=
			"resource_plane_relationships_invalid" ||
		len(bounded.ResourcePlanes[0].Relationships) != 0 {
		t.Fatalf(
			"oversized resource plane was not failed closed: %+v",
			bounded.ResourcePlanes[0],
		)
	}
}

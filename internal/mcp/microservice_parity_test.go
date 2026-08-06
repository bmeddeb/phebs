package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

type workbenchImpactToolFixture struct {
	page      *api.WorkbenchImpactPage
	err       error
	principal string
	request   api.WorkbenchImpactRequest
	calls     int
	started   chan struct{}
}

func (fixture *workbenchImpactToolFixture) Read(
	ctx context.Context,
	principal string,
	request api.WorkbenchImpactRequest,
) (*api.WorkbenchImpactPage, error) {
	fixture.calls++
	fixture.principal = principal
	fixture.request = request
	if fixture.started != nil {
		close(fixture.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if fixture.err != nil {
		return nil, fixture.err
	}
	return fixture.page, nil
}

func TestMicroserviceMCPImpactUsesExactSharedContract(t *testing.T) {
	for _, pair := range []struct {
		name   string
		shared reflect.Type
		mcp    reflect.Type
	}{
		{"request", reflect.TypeOf(api.WorkbenchImpactRequest{}), reflect.TypeOf(workbenchImpactInput{})},
		{"filters", reflect.TypeOf(api.WorkbenchImpactFilters{}), reflect.TypeOf(workbenchImpactFiltersInput{})},
	} {
		shared, mcp := directJSONFields(pair.shared), directJSONFields(pair.mcp)
		if !slices.Equal(shared, mcp) {
			t.Fatalf("%s transport fields = %v, shared fields = %v", pair.name, mcp, shared)
		}
	}
	workbench, checklist := workbenchToolFixture()
	page := microserviceImpactPage()
	impact := &workbenchImpactToolFixture{page: &page}
	server := NewServer(Options{
		Version: "test", Workbench: workbench,
		WorkbenchChecklist: checklist, WorkbenchImpact: impact,
		Principal: func(context.Context) string { return "user:owner" },
	})
	arguments := map[string]any{
		"investigation_id":     "inv_workbench",
		"revision_id":          "irev_workbench",
		"compatibility_run_id": "run_exact",
		"filters": map[string]any{
			"unit": "checkout", "owner": "payments",
			"path_prefix": "services/checkout", "code_role": "caller",
			"tier": "1", "freshness": "current", "resolution": "resolved",
			"ordering": "target", "level": "group",
			"service_repository": "example.com/acme/mono",
			"source_service":     "checkout", "target_service": "payments",
		},
		"page_size": 23,
		"cursor":    "next-page",
	}
	got, result := callTool[api.WorkbenchImpactPage](
		t, server, "get_change_workbench_impact", arguments,
	)
	if result.IsError || !slices.Equal(got.ScenarioEmphasis, page.ScenarioEmphasis) ||
		got.ServiceImpact == nil || got.ServiceImpact.Authority.Digest != page.ServiceImpact.Authority.Digest {
		t.Fatalf("impact result = %+v, error=%t", got, result.IsError)
	}
	if impact.calls != 1 || impact.principal != "user:owner" {
		t.Fatalf("shared calls = %d principal=%q", impact.calls, impact.principal)
	}
	wantRequest := api.WorkbenchImpactRequest{
		InvestigationID: "inv_workbench", RevisionID: "irev_workbench",
		CompatibilityRun: "run_exact", PageSize: 23, Cursor: "next-page",
		Filters: api.WorkbenchImpactFilters{
			Unit: "checkout", Owner: "payments", PathPrefix: "services/checkout",
			CodeRole: "caller", Tier: "1", Freshness: "current",
			Resolution: "resolved", Ordering: "target", Level: "group",
			ServiceRepository: "example.com/acme/mono",
			SourceService:     "checkout", TargetService: "payments",
		},
	}
	if impact.request != wantRequest {
		t.Fatalf("shared request = %+v, want %+v", impact.request, wantRequest)
	}
	_, omittedResult := callTool[api.WorkbenchImpactPage](
		t, server, "get_change_workbench_impact", map[string]any{
			"investigation_id": "inv_workbench",
			"revision_id":      "irev_workbench",
		},
	)
	if omittedResult.IsError || impact.calls != 2 ||
		impact.request.Filters != (api.WorkbenchImpactFilters{}) {
		t.Fatalf("optional filters result=%+v calls=%d request=%+v", omittedResult, impact.calls, impact.request)
	}

	tool := listServiceDirectoryTools(t, server)["get_change_workbench_impact"]
	if tool == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint ||
		tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Fatalf("impact annotations = %+v", tool)
	}
	description := strings.ToLower(tool.Description)
	for _, boundary := range []string{"evidence", "no write", "decision", "completeness", "safety"} {
		if !strings.Contains(description, boundary) {
			t.Errorf("impact description omitted %q: %q", boundary, tool.Description)
		}
	}
}

func TestMicroserviceMCPToolsAreStrictBoundedAndCapabilityGated(t *testing.T) {
	workbench, checklist := workbenchToolFixture()
	impact := &workbenchImpactToolFixture{}
	principal := func(context.Context) string { return "user:owner" }
	tests := []struct {
		name    string
		opts    Options
		present bool
	}{
		{name: "dark", opts: Options{Version: "test"}},
		{name: "impact only", opts: Options{Version: "test", WorkbenchImpact: impact, Principal: principal}},
		{name: "missing impact", opts: Options{Version: "test", Workbench: workbench, WorkbenchChecklist: checklist, Principal: principal}},
		{name: "complete", present: true, opts: Options{Version: "test", Workbench: workbench, WorkbenchChecklist: checklist, WorkbenchImpact: impact, Principal: principal}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			present := listServiceDirectoryTools(t, NewServer(test.opts))["get_change_workbench_impact"] != nil
			if present != test.present {
				t.Fatalf("impact presence = %t, want %t", present, test.present)
			}
		})
	}

	t.Run("unknown nested input", func(t *testing.T) {
		page := microserviceImpactPage()
		strict := &workbenchImpactToolFixture{page: &page}
		_, result := callTool[api.WorkbenchImpactPage](t, NewServer(Options{
			Version: "test", Workbench: workbench,
			WorkbenchChecklist: checklist, WorkbenchImpact: strict,
			Principal: principal,
		}), "get_change_workbench_impact", map[string]any{
			"investigation_id": "inv", "revision_id": "rev",
			"filters": map[string]any{"future_authority": true},
		})
		if !result.IsError || strict.calls != 0 {
			t.Fatalf("hostile input result=%+v calls=%d", result, strict.calls)
		}
	})

	t.Run("encoded output ceiling", func(t *testing.T) {
		page := microserviceImpactPage()
		page.Caveat = strings.Repeat("x", api.WorkbenchImpactResponseByteLimit)
		bounded := &workbenchImpactToolFixture{page: &page}
		_, result := callTool[api.WorkbenchImpactPage](t, NewServer(Options{
			Version: "test", Workbench: workbench,
			WorkbenchChecklist: checklist, WorkbenchImpact: bounded,
			Principal: principal,
		}), "get_change_workbench_impact", map[string]any{
			"investigation_id": "inv", "revision_id": "rev",
			"filters": map[string]any{},
		})
		if !result.IsError || bounded.calls != 1 ||
			!strings.Contains(textContent(t, result), "8 MiB") {
			t.Fatalf("oversized result=%+v calls=%d", result, bounded.calls)
		}
	})

	t.Run("authorization non-disclosure", func(t *testing.T) {
		refused := &workbenchImpactToolFixture{err: store.ErrNotFound}
		_, hidden := callTool[api.WorkbenchImpactPage](t, NewServer(Options{
			Version: "test", Workbench: workbench,
			WorkbenchChecklist: checklist, WorkbenchImpact: refused,
			Principal: principal,
		}), "get_change_workbench_impact", map[string]any{
			"investigation_id": "hidden", "revision_id": "rev", "filters": map[string]any{},
		})
		_, unnamed := callTool[api.WorkbenchImpactPage](t, NewServer(Options{
			Version: "test", Workbench: workbench,
			WorkbenchChecklist: checklist, WorkbenchImpact: refused,
			Principal: func(context.Context) string { return "" },
		}), "get_change_workbench_impact", map[string]any{
			"investigation_id": "hidden", "revision_id": "rev", "filters": map[string]any{},
		})
		if !hidden.IsError || !unnamed.IsError || textContent(t, hidden) != textContent(t, unnamed) ||
			!strings.Contains(textContent(t, hidden), "workbench resource not found") {
			t.Fatalf("non-disclosure differs: %q / %q", textContent(t, hidden), textContent(t, unnamed))
		}
	})
}

func TestMicroserviceMCPImpactCancellationReachesSharedService(t *testing.T) {
	workbench, checklist := workbenchToolFixture()
	impact := &workbenchImpactToolFixture{started: make(chan struct{})}
	server := NewServer(Options{
		Version: "test", Workbench: workbench,
		WorkbenchChecklist: checklist, WorkbenchImpact: impact,
		Principal: func(context.Context) string { return "user:owner" },
	})
	session := connectWorkbenchToolSession(t, server)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &sdk.CallToolParams{
			Name: "get_change_workbench_impact",
			Arguments: map[string]any{
				"investigation_id": "inv", "revision_id": "rev", "filters": map[string]any{},
			},
		})
		done <- err
	}()
	select {
	case <-impact.started:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("shared impact read did not start")
	}
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled MCP call error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled MCP call did not return")
	}
}

func TestMicroserviceMCPToolCountAndSchemaDigests(t *testing.T) {
	workbench, checklist := workbenchToolFixture()
	page := microserviceImpactPage()
	server := NewServer(Options{
		Version: "test", ServiceDirectory: &serviceDirectoryToolFixture{},
		Relationships: &relationshipToolFixture{}, Workbench: workbench,
		WorkbenchChecklist: checklist,
		WorkbenchImpact:    &workbenchImpactToolFixture{page: &page},
		Principal:          func(context.Context) string { return "user:owner" },
	})
	tools := listServiceDirectoryTools(t, server)
	if len(tools) != 18 {
		t.Fatalf("complete read-only MCP tool count = %d, want 18", len(tools))
	}
	wantDigests := map[string]string{
		"list_services":                      "sha256:f03165fd4ba8ecd56f34ceaee7ecf0ee40249cbcfc4e4423e65cbd5e5dcd5b0b",
		"get_service":                        "sha256:b8c6cd0c38baafc4eb1b9407d0ccd0f890329e79bb9ade9af3eb3e21069a64a1",
		"list_service_relationships":         "sha256:b83bdde30f6f4444d09cc82d73f0f29ce26a865bcac3fc78ff8ec53dc527e17e",
		"compare_service_relationships":      "sha256:32a183b263aaeba9ab1e1b2655056834e78d9bb4efce8330ce691990fa84994c",
		"read_service_relationship_citation": "sha256:b9afbea7a17cbc13aad7d443dd5fdd229c3769a81a157f3952aa1757fd284f7b",
		"get_change_workbench_impact":        "sha256:5efccf389c568004c4c04b19f75b33fd2e3feff71dcb5ec88dde6bef04fdbd0b",
	}
	for name, want := range wantDigests {
		tool := tools[name]
		if tool == nil {
			t.Fatalf("microservice tool %q is absent", name)
		}
		input, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		output, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(append(append(input, '\n'), output...))
		got := "sha256:" + hex.EncodeToString(digest[:])
		if got != want {
			t.Errorf("%s schema digest = %s, want %s", name, got, want)
		}
		if !strings.Contains(string(input), `"additionalProperties":false`) {
			t.Errorf("%s input is not strict: %s", name, input)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not read-only", name)
		}
	}
}

func microserviceImpactPage() api.WorkbenchImpactPage {
	return api.WorkbenchImpactPage{
		SchemaVersion:   "workbench-impact-inventory-v3",
		InvestigationID: "inv_workbench", RevisionID: "irev_workbench",
		TicketKind:       store.ChangeBriefModify,
		ScenarioEmphasis: []string{"dependency evidence"},
		Atlas:            []api.WorkbenchAtlasImpact{}, Callers: []api.WorkbenchCallerImpact{},
		ResourcePlanes: []api.ResourcePlaneSnapshot{},
		ServiceImpact: &api.WorkbenchServiceImpact{
			Authority: api.RelationshipProofCoverage{
				SchemaVersion: "phebs-service-relationship-coverage-v1",
				Digest:        "sha256:" + strings.Repeat("a", 64),
			},
			Caveat: "static evidence only",
		},
		AnalysisScope: api.WorkbenchAnalysisScope{
			Coverage:     []api.WorkbenchImpactCoverage{},
			Capabilities: []api.WorkbenchImpactCapability{},
			Gaps:         []api.WorkbenchImpactGap{{Capability: "service-relationships", State: "unavailable", Code: "root_gap"}},
		},
		Pagination: api.WorkbenchImpactPagination{Complete: true},
		Caveat:     "static evidence does not establish a decision",
	}
}

func directJSONFields(typ reflect.Type) []string {
	fields := make([]string, 0, typ.NumField())
	for index := range typ.NumField() {
		name := strings.Split(typ.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}

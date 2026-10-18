package mcp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

type operationImpactMCPStub struct {
	query  api.CallerMapQuery
	size   int
	cursor string
	calls  int
	report *api.OperationImpactReport
	err    error
}

func (stub *operationImpactMCPStub) Build(_ context.Context, shape string, query api.CallerMapQuery, size int, cursor string) (*api.OperationImpactReport, error) {
	stub.calls++
	stub.query, stub.size, stub.cursor = query, size, cursor
	if shape != "operation_callers" {
		return nil, errors.New("unsupported shape")
	}
	return stub.report, stub.err
}

func TestOperationImpactMCPRegistryAndParity(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "dark", true: "admitted"}[admitted], func(t *testing.T) {
			query := api.CallerMapQuery{Endpoint: api.CallerMapEndpoint{Protocol: "protobuf", Repository: "example.com/repo", Lineage: "decl", Operation: "/demo.Service/Get"}, Owner: "owner", Freshness: "any", Resolution: "any", Ordering: "source"}
			stub := &operationImpactMCPStub{report: &api.OperationImpactReport{
				SchemaVersion: api.OperationImpactSchemaVersion, Shape: "operation_callers",
				CallerPage:          &api.CallerMapPage{SchemaVersion: "caller-map-v2", Query: query, Rows: []api.CallerMapRow{}, Pagination: api.CallerMapPagination{Complete: true}, Generation: &api.CallerMapGeneration{State: "missing", Plane: "repository-overlay", Repository: query.Endpoint.Repository}, Scope: &api.AnalysisScopeProjection{Repository: query.Endpoint.Repository, ScopePosture: "whole-repository"}, MatchingRowsState: "unavailable", Caveat: "No absence claim."},
				Conclusion:          api.OperationImpactConclusion{State: "unavailable", Query: query, Qualification: "Caller evidence is unavailable."},
				UnavailableSections: api.OperationImpactUnavailableSections{FieldReferences: "not_in_supported_shape", ProposedChange: "not_in_supported_shape", Implementations: "not_in_supported_shape", WireVerdict: "not_in_supported_shape", MatchingSyntacticCalls: "not_in_supported_shape", SavedReport: "not_in_supported_shape"},
			}}
			opts := Options{Version: "test"}
			if admitted {
				opts.OperationImpact = stub
			}
			server := NewServer(opts)
			serverTransport, clientTransport := sdk.NewInMemoryTransports()
			go func() { _, _ = server.Connect(t.Context(), serverTransport, nil) }()
			client := sdk.NewClient(&sdk.Implementation{Name: "t542", Version: "1"}, nil)
			session, err := client.Connect(t.Context(), clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			listed, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var found *sdk.Tool
			for _, tool := range listed.Tools {
				if tool.Name == "build_operation_impact_report" {
					found = tool
				}
			}
			if (found != nil) != admitted {
				t.Fatalf("admitted=%v tool=%+v", admitted, found)
			}
			if !admitted {
				return
			}
			if found.Annotations == nil || !found.Annotations.ReadOnlyHint {
				t.Fatal("report tool not declared read-only")
			}
			args := map[string]any{"shape": "operation_callers", "protocol": "protobuf", "repository": query.Endpoint.Repository, "declaration_lineage": "decl", "operation": query.Endpoint.Operation, "owner": "owner", "freshness": "any", "resolution": "any", "ordering": "source", "page_size": 1, "cursor": "opaque"}
			report, result := callToolSession[api.OperationImpactReport](t, session, "build_operation_impact_report", args)
			if result.IsError || stub.calls != 1 || stub.query != query || stub.size != 1 || stub.cursor != "opaque" || !reflect.DeepEqual(report, *stub.report) {
				t.Fatalf("result=%+v report=%+v stub=%+v", result, report, stub)
			}
			args["field_number"] = 0
			result, err = session.CallTool(t.Context(), &sdk.CallToolParams{Name: "build_operation_impact_report", Arguments: args})
			if err == nil && !result.IsError || stub.calls != 1 {
				t.Fatalf("mixed input executed calls=%d result=%+v err=%v", stub.calls, result, err)
			}
		})
	}
}

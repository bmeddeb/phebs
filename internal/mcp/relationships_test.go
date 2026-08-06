package mcp

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
)

type relationshipToolFixture struct{ list, compare, citation int }

func (fixture *relationshipToolFixture) List(_ context.Context, query api.RelationshipQuery, _ int, _ string) (*api.RelationshipPage, error) {
	fixture.list++
	return &api.RelationshipPage{SchemaVersion: "phebs-service-relationship-page-v1", Query: query, Rows: []api.RelationshipRow{}, Roots: []api.RelationshipRootReceipt{}, RowsState: "empty"}, nil
}
func (fixture *relationshipToolFixture) Compare(_ context.Context, query api.RelationshipComparisonQuery, _ int, _ string) (*api.RelationshipComparisonPage, error) {
	fixture.compare++
	return &api.RelationshipComparisonPage{SchemaVersion: "phebs-service-relationship-comparison-v1", Query: query, Rows: []api.RelationshipComparisonRow{}, Roots: []api.RelationshipRootReceipt{}, RowsState: "empty"}, nil
}
func (fixture *relationshipToolFixture) ReadCitation(_ context.Context, _ string) (*api.RelationshipCitation, error) {
	fixture.citation++
	return &api.RelationshipCitation{SchemaVersion: "phebs-service-relationship-citation-v1", Content: "call()"}, nil
}

func TestRelationshipMCPToolsUseSharedProjectionAndStayDefaultDark(t *testing.T) {
	fixture := &relationshipToolFixture{}
	server := NewServer(Options{Version: "test", Relationships: fixture})
	page, result := callTool[api.RelationshipPage](t, server, "list_service_relationships", map[string]any{"service_key": "orders", "view": "dependencies"})
	if result.IsError || page.RowsState != "empty" || fixture.list != 1 {
		t.Fatalf("relationship list = %+v, result=%+v calls=%d", page, result, fixture.list)
	}
	comparison, result := callTool[api.RelationshipComparisonPage](t, server, "compare_service_relationships", map[string]any{"repository": "example.com/acme/mono", "service_key": "orders", "before_generation": "sha256:a", "before_root_digest": "sha256:b", "after_generation": "sha256:c", "after_root_digest": "sha256:d"})
	if result.IsError || comparison.RowsState != "empty" || fixture.compare != 1 {
		t.Fatalf("relationship comparison = %+v, result=%+v calls=%d", comparison, result, fixture.compare)
	}
	citation, result := callTool[api.RelationshipCitation](t, server, "read_service_relationship_citation", map[string]any{"citation": "opaque"})
	if result.IsError || citation.Content != "call()" || fixture.citation != 1 {
		t.Fatalf("relationship citation = %+v, result=%+v calls=%d", citation, result, fixture.citation)
	}
	tools := listServiceDirectoryTools(t, server)
	for _, name := range []string{"list_service_relationships", "compare_service_relationships", "read_service_relationship_citation"} {
		if tools[name] == nil {
			t.Errorf("relationship tool %q is absent", name)
		}
	}
	dark := listServiceDirectoryTools(t, NewServer(Options{Version: "test"}))
	for _, name := range []string{"list_service_relationships", "compare_service_relationships", "read_service_relationship_citation"} {
		if dark[name] != nil {
			t.Errorf("dark server exposed %q", name)
		}
	}
}

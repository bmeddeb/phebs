package mcp

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/compat"
)

// TestWireCompatibilityToolReturnsOnlyTheVerdict pins T53.2's MCP twin of the
// standalone HTTP verdict: no store or proof service, just the checker's result.
func TestWireCompatibilityToolReturnsOnlyTheVerdict(t *testing.T) {
	server := NewServer(Options{Version: "test", WireCompatibility: proofToolCompatibility{}})
	args := map[string]any{
		"lineage": "contract_wire_test",
		"before":  []map[string]any{{"path": "shop/cart.proto", "content": "syntax = \"proto3\";\n"}},
		"after":   []map[string]any{{"path": "shop/cart.proto", "content": "syntax = \"proto3\";\n"}},
	}
	got, result := callTool[compat.CompatibilityResult](t, server, "check_wire_compatibility", args)
	if result.IsError || got.Compatible || len(got.AffectedFields) != 1 ||
		got.AffectedFields[0].Lineage != "contract_wire_test" || got.Before.Digest == "" || got.After.Digest == "" {
		t.Fatalf("verdict = %+v (error %v)", got, result.IsError)
	}

	_, refused := callTool[compat.CompatibilityResult](t, server, "check_wire_compatibility", map[string]any{
		"lineage": "contract_wire_test", "before": []map[string]any{}, "after": []map[string]any{},
	})
	if !refused.IsError {
		t.Fatal("an invalid request produced a verdict")
	}
}

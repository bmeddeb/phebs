package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Read-only clients admit these tools from tools/list, before any invocation.
// Exercise the production stateless HTTP transport so an omitted false pointer
// or SDK serialization change cannot silently restore the destructive default.
func TestSearchReadToolsAdvertiseReadOnlyOverHTTP(t *testing.T) {
	server := NewServer(Options{Version: "test"})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true})
	peer := httptest.NewServer(handler)
	t.Cleanup(peer.Close)
	client := sdk.NewClient(&sdk.Implementation{Name: "readonly-discovery-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: peer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*sdk.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"search_code", "read_file"} {
		t.Run(name, func(t *testing.T) {
			tool := tools[name]
			if tool == nil {
				t.Fatal("tool absent from tools/list")
			}
			// Check the serialized contract a client actually consumes, including
			// explicit false instead of the protocol's destructive default true.
			raw, err := json.Marshal(tool)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Annotations map[string]any `json:"annotations"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Annotations["readOnlyHint"] != true || wire.Annotations["destructiveHint"] != false {
				t.Fatalf("read-only tool admission refused: annotations = %v", wire.Annotations)
			}
		})
	}
}

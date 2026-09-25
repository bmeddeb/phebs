package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestMicroserviceMCPToolCountAndSchemaDigests(t *testing.T) {
	server := NewServer(Options{
		Version: "test", ServiceDirectory: &serviceDirectoryToolFixture{},
		Relationships: &relationshipToolFixture{},
	})
	tools := listServiceDirectoryTools(t, server)
	if len(tools) != 15 {
		t.Fatalf("complete read-only MCP tool count = %d, want 15", len(tools))
	}
	for _, name := range []string{
		"preview_change_workbench", "create_change_workbench",
		"get_change_workbench", "record_change_disposition",
		"get_change_workbench_impact",
	} {
		if tools[name] != nil {
			t.Errorf("retired Workbench tool %q is registered", name)
		}
	}
	wantDigests := map[string]string{
		"list_services":                      "sha256:f03165fd4ba8ecd56f34ceaee7ecf0ee40249cbcfc4e4423e65cbd5e5dcd5b0b",
		"get_service":                        "sha256:b8c6cd0c38baafc4eb1b9407d0ccd0f890329e79bb9ade9af3eb3e21069a64a1",
		"list_service_relationships":         "sha256:5c619d34c964492bee4316549b08b84f2a5dcf5b77be62b09285ae63428c7955",
		"compare_service_relationships":      "sha256:3300e5bb2b8d359bc8c49003b2e1d6bf019447fc2bc4b0f9123458e3a7fb27a1",
		"read_service_relationship_citation": "sha256:9f632b48da15163c50ada60d85cf29fc3a772935eb651c6fc487a9680cd61d6f",
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

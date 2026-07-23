package investigation

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestInvestigationFixturesValidateAgainstGeneratedSchemas(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../../docs/fixtures/investigations/*.json")
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(files) != 8 {
		t.Fatalf("fixture count = %d, want 8", len(files))
	}
	envelopeSchema := loadResolvedSchema(t, "../../schemas/mcp-envelope-v1.0.json")
	payloadSchemas := map[string]*jsonschema.Resolved{
		"contract_edges": loadResolvedSchema(t, "../../schemas/mcp-payload-contract-edges-v1.0.json"),
		"snapshot_comparison": loadResolvedSchema(
			t,
			"../../schemas/mcp-payload-snapshot-comparison-v1.0.json",
		),
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var instance any
			if err := json.Unmarshal(content, &instance); err != nil {
				t.Fatalf("decode generic fixture: %v", err)
			}
			if err := envelopeSchema.Validate(instance); err != nil {
				t.Fatalf("envelope schema: %v", err)
			}
			object := instance.(map[string]any)
			if payload, ok := object["payload"].(map[string]any); ok {
				kind, _ := payload["kind"].(string)
				schema, ok := payloadSchemas[kind]
				if !ok {
					t.Fatalf("fixture payload kind %q has no conformance schema", kind)
				}
				if err := schema.Validate(payload); err != nil {
					t.Fatalf("%s payload schema: %v", kind, err)
				}
			}
			var envelope Envelope
			if err := json.Unmarshal(content, &envelope); err != nil {
				t.Fatalf("decode typed fixture: %v", err)
			}
			if err := envelope.ValidateSemantics(); err != nil {
				t.Fatalf("semantic validation: %v", err)
			}
		})
	}
}

func TestTruncatedResultIsIrreversibleAndAbsenceBlocking(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("../../docs/fixtures/investigations/08-truncated-result.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var valid Envelope
	if err := json.Unmarshal(content, &valid); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if err := valid.ValidateSemantics(); err != nil {
		t.Fatalf("fixture is invalid: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{
			name: "cannot become complete",
			mutate: func(envelope *Envelope) {
				envelope.ResultWindow.ResultSetComplete = true
			},
		},
		{
			name: "cannot offer continuation",
			mutate: func(envelope *Envelope) {
				token := "page-2"
				envelope.ResultWindow.NextPageToken = &token
			},
		},
		{
			name: "cannot support absence",
			mutate: func(envelope *Envelope) {
				envelope.AbsenceEligibility.BlockerCodes = []string{"PROCESSING_PARTIAL"}
			},
		},
		{
			name: "cannot omit qualification",
			mutate: func(envelope *Envelope) {
				envelope.AbsenceEligibility.Qualification = nil
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			window := *valid.ResultWindow
			candidate.ResultWindow = &window
			absence := *valid.AbsenceEligibility
			absence.BlockerCodes = append([]string(nil), valid.AbsenceEligibility.BlockerCodes...)
			candidate.AbsenceEligibility = &absence
			test.mutate(&candidate)
			if err := candidate.ValidateSemantics(); err == nil {
				t.Fatal("mutated truncated result unexpectedly validated")
			}
		})
	}
}

func TestCheckedInSchemasMatchGenerator(t *testing.T) {
	t.Parallel()
	documents := SchemaDocuments()
	entries, err := filepath.Glob("../../schemas/*.json")
	if err != nil {
		t.Fatalf("glob checked-in schemas: %v", err)
	}
	actualNames := make([]string, len(entries))
	for i, entry := range entries {
		actualNames[i] = filepath.Base(entry)
	}
	expectedNames := SchemaFilenames()
	slices.Sort(actualNames)
	slices.Sort(expectedNames)
	if !slices.Equal(actualNames, expectedNames) {
		t.Fatalf("checked-in schema set = %v, want %v", actualNames, expectedNames)
	}
	for _, name := range SchemaFilenames() {
		name := name
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(documents[name], "", "  ")
			if err != nil {
				t.Fatalf("marshal schema: %v", err)
			}
			got = append(got, '\n')
			want, err := os.ReadFile(filepath.Join("../../schemas", name))
			if err != nil {
				t.Fatalf("read checked-in schema: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s is stale; run go generate ./internal/mcp", name)
			}
			var schema jsonschema.Schema
			if err := json.Unmarshal(want, &schema); err != nil {
				t.Fatalf("decode generated schema: %v", err)
			}
			if _, err := schema.Resolve(nil); err != nil {
				t.Fatalf("resolve generated schema: %v", err)
			}
		})
	}
}

func loadResolvedSchema(t *testing.T, path string) *jsonschema.Resolved {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema %s: %v", path, err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decode schema %s: %v", path, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema %s: %v", path, err)
	}
	return resolved
}

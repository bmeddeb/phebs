package t211

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	frozenGlossaryDigest = "sha256:ce13b715607141f2833c8ade57b8aa552d89b68475c5884257972a7defcb3274"
	frozenScenarioDigest = "sha256:922034e9f9a3cb40ff0d602b27a0245795a45949b2e012c6f8d7f75f145120f4"
)

func TestFrozenContractsAreCanonicalAndDeterministic(t *testing.T) {
	glossaryRaw := readFixture(t, "glossary.json")
	scenarioRaw := readFixture(t, "scenarios.json")

	glossary, glossaryCanonical, glossaryDigest, err := LoadGlossary(glossaryRaw)
	if err != nil {
		t.Fatalf("load glossary: %v", err)
	}
	scenarios, scenarioCanonical, scenarioDigest, err := LoadScenarioContract(scenarioRaw)
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	if err := ValidateContractPair(glossary, scenarios); err != nil {
		t.Fatalf("validate contract pair: %v", err)
	}
	if glossaryDigest != frozenGlossaryDigest {
		t.Fatalf("glossary digest = %q, want %q", glossaryDigest, frozenGlossaryDigest)
	}
	if scenarioDigest != frozenScenarioDigest {
		t.Fatalf("scenario digest = %q, want %q", scenarioDigest, frozenScenarioDigest)
	}
	if !json.Valid(glossaryCanonical) || !json.Valid(scenarioCanonical) {
		t.Fatal("canonical contract bytes are not valid JSON")
	}

	permutedGlossary := glossary
	slices.Reverse(permutedGlossary.Capabilities)
	slices.Reverse(permutedGlossary.Terms)
	for index := range permutedGlossary.Terms {
		term := &permutedGlossary.Terms[index]
		slices.Reverse(term.Modes)
		slices.Reverse(term.Surfaces)
		slices.Reverse(term.WireAliases)
		slices.Reverse(term.Availability.RequiresAll)
		slices.Reverse(term.Availability.RequiresAny)
	}
	_, canonicalAgain, digestAgain, err := LoadGlossary(mustJSON(t, permutedGlossary))
	if err != nil {
		t.Fatalf("load permuted glossary: %v", err)
	}
	if !bytes.Equal(canonicalAgain, glossaryCanonical) || digestAgain != glossaryDigest {
		t.Fatal("semantically equal glossary did not produce equal canonical bytes and digest")
	}

	permutedScenarios := scenarios
	slices.Reverse(permutedScenarios.NeutralFixture.SourcePaths)
	slices.Reverse(permutedScenarios.Services)
	for index := range permutedScenarios.Services {
		slices.Reverse(permutedScenarios.Services[index].SourceAnchors)
	}
	slices.Reverse(permutedScenarios.Scenarios)
	for scenarioIndex := range permutedScenarios.Scenarios {
		scenario := &permutedScenarios.Scenarios[scenarioIndex]
		slices.Reverse(scenario.Steps)
		for stepIndex := range scenario.Steps {
			step := &scenario.Steps[stepIndex]
			for _, rows := range []*[]string{
				&step.Inputs, &step.ServiceCalls, &step.Outputs, &step.Mutations,
				&step.EvidenceSources, &step.Bounds, &step.UnsupportedPlanes,
				&step.HumanDecisions,
			} {
				slices.Reverse(*rows)
			}
		}
	}
	slices.Reverse(permutedScenarios.TerminologyTraces)
	slices.Reverse(permutedScenarios.TicketGates)
	for index := range permutedScenarios.TicketGates {
		slices.Reverse(permutedScenarios.TicketGates[index].RegistrationRequires)
	}
	_, canonicalAgain, digestAgain, err = LoadScenarioContract(mustJSON(t, permutedScenarios))
	if err != nil {
		t.Fatalf("load permuted scenarios: %v", err)
	}
	if !bytes.Equal(canonicalAgain, scenarioCanonical) || digestAgain != scenarioDigest {
		t.Fatal("semantically equal scenario contract did not produce equal canonical bytes and digest")
	}
}

func TestGlossaryLanguageProjections(t *testing.T) {
	glossary, _, glossaryDigest, err := LoadGlossary(readFixture(t, "glossary.json"))
	if err != nil {
		t.Fatalf("load glossary: %v", err)
	}

	goProjection, err := ProjectGo(glossary)
	if err != nil {
		t.Fatalf("project Go: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "glossary.go", goProjection, parser.AllErrors); err != nil {
		t.Fatalf("parse projected Go: %v", err)
	}
	for _, term := range glossary.Terms {
		if !bytes.Contains(goProjection, []byte(strconv.Quote(term.ID))) ||
			!bytes.Contains(goProjection, []byte(strconv.Quote(term.Label))) {
			t.Fatalf("Go projection omits term %s", term.ID)
		}
	}

	typeScriptProjection, err := ProjectTypeScript(glossary)
	if err != nil {
		t.Fatalf("project TypeScript: %v", err)
	}
	const typeScriptPrefix = "export const glossaryTerms = "
	start := bytes.Index(typeScriptProjection, []byte(typeScriptPrefix))
	end := bytes.LastIndex(typeScriptProjection, []byte(" as const satisfies readonly GlossaryTerm[]"))
	if start < 0 || end < 0 || end <= start {
		t.Fatal("TypeScript projection does not contain its generated JSON value")
	}
	var typeScriptTerms []map[string]any
	if err := json.Unmarshal(typeScriptProjection[start+len(typeScriptPrefix):end], &typeScriptTerms); err != nil {
		t.Fatalf("decode TypeScript projection value: %v", err)
	}
	if len(typeScriptTerms) != len(glossary.Terms) {
		t.Fatalf("TypeScript term count = %d, want %d", len(typeScriptTerms), len(glossary.Terms))
	}
	for index, term := range glossary.Terms {
		if typeScriptTerms[index]["id"] != term.ID ||
			typeScriptTerms[index]["label"] != term.Label ||
			typeScriptTerms[index]["shortHelp"] != term.ShortHelp {
			t.Fatalf("TypeScript projection term %d does not match %s", index, term.ID)
		}
	}

	manualProjection := ProjectManual(glossary, glossaryDigest)
	if !bytes.Contains(manualProjection, []byte(glossaryDigest)) {
		t.Fatal("MANUAL projection does not bind the glossary digest")
	}
	for _, term := range glossary.Terms {
		for _, required := range []string{
			"### " + term.Label,
			term.ShortHelp,
			term.ExpandedHelp,
			term.EvidenceBoundary,
			term.AuthorityBoundary,
			term.Availability.UnavailableHelp,
		} {
			if !bytes.Contains(manualProjection, []byte(required)) {
				t.Fatalf("MANUAL projection omits %s content %q", term.ID, required)
			}
		}
	}

	mcpProjection, err := ProjectMCP(glossary, glossaryDigest)
	if err != nil {
		t.Fatalf("project MCP: %v", err)
	}
	var mcp struct {
		SchemaVersion  string `json:"schema_version"`
		GlossaryDigest string `json:"glossary_digest"`
		Terms          []struct {
			ID           string              `json:"id"`
			Label        string              `json:"label"`
			Description  string              `json:"description"`
			Availability CapabilityPredicate `json:"availability"`
		} `json:"terms"`
	}
	if err := json.Unmarshal(mcpProjection, &mcp); err != nil {
		t.Fatalf("decode MCP projection: %v", err)
	}
	if mcp.SchemaVersion != "change-workbench-mcp-glossary-preview-v1" {
		t.Fatalf("MCP schema = %q", mcp.SchemaVersion)
	}
	if mcp.GlossaryDigest != glossaryDigest || len(mcp.Terms) != len(glossary.Terms) {
		t.Fatal("MCP projection does not bind the complete glossary")
	}
	for index, term := range glossary.Terms {
		projected := mcp.Terms[index]
		if projected.ID != term.ID || projected.Label != term.Label ||
			!strings.Contains(projected.Description, term.ShortHelp) ||
			!slices.Equal(projected.Availability.RequiresAll, term.Availability.RequiresAll) ||
			!slices.Equal(projected.Availability.RequiresAny, term.Availability.RequiresAny) {
			t.Fatalf("MCP projection term %d does not match %s", index, term.ID)
		}
	}

	goProjectionAgain, _ := ProjectGo(glossary)
	typeScriptAgain, _ := ProjectTypeScript(glossary)
	mcpAgain, _ := ProjectMCP(glossary, glossaryDigest)
	if !bytes.Equal(goProjection, goProjectionAgain) ||
		!bytes.Equal(typeScriptProjection, typeScriptAgain) ||
		!bytes.Equal(manualProjection, ProjectManual(glossary, glossaryDigest)) ||
		!bytes.Equal(mcpProjection, mcpAgain) {
		t.Fatal("one or more language projections are nondeterministic")
	}
}

func TestServiceInventoryAnchorsCurrentImplementation(t *testing.T) {
	scenarios, _, _, err := LoadScenarioContract(readFixture(t, "scenarios.json"))
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, service := range scenarios.Services {
		for _, anchor := range service.SourceAnchors {
			content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(anchor.Path)))
			if err != nil {
				t.Fatalf("read %s anchor %s: %v", service.ID, anchor.Path, err)
			}
			if !bytes.Contains(content, []byte(anchor.Needle)) {
				t.Errorf("%s anchor %s no longer contains %q", service.ID, anchor.Path, anchor.Needle)
			}
		}
	}
}

func TestScenarioGateMatrixAndTerminology(t *testing.T) {
	glossary, _, _, err := LoadGlossary(readFixture(t, "glossary.json"))
	if err != nil {
		t.Fatalf("load glossary: %v", err)
	}
	scenarios, _, _, err := LoadScenarioContract(readFixture(t, "scenarios.json"))
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	if err := ValidateContractPair(glossary, scenarios); err != nil {
		t.Fatalf("validate contract pair: %v", err)
	}

	dependencies := []string{
		"none", "none", "none", "none", "none", "T20.10", "T20.13",
		"T20.10", "T20.13", "T20.10", "T20.13", "none", "T20.13", "T20.14",
	}
	postures := []string{
		"docs_tests_only",
		"production_unregistered", "production_unregistered",
		"production_unregistered", "production_unregistered_dark",
		"production_unregistered", "production_unregistered",
		"production_unregistered", "production_unregistered",
		"production_unregistered_dark", "production_unregistered_dark",
		"production_unregistered", "production_unregistered_dark",
		"implementation_closure_only",
	}
	for index, gate := range scenarios.TicketGates {
		if gate.Epic20Dependency != dependencies[index] {
			t.Errorf("%s Epic 20 dependency = %q, want %q",
				gate.Ticket, gate.Epic20Dependency, dependencies[index])
		}
		if gate.LandingPosture != postures[index] {
			t.Errorf("%s landing posture = %q, want %q",
				gate.Ticket, gate.LandingPosture, postures[index])
		}
	}

	for _, scenario := range scenarios.Scenarios {
		encoded := strings.ToLower(string(mustJSON(t, scenario)))
		if strings.Contains(encoded, "known consumer") {
			t.Errorf("%s story uses the legacy user-facing known-consumer term", scenario.ID)
		}
	}
}

func TestGlossaryRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	raw := readFixture(t, "glossary.json")
	valid, _, _, err := LoadGlossary(raw)
	if err != nil {
		t.Fatalf("load glossary: %v", err)
	}

	tests := []struct {
		name string
		raw  func() []byte
	}{
		{
			name: "unknown field",
			raw: func() []byte {
				return bytes.Replace(raw, []byte(`"schema_version":`),
					[]byte(`"unexpected": true, "schema_version":`), 1)
			},
		},
		{
			name: "duplicate term id",
			raw: func() []byte {
				value := cloneJSON(t, valid)
				value.Terms = append(value.Terms, value.Terms[0])
				return mustJSON(t, value)
			},
		},
		{
			name: "duplicate mode",
			raw: func() []byte {
				value := cloneJSON(t, valid)
				value.Terms[0].Modes = append(value.Terms[0].Modes, value.Terms[0].Modes[0])
				return mustJSON(t, value)
			},
		},
		{
			name: "unknown capability",
			raw: func() []byte {
				value := cloneJSON(t, valid)
				value.Terms[0].Availability.RequiresAll = []string{"unreviewed-capability"}
				return mustJSON(t, value)
			},
		},
		{
			name: "oversized help",
			raw: func() []byte {
				value := cloneJSON(t, valid)
				value.Terms[0].ShortHelp = strings.Repeat("x", maxTextBytes+1)
				return mustJSON(t, value)
			},
		},
		{
			name: "invalid UTF-8",
			raw: func() []byte {
				return bytes.Replace(raw, []byte(`"Analysis scope & gaps"`),
					[]byte{'"', 0xff, '"'}, 1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := LoadGlossary(test.raw()); err == nil {
				t.Fatal("LoadGlossary accepted invalid input")
			}
		})
	}
}

func TestScenarioContractRejectsUnsafeOrIncompleteInput(t *testing.T) {
	valid, _, _, err := LoadScenarioContract(readFixture(t, "scenarios.json"))
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*ScenarioContract)
	}{
		{
			name: "unknown service",
			mutate: func(value *ScenarioContract) {
				value.Scenarios[0].Steps[0].ServiceCalls[0] = "invented_service"
			},
		},
		{
			name: "missing mutation inventory",
			mutate: func(value *ScenarioContract) {
				value.Scenarios[0].Steps[0].Mutations = nil
			},
		},
		{
			name: "weakened registration gate",
			mutate: func(value *ScenarioContract) {
				value.TicketGates[0].RegistrationRequires = []string{"ESTABLISHED"}
			},
		},
		{
			name: "nonhex revision",
			mutate: func(value *ScenarioContract) {
				value.NeutralFixture.Revision = strings.Repeat("z", 40)
			},
		},
		{
			name: "duplicate source anchor",
			mutate: func(value *ScenarioContract) {
				value.Services[0].SourceAnchors = append(
					value.Services[0].SourceAnchors,
					value.Services[0].SourceAnchors[0],
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneJSON(t, valid)
			test.mutate(&value)
			if _, _, _, err := LoadScenarioContract(mustJSON(t, value)); err == nil {
				t.Fatal("LoadScenarioContract accepted invalid input")
			}
		})
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	if name == "glossary.json" {
		name = filepath.Join("..", "..", "internal", "glossary", "glossary.json")
	}
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return content
}

func cloneJSON[T any](t *testing.T, value T) T {
	t.Helper()
	var clone T
	if err := json.Unmarshal(mustJSON(t, value), &clone); err != nil {
		t.Fatalf("clone JSON value: %v", err)
	}
	return clone
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON fixture: %v", err)
	}
	return content
}

package glossary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"strconv"
	"strings"
	"unicode"
)

const (
	MCPProjectionSchemaVersion = "change-workbench-mcp-glossary-v1"
	ManualBeginMarker          = "<!-- BEGIN GENERATED CHANGE WORKBENCH GLOSSARY -->"
	ManualEndMarker            = "<!-- END GENERATED CHANGE WORKBENCH GLOSSARY -->"
)

// Artifacts contains every checked-in projection derived from glossary.json.
type Artifacts struct {
	Digest     string
	Go         []byte
	TypeScript []byte
	Schema     []byte
	MCP        []byte
	Manual     []byte
}

// Generate produces byte-stable repository projections from one validated
// canonical input.
func Generate(raw []byte) (Artifacts, error) {
	document, _, glossaryDigest, err := Load(raw)
	if err != nil {
		return Artifacts{}, err
	}
	goProjection, err := projectGo(document, glossaryDigest)
	if err != nil {
		return Artifacts{}, err
	}
	typeScript, err := projectTypeScript(document, glossaryDigest)
	if err != nil {
		return Artifacts{}, err
	}
	schema, err := projectSchema(document)
	if err != nil {
		return Artifacts{}, err
	}
	mcp, err := projectMCP(document, glossaryDigest)
	if err != nil {
		return Artifacts{}, err
	}
	return Artifacts{
		Digest: glossaryDigest, Go: goProjection, TypeScript: typeScript,
		Schema: schema, MCP: mcp, Manual: projectManual(document, glossaryDigest),
	}, nil
}

func projectGo(document Document, glossaryDigest string) ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("// Code generated from glossary.json; DO NOT EDIT.\n\n")
	output.WriteString("package glossary\n\n")
	output.WriteString("const Digest = " + strconv.Quote(glossaryDigest) + "\n\n")
	output.WriteString("const (\n")
	for _, capability := range document.Capabilities {
		fmt.Fprintf(&output, "\tCapability%s Capability = %s\n",
			goIdentifier(string(capability)), strconv.Quote(string(capability)))
	}
	output.WriteString(")\n\nconst (\n")
	for _, mode := range []Mode{"add", "migrate", "modify", "retire"} {
		fmt.Fprintf(&output, "\tMode%s Mode = %s\n",
			goIdentifier(string(mode)), strconv.Quote(string(mode)))
	}
	output.WriteString(")\n\nconst (\n")
	for _, surface := range []Surface{"atlas", "caller_map", "impact", "manual", "mcp", "workbench"} {
		fmt.Fprintf(&output, "\tSurface%s Surface = %s\n",
			goIdentifier(string(surface)), strconv.Quote(string(surface)))
	}
	output.WriteString(")\n\nconst (\n")
	for _, term := range document.Terms {
		fmt.Fprintf(&output, "\tTerm%s TermID = %s\n",
			goIdentifier(string(term.ID)), strconv.Quote(string(term.ID)))
	}
	output.WriteString(")\n\n")
	output.WriteString("var Capabilities = []Capability{")
	for index, capability := range document.Capabilities {
		if index > 0 {
			output.WriteString(", ")
		}
		output.WriteString("Capability" + goIdentifier(string(capability)))
	}
	output.WriteString("}\n\n")
	output.WriteString("var Terms = []Term{\n")
	for _, term := range document.Terms {
		output.WriteString("\t{\n")
		fmt.Fprintf(&output, "\t\tID: Term%s,\n", goIdentifier(string(term.ID)))
		writeGoString(&output, "Label", term.Label, "\t\t")
		writeGoString(&output, "ShortHelp", term.ShortHelp, "\t\t")
		writeGoString(&output, "ExpandedHelp", term.ExpandedHelp, "\t\t")
		writeGoString(&output, "EvidenceBoundary", term.EvidenceBoundary, "\t\t")
		writeGoString(&output, "AuthorityBoundary", term.AuthorityBoundary, "\t\t")
		writeGoTypedSlice(&output, "Modes", "Mode", term.Modes, "\t\t")
		writeGoTypedSlice(&output, "Surfaces", "Surface", term.Surfaces, "\t\t")
		writeGoStringSlice(&output, "WireAliases", term.WireAliases, "\t\t")
		output.WriteString("\t\tAvailability: CapabilityPredicate{\n")
		writeGoTypedSlice(&output, "RequiresAll", "Capability",
			term.Availability.RequiresAll, "\t\t\t")
		writeGoTypedSlice(&output, "RequiresAny", "Capability",
			term.Availability.RequiresAny, "\t\t\t")
		writeGoString(&output, "UnavailableHelp", term.Availability.UnavailableHelp, "\t\t\t")
		output.WriteString("\t\t},\n\t},\n")
	}
	output.WriteString("}\n\n")
	output.WriteString("var termsByID = func() map[TermID]Term {\n")
	output.WriteString("\tresult := make(map[TermID]Term, len(Terms))\n")
	output.WriteString("\tfor _, term := range Terms {\n\t\tresult[term.ID] = term\n\t}\n")
	output.WriteString("\treturn result\n}()\n\n")
	output.WriteString("// Lookup returns one generated glossary term by stable identity.\n")
	output.WriteString("func Lookup(id TermID) (Term, bool) {\n\tterm, ok := termsByID[id]\n\treturn term, ok\n}\n")
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format Go glossary projection: %w", err)
	}
	return formatted, nil
}

func projectTypeScript(document Document, glossaryDigest string) ([]byte, error) {
	type availability struct {
		RequiresAll     []Capability `json:"requiresAll"`
		RequiresAny     []Capability `json:"requiresAny"`
		UnavailableHelp string       `json:"unavailableHelp"`
	}
	type termProjection struct {
		ID                TermID       `json:"id"`
		Label             string       `json:"label"`
		ShortHelp         string       `json:"shortHelp"`
		ExpandedHelp      string       `json:"expandedHelp"`
		EvidenceBoundary  string       `json:"evidenceBoundary"`
		AuthorityBoundary string       `json:"authorityBoundary"`
		Modes             []Mode       `json:"modes"`
		Surfaces          []Surface    `json:"surfaces"`
		WireAliases       []string     `json:"wireAliases"`
		Availability      availability `json:"availability"`
	}
	terms := make([]termProjection, len(document.Terms))
	for index, term := range document.Terms {
		terms[index] = termProjection{
			ID: term.ID, Label: term.Label, ShortHelp: term.ShortHelp,
			ExpandedHelp: term.ExpandedHelp, EvidenceBoundary: term.EvidenceBoundary,
			AuthorityBoundary: term.AuthorityBoundary, Modes: term.Modes,
			Surfaces: term.Surfaces, WireAliases: term.WireAliases,
			Availability: availability{
				RequiresAll:     term.Availability.RequiresAll,
				RequiresAny:     term.Availability.RequiresAny,
				UnavailableHelp: term.Availability.UnavailableHelp,
			},
		}
	}
	capabilities, err := json.MarshalIndent(document.Capabilities, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode TypeScript glossary capabilities: %w", err)
	}
	content, err := json.MarshalIndent(terms, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode TypeScript glossary terms: %w", err)
	}
	var output bytes.Buffer
	output.WriteString("// Code generated from internal/glossary/glossary.json; DO NOT EDIT.\n")
	fmt.Fprintf(&output, "export const glossarySchemaVersion = %s as const\n",
		strconv.Quote(document.SchemaVersion))
	fmt.Fprintf(&output, "export const glossaryDigest = %s as const\n\n", strconv.Quote(glossaryDigest))
	output.WriteString("export const glossaryCapabilities = ")
	output.Write(capabilities)
	output.WriteString(" as const\n")
	output.WriteString("export type GlossaryCapability = typeof glossaryCapabilities[number]\n")
	output.WriteString("export type GlossaryMode = 'add' | 'migrate' | 'modify' | 'retire'\n")
	output.WriteString("export type GlossarySurface = 'atlas' | 'caller_map' | 'impact' | 'manual' | 'mcp' | 'workbench'\n\n")
	output.WriteString("export type GlossaryTerm = Readonly<{\n")
	output.WriteString("  id: string\n  label: string\n  shortHelp: string\n  expandedHelp: string\n")
	output.WriteString("  evidenceBoundary: string\n  authorityBoundary: string\n")
	output.WriteString("  modes: readonly GlossaryMode[]\n  surfaces: readonly GlossarySurface[]\n")
	output.WriteString("  wireAliases: readonly string[]\n")
	output.WriteString("  availability: Readonly<{\n")
	output.WriteString("    requiresAll: readonly GlossaryCapability[]\n")
	output.WriteString("    requiresAny: readonly GlossaryCapability[]\n")
	output.WriteString("    unavailableHelp: string\n  }>\n}>\n\n")
	output.WriteString("export const glossaryTerms = ")
	output.Write(content)
	output.WriteString(" as const satisfies readonly GlossaryTerm[]\n")
	output.WriteString("export type GlossaryTermId = typeof glossaryTerms[number]['id']\n")
	return output.Bytes(), nil
}

func projectSchema(document Document) ([]byte, error) {
	capabilities := make([]string, len(document.Capabilities))
	for index, capability := range document.Capabilities {
		capabilities[index] = string(capability)
	}
	termIDs := make([]string, len(document.Terms))
	for index, term := range document.Terms {
		termIDs[index] = string(term.ID)
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "urn:phebs:schema:change-workbench-glossary-v1",
		"title":                "phebs Change Workbench glossary v1",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"schema_version", "capabilities", "terms"},
		"properties": map[string]any{
			"schema_version": map[string]any{"const": document.SchemaVersion},
			"capabilities": map[string]any{
				"type": "array", "minItems": len(capabilities), "maxItems": len(capabilities),
				"uniqueItems": true, "items": map[string]any{"enum": capabilities},
			},
			"terms": map[string]any{
				"type": "array", "minItems": len(termIDs), "maxItems": len(termIDs),
				"items": map[string]any{"$ref": "#/$defs/term"},
			},
		},
		"$defs": map[string]any{
			"term": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"id", "label", "short_help", "expanded_help", "evidence_boundary",
					"authority_boundary", "modes", "surfaces", "wire_aliases", "availability",
				},
				"properties": map[string]any{
					"id":                 map[string]any{"enum": termIDs},
					"label":              boundedStringSchema(128),
					"short_help":         boundedStringSchema(maxTextBytes),
					"expanded_help":      boundedStringSchema(maxTextBytes),
					"evidence_boundary":  boundedStringSchema(maxTextBytes),
					"authority_boundary": boundedStringSchema(maxTextBytes),
					"modes":              enumArraySchema([]string{"add", "migrate", "modify", "retire"}, 1, 4),
					"surfaces": enumArraySchema(
						[]string{"atlas", "caller_map", "impact", "manual", "mcp", "workbench"}, 1, 6,
					),
					"wire_aliases": map[string]any{
						"type": "array", "maxItems": maxArrayRows, "uniqueItems": true,
						"items": map[string]any{
							"type": "string", "minLength": 1, "maxLength": 128,
							"pattern": wireAliasPattern.String(),
						},
					},
					"availability": map[string]any{"$ref": "#/$defs/capability_predicate"},
				},
			},
			"capability_predicate": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"requires_all", "requires_any", "unavailable_help"},
				"properties": map[string]any{
					"requires_all":     enumArraySchema(capabilities, 0, maxArrayRows),
					"requires_any":     enumArraySchema(capabilities, 0, maxArrayRows),
					"unavailable_help": boundedStringSchema(maxTextBytes),
				},
				"anyOf": []any{
					map[string]any{
						"properties": map[string]any{
							"requires_all": map[string]any{"minItems": 1},
						},
					},
					map[string]any{
						"properties": map[string]any{
							"requires_any": map[string]any{"minItems": 1},
						},
					},
				},
			},
		},
	}
	return marshalIndented(schema, "encode glossary schema")
}

func projectMCP(document Document, glossaryDigest string) ([]byte, error) {
	type mcpTerm struct {
		ID           TermID              `json:"id"`
		Label        string              `json:"label"`
		Description  string              `json:"description"`
		Modes        []Mode              `json:"modes"`
		Surfaces     []Surface           `json:"surfaces"`
		Availability CapabilityPredicate `json:"availability"`
	}
	projection := struct {
		SchemaVersion  string    `json:"schema_version"`
		GlossaryDigest string    `json:"glossary_digest"`
		Terms          []mcpTerm `json:"terms"`
	}{
		SchemaVersion:  MCPProjectionSchemaVersion,
		GlossaryDigest: glossaryDigest,
		Terms:          make([]mcpTerm, len(document.Terms)),
	}
	for index, term := range document.Terms {
		projection.Terms[index] = mcpTerm{
			ID: term.ID, Label: term.Label,
			Description: strings.Join([]string{
				term.ShortHelp,
				term.ExpandedHelp,
				"Evidence boundary: " + term.EvidenceBoundary,
				"Authority boundary: " + term.AuthorityBoundary,
			}, " "),
			Modes: term.Modes, Surfaces: term.Surfaces, Availability: term.Availability,
		}
	}
	return marshalIndented(projection, "encode MCP glossary projection")
}

func projectManual(document Document, glossaryDigest string) []byte {
	var output bytes.Buffer
	output.WriteString("#### Canonical Change Workbench glossary\n\n")
	output.WriteString("The following help is generated from the reviewed `" + document.SchemaVersion +
		"` source. Glossary digest: `" + glossaryDigest + "`.\n\n")
	for _, term := range document.Terms {
		output.WriteString("##### " + term.Label + "\n\n")
		output.WriteString(term.ShortHelp + "\n\n")
		output.WriteString(term.ExpandedHelp + "\n\n")
		output.WriteString("- Evidence boundary: " + term.EvidenceBoundary + "\n")
		output.WriteString("- Authority boundary: " + term.AuthorityBoundary + "\n")
		output.WriteString("- Applies to modes: " + joinStringers(term.Modes) + "\n")
		output.WriteString("- Registered surfaces: " + joinStringers(term.Surfaces) + "\n")
		output.WriteString("- Required capabilities (all): " +
			joinStringersOrNone(term.Availability.RequiresAll) + "\n")
		output.WriteString("- Required capabilities (any): " +
			joinStringersOrNone(term.Availability.RequiresAny) + "\n")
		output.WriteString("- When unavailable: " + term.Availability.UnavailableHelp + "\n\n")
	}
	return output.Bytes()
}

func boundedStringSchema(maximum int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": maximum}
}

func enumArraySchema(values []string, minimum, maximum int) map[string]any {
	return map[string]any{
		"type": "array", "minItems": minimum, "maxItems": maximum,
		"uniqueItems": true, "items": map[string]any{"enum": values},
	}
}

func marshalIndented(value any, action string) ([]byte, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return append(content, '\n'), nil
}

func goIdentifier(value string) string {
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == '_' || character == '-'
	})
	var result strings.Builder
	for _, part := range parts {
		if part == "mcp" {
			result.WriteString("MCP")
			continue
		}
		for index, character := range part {
			if index == 0 {
				result.WriteRune(unicode.ToUpper(character))
			} else {
				result.WriteRune(character)
			}
		}
	}
	return result.String()
}

func writeGoString(output *bytes.Buffer, field, value, indent string) {
	fmt.Fprintf(output, "%s%s: %s,\n", indent, field, strconv.Quote(value))
}

func writeGoStringSlice(output *bytes.Buffer, field string, values []string, indent string) {
	fmt.Fprintf(output, "%s%s: []string{", indent, field)
	for index, value := range values {
		if index > 0 {
			output.WriteString(", ")
		}
		output.WriteString(strconv.Quote(value))
	}
	output.WriteString("},\n")
}

func writeGoTypedSlice[T ~string](
	output *bytes.Buffer,
	field string,
	typeName string,
	values []T,
	indent string,
) {
	fmt.Fprintf(output, "%s%s: []%s{", indent, field, typeName)
	for index, value := range values {
		if index > 0 {
			output.WriteString(", ")
		}
		output.WriteString(strconv.Quote(string(value)))
	}
	output.WriteString("},\n")
}

func joinStringers[T ~string](values []T) string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = "`" + string(value) + "`"
	}
	return strings.Join(result, ", ")
}

func joinStringersOrNone[T ~string](values []T) string {
	if len(values) == 0 {
		return "none"
	}
	return joinStringers(values)
}

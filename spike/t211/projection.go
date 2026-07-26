package t211

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"strconv"
	"strings"
)

// ProjectGo returns a compile-valid preview of the typed Go projection T21.4
// will productionize. T21.1 commits no generated production package.
func ProjectGo(glossary Glossary) ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("// Code generated from change-workbench-glossary-v1; DO NOT EDIT.\n")
	output.WriteString("package glossarypreview\n\n")
	output.WriteString("type CapabilityPredicate struct {\n")
	output.WriteString("\tRequiresAll []string\n\tRequiresAny []string\n\tUnavailableHelp string\n}\n\n")
	output.WriteString("type Term struct {\n")
	output.WriteString("\tID string\n\tLabel string\n\tShortHelp string\n\tExpandedHelp string\n")
	output.WriteString("\tEvidenceBoundary string\n\tAuthorityBoundary string\n")
	output.WriteString("\tModes []string\n\tSurfaces []string\n\tWireAliases []string\n")
	output.WriteString("\tAvailability CapabilityPredicate\n}\n\n")
	output.WriteString("var Terms = []Term{\n")
	for _, term := range glossary.Terms {
		output.WriteString("\t{\n")
		writeGoString(&output, "ID", term.ID)
		writeGoString(&output, "Label", term.Label)
		writeGoString(&output, "ShortHelp", term.ShortHelp)
		writeGoString(&output, "ExpandedHelp", term.ExpandedHelp)
		writeGoString(&output, "EvidenceBoundary", term.EvidenceBoundary)
		writeGoString(&output, "AuthorityBoundary", term.AuthorityBoundary)
		writeGoStrings(&output, "Modes", term.Modes)
		writeGoStrings(&output, "Surfaces", term.Surfaces)
		writeGoStrings(&output, "WireAliases", term.WireAliases)
		output.WriteString("\t\tAvailability: CapabilityPredicate{\n")
		writeGoStringsIndent(&output, "RequiresAll", term.Availability.RequiresAll, "\t\t\t")
		writeGoStringsIndent(&output, "RequiresAny", term.Availability.RequiresAny, "\t\t\t")
		output.WriteString("\t\t\tUnavailableHelp: " + strconv.Quote(term.Availability.UnavailableHelp) + ",\n")
		output.WriteString("\t\t},\n\t},\n")
	}
	output.WriteString("}\n")
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format Go glossary projection: %w", err)
	}
	return formatted, nil
}

// ProjectTypeScript returns a deterministic typed UI preview. JSON literals
// are valid TypeScript values and keep escaping behavior centralized.
func ProjectTypeScript(glossary Glossary) ([]byte, error) {
	type termProjection struct {
		ID                string              `json:"id"`
		Label             string              `json:"label"`
		ShortHelp         string              `json:"shortHelp"`
		ExpandedHelp      string              `json:"expandedHelp"`
		EvidenceBoundary  string              `json:"evidenceBoundary"`
		AuthorityBoundary string              `json:"authorityBoundary"`
		Modes             []string            `json:"modes"`
		Surfaces          []string            `json:"surfaces"`
		WireAliases       []string            `json:"wireAliases"`
		Availability      CapabilityPredicate `json:"availability"`
	}
	terms := make([]termProjection, len(glossary.Terms))
	for index, term := range glossary.Terms {
		terms[index] = termProjection(term)
	}
	content, err := json.MarshalIndent(terms, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode TypeScript glossary projection: %w", err)
	}
	var output bytes.Buffer
	output.WriteString("// Code generated from change-workbench-glossary-v1; DO NOT EDIT.\n")
	output.WriteString("export type GlossaryTerm = Readonly<{\n")
	output.WriteString("  id: string\n  label: string\n  shortHelp: string\n  expandedHelp: string\n")
	output.WriteString("  evidenceBoundary: string\n  authorityBoundary: string\n")
	output.WriteString("  modes: readonly string[]\n  surfaces: readonly string[]\n  wireAliases: readonly string[]\n")
	output.WriteString("  availability: Readonly<{ requires_all: readonly string[]; requires_any: readonly string[]; unavailable_help: string }>\n")
	output.WriteString("}>\n\n")
	output.WriteString("export const glossaryTerms = ")
	output.Write(content)
	output.WriteString(" as const satisfies readonly GlossaryTerm[]\n")
	return output.Bytes(), nil
}

func ProjectManual(glossary Glossary, glossaryDigest string) []byte {
	var output bytes.Buffer
	output.WriteString("<!-- Generated preview from change-workbench-glossary-v1. -->\n")
	output.WriteString("Glossary digest: `" + glossaryDigest + "`\n\n")
	for _, term := range glossary.Terms {
		output.WriteString("### " + term.Label + "\n\n")
		output.WriteString(term.ShortHelp + "\n\n")
		output.WriteString(term.ExpandedHelp + "\n\n")
		output.WriteString("- Evidence boundary: " + term.EvidenceBoundary + "\n")
		output.WriteString("- Authority boundary: " + term.AuthorityBoundary + "\n")
		output.WriteString("- When unavailable: " + term.Availability.UnavailableHelp + "\n\n")
	}
	return output.Bytes()
}

func ProjectMCP(glossary Glossary, glossaryDigest string) ([]byte, error) {
	type mcpTerm struct {
		ID           string              `json:"id"`
		Label        string              `json:"label"`
		Description  string              `json:"description"`
		Availability CapabilityPredicate `json:"availability"`
	}
	projection := struct {
		SchemaVersion  string    `json:"schema_version"`
		GlossaryDigest string    `json:"glossary_digest"`
		Terms          []mcpTerm `json:"terms"`
	}{
		SchemaVersion:  "change-workbench-mcp-glossary-preview-v1",
		GlossaryDigest: glossaryDigest,
		Terms:          make([]mcpTerm, len(glossary.Terms)),
	}
	for index, term := range glossary.Terms {
		projection.Terms[index] = mcpTerm{
			ID: term.ID, Label: term.Label,
			Description: strings.Join([]string{
				term.ShortHelp,
				"Evidence boundary: " + term.EvidenceBoundary,
				"Authority boundary: " + term.AuthorityBoundary,
			}, " "),
			Availability: term.Availability,
		}
	}
	content, err := json.Marshal(projection)
	if err != nil {
		return nil, fmt.Errorf("encode MCP glossary projection: %w", err)
	}
	return content, nil
}

func writeGoString(output *bytes.Buffer, field, value string) {
	output.WriteString("\t\t" + field + ": " + strconv.Quote(value) + ",\n")
}

func writeGoStrings(output *bytes.Buffer, field string, values []string) {
	writeGoStringsIndent(output, field, values, "\t\t")
}

func writeGoStringsIndent(output *bytes.Buffer, field string, values []string, indent string) {
	output.WriteString(indent + field + ": []string{")
	for index, value := range values {
		if index > 0 {
			output.WriteString(", ")
		}
		output.WriteString(strconv.Quote(value))
	}
	output.WriteString("},\n")
}

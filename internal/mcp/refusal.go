package mcp

import (
	"encoding/json"
	"time"
)

// NotAvailableText is the single authoritative refusal sentence for unknown
// and unauthorized sensitive identities (MCP envelope v0.2 §5, fixture 06).
// The server renders it verbatim; clients never compose their own wording.
const NotAvailableText = "The requested object is not available to this principal."

type refusalTool struct {
	Name            string `json:"name"`
	SemanticVersion string `json:"semantic_version"`
}

type refusalBody struct {
	AuthoritativeText string `json:"authoritative_text"`
	Code              string `json:"code"`
}

// notAvailableEnvelope is the minimal refusal shape of fixture 06. It is a
// closed struct on purpose: the NOT_AVAILABLE refusal must never carry scope,
// counts, result window, pack, validation, freshness, or provenance, so those
// fields do not exist on the type. Field order follows the canonical
// alphabetical key order of the fixture.
type notAvailableEnvelope struct {
	EnvelopeVersion string      `json:"envelope_version"`
	Errors          []string    `json:"errors"`
	GeneratedAt     string      `json:"generated_at"`
	Outcome         string      `json:"outcome"`
	Refusal         refusalBody `json:"refusal"`
	RequestID       string      `json:"request_id"`
	Tool            refusalTool `json:"tool"`
}

// NotAvailableRefusal renders the canonical NOT_AVAILABLE refusal envelope.
// The bytes are a pure function of tool identity, request id, and timestamp:
// an unknown identity and an unauthorized identity are indistinguishable by
// construction, because the denial reason is not an input.
func NotAvailableRefusal(tool, semanticVersion, requestID string, generatedAt time.Time) ([]byte, error) {
	out, err := json.MarshalIndent(notAvailableEnvelope{
		EnvelopeVersion: "1.0",
		Errors:          []string{},
		GeneratedAt:     generatedAt.UTC().Format(time.RFC3339),
		Outcome:         "refused",
		Refusal:         refusalBody{AuthoritativeText: NotAvailableText, Code: "NOT_AVAILABLE"},
		RequestID:       requestID,
		Tool:            refusalTool{Name: tool, SemanticVersion: semanticVersion},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

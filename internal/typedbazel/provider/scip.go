package provider

import (
	"slices"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// This preserves raw producer identity/ranges and checks exact root membership.
// Canonicalization, blank metadata and generated localization are a separate
// reviewed adapter, never an implicit mutation of this evidence.
func verifySCIP(raw []byte, documents []DocumentOutcome, s Selection, patterns []string) error {
	if len(raw) == 0 || len(raw) > typedindex.MaxSCIPMemberBytes {
		return typedindex.Capacity
	}
	var index scip.Index
	if proto.Unmarshal(raw, &index) != nil {
		return typedindex.Invalid
	}
	m := index.Metadata
	if m == nil || m.ProjectRoot != "file://"+launcher.Workspace || m.TextDocumentEncoding != scip.TextEncoding_UTF8 || m.ToolInfo == nil || m.ToolInfo.Name != "scip-go" || m.ToolInfo.Version != "0.2.7" || !slices.Equal(m.ToolInfo.Arguments, scipArguments(s, patterns)) {
		return typedindex.Invalid
	}
	want := map[string]bool{}
	for _, d := range documents {
		want[d.RawPath] = true
	}
	if len(index.Documents) != len(want) || len(index.ExternalSymbols) > 8192 {
		return typedindex.Invalid
	}
	seen := map[string]bool{}
	occurrences, symbols := 0, 0
	for _, d := range index.Documents {
		if d == nil || d.Language != "go" || !want[d.RelativePath] || seen[d.RelativePath] || len(d.Occurrences) > 65536-occurrences || len(d.Symbols) > 65536-symbols {
			return typedindex.Invalid
		}
		seen[d.RelativePath] = true
		occurrences += len(d.Occurrences)
		symbols += len(d.Symbols)
		for _, o := range d.Occurrences {
			if o == nil {
				return typedindex.Invalid
			}
			r, ok := o.SourceRange()
			if !ok || r.Start.Line < 0 || r.Start.Character < 0 || r.End.Line < r.Start.Line || r.End.Character < 0 || r.Start.Line == r.End.Line && r.End.Character <= r.Start.Character {
				return typedindex.Invalid
			}
		}
		for _, symbol := range d.Symbols {
			if symbol == nil {
				return typedindex.Invalid
			}
		}
	}
	for _, symbol := range index.ExternalSymbols {
		if symbol == nil {
			return typedindex.Invalid
		}
	}
	return nil
}

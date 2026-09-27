package t451b

import (
	"errors"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

type SCIPFacts struct {
	Documents           int `json:"documents"`
	Occurrences         int `json:"occurrences"`
	Symbols             int `json:"symbols"`
	ExternalSymbols     int `json:"external_symbols"`
	Definitions         int `json:"definitions"`
	CrossFileReferences int `json:"cross_file_references"`
	ExternalReferences  int `json:"external_references"`
	Hovers              int `json:"hovers"`
}

func neutralSymbol(value, pkg, name string) bool {
	symbol, err := scip.ParseSymbol(value)
	return err == nil && symbol.Scheme == "scip-go" && symbol.Package != nil && symbol.Package.Manager == "gomod" && symbol.Package.Name == ModulePath && symbol.Package.Version == ModuleVersion && len(symbol.Descriptors) == 2 && symbol.Descriptors[0].Name == pkg && symbol.Descriptors[0].Suffix == scip.Descriptor_Namespace && symbol.Descriptors[1].Name == name && symbol.Descriptors[1].Suffix == scip.Descriptor_Method && symbol.Descriptors[1].Disambiguator == ""
}

// VerifySCIP is an independent oracle over the two admitted alias documents.
// Dependency export loading is covered by the typed probe; dependencies are
// deliberately not claimed as index-root documents or independent modules.
func VerifySCIP(data []byte) (SCIPFacts, error) {
	var facts SCIPFacts
	if len(data) == 0 || len(data) > maxClientBytes {
		return facts, errors.New("SCIP byte bound")
	}
	var index scip.Index
	if err := proto.Unmarshal(data, &index); err != nil {
		return facts, err
	}
	metadata := index.Metadata
	if metadata == nil || metadata.ProjectRoot != "file:///scratch/workspace" || metadata.TextDocumentEncoding != scip.TextEncoding_UTF8 || metadata.ToolInfo == nil || metadata.ToolInfo.Name != "scip-go" || metadata.ToolInfo.Version != "0.2.7" || !slices.Equal(metadata.ToolInfo.Arguments, scipArguments([]string{"example.test/neutral/lib"})) {
		return facts, errors.New("SCIP metadata differs from closed invocation")
	}
	if len(index.Documents) != 2 || len(index.ExternalSymbols) > 8192 {
		return facts, errors.New("SCIP exact root-document set mismatch")
	}
	docs := map[string]*scip.Document{}
	for _, doc := range index.Documents {
		if doc == nil || doc.Language != "go" || docs[doc.RelativePath] != nil || (doc.RelativePath != "lib/a.go" && doc.RelativePath != "lib/lib.go") {
			return facts, errors.New("SCIP root-document identity refused")
		}
		if len(doc.Occurrences) > 4096 || len(doc.Symbols) > 4096 {
			return facts, errors.New("SCIP neutral count bound")
		}
		docs[doc.RelativePath] = doc
		facts.Documents++
		facts.Occurrences += len(doc.Occurrences)
		facts.Symbols += len(doc.Symbols)
	}
	facts.ExternalSymbols = len(index.ExternalSymbols)
	type wantOccurrence struct {
		path             string
		line, start, end int32
		pkg, name        string
		definition       bool
	}
	wants := []wantOccurrence{
		{"lib/a.go", 2, 5, 12, "example.test/neutral/lib", "variant", true},
		{"lib/lib.go", 4, 5, 10, "example.test/neutral/lib", "Value", true},
		{"lib/lib.go", 4, 40, 47, "example.test/neutral/lib", "variant", false},
		{"lib/lib.go", 4, 30, 35, "example.test/external/pkg", "Value", false},
	}
	var definition, reference string
	for _, want := range wants {
		count := 0
		for _, occ := range docs[want.path].Occurrences {
			if occ == nil {
				return facts, errors.New("nil SCIP occurrence")
			}
			r, ok := occ.SourceRange()
			expected := scip.Range{Start: scip.Position{Line: want.line, Character: want.start}, End: scip.Position{Line: want.line, Character: want.end}}
			if !ok || r != expected {
				continue
			}
			if !neutralSymbol(occ.Symbol, want.pkg, want.name) || scip.SymbolRole_Definition.Matches(occ) != want.definition {
				return facts, errors.New("SCIP semantic oracle mismatch")
			}
			count++
			if want.definition {
				facts.Definitions++
				hovers := 0
				for _, info := range docs[want.path].Symbols {
					if info == nil {
						return facts, errors.New("nil SCIP symbol")
					}
					if info.Symbol == occ.Symbol && info.SignatureDocumentation != nil && info.SignatureDocumentation.Language == "go" && strings.TrimSpace(info.SignatureDocumentation.Text) == "func "+want.name+"() int" {
						hovers++
					}
				}
				if hovers != 1 {
					return facts, errors.New("SCIP exact function hover missing")
				}
				facts.Hovers++
				if want.name == "variant" {
					definition = occ.Symbol
				}
			} else if want.name == "variant" {
				reference = occ.Symbol
				facts.CrossFileReferences++
			} else {
				facts.ExternalReferences++
			}
		}
		if count != 1 {
			return facts, errors.New("SCIP required definition/reference absent or repeated")
		}
	}
	if definition == "" || definition != reference {
		return facts, errors.New("SCIP cross-file symbol identity mismatch")
	}
	return facts, nil
}

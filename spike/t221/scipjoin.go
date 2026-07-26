package t221

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// Thrift field-identity bounds: i16 field IDs, with 0 (the result success
// slot) a first-class identity — unlike protobuf, whose minimum is 1.
const (
	ThriftFieldMin = 0
	ThriftFieldMax = 32767
)

// ValidThriftFieldID is the bounds rule D2 freezes for the production
// validateFieldIdentity extension.
func ValidThriftFieldID(id int) bool {
	return id >= ThriftFieldMin && id <= ThriftFieldMax
}

// FieldIdentity is the object spelling for a Thrift field reference:
// scope.Message#ID, aligned with the protobuf pack's scope.Message#N and
// thriftdecl's scope.Operation spelling for the scope segment.
func FieldIdentity(scope, message string, id int) string {
	return fmt.Sprintf("%s.%s#%d", scope, message, id)
}

// SymbolLineage mirrors the scipfield contract_scip_package_v1 recipe:
// sha256 over scheme, package manager, and package name. Reusing the recipe
// byte-for-byte is D6 — no third lineage family.
func SymbolLineage(value string) (string, bool) {
	if scip.IsLocalSymbol(value) {
		return "", false
	}
	symbol, err := scip.ParseSymbol(value)
	if err != nil || symbol.GetScheme() == "" || symbol.GetPackage() == nil ||
		symbol.GetPackage().GetManager() == "" || symbol.GetPackage().GetName() == "" {
		return "", false
	}
	pkg := symbol.GetPackage()
	hash := sha256.Sum256([]byte(symbol.GetScheme() + "\x00" + pkg.GetManager() + "\x00" + pkg.GetName()))
	return "contract_scip_package_v1_" + hex.EncodeToString(hash[:]), true
}

// symbolIdentifier returns the last descriptor's name — the identifier a
// definition occurrence must cover byte-for-byte to be accepted.
func symbolIdentifier(value string) (string, bool) {
	if scip.IsLocalSymbol(value) {
		return "", false
	}
	symbol, err := scip.ParseSymbol(value)
	if err != nil {
		return "", false
	}
	descriptors := symbol.GetDescriptors()
	if len(descriptors) == 0 || descriptors[len(descriptors)-1].GetName() == "" {
		return "", false
	}
	return descriptors[len(descriptors)-1].GetName(), true
}

// LoadIndex reads a SCIP index and verifies its bytes against the expected
// digest from index.lock.json — the prepared-once-reviewed-and-copied-verbatim
// policy from spike/t201.
func LoadIndex(path, wantSHA256 string) (*scip.Index, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return nil, fmt.Errorf("index %s digest %s does not match locked %s", path, got, wantSHA256)
	}
	index := &scip.Index{}
	if err := proto.Unmarshal(raw, index); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	return index, nil
}

// Binding is one accepted field-accessor definition: an eligible generated
// document, a symbol whose identifier the occurrence covers byte-for-byte,
// and the references that joined to it by symbol equality.
type Binding struct {
	Symbol     string
	Family     string
	DocPath    string
	Identifier string
	References []JoinedReference
}

// JoinedReference is one non-definition occurrence joined to a binding.
type JoinedReference struct {
	DocPath        string
	Classification string // "read" or "write", from SCIP symbol roles
}

// Abstention records one occurrence the join refused, with the reason class.
// Abstention is silent in production (D7); the spike surfaces reasons so the
// gates can assert each adversarial entry died for the intended reason.
type Abstention struct {
	Symbol  string
	DocPath string
	Reason  string // "ineligible-document" | "span-mismatch" | "unbound-symbol" | "malformed-symbol"
}

// JoinFieldReferences runs the three-way join discipline over an authored
// index against the real corpus bytes: document eligibility, exact byte-span
// identifier equality for definitions, then symbol-equality reference
// joining. family classifies a document path ("thriftrw"/"apache", ok=false
// for ineligible). References may live in ineligible documents — call sites
// are handwritten code — but definitions must not.
func JoinFieldReferences(index *scip.Index, corpusDir string, family func(rel string) (string, bool)) (map[string]*Binding, []Abstention, error) {
	bindings := make(map[string]*Binding)
	var abstentions []Abstention
	fileCache := make(map[string][]byte)
	readFile := func(rel string) ([]byte, error) {
		if content, ok := fileCache[rel]; ok {
			return content, nil
		}
		content, err := os.ReadFile(filepath.Join(corpusDir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("read corpus file %s: %w", rel, err)
		}
		fileCache[rel] = content
		return content, nil
	}

	for _, doc := range index.GetDocuments() {
		fam, eligible := family(doc.GetRelativePath())
		for _, occurrence := range doc.GetOccurrences() {
			if scip.SymbolRole_Definition.Matches(occurrence) {
				identifier, ok := symbolIdentifier(occurrence.GetSymbol())
				if !ok {
					abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "malformed-symbol"})
					continue
				}
				if !eligible {
					abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "ineligible-document"})
					continue
				}
				content, err := readFile(doc.GetRelativePath())
				if err != nil {
					return nil, nil, err
				}
				rangeValue, present := occurrence.SourceRange()
				if !present || rangeValue.Validate() != nil {
					abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "malformed-symbol"})
					continue
				}
				text, err := spanText(content, rangeValue)
				if err != nil {
					return nil, nil, fmt.Errorf("document %s: %w", doc.GetRelativePath(), err)
				}
				if text != identifier {
					abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "span-mismatch"})
					continue
				}
				bindings[occurrence.GetSymbol()] = &Binding{
					Symbol:     occurrence.GetSymbol(),
					Family:     fam,
					DocPath:    doc.GetRelativePath(),
					Identifier: identifier,
				}
			}
		}
	}

	for _, doc := range index.GetDocuments() {
		for _, occurrence := range doc.GetOccurrences() {
			if scip.SymbolRole_Definition.Matches(occurrence) {
				continue
			}
			binding, ok := bindings[occurrence.GetSymbol()]
			if !ok {
				abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "unbound-symbol"})
				continue
			}
			classification := "read"
			if scip.SymbolRole_WriteAccess.Matches(occurrence) {
				classification = "write"
			}
			binding.References = append(binding.References, JoinedReference{doc.GetRelativePath(), classification})
		}
	}
	for _, binding := range bindings {
		sort.Slice(binding.References, func(i, j int) bool {
			return binding.References[i].DocPath < binding.References[j].DocPath
		})
	}
	return bindings, abstentions, nil
}

// spanText extracts the bytes a single-line SCIP range covers, treating the
// range's character offsets as UTF-8 code units per the authored indexes'
// declared position encoding.
func spanText(content []byte, scipRange scip.Range) (string, error) {
	if scipRange.Start.Line != scipRange.End.Line {
		return "", fmt.Errorf("only single-line ranges are authored, got %v", scipRange)
	}
	line, start, end := int(scipRange.Start.Line), int(scipRange.Start.Character), int(scipRange.End.Character)
	if start > end || line < 0 || start < 0 {
		return "", fmt.Errorf("invalid range %v", scipRange)
	}
	offset := 0
	for current := 0; current < line; current++ {
		next := strings.IndexByte(string(content[offset:]), '\n')
		if next < 0 {
			return "", fmt.Errorf("line %d beyond document", line)
		}
		offset += next + 1
	}
	lineEnd := strings.IndexByte(string(content[offset:]), '\n')
	if lineEnd < 0 {
		lineEnd = len(content) - offset
	}
	if end > lineEnd {
		return "", fmt.Errorf("range %v beyond line end %d", scipRange, lineEnd)
	}
	return string(content[offset+start : offset+end]), nil
}

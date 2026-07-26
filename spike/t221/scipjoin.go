package t221

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"unicode/utf8"

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
	lineage, _, ok := symbolIdentity(value, "", "")
	return lineage, ok
}

// symbolIdentity additionally validates that a SCIP definition names the
// expected generated identifier under the expected enclosing type. This is
// the symbol-to-Thrift-identity part of the three-way join.
func symbolIdentity(value, message, identifier string) (string, string, bool) {
	if scip.IsLocalSymbol(value) {
		return "", "", false
	}
	symbol, err := scip.ParseSymbol(value)
	if err != nil || symbol.GetScheme() == "" || symbol.GetPackage() == nil ||
		symbol.GetPackage().GetManager() == "" || symbol.GetPackage().GetName() == "" {
		return "", "", false
	}
	descriptors := symbol.GetDescriptors()
	if identifier != "" {
		if len(descriptors) == 0 || descriptors[len(descriptors)-1].GetName() != identifier {
			return "", "", false
		}
		matched := false
		for index := len(descriptors) - 2; index >= 0; index-- {
			if descriptors[index].GetSuffix() != scip.Descriptor_Type {
				continue
			}
			matched = descriptors[index].GetName() == message
			break
		}
		if !matched {
			return "", "", false
		}
	}
	pkg := symbol.GetPackage()
	hash := sha256.Sum256([]byte(symbol.GetScheme() + "\x00" + pkg.GetManager() + "\x00" + pkg.GetName()))
	return "contract_scip_package_v1_" + hex.EncodeToString(hash[:]), pkg.GetVersion(), true
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

// Binding is one accepted, fully typed field-accessor definition.
type Binding struct {
	Symbol            string
	Family            string
	DocPath           string
	Identifier        string
	Scope             string
	Message           string
	FieldName         string
	FieldID           int
	Object            string
	Lineage           string
	DependencyVersion string
	SourceBinding     string
	References        []JoinedReference
}

// JoinedReference is one non-definition occurrence joined to a binding. Its
// byte span has already been validated against the immutable source bytes
// using the document's declared SCIP position encoding.
type JoinedReference struct {
	DocPath        string
	StartByte      int
	EndByte        int
	Classification string
}

// Abstention records one occurrence the join refused, with the reason class.
// Abstention is silent in production (D7); the spike surfaces reasons so the
// gates can assert each adversarial entry died for the intended reason.
type Abstention struct {
	Symbol  string
	DocPath string
	Reason  string
}

type bindingCandidate struct {
	binding Binding
	start   int
	end     int
}

// JoinFieldReferences runs the complete three-way join discipline:
// generated-document eligibility, independently recovered Thrift field
// identity at the exact definition span, then SCIP symbol equality for
// references. Duplicate definitions are ambiguous even when their payloads
// happen to agree; no map overwrite can select one by traversal order.
func JoinFieldReferences(index *scip.Index, corpusDir string, resolve DocumentResolver) (map[string]*Binding, []Abstention, error) {
	candidates := make(map[string][]bindingCandidate)
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
		if !utf8.Valid(content) {
			return nil, fmt.Errorf("corpus file %s is not UTF-8", rel)
		}
		fileCache[rel] = content
		return content, nil
	}

	for _, doc := range index.GetDocuments() {
		model, eligible, err := resolve(doc.GetRelativePath())
		if err != nil {
			return nil, nil, fmt.Errorf("classify document %s: %w", doc.GetRelativePath(), err)
		}
		for _, occurrence := range doc.GetOccurrences() {
			if !scip.SymbolRole_Definition.Matches(occurrence) {
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
			start, end, ok := occurrenceByteSpan(content, occurrence, doc.GetPositionEncoding())
			if !ok || start == end {
				abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "malformed-definition-range"})
				continue
			}
			definition, ok := model.DefinitionAt(start, end)
			if !ok {
				reason := "unbound-field-identity"
				if identifier, valid := symbolLastIdentifier(occurrence.GetSymbol()); valid &&
					string(content[start:end]) != identifier {
					reason = "span-mismatch"
				}
				abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), reason})
				continue
			}
			lineage, version, ok := symbolIdentity(occurrence.GetSymbol(), definition.Message, definition.Identifier)
			if !ok || string(content[start:end]) != definition.Identifier {
				abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "malformed-symbol"})
				continue
			}
			candidates[occurrence.GetSymbol()] = append(candidates[occurrence.GetSymbol()], bindingCandidate{
				start: start,
				end:   end,
				binding: Binding{
					Symbol:            occurrence.GetSymbol(),
					Family:            model.Family,
					DocPath:           doc.GetRelativePath(),
					Identifier:        definition.Identifier,
					Scope:             definition.Scope,
					Message:           definition.Message,
					FieldName:         definition.FieldName,
					FieldID:           definition.FieldID,
					Object:            FieldIdentity(definition.Scope, definition.Message, definition.FieldID),
					Lineage:           lineage,
					DependencyVersion: version,
					SourceBinding:     definition.SourceBinding,
				},
			})
		}
	}

	bindings := make(map[string]*Binding)
	for symbol, values := range candidates {
		if len(values) != 1 {
			for _, value := range values {
				abstentions = append(abstentions, Abstention{symbol, value.binding.DocPath, "ambiguous-definition"})
			}
			continue
		}
		value := values[0].binding
		bindings[symbol] = &value
	}

	for _, doc := range index.GetDocuments() {
		var content []byte
		for _, occurrence := range doc.GetOccurrences() {
			if occurrence.GetSymbolRoles()&int32(scip.SymbolRole_Definition|scip.SymbolRole_ForwardDefinition) != 0 {
				continue
			}
			binding, ok := bindings[occurrence.GetSymbol()]
			if !ok {
				abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "unbound-symbol"})
				continue
			}
			if content == nil {
				var err error
				content, err = readFile(doc.GetRelativePath())
				if err != nil {
					return nil, nil, err
				}
			}
			start, end, ok := occurrenceByteSpan(content, occurrence, doc.GetPositionEncoding())
			if !ok || start == end {
				abstentions = append(abstentions, Abstention{occurrence.GetSymbol(), doc.GetRelativePath(), "malformed-reference-range"})
				continue
			}
			binding.References = append(binding.References, JoinedReference{
				DocPath:        doc.GetRelativePath(),
				StartByte:      start,
				EndByte:        end,
				Classification: classifyRoles(occurrence.GetSymbolRoles()),
			})
		}
	}
	for _, binding := range bindings {
		sort.Slice(binding.References, func(i, j int) bool {
			left, right := binding.References[i], binding.References[j]
			if left.DocPath != right.DocPath {
				return left.DocPath < right.DocPath
			}
			if left.StartByte != right.StartByte {
				return left.StartByte < right.StartByte
			}
			if left.EndByte != right.EndByte {
				return left.EndByte < right.EndByte
			}
			return left.Classification < right.Classification
		})
	}
	sort.Slice(abstentions, func(i, j int) bool {
		if abstentions[i].Symbol != abstentions[j].Symbol {
			return abstentions[i].Symbol < abstentions[j].Symbol
		}
		if abstentions[i].DocPath != abstentions[j].DocPath {
			return abstentions[i].DocPath < abstentions[j].DocPath
		}
		return abstentions[i].Reason < abstentions[j].Reason
	})
	return bindings, abstentions, nil
}

func symbolLastIdentifier(value string) (string, bool) {
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

func classifyRoles(roles int32) string {
	switch {
	case roles&int32(scip.SymbolRole_WriteAccess) != 0:
		return "write"
	case roles&int32(scip.SymbolRole_ReadAccess) != 0:
		return "read"
	case roles&int32(scip.SymbolRole_Test) != 0:
		return "test"
	case roles&int32(scip.SymbolRole_Generated) != 0:
		return "generated"
	default:
		return "unknown"
	}
}

func occurrenceByteSpan(content []byte, occurrence *scip.Occurrence, encoding scip.PositionEncoding) (int, int, bool) {
	rangeValue, present := occurrence.SourceRange()
	if !present || rangeValue.Validate() != nil {
		return 0, 0, false
	}
	starts := lineStarts(string(content))
	return byteSpan(string(content), starts, rangeValue, encoding)
}

func lineStarts(content string) []int {
	starts := []int{0}
	for index := 0; index < len(content); index++ {
		if content[index] == '\n' {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func byteSpan(content string, starts []int, rangeValue scip.Range, encoding scip.PositionEncoding) (int, int, bool) {
	start, ok := positionByte(content, starts, rangeValue.Start, encoding)
	if !ok {
		return 0, 0, false
	}
	end, ok := positionByte(content, starts, rangeValue.End, encoding)
	return start, end, ok && end >= start
}

func positionByte(content string, starts []int, position scip.Position, encoding scip.PositionEncoding) (int, bool) {
	if position.Line < 0 || int(position.Line) >= len(starts) || position.Character < 0 {
		return 0, false
	}
	lineStart := starts[position.Line]
	lineEnd := len(content)
	if int(position.Line)+1 < len(starts) {
		lineEnd = starts[position.Line+1] - 1
		if lineEnd > lineStart && content[lineEnd-1] == '\r' {
			lineEnd--
		}
	}
	line := content[lineStart:lineEnd]
	switch encoding {
	case scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart:
		offset := int(position.Character)
		if offset > len(line) || (offset < len(line) && !utf8.RuneStart(line[offset])) {
			return 0, false
		}
		return lineStart + offset, true
	case scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart:
		units := int32(0)
		byteOffset := 0
		for byteOffset < len(line) {
			if units == position.Character {
				return lineStart + byteOffset, true
			}
			r, size := utf8.DecodeRuneInString(line[byteOffset:])
			step := int32(1)
			if encoding == scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart && r > 0xffff {
				step = 2
			}
			units += step
			if units > position.Character {
				return 0, false
			}
			byteOffset += size
		}
		if units == position.Character {
			return lineStart + byteOffset, true
		}
	}
	return 0, false
}

func spanKey(start, end int) string {
	return strconv.Itoa(start) + ":" + strconv.Itoa(end)
}

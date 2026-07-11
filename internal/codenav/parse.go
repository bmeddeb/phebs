package codenav

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
)

type snapshot struct {
	documents         map[string]*document
	definitions       map[string][]indexedLocation
	references        map[string][]indexedLocation
	symbols           map[string]*symbolInfo
	referenceTargets  map[string]map[string]struct{}
	referenceSources  map[string]map[string]struct{}
	validSymbols      map[string]bool
	occurrenceCount   int
	documentCount     int
	symbolCount       int
	relationshipCount int
	estimatedBytes    int64
}

type parseLimits struct {
	documents     int
	occurrences   int
	symbols       int
	relationships int
	hoverBytes    int
	hoverParts    int
	symbolBytes   int
}

func newParseLimits(opts Options) parseLimits {
	return parseLimits{
		documents:     positiveOr(opts.MaxDocuments, DefaultMaxDocuments),
		occurrences:   positiveOr(opts.MaxOccurrences, DefaultMaxOccurrences),
		symbols:       positiveOr(opts.MaxSymbols, DefaultMaxSymbols),
		relationships: positiveOr(opts.MaxRelationships, DefaultMaxRelationships),
		hoverBytes:    positiveOr(opts.MaxHoverBytes, DefaultMaxHoverBytes),
		hoverParts:    positiveOr(opts.MaxHoverParts, DefaultMaxHoverParts),
		symbolBytes:   positiveOr(opts.MaxSymbolBytes, DefaultMaxSymbolBytes),
	}
}

func positiveOr(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

type document struct {
	path        string
	encoding    PositionEncoding
	occurrences []*scip.Occurrence
}

type indexedLocation struct {
	path      string
	codeRange Range
	encoding  PositionEncoding
}

type symbolInfo struct {
	displayName       string
	kind              string
	signature         string
	language          string
	documentation     []string
	definitionSymbols []string
}

func parseSnapshot(ctx context.Context, data []byte, limits parseLimits) (*snapshot, error) {
	result := &snapshot{
		documents:        make(map[string]*document),
		definitions:      make(map[string][]indexedLocation),
		references:       make(map[string][]indexedLocation),
		symbols:          make(map[string]*symbolInfo),
		referenceTargets: make(map[string]map[string]struct{}),
		referenceSources: make(map[string]map[string]struct{}),
		validSymbols:     make(map[string]bool),
	}
	metadataSeen := false
	visitor := scip.IndexVisitor{
		VisitMetadata: func(ctx context.Context, _ *scip.Metadata) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if metadataSeen {
				return fmt.Errorf("SCIP metadata appears more than once")
			}
			metadataSeen = true
			return nil
		},
		VisitDocument: func(ctx context.Context, doc *scip.Document) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !metadataSeen {
				return fmt.Errorf("SCIP document appears before metadata")
			}
			result.documentCount++
			if result.documentCount > limits.documents {
				return semanticLimit("documents", result.documentCount, limits.documents)
			}
			return result.addDocument(doc, limits)
		},
		VisitExternalSymbol: func(ctx context.Context, info *scip.SymbolInformation) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !metadataSeen {
				return fmt.Errorf("SCIP external symbol appears before metadata")
			}
			return result.addSymbol("", info, limits)
		},
	}
	if err := visitor.ParseStreaming(ctx, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	if !metadataSeen {
		return nil, fmt.Errorf("SCIP metadata is missing")
	}
	for _, doc := range result.documents {
		scip.SortOccurrences(doc.occurrences)
	}
	result.validSymbols = nil
	// Raw protobuf bytes account for retained string/range payloads. Fixed
	// structural charges conservatively cover projected structs, maps, slices,
	// and relationship edges used by the in-memory lookup graph.
	result.estimatedBytes = int64(len(data)) +
		int64(result.documentCount)*256 +
		int64(result.occurrenceCount)*384 +
		int64(result.symbolCount)*512 +
		int64(result.relationshipCount)*192
	return result, nil
}

func (s *snapshot) addDocument(input *scip.Document, limits parseLimits) error {
	docPath := input.GetRelativePath()
	if err := validateRepoPath(docPath); err != nil {
		return fmt.Errorf("SCIP document path: %w", err)
	}
	encoding, err := fromSCIPEncoding(input.GetPositionEncoding())
	if err != nil {
		return fmt.Errorf("document %q: %w", docPath, err)
	}
	doc := s.documents[docPath]
	if doc == nil {
		doc = &document{path: docPath, encoding: encoding}
		s.documents[docPath] = doc
	} else if doc.encoding != encoding {
		return fmt.Errorf("document %q has conflicting position encodings", docPath)
	}

	if len(input.GetOccurrences()) > limits.occurrences-s.occurrenceCount {
		return semanticLimit("occurrences", s.occurrenceCount+len(input.GetOccurrences()), limits.occurrences)
	}
	s.occurrenceCount += len(input.GetOccurrences())
	for _, occurrence := range input.GetOccurrences() {
		if occurrence.GetSymbol() == "" {
			continue
		}
		// A stale or partially corrupt index should not make unrelated symbols
		// unusable. Count malformed occurrences against the semantic limit, but
		// do not retain them or validate payload that cannot produce a location.
		r, ok := occurrence.SourceRange()
		if !ok || r.Validate() != nil {
			continue
		}
		if len(occurrence.GetSymbol()) > limits.symbolBytes {
			return fmt.Errorf("document %q occurrence symbol is %d bytes (limit %d): %w", docPath, len(occurrence.GetSymbol()), limits.symbolBytes, ErrSemanticLimit)
		}
		if err := s.validateSymbol(occurrence.GetSymbol()); err != nil {
			return fmt.Errorf("document %q occurrence: %w", docPath, err)
		}
		if err := validateHoverContent(occurrence.GetOverrideDocumentation(), "", "", limits); err != nil {
			return fmt.Errorf("document %q occurrence: %w", docPath, err)
		}
		storedOccurrence := &scip.Occurrence{
			Symbol:                occurrence.GetSymbol(),
			SymbolRoles:           occurrence.GetSymbolRoles(),
			OverrideDocumentation: append([]string(nil), occurrence.GetOverrideDocumentation()...),
		}
		storedOccurrence.SetSourceRange(r)
		doc.occurrences = append(doc.occurrences, storedOccurrence)
		location := indexedLocation{path: docPath, codeRange: fromSCIPRange(r), encoding: encoding}
		key := symbolKey(docPath, occurrence.GetSymbol())
		if scip.SymbolRole_Definition.Matches(occurrence) {
			s.definitions[key] = append(s.definitions[key], location)
		} else if !scip.SymbolRole_ForwardDefinition.Matches(occurrence) {
			s.references[key] = append(s.references[key], location)
		}
	}
	for _, info := range input.GetSymbols() {
		if err := s.addSymbol(docPath, info, limits); err != nil {
			return err
		}
	}
	return nil
}

func (s *snapshot) addSymbol(docPath string, input *scip.SymbolInformation, limits parseLimits) error {
	if input.GetSymbol() == "" {
		return nil
	}
	s.symbolCount++
	if s.symbolCount > limits.symbols {
		return semanticLimit("symbols", s.symbolCount, limits.symbols)
	}
	if len(input.GetSymbol()) > limits.symbolBytes {
		return fmt.Errorf("symbol is %d bytes (limit %d): %w", len(input.GetSymbol()), limits.symbolBytes, ErrSemanticLimit)
	}
	if err := s.validateSymbol(input.GetSymbol()); err != nil {
		return err
	}
	if docPath == "" && scip.IsLocalSymbol(input.GetSymbol()) {
		return fmt.Errorf("external symbol %q is document-local", input.GetSymbol())
	}
	signature, language := "", ""
	if value := input.GetSignatureDocumentation(); value != nil {
		signature, language = value.GetText(), value.GetLanguage()
	}
	if err := validateHoverContent(input.GetDocumentation(), signature, language, limits); err != nil {
		return fmt.Errorf("symbol %q: %w", input.GetSymbol(), err)
	}
	key := symbolKey(docPath, input.GetSymbol())
	info := s.symbols[key]
	if info == nil {
		info = &symbolInfo{}
		s.symbols[key] = info
	}
	if info.displayName == "" {
		info.displayName = input.GetDisplayName()
	}
	if info.kind == "" && input.GetKind() != scip.SymbolInformation_UnspecifiedKind {
		info.kind = strings.ToLower(input.GetKind().String())
	}
	if signature := input.GetSignatureDocumentation(); signature != nil {
		if info.signature == "" {
			info.signature = signature.GetText()
		}
		if info.language == "" {
			info.language = signature.GetLanguage()
		}
	}
	info.documentation = appendUnique(info.documentation, input.GetDocumentation()...)
	if err := validateMergedHoverContent(info, limits); err != nil {
		return fmt.Errorf("symbol %q: %w", input.GetSymbol(), err)
	}
	for _, relationship := range input.GetRelationships() {
		if relationship.GetSymbol() == "" {
			continue
		}
		s.relationshipCount++
		if s.relationshipCount > limits.relationships {
			return semanticLimit("relationships", s.relationshipCount, limits.relationships)
		}
		if len(relationship.GetSymbol()) > limits.symbolBytes {
			return fmt.Errorf("relationship symbol is %d bytes (limit %d): %w", len(relationship.GetSymbol()), limits.symbolBytes, ErrSemanticLimit)
		}
		if err := s.validateSymbol(relationship.GetSymbol()); err != nil {
			return fmt.Errorf("relationship from %q: %w", input.GetSymbol(), err)
		}
		related := symbolKey(docPath, relationship.GetSymbol())
		if relationship.GetIsDefinition() {
			info.definitionSymbols = appendUnique(info.definitionSymbols, related)
		}
		if relationship.GetIsReference() {
			s.addReferenceRelationship(key, related)
		}
	}
	return nil
}

func semanticLimit(kind string, got, limit int) error {
	return fmt.Errorf("SCIP %s count %d exceeds limit %d: %w", kind, got, limit, ErrSemanticLimit)
}

func validateHoverContent(documentation []string, signature, language string, limits parseLimits) error {
	if len(documentation) > limits.hoverParts {
		return fmt.Errorf("hover has %d documentation parts (limit %d): %w", len(documentation), limits.hoverParts, ErrHoverTooLarge)
	}
	bytes := len(signature) + len(language)
	for _, part := range documentation {
		bytes += len(part)
		if bytes > limits.hoverBytes {
			return fmt.Errorf("hover is %d bytes (limit %d): %w", bytes, limits.hoverBytes, ErrHoverTooLarge)
		}
	}
	if bytes > limits.hoverBytes {
		return fmt.Errorf("hover is %d bytes (limit %d): %w", bytes, limits.hoverBytes, ErrHoverTooLarge)
	}
	return nil
}

func validateMergedHoverContent(info *symbolInfo, limits parseLimits) error {
	if len(info.documentation) > limits.hoverParts {
		return fmt.Errorf("merged hover has %d documentation parts (limit %d): %w", len(info.documentation), limits.hoverParts, ErrHoverTooLarge)
	}
	bytes := len(info.displayName) + len(info.signature) + len(info.language)
	for _, part := range info.documentation {
		bytes += len(part)
		if bytes > limits.hoverBytes {
			return fmt.Errorf("merged hover is %d bytes (limit %d): %w", bytes, limits.hoverBytes, ErrHoverTooLarge)
		}
	}
	if bytes > limits.hoverBytes {
		return fmt.Errorf("merged hover is %d bytes (limit %d): %w", bytes, limits.hoverBytes, ErrHoverTooLarge)
	}
	return nil
}

func validateHoverResponse(info *HoverInfo, limits parseLimits) error {
	if len(info.Documentation) > limits.hoverParts {
		return fmt.Errorf("hover response has %d documentation parts (limit %d): %w", len(info.Documentation), limits.hoverParts, ErrHoverTooLarge)
	}
	bytes := len(info.DisplayName) + len(info.Signature) + len(info.Language)
	for _, part := range info.Documentation {
		bytes += len(part)
		if bytes > limits.hoverBytes {
			return fmt.Errorf("hover response is %d bytes (limit %d): %w", bytes, limits.hoverBytes, ErrHoverTooLarge)
		}
	}
	if bytes > limits.hoverBytes {
		return fmt.Errorf("hover response is %d bytes (limit %d): %w", bytes, limits.hoverBytes, ErrHoverTooLarge)
	}
	return nil
}

func (s *snapshot) validateSymbol(symbol string) error {
	if s.validSymbols[symbol] {
		return nil
	}
	if _, err := scip.ParseSymbol(symbol); err != nil {
		return fmt.Errorf("invalid SCIP symbol %q: %w", symbol, err)
	}
	s.validSymbols[symbol] = true
	return nil
}

func (s *snapshot) addReferenceRelationship(source, target string) {
	if s.referenceTargets[source] == nil {
		s.referenceTargets[source] = make(map[string]struct{})
	}
	if s.referenceSources[target] == nil {
		s.referenceSources[target] = make(map[string]struct{})
	}
	s.referenceTargets[source][target] = struct{}{}
	s.referenceSources[target][source] = struct{}{}
}

func (s *snapshot) relatedReferenceSymbols(start string) []string {
	seen := map[string]bool{start: true}
	// SCIP reference relationships are bidirectional for lookup, but they are
	// not transitive. Include direct targets and their registered inverse only;
	// walking the graph would merge unrelated override/mixin families.
	for related := range s.referenceTargets[start] {
		seen[related] = true
	}
	for related := range s.referenceSources[start] {
		seen[related] = true
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (s *snapshot) relatedDefinitionSymbols(start string) []string {
	seen := map[string]bool{start: true}
	// Definition relationships override this symbol's target. They are
	// directional and apply only one hop according to the SCIP protocol.
	if info := s.symbols[start]; info != nil {
		for _, related := range info.definitionSymbols {
			seen[related] = true
		}
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func symbolKey(docPath, symbol string) string {
	if scip.IsLocalSymbol(symbol) {
		return docPath + "\x00" + symbol
	}
	return symbol
}

func fromSCIPEncoding(encoding scip.PositionEncoding) (PositionEncoding, error) {
	switch encoding {
	case scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart:
		return EncodingUTF8, nil
	case scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart:
		return EncodingUTF16, nil
	case scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart:
		return EncodingUTF32, nil
	default:
		return "", fmt.Errorf("SCIP document has unspecified position encoding: %w", ErrUnsupportedEncoding)
	}
}

func fromSCIPRange(r scip.Range) Range {
	return Range{
		Start: Position{Line: r.Start.Line, Character: r.Start.Character},
		End:   Position{Line: r.End.Line, Character: r.End.Character},
	}
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		if addition == "" {
			continue
		}
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}

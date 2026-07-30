// Package scipfield extracts protobuf field references from a repository's
// committed SCIP index. It performs no indexing, module resolution, process
// execution, filesystem access, or network access.
//
// A symbol becomes eligible only when an exact SCIP definition occurrence can
// be joined to a generated Go protobuf struct field or getter, that generated
// declaration carries a protobuf field number/name, and the generator's source
// comment resolves to one unambiguous source-proto declaration. This deliberate
// join lets references use exact SCIP source ranges while field identity uses
// the protobuf message full name and field number. Global SCIP package identity
// supplies version-independent contract lineage; uncertain joins abstain.
package scipfield

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	protoast "github.com/bufbuild/protocompile/ast"
	protoparser "github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"github.com/scip-code/scip/bindings/go/scip"

	"github.com/bmeddeb/phebs/internal/extract/scipsource"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	domain        = "scip-proto-field"
	version       = "1.3.0"
	schemaVersion = "t13-v1"
	indexPath     = "index.scip" // legacy whole-repository test fixture path

	maxDocuments      = 100_000
	maxOccurrences    = 1_000_000
	maxSymbolBytes    = 16 << 10
	maxProtoBytes     = 4 << 20
	maxGeneratedBytes = 4 << 20

	adapterConfigDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// New returns the stateless extractor.
func New() sdk.Extractor { return extractor{} }

type extractor struct{}

func (extractor) Domain() string        { return domain }
func (extractor) Version() string       { return version }
func (extractor) Candidate(string) bool { return false }

type indexedOccurrence struct {
	rangeValue scip.Range
	symbol     string
	roles      int32
}

type indexedDocument struct {
	path        string
	encoding    scip.PositionEncoding
	occurrences []indexedOccurrence
}

type generatedField struct {
	messageGo string
	goName    string
	protoName string
	number    uint64
}

type generatedDefinition struct {
	field      generatedField
	identifier string
	start      int
	end        int
	symbol     string
}

type protoField struct {
	messageFullName string
	name            string
	number          uint64
}

type fieldBinding struct {
	object            string
	lineage           string
	name              string
	dependencyVersion string
}

func (extractor) Extract(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	paths := make(map[string]struct{})
	if err := corpus.WalkFiles(ctx, func(filePath string) error {
		paths[filePath] = struct{}{}
		return nil
	}); err != nil {
		return sdk.Coverage{}, err
	}
	indexCorpus, ok := corpus.(sdk.SCIPCorpus)
	if !ok {
		return sdk.Coverage{}, errors.New("corpus does not support bounded SCIP index reads")
	}
	input, err := indexCorpus.ReadSCIPIndex(ctx)
	if err != nil {
		return sdk.Coverage{}, fmt.Errorf("read SCIP input: %w", err)
	}
	if !input.Present {
		return sdk.Coverage{Protocols: []string{"scip-index-absent"}}, nil
	}
	documents, err := parseIndexScoped(ctx, input.Content, corpus)
	if err != nil {
		return sdk.Coverage{}, fmt.Errorf("parse %s: %w", input.Path, err)
	}
	// Retain only the derived document/occurrence index before the next corpus
	// read; raw index bytes remain owned only by the trusted current-read slot.
	input = sdk.SCIPInput{}

	bindings, err := bindGeneratedFields(ctx, corpus, paths, documents)
	if err != nil {
		return sdk.Coverage{}, err
	}
	if err := emitReferences(ctx, corpus, paths, documents, bindings, emit); err != nil {
		return sdk.Coverage{}, err
	}
	return sdk.Coverage{Protocols: []string{
		"protobuf-generated-accessor-v1",
		"scip",
		"scip-package-lineage-v1",
	}}, nil
}

func parseIndex(ctx context.Context, content string) ([]indexedDocument, error) {
	return parseIndexScoped(ctx, content, nil)
}

func parseIndexScoped(
	ctx context.Context,
	content string,
	corpus sdk.Corpus,
) ([]indexedDocument, error) {
	byPath := make(map[string]*indexedDocument)
	metadataSeen := false
	metadataEncoding := scip.PositionEncoding_UnspecifiedPositionEncoding
	documentCount := 0
	occurrenceCount := 0
	visitor := scip.IndexVisitor{
		VisitMetadata: func(ctx context.Context, metadata *scip.Metadata) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if metadataSeen {
				return errors.New("metadata appears more than once")
			}
			metadataSeen = true
			switch metadata.GetTextDocumentEncoding() {
			case scip.TextEncoding_UnspecifiedTextEncoding:
			case scip.TextEncoding_UTF8:
				metadataEncoding = scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart
			case scip.TextEncoding_UTF16:
				metadataEncoding = scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart
			default:
				return fmt.Errorf(
					"metadata has unsupported text document encoding %s",
					metadata.GetTextDocumentEncoding(),
				)
			}
			return nil
		},
		VisitDocument: func(ctx context.Context, doc *scip.Document) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !metadataSeen {
				return errors.New("document appears before metadata")
			}
			documentCount++
			if documentCount > maxDocuments {
				return fmt.Errorf("more than %d documents", maxDocuments)
			}
			if err := validPath(doc.GetRelativePath()); err != nil {
				return fmt.Errorf("document path: %w", err)
			}
			if scope, ok := corpus.(sdk.SCIPDocumentScope); ok &&
				!scope.SCIPDocumentInScope(doc.GetRelativePath()) {
				return fmt.Errorf(
					"document %q is outside the committed analysis unit",
					doc.GetRelativePath(),
				)
			}
			encoding := doc.GetPositionEncoding()
			if encoding == scip.PositionEncoding_UnspecifiedPositionEncoding {
				encoding = metadataEncoding
			}
			if !validEncoding(encoding) {
				return fmt.Errorf("document %q has unspecified position encoding", doc.GetRelativePath())
			}
			stored := byPath[doc.GetRelativePath()]
			if stored == nil {
				stored = &indexedDocument{path: doc.GetRelativePath(), encoding: encoding}
				byPath[stored.path] = stored
			} else if stored.encoding != encoding {
				return fmt.Errorf("document %q has conflicting position encodings", stored.path)
			}
			if len(doc.GetOccurrences()) > maxOccurrences-occurrenceCount {
				return fmt.Errorf("more than %d occurrences", maxOccurrences)
			}
			occurrenceCount += len(doc.GetOccurrences())
			for _, occurrence := range doc.GetOccurrences() {
				if occurrence.GetSymbol() == "" {
					continue
				}
				if len(occurrence.GetSymbol()) > maxSymbolBytes {
					return fmt.Errorf("document %q has a symbol over %d bytes", stored.path, maxSymbolBytes)
				}
				rangeValue, present := occurrence.SourceRange()
				if !present || rangeValue.Validate() != nil {
					continue
				}
				stored.occurrences = append(stored.occurrences, indexedOccurrence{
					rangeValue: rangeValue,
					symbol:     occurrence.GetSymbol(),
					roles:      occurrence.GetSymbolRoles(),
				})
			}
			return nil
		},
	}
	if err := visitor.ParseStreaming(ctx, strings.NewReader(content)); err != nil {
		return nil, err
	}
	if !metadataSeen {
		return nil, errors.New("metadata is missing")
	}
	documents := make([]indexedDocument, 0, len(byPath))
	for _, doc := range byPath {
		if !scipsource.Eligible(scipsource.ProtoField, doc.path) {
			continue
		}
		sort.Slice(doc.occurrences, func(i, j int) bool {
			left, right := doc.occurrences[i], doc.occurrences[j]
			if comparison := left.rangeValue.CompareStrict(right.rangeValue); comparison != 0 {
				return comparison < 0
			}
			if left.symbol != right.symbol {
				return left.symbol < right.symbol
			}
			return left.roles < right.roles
		})
		documents = append(documents, *doc)
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].path < documents[j].path })
	return documents, nil
}

func bindGeneratedFields(
	ctx context.Context,
	corpus sdk.Corpus,
	paths map[string]struct{},
	documents []indexedDocument,
) (map[string]fieldBinding, error) {
	protoCache := make(map[string]map[string]protoField)
	candidates := make(map[string][]fieldBinding)
	protoSources := generatedProtoSourceIndex(paths)
	for _, doc := range documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !isGeneratedProtoGo(doc.path) {
			continue
		}
		if _, exists := paths[doc.path]; !exists {
			return nil, fmt.Errorf("SCIP generated document %q is absent from the immutable tree", doc.path)
		}
		blob, err := corpus.Read(ctx, doc.path)
		if err != nil {
			return nil, fmt.Errorf("read generated document %q: %w", doc.path, err)
		}
		generatorRelativePath, definitions, err := generatedDefinitions(ctx, doc, blob.Content)
		if err != nil {
			return nil, fmt.Errorf("generated document %q: %w", doc.path, err)
		}
		// generatedDefinitions returns names, spans, and symbols only.
		blob = sdk.Blob{}
		if generatorRelativePath == "" {
			continue
		}
		protoPath := protoSources[generatorRelativePath]
		if protoPath == "" {
			continue
		}
		fields := protoCache[protoPath]
		if fields == nil {
			protoBlob, err := corpus.Read(ctx, protoPath)
			if err != nil {
				return nil, fmt.Errorf("read source proto %q: %w", protoPath, err)
			}
			fields, err = parseProtoFields(ctx, protoPath, protoBlob.Content)
			if err != nil {
				return nil, fmt.Errorf("source proto %q: %w", protoPath, err)
			}
			protoBlob = sdk.Blob{}
			protoCache[protoPath] = fields
		}
		for _, definition := range definitions {
			field, ok := fields[protoFieldKey(definition.field.messageGo, definition.field.number, definition.field.protoName)]
			if !ok {
				continue
			}
			lineage, dependencyVersion, ok := symbolIdentity(
				definition.symbol, definition.field.messageGo, definition.identifier,
			)
			if !ok {
				continue
			}
			candidates[definition.symbol] = append(candidates[definition.symbol], fieldBinding{
				object:            fmt.Sprintf("%s#%d", field.messageFullName, field.number),
				lineage:           lineage,
				name:              field.name,
				dependencyVersion: dependencyVersion,
			})
		}
	}

	bindings := make(map[string]fieldBinding)
	for symbol, values := range candidates {
		first := values[0]
		unambiguous := true
		for _, value := range values[1:] {
			if value != first {
				unambiguous = false
				break
			}
		}
		if unambiguous {
			bindings[symbol] = first
		}
	}
	return bindings, nil
}

func generatedDefinitions(ctx context.Context, doc indexedDocument, content string) (string, []generatedDefinition, error) {
	if len(content) > maxGeneratedBytes {
		return "", nil, fmt.Errorf("source exceeds %d-byte generated parser limit", maxGeneratedBytes)
	}
	if !utf8.ValidString(content) {
		return "", nil, errors.New("source is not UTF-8")
	}
	protoPath := generatedSourcePath(content)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, doc.path, content, parser.SkipObjectResolution)
	if err != nil {
		return "", nil, fmt.Errorf("parse Go: %w", err)
	}
	fields := make(map[string]generatedField)
	var spans []struct {
		field      generatedField
		identifier string
		start      int
		end        int
	}
	for _, declaration := range parsed.Decls {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, fieldNode := range structure.Fields.List {
				if len(fieldNode.Names) != 1 || fieldNode.Tag == nil {
					continue
				}
				number, protoName, ok := parseProtobufTag(fieldNode.Tag.Value)
				if !ok {
					continue
				}
				field := generatedField{
					messageGo: strings.Clone(typeSpec.Name.Name),
					goName:    strings.Clone(fieldNode.Names[0].Name),
					protoName: protoName,
					number:    number,
				}
				fields[field.messageGo+"\x00"+field.goName] = field
				start, end := nodeSpan(fset, fieldNode.Names[0])
				spans = append(spans, struct {
					field      generatedField
					identifier string
					start      int
					end        int
				}{field: field, identifier: strings.Clone(fieldNode.Names[0].Name), start: start, end: end})
			}
		}
	}
	for _, declaration := range parsed.Decls {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || !strings.HasPrefix(function.Name.Name, "Get") {
			continue
		}
		receiver := receiverName(function.Recv)
		field, ok := fields[receiver+"\x00"+strings.TrimPrefix(function.Name.Name, "Get")]
		if !ok {
			continue
		}
		start, end := nodeSpan(fset, function.Name)
		spans = append(spans, struct {
			field      generatedField
			identifier string
			start      int
			end        int
		}{field: field, identifier: strings.Clone(function.Name.Name), start: start, end: end})
	}

	definitionsBySpan := make(map[string][]indexedOccurrence)
	starts := lineStarts(content)
	for index, occurrence := range doc.occurrences {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		if occurrence.roles&int32(scip.SymbolRole_Definition) == 0 || scip.IsLocalSymbol(occurrence.symbol) {
			continue
		}
		start, end, ok := byteSpan(content, starts, occurrence.rangeValue, doc.encoding)
		if !ok || start == end {
			continue
		}
		key := spanKey(start, end)
		definitionsBySpan[key] = append(definitionsBySpan[key], occurrence)
	}
	definitions := make([]generatedDefinition, 0, len(spans))
	for _, span := range spans {
		occurrences := definitionsBySpan[spanKey(span.start, span.end)]
		if len(occurrences) != 1 {
			continue
		}
		definitions = append(definitions, generatedDefinition{
			field: span.field, identifier: span.identifier,
			start: span.start, end: span.end, symbol: occurrences[0].symbol,
		})
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].start != definitions[j].start {
			return definitions[i].start < definitions[j].start
		}
		return definitions[i].symbol < definitions[j].symbol
	})
	return protoPath, definitions, nil
}

func parseProtoFields(ctx context.Context, protoPath, content string) (map[string]protoField, error) {
	if len(content) > maxProtoBytes {
		return nil, fmt.Errorf("source exceeds %d-byte proto parser limit", maxProtoBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handler := reporter.NewHandler(nil)
	fileNode, err := protoparser.Parse(protoPath, strings.NewReader(content), handler)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if fileNode == nil {
		return nil, errors.New("parse returned no file")
	}
	var packageName string
	for _, declaration := range fileNode.Decls {
		if packageNode, ok := declaration.(*protoast.PackageNode); ok {
			packageName = string(packageNode.Name.AsIdentifier())
			break
		}
	}
	values := make(map[string][]protoField)
	var walkMessage func(fullName, goName string, declarations []protoast.MessageElement) error
	addField := func(fullName, goName, name string, number uint64) {
		key := protoFieldKey(goName, number, name)
		values[key] = append(values[key], protoField{
			messageFullName: strings.Clone(fullName), name: strings.Clone(name), number: number,
		})
	}
	walkMessage = func(fullName, goName string, declarations []protoast.MessageElement) error {
		for _, element := range declarations {
			if err := ctx.Err(); err != nil {
				return err
			}
			switch node := element.(type) {
			case *protoast.FieldNode:
				if node.Name != nil && node.Tag != nil {
					addField(fullName, goName, string(node.Name.AsIdentifier()), node.Tag.Val)
				}
			case *protoast.MapFieldNode:
				if node.Name != nil && node.Tag != nil {
					addField(fullName, goName, string(node.Name.AsIdentifier()), node.Tag.Val)
				}
			case *protoast.OneofNode:
				for _, oneofElement := range node.Decls {
					if field, ok := oneofElement.(*protoast.FieldNode); ok && field.Name != nil && field.Tag != nil {
						addField(fullName, goName, string(field.Name.AsIdentifier()), field.Tag.Val)
					}
				}
			case *protoast.MessageNode:
				if node.Name == nil {
					continue
				}
				name := string(node.Name.AsIdentifier())
				if err := walkMessage(fullName+"."+name, goName+"_"+goCamelCase(name), node.Decls); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, declaration := range fileNode.Decls {
		message, ok := declaration.(*protoast.MessageNode)
		if !ok || message.Name == nil {
			continue
		}
		name := string(message.Name.AsIdentifier())
		fullName := name
		if packageName != "" {
			fullName = packageName + "." + name
		}
		if err := walkMessage(fullName, goCamelCase(name), message.Decls); err != nil {
			return nil, err
		}
	}
	fields := make(map[string]protoField)
	for key, candidates := range values {
		if len(candidates) == 1 {
			fields[key] = candidates[0]
		}
	}
	return fields, nil
}

func emitReferences(
	ctx context.Context,
	corpus sdk.Corpus,
	paths map[string]struct{},
	documents []indexedDocument,
	bindings map[string]fieldBinding,
	emit sdk.Emit,
) error {
	for _, doc := range documents {
		if err := ctx.Err(); err != nil {
			return err
		}
		hasReferences := false
		for index, occurrence := range doc.occurrences {
			if index&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if _, ok := bindings[occurrence.symbol]; ok && isReference(occurrence.roles) {
				hasReferences = true
				break
			}
		}
		if !hasReferences {
			continue
		}
		if _, exists := paths[doc.path]; !exists {
			return fmt.Errorf("SCIP reference document %q is absent from the immutable tree", doc.path)
		}
		blob, err := corpus.Read(ctx, doc.path)
		if err != nil {
			return fmt.Errorf("read reference document %q: %w", doc.path, err)
		}
		if !utf8.ValidString(blob.Content) {
			return fmt.Errorf("reference document %q is not UTF-8", doc.path)
		}
		starts := lineStarts(blob.Content)
		previous := ""
		for index, occurrence := range doc.occurrences {
			if index&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			binding, ok := bindings[occurrence.symbol]
			if !ok || !isReference(occurrence.roles) {
				continue
			}
			start, end, ok := byteSpan(blob.Content, starts, occurrence.rangeValue, doc.encoding)
			if !ok || start == end {
				continue
			}
			dedupeKey := spanKey(start, end) + "\x00" + occurrence.symbol + "\x00" + strconv.FormatInt(int64(occurrence.roles), 10)
			if dedupeKey == previous {
				continue
			}
			previous = dedupeKey
			classification := classify(occurrence.roles)
			detail := `{"schema":"proto-field-reference-detail-v1","name":` + strconv.Quote(binding.name) +
				`,"classification":` + strconv.Quote(classification) +
				`,"dependency_version":` + strconv.Quote(binding.dependencyVersion) + `}`
			if err := emit(sdk.Fact{
				Atom: sdk.AtomInput{
					SchemaVersion:       schemaVersion,
					BlobDigest:          blob.Digest,
					StartByte:           start,
					EndByte:             end,
					RuleID:              "scip-proto-field-reference-v1",
					AdapterConfigDigest: adapterConfigDigest,
					FactFingerprint:     "REFERENCES_PROTO_FIELD|" + binding.object + "|" + classification,
				},
				Path:      doc.path,
				StartLine: lineAtByte(starts, start),
				EndLine:   lineAtByte(starts, end-1),
				Assertion: sdk.AssertionInput{
					Predicate: "REFERENCES_PROTO_FIELD",
					Subject:   doc.path + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end),
					Object:    binding.object,
					Lineage:   binding.lineage,
					Tier:      "exact",
					CodeRole:  codeRole(doc.path, occurrence.roles),
					Detail:    detail,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func isReference(roles int32) bool {
	return roles&int32(scip.SymbolRole_Definition|scip.SymbolRole_ForwardDefinition) == 0
}

func classify(roles int32) string {
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

func codeRole(filePath string, roles int32) string {
	segments := strings.Split(path.Clean(filePath), "/")
	for _, segment := range segments {
		if segment == "vendor" {
			return "vendor"
		}
	}
	for _, segment := range segments {
		if segment == "mock" || segment == "mocks" || strings.HasPrefix(segment, "mock_") || strings.HasSuffix(segment, "_mock.go") {
			return "mock"
		}
	}
	if roles&int32(scip.SymbolRole_Generated) != 0 || isGeneratedProtoGo(filePath) {
		return "generated"
	}
	if roles&int32(scip.SymbolRole_Test) != 0 || strings.HasSuffix(filePath, "_test.go") {
		return "test"
	}
	for _, segment := range segments {
		if segment == "test" || segment == "tests" || segment == "testdata" {
			return "test"
		}
	}
	return "production"
}

func symbolIdentity(value, messageGo, identifier string) (string, string, bool) {
	if scip.IsLocalSymbol(value) {
		return "", "", false
	}
	symbol, err := scip.ParseSymbol(value)
	if err != nil || symbol.GetScheme() == "" || symbol.GetPackage() == nil ||
		symbol.GetPackage().GetManager() == "" || symbol.GetPackage().GetName() == "" {
		return "", "", false
	}
	descriptors := symbol.GetDescriptors()
	if len(descriptors) == 0 || descriptors[len(descriptors)-1].GetName() != identifier {
		return "", "", false
	}
	messageMatched := false
	for index := len(descriptors) - 2; index >= 0; index-- {
		if descriptors[index].GetSuffix() != scip.Descriptor_Type {
			continue
		}
		messageMatched = descriptors[index].GetName() == messageGo
		break
	}
	if !messageMatched {
		return "", "", false
	}
	pkg := symbol.GetPackage()
	hash := sha256.Sum256([]byte(symbol.GetScheme() + "\x00" + pkg.GetManager() + "\x00" + pkg.GetName()))
	return "contract_scip_package_v1_" + hex.EncodeToString(hash[:]), pkg.GetVersion(), true
}

func parseProtobufTag(literal string) (uint64, string, bool) {
	tag, err := strconv.Unquote(literal)
	if err != nil {
		return 0, "", false
	}
	value := reflect.StructTag(tag).Get("protobuf")
	if value == "" {
		return 0, "", false
	}
	parts := strings.Split(value, ",")
	if len(parts) < 2 {
		return 0, "", false
	}
	number, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || number == 0 {
		return 0, "", false
	}
	var name string
	for _, part := range parts[2:] {
		if candidate, ok := strings.CutPrefix(part, "name="); ok {
			name = candidate
			break
		}
	}
	return number, name, name != ""
}

func generatedSourcePath(content string) string {
	limit := len(content)
	if limit > 64<<10 {
		limit = 64 << 10
	}
	for _, line := range strings.Split(content[:limit], "\n") {
		line = strings.TrimSuffix(line, "\r")
		value, ok := strings.CutPrefix(line, "// source: ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if validPath(value) == nil && strings.HasSuffix(value, ".proto") {
			return strings.Clone(value)
		}
		return ""
	}
	return ""
}

// generatedProtoSourceIndex maps protoc-gen-go's generator-relative source
// marker to one immutable repository path. A regular committed buf.yaml marks
// its directory as a Buf module root; the repository root remains the legacy
// invocation root. Duplicate relative paths across roots are deliberately
// omitted instead of guessing which module produced the generated file.
func generatedProtoSourceIndex(paths map[string]struct{}) map[string]string {
	moduleRoots := map[string]struct{}{".": {}}
	for filePath := range paths {
		if path.Base(filePath) == "buf.yaml" && validPath(filePath) == nil {
			moduleRoots[path.Dir(filePath)] = struct{}{}
		}
	}

	resolved := make(map[string]string)
	ambiguous := make(map[string]struct{})
	for filePath := range paths {
		if !strings.HasSuffix(filePath, ".proto") || validPath(filePath) != nil {
			continue
		}
		for root := path.Dir(filePath); ; root = path.Dir(root) {
			if _, ok := moduleRoots[root]; ok {
				relative := filePath
				if root != "." {
					relative = strings.TrimPrefix(filePath, root+"/")
				}
				if previous, exists := resolved[relative]; !exists {
					resolved[relative] = filePath
				} else if previous != filePath {
					ambiguous[relative] = struct{}{}
				}
			}
			if root == "." {
				break
			}
		}
	}
	for relative := range ambiguous {
		delete(resolved, relative)
	}
	return resolved
}

func receiverName(list *ast.FieldList) string {
	if list == nil || len(list.List) != 1 {
		return ""
	}
	expression := list.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	identifier, _ := expression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func nodeSpan(fset *token.FileSet, node ast.Node) (int, int) {
	return fset.PositionFor(node.Pos(), false).Offset, fset.PositionFor(node.End(), false).Offset
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

func lineAtByte(starts []int, offset int) int {
	return sort.Search(len(starts), func(index int) bool { return starts[index] > offset })
}

func spanKey(start, end int) string {
	return strconv.Itoa(start) + ":" + strconv.Itoa(end)
}

func protoFieldKey(goMessage string, number uint64, protoName string) string {
	return goMessage + "\x00" + strconv.FormatUint(number, 10) + "\x00" + protoName
}

func isGeneratedProtoGo(filePath string) bool {
	return strings.HasSuffix(filePath, ".pb.go") && !strings.HasSuffix(filePath, "_grpc.pb.go")
}

func goCamelCase(value string) string {
	var result strings.Builder
	upper := true
	for _, r := range value {
		if r == '_' {
			upper = true
			continue
		}
		if upper && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		result.WriteRune(r)
		upper = false
	}
	return result.String()
}

func validEncoding(encoding scip.PositionEncoding) bool {
	return encoding == scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart ||
		encoding == scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart ||
		encoding == scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart
}

func validPath(value string) error {
	if value == "" || len(value) > 4096 || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "-") ||
		strings.Contains(value, `\`) || path.Clean(value) != value || !utf8.ValidString(value) {
		return fmt.Errorf("invalid repository path %q", value)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("invalid repository path %q", value)
		}
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid repository path %q", value)
		}
	}
	return nil
}

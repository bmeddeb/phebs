// Package thriftfield extracts thriftrw field references from a repository's
// committed SCIP index. It performs no indexing, module resolution, process
// execution, filesystem access, or network access.
//
// A symbol becomes eligible only when an exact SCIP definition occurrence can
// be joined to a field or accessor in a thriftrw-generated Go file. That file
// must carry a digest-valid embedded ThriftModule, and the Raw IDL field
// identity must agree with the generated struct-field order and the
// wire.Field{ID:} literals in its ToWire method. References retain exact SCIP
// source spans; uncertain or duplicate joins abstain.
package thriftfield

import (
	"context"
	"crypto/sha1" //nolint:gosec // thriftrw defines SHA1 as descriptor identity; this verifies it, not a security boundary.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scip-code/scip/bindings/go/scip"
	thriftast "go.uber.org/thriftrw/ast"
	"go.uber.org/thriftrw/idl"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	domain        = "scip-thrift-field"
	version       = "1.0.0"
	schemaVersion = "t22-v1"
	indexPath     = "index.scip"

	// T22.1 proved the 4 MiB generated-file bound against the pinned cadence
	// corpus. The root-index, document, occurrence, and symbol limits inherited
	// from scipfield were not scale-validated there, so T22.2 deliberately
	// narrows them instead of representing them as measured.
	maxIndexBytes     = 32 << 20
	maxDocuments      = 50_000
	maxOccurrences    = 500_000
	maxSymbolBytes    = 8 << 10
	maxGeneratedBytes = 4 << 20

	minFieldID = 0
	maxFieldID = 32_767

	adapterConfigDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// New returns the stateless extractor.
func New() sdk.Extractor { return extractor{} }

type extractor struct{}

func (extractor) Domain() string  { return domain }
func (extractor) Version() string { return version }
func (extractor) Candidate(filePath string) bool {
	return filePath == indexPath
}

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

type moduleDeclaration struct {
	name     string
	pkg      string
	filePath string
	sha1     string
	raw      string
}

type expectedField struct {
	name   string
	goName string
	id     int
}

type generatedField struct {
	name  string
	start int
	end   int
	id    int
}

type fieldDefinition struct {
	identifier    string
	scope         string
	message       string
	fieldName     string
	fieldID       int
	sourceBinding string
}

type generatedDefinition struct {
	field      fieldDefinition
	identifier string
	start      int
	end        int
	symbol     string
}

type fieldBinding struct {
	object            string
	lineage           string
	name              string
	dependencyVersion string
	generator         string
	sourceBinding     string
}

func (extractor) Extract(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	paths := make(map[string]struct{})
	if err := corpus.WalkFiles(ctx, func(filePath string) error {
		paths[filePath] = struct{}{}
		return nil
	}); err != nil {
		return sdk.Coverage{}, err
	}
	if _, ok := paths[indexPath]; !ok {
		return sdk.Coverage{Protocols: []string{"scip-index-absent"}}, nil
	}
	indexCorpus, ok := corpus.(sdk.SCIPCorpus)
	if !ok {
		return sdk.Coverage{}, errors.New("corpus does not support bounded SCIP index reads")
	}
	indexBlob, err := indexCorpus.ReadSCIPIndex(ctx)
	if err != nil {
		return sdk.Coverage{}, fmt.Errorf("read %s: %w", indexPath, err)
	}
	documents, err := parseIndex(ctx, indexBlob.Content)
	if err != nil {
		return sdk.Coverage{}, fmt.Errorf("parse %s: %w", indexPath, err)
	}
	// Retain only the bounded derived index before reading source blobs; raw
	// index bytes remain owned by the trusted current-read slot.
	indexBlob = sdk.Blob{}

	bindings, err := bindGeneratedFields(ctx, corpus, paths, documents)
	if err != nil {
		return sdk.Coverage{}, err
	}
	if err := emitReferences(ctx, corpus, paths, documents, bindings, emit); err != nil {
		return sdk.Coverage{}, err
	}
	return sdk.Coverage{Protocols: []string{
		"scip",
		"scip-package-lineage-v1",
		"thriftrw-module-digest-v1",
	}}, nil
}

func parseIndex(ctx context.Context, content string) ([]indexedDocument, error) {
	if len(content) > maxIndexBytes {
		return nil, fmt.Errorf("index exceeds %d-byte thriftfield limit", maxIndexBytes)
	}
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
	candidates := make(map[string][]fieldBinding)
	for _, doc := range documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(doc.path, ".go") || !hasDefinitions(doc) {
			continue
		}
		if _, exists := paths[doc.path]; !exists {
			return nil, fmt.Errorf("SCIP definition document %q is absent from the immutable tree", doc.path)
		}
		blob, err := corpus.Read(ctx, doc.path)
		if err != nil {
			return nil, fmt.Errorf("read definition document %q: %w", doc.path, err)
		}
		definitions, eligible, err := generatedDefinitions(ctx, doc, blob.Content)
		if err != nil {
			return nil, fmt.Errorf("generated document %q: %w", doc.path, err)
		}
		// Definitions retain names, identity, and symbol only.
		blob = sdk.Blob{}
		if !eligible {
			continue
		}
		for _, definition := range definitions {
			lineage, dependencyVersion, ok := symbolIdentity(
				definition.symbol, definition.field.message, definition.identifier,
			)
			if !ok {
				continue
			}
			candidates[definition.symbol] = append(candidates[definition.symbol], fieldBinding{
				object: fmt.Sprintf(
					"%s.%s#%d",
					definition.field.scope, definition.field.message, definition.field.fieldID,
				),
				lineage:           lineage,
				name:              definition.field.fieldName,
				dependencyVersion: dependencyVersion,
				generator:         "thriftrw",
				sourceBinding:     definition.field.sourceBinding,
			})
		}
	}

	bindings := make(map[string]fieldBinding)
	for symbol, values := range candidates {
		// Duplicate definitions are ambiguous even when their payloads happen
		// to agree; traversal order must never select one.
		if len(values) == 1 {
			bindings[symbol] = values[0]
		}
	}
	return bindings, nil
}

func generatedDefinitions(
	ctx context.Context,
	doc indexedDocument,
	content string,
) ([]generatedDefinition, bool, error) {
	if !looksLikeThriftRWModule(content) {
		return nil, false, nil
	}
	if len(content) > maxGeneratedBytes {
		return nil, false, fmt.Errorf("source exceeds %d-byte generated parser limit", maxGeneratedBytes)
	}
	if !utf8.ValidString(content) {
		return nil, false, errors.New("source is not UTF-8")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, doc.path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, false, fmt.Errorf("parse Go: %w", err)
	}
	module, found, err := parseModule(file)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	sum := sha1.Sum([]byte(module.raw)) //nolint:gosec // descriptor integrity, not security.
	if got := hex.EncodeToString(sum[:]); got != module.sha1 {
		return nil, false, fmt.Errorf("ThriftModule sha1(Raw) %s does not match %s", got, module.sha1)
	}
	scope, err := moduleScope(module)
	if err != nil {
		return nil, false, err
	}
	expected, err := expectedFields(module.raw)
	if err != nil {
		return nil, false, err
	}
	model, err := documentDefinitions(fset, file, expected, scope)
	if err != nil {
		return nil, false, err
	}

	definitionsBySpan := make(map[string][]indexedOccurrence)
	starts := lineStarts(content)
	for index, occurrence := range doc.occurrences {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		if occurrence.roles&int32(scip.SymbolRole_Definition) == 0 || scip.IsLocalSymbol(occurrence.symbol) {
			continue
		}
		start, end, ok := byteSpan(content, starts, occurrence.rangeValue, doc.encoding)
		if !ok || start == end {
			continue
		}
		definitionsBySpan[spanKey(start, end)] = append(
			definitionsBySpan[spanKey(start, end)],
			occurrence,
		)
	}
	definitions := make([]generatedDefinition, 0, len(model))
	for key, field := range model {
		occurrences := definitionsBySpan[key]
		if len(occurrences) != 1 {
			continue
		}
		start, end, ok := parseSpanKey(key)
		if !ok || content[start:end] != field.identifier {
			continue
		}
		definitions = append(definitions, generatedDefinition{
			field: field, identifier: field.identifier,
			start: start, end: end, symbol: occurrences[0].symbol,
		})
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].start != definitions[j].start {
			return definitions[i].start < definitions[j].start
		}
		return definitions[i].symbol < definitions[j].symbol
	})
	return definitions, true, nil
}

func documentDefinitions(
	fset *token.FileSet,
	file *ast.File,
	expected map[string]map[int]expectedField,
	scope string,
) (map[string]fieldDefinition, error) {
	structs := generatedStructFields(fset, file)
	wireIDs, err := orderedWireFieldIDs(file)
	if err != nil {
		return nil, err
	}
	fieldsByType := make(map[string]map[string]generatedField)
	for message, fields := range structs {
		ids := wireIDs[message]
		want := expected[message]
		if len(fields) == 0 || len(fields) != len(ids) || len(fields) != len(want) {
			continue
		}
		resolved := make(map[string]generatedField, len(fields))
		valid := true
		for index, field := range fields {
			id := ids[index]
			expectedField, ok := want[id]
			if !ok || expectedField.goName != field.name || !validFieldID(id) {
				valid = false
				break
			}
			field.id = id
			resolved[field.name] = field
		}
		if valid && len(resolved) == len(fields) {
			fieldsByType[message] = resolved
		}
	}

	definitions := make(map[string]fieldDefinition)
	add := func(start, end int, identifier, message, goField string) error {
		field, ok := fieldsByType[message][goField]
		if !ok {
			return nil
		}
		want := expected[message][field.id]
		key := spanKey(start, end)
		if _, duplicate := definitions[key]; duplicate {
			return fmt.Errorf("duplicate thriftrw definition span %s", key)
		}
		definitions[key] = fieldDefinition{
			identifier:    identifier,
			scope:         scope,
			message:       message,
			fieldName:     want.name,
			fieldID:       field.id,
			sourceBinding: "module_digest",
		}
		return nil
	}
	for message, fields := range fieldsByType {
		for _, field := range fields {
			if err := add(field.start, field.end, field.name, message, field.name); err != nil {
				return nil, err
			}
		}
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name == nil {
			continue
		}
		message := receiverName(function.Recv)
		if message == "" {
			continue
		}
		var goField string
		switch {
		case strings.HasPrefix(function.Name.Name, "Get"):
			goField = strings.TrimPrefix(function.Name.Name, "Get")
		case strings.HasPrefix(function.Name.Name, "IsSet"):
			goField = strings.TrimPrefix(function.Name.Name, "IsSet")
		default:
			continue
		}
		start, end := nodeSpan(fset, function.Name)
		if err := add(start, end, function.Name.Name, message, goField); err != nil {
			return nil, err
		}
	}
	return definitions, nil
}

func parseModule(file *ast.File) (moduleDeclaration, bool, error) {
	alias := importAliasFor(file, "go.uber.org/thriftrw/thriftreflect")
	if alias == "" {
		return moduleDeclaration{}, false, nil
	}
	literal := findModuleLiteral(file, alias)
	if literal == nil {
		if hasModuleDeclaration(file) {
			return moduleDeclaration{}, false, errors.New("ThriftModule declaration has unsupported shape")
		}
		return moduleDeclaration{}, false, nil
	}
	module := moduleDeclaration{}
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok {
			continue
		}
		var target *string
		switch key.Name {
		case "Name":
			target = &module.name
		case "Package":
			target = &module.pkg
		case "FilePath":
			target = &module.filePath
		case "SHA1":
			target = &module.sha1
		case "Raw":
			target = &module.raw
		default:
			continue
		}
		value, err := resolveStringExpr(file, keyValue.Value, make(map[string]bool))
		if err != nil {
			return moduleDeclaration{}, false, fmt.Errorf("ThriftModule field %s: %w", key.Name, err)
		}
		*target = value
	}
	if module.name == "" || module.pkg == "" || module.filePath == "" ||
		module.sha1 == "" || module.raw == "" {
		return moduleDeclaration{}, false, fmt.Errorf(
			"ThriftModule literal is missing required fields (name=%q package=%q filePath=%q)",
			module.name, module.pkg, module.filePath,
		)
	}
	if err := validPath(module.filePath); err != nil || !strings.HasSuffix(module.filePath, ".thrift") {
		return moduleDeclaration{}, false, fmt.Errorf("ThriftModule FilePath %q is invalid", module.filePath)
	}
	return module, true, nil
}

func findModuleLiteral(file *ast.File, alias string) *ast.CompositeLit {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "ThriftModule" ||
				len(value.Values) != 1 {
				continue
			}
			address, ok := value.Values[0].(*ast.UnaryExpr)
			if !ok || address.Op != token.AND {
				continue
			}
			literal, ok := address.X.(*ast.CompositeLit)
			if !ok {
				continue
			}
			selector, ok := literal.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "ThriftModule" {
				continue
			}
			pkg, ok := selector.X.(*ast.Ident)
			if ok && pkg.Name == alias {
				return literal
			}
		}
	}
	return nil
}

func hasModuleDeclaration(file *ast.File) bool {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				if name.Name == "ThriftModule" {
					return true
				}
			}
		}
	}
	return false
}

func resolveStringExpr(file *ast.File, expression ast.Expr, resolving map[string]bool) (string, error) {
	switch typed := expression.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", fmt.Errorf("unexpected literal kind %s", typed.Kind)
		}
		return strconv.Unquote(typed.Value)
	case *ast.Ident:
		if resolving[typed.Name] {
			return "", fmt.Errorf("cyclic same-file string declaration %s", typed.Name)
		}
		resolving[typed.Name] = true
		defer delete(resolving, typed.Name)
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || (general.Tok != token.CONST && general.Tok != token.VAR) {
				continue
			}
			for _, specification := range general.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range value.Names {
					if name.Name == typed.Name && index < len(value.Values) {
						return resolveStringExpr(file, value.Values[index], resolving)
					}
				}
			}
		}
		return "", fmt.Errorf("identifier %s is not a same-file string declaration", typed.Name)
	default:
		return "", fmt.Errorf("unsupported expression %T", expression)
	}
}

func moduleScope(module moduleDeclaration) (string, error) {
	program, err := idl.Parse([]byte(module.raw))
	if err != nil {
		return "", fmt.Errorf("parse embedded IDL: %w", err)
	}
	scope := strings.TrimSuffix(path.Base(module.filePath), path.Ext(module.filePath))
	for _, header := range program.Headers {
		namespace, ok := header.(*thriftast.Namespace)
		if !ok || namespace.Scope != "go" {
			continue
		}
		parts := strings.Split(namespace.Name, ".")
		scope = parts[len(parts)-1]
		break
	}
	if scope == "" || module.name != scope {
		return "", fmt.Errorf("module name %q disagrees with IDL scope %q", module.name, scope)
	}
	return scope, nil
}

func expectedFields(raw string) (map[string]map[int]expectedField, error) {
	program, err := idl.Parse([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parse embedded IDL: %w", err)
	}
	out := make(map[string]map[int]expectedField)
	add := func(owner string, fields []*thriftast.Field, success bool) error {
		values := make(map[int]expectedField, len(fields)+1)
		if success {
			values[0] = expectedField{name: "success", goName: "Success", id: 0}
		}
		ambiguous := false
		for _, field := range fields {
			if field.IDUnset || !validFieldID(field.ID) {
				return fmt.Errorf("%s field %s has invalid or implicit ID %d", owner, field.Name, field.ID)
			}
			if _, duplicate := values[field.ID]; duplicate {
				ambiguous = true
				continue
			}
			values[field.ID] = expectedField{
				name: field.Name, goName: thriftGoName(field.Name, field.Annotations), id: field.ID,
			}
		}
		if !ambiguous {
			out[owner] = values
		}
		return nil
	}
	for _, definition := range program.Definitions {
		switch typed := definition.(type) {
		case *thriftast.Struct:
			if err := add(thriftGoName(typed.Name, typed.Annotations), typed.Fields, false); err != nil {
				return nil, err
			}
		case *thriftast.Service:
			service := thriftGoName(typed.Name, typed.Annotations)
			for _, function := range typed.Functions {
				functionName := thriftGoName(function.Name, function.Annotations)
				if err := add(service+"_"+functionName+"_Args", function.Parameters, false); err != nil {
					return nil, err
				}
				resultFields := append([]*thriftast.Field(nil), function.Exceptions...)
				if err := add(service+"_"+functionName+"_Result", resultFields, function.ReturnType != nil); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

func generatedStructFields(fset *token.FileSet, file *ast.File) map[string][]generatedField {
	out := make(map[string][]generatedField)
	for _, declaration := range file.Decls {
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
			for _, field := range structure.Fields.List {
				if len(field.Names) != 1 {
					continue
				}
				identifier := field.Names[0]
				start, end := nodeSpan(fset, identifier)
				out[typeSpec.Name.Name] = append(out[typeSpec.Name.Name], generatedField{
					name: identifier.Name, start: start, end: end,
				})
			}
		}
	}
	return out
}

func orderedWireFieldIDs(file *ast.File) (map[string][]int, error) {
	out := make(map[string][]int)
	wireAlias := importAliasFor(file, "go.uber.org/thriftrw/wire")
	if wireAlias == "" {
		return out, nil
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "ToWire" || function.Recv == nil {
			continue
		}
		receiver := receiverName(function.Recv)
		if receiver == "" {
			continue
		}
		seen := make(map[int]bool)
		var ids []int
		var parseErr error
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			selector, ok := literal.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Field" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != wireAlias {
				return true
			}
			for _, element := range literal.Elts {
				keyValue, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := keyValue.Key.(*ast.Ident)
				if !ok || key.Name != "ID" {
					continue
				}
				value, ok := keyValue.Value.(*ast.BasicLit)
				if !ok || value.Kind != token.INT {
					parseErr = fmt.Errorf("%s has non-literal wire field ID", receiver)
					return false
				}
				id, err := strconv.Atoi(value.Value)
				if err != nil || seen[id] {
					parseErr = fmt.Errorf("%s has invalid or duplicate wire field ID %q", receiver, value.Value)
					return false
				}
				seen[id] = true
				ids = append(ids, id)
			}
			return parseErr == nil
		})
		if parseErr != nil {
			return nil, parseErr
		}
		if len(ids) > 0 {
			out[receiver] = ids
		}
	}
	return out, nil
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
			dedupeKey := spanKey(start, end) + "\x00" + occurrence.symbol + "\x00" +
				strconv.FormatInt(int64(occurrence.roles), 10)
			if dedupeKey == previous {
				continue
			}
			previous = dedupeKey
			classification := classify(occurrence.roles)
			detail := `{"schema":"thrift-field-reference-detail-v1","name":` +
				strconv.Quote(binding.name) +
				`,"classification":` + strconv.Quote(classification) +
				`,"dependency_version":` + strconv.Quote(binding.dependencyVersion) +
				`,"generator":` + strconv.Quote(binding.generator) +
				`,"source_binding":` + strconv.Quote(binding.sourceBinding) + `}`
			if err := emit(sdk.Fact{
				Atom: sdk.AtomInput{
					SchemaVersion:       schemaVersion,
					BlobDigest:          blob.Digest,
					StartByte:           start,
					EndByte:             end,
					RuleID:              "scip-thrift-field-reference-v1",
					AdapterConfigDigest: adapterConfigDigest,
					FactFingerprint: "REFERENCES_THRIFT_FIELD|" + binding.object + "|" +
						classification,
				},
				Path:      doc.path,
				StartLine: lineAtByte(starts, start),
				EndLine:   lineAtByte(starts, end-1),
				Assertion: sdk.AssertionInput{
					Predicate: "REFERENCES_THRIFT_FIELD",
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

func hasDefinitions(doc indexedDocument) bool {
	for _, occurrence := range doc.occurrences {
		if occurrence.roles&int32(scip.SymbolRole_Definition) != 0 {
			return true
		}
	}
	return false
}

func looksLikeThriftRWModule(content string) bool {
	return strings.Contains(content, `"go.uber.org/thriftrw/thriftreflect"`) &&
		strings.Contains(content, "ThriftModule")
}

func importAliasFor(file *ast.File, importPath string) string {
	for _, specification := range file.Imports {
		value, err := strconv.Unquote(specification.Path.Value)
		if err != nil || value != importPath {
			continue
		}
		if specification.Name == nil {
			return path.Base(importPath)
		}
		if specification.Name.Name == "." || specification.Name.Name == "_" {
			return ""
		}
		return specification.Name.Name
	}
	return ""
}

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
	if len(descriptors) == 0 || descriptors[len(descriptors)-1].GetName() != identifier {
		return "", "", false
	}
	messageMatched := false
	for index := len(descriptors) - 2; index >= 0; index-- {
		if descriptors[index].GetSuffix() != scip.Descriptor_Type {
			continue
		}
		messageMatched = descriptors[index].GetName() == message
		break
	}
	if !messageMatched {
		return "", "", false
	}
	pkg := symbol.GetPackage()
	hash := sha256.Sum256([]byte(
		symbol.GetScheme() + "\x00" + pkg.GetManager() + "\x00" + pkg.GetName(),
	))
	return "contract_scip_package_v1_" + hex.EncodeToString(hash[:]), pkg.GetVersion(), true
}

func validFieldID(id int) bool {
	return id >= minFieldID && id <= maxFieldID
}

func thriftGoName(name string, annotations []*thriftast.Annotation) string {
	for _, annotation := range annotations {
		if annotation.Name == "go.name" && annotation.Value != "" {
			return annotation.Value
		}
	}
	parts := strings.Split(name, "_")
	allowSingleAllCaps := len(parts) == 1
	for index, part := range parts {
		if part == "" {
			continue
		}
		upper := strings.ToUpper(part)
		if thriftInitialisms[upper] {
			parts[index] = upper
			continue
		}
		if !allowSingleAllCaps && allLettersUpper(part) {
			part = strings.ToLower(part)
		}
		first, size := utf8.DecodeRuneInString(part)
		parts[index] = string(unicode.ToUpper(first)) + part[size:]
	}
	return strings.Join(parts, "")
}

func allLettersUpper(value string) bool {
	for _, character := range value {
		if unicode.IsLetter(character) && !unicode.IsUpper(character) {
			return false
		}
	}
	return true
}

var thriftInitialisms = map[string]bool{
	"API": true, "ASCII": true, "CPU": true, "CSS": true, "DNS": true,
	"EOF": true, "GUID": true, "HTML": true, "HTTP": true, "HTTPS": true,
	"ID": true, "IP": true, "JSON": true, "LHS": true, "QPS": true,
	"RAM": true, "RHS": true, "RPC": true, "SLA": true, "SMTP": true,
	"SQL": true, "SSH": true, "TCP": true, "TLS": true, "TTL": true,
	"UDP": true, "UI": true, "UID": true, "UUID": true, "URI": true,
	"URL": true, "UTF8": true, "VM": true, "XML": true, "XSRF": true,
	"XSS": true,
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
		if segment == "mock" || segment == "mocks" ||
			strings.HasPrefix(segment, "mock_") || strings.HasSuffix(segment, "_mock.go") {
			return "mock"
		}
	}
	if roles&int32(scip.SymbolRole_Generated) != 0 || looksLikeGeneratedThriftRW(filePath) {
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

func looksLikeGeneratedThriftRW(filePath string) bool {
	return strings.Contains(filePath, "/.gen/go/") || strings.HasPrefix(filePath, ".gen/go/")
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

func byteSpan(
	content string,
	starts []int,
	rangeValue scip.Range,
	encoding scip.PositionEncoding,
) (int, int, bool) {
	start, ok := positionByte(content, starts, rangeValue.Start, encoding)
	if !ok {
		return 0, 0, false
	}
	end, ok := positionByte(content, starts, rangeValue.End, encoding)
	return start, end, ok && end >= start
}

func positionByte(
	content string,
	starts []int,
	position scip.Position,
	encoding scip.PositionEncoding,
) (int, bool) {
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
	case scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart,
		scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart:
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

func parseSpanKey(value string) (int, int, bool) {
	left, right, ok := strings.Cut(value, ":")
	if !ok {
		return 0, 0, false
	}
	start, err := strconv.Atoi(left)
	if err != nil {
		return 0, 0, false
	}
	end, err := strconv.Atoi(right)
	return start, end, err == nil && start >= 0 && end >= start
}

func validEncoding(encoding scip.PositionEncoding) bool {
	return encoding == scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart ||
		encoding == scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart ||
		encoding == scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart
}

func validPath(value string) error {
	if value == "" || len(value) > 4096 || strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "-") || strings.Contains(value, `\`) ||
		path.Clean(value) != value || !utf8.ValidString(value) {
		return fmt.Errorf("invalid repository path %q", value)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("invalid repository path %q", value)
		}
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("invalid repository path %q", value)
		}
	}
	return nil
}

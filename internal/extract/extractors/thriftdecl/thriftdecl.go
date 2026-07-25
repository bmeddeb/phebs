// Package thriftdecl extracts source-declared Thrift services, operations,
// and message shapes. It parses each bounded regular .thrift blob
// independently and performs no include resolution or cross-file linking.
// Operation request/response shapes are modeled wire-honestly as the implicit
// argument and result structs Thrift serializes (result field 0 is the
// success slot, throws clauses are result fields), emitted as synthetic
// same-file message declarations. Rule decisions D1-D9 are recorded in
// spike/t191/README.md (T19.1).
package thriftdecl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"go.uber.org/thriftrw/ast"
	"go.uber.org/thriftrw/idl"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	domain        = "thrift-contract"
	version       = "1.0.0"
	schemaVersion = "t19-v1"

	// idl.Parse is in-process and does not accept a context. Preflight the
	// source with conservative size, token, and structural-depth ceilings so
	// a single candidate cannot hand it an unbounded working set.
	maxThriftSourceBytes     = 4 << 20
	maxThriftTokens          = 500_000
	maxThriftStructuralDepth = 128
	maxIncludeContextPaths   = 64
	maxIncludeContextBytes   = 4 << 10
	// Same-file typedef chains resolve through at most this many hops before
	// abstaining; cycles abstain immediately.
	maxTypedefChase = 16
	// Explicit Thrift field identifiers are positive i16 values.
	maxThriftFieldID = 32767

	// SHA-256 of the empty adapter configuration, matching protodecl.
	adapterConfigDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// New returns the stateless extractor.
func New() sdk.Extractor { return thriftDecl{} }

type thriftDecl struct{}

func (thriftDecl) Domain() string                 { return domain }
func (thriftDecl) Version() string                { return version }
func (thriftDecl) Candidate(filePath string) bool { return strings.HasSuffix(filePath, ".thrift") }

type serviceDetail struct {
	Schema  string `json:"schema"`
	Name    string `json:"name"`
	Extends string `json:"extends,omitempty"`
}

type messageDetail struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // struct | union | exception
	Synthetic bool   `json:"synthetic,omitempty"`
}

type includeContextDetail struct {
	Count     int      `json:"count"`
	Digest    string   `json:"digest"`
	Paths     []string `json:"paths,omitempty"`
	Truncated bool     `json:"truncated"`
}

type typeReferenceDetail struct {
	Raw         string                `json:"raw"`
	Kind        string                `json:"kind,omitempty"`
	Resolution  string                `json:"resolution"`
	Declaration string                `json:"declaration,omitempty"`
	Reason      string                `json:"reason,omitempty"`
	Imports     *includeContextDetail `json:"imports,omitempty"`
}

type operationDetail struct {
	Schema   string              `json:"schema"`
	Request  typeReferenceDetail `json:"request"`
	Response typeReferenceDetail `json:"response"`
	OneWay   bool                `json:"oneway"`
}

type mapShapeDetail struct {
	Key   typeReferenceDetail `json:"key"`
	Value typeReferenceDetail `json:"value"`
}

// fieldDetail reuses the shared "cardinality" detail key to carry Thrift
// requiredness (required | optional | default) so the catalog's generic
// field projection renders without a parallel schema.
type fieldDetail struct {
	Schema      string               `json:"schema"`
	Name        string               `json:"name"`
	Type        *typeReferenceDetail `json:"type,omitempty"`
	Cardinality string               `json:"cardinality"`
	Map         *mapShapeDetail      `json:"map,omitempty"`
}

type gapDetail struct {
	Schema string `json:"schema"`
	Reason string `json:"reason"`
}

// lineageID is explicitly provisional and file-scoped, byte-identical to the
// protodecl and grpcgo recipes so all provisional lineages share semantics.
func lineageID(repo, thriftPath string) string {
	h := sha256.Sum256([]byte(repo + "\x00" + thriftPath))
	return "provisional_repo_path_v1_" + hex.EncodeToString(h[:])
}

// roleFor is the T11.1 classifier reduced to source paths, matching protodecl.
func roleFor(relPath string) string {
	segments := strings.Split(path.Clean(relPath), "/")
	for _, segment := range segments {
		if segment == "vendor" {
			return "vendor"
		}
	}
	for _, segment := range segments {
		if segment == "tests" || segment == "testdata" {
			return "test"
		}
	}
	return "production"
}

func detailJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal fact detail: %w", err)
	}
	return string(encoded), nil
}

func (thriftDecl) Extract(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	coverage := sdk.Coverage{Protocols: []string{
		"lineage-provisional-repo-path-v1", "thrift",
	}}
	err := corpus.WalkFiles(ctx, func(relPath string) error {
		if !strings.HasSuffix(relPath, ".thrift") {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		blob, err := corpus.Read(ctx, relPath)
		if err != nil {
			return fmt.Errorf("%s: read: %w", relPath, err)
		}
		unresolved, err := fileFacts(ctx, corpus, relPath, blob, emit)
		if err != nil {
			return fmt.Errorf("%s: %w", relPath, err)
		}
		coverage.UnresolvedCount += unresolved
		return nil
	})
	if err != nil {
		// Candidate read/parse failures fail the whole staged run. Returning
		// successful partial coverage would replace known evidence with a
		// subset.
		return sdk.Coverage{}, err
	}
	return coverage, nil
}

// declaredType is one same-file type declaration usable for resolution.
// Enums and typedefs are indexed for resolution but never emitted as
// declarations, matching protodecl's enum posture.
type declaredType struct {
	kind    string // message | enum | typedef
	typedef ast.Type
	count   int
}

// fileState carries the per-file context every emission needs.
type fileState struct {
	relPath  string
	blob     sdk.Blob
	scope    string
	lineage  string
	role     string
	includes includeContextDetail
	types    map[string]*declaredType
	lines    []int // byte offset of each line start
	emit     sdk.Emit
}

// fileFacts extracts one .thrift blob. The returned count is the number of
// tier-unresolved facts emitted (file-level gaps), which the worker
// reconciles against the coverage manifest.
func fileFacts(ctx context.Context, corpus sdk.Corpus, relPath string, blob sdk.Blob, emit sdk.Emit) (int, error) {
	if err := validateThriftComplexity(ctx, blob.Content); err != nil {
		return 0, err
	}
	program, err := idl.Parse([]byte(blob.Content))
	if err != nil {
		return 0, fmt.Errorf("parse: %w", err)
	}
	if program == nil {
		return 0, errors.New("parse returned no program")
	}

	state := &fileState{
		relPath: relPath,
		blob:    blob,
		scope:   scopeFor(program, relPath),
		lineage: lineageID(corpus.RepoName(), relPath),
		role:    roleFor(relPath),
		lines:   lineStarts(blob.Content),
		emit:    emit,
	}
	state.includes = collectIncludeContext(program)
	if err := buildTypeIndex(program, state); err != nil {
		return 0, err
	}

	// Field-identifier admissibility is file-wide: one inadmissible field
	// makes the file's wire shapes unstatable without fabricating identity,
	// so the whole file becomes a single structured gap (D2).
	if gap := findFieldIDGap(program); gap != nil {
		if err := emitFileGap(state, gap.reason, gap.line, gap.column); err != nil {
			return 0, err
		}
		return 1, nil
	}

	for _, definition := range program.Definitions {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		switch node := definition.(type) {
		case *ast.Service:
			if err := emitService(state, node); err != nil {
				return 0, err
			}
		case *ast.Struct:
			if err := emitStructMessage(state, node); err != nil {
				return 0, err
			}
		}
	}
	return 0, nil
}

// scopeFor is decision D1: the last dot-segment of `namespace go` when
// present, then `namespace *`, otherwise the file basename. Thrift defines the
// wildcard namespace as applying to every target language, so treating it as
// absent would give declarations a different scope from generated Go.
func scopeFor(program *ast.Program, relPath string) string {
	wildcard := ""
	for _, header := range program.Headers {
		namespace, ok := header.(*ast.Namespace)
		if !ok || namespace.Name == "" {
			continue
		}
		switch namespace.Scope {
		case "go":
			return lastNamespaceSegment(namespace.Name)
		case "*":
			if wildcard == "" {
				wildcard = namespace.Name
			}
		}
	}
	if wildcard != "" {
		return lastNamespaceSegment(wildcard)
	}
	base := path.Base(relPath)
	return strings.TrimSuffix(base, ".thrift")
}

func lastNamespaceSegment(namespace string) string {
	segments := strings.Split(namespace, ".")
	return segments[len(segments)-1]
}

func collectIncludeContext(program *ast.Program) includeContextDetail {
	var includes []string
	for _, header := range program.Headers {
		if include, ok := header.(*ast.Include); ok {
			includes = append(includes, include.Path)
		}
	}
	sort.Strings(includes)
	hash := sha256.New()
	for _, includePath := range includes {
		_, _ = fmt.Fprintf(hash, "%d:", len(includePath))
		_, _ = hash.Write([]byte(includePath))
	}
	context := includeContextDetail{
		Count:  len(includes),
		Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}
	totalBytes := 0
	for _, includePath := range includes {
		if len(context.Paths) >= maxIncludeContextPaths ||
			totalBytes+len(includePath) > maxIncludeContextBytes {
			context.Truncated = true
			break
		}
		context.Paths = append(context.Paths, includePath)
		totalBytes += len(includePath)
	}
	return context
}

func buildTypeIndex(program *ast.Program, state *fileState) error {
	state.types = map[string]*declaredType{}
	add := func(name, kind string, typedef ast.Type) error {
		if name == "" {
			return fmt.Errorf("%s declaration is missing name", kind)
		}
		if existing, ok := state.types[name]; ok {
			existing.count++
			return nil
		}
		state.types[name] = &declaredType{kind: kind, typedef: typedef, count: 1}
		return nil
	}
	for _, definition := range program.Definitions {
		var err error
		switch node := definition.(type) {
		case *ast.Struct:
			err = add(node.Name, "message", nil)
		case *ast.Enum:
			err = add(node.Name, "enum", nil)
		case *ast.Typedef:
			err = add(node.Name, "typedef", node.Type)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type fieldIDGap struct {
	reason string
	line   int
	column int
}

// findFieldIDGap scans every field position in the file (struct fields,
// function parameters, throws clauses) for identifiers the pack refuses to
// model: implicit IDs (Thrift assigns negative wire IDs; emitting a guess
// would fabricate identity) and out-of-range explicit IDs.
func findFieldIDGap(program *ast.Program) *fieldIDGap {
	check := func(fields []*ast.Field) *fieldIDGap {
		for _, field := range fields {
			if field.IDUnset {
				return &fieldIDGap{reason: "implicit_field_id", line: field.Line, column: field.Column}
			}
			if field.ID < 1 || field.ID > maxThriftFieldID {
				return &fieldIDGap{reason: "invalid_field_id", line: field.Line, column: field.Column}
			}
		}
		return nil
	}
	for _, definition := range program.Definitions {
		switch node := definition.(type) {
		case *ast.Struct:
			if gap := check(node.Fields); gap != nil {
				return gap
			}
		case *ast.Service:
			for _, function := range node.Functions {
				if gap := check(function.Parameters); gap != nil {
					return gap
				}
				if gap := check(function.Exceptions); gap != nil {
					return gap
				}
			}
		}
	}
	return nil
}

// span converts a 1-based thriftrw position into the byte span from the node
// start to the end of its source line. thriftrw records no end positions or
// raw text, so the declaration-start line is the exact, verifiable locator
// this pack cites (multi-line declarations cite their first line).
func (s *fileState) span(line, column int) (startByte, endByte, startLine int, err error) {
	if line < 1 || line > len(s.lines) || column < 1 {
		return 0, 0, 0, fmt.Errorf("invalid parser position %d:%d", line, column)
	}
	lineStart := s.lines[line-1]
	lineEnd := len(s.blob.Content)
	if line < len(s.lines) {
		lineEnd = s.lines[line] - 1 // exclude the newline itself
	}
	startByte = lineStart + column - 1
	if startByte >= lineEnd {
		return 0, 0, 0, fmt.Errorf("parser position %d:%d beyond line end", line, column)
	}
	return startByte, lineEnd, line, nil
}

func (s *fileState) emitFact(predicate, object, rule, detail string, line, column int) error {
	startByte, endByte, startLine, err := s.span(line, column)
	if err != nil {
		return fmt.Errorf("span for %s: %w", object, err)
	}
	tier, lineage := "exact", s.lineage
	if predicate == "THRIFT_EXTRACTION_GAP" {
		tier, lineage = "unresolved", ""
	}
	return s.emit(sdk.Fact{
		Atom: sdk.AtomInput{
			SchemaVersion: schemaVersion, BlobDigest: s.blob.Digest,
			StartByte: startByte, EndByte: endByte, RuleID: rule,
			AdapterConfigDigest: adapterConfigDigest,
			// Lineage is placement-derived and must not enter content
			// identity: identical vendored blobs share atoms across repos.
			FactFingerprint: predicate + "|" + object,
		},
		Path: s.relPath, StartLine: startLine, EndLine: startLine,
		Assertion: sdk.AssertionInput{
			Predicate: predicate, Subject: s.relPath, Object: object,
			Lineage: lineage, Tier: tier, CodeRole: s.role, Detail: detail,
		},
	})
}

func emitFileGap(state *fileState, reason string, line, column int) error {
	detail, err := detailJSON(gapDetail{Schema: "thrift-gap-detail-v1", Reason: reason})
	if err != nil {
		return err
	}
	return state.emitFact("THRIFT_EXTRACTION_GAP", reason, "thrift-file-gap-v1", detail, line, column)
}

func emitService(state *fileState, service *ast.Service) error {
	if service.Name == "" {
		return errors.New("service is missing name")
	}
	serviceObject := state.scope + "." + service.Name
	detail := serviceDetail{Schema: "thrift-service-detail-v1", Name: service.Name}
	if service.Parent != nil {
		detail.Extends = service.Parent.Name
	}
	encoded, err := detailJSON(detail)
	if err != nil {
		return err
	}
	if err := state.emitFact(
		"DECLARES_SERVICE", serviceObject, "thrift-service-v1", encoded,
		service.Line, service.Column,
	); err != nil {
		return err
	}
	for _, function := range service.Functions {
		if err := emitOperation(state, service, function); err != nil {
			return err
		}
	}
	return nil
}

func emitOperation(state *fileState, service *ast.Service, function *ast.Function) error {
	if function.Name == "" {
		return fmt.Errorf("service %s function is missing name", service.Name)
	}
	argsName := service.Name + "." + function.Name + "_args"
	request := typeReferenceDetail{
		Raw: argsName, Kind: "message", Resolution: "same_file",
		Declaration: state.scope + "." + argsName,
	}
	if err := emitSyntheticMessage(
		state, request.Declaration, function.Name+"_args", function,
	); err != nil {
		return err
	}
	for _, parameter := range function.Parameters {
		if err := emitField(state, request.Declaration, parameter); err != nil {
			return err
		}
	}

	response := typeReferenceDetail{Raw: "void", Kind: "scalar", Resolution: "intrinsic"}
	if !function.OneWay {
		resultName := service.Name + "." + function.Name + "_result"
		response = typeReferenceDetail{
			Raw: resultName, Kind: "message", Resolution: "same_file",
			Declaration: state.scope + "." + resultName,
		}
		if err := emitSyntheticMessage(
			state, response.Declaration, function.Name+"_result", function,
		); err != nil {
			return err
		}
		if function.ReturnType != nil {
			success := fieldDetail{
				Schema: "thrift-field-detail-v1", Name: "success", Cardinality: "default",
			}
			resolveInto(state, &success, function.ReturnType)
			encoded, err := detailJSON(success)
			if err != nil {
				return err
			}
			if err := state.emitFact(
				"DECLARES_FIELD", response.Declaration+"#0", "thrift-field-v1",
				encoded, function.Line, function.Column,
			); err != nil {
				return err
			}
		}
		for _, exception := range function.Exceptions {
			if err := emitField(state, response.Declaration, exception); err != nil {
				return err
			}
		}
	}

	operation := operationDetail{
		Schema: "thrift-operation-detail-v1", Request: request,
		Response: response, OneWay: function.OneWay,
	}
	encoded, err := detailJSON(operation)
	if err != nil {
		return err
	}
	return state.emitFact(
		"DECLARES_OPERATION",
		state.scope+"."+service.Name+"/"+function.Name,
		"thrift-rpc-v1", encoded, function.Line, function.Column,
	)
}

// emitSyntheticMessage declares the wire-implicit argument or result struct
// of one function, spanned to the declaring function line.
func emitSyntheticMessage(state *fileState, object, shortName string, function *ast.Function) error {
	encoded, err := detailJSON(messageDetail{
		Schema: "thrift-message-detail-v1", Name: shortName,
		Kind: "struct", Synthetic: true,
	})
	if err != nil {
		return err
	}
	return state.emitFact(
		"DECLARES_MESSAGE", object, "thrift-message-v1", encoded,
		function.Line, function.Column,
	)
}

func emitStructMessage(state *fileState, node *ast.Struct) error {
	kind := "struct"
	switch node.Type {
	case ast.UnionType:
		kind = "union"
	case ast.ExceptionType:
		kind = "exception"
	}
	object := state.scope + "." + node.Name
	encoded, err := detailJSON(messageDetail{
		Schema: "thrift-message-detail-v1", Name: node.Name, Kind: kind,
	})
	if err != nil {
		return err
	}
	if err := state.emitFact(
		"DECLARES_MESSAGE", object, "thrift-message-v1", encoded,
		node.Line, node.Column,
	); err != nil {
		return err
	}
	for _, field := range node.Fields {
		if err := emitField(state, object, field); err != nil {
			return err
		}
	}
	return nil
}

func emitField(state *fileState, messageObject string, field *ast.Field) error {
	if field.Name == "" {
		return fmt.Errorf("field %d in %s is missing name", field.ID, messageObject)
	}
	detail := fieldDetail{
		Schema: "thrift-field-detail-v1", Name: field.Name,
		Cardinality: requirednessLabel(field.Requiredness),
	}
	resolveInto(state, &detail, field.Type)
	encoded, err := detailJSON(detail)
	if err != nil {
		return err
	}
	return state.emitFact(
		"DECLARES_FIELD",
		fmt.Sprintf("%s#%d", messageObject, field.ID),
		"thrift-field-v1", encoded, field.Line, field.Column,
	)
}

func requirednessLabel(requiredness ast.Requiredness) string {
	switch requiredness {
	case ast.Required:
		return "required"
	case ast.Optional:
		return "optional"
	default:
		return "default"
	}
}

// resolveInto fills the field detail's Type or Map from an AST type. Maps use
// the shared map shape; list/set references resolve their element type while
// Raw preserves the container form as written.
func resolveInto(state *fileState, detail *fieldDetail, fieldType ast.Type) {
	if mapType, ok := fieldType.(ast.MapType); ok {
		detail.Map = &mapShapeDetail{
			Key:   resolveType(state, mapType.KeyType, 0),
			Value: resolveType(state, mapType.ValueType, 0),
		}
		return
	}
	reference := resolveType(state, fieldType, 0)
	detail.Type = &reference
}

// resolveType is decision D8: same-file resolution only. Base types are
// intrinsic; unqualified names bind same-file structs/unions/exceptions and
// enums, chasing same-file typedefs to a bounded depth; include-qualified
// names stay unresolved with the include context attached; containers
// resolve their element type with the container form preserved in Raw.
func resolveType(state *fileState, fieldType ast.Type, typedefDepth int) typeReferenceDetail {
	switch node := fieldType.(type) {
	case ast.BaseType:
		return typeReferenceDetail{Raw: node.String(), Kind: "scalar", Resolution: "intrinsic"}
	case ast.ListType:
		return containerReference(state, "list", node.ValueType, typedefDepth)
	case ast.SetType:
		return containerReference(state, "set", node.ValueType, typedefDepth)
	case ast.MapType:
		// Reached only for nested containers (map<k, list<...>> handles its
		// own elements; a map directly under a container abstains).
		return typeReferenceDetail{
			Raw: node.String(), Kind: "container", Resolution: "unresolved",
			Reason: "NESTED_CONTAINER_SHAPE",
		}
	case ast.TypeReference:
		return referenceByName(state, node.Name, typedefDepth)
	}
	return typeReferenceDetail{
		Raw: fmt.Sprintf("%v", fieldType), Resolution: "unresolved",
		Reason: "UNSUPPORTED_TYPE_FORM",
	}
}

func containerReference(state *fileState, form string, element ast.Type, typedefDepth int) typeReferenceDetail {
	switch element.(type) {
	case ast.ListType, ast.SetType, ast.MapType:
		// Single-level unwrap only; deeper nesting abstains (known ceiling,
		// D8). The raw form still records what was written.
		return typeReferenceDetail{
			Raw: form + "<" + element.String() + ">", Kind: "container",
			Resolution: "unresolved", Reason: "NESTED_CONTAINER_SHAPE",
		}
	}
	reference := resolveType(state, element, typedefDepth)
	reference.Raw = form + "<" + reference.Raw + ">"
	return reference
}

func referenceByName(state *fileState, name string, typedefDepth int) typeReferenceDetail {
	reference := typeReferenceDetail{Raw: name}
	if strings.Contains(name, ".") {
		reference.Resolution = "unresolved"
		reference.Reason = "IMPORT_LINKING_UNAVAILABLE"
		includes := state.includes
		reference.Imports = &includes
		return reference
	}
	declared, ok := state.types[name]
	if !ok {
		reference.Resolution = "unresolved"
		reference.Reason = "DECLARATION_NOT_FOUND"
		return reference
	}
	if declared.count > 1 {
		reference.Resolution = "unresolved"
		reference.Reason = "AMBIGUOUS_SAME_FILE_DECLARATION"
		return reference
	}
	if declared.kind == "typedef" {
		if typedefDepth >= maxTypedefChase {
			reference.Resolution = "unresolved"
			reference.Reason = "TYPEDEF_CHAIN_LIMIT"
			return reference
		}
		resolved := resolveType(state, declared.typedef, typedefDepth+1)
		resolved.Raw = name
		return resolved
	}
	reference.Kind = declared.kind
	reference.Resolution = "same_file"
	reference.Declaration = state.scope + "." + name
	return reference
}

// validateThriftComplexity preflights source before the context-free
// idl.Parse call, mirroring protodecl's posture: bounded bytes, bounded token
// count, bounded structural depth, and rejection of unterminated block
// comments and string literals. Thrift adds '#' line comments and
// single-quoted strings to the protobuf lexical shape.
func validateThriftComplexity(ctx context.Context, source string) error {
	if len(source) > maxThriftSourceBytes {
		return fmt.Errorf("source exceeds %d-byte thrift parser limit", maxThriftSourceBytes)
	}
	depth, tokens := 0, 0
	addToken := func() error {
		tokens++
		if tokens > maxThriftTokens {
			return fmt.Errorf("source exceeds %d-token thrift parser limit", maxThriftTokens)
		}
		return nil
	}
	for i := 0; i < len(source); {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		current := source[i]
		switch {
		case current == ' ' || current == '\t' || current == '\r' || current == '\n':
			i++
		case current == '#':
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case current == '/' && i+1 < len(source) && source[i+1] == '/':
			i += 2
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case current == '/' && i+1 < len(source) && source[i+1] == '*':
			i += 2
			closed := false
			for i+1 < len(source) {
				if i&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if source[i] == '*' && source[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return errors.New("unterminated thrift block comment")
			}
		case current == '\'' || current == '"':
			if err := addToken(); err != nil {
				return err
			}
			quote := current
			i++
			closed := false
			for i < len(source) {
				if i&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				switch source[i] {
				case '\\':
					i += 2
				case quote:
					i++
					closed = true
				default:
					i++
				}
				if closed {
					break
				}
			}
			if !closed {
				return errors.New("unterminated thrift string literal")
			}
		case current == '{' || current == '<':
			if err := addToken(); err != nil {
				return err
			}
			depth++
			if depth > maxThriftStructuralDepth {
				return fmt.Errorf("source exceeds %d-level thrift nesting limit", maxThriftStructuralDepth)
			}
			i++
		case current == '}' || current == '>':
			if err := addToken(); err != nil {
				return err
			}
			if depth > 0 {
				depth--
			}
			i++
		case isThriftWordByte(current):
			if err := addToken(); err != nil {
				return err
			}
			for i < len(source) && isThriftWordByte(source[i]) {
				i++
			}
		default:
			if err := addToken(); err != nil {
				return err
			}
			i++
		}
	}
	return nil
}

func isThriftWordByte(b byte) bool {
	return b == '_' || b == '.' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func lineStarts(content string) []int {
	starts := []int{0}
	for offset := 0; offset < len(content); offset++ {
		if content[offset] == '\n' {
			starts = append(starts, offset+1)
		}
	}
	return starts
}

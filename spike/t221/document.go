package t221

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // thriftrw embeds SHA1 as descriptor identity.
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	thriftast "go.uber.org/thriftrw/ast"
	"go.uber.org/thriftrw/idl"
)

const maxGeneratedDocumentBytes = 4 << 20

// DocumentResolver is the reviewed generated-document boundary used by the
// spike join. An eligible document carries exact generated identifier spans
// and their independently recovered Thrift wire identities.
type DocumentResolver func(rel string) (DocumentModel, bool, error)

type DocumentModel struct {
	Family      string
	definitions map[string]FieldDefinition
}

type FieldDefinition struct {
	Identifier    string
	Scope         string
	Message       string
	FieldName     string
	FieldID       int
	SourceBinding string
}

func (m DocumentModel) DefinitionAt(start, end int) (FieldDefinition, bool) {
	value, ok := m.definitions[spanKey(start, end)]
	return value, ok
}

// CorpusDocumentResolver returns a resolver fixed to one corpus root. It
// first recognizes a candidate from immutable bytes, then parses it. Parser
// and identity failures on candidates are errors rather than silent
// ineligibility.
func CorpusDocumentResolver(corpusDir string) DocumentResolver {
	return func(rel string) (DocumentModel, bool, error) {
		full := filepath.Join(corpusDir, filepath.FromSlash(rel))
		content, err := os.ReadFile(full)
		if err != nil {
			return DocumentModel{}, false, fmt.Errorf("read: %w", err)
		}
		if len(content) > maxGeneratedDocumentBytes {
			if bytes.Contains(content, []byte("ThriftModule")) ||
				hasApacheMarkerBytes(content) {
				return DocumentModel{}, false, fmt.Errorf("generated candidate exceeds %d-byte limit", maxGeneratedDocumentBytes)
			}
			return DocumentModel{}, false, nil
		}
		if bytes.Contains(content, []byte("ThriftModule")) {
			module, found, err := ParseThriftModuleFile(full)
			if err != nil {
				return DocumentModel{}, false, err
			}
			if found {
				model, err := resolveThriftRWDocument(full, content, module)
				return model, err == nil, err
			}
		}
		if !hasApacheMarkerBytes(content) {
			return DocumentModel{}, false, nil
		}
		tags, err := ParseThriftTags(full)
		if err != nil {
			return DocumentModel{}, false, err
		}
		if len(tags) == 0 {
			return DocumentModel{}, false, nil
		}
		model, err := resolveApacheDocument(full, tags)
		return model, err == nil, err
	}
}

func hasApacheMarkerBytes(content []byte) bool {
	if len(content) > 4096 {
		content = content[:4096]
	}
	for _, rawLine := range bytes.Split(content, []byte{'\n'}) {
		line := strings.TrimSpace(strings.TrimSuffix(string(rawLine), "\r"))
		for _, marker := range apacheGeneratedMarkers {
			// Line-anchored prefix: the generator appends its version
			// ("... Thrift Compiler (0.14.1). DO NOT EDIT."), so exact
			// equality would make every real header ineligible.
			if strings.HasPrefix(line, marker) {
				return true
			}
		}
	}
	return false
}

func resolveApacheDocument(path string, tags []TaggedField) (DocumentModel, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return DocumentModel{}, fmt.Errorf("parse Apache generated Go: %w", err)
	}
	if file.Name == nil || file.Name.Name == "" {
		return DocumentModel{}, fmt.Errorf("apache generated Go has no package name")
	}
	definitions := make(map[string]FieldDefinition, len(tags))
	for _, tag := range tags {
		if !ValidThriftFieldID(tag.ID) || tag.ThriftName == "" {
			return DocumentModel{}, fmt.Errorf("invalid Apache field identity %s.%s#%d", tag.Struct, tag.ThriftName, tag.ID)
		}
		key := spanKey(tag.StartByte, tag.EndByte)
		if _, duplicate := definitions[key]; duplicate {
			return DocumentModel{}, fmt.Errorf("duplicate Apache field span %s", key)
		}
		definitions[key] = FieldDefinition{
			Identifier:    tag.GoName,
			Scope:         file.Name.Name,
			Message:       tag.Struct,
			FieldName:     tag.ThriftName,
			FieldID:       tag.ID,
			SourceBinding: "none",
		}
	}
	return DocumentModel{Family: "apache", definitions: definitions}, nil
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

func resolveThriftRWDocument(path string, content []byte, module ThriftModuleDecl) (DocumentModel, error) {
	sum := sha1.Sum([]byte(module.Raw)) //nolint:gosec // descriptor identity, not security.
	if got := hex.EncodeToString(sum[:]); got != module.SHA1 {
		return DocumentModel{}, fmt.Errorf("ThriftModule sha1(Raw) %s does not match %s", got, module.SHA1)
	}
	scope, err := moduleScope(module)
	if err != nil {
		return DocumentModel{}, err
	}
	expected, err := expectedThriftRWFields(module.Raw)
	if err != nil {
		return DocumentModel{}, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err != nil {
		return DocumentModel{}, fmt.Errorf("parse thriftrw generated Go: %w", err)
	}
	structs := generatedStructFields(fset, file)
	wireIDs, err := orderedWireFieldIDs(file)
	if err != nil {
		return DocumentModel{}, err
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
			if !ok || expectedField.goName != field.name || !ValidThriftFieldID(id) {
				valid = false
				break
			}
			field.id = id
			resolved[field.name] = field
		}
		if valid {
			fieldsByType[message] = resolved
		}
	}

	definitions := make(map[string]FieldDefinition)
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
		definitions[key] = FieldDefinition{
			Identifier:    identifier,
			Scope:         scope,
			Message:       message,
			FieldName:     want.name,
			FieldID:       field.id,
			SourceBinding: "module_digest",
		}
		return nil
	}
	for message, fields := range fieldsByType {
		for _, field := range fields {
			if err := add(field.start, field.end, field.name, message, field.name); err != nil {
				return DocumentModel{}, err
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
		start := fset.PositionFor(function.Name.Pos(), false).Offset
		end := fset.PositionFor(function.Name.End(), false).Offset
		if err := add(start, end, function.Name.Name, message, goField); err != nil {
			return DocumentModel{}, err
		}
	}
	return DocumentModel{Family: "thriftrw", definitions: definitions}, nil
}

func moduleScope(module ThriftModuleDecl) (string, error) {
	namespace, found, err := IDLGoNamespace(module.Raw)
	if err != nil {
		return "", err
	}
	scope := strings.TrimSuffix(filepath.Base(module.FilePath), filepath.Ext(module.FilePath))
	if found {
		parts := strings.Split(namespace, ".")
		scope = parts[len(parts)-1]
	}
	if scope == "" || module.Name != scope {
		return "", fmt.Errorf("module name %q disagrees with IDL scope %q", module.Name, scope)
	}
	return scope, nil
}

func expectedThriftRWFields(raw string) (map[string]map[int]expectedField, error) {
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
		for _, field := range fields {
			if field.IDUnset || !ValidThriftFieldID(field.ID) {
				return fmt.Errorf("%s field %s has invalid or implicit ID %d", owner, field.Name, field.ID)
			}
			if _, duplicate := values[field.ID]; duplicate {
				return fmt.Errorf("%s has duplicate field ID %d", owner, field.ID)
			}
			values[field.ID] = expectedField{
				name:   field.Name,
				goName: thriftGoName(field.Name, field.Annotations),
				id:     field.ID,
			}
		}
		out[owner] = values
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
				out[typeSpec.Name.Name] = append(out[typeSpec.Name.Name], generatedField{
					name:  identifier.Name,
					start: fset.PositionFor(identifier.Pos(), false).Offset,
					end:   fset.PositionFor(identifier.End(), false).Offset,
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
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "ID" {
					continue
				}
				value, ok := kv.Value.(*ast.BasicLit)
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

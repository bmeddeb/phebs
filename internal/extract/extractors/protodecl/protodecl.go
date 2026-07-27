// Package protodecl extracts source-declared protobuf services, messages,
// operations, and field shapes. It parses each bounded regular .proto blob
// independently and performs no import resolution or cross-file linking.
package protodecl

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

	"github.com/bufbuild/protocompile/ast"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/idlpreflight"
)

const (
	domain        = "proto-contract"
	version       = "3.0.0"
	schemaVersion = "t17-v1"

	// parser.Parse is in-process and does not accept a context. Preflight the
	// source with conservative size, token, and structural-depth ceilings so a
	// single candidate cannot hand it an unbounded working set or recursive
	// grammar shape. The worker deadline remains a cooperative outer deadline.
	maxProtoSourceBytes   = idlpreflight.MaxFileBytes
	maxImportContextPaths = 64
	maxImportContextBytes = 4 << 10

	// SHA-256 of the empty adapter configuration. A real digest, rather than a
	// sentinel word, keeps atom provenance machine-verifiable.
	adapterConfigDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// New returns the stateless extractor.
func New() sdk.Extractor { return protoDecl{} }

type protoDecl struct{}

func (protoDecl) Domain() string                 { return domain }
func (protoDecl) Version() string                { return version }
func (protoDecl) Candidate(filePath string) bool { return strings.HasSuffix(filePath, ".proto") }

type declarationKind string

const (
	declarationMessage declarationKind = "message"
	declarationEnum    declarationKind = "enum"
)

type namedDeclaration struct {
	fullName string
	kind     declarationKind
}

type declarationIndex map[string][]namedDeclaration

type declarationDetail struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

type importContextDetail struct {
	Count     int      `json:"count"`
	Digest    string   `json:"digest"`
	Paths     []string `json:"paths,omitempty"`
	Truncated bool     `json:"truncated"`
}

type typeReferenceDetail struct {
	Raw         string               `json:"raw"`
	Kind        string               `json:"kind,omitempty"`
	Resolution  string               `json:"resolution"`
	Declaration string               `json:"declaration,omitempty"`
	Reason      string               `json:"reason,omitempty"`
	Imports     *importContextDetail `json:"imports,omitempty"`
}

type operationDetail struct {
	Schema          string              `json:"schema"`
	Request         typeReferenceDetail `json:"request"`
	Response        typeReferenceDetail `json:"response"`
	ClientStreaming bool                `json:"client_streaming"`
	ServerStreaming bool                `json:"server_streaming"`
}

type mapShapeDetail struct {
	Key   typeReferenceDetail `json:"key"`
	Value typeReferenceDetail `json:"value"`
}

type fieldDetail struct {
	Schema      string               `json:"schema"`
	Name        string               `json:"name"`
	Type        *typeReferenceDetail `json:"type,omitempty"`
	Cardinality string               `json:"cardinality"`
	Map         *mapShapeDetail      `json:"map,omitempty"`
	Oneof       string               `json:"oneof,omitempty"`
}

// lineageID is explicitly provisional and file-scoped. Parser-only extraction
// cannot prove that same-directory files belong to one canonical descriptor
// set; separating them avoids false merges for mutually-exclusive roots with
// duplicate FQNs. The prefix and coverage protocol make that limitation
// machine-visible until trusted module/import-root identity is available.
func lineageID(repo, protoPath string) string {
	h := sha256.Sum256([]byte(repo + "\x00" + protoPath))
	return "provisional_repo_path_v1_" + hex.EncodeToString(h[:])
}

func joinFullName(scope, name string) string {
	name = strings.TrimPrefix(name, ".")
	if scope == "" {
		return name
	}
	return scope + "." + name
}

func detailJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal fact detail: %w", err)
	}
	return string(encoded), nil
}

func collectFileContext(fileNode *ast.FileNode) (string, importContextDetail, error) {
	var pkg string
	var imports []string
	for _, decl := range fileNode.Decls {
		switch node := decl.(type) {
		case *ast.PackageNode:
			if node.Name == nil {
				return "", importContextDetail{}, errors.New("package is missing name")
			}
			if pkg == "" {
				pkg = string(node.Name.AsIdentifier())
			}
		case *ast.ImportNode:
			if node.Name == nil {
				return "", importContextDetail{}, errors.New("import is missing path")
			}
			imports = append(imports, node.Name.AsString())
		}
	}
	sort.Strings(imports)

	hash := sha256.New()
	for _, importPath := range imports {
		_, _ = fmt.Fprintf(hash, "%d:", len(importPath))
		_, _ = hash.Write([]byte(importPath))
	}
	context := importContextDetail{
		Count:  len(imports),
		Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}
	totalBytes := 0
	for _, importPath := range imports {
		if len(context.Paths) >= maxImportContextPaths ||
			totalBytes+len(importPath) > maxImportContextBytes {
			context.Truncated = true
			break
		}
		context.Paths = append(context.Paths, importPath)
		totalBytes += len(importPath)
	}
	return pkg, context, nil
}

func buildDeclarationIndex(pkg string, fileNode *ast.FileNode) (declarationIndex, error) {
	index := declarationIndex{}
	add := func(scope, name string, kind declarationKind) (string, error) {
		if name == "" {
			return "", fmt.Errorf("%s declaration is missing name", kind)
		}
		fullName := joinFullName(scope, name)
		index[fullName] = append(index[fullName], namedDeclaration{
			fullName: fullName,
			kind:     kind,
		})
		return fullName, nil
	}

	var collectBody func(string, []ast.MessageElement) error
	var collectMessage func(string, *ast.MessageNode) error
	var collectGroup func(string, *ast.GroupNode) error

	collectMessage = func(scope string, node *ast.MessageNode) error {
		if node.Name == nil {
			return errors.New("message is missing name")
		}
		fullName, err := add(scope, string(node.Name.AsIdentifier()), declarationMessage)
		if err != nil {
			return err
		}
		return collectBody(fullName, node.Decls)
	}
	collectGroup = func(scope string, node *ast.GroupNode) error {
		if node.Name == nil {
			return errors.New("group is missing name")
		}
		fullName, err := add(scope, string(node.Name.AsIdentifier()), declarationMessage)
		if err != nil {
			return err
		}
		return collectBody(fullName, node.Decls)
	}
	collectBody = func(scope string, declarations []ast.MessageElement) error {
		for _, element := range declarations {
			switch node := element.(type) {
			case *ast.MessageNode:
				if err := collectMessage(scope, node); err != nil {
					return err
				}
			case *ast.EnumNode:
				if node.Name == nil {
					return errors.New("enum is missing name")
				}
				if _, err := add(scope, string(node.Name.AsIdentifier()), declarationEnum); err != nil {
					return err
				}
			case *ast.GroupNode:
				if err := collectGroup(scope, node); err != nil {
					return err
				}
			case *ast.OneofNode:
				for _, oneofElement := range node.Decls {
					if group, ok := oneofElement.(*ast.GroupNode); ok {
						if err := collectGroup(scope, group); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	}

	for _, decl := range fileNode.Decls {
		switch node := decl.(type) {
		case *ast.MessageNode:
			if err := collectMessage(pkg, node); err != nil {
				return nil, err
			}
		case *ast.EnumNode:
			if node.Name == nil {
				return nil, errors.New("enum is missing name")
			}
			if _, err := add(pkg, string(node.Name.AsIdentifier()), declarationEnum); err != nil {
				return nil, err
			}
		}
	}
	return index, nil
}

func scalarType(raw string) bool {
	switch raw {
	case "double", "float",
		"int32", "int64", "uint32", "uint64", "sint32", "sint64",
		"fixed32", "fixed64", "sfixed32", "sfixed64",
		"bool", "string", "bytes":
		return true
	default:
		return false
	}
}

func lexicalCandidates(scope, raw string) []string {
	absolute := strings.HasPrefix(raw, ".")
	name := strings.TrimPrefix(raw, ".")
	if absolute || scope == "" {
		return []string{name}
	}
	parts := strings.Split(scope, ".")
	candidates := make([]string, 0, len(parts)+1)
	seen := map[string]bool{}
	for index := len(parts); index >= 0; index-- {
		candidate := joinFullName(strings.Join(parts[:index], "."), name)
		if !seen[candidate] {
			candidates = append(candidates, candidate)
			seen[candidate] = true
		}
	}
	return candidates
}

func resolveTypeReference(
	raw, scope string,
	index declarationIndex,
	imports importContextDetail,
	messageOnly bool,
) typeReferenceDetail {
	if scalarType(raw) {
		return typeReferenceDetail{
			Raw: raw, Kind: "scalar", Resolution: "intrinsic",
		}
	}
	for _, candidate := range lexicalCandidates(scope, raw) {
		declarations := index[candidate]
		if len(declarations) == 0 {
			continue
		}
		if len(declarations) != 1 {
			return typeReferenceDetail{
				Raw: raw, Resolution: "unresolved",
				Reason: "AMBIGUOUS_SAME_FILE_DECLARATION",
			}
		}
		declaration := declarations[0]
		if messageOnly && declaration.kind != declarationMessage {
			return typeReferenceDetail{
				Raw: raw, Kind: string(declaration.kind), Resolution: "unresolved",
				Declaration: declaration.fullName, Reason: "INVALID_DECLARATION_KIND",
			}
		}
		return typeReferenceDetail{
			Raw: raw, Kind: string(declaration.kind), Resolution: "same_file",
			Declaration: declaration.fullName,
		}
	}

	reason := "DECLARATION_NOT_FOUND"
	if imports.Count > 0 {
		reason = "IMPORT_LINKING_UNAVAILABLE"
	}
	return typeReferenceDetail{
		Raw: raw, Resolution: "unresolved", Reason: reason, Imports: &imports,
	}
}

func fieldCardinality(label ast.FieldLabel) string {
	switch {
	case label.Repeated:
		return "repeated"
	case label.Required:
		return "required"
	case label.IsPresent():
		return "optional"
	default:
		return "singular"
	}
}

func (protoDecl) Extract(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	coverage := sdk.Coverage{Protocols: []string{
		"protobuf", "lineage-provisional-repo-path-v1",
	}}
	err := corpus.WalkFiles(ctx, func(relPath string) error {
		if !strings.HasSuffix(relPath, ".proto") {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		blob, err := corpus.Read(ctx, relPath)
		if err != nil {
			return fmt.Errorf("%s: read: %w", relPath, err)
		}
		if err := fileFacts(ctx, corpus, relPath, blob, emit); err != nil {
			return fmt.Errorf("%s: %w", relPath, err)
		}
		return nil
	})
	if err != nil {
		// Candidate read/parse/tree failures fail the whole staged run. Returning
		// successful partial coverage would replace known evidence with a subset.
		return sdk.Coverage{}, err
	}
	return coverage, nil
}

func fileFacts(ctx context.Context, corpus sdk.Corpus, relPath string, blob sdk.Blob, emit sdk.Emit) error {
	if err := validateProtoComplexity(ctx, blob.Content); err != nil {
		return err
	}
	handler := reporter.NewHandler(nil)
	fileNode, err := parser.Parse(relPath, strings.NewReader(blob.Content), handler)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if fileNode == nil {
		return errors.New("parse returned no file")
	}

	// Package/import context and same-file declarations are semantically
	// file-wide. Collect derived names before emitting facts so unusual but
	// parseable declaration order cannot change identities or resolution.
	pkg, imports, err := collectFileContext(fileNode)
	if err != nil {
		return err
	}
	declarations, err := buildDeclarationIndex(pkg, fileNode)
	if err != nil {
		return err
	}

	lineage := lineageID(corpus.RepoName(), relPath)
	role := roleFor(relPath)
	emitNode := func(predicate, object, rule, detail string, node ast.Node) error {
		info := fileNode.NodeInfo(node)
		start, raw := info.Start(), info.RawText()
		// protocompile v0.14.1 documents End as exclusive, but End().Offset
		// identifies the final byte of the node (only End().Col is advanced).
		// Derive the half-open end from the documented exact RawText instead, so
		// byte spans remain correct if that upstream inconsistency is fixed.
		if start.Offset < 0 || start.Offset >= len(blob.Content) || len(raw) == 0 ||
			len(raw) > len(blob.Content)-start.Offset || start.Line <= 0 {
			return fmt.Errorf("invalid parser span for %s", object)
		}
		endByte := start.Offset + len(raw)
		if blob.Content[start.Offset:endByte] != raw {
			return fmt.Errorf("parser text does not match source span for %s", object)
		}
		endLine := start.Line + strings.Count(raw[:len(raw)-1], "\n")
		return emit(sdk.Fact{
			Atom: sdk.AtomInput{
				SchemaVersion: schemaVersion, BlobDigest: blob.Digest,
				StartByte: start.Offset, EndByte: endByte, RuleID: rule,
				AdapterConfigDigest: adapterConfigDigest,
				// Lineage is placement-derived and must not enter content identity:
				// identical vendored blobs in different repositories share atoms.
				FactFingerprint: predicate + "|" + object,
			},
			Path: relPath, StartLine: start.Line, EndLine: endLine,
			Assertion: sdk.AssertionInput{
				Predicate: predicate, Subject: relPath, Object: object,
				Lineage: lineage, Tier: "exact", CodeRole: role, Detail: detail,
			},
		})
	}

	emitDeclaration := func(
		predicate, object, rule, schema, name string,
		node ast.Node,
	) error {
		detail, err := detailJSON(declarationDetail{Schema: schema, Name: name})
		if err != nil {
			return err
		}
		return emitNode(predicate, object, rule, detail, node)
	}
	emitField := func(
		messageFullName, name string,
		tag uint64,
		detail fieldDetail,
		node ast.Node,
	) error {
		if messageFullName == "" {
			return errors.New("field has no containing message")
		}
		detail.Name = name
		encoded, err := detailJSON(detail)
		if err != nil {
			return err
		}
		return emitNode(
			"DECLARES_FIELD",
			fmt.Sprintf("%s#%d", messageFullName, tag),
			"proto-field-v3",
			encoded,
			node,
		)
	}

	var walkBody func(messageFullName string, declarations []ast.MessageElement) error
	var walkMessage func(parentScope string, message *ast.MessageNode) error
	var walkGroup func(parentFullName, oneof string, group *ast.GroupNode) error

	walkMessage = func(parentScope string, message *ast.MessageNode) error {
		if message.Name == nil {
			return errors.New("message is missing name")
		}
		name := string(message.Name.AsIdentifier())
		fullName := joinFullName(parentScope, name)
		if err := emitDeclaration(
			"DECLARES_MESSAGE", fullName, "proto-message-v3",
			"proto-message-detail-v1", name, message,
		); err != nil {
			return err
		}
		return walkBody(fullName, message.Decls)
	}
	walkGroup = func(parentFullName, oneof string, group *ast.GroupNode) error {
		if group.Name == nil || group.Tag == nil {
			return errors.New("group is missing name or field number")
		}
		name := string(group.Name.AsIdentifier())
		fieldType := resolveTypeReference(name, parentFullName, declarations, imports, false)
		// The proto2 descriptor field name for `group Key` is `key`, while
		// the synthetic nested message remains `Key`.
		if err := emitField(
			parentFullName, strings.ToLower(name), group.Tag.Val,
			fieldDetail{
				Schema: "proto-field-detail-v2", Type: &fieldType,
				Cardinality: fieldCardinality(group.Label), Oneof: oneof,
			},
			group,
		); err != nil {
			return err
		}
		groupFullName := joinFullName(parentFullName, name)
		if err := emitDeclaration(
			"DECLARES_MESSAGE", groupFullName, "proto-message-v3",
			"proto-message-detail-v1", name, group,
		); err != nil {
			return err
		}
		return walkBody(groupFullName, group.Decls)
	}
	walkBody = func(messageFullName string, elements []ast.MessageElement) error {
		for _, element := range elements {
			switch field := element.(type) {
			case *ast.FieldNode:
				if field.Name == nil || field.Tag == nil || field.FldType == nil {
					return errors.New("field is missing name, number, or type")
				}
				fieldType := resolveTypeReference(
					string(field.FldType.AsIdentifier()), messageFullName,
					declarations, imports, false,
				)
				if err := emitField(
					messageFullName, string(field.Name.AsIdentifier()), field.Tag.Val,
					fieldDetail{
						Schema: "proto-field-detail-v2", Type: &fieldType,
						Cardinality: fieldCardinality(field.Label),
					},
					field,
				); err != nil {
					return err
				}
			case *ast.MapFieldNode:
				if field.Name == nil || field.Tag == nil || field.MapType == nil ||
					field.MapType.KeyType == nil || field.MapType.ValueType == nil {
					return errors.New("map field is missing name, number, or type")
				}
				key := resolveTypeReference(
					string(field.MapType.KeyType.AsIdentifier()), messageFullName,
					declarations, imports, false,
				)
				value := resolveTypeReference(
					string(field.MapType.ValueType.AsIdentifier()), messageFullName,
					declarations, imports, false,
				)
				if err := emitField(
					messageFullName, string(field.Name.AsIdentifier()), field.Tag.Val,
					fieldDetail{
						Schema: "proto-field-detail-v2", Cardinality: "repeated",
						Map: &mapShapeDetail{Key: key, Value: value},
					},
					field,
				); err != nil {
					return err
				}
			case *ast.GroupNode:
				if err := walkGroup(messageFullName, "", field); err != nil {
					return err
				}
			case *ast.OneofNode:
				if field.Name == nil {
					return errors.New("oneof is missing name")
				}
				oneof := string(field.Name.AsIdentifier())
				for _, oneofElement := range field.Decls {
					switch oneofField := oneofElement.(type) {
					case *ast.FieldNode:
						if oneofField.Name == nil || oneofField.Tag == nil ||
							oneofField.FldType == nil {
							return errors.New("oneof field is missing name, number, or type")
						}
						fieldType := resolveTypeReference(
							string(oneofField.FldType.AsIdentifier()), messageFullName,
							declarations, imports, false,
						)
						if err := emitField(
							messageFullName, string(oneofField.Name.AsIdentifier()), oneofField.Tag.Val,
							fieldDetail{
								Schema: "proto-field-detail-v2", Type: &fieldType,
								Cardinality: "singular", Oneof: oneof,
							},
							oneofField,
						); err != nil {
							return err
						}
					case *ast.GroupNode:
						if err := walkGroup(messageFullName, oneof, oneofField); err != nil {
							return err
						}
					}
				}
			case *ast.MessageNode:
				if err := walkMessage(messageFullName, field); err != nil {
					return err
				}
			case *ast.ExtendNode:
				return errors.New("extension fields require descriptor linking for extendee lineage")
			}
		}
		return nil
	}

	for _, decl := range fileNode.Decls {
		switch node := decl.(type) {
		case *ast.ServiceNode:
			if node.Name == nil {
				return errors.New("service is missing name")
			}
			serviceName := string(node.Name.AsIdentifier())
			service := joinFullName(pkg, serviceName)
			if err := emitDeclaration(
				"DECLARES_SERVICE", service, "proto-service-v3",
				"proto-service-detail-v1", serviceName, node,
			); err != nil {
				return err
			}
			for _, element := range node.Decls {
				if rpc, ok := element.(*ast.RPCNode); ok {
					if rpc.Name == nil || rpc.Input == nil || rpc.Output == nil ||
						rpc.Input.MessageType == nil || rpc.Output.MessageType == nil {
						return errors.New("rpc is missing name, request, or response type")
					}
					detail, err := detailJSON(operationDetail{
						Schema: "proto-operation-detail-v1",
						Request: resolveTypeReference(
							string(rpc.Input.MessageType.AsIdentifier()), pkg,
							declarations, imports, true,
						),
						Response: resolveTypeReference(
							string(rpc.Output.MessageType.AsIdentifier()), pkg,
							declarations, imports, true,
						),
						ClientStreaming: rpc.Input.Stream != nil,
						ServerStreaming: rpc.Output.Stream != nil,
					})
					if err != nil {
						return err
					}
					if err := emitNode(
						"DECLARES_OPERATION",
						service+"/"+string(rpc.Name.AsIdentifier()),
						"proto-rpc-v3", detail, rpc,
					); err != nil {
						return err
					}
				}
			}
		case *ast.MessageNode:
			if err := walkMessage(pkg, node); err != nil {
				return err
			}
		case *ast.ExtendNode:
			return errors.New("extension fields require descriptor linking for extendee lineage")
		}
	}
	return nil
}

func validateProtoComplexity(ctx context.Context, source string) error {
	return idlpreflight.Validate(ctx, idlpreflight.Protobuf, source)
}

// roleFor is the T11.1 classifier reduced to source-proto paths.
func roleFor(relPath string) string {
	segments := strings.Split(path.Clean(relPath), "/")
	// Preserve the validated classifier's precedence: vendored content stays
	// vendor even when its repository placement is also below a test tree.
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

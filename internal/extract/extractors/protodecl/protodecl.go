// Package protodecl extracts source-declared protobuf operations and fields.
// It parses each bounded regular .proto blob independently and performs no
// import resolution or linking.
package protodecl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/bufbuild/protocompile/ast"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	domain        = "proto-contract"
	version       = "2.0.0"
	schemaVersion = "t12-v2"

	// parser.Parse is in-process and does not accept a context. Preflight the
	// source with conservative size, token, and structural-depth ceilings so a
	// single candidate cannot hand it an unbounded working set or recursive
	// grammar shape. The worker deadline remains a cooperative outer deadline.
	maxProtoSourceBytes     = 4 << 20
	maxProtoTokens          = 500_000
	maxProtoStructuralDepth = 128

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

// lineageID is explicitly provisional and file-scoped. Parser-only extraction
// cannot prove that same-directory files belong to one canonical descriptor
// set; separating them avoids false merges for mutually-exclusive roots with
// duplicate FQNs. The prefix and coverage protocol make that limitation
// machine-visible until trusted module/import-root identity is available.
func lineageID(repo, protoPath string) string {
	h := sha256.Sum256([]byte(repo + "\x00" + protoPath))
	return "provisional_repo_path_v1_" + hex.EncodeToString(h[:])
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

	// Package declarations are semantically file-wide. Collect first so an
	// unusual but parseable declaration order cannot change emitted identities.
	var pkg string
	for _, decl := range fileNode.Decls {
		if packageNode, ok := decl.(*ast.PackageNode); ok {
			pkg = string(packageNode.Name.AsIdentifier())
			break
		}
	}

	lineage := lineageID(corpus.RepoName(), relPath)
	role := roleFor(relPath)
	emitNode := func(predicate, object, rule, detail string, node ast.Node) error {
		info := fileNode.NodeInfo(node)
		start, end := info.Start(), info.End()
		if start.Offset < 0 || end.Offset <= start.Offset || end.Offset > len(blob.Content) ||
			start.Line <= 0 || end.Line < start.Line {
			return fmt.Errorf("invalid parser span for %s", object)
		}
		return emit(sdk.Fact{
			Atom: sdk.AtomInput{
				SchemaVersion: schemaVersion, BlobDigest: blob.Digest,
				StartByte: start.Offset, EndByte: end.Offset, RuleID: rule,
				AdapterConfigDigest: adapterConfigDigest,
				// Lineage is placement-derived and must not enter content identity:
				// identical vendored blobs in different repositories share atoms.
				FactFingerprint: predicate + "|" + object,
			},
			Path: relPath, StartLine: start.Line, EndLine: end.Line,
			Assertion: sdk.AssertionInput{
				Predicate: predicate, Subject: relPath, Object: object,
				Lineage: lineage, Tier: "exact", CodeRole: role, Detail: detail,
			},
		})
	}

	emitField := func(messageFullName, name string, tag uint64, node ast.Node) error {
		if messageFullName == "" {
			return errors.New("field has no containing message")
		}
		return emitNode(
			"DECLARES_FIELD",
			fmt.Sprintf("%s#%d", messageFullName, tag),
			"proto-field-v2",
			`{"schema":"proto-field-detail-v1","name":`+strconv.Quote(name)+`}`,
			node,
		)
	}

	var walkBody func(messageFullName string, declarations []ast.MessageElement) error
	walkGroup := func(parentFullName string, group *ast.GroupNode) error {
		if group.Name == nil || group.Tag == nil {
			return errors.New("group is missing name or field number")
		}
		name := string(group.Name.AsIdentifier())
		// The proto2 descriptor field name for `group Key` is `key`, while
		// the synthetic nested message remains `Key`.
		if err := emitField(parentFullName, strings.ToLower(name), group.Tag.Val, group); err != nil {
			return err
		}
		return walkBody(parentFullName+"."+name, group.Decls)
	}
	walkBody = func(messageFullName string, declarations []ast.MessageElement) error {
		for _, element := range declarations {
			switch field := element.(type) {
			case *ast.FieldNode:
				if field.Name == nil || field.Tag == nil {
					return errors.New("field is missing name or number")
				}
				if err := emitField(messageFullName, string(field.Name.AsIdentifier()), field.Tag.Val, field); err != nil {
					return err
				}
			case *ast.MapFieldNode:
				if field.Name == nil || field.Tag == nil {
					return errors.New("map field is missing name or number")
				}
				if err := emitField(messageFullName, string(field.Name.AsIdentifier()), field.Tag.Val, field); err != nil {
					return err
				}
			case *ast.GroupNode:
				if err := walkGroup(messageFullName, field); err != nil {
					return err
				}
			case *ast.OneofNode:
				for _, oneofElement := range field.Decls {
					switch oneofField := oneofElement.(type) {
					case *ast.FieldNode:
						if oneofField.Name == nil || oneofField.Tag == nil {
							return errors.New("oneof field is missing name or number")
						}
						if err := emitField(messageFullName, string(oneofField.Name.AsIdentifier()), oneofField.Tag.Val, oneofField); err != nil {
							return err
						}
					case *ast.GroupNode:
						if err := walkGroup(messageFullName, oneofField); err != nil {
							return err
						}
					}
				}
			case *ast.MessageNode:
				if field.Name == nil {
					return errors.New("nested message is missing name")
				}
				if err := walkBody(messageFullName+"."+string(field.Name.AsIdentifier()), field.Decls); err != nil {
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
			service := qualify(pkg, string(node.Name.AsIdentifier()))
			for _, element := range node.Decls {
				if rpc, ok := element.(*ast.RPCNode); ok {
					if rpc.Name == nil {
						return errors.New("rpc is missing name")
					}
					if err := emitNode(
						"DECLARES_OPERATION",
						service+"/"+string(rpc.Name.AsIdentifier()),
						"proto-rpc-v2", "", rpc,
					); err != nil {
						return err
					}
				}
			}
		case *ast.MessageNode:
			if node.Name == nil {
				return errors.New("message is missing name")
			}
			if err := walkBody(qualify(pkg, string(node.Name.AsIdentifier())), node.Decls); err != nil {
				return err
			}
		case *ast.ExtendNode:
			return errors.New("extension fields require descriptor linking for extendee lineage")
		}
	}
	return nil
}

func validateProtoComplexity(ctx context.Context, source string) error {
	if len(source) > maxProtoSourceBytes {
		return fmt.Errorf("source exceeds %d-byte proto parser limit", maxProtoSourceBytes)
	}
	depth := 0
	var expectedClosers [maxProtoStructuralDepth]byte
	tokens := 0
	addToken := func() error {
		tokens++
		if tokens > maxProtoTokens {
			return fmt.Errorf("source exceeds %d-token proto parser limit", maxProtoTokens)
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
		case isProtoSpace(current):
			i++
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
				return errors.New("unterminated proto block comment")
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
				return errors.New("unterminated proto string literal")
			}
		case isProtoWordByte(current):
			if err := addToken(); err != nil {
				return err
			}
			i++
			for i < len(source) && isProtoWordByte(source[i]) {
				i++
			}
		default:
			if err := addToken(); err != nil {
				return err
			}
			switch current {
			case '{', '[', '(', '<':
				if depth >= maxProtoStructuralDepth {
					return fmt.Errorf("source exceeds %d-level proto structural-depth limit", maxProtoStructuralDepth)
				}
				switch current {
				case '{':
					expectedClosers[depth] = '}'
				case '[':
					expectedClosers[depth] = ']'
				case '(':
					expectedClosers[depth] = ')'
				case '<':
					expectedClosers[depth] = '>'
				}
				depth++
			case '}', ']', ')', '>':
				if depth > 0 && expectedClosers[depth-1] == current {
					depth--
				}
			}
			i++
		}
	}
	return nil
}

func isProtoSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

func isProtoWordByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func qualify(pkg, name string) string {
	name = strings.TrimPrefix(name, ".")
	if pkg == "" {
		return name
	}
	return pkg + "." + name
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

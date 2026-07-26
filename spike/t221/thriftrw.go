package t221

import (
	"crypto/sha1" //nolint:gosec // thriftrw's embedded descriptor uses SHA1; this verifies it, it is not a security boundary.
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	thriftast "go.uber.org/thriftrw/ast"
	"go.uber.org/thriftrw/idl"
)

// ThriftModuleDecl is one embedded thriftreflect.ThriftModule literal
// recovered from a thriftrw-generated Go file. It is the in-file IDL identity
// the protobuf pack gets from the `// source:` header — but stronger, because
// SHA1 and Raw make the binding digest-verifiable instead of path-asserted.
type ThriftModuleDecl struct {
	GoFile   string // repo-relative path of the generated file
	Name     string
	Package  string
	FilePath string
	SHA1     string
	Raw      string // the embedded raw IDL bytes
}

// RawSHA1 returns the hex SHA1 of the embedded raw IDL bytes.
func (m ThriftModuleDecl) RawSHA1() string {
	sum := sha1.Sum([]byte(m.Raw)) //nolint:gosec // descriptor integrity check, not a security boundary
	return hex.EncodeToString(sum[:])
}

// DiscoverThriftModules walks .go files under root/subdir and returns every
// embedded ThriftModule declaration, sorted by file path. Files that do not
// declare a module are skipped silently; a file that declares one with a
// malformed shape is an error, never a partial result.
func DiscoverThriftModules(root, subdir string) ([]ThriftModuleDecl, error) {
	var out []ThriftModuleDecl
	base := filepath.Join(root, filepath.FromSlash(subdir))
	err := fs.WalkDir(os.DirFS(base), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel := filepath.ToSlash(filepath.Join(subdir, path))
		module, found, err := ParseThriftModuleFile(filepath.Join(base, path))
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		if found {
			module.GoFile = rel
			out = append(out, module)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", base, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GoFile < out[j].GoFile })
	return out, nil
}

// ParseThriftModuleFile reports whether the Go file declares
// `var ThriftModule = &thriftreflect.ThriftModule{...}` and recovers its
// string fields. Raw may be an inline literal or a reference to a same-file
// string constant (thriftrw emits `const rawIDL = "..."`).
func ParseThriftModuleFile(path string) (ThriftModuleDecl, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return ThriftModuleDecl{}, false, err
	}
	lit := findThriftModuleLiteral(file)
	if lit == nil {
		return ThriftModuleDecl{}, false, nil
	}
	module := ThriftModuleDecl{}
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name", "Package", "FilePath", "SHA1", "Raw":
			value, err := resolveStringExpr(file, kv.Value)
			if err != nil {
				return ThriftModuleDecl{}, false, fmt.Errorf("ThriftModule field %s: %w", key.Name, err)
			}
			switch key.Name {
			case "Name":
				module.Name = value
			case "Package":
				module.Package = value
			case "FilePath":
				module.FilePath = value
			case "SHA1":
				module.SHA1 = value
			case "Raw":
				module.Raw = value
			}
		}
	}
	if module.Name == "" || module.FilePath == "" || module.SHA1 == "" || module.Raw == "" {
		return ThriftModuleDecl{}, false, fmt.Errorf("ThriftModule literal missing required fields (name=%q filePath=%q)", module.Name, module.FilePath)
	}
	return module, true, nil
}

func findThriftModuleLiteral(file *ast.File) *ast.CompositeLit {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "ThriftModule" || len(value.Values) != 1 {
				continue
			}
			unary, ok := value.Values[0].(*ast.UnaryExpr)
			if !ok || unary.Op != token.AND {
				continue
			}
			lit, ok := unary.X.(*ast.CompositeLit)
			if !ok {
				continue
			}
			selector, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "ThriftModule" {
				continue
			}
			return lit
		}
	}
	return nil
}

// resolveStringExpr evaluates a string literal, or a same-file identifier
// bound to one (the rawIDL constant). Anything else abstains as an error:
// the identity must be fully in-file to count as in-file identity.
func resolveStringExpr(file *ast.File, expr ast.Expr) (string, error) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", fmt.Errorf("unexpected literal kind %s", typed.Kind)
		}
		return strconv.Unquote(typed.Value)
	case *ast.Ident:
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if name.Name == typed.Name && i < len(value.Values) {
						return resolveStringExpr(file, value.Values[i])
					}
				}
			}
		}
		return "", fmt.Errorf("identifier %s is not a same-file string declaration", typed.Name)
	default:
		return "", fmt.Errorf("unsupported expression %T", expr)
	}
}

// ParseIDLStructFields parses raw Thrift IDL with the thriftrw parser (the
// same library thriftdecl already allowlists) and returns declared field IDs
// per structure: struct, union, and exception definitions, keyed by declared
// name, mapping declared field name to field ID.
func ParseIDLStructFields(raw string) (map[string]map[string]int, error) {
	program, err := idl.Parse([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parse IDL: %w", err)
	}
	out := make(map[string]map[string]int)
	for _, definition := range program.Definitions {
		structure, ok := definition.(*thriftast.Struct)
		if !ok {
			continue
		}
		fields := make(map[string]int, len(structure.Fields))
		for _, field := range structure.Fields {
			fields[field.Name] = field.ID
		}
		out[structure.Name] = fields
	}
	return out, nil
}

// IDLGoNamespace returns the `namespace go` value when the raw IDL declares
// one, with ok=false for the basename fallback thriftdecl's scope rule uses.
func IDLGoNamespace(raw string) (string, bool, error) {
	program, err := idl.Parse([]byte(raw))
	if err != nil {
		return "", false, fmt.Errorf("parse IDL: %w", err)
	}
	for _, header := range program.Headers {
		namespace, ok := header.(*thriftast.Namespace)
		if ok && namespace.Scope == "go" {
			return namespace.Name, true, nil
		}
	}
	return "", false, nil
}

// WireFieldIDsByStruct recovers, per receiver struct, the set of field IDs
// that appear as wire.Field{ID: N} literals inside that struct's ToWire
// method in a thriftrw-generated file. This is the second, independent
// in-file field-identity witness the G3 agreement gate compares against the
// Raw IDL parse.
func WireFieldIDsByStruct(path string) (map[string]map[int]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make(map[string]map[int]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ToWire" || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		receiver := receiverTypeName(fn.Recv.List[0].Type)
		if receiver == "" {
			continue
		}
		ids := make(map[int]bool)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			selector, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Field" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "wire" {
				return true
			}
			for _, element := range lit.Elts {
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
					continue
				}
				id, err := strconv.Atoi(value.Value)
				if err == nil {
					ids[id] = true
				}
			}
			return true
		})
		if len(ids) > 0 {
			out[receiver] = ids
		}
	}
	return out, nil
}

func receiverTypeName(expr ast.Expr) string {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return ""
	}
	ident, ok := star.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

package gocaller

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"unicode/utf8"
)

// PHEBS-48: package-scoped struct fields. The direct scanner models one file
// at a time, so a receiver whose struct is declared in a sibling file of the
// same package lost its field types. The resolver catalog records, per
// directory and package clause, the struct fields whose declared type is
// exactly an admitted generated client type, resolved through the declaring
// file's own imports. The scanner consults that projection only for a struct
// name the scanned file does not declare itself.

// MaxPackageStructSourceBytes is the per-file bound of the projection scan;
// it is the scanner's own source bound.
const MaxPackageStructSourceBytes = maxSourceBytes

// PackageStructField is one projected field of a struct declared in Directory
// under package clause Package. Field is empty for an embedded client type.
type PackageStructField struct {
	Directory      string
	Package        string
	Struct         string
	Field          string
	ImportPath     string
	ClientType     string
	SourcePath     string
	SourceObjectID string
}

// PackageStructFile is what one source file contributes to the projection:
// its package clause, every top-level type name it declares (for duplicate
// detection across build-tag variants), and its client-typed struct fields.
type PackageStructFile struct {
	Package   string
	TypeNames []string
	Fields    []PackageStructField
}

// ClientTypes is the admitted generated client population the projection is
// computed against: import path -> package name, and the exact
// (import path, client type) pairs.
type ClientTypes struct {
	packageNames map[string]string
	types        map[string]struct{}
}

// NewClientTypes builds an admitted population. A conflicting package name
// for one import path is an error, as it is for the resolver.
func NewClientTypes() *ClientTypes {
	return &ClientTypes{packageNames: make(map[string]string), types: make(map[string]struct{})}
}

// Add admits one (import path, package, client type).
func (clients *ClientTypes) Add(importPath, packageName, clientType string) error {
	if importPath == "" || packageName == "" || clientType == "" {
		return errors.New("client type identity is incomplete")
	}
	if prior, present := clients.packageNames[importPath]; present && prior != packageName {
		return errors.New("client import path names multiple packages")
	}
	clients.packageNames[importPath] = packageName
	clients.types[importPath+"\x00"+clientType] = struct{}{}
	return nil
}

// Empty reports whether no client type is admitted.
func (clients *ClientTypes) Empty() bool { return clients == nil || len(clients.types) == 0 }

// DescribePackageStructFile reads one source file. ok is false when the file
// imports no admitted client package (it then contributes nothing and needs
// no full parse). err reports a file that cannot be read exactly; its
// directory must then contribute nothing.
func DescribePackageStructFile(
	filePath, objectID, content string,
	clients *ClientTypes,
) (result PackageStructFile, ok bool, err error) {
	if len(content) > MaxPackageStructSourceBytes {
		return PackageStructFile{}, false, errors.New("source exceeds the projection bound")
	}
	if !utf8.ValidString(content) {
		return PackageStructFile{}, false, errors.New("source is not valid UTF-8")
	}
	fset := token.NewFileSet()
	header, err := parser.ParseFile(fset, filePath, content, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return PackageStructFile{}, false, err
	}
	admitted := false
	for _, spec := range header.Imports {
		importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
		if unquoteErr != nil {
			continue
		}
		if _, present := clients.packageNames[importPath]; present {
			admitted = true
			break
		}
	}
	if !admitted {
		return PackageStructFile{Package: header.Name.Name}, false, nil
	}
	file, err := parser.ParseFile(fset, filePath, content, parser.SkipObjectResolution)
	if err != nil {
		return PackageStructFile{}, false, err
	}
	model := buildSourceFileModel(file, generatedSyntaxIndex{packageNames: clients.packageNames}, nil)
	result.Package = file.Name.Name
	result.TypeNames = declaredTypeNames(file)
	directory := path.Dir(filePath)
	names := make([]string, 0, len(model.structs))
	for name := range model.structs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		structure := model.structs[name]
		add := func(field string, types []sourceType) {
			for _, value := range types {
				if value.importPath == "" || value.clientType == "" {
					continue
				}
				if _, known := clients.types[value.importPath+"\x00"+value.clientType]; !known {
					continue
				}
				result.Fields = append(result.Fields, PackageStructField{
					Directory: directory, Package: result.Package, Struct: name,
					Field: field, ImportPath: value.importPath, ClientType: value.clientType,
					SourcePath: filePath, SourceObjectID: objectID,
				})
			}
		}
		fieldNames := make([]string, 0, len(structure.fields))
		for field := range structure.fields {
			fieldNames = append(fieldNames, field)
		}
		sort.Strings(fieldNames)
		for _, field := range fieldNames {
			add(field, structure.fields[field])
		}
		add("", structure.embedded)
	}
	return result, true, nil
}

func declaredTypeNames(file *ast.File) []string {
	var names []string
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, spec := range general.Specs {
			if typeSpec, ok := spec.(*ast.TypeSpec); ok {
				names = append(names, typeSpec.Name.Name)
			}
		}
	}
	return names
}

func packageStructKey(directory, packageName string) string {
	return directory + "\x00" + packageName
}

// WithPackageStructs returns a resolver view that also knows the projected
// struct fields. Every field must name a client type this resolver admits.
func (resolver *DirectResolver) WithPackageStructs(
	ctx context.Context,
	fields []PackageStructField,
) (*DirectResolver, error) {
	if resolver == nil {
		return nil, errors.New("direct resolver is nil")
	}
	if len(fields) == 0 {
		return resolver, nil
	}
	admitted := make(map[string]struct{})
	for _, methods := range resolver.index.methods {
		for _, method := range methods {
			admitted[method.importPath+"\x00"+method.clientType] = struct{}{}
		}
	}
	projected := make(map[string]map[string]sourceStruct)
	for position, field := range fields {
		if position%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if field.Directory == "" || field.Package == "" || field.Struct == "" ||
			field.SourcePath == "" || field.SourceObjectID == "" ||
			path.Dir(field.SourcePath) != field.Directory {
			return nil, errors.New("package struct field identity is incomplete")
		}
		if _, known := admitted[field.ImportPath+"\x00"+field.ClientType]; !known {
			return nil, errors.New("package struct field names an unadmitted client type")
		}
		key := packageStructKey(field.Directory, field.Package)
		structs := projected[key]
		if structs == nil {
			structs = make(map[string]sourceStruct)
			projected[key] = structs
		}
		structure, present := structs[field.Struct]
		if !present {
			structure = sourceStruct{fields: make(map[string][]sourceType)}
		}
		value := sourceType{importPath: field.ImportPath, clientType: field.ClientType}
		if field.Field == "" {
			structure.embedded = appendTypeUnique(structure.embedded, value)
		} else {
			structure.fields[field.Field] = appendTypeUnique(structure.fields[field.Field], value)
		}
		structs[field.Struct] = structure
	}
	copy := *resolver
	copy.index.packageStructs = projected
	return &copy, nil
}

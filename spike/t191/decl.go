package main

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"go.uber.org/thriftrw/ast"
	"go.uber.org/thriftrw/idl"
)

// declFile is the T19.2 rule prototype's view of one parsed .thrift file:
// enough structure to derive scope, operation objects, and the construct
// inventory the decision table reports on. It deliberately mirrors what the
// production thriftdecl extractor will emit, not everything the AST holds.
type declFile struct {
	Path       string
	Scope      string
	Includes   []string
	Services   []declService
	Structs    int
	Unions     int
	Exceptions int
	Typedefs   int
	Enums      int
	Consts     int
	// ImplicitIDs counts fields with no explicit ID anywhere in the file
	// (struct fields, function parameters, and throws clauses). The decided
	// production posture is a file-level extraction gap when nonzero.
	ImplicitIDs int
}

type declService struct {
	Name      string
	Extends   string
	Functions []declFunction
}

type declFunction struct {
	Name       string
	OneWay     bool
	VoidReturn bool
	Params     int
	Throws     int
}

// parseDeclFile applies the T19.2 declaration rules to one .thrift source.
func parseDeclFile(relPath string, content []byte) (*declFile, error) {
	program, err := idl.Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relPath, err)
	}
	out := &declFile{Path: relPath, Scope: scopeFor(program, relPath)}
	for _, header := range program.Headers {
		if include, ok := header.(*ast.Include); ok {
			out.Includes = append(out.Includes, include.Path)
		}
	}
	for _, definition := range program.Definitions {
		switch node := definition.(type) {
		case *ast.Service:
			service := declService{Name: node.Name}
			if node.Parent != nil {
				service.Extends = node.Parent.Name
			}
			for _, function := range node.Functions {
				out.ImplicitIDs += countImplicitIDs(function.Parameters)
				out.ImplicitIDs += countImplicitIDs(function.Exceptions)
				service.Functions = append(service.Functions, declFunction{
					Name:       function.Name,
					OneWay:     function.OneWay,
					VoidReturn: function.ReturnType == nil,
					Params:     len(function.Parameters),
					Throws:     len(function.Exceptions),
				})
			}
			out.Services = append(out.Services, service)
		case *ast.Struct:
			out.ImplicitIDs += countImplicitIDs(node.Fields)
			switch node.Type {
			case ast.StructType:
				out.Structs++
			case ast.UnionType:
				out.Unions++
			case ast.ExceptionType:
				out.Exceptions++
			}
		case *ast.Typedef:
			out.Typedefs++
		case *ast.Enum:
			out.Enums++
		case *ast.Constant:
			out.Consts++
		}
	}
	return out, nil
}

// scopeFor is the decided scope-precedence rule: the last dot-separated
// segment of `namespace go` when present, otherwise the file basename. Both
// are reproducible from the consumer side, where only the generated Go
// package name is observable; full dotted namespaces and non-Go namespaces
// are not, so they were rejected (see README decision table).
func scopeFor(program *ast.Program, relPath string) string {
	for _, header := range program.Headers {
		namespace, ok := header.(*ast.Namespace)
		if !ok || namespace.Scope != "go" {
			continue
		}
		segments := strings.Split(namespace.Name, ".")
		return segments[len(segments)-1]
	}
	base := path.Base(relPath)
	return strings.TrimSuffix(base, ".thrift")
}

func countImplicitIDs(fields []*ast.Field) int {
	count := 0
	for _, field := range fields {
		if field.IDUnset {
			count++
		}
	}
	return count
}

// operationObjects lists the DECLARES_OPERATION objects (storage form, no
// leading slash) this file would emit: `scope.Service/method`.
func (f *declFile) operationObjects() []string {
	var objects []string
	for _, service := range f.Services {
		for _, function := range service.Functions {
			objects = append(objects, f.Scope+"."+service.Name+"/"+function.Name)
		}
	}
	sort.Strings(objects)
	return objects
}

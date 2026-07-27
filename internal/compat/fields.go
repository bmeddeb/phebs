package compat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bufbuild/protocompile/ast"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"

	"github.com/bmeddeb/phebs/internal/idlpreflight"
)

type sourcePosition struct {
	line   int
	column int
}

type fieldDeclaration struct {
	snapshot string
	path     string
	message  string
	number   int
	start    sourcePosition
	end      sourcePosition // exclusive
}

func fieldDeclarations(ctx context.Context, before, after []File) ([]fieldDeclaration, error) {
	result := make([]fieldDeclaration, 0)
	for _, snapshot := range []struct {
		name  string
		files []File
	}{{name: "before", files: before}, {name: "after", files: after}} {
		for _, file := range snapshot.files {
			if err := validateProtoComplexity(ctx, file.Content); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", snapshot.name, file.Path, err)
			}
			rows, err := parseFieldDeclarations(snapshot.name, file)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", snapshot.name, file.Path, err)
			}
			result = append(result, rows...)
		}
	}
	return result, nil
}

func parseFieldDeclarations(snapshot string, file File) ([]fieldDeclaration, error) {
	handler := reporter.NewHandler(nil)
	fileNode, err := parser.Parse(file.Path, strings.NewReader(file.Content), handler)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if fileNode == nil {
		return nil, errors.New("parse returned no file")
	}
	var pkg string
	for _, declaration := range fileNode.Decls {
		if packageNode, ok := declaration.(*ast.PackageNode); ok {
			pkg = string(packageNode.Name.AsIdentifier())
			break
		}
	}

	result := make([]fieldDeclaration, 0)
	add := func(message string, number uint64, node ast.Node) error {
		if message == "" || number == 0 || number > 536_870_911 {
			return errors.New("field has an invalid identity")
		}
		info := fileNode.NodeInfo(node)
		start, raw := info.Start(), info.RawText()
		if start.Line < 1 || start.Col < 1 || raw == "" {
			return errors.New("field has an invalid parser span")
		}
		end := sourcePosition{line: start.Line, column: start.Col}
		for _, r := range raw {
			if r == '\n' {
				end.line++
				end.column = 1
			} else {
				end.column++
			}
		}
		result = append(result, fieldDeclaration{
			snapshot: snapshot, path: file.Path, message: message, number: int(number),
			start: sourcePosition{line: start.Line, column: start.Col}, end: end,
		})
		return nil
	}

	var walkBody func(string, []ast.MessageElement) error
	walkGroup := func(parent string, group *ast.GroupNode) error {
		if group.Name == nil || group.Tag == nil {
			return errors.New("group is missing name or number")
		}
		if err := add(parent, group.Tag.Val, group); err != nil {
			return err
		}
		return walkBody(parent+"."+string(group.Name.AsIdentifier()), group.Decls)
	}
	walkBody = func(message string, declarations []ast.MessageElement) error {
		for _, declaration := range declarations {
			switch node := declaration.(type) {
			case *ast.FieldNode:
				if node.Tag == nil {
					return errors.New("field is missing number")
				}
				if err := add(message, node.Tag.Val, node); err != nil {
					return err
				}
			case *ast.MapFieldNode:
				if node.Tag == nil {
					return errors.New("map field is missing number")
				}
				if err := add(message, node.Tag.Val, node); err != nil {
					return err
				}
			case *ast.GroupNode:
				if err := walkGroup(message, node); err != nil {
					return err
				}
			case *ast.OneofNode:
				for _, oneofDeclaration := range node.Decls {
					switch oneof := oneofDeclaration.(type) {
					case *ast.FieldNode:
						if oneof.Tag == nil {
							return errors.New("oneof field is missing number")
						}
						if err := add(message, oneof.Tag.Val, oneof); err != nil {
							return err
						}
					case *ast.GroupNode:
						if err := walkGroup(message, oneof); err != nil {
							return err
						}
					}
				}
			case *ast.MessageNode:
				if node.Name == nil {
					return errors.New("nested message is missing name")
				}
				if err := walkBody(message+"."+string(node.Name.AsIdentifier()), node.Decls); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, declaration := range fileNode.Decls {
		message, ok := declaration.(*ast.MessageNode)
		if !ok {
			continue
		}
		if message.Name == nil {
			return nil, errors.New("message is missing name")
		}
		if err := walkBody(qualify(pkg, string(message.Name.AsIdentifier())), message.Decls); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func qualify(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

func matchField(declarations []fieldDeclaration, snapshot, filePath string, line, column int) (FieldIdentity, bool) {
	point := sourcePosition{line: line, column: column}
	var matched *fieldDeclaration
	for i := range declarations {
		declaration := &declarations[i]
		if declaration.snapshot != snapshot || declaration.path != filePath ||
			positionLess(point, declaration.start) || !positionLess(point, declaration.end) {
			continue
		}
		if matched != nil {
			return FieldIdentity{}, false
		}
		matched = declaration
	}
	if matched == nil {
		return FieldIdentity{}, false
	}
	return FieldIdentity{Message: matched.message, Number: matched.number}, true
}

func positionLess(left, right sourcePosition) bool {
	return left.line < right.line || left.line == right.line && left.column < right.column
}

func validateProtoComplexity(ctx context.Context, source string) error {
	return idlpreflight.Validate(ctx, idlpreflight.Protobuf, source)
}

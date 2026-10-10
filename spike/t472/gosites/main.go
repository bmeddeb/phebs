// gosites inventories source syntax only; it never reads an index or prediction.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"sort"
	"strconv"
)

type request struct {
	Path         string   `json:"path"`
	Content      []byte   `json:"content"`
	Methods      []string `json:"methods"`
	Constructors []string `json:"constructors"`
}

type site struct {
	Kind          string `json:"kind"`
	Symbol        string `json:"symbol"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	CitationStart int    `json:"citation_start"`
	CitationEnd   int    `json:"citation_end"`
}

type response struct {
	Sites   []site   `json:"sites"`
	Imports []string `json:"imports"`
	Error   string   `json:"error,omitempty"`
}

func inventory(req request) (response, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, req.Path, req.Content, parser.SkipObjectResolution|parser.AllErrors)
	if err != nil {
		return response{}, fmt.Errorf("parse %s: %w", req.Path, err)
	}
	methods, constructors := make(map[string]bool), make(map[string]bool)
	for _, name := range req.Methods {
		methods[name] = true
	}
	for _, name := range req.Constructors {
		constructors[name] = true
	}
	tf := fset.File(file.Pos())
	res := response{Sites: []site{}, Imports: []string{}}
	for _, imp := range file.Imports {
		value, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return response{}, fmt.Errorf("import %s: %w", req.Path, err)
		}
		res.Imports = append(res.Imports, value)
	}
	seen := make(map[[2]int]bool)
	add := func(kind string, expression ast.Expr) {
		anchor := expression
		symbol := "<expression>"
		switch value := expression.(type) {
		case *ast.SelectorExpr:
			anchor, symbol = value.Sel, value.Sel.Name
		case *ast.Ident:
			symbol = value.Name
		}
		start, end := tf.Offset(anchor.Pos()), tf.Offset(anchor.End())
		key := [2]int{start, end}
		if seen[key] {
			return
		}
		seen[key] = true
		res.Sites = append(res.Sites, site{kind, symbol, start, end,
			tf.Offset(expression.Pos()), tf.Offset(expression.End())})
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			kind := "indirect_or_other_call"
			name := ""
			switch fn := value.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if constructors[name] {
				kind = "constructor_call"
			} else if methods[name] {
				kind = "operation_invocation"
			}
			add(kind, value.Fun)
		case *ast.SelectorExpr:
			if methods[value.Sel.Name] {
				add("operation_reference", value)
			}
		}
		return true
	})
	sort.Slice(res.Sites, func(i, j int) bool {
		if res.Sites[i].Start != res.Sites[j].Start {
			return res.Sites[i].Start < res.Sites[j].Start
		}
		return res.Sites[i].End < res.Sites[j].End
	})
	sort.Strings(res.Imports)
	return res, nil
}

func main() {
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var req request
		if err := decoder.Decode(&req); err == io.EOF {
			return
		} else if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		res, err := inventory(req)
		if err != nil {
			res = response{Error: err.Error()}
		}
		if err := encoder.Encode(res); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

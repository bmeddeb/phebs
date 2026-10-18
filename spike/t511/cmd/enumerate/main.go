// Command enumerate freezes a source-only population before any SCIP or phebs
// output is read. It uses Go's parser, not the field extractor or an index.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
)

type site struct {
	Path         string `json:"path"`
	SourceDigest string `json:"source_digest"`
	Start        int    `json:"start_byte"`
	End          int    `json:"end_byte"`
	Line         int    `json:"line"`
	Kind         string `json:"kind"`
	Token        string `json:"token"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: enumerate source.go [...]")
	}
	var sites []site
	for _, name := range os.Args[1:] {
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, name, content, 0)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		add := func(id *ast.Ident, kind string) {
			start, end := set.Position(id.Pos()).Offset, set.Position(id.End()).Offset
			sites = append(sites, site{name, "sha256:" + hex.EncodeToString(digest[:]), start, end, set.Position(id.Pos()).Line, kind, string(content[start:end])})
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.SelectorExpr:
				add(n.Sel, "selector")
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok {
					add(id, "key")
				}
			}
			return true
		})
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Path != sites[j].Path {
			return sites[i].Path < sites[j].Path
		}
		return sites[i].Start < sites[j].Start
	})
	encoder := json.NewEncoder(os.Stdout)
	for _, row := range sites {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

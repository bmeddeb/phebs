package main

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path"
	"strconv"
	"strings"
)

type goFile struct {
	f          inputFile
	raw        []byte
	set        *token.FileSet
	tree       *ast.File
	e          *engine
	imports    map[string]string
	constant   map[*ast.Object]ast.Expr
	bad        map[*ast.Object]bool
	modulePath string
	version    module
	packageKey string
	boundary   bool
}

func parseModule(raw []byte) module {
	versions := []string{}
	invalid := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.SplitN(line, "//", 2)[0]
		fields := strings.Fields(line)
		for i, word := range fields {
			if word == "github.com/redis/go-redis/v9" {
				if i+1 >= len(fields) {
					invalid = true
				} else {
					versions = append(versions, fields[i+1])
				}
				if strings.Contains(line, "=>") {
					invalid = true
				}
			}
		}
	}
	if len(versions) != 1 || invalid {
		return module{}
	}
	v := versions[0]
	return module{Version: v, Valid: v == "v9.14.1" || v == "v9.17.2"}
}
func prepareGo(e *engine, f inputFile, raw []byte) (*goFile, string) {
	set := token.NewFileSet()
	file := set.AddFile(f.Path, -1, len(raw))
	var scan scanner.Scanner
	failed := false
	scan.Init(file, raw, func(token.Position, string) { failed = true }, 0)
	count, depth := 0, 0
	for {
		_, kind, _ := scan.Scan()
		if kind == token.EOF {
			break
		}
		count++
		if count > 65536 {
			return nil, "resource-limit"
		}
		switch kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		}
		if depth > 64 {
			return nil, "resource-limit"
		}
		if depth < 0 {
			failed = true
		}
	}
	if failed || depth != 0 {
		return nil, "malformed-input"
	}
	set = token.NewFileSet()
	tree, err := parser.ParseFile(set, f.Path, raw, 0)
	if err != nil {
		return nil, "malformed-input"
	}
	nodes := 0
	nameLimit := false
	ast.Inspect(tree, func(n ast.Node) bool {
		if n != nil {
			nodes++
		}
		if id, ok := n.(*ast.Ident); ok && len(id.Name) > 4096 {
			nameLimit = true
		}
		return nodes <= 100000
	})
	if nodes > 100000 || nameLimit {
		return nil, "resource-limit"
	}
	g := &goFile{f: f, raw: raw, set: set, tree: tree, e: e, imports: map[string]string{}, constant: map[*ast.Object]ast.Expr{}, bad: map[*ast.Object]bool{}}
	g.modulePath, g.version = e.moduleFor(f)
	g.packageKey = f.Population + "\x00" + g.modulePath + "\x00" + path.Dir(f.Path) + "\x00" + tree.Name.Name
	if strings.HasSuffix(f.Path, "_test.go") {
		g.packageKey += "\x00test"
	} else {
		g.packageKey += "\x00normal"
	}
	for _, spec := range tree.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		} else if p == "github.com/redis/go-redis/v9" {
			name = "redis"
		} else if p == "fmt" {
			name = "fmt"
		} else {
			name = path.Base(p)
		}
		if name == "." || name == "_" {
			continue
		}
		if _, exists := g.imports[name]; exists {
			g.imports[name] = ""
		} else {
			g.imports[name] = p
		}
	}
	ast.Inspect(tree, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GenDecl:
			if x.Tok == token.CONST {
				for _, spec := range x.Specs {
					v, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, id := range v.Names {
						if i < len(v.Values) && id.Obj != nil {
							g.constant[id.Obj] = v.Values[i]
						}
					}
				}
			}
		case *ast.AssignStmt:
			for _, left := range x.Lhs {
				g.write(left, x)
			}
		case *ast.IncDecStmt:
			g.write(x.X, nil)
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				g.write(x.X, nil)
			}
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "append" && len(x.Args) > 0 {
				g.write(x.Args[0], nil)
			}
		}
		return true
	})
	if len(g.constant) > 1024 {
		return nil, "resource-limit"
	}
	return g, ""
}
func (g *goFile) write(x ast.Expr, decl ast.Node) {
	switch v := x.(type) {
	case *ast.Ident:
		if v.Obj != nil && v.Obj.Decl != decl {
			g.bad[v.Obj] = true
		}
	case *ast.IndexExpr:
		g.write(v.X, nil)
	case *ast.SelectorExpr:
		g.write(v.X, nil)
	case *ast.StarExpr:
		g.write(v.X, nil)
	}
}
func (g *goFile) at(n ast.Node) span {
	return chosen(g.raw, g.set.Position(n.Pos()).Offset, g.set.Position(n.End()).Offset)
}
func (g *goFile) imported(id *ast.Ident, want string) bool {
	return id != nil && (id.Obj == nil || id.Obj.Kind == ast.Pkg) && g.imports[id.Name] == want
}
func (g *goFile) typeName(x ast.Expr) fieldType {
	if ptr, ok := x.(*ast.StarExpr); ok {
		v := g.typeName(ptr.X)
		if v.Type == "client-value" {
			v.Type = "Client"
			return v
		}
		if v.Named != "" {
			return v
		}
		return fieldType{}
	}
	if id, ok := x.(*ast.Ident); ok {
		if id.Obj != nil && (id.Obj.Kind != ast.Typ || g.tree.Scope.Lookup(id.Name) != id.Obj) {
			return fieldType{}
		}
		return fieldType{Named: id.Name}
	}
	if s, ok := x.(*ast.SelectorExpr); ok {
		if id, ok := s.X.(*ast.Ident); ok && g.imported(id, "github.com/redis/go-redis/v9") {
			switch s.Sel.Name {
			case "Client":
				return fieldType{Type: "client-value"}
			case "UniversalClient", "Pipeliner":
				return fieldType{Type: s.Sel.Name}
			}
		}
	}
	return fieldType{}
}
func (e *engine) collect(f inputFile, raw []byte) {
	g, reason := prepareGo(e, f, raw)
	if reason != "" || e.metadataFailed {
		return
	}
	keys := []string{g.packageKey}
	if !strings.HasSuffix(f.Path, "_test.go") {
		keys = append(keys, strings.TrimSuffix(g.packageKey, "normal")+"test")
	}
	for _, packageKey := range keys {
		p := e.packages[packageKey]
		if p == nil {
			p = &packageInfo{Fields: map[string][]fieldType{}}
			e.packages[packageKey] = p
			if !e.account(1, len(packageKey)) {
				return
			}
		}
		for _, decl := range g.tree.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				t, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				prefix := t.Name.Name + "\x00"
				if !e.account(1, len(prefix)) {
					return
				}
				p.Fields[prefix] = append(p.Fields[prefix], fieldType{})
				st, ok := t.Type.(*ast.StructType)
				if !ok || t.Assign.IsValid() {
					continue
				}
				for _, field := range st.Fields.List {
					typ := g.typeName(field.Type)
					for _, name := range field.Names {
						key := prefix + name.Name
						if !e.account(1, len(key)+len(typ.Type)+len(typ.Named)) {
							return
						}
						p.Fields[key] = append(p.Fields[key], typ)
					}
				}
			}
		}
	}
}

func (g *goFile) objectType(id *ast.Ident, seen map[*ast.Object]bool, depth int) fieldType {
	if id.Obj == nil || g.bad[id.Obj] || depth > 64 || seen[id.Obj] {
		return fieldType{}
	}
	seen[id.Obj] = true
	defer delete(seen, id.Obj)
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		return g.typeName(d.Type)
	case *ast.ValueSpec:
		if d.Type != nil {
			return g.typeName(d.Type)
		}
		return fieldType{}
	case *ast.AssignStmt:
		if len(d.Lhs) != 1 || len(d.Rhs) != 1 {
			return fieldType{}
		}
		call, ok := d.Rhs[0].(*ast.CallExpr)
		if !ok {
			return fieldType{}
		}
		s, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return fieldType{}
		}
		if pkg, ok := s.X.(*ast.Ident); ok && g.imported(pkg, "github.com/redis/go-redis/v9") && s.Sel.Name == "NewClient" {
			return fieldType{Type: "Client"}
		}
		if s.Sel.Name == "Pipeline" {
			t := g.receiver(s.X, seen, depth+1)
			if t.Type == "Client" || t.Type == "UniversalClient" {
				return fieldType{Type: "Pipeliner"}
			}
		}
	}
	return fieldType{}
}
func (g *goFile) receiver(x ast.Expr, seen map[*ast.Object]bool, depth int) fieldType {
	if depth > 64 {
		return fieldType{}
	}
	if id, ok := x.(*ast.Ident); ok {
		return g.objectType(id, seen, depth+1)
	}
	if s, ok := x.(*ast.SelectorExpr); ok {
		owner, ok := s.X.(*ast.Ident)
		if !ok {
			return fieldType{}
		}
		typ := g.objectType(owner, seen, depth+1)
		if typ.Named == "" {
			return fieldType{}
		}
		p := g.e.packages[g.packageKey]
		if p == nil || len(p.Fields[typ.Named+"\x00"]) != 1 {
			return fieldType{}
		}
		fields := p.Fields[typ.Named+"\x00"+s.Sel.Name]
		if len(fields) == 1 {
			return fields[0]
		}
	}
	return fieldType{}
}
func (g *goFile) literal(x ast.Expr, steps *int) value {
	*steps = *steps + 1
	if *steps > 4096 {
		g.boundary = true
		return value{State: "unresolved", Reason: "opaque-helper", Span: g.at(x)}
	}
	v := value{State: "unresolved", Reason: "runtime-namespace", Span: g.at(x)}
	switch n := x.(type) {
	case *ast.BasicLit:
		if n.Kind == token.STRING {
			b, err := strconv.Unquote(n.Value)
			if err == nil {
				if len(b) > 4096 {
					v.TooLarge = true
					return v
				}
				return value{Bytes: []byte(b), State: "literal", Span: v.Span}
			}
		}
		if n.Kind == token.INT && len(n.Value) <= 20 {
			return value{Bytes: []byte(n.Value), State: "literal", Span: v.Span, Integer: true}
		}
	case *ast.Ident:
		if expr, ok := g.constant[n.Obj]; ok {
			result := g.literal(expr, steps)
			result.Span = v.Span
			return result
		}
	case *ast.BinaryExpr:
		if n.Op == token.ADD {
			a, b := g.literal(n.X, steps), g.literal(n.Y, steps)
			if a.State == "literal" && b.State == "literal" && len(a.Bytes)+len(b.Bytes) <= 4096 {
				return value{Bytes: append(append([]byte{}, a.Bytes...), b.Bytes...), State: "literal", Span: v.Span}
			}
			v.Reason = "dynamic-concatenation"
		}
	case *ast.SelectorExpr:
		v.Reason = "opaque-helper"
	case *ast.CallExpr:
		if s, ok := n.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Sprintf" {
			if id, ok := s.X.(*ast.Ident); ok && g.imported(id, "fmt") {
				return g.formatValue(n, steps)
			}
		}
		v.Reason = "opaque-helper"
	case *ast.UnaryExpr:
		if n.Op == token.SUB {
			a := g.literal(n.X, steps)
			if a.State == "literal" && a.Integer {
				a.Bytes = append([]byte{'-'}, a.Bytes...)
				a.Span = v.Span
				return a
			}
		}
	}
	return v
}
func (g *goFile) formatValue(call *ast.CallExpr, steps *int) value {
	v := value{State: "unresolved", Reason: "opaque-helper", Span: g.at(call)}
	if len(call.Args) < 1 {
		return v
	}
	if len(call.Args) > 9 {
		v.TooLarge = true
		return v
	}
	format := g.literal(call.Args[0], steps)
	if format.TooLarge || len(format.Bytes) > 256 {
		v.TooLarge = true
		return v
	}
	if format.State != "literal" {
		return v
	}
	parts := []segment{}
	literal := []byte{}
	argument := 1
	flush := func() {
		if len(literal) > 0 {
			parts = append(parts, segment{Kind: "literal", Literal: append([]byte{}, literal...)})
			literal = nil
		}
	}
	for i := 0; i < len(format.Bytes); i++ {
		ch := format.Bytes[i]
		if ch != '%' {
			literal = append(literal, ch)
			continue
		}
		i++
		if i == len(format.Bytes) {
			return v
		}
		ch = format.Bytes[i]
		if ch == '%' {
			literal = append(literal, '%')
			continue
		}
		if (ch != 's' && ch != 'd') || argument >= len(call.Args) {
			return v
		}
		id, ok := call.Args[argument].(*ast.Ident)
		if !ok || id.Obj == nil || g.bad[id.Obj] {
			return v
		}
		field, ok := id.Obj.Decl.(*ast.Field)
		if !ok {
			return v
		}
		kind, ok := field.Type.(*ast.Ident)
		if !ok || (ch == 's' && kind.Name != "string") || (ch == 'd' && kind.Name != "int" && kind.Name != "int64") {
			return v
		}
		flush()
		parts = append(parts, segment{Kind: "parameter", Name: id.Name, Format: "%" + string(ch)})
		argument++
	}
	flush()
	if len(parts) > 32 {
		v.TooLarge = true
		return v
	}
	if argument != len(call.Args) {
		return v
	}
	v.State = "structure"
	v.Reason = ""
	v.Segments = parts
	return v
}
func (g *goFile) array(x ast.Expr) ([]ast.Expr, bool) {
	if id, ok := x.(*ast.Ident); ok {
		if id.Obj == nil || g.bad[id.Obj] {
			return nil, false
		}
		switch d := id.Obj.Decl.(type) {
		case *ast.AssignStmt:
			if len(d.Rhs) != 1 {
				return nil, false
			}
			x = d.Rhs[0]
		case *ast.ValueSpec:
			if len(d.Values) != 1 {
				return nil, false
			}
			x = d.Values[0]
		default:
			return nil, false
		}
	}
	lit, ok := x.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	if len(lit.Elts) > 256 {
		g.boundary = true
		return nil, false
	}
	if _, ok := lit.Type.(*ast.ArrayType); !ok {
		return nil, false
	}
	out := make([]ast.Expr, 0, len(lit.Elts))
	for _, item := range lit.Elts {
		if _, bad := item.(*ast.KeyValueExpr); bad {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}
func (e *engine) goBlob(f inputFile, raw []byte) blobResult {
	g, reason := prepareGo(e, f, raw)
	if reason != "" {
		return refusal(f, reason)
	}
	b := newResult(f)
	b.ModulePath = g.modulePath
	b.ClientVersion = g.version.Version
	b.SourceScope = sha([]byte(f.Repository + "\x00" + g.modulePath + "\x00github.com/redis/go-redis/v9"))
	ast.Inspect(g.tree, func(n ast.Node) bool {
		if g.boundary {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		s, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := s.Sel.Name
		if method == "NewRetriever" || method == "NewIndexer" || method == "Run" || method == "runScript" || method == "runScriptWithErrorCode" {
			b.Sites = append(b.Sites, site{Method: method, Span: g.at(s.Sel), Gaps: []string{"opaque-helper"}, Keys: []keyFact{}, KeyCardinality: "unmeasured"})
			return true
		}
		typ := g.receiver(s.X, map[*ast.Object]bool{}, 0)
		_, known := methodCommands[method]
		if !known && method != "Do" {
			if typ.Type == "" || method == "Pipeline" || method == "Close" || method == "Receive" || method == "Exec" || method == "Discard" || method == "TxPipeline" {
				return true
			}
		}
		row := site{Method: method, Span: g.at(s.Sel), Gaps: []string{}, Keys: []keyFact{}}
		if !g.version.Valid || (typ.Type != "Client" && typ.Type != "UniversalClient" && typ.Type != "Pipeliner") {
			row.Gaps = []string{"unknown-client-adapter"}
			b.Sites = append(b.Sites, row)
			return true
		}
		if method != "Do" && !known {
			row.Gaps = []string{"unknown-client-adapter"}
			b.Sites = append(b.Sites, row)
			return true
		}
		if method == "Del" && call.Ellipsis.IsValid() {
			if len(call.Args) < 2 {
				row.Gaps = []string{"incomplete-key-specification"}
				row.KeyCardinality = "unmeasured"
				b.Sites = append(b.Sites, row)
				return true
			}
			if _, ok := g.array(call.Args[len(call.Args)-1]); !ok {
				row.Recognized = true
				row.Command = "DEL"
				row.Gaps = []string{"incomplete-key-specification"}
				row.KeyCardinality = "unmeasured"
				b.Sites = append(b.Sites, row)
				return true
			}
		}
		steps := 0
		args := []value{}
		if method == "Do" {
			if len(call.Args) < 2 {
				row.Gaps = []string{"opaque-helper"}
			} else if call.Ellipsis.IsValid() {
				if len(call.Args) != 2 {
					row.Gaps = []string{"opaque-helper"}
				} else if entries, ok := g.array(call.Args[1]); ok {
					for _, x := range entries {
						args = append(args, g.literal(x, &steps))
					}
				} else {
					row.Gaps = []string{"opaque-helper"}
				}
			} else {
				for _, x := range call.Args[1:] {
					args = append(args, g.literal(x, &steps))
				}
			}
		} else {
			args = g.typedVector(method, call, &steps)
		}
		if len(args) == 0 {
			if len(row.Gaps) == 0 {
				row.Gaps = []string{"opaque-helper"}
			}
			b.Sites = append(b.Sites, row)
			return true
		}
		e.commandRow(&row, args, method, &b)
		b.Sites = append(b.Sites, row)
		return true
	})
	facts := len(b.Sites) + len(b.Native)
	for _, s := range b.Sites {
		facts += len(s.Keys)
	}
	for _, n := range b.Native {
		facts += len(n.Fields) + len(n.References)
	}
	if g.boundary || b.Boundary != "" || facts > 8192 {
		return refusal(f, "resource-limit")
	}
	return b
}

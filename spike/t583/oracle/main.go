//go:build t583oracle

// Command oracle is the T58.3 gRPC identity oracle. It type-checks one Go
// module checkout with go/packages (tests included) and reports every call
// that go/types resolves to a gRPC client method or to a gRPC service
// registration function, with the identity that code actually uses:
//
//   - a client method's identity is the constant "/pkg.Service/Method" string
//     its own body passes to an Invoke or NewStream call; a call through an
//     interface method takes the identity of the unique concrete type in the
//     interface's package that implements it;
//   - a registration function's identity is the ServiceName constant of the
//     ServiceDesc its own body passes to RegisterService.
//
// It never uses file names, generated-file markers or method-name matching,
// so it is independent of grpcgo's rules. A call whose method is a gRPC client
// method by signature (its last parameter is variadic grpc.CallOption) but
// whose declaring package has no source in the checkout is reported with
// state "external": a gRPC site whose identity this oracle cannot read.
// One JSON line per site; a closing summary line counts files the type
// checker could not load. It must not run on a population before its lock.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

var methodString = regexp.MustCompile(`^/[^/\s]+/[^/\s]+$`)

const callOption = "google.golang.org/grpc.CallOption"

type site struct {
	Repository string   `json:"repository"`
	Path       string   `json:"path"`
	StartByte  int      `json:"start_byte"`
	EndByte    int      `json:"end_byte"`
	Kind       string   `json:"kind"`  // call | registration
	State      string   `json:"state"` // resolved | ambiguous | external
	Identity   string   `json:"identity,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

type summary struct {
	Repository      string   `json:"repository"`
	Summary         bool     `json:"summary"`
	LoadedFiles     int      `json:"loaded_files"`
	Loaded          []string `json:"loaded"`
	IllTypedFiles   []string `json:"ill_typed_files"`
	PackageErrors   int      `json:"package_errors"`
	ClientMethods   int      `json:"client_methods"`
	RegisterFuncs   int      `json:"register_funcs"`
	InterfaceLinked int      `json:"interface_methods_linked"`
}

// index holds identities keyed by "pkgpath.Recv.Method" or "pkgpath.Func".
type index struct {
	client   map[string]string
	iface    map[string][]string
	register map[string]string
}

func main() {
	repo := flag.String("repo", "", "module checkout directory")
	name := flag.String("name", "", "repository name")
	flag.Parse()
	root, err := filepath.Abs(*repo)
	check(err)
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir: root, Tests: true,
		Env: append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	check(err)

	idx := index{client: map[string]string{}, iface: map[string][]string{}, register: map[string]string{}}
	sum := summary{Repository: *name, Summary: true, IllTypedFiles: []string{}}
	good := []*packages.Package{}
	illTyped := map[string]bool{}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 || pkg.IllTyped || pkg.TypesInfo == nil {
			sum.PackageErrors += len(pkg.Errors)
			for _, f := range pkg.CompiledGoFiles {
				illTyped[f] = true
			}
			continue
		}
		good = append(good, pkg)
		buildIndex(pkg, &idx)
	}
	for _, pkg := range good {
		linkInterfaces(pkg, &idx)
	}
	sum.ClientMethods, sum.RegisterFuncs, sum.InterfaceLinked = len(idx.client), len(idx.register), len(idx.iface)

	seen := map[string]bool{}
	var sites []site
	loaded := map[string]bool{}
	for _, pkg := range good {
		for _, file := range pkg.Syntax {
			path := pkg.Fset.Position(file.Pos()).Filename
			rel, err := filepath.Rel(root, path)
			if err != nil || strings.HasPrefix(rel, "..") || illTyped[path] {
				continue
			}
			loaded[rel] = true
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				s, ok := classify(pkg.TypesInfo, &idx, call)
				if !ok {
					return true
				}
				s.Repository, s.Path = *name, rel
				s.StartByte = pkg.Fset.Position(call.Pos()).Offset
				s.EndByte = pkg.Fset.Position(call.End()).Offset
				key := fmt.Sprintf("%s:%d:%d", rel, s.StartByte, s.EndByte)
				if !seen[key] {
					seen[key] = true
					sites = append(sites, s)
				}
				return true
			})
		}
	}
	sum.LoadedFiles = len(loaded)
	for f := range loaded {
		sum.Loaded = append(sum.Loaded, f)
	}
	sort.Strings(sum.Loaded)
	for f := range illTyped {
		if rel, err := filepath.Rel(root, f); err == nil && !loaded[rel] {
			sum.IllTypedFiles = append(sum.IllTypedFiles, rel)
		}
	}
	sort.Strings(sum.IllTypedFiles)
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Path != sites[j].Path {
			return sites[i].Path < sites[j].Path
		}
		return sites[i].StartByte < sites[j].StartByte
	})
	enc := json.NewEncoder(os.Stdout)
	for _, s := range sites {
		check(enc.Encode(s))
	}
	check(enc.Encode(sum))
}

// buildIndex records, from source bodies, every method that passes a constant
// method string to Invoke or NewStream, and every function that passes a
// ServiceDesc with a constant ServiceName to RegisterService.
func buildIndex(pkg *packages.Package, idx *index) {
	info := pkg.TypesInfo
	descs := map[types.Object]string{}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, ident := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if name, ok := serviceName(info, vs.Values[i]); ok {
						descs[info.Defs[ident]] = name
					}
				}
			}
		}
	}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			obj, ok := info.Defs[fn.Name].(*types.Func)
			if !ok {
				continue
			}
			key := funcKey(obj)
			var methods, services []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Invoke", "NewStream":
					for _, arg := range call.Args {
						if tv, ok := info.Types[arg]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
							if s := constant.StringVal(tv.Value); methodString.MatchString(s) {
								methods = append(methods, s)
							}
						}
					}
				case "RegisterService":
					if len(call.Args) > 0 {
						if u, ok := call.Args[0].(*ast.UnaryExpr); ok && u.Op == token.AND {
							if name, ok := descs[referenced(info, u.X)]; ok {
								services = append(services, name)
							}
						}
					}
				}
				return true
			})
			if m := unique(methods); m != "" {
				idx.client[key] = m
			}
			if s := unique(services); s != "" {
				idx.register[key] = s
			}
		}
	}
}

// linkInterfaces gives each interface method the identities of the concrete
// types in the same package that implement the interface.
func linkInterfaces(pkg *packages.Package, idx *index) {
	scope := pkg.Types.Scope()
	var named []*types.Named
	for _, n := range scope.Names() {
		if tn, ok := scope.Lookup(n).(*types.TypeName); ok {
			if nt, ok := tn.Type().(*types.Named); ok {
				named = append(named, nt)
			}
		}
	}
	for _, it := range named {
		iface, ok := it.Underlying().(*types.Interface)
		if !ok || iface.NumMethods() == 0 {
			continue
		}
		for _, ct := range named {
			if _, isIface := ct.Underlying().(*types.Interface); isIface {
				continue
			}
			if !types.Implements(ct, iface) && !types.Implements(types.NewPointer(ct), iface) {
				continue
			}
			for i := 0; i < iface.NumMethods(); i++ {
				m := iface.Method(i)
				if id, ok := idx.client[pkg.Types.Path()+"."+ct.Obj().Name()+"."+m.Name()]; ok {
					k := pkg.Types.Path() + "." + it.Obj().Name() + "." + m.Name()
					idx.iface[k] = appendUnique(idx.iface[k], id)
				}
			}
		}
	}
}

func classify(info *types.Info, idx *index, call *ast.CallExpr) (site, bool) {
	fun := call.Fun
	for {
		switch f := fun.(type) {
		case *ast.IndexExpr:
			fun = f.X
			continue
		case *ast.IndexListExpr:
			fun = f.X
			continue
		case *ast.ParenExpr:
			fun = f.X
			continue
		}
		break
	}
	var obj *types.Func
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[f]; ok {
			obj, _ = sel.Obj().(*types.Func)
		} else {
			obj, _ = info.Uses[f.Sel].(*types.Func)
		}
	case *ast.Ident:
		obj, _ = info.Uses[f].(*types.Func)
	}
	if obj == nil || obj.Pkg() == nil {
		return site{}, false
	}
	key := funcKey(obj)
	if id, ok := idx.client[key]; ok {
		return site{Kind: "call", State: "resolved", Identity: id}, true
	}
	if ids, ok := idx.iface[key]; ok {
		if len(ids) == 1 {
			return site{Kind: "call", State: "resolved", Identity: ids[0]}, true
		}
		sorted := append([]string(nil), ids...)
		sort.Strings(sorted)
		return site{Kind: "call", State: "ambiguous", Candidates: sorted}, true
	}
	if id, ok := idx.register[key]; ok {
		return site{Kind: "registration", State: "resolved", Identity: id}, true
	}
	if isClientSignature(obj) && !hasSource(idx, obj) {
		return site{Kind: "call", State: "external"}, true
	}
	return site{}, false
}

// isClientSignature reports a gRPC client method by signature alone: its last
// parameter is variadic ...grpc.CallOption.
func isClientSignature(obj *types.Func) bool {
	sig, ok := obj.Type().(*types.Signature)
	if !ok || sig.Recv() == nil || !sig.Variadic() || sig.Params().Len() == 0 {
		return false
	}
	last := sig.Params().At(sig.Params().Len() - 1).Type()
	slice, ok := last.(*types.Slice)
	return ok && types.TypeString(slice.Elem(), nil) == callOption
}

// hasSource reports whether any indexed identity lives in obj's package, i.e.
// the declaring package was type-checked from source in this checkout.
func hasSource(idx *index, obj *types.Func) bool {
	prefix := obj.Pkg().Path() + "."
	for k := range idx.client {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func funcKey(obj *types.Func) string {
	sig, _ := obj.Type().(*types.Signature)
	if sig != nil && sig.Recv() != nil {
		t := sig.Recv().Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := types.Unalias(t).(*types.Named); ok {
			return obj.Pkg().Path() + "." + n.Obj().Name() + "." + obj.Name()
		}
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

func serviceName(info *types.Info, expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "ServiceName" {
			if tv, ok := info.Types[kv.Value]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
				return constant.StringVal(tv.Value), true
			}
		}
	}
	return "", false
}

func referenced(info *types.Info, expr ast.Expr) types.Object {
	switch e := expr.(type) {
	case *ast.Ident:
		return info.Uses[e]
	case *ast.SelectorExpr:
		return info.Uses[e.Sel]
	}
	return nil
}

func unique(values []string) string {
	if len(values) == 0 {
		return ""
	}
	for _, v := range values[1:] {
		if v != values[0] {
			return ""
		}
	}
	return values[0]
}

func appendUnique(values []string, v string) []string {
	for _, existing := range values {
		if existing == v {
			return values
		}
	}
	return append(values, v)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

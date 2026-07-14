package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Gate 4 extractor: REFERENCES_PROTO_FIELD — deliberately NOT
// READS_RPC_RESPONSE_FIELD (per the T11.1 assertion boundary: a getter
// reference alone proves neither read-of-RPC-result nor executing service).
//
// Proto message structs are recognized by their `protobuf:"...,N,..."` field
// tags — a wire-truth signal that works even for dependency packages loaded
// from export data (temporal's go.temporal.io/api). Field identity is
// (message Go name, field number, field proto name); the object joins to an
// in-repo DECLARES_FIELD fact when exactly one matches (tier exact), else
// stays package-abstained (tier derived).
//
// access kinds: getter_read | field_read | field_write | construct
func extractFieldRefs(system, commit, root string, declared []Fact, buildTags []string) ([]Fact, error) {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	// (MessageName, fieldNumber) -> full declared object "pkg.Msg#N:name",
	// only when unambiguous within the system.
	declIdx := map[string][]string{}
	for _, d := range declared {
		if d.Predicate != "DECLARES_FIELD" {
			continue
		}
		// object: pkg.Message#N:name (Message may be nested "A.B")
		obj := d.Object
		hash := strings.LastIndex(obj, "#")
		if hash < 0 {
			continue
		}
		numName := obj[hash+1:]
		num := numName
		if c := strings.Index(numName, ":"); c >= 0 {
			num = numName[:c]
		}
		msgPath := obj[:hash]
		short := msgPath[strings.LastIndex(msgPath, ".")+1:]
		k := short + "#" + num
		declIdx[k] = append(declIdx[k], obj)
	}
	uniqueDecl := func(msg, num string) string {
		objs := declIdx[msg+"#"+num]
		seen := map[string]bool{}
		for _, o := range objs {
			seen[o] = true
		}
		if len(seen) == 1 {
			for o := range seen {
				return o
			}
		}
		return ""
	}

	mods, err := findGoModules(root)
	if err != nil {
		return nil, err
	}
	var facts []Fact
	for _, mod := range mods {
		modFacts, merr := fieldRefsForModule(system, commit, root, mod, uniqueDecl, buildTags)
		if merr != nil {
			rel, _ := filepath.Rel(root, mod)
			rel = filepath.ToSlash(rel)
			failure, ferr := newWholeFileFact("EXTRACTION_FAILURE", rel, merr.Error(), "", tierUnresolved,
				system, commit, root, filepath.ToSlash(filepath.Join(rel, "go.mod")), "go-fieldref-v2", "stage=packages-load")
			if ferr != nil {
				return nil, fmt.Errorf("record module failure for %s: %w", rel, ferr)
			}
			facts = append(facts, failure)
			continue
		}
		facts = append(facts, modFacts...)
	}
	return facts, nil
}

type protoField struct {
	goName string
	name   string // proto field name from tag
	number string
}

type protoMsg struct {
	fields  map[string]protoField // Go field name -> field
	getters map[string]protoField // getter method name -> field
}

var protoTagRe = regexp.MustCompile(`protobuf:"[^"]*?\b(?:bytes|varint|fixed32|fixed64|zigzag32|zigzag64|group)?,?(\d+),[^"]*?name=([A-Za-z0-9_.]+)`)

func parseProtoTag(tag string) (num, name string, ok bool) {
	m := protoTagRe.FindStringSubmatch(tag)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func fieldRefsForModule(system, commit, root, modDir string, uniqueDecl func(msg, num string) string, buildTags []string) ([]Fact, error) {
	if err := validateLocalReplaces(root, modDir); err != nil {
		return nil, err
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Dir:        modDir,
		Tests:      true,
		Env:        goPackageEnv(modDir),
		BuildFlags: packageBuildFlags(buildTags),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	semanticInputs, err := verifyPackageSemanticInputs(root, commit, pkgs)
	if err != nil {
		return nil, fmt.Errorf("verify Go semantic inputs: %w", err)
	}
	var loadErrs int
	packages.Visit(pkgs, nil, func(p *packages.Package) { loadErrs += len(p.Errors) })
	if loadErrs > 0 {
		return nil, fmt.Errorf("%d package errors", loadErrs)
	}

	// Pass 1: index proto message structs across the dependency graph.
	// inRepo tracks whether the message's package lives inside the corpus
	// checkout: declared-identity joins are only safe for in-repo types —
	// external modules (temporal's go.temporal.io/api) can reuse message
	// names + field numbers that in-repo protos also use, and joining those
	// by short name fabricates the wrong package (dev finding, sizelimit_test).
	msgs := map[*types.TypeName]*protoMsg{}
	inRepoPkg := map[*types.TypeName]bool{}
	seen := map[*packages.Package]bool{}
	var index func(p *packages.Package)
	index = func(p *packages.Package) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		for _, imp := range p.Imports {
			index(imp)
		}
		if p.Types == nil {
			return
		}
		pkgInRepo := p.Module != nil && p.Module.Dir != "" && strings.HasPrefix(p.Module.Dir, root)
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			st, ok := tn.Type().Underlying().(*types.Struct)
			if !ok {
				continue
			}
			var pm *protoMsg
			for i := 0; i < st.NumFields(); i++ {
				num, pname, ok := parseProtoTag(st.Tag(i))
				if !ok {
					continue
				}
				if pm == nil {
					pm = &protoMsg{fields: map[string]protoField{}, getters: map[string]protoField{}}
				}
				f := protoField{goName: st.Field(i).Name(), name: pname, number: num}
				pm.fields[f.goName] = f
				pm.getters["Get"+f.goName] = f
			}
			if pm != nil {
				msgs[tn] = pm
				inRepoPkg[tn] = pkgInRepo
			}
		}
	}
	for _, p := range pkgs {
		index(p)
	}

	msgOf := func(t types.Type) (*types.TypeName, *protoMsg) {
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		}
		named, ok := t.(*types.Named)
		if !ok {
			return nil, nil
		}
		pm, ok := msgs[named.Obj()]
		if !ok {
			return nil, nil
		}
		return named.Obj(), pm
	}

	var facts []Fact
	emitted := map[string]bool{}
	digests := map[string]string{}
	contents := map[string][]byte{}

	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		modRel, _ := filepath.Rel(root, modDir)
		subject := filepath.ToSlash(filepath.Join(modRel, strings.TrimPrefix(p.PkgPath, modulePath(p))))
		for _, file := range p.Syntax {
			// LHS selectors of assignments are writes.
			writes := map[*ast.SelectorExpr]bool{}
			ast.Inspect(file, func(n ast.Node) bool {
				if as, ok := n.(*ast.AssignStmt); ok {
					for _, lhs := range as.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok {
							writes[sel] = true
						}
					}
				}
				return true
			})
			emit := func(n ast.Node, tn *types.TypeName, f protoField, access string) {
				pp, pe := p.Fset.Position(n.Pos()), p.Fset.Position(n.End())
				abs := pp.Filename
				rel, rerr := filepath.Rel(root, abs)
				if rerr != nil {
					rel = abs
				}
				rel = filepath.ToSlash(rel)
				if _, ok := digests[abs]; !ok {
					b, _ := os.ReadFile(abs)
					contents[abs] = b
					digests[abs] = blobDigest(b)
				}
				object := "?." + tn.Name() + "#" + f.number + ":" + f.name
				tier := tierDerived
				if inRepoPkg[tn] {
					if full := uniqueDecl(tn.Name(), f.number); full != "" {
						object, tier = full, tierExact
					}
				}
				role := classifyRole(rel, contents[abs])
				fact := newFact("REFERENCES_PROTO_FIELD", subject, object, role, tier,
					system, commit, rel, pp.Offset, pe.Offset, pp.Line, pe.Line,
					"go-fieldref-v1", digests[abs], "access="+access+";go="+tn.Name()+"."+f.goName)
				key := fact.AtomID + "|" + fact.Path + "|" + access
				if !emitted[key] {
					emitted[key] = true
					facts = append(facts, fact)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					si, ok := p.TypesInfo.Selections[sel]
					if !ok || si.Kind() != types.MethodVal || len(x.Args) != 0 {
						return true
					}
					tn, pm := msgOf(si.Recv())
					if pm == nil {
						return true
					}
					if f, ok := pm.getters[sel.Sel.Name]; ok {
						emit(x, tn, f, "getter_read")
					}
				case *ast.SelectorExpr:
					si, ok := p.TypesInfo.Selections[x]
					if !ok || si.Kind() != types.FieldVal {
						return true
					}
					tn, pm := msgOf(si.Recv())
					if pm == nil {
						return true
					}
					f, ok := pm.fields[x.Sel.Name]
					if !ok {
						return true
					}
					access := "field_read"
					if writes[x] {
						access = "field_write"
					}
					emit(x, tn, f, access)
				case *ast.CompositeLit:
					tn, pm := msgOf(p.TypesInfo.TypeOf(x))
					if pm == nil {
						return true
					}
					for _, el := range x.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						id, ok := kv.Key.(*ast.Ident)
						if !ok {
							continue
						}
						if f, ok := pm.fields[id.Name]; ok {
							emit(kv, tn, f, "construct")
						}
					}
				}
				return true
			})
		}
	}
	verifiedAgain, err := verifyPackageSemanticInputs(root, commit, pkgs)
	if err != nil {
		return nil, fmt.Errorf("reverify Go semantic inputs: %w", err)
	}
	if verifiedAgain != semanticInputs {
		return nil, fmt.Errorf("go semantic inputs changed during extraction")
	}
	return bindSemanticInputs(facts, semanticInputs), nil
}

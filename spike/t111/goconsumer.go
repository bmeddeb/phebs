package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// extractGoGRPC loads every Go module under root and emits:
//   - IMPLEMENTS_SERVICE facts at Register<Svc>Server call sites,
//   - CALLS_OPERATION facts at method calls whose receiver is a generated
//     gRPC client interface (protoc-gen-go-grpc shape).
//
// Deliberate spike boundaries (recall loss, not precision loss — PLAN ADR):
// calls through hand-written wrapper interfaces are NOT resolved (that is
// the VTA pass, later), and dynamic invocation is out of scope. Failures to
// load a module are emitted as EXTRACTION_FAILURE facts, not swallowed —
// coverage honesty is gate 1.
func extractGoGRPC(system, commit, root string, buildTags []string) ([]Fact, error) {
	mods, err := findGoModules(root)
	if err != nil {
		return nil, fmt.Errorf("find modules: %w", err)
	}
	var facts []Fact
	for _, mod := range mods {
		modFacts, merr := extractModule(system, commit, root, mod, buildTags)
		if merr != nil {
			rel, _ := filepath.Rel(root, mod)
			rel = filepath.ToSlash(rel)
			failure, ferr := newWholeFileFact("EXTRACTION_FAILURE", rel, merr.Error(), "", tierUnresolved,
				system, commit, root, filepath.ToSlash(filepath.Join(rel, "go.mod")), "go-load-v2", "stage=packages-load")
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

func findGoModules(root string) ([]string, error) {
	var mods []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			mods = append(mods, filepath.Dir(path))
		}
		return nil
	})
	return mods, err
}

// clientIface is one generated gRPC client interface plus its resolved
// method → full-method-string map.
type clientIface struct {
	obj      *types.TypeName
	service  string            // e.g. "ProductCatalogService"
	declFile string            // source file declaring the interface, if known
	genDecl  bool              // declaring file carries a "Code generated" header
	methods  map[string]string // method name → "/pkg.Service/Method" (tier exact) or "" (derived)
}

func extractModule(system, commit, root, modDir string, buildTags []string) ([]Fact, error) {
	if err := validateLocalReplaces(root, modDir); err != nil {
		return nil, err
	}
	resolvedInputs, err := verifyResolvedModuleGraph(root, commit, modDir)
	if err != nil {
		return nil, fmt.Errorf("preverify Go module graph: %w", err)
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
	resolvedAgain, err := verifyResolvedModuleGraph(root, commit, modDir)
	if err != nil {
		return nil, fmt.Errorf("reverify Go module graph after load: %w", err)
	}
	if resolvedAgain != resolvedInputs {
		return nil, fmt.Errorf("go module graph changed during package loading")
	}
	// A valid tooling-only module may contain only a go.mod (Loki's
	// operator/.bingo). packages.Load returns an empty slice and nil error for
	// ./... in that case. It has an exactly measured empty Go population, not
	// an unbound semantic input or a hidden load failure.
	if len(pkgs) == 0 {
		return nil, nil
	}
	semanticInputs, err := verifyPackageSemanticInputs(root, commit, pkgs)
	if err != nil {
		return nil, fmt.Errorf("verify Go semantic inputs: %w", err)
	}
	var loadErrs int
	packages.Visit(pkgs, nil, func(p *packages.Package) { loadErrs += len(p.Errors) })

	// Pass 1: index generated client interfaces and Register*Server funcs
	// across the whole dependency graph (generated code may live in deps,
	// e.g. temporalio/api or vendored genproto).
	clients := map[*types.TypeName]*clientIface{}
	registers := map[types.Object]string{} // Register func obj → fully-qualified proto service
	serviceDescs := map[types.Object]string{}
	graph := packageClosure(pkgs)
	for _, p := range graph {
		if p.Types == nil {
			continue
		}
		indexServiceDescriptors(p, serviceDescs)
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if tn, ok := obj.(*types.TypeName); ok && strings.HasSuffix(name, "Client") {
				if ci := asGRPCClientIface(tn); ci != nil {
					ci.declFile = findDeclFile(p.GoFiles, name)
					if ci.declFile != "" {
						if b, rerr := os.ReadFile(ci.declFile); rerr == nil {
							head := b
							if len(head) > 4096 {
								head = head[:4096]
							}
							ci.genDecl = generatedHeaderRe.Match(head)
						}
					}
					clients[tn] = ci
				}
			}
			if fn, ok := obj.(*types.Func); ok &&
				strings.HasPrefix(name, "Register") && strings.HasSuffix(name, "Server") {
				svc := strings.TrimSuffix(strings.TrimPrefix(name, "Register"), "Server")
				if svc != "" && isRegisterShape(fn) {
					// A Go symbol name is not contract identity. Generated
					// ServiceDesc metadata carries the fully-qualified proto
					// service; ambiguity or missing metadata abstains.
					if full := resolveRegisteredService(p.GoFiles, svc); full != "" {
						registers[obj] = full
					}
				}
			}
		}
	}
	resolveFullMethods(clients)

	// Pass 2: walk every dependency package whose source is itself inside the
	// pinned snapshot. This includes vendor/ packages, but excludes external
	// module-cache source even though pass 1 may use it to resolve types.
	var facts []Fact
	digests := map[string]string{}
	contents := map[string][]byte{}
	fileInfo := func(fset *token.FileSet, pos, end token.Pos) (relPath string, sb, eb, sl, el int, digest string, content []byte) {
		pp, pe := fset.Position(pos), fset.Position(end)
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
		return rel, pp.Offset, pe.Offset, pp.Line, pe.Line, digests[abs], contents[abs]
	}

	emitted := map[string]bool{} // dedupe test-variant double loads by atom+path
	for _, p := range graph {
		if p.TypesInfo == nil {
			continue
		}
		for _, file := range p.Syntax {
			filename := p.Fset.Position(file.Pos()).Filename
			fileRel, inside := relativeWithin(root, filename)
			if !inside {
				continue
			}
			subject := filepath.ToSlash(filepath.Dir(fileRel))
			registerForwarders := registerHelperForwarders(file)
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					if id, ok2 := call.Fun.(*ast.Ident); ok2 {
						if svc, ok3 := registers[p.TypesInfo.Uses[id]]; ok3 {
							facts = appendUnique(facts, emitted, implementsFact(p, svc, subject, system, commit, root, call, fileInfo,
								"go-grpc-register-v2", "registration=generated-helper"))
						}
					}
					return true
				}
				// Register<Svc>Server called as pb.RegisterFooServer(...)
				if svc, ok2 := registers[p.TypesInfo.Uses[sel.Sel]]; ok2 {
					facts = appendUnique(facts, emitted, implementsFact(p, svc, subject, system, commit, root, call, fileInfo,
						"go-grpc-register-v2", "registration=generated-helper"))
					return true
				}
				// Hand-written direct registration is an implementation pin
				// only when the grpc.ServiceDesc identity is statically exact.
				// Generated Register<S>Server wrapper internals merely forward
				// an implementation and are not separate implementation sites.
				if isGRPCRegisterService(p.TypesInfo, sel) && len(call.Args) >= 2 && !registerForwarders[call.Pos()] {
					rel, _, _, _, _, _, content := fileInfo(p.Fset, call.Pos(), call.End())
					if classifyRole(rel, content) != roleGenerated {
						if svc := registeredServiceFromExpr(p.TypesInfo, call.Args[0], serviceDescs); svc != "" {
							facts = appendUnique(facts, emitted, implementsFact(p, svc, subject, system, commit, root, call, fileInfo,
								"go-grpc-register-service-v1", "registration=direct-service-desc"))
							return true
						}
					}
				}
				// Client interface method call.
				selInfo, ok2 := p.TypesInfo.Selections[sel]
				if !ok2 || selInfo.Kind() != types.MethodVal {
					return true
				}
				recv := selInfo.Recv()
				if ptr, isPtr := recv.(*types.Pointer); isPtr {
					recv = ptr.Elem()
				}
				named, isNamed := recv.(*types.Named)
				if !isNamed {
					return true
				}
				ci, isClient := clients[named.Obj()]
				if !isClient {
					// Embedding case: a hand-written interface embedding the
					// generated client (dapr's schedclient.Interface). Accept
					// only when exactly ONE indexed client interface is
					// implemented by the receiver AND declares this method —
					// ambiguity abstains rather than guesses.
					ci = uniqueImplementedClient(recv, sel.Sel.Name, clients)
					if ci == nil {
						return true
					}
					named = ci.obj.Type().(*types.Named)
				}
				// Tier honesty: exact = full-method literal found in the
				// generated file; derived = generated interface, literal
				// missed; heuristic = hand-written client-shaped interface
				// (bonus recall, weakest identity claim).
				method := sel.Sel.Name
				full, tier := ci.methods[method], tierExact
				if full == "" {
					full = "/?." + ci.service + "/" + method
					if ci.genDecl {
						tier = tierDerived
					} else {
						tier = tierHeuristic
					}
				}
				rel, sb, eb, sl, el, digest, content := fileInfo(p.Fset, call.Pos(), call.End())
				role := classifyRole(rel, content)
				facts = appendUnique(facts, emitted, newFact("CALLS_OPERATION", subject, full, role, tier,
					system, commit, rel, sb, eb, sl, el, "go-grpc-client-call-v1", digest,
					"iface="+named.Obj().Pkg().Path()+"."+named.Obj().Name()))
				return true
			})
		}
	}
	if loadErrs > 0 {
		modRel, _ := filepath.Rel(root, modDir)
		modRel = filepath.ToSlash(modRel)
		failure, ferr := newWholeFileFact("LOAD_ERRORS", modRel,
			fmt.Sprintf("%d package errors (facts from this module may undercount)", loadErrs), "", tierUnresolved,
			system, commit, root, filepath.ToSlash(filepath.Join(modRel, "go.mod")), "go-load-v2", "stage=packages-load")
		if ferr != nil {
			return nil, fmt.Errorf("record package errors for %s: %w", modRel, ferr)
		}
		facts = append(facts, failure)
	}
	verifiedAgain, err := verifyPackageSemanticInputs(root, commit, pkgs)
	if err != nil {
		return nil, fmt.Errorf("reverify Go semantic inputs: %w", err)
	}
	if verifiedAgain != semanticInputs {
		return nil, fmt.Errorf("go semantic inputs changed during extraction")
	}
	resolvedFinally, err := verifyResolvedModuleGraph(root, commit, modDir)
	if err != nil {
		return nil, fmt.Errorf("final Go module graph verification: %w", err)
	}
	if resolvedFinally != resolvedInputs {
		return nil, fmt.Errorf("go module graph changed during extraction")
	}
	return bindSemanticInputs(facts, semanticInputs), nil
}

func packageClosure(roots []*packages.Package) []*packages.Package {
	seen := map[*packages.Package]bool{}
	var visit func(*packages.Package)
	visit = func(pkg *packages.Package) {
		if pkg == nil || seen[pkg] {
			return
		}
		seen[pkg] = true
		imports := make([]string, 0, len(pkg.Imports))
		for path := range pkg.Imports {
			imports = append(imports, path)
		}
		sort.Strings(imports)
		for _, path := range imports {
			visit(pkg.Imports[path])
		}
	}
	orderedRoots := append([]*packages.Package(nil), roots...)
	sort.Slice(orderedRoots, func(i, j int) bool { return orderedRoots[i].ID < orderedRoots[j].ID })
	for _, root := range orderedRoots {
		visit(root)
	}
	result := make([]*packages.Package, 0, len(seen))
	for pkg := range seen {
		result = append(result, pkg)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID != result[j].ID {
			return result[i].ID < result[j].ID
		}
		return result[i].PkgPath < result[j].PkgPath
	})
	return result
}

// registerHelperForwarders marks the grpc.RegisterService call inside every
// Register<S>Server helper. That wrapper body forwards an implementation; it
// is not itself an application implementation-registration occurrence. The
// structural rule also works for generated files under vendor/, whose role is
// intentionally vendor rather than generated.
func registerHelperForwarders(file *ast.File) map[token.Pos]bool {
	positions := map[token.Pos]bool{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || function.Name == nil ||
			!strings.HasPrefix(function.Name.Name, "Register") || !strings.HasSuffix(function.Name.Name, "Server") {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "RegisterService" {
				positions[call.Pos()] = true
			}
			return true
		})
	}
	return positions
}

func packageBuildFlags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	ordered := append([]string(nil), tags...)
	sort.Strings(ordered)
	return []string{"-tags=" + strings.Join(ordered, ",")}
}

func modulePath(p *packages.Package) string {
	if p.Module != nil {
		return p.Module.Path
	}
	return ""
}

func appendUnique(facts []Fact, emitted map[string]bool, f Fact) []Fact {
	key := f.AtomID + "|" + f.Path
	if emitted[key] {
		return facts
	}
	emitted[key] = true
	return append(facts, f)
}

func implementsFact(p *packages.Package, svc, subject, system, commit, root string, call *ast.CallExpr,
	fileInfo func(*token.FileSet, token.Pos, token.Pos) (string, int, int, int, int, string, []byte),
	ruleID, detail string,
) Fact {
	rel, sb, eb, sl, el, digest, content := fileInfo(p.Fset, call.Pos(), call.End())
	role := classifyRole(rel, content)
	return newFact("IMPLEMENTS_SERVICE", subject, svc, role, tierExact,
		system, commit, rel, sb, eb, sl, el, ruleID, digest, detail)
}

func isGRPCRegisterService(info *types.Info, selector *ast.SelectorExpr) bool {
	selection := info.Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal {
		return false
	}
	obj := selection.Obj()
	return obj != nil && obj.Name() == "RegisterService" && obj.Pkg() != nil && obj.Pkg().Path() == "google.golang.org/grpc"
}

func registeredServiceFromExpr(info *types.Info, expr ast.Expr, descriptors map[types.Object]string) string {
	for {
		switch value := expr.(type) {
		case *ast.ParenExpr:
			expr = value.X
			continue
		case *ast.UnaryExpr:
			if value.Op == token.AND {
				expr = value.X
				continue
			}
		case *ast.CompositeLit:
			if isGRPCServiceDesc(info.TypeOf(value)) {
				return serviceNameLiteral(value)
			}
		case *ast.Ident:
			return descriptors[info.Uses[value]]
		case *ast.SelectorExpr:
			return descriptors[info.Uses[value.Sel]]
		}
		return ""
	}
}

func isGRPCServiceDesc(value types.Type) bool {
	if pointer, ok := value.(*types.Pointer); ok {
		value = pointer.Elem()
	}
	return isNamedType(value, "google.golang.org/grpc", "ServiceDesc")
}

func serviceNameLiteral(literal *ast.CompositeLit) string {
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != "ServiceName" {
			continue
		}
		value, ok := pair.Value.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return ""
		}
		full, err := strconv.Unquote(value.Value)
		if err != nil || !validProtoServiceName(full) {
			return ""
		}
		return full
	}
	return ""
}

func validProtoServiceName(value string) bool {
	if value == "" {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if segment == "" {
			return false
		}
		for index, r := range segment {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (index > 0 && r >= '0' && r <= '9') {
				continue
			}
			return false
		}
	}
	return true
}

func indexServiceDescriptors(pkg *packages.Package, descriptors map[types.Object]string) {
	if pkg.TypesInfo == nil {
		return
	}
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.ValueSpec:
				for index, name := range declaration.Names {
					if index >= len(declaration.Values) {
						continue
					}
					if full := registeredServiceFromExpr(pkg.TypesInfo, declaration.Values[index], descriptors); full != "" {
						if obj := pkg.TypesInfo.Defs[name]; obj != nil {
							descriptors[obj] = full
						}
					}
				}
			case *ast.AssignStmt:
				for index, left := range declaration.Lhs {
					if index >= len(declaration.Rhs) {
						continue
					}
					name, ok := left.(*ast.Ident)
					if !ok {
						continue
					}
					if full := registeredServiceFromExpr(pkg.TypesInfo, declaration.Rhs[index], descriptors); full != "" {
						obj := pkg.TypesInfo.Defs[name]
						if obj == nil {
							obj = pkg.TypesInfo.Uses[name]
						}
						if obj != nil {
							descriptors[obj] = full
						}
					}
				}
			}
			return true
		})
	}
}

// asGRPCClientIface returns metadata iff the named type has the
// protoc-gen-go-grpc client-interface shape: every method takes
// context.Context first and variadic grpc.CallOption last.
func asGRPCClientIface(tn *types.TypeName) *clientIface {
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return nil
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || iface.NumMethods() == 0 {
		return nil
	}
	for i := 0; i < iface.NumMethods(); i++ {
		sig, ok := iface.Method(i).Type().(*types.Signature)
		if !ok || sig.Params().Len() < 2 || !sig.Variadic() {
			return nil
		}
		if !isNamedType(sig.Params().At(0).Type(), "context", "Context") {
			return nil
		}
		last := sig.Params().At(sig.Params().Len() - 1).Type()
		slice, ok := last.(*types.Slice)
		if !ok || !isNamedType(slice.Elem(), "google.golang.org/grpc", "CallOption") {
			return nil
		}
	}
	return &clientIface{
		obj:     tn,
		service: strings.TrimSuffix(tn.Name(), "Client"),
		methods: map[string]string{},
	}
}

// uniqueImplementedClient resolves the embedding case: the receiver is an
// interface (not itself an indexed client) that satisfies exactly one indexed
// generated client interface declaring the called method. Two or more
// matches → nil (abstain; a wrong edge is worse than a missing one).
func uniqueImplementedClient(recv types.Type, method string, clients map[*types.TypeName]*clientIface) *clientIface {
	if _, ok := recv.Underlying().(*types.Interface); !ok {
		return nil
	}
	var found *clientIface
	for tn, ci := range clients {
		iface, ok := tn.Type().Underlying().(*types.Interface)
		if !ok {
			continue
		}
		declares := false
		for i := 0; i < iface.NumMethods(); i++ {
			if iface.Method(i).Name() == method {
				declares = true
				break
			}
		}
		if !declares || !types.Implements(recv, iface) {
			continue
		}
		if found != nil {
			return nil // ambiguous — abstain
		}
		found = ci
	}
	return found
}

// isRegisterShape accepts both codegen eras:
// protoc-gen-go-grpc: func Register<S>Server(s grpc.ServiceRegistrar, srv <S>Server)
// legacy protoc-gen-go plugins=grpc: func Register<S>Server(s *grpc.Server, srv <S>Server)
func isRegisterShape(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Params().Len() != 2 {
		return false
	}
	first := sig.Params().At(0).Type()
	if ptr, isPtr := first.(*types.Pointer); isPtr {
		first = ptr.Elem()
	}
	return isNamedType(first, "google.golang.org/grpc", "ServiceRegistrar") ||
		isNamedType(first, "google.golang.org/grpc", "Server")
}

func isNamedType(t types.Type, pkgPath, name string) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == name && obj.Pkg() != nil && obj.Pkg().Path() == pkgPath
}

var fullMethodRe = regexp.MustCompile(`"(/[\w.]+\.(\w+)/(\w+))"`)
var serviceNameRe = regexp.MustCompile(`\bServiceName\s*:\s*"([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)"`)

// resolveRegisteredService reads generated grpc.ServiceDesc initializers and
// returns a contract-level service identity only when one value uniquely
// matches the Register<S>Server symbol. This avoids treating the short Go
// symbol as globally meaningful and fails closed when two proto packages map
// to the same Go package/name.
func resolveRegisteredService(goFiles []string, short string) string {
	matches := map[string]bool{}
	for _, path := range goFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, match := range serviceNameRe.FindAllSubmatch(content, -1) {
			full := string(match[1])
			if full == short || strings.HasSuffix(full, "."+short) {
				matches[full] = true
			}
		}
	}
	if len(matches) != 1 {
		return ""
	}
	for full := range matches {
		return full
	}
	return ""
}

// resolveFullMethods reads each client interface's declaring file (and its
// sibling generated files) and maps method names to the exact full-method
// string literals found there ("/pkg.Service/Method"). Found literal → tier
// exact; otherwise call sites degrade to tier derived with a constructed
// object — a miss is never a wrong edge.
func resolveFullMethods(clients map[*types.TypeName]*clientIface) {
	for _, ci := range clients {
		if ci.declFile == "" {
			continue
		}
		dir := filepath.Dir(ci.declFile)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, m := range fullMethodRe.FindAllStringSubmatch(string(content), -1) {
				full, svc, method := m[1], m[2], m[3]
				if ci.service == svc {
					ci.methods[method] = full
				}
			}
		}
	}
}

// findDeclFile locates the source file declaring `type <name> interface`
// among the package's Go files (available for source-loaded packages; for
// export-data-only deps GoFiles is empty and the caller degrades to derived).
func findDeclFile(goFiles []string, name string) string {
	needle := "type " + name + " interface"
	for _, p := range goFiles {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), needle) {
			return p
		}
	}
	return ""
}

package gocaller

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type byteRange struct {
	start int
	end   int
}

type generatedMethod struct {
	importPath  string
	packageName string
	clientType  string
	method      string
	binding     symbolBinding
}

type generatedSyntaxIndex struct {
	methods      map[string][]generatedMethod
	packageNames map[string]string
	constructors map[string][]string
}

type sourceType struct {
	importPath string
	clientType string
	localType  string
}

type sourceStruct struct {
	fields   map[string][]sourceType
	embedded []sourceType
}

type sourceFileModel struct {
	aliases        map[string]string
	imported       map[string]struct{}
	dotImport      bool
	typeAliases    map[string][]sourceType
	structs        map[string]sourceStruct
	globalBindings map[string][]sourceType
}

func (e extractor) emitSyntaxFallback(
	ctx context.Context,
	corpus sdk.Corpus,
	paths map[string]struct{},
	typedSpans map[string]map[byteRange]struct{},
	attribution attributionLookup,
	attributionDigest string,
	emit sdk.Emit,
) (int, error) {
	modules, err := readModulePaths(ctx, corpus, paths)
	if err != nil {
		return 0, fmt.Errorf("read module identity: %w", err)
	}
	index, err := e.buildGeneratedSyntaxIndex(
		ctx, corpus, paths, modules, attribution,
	)
	if err != nil {
		return 0, err
	}

	filePaths := make([]string, 0, len(paths))
	for filePath := range paths {
		if strings.HasSuffix(filePath, ".go") {
			filePaths = append(filePaths, filePath)
		}
	}
	sort.Strings(filePaths)
	unresolved := make(map[string]struct{})
	for _, filePath := range filePaths {
		if err := ctx.Err(); err != nil {
			return len(unresolved), err
		}
		// Read even when no generated methods were indexed: Candidate() claims
		// every .go file, and the verified corpus rejects the whole run if a
		// claimed candidate was never read.
		blob, err := corpus.Read(ctx, filePath)
		if err != nil {
			return len(unresolved), fmt.Errorf(
				"read syntax-fallback document %q: %w", filePath, err,
			)
		}
		if len(index.methods) == 0 {
			continue
		}
		if len(blob.Content) > maxSourceBytes || !utf8.ValidString(blob.Content) {
			continue
		}
		count, err := e.scanSyntaxFile(
			filePath, blob, typedSpans[filePath], index, attribution,
			attributionDigest, emit,
		)
		if err != nil {
			return len(unresolved), err
		}
		for _, identity := range count {
			unresolved[identity] = struct{}{}
		}
	}
	return len(unresolved), nil
}

// readModulePaths maps each module root directory ("." for the repo root) to
// its declared module path, supporting monorepos whose Go modules live in
// subdirectories. A missing, oversized, or malformed go.mod leaves its
// subtree without a module identity; extraction proceeds without it.
func readModulePaths(
	ctx context.Context,
	corpus sdk.Corpus,
	paths map[string]struct{},
) (map[string]string, error) {
	modFiles := make([]string, 0, 1)
	for filePath := range paths {
		if filePath == "go.mod" || strings.HasSuffix(filePath, "/go.mod") {
			modFiles = append(modFiles, filePath)
		}
	}
	sort.Strings(modFiles)
	modules := make(map[string]string, len(modFiles))
	for _, filePath := range modFiles {
		blob, err := corpus.Read(ctx, filePath)
		if err != nil {
			return nil, err
		}
		modulePath, ok := parseModulePath(blob.Content)
		if !ok {
			continue
		}
		modules[path.Dir(filePath)] = modulePath
	}
	return modules, nil
}

func parseModulePath(content string) (string, bool) {
	if len(content) > maxSourceBytes || !utf8.ValidString(content) {
		return "", false
	}
	modulePath := ""
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(strings.SplitN(line, "//", 2)[0]))
		if len(fields) == 0 || fields[0] != "module" {
			continue
		}
		if len(fields) != 2 || modulePath != "" {
			return "", false
		}
		modulePath = fields[1]
		if strings.HasPrefix(modulePath, `"`) {
			unquoted, err := strconv.Unquote(modulePath)
			if err != nil {
				return "", false
			}
			modulePath = unquoted
		}
		if !validModulePath(modulePath) {
			return "", false
		}
	}
	return modulePath, modulePath != ""
}

// moduleFor resolves the deepest module root enclosing filePath.
func moduleFor(modules map[string]string, filePath string) (string, string, bool) {
	directory := path.Dir(filePath)
	for {
		if modulePath, ok := modules[directory]; ok {
			return directory, modulePath, true
		}
		if directory == "." {
			return "", "", false
		}
		directory = path.Dir(directory)
	}
}

func validModulePath(value string) bool {
	if value == "" || len(value) > 1024 || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.Contains(value, `\`) ||
		strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func (e extractor) buildGeneratedSyntaxIndex(
	ctx context.Context,
	corpus sdk.Corpus,
	paths map[string]struct{},
	modules map[string]string,
	attribution generatedFromLookup,
) (generatedSyntaxIndex, error) {
	index := generatedSyntaxIndex{
		methods:      make(map[string][]generatedMethod),
		packageNames: make(map[string]string),
		constructors: make(map[string][]string),
	}
	filePaths := make([]string, 0, len(paths))
	for filePath := range paths {
		if e.generatedPathEligible(filePath) {
			filePaths = append(filePaths, filePath)
		}
	}
	sort.Strings(filePaths)
	for _, filePath := range filePaths {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		blob, err := corpus.Read(ctx, filePath)
		if err != nil {
			return index, fmt.Errorf("read generated fallback document %q: %w", filePath, err)
		}
		if len(blob.Content) > maxGeneratedBytes || !utf8.ValidString(blob.Content) {
			continue
		}
		importPath := generatedImportPath(modules, filePath)
		if importPath == "" {
			continue
		}
		var methods []generatedMethod
		var constructors map[string][]string
		switch e.protocol {
		case "grpc":
			methods, constructors, err = grpcSyntaxMethods(
				filePath, importPath, blob.Content, attribution,
			)
		case "thrift":
			methods, constructors, err = thriftSyntaxMethods(
				filePath, importPath, blob.Content, attribution,
			)
		}
		if err != nil {
			continue
		}
		for _, method := range methods {
			key := syntaxMethodKey(method.importPath, method.clientType, method.method)
			index.methods[key] = appendGeneratedMethodUnique(index.methods[key], method)
			if prior, present := index.packageNames[method.importPath]; !present {
				index.packageNames[method.importPath] = method.packageName
			} else if prior != method.packageName {
				delete(index.packageNames, method.importPath)
			}
		}
		for name, clientTypes := range constructors {
			key := importPath + "\x00" + name
			for _, clientType := range clientTypes {
				index.constructors[key] = appendUnique(index.constructors[key], clientType)
			}
			sort.Strings(index.constructors[key])
		}
	}
	return index, nil
}

func generatedImportPath(modules map[string]string, filePath string) string {
	if marker := strings.LastIndex(filePath, "/vendor/"); marker >= 0 {
		return path.Dir(strings.TrimPrefix(
			filePath[marker+len("/vendor/"):], "/",
		))
	}
	if strings.HasPrefix(filePath, "vendor/") {
		return strings.TrimPrefix(path.Dir(filePath), "vendor/")
	}
	moduleDir, modulePath, ok := moduleFor(modules, filePath)
	if !ok {
		return ""
	}
	directory := path.Dir(filePath)
	if directory == moduleDir {
		return modulePath
	}
	if moduleDir == "." {
		return modulePath + "/" + directory
	}
	return modulePath + "/" + strings.TrimPrefix(directory, moduleDir+"/")
}

func grpcSyntaxMethods(
	filePath, importPath, content string,
	attribution generatedFromLookup,
) ([]generatedMethod, map[string][]string, error) {
	if !generatedHdr.MatchString(firstBytes(content, 4096)) {
		return nil, nil, errors.New("not generated gRPC Go")
	}
	sourceMatches := sourceLineRe.FindAllStringSubmatch(content, -1)
	if len(sourceMatches) != 1 {
		return nil, nil, errors.New("generated gRPC source marker is not unique")
	}
	generatorRelative := sourceMatches[0][1]
	file, err := parser.ParseFile(
		token.NewFileSet(), filePath, content, parser.SkipObjectResolution,
	)
	if err != nil {
		return nil, nil, err
	}
	fullMethods := make(map[string][]string)
	serviceNames := make(map[string][]string)
	ast.Inspect(file, func(node ast.Node) bool {
		if keyValue, ok := node.(*ast.KeyValueExpr); ok {
			key, keyOK := keyValue.Key.(*ast.Ident)
			literal, literalOK := keyValue.Value.(*ast.BasicLit)
			if keyOK && key.Name == "ServiceName" && literalOK &&
				literal.Kind == token.STRING {
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr == nil && fullMethodServiceNameValid(value) {
					short := value[strings.LastIndex(value, ".")+1:]
					serviceNames[short] = appendUnique(serviceNames[short], value)
				}
			}
		}
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, unquoteErr := strconv.Unquote(literal.Value)
		if unquoteErr != nil {
			return true
		}
		match := fullMethodRe.FindStringSubmatch(value)
		if match != nil {
			key := match[1][strings.LastIndex(match[1], ".")+1:] + "\x00" + match[2]
			fullMethods[key] = appendUnique(fullMethods[key], value)
		}
		return true
	})

	var methods []generatedMethod
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || !strings.HasSuffix(typeSpec.Name.Name, "Client") {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			service := strings.TrimSuffix(typeSpec.Name.Name, "Client")
			for _, field := range iface.Methods.List {
				if len(field.Names) != 1 {
					continue
				}
				operationValues := fullMethods[service+"\x00"+field.Names[0].Name]
				if len(operationValues) != 1 {
					continue
				}
				match := fullMethodRe.FindStringSubmatch(operationValues[0])
				if match == nil || len(serviceNames[service]) != 1 ||
					serviceNames[service][0] != match[1] {
					continue
				}
				relation := attribution.GeneratedFrom(
					"grpc", filePath, generatorRelative,
				)
				anchor := relationAnchor(
					"grpc", filePath, generatorRelative, operationValues[0], relation,
				)
				methods = append(methods, generatedMethod{
					importPath: importPath, packageName: file.Name.Name,
					clientType: typeSpec.Name.Name, method: field.Names[0].Name,
					binding: bindingFromAnchor(anchor),
				})
			}
		}
	}
	return methods, generatedConstructors(file, methods), nil
}

func thriftSyntaxMethods(
	filePath, importPath, content string,
	attribution generatedFromLookup,
) ([]generatedMethod, map[string][]string, error) {
	if !thriftHeader.MatchString(firstBytes(content, 4096)) {
		return nil, nil, errors.New("not generated Thrift Go")
	}
	file, err := parser.ParseFile(
		token.NewFileSet(), filePath, content, parser.SkipObjectResolution,
	)
	if err != nil {
		return nil, nil, err
	}
	var methods []generatedMethod
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
			continue
		}
		clientType := receiverTypeName(fn.Recv.List[0].Type)
		if !strings.HasSuffix(clientType, "Client") {
			continue
		}
		wireNames := thriftWireNames(fn)
		if len(wireNames) != 1 {
			continue
		}
		service := strings.TrimSuffix(clientType, "Client")
		operation := "/" + file.Name.Name + "." + service + "/" + wireNames[0]
		relation := attribution.GeneratedFrom("thrift", filePath, "")
		anchor := relationAnchor("thrift", filePath, "", operation, relation)
		methods = append(methods, generatedMethod{
			importPath: importPath, packageName: file.Name.Name,
			clientType: clientType, method: fn.Name.Name,
			binding: bindingFromAnchor(anchor),
		})
	}
	return methods, generatedConstructors(file, methods), nil
}

func bindingFromAnchor(anchor generatedAnchor) symbolBinding {
	return symbolBinding{
		operation: anchor.operation, lineage: anchor.lineage,
		generatedPath:     anchor.generatedPath,
		declarationPath:   anchor.declarationPath,
		generatorRelative: anchor.generatorRelative,
		reason:            anchor.reason,
	}
}

func generatedConstructors(
	file *ast.File,
	methods []generatedMethod,
) map[string][]string {
	known := make(map[string]struct{})
	for _, method := range methods {
		known[method.clientType] = struct{}{}
	}
	result := make(map[string][]string)
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Type.Results == nil ||
			len(fn.Type.Results.List) == 0 || !strings.HasPrefix(fn.Name.Name, "New") {
			continue
		}
		for _, field := range fn.Type.Results.List {
			clientType := localTypeName(field.Type)
			if _, present := known[clientType]; present {
				result[fn.Name.Name] = appendUnique(result[fn.Name.Name], clientType)
			}
		}
	}
	return result
}

func localTypeName(expression ast.Expr) string {
	for {
		switch typed := expression.(type) {
		case *ast.StarExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		default:
			identifier, _ := expression.(*ast.Ident)
			if identifier != nil {
				return identifier.Name
			}
			return ""
		}
	}
}

func appendGeneratedMethodUnique(
	values []generatedMethod,
	candidate generatedMethod,
) []generatedMethod {
	for _, value := range values {
		if value.importPath == candidate.importPath &&
			value.clientType == candidate.clientType &&
			value.method == candidate.method &&
			value.binding.operation == candidate.binding.operation &&
			value.binding.lineage == candidate.binding.lineage &&
			value.binding.reason == candidate.binding.reason {
			return values
		}
	}
	return append(values, candidate)
}

func syntaxMethodKey(importPath, clientType, method string) string {
	return importPath + "\x00" + clientType + "\x00" + method
}

func (e extractor) scanSyntaxFile(
	filePath string,
	blob sdk.Blob,
	typed map[byteRange]struct{},
	index generatedSyntaxIndex,
	attribution attributionLookup,
	attributionDigest string,
	emit sdk.Emit,
) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(
		fset, filePath, blob.Content, parser.SkipObjectResolution,
	)
	if err != nil {
		return nil, nil
	}
	tokenFile := fset.File(file.Pos())
	model := buildSourceFileModel(file, index)
	starts := lineStarts(blob.Content)
	var unresolved []string
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		env := cloneBindings(model.globalBindings)
		bindFunctionFields(fn.Type.Params, model, env)
		if fn.Recv != nil {
			bindFunctionFields(fn.Recv, model, env)
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch typedNode := node.(type) {
			case *ast.DeclStmt:
				bindDeclaration(typedNode.Decl, model, index, env)
			case *ast.AssignStmt:
				bindAssignment(typedNode, model, index, env)
			case *ast.CallExpr:
				selector, ok := typedNode.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				methodStart := tokenFile.Offset(selector.Sel.Pos())
				methodEnd := tokenFile.Offset(selector.Sel.End())
				if overlapsTypedRange(typed, methodStart, methodEnd) {
					return true
				}
				bindings, reason := resolveSyntaxCall(selector, model, index, env)
				for _, binding := range bindings {
					spanStart := tokenFile.Offset(selector.Pos())
					spanEnd := methodEnd
					rowReason := reason
					if binding.reason != "" {
						rowReason = binding.reason
					}
					err = emitCallerFact(
						e.protocol, filePath, blob, starts, spanStart, spanEnd,
						binding, attributionDigest,
						attribution.ConsumerUnits(
							filePath, lineAtByte(starts, spanStart),
						),
						"syntax", rowReason,
						classifyRole(filePath, blob.Content), emit,
					)
					if err != nil {
						return false
					}
					if rowReason != "" {
						unresolved = append(
							unresolved,
							callerIdentity(binding.operation, filePath, spanStart, spanEnd),
						)
					}
				}
			}
			return err == nil
		})
		if err != nil {
			return unresolved, err
		}
	}
	return unresolved, nil
}

func buildSourceFileModel(
	file *ast.File,
	index generatedSyntaxIndex,
) sourceFileModel {
	model := sourceFileModel{
		aliases: make(map[string]string), imported: make(map[string]struct{}),
		typeAliases:    make(map[string][]sourceType),
		structs:        make(map[string]sourceStruct),
		globalBindings: make(map[string][]sourceType),
	}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		packageName, admitted := index.packageNames[importPath]
		if !admitted {
			continue
		}
		model.imported[importPath] = struct{}{}
		switch {
		case spec.Name == nil:
			model.aliases[packageName] = importPath
		case spec.Name.Name == ".":
			model.dotImport = true
		case spec.Name.Name != "_":
			model.aliases[spec.Name.Name] = importPath
		}
	}
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		switch gen.Tok {
		case token.TYPE:
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if structType, ok := typeSpec.Type.(*ast.StructType); ok {
					model.structs[typeSpec.Name.Name] = sourceStructFromFields(
						structType.Fields, model,
					)
					continue
				}
				model.typeAliases[typeSpec.Name.Name] = resolveTypeExpression(
					typeSpec.Type, model,
				)
			}
		case token.VAR:
			bindDeclaration(gen, model, index, model.globalBindings)
		}
	}
	return model
}

func sourceStructFromFields(
	fields *ast.FieldList,
	model sourceFileModel,
) sourceStruct {
	result := sourceStruct{fields: make(map[string][]sourceType)}
	if fields == nil {
		return result
	}
	for _, field := range fields.List {
		types := resolveTypeExpression(field.Type, model)
		if len(field.Names) == 0 {
			result.embedded = appendTypeUnique(result.embedded, types...)
			continue
		}
		for _, name := range field.Names {
			result.fields[name.Name] = appendTypeUnique(result.fields[name.Name], types...)
		}
	}
	return result
}

func resolveTypeExpression(
	expression ast.Expr,
	model sourceFileModel,
) []sourceType {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return resolveTypeExpression(typed.X, model)
	case *ast.ParenExpr:
		return resolveTypeExpression(typed.X, model)
	case *ast.IndexExpr:
		return resolveTypeExpression(typed.X, model)
	case *ast.IndexListExpr:
		return resolveTypeExpression(typed.X, model)
	case *ast.SelectorExpr:
		alias, ok := typed.X.(*ast.Ident)
		if !ok {
			return nil
		}
		importPath := model.aliases[alias.Name]
		if importPath == "" {
			return nil
		}
		return []sourceType{{importPath: importPath, clientType: typed.Sel.Name}}
	case *ast.Ident:
		if types := model.typeAliases[typed.Name]; len(types) > 0 {
			return append([]sourceType(nil), types...)
		}
		if _, present := model.structs[typed.Name]; present {
			return []sourceType{{localType: typed.Name}}
		}
	}
	return nil
}

func bindFunctionFields(
	fields *ast.FieldList,
	model sourceFileModel,
	env map[string][]sourceType,
) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		types := resolveTypeExpression(field.Type, model)
		for _, name := range field.Names {
			env[name.Name] = appendTypeUnique(env[name.Name], types...)
		}
	}
}

func bindDeclaration(
	declaration ast.Decl,
	model sourceFileModel,
	index generatedSyntaxIndex,
	env map[string][]sourceType,
) {
	gen, ok := declaration.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return
	}
	for _, spec := range gen.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		explicit := resolveTypeExpression(valueSpec.Type, model)
		for position, name := range valueSpec.Names {
			types := explicit
			if len(types) == 0 && position < len(valueSpec.Values) {
				types = inferExpression(valueSpec.Values[position], model, index, env)
			}
			env[name.Name] = appendTypeUnique(nil, types...)
		}
	}
}

func bindAssignment(
	assignment *ast.AssignStmt,
	model sourceFileModel,
	index generatedSyntaxIndex,
	env map[string][]sourceType,
) {
	for position, left := range assignment.Lhs {
		identifier, ok := left.(*ast.Ident)
		if !ok || identifier.Name == "_" || position >= len(assignment.Rhs) {
			continue
		}
		types := inferExpression(assignment.Rhs[position], model, index, env)
		if len(types) > 0 {
			env[identifier.Name] = appendTypeUnique(nil, types...)
		} else if assignment.Tok == token.DEFINE {
			// A short declaration creates a new lexical binding. Even though
			// this bounded reader deliberately has no full scope graph, it
			// must forget a generated-client provenance that the new value
			// does not prove; retaining it would turn shadowing into a guess.
			delete(env, identifier.Name)
		}
	}
}

func overlapsTypedRange(typed map[byteRange]struct{}, start, end int) bool {
	for candidate := range typed {
		if candidate.start < end && start < candidate.end {
			return true
		}
	}
	return false
}

func inferExpression(
	expression ast.Expr,
	model sourceFileModel,
	index generatedSyntaxIndex,
	env map[string][]sourceType,
) []sourceType {
	switch typed := expression.(type) {
	case *ast.Ident:
		return append([]sourceType(nil), env[typed.Name]...)
	case *ast.UnaryExpr:
		return inferExpression(typed.X, model, index, env)
	case *ast.ParenExpr:
		return inferExpression(typed.X, model, index, env)
	case *ast.CompositeLit:
		return resolveTypeExpression(typed.Type, model)
	case *ast.CallExpr:
		selector, ok := typed.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok {
			return nil
		}
		importPath := model.aliases[alias.Name]
		var result []sourceType
		for _, clientType := range index.constructors[importPath+"\x00"+selector.Sel.Name] {
			result = appendTypeUnique(result, sourceType{
				importPath: importPath, clientType: clientType,
			})
		}
		return result
	case *ast.SelectorExpr:
		var result []sourceType
		for _, receiver := range inferExpression(typed.X, model, index, env) {
			if receiver.localType == "" {
				continue
			}
			structure := model.structs[receiver.localType]
			result = appendTypeUnique(result, structure.fields[typed.Sel.Name]...)
		}
		return result
	}
	return nil
}

func resolveSyntaxCall(
	selector *ast.SelectorExpr,
	model sourceFileModel,
	index generatedSyntaxIndex,
	env map[string][]sourceType,
) ([]symbolBinding, string) {
	receiverTypes := inferExpression(selector.X, model, index, env)
	receiverTypes = expandEmbedded(receiverTypes, model, make(map[string]bool))
	var bindings []symbolBinding
	for _, receiver := range receiverTypes {
		if receiver.importPath == "" || receiver.clientType == "" {
			continue
		}
		key := syntaxMethodKey(receiver.importPath, receiver.clientType, selector.Sel.Name)
		for _, method := range index.methods[key] {
			bindings = appendBindingUnique(bindings, method.binding)
		}
	}
	if len(bindings) == 1 {
		return bindings, ""
	}
	if len(bindings) > 1 {
		bindings = operationKeyedBindings(bindings)
		if len(bindings) == 1 && bindings[0].lineage != "" &&
			bindings[0].reason == "" {
			return bindings, ""
		}
		return bindings, "ambiguous_receiver_provenance"
	}

	importPaths := make([]string, 0, len(model.imported))
	for importPath := range model.imported {
		importPaths = append(importPaths, importPath)
	}
	sort.Strings(importPaths)
	methodKeys := make([]string, 0, len(index.methods))
	for key := range index.methods {
		methodKeys = append(methodKeys, key)
	}
	sort.Strings(methodKeys)
	for _, importPath := range importPaths {
		for _, key := range methodKeys {
			if !strings.HasPrefix(key, importPath+"\x00") {
				continue
			}
			for _, method := range index.methods[key] {
				if method.method == selector.Sel.Name {
					bindings = appendBindingUnique(bindings, method.binding)
				}
			}
		}
	}
	if len(bindings) == 0 {
		return nil, ""
	}
	bindings = operationKeyedBindings(bindings)
	reason := "unsupported_receiver_flow"
	if model.dotImport {
		reason = "dot_import_unsupported"
	}
	if len(bindings) > 1 {
		reason = "ambiguous_method_candidates"
	}
	return bindings, reason
}

func operationKeyedBindings(input []symbolBinding) []symbolBinding {
	byOperation := make(map[string][]symbolBinding)
	for _, binding := range input {
		if binding.operation != "" {
			byOperation[binding.operation] = append(
				byOperation[binding.operation], binding,
			)
		}
	}
	operations := make([]string, 0, len(byOperation))
	for operation := range byOperation {
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	result := make([]symbolBinding, 0, len(operations))
	for _, operation := range operations {
		values := byOperation[operation]
		sort.Slice(values, func(i, j int) bool {
			if values[i].lineage != values[j].lineage {
				return values[i].lineage < values[j].lineage
			}
			if values[i].reason != values[j].reason {
				return values[i].reason < values[j].reason
			}
			return values[i].generatedPath < values[j].generatedPath
		})
		first := values[0]
		consistent := true
		for _, value := range values[1:] {
			if value.lineage != first.lineage || value.reason != first.reason {
				consistent = false
				break
			}
		}
		if consistent {
			result = append(result, first)
		} else {
			result = append(result, symbolBinding{operation: operation})
		}
	}
	return result
}

func expandEmbedded(
	input []sourceType,
	model sourceFileModel,
	seen map[string]bool,
) []sourceType {
	var result []sourceType
	for _, value := range input {
		if value.localType == "" {
			result = appendTypeUnique(result, value)
			continue
		}
		if seen[value.localType] {
			continue
		}
		seen[value.localType] = true
		structure := model.structs[value.localType]
		result = appendTypeUnique(
			result, expandEmbedded(structure.embedded, model, seen)...,
		)
	}
	return result
}

func appendTypeUnique(values []sourceType, candidates ...sourceType) []sourceType {
	for _, candidate := range candidates {
		if candidate.importPath == "" && candidate.clientType == "" &&
			candidate.localType == "" {
			continue
		}
		found := false
		for _, value := range values {
			if value == candidate {
				found = true
				break
			}
		}
		if !found {
			values = append(values, candidate)
		}
	}
	return values
}

func appendBindingUnique(
	values []symbolBinding,
	candidate symbolBinding,
) []symbolBinding {
	for _, value := range values {
		if value.operation == candidate.operation &&
			value.lineage == candidate.lineage &&
			value.reason == candidate.reason {
			return values
		}
	}
	return append(values, candidate)
}

func cloneBindings(
	input map[string][]sourceType,
) map[string][]sourceType {
	result := make(map[string][]sourceType, len(input))
	for name, values := range input {
		result[name] = append([]sourceType(nil), values...)
	}
	return result
}

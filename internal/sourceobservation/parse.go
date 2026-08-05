package sourceobservation

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Input struct {
	Path    string
	Content string
}

type parserState struct {
	fset       *token.FileSet
	file       *ast.File
	sourceSize int
	aliases    map[string]string
	imports    map[string]libraryImport
	dotImport  bool
	constants  []constantDecl
	steps      int
	stepLimit  bool
	callCount  int
	topics     []TopicSite
	topicLimit bool
}

type libraryImport struct {
	library string
	path    string
}

type constantDecl struct {
	name        string
	value       string
	scopeStart  token.Pos
	scopeEnd    token.Pos
	packageWide bool
}

// Parse performs one bounded Go parse and derives the target-independent
// observation. No adapter reparses the source.
func Parse(ctx context.Context, input Input) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	if input.Path == "" || !strings.HasSuffix(input.Path, ".go") {
		return Observation{}, errors.New("source observation requires a Go path")
	}
	if len(input.Content) > MaxSourceBytes {
		return Observation{}, fmt.Errorf("%w: source bytes", ErrLimit)
	}
	if !utf8.ValidString(input.Content) {
		return Observation{}, errors.New("source observation input is not valid UTF-8")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, input.Path, input.Content, 0)
	if err != nil {
		return Observation{}, errors.New("source observation parse failed")
	}
	state := &parserState{
		fset: fset, file: file, sourceSize: len(input.Content),
		aliases: make(map[string]string), imports: make(map[string]libraryImport),
	}
	value := Observation{
		SchemaVersion: SchemaVersion, PolicyVersion: PolicyVersion,
		PolicyDigest:  PolicyDigest(),
		ContentDigest: contentDigest(input.Content), SourceBytes: len(input.Content),
		Package: file.Name.Name,
	}
	if err := state.readImports(&value); err != nil {
		return Observation{}, err
	}
	state.readConstants(file)
	if err := state.readFunctions(ctx, &value); err != nil {
		return Observation{}, err
	}
	if err := state.readKafka(); err != nil {
		return Observation{}, err
	}
	value.TopicSites = state.topics
	canonicalize(&value)
	if err := Validate(value); err != nil {
		return Observation{}, err
	}
	return value, nil
}

func (s *parserState) readImports(value *Observation) error {
	if len(s.file.Imports) > MaxImports {
		return fmt.Errorf("%w: imports", ErrLimit)
	}
	for _, spec := range s.file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path == "" || len(path) > MaxTextBytes {
			return errors.New("invalid Go import")
		}
		alias, kind := defaultAlias(path), "named"
		if spec.Name != nil {
			alias = spec.Name.Name
			switch alias {
			case ".":
				kind = "dot"
			case "_":
				kind = "blank"
			}
		}
		value.Imports = append(value.Imports, Import{Path: path, Alias: alias, Kind: kind})
		switch kind {
		case "named":
			s.aliases[alias] = path
		case "dot":
			s.dotImport = true
		}
		var library string
		switch path {
		case "github.com/Shopify/sarama", "github.com/IBM/sarama":
			library = "sarama"
		case "github.com/segmentio/kafka-go":
			library = "segmentio"
		}
		if library != "" && kind == "named" {
			s.imports[alias] = libraryImport{library: library, path: path}
		}
	}
	return nil
}

func defaultAlias(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	if base == "kafka-go" {
		return "kafka"
	}
	return base
}

func (s *parserState) readConstants(node ast.Node) {
	file, ok := node.(*ast.File)
	if !ok {
		return
	}
	for _, declaration := range file.Decls {
		switch typed := declaration.(type) {
		case *ast.GenDecl:
			s.addConstants(typed, file.Pos(), file.End(), true)
		case *ast.FuncDecl:
			s.collectBlockConstants(typed.Body)
		}
	}
}

func (s *parserState) collectBlockConstants(block *ast.BlockStmt) {
	if block == nil {
		return
	}
	for _, statement := range block.List {
		if declaration, ok := statement.(*ast.DeclStmt); ok {
			if gen, ok := declaration.Decl.(*ast.GenDecl); ok {
				s.addConstants(gen, block.Pos(), block.End(), false)
			}
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			nested, ok := node.(*ast.BlockStmt)
			if !ok {
				return true
			}
			s.collectBlockConstants(nested)
			return false
		})
	}
}

func (s *parserState) addConstants(declaration *ast.GenDecl, start, end token.Pos, packageWide bool) {
	if declaration == nil || declaration.Tok != token.CONST {
		return
	}
	for _, item := range declaration.Specs {
		spec, ok := item.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if value, err := strconv.Unquote(lit.Value); err == nil {
				s.constants = append(s.constants, constantDecl{
					name: name.Name, value: value, scopeStart: start,
					scopeEnd: end, packageWide: packageWide,
				})
			}
		}
	}
}

func (s *parserState) readFunctions(ctx context.Context, value *Observation) error {
	for _, declaration := range s.file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if len(value.Functions) == MaxFunctions {
			return fmt.Errorf("%w: functions", ErrLimit)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		row := Function{Name: fn.Name.Name, Span: s.span(fn)}
		env := make(map[string][]Binding)
		s.bindFields(fn.Type.Params, "parameter", env)
		if fn.Recv != nil {
			s.bindFields(fn.Recv, "receiver", env)
			if len(fn.Recv.List) > 0 {
				row.ReceiverType = typeName(fn.Recv.List[0].Type)
			}
		}
		seenGap := make(map[string]struct{})
		callLimit := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if node == nil || callLimit {
				return !callLimit
			}
			switch typed := node.(type) {
			case *ast.DeclStmt:
				s.bindDecl(typed.Decl, env)
			case *ast.AssignStmt:
				s.bindAssign(typed, env)
			case *ast.CallExpr:
				selector, ok := typed.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if s.callCount == MaxCalls {
					callLimit = true
					return false
				}
				bindings := s.infer(selector.X, env)
				receiver := "resolved"
				reason := ""
				if len(bindings) == 0 {
					receiver = "dynamic"
					reason = "dynamic_receiver"
					if s.stepLimit {
						receiver = "unsupported"
						reason = "propagation_limit"
					} else if s.dotImport {
						receiver = "unsupported"
						reason = "dot_import_unsupported"
					}
				}
				row.Calls = append(row.Calls, Call{
					Selector: selector.Sel.Name, Span: s.span(selector), Receiver: receiver,
					Reason: reason, Bindings: bindings, ArgumentCount: len(typed.Args),
				})
				s.callCount++
				if receiver != "resolved" {
					key := receiver + "\x00" + reason + "\x00" + strconv.Itoa(s.span(selector).StartByte)
					if _, exists := seenGap[key]; !exists {
						seenGap[key] = struct{}{}
						row.Gaps = append(row.Gaps, Gap{Kind: receiver, Reason: reason, Span: s.span(selector)})
					}
				}
			}
			return true
		})
		if s.stepLimit && !hasGap(row.Gaps, "propagation_limit") {
			row.Gaps = append(row.Gaps, Gap{
				Kind: "unsupported", Reason: "propagation_limit", Span: row.Span,
			})
		}
		if callLimit {
			return fmt.Errorf("%w: calls", ErrLimit)
		}
		value.Functions = append(value.Functions, row)
	}
	return nil
}

func hasGap(values []Gap, reason string) bool {
	for _, value := range values {
		if value.Reason == reason {
			return true
		}
	}
	return false
}

func (s *parserState) bindFields(fields *ast.FieldList, origin string, env map[string][]Binding) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		binding, ok := typedBinding(field.Type, origin, s.aliases)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			if !s.step() {
				return
			}
			env[name.Name] = []Binding{binding}
		}
	}
}

func typedBinding(expression ast.Expr, origin string, aliases map[string]string) (Binding, bool) {
	for {
		switch typed := expression.(type) {
		case *ast.StarExpr:
			expression = typed.X
		case *ast.ParenExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		default:
			selector, ok := expression.(*ast.SelectorExpr)
			if !ok {
				return Binding{}, false
			}
			alias, ok := selector.X.(*ast.Ident)
			if !ok || aliases[alias.Name] == "" {
				return Binding{}, false
			}
			return Binding{ImportPath: aliases[alias.Name], TypeName: selector.Sel.Name, Origin: origin}, true
		}
	}
}

func typeName(expression ast.Expr) string {
	for {
		switch typed := expression.(type) {
		case *ast.StarExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		default:
			if ident, ok := expression.(*ast.Ident); ok {
				return ident.Name
			}
			return ""
		}
	}
}

func (s *parserState) step() bool {
	s.steps++
	if s.steps > MaxPropagationSteps {
		s.stepLimit = true
		return false
	}
	return true
}

func (s *parserState) bindDecl(declaration ast.Decl, env map[string][]Binding) {
	gen, ok := declaration.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR || !s.step() {
		return
	}
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range value.Names {
			var bindings []Binding
			if binding, ok := typedBinding(value.Type, "declaration", s.aliases); ok {
				bindings = []Binding{binding}
			} else if i < len(value.Values) {
				bindings = s.infer(value.Values[i], env)
			}
			env[name.Name] = cloneBindings(bindings, "declaration")
		}
	}
}

func (s *parserState) bindAssign(value *ast.AssignStmt, env map[string][]Binding) {
	if !s.step() {
		return
	}
	for i, left := range value.Lhs {
		name, ok := left.(*ast.Ident)
		if !ok || name.Name == "_" || i >= len(value.Rhs) {
			continue
		}
		bindings := s.infer(value.Rhs[i], env)
		if len(bindings) == 0 {
			delete(env, name.Name)
			continue
		}
		env[name.Name] = cloneBindings(bindings, "propagation")
	}
}

func cloneBindings(input []Binding, origin string) []Binding {
	if len(input) > MaxBindingsPerValue {
		input = input[:MaxBindingsPerValue]
	}
	out := append([]Binding(nil), input...)
	for i := range out {
		if out[i].Origin != "constructor" {
			out[i].Origin = origin
		}
	}
	return out
}

func (s *parserState) infer(expression ast.Expr, env map[string][]Binding) []Binding {
	switch typed := expression.(type) {
	case *ast.Ident:
		return cloneBindings(env[typed.Name], "propagation")
	case *ast.ParenExpr:
		return s.infer(typed.X, env)
	case *ast.UnaryExpr:
		return s.infer(typed.X, env)
	case *ast.CallExpr:
		selector, ok := typed.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok || s.aliases[alias.Name] == "" {
			return nil
		}
		return []Binding{{ImportPath: s.aliases[alias.Name], Constructor: selector.Sel.Name, Origin: "constructor"}}
	default:
		return nil
	}
}

func (s *parserState) span(node ast.Node) Span {
	start := s.fset.PositionFor(node.Pos(), false)
	end := s.fset.PositionFor(node.End(), false)
	endLine := start.Line
	if node.End() > node.Pos() {
		endLine = s.fset.PositionFor(node.End()-1, false).Line
	}
	return Span{StartByte: start.Offset, EndByte: end.Offset, StartLine: start.Line, EndLine: endLine}
}

func (s *parserState) readKafka() error {
	if len(s.imports) == 0 {
		return nil
	}
	ast.Inspect(s.file, func(node ast.Node) bool {
		if s.topicLimit {
			return false
		}
		switch typed := node.(type) {
		case *ast.CompositeLit:
			s.kafkaComposite(typed)
		case *ast.CallExpr:
			s.kafkaCall(typed)
		}
		return true
	})
	if s.topicLimit {
		return fmt.Errorf("%w: topic sites", ErrLimit)
	}
	return nil
}

func (s *parserState) qualifiedType(lit *ast.CompositeLit) (libraryImport, string, bool) {
	selector, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return libraryImport{}, "", false
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok {
		return libraryImport{}, "", false
	}
	value, ok := s.imports[alias.Name]
	return value, selector.Sel.Name, ok
}

func (s *parserState) kafkaComposite(lit *ast.CompositeLit) {
	lib, name, ok := s.qualifiedType(lit)
	if !ok {
		return
	}
	switch {
	case lib.library == "sarama" && name == "ProducerMessage":
		s.topicField(lit, lib, "producer", "ProducerMessage", "")
	case lib.library == "segmentio" && (name == "Writer" || name == "WriterConfig"):
		s.topicField(lit, lib, "producer", name, "")
	case lib.library == "segmentio" && name == "ReaderConfig":
		group := s.groupID(lit)
		s.topicField(lit, lib, "consumer", "ReaderConfig.Topic", group)
		for _, element := range lit.Elts {
			kv, ok := element.(*ast.KeyValueExpr)
			key, keyOK := func() (*ast.Ident, bool) {
				if !ok {
					return nil, false
				}
				v, yes := kv.Key.(*ast.Ident)
				return v, yes
			}()
			if !keyOK || key.Name != "GroupTopics" {
				continue
			}
			slice, ok := kv.Value.(*ast.CompositeLit)
			if !ok {
				s.addTopic(lib, "consumer", "ReaderConfig.GroupTopics", group, kv.Value)
				continue
			}
			for _, item := range slice.Elts {
				s.addTopic(lib, "consumer", "ReaderConfig.GroupTopics", group, item)
			}
		}
	}
}

func (s *parserState) topicField(lit *ast.CompositeLit, lib libraryImport, plane, shape, group string) {
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if ok && key.Name == "Topic" {
			s.addTopic(lib, plane, shape, group, kv.Value)
		}
	}
}

func (s *parserState) groupID(lit *ast.CompositeLit) string {
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if ok && key.Name == "GroupID" {
			value, _, ok := s.stringValue(kv.Value)
			if ok {
				return value
			}
		}
	}
	return ""
}

func (s *parserState) kafkaCall(call *ast.CallExpr) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if selector.Sel.Name == "WriteMessages" && len(call.Args) >= 2 {
		for _, arg := range call.Args[1:] {
			lit, ok := arg.(*ast.CompositeLit)
			lib, name, qualified := func() (libraryImport, string, bool) {
				if !ok {
					return libraryImport{}, "", false
				}
				return s.qualifiedType(lit)
			}()
			if qualified && lib.library == "segmentio" && name == "Message" {
				s.topicField(lit, lib, "producer", "Message", "")
			}
		}
	}
	var sarama []libraryImport
	for _, item := range s.imports {
		if item.library == "sarama" {
			sarama = append(sarama, item)
		}
	}
	sort.Slice(sarama, func(i, j int) bool { return sarama[i].path < sarama[j].path })
	switch selector.Sel.Name {
	case "Consume":
		if len(call.Args) != 3 {
			return
		}
		if len(sarama) != 1 {
			s.addUnsupported("consumer", "sarama", "ambiguous-library-import", call)
			return
		}
		slice, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			s.addTopic(sarama[0], "consumer", "Consume-slice", "", call.Args[1])
			return
		}
		for _, item := range slice.Elts {
			s.addTopic(sarama[0], "consumer", "Consume-slice", "", item)
		}
	case "ConsumePartition":
		if len(call.Args) != 3 {
			return
		}
		if len(sarama) != 1 {
			s.addUnsupported("consumer", "sarama", "ambiguous-library-import", call)
			return
		}
		s.addTopic(sarama[0], "consumer", "ConsumePartition", "", call.Args[0])
	}
}

func (s *parserState) addUnsupported(plane, library, reason string, node ast.Node) {
	s.appendTopic(TopicSite{Plane: plane, Library: library, Shape: reason, State: "unsupported", Reason: reason, Span: s.span(node)})
}

func (s *parserState) addTopic(lib libraryImport, plane, shape, group string, expression ast.Expr) {
	value, binding, ok := s.stringValue(expression)
	row := TopicSite{Plane: plane, Library: lib.library, ImportPath: lib.path, Shape: shape, GroupID: group, Span: s.span(expression)}
	switch {
	case !ok:
		row.State = "dynamic"
		row.Reason = expressionClass(expression)
	case !validTopic(value):
		row.State = "unsupported"
		row.Reason = "invalid-topic-literal"
	default:
		row.State = "resolved"
		row.Topic = value
		row.Binding = binding
	}
	s.appendTopic(row)
}

func (s *parserState) appendTopic(value TopicSite) {
	if len(s.topics) == MaxTopicSites {
		s.topicLimit = true
		return
	}
	s.topics = append(s.topics, value)
}

func (s *parserState) stringValue(expression ast.Expr) (string, string, bool) {
	switch typed := expression.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", "", false
		}
		value, err := strconv.Unquote(typed.Value)
		return value, "literal", err == nil
	case *ast.Ident:
		var candidates []constantDecl
		for _, candidate := range s.constants {
			if candidate.name != typed.Name ||
				(!candidate.packageWide && (typed.Pos() < candidate.scopeStart || typed.Pos() > candidate.scopeEnd)) {
				continue
			}
			if len(candidates) == 0 || candidate.scopeStart > candidates[0].scopeStart {
				candidates = []constantDecl{candidate}
			} else if candidate.scopeStart == candidates[0].scopeStart {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) != 1 {
			return "", "", false
		}
		return candidates[0].value, "same-file-const", true
	default:
		return "", "", false
	}
}

func expressionClass(expression ast.Expr) string {
	switch expression.(type) {
	case *ast.SelectorExpr:
		return "selector-expr"
	case *ast.CallExpr:
		return "call-expr"
	case *ast.Ident:
		return "unresolved-ident"
	default:
		return "non-literal-expr"
	}
}

func validTopic(value string) bool {
	if value == "" || len(value) > 249 || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

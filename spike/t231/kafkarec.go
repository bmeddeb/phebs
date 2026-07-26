package t231

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Library import paths recognized in round one. franz-go is deliberately
// absent: the reference OSS infrastructure (jaeger v2's kafka components)
// has migrated to it, and that migration is recorded as a deferred family
// in the decision table, never silently admitted.
const (
	importSaramaShopify = "github.com/Shopify/sarama"
	importSaramaIBM     = "github.com/IBM/sarama"
	importSegmentio     = "github.com/segmentio/kafka-go"
)

// TopicEvidence is one recognized producer- or consumer-plane topic use
// with a source-literal (or same-file-const) topic spelling. It carries no
// cluster, environment, runtime, or completeness claim.
type TopicEvidence struct {
	File      string
	Library   string // "sarama" | "segmentio"
	Plane     string // "producer" | "consumer"
	Topic     string
	Binding   string // "literal" | "same-file-const"
	Shape     string // "ProducerMessage" | "Message" | "Writer" | "WriterConfig" | "ReaderConfig.Topic" | "ReaderConfig.GroupTopics" | "Consume-slice" | "ConsumePartition"
	Tier      string // "derived" | "heuristic"
	GroupID   string // consumer detail only, never identity; empty when non-literal or absent
	Import    string // the matched import path (records the Shopify/IBM era split)
	StartByte int    // exact source-expression span, zero-based and end-exclusive
	EndByte   int
	StartLine int // one-based
}

// TopicAbstention is one recognized producer/consumer site whose topic is
// not a source literal. Production code is expected to dominate here; the
// shape class is the sanitized census the census gate records.
type TopicAbstention struct {
	File      string
	Library   string
	Plane     string
	Shape     string // "selector-expr" | "call-expr" | "unresolved-ident" | "non-literal-expr" | "invalid-topic-literal" | "ambiguous-library-import"
	Tier      string
	Import    string // empty only for an ambiguous Sarama-import abstention
	StartByte int
	EndByte   int
	StartLine int
}

// ValidTopicLiteral applies Kafka's own topic-name constraints: 1..249
// characters from [a-zA-Z0-9._-]. A literal failing these is authored
// noise, not identity, and abstains.
func ValidTopicLiteral(topic string) bool {
	if len(topic) == 0 || len(topic) > 249 || topic == "." || topic == ".." {
		return false
	}
	for _, r := range topic {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// TopicObject is the object spelling: topic:<literal>. The spelling contains
// the source literal and nothing else — no cluster, no environment, no
// group — which is exactly the identity claim the epic makes.
func TopicObject(topic string) string {
	return "topic:" + topic
}

// ScanFile parses one Go file and returns the topic evidence and abstentions
// its recognition rules produce. Files that import none of the round-one
// libraries yield nothing: the import gate is the document-eligibility rule.
func ScanFile(path, rel string) ([]TopicEvidence, []TopicAbstention, error) {
	fset := token.NewFileSet()
	// Keep in-file object resolution so the frozen same-file-const rule covers
	// lexically visible package and function-local constants, never vars or
	// cross-file/dataflow propagation.
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	aliases := libraryAliases(file)
	if len(aliases) == 0 {
		return nil, nil, nil
	}
	scan := &fileScan{fset: fset, rel: rel, aliases: aliases}
	ast.Inspect(file, scan.visit)
	return scan.evidence, scan.abstentions, nil
}

type libraryImport struct {
	library    string
	importPath string
}

// libraryAliases maps the local package alias to the recognized library.
// Dot-imports are refused by omission: an unqualified composite literal has
// no in-file proof of which package it belongs to (the sarama repository's
// own in-package tests demonstrate the ambiguity), so recognition requires
// a qualified selector.
func libraryAliases(file *ast.File) map[string]libraryImport {
	out := make(map[string]libraryImport)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		var library string
		switch path {
		case importSaramaShopify, importSaramaIBM:
			library = "sarama"
		case importSegmentio:
			library = "segmentio"
		default:
			continue
		}
		alias := defaultAlias(path)
		if spec.Name != nil {
			if spec.Name.Name == "." || spec.Name.Name == "_" {
				continue
			}
			alias = spec.Name.Name
		}
		out[alias] = libraryImport{library: library, importPath: path}
	}
	return out
}

func defaultAlias(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	// kafka-go's package name is kafka, not kafka-go.
	if base == "kafka-go" {
		return "kafka"
	}
	return base
}

type fileScan struct {
	fset        *token.FileSet
	rel         string
	aliases     map[string]libraryImport
	evidence    []TopicEvidence
	abstentions []TopicAbstention
}

func (s *fileScan) visit(node ast.Node) bool {
	switch typed := node.(type) {
	case *ast.CompositeLit:
		s.visitComposite(typed)
	case *ast.CallExpr:
		s.visitCall(typed)
	}
	return true
}

// qualifiedType resolves a composite literal's type to (library, typeName)
// when it is alias.Type for a recognized library import.
func (s *fileScan) qualifiedType(lit *ast.CompositeLit) (libraryImport, string, bool) {
	selector, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return libraryImport{}, "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return libraryImport{}, "", false
	}
	imported, ok := s.aliases[pkg.Name]
	if !ok {
		return libraryImport{}, "", false
	}
	return imported, selector.Sel.Name, true
}

func (s *fileScan) visitComposite(lit *ast.CompositeLit) {
	imported, typeName, ok := s.qualifiedType(lit)
	if !ok {
		return
	}
	switch {
	case imported.library == "sarama" && typeName == "ProducerMessage":
		s.topicKey(lit, imported, "producer", "ProducerMessage", "")
	case imported.library == "segmentio" && (typeName == "Writer" || typeName == "WriterConfig"):
		s.topicKey(lit, imported, "producer", typeName, "")
	case imported.library == "segmentio" && typeName == "ReaderConfig":
		group := s.literalGroupID(lit)
		s.topicKey(lit, imported, "consumer", "ReaderConfig.Topic", group)
		s.groupTopics(lit, imported, group)
	}
}

// topicKey resolves the Topic key of a recognized composite literal. A
// composite without a Topic key emits nothing: Topic-less Writer and
// ReaderConfig values are legal (per-message topics, GroupTopics).
func (s *fileScan) topicKey(lit *ast.CompositeLit, imported libraryImport, plane, shape, group string) {
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Topic" {
			continue
		}
		s.resolveTopic(kv.Value, imported, plane, shape, group)
	}
}

// groupTopics resolves segmentio's GroupTopics: []string{...} slice, one
// evidence row per literal element.
func (s *fileScan) groupTopics(lit *ast.CompositeLit, imported libraryImport, group string) {
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "GroupTopics" {
			continue
		}
		slice, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			s.abstain(imported, "consumer", "non-literal-expr", "derived", kv.Value)
			continue
		}
		for _, topicExpr := range slice.Elts {
			s.resolveTopic(topicExpr, imported, "consumer", "ReaderConfig.GroupTopics", group)
		}
	}
}

func (s *fileScan) literalGroupID(lit *ast.CompositeLit) string {
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "GroupID" {
			continue
		}
		if value, _, ok := s.stringConstant(kv.Value); ok {
			return value
		}
	}
	return ""
}

func (s *fileScan) visitCall(call *ast.CallExpr) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if selector.Sel.Name == "WriteMessages" {
		s.writeMessages(call)
	}
	sarama, saramaImports := s.saramaImport()
	if saramaImports == 0 {
		return
	}
	switch selector.Sel.Name {
	case "Consume":
		// Heuristic by design (recorded): the receiver's type is not
		// resolvable without type-checking, so a three-argument Consume in
		// a sarama-importing file is treated as sarama's
		// ConsumerGroup.Consume(ctx, topics, handler).
		if len(call.Args) != 3 {
			return
		}
		if saramaImports > 1 {
			s.abstain(libraryImport{library: "sarama"}, "consumer", "ambiguous-library-import", "heuristic", call)
			return
		}
		s.consumeSlice(call.Args[1], sarama)
	case "ConsumePartition":
		if len(call.Args) != 3 {
			return
		}
		if saramaImports > 1 {
			s.abstain(libraryImport{library: "sarama"}, "consumer", "ambiguous-library-import", "heuristic", call)
			return
		}
		s.resolveTopic(call.Args[0], sarama, "consumer", "ConsumePartition", "")
	}
}

// writeMessages recognizes only qualified kafka.Message composite literals
// passed directly to WriteMessages. A standalone Message may instead be a
// consumer offset passed to CommitMessages, so classifying every Message
// composite as a producer would be unsound. Following a local variable or
// slice into the call would also cross the frozen no-dataflow boundary.
func (s *fileScan) writeMessages(call *ast.CallExpr) {
	if len(call.Args) < 2 {
		return
	}
	for _, arg := range call.Args[1:] {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		imported, typeName, ok := s.qualifiedType(lit)
		if !ok || imported.library != "segmentio" || typeName != "Message" {
			continue
		}
		s.topicKey(lit, imported, "producer", "Message", "")
	}
}

// saramaImport returns the sole Sarama import when one exists. The count is
// deliberately separate: a migration file may import both historical paths,
// and a receiver-untyped method shape cannot truthfully choose either era.
func (s *fileScan) saramaImport() (libraryImport, int) {
	var found libraryImport
	count := 0
	for _, imported := range s.aliases {
		if imported.library == "sarama" {
			found = imported
			count++
		}
	}
	if count != 1 {
		return libraryImport{}, count
	}
	return found, count
}

func (s *fileScan) consumeSlice(arg ast.Expr, imported libraryImport) {
	slice, ok := arg.(*ast.CompositeLit)
	if !ok {
		s.abstain(imported, "consumer", shapeClass(arg), "heuristic", arg)
		return
	}
	arrayType, ok := slice.Type.(*ast.ArrayType)
	if !ok {
		s.abstain(imported, "consumer", "non-literal-expr", "heuristic", arg)
		return
	}
	if ident, ok := arrayType.Elt.(*ast.Ident); !ok || ident.Name != "string" {
		s.abstain(imported, "consumer", "non-literal-expr", "heuristic", arg)
		return
	}
	for _, element := range slice.Elts {
		s.resolveTopic(element, imported, "consumer", "Consume-slice", "")
	}
}

// resolveTopic applies the literal-or-abstain boundary: a string literal or
// a same-file const resolves; everything else abstains with its shape class.
// There is no dataflow, no cross-file resolution, and no var admission —
// a var is mutable state, not a declaration-strength spelling.
func (s *fileScan) resolveTopic(expr ast.Expr, imported libraryImport, plane, shape, group string) {
	tier := tierForShape(shape)
	value, binding, ok := s.stringConstant(expr)
	if !ok {
		s.abstain(imported, plane, shapeClass(expr), tier, expr)
		return
	}
	if !ValidTopicLiteral(value) {
		s.abstain(imported, plane, "invalid-topic-literal", tier, expr)
		return
	}
	if plane != "consumer" {
		group = ""
	}
	start, end, line := s.sourceSite(expr)
	s.evidence = append(s.evidence, TopicEvidence{
		File:      s.rel,
		Library:   imported.library,
		Plane:     plane,
		Topic:     value,
		Binding:   binding,
		Shape:     shape,
		Tier:      tier,
		GroupID:   group,
		Import:    imported.importPath,
		StartByte: start,
		EndByte:   end,
		StartLine: line,
	})
}

func tierForShape(shape string) string {
	switch shape {
	case "Consume-slice", "ConsumePartition":
		return "heuristic"
	default:
		return "derived"
	}
}

// stringConstant evaluates a string literal or a lexically resolved same-file
// const identifier.
func (s *fileScan) stringConstant(expr ast.Expr) (string, string, bool) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", "", false
		}
		value, err := strconv.Unquote(typed.Value)
		if err != nil {
			return "", "", false
		}
		return value, "literal", true
	case *ast.Ident:
		if typed.Obj == nil || typed.Obj.Kind != ast.Con {
			return "", "", false
		}
		value, ok := typed.Obj.Decl.(*ast.ValueSpec)
		if !ok {
			return "", "", false
		}
		for i, name := range value.Names {
			if name.Name != typed.Name || i >= len(value.Values) {
				continue
			}
			if lit, ok := value.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if resolved, err := strconv.Unquote(lit.Value); err == nil {
					return resolved, "same-file-const", true
				}
			}
			return "", "", false
		}
		return "", "", false
	default:
		return "", "", false
	}
}

func shapeClass(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.SelectorExpr:
		return "selector-expr"
	case *ast.CallExpr:
		return "call-expr"
	case *ast.Ident:
		_ = typed
		return "unresolved-ident"
	default:
		return "non-literal-expr"
	}
}

func (s *fileScan) abstain(imported libraryImport, plane, shape, tier string, node ast.Node) {
	start, end, line := s.sourceSite(node)
	s.abstentions = append(s.abstentions, TopicAbstention{
		File:      s.rel,
		Library:   imported.library,
		Plane:     plane,
		Shape:     shape,
		Tier:      tier,
		Import:    imported.importPath,
		StartByte: start,
		EndByte:   end,
		StartLine: line,
	})
}

func (s *fileScan) sourceSite(node ast.Node) (int, int, int) {
	start := s.fset.PositionFor(node.Pos(), false)
	end := s.fset.PositionFor(node.End(), false)
	return start.Offset, end.Offset, start.Line
}

func walkGo(root, subdir string, visit func(rel, full string) error) error {
	return walkGoPopulation(root, subdir, false, visit)
}

// walkAllGo is the corpus-census variant: it includes testdata so a claimed
// whole-checkout absence cannot be manufactured by the production scanner's
// fixture-directory exclusion.
func walkAllGo(root, subdir string, visit func(rel, full string) error) error {
	return walkGoPopulation(root, subdir, true, visit)
}

func walkGoPopulation(root, subdir string, includeTestdata bool, visit func(rel, full string) error) error {
	base := filepath.Join(root, filepath.FromSlash(subdir))
	return fs.WalkDir(os.DirFS(base), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || (!includeTestdata && d.Name() == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel := filepath.ToSlash(filepath.Join(subdir, path))
		return visit(rel, filepath.Join(base, path))
	})
}

// ScanTree scans every non-test .go file under root/subdir, returning
// evidence and abstentions sorted by file. It never descends into .git or
// testdata, and it skips _test.go files: a fixture literal (the jaeger v1
// ingester tests consume the invented topic "morekuzambu") is authored
// noise, not production topic evidence. The census gate separately records
// what the exclusion leaves out.
func ScanTree(root, subdir string) ([]TopicEvidence, []TopicAbstention, int, error) {
	var evidence []TopicEvidence
	var abstentions []TopicAbstention
	scanned := 0
	err := walkGo(root, subdir, func(rel, full string) error {
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		fileEvidence, fileAbstentions, err := ScanFile(full, rel)
		if err != nil {
			return err
		}
		scanned++
		evidence = append(evidence, fileEvidence...)
		abstentions = append(abstentions, fileAbstentions...)
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].File != evidence[j].File {
			return evidence[i].File < evidence[j].File
		}
		if evidence[i].StartByte != evidence[j].StartByte {
			return evidence[i].StartByte < evidence[j].StartByte
		}
		return evidence[i].Topic < evidence[j].Topic
	})
	sort.Slice(abstentions, func(i, j int) bool {
		if abstentions[i].File != abstentions[j].File {
			return abstentions[i].File < abstentions[j].File
		}
		return abstentions[i].StartByte < abstentions[j].StartByte
	})
	return evidence, abstentions, scanned, nil
}

// Package kafkago extracts Kafka topic evidence from Go source per the
// T23.1-frozen decision table (spike/t231/README.md, KD1–KD10). Two
// extractors share one recognizer: the producer plane emits
// PRODUCES_TO_TOPIC and the consumer plane CONSUMES_FROM_TOPIC, both with
// `topic:<literal>` objects that carry no cluster, environment, runtime, or
// completeness claim. Recognition is qualified-selector shapes over the two
// sarama import paths and segmentio/kafka-go; the topic must be a string
// literal or same-file const inside Kafka's own naming bounds; everything
// else emits an UNRESOLVED_KAFKA_* assertion with the frozen shape class —
// production code is expected to be abstention-dominant, and every surface
// must present that volume as the honest norm.
package kafkago

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	producerDomain = "kafka-producer"
	consumerDomain = "kafka-consumer"
	version        = "1.0.0"
	schemaVersion  = "t23-v1"

	// A Go source file above this size is a coverage gap, not a parse target.
	maxGoSourceBytes = 4 << 20

	// SHA-256 of the empty adapter configuration.
	adapterConfigDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	predicateProduces           = "PRODUCES_TO_TOPIC"
	predicateConsumes           = "CONSUMES_FROM_TOPIC"
	predicateUnresolvedProducer = "UNRESOLVED_KAFKA_PRODUCER"
	predicateUnresolvedConsumer = "UNRESOLVED_KAFKA_CONSUMER"
	predicateGap                = "KAFKA_EXTRACTION_GAP"
)

// Library import paths recognized in round one (KD3). franz-go is a
// recorded deferred family, never silently admitted.
const (
	importSaramaShopify = "github.com/Shopify/sarama"
	importSaramaIBM     = "github.com/IBM/sarama"
	importSegmentio     = "github.com/segmentio/kafka-go"
)

// NewProducer returns the kafka-producer plane extractor.
func NewProducer() sdk.Extractor { return kafkaGo{plane: "producer", domain: producerDomain} }

// NewConsumer returns the kafka-consumer plane extractor.
func NewConsumer() sdk.Extractor { return kafkaGo{plane: "consumer", domain: consumerDomain} }

type kafkaGo struct {
	plane  string
	domain string
}

func (k kafkaGo) Domain() string  { return k.domain }
func (k kafkaGo) Version() string { return version }

// Candidate admits every Go file; eligibility (round-one import present,
// not a _test.go fixture) is decided inside Extract per KD5 so the worker's
// read discipline still covers the whole population.
func (k kafkaGo) Candidate(filePath string) bool {
	return strings.HasSuffix(filePath, ".go")
}

func (k kafkaGo) Extract(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	coverage := sdk.Coverage{Protocols: []string{"kafka"}}
	repo := corpus.RepoName()
	unresolved := make(map[string]struct{})
	err := corpus.WalkFiles(ctx, func(relPath string) error {
		if !k.Candidate(relPath) {
			return nil
		}
		blob, err := corpus.Read(ctx, relPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", relPath, err)
		}
		// KD5: fixture literals in test files are authored noise, not
		// production topic evidence (the jaeger v1 ingester's invented
		// "morekuzambu" topic froze this exclusion).
		if strings.HasSuffix(relPath, "_test.go") {
			return nil
		}
		if len(blob.Content) > maxGoSourceBytes {
			return k.emitGap(emit, relPath, blob, "source_too_large", unresolved)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, relPath, blob.Content, parser.SkipObjectResolution)
		if err != nil {
			return k.emitGap(emit, relPath, blob, "parse_error", unresolved)
		}
		aliases := libraryAliases(file)
		if len(aliases) == 0 {
			return nil
		}
		scan := &fileScan{file: file, fset: fset, aliases: aliases}
		ast.Inspect(file, scan.visit)
		if err := k.emitEvidenceGroups(emit, repo, relPath, blob, scan.evidence); err != nil {
			return err
		}
		for _, row := range scan.abstentions {
			if row.plane != k.plane {
				continue
			}
			if err := k.emitUnresolved(emit, relPath, blob, row, unresolved); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return sdk.Coverage{}, err
	}
	coverage.UnresolvedCount = len(unresolved)
	return coverage, nil
}

func (k kafkaGo) evidencePredicate() string {
	if k.plane == "producer" {
		return predicateProduces
	}
	return predicateConsumes
}

func (k kafkaGo) unresolvedPredicate() string {
	if k.plane == "producer" {
		return predicateUnresolvedProducer
	}
	return predicateUnresolvedConsumer
}

// emitEvidenceGroups emits all of one file's evidence for this plane,
// grouped by topic. ComputeAssertionID hashes (predicate, subject, object,
// lineage, repo) and the store rejects duplicate IDs whose (tier, code_role,
// detail) differ, so every site sharing one claim identity must share one
// assertion attribute tuple exactly: the detail is a canonical aggregate
// over the group's sites (sorted, deduplicated), per-site spans live only
// on the atoms, and the store merges the supporting atom set. The group's
// tier is the strongest site binding — the claim asserts the relationship
// exists, and its strength is the best independent evidence for it; the
// shapes list keeps the heuristic members visible.
func (k kafkaGo) emitEvidenceGroups(emit sdk.Emit, repo, relPath string, blob sdk.Blob, rows []topicEvidence) error {
	predicate := k.evidencePredicate()
	role := classifyRole(relPath, blob.Content)
	groups := make(map[string][]topicEvidence)
	order := make([]string, 0)
	for _, row := range rows {
		if row.plane != k.plane {
			continue
		}
		if _, seen := groups[row.topic]; !seen {
			order = append(order, row.topic)
		}
		groups[row.topic] = append(groups[row.topic], row)
	}
	for _, topic := range order {
		sites := groups[topic]
		object := "topic:" + topic
		tier := "heuristic"
		var libraries, importPaths, shapes, bindings, groupIDs []string
		for _, site := range sites {
			if site.tier == "derived" {
				tier = "derived"
			}
			libraries = append(libraries, site.library)
			importPaths = append(importPaths, site.importPath)
			shapes = append(shapes, site.shape)
			bindings = append(bindings, site.binding)
			if site.groupID != "" {
				groupIDs = append(groupIDs, site.groupID)
			}
		}
		detail := `{"schema":"kafka-topic-evidence-detail-v1"` +
			`,"libraries":` + quotedList(libraries) +
			`,"import_paths":` + quotedList(importPaths) +
			`,"shapes":` + quotedList(shapes) +
			`,"bindings":` + quotedList(bindings)
		if len(groupIDs) > 0 {
			detail += `,"group_ids":` + quotedList(groupIDs)
		}
		detail += `}`
		for _, site := range sites {
			if err := validSpan(site.startByte, site.endByte, len(blob.Content)); err != nil {
				return fmt.Errorf("evidence span in %s: %w", relPath, err)
			}
			if err := emit(sdk.Fact{
				Atom: sdk.AtomInput{
					SchemaVersion:       schemaVersion,
					BlobDigest:          blob.Digest,
					StartByte:           site.startByte,
					EndByte:             site.endByte,
					RuleID:              "kafkago-topic-v1",
					AdapterConfigDigest: adapterConfigDigest,
					FactFingerprint:     predicate + "|" + object,
				},
				Assertion: sdk.AssertionInput{
					Predicate: predicate,
					Subject:   relPath,
					Object:    object,
					Lineage:   lineageID(repo, relPath),
					Tier:      tier,
					CodeRole:  role,
					Detail:    detail,
				},
				Path:      relPath,
				StartLine: site.startLine,
				EndLine:   site.endLine,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// quotedList renders a sorted, deduplicated JSON string array — the detail
// must be byte-identical however the sites were ordered.
func quotedList(values []string) string {
	unique := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	parts := make([]string, len(unique))
	for i, value := range unique {
		parts[i] = strconv.Quote(value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// emitUnresolved emits one UNRESOLVED_KAFKA_* row. The detail is a pure
// function of the object: ComputeAssertionID hashes (predicate, subject,
// object, lineage, repo) and excludes detail, so a file abstaining via two
// libraries under the same shape class collapses into one assertion —
// per-site spans stay on the atoms, and richer breakdowns belong to the
// spike census, never to a detail the identity cannot distinguish.
// Unresolved rows carry no lineage, mirroring thriftgo.
func (k kafkaGo) emitUnresolved(emit sdk.Emit, relPath string, blob sdk.Blob, row topicAbstention, unresolved map[string]struct{}) error {
	if err := validSpan(row.startByte, row.endByte, len(blob.Content)); err != nil {
		return fmt.Errorf("abstention span in %s: %w", relPath, err)
	}
	predicate := k.unresolvedPredicate()
	object := "unresolved:" + row.shape
	unresolved[predicate+"\x00"+relPath+"\x00"+object] = struct{}{}
	return emit(sdk.Fact{
		Atom: sdk.AtomInput{
			SchemaVersion:       schemaVersion,
			BlobDigest:          blob.Digest,
			StartByte:           row.startByte,
			EndByte:             row.endByte,
			RuleID:              "kafkago-unresolved-v1",
			AdapterConfigDigest: adapterConfigDigest,
			FactFingerprint:     predicate + "|" + object,
		},
		Assertion: sdk.AssertionInput{
			Predicate: predicate,
			Subject:   relPath,
			Object:    object,
			Tier:      "unresolved",
			CodeRole:  classifyRole(relPath, blob.Content),
			Detail:    `{"schema":"kafka-topic-unresolved-detail-v1","shape":` + strconv.Quote(row.shape) + `}`,
		},
		Path:      relPath,
		StartLine: row.startLine,
		EndLine:   row.endLine,
	})
}

// emitGap mirrors thriftgo's file gap verbatim: tier unresolved, the reason
// as object, whole-file span, no lineage. Gap assertions count toward the
// worker's distinct-unresolved contract.
func (k kafkaGo) emitGap(emit sdk.Emit, relPath string, blob sdk.Blob, reason string, unresolved map[string]struct{}) error {
	if len(blob.Content) == 0 {
		return nil
	}
	endLine := 1 + strings.Count(blob.Content[:len(blob.Content)-1], "\n")
	unresolved[predicateGap+"\x00"+relPath+"\x00"+reason] = struct{}{}
	return emit(sdk.Fact{
		Atom: sdk.AtomInput{
			SchemaVersion:       schemaVersion,
			BlobDigest:          blob.Digest,
			StartByte:           0,
			EndByte:             len(blob.Content),
			RuleID:              "kafkago-file-gap-v1",
			AdapterConfigDigest: adapterConfigDigest,
			FactFingerprint:     predicateGap + "|" + reason,
		},
		Assertion: sdk.AssertionInput{
			Predicate: predicateGap,
			Subject:   relPath,
			Object:    reason,
			Tier:      "unresolved",
			CodeRole:  classifyRole(relPath, blob.Content),
			Detail:    `{"schema":"kafkago-gap-detail-v1","reason":` + strconv.Quote(reason) + `}`,
		},
		Path:      relPath,
		StartLine: 1,
		EndLine:   endLine,
	})
}

func validSpan(start, end, size int) error {
	if start < 0 || end <= start || end > size {
		return fmt.Errorf("invalid span [%d,%d) for %d bytes", start, end, size)
	}
	return nil
}

func lineageID(repo, path string) string {
	h := sha256.Sum256([]byte(repo + "\x00" + path))
	return "provisional_repo_path_v1_" + hex.EncodeToString(h[:])
}

// validTopicLiteral applies Kafka's own topic-name constraints: 1..249
// characters from [a-zA-Z0-9._-], excluding the reserved "." and "..".
func validTopicLiteral(topic string) bool {
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

type topicEvidence struct {
	library    string
	plane      string
	topic      string
	binding    string // "literal" | "same-file-const"
	shape      string
	tier       string
	groupID    string
	importPath string
	startByte  int
	endByte    int
	startLine  int
	endLine    int
}

type topicAbstention struct {
	library    string
	plane      string
	shape      string // frozen six-class vocabulary
	tier       string
	importPath string
	startByte  int
	endByte    int
	startLine  int
	endLine    int
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
	file        *ast.File
	fset        *token.FileSet
	aliases     map[string]libraryImport
	evidence    []topicEvidence
	abstentions []topicAbstention
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
		// Heuristic by design (KD9): the receiver's type is not resolvable
		// without type-checking, so a three-argument Consume in a
		// sarama-importing file is treated as sarama's
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

// resolveTopic applies the literal-or-abstain boundary (KD6): a string
// literal or a same-file const resolves; everything else abstains with its
// shape class. There is no dataflow, no cross-file resolution, and no var
// admission — a var is mutable state, not a declaration-strength spelling.
func (s *fileScan) resolveTopic(expr ast.Expr, imported libraryImport, plane, shape, group string) {
	tier := tierForShape(shape)
	value, binding, ok := s.stringConstant(expr)
	if !ok {
		s.abstain(imported, plane, shapeClass(expr), tier, expr)
		return
	}
	if !validTopicLiteral(value) {
		s.abstain(imported, plane, "invalid-topic-literal", tier, expr)
		return
	}
	if plane != "consumer" {
		group = ""
	}
	start, end, line, endLine := s.sourceSite(expr)
	s.evidence = append(s.evidence, topicEvidence{
		library:    imported.library,
		plane:      plane,
		topic:      value,
		binding:    binding,
		shape:      shape,
		tier:       tier,
		groupID:    group,
		importPath: imported.importPath,
		startByte:  start,
		endByte:    end,
		startLine:  line,
		endLine:    endLine,
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

// stringConstant evaluates a string literal or a same-file const identifier.
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
		for _, decl := range s.file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
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
			}
		}
		return "", "", false
	default:
		return "", "", false
	}
}

func shapeClass(expr ast.Expr) string {
	switch expr.(type) {
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

func (s *fileScan) abstain(imported libraryImport, plane, shape, tier string, node ast.Node) {
	start, end, line, endLine := s.sourceSite(node)
	s.abstentions = append(s.abstentions, topicAbstention{
		library:    imported.library,
		plane:      plane,
		shape:      shape,
		tier:       tier,
		importPath: imported.importPath,
		startByte:  start,
		endByte:    end,
		startLine:  line,
		endLine:    endLine,
	})
}

func (s *fileScan) sourceSite(node ast.Node) (int, int, int, int) {
	start := s.fset.PositionFor(node.Pos(), false)
	end := s.fset.PositionFor(node.End(), false)
	return start.Offset, end.Offset, start.Line, s.fset.PositionFor(node.End()-1, false).Line
}

// ---- code_role classification, ported verbatim from grpcgo/T11.1 ----

const (
	roleProduction = "production"
	roleTest       = "test"
	roleMock       = "mock"
	roleGenerated  = "generated"
	roleVendor     = "vendor"
)

var (
	generatedHeaderRe = regexp.MustCompile(`(?m)^(//|#).*(Code generated .* DO NOT EDIT|Autogenerated by Thrift Compiler)`)
	mockHeaderRe      = regexp.MustCompile(`Code generated by MockGen|Code generated by mockery|Code generated by counterfeiter`)
)

// classifyRole implements the spike's validated precedence:
// vendor > mock > generated > test > production.
func classifyRole(relPath, content string) string {
	if pathHasSegment(relPath, "vendor") {
		return roleVendor
	}
	head := content
	if len(head) > 4096 {
		head = head[:4096]
	}
	if mockHeaderRe.MatchString(head) || pathHasMockSegment(relPath) {
		return roleMock
	}
	if generatedHeaderRe.MatchString(head) {
		return roleGenerated
	}
	if strings.HasSuffix(relPath, "_test.go") || hasTestSegment(relPath) {
		return roleTest
	}
	return roleProduction
}

func hasTestSegment(relPath string) bool {
	for _, seg := range strings.Split(relPath, "/") {
		if seg == "tests" || seg == "testing" || seg == "testdata" {
			return true
		}
	}
	return false
}

func pathHasSegment(relPath, want string) bool {
	for _, seg := range strings.Split(relPath, "/") {
		if seg == want {
			return true
		}
	}
	return false
}

func pathHasMockSegment(relPath string) bool {
	parts := strings.Split(relPath, "/")
	for i, seg := range parts {
		isFile := i == len(parts)-1
		if seg == "mock" || seg == "mocks" || seg == "mocksgen" || seg == "fake" || seg == "fakes" {
			return true
		}
		if isFile && (strings.HasPrefix(seg, "mock_") || strings.HasPrefix(seg, "fake_") ||
			strings.HasSuffix(seg, "_mock.go") || strings.HasSuffix(seg, "_mock_test.go") ||
			strings.HasSuffix(seg, "_fake.go") || strings.HasSuffix(seg, "_fake_test.go")) {
			return true
		}
	}
	return false
}

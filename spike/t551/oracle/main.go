//go:build t551oracle

// Command oracle is the T55.1 independent Kafka producer-site oracle. It is
// written from the frozen T23.1 decision table (spike/t231/README.md KD1-KD10)
// and the phebs.kafka.producer card only: it reads no phebs code or output and
// shares no code with the kafkago extractor or the T23.1 prototype recognizer.
//
// For each eligible file (non-test .go importing a round-one library, KD3/KD5)
// it lists every producer site of KD4's three qualified shapes and classifies
// the Topic expression by KD2/KD6: resolved `topic:<literal>` with binding
// literal or same-file-const, or one of the six abstention classes. A site
// with no Topic key, and a file that does not parse, are listed with their own
// states rather than dropped.
package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var consumerPlane bool

var (
	saramaPaths  = map[string]string{"github.com/Shopify/sarama": "Shopify", "github.com/IBM/sarama": "IBM"}
	segmentio    = "github.com/segmentio/kafka-go"
	topicPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,249}$`)
)

type site struct {
	UnitID     string `json:"unit_id,omitempty"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	Shape      string `json:"shape"` // ProducerMessage | Writer | WriterConfig | Message
	Library    string `json:"library"`
	State      string `json:"state"` // resolved | unresolved | no_topic_field | parse_error
	Object     string `json:"object,omitempty"`
	Binding    string `json:"binding,omitempty"`
	Class      string `json:"class,omitempty"`
	StartByte  int    `json:"start_byte"`
	EndByte    int    `json:"end_byte"`
	StartLine  int    `json:"start_line"`
}

func main() {
	repo := flag.String("repo", "", "checkout directory")
	name := flag.String("name", "", "repository name")
	units := flag.String("units", "", "population units.jsonl.gz; each unit's blob is read from -history")
	history := flag.String("history", "", "directory of bare history clones, one per unit dir")
	plane := flag.String("plane", "producer", "producer (T55.1) or consumer (T56.1)")
	flag.Parse()
	consumerPlane = *plane == "consumer"
	if *units != "" {
		scanUnits(*units, *history)
		return
	}
	commit := strings.TrimSpace(run(*repo, "git", "rev-parse", "HEAD"))
	var files []string
	for _, f := range strings.Split(run(*repo, "git", "ls-files", "-z", "--", "*.go"), "\x00") {
		if f != "" && !strings.HasSuffix(f, "_test.go") && !vendored(f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)
	for _, file := range files {
		content, err := os.ReadFile(path.Join(*repo, file))
		if err != nil {
			fail(err)
		}
		for _, s := range scan(content) {
			s.Repository, s.Commit, s.Path = *name, commit, file
			if err := enc.Encode(s); err != nil {
				fail(err)
			}
		}
	}
}

type unit struct {
	UnitID     string `json:"unit_id"`
	Repository string `json:"repository"`
	Dir        string `json:"dir"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	Blob       string `json:"blob"`
	SHA256     string `json:"sha256"`
}

// scanUnits scores each population unit's exact blob, refusing a digest mismatch.
func scanUnits(unitsPath, history string) {
	raw, err := os.Open(unitsPath)
	if err != nil {
		fail(err)
	}
	defer raw.Close()
	zr, err := gzip.NewReader(raw)
	if err != nil {
		fail(err)
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)
	dec := json.NewDecoder(zr)
	for dec.More() {
		var u unit
		if err := dec.Decode(&u); err != nil {
			fail(err)
		}
		content := []byte(run(path.Join(history, u.Dir), "git", "cat-file", "blob", u.Blob))
		if sum := sha256.Sum256(content); "sha256:"+hex.EncodeToString(sum[:]) != u.SHA256 {
			fail(fmt.Errorf("%s: blob does not match its frozen digest", u.UnitID))
		}
		for _, s := range scan(content) {
			s.UnitID, s.Repository, s.Commit, s.Path = u.UnitID, u.Repository, u.Commit, u.Path
			if err := enc.Encode(s); err != nil {
				fail(err)
			}
		}
	}
}

func vendored(f string) bool {
	return strings.HasPrefix(f, "vendor/") || strings.Contains(f, "/vendor/") || strings.HasPrefix(f, "third_party/")
}

// scan returns the producer sites of one file, or one parse_error row for an
// eligible-looking file the Go parser rejects.
func scan(content []byte) []site {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "f.go", content, parser.ParseComments)
	if err != nil {
		if strings.Contains(string(content), saramaQuoted("Shopify")) || strings.Contains(string(content), saramaQuoted("IBM")) ||
			strings.Contains(string(content), strconv.Quote(segmentio)) {
			return []site{{State: "parse_error"}}
		}
		return nil
	}
	aliases := map[string]string{} // local name -> library
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		library := ""
		if era, ok := saramaPaths[importPath]; ok {
			library = "sarama-" + era
		} else if importPath == segmentio {
			library = "segmentio"
		}
		if library == "" {
			continue
		}
		local := path.Base(importPath)
		if importPath == segmentio {
			local = "kafka"
		}
		if spec.Name != nil {
			local = spec.Name.Name
		}
		if local == "." || local == "_" {
			continue // dot and blank imports are refused by omission (KD4)
		}
		aliases[local] = library
	}
	if len(aliases) == 0 {
		return nil
	}
	if consumerPlane {
		return scanConsumer(fset, file, aliases)
	}
	direct := map[*ast.CompositeLit]bool{} // Message composites passed straight to .WriteMessages
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WriteMessages" {
			for _, arg := range call.Args {
				if lit := composite(arg); lit != nil {
					direct[lit] = true
				}
			}
		}
		return true
	})
	var sites []site
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		library, ok := aliases[pkg.Name]
		if !ok {
			return true
		}
		shape := sel.Sel.Name
		switch {
		case strings.HasPrefix(library, "sarama") && shape == "ProducerMessage":
		case library == "segmentio" && (shape == "Writer" || shape == "WriterConfig"):
		case library == "segmentio" && shape == "Message" && direct[lit]:
		default:
			return true
		}
		s := site{Shape: shape, Library: library, State: "no_topic_field",
			StartByte: fset.Position(lit.Pos()).Offset, EndByte: fset.Position(lit.End()).Offset,
			StartLine: fset.Position(lit.Pos()).Line}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Topic" {
				s.StartByte, s.EndByte = fset.Position(kv.Value.Pos()).Offset, fset.Position(kv.Value.End()).Offset
				s.StartLine = fset.Position(kv.Value.Pos()).Line
				classify(&s, kv.Value)
			}
		}
		sites = append(sites, s)
		return true
	})
	return sites
}

// scanConsumer lists KD4's consumer sites: a segmentio ReaderConfig's Topic
// and each GroupTopics element (an expression list is one site per element,
// any other GroupTopics value one site), and the receiver-untyped sarama
// shapes .Consume(ctx, topics, handler) (each element of a literal list, else
// the topics argument) and .ConsumePartition(topic, partition, offset). The
// receiver-untyped shapes count only in files importing sarama, and a file
// importing both sarama eras abstains ambiguous-library-import on them (KD6).
func scanConsumer(fset *token.FileSet, file *ast.File, aliases map[string]string) []site {
	eras := map[string]bool{}
	for _, library := range aliases {
		if strings.HasPrefix(library, "sarama") {
			eras[library] = true
		}
	}
	saramaLibrary := ""
	for era := range eras {
		saramaLibrary = era
	}
	if len(eras) > 1 {
		saramaLibrary = "sarama-ambiguous"
	}
	var sites []site
	add := func(shape, library string, expr ast.Expr) {
		s := site{Shape: shape, Library: library, StartByte: fset.Position(expr.Pos()).Offset,
			EndByte: fset.Position(expr.End()).Offset, StartLine: fset.Position(expr.Pos()).Line}
		if library == "sarama-ambiguous" {
			s.State, s.Class = "unresolved", "ambiguous-library-import"
		} else {
			classify(&s, expr)
		}
		sites = append(sites, s)
	}
	each := func(shape, library string, expr ast.Expr) {
		if list, ok := expr.(*ast.CompositeLit); ok {
			for _, element := range list.Elts {
				add(shape, library, element)
			}
			return
		}
		add(shape, library, expr)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			sel, ok := node.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || aliases[pkg.Name] != "segmentio" || sel.Sel.Name != "ReaderConfig" {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				switch key, _ := kv.Key.(*ast.Ident); {
				case key != nil && key.Name == "Topic":
					add("ReaderConfig.Topic", "segmentio", kv.Value)
				case key != nil && key.Name == "GroupTopics":
					each("ReaderConfig.GroupTopics", "segmentio", kv.Value)
				}
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || saramaLibrary == "" || len(node.Args) != 3 {
				return true
			}
			switch sel.Sel.Name {
			case "Consume":
				each("Consume", saramaLibrary, node.Args[1])
			case "ConsumePartition":
				add("ConsumePartition", saramaLibrary, node.Args[0])
			}
		}
		return true
	})
	return sites
}

func composite(expr ast.Expr) *ast.CompositeLit {
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		expr = unary.X
	}
	lit, _ := expr.(*ast.CompositeLit)
	return lit
}

// classify applies KD2/KD6 to one Topic expression.
func classify(s *site, expr ast.Expr) {
	resolve := func(value, binding string) {
		if topicPattern.MatchString(value) && value != "." && value != ".." {
			s.State, s.Object, s.Binding = "resolved", "topic:"+value, binding
		} else {
			s.State, s.Class = "unresolved", "invalid-topic-literal"
		}
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			value, err := strconv.Unquote(e.Value)
			if err == nil {
				resolve(value, "literal")
				return
			}
		}
		s.State, s.Class = "unresolved", "non-literal-expr"
	case *ast.Ident:
		if value, ok := sameFileConst(e); ok {
			resolve(value, "same-file-const")
			return
		}
		s.State, s.Class = "unresolved", "unresolved-ident"
	case *ast.SelectorExpr:
		s.State, s.Class = "unresolved", "selector-expr"
	case *ast.CallExpr:
		s.State, s.Class = "unresolved", "call-expr"
	default:
		s.State, s.Class = "unresolved", "non-literal-expr"
	}
}

// sameFileConst follows the parser's lexical object binding only: an
// identifier bound in this file to a const whose own explicit initializer is a
// string literal. Anything else (a var, an unbound name, a const of another
// expression, an implicit iota repetition) is not resolved.
func sameFileConst(ident *ast.Ident) (string, bool) {
	//nolint:staticcheck // lexical binding is exactly what KD6 specifies
	obj := ident.Obj
	if obj == nil || obj.Kind != ast.Con {
		return "", false
	}
	spec, ok := obj.Decl.(*ast.ValueSpec)
	if !ok {
		return "", false
	}
	for i, n := range spec.Names {
		if n.Name != ident.Name || i >= len(spec.Values) {
			continue
		}
		if lit, ok := spec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, err := strconv.Unquote(lit.Value)
			return value, err == nil
		}
	}
	return "", false
}

func saramaQuoted(era string) string { return strconv.Quote("github.com/" + era + "/sarama") }

func run(dir string, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		fail(fmt.Errorf("%s %v: %w", name, args, err))
	}
	return string(out)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

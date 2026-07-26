// T23.1 gates. Offline tests (synthetic recognition, identity rules,
// vocabulary) always run against committed bytes; corpus gates K1–K6 skip
// without pinned clones — set T231_FETCH=1 to clone and pin on demand, or
// point T231_CORPUS_ROOT at existing pinned clones.
package t231

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func corpusDirs(t *testing.T) map[string]string {
	t.Helper()
	entries, err := LoadCorpusLock("corpus.lock.json")
	if err != nil {
		t.Fatalf("load corpus lock: %v", err)
	}
	root := CorpusRoot(".")
	dirs := make(map[string]string, len(entries))
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name)
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
			if os.Getenv("T231_FETCH") == "" {
				t.Skipf("corpus %s not fetched; run with T231_FETCH=1", entry.Name)
			}
			if _, err := EnsureCorpus(root, entry); err != nil {
				t.Fatalf("ensure corpus %s: %v", entry.Name, err)
			}
		} else if err := VerifyCorpusCheckout(dir, entry); err != nil {
			t.Fatalf("verify corpus %s: %v", entry.Name, err)
		}
		dirs[entry.Name] = dir
	}
	return dirs
}

// The frozen abstention vocabulary. A same-file var deliberately reports as
// unresolved-ident: round one does not distinguish mutable spellings from
// unknown ones (decision table KD6).
var knownAbstentionShapes = map[string]bool{
	"selector-expr":         true,
	"call-expr":             true,
	"unresolved-ident":      true,
	"non-literal-expr":      true,
	"invalid-topic-literal": true,
}

// Offline — the synthetic canonical shapes freeze the recognition rules:
// every labeled positive binds with its exact topic, binding, plane, and
// shape; every labeled negative abstains with its exact shape class.
func TestSyntheticRecognition(t *testing.T) {
	saramaEvidence, saramaAbstentions, err := ScanFile(
		filepath.Join("testdata", "synthetic", "sarama_shapes.go"), "sarama_shapes.go")
	if err != nil {
		t.Fatal(err)
	}
	wantSarama := []TopicEvidence{
		{Topic: "orders-v1", Plane: "producer", Binding: "literal", Shape: "ProducerMessage"},
		{Topic: "audit-v2", Plane: "producer", Binding: "same-file-const", Shape: "ProducerMessage"},
		{Topic: "orders-v1", Plane: "consumer", Binding: "literal", Shape: "Consume-slice"},
		{Topic: "payments", Plane: "consumer", Binding: "literal", Shape: "Consume-slice"},
		{Topic: "clicks", Plane: "consumer", Binding: "literal", Shape: "ConsumePartition"},
	}
	assertEvidence(t, "sarama synthetic", saramaEvidence, wantSarama, "sarama", importSaramaIBM)
	wantSaramaAbstain := map[string]int{"unresolved-ident": 2, "call-expr": 1, "invalid-topic-literal": 1}
	assertAbstentions(t, "sarama synthetic", saramaAbstentions, wantSaramaAbstain)

	segmentioEvidence, segmentioAbstentions, err := ScanFile(
		filepath.Join("testdata", "synthetic", "segmentio_shapes.go"), "segmentio_shapes.go")
	if err != nil {
		t.Fatal(err)
	}
	wantSegmentio := []TopicEvidence{
		{Topic: "orders-v1", Plane: "producer", Binding: "literal", Shape: "Writer"},
		{Topic: "audit-log", Plane: "producer", Binding: "literal", Shape: "WriterConfig"},
		{Topic: "orders-v1", Plane: "consumer", Binding: "literal", Shape: "ReaderConfig.Topic", GroupID: "billing"},
		{Topic: "orders-v1", Plane: "consumer", Binding: "literal", Shape: "ReaderConfig.GroupTopics", GroupID: "billing"},
		{Topic: "refunds", Plane: "consumer", Binding: "literal", Shape: "ReaderConfig.GroupTopics", GroupID: "billing"},
	}
	assertEvidence(t, "segmentio synthetic", segmentioEvidence, wantSegmentio, "segmentio", importSegmentio)
	assertAbstentions(t, "segmentio synthetic", segmentioAbstentions, map[string]int{"call-expr": 1})
}

func assertEvidence(t *testing.T, label string, got []TopicEvidence, want []TopicEvidence, library, importPath string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d evidence rows, want %d: %+v", label, len(got), len(want), got)
	}
	matched := make([]bool, len(want))
	for _, row := range got {
		if row.Library != library || row.Import != importPath {
			t.Fatalf("%s: row %+v has wrong library/import", label, row)
		}
		found := false
		for i, expected := range want {
			if matched[i] || row.Topic != expected.Topic || row.Plane != expected.Plane ||
				row.Binding != expected.Binding || row.Shape != expected.Shape || row.GroupID != expected.GroupID {
				continue
			}
			matched[i], found = true, true
			break
		}
		if !found {
			t.Fatalf("%s: unexpected evidence row %+v", label, row)
		}
	}
}

func assertAbstentions(t *testing.T, label string, got []TopicAbstention, want map[string]int) {
	t.Helper()
	counts := make(map[string]int)
	for _, abstention := range got {
		counts[abstention.Shape]++
	}
	if len(counts) != len(want) {
		t.Fatalf("%s: abstention shapes %v, want %v", label, counts, want)
	}
	for shape, count := range want {
		if counts[shape] != count {
			t.Fatalf("%s: %d %s abstentions, want %d", label, counts[shape], shape, count)
		}
	}
}

// Offline — identity rules: Kafka's own topic constraints bound the literal,
// the object spelling carries the literal and nothing else, and group ids
// are structurally excluded from identity.
func TestTopicIdentityRules(t *testing.T) {
	valid := []string{"a", "orders-v1", "a.b_c-9", strings.Repeat("x", 249)}
	for _, topic := range valid {
		if !ValidTopicLiteral(topic) {
			t.Fatalf("topic %q should be valid", topic)
		}
	}
	invalid := []string{"", "bad topic!", "utf8-héllo", strings.Repeat("x", 250)}
	for _, topic := range invalid {
		if ValidTopicLiteral(topic) {
			t.Fatalf("topic %q should be invalid", topic)
		}
	}
	if got := TopicObject("orders-v1"); got != "topic:orders-v1" {
		t.Fatalf("object spelling %q", got)
	}
}

// K1 — corpus survey: the import-path era split is real (jaeger v1
// production code is on the Shopify path, the sarama corpus is on the IBM
// path) and each corpus exercises exactly the expected library.
func TestK1CorpusSurvey(t *testing.T) {
	dirs := corpusDirs(t)
	counts := make(map[string]map[string]int)
	for name, subdirs := range map[string][]string{
		"jaeger-v1": {"plugin/storage/kafka", "cmd/ingester"},
		"sarama":    {"examples"},
		"kafka-go":  {"examples"},
	} {
		counts[name] = map[string]int{}
		for _, subdir := range subdirs {
			err := walkGo(dirs[name], subdir, func(rel, full string) error {
				fset := token.NewFileSet()
				file, err := parser.ParseFile(fset, full, nil, parser.ImportsOnly)
				if err != nil {
					return fmt.Errorf("parse %s: %w", rel, err)
				}
				for _, imported := range libraryAliases(file) {
					counts[name][imported.importPath]++
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if counts["jaeger-v1"][importSaramaShopify] == 0 || counts["jaeger-v1"][importSaramaIBM] != 0 {
		t.Fatalf("jaeger-v1 import survey unexpected: %v", counts["jaeger-v1"])
	}
	if counts["sarama"][importSaramaIBM] == 0 {
		t.Fatalf("sarama examples import survey unexpected: %v", counts["sarama"])
	}
	if counts["kafka-go"][importSegmentio] == 0 {
		t.Fatalf("kafka-go examples import survey unexpected: %v", counts["kafka-go"])
	}
	t.Logf("K1: import survey %v", counts)
}

// K2 — sarama producer plane: the qualified literal examples bind exactly,
// and jaeger v1's production writer abstains on its configuration-driven
// selector topic. Hand labels: sarama examples/http_server carries topics
// "important" and "access_log"; jaeger writer.go sends Topic: w.topic.
func TestK2SaramaProducer(t *testing.T) {
	dirs := corpusDirs(t)
	evidence, abstentions, _, err := ScanTree(dirs["sarama"], "examples/http_server")
	if err != nil {
		t.Fatal(err)
	}
	topics := map[string]bool{}
	for _, row := range evidence {
		if row.Plane != "producer" || row.Binding != "literal" || row.Shape != "ProducerMessage" || row.Import != importSaramaIBM {
			t.Fatalf("unexpected evidence row %+v", row)
		}
		topics[row.Topic] = true
	}
	if len(topics) != 2 || !topics["important"] || !topics["access_log"] {
		t.Fatalf("http_server topics %v, hand-labeled {important, access_log}", topics)
	}
	if len(abstentions) != 0 {
		t.Fatalf("http_server unexpectedly abstained: %+v", abstentions)
	}

	jaegerEvidence, jaegerAbstentions, _, err := ScanTree(dirs["jaeger-v1"], "plugin/storage/kafka")
	if err != nil {
		t.Fatal(err)
	}
	if len(jaegerEvidence) != 0 {
		t.Fatalf("jaeger production kafka storage yielded literal evidence: %+v", jaegerEvidence)
	}
	writerAbstained := false
	for _, abstention := range jaegerAbstentions {
		if abstention.Plane == "producer" && abstention.Shape == "selector-expr" && strings.HasSuffix(abstention.File, "writer.go") {
			writerAbstained = true
		}
	}
	if !writerAbstained {
		t.Fatalf("jaeger writer.go selector topic did not abstain: %+v", jaegerAbstentions)
	}
	t.Logf("K2: sarama literals bind (2 topics), jaeger production writer abstains selector-expr (%d total abstentions)", len(jaegerAbstentions))
}

// K3 — sarama consumer plane: the consumergroup example's
// strings.Split(...) topics abstain as call-expr, and the jaeger v1
// ingester yields zero literal consumer evidence once _test.go files are
// excluded — its tests consume the invented topic "morekuzambu", the
// finding that froze the test-file exclusion (KD5).
func TestK3SaramaConsumer(t *testing.T) {
	dirs := corpusDirs(t)
	evidence, abstentions, _, err := ScanTree(dirs["sarama"], "examples/consumergroup")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range evidence {
		if row.Plane == "consumer" {
			t.Fatalf("consumergroup example unexpectedly bound: %+v", row)
		}
	}
	splitAbstained := false
	for _, abstention := range abstentions {
		if abstention.Plane == "consumer" && abstention.Shape == "call-expr" {
			splitAbstained = true
		}
	}
	if !splitAbstained {
		t.Fatalf("consumergroup Consume(strings.Split(...)) did not abstain: %+v", abstentions)
	}

	ingesterEvidence, ingesterAbstentions, scanned, err := ScanTree(dirs["jaeger-v1"], "cmd/ingester")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range ingesterEvidence {
		if row.Plane == "consumer" {
			t.Fatalf("jaeger ingester unexpectedly bound a literal consumer topic: %+v", row)
		}
	}
	t.Logf("K3: consumergroup abstains call-expr; jaeger ingester %d files, %d evidence, %d abstentions", scanned, len(ingesterEvidence), len(ingesterAbstentions))
}

// K4 — segmentio plane: kafka-go's own examples are entirely
// environment-driven, so the corpus yields zero literal evidence and only
// known abstention shapes. The positive segmentio shapes are frozen by the
// synthetic fixtures, and that provenance is disclosed in the README.
func TestK4SegmentioExamples(t *testing.T) {
	dirs := corpusDirs(t)
	evidence, abstentions, scanned, err := ScanTree(dirs["kafka-go"], "examples")
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 0 {
		t.Fatalf("kafka-go examples unexpectedly yielded literal evidence: %+v", evidence)
	}
	if len(abstentions) == 0 {
		t.Fatal("kafka-go examples yielded no abstentions; expected environment-driven topics to abstain")
	}
	for _, abstention := range abstentions {
		if !knownAbstentionShapes[abstention.Shape] {
			t.Fatalf("unknown abstention shape %+v", abstention)
		}
	}
	t.Logf("K4: %d files, 0 literal evidence, %d abstentions — production examples are config-driven", scanned, len(abstentions))
}

// K5 — declarations plane: no corpus carries a qualified in-code topic
// declaration (kafka.TopicConfig composite) outside the library's own
// implementation. The round-one verdict — producer/consumer planes only, no
// catalog/Atlas surface — rests on this recorded absence.
func TestK5DeclarationsPlaneVerdict(t *testing.T) {
	dirs := corpusDirs(t)
	declarations := 0
	for name, subdirs := range map[string][]string{
		"jaeger-v1": {"plugin/storage/kafka", "cmd/ingester"},
		"sarama":    {"examples"},
		"kafka-go":  {"examples"},
	} {
		for _, subdir := range subdirs {
			err := walkGo(dirs[name], subdir, func(rel, full string) error {
				count, err := countQualifiedComposites(full, "segmentio", "TopicConfig")
				if err != nil {
					return err
				}
				declarations += count
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if declarations != 0 {
		t.Fatalf("found %d TopicConfig declaration candidates; the round-one declarations-plane verdict must be revisited", declarations)
	}
	t.Log("K5: zero in-code topic declarations across all corpora; declarations plane stays honestly empty in round one")
}

func countQualifiedComposites(path, library, typeName string) (int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	aliases := libraryAliases(file)
	if len(aliases) == 0 {
		return 0, nil
	}
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		selector, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		imported, ok := aliases[pkg.Name]
		if ok && imported.library == library && selector.Sel.Name == typeName {
			count++
		}
		return true
	})
	return count, nil
}

// K6 — census: the recognition sweep over every kafka-relevant subtree
// stays inside the frozen shape vocabulary, every bound topic passes the
// identity bounds, and the sanitized counts are recorded. The subtree
// scope (not whole-repo) is a recorded bound, not a silent cap.
func TestK6Census(t *testing.T) {
	dirs := corpusDirs(t)
	scopes := map[string][]string{
		"jaeger-v1": {"plugin/storage/kafka", "cmd/ingester"},
		"sarama":    {"examples", "mocks", "tools"},
		"kafka-go":  {"examples"},
	}
	totalEvidence, totalAbstentions, totalScanned := 0, 0, 0
	for name, subdirs := range scopes {
		for _, subdir := range subdirs {
			evidence, abstentions, scanned, err := ScanTree(dirs[name], subdir)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range evidence {
				if !ValidTopicLiteral(row.Topic) {
					t.Fatalf("census: bound topic fails identity bounds: %+v", row)
				}
				if row.Binding != "literal" && row.Binding != "same-file-const" {
					t.Fatalf("census: unknown binding %+v", row)
				}
			}
			for _, abstention := range abstentions {
				if !knownAbstentionShapes[abstention.Shape] {
					t.Fatalf("census: unknown abstention shape %+v", abstention)
				}
			}
			totalEvidence += len(evidence)
			totalAbstentions += len(abstentions)
			totalScanned += scanned
			t.Logf("K6: %s/%s — %d files, %d evidence, %d abstentions", name, subdir, scanned, len(evidence), len(abstentions))
		}
	}
	if totalScanned == 0 {
		t.Fatal("census scanned nothing")
	}
	// Record what the KD5 test-file exclusion leaves out — the excluded
	// rows are visible here, never silently dropped. The jaeger ingester
	// fixture topic must be among them.
	excluded := 0
	fixtureSeen := false
	for name, subdirs := range scopes {
		for _, subdir := range subdirs {
			err := walkGo(dirs[name], subdir, func(rel, full string) error {
				if !strings.HasSuffix(rel, "_test.go") {
					return nil
				}
				evidence, _, err := ScanFile(full, rel)
				if err != nil {
					return err
				}
				excluded += len(evidence)
				for _, row := range evidence {
					if row.Topic == "morekuzambu" {
						fixtureSeen = true
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if !fixtureSeen {
		t.Fatal("expected the jaeger ingester fixture topic among excluded test-file evidence")
	}
	t.Logf("K6 total: %d files, %d evidence, %d abstentions; %d test-file evidence rows excluded by KD5", totalScanned, totalEvidence, totalAbstentions, excluded)
}

// Offline — spike prose stays inside the no-accuracy-claim discipline.
func TestVocabularyDiscipline(t *testing.T) {
	banned := []string{"precision", "recall", "f1 score"}
	err := fs.WalkDir(os.DirFS("."), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".json") {
			return nil
		}
		if path == "gates_test.go" { // the file defining the ban list
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(content))
		for _, word := range banned {
			if strings.Contains(lower, word) {
				t.Errorf("%s contains banned vocabulary %q", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("README.md missing: %v", err)
	}
	for _, required := range []string{"not an accuracy claim", "GATE2-V2"} {
		if !strings.Contains(string(readme), required) {
			t.Fatalf("README.md missing required disclaimer %q", required)
		}
	}
}

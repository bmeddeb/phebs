package kafkago

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type fakeCorpus struct {
	files map[string]string
}

func (f fakeCorpus) RepoName() string { return "example.com/app" }
func (f fakeCorpus) Commit() string   { return "c0ffee" }

func (f fakeCorpus) WalkFiles(_ context.Context, visit func(path string) error) error {
	paths := make([]string, 0, len(f.files))
	for path := range f.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	content, ok := f.files[path]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("no such file %s", path)
	}
	return sdk.Blob{Content: content, Digest: "sha256:test-" + path}, nil
}

func runExtractor(t *testing.T, extractor sdk.Extractor, files map[string]string) ([]sdk.Fact, sdk.Coverage) {
	t.Helper()
	var facts []sdk.Fact
	coverage, err := extractor.Extract(context.Background(), fakeCorpus{files: files}, func(fact sdk.Fact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return facts, coverage
}

const saramaFixture = `package svc

import (
	"context"
	"os"
	"strings"

	"github.com/IBM/sarama"
)

const auditTopic = "audit-v2"

var mutableTopic = "orders-mutable"

func produce(p sarama.SyncProducer, w conf, cfgTopic string) {
	p.SendMessage(&sarama.ProducerMessage{Topic: "orders-v1"})
	p.SendMessage(&sarama.ProducerMessage{Topic: auditTopic})
	p.SendMessage(&sarama.ProducerMessage{Topic: mutableTopic})
	p.SendMessage(&sarama.ProducerMessage{Topic: cfgTopic})
	p.SendMessage(&sarama.ProducerMessage{Topic: os.Getenv("TOPIC")})
	p.SendMessage(&sarama.ProducerMessage{Topic: w.topic})
}

func consume(ctx context.Context, g sarama.ConsumerGroup, c sarama.Consumer, h sarama.ConsumerGroupHandler, topics string) {
	g.Consume(ctx, []string{"orders-v1", "payments"}, h)
	g.Consume(ctx, strings.Split(topics, ","), h)
	c.ConsumePartition("clicks", 0, sarama.OffsetNewest)
	c.ConsumePartition("bad topic!", 0, sarama.OffsetNewest)
}
`

const segmentioFixture = `package svc

import (
	"context"
	"os"

	kafka "github.com/segmentio/kafka-go"
)

const messageTopic = "per-message-const"

func writers(addr kafka.Addr, topics []string) {
	_ = &kafka.Writer{Addr: addr, Topic: "orders-v1"}
	_ = kafka.NewWriter(kafka.WriterConfig{Topic: "audit-log"})
	_ = &kafka.Writer{Addr: addr, Topic: os.Getenv("TOPIC")}
	_ = kafka.NewReader(kafka.ReaderConfig{GroupID: "billing", Topic: "orders-v1"})
	_ = kafka.NewReader(kafka.ReaderConfig{GroupID: "billing", GroupTopics: []string{"orders-v1", "refunds"}})
	_ = kafka.NewReader(kafka.ReaderConfig{GroupID: "billing", GroupTopics: topics})
}

func perMessage(w *kafka.Writer, r *kafka.Reader, ctx context.Context) {
	w.WriteMessages(ctx, kafka.Message{Topic: "per-message"}, kafka.Message{Topic: messageTopic})
	r.CommitMessages(ctx, kafka.Message{Topic: "offset-only"})
}
`

const ambiguousFixture = `package svc

import (
	"context"

	ibm "github.com/IBM/sarama"
	legacy "github.com/Shopify/sarama"
)

func consume(ctx context.Context, c legacy.Consumer, h ibm.ConsumerGroupHandler) {
	c.ConsumePartition("clicks", 0, legacy.OffsetNewest)
}
`

type wantRow struct {
	predicate string
	object    string
	tier      string
	spanText  string // exact source bytes the atom must cover
	detail    string // substring the detail must contain
}

func assertRows(t *testing.T, label, content string, facts []sdk.Fact, want []wantRow) {
	t.Helper()
	if len(facts) != len(want) {
		t.Fatalf("%s: %d facts, want %d: %+v", label, len(facts), len(want), facts)
	}
	for i, fact := range facts {
		expected := want[i]
		if fact.Assertion.Predicate != expected.predicate || fact.Assertion.Object != expected.object ||
			fact.Assertion.Tier != expected.tier {
			t.Fatalf("%s row %d: got (%s, %s, %s), want (%s, %s, %s)", label, i,
				fact.Assertion.Predicate, fact.Assertion.Object, fact.Assertion.Tier,
				expected.predicate, expected.object, expected.tier)
		}
		if got := content[fact.Atom.StartByte:fact.Atom.EndByte]; got != expected.spanText {
			t.Fatalf("%s row %d: span %q, want %q", label, i, got, expected.spanText)
		}
		if expected.detail != "" && !strings.Contains(fact.Assertion.Detail, expected.detail) {
			t.Fatalf("%s row %d: detail %q missing %q", label, i, fact.Assertion.Detail, expected.detail)
		}
	}
}

func TestSaramaProducerPlane(t *testing.T) {
	files := map[string]string{"svc/kafka.go": saramaFixture}
	facts, coverage := runExtractor(t, NewProducer(), files)
	assertRows(t, "producer", saramaFixture, facts, []wantRow{
		{"PRODUCES_TO_TOPIC", "topic:orders-v1", "derived", `"orders-v1"`, `"bindings":["literal"]`},
		{"PRODUCES_TO_TOPIC", "topic:audit-v2", "derived", `auditTopic`, `"bindings":["same-file-const"]`},
		{"UNRESOLVED_KAFKA_PRODUCER", "unresolved:unresolved-ident", "unresolved", `mutableTopic`, `"shape":"unresolved-ident"`},
		{"UNRESOLVED_KAFKA_PRODUCER", "unresolved:unresolved-ident", "unresolved", `cfgTopic`, ""},
		{"UNRESOLVED_KAFKA_PRODUCER", "unresolved:call-expr", "unresolved", `os.Getenv("TOPIC")`, ""},
		{"UNRESOLVED_KAFKA_PRODUCER", "unresolved:selector-expr", "unresolved", `w.topic`, ""},
	})
	// Two unresolved-ident sites collapse into one distinct assertion; the
	// worker counts distinct unresolved assertions, not sites.
	if coverage.UnresolvedCount != 3 {
		t.Fatalf("producer UnresolvedCount = %d, want 3", coverage.UnresolvedCount)
	}
}

func TestSaramaConsumerPlane(t *testing.T) {
	files := map[string]string{"svc/kafka.go": saramaFixture}
	facts, coverage := runExtractor(t, NewConsumer(), files)
	assertRows(t, "consumer", saramaFixture, facts, []wantRow{
		{"CONSUMES_FROM_TOPIC", "topic:orders-v1", "heuristic", `"orders-v1"`, `"shapes":["Consume-slice"]`},
		{"CONSUMES_FROM_TOPIC", "topic:payments", "heuristic", `"payments"`, ""},
		{"CONSUMES_FROM_TOPIC", "topic:clicks", "heuristic", `"clicks"`, `"shapes":["ConsumePartition"]`},
		{"UNRESOLVED_KAFKA_CONSUMER", "unresolved:call-expr", "unresolved", `strings.Split(topics, ",")`, ""},
		{"UNRESOLVED_KAFKA_CONSUMER", "unresolved:invalid-topic-literal", "unresolved", `"bad topic!"`, ""},
	})
	if coverage.UnresolvedCount != 2 {
		t.Fatalf("consumer UnresolvedCount = %d, want 2", coverage.UnresolvedCount)
	}
}

func TestSegmentioBothPlanes(t *testing.T) {
	files := map[string]string{"svc/kafka.go": segmentioFixture}
	producerFacts, _ := runExtractor(t, NewProducer(), files)
	assertRows(t, "segmentio producer", segmentioFixture, producerFacts, []wantRow{
		{"PRODUCES_TO_TOPIC", "topic:orders-v1", "derived", `"orders-v1"`, `"shapes":["Writer"]`},
		{"PRODUCES_TO_TOPIC", "topic:audit-log", "derived", `"audit-log"`, `"shapes":["WriterConfig"]`},
		{"PRODUCES_TO_TOPIC", "topic:per-message", "derived", `"per-message"`, `"shapes":["Message"]`},
		{"PRODUCES_TO_TOPIC", "topic:per-message-const", "derived", `messageTopic`, `"bindings":["same-file-const"]`},
		{"UNRESOLVED_KAFKA_PRODUCER", "unresolved:call-expr", "unresolved", `os.Getenv("TOPIC")`, ""},
	})
	consumerFacts, _ := runExtractor(t, NewConsumer(), files)
	assertRows(t, "segmentio consumer", segmentioFixture, consumerFacts, []wantRow{
		{"CONSUMES_FROM_TOPIC", "topic:orders-v1", "derived", `"orders-v1"`, `"group_ids":["billing"]`},
		{"CONSUMES_FROM_TOPIC", "topic:orders-v1", "derived", `"orders-v1"`, `"shapes":["ReaderConfig.GroupTopics","ReaderConfig.Topic"]`},
		{"CONSUMES_FROM_TOPIC", "topic:refunds", "derived", `"refunds"`, ""},
		{"UNRESOLVED_KAFKA_CONSUMER", "unresolved:non-literal-expr", "unresolved", `topics`, ""},
	})
	// The consumer CommitMessages Message must never be classified as
	// production: no producer row for topic:offset-only exists.
	for _, fact := range producerFacts {
		if fact.Assertion.Object == "topic:offset-only" {
			t.Fatal("CommitMessages Message wrongly classified as producer evidence")
		}
	}
}

func TestAmbiguousDualSaramaImport(t *testing.T) {
	files := map[string]string{"svc/kafka.go": ambiguousFixture}
	facts, coverage := runExtractor(t, NewConsumer(), files)
	if len(facts) != 1 || facts[0].Assertion.Object != "unresolved:ambiguous-library-import" {
		t.Fatalf("dual-import facts = %+v", facts)
	}
	if coverage.UnresolvedCount != 1 {
		t.Fatalf("UnresolvedCount = %d, want 1", coverage.UnresolvedCount)
	}
}

func TestTestFilesAndForeignFilesExcluded(t *testing.T) {
	files := map[string]string{
		// KD5: fixture literals in test files are authored noise.
		"svc/kafka_test.go": saramaFixture,
		// No round-one import: the file is ineligible.
		"svc/other.go": "package svc\n\nfunc noop() {}\n",
		// Not a Go file: not a candidate.
		"README.md": "producer of topics\n",
	}
	for _, extractor := range []sdk.Extractor{NewProducer(), NewConsumer()} {
		facts, coverage := runExtractor(t, extractor, files)
		if len(facts) != 0 || coverage.UnresolvedCount != 0 {
			t.Fatalf("%s: expected zero rows, got %+v", extractor.Domain(), facts)
		}
	}
}

func TestGapRows(t *testing.T) {
	files := map[string]string{
		"svc/broken.go": "package svc\nfunc {",
		"svc/huge.go":   "package svc\n// " + strings.Repeat("x", maxGoSourceBytes),
	}
	facts, coverage := runExtractor(t, NewProducer(), files)
	if len(facts) != 2 || coverage.UnresolvedCount != 2 {
		t.Fatalf("gap facts = %d, unresolved = %d, want 2/2", len(facts), coverage.UnresolvedCount)
	}
	objects := map[string]bool{}
	for _, fact := range facts {
		if fact.Assertion.Predicate != "KAFKA_EXTRACTION_GAP" || fact.Assertion.Tier != "unresolved" {
			t.Fatalf("gap row = %+v", fact.Assertion)
		}
		objects[fact.Assertion.Object] = true
	}
	if !objects["parse_error"] || !objects["source_too_large"] {
		t.Fatalf("gap reasons = %v", objects)
	}
}

func TestDeterministicDoubleRun(t *testing.T) {
	files := map[string]string{
		"svc/sarama.go":    saramaFixture,
		"svc/segmentio.go": segmentioFixture,
		"svc/ambiguous.go": ambiguousFixture,
	}
	for _, extractor := range []sdk.Extractor{NewProducer(), NewConsumer()} {
		first, firstCoverage := runExtractor(t, extractor, files)
		second, secondCoverage := runExtractor(t, extractor, files)
		if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstCoverage, secondCoverage) {
			t.Fatalf("%s: non-deterministic extraction", extractor.Domain())
		}
	}
}

// T23.R B1 regression: same-file same-plane same-topic sites must share one
// identical assertion attribute tuple (the store rejects duplicate semantic
// IDs with conflicting tier/code_role/detail), and mixed-tier merges take
// the strongest binding while the shapes list keeps both members visible.
func TestSameTopicSitesShareOneAttributeTuple(t *testing.T) {
	fixture := `package svc

import (
	"context"

	"github.com/IBM/sarama"
	kafka "github.com/segmentio/kafka-go"
)

func consume(ctx context.Context, c sarama.Consumer) {
	_, _ = c.ConsumePartition("clicks", 0, sarama.OffsetNewest)
	_, _ = c.ConsumePartition("clicks", 1, sarama.OffsetNewest)
	_ = kafka.NewReader(kafka.ReaderConfig{GroupID: "billing", Topic: "clicks"})
}
`
	files := map[string]string{"svc/kafka.go": fixture}
	facts, coverage := runExtractor(t, NewConsumer(), files)
	if len(facts) != 3 || coverage.UnresolvedCount != 0 {
		t.Fatalf("facts = %d, unresolved = %d: %+v", len(facts), coverage.UnresolvedCount, facts)
	}
	first := facts[0].Assertion
	for i, fact := range facts {
		a := fact.Assertion
		if a.Predicate != first.Predicate || a.Subject != first.Subject || a.Object != "topic:clicks" ||
			a.Lineage != first.Lineage || a.Tier != first.Tier || a.CodeRole != first.CodeRole || a.Detail != first.Detail {
			t.Fatalf("site %d attribute tuple diverges: %+v vs %+v", i, a, first)
		}
	}
	// One derived site (ReaderConfig.Topic) lifts the merged claim to
	// derived; both libraries and all shapes stay visible in the detail.
	if first.Tier != "derived" {
		t.Fatalf("merged tier = %q, want derived", first.Tier)
	}
	for _, want := range []string{
		`"libraries":["sarama","segmentio"]`,
		`"shapes":["ConsumePartition","ReaderConfig.Topic"]`,
		`"bindings":["literal"]`,
		`"group_ids":["billing"]`,
	} {
		if !strings.Contains(first.Detail, want) {
			t.Fatalf("merged detail %q missing %q", first.Detail, want)
		}
	}
	// Atoms stay per-site: three distinct spans.
	spans := map[[2]int]bool{}
	for _, fact := range facts {
		spans[[2]int{fact.Atom.StartByte, fact.Atom.EndByte}] = true
	}
	if len(spans) != 3 {
		t.Fatalf("expected 3 distinct atom spans, got %v", spans)
	}
}

// T23.R B2 regression: abstentions anchored on multi-line nodes must carry
// the real end line — the worker validates the fact line span against the
// trusted byte span and aborts the run on mismatch.
func TestMultiLineAbstentionLineSpans(t *testing.T) {
	fixture := `package svc

import (
	"context"
	"strings"

	"github.com/IBM/sarama"
)

func consume(ctx context.Context, g sarama.ConsumerGroup, h sarama.ConsumerGroupHandler, topics string) {
	g.Consume(ctx, strings.Split(
		topics,
		",",
	), h)
}
`
	files := map[string]string{"svc/kafka.go": fixture}
	facts, _ := runExtractor(t, NewConsumer(), files)
	if len(facts) != 1 || facts[0].Assertion.Object != "unresolved:call-expr" {
		t.Fatalf("facts = %+v", facts)
	}
	fact := facts[0]
	if fact.EndLine <= fact.StartLine {
		t.Fatalf("multi-line abstention EndLine = %d, StartLine = %d", fact.EndLine, fact.StartLine)
	}
	// The line span must agree with the byte span exactly as the worker
	// checks it: EndLine is the line containing EndByte-1.
	content := fixture
	wantStart := 1 + strings.Count(content[:fact.Atom.StartByte], "\n")
	wantEnd := 1 + strings.Count(content[:fact.Atom.EndByte-1], "\n")
	if fact.StartLine != wantStart || fact.EndLine != wantEnd {
		t.Fatalf("line span (%d,%d), trusted span wants (%d,%d)", fact.StartLine, fact.EndLine, wantStart, wantEnd)
	}
}

func TestLineageAndSubjects(t *testing.T) {
	files := map[string]string{"svc/kafka.go": saramaFixture}
	facts, _ := runExtractor(t, NewProducer(), files)
	for _, fact := range facts {
		if fact.Assertion.Subject != "svc/kafka.go" {
			t.Fatalf("subject = %q", fact.Assertion.Subject)
		}
		switch fact.Assertion.Predicate {
		case "PRODUCES_TO_TOPIC":
			if !strings.HasPrefix(fact.Assertion.Lineage, "provisional_repo_path_v1_") || len(fact.Assertion.Lineage) != len("provisional_repo_path_v1_")+64 {
				t.Fatalf("evidence lineage = %q", fact.Assertion.Lineage)
			}
		default:
			if fact.Assertion.Lineage != "" {
				t.Fatalf("unresolved lineage = %q", fact.Assertion.Lineage)
			}
		}
	}
}

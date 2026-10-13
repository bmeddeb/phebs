//go:build t551oracle

package main

import (
	"strings"
	"testing"
)

const fixture = `package p

import (
	"github.com/IBM/sarama"
	kafka "github.com/segmentio/kafka-go"
)

const topicConst = "orders"
const derived = topicConst + "-v2"

var topicVar = "payments"

func f(cfg struct{ Topic string }, w *kafka.Writer) {
	const local = "local.topic"
	_ = &sarama.ProducerMessage{Topic: "literal-topic"}
	_ = sarama.ProducerMessage{Topic: topicConst}
	_ = sarama.ProducerMessage{Topic: local}
	_ = sarama.ProducerMessage{Topic: derived}
	_ = sarama.ProducerMessage{Topic: topicVar}
	_ = sarama.ProducerMessage{Topic: cfg.Topic}
	_ = sarama.ProducerMessage{Topic: name()}
	_ = sarama.ProducerMessage{Topic: "bad topic!"}
	_ = sarama.ProducerMessage{Topic: "a" + "b"}
	_ = kafka.Writer{Brokers: nil}
	_ = kafka.WriterConfig{Topic: "cfg-topic"}
	w.WriteMessages(nil, kafka.Message{Topic: "direct"})
	m := kafka.Message{Topic: "standalone"}
	_ = m
}

func name() string { return "x" }
`

func TestScanAppliesTheDecisionTable(t *testing.T) {
	got := scan([]byte(fixture))
	want := []struct{ shape, state, object, binding, class string }{
		{"ProducerMessage", "resolved", "topic:literal-topic", "literal", ""},
		{"ProducerMessage", "resolved", "topic:orders", "same-file-const", ""},
		{"ProducerMessage", "resolved", "topic:local.topic", "same-file-const", ""},
		{"ProducerMessage", "unresolved", "", "", "unresolved-ident"},
		{"ProducerMessage", "unresolved", "", "", "unresolved-ident"},
		{"ProducerMessage", "unresolved", "", "", "selector-expr"},
		{"ProducerMessage", "unresolved", "", "", "call-expr"},
		{"ProducerMessage", "unresolved", "", "", "invalid-topic-literal"},
		{"ProducerMessage", "unresolved", "", "", "non-literal-expr"},
		{"Writer", "no_topic_field", "", "", ""},
		{"WriterConfig", "resolved", "topic:cfg-topic", "literal", ""},
		{"Message", "resolved", "topic:direct", "literal", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sites, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Shape != w.shape || g.State != w.state || g.Object != w.object || g.Binding != w.binding || g.Class != w.class {
			t.Errorf("site %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestScanSkipsFilesWithoutARoundOneImport(t *testing.T) {
	if got := scan([]byte("package p\n\nimport sarama \"example.com/other\"\n\nvar _ = sarama.ProducerMessage{Topic: \"x\"}\n")); len(got) != 0 {
		t.Fatalf("non round-one import produced sites: %+v", got)
	}
}

const consumerFixture = `package p

import (
	"github.com/IBM/sarama"
	kafka "github.com/segmentio/kafka-go"
)

const group = "events"

var dynamic = "x"

func f(c sarama.ConsumerGroup, pc sarama.Consumer, cfg struct{ Topic string }) {
	_ = kafka.ReaderConfig{Topic: "orders"}
	_ = kafka.ReaderConfig{GroupTopics: []string{group, dynamic}}
	_ = kafka.ReaderConfig{GroupTopics: topics()}
	c.Consume(nil, []string{"payments", cfg.Topic}, nil)
	pc.ConsumePartition(cfg.Topic, 0, 0)
	c.Consume(nil, topics(), nil)
}

func topics() []string { return nil }
`

func TestScanConsumerAppliesTheDecisionTable(t *testing.T) {
	consumerPlane = true
	t.Cleanup(func() { consumerPlane = false })
	got := scan([]byte(consumerFixture))
	want := []struct{ shape, state, object, class string }{
		{"ReaderConfig.Topic", "resolved", "topic:orders", ""},
		{"ReaderConfig.GroupTopics", "resolved", "topic:events", ""},
		{"ReaderConfig.GroupTopics", "unresolved", "", "unresolved-ident"},
		{"ReaderConfig.GroupTopics", "unresolved", "", "call-expr"},
		{"Consume", "resolved", "topic:payments", ""},
		{"Consume", "unresolved", "", "selector-expr"},
		{"ConsumePartition", "unresolved", "", "selector-expr"},
		{"Consume", "unresolved", "", "call-expr"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sites, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Shape != w.shape || g.State != w.state || g.Object != w.object || g.Class != w.class {
			t.Errorf("site %d = %+v, want %+v", i, g, w)
		}
	}

	dual := strings.Replace(consumerFixture, `"github.com/IBM/sarama"`, "\"github.com/IBM/sarama\"\n\tshopify \"github.com/Shopify/sarama\"", 1)
	dual = strings.Replace(dual, "func topics()", "var _ shopify.Config\n\nfunc topics()", 1)
	for _, s := range scan([]byte(dual)) {
		if (s.Shape == "Consume" || s.Shape == "ConsumePartition") && s.Class != "ambiguous-library-import" {
			t.Fatalf("dual-era receiver-untyped site = %+v, want ambiguous-library-import", s)
		}
	}
	segmentioOnly := "package p\n\nimport kafka \"github.com/segmentio/kafka-go\"\n\nvar _ kafka.Reader\n\nfunc g(c interface{ Consume(a, b, c any) }) { c.Consume(nil, []string{\"x\"}, nil) }\n"
	if got := scan([]byte(segmentioOnly)); len(got) != 0 {
		t.Fatalf("a segmentio-only Consume call was treated as a sarama site: %+v", got)
	}
}

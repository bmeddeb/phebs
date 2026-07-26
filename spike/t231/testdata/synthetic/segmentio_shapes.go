// Canonical segmentio/kafka-go shapes for offline recognition tests.
// Parse-only fixture data under testdata; never compiled.
package synthetic

import (
	"context"
	"os"

	kafka "github.com/segmentio/kafka-go"
)

const messageTopic = "per-message-const"

func writers(addr kafka.Addr) []any {
	return []any{
		// literal Writer topic → evidence
		&kafka.Writer{Addr: addr, Topic: "orders-v1"},
		// legacy WriterConfig literal → evidence
		kafka.NewWriter(kafka.WriterConfig{Topic: "audit-log"}),
		// environment-driven → abstain call-expr
		&kafka.Writer{Addr: addr, Topic: os.Getenv("TOPIC")},
	}
}

func perMessage(writer *kafka.Writer, ctx context.Context) error {
	// A topic-less Writer may carry the destination per message. Only
	// qualified Message literals passed directly to WriteMessages qualify:
	// following a local message variable would cross the no-dataflow bound.
	return writer.WriteMessages(
		ctx,
		kafka.Message{Topic: "per-message"},
		kafka.Message{Topic: messageTopic},
		kafka.Message{Topic: os.Getenv("MESSAGE_TOPIC")},
	)
}

func commitOffset(reader *kafka.Reader, ctx context.Context) error {
	// A Message passed to CommitMessages is consumer bookkeeping, not a
	// producer. The recognizer must not classify this topic as production.
	return reader.CommitMessages(ctx, kafka.Message{Topic: "offset-only"})
}

func readers(broker string) []*kafka.Reader {
	return []*kafka.Reader{
		// literal topic + literal group id (detail, never identity)
		kafka.NewReader(kafka.ReaderConfig{
			Brokers: []string{broker},
			GroupID: "billing",
			Topic:   "orders-v1",
		}),
		// GroupTopics literal slice → one evidence row per element
		kafka.NewReader(kafka.ReaderConfig{
			GroupID:     "billing",
			GroupTopics: []string{"orders-v1", "refunds"},
		}),
	}
}

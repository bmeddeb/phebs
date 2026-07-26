// Canonical segmentio/kafka-go shapes for offline recognition tests.
// Parse-only fixture data under testdata; never compiled.
package synthetic

import (
	"os"

	kafka "github.com/segmentio/kafka-go"
)

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

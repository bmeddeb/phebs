// Canonical sarama shapes for offline recognition tests. This file is
// parse-only fixture data under testdata: it is never compiled, and the
// declared shapes are the frozen positive and negative recognition cases.
package synthetic

import (
	"context"
	"os"

	"github.com/IBM/sarama"
)

const auditTopic = "audit-v2"

var mutableTopic = "orders-mutable"

func produce(producer sarama.SyncProducer, cfgTopic string) error {
	// literal → evidence
	if _, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: "orders-v1"}); err != nil {
		return err
	}
	// same-file const → evidence with const binding
	if _, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: auditTopic}); err != nil {
		return err
	}
	// same-file var → abstain (mutable state is not a declaration spelling)
	if _, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: mutableTopic}); err != nil {
		return err
	}
	// parameter → abstain unresolved-ident
	if _, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: cfgTopic}); err != nil {
		return err
	}
	// environment call → abstain call-expr
	_, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: os.Getenv("TOPIC")})
	return err
}

func consume(ctx context.Context, group sarama.ConsumerGroup, consumer sarama.Consumer, handler sarama.ConsumerGroupHandler) error {
	// literal slice → two evidence rows
	if err := group.Consume(ctx, []string{"orders-v1", "payments"}, handler); err != nil {
		return err
	}
	// literal partition consumer → evidence
	if _, err := consumer.ConsumePartition("clicks", 0, sarama.OffsetNewest); err != nil {
		return err
	}
	// illegal topic literal → abstain invalid-topic-literal
	_, err := consumer.ConsumePartition("bad topic!", 0, sarama.OffsetNewest)
	return err
}

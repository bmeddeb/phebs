package orders

import (
	"context"

	"github.com/IBM/sarama"
)

func PublishCreated(producer sarama.SyncProducer) {
	_, _, _ = producer.SendMessage(&sarama.ProducerMessage{Topic: "orders.created.v1"})
}

func ConsumeCreated(
	ctx context.Context,
	group sarama.ConsumerGroup,
	handler sarama.ConsumerGroupHandler,
) error {
	return group.Consume(ctx, []string{"orders.created.v1"}, handler)
}

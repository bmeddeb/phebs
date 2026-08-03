package orders

import "github.com/IBM/sarama"

// This authored test-only topic must contribute a go_test exclusion, never a
// production Kafka fact.
func publishTestOnly(producer sarama.SyncProducer) {
	_, _, _ = producer.SendMessage(&sarama.ProducerMessage{
		Topic: "orders.test-only.v1",
	})
}

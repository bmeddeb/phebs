package extract

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/store"
)

// Regression for T23.2: the real worker publishes each Kafka plane as its
// own run in which every Go file is read, _test.go fixtures are excluded
// from recognition (KD5), literal and same-file-const topics bind, and the
// manifest reconciles evidence with distinct unresolved abstentions.
// Mirrors the thriftgo worker regression.

const kafkaProducerFile = `package svc

import (
	"os"

	"github.com/IBM/sarama"
)

const auditTopic = "audit-v2"

func produce(p sarama.SyncProducer) {
	p.SendMessage(&sarama.ProducerMessage{Topic: "orders-v1"})
	p.SendMessage(&sarama.ProducerMessage{Topic: auditTopic})
	// T23.R B1: a second site for the same topic must merge into one
	// assertion identity (the memoryEvidence harness now rejects
	// conflicting attribute tuples exactly like the store).
	p.SendMessage(&sarama.ProducerMessage{Topic: "orders-v1"})
	// T23.R B2: a multi-line abstention node must survive the worker's
	// trusted line-span validation.
	p.SendMessage(&sarama.ProducerMessage{Topic: os.Getenv(
		"TOPIC",
	)})
}
`

const kafkaConsumerFile = `package svc

import (
	"context"
	"strings"

	"github.com/IBM/sarama"
)

func consume(ctx context.Context, g sarama.ConsumerGroup, c sarama.Consumer, h sarama.ConsumerGroupHandler, topics string) {
	g.Consume(ctx, []string{"payments"}, h)
	g.Consume(ctx, strings.Split(topics, ","), h)
	c.ConsumePartition("clicks", 0, sarama.OffsetNewest)
}
`

func kafkaWorkerFiles() map[string]string {
	return map[string]string{
		"svc/producer.go": kafkaProducerFile,
		"svc/consumer.go": kafkaConsumerFile,
		// KD5: a fixture literal in a test file must never become evidence.
		"svc/fixture_test.go": `package svc

import "github.com/IBM/sarama"

func fixture(p sarama.SyncProducer) {
	p.SendMessage(&sarama.ProducerMessage{Topic: "morekuzambu"})
}
`,
	}
}

func TestKafkaProducerPublishesThroughWorker(t *testing.T) {
	repo := &store.Repo{Name: "example.com/app", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus:  grpcWorkerCorpusFactory{files: kafkaWorkerFiles()},
		Extractors: []Extractor{kafkago.NewProducer()},
	}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !evidence.published || evidence.aborted {
		t.Fatalf("run state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
	coverage := evidence.publishedWith
	// Two topics (orders-v1 twice, merged; audit-v2 once) plus one distinct
	// multi-line call-expr abstention: three assertions over four atoms; the
	// test fixture's topic never appears; all three Go files are candidates
	// and are read.
	if coverage.UnresolvedCount != 1 || coverage.AssertionCount != 3 || coverage.AtomCount != 4 ||
		coverage.CandidateFileCount != 3 || coverage.ReadFileCount != 3 {
		t.Fatalf("producer coverage = %+v", coverage)
	}
}

func TestKafkaConsumerPublishesThroughWorker(t *testing.T) {
	repo := &store.Repo{Name: "example.com/app", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus:  grpcWorkerCorpusFactory{files: kafkaWorkerFiles()},
		Extractors: []Extractor{kafkago.NewConsumer()},
	}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !evidence.published || evidence.aborted {
		t.Fatalf("run state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
	coverage := evidence.publishedWith
	// A heuristic Consume-slice topic, a heuristic ConsumePartition topic,
	// and one distinct call-expr abstention.
	if coverage.UnresolvedCount != 1 || coverage.AssertionCount != 3 || coverage.AtomCount != 3 ||
		coverage.CandidateFileCount != 3 || coverage.ReadFileCount != 3 {
		t.Fatalf("consumer coverage = %+v", coverage)
	}
}

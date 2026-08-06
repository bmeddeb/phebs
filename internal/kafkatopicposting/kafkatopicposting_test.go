package kafkatopicposting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

type observedFixture struct {
	record      observationpublication.Record
	observation sourceobservation.Observation
}

type fakeObservationSource struct {
	manifest observationpublication.Manifest
	values   []observedFixture
}

func (source fakeObservationSource) Manifest() observationpublication.Manifest {
	return source.manifest
}

func (source fakeObservationSource) WalkObserved(
	ctx context.Context,
	visit func(observationpublication.Record, sourceobservation.Observation) error,
) error {
	for _, value := range source.values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(value.record, value.observation); err != nil {
			return err
		}
	}
	return nil
}

const kafkaFixture = `package app
import (
  "context"
  "os"
  "github.com/IBM/sarama"
  kafka "github.com/segmentio/kafka-go"
)
const auditTopic = "audit-v2"
var crossFileOrMutable = "mutable"
func use(ctx context.Context, producer sarama.SyncProducer, consumer sarama.Consumer) {
  producer.SendMessage(&sarama.ProducerMessage{Topic: "orders-v1"})
  producer.SendMessage(&sarama.ProducerMessage{Topic: auditTopic})
  producer.SendMessage(&sarama.ProducerMessage{Topic: crossFileOrMutable})
  producer.SendMessage(&sarama.ProducerMessage{Topic: os.Getenv("TOPIC")})
  consumer.ConsumePartition("clicks", 0, sarama.OffsetNewest)
  consumer.ConsumePartition("bad topic!", 0, sarama.OffsetNewest)
  _ = kafka.NewReader(kafka.ReaderConfig{GroupID: "billing", Topic: "orders-v1"})
}
`

func TestBuildSeparatesPlanesSpellingClassificationsAndOccurrences(t *testing.T) {
	root := t.TempDir()
	fixture := observationFixture(t, "cmd/kafka.go", kafkaFixture, []sourcepartition.Placement{
		{Path: "cmd/kafka.go", Mode: "100644", Revisions: []int{0}},
		{Path: "copy/kafka.go", Mode: "100644", Revisions: []int{0, 1}},
		{Path: "tests/kafka_test.go", Mode: "100644", Revisions: []int{1}},
		{Path: "vendor/acme/kafka.go", Mode: "100644", Revisions: []int{0}},
	})
	prepared, err := buildSource(t.Context(), root, fakeSource(t, []observedFixture{fixture}))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rootValue := publication.Root()
	// The fixture has seven sites and four exact placements.
	if rootValue.PostingCount != 28 || rootValue.ProducerCount != 16 ||
		rootValue.ConsumerCount != 12 || rootValue.LiteralCount != 16 ||
		rootValue.UnresolvedCount != 12 {
		t.Fatalf("root totals = %+v", rootValue)
	}
	producers, err := publication.ReadTopic(t.Context(), "producer", "orders-v1")
	if err != nil || len(producers) != 4 {
		t.Fatalf("producer postings = %+v, %v", producers, err)
	}
	reopened, err := OpenGeneration(
		t.Context(), root, rootValue.Authority.Repository,
		rootValue.GenerationDigest, rootValue.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	byDigest, err := reopened.ReadDigest(
		t.Context(), "producer", "orders-v1", producers[0].Digest,
	)
	if err != nil || byDigest.Digest != producers[0].Digest {
		t.Fatalf("exact generation digest read = %+v, %v", byDigest, err)
	}
	consumers, err := publication.ReadTopic(t.Context(), "consumer", "orders-v1")
	if err != nil || len(consumers) != 4 {
		t.Fatalf("consumer postings = %+v, %v", consumers, err)
	}
	if values, err := publication.ReadTopic(t.Context(), "consumer", "audit-v2"); err != nil || len(values) != 0 {
		t.Fatalf("plane separation = %+v, %v", values, err)
	}
	roles := map[string]int{}
	digests := map[string]struct{}{}
	for _, posting := range producers {
		roles[posting.SourceRole]++
		digests[posting.Digest] = struct{}{}
		if posting.TopicSpelling != "orders-v1" || posting.Binding != "literal" {
			t.Fatalf("topic source spelling = %+v", posting)
		}
	}
	if roles["production"] != 2 || roles["test"] != 1 || roles["vendor"] != 1 || len(digests) != 4 {
		t.Fatalf("roles=%v identities=%d", roles, len(digests))
	}
	unresolved, err := publication.ReadUnresolved(t.Context(), "producer")
	if err != nil || len(unresolved) != 8 {
		t.Fatalf("producer unresolved = %+v, %v", unresolved, err)
	}
	sourceReasons := map[string]int{}
	for _, posting := range unresolved {
		if posting.TopicSpelling != "" || posting.Binding != "" || posting.SourceReason == "" ||
			(posting.Reason != "dynamic_topic" && posting.Reason != "unsupported_topic") {
			t.Fatalf("unresolved promoted authority = %+v", posting)
		}
		sourceReasons[posting.SourceReason]++
	}
	if sourceReasons["unresolved-ident"] != 4 || sourceReasons["call-expr"] != 4 {
		t.Fatalf("dynamic source reasons = %v", sourceReasons)
	}
	consumerUnresolved, err := publication.ReadUnresolved(t.Context(), "consumer")
	if err != nil || len(consumerUnresolved) != 4 {
		t.Fatalf("consumer unresolved = %+v, %v", consumerUnresolved, err)
	}
	for _, posting := range consumerUnresolved {
		if posting.Reason != "unsupported_topic" || posting.SourceReason != "invalid-topic-literal" {
			t.Fatalf("unsupported source reason = %+v", posting)
		}
	}

	second, err := buildSource(t.Context(), root, fakeSource(t, []observedFixture{fixture}))
	if err != nil {
		t.Fatal(err)
	}
	secondPublication, err := second.Publish(t.Context())
	if err != nil || secondPublication.Root().Digest != rootValue.Digest {
		t.Fatalf("deterministic rebuild = %v, %v", secondPublication, err)
	}

	// The selected literal and unresolved fixture buckets are deliberately
	// different so corrupt unresolved bytes cannot affect the keyed literal read.
	if partitionBucket("orders-v1") == partitionBucket("__unresolved__") {
		t.Fatal("fixture bucket collision")
	}
	receipt := memberReceipt(t, publication, "producer", partitionBucket("__unresolved__"))
	if err := os.WriteFile(filepath.Join(publication.directory, receipt.Name), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if values, err := publication.ReadTopic(t.Context(), "producer", "orders-v1"); err != nil || len(values) != 4 {
		t.Fatalf("sparse read touched corrupt sibling: %+v, %v", values, err)
	}
	if _, err := publication.ReadUnresolved(t.Context(), "producer"); err == nil {
		t.Fatal("corrupt selected member was accepted")
	}
}

func TestSupportedCasesMatchDirectKafkaExtractors(t *testing.T) {
	root := t.TempDir()
	fixture := observationFixture(t, "cmd/kafka.go", kafkaFixture, []sourcepartition.Placement{{
		Path: "cmd/kafka.go", Mode: "100644", Revisions: []int{0},
	}})
	prepared, err := buildSource(t.Context(), root, fakeSource(t, []observedFixture{fixture}))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		plane     string
		extractor sdk.Extractor
	}{
		{plane: "producer", extractor: kafkago.NewProducer()},
		{plane: "consumer", extractor: kafkago.NewConsumer()},
	} {
		t.Run(test.plane, func(t *testing.T) {
			facts := []sdk.Fact{}
			_, err := test.extractor.Extract(t.Context(), memoryCorpus{"cmd/kafka.go": kafkaFixture}, func(value sdk.Fact) error {
				facts = append(facts, value)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			expected := map[string]struct{}{}
			for _, fact := range facts {
				if !strings.HasPrefix(fact.Assertion.Object, "topic:") {
					continue
				}
				topic := strings.TrimPrefix(fact.Assertion.Object, "topic:")
				expected[fmt.Sprintf("%s\x00%d\x00%d", topic, fact.Atom.StartByte, fact.Atom.EndByte)] = struct{}{}
			}
			actual := map[string]struct{}{}
			seenTopics := map[string]struct{}{}
			for _, site := range fixture.observation.TopicSites {
				if site.Plane == test.plane && site.State == "resolved" {
					seenTopics[site.Topic] = struct{}{}
				}
			}
			for topic := range seenTopics {
				postings, err := publication.ReadTopic(t.Context(), test.plane, topic)
				if err != nil {
					t.Fatal(err)
				}
				for _, posting := range postings {
					actual[fmt.Sprintf("%s\x00%d\x00%d", posting.TopicSpelling, posting.Span.StartByte, posting.Span.EndByte)] = struct{}{}
				}
			}
			if !equalSet(actual, expected) {
				t.Fatalf("supported parity: postings=%v direct=%v", actual, expected)
			}
		})
	}
}

func equalSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, present := right[value]; !present {
			return false
		}
	}
	return true
}

func TestNoTopicSitesPublishesClosedEmptyGeneration(t *testing.T) {
	root := t.TempDir()
	fixture := observationFixture(t, "cmd/plain.go", "package app\nfunc plain() {}\n", []sourcepartition.Placement{{
		Path: "cmd/plain.go", Mode: "100644", Revisions: []int{0},
	}})
	prepared, err := buildSource(t.Context(), root, fakeSource(t, []observedFixture{fixture}))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rootValue := publication.Root()
	if rootValue.PostingCount != 0 || len(rootValue.Members) != 0 {
		t.Fatalf("empty root = %+v", rootValue)
	}
	if values, err := publication.ReadTopic(t.Context(), "producer", "orders-v1"); err != nil || len(values) != 0 {
		t.Fatalf("empty keyed read = %+v, %v", values, err)
	}
	if values, err := publication.ReadUnresolved(t.Context(), "consumer"); err != nil || len(values) != 0 {
		t.Fatalf("empty unresolved read = %+v, %v", values, err)
	}
}

func TestMemberLimitCheckedBeforeGrowth(t *testing.T) {
	fixture := observationFixture(t, "cmd/kafka.go", kafkaFixture, []sourcepartition.Placement{{
		Path: "cmd/kafka.go", Mode: "100644", Revisions: []int{0},
	}})
	posting := postingFromSite(
		fixture.record, "cmd/kafka.go", "100644", []int{0}, fixture.observation.TopicSites[0],
	)
	if err := setPostingDigest(&posting); err != nil {
		t.Fatal(err)
	}
	key := memberKey(posting.Plane, partitionBucket(partitionIdentity(posting)))
	byMember := map[string][]Posting{key: make([]Posting, MaxPostingsPerMember)}
	seen := map[string]struct{}{}
	count := 0
	var identityBytes int64
	var residentCharge int64
	if err := addPosting(
		byMember, seen, &count, &identityBytes, &residentCharge,
		MaxPostingIdentityBytes, posting,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("member limit error = %v", err)
	}
	if len(byMember[key]) != MaxPostingsPerMember || len(seen) != 0 || count != 0 || identityBytes != 0 {
		t.Fatal("member limit mutated posting state")
	}
}

type memoryCorpus map[string]string

func (m memoryCorpus) RepoName() string { return "github.com/acme/repo" }
func (m memoryCorpus) Commit() string   { return strings.Repeat("f", 40) }
func (m memoryCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	paths := make([]string, 0, len(m))
	for path := range m {
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
func (m memoryCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	content, present := m[path]
	if !present {
		return sdk.Blob{}, fmt.Errorf("missing fixture")
	}
	return sdk.Blob{Content: content, Digest: "sha256:fixture"}, nil
}

func observationFixture(
	t *testing.T,
	path, content string,
	placements []sourcepartition.Placement,
) observedFixture {
	t.Helper()
	observation, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{Path: path, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return observedFixture{
		observation: observation,
		record: observationpublication.Record{
			Schema: observationpublication.RecordSchema, ObjectID: hex.EncodeToString(sum[:])[:40],
			DeclaredBytes: int64(len(content)), Placements: placements, State: "observed",
			ContentDigest:     observation.ContentDigest,
			ObservationDigest: "sha256:" + strings.Repeat("3", 64),
			ObservationName:   "objects/fixture.json", ObservationOrigin: "parsed",
		},
	}
}

func fakeSource(t *testing.T, values []observedFixture) fakeObservationSource {
	t.Helper()
	manifest := observationpublication.Manifest{
		Schema: observationpublication.ManifestSchema, Repository: "github.com/acme/repo",
		Language:                  observationpublication.LanguagePack,
		SourceGenerationDigest:    "sha256:" + strings.Repeat("4", 64),
		PartitionGenerationDigest: "sha256:" + strings.Repeat("5", 64),
		PartitionManifestDigest:   "sha256:" + strings.Repeat("6", 64),
		PartitionPolicyDigest:     "sha256:" + strings.Repeat("7", 64),
		ObservationPolicyDigest:   sourceobservation.PolicyDigest(),
		Members: []observationpublication.Member{{
			Ordinal: 0, Count: 1, Name: "member-00000.jsonl",
			SourceMemberDigest: "sha256:" + strings.Repeat("8", 64),
			RecordCount:        len(values), ObservedCount: len(values), ContentBytes: 1,
			Digest: "sha256:" + strings.Repeat("9", 64),
		}},
		RecordCount: len(values), ObservedCount: len(values),
		EncodedMemberBytes: 1, ObservationBytes: int64(len(values)),
	}
	manifest.GenerationDigest = observationGeneration(manifest)
	digest, err := observationpublication.ManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = digest
	if err := observationpublication.ValidateManifest(manifest); err != nil {
		t.Fatal(err)
	}
	return fakeObservationSource{manifest: manifest, values: values}
}

func observationGeneration(value observationpublication.Manifest) string {
	joined := strings.Join([]string{
		value.Repository, value.SourceGenerationDigest, value.PartitionGenerationDigest,
		value.PartitionManifestDigest, value.PartitionPolicyDigest,
		value.ObservationPolicyDigest, value.Language,
	}, "\x00")
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-observation-generation-v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(joined))
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func memberReceipt(t *testing.T, publication *Publication, plane string, bucket int) MemberReceipt {
	t.Helper()
	for _, receipt := range publication.Root().Members {
		if receipt.Plane == plane && receipt.Bucket == bucket {
			return receipt
		}
	}
	t.Fatalf("missing member %s/%d", plane, bucket)
	return MemberReceipt{}
}

func TestRootMembersRemainCanonical(t *testing.T) {
	values := []MemberReceipt{{Plane: "producer", Bucket: 2}, {Plane: "consumer", Bucket: 1}}
	slices.SortFunc(values, func(left, right MemberReceipt) int {
		return strings.Compare(memberKey(left.Plane, left.Bucket), memberKey(right.Plane, right.Bucket))
	})
	if values[0].Plane != "consumer" {
		t.Fatalf("member ordering = %+v", values)
	}
}

package kafkatopicposting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/spike/t401"
)

type observedFixture struct {
	record      observationpublication.Record
	observation sourceobservation.Observation
}

type fakeObservationSource struct {
	manifest observationpublication.Manifest
	values   []observedFixture
}

type frozenSemanticKafkaSource struct {
	manifest observationpublication.Manifest
	literal  sourceobservation.Observation
	dynamic  sourceobservation.Observation
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

func (source frozenSemanticKafkaSource) Manifest() observationpublication.Manifest {
	return source.manifest
}

func (source frozenSemanticKafkaSource) WalkObserved(
	ctx context.Context,
	visit func(observationpublication.Record, sourceobservation.Observation) error,
) error {
	const familyInputs = 65_536
	families := []struct {
		start       int
		observation sourceobservation.Observation
	}{
		{start: familyInputs, observation: source.literal},
		{start: 3 * familyInputs, observation: source.dynamic},
	}
	for _, family := range families {
		for offset := 0; offset < familyInputs; offset++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			ordinal := family.start + offset
			path := fmt.Sprintf("semantic/go/b%03d/f%06d.go", ordinal/1_024, ordinal)
			identity := sha256.Sum256([]byte(fmt.Sprintf("t40r1-kafka-%06d", ordinal)))
			record := observationpublication.Record{
				Schema:   observationpublication.RecordSchema,
				ObjectID: hex.EncodeToString(identity[:])[:40], DeclaredBytes: 4_608,
				Placements: []sourcepartition.Placement{{
					Path: path, Mode: "100644", Revisions: []int{0},
				}},
				State: "observed", ContentDigest: family.observation.ContentDigest,
				ObservationDigest: "sha256:" + strings.Repeat("3", 64),
				ObservationName:   "objects/generated.json", ObservationOrigin: "parsed",
			}
			if err := visit(record, family.observation); err != nil {
				return err
			}
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
	if err := prepared.Discard(); err != nil {
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

func TestFrozenSemanticPostingChargeFitsRelationshipEnvelope(t *testing.T) {
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var semantic t401.Profile
	for _, profile := range profiles {
		if profile.Kind == "semantic" {
			semantic = profile
			break
		}
	}
	if semantic.Name != t401.SemanticProfileName {
		t.Fatalf("semantic profile = %+v", semantic)
	}
	tests := []struct {
		name       string
		family     string
		ordinals   []uint64
		wantJSON   int
		wantCharge int64
	}{
		{
			name: "literal", family: "go_kafka_literal",
			ordinals: []uint64{65_536, 131_071}, wantJSON: 606, wantCharge: 1_118,
		},
		{
			name: "dynamic", family: "go_dynamic_topic",
			ordinals: []uint64{196_608, 262_143}, wantJSON: 618, wantCharge: 1_130,
		},
	}
	var perFamily []int64
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, ordinal := range test.ordinals {
				path, content, fixtureErr := t401.FrozenSemanticGoFixture(
					semantic, test.family, ordinal,
				)
				if fixtureErr != nil {
					t.Fatal(fixtureErr)
				}
				fixture := observationFixture(t, path, string(content), []sourcepartition.Placement{{
					Path: path, Mode: "100644", Revisions: []int{0},
				}})
				if len(fixture.observation.TopicSites) != 1 {
					t.Fatalf("topic sites = %d", len(fixture.observation.TopicSites))
				}
				posting := postingFromSite(
					fixture.record, path, "100644", []int{0}, fixture.observation.TopicSites[0],
				)
				if err := setPostingDigest(&posting); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(posting)
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) != test.wantJSON || int64(len(raw)+512) != test.wantCharge {
					t.Fatalf("ordinal %d charge = %d + 512, want %d + 512",
						ordinal, len(raw), test.wantJSON)
				}
			}
		})
		perFamily = append(perFamily, test.wantCharge)
	}
	const familyInputs = int64(65_536)
	total := familyInputs * (perFamily[0] + perFamily[1])
	if total != 147_324_928 || total <= 128<<20 || total > 160<<20 ||
		MaxPostingsPerMember != int(familyInputs) ||
		historicalMaxPostingsPerMember != 50_000 ||
		historicalMaxPostingsPerMember >= MaxPostingsPerMember {
		t.Fatalf("semantic Kafka resident charge = %d", total)
	}
}

func TestHistoricalPostingPolicyRemainsAdmitted(t *testing.T) {
	prepared, err := buildSource(
		t.Context(), t.TempDir(), fakeSource(t, []observedFixture{
			observationFixture(t, "cmd/kafka.go", kafkaFixture, []sourcepartition.Placement{{
				Path: "cmd/kafka.go", Mode: "100644", Revisions: []int{0},
			}}),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	root := prepared.rootValue
	root.Policy = historicalPolicy()
	policyDigest, err := digestValue(root.Policy)
	if err != nil {
		t.Fatal(err)
	}
	root.Authority.PolicyDigest = policyDigest
	if err := setRootDigests(&root); err != nil {
		t.Fatalf("historical root policy rejected: %v", err)
	}
	tampered := root
	tampered.Policy.MaxPostingsPerMember--
	if ValidateRoot(tampered) == nil {
		t.Fatal("unfrozen historical posting policy was admitted")
	}
}

func TestFrozenSemanticPostingBuildDiagnostic(t *testing.T) {
	if os.Getenv("T40R1_RELATIONSHIP_KAFKA_DIAGNOSTIC") != "1" {
		t.Skip("set T40R1_RELATIONSHIP_KAFKA_DIAGNOSTIC=1 to run the exact Kafka posting build")
	}
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var semantic t401.Profile
	for _, profile := range profiles {
		if profile.Name == t401.SemanticProfileName {
			semantic = profile
			break
		}
	}
	fixtures := make([]sourceobservation.Observation, 0, 2)
	for _, family := range []struct {
		name    string
		ordinal uint64
	}{
		{name: "go_kafka_literal", ordinal: 65_536},
		{name: "go_dynamic_topic", ordinal: 196_608},
	} {
		path, content, fixtureErr := t401.FrozenSemanticGoFixture(
			semantic, family.name, family.ordinal,
		)
		if fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		observation, parseErr := sourceobservation.Parse(
			t.Context(), sourceobservation.Input{Path: path, Content: string(content)},
		)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		fixtures = append(fixtures, observation)
	}
	const observed = 2 * 65_536
	source := frozenSemanticKafkaSource{
		manifest: fakeManifest(t, observed), literal: fixtures[0], dynamic: fixtures[1],
	}
	prepared, err := buildSourceBounded(t.Context(), t.TempDir(), source, 160<<20)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root := publication.Root()
	if root.PostingCount != observed || root.ProducerCount != observed ||
		root.LiteralCount != 65_536 || root.UnresolvedCount != 65_536 ||
		len(root.Members) != 2 {
		t.Fatalf("semantic Kafka root = %+v", root)
	}
	for _, member := range root.Members {
		if member.PostingCount != 65_536 {
			t.Fatalf("semantic Kafka member = %+v", member)
		}
	}
	t.Logf(
		"semantic Kafka posting build: postings=%d members=%d encoded_bytes=%d generation=%s root=%s",
		root.PostingCount, len(root.Members), root.EncodedMemberBytes,
		root.GenerationDigest, root.Digest,
	)
}

func TestResidentLimitErrorCarriesOnlyExactByteScalars(t *testing.T) {
	fixture := observationFixture(t, "cmd/kafka.go", kafkaFixture, []sourcepartition.Placement{{
		Path: "cmd/kafka.go", Mode: "100644", Revisions: []int{0},
	}})
	posting := postingFromSite(
		fixture.record, "cmd/kafka.go", "100644", []int{0}, fixture.observation.TopicSites[0],
	)
	if err := setPostingDigest(&posting); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(posting)
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(len(raw) + 511)
	var count int
	var identityBytes, residentCharge int64
	err = addPosting(
		make(map[string][]Posting), make(map[string]struct{}), &count, &identityBytes,
		&residentCharge, limit, posting,
	)
	var resident *pipelinerefusal.Measurement
	if !errors.As(err, &resident) || !errors.Is(err, ErrLimit) ||
		resident.Dimension != pipelinerefusal.DimensionResidentBytes ||
		resident.Observed != limit+1 || resident.Limit != limit {
		t.Fatalf("resident refusal = %+v, %v", resident, err)
	}
}

func TestDiscardRemovesStageAfterPreinstallPublishFailure(t *testing.T) {
	root := t.TempDir()
	fixture := observationFixture(
		t, "cmd/plain.go", "package app\n", []sourcepartition.Placement{{
			Path: "cmd/plain.go", Mode: "100644", Revisions: []int{0},
		}},
	)
	prepared, err := buildSource(
		t.Context(), root, fakeSource(t, []observedFixture{fixture}),
	)
	if err != nil {
		t.Fatal(err)
	}
	stage := prepared.directory
	target := generationDirectory(root, prepared.repository, prepared.rootValue.GenerationDigest)
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Publish(t.Context()); err == nil {
		t.Fatal("publish accepted conflicting incomplete generation")
	}
	if _, err := os.Lstat(stage); err != nil {
		t.Fatalf("pre-install publish failure consumed stage: %v", err)
	}
	if err := prepared.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discarded stage remains: %v", err)
	}
	if err := prepared.Discard(); err != nil {
		t.Fatalf("repeated discard = %v", err)
	}
}

func TestBuildCleanupFailureIsNotClassifiedAsLimit(t *testing.T) {
	root := t.TempDir()
	repository := "github.com/acme/repo"
	ctx := &stageCleanupFailureContext{
		Context: t.Context(),
		remove:  publicationRepository(root, repository),
		cause:   ErrLimit,
	}
	_, err := writeStage(
		ctx, root, Authority{Repository: repository}, RootSchema, nil,
		map[string][]Posting{memberKey("producer", 0): {}},
	)
	if ctx.removeErr != nil {
		t.Fatal(ctx.removeErr)
	}
	if err == nil || errors.Is(err, ErrLimit) ||
		!strings.Contains(err.Error(), "clean failed Kafka topic posting stage") ||
		!strings.Contains(err.Error(), ErrLimit.Error()) {
		t.Fatalf("cleanup failure classification = %v", err)
	}
}

type stageCleanupFailureContext struct {
	context.Context
	remove    string
	cause     error
	removeErr error
}

func (ctx *stageCleanupFailureContext) Err() error {
	if ctx.remove != "" {
		ctx.removeErr = os.RemoveAll(ctx.remove)
		ctx.remove = ""
	}
	return ctx.cause
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
	return fakeObservationSource{manifest: fakeManifest(t, len(values)), values: values}
}

func fakeManifest(t *testing.T, records int) observationpublication.Manifest {
	t.Helper()
	memberCount := (records + sourcepartition.MaxBlobsPerPartition - 1) /
		sourcepartition.MaxBlobsPerPartition
	members := make([]observationpublication.Member, memberCount)
	remaining := records
	for ordinal := range members {
		count := min(remaining, sourcepartition.MaxBlobsPerPartition)
		members[ordinal] = observationpublication.Member{
			Ordinal: ordinal, Count: memberCount,
			Name:               fmt.Sprintf("member-%05d.jsonl", ordinal),
			SourceMemberDigest: "sha256:" + strings.Repeat("8", 64),
			RecordCount:        count, ObservedCount: count, ContentBytes: 1,
			Digest: "sha256:" + strings.Repeat("9", 64),
		}
		remaining -= count
	}
	manifest := observationpublication.Manifest{
		Schema: observationpublication.ManifestSchema, Repository: "github.com/acme/repo",
		Language:                  observationpublication.LanguagePack,
		SourceGenerationDigest:    "sha256:" + strings.Repeat("4", 64),
		PartitionGenerationDigest: "sha256:" + strings.Repeat("5", 64),
		PartitionManifestDigest:   "sha256:" + strings.Repeat("6", 64),
		PartitionPolicyDigest:     "sha256:" + strings.Repeat("7", 64),
		ObservationPolicyDigest:   sourceobservation.PolicyDigest(),
		Members:                   members,
		RecordCount:               records, ObservedCount: records,
		EncodedMemberBytes: int64(memberCount), ObservationBytes: int64(records),
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
	return manifest
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

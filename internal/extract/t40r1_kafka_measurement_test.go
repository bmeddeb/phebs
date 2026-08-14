package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

const t40r1KafkaMeasurementSchema = "t4013-take19-kafka-result-measurement-v1"

type t40r1KafkaCorpus struct {
	paths   []string
	content map[string]string
}

func (c t40r1KafkaCorpus) RepoName() string { return t401.RepositoryName }
func (c t40r1KafkaCorpus) Commit() string   { return "0000000000000000000000000000000000000000" }
func (c t40r1KafkaCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	for _, path := range c.paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}
func (c t40r1KafkaCorpus) Read(ctx context.Context, path string) (sdk.Blob, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Blob{}, err
	}
	content, ok := c.content[path]
	if !ok {
		return sdk.Blob{}, errors.New("T40.R1 measurement fixture path is absent")
	}
	sum := sha256.Sum256([]byte(content))
	return sdk.Blob{Content: content, Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

type t40r1KafkaTotals struct {
	Facts          int64 `json:"facts"`
	Rows           int64 `json:"rows"`
	References     int64 `json:"references"`
	CanonicalBytes int64 `json:"canonical_bytes"`
	EncodedBytes   int64 `json:"encoded_bytes"`
}

type t40r1KafkaMeasurement struct {
	Schema                   string                       `json:"schema"`
	Profile                  string                       `json:"profile"`
	ProfileDigest            string                       `json:"profile_digest"`
	Domain                   string                       `json:"domain"`
	ExtractorVersion         string                       `json:"extractor_version"`
	CandidatePartitions      int                          `json:"candidate_partitions"`
	EmittingPartitions       int                          `json:"emitting_partitions"`
	RecordsPerPartition      int64                        `json:"records_per_partition"`
	MinimumPartition         t40r1KafkaTotals             `json:"minimum_partition"`
	MaximumPartition         t40r1KafkaTotals             `json:"maximum_partition"`
	ExactOutput              t40r1KafkaTotals             `json:"exact_output"`
	CurrentLimits            candidate.DomainResultLimits `json:"current_limits"`
	RequiredEqualReservation t40r1KafkaTotals             `json:"required_equal_reservation"`
	PerPartitionByteBackstop int64                        `json:"per_partition_byte_backstop"`
	EstablishesScalePass     bool                         `json:"establishes_scale_pass"`
	AuthorizesFreeze         bool                         `json:"authorizes_freeze"`
}

func TestT40R1ExactKafkaResultMeasurement(t *testing.T) {
	measurement := measureT40R1KafkaResult(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "spike", "t4013", "take19-kafka-result-measurement.json"))
	if err != nil {
		t.Fatal(err)
	}
	var retained t40r1KafkaMeasurement
	if json.Unmarshal(raw, &retained) != nil || !reflect.DeepEqual(retained, measurement) {
		t.Fatalf("retained Kafka measurement differs:\n got %+v\nwant %+v", retained, measurement)
	}
}

func measureT40R1KafkaResult(t *testing.T) t40r1KafkaMeasurement {
	t.Helper()
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var profile t401.Profile
	for _, current := range profiles {
		if current.Kind == "semantic" {
			profile = current
			break
		}
	}
	profileDigest, err := t401.ProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	extractor := kafkago.NewProducer()
	const partitionSize = uint64(candidate.MaxRecordsPerArtifact)
	var aggregate t40r1KafkaTotals
	assertionIDs := make(map[string]struct{}, 131_072)
	minimum := t40r1KafkaTotals{CanonicalBytes: 1<<63 - 1, EncodedBytes: 1<<63 - 1}
	var maximum t40r1KafkaTotals
	for _, family := range []struct {
		name  string
		start uint64
	}{
		{name: "go_kafka_literal", start: 65_536},
		{name: "go_dynamic_topic", start: 196_608},
	} {
		for partition := uint64(0); partition < 16; partition++ {
			corpus := t40r1KafkaCorpus{content: make(map[string]string, partitionSize)}
			for offset := uint64(0); offset < partitionSize; offset++ {
				ordinal := family.start + partition*partitionSize + offset
				path, content, fixtureErr := t401.FrozenSemanticGoFixture(profile, family.name, ordinal)
				if fixtureErr != nil {
					t.Fatal(fixtureErr)
				}
				corpus.paths = append(corpus.paths, path)
				corpus.content[path] = string(content)
			}
			var facts []sdk.Fact
			coverage, extractErr := extractor.Extract(t.Context(), corpus, func(fact sdk.Fact) error {
				facts = append(facts, fact)
				return nil
			})
			if extractErr != nil || len(coverage.Failures) != 0 || len(facts) != int(partitionSize) {
				t.Fatalf("Kafka family %s partition %d: facts=%d coverage=%+v err=%v", family.name, partition, len(facts), coverage, extractErr)
			}
			current := t40r1KafkaTotals{Facts: int64(len(facts)), Rows: int64(len(facts))}
			for _, fact := range facts {
				atom := store.EvidenceAtom{
					SchemaVersion: fact.Atom.SchemaVersion, BlobDigest: fact.Atom.BlobDigest,
					StartByte: fact.Atom.StartByte, EndByte: fact.Atom.EndByte,
					RuleID: fact.Atom.RuleID, ExtractorVersion: extractor.Version(),
					AdapterConfigDigest: fact.Atom.AdapterConfigDigest,
					FactFingerprint: fact.Atom.FactFingerprint,
				}
				atom.ID = store.ComputeAtomID(atom)
				assertion := store.Assertion{
					Predicate: fact.Assertion.Predicate, Subject: fact.Assertion.Subject,
					Object: fact.Assertion.Object, Lineage: fact.Assertion.Lineage,
					Tier: fact.Assertion.Tier, CodeRole: fact.Assertion.CodeRole,
					Repo: t401.RepositoryName, Supporting: []string{atom.ID}, Detail: fact.Assertion.Detail,
				}
				assertionID := store.ComputeAssertionID(assertion)
				if _, duplicate := assertionIDs[assertionID]; duplicate {
					t.Fatal("Kafka measurement produced a duplicate assertion identity")
				}
				assertionIDs[assertionID] = struct{}{}
				current.Rows++
				current.References += int64(len(assertion.Supporting))
			}
			for start := 0; start < len(facts); start += evidenceChunkSize {
				end := min(start+evidenceChunkSize, len(facts))
				chunk := buildFactChunkForNamespace(uint64(start/evidenceChunkSize), facts[start:end], "measurement")
				raw, marshalErr := json.Marshal(chunk)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				current.CanonicalBytes += int64(len(raw))
				current.EncodedBytes += int64(len(raw))
			}
			aggregate = addT40R1KafkaTotals(aggregate, current)
			if current.CanonicalBytes < minimum.CanonicalBytes {
				minimum = current
			}
			if current.CanonicalBytes > maximum.CanonicalBytes {
				maximum = current
			}
		}
	}
	return t40r1KafkaMeasurement{
		Schema: t40r1KafkaMeasurementSchema, Profile: profile.Name, ProfileDigest: profileDigest,
		Domain: extractor.Domain(), ExtractorVersion: extractor.Version(),
		CandidatePartitions: 64, EmittingPartitions: 32, RecordsPerPartition: int64(partitionSize),
		MinimumPartition: minimum, MaximumPartition: maximum, ExactOutput: aggregate,
		CurrentLimits: candidate.FrozenDomainResultLimitsV2(),
		RequiredEqualReservation: t40r1KafkaTotals{
			Facts: 262_144, Rows: 524_288, References: 262_144,
			CanonicalBytes: 256 << 20, EncodedBytes: 256 << 20,
		},
		PerPartitionByteBackstop: 64 << 20,
	}
}

func addT40R1KafkaTotals(left, right t40r1KafkaTotals) t40r1KafkaTotals {
	return t40r1KafkaTotals{
		Facts: left.Facts + right.Facts, Rows: left.Rows + right.Rows,
		References:     left.References + right.References,
		CanonicalBytes: left.CanonicalBytes + right.CanonicalBytes,
		EncodedBytes:   left.EncodedBytes + right.EncodedBytes,
	}
}

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

// partitionedKafkaStore serves one partitioned kafka-producer root behind a
// fence the test controls: the production publication shape the T55.3
// real-binary run found topic usage could not read.
type partitionedKafkaStore struct {
	*proofAPIStore
	root        store.PartitionedExtractionDomainReference
	manifest    store.CandidateManifestPublication
	current     bool
	nativeReads int
}

const kafkaNativeRun = "native-kafka-run"

func (s *partitionedKafkaStore) authority() store.PartitionedAssertionAuthority {
	return store.PartitionedAssertionAuthority{
		Repository: atlasRepo, Domain: "kafka-producer", RunID: s.root.RunID,
		PlanDigest: s.root.PlanDigest, RootDigest: s.root.RootDigest, Commit: catalogCommit,
		CandidateManifestDigest: s.manifest.ManifestDigest, CandidatePolicyDigest: s.manifest.PolicyDigest,
	}
}

func (s *partitionedKafkaStore) GetPartitionedExtractionDomainReference(_ context.Context, repository, domain string) (store.PartitionedExtractionDomainReference, error) {
	if repository != atlasRepo || domain != "kafka-producer" {
		return store.PartitionedExtractionDomainReference{}, store.ErrNotFound
	}
	return s.root, nil
}

func (s *partitionedKafkaStore) GetCandidateManifestPublication(_ context.Context, repository string) (*store.CandidateManifestPublication, error) {
	manifest := s.manifest
	return &manifest, nil
}

func (s *partitionedKafkaStore) ListPartitionedAssertions(ctx context.Context, query store.AssertionQuery, authority store.PartitionedAssertionAuthority) ([]store.Assertion, error) {
	if !s.current || authority != s.authority() {
		return nil, store.ErrNotFound
	}
	return s.ListAssertions(ctx, query)
}

func (s *partitionedKafkaStore) ResolvePartitionedEvidence(_ context.Context, authority store.PartitionedAssertionAuthority, atomID string) (*store.EvidenceResolution, error) {
	s.nativeReads++
	if !s.current || authority != s.authority() {
		return nil, store.ErrNotFound
	}
	resolution, ok := s.resolutions[proofEvidenceScope(authority.Repository, authority.RunID, atomID)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &resolution, nil
}

// TestKafkaTopicUsageReadsTheCurrentPartitionedRoot pins the T55.3 fix: with no
// legacy run, topic usage and its census read the fenced current partitioned
// producer root, name it in exact_authorities, and resolve citations through
// it; a stale root leaves the plane unpublished rather than partial.
func TestKafkaTopicUsageReadsTheCurrentPartitionedRoot(t *testing.T) {
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	base := &proofAPIStore{
		repos:       []store.Repo{{Name: atlasRepo, IndexedCommitHash: catalogCommit}},
		runs:        map[string]store.ExtractionRun{},
		assertions:  map[string][]store.Assertion{},
		resolutions: map[string]store.EvidenceResolution{},
		bundles:     map[string]store.ProofBundleRecord{},
	}
	produces, producesResolution := proofAssertion(atlasRepo, kafkaNativeRun, "produces", "PRODUCES_TO_TOPIC", "topic:orders-v1", "provisional_repo_path_v1_"+strings.Repeat("a", 64), "orders")
	unresolved, unresolvedResolution := proofAssertion(atlasRepo, kafkaNativeRun, "abstains", "UNRESOLVED_KAFKA_PRODUCER", "unresolved:selector-expr", "", "cfg")
	unresolved.Supporting = append(unresolved.Supporting, "atom-second-site")
	base.assertions[atlasRepo] = []store.Assertion{produces, unresolved}
	base.resolutions[proofEvidenceScope(atlasRepo, kafkaNativeRun, produces.Supporting[0])] = producesResolution
	base.resolutions[proofEvidenceScope(atlasRepo, kafkaNativeRun, unresolved.Supporting[0])] = unresolvedResolution
	st := &partitionedKafkaStore{
		proofAPIStore: base, current: true,
		root: store.PartitionedExtractionDomainReference{
			Schema: store.PartitionedExtractionDomainSchema, Repository: atlasRepo, Domain: "kafka-producer",
			RunID: kafkaNativeRun, PlanDigest: digest("1"), RootDigest: digest("2"), CandidateDigest: digest("3"),
			SourceDigest: digest("4"), ObservationDigest: digest("5"),
		},
		manifest: store.CandidateManifestPublication{
			Repository: atlasRepo, HeadCommit: catalogCommit, ManifestDigest: digest("3"), PolicyDigest: digest("6"),
		},
	}
	handler := api.New(api.Options{
		Version: "test", Store: st, Evidence: st, ProofBundles: st, KafkaProducerOnly: true,
		Visible:               func(context.Context) func(store.Repo) bool { return nil },
		Principal:             func(context.Context) string { return "user:member" },
		AuthorizationProvider: "test-permissions-v1",
	})
	code, body, envelope := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1")
	census := envelope.Bundle.UnresolvedCensus
	if code != http.StatusOK || len(envelope.Bundle.Assertions) != 1 || len(envelope.Bundle.Evidence) != 1 ||
		census == nil || census.ProducerPublishedRuns != 1 || census.Producer["selector-expr"] != 2 ||
		len(envelope.Bundle.ExactAuthorities) != 1 || envelope.Bundle.ExactAuthorities[0].RunID != kafkaNativeRun ||
		st.nativeReads == 0 {
		t.Fatalf("partitioned topic usage = %d %s (native reads %d)", code, body, st.nativeReads)
	}

	if code, _, read := getProof(t, handler, "/api/proof_bundles/"+envelope.ID); code != http.StatusOK ||
		len(read.Bundle.ExactAuthorities) != 1 {
		t.Fatalf("partitioned bundle read = %d", code)
	}

	st.manifest.HeadCommit = strings.Repeat("b", 40) // the root belongs to another candidate generation
	code, body, stale := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1")
	if code != http.StatusOK || len(stale.Bundle.Assertions) != 0 || stale.Bundle.UnresolvedCensus.ProducerPublishedRuns != 0 ||
		len(stale.Bundle.ExactAuthorities) != 0 {
		t.Fatalf("stale partitioned root was served: %d %s", code, body)
	}
}

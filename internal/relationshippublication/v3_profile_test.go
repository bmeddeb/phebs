package relationshippublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

type t411DistributionV3 struct {
	name     string
	services int
}

type t411RPCSourceV3 struct {
	root         rpccallerposting.Root
	distribution t411DistributionV3
}

func (source t411RPCSourceV3) Root() rpccallerposting.Root { return source.root }

func (source t411RPCSourceV3) WalkPostings(
	ctx context.Context,
	visit func(rpccallerposting.Posting) error,
) error {
	return source.distribution.walk(ctx, visit, nil)
}

type t411KafkaSourceV3 struct {
	root         kafkatopicposting.Root
	distribution t411DistributionV3
}

func (source t411KafkaSourceV3) Root() kafkatopicposting.Root { return source.root }

func (source t411KafkaSourceV3) WalkPostings(
	ctx context.Context,
	visit func(kafkatopicposting.Posting) error,
) error {
	return source.distribution.walk(ctx, nil, visit)
}

func TestT411MaximumProfileEmptyAndMixedBuildPublishV3(t *testing.T) {
	profile := t411MaximumProfileV3(t)
	fixture := t411MaximumFixtureV3(t, profile)
	for _, name := range []string{"empty", "mixed"} {
		t.Run(name, func(t *testing.T) {
			runT411DistributionV3(t, profile, fixture, name)
		})
	}
}

func TestT411MaximumProfileDenseBuildPublishV3(t *testing.T) {
	profile := t411MaximumProfileV3(t)
	fixture := t411MaximumFixtureV3(t, profile)
	runT411DistributionV3(t, profile, fixture, "dense")
}

type t411MaximumFixture struct {
	repository string
	generation servicecatalogv3.Generation
	states     []servicecatalog.ServiceState
	summary    servicecatalog.RepositoryState
	upstream   downstreamauthority.Authority
	resolver   fakeResolver
}

type t411ProfileV3 struct {
	AcceptedServices int `json:"accepted_services"`
	Memberships      int `json:"memberships"`
	DistinctPaths    int `json:"distinct_paths"`
	UnownedPaths     int `json:"unowned_paths"`
	Authority        struct {
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"authority"`
	Relationships []struct {
		Name  string `json:"name"`
		Edges int    `json:"edges"`
	} `json:"relationship_distributions"`
}

func t411MaximumProfileV3(t *testing.T) t411ProfileV3 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "spike", "t411", "envelope.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Schema   string          `json:"schema"`
		Profiles []t411ProfileV3 `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Schema != "t411-service-load-envelope-v1" {
		t.Fatalf("decode frozen T41.1 envelope: %v", err)
	}
	for _, profile := range envelope.Profiles {
		if profile.AcceptedServices == MaxServicesV3 {
			return profile
		}
	}
	t.Fatal("T41.1 maximum profile is absent")
	return t411ProfileV3{}
}

func t411MaximumFixtureV3(t *testing.T, profile t411ProfileV3) t411MaximumFixture {
	t.Helper()
	repository := "example.com/acme/t411-maximum-relationships"
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: profile.Authority.Kind, ID: profile.Authority.ID, Version: profile.Authority.Version,
		},
		Services:    make([]servicecatalog.Service, 0, profile.AcceptedServices),
		Memberships: make([]servicecatalog.Membership, 0, profile.Memberships),
		Unowned:     make([]servicecatalog.UnownedPlacement, 0, profile.UnownedPaths),
	}
	for index := range profile.AcceptedServices {
		key := fmt.Sprintf("svc.load-%05d", index)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
		for _, membership := range []struct {
			path string
			role string
		}{
			{fmt.Sprintf("services/service-%05d/main.go", index), servicecatalog.RolePrimary},
			{fmt.Sprintf("contracts/service-%05d/api.proto", index), servicecatalog.RoleSupporting},
			{fmt.Sprintf("contracts/service-%05d/api.proto", index), servicecatalog.RoleTyped},
			{fmt.Sprintf("shared/group-%04d/library.go", index/20), servicecatalog.RoleShared},
			{fmt.Sprintf("generated/service-%05d/client.pb.go", index), servicecatalog.RoleGenerated},
			{fmt.Sprintf("generated/shared/group-%04d/types.pb.go", index/10), servicecatalog.RoleGenerated},
		} {
			catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
				ServiceKey: key, Path: membership.path, Role: membership.role,
				Origin: servicecatalog.OriginBase,
			})
		}
	}
	for index := range profile.UnownedPaths {
		catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
			Path: fmt.Sprintf("tools/unowned-%04d.go", index), Origin: servicecatalog.OriginBase,
		})
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator, Path: "/t411-service-catalog.json",
			Commit: strings.Repeat("a", 40), CensusDigest: fixedDigest("c"),
			FileCount:         profile.DistinctPaths,
			AcceptedFileCount: profile.DistinctPaths - profile.UnownedPaths,
			UnownedFileCount:  profile.UnownedPaths,
		},
		Authority: catalog.Authority,
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := generation.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(opened.Services) != profile.AcceptedServices ||
		len(opened.Memberships) != profile.Memberships ||
		generation.Root.Paths != profile.DistinctPaths {
		t.Fatalf("T41.1 catalog shape = services %d memberships %d paths %d",
			len(opened.Services), len(opened.Memberships), generation.Root.Paths)
	}
	states, summary := relationshipStatesV3Test(t, generation.Root, opened)
	upstream := relationshipUpstreamV3Test(t, repository)
	return t411MaximumFixture{
		repository: repository, generation: generation, states: states, summary: summary,
		upstream: upstream, resolver: relationshipResolverV3Test(t, repository, upstream),
	}
}

func runT411DistributionV3(
	t *testing.T,
	profile t411ProfileV3,
	fixture t411MaximumFixture,
	name string,
) {
	t.Helper()
	started := time.Now()
	distribution := t411DistributionV3{name: name, services: profile.AcceptedServices}
	rpc := t411RPCSourceV3{
		distribution: distribution,
		root:         t411RPCRootV3(t, fixture.repository, fixture.resolver.root, fixture.upstream, distribution),
	}
	kafka := t411KafkaSourceV3{
		distribution: distribution,
		root:         t411KafkaRootV3(t, fixture.repository, rpc.root.Authority, fixture.upstream, distribution),
	}
	prepared, err := BuildV3(t.Context(), BuildRequestV3{
		Root: t.TempDir(), Catalog: fixture.generation, States: fixture.states,
		ServiceSummary: fixture.summary, Resolver: fixture.resolver,
		RPC: rpc, Kafka: kafka, Upstream: fixture.upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	pins := &testPublishPinsV3{}
	publication, err := PublishV3(t.Context(), prepared, pins)
	if err != nil {
		t.Fatal(err)
	}
	root := publication.Root()
	expectedReferences := t411RelationshipEdgesV3(t, profile, name)
	expectedProjections := expectedReferences
	if name == "dense" {
		expectedProjections /= 20
	}
	expectedComplete, expectedEmpty := profile.AcceptedServices, 0
	if name == "empty" {
		expectedComplete, expectedEmpty = 0, profile.AcceptedServices
	}
	if root.ServiceCount != profile.AcceptedServices ||
		root.ProjectionCount != expectedProjections ||
		root.ServiceReferenceCount != expectedReferences || root.FailedServiceCount != 0 ||
		root.CompleteServiceCount != expectedComplete || root.EmptyServiceCount != expectedEmpty ||
		pins.relationshipPins != 1 || len(pins.runs) != 1 {
		t.Fatalf("T41.1 %s root = services %d projections %d refs %d complete/empty/failed %d/%d/%d pins %d/%d",
			name, root.ServiceCount, root.ProjectionCount, root.ServiceReferenceCount,
			root.CompleteServiceCount, root.EmptyServiceCount, root.FailedServiceCount,
			pins.relationshipPins, len(pins.runs))
	}
	if _, err := ValidateGenerationV3(
		t.Context(), prepared.root, fixture.repository, root.GenerationDigest, root.Digest,
	); err != nil {
		t.Fatalf("validate T41.1 %s publication: %v", name, err)
	}
	first, err := publication.ReadService(t.Context(), "svc.load-00000")
	if err != nil || first.State == "failed" {
		t.Fatalf("T41.1 %s first service = %+v, %v", name, first, err)
	}
	wantFirstReferences := 1
	switch name {
	case "empty":
		wantFirstReferences = 0
	case "dense":
		wantFirstReferences = 19
	}
	if len(first.References) != wantFirstReferences {
		t.Fatalf("T41.1 %s first references = %d, want %d",
			name, len(first.References), wantFirstReferences)
	}
	t.Logf("T41.1 %s BuildV3+publish+second validation: %s; repository=%d B service=%d B files=%d",
		name, time.Since(started).Round(time.Millisecond), root.EncodedRepositoryBytes,
		root.EncodedServiceBytes, 1+len(root.RepositoryMembers)+len(root.ServiceMembers))
}

func t411RelationshipEdgesV3(t *testing.T, profile t411ProfileV3, name string) int {
	t.Helper()
	for _, distribution := range profile.Relationships {
		if distribution.Name == name {
			return distribution.Edges
		}
	}
	t.Fatalf("T41.1 relationship distribution %q is absent", name)
	return 0
}

func (distribution t411DistributionV3) walk(
	ctx context.Context,
	rpcVisit func(rpccallerposting.Posting) error,
	kafkaVisit func(kafkatopicposting.Posting) error,
) error {
	emitRPC := func(posting rpccallerposting.Posting) error {
		if rpcVisit == nil {
			return nil
		}
		return rpcVisit(posting)
	}
	emitKafka := func(posting kafkatopicposting.Posting) error {
		if kafkaVisit == nil {
			return nil
		}
		return kafkaVisit(posting)
	}
	switch distribution.name {
	case "empty":
		return nil
	case "mixed":
		for index := range distribution.services {
			if index%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			path := fmt.Sprintf("services/service-%05d/main.go", index)
			identity := fmt.Sprintf("mixed-%05d", index)
			if index%2 == 0 {
				if err := emitRPC(t411RPCPostingV3(identity, path)); err != nil {
					return err
				}
			} else if err := emitKafka(t411KafkaPostingV3(identity, path)); err != nil {
				return err
			}
		}
		return nil
	case "dense":
		groups := distribution.services / 20
		for group := range groups {
			if group%64 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			path := fmt.Sprintf("shared/group-%04d/library.go", group)
			for edge := range 19 {
				if err := emitRPC(t411RPCPostingV3(
					fmt.Sprintf("dense-%04d-%02d", group, edge), path,
				)); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown T41.1 relationship distribution %q", distribution.name)
	}
}

func t411RPCPostingV3(identity, path string) rpccallerposting.Posting {
	sum := sha256.Sum256([]byte("t411-rpc-" + identity))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	value := rpccallerposting.Posting{
		Schema: rpccallerposting.PostingSchema, Protocol: "grpc", Class: "unresolved",
		Reason: "dynamic_receiver", CandidateOperations: []string{},
		ObjectID: hex.EncodeToString(sum[:])[:40], ContentDigest: digest,
		Path: path, Mode: "100644", Revisions: []int{0},
		Span: sourceobservation.Span{
			StartByte: 1, EndByte: 2, StartLine: 1, EndLine: 1,
		},
		SourceRole: "production", ResolverRecordDigests: []string{},
	}
	value.Digest = postingDigest(value)
	return value
}

func t411KafkaPostingV3(identity, path string) kafkatopicposting.Posting {
	sum := sha256.Sum256([]byte("t411-kafka-" + identity))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	value := kafkatopicposting.Posting{
		Schema: kafkatopicposting.PostingSchema, Plane: "producer", Class: "literal",
		TopicSpelling: "topic." + identity, Library: "sarama",
		ImportPath: "github.com/IBM/sarama", Shape: "t411", Binding: "literal",
		ObjectID: hex.EncodeToString(sum[:])[:40], ContentDigest: digest,
		Path: path, Mode: "100644", Revisions: []int{0},
		Span: sourceobservation.Span{
			StartByte: 1, EndByte: 2, StartLine: 1, EndLine: 1,
		},
		SourceRole: "production",
	}
	value.Digest = postingDigest(value)
	return value
}

func t411RPCRootV3(
	t *testing.T,
	repository string,
	resolver resolvernamespace.Root,
	upstream downstreamauthority.Authority,
	distribution t411DistributionV3,
) rpccallerposting.Root {
	t.Helper()
	policy := rpccallerposting.FrozenPolicy()
	value := rpccallerposting.Root{
		Schema: rpccallerposting.RootSchemaV2,
		Authority: rpccallerposting.Authority{
			Repository:                  repository,
			ObservationGenerationDigest: upstream.Observation.ObservationGenerationDigest,
			ObservationManifestDigest:   upstream.Observation.ObservationRootDigest,
			ObservationSourceDigest:     upstream.Observation.SourceGenerationDigest,
			ResolverCommit:              resolver.Authority.Commit,
			ResolverGenerationDigest:    resolver.GenerationDigest,
			ResolverRootDigest:          resolver.Digest, PolicyDigest: mustDigest(t, policy),
			ObservationV2: &upstream.Observation, Upstream: &upstream,
		},
		Policy: policy, Members: []rpccallerposting.MemberReceipt{},
	}
	groups := make(map[int]int)
	if err := distribution.walk(t.Context(), func(posting rpccallerposting.Posting) error {
		groups[rpcBucket(posting)]++
		value.PostingCount++
		value.UnresolvedCount++
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	buckets := make([]int, 0, len(groups))
	for bucket := range groups {
		buckets = append(buckets, bucket)
	}
	slices.Sort(buckets)
	for _, bucket := range buckets {
		value.Members = append(value.Members, rpccallerposting.MemberReceipt{
			Protocol: "grpc", Bucket: bucket, Name: fmt.Sprintf("grpc-%02x.json", bucket),
			PostingCount: groups[bucket], ContentBytes: 1, ContentDigest: fixedDigest("6"),
		})
		value.EncodedMemberBytes++
	}
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := rpccallerposting.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func t411KafkaRootV3(
	t *testing.T,
	repository string,
	observation rpccallerposting.Authority,
	upstream downstreamauthority.Authority,
	distribution t411DistributionV3,
) kafkatopicposting.Root {
	t.Helper()
	policy := kafkatopicposting.FrozenPolicy()
	value := kafkatopicposting.Root{
		Schema: kafkatopicposting.RootSchemaV2,
		Authority: kafkatopicposting.Authority{
			Repository:                  repository,
			ObservationGenerationDigest: observation.ObservationGenerationDigest,
			ObservationManifestDigest:   observation.ObservationManifestDigest,
			ObservationSourceDigest:     observation.ObservationSourceDigest,
			PolicyDigest:                mustDigest(t, policy), ObservationV2: &upstream.Observation,
			Upstream: &upstream,
		},
		Policy: policy, Members: []kafkatopicposting.MemberReceipt{},
	}
	groups := make(map[int]int)
	if err := distribution.walk(t.Context(), nil, func(posting kafkatopicposting.Posting) error {
		groups[kafkaBucket(posting)]++
		value.PostingCount++
		value.ProducerCount++
		value.LiteralCount++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	buckets := make([]int, 0, len(groups))
	for bucket := range groups {
		buckets = append(buckets, bucket)
	}
	slices.Sort(buckets)
	for _, bucket := range buckets {
		value.Members = append(value.Members, kafkatopicposting.MemberReceipt{
			Plane: "producer", Bucket: bucket, Name: fmt.Sprintf("producer-%03d.json", bucket),
			PostingCount: groups[bucket], ContentBytes: 1, ContentDigest: fixedDigest("7"),
		})
		value.EncodedMemberBytes++
	}
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := kafkatopicposting.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return value
}

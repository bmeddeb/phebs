package rpccallerposting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
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

type countingResolver struct {
	publication *resolvernamespace.Publication
	reads       int
}

func (resolver *countingResolver) Root() resolvernamespace.Root {
	return resolver.publication.Root()
}

func (resolver *countingResolver) LookupNamespace(
	ctx context.Context,
	language, protocol, namespace string,
) ([]resolvernamespace.Record, error) {
	resolver.reads++
	return resolver.publication.LookupNamespace(ctx, language, protocol, namespace)
}

func TestBuildPublishesResolvedNameMatchUnresolvedAndOccurrenceIdentity(t *testing.T) {
	root := t.TempDir()
	operation := "payments.v1.Payments/Charge"
	for partitionBucket(operation) == partitionBucket("__unresolved__") {
		operation += "X"
	}
	descriptor := grpcDescriptor(
		"example.test/payments/v1", "PaymentsClient", "Charge", operation, "a",
	)
	resolver := &countingResolver{publication: resolverPublication(t, root, []gocaller.DirectDescriptor{descriptor})}
	resolvedSource := `package app
import pb "example.test/payments/v1"
func charge(client pb.PaymentsClient) { client.Charge(nil) }
`
	nameSource := `package app
import pb "example.test/payments/v1"
func charge(client pb.OtherClient) { client.Charge(nil) }
`
	dynamicSource := `package app
import pb "example.test/payments/v1"
func charge(client any) { _ = pb.Marker; client.Charge(nil) }
`
	resolved := observationFixture(t, "cmd/charge.go", resolvedSource, []sourcepartition.Placement{
		{Path: "cmd/charge.go", Mode: "100644", Revisions: []int{0}},
		{Path: "copy/charge.go", Mode: "100644", Revisions: []int{0, 1}},
		{Path: "cmd/charge_test.go", Mode: "100644", Revisions: []int{1}},
		{Path: descriptor.GeneratedPath, Mode: "100644", Revisions: []int{0}},
	})
	nameMatch := observationFixture(t, "cmd/name.go", nameSource, []sourcepartition.Placement{
		{Path: "cmd/name.go", Mode: "100644", Revisions: []int{0}},
	})
	dynamic := observationFixture(t, "cmd/dynamic.go", dynamicSource, []sourcepartition.Placement{
		{Path: "vendor/acme/dynamic.go", Mode: "100644", Revisions: []int{0}},
	})
	source := fakeSource(t, []observedFixture{resolved, nameMatch, dynamic})
	prepared, err := buildSources(t.Context(), root, source, resolver)
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
	if rootValue.ResolvedCount != 3 || rootValue.NameMatchCount != 1 || rootValue.UnresolvedCount != 2 {
		t.Fatalf("class totals = %+v", rootValue)
	}
	resolvedPostings, err := publication.ReadOperation(t.Context(), "grpc", operation)
	if err != nil || len(resolvedPostings) != 4 {
		t.Fatalf("operation postings = %+v, %v", resolvedPostings, err)
	}
	reopened, err := OpenGeneration(
		t.Context(), root, rootValue.Authority.Repository,
		rootValue.GenerationDigest, rootValue.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	byDigest, err := reopened.ReadDigest(
		t.Context(), "grpc", operation, resolvedPostings[0].Digest,
	)
	if err != nil || byDigest.Digest != resolvedPostings[0].Digest {
		t.Fatalf("exact generation digest read = %+v, %v", byDigest, err)
	}
	classes := map[string]int{}
	roles := map[string]int{}
	digests := map[string]struct{}{}
	for _, posting := range resolvedPostings {
		classes[posting.Class]++
		roles[posting.SourceRole]++
		digests[posting.Digest] = struct{}{}
		if posting.Class == "name_match" && posting.Operation != "" {
			t.Fatal("name match asserted an operation")
		}
	}
	if classes["resolved"] != 3 || classes["name_match"] != 1 ||
		roles["production"] != 3 || roles["test"] != 1 || len(digests) != 4 {
		t.Fatalf("classes=%v roles=%v identities=%d", classes, roles, len(digests))
	}
	unresolved, err := publication.ReadUnresolved(t.Context(), "grpc")
	if err != nil || len(unresolved) != 2 {
		t.Fatalf("unresolved postings = %+v, %v", unresolved, err)
	}
	reasons := []string{unresolved[0].Reason, unresolved[1].Reason}
	slices.Sort(reasons)
	if !slices.Equal(reasons, []string{"dynamic_receiver", "resolver_generated_input"}) ||
		unresolved[0].Operation != "" || unresolved[1].Operation != "" {
		t.Fatalf("unresolved classifications = %+v", unresolved)
	}
	// The same imported namespace is opened once per protocol across all three
	// observation records. Empty thrift authority is cached as deliberately as grpc.
	if resolver.reads != 2 {
		t.Fatalf("namespace reads = %d, want 2", resolver.reads)
	}

	second, err := buildSources(t.Context(), root, source, resolver)
	if err != nil {
		t.Fatal(err)
	}
	secondPublication, err := second.Publish(t.Context())
	if err != nil || secondPublication.Root().Digest != rootValue.Digest {
		t.Fatalf("deterministic rebuild = %v, %v", secondPublication, err)
	}

	// Corrupting the unresolved bucket cannot affect the operation-keyed read.
	unresolvedReceipt := memberReceipt(t, publication, "grpc", partitionBucket("__unresolved__"))
	if err := os.WriteFile(filepath.Join(publication.directory, unresolvedReceipt.Name), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if values, err := publication.ReadOperation(t.Context(), "grpc", operation); err != nil || len(values) != 4 {
		t.Fatalf("sparse operation read touched unresolved sibling: %+v, %v", values, err)
	}
	if _, err := publication.ReadUnresolved(t.Context(), "grpc"); err == nil {
		t.Fatal("corrupt selected unresolved member was accepted")
	}
}

func TestConflictAndDynamicSitesNeverResolve(t *testing.T) {
	root := t.TempDir()
	first := grpcDescriptor("example.test/shared", "SharedClient", "Get", "one.Service/Get", "a")
	second := grpcDescriptor("example.test/shared", "SharedClient", "Get", "two.Service/Get", "b")
	resolver := resolverPublication(t, root, []gocaller.DirectDescriptor{first, second})
	sourceText := `package app
import pb "example.test/shared"
func call(client pb.SharedClient) { client.Get(nil) }
`
	source := fakeSource(t, []observedFixture{
		observationFixture(t, "call.go", sourceText, []sourcepartition.Placement{{
			Path: "call.go", Mode: "100644", Revisions: []int{0},
		}}),
	})
	prepared, err := buildSources(t.Context(), root, source, resolver)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	values, err := publication.ReadUnresolved(t.Context(), "grpc")
	if err != nil || len(values) != 1 || values[0].Reason != "resolver_conflict" ||
		values[0].Operation != "" ||
		!slices.Equal(values[0].CandidateOperations, []string{"one.Service/Get", "two.Service/Get"}) {
		t.Fatalf("conflict posting = %+v, %v", values, err)
	}
}

func TestResolvedPostingMatchesDirectCallerSupportedCase(t *testing.T) {
	root := t.TempDir()
	operation := "payments.v1.Payments/Charge"
	descriptor := grpcDescriptor("example.test/payments/v1", "PaymentsClient", "Charge", operation, "a")
	resolver := resolverPublication(t, root, []gocaller.DirectDescriptor{descriptor})
	sourceText := `package app
import pb "example.test/payments/v1"
func charge(client pb.PaymentsClient) { client.Charge(nil) }
`
	fixture := observationFixture(t, "cmd/charge.go", sourceText, []sourcepartition.Placement{{
		Path: "cmd/charge.go", Mode: "100644", Revisions: []int{0},
	}})
	prepared, err := buildSources(
		t.Context(), root, fakeSource(t, []observedFixture{fixture}), resolver,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	postings, err := publication.ReadOperation(t.Context(), "grpc", operation)
	if err != nil || len(postings) != 1 {
		t.Fatalf("postings = %+v, %v", postings, err)
	}
	facts := []sdk.Fact{}
	_, err = gocaller.ScanDirectSyntaxFile(t.Context(), gocaller.DirectScanRequest{
		Protocol: "grpc", Path: "cmd/charge.go",
		Blob:                  sdk.Blob{Content: sourceText, Digest: fixture.observation.ContentDigest},
		Descriptors:           []gocaller.DirectDescriptor{descriptor},
		ResolverCatalogDigest: resolver.Root().Digest,
	}, func(fact sdk.Fact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil || len(facts) != 1 || facts[0].Assertion.Predicate != "CALLS_OPERATION" {
		t.Fatalf("direct facts = %+v, %v", facts, err)
	}
	posting := postings[0]
	if posting.Operation != facts[0].Assertion.Object || posting.Path != facts[0].Path ||
		posting.Span.StartByte != facts[0].Atom.StartByte ||
		posting.Span.EndByte != facts[0].Atom.EndByte {
		t.Fatalf("posting/direct parity: posting=%+v fact=%+v", posting, facts[0])
	}

	nameSource := `package app
import pb "example.test/payments/v1"
func charge(client pb.OtherClient) { client.Charge(nil) }
`
	nameObservation, err := sourceobservation.Parse(
		t.Context(), sourceobservation.Input{Path: "cmd/name.go", Content: nameSource},
	)
	if err != nil {
		t.Fatal(err)
	}
	projection, present, err := projectCall(
		t.Context(),
		&namespaceCache{resolver: resolver, values: make(map[string][]resolvernamespace.Record)},
		nameObservation, nameObservation.Functions[0].Calls[0], "grpc",
	)
	if err != nil || !present || projection.class != "name_match" ||
		!slices.Equal(projection.candidates, []string{operation}) || projection.operation != "" {
		t.Fatalf("name-match projection = %+v, %t, %v", projection, present, err)
	}
	nameFacts := []sdk.Fact{}
	_, err = gocaller.ScanDirectSyntaxFile(t.Context(), gocaller.DirectScanRequest{
		Protocol: "grpc", Path: "cmd/name.go",
		Blob:                  sdk.Blob{Content: nameSource, Digest: nameObservation.ContentDigest},
		Descriptors:           []gocaller.DirectDescriptor{descriptor},
		ResolverCatalogDigest: resolver.Root().Digest,
	}, func(fact sdk.Fact) error {
		nameFacts = append(nameFacts, fact)
		return nil
	})
	if err != nil || len(nameFacts) != 1 ||
		nameFacts[0].Assertion.Predicate != "UNRESOLVED_CALLER" ||
		nameFacts[0].Assertion.Object != projection.candidates[0] {
		t.Fatalf("name-match/direct parity = %+v, %v", nameFacts, err)
	}
}

func TestProjectionCandidateAndDigestBoundsBeforeGrowth(t *testing.T) {
	values := make([]resolvernamespace.Record, 0, MaxCandidateOperations+1)
	for index := 0; index <= MaxCandidateOperations; index++ {
		values = append(values, resolvernamespace.Record{
			Kind: "symbol", State: "resolved", Operation: fmt.Sprintf("service.%02d/Get", index),
			Digest: "sha256:" + fmt.Sprintf("%064x", index+1),
		})
	}
	if _, err := projectionFromRecords(values); !errors.Is(err, ErrLimit) {
		t.Fatalf("candidate limit error = %v", err)
	}
}

func TestResidentLimitCarriesExactMeasurement(t *testing.T) {
	root := t.TempDir()
	descriptor := grpcDescriptor(
		"example.test/payments/v1", "PaymentsClient", "Charge",
		"payments.v1.Payments/Charge", "resident",
	)
	resolver := resolverPublication(t, root, []gocaller.DirectDescriptor{descriptor})
	sourceText := `package app
import pb "example.test/payments/v1"
func charge(client pb.PaymentsClient) { client.Charge(nil) }
`
	fixture := observationFixture(t, "cmd/charge.go", sourceText, []sourcepartition.Placement{{
		Path: "cmd/charge.go", Mode: "100644", Revisions: []int{0},
	}})
	_, err := buildSourcesBounded(
		t.Context(), root, fakeSource(t, []observedFixture{fixture}), resolver, 1,
	)
	var measurement *pipelinerefusal.Measurement
	if !errors.As(err, &measurement) || !errors.Is(err, ErrLimit) ||
		measurement.Dimension != pipelinerefusal.DimensionResidentBytes ||
		measurement.Observed <= measurement.Limit || measurement.Limit != 1 {
		t.Fatalf("resident refusal = %+v, %v", measurement, err)
	}
}

func TestDiscardRemovesStageAfterPreinstallPublishFailure(t *testing.T) {
	root := t.TempDir()
	resolver := &countingResolver{publication: resolverPublication(t, root, nil)}
	fixture := observationFixture(
		t, "cmd/plain.go", "package app\n", []sourcepartition.Placement{{
			Path: "cmd/plain.go", Mode: "100644", Revisions: []int{0},
		}},
	)
	prepared, err := buildSources(
		t.Context(), root, fakeSource(t, []observedFixture{fixture}), resolver,
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
		map[string][]Posting{memberKey("grpc", 0): {}},
	)
	if ctx.removeErr != nil {
		t.Fatal(ctx.removeErr)
	}
	if err == nil || errors.Is(err, ErrLimit) ||
		!strings.Contains(err.Error(), "clean failed RPC caller posting stage") ||
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

func resolverPublication(
	t *testing.T,
	root string,
	descriptors []gocaller.DirectDescriptor,
) *resolvernamespace.Publication {
	t.Helper()
	prepared, err := resolvernamespace.Build(t.Context(), resolvernamespace.BuildRequest{
		Root: root, Repository: "github.com/acme/repo", Commit: strings.Repeat("f", 40),
		ResolverGenerationDigest: "sha256:" + strings.Repeat("1", 64),
		ResolverManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		Descriptors:              descriptors,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func grpcDescriptor(namespace, client, method, operation, seed string) gocaller.DirectDescriptor {
	sum := sha256.Sum256([]byte(seed))
	digest := hex.EncodeToString(sum[:])
	return gocaller.DirectDescriptor{
		State: "resolved", Protocol: "grpc", ImportPath: namespace,
		Package: "fixturepb", ClientType: client, Method: method, Operation: operation,
		Constructors: []string{"New" + client}, GeneratedPath: "gen/" + digest[:12] + ".go",
		GeneratedObjectID: digest[:40], GeneratedDigest: "sha256:" + digest,
		GeneratorRelativePath: "api/" + digest[:12] + ".proto",
		DeclarationPath:       "api/" + digest[:12] + ".proto",
		DeclarationLineage:    "lineage-" + seed,
	}
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
			Schema:   observationpublication.RecordSchema,
			ObjectID: hex.EncodeToString(sum[:])[:40], DeclaredBytes: int64(len(content)),
			Placements: placements, State: "observed", ContentDigest: observation.ContentDigest,
			ObservationDigest: "sha256:" + strings.Repeat("3", 64),
			ObservationName:   "objects/fixture.json", ObservationOrigin: "parsed",
		},
	}
}

func fakeSource(t *testing.T, values []observedFixture) fakeObservationSource {
	t.Helper()
	manifest := observationpublication.Manifest{
		Schema:     observationpublication.ManifestSchema,
		Repository: "github.com/acme/repo", Language: observationpublication.LanguagePack,
		SourceGenerationDigest:    "sha256:" + strings.Repeat("4", 64),
		PartitionGenerationDigest: "sha256:" + strings.Repeat("5", 64),
		PartitionManifestDigest:   "sha256:" + strings.Repeat("6", 64),
		PartitionPolicyDigest:     "sha256:" + strings.Repeat("7", 64),
		ObservationPolicyDigest:   sourceobservation.PolicyDigest(),
		Members: []observationpublication.Member{{
			Ordinal: 0, Count: 1, Name: "member-00000.jsonl",
			SourceMemberDigest: "sha256:" + strings.Repeat("8", 64),
			RecordCount:        len(values), ObservedCount: len(values),
			ContentBytes: 1, Digest: "sha256:" + strings.Repeat("9", 64),
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
		value.Repository, value.SourceGenerationDigest,
		value.PartitionGenerationDigest, value.PartitionManifestDigest,
		value.PartitionPolicyDigest, value.ObservationPolicyDigest, value.Language,
	}, "\x00")
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-observation-generation-v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(joined))
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func memberReceipt(t *testing.T, publication *Publication, protocol string, bucket int) MemberReceipt {
	t.Helper()
	for _, receipt := range publication.Root().Members {
		if receipt.Protocol == protocol && receipt.Bucket == bucket {
			return receipt
		}
	}
	t.Fatalf("missing member %s/%d", protocol, bucket)
	return MemberReceipt{}
}

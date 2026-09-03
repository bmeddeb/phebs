package t421

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/resolverinput"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/spike/t401"
)

type productionReplayCorpus struct {
	paths []string
	blobs map[string]sdk.Blob
}

func (c productionReplayCorpus) RepoName() string { return "example.invalid/t421-combined" }
func (c productionReplayCorpus) Commit() string   { return strings.Repeat("a", 40) }

func (c productionReplayCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
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

func (c productionReplayCorpus) Read(ctx context.Context, path string) (sdk.Blob, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Blob{}, err
	}
	blob, ok := c.blobs[path]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("T42.1 replay blob %q is absent", path)
	}
	return blob, nil
}

func TestProductionExtractorsAcceptFullCombinedOverlay(t *testing.T) {
	combined, err := BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	profile := combined.Profile.Pipeline
	corpus := productionReplayCorpus{blobs: make(map[string]sdk.Blob, combined.Profile.Logical.DistinctPaths)}
	if err := WalkCombinedAdditions(func(path string, content []byte) error {
		if len(corpus.paths) > 0 && corpus.paths[len(corpus.paths)-1] >= path {
			return fmt.Errorf("T42.1 replay paths are not strictly ordered at %q", path)
		}
		if _, duplicate := corpus.blobs[path]; duplicate {
			return fmt.Errorf("T42.1 replay path %q is duplicated", path)
		}
		corpus.paths = append(corpus.paths, path)
		corpus.blobs[path] = sdk.Blob{Content: string(content), Digest: SHA256(content)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := uint64(len(corpus.paths)), combined.Profile.Logical.DistinctPaths; got != want {
		t.Fatalf("combined addition files = %d, want %d", got, want)
	}

	policy := resolvermaterialize.FrozenMaterializationPolicy()
	mappingBlob := corpus.blobs[resolverinput.GeneratedFromSnapshotPath]
	mapping, err := resolverinput.DecodeGeneratedFromSnapshot(mappingBlob.Content, resolverinput.SnapshotLimits{
		GeneratedMappings:    policy.MaxGeneratedMappings,
		GeneratorInvocations: policy.MaxGeneratorInvocations,
	})
	if err != nil {
		t.Fatalf("decode generated mapping through production limits: %v", err)
	}
	if mapping.Version != resolverinput.GeneratedFromSnapshotVersion || len(mapping.Invocations) != 0 ||
		uint64(len(mapping.Mappings)) != profile.GeneratedMappings ||
		len(mapping.Mappings) > policy.MaxGeneratedMappings {
		t.Fatalf("generated mapping = version %q, mappings %d, invocations %d", mapping.Version, len(mapping.Mappings), len(mapping.Invocations))
	}

	counts := make(map[string]uint64)
	operationsByPath := make(map[string]uint64, len(mapping.Mappings))
	run := func(extractor sdk.Extractor) sdk.Coverage {
		t.Helper()
		coverage, extractErr := extractor.Extract(
			sdk.WithDiagnosticCounters(t.Context()), corpus,
			func(fact sdk.Fact) error {
				blob, ok := corpus.blobs[fact.Path]
				if !ok || fact.Atom.BlobDigest != blob.Digest || fact.Atom.StartByte < 0 ||
					fact.Atom.EndByte <= fact.Atom.StartByte || fact.Atom.EndByte > len(blob.Content) {
					return fmt.Errorf("%s emitted invalid provenance for %q", extractor.Domain(), fact.Path)
				}
				counts[extractor.Domain()+"\x00"+fact.Assertion.Predicate]++
				if extractor.Domain() == "proto-contract" && fact.Assertion.Predicate == "DECLARES_OPERATION" {
					operationsByPath[fact.Path]++
				}
				return nil
			},
		)
		if extractErr != nil {
			t.Fatalf("%s production replay: %v", extractor.Domain(), extractErr)
		}
		if len(coverage.Failures) != 0 || coverage.UnresolvedCount != 0 {
			t.Fatalf("%s coverage = %+v", extractor.Domain(), coverage)
		}
		return coverage
	}
	run(protodecl.New())
	grpcCoverage := run(grpcgo.New())
	run(kafkago.NewProducer())
	run(kafkago.NewConsumer())
	diagnostic := func(name string) int64 {
		t.Helper()
		for _, counter := range grpcCoverage.Diagnostics {
			if counter.Name == name {
				return counter.Value
			}
		}
		t.Fatalf("grpc-consumer diagnostic %q is absent", name)
		return -1
	}
	if first, second := diagnostic("first_pass_reads"), diagnostic("second_pass_reads"); first != int64(profile.GeneratedMappings) || first+second != int64(combined.Profile.Overlay.GoFiles) ||
		diagnostic("ambiguous_calls") != 0 {
		t.Fatalf("grpc full-Go replay = first reads %d, second reads %d, ambiguous %d",
			first, second, diagnostic("ambiguous_calls"))
	}

	assertCount := func(domain, predicate string, want uint64) {
		t.Helper()
		if got := counts[domain+"\x00"+predicate]; got != want {
			t.Fatalf("%s/%s facts = %d, want %d", domain, predicate, got, want)
		}
	}
	assertCount("proto-contract", "DECLARES_MESSAGE", profile.ProtoMessages)
	assertCount("proto-contract", "DECLARES_SERVICE", profile.ProtoServices)
	assertCount("proto-contract", "DECLARES_OPERATION", profile.ProtoOperations)
	assertCount("grpc-consumer", "CALLS_OPERATION", profile.RPCCallPostings)
	assertCount("kafka-producer", "PRODUCES_TO_TOPIC", profile.KafkaProducerPostings)
	assertCount("kafka-consumer", "CONSUMES_FROM_TOPIC", profile.KafkaConsumerPostings)

	var generatedDescriptors, generatedBytes uint64
	generatedPaths := make(map[string]struct{}, len(mapping.Mappings))
	declarationPaths := make(map[string]struct{}, len(mapping.Mappings))
	for _, item := range mapping.Mappings {
		_, generatedDuplicate := generatedPaths[item.GeneratedPath]
		_, declarationDuplicate := declarationPaths[item.DeclarationPath]
		if item.Protocol != string(resolvermaterialize.ProtocolGRPC) || corpus.blobs[item.GeneratedPath].Content == "" ||
			corpus.blobs[item.DeclarationPath].Content == "" || operationsByPath[item.DeclarationPath] == 0 ||
			generatedDuplicate || declarationDuplicate {
			t.Fatalf("generated mapping does not join production inputs: %+v", item)
		}
		generatedPaths[item.GeneratedPath] = struct{}{}
		declarationPaths[item.DeclarationPath] = struct{}{}
		importPath := resolverinput.GoImportPath(map[string]string{".": combinedModulePath}, item.GeneratedPath)
		symbols, describeErr := gocaller.DescribeGeneratedSource(
			item.Protocol, item.GeneratedPath, importPath, corpus.blobs[item.GeneratedPath].Content,
		)
		if describeErr != nil {
			t.Fatalf("describe generated source %q: %v", item.GeneratedPath, describeErr)
		}
		for _, symbol := range symbols {
			if symbol.GeneratorRelativePath != item.GeneratorRelativePath {
				t.Fatalf("generated source %q names %q, mapping names %q",
					item.GeneratedPath, symbol.GeneratorRelativePath, item.GeneratorRelativePath)
			}
		}
		generatedDescriptors += uint64(len(symbols))
		generatedBytes += uint64(len(corpus.blobs[item.GeneratedPath].Content))
	}
	if uint64(len(mapping.Mappings)) > uint64(policy.MaxGeneratedMappings) ||
		profile.ProtoOperations > uint64(policy.MaxDeclarationRecords) ||
		generatedDescriptors != profile.GeneratedDescriptors {
		t.Fatalf("resolver budgets: mappings=%d/%d declarations=%d/%d descriptors=%d/%d",
			len(mapping.Mappings), policy.MaxGeneratedMappings,
			profile.ProtoOperations, policy.MaxDeclarationRecords,
			generatedDescriptors, profile.GeneratedDescriptors)
	}
	structural, err := frozenStructuralProfile()
	if err != nil {
		t.Fatal(err)
	}
	_, goMod, err := t401.FrozenCallerControlFixture(structural)
	if err != nil {
		t.Fatal(err)
	}
	resolverReads := uint64(2 + len(mapping.Mappings))
	resolverBytes := uint64(len(mappingBlob.Content)+len(goMod)) + generatedBytes
	if profile.ResolverFixedReadsPerBuild != 1 || profile.ResolverModuleReadsPerBuild != 1 ||
		profile.ResolverGeneratedReadsPerBuild != uint64(len(mapping.Mappings)) ||
		profile.ResolverBlobReadsPerBuild != resolverReads || profile.ResolverBlobBytesPerBuild != resolverBytes {
		t.Fatalf("resolver source reads = %d/%d bytes=%d/%d",
			resolverReads, profile.ResolverBlobReadsPerBuild, resolverBytes, profile.ResolverBlobBytesPerBuild)
	}
	projections := profile.RPCCallPostings + profile.KafkaProducerPostings + profile.KafkaConsumerPostings
	serviceReferences := 2*profile.RPCCallPostings + profile.KafkaProducerPostings + profile.KafkaConsumerPostings
	if projections != profile.RelationshipProjections || serviceReferences != profile.ServiceReferences {
		t.Fatalf("product relationship replay = projections %d, references %d", projections, serviceReferences)
	}

	for key, count := range counts {
		domain, predicate, ok := strings.Cut(key, "\x00")
		if !ok {
			t.Fatal("invalid replay counter key")
		}
		switch domain + "/" + predicate {
		case "proto-contract/DECLARES_MESSAGE", "proto-contract/DECLARES_SERVICE",
			"proto-contract/DECLARES_OPERATION", "grpc-consumer/CALLS_OPERATION",
			"kafka-producer/PRODUCES_TO_TOPIC", "kafka-consumer/CONSUMES_FROM_TOPIC":
		default:
			t.Fatalf("unexpected production fact %s/%s = %d", domain, predicate, count)
		}
	}
}

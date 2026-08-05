package sourceobservation_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

const sharedFixture = `package caller

import (
	"context"
	grpcapi "example.com/repo/grpc"
	thriftapi "example.com/repo/thrift"
	"github.com/IBM/sarama"
)

const auditTopic = "audit-v2"
var mutableTopic = "mutable"

func invoke(ctx context.Context, grpcClient grpcapi.GreeterClient, thriftClient thriftapi.AgentClient, producer sarama.SyncProducer, consumer sarama.Consumer) {
	const localTopic = "local-v1"
	constructed := grpcapi.NewGreeterClient()
	forwarded := constructed
	forwarded.SayHello()
	grpcClient.SayHello()
	thriftClient.EmitBatch()
	producer.SendMessage(&sarama.ProducerMessage{Topic: "orders-v1"})
	producer.SendMessage(&sarama.ProducerMessage{Topic: auditTopic})
	producer.SendMessage(&sarama.ProducerMessage{Topic: localTopic})
	producer.SendMessage(&sarama.ProducerMessage{Topic: mutableTopic})
	consumer.ConsumePartition("clicks", 0, sarama.OffsetNewest)
}
`

func TestSharedObservationDeterminismCitationsAndIndependentAdapters(t *testing.T) {
	first, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{
		Path: "src/caller.go", Content: sharedFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{
		Path: "other/path.go", Content: sharedFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := sourceobservation.Canonical(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := sourceobservation.Canonical(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("content-identical observations depend on placement path")
	}

	grpcDescriptors := []sourceobservation.MethodDescriptor{
		{Protocol: "grpc", ImportPath: "example.com/repo/grpc", ClientType: "GreeterClient", Method: "SayHello", Operation: "/demo.Greeter/SayHello", Constructors: []string{"NewGreeterClient"}},
		{Protocol: "grpc", ImportPath: "example.com/repo/grpc", ClientType: "GreeterClient", Method: "SayHello", Operation: "/other.Greeter/SayHello", Constructors: []string{"NewGreeterClient"}},
	}
	grpc, err := sourceobservation.AdaptCallers(first, "grpc", grpcDescriptors)
	if err != nil {
		t.Fatal(err)
	}
	thrift, err := sourceobservation.AdaptCallers(first, "thrift", []sourceobservation.MethodDescriptor{{
		Protocol: "thrift", ImportPath: "example.com/repo/thrift", ClientType: "AgentClient", Method: "EmitBatch", Operation: "demo.Agent.EmitBatch",
	}})
	if err != nil {
		t.Fatal(err)
	}
	producer, err := sourceobservation.AdaptKafka(first, "producer")
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := sourceobservation.AdaptKafka(first, "consumer")
	if err != nil {
		t.Fatal(err)
	}
	if len(grpc.Evidence) != 2 || grpc.Evidence[0].State != "ambiguous" || grpc.Evidence[1].State != "ambiguous" {
		t.Fatalf("gRPC evidence = %+v", grpc.Evidence)
	}
	if len(thrift.Evidence) != 1 || thrift.Evidence[0].State != "resolved" || thrift.Evidence[0].Operation != "demo.Agent.EmitBatch" {
		t.Fatalf("Thrift evidence = %+v", thrift.Evidence)
	}
	if len(producer.Evidence) != 4 || producer.Evidence[0].State != "resolved" ||
		producer.Evidence[1].State != "resolved" || producer.Evidence[2].State != "resolved" ||
		producer.Evidence[2].Binding != "same-file-const" || producer.Evidence[3].State != "dynamic" {
		t.Fatalf("producer evidence = %+v", producer.Evidence)
	}
	if len(consumer.Evidence) != 1 || consumer.Evidence[0].Topic != "clicks" {
		t.Fatalf("consumer evidence = %+v", consumer.Evidence)
	}
	for _, rows := range [][]sourceobservation.KafkaEvidence{producer.Evidence, consumer.Evidence} {
		for _, row := range rows {
			if got := sharedFixture[row.Span.StartByte:row.Span.EndByte]; got == "" {
				t.Fatal("empty Kafka citation")
			}
		}
	}
	if got := sharedFixture[thrift.Evidence[0].Span.StartByte:thrift.Evidence[0].Span.EndByte]; got != "thriftClient.EmitBatch" {
		t.Fatalf("Thrift citation = %q", got)
	}
}

func TestExistingGRPCAndThriftDirectCallerParity(t *testing.T) {
	observation, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{Path: "src/caller.go", Content: sharedFixture})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		protocol, importPath, clientType, method, operation, constructor string
	}{
		{"grpc", "example.com/repo/grpc", "GreeterClient", "SayHello", "/demo.Greeter/SayHello", "NewGreeterClient"},
		{"thrift", "example.com/repo/thrift", "AgentClient", "EmitBatch", "demo.Agent.EmitBatch", "NewAgentClient"},
	}
	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			modern, err := sourceobservation.AdaptCallers(observation, test.protocol, []sourceobservation.MethodDescriptor{{
				Protocol: test.protocol, ImportPath: test.importPath, ClientType: test.clientType,
				Method: test.method, Operation: test.operation, Constructors: []string{test.constructor},
			}})
			if err != nil {
				t.Fatal(err)
			}
			descriptor := gocaller.DirectDescriptor{
				State: "resolved", Protocol: test.protocol, ImportPath: test.importPath,
				Package:    strings.TrimSuffix(strings.TrimPrefix(test.importPath, "example.com/repo/"), "/"),
				ClientType: test.clientType, Method: test.method, Operation: test.operation,
				Constructors: []string{test.constructor}, GeneratedPath: "gen/generated.go",
				GeneratedObjectID: strings.Repeat("1", 40), GeneratedDigest: "sha256:" + strings.Repeat("1", 64),
				GeneratorRelativePath: "api.proto", DeclarationPath: "idl/api.proto", DeclarationLineage: "lineage-a",
			}
			resolver, err := gocaller.NewDirectResolver(t.Context(), test.protocol, []gocaller.DirectDescriptor{descriptor})
			if err != nil {
				t.Fatal(err)
			}
			var legacy []sdk.Fact
			_, err = gocaller.ScanDirectSyntaxFile(t.Context(), gocaller.DirectScanRequest{
				Protocol: test.protocol, Path: "src/caller.go", Blob: sdk.Blob{Content: sharedFixture, Digest: "sha256:source"}, Resolver: resolver,
			}, func(value sdk.Fact) error { legacy = append(legacy, value); return nil })
			if err != nil {
				t.Fatal(err)
			}
			if len(legacy) != len(modern.Evidence) {
				t.Fatalf("legacy %d != observation %d: legacy=%+v modern=%+v", len(legacy), len(modern.Evidence), legacy, modern.Evidence)
			}
			for i := range legacy {
				if legacy[i].Assertion.Object != modern.Evidence[i].Operation ||
					legacy[i].Atom.StartByte != modern.Evidence[i].Span.StartByte || legacy[i].Atom.EndByte != modern.Evidence[i].Span.EndByte {
					t.Fatalf("parity row %d: legacy=%+v modern=%+v", i, legacy[i], modern.Evidence[i])
				}
			}
		})
	}
}

type memoryCorpus map[string]string

func (memoryCorpus) RepoName() string { return "example.com/repo" }
func (memoryCorpus) Commit() string   { return "c0ffee" }
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
	return sdk.Blob{Content: m[path], Digest: "sha256:source"}, nil
}

func TestExistingKafkaPlaneParity(t *testing.T) {
	observation, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{Path: "src/caller.go", Content: sharedFixture})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		plane string
		old   sdk.Extractor
	}{
		{"producer", kafkago.NewProducer()},
		{"consumer", kafkago.NewConsumer()},
	} {
		t.Run(test.plane, func(t *testing.T) {
			modern, err := sourceobservation.AdaptKafka(observation, test.plane)
			if err != nil {
				t.Fatal(err)
			}
			var legacy []sdk.Fact
			_, err = test.old.Extract(t.Context(), memoryCorpus{"src/caller.go": sharedFixture}, func(value sdk.Fact) error {
				legacy = append(legacy, value)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(legacy) != len(modern.Evidence) {
				t.Fatalf("legacy %d != observation %d: legacy=%+v modern=%+v", len(legacy), len(modern.Evidence), legacy, modern.Evidence)
			}
			for i := range legacy {
				var object string
				if modern.Evidence[i].State == "resolved" {
					object = "topic:" + modern.Evidence[i].Topic
				} else {
					object = "unresolved:" + modern.Evidence[i].Reason
				}
				if legacy[i].Assertion.Object != object || legacy[i].Atom.StartByte != modern.Evidence[i].Span.StartByte ||
					legacy[i].Atom.EndByte != modern.Evidence[i].Span.EndByte {
					t.Fatalf("parity row %d: legacy=%+v modern=%+v", i, legacy[i], modern.Evidence[i])
				}
			}
		})
	}
}

func TestBoundsAndClosedValidation(t *testing.T) {
	_, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{Path: "big.go", Content: strings.Repeat("x", sourceobservation.MaxSourceBytes+1)})
	if !errors.Is(err, sourceobservation.ErrLimit) {
		t.Fatalf("oversized error = %v", err)
	}
	value, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{Path: "ok.go", Content: "package p\nfunc f(x any) { x.M() }\n"})
	if err != nil {
		t.Fatal(err)
	}
	value.TopicSites = append(value.TopicSites, sourceobservation.TopicSite{Plane: "producer", State: "resolved"})
	if _, err := sourceobservation.Canonical(value); err == nil {
		t.Fatal("invalid appended topic site passed closed validation")
	}

	var calls strings.Builder
	calls.WriteString("package p\nfunc f(x any) {\n")
	for range sourceobservation.MaxCalls + 1 {
		calls.WriteString("x.M()\n")
	}
	calls.WriteString("}\n")
	if _, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{Path: "calls.go", Content: calls.String()}); !errors.Is(err, sourceobservation.ErrLimit) {
		t.Fatalf("call limit error = %v", err)
	}

	var propagation strings.Builder
	propagation.WriteString("package p\nfunc f(x any) {\n")
	for range sourceobservation.MaxPropagationSteps + 1 {
		propagation.WriteString("x = x\n")
	}
	propagation.WriteString("x.M()\n}\n")
	limited, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{Path: "propagation.go", Content: propagation.String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Functions) != 1 || len(limited.Functions[0].Gaps) != 1 ||
		limited.Functions[0].Gaps[0].Reason != "propagation_limit" ||
		limited.Functions[0].Calls[0].Receiver != "unsupported" {
		t.Fatalf("propagation limit classification = %+v", limited.Functions)
	}
}

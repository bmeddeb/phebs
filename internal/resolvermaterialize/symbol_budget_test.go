package resolvermaterialize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
)

func TestGeneratedSymbolProjectionPolicyAndBoundaries(t *testing.T) {
	policy := FrozenMaterializationPolicy()
	if policy.MaxGeneratedSymbolSources != 25_000 ||
		policy.MaxGeneratedSymbolDescriptors != 100_000 ||
		policy.MaxGeneratedSymbolIdentityBytes != 32<<20 ||
		policy.MaxGeneratedSymbolSourceBytes != 4<<20 ||
		policy.GeneratedSymbolIdentityAccounting !=
			"catalog-wide-unique-source-plus-descriptor-v1" {
		t.Fatalf("generated-symbol policy = %+v", policy)
	}
	record := generatedSymbolRecord{
		Schema: resolvercatalog.RecordSchema, RecordSchema: GeneratedSymbolRecordSchema,
		Pack: GRPCGeneratedPackName, PackVersion: PackVersion,
		Kind: "generated_symbol", State: StateResolved, Protocol: ProtocolGRPC,
		ImportPath: "example.com/api", Package: "api", ClientType: "APIClient",
		Method: "Get", Operation: "/api.API/Get", Constructors: []string{"NewAPIClient"},
		GeneratedPath:         "gen/api_grpc.pb.go",
		GeneratedObjectID:     "1111111111111111111111111111111111111111",
		GeneratedDigest:       "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		GeneratorRelativePath: "api.proto", DeclarationPath: "idl/api.proto",
		DeclarationLineage: "lineage-api",
	}
	write := func(json.RawMessage) error { return nil }

	countBudget := &materializationBudget{
		generatedSymbolDescriptors: MaxGeneratedSymbolDescriptors - 1,
	}
	if err := writeGeneratedSymbol(write, countBudget, record); err != nil {
		t.Fatalf("descriptor cap = %v", err)
	}
	second := record
	second.Method = "List"
	second.Operation = "/api.API/List"
	if err := writeGeneratedSymbol(write, countBudget, second); !errors.Is(err, resolvercatalog.ErrLimit) {
		t.Fatalf("descriptor cap+1 = %v, want ErrLimit", err)
	}

	probe := &materializationBudget{}
	if err := writeGeneratedSymbol(write, probe, record); err != nil {
		t.Fatal(err)
	}
	exactBytes := &materializationBudget{
		generatedSymbolIdentityBytes: MaxGeneratedSymbolIdentityBytes -
			probe.generatedSymbolIdentityBytes,
	}
	if err := writeGeneratedSymbol(write, exactBytes, record); err != nil {
		t.Fatalf("identity-byte cap = %v", err)
	}
	overBytes := &materializationBudget{
		generatedSymbolIdentityBytes: MaxGeneratedSymbolIdentityBytes -
			probe.generatedSymbolIdentityBytes + 1,
	}
	if err := writeGeneratedSymbol(write, overBytes, record); !errors.Is(err, resolvercatalog.ErrLimit) {
		t.Fatalf("identity-byte cap+1 = %v, want ErrLimit", err)
	}
}

func TestGeneratedSymbolSourceCapRefusesBeforeBlobRead(t *testing.T) {
	sources := make(map[string]string, MaxGeneratedSymbolSources)
	for index := range MaxGeneratedSymbolSources {
		sources[fmt.Sprintf("gen/%05d.go", index)] =
			"1111111111111111111111111111111111111111"
	}
	reads := 0
	file := extract.CandidateManifestFile{
		Path: "gen/cap.go", ObjectID: "2222222222222222222222222222222222222222",
		Mode: "100644", SourceLane: "base", DeclaredBytes: 1,
	}
	plan := &generatedPlan{
		generatedFiles: map[string]extract.CandidateManifestFile{file.Path: file},
		blobs: &blobBudget{read: func(
			context.Context, string, string, int64,
		) ([]byte, error) {
			reads++
			return []byte{'x'}, nil
		}},
	}
	err := emitGeneratedSymbols(
		context.Background(), adapter{protocol: ProtocolGRPC}, plan,
		mappingKey{generated: file.Path}, StateUnavailable, "missing", nil,
		&materializationBudget{generatedSourceObjects: sources},
		func(json.RawMessage) error { return nil },
	)
	if !errors.Is(err, resolvercatalog.ErrLimit) || reads != 0 {
		t.Fatalf("source cap+1 error=%v reads=%d", err, reads)
	}
}

func TestGeneratedProjectionIdentityChargesUniqueSourcesAndDescriptorsTogether(t *testing.T) {
	write := func(json.RawMessage) error { return nil }
	input := generatedSymbolRecord{
		Schema: resolvercatalog.RecordSchema, RecordSchema: GeneratedSymbolRecordSchema,
		Pack: GRPCGeneratedPackName, PackVersion: PackVersion,
		Kind: "generated_symbol_input", State: StateUnsupported,
		Reason: "generated_source_unparseable", Protocol: ProtocolGRPC,
		GeneratedPath:     "gen/input.go",
		GeneratedObjectID: "1111111111111111111111111111111111111111",
		GeneratedDigest:   "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	}
	sourceBytes := len(input.GeneratedPath) + len(input.GeneratedObjectID)
	exact := &materializationBudget{
		generatedSymbolIdentityBytes: MaxGeneratedSymbolIdentityBytes - sourceBytes,
	}
	if err := writeGeneratedSymbol(write, exact, input); err != nil {
		t.Fatalf("source identity exact cap = %v", err)
	}
	over := &materializationBudget{
		generatedSymbolIdentityBytes: MaxGeneratedSymbolIdentityBytes - sourceBytes + 1,
	}
	if err := writeGeneratedSymbol(write, over, input); !errors.Is(err, resolvercatalog.ErrLimit) {
		t.Fatalf("source identity cap+1 = %v, want ErrLimit", err)
	}

	shared := &materializationBudget{}
	if err := writeGeneratedSymbol(write, shared, input); err != nil {
		t.Fatal(err)
	}
	charged := shared.generatedSymbolIdentityBytes
	if err := writeGeneratedSymbol(write, shared, input); err != nil {
		t.Fatal(err)
	}
	if shared.generatedSymbolIdentityBytes != charged ||
		len(shared.generatedSourceObjects) != 1 {
		t.Fatalf("shared source charge = bytes:%d->%d sources:%d",
			charged, shared.generatedSymbolIdentityBytes, len(shared.generatedSourceObjects))
	}
}

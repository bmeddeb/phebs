package t421

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestEpochFinalOptionalWireOrder(t *testing.T) {
	native, err := parser.ParseFile(token.NewFileSet(), "../../cmd/phebs/t421_final_authority.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	ast.Inspect(native, func(node ast.Node) bool {
		typ, ok := node.(*ast.TypeSpec)
		if !ok || typ.Name.Name != "t421FinalAuthorityResponse" {
			return true
		}
		for _, field := range typ.Type.(*ast.StructType).Fields.List {
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, reflect.StructTag(tag).Get("json"))
		}
		return false
	})
	var got []string
	client := reflect.TypeFor[epochFinalResponse]()
	for i := range client.NumField() {
		got = append(got, client.Field(i).Tag.Get("json"))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("F field order/tags = %v, production = %v", got, want)
	}
}

func TestEpochFinalBodyIncludesAllNativeObservations(t *testing.T) {
	reader, wire := epochTestFinal(t)
	wire.CatalogPopulation = &ExecutionCatalogPopulation{AcceptedServices: 1}
	wire.CallerPublication = &ExecutionCallerPublicationObservation{
		RelationshipRootReads: 1, RelationshipGenerationReads: 1,
		GenerationSHA256: wire.Authority.CallerGenerationSHA256, ManifestSHA256: wire.Authority.CallerRootSHA256,
		Leaves: []callerpublication.LeafObservation{}, RPCProjection: SetIdentity{Records: 1, FramedBytes: 1, SHA256: testDigest("rpc")},
	}
	wire.ResolverCatalogCounts = &readaccounting.ResolverCatalogCounts{
		GenerationSHA256: wire.Authority.ResolverCatalogGenerationSHA256, ManifestSHA256: wire.Authority.ResolverCatalogRootSHA256,
		DeclarationRecords: 5, GeneratedDescriptors: 7,
	}
	wire.RPCPostings = &ExecutionRPCPostingObservation{Resolved: 2, NameMatch: 1, Unresolved: 3}
	authority, projection, err := reader.decodeFinal(epochTestJSON(t, wire, true))
	if err != nil {
		t.Fatal(err)
	}
	final := ExecutionInspectionFinal{CatalogPopulation: reader.finalCatalogPopulation, CallerPublication: reader.finalCallerPublication,
		ResolverCatalogCounts: reader.finalResolverCatalogCounts, RPCPostings: reader.finalRPCPostings}
	for _, phase := range []string{"archive", "product_queries"} {
		t.Run(phase, func(t *testing.T) {
			wire := wire
			if phase == "product_queries" {
				wire.QueryAuthority = &epochQueryAuthority{CatalogSourceGenerationSHA256: testDigest("catalog-source"),
					ResolverNamespaceGenerationSHA256: testDigest("namespace-generation"), ResolverNamespaceRootSHA256: testDigest("namespace-root")}
			}
			raw, err := epochFinalBody(authority.AuthorityState, authority.ExtractionRoots, projection, final, wire.QueryAuthority)
			if err != nil || !bytes.Equal(raw, epochTestJSON(t, wire, true)) {
				t.Fatalf("F rebuild differs from complete native wire body: %v", err)
			}
		})
	}
}

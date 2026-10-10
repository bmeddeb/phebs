package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
)

// TestCallerMapPackIdentityAndRecipeFrozen pins the exact T47.2a frozen tuple:
// the stable pack identity `phebs.grpc.caller.go` (docs/PROTO_GRPC_PACK_CARDS.md,
// "Exact Go gRPC callers") and the fixed extractor set that implements it. The
// declaration extractor travels with the caller extractor on purpose, so
// resolved callers keep their repository-committed declaration lineage without
// depending on the experimental protobuf umbrella.
func TestCallerMapPackIdentityAndRecipeFrozen(t *testing.T) {
	if callerMapPackID != "phebs.grpc.caller.go" {
		t.Fatalf("caller map pack id = %q, want phebs.grpc.caller.go", callerMapPackID)
	}
	recipe := callerMapRecipe()
	want := []struct{ domain, version string }{
		{"proto-contract", "3.0.0"},
		{"grpc-caller", "1.5.0"},
	}
	if len(recipe) != len(want) {
		t.Fatalf("caller map recipe has %d extractors, want %d", len(recipe), len(want))
	}
	for index, expected := range want {
		if recipe[index].Domain() != expected.domain || recipe[index].Version() != expected.version {
			t.Fatalf("caller map recipe[%d] = %s@%s, want %s@%s",
				index, recipe[index].Domain(), recipe[index].Version(),
				expected.domain, expected.version)
		}
	}
}

// TestCallerMapRecipeStaysUnboundUntilReleased pins the T47.5 handoff: the
// recipe is deliberately absent from packRecipes until the same PR that records
// its first passing signed released record binds it. A verified released record
// for the caller-map pack must therefore still refuse startup as an unbound
// release, so Caller Map cannot reach ordinary activation through a record
// alone.
func TestCallerMapRecipeStaysUnboundUntilReleased(t *testing.T) {
	if _, bound := packRecipes[callerMapPackID]; bound {
		t.Fatal("callerMapRecipe must not be bound before its first released record (T47.5 owns the binding)")
	}
	dir := t.TempDir()
	public, release := writeReleasedRecord(t, dir, callerMapPackID, "key-1")
	bindReleaseLoad(t, release)
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
	_, err := releasedExtractors(context.Background(), cfg)
	if err == nil {
		t.Fatal("a verified released caller-map record without its in-tree binding must refuse startup")
	}
	if !strings.Contains(err.Error(), "has no fixed in-tree recipe") ||
		!strings.Contains(err.Error(), callerMapPackID) {
		t.Fatalf("unbound release refusal = %v, want the missing in-tree recipe cause for %q",
			err, callerMapPackID)
	}
}

// TestCallerMapRegistrationFollowsExtractorSet pins the T47.3 registration
// semantics at the exact registry boundary: the Caller Map surface is enabled
// by whichever admission supplied a caller-adapter domain — the released
// recipe or the experimental-dark switches — and never by evidence that cannot
// produce a caller pair. Thrift-field- or Kafka-only admissions must stay
// visibly unavailable.
func TestCallerMapRegistrationFollowsExtractorSet(t *testing.T) {
	released, err := callerexecute.NewRegistry(mergeExtractors(nil, callerMapRecipe()))
	if err != nil {
		t.Fatalf("released registry: %v", err)
	}
	if !released.Enabled() {
		t.Fatal("a released caller-map recipe must enable the Caller Map surface")
	}
	adapters := released.Adapters()
	if len(adapters) != 1 || adapters[0] != (callerexecute.Adapter{
		Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc",
	}) {
		t.Fatalf("released adapters = %+v, want grpc-caller@1.5.0/grpc", adapters)
	}
	domains := released.CandidateDomains()
	if len(domains) != 2 ||
		domains[0].Domain != "grpc-caller" || domains[0].Version != "1.5.0" ||
		domains[1].Domain != "proto-contract" || domains[1].Version != "3.0.0" {
		t.Fatalf("released candidate domains = %+v, want grpc-caller@1.5.0 then proto-contract@3.0.0", domains)
	}

	for _, testCase := range []struct {
		name       string
		proto      bool
		thrift     bool
		thriftFld  bool
		kafka      bool
		wantEnable bool
	}{
		{name: "proto umbrella only", proto: true, wantEnable: true},
		{name: "thrift umbrella only", thrift: true, wantEnable: true},
		{name: "thrift-field only", thriftFld: true, wantEnable: false},
		{name: "kafka only", kafka: true, wantEnable: false},
		{name: "thrift-field and kafka", thriftFld: true, kafka: true, wantEnable: false},
		{name: "all off", wantEnable: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			registry, err := callerexecute.NewRegistry(mergeExtractors(
				evidenceExtractors(testCase.proto, testCase.thrift, testCase.thriftFld, testCase.kafka),
				nil,
			))
			if err != nil {
				t.Fatalf("dark registry: %v", err)
			}
			if registry.Enabled() != testCase.wantEnable {
				t.Fatalf("enabled = %v, want %v (adapters %+v)",
					registry.Enabled(), testCase.wantEnable, registry.Adapters())
			}
		})
	}
}

// releasedRecipeMark wraps a recipe extractor so a test can tell the released
// instance from the dark one: callerMapRecipe deliberately reuses the dark
// constructors, so both report the identical domain@version.
type releasedRecipeMark struct{ extract.Extractor }

// TestMergeExtractorsCallerMapRecipeGovernsMixedProvenance proves the
// mixed-provenance answer for the T48.4b finding: when the experimental
// protobuf umbrella and the released caller-map recipe both name the
// declaration and caller domains, the released instances govern both domains
// and the merged set carries exactly one extractor per domain, so the caller
// registry holds a single adapter. The plane is still mixed for every other
// domain the umbrella admits: grpc-consumer and scip-proto-field stay
// experimental-dark beside the released pair.
func TestMergeExtractorsCallerMapRecipeGovernsMixedProvenance(t *testing.T) {
	recipe := callerMapRecipe()
	marked := make([]extract.Extractor, 0, len(recipe))
	for _, extractor := range recipe {
		marked = append(marked, releasedRecipeMark{extractor})
	}
	merged := mergeExtractors(evidenceExtractors(true, false, false, false), marked)
	counts := map[string]int{}
	released := map[string]bool{}
	for _, extractor := range merged {
		counts[extractor.Domain()]++
		_, isReleased := extractor.(releasedRecipeMark)
		released[extractor.Domain()] = isReleased
	}
	for _, test := range []struct {
		domain   string
		released bool
	}{
		{"proto-contract", true},
		{"grpc-caller", true},
		{"grpc-consumer", false},
		{"scip-proto-field", false},
	} {
		if counts[test.domain] != 1 || released[test.domain] != test.released {
			t.Fatalf("%s: count %d released %v, want exactly one with released %v (merged %v)",
				test.domain, counts[test.domain], released[test.domain], test.released, merged)
		}
	}
	if len(merged) != len(counts) {
		t.Fatalf("merged set has %d extractors over %d domains, want one per domain", len(merged), len(counts))
	}
	registry, err := callerexecute.NewRegistry(merged)
	if err != nil {
		t.Fatalf("mixed registry: %v", err)
	}
	adapters := registry.Adapters()
	if len(adapters) != 1 || adapters[0] != (callerexecute.Adapter{
		Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc",
	}) {
		t.Fatalf("mixed adapters = %+v, want exactly grpc-caller@1.5.0/grpc", adapters)
	}
}

// servedCallerSurfaces is what one admitted startup serves: the sorted
// /api/version capabilities and whether MCP lists the Caller Map, comparison
// and proof tools.
type servedCallerSurfaces struct {
	capabilities      []string
	callerMapTool     bool
	comparisonTool    bool
	proofTool         bool
	callerMapEnabled  bool
	callerMapServiced bool
}

// serveCallerSurfaces drives one startup through the production assembly and
// reports what it serves: newServeExtractionRegistries admits the extractor
// set, bindServeCallerReader binds the caller publication reader,
// newServeAPIOptions assembles the API options, api.New serves /api/version,
// and newServeMCPServer builds the MCP tool registry from those same options.
// The store is never opened: a nil *store.Surreal stands in for it, so every
// presence check sees a store and nothing on this path reads it. Two
// stand-ins remain, each named where it is set.
func serveCallerSurfaces(t *testing.T, cfg *config.Config) servedCallerSurfaces {
	t.Helper()
	t.Setenv("PHEBS_CONTRACT_ATLAS_FIXTURE", "")
	deps := &serveDeps{ctx: context.Background(), cfg: cfg}
	if err := newServeExtractionRegistries(deps); err != nil {
		t.Fatalf("newServeExtractionRegistries: %v", err)
	}
	if err := bindServeCallerReader(deps); err != nil {
		t.Fatalf("bindServeCallerReader: %v", err)
	}
	// startServeExtractionPipeline installs the store as both the evidence
	// view and the proof-bundle store whenever any extractor is admitted; it
	// also starts workers, so it is not run here.
	if len(deps.exs) > 0 {
		deps.evidenceView, deps.proofBundles = deps.st, deps.st
	}
	apiOpts, err := newServeAPIOptions(deps)
	if err != nil {
		t.Fatalf("newServeAPIOptions: %v", err)
	}
	served := servedCallerSurfaces{
		callerMapEnabled:  apiOpts.CallerMapEnabled,
		callerMapServiced: apiOpts.CallerMap != nil,
	}

	// /api/version lists capabilities only to an authenticated caller, whose
	// principal only the auth middleware can establish; that caller is stood
	// in for. No service constructor reads the principal's value.
	versionOpts := apiOpts
	versionOpts.Principal = func(context.Context) string { return "user:t47.3" }
	recorder := httptest.NewRecorder()
	api.New(versionOpts).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/version = %d: %s", recorder.Code, recorder.Body)
	}
	var version struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &version); err != nil {
		t.Fatalf("decode /api/version: %v", err)
	}
	served.capabilities = append([]string{}, version.Capabilities...)
	slices.Sort(served.capabilities)

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	server := newServeMCPServer(deps, apiOpts)
	go func() {
		_, _ = server.Connect(t.Context(), serverTransport, nil)
	}()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t47.3-surfaces", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	for _, tool := range listed.Tools {
		switch tool.Name {
		case "list_operation_callers":
			served.callerMapTool = true
		case "compare_operation_callers":
			served.comparisonTool = true
		case "find_operation_consumers":
			served.proofTool = true
		}
	}
	return served
}

// TestCallerMapOrdinarySurfaceFollowsProductionAssembly pins what an ordinary
// startup serves, through the production assembly, for the experimental-dark
// baselines and for the released Caller Map recipe. The recipe is bound only
// inside the released cases, the way T47.5 will bind it. A released Caller
// Map alone serves Caller Map and the Contract Atlas discovery it is selected
// through. Proof bundles, Contract Impact, Thrift-field references and Kafka
// topic usage (the proof service), and caller comparison, stay admitted only by
// the experimental switches.
func TestCallerMapOrdinarySurfaceFollowsProductionAssembly(t *testing.T) {
	everyProduct := []string{
		"contract-atlas", "contract-caller-comparison", "contract-caller-map",
		"contract-impact-report", "kafka-topic-usage", "service-catalog-v2",
	}
	tests := []struct {
		name      string
		released  bool
		configure func(*config.Config)
		want      servedCallerSurfaces
	}{
		{
			name:      "every switch off and no selection",
			configure: func(*config.Config) {},
			want:      servedCallerSurfaces{capabilities: []string{"service-catalog-v2"}},
		},
		{
			name:      "the proto switch alone keeps its experimental surface",
			configure: func(cfg *config.Config) { cfg.Experimental.ProvisionalProtoExtraction = true },
			want: servedCallerSurfaces{
				capabilities:  everyProduct,
				callerMapTool: true, comparisonTool: true, proofTool: true,
				callerMapEnabled: true, callerMapServiced: true,
			},
		},
		{
			name:      "the kafka switch alone serves no Caller Map",
			configure: func(cfg *config.Config) { cfg.Experimental.ProvisionalKafkaExtraction = true },
			want: servedCallerSurfaces{
				capabilities: []string{
					"contract-atlas", "contract-impact-report", "kafka-topic-usage", "service-catalog-v2",
				},
				proofTool: true,
			},
		},
		{
			name:      "a released Caller Map alone serves only Caller Map and its discovery",
			released:  true,
			configure: func(*config.Config) {},
			want: servedCallerSurfaces{
				capabilities:  []string{"contract-atlas", "contract-caller-map", "service-catalog-v2"},
				callerMapTool: true, callerMapEnabled: true, callerMapServiced: true,
			},
		},
		{
			// Comparison follows the experimental switches, not the protocol of
			// the caller adapters it reads; Epic 50 owns narrowing it.
			name:      "a released Caller Map beside the thrift switch",
			released:  true,
			configure: func(cfg *config.Config) { cfg.Experimental.ProvisionalThriftExtraction = true },
			want: servedCallerSurfaces{
				capabilities:  everyProduct,
				callerMapTool: true, comparisonTool: true, proofTool: true,
				callerMapEnabled: true, callerMapServiced: true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Server.DataDir = t.TempDir()
			test.configure(cfg)
			if test.released {
				restore := packRecipes
				packRecipes = map[string]func() []extract.Extractor{callerMapPackID: callerMapRecipe}
				t.Cleanup(func() { packRecipes = restore })
				dir := t.TempDir()
				public, release := writeReleasedRecord(t, dir, callerMapPackID, "key-1")
				bindReleaseLoad(t, release)
				cfg.ReleaseSelection.Path = dir
				cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
			}
			got := serveCallerSurfaces(t, cfg)
			if !slices.Equal(got.capabilities, test.want.capabilities) {
				t.Fatalf("/api/version capabilities = %v, want %v", got.capabilities, test.want.capabilities)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("served = %+v, want %+v", got, test.want)
			}
		})
	}
}

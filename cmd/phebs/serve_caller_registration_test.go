package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/packrelease"
	"github.com/bmeddeb/phebs/spike/t201"
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
		{"proto-contract", "4.0.0"},
		{"grpc-caller", "1.6.0-released.1"},
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

// TestCallerMapReleasedRecipeAdmission drives the actual signed selection and
// in-tree recipe, without replacing the recipe registry or enabling switches.
func TestCallerMapReleasedRecipeAdmission(t *testing.T) {
	if _, bound := packRecipes[callerMapPackID]; !bound {
		t.Fatal("released Caller Map recipe is not bound")
	}
	dir := t.TempDir()
	public, release := writeReleasedRecord(t, dir, callerMapPackID, "key-1")
	bindReleaseLoad(t, release)
	cfg := &config.Config{}
	cfg.Server.DataDir = t.TempDir()
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
	extractors, claims, err := releasedExtractors(t.Context(), cfg)
	if err != nil || len(extractors) != 2 || len(claims) != 0 {
		t.Fatalf("released Caller Map = %v, %v, %v", extractors, claims, err)
	}
	if extractors[1].Domain() != "grpc-caller" || extractors[1].Version() != "1.6.0-released.1" {
		t.Fatalf("released caller identity = %s@%s", extractors[1].Domain(), extractors[1].Version())
	}
	served := serveCallerSurfaces(t, cfg)
	if !served.callerMapEnabled || !served.callerMapServiced || !served.callerMapTool {
		t.Fatalf("released Caller Map surface = %+v", served)
	}
	for _, capability := range []string{"contract-caller-comparison", "contract-impact-report", "kafka-topic-usage"} {
		if slices.Contains(served.capabilities, capability) {
			t.Fatalf("Caller Map release also enabled %s", capability)
		}
	}
	cfg.ReleaseSelection.Revoked = []string{release.ReleaseID}
	withdrawn := serveCallerSurfaces(t, cfg)
	if withdrawn.callerMapEnabled || withdrawn.callerMapServiced || withdrawn.callerMapTool {
		t.Fatalf("revoked Caller Map still serves: %+v", withdrawn)
	}
}

func TestCallerMapReleasedAdmissionKeepsFactsAndChangesAuthority(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(profile.Files))
	for path, content := range profile.Files {
		files[path] = string(content)
	}
	released := callerMapRecipe()
	dark := []extract.Extractor{released[0], gocaller.NewGRPC()}
	if strings.TrimSuffix(released[1].Version(), "-released.1") != dark[1].Version() {
		t.Fatal("released admission no longer names its underlying algorithm version")
	}
	run := func(extractor extract.Extractor) []sdk.Fact {
		var facts []sdk.Fact
		coverage, extractErr := extractor.Extract(t.Context(), releasedCallerTestCorpus{newT307Corpus(files)}, func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		})
		if extractErr != nil || len(coverage.Failures) != 0 {
			t.Fatalf("caller extraction: %v, failures %v", extractErr, coverage.Failures)
		}
		return facts
	}
	want := run(dark[1])
	if len(want) == 0 || !reflect.DeepEqual(run(released[1]), want) {
		t.Fatal("released admission changed or omitted caller facts")
	}
	var previousPolicy, previousGeneration, previousExtractors string
	for _, extractors := range [][]extract.Extractor{dark, released} {
		policies, policyErr := extract.CandidatePolicies(extractors)
		if policyErr != nil {
			t.Fatal(policyErr)
		}
		identities, identityErr := candidate.PolicyIdentities(policies)
		if identityErr != nil {
			t.Fatal(identityErr)
		}
		policy, digestErr := candidate.PolicyDigest(identities)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		registry, registryErr := callerexecute.NewRegistry(extractors)
		if registryErr != nil {
			t.Fatal(registryErr)
		}
		// Hold upstream authority equal to prove the caller generation itself
		// fences admission, even before a new candidate has been published.
		generation, generationErr := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
			Repository: "example.invalid/callers", HeadCommit: strings.Repeat("a", 40),
			DeclarationSetDigest:    testReleaseDigest("declarations"),
			CandidateManifestDigest: testReleaseDigest("candidate"), CandidatePolicyDigest: testReleaseDigest("policy"),
			ResolverGenerationDigest: testReleaseDigest("resolver"), ResolverManifestDigest: testReleaseDigest("root"),
			Extractors: registry.ExtractorIdentities(),
		})
		if generationErr != nil {
			t.Fatal(generationErr)
		}
		if policy == previousPolicy || generation.Digest == previousGeneration || generation.ExtractorSetDigest == previousExtractors {
			t.Fatal("released admission reused experimental candidate or caller authority")
		}
		previousPolicy, previousGeneration, previousExtractors = policy, generation.Digest, generation.ExtractorSetDigest
	}
}

type releasedCallerTestCorpus struct{ t307Corpus }

func (corpus releasedCallerTestCorpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	blob, err := corpus.Read(ctx, "index.scip")
	return sdk.SCIPInput{Path: "index.scip", Blob: blob, Present: err == nil}, err
}

// TestKafkaProducerRecipeIsProducerOnlyAndBound pins T55.2/T55.3: the recipe is
// the producer plane alone, it is bound, and a verified released record admits
// exactly the kafka-producer extractor.
func TestKafkaProducerRecipeIsProducerOnlyAndBound(t *testing.T) {
	recipe := kafkaProducerRecipe()
	if len(recipe) != 1 || recipe[0].Domain() != "kafka-producer" {
		t.Fatalf("producer recipe = %v, want the kafka-producer extractor alone", recipe)
	}
	if _, bound := packRecipes[kafkaProducerPackID]; !bound {
		t.Fatal("kafkaProducerRecipe must be bound now that T55.3 records its released record")
	}
	dir := t.TempDir()
	public, release := writeReleasedRecord(t, dir, kafkaProducerPackID, "key-1")
	bindReleaseLoad(t, release)
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
	extractors, _, err := releasedExtractors(context.Background(), cfg)
	if err != nil || len(extractors) != 1 || extractors[0].Domain() != "kafka-producer" {
		t.Fatalf("released producer = %v, %v; want the kafka-producer extractor alone", extractors, err)
	}
}

// TestKafkaConsumerRecipeIsConsumerOnlyAndBound pins T56.2/T56.3: the recipe is
// the consumer plane alone, it is bound, and a verified released record admits
// exactly the kafka-consumer extractor.
func TestKafkaConsumerRecipeIsConsumerOnlyAndBound(t *testing.T) {
	recipe := kafkaConsumerRecipe()
	if len(recipe) != 1 || recipe[0].Domain() != "kafka-consumer" {
		t.Fatalf("consumer recipe = %v, want the kafka-consumer extractor alone", recipe)
	}
	if _, bound := packRecipes[kafkaConsumerPackID]; !bound {
		t.Fatal("kafkaConsumerRecipe must be bound now that T56.3 records its released record")
	}
	dir := t.TempDir()
	public, release := writeReleasedRecord(t, dir, kafkaConsumerPackID, "key-1")
	bindReleaseLoad(t, release)
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
	extractors, _, err := releasedExtractors(context.Background(), cfg)
	if err != nil || len(extractors) != 1 || extractors[0].Domain() != "kafka-consumer" {
		t.Fatalf("released consumer = %v, %v; want the kafka-consumer extractor alone", extractors, err)
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
		Domain: "grpc-caller", Version: "1.6.0-released.1", Protocol: "grpc",
	}) {
		t.Fatalf("released adapters = %+v, want grpc-caller@1.6.0-released.1/grpc", adapters)
	}
	domains := released.CandidateDomains()
	if len(domains) != 2 ||
		domains[0].Domain != "grpc-caller" || domains[0].Version != "1.6.0-released.1" ||
		domains[1].Domain != "proto-contract" || domains[1].Version != "4.0.0" {
		t.Fatalf("released candidate domains = %+v, want grpc-caller@1.6.0-released.1 then proto-contract@4.0.0", domains)
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
// instance from the dark one, including the shared declaration extractor.
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
		Domain: "grpc-caller", Version: "1.6.0-released.1", Protocol: "grpc",
	}) {
		t.Fatalf("mixed adapters = %+v, want exactly grpc-caller@1.6.0-released.1/grpc", adapters)
	}
}

// servedCallerSurfaces is what one admitted startup serves: the sorted
// /api/version capabilities and whether MCP lists the Caller Map, comparison
// and proof tools.
type servedCallerSurfaces struct {
	capabilities       []string
	callerMapTool      bool
	comparisonTool     bool
	comparisonReleased bool
	proofTool          bool
	topicTool          bool
	wireTool           bool
	listTopicsTool     bool
	readInventoryTool  bool
	atlasTools         int // search_contract_operations and get_contract_operation
	callerMapEnabled   bool
	callerMapServiced  bool
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
	// A validated Buf checker is stood in for, so admission alone decides
	// whether the compatibility surfaces appear; nothing here calls it.
	deps := &serveDeps{ctx: context.Background(), cfg: cfg, compatibility: stubCompatibility{}}
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
		callerMapEnabled:   apiOpts.CallerMapEnabled,
		callerMapServiced:  apiOpts.CallerMap != nil,
		comparisonReleased: apiOpts.CallerComparisonReleased,
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
		case "check_wire_compatibility":
			served.wireTool = true
		case "find_kafka_topic_usage":
			served.topicTool = true
		case "list_kafka_topics":
			served.listTopicsTool = true
		case "read_topic_inventory":
			served.readInventoryTool = true
		case "search_contract_operations", "get_contract_operation":
			served.atlasTools++
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
		"contract-atlas", "contract-caller-comparison", "contract-caller-map", "contract-compatibility",
		"contract-impact-report", "kafka-topic-inventory", "kafka-topic-usage", "service-catalog-v2",
		"wire-compatibility",
	}
	tests := []struct {
		name      string
		released  bool
		claim     bool     // a signed record selecting the T53.3 Buf WIRE claim
		producer  bool     // a signed record selecting the T55.2 producer recipe, bound as T55.3 will
		consumer  bool     // a signed record selecting the T56.2 consumer recipe, bound as T56.3 will
		kafka     []string // T57.3: one selection of these Kafka packs sharing one binding
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
				callerMapTool: true, comparisonTool: true, proofTool: true, topicTool: true, wireTool: true,
				listTopicsTool: true, readInventoryTool: true,
				callerMapEnabled: true, callerMapServiced: true,
			},
		},
		{
			name:      "the kafka switch alone serves no Caller Map",
			configure: func(cfg *config.Config) { cfg.Experimental.ProvisionalKafkaExtraction = true },
			want: servedCallerSurfaces{
				capabilities: []string{
					"contract-atlas", "contract-compatibility", "contract-impact-report",
					"kafka-topic-inventory", "kafka-topic-usage", "service-catalog-v2",
					"wire-compatibility",
				},
				proofTool: true, topicTool: true, wireTool: true,
				listTopicsTool: true, readInventoryTool: true,
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
			// T56.2: the consumer plane alone, independent of the producer release.
			// T57.2: the composed inventory stays unlisted, so listTopicsTool and
			// readInventoryTool are false here by omission — only the released
			// T57.3 Topics claim beside both planes lists it.
			name:      "a released Kafka consumer alone serves only consumer topic usage",
			consumer:  true,
			configure: func(*config.Config) {},
			want: servedCallerSurfaces{
				capabilities: []string{"kafka-topic-usage", "service-catalog-v2"},
				topicTool:    true,
			},
		},
		{
			// T55.2: the producer plane alone, never the consumer or any other proof surface.
			name:      "a released Kafka producer alone serves only producer topic usage",
			producer:  true,
			configure: func(*config.Config) {},
			want: servedCallerSurfaces{
				capabilities: []string{"kafka-topic-usage", "service-catalog-v2"},
				topicTool:    true,
			},
		},
		{
			// T49.3: Thrift declarations alone admit Atlas and nothing else.
			name:      "a released Thrift contract alone serves only Contract Atlas",
			kafka:     []string{thriftContractPackID},
			configure: func(*config.Config) {},
			want: servedCallerSurfaces{
				capabilities: []string{"contract-atlas", "service-catalog-v2"},
			},
		},
		{
			// T57.3: the combined claim adds exactly the composed inventory.
			name:      "released Kafka planes with the Topics claim serve the composed inventory",
			kafka:     []string{kafkaProducerPackID, kafkaConsumerPackID, kafkaTopicsPackID},
			configure: func(*config.Config) {},
			want: servedCallerSurfaces{
				capabilities: []string{"kafka-topic-inventory", "kafka-topic-usage", "service-catalog-v2"},
				topicTool:    true, listTopicsTool: true, readInventoryTool: true,
			},
		},
		{
			// The combined claim measured both planes; one alone withdraws it.
			name:      "the Topics claim beside one released plane is withdrawn",
			kafka:     []string{kafkaProducerPackID, kafkaTopicsPackID},
			configure: func(*config.Config) {},
			want: servedCallerSurfaces{
				capabilities: []string{"kafka-topic-usage", "service-catalog-v2"},
				topicTool:    true,
			},
		},
		{
			name:      "a released Buf WIRE claim alone serves only the wire verdict",
			claim:     true,
			configure: func(*config.Config) {},
			want: servedCallerSurfaces{
				capabilities: []string{"service-catalog-v2", "wire-compatibility"},
				wireTool:     true,
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
				callerMapTool: true, comparisonTool: true, proofTool: true, topicTool: true, wireTool: true,
				listTopicsTool: true, readInventoryTool: true,
				callerMapEnabled: true, callerMapServiced: true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Server.DataDir = t.TempDir()
			test.configure(cfg)
			if test.producer {
				restore := packRecipes
				packRecipes = map[string]func() []extract.Extractor{kafkaProducerPackID: kafkaProducerRecipe}
				t.Cleanup(func() { packRecipes = restore })
				dir := t.TempDir()
				public, release := writeReleasedRecord(t, dir, kafkaProducerPackID, "key-1")
				bindReleaseLoad(t, release)
				cfg.ReleaseSelection.Path = dir
				cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
			}
			if test.consumer {
				restore := packRecipes
				packRecipes = map[string]func() []extract.Extractor{kafkaConsumerPackID: kafkaConsumerRecipe}
				t.Cleanup(func() { packRecipes = restore })
				dir := t.TempDir()
				public, release := writeReleasedRecord(t, dir, kafkaConsumerPackID, "key-1")
				bindReleaseLoad(t, release)
				cfg.ReleaseSelection.Path = dir
				cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
			}
			if len(test.kafka) > 0 {
				restore := packRecipes
				packRecipes = map[string]func() []extract.Extractor{
					kafkaProducerPackID: kafkaProducerRecipe, kafkaConsumerPackID: kafkaConsumerRecipe,
					thriftContractPackID: thriftContractRecipe,
				}
				t.Cleanup(func() { packRecipes = restore })
				selection := newReleasedSelection(t)
				first := selection.admit(t, test.kafka[0], "key-0", packrelease.StatusReleased)
				var more []*packrelease.PackRelease
				for index, packID := range test.kafka[1:] {
					more = append(more, selection.admitShaped(t, packID, fmt.Sprintf("key-%d", index+1), sharingBinding(first)))
				}
				bindReleaseLoad(t, first, more...)
				selection.bind(cfg)
			}
			if test.claim {
				dir := t.TempDir()
				public, release := writeReleasedRecord(t, dir, wireCompatibilityPackID, "key-1")
				bindReleaseLoad(t, release)
				cfg.ReleaseSelection.Path = dir
				cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
			}
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
			// T49.4: Atlas MCP parity — both discovery tools exactly when the
			// contract-atlas capability is advertised, Caller Map or not.
			if wantAtlas := slices.Contains(got.capabilities, "contract-atlas"); (got.atlasTools == 2) != wantAtlas ||
				(!wantAtlas && got.atlasTools != 0) {
				t.Fatalf("Atlas MCP tools = %d with contract-atlas advertised = %v", got.atlasTools, wantAtlas)
			}
			got.atlasTools = 0
			if !slices.Equal(got.capabilities, test.want.capabilities) {
				t.Fatalf("/api/version capabilities = %v, want %v", got.capabilities, test.want.capabilities)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("served = %+v, want %+v", got, test.want)
			}
		})
	}
}

type stubCompatibility struct{}

func (stubCompatibility) Check(context.Context, compat.Request) (*compat.CompatibilityResult, error) {
	return nil, compat.ErrUnavailable
}

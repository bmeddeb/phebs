package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/packrelease"
	"github.com/bmeddeb/phebs/internal/store"
)

// releasedStubVersion is the version every released stub recipe reports. No
// dark extractor reports it (the real proto-contract and grpc-caller report
// "3.0.0" and "1.5.0"), so an admitted domain@version carrying it can only come
// from a signed released pack, never from the switch-gated dark extractor that
// shares its domain.
const releasedStubVersion = "9.9.9"

// releasedSelection accumulates one selection directory and its trust anchors,
// so a scenario configures packs the way an operator would: sign records into
// the directory, then point the configuration at the directory and the keys.
type releasedSelection struct {
	dir  string
	keys []config.ReleaseKey
}

func newReleasedSelection(t *testing.T) *releasedSelection {
	t.Helper()
	return &releasedSelection{dir: t.TempDir()}
}

// admit signs a record for packID carrying status into the selection directory,
// adds its key to the trust anchors, and returns the record so the scenario can
// bind the load bindings that record names.
func (s *releasedSelection) admit(t *testing.T, packID, keyID, status string) *packrelease.PackRelease {
	t.Helper()
	return s.admitShaped(t, packID, keyID, func(release *packrelease.PackRelease) {
		release.DerivedStatus = status
	})
}

// admitShaped signs a released record for packID after shape adjusts it, adds
// its key to the trust anchors, and returns the record.
func (s *releasedSelection) admitShaped(
	t *testing.T, packID, keyID string, shape func(*packrelease.PackRelease),
) *packrelease.PackRelease {
	t.Helper()
	public, release := writeShapedRecord(t, s.dir, packID, keyID, shape)
	s.keys = append(s.keys, config.ReleaseKey{ID: keyID, PublicKey: public})
	return release
}

// bind points cfg at the accumulated directory and trust anchors.
func (s *releasedSelection) bind(cfg *config.Config) {
	cfg.ReleaseSelection.Path = s.dir
	cfg.ReleaseSelection.Keys = s.keys
}

// sharingBinding shapes a released record to name the implementation identity
// and referenced-artifacts root that reference names. The selection holds one
// running binding, so two released packs are admitted together only when both
// records name it.
func sharingBinding(reference *packrelease.PackRelease) func(*packrelease.PackRelease) {
	return func(release *packrelease.PackRelease) {
		release.Implementation = reference.Implementation
		release.ReferencedArtifactsRootDigest = reference.ReferencedArtifactsRootDigest
	}
}

// expiredValidation shapes a released record whose validation has expired, so
// the selection withdraws its pack as automatic suspension.
func expiredValidation(release *packrelease.PackRelease) {
	release.ApprovedAt = "2019-07-17T20:00:00Z"
	release.Validation.ExpiresAt = "2020-01-01T00:00:00Z"
}

// bindRecipes installs the given in-tree recipe registry for the rest of the
// test. packRecipes ships empty on purpose, so every released-pack admission in
// a test installs its recipe the same way the owning epic eventually will.
func bindRecipes(t *testing.T, recipes map[string][]extract.Extractor) {
	t.Helper()
	restore := packRecipes
	packRecipes = make(map[string]func() []extract.Extractor, len(recipes))
	for packID, extractors := range recipes {
		packRecipes[packID] = func() []extract.Extractor {
			return slices.Clone(extractors)
		}
	}
	t.Cleanup(func() { packRecipes = restore })
}

// extractorDomains returns the sorted domains of an admitted extractor set so
// a scenario can compare sets rather than construction order.
func extractorDomains(extractors []extract.Extractor) []string {
	domains := make([]string, 0, len(extractors))
	for _, extractor := range extractors {
		domains = append(domains, extractor.Domain())
	}
	slices.Sort(domains)
	return domains
}

// releasedIdentities returns the sorted domain@version identities of the
// admitted extractors that only a signed released stub recipe reports.
func releasedIdentities(extractors []extract.Extractor) []string {
	identities := []string{}
	for _, extractor := range extractors {
		if extractor.Version() == releasedStubVersion {
			identities = append(identities, extractor.Domain()+"@"+extractor.Version())
		}
	}
	slices.Sort(identities)
	return identities
}

// captureLogDuring runs fn with the standard logger redirected and returns
// exactly what it wrote, so each scenario pins its startup line byte for byte
// (silence included) rather than only exercising it.
func captureLogDuring(t *testing.T, run func()) string {
	t.Helper()
	var output bytes.Buffer
	previousWriter, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()
	run()
	return output.String()
}

// TestStartupAdmissionAndDiscoveryMatrix drives the real ordinary startup
// boundary (newServeExtractionRegistries) across the absent, one-pack and
// mixed-pack configuration matrix T48.4 must qualify. Every scenario asserts
// the admitted domain set, which of those domains a signed released recipe
// supplied, both downstream registry gates, the exact startup refusal when
// admission refuses, and the exact startup log — including silence, because a
// dark startup must stay byte-for-byte unchanged. The Caller Map discovery
// half of the matrix is TestCallerMapDiscoveryAcrossAdmissionShapes, over this
// same boundary.
func TestStartupAdmissionAndDiscoveryMatrix(t *testing.T) {
	declaration := stubExtractor{domain: "proto-contract", version: releasedStubVersion}
	caller := stubExtractor{domain: "grpc-caller", version: releasedStubVersion}
	tests := []struct {
		name string
		// prepare builds the scenario's configuration and installs the load
		// bindings and recipes that configuration requires.
		prepare func(t *testing.T) *config.Config
		// refuseContains is a substring the startup refusal must carry; when
		// refuseContains and refuseReason are both empty the scenario must be
		// admitted instead.
		refuseContains string
		// refuseReason, when set, is the classified reason the refusal must
		// carry.
		refuseReason packrelease.Reason
		// wantDomains is the sorted admitted domain set.
		wantDomains []string
		// wantReleased is the sorted domain@version set a signed released
		// recipe supplied; every other admitted domain is switch-gated dark.
		wantReleased []string
		// wantResolver and wantCaller are the downstream registry gates. The
		// runtime scheduling and reconciliation paths read them, and the
		// caller registry also decides whether the Caller Map publication
		// reader exists at all.
		wantResolver bool
		wantCaller   bool
		// wantLog is the exact startup log, silence included.
		wantLog string
	}{
		{
			name: "absent selection admits nothing and enables nothing",
			prepare: func(*testing.T) *config.Config {
				return &config.Config{}
			},
			wantDomains:  []string{},
			wantReleased: []string{},
			wantLog:      "",
		},
		{
			name: "the proto switch alone admits the dark set",
			prepare: func(*testing.T) *config.Config {
				cfg := &config.Config{}
				cfg.Experimental.ProvisionalProtoExtraction = true
				return cfg
			},
			wantDomains:  []string{"grpc-caller", "grpc-consumer", "proto-contract", "scip-proto-field"},
			wantReleased: []string{},
			wantResolver: true,
			wantCaller:   true,
			wantLog:      "",
		},
		{
			// The AC's "one released component does not activate another": a
			// released declaration pack governs its own domain and leaves both
			// dependent registries unenabled because no caller extractor is
			// admitted.
			name: "one released declaration pack governs only its own domain",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				bindReleaseLoad(t, selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{"phebs.proto.contract": {declaration}})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			wantDomains:  []string{"proto-contract"},
			wantReleased: []string{"proto-contract@9.9.9"},
			wantLog:      "",
		},
		{
			name: "a released caller pack without its declaration refuses startup",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				bindReleaseLoad(t, selection.admit(t, "phebs.grpc.caller", "key-1", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{"phebs.grpc.caller": {caller}})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseContains: `resolver caller "grpc-caller" requires declaration domain "proto-contract"`,
			wantLog:        "",
		},
		{
			// The dependency pair the release gate exists for. It is admitted
			// only because both records name one implementation identity and
			// artifact root; the next scenario is the same pair without that.
			name: "a released declaration and caller naming one binding are admitted together",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				declared := selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusReleased)
				calling := selection.admitShaped(t, "phebs.grpc.caller", "key-2", sharingBinding(declared))
				bindReleaseLoad(t, declared, calling)
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.proto.contract": {declaration},
					"phebs.grpc.caller":    {caller},
				})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			wantDomains:  []string{"grpc-caller", "proto-contract"},
			wantReleased: []string{"grpc-caller@9.9.9", "proto-contract@9.9.9"},
			wantResolver: true,
			wantCaller:   true,
			wantLog:      "",
		},
		{
			// A real limit, pinned rather than endorsed: the selection holds
			// one running implementation identity and one artifact root, so a
			// released record naming another pack implementation or root
			// cannot bind. Startup refuses as a whole instead of admitting the
			// pack that does match. T48.4c owns that running identity.
			name: "two released packs naming different bindings refuse startup",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				declared := selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusReleased)
				calling := selection.admit(t, "phebs.grpc.caller", "key-2", packrelease.StatusReleased)
				bindReleaseLoad(t, declared, calling)
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.proto.contract": {declaration},
					"phebs.grpc.caller":    {caller},
				})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseContains: "implementation identity does not match the running implementation",
			refuseReason:   packrelease.ReasonDigestMismatch,
			wantLog:        "",
		},
		{
			// A suspension of the declaration withdraws it; nothing replaces
			// it, so the released caller loses its structural requirement and
			// startup refuses instead of joining a stale declaration.
			name: "a suspended declaration withdraws instead of satisfying the caller",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusSuspended)
				bindReleaseLoad(t, selection.admit(t, "phebs.grpc.caller", "key-2", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{"phebs.grpc.caller": {caller}})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseContains: `resolver caller "grpc-caller" requires declaration domain "proto-contract"`,
			wantLog:        "pack release selection: 1 configured pack(s) not admitted: phebs.proto.contract=suspended\n",
		},
		{
			// Operator revocation is the other suspension control, and it is
			// judged before the record's own status and before any binding.
			name: "a revoked declaration withdraws instead of satisfying the caller",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				declared := selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusReleased)
				bindReleaseLoad(t, selection.admit(t, "phebs.grpc.caller", "key-2", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.proto.contract": {declaration},
					"phebs.grpc.caller":    {caller},
				})
				cfg := &config.Config{}
				selection.bind(cfg)
				cfg.ReleaseSelection.Revoked = []string{declared.ReleaseID}
				return cfg
			},
			refuseContains: `resolver caller "grpc-caller" requires declaration domain "proto-contract"`,
			wantLog:        "pack release selection: 1 configured pack(s) not admitted: phebs.proto.contract=revoked\n",
		},
		{
			// Validation expiry is automatic suspension of the governing
			// record, so it withdraws the declaration exactly like a signed
			// suspension.
			name: "an expired declaration withdraws instead of satisfying the caller",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				selection.admitShaped(t, "phebs.proto.contract", "key-1", expiredValidation)
				bindReleaseLoad(t, selection.admit(t, "phebs.grpc.caller", "key-2", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.proto.contract": {declaration},
					"phebs.grpc.caller":    {caller},
				})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseContains: `resolver caller "grpc-caller" requires declaration domain "proto-contract"`,
			wantLog:        "pack release selection: 1 configured pack(s) not admitted: phebs.proto.contract=expired\n",
		},
		{
			// The mixed-provenance gap, pinned rather than endorsed: a
			// provisional extraction switch can satisfy the declaration domain
			// of a released caller pack, so a suspended signed declaration
			// dependency is structurally replaced by an experimental-dark
			// extractor the signed record never named. The released caller's
			// own identity proves it, not the dark caller, is the one admitted.
			// T47.3 owns this finding; Epic 48's independent-promotion premise
			// is exactly why the join is not refused here today — retiring it
			// is a product decision, not a merge-bar change to this slice.
			name: "a provisional declaration still satisfies a released caller",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusSuspended)
				bindReleaseLoad(t, selection.admit(t, "phebs.grpc.caller", "key-2", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{"phebs.grpc.caller": {caller}})
				cfg := &config.Config{}
				cfg.Experimental.ProvisionalProtoExtraction = true
				selection.bind(cfg)
				return cfg
			},
			wantDomains:  []string{"grpc-caller", "grpc-consumer", "proto-contract", "scip-proto-field"},
			wantReleased: []string{"grpc-caller@9.9.9"},
			wantResolver: true,
			wantCaller:   true,
			wantLog:      "pack release selection: 1 configured pack(s) not admitted: phebs.proto.contract=suspended\n",
		},
		{
			name: "an all-withdrawn directory admits nothing and says so",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				selection.admit(t, "phebs.grpc.caller", "key-1", packrelease.StatusSuspended)
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			wantDomains:  []string{},
			wantReleased: []string{},
			wantLog:      "pack release selection: 1 configured pack(s) not admitted: phebs.grpc.caller=suspended\n",
		},
		{
			name: "a bound released pack without a recipe refuses startup",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				bindReleaseLoad(t, selection.admit(t, "phebs.unbound.pack", "key-1", packrelease.StatusReleased))
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseContains: "has no fixed in-tree recipe",
			wantLog:        "",
		},
		{
			name: "a released pack without load bindings refuses as unresolved",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				selection.admit(t, "phebs.bound.pack", "key-1", packrelease.StatusReleased)
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseReason: packrelease.ReasonUnresolvedReference,
			wantLog:      "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.prepare(t)
			deps := &serveDeps{ctx: context.Background(), cfg: cfg}
			var err error
			output := captureLogDuring(t, func() { err = newServeExtractionRegistries(deps) })

			if output != test.wantLog {
				t.Fatalf("startup log = %q, want %q", output, test.wantLog)
			}
			if test.refuseContains != "" || test.refuseReason != "" {
				if err == nil {
					t.Fatalf("startup admitted; want a refusal containing %q (reason %q)",
						test.refuseContains, test.refuseReason)
				}
				if test.refuseContains != "" && !strings.Contains(err.Error(), test.refuseContains) {
					t.Fatalf("refusal %q does not contain %q", err, test.refuseContains)
				}
				if test.refuseReason != "" {
					if reason, ok := packrelease.ReasonOf(err); !ok || reason != test.refuseReason {
						t.Fatalf("refusal reason = %q (%v), want %q", reason, err, test.refuseReason)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("newServeExtractionRegistries: %v", err)
			}
			if got := extractorDomains(deps.exs); !slices.Equal(got, test.wantDomains) {
				t.Fatalf("admitted domains = %v, want %v", got, test.wantDomains)
			}
			if got := releasedIdentities(deps.exs); !slices.Equal(got, test.wantReleased) {
				t.Fatalf("released identities = %v, want %v", got, test.wantReleased)
			}
			if got := deps.resolverRegistry.Enabled(); got != test.wantResolver {
				t.Fatalf("resolver registry Enabled = %v, want %v", got, test.wantResolver)
			}
			if got := deps.callerRegistry.Enabled(); got != test.wantCaller {
				t.Fatalf("caller registry Enabled = %v, want %v", got, test.wantCaller)
			}
		})
	}
}

// startupCallerMapDiscovery reports whether the admitted deps would offer the
// Caller Map surface on the API and on MCP. It composes the production gates in
// production order: the caller publication reader the admitted caller registry
// permits (newServeCallerReader), the provisional-switch half
// (callerMapDiscoverable), the API service constructors, and the
// nil-preserving MCP conversion; it then lists the tools a real MCP server
// registers from them. Stubs stand in for a fully wired store, and nothing on
// this path calls them.
func startupCallerMapDiscovery(t *testing.T, deps *serveDeps) (apiOffered, mcpOffered bool) {
	t.Helper()
	reader, err := newServeCallerReader(deps, struct {
		callerexecute.PublicationReadStore
	}{})
	if err != nil {
		t.Fatalf("newServeCallerReader: %v", err)
	}
	opts := api.Options{
		Store:            struct{ store.Store }{},
		Evidence:         struct{ store.EvidenceStore }{},
		Principal:        func(context.Context) string { return "" },
		DataDir:          deps.cfg.Server.DataDir,
		CallerMapEnabled: callerMapDiscoverable(deps.cfg),
		CallerReader:     reader,
	}
	callerMap := api.NewCallerMapService(opts)
	catalogQueries, callerMapQueries, _ := mcpCallerMapServices(
		api.NewContractCatalogService(opts), callerMap, nil,
	)
	server := phebsmcp.NewServer(phebsmcp.Options{
		Version: "test", ContractCatalog: catalogQueries, CallerMap: callerMapQueries,
	})
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	go func() {
		_, _ = server.Connect(t.Context(), serverTransport, nil)
	}()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t48.4b-discovery", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	mcpOffered = slices.ContainsFunc(listed.Tools, func(tool *mcpsdk.Tool) bool {
		return tool.Name == "list_operation_callers"
	})
	return callerMap != nil, mcpOffered
}

// TestCallerMapDiscoveryAcrossAdmissionShapes holds Caller Map discovery on the
// API and on MCP across all sixteen provisional switch combinations under four
// real startup shapes: no selection, a released declaration admitted, a
// released declaration and caller admitted together, and a suspended
// declaration withdrawn. The surface needs a provisional caller switch and an
// enabled caller registry. Admission never flips it today, because a switch
// always admits its own caller domain, a released pack only replaces a dark
// extractor within the same domain, and withdrawal never removes a dark one. A
// released caller pair alone enables caller execution but is not discoverable.
func TestCallerMapDiscoveryAcrossAdmissionShapes(t *testing.T) {
	// A withdrawn pack is withdrawn visibly: the startup log names it exactly.
	withdrawnLog := "pack release selection: 1 configured pack(s) not admitted: phebs.proto.contract=suspended\n"
	declaration := stubExtractor{domain: "proto-contract", version: releasedStubVersion}
	caller := stubExtractor{domain: "grpc-caller", version: releasedStubVersion}
	shapes := []struct {
		name string
		// configure signs the shape's records into a selection and installs its
		// bindings and recipes; nil means no selection path is configured.
		configure func(t *testing.T, cfg *config.Config)
		// wantReleased is the sorted domain@version set a released recipe
		// must supply.
		wantReleased []string
		// callerPack is whether the shape itself admits a released caller,
		// which enables the caller registry with every switch dark.
		callerPack bool
		wantLog    string
	}{
		{name: "no selection", wantReleased: []string{}},
		{
			name: "a released declaration admitted",
			configure: func(t *testing.T, cfg *config.Config) {
				selection := newReleasedSelection(t)
				bindReleaseLoad(t, selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{"phebs.proto.contract": {declaration}})
				selection.bind(cfg)
			},
			wantReleased: []string{"proto-contract@9.9.9"},
		},
		{
			name: "a released declaration and caller admitted",
			configure: func(t *testing.T, cfg *config.Config) {
				selection := newReleasedSelection(t)
				declared := selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusReleased)
				calling := selection.admitShaped(t, "phebs.grpc.caller", "key-2", sharingBinding(declared))
				bindReleaseLoad(t, declared, calling)
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.proto.contract": {declaration},
					"phebs.grpc.caller":    {caller},
				})
				selection.bind(cfg)
			},
			wantReleased: []string{"grpc-caller@9.9.9", "proto-contract@9.9.9"},
			callerPack:   true,
		},
		{
			name: "a suspended declaration withdrawn",
			configure: func(t *testing.T, cfg *config.Config) {
				selection := newReleasedSelection(t)
				selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusSuspended)
				selection.bind(cfg)
			},
			wantReleased: []string{},
			wantLog:      withdrawnLog,
		},
	}
	for _, shape := range shapes {
		for mask := range 16 {
			proto, thrift := mask&1 != 0, mask&2 != 0
			thriftField, kafka := mask&4 != 0, mask&8 != 0
			name := fmt.Sprintf("%s/proto=%t,thrift=%t,thrift_field=%t,kafka=%t",
				shape.name, proto, thrift, thriftField, kafka)
			t.Run(name, func(t *testing.T) {
				cfg := &config.Config{}
				cfg.Server.DataDir = t.TempDir()
				cfg.Experimental.ProvisionalProtoExtraction = proto
				cfg.Experimental.ProvisionalThriftExtraction = thrift
				cfg.Experimental.ProvisionalThriftFieldExtraction = thriftField
				cfg.Experimental.ProvisionalKafkaExtraction = kafka
				if shape.configure != nil {
					shape.configure(t, cfg)
				}
				deps := &serveDeps{ctx: context.Background(), cfg: cfg}
				var err error
				output := captureLogDuring(t, func() { err = newServeExtractionRegistries(deps) })
				if err != nil {
					t.Fatalf("newServeExtractionRegistries: %v", err)
				}
				if output != shape.wantLog {
					t.Fatalf("startup log = %q, want %q", output, shape.wantLog)
				}
				if got := releasedIdentities(deps.exs); !slices.Equal(got, shape.wantReleased) {
					t.Fatalf("released identities = %v (domains %v), want %v",
						got, extractorDomains(deps.exs), shape.wantReleased)
				}
				switchOn := proto || thrift
				if got, want := deps.callerRegistry.Enabled(), switchOn || shape.callerPack; got != want {
					t.Fatalf("caller registry Enabled = %v, want %v", got, want)
				}
				apiOffered, mcpOffered := startupCallerMapDiscovery(t, deps)
				if apiOffered != switchOn {
					t.Fatalf("API Caller Map offered = %v, want %v", apiOffered, switchOn)
				}
				if mcpOffered != switchOn {
					t.Fatalf("MCP Caller Map tools offered = %v, want %v", mcpOffered, switchOn)
				}
			})
		}
	}
}

package main

import (
	"bytes"
	"context"
	"log"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

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
	public, release := writeStatusRecord(t, s.dir, packID, keyID, status)
	s.keys = append(s.keys, config.ReleaseKey{ID: keyID, PublicKey: public})
	return release
}

// bind points cfg at the accumulated directory and trust anchors.
func (s *releasedSelection) bind(cfg *config.Config) {
	cfg.ReleaseSelection.Path = s.dir
	cfg.ReleaseSelection.Keys = s.keys
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
// the admitted domain set, both registry gates the API/MCP discovery surfaces
// read, the exact startup refusal when admission refuses, and the exact
// startup log — including silence, because a dark startup must stay
// byte-for-byte unchanged.
func TestStartupAdmissionAndDiscoveryMatrix(t *testing.T) {
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
		// wantResolver and wantCaller are the registry gates the discovery
		// surfaces read.
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
			wantDomains: []string{},
			wantLog:     "",
		},
		{
			name: "the proto switch alone admits the dark set",
			prepare: func(*testing.T) *config.Config {
				cfg := &config.Config{}
				cfg.Experimental.ProvisionalProtoExtraction = true
				return cfg
			},
			wantDomains:  []string{"grpc-caller", "grpc-consumer", "proto-contract", "scip-proto-field"},
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
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.proto.contract": {stubExtractor{domain: "proto-contract", version: "3.0.0"}},
				})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			wantDomains: []string{"proto-contract"},
			wantLog:     "",
		},
		{
			name: "a released caller pack without its declaration refuses startup",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				bindReleaseLoad(t, selection.admit(t, "phebs.grpc.caller", "key-1", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.grpc.caller": {stubExtractor{domain: "grpc-caller", version: "1.5.0"}},
				})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseContains: `resolver caller "grpc-caller" requires declaration domain "proto-contract"`,
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
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.grpc.caller": {stubExtractor{domain: "grpc-caller", version: "1.5.0"}},
				})
				cfg := &config.Config{}
				selection.bind(cfg)
				return cfg
			},
			refuseContains: `resolver caller "grpc-caller" requires declaration domain "proto-contract"`,
			wantLog:        "pack release selection: 1 configured pack(s) not admitted: phebs.proto.contract=suspended\n",
		},
		{
			// The mixed-provenance gap, pinned rather than endorsed: a
			// provisional extraction switch can satisfy the declaration domain
			// of a released caller pack, so a suspended signed declaration
			// dependency is structurally replaced by an experimental-dark
			// extractor the signed record never named. T47.3 owns this finding;
			// Epic 48's independent-promotion premise is exactly why the join
			// is not refused here today — retiring it is a product decision,
			// not a merge-bar change to this slice.
			name: "a provisional declaration still satisfies a released caller",
			prepare: func(t *testing.T) *config.Config {
				selection := newReleasedSelection(t)
				selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusSuspended)
				bindReleaseLoad(t, selection.admit(t, "phebs.grpc.caller", "key-2", packrelease.StatusReleased))
				bindRecipes(t, map[string][]extract.Extractor{
					"phebs.grpc.caller": {stubExtractor{domain: "grpc-caller", version: "1.5.0"}},
				})
				cfg := &config.Config{}
				cfg.Experimental.ProvisionalProtoExtraction = true
				selection.bind(cfg)
				return cfg
			},
			wantDomains:  []string{"grpc-caller", "grpc-consumer", "proto-contract", "scip-proto-field"},
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
			wantDomains: []string{},
			wantLog:     "pack release selection: 1 configured pack(s) not admitted: phebs.grpc.caller=suspended\n",
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
					t.Fatalf("startup admitted; want a refusal containing %q", test.refuseContains)
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
			if got := deps.resolverRegistry.Enabled(); got != test.wantResolver {
				t.Fatalf("resolver registry Enabled = %v, want %v", got, test.wantResolver)
			}
			if got := deps.callerRegistry.Enabled(); got != test.wantCaller {
				t.Fatalf("caller registry Enabled = %v, want %v", got, test.wantCaller)
			}
		})
	}
}

// TestCallerMapDiscoveryFollowsTheProvisionalSwitchesAlone pins that the
// Caller Map discovery predicate reads the provisional extraction switches and
// nothing else: a configured selection — even one naming a signed admitted
// pack — never flips the surface, so the ordinary/released path cannot make
// another component discoverable and only an explicit future ticket can move
// discovery to selection-aware behavior.
func TestCallerMapDiscoveryFollowsTheProvisionalSwitchesAlone(t *testing.T) {
	selection := newReleasedSelection(t)
	selection.admit(t, "phebs.proto.contract", "key-1", packrelease.StatusReleased)

	tests := []struct {
		name        string
		proto       bool
		thrift      bool
		wantOffered bool
	}{
		{"both switches dark", false, false, false},
		{"the proto switch alone", true, false, true},
		{"the thrift switch alone", false, true, true},
		{"both switches", true, true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Experimental.ProvisionalProtoExtraction = test.proto
			cfg.Experimental.ProvisionalThriftExtraction = test.thrift
			if got := callerMapDiscoverable(cfg); got != test.wantOffered {
				t.Fatalf("without a selection: discoverable = %v, want %v", got, test.wantOffered)
			}
			selection.bind(cfg)
			if got := callerMapDiscoverable(cfg); got != test.wantOffered {
				t.Fatalf("with a selection: discoverable = %v, want %v", got, test.wantOffered)
			}
		})
	}
}

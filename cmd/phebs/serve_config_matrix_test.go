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
// the admitted domain set, both downstream registry gates that the runtime
// scheduling and reconciliation paths read, the exact startup refusal when
// admission refuses, and the exact startup log — including silence, because a
// dark startup must stay byte-for-byte unchanged. The Caller Map discovery
// predicate is the other half of the matrix and is pinned by
// TestCallerMapDiscoveryFollowsTheProvisionalSwitchesAlone over this same
// boundary.
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
		// wantResolver and wantCaller are the downstream registry gates the
		// runtime scheduling and reconciliation paths read; the API/MCP
		// discovery surfaces never consult them.
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

// TestCallerMapDiscoveryFollowsTheProvisionalSwitchesAlone holds the discovery
// predicate across every provisional switch combination under three real
// startup shapes: no selection, a signed released pack genuinely admitted by
// newServeExtractionRegistries, and a suspended pack it withdraws. Discovery
// follows the provisional switches alone, so neither admission nor withdrawal
// can make another component discoverable, and only an explicit future ticket
// can move discovery to selection-aware behavior.
func TestCallerMapDiscoveryFollowsTheProvisionalSwitchesAlone(t *testing.T) {
	// A withdrawn pack is withdrawn visibly: the startup log names it exactly.
	withdrawnLog := "pack release selection: 1 configured pack(s) not admitted: phebs.proto.contract=suspended\n"
	// The stub recipe's version is deliberately distinct from every dark
	// extractor's identity — the real proto-contract extractor reports the same
	// "3.0.0" a fixture would naively reuse — so presence of this exact
	// (domain, version) pair proves the signed released pack was admitted
	// rather than the switch-gated dark extractor that shares its domain.
	const releasedVersion = "9.9.9"
	tests := []struct {
		name   string
		proto  bool
		thrift bool
		// status is the governing record's derived status; "" means no
		// selection path is configured at all.
		status string
		// wantAdmitted is whether the released pack's own extractor reached the
		// admitted set, which only the signed released shape earns and the
		// suspended shape is refused.
		wantAdmitted bool
		wantOffered  bool
		wantLog      string
	}{
		{"no selection, both switches dark", false, false, "", false, false, ""},
		{"no selection, the proto switch alone", true, false, "", false, true, ""},
		{"no selection, the thrift switch alone", false, true, "", false, true, ""},
		{"no selection, both switches", true, true, "", false, true, ""},
		{"a released pack admitted, both switches dark", false, false, packrelease.StatusReleased, true, false, ""},
		{"a released pack admitted, the proto switch alone", true, false, packrelease.StatusReleased, true, true, ""},
		{"a released pack admitted, the thrift switch alone", false, true, packrelease.StatusReleased, true, true, ""},
		{"a released pack admitted, both switches", true, true, packrelease.StatusReleased, true, true, ""},
		{"a suspended pack withdrawn, both switches dark", false, false, packrelease.StatusSuspended, false, false, withdrawnLog},
		{"a suspended pack withdrawn, the proto switch alone", true, false, packrelease.StatusSuspended, false, true, withdrawnLog},
		{"a suspended pack withdrawn, the thrift switch alone", false, true, packrelease.StatusSuspended, false, true, withdrawnLog},
		{"a suspended pack withdrawn, both switches", true, true, packrelease.StatusSuspended, false, true, withdrawnLog},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Experimental.ProvisionalProtoExtraction = test.proto
			cfg.Experimental.ProvisionalThriftExtraction = test.thrift
			if test.status != "" {
				selection := newReleasedSelection(t)
				record := selection.admit(t, "phebs.proto.contract", "key-1", test.status)
				if test.status == packrelease.StatusReleased {
					bindReleaseLoad(t, record)
					bindRecipes(t, map[string][]extract.Extractor{
						"phebs.proto.contract": {stubExtractor{domain: "proto-contract", version: releasedVersion}},
					})
				}
				selection.bind(cfg)
			}
			if got := callerMapDiscoverable(cfg); got != test.wantOffered {
				t.Fatalf("before startup: discoverable = %v, want %v", got, test.wantOffered)
			}
			deps := &serveDeps{ctx: context.Background(), cfg: cfg}
			var err error
			output := captureLogDuring(t, func() { err = newServeExtractionRegistries(deps) })
			if err != nil {
				t.Fatalf("newServeExtractionRegistries: %v", err)
			}
			if output != test.wantLog {
				t.Fatalf("startup log = %q, want %q", output, test.wantLog)
			}
			admitted := slices.ContainsFunc(deps.exs, func(candidate extract.Extractor) bool {
				return candidate.Domain() == "proto-contract" && candidate.Version() == releasedVersion
			})
			if admitted != test.wantAdmitted {
				t.Fatalf("released pack extractor admitted = %v (domains %v), want %v",
					admitted, extractorDomains(deps.exs), test.wantAdmitted)
			}
			if got := callerMapDiscoverable(deps.cfg); got != test.wantOffered {
				t.Fatalf("after startup: discoverable = %v, want %v", got, test.wantOffered)
			}
		})
	}
}

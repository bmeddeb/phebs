package main

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/packrelease"
	"github.com/bmeddeb/phebs/spike/t201"
)

// TestThriftCallerMapPackIdentityAndRecipeFrozen pins the PHEBS-42 tuple: the
// pack identity, its claim version, and the recipe that carries the Thrift
// declaration extractor beside the released Thrift caller admission.
func TestThriftCallerMapPackIdentityAndRecipeFrozen(t *testing.T) {
	if thriftCallerMapPackID != "phebs.thrift.caller.go" || thriftCallerMapClaimVersion != "1.3.0" {
		t.Fatalf("Thrift Caller Map identity = %s %s", thriftCallerMapPackID, thriftCallerMapClaimVersion)
	}
	if _, bound := packRecipes[thriftCallerMapPackID]; !bound {
		t.Fatal("released Thrift Caller Map recipe is not bound")
	}
	recipe := thriftCallerMapRecipe()
	want := []struct{ domain, version string }{
		{"thrift-contract", thriftdecl.New().Version()},
		{"thrift-caller", "1.6.0-released.1"},
	}
	if len(recipe) != len(want) {
		t.Fatalf("Thrift Caller Map recipe has %d extractors, want %d", len(recipe), len(want))
	}
	for index, expected := range want {
		if recipe[index].Domain() != expected.domain || recipe[index].Version() != expected.version {
			t.Fatalf("Thrift Caller Map recipe[%d] = %s@%s, want %s@%s", index,
				recipe[index].Domain(), recipe[index].Version(), expected.domain, expected.version)
		}
	}
	if strings.TrimSuffix(recipe[1].Version(), "-released.1") != gocaller.NewThrift().Version() {
		t.Fatal("released Thrift admission no longer names its underlying algorithm version")
	}
}

// TestThriftCallerMapReleasedAdmission drives the real signed selection and
// in-tree recipes. The Thrift Caller Map admits only its two domains, keeps
// the protobuf-only comparison and operation Impact claims unchanged, and is
// withdrawn by revocation, suspension and expiry like every other pack.
func TestThriftCallerMapReleasedAdmission(t *testing.T) {
	grpcFamily := []string{callerMapPackID, callerComparisonPackID, operationImpactPackID}
	for _, test := range []struct {
		name                    string
		packs                   []string
		revoke, suspend, expire string
		wantThrift, wantGRPC    bool
	}{
		{name: "dark"},
		{name: "Thrift Caller Map alone", packs: []string{thriftCallerMapPackID}, wantThrift: true},
		{name: "beside the protobuf caller family", packs: append([]string{thriftCallerMapPackID}, grpcFamily...), wantThrift: true, wantGRPC: true},
		{name: "beside the Thrift Atlas", packs: []string{thriftCallerMapPackID, thriftContractPackID}, wantThrift: true},
		{name: "protobuf family without Thrift", packs: grpcFamily, wantGRPC: true},
		{name: "revoked", packs: []string{thriftCallerMapPackID}, revoke: thriftCallerMapPackID},
		{name: "suspended", packs: []string{thriftCallerMapPackID}, suspend: thriftCallerMapPackID},
		{name: "expired", packs: []string{thriftCallerMapPackID}, expire: thriftCallerMapPackID},
		{name: "Thrift revoked beside protobuf", packs: append([]string{thriftCallerMapPackID}, grpcFamily...), revoke: thriftCallerMapPackID, wantGRPC: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			var records []*packrelease.PackRelease
			for index, pack := range test.packs {
				r := selection.admitShaped(t, pack, "thrift-caller-key-"+strconv.Itoa(index), func(record *packrelease.PackRelease) {
					if len(records) > 0 {
						sharingBinding(records[0])(record)
					}
					if pack == test.suspend {
						record.DerivedStatus = packrelease.StatusSuspended
					}
					if pack == test.expire {
						expiredValidation(record)
					}
				})
				records = append(records, r)
			}
			cfg := &config.Config{}
			cfg.Server.DataDir = t.TempDir()
			if len(records) > 0 {
				bindReleaseLoad(t, records[0], records[1:]...)
				selection.bind(cfg)
				for _, record := range records {
					if record.PackID == test.revoke {
						cfg.ReleaseSelection.Revoked = append(cfg.ReleaseSelection.Revoked, record.ReleaseID)
					}
				}
			}
			deps := &serveDeps{ctx: t.Context(), cfg: cfg}
			if err := newServeExtractionRegistries(deps); err != nil {
				t.Fatal(err)
			}
			versions := map[string]string{}
			for _, extractor := range deps.exs {
				versions[extractor.Domain()] = extractor.Version()
			}
			if got := versions["thrift-caller"] == "1.6.0-released.1" && versions["thrift-contract"] == thriftdecl.New().Version(); got != test.wantThrift {
				t.Fatalf("released Thrift caller admission = %t, want %t (%v)", got, test.wantThrift, versions)
			}
			if got := versions["grpc-caller"] == "1.6.0-released.1"; got != test.wantGRPC {
				t.Fatalf("released protobuf caller admission = %t, want %t (%v)", got, test.wantGRPC, versions)
			}
			for _, domain := range []string{"thrift-consumer", "grpc-consumer"} {
				if _, ok := versions[domain]; ok {
					t.Fatalf("released caller admission registered name-matching %s", domain)
				}
			}
			served := serveCallerSurfaces(t, cfg)
			want := test.wantThrift || test.wantGRPC
			if served.callerMapEnabled != want || served.callerMapServiced != want || served.callerMapTool != want {
				t.Fatalf("Caller Map surface = %+v, want %t", served, want)
			}
			for _, capability := range []string{"contract-caller-comparison", "contract-impact-operation"} {
				if slices.Contains(served.capabilities, capability) != test.wantGRPC {
					t.Fatalf("%s = %t beside Thrift Caller Map, want %t (%v)",
						capability, !test.wantGRPC, test.wantGRPC, served.capabilities)
				}
			}
			if slices.Contains(served.capabilities, "contract-impact-report") || served.proofTool {
				t.Fatalf("Thrift Caller Map enabled the experimental proof surface: %v", served.capabilities)
			}
		})
	}
}

func TestThriftCallerMapUnsupportedClaimVersionRefuses(t *testing.T) {
	selection := newReleasedSelection(t)
	r := selection.admitShaped(t, thriftCallerMapPackID, "claim-version", func(r *packrelease.PackRelease) { r.PackClaimVersion = "2.0.0" })
	bindReleaseLoad(t, r)
	cfg := &config.Config{}
	selection.bind(cfg)
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("unsupported Thrift Caller Map claim version admitted")
	}
}

// TestThriftCallerMapReleasedAdmissionKeepsFactsAndChangesAuthority proves the
// released admission reproduces the dark reader's Thrift caller facts byte for
// byte over the t201 profile, while candidate and caller generation identities
// change so experimental Thrift caller publications are never served as released.
func TestThriftCallerMapReleasedAdmissionKeepsFactsAndChangesAuthority(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(profile.Files))
	for path, content := range profile.Files {
		files[path] = string(content)
	}
	released := thriftCallerMapRecipe()
	dark := []extract.Extractor{released[0], gocaller.NewThrift()}
	run := func(extractor extract.Extractor) []sdk.Fact {
		var facts []sdk.Fact
		coverage, extractErr := extractor.Extract(t.Context(), releasedCallerTestCorpus{newT307Corpus(files)}, func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		})
		if extractErr != nil || len(coverage.Failures) != 0 {
			t.Fatalf("Thrift caller extraction: %v, failures %v", extractErr, coverage.Failures)
		}
		return facts
	}
	want := run(dark[1])
	// Without an attribution snapshot the profile's call is an explicit
	// abstention; resolved Thrift rows are covered by the t2114 integration
	// fixture. What this proves is that the call is seen and the facts match.
	seen := false
	for _, fact := range want {
		seen = seen || fact.Assertion.Object == "/ledger.Ledger/get" && fact.Path == "src/thrift/caller.go"
	}
	if !seen || !reflect.DeepEqual(run(released[1]), want) {
		t.Fatalf("released Thrift admission changed or omitted caller facts: %+v", want)
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
		generation, generationErr := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
			Repository: "example.invalid/thrift-callers", HeadCommit: strings.Repeat("b", 40),
			DeclarationSetDigest:    testReleaseDigest("declarations"),
			CandidateManifestDigest: testReleaseDigest("candidate"), CandidatePolicyDigest: testReleaseDigest("policy"),
			ResolverGenerationDigest: testReleaseDigest("resolver"), ResolverManifestDigest: testReleaseDigest("root"),
			Extractors: registry.ExtractorIdentities(),
		})
		if generationErr != nil {
			t.Fatal(generationErr)
		}
		if policy == previousPolicy || generation.Digest == previousGeneration || generation.ExtractorSetDigest == previousExtractors {
			t.Fatal("released Thrift admission reused experimental candidate or caller authority")
		}
		previousPolicy, previousGeneration, previousExtractors = policy, generation.Digest, generation.ExtractorSetDigest
	}
}

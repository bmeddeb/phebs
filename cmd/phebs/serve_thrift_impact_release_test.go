package main

import (
	"slices"
	"strconv"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// TestThriftOperationImpactIdentityFrozen pins the PHEBS-45 tuple: the pack
// identity, its claim version and the implementation identity a release binds.
func TestThriftOperationImpactIdentityFrozen(t *testing.T) {
	if thriftOperationImpactPackID != "phebs.thrift.impact.operation" || thriftOperationImpactClaimVersion != "1.2.0" {
		t.Fatalf("Thrift operation Impact identity = %s %s", thriftOperationImpactPackID, thriftOperationImpactClaimVersion)
	}
	want := "build_operation_impact_report@" + api.OperationImpactSchemaVersion + "+thrift"
	if releasedClaims[thriftOperationImpactPackID] != want {
		t.Fatalf("Thrift operation Impact implementation identity = %q, want %q", releasedClaims[thriftOperationImpactPackID], want)
	}
	if _, recipe := packRecipes[thriftOperationImpactPackID]; recipe {
		t.Fatal("the Thrift operation Impact claim must not carry extractors of its own")
	}
}

// TestThriftOperationImpactReleasedAdmission drives the real signed selection.
// The Thrift operation Impact claim is admitted only beside the released
// Thrift Caller Map recipe, never alone or through the experimental Thrift
// switch, and is withdrawn by revocation, suspension and expiry; it neither
// needs nor enables the protobuf operation Impact claim.
func TestThriftOperationImpactReleasedAdmission(t *testing.T) {
	protobufImpact := []string{operationImpactPackID, callerMapPackID}
	for _, test := range []struct {
		name                    string
		packs                   []string
		revoke, suspend, expire string
		experimentalThrift      bool
		wantThrift, wantGRPC    bool
	}{
		{name: "dark"},
		{name: "joint admission", packs: []string{thriftOperationImpactPackID, thriftCallerMapPackID}, wantThrift: true},
		{name: "beside the Thrift Atlas and comparison", packs: []string{thriftOperationImpactPackID, thriftCallerMapPackID, thriftContractPackID, thriftCallerComparisonPackID}, wantThrift: true},
		{name: "claim alone", packs: []string{thriftOperationImpactPackID}},
		{name: "Thrift Caller Map alone", packs: []string{thriftCallerMapPackID}},
		{name: "claim beside only the protobuf Caller Map", packs: []string{thriftOperationImpactPackID, callerMapPackID}},
		{name: "beside protobuf operation Impact", packs: append([]string{thriftOperationImpactPackID, thriftCallerMapPackID}, protobufImpact...), wantThrift: true, wantGRPC: true},
		{name: "protobuf operation Impact without the Thrift claim", packs: append([]string{thriftCallerMapPackID}, protobufImpact...), wantGRPC: true},
		{name: "claim revoked", packs: []string{thriftOperationImpactPackID, thriftCallerMapPackID}, revoke: thriftOperationImpactPackID},
		{name: "Thrift Caller Map revoked", packs: []string{thriftOperationImpactPackID, thriftCallerMapPackID}, revoke: thriftCallerMapPackID},
		{name: "Thrift revoked beside protobuf", packs: append([]string{thriftOperationImpactPackID, thriftCallerMapPackID}, protobufImpact...), revoke: thriftCallerMapPackID, wantGRPC: true},
		{name: "claim suspended", packs: []string{thriftOperationImpactPackID, thriftCallerMapPackID}, suspend: thriftOperationImpactPackID},
		{name: "claim expired", packs: []string{thriftOperationImpactPackID, thriftCallerMapPackID}, expire: thriftOperationImpactPackID},
		{name: "experimental Thrift cannot substitute", packs: []string{thriftOperationImpactPackID}, experimentalThrift: true},
		{name: "released claim governs beside experimental Thrift", packs: []string{thriftOperationImpactPackID, thriftCallerMapPackID}, experimentalThrift: true, wantThrift: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			var records []*packrelease.PackRelease
			for index, pack := range test.packs {
				r := selection.admitShaped(t, pack, "thrift-impact-key-"+strconv.Itoa(index), func(record *packrelease.PackRelease) {
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
			cfg.Experimental.ProvisionalThriftExtraction = test.experimentalThrift
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
			if got := releasedThriftOperationImpact(deps); got != test.wantThrift {
				t.Fatalf("released Thrift operation Impact = %t, want %t", got, test.wantThrift)
			}
			if got := releasedOperationImpact(deps); got != test.wantGRPC {
				t.Fatalf("released protobuf operation Impact = %t, want %t", got, test.wantGRPC)
			}
			served := serveCallerSurfaces(t, cfg)
			want := test.wantThrift || test.wantGRPC
			if served.operationImpactTool != want || slices.Contains(served.capabilities, "contract-impact-operation") != want {
				t.Fatalf("operation Impact tool %t / capability %v, want %t", served.operationImpactTool, served.capabilities, want)
			}
			if !test.experimentalThrift && (slices.Contains(served.capabilities, "contract-impact-report") || served.proofTool) {
				t.Fatalf("Thrift operation Impact enabled the experimental proof surface: %v", served.capabilities)
			}
		})
	}
}

// TestThriftOperationImpactNeedsCurrentThriftRecipe refuses an old or missing
// Thrift declaration or caller extractor even when both domains are released.
func TestThriftOperationImpactNeedsCurrentThriftRecipe(t *testing.T) {
	base := func() *serveDeps {
		return &serveDeps{releasedClaims: map[string]bool{thriftOperationImpactPackID: true}, releasedDomains: map[string]bool{"thrift-contract": true, "thrift-caller": true}, exs: thriftCallerMapRecipe()}
	}
	if !releasedThriftOperationImpact(base()) {
		t.Fatal("current released Thrift recipe refused")
	}
	for _, domain := range []string{"thrift-contract", "thrift-caller"} {
		d := base()
		for index, ex := range d.exs {
			if ex.Domain() == domain {
				d.exs[index] = stubExtractor{domain: domain, version: "old"}
			}
		}
		if releasedThriftOperationImpact(d) {
			t.Fatalf("old %s admitted", domain)
		}
	}
	d := base()
	d.exs = []extract.Extractor{thriftCallerMapRecipe()[0]}
	if releasedThriftOperationImpact(d) {
		t.Fatal("missing Thrift caller admitted")
	}
}

func TestThriftOperationImpactUnsupportedClaimVersionRefuses(t *testing.T) {
	selection := newReleasedSelection(t)
	r := selection.admitShaped(t, thriftOperationImpactPackID, "claim-version", func(r *packrelease.PackRelease) { r.PackClaimVersion = "2.0.0" })
	bindReleaseLoad(t, r)
	cfg := &config.Config{}
	selection.bind(cfg)
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("unsupported Thrift operation Impact claim version admitted")
	}
}

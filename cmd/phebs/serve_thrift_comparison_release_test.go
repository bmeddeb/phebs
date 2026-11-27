package main

import (
	"slices"
	"strconv"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// TestThriftCallerComparisonIdentityFrozen pins the PHEBS-44 tuple: the pack
// identity, its claim version and the implementation identity a release binds.
func TestThriftCallerComparisonIdentityFrozen(t *testing.T) {
	if thriftCallerComparisonPackID != "phebs.thrift.caller.comparison" || thriftCallerComparisonClaimVersion != "1.2.0" {
		t.Fatalf("Thrift caller comparison identity = %s %s", thriftCallerComparisonPackID, thriftCallerComparisonClaimVersion)
	}
	want := "compare_operation_callers@" + api.CallerComparisonSchemaVersion + "+thrift-pairs"
	if releasedClaims[thriftCallerComparisonPackID] != want {
		t.Fatalf("Thrift comparison implementation identity = %q, want %q", releasedClaims[thriftCallerComparisonPackID], want)
	}
	if _, recipe := packRecipes[thriftCallerComparisonPackID]; recipe {
		t.Fatal("the Thrift comparison claim must not carry extractors of its own")
	}
}

// TestThriftCallerComparisonReleasedAdmission drives the real signed selection.
// The Thrift comparison claim admits Thrift pairs only beside the released
// Thrift Caller Map recipe, never alone, never through the experimental Thrift
// switch, and is withdrawn by revocation, suspension and expiry; it neither
// needs nor enables the protobuf comparison claim.
func TestThriftCallerComparisonReleasedAdmission(t *testing.T) {
	grpcFamily := []string{callerMapPackID, callerComparisonPackID}
	for _, test := range []struct {
		name                    string
		packs                   []string
		revoke, suspend, expire string
		experimentalThrift      bool
		wantThrift, wantGRPC    bool
		wantCapability          bool
	}{
		{name: "dark"},
		{name: "joint admission", packs: []string{thriftCallerComparisonPackID, thriftCallerMapPackID}, wantThrift: true, wantCapability: true},
		{name: "beside the Thrift Atlas", packs: []string{thriftCallerComparisonPackID, thriftCallerMapPackID, thriftContractPackID}, wantThrift: true, wantCapability: true},
		{name: "claim alone", packs: []string{thriftCallerComparisonPackID}},
		{name: "Thrift Caller Map alone", packs: []string{thriftCallerMapPackID}},
		{name: "claim beside only the protobuf Caller Map", packs: []string{thriftCallerComparisonPackID, callerMapPackID}},
		{name: "beside the protobuf comparison family", packs: append([]string{thriftCallerComparisonPackID, thriftCallerMapPackID}, grpcFamily...), wantThrift: true, wantGRPC: true, wantCapability: true},
		{name: "protobuf family without the Thrift claim", packs: append([]string{thriftCallerMapPackID}, grpcFamily...), wantGRPC: true, wantCapability: true},
		{name: "claim revoked", packs: []string{thriftCallerComparisonPackID, thriftCallerMapPackID}, revoke: thriftCallerComparisonPackID},
		{name: "Thrift Caller Map revoked", packs: []string{thriftCallerComparisonPackID, thriftCallerMapPackID}, revoke: thriftCallerMapPackID},
		{name: "claim suspended", packs: []string{thriftCallerComparisonPackID, thriftCallerMapPackID}, suspend: thriftCallerComparisonPackID},
		{name: "claim expired", packs: []string{thriftCallerComparisonPackID, thriftCallerMapPackID}, expire: thriftCallerComparisonPackID},
		{name: "experimental Thrift is no released dependency", packs: []string{thriftCallerComparisonPackID}, experimentalThrift: true, wantCapability: true},
		{name: "released claim governs beside experimental Thrift", packs: []string{thriftCallerComparisonPackID, thriftCallerMapPackID}, experimentalThrift: true, wantThrift: true, wantCapability: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			var records []*packrelease.PackRelease
			for index, pack := range test.packs {
				r := selection.admitShaped(t, pack, "thrift-comparison-key-"+strconv.Itoa(index), func(record *packrelease.PackRelease) {
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
			served := serveCallerSurfaces(t, cfg)
			if served.thriftComparisonReleased != test.wantThrift || served.comparisonReleased != test.wantGRPC {
				t.Fatalf("released comparison = Thrift %t protobuf %t, want %t %t (%+v)",
					served.thriftComparisonReleased, served.comparisonReleased, test.wantThrift, test.wantGRPC, served)
			}
			if got := slices.Contains(served.capabilities, "contract-caller-comparison"); got != test.wantCapability || served.comparisonTool != test.wantCapability {
				t.Fatalf("comparison capability %t / tool %t, want %t (%v)", got, served.comparisonTool, test.wantCapability, served.capabilities)
			}
			if !test.experimentalThrift && (slices.Contains(served.capabilities, "contract-impact-report") || served.proofTool) {
				t.Fatalf("Thrift comparison enabled the experimental proof surface: %v", served.capabilities)
			}
			if slices.Contains(served.capabilities, "contract-impact-operation") && !slices.Contains(test.packs, operationImpactPackID) {
				t.Fatalf("Thrift comparison enabled operation Impact: %v", served.capabilities)
			}
		})
	}
}

func TestThriftCallerComparisonUnsupportedClaimVersionRefuses(t *testing.T) {
	selection := newReleasedSelection(t)
	r := selection.admitShaped(t, thriftCallerComparisonPackID, "claim-version", func(r *packrelease.PackRelease) { r.PackClaimVersion = "2.0.0" })
	bindReleaseLoad(t, r)
	cfg := &config.Config{}
	selection.bind(cfg)
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("unsupported Thrift caller comparison claim version admitted")
	}
}

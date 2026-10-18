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

func TestOperationImpactReleasedAdmission(t *testing.T) {
	if releasedClaims[operationImpactPackID] != "build_operation_impact_report@"+api.OperationImpactSchemaVersion {
		t.Fatal("operation claim identity changed")
	}
	for _, test := range []struct {
		name                    string
		packs                   []string
		revoke, suspend, expire string
		experimental, want      bool
	}{
		{name: "dark"},
		{name: "Caller Map alone", packs: []string{callerMapPackID}},
		{name: "Impact claim alone", packs: []string{operationImpactPackID}},
		{name: "joint recipe", packs: []string{operationImpactPackID, callerMapPackID}, want: true},
		{name: "claim revoked", packs: []string{operationImpactPackID, callerMapPackID}, revoke: operationImpactPackID},
		{name: "caller revoked", packs: []string{operationImpactPackID, callerMapPackID}, revoke: callerMapPackID},
		{name: "claim suspended", packs: []string{operationImpactPackID, callerMapPackID}, suspend: operationImpactPackID},
		{name: "caller expired", packs: []string{operationImpactPackID, callerMapPackID}, expire: callerMapPackID},
		{name: "provisional cannot substitute", packs: []string{operationImpactPackID}, experimental: true},
		{name: "mixed keeps stable view", packs: []string{operationImpactPackID, callerMapPackID}, experimental: true, want: true},
		{name: "Atlas withdrawal independent", packs: []string{operationImpactPackID, callerMapPackID, protobufContractPackID}, revoke: protobufContractPackID, want: true},
		{name: "other product coexistence", packs: []string{operationImpactPackID, callerMapPackID, protobufFieldPackID, callerComparisonPackID}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			var records []*packrelease.PackRelease
			for index, pack := range test.packs {
				r := selection.admitShaped(t, pack, "impact-key-"+strconv.Itoa(index), func(record *packrelease.PackRelease) {
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
			cfg.Experimental.ProvisionalProtoExtraction = test.experimental
			if len(records) > 0 {
				bindReleaseLoad(t, records[0], records[1:]...)
				selection.bind(cfg)
				for _, record := range records {
					if record.PackID == test.revoke {
						cfg.ReleaseSelection.Revoked = append(cfg.ReleaseSelection.Revoked, record.ReleaseID)
					}
				}
			}
			got := serveCallerSurfaces(t, cfg)
			if got.operationImpactTool != test.want || slices.Contains(got.capabilities, "contract-impact-operation") != test.want {
				t.Fatalf("surfaces=%+v", got)
			}
			if !test.experimental && slices.Contains(got.capabilities, "contract-impact-report") {
				t.Fatal("stable claim activated experimental v2")
			}
		})
	}
}

func TestOperationImpactCurrentDependencyVersions(t *testing.T) {
	base := func() *serveDeps {
		return &serveDeps{releasedClaims: map[string]bool{operationImpactPackID: true}, releasedDomains: map[string]bool{"proto-contract": true, "grpc-caller": true}, exs: callerMapRecipe()}
	}
	if !releasedOperationImpact(base()) {
		t.Fatal("current actual recipe refused")
	}
	for _, domain := range []string{"proto-contract", "grpc-caller"} {
		d := base()
		for index, ex := range d.exs {
			if ex.Domain() == domain {
				d.exs[index] = stubExtractor{domain: domain, version: "old"}
			}
		}
		if releasedOperationImpact(d) {
			t.Fatalf("old %s admitted", domain)
		}
	}
	d := base()
	d.exs = []extract.Extractor{callerMapRecipe()[0]}
	if releasedOperationImpact(d) {
		t.Fatal("missing actual caller admitted")
	}
	selection := newReleasedSelection(t)
	r := selection.admitShaped(t, operationImpactPackID, "claim-version", func(r *packrelease.PackRelease) { r.PackClaimVersion = "2.0.0" })
	bindReleaseLoad(t, r)
	cfg := &config.Config{}
	selection.bind(cfg)
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("unsupported Impact claim version admitted")
	}
}

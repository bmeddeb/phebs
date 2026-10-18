package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// The real recipe registry and signed selection govern these starts. Only the
// fixture binary/artifact bindings are substituted, as in the startup matrix.
func TestProtobufAtlasReleasedAdmission(t *testing.T) {
	if protobufContractPackID != "phebs.protobuf.contract" {
		t.Fatal("protobuf Atlas pack identity changed")
	}
	recipe, bound := packRecipes[protobufContractPackID]
	if !bound {
		t.Fatal("protobuf Atlas recipe is not bound")
	}
	if extractors := recipe(); len(extractors) != 1 || extractors[0].Domain() != "proto-contract" || extractors[0].Version() != "4.0.0" {
		t.Fatalf("protobuf Atlas must admit only proto-contract@4.0.0: %v", extractors)
	}
	for _, test := range []struct {
		name        string
		alongside   string
		shape       func(*packrelease.PackRelease)
		revokeAtlas bool
		revokeOther bool
		wantDomains []string
		want        servedCallerSurfaces
	}{
		{
			name:        "declarations alone",
			wantDomains: []string{"proto-contract"},
			want:        servedCallerSurfaces{capabilities: []string{"contract-atlas", "service-catalog-v2"}, atlasTools: 2},
		},
		{
			name: "revoked", revokeAtlas: true,
			want: servedCallerSurfaces{capabilities: []string{"service-catalog-v2"}},
		},
		{
			name: "expired", shape: expiredValidation,
			want: servedCallerSurfaces{capabilities: []string{"service-catalog-v2"}},
		},
		{
			name: "suspended", shape: func(release *packrelease.PackRelease) { release.DerivedStatus = packrelease.StatusSuspended },
			want: servedCallerSurfaces{capabilities: []string{"service-catalog-v2"}},
		},
		{
			name: "Caller Map shares declarations once", alongside: callerMapPackID,
			wantDomains: []string{"grpc-caller", "proto-contract"},
			want: servedCallerSurfaces{
				capabilities: []string{"contract-atlas", "contract-caller-map", "service-catalog-v2"},
				atlasTools:   2, callerMapTool: true, callerMapEnabled: true, callerMapServiced: true,
			},
		},
		{
			name: "Caller Map withdrawal preserves standalone Atlas", alongside: callerMapPackID, revokeOther: true,
			wantDomains: []string{"proto-contract"},
			want:        servedCallerSurfaces{capabilities: []string{"contract-atlas", "service-catalog-v2"}, atlasTools: 2},
		},
		{
			name: "Atlas withdrawal preserves Caller Map discovery", alongside: callerMapPackID, revokeAtlas: true,
			wantDomains: []string{"grpc-caller", "proto-contract"},
			want: servedCallerSurfaces{
				capabilities: []string{"contract-atlas", "contract-caller-map", "service-catalog-v2"},
				atlasTools:   2, callerMapTool: true, callerMapEnabled: true, callerMapServiced: true,
			},
		},
		{
			name: "Thrift and protobuf stay separate", alongside: thriftContractPackID,
			wantDomains: []string{"proto-contract", "thrift-contract"},
			want:        servedCallerSurfaces{capabilities: []string{"contract-atlas", "service-catalog-v2"}, atlasTools: 2},
		},
		{
			name: "protobuf withdrawal preserves Thrift", alongside: thriftContractPackID, revokeAtlas: true,
			wantDomains: []string{"thrift-contract"},
			want:        servedCallerSurfaces{capabilities: []string{"contract-atlas", "service-catalog-v2"}, atlasTools: 2},
		},
		{
			name: "Atlas withdrawal preserves Kafka", alongside: kafkaProducerPackID, revokeAtlas: true,
			wantDomains: []string{"kafka-producer"},
			want:        servedCallerSurfaces{capabilities: []string{"kafka-topic-usage", "service-catalog-v2"}, topicTool: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			shape := test.shape
			if shape == nil {
				shape = func(*packrelease.PackRelease) {}
			}
			atlas := selection.admitShaped(t, protobufContractPackID, "atlas-key", shape)
			var others []*packrelease.PackRelease
			if test.alongside != "" {
				others = append(others, selection.admitShaped(t, test.alongside, "other-key", sharingBinding(atlas)))
			}
			bindReleaseLoad(t, atlas, others...)
			cfg := &config.Config{}
			cfg.Server.DataDir = t.TempDir()
			selection.bind(cfg)
			if test.revokeAtlas {
				cfg.ReleaseSelection.Revoked = append(cfg.ReleaseSelection.Revoked, atlas.ReleaseID)
			}
			if test.revokeOther {
				cfg.ReleaseSelection.Revoked = append(cfg.ReleaseSelection.Revoked, others[0].ReleaseID)
			}
			deps := &serveDeps{ctx: t.Context(), cfg: cfg}
			if err := newServeExtractionRegistries(deps); err != nil {
				t.Fatal(err)
			}
			var domains []string
			for _, extractor := range deps.exs {
				domains = append(domains, extractor.Domain())
			}
			slices.Sort(domains)
			if !slices.Equal(domains, test.wantDomains) {
				t.Fatalf("registered domains = %v, want %v", domains, test.wantDomains)
			}
			if got := serveCallerSurfaces(t, cfg); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("API/MCP surface = %+v, want %+v", got, test.want)
			}
			// The declaration-only selection never enables a caller adapter.
			if deps.callerRegistry.Enabled() != test.want.callerMapEnabled {
				t.Fatalf("caller registry enabled = %v", deps.callerRegistry.Enabled())
			}
		})
	}
}

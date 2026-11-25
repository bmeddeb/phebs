package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/packrelease"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// serveChangeImpactTools drives the production assembly once and reports the
// API capabilities plus whether MCP lists check_contract_compatibility.
func serveChangeImpactTools(t *testing.T, cfg *config.Config) ([]string, bool, api.Options) {
	t.Helper()
	t.Setenv("PHEBS_CONTRACT_ATLAS_FIXTURE", "")
	deps := &serveDeps{ctx: t.Context(), cfg: cfg, compatibility: stubCompatibility{}}
	if err := newServeExtractionRegistries(deps); err != nil {
		t.Fatal(err)
	}
	if err := bindServeCallerReader(deps); err != nil {
		t.Fatal(err)
	}
	if len(deps.exs) > 0 {
		deps.evidenceView, deps.proofBundles = deps.st, deps.st
	}
	apiOpts, err := newServeAPIOptions(deps)
	if err != nil {
		t.Fatal(err)
	}
	versionOpts := apiOpts
	versionOpts.Principal = func(context.Context) string { return "user:phebs41" }
	recorder := httptest.NewRecorder()
	api.New(versionOpts).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	var version struct {
		Capabilities []string `json:"capabilities"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &version) != nil {
		t.Fatalf("GET /api/version = %d: %s", recorder.Code, recorder.Body)
	}
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	server := newServeMCPServer(deps, apiOpts)
	go func() { _, _ = server.Connect(t.Context(), serverTransport, nil) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "phebs41-surfaces", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tool := false
	for _, listedTool := range listed.Tools {
		tool = tool || listedTool.Name == "check_contract_compatibility"
	}
	return version.Capabilities, tool, apiOpts
}

func TestChangeImpactReleasedAdmission(t *testing.T) {
	if releasedClaims[changeImpactPackID] != "build_contract_change_impact@"+api.ContractImpactReportSchemaVersion+"+buf-"+compat.Version+"-"+compat.Policy {
		t.Fatal("change Impact claim identity changed")
	}
	all := []string{changeImpactPackID, wireCompatibilityPackID, protobufFieldPackID}
	for _, test := range []struct {
		name                    string
		packs                   []string
		revoke, suspend, expire string
		experimental, want      bool
	}{
		{name: "dark"},
		{name: "claim alone", packs: []string{changeImpactPackID}},
		{name: "claim and wire without fields", packs: []string{changeImpactPackID, wireCompatibilityPackID}},
		{name: "claim and fields without wire", packs: []string{changeImpactPackID, protobufFieldPackID}},
		{name: "wire and fields without claim", packs: []string{wireCompatibilityPackID, protobufFieldPackID}},
		{name: "claim with Thrift fields only", packs: []string{changeImpactPackID, wireCompatibilityPackID, thriftRWFieldPackID}},
		{name: "joint claim", packs: all, want: true},
		{name: "joint claim beside Thrift fields", packs: append(slices.Clone(all), thriftRWFieldPackID), want: true},
		{name: "claim revoked", packs: all, revoke: changeImpactPackID},
		{name: "wire revoked", packs: all, revoke: wireCompatibilityPackID},
		{name: "fields revoked", packs: all, revoke: protobufFieldPackID},
		{name: "claim suspended", packs: all, suspend: changeImpactPackID},
		{name: "wire expired", packs: all, expire: wireCompatibilityPackID},
		{name: "provisional cannot substitute", packs: []string{changeImpactPackID}, experimental: true},
		{name: "provisional keeps the experimental surface", packs: all, experimental: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			var records []*packrelease.PackRelease
			for index, pack := range test.packs {
				r := selection.admitShaped(t, pack, "change-key-"+strconv.Itoa(index), func(record *packrelease.PackRelease) {
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
			capabilities, tool, apiOpts := serveChangeImpactTools(t, cfg)
			if apiOpts.ChangeImpactReleased != test.want || slices.Contains(capabilities, "contract-impact-change") != test.want {
				t.Fatalf("released=%t capabilities=%v", apiOpts.ChangeImpactReleased, capabilities)
			}
			if test.experimental {
				// The provisional switches keep the experimental proof surface,
				// which already serves compatibility; the released claim adds nothing.
				if !slices.Contains(capabilities, "contract-impact-report") || !tool {
					t.Fatalf("experimental surface lost: %v tool=%t", capabilities, tool)
				}
				return
			}
			if slices.Contains(capabilities, "contract-impact-report") {
				t.Fatal("released claim activated the experimental report")
			}
			if slices.Contains(capabilities, "contract-compatibility") != test.want || tool != test.want {
				t.Fatalf("compatibility capability/tool = %v/%t, want %t", capabilities, tool, test.want)
			}
		})
	}
}

func TestChangeImpactUnsupportedClaimVersionRefuses(t *testing.T) {
	selection := newReleasedSelection(t)
	r := selection.admitShaped(t, changeImpactPackID, "claim-version", func(r *packrelease.PackRelease) { r.PackClaimVersion = "2.0.0" })
	bindReleaseLoad(t, r)
	cfg := &config.Config{}
	selection.bind(cfg)
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("unsupported change Impact claim version admitted")
	}
}

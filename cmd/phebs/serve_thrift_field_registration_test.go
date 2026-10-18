package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/packrelease"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReleasedThriftFieldRegistration(t *testing.T) {
	for _, test := range []struct {
		name, version string
		packs         []string
		withdraw      string
		badClaim      string
		provisional   bool
	}{
		{name: "none"},
		{name: "Apache", packs: []string{apacheThriftFieldPackID}, version: thriftfield.CommittedApacheVersion},
		{name: "RW", packs: []string{thriftRWFieldPackID}, version: thriftfield.CommittedThriftRWVersion},
		{name: "both", packs: []string{apacheThriftFieldPackID, thriftRWFieldPackID}, version: thriftfield.CommittedApacheThriftRWVersion},
		{name: "withdraw Apache preserves RW", packs: []string{apacheThriftFieldPackID, thriftRWFieldPackID}, withdraw: apacheThriftFieldPackID, version: thriftfield.CommittedThriftRWVersion},
		{name: "withdraw RW preserves Apache", packs: []string{apacheThriftFieldPackID, thriftRWFieldPackID}, withdraw: thriftRWFieldPackID, version: thriftfield.CommittedApacheVersion},
		{name: "withdraw only recipe", packs: []string{apacheThriftFieldPackID}, withdraw: apacheThriftFieldPackID},
		{name: "wrong Apache claim", packs: []string{apacheThriftFieldPackID}, badClaim: apacheThriftFieldPackID},
		{name: "wrong RW claim", packs: []string{thriftRWFieldPackID}, badClaim: thriftRWFieldPackID},
		{name: "both with proto and Kafka", packs: []string{apacheThriftFieldPackID, thriftRWFieldPackID, protobufFieldPackID, kafkaProducerPackID}, version: thriftfield.CommittedApacheThriftRWVersion},
		{name: "mixed provisional", packs: []string{apacheThriftFieldPackID}, version: thriftfield.CommittedApacheVersion, provisional: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			var records []*packrelease.PackRelease
			for index, pack := range test.packs {
				record := selection.admitShaped(t, pack, "t523-key-"+strconv.Itoa(index), func(r *packrelease.PackRelease) {
					if len(records) > 0 {
						sharingBinding(records[0])(r)
					}
					if pack == test.withdraw {
						r.DerivedStatus = packrelease.StatusSuspended
					}
					if pack == test.badClaim {
						r.PackClaimVersion = "2.0.0"
					}
				})
				records = append(records, record)
			}
			if len(records) > 0 {
				bindReleaseLoad(t, records[0], records[1:]...)
			}
			cfg := &config.Config{}
			cfg.Server.DataDir = t.TempDir()
			if len(records) > 0 {
				selection.bind(cfg)
			}
			if test.provisional {
				cfg.Experimental.ProvisionalProtoExtraction = true
				cfg.Experimental.ProvisionalThriftExtraction = true
				cfg.Experimental.ProvisionalThriftFieldExtraction = true
			}
			deps := &serveDeps{ctx: t.Context(), cfg: cfg, compatibility: stubCompatibility{}}
			err := newServeExtractionRegistries(deps)
			if test.badClaim != "" {
				if err == nil {
					t.Fatal("unsupported claim admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, ex := range deps.exs {
				if ex.Domain() == "scip-thrift-field" {
					count++
					if ex.Version() != test.version {
						t.Fatalf("version=%s want=%s", ex.Version(), test.version)
					}
				}
			}
			if (count == 1) != (test.version != "") || count > 1 {
				t.Fatalf("shared domain count=%d", count)
			}
			if test.provisional {
				var domains []string
				for _, ex := range deps.exs {
					domains = append(domains, ex.Domain())
				}
				for _, want := range []string{"proto-contract", "grpc-consumer", "scip-proto-field", "thrift-contract", "thrift-consumer"} {
					if !slices.Contains(domains, want) {
						t.Fatalf("provisional domain %s lost", want)
					}
				}
			}
			deps.evidenceView, deps.proofBundles = deps.st, deps.st
			opts, err := newServeAPIOptions(deps)
			if err != nil {
				t.Fatal(err)
			}
			admitted := test.version != ""
			if opts.ThriftFieldReferencesReleased != (admitted && !test.provisional) || opts.EvidenceExtractorVersions["scip-thrift-field"] != test.version {
				t.Fatal("Thrift admission/version lost")
			}
			if !admitted {
				return
			}
			proto := test.provisional || slices.Contains(test.packs, protobufFieldPackID)
			opts.Principal = func(context.Context) string { return "user:t523" }
			handler := api.New(opts)
			for _, route := range []struct {
				path    string
				present bool
			}{
				{"/api/find_field_references", true},
				{"/api/find_proto_field_references", proto},
				{"/api/find_operation_consumers", test.provisional},
				{"/api/get_extraction_coverage", test.provisional},
			} {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, route.path, nil))
				if (rec.Code != http.StatusNotFound) != route.present {
					t.Fatalf("route=%s status=%d", route.path, rec.Code)
				}
			}
			serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
			server := newServeMCPServer(deps, opts)
			go func() { _, _ = server.Connect(t.Context(), serverTransport, nil) }()
			client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t523", Version: "1"}, nil)
			session, err := client.Connect(t.Context(), clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			tools, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, tool := range tools.Tools {
				names = append(names, tool.Name)
			}
			if !slices.Contains(names, "find_field_references") || slices.Contains(names, "find_proto_field_references") != proto || slices.Contains(names, "find_operation_consumers") != test.provisional {
				t.Fatalf("tools=%v", names)
			}
		})
	}
}

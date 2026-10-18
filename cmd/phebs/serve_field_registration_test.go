package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/packrelease"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReleasedProtobufFieldRegistration(t *testing.T) {
	for _, test := range []struct {
		name, alongside   string
		shape             func(*packrelease.PackRelease)
		configure         func(*config.Config)
		admitted, refused bool
		provisional       bool
	}{
		{name: "field only", admitted: true},
		{name: "with independently released Kafka", alongside: kafkaProducerPackID, admitted: true},
		{name: "with independent Caller Map", alongside: callerMapPackID, admitted: true},
		{name: "with provisional proto", admitted: true, provisional: true, configure: func(cfg *config.Config) {
			cfg.Experimental.ProvisionalProtoExtraction = true
		}},
		{name: "with provisional Thrift and fields", admitted: true, provisional: true, configure: func(cfg *config.Config) {
			cfg.Experimental.ProvisionalThriftExtraction = true
			cfg.Experimental.ProvisionalThriftFieldExtraction = true
		}},
		{name: "suspended", shape: func(r *packrelease.PackRelease) { r.DerivedStatus = packrelease.StatusSuspended }},
		{name: "expired", shape: expiredValidation},
		{name: "unadmitted claim version", shape: func(r *packrelease.PackRelease) { r.PackClaimVersion = "2.0.0" }, refused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			shape := test.shape
			if shape == nil {
				shape = func(*packrelease.PackRelease) {}
			}
			field := selection.admitShaped(t, protobufFieldPackID, "field-key", shape)
			var other []*packrelease.PackRelease
			if test.alongside != "" {
				other = append(other, selection.admitShaped(t, test.alongside, "other-key", sharingBinding(field)))
			}
			bindReleaseLoad(t, field, other...)
			cfg := &config.Config{}
			cfg.Server.DataDir = t.TempDir()
			if test.configure != nil {
				test.configure(cfg)
			}
			selection.bind(cfg)
			deps := &serveDeps{ctx: t.Context(), cfg: cfg, compatibility: stubCompatibility{}}
			err := newServeExtractionRegistries(deps)
			if test.refused {
				if err == nil {
					t.Fatal("unsupported signed claim version admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var domains []string
			fieldCount := 0
			for _, ex := range deps.exs {
				domains = append(domains, ex.Domain())
				if ex.Domain() == "scip-proto-field" {
					fieldCount++
					if ex.Version() != "1.4.0-released.1" {
						t.Fatal("provisional field version reused")
					}
				}
			}
			if slices.Contains(domains, "scip-proto-field") != test.admitted {
				t.Fatalf("domains=%v", domains)
			}
			if test.admitted && fieldCount != 1 {
				t.Fatalf("signed field domain registered %d times", fieldCount)
			}
			if test.provisional {
				for _, dark := range evidenceExtractors(cfg.Experimental.ProvisionalProtoExtraction, cfg.Experimental.ProvisionalThriftExtraction, cfg.Experimental.ProvisionalThriftFieldExtraction, false) {
					if !slices.Contains(domains, dark.Domain()) {
						t.Fatalf("independently selected provisional domain lost %s", dark.Domain())
					}
				}
			} else {
				for _, unrelated := range []string{"grpc-consumer", "scip-thrift-field", "thrift-consumer"} {
					if slices.Contains(domains, unrelated) {
						t.Fatalf("unrelated domain admitted %s", unrelated)
					}
				}
			}
			if len(deps.exs) > 0 {
				deps.evidenceView, deps.proofBundles = deps.st, deps.st
			}
			opts, err := newServeAPIOptions(deps)
			if err != nil {
				t.Fatal(err)
			}
			if opts.ProtobufFieldReferencesReleased != (test.admitted && !test.provisional) {
				t.Fatal("field service admission differs")
			}
			if test.provisional && opts.CatalogReleasedDomains != nil {
				t.Fatal("explicit provisional catalog was restricted by the signed field selection")
			}
			if test.admitted && opts.EvidenceExtractorVersions["scip-proto-field"] != "1.4.0-released.1" {
				t.Fatal("field read version fence absent")
			}
			if !test.admitted {
				return
			}
			opts.Principal = func(context.Context) string { return "user:t51" }
			handler := api.New(opts)
			for _, route := range []struct {
				path        string
				wantPresent bool
			}{
				{path: "/api/find_proto_field_references", wantPresent: true},
				{path: "/api/find_field_references", wantPresent: true},
				{path: "/api/find_operation_consumers", wantPresent: test.provisional},
				{path: "/api/get_extraction_coverage", wantPresent: test.provisional},
				{path: "/api/check_contract_compatibility", wantPresent: test.provisional},
			} {
				rec := httptest.NewRecorder()
				// POST exercises method routing without executing these GET
				// handlers against the unopened-store constructor stand-in.
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, route.path, nil))
				if (rec.Code != http.StatusNotFound) != route.wantPresent {
					t.Fatalf("route%s status%d body%s", route.path, rec.Code, rec.Body)
				}
			}
			serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
			server := newServeMCPServer(deps, opts)
			go func() { _, _ = server.Connect(t.Context(), serverTransport, nil) }()
			client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t51", Version: "1"}, nil)
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
			for _, name := range []string{"find_proto_field_references", "find_field_references"} {
				if !slices.Contains(names, name) {
					t.Fatalf("field tool missing%s", name)
				}
			}
			for _, name := range []string{"find_operation_consumers", "get_extraction_coverage", "check_contract_compatibility", "list_kafka_topics", "read_topic_inventory"} {
				if slices.Contains(names, name) != test.provisional {
					t.Fatalf("tool%s provisional admission differs", name)
				}
			}
			if slices.Contains(names, "find_kafka_topic_usage") != (test.provisional || test.alongside == kafkaProducerPackID) {
				t.Fatal("Kafka tool admission differs")
			}
		})
	}
}

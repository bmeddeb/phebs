package t4110

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	phebssearch "github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestProductHTTPJSONAdmitsOnlyHumaSchemaEnvelope(t *testing.T) {
	mux := http.NewServeMux()
	humaAPI := humago.New(mux, huma.DefaultConfig("t4110-test", "test"))
	type inventoryOutput struct{ Body api.ServiceInventory }
	type detailOutput struct{ Body api.ServiceDetail }
	type searchOutput struct{ Body *phebssearch.Result }
	inventory := api.ServiceInventory{
		SchemaVersion: "inventory-test",
		Services:      []api.Service{{Key: "alpha", DisplayName: "Alpha"}},
		Pagination:    api.ServicePagination{Order: "service_key:asc", PageSize: 1, Returned: 1},
	}
	detail := api.ServiceDetail{
		SchemaVersion: "detail-test",
		Service:       api.Service{Key: "alpha", DisplayName: "Alpha"},
		Successors:    []string{"beta"},
		Memberships:   []api.ServiceMembership{{Path: "alpha/main.go", Role: "primary", Origin: "base"}},
	}
	searchResult := phebssearch.Result{
		Files: []phebssearch.FileResult{{
			Repo: "example/repository", Path: "alpha/main.go",
			Chunks: []phebssearch.Chunk{{
				Content: "match\n", StartLine: 7,
				Ranges: []phebssearch.Range{{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 6}},
			}},
		}},
		Stats: phebssearch.Stats{MatchCount: 1, FileCount: 1, DurationMS: 2},
	}
	huma.Get(humaAPI, "/inventory", func(context.Context, *struct{}) (*inventoryOutput, error) {
		return &inventoryOutput{Body: inventory}, nil
	})
	huma.Get(humaAPI, "/detail", func(context.Context, *struct{}) (*detailOutput, error) {
		return &detailOutput{Body: detail}, nil
	})
	huma.Get(humaAPI, "/search", func(context.Context, *struct{}) (*searchOutput, error) {
		return &searchOutput{Body: &searchResult}, nil
	})
	decodedInventory, err := getProductJSON[api.ServiceInventory](t.Context(), mux, "/inventory")
	if err != nil || !reflect.DeepEqual(decodedInventory, inventory) {
		t.Fatalf("real Huma inventory = %+v, %v", decodedInventory, err)
	}
	decodedDetail, err := getProductJSON[api.ServiceDetail](t.Context(), mux, "/detail")
	if err != nil || !reflect.DeepEqual(decodedDetail, detail) {
		t.Fatalf("real Huma detail = %+v, %v", decodedDetail, err)
	}
	decodedSearch, err := getProductJSON[phebssearch.Result](t.Context(), mux, "/search")
	if err != nil || !reflect.DeepEqual(decodedSearch, searchResult) {
		t.Fatalf("real Huma search = %+v, %v", decodedSearch, err)
	}

	const schemaPath = "/schemas/TestResponse.json"
	const schemaLink = "http://127.0.0.1" + schemaPath
	const schemaField = `"$schema":"` + schemaLink + `"`
	invalid := []string{
		`{"status":"ok"}`,
		`{` + schemaField + `,` + schemaField + `,"status":"ok"}`,
		`{"$schema":"http://127.0.0.2/schemas/TestResponse.json","status":"ok"}`,
		`{"$schema":"http://127.0.0.1/schemas/OtherResponse.json","status":"ok"}`,
		`{"$schema":"http://127.0.0.1/schemas/TestResponse.json?","status":"ok"}`,
		`{"$schema":"http://127.0.0.1/schemas/TestResponse.json#","status":"ok"}`,
		`{` + schemaField + `,"status":"ok","extra":true}`,
		`{` + schemaField + `,"status":"ok","status":"ok"}`,
		`{` + schemaField + `,"status":"ok"} {}`,
		`true`,
	}
	for _, raw := range invalid {
		var value struct {
			Status string `json:"status"`
		}
		if err := decodeProductHTTPJSON([]byte(raw), schemaPath, &value); err == nil {
			t.Fatalf("invalid Huma response passed: %s", raw)
		}
	}
	for _, header := range [][]string{
		nil,
		{`<` + schemaPath + `>; rel="describedBy"`, `<` + schemaPath + `>; rel="describedBy"`},
		{`<https://127.0.0.1/schemas/TestResponse.json>; rel="describedBy"`},
		{`<http://127.0.0.2/schemas/TestResponse.json>; rel="describedBy"`},
		{`</schemas/private/TestResponse.json>; rel="describedBy"`},
		{`<` + schemaPath + `?>; rel="describedBy"`},
		{`<` + schemaPath + `#>; rel="describedBy"`},
		{`<` + schemaPath + `>; rel="alternate"`},
	} {
		if _, err := productResponseSchemaLink(header); err == nil {
			t.Fatalf("invalid Huma schema header passed: %v", header)
		}
	}
	var application struct {
		Status string `json:"status"`
	}
	if err := decodeProductJSON(
		[]byte(`{`+schemaField+`,"status":"ok"}`),
		&application,
	); err == nil {
		t.Fatal("MCP application decoder accepted HTTP-only schema metadata")
	}
}

func TestSelectedServiceComparisonUsesSemanticSlices(t *testing.T) {
	expected := servicecatalog.Service{
		Key: "alpha", DisplayName: "Alpha",
		Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
	}
	membership := servicecatalog.Membership{
		ServiceKey: expected.Key, Path: "alpha/main.go",
		Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
	}
	projectionService := expected
	projectionService.Successors = []string{}
	entry := store.ServiceStateEntry{
		State: servicecatalog.ServiceState{
			ServiceKey: expected.Key, DisplayName: expected.DisplayName,
			Disposition: expected.Disposition, Origin: expected.Origin,
			Status: servicecatalog.StatusCurrent, Successors: []string{},
		},
		Projection: &servicecatalog.ServiceProjection{
			Service: projectionService, Memberships: []servicecatalog.Membership{membership},
		},
	}
	if !selectedServiceValuesMatch(entry, expected, []servicecatalog.Membership{membership}) ||
		!sameCatalogService(expected, projectionService) {
		t.Fatal("nil and empty successor slices were treated as different values")
	}
	changed := projectionService
	changed.Successors = []string{"beta"}
	if sameCatalogService(expected, changed) {
		t.Fatal("a real successor change was ignored")
	}

	left := servicecatalog.Catalog{Services: []servicecatalog.Service{
		expected,
		{Key: "beta", DisplayName: "Beta", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
	}}
	right := cloneCatalog(left)
	right.Services[0].Successors = []string{}
	right.Services[1].DisplayName = "Beta changed"
	key, err := firstCatalogServiceDifference(left, right)
	if err != nil || key != "beta" {
		t.Fatalf("first semantic service difference = %q, %v", key, err)
	}
}

func TestT4110EmptyRelationshipInputsAreValidAndDeterministic(t *testing.T) {
	repository := "neutral.invalid/t4110/relationship"
	commit := strings.Repeat("a", 40)
	first, err := publishEmptyRelationshipComponents(
		t.Context(),
		t.TempDir(),
		repository,
		commit,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := publishEmptyRelationshipComponents(
		t.Context(),
		t.TempDir(),
		repository,
		commit,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.resolver.Root().Digest != second.resolver.Root().Digest ||
		first.rpc.Root().Digest != second.rpc.Root().Digest ||
		first.kafka.Root().Digest != second.kafka.Root().Digest ||
		first.upstream.Digest != second.upstream.Digest {
		t.Fatal("empty relationship inputs are not deterministic")
	}
	if err := first.rpc.WalkPostings(context.Background(), func(_ rpccallerposting.Posting) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

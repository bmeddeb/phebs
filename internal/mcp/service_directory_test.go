package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

type serviceDirectoryToolFixture struct {
	listCalls   int
	detailCalls int
}

func (fixture *serviceDirectoryToolFixture) List(
	_ context.Context,
	query api.ServiceInventoryQuery,
	pageSize int,
	cursor string,
) (*api.ServiceInventory, error) {
	fixture.listCalls++
	return &api.ServiceInventory{
		SchemaVersion: "phebs-service-inventory-v1",
		Filters:       query,
		Services: []api.Service{{
			Repository: query.Repository, Key: "orders", DisplayName: "Orders",
			Disposition: "accepted", Origin: "base", Incarnation: 1,
			Status: "unavailable", MembershipCount: 1, DistinctPathCount: 1,
		}},
		Pagination: api.ServicePagination{
			Order: "service_key:asc", PageSize: pageSize,
			Returned: 1, NextCursor: cursor,
		},
	}, nil
}

func (fixture *serviceDirectoryToolFixture) Detail(
	_ context.Context,
	repository, serviceKey string,
) (*api.ServiceDetail, error) {
	fixture.detailCalls++
	return &api.ServiceDetail{
		SchemaVersion: "phebs-service-detail-v1",
		Service: api.Service{
			Repository: repository, Key: serviceKey, DisplayName: "Orders",
			Disposition: "accepted", Origin: "base", Incarnation: 1,
			Status: "unavailable", MembershipCount: 1, DistinctPathCount: 1,
		},
		Successors: []string{},
		Memberships: []api.ServiceMembership{{
			Path: "services/orders", Role: "primary", Origin: "base",
		}},
	}, nil
}

func TestServiceDirectoryToolsShareResponsesAndSchemas(t *testing.T) {
	fixture := &serviceDirectoryToolFixture{}
	server := NewServer(Options{
		Version: "test", ServiceDirectory: fixture,
	})
	listed, listResult := callTool[api.ServiceInventory](t, server, "list_services", map[string]any{
		"repository": "example.com/acme/mono", "page_size": 17, "cursor": "next",
	})
	if listResult.IsError || len(listed.Services) != 1 || listed.Services[0].Key != "orders" ||
		listed.Pagination.PageSize != 17 || listed.Pagination.NextCursor != "next" {
		t.Fatalf("list_services = %+v, error=%t", listed, listResult.IsError)
	}
	detail, detailResult := callTool[api.ServiceDetail](t, server, "get_service", map[string]any{
		"repository": "example.com/acme/mono", "service_key": "orders",
	})
	if detailResult.IsError || detail.Service != listed.Services[0] ||
		len(detail.Memberships) != 1 || detail.Memberships[0].Path != "services/orders" {
		t.Fatalf("get_service = %+v, error=%t", detail, detailResult.IsError)
	}
	if fixture.listCalls != 1 || fixture.detailCalls != 1 {
		t.Fatalf("shared service calls = list %d detail %d", fixture.listCalls, fixture.detailCalls)
	}

	tools := listServiceDirectoryTools(t, server)
	for name, typ := range map[string]reflect.Type{
		"list_services": reflect.TypeOf(api.ServiceInventory{}),
		"get_service":   reflect.TypeOf(api.ServiceDetail{}),
	} {
		tool := tools[name]
		if tool == nil {
			t.Fatalf("tool %q is absent", name)
		}
		encoded, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range recursiveJSONFields(typ, nil) {
			if !strings.Contains(string(encoded), `"`+field+`"`) {
				t.Errorf("%s output schema omitted shared field %q: %s", name, field, encoded)
			}
		}
	}
	listSchema, _ := json.Marshal(tools["list_services"].OutputSchema)
	detailSchema, _ := json.Marshal(tools["get_service"].OutputSchema)
	if strings.Contains(string(listSchema), `"memberships"`) ||
		!strings.Contains(string(detailSchema), `"memberships"`) {
		t.Fatalf("list/detail path boundary mismatch: list=%s detail=%s", listSchema, detailSchema)
	}
}

func TestServiceDirectoryToolsRemainDarkWithoutSharedService(t *testing.T) {
	tools := listServiceDirectoryTools(t, NewServer(Options{Version: "test"}))
	if tools["list_services"] != nil || tools["get_service"] != nil {
		t.Fatalf("dark server exposed service tools: %v", tools)
	}
}

func listServiceDirectoryTools(
	t *testing.T,
	server *sdk.Server,
) map[string]*sdk.Tool {
	t.Helper()
	ctx := t.Context()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _, _ = server.Connect(ctx, serverTransport, nil) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*sdk.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func recursiveJSONFields(typ reflect.Type, seen map[reflect.Type]bool) []string {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeOf(api.ServiceRepository{}.PublishedAt) {
		return nil
	}
	if seen == nil {
		seen = make(map[reflect.Type]bool)
	}
	if seen[typ] {
		return nil
	}
	seen[typ] = true
	var fields []string
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
		fields = append(fields, recursiveJSONFields(field.Type, seen)...)
	}
	return fields
}

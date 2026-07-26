package t201

import (
	"fmt"
	"strings"
)

type callSpec struct {
	id, protocol, operation, path, needle string
	occurrence                            int
	role                                  string
	resolution                            Resolution
	reason, symbol                        string
}

func smallOracle(files map[string][]byte) (Oracle, error) {
	specs := []callSpec{
		{"proto-checkout", "grpc", "/synthetic.orders.v1.Orders/Get", "src/checkout/caller.go", "client.Get", 0, "production", ResolutionKnown, "scip_symbol_to_generated_anchor", ProtoGetSymbol},
		{"proto-alias", "grpc", "/synthetic.orders.v1.Orders/Get", "src/alias/caller.go", "c.Get", 0, "production", ResolutionKnown, "scip_symbol_to_generated_anchor", ProtoGetSymbol},
		{"proto-embedded", "grpc", "/synthetic.orders.v1.Orders/Get", "src/embedded/caller.go", "c.Get", 0, "production", ResolutionKnown, "scip_symbol_to_generated_anchor", ProtoGetSymbol},
		{"proto-wrapper", "grpc", "/synthetic.orders.v1.Orders/Get", "src/wrapper/client.go", "c.Get", 0, "production", ResolutionKnown, "scip_symbol_to_generated_anchor", ProtoGetSymbol},
		{"thrift-ledger", "thrift", "/ledger.Ledger/get", "src/thrift/caller.go", "client.Get", 0, "production", ResolutionKnown, "scip_symbol_to_generated_anchor", ThriftGetSymbol},
		{"proto-collision", "grpc", "", "src/collision/caller.go", "client.Get", 0, "production", ResolutionUnresolved, "multiple_generated_symbols", ""},
		{"proto-test", "grpc", "", "src/checkout/caller_test.go", "client.Get", 0, "test", ResolutionUnresolved, "no_typed_occurrence", ""},
		{"proto-mock", "grpc", "", "src/checkout/mock_client.go", "m.Get", 0, "mock", ResolutionUnresolved, "mock_receiver", ""},
		{"proto-vendor", "grpc", "", "vendor/example/generated_call.go", "client.Get", 0, "vendor", ResolutionUnresolved, "vendored_source", ""},
		{"dynamic-call", "unknown", "", "src/dynamic/caller.go", "method", 1, "production", ResolutionUnresolved, "dynamic_dispatch", ""},
	}
	oracle := Oracle{Profile: SmallProfileName}
	for _, spec := range specs {
		content, ok := files[spec.path]
		if !ok {
			return Oracle{}, fmt.Errorf("oracle path %s is absent", spec.path)
		}
		call, err := locateCall(spec.id, spec.protocol, spec.operation, spec.path,
			content, spec.needle, spec.occurrence, spec.role, spec.resolution,
			spec.reason, spec.symbol)
		if err != nil {
			return Oracle{}, err
		}
		oracle.Calls = append(oracle.Calls, call)
	}
	oracle.GeneratedFrom = []GeneratedFromExpectation{
		{
			GeneratedPath:      "gen/proto/orders/v1/orders_grpc.pb.go",
			DeclarationPath:    "idl/proto/orders/v1/orders.proto",
			DeclarationLineage: "synthetic.invalid/mono:idl/proto/orders/v1/orders.proto",
			Protocol:           "grpc", State: "resolved",
		},
		{
			GeneratedPath:      "gen/proto-copy/orders/v1/orders_grpc.pb.go",
			DeclarationPath:    "idl/proto/orders/v1/orders.proto",
			DeclarationLineage: "synthetic.invalid/mono:idl/proto/orders/v1/orders.proto",
			Protocol:           "grpc", State: "resolved",
		},
		{
			GeneratedPath: "gen/proto/collision/v1/common_grpc.pb.go",
			Protocol:      "grpc", State: "missing",
			Reason: "no_snapshot_mapping",
		},
		{
			GeneratedPath:      "gen/thrift/ledger/ledger.go",
			DeclarationPath:    "idl/thrift/ledger.thrift",
			DeclarationLineage: "synthetic.invalid/mono:idl/thrift/ledger.thrift",
			Protocol:           "thrift", State: "resolved",
		},
		{
			GeneratedPath: "gen/thrift/missing/missing.go",
			Protocol:      "thrift", State: "missing",
			Reason: "no_snapshot_mapping",
		},
		{
			GeneratedPath: "gen/thrift/conflict/conflict.go",
			Protocol:      "thrift", State: "conflict",
			Reason: "multiple_declaration_paths",
		},
	}
	oracle.UnitMappings = []UnitMappingExpectation{
		{CallID: "proto-checkout", SourcePath: "src/checkout/caller.go", StartLine: callLine(oracle, "proto-checkout"), BuildTargets: []string{"//src/checkout:lib"}, Deployables: []string{"checkout-api"}, LogicalService: []string{"checkout"}, Owners: []string{"team-checkout"}, State: "resolved", Source: "unit-snapshot-v1"},
		{CallID: "proto-alias", SourcePath: "src/alias/caller.go", StartLine: callLine(oracle, "proto-alias"), BuildTargets: []string{"//src/alias:lib"}, Deployables: []string{"alias-worker"}, LogicalService: []string{"alias-service"}, Owners: []string{"team-orders"}, State: "resolved", Source: "unit-snapshot-v1"},
		{CallID: "proto-embedded", SourcePath: "src/embedded/caller.go", StartLine: callLine(oracle, "proto-embedded"), BuildTargets: []string{"//src/embedded:lib", "//src/shared:clients"}, Deployables: []string{"embedded-api"}, LogicalService: []string{"embedded-service", "shared-client"}, Owners: []string{"team-embedded", "team-platform"}, State: "ambiguous", Source: "unit-snapshot-v1"},
		{CallID: "proto-wrapper", SourcePath: "src/wrapper/client.go", StartLine: callLine(oracle, "proto-wrapper"), BuildTargets: []string{"//src/wrapper:lib"}, Deployables: []string{"wrapper-api"}, LogicalService: []string{"wrapper-service"}, State: "resolved", Source: "unit-snapshot-v1"},
		{CallID: "thrift-ledger", SourcePath: "src/thrift/caller.go", StartLine: callLine(oracle, "thrift-ledger"), State: "unavailable", Source: "unit-snapshot-v1"},
	}
	return oracle, nil
}

func locateCall(id, protocol, operation, path string, content []byte, needle string,
	occurrence int, role string, resolution Resolution, reason, symbol string,
) (CallExpectation, error) {
	offset := -1
	from := 0
	for range occurrence + 1 {
		next := strings.Index(string(content[from:]), needle)
		if next < 0 {
			return CallExpectation{}, fmt.Errorf("oracle %s: occurrence %d of %q absent from %s", id, occurrence, needle, path)
		}
		offset = from + next
		from = offset + len(needle)
	}
	startLine := 1 + strings.Count(string(content[:offset]), "\n")
	endLine := startLine + strings.Count(string(content[offset:offset+len(needle)]), "\n")
	return CallExpectation{
		ID: id, Protocol: protocol, CanonicalOperation: operation, Path: path,
		StartByte: offset, EndByte: offset + len(needle),
		StartLine: startLine, EndLine: endLine,
		CodeRole: role, Resolution: resolution, Reason: reason,
		GeneratedSymbol: symbol,
	}, nil
}

func callLine(oracle Oracle, id string) int {
	for _, call := range oracle.Calls {
		if call.ID == id {
			return call.StartLine
		}
	}
	panic("missing call " + id)
}

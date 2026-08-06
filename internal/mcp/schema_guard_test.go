package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
)

// TestHandWrittenStrictOutputSchemasAdmitEveryField walks the Contract and
// Caller tools' hand-written strict MCP output schemas against their Go output
// types. A field added to any nested strict object without a matching schema
// property then fails CI instead of production SDK output validation. Proof
// tools use investigation.EnvelopeOutputSchema rather than these schemas.
func TestHandWrittenStrictOutputSchemasAdmitEveryField(t *testing.T) {
	for _, guard := range []struct {
		name   string
		typ    reflect.Type
		schema map[string]any
	}{
		{"list_operation_callers", reflect.TypeOf(api.CallerMapPage{}), callerMapPageOutputSchema()},
		{"search_contract_operations", reflect.TypeOf(api.ContractCatalogList{}), contractCatalogListOutputSchema()},
		{"get_contract_operation", reflect.TypeOf(contractOperationResult{}), contractOperationOutputSchema()},
		{"read_operation_caller_citation", reflect.TypeOf(api.CallerMapCitation{}), callerCitationOutputSchema()},
		{"compare_operation_callers", reflect.TypeOf(api.CallerComparisonExactPage{}), callerComparisonOutputSchema()},
		{"get_change_workbench_impact", reflect.TypeOf(api.WorkbenchImpactPage{}), workbenchImpactOutputSchema()},
	} {
		t.Run(guard.name, func(t *testing.T) {
			missing := schemaMissingFields(guard.typ, guard.schema, guard.name)
			if len(missing) != 0 {
				t.Fatalf(
					"fields not admitted by the strict MCP output schema: %v",
					missing,
				)
			}
		})
	}

	t.Run("guard detects an omitted property", func(t *testing.T) {
		type synthetic struct {
			Kept    string `json:"kept"`
			Dropped string `json:"dropped,omitempty"`
		}
		schema := topLevelOutputSchema(
			[]string{"kept"},
			map[string]any{"kept": map[string]any{"type": "string"}},
		)
		missing := schemaMissingFields(
			reflect.TypeOf(synthetic{}), schema, "synthetic",
		)
		if len(missing) != 1 || missing[0] != "synthetic.dropped" {
			t.Fatalf("guard self-check = %v, want [synthetic.dropped]", missing)
		}
	})

	t.Run("guard detects an omitted nested property", func(t *testing.T) {
		type nested struct {
			Kept    string `json:"kept"`
			Dropped string `json:"dropped,omitempty"`
		}
		type synthetic struct {
			Nested nested `json:"nested"`
		}
		schema := topLevelOutputSchema(
			[]string{"nested"},
			map[string]any{
				"nested": topLevelOutputSchema(
					[]string{"kept"},
					map[string]any{"kept": map[string]any{"type": "string"}},
				),
			},
		)
		missing := schemaMissingFields(
			reflect.TypeOf(synthetic{}), schema, "synthetic",
		)
		if len(missing) != 1 || missing[0] != "synthetic.nested.dropped" {
			t.Fatalf(
				"nested guard self-check = %v, want [synthetic.nested.dropped]",
				missing,
			)
		}
	})
}

func schemaMissingFields(
	typ reflect.Type,
	schema map[string]any,
	path string,
) []string {
	for {
		switch typ.Kind() {
		case reflect.Pointer:
			typ = typ.Elem()
			continue
		case reflect.Slice, reflect.Array:
			items, ok := schema["items"].(map[string]any)
			if !ok {
				// The schema permits arbitrary items (or the Go value has
				// an unconstrained representation): nothing
				// strict to guard beneath this point.
				return nil
			}
			schema = items
			typ = typ.Elem()
			continue
		}
		break
	}
	if typ.Kind() != reflect.Struct {
		return nil
	}
	if strict, ok := schema["additionalProperties"].(bool); !ok || strict {
		// Only additionalProperties:false schemas reject unknown fields; a
		// permissive object cannot break SDK output validation.
		return nil
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return []string{path + " (strict schema without a properties map)"}
	}
	var missing []string
	for index := range typ.NumField() {
		field := typ.Field(index)
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			missing = append(
				missing, schemaMissingFields(field.Type, schema, path)...,
			)
			continue
		}
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		property, admitted := properties[name]
		if !admitted {
			missing = append(missing, path+"."+name)
			continue
		}
		if nested, isObject := property.(map[string]any); isObject {
			missing = append(
				missing,
				schemaMissingFields(field.Type, nested, path+"."+name)...,
			)
		}
	}
	return missing
}

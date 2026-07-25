package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigExampleDocumentsEveryOption(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Parse(data); err != nil {
		t.Fatalf("config example is not loadable: %v", err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}

	want := make(map[string]bool)
	collectSchemaPaths(reflect.TypeFor[Config](), "", want)

	documented := make(map[string]bool)
	var active []configExampleEvent
	collectActiveConfigPaths(&document, reflect.TypeFor[Config](), "", documented, &active)
	collectCommentedConfigPaths(data, reflect.TypeFor[Config](), active, documented)

	var missing []string
	for path := range want {
		if !documented[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("docs/config.example.yaml does not document these YAML options:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestOpenTelemetryDemoConfigIsPortableAndEnablesContractAtlas(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "phebs-otel-demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/Users/") {
		t.Fatal("OpenTelemetry demo config contains a machine-specific home path")
	}

	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("OpenTelemetry demo config is not loadable: %v", err)
	}
	if !cfg.Experimental.ProvisionalProtoExtraction {
		t.Fatal("OpenTelemetry demo config must explicitly enable provisional contract extraction")
	}
	if len(cfg.Connections) != 1 ||
		cfg.Connections[0].URL != "https://github.com/open-telemetry/opentelemetry-demo.git" {
		t.Fatalf("OpenTelemetry demo connection = %#v", cfg.Connections)
	}
	if !filepath.IsAbs(cfg.Server.DataDir) ||
		filepath.Base(cfg.Server.DataDir) != ".phebs-otel-demo" {
		t.Fatalf("OpenTelemetry demo data_dir = %q, want portable user-local path", cfg.Server.DataDir)
	}
}

func TestThriftDemoConfigIsPortableAndEnablesBothPacks(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "phebs-thrift-demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/Users/") {
		t.Fatal("Thrift demo config contains a machine-specific home path")
	}

	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Thrift demo config is not loadable: %v", err)
	}
	if !cfg.Experimental.ProvisionalThriftExtraction || !cfg.Experimental.ProvisionalProtoExtraction {
		t.Fatal("Thrift demo config must explicitly enable both provisional packs")
	}
	urls := make([]string, 0, len(cfg.Connections))
	for _, connection := range cfg.Connections {
		urls = append(urls, connection.URL)
	}
	sort.Strings(urls)
	want := []string{
		"https://github.com/jaegertracing/jaeger-client-go.git",
		"https://github.com/jaegertracing/jaeger-idl.git",
		"https://github.com/jaegertracing/jaeger.git",
	}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("Thrift demo connections = %v", urls)
	}
	if !filepath.IsAbs(cfg.Server.DataDir) ||
		filepath.Base(cfg.Server.DataDir) != ".phebs-thrift-demo" {
		t.Fatalf("Thrift demo data_dir = %q, want portable user-local path", cfg.Server.DataDir)
	}
}

type configExampleEvent struct {
	line   int
	indent int
	path   string
	typ    reflect.Type
	key    string // non-empty only for a commented mapping key
}

func collectSchemaPaths(typ reflect.Type, prefix string, paths map[string]bool) {
	typ = dereferenceConfigType(typ)
	switch typ.Kind() {
	case reflect.Struct:
		for _, field := range yamlConfigFields(typ) {
			path := joinConfigPath(prefix, yamlConfigFieldName(field))
			paths[path] = true
			collectSchemaPaths(field.Type, path, paths)
		}
	case reflect.Slice, reflect.Array:
		collectSchemaPaths(typ.Elem(), prefix, paths)
	case reflect.Map:
		collectSchemaPaths(typ.Elem(), prefix+".*", paths)
	}
}

func collectActiveConfigPaths(node *yaml.Node, typ reflect.Type, prefix string, paths map[string]bool, events *[]configExampleEvent) {
	for node.Kind == yaml.DocumentNode || node.Kind == yaml.AliasNode {
		if node.Kind == yaml.DocumentNode {
			if len(node.Content) == 0 {
				return
			}
			node = node.Content[0]
		} else {
			node = node.Alias
		}
	}

	typ = dereferenceConfigType(typ)
	switch typ.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return
		}
		fields := make(map[string]reflect.StructField)
		for _, field := range yamlConfigFields(typ) {
			fields[yamlConfigFieldName(field)] = field
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			field, ok := fields[key.Value]
			if !ok {
				continue
			}
			path := joinConfigPath(prefix, key.Value)
			paths[path] = true
			*events = append(*events, configExampleEvent{
				line: key.Line, indent: key.Column - 1, path: path, typ: field.Type,
			})
			collectActiveConfigPaths(value, field.Type, path, paths, events)
		}
	case reflect.Slice, reflect.Array:
		if node.Kind != yaml.SequenceNode {
			return
		}
		for _, child := range node.Content {
			collectActiveConfigPaths(child, typ.Elem(), prefix, paths, events)
		}
	case reflect.Map:
		if node.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			path := prefix + ".*"
			*events = append(*events, configExampleEvent{
				line: key.Line, indent: key.Column - 1, path: path, typ: typ.Elem(),
			})
			collectActiveConfigPaths(value, typ.Elem(), path, paths, events)
		}
	}
}

func collectCommentedConfigPaths(data []byte, root reflect.Type, active []configExampleEvent, paths map[string]bool) {
	events := append([]configExampleEvent(nil), active...)
	for lineNumber, line := range strings.Split(string(data), "\n") {
		indent, key, ok := commentedYAMLKey(line)
		if ok {
			events = append(events, configExampleEvent{
				line: lineNumber + 1, indent: indent, key: key,
			})
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].line != events[j].line {
			return events[i].line < events[j].line
		}
		return events[i].indent < events[j].indent
	})

	stack := []configExampleEvent{{indent: -1, typ: root}}
	for _, event := range events {
		for len(stack) > 1 && stack[len(stack)-1].indent >= event.indent {
			stack = stack[:len(stack)-1]
		}
		if event.key == "" {
			stack = append(stack, event)
			continue
		}

		parent := stack[len(stack)-1]
		parentType := dereferenceConfigType(parent.typ)
		if parentType.Kind() == reflect.Map {
			stack = append(stack, configExampleEvent{
				indent: event.indent, path: parent.path + ".*", typ: parentType.Elem(),
			})
			continue
		}
		for _, field := range yamlConfigFields(parentType) {
			if yamlConfigFieldName(field) != event.key {
				continue
			}
			path := joinConfigPath(parent.path, event.key)
			paths[path] = true
			stack = append(stack, configExampleEvent{
				indent: event.indent, path: path, typ: field.Type,
			})
			break
		}
	}
}

// commentedYAMLKey accepts only comment-only lines whose content is a single
// YAML mapping entry. One space after '#' is formatting; further spaces retain
// the example's nesting relative to the comment marker.
func commentedYAMLKey(line string) (int, string, bool) {
	leading := len(line) - len(strings.TrimLeft(line, " "))
	rest := line[leading:]
	if !strings.HasPrefix(rest, "# ") {
		return 0, "", false
	}
	content := strings.TrimPrefix(rest, "# ")
	nested := len(content) - len(strings.TrimLeft(content, " "))
	content = strings.TrimSpace(content)

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil || len(node.Content) != 1 {
		return 0, "", false
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode || len(root.Content) != 2 || root.Content[0].Kind != yaml.ScalarNode {
		return 0, "", false
	}
	return leading + nested, root.Content[0].Value, true
}

func yamlConfigFields(typ reflect.Type) []reflect.StructField {
	typ = dereferenceConfigType(typ)
	for typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = dereferenceConfigType(typ.Elem())
	}
	if typ.Kind() != reflect.Struct {
		return nil
	}
	fields := make([]reflect.StructField, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := yamlConfigFieldName(field)
		if field.IsExported() && name != "" && name != "-" {
			fields = append(fields, field)
		}
	}
	return fields
}

func yamlConfigFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
	return name
}

func dereferenceConfigType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

func joinConfigPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

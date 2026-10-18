package t271

import (
	"encoding/json"
	"strconv"
	"strings"
)

type Segment struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

type fieldPath []Segment

type declaration struct {
	kind     string
	owner    *Owner
	meaning  map[string]any
	identity any
	node     *node
}

type reference struct {
	kind string
	name string
	path fieldPath
}

type association struct {
	target string
	pairs  [][2]fieldPath
}

type validatedOwner struct {
	kind, name string
	fields     map[string]bool
	primary    []fieldPath
	partition  []fieldPath
	columns    []fieldPath
	sources    []reference
	assocs     []association
}

type validatedManifest struct {
	sourceClass  string
	declarations []declaration
	owners       []*validatedOwner
	byKind       map[string]map[string]*validatedOwner
	duplicate    bool
	associations int
	countError   *refusalError
}

func validateManifest(root *node) (*validatedManifest, *refusalError) {
	if err := object(root, []string{"format", "source_class", "tables", "views"}, nil); err != nil {
		return nil, err
	}
	version, err := text(root.object["format"], 256, "type_utf8_bytes")
	if err != nil {
		return nil, err
	}
	if version != format {
		return nil, reject("unsupported_format")
	}
	class, err := text(root.object["source_class"], 128, "name_utf8_bytes")
	if err != nil {
		return nil, err
	}
	if class != "authored" && class != "captured" {
		return nil, reject("invalid_shape")
	}
	b := &validatedManifest{sourceClass: class, byKind: map[string]map[string]*validatedOwner{
		"table": {}, "view": {},
	}}
	b.add("DECLARES_SCHEMA_SET", nil, root, map[string]any{"format": format, "source_class": class}, nil)
	for _, collection := range []struct{ key, kind, limit string }{
		{key: "tables", kind: "table", limit: "tables"},
		{key: "views", kind: "view", limit: "views"},
	} {
		items, err := array(root.object[collection.key], 16, 0, collection.limit)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			owner, err := b.readOwner(item, collection.kind)
			if err != nil {
				return nil, err
			}
			if _, exists := b.byKind[collection.kind][owner.name]; exists {
				b.duplicate = true
			}
			b.byKind[collection.kind][owner.name] = owner
			b.owners = append(b.owners, owner)
		}
	}
	if b.countError != nil {
		return nil, b.countError
	}
	if b.duplicate {
		return nil, reject("duplicate_declaration")
	}
	if err := b.references(); err != nil {
		return nil, err
	}
	return b, nil
}

func object(n *node, required, optional []string) *refusalError {
	if n == nil || n.kind != "object" {
		return reject("invalid_shape")
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range append(append([]string{}, required...), optional...) {
		allowed[key] = true
	}
	for _, key := range n.keys {
		if !allowed[key] {
			return reject("unknown_key")
		}
	}
	for _, key := range required {
		if _, exists := n.object[key]; !exists {
			return reject("invalid_shape")
		}
	}
	return nil
}

func array(n *node, maximum, minimum int, limit string) ([]*node, *refusalError) {
	if n == nil || n.kind != "array" {
		return nil, reject("invalid_shape")
	}
	if len(n.array) > maximum {
		return nil, over(limit)
	}
	if len(n.array) < minimum {
		return nil, reject("invalid_shape")
	}
	return n.array, nil
}

func text(n *node, maximum int, limit string) (string, *refusalError) {
	if n == nil || n.kind != "string" {
		return "", reject("invalid_shape")
	}
	value, ok := n.scalar.(string)
	if !ok {
		return "", reject("invalid_shape")
	}
	if len(value) > maximum {
		return "", over(limit)
	}
	if !validText(value) {
		return "", reject("invalid_text")
	}
	return value, nil
}

func name(n *node) (string, *refusalError) { return text(n, 128, "name_utf8_bytes") }

func readPath(n *node) (fieldPath, *refusalError) {
	segments, err := array(n, 8, 1, "path_depth")
	if err != nil {
		return nil, err
	}
	result := make(fieldPath, 0, len(segments))
	for index, item := range segments {
		if err := object(item, []string{"kind"}, []string{"name"}); err != nil {
			return nil, err
		}
		kind, err := name(item.object["kind"])
		if err != nil {
			return nil, err
		}
		segment := Segment{Kind: kind}
		switch kind {
		case "field":
			segment.Name, err = name(item.object["name"])
			if err != nil {
				return nil, err
			}
		case "array_element", "map_value":
			if _, exists := item.object["name"]; exists {
				return nil, reject("unknown_key")
			}
			if index == 0 {
				return nil, reject("invalid_shape")
			}
		default:
			return nil, reject("invalid_shape")
		}
		result = append(result, segment)
	}
	return result, nil
}

// Lookup encoding is length-prefixed; stable public identities use structured
// canonical JSON during publication. Neither representation splits names.
func pathKey(value fieldPath) string {
	var key strings.Builder
	for _, segment := range value {
		key.WriteString(strconv.Itoa(len(segment.Kind)))
		key.WriteByte(':')
		key.WriteString(segment.Kind)
		key.WriteString(strconv.Itoa(len(segment.Name)))
		key.WriteByte(':')
		key.WriteString(segment.Name)
	}
	return key.String()
}

func (b *validatedManifest) add(kind string, owner *Owner, n *node, meaning map[string]any, identity any) {
	if b.countError != nil {
		return
	}
	if len(b.declarations) == 256 {
		b.countError = over("aggregate_declarations")
		return
	}
	b.declarations = append(b.declarations, declaration{kind: kind, owner: owner, node: n, meaning: meaning, identity: identity})
}

func (b *validatedManifest) readOwner(n *node, kind string) (*validatedOwner, *refusalError) {
	required := []string{"name", "fields", "primary_key", "indexes"}
	if kind == "table" {
		required = append(required, "associations")
	}
	if err := object(n, required, []string{"partition_key"}); err != nil {
		return nil, err
	}
	ownerName, err := name(n.object["name"])
	if err != nil {
		return nil, err
	}
	owner := &validatedOwner{kind: kind, name: ownerName, fields: make(map[string]bool)}
	identity := &Owner{Kind: kind, Name: ownerName}
	partitionState := "undeclared"
	if n.object["partition_key"] != nil {
		partitionState = "declared"
	}
	factKind := "DECLARES_TABLE"
	if kind == "view" {
		factKind = "DECLARES_VIEW"
	}
	b.add(factKind, identity, n, map[string]any{"name": ownerName, "partition_key_state": partitionState}, nil)
	if err := b.readFields(n.object["fields"], owner, identity); err != nil {
		return nil, err
	}
	owner.primary, err = b.readKeys(n.object["primary_key"], identity)
	if err != nil {
		return nil, err
	}
	if partition := n.object["partition_key"]; partition != nil {
		items, err := array(partition, 8, 0, "key_width")
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, reject("invalid_partition_key")
		}
		seen := make(map[string]bool, len(items))
		for position, item := range items {
			value, err := readPath(item)
			if err != nil {
				return nil, err
			}
			b.markDuplicate(seen, pathKey(value))
			owner.partition = append(owner.partition, value)
			b.add("DECLARES_PARTITION_KEY", identity, item, map[string]any{"path": value, "position": position}, value)
		}
	}
	if err := b.readIndexes(n.object["indexes"], owner, identity); err != nil {
		return nil, err
	}
	if kind == "table" {
		if err := b.readAssociations(n.object["associations"], owner, identity); err != nil {
			return nil, err
		}
	}
	return owner, nil
}

func (b *validatedManifest) markDuplicate(seen map[string]bool, key string) {
	if seen[key] {
		b.duplicate = true
	}
	seen[key] = true
}

func (b *validatedManifest) readFields(n *node, owner *validatedOwner, identity *Owner) *refusalError {
	limit := "fields_per_table"
	if owner.kind == "view" {
		limit = "fields_per_view"
	}
	items, err := array(n, 128, 1, limit)
	if err != nil {
		return err
	}
	for position, item := range items {
		if owner.kind == "table" {
			if err := object(item, []string{"path", "type"}, nil); err != nil {
				return err
			}
			value, err := readPath(item.object["path"])
			if err != nil {
				return err
			}
			spelling, err := text(item.object["type"], 256, "type_utf8_bytes")
			if err != nil {
				return err
			}
			b.markDuplicate(owner.fields, pathKey(value))
			b.add("DECLARES_FIELD", identity, item, map[string]any{"path": value, "type": spelling}, value)
			continue
		}
		if err := object(item, []string{"name", "type", "sources"}, nil); err != nil {
			return err
		}
		outputName, err := name(item.object["name"])
		if err != nil {
			return err
		}
		spelling, err := text(item.object["type"], 256, "type_utf8_bytes")
		if err != nil {
			return err
		}
		b.markDuplicate(owner.fields, pathKey(fieldPath{{Kind: "field", Name: outputName}}))
		b.add("DECLARES_VIEW_FIELD", identity, item, map[string]any{"name": outputName, "type": spelling, "position": position}, outputName)
		sources, err := array(item.object["sources"], 16, 0, "sources_per_view_field")
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(sources))
		for _, source := range sources {
			if err := object(source, []string{"kind", "name", "path"}, nil); err != nil {
				return err
			}
			kind, err := name(source.object["kind"])
			if err != nil {
				return err
			}
			if kind != "table" && kind != "view" {
				return reject("invalid_shape")
			}
			sourceName, err := name(source.object["name"])
			if err != nil {
				return err
			}
			value, err := readPath(source.object["path"])
			if err != nil {
				return err
			}
			owner.sources = append(owner.sources, reference{kind: kind, name: sourceName, path: value})
			b.markDuplicate(seen, kind+"\x00"+sourceName+"\x00"+pathKey(value))
			sourceOwner := *identity
			sourceOwner.Field = outputName
			meaning := map[string]any{"kind": kind, "name": sourceName, "path": value}
			b.add("DECLARES_VIEW_SOURCE", &sourceOwner, source, meaning, meaning)
		}
	}
	return nil
}

func readColumn(n *node, prefix bool) (fieldPath, map[string]any, *refusalError) {
	optional := []string{"direction"}
	if prefix {
		optional = append(optional, "prefix_length", "prefix_unit")
	}
	if err := object(n, []string{"path"}, optional); err != nil {
		return nil, nil, err
	}
	value, err := readPath(n.object["path"])
	if err != nil {
		return nil, nil, err
	}
	direction := "asc"
	if item := n.object["direction"]; item != nil {
		direction, err = name(item)
		if err != nil {
			return nil, nil, err
		}
		if direction != "asc" && direction != "desc" {
			return nil, nil, reject("invalid_shape")
		}
	}
	meaning := map[string]any{"path": value, "direction": direction}
	length, unit := n.object["prefix_length"], n.object["prefix_unit"]
	if (length == nil) != (unit == nil) {
		return nil, nil, reject("invalid_shape")
	}
	if length != nil {
		number, ok := length.scalar.(json.Number)
		if !ok || length.kind != "number" {
			return nil, nil, reject("invalid_shape")
		}
		count, parseErr := strconv.ParseInt(string(number), 10, 64)
		if parseErr != nil || count <= 0 {
			return nil, nil, reject("invalid_shape")
		}
		spelling, err := name(unit)
		if err != nil {
			return nil, nil, err
		}
		meaning["prefix_length"], meaning["prefix_unit"] = count, spelling
	}
	return value, meaning, nil
}

func (b *validatedManifest) readKeys(n *node, identity *Owner) ([]fieldPath, *refusalError) {
	items, err := array(n, 8, 1, "key_width")
	if err != nil {
		return nil, err
	}
	result := make([]fieldPath, 0, len(items))
	seen := make(map[string]bool, len(items))
	for position, item := range items {
		value, meaning, err := readColumn(item, false)
		if err != nil {
			return nil, err
		}
		b.markDuplicate(seen, pathKey(value))
		meaning["position"] = position
		result = append(result, value)
		b.add("DECLARES_PRIMARY_KEY", identity, item, meaning, value)
	}
	return result, nil
}

func (b *validatedManifest) readIndexes(n *node, owner *validatedOwner, identity *Owner) *refusalError {
	items, err := array(n, 16, 0, "indexes_per_owner")
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(items))
	for position, item := range items {
		if err := object(item, []string{"name", "kind", "columns"}, nil); err != nil {
			return err
		}
		indexName, err := name(item.object["name"])
		if err != nil {
			return err
		}
		kind, err := name(item.object["kind"])
		if err != nil {
			return err
		}
		b.markDuplicate(seen, indexName)
		b.add("DECLARES_INDEX", identity, item, map[string]any{"name": indexName, "kind": kind, "position": position}, indexName)
		columns, err := array(item.object["columns"], 8, 1, "index_columns")
		if err != nil {
			return err
		}
		columnSeen := make(map[string]bool, len(columns))
		for columnPosition, column := range columns {
			value, meaning, err := readColumn(column, true)
			if err != nil {
				return err
			}
			b.markDuplicate(columnSeen, pathKey(value))
			meaning["position"] = columnPosition
			owner.columns = append(owner.columns, value)
			columnOwner := *identity
			columnOwner.Index = indexName
			b.add("DECLARES_INDEX_COLUMN", &columnOwner, column, meaning, value)
		}
	}
	return nil
}

func (b *validatedManifest) readAssociations(n *node, owner *validatedOwner, identity *Owner) *refusalError {
	items, err := array(n, 64, 0, "associations_total")
	if err != nil {
		return err
	}
	b.associations += len(items)
	if b.associations > 64 {
		return over("associations_total")
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if err := object(item, []string{"name", "target", "fields"}, nil); err != nil {
			return err
		}
		associationName, err := name(item.object["name"])
		if err != nil {
			return err
		}
		target, err := name(item.object["target"])
		if err != nil {
			return err
		}
		b.markDuplicate(seen, associationName)
		b.add("DECLARES_ASSOCIATION", identity, item, map[string]any{"name": associationName, "target": target}, associationName)
		pairs, err := array(item.object["fields"], 8, 1, "association_fields")
		if err != nil {
			return err
		}
		association := association{target: target}
		pairSeen := make(map[string]bool, len(pairs))
		for _, pair := range pairs {
			if err := object(pair, []string{"source", "target"}, nil); err != nil {
				return err
			}
			from, err := readPath(pair.object["source"])
			if err != nil {
				return err
			}
			to, err := readPath(pair.object["target"])
			if err != nil {
				return err
			}
			b.markDuplicate(pairSeen, pathKey(from)+"\x00"+pathKey(to))
			association.pairs = append(association.pairs, [2]fieldPath{from, to})
			pairOwner := *identity
			pairOwner.Association = associationName
			meaning := map[string]any{"source": from, "target": to}
			b.add("DECLARES_ASSOCIATION_FIELD", &pairOwner, pair, meaning, meaning)
		}
		owner.assocs = append(owner.assocs, association)
	}
	return nil
}

func (b *validatedManifest) references() *refusalError {
	for _, owner := range b.owners {
		for _, value := range append(append([]fieldPath{}, owner.primary...), owner.columns...) {
			if !owner.fields[pathKey(value)] || owner.kind == "view" && len(value) != 1 {
				return reject("unresolved_reference")
			}
		}
		for _, reference := range owner.sources {
			target := b.byKind[reference.kind][reference.name]
			if target == nil || !target.fields[pathKey(reference.path)] || reference.kind == "view" && len(reference.path) != 1 {
				return reject("unresolved_reference")
			}
		}
		for _, association := range owner.assocs {
			target := b.byKind["table"][association.target]
			if target == nil {
				return reject("unresolved_reference")
			}
			for _, pair := range association.pairs {
				if !owner.fields[pathKey(pair[0])] || !target.fields[pathKey(pair[1])] {
					return reject("unresolved_reference")
				}
			}
		}
	}
	for _, owner := range b.owners {
		if len(owner.partition) > len(owner.primary) {
			return reject("invalid_partition_key")
		}
		for index, value := range owner.partition {
			if pathKey(value) != pathKey(owner.primary[index]) {
				return reject("invalid_partition_key")
			}
		}
	}
	colors := make(map[string]uint8, len(b.byKind["view"]))
	var visit func(string) bool
	visit = func(name string) bool {
		if colors[name] == 1 {
			return false
		}
		if colors[name] == 2 {
			return true
		}
		colors[name] = 1
		for _, source := range b.byKind["view"][name].sources {
			if source.kind == "view" && !visit(source.name) {
				return false
			}
		}
		colors[name] = 2
		return true
	}
	for _, owner := range b.owners {
		if owner.kind == "view" && !visit(owner.name) {
			return reject("view_dependency_cycle")
		}
	}
	return nil
}

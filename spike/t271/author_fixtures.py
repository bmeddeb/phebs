#!/usr/bin/env python3
"""Author frozen inputs/labels. This encoder neither reads manifests nor parses CQL.

Run only before the prerequisite checkpoint. The later reader must never import
this module or regenerate labels from its own results.
"""

import copy
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent
if (ROOT / "preregistration.json").exists():
    raise SystemExit("preregistration exists; no post-freeze regeneration")

FORMAT = "phebs-document-schema-v1"
BOUNDS = {
    "manifest_bytes": 65536,
    "json_depth": 32,
    "tables": 16,
    "fields_per_table": 128,
    "path_depth": 8,
    "name_utf8_bytes": 128,
    "type_utf8_bytes": 256,
    "key_width": 8,
    "index_columns": 8,
    "association_fields": 8,
    "prefix_length_max": 9223372036854775807,
    "indexes_per_owner": 16,
    "associations_total": 64,
    "views": 16,
    "fields_per_view": 128,
    "sources_per_view_field": 16,
    "aggregate_declarations": 256,
    "emitted_citations": 192,
    "repository_utf8_bytes": 256,
    "manifest_path_utf8_bytes": 256,
}
CONTEXT = {"repository": "example.invalid/neutral/schema", "manifest_path": "schema/document.json", "commit": "1" * 40}


def field_segment(name):
    return {"kind": "field", "name": name}


def field_path(*names):
    return [field_segment(name) if isinstance(name, str) else name for name in names]


def field(name, spelling="opaque:string"):
    return {"path": field_path(name), "type": spelling}


def key(name, direction=None):
    value = {"path": field_path(name)}
    if direction is not None:
        value["direction"] = direction
    return value


def table(name="T", names=("id",)):
    return {"name": name, "fields": [field(name) for name in names], "primary_key": [key(names[0])], "indexes": [], "associations": []}


def view(name="V", output="id", sources=None):
    return {"name": name, "fields": [{"name": output, "type": "opaque:string", "sources": sources or []}], "primary_key": [key(output)], "indexes": []}


def source(kind, name, field_name):
    return {"kind": kind, "name": name, "path": field_path(field_name)}


def manifest(tables=None, views=None, source_class="authored"):
    return {"format": FORMAT, "source_class": source_class, "tables": tables if tables is not None else [table()], "views": views or []}


def encode(value):
    """JSON encoder records value offsets while writing, without a JSON decoder."""
    output = bytearray()
    positions = {}

    def write(item, pointer, depth):
        start = len(output)
        if isinstance(item, dict):
            output.extend(b"{")
            for ordinal, (name, child) in enumerate(item.items()):
                output.extend(("," if ordinal else "").encode() + b"\n" + b"  " * (depth + 1))
                output.extend(json.dumps(name, ensure_ascii=False).encode() + b": ")
                escaped = name.replace("~", "~0").replace("/", "~1")
                write(child, pointer + "/" + escaped, depth + 1)
            if item:
                output.extend(b"\n" + b"  " * depth)
            output.extend(b"}")
        elif isinstance(item, list):
            output.extend(b"[")
            for ordinal, child in enumerate(item):
                output.extend(("," if ordinal else "").encode() + b"\n" + b"  " * (depth + 1))
                write(child, pointer + "/" + str(ordinal), depth + 1)
            if item:
                output.extend(b"\n" + b"  " * depth)
            output.extend(b"]")
        else:
            output.extend(json.dumps(item, ensure_ascii=False, allow_nan=False).encode())
        positions[pointer] = [start, len(output)]

    write(value, "", 0)
    output.extend(b"\n")
    return bytes(output), positions


def write_json(name, value):
    path = ROOT / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False) + "\n")


def expected_declarations(value):
    """Fixture-author ledger, fixed before the reader exists; no source input read."""
    labels = []

    def add(kind, pointer, owner=None, meaning=None):
        labels.append({"kind": kind, "pointer": pointer, "owner": owner, "meaning": meaning})

    add("DECLARES_SCHEMA_SET", "", meaning={"format": FORMAT, "source_class": value["source_class"]})
    for collection, owner_kind in [("tables", "table"), ("views", "view")]:
        for ordinal, owner in enumerate(value[collection]):
            prefix = f"/{collection}/{ordinal}"
            identity = {"kind": owner_kind, "name": owner["name"]}
            add("DECLARES_TABLE" if owner_kind == "table" else "DECLARES_VIEW", prefix, identity,
                {"name": owner["name"], "partition_key_state": "declared" if "partition_key" in owner else "undeclared"})
            for index, item in enumerate(owner["fields"]):
                meaning = {"path": item["path"], "type": item["type"]} if owner_kind == "table" else {"name": item["name"], "type": item["type"], "position": index}
                add("DECLARES_FIELD" if owner_kind == "table" else "DECLARES_VIEW_FIELD", f"{prefix}/fields/{index}", identity, meaning)
                if owner_kind == "view":
                    for source_index, reference in enumerate(item["sources"]):
                        add("DECLARES_VIEW_SOURCE", f"{prefix}/fields/{index}/sources/{source_index}", {**identity, "field": item["name"]}, reference)
            for index, item in enumerate(owner["primary_key"]):
                add("DECLARES_PRIMARY_KEY", f"{prefix}/primary_key/{index}", identity,
                    {"path": item["path"], "position": index, "direction": item.get("direction", "asc")})
            for index, item in enumerate(owner.get("partition_key", [])):
                add("DECLARES_PARTITION_KEY", f"{prefix}/partition_key/{index}", identity, {"path": item, "position": index})
            for index, item in enumerate(owner["indexes"]):
                index_owner = {**identity, "index": item["name"]}
                add("DECLARES_INDEX", f"{prefix}/indexes/{index}", identity, {"name": item["name"], "kind": item["kind"], "position": index})
                for column_index, column in enumerate(item["columns"]):
                    add("DECLARES_INDEX_COLUMN", f"{prefix}/indexes/{index}/columns/{column_index}", index_owner,
                        {**column, "direction": column.get("direction", "asc"), "position": column_index})
            for index, item in enumerate(owner.get("associations", [])):
                association_owner = {**identity, "association": item["name"]}
                add("DECLARES_ASSOCIATION", f"{prefix}/associations/{index}", identity, {"name": item["name"], "target": item["target"]})
                for pair_index, pair in enumerate(item["fields"]):
                    add("DECLARES_ASSOCIATION_FIELD", f"{prefix}/associations/{index}/fields/{pair_index}", association_owner, pair)
    return labels


CASES = []
PUBLIC_POSITIONS = None
PUBLIC_VALUE = None


def positive(name, value, features, padded=None):
    global PUBLIC_POSITIONS, PUBLIC_VALUE
    data, positions = encode(value)
    if padded is not None:
        assert len(data) <= padded
        data += b" " * (padded - len(data))
    labels = expected_declarations(value)
    for label in labels:
        start, end = positions[label["pointer"]]
        label["span"] = {"start": start, "end": end, "sha256": hashlib.sha256(data[start:end]).hexdigest()}
    path = ROOT / f"fixtures/positive/{name}.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    write_json(f"labels/{name}.json", {"authorship": "Fixture-author declarative labels, frozen before any reader implementation or run; not independent extraction-accuracy evidence.", "fixture_sha256": hashlib.sha256(data).hexdigest(), "declarations": labels})
    CASES.append({"id": name, "input": str(path.relative_to(ROOT)), "context": CONTEXT, "expect": "accepted", "labels": f"labels/{name}.json", "declarations": len(labels), "features": features})
    if name == "public-captured":
        PUBLIC_POSITIONS, PUBLIC_VALUE = positions, value


def negative(name, value=None, raw=None, code="invalid_shape", limit=None, context=None):
    if raw is None:
        raw, _ = encode(value)
    path = ROOT / f"fixtures/negative/{name}.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(raw)
    CASES.append({"id": name, "input": str(path.relative_to(ROOT)), "context": context or CONTEXT, "expect": "refused", "refusal": {"code": code, **({"limit": limit} if limit else {})}, "expected_facts": 0, "expected_citations": 0, "fixture_sha256": hashlib.sha256(raw).hexdigest()})


# Shape vocabulary is deliberately closed. All byte/depth/aggregate/relational
# conditions in README.md also apply; JSON Schema character lengths alone
# do not pretend to enforce UTF-8 byte limits or cross-object references.
name = {"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[^\\u0000-\\u001f\\u007f-\\u009f]+$"}
opaque = {**name, "maxLength": 256}


def obj(properties, required):
    return {"type": "object", "additionalProperties": False, "properties": properties, "required": required}


def array(reference, maximum, minimum=0):
    return {"type": "array", "items": {"$ref": "#/$defs/" + reference}, "minItems": minimum, "maxItems": maximum}


definitions = {
    "name": name,
    "opaque_type": opaque,
    "segment": {"oneOf": [obj({"kind": {"const": "field"}, "name": {"$ref": "#/$defs/name"}}, ["kind", "name"]), obj({"kind": {"enum": ["array_element", "map_value"]}}, ["kind"])]},
    "path": array("segment", 8, 1),
    "field": obj({"path": {"$ref": "#/$defs/path"}, "type": {"$ref": "#/$defs/opaque_type"}}, ["path", "type"]),
    "key": obj({"path": {"$ref": "#/$defs/path"}, "direction": {"enum": ["asc", "desc"], "default": "asc"}}, ["path"]),
    "index_column": {**obj({"path": {"$ref": "#/$defs/path"}, "direction": {"enum": ["asc", "desc"], "default": "asc"}, "prefix_length": {"type": "integer", "minimum": 1, "maximum": 9223372036854775807}, "prefix_unit": {"$ref": "#/$defs/name"}}, ["path"]), "dependentRequired": {"prefix_length": ["prefix_unit"], "prefix_unit": ["prefix_length"]}},
    "index": obj({"name": {"$ref": "#/$defs/name"}, "kind": {"$ref": "#/$defs/name"}, "columns": array("index_column", 8, 1)}, ["name", "kind", "columns"]),
    "association_pair": obj({"source": {"$ref": "#/$defs/path"}, "target": {"$ref": "#/$defs/path"}}, ["source", "target"]),
    "association": obj({"name": {"$ref": "#/$defs/name"}, "target": {"$ref": "#/$defs/name"}, "fields": array("association_pair", 8, 1)}, ["name", "target", "fields"]),
    "view_source": obj({"kind": {"enum": ["table", "view"]}, "name": {"$ref": "#/$defs/name"}, "path": {"$ref": "#/$defs/path"}}, ["kind", "name", "path"]),
    "view_field": obj({"name": {"$ref": "#/$defs/name"}, "type": {"$ref": "#/$defs/opaque_type"}, "sources": array("view_source", 16)}, ["name", "type", "sources"]),
}
definitions["table"] = obj({"name": {"$ref": "#/$defs/name"}, "fields": array("field", 128, 1), "primary_key": array("key", 8, 1), "partition_key": array("path", 8, 1), "indexes": array("index", 16), "associations": array("association", 64)}, ["name", "fields", "primary_key", "indexes", "associations"])
definitions["view"] = obj({"name": {"$ref": "#/$defs/name"}, "fields": array("view_field", 128, 1), "primary_key": array("key", 8, 1), "partition_key": array("path", 8, 1), "indexes": array("index", 16)}, ["name", "fields", "primary_key", "indexes"])
schema = {"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://phebs.com/schemas/phebs-document-schema-v1.json", "title": FORMAT, "$comment": "Spike-only contract. README.md defines byte/depth/aggregate/reference/order/duplicate/payload refusal requirements not expressible by this structural schema. No vendor/runtime schema validation is claimed.", **obj({"format": {"const": FORMAT}, "source_class": {"enum": ["authored", "captured"]}, "tables": array("table", 16), "views": array("view", 16)}, ["format", "source_class", "tables", "views"]), "$defs": definitions}
write_json("phebs-document-schema-v1.schema.json", schema)
write_json("bounds.json", BOUNDS)

# Positive synthetic truth is authored before any reader exists.
orders = table("Orders", ("tenant", "id", "owner", "[]", "payload", "value"))
orders["fields"] += [
    {"path": field_path("payload", "children"), "type": "source:list<opaque-object>"},
    {"path": field_path("payload", "children", {"kind": "array_element"}), "type": "opaque-object"},
    {"path": field_path("payload", "children", {"kind": "array_element"}, "tags"), "type": "source:map<opaque-key,opaque-object>"},
    {"path": field_path("payload", "children", {"kind": "array_element"}, "tags", {"kind": "map_value"}), "type": "opaque-object"},
    {"path": field_path("payload", "children", {"kind": "array_element"}, "tags", {"kind": "map_value"}, "[]"), "type": "主张<opaque/UTF-8>"},
]
orders["fields"][3]["type"] = "money<12,4> (opaque; no conversion)"
orders["primary_key"] = [key("tenant"), key("id", "desc")]
orders["partition_key"] = [field_path("tenant")]
orders["indexes"] = [{"name": "prefix-owner", "kind": "opaque::ordered-prefix", "columns": [{**key("owner", "desc"), "prefix_length": 4, "prefix_unit": "source-codepoints"}, key("tenant")]}, {"name": "ID-only", "kind": "sparse/custom β", "columns": [key("id")]}]
orders["associations"] = [{"name": "purchaser", "target": "客户", "fields": [{"source": field_path("owner"), "target": field_path("账号")}]}, {"name": "billing", "target": "客户", "fields": [{"source": field_path("owner"), "target": field_path("账号")}]}, {"name": "self-parent", "target": "Orders", "fields": [{"source": field_path("id"), "target": field_path("id")}]}]
customer = table("客户", ("账号", "姓名"))
customer["associations"] = [{"name": "orders", "target": "Orders", "fields": [{"source": field_path("账号"), "target": field_path("owner")}]}]
summary = view("Summary", "customer", [source("table", "Orders", "owner"), source("table", "客户", "账号")])
summary["fields"] += [{"name": "[]", "type": "opaque-declared-output", "sources": []}, {"name": "流水", "type": "not-a-vendor-scalar", "sources": [source("table", "Orders", "id")]}]
summary["primary_key"] = [key("customer", "desc"), key("流水")]
summary["partition_key"] = [field_path("customer")]
summary["indexes"] = [{"name": "view-prefix", "kind": "opaque:view-index", "columns": [{**key("customer"), "prefix_length": 2, "prefix_unit": "source-defined-unit"}]}]
rich = manifest([orders, customer, table("孤立")], [summary, view("SummarySubset", "owner", [source("view", "Summary", "customer")])])
positive("authored-rich", rich, ["typed nested object/list/map paths", "literal [] distinct from traversal", "opaque types/kinds/prefix units", "non-ASCII exact names", "descending composite primary key", "partition prefix", "undeclared partition key", "ordered indexes", "view-owned index", "same-pair distinct associations", "self/cyclic association graph", "table with no associations", "multi/zero-source view outputs", "acyclic typed view dependency"])
positive("captured-minimal", manifest(source_class="captured"), ["captured source class", "undeclared partition key"])
positive("empty-captured", manifest([], [], "captured"), ["explicit empty snapshot distinct from missing artifact"])
positive("comparison-before", manifest([table("Queue", ("id", "old", "stable"))]), ["explicit comparison old input"])
after = manifest([table("Queue", ("id", "new β", "stable"))])
after["tables"][0]["fields"][2]["type"] = "opaque:bytes"
positive("comparison-after", after, ["explicit comparison replacement input"])
positive("input-bytes-at-limit", manifest(), ["inclusive input byte bound"], padded=65536)
positive("tables-at-limit", manifest([table("T" + str(i)) for i in range(16)]), ["inclusive table bound"])
positive("fields-at-limit", manifest([table(names=tuple("f" + str(i) for i in range(128)))]), ["inclusive table field bound"])
name_bound = manifest([table(names=("é" * 64,))])
name_bound["tables"][0]["fields"][0]["type"] = "x" * 256
positive("names-at-limit", name_bound, ["UTF-8 name byte bound", "type byte bound"])
typed_names = manifest([table("Shared", ("id", "é", "e\u0301", "[]", ".", "/"))], [view("Shared", sources=[source("table", "Shared", "id")])])
positive("literal-and-kind-identities", typed_names, ["composed/decomposed Unicode names remain distinct", "literal punctuation is not filesystem traversal", "table/view namespaces explicitly disjoint"])
path_bound = manifest(); path_bound["tables"][0]["fields"].append({"path": field_path(*("p" + str(i) for i in range(8))), "type": "opaque"})
positive("path-at-limit", path_bound, ["inclusive structured path depth bound", "flattened path declares no inferred parent fields"])
positive("citations-at-limit", manifest([table("A", tuple("f" + str(i) for i in range(94))), table("B", tuple("f" + str(i) for i in range(93)))]), ["inclusive 192-fact/citation output bound"])

# Public fixture is a manual derivation of the fixed text, not CQL parsing.
hotel_fields = [("hotel_id", "text"), ("start_date", "date"), ("end_date", "date"), ("room_number", "smallint"), ("confirm_number", "text"), ("guest_id", "uuid")]
guest_fields = [("guest_last_name", "text"), ("hotel_id", "text"), ("start_date", "date"), ("end_date", "date"), ("room_number", "smallint"), ("confirm_number", "text"), ("guest_id", "uuid")]
people_fields = [("guest_id", "uuid"), ("first_name", "text"), ("last_name", "text"), ("title", "text"), ("emails", "set<text>"), ("phone_numbers", "list<text>"), ("addresses", "map<text, frozen<address>>"), ("confirm_number", "text")]
public_tables = []
for table_name, fields, primary, partition in [
    ("reservation.reservations_by_hotel_date", hotel_fields, ["hotel_id", "start_date", "room_number"], ["hotel_id", "start_date"]),
    ("reservation.reservations_by_guest", guest_fields, ["guest_last_name", "hotel_id"], ["guest_last_name"]),
    ("reservation.guests", people_fields, ["guest_id"], ["guest_id"]),
]:
    item = table(table_name)
    item["fields"] = [field(name, spelling) for name, spelling in fields]
    item["primary_key"] = [key(name) for name in primary]
    item["partition_key"] = [field_path(name) for name in partition]
    public_tables.append(item)
public_tables[2]["fields"] += [
    {"path": field_path("emails", {"kind": "array_element"}), "type": "text"},
    {"path": field_path("phone_numbers", {"kind": "array_element"}), "type": "text"},
    {"path": field_path("addresses", {"kind": "map_value"}), "type": "frozen<address>"},
    *[{"path": field_path("addresses", {"kind": "map_value"}, name), "type": "text"} for name in ["street", "city", "state_or_province", "postal_code", "country"]],
]
public_view = view("reservation.reservations_by_confirmation")
public_view["fields"] = [{"name": name, "type": spelling, "sources": [source("table", "reservation.reservations_by_hotel_date", name)]} for name, spelling in hotel_fields]
public_view["primary_key"] = [key(name) for name in ["confirm_number", "hotel_id", "start_date", "room_number"]]
public_view["partition_key"] = [field_path("confirm_number")]
positive("public-captured", manifest(public_tables, [public_view], "captured"), ["hand-derived pinned Apache public reference", "UDT expansion", "set/list/map traversals", "composite/scalar partition keys", "wildcard view output expansion"])

# Single-cause negative labels cover strict decoding, structural and relational
# refusal. There is intentionally no prediction stream or reader result here.
base = manifest()
base_bytes, _ = encode(base)
negative("invalid-utf8", raw=base_bytes.replace(b'"T"', b'"\xff"'), code="invalid_utf8")
negative("unpaired-surrogate", raw=base_bytes.replace(b'"T"', b'"\\ud800"'), code="invalid_unicode_scalar")
negative("malformed-json", raw=b'{"format":', code="invalid_json")
negative("trailing-value", raw=base_bytes + b"true", code="trailing_bytes")
negative("duplicate-root-key", raw=base_bytes.replace(b'{\n', b'{\n  "format": "' + FORMAT.encode() + b'",\n', 1), code="duplicate_key")
negative("duplicate-nested-key", raw=base_bytes.replace(b'"name": "T"', b'"name": "T", "name": "T"'), code="duplicate_key")
negative("control-name", value=manifest([table("bad\x01name")]), code="invalid_text")
negative("blank-name", value=manifest([table("  ")]), code="invalid_text")
negative("c1-control-name", value=manifest([table("bad\x85name")]), code="invalid_text")
negative("empty-name", value=manifest([table("")]), code="invalid_text")
for text, name in [("", "empty-type"), ("\t", "control-type")]:
    x = copy.deepcopy(base); x["tables"][0]["fields"][0]["type"] = text
    negative(name, x, code="invalid_text")
negative("unknown-root-key", value={**base, "unknown": "no"}, code="unknown_key")
v = copy.deepcopy(base); v["tables"][0]["fields"][0]["unknown"] = "no"
negative("unknown-nested-key", v, code="unknown_key")
negative("unknown-version", {**base, "format": "phebs-document-schema-v99"}, code="unsupported_format")
negative("source-class-missing", {k: v for k, v in base.items() if k != "source_class"})
negative("source-class-unknown", {**base, "source_class": "live"})
negative("root-array", value=[])
negative("null-table", {**base, "tables": [None]})
negative("missing-primary-key", manifest([{k: v for k, v in table().items() if k != "primary_key"}]))
negative("empty-primary-key", manifest([{**table(), "primary_key": []}]))
for bad_path, name in [("id", "path-string"), ([], "empty-path"), ([{"kind": "array_element"}], "path-starts-traversal"), ([{"kind": "field"}], "field-segment-missing-name"), ([{"kind": "field", "name": "id", "extra": True}], "segment-extra-key"), ([{"kind": "field", "name": "id"}, {"kind": "map_value", "name": "bad"}], "traversal-with-name"), ([{"kind": "field", "name": "id"}, {"kind": "set_element"}], "unknown-traversal")]:
    v = copy.deepcopy(base); v["tables"][0]["fields"][0]["path"] = bad_path
    negative(name, v, code="unknown_key" if name in ["segment-extra-key", "traversal-with-name"] else "invalid_shape")
v = manifest([table(names=("id", "other"))]); v["tables"][0]["partition_key"] = [field_path("other")]
negative("partition-not-prefix", v, code="invalid_partition_key")
v = copy.deepcopy(base); v["tables"][0]["partition_key"] = []
negative("empty-partition", v, code="invalid_partition_key")
v = copy.deepcopy(base); v["tables"][0]["partition_key"] = [{**key("id"), "direction": "asc"}]
negative("direction-on-partition", v)
for direction in ["sideways", 7, None]:
    v = copy.deepcopy(base); v["tables"][0]["primary_key"][0]["direction"] = direction
    negative("invalid-direction-" + str(direction), v)
v = copy.deepcopy(base); v["tables"][0]["indexes"] = [{"name": "I", "kind": "opaque", "columns": [key("id")]}]
for length in [0, -1, 1.5, "4", 9223372036854775808]:
    x = copy.deepcopy(v); x["tables"][0]["indexes"][0]["columns"][0].update(prefix_length=length, prefix_unit="opaque")
    negative("invalid-prefix-" + str(length), x)
x = copy.deepcopy(v); x["tables"][0]["indexes"][0]["columns"][0]["prefix_length"] = 4
negative("prefix-without-unit", x)
x = copy.deepcopy(v); x["tables"][0]["indexes"][0]["columns"][0]["prefix_unit"] = "opaque"
negative("unit-without-prefix", x)
x = copy.deepcopy(v); x["tables"][0]["indexes"][0]["columns"][0].update(prefix_length=9223372036854775807, prefix_unit="opaque-source-unit")
positive("prefix-at-integer-limit", x, ["opaque positive signed-64-bit metadata; no vendor-unit conversion or work allocation"])
negative("duplicate-table", manifest([table(), table()]), code="duplicate_declaration")
x = copy.deepcopy(base); x["tables"][0]["fields"] *= 2
negative("duplicate-field", x, code="duplicate_declaration")
x = copy.deepcopy(base); x["tables"][0]["primary_key"] *= 2
negative("duplicate-key-column", x, code="duplicate_declaration")
x = copy.deepcopy(v); x["tables"][0]["indexes"] *= 2
negative("duplicate-index", x, code="duplicate_declaration")
x = copy.deepcopy(v); x["tables"][0]["indexes"][0]["columns"] *= 2
negative("duplicate-index-column", x, code="duplicate_declaration")
x = copy.deepcopy(base); x["tables"][0]["partition_key"] = [field_path("id"), field_path("id")]
negative("duplicate-partition-column", x, code="duplicate_declaration")
assoc = {"name": "self", "target": "T", "fields": [{"source": field_path("id"), "target": field_path("id")} ]}
x = copy.deepcopy(base); x["tables"][0]["associations"] = [assoc, assoc]
negative("duplicate-association", x, code="duplicate_declaration")
x = copy.deepcopy(base); x["tables"][0]["associations"] = [{**assoc, "fields": assoc["fields"] * 2}]
negative("duplicate-association-pair", x, code="duplicate_declaration")
negative("duplicate-view", manifest(views=[view(), view()]), code="duplicate_declaration")
x = manifest(views=[view()]); x["views"][0]["fields"] *= 2
negative("duplicate-view-output", x, code="duplicate_declaration")
x = manifest(views=[view(sources=[source("table", "T", "id"), source("table", "T", "id")])])
negative("duplicate-view-source", x, code="duplicate_declaration")
x = manifest(views=[view()]); x["views"][0]["primary_key"] *= 2
negative("duplicate-view-key", x, code="duplicate_declaration")
for dimension in ["primary", "index", "association-target", "association-source-field", "association-target-field", "view-source-table", "view-source-table-field", "view-source-view", "view-source-view-field", "view-primary", "view-index"]:
    x = copy.deepcopy(base)
    if dimension == "primary": x["tables"][0]["primary_key"] = [key("missing")]
    elif dimension == "index": x["tables"][0]["indexes"] = [{"name": "I", "kind": "opaque", "columns": [key("missing")]}]
    elif dimension.startswith("association"):
        item = copy.deepcopy(assoc)
        if dimension == "association-target": item["target"] = "missing"
        elif dimension == "association-source-field": item["fields"][0]["source"] = field_path("missing")
        else: item["fields"][0]["target"] = field_path("missing")
        x["tables"][0]["associations"] = [item]
    else:
        x["views"] = [view()]
        if dimension == "view-source-table": x["views"][0]["fields"][0]["sources"] = [source("table", "missing", "id")]
        elif dimension == "view-source-table-field": x["views"][0]["fields"][0]["sources"] = [source("table", "T", "missing")]
        elif dimension == "view-source-view": x["views"][0]["fields"][0]["sources"] = [source("view", "missing", "id")]
        elif dimension == "view-source-view-field": x["views"].append(view("Other")); x["views"][0]["fields"][0]["sources"] = [source("view", "Other", "missing")]
        elif dimension == "view-primary": x["views"][0]["primary_key"] = [key("missing")]
        else: x["views"][0]["indexes"] = [{"name": "I", "kind": "opaque", "columns": [key("missing")]}]
    negative("unresolved-" + dimension, x, code="unresolved_reference")
negative("view-self-cycle", manifest(views=[view(sources=[source("view", "V", "id")])]), code="view_dependency_cycle")
negative("view-two-cycle", manifest(views=[view("A", sources=[source("view", "B", "id")]), view("B", sources=[source("view", "A", "id")])]), code="view_dependency_cycle")
for payload in ["record", "records", "sample", "samples", "default", "defaults", "example", "examples", "value", "values", "data", "documents"]:
    negative("payload-" + payload, {**base, payload: ["forbidden"]}, code="data_payload_forbidden")
negative("input-bytes-over", raw=base_bytes + b" " * (65537 - len(base_bytes)), code="limit_exceeded", limit="manifest_bytes")
negative("json-depth-over", raw=b"[" * 33 + b"0" + b"]" * 33, code="limit_exceeded", limit="json_depth")
negative("tables-over", manifest([table("T" + str(i)) for i in range(17)]), code="limit_exceeded", limit="tables")
negative("fields-over", manifest([table(names=tuple("f" + str(i) for i in range(129)))]), code="limit_exceeded", limit="fields_per_table")
x = copy.deepcopy(base); x["tables"][0]["fields"].append({"path": field_path(*("p" + str(i) for i in range(9))), "type": "opaque"})
negative("path-depth-over", x, code="limit_exceeded", limit="path_depth")
negative("name-bytes-over", manifest([table("é" * 65)]), code="limit_exceeded", limit="name_utf8_bytes")
x = copy.deepcopy(base); x["tables"][0]["fields"][0]["type"] = "x" * 257
negative("type-bytes-over", x, code="limit_exceeded", limit="type_utf8_bytes")
x = manifest([table(names=tuple("f" + str(i) for i in range(9)))]); x["tables"][0]["primary_key"] = [key("f" + str(i)) for i in range(9)]
negative("key-width-over", x, code="limit_exceeded", limit="key_width")
x = copy.deepcopy(base); x["tables"][0]["indexes"] = [{"name": "I" + str(i), "kind": "opaque", "columns": [key("id")]} for i in range(17)]
negative("indexes-over", x, code="limit_exceeded", limit="indexes_per_owner")
x = manifest([table(names=tuple("f" + str(i) for i in range(9)))]); x["tables"][0]["indexes"] = [{"name": "I", "kind": "opaque", "columns": [key("f" + str(i)) for i in range(9)]}]
negative("index-columns-over", x, code="limit_exceeded", limit="index_columns")
x = copy.deepcopy(base); x["tables"][0]["associations"] = [{**copy.deepcopy(assoc), "name": "A" + str(i)} for i in range(65)]
negative("associations-over", x, code="limit_exceeded", limit="associations_total")
x = manifest([table(names=tuple("f" + str(i) for i in range(9)))]); x["tables"][0]["associations"] = [{"name": "self", "target": "T", "fields": [{"source": field_path("f" + str(i)), "target": field_path("f" + str(i))} for i in range(9)]}]
negative("association-fields-over", x, code="limit_exceeded", limit="association_fields")
negative("views-over", manifest(views=[view("V" + str(i)) for i in range(17)]), code="limit_exceeded", limit="views")
x = manifest(views=[view()]); x["views"][0]["fields"] = [{"name": "f" + str(i), "type": "opaque", "sources": []} for i in range(129)]; x["views"][0]["primary_key"] = [key("f0")]
negative("view-fields-over", x, code="limit_exceeded", limit="fields_per_view")
x = manifest([table(names=tuple("f" + str(i) for i in range(17)))], [view(sources=[source("table", "T", "f" + str(i)) for i in range(17)])])
negative("view-sources-over", x, code="limit_exceeded", limit="sources_per_view_field")
negative("aggregate-over", manifest([table(name, tuple("f" + str(i) for i in range(84))) for name in ["A", "B", "C"]]), code="limit_exceeded", limit="aggregate_declarations")
negative("citations-over", manifest([table(name, tuple("f" + str(i) for i in range(94))) for name in ["A", "B"]]), code="limit_exceeded", limit="emitted_citations")
negative("manifest-path-unnormalized", base, code="input_context_invalid", context={**CONTEXT, "manifest_path": "./schema/document.json"})
negative("manifest-path-traversal", base, code="input_context_invalid", context={**CONTEXT, "manifest_path": "../schema.json"})
negative("manifest-path-absolute", base, code="input_context_invalid", context={**CONTEXT, "manifest_path": "/schema.json"})
negative("manifest-path-backslash", base, code="input_context_invalid", context={**CONTEXT, "manifest_path": "schema\\document.json"})
negative("context-invalid-commit", base, code="input_context_invalid", context={**CONTEXT, "commit": "not-a-commit"})
negative("context-blank-repository", base, code="input_context_invalid", context={**CONTEXT, "repository": "  "})
negative("repository-context-over", base, code="limit_exceeded", limit="repository_utf8_bytes", context={**CONTEXT, "repository": "x" * 257})
negative("path-context-over", base, code="limit_exceeded", limit="manifest_path_utf8_bytes", context={**CONTEXT, "manifest_path": "x" * 257})
negative("cql-not-admitted", raw=(ROOT / "public/reservation.cql").read_bytes(), code="invalid_json")
CASES.append({"id": "missing-artifact", "input": None, "context": CONTEXT, "expect": "missing", "state": "document-schema-artifact-missing", "checklist": "request-schema-export", "expected_facts": 0, "expected_citations": 0})

write_json("cases.json", {"authorship": "All labels fixed by fixture author before reader execution; negatives require atomic refusal, not partial facts.", "cases": CASES})
write_json("comparison-labels.json", {"before": "comparison-before", "after": "comparison-after", "same_external_manifest_path": True, "expected": [{"change": "added", "kind": "DECLARES_FIELD", "table": "Queue", "path": field_path("new β")}, {"change": "removed", "kind": "DECLARES_FIELD", "table": "Queue", "path": field_path("old")}, {"change": "changed", "kind": "DECLARES_FIELD", "table": "Queue", "path": field_path("stable"), "before_type": "opaque:string", "after_type": "opaque:bytes"}], "unchanged_identity": {"table": "Queue", "path": field_path("id")}, "source_class_mismatch": "comparison_source_class_mismatch", "different_lineage": "comparison_lineage_mismatch", "claim": "explicit snapshot differences only; neither snapshot is called live or current"})

# Complete source-to-manifest mapping uses explicit known literal spans only.
cql = (ROOT / "public/reservation.cql").read_bytes()


def cql_span(needle, scope=None):
    start, end = scope or (0, len(cql))
    raw = needle.encode()
    found = cql.find(raw, start, end)
    assert found >= 0, needle
    assert cql.find(raw, found + 1, end) < 0, ("ambiguous hand span", needle)
    return {"start": found, "end": found + len(raw), "text": needle}


scopes = {
    "reservation.reservations_by_hotel_date": (cql.index(b"CREATE TABLE reservation.reservations_by_hotel_date"), cql.index(b"CREATE MATERIALIZED VIEW")),
    "reservation.reservations_by_guest": (cql.index(b"CREATE TABLE reservation.reservations_by_guest"), cql.index(b"CREATE TABLE reservation.guests")),
    "reservation.guests": (cql.index(b"CREATE TABLE reservation.guests"), len(cql)),
    "reservation.reservations_by_confirmation": (cql.index(b"CREATE MATERIALIZED VIEW"), cql.index(b"CREATE TABLE reservation.reservations_by_guest")),
}
udt_scope = (cql.index(b"CREATE TYPE reservation.address"), cql.index(b"CREATE TABLE reservation.reservations_by_hotel_date"))
derivation = []
for label in expected_declarations(PUBLIC_VALUE):
    kind, owner, meaning = label["kind"], label["owner"], label["meaning"]
    if kind == "DECLARES_SCHEMA_SET":
        spans = [cql_span("CREATE KEYSPACE reservation")]
        decision = "Neutral lineage uses external repository/path/format. Keyspace spelling survives only in exact qualified table/view names; replication settings are not neutral declarations."
    elif kind in ["DECLARES_TABLE", "DECLARES_VIEW"]:
        prefix = "CREATE TABLE " if kind == "DECLARES_TABLE" else "CREATE MATERIALIZED VIEW "
        spans = [cql_span(prefix + owner["name"], scopes[owner["name"]])]
        decision = "Exact qualified source name; no database, service or runtime identity."
    elif kind == "DECLARES_FIELD":
        segments = meaning["path"]
        first = segments[0]["name"]
        if len(segments) == 1:
            spans = [cql_span(first + " " + meaning["type"], scopes[owner["name"]])]
            decision = "Exact column spelling and opaque CQL type spelling, without a vendor type reader."
        elif segments[-1].get("kind") == "field":
            spans = [cql_span(segments[-1]["name"] + " text", udt_scope), cql_span("addresses map<text, frozen<address>>", scopes[owner["name"]])]
            decision = "Manual address UDT definition/use expansion under addresses/map_value; no runtime CQL parsing or canonical vendor-type claim."
        else:
            literal = {"emails": "emails set<text>", "phone_numbers": "phone_numbers list<text>", "addresses": "addresses map<text, frozen<address>>"}[first]
            spans = [cql_span(literal, scopes[owner["name"]])]
            decision = "Set/list both use neutral array_element with no ordering claim; map_value never traverses map keys. Type spelling remains opaque."
    elif kind in ["DECLARES_PRIMARY_KEY", "DECLARES_PARTITION_KEY"]:
        scope = scopes[owner["name"]]
        if owner["name"] == "reservation.guests":
            spans = [cql_span("guest_id uuid PRIMARY KEY", scope)]
        else:
            literal = {"reservation.reservations_by_hotel_date": "PRIMARY KEY ((hotel_id, start_date), room_number)", "reservation.reservations_by_guest": "PRIMARY KEY ((guest_last_name), hotel_id)", "reservation.reservations_by_confirmation": "PRIMARY KEY (confirm_number, hotel_id, start_date, room_number)"}[owner["name"]]
            key_span = cql_span(literal, scope)
            spans = [cql_span(meaning["path"][0]["name"], (key_span["start"], key_span["end"]))]
        decision = "Source key order and scalar/composite partition grouping are explicit. Direction omitted in neutral input defaults asc; that is not an ordering/runtime claim about partition keys."
    else:
        name = meaning["name"] if kind == "DECLARES_VIEW_FIELD" else owner["field"]
        spelling = dict(hotel_fields)[name]
        spans = [cql_span("SELECT * FROM reservation.reservations_by_hotel_date", scopes["reservation.reservations_by_confirmation"]), cql_span(name + " " + spelling, scopes["reservation.reservations_by_hotel_date"])]
        decision = "Manual wildcard expansion into six own output names with exact source-field references; null filters are not represented or inferred as a runtime freshness guarantee."
    start, end = PUBLIC_POSITIONS[label["pointer"]]
    derivation.append({"kind": kind, "json_pointer": label["pointer"], "json_span": {"start": start, "end": end}, "cql_spans": spans, "decision": decision})
assert len(derivation) == 61
write_json("public/derivation.json", {"method": "Hand-derived declarations and literal span lookup, before any JSON reader run; no CQL parser or vendor adapter.", "rows": derivation, "remaining_source_forms": [{"form": "license/comments", "disposition": "Original bytes/notice retained; no neutral data assertion."}, {"form": "CREATE TYPE address", "disposition": "All five members explicitly expanded at addresses/map_value; source definition and use both cited."}, {"form": "replication options and WITH comment strings", "disposition": "Outside neutral structural declaration vocabulary; not copied as data/default/example payload."}, {"form": "view WHERE NOT NULL", "disposition": "Outside round-one declaration-only semantics; no implicit nullability/filter/runtime statement."}]})
write_json("public/receipt.json", {"repository": "https://github.com/jeffreyscarpenter/cassandra-guide", "commit": "bed58e7e51768f9f30dd435e74f31e5b3b2649a4", "path": "resources/reservation.cql", "git_blob": "2c1c098ff706f63f28be772514e7b7a664878774", "bytes": len(cql), "sha256": hashlib.sha256(cql).hexdigest(), "license": "Apache-2.0", "copyright": "Copyright (C) 2016 Jeff Carpenter", "license_evidence": "Original file header retained verbatim; canonical Apache license text included from https://www.apache.org/licenses/LICENSE-2.0.txt", "license_sha256": hashlib.sha256((ROOT / "public/LICENSE-2.0.txt").read_bytes()).hexdigest(), "source_url": "https://raw.githubusercontent.com/jeffreyscarpenter/cassandra-guide/bed58e7e51768f9f30dd435e74f31e5b3b2649a4/resources/reservation.cql", "semantics_reference": "https://cassandra.apache.org/doc/3.11/cassandra/cql/ddl.html#clustering-order", "qualification": "External derivation oracle only; no admitted CQL input, Cassandra pack, live store, adapter or vendor-limit validation."})

print({"cases": len(CASES), "accepted_labels": sum(case.get("declarations", 0) for case in CASES), "public_declarations": len(derivation), "reader_runs": 0})

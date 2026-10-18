#!/usr/bin/env python3
"""Static independent source/IDL/range witnesses; no production or SCIP read."""
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent


def digest(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def unique(raw, pattern):
    matches = list(re.finditer(pattern, raw, re.M))
    assert len(matches) == 1, (pattern, len(matches))
    return matches[0]


def main():
    modules, fields = [], []
    cases = [
        ("public", ".gen/go/health/health.go", "idl", "thrift/health.thrift", "health", [
            ("HealthStatus", "Ok", "ok", 1, ["GetOk"]),
            ("HealthStatus", "Msg", "msg", 2, ["GetMsg", "IsSetMsg"]),
            ("Meta_Health_Result", "Success", "success", 0, ["GetSuccess", "IsSetSuccess"]),
        ]),
        ("neutral", "gen/packet.go", "neutral", "idl/packet.thrift", "packet", [
            ("Envelope", "Key", "key", 1, ["GetKey", "IsSetKey"]),
            ("Envelope", "Count", "count", 7, []),
            ("Envelope", "Ready", "ready", 9, []),
            ("Envelope", "Payload", "payload", 13, []),
            ("Probe_Check_Result", "Success", "success", 0, ["GetSuccess"]),
        ]),
        ("neutral", "renamed/packet.go", "neutral", "idl/renamed.thrift", "packet", [
            ("Envelope", "Identifier", "identifier", 1, ["GetIdentifier"]),
        ]),
    ]
    for population, path, ipop, ipath, scope, entries in cases:
        raw = (ROOT / "testdata" / population / path).read_bytes()
        idl = (ROOT / "testdata" / ipop / ipath).read_bytes()
        literal = unique(raw, rb'^const rawIDL = ("(?:\\.|[^"\\])*")$')
        embedded = json.loads(literal.group(1)).encode()
        assert embedded == idl, "embedded Raw must equal independent retained IDL bytes"
        declared_sha1 = unique(raw, rb'SHA1:\s*"([0-9a-f]{40})"').group(1).decode()
        assert hashlib.sha1(idl).hexdigest() == declared_sha1
        name = unique(raw, rb'Name:\s*"([^"]+)"').group(1).decode()
        assert name == scope
        package = unique(raw, rb'Package:\s*"([^"]+)"').group(1).decode()
        filepath = unique(raw, rb'FilePath:\s*"([^"]+)"').group(1).decode()
        modules.append(dict(population=population, generated_path=path, generated_digest=digest(raw), idl_population=ipop, idl_path=ipath, idl_digest=digest(idl), embedded_raw_digest=digest(embedded), embedded_sha1=declared_sha1, embedded_raw_literal_span=list(literal.span(1)), name=name, package=package, file_path=filepath, scope=scope,
                            scope_basis="namespace go packet" if scope == "packet" else "No go namespace; exact embedded health.thrift basename"))
        for message, go_name, thrift_name, number, accessors in entries:
            struct = unique(raw, rb'^type '+re.escape(message.encode())+rb' struct \{([\s\S]*?)\}')
            field = unique(struct.group(1), rb'\b('+go_name.encode()+rb')\s+[^\n;]+')
            offset = struct.start(1)
            span = [offset+field.start(1), offset+field.end(1)]
            function = unique(raw, rb'^func \(v \*'+message.encode()+rb'\) ToWire\(\) \(wire.Value, error\) \{([\s\S]*?)^\}')
            wire = unique(function.group(1), rb'wire.Field\{ID:\s*('+str(number).encode()+rb')\b')
            wire_span = [function.start(1)+wire.start(1), function.start(1)+wire.end(1)]
            if number:
                block = unique(idl, rb'struct '+message.encode()+rb'\s*\{([\s\S]*?)\}')
                declaration = unique(block.group(1), rb'(?m)^\s*'+str(number).encode()+rb':[^\n]*\b'+thrift_name.encode()+rb'\b[^\n]*')
                idl_span = [block.start(1)+declaration.start(), block.start(1)+declaration.end()]
                idl_basis = "Explicit source field number/name; type spelling retained exactly"
            else:
                method = b"health" if population == "public" else b"check"
                declaration = unique(idl, rb'(?m)^\s*[^\n]+\s'+method+rb'\(\)[^\n]*')
                idl_span = list(declaration.span())
                idl_basis = "Explicit non-void service return; implicit success field0 convention, not a written field0 declaration"
            bindings = [dict(identifier=go_name, kind="field", span=span)]
            for accessor in accessors:
                method = unique(raw, rb'^func \(v \*'+message.encode()+rb'\) ('+accessor.encode()+rb')\(')
                bindings.append(dict(identifier=accessor, kind="accessor", span=list(method.span(1))))
            fields.append(dict(population=population, generated_path=path, generated_digest=digest(raw), scope=scope, generated_message=message, go_name=go_name, thrift_name=thrift_name, field_number=number, object=scope+"."+message+"#"+str(number), bindings=bindings, wire_id_span=wire_span, idl_population=ipop, idl_path=ipath, idl_digest=digest(idl), idl_span=idl_span, idl_basis=idl_basis))
    result = dict(schema="t522-source-field-witness-v1", record_date="2026-10-18", candidate_runs=0, indexer_runs=0, generator_runs=0, modules=modules, fields=fields,
                  rename_control="The two authored packet.Envelope#1 entries keep numeric identity across key→identifier and matching field/accessor renames; no name-derived family rewrite.",
                  nonclaims="Static source-only witnesses do not establish actual SCIP definition joins or production declaration-family translation. Module.Package is retained provenance, never silently equated to the independent SCIP package family.")
    raw = (json.dumps(result, indent=2)+"\n").encode()
    target = ROOT / "field-witnesses.json"
    if target.exists():
        assert target.read_bytes() == raw, "frozen witnesses changed"
    else:
        with target.open("xb") as stream:
            stream.write(raw)
    print(json.dumps(dict(modules=len(modules), field_witnesses=len(fields), definition_bindings=sum(len(f["bindings"]) for f in fields), digest=digest(raw))))


if __name__ == "__main__":
    main()

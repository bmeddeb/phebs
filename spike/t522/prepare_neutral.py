#!/usr/bin/env python3
"""Author source controls only; never invoke a Thrift/Go/SCIP tool.

The generated-shaped fixtures are explicitly authored, not compiler output.
They supply no public-generator quality result. IDs/names are chosen from the
committed IDL before any candidate execution.
"""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def retain(path, raw):
    if isinstance(raw, str):
        raw = raw.encode()
    target = ROOT / "testdata/neutral" / path
    target.parent.mkdir(parents=True, exist_ok=True)
    if target.exists():
        assert target.read_bytes() == raw, "refusing to replace neutral source"
    else:
        with target.open("xb") as stream:
            stream.write(raw)


def generated(raw, body):
    return '''// Evaluator-authored thriftrw-shaped control, not compiler output.
// No generator execution or independent public quality is claimed.
package packet

import (
    "go.uber.org/thriftrw/thriftreflect"
    "go.uber.org/thriftrw/wire"
)

''' + body + '''
var ThriftModule = &thriftreflect.ThriftModule{
    Name: "packet", Package: "example.com/t522-neutral/packet", FilePath: "packet.thrift",
    SHA1: "''' + hashlib.sha1(raw.encode()).hexdigest() + '''", Raw: rawIDL,
}
const rawIDL = ''' + json.dumps(raw, ensure_ascii=False) + "\n"


def main():
    raw = '''namespace go packet
struct Envelope {
  1: required string key
  7: optional i64 count
  9: optional bool ready
  13: optional binary payload
}
struct Ambiguous {
  1: optional string left
  1: optional string right
}
service Probe {
  string check()
}
'''
    renamed = '''namespace go packet
struct Envelope {
  1: required string identifier
}
'''
    body = '''type Envelope struct {
    Key string
    Count *int64
    Ready *bool
    Payload []byte
}
func (v *Envelope) ToWire() (wire.Value, error) {
    fields := []wire.Field{
        wire.Field{ID: 1, Value: wire.NewValueString(v.Key)},
        wire.Field{ID: 7, Value: wire.NewValueI64(0)},
        wire.Field{ID: 9, Value: wire.NewValueBool(false)},
        wire.Field{ID: 13, Value: wire.NewValueBinary(v.Payload)},
    }
    return wire.NewValueStruct(wire.Struct{Fields: fields}), nil
}
func (v *Envelope) GetKey() string { return v.Key }
func (v *Envelope) IsSetKey() bool { return v != nil }
func (v *Envelope) ComputeKey() string { return v.Key }
type Probe_Check_Result struct { Success *string }
func (v *Probe_Check_Result) ToWire() (wire.Value, error) {
    fields := []wire.Field{wire.Field{ID: 0, Value: wire.NewValueString("")}}
    return wire.NewValueStruct(wire.Struct{Fields: fields}), nil
}
func (v *Probe_Check_Result) GetSuccess() string { return *v.Success }
type Ambiguous struct { Left *string; Right *string }
func (v *Ambiguous) ToWire() (wire.Value, error) {
    fields := []wire.Field{wire.Field{ID: 1}, wire.Field{ID: 2}}
    return wire.NewValueStruct(wire.Struct{Fields: fields}), nil
}
'''
    renamed_body = '''type Envelope struct { Identifier string }
func (v *Envelope) ToWire() (wire.Value, error) {
    fields := []wire.Field{wire.Field{ID: 1, Value: wire.NewValueString(v.Identifier)}}
    return wire.NewValueStruct(wire.Struct{Fields: fields}), nil
}
func (v *Envelope) GetIdentifier() string { return v.Identifier }
'''
    use = '''package use

import (
    "reflect"
    packet "example.com/t522-neutral/gen"
    renamed "example.com/t522-neutral/renamed"
    "example.com/t522-neutral/plain"
)

// A snowman ☃ makes character offsets differ from byte offsets below.
func use(v *packet.Envelope, r *packet.Probe_Check_Result, bad *packet.Ambiguous, fake *plain.Envelope, n *renamed.Envelope) {
    v.Key = "value"
    _ = v.Key
    _ = v.GetKey()
    _ = v.IsSetKey()
    _ = v.Count
    _ = v.Ready
    _ = v.Payload
    _ = packet.Envelope{Key: "x", Count: nil, Ready: nil, Payload: nil}
    _ = r.Success
    _ = r.GetSuccess()
    _ = n.Identifier
    _ = n.GetIdentifier()
    fake.Key = "lookalike"
    _ = fake.Count
    _ = v.ComputeKey()
    _ = bad.Left
    _ = bad.Right
    _ = reflect.ValueOf(v).FieldByName("Key")
}
'''
    retain("go.mod", "module example.com/t522-neutral\n\ngo 1.26.5\n\nrequire go.uber.org/thriftrw v1.34.0\n")
    retain("idl/packet.thrift", raw)
    retain("idl/renamed.thrift", renamed)
    retain("gen/packet.go", generated(raw, body))
    retain("renamed/packet.go", generated(renamed, renamed_body))
    retain("plain/packet.go", "// Authored ordinary Go lookalike: no module descriptor.\npackage plain\ntype Envelope struct { Key string; Count int64 }\n")
    retain("use/use.go", use)
    print("retained7authoredsourcefiles; candidate/indexer/generator runs0")


if __name__ == "__main__":
    main()

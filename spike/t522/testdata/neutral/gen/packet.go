// Evaluator-authored thriftrw-shaped control, not compiler output.
// No generator execution or independent public quality is claimed.
package packet

import (
    "go.uber.org/thriftrw/thriftreflect"
    "go.uber.org/thriftrw/wire"
)

type Envelope struct {
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

var ThriftModule = &thriftreflect.ThriftModule{
    Name: "packet", Package: "example.com/t522-neutral/packet", FilePath: "packet.thrift",
    SHA1: "add3300b733e31f22ee05fe41e1298f57de3c63b", Raw: rawIDL,
}
const rawIDL = "namespace go packet\nstruct Envelope {\n  1: required string key\n  7: optional i64 count\n  9: optional bool ready\n  13: optional binary payload\n}\nstruct Ambiguous {\n  1: optional string left\n  1: optional string right\n}\nservice Probe {\n  string check()\n}\n"

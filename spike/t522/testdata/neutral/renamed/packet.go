// Evaluator-authored thriftrw-shaped control, not compiler output.
// No generator execution or independent public quality is claimed.
package packet

import (
    "go.uber.org/thriftrw/thriftreflect"
    "go.uber.org/thriftrw/wire"
)

type Envelope struct { Identifier string }
func (v *Envelope) ToWire() (wire.Value, error) {
    fields := []wire.Field{wire.Field{ID: 1, Value: wire.NewValueString(v.Identifier)}}
    return wire.NewValueStruct(wire.Struct{Fields: fields}), nil
}
func (v *Envelope) GetIdentifier() string { return v.Identifier }

var ThriftModule = &thriftreflect.ThriftModule{
    Name: "packet", Package: "example.com/t522-neutral/packet", FilePath: "packet.thrift",
    SHA1: "2cbfef329ff52d153e104a9f63fa41775bb1eb13", Raw: rawIDL,
}
const rawIDL = "namespace go packet\nstruct Envelope {\n  1: required string identifier\n}\n"

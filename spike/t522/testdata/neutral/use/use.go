package use

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

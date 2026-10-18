// T51.1 authored controls supplement the independently labelled public source.
package use

import (
	"example.com/t511/schema"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type unrelated struct{ Value string }

func Direct(item *schema.Item, count int32) {
	_ = item.Value
	item.Value = "assigned"
	_ = item.GetValue()
	getter := item.GetValue
	_ = getter()
	_ = schema.Item{Value: "literal", Count: &count, Labels: []string{"x"}}
	_ = item.Count
	_ = item.GetCount()
	_ = item.Labels[0]
	item.Attributes["key"] = "value"
	_ = item.GetAttributes()
	_ = item.Nested.Value
	_ = item.GetNested().GetValue()
	_ = schema.Item_Nested{Value: "nested"}
	_ = unrelated{Value: "look-alike"}.Value
}

// Runtime reflection, dynamic messages, unknown fields and oneof wrappers are
// explicitly outside this direct tagged-field/getter recipe.
func Unsupported(item *schema.Item, descriptor protoreflect.MessageDescriptor) {
	reflected := item.ProtoReflect()
	field := reflected.Descriptor().Fields().ByName("value")
	_ = reflected.Get(field)
	_ = reflected.GetUnknown()
	_ = dynamicpb.NewMessage(descriptor).Get(field)
	_ = item.GetText()
	_ = (&schema.Item_Text{Text: "oneof"}).Text
}

package shared

import (
	"go.uber.org/thriftrw/thriftreflect"
	"go.uber.org/thriftrw/wire"
)

type Item struct {
	WorkflowId *string
}

func (v *Item) ToWire() (wire.Value, error) {
	fields := make([]wire.Field, 0, 1)
	if v.WorkflowId != nil {
		fields = append(fields, wire.Field{
			ID:    10,
			Value: wire.NewValueString(*v.WorkflowId),
		})
	}
	return wire.NewValueStruct(wire.Struct{Fields: fields}), nil
}

func (v *Item) GetWorkflowId() string {
	if v != nil && v.WorkflowId != nil {
		return *v.WorkflowId
	}
	return ""
}

func (v *Item) IsSetWorkflowId() bool {
	return v != nil && v.WorkflowId != nil
}

var ThriftModule = &thriftreflect.ThriftModule{
	Name:     "shared",
	Package:  "example.invalid/t222-realindex/generated",
	FilePath: "shared.thrift",
	SHA1:     "260a839bc41b29c552e77748aaaca90f2dd76d7b",
	Raw:      rawIDL,
}

const rawIDL = "namespace go example.shared\nstruct Item {\n  10: optional string workflowId\n}\n"

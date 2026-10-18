package use

import (
	"reflect"

	neutral "example.com/t521-neutral/gen"
	"example.com/t521-neutral/fake"
)

func Exercise(p *neutral.Parcel, result *neutral.MessengerSendResult, ambiguous *neutral.Ambiguous, lookalike *fake.Parcel) {
	count := int64(7)
	code := "snowman ☃"
	_ = neutral.Parcel{Code: code, Count: &count, Tags: []string{"tag"}, Attrs: map[string]string{"key": "value"}}
	p.Code = code
	_ = p.Code
	_ = p.Count
	_ = p.Tags
	_ = p.Attrs
	_ = neutral.MessengerSendResult{Success: &code}
	_ = result.Success
	_ = lookalike.Code
	lookalike.Count = count
	_ = p.GetCode()
	_ = reflect.ValueOf(p).Elem().FieldByName("Code")
	_ = ambiguous.Left
	_ = ambiguous.Right
}

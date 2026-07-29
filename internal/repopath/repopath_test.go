package repopath

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "nested", value: "service/api.proto", valid: true},
		{name: "interior dash", value: "service/-generated.go", valid: true},
		{name: "empty"},
		{name: "dot", value: "."},
		{name: "leading dash", value: "-option.go"},
		{name: "absolute", value: "/service/api.proto"},
		{name: "backslash", value: `service\api.proto`},
		{name: "parent", value: "service/../api.proto"},
		{name: "empty component", value: "service//api.proto"},
		{name: "control", value: "service/api\n.proto"},
		{name: "invalid utf8", value: string([]byte{'a', '/', 0xff})},
		{name: "too long", value: strings.Repeat("a", MaxBytes+1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := Validate(testCase.value)
			if testCase.valid && err != nil {
				t.Fatalf("Validate(%q) = %v", testCase.value, err)
			}
			if !testCase.valid && err == nil {
				t.Fatalf("Validate(%q) succeeded", testCase.value)
			}
		})
	}
}

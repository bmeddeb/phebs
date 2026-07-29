package reponame

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "host path", value: "github.com/acme/service", valid: true},
		{name: "interior space", value: "local/acme service", valid: true},
		{name: "empty"},
		{name: "absolute", value: "/acme/service"},
		{name: "backslash", value: `acme\service`},
		{name: "parent", value: "acme/../service"},
		{name: "empty component", value: "acme//service"},
		{name: "intermediate git", value: "acme.git/service"},
		{name: "case folded intermediate git", value: "acme.GIT/service"},
		{name: "leading whitespace", value: " acme/service"},
		{name: "trailing unicode whitespace", value: "acme/service\u2003"},
		{name: "control", value: "acme/ser\nvice"},
		{name: "invalid utf8", value: string([]byte{'a', '/', 0xff})},
		{name: "too long", value: strings.Repeat("a", MaxBytes+1)},
	}
	for _, testCase := range tests {
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

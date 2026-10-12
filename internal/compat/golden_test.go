package compat

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"sort"
	"testing"
)

type goldenChanges struct {
	Schema string `json:"schema"`
	Cases  []struct {
		ID        string `json:"id"`
		Rationale string `json:"rationale"`
		Path      string `json:"path"`
		Before    string `json:"before"`
		After     string `json:"after"`
		Expect    struct {
			Compatible bool     `json:"compatible"`
			Rules      []string `json:"rules"`
		} `json:"expect"`
		// EngineConservative pins a recorded disagreement where Buf reports a break the
		// independent truth calls compatible. Only this direction is admissible: a false
		// "compatible" would be a release blocker, not a record.
		EngineConservative *struct {
			Rules  []string `json:"rules"`
			Reason string   `json:"reason"`
		} `json:"engine_conservative"`
	} `json:"cases"`
	NotAsserted map[string]string `json:"not_asserted"`
}

// TestGoldenWireChanges runs every independently authored golden change through
// the real sandboxed Buf and requires the exact verdict and rule set (T53.1).
func TestGoldenWireChanges(t *testing.T) {
	checker := requireChecker(t)
	raw, err := os.ReadFile("testdata/golden_changes.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden goldenChanges
	if err := json.Unmarshal(raw, &golden); err != nil || golden.Schema != "t53.1-golden-changes-v1" {
		t.Fatalf("golden changes are unreadable: %v", err)
	}
	asserted := map[string]bool{}
	for _, c := range golden.Cases {
		t.Run(c.ID, func(t *testing.T) {
			result, err := checker.Check(context.Background(), Request{
				Lineage: "contract_golden_" + c.ID,
				Before:  []File{{Path: c.Path, Content: c.Before}},
				After:   []File{{Path: c.Path, Content: c.After}},
			})
			if err != nil {
				t.Fatalf("%s: %v", c.Rationale, err)
			}
			rules := make([]string, 0, len(result.Violations))
			for _, violation := range result.Violations {
				if !slices.Contains(rules, violation.Rule) {
					rules = append(rules, violation.Rule)
				}
			}
			sort.Strings(rules)
			want := append([]string{}, c.Expect.Rules...)
			wantCompatible := c.Expect.Compatible
			if c.EngineConservative != nil {
				if !c.Expect.Compatible || len(c.Expect.Rules) != 0 || len(c.EngineConservative.Rules) == 0 || c.EngineConservative.Reason == "" {
					t.Fatal("an engine disagreement may only record a conservative break against a compatible truth")
				}
				want, wantCompatible = append([]string{}, c.EngineConservative.Rules...), false
			}
			sort.Strings(want)
			if result.Compatible != wantCompatible || !slices.Equal(rules, want) {
				t.Fatalf("%s\ngot compatible=%v rules=%v, want compatible=%v rules=%v",
					c.Rationale, result.Compatible, rules, wantCompatible, want)
			}
		})
		for _, rule := range c.Expect.Rules {
			asserted[rule] = true
		}
	}
	// Every WIRE rule is either asserted by a golden or explicitly recorded as not asserted.
	for _, rule := range []string{
		"FIELD_SAME_DEFAULT", "FIELD_SAME_ONEOF", "FILE_SAME_PACKAGE", "MESSAGE_SAME_REQUIRED_FIELDS",
		"RESERVED_ENUM_NO_DELETE", "RESERVED_MESSAGE_NO_DELETE", "RPC_SAME_CLIENT_STREAMING",
		"RPC_SAME_IDEMPOTENCY_LEVEL", "RPC_SAME_REQUEST_TYPE", "RPC_SAME_RESPONSE_TYPE",
		"RPC_SAME_SERVER_STREAMING", "ENUM_VALUE_NO_DELETE_UNLESS_NUMBER_RESERVED",
		"FIELD_NO_DELETE_UNLESS_NUMBER_RESERVED", "FIELD_WIRE_COMPATIBLE_CARDINALITY", "FIELD_WIRE_COMPATIBLE_TYPE",
	} {
		if !asserted[rule] && golden.NotAsserted[rule] == "" {
			t.Errorf("WIRE rule %s has neither a golden nor a recorded reason", rule)
		}
	}
}

func TestGoldenUncompilableSourceIsRefusedNotJudged(t *testing.T) {
	checker := requireChecker(t)
	_, err := checker.Check(context.Background(), Request{
		Lineage: "contract_golden_uncompilable",
		Before:  []File{{Path: "shop/v1/shop.proto", Content: "syntax = \"proto3\";\npackage shop.v1;\nmessage M { string a = 1; }\n"}},
		After:   []File{{Path: "shop/v1/shop.proto", Content: "syntax = \"proto3\";\npackage shop.v1;\nmessage M { string a = ; }\n"}},
	})
	if err == nil {
		t.Fatal("a source that does not compile produced a verdict")
	}
}

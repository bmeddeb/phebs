//go:build linux

package t421

import (
	"slices"
	"testing"
)

func TestLinuxGoBuildLifetimeInputShape(t *testing.T) {
	base := inputTreeCensus{rows: []inputTreeEntry{{name: "inputs", mode: 0o700}, {name: "inputs/sentinel", mode: 0o400, size: uint64(len(linuxGoLifetimeSentinel))}}}
	if !goLifetimeInputShape(base) {
		t.Fatal("fixed lifetime sentinel refused")
	}
	for _, test := range []struct {
		name   string
		change func(*inputTreeCensus)
	}{
		{"missing", func(c *inputTreeCensus) { c.rows = c.rows[:1] }},
		{"extra", func(c *inputTreeCensus) { c.rows = append(c.rows, inputTreeEntry{name: "inputs/extra", mode: 0o400}) }},
		{"wrong-root", func(c *inputTreeCensus) { c.rows[0].name = "foreign" }},
		{"executable", func(c *inputTreeCensus) { c.rows[1].mode = 0o500 }},
		{"wrong-size", func(c *inputTreeCensus) { c.rows[1].size++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := base
			copy.rows = slices.Clone(base.rows)
			test.change(&copy)
			if goLifetimeInputShape(copy) {
				t.Fatal("nonfixed lifetime input admitted")
			}
		})
	}
}

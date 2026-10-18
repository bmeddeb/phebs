package main

import (
	"fmt"
	"strings"
	"testing"
)

// Supplemental implementation-boundary controls; the original source oracle,
// fixture inventory and quality denominators are unchanged.
func TestSupplementalKeynumAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, count, boundary string
		keys                  int
	}{
		{name: "at-limit", count: "64", keys: 64},
		{name: "over-limit", count: "65", keys: 65, boundary: "resource-limit"},
		{name: "large-canonical", count: `"999999999999999999999999"`, keys: 1, boundary: "resource-limit"},
		{name: "negative-is-semantic", count: "-1", keys: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &engine{modules: map[string]module{"supplemental\x00go.mod": {Version: "v9.17.2", Valid: true}}, commands: map[string]commandSpec{}}
			if err := e.addCommand([]byte(`{"EVAL":{"key_specs":[{"flags":["RW","ACCESS","UPDATE"],"begin_search":{"index":{"pos":2}},"find_keys":{"keynum":{"keynumidx":0,"firstkey":1,"step":1}}}]}}`)); err != nil {
				t.Fatal(err)
			}
			raw := []byte(fmt.Sprintf("package p\nimport redis \"github.com/redis/go-redis/v9\"\nfunc f(c *redis.Client, ctx any) { c.Do(ctx, \"EVAL\", \"return 1\", %s%s) }\n", tc.count, strings.Repeat(`, "k"`, tc.keys)))
			f := inputFile{Population: "supplemental", Repository: "synthetic", Path: "boundary.go", Role: "go-source", Digest: sha(raw), Bytes: len(raw)}
			got := e.goBlob(f, raw)
			if got.Boundary != tc.boundary {
				t.Fatalf("boundary=%q, want %q", got.Boundary, tc.boundary)
			}
			if tc.boundary != "" {
				if got.Status != "refused" || len(got.Sites)+len(got.Native)+len(got.Principals)+len(got.ACLPairs) != 0 {
					t.Fatal("over-limit count retained partial facts")
				}
			} else if tc.count == "64" {
				if got.Status != "processed" || len(got.Sites) != 1 || len(got.Sites[0].Keys) != 64 {
					t.Fatal("at-limit count failed to retain all script positions")
				}
				for _, key := range got.Sites[0].Keys {
					if key.Predicate != "PASSES_REDIS_KEY_TO_SCRIPT" || !key.ScriptPass || len(key.Access) != 0 || key.ExpectedKind != "" {
						t.Fatal("script position inherited command access or kind")
					}
				}
			} else if got.Status != "processed" || len(got.Sites) != 1 || !contains(got.Sites[0].Gaps, "incomplete-key-specification") {
				t.Fatal("negative count lost its semantic refusal")
			}
		})
	}
}

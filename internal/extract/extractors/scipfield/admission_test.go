package scipfield

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

func committedPublicCorpus(t *testing.T) memoryCorpus {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "spike", "t511", "testdata", "public")
	c := memoryCorpus{repo: "evaluation.local/t511-public", commit: "6c4c4b5897f11fb26619fc980c114b286c679606", files: map[string]string{}}
	if err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		c.files[filepath.ToSlash(rel)] = string(content)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCommittedOpenAPIAdmission(t *testing.T) {
	if New().Version() != "1.4.0" || NewCommittedOpenAPI().Version() != CommittedOpenAPIVersion {
		t.Fatal("admission versions must remain distinct")
	}
	for _, test := range []struct {
		name      string
		mutate    func(*memoryCorpus)
		wantFacts int
		wantError bool
	}{
		{name: "admitted frozen public input", wantFacts: 25},
		{name: "missing index is an explicit gap", mutate: func(c *memoryCorpus) { delete(c.files, "index.scip") }},
		{name: "alternate committed index path", wantError: true, mutate: func(c *memoryCorpus) { c.typedPath = "typed/index.scip"; c.files[c.typedPath] = c.files["index.scip"] }},
		{name: "unsupported generator", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.pb.go"
			c.files[p] = strings.Replace(c.files[p], "protoc-gen-go v1.36.11", "protoc-gen-go v1.36.12", 1)
		}},
		{name: "conflicting extra generator header", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.pb.go"
			c.files[p] = "// protoc-gen-go v1.36.12\n" + c.files[p]
		}},
		{name: "empty descriptor before valid descriptor", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.pb.go"
			c.files[p] = strings.Replace(c.files[p], "const file_profiling_proto_service_proto_rawDesc", "const extra_rawDesc = \"\"\n\nconst file_profiling_proto_service_proto_rawDesc", 1)
		}},
		{name: "empty descriptor after valid descriptor", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.pb.go"
			c.files[p] += "\nconst extra_rawDesc = \"\"\n"
		}},
		{name: "opaque API", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.pb.go"
			c.files[p] = strings.ReplaceAll(c.files[p], "open.v1", "opaque.v1")
		}},
		{name: "missing source marker", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.pb.go"
			c.files[p] = strings.Replace(c.files[p], "// source: profiling/proto/service.proto", "// source: missing.proto", 1)
		}},
		{name: "descriptor source identity drift", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.proto"
			c.files[p] = strings.Replace(c.files[p], "bool enabled = 1;", "bool changed = 1;", 1)
		}},
		{name: "unsupported proto2", wantError: true, mutate: func(c *memoryCorpus) {
			p := "profiling/proto/service.proto"
			c.files[p] = strings.Replace(c.files[p], `syntax = "proto3";`, `syntax = "proto2";`, 1)
		}},
		{name: "foreign SCIP symbol scheme", wantError: true, mutate: func(c *memoryCorpus) {
			index := scip.Index{}
			if err := proto.Unmarshal([]byte(c.files["index.scip"]), &index); err != nil {
				t.Fatal(err)
			}
			for _, doc := range index.Documents {
				for _, occ := range doc.Occurrences {
					occ.Symbol = strings.Replace(occ.Symbol, "scip-go gomod google.golang.org/grpc ", "other gomod google.golang.org/grpc ", 1)
				}
			}
			raw, err := proto.Marshal(&index)
			if err != nil {
				t.Fatal(err)
			}
			c.files["index.scip"] = string(raw)
		}},
		{name: "unadmitted indexer", wantError: true, mutate: func(c *memoryCorpus) {
			index := scip.Index{}
			if err := proto.Unmarshal([]byte(c.files["index.scip"]), &index); err != nil {
				t.Fatal(err)
			}
			index.Metadata.ToolInfo.Version = "0.2.8"
			raw, err := proto.Marshal(&index)
			if err != nil {
				t.Fatal(err)
			}
			c.files["index.scip"] = string(raw)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := committedPublicCorpus(t)
			if test.mutate != nil {
				before := make(map[string]string, len(c.files))
				for path, content := range c.files {
					before[path] = content
				}
				test.mutate(&c)
				changed := len(before) != len(c.files)
				for path, content := range before {
					if c.files[path] != content {
						changed = true
					}
				}
				if !changed {
					t.Fatal("input refusal control did not mutate its source")
				}
			}
			facts := 0
			coverage, err := NewCommittedOpenAPI().Extract(context.Background(), c, func(sdk.Fact) error { facts++; return nil })
			if (err != nil) != test.wantError || facts != test.wantFacts {
				t.Fatalf("facts=%d err=%v", facts, err)
			}
			if test.name == "missing index is an explicit gap" && (len(coverage.Protocols) != 1 || coverage.Protocols[0] != "scip-index-absent") {
				t.Fatal("missing input gap lost")
			}
		})
	}
}

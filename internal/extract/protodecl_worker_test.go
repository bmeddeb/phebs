package extract

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/store"
)

// PHEBS-40: before revision 1, a .proto file with an extension group refused
// the whole proto-contract run at extractor execution, and one with a
// conflicting repeated field number emitted two assertions the evidence store
// refuses at staging. Either way Contract Atlas served no protobuf for the
// repository. Each such file is now one PROTO_EXTRACTION_GAP and the rest of
// the repository publishes through the real worker and staging rules.
func TestProtoDeclGappedFilesStillPublish(t *testing.T) {
	files := map[string]string{
		"api/greeter.proto": `syntax = "proto3";
package acme;
service Greeter { rpc Hello(Req) returns (Res); }
message Req { string name = 1; }
message Res { string text = 1; }
`,
		"legacy/groups.proto": `syntax = "proto2";
package legacy;
message Base { extensions 100 to max; }
extend Base { optional group Grp = 100 { optional string x = 1; } }
`,
		"legacy/quotes.proto": `syntax = "proto2";
package legacy;
message Foo {
  optional string a = 1;
  optional int32 b = 1;
}
`,
	}
	repo := &store.Repo{Name: "example.com/protos", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus:  grpcWorkerCorpusFactory{files: files},
		Extractors: []Extractor{protodecl.New()},
	}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !evidence.published || evidence.aborted {
		t.Fatalf("run state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
	gaps := map[string]string{}
	operations := 0
	for _, batch := range evidence.batches {
		for _, assertion := range batch.asserts {
			switch {
			case assertion.Predicate == "PROTO_EXTRACTION_GAP":
				gaps[assertion.Subject] = assertion.Object
			case assertion.Subject != "api/greeter.proto":
				t.Fatalf("gapped file declared %+v", assertion)
			case assertion.Predicate == "DECLARES_OPERATION":
				operations++
			}
		}
	}
	if len(gaps) != 2 || gaps["legacy/groups.proto"] != "extension_group" ||
		gaps["legacy/quotes.proto"] != "duplicate_field_number" {
		t.Fatalf("gaps = %v", gaps)
	}
	if operations != 1 || evidence.publishedWith.UnresolvedCount != 2 {
		t.Fatalf("operations = %d, coverage = %+v", operations, evidence.publishedWith)
	}
}

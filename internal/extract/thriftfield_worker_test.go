package extract

import (
	"context"
	"crypto/sha1" //nolint:gosec // fixture follows thriftrw's descriptor identity.
	"encoding/hex"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestThriftFieldReferencePublishesThroughTrustedWorker(t *testing.T) {
	raw := "namespace go example.shared\nstruct Item {\n  10: optional string workflowId\n}\n"
	sum := sha1.Sum([]byte(raw)) //nolint:gosec // fixture descriptor identity.
	generated := `package shared
import (
	"go.uber.org/thriftrw/thriftreflect"
	"go.uber.org/thriftrw/wire"
)
type Item struct { WorkflowId *string }
func (v *Item) ToWire() (wire.Value, error) {
	var fields [1]wire.Field
	if v.WorkflowId != nil {
		fields[0] = wire.Field{ID: 10, Value: wire.NewValueString(*v.WorkflowId)}
	}
	return wire.NewValueStruct(wire.Struct{Fields: fields[:]}), nil
}
func (v *Item) GetWorkflowId() string {
	if v != nil && v.WorkflowId != nil { return *v.WorkflowId }
	return ""
}
var ThriftModule = &thriftreflect.ThriftModule{
	Name: "shared", Package: "example.invalid/contracts/shared",
	FilePath: "shared.thrift", SHA1: "` + hex.EncodeToString(sum[:]) + `", Raw: rawIDL,
}
const rawIDL = ` + "`" + raw + "`" + `
`
	consumer := `package consumer
func use(v *shared.Item) { _ = v.GetWorkflowId() }
`
	symbol := "scip-go gomod example.invalid/contracts v1.0.0 pkg/Item#GetWorkflowId()."
	index := &scip.Index{
		Metadata: &scip.Metadata{},
		Documents: []*scip.Document{
			{
				RelativePath:     "generated/shared.go",
				PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
				Occurrences: []*scip.Occurrence{
					{
						Range:       workerSCIPRange(t, generated, "GetWorkflowId", 0),
						Symbol:      symbol,
						SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated),
					},
				},
			},
			{
				RelativePath:     "consumer/use.go",
				PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
				Occurrences: []*scip.Occurrence{
					{
						Range:       workerSCIPRange(t, consumer, "GetWorkflowId", 0),
						Symbol:      symbol,
						SymbolRoles: int32(scip.SymbolRole_ReadAccess),
					},
				},
			},
		},
	}
	indexBytes, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.scip":          string(indexBytes),
		"generated/shared.go": generated,
		"consumer/use.go":     consumer,
	}
	repo := &store.Repo{Name: "example.invalid/consumer", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus:  grpcWorkerCorpusFactory{files: files},
		Extractors: []Extractor{thriftfield.New()},
	}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !evidence.published || evidence.aborted {
		t.Fatalf("run state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
	coverage := evidence.publishedWith
	if coverage.CandidateFileCount != 0 || coverage.ReadFileCount != 3 ||
		coverage.AssertionCount != 1 || coverage.AtomCount != 1 ||
		coverage.UnresolvedCount != 0 {
		t.Fatalf("coverage = %+v", coverage)
	}
	if len(evidence.batches) != 1 || len(evidence.batches[0].asserts) != 1 {
		t.Fatalf("evidence batches = %+v", evidence.batches)
	}
	assertion := evidence.batches[0].asserts[0]
	if assertion.Predicate != "REFERENCES_THRIFT_FIELD" ||
		assertion.Object != "shared.Item#10" ||
		assertion.Tier != store.TierExact ||
		!strings.HasPrefix(assertion.Lineage, "contract_scip_package_v1_") {
		t.Fatalf("assertion = %+v", assertion)
	}
}

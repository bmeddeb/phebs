package extract

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t201"
)

func TestT208TypedCallersPublishThroughTrustedWorker(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(profile.Files))
	for filePath, content := range profile.Files {
		files[filePath] = string(content)
	}
	tests := []struct {
		name, domain, operation string
		extractor               Extractor
		want                    int
	}{
		{
			name: "grpc", domain: "grpc-caller",
			operation: "/synthetic.orders.v1.Orders/Get",
			extractor: gocaller.NewGRPC(), want: 4,
		},
		{
			name: "thrift", domain: "thrift-caller",
			operation: "/ledger.Ledger/get",
			extractor: gocaller.NewThrift(), want: 1,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &store.Repo{
				Name: "synthetic.invalid/mono", IndexedCommitHash: unitCommit,
			}
			evidence := newMemoryEvidence()
			worker := Worker{
				Repos: readyRepoGetter(repo), Evidence: evidence,
				NewCorpus:  grpcWorkerCorpusFactory{files: files},
				Extractors: []Extractor{testCase.extractor},
			}
			if err := worker.Handle(
				context.Background(), store.Job{Target: repo.Name},
			); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if !evidence.published || evidence.aborted {
				t.Fatalf(
					"run state: published=%v aborted=%v",
					evidence.published, evidence.aborted,
				)
			}
			if evidence.publishedWith.AssertionCount != testCase.want ||
				evidence.publishedWith.AtomCount != testCase.want ||
				evidence.publishedWith.UnresolvedCount != 0 ||
				evidence.publishedWith.CandidateFileCount != 18 {
				t.Fatalf("coverage = %+v", evidence.publishedWith)
			}
			assertions := 0
			sawUnitAttribution := false
			for _, batch := range evidence.batches {
				for _, assertion := range batch.asserts {
					if assertion.Predicate != "CALLS_OPERATION" ||
						assertion.Object != testCase.operation ||
						assertion.Tier != store.TierDerived ||
						!strings.HasPrefix(
							assertion.Lineage, "provisional_repo_path_v1_",
						) {
						t.Fatalf("assertion = %+v", assertion)
					}
					if strings.Contains(assertion.Detail, `"unit_state":"resolved"`) {
						sawUnitAttribution = true
					}
					assertions++
				}
			}
			if assertions != testCase.want {
				t.Fatalf("assertions = %d, want %d", assertions, testCase.want)
			}
			if testCase.name == "grpc" && !sawUnitAttribution {
				t.Fatal("resolved caller omitted its immutable unit attribution")
			}
		})
	}
}

func TestT209SyntaxFallbackPublishesThroughTrustedWorker(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(profile.Files)+1)
	for filePath, content := range profile.Files {
		files[filePath] = string(content)
	}
	files["gen/proto/orders/v1/orders_grpc.pb.go"] +=
		"\nfunc NewOrdersClient(any) OrdersClient { return nil }\n"
	files["src/syntax/worker.go"] = `package syntax
import orders "synthetic.invalid/mono/gen/proto/orders/v1"
func Load(ctx any) {
	client := orders.NewOrdersClient(nil)
	_, _ = client.Get(ctx, nil)
}
`
	repo := &store.Repo{
		Name: "synthetic.invalid/mono", IndexedCommitHash: unitCommit,
	}
	evidence := newMemoryEvidence()
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus:  grpcWorkerCorpusFactory{files: files},
		Extractors: []Extractor{gocaller.NewGRPC()},
	}
	if err := worker.Handle(
		context.Background(), store.Job{Target: repo.Name},
	); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	resolvedFallback := 0
	for _, batch := range evidence.batches {
		for _, assertion := range batch.asserts {
			if assertion.Predicate == "CALLS_OPERATION" &&
				assertion.Tier == store.TierHeuristic &&
				strings.Contains(assertion.Detail, `"resolution":"syntax"`) {
				resolvedFallback++
			}
		}
	}
	if !evidence.published || evidence.aborted ||
		evidence.publishedWith.AssertionCount != 5 ||
		evidence.publishedWith.UnresolvedCount != 0 ||
		resolvedFallback != 1 {
		t.Fatalf(
			"fallback publication: published=%v aborted=%v coverage=%+v resolved=%d",
			evidence.published, evidence.aborted, evidence.publishedWith,
			resolvedFallback,
		)
	}
}

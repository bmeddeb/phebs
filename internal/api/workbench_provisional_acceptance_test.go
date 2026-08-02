package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	provisionalWorkbenchRepo   = "example.com/acceptance/contracts"
	provisionalWorkbenchCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type provisionalWorkbenchCorpus struct {
	repo, commit string
	files        map[string]string
}

func (corpus provisionalWorkbenchCorpus) RepoName() string { return corpus.repo }
func (corpus provisionalWorkbenchCorpus) Commit() string   { return corpus.commit }

func (corpus provisionalWorkbenchCorpus) WalkFiles(
	ctx context.Context,
	visit func(string) error,
) error {
	paths := make([]string, 0, len(corpus.files))
	for filePath := range corpus.files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(filePath); err != nil {
			return err
		}
	}
	return nil
}

func (corpus provisionalWorkbenchCorpus) Read(
	_ context.Context,
	filePath string,
) (sdk.Blob, error) {
	content, ok := corpus.files[filePath]
	if !ok {
		return sdk.Blob{}, store.ErrNotFound
	}
	digest := sha256.Sum256([]byte(content))
	return sdk.Blob{
		Content: content,
		Digest:  "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

type provisionalWorkbenchCorpusFactory struct {
	files map[string]string
}

func (provisionalWorkbenchCorpusFactory) Lock(
	context.Context,
	string,
) (func(), error) {
	return func() {}, nil
}

func (factory provisionalWorkbenchCorpusFactory) New(
	repo,
	commit string,
) sdk.Corpus {
	return provisionalWorkbenchCorpus{
		repo: repo, commit: commit, files: factory.files,
	}
}

func TestProvisionalWorkbenchResolvesWorkerPublishedContract(t *testing.T) {
	ctx := t.Context()
	st, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if err := st.UpsertRepo(ctx, store.Repo{
		Name:     provisionalWorkbenchRepo,
		CloneURL: "file:///generated/provisional-workbench.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(
		ctx,
		provisionalWorkbenchRepo,
		provisionalWorkbenchCommit,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}

	proto := `syntax = "proto3";
package acceptance.v1;

service Catalog {
  rpc Get(GetRequest) returns (GetResponse);
}

message GetRequest {}
message GetResponse {}
`
	worker := &extract.Worker{
		Repos:    st,
		Evidence: st,
		NewCorpus: provisionalWorkbenchCorpusFactory{files: map[string]string{
			"contracts/catalog.proto": proto,
		}},
		Extractors: []extract.Extractor{protodecl.New()},
	}
	if err := worker.Handle(ctx, store.Job{
		Kind: store.JobExtract, Target: provisionalWorkbenchRepo,
	}); err != nil {
		t.Fatalf("publish ordinary extraction evidence: %v", err)
	}

	opts := api.Options{
		Store: st, Evidence: st,
		Principal: func(context.Context) string {
			return "user:provisional-workbench"
		},
		AuthorizationProvider: "provisional-workbench-acceptance-v1",
	}
	catalog := api.NewContractCatalogService(opts)
	if catalog == nil {
		t.Fatal("store-derived Contract Catalog = nil")
	}
	opts.ContractCatalog = catalog
	resolver := api.NewWorkbenchTargetResolver(opts)
	if resolver == nil {
		t.Fatal("Workbench target resolver = nil")
	}

	list, err := catalog.List(ctx, api.ContractCatalogQuery{
		Repository: provisionalWorkbenchRepo,
		Protocol:   "protobuf",
	}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	var operation *api.ContractCatalogItem
	for index := range list.Items {
		if list.Items[index].Kind == "operation" {
			operation = &list.Items[index]
			break
		}
	}
	if operation == nil {
		t.Fatalf("store-derived catalog has no operation: %+v", list.Items)
	}

	resolution, err := resolver.ResolveWorkbench(
		ctx,
		"user:provisional-workbench",
		store.WorkbenchResolutionRequest{
			Repositories: []string{provisionalWorkbenchRepo},
			Selections: []store.ChangeBriefContractSelection{{
				Role:               store.ChangeBriefCurrent,
				Protocol:           operation.Protocol,
				Repository:         operation.Repository,
				DeclarationLineage: operation.Lineage,
				CanonicalOperation: operation.Operation,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Repositories) != 1 ||
		resolution.Repositories[0].Name != provisionalWorkbenchRepo ||
		resolution.Repositories[0].Commit != provisionalWorkbenchCommit ||
		len(resolution.Endpoints) != 1 ||
		resolution.Endpoints[0].Selection.CanonicalOperation !=
			operation.Operation ||
		resolution.Endpoints[0].DeclarationDigest == "" {
		t.Fatalf("Workbench resolution = %+v", resolution)
	}
	published, err := st.LatestPublishedRun(
		ctx,
		store.ExtractionScope{
			Repository: provisionalWorkbenchRepo,
			Commit:     provisionalWorkbenchCommit,
			Domain:     "proto-contract",
		},
	)
	if err != nil {
		t.Fatalf("read published run: %v", err)
	}
	if published.Commit != provisionalWorkbenchCommit ||
		published.Status != "published" {
		t.Fatalf("published run = %+v", published)
	}
}

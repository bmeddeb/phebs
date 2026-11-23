package scipfield

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
)

// t225Repo is the synthetic T22.5 demo repository. Its index.scip is written
// by a fixture author rather than scip-go 0.2.7.
var t225Repo = filepath.Join("..", "..", "..", "..", "docs", "fixtures", "thrift-field", "repo")

// TestReleasedRecipeAdmissionRefusalsKeepTheirCause pins the PHEBS-38 causes
// seen on dave: the released recipes refuse these fixtures, and the partition
// boundary now records which admission check refused instead of unknown.
func TestReleasedRecipeAdmissionRefusalsKeepTheirCause(t *testing.T) {
	assertAdmissionRefusal(t, NewCommittedOpenAPI(), t225Repo, pipelinerefusal.DimensionSCIPProducer)
}

func assertAdmissionRefusal(t *testing.T, extractor sdk.Extractor, root string, want pipelinerefusal.Dimension) {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		files[filepath.ToSlash(relative)] = string(content)
		return err
	})
	if err != nil {
		t.Fatalf("read fixture %s: %v", root, err)
	}
	_, err = extractor.Extract(context.Background(), memoryCorpus{
		repo: "example.invalid/" + filepath.Base(root), commit: strings.Repeat("3", 40), files: files,
	}, func(sdk.Fact) error {
		t.Fatalf("%s emitted a fact from refused input", extractor.Version())
		return nil
	})
	receipt, ok := pipelinerefusal.From(pipelinerefusal.At(
		err, pipelinerefusal.StageExtractorExecution, pipelinerefusal.GenerationExtractionDomain,
	))
	if err == nil || !ok || receipt.Classification != pipelinerefusal.ClassificationUnsupportedInput ||
		receipt.Dimension != want {
		t.Fatalf("%s on %s: err=%v receipt=%+v, want unsupported_input/%s",
			extractor.Version(), filepath.Base(root), err, receipt, want)
	}
}

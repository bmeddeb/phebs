package t201

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"

	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

// ExtractionMeasurement captures current behavior without treating it as the
// future Caller Map resolver or making an accuracy claim.
type ExtractionMeasurement struct {
	Domain          string         `json:"domain"`
	Version         string         `json:"version"`
	Facts           int            `json:"facts"`
	UnresolvedCount int            `json:"unresolved_count"`
	Predicates      map[string]int `json:"predicates"`
}

// MeasureCurrentExtraction executes today's pure readers over one generated
// profile. The candidate-independent oracle is not supplied to the readers.
func MeasureCurrentExtraction(ctx context.Context, profile Profile) ([]ExtractionMeasurement, error) {
	corpus := profileCorpus{files: profile.Files}
	extractors := []sdk.Extractor{
		protodecl.New(), thriftdecl.New(), grpcgo.New(), thriftgo.New(), scipfield.New(),
	}
	out := make([]ExtractionMeasurement, 0, len(extractors))
	for _, extractor := range extractors {
		measurement := ExtractionMeasurement{
			Domain: extractor.Domain(), Version: extractor.Version(),
			Predicates: map[string]int{},
		}
		coverage, err := extractor.Extract(ctx, corpus, func(fact sdk.Fact) error {
			measurement.Facts++
			measurement.Predicates[fact.Assertion.Predicate]++
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("%s extraction: %w", extractor.Domain(), err)
		}
		measurement.UnresolvedCount = coverage.UnresolvedCount
		out = append(out, measurement)
	}
	slices.SortFunc(out, func(a, b ExtractionMeasurement) int {
		if a.Domain < b.Domain {
			return -1
		}
		if a.Domain > b.Domain {
			return 1
		}
		return 0
	})
	return out, nil
}

type profileCorpus struct {
	files map[string][]byte
}

func (profileCorpus) RepoName() string { return "synthetic.invalid/mono" }
func (profileCorpus) Commit() string   { return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }

func (c profileCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	paths := make([]string, 0, len(c.files))
	for path := range c.files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

func (c profileCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	content, ok := c.files[path]
	if !ok {
		return sdk.Blob{}, os.ErrNotExist
	}
	sum := sha256.Sum256(content)
	return sdk.Blob{
		Content: string(content),
		Digest:  "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func (c profileCorpus) ReadSCIPIndex(ctx context.Context) (sdk.Blob, error) {
	return c.Read(ctx, "index.scip")
}

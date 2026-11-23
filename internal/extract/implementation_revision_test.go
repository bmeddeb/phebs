package extract

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type revisionTestExtractor struct {
	domain, version string
	revision        uint32
}

func (e revisionTestExtractor) Domain() string                 { return e.domain }
func (e revisionTestExtractor) Version() string                { return e.version }
func (revisionTestExtractor) Candidate(string) bool            { return false }
func (e revisionTestExtractor) ImplementationRevision() uint32 { return e.revision }
func (revisionTestExtractor) Extract(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error) {
	return sdk.Coverage{}, nil
}

type unrevisionedTestExtractor struct{ revisionTestExtractor }

func (unrevisionedTestExtractor) ImplementationRevision() {}

func TestImplementationRevisionDigest(t *testing.T) {
	plain := revisionTestExtractor{domain: "a", version: "1"}
	none, err := ImplementationRevisionDigest([]Extractor{plain, unrevisionedTestExtractor{plain}})
	if err != nil || none != "" {
		t.Fatalf("no positive revision must keep identities exact: %q, %v", none, err)
	}
	revised := revisionTestExtractor{domain: "b", version: "2", revision: 1}
	other := revisionTestExtractor{domain: "c", version: "3", revision: 4}
	first, err := ImplementationRevisionDigest([]Extractor{plain, revised, other})
	if err != nil || !strings.HasPrefix(first, "sha256:") || len(first) != len("sha256:")+64 {
		t.Fatalf("revision digest = %q, %v", first, err)
	}
	reordered, err := ImplementationRevisionDigest([]Extractor{other, revised, plain, revised})
	if err != nil || reordered != first {
		t.Fatalf("revision digest is order dependent: %q != %q (%v)", reordered, first, err)
	}
	bumped, err := ImplementationRevisionDigest([]Extractor{plain, revisionTestExtractor{domain: "b", version: "2", revision: 2}, other})
	if err != nil || bumped == first {
		t.Fatalf("a revision bump must change the digest: %q, %v", bumped, err)
	}
	if _, err := ImplementationRevisionDigest([]Extractor{revised, revisionTestExtractor{domain: "b", version: "2", revision: 3}}); err == nil {
		t.Fatal("conflicting revisions for one domain@version were admitted")
	}
	if _, err := ImplementationRevisionDigest([]Extractor{nil}); err == nil {
		t.Fatal("nil extractor was admitted")
	}
}

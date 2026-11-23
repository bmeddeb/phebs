package main

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
)

// PHEBS-37: ordinary serve layers the admitted extractors' implementation
// revisions, while the pinned T422 semantic launch keeps its identities.
func TestServeImplementationRevisions(t *testing.T) {
	admitted := []extract.Extractor{thriftfield.NewCommittedThriftRW()}
	ordinary, err := serveImplementationRevisions(admitted, nil)
	if err != nil || ordinary == "" {
		t.Fatalf("ordinary serve revision layer = %q, %v", ordinary, err)
	}
	ceremony, err := serveImplementationRevisions(admitted, &t422SemanticLaunch{})
	if err != nil || ceremony != "" {
		t.Fatalf("T422 semantic launch took the revision layer: %q, %v", ceremony, err)
	}
	unrevised, err := serveImplementationRevisions([]extract.Extractor{thriftfield.NewCommittedApache()}, nil)
	if err != nil || unrevised != "" {
		t.Fatalf("an unrevised selection changed the policy identity: %q, %v", unrevised, err)
	}
}

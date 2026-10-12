package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
)

// TestReleasedClaimAloneInitializesTheChecker pins the T53.3 release-run
// finding: with no extractor admitted the extraction pipeline never starts, so
// a released Buf WIRE claim must get its checker here or it is silently dark.
func TestReleasedClaimAloneInitializesTheChecker(t *testing.T) {
	buf, err := filepath.Abs("../../bin/buf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(buf); err != nil {
		t.Skip("bin/buf is not built (make bin/buf)")
	}
	t.Setenv("PHEBS_BUF", buf)
	dark := &serveDeps{ctx: context.Background(), cfg: &config.Config{}}
	if err := startServeExtractionPipeline(dark); err != nil || dark.compatibility != nil {
		t.Fatalf("no claim and no extractor: compatibility=%v err=%v", dark.compatibility, err)
	}
	released := &serveDeps{ctx: context.Background(), cfg: &config.Config{},
		releasedClaims: map[string]bool{wireCompatibilityPackID: true}}
	if err := startServeExtractionPipeline(released); err != nil {
		t.Fatal(err)
	}
	if released.compatibility == nil {
		t.Fatal("a released WIRE claim with no extractor left the checker uninitialized")
	}
}

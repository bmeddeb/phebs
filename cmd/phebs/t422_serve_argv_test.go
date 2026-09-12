package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Both spellings reach the actual serve config loader. The missing file stops
// before any database, worker or native tool starts; this is not server readiness.
func TestT422ServeConfigArgvLoadsExactPath(t *testing.T) {
	if dispatchadmission.ProductionSemanticSelected() {
		t.Fatal("ordinary CLI test must not consume selected semantic input")
	}
	t.Setenv(t421ExactReadsEnvironment, t421ExactReadsContract)
	t.Setenv(t4013ExactReportsEnvironment, t4013ExactReportsContract)
	for _, spelling := range []string{"-config", "--config"} {
		t.Run(spelling, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing-config.yaml")
			err := serve([]string{spelling, path})
			var pathErr *os.PathError
			if errors.Is(err, errServeFlags) || !errors.Is(err, os.ErrNotExist) ||
				!errors.As(err, &pathErr) || pathErr.Path != path {
				t.Fatalf("serve did not parse the exact config path: %v", err)
			}
		})
	}
}

package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestT422CompatibilityInitializationSelection(t *testing.T) {
	// An explicit nonexistent binary makes entering even FindBinary observable
	// without a native tool, sandbox, injection callback or fake success. The
	// decoder fixture exercises selection only, not protected parent admission.
	t.Setenv("PHEBS_BUF", filepath.Join(t.TempDir(), "unavailable-buf"))
	raw, snapshot := t422SemanticTestRequest(t)
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name              string
		older, selector   bool
		launch            *t422SemanticLaunch
		wantCompatibility bool
	}{
		{name: "ordinary", wantCompatibility: true},
		{name: "older-exact", older: true, wantCompatibility: true},
		{name: "environment-only", older: true, selector: true, wantCompatibility: true},
		{name: "decoded-v3", older: true, selector: true, launch: launch},
	} {
		t.Run(test.name, func(t *testing.T) {
			exact, selector := "", ""
			if test.older {
				exact = "source-free-v1"
			}
			if test.selector {
				selector = dispatchadmission.ProductionSelector
			}
			t.Setenv("PHEBS_T4013_EXACT_REPORTS", exact)
			t.Setenv("PHEBS_T421_EXACT_READS", exact)
			t.Setenv(dispatchadmission.ProductionEnvironment, selector)
			service, err := initializeCompatibilityForLaunch(t.Context(), test.launch)
			if service != nil {
				t.Fatal("unavailable fixture published compatibility capability")
			}
			if test.wantCompatibility {
				if !errors.Is(err, compat.ErrUnavailable) {
					t.Fatal("ordinary/older exact initialization was bypassed", err)
				}
			} else if err != nil {
				t.Fatal("decoded V3 entered native compatibility initialization", err)
			}
		})
	}
}

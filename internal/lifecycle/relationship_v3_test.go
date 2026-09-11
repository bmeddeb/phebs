package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
)

func TestRelationshipGenerationOwnerV3IsDarkAndExactWhenEmpty(t *testing.T) {
	acquired := 0
	released := 0
	owner := RelationshipGenerationOwnerV3{
		DataDir: t.TempDir(),
		Pins:    &relationshippublication.CacheV3{},
		AcquireExclusive: func(context.Context) (func(), error) {
			acquired++
			return func() { released++ }, nil
		},
	}
	if owner.Name() != RelationshipV3Owner || owner.Name() != "relationship-v3-namespaces" {
		t.Fatalf("v3 owner name = %q", owner.Name())
	}
	result := owner.Sweep(t.Context(), time.Now().UTC(), "", DefaultLimits())
	if result.Err != nil || result.Completeness != Exact || result.More ||
		result.Scanned != 0 || result.Deleted != 0 || acquired != 1 || released != 1 {
		t.Fatalf(
			"empty dark v3 lifecycle = %+v, acquired %d released %d",
			result, acquired, released,
		)
	}
}

func TestRelationshipOwnersRetainFailedDeletionPrefix(t *testing.T) {
	for _, v3 := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "v3"}[v3], func(t *testing.T) {
			data := t.TempDir()
			base := filepath.Join(data, "relationships", "relationship-publications")
			if v3 {
				base = relationshippublication.ShadowBase(filepath.Join(data, "relationships"))
			}
			stage := filepath.Join(base, strings.Repeat("1", 64), ".stage-partial-prefix")
			if err := os.MkdirAll(stage, 0o700); err != nil {
				t.Fatal(err)
			}
			first := filepath.Join(stage, "a.json")
			if err := os.WriteFile(first, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(data, "outside")
			if err := os.WriteFile(outside, []byte("preserved"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(stage, "z-link")); err != nil {
				t.Fatal(err)
			}
			released := false
			acquire := func(context.Context) (func(), error) { return func() { released = true }, nil }
			var owner Owner = RelationshipGenerationOwner{DataDir: data, Pins: &relationshippublication.Cache{}, AcquireExclusive: acquire}
			if v3 {
				owner = RelationshipGenerationOwnerV3{DataDir: data, Pins: &relationshippublication.CacheV3{}, AcquireExclusive: acquire}
			}
			result := owner.Sweep(t.Context(), time.Now(), "prior", DefaultLimits())
			if result.Err == nil || result.Completeness != Unavailable || result.Scanned != 1 || result.Deleted != 1 || result.Cursor != "prior" || result.AdvanceOnError || !released {
				t.Fatalf("failed prefix lost: %+v, released %v", result, released)
			}
			if _, err := os.Lstat(first); !os.IsNotExist(err) {
				t.Fatal("completed unlink not observed", err)
			}
			if raw, err := os.ReadFile(outside); err != nil || string(raw) != "preserved" {
				t.Fatal("symlink target changed", err)
			}
		})
	}
}

func TestRelationshipGenerationOwnerV3RefusesIncompleteInputs(t *testing.T) {
	result := (RelationshipGenerationOwnerV3{}).Sweep(
		t.Context(), time.Now().UTC(), "retained", DefaultLimits(),
	)
	if result.Err == nil || result.Completeness != Unavailable || result.Cursor != "retained" {
		t.Fatalf("incomplete v3 owner = %+v", result)
	}
}

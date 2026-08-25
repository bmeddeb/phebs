package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPartialStageOwnerReportsLiveAndRetiredExtractionStagesTruthfully(t *testing.T) {
	root := filepath.Join(t.TempDir(), "extraction-publications")
	owner := ExtractionStageOwner{
		Root: root,
		Acquire: func(context.Context) (func(), error) {
			return func() {}, nil
		},
	}
	now := time.Now().UTC()
	result := owner.Sweep(t.Context(), now, "", DefaultLimits())
	if result.Err != nil || result.Completeness != Exact || result.More {
		t.Fatalf("absent partial-stage owner = %+v", result)
	}

	repository := strings.Repeat("a", 64)
	directory := filepath.Join(root, repository)
	live := filepath.Join(directory, ".stage-generation-1")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	result = owner.Sweep(t.Context(), now, "", DefaultLimits())
	if result.Err != nil || result.Completeness != LowerBound || !result.More || result.Deleted != 0 {
		t.Fatalf("live partial-stage owner = %+v", result)
	}
	if _, err := os.Lstat(live); err != nil {
		t.Fatalf("partial-stage owner retired live state: %v", err)
	}
	result = owner.Sweep(t.Context(), now, result.Cursor, DefaultLimits())
	if result.Err != nil || result.Completeness != LowerBound || result.More || result.Deleted != 0 {
		t.Fatalf("completed live partial-stage pass = %+v", result)
	}

	retired := filepath.Join(directory, ".collecting-stage-generation-1")
	if err := os.Rename(live, retired); err != nil {
		t.Fatal(err)
	}
	result = owner.Sweep(t.Context(), now, "", DefaultLimits())
	if result.Err != nil || result.Completeness != LowerBound || !result.More || result.Deleted != 1 {
		t.Fatalf("retired partial-stage owner = %+v", result)
	}
	result = owner.Sweep(t.Context(), now, result.Cursor, DefaultLimits())
	if result.Err != nil || result.Completeness != Exact || result.More {
		t.Fatalf("completed retired partial-stage pass = %+v", result)
	}
}

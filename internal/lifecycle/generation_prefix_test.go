package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestGenerationOwnerFailureRetainsPrefixWithoutAdvancingCursor(t *testing.T) {
	for _, cause := range []error{context.Canceled, errors.New("later candidate refused")} {
		t.Run(cause.Error(), func(t *testing.T) {
			cursors := newMemoryCursorStore()
			cursors.values["owner:"+GenerationScheduleOwner] = "original"
			owner := GenerationOwner{
				Store: generationStoreFunc(func(_ context.Context, cursor string, _, _, _ int) (store.GenerationLifecycleSweep, error) {
					if cursor != "original" {
						t.Fatalf("failed prefix advanced durable cursor: %q", cursor)
					}
					return store.GenerationLifecycleSweep{Cursor: "later-candidate", Scanned: 5, Deleted: 3, More: true}, cause
				}),
				Acquire: func(context.Context) (func(), error) { return func() {}, nil },
			}
			controller, err := NewController(cursors, owner)
			if err != nil {
				t.Fatal(err)
			}
			controller.now = func() time.Time { return time.Unix(1, 0) }
			for range 2 {
				result := controller.Tick(t.Context())
				if !errors.Is(result.Err, cause) || result.Completeness != Unavailable || result.Scanned != 5 || result.Deleted != 3 ||
					result.Cursor != "original" || result.AdvanceOnError || !result.More {
					t.Fatalf("failed owner lost prefix or changed retry policy: %+v", result)
				}
			}
			if cursors.revisions["owner:"+GenerationScheduleOwner] != 0 || cursors.values[rotationCursorKey] != GenerationScheduleOwner {
				t.Fatal("owner retry or outer rotation changed")
			}
		})
	}
}

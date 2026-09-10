//go:build darwin

package custodybytes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCustodyByteGuardRequiresRealWalkAndUnlock(t *testing.T) {
	for _, mode := range []string{"once", "missing", "twice", "swallowed-walk-error", "guard-error", "late"} {
		t.Run(mode, func(t *testing.T) {
			owner, ctx := custodyByteFixture(t)
			if err := os.WriteFile(filepath.Join(owner.path, "native"), []byte("actual"), 0600); err != nil {
				t.Fatal(err)
			}
			observer := newObservation(owner)
			prior, err := observer.Sample(ctx, 9)
			if err != nil {
				t.Fatal(err)
			}
			locked := false
			var late func(context.Context) error
			guard := func(ctx context.Context, walk func(context.Context) error) error {
				locked = true
				defer func() { locked = false }()
				if mode == "missing" {
					return nil
				}
				if mode == "late" {
					late = walk
					return nil
				}
				if mode == "swallowed-walk-error" {
					if e := owner.file.Close(); e != nil {
						t.Fatal(e)
					}
					_ = walk(ctx)
					return nil
				}
				if e := walk(ctx); e != nil {
					return e
				}
				if mode == "twice" {
					_ = walk(ctx)
				}
				if mode == "guard-error" {
					return errors.New("resume refused")
				}
				return nil
			}
			got, err := observer.SampleGuarded(ctx, 9, guard, func() bool { return !locked })
			if (err == nil) != (mode == "once") {
				t.Fatal(mode, got, err)
			}
			if mode == "once" {
				if got != prior {
					t.Fatal("not native result", got, prior)
				}
			} else if !observer.Snapshot().Unavailable || observer.Snapshot().Phases[8].Maximum != prior {
				t.Fatal("lost prefix")
			}
			if late != nil && late(ctx) == nil {
				t.Fatal("walk after guard release admitted")
			}
		})
	}
}

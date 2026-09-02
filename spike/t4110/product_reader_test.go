package t4110

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/rpccallerposting"
)

func TestT4110EmptyRelationshipInputsAreValidAndDeterministic(t *testing.T) {
	repository := "neutral.invalid/t4110/relationship"
	commit := strings.Repeat("a", 40)
	first, err := publishEmptyRelationshipComponents(
		t.Context(),
		t.TempDir(),
		repository,
		commit,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := publishEmptyRelationshipComponents(
		t.Context(),
		t.TempDir(),
		repository,
		commit,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.resolver.Root().Digest != second.resolver.Root().Digest ||
		first.rpc.Root().Digest != second.rpc.Root().Digest ||
		first.kafka.Root().Digest != second.kafka.Root().Digest ||
		first.upstream.Digest != second.upstream.Digest {
		t.Fatal("empty relationship inputs are not deterministic")
	}
	if err := first.rpc.WalkPostings(context.Background(), func(_ rpccallerposting.Posting) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

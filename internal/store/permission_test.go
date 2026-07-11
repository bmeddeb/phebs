package store_test

import (
	"context"
	"slices"
	"testing"
)

func TestRepoPermissionsReplaceListDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetRepoPermissions(ctx, "github.com/foo/bar",
		[]string{"github.com:alice", "GitHub.com:Bob", "github.com:alice"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.SetRepoPermissions(ctx, "github.com/baz/qux", []string{"github.com:alice"}); err != nil {
		t.Fatalf("set second: %v", err)
	}

	names, err := s.ListPermittedRepos(ctx, []string{"github.com:ALICE"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"github.com/baz/qux", "github.com/foo/bar"}) {
		t.Fatalf("alice sees %v", names)
	}
	names, err = s.ListPermittedRepos(ctx, []string{"github.com:bob"})
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if !slices.Equal(names, []string{"github.com/foo/bar"}) {
		t.Fatalf("bob sees %v (case-insensitive identity write expected)", names)
	}

	// replace-set revokes: bob dropped from foo/bar
	if err := s.SetRepoPermissions(ctx, "github.com/foo/bar", []string{"github.com:alice"}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	names, err = s.ListPermittedRepos(ctx, []string{"github.com:bob"})
	if err != nil {
		t.Fatalf("list bob after revoke: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("bob still sees %v after revocation", names)
	}

	// empty replacement clears every edge
	if err := s.SetRepoPermissions(ctx, "github.com/baz/qux", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	// deletion prunes edges
	if err := s.DeleteRepoPermissions(ctx, "github.com/foo/bar"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	names, err = s.ListPermittedRepos(ctx, []string{"github.com:alice"})
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("alice still sees %v", names)
	}

	// no identities never queries broadly
	if names, err := s.ListPermittedRepos(ctx, nil); err != nil || names != nil {
		t.Fatalf("nil identities = %v, %v", names, err)
	}
}

package search_test

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
)

type fakeStore struct{ store.Store }

func (fakeStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{
		{Name: "h/public-plain", IsPublic: true},
		{Name: "h/private-fork", IsFork: true},
		{Name: "h/public-archived", IsPublic: true, IsArchived: true},
	}, nil
}

// T4.1 AC: input strings → expected zoekt query trees.
func TestCompile(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // q.String() of the compiled tree
		err  bool
	}{
		{"plain", "needle", `substr:"needle"`, false},
		{"zoekt atoms pass through", "needle repo:foo lang:go",
			`(and substr:"needle" repo:foo lang:Go)`, false},
		{"no forks", "fork:no needle",
			`(and (reposet h/public-archived h/public-plain) substr:"needle")`, false},
		{"only archived", "archived:yes needle",
			`(and (reposet h/public-archived) substr:"needle")`, false},
		{"public non-archived", "public:yes archived:no needle",
			`(and (reposet h/public-plain) substr:"needle")`, false},
		{"only forks", "fork:yes needle",
			`(and (reposet h/private-fork) substr:"needle")`, false},
		// empty repo set simplifies straight to FALSE — nothing can match
		{"filters match nothing", "fork:yes archived:yes needle", `FALSE`, false},
		{"parse error", "(unbalanced", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := search.Compile(context.Background(), fakeStore{}, tt.raw)
			if (err != nil) != tt.err {
				t.Fatalf("Compile(%q) err = %v, wantErr %v", tt.raw, err, tt.err)
			}
			if err != nil {
				return
			}
			if got := q.String(); got != tt.want {
				t.Errorf("Compile(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

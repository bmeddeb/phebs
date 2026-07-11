package search_test

import (
	"context"
	"strings"
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

// T4.1 AC: input strings → expected zoekt query trees. Each metadata atom is
// rewritten IN PLACE with its matching RepoSet, so negation and OR are
// preserved (regression for the pre-fix global-hoist that broke them).
func TestCompile(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // exact q.String(); or "" when only checks below apply
		err  bool

		notFalse bool     // compiled query must not collapse to FALSE
		contains []string // substrings that must appear in q.String()
	}{
		{name: "plain", raw: "needle", want: `substr:"needle"`},
		{name: "zoekt atoms pass through", raw: "needle repo:foo lang:go",
			want: `(and substr:"needle" repo:foo lang:Go)`},
		{name: "no forks", raw: "fork:no needle",
			want: `(and (reposet h/public-archived h/public-plain) substr:"needle")`},
		{name: "only archived", raw: "archived:yes needle",
			want: `(and (reposet h/public-archived) substr:"needle")`},
		{name: "only forks", raw: "fork:yes needle",
			want: `(and (reposet h/private-fork) substr:"needle")`},

		// Two positive atoms → two RepoSets AND'd (zoekt intersects at search
		// time). public∩non-archived = public-plain; fork∩archived = ∅.
		{name: "public non-archived", raw: "public:yes archived:no needle",
			contains: []string{"reposet", `substr:"needle"`}},
		{name: "disjoint filters", raw: "fork:yes archived:yes needle",
			contains: []string{"reposet", `substr:"needle"`}},

		// Regression: these MUST NOT collapse to FALSE (the pre-fix bug).
		{name: "negated fork", raw: "needle -fork:yes",
			notFalse: true, contains: []string{"not", "reposet", `substr:"needle"`}},
		{name: "or'd metadata atom", raw: "(archived:yes or lang:go) needle",
			notFalse: true, contains: []string{"or", "lang:Go", "reposet"}},

		{name: "parse error", raw: "(unbalanced", err: true},
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
			got := q.String()
			if tt.want != "" && got != tt.want {
				t.Errorf("Compile(%q) = %s, want %s", tt.raw, got, tt.want)
			}
			if tt.notFalse && got == "FALSE" {
				t.Errorf("Compile(%q) collapsed to FALSE: %s", tt.raw, got)
			}
			for _, sub := range tt.contains {
				if !strings.Contains(got, sub) {
					t.Errorf("Compile(%q) = %s, missing %q", tt.raw, got, sub)
				}
			}
		})
	}
}

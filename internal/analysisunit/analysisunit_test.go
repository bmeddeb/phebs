package analysisunit

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/repopath"
)

func TestScopeIdentityAndState(t *testing.T) {
	scope := Scope{
		Repository: "example.com/neutral/monorepo",
		Name:       "payments",
		Primary:    []string{"services/payments/src"},
		Supporting: []string{
			"services/payments/go.mod",
			"contracts/payment.proto",
			"services/payments/generated",
		},
	}
	digest, err := scope.Digest()
	if err != nil {
		t.Fatal(err)
	}
	const spikeDigest = "sha256:494eaa79044f330e08ecc3e9ef14c4c71150a887b1ea860681e26ee7e411b768"
	if digest != spikeDigest {
		t.Fatalf("digest = %q, want T30.1 digest %q", digest, spikeDigest)
	}
	state, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(scope.Repository); err != nil {
		t.Fatal(err)
	}
	if state.PrimaryPathCount != 1 || state.SupportingPathCount != 3 ||
		state.SearchIndexPosture != SearchIndexFocused ||
		state.TypedIndexPosture != TypedIndexRepositoryRootUnbound ||
		!slices.IsSorted(state.SupportingPaths) {
		t.Fatalf("state = %+v", state)
	}
	clone := CloneState(state)
	clone.PrimaryPaths[0] = "changed"
	if state.PrimaryPaths[0] == "changed" || EqualState(state, clone) {
		t.Fatal("state clone aliased input")
	}
}

func TestEmptySupportingPathsHaveOneIdentity(t *testing.T) {
	omitted := Scope{
		Repository: "example.com/repo",
		Name:       "service",
		Primary:    []string{"service/src"},
	}
	explicit := omitted
	explicit.Supporting = []string{}

	omittedDigest, err := omitted.Digest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if omittedDigest != explicitDigest {
		t.Fatalf("omitted digest = %q, explicit-empty digest = %q", omittedDigest, explicitDigest)
	}
	state, err := omitted.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.SupportingPaths == nil || len(state.SupportingPaths) != 0 {
		t.Fatalf("supporting paths = %#v, want canonical empty list", state.SupportingPaths)
	}
}

func TestScopeRefusals(t *testing.T) {
	base := Scope{
		Repository: "example.com/repo",
		Name:       "service",
		Primary:    []string{"service/src"},
	}
	tests := []struct {
		name   string
		mutate func(*Scope)
	}{
		{"unsafe repository", func(scope *Scope) { scope.Repository = "../repo" }},
		{"nested git namespace", func(scope *Scope) {
			scope.Repository = "example.com/owner.git/repo"
		}},
		{"invalid unit name", func(scope *Scope) { scope.Name = "service name" }},
		{"unit name byte limit", func(scope *Scope) {
			scope.Name = strings.Repeat("n", MaxUnitNameBytes+1)
		}},
		{"repository byte limit", func(scope *Scope) {
			scope.Repository = strings.Repeat("r", MaxRepositoryBytes+1)
		}},
		{"missing primary", func(scope *Scope) { scope.Primary = nil }},
		{"absolute", func(scope *Scope) { scope.Primary = []string{"/service"} }},
		{"leading dash", func(scope *Scope) { scope.Primary = []string{"-service"} }},
		{"path byte limit", func(scope *Scope) {
			scope.Primary = []string{strings.Repeat("p", repopath.MaxBytes+1)}
		}},
		{"traversal", func(scope *Scope) { scope.Primary = []string{"service/../other"} }},
		{"backslash", func(scope *Scope) { scope.Primary = []string{`service\\src`} }},
		{"control", func(scope *Scope) { scope.Primary = []string{"service/\nsource"} }},
		{"duplicate", func(scope *Scope) {
			scope.Supporting = []string{"service/src"}
		}},
		{"overlap", func(scope *Scope) {
			scope.Supporting = []string{"service/src/generated"}
		}},
		{"interposed overlap", func(scope *Scope) {
			scope.Primary = []string{"a", "a-b", "a/c"}
		}},
		{"too many", func(scope *Scope) {
			scope.Primary = nil
			for index := range MaxSelectedPaths + 1 {
				scope.Primary = append(scope.Primary, "p"+strings.Repeat("x", index+1))
			}
		}},
		{"path byte limit", func(scope *Scope) {
			scope.Primary = []string{strings.Repeat("p", MaxSelectedPathBytes+1)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope := base
			scope.Primary = slices.Clone(base.Primary)
			test.mutate(&scope)
			if _, err := scope.Digest(); !errors.Is(err, ErrInvalidScope) {
				t.Fatalf("error = %v, want ErrInvalidScope", err)
			}
		})
	}
}

func TestStateRejectsNonCanonicalOrMismatchedValues(t *testing.T) {
	scope := Scope{
		Repository: "example.com/repo",
		Name:       "service",
		Primary:    []string{"service/src"},
	}
	state, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{"repository", func(state *State) {}},
		{"digest", func(state *State) { state.Digest = "sha256:wrong" }},
		{"schema", func(state *State) { state.Schema = "future" }},
		{"count", func(state *State) { state.PrimaryPathCount++ }},
		{"search posture", func(state *State) { state.SearchIndexPosture = "unknown" }},
		{"posture", func(state *State) { state.TypedIndexPosture = "unit-bound" }},
		{"path order", func(state *State) {
			state.PrimaryPaths = []string{"z", "a"}
			state.PrimaryPathCount = 2
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := CloneState(state)
			test.mutate(candidate)
			repository := scope.Repository
			if test.name == "repository" {
				repository = "example.com/other"
			}
			if err := candidate.Validate(repository); !errors.Is(err, ErrInvalidScope) {
				t.Fatalf("error = %v, want ErrInvalidScope", err)
			}
		})
	}
}

func TestT302WholeRepositoryStateRemainsReadableButIsNotDesired(t *testing.T) {
	scope := Scope{
		Repository: "example.com/repo",
		Name:       "service",
		Primary:    []string{"service/src"},
	}
	desired, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	legacy := CloneState(desired)
	legacy.SearchIndexPosture = SearchIndexWholeRepository
	if err := legacy.Validate(scope.Repository); err != nil {
		t.Fatalf("legacy T30.2 state must reopen: %v", err)
	}
	if EqualState(desired, legacy) {
		t.Fatal("legacy whole-repository state must enqueue focused replacement")
	}
}

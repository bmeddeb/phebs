// Package analysisunit defines the stable service-scope identity introduced by
// T30.2 and consumed by T30.3 focused indexing.
package analysisunit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	SchemaVersion = "analysis-unit-v1"

	// TypedIndexRepositoryRootUnbound means the existing repository-root
	// index.scip input has no reviewed binding to this service scope.
	TypedIndexRepositoryRootUnbound = "repository-root-unbound"
	// TypedIndexUnitBound means the exact configured typed input is a reviewed
	// supporting path in this service scope.
	TypedIndexUnitBound = "unit-bound"
	// TypedIndexKindSCIP is the only typed input supported by T30.5.
	TypedIndexKindSCIP = "scip"
	// SearchIndexWholeRepository remains readable for T30.2 committed rows.
	SearchIndexWholeRepository = "whole-repository"
	// SearchIndexFocused means zoekt input is restricted to the canonical
	// primary and supporting paths in this state.
	SearchIndexFocused = "focused"

	MaxSelectedPaths     = 128
	MaxSelectedPathBytes = 64 << 10
	MaxRepositoryBytes   = reponame.MaxBytes
	MaxUnitNameBytes     = 128
)

var (
	ErrInvalidScope = errors.New("invalid analysis-unit scope")
	unitNameRE      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Config is the repository-keyed YAML value. Repository is supplied by the
// owning map key so one repository can have at most one active unit.
type Config struct {
	Name       string      `yaml:"name" json:"name"`
	Primary    []string    `yaml:"primary" json:"primary"`
	Supporting []string    `yaml:"supporting" json:"supporting"`
	TypedIndex *TypedIndex `yaml:"typed_index,omitempty" json:"typed_index,omitempty"`
}

// TypedIndex names one exact, committed typed-index input. Path must also be
// present as an exact Supporting entry; a selected directory does not
// implicitly admit a typed input below it.
type TypedIndex struct {
	Kind string `yaml:"kind" json:"kind"`
	Path string `yaml:"path" json:"path"`
}

// Scope is the complete stable semantic identity.
type Scope struct {
	Repository string   `json:"repository"`
	Name       string   `json:"name"`
	Primary    []string `json:"primary"`
	Supporting []string `json:"supporting"`
	// TypedIndex is deliberately outside the stable semantic scope digest.
	// The selected paths already bind its bytes to the service boundary; the
	// candidate generation separately binds which selected artifact is the
	// typed input so changing that designation cannot reuse a publication.
	TypedIndex *TypedIndex `json:"-"`
}

// State is the committed index-state projection and /api/repo-status shape.
// Counts are explicit operator diagnostics and are validated against the
// canonical path lists whenever state is committed.
type State struct {
	Schema              string      `json:"schema"`
	Name                string      `json:"name"`
	Digest              string      `json:"digest"`
	PrimaryPaths        []string    `json:"primary_paths"`
	SupportingPaths     []string    `json:"supporting_paths"`
	PrimaryPathCount    int         `json:"primary_path_count"`
	SupportingPathCount int         `json:"supporting_path_count"`
	SearchIndexPosture  string      `json:"search_index_posture"`
	TypedIndexPosture   string      `json:"typed_index_posture"`
	TypedIndex          *TypedIndex `json:"typed_index,omitempty"`
}

func (config Config) Scope(repository string) Scope {
	return Scope{
		Repository: repository,
		Name:       config.Name,
		Primary:    slices.Clone(config.Primary),
		Supporting: slices.Clone(config.Supporting),
		TypedIndex: cloneTypedIndex(config.TypedIndex),
	}
}

func (scope Scope) Canonical() ([]byte, error) {
	if err := validateRepository(scope.Repository); err != nil {
		return nil, fmt.Errorf("%w: repository: %v", ErrInvalidScope, err)
	}
	if len(scope.Name) > MaxUnitNameBytes || !validText(scope.Name) ||
		!unitNameRE.MatchString(scope.Name) {
		return nil, fmt.Errorf(
			"%w: unit name must be a %d-byte-or-shorter token using letters, digits, '.', '_', or '-'",
			ErrInvalidScope, MaxUnitNameBytes,
		)
	}
	primary, err := canonicalPaths(scope.Primary, true)
	if err != nil {
		return nil, fmt.Errorf("%w: primary paths: %v", ErrInvalidScope, err)
	}
	supporting, err := canonicalPaths(scope.Supporting, false)
	if err != nil {
		return nil, fmt.Errorf("%w: supporting paths: %v", ErrInvalidScope, err)
	}
	if len(primary)+len(supporting) > MaxSelectedPaths {
		return nil, fmt.Errorf(
			"%w: at most %d selected paths are allowed",
			ErrInvalidScope, MaxSelectedPaths,
		)
	}
	var pathBytes int
	all := make([]string, 0, len(primary)+len(supporting))
	all = append(all, primary...)
	all = append(all, supporting...)
	seen := make(map[string]struct{}, len(all))
	for _, current := range all {
		pathBytes += len(current)
		if _, duplicate := seen[current]; duplicate {
			return nil, fmt.Errorf("%w: duplicate path %q", ErrInvalidScope, current)
		}
		seen[current] = struct{}{}
	}
	if pathBytes > MaxSelectedPathBytes {
		return nil, fmt.Errorf(
			"%w: selected paths contain %d bytes; at most %d are allowed",
			ErrInvalidScope, pathBytes, MaxSelectedPathBytes,
		)
	}
	if err := validateTypedIndex(scope.TypedIndex, supporting); err != nil {
		return nil, fmt.Errorf("%w: typed index: %v", ErrInvalidScope, err)
	}
	for _, current := range all {
		for parent := path.Dir(current); parent != "."; parent = path.Dir(parent) {
			if _, overlap := seen[parent]; overlap {
				return nil, fmt.Errorf(
					"%w: overlapping paths %q and %q",
					ErrInvalidScope, parent, current,
				)
			}
		}
	}
	canonical := struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Name       string   `json:"name"`
		Primary    []string `json:"primary"`
		Supporting []string `json:"supporting"`
	}{
		Schema: SchemaVersion, Repository: scope.Repository, Name: scope.Name,
		Primary: primary, Supporting: supporting,
	}
	return json.Marshal(canonical)
}

func (scope Scope) Digest() (string, error) {
	canonical, err := scope.Canonical()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-analysis-unit-v1\x00"))
	_, _ = hash.Write(canonical)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func (scope Scope) State() (*State, error) {
	if _, err := scope.Canonical(); err != nil {
		return nil, err
	}
	digest, err := scope.Digest()
	if err != nil {
		return nil, err
	}
	// Allocate even empty lists so omitted and explicit-empty YAML have the
	// same canonical state and public diagnostic shape.
	primary := append([]string{}, scope.Primary...)
	supporting := append([]string{}, scope.Supporting...)
	slices.Sort(primary)
	slices.Sort(supporting)
	return &State{
		Schema:              SchemaVersion,
		Name:                scope.Name,
		Digest:              digest,
		PrimaryPaths:        primary,
		SupportingPaths:     supporting,
		PrimaryPathCount:    len(primary),
		SupportingPathCount: len(supporting),
		SearchIndexPosture:  SearchIndexFocused,
		TypedIndexPosture:   typedIndexPosture(scope.TypedIndex),
		TypedIndex:          cloneTypedIndex(scope.TypedIndex),
	}, nil
}

func (state *State) Validate(repository string) error {
	if state == nil {
		return nil
	}
	scope := Scope{
		Repository: repository,
		Name:       state.Name,
		Primary:    state.PrimaryPaths,
		Supporting: state.SupportingPaths,
		TypedIndex: cloneTypedIndex(state.TypedIndex),
	}
	expected, err := scope.State()
	if err != nil {
		return err
	}
	// T30.2 whole-repository rows predate typed-index designation. Preserve
	// exactly that historical shape without allowing a current focused,
	// unit-bound state to become whole-repository authority by changing only
	// its posture string.
	legacyWholeRepository :=
		state.SearchIndexPosture == SearchIndexWholeRepository &&
			state.TypedIndexPosture == TypedIndexRepositoryRootUnbound &&
			state.TypedIndex == nil
	if state.Schema != expected.Schema ||
		state.Digest != expected.Digest ||
		state.PrimaryPathCount != expected.PrimaryPathCount ||
		state.SupportingPathCount != expected.SupportingPathCount ||
		(state.SearchIndexPosture != expected.SearchIndexPosture && !legacyWholeRepository) ||
		state.TypedIndexPosture != expected.TypedIndexPosture ||
		!equalTypedIndex(state.TypedIndex, expected.TypedIndex) ||
		!slices.Equal(state.PrimaryPaths, expected.PrimaryPaths) ||
		!slices.Equal(state.SupportingPaths, expected.SupportingPaths) {
		return fmt.Errorf("%w: committed state is not canonical", ErrInvalidScope)
	}
	return nil
}

func EqualState(left, right *State) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Schema == right.Schema &&
		left.Name == right.Name &&
		left.Digest == right.Digest &&
		left.PrimaryPathCount == right.PrimaryPathCount &&
		left.SupportingPathCount == right.SupportingPathCount &&
		left.SearchIndexPosture == right.SearchIndexPosture &&
		left.TypedIndexPosture == right.TypedIndexPosture &&
		equalTypedIndex(left.TypedIndex, right.TypedIndex) &&
		slices.Equal(left.PrimaryPaths, right.PrimaryPaths) &&
		slices.Equal(left.SupportingPaths, right.SupportingPaths)
}

func CloneState(state *State) *State {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.PrimaryPaths = slices.Clone(state.PrimaryPaths)
	cloned.SupportingPaths = slices.Clone(state.SupportingPaths)
	cloned.TypedIndex = cloneTypedIndex(state.TypedIndex)
	return &cloned
}

func validateTypedIndex(input *TypedIndex, supporting []string) error {
	if input == nil {
		return nil
	}
	if input.Kind != TypedIndexKindSCIP {
		return fmt.Errorf("kind must be %q", TypedIndexKindSCIP)
	}
	if err := repopath.Validate(input.Path); err != nil {
		return fmt.Errorf("unsafe repository-relative Git path %q", input.Path)
	}
	if !slices.Contains(supporting, input.Path) {
		return errors.New("path must exactly match one supporting path")
	}
	return nil
}

func typedIndexPosture(input *TypedIndex) string {
	if input == nil {
		return TypedIndexRepositoryRootUnbound
	}
	return TypedIndexUnitBound
}

func equalTypedIndex(left, right *TypedIndex) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func cloneTypedIndex(input *TypedIndex) *TypedIndex {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func canonicalPaths(input []string, required bool) ([]string, error) {
	if required && len(input) == 0 {
		return nil, errors.New("at least one path is required")
	}
	// Allocate even for nil input: an optional empty path set has one
	// canonical JSON representation ([] rather than null).
	result := append([]string{}, input...)
	slices.Sort(result)
	for _, value := range result {
		if err := repopath.Validate(value); err != nil {
			return nil, fmt.Errorf("unsafe repository-relative Git path %q", value)
		}
	}
	return result, nil
}

func validateRepository(repository string) error {
	if err := reponame.Validate(repository); err != nil {
		return errors.New("unsafe repository name")
	}
	return nil
}

func validText(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

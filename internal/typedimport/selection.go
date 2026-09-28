// Package typedimport implements the closed operator-supplied existing-artifact
// import provider contract for T45.7. This leaf supplies ONLY the pure, bounded
// ImportSelection contract and its strict canonical decoder: the closed authority
// an importer must be handed before it may copy and validate any artifact.
//
// It reads no filesystem, copies or verifies no artifact byte, launches no
// child/Bazel/driver/indexer/scip-go, downloads or builds no tool, registers no
// provider, and exposes no worker/result/execution path. The byte-copy, staging,
// worker, result-finalizer, publication and recovery halves are deliberately out
// of this leaf: they verify actual copied bytes against the immutable Git HEAD and
// the independently admitted universe, which requires the separately authorized
// native gate.
//
// Trust boundary: the DECLARED producer recorded here is an operator ATTESTATION
// ONLY. A name/version/digest is never treated as proof that the corresponding
// binary generated the imported bytes, and it is kept structurally separate from
// any executed-helper identity (which this contract does not model). A bare
// artifact with no exact-HEAD source attestation or no operator provenance is
// refused rather than having its HEAD inferred from mtime, current checkout,
// project_root or symbol version strings.
package typedimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"golang.org/x/mod/module"
)

// SelectionSchema versions the closed import selection result.
const SelectionSchema = "phebs-typed-import-selection-v1"

// PinnedProducer is the only declared producer the initial closed import contract
// accepts. Any other name or version refuses Unsupported rather than guessing a
// recipe; widening this set is a separately proven leaf.
const (
	PinnedProducerName    = "scip-go"
	PinnedProducerVersion = "0.2.7"
)

// Bounds are the import-selection control ceilings. They are new discovery-style
// limits, not cap increases or validated corpus coverage; a larger valid selection
// refuses with Capacity rather than growing a bound. MaxSelectionBytes is the
// binding aggregate decode ceiling: the count caps below are cheap early refusals,
// and a selection whose canonical encoding exceeds MaxSelectionBytes refuses Invalid
// at the length guard before decode, so the caps are never reachable at their
// maximum when selectors/paths are large. Both SCIP byte ceilings are derived from
// the frozen typedindex authority so they cannot drift from the sealed execution
// envelope.
const (
	MaxSelectionBytes     = 64 << 10
	MaxArtifacts          = 512
	MaxRoots              = 64
	MaxCoverageSelectors  = 8192
	MaxPathBytes          = 512
	MaxTokenBytes         = 64
	MaxArtifactBytes      = int64(typedindex.MaxSCIPMemberBytes)
	MaxAggregateSCIPBytes = int64(typedindex.MaxSCIPAggregateBytes)
)

// ImportArtifact is one expected operator-installed artifact file. Digest is the
// operator-attested expected sha256 of the copied bytes; verifying the actual copy
// against it is the native-gated worker's job, not this contract's.
type ImportArtifact struct {
	Path   string `json:"path"`   // repo-relative artifact path inside the admitted inventory
	Bytes  int64  `json:"bytes"`  // expected size in bytes
	Digest string `json:"digest"` // attested expected sha256 of the artifact bytes
}

// Producer is the DECLARED producer identity. It is an operator attestation only;
// the contract never treats it as proof the binary generated the imported bytes.
type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"` // attested claimed producer binary digest
}

// RootMapping maps one sealed known input root in the artifact to a repo-relative
// target root. Project roots may be normalized only from independently sealed
// known roots, never from raw SCIP alone; "." is the repository root.
type RootMapping struct {
	Input string `json:"input"` // sealed known source root as it appears in the artifact
	Repo  string `json:"repo"`  // repo-relative target root; "." is the repository root
}

// ImportSelection is the admitted closed import authority. Artifacts and Roots are
// strictly ascending and distinct by their key; Coverage and Excluded are strictly
// ascending, distinct, disjoint literal package selectors with Coverage non-empty.
// Roots may be empty, which authorizes NO project-root normalization; the
// input→repo mapping semantics an importer applies are the native-gated worker's
// concern, not this contract's. Provenance is shape-validated only here: its trust
// derives solely from authenticated operator state at a later admission boundary,
// exactly as the frozen typedindex Authority does, never from this decoder.
type ImportSelection struct {
	Schema     string            `json:"schema"`
	Source     typedindex.Source `json:"source"`     // repository/incarnation/generation/exact HEAD
	Producer   Producer          `json:"producer"`   // declared producer attestation
	Artifacts  []ImportArtifact  `json:"artifacts"`  // expected artifact digests/sizes
	Roots      []RootMapping     `json:"roots"`      // sealed input→repo root mapping; empty authorizes no normalization
	Coverage   []string          `json:"coverage"`   // mandatory non-empty covered package selectors
	Excluded   []string          `json:"excluded"`   // explicitly excluded package selectors
	Provenance string            `json:"provenance"` // operator provenance identity; trust comes from authenticated state, not this shape check
}

// Encode returns the canonical JSON bytes of the selection.
func (s ImportSelection) Encode() ([]byte, error) { return json.Marshal(s) }

// Digest is the sha256 identity of the canonical encoding, or "" if unencodable.
func (s ImportSelection) Digest() string {
	b, err := s.Encode()
	if err != nil {
		return ""
	}
	return digestBytes(b)
}

// DecodeImportSelection strictly decodes canonical bytes and re-validates the
// closed invariants, so a stored or transported selection cannot drift from one
// an operator authored. It mirrors the frozen typedindex canonicality discipline:
// DisallowUnknownFields plus a json.Compact==json.Marshal byte comparison.
func DecodeImportSelection(ctx context.Context, raw []byte) (ImportSelection, error) {
	if err := ctx.Err(); err != nil {
		return ImportSelection{}, err
	}
	if len(raw) == 0 || len(raw) > MaxSelectionBytes || !utf8.Valid(raw) {
		return ImportSelection{}, typedindex.Invalid
	}
	var s ImportSelection
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&s) != nil {
		return ImportSelection{}, typedindex.Invalid
	}
	want, err := json.Marshal(s)
	if err != nil {
		return ImportSelection{}, typedindex.Invalid
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || !bytes.Equal(compact.Bytes(), want) {
		return ImportSelection{}, typedindex.Invalid
	}
	if err := s.validate(); err != nil {
		return ImportSelection{}, err
	}
	return s, nil
}

func (s ImportSelection) validate() error {
	if s.Schema != SelectionSchema {
		return typedindex.Invalid
	}
	// Exact-HEAD source attestation is mandatory; a bare artifact refuses. HEAD is
	// never inferred from mtime, checkout, project_root or symbol version strings.
	if err := s.Source.Validate(); err != nil {
		return err
	}
	if !token(s.Provenance) {
		return typedindex.Invalid
	}
	// The declared producer is pinned and is an attestation only, never proof of
	// execution; any other producer refuses rather than guessing a recipe.
	if s.Producer.Name != PinnedProducerName || s.Producer.Version != PinnedProducerVersion {
		return typedindex.Unsupported
	}
	if !isDigest(s.Producer.Digest) {
		return typedindex.Invalid
	}
	if err := validateArtifacts(s.Artifacts); err != nil {
		return err
	}
	if err := validateRoots(s.Roots); err != nil {
		return err
	}
	// Retained coverage is mandatory and non-empty; covered/excluded must be
	// disjoint, ascending, distinct literal selectors.
	if len(s.Coverage) == 0 {
		return typedindex.Invalid
	}
	if len(s.Coverage) > MaxCoverageSelectors || len(s.Excluded) > MaxCoverageSelectors {
		return typedindex.Capacity
	}
	if err := validateSelectors(s.Coverage); err != nil {
		return err
	}
	if len(s.Excluded) > 0 {
		if err := validateSelectors(s.Excluded); err != nil {
			return err
		}
		excluded := make(map[string]bool, len(s.Excluded))
		for _, e := range s.Excluded {
			excluded[e] = true
		}
		for _, c := range s.Coverage {
			if excluded[c] {
				return typedindex.Invalid
			}
		}
	}
	return nil
}

func validateArtifacts(arts []ImportArtifact) error {
	if len(arts) == 0 || len(arts) > MaxArtifacts {
		return typedindex.Capacity
	}
	ceiling := MaxArtifactBytes
	var aggregate int64
	prev := ""
	seen := make(map[string]bool, len(arts))
	for _, a := range arts {
		if !controlPath(a.Path) || a.Path <= prev || seen[a.Path] {
			return typedindex.Invalid
		}
		seen[a.Path] = true
		if a.Bytes <= 0 || a.Bytes > ceiling || !isDigest(a.Digest) {
			return typedindex.Invalid
		}
		if a.Bytes > MaxAggregateSCIPBytes-aggregate {
			return typedindex.Capacity
		}
		aggregate += a.Bytes
		prev = a.Path
	}
	return nil
}

func validateRoots(roots []RootMapping) error {
	if len(roots) > MaxRoots {
		return typedindex.Capacity
	}
	prev := ""
	seen := make(map[string]bool, len(roots))
	for _, r := range roots {
		if !controlPath(r.Input) || r.Input <= prev || seen[r.Input] {
			return typedindex.Invalid
		}
		seen[r.Input] = true
		if !repoDir(r.Repo) {
			return typedindex.Invalid
		}
		prev = r.Input
	}
	return nil
}

// validateSelectors requires a strictly ascending, distinct set of explicit
// literal package selectors. It refuses wildcards, recursive "./..." patterns, the
// all/std defaults, absolutes and empty/oversized values, so no owned invocation
// can silently expand to a recursive default.
func validateSelectors(sel []string) error {
	prev := ""
	seen := make(map[string]bool, len(sel))
	for _, s := range sel {
		if !literalSelector(s) || s <= prev || seen[s] {
			return typedindex.Invalid
		}
		seen[s] = true
		prev = s
	}
	return nil
}

// literalSelector accepts only an explicit literal package selector: a valid
// import path, or a repo-relative "./dir" path.
func literalSelector(s string) bool {
	if s == "" || len(s) > MaxPathBytes || strings.ContainsAny(s, "*\\\x00") {
		return false
	}
	if strings.Contains(s, "...") {
		return false
	}
	switch s {
	case ".", "./", "all", "std", "./...", "..":
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if strings.HasPrefix(s, "/") {
		return false
	}
	if strings.HasPrefix(s, "./") {
		rest := s[2:]
		return rest != "" && fs.ValidPath(rest)
	}
	return module.CheckImportPath(s) == nil
}

// controlPath validates a repo-relative path with the same grammar as the frozen
// inventory bundle paths: a valid, bounded, non-escaping path with no
// backslash/colon/control bytes and no over-long component. "." alone is rejected.
func controlPath(s string) bool {
	if s == "." || s == "" || len(s) > MaxPathBytes || !fs.ValidPath(s) || strings.ContainsAny(s, "\\:") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, c := range strings.Split(s, "/") {
		if c == "" || c == ".." || len(c) > 255 {
			return false
		}
	}
	return true
}

// repoDir validates a repo-relative directory path ("." allowed).
func repoDir(s string) bool {
	if s == "." {
		return true
	}
	return controlPath(s)
}

// token validates a bounded provenance/identity token with the frozen grammar.
func token(s string) bool {
	if len(s) == 0 || len(s) > MaxTokenBytes {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return s != "." && s != ".."
}

func isDigest(s string) bool {
	if !strings.HasPrefix(s, "sha256:") {
		return false
	}
	hexPart := strings.TrimPrefix(s, "sha256:")
	if len(hexPart) != 64 {
		return false
	}
	// Lowercase-only, matching the frozen typedindex digest grammar.
	for _, c := range hexPart {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func digestBytes(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

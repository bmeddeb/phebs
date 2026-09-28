package typedimport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func validSource() typedindex.Source {
	return typedindex.Source{
		Repository:  "example.test/team/repo",
		Incarnation: "repo-1",
		Generation:  "sha256:" + strings.Repeat("ab", 32),
		Commit:      strings.Repeat("a", 40),
	}
}

func dg(fill string) string { return "sha256:" + strings.Repeat(fill, 32) }

// validSelection builds a well-formed scip-go/0.2.7 import selection.
func validSelection() ImportSelection {
	return ImportSelection{
		Schema:   SelectionSchema,
		Source:   validSource(),
		Producer: Producer{Name: PinnedProducerName, Version: PinnedProducerVersion, Digest: dg("cd")},
		Artifacts: []ImportArtifact{
			{Path: "index.scip", Bytes: 1024, Digest: dg("ef")},
		},
		Roots:      []RootMapping{{Input: "src/root", Repo: "."}},
		Coverage:   []string{"example.test/team/repo/cmd"},
		Excluded:   []string{"example.test/team/repo/internal/skip"},
		Provenance: "operator-install-1",
	}
}

func mustDecode(t *testing.T, s ImportSelection) ImportSelection {
	t.Helper()
	raw, err := s.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeImportSelection(context.Background(), raw)
	if err != nil {
		t.Fatalf("decode of a valid selection failed: %v", err)
	}
	return got
}

func TestImportSelectionValid(t *testing.T) {
	sel := validSelection()
	got := mustDecode(t, sel)
	raw, _ := sel.Encode()
	d1 := got.Digest()
	if !isDigest(d1) {
		t.Fatalf("bad digest %q", d1)
	}
	re, _ := got.Encode()
	if string(re) != string(raw) {
		t.Fatal("round-trip is not byte-stable")
	}
	if got.Producer.Name != PinnedProducerName || got.Producer.Version != PinnedProducerVersion {
		t.Fatalf("producer not preserved: %#v", got.Producer)
	}
	if len(got.Coverage) != 1 || len(got.Excluded) != 1 || len(got.Artifacts) != 1 || len(got.Roots) != 1 {
		t.Fatalf("unexpected selection shape: %#v", got)
	}
}

func TestImportSelectionRefusals(t *testing.T) {
	ctx := context.Background()

	// mutate builds a selection, applies fn, encodes and decodes, returning the error.
	mutate := func(fn func(*ImportSelection)) error {
		s := validSelection()
		fn(&s)
		raw, err := s.Encode()
		if err != nil {
			t.Fatalf("encode mutated selection: %v", err)
		}
		_, err = DecodeImportSelection(ctx, raw)
		return err
	}

	cases := []struct {
		name string
		fn   func(*ImportSelection)
		want error
	}{
		{"wrong schema", func(s *ImportSelection) { s.Schema = "phebs-typed-import-selection-v0" }, typedindex.Invalid},
		{"empty schema", func(s *ImportSelection) { s.Schema = "" }, typedindex.Invalid},
		{"empty source commit", func(s *ImportSelection) { s.Source.Commit = "" }, typedindex.Invalid},
		{"short source commit", func(s *ImportSelection) { s.Source.Commit = strings.Repeat("a", 39) }, typedindex.Invalid},
		{"uppercase source commit", func(s *ImportSelection) { s.Source.Commit = strings.Repeat("A", 40) }, typedindex.Invalid},
		{"bad source generation", func(s *ImportSelection) { s.Source.Generation = "nope" }, typedindex.Invalid},
		{"empty repository", func(s *ImportSelection) { s.Source.Repository = "" }, typedindex.Invalid},
		{"empty provenance", func(s *ImportSelection) { s.Provenance = "" }, typedindex.Invalid},
		{"oversized provenance", func(s *ImportSelection) { s.Provenance = strings.Repeat("a", MaxTokenBytes+1) }, typedindex.Invalid},
		{"provenance bad char", func(s *ImportSelection) { s.Provenance = "oper ator" }, typedindex.Invalid},
		{"wrong producer name", func(s *ImportSelection) { s.Producer.Name = "scip-python" }, typedindex.Unsupported},
		{"empty producer name", func(s *ImportSelection) { s.Producer.Name = "" }, typedindex.Unsupported},
		{"wrong producer version", func(s *ImportSelection) { s.Producer.Version = "0.2.8" }, typedindex.Unsupported},
		{"empty producer version", func(s *ImportSelection) { s.Producer.Version = "" }, typedindex.Unsupported},
		{"bad producer digest", func(s *ImportSelection) { s.Producer.Digest = "sha256:zz" }, typedindex.Invalid},
		{"empty producer digest", func(s *ImportSelection) { s.Producer.Digest = "" }, typedindex.Invalid},
		{"uppercase producer digest", func(s *ImportSelection) { s.Producer.Digest = "sha256:" + strings.Repeat("CD", 32) }, typedindex.Invalid},
		{"zero artifacts", func(s *ImportSelection) { s.Artifacts = nil }, typedindex.Capacity},
		{"artifact zero bytes", func(s *ImportSelection) { s.Artifacts[0].Bytes = 0 }, typedindex.Invalid},
		{"artifact negative bytes", func(s *ImportSelection) { s.Artifacts[0].Bytes = -1 }, typedindex.Invalid},
		{"artifact over ceiling", func(s *ImportSelection) { s.Artifacts[0].Bytes = MaxArtifactBytes + 1 }, typedindex.Invalid},
		{"artifact bad digest", func(s *ImportSelection) { s.Artifacts[0].Digest = "md5:" + strings.Repeat("a", 32) }, typedindex.Invalid},
		{"artifact absolute path", func(s *ImportSelection) { s.Artifacts[0].Path = "/index.scip" }, typedindex.Invalid},
		{"artifact escaping path", func(s *ImportSelection) { s.Artifacts[0].Path = "../index.scip" }, typedindex.Invalid},
		{"artifact dot path", func(s *ImportSelection) { s.Artifacts[0].Path = "." }, typedindex.Invalid},
		{"artifact empty path", func(s *ImportSelection) { s.Artifacts[0].Path = "" }, typedindex.Invalid},
		{"artifact duplicate path", func(s *ImportSelection) {
			s.Artifacts = []ImportArtifact{{Path: "a.scip", Bytes: 10, Digest: dg("11")}, {Path: "a.scip", Bytes: 10, Digest: dg("22")}}
		}, typedindex.Invalid},
		{"artifact unsorted", func(s *ImportSelection) {
			s.Artifacts = []ImportArtifact{{Path: "b.scip", Bytes: 10, Digest: dg("11")}, {Path: "a.scip", Bytes: 10, Digest: dg("22")}}
		}, typedindex.Invalid},
		{"artifact aggregate over", func(s *ImportSelection) {
			half := MaxArtifactBytes
			s.Artifacts = []ImportArtifact{
				{Path: "a.scip", Bytes: half, Digest: dg("11")},
				{Path: "b.scip", Bytes: half, Digest: dg("22")},
				{Path: "c.scip", Bytes: half, Digest: dg("33")},
				{Path: "d.scip", Bytes: half, Digest: dg("44")},
			}
		}, typedindex.Capacity},
		{"empty coverage", func(s *ImportSelection) { s.Coverage = nil }, typedindex.Invalid},
		{"coverage wildcard", func(s *ImportSelection) { s.Coverage = []string{"example.com/..."} }, typedindex.Invalid},
		{"coverage dot-ellipsis", func(s *ImportSelection) { s.Coverage = []string{"./..."} }, typedindex.Invalid},
		{"coverage all", func(s *ImportSelection) { s.Coverage = []string{"all"} }, typedindex.Invalid},
		{"coverage std", func(s *ImportSelection) { s.Coverage = []string{"std"} }, typedindex.Invalid},
		{"coverage empty selector", func(s *ImportSelection) { s.Coverage = []string{""} }, typedindex.Invalid},
		{"coverage absolute", func(s *ImportSelection) { s.Coverage = []string{"/abs"} }, typedindex.Invalid},
		{"coverage star", func(s *ImportSelection) { s.Coverage = []string{"example.com/*"} }, typedindex.Invalid},
		{"coverage duplicate", func(s *ImportSelection) { s.Coverage = []string{"a.com/x", "a.com/x"} }, typedindex.Invalid},
		{"coverage unsorted", func(s *ImportSelection) { s.Coverage = []string{"b.com/x", "a.com/x"} }, typedindex.Invalid},
		{"excluded unsorted", func(s *ImportSelection) { s.Excluded = []string{"b.com/x", "a.com/x"} }, typedindex.Invalid},
		{"coverage excluded overlap", func(s *ImportSelection) {
			s.Coverage = []string{"a.com/x", "a.com/y"}
			s.Excluded = []string{"a.com/x"}
		}, typedindex.Invalid},
		{"root escaping input", func(s *ImportSelection) { s.Roots = []RootMapping{{Input: "../x", Repo: "."}} }, typedindex.Invalid},
		{"root absolute input", func(s *ImportSelection) { s.Roots = []RootMapping{{Input: "/x", Repo: "."}} }, typedindex.Invalid},
		{"root dot input", func(s *ImportSelection) { s.Roots = []RootMapping{{Input: ".", Repo: "."}} }, typedindex.Invalid},
		{"root bad repo", func(s *ImportSelection) { s.Roots = []RootMapping{{Input: "src", Repo: "/abs"}} }, typedindex.Invalid},
		{"root duplicate input", func(s *ImportSelection) {
			s.Roots = []RootMapping{{Input: "a", Repo: "."}, {Input: "a", Repo: "x"}}
		}, typedindex.Invalid},
		{"root unsorted", func(s *ImportSelection) {
			s.Roots = []RootMapping{{Input: "b", Repo: "."}, {Input: "a", Repo: "x"}}
		}, typedindex.Invalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := mutate(tc.fn); err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestImportSelectionCapacityCounts(t *testing.T) {
	ctx := context.Background()
	decode := func(s ImportSelection) error {
		raw, err := s.Encode()
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		_, err = DecodeImportSelection(ctx, raw)
		return err
	}

	// artifacts cap+1 (count check precedes per-entry validation, so minimal entries suffice)
	over := validSelection()
	over.Artifacts = make([]ImportArtifact, MaxArtifacts+1)
	for i := range over.Artifacts {
		over.Artifacts[i] = ImportArtifact{Path: "a", Bytes: 1, Digest: "d"}
	}
	if err := decode(over); err != typedindex.Capacity {
		t.Fatalf("artifact cap+1 err = %v, want Capacity", err)
	}
	// artifacts exactly at cap is not a count refusal (per-entry validation then applies)
	atCap := validSelection()
	atCap.Artifacts = make([]ImportArtifact, MaxArtifacts)
	for i := range atCap.Artifacts {
		atCap.Artifacts[i] = ImportArtifact{Path: "a", Bytes: 1, Digest: "d"}
	}
	if err := decode(atCap); err == typedindex.Capacity {
		t.Fatalf("artifact cap should not be a count Capacity refusal, got %v", err)
	}

	// roots cap+1
	overRoots := validSelection()
	overRoots.Roots = make([]RootMapping, MaxRoots+1)
	for i := range overRoots.Roots {
		overRoots.Roots[i] = RootMapping{Input: "a", Repo: "."}
	}
	if err := decode(overRoots); err != typedindex.Capacity {
		t.Fatalf("root cap+1 err = %v, want Capacity", err)
	}

	// coverage cap+1
	overCov := validSelection()
	overCov.Coverage = make([]string, MaxCoverageSelectors+1)
	for i := range overCov.Coverage {
		overCov.Coverage[i] = "a"
	}
	if err := decode(overCov); err != typedindex.Capacity {
		t.Fatalf("coverage cap+1 err = %v, want Capacity", err)
	}
}

func TestImportSelectionCanonical(t *testing.T) {
	ctx := context.Background()
	sel := validSelection()
	raw, err := sel.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// Non-canonical: re-encoded from a map (alphabetical keys) must refuse.
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	reordered, _ := json.Marshal(m)
	if _, err := DecodeImportSelection(ctx, reordered); err != typedindex.Invalid {
		t.Fatalf("reordered-key bytes err = %v, want Invalid", err)
	}
	// Unknown field must refuse.
	if _, err := DecodeImportSelection(ctx, []byte(strings.Replace(string(raw), `{"schema"`, `{"evil":1,"schema"`, 1))); err != typedindex.Invalid {
		t.Fatalf("unknown-field bytes err = %v, want Invalid", err)
	}
	// Tamper negative control: corrupt the schema value (same length) -> refuse.
	tampered := []byte(strings.Replace(string(raw), SelectionSchema, "phebs-typed-import-selection-v0", 1))
	if string(tampered) == string(raw) {
		t.Fatal("tamper control did not change bytes")
	}
	if _, err := DecodeImportSelection(ctx, tampered); err != typedindex.Invalid {
		t.Fatalf("tampered schema err = %v, want Invalid", err)
	}
	// Tamper: swap the declared producer version -> Unsupported (still decodes canonically).
	badProd := sel
	badProd.Producer.Version = "9.9.9"
	badRaw, _ := badProd.Encode()
	if _, err := DecodeImportSelection(ctx, badRaw); err != typedindex.Unsupported {
		t.Fatalf("tampered producer err = %v, want Unsupported", err)
	}
	// Empty and oversize inputs refuse.
	if _, err := DecodeImportSelection(ctx, nil); err != typedindex.Invalid {
		t.Fatalf("empty err = %v, want Invalid", err)
	}
	if _, err := DecodeImportSelection(ctx, make([]byte, MaxSelectionBytes+1)); err != typedindex.Invalid {
		t.Fatalf("oversize err = %v, want Invalid", err)
	}
	// Invalid UTF-8 refuses.
	if _, err := DecodeImportSelection(ctx, []byte{0xff, 0xfe, 0xfd}); err != typedindex.Invalid {
		t.Fatalf("invalid utf8 err = %v, want Invalid", err)
	}
}

func TestImportSelectionDigestContentBound(t *testing.T) {
	a := validSelection()
	b := validSelection()
	b.Provenance = "operator-install-2"
	if a.Digest() == b.Digest() {
		t.Fatal("digest is not content-bound: differing provenance collided")
	}
	if a.Digest() != validSelection().Digest() {
		t.Fatal("digest is not stable for identical content")
	}
}

func TestImportLiteralSelector(t *testing.T) {
	for _, ok := range []string{"example.com/a/b", "./cmd/phebs", "./a", "github.com/x/y"} {
		if !literalSelector(ok) {
			t.Fatalf("literalSelector(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", ".", "./", "./...", "...", "all", "std", "m/*", "/abs", "a\\b", "a\x00b", strings.Repeat("a", MaxPathBytes+1)} {
		if literalSelector(bad) {
			t.Fatalf("literalSelector(%q) = true, want false", bad)
		}
	}
}

func TestImportIsDigest(t *testing.T) {
	lower := "sha256:" + strings.Repeat("ab0123", 10) + "abcd"
	if !isDigest(lower) {
		t.Fatalf("isDigest(%q) = false, want true", lower)
	}
	for _, bad := range []string{"", "sha256:", lower[:len(lower)-1], "sha256:" + strings.Repeat("AB", 32), "md5:" + strings.Repeat("a", 32), "sha256:" + strings.Repeat("g", 64)} {
		if isDigest(bad) {
			t.Fatalf("isDigest(%q) = true, want false", bad)
		}
	}
}

func TestImportToken(t *testing.T) {
	for _, ok := range []string{"a", "repo-1", "operator_install.1", strings.Repeat("x", MaxTokenBytes)} {
		if !token(ok) {
			t.Fatalf("token(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "a b", "a/b", "a:b", strings.Repeat("x", MaxTokenBytes+1)} {
		if token(bad) {
			t.Fatalf("token(%q) = true, want false", bad)
		}
	}
}

// TestImportCeilingsBoundToFrozenAuthority pins the SCIP byte ceilings to the
// frozen typedindex constants so a future edit cannot silently drift the import
// selection's per-artifact or aggregate limit away from the sealed envelope.
func TestImportCeilingsBoundToFrozenAuthority(t *testing.T) {
	if MaxArtifactBytes != int64(typedindex.MaxSCIPMemberBytes) {
		t.Fatalf("MaxArtifactBytes = %d, want frozen typedindex.MaxSCIPMemberBytes = %d", MaxArtifactBytes, typedindex.MaxSCIPMemberBytes)
	}
	if MaxAggregateSCIPBytes != int64(typedindex.MaxSCIPAggregateBytes) {
		t.Fatalf("MaxAggregateSCIPBytes = %d, want frozen typedindex.MaxSCIPAggregateBytes = %d", MaxAggregateSCIPBytes, typedindex.MaxSCIPAggregateBytes)
	}
	if MaxArtifactBytes <= 0 || MaxAggregateSCIPBytes <= 0 {
		t.Fatalf("ceilings must be positive: member=%d aggregate=%d", MaxArtifactBytes, MaxAggregateSCIPBytes)
	}
}

// TestImportSelectionLengthGuardIsolating proves the MaxSelectionBytes decode guard
// fires on an OTHERWISE validate-legal selection: without the length guard the same
// bytes would decode successfully, so this isolates the guard rather than leaning on
// a JSON parse failure. It also documents that the byte ceiling is the binding
// aggregate limit (a count-legal selection can still exceed it and refuse Invalid).
func TestImportSelectionLengthGuardIsolating(t *testing.T) {
	ctx := context.Background()
	s := validSelection()
	// Build a count-legal (<= MaxCoverageSelectors), ascending, distinct literal
	// selector set large enough that the canonical encoding exceeds MaxSelectionBytes.
	n := MaxCoverageSelectors / 2
	s.Coverage = make([]string, n)
	for i := 0; i < n; i++ {
		s.Coverage[i] = fmt.Sprintf("example.test/pkg%05d", i)
	}
	s.Excluded = nil
	// The selection must be validate-legal, so the only thing that can refuse it is
	// the decode-time length guard.
	if err := s.validate(); err != nil {
		t.Fatalf("expected a validate-legal oversized selection, got %v", err)
	}
	raw, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= MaxSelectionBytes {
		t.Fatalf("test premise broken: encoding is %d bytes, want > MaxSelectionBytes %d", len(raw), MaxSelectionBytes)
	}
	if _, err := DecodeImportSelection(ctx, raw); err != typedindex.Invalid {
		t.Fatalf("oversized validate-legal selection err = %v, want Invalid (length guard)", err)
	}
}

package glossary_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/bmeddeb/phebs/internal/glossary"
	"github.com/bmeddeb/phebs/internal/glossary/genrepo"
)

const frozenDigest = "sha256:ce13b715607141f2833c8ade57b8aa552d89b68475c5884257972a7defcb3274"

func TestEmbeddedGlossaryAndGeneratedGoStayCanonical(t *testing.T) {
	document, canonical, digest, err := glossary.LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded glossary: %v", err)
	}
	if digest != frozenDigest || glossary.Digest != frozenDigest {
		t.Fatalf("digest = %q / %q, want %q", digest, glossary.Digest, frozenDigest)
	}
	if !json.Valid(canonical) {
		t.Fatal("canonical glossary is not JSON")
	}
	if document.SchemaVersion != glossary.SchemaVersion {
		t.Fatalf("schema = %q, want %q", document.SchemaVersion, glossary.SchemaVersion)
	}
	if !reflect.DeepEqual(document.Capabilities, glossary.Capabilities) {
		t.Fatalf("generated capabilities differ from canonical input")
	}
	if !reflect.DeepEqual(document.Terms, glossary.Terms) {
		t.Fatalf("generated Go terms differ from canonical input")
	}
	for _, term := range glossary.Terms {
		projected, ok := glossary.Lookup(term.ID)
		if !ok || !reflect.DeepEqual(projected, term) {
			t.Fatalf("lookup %q = %+v, %v", term.ID, projected, ok)
		}
		if !slices.Contains(term.Surfaces, glossary.SurfaceManual) ||
			!slices.Contains(term.Surfaces, glossary.SurfaceMCP) {
			t.Fatalf("%s lacks required MANUAL/MCP registration", term.ID)
		}
	}
	if _, ok := glossary.Lookup(glossary.TermID("stale_term")); ok {
		t.Fatal("unknown term lookup succeeded")
	}
}

func TestCanonicalEqualInputProducesByteEqualArtifacts(t *testing.T) {
	document, canonical, digest, err := glossary.LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded glossary: %v", err)
	}
	first, err := glossary.Generate(glossary.Source())
	if err != nil {
		t.Fatalf("generate glossary: %v", err)
	}

	slices.Reverse(document.Capabilities)
	slices.Reverse(document.Terms)
	for index := range document.Terms {
		term := &document.Terms[index]
		slices.Reverse(term.Modes)
		slices.Reverse(term.Surfaces)
		slices.Reverse(term.WireAliases)
		slices.Reverse(term.Availability.RequiresAll)
		slices.Reverse(term.Availability.RequiresAny)
	}
	permuted, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal permuted glossary: %v", err)
	}
	_, canonicalAgain, digestAgain, err := glossary.Load(permuted)
	if err != nil {
		t.Fatalf("load permuted glossary: %v", err)
	}
	second, err := glossary.Generate(permuted)
	if err != nil {
		t.Fatalf("generate permuted glossary: %v", err)
	}
	if !bytes.Equal(canonicalAgain, canonical) || digestAgain != digest {
		t.Fatal("semantically equal input changed canonical bytes or digest")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("semantically equal input changed generated projection bytes")
	}
}

func TestGlossaryRejectsUnsafeAmbiguousOrStaleInput(t *testing.T) {
	valid := loadDocument(t)
	raw := glossary.Source()
	tests := []struct {
		name   string
		mutate func(glossary.Document) []byte
	}{
		{
			name: "unknown field",
			mutate: func(glossary.Document) []byte {
				return bytes.Replace(raw, []byte(`"schema_version":`),
					[]byte(`"unexpected":true,"schema_version":`), 1)
			},
		},
		{
			name: "duplicate term id",
			mutate: func(value glossary.Document) []byte {
				value.Terms = append(value.Terms, value.Terms[0])
				return mustJSON(t, value)
			},
		},
		{
			name: "stale term id",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].ID = "known_consumers"
				return mustJSON(t, value)
			},
		},
		{
			name: "duplicate label",
			mutate: func(value glossary.Document) []byte {
				value.Terms[1].Label = value.Terms[0].Label
				return mustJSON(t, value)
			},
		},
		{
			name: "unknown capability declaration",
			mutate: func(value glossary.Document) []byte {
				value.Capabilities[0] = "unreviewed-capability"
				return mustJSON(t, value)
			},
		},
		{
			name: "unknown capability reference",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].Availability.RequiresAll =
					[]glossary.Capability{"unreviewed-capability"}
				return mustJSON(t, value)
			},
		},
		{
			name: "duplicate mode",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].Modes = append(value.Terms[0].Modes, value.Terms[0].Modes[0])
				return mustJSON(t, value)
			},
		},
		{
			name: "unknown surface",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].Surfaces[0] = "dashboard"
				return mustJSON(t, value)
			},
		},
		{
			name: "missing MANUAL registration",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].Surfaces = slices.DeleteFunc(
					value.Terms[0].Surfaces,
					func(surface glossary.Surface) bool { return surface == "manual" },
				)
				return mustJSON(t, value)
			},
		},
		{
			name: "shared wire alias",
			mutate: func(value glossary.Document) []byte {
				value.Terms[1].WireAliases = []string{value.Terms[0].WireAliases[0]}
				return mustJSON(t, value)
			},
		},
		{
			name: "oversized text",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].ShortHelp = strings.Repeat("x", 4097)
				return mustJSON(t, value)
			},
		},
		{
			name: "control character",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].ShortHelp = "unsafe\nheading"
				return mustJSON(t, value)
			},
		},
		{
			name: "manual markup",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].ExpandedHelp = "unsafe [link](target)"
				return mustJSON(t, value)
			},
		},
		{
			name: "untrimmed text",
			mutate: func(value glossary.Document) []byte {
				value.Terms[0].ExpandedHelp += " "
				return mustJSON(t, value)
			},
		},
		{
			name: "invalid UTF-8",
			mutate: func(glossary.Document) []byte {
				return bytes.Replace(raw, []byte(`"Analysis scope & gaps"`),
					[]byte{'"', 0xff, '"'}, 1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneDocument(t, valid)
			if _, _, _, err := glossary.Load(test.mutate(value)); err == nil {
				t.Fatal("Load accepted invalid glossary")
			}
		})
	}
}

func TestGeneratedLanguageProjectionsAreComplete(t *testing.T) {
	document := loadDocument(t)
	artifacts, err := glossary.Generate(glossary.Source())
	if err != nil {
		t.Fatalf("generate glossary: %v", err)
	}
	if artifacts.Digest != frozenDigest {
		t.Fatalf("artifact digest = %q", artifacts.Digest)
	}
	for _, term := range document.Terms {
		if !bytes.Contains(artifacts.Go, []byte(term.ID)) ||
			!bytes.Contains(artifacts.Go, []byte(term.Label)) {
			t.Errorf("Go projection omits term %s", term.ID)
		}
		for name, content := range map[string][]byte{
			"TypeScript": artifacts.TypeScript,
			"schema":     artifacts.Schema,
			"MCP":        artifacts.MCP,
		} {
			if !bytes.Contains(content, []byte(term.ID)) {
				t.Errorf("%s projection omits term id %s", name, term.ID)
			}
		}
		if !bytes.Contains(artifacts.Manual, []byte(term.Label)) {
			t.Errorf("MANUAL projection omits term label %s", term.Label)
		}
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(artifacts.Schema, &schema); err != nil {
		t.Fatalf("decode generated schema: %v", err)
	}
	if _, err := schema.Resolve(nil); err != nil {
		t.Fatalf("resolve generated schema: %v", err)
	}
	var mcp struct {
		SchemaVersion  string `json:"schema_version"`
		GlossaryDigest string `json:"glossary_digest"`
		Terms          []struct {
			ID          glossary.TermID `json:"id"`
			Description string          `json:"description"`
		} `json:"terms"`
	}
	if err := json.Unmarshal(artifacts.MCP, &mcp); err != nil {
		t.Fatalf("decode MCP projection: %v", err)
	}
	if mcp.SchemaVersion != glossary.MCPProjectionSchemaVersion ||
		mcp.GlossaryDigest != frozenDigest || len(mcp.Terms) != len(document.Terms) {
		t.Fatal("MCP projection does not bind the complete glossary")
	}
	for index, term := range document.Terms {
		if mcp.Terms[index].ID != term.ID ||
			!strings.Contains(mcp.Terms[index].Description, term.ShortHelp) ||
			!strings.Contains(mcp.Terms[index].Description, term.EvidenceBoundary) ||
			!strings.Contains(mcp.Terms[index].Description, term.AuthorityBoundary) {
			t.Errorf("MCP projection term %d differs from %s", index, term.ID)
		}
	}
}

func TestQualifiedSurfaceRegistrations(t *testing.T) {
	want := map[glossary.Surface][]glossary.TermID{
		glossary.SurfaceAtlas: {
			glossary.TermCoverageCertificate,
			glossary.TermMatchingStaticEvidence,
		},
		glossary.SurfaceCallerMap: {
			glossary.TermAnalysisScopeAndGaps,
			glossary.TermCouldNotResolve,
			glossary.TermMatchingStaticEvidence,
			glossary.TermNameMatchNeedingReview,
			glossary.TermResolvedCaller,
		},
		glossary.SurfaceImpact: {
			glossary.TermAnalysisScopeAndGaps,
			glossary.TermCouldNotResolve,
			glossary.TermCoverageCertificate,
			glossary.TermMatchingStaticEvidence,
		},
		glossary.SurfaceManual: {
			glossary.TermAnalysisScopeAndGaps,
			glossary.TermCouldNotResolve,
			glossary.TermCoverageCertificate,
			glossary.TermImplementationEvidence,
			glossary.TermMatchingStaticEvidence,
			glossary.TermNameMatchNeedingReview,
			glossary.TermResolvedCaller,
			glossary.TermSuccessCriterion,
		},
		glossary.SurfaceMCP: {
			glossary.TermAnalysisScopeAndGaps,
			glossary.TermCouldNotResolve,
			glossary.TermCoverageCertificate,
			glossary.TermImplementationEvidence,
			glossary.TermMatchingStaticEvidence,
			glossary.TermNameMatchNeedingReview,
			glossary.TermResolvedCaller,
			glossary.TermSuccessCriterion,
		},
		glossary.SurfaceWorkbench: {
			glossary.TermAnalysisScopeAndGaps,
			glossary.TermCouldNotResolve,
			glossary.TermCoverageCertificate,
			glossary.TermImplementationEvidence,
			glossary.TermMatchingStaticEvidence,
			glossary.TermNameMatchNeedingReview,
			glossary.TermResolvedCaller,
			glossary.TermSuccessCriterion,
		},
	}
	got := make(map[glossary.Surface][]glossary.TermID)
	for _, term := range glossary.Terms {
		for _, surface := range term.Surfaces {
			got[surface] = append(got[surface], term.ID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qualified surface registrations = %#v, want %#v", got, want)
	}
}

func TestCapabilityAvailability(t *testing.T) {
	tests := []struct {
		name      string
		predicate glossary.CapabilityPredicate
		enabled   map[glossary.Capability]bool
		want      bool
	}{
		{
			name: "all and any satisfied",
			predicate: glossary.CapabilityPredicate{
				RequiresAll: []glossary.Capability{"history"},
				RequiresAny: []glossary.Capability{"source-search", "code-navigation"},
			},
			enabled: map[glossary.Capability]bool{"history": true, "source-search": true},
			want:    true,
		},
		{
			name: "required all missing",
			predicate: glossary.CapabilityPredicate{
				RequiresAll: []glossary.Capability{"history"},
				RequiresAny: []glossary.Capability{},
			},
			enabled: map[glossary.Capability]bool{},
			want:    false,
		},
		{
			name: "required any missing",
			predicate: glossary.CapabilityPredicate{
				RequiresAll: []glossary.Capability{},
				RequiresAny: []glossary.Capability{"history", "source-search"},
			},
			enabled: map[glossary.Capability]bool{"code-navigation": true},
			want:    false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := glossary.Available(test.predicate, test.enabled); got != test.want {
				t.Fatalf("Available = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRepositoryVerifierRejectsEveryProjectionDrift(t *testing.T) {
	paths := append(genrepo.ProjectionPaths(), genrepo.ManualPath)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			root := prepareRepository(t)
			if err := genrepo.VerifyRepository(root); err != nil {
				t.Fatalf("verify generated repository: %v", err)
			}
			fullPath := filepath.Join(root, filepath.FromSlash(path))
			content, err := os.ReadFile(fullPath)
			if err != nil {
				t.Fatalf("read projection: %v", err)
			}
			if path == genrepo.ManualPath {
				content = bytes.Replace(
					content,
					[]byte("##### Analysis scope & gaps"),
					[]byte("##### Hand-edited scope"),
					1,
				)
			} else {
				content = append(content, []byte("\nhand-edited drift\n")...)
			}
			if err := os.WriteFile(fullPath, content, 0o644); err != nil {
				t.Fatalf("mutate projection: %v", err)
			}
			err = genrepo.VerifyRepository(root)
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("VerifyRepository drift error = %v, want path %s", err, path)
			}
		})
	}
}

func TestCheckedInRepositoryProjectionsAreCurrent(t *testing.T) {
	if err := genrepo.VerifyRepository(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
}

func prepareRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, genrepo.SourcePath, glossary.Source())
	manual := strings.Join([]string{
		"# Manual",
		"",
		glossary.ManualBeginMarker,
		"placeholder",
		glossary.ManualEndMarker,
		"",
		"preserved suffix",
		"",
	}, "\n")
	writeTestFile(t, root, genrepo.ManualPath, []byte(manual))
	if err := genrepo.WriteRepository(root); err != nil {
		t.Fatalf("write generated repository: %v", err)
	}
	return root
}

func writeTestFile(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s directory: %v", relative, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func loadDocument(t *testing.T) glossary.Document {
	t.Helper()
	document, _, _, err := glossary.LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded glossary: %v", err)
	}
	return document
}

func cloneDocument(t *testing.T, value glossary.Document) glossary.Document {
	t.Helper()
	var clone glossary.Document
	if err := json.Unmarshal(mustJSON(t, value), &clone); err != nil {
		t.Fatalf("clone glossary: %v", err)
	}
	return clone
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return content
}

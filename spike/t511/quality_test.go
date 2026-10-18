package t511

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type population struct {
	ID, Repo, Commit, Module, Version, Proto, Generated, Consumer string
	Bundle                                                        string `json:"evaluation_bundle"`
	BundleDigest                                                  string `json:"evaluation_bundle_digest"`
	BundleBytes                                                   int    `json:"evaluation_bundle_bytes"`
	FieldLineage                                                  string `json:"field_lineage"`
	DeclarationLineage                                            string `json:"declaration_lineage"`
	Positive, Negative, Unsupported                               int
	Files                                                         []inputFile
}

type inputFile struct {
	Path, Digest string
	Bytes        int
}

type registration struct {
	Populations      []population
	AuthorityRecords []struct{ Path, Digest string } `json:"authority_records"`
	Labels           struct {
		Path, Digest       string
		DeclarationsPath   string `json:"independent_declarations_path"`
		DeclarationsDigest string `json:"independent_declarations_digest"`
	}
}

type label struct {
	Population, Path, Token, Label, Object string
	SourceDigest                           string `json:"source_digest"`
	Start                                  int    `json:"start_byte"`
	End                                    int    `json:"end_byte"`
	Line                                   int
}

type declaration struct {
	Population, Path, Object, Name string
	SourceDigest                   string `json:"source_digest"`
	Start                          int    `json:"start_byte"`
	End                            int    `json:"end_byte"`
	Number                         int32  `json:"field_number"`
	JoinSupported                  bool   `json:"join_supported"`
}

type corpus struct {
	population
	files   map[string]string
	current string
	reads   int
	bytes   int
}

func (c *corpus) RepoName() string { return c.Repo }
func (c *corpus) Commit() string   { return c.population.Commit }
func (c *corpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	paths := make([]string, 0, len(c.files))
	for name := range c.files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
func (c *corpus) Read(ctx context.Context, name string) (sdk.Blob, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Blob{}, err
	}
	content, ok := c.files[name]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("missing frozen source %q", name)
	}
	c.current = name
	c.reads++
	c.bytes += len(content)
	return sdk.Blob{Content: content, Digest: digest([]byte(content))}, nil
}
func (c *corpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	blob, err := c.Read(ctx, "index.scip")
	c.current = "" // typed input cannot support a source citation
	return sdk.SCIPInput{Path: "index.scip", Blob: blob, Present: err == nil}, err
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readJSON(t *testing.T, name string, value any) []byte {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, value); err != nil {
		t.Fatal(err)
	}
	return content
}

func loadFrozen(t *testing.T) (registration, []label, []declaration, []byte) {
	t.Helper()
	var reg registration
	prereg := readJSON(t, "preregistration.json", &reg)
	for _, record := range reg.AuthorityRecords {
		content, err := os.ReadFile(record.Path)
		if err != nil {
			t.Fatal(err)
		}
		if digest(content) != record.Digest {
			t.Fatalf("authority record drift: %s", record.Path)
		}
	}
	content, err := os.ReadFile(reg.Labels.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(content); got != reg.Labels.Digest {
		t.Fatalf("label drift: %s", got)
	}
	var labels []label
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		var row label
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	var declarations []declaration
	content = readJSON(t, reg.Labels.DeclarationsPath, &declarations)
	if digest(content) != reg.Labels.DeclarationsDigest {
		t.Fatal("declaration oracle drift")
	}
	return reg, labels, declarations, prereg
}

func frozenCorpus(t *testing.T, p population) *corpus {
	t.Helper()
	if len(p.Commit) != 40 {
		t.Fatal("evaluation revision must be an exact Git commit")
	}
	if _, err := hex.DecodeString(p.Commit); err != nil {
		t.Fatal(err)
	}
	if p.Repo != "evaluation.local/t511-"+p.ID {
		t.Fatal("evaluation commits must not impersonate upstream history")
	}
	bundle, err := os.ReadFile(p.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle) != p.BundleBytes || digest(bundle) != p.BundleDigest {
		t.Fatal("evaluation source/index bundle drift")
	}
	c := &corpus{population: p, files: map[string]string{}}
	for _, file := range p.Files {
		content, err := os.ReadFile(filepath.Join("testdata", p.ID, file.Path))
		if err != nil {
			t.Fatal(err)
		}
		if len(content) != file.Bytes || digest(content) != file.Digest {
			t.Fatalf("input drift: %s/%s", p.ID, file.Path)
		}
		c.files[file.Path] = string(content)
	}
	var actual []string
	err = filepath.WalkDir(filepath.Join("testdata", p.ID), func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("nonregular input %s", name)
		}
		rel, err := filepath.Rel(filepath.Join("testdata", p.ID), name)
		if err != nil {
			return err
		}
		actual = append(actual, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(c.files) {
		t.Fatal("unregistered input file")
	}
	for _, name := range actual {
		if _, ok := c.files[name]; !ok {
			t.Fatalf("unregistered input %s", name)
		}
	}
	return c
}

func spanKey(path string, start, end int) string { return fmt.Sprintf("%s:%d-%d", path, start, end) }

// This source-only census uses no SCIP or extractor result. Rechecking it before
// scoring prevents a missing negative site from silently leaving the denominator.
func checkSourceCensus(t *testing.T, c *corpus, labels []label) map[string]label {
	t.Helper()
	want := map[string]label{}
	counts := map[string]int{}
	for _, row := range labels {
		if row.Population != c.ID {
			continue
		}
		content := c.files[row.Path]
		if row.Path != c.Consumer || digest([]byte(content)) != row.SourceDigest || row.Start < 0 || row.End > len(content) || row.Start >= row.End || content[row.Start:row.End] != row.Token {
			t.Fatalf("bad source label: %+v", row)
		}
		if 1+strings.Count(content[:row.Start], "\n") != row.Line {
			t.Fatal("label line drift")
		}
		key := spanKey(row.Path, row.Start, row.End)
		if _, ok := want[key]; ok {
			t.Fatal("duplicate source label")
		}
		want[key] = row
		counts[row.Label]++
	}
	if counts["positive"] != c.Positive || counts["negative"] != c.Negative || counts["unsupported"] != c.Unsupported || len(want) != c.Positive+c.Negative+c.Unsupported {
		t.Fatalf("label counts: %v", counts)
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, c.Consumer, c.files[c.Consumer], 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	add := func(id *ast.Ident) {
		key := spanKey(c.Consumer, set.Position(id.Pos()).Offset, set.Position(id.End()).Offset)
		if _, ok := want[key]; !ok {
			t.Fatalf("unlabelled source site %s", key)
		}
		seen[key] = true
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			add(n.Sel)
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok {
				add(id)
			}
		}
		return true
	})
	if len(seen) != len(want) {
		t.Fatal("label not present in source-only census")
	}
	return want
}

func extract(t *testing.T, c *corpus, reader sdk.Extractor) ([]sdk.Fact, sdk.Coverage) {
	t.Helper()
	var facts []sdk.Fact
	coverage, err := reader.Extract(context.Background(), c, func(fact sdk.Fact) error {
		content, ok := c.files[fact.Path]
		if !ok || c.current != fact.Path || fact.Atom.BlobDigest != digest([]byte(content)) || fact.Atom.StartByte < 0 || fact.Atom.StartByte >= fact.Atom.EndByte || fact.Atom.EndByte > len(content) {
			return fmt.Errorf("invalid streaming source citation: %+v", fact)
		}
		if fact.StartLine != 1+strings.Count(content[:fact.Atom.StartByte], "\n") || fact.EndLine != 1+strings.Count(content[:fact.Atom.EndByte-1], "\n") {
			return fmt.Errorf("invalid line citation")
		}
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.Failures) > 0 || coverage.UnresolvedCount != 0 {
		t.Fatalf("coverage: %+v", coverage)
	}
	return facts, coverage
}

type result struct {
	Population                               string `json:"population"`
	Positive, Negative, Unsupported, Matched int
	GeneratedEmissions                       int            `json:"generated_emissions_outside_quality_population"`
	FieldFacts                               int            `json:"field_facts_all_paths"`
	Reads                                    int            `json:"corpus_reads"`
	Bytes                                    int            `json:"corpus_read_bytes"`
	WallMicros                               int64          `json:"wall_microseconds"`
	RoleCounts                               map[string]int `json:"raw_scip_role_classifications"`
	Witnesses                                []witness      `json:"exact_join_witnesses"`
}

type witness struct {
	Object                                                              string `json:"object"`
	DeclarationLineage                                                  string `json:"declaration_lineage"`
	FieldLineage                                                        string `json:"field_lineage"`
	Repo, Commit                                                        string
	ProtoPath, ProtoDigest, GeneratedPath, GeneratedDigest, IndexDigest string
	DeclarationStart, DeclarationEnd, DefinitionStart, DefinitionEnd    int
	Symbol                                                              string `json:"scip_symbol"`
}

func TestFrozenFieldQuality(t *testing.T) {
	reg, labels, declarations, prereg := loadFrozen(t)
	var results []result
	for _, p := range reg.Populations {
		t.Run(p.ID, func(t *testing.T) {
			c := frozenCorpus(t, p)
			gold := checkSourceCensus(t, c, labels)
			checkDescriptor(t, c, declarations)
			checkDeclarations(t, c, declarations)
			index := &scip.Index{}
			if err := proto.Unmarshal([]byte(c.files["index.scip"]), index); err != nil {
				t.Fatal(err)
			}
			if index.GetMetadata().GetToolInfo().GetName() != "scip-go" || index.GetMetadata().GetToolInfo().GetVersion() != "0.2.7" || index.GetMetadata().GetTextDocumentEncoding() != scip.TextEncoding_UTF8 {
				t.Fatal("unsupported frozen SCIP metadata")
			}
			start := time.Now()
			c.reads, c.bytes = 0, 0
			facts, coverage := extract(t, c, scipfield.New())
			if !reflect.DeepEqual(coverage.Protocols, []string{"protobuf-generated-accessor-v1", "scip", "scip-package-lineage-v1"}) {
				t.Fatalf("protocols: %v", coverage.Protocols)
			}
			r := result{Population: p.ID, Positive: p.Positive, Negative: p.Negative, Unsupported: p.Unsupported, FieldFacts: len(facts), Reads: c.reads, Bytes: c.bytes, WallMicros: time.Since(start).Microseconds(), RoleCounts: map[string]int{}}
			seen := map[string]bool{}
			witnesses := map[string]witness{}
			for _, fact := range facts {
				if fact.Path != p.Consumer {
					if fact.Path != p.Generated {
						t.Fatalf("unscored unexpected path %s", fact.Path)
					}
					r.GeneratedEmissions++
					continue
				}
				key := spanKey(fact.Path, fact.Atom.StartByte, fact.Atom.EndByte)
				row, ok := gold[key]
				if !ok || row.Label != "positive" || seen[key] {
					t.Fatalf("extra/negative/unsupported/duplicate emission %s: %+v", key, fact.Assertion)
				}
				seen[key] = true
				if fact.Assertion.Object != row.Object || fact.Assertion.Predicate != "REFERENCES_PROTO_FIELD" || fact.Assertion.Tier != "exact" || fact.Assertion.CodeRole != "production" || fact.Assertion.Lineage != p.FieldLineage || fact.Atom.SchemaVersion != "t13-v1" || fact.Atom.RuleID != "scip-proto-field-reference-v1" {
					t.Fatalf("identity/schema mismatch %s: %+v", key, fact)
				}
				var detail struct {
					Schema, Name, Classification string
					DependencyVersion            string `json:"dependency_version"`
				}
				if err := json.Unmarshal([]byte(fact.Assertion.Detail), &detail); err != nil {
					t.Fatal(err)
				}
				decl := findDeclaration(t, declarations, p.ID, row.Object)
				if detail.Schema != "proto-field-reference-detail-v1" || detail.Name != decl.Name || detail.DependencyVersion != p.Version {
					t.Fatalf("detail mismatch: %+v", detail)
				}
				w, classification := exactWitness(t, c, index, row, decl)
				if detail.Classification != classification {
					t.Fatalf("raw SCIP role mismatch: %s vs %s", detail.Classification, classification)
				}
				witnesses[w.Symbol] = w
				r.RoleCounts[classification]++
				r.Matched++
			}
			for key, row := range gold {
				if row.Label == "positive" && !seen[key] {
					t.Errorf("missed positive %s -> %s", key, row.Object)
				}
			}
			if r.Matched != p.Positive {
				t.Fatalf("matched %d / %d", r.Matched, p.Positive)
			}
			for _, w := range witnesses {
				r.Witnesses = append(r.Witnesses, w)
			}
			sort.Slice(r.Witnesses, func(i, j int) bool { return r.Witnesses[i].Symbol < r.Witnesses[j].Symbol })
			results = append(results, r)
			t.Logf("%s: %d/%d positive, zero emissions at %d negative + %d unsupported sites; %d generated emissions; %d reads/%d bytes", p.ID, r.Matched, p.Positive, p.Negative, p.Unsupported, r.GeneratedEmissions, r.Reads, r.Bytes)
		})
	}
	if t.Failed() {
		return
	}
	if path := os.Getenv("T511_RESULT_PATH"); path != "" {
		report := struct {
			Schema, RecordDate, PreregistrationDigest string
			Results                                   []result
		}{"t511-finite-field-quality-v1", "2026-10-18", digest(prereg), results}
		content, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(content, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func findDeclaration(t *testing.T, all []declaration, population, object string) declaration {
	t.Helper()
	var found []declaration
	for _, d := range all {
		if d.Population == population && d.Object == object {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		t.Fatalf("ambiguous/missing independent declaration %s/%s", population, object)
	}
	return found[0]
}

func checkDeclarations(t *testing.T, c *corpus, all []declaration) {
	t.Helper()
	want := map[string]declaration{}
	for _, d := range all {
		if d.Population == c.ID {
			want[d.Object] = d
		}
	}
	facts, _ := extract(t, c, protodecl.New())
	seen := map[string]bool{}
	for _, fact := range facts {
		if fact.Assertion.Predicate != "DECLARES_FIELD" {
			continue
		}
		d, ok := want[fact.Assertion.Object]
		if !ok || seen[d.Object] || fact.Path != d.Path || fact.Atom.StartByte != d.Start || fact.Atom.EndByte != d.End || fact.Atom.BlobDigest != d.SourceDigest || fact.Assertion.Lineage != c.DeclarationLineage {
			t.Fatalf("independent declaration disagreement: %+v", fact)
		}
		seen[d.Object] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("declaration census: %d/%d", len(seen), len(want))
	}
	if c.FieldLineage == c.DeclarationLineage || !strings.HasPrefix(c.FieldLineage, "contract_scip_package_v1_") || !strings.HasPrefix(c.DeclarationLineage, "provisional_repo_path_v1_") {
		t.Fatal("lineage families must remain distinct")
	}
}

func checkDescriptor(t *testing.T, c *corpus, all []declaration) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), c.Generated, c.files[c.Generated], 0)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	var literal func(ast.Expr) string
	literal = func(expr ast.Expr) string {
		switch n := expr.(type) {
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				t.Fatal("descriptor is not literal string")
			}
			s, err := strconv.Unquote(n.Value)
			if err != nil {
				t.Fatal(err)
			}
			return s
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				t.Fatal("unsupported descriptor expression")
			}
			return literal(n.X) + literal(n.Y)
		default:
			t.Fatalf("unsupported descriptor expression %T", expr)
			return ""
		}
	}
	for _, d := range file.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			v := s.(*ast.ValueSpec)
			if len(v.Names) == 1 && strings.HasSuffix(v.Names[0].Name, "_rawDesc") {
				if raw != "" {
					t.Fatal("multiple raw descriptors")
				}
				raw = literal(v.Values[0])
			}
		}
	}
	var desc descriptorpb.FileDescriptorProto
	if raw == "" {
		t.Fatal("missing generated raw descriptor")
	}
	if err := proto.Unmarshal([]byte(raw), &desc); err != nil {
		t.Fatal(err)
	}
	if desc.GetName() != c.Proto || desc.GetSyntax() != "proto3" {
		t.Fatal("generated descriptor source/syntax mismatch")
	}
	seen := map[string]bool{}
	var walk func(string, []*descriptorpb.DescriptorProto)
	walk = func(prefix string, messages []*descriptorpb.DescriptorProto) {
		for _, m := range messages {
			if m.GetOptions().GetMapEntry() {
				continue
			}
			name := prefix + "." + m.GetName()
			for _, f := range m.GetField() {
				object := fmt.Sprintf("%s#%d", name, f.GetNumber())
				d := findDeclaration(t, all, c.ID, object)
				if seen[object] || d.Name != f.GetName() || d.Number != f.GetNumber() {
					t.Fatalf("descriptor/source disagreement %s", object)
				}
				seen[object] = true
			}
			walk(name, m.GetNestedType())
		}
	}
	walk(desc.GetPackage(), desc.GetMessageType())
	want := 0
	for _, d := range all {
		if d.Population == c.ID {
			want++
		}
	}
	if len(seen) != want {
		t.Fatal("descriptor/source field census mismatch")
	}
}

func byteRange(t *testing.T, content string, ranges []int32) (int, int) {
	t.Helper()
	if len(ranges) != 3 && len(ranges) != 4 {
		t.Fatal("invalid frozen SCIP range")
	}
	line, col, endLine, endCol := int(ranges[0]), int(ranges[1]), int(ranges[0]), int(ranges[2])
	if len(ranges) == 4 {
		endLine, endCol = int(ranges[2]), int(ranges[3])
	}
	lines := strings.SplitAfter(content, "\n")
	if line < 0 || line >= len(lines) || endLine < line || endLine >= len(lines) || col < 0 || col > len(strings.TrimSuffix(lines[line], "\n")) || endCol < 0 || endCol > len(strings.TrimSuffix(lines[endLine], "\n")) {
		t.Fatal("invalid frozen SCIP byte range")
	}
	start, end := col, endCol
	for _, s := range lines[:line] {
		start += len(s)
	}
	for _, s := range lines[:endLine] {
		end += len(s)
	}
	if start >= end {
		t.Fatal("empty/reversed frozen SCIP range")
	}
	return start, end
}

func exactWitness(t *testing.T, c *corpus, index *scip.Index, row label, decl declaration) (witness, string) {
	t.Helper()
	if !decl.JoinSupported || decl.Path != c.Proto || decl.SourceDigest != digest([]byte(c.files[c.Proto])) {
		t.Fatal("unsupported or foreign declaration witness")
	}
	if !strings.Contains(c.files[c.Generated], "// source: "+c.Proto+"\n") {
		t.Fatal("missing exact generated source comment")
	}
	var matches []*scip.Occurrence
	for _, d := range index.GetDocuments() {
		if d.GetRelativePath() == row.Path {
			for _, o := range d.GetOccurrences() {
				a, b := byteRange(t, c.files[row.Path], o.GetRange())
				if a == row.Start && b == row.End && o.GetSymbolRoles()&int32(scip.SymbolRole_Definition|scip.SymbolRole_ForwardDefinition) == 0 {
					matches = append(matches, o)
				}
			}
		}
	}
	if len(matches) != 1 {
		t.Fatalf("ambiguous/missing SCIP reference witness %+v", row)
	}
	o := matches[0]
	symbol, err := scip.ParseSymbol(o.GetSymbol())
	if err != nil {
		t.Fatal(err)
	}
	pkg := symbol.GetPackage()
	if symbol.GetScheme() != "scip-go" || pkg.GetManager() != "gomod" || pkg.GetName() != c.Module || pkg.GetVersion() != c.Version {
		t.Fatal("foreign SCIP package witness")
	}
	if got := "contract_scip_package_v1_" + strings.TrimPrefix(digest([]byte("scip-go\x00gomod\x00"+c.Module)), "sha256:"); got != c.FieldLineage {
		t.Fatal("incorrect package family")
	}
	if got := "provisional_repo_path_v1_" + strings.TrimPrefix(digest([]byte(c.Repo+"\x00"+c.Proto)), "sha256:"); got != c.DeclarationLineage {
		t.Fatal("incorrect declaration family")
	}
	var definitions [][2]int
	for _, d := range index.GetDocuments() {
		for _, def := range d.GetOccurrences() {
			if def.GetSymbol() == o.GetSymbol() && def.GetSymbolRoles()&int32(scip.SymbolRole_Definition) != 0 {
				if d.GetRelativePath() != c.Generated {
					t.Fatal("foreign definition path")
				}
				a, b := byteRange(t, c.files[c.Generated], def.GetRange())
				definitions = append(definitions, [2]int{a, b})
			}
		}
	}
	if len(definitions) != 1 {
		t.Fatal("ambiguous/missing exact generated SCIP definition")
	}
	a, b := definitions[0][0], definitions[0][1]
	checkTaggedDefinition(t, c, decl, a, b)
	classification := "unknown"
	for _, r := range []struct {
		role scip.SymbolRole
		name string
	}{{scip.SymbolRole_WriteAccess, "write"}, {scip.SymbolRole_ReadAccess, "read"}, {scip.SymbolRole_Test, "test"}, {scip.SymbolRole_Generated, "generated"}} {
		if o.GetSymbolRoles()&int32(r.role) != 0 {
			classification = r.name
			break
		}
	}
	return witness{Object: row.Object, DeclarationLineage: c.DeclarationLineage, FieldLineage: c.FieldLineage, Repo: c.Repo, Commit: c.population.Commit, ProtoPath: c.Proto, ProtoDigest: decl.SourceDigest, GeneratedPath: c.Generated, GeneratedDigest: digest([]byte(c.files[c.Generated])), IndexDigest: digest([]byte(c.files["index.scip"])), DeclarationStart: decl.Start, DeclarationEnd: decl.End, DefinitionStart: a, DefinitionEnd: b, Symbol: o.GetSymbol()}, classification
}

func checkTaggedDefinition(t *testing.T, c *corpus, decl declaration, start, end int) {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, c.Generated, c.files[c.Generated], 0)
	if err != nil {
		t.Fatal(err)
	}
	type field struct {
		receiver, name string
		number         int32
		protoName      string
	}
	var fields []field
	var receiver, identifier string
	for _, d := range file.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.TYPE {
			for _, s := range g.Specs {
				ts := s.(*ast.TypeSpec)
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, f := range st.Fields.List {
					if len(f.Names) != 1 || f.Tag == nil {
						continue
					}
					tag, err := strconv.Unquote(f.Tag.Value)
					if err != nil {
						t.Fatal(err)
					}
					parts := strings.Split(reflect.StructTag(tag).Get("protobuf"), ",")
					if len(parts) < 2 {
						continue
					}
					number, err := strconv.ParseInt(parts[1], 10, 32)
					if err != nil {
						continue
					}
					name := ""
					for _, p := range parts {
						if strings.HasPrefix(p, "name=") {
							name = strings.TrimPrefix(p, "name=")
						}
					}
					fields = append(fields, field{ts.Name.Name, f.Names[0].Name, int32(number), name})
					if set.Position(f.Names[0].Pos()).Offset == start && set.Position(f.Names[0].End()).Offset == end {
						receiver, identifier = ts.Name.Name, f.Names[0].Name
					}
				}
			}
		}
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil && len(fn.Recv.List) == 1 && set.Position(fn.Name.Pos()).Offset == start && set.Position(fn.Name.End()).Offset == end {
			ptr, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				t.Fatal("unsupported generated receiver")
			}
			typ, ok := ptr.X.(*ast.Ident)
			if !ok {
				t.Fatal("unsupported generated receiver")
			}
			receiver, identifier = typ.Name, strings.TrimPrefix(fn.Name.Name, "Get")
			if identifier == fn.Name.Name {
				t.Fatal("not a generated getter")
			}
		}
	}
	// Source nested message spelling maps to the pinned protoc Go Open API type.
	message := strings.Split(decl.Object, "#")[0]
	packagePrefix := "grpc.go.profiling.v1alpha."
	if c.ID == "neutral" {
		packagePrefix = "t511.v1."
	}
	wantReceiver := strings.ReplaceAll(strings.TrimPrefix(message, packagePrefix), ".", "_")
	matches := 0
	for _, f := range fields {
		if f.receiver == receiver && f.name == identifier && f.number == decl.Number && f.protoName == decl.Name && receiver == wantReceiver {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("generated definition does not uniquely witness source tag: %s/%s/%s", decl.Object, receiver, identifier)
	}
}

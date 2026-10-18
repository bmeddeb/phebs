package t521

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type inputFile struct {
	Path, Location, Digest string
	Bytes                  int
}

type population struct {
	ID                 string
	Repo               string `json:"evaluation_repository"`
	Commit, Bundle     string
	BundleDigest       string `json:"bundle_digest"`
	BundleBytes        int    `json:"bundle_bytes"`
	Package            string `json:"scip_package"`
	Version            string `json:"module_version"`
	Lineage            string `json:"field_lineage"`
	Application        []string
	Generated          []string `json:"generated_paths"`
	Positive, Negative int
	Unsupported        int
	Files              []inputFile
}

type registration struct {
	Populations []population
	Authority   []struct{ Path, Digest string } `json:"authority_records"`
	Labels      struct{ Path, Digest string }
}

type label struct {
	Population, Path, Text, Expected, Object string
	SourceDigest                             string `json:"source_digest"`
	Start                                    int    `json:"start_byte"`
	End                                      int    `json:"end_byte"`
	Line                                     int
}

type fieldWitness struct {
	Population      string
	GeneratedPath   string `json:"generated_path"`
	Message         string `json:"generated_message"`
	GoName          string `json:"go_name"`
	ThriftName      string `json:"thrift_name"`
	GeneratedSpan   [2]int `json:"generated_identifier_span"`
	GeneratedDigest string `json:"generated_source_digest"`
	IDLPopulation   string `json:"idl_population"`
	IDLPath         string `json:"idl_path"`
	IDLSpan         [2]int `json:"idl_witness_span"`
	IDLDigest       string `json:"idl_source_digest"`
	Number          int    `json:"field_number"`
}

type corpus struct {
	population
	files   map[string]string
	digests map[string]string
	lines   map[string][]int
	current string
	reads   int
	bytes   int
}

func (c *corpus) RepoName() string { return c.Repo }
func (c *corpus) Commit() string   { return c.population.Commit }
func (c *corpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	paths := make([]string, 0, len(c.files))
	for path := range c.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}
func (c *corpus) Read(ctx context.Context, path string) (sdk.Blob, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Blob{}, err
	}
	content, ok := c.files[path]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("unregistered source %q", path)
	}
	c.current = path
	c.reads++
	c.bytes += len(content)
	return sdk.Blob{Content: content, Digest: c.digests[path]}, nil
}
func (c *corpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	blob, err := c.Read(ctx, "index.scip")
	c.current = "" // typed bytes cannot support a source citation
	return sdk.SCIPInput{Path: "index.scip", Blob: blob, Present: err == nil}, err
}
func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func readJSON(path string, out any) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, out)
	}
	return raw, err
}
func spanKey(path string, start, end int) string {
	return path + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
}
func lineNumber(starts []int, offset int) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i] > offset })
}

func loadFrozen() (registration, []label, []fieldWitness, string, error) {
	var reg registration
	raw, err := readJSON("final-preregistration.json", &reg)
	if err != nil {
		return reg, nil, nil, "", err
	}
	for _, record := range reg.Authority {
		content, err := os.ReadFile(record.Path)
		if err != nil {
			return reg, nil, nil, "", fmt.Errorf("authority drift %s: %w", record.Path, err)
		}
		if digest(content) != record.Digest {
			return reg, nil, nil, "", fmt.Errorf("authority digest drift %s", record.Path)
		}
	}
	content, err := os.ReadFile(reg.Labels.Path)
	if err != nil || digest(content) != reg.Labels.Digest {
		return reg, nil, nil, "", errors.New("frozen source-label drift")
	}
	var labels []label
	scan := bufio.NewScanner(strings.NewReader(string(content)))
	for scan.Scan() {
		var row label
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			return reg, nil, nil, "", err
		}
		labels = append(labels, row)
	}
	if err := scan.Err(); err != nil {
		return reg, nil, nil, "", err
	}
	var witnesses struct{ Fields []fieldWitness }
	if _, err := readJSON("field-witnesses.json", &witnesses); err != nil {
		return reg, nil, nil, "", err
	}
	var freeze struct {
		Files []struct {
			Path, Digest string
			Bytes        int
		}
	}
	if _, err := readJSON("source-freeze.json", &freeze); err != nil {
		return reg, nil, nil, "", err
	}
	for _, file := range freeze.Files {
		content, err := os.ReadFile(file.Path)
		if err != nil || len(content) != file.Bytes || digest(content) != file.Digest {
			return reg, nil, nil, "", fmt.Errorf("source freeze drift %s", file.Path)
		}
	}
	return reg, labels, witnesses.Fields, digest(raw), nil
}

func loadCorpus(p population) (*corpus, error) {
	commit, err := hex.DecodeString(p.Commit)
	if err != nil || len(commit) != 20 || p.Repo != "evaluation.local/t521-"+p.ID {
		return nil, errors.New("invalid evaluation repository/commit")
	}
	bundle, err := os.ReadFile(p.Bundle)
	if err != nil || len(bundle) != p.BundleBytes || digest(bundle) != p.BundleDigest {
		return nil, errors.New("committed evaluation Git bundle drift")
	}
	c := &corpus{population: p, files: map[string]string{}, digests: map[string]string{}, lines: map[string][]int{}}
	for _, file := range p.Files {
		info, err := os.Lstat(file.Location)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("source is not regular: %s", file.Location)
		}
		raw, err := os.ReadFile(file.Location)
		if err != nil || len(raw) != file.Bytes || digest(raw) != file.Digest {
			return nil, fmt.Errorf("input drift %s", file.Location)
		}
		if _, duplicate := c.files[file.Path]; duplicate {
			return nil, errors.New("duplicate input path")
		}
		c.files[file.Path], c.digests[file.Path] = string(raw), file.Digest
		starts := []int{0}
		for i, b := range raw {
			if b == '\n' {
				starts = append(starts, i+1)
			}
		}
		c.lines[file.Path] = starts
	}
	return c, nil
}

// The standard Go scanner checks the complete frozen identifier census without
// consulting SCIP, bindings or emitted facts. The one reflection string is an
// explicit extra source-only unsupported site rather than a Go identifier.
func sourceCensus(c *corpus, labels []label) (map[string]label, error) {
	gold := map[string]label{}
	counts := map[string]int{}
	for _, row := range labels {
		if row.Population != c.ID {
			continue
		}
		source := c.files[row.Path]
		if !slices.Contains(c.Application, row.Path) || c.digests[row.Path] != row.SourceDigest || row.Start < 0 || row.End <= row.Start || row.End > len(source) || source[row.Start:row.End] != row.Text || lineNumber(c.lines[row.Path], row.Start) != row.Line {
			return nil, fmt.Errorf("frozen source label drift: %+v", row)
		}
		key := spanKey(row.Path, row.Start, row.End)
		if _, duplicate := gold[key]; duplicate {
			return nil, errors.New("duplicate source-label span")
		}
		gold[key] = row
		counts[row.Expected]++
	}
	if counts["positive"] != c.Positive || counts["negative"] != c.Negative || counts["unsupported"] != c.Unsupported || len(gold) != c.Positive+c.Negative+c.Unsupported {
		return nil, fmt.Errorf("label denominator changed: %v", counts)
	}
	seen := map[string]bool{}
	for _, path := range c.Application {
		set := token.NewFileSet()
		file := set.AddFile(path, -1, len(c.files[path]))
		var scan scanner.Scanner
		var scanErr error
		scan.Init(file, []byte(c.files[path]), func(_ token.Position, message string) { scanErr = errors.New(message) }, 0)
		for {
			position, kind, text := scan.Scan()
			if kind == token.EOF {
				break
			}
			if kind == token.IDENT {
				key := spanKey(path, file.Offset(position), file.Offset(position)+len(text))
				if _, ok := gold[key]; !ok {
					return nil, fmt.Errorf("unlabelled source identifier %s", key)
				}
				seen[key] = true
			}
		}
		if scanErr != nil {
			return nil, scanErr
		}
	}
	for key, row := range gold {
		if !seen[key] && !(row.Expected == "unsupported" && row.Text == "Code" && strings.HasSuffix(c.files[row.Path][:row.Start], "FieldByName(\"")) {
			return nil, fmt.Errorf("label is not in source census %s", key)
		}
	}
	return gold, nil
}

func occurrenceSpan(c *corpus, path string, occurrence *scip.Occurrence) (int, int, error) {
	r, ok := occurrence.SourceRange()
	if !ok || r.Validate() != nil {
		return 0, 0, errors.New("invalid SCIP range")
	}
	starts, source := c.lines[path], c.files[path]
	point := func(line, col int) (int, bool) {
		if line < 0 || line >= len(starts) || col < 0 {
			return 0, false
		}
		end := len(source)
		if line+1 < len(starts) {
			end = starts[line+1] - 1
		}
		return starts[line] + col, col <= end-starts[line]
	}
	start, a := point(int(r.Start.Line), int(r.Start.Character))
	end, b := point(int(r.End.Line), int(r.End.Character))
	if !a || !b || start >= end {
		return 0, 0, errors.New("SCIP span outside frozen UTF8 source")
	}
	return start, end, nil
}

type joinWitness struct {
	Object, GeneratedPath, IDLPath, Symbol string
	ThriftName                             string
	DefinitionStart, DefinitionEnd         int
}

func inputWitnesses(c *corpus, all []fieldWitness) ([]joinWitness, error) {
	var index scip.Index
	if err := proto.Unmarshal([]byte(c.files["index.scip"]), &index); err != nil {
		return nil, err
	}
	meta := index.GetMetadata()
	if meta.GetToolInfo().GetName() != "scip-go" || meta.GetToolInfo().GetVersion() != "0.2.7" || meta.GetTextDocumentEncoding() != scip.TextEncoding_UTF8 {
		return nil, errors.New("unadmitted SCIP metadata")
	}
	documents := map[string]*scip.Document{}
	for _, doc := range index.GetDocuments() {
		path := doc.GetRelativePath()
		if _, ok := c.files[path]; !ok || documents[path] != nil || doc.GetPositionEncoding() != 0 && doc.GetPositionEncoding() != scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart {
			return nil, errors.New("unknown, repeated or non-UTF8 SCIP source document")
		}
		documents[path] = doc
	}
	var result []joinWitness
	for _, row := range all {
		if row.Population != c.ID {
			continue
		}
		source := c.files[row.GeneratedPath]
		span := row.GeneratedSpan
		if c.digests[row.GeneratedPath] != row.GeneratedDigest || span[0] < 0 || span[1] <= span[0] || span[1] > len(source) || source[span[0]:span[1]] != row.GoName {
			return nil, errors.New("generated identifier witness drift")
		}
		idl, err := os.ReadFile(filepath.Join("testdata", row.IDLPopulation, row.IDLPath))
		if err != nil || digest(idl) != row.IDLDigest || row.IDLSpan[0] < 0 || row.IDLSpan[1] <= row.IDLSpan[0] || row.IDLSpan[1] > len(idl) {
			return nil, errors.New("IDL witness drift")
		}
		file, err := parser.ParseFile(token.NewFileSet(), row.GeneratedPath, source, parser.PackageClauseOnly)
		if err != nil {
			return nil, err
		}
		object := file.Name.Name + "." + row.Message + "#" + strconv.Itoa(row.Number)
		var symbols []string
		for _, occurrence := range documents[row.GeneratedPath].GetOccurrences() {
			if occurrence.GetSymbolRoles()&int32(scip.SymbolRole_Definition) == 0 {
				continue
			}
			start, end, err := occurrenceSpan(c, row.GeneratedPath, occurrence)
			if err != nil {
				return nil, err
			}
			if start == span[0] && end == span[1] {
				symbols = append(symbols, occurrence.GetSymbol())
			}
		}
		if len(symbols) != 1 {
			return nil, fmt.Errorf("definition witness %s matched %d occurrences", object, len(symbols))
		}
		symbol, err := scip.ParseSymbol(symbols[0])
		if err != nil || symbol.GetScheme() != "scip-go" || symbol.GetPackage().GetManager() != "gomod" || symbol.GetPackage().GetName() != c.Package || symbol.GetPackage().GetVersion() != c.Version {
			return nil, errors.New("definition SCIP package identity mismatch")
		}
		descriptors := symbol.GetDescriptors()
		if len(descriptors) < 2 || descriptors[len(descriptors)-1].GetName() != row.GoName || descriptors[len(descriptors)-2].GetName() != row.Message || descriptors[len(descriptors)-2].GetSuffix() != scip.Descriptor_Type {
			return nil, errors.New("definition SCIP enclosing type/field mismatch")
		}
		result = append(result, joinWitness{Object: object, GeneratedPath: row.GeneratedPath, IDLPath: row.IDLPath, Symbol: symbols[0], ThriftName: row.ThriftName, DefinitionStart: span[0], DefinitionEnd: span[1]})
	}
	return result, nil
}

type mismatch struct{ Span, Expected, Actual string }
type result struct {
	Population                               string
	Commit                                   string
	Positive, Negative, Unsupported, Matched int
	GeneratedEmissions, AllEmissions         int
	Reads, ReadBytes                         int
	WallMicros                               int64
	ApplicationRoles, GeneratedRoles         map[string]int
	Mismatches                               []mismatch
	Missing                                  []string
	Witnesses                                []joinWitness
	Error                                    string `json:",omitempty"`
}

func evaluate(ctx context.Context, p population, labels []label, witnesses []fieldWitness) (r result, err error) {
	r = result{Population: p.ID, Commit: p.Commit, Positive: p.Positive, Negative: p.Negative, Unsupported: p.Unsupported, ApplicationRoles: map[string]int{}, GeneratedRoles: map[string]int{}}
	c, err := loadCorpus(p)
	if err != nil {
		return r, err
	}
	gold, err := sourceCensus(c, labels)
	if err != nil {
		return r, err
	}
	r.Witnesses, err = inputWitnesses(c, witnesses)
	if err != nil {
		return r, err
	}
	reader := thriftfield.New()
	if reader.Domain() != "scip-thrift-field" || reader.Version() != "1.4.0" {
		return r, errors.New("candidate implementation identity changed")
	}
	seen := map[string]bool{}
	names := make(map[string]string, len(r.Witnesses))
	for _, witness := range r.Witnesses {
		names[witness.Object] = witness.ThriftName
	}
	start := time.Now()
	coverage, extractErr := reader.Extract(ctx, c, func(fact sdk.Fact) error {
		r.AllEmissions++
		source := c.files[fact.Path]
		a, b := fact.Atom.StartByte, fact.Atom.EndByte
		if c.current != fact.Path || fact.Atom.BlobDigest != c.digests[fact.Path] || a < 0 || b <= a || b > len(source) || fact.StartLine != lineNumber(c.lines[fact.Path], a) || fact.EndLine != lineNumber(c.lines[fact.Path], b-1) {
			return errors.New("streaming citation is outside current immutable source")
		}
		var detail struct {
			Schema, Name, Classification, Generator string
			SourceBinding                           string `json:"source_binding"`
			Version                                 string `json:"dependency_version"`
		}
		if err := json.Unmarshal([]byte(fact.Assertion.Detail), &detail); err != nil {
			return err
		}
		if fact.Atom.SchemaVersion != "t22-v1" || fact.Atom.RuleID != "scip-thrift-field-reference-v1" || fact.Assertion.Predicate != "REFERENCES_THRIFT_FIELD" || fact.Assertion.Tier != "derived" || fact.Assertion.Lineage != c.Lineage || fact.Assertion.Subject != spanKey(fact.Path, a, b) || detail.Schema != "thrift-field-reference-detail-v1" || detail.Generator != "apache" || detail.SourceBinding != "none" || detail.Version != c.Version || !slices.Contains([]string{"write", "read", "test", "generated", "unknown"}, detail.Classification) {
			return errors.New("emitted field schema/Apache tier/identity changed")
		}
		if name, witnessed := names[fact.Assertion.Object]; witnessed && detail.Name != name {
			return errors.New("emitted field name differs from source/tag witness")
		}
		if slices.Contains(c.Generated, fact.Path) {
			r.GeneratedEmissions++
			r.GeneratedRoles[detail.Classification]++
			return nil
		}
		r.ApplicationRoles[detail.Classification]++
		key := spanKey(fact.Path, a, b)
		row, ok := gold[key]
		if !ok || row.Expected != "positive" || row.Object != fact.Assertion.Object || seen[key] {
			r.Mismatches = append(r.Mismatches, mismatch{Span: key, Expected: row.Expected + " " + row.Object, Actual: fact.Assertion.Object})
			return nil
		}
		seen[key] = true
		r.Matched++
		return nil
	})
	r.WallMicros, r.Reads, r.ReadBytes = time.Since(start).Microseconds(), c.reads, c.bytes
	if extractErr != nil {
		return r, extractErr
	}
	if len(coverage.Failures) != 0 || coverage.UnresolvedCount != 0 || coverage.ExcludedSCIPDocuments != 0 || coverage.ExcludedSCIPDefinitions != 0 || coverage.ExcludedSCIPOccurrences != 0 || !slices.Equal(coverage.Protocols, []string{"apache-thrift-tag-v1", "scip", "scip-package-lineage-v1", "thriftrw-module-digest-v1"}) {
		return r, errors.New("candidate returned a coverage failure/gap/exclusion")
	}
	for key, row := range gold {
		if row.Expected == "positive" && !seen[key] {
			r.Missing = append(r.Missing, key+" "+row.Object)
		}
	}
	sort.Strings(r.Missing)
	if len(r.Mismatches) != 0 || len(r.Missing) != 0 || r.Matched != c.Positive {
		return r, errors.New("frozen field-quality threshold failed")
	}
	return r, nil
}

func TestFrozenApacheFieldQuality(t *testing.T) {
	reg, labels, witnesses, preregDigest, err := loadFrozen()
	if err != nil {
		t.Fatal(err)
	}
	var results []result
	for _, p := range reg.Populations {
		r, runErr := evaluate(t.Context(), p, labels, witnesses)
		if runErr != nil {
			r.Error = runErr.Error()
		}
		results = append(results, r)
		if runErr != nil {
			err = runErr
			break
		}
	}
	if os.Getenv("PHEBS_T521_RECORD_RESULT") == "1" {
		record := struct {
			Schema, PreregistrationDigest, CostScope string
			Results                                  []result
		}{"t521-first-field-quality-v1", preregDigest, "Extract plus streaming verification/counters; precomputed input digests and line starts, no child; wall time not isolated CPU", results}
		raw, marshalErr := json.MarshalIndent(record, "", "  ")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		file, openErr := os.OpenFile("first-result.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, writeErr := file.Write(append(raw, '\n'))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(errors.Join(writeErr, closeErr))
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}

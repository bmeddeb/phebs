// Command export offers frozen source-only units to the unchanged Thrift reader.
// Only the bounded stdin plan and stdout/stderr are used; no source filesystem,
// module, application, network or child-process access is implemented.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	preregSHA     = "6e9f39ad56cbba47eb4a5ea74630b3a33f8b747ac6c4db7d1256ccaf84fd51e5"
	maxPlan       = 32 << 20
	maxUnitSource = 8 << 20
	maxPassSource = 16 << 20
	maxUnitOutput = 8 << 20
	maxPassOutput = 16 << 20
	maxUnitFacts  = 4096
	maxPassFacts  = 8192
)

type inputFile struct {
	Path    string `json:"path"`
	Origin  string `json:"origin"`
	Content []byte `json:"content"`
	SHA256  string `json:"sha256"`
	Context bool   `json:"context"`
}

type unit struct {
	ID         string      `json:"id"`
	Repository string      `json:"repository"`
	Commit     string      `json:"commit"`
	Files      []inputFile `json:"files"`
}

type plan struct {
	Schema                string `json:"schema"`
	PreregistrationSHA256 string `json:"preregistration_sha256"`
	Units                 []unit `json:"units"`
}

type fileReceipt struct {
	Path    string `json:"path"`
	Origin  string `json:"origin"`
	SHA256  string `json:"sha256"`
	Bytes   int    `json:"bytes"`
	Context bool   `json:"context"`
	Reads   int    `json:"reads"`
}

type record struct {
	Raw               sdk.Fact `json:"raw_fact"`
	SourceSliceSHA256 string   `json:"source_slice_sha256"`
	ValidationError   string   `json:"validation_error"`
}

type unitResult struct {
	Schema                     string              `json:"schema"`
	ID                         string              `json:"id"`
	Repository                 string              `json:"repository"`
	Commit                     string              `json:"commit"`
	Files                      []fileReceipt       `json:"files"`
	Facts                      []record            `json:"facts"`
	Coverage                   sdk.Coverage        `json:"coverage"`
	SourceOutcomes             []sdk.SourceOutcome `json:"source_outcomes"`
	SourceOutcomeQualification string              `json:"source_outcome_qualification"`
	ReadBytes                  int                 `json:"read_bytes"`
	Error                      string              `json:"error"`
}

type footer struct {
	Schema                string `json:"schema"`
	PreregistrationSHA256 string `json:"preregistration_sha256"`
	Units                 int    `json:"units"`
	Facts                 int    `json:"facts"`
	Domain                string `json:"domain"`
	Version               string `json:"version"`
	GoVersion             string `json:"go_version"`
	GOOS                  string `json:"goos"`
	GOARCH                string `json:"goarch"`
}

type corpus struct {
	u          unit
	files      map[string]inputFile
	reads      map[string]int
	last       string
	readBytes  int
	byteBudget int
}

var _ sdk.Corpus = (*corpus)(nil)

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func canonicalPath(value string) bool {
	return value != "" && len(value) <= 512 && value == path.Clean(value) &&
		!path.IsAbs(value) && value != "." && !strings.HasPrefix(value, "../") &&
		!strings.ContainsAny(value, "\\\x00\r\n")
}

func newCorpus(u unit) (*corpus, error) {
	if u.ID == "" || len(u.ID) > 96 || u.Repository == "" || len(u.Repository) > 256 ||
		u.Commit == "" || len(u.Commit) > 128 || len(u.Files) > 64 {
		return nil, errors.New("unit metadata bound")
	}
	c := &corpus{u: u, files: make(map[string]inputFile, len(u.Files)), reads: make(map[string]int, len(u.Files))}
	for _, f := range u.Files {
		if !canonicalPath(f.Path) || !canonicalPath(f.Origin) || !strings.HasSuffix(f.Path, ".go") {
			return nil, errors.New("file path admission")
		}
		if _, exists := c.files[f.Path]; exists {
			return nil, errors.New("duplicate logical file")
		}
		if len(f.Content) > (4<<20)+1 || digest(f.Content) != f.SHA256 {
			return nil, errors.New("file size or digest admission")
		}
		c.byteBudget += len(f.Content)
		if c.byteBudget > maxUnitSource {
			return nil, errors.New("unit source bound")
		}
		c.files[f.Path] = f
	}
	return c, nil
}

func (c *corpus) RepoName() string { return c.u.Repository }
func (c *corpus) Commit() string   { return c.u.Commit }

func (c *corpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	for _, f := range c.u.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(f.Path); err != nil {
			return err
		}
	}
	return nil
}

func (c *corpus) Read(ctx context.Context, name string) (sdk.Blob, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Blob{}, err
	}
	f, exists := c.files[name]
	if !exists || c.reads[name] >= 2 || c.readBytes+len(f.Content) > 2*c.byteBudget {
		return sdk.Blob{}, errors.New("corpus read admission")
	}
	c.last = name
	c.reads[name]++
	c.readBytes += len(f.Content)
	return sdk.Blob{Content: string(f.Content), Digest: "sha256:" + f.SHA256}, nil
}

func validateFact(c *corpus, f sdk.Fact) (record, error) {
	file, exists := c.files[f.Path]
	a := f.Atom
	if !exists || c.last != f.Path || a.BlobDigest != "sha256:"+file.SHA256 ||
		a.StartByte < 0 || a.EndByte <= a.StartByte || a.EndByte > len(file.Content) {
		return record{}, errors.New("fact most-recent source/digest/span fence")
	}
	end := a.EndByte
	if f.Assertion.Predicate == "THRIFT_EXTRACTION_GAP" {
		end--
	}
	if f.StartLine != 1+bytes.Count(file.Content[:a.StartByte], []byte{'\n'}) ||
		f.EndLine != 1+bytes.Count(file.Content[:end], []byte{'\n'}) {
		return record{}, errors.New("fact source line fence")
	}
	if a.SchemaVersion != "t19-v1" || a.RuleID == "" || a.FactFingerprint == "" ||
		a.AdapterConfigDigest != "sha256:"+digest(nil) {
		return record{}, errors.New("fact atom metadata fence")
	}
	return record{Raw: f, SourceSliceSHA256: digest(file.Content[a.StartByte:a.EndByte])}, nil
}

// JSON escaping expands each byte by at most six. The fixed overhead bounds
// all scalar keys/integers and the source-slice digest in this closed record.
func recordBound(f sdk.Fact) int {
	a, s := f.Atom, f.Assertion
	return 1024 + 6*(len(f.Path)+len(a.SchemaVersion)+len(a.BlobDigest)+len(a.RuleID)+
		len(a.AdapterConfigDigest)+len(a.FactFingerprint)+len(s.Predicate)+len(s.Subject)+
		len(s.Object)+len(s.Lineage)+len(s.Tier)+len(s.CodeRole)+len(s.Detail))
}

func runUnit(ctx context.Context, u unit, remainingFacts int) (unitResult, error) {
	result := unitResult{Schema: "t582-unit-output-v1", ID: u.ID, Repository: u.Repository,
		Commit: u.Commit, Facts: make([]record, 0), SourceOutcomes: make([]sdk.SourceOutcome, 0),
		SourceOutcomeQualification: "Only actual SDK reports; zero reports does not assert per-file analyzed coverage."}
	c, err := newCorpus(u)
	if err != nil {
		return result, err
	}
	// Bound record retention before encoding. 2 MiB reserves file/coverage
	// metadata (64 bounded paths, 128 bounded source outcomes, fixed diagnostics).
	estimatedBytes := 2 << 20
	ctx = sdk.WithDiagnosticCounters(ctx)
	ctx = sdk.WithSourceOutcomes(ctx, func(outcome sdk.SourceOutcome) error {
		if len(result.SourceOutcomes) >= 128 || len(outcome.Path)+len(outcome.State)+len(outcome.Reason)+len(outcome.BlobDigest) > 1024 {
			return errors.New("source outcome bound")
		}
		result.SourceOutcomes = append(result.SourceOutcomes, outcome)
		return nil
	})
	reader := thriftgo.New() // New reader and catalog for every unit.
	result.Coverage, err = reader.Extract(ctx, c, func(f sdk.Fact) error {
		bound := recordBound(f)
		if len(result.Facts) >= maxUnitFacts || len(result.Facts) >= remainingFacts ||
			bound > maxUnitOutput-estimatedBytes {
			return errors.New("assertion/output retention bound")
		}
		value, validationErr := validateFact(c, f)
		if validationErr != nil {
			value = record{Raw: f, ValidationError: validationErr.Error()}
		}
		estimatedBytes += bound
		result.Facts = append(result.Facts, value)
		return validationErr
	})
	result.ReadBytes = c.readBytes
	for _, f := range u.Files {
		result.Files = append(result.Files, fileReceipt{Path: f.Path, Origin: f.Origin,
			SHA256: f.SHA256, Bytes: len(f.Content), Context: f.Context, Reads: c.reads[f.Path]})
		if c.reads[f.Path] == 0 && err == nil {
			err = errors.New("mandatory candidate was not read")
		}
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result, err
}

func decodePlan(reader io.Reader) (plan, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxPlan+1))
	if err != nil || len(raw) > maxPlan {
		return plan{}, errors.New("input plan read bound")
	}
	var p plan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return plan{}, fmt.Errorf("decode plan: %w", err)
	}
	var tail any
	if err := decoder.Decode(&tail); !errors.Is(err, io.EOF) {
		return plan{}, errors.New("plan trailing data")
	}
	if p.Schema != "t582-plan-v1" || p.PreregistrationSHA256 != preregSHA || len(p.Units) != 27 {
		return plan{}, errors.New("plan preregistration/unit admission")
	}
	seen := make(map[string]bool, len(p.Units))
	totalBytes := 0
	for _, u := range p.Units {
		if seen[u.ID] {
			return plan{}, errors.New("duplicate unit")
		}
		seen[u.ID] = true
		c, err := newCorpus(u)
		if err != nil {
			return plan{}, err
		}
		totalBytes += c.byteBudget
		if totalBytes > maxPassSource {
			return plan{}, errors.New("pass source admission")
		}
		if !sort.SliceIsSorted(u.Files, func(i, j int) bool { return u.Files[i].Path < u.Files[j].Path }) {
			return plan{}, errors.New("file ordering admission")
		}
	}
	return p, nil
}

func execute(ctx context.Context, reader io.Reader, output io.Writer) error {
	p, err := decodePlan(reader)
	if err != nil {
		return err
	}
	totalFacts, outputBytes := 0, 0
	write := func(value any, bound int) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode output: %w", err)
		}
		if len(raw)+1 > bound || len(raw)+1 > maxPassOutput-outputBytes {
			return errors.New("encoded output bound")
		}
		raw = append(raw, '\n')
		n, err := output.Write(raw)
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		if n != len(raw) {
			return io.ErrShortWrite
		}
		outputBytes += n
		return nil
	}
	for _, u := range p.Units {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, runErr := runUnit(ctx, u, maxPassFacts-totalFacts)
		totalFacts += len(result.Facts)
		if err := write(result, maxUnitOutput); err != nil {
			return err
		}
		if runErr != nil {
			return runErr
		}
	}
	extractor := thriftgo.New()
	return write(footer{Schema: "t582-pass-complete-v1", PreregistrationSHA256: preregSHA,
		Units: len(p.Units), Facts: totalFacts, Domain: extractor.Domain(), Version: extractor.Version(),
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, 4096)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := execute(ctx, os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "t582 exporter stopped:", err)
		os.Exit(1)
	}
}

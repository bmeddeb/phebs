//go:build t581export

// Command export runs the in-tree grpc-consumer extractor over each T58.1
// population unit and writes every emitted fact as JSON Lines: the candidate
// side of the round.
//
// A unit is a whole repository at one pinned commit, because the extractor is
// two-pass and repository-scoped (GD6): pass 1 builds the service and method
// index from every *_grpc.pb.go stub, pass 2 scans every other .go file against
// it. A per-file corpus would have no index and would score entirely as out of
// domain.
//
// It refuses to run without a seal file whose units_sha256 matches the units it
// was given, so the protocol's rule that the exporter must not touch the
// population before the seal is enforced by the tool rather than by a comment.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

// gistRe matches a real published gist URL. A prefix or length check is not
// enough: a placeholder cleared an earlier guard and let the extractor run over
// the locked population before any seal existed.
var gistRe = regexp.MustCompile(`^https://gist\.github\.com/[A-Za-z0-9_-]+/[0-9a-f]{16,64}$`)

const (
	unitSchema      = "t581-unit-v1"
	candidateSchema = "t581-candidate-v1"
	sealSchema      = "t581-seal-v1"
)

type unit struct {
	Schema     string   `json:"schema"`
	Unit       string   `json:"unit"`
	Repository string   `json:"repository"`
	Commit     string   `json:"commit"`
	Dir        string   `json:"dir"`
	GoFiles    []string `json:"go_files"`
	StubFiles  []string `json:"stub_files"`
}

type seal struct {
	Schema      string `json:"schema"`
	UnitsSHA256 string `json:"units_sha256"`
	SealedUTC   string `json:"sealed_utc"`
	Gist        string `json:"gist"`
	ProtocolSHA string `json:"protocol_sha256"`
}

// candidate is one emitted fact: one row per site, never one per assertion.
// Two sites in one file sharing a claim identity collapse into one stored
// assertion carrying two atoms, so exporting assertions would understate the
// denominator and silently pass missed sites.
type candidate struct {
	Schema    string `json:"schema"`
	UnitID    string `json:"unit_id"`
	Repo      string `json:"repository"`
	Commit    string `json:"commit"`
	Path      string `json:"path"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	Tier      string `json:"tier"`
	CodeRole  string `json:"code_role"`
	Detail    string `json:"detail"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// treeCorpus serves one unit's complete .go tree from bytes read at the pinned
// commit, so the extractor sees exactly the locked content and nothing from the
// working tree.
type treeCorpus struct {
	repository string
	commit     string
	files      []string
	blobs      map[string]string
}

func (c *treeCorpus) RepoName() string { return c.repository }
func (c *treeCorpus) Commit() string   { return c.commit }

func (c *treeCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	for _, path := range c.files {
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

func (c *treeCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	content, ok := c.blobs[path]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("%s is not in this unit's pinned tree", path)
	}
	sum := sha256.Sum256([]byte(content))
	return sdk.Blob{Content: content, Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

// readTree loads every listed path at exactly one commit through a single
// `git cat-file --batch` per unit. One child per unit, not one per file.
func readTree(clone string, u unit) (map[string]string, error) {
	command := exec.Command("git", "-C", clone, "cat-file", "--batch")
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	go func() {
		defer func() { _ = stdin.Close() }()
		for _, path := range u.GoFiles {
			_, _ = io.WriteString(stdin, u.Commit+":"+path+"\n")
		}
	}()
	blobs := make(map[string]string, len(u.GoFiles))
	reader := bufio.NewReader(stdout)
	for _, path := range u.GoFiles {
		header, err := reader.ReadString('\n')
		if err != nil {
			_ = command.Wait()
			return nil, fmt.Errorf("%s: batch header: %w", path, err)
		}
		fields := strings.Fields(strings.TrimSuffix(header, "\n"))
		if len(fields) == 2 && fields[1] == "missing" {
			_ = command.Wait()
			return nil, fmt.Errorf("%s: absent from commit %s", path, u.Commit)
		}
		if len(fields) != 3 {
			_ = command.Wait()
			return nil, fmt.Errorf("%s: unparsable batch header %q", path, header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			_ = command.Wait()
			return nil, fmt.Errorf("%s: bad size %q: %w", path, fields[2], err)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			_ = command.Wait()
			return nil, fmt.Errorf("%s: short read: %w", path, err)
		}
		// Each object is followed by a single newline.
		if terminator, err := reader.ReadByte(); err != nil || terminator != '\n' {
			_ = command.Wait()
			return nil, fmt.Errorf("%s: missing batch terminator", path)
		}
		blobs[path] = string(content)
	}
	if err := command.Wait(); err != nil {
		return nil, err
	}
	return blobs, nil
}

// requireSeal enforces the protocol's pre-seal prohibition on running the
// extractor over the population. It returns the units bytes it validated, so the
// digest the seal binds is the digest of exactly what is then parsed.
func requireSeal(sealPath, unitsPath, protocolPath string, gunzip bool) ([]byte, error) {
	if sealPath == "" {
		return nil, errors.New("refusing to export without -seal: the round is not sealed")
	}
	raw, err := os.ReadFile(sealPath)
	if err != nil {
		return nil, fmt.Errorf("read seal: %w", err)
	}
	var value seal
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("seal is not a valid %s document: %w", sealSchema, err)
	}
	if value.Schema != sealSchema {
		return nil, fmt.Errorf("seal schema = %q, want %q", value.Schema, sealSchema)
	}
	committed, err := os.ReadFile(unitsPath)
	if err != nil {
		return nil, err
	}
	// The seal binds the committed bytes, which are gzipped, so the digest is
	// taken before decompression.
	if actual := digestBytes(committed); value.UnitsSHA256 != actual {
		return nil, fmt.Errorf(
			"seal binds units %s but the units file is %s: the population moved after sealing",
			value.UnitsSHA256, actual,
		)
	}
	// A seal is only a seal if it names a published gist, carries a real date,
	// and binds the protocol text the thresholds were frozen in. Without these
	// a hand-written placeholder file would satisfy the units digest alone and
	// silently defeat the pre-seal prohibition this guard exists to enforce.
	//
	// The gist must look like a real published one. A length check is not
	// enough: "https://gist.github.com/TODO" cleared an earlier version of this
	// guard and let the extractor run over the locked population before any
	// seal existed, which is recorded as a protocol breach in §7.
	if !gistRe.MatchString(value.Gist) {
		return nil, fmt.Errorf(
			"seal names no published gist (%q): §7 requires a real "+
				"https://gist.github.com/<user>/<id> URL before export runs",
			value.Gist,
		)
	}
	if _, err := time.Parse("2006-01-02", value.SealedUTC); err != nil {
		return nil, fmt.Errorf("seal date %q is not YYYY-MM-DD: %w", value.SealedUTC, err)
	}
	if sealed, err := time.Parse("2006-01-02", value.SealedUTC); err == nil &&
		sealed.After(time.Now().UTC().AddDate(0, 0, 1)) {
		return nil, fmt.Errorf("seal date %q is in the future", value.SealedUTC)
	}
	// The seal must also be a committed round record, not an ad-hoc file: a
	// seal that is not tracked in git has no reviewed provenance.
	if out, err := exec.Command("git", "ls-files", "--error-unmatch", sealPath).CombinedOutput(); err != nil {
		return nil, fmt.Errorf(
			"seal %s is not tracked in git (%v): commit the round record before exporting",
			sealPath, strings.TrimSpace(string(out)),
		)
	}
	protocol, err := os.ReadFile(protocolPath)
	if err != nil {
		return nil, fmt.Errorf("read the protocol the seal must bind: %w", err)
	}
	if actual := digestBytes(protocol); value.ProtocolSHA != actual {
		return nil, fmt.Errorf(
			"seal binds protocol %s but %s is %s: the specification moved after sealing",
			value.ProtocolSHA, protocolPath, actual,
		)
	}
	if !gunzip {
		return committed, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(committed))
	if err != nil {
		return nil, fmt.Errorf("units are not valid gzip: %w", err)
	}
	expanded, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if err := reader.Close(); err != nil {
		return nil, err
	}
	return expanded, nil
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func openUnits(raw []byte) ([]unit, error) {
	var result []unit
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for decoder.More() {
		var u unit
		if err := decoder.Decode(&u); err != nil {
			return nil, err
		}
		if u.Schema != unitSchema {
			return nil, fmt.Errorf("unit %q has schema %q, want %q", u.Unit, u.Schema, unitSchema)
		}
		if len(u.GoFiles) == 0 {
			return nil, fmt.Errorf("unit %q has no .go files", u.Unit)
		}
		result = append(result, u)
	}
	if len(result) == 0 {
		return nil, errors.New("the units file holds no units")
	}
	return result, nil
}

func main() {
	unitsPath := flag.String("units", "", "population units.jsonl.gz as committed")
	gunzip := flag.Bool("gunzip", true, "the committed units file is gzipped; set false for plain JSON Lines")
	corpus := flag.String("corpus", "", "corpus root holding one clone directory per unit")
	sealPath := flag.String("seal", "", "sealed round file; required, and must bind this units digest")
	protocolPath := flag.String("protocol", "docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md",
		"protocol the seal must bind by digest")
	only := flag.String("unit", "", "export only this unit id")
	flag.Parse()

	if *unitsPath == "" || *corpus == "" {
		fmt.Fprintln(os.Stderr, "both -units and -corpus are required")
		os.Exit(2)
	}
	raw, err := requireSeal(*sealPath, *unitsPath, *protocolPath, *gunzip)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export refused: %v\n", err)
		os.Exit(1)
	}
	units, err := openUnits(raw)
	check(err)

	extractor := grpcgo.New()
	encoder := json.NewEncoder(os.Stdout)
	for _, u := range units {
		if *only != "" && u.Unit != *only {
			continue
		}
		blobs, err := readTree(filepath.Join(*corpus, u.Dir), u)
		check(err)
		tree := &treeCorpus{
			repository: u.Repository, commit: u.Commit,
			files: u.GoFiles, blobs: blobs,
		}
		count := 0
		_, err = extractor.Extract(context.Background(), tree, func(f sdk.Fact) error {
			count++
			return encoder.Encode(candidate{
				Schema: candidateSchema, UnitID: u.Unit, Repo: u.Repository, Commit: u.Commit,
				Path: f.Path, Predicate: f.Assertion.Predicate, Object: f.Assertion.Object,
				Tier: f.Assertion.Tier, CodeRole: f.Assertion.CodeRole, Detail: f.Assertion.Detail,
				StartByte: f.Atom.StartByte, EndByte: f.Atom.EndByte,
				StartLine: f.StartLine, EndLine: f.EndLine,
			})
		})
		check(err)
		fmt.Fprintf(os.Stderr, "  %-38s %6d files -> %5d facts\n", u.Unit, len(u.GoFiles), count)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		os.Exit(1)
	}
}

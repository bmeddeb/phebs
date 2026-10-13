//go:build t551export

// Command export runs the in-tree kafka-producer extractor over one checkout
// through the sdk.Corpus interface the worker uses, and writes every emitted
// fact as JSON lines: the candidate side of the T55.1 round. It must not run
// on the round population before the round is sealed.
package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

var newExtractor = kafkago.NewProducer

type checkout struct {
	dir, name, commit string
	files             []string
}

func (c checkout) RepoName() string { return c.name }
func (c checkout) Commit() string   { return c.commit }

func (c checkout) WalkFiles(ctx context.Context, visit func(path string) error) error {
	for _, f := range c.files {
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}

func (c checkout) Read(_ context.Context, path string) (sdk.Blob, error) {
	content, err := os.ReadFile(filepath.Join(c.dir, path))
	if err != nil {
		return sdk.Blob{}, err
	}
	sum := sha256.Sum256(content)
	return sdk.Blob{Content: string(content), Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

type row struct {
	UnitID     string `json:"unit_id,omitempty"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	Predicate  string `json:"predicate"`
	Object     string `json:"object"`
	Tier       string `json:"tier"`
	Detail     string `json:"detail"`
	StartByte  int    `json:"start_byte"`
	EndByte    int    `json:"end_byte"`
	StartLine  int    `json:"start_line"`
}

// blobCorpus is one population unit: a single file whose content is the unit's blob.
type blobCorpus struct {
	name, commit, path, content string
}

func (b blobCorpus) RepoName() string { return b.name }
func (b blobCorpus) Commit() string   { return b.commit }
func (b blobCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	return visit(b.path)
}
func (b blobCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	if path != b.path {
		return sdk.Blob{}, fmt.Errorf("%s is not this unit's file", path)
	}
	sum := sha256.Sum256([]byte(b.content))
	return sdk.Blob{Content: b.content, Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

type unit struct {
	UnitID     string `json:"unit_id"`
	Repository string `json:"repository"`
	Dir        string `json:"dir"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	Blob       string `json:"blob"`
	SHA256     string `json:"sha256"`
}

func exportUnits(unitsPath, history string) {
	raw, err := os.Open(unitsPath)
	check(err)
	defer raw.Close()
	zr, err := gzip.NewReader(raw)
	check(err)
	extractor := newExtractor()
	enc := json.NewEncoder(os.Stdout)
	dec := json.NewDecoder(zr)
	for dec.More() {
		var u unit
		check(dec.Decode(&u))
		content, err := exec.Command("git", "-C", filepath.Join(history, u.Dir), "cat-file", "blob", u.Blob).Output()
		check(err)
		if sum := sha256.Sum256(content); "sha256:"+hex.EncodeToString(sum[:]) != u.SHA256 {
			check(fmt.Errorf("%s: blob does not match its frozen digest", u.UnitID))
		}
		c := blobCorpus{name: u.Repository, commit: u.Commit, path: u.Path, content: string(content)}
		_, err = extractor.Extract(context.Background(), c, func(f sdk.Fact) error {
			return enc.Encode(row{UnitID: u.UnitID, Repository: u.Repository, Commit: u.Commit, Path: f.Path,
				Predicate: f.Assertion.Predicate, Object: f.Assertion.Object, Tier: f.Assertion.Tier,
				Detail: f.Assertion.Detail, StartByte: f.Atom.StartByte, EndByte: f.Atom.EndByte, StartLine: f.StartLine})
		})
		check(err)
	}
}

func main() {
	dir := flag.String("repo", "", "checkout directory")
	name := flag.String("name", "", "repository name")
	units := flag.String("units", "", "population units.jsonl.gz; each unit's blob is read from -history")
	history := flag.String("history", "", "directory of bare history clones")
	plane := flag.String("plane", "producer", "producer (T55.1) or consumer (T56.1)")
	flag.Parse()
	if *plane == "consumer" {
		newExtractor = kafkago.NewConsumer
	}
	if *units != "" {
		exportUnits(*units, *history)
		return
	}
	head, err := exec.Command("git", "-C", *dir, "rev-parse", "HEAD").Output()
	check(err)
	listed, err := exec.Command("git", "-C", *dir, "ls-files", "-z", "--", "*.go").Output()
	check(err)
	extractor := newExtractor()
	c := checkout{dir: *dir, name: *name, commit: strings.TrimSpace(string(head))}
	for _, f := range strings.Split(string(listed), "\x00") {
		if f != "" && extractor.Candidate(f) {
			c.files = append(c.files, f)
		}
	}
	sort.Strings(c.files)
	enc := json.NewEncoder(os.Stdout)
	_, err = extractor.Extract(context.Background(), c, func(f sdk.Fact) error {
		return enc.Encode(row{Repository: c.name, Commit: c.commit, Path: f.Path,
			Predicate: f.Assertion.Predicate, Object: f.Assertion.Object, Tier: f.Assertion.Tier,
			Detail: f.Assertion.Detail, StartByte: f.Atom.StartByte, EndByte: f.Atom.EndByte, StartLine: f.StartLine})
	})
	check(err)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

//go:build t583export

// Command export runs the in-tree grpc-consumer extractor over every tracked
// .go file of one checkout (grpcgo indexes the repository's stubs before it
// scans call sites, so the corpus is the whole checkout) and writes one JSON
// line per emitted row. It is the candidate side of the T58.3 identity round
// and must not run on the population before its lock is committed.
package main

import (
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

	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type checkout struct {
	dir, name, commit string
	files             []string
}

func (c checkout) RepoName() string { return c.name }
func (c checkout) Commit() string   { return c.commit }
func (c checkout) WalkFiles(_ context.Context, visit func(string) error) error {
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
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Predicate  string `json:"predicate"`
	Object     string `json:"object"`
	StartByte  int    `json:"start_byte"`
	EndByte    int    `json:"end_byte"`
	CodeRole   string `json:"code_role"`
}

func main() {
	dir := flag.String("repo", "", "checkout directory")
	name := flag.String("name", "", "repository name")
	flag.Parse()
	head, err := exec.Command("git", "-C", *dir, "rev-parse", "HEAD").Output()
	check(err)
	listed, err := exec.Command("git", "-C", *dir, "ls-files", "-z", "--", "*.go").Output()
	check(err)
	extractor := grpcgo.New()
	c := checkout{dir: *dir, name: *name, commit: strings.TrimSpace(string(head))}
	for _, f := range strings.Split(string(listed), "\x00") {
		if f != "" && extractor.Candidate(f) {
			c.files = append(c.files, f)
		}
	}
	sort.Strings(c.files)
	enc := json.NewEncoder(os.Stdout)
	_, err = extractor.Extract(context.Background(), c, func(f sdk.Fact) error {
		return enc.Encode(row{*name, f.Path, f.Assertion.Predicate, f.Assertion.Object,
			f.Atom.StartByte, f.Atom.EndByte, f.Assertion.CodeRole})
	})
	check(err)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

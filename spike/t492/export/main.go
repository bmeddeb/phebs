//go:build t492export

// Command export runs the in-tree thrift-contract extractor over every
// tracked .thrift file of one checkout, one file at a time, and writes one
// JSON line per file: its facts, or the error that would fail the whole
// repository's run in production. It is the candidate side of the T49.2
// round and must not run on the population before its lock is committed.
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

	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type blobCorpus struct{ name, commit, path, content string }

func (b blobCorpus) RepoName() string { return b.name }
func (b blobCorpus) Commit() string   { return b.commit }
func (b blobCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	return visit(b.path)
}
func (b blobCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	if path != b.path {
		return sdk.Blob{}, fmt.Errorf("%s is not this file", path)
	}
	sum := sha256.Sum256([]byte(b.content))
	return sdk.Blob{Content: b.content, Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

type fact struct {
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	Detail    string `json:"detail"`
}

type fileRow struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Error      string `json:"error,omitempty"`
	Facts      []fact `json:"facts"`
}

func main() {
	dir := flag.String("repo", "", "checkout directory")
	name := flag.String("name", "", "repository name")
	flag.Parse()
	head, err := exec.Command("git", "-C", *dir, "rev-parse", "HEAD").Output()
	check(err)
	listed, err := exec.Command("git", "-C", *dir, "ls-files", "-z", "--", "*.thrift").Output()
	check(err)
	var files []string
	for _, f := range strings.Split(string(listed), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	commit := strings.TrimSpace(string(head))
	extractor := thriftdecl.New()
	enc := json.NewEncoder(os.Stdout)
	for _, path := range files {
		content, err := os.ReadFile(filepath.Join(*dir, path))
		check(err)
		sum := sha256.Sum256(content)
		row := fileRow{Repository: *name, Commit: commit, Path: path,
			SHA256: "sha256:" + hex.EncodeToString(sum[:]), Facts: []fact{}}
		_, err = extractor.Extract(context.Background(),
			blobCorpus{name: *name, commit: commit, path: path, content: string(content)},
			func(f sdk.Fact) error {
				row.Facts = append(row.Facts, fact{f.Assertion.Predicate, f.Assertion.Object, f.Assertion.Detail})
				return nil
			})
		if err != nil {
			row.Error, row.Facts = err.Error(), []fact{}
		}
		check(enc.Encode(row))
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

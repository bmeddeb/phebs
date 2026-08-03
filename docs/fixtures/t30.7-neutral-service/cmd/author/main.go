// Command author creates the retained T30.7 neutral service Git bundle.
//
// Normal tests and make dev consume the committed bundle. Re-authoring is an
// explicit maintenance action so the reviewed source tree and receipt cannot
// drift silently.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	authorName  = "phebs-t307-fixture-author"
	authorEmail = "fixture@example.invalid"
	commitDate  = "2026-08-02T23:30:00Z"
)

func main() {
	root := filepath.Join("docs", "fixtures", "t30.7-neutral-service")
	if len(os.Args) == 2 {
		root = filepath.Clean(os.Args[1])
	}
	absoluteRoot, err := filepath.Abs(root)
	must(err)
	root = absoluteRoot
	temp, err := os.MkdirTemp("", "phebs-t307-author-*")
	must(err)
	defer func() { _ = os.RemoveAll(temp) }()

	repository := filepath.Join(temp, "repo")
	must(os.MkdirAll(repository, 0o755))
	must(copyTree(filepath.Join(root, "repo"), repository))
	run(repository, nil, "git", "init", "-b", "main")
	run(repository, nil, "git", "config", "user.name", authorName)
	run(repository, nil, "git", "config", "user.email", authorEmail)
	commit(repository, commitDate, "fixture: add neutral focused service cohort")

	target := filepath.Join(root, "t307-neutral-service.bundle")
	must(os.RemoveAll(target))
	// Carry both the symbolic checkout target and its branch. A bundle that
	// advertises only refs/heads/main is not cloned consistently by every
	// supported Git version.
	run(repository, nil, "git", "bundle", "create", target, "HEAD", "main")
	commitHash := strings.TrimSpace(string(run(
		repository, nil, "git", "rev-parse", "HEAD",
	)))
	bundle, err := os.ReadFile(target)
	must(err)
	digest := sha256.Sum256(bundle)
	fmt.Printf(
		"commit=%s\nbundle_sha256=sha256:%s\nbundle_bytes=%d\n",
		commitHash,
		hex.EncodeToString(digest[:]),
		len(bundle),
	)
}

func commit(repository, date, message string) {
	environment := []string{
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_DATE=" + date,
	}
	run(repository, environment, "git", "add", "--all")
	run(repository, environment, "git", "commit", "-m", message)
}

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o644)
	})
}

func run(dir string, environment []string, name string, arguments ...string) []byte {
	command := exec.Command(name, arguments...)
	command.Dir = dir
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("%s %v: %v\n%s", name, arguments, err, output))
	}
	return output
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// Command author creates the retained T21.14 synthetic Git bundle.
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
)

const (
	authorName  = "phebs-t2114-fixture-author"
	authorEmail = "fixture@example.invalid"
	firstDate   = "2026-07-27T18:00:00Z"
	secondDate  = "2026-07-27T18:01:00Z"
)

func main() {
	root := filepath.Join("docs", "fixtures", "change-workbench")
	if len(os.Args) == 2 {
		root = filepath.Clean(os.Args[1])
	}
	absoluteRoot, err := filepath.Abs(root)
	must(err)
	root = absoluteRoot
	temp, err := os.MkdirTemp("", "phebs-t2114-author-*")
	must(err)
	defer func() { _ = os.RemoveAll(temp) }()

	repository := filepath.Join(temp, "repo")
	must(os.MkdirAll(repository, 0o755))
	must(copyTree(filepath.Join(root, "repo"), repository))
	run(repository, nil, "git", "init", "-b", "main")
	run(repository, nil, "git", "config", "user.name", authorName)
	run(repository, nil, "git", "config", "user.email", authorEmail)

	legacy := filepath.Join(repository, "src", "legacy", "retired_server.go")
	must(os.MkdirAll(filepath.Dir(legacy), 0o755))
	must(os.WriteFile(legacy, []byte(`package legacy

const operation = "/demo.search.v1.CodeSearch/LegacySearch"
`), 0o644))
	commit(repository, firstDate, "fixture: establish neutral Workbench scenarios")
	must(os.Remove(legacy))
	commit(repository, secondDate, "fixture: retire legacy implementation")

	target := filepath.Join(root, "t2114-workbench-closure.bundle")
	must(os.RemoveAll(target))
	run(repository, nil, "git", "bundle", "create", target, "main")
	commitHash := string(run(repository, nil, "git", "rev-parse", "HEAD"))
	bundle, err := os.ReadFile(target)
	must(err)
	digest := sha256.Sum256(bundle)
	fmt.Printf(
		"commit=%s\nbundle_sha256=sha256:%s\nbundle_bytes=%d\n",
		trimLine(commitHash),
		hex.EncodeToString(digest[:]),
		len(bundle),
	)
}

func commit(repository, date, message string) {
	run(repository, []string{
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_DATE=" + date,
	}, "git", "add", "--all")
	run(repository, []string{
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_DATE=" + date,
	}, "git", "commit", "-m", message)
}

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
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

func trimLine(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	return value
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

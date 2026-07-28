// Command author creates the reviewed T22.5 synthetic field-zero SCIP input.
//
// The committed index is the demo input. Normal tests and make dev never run
// this command: they verify and consume the authored bytes. The symbols are
// deliberately asserted rather than produced by a real indexer, following the
// prepared-once policy recorded by T20.1 and T22.1.
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

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

const (
	symbol        = "scip-go gomod example.invalid/t225-thrift-field-demo v0.0.0 demo/Meta_Health_Result#GetSuccess()."
	authorName    = "phebs fixture"
	authorEmail   = "fixture@example.invalid"
	authorDate    = "2026-07-27T12:00:00Z"
	commitMessage = "add neutral Thrift field-zero demo"
)

func main() {
	root := filepath.Join("docs", "fixtures", "thrift-field", "repo")
	if len(os.Args) == 2 {
		root = filepath.Clean(os.Args[1])
	}
	absoluteRoot, err := filepath.Abs(root)
	must(err)
	root = absoluteRoot

	generated := mustRead(root, "generated/health.go")
	consumer := mustRead(root, "consumer/use.go")
	index := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo:             &scip.ToolInfo{Name: "phebs-t225-authored-preparer", Version: "1"},
			ProjectRoot:          "file:///fixture/t225-thrift-field-demo",
			TextDocumentEncoding: scip.TextEncoding_UTF8,
		},
		Documents: []*scip.Document{
			document("generated/health.go", generated, "GetSuccess", scip.SymbolRole_Definition|scip.SymbolRole_Generated),
			document("consumer/use.go", consumer, "GetSuccess", scip.SymbolRole_ReadAccess),
		},
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		panic(err)
	}
	target := filepath.Join(root, "index.scip")
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		panic(err)
	}

	temp, err := os.MkdirTemp("", "phebs-t225-author-*")
	must(err)
	defer func() { _ = os.RemoveAll(temp) }()
	repository := filepath.Join(temp, "repo")
	must(os.MkdirAll(repository, 0o755))
	must(copyTree(root, repository))
	run(repository, nil, "git", "init", "-b", "main")
	run(repository, nil, "git", "config", "user.name", authorName)
	run(repository, nil, "git", "config", "user.email", authorEmail)
	run(repository, []string{
		"GIT_AUTHOR_DATE=" + authorDate,
		"GIT_COMMITTER_DATE=" + authorDate,
	}, "git", "add", "--all")
	run(repository, []string{
		"GIT_AUTHOR_DATE=" + authorDate,
		"GIT_COMMITTER_DATE=" + authorDate,
	}, "git", "commit", "-m", commitMessage)

	bundlePath := filepath.Join(filepath.Dir(root), "t225-thrift-field-demo.bundle")
	must(os.RemoveAll(bundlePath))
	// Explicit HEAD makes the retained bundle clone the same way under Apple
	// Git and the newer Linux Git used by hosted CI.
	run(repository, nil, "git", "bundle", "create", bundlePath, "HEAD", "main")
	commitHash := trimLine(string(run(repository, nil, "git", "rev-parse", "HEAD")))
	bundle := mustReadFile(bundlePath)
	indexSum := sha256.Sum256(encoded)
	bundleSum := sha256.Sum256(bundle)
	fmt.Printf(
		"commit=%s\nindex_sha256=sha256:%s\nindex_bytes=%d\nbundle_sha256=sha256:%s\nbundle_bytes=%d\n",
		commitHash,
		hex.EncodeToString(indexSum[:]),
		len(encoded),
		hex.EncodeToString(bundleSum[:]),
		len(bundle),
	)
}

func mustRead(root, name string) string {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		panic(err)
	}
	return string(content)
}

func document(path, content, identifier string, roles scip.SymbolRole) *scip.Document {
	offset := strings.Index(content, identifier)
	if offset < 0 {
		panic(fmt.Sprintf("%s: identifier %q not found", path, identifier))
	}
	line := int32(strings.Count(content[:offset], "\n"))
	lineStart := strings.LastIndex(content[:offset], "\n") + 1
	character := int32(offset - lineStart)
	return &scip.Document{
		RelativePath:     path,
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{
			Range:       []int32{line, character, character + int32(len(identifier))},
			Symbol:      symbol,
			SymbolRoles: int32(roles),
		}},
	}
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

func mustReadFile(path string) []byte {
	content, err := os.ReadFile(path)
	must(err)
	return content
}

func trimLine(value string) string {
	return strings.TrimRight(value, "\r\n")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

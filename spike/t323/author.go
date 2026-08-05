package t323

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	fixtureAuthorName  = "phebs-t323-fixture-author"
	fixtureAuthorEmail = "fixture@example.invalid"
	bundleName         = "t323-neutral-corpus.bundle"
)

func Author(root string) (Receipt, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Receipt{}, err
	}
	artifacts, err := BuildArtifacts()
	if err != nil {
		return Receipt{}, err
	}
	snapshots, err := SmallSnapshots()
	if err != nil {
		return Receipt{}, err
	}
	temporary, err := os.MkdirTemp("", "phebs-t323-author-*")
	if err != nil {
		return Receipt{}, err
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	repository := filepath.Join(temporary, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		return Receipt{}, err
	}
	if _, err := runGit(repository, nil, "init", "-q", "-b", "main"); err != nil {
		return Receipt{}, err
	}
	for key, value := range map[string]string{
		"user.name": fixtureAuthorName, "user.email": fixtureAuthorEmail,
		"core.autocrlf": "false", "core.filemode": "false",
		"commit.gpgsign": "false",
	} {
		if _, err := runGit(repository, nil, "config", key, value); err != nil {
			return Receipt{}, err
		}
	}

	revisions := make([]RevisionIdentity, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if err := replaceWorktree(repository, snapshot.Files); err != nil {
			return Receipt{}, err
		}
		environment := []string{
			"GIT_AUTHOR_DATE=" + snapshot.Date,
			"GIT_COMMITTER_DATE=" + snapshot.Date,
			"TZ=UTC",
			"LC_ALL=C",
		}
		if _, err := runGit(repository, environment, "add", "--all"); err != nil {
			return Receipt{}, err
		}
		if _, err := runGit(repository, environment, "commit", "--no-gpg-sign", "-q", "-m", snapshot.Message); err != nil {
			return Receipt{}, err
		}
		commit, err := runGit(repository, nil, "rev-parse", "HEAD")
		if err != nil {
			return Receipt{}, err
		}
		tree, err := runGit(repository, nil, "rev-parse", "HEAD^{tree}")
		if err != nil {
			return Receipt{}, err
		}
		revisions = append(revisions, RevisionIdentity{
			Revision: snapshot.Revision, Commit: strings.TrimSpace(commit),
			Tree: strings.TrimSpace(tree), RegularFiles: len(snapshot.Files),
		})
	}

	bundlePath := filepath.Join(temporary, bundleName)
	if _, err := runGit(repository, nil, "bundle", "create", bundlePath, "HEAD", "main"); err != nil {
		return Receipt{}, err
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return Receipt{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Receipt{}, err
	}
	for artifactPath, content := range artifacts {
		if err := writeFile(root, artifactPath, content); err != nil {
			return Receipt{}, err
		}
	}
	if err := writeFile(root, bundleName, bundle); err != nil {
		return Receipt{}, err
	}

	artifactPaths := make([]string, 0, len(artifacts))
	for artifactPath := range artifacts {
		artifactPaths = append(artifactPaths, artifactPath)
	}
	slices.Sort(artifactPaths)
	receipt := Receipt{
		Schema: ReceiptSchema, Repository: RepositoryName,
		Bundle:    ArtifactIdentity{Path: bundleName, Bytes: len(bundle), SHA256: SHA256(bundle)},
		Revisions: revisions, Claims: ProfileClaims{Synthetic: true},
	}
	for _, artifactPath := range artifactPaths {
		content := artifacts[artifactPath]
		receipt.Artifacts = append(receipt.Artifacts, ArtifactIdentity{
			Path: artifactPath, Bytes: len(content), SHA256: SHA256(content),
		})
	}
	receiptBytes, err := MarshalCanonical(receipt)
	if err != nil {
		return Receipt{}, err
	}
	if err := writeFile(root, "receipt.json", receiptBytes); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func replaceWorktree(repository string, files map[string][]byte) error {
	entries, err := os.ReadDir(repository)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(repository, entry.Name())); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	slices.Sort(paths)
	for _, filePath := range paths {
		if err := writeFile(repository, filePath, files[filePath]); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(root, name string, content []byte) error {
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o644)
}

func runGit(repository string, environment []string, arguments ...string) (string, error) {
	command := exec.Command("git", arguments...)
	command.Dir = repository
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	command.Env = append(command.Env, environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w\n%s", arguments, err, output)
	}
	return string(output), nil
}

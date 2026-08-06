// Command t392-observation-interrupt stages one deliberately incomplete
// observation publication for the T39.2 recovery exercise. It must be run only
// against the dedicated, stopped measurement installation. Production startup
// then exercises its ordinary marker recovery path.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const confirmation = "interrupt-incomplete-observation-stage"

type pointer struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	ManifestDigest   string `json:"manifest_digest"`
	ManifestName     string `json:"manifest_name"`
}

type marker struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	ManifestDigest   string `json:"manifest_digest,omitempty"`
}

func main() {
	root := flag.String("data", "", "absolute dedicated phebs data directory")
	repository := flag.String("repository", "", "exact private repository name")
	confirm := flag.String("confirm", "", "required destructive-test confirmation")
	flag.Parse()
	if flag.NArg() != 0 || *confirm != confirmation {
		fail(errors.New("explicit observation-interruption confirmation is required"))
	}
	if !filepath.IsAbs(*root) || strings.TrimSpace(*repository) == "" {
		fail(errors.New("absolute data directory and repository are required"))
	}
	generation, err := stage(*root, *repository)
	if err != nil {
		fail(err)
	}
	fmt.Println(generation)
}

func stage(dataDirectory, repository string) (string, error) {
	repositoryHash := sha256.Sum256([]byte(repository))
	repositoryRoot := filepath.Join(
		dataDirectory, "observations", hex.EncodeToString(repositoryHash[:]),
	)
	currentPath := filepath.Join(repositoryRoot, "current.json")
	raw, err := readBoundedRegular(currentPath, 64<<10)
	if err != nil {
		return "", fmt.Errorf("read current observation pointer: %w", err)
	}
	var current pointer
	if err := decodeCanonical(raw, &current); err != nil ||
		current.Schema != "phebs-observation-pointer-v1" || current.Repository != repository {
		return "", errors.New("current observation pointer is not exact authority")
	}
	digest := sha256.Sum256([]byte("phebs-t392-interrupted-observation-v1\x00" + repository))
	generation := "sha256:" + hex.EncodeToString(digest[:])
	if current.GenerationDigest == generation {
		return "", errors.New("interrupted generation collides with current authority")
	}
	value := marker{
		Schema: "phebs-observation-marker-v1", Repository: repository,
		GenerationDigest: generation,
	}
	markerBytes, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	markerPath := filepath.Join(repositoryRoot, "publishing.json")
	markerFile, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create observation marker: %w", err)
	}
	markerComplete := false
	defer func() {
		if !markerComplete {
			_ = markerFile.Close()
			_ = os.Remove(markerPath)
		}
	}()
	if _, err := markerFile.Write(markerBytes); err != nil {
		return "", err
	}
	if err := markerFile.Sync(); err != nil {
		return "", err
	}
	if err := markerFile.Close(); err != nil {
		return "", err
	}
	markerComplete = true

	generationRoot := filepath.Join(repositoryRoot, strings.TrimPrefix(generation, "sha256:"))
	if err := os.Mkdir(generationRoot, 0o700); err != nil {
		_ = os.Remove(markerPath)
		return "", fmt.Errorf("create incomplete generation: %w", err)
	}
	stageComplete := false
	defer func() {
		if !stageComplete {
			_ = os.RemoveAll(generationRoot)
			_ = os.Remove(markerPath)
		}
	}()
	if err := os.Mkdir(filepath.Join(generationRoot, "objects"), 0o700); err != nil {
		return "", err
	}
	partial, err := os.OpenFile(
		filepath.Join(generationRoot, "partial"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
	)
	if err != nil {
		return "", err
	}
	if err := errors.Join(partial.Sync(), partial.Close()); err != nil {
		return "", err
	}
	if err := syncDirectory(generationRoot); err != nil {
		return "", err
	}
	if err := syncDirectory(repositoryRoot); err != nil {
		return "", err
	}
	stageComplete = true
	return generation, nil
}

func decodeCanonical(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("pointer has trailing JSON")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(canonical, raw) {
		return errors.New("pointer is not canonical JSON")
	}
	return nil
}

func readBoundedRegular(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > limit {
		return nil, errors.Join(err, errors.New("pointer is not a bounded regular file"))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.Join(err, errors.New("pointer changed while opening"))
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if readErr != nil || statErr != nil || closeErr != nil || currentErr != nil ||
		len(raw) > int(limit) || !os.SameFile(opened, after) || !os.SameFile(after, current) ||
		after.Size() != int64(len(raw)) || current.Size() != after.Size() ||
		!after.ModTime().Equal(opened.ModTime()) || !current.ModTime().Equal(after.ModTime()) {
		return nil, errors.Join(
			readErr, statErr, closeErr, currentErr,
			errors.New("pointer changed while reading"),
		)
	}
	return raw, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

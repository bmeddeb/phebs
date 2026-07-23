// Package releasebundle assembles and verifies the deterministic on-disk
// release layout. The manifest authenticates bytes, not provenance: release
// signing and publication remain an operator action.
package releasebundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	Schema       = "phebs-release-manifest-v1"
	ManifestName = "release-manifest.json"
	maxManifest  = 1 << 20
)

var ErrInvalidBundle = errors.New("invalid release bundle")

var (
	releaseVersionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	commitRE         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	goVersionRE      = regexp.MustCompile(`^1\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	targetRE         = regexp.MustCompile(`^[a-z0-9]+$`)
	requiredFiles    = map[string]bool{
		"LICENSE": false, "README.md": false,
		"phebs": true, "bin/buf": true, "bin/zoekt-git-index": true,
	}
)

// Source is one explicitly authorized input to a release bundle.
type Source struct {
	Path       string
	SourcePath string
	Executable bool
}

// BuildOptions are the immutable inputs recorded in the release manifest.
type BuildOptions struct {
	OutputDir string
	Version   string
	Commit    string
	GoVersion string
	GOOS      string
	GOARCH    string
	Sources   []Source
}

// Manifest is the canonical inventory of a release directory.
type Manifest struct {
	Schema    string      `json:"schema"`
	Version   string      `json:"version"`
	Commit    string      `json:"commit"`
	GoVersion string      `json:"go_version"`
	GOOS      string      `json:"goos"`
	GOARCH    string      `json:"goarch"`
	Files     []FileEntry `json:"files"`
}

// FileEntry binds a relative path, stable installed mode, size, and digest.
type FileEntry struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Build copies the explicit sources into a new directory and writes its
// canonical manifest. It never overlays an existing path.
func Build(opts BuildOptions) (*Manifest, error) {
	if err := validateBuildOptions(opts); err != nil {
		return nil, err
	}
	parent := filepath.Dir(opts.OutputDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create release parent: %w", err)
	}
	if _, err := os.Lstat(opts.OutputDir); err == nil {
		return nil, fmt.Errorf("%w: output path already exists", ErrInvalidBundle)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspect output path: %w", err)
	}

	temp, err := os.MkdirTemp(parent, "."+filepath.Base(opts.OutputDir)+".tmp-")
	if err != nil {
		return nil, fmt.Errorf("create release staging directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temp)
		}
	}()

	sources := append([]Source(nil), opts.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	manifest := &Manifest{
		Schema:    Schema,
		Version:   opts.Version,
		Commit:    opts.Commit,
		GoVersion: opts.GoVersion,
		GOOS:      opts.GOOS,
		GOARCH:    opts.GOARCH,
		Files:     make([]FileEntry, 0, len(sources)),
	}
	for _, source := range sources {
		entry, err := copySource(temp, source)
		if err != nil {
			return nil, err
		}
		manifest.Files = append(manifest.Files, entry)
	}
	encoded, err := encodeManifest(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(temp, ManifestName), encoded, 0o644); err != nil {
		return nil, fmt.Errorf("write release manifest: %w", err)
	}
	if err := os.Chmod(filepath.Join(temp, ManifestName), 0o644); err != nil {
		return nil, fmt.Errorf("set release manifest mode: %w", err)
	}
	if err := os.Rename(temp, opts.OutputDir); err != nil {
		return nil, fmt.Errorf("publish release directory: %w", err)
	}
	keep = true
	return manifest, nil
}

// Verify validates the canonical manifest and every directory entry. It
// rejects unmanifested files and symlinks as well as byte or mode changes.
func Verify(dir string) (*Manifest, error) {
	manifestPath := filepath.Join(dir, ManifestName)
	data, err := readBounded(manifestPath, maxManifest)
	if err != nil {
		return nil, fmt.Errorf("%w: read manifest: %v", ErrInvalidBundle, err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("%w: decode manifest: %v", ErrInvalidBundle, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("%w: decode manifest: %v", ErrInvalidBundle, err)
	}
	if err := validateManifest(&manifest); err != nil {
		return nil, err
	}
	canonical, err := encodeManifest(&manifest)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("%w: manifest is not canonical JSON", ErrInvalidBundle)
	}

	expected := map[string]FileEntry{ManifestName: {}}
	allowedDirs := map[string]bool{".": true}
	for _, entry := range manifest.Files {
		expected[entry.Path] = entry
		for parent := filepath.ToSlash(filepath.Dir(entry.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			allowedDirs[parent] = true
		}
	}
	seen := make(map[string]bool, len(expected))
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if !allowedDirs[relative] {
				return fmt.Errorf("%w: unmanifested directory %q", ErrInvalidBundle, relative)
			}
			return nil
		}
		want, ok := expected[relative]
		if !ok {
			return fmt.Errorf("%w: unmanifested path %q", ErrInvalidBundle, relative)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %q", ErrInvalidBundle, relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: non-regular path %q", ErrInvalidBundle, relative)
		}
		seen[relative] = true
		if relative == ManifestName {
			if info.Mode().Perm() != 0o644 {
				return fmt.Errorf("%w: manifest mode is %04o, want 0644", ErrInvalidBundle, info.Mode().Perm())
			}
			return nil
		}
		if got := fmt.Sprintf("%04o", info.Mode().Perm()); got != want.Mode {
			return fmt.Errorf("%w: mode mismatch for %q", ErrInvalidBundle, relative)
		}
		if info.Size() != want.Size {
			return fmt.Errorf("%w: size mismatch for %q", ErrInvalidBundle, relative)
		}
		digest, _, err := digestFile(path)
		if err != nil {
			return err
		}
		if digest != want.SHA256 {
			return fmt.Errorf("%w: digest mismatch for %q", ErrInvalidBundle, relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for path := range expected {
		if !seen[path] {
			return nil, fmt.Errorf("%w: missing path %q", ErrInvalidBundle, path)
		}
	}
	return &manifest, nil
}

func validateBuildOptions(opts BuildOptions) error {
	for label, value := range map[string]string{
		"output": opts.OutputDir, "version": opts.Version, "commit": opts.Commit,
		"go version": opts.GoVersion, "goos": opts.GOOS, "goarch": opts.GOARCH,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%w: invalid %s", ErrInvalidBundle, label)
		}
	}
	if !releaseVersionRE.MatchString(opts.Version) || !commitRE.MatchString(opts.Commit) ||
		!goVersionRE.MatchString(opts.GoVersion) ||
		!targetRE.MatchString(opts.GOOS) || !targetRE.MatchString(opts.GOARCH) {
		return fmt.Errorf("%w: invalid release identity", ErrInvalidBundle)
	}
	if len(opts.Sources) == 0 {
		return fmt.Errorf("%w: no sources", ErrInvalidBundle)
	}
	if len(opts.Sources) != len(requiredFiles) {
		return fmt.Errorf("%w: release requires exactly %d payload files", ErrInvalidBundle, len(requiredFiles))
	}
	seen := make(map[string]bool, len(opts.Sources))
	for _, source := range opts.Sources {
		if !safeRelativePath(source.Path) || source.Path == ManifestName {
			return fmt.Errorf("%w: unsafe bundle path %q", ErrInvalidBundle, source.Path)
		}
		if seen[source.Path] {
			return fmt.Errorf("%w: duplicate bundle path %q", ErrInvalidBundle, source.Path)
		}
		seen[source.Path] = true
		wantExecutable, required := requiredFiles[source.Path]
		if !required || source.Executable != wantExecutable {
			return fmt.Errorf("%w: unexpected release payload %q", ErrInvalidBundle, source.Path)
		}
		info, err := os.Lstat(source.SourcePath)
		if err != nil {
			return fmt.Errorf("%w: inspect source %q: %v", ErrInvalidBundle, source.SourcePath, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: source %q is not a regular file", ErrInvalidBundle, source.SourcePath)
		}
		if source.Executable && info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("%w: source %q is not executable", ErrInvalidBundle, source.SourcePath)
		}
	}
	return nil
}

func validateManifest(manifest *Manifest) error {
	if manifest.Schema != Schema {
		return fmt.Errorf("%w: schema %q", ErrInvalidBundle, manifest.Schema)
	}
	for label, value := range map[string]string{
		"version": manifest.Version, "commit": manifest.Commit, "go version": manifest.GoVersion,
		"goos": manifest.GOOS, "goarch": manifest.GOARCH,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%w: invalid %s", ErrInvalidBundle, label)
		}
	}
	if !releaseVersionRE.MatchString(manifest.Version) || !commitRE.MatchString(manifest.Commit) ||
		!goVersionRE.MatchString(manifest.GoVersion) ||
		!targetRE.MatchString(manifest.GOOS) || !targetRE.MatchString(manifest.GOARCH) {
		return fmt.Errorf("%w: invalid release identity", ErrInvalidBundle)
	}
	if len(manifest.Files) == 0 {
		return fmt.Errorf("%w: empty file inventory", ErrInvalidBundle)
	}
	if len(manifest.Files) != len(requiredFiles) {
		return fmt.Errorf("%w: release requires exactly %d payload files", ErrInvalidBundle, len(requiredFiles))
	}
	prior := ""
	for _, entry := range manifest.Files {
		if !safeRelativePath(entry.Path) || entry.Path == ManifestName || entry.Path <= prior {
			return fmt.Errorf("%w: unsorted or unsafe file path %q", ErrInvalidBundle, entry.Path)
		}
		prior = entry.Path
		if entry.Mode != "0644" && entry.Mode != "0755" {
			return fmt.Errorf("%w: invalid mode for %q", ErrInvalidBundle, entry.Path)
		}
		wantExecutable, required := requiredFiles[entry.Path]
		wantMode := "0644"
		if wantExecutable {
			wantMode = "0755"
		}
		if !required || entry.Mode != wantMode {
			return fmt.Errorf("%w: unexpected release payload %q", ErrInvalidBundle, entry.Path)
		}
		if entry.Size < 0 || !validDigest(entry.SHA256) {
			return fmt.Errorf("%w: invalid metadata for %q", ErrInvalidBundle, entry.Path)
		}
	}
	return nil
}

func copySource(root string, source Source) (FileEntry, error) {
	target := filepath.Join(root, filepath.FromSlash(source.Path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return FileEntry{}, fmt.Errorf("create bundle directory: %w", err)
	}
	input, err := os.Open(source.SourcePath)
	if err != nil {
		return FileEntry{}, fmt.Errorf("open source %q: %w", source.SourcePath, err)
	}
	defer func() { _ = input.Close() }()
	mode := fs.FileMode(0o644)
	if source.Executable {
		mode = 0o755
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return FileEntry{}, fmt.Errorf("create bundle path %q: %w", source.Path, err)
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	closeErr := output.Close()
	if copyErr != nil {
		return FileEntry{}, fmt.Errorf("copy bundle path %q: %w", source.Path, copyErr)
	}
	if closeErr != nil {
		return FileEntry{}, fmt.Errorf("close bundle path %q: %w", source.Path, closeErr)
	}
	if err := os.Chmod(target, mode); err != nil {
		return FileEntry{}, fmt.Errorf("set bundle path %q mode: %w", source.Path, err)
	}
	return FileEntry{
		Path:   source.Path,
		Mode:   fmt.Sprintf("%04o", mode),
		Size:   size,
		SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func encodeManifest(manifest *Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func digestFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}

func safeRelativePath(path string) bool {
	return path != "" &&
		path == filepath.ToSlash(path) &&
		!strings.HasPrefix(path, "/") &&
		path != ".." &&
		!strings.HasPrefix(path, "../") &&
		!strings.Contains(path, "/../") &&
		filepath.Clean(filepath.FromSlash(path)) == filepath.FromSlash(path) &&
		path != "." &&
		!strings.Contains(path, `\`)
}

func validDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

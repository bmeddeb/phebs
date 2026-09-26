// Package t451a implements the source-free, neutral-only managed SCIP feasibility spike.
// It is not imported by the server and grants no target-repository execution authority.
package t451a

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	MaxManifestBytes     = 16 << 20
	MaxBundleFiles       = 50000
	MaxBundleDirectories = 20000
	MaxBundleBytes       = 2 << 30
	MaxFileBytes         = 256 << 20
)

// Bundle is an operator-prepared offline inventory. Every file is copied and
// verified before the sandbox is created. Links and undeclared files refuse.
type Bundle struct {
	Schema string       `json:"schema"`
	Files  []BundleFile `json:"files"`
}

type BundleFile struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable"`
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil && strings.ToLower(value) == value
}

// canonicalDecode deliberately admits only the canonical wire representation.
// Re-encoding also rejects duplicate keys, omitted fields, and alternate spellings.
func canonicalDecode[T any](data []byte, limit int) (T, error) {
	var value T
	if len(data) > limit {
		return value, errors.New("wire byte limit exceeded")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode wire: %w", err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return value, fmt.Errorf("encode wire: %w", err)
	}
	if !bytes.Equal(data, append(encoded, '\n')) {
		return value, errors.New("wire is not canonical")
	}
	return value, nil
}

func validPath(name string) bool {
	if len(name) == 0 || len(name) > 512 || !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\\x00\r\n\t") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if len(component) > 255 {
			return false
		}
	}
	return true
}

func DecodeBundle(data []byte) (Bundle, error) {
	bundle, err := canonicalDecode[Bundle](data, MaxManifestBytes)
	if err != nil {
		return Bundle{}, err
	}
	if bundle.Schema != "phebs-t451a-offline-v1" || len(bundle.Files) == 0 || len(bundle.Files) > MaxBundleFiles {
		return Bundle{}, errors.New("invalid bundle schema or count")
	}
	var total int64
	previous := ""
	for _, file := range bundle.Files {
		if !validPath(file.Path) || file.Path <= previous || !validDigest(file.SHA256) || file.Bytes < 0 || file.Bytes > MaxFileBytes {
			return Bundle{}, errors.New("invalid bundle entry")
		}
		total += file.Bytes
		if total > MaxBundleBytes {
			return Bundle{}, errors.New("bundle byte limit exceeded")
		}
		previous = file.Path
	}
	return bundle, nil
}

// ImportBundle creates a new private copy. It never runs tools, and removes
// partial copies on refusal. The source is an operator-owned tree: concurrent
// malicious host mutation is outside this spike's threat model. os.Root still
// confines all reads, and digests bind exactly the bytes actually copied.
func ImportBundle(ctx context.Context, source, parent string, manifest []byte, expected string) (_ string, err error) {
	if !validDigest(expected) || Digest(manifest) != expected {
		return "", errors.New("bundle manifest identity mismatch")
	}
	bundle, err := DecodeBundle(manifest)
	if err != nil {
		return "", err
	}
	input, err := os.OpenRoot(source)
	if err != nil {
		return "", fmt.Errorf("open bundle root: %w", err)
	}
	defer func() { _ = input.Close() }()
	entries := make(map[string]BundleFile, len(bundle.Files))
	directories := map[string]bool{".": true}
	for _, file := range bundle.Files {
		entries[file.Path] = file
		for name := path.Dir(file.Path); name != "."; name = path.Dir(name) {
			directories[name] = true
			if len(directories) > MaxBundleDirectories {
				return "", errors.New("bundle directory count limit exceeded")
			}
		}
	}
	if err := inspectInventory(ctx, input, entries, directories); err != nil {
		return "", fmt.Errorf("bundle inventory refused: %w", err)
	}
	copyRoot, err := os.MkdirTemp(parent, "t451a-inputs-")
	if err != nil {
		return "", fmt.Errorf("create private inputs: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(copyRoot))
		}
	}()
	for _, file := range bundle.Files {
		if err := copyBundleFile(ctx, input, copyRoot, file); err != nil {
			return "", err
		}
	}
	return copyRoot, nil
}

func inspectInventory(ctx context.Context, root *os.Root, entries map[string]BundleFile, directories map[string]bool) error {
	queue := []string{"."}
	seen := 0
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		dir, err := root.Open(name)
		if err != nil {
			return err
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = dir.Close()
				return err
			}
			batch, readErr := dir.ReadDir(128)
			for _, entry := range batch {
				child := path.Join(name, entry.Name())
				if entry.IsDir() && directories[child] {
					queue = append(queue, child)
					continue
				}
				file, ok := entries[child]
				if !ok || !entry.Type().IsRegular() {
					_ = dir.Close()
					return errors.New("undeclared or non-regular entry")
				}
				info, err := entry.Info()
				if err != nil || info.Size() != file.Bytes || info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
					_ = dir.Close()
					return errors.New("file metadata mismatch")
				}
				seen++
			}
			if readErr != nil {
				_ = dir.Close()
				if !errors.Is(readErr, io.EOF) {
					return readErr
				}
				break
			}
		}
	}
	if seen != len(entries) {
		return errors.New("missing bundle entry")
	}
	return nil
}

func copyBundleFile(ctx context.Context, input *os.Root, destination string, file BundleFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := input.Lstat(file.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Bytes {
		return errors.New("bundle input changed")
	}
	source, err := input.Open(file.Path)
	if err != nil {
		return fmt.Errorf("open bundle file: %w", err)
	}
	defer func() { _ = source.Close() }()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("bundle input replaced")
	}
	name := filepath.Join(destination, filepath.FromSlash(file.Path))
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return fmt.Errorf("create bundle directory: %w", err)
	}
	mode := fs.FileMode(0400)
	if file.Executable {
		mode = 0500
	}
	output, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create bundle file: %w", err)
	}
	hash := sha256.New()
	writer := io.MultiWriter(output, hash)
	buffer := make([]byte, 128<<10)
	var count int64
	for count <= file.Bytes {
		if err := ctx.Err(); err != nil {
			_ = output.Close()
			return err
		}
		n, readErr := source.Read(buffer[:min(int64(len(buffer)), file.Bytes-count+1)])
		if n > 0 {
			count += int64(n)
			if _, err := writer.Write(buffer[:n]); err != nil {
				_ = output.Close()
				return fmt.Errorf("copy bundle file: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = output.Close()
			return fmt.Errorf("read bundle file: %w", readErr)
		}
	}
	closeErr := output.Close()
	if count != file.Bytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return errors.New("bundle content identity mismatch")
	}
	if closeErr != nil {
		return fmt.Errorf("close bundle file: %w", closeErr)
	}
	return nil
}

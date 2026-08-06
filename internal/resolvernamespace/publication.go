package resolvernamespace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Publication is one immutable root. Opening current validates only bounded
// controls; each keyed member is validated on demand. Publication/recovery
// performs the complete-member pass before making the root current.
type Publication struct {
	base       string
	directory  string
	pointerRaw []byte
	pointer    Pointer
	rootValue  Root
}

func (prepared *Prepared) Publish(ctx context.Context) (*Publication, error) {
	if prepared == nil || prepared.closed {
		return nil, errors.New("resolver namespace stage is closed")
	}
	publication, err := prepared.install(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeMarker(publication.base, publication.pointer); err != nil {
		return nil, err
	}
	if _, err := openGeneration(ctx, publication.directory, publication.rootValue, true); err != nil {
		return nil, fmt.Errorf("validate installed resolver namespace generation: %w", err)
	}
	if err := writePointer(publication.base, publication.pointer); err != nil {
		return nil, err
	}
	if err := removeMarker(publication.base); err != nil {
		return nil, err
	}
	pointerRaw, err := json.Marshal(publication.pointer)
	if err != nil {
		return nil, err
	}
	publication.pointerRaw = pointerRaw
	return publication, nil
}

func (prepared *Prepared) install(ctx context.Context) (*Publication, error) {
	if prepared == nil || prepared.closed {
		return nil, errors.New("resolver namespace stage is closed")
	}
	target := generationPath(prepared.root, prepared.repository, prepared.rootValue.GenerationDigest)
	if _, err := os.Lstat(target); err == nil {
		if _, openErr := openGeneration(ctx, target, prepared.rootValue, true); openErr != nil {
			return nil, fmt.Errorf("existing resolver namespace generation conflicts: %w", openErr)
		}
		if err := os.RemoveAll(prepared.directory); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if err := os.Rename(prepared.directory, target); err != nil {
		return nil, fmt.Errorf("install resolver namespace generation: %w", err)
	}
	prepared.closed = true
	if err := syncDirectory(repositoryRoot(prepared.root, prepared.repository)); err != nil {
		return nil, err
	}
	pointer := Pointer{
		Schema: PointerSchema, Repository: prepared.repository,
		GenerationDigest: prepared.rootValue.GenerationDigest,
		RootDigest:       prepared.rootValue.Digest, RootFile: "root.json",
	}
	if err := setPointerDigest(&pointer); err != nil {
		return nil, err
	}
	return &Publication{
		base:      repositoryRoot(prepared.root, prepared.repository),
		directory: target, pointer: pointer, rootValue: prepared.rootValue,
	}, nil
}

// Recover completes an installed, fully validated marked generation. Invalid
// derived bytes never replace the prior current pointer.
func Recover(ctx context.Context, root, repository string) (*Publication, error) {
	if err := validateRepositoryDirectory(root, repository); err != nil {
		return nil, err
	}
	base := repositoryRoot(root, repository)
	raw, err := readRegular(filepath.Join(base, "publishing.json"), MaxRootBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return OpenCurrent(ctx, root, repository)
		}
		return nil, err
	}
	var value marker
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil {
		return nil, fmt.Errorf("%w: marker: %v", ErrInvalid, err)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, digestErr := digestValue(copyValue)
	if digestErr != nil || value.Schema != MarkerSchema || value.Digest != digest ||
		validatePointer(value.Pointer) != nil || value.Pointer.Repository != repository {
		return nil, fmt.Errorf("%w: marker", ErrInvalid)
	}
	directory := generationPath(root, repository, value.Pointer.GenerationDigest)
	rootValue, err := readRoot(directory, value.Pointer)
	if err != nil {
		return nil, err
	}
	publication, err := openGeneration(ctx, directory, rootValue, true)
	if err != nil {
		return nil, err
	}
	publication.base = base
	publication.pointer = value.Pointer
	if err := writePointer(base, value.Pointer); err != nil {
		return nil, err
	}
	if err := removeMarker(base); err != nil {
		return nil, err
	}
	publication.pointerRaw, _ = json.Marshal(value.Pointer)
	return publication, nil
}

func OpenCurrent(ctx context.Context, root, repository string) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRepositoryDirectory(root, repository); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	base := repositoryRoot(root, repository)
	raw, err := readRegular(filepath.Join(base, "current.json"), MaxRootBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var pointer Pointer
	if err := decodeExact(raw, MaxRootBytes, &pointer); err != nil ||
		validatePointer(pointer) != nil || pointer.Repository != repository {
		return nil, fmt.Errorf("%w: current pointer", ErrInvalid)
	}
	canonical, err := json.Marshal(pointer)
	if err != nil || !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: noncanonical current pointer", ErrInvalid)
	}
	directory := generationPath(root, repository, pointer.GenerationDigest)
	rootValue, err := readRoot(directory, pointer)
	if err != nil {
		return nil, err
	}
	publication, err := openValidatedDirectory(directory, rootValue)
	if err != nil {
		return nil, err
	}
	publication.base, publication.pointerRaw = base, raw
	publication.pointer = pointer
	return publication, nil
}

func (publication *Publication) Root() Root {
	if publication == nil {
		return Root{}
	}
	result := publication.rootValue
	result.Namespaces = slices.Clone(result.Namespaces)
	return result
}

func (publication *Publication) Lookup(
	ctx context.Context,
	language, protocol, namespace, clientType, method string,
) ([]Record, error) {
	if publication == nil || language != LanguageGo ||
		(protocol != "grpc" && protocol != "thrift") ||
		!validText(namespace) || !validText(clientType) || !validText(method) {
		return nil, fmt.Errorf("%w: lookup", ErrInvalid)
	}
	wanted := namespaceKey(language, protocol, namespace)
	index, found := slices.BinarySearchFunc(
		publication.rootValue.Namespaces, wanted,
		func(receipt NamespaceReceipt, key string) int {
			return strings.Compare(namespaceKey(receipt.Language, receipt.Protocol, receipt.Namespace), key)
		},
	)
	if !found {
		return []Record{}, nil
	}
	member, _, err := publication.openMember(ctx, publication.rootValue.Namespaces[index])
	if err != nil {
		return nil, err
	}
	result := []Record{}
	for _, record := range member.Records {
		if record.ClientType == clientType && record.Method == method {
			result = append(result, record)
		}
	}
	return cloneRecords(result), nil
}

// LookupCurrent fences both sides of a sparse member read with the exact
// current pointer. A concurrent publication discards the result.
func LookupCurrent(
	ctx context.Context,
	root, repository, language, protocol, namespace, clientType, method string,
) (*Publication, []Record, error) {
	publication, err := OpenCurrent(ctx, root, repository)
	if err != nil {
		return nil, nil, err
	}
	result, err := publication.Lookup(ctx, language, protocol, namespace, clientType, method)
	if err != nil {
		return nil, nil, err
	}
	current, err := readRegular(filepath.Join(publication.base, "current.json"), MaxRootBytes)
	if err != nil {
		return nil, nil, err
	}
	if !slices.Equal(current, publication.pointerRaw) {
		return nil, nil, fmt.Errorf("resolver namespace authority changed: %w", ErrPublishing)
	}
	return publication, result, nil
}

func readRoot(directory string, pointer Pointer) (Root, error) {
	raw, err := readRegular(filepath.Join(directory, pointer.RootFile), MaxRootBytes)
	if err != nil {
		return Root{}, err
	}
	var value Root
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil || validateRoot(value) != nil ||
		value.Authority.Repository != pointer.Repository ||
		value.GenerationDigest != pointer.GenerationDigest || value.Digest != pointer.RootDigest {
		return Root{}, fmt.Errorf("%w: root control", ErrInvalid)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !slices.Equal(raw, canonical) {
		return Root{}, fmt.Errorf("%w: noncanonical root", ErrInvalid)
	}
	return value, nil
}

func openGeneration(
	ctx context.Context,
	directory string,
	expected Root,
	complete bool,
) (*Publication, error) {
	publication, err := openValidatedDirectory(directory, expected)
	if err != nil {
		return nil, err
	}
	if !complete {
		return publication, nil
	}
	raw, err := readRegular(filepath.Join(directory, "root.json"), MaxRootBytes)
	if err != nil {
		return nil, err
	}
	var value Root
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil || validateRoot(value) != nil ||
		value.Digest != expected.Digest || value.GenerationDigest != expected.GenerationDigest {
		return nil, fmt.Errorf("%w: generation root", ErrInvalid)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: generation root bytes", ErrInvalid)
	}
	publication.rootValue = value
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	if len(entries) != len(value.Namespaces)+1 {
		return nil, fmt.Errorf("%w: generation inventory", ErrInvalid)
	}
	wanted := map[string]struct{}{"root.json": {}}
	for _, receipt := range value.Namespaces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wanted[receipt.Member] = struct{}{}
		if _, _, err := publication.openMember(ctx, receipt); err != nil {
			return nil, err
		}
	}
	for _, entry := range entries {
		if _, found := wanted[entry.Name()]; !found || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: unexpected generation entry", ErrInvalid)
		}
	}
	return publication, nil
}

func openValidatedDirectory(directory string, expected Root) (*Publication, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: generation directory", ErrInvalid)
	}
	return &Publication{directory: directory, rootValue: expected}, nil
}

func (publication *Publication) openMember(
	ctx context.Context,
	receipt NamespaceReceipt,
) (Member, []byte, error) {
	if err := ctx.Err(); err != nil {
		return Member{}, nil, err
	}
	raw, err := readRegular(filepath.Join(publication.directory, receipt.Member), MaxMemberBytes)
	if err != nil {
		return Member{}, nil, err
	}
	if int64(len(raw)) != receipt.ContentBytes {
		return Member{}, nil, fmt.Errorf("%w: member bytes", ErrInvalid)
	}
	var member Member
	if err := decodeExact(raw, MaxMemberBytes, &member); err != nil || validateMember(member) != nil ||
		member.Language != receipt.Language || member.Protocol != receipt.Protocol ||
		member.Namespace != receipt.Namespace ||
		len(member.Records) != receipt.RecordCount || member.Digest != receipt.ContentDigest {
		return Member{}, nil, fmt.Errorf("%w: member", ErrInvalid)
	}
	canonical, err := json.Marshal(member)
	if err != nil || !slices.Equal(raw, canonical) {
		return Member{}, nil, fmt.Errorf("%w: member canonical bytes", ErrInvalid)
	}
	return member, raw, nil
}

func repositoryRoot(root, repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return filepath.Join(root, "resolver-namespaces", hex.EncodeToString(sum[:]))
}

func ensureRepositoryDirectory(root, repository string) error {
	if err := validateDirectory(root); err != nil {
		return err
	}
	namespaceRoot := filepath.Join(root, "resolver-namespaces")
	if err := os.Mkdir(namespaceRoot, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := validateDirectory(namespaceRoot); err != nil {
		return err
	}
	base := repositoryRoot(root, repository)
	if err := os.Mkdir(base, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validateDirectory(base)
}

func validateRepositoryDirectory(root, repository string) error {
	if err := validateDirectory(root); err != nil {
		return err
	}
	if err := validateDirectory(filepath.Join(root, "resolver-namespaces")); err != nil {
		return err
	}
	return validateDirectory(repositoryRoot(root, repository))
}

func validateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: directory is not a real directory", ErrInvalid)
	}
	return nil
}

func generationPath(root, repository, digest string) string {
	return filepath.Join(repositoryRoot(root, repository), "generation-"+strings.TrimPrefix(digest, "sha256:"))
}

func writeMarker(base string, pointer Pointer) error {
	value := marker{Schema: MarkerSchema, Pointer: pointer}
	digest, err := digestValue(value)
	if err != nil {
		return err
	}
	value.Digest = digest
	return writeAtomic(filepath.Join(base, "publishing.json"), value)
}

func writePointer(base string, pointer Pointer) error {
	return writeAtomic(filepath.Join(base, "current.json"), pointer)
}

func removeMarker(base string) error {
	err := os.Remove(filepath.Join(base, "publishing.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(base)
}

func writeAtomic(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".control-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	failed := true
	defer func() {
		_ = temporary.Close()
		if failed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	failed = false
	return syncDirectory(filepath.Dir(path))
}

func readRegular(path string, limit int) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > int64(limit) {
		return nil, fmt.Errorf("%w: non-regular or oversized file", ErrInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() > int64(limit) {
		return nil, fmt.Errorf("%w: file identity changed", ErrInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	afterOpen, statErr := file.Stat()
	afterPath, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || len(raw) > limit ||
		!os.SameFile(opened, afterOpen) || !os.SameFile(opened, afterPath) ||
		afterPath.Mode()&os.ModeSymlink != 0 || int64(len(raw)) != afterOpen.Size() ||
		opened.Size() != afterOpen.Size() || !opened.ModTime().Equal(afterOpen.ModTime()) {
		return nil, fmt.Errorf("%w: file changed while read", ErrInvalid)
	}
	return raw, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	return errors.Join(syncErr, directory.Close())
}

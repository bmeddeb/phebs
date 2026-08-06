package rpccallerposting

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

type Publication struct {
	directory string
	rootValue Root
}

func (prepared *Prepared) Publish(ctx context.Context) (*Publication, error) {
	if prepared == nil || prepared.closed {
		return nil, errors.New("RPC caller posting stage is closed")
	}
	target := generationDirectory(prepared.root, prepared.repository, prepared.rootValue.GenerationDigest)
	if _, err := os.Lstat(target); err == nil {
		if _, openErr := openDirectory(ctx, target, prepared.rootValue, true); openErr != nil {
			return nil, fmt.Errorf("existing RPC posting generation conflicts: %w", openErr)
		}
		if err := os.RemoveAll(prepared.directory); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if err := os.Rename(prepared.directory, target); err != nil {
		return nil, err
	}
	prepared.closed = true
	if err := syncDirectory(publicationRepository(prepared.root, prepared.repository)); err != nil {
		return nil, err
	}
	return openDirectory(ctx, target, prepared.rootValue, true)
}

// Open opens exact immutable root controls without scanning members. A keyed
// read validates only its selected member; the complete pass already occurred
// before Publish returned.
func Open(ctx context.Context, root string, expected Root) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRoot(expected); err != nil {
		return nil, err
	}
	directory := generationDirectory(root, expected.Authority.Repository, expected.GenerationDigest)
	raw, err := readRegular(filepath.Join(directory, "root.json"), MaxRootBytes)
	if err != nil {
		return nil, err
	}
	var value Root
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil || validateRoot(value) != nil ||
		value.Digest != expected.Digest || value.GenerationDigest != expected.GenerationDigest {
		return nil, fmt.Errorf("%w: root control", ErrInvalid)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: root canonical bytes", ErrInvalid)
	}
	return openDirectory(ctx, directory, value, false)
}

func (publication *Publication) Root() Root {
	if publication == nil {
		return Root{}
	}
	value := publication.rootValue
	value.Members = slices.Clone(value.Members)
	return value
}

// WalkPostings validates the complete immutable member inventory while
// yielding cloned postings. A later member failure invalidates the whole walk;
// callers must not publish visitor side effects until WalkPostings succeeds.
func (publication *Publication) WalkPostings(
	ctx context.Context,
	visit func(Posting) error,
) error {
	if publication == nil || visit == nil {
		return fmt.Errorf("%w: posting visitor", ErrInvalid)
	}
	entries, err := os.ReadDir(publication.directory)
	if err != nil || len(entries) != len(publication.rootValue.Members)+1 {
		return fmt.Errorf("%w: generation inventory", ErrInvalid)
	}
	wanted := map[string]struct{}{"root.json": {}}
	var postingCount, resolvedCount, nameMatchCount, unresolvedCount int
	var encodedBytes int64
	for _, receipt := range publication.rootValue.Members {
		wanted[receipt.Name] = struct{}{}
		member, err := publication.openMember(ctx, receipt)
		if err != nil {
			return err
		}
		postingCount += len(member.Postings)
		encodedBytes += receipt.ContentBytes
		for _, posting := range member.Postings {
			switch posting.Class {
			case "resolved":
				resolvedCount++
			case "name_match":
				nameMatchCount++
			case "unresolved":
				unresolvedCount++
			}
			cloned := clonePostings([]Posting{posting})[0]
			if err := visit(cloned); err != nil {
				return err
			}
		}
	}
	if postingCount != publication.rootValue.PostingCount ||
		resolvedCount != publication.rootValue.ResolvedCount ||
		nameMatchCount != publication.rootValue.NameMatchCount ||
		unresolvedCount != publication.rootValue.UnresolvedCount ||
		encodedBytes != publication.rootValue.EncodedMemberBytes {
		return fmt.Errorf("%w: posting walk totals", ErrInvalid)
	}
	for _, entry := range entries {
		if _, present := wanted[entry.Name()]; !present || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: unexpected generation entry", ErrInvalid)
		}
	}
	return ctx.Err()
}

func (publication *Publication) ReadOperation(
	ctx context.Context,
	protocol, operation string,
) ([]Posting, error) {
	if !validText(operation) {
		return nil, fmt.Errorf("%w: operation lookup", ErrInvalid)
	}
	return publication.readSelected(ctx, protocol, partitionBucket(operation), func(value Posting) bool {
		return value.LookupOperation == operation
	})
}

func (publication *Publication) ReadUnresolved(
	ctx context.Context,
	protocol string,
) ([]Posting, error) {
	return publication.readSelected(
		ctx, protocol, partitionBucket("__unresolved__"),
		func(value Posting) bool { return value.Class == "unresolved" },
	)
}

func (publication *Publication) readSelected(
	ctx context.Context,
	protocol string,
	bucket int,
	selectPosting func(Posting) bool,
) ([]Posting, error) {
	if publication == nil || (protocol != "grpc" && protocol != "thrift") || selectPosting == nil {
		return nil, fmt.Errorf("%w: posting lookup", ErrInvalid)
	}
	key := memberKey(protocol, bucket)
	index, found := slices.BinarySearchFunc(
		publication.rootValue.Members, key,
		func(value MemberReceipt, wanted string) int {
			return strings.Compare(memberKey(value.Protocol, value.Bucket), wanted)
		},
	)
	if !found {
		return []Posting{}, nil
	}
	member, err := publication.openMember(ctx, publication.rootValue.Members[index])
	if err != nil {
		return nil, err
	}
	result := []Posting{}
	for _, posting := range member.Postings {
		if selectPosting(posting) {
			result = append(result, posting)
		}
	}
	return clonePostings(result), nil
}

func openDirectory(
	ctx context.Context,
	directory string,
	expected Root,
	complete bool,
) (*Publication, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: generation directory", ErrInvalid)
	}
	publication := &Publication{directory: directory, rootValue: expected}
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
		return nil, fmt.Errorf("%w: complete root", ErrInvalid)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: complete root bytes", ErrInvalid)
	}
	publication.rootValue = value
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(value.Members)+1 {
		return nil, fmt.Errorf("%w: generation inventory", ErrInvalid)
	}
	wanted := map[string]struct{}{"root.json": {}}
	var postingCount, resolvedCount, nameMatchCount, unresolvedCount int
	var encodedBytes int64
	for _, receipt := range value.Members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wanted[receipt.Name] = struct{}{}
		member, err := publication.openMember(ctx, receipt)
		if err != nil {
			return nil, err
		}
		postingCount += len(member.Postings)
		encodedBytes += receipt.ContentBytes
		for _, posting := range member.Postings {
			switch posting.Class {
			case "resolved":
				resolvedCount++
			case "name_match":
				nameMatchCount++
			case "unresolved":
				unresolvedCount++
			}
		}
	}
	if postingCount != value.PostingCount || resolvedCount != value.ResolvedCount ||
		nameMatchCount != value.NameMatchCount || unresolvedCount != value.UnresolvedCount ||
		encodedBytes != value.EncodedMemberBytes {
		return nil, fmt.Errorf("%w: complete generation totals", ErrInvalid)
	}
	for _, entry := range entries {
		if _, present := wanted[entry.Name()]; !present || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: unexpected generation entry", ErrInvalid)
		}
	}
	return publication, nil
}

func (publication *Publication) openMember(
	ctx context.Context,
	receipt MemberReceipt,
) (Member, error) {
	if err := ctx.Err(); err != nil {
		return Member{}, err
	}
	raw, err := readRegular(filepath.Join(publication.directory, receipt.Name), MaxMemberBytes)
	if err != nil || int64(len(raw)) != receipt.ContentBytes {
		return Member{}, fmt.Errorf("%w: member bytes", ErrInvalid)
	}
	var value Member
	if err := decodeExact(raw, MaxMemberBytes, &value); err != nil || validateMember(value) != nil ||
		value.Protocol != receipt.Protocol || value.Bucket != receipt.Bucket ||
		len(value.Postings) != receipt.PostingCount || value.Digest != receipt.ContentDigest {
		return Member{}, fmt.Errorf("%w: member", ErrInvalid)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !slices.Equal(raw, canonical) {
		return Member{}, fmt.Errorf("%w: member canonical bytes", ErrInvalid)
	}
	return value, nil
}

func publicationRepository(root, repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return filepath.Join(root, "rpc-caller-postings", hex.EncodeToString(sum[:]))
}

func generationDirectory(root, repository, generation string) string {
	return filepath.Join(publicationRepository(root, repository), strings.TrimPrefix(generation, "sha256:"))
}

func ensureRoot(root string) error {
	if err := validateDirectory(root); err != nil {
		return err
	}
	base := filepath.Join(root, "rpc-caller-postings")
	if err := os.Mkdir(base, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validateDirectory(base)
}

func validateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: real directory required", ErrInvalid)
	}
	return nil
}

func writeExclusive(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

func readRegular(path string, limit int) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > int64(limit) {
		return nil, fmt.Errorf("%w: regular bounded file required", ErrInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: file identity changed", ErrInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	afterOpen, openErr := file.Stat()
	afterPath, pathErr := os.Lstat(path)
	if openErr != nil || pathErr != nil || len(raw) > limit ||
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

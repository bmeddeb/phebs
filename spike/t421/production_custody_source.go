package t421

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" // Git pack/index format mandates SHA-1.
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const productionSourceLeaseName = ".t422-source.lock"

func (custody *ExecutionProductionCustody) admitSource(ctx context.Context) error {
	// Capture control identities before Git reads their semantics. The final
	// metadata check rejects replacement or edits during this bounded census.
	for _, name := range []string{"config", "HEAD", "refs/heads/main"} {
		path := filepath.Join(custody.request.SourceRepository, name)
		file, err := t4013.OpenHostImage(path)
		if err != nil {
			return ErrExecutionProductionCustody
		}
		info, err := file.Stat()
		if err != nil || !inputCustodyOwned(info) || !info.Mode().IsRegular() || info.Size() > 4096 {
			_ = file.Close()
			return ErrExecutionProductionCustody
		}
		custody.controls = append(custody.controls, productionSourceControl{path: path, file: file, info: info})
	}
	packs, err := inspectProductionSourceFiles(ctx, custody.request.SourceRepository)
	if err != nil || custody.admitSourcePack(ctx, packs) != nil {
		return ErrExecutionProductionCustody
	}
	identity, git, err := custody.request.Git.Check(ctx)
	if err != nil {
		return ErrExecutionProductionCustody
	}
	origin := executionCheckoutInspector{root: custody.request.SourceRepository, git: git, digest: identity.SHA256, custody: custody.request.Git}
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "--is-bare-repository"}, "true\n"},
		{[]string{"rev-parse", "--verify", "HEAD"}, custody.request.SourceCommit + "\n"},
		{[]string{"symbolic-ref", "HEAD"}, "refs/heads/main\n"},
		{[]string{"for-each-ref", "--count=2", "--format=%(refname) %(objectname)"}, "refs/heads/main " + custody.request.SourceCommit + "\n"},
	} {
		raw, err := origin.run(ctx, 4096, check.args...)
		if err != nil || string(raw) != check.want {
			return ErrExecutionProductionCustody
		}
	}
	rawConfig, err := origin.run(ctx, 4096, "config", "--local", "--no-includes", "--null", "--list")
	if err != nil || !validProductionSourceConfig(rawConfig) {
		return ErrExecutionProductionCustody
	}
	commit, err := origin.run(ctx, 16<<10, "cat-file", "commit", custody.request.SourceCommit)
	if err != nil || gitSHA1ObjectID("commit", commit) != custody.request.SourceCommit {
		return ErrExecutionProductionCustody
	}
	header, _, ok := strings.Cut(string(commit), "\n\n")
	if !ok || strings.Contains("\n"+header, "\nparent ") {
		return ErrExecutionProductionCustody
	}
	raw, err := origin.run(ctx, 32<<10, "ls-tree", "-rz", "--full-tree", "HEAD")
	if err != nil {
		return ErrExecutionProductionCustody
	}
	entries, err := executionCheckoutEntries(raw)
	if err != nil || len(entries) == 0 || len(entries) > 64 {
		return ErrExecutionProductionCustody
	}
	var input strings.Builder
	for _, entry := range entries {
		input.WriteString(entry.object)
		input.WriteByte('\n')
	}
	batch, err := origin.runInput(ctx, strings.NewReader(input.String()), (1<<20)+8192, "cat-file", "--batch")
	if err != nil || validateProductionSourceBlobs(entries, batch) != nil {
		return ErrExecutionProductionCustody
	}
	return nil
}

func inspectProductionSourceFiles(ctx context.Context, path string) (_ []string, retErr error) {
	source, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrExecutionProductionCustody
	}
	defer func() {
		if source.Close() != nil {
			retErr = ErrExecutionProductionCustody
		}
	}()
	// ponytail: a flat 512-entry queue is ample for the admitted 64-blob cold
	// source; keep this small admission census separate from the SDK walker.
	pending := []string{"."}
	count := 1
	var total int64
	var packs []string
	for index := 0; index < len(pending); index++ {
		if ctx.Err() != nil {
			return nil, ErrExecutionProductionCustody
		}
		directory, err := source.Open(pending[index])
		if err != nil {
			return nil, ErrExecutionProductionCustody
		}
		rows, readErr := directory.ReadDir(513 - count)
		closeErr := directory.Close()
		count += len(rows)
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || count > 512 {
			return nil, ErrExecutionProductionCustody
		}
		for _, row := range rows {
			name := filepath.Join(pending[index], row.Name())
			info, err := source.Lstat(name)
			if ctx.Err() != nil || err != nil || len(name) > 4096 || !inputCustodyOwned(info) || info.Size() < 0 {
				return nil, ErrExecutionProductionCustody
			}
			if info.IsDir() {
				if !validProductionSourceDirectory(name) {
					return nil, ErrExecutionProductionCustody
				}
				pending = append(pending, name)
				continue
			}
			if !info.Mode().IsRegular() || info.Size() > (2<<20)-total {
				return nil, ErrExecutionProductionCustody
			}
			total += info.Size()
			if name == "HEAD" || name == "config" || name == "description" || name == "refs/heads/main" {
				continue
			}
			parts := strings.Split(filepath.ToSlash(name), "/")
			if len(parts) == 3 && parts[0] == "objects" && parts[1] == "pack" {
				base, extension := strings.TrimSuffix(parts[2], filepath.Ext(parts[2])), filepath.Ext(parts[2])
				if !strings.HasPrefix(base, "pack-") || !validCommit(strings.TrimPrefix(base, "pack-")) ||
					(extension != ".pack" && extension != ".idx") || len(packs) >= 2 {
					return nil, ErrExecutionProductionCustody
				}
				packs = append(packs, name)
				continue
			}
			if len(parts) != 3 || parts[0] != "objects" || len(parts[1]) != 2 || len(parts[2]) != 38 || !validCommit(parts[1]+parts[2]) {
				return nil, ErrExecutionProductionCustody
			}
		}
	}
	return packs, nil
}

func (custody *ExecutionProductionCustody) admitSourcePack(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if len(paths) != 2 || strings.TrimSuffix(paths[0], filepath.Ext(paths[0])) != strings.TrimSuffix(paths[1], filepath.Ext(paths[1])) ||
		filepath.Ext(paths[0]) == filepath.Ext(paths[1]) {
		return ErrExecutionProductionCustody
	}
	for _, relative := range paths {
		path := filepath.Join(custody.request.SourceRepository, relative)
		file, err := t4013.OpenHostImage(path)
		if err != nil {
			return ErrExecutionProductionCustody
		}
		info, err := file.Stat()
		if err != nil || !inputCustodyOwned(info) || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			_ = file.Close()
			return ErrExecutionProductionCustody
		}
		custody.controls = append(custody.controls, productionSourceControl{path: path, file: file, info: info})
		raw, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
		if err != nil || ctx.Err() != nil || len(raw) < 40 || len(raw) > 2<<20 || int64(len(raw)) != info.Size() {
			return ErrExecutionProductionCustody
		}
		digest := sha1.Sum(raw[:len(raw)-sha1.Size])
		if !bytes.Equal(digest[:], raw[len(raw)-sha1.Size:]) {
			return ErrExecutionProductionCustody
		}
		nameDigest := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(relative), "pack-"), filepath.Ext(relative))
		if filepath.Ext(relative) == ".pack" {
			if string(raw[:4]) != "PACK" || binary.BigEndian.Uint32(raw[4:8]) != 2 ||
				binary.BigEndian.Uint32(raw[8:12]) == 0 || binary.BigEndian.Uint32(raw[8:12]) > 512 || hex.EncodeToString(digest[:]) != nameDigest {
				return ErrExecutionProductionCustody
			}
		} else if len(raw) < 8+256*4+40 || string(raw[:4]) != "\xfftOc" || binary.BigEndian.Uint32(raw[4:8]) != 2 ||
			binary.BigEndian.Uint32(raw[8+255*4:8+256*4]) == 0 || binary.BigEndian.Uint32(raw[8+255*4:8+256*4]) > 512 ||
			hex.EncodeToString(raw[len(raw)-40:len(raw)-20]) != nameDigest {
			return ErrExecutionProductionCustody
		}
	}
	return nil
}

func validProductionSourceDirectory(path string) bool {
	switch path {
	case "objects", "objects/info", "objects/pack", "refs", "refs/heads", "refs/tags":
		return true
	default:
		return len(path) == len("objects/00") && strings.HasPrefix(path, "objects/") && validCommit(path[len("objects/"):]+strings.Repeat("0", 38))
	}
}

func validProductionSourceConfig(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 4096 || raw[len(raw)-1] != 0 {
		return false
	}
	seen := map[string]bool{}
	for _, row := range strings.Split(string(raw[:len(raw)-1]), "\x00") {
		key, value, ok := strings.Cut(row, "\n")
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		switch key {
		case "core.repositoryformatversion":
			if value != "0" {
				return false
			}
		case "core.bare", "core.filemode":
			if value != "true" {
				return false
			}
		case "core.ignorecase", "core.precomposeunicode":
			if value != "true" && value != "false" {
				return false
			}
		default:
			return false
		}
	}
	return seen["core.repositoryformatversion"] && seen["core.bare"] && seen["core.filemode"]
}

func validateProductionSourceBlobs(entries []executionCheckoutEntry, raw []byte) error {
	reader := bufio.NewReader(bytes.NewReader(raw))
	var total int64
	for _, entry := range entries {
		header, err := reader.ReadString('\n')
		parts := strings.Fields(header)
		if err != nil || len(header) > 128 || len(parts) != 3 || parts[0] != entry.object || parts[1] != "blob" {
			return ErrExecutionProductionCustody
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || size < 0 || size > (1<<20)-total || strconv.FormatInt(size, 10) != parts[2] {
			return ErrExecutionProductionCustody
		}
		total += size
		blob := make([]byte, int(size))
		if _, err := io.ReadFull(reader, blob); err != nil || gitSHA1ObjectID("blob", blob) != entry.object {
			return ErrExecutionProductionCustody
		}
		if delimiter, err := reader.ReadByte(); err != nil || delimiter != '\n' {
			return ErrExecutionProductionCustody
		}
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return ErrExecutionProductionCustody
	}
	return nil
}

func protectProductionConfig(ctx context.Context, parent string, raw []byte) (_ *ExecutionInputCustody, retErr error) {
	writer, err := os.CreateTemp(parent, ".t422-config-")
	if err != nil {
		return nil, ErrExecutionProductionCustody
	}
	path := writer.Name()
	n, writeErr := writer.Write(raw)
	info, statErr := writer.Stat()
	closeErr := writer.Close()
	defer func() {
		current, err := os.Lstat(path)
		if err != nil || info == nil || !os.SameFile(info, current) || os.Remove(path) != nil {
			retErr = ErrExecutionProductionCustody
		}
	}()
	if ctx.Err() != nil || writeErr != nil || statErr != nil || closeErr != nil || n != len(raw) {
		return nil, ErrExecutionProductionCustody
	}
	return ProtectExecutionInputs(ctx, parent, []ExecutionInputCopy{{Name: "config", Path: path, SHA256: SHA256(raw)}})
}

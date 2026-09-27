package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"golang.org/x/sys/unix"
)

// These bounds admit only the pinned release (1,224 entries, 9,985,804
// uncompressed bytes). They do not authorize a different rules_go release.
const maxRulesEntries = 1300
const maxRulesBytes = 16 << 20
const maxRulesFileBytes = 8 << 20

type rulesMember struct {
	directory bool
	bytes     int64
	digest    string
}

func verifyRules(ctx context.Context, inv typedindex.Inventory) error {
	b, e := readInventory(inv, "tools/cache/repository/content_addressable/sha256/"+rulesArchive+"/file", rulesFiles[0].bytes)
	if e != nil || hash(b) != "sha256:"+rulesArchive {
		return typedindex.Unprepared
	}
	module, e := readInventory(inv, "tools/cache/repository/content_addressable/sha256/"+rulesFiles[2].digest+"/file", rulesFiles[2].bytes)
	if e != nil || hash(module) != "sha256:"+rulesFiles[2].digest {
		return typedindex.Unprepared
	}
	want, e := rulesManifest(ctx, b, module)
	if e != nil {
		return e
	}
	return verifyRulesTree(ctx, "/scratch/bazel-output/external/rules_go+", want)
}

func rulesManifest(ctx context.Context, archive, patchedModule []byte) (map[string]rulesMember, error) {
	z, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if e != nil || len(z.File) == 0 || len(z.File) > maxRulesEntries {
		return nil, typedindex.Unsupported
	}
	want := make(map[string]rulesMember, len(z.File))
	var total uint64
	moduleSeen := false
	for _, f := range z.File {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		name := strings.TrimSuffix(f.Name, "/")
		if !safeRelative(name) || name == "." {
			return nil, typedindex.Unsupported
		}
		if _, exists := want[name]; exists {
			return nil, typedindex.Unsupported
		}
		if f.FileInfo().IsDir() {
			if f.Mode().Type() != os.ModeDir || f.UncompressedSize64 != 0 {
				return nil, typedindex.Unsupported
			}
			want[name] = rulesMember{directory: true}
			continue
		}
		if !f.Mode().IsRegular() || f.UncompressedSize64 > maxRulesFileBytes || f.UncompressedSize64 > maxRulesBytes-total {
			return nil, typedindex.Unsupported
		}
		total += f.UncompressedSize64
		r, err := f.Open()
		if err != nil {
			return nil, typedindex.Unsupported
		}
		b, err := io.ReadAll(io.LimitReader(r, int64(f.UncompressedSize64)+1))
		err = errors.Join(err, r.Close())
		if err != nil || uint64(len(b)) != f.UncompressedSize64 {
			return nil, typedindex.Unsupported
		}
		if name == "MODULE.bazel" {
			old := []byte("    repo_name = \"io_bazel_rules_go\",\n")
			if bytes.Count(b, old) != 1 {
				return nil, typedindex.Unsupported
			}
			b = bytes.Replace(b, old, append(bytes.Clone(old), []byte("    version = \"0.59.0\",\n")...), 1)
			if !bytes.Equal(b, patchedModule) {
				return nil, typedindex.Unsupported
			}
			moduleSeen = true
		}
		want[name] = rulesMember{bytes: int64(len(b)), digest: hash(b)}
	}
	if !moduleSeen {
		return nil, typedindex.Unsupported
	}
	// ZIPs may omit directory entries, but every actual directory must still be
	// accounted for and may not alias a regular file.
	for name := range want {
		for p := path.Dir(name); p != "."; p = path.Dir(p) {
			if old, ok := want[p]; ok && !old.directory {
				return nil, typedindex.Unsupported
			}
			want[p] = rulesMember{directory: true}
			if len(want) > maxRulesEntries {
				return nil, typedindex.Capacity
			}
		}
	}
	return want, nil
}

func verifyRulesTree(ctx context.Context, name string, want map[string]rulesMember) error {
	before, e := os.Lstat(name)
	if e != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return typedindex.Unsupported
	}
	r, e := os.OpenRoot(name)
	if e != nil {
		return typedindex.Unsupported
	}
	defer func() { _ = r.Close() }()
	rootStat, e := r.Stat(".")
	if e != nil || !os.SameFile(before, rootStat) {
		return typedindex.Stale
	}
	stack := []string{"."}
	seen := 0
	for len(stack) != 0 {
		if e = ctx.Err(); e != nil {
			return e
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		f, err := r.OpenFile(dir, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
		if err != nil {
			return typedindex.Unsupported
		}
		err = func() error {
			defer func() { _ = f.Close() }()
			for {
				entries, readErr := f.ReadDir(64)
				for _, entry := range entries {
					if e := ctx.Err(); e != nil {
						return e
					}
					seen++
					if seen > len(want) || seen > maxRulesEntries {
						return typedindex.Unsupported
					}
					p := path.Join(dir, entry.Name())
					m, ok := want[p]
					if !ok || entry.Type()&os.ModeSymlink != 0 {
						return typedindex.Unsupported
					}
					st, e := r.Lstat(p)
					if e != nil {
						return typedindex.Unsupported
					}
					if m.directory {
						if !st.IsDir() {
							return typedindex.Unsupported
						}
						stack = append(stack, p)
					} else if e = verifyRulesFile(r, p, st, m); e != nil {
						return e
					}
				}
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				if readErr != nil {
					return typedindex.Unsupported
				}
			}
		}()
		if err != nil {
			return err
		}
	}
	after, e := os.Lstat(name)
	if e != nil || !os.SameFile(before, after) || seen != len(want) {
		return typedindex.Stale
	}
	return nil
}

func verifyRulesFile(root *os.Root, name string, before os.FileInfo, want rulesMember) error {
	if !before.Mode().IsRegular() || before.Size() != want.bytes {
		return typedindex.Unsupported
	}
	f, e := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if e != nil {
		return typedindex.Unsupported
	}
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil || !os.SameFile(before, st) {
		return typedindex.Stale
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, want.bytes+1))
	after, statErr := f.Stat()
	named, nameErr := root.Lstat(name)
	if e != nil || statErr != nil || nameErr != nil || !os.SameFile(st, named) || after.Size() != st.Size() || !after.ModTime().Equal(st.ModTime()) || n != want.bytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != want.digest {
		return typedindex.Stale
	}
	return nil
}

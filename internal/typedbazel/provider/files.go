package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"embed"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

//go:embed bootstrap/*
var bootstrap embed.FS

func readBounded(name string, limit int64) ([]byte, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() < 0 || st.Size() > limit {
		return nil, typedindex.Invalid
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) != st.Size() {
		return nil, typedindex.Invalid
	}
	return b, nil
}
func safeRelative(s string) bool {
	return s != "" && !strings.HasPrefix(s, "/") && path.Clean(s) == s && s != ".." && !strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\\\x00\r\n")
}
func closedFile(name string, b []byte) error { return writeFile(name, b, 0400) }
func writeFile(name string, b []byte, mode os.FileMode) error {
	f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	return errors.Join(e, f.Close())
}
func inventoryFile(inv typedindex.Inventory, name string) (typedindex.BundleFile, error) {
	if f, ok := inv.File(name); ok {
		return f, nil
	}
	return typedindex.BundleFile{}, typedindex.Unprepared
}
func readInventory(inv typedindex.Inventory, name string, limit int64) ([]byte, error) {
	f, e := inventoryFile(inv, name)
	if e != nil {
		return nil, typedindex.Unprepared
	}
	return readInventoryEntry(f, limit)
}
func readInventoryEntry(f typedindex.BundleFile, limit int64) ([]byte, error) {
	if f.Bytes > limit {
		return nil, typedindex.Unprepared
	}
	b, e := readBounded("/inputs/"+f.Path, limit)
	if e != nil || int64(len(b)) != f.Bytes || hash(b) != f.Digest {
		return nil, typedindex.Unprepared
	}
	return b, nil
}

type original struct {
	Path   string
	Bytes  int64
	Digest string
}

func materializeSource(ctx context.Context, i Invocation) ([]original, error) {
	var originals []original
	var total int64
	for _, f := range i.Inventory.Files() {
		if !strings.HasPrefix(f.Path, "source/") {
			continue
		}
		name := strings.TrimPrefix(f.Path, "source/")
		if !safeRelative(name) || name == "phebs_plan" || strings.HasPrefix(name, "phebs_plan/") || name == "phebs_excluded" || strings.HasPrefix(name, "phebs_excluded/") || f.Bytes > planner.MaxFileBytes || f.Bytes > planner.MaxProtoBytes-total {
			return nil, typedindex.Invalid
		}
		total += f.Bytes
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		b, e := readInventoryEntry(f, planner.MaxFileBytes)
		if e != nil {
			return nil, e
		}
		dest := path.Join(launcher.Workspace, name)
		if e = os.MkdirAll(path.Dir(dest), 0700); e != nil {
			return nil, e
		}
		mode := os.FileMode(0600)
		if f.Executable {
			mode = 0700
		}
		if e = writeFile(dest, b, mode); e != nil {
			return nil, e
		}
		originals = append(originals, original{name, f.Bytes, f.Digest})
	}
	if len(originals) == 0 {
		return nil, typedindex.Unprepared
	}
	return originals, nil
}

func materialize(ctx context.Context, i Invocation) ([]original, error) {
	originals, e := materializeSource(ctx, i)
	if e != nil {
		return nil, e
	}
	for _, name := range []string{"MODULE.bazel", "MODULE.bazel.lock", "go.mod"} {
		found := false
		for _, f := range originals {
			if f.Path == name {
				found = true
			}
		}
		if !found {
			return nil, typedindex.Unsupported
		}
	}
	helper, e := readInventory(i.Inventory, typedindex.ManagedHelperFile, typedindex.MaxFileBytes)
	if e != nil {
		return nil, e
	}
	aspect, _ := bootstrap.ReadFile("bootstrap/aspect.bzl")
	build, _ := bootstrap.ReadFile("bootstrap/BUILD.bazel")
	for _, f := range []struct {
		name string
		b    []byte
		mode os.FileMode
	}{{"phebs_plan/aspect.bzl", aspect, 0600}, {"phebs_plan/BUILD.bazel", build, 0600}, {"phebs_plan/t451a", helper, 0500}, {"phebs_excluded/BUILD.bazel", []byte("genrule(name = \"broken\", outs = [\"sentinel\"], cmd = \"touch /scratch/t451b-excluded-sentinel; touch $@; exit 1\")\n"), 0600}} {
		dest := path.Join(launcher.Workspace, f.name)
		if e = os.MkdirAll(path.Dir(dest), 0700); e != nil {
			return nil, e
		}
		if e = writeFile(dest, f.b, f.mode); e != nil {
			return nil, e
		}
		originals = append(originals, original{f.name, int64(len(f.b)), hash(f.b)})
	}
	return originals, nil
}
func verifyWorkspace(ctx context.Context, p planner.Plan, originals []original) error {
	for _, f := range originals {
		if e := ctx.Err(); e != nil {
			return e
		}
		name := path.Join(launcher.Workspace, f.Path)
		st, e := os.Lstat(name)
		if e != nil || !st.Mode().IsRegular() {
			return typedindex.Invalid
		}
		b, e := readBounded(name, f.Bytes)
		if e != nil || int64(len(b)) != f.Bytes || hash(b) != f.Digest {
			return typedindex.Invalid
		}
	}
	for _, t := range p.Targets {
		if strings.HasPrefix(t.Label, "@@//phebs_excluded:") {
			return typedindex.Invalid
		}
	}
	if _, e := os.Lstat("/scratch/t451b-excluded-sentinel"); !errors.Is(e, os.ErrNotExist) {
		return typedindex.Invalid
	}
	return nil
}

const compilerScriptArm64 = `#!/bin/sh
export GCC_EXEC_PREFIX=/scratch/toolchain/usr/lib/gcc/
export LIBRARY_PATH=/scratch/toolchain/usr/lib/aarch64-linux-gnu:/scratch/toolchain/lib/aarch64-linux-gnu
export LD_LIBRARY_PATH=/scratch/toolchain/usr/lib/aarch64-linux-gnu:/scratch/toolchain/lib/aarch64-linux-gnu
exec /scratch/toolchain/usr/bin/aarch64-linux-gnu-gcc-12 --sysroot=/scratch/toolchain -B/scratch/toolchain/usr/bin/ -B/scratch/toolchain/usr/lib/gcc/aarch64-linux-gnu/12/ "$@"
`

const compilerScriptAmd64 = `#!/bin/sh
export GCC_EXEC_PREFIX=/scratch/toolchain/usr/lib/gcc/
export LIBRARY_PATH=/scratch/toolchain/usr/lib/x86_64-linux-gnu:/scratch/toolchain/lib/x86_64-linux-gnu
export LD_LIBRARY_PATH=/scratch/toolchain/usr/lib/x86_64-linux-gnu:/scratch/toolchain/lib/x86_64-linux-gnu
exec /scratch/toolchain/usr/bin/x86_64-linux-gnu-gcc-13 --sysroot=/scratch/toolchain -B/scratch/toolchain/usr/bin/ -B/scratch/toolchain/usr/lib/gcc/x86_64-linux-gnu/13/ -B/scratch/toolchain/usr/libexec/gcc/x86_64-linux-gnu/13/ "$@"
`

const compilerDigestArm64 = "sha256:b5bc6148e85dc7f96e9a756f407aac10893cf8327668e4e22f5f4263454cb5c4"
const compilerDigestAmd64 = "sha256:2f2ec79d40bb602c2c957ffa2f4be62ec2709a53e28060c57e5d5664a7f8dcde"

func compilerScriptFor(arch string) (string, bool) {
	switch arch {
	case "arm64":
		return compilerScriptArm64, true
	case "amd64":
		return compilerScriptAmd64, true
	default:
		return "", false
	}
}

func compilerDigestFor(arch string) (string, bool) {
	switch arch {
	case "arm64":
		return compilerDigestArm64, true
	case "amd64":
		return compilerDigestAmd64, true
	default:
		return "", false
	}
}

func setupCompiler(ctx context.Context, inv typedindex.Inventory) error {
	// Verify the immutable archive before expansion; expansion cannot introduce a
	// link, overwrite, unbounded entry or executable outside private toolchain.
	b, e := readInventory(inv, "tools/cc-sysroot.zip", typedindex.MaxFileBytes)
	if e != nil {
		return e
	}
	r, e := compilerZIP(b)
	if e != nil {
		return e
	}
	return unpackCompiler(ctx, r, "/scratch/toolchain")
}
func compilerZIP(b []byte) (*zip.Reader, error) {
	digest, ok := compilerDigestFor(runtime.GOARCH)
	if !ok || hash(b) != digest {
		return nil, typedindex.Unsupported
	}
	return zip.NewReader(bytes.NewReader(b), int64(len(b)))
}

func unpackCompiler(ctx context.Context, r *zip.Reader, destination string) error {
	if len(r.File) == 0 || len(r.File) > 12000 {
		return typedindex.Capacity
	}
	var total uint64
	previous := ""
	for _, f := range r.File {
		if !safeRelative(f.Name) || f.Name <= previous || !f.Mode().IsRegular() || f.UncompressedSize64 > 128<<20 || f.UncompressedSize64 > 512<<20-total {
			return typedindex.Invalid
		}
		previous = f.Name
		total += f.UncompressedSize64
	}
	for _, f := range r.File {
		if e := ctx.Err(); e != nil {
			return e
		}
		name := filepath.Join(destination, f.Name)
		if e := os.MkdirAll(filepath.Dir(name), 0700); e != nil {
			return e
		}
		mode := os.FileMode(0400)
		if f.Mode().Perm()&0111 != 0 {
			mode = 0500
		}
		out, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if e != nil {
			return e
		}
		in, e := f.Open()
		if e != nil {
			_ = out.Close()
			return e
		}
		n, e := io.Copy(out, io.LimitReader(in, int64(f.UncompressedSize64)+1))
		e = errors.Join(e, in.Close(), out.Close())
		if e != nil || n != int64(f.UncompressedSize64) {
			return typedindex.Invalid
		}
	}
	script, ok := compilerScriptFor(runtime.GOARCH)
	if !ok {
		return typedindex.Unsupported
	}
	return writeFile(filepath.Join(destination, "cc"), []byte(script), 0500)
}

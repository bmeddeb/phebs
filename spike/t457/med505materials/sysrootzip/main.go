// Command sysrootzip serializes a tar of regular files into the deterministic
// ZIP that the amd64 typed-Bazel provider unpacks as tools/cc-sysroot.zip.
//
// The provider admits only sorted, unique, safe relative names of regular
// files, at most 12,000 entries, 128 MiB per file and 512 MiB expanded
// (internal/typedbazel/provider/files.go). This tool enforces the same bounds,
// sorts names, fixes every timestamp and keeps only the executable bit, so the
// same tar and the same Go toolchain always yield the same bytes.
//
// Usage: sysrootzip -in files.tar -out cc-sysroot.zip
package main

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	maxEntries   = 12000
	maxFileBytes = 128 << 20
	maxTotal     = 512 << 20
)

// epoch is the earliest time a ZIP header can carry.
var epoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

type member struct {
	name string
	exec bool
	data []byte
}

func main() {
	in := flag.String("in", "", "input tar of regular files")
	out := flag.String("out", "", "output zip")
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: sysrootzip -in files.tar -out cc-sysroot.zip")
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "sysrootzip:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	members, err := readTar(f)
	if err != nil {
		return err
	}
	w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err = writeZip(w, members); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func safe(name string) bool {
	return name != "" && name != "." && !strings.HasPrefix(name, "/") && path.Clean(name) == name &&
		name != ".." && !strings.HasPrefix(name, "../")
}

func readTar(r io.Reader) ([]member, error) {
	t := tar.NewReader(r)
	seen := map[string]bool{}
	var members []member
	var total int64
	for {
		h, err := t.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%s: not a regular file", name)
		}
		if !safe(name) || seen[name] {
			return nil, fmt.Errorf("%s: unsafe or duplicate name", name)
		}
		if h.Size > maxFileBytes || h.Size > maxTotal-total {
			return nil, fmt.Errorf("%s: size bound", name)
		}
		data, err := io.ReadAll(io.LimitReader(t, h.Size+1))
		if err != nil || int64(len(data)) != h.Size {
			return nil, fmt.Errorf("%s: short read", name)
		}
		seen[name] = true
		total += h.Size
		members = append(members, member{name: name, exec: h.Mode&0o111 != 0, data: data})
		if len(members) > maxEntries {
			return nil, errors.New("entry bound")
		}
	}
	if len(members) == 0 {
		return nil, errors.New("empty input")
	}
	sort.Slice(members, func(i, j int) bool { return members[i].name < members[j].name })
	return members, nil
}

func writeZip(w io.Writer, members []member) error {
	z := zip.NewWriter(w)
	for _, m := range members {
		mode := os.FileMode(0o644)
		if m.exec {
			mode = 0o755
		}
		h := &zip.FileHeader{Name: m.name, Method: zip.Deflate, Modified: epoch}
		h.SetMode(mode)
		fw, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err = fw.Write(m.data); err != nil {
			return err
		}
	}
	return z.Close()
}

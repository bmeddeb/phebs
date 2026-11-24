package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func tarOf(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, h := range entries {
		h := h
		body := []byte(strings.TrimPrefix(h.Name, "./"))
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := w.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := w.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func serialize(t *testing.T, raw []byte) []byte {
	t.Helper()
	members, err := readTar(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = writeZip(&out, members); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestZipIsSortedDeterministicAndKeepsOnlyExecBit(t *testing.T) {
	a := tarOf(t,
		tar.Header{Name: "usr/bin/gcc", Typeflag: tar.TypeReg, Mode: 0o4755},
		tar.Header{Name: "lib/x86_64-linux-gnu/libc.so.6", Typeflag: tar.TypeReg, Mode: 0o644},
	)
	b := tarOf(t,
		tar.Header{Name: "lib/x86_64-linux-gnu/libc.so.6", Typeflag: tar.TypeReg, Mode: 0o600},
		tar.Header{Name: "./usr/bin/gcc", Typeflag: tar.TypeReg, Mode: 0o700},
	)
	za, zb := serialize(t, a), serialize(t, b)
	if !bytes.Equal(za, zb) {
		t.Fatal("same files in a different order or mode bits produced different bytes")
	}
	r, err := zip.NewReader(bytes.NewReader(za), int64(len(za)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.File) != 2 || r.File[0].Name != "lib/x86_64-linux-gnu/libc.so.6" || r.File[1].Name != "usr/bin/gcc" {
		t.Fatalf("names not sorted: %v", r.File)
	}
	if r.File[0].Mode().Perm() != 0o644 || r.File[1].Mode().Perm() != 0o755 || !r.File[1].Mode().IsRegular() {
		t.Fatalf("modes = %v %v", r.File[0].Mode(), r.File[1].Mode())
	}
	if !r.File[0].Modified.Equal(epoch) {
		t.Fatalf("timestamp = %v", r.File[0].Modified)
	}
}

func TestZipRefusesWhatTheProviderRefuses(t *testing.T) {
	for name, raw := range map[string][]byte{
		"symlink":   tarOf(t, tar.Header{Name: "a", Typeflag: tar.TypeSymlink, Linkname: "b"}),
		"directory": tarOf(t, tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}),
		"escape":    tarOf(t, tar.Header{Name: "../x", Typeflag: tar.TypeReg}),
		"absolute":  tarOf(t, tar.Header{Name: "/etc/x", Typeflag: tar.TypeReg}),
		"duplicate": tarOf(t, tar.Header{Name: "a", Typeflag: tar.TypeReg}, tar.Header{Name: "./a", Typeflag: tar.TypeReg}),
		"empty":     tarOf(t),
	} {
		if _, err := readTar(bytes.NewReader(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

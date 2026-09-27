package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var rulesArchiveFlag = flag.String("provider-rules-archive", "", "optional read-only retained rules_go archive proof; runs no native tool")
var rulesModuleFlag = flag.String("provider-rules-module", "", "optional retained registry MODULE bytes")
var compilerArchiveFlag = flag.String("provider-compiler-archive", "", "optional read-only retained compiler archive identity proof; does not extract or execute")

func TestOwnedAspectCanonicalRules(t *testing.T) {
	b, e := bootstrap.ReadFile("bootstrap/aspect.bzl")
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "load(") {
			if !strings.HasPrefix(line, `load("@@rules_go+//`) {
				t.Fatal("apparent repository load", line)
			}
			count++
		}
	}
	if count != 3 {
		t.Fatal("canonical load census", count)
	}
	if _, e = compilerZIP([]byte("an inventory-admitted replacement is insufficient")); e == nil {
		t.Fatal("compiler replacement accepted")
	}
}

func TestRetainedRulesArtifact(t *testing.T) {
	if *rulesArchiveFlag == "" && *rulesModuleFlag == "" {
		t.Skip("explicit retained artifact paths required; no native execution")
	}
	b, e := readBounded(*rulesArchiveFlag, rulesFiles[0].bytes)
	if e != nil || hash(b) != "sha256:"+rulesArchive {
		t.Fatal("archive identity", e)
	}
	m, e := readBounded(*rulesModuleFlag, rulesFiles[2].bytes)
	if e != nil || hash(m) != "sha256:"+rulesFiles[2].digest {
		t.Fatal("module identity", e)
	}
	want, e := rulesManifest(context.Background(), b, m)
	if e != nil {
		t.Fatal(e)
	}
	if len(want) != 1224 {
		t.Fatal("retained tree census", len(want))
	}
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	for _, f := range z.File {
		p := filepath.Join(root, f.Name)
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(p, 0700); e != nil {
				t.Fatal(e)
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, readErr := io.ReadAll(io.LimitReader(r, maxRulesFileBytes+1))
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal(readErr, closeErr)
		}
		if f.Name == "MODULE.bazel" {
			data = m
		}
		if e = os.WriteFile(p, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = verifyRulesTree(context.Background(), root, want); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "unrecognized-bazel-file"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifyRulesTree(context.Background(), root, want); e == nil {
		t.Fatal("unknown generated file accepted")
	}
}

func TestRetainedCompilerArtifact(t *testing.T) {
	if *compilerArchiveFlag == "" {
		t.Skip("explicit retained archive path required; no extraction or native execution")
	}
	b, e := readBounded(*compilerArchiveFlag, 256<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = compilerZIP(b); e != nil {
		t.Fatal(e)
	}
	b[len(b)-1] ^= 1
	if _, e = compilerZIP(b); e == nil {
		t.Fatal("mutated compiler archive accepted")
	}
}

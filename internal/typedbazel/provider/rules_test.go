package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func rulesFixture(t *testing.T) ([]byte, []byte, map[string][]byte) {
	t.Helper()
	module := []byte("module(\n    name = \"rules_go\",\n    repo_name = \"io_bazel_rules_go\",\n)\n")
	patched := bytes.Replace(module, []byte("    repo_name = \"io_bazel_rules_go\",\n"), []byte("    repo_name = \"io_bazel_rules_go\",\n    version = \"0.59.0\",\n"), 1)
	files := map[string][]byte{"MODULE.bazel": module, "go/def.bzl": []byte("pinned code\n")}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range []string{"MODULE.bazel", "go/def.bzl"} {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(files[name]); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	files["MODULE.bazel"] = patched
	return out.Bytes(), patched, files
}

func TestResolvedRulesIdentity(t *testing.T) {
	archive, module, files := rulesFixture(t)
	ctx := context.Background()
	want, e := rulesManifest(ctx, archive, module)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"exact", "override", "extra", "missing", "symlink", "directory-symlink", "root-symlink", "unpatched", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			e = nil
			for name, b := range files {
				p := filepath.Join(root, name)
				if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(p, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			check := ctx
			file := filepath.Join(root, "go/def.bzl")
			switch kind {
			case "override":
				e = os.WriteFile(file, []byte("evil!! code\n"), 0600)
			case "extra":
				e = os.WriteFile(filepath.Join(root, "unexpected"), nil, 0600)
			case "missing":
				e = os.Remove(file)
			case "symlink":
				e = os.Remove(file)
				if e == nil {
					e = os.Symlink("../MODULE.bazel", file)
				}
			case "directory-symlink":
				e = os.Rename(filepath.Join(root, "go"), filepath.Join(root, "alias"))
				if e == nil {
					e = os.Symlink("alias", filepath.Join(root, "go"))
				}
			case "root-symlink":
				alias := filepath.Join(t.TempDir(), "alias")
				e = os.Symlink(root, alias)
				root = alias
			case "unpatched":
				e = os.WriteFile(filepath.Join(root, "MODULE.bazel"), []byte("module(name=\"rules_go\")"), 0600)
			case "canceled":
				var cancel context.CancelFunc
				check, cancel = context.WithCancel(ctx)
				cancel()
			}
			if e != nil {
				t.Fatal(e)
			}
			e = verifyRulesTree(check, root, want)
			if (e == nil) != (kind == "exact") {
				t.Fatalf("%s: %v", kind, e)
			}
		})
	}
	if _, e = rulesManifest(ctx, archive, []byte("different patch")); e == nil {
		t.Fatal("accepted unknown patch")
	}
	if _, e = rulesManifest(ctx, nil, module); e == nil {
		t.Fatal("accepted missing archive")
	}
}

func TestWorkerRulesRechecked(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		i, s, p, m, raw := fixture(t)
		i = executing(t, i, s, p)
		var events []string
		o := neutralOperations(t, s, p, m, raw, &events)
		n := 0
		o.rules = func(context.Context, typedindex.Inventory) error {
			n++
			if n == failAt {
				return typedindex.Unsupported
			}
			return nil
		}
		b, e := run(context.Background(), i, o)
		if e == nil {
			t.Fatal("changed resolved rules accepted", failAt)
		}
		r, e := decode[Result](b, 1<<20)
		if e != nil || r.Failure == nil {
			t.Fatal("lost rules failure", e)
		}
	}
}

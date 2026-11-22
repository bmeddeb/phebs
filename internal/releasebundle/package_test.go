package releasebundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packageFixture(t *testing.T) (root string, opts PackageOptions, signed *[]string) {
	t.Helper()
	root = t.TempDir()
	bundle := filepath.Join(root, "bundle")
	if _, err := Build(baseOptions(bundle, testInputs(t, root))); err != nil {
		t.Fatalf("Build: %v", err)
	}
	templates := filepath.Join(root, "templates")
	artifacts := filepath.Join(root, "artifacts")
	for dir, files := range map[string]map[string]string{
		templates: {"caller-map.json": `{"pack_id":"a"}`, "wire.json": `{"pack_id":"b"}`},
		artifacts: {"card.md": "card", "manifest.json": "{}", "validation.json": "{}"},
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	calls := []string{}
	signed = &calls
	opts = PackageOptions{
		BundleDir:    bundle,
		OutputDir:    filepath.Join(root, "dist", "package"),
		TemplatesDir: templates,
		ArtifactsDir: artifacts,
		Sign: func(_ context.Context, phebs, template, artifactsDir, out string) error {
			if filepath.Base(phebs) != "phebs" || filepath.Base(filepath.Dir(phebs)) != PackageRuntimeDir {
				t.Fatalf("signer ran %s, want the package runtime/phebs", phebs)
			}
			if _, err := os.Stat(filepath.Join(artifactsDir, "card.md")); err != nil {
				t.Fatalf("signer saw no copied artifacts: %v", err)
			}
			calls = append(calls, filepath.Base(template))
			return os.WriteFile(out, []byte("signed "+filepath.Base(template)), 0o644)
		},
	}
	return root, opts, signed
}

func TestPackageLaysOutRuntimeAndSignedPacks(t *testing.T) {
	_, opts, signed := packageFixture(t)
	if err := Package(t.Context(), opts); err != nil {
		t.Fatalf("Package: %v", err)
	}
	if strings.Join(*signed, ",") != "caller-map.json,wire.json" {
		t.Fatalf("signed templates = %v, want both in sorted order", *signed)
	}
	if _, err := Verify(filepath.Join(opts.OutputDir, PackageRuntimeDir)); err != nil {
		t.Fatalf("packaged runtime must still verify: %v", err)
	}
	for _, rel := range []string{
		"packs/artifacts/card.md", "packs/artifacts/manifest.json", "packs/artifacts/validation.json",
		"packs/selection/caller-map.json", "packs/selection/wire.json",
	} {
		if _, err := os.Stat(filepath.Join(opts.OutputDir, rel)); err != nil {
			t.Fatalf("package lacks %s: %v", rel, err)
		}
	}
	info, err := os.Stat(filepath.Join(opts.OutputDir, PackageRuntimeDir, "phebs"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("runtime/phebs must stay executable: %v %v", info, err)
	}
}

func TestPackageRefusesExistingOutput(t *testing.T) {
	_, opts, _ := packageFixture(t)
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Package(t.Context(), opts); err == nil {
		t.Fatal("an existing package output must refuse")
	}
}

func TestPackageLeavesNothingWhenSigningFails(t *testing.T) {
	_, opts, _ := packageFixture(t)
	opts.Sign = func(context.Context, string, string, string, string) error { return errors.New("key unavailable") }
	if err := Package(t.Context(), opts); err == nil {
		t.Fatal("a signing failure must refuse")
	}
	entries, err := os.ReadDir(filepath.Dir(opts.OutputDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a failed package left %d entries behind", len(entries))
	}
}

func TestPackageRefusesTamperedBundle(t *testing.T) {
	_, opts, _ := packageFixture(t)
	appendTamper(t, filepath.Join(opts.BundleDir, "phebs"))
	if err := Package(t.Context(), opts); err == nil {
		t.Fatal("a tampered bundle must refuse before signing")
	}
}

func TestPackageRefusesNonTemplateEntries(t *testing.T) {
	_, opts, _ := packageFixture(t)
	if err := os.WriteFile(filepath.Join(opts.TemplatesDir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Package(t.Context(), opts); err == nil {
		t.Fatal("a non-JSON template entry must refuse")
	}
}

func TestPackageRefusesNestedArtifacts(t *testing.T) {
	_, opts, _ := packageFixture(t)
	if err := os.MkdirAll(filepath.Join(opts.ArtifactsDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Package(t.Context(), opts); err == nil {
		t.Fatal("a nested artifact directory must refuse")
	}
}

func TestPackageRefusesUnwrittenRecord(t *testing.T) {
	_, opts, _ := packageFixture(t)
	opts.Sign = func(context.Context, string, string, string, string) error { return nil }
	if err := Package(t.Context(), opts); err == nil {
		t.Fatal("a signer that writes no record must refuse")
	}
}

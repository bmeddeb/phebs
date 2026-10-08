//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

type installationFixture struct {
	selected        *config.ManagedSCIP
	manifest        typedInstallationManifest
	directory, data string
	inventory       []byte
}

func installationTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeInstallationControl(t *testing.T, name string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func newInstallationFixture(t *testing.T) installationFixture {
	t.Helper()
	root := t.TempDir()
	root, resolveErr := filepath.EvalSymlinks(root)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	base, data, source := filepath.Join(root, "installation"), filepath.Join(root, "data"), filepath.Join(root, "source")
	for _, name := range []string{base, data, source, filepath.Join(base, "repo")} {
		if err := os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	const repository = "example.test/installed-scip"
	body := []byte("package pkg\nvar Foo int\n")
	if err := os.Mkdir(filepath.Join(source, "pkg"), 0700); err != nil {
		t.Fatal(err)
	}
	writeInstallationControl(t, filepath.Join(source, "pkg", "a.go"), body)
	for _, args := range [][]string{{"init", "-q"}, {"add", "pkg/a.go"}, {"-c", "user.name=Neutral", "-c", "user.email=neutral@example.invalid", "commit", "-qm", "neutral"}} {
		if _, err := gitobj.Output(t.Context(), source, 4096, args...); err != nil {
			t.Fatal(err)
		}
	}
	head, err := gitobj.Output(t.Context(), source, 64, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := phebssync.SafeRepoDir(data, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(mirror), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = gitobj.Output(t.Context(), root, 4096, "clone", "--bare", "--no-hardlinks", source, mirror); err != nil {
		t.Fatal(err)
	}
	h := typedNavigationBytes([]byte("identity"))
	selection := []byte("{}") // Loader checks the control envelope; the worker admits its full provider semantics.
	inventory := installationTestJSON(t, typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: []typedindex.BundleFile{{Path: "source/pkg/a.go", Bytes: int64(len(body)), Digest: typedNavigationBytes(body)}, {Path: "typed-module-selection.json", Bytes: int64(len(selection)), Digest: typedNavigationBytes(selection)}}})
	configuration, err := typedindex.InputConfig(typedindex.ModuleProviderID, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	helper := typedindex.Tool{Version: "installed", Digest: h}
	profile := installationTestJSON(t, typedindex.ProfileDefinition{Schema: typedindex.InputProfileSchema, Provider: typedindex.ModuleProviderID, Name: "installed", Tools: typedindex.Tools{Go: typedindex.Tool{Version: "1.25.0", Digest: h}, Indexer: typedindex.SCIPGoIndexer(runtime.GOARCH), Planner: helper, Launcher: helper}, Config: configuration, Policy: typedindex.MeasuredPolicy(), BundleDigest: typedNavigationBytes(inventory), ImageDigest: h})
	directory := filepath.Join(base, "repo")
	writeInstallationControl(t, filepath.Join(directory, "inventory.json"), inventory)
	writeInstallationControl(t, filepath.Join(directory, "profile.json"), profile)
	manifest := typedInstallationManifest{Schema: typedInstallationSchema, Workspace: filepath.Join(root, "workspace"), Socket: filepath.Join(root, "docker.sock"), Image: h, Repositories: []typedInstalledRepository{{Source: typedindex.Source{Repository: repository, Incarnation: "neutral", Commit: strings.TrimSpace(string(head)), Generation: h}, ProfileEpoch: 1, UniverseDigest: h, ProfileDigest: typedNavigationBytes(profile), InventoryDigest: typedNavigationBytes(inventory), Directory: "repo"}}}
	f := installationFixture{manifest: manifest, selected: &config.ManagedSCIP{Manifest: filepath.Join(base, "installation.json")}, directory: directory, data: data, inventory: inventory}
	f.save(t)
	return f
}

func (f *installationFixture) save(t *testing.T) {
	raw := installationTestJSON(t, f.manifest)
	writeInstallationControl(t, f.selected.Manifest, raw)
	f.selected.SHA256 = typedNavigationBytes(raw)
}

type installationTestStore struct {
	source typedindex.Source
	intent store.TypedIndexIntent
	writes int
	drift  bool
}

func (s *installationTestStore) GetTypedSource(context.Context, string) (typedindex.Source, error) {
	return s.source, nil
}
func (s *installationTestStore) GetTypedIndexIntent(context.Context, string) (store.TypedIndexIntent, error) {
	if s.intent.ProfileEpoch == 0 {
		return s.intent, store.ErrNotFound
	}
	return s.intent, nil
}
func (s *installationTestStore) InstallTypedProfileExpectedSource(_ context.Context, source typedindex.Source, profile typedindex.Profile, universe string, epoch int64) (store.TypedIndexIntent, error) {
	if s.drift || source != s.source || epoch != s.intent.ProfileEpoch {
		return store.TypedIndexIntent{}, typedindex.Stale
	}
	s.writes++
	s.intent = store.TypedIndexIntent{Repository: source.Repository, ProfileEpoch: epoch + 1, ProfileDigest: profile.Digest(), UniverseDigest: universe}
	return s.intent, nil
}

func TestManagedSCIPInstallationRestartAndSourceFences(t *testing.T) {
	f := newInstallationFixture(t)
	installed, err := loadTypedServeInstallation(t.Context(), f.selected)
	if err != nil {
		t.Fatal(err)
	}
	r := installed.Registry
	entry := f.manifest.Repositories[0]
	s := &installationTestStore{source: entry.Source}
	if err = r.install(t.Context(), s, f.data); err != nil || s.writes != 1 {
		t.Fatal("first installation", err, s.writes)
	}
	s.intent.Desired = "retained-desire"
	if err = r.install(t.Context(), s, f.data); err != nil || s.writes != 1 || s.intent.Desired != "retained-desire" {
		t.Fatal("restart rewrote authority", err, s.writes)
	}
	profile := r.entries[entry.Source.Repository].profile
	snapshot := store.TypedIndexOperator{Source: entry.Source, Profile: profile, ProfileEpoch: entry.ProfileEpoch, UniverseDigest: entry.UniverseDigest}
	if !r.admits(snapshot) || !r.provider(typedindex.ModuleProviderID) || r.provider(typedindex.ProviderID) || r.provider(typedindex.ImportProviderID) {
		t.Fatal("availability exceeds installation")
	}
	snapshot.Source.Commit = strings.Repeat("b", 40)
	if r.admits(snapshot) {
		t.Fatal("changed source admitted")
	}
	request := typedindex.NewManagedRequest(entry.Source, profile, entry.ProfileEpoch, entry.UniverseDigest, typedindex.Canary)
	admitted, err := typedindex.Admit(t.Context(), typedindex.Authority{Enabled: true, Administrator: true, Source: entry.Source, Profile: typedindex.Epoch{Number: entry.ProfileEpoch, Digest: profile.Digest()}, UniverseDigest: entry.UniverseDigest}, profile, installationTestJSON(t, request))
	if err != nil {
		t.Fatal(err)
	}
	name, raw, err := installed.Bundle(t.Context(), admitted)
	if err != nil || name != filepath.Join(f.directory, "bundle") || !slices.Equal(raw, f.inventory) {
		t.Fatal("lookup changed installation", name, err)
	}
	writeInstallationControl(t, filepath.Join(f.directory, "inventory.json"), []byte("changed"))
	if _, _, err = installed.Bundle(t.Context(), admitted); err == nil {
		t.Fatal("changed control admitted")
	}
	writeInstallationControl(t, filepath.Join(f.directory, "inventory.json"), f.inventory)
	for _, mode := range []string{"source", "restore", "epoch", "write-drift"} {
		t.Run(mode, func(t *testing.T) {
			s := &installationTestStore{source: entry.Source}
			switch mode {
			case "source":
				s.source.Generation = typedNavigationBytes([]byte("other"))
			case "restore":
				s.intent = store.TypedIndexIntent{ProfileEpoch: 1, ProfileDigest: entry.ProfileDigest, UniverseDigest: entry.UniverseDigest, RestoreRequired: true}
			case "epoch":
				s.intent.ProfileEpoch = 3
			case "write-drift":
				s.drift = true
			}
			if err := r.install(t.Context(), s, f.data); !errors.Is(err, typedindex.Stale) || s.writes != 0 {
				t.Fatal("invalid installation wrote", err, s.writes)
			}
		})
	}
}

func TestManagedSCIPInstallationManifestRefusals(t *testing.T) {
	f := newInstallationFixture(t)
	for _, mode := range []string{"digest", "unknown-field", "duplicate-field", "duplicate-repository", "directory-traversal", "workspace-overlap", "foreign-image", "zero-epoch", "control-symlink", "control-mode", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			selected := *f.selected
			manifest := f.manifest
			manifest.Repositories = slices.Clone(manifest.Repositories)
			switch mode {
			case "digest":
				selected.SHA256 = typedNavigationBytes([]byte("wrong"))
			case "duplicate-repository":
				manifest.Repositories = append(manifest.Repositories, manifest.Repositories[0])
			case "directory-traversal":
				manifest.Repositories[0].Directory = "../repo"
			case "workspace-overlap":
				manifest.Workspace = filepath.Dir(selected.Manifest)
			case "foreign-image":
				manifest.Image = typedNavigationBytes([]byte("other"))
			case "zero-epoch":
				manifest.Repositories[0].ProfileEpoch = 0
			case "overflow":
				manifest.Repositories = make([]typedInstalledRepository, maxTypedInstalledRepositories+1)
			case "control-symlink":
				original := filepath.Join(f.directory, "profile.json")
				if err := os.Rename(original, original+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original+".real", original); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(original); _ = os.Rename(original+".real", original) })
			case "control-mode":
				if err := os.Chmod(filepath.Join(f.directory, "profile.json"), 0644); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(filepath.Join(f.directory, "profile.json"), 0600) })
			}
			raw := installationTestJSON(t, manifest)
			if mode == "unknown-field" {
				raw = append([]byte(`{"unknown":true,`), raw[1:]...)
			}
			if mode == "duplicate-field" {
				raw = append([]byte(`{"schema":"`+typedInstallationSchema+`",`), raw[1:]...)
			}
			writeInstallationControl(t, selected.Manifest, raw)
			if mode != "digest" {
				selected.SHA256 = typedNavigationBytes(raw)
			}
			if _, err := loadTypedServeInstallation(t.Context(), &selected); err == nil {
				t.Fatal("invalid manifest admitted", mode)
			}
		})
	}
	f.save(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := loadTypedServeInstallation(ctx, f.selected); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestManagedSCIPInstallationRejectsIncorrectGitBlobs(t *testing.T) {
	f := newInstallationFixture(t)
	installed, err := loadTypedServeInstallation(t.Context(), f.selected)
	if err != nil {
		t.Fatal(err)
	}
	profile := installed.Registry.entries[f.manifest.Repositories[0].Source.Repository]
	if err = profile.verifySource(t.Context(), f.data); err != nil {
		t.Fatal(err)
	}
	var inventory typedindex.InventoryDefinition
	if err = json.Unmarshal(f.inventory, &inventory); err != nil {
		t.Fatal(err)
	}
	inventory.Files[0].Digest = typedNavigationBytes([]byte("wrong"))
	raw := installationTestJSON(t, inventory)
	writeInstallationControl(t, filepath.Join(f.directory, "inventory.json"), raw)
	profile.entry.InventoryDigest = typedNavigationBytes(raw)
	if err = profile.verifySource(t.Context(), f.data); !errors.Is(err, typedindex.Stale) {
		t.Fatal("incorrect source digest accepted", err)
	}
	inventory.Files[0].Executable = true
	raw = installationTestJSON(t, inventory)
	writeInstallationControl(t, filepath.Join(f.directory, "inventory.json"), raw)
	profile.entry.InventoryDigest = typedNavigationBytes(raw)
	if err = profile.verifySource(t.Context(), f.data); !errors.Is(err, typedindex.Stale) {
		t.Fatal("incorrect source mode accepted", err)
	}
}

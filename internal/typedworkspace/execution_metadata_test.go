//go:build linux

package typedworkspace

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func metadataFixture(t *testing.T) (publicationFixture, OwnerIdentity, OwnerManifest, []byte) {
	t.Helper()
	f := newPublicationFixture(t)
	source := t.TempDir()
	payloads := map[string][]byte{typedindex.HostToolsFile: []byte(`{"schema":"phebs-typed-host-tools-v1","mkfs_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), "typed-bazel-selection.json": []byte(`{"neutral":"selection"}`)}
	var files []typedindex.BundleFile
	for name, raw := range payloads {
		if err := os.WriteFile(filepath.Join(source, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		files = append(files, typedindex.BundleFile{Path: name, Bytes: int64(len(raw)), Digest: digest(raw)})
	}
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	raw := append([]byte(" \n"), publicationJSON(t, typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})...)
	inv, err := typedindex.DecodeInventory(t.Context(), raw, digest(raw))
	if err != nil {
		t.Fatal(err)
	}
	tool := typedindex.Tool{Version: "0.2.7", Digest: digest([]byte("tool"))}
	profile, err := typedindex.DecodeProfile(t.Context(), publicationJSON(t, typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "metadata", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: inv.Digest(), ImageDigest: digest([]byte("image"))}))
	if err != nil {
		t.Fatal(err)
	}
	authority := typedindex.Authority{Enabled: true, Administrator: true, Source: f.parent.Request().Source, Profile: typedindex.Epoch{Number: 1, Digest: profile.Digest()}, UniverseDigest: f.parent.Request().UniverseDigest}
	request := typedindex.NewRequest(authority.Source, profile, 1, authority.UniverseDigest, "metadata")
	parent, err := typedindex.Admit(t.Context(), authority, profile, publicationJSON(t, request))
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewOwnerIdentity(parent, digest([]byte("chunk")), "lease")
	if err != nil {
		t.Fatal(err)
	}
	m, err := CreateOwner(t.Context(), f.dir, ownerProvisionedObservation(t, f.dir), id, OwnerBudget{Bytes: 1 << 20, Inodes: 128}, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := Copy(t.Context(), source, filepath.Join(f.dir, id.RelativeName()), inv, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	m, err = SaveOwnerInputs(t.Context(), f.dir, id, m.Digest(), raw, copied, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	return f, id, m, raw
}
func TestExecutionMetadataOriginalBytesAndCustody(t *testing.T) {
	for _, fault := range []string{"good", "content", "inode", "symlink", "missing", "writable", "manifest", "inventory bytes"} {
		t.Run(fault, func(t *testing.T) {
			f, id, m, raw := metadataFixture(t)
			expected := m.Digest()
			input := filepath.Join(f.dir, id.RelativeName(), m.InputName)
			name := filepath.Join(input, typedindex.HostToolsFile)
			must := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			if fault != "good" {
				must(os.Chmod(input, 0700))
			}
			switch fault {
			case "content":
				data, e := os.ReadFile(name)
				must(e)
				data[0] ^= 1
				must(os.Chmod(name, 0600))
				must(os.WriteFile(name, data, 0600))
				must(os.Chmod(name, 0444))
			case "inode":
				data, e := os.ReadFile(name)
				must(e)
				must(os.Rename(name, name+".old"))
				must(os.WriteFile(name, data, 0444))
			case "symlink":
				must(os.Rename(name, name+".old"))
				must(os.Symlink(name+".old", name))
			case "missing":
				must(os.Remove(name))
			case "writable":
				must(os.Chmod(name, 0644))
			case "manifest":
				expected = digest([]byte("wrong"))
			case "inventory bytes":
				name = filepath.Join(f.dir, id.RelativeName(), "inventory.json")
				must(os.Chmod(name, 0600))
				must(os.WriteFile(name, append(raw, ' '), 0600))
				must(os.Chmod(name, 0444))
			}
			must(os.Chmod(input, 0555))
			got, err := LoadOwnerExecutionMetadata(t.Context(), f.dir, id, expected)
			if fault != "good" {
				if err == nil {
					t.Fatal("mutation accepted")
				}
				return
			}
			if err != nil || !bytes.Equal(got.InventoryRaw, raw) || got.Inventory.Digest() != digest(raw) || len(got.HostTools) == 0 || len(got.Selection) == 0 {
				t.Fatal("snapshot", err)
			}
			got.InventoryRaw[0] = 'x'
			again, err := LoadOwnerExecutionMetadata(t.Context(), f.dir, id, expected)
			if err != nil || !bytes.Equal(again.InventoryRaw, raw) {
				t.Fatal("snapshot aliases disk", err)
			}
		})
	}
}

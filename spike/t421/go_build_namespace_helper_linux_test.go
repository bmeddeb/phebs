//go:build linux

package t421

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"
	"time"

	"golang.org/x/mod/module"
)

func TestLinuxGoBuildNeutralInputShape(t *testing.T) {
	var base inputTreeCensus
	for _, name := range []string{"inputs", "inputs/git", "inputs/git/core", "inputs/modules", "inputs/sdk", "inputs/source", "inputs/source/.git"} {
		base.rows = append(base.rows, inputTreeEntry{name: name, mode: 0o700})
	}
	for _, name := range executionGitImageNames() {
		base.rows = append(base.rows, inputTreeEntry{name: "inputs/git/core/" + name, mode: 0o500, digest: [32]byte{1}})
	}
	for _, name := range []string{"inputs/source/go.mod", "inputs/source/main.go", "inputs/source/.git/config"} {
		base.rows = append(base.rows, inputTreeEntry{name: name, mode: 0o400})
	}
	if !neutralGoBuildInputShape(base) {
		t.Fatal("fixed input shape refused")
	}
	for _, test := range []struct {
		name   string
		change func(*inputTreeCensus)
	}{
		{"missing-root", func(c *inputTreeCensus) { c.rows = c.rows[1:] }},
		{"missing-alias", func(c *inputTreeCensus) { c.rows = slices.Delete(c.rows, 7, 8) }},
		{"different-alias-image", func(c *inputTreeCensus) { c.rows[7].digest[0]++ }},
		{"non-executable-alias", func(c *inputTreeCensus) { c.rows[7].mode = 0o400 }},
		{"additional-source", func(c *inputTreeCensus) {
			c.rows = append(c.rows, inputTreeEntry{name: "inputs/source/extra.go", mode: 0o400})
		}},
		{"ambient-cache", func(c *inputTreeCensus) {
			c.rows = append(c.rows, inputTreeEntry{name: "inputs/modules/ambient", mode: 0o400})
		}},
		{"foreign-root", func(c *inputTreeCensus) { c.rows = append(c.rows, inputTreeEntry{name: "foreign", mode: 0o700}) }},
		{"git-redirect", func(c *inputTreeCensus) {
			c.rows = append(c.rows, inputTreeEntry{name: "inputs/source/.git/commondir", mode: 0o400})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := base
			copy.rows = slices.Clone(base.rows)
			test.change(&copy)
			if neutralGoBuildInputShape(copy) {
				t.Fatal("unsafe input shape admitted")
			}
		})
	}
}

func TestLinuxGoBuildNeutralVCSFence(t *testing.T) {
	const revision = "0123456789012345678901234567890123456789"
	commitTime := time.Unix(linuxGoBuildCommitUnix, 0).UTC()
	base := debug.BuildInfo{GoVersion: runtime.Version(), Path: "neutral.invalid/protected", Main: debug.Module{Path: "neutral.invalid/protected", Version: module.PseudoVersion("v0", "", commitTime, revision[:12])}, Settings: []debug.BuildSetting{
		{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"}, {Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "amd64"},
		{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "false"},
		{Key: "vcs.time", Value: commitTime.Format(time.RFC3339)},
	}}
	if !neutralGoBuildInfo(&base, revision) {
		t.Fatal("clean exact neutral build refused")
	}
	for _, replacement := range []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}, {Key: "vcs.revision", Value: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {Key: "vcs", Value: ""}, {Key: "CGO_ENABLED", Value: "1"}, {Key: "GOOS", Value: "darwin"}, {Key: "-trimpath", Value: "false"}, {Key: "vcs.time", Value: "2023-11-14T22:13:21Z"}} {
		t.Run(replacement.Key+"="+replacement.Value, func(t *testing.T) {
			changed := base
			changed.Settings = slices.Clone(base.Settings)
			for i := range changed.Settings {
				if changed.Settings[i].Key == replacement.Key {
					changed.Settings[i] = replacement
				}
			}
			if neutralGoBuildInfo(&changed, revision) {
				t.Fatal("weakened neutral build accepted")
			}
		})
	}
	for _, version := range []string{"(devel)", base.Main.Version + "+dirty", "v0.0.0-20231114221320-aaaaaaaaaaaa"} {
		changed := base
		changed.Main.Version = version
		if neutralGoBuildInfo(&changed, revision) {
			t.Fatal("non-exact neutral module version admitted", version)
		}
	}
	changed := base
	changed.Settings = append(slices.Clone(base.Settings), base.Settings[0])
	if neutralGoBuildInfo(&changed, revision) || neutralGoBuildInfo(nil, revision) || neutralGoBuildInfo(&base, "invalid") {
		t.Fatal("ambiguous build identity accepted")
	}
}

func TestLinuxGoBuildNeutralSourceAndMetadata(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"inputs", "inputs/source", "inputs/source/.git"} {
		if os.Mkdir(filepath.Join(directory, name), 0o700) != nil {
			t.Fatal("fixture")
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, file := range []struct{ name, content string }{{"go.mod", linuxGoBuildModule}, {"main.go", linuxGoBuildSource}, {".git/config", linuxGoBuildGitConfig}} {
		if root.WriteFile("inputs/source/"+file.name, []byte(file.content), 0o600) != nil {
			t.Fatal("fixture")
		}
	}
	if !neutralGoBuildSource(root) {
		t.Fatal("neutral fixed source refused")
	}
	if root.WriteFile("inputs/source/.git/config", []byte("[include]\n path=/ambient\n"), 0o600) != nil || neutralGoBuildSource(root) {
		t.Fatal("Git include admitted")
	}
	for _, name := range []string{"config.worktree", "commondir", "gitdir", "info/attributes", "info/grafts", "objects/info/alternates", "hooks/post-checkout", "refs/replace/123"} {
		if neutralGoBuildGitEntry(inputTreeEntry{name: "inputs/source/.git/" + name, mode: 0o400}) {
			t.Fatal("Git execution/redirect control admitted", name)
		}
	}
	for _, name := range []string{"HEAD", "ORIG_HEAD", "index", "refs/heads/main", "objects/ab/01234567890123456789012345678901234567", "objects/pack/pack-0123456789012345678901234567890123456789.pack"} {
		if !neutralGoBuildGitEntry(inputTreeEntry{name: "inputs/source/.git/" + name, mode: 0o400}) {
			t.Fatal("fixed Git data refused", name)
		}
	}
}

//go:build linux

package t421

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func goReferenceCensusFixture(t *testing.T) inputTreeCensus {
	t.Helper()
	rows := map[string]inputTreeEntry{}
	add := func(name string, mode uint32) {
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			rows[parent] = inputTreeEntry{name: parent, mode: 0o700}
		}
		rows[name] = inputTreeEntry{name: name, mode: mode}
	}
	for _, path := range []string{"inputs/sdk/src", "inputs/sdk/pkg", "inputs/sdk/lib", "inputs/modules", "inputs/source/.git"} {
		add(path, 0o700)
	}
	for _, path := range []string{"inputs/sdk/go.env", "inputs/sdk/VERSION", "inputs/source/go.mod", "inputs/source/go.sum", "inputs/source/.git/HEAD", "inputs/source/.git/config", "inputs/source/.git/index", "inputs/source/.git/shallow"} {
		add(path, 0o400)
	}
	for _, path := range []string{"inputs/sdk/bin/go", "inputs/sdk/pkg/tool/linux_amd64/asm", "inputs/sdk/pkg/tool/linux_amd64/cgo", "inputs/sdk/pkg/tool/linux_amd64/compile", "inputs/sdk/pkg/tool/linux_amd64/cover", "inputs/sdk/pkg/tool/linux_amd64/fix", "inputs/sdk/pkg/tool/linux_amd64/link", "inputs/sdk/pkg/tool/linux_amd64/preprofile", "inputs/sdk/pkg/tool/linux_amd64/vet"} {
		add(path, 0o500)
	}
	for _, name := range executionGitImageNames() {
		path := "inputs/git/core/" + name
		add(path, 0o500)
		row := rows[path]
		row.digest = [32]byte{1}
		rows[path] = row
	}
	var names []string
	for name := range rows {
		names = append(names, name)
	}
	slices.Sort(names)
	var census inputTreeCensus
	for _, name := range names {
		row := rows[name]
		if row.mode == 0o500 {
			row.size = 1
		}
		if census.add(row) != nil {
			t.Fatal("census fixture")
		}
	}
	return census
}

func TestLinuxGoReferenceInputShape(t *testing.T) {
	base := goReferenceCensusFixture(t)
	if digest, ok := goReferenceInputShape(base); !ok || !validExecutionSHA256(digest) {
		t.Fatal("complete protected shape refused")
	}
	for _, name := range []string{"inputs/source/.git/shallow", "inputs/sdk/bin/go", "inputs/git/core/git-upload-pack"} {
		changed := base
		changed.rows = slices.DeleteFunc(slices.Clone(base.rows), func(row inputTreeEntry) bool { return row.name == name })
		if _, ok := goReferenceInputShape(changed); ok {
			t.Fatal("missing required input admitted", name)
		}
	}
	for _, path := range []string{"foreign", "inputs/unselected", "inputs/git/core/git-shell", "inputs/source/.git/commondir", "inputs/source/.git/objects/info/alternates", "inputs/source/.git/hooks/post-checkout", "inputs/source/.git/refs/tags/v1.0.0"} {
		changed := base
		changed.rows = append(slices.Clone(base.rows), inputTreeEntry{name: path, mode: 0o400})
		if _, ok := goReferenceInputShape(changed); ok {
			t.Fatal("extra authority/control admitted", path)
		}
	}
	changed := base
	changed.rows = slices.Clone(base.rows)
	for i := range changed.rows {
		if changed.rows[i].name == "inputs/git/core/git" {
			changed.rows[i].digest[0]++
		}
	}
	if _, ok := goReferenceInputShape(changed); ok {
		t.Fatal("different Git image admitted")
	}
}

func TestLinuxGoReferenceMaskSelection(t *testing.T) {
	plan := goReferencePlanFixture()
	if paths, err := goReferenceMaskPaths(plan, "/private/mount", "/private/work"); err != nil || len(paths) != 3 {
		t.Fatal("disjoint originals refused", err)
	}
	for _, path := range []string{"/", "/proc", "/proc/self", "/dev", "/sys", "/private", "/private/work/home", "/selected/modules", "/selected", "/selected/modules/nested", "/bad path", "/bad\\path", strings.Repeat("/x", 4096)} {
		changed := plan
		changed.OriginalSDK = path
		if _, err := goReferenceMaskPaths(changed, "/private/mount", "/private/work"); err == nil {
			t.Fatal("overlapping/system/noncanonical mask admitted", path)
		}
	}
}

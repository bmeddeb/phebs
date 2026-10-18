package t421

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLinuxInputTreeWireClosedFrames(t *testing.T) {
	good, err := encodeInputTreeFrame(treeWireData, 1, []byte("neutral"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"short", func(b []byte) []byte { return b[:23] }},
		{"magic", func(b []byte) []byte { b[0] = 'X'; return b }},
		{"unknown kind", func(b []byte) []byte { b[4] = 9; return b }},
		{"reserved prefix", func(b []byte) []byte { b[7] = 1; return b }},
		{"reserved tail", func(b []byte) []byte { b[23] = 1; return b }},
		{"ordinal replay", func(b []byte) []byte { binary.BigEndian.PutUint64(b[8:], 2); return b }},
		{"length mismatch", func(b []byte) []byte { binary.BigEndian.PutUint32(b[16:], 6); return b }},
		{"extra payload", func(b []byte) []byte { return append(b, 0) }},
		{"oversize", func(b []byte) []byte { return append(b, make([]byte, treeWireMaximum)...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := decodeInputTreeFrame(test.edit(bytes.Clone(good)), 1); err == nil {
				t.Fatal("malformed frame accepted")
			}
		})
	}
	kind, raw, err := decodeInputTreeFrame(good, 1)
	if err != nil || kind != treeWireData || string(raw) != "neutral" {
		t.Fatal("valid frame refused", err)
	}
	for _, test := range []struct {
		kind    byte
		ordinal uint64
		size    int
	}{{0, 1, 0}, {9, 1, 0}, {treeWireData, 0, 0}, {treeWireData, 1, treeWireMaximum}} {
		if _, err := encodeInputTreeFrame(test.kind, test.ordinal, make([]byte, test.size)); err == nil {
			t.Fatal("invalid frame construction accepted", test)
		}
	}
}

func TestLinuxInputTreeCensusClosedNamesTypesAndLimits(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry inputTreeEntry
	}{
		{"absolute", inputTreeEntry{name: "/root", mode: 0o700}},
		{"escape", inputTreeEntry{name: "../root", mode: 0o700}},
		{"noncanonical", inputTreeEntry{name: "root//file", mode: 0o400}},
		{"missing parent", inputTreeEntry{name: "root/file", mode: 0o400}},
		{"backslash", inputTreeEntry{name: "root\\file", mode: 0o400}},
		{"nul", inputTreeEntry{name: "root\x00", mode: 0o700}},
		{"newline", inputTreeEntry{name: "root\n", mode: 0o700}},
		{"bad unicode", inputTreeEntry{name: "root\xff", mode: 0o700}},
		{"long path", inputTreeEntry{name: strings.Repeat("x", maxInputCustodyPathBytes+1), mode: 0o700}},
		{"setid", inputTreeEntry{name: "root", mode: 0o4700}},
		{"writable file", inputTreeEntry{name: "root", mode: 0o600}},
		{"directory payload", inputTreeEntry{name: "root", mode: 0o700, size: 1}},
		{"empty executable", inputTreeEntry{name: "root", mode: 0o500}},
		{"large file", inputTreeEntry{name: "root", mode: 0o400, size: maxInputCustodyFileBytes + 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var census inputTreeCensus
			if census.add(test.entry) == nil || len(census.rows) != 0 || census.inventory.entries != 0 {
				t.Fatal("invalid row retained")
			}
		})
	}
	for _, test := range []struct {
		name      string
		inventory inputTreeInventory
	}{
		{"entries", inputTreeInventory{entries: maxGoBuildEntries}},
		{"names", inputTreeInventory{pathBytes: maxGoBuildPathBytes}},
		{"bytes", inputTreeInventory{bytes: maxGoBuildBytes}},
	} {
		t.Run(test.name, func(t *testing.T) {
			census := inputTreeCensus{inventory: test.inventory}
			if census.add(inputTreeEntry{name: "root", mode: 0o400, size: 1}) == nil || len(census.rows) != 0 {
				t.Fatal("aggregate bound accepted")
			}
		})
	}
	var census inputTreeCensus
	for _, row := range []inputTreeEntry{{name: "root", mode: 0o700}, {name: "root/a", mode: 0o400}, {name: "root/b", mode: 0o500, size: 1}} {
		raw := encodeInputTreeEntry(row)
		decoded, err := decodeInputTreeEntry(raw)
		if err != nil || decoded != row || census.add(decoded) != nil {
			t.Fatal("valid row refused", err)
		}
	}
	for _, row := range []inputTreeEntry{{name: "root/a", mode: 0o400}, {name: "root/b/child", mode: 0o400}} {
		if census.add(row) == nil {
			t.Fatal("duplicate/order/file-parent accepted")
		}
	}
	for _, size := range []int{0, 12, 13 + maxInputCustodyPathBytes} {
		if _, err := decodeInputTreeEntry(make([]byte, size)); err == nil {
			t.Fatal("invalid entry envelope accepted")
		}
	}
}

func TestLinuxInputTreeManifestBindsEveryEntryDimension(t *testing.T) {
	var census inputTreeCensus
	for _, row := range []inputTreeEntry{{name: "root", mode: 0o700}, {name: "root/file", mode: 0o400, size: 3, digest: sha256.Sum256([]byte("abc"))}} {
		if census.add(row) != nil {
			t.Fatal("fixture")
		}
	}
	base := inputTreeManifest(census)
	if len(base) != 64 || binary.BigEndian.Uint64(base) != 2 || binary.BigEndian.Uint64(base[8:]) != 1 || binary.BigEndian.Uint64(base[16:]) != 3 {
		t.Fatal("census not bound")
	}
	for _, change := range []func(*inputTreeEntry){
		func(row *inputTreeEntry) { row.name = "root/other" }, func(row *inputTreeEntry) { row.mode = 0o500 },
		func(row *inputTreeEntry) { row.size = 4 }, func(row *inputTreeEntry) { row.digest[0] ^= 1 },
	} {
		modified := census
		modified.rows = slices.Clone(census.rows)
		change(&modified.rows[1])
		if bytes.Equal(base, inputTreeManifest(modified)) {
			t.Fatal("manifest dimension unbound")
		}
	}
}

func TestLinuxInputTreeHelperReferenceInventory(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	path, module, version, sum, recipe, err := referenceToolRoleForSchema(linuxInputTreeRole, linuxInputTreeRecipe, commit)
	if err != nil || path != "github.com/bmeddeb/phebs/spike/t421/cmd/input-tree-helper" || module != "" || version != "" || sum != "" || recipe != "" {
		t.Fatal("fixed recipe", err)
	}
	for _, test := range []struct{ role, schema, commit string }{
		{linuxInputTreeRole, "", commit}, {linuxInputTreeRole, PlanV3Schema, commit}, {"phebs", linuxInputTreeRecipe, commit},
		{linuxInputTreeRole, linuxInputTreeRecipe, "not-a-commit"},
	} {
		if _, _, _, _, _, err := referenceToolRoleForSchema(test.role, test.schema, test.commit); err == nil {
			t.Fatal("helper widened historical role admission")
		}
	}
	if knownPlanSchema(linuxInputTreeRecipe) {
		t.Fatal("helper recipe became a ceremony plan")
	}
	main, err := os.ReadFile(filepath.Join("cmd", "input-tree-helper", "main.go"))
	if err != nil || !bytes.Contains(main, []byte("os.Exit(t421.LinuxInputTreeHelperMain())")) {
		t.Fatal("fixed helper entrypoint", err)
	}
	info := &debug.BuildInfo{GoVersion: runtime.Version(), Path: path, Main: debug.Module{Path: "github.com/bmeddeb/phebs"}, Settings: []debug.BuildSetting{
		{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"}, {Key: "GOOS", Value: runtime.GOOS}, {Key: "GOARCH", Value: runtime.GOARCH},
		{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: "false"},
	}}
	if validateReferenceBuildInfoForSchema(info, linuxInputTreeRole, linuxInputTreeRecipe, path, commit, "", "", "", nil) != nil {
		t.Fatal("exact buildinfo refused")
	}
	for _, setting := range []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "1"}, {Key: "vcs.modified", Value: "true"}, {Key: "vcs.revision", Value: strings.Repeat("a", 40)}} {
		copyInfo := *info
		copyInfo.Settings = slices.Clone(info.Settings)
		for i := range copyInfo.Settings {
			if copyInfo.Settings[i].Key == setting.Key {
				copyInfo.Settings[i] = setting
			}
		}
		if validateReferenceBuildInfoForSchema(&copyInfo, linuxInputTreeRole, linuxInputTreeRecipe, path, commit, "", "", "", nil) == nil {
			t.Fatal("false helper buildinfo accepted")
		}
	}
}

func TestLinuxInputTreeActualWaitFenceDoesNotInventJoin(t *testing.T) {
	// Construction reuses this existing actual-Wait fence. A timeout leaves its
	// sole channel and owner alive; only the later real Wait result proves join.
	waited := make(chan error, 1)
	if err, joined := waitExecutionProcessRoot(waited, time.Now().Add(-time.Second)); err != nil || joined {
		t.Fatal("timeout invented a root join", err, joined)
	}
	terminal := ErrLinuxInputTree
	waited <- terminal
	if err, joined := waitExecutionProcessRoot(waited, time.Now().Add(-time.Second)); err != terminal || !joined {
		t.Fatal("later actual Wait result lost", err, joined)
	}
	waited <- nil
	if err, joined := waitExecutionProcessRoot(waited, time.Now().Add(time.Second)); err != nil || !joined {
		t.Fatal("successful actual Wait refused", err, joined)
	}
	if err, joined := waitExecutionProcessRoot(nil, time.Now().Add(-time.Second)); err != nil || joined {
		t.Fatal("missing Wait invented join", err, joined)
	}
}

func TestLinuxInputTreeCompletionPublishesBeforeCancellation(t *testing.T) {
	done := make(chan struct{})
	completeInputTreeConstruction(done, func() {
		select {
		case <-done:
		default:
			t.Fatal("cleanup cancellation preceded terminal publication")
		}
	})
}

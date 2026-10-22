//go:build linux

package t4013

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestLinuxProcessSessionCohortUnion(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		t.Run(strconv.FormatBool(adopted), func(t *testing.T) {
			root := t.TempDir()
			for _, entry := range []struct{ pid, parent, session int }{{10, 1, 10}, {20, 10, 20}, {11, 20, 20}, {12, 11, 20}} {
				if adopted && entry.pid == 11 {
					entry.parent = 1
				}
				writeLinuxProcessFixture(t, root, entry.pid, entry.parent)
				raw := linuxStatFixture(entry.pid, entry.parent, "test", "100", "S")
				parts := strings.SplitN(string(raw), ") ", 2)
				fields := strings.Fields(parts[1])
				fields[3] = strconv.Itoa(entry.session)
				if err := os.WriteFile(filepath.Join(root, strconv.Itoa(entry.pid), "stat"), []byte(parts[0]+") "+strings.Join(fields, " ")+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			pids, records, candidates, err := linuxProcessSnapshotWithSessionAt(t.Context(), root, 10, 20)
			if err != nil || len(pids) != 4 || len(records) != 4 || !slices.Equal(candidates, []int{11, 12, 20}) {
				t.Fatal("bounded deduplicated union", pids, records, candidates, err)
			}
			rows, err := nativeProcessCohortRecords(10, pids, records, candidates)
			if err != nil || len(rows) != 4 {
				t.Fatal(rows, err)
			}
			var rss int64
			for _, row := range rows {
				rss += row.RSSBytes
				if row.PID == 11 && adopted && row.ParentPID != 1 {
					t.Fatal("raw current adopter was rewritten", row)
				}
			}
			if rss != 4*4096 {
				t.Fatal("RSS omitted or counted twice", rss)
			}
			if adopted {
				if _, err := nativeProcessRecords(10, pids, records); err == nil {
					t.Fatal("ordinary current-tree validator weakened")
				}
			}
		})
	}
}

func TestLinuxProcessCohortRefusesMissingAuthorityInputs(t *testing.T) {
	p := processSnapshot{parent: 1, rssBytes: 4096, identityToken: "birth", name: "test", coherent: true}
	for _, candidates := range [][]int{{0}, {11, 11}, {12}} {
		if _, err := nativeProcessCohortRecords(10, []int{10, 11}, map[int]processSnapshot{10: p, 11: p}, candidates); err == nil {
			t.Fatal("invalid/missing candidate accepted", candidates)
		}
	}
}

func TestLinuxProcessCohortRefusesSessionChange(t *testing.T) {
	root := t.TempDir()
	writeLinuxProcessFixture(t, root, 10, 1)
	before, err := linuxProcessStatAt(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	before.session = "different-session"
	if _, err := linuxProcessResidentBytesWithSession(root, 10, before, true); err == nil {
		t.Fatal("changed SID accepted")
	}
	if _, err := linuxProcessResidentBytes(root, 10, before); err != nil {
		t.Fatal("ordinary observation changed", err)
	}
}

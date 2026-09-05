package t4013

import (
	"context"
	"os"
	"runtime"
	"testing"
)

func TestNativeProcessRecordsRequireCompleteCoherentRows(t *testing.T) {
	const root, child = 41, 42
	for _, test := range []struct {
		name   string
		mutate func(*[]int, map[int]processSnapshot)
	}{
		{"missing root", func(pids *[]int, _ map[int]processSnapshot) { *pids = []int{child} }},
		{"missing row", func(_ *[]int, rows map[int]processSnapshot) { delete(rows, child) }},
		{"extra row", func(_ *[]int, rows map[int]processSnapshot) { rows[43] = rows[child] }},
		{"duplicate", func(pids *[]int, _ map[int]processSnapshot) { *pids = []int{root, root} }},
		{"incoherent", func(_ *[]int, rows map[int]processSnapshot) {
			row := rows[child]
			row.coherent = false
			rows[child] = row
		}},
		{"missing identity", func(_ *[]int, rows map[int]processSnapshot) {
			row := rows[child]
			row.identityToken = ""
			rows[child] = row
		}},
		{"missing name", func(_ *[]int, rows map[int]processSnapshot) { row := rows[child]; row.name = ""; rows[child] = row }},
		{"wrong parent", func(_ *[]int, rows map[int]processSnapshot) { row := rows[child]; row.parent = 99; rows[child] = row }},
		{"negative RSS", func(_ *[]int, rows map[int]processSnapshot) { row := rows[child]; row.rssBytes = -1; rows[child] = row }},
		{"zero root RSS", func(_ *[]int, rows map[int]processSnapshot) { row := rows[root]; row.rssBytes = 0; rows[root] = row }},
		{"inventory overflow", func(pids *[]int, _ map[int]processSnapshot) { *pids = make([]int, MaxNativeProcessRecords+1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			pids := []int{root, child}
			rows := map[int]processSnapshot{root: {parent: 1, rssBytes: 4096, identityToken: "100:1", name: "phebs", coherent: true},
				child: {parent: root, identityToken: "101:1", name: "git", coherent: true}}
			test.mutate(&pids, rows)
			if _, err := nativeProcessRecords(root, pids, rows); err == nil {
				t.Fatal("invalid native rows accepted")
			}
		})
	}
	rows, err := nativeProcessRecords(root, []int{root, child}, map[int]processSnapshot{
		root:  {parent: 1, rssBytes: 4096, identityToken: "100:1", name: "phebs", coherent: true},
		child: {parent: root, identityToken: "101:1", name: "git", coherent: true},
	})
	if err != nil || len(rows) != 2 || rows[1].RSSBytes != 0 || rows[1].ParentPID != root || rows[1].ObservedName != "git" {
		t.Fatalf("coherent native rows = %+v, %v", rows, err)
	}
}

func TestObserveProcessTreeRecordsRealAndCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ObserveProcessTreeRecords(ctx, os.Getpid()); err == nil {
		t.Fatal("canceled observation accepted")
	}
	if runtime.GOOS != "darwin" {
		if _, err := ObserveProcessTreeRecords(t.Context(), os.Getpid()); err == nil {
			t.Fatal("unsupported native collector accepted")
		}
		return
	}
	rows, err := ObserveProcessTreeRecords(t.Context(), os.Getpid())
	if err != nil || len(rows) == 0 || len(rows) > MaxNativeProcessRecords || rows[0].PID != os.Getpid() ||
		rows[0].ParentPID != os.Getppid() || rows[0].StartIdentity == "" || rows[0].ObservedName == "" || rows[0].RSSBytes <= 0 {
		t.Fatalf("real native rows = %+v, %v", rows, err)
	}
}

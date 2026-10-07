//go:build linux

package t4013

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func linuxStatFixture(pid, parent int, name, token, state string) []byte {
	fields := make([]string, 22)
	for index := range fields {
		fields[index] = "0"
	}
	fields[0], fields[1], fields[19] = state, strconv.Itoa(parent), token
	return []byte(fmt.Sprintf("%d (%s) %s\n", pid, name, strings.Join(fields, " ")))
}

func writeLinuxProcessFixture(t *testing.T, root string, pid, parent int) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), linuxStatFixture(pid, parent, "test", "100", "S"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "smaps_rollup"), []byte("Rss: 4 kB\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxProcessStatAndRSSBounds(t *testing.T) {
	raw := linuxStatFixture(10, 1, "name ) ( x", "123", "S")
	got, err := parseLinuxProcessStat(10, raw)
	if err != nil || got.snapshot.name != "name ) ( x" || got.snapshot.parent != 1 || got.snapshot.identityToken != "123" {
		t.Fatalf("stat=%+v, %v", got, err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"empty", nil}, {"truncated", []byte("10 (x) S 1")},
		{"wrong pid", linuxStatFixture(11, 1, "test", "1", "S")},
		{"negative parent", linuxStatFixture(10, -1, "test", "1", "S")},
		{"self parent", linuxStatFixture(10, 10, "test", "1", "S")},
		{"invalid state", linuxStatFixture(10, 1, "test", "1", "?")},
		{"zero start", linuxStatFixture(10, 1, "test", "0", "S")},
		{"overflow start", linuxStatFixture(10, 1, "test", "18446744073709551616", "S")},
		{"long name", linuxStatFixture(10, 1, strings.Repeat("x", 257), "1", "S")},
		{"file overflow", []byte(strings.Repeat("x", maxLinuxProcessFileBytes+1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseLinuxProcessStat(10, test.raw); err == nil {
				t.Fatal("invalid stat admitted")
			}
		})
	}
	for _, raw := range []string{"", "Pss: 4 kB\n", "Rss: -1 kB\n", "Rss: 9007199254740992 kB\n", "Rss: 1 B\n", "Rss: 1 kB\nRss: 2 kB\n", strings.Repeat("x", 4097)} {
		if _, err := parseLinuxResidentBytes([]byte(raw)); err == nil {
			t.Fatalf("invalid RSS admitted: %.40q", raw)
		}
	}
	if rss, err := parseLinuxResidentBytes([]byte("Rss: 0 kB\n")); err != nil || rss != 0 {
		t.Fatalf("zero RSS=%d,%v", rss, err)
	}
}

func TestLinuxSnapshotRefusalAndTraversal(t *testing.T) {
	root := t.TempDir()
	writeLinuxProcessFixture(t, root, 10, 1)
	writeLinuxProcessFixture(t, root, 11, 10)
	writeLinuxProcessFixture(t, root, 12, 11)
	pids, rows, err := linuxProcessSnapshotAt(t.Context(), root, 10)
	if err != nil || !slices.Equal(pids, []int{10, 11, 12}) || len(rows) != 3 || rows[12].rssBytes != 4096 || !rows[12].coherent {
		t.Fatalf("tree=%v,%+v,%v", pids, rows, err)
	}
	before, err := linuxProcessStatAt(root, 11)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"lifetime", linuxStatFixture(11, 10, "test", "101", "S")},
		{"parent", linuxStatFixture(11, 1, "test", "100", "S")},
		{"image class", linuxStatFixture(11, 10, "git", "100", "S")},
		{"exit", linuxStatFixture(11, 10, "test", "100", "Z")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, "11", "stat"), test.raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := linuxProcessResidentBytes(root, 11, before); err == nil {
				t.Fatal("changed identity admitted")
			}
		})
	}
	if err := os.WriteFile(filepath.Join(root, "11", "stat"), linuxStatFixture(11, 10, "test", "100", "S"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "11", "smaps_rollup")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := linuxProcessSnapshotAt(t.Context(), root, 10); err == nil {
		t.Fatal("unavailable child RSS admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := linuxProcessSnapshotAt(ctx, root, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	//nolint:staticcheck // Explicit nil-context refusal regression.
	if _, _, err := linuxProcessSnapshotAt(nil, root, 10); err == nil {
		t.Fatal("nil context admitted")
	}
	if _, err := linuxProcessStatAt(root, 99); !errors.Is(err, errProcessIdentityMissing) {
		t.Fatalf("missing=%v", err)
	}
}

func TestLinuxSnapshotInventoryCaps(t *testing.T) {
	t.Run("descendants", func(t *testing.T) {
		root := t.TempDir()
		writeLinuxProcessFixture(t, root, 10, 1)
		for index := 0; index < MaxNativeProcessRecords; index++ {
			writeLinuxProcessFixture(t, root, index+11, 10)
		}
		if _, _, err := linuxProcessSnapshotAt(t.Context(), root, 10); err == nil {
			t.Fatal("descendant overflow admitted")
		}
	})
	t.Run("host census", func(t *testing.T) {
		root := t.TempDir()
		for index := 0; index <= maxProcessSnapshotRows; index++ {
			if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("entry-%d", index)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := linuxProcessSnapshotAt(t.Context(), root, 10); err == nil {
			t.Fatal("host census overflow admitted")
		}
	})
}

func TestLinuxNativeProcessTreeAndSampler(t *testing.T) {
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	rows, err := ObserveProcessTreeRecords(t.Context(), os.Getpid())
	if err != nil || len(rows) < 2 {
		t.Fatalf("real tree=%+v,%v", rows, err)
	}
	found := false
	for _, row := range rows {
		if row.PID == child.Process.Pid {
			found = true
			if row.ParentPID != os.Getpid() || row.StartIdentity == "" || row.RSSBytes <= 0 {
				t.Fatalf("child=%+v", row)
			}
		}
	}
	if !found {
		t.Fatal("live child omitted")
	}
	sampler := newRSSSampler(os.Getpid(), true)
	sampler.captureRootIdentity()
	sampler.sample()
	peak, _, _, others, err := sampler.metrics()
	if err != nil || peak <= 0 || others < 1 || sampler.failedSamples != 0 {
		t.Fatalf("sampler=%d,%d,%v", peak, others, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	deadline := time.Now().Add(time.Second)
	for {
		rows, err = ObserveProcessTreeRecords(t.Context(), os.Getpid())
		found = false
		for _, row := range rows {
			found = found || row.PID == child.Process.Pid
		}
		if err == nil && !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("exited child retained=%+v,%v", rows, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLinuxNativeSamplerAccountsExecClassEpoch(t *testing.T) {
	command := exec.CommandContext(t.Context(), "/bin/sh", "-c",
		`/bin/sh -c 'sleep 1; exec /usr/bin/git hash-object --stdin' child <&3 & wait`)
	read, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	command.ExtraFiles = []*os.File{read}
	defer func() { _ = read.Close() }()
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		_ = killPrivateServerSession(command.Process.Pid)
		_ = command.Wait()
		_ = finishCustodyCommandSession(command.Process.Pid)
	}()

	_ = read.Close()
	sampler := newRSSSampler(command.Process.Pid, true)
	sampler.captureRootIdentity()
	seenOther := make(map[processIdentity]struct{})
	transitioned := false
	deadline := time.Now().Add(5 * time.Second)
	for !transitioned && time.Now().Before(deadline) {
		sampler.sample()
		sampler.mu.Lock()
		for _, child := range sampler.activeChildren {
			if child.class == processClassOther {
				seenOther[child.identity] = struct{}{}
			}
			if child.class == processClassGit {
				_, transitioned = seenOther[child.identity]
			}
		}
		sampler.mu.Unlock()
		if !transitioned {
			time.Sleep(20 * time.Millisecond)
		}
	}
	sampler.sample()
	metrics, err := sampler.phaseMetrics()
	if err != nil || !transitioned || metrics.GitChildren != 1 || metrics.OtherChildren == 0 ||
		metrics.OtherToGitTransitions != 1 || sampler.failedSamples != 0 || sampler.samples < 2 {
		t.Fatalf("native exec epochs = transitioned:%t metrics:%+v samples:%d failed:%d err=%v",
			transitioned, metrics, sampler.samples, sampler.failedSamples, err)
	}
}

func TestLinuxResidentObservationRetriesOnlyNameTransition(t *testing.T) {
	census := linuxProcessStat{snapshot: processSnapshot{parent: 1, identityToken: "100", name: "sh"}}
	for _, test := range []struct {
		name         string
		probeErr     error
		change       string
		wantAttempts int
		wantError    bool
	}{
		{"settled", errLinuxProcessNameTransition, "name", 2, false},
		{"denied", os.ErrPermission, "name", 1, true},
		{"missing memory", os.ErrNotExist, "name", 1, true},
		{"parent", errLinuxProcessNameTransition, "parent", 1, true},
		{"lifetime", errLinuxProcessNameTransition, "lifetime", 1, true},
		{"unsettled", errLinuxProcessNameTransition, "unsettled", maxProcessSampleAttempts, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			_, err := collectLinuxResidentObservation(t.Context(), census,
				func(before linuxProcessStat) (processSnapshot, error) {
					attempts++
					if attempts == 1 || test.change == "unsettled" {
						return processSnapshot{}, test.probeErr
					}
					row := before.snapshot
					row.coherent = true
					row.rssBytes = 4096
					return row, nil
				}, func() (linuxProcessStat, error) {
					row := census
					row.snapshot.name = "git"
					if test.change == "parent" {
						row.snapshot.parent = 2
					}
					if test.change == "lifetime" {
						row.snapshot.identityToken = "101"
					}
					return row, nil
				})
			if (err != nil) != test.wantError || attempts != test.wantAttempts {
				t.Fatalf("attempts=%d,err=%v", attempts, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	_, err := collectLinuxResidentObservation(ctx, census, func(linuxProcessStat) (processSnapshot, error) { called = true; return processSnapshot{}, nil }, nil)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled attempt called=%t,err=%v", called, err)
	}
}

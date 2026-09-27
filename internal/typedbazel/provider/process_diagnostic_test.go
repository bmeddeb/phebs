package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestQuiescenceProcessState(t *testing.T) {
	for _, tc := range []struct {
		name, comm, state, start string
		valid                    bool
	}{
		{"live", "worker", "S", "123", true},
		{"zombie", "worker", "Z", "123", true},
		{"comm delimiters", "a) (b\nc)", "Z", "123", true},
		{"opaque comm bytes", "bad\xff\x00name", "S", "123", true},
		{"unknown state", "worker", "?", "123", false},
		{"long state", "worker", "ZZ", "123", false},
		{"zero lifetime", "worker", "Z", "0", false},
		{"bad lifetime", "worker", "Z", "bad", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := newDiagnosticFS(t)
			// These fields are irrelevant to state/lifetime observation; only
			// the separately requested diagnostic validates and retains them.
			proc.reads[0].data = diagnosticStatFixture(tc.comm, map[int]string{0: tc.state, 1: "unused", 2: "unused", 3: "unused", 19: tc.start})
			start, state, err := ReadProcessState(proc, 7)
			if (err == nil) != tc.valid || tc.valid && (start != 123 || state != tc.state[0]) {
				t.Fatalf("start=%d state=%q err=%v", start, state, err)
			}
			proc.verify(1)
		})
	}
	for _, kind := range []string{"open", "read", "close", "oversized", "PID mismatch", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			proc := newDiagnosticFS(t)
			switch kind {
			case "open":
				proc.reads[0].openErr = fs.ErrNotExist
			case "read":
				proc.reads[0].readErr = fs.ErrNotExist
			case "close":
				proc.reads[0].readErr = fs.ErrNotExist
				proc.reads[0].closeErr = fs.ErrPermission
			case "oversized":
				proc.reads[0].data += strings.Repeat(" ", 8193)
			case "PID mismatch":
				proc.reads[0].data = strings.Replace(proc.reads[0].data, "7 (", "8 (", 1)
			case "truncated":
				proc.reads[0].data = "7 (worker) Z"
			}
			if _, _, err := ReadProcessState(proc, 7); err == nil || ProcessGone(err) != (kind == "open" || kind == "read") {
				t.Fatal("state read failure classification changed", err)
			}
			proc.verify(1)
		})
	}
}

func TestQuiescenceProcessGone(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		gone bool
	}{
		{"nil", nil, false},
		{"missing", fs.ErrNotExist, true},
		{"exited", syscall.ESRCH, true},
		{"wrapped", fmt.Errorf("read: %w", syscall.ESRCH), true},
		{"joined missing", errors.Join(fs.ErrNotExist, syscall.ESRCH), true},
		{"mixed error", errors.Join(fs.ErrNotExist, fs.ErrPermission), false},
		{"nested mixed", fmt.Errorf("read: %w", errors.Join(syscall.ESRCH, errors.New("close"))), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ProcessGone(tc.err) != tc.gone {
				t.Fatal("unexpected disappearance classification")
			}
		})
	}
}

const diagnosticStatusFixture = "Name:\tprivate-name\nPid:\t7\nUid:\t0\t1\t2\t4294967295\nGid:\t3\t4\t5\t6\nNoNewPrivs:\t1\nCapPrm:\t00000000000000c0\nIgnored:\tprivate-payload\n"

func diagnosticStatFixture(comm string, changes map[int]string) string {
	fields := make([]string, 50)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[1], fields[2], fields[3], fields[19], fields[20], fields[21] = "S", "1", "7", "7", "123", "4096", "1"
	for index, value := range changes {
		fields[index] = value
	}
	return "7 (" + comm + ") " + strings.Join(fields, " ") + "\n"
}

type diagnosticRead struct {
	name, data                 string
	openErr, readErr, closeErr error
}

type diagnosticFS struct {
	t      *testing.T
	reads  []diagnosticRead
	opened []string
	bytes  []int
	closed int
}

func newDiagnosticFS(t *testing.T) *diagnosticFS {
	t.Helper()
	stat := diagnosticStatFixture("worker", nil)
	return &diagnosticFS{t: t, reads: []diagnosticRead{
		{name: "7/stat", data: stat},
		{name: "7/status", data: diagnosticStatusFixture},
		{name: "7/stat", data: stat},
	}}
}

func (proc *diagnosticFS) Open(name string) (fs.File, error) {
	index := len(proc.opened)
	proc.opened = append(proc.opened, name)
	proc.bytes = append(proc.bytes, 0)
	if index >= len(proc.reads) || name != proc.reads[index].name {
		proc.t.Fatalf("unexpected proc access %q at %d", name, index)
	}
	step := proc.reads[index]
	if step.openErr != nil {
		return nil, step.openErr
	}
	return &diagnosticFile{proc: proc, index: index, reader: strings.NewReader(step.data)}, nil
}

type diagnosticFile struct {
	proc   *diagnosticFS
	index  int
	reader io.Reader
}

func (file *diagnosticFile) Stat() (fs.FileInfo, error) {
	file.proc.t.Fatal("diagnostic must not inspect extra metadata")
	return nil, fs.ErrInvalid
}

func (file *diagnosticFile) Read(data []byte) (int, error) {
	n, err := file.reader.Read(data)
	file.proc.bytes[file.index] += n
	if readErr := file.proc.reads[file.index].readErr; readErr != nil {
		return n, readErr
	}
	return n, err
}

func (file *diagnosticFile) Close() error {
	file.proc.closed++
	return file.proc.reads[file.index].closeErr
}

func (proc *diagnosticFS) verify(reads int) {
	proc.t.Helper()
	if !slices.Equal(proc.opened, []string{"7/stat", "7/status", "7/stat"}[:reads]) {
		proc.t.Fatalf("unexpected read sequence: %v", proc.opened)
	}
	wantClosed := 0
	for index, count := range proc.bytes {
		if count > 8193 {
			proc.t.Fatalf("read %d bytes from slot %d", count, index)
		}
		if proc.reads[index].openErr == nil {
			wantClosed++
		}
	}
	if proc.closed != wantClosed {
		proc.t.Fatalf("closed %d files, want %d", proc.closed, wantClosed)
	}
}

func TestProcessDiagnosticAllowlistAndAccess(t *testing.T) {
	for _, expectedStart := range []uint64{0, 123} {
		t.Run(fmt.Sprint(expectedStart), func(t *testing.T) {
			proc := newDiagnosticFS(t)
			proc.reads[0].data = diagnosticStatFixture("a) (b\nc)", nil)
			proc.reads[2].data = proc.reads[0].data
			got := ReadProcessDiagnostic(proc, 7, expectedStart)
			proc.verify(3)
			if got == nil {
				t.Fatal("valid identity refused")
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			const want = `{"comm":"a) (b\nc)","state":"S","ppid":1,"pgid":7,"sid":7,"Uid":[0,1,2,4294967295],"Gid":[3,4,5,6],"NoNewPrivs":true,"CapPrm":"00000000000000c0"}`
			if string(data) != want {
				t.Fatalf("unexpected diagnostic allowlist/values: %s", data)
			}
		})
	}
	proc := newDiagnosticFS(t)
	proc.reads[1].data = strings.ReplaceAll(diagnosticStatusFixture, "NoNewPrivs:\t1", "NoNewPrivs:\t0")
	if got := ReadProcessDiagnostic(proc, 7, 123); got == nil || got.NoNewPrivs {
		t.Fatal("NoNewPrivs=0 was not retained")
	}
	proc.verify(3)
}

func TestProcessDiagnosticRejectsIdentityChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*diagnosticFS)
		reads  int
	}{
		{"first PID", func(p *diagnosticFS) { p.reads[0].data = strings.Replace(p.reads[0].data, "7 (", "8 (", 1) }, 1},
		{"status PID", func(p *diagnosticFS) { p.reads[1].data = strings.Replace(p.reads[1].data, "Pid:\t7", "Pid:\t8", 1) }, 2},
		{"last PID", func(p *diagnosticFS) { p.reads[2].data = strings.Replace(p.reads[2].data, "7 (", "8 (", 1) }, 3},
		{"expected lifetime", func(p *diagnosticFS) { p.reads[0].data = diagnosticStatFixture("worker", map[int]string{19: "124"}) }, 1},
		{"PID reuse", func(p *diagnosticFS) { p.reads[2].data = diagnosticStatFixture("worker", map[int]string{19: "124"}) }, 3},
		{"comm", func(p *diagnosticFS) { p.reads[2].data = diagnosticStatFixture("changed", nil) }, 3},
		{"state", func(p *diagnosticFS) { p.reads[2].data = diagnosticStatFixture("worker", map[int]string{0: "R"}) }, 3},
		{"ppid", func(p *diagnosticFS) { p.reads[2].data = diagnosticStatFixture("worker", map[int]string{1: "2"}) }, 3},
		{"pgid", func(p *diagnosticFS) { p.reads[2].data = diagnosticStatFixture("worker", map[int]string{2: "8"}) }, 3},
		{"sid", func(p *diagnosticFS) { p.reads[2].data = diagnosticStatFixture("worker", map[int]string{3: "8"}) }, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := newDiagnosticFS(t)
			tc.change(proc)
			if got := ReadProcessDiagnostic(proc, 7, 123); got != nil {
				t.Fatal("retained a changed identity")
			}
			proc.verify(tc.reads)
		})
	}
	for _, pid := range []uint32{0, 1 << 31, ^uint32(0)} {
		proc := newDiagnosticFS(t)
		if ReadProcessDiagnostic(proc, pid, 0) != nil {
			t.Fatal("invalid PID admitted")
		}
		proc.verify(0)
	}
	if ReadProcessDiagnostic(nil, 7, 0) != nil {
		t.Fatal("nil proc admitted")
	}
	proc := newDiagnosticFS(t)
	proc.reads[2].data = diagnosticStatFixture("worker", map[int]string{19: "124"})
	if ReadProcessDiagnostic(proc, 7, 0) != nil {
		t.Fatal("unavailable earlier start allowed PID reuse during capture")
	}
	proc.verify(3)
}

func TestProcessDiagnosticRejectsMalformedStat(t *testing.T) {
	for name, data := range map[string]string{
		"missing":          "",
		"no comm":          "7 S 1 7 7",
		"unclosed comm":    "7 (worker S 1 7 7",
		"wrong PID shape":  "07 (worker) S 1 7 7",
		"NUL comm":         diagnosticStatFixture("bad\x00name", nil),
		"invalid UTF8":     diagnosticStatFixture("bad\xffname", nil),
		"short":            "7 (worker) S 1 7 7",
		"invalid state":    diagnosticStatFixture("worker", map[int]string{0: "?"}),
		"long state":       diagnosticStatFixture("worker", map[int]string{0: "SS"}),
		"negative parent":  diagnosticStatFixture("worker", map[int]string{1: "-1"}),
		"overflow parent":  diagnosticStatFixture("worker", map[int]string{1: "2147483648"}),
		"signed group":     diagnosticStatFixture("worker", map[int]string{2: "+7"}),
		"nondecimal sid":   diagnosticStatFixture("worker", map[int]string{3: "0x7"}),
		"overflow start":   diagnosticStatFixture("worker", map[int]string{19: "18446744073709551616"}),
		"negative start":   diagnosticStatFixture("worker", map[int]string{19: "-123"}),
		"nondecimal start": diagnosticStatFixture("worker", map[int]string{19: "١٢٣"}),
	} {
		t.Run(name, func(t *testing.T) {
			for _, slot := range []int{0, 2} {
				proc := newDiagnosticFS(t)
				proc.reads[slot].data = data
				if ReadProcessDiagnostic(proc, 7, 0) != nil {
					t.Fatal("malformed stat admitted")
				}
				proc.verify(slot + 1)
			}
		})
	}
}

func TestProcessDiagnosticRejectsMalformedStatus(t *testing.T) {
	for _, key := range []string{"Pid", "Uid", "Gid", "NoNewPrivs", "CapPrm"} {
		for _, duplicate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/duplicate=%t", key, duplicate), func(t *testing.T) {
				proc := newDiagnosticFS(t)
				var lines []string
				for _, line := range strings.Split(diagnosticStatusFixture, "\n") {
					if strings.HasPrefix(line, key+":") {
						if duplicate {
							lines = append(lines, line, line)
						}
					} else {
						lines = append(lines, line)
					}
				}
				proc.reads[1].data = strings.Join(lines, "\n")
				if ReadProcessDiagnostic(proc, 7, 0) != nil {
					t.Fatal("missing/duplicate status field admitted")
				}
				proc.verify(2)
			})
		}
	}
	for _, tc := range []struct{ key, value string }{
		{"Pid", ""}, {"Pid", "+7"}, {"Pid", "7 7"}, {"Pid", "2147483648"},
		{"Uid", "0 1 2"}, {"Uid", "0 1 2 3 4"}, {"Uid", "0 1 2 4294967296"},
		{"Gid", "0 1 2 -1"}, {"Gid", "0 1 2 +1"}, {"Gid", "0 1 2 0x1"},
		{"NoNewPrivs", "2"}, {"NoNewPrivs", "true"}, {"NoNewPrivs", "0 1"},
		{"CapPrm", "0"}, {"CapPrm", "00000000000000C0"}, {"CapPrm", "00000000000000000"},
		{"CapPrm", "00000000000000gg"}, {"CapPrm", "00000000000000c0 extra"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			proc := newDiagnosticFS(t)
			lines := strings.Split(diagnosticStatusFixture, "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, tc.key+":") {
					lines[i] = tc.key + ":\t" + tc.value
				}
			}
			proc.reads[1].data = strings.Join(lines, "\n")
			if ReadProcessDiagnostic(proc, 7, 0) != nil {
				t.Fatal("invalid status value admitted")
			}
			proc.verify(2)
		})
	}
}

func TestProcessDiagnosticReadBoundsAndFailures(t *testing.T) {
	for slot := range 3 {
		for _, failure := range []string{"open", "read", "close", "oversized"} {
			t.Run(fmt.Sprintf("slot%d/%s", slot, failure), func(t *testing.T) {
				proc := newDiagnosticFS(t)
				err := errors.New("private error must not escape")
				switch failure {
				case "open":
					proc.reads[slot].openErr = err
				case "read":
					proc.reads[slot].readErr = err
				case "close":
					proc.reads[slot].closeErr = err
				case "oversized":
					proc.reads[slot].data += strings.Repeat(" ", 65536)
				}
				if ReadProcessDiagnostic(proc, 7, 0) != nil {
					t.Fatal("unavailable or oversized record admitted")
				}
				proc.verify(slot + 1)
				if failure == "oversized" && proc.bytes[slot] != 8193 {
					t.Fatalf("overflow sentinel read %d bytes", proc.bytes[slot])
				}
			})
		}
	}
	proc := newDiagnosticFS(t)
	for index := range proc.reads {
		proc.reads[index].data += strings.Repeat("\n", 8192-len(proc.reads[index].data))
	}
	if ReadProcessDiagnostic(proc, 7, 0) == nil {
		t.Fatal("exactly bounded record refused")
	}
	proc.verify(3)
}

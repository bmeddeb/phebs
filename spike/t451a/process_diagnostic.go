package t451a

import (
	"io"
	"io/fs"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ProcessDiagnostic is failure-only evidence. It deliberately excludes process
// PID, start time, executable paths, command lines, environments and raw errors.
type ProcessDiagnostic struct {
	Comm       string    `json:"comm"`
	State      string    `json:"state"`
	PPID       uint32    `json:"ppid"`
	PGID       uint32    `json:"pgid"`
	SID        uint32    `json:"sid"`
	UID        [4]uint32 `json:"Uid"`
	GID        [4]uint32 `json:"Gid"`
	NoNewPrivs bool      `json:"NoNewPrivs"`
	CapPrm     string    `json:"CapPrm"`
}

type diagnosticStat struct {
	comm, state     string
	ppid, pgid, sid uint32
	start           uint64
}

// ReadProcessDiagnostic makes one bounded stat/status/stat attempt. A nonzero
// expectedStart binds an earlier observation; zero means that reference is
// unavailable. Missing, malformed or changed identity yields no diagnostic and
// must never relax the caller's original refusal.
func ReadProcessDiagnostic(proc fs.FS, pid uint32, expectedStart uint64) *ProcessDiagnostic {
	if proc == nil || pid == 0 || pid > 1<<31-1 {
		return nil
	}
	prefix := strconv.FormatUint(uint64(pid), 10) + "/"
	before, ok := parseDiagnosticStat(readDiagnosticFile(proc, prefix+"stat"), pid)
	if !ok || expectedStart != 0 && before.start != expectedStart {
		return nil
	}
	diagnostic := parseDiagnosticStatus(readDiagnosticFile(proc, prefix+"status"), pid)
	if diagnostic == nil {
		return nil
	}
	after, ok := parseDiagnosticStat(readDiagnosticFile(proc, prefix+"stat"), pid)
	if !ok || before != after {
		return nil
	}
	diagnostic.Comm, diagnostic.State = before.comm, before.state
	diagnostic.PPID, diagnostic.PGID, diagnostic.SID = before.ppid, before.pgid, before.sid
	return diagnostic
}

func readDiagnosticFile(proc fs.FS, name string) []byte {
	file, err := proc.Open(name)
	if err != nil {
		return nil
	}
	// One sentinel byte distinguishes an 8192-byte record from truncation.
	data, readErr := io.ReadAll(io.LimitReader(file, 8193))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > 8192 {
		return nil
	}
	return data
}

func parseDiagnosticStat(data []byte, pid uint32) (diagnosticStat, bool) {
	var out diagnosticStat
	raw := string(data)
	prefix := strconv.FormatUint(uint64(pid), 10) + " ("
	end := strings.LastIndex(raw, ") ")
	if !strings.HasPrefix(raw, prefix) || end < len(prefix) {
		return out, false
	}
	out.comm = strings.Clone(raw[len(prefix):end])
	if !utf8.ValidString(out.comm) || strings.ContainsRune(out.comm, 0) {
		return diagnosticStat{}, false
	}
	fields := strings.Fields(raw[end+2:])
	if len(fields) < 20 || len(fields[0]) != 1 || !strings.ContainsAny(fields[0], "RSDZTtXxKWPI") {
		return diagnosticStat{}, false
	}
	out.state = strings.Clone(fields[0])
	for i, destination := range []*uint32{&out.ppid, &out.pgid, &out.sid} {
		value, ok := diagnosticUint(fields[i+1], 31)
		if !ok {
			return diagnosticStat{}, false
		}
		*destination = uint32(value)
	}
	start, ok := diagnosticUint(fields[19], 64)
	if !ok {
		return diagnosticStat{}, false
	}
	out.start = start
	return out, true
}

func parseDiagnosticStatus(data []byte, pid uint32) *ProcessDiagnostic {
	var out ProcessDiagnostic
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "Pid", "Uid", "Gid", "NoNewPrivs", "CapPrm":
		default:
			continue
		}
		if seen[key] {
			return nil
		}
		seen[key] = true
		fields := strings.Fields(value)
		switch key {
		case "Uid", "Gid":
			if len(fields) != 4 {
				return nil
			}
			var ids [4]uint32
			for i, field := range fields {
				id, ok := diagnosticUint(field, 32)
				if !ok {
					return nil
				}
				ids[i] = uint32(id)
			}
			if key == "Uid" {
				out.UID = ids
			} else {
				out.GID = ids
			}
		case "Pid":
			if len(fields) != 1 {
				return nil
			}
			value, ok := diagnosticUint(fields[0], 31)
			if !ok || value != uint64(pid) {
				return nil
			}
		case "NoNewPrivs":
			if len(fields) != 1 || fields[0] != "0" && fields[0] != "1" {
				return nil
			}
			out.NoNewPrivs = fields[0] == "1"
		case "CapPrm":
			if len(fields) != 1 || len(fields[0]) != 16 || strings.Trim(fields[0], "0123456789abcdef") != "" {
				return nil
			}
			out.CapPrm = strings.Clone(fields[0])
		}
	}
	if len(seen) != 5 {
		return nil
	}
	return &out
}

func diagnosticUint(value string, bits int) (uint64, bool) {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 10, bits)
	return parsed, err == nil
}

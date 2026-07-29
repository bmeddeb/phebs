package indexer

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestChildOutputForwardsCompleteAndPartialLines(t *testing.T) {
	var logs bytes.Buffer
	out := newChildOutput(log.New(&logs, "", 0), "repo: ", true)

	if _, err := out.Write([]byte("first\r\npartial")); err != nil {
		t.Fatal(err)
	}
	if got, want := logs.String(), "repo: first\n"; got != want {
		t.Fatalf("logs before Flush = %q, want %q", got, want)
	}

	out.Flush()
	if got, want := logs.String(), "repo: first\nrepo: partial\n"; got != want {
		t.Fatalf("logs after Flush = %q, want %q", got, want)
	}
	if got, want := out.String(), "first\\x0d\npartial"; got != want {
		t.Fatalf("captured output = %q, want %q", got, want)
	}
}

func TestChildOutputDisabledStillCapturesDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	out := newChildOutput(log.New(&logs, "", 0), "repo: ", false)

	if _, err := out.Write([]byte("diagnostic\n")); err != nil {
		t.Fatal(err)
	}
	out.Flush()
	if logs.Len() != 0 {
		t.Fatalf("disabled verbose output logged %q", logs.String())
	}
	if got, want := out.String(), "diagnostic\n"; got != want {
		t.Fatalf("captured output = %q, want %q", got, want)
	}
}

func TestChildOutputRepairsInvalidUTF8(t *testing.T) {
	var logs bytes.Buffer
	out := newChildOutput(log.New(&logs, "", 0), "repo: ", true)

	if _, err := out.Write([]byte{'a', 0xff, '\n'}); err != nil {
		t.Fatal(err)
	}
	if got, want := logs.String(), "repo: a\uFFFD\n"; got != want {
		t.Fatalf("logs = %q, want %q", got, want)
	}
	if got, want := out.String(), "a\uFFFD\n"; got != want {
		t.Fatalf("captured output = %q, want %q", got, want)
	}
}

func TestChildOutputEscapesTerminalControls(t *testing.T) {
	var logs bytes.Buffer
	out := newChildOutput(log.New(&logs, "", 0), "repo: ", true)

	payload := []byte("safe\t\x1b[31mred\rrewrite\x00\u0085end\n")
	if _, err := out.Write(payload); err != nil {
		t.Fatal(err)
	}
	if got, want := logs.String(),
		"repo: safe\t\\x1b[31mred\\x0drewrite\\x00\\x85end\n"; got != want {
		t.Fatalf("logs = %q, want %q", got, want)
	}
	if got, want := out.String(),
		"safe\t\\x1b[31mred\\x0drewrite\\x00\\x85end\n"; got != want {
		t.Fatalf("captured output = %q, want %q", got, want)
	}
}

func TestChildOutputChunksLongVerboseLines(t *testing.T) {
	var logs bytes.Buffer
	out := newChildOutput(log.New(&logs, "", 0), "repo: ", true)
	line := bytes.Repeat([]byte("x"), maxVerboseLineBytes+3)

	if _, err := out.Write(line); err != nil {
		t.Fatal(err)
	}
	out.Flush()

	got := strings.Split(strings.TrimSuffix(logs.String(), "\n"), "\n")
	if len(got) != 2 {
		t.Fatalf("log line count = %d, want 2", len(got))
	}
	if want := "repo: " + strings.Repeat("x", maxVerboseLineBytes) + " [continued]"; got[0] != want {
		t.Fatalf("first chunk length/content mismatch: len=%d", len(got[0]))
	}
	if got[1] != "repo: xxx" {
		t.Fatalf("final chunk = %q, want %q", got[1], "repo: xxx")
	}
}

func TestChildOutputRetainsBoundedTail(t *testing.T) {
	out := newChildOutput(nil, "", false)
	payload := append([]byte("discard-me:"), bytes.Repeat([]byte("z"), maxChildOutputBytes+17)...)

	if _, err := out.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	wantPrefix := "[... 28 earlier child-output bytes omitted ...]\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("captured output prefix = %q, want %q", got[:min(len(got), len(wantPrefix))], wantPrefix)
	}
	if tail := strings.TrimPrefix(got, wantPrefix); len(tail) != maxChildOutputBytes {
		t.Fatalf("captured tail bytes = %d, want %d", len(tail), maxChildOutputBytes)
	}
}

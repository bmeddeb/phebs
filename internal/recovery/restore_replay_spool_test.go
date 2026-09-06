package recovery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRestoreReplaySpoolUsesConsumedBytes(t *testing.T) {
	const original = "OPTION IMPORT; INSERT [{id: repo:one, body: 'original'}];"
	path, artifact := restoreReplayTestArtifact(t, original)
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	directory := t.TempDir()
	file, unit, err := prepared.spoolNext(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write([]byte("changed")); err == nil {
		t.Fatal("adopted spool retained writable descriptor")
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("spool remains named: %v %v", entries, err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(original, "original", "mutated!")), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(io.NewSectionReader(file, unit.Span.Start, unit.Span.End-unit.Span.Start))
	if err != nil || string(raw) != "{id: repo:one, body: 'original'}" {
		t.Fatalf("spool reread changed archive: %q %v", raw, err)
	}
	if _, err := prepared.next(t.Context()); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("late drift was accepted: %v", err)
	}
}

func TestRestoreReplayHTTPTransport(t *testing.T) {
	for _, mode := range []string{"success", "HTTP error", "native error", "redirect", "late drift"} {
		t.Run(mode, func(t *testing.T) {
			const definition = "DEFINE TABLE repo TYPE ANY SCHEMALESS PERMISSIONS NONE;"
			const record = "{id: repo:one, body: 'neutral'}"
			parts := make([]string, 513)
			for index := range parts {
				parts[index] = record
			}
			raw := "-- preserved export\nOPTION IMPORT;\n" + definition + "\nINSERT [" + strings.Join(parts, ", ") + "];\n"
			path, artifact := restoreReplayTestArtifact(t, raw)
			prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.close() }()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				ordinal := calls.Add(1)
				body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
				if err != nil {
					t.Error(err)
				}
				user, password, ok := request.BasicAuth()
				wantPath, wantNS, wantDB := "/import", "phebs", "phebs"
				if ordinal <= 2 {
					wantPath, wantDB = "/sql", ""
					if ordinal == 1 {
						wantNS = ""
					}
				}
				if !ok || user != "root" || password != "root" || request.Method != http.MethodPost || request.URL.Path != wantPath ||
					request.Header.Get("Surreal-NS") != wantNS || request.Header.Get("Surreal-DB") != wantDB ||
					request.ContentLength != int64(len(body)) || len(request.TransferEncoding) != 0 {
					t.Error("request envelope differs from fixed import recipe")
				}
				want := "OPTION IMPORT; BEGIN;\n" + definition + "\n\nCOMMIT;"
				switch ordinal {
				case 1:
					want = "BEGIN;\nDEFINE NAMESPACE IF NOT EXISTS phebs;\nCOMMIT;"
				case 2:
					want = "BEGIN;\nDEFINE DATABASE IF NOT EXISTS phebs;\nCOMMIT;"
				case 4:
					want = "OPTION IMPORT; BEGIN;\nINSERT [" + strings.Join(parts[:512], ", ") + "];\nCOMMIT;"
				case 5:
					want = "OPTION IMPORT; BEGIN;\nINSERT [" + record + "];\nCOMMIT;"
				}
				if string(body) != want {
					t.Errorf("unit %d payload changed", ordinal)
				}
				if ordinal <= 2 {
					_, _ = io.WriteString(writer, `[{"result":null,"status":"OK","time":"0ns","type":null},{"result":null,"status":"OK","time":"0ns","type":null},{"result":null,"status":"OK","time":"0ns","type":null}]`)
					return
				}
				switch mode {
				case "HTTP error":
					writer.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				case "native error":
					_, _ = io.WriteString(writer, `[{"result":"failed","status":"ERR","time":"0ns","type":null}]`)
					return
				case "redirect":
					writer.Header().Set("Location", "/elsewhere")
					writer.WriteHeader(http.StatusTemporaryRedirect)
					return
				case "late drift":
					if err := os.WriteFile(path, []byte(strings.ReplaceAll(raw, "neutral", "changed")), 0o600); err != nil {
						t.Error(err)
					}
				}
				result := "null"
				if ordinal > 3 {
					result = "[]"
				}
				_, _ = fmt.Fprintf(writer, `[{"result":%s,"status":"OK","time":"0ns","type":null},{"result":null,"status":"OK","time":"0ns","type":null}]`, result)
			}))
			defer server.Close()
			target := t.TempDir()
			retained := filepath.Join(target, "retained-partial-target")
			if err := os.WriteFile(retained, []byte("owned database prefix"), 0o600); err != nil {
				t.Fatal(err)
			}
			err = executeRestoreReplay(t.Context(), prepared, target, strings.Replace(server.URL, "http://", "ws://", 1), DatabaseIdentity{Namespace: "phebs", Database: "phebs"})
			wantCalls := int32(3)
			if mode == "success" {
				wantCalls = 5
			}
			if (err == nil) != (mode == "success") || calls.Load() != wantCalls {
				t.Fatalf("result=%v requests=%d want=%d", err, calls.Load(), wantCalls)
			}
			if mode != "success" {
				if _, nextErr := prepared.next(t.Context()); nextErr == nil || errors.Is(nextErr, io.EOF) {
					t.Fatalf("failed submission allowed prepared re-entry: %v", nextErr)
				}
			}
			if entries, err := os.ReadDir(target); err != nil || len(entries) != 1 || entries[0].Name() != "retained-partial-target" {
				t.Fatalf("temporary spool leak: %v %v", entries, err)
			}
			if got, err := os.ReadFile(retained); err != nil || string(got) != "owned database prefix" {
				t.Fatalf("failed target prefix was altered: %q %v", got, err)
			}
			if err := requireEmptyOrAbsent(target); err == nil {
				t.Fatal("subsequent restore accepted retained partial target")
			}
		})
	}
}

func TestRestoreReplayCaptureFailure(t *testing.T) {
	path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT; INSERT [{body:'"+strings.Repeat("x", 128<<10)+"'}];")
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	capture := bufio.NewWriterSize(restoreReplayRejectedWriter{}, restoreReplayBufferBytes)
	if _, err := prepared.nextCaptured(t.Context(), capture); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("capture failure lost: %v", err)
	}
	if prepared.scanner.offset >= artifact.Size {
		t.Fatal("capture failure drained remaining record instead of stopping")
	}
	if _, err := prepared.next(t.Context()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("capture failure was not terminal: %v", err)
	}
}

type restoreReplayRejectedWriter struct{}

func (restoreReplayRejectedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRestoreReplayPostParseCancellationIsTerminal(t *testing.T) {
	path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT; INSERT [{body:'neutral'}];")
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Actual cancellation occurs only once recognition has advanced the unit
	// census, at spoolNext's post-parse/pre-adoption context check.
	afterParse := &restoreReplayCancelAfterParse{Context: ctx, prepared: prepared, cancel: cancel}
	directory := t.TempDir()
	if file, _, err := prepared.spoolNext(afterParse, directory); file != nil || !errors.Is(err, context.Canceled) || prepared.seen.Units != 1 {
		t.Fatalf("post-parse cancellation: file=%v seen=%d error=%v", file, prepared.seen.Units, err)
	}
	if _, err := prepared.next(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled never-submitted unit was skipped: %v", err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("canceled spool leaked: %v %v", entries, err)
	}
}

type restoreReplayCancelAfterParse struct {
	context.Context
	prepared *preparedRestoreReplay
	cancel   context.CancelFunc
}

func (ctx *restoreReplayCancelAfterParse) Err() error {
	if ctx.prepared.seen.Units == 1 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestRestoreReplayHTTPCanceledAndEarlyRefusal(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			// Large enough that the server can refuse before the transport
			// finishes reading the body; Close must join that read.
			path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT; INSERT [{body:'"+strings.Repeat("x", 2<<20)+"'}];")
			prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.close() }()
			file, unit, err := prepared.spoolNext(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writer.WriteHeader(http.StatusRequestEntityTooLarge)
			}))
			defer server.Close()
			transport := &http.Transport{MaxConnsPerHost: 1}
			defer transport.CloseIdleConnections()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			err = submitRestoreReplayUnit(ctx, &http.Client{Transport: transport}, server.URL, DatabaseIdentity{Namespace: "neutral", Database: "neutral"}, file, unit)
			if err == nil || (canceled && (!errors.Is(err, context.Canceled) || calls.Load() != 0)) {
				t.Fatalf("canceled=%t error=%v calls=%d", canceled, err, calls.Load())
			}
		})
	}
}

func TestRestoreReplayBootstrapFailureIsTerminal(t *testing.T) {
	path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT; INSERT [{body:'neutral'}];")
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(writer, `[{"result":null,"status":"OK","time":"0ns","type":null},{"result":"failed","status":"ERR","time":"0ns","type":null},{"result":"aborted","status":"ERR","time":"0ns","type":null}]`)
	}))
	defer server.Close()
	target := t.TempDir()
	for range 2 {
		if err := executeRestoreReplay(t.Context(), prepared, target, strings.Replace(server.URL, "http://", "ws://", 1), DatabaseIdentity{Namespace: "phebs", Database: "phebs"}); err == nil {
			t.Fatal("failed namespace bootstrap was accepted")
		}
	}
	if calls.Load() != 1 || prepared.seen.Units != 0 {
		t.Fatalf("failed namespace bootstrap replayed or submitted archive: calls=%d units=%d", calls.Load(), prepared.seen.Units)
	}
}

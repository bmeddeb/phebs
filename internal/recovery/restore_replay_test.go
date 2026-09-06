package recovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

// AssertRestoreReplaySupportedForTest bridges the external complete-archive
// regression to the private recognizer without adding a production selector.
func AssertRestoreReplaySupportedForTest(ctx context.Context, t *testing.T, backup string, manifest Manifest) {
	t.Helper()
	if !restoreReplayNonblockingAvailable || manifest.Surreal.Version != "3.2.0" {
		t.Logf("protected replay branch unselected: nonblocking=%t, native version=%s; ordinary restore coverage continues",
			restoreReplayNonblockingAvailable, manifest.Surreal.Version)
		return
	}
	for _, artifact := range manifest.Inventory {
		if artifact.Path != DatabaseName {
			continue
		}
		prepared, err := prepareRestoreReplay(ctx, filepath.Join(backup, DatabaseName), artifact)
		if err != nil {
			t.Fatalf("real backup export is not recognized for protected replay: %v", err)
		}
		if err := prepared.close(); err != nil {
			t.Fatalf("close real backup replay preparation: %v", err)
		}
		return
	}
	t.Fatal("real backup manifest lacks its database artifact")
}

func restoreReplayTestArtifact(t *testing.T, raw string) (string, Artifact) {
	t.Helper()
	path := filepath.Join(t.TempDir(), DatabaseName)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, Artifact{Path: DatabaseName, Size: int64(len(raw)), SHA256: digestBytes([]byte(raw))}
}

func TestRestoreReplayDigitLeadingRecordIDs(t *testing.T) {
	for _, id := range []string{"1", "1234567890", "1fe44fe9046fd23d55ff4dd34fd046827a8004d6c1ba5379fb9eedeb4f6838d9", "01neutral", "1e2"} {
		t.Run(id, func(t *testing.T) {
			record := "{id: generation_schedule:" + id + ", original: 'unchanged'}"
			raw := "OPTION IMPORT; INSERT [" + record + "];"
			scanner := newRestoreReplayScanner(strings.NewReader(raw))
			unit, err := scanner.next()
			if err != nil || unit.Count != 1 || unit.Definition || raw[unit.Span.Start:unit.Span.End] != record {
				t.Fatalf("native digit-leading record ID changed/refused: %+v %v", unit, err)
			}
			if _, err := scanner.next(); !errors.Is(err, io.EOF) {
				t.Fatalf("record terminator: %v", err)
			}
		})
	}
	for _, id := range []string{"1neutral()", "1neutral::run()", "1neutral + 2", "1neutral/*comment*/", "1neutral; DELETE generation_schedule"} {
		scanner := newRestoreReplayScanner(strings.NewReader("OPTION IMPORT; INSERT [{id: generation_schedule:" + id + "}];"))
		var unsupported *restoreReplayUnsupported
		if _, err := scanner.next(); !errors.As(err, &unsupported) {
			t.Fatalf("nonliteral record ID accepted: %q %v", id, err)
		}
	}
}

func TestRestoreReplayRecordBoundaries(t *testing.T) {
	for _, count := range []int{511, 512, 513, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			const record = `{ body: "neutral };],; -- /* \\ \" ' ` + "`" + ` text\nnext", id: repo:` + "`0`" + `, nested: [1, [true, 'a,b'], {at: d'2026-01-01T00:00:00Z', elapsed: 2h30m, uuid: u'0199b4e8-306e-7000-8000-000000000000'}] }`
			records := make([]string, count)
			for i := range records {
				records[i] = record
			}
			raw := "-- native export\nOPTION IMPORT;\nINSERT [ " + strings.Join(records, ", ") + " ];\n"
			path, artifact := restoreReplayTestArtifact(t, raw)
			prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.close() }()
			if prepared.census.Records != uint64(count) || prepared.census.Units != uint64((count+511)/512) {
				t.Fatalf("preflight census: %+v", prepared.census)
			}
			seen := 0
			for {
				unit, err := prepared.next(t.Context())
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if unit.Count <= 0 || unit.Count > 512 {
					t.Fatalf("unit record count: %d", unit.Count)
				}
				if unit.Definition || raw[unit.Span.Start:unit.Span.End] != strings.Join(records[:unit.Count], ", ") {
					t.Fatal("contiguous record payload/separator bytes changed")
				}
				seen += unit.Count
			}
			if seen != count {
				t.Fatalf("replay records = %d", seen)
			}
		})
	}
}

func TestRestoreReplayUnsupportedAndTruncated(t *testing.T) {
	for _, body := range []string{
		"DEFINE FUNCTION fn::script() { RETURN function() { return `};${'x'}`; } };",
		"DEFINE TABLE repo AS SELECT * FROM other;",
		"INSERT [{id: repo:one, x: function() { return 1; }}];",
		"INSERT [{id: repo:one, x: /[};]/}];",
		"INSERT [{id: repo:one, x: $parameter}];",
		"INSERT [{id: repo:one, x: (DELETE repo)}];",
		"INSERT [{id: repo:one, x: time::now()}];",
		"INSERT [{id: repo:one}]; DELETE repo;",
		"INSERT [{id: repo:one, x: 1 /* }; */}];",
		"DEFINE TABLE " + strings.Repeat("x", restoreReplayDefinitionBytes) + " TYPE ANY SCHEMALESS PERMISSIONS NONE;",
		"DEFINE FIELD OVERWRITE value ON repo TYPE string VALUE time::now() PERMISSIONS FULL;",
		"DEFINE INDEX idx ON repo FIELDS value CONCURRENTLY;",
		"DEFINE EVENT custom ON repo WHEN true THEN { DELETE repo; };",
		"DEFINE EVENT custom ON repo WHEN true\nTHEN { THROW 'custom'; };",
		"INSERT [{id: repo:one, x:" + strings.Repeat("[", restoreReplayDepthLimit) + "1" + strings.Repeat("]", restoreReplayDepthLimit) + "}];",
	} {
		path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT;\n"+body)
		prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
		var unsupported *restoreReplayUnsupported
		if prepared != nil || !errors.As(err, &unsupported) {
			t.Fatalf("unsupported input result = %v, %v", prepared, err)
		}
	}
	for _, raw := range []string{"", "OPTION", "OPTION IMPORT; INSERT [", "OPTION IMPORT; INSERT [{id: repo:one", "OPTION IMPORT; INSERT [{x: 'unfinished\\"} {
		// An empty file is rejected at the identity boundary; recognized but
		// incomplete syntax must never be an ordinary-fallback result either.
		path, artifact := restoreReplayTestArtifact(t, raw)
		prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
		var unsupported *restoreReplayUnsupported
		if prepared != nil || err == nil || errors.As(err, &unsupported) {
			t.Fatalf("truncated input result = %v, %v", prepared, err)
		}
	}
}

func TestRestoreReplayCancellationDigestAndDrift(t *testing.T) {
	const raw = "OPTION IMPORT; INSERT [{id: repo:one}];"
	path, artifact := restoreReplayTestArtifact(t, raw)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := prepareRestoreReplay(ctx, path, artifact); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preflight = %v", err)
	}
	wrong := artifact
	wrong.SHA256 = digestBytes([]byte("wrong"))
	if _, err := prepareRestoreReplay(t.Context(), path, wrong); err == nil {
		t.Fatal("wrong artifact digest accepted")
	}
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.next(t.Context()); err == nil {
		t.Fatal("late artifact drift accepted")
	}
	// Unsupported syntax cannot hide a later manifest mismatch as fallback.
	path, artifact = restoreReplayTestArtifact(t, "OPTION IMPORT; DEFINE FUNCTION fn::x() {};\n")
	artifact.SHA256 = wrong.SHA256
	var unsupported *restoreReplayUnsupported
	if _, err := prepareRestoreReplay(t.Context(), path, artifact); err == nil || errors.As(err, &unsupported) {
		t.Fatalf("unsupported plus drift selected fallback: %v", err)
	}
}

func TestRestoreReplayStreamingValuesAndReadErrors(t *testing.T) {
	for _, value := range []string{"1dec", "-1.25dec", "2.5f", "1.2e-10", "2h30m", "123ns", "7us", "3ms", "r'repo:one'", "NULL", "NONE", "repo:`escaped\\`identifier`", `repo:'quoted \' identifier'`} {
		path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT; INSERT [{id: repo:one, value: "+value+"}];")
		prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
		if err != nil {
			t.Fatalf("typed literal %s: %v", value, err)
		}
		if err := prepared.close(); err != nil {
			t.Fatal(err)
		}
	}
	// No record/statement byte cap substitutes for the archive's existing cap.
	large := "OPTION IMPORT; INSERT [{id: repo:one, value: '" + strings.Repeat("x", 9<<20) + "'}];"
	path, artifact := restoreReplayTestArtifact(t, large)
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.census.Records != 1 {
		t.Fatal("large record changed the row unit")
	}
	if err := prepared.close(); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{context.Canceled, io.ErrClosedPipe} {
		for _, prefix := range []string{"'", "2d", "2h", "3m", "1", "false", "time:", "[", "{name"} {
			data := "OPTION IMPORT; INSERT [{value: " + prefix
			for _, reader := range []io.Reader{
				io.MultiReader(strings.NewReader(data), restoreReplayTestReadError{failure}),
				&restoreReplayDataAndError{data: []byte(data), err: failure},
			} {
				scanner := newRestoreReplayScanner(reader)
				if _, err := scanner.next(); !errors.Is(err, failure) {
					t.Fatalf("reader failure after %q became syntax/EOF: %v", prefix, err)
				}
			}
		}
	}
	// bufio may retain an error delivered with bytes. Unsupported-source
	// draining must use that same buffer, not skip directly to the source.
	scanner := newRestoreReplayScanner(&restoreReplayDataAndError{data: []byte("OPTION IMPORT; DEFINE FUNCTION fn::custom() {};\n"), err: io.ErrClosedPipe})
	var unsupportedRead *restoreReplayUnsupported
	if _, err := scanner.next(); !errors.As(err, &unsupportedRead) {
		t.Fatalf("unsupported buffered form = %v", err)
	}
	if err := scanner.drain(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("buffered read failure lost during drain: %v", err)
	}
	path, artifact = restoreReplayTestArtifact(t, "OPTION IMPORT;\n")
	artifact.Size = maxArtifactBytes + 1
	if _, err := prepareRestoreReplay(t.Context(), path, artifact); err == nil {
		t.Fatal("artifact cap not enforced")
	}
	path, artifact = restoreReplayTestArtifact(t, "OPTION IMPORT; DEFINE FUNCTION fn::custom() {};\n-- "+strings.Repeat("x", 128<<10))
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &restoreReplayCancelOnCheck{Context: base, cancel: cancel, remaining: 4}
	var unsupported *restoreReplayUnsupported
	if _, err := prepareRestoreReplay(ctx, path, artifact); !errors.Is(err, context.Canceled) || errors.As(err, &unsupported) {
		t.Fatalf("unsupported drain hid cancellation: %v", err)
	}
}

type restoreReplayTestReadError struct{ err error }

func (reader restoreReplayTestReadError) Read([]byte) (int, error) { return 0, reader.err }

type restoreReplayDataAndError struct {
	data []byte
	err  error
}

func (reader *restoreReplayDataAndError) Read(raw []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	n := copy(raw, reader.data)
	reader.data = reader.data[n:]
	return n, reader.err
}

func TestRestoreReplayLaterUnitContext(t *testing.T) {
	first := strings.TrimSuffix(strings.Repeat("{id: repo:one},", 512), ",")
	raw := "OPTION IMPORT; INSERT [" + first + ",{id: repo:large, value:'" + strings.Repeat("x", 128<<10) + "'}];"
	path, artifact := restoreReplayTestArtifact(t, raw)
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	if unit, err := prepared.next(t.Context()); err != nil || unit.Count != 512 {
		t.Fatalf("first unit: count=%d err=%v", unit.Count, err)
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	// The first Err check admits next(), and its next underlying buffer refill
	// cancels the new call context. The old implementation retained first-call
	// context and read the whole large value before detecting this cancellation.
	ctx := &restoreReplayCancelOnCheck{Context: base, cancel: cancel, remaining: 2}
	before := prepared.scanner.offset
	if _, err := prepared.next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("later-unit cancellation = %v", err)
	}
	if prepared.scanner.offset-before >= 64<<10 {
		t.Fatal("later call context did not cancel the next buffered read")
	}
}

type restoreReplayCancelOnCheck struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (ctx *restoreReplayCancelOnCheck) Err() error {
	ctx.remaining--
	if ctx.remaining == 0 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

// The fixture is generated only by the separately opted-in native test below.
// This selector reads it without starting an engine or attempting a restore.
func TestRestoreReplayOwnedExportCoverage(t *testing.T) {
	path := os.Getenv("PHEBS_RESTORE_REPLAY_FIXTURE_INPUT")
	if path == "" {
		t.Skip("owned native export fixture input not selected")
	}
	artifact, err := inspectArtifact(t.Context(), path, DatabaseName, "precious", "application/surrealql")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	if prepared.census.Definitions != 716 || prepared.census.Records == 0 {
		t.Fatalf("actual owned export census changed: %+v", prepared.census)
	}
	for {
		if _, err := prepared.next(t.Context()); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual owned export preflight/replay census: %+v", prepared.census)
}

// TestRestoreReplayOwnedExportFixture is an opt-in, neutral native-format
// fixture author. It proves no restore, replay, phase accounting, or equivalence.
func TestRestoreReplayOwnedExportFixture(t *testing.T) {
	output := os.Getenv("PHEBS_RESTORE_REPLAY_FIXTURE_OUTPUT")
	if output == "" {
		t.Skip("owned native export fixture not selected")
	}
	if !filepath.IsAbs(output) || filepath.Clean(output) != output {
		t.Fatal("fixture output must be an absolute clean absent directory")
	}
	if err := requireAbsent(output, "native fixture output"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	directory := t.TempDir()
	s, err := store.OpenLocal(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("close native fixture store: %v", err)
		}
	}()
	if err := s.UpsertRepo(ctx, store.Repo{Name: "example.com/neutral/replay", CloneURL: "https://example.com/neutral/replay.git"}); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.ReadLocalRuntime(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(output, DatabaseName)
	if err := runSurreal(ctx, runtime.Surreal.Path, []string{
		"export", "--endpoint", cliEndpoint(runtime.Endpoint), "--namespace", "phebs", "--database", "phebs", "--log", "none", path,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := inspectArtifact(ctx, path, DatabaseName, "precious", "application/surrealql")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("neutral owned-schema fixture retained: %s bytes=%d digest=%s", path, artifact.Size, artifact.SHA256)
}

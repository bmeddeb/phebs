package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	t421fixture "github.com/bmeddeb/phebs/spike/t421"
)

const t422NativeQuerySchema = "t422-query-native-helper-v1"

type t422NativeQueryResult struct {
	Schema      string                              `json:"schema"`
	NextOrdinal uint64                              `json:"next_ordinal"`
	Rows        []t421fixture.ExecutionProductQuery `json:"rows"`
}

// This optional fixture compiles the real private wire/projector tests with
// race instrumentation even when its enclosing command test is not raced.
// It does not construct or claim selected epoch-five lifecycle/DA admission.
func t422BuildNativeQueryProjection(t *testing.T, ctx context.Context, moduleRoot, workspace string) {
	t.Helper()
	goRoot := os.Getenv("GOROOT")
	if !filepath.IsAbs(goRoot) || filepath.Clean(goRoot) != goRoot {
		t.Fatal("native query helper build requires the gate's absolute pinned GOROOT")
	}
	settings := map[string]string{"GOROOT": goRoot, "GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"}
	environment := make([]string, 0, len(os.Environ())+len(settings))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := settings[key]; !replaced && !strings.HasPrefix(key, "PHEBS_") {
			environment = append(environment, item)
		}
	}
	for key, value := range settings {
		environment = append(environment, key+"="+value)
	}
	args := []string{filepath.Join(goRoot, "bin", "go"), "test", "-c", "-race", "-trimpath", "-o", filepath.Join(workspace, "bin", "t422-query.test"), "./spike/t421"}
	if err := t422NativeQueryCommand(ctx, moduleRoot, filepath.Join(workspace, "query-build.log"), args, environment, nil, nil); err != nil {
		t.Fatalf("build native query helper (private query-build.log): %v", err)
	}
}

func t422RunNativeQueryProjection(t *testing.T, ctx context.Context, moduleRoot, workspace, listen, repository, apiKey string,
	ordinal uint64, catalog, final []byte, queries []t421fixture.QueryCase,
) uint64 {
	t.Helper()
	if len(catalog) == 0 || len(catalog) > 16<<20 || len(final) == 0 || len(final) > 1<<20 || ordinal == 0 || ordinal > math.MaxUint64-38 {
		t.Fatal("native query helper input exceeds fixture bounds")
	}
	request := struct {
		Schema      string          `json:"schema"`
		Listen      string          `json:"listen"`
		Repository  string          `json:"repository"`
		APIKey      string          `json:"api_key"`
		NextOrdinal uint64          `json:"next_ordinal"`
		Catalog     json.RawMessage `json:"catalog"`
		Final       json.RawMessage `json:"final"`
	}{t422NativeQuerySchema, listen, repository, apiKey, ordinal, catalog, final}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > (17<<20)+(64<<10) {
		t.Fatal("native query helper request encoding refused")
	}
	result, err := os.OpenFile(filepath.Join(workspace, "query-result.json"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := result.Stat()
	if err != nil || !prior.Mode().IsRegular() || prior.Mode().Perm() != 0o600 || prior.Size() != 0 {
		_ = result.Close()
		t.Fatal("native query result custody refused")
	}
	defer func() {
		if err := result.Close(); err != nil {
			t.Errorf("close native query result: %v", err)
		}
	}()
	args := []string{filepath.Join(workspace, "bin", "t422-query.test"), "-test.run=^TestEpochNativeQueryHelper$", "-test.count=1", "-test.timeout=20m"}
	environment := []string{"PHEBS_T422_NATIVE_QUERY_HELPER=1", "GORACE=atexit_sleep_ms=0"}
	if err := t422NativeQueryCommand(ctx, moduleRoot, filepath.Join(workspace, "query-helper.log"), args, environment, bytes.NewReader(raw), []*os.File{result}); err != nil {
		t.Fatalf("native query helper refused (private query-helper.log): %v", err)
	}
	// The sole Wait has completed, including both bounded diagnostic copiers;
	// no running writer can change the accepted result afterward.
	info, err := result.Stat()
	if err != nil || !os.SameFile(prior, info) || info.Mode() != prior.Mode() || info.Size() <= 0 || info.Size() > 64<<10 {
		t.Fatal("native query result size refused")
	}
	if _, err := result.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(io.LimitReader(result, (64<<10)+1))
	if err != nil {
		t.Fatal(err)
	}
	next, rows, err := t422DecodeNativeQueryResult(encoded, ordinal, queries)
	if err != nil {
		t.Fatal(err)
	}
	var members uint64
	for _, row := range rows {
		if row.MemberVisits > math.MaxUint64-members {
			t.Fatal("native query member total overflow")
		}
		members += row.MemberVisits
	}
	t.Logf("native query projections: rows=%d requests=38 C=160 S=164 M=%d next_ordinal=%d; actual ordinary auth/exact-read, not selected lifecycle", len(rows), members, next)
	return next
}

func t422DecodeNativeQueryResult(raw []byte, ordinal uint64, queries []t421fixture.QueryCase) (uint64, []t421fixture.ExecutionProductQuery, error) {
	var result t422NativeQueryResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 64<<10 || decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Schema != t422NativeQuerySchema ||
		ordinal == 0 || ordinal > math.MaxUint64-38 || result.NextOrdinal != ordinal+38 || len(queries) != 11 || len(result.Rows) != 22 {
		return 0, nil, errors.New("native query result identity refused")
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(bytes.TrimSuffix(raw, []byte{'\n'}), canonical) {
		return 0, nil, errors.New("native query result encoding refused")
	}
	next, controls, stores := ordinal, uint64(0), uint64(0)
	for i, row := range result.Rows {
		query := queries[i%len(queries)]
		transport, code := "http", strconv.Itoa(int(query.ExpectedStatus))
		if i >= len(queries) {
			transport, code = "mcp", query.ExpectedMCPCode
		}
		pages := t421BlackBoxQueryPages(query)
		if row.Name != query.Name || row.Transport != transport || row.Code != code || row.ProjectionSHA256 != query.ProjectionSHA256 ||
			row.VisibleRepositoriesObserved != (query.Name == "all_code_structural_marker") || query.Name == "all_code_structural_marker" && row.VisibleRepositories != 1 || query.Name != "all_code_structural_marker" && row.VisibleRepositories != 0 ||
			row.Records != query.ExpectedRecords || row.Paths != query.ExpectedPaths || row.Pages != pages || pages == 0 ||
			row.FirstOrdinal != next || row.LastOrdinal < next || row.LastOrdinal-next != pages-1 || row.LastOrdinal >= result.NextOrdinal ||
			row.ControlFileReads > 160-controls || row.StoreReadAttempts > 164-stores {
			return 0, nil, errors.New("native query result row refused")
		}
		next = row.LastOrdinal + 1
		controls += row.ControlFileReads
		stores += row.StoreReadAttempts
	}
	if next != result.NextOrdinal || controls != 160 || stores != 164 {
		return 0, nil, errors.New("native query result accounting refused")
	}
	return next, result.Rows, nil
}

// One serial process and one aggregate diagnostic cap. A regular result FD
// avoids a second pipe whose unread output could deadlock Wait.
func t422NativeQueryCommand(ctx context.Context, dir, logPath string, args, environment []string, input io.Reader, files []*os.File) error {
	op, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(op, args[0], args[1:]...)
	command.Dir, command.Env, command.Stdin, command.ExtraFiles = dir, environment, input, files
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = 5 * time.Second
	output := &t422NativeQueryOutput{cancel: cancel}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return fmt.Errorf("start native query fixture command: %w", err)
	}
	err := command.Wait()
	// This proves this owned process group, not arbitrary sessions or the host.
	if groupErr := syscall.Kill(-command.Process.Pid, 0); groupErr != syscall.ESRCH {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		err = errors.Join(err, errors.New("native query fixture process group did not close"))
	}
	err = errors.Join(err, output.err, os.WriteFile(logPath, output.buffer.Bytes(), 0o600), ctx.Err())
	return err
}

type t422NativeQueryOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	cancel context.CancelFunc
	err    error
}

func (output *t422NativeQueryOutput) Write(raw []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.err != nil {
		return 0, output.err
	}
	remaining := (1 << 20) - output.buffer.Len()
	if len(raw) > remaining {
		_, _ = output.buffer.Write(raw[:remaining])
		output.err = errors.New("native query fixture diagnostic cap exceeded")
		output.cancel()
		return remaining, output.err
	}
	return output.buffer.Write(raw)
}

func TestT422NativeQueryCommandChild(t *testing.T) {
	switch os.Getenv("PHEBS_T422_QUERY_COMMAND_CHILD") {
	case "small":
		_, _ = os.Stdout.Write([]byte("joined helper\n"))
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, (1<<20)+1))
	case "wait":
		<-t.Context().Done()
	}
}

func TestT422NativeQueryCommand(t *testing.T) {
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"small", "overflow", "wait"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if mode == "wait" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			logPath := filepath.Join(t.TempDir(), "helper.log")
			args := []string{helper, "-test.run=^TestT422NativeQueryCommandChild$", "-test.count=1", "-test.timeout=10s"}
			err := t422NativeQueryCommand(ctx, "", logPath, args, []string{"PHEBS_T422_QUERY_COMMAND_CHILD=" + mode, "GORACE=atexit_sleep_ms=0"}, nil, nil)
			if (err == nil) != (mode == "small") {
				t.Fatal("command disposition", err)
			}
			info, statErr := os.Stat(logPath)
			if statErr != nil || info.Mode().Perm() != 0o600 || info.Size() > 1<<20 {
				t.Fatal("bounded private diagnostic missing", statErr)
			}
		})
	}
}

func TestT422NativeQueryResult(t *testing.T) {
	oracle, err := t421fixture.BuildIndependentOracle()
	if err != nil {
		t.Fatal(err)
	}
	// Modeled rows exercise only the closed parent-result boundary. The
	// optional real-server fixture must obtain these facts from actual requests.
	for _, mode := range []string{"valid", "schema", "missing", "reordered", "projection", "ordinal", "pages", "counts", "repositories_absent", "repositories_zero", "repositories_two", "repositories_foreign", "trailing", "unknown", "duplicate", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			rows := make([]t421fixture.ExecutionProductQuery, 0, 22)
			next := uint64(2)
			for _, transport := range []string{"http", "mcp"} {
				for _, query := range oracle.QueryCases {
					code := strconv.Itoa(int(query.ExpectedStatus))
					if transport == "mcp" {
						code = query.ExpectedMCPCode
					}
					pages := t421BlackBoxQueryPages(query)
					rows = append(rows, t421fixture.ExecutionProductQuery{Name: query.Name, Transport: transport, Code: code,
						ProjectionSHA256: query.ProjectionSHA256, Records: query.ExpectedRecords, Paths: query.ExpectedPaths,
						Pages: pages, FirstOrdinal: next, LastOrdinal: next + pages - 1})
					if query.Name == "all_code_structural_marker" {
						rows[len(rows)-1].VisibleRepositories, rows[len(rows)-1].VisibleRepositoriesObserved = 1, true
					}
					next += pages
				}
			}
			rows[0].ControlFileReads, rows[0].StoreReadAttempts = 160, 164 // Only aggregate checking is duplicated by this parent.
			schema := t422NativeQuerySchema
			switch mode {
			case "schema":
				schema = "other"
			case "missing":
				rows = rows[:21]
			case "reordered":
				rows[0], rows[1] = rows[1], rows[0]
			case "projection":
				rows[0].ProjectionSHA256 = ""
			case "ordinal":
				rows[1].FirstOrdinal++
			case "pages":
				rows[0].Pages++
			case "counts":
				rows[0].StoreReadAttempts++
			case "repositories_absent":
				rows[0].VisibleRepositoriesObserved = false
			case "repositories_zero":
				rows[0].VisibleRepositories = 0
			case "repositories_two":
				rows[11].VisibleRepositories = 2
			case "repositories_foreign":
				rows[1].VisibleRepositories, rows[1].VisibleRepositoriesObserved = 1, true
			}
			raw, err := json.Marshal(t422NativeQueryResult{Schema: schema, NextOrdinal: next, Rows: rows})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "trailing":
				raw = append(raw, []byte("{}")...)
			case "unknown":
				raw = append([]byte(`{"unexpected":1,`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"schema":"`+t422NativeQuerySchema+`",`), raw[1:]...)
			case "oversize":
				raw = bytes.Repeat([]byte{' '}, (64<<10)+1)
			}
			got, actual, err := t422DecodeNativeQueryResult(raw, 2, oracle.QueryCases)
			if (err == nil) != (mode == "valid") || err == nil && (got != 40 || len(actual) != 22) {
				t.Fatal("result admission", err)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func t422SemanticTestRequest(t *testing.T) ([]byte, dispatchadmission.ProductionSemanticSnapshot) {
	t.Helper()
	request := t422SemanticLaunchRequest{
		Schema: t422SemanticLaunchSchema, Recipe: t422SemanticLaunchRecipe,
		PlanSHA256: "sha256:" + strings.Repeat("1", 64), ConfigSHA256: "sha256:" + strings.Repeat("2", 64),
		ServerEpoch: 1, Repository: "local/tmp/t422-source",
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	return raw, dispatchadmission.ProductionSemanticSnapshot{
		Mode: dispatchadmission.ProductionSemanticV3, InputSHA256: sha256.Sum256(raw), ProducerID: 7, Phase: 2,
	}
}

func TestT422SemanticLaunchClosedEnvelope(t *testing.T) {
	raw, snapshot := t422SemanticTestRequest(t)
	if launch, err := decodeT422SemanticLaunch(raw, snapshot); err != nil || launch.request.ServerEpoch != 1 {
		t.Fatal("canonical launch refused", err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"unknown", bytes.Replace(raw, []byte("{\"schema\":"), []byte("{\"path\":\"/tmp/other\",\"schema\":"), 1)},
		{"prior-not-implemented", bytes.Replace(raw, []byte("{\"schema\":"), []byte("{\"prior\":{},\"schema\":"), 1)},
		{"restart-not-implemented", bytes.Replace(raw, []byte("{\"schema\":"), []byte("{\"restart\":{},\"schema\":"), 1)},
		{"schema", bytes.Replace(raw, []byte(t422SemanticLaunchSchema), []byte("unselected"), 1)},
		{"recipe", bytes.Replace(raw, []byte(t422SemanticLaunchRecipe), []byte("arbitrary"), 1)},
		{"no-newline", bytes.TrimSuffix(raw, []byte("\n"))},
		{"space", append([]byte(" "), raw...)},
		{"second-object", append(append([]byte(nil), raw...), []byte("{}")...)},
		{"duplicate", bytes.Replace(raw, []byte("{\"schema\":"), []byte("{\"server_epoch\":1,\"schema\":"), 1)},
		{"oversize", bytes.Repeat([]byte(" "), t422SemanticLaunchBytes+1)},
		{"bad-epoch", bytes.Replace(raw, []byte("\"server_epoch\":1"), []byte("\"server_epoch\":6"), 1)},
		{"bad-digest", bytes.Replace(raw, []byte(strings.Repeat("1", 64)), []byte(strings.Repeat("Z", 64)), 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := snapshot
			binding.InputSHA256 = sha256.Sum256(test.raw)
			if _, err := decodeT422SemanticLaunch(test.raw, binding); !errors.Is(err, errT422SemanticLaunch) {
				t.Fatal("unclosed envelope accepted", err)
			}
		})
	}
	for _, change := range []func(*dispatchadmission.ProductionSemanticSnapshot){
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.Mode = "" },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.InputSHA256[0] ^= 1 },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.ProducerID = 0 },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.Phase = 3 },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.RequestSequence = 1 },
	} {
		value := snapshot
		change(&value)
		if _, err := decodeT422SemanticLaunch(raw, value); err == nil {
			t.Fatal("wrong authenticated binding accepted")
		}
	}
}

func TestT422SemanticEpochAndWindowIdentity(t *testing.T) {
	for epoch := uint64(0); epoch <= 6; epoch++ {
		for phase := uint32(0); phase <= 16; phase++ {
			first := map[uint64]uint32{1: 2, 2: 5, 3: 6, 4: 8, 5: 12}[epoch]
			last := map[uint64]uint32{1: 4, 2: 5, 3: 8, 4: 11, 5: 14}[epoch]
			if t422SemanticEpochPhase(epoch, phase, true) != (first != 0 && phase == first) ||
				t422SemanticEpochPhase(epoch, phase, false) != (first != 0 && phase >= first && phase <= last) {
				t.Fatalf("epoch %d phase %d changed fixed membership", epoch, phase)
			}
		}
	}
	raw, snapshot := t422SemanticTestRequest(t)
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil || !launch.sameRequest(snapshot, snapshot) {
		t.Fatal("same-window snapshot refused", err)
	}
	for _, change := range []func(*dispatchadmission.ProductionSemanticSnapshot){
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.Mode = "" },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.InputSHA256[0] ^= 1 },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.ProducerID++ },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.Phase++ },
		func(value *dispatchadmission.ProductionSemanticSnapshot) { value.RequestSequence++ },
	} {
		current := snapshot
		change(&current)
		if launch.sameRequest(snapshot, current) {
			t.Fatal("old reservation followed a changed producer, phase or request window")
		}
	}
	if launch.requestCurrent(t.Context()) {
		t.Fatal("caller context invented a real semantic request reservation")
	}
}

func TestT422SemanticLaunchSocketBoundsAndCancellation(t *testing.T) {
	raw, snapshot := t422SemanticTestRequest(t)
	for _, cancelRead := range []bool{false, true} {
		reader, writer, err := dispatchadmission.NewPipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		timeout := 2 * time.Second
		if cancelRead {
			timeout = 50 * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(t.Context(), timeout)
		if !cancelRead {
			if _, err := writer.Write(raw); err != nil {
				t.Fatal(err)
			}
			_ = writer.Close()
		}
		launch, err := readT422SemanticSocket(ctx, reader, snapshot)
		cancel()
		if (err != nil) != cancelRead || (launch == nil) != cancelRead {
			t.Fatal("bounded socket result differs", cancelRead, err)
		}
		if _, err := reader.Stat(); err != nil {
			t.Fatal("borrowed stdin closed", err)
		}
	}
	// Optional absence never reads or validates process stdio/context.
	if value, err := readT422SemanticLaunch(t.Context(), nil); value != nil || err != nil {
		t.Fatal("ordinary launch gained semantic input work", err)
	}
}

func TestT422SemanticInheritedSocket(t *testing.T) {
	for _, mode := range []string{"complete", "no-eof"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			raw, _ := t422SemanticTestRequest(t)
			reader, writer, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close(); _ = writer.Close() }()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422SemanticInheritedSocketHelper$")
			command.Env = []string{"PHEBS_TEST_SEMANTIC_STDIN=" + mode, "GORACE=atexit_sleep_ms=0"}
			command.Stdin = reader
			command.WaitDelay = time.Second
			if _, err := writer.Write(raw); err != nil {
				t.Fatal(err)
			}
			if mode == "complete" {
				_ = writer.Close()
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("inherited stdin: %s: %v", output, err)
			}
		})
	}
}

func TestT422SemanticInheritedSocketHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_TEST_SEMANTIC_STDIN")
	if mode == "" {
		return
	}
	_, snapshot := t422SemanticTestRequest(t)
	timeout := 2 * time.Second
	if mode == "no-eof" {
		timeout = 100 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	_, err := readT422SemanticSocket(ctx, os.Stdin, snapshot)
	if (err != nil) != (mode == "no-eof") {
		t.Fatal("actual inherited stdin socket outcome differs", err)
	}
	if _, err := os.Stdin.Stat(); err != nil {
		t.Fatal("borrowed inherited stdin was closed", err)
	}
}

func TestT422SemanticConfigAndServeBinding(t *testing.T) {
	t.Setenv("PHEBS_LITERAL_TEST", "ambient-value")
	raw, snapshot := t422SemanticTestRequest(t)
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	configRaw := []byte("server: {addr: '127.0.0.1:3070', data_dir: /tmp/t422-data}\nauth: {api_key: exact-test}\nsync: {resync_interval: '0'}\nconnections:\n  - {name: neutral, type: git, url: 'file:///tmp/t422-source.git', watch: true}\nexperimental: {provisional_proto_extraction: true}\nservice_catalogs:\n  local/tmp/t422-source: {kind: operator, id: platform, version: v1, path: /tmp/catalog.json, runtime: v3}\n")
	digest := sha256.Sum256(configRaw)
	launch.request.ConfigSHA256 = "sha256:" + hex.EncodeToString(digest[:])
	cfg, boundRaw, err := launch.parseConfig(configRaw)
	if err != nil || cfg == nil || !bytes.Equal(configRaw, boundRaw) {
		t.Fatal("exact config refused", err)
	}
	for _, changed := range [][]byte{
		append(append([]byte(nil), configRaw...), '\n'), nil,
		bytes.Repeat([]byte(" "), t422SemanticConfigBytes+1),
	} {
		if _, _, err := launch.parseConfig(changed); err == nil {
			t.Fatal("changed config accepted")
		}
	}
	for _, changed := range [][]byte{
		[]byte("{}"),
		bytes.Replace(configRaw, []byte("runtime: v3"), []byte("runtime: v2"), 1),
		bytes.Replace(configRaw, []byte("local/tmp/t422-source:"), []byte("local/tmp/other:"), 1),
		bytes.Replace(configRaw, []byte("exact-test"), []byte("$AMBIENT"), 1),
		bytes.Replace(configRaw, []byte("exact-test"), []byte(`"\u0024{PHEBS_LITERAL_TEST}"`), 1),
		bytes.Replace(configRaw, []byte("exact-test"), []byte(`"\x24{PHEBS_LITERAL_TEST}"`), 1),
		bytes.Replace(configRaw, []byte(", data_dir: /tmp/t422-data"), nil, 1),
		bytes.Replace(configRaw, []byte("/tmp/t422-data"), []byte("'~/t422-data'"), 1),
		bytes.Replace(configRaw, []byte("/tmp/t422-data"), []byte("relative/t422-data"), 1),
		bytes.Replace(configRaw, []byte("/tmp/t422-data"), []byte("/tmp/../t422-data"), 1),
		bytes.Replace(configRaw, []byte("file:///tmp/t422-source.git"), []byte("file:///tmp/other.git"), 1),
		bytes.Replace(configRaw, []byte("file:///tmp/t422-source.git"), []byte("https://example.test/t422-source.git"), 1),
		bytes.Replace(configRaw, []byte("watch: true"), []byte("watch: false"), 1),
		bytes.Replace(configRaw, []byte("127.0.0.1:3070"), []byte("0.0.0.0:3070"), 1),
		bytes.Replace(configRaw, []byte("experimental:"), []byte("  - {name: second, type: git, url: 'file:///tmp/other.git'}\nexperimental:"), 1),
	} {
		selected := *launch
		digest := sha256.Sum256(changed)
		selected.request.ConfigSHA256 = "sha256:" + hex.EncodeToString(digest[:])
		if _, _, err := selected.parseConfig(changed); err == nil {
			t.Fatal("unsupported config semantics accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, configRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, raw, err := launch.loadConfig(path); err != nil || !reflect.DeepEqual(got, cfg) || !bytes.Equal(raw, configRaw) {
		t.Fatal("bound single-parse config differs", err)
	}
	if _, _, err := launch.loadConfig(filepath.Dir(path)); err == nil {
		t.Fatal("directory config accepted")
	}
	for _, test := range []struct {
		addr           string
		extra          []string
		reads, reports bool
	}{
		{addr: "127.0.0.1:3070", reads: true, reports: true},
		{extra: []string{"unexpected"}, reads: true, reports: true},
		{reads: false, reports: true}, {reads: true, reports: false},
	} {
		if launch.validateServeSelection(test.addr, test.extra, test.reads, test.reports) == nil {
			t.Fatal("unbound serve override accepted")
		}
	}
	if err := launch.validateServeSelection("", nil, true, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHEBS_T335_SERVICE_CATALOG", "/tmp/unbound")
	if launch.validateServeSelection("", nil, true, true) == nil {
		t.Fatal("demo override escaped config binding")
	}
	if err := (*t422SemanticLaunch)(nil).validateServeSelection("override", []string{"ordinary"}, false, false); err != nil {
		t.Fatal("ordinary serve gained semantic restriction", err)
	}
}

func TestT422DuplicateRequestHeaderRefusesBeforeAuthAndSlot(t *testing.T) {
	owners, err := dispatchadmission.NewOwners(t.Context(), dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ })
	if reflect.ValueOf(t422OwnerHTTPHandler(nil, next)).Pointer() != reflect.ValueOf(next).Pointer() {
		t.Fatal("ordinary handler acquired wrapper")
	}
	handler := t422OwnerHTTPHandler(owners, next)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Add(dispatchadmission.ProductionRequestHeader, "first")
	request.Header.Add(dispatchadmission.ProductionRequestHeader, "second")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || calls != 0 {
		t.Fatal("ambiguous request reached authentication")
	}
	turn, err := owners.EnterRequest(t.Context())
	if err != nil {
		t.Fatal("refused header retained request slot", err)
	}
	turn.End()
}

func TestT422SharedReadStateBindsReadersWithoutReset(t *testing.T) {
	reader := t421ExactFinalAuthorityRead{Read: func(context.Context) ([]byte, func() error, error) {
		return []byte("{}"), nil, nil
	}}
	capture := &t421ExactReadTestCapture{}
	state := t421NewExactReadAccountingState(capture.report, capture.fail)
	if err := state.bindFinalReaders(reader, reader); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	authService := newT421ExactReadAuthService(t)
	apiHandler, mcpHandler := state.wrap(next), state.wrap(next)
	if apiHandler.(*t421ExactReadAccountingHandler).state != mcpHandler.(*t421ExactReadAccountingHandler).state {
		t.Fatal("transports acquired separate ordinal state")
	}
	for index, handler := range []http.Handler{apiHandler, mcpHandler} {
		response := serveT421ExactReadRequest(t, authService.Require(handler), exactT421ReadRequest(
			http.MethodGet, "/api/extraction-progress?repository=private", uint64(index+1),
		))
		if response.status != http.StatusNoContent {
			t.Fatal("shared ordinal refused", response.status)
		}
	}
	if err := state.bindFinalReaders(reader, reader); err == nil || state.nextOrdinal != 3 {
		t.Fatal("binding reset or replaced a consumed ordinal")
	}
	late := t421NewExactReadAccountingState(capture.report, capture.fail)
	_ = late.wrap(next)
	if err := late.bindFinalReaders(reader, reader); err == nil {
		t.Fatal("readers mutated after HTTP handler exposure")
	}
	used := t421NewExactReadAccountingState(capture.report, capture.fail)
	used.nextOrdinal = 2
	if err := used.bindFinalReaders(reader, reader); err == nil || used.nextOrdinal != 2 {
		t.Fatal("early native ordinal was discarded during binding")
	}
}

func TestT422RequestOwnerSpansAuthenticationTail(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	// This exercises actual local owner reservation and complete outer-handler
	// lifetime. Dispatch package tests independently prove authenticated token
	// validation and slot reservation share one lock boundary.
	handler := t422OwnerHTTPHandler(owners, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, "local-owner-test")
	go func() { handler.ServeHTTP(httptest.NewRecorder(), request); close(returned) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("request did not enter its owner")
	}
	fenced := make(chan error, 1)
	go func() { fenced <- owners.FenceRequests(ctx) }()
	select {
	case err := <-fenced:
		t.Fatal("request fence skipped unfinished authentication/report tail", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case <-returned:
	case <-ctx.Done():
		t.Fatal("request tail did not return")
	}
	if err := <-fenced; err != nil {
		t.Fatal(err)
	}
}

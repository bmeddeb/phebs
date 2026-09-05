package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	t422SemanticLaunchSchema = "t422-semantic-launch-v3"
	t422SemanticLaunchRecipe = "t422-fixed-phase-control-v3"
	t422SemanticLaunchBytes  = 16 << 10
	t422SemanticConfigBytes  = 64 << 10
)

var errT422SemanticLaunch = errors.New("T42.2 semantic launch admission refused")

// This private envelope authenticates the parent's selected plan digest; it
// does not claim that main decoded or admitted that plan. The genuine parent
// retains protected plan/config custody. No path, arbitrary phase list, prior
// authority or restart claim is accepted before its native operation exists.
type t422SemanticLaunchRequest struct {
	Schema       string `json:"schema"`
	Recipe       string `json:"recipe"`
	PlanSHA256   string `json:"plan_sha256"`
	ConfigSHA256 string `json:"config_sha256"`
	ServerEpoch  uint64 `json:"server_epoch"`
	Repository   string `json:"repository"`
}

type t422SemanticLaunch struct {
	request t422SemanticLaunchRequest
	initial dispatchadmission.ProductionSemanticSnapshot
	fail    func(error)
}

// Phase IDs are one-based positions in the fixed V3 phase order. Epoch three
// also owns phase eight's prepared hit before hard death; only epoch four can
// launch there and eventually own its recovered observation.
func t422SemanticEpochPhase(epoch uint64, phase uint32, initial bool) bool {
	var first, last uint32
	switch epoch {
	case 1:
		first, last = 2, 4
	case 2:
		first, last = 5, 5
	case 3:
		first, last = 6, 8
	case 4:
		first, last = 8, 11
	case 5:
		first, last = 12, 14
	default:
		return false
	}
	return phase == first || !initial && phase > first && phase <= last
}

func t422SemanticDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && "sha256:"+hex.EncodeToString(raw) == value
}

func decodeT422SemanticLaunch(raw []byte, snapshot dispatchadmission.ProductionSemanticSnapshot) (*t422SemanticLaunch, error) {
	if len(raw) == 0 || len(raw) > t422SemanticLaunchBytes || snapshot.Mode != dispatchadmission.ProductionSemanticV3 ||
		snapshot.InputSHA256 == ([32]byte{}) || snapshot.ProducerID == 0 || snapshot.RequestSequence != 0 || sha256.Sum256(raw) != snapshot.InputSHA256 {
		return nil, errT422SemanticLaunch
	}
	var request t422SemanticLaunchRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
		request.Schema != t422SemanticLaunchSchema || request.Recipe != t422SemanticLaunchRecipe ||
		!t422SemanticDigest(request.PlanSHA256) || !t422SemanticDigest(request.ConfigSHA256) ||
		!t422SemanticEpochPhase(request.ServerEpoch, snapshot.Phase, true) ||
		request.Repository == "" || len(request.Repository) > 256 || strings.ContainsAny(request.Repository, "\x00\r\n") {
		return nil, errT422SemanticLaunch
	}
	canonical, err := json.Marshal(request)
	if err != nil || !bytes.Equal(raw, append(canonical, '\n')) {
		return nil, errT422SemanticLaunch
	}
	return &t422SemanticLaunch{request: request, initial: snapshot}, nil
}

// The selected parent supplies stdin from the existing private Unix socket
// pair API. FileConn owns one pollable duplicate, unlike inherited blocking
// os.Stdin. Only that duplicate is closed; borrowed process stdio stays open.
func readT422SemanticLaunch(ctx context.Context, input *os.File) (*t422SemanticLaunch, error) {
	if !dispatchadmission.ProductionSemanticSelected() {
		return nil, nil
	}
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		return nil, errT422SemanticLaunch
	}
	return readT422SemanticSocket(ctx, input, snapshot)
}

func readT422SemanticSocket(ctx context.Context, input *os.File, snapshot dispatchadmission.ProductionSemanticSnapshot) (_ *t422SemanticLaunch, retErr error) {
	if ctx == nil || ctx.Err() != nil || input == nil {
		return nil, errT422SemanticLaunch
	}
	info, err := input.Stat()
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, errT422SemanticLaunch
	}
	connection, err := net.FileConn(input)
	if err != nil {
		return nil, errT422SemanticLaunch
	}
	defer func() {
		if connection.Close() != nil {
			retErr = errT422SemanticLaunch
		}
	}()
	if _, ok := connection.(*net.UnixConn); !ok {
		return nil, errT422SemanticLaunch
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := readCtx.Deadline()
	if connection.SetReadDeadline(deadline) != nil {
		return nil, errT422SemanticLaunch
	}
	unblocked := make(chan struct{})
	stop := context.AfterFunc(readCtx, func() {
		_ = connection.SetReadDeadline(time.Now())
		close(unblocked)
	})
	raw, readErr := io.ReadAll(io.LimitReader(connection, t422SemanticLaunchBytes+1))
	if !stop() {
		<-unblocked
	}
	if readErr != nil || readCtx.Err() != nil {
		return nil, errT422SemanticLaunch
	}
	return decodeT422SemanticLaunch(raw, snapshot)
}

func (launch *t422SemanticLaunch) loadConfig(path string) (*config.Config, []byte, error) {
	if launch == nil {
		return loadServerConfig(path)
	}
	if len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, errT422SemanticLaunch
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > t422SemanticConfigBytes {
		return nil, nil, errT422SemanticLaunch
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errT422SemanticLaunch
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() <= 0 || info.Size() > t422SemanticConfigBytes {
		_ = file.Close()
		return nil, nil, errT422SemanticLaunch
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, t422SemanticConfigBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, nil, errT422SemanticLaunch
	}
	return launch.parseConfig(raw)
}

func (launch *t422SemanticLaunch) parseConfig(raw []byte) (*config.Config, []byte, error) {
	digest := sha256.Sum256(raw)
	if len(raw) == 0 || len(raw) > t422SemanticConfigBytes ||
		"sha256:"+hex.EncodeToString(digest[:]) != launch.request.ConfigSHA256 || bytes.Contains(raw, []byte("$")) {
		return nil, nil, errT422SemanticLaunch
	}
	// Parse these very bytes once; do not re-open a mutable path after its
	// digest check, or let ambient secret expansion replace admitted bytes.
	cfg, err := config.Parse(raw)
	if err != nil {
		return nil, nil, errT422SemanticLaunch
	}
	repository, err := t421FinalAuthorityRepository(cfg.ServiceCatalogs)
	if err != nil || repository != launch.request.Repository || !t422SemanticConfigShape(cfg, repository) {
		return nil, nil, errT422SemanticLaunch
	}
	return cfg, raw, nil
}

func t422SemanticConfigShape(cfg *config.Config, repository string) bool {
	if len(cfg.Connections) != 1 || len(cfg.AnalysisUnits) != 0 || len(cfg.Revisions) != 0 || len(cfg.Contexts) != 0 ||
		cfg.Sync.ResyncInterval != "0" || cfg.Sync.CleanupOrphans ||
		cfg.Auth.APIKey == "" || !cfg.Auth.OIDC.IsZero() || !cfg.Auth.BootstrapUser.IsZero() || len(cfg.Auth.TrustedProxies) != 0 ||
		!filepath.IsAbs(cfg.Server.DataDir) || filepath.Clean(cfg.Server.DataDir) != cfg.Server.DataDir ||
		cfg.ServiceCatalogs[repository].Kind != "operator" {
		return false
	}
	host, port, err := net.SplitHostPort(cfg.Server.Addr)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || host != "127.0.0.1" || number < 1 || number > 65535 || strconv.Itoa(number) != port {
		return false
	}
	connection := cfg.Connections[0]
	if connection.Type != "git" || !connection.Watch || connection.Token != "" || !connection.HTTPAuth.IsZero() ||
		connection.App != (config.GitHubApp{}) || len(connection.Orgs)+len(connection.Groups)+len(connection.Users)+len(connection.Repos) != 0 ||
		connection.Exclude.Archived || connection.Exclude.Forks || len(connection.Exclude.Repos) != 0 {
		return false
	}
	source, err := url.Parse(connection.URL)
	if err != nil || source.Scheme != "file" || source.Host != "" || source.User != nil || source.RawQuery != "" || source.ForceQuery || source.Fragment != "" ||
		!filepath.IsAbs(source.Path) || filepath.Clean(source.Path) != source.Path || source.String() != connection.URL {
		return false
	}
	actual, err := phebssync.RepoName(connection.URL)
	return err == nil && actual == repository
}

func (launch *t422SemanticLaunch) validateServeSelection(addr string, extra []string, exactReads, exactReports bool) error {
	if launch == nil {
		return nil
	}
	if addr != "" || len(extra) != 0 || !exactReads || !exactReports {
		return errT422SemanticLaunch
	}
	for _, name := range []string{
		"PHEBS_T307_NEUTRAL_SERVICE_REPO", "PHEBS_T335_SERVICE_CATALOG",
		"PHEBS_T344_SERVICE_SEARCH_REPO", "PHEBS_T344_SERVICE_SEARCH_CATALOG",
		"PHEBS_WORKBENCH_CLOSURE_REPO", "PHEBS_THRIFT_FIELD_DEMO_REPO",
		"PHEBS_INVESTIGATION_FIXTURES", "PHEBS_CONTRACT_ATLAS_FIXTURE", "PHEBS_SYNTHETIC_WORKBENCH",
	} {
		if value := os.Getenv(name); value != "" {
			return errT422SemanticLaunch
		}
	}
	return nil
}

type t422SemanticRequestKey struct{}

func (launch *t422SemanticLaunch) matches(snapshot dispatchadmission.ProductionSemanticSnapshot) bool {
	return launch != nil && snapshot.Mode == launch.initial.Mode && snapshot.InputSHA256 == launch.initial.InputSHA256 &&
		snapshot.ProducerID == launch.initial.ProducerID && t422SemanticEpochPhase(launch.request.ServerEpoch, snapshot.Phase, false)
}

func (launch *t422SemanticLaunch) admitRequest(request *http.Request) (*http.Request, error) {
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil || !launch.matches(snapshot) {
		// A valid private request already owns its slot. A changed semantic
		// producer/phase is terminal, unlike an unauthenticated token refusal.
		if launch.fail != nil {
			launch.fail(errT422SemanticLaunch)
		}
		return nil, errT422SemanticLaunch
	}
	return request.WithContext(context.WithValue(request.Context(), t422SemanticRequestKey{}, snapshot)), nil
}

func (launch *t422SemanticLaunch) requestCurrent(ctx context.Context) bool {
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	current, err := dispatchadmission.ProductionSemanticState()
	return ok && err == nil && launch.sameRequest(admitted, current)
}

func (launch *t422SemanticLaunch) sameRequest(admitted, current dispatchadmission.ProductionSemanticSnapshot) bool {
	return launch.matches(current) && admitted == current
}

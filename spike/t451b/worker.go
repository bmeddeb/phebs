package t451b

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

type ToolIdentity struct {
	Path   string           `json:"path"`
	Bytes  int              `json:"bytes"`
	SHA256 string           `json:"sha256"`
	Build  *debug.BuildInfo `json:"build"`
}
type LegEvidence struct {
	Slot            string       `json:"slot"`
	ClientArgv      []string     `json:"client_argv"`
	Environment     []string     `json:"environment"`
	WallNanoseconds int64        `json:"wall_nanoseconds"`
	Stdout          []byte       `json:"stdout"`
	Stderr          []byte       `json:"stderr"`
	Call            CallEvidence `json:"call"`
}
type Evidence struct {
	Version               string              `json:"version"`
	Request               Request             `json:"request"`
	Plan                  planner.Plan        `json:"plan"`
	CompilerCacheEviction t451a.CacheEviction `json:"compiler_cache_eviction"`
	Tools                 []ToolIdentity      `json:"tools"`
	Legs                  []LegEvidence       `json:"legs"`
	SCIP                  []byte              `json:"scip"`
	SCIPSHA256            string              `json:"scip_sha256"`
	Oracle                SCIPFacts           `json:"oracle"`
}

func neutralRoots(plan planner.Plan) ([]planner.Configured, error) {
	if err := planner.VerifyNeutral(plan); err != nil {
		return nil, err
	}
	var roots []planner.Configured
	for _, t := range plan.Targets {
		if t.Label == "@@//lib:alias" {
			roots = append(roots, t.Configured)
		}
	}
	if len(roots) != 1 {
		return nil, errors.New("exact alias root missing")
	}
	return roots, nil
}

func scipArguments(patterns []string) []string {
	return append([]string{"index", "--module-root=" + launcher.Workspace, "--module-path=" + ModulePath, "--module-version=" + ModuleVersion, "--repository-remote=https://example.test/phebs-neutral", "--go-version=go1.25.0", "--skip-tests", "--skip-implementations", "--output=/scratch/t451b-index.scip"}, patterns...)
}

type toolProfile struct {
	path, sha, main string
	tools           bool
}

func toolProfiles(r Request) []toolProfile {
	return []toolProfile{
		{"/inputs/t451a", r.HelperSHA256, "github.com/bmeddeb/phebs/spike/t451b/cmd/t451b", false},
		{AdapterPath, r.HelperSHA256, "github.com/bmeddeb/phebs/spike/t451b/cmd/t451b", false},
		{ProbePath, r.ProbeSHA256, "phebs.local/t451b-load-probe", true},
		{SCIPPath, r.SCIPGoSHA256, "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"/inputs/tools/go/bin/go", r.GoSHA256, "cmd/go", false},
	}
}
func validateTools(r Request, tools []ToolIdentity) error {
	return validateToolProfiles(toolProfiles(r), tools)
}
func validateToolProfiles(expected []toolProfile, tools []ToolIdentity) error {
	if len(tools) != len(expected) {
		return errors.New("executed tool inventory count mismatch")
	}
	for i, t := range expected {
		got := tools[i]
		info := got.Build
		if got.Path != t.path || got.SHA256 != t.sha || !digest(got.SHA256) || got.Bytes <= 0 || got.Bytes > t451a.MaxFileBytes || info == nil || info.Path != t.main {
			return errors.New("executed tool identity mismatch")
		}
		settings := map[string]string{}
		for _, s := range info.Settings {
			if _, ok := settings[s.Key]; ok {
				return errors.New("duplicate executed tool build setting")
			}
			settings[s.Key] = s.Value
		}
		if settings["GOOS"] != "linux" || !typedindex.AdmittedNativeArch(settings["GOARCH"]) {
			return errors.New("executed tool platform mismatch")
		}
		if t.tools {
			if info.GoVersion != "go1.25.0" || settings["CGO_ENABLED"] != "0" || !typedindex.AdmittedNativeVariant(settings["GOARCH"], settings["GOARM64"], settings["GOAMD64"]) {
				return errors.New("typed tool build profile mismatch")
			}
			count := 0
			for _, d := range info.Deps {
				if d == nil {
					return errors.New("nil executed tool dependency")
				}
				if d.Path == "golang.org/x/tools" {
					if d.Version != ToolsVersion || d.Sum != ToolsSum || d.Replace != nil {
						return errors.New("typed tool x/tools graph mismatch")
					}
					count++
				}
			}
			if count != 1 {
				return errors.New("typed tool x/tools graph absent or repeated")
			}
		}
		if t.main == "cmd/go" && info.GoVersion != "go1.25.0" {
			return errors.New("SDK executable release mismatch")
		}
	}
	return nil
}
func toolIdentities(r Request) ([]ToolIdentity, error) {
	return readToolProfiles(toolProfiles(r))
}
func readToolProfiles(profiles []toolProfile) ([]ToolIdentity, error) {
	var result []ToolIdentity
	for _, t := range profiles {
		b, err := readBounded(t.path, t451a.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		info, err := buildinfo.ReadFile(t.path)
		if err != nil {
			return nil, err
		}
		result = append(result, ToolIdentity{t.path, len(b), t451a.Digest(b), info})
	}
	if err := validateToolProfiles(profiles, result); err != nil {
		return nil, err
	}
	return result, nil
}

func Worker(ctx context.Context, r Request) error {
	tools, err := toolIdentities(r)
	if err != nil {
		return err
	}
	plan, evicted, err := t451a.BuildNeutralPlan(ctx)
	if err != nil {
		return err
	}
	roots, err := neutralRoots(plan)
	if err != nil {
		return err
	}
	evidence := Evidence{Version: "phebs-t451b-neutral-v1", Request: r, Plan: plan, CompilerCacheEviction: evicted, Tools: tools}
	for _, slot := range []string{"load", "scip"} {
		p, err := launcher.PrepareCompatibility(plan, roots, slot)
		if err != nil {
			return err
		}
		data, err := json.Marshal(control{"phebs-t451b-client-plan-v1", slot, plan, roots})
		if err != nil {
			return err
		}
		data = append(data, '\n')
		planPath, tracePath, err := slotPaths(slot)
		if err != nil {
			return err
		}
		if len(data) > planner.MaxProtoBytes {
			return errors.New("compatibility plan byte limit")
		}
		env := callerEnvironment(p, slot, t451a.Digest(data))
		if len(callerRequest(env)) > maxRequestBytes {
			return errors.New("client environment exceeds request ceiling")
		}
		if err = closedFile(planPath, data); err != nil {
			return err
		}
		executable, args := ProbePath, p.Invocation().Arguments
		if slot == "scip" {
			executable, args = SCIPPath, scipArguments(args)
		}
		started := time.Now()
		stdout, stderr, err := runClient(ctx, executable, args, env)
		if err != nil {
			return fmt.Errorf("neutral %s client refused: %w: %.4096s", slot, err, stderr)
		}
		raw, err := readBounded(tracePath, maxTraceBytes)
		if err != nil {
			return err
		}
		call, err := decode[CallEvidence](raw, maxTraceBytes, false)
		if err != nil {
			return err
		}
		if err = verifyCall(plan, roots, slot, call); err != nil {
			return err
		}
		if err = launcher.VerifyCompatibilityFiles(ctx, plan, roots, slot, call.Result.Exports); err != nil {
			return err
		}
		if slot == "load" {
			if err = verifyProbe(stdout, call.Result.Response); err != nil {
				return err
			}
		}
		evidence.Legs = append(evidence.Legs, LegEvidence{slot, append([]string{executable}, args...), env, time.Since(started).Nanoseconds(), stdout, stderr, call})
	}
	evidence.SCIP, err = readBounded("/scratch/t451b-index.scip", maxClientBytes)
	if err != nil {
		return err
	}
	evidence.SCIPSHA256 = t451a.Digest(evidence.SCIP)
	evidence.Oracle, err = VerifySCIP(evidence.SCIP)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	if len(encoded)+1 > sandbox.OutputBytes {
		return errors.New("neutral evidence aggregate output limit")
	}
	_, err = os.Stdout.Write(append(encoded, '\n'))
	return err
}

type clientBudget struct {
	mu        sync.Mutex
	remaining int
	cancel    context.CancelFunc
}
type clientOutput struct {
	budget *clientBudget
	data   bytes.Buffer
}

func (w *clientOutput) Write(data []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	if len(data) > w.budget.remaining {
		w.budget.cancel()
		return 0, errors.New("client output limit")
	}
	w.budget.remaining -= len(data)
	return w.data.Write(data)
}
func runClient(ctx context.Context, executable string, args, env []string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, launcher.MaxWall)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = launcher.Workspace
	command.Env = env
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = time.Second
	budget := clientBudget{remaining: maxClientBytes, cancel: cancel}
	stdout, stderr := clientOutput{budget: &budget}, clientOutput{budget: &budget}
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return stdout.data.Bytes(), stderr.data.Bytes(), err
}

func verifyCall(plan planner.Plan, roots []planner.Configured, slot string, call CallEvidence) error {
	data, err := json.Marshal(control{"phebs-t451b-client-plan-v1", slot, plan, roots})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, p, err := inspectCall(data, call.PlanSHA256, call.Argv, call.Environment, call.Directory, call.Request)
	if err != nil {
		return err
	}
	return verifyPreparedCall(p, slot, call)
}

func verifyPreparedCall(p launcher.Prepared, slot string, call CallEvidence) error {
	if call.Slot != slot || call.RequestSHA256 != t451a.Digest(call.Request) || call.LauncherSHA256 != p.Digest() || !reflect.DeepEqual(call.Launcher, p.Invocation()) || call.DriverResponseSHA256 != t451a.Digest(call.Result.DriverResponse) || call.ResponseSHA256 != t451a.Digest(call.Result.Response) {
		return errors.New("client call evidence identity mismatch")
	}
	if err := launcher.Reconcile(p, call.Result.DriverResponse); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(call.Result.DriverResponse, &raw); err != nil {
		return err
	}
	raw["Compiler"], raw["Arch"], raw["GoVersion"] = json.RawMessage(`"gc"`), json.RawMessage(strconv.Quote(runtime.GOARCH)), json.RawMessage(`25`)
	expected, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, call.Result.Response) {
		return errors.New("typed response differs from exact metadata adaptation")
	}
	var exports struct{ Packages []struct{ ExportFile string } }
	if err = json.Unmarshal(call.Result.DriverResponse, &exports); err != nil {
		return err
	}
	paths := map[string]bool{}
	for _, p := range exports.Packages {
		if p.ExportFile != "" {
			paths[p.ExportFile] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	if len(ordered) != len(call.Result.Exports) {
		return errors.New("export evidence inventory mismatch")
	}
	total := 0
	for i, export := range call.Result.Exports {
		if export.Path != ordered[i] || export.Bytes <= 0 || export.Bytes > planner.MaxFileBytes || export.Bytes > planner.MaxProtoBytes-total || !digest("sha256:"+export.SHA256) {
			return errors.New("export evidence identity mismatch")
		}
		total += export.Bytes
	}

	return nil
}

type loadFact struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Types    bool   `json:"types"`
	Syntax   int    `json:"syntax"`
	TypeInfo bool   `json:"type_info"`
	Errors   int    `json:"errors"`
	IllTyped bool   `json:"ill_typed"`
}
type loadReport struct {
	Mode     int        `json:"mode"`
	Roots    []loadFact `json:"roots"`
	Packages []loadFact `json:"packages"`
}

func verifyProbe(data, response []byte) error {
	report, err := decode[loadReport](data, maxClientBytes, false)
	if err != nil {
		return err
	}
	var flat struct {
		Roots    []string
		Packages []struct{ ID, PkgPath string }
	}
	if err = json.Unmarshal(response, &flat); err != nil {
		return err
	}
	if report.Mode != launcher.CompatibilityMode || len(report.Roots) != 1 || len(flat.Roots) != 1 || len(report.Packages) != 2 || len(flat.Packages) != 2 {
		return errors.New("typed probe closure mismatch")
	}
	root := report.Roots[0]
	if root.ID != flat.Roots[0] || root.Path != "example.test/neutral/lib" || !root.Types || root.Syntax != 2 || !root.TypeInfo || root.Errors != 0 || root.IllTyped {
		return errors.New("typed root incomplete")
	}
	want := map[string]string{}
	for _, p := range flat.Packages {
		want[p.ID] = p.PkgPath
	}
	previous := ""
	for _, p := range report.Packages {
		path, ok := want[p.ID]
		if !ok || p.ID <= previous || path != p.Path || !p.Types || p.Syntax < 0 || p.Syntax > planner.MaxDocuments || p.Errors != 0 || p.IllTyped {
			return errors.New("typed dependency incomplete")
		}
		previous = p.ID
		if p.ID == root.ID && p != root {
			return errors.New("typed root facts disagree")
		}
	}
	return nil
}

func DecodeEvidence(data []byte) (Evidence, error) {
	e, err := decode[Evidence](data, sandbox.OutputBytes, false)
	if err != nil {
		return e, err
	}
	r, _ := json.MarshalIndent(e.Request, "", "  ")
	if _, err = DecodeRequest(append(r, '\n')); err != nil {
		return e, err
	}
	if err = validateTools(e.Request, e.Tools); err != nil {
		return e, err
	}
	if e.Version != "phebs-t451b-neutral-v1" || len(e.Legs) != 2 || len(e.SCIP) == 0 || e.SCIPSHA256 != t451a.Digest(e.SCIP) {
		return e, errors.New("neutral evidence shape mismatch")
	}
	roots, err := neutralRoots(e.Plan)
	if err != nil {
		return e, err
	}
	for i, slot := range []string{"load", "scip"} {
		leg := e.Legs[i]
		if leg.Slot != slot || leg.WallNanoseconds <= 0 || len(leg.Stdout)+len(leg.Stderr) > maxClientBytes {
			return e, errors.New("neutral leg shape mismatch")
		}
		if err = verifyCall(e.Plan, roots, slot, leg.Call); err != nil {
			return e, err
		}
		executable, args := ProbePath, leg.Call.Launcher.Arguments
		if slot == "scip" {
			executable, args = SCIPPath, scipArguments(args)
		}
		if !slices.Equal(leg.ClientArgv, append([]string{executable}, args...)) || !slices.Equal(leg.Environment, leg.Call.Environment) {
			return e, errors.New("neutral client invocation mismatch")
		}
		if slot == "load" {
			if err = verifyProbe(leg.Stdout, leg.Call.Result.Response); err != nil {
				return e, err
			}
		}
	}
	facts, err := VerifySCIP(e.SCIP)
	if err != nil {
		return e, err
	}
	if facts != e.Oracle {
		return e, errors.New("SCIP oracle evidence mismatch")
	}
	return e, nil
}

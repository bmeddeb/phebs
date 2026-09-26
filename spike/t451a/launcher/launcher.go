// Package launcher implements the closed T45.1a driver boundary. It consumes a
// sealed Bazel plan; a driver can satisfy that plan but cannot extend it.
package launcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

const (
	DriverPath       = "/inputs/tools/bin/gopackagesdriver"
	DriverSHA256     = "4e5526a4d9b15a4a1d790dd31419cabd0ee3ea8653f01a5961f24be526a2017c"
	Workspace        = "/scratch/workspace"
	OutputBase       = "/scratch/bazel-output"
	ExecRoot         = OutputBase + "/execroot/_main"
	MaxResponseBytes = 16 << 20
	MaxWall          = 2 * time.Minute
)

var ErrUnrepresentable = errors.New("sealed plan is not representable by the pinned driver")

type flatPackage struct {
	ID, Name, PkgPath                    string
	Errors                               []json.RawMessage `json:",omitempty"`
	GoFiles, CompiledGoFiles, OtherFiles []string
	ExportFile                           string
	Imports                              map[string]string
}

type response struct {
	NotHandled     bool
	Compiler, Arch string
	Roots          []string
	Packages       []flatPackage
	GoVersion      int
}

type fileIdentity struct {
	digest string
	bytes  int
}

// Prepared has no caller-settable argv, environment, overlay, build flags or
// response authority. Prepare is its only constructor.
type Prepared struct {
	patterns, environment, roots []string
	packages                     map[string]flatPackage
	files                        map[string]fileIdentity
	request                      []byte
	scope                        []byte
	digest                       string
}

// Digest binds the selected configured roots and exact closed invocation.
func (p Prepared) Digest() string { return p.digest }

func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func jsonHash(v any) string   { b, _ := json.Marshal(v); return hash(b) }
func validPath(p string) bool {
	return p != "" && len(p) <= 4096 && path.Clean(p) == p && !strings.HasPrefix(p, "/") && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\\x00\r\n")
}

var labelPattern = regexp.MustCompile(`^@@[A-Za-z0-9._+~-]*//[A-Za-z0-9._+~/-]*:[A-Za-z0-9._+~/-]+$`)

func key(c planner.Configured) string { return c.Label + "#" + c.Configuration }

func checkSeals(plan planner.Plan) error {
	if plan.Version != "phebs-t451a-plan-v1" || len(plan.Targets) > planner.MaxTargets || len(plan.Units) == 0 || len(plan.Units) > planner.MaxUnits || len(plan.Documents) > planner.MaxDocuments || len(plan.SDKs) > 64 {
		return errors.New("invalid sealed plan shape")
	}
	u := slices.Clone(plan.Targets)
	for i := range u {
		u[i].Units = nil
	}
	if plan.UniverseSHA256 != jsonHash(u) || plan.MappingSHA256 != jsonHash(struct {
		Targets []planner.Target
		Units   []planner.Unit
		SDKs    []planner.SDKPlan
	}{plan.Targets, plan.Units, plan.SDKs}) || plan.DocumentsSHA256 != jsonHash(struct {
		Documents []planner.Document
		SDKs      []planner.SDKPlan
	}{plan.Documents, plan.SDKs}) {
		return errors.New("plan digest mismatch")
	}
	return nil
}

// Prepare selects only exact configured targets already in the sealed plan.
// Their package roots determine all request patterns. It neither runs a query
// nor accepts any repository-provided driver configuration.
func Prepare(plan planner.Plan, roots []planner.Configured) (Prepared, error) {
	if err := checkSeals(plan); err != nil {
		return Prepared{}, err
	}
	if len(roots) == 0 || len(roots) > 64 {
		return Prepared{}, errors.New("configured root count")
	}
	targets := map[string]planner.Target{}
	units := map[string]planner.Unit{}
	docs := map[string]planner.Document{}
	sdks := map[string]planner.SDKPlan{}
	for _, t := range plan.Targets {
		if _, ok := targets[key(t.Configured)]; ok {
			return Prepared{}, errors.New("duplicate configured target")
		}
		targets[key(t.Configured)] = t
	}
	for _, u := range plan.Units {
		if _, ok := units[u.ID]; ok {
			return Prepared{}, errors.New("duplicate unit")
		}
		units[u.ID] = u
	}
	for _, d := range plan.Documents {
		if _, ok := docs[d.ID]; ok {
			return Prepared{}, errors.New("duplicate document")
		}
		docs[d.ID] = d
	}
	for _, s := range plan.SDKs {
		if _, ok := sdks[s.ID]; ok {
			return Prepared{}, errors.New("duplicate SDK")
		}
		sdks[s.ID] = s
	}
	p := Prepared{packages: map[string]flatPackage{}, files: map[string]fileIdentity{}}
	rootUnits := map[string]bool{}
	seenTargets := map[string]bool{}
	for _, root := range roots {
		t, ok := targets[key(root)]
		if !ok || seenTargets[key(root)] || t.Tool || len(t.Units) == 0 {
			return Prepared{}, errors.New("missing/duplicate/tool configured root")
		}
		seenTargets[key(root)] = true
		for _, id := range t.Units {
			rootUnits[id] = true
		}
	}
	var pending []string
	for id := range rootUnits {
		pending = append(pending, id)
	}
	selected := map[string]planner.Unit{}
	var selectedMode planner.GoMode
	labelOwners := map[string]string{}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, ok := selected[id]; ok {
			continue
		}
		u, ok := units[id]
		if !ok || u.Tool || !labelPattern.MatchString(u.ArchiveLabel) || !labelPattern.MatchString(u.Owner.Label) {
			return Prepared{}, fmt.Errorf("%w: missing/tool package unit", ErrUnrepresentable)
		}
		if other := labelOwners[u.ArchiveLabel]; other != "" && other != id {
			return Prepared{}, fmt.Errorf("%w: repeated label configuration or archive variant", ErrUnrepresentable)
		}
		if u.Mode.GOOS != "linux" || u.Mode.GOARCH != "arm64" || len(u.Mode.Tags) > 64 {
			return Prepared{}, fmt.Errorf("%w: archive mode", ErrUnrepresentable)
		}
		if len(selected) > 0 && jsonHash(selectedMode) != jsonHash(u.Mode) {
			return Prepared{}, fmt.Errorf("%w: mixed archive modes", ErrUnrepresentable)
		}
		selectedMode = u.Mode
		labelOwners[u.ArchiveLabel] = id
		selected[id] = u
		for _, im := range u.Imports {
			pending = append(pending, im.Unit)
		}
	}
	addFile := func(name, digest string, size int) error {
		if len(digest) != 64 || size < 0 || size > planner.MaxFileBytes {
			return errors.New("invalid sealed file identity")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return err
		}
		f := fileIdentity{digest, size}
		if old, ok := p.files[name]; ok && old != f {
			return errors.New("conflicting sealed file identity")
		}
		p.files[name] = f
		if len(p.files) > planner.MaxDocuments {
			return errors.New("selected document count")
		}
		return nil
	}
	sdkUsed := map[string]bool{}
	sdkPending := map[string][]string{}
	for _, u := range selected {
		_, export, ok := strings.Cut(u.Variant, "|")
		if !ok || !validPath(export) || !strings.HasPrefix(export, "bazel-out/") {
			return Prepared{}, errors.New("missing declared archive locator")
		}
		pkg := flatPackage{ID: u.ArchiveLabel, Name: u.PackageName, PkgPath: u.ImportPath, ExportFile: ExecRoot + "/" + export, GoFiles: []string{}, CompiledGoFiles: []string{}, Imports: map[string]string{}}
		for index, list := range [][]string{u.GoFiles, u.CompiledGoFiles} {
			for _, id := range list {
				d, ok := docs[id]
				if !ok || !validPath(d.ExecPath) {
					return Prepared{}, errors.New("missing declared document locator")
				}
				var name string
				switch d.Kind {
				case "source":
					if d.Repository != "" || d.Path != d.ExecPath {
						return Prepared{}, errors.New("source document location")
					}
					name = Workspace + "/" + d.ExecPath
				case "external":
					if d.ExecPath != "external/"+d.Repository+"/"+d.Path {
						return Prepared{}, errors.New("external document location")
					}
					name = OutputBase + "/" + d.ExecPath
				case "generated":
					if !strings.HasPrefix(d.ExecPath, "bazel-out/") {
						return Prepared{}, errors.New("generated document location")
					}
					name = ExecRoot + "/" + d.ExecPath
				default:
					return Prepared{}, errors.New("unknown document lane")
				}
				if err := addFile(name, d.SHA256, d.Bytes); err != nil {
					return Prepared{}, err
				}
				if index == 0 {
					pkg.GoFiles = append(pkg.GoFiles, name)
				} else {
					pkg.CompiledGoFiles = append(pkg.CompiledGoFiles, name)
				}
			}
		}
		for _, im := range u.Imports {
			dependency, ok := selected[im.Unit]
			if !ok || im.Path == "" || pkg.Imports[im.Path] != "" {
				return Prepared{}, errors.New("invalid package import edge")
			}
			pkg.Imports[im.Path] = dependency.ArchiveLabel
		}
		for _, name := range u.SourceImports {
			if pkg.Imports[name] != "" {
				continue
			}
			sdk, ok := sdks[u.SDK]
			if !ok || sdk.Version != "1.25.0" {
				return Prepared{}, fmt.Errorf("%w: unplanned SDK import", ErrUnrepresentable)
			}
			id := sdk.Prefix + name
			pkg.Imports[name] = id
			sdkPending[u.SDK] = append(sdkPending[u.SDK], id)
		}
		sort.Strings(pkg.GoFiles)
		sort.Strings(pkg.CompiledGoFiles)
		p.packages[pkg.ID] = pkg
	}
	for sdkID, queue := range sdkPending {
		sdk := sdks[sdkID]
		if jsonHash(sdk.Mode) != jsonHash(selectedMode) {
			return Prepared{}, fmt.Errorf("%w: SDK/archive mode mismatch", ErrUnrepresentable)
		}
		packages := map[string]planner.SDKPackage{}
		documents := map[string]planner.SDKDocument{}
		for _, pkg := range sdk.Packages {
			if _, ok := packages[pkg.ID]; ok {
				return Prepared{}, errors.New("duplicate SDK package")
			}
			packages[pkg.ID] = pkg
		}
		for _, d := range sdk.Documents {
			if _, ok := documents[d.ExecPath]; ok {
				return Prepared{}, errors.New("duplicate SDK document")
			}
			documents[d.ExecPath] = d
		}
		for len(queue) > 0 {
			id := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if sdkUsed[sdkID+"|"+id] {
				continue
			}
			sdkUsed[sdkID+"|"+id] = true
			s, ok := packages[id]
			if !ok || id != sdk.Prefix+s.ImportPath {
				return Prepared{}, errors.New("missing exact SDK package")
			}
			if _, ok := p.packages[id]; ok {
				return Prepared{}, fmt.Errorf("%w: colliding SDK configuration", ErrUnrepresentable)
			}
			pkg := flatPackage{ID: id, Name: s.Name, PkgPath: s.ImportPath, GoFiles: []string{}, CompiledGoFiles: []string{}, Imports: maps.Clone(s.Imports)}
			for index, list := range [][]string{s.GoFiles, s.CompiledGoFiles} {
				for _, execPath := range list {
					d, ok := documents[execPath]
					if !ok || !validPath(execPath) {
						return Prepared{}, errors.New("missing SDK document")
					}
					base := OutputBase
					if d.Generated {
						base = ExecRoot
					}
					name := base + "/" + execPath
					if err := addFile(name, d.SHA256, d.Bytes); err != nil {
						return Prepared{}, err
					}
					if index == 0 {
						pkg.GoFiles = append(pkg.GoFiles, name)
					} else {
						pkg.CompiledGoFiles = append(pkg.CompiledGoFiles, name)
					}
				}
			}
			if s.ExportFile != "" {
				if !validPath(s.ExportFile) {
					return Prepared{}, errors.New("SDK export path")
				}
				pkg.ExportFile = ExecRoot + "/" + s.ExportFile
			}
			for _, next := range s.Imports {
				queue = append(queue, next)
			}
			sort.Strings(pkg.GoFiles)
			sort.Strings(pkg.CompiledGoFiles)
			p.packages[id] = pkg
		}
	}
	if len(p.packages) > planner.MaxUnits {
		return Prepared{}, errors.New("selected package count")
	}
	var scope []string
	if len(rootUnits) > 64 {
		return Prepared{}, errors.New("driver root unit count")
	}
	for id := range rootUnits {
		u := selected[id]
		t, ok := targets[key(u.Owner)]
		if !ok || t.Tool || u.ArchiveLabel != u.Owner.Label {
			return Prepared{}, fmt.Errorf("%w: upstream root selector cannot name this archive", ErrUnrepresentable)
		}
		if !validImportPattern(u.ImportPath) {
			return Prepared{}, errors.New("invalid root import path")
		}
		p.patterns = append(p.patterns, u.ImportPath)
		p.roots = append(p.roots, u.ArchiveLabel)
		scope = append(scope, u.Owner.Label)
	}
	sort.Strings(p.patterns)
	sort.Strings(p.roots)
	sort.Strings(scope)
	for i := 1; i < len(p.patterns); i++ {
		if p.patterns[i] == p.patterns[i-1] {
			return Prepared{}, fmt.Errorf("%w: ambiguous root importpath", ErrUnrepresentable)
		}
	}
	p.scope = scopeBytes(plan.MappingSHA256, plan.DocumentsSHA256, p.roots, p.patterns)
	p.environment = closedEnvironment("set(" + strings.Join(scope, " ") + ")")
	cgo := "0"
	if selectedMode.Cgo {
		cgo = "1"
	}
	for _, tag := range selectedMode.Tags {
		if tag == "" || strings.ContainsAny(tag, " ,\x00\r\n") {
			return Prepared{}, errors.New("invalid closed Go build tag")
		}
	}
	p.environment = append(p.environment, "CGO_ENABLED="+cgo, "GOTAGS="+strings.Join(selectedMode.Tags, ","))
	p.environment = append(p.environment, ScopeDigestEnv+"="+hash(p.scope))
	p.request = []byte(`{"mode":31,"env":[],"build_flags":[],"tests":false,"overlay":{}}`)
	p.digest = jsonHash(struct {
		Mapping, Documents    string
		Roots                 []planner.Configured
		Patterns, Environment []string
		Request               string
	}{plan.MappingSHA256, plan.DocumentsSHA256, roots, p.patterns, p.environment, string(p.request)})
	return p, nil
}

// BazelEnvironment is the complete fixed worker/tool environment. No host or
// repository environment is inherited. Driver-only mode/selector variables are
// added by Prepare from the sealed package plan.
func BazelEnvironment() []string {
	return []string{
		"PATH=" + ToolPath, "LD_LIBRARY_PATH=" + LibraryPath, "GCOV=" + GCOVPath, "HOME=/scratch/home", "HOSTNAME=phebs-t451a", "TMPDIR=/scratch/tmp", "TMP=/scratch/tmp", "TEMP=/scratch/tmp", "LANG=C", "LC_ALL=C", "TZ=UTC",
		"XDG_CACHE_HOME=/scratch/cache", "XDG_CONFIG_HOME=/scratch/home", "XDG_DATA_HOME=/scratch/home",
		"GOROOT=/inputs/tools/go", "GOMAXPROCS=1", "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=file:///inputs/tools/cache/goproxy", "GOSUMDB=off", "GOCACHE=/scratch/gocache", "GOMODCACHE=/scratch/gomodcache", "GOTELEMETRY=off", "GOWORK=off", "CC=/scratch/toolchain/cc",
	}
}

func closedEnvironment(scope string) []string {
	return append(BazelEnvironment(),
		"BUILD_WORKSPACE_DIRECTORY="+Workspace, "BUILD_WORKING_DIRECTORY="+Workspace,
		"GOPACKAGESDRIVER_BAZEL="+BazelWrapperPath,
		"GOPACKAGESDRIVER_BAZEL_FLAGS="+strings.Join(BazelStartup(), " "),
		"GOPACKAGESDRIVER_BAZEL_COMMON_FLAGS="+strings.Join(BazelCommon(), " "),
		"GOPACKAGESDRIVER_BAZEL_BUILD_FLAGS="+strings.Join(BazelBuild(), " "),
		"GOPACKAGESDRIVER_BAZEL_QUERY_SCOPE="+scope,
	)
}

// Run is called only inside the already admitted Linux worker sandbox. The
// driver digest comes from that worker's independently sealed tool manifest.
// No target/environment/request argument is forwarded directly to the child.
func Run(ctx context.Context, plan planner.Plan, roots []planner.Configured, driverSHA256 string) ([]byte, error) {
	p, err := Prepare(plan, roots)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Getuid() != 65534 {
		return nil, errors.New("driver requires admitted Linux arm64 worker")
	}
	if err := checkDriver(driverSHA256); err != nil {
		return nil, err
	}
	if err := verifyFiles(ctx, p.files); err != nil {
		return nil, err
	}
	if err := writeClosedFile(ScopePath, p.scope, 0400); err != nil {
		return nil, err
	}
	if err := writeClosedFile(BazelWrapperPath, []byte(BazelWrapper), 0500); err != nil {
		return nil, err
	}
	data, err := runClosedChild(ctx, DriverPath, p.patterns, p.environment, p.request)
	if err != nil {
		return nil, err
	}
	if err := reconcileAndVerifyFiles(ctx, p, data); err != nil {
		return nil, err
	}
	return data, nil
}

func runClosedChild(ctx context.Context, executable string, args, environment []string, request []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, MaxWall)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir, command.Env, command.Stdin = Workspace, environment, bytes.NewReader(request)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = time.Second
	budget := outputBudget{remaining: MaxResponseBytes, cancel: cancel}
	stdout, stderr := outputWriter{budget: &budget}, outputWriter{budget: &budget}
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		// This spike can execute only its compiled-in neutral fixture. Raw
		// diagnostics remain local; the host receipt contains closed status.
		return nil, fmt.Errorf("closed driver execution refused: %w: %.4096s", err, stderr.data.Bytes())
	}
	return stdout.data.Bytes(), nil
}

func reconcileAndVerifyFiles(ctx context.Context, p Prepared, data []byte) error {
	if err := Reconcile(p, data); err != nil {
		return err
	}
	return verifyFiles(ctx, p.files)
}

func checkDriver(want string) error {
	if want != DriverSHA256 {
		return errors.New("missing pinned driver identity")
	}
	b, err := readFile(DriverPath, 128<<20)
	if err != nil {
		return err
	}
	if hash(b) != want {
		return errors.New("driver digest mismatch")
	}
	info, err := buildinfo.ReadFile(DriverPath)
	if err != nil {
		return err
	}
	if info.GoVersion != "go1.25.0" {
		return errors.New("driver SDK release-tag mismatch")
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if info.Path != "github.com/bazelbuild/rules_go/go/tools/gopackagesdriver" || settings["GOOS"] != "linux" || settings["GOARCH"] != "arm64" || settings["CGO_ENABLED"] != "0" || settings["GOARM64"] != "v8.0" {
		return errors.New("driver closed build profile mismatch")
	}
	return nil
}

func readFile(name string, limit int) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("declared file type/size refused")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, errors.New("declared file byte bound")
	}
	return b, nil
}
func verifyFiles(ctx context.Context, files map[string]fileIdentity) error {
	total := 0
	for name, want := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := readFile(name, planner.MaxFileBytes)
		if err != nil {
			return err
		}
		total += len(b)
		if total > planner.MaxProtoBytes {
			return errors.New("selected source byte bound")
		}
		if len(b) != want.bytes || hash(b) != want.digest {
			return errors.New("sealed document changed before driver")
		}
	}
	return nil
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int
	cancel    context.CancelFunc
}
type outputWriter struct {
	budget *outputBudget
	data   bytes.Buffer
}

func (w *outputWriter) Write(b []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	if len(b) > w.budget.remaining {
		w.budget.cancel()
		return 0, errors.New("driver aggregate output bound")
	}
	w.budget.remaining -= len(b)
	return w.data.Write(b)
}

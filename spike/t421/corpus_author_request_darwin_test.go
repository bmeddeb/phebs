//go:build darwin

package t421

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// This subprocess runs the real authenticated request/root/stream/dispatch
// machinery with the private tiny recipe. The actual CLI has no such mode and
// always decodes the frozen V3 plan; this is not a full-corpus ceremony test.
func TestCorpusAuthorRequestChild(t *testing.T) {
	if os.Getenv("T422_CORPUS_REQUEST_TEST_CHILD") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapAuthor(ctx)
	if err != nil {
		os.Exit(41)
	}
	defer func() { _ = lifetime.Close(context.Background()) }()
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, MaxExecutionCorpusAuthorRequestBytes+1))
	expected, digestErr := dispatchadmission.AuthorInputSHA256()
	request, requestErr := decodeCorpusAuthorRequest(raw, expected)
	if err != nil || digestErr != nil || requestErr != nil {
		os.Exit(42)
	}
	plan, info, _, err := readCorpusAuthorPlan(ctx, request.PlanPath, request.PlanSHA256)
	if err != nil {
		os.Exit(43)
	}
	author, err := resumeCorpusAuthor(ctx, corpusAuthorTestSource(t), request,
		dispatchadmission.ProductionTool("git"), dispatchadmission.StartAuthor)
	if err != nil {
		_ = plan.Close()
		os.Exit(44)
	}
	author.lifetime = dispatchadmission.ProcessContext()
	author.planFile, author.planInfo, author.planPath = plan, info, request.PlanPath
	defer func() { _ = author.Close() }()
	response, err := author.AuthorRequested(ctx)
	if err != nil {
		os.Exit(45)
	}
	if _, err := os.Stdout.Write(response); err != nil {
		os.Exit(46)
	}
	if err := dispatchadmission.WaitAuthorCheckpoint(ctx); err != nil {
		os.Exit(47)
	}
	if err := lifetime.Close(ctx); err != nil {
		os.Exit(48)
	}
}

func corpusAuthorRequestFixture(t *testing.T, parent, source string) ExecutionCorpusAuthorRequest {
	t.Helper()
	fixture := filepath.Join(parent, "neutral-plan-input")
	if err := os.WriteFile(fixture, []byte("neutral private protocol fixture; not a frozen V3 plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []ExecutionInputCopy{{Name: "plan", Path: fixture, SHA256: SHA256(raw)}}
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, inputs)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := ObserveExecutionCorpusSource(t.Context(), source)
	if err != nil || observed.ConfigSHA256 != "" {
		t.Fatalf("cold parent source observation: %+v, %v", observed, err)
	}
	return ExecutionCorpusAuthorRequest{Schema: ExecutionCorpusAuthorRequestSchema, PlanPath: filepath.Join(custody.Directory(), "plan"),
		PlanSHA256: SHA256(raw), SourcePath: source, SourceIdentity: observed.Identity, Revision: "a"}
}

func TestCorpusAuthorRequestThreeActualLifetimes(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	source := filepath.Join(parent, "parent-owned-source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	request := corpusAuthorRequestFixture(t, parent, source)
	var previous ExecutionCorpusAuthorResponse
	var total uint64
	for index, revision := range []string{"a", "b", "a-return"} {
		request.Revision = revision
		if index != 0 {
			request.Previous = &previous
		}
		response, attempts := corpusAuthorRequestRunChild(t, git, parent, request, "")
		want := uint64(3)
		if index == 0 {
			want = 4
		}
		if attempts != want || json.Unmarshal(response, &previous) != nil || previous.Result.Name != revision {
			t.Fatal("separate actual author lifetime did not return its measured current revision")
		}
		total += attempts
		observation, err := ObserveExecutionCorpusSource(t.Context(), source)
		if err != nil || observation.Identity != request.SourceIdentity || observation.ConfigSHA256 != previous.ConfigSHA256 {
			t.Fatal("parent's independently reobserved source differs from joined child response")
		}
		t.Logf("actual %s response: %d bytes, %d admitted commands", revision, len(response), attempts)
	}
	if total != 10 {
		t.Fatal("three actual author lifetimes did not preserve exact4/3/3 recipe")
	}
}

func corpusAuthorRequestRunChild(t *testing.T, git *ExecutionGitCustody, parent string, request ExecutionCorpusAuthorRequest, refusal string) ([]byte, uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	raw, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, binary, err := git.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := git.Environment(ctx, parent, parent)
	if err != nil {
		t.Fatal(err)
	}
	record := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramCorpusAuthor, InputSHA256: sha256.Sum256(raw),
		Producer: dispatchadmission.Producer{ID: 1, Binding: [32]byte{53}, Sites: dispatchadmission.AuthorSites()}, Phase: 1,
		Limits: dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1, Attempts: 4,
			WireBytes: 16 << 10, AckTimeout: 2 * time.Second},
		Control: dispatchadmission.PhaseControlConfig{Phases: []uint32{1}, InitialPhase: 1, MaximumPhases: 1, MaximumWireBytes: 4096, Timeout: 2 * time.Second},
		Tools:   []dispatchadmission.ProductionToolBinding{{Role: "git", Path: binary, Environment: environment}}}
	if refusal == "wrong digest" {
		record.InputSHA256[0] ^= 1
	}
	controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: record.Limits,
		Producers: []dispatchadmission.Producer{record.Producer}, Phases: []dispatchadmission.Phase{{ID: 1,
			Roles: []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit, Attempts: 4}}}}})
	if err != nil {
		t.Fatal(err)
	}
	admissionParent, admissionChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admissionParent.Close(); _ = admissionChild.Close() }()
	controlParent, controlChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controlParent.Close(); _ = controlChild.Close() }()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, testBinary, "-test.run=^TestCorpusAuthorRequestChild$")
	command.Env = []string{"T422_CORPUS_REQUEST_TEST_CHILD=1", dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector}
	command.ExtraFiles, command.Stdin = []*os.File{admissionChild, controlChild}, bytes.NewReader(raw)
	command.WaitDelay = time.Second
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	_ = admissionChild.Close()
	_ = controlChild.Close()
	defer func() {
		if command.ProcessState == nil {
			cancel()
			_ = command.Wait()
		}
	}()
	if err := dispatchadmission.SendProductionBootstrap(ctx, admissionParent, controlParent, record); err != nil {
		t.Fatal(err)
	}
	control, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Close() }()
	done := make(chan error, 1)
	go func() {
		done <- controller.ServeChecked(ctx, 1, command.Process.Pid, admissionParent, func(ctx context.Context, _ dispatchadmission.Site) error {
			_, _, err := git.Check(ctx)
			return err
		})
	}()
	reader := bufio.NewReaderSize(output, MaxExecutionCorpusAuthorResponseBytes+1)
	response, readErr := reader.ReadSlice('\n')
	response = bytes.Clone(response)
	if refusal == "" {
		if readErr != nil || len(response) > MaxExecutionCorpusAuthorResponseBytes {
			t.Fatalf("actual author response read: %v", readErr)
		}
		var decoded ExecutionCorpusAuthorResponse
		if err := json.Unmarshal(response, &decoded); err != nil {
			t.Fatal(err)
		}
		canonical, err := corpusAuthorCanonical(decoded, MaxExecutionCorpusAuthorResponseBytes)
		if err != nil || !bytes.Equal(response, canonical) || strings.Contains(string(response), request.SourcePath) ||
			strings.Contains(string(response), "device") || strings.Contains(string(response), "inode") {
			t.Fatal("response is not exact bounded source-free output")
		}
		if control.Pause(ctx) != nil || controller.Fence() != nil || control.Checkpoint(ctx) != nil {
			t.Fatal("author completion checkpoint failed")
		}
	} else if readErr == nil || len(response) != 0 {
		t.Fatal("refused request returned successful output")
	}
	waitErr := command.Wait()
	serveErr := <-done
	snapshot, snapshotErr := controller.Snapshot()
	if refusal == "" {
		if waitErr != nil || serveErr != nil || snapshotErr != nil || !snapshot.Complete || snapshot.Producers[0].Active != 0 {
			t.Fatalf("author child or transport did not close: %v/%v/%v", waitErr, serveErr, snapshotErr)
		}
	} else if waitErr == nil || snapshot.Attempts != 0 {
		t.Fatal("refused request launched a Git command")
	}
	return response, snapshot.Attempts
}

func TestCorpusAuthorRequestCrossProcessRefusals(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	for _, name := range []string{"wrong digest", "wrong inode", "wrong plan hash", "nonempty cold", "missing previous", "wrong previous manifest", "wrong previous parent", "wrong config"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.MkdirTemp(parent, "request-source-")
			if err != nil {
				t.Fatal(err)
			}
			request := corpusAuthorRequestFixture(t, parent, source)
			switch name {
			case "wrong inode":
				request.SourceIdentity.Inode++
			case "wrong plan hash":
				request.PlanSHA256 = SHA256([]byte("not the input"))
			case "nonempty cold":
				if err := os.WriteFile(filepath.Join(source, "unexpected"), []byte("retained"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing previous":
				request.Revision = "b"
			case "wrong previous manifest", "wrong previous parent", "wrong config":
				response, _ := corpusAuthorRequestRunChild(t, git, parent, request, "")
				request.Previous = &ExecutionCorpusAuthorResponse{}
				if err := json.Unmarshal(response, request.Previous); err != nil {
					t.Fatal(err)
				}
				request.Revision = "b"
				switch name {
				case "wrong previous manifest":
					request.Previous.Result.Manifest.RegularFiles++
				case "wrong previous parent":
					request.Previous.Result.ParentCommit = request.Previous.Result.Commit
				case "wrong config":
					request.Previous.ConfigSHA256 = SHA256([]byte("unobserved config"))
				}
			}
			corpusAuthorRequestRunChild(t, git, parent, request, name)
		})
	}
}

func TestCorpusAuthorRequestPlanProtectionSpansUse(t *testing.T) {
	parent, original := inputCustodyTestFixture(t)
	raw, err := os.ReadFile(original.Path)
	if err != nil {
		t.Fatal(err)
	}
	if file, _, _, err := readCorpusAuthorPlan(t.Context(), original.Path, SHA256(raw)); err == nil || file != nil {
		t.Fatal("mutable original plan was accepted")
	}
	inputs := []ExecutionInputCopy{{Name: "plan", Path: original.Path, SHA256: SHA256(raw)}}
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, inputs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(custody.Directory(), "plan")
	file, info, got, err := readCorpusAuthorPlan(t.Context(), path, SHA256(raw))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("actual protected plan read failed")
	}
	defer func() { _ = file.Close() }()
	author := &ExecutionCorpusAuthor{planFile: file, planInfo: info, planPath: path}
	if author.checkPlan(t.Context()) != nil {
		t.Fatal("unchanged protected plan refused")
	}
	if inputCustodyFlag(file, false) != nil {
		t.Fatal("cannot exercise owned test plan protection loss")
	}
	if author.checkPlan(t.Context()) == nil {
		t.Fatal("plan protection loss was not refused before reuse")
	}
}

func TestCorpusAuthorFrozenResponseByteBound(t *testing.T) {
	// V3 deliberately retains these exact V2 functional source recipes. This
	// is a serialization-size fixture, never an actually authored Git result.
	raw, err := os.ReadFile("plan-v2.json")
	if err != nil || SHA256(raw) != retainedPlanV2SHA256 {
		t.Fatal("retained source recipe bytes changed")
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil || len(plan.Revisions.Physical) != 3 {
		t.Fatal("retained source recipe unavailable")
	}
	source := &corpusAuthorSource{recipe: plan.Revisions.SourceRecipe, profile: plan.Profile}
	parent := ""
	for _, physical := range plan.Revisions.Physical {
		commit, err := canonicalGitCommitBytesFor(physical.ExpectedTree, parent, physical.CommitMessage, source.recipe)
		if err != nil {
			t.Fatal(err)
		}
		response := ExecutionCorpusAuthorResponse{Result: AuthoredExecutionRevision{Name: physical.Name,
			Commit: physical.ExpectedCommit, Tree: physical.ExpectedTree, ParentCommit: parent,
			Manifest: source.manifest(physical, physical.ExpectedTreeInventory, SHA256(commit))}, ConfigSHA256: SHA256(nil)}
		encoded, err := corpusAuthorCanonical(response, MaxExecutionCorpusAuthorResponseBytes)
		if err != nil {
			t.Fatal("fully populated frozen manifest exceeded4KiB response bound")
		}
		t.Logf("full frozen %s response shape: %d bytes", physical.Name, len(encoded))
		request := ExecutionCorpusAuthorRequest{Schema: ExecutionCorpusAuthorRequestSchema,
			PlanPath: "/" + strings.Repeat("p", 4095), PlanSHA256: SHA256(nil), SourcePath: "/" + strings.Repeat("s", 4095),
			SourceIdentity: ExecutionCorpusSourceIdentity{Device: -1 << 31, Inode: ^uint64(0), Generation: ^uint32(0), Volume: [2]int32{-1 << 31, -1 << 31}},
			Revision:       "b", Previous: &response}
		if _, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes); err != nil {
			t.Fatal("bounded private paths plus full manifest exceeded16KiB request bound")
		}
		parent = physical.ExpectedCommit
	}
}

func TestCorpusAuthorRequestCanonicalRefusals(t *testing.T) {
	request := ExecutionCorpusAuthorRequest{Schema: ExecutionCorpusAuthorRequestSchema, PlanPath: "/private/plan", PlanSHA256: SHA256(nil),
		SourcePath: "/private/source", SourceIdentity: ExecutionCorpusSourceIdentity{Inode: 1}, Revision: "a"}
	raw, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "oversize", "unknown", "duplicate", "trailing", "not canonical", "zero binding", "missing newline"} {
		t.Run(name, func(t *testing.T) {
			candidate := bytes.Clone(raw)
			switch name {
			case "oversize":
				candidate = bytes.Repeat([]byte("x"), MaxExecutionCorpusAuthorRequestBytes+1)
			case "unknown":
				candidate = append([]byte(`{"unknown":1,`), raw[1:]...)
			case "duplicate":
				candidate = append([]byte(`{"revision":"a",`), raw[1:]...)
			case "trailing":
				candidate = append(candidate, []byte("{}\n")...)
			case "not canonical":
				candidate = append([]byte(" "), candidate...)
			case "missing newline":
				candidate = candidate[:len(candidate)-1]
			}
			digest := sha256.Sum256(candidate)
			if name == "zero binding" {
				digest = [32]byte{}
			}
			_, err := decodeCorpusAuthorRequest(candidate, digest)
			if (err == nil) != (name == "valid") {
				t.Fatalf("canonical request %s: %v", name, err)
			}
		})
	}
	if author, err := OpenExecutionCorpusAuthorRequest(t.Context(), raw); !errors.Is(err, ErrExecutionCorpusAuthor) || author != nil {
		t.Fatal("unbootstrapped public request acquired input custody")
	}
}

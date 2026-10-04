package dispatchadmission

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

const (
	SiteCandidateTree uint32 = iota + 1
	SiteCompatibilitySandbox
	SiteExtractSubtree
	SiteExtractTree
	SiteGitBlobBatch
	SiteGitOutput
	SiteIndexBuild
	SiteRecoverySurreal
	SiteRepositoryTree
	SiteServiceCatalogCensus
	SiteSourcePartitionBatch
	SiteSurrealEngine
	SiteSurrealVersion
	SiteSyncGit
	SiteSyncGitHistory
	SiteSyncGitRead
	SiteCorpusAuthorGit
)

const (
	RoleGit uint32 = iota + 1
	RoleSurreal
	RoleZoekt
	RoleCompatibility
)

var ErrProductionBootstrap = errors.New("production dispatch bootstrap unavailable or invalid")

// SurrealPassEnvKey is the only caller-supplied environment key the
// production bootstrap admits for SurrealDB child sites. The store supplies
// the database-bound password selected for a persistent engine, or the fresh
// password selected for a volatile engine; the closed tool record never
// carries credentials, and the password never appears on argv.
const SurrealPassEnvKey = "SURREAL_PASS"

// surrealPassSites are the dispatch sites that must receive a caller-supplied
// SurrealDB root password through the closed extra-environment channel.
func surrealPassSites(site uint32) bool {
	return site == SiteSurrealEngine || site == SiteRecoverySurreal
}

var productionRuntime atomic.Pointer[ProductionLifetime]
var productionBootstrapStarted atomic.Bool

// ProductionSites returns the source-owned whole-repository dispatch inventory.
// It is not an attempt budget or input-admission assertion. Partition-scoped Git
// batch readers are one-shots; only the deliberately long-lived engine carries.
func ProductionSites() []Site {
	result := make([]Site, 0, SiteSyncGitRead)
	for id := SiteCandidateTree; id <= SiteSyncGitRead; id++ {
		role := RoleGit
		switch id {
		case SiteCompatibilitySandbox:
			role = RoleCompatibility
		case SiteIndexBuild:
			role = RoleZoekt
		case SiteRecoverySurreal, SiteSurrealEngine, SiteSurrealVersion:
			role = RoleSurreal
		}
		result = append(result, Site{ID: id, Role: role, Persistent: id == SiteSurrealEngine})
	}
	return result
}

// ProductionLifetime owns the installed producer and phase receiver, not its
// child commands or parent-held input custody. Main must join application work
// first, then Close, and propagate its terminal failure even if work returned nil.
// A failed/closed lifetime is never replaced with the ordinary pass-through.
type ProductionLifetime struct {
	program                  string
	semanticMode             string
	producerID               uint32
	inputSHA256              [32]byte
	client                   *Client
	controlDone              <-chan error
	tools                    map[string]ProductionToolBinding
	closeOnce                sync.Once
	closeErr                 error
	storeMu                  sync.Mutex
	storeClient              *storeaccounting.Client
	storeOwner               *storeaccounting.SDKOwner
	cancelStore              context.CancelFunc
	storeTaken               bool
	storeClosed              bool
	storeRetired             bool
	workspaceMu              sync.Mutex
	workspace                *productionWorkspace
	warmWorkspace            *warmStartWorkspace
	physicalWorkspace        *warmStartWorkspace
	cancelArchive            context.CancelFunc
	archiveMeasurements      uint32
	archiveMeasurement       *archiveMeasurementClient
	backupMeasurementMu      sync.Mutex
	backupMeasurementMaximum uint32
	backupMeasurementGuard   func(context.Context, func(context.Context) error) error
}

// ProductionSemanticSnapshot contains copied parent-bound launch identity and
// current local phase/window state, never a mutable phase setter or native
// readiness assertion. An HTTP caller must already hold its admitted request
// owner so FenceRequests cannot complete a window transition during its tail.
type ProductionSemanticSnapshot struct {
	Mode            string
	InputSHA256     [32]byte
	ProducerID      uint32
	Phase           uint32
	RequestSequence uint64
	// OrdinaryOwnersDrained describes the completed ordinary-owner fence only.
	// An admitted preparation request may still be active; this is not request
	// drainage, native inactivity, authority readiness or input custody.
	OrdinaryOwnersDrained bool
}

// ProductionSemanticSelected remains true after a selected lifetime fails.
// Main must then refuse, not fall back to ordinary execution or ignore stdin.
func ProductionSemanticSelected() bool {
	lifetime := productionRuntime.Load()
	return lifetime != nil && lifetime.semanticMode != ""
}

func ProductionSemanticState() (ProductionSemanticSnapshot, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.program != ProgramPhebs || lifetime.semanticMode != ProductionSemanticV3 ||
		lifetime.producerID == 0 || lifetime.inputSHA256 == ([32]byte{}) || lifetime.client == nil {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	client := lifetime.client
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed || client.err != nil || client.ctx.Err() != nil || !client.ownersRequired {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	ordinaryOwnersDrained := false
	if owners := client.owners; owners != nil {
		owners.mu.Lock()
		ordinaryOwnersDrained = owners.err == nil && owners.ctx.Err() == nil &&
			owners.paused && owners.pausedReady && owners.active == 0
		owners.mu.Unlock()
	}
	return ProductionSemanticSnapshot{Mode: lifetime.semanticMode, InputSHA256: lifetime.inputSHA256,
		ProducerID: lifetime.producerID, Phase: client.phase, RequestSequence: client.ownerRequestSequence,
		OrdinaryOwnersDrained: ordinaryOwnersDrained}, nil
}

// AuthorSites names the real author's one shared native Git start boundary.
// Its four source-owned command choices remain the author's responsibility.
func AuthorSites() []Site { return []Site{{ID: SiteCorpusAuthorGit, Role: RoleGit}} }

// RequireAuthorBootstrap distinguishes a live author lifetime from ordinary
// execution or the application program. It attests no source/plan continuity;
// the real parent must separately bind those inputs before bootstrapping.
func RequireAuthorBootstrap() error {
	runtime := productionRuntime.Load()
	if runtime == nil || runtime.program != ProgramCorpusAuthor || runtime.client.Context().Err() != nil {
		return ErrProductionBootstrap
	}
	runtime.client.mu.Lock()
	defer runtime.client.mu.Unlock()
	if runtime.client.closed || runtime.client.err != nil {
		return ErrProductionBootstrap
	}
	return nil
}

// AuthorInputSHA256 returns only the live author program's parent-authenticated
// request digest. It does not attest the request's semantic inputs or custody.
func AuthorInputSHA256() ([32]byte, error) {
	if RequireAuthorBootstrap() != nil {
		return [32]byte{}, ErrProductionBootstrap
	}
	return productionRuntime.Load().inputSHA256, nil
}

// WaitAuthorCheckpoint waits for the existing remote checkpoint's complete
// echo ACK, not merely the local DA01 checkpoint flag. Only then may an author
// close its lifetime without cutting off the parent's pending PC01 response.
// Semantic completion/output must precede this wait; it adds no wire operation.
func WaitAuthorCheckpoint(ctx context.Context) error {
	return waitAuthorCompletion(ctx, false)
}

// WaitAuthorCompletion waits only for the authenticated bootstrap's selected
// terminal handshake. Terminal authors use a complete Pause echo ACK followed
// by their own Client.Close, not a whole-controller checkpoint. The default
// legacy author still requires its complete checkpoint echo ACK.
func WaitAuthorCompletion(ctx context.Context) error {
	if RequireAuthorBootstrap() != nil {
		return ErrProductionBootstrap
	}
	client := productionRuntime.Load().client
	client.mu.Lock()
	terminal := client.controlTerminalAuthor
	client.mu.Unlock()
	return waitAuthorCompletion(ctx, terminal)
}

func waitAuthorCompletion(ctx context.Context, terminal bool) error {
	if ctx == nil || RequireAuthorBootstrap() != nil {
		return ErrProductionBootstrap
	}
	client := productionRuntime.Load().client
	for {
		client.mu.Lock()
		err := client.err
		if client.controlTerminalAuthor != terminal {
			err = ErrProductionBootstrap
		}
		if err == nil && (client.closed || client.ctx.Err() != nil) {
			err = ErrIncomplete
		}
		ready := client.checkpoint && client.controlCheckpointAcknowledged
		if terminal {
			ready = client.paused && client.controlPauseAcknowledged
		}
		changed := client.changed
		client.mu.Unlock()
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.ctx.Done():
			return ErrIncomplete
		case <-changed:
		}
	}
}

// ProcessContext adds no state to the ordinary path. Exact-mode main uses this
// lifetime as its shutdown/error parent, including backup/restore and startup
// probes that otherwise use background contexts. It is not worker readiness.
func ProcessContext() context.Context {
	if runtime := productionRuntime.Load(); runtime != nil {
		return runtime.client.Context()
	}
	return context.Background()
}

// ProductionTool resolves before exec.Command's ambient LookPath. Unknown or
// unadmitted exact-mode roles return an empty path, never an ambient fallback.
func ProductionTool(role string) string {
	if runtime := productionRuntime.Load(); runtime != nil {
		return runtime.tools[role].Path
	}
	return role
}

func (lifetime *ProductionLifetime) Close(ctx context.Context) error {
	if lifetime == nil {
		return nil
	}
	lifetime.closeOnce.Do(func() {
		if lifetime.cancelArchive != nil {
			defer lifetime.cancelArchive()
		}
		if ctx == nil {
			ctx = context.Background()
			lifetime.closeErr = lifetime.client.fail(ErrCanceled)
		}
		closeCtx, cancel := context.WithTimeout(ctx, lifetime.client.limits.AckTimeout)
		defer cancel()
		if err := lifetime.closePhysicalPostAuthorWorkspace(closeCtx); err != nil {
			lifetime.closeErr = errors.Join(lifetime.closeErr, lifetime.client.fail(err))
			return
		}
		if err := lifetime.closeWarmStartWorkspace(closeCtx); err != nil {
			// A still-running callback owns SDK/FD6. Fail closed without
			// releasing those resources beneath its native walk.
			lifetime.closeErr = errors.Join(lifetime.closeErr, lifetime.client.fail(err))
			return
		}
		archiveErr := lifetime.closeArchiveMeasurement(closeCtx)
		lifetime.closeErr = errors.Join(lifetime.closeErr, archiveErr)
		storeErr := lifetime.closeStore(closeCtx)
		lifetime.closeErr = errors.Join(lifetime.closeErr, storeErr)
		// Main joins lifecycle callbacks before closing its lifetime. The
		// borrowed workspace cannot outlive that join or acknowledge a failed
		// descriptor close as clean dispatch completion.
		workspaceErr := lifetime.closeWorkspace()
		lifetime.closeErr = errors.Join(lifetime.closeErr, workspaceErr)
		if storeErr != nil || workspaceErr != nil || archiveErr != nil {
			// A failed SDK/SA close cannot acknowledge clean dispatch closure.
			_ = lifetime.client.fail(ErrIncomplete)
		}
		lifetime.closeErr = errors.Join(lifetime.closeErr, lifetime.client.Close(closeCtx))
		if lifetime.closeErr != nil {
			// Release both transports even when accounting cannot close. The
			// owning launcher still joins commands and retains uncertain custody.
			_ = lifetime.client.fail(ErrIncomplete)
			_ = lifetime.client.conn.Close()
		}
		lifetime.closeErr = errors.Join(lifetime.closeErr, <-lifetime.controlDone)
	})
	return lifetime.closeErr
}

func productionRole(site uint32) string {
	switch site {
	case SiteCandidateTree, SiteExtractSubtree, SiteExtractTree, SiteGitBlobBatch, SiteGitOutput,
		SiteRepositoryTree, SiteServiceCatalogCensus, SiteSourcePartitionBatch,
		SiteSyncGit, SiteSyncGitHistory, SiteSyncGitRead:
		return "git"
	case SiteIndexBuild:
		return "zoekt-git-index"
	case SiteRecoverySurreal, SiteSurrealEngine, SiteSurrealVersion:
		return "surreal"
	default:
		return ""
	}
}

// StartProduction enforces the source-owned direct path and closed child
// environment, then waits for the parent's input check and committed DA01 ACK.
// The parent must retain genuine tool/config/resource custody through child join;
// neither this wrapper nor a caller-authored bootstrap record proves admission.
// Command context, output limits, native session ownership and Wait remain the
// existing caller's responsibility. No child may inherit the control endpoints.
func StartProduction(ctx context.Context, site uint32, command *exec.Cmd) (Handle, error) {
	runtime := productionRuntime.Load()
	if runtime == nil {
		return (*Client)(nil).Start(ctx, site, command)
	}
	if runtime.program != ProgramPhebs {
		return Handle{}, runtime.client.fail(ErrProductionBootstrap)
	}
	return startProductionCommand(ctx, runtime, site, productionRole(site), command)
}

// StartAuthor never falls back to an uncounted command. Only the explicitly
// bootstrapped author program can use its single source-owned Git start site.
func StartAuthor(ctx context.Context, command *exec.Cmd) (Handle, error) {
	runtime := productionRuntime.Load()
	if runtime == nil {
		return Handle{}, ErrProductionBootstrap
	}
	if runtime.program != ProgramCorpusAuthor {
		return Handle{}, runtime.client.fail(ErrProductionBootstrap)
	}
	return startProductionCommand(ctx, runtime, SiteCorpusAuthorGit, "git", command)
}

func startProductionCommand(ctx context.Context, runtime *ProductionLifetime, site uint32, role string, command *exec.Cmd) (Handle, error) {
	return startProductionCommandWithEnv(ctx, runtime, site, role, command, nil)
}

// admitSurrealPassEnv validates the caller-supplied SurrealDB password entry.
// SurrealDB child sites fail closed without exactly one exact
// "SURREAL_PASS=<value>" pair; every other site refuses any extra entry.
func admitSurrealPassEnv(site uint32, extraEnv []string) (string, error) {
	if !surrealPassSites(site) {
		if len(extraEnv) != 0 {
			return "", ErrProductionBootstrap
		}
		return "", nil
	}
	if len(extraEnv) != 1 {
		return "", ErrProductionBootstrap
	}
	key, value, found := strings.Cut(extraEnv[0], "=")
	if !found || key != SurrealPassEnvKey || value == "" ||
		strings.ContainsAny(value, "\x00\n") {
		return "", ErrProductionBootstrap
	}
	return extraEnv[0], nil
}

func startProductionCommandWithEnv(ctx context.Context, runtime *ProductionLifetime, site uint32, role string, command *exec.Cmd, extraEnv []string) (Handle, error) {
	tool, known := runtime.tools[role]
	if !known || command == nil || command.Err != nil || command.Path != tool.Path ||
		len(command.Args) == 0 || command.Args[0] != tool.Path || len(command.ExtraFiles) != 0 {
		return Handle{}, runtime.client.fail(ErrProductionBootstrap)
	}
	command.Env = slices.Clone(tool.Environment)
	if runtime.semanticMode == ProductionSemanticV3 && role == "zoekt-git-index" && !hasProductionIndexMode(command.Env) {
		return Handle{}, runtime.client.fail(ErrProductionBootstrap)
	}
	passEntry, err := admitSurrealPassEnv(site, extraEnv)
	if err != nil {
		return Handle{}, runtime.client.fail(err)
	}
	switch site {
	case SiteRecoverySurreal:
		// The caller supplies the live child's database-bound password; the
		// bootstrap still pins the non-secret root username from the closed record.
		command.Env = append(command.Env, "SURREAL_USER=root", passEntry)
	case SiteSurrealEngine:
		// The engine start supplies both user (as an argv flag) and the
		// selected root password through the admitted environment entry.
		command.Env = append(command.Env, passEntry)
	}
	return runtime.client.Start(ctx, site, command)
}

// StartProductionWithEnv is StartProduction with a caller-supplied extra
// environment list. Only the exact "SURREAL_PASS=<value>" entry is admitted,
// and only for the SurrealDB child sites that need the selected root password.
// The caller retains custody of the value until the child is admitted.
func StartProductionWithEnv(ctx context.Context, site uint32, command *exec.Cmd, extraEnv []string) (Handle, error) {
	runtime := productionRuntime.Load()
	if runtime == nil {
		return (*Client)(nil).Start(ctx, site, command)
	}
	return startProductionCommandWithEnv(ctx, runtime, site, productionRole(site), command, extraEnv)
}

func RunProduction(ctx context.Context, site uint32, command *exec.Cmd) error {
	if productionRuntime.Load() == nil {
		return (*Client)(nil).Run(ctx, site, command)
	}
	handle, err := StartProduction(ctx, site, command)
	if err != nil {
		return err
	}
	return handle.Wait()
}

// RunProductionWithEnv is RunProduction with a caller-supplied extra
// environment list. It exists for the SurrealDB export/import CLI, which must
// authenticate with the live child's database-bound root password.
func RunProductionWithEnv(ctx context.Context, site uint32, command *exec.Cmd, extraEnv []string) error {
	if productionRuntime.Load() == nil {
		return (*Client)(nil).Run(ctx, site, command)
	}
	handle, err := StartProductionWithEnv(ctx, site, command, extraEnv)
	if err != nil {
		return err
	}
	return handle.Wait()
}

// CombinedOutputProduction preserves the ordinary exec.Cmd behavior. Exact
// Surreal version output is limited to 4 KiB; overflow latches failure and kills
// the owned command, which is still joined before returning. The caller retains
// its existing five-second command context. This is not a report collector.
func CombinedOutputProduction(ctx context.Context, site uint32, command *exec.Cmd) ([]byte, error) {
	return productionOutput(ctx, site, command, true)
}

// OutputProduction returns stdout only. In exact mode, both output streams
// still share the version probe's 4 KiB limit and overflow kills and joins it.
func OutputProduction(ctx context.Context, site uint32, command *exec.Cmd) ([]byte, error) {
	return productionOutput(ctx, site, command, false)
}

func productionOutput(ctx context.Context, site uint32, command *exec.Cmd, combined bool) ([]byte, error) {
	if command == nil {
		return nil, ErrConfig
	}
	runtime := productionRuntime.Load()
	if runtime == nil {
		if combined {
			return command.CombinedOutput()
		}
		return command.Output()
	}
	if site != SiteSurrealVersion || command.Stdout != nil || command.Stderr != nil {
		return nil, runtime.client.fail(ErrProductionBootstrap)
	}
	output := productionVersionOutput{command: command, client: runtime.client}
	command.Stdout, command.Stderr = &output, &output
	if !combined {
		command.Stderr = productionVersionStderr{output: &output}
	}
	if command.WaitDelay == 0 || command.WaitDelay > time.Second {
		command.WaitDelay = time.Second
	}
	err := RunProduction(ctx, site, command)
	return output.buffer[:output.size], errors.Join(err, output.err)
}

type productionVersionOutput struct {
	mu      sync.Mutex
	buffer  [4 << 10]byte
	size    int
	used    int
	command *exec.Cmd
	client  *Client
	err     error
}

func (output *productionVersionOutput) Write(data []byte) (int, error) {
	return output.write(data, true)
}

type productionVersionStderr struct{ output *productionVersionOutput }

func (stderr productionVersionStderr) Write(data []byte) (int, error) {
	return stderr.output.write(data, false)
}

func (output *productionVersionOutput) write(data []byte, retain bool) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.err != nil {
		return 0, output.err
	}
	accepted := min(len(data), len(output.buffer)-output.used)
	output.used += accepted
	if retain {
		output.size += copy(output.buffer[output.size:], data[:accepted])
	}
	if accepted == len(data) {
		return accepted, nil
	}
	output.err = output.client.fail(ErrLimit)
	// exec invokes this pipe-copy writer only after assigning command.Process.
	// Killing the owned process unblocks Wait even before the caller's timeout.
	_ = output.command.Process.Kill()
	return accepted, output.err
}

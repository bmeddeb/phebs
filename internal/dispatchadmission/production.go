package dispatchadmission

import (
	"context"
	"errors"
	"os/exec"
	"slices"
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
	program      string
	semanticMode string
	producerID   uint32
	inputSHA256  [32]byte
	client       *Client
	controlDone  <-chan error
	tools        map[string]ProductionToolBinding
	closeOnce    sync.Once
	closeErr     error
	storeMu      sync.Mutex
	storeClient  *storeaccounting.Client
	storeOwner   *storeaccounting.SDKOwner
	cancelStore  context.CancelFunc
	storeTaken   bool
	storeClosed  bool
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
		if ctx == nil {
			ctx = context.Background()
			lifetime.closeErr = lifetime.client.fail(ErrCanceled)
		}
		closeCtx, cancel := context.WithTimeout(ctx, lifetime.client.limits.AckTimeout)
		defer cancel()
		storeErr := lifetime.closeStore(closeCtx)
		lifetime.closeErr = errors.Join(lifetime.closeErr, storeErr)
		if storeErr != nil {
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
	tool, known := runtime.tools[role]
	if !known || command == nil || command.Err != nil || command.Path != tool.Path ||
		len(command.Args) == 0 || command.Args[0] != tool.Path || len(command.ExtraFiles) != 0 {
		return Handle{}, runtime.client.fail(ErrProductionBootstrap)
	}
	command.Env = slices.Clone(tool.Environment)
	if site == SiteRecoverySurreal {
		command.Env = append(command.Env, "SURREAL_USER=root", "SURREAL_PASS=root")
	}
	return runtime.client.Start(ctx, site, command)
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

// CombinedOutputProduction preserves the ordinary exec.Cmd behavior. Exact
// Surreal version output is limited to 4 KiB; overflow latches failure and kills
// the owned command, which is still joined before returning. The caller retains
// its existing five-second command context. This is not a report collector.
func CombinedOutputProduction(ctx context.Context, site uint32, command *exec.Cmd) ([]byte, error) {
	if command == nil {
		return nil, ErrConfig
	}
	if productionRuntime.Load() == nil {
		return command.CombinedOutput()
	}
	if site != SiteSurrealVersion || command.Stdout != nil || command.Stderr != nil {
		return nil, productionRuntime.Load().client.fail(ErrProductionBootstrap)
	}
	runtime := productionRuntime.Load()
	output := productionVersionOutput{command: command, client: runtime.client}
	command.Stdout, command.Stderr = &output, &output
	if command.WaitDelay == 0 || command.WaitDelay > time.Second {
		command.WaitDelay = time.Second
	}
	err := RunProduction(ctx, site, command)
	return output.buffer[:output.size], errors.Join(err, output.err)
}

type productionVersionOutput struct {
	buffer  [4 << 10]byte
	size    int
	command *exec.Cmd
	client  *Client
	err     error
}

func (output *productionVersionOutput) Write(data []byte) (int, error) {
	if output.err != nil {
		return 0, output.err
	}
	accepted := copy(output.buffer[output.size:], data)
	output.size += accepted
	if accepted == len(data) {
		return accepted, nil
	}
	output.err = output.client.fail(ErrLimit)
	// exec invokes this pipe-copy writer only after assigning command.Process.
	// Killing the owned process unblocks Wait even before the caller's timeout.
	_ = output.command.Process.Kill()
	return accepted, output.err
}

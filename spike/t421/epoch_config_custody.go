package t421

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"gopkg.in/yaml.v3"
)

const (
	maxEpochCatalogBytes = 16 << 20
	maxEpochConfigBytes  = 64 << 10
)

var ErrExecutionEpochConfigs = errors.New("execution epoch configuration custody unavailable or changed")

// ExecutionEpochConfig is a private launcher's copied input selection, not a
// profile admission or a promise that a released address remains available.
// Paths and APIKey must never enter public evidence.
type ExecutionEpochConfig struct {
	Epoch                      uint64
	LogicalRevision            string
	ConfigPath, ConfigSHA256   string
	CatalogPath, CatalogSHA256 string
	Repository, Listen, APIKey string
	DataRoot, Home, Temporary  string
	BackupRoot                 string
}

// ExecutionEpochConfigCustody borrows the genuine author's protected inputs
// and native source/lease. It owns only eight protected input files, four new
// writable root descriptors and five initially reserved loopback listeners.
// Never copy it. The parent must retain it and Author through all joined uses;
// this object neither owns a server nor proves that those uses have joined.
type ExecutionEpochConfigCustody struct {
	mu        sync.Mutex
	author    *ExecutionAuthorCustody
	roots     []productionRoot
	catalogs  *ExecutionInputCustody
	configs   *ExecutionInputCustody
	epochs    [5]ExecutionEpochConfig
	listeners [5]net.Listener
	released  uint64
	stages    []string
	active    bool // A native epoch user must join before these inputs can close.
	closed    bool
	err       error
}

// PrepareExecutionEpochConfigs uses only the actual author's already-admitted
// plan and source root. It regenerates the bounded target catalog/path overlay,
// not the two-million-file physical corpus, and checks every canonical catalog
// against the exact plan bytes/digest before protecting it. No source census,
// second lease, SDK inventory, tool execution or runtime-constant issuer exists
// here. The five-minute outer context checks synchronous generators before and
// after; it cannot interrupt their contextless CPU work.
func PrepareExecutionEpochConfigs(ctx context.Context, author *ExecutionAuthorCustody) (_ *ExecutionEpochConfigCustody, retErr error) {
	if ctx == nil || ctx.Err() != nil || author == nil {
		return nil, ErrExecutionEpochConfigs
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// All paths that need both owners take author before configuration custody.
	// Setup is serialized with author starts; it performs no native child work.
	author.mu.Lock()
	defer author.mu.Unlock()
	if author.active || author.check(ctx) != nil {
		return nil, ErrExecutionEpochConfigs
	}
	_, raw, err := readAuthorCustodyPlan(ctx, author.request.Plan)
	if err != nil || SHA256(raw) != author.planSHA256 {
		return nil, ErrExecutionEpochConfigs
	}
	var plan Plan
	if json.Unmarshal(raw, &plan) != nil || plan.Schema != PlanV3Schema || validatePlan(plan, &plan.Revisions) != nil || ctx.Err() != nil {
		return nil, ErrExecutionEpochConfigs
	}
	// DecodePlan already admitted these protected bytes in the author issuer;
	// the trusted-revision path avoids repeating physical tree regeneration.
	custody := &ExecutionEpochConfigCustody{author: author}
	defer func() {
		if retErr != nil {
			custody.err = ErrExecutionEpochConfigs
			_ = custody.closeLocked()
			retErr = ErrExecutionEpochConfigs
		}
	}()
	catalogs, err := epochCatalogInputs(ctx, plan)
	if err != nil {
		return custody, ErrExecutionEpochConfigs
	}
	custody.catalogs, err = custody.protectInputs(ctx, catalogs)
	if err != nil {
		return custody, ErrExecutionEpochConfigs
	}
	for _, prefix := range []string{"t422-data-", "t422-home-", "t422-tmp-", "t422-backup-"} {
		if ctx.Err() != nil {
			return custody, ErrExecutionEpochConfigs
		}
		path, err := os.MkdirTemp(author.parent, prefix)
		if err != nil {
			return custody, ErrExecutionEpochConfigs
		}
		custody.roots = append(custody.roots, productionRoot{path: path})
		root, err := openProductionRoot(path)
		if err != nil {
			return custody, ErrExecutionEpochConfigs
		}
		custody.roots[len(custody.roots)-1] = root
		if root.volume != author.roots[0].volume {
			return custody, ErrExecutionEpochConfigs
		}
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return custody, ErrExecutionEpochConfigs
	}
	apiKey := hex.EncodeToString(key[:])
	source := productionSourceURL(author.roots[1].path)
	repository, err := phebssync.RepoName(source)
	if err != nil {
		return custody, ErrExecutionEpochConfigs
	}
	inputs := make([]epochGeneratedInput, 0, len(custody.epochs))
	for index, logical := range []string{"a", "b", "a-return", "a-return", "a-return"} {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
		if err != nil {
			return custody, ErrExecutionEpochConfigs
		}
		custody.listeners[index] = listener
		catalogIndex := slices.Index([]string{"a", "b", "a-return"}, logical)
		catalogPath, err := custody.catalogs.Check(ctx, "catalog-"+logical)
		if err != nil {
			return custody, ErrExecutionEpochConfigs
		}
		epoch := ExecutionEpochConfig{Epoch: uint64(index + 1), LogicalRevision: logical,
			CatalogPath: catalogPath, CatalogSHA256: plan.Revisions.Logical[catalogIndex].CatalogSource.SHA256,
			Repository: repository, Listen: listener.Addr().String(), APIKey: apiKey,
			DataRoot: custody.roots[0].path, Home: custody.roots[1].path, Temporary: custody.roots[2].path, BackupRoot: custody.roots[3].path}
		raw, err := epochConfigBytes(plan, epoch, source)
		if err != nil {
			return custody, ErrExecutionEpochConfigs
		}
		epoch.ConfigSHA256 = SHA256(raw)
		custody.epochs[index] = epoch
		inputs = append(inputs, epochGeneratedInput{name: "config-" + strconv.Itoa(index+1), raw: raw})
	}
	custody.configs, err = custody.protectInputs(ctx, inputs)
	if err != nil {
		return custody, ErrExecutionEpochConfigs
	}
	for index := range custody.epochs {
		custody.epochs[index].ConfigPath, err = custody.configs.Check(ctx, "config-"+strconv.Itoa(index+1))
		if err != nil {
			return custody, ErrExecutionEpochConfigs
		}
	}
	if custody.checkLocked(ctx, 1) != nil {
		return custody, ErrExecutionEpochConfigs
	}
	return custody, nil
}

type epochGeneratedInput struct {
	name string
	raw  []byte
}

func epochCatalogInputs(ctx context.Context, plan Plan) ([]epochGeneratedInput, error) {
	if ctx == nil || ctx.Err() != nil || len(plan.Revisions.Logical) != 3 {
		return nil, ErrExecutionEpochConfigs
	}
	target, err := frozenTargetCorpus()
	if err != nil || ctx.Err() != nil {
		return nil, ErrExecutionEpochConfigs
	}
	paths := make(map[string]string, len(target.Files))
	if walkOverlayFiles(target.Files, func(original, transformed string, _, _ []byte, _ bool) error {
		paths[original] = transformed
		return ctx.Err()
	}) != nil {
		return nil, ErrExecutionEpochConfigs
	}
	base, err := transformCatalog(target.Catalog, paths)
	if err != nil {
		return nil, ErrExecutionEpochConfigs
	}
	inputs := make([]epochGeneratedInput, 0, 3)
	for index, revision := range []string{"a", "b", "a-return"} {
		catalog, err := logicalCatalogForRevision(base, revision)
		if err != nil || ctx.Err() != nil {
			return nil, ErrExecutionEpochConfigs
		}
		raw, err := json.Marshal(catalog)
		actual := CatalogSourceProfile{Schema: catalogSourceSchema, Bytes: uint64(len(raw)), SHA256: SHA256(raw),
			Records: uint64(len(catalog.Services) + len(catalog.Memberships) + len(catalog.Unowned))}
		if err != nil || len(raw) == 0 || len(raw) > maxEpochCatalogBytes || plan.Revisions.Logical[index].Name != revision ||
			plan.Revisions.Logical[index].CatalogSource != actual {
			return nil, ErrExecutionEpochConfigs
		}
		inputs = append(inputs, epochGeneratedInput{name: "catalog-" + revision, raw: raw})
	}
	return inputs, ctx.Err()
}

func epochConfigBytes(plan Plan, epoch ExecutionEpochConfig, source string) ([]byte, error) {
	logical := slices.Index([]string{"a", "b", "a-return"}, epoch.LogicalRevision)
	versions := []string{combinedAuthorityA, combinedAuthorityB, combinedAuthorityAReturn}
	if logical < 0 || epoch.Epoch < 1 || epoch.Epoch > 5 ||
		epoch.LogicalRevision != []string{"a", "b", "a-return", "a-return", "a-return"}[epoch.Epoch-1] {
		return nil, ErrExecutionEpochConfigs
	}
	raw, err := yaml.Marshal(map[string]any{
		"server":      map[string]string{"addr": epoch.Listen, "data_dir": epoch.DataRoot},
		"auth":        map[string]any{"api_key": epoch.APIKey, "cookie_secure": false},
		"sync":        map[string]string{"poll_interval": "250ms", "resync_interval": "0"},
		"lifecycle":   map[string]bool{"enabled": true},
		"diagnostics": map[string]bool{"jobs": true, "candidates": true, "extraction": true, "extractor_details": false},
		"experimental": map[string]bool{"provisional_proto_extraction": true, "provisional_thrift_extraction": true,
			"provisional_thrift_field_extraction": false, "provisional_kafka_extraction": true, "provisional_workbench": false},
		"connections": []map[string]any{{"name": "t422-local", "type": "git", "url": source, "watch": true}},
		"service_catalogs": map[string]config.ServiceCatalog{epoch.Repository: {
			Kind: servicecatalog.AuthorityOperator, ID: combinedAuthorityID, Version: versions[logical],
			Path: epoch.CatalogPath, Runtime: config.ServiceCatalogRuntimeV3}},
	})
	if err != nil || len(raw) > maxEpochConfigBytes || validateEpochConfigBytes(plan, epoch, source, raw) != nil {
		return nil, ErrExecutionEpochConfigs
	}
	return raw, nil
}

func validateEpochConfigBytes(plan Plan, epoch ExecutionEpochConfig, source string, raw []byte) error {
	if len(raw) == 0 || len(raw) > maxEpochConfigBytes {
		return ErrExecutionEpochConfigs
	}
	cfg, err := config.ParseLiteral(raw)
	if err != nil {
		return ErrExecutionEpochConfigs
	}
	want := frozenExecutionConfig(plan, "")
	var domains []string
	for _, domain := range plan.Profile.Pipeline.ExtractionDomains {
		domains = append(domains, domain.Domain)
	}
	logical := slices.Index([]string{"a", "b", "a-return"}, epoch.LogicalRevision)
	versions := []string{combinedAuthorityA, combinedAuthorityB, combinedAuthorityAReturn}
	actualRepository, sourceErr := phebssync.RepoName(source)
	host, port, listenErr := net.SplitHostPort(epoch.Listen)
	portNumber, portErr := strconv.Atoi(port)
	key, keyErr := hex.DecodeString(epoch.APIKey)
	if sourceErr != nil || actualRepository != epoch.Repository || listenErr != nil || host != "127.0.0.1" ||
		portErr != nil || portNumber < 1 || portNumber > 65535 || strconv.Itoa(portNumber) != port ||
		keyErr != nil || len(key) != 32 || hex.EncodeToString(key) != epoch.APIKey || [32]byte(key) == ([32]byte{}) {
		return ErrExecutionEpochConfigs
	}
	if logical < 0 || !slices.Equal(domains, want.EnabledExtractorDomains) ||
		cfg.Server.Addr != epoch.Listen || cfg.Server.DataDir != epoch.DataRoot || cfg.Auth.APIKey != epoch.APIKey || cfg.Auth.SecureCookies() ||
		!cfg.Auth.OIDC.IsZero() || !cfg.Auth.BootstrapUser.IsZero() || len(cfg.Auth.TrustedProxies) != 0 ||
		cfg.Sync.Interval() != time.Duration(want.SyncPollMilliseconds)*time.Millisecond || cfg.Sync.ResyncEvery() != 0 || cfg.Sync.CleanupOrphans ||
		cfg.Lifecycle.EnabledFor() != want.LifecycleEnabled ||
		cfg.Diagnostics != (config.Diagnostics{Jobs: want.DiagnosticsJobs, Candidates: want.DiagnosticsCandidates, Extraction: want.DiagnosticsExtraction, ExtractorDetails: want.DiagnosticsExtractorDetails}) ||
		cfg.Experimental != (config.Experimental{ProvisionalProtoExtraction: want.ProvisionalProtoExtraction,
			ProvisionalThriftExtraction: want.ProvisionalThriftExtraction, ProvisionalThriftFieldExtraction: want.ProvisionalThriftFieldExtraction,
			ProvisionalKafkaExtraction: want.ProvisionalKafkaExtraction, ProvisionalWorkbench: want.ProvisionalWorkbench}) ||
		len(cfg.Connections) != 1 || !reflect.DeepEqual(cfg.Connections[0], config.Connection{Name: "t422-local", Type: "git", URL: source, Watch: true}) ||
		len(cfg.ServiceCatalogs) != 1 || cfg.ServiceCatalogs[epoch.Repository] != (config.ServiceCatalog{
		Kind: servicecatalog.AuthorityOperator, ID: combinedAuthorityID, Version: versions[logical], Path: epoch.CatalogPath, Runtime: config.ServiceCatalogRuntimeV3}) ||
		len(cfg.AnalysisUnits) != 0 || len(cfg.Revisions) != 0 || len(cfg.Contexts) != 0 || cfg.Permissions != nil ||
		cfg.Webhook.Secret != "" || cfg.Indexing.Verbose || cfg.Auth.SessionLifetime != "" ||
		cfg.Audit != (config.Audit{}) || cfg.Analytics != (config.Analytics{}) || cfg.ProofBundles != (config.ProofBundles{}) {
		return ErrExecutionEpochConfigs
	}
	return nil
}

// protectInputs is only the fixed three-catalog/five-config staging seam. The
// existing flat custodian closes writers before protection and post-copy hash.
// Failed exact-stage removal is retained explicitly for the owning cleanup.
func (custody *ExecutionEpochConfigCustody) protectInputs(ctx context.Context, inputs []epochGeneratedInput) (_ *ExecutionInputCustody, retErr error) {
	if len(inputs) != 3 && len(inputs) != 5 {
		return nil, ErrExecutionEpochConfigs
	}
	type stage struct {
		path string
		info os.FileInfo
	}
	stages := make([]stage, 0, len(inputs))
	defer func() {
		for _, stage := range stages {
			current, err := os.Lstat(stage.path)
			if err != nil || stage.info == nil || !os.SameFile(stage.info, current) || os.Remove(stage.path) != nil {
				custody.stages = append(custody.stages, stage.path)
				retErr = ErrExecutionEpochConfigs
			}
		}
	}()
	copies := make([]ExecutionInputCopy, 0, len(inputs))
	for _, input := range inputs {
		limit := maxEpochConfigBytes
		if len(inputs) == 3 {
			limit = maxEpochCatalogBytes
		}
		if ctx.Err() != nil || len(input.raw) == 0 || len(input.raw) > limit {
			return nil, ErrExecutionEpochConfigs
		}
		writer, err := os.CreateTemp(custody.author.parent, ".t422-epoch-input-")
		if err != nil {
			return nil, ErrExecutionEpochConfigs
		}
		written, writeErr := writer.Write(input.raw)
		info, statErr := writer.Stat()
		closeErr := writer.Close()
		stages = append(stages, stage{path: writer.Name(), info: info})
		if writeErr != nil || statErr != nil || closeErr != nil || written != len(input.raw) {
			return nil, ErrExecutionEpochConfigs
		}
		copies = append(copies, ExecutionInputCopy{Name: input.name, Path: writer.Name(), SHA256: SHA256(input.raw)})
	}
	return ProtectExecutionInputs(ctx, custody.author.parent, copies)
}

func (custody *ExecutionEpochConfigCustody) checkLocked(ctx context.Context, epoch uint64) error {
	if ctx == nil || ctx.Err() != nil || custody.closed || custody.err != nil || epoch < 1 || epoch > 5 ||
		custody.catalogs == nil || custody.configs == nil || len(custody.roots) != 4 || len(custody.stages) != 0 || custody.author.check(ctx) != nil {
		return ErrExecutionEpochConfigs
	}
	for _, root := range custody.roots {
		if root.file == nil {
			return ErrExecutionEpochConfigs
		}
		held, statErr := root.file.Stat()
		current, pathErr := os.Lstat(root.path)
		volume, volumeErr := inputCustodyVolume(root.file)
		canonical, canonicalErr := filepath.EvalSymlinks(root.path)
		if statErr != nil || pathErr != nil || volumeErr != nil || canonicalErr != nil || canonical != root.path ||
			!os.SameFile(root.info, held) || !os.SameFile(held, current) || !current.IsDir() || !inputCustodyOwned(current) ||
			current.Mode().Perm() != 0o700 || volume != root.volume {
			return ErrExecutionEpochConfigs
		}
	}
	selected := custody.epochs[epoch-1]
	path, err := custody.configs.Check(ctx, "config-"+strconv.FormatUint(epoch, 10))
	catalog, catalogErr := custody.catalogs.Check(ctx, "catalog-"+selected.LogicalRevision)
	if err != nil || catalogErr != nil || path != selected.ConfigPath || catalog != selected.CatalogPath ||
		epoch > custody.released && (custody.listeners[epoch-1] == nil || custody.listeners[epoch-1].Addr().String() != selected.Listen) {
		return ErrExecutionEpochConfigs
	}
	return nil
}

func (custody *ExecutionEpochConfigCustody) Check(ctx context.Context, epoch uint64) (ExecutionEpochConfig, error) {
	if custody == nil || custody.author == nil {
		return ExecutionEpochConfig{}, ErrExecutionEpochConfigs
	}
	custody.author.mu.Lock()
	defer custody.author.mu.Unlock()
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if custody.checkLocked(ctx, epoch) != nil {
		custody.err = ErrExecutionEpochConfigs
		return ExecutionEpochConfig{}, custody.err
	}
	return custody.epochs[epoch-1], nil
}

// ReleaseListener is one-shot and ordered. Call immediately before the owning
// native Start. It does not claim the kernel reservation survives this close;
// that Start must fail honestly if another listener wins the intervening race.
func (custody *ExecutionEpochConfigCustody) ReleaseListener(ctx context.Context, epoch uint64) error {
	if custody == nil || custody.author == nil {
		return ErrExecutionEpochConfigs
	}
	custody.author.mu.Lock()
	defer custody.author.mu.Unlock()
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if epoch != custody.released+1 || custody.checkLocked(ctx, epoch) != nil {
		custody.err = ErrExecutionEpochConfigs
		return custody.err
	}
	if custody.listeners[epoch-1].Close() != nil {
		custody.err = ErrExecutionEpochConfigs
		return custody.err
	}
	custody.listeners[epoch-1] = nil
	custody.released++
	return nil
}

// RetainedPaths includes only newly owned roots/inputs and any failed stage
// cleanup, never the borrowed author's source or tools. It is not deletion
// authority; native retained identities and joined users remain mandatory.
func (custody *ExecutionEpochConfigCustody) RetainedPaths() []string {
	if custody == nil {
		return nil
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	paths := slices.Clone(custody.stages)
	for _, root := range custody.roots {
		paths = append(paths, root.path)
	}
	for _, inputs := range []*ExecutionInputCustody{custody.catalogs, custody.configs} {
		if inputs != nil {
			paths = append(paths, inputs.Directory())
		}
	}
	return paths
}

func (custody *ExecutionEpochConfigCustody) closeLocked() error {
	if custody.closed {
		return custody.err
	}
	custody.closed = true
	for index, listener := range custody.listeners {
		if listener != nil {
			if listener.Close() != nil {
				custody.err = ErrExecutionEpochConfigs
			}
			custody.listeners[index] = nil
		}
	}
	for _, root := range custody.roots {
		if root.file != nil && root.file.Close() != nil {
			custody.err = ErrExecutionEpochConfigs
		}
	}
	for _, inputs := range []*ExecutionInputCustody{custody.catalogs, custody.configs} {
		if inputs != nil && inputs.Close() != nil {
			custody.err = ErrExecutionEpochConfigs
		}
	}
	return custody.err
}

// Close releases only owned descriptors, never borrowed source/lease/tool
// custody or filesystem bytes. The caller must first join all input users.
func (custody *ExecutionEpochConfigCustody) Close() error {
	if custody == nil {
		return nil
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if custody.active {
		return ErrExecutionEpochConfigs
	}
	return custody.closeLocked()
}

var _ io.Closer = (*ExecutionEpochConfigCustody)(nil)

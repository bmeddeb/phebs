package t421

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"gopkg.in/yaml.v3"
)

var ErrExecutionProductionCustody = errors.New("execution production custody unavailable or changed")

// ExecutionProductionRequest borrows genuine protected tools and their exact
// measured reference-input issuer. All mutable directories are direct children
// of parent, are private and share its native volume. The parent alone owns
// source mutation; this slice does not admit an author handoff or full profile.
type ExecutionProductionRequest struct {
	Git                               *ExecutionGitCustody
	Builds                            *ExecutionGoBuildCustody
	Phebs, Zoekt, Surreal             *ExecutionToolCustody
	SourceRepository, SourceCommit    string
	DataRoot, Home, Temporary, Listen string
}

type productionRoot struct {
	path   string
	file   *os.File
	info   os.FileInfo
	volume [2]int32
}

type productionSourceControl struct {
	path string
	file *os.File
	info os.FileInfo
}

// ExecutionProductionCustody owns protected configuration, source-owner lease
// and root/control descriptors. Tool/build handles are borrowed and must outlive
// its joined run. It is not a twelve-tool, host/profile or freeze admission.
type ExecutionProductionCustody struct {
	mu          sync.Mutex
	parent      string
	request     ExecutionProductionRequest
	roots       []productionRoot
	controls    []productionSourceControl
	lease       *os.File
	leaseInfo   os.FileInfo
	config      *ExecutionInputCustody
	configPath  string
	apiKey      string
	tools       []dispatchadmission.ProductionToolBinding
	environment []string
	phebsPath   string
	active      bool
	used        bool
	closed      bool
	err         error
}

// PrepareExecutionProduction admits one ordinary watched local repository for a
// bounded serve rehearsal. It generates the config and every environment/argv
// element; no caller callback, verified bit, bootstrap record or command is used.
// The source tree is limited to 64 regular blobs and 1 MiB; its native bare
// object store is separately bounded and its controls are closed. Mutable source
// ownership is cooperative, not a hostile-same-user sandbox. The constructor
// has a separate five-minute outer context, including source/input checks.
func PrepareExecutionProduction(ctx context.Context, parent string, request ExecutionProductionRequest) (_ *ExecutionProductionCustody, retErr error) {
	if ctx == nil || ctx.Err() != nil || !executionGitPrivateDirectory(parent) || request.Git == nil || request.Builds == nil ||
		request.Phebs == nil || request.Zoekt == nil || request.Surreal == nil || !validCommit(request.SourceCommit) ||
		request.Phebs.referenceInputs != request.Builds || request.Zoekt.referenceInputs != request.Builds || request.Builds.git != request.Git {
		return nil, ErrExecutionProductionCustody
	}
	for _, directory := range []string{request.Git.Directory(), request.Builds.Directory(), request.Phebs.Directory(), request.Zoekt.Directory(), request.Surreal.Directory()} {
		if filepath.Dir(directory) != parent {
			return nil, ErrExecutionProductionCustody
		}
	}
	host, port, err := net.SplitHostPort(request.Listen)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || host != "127.0.0.1" || portNumber < 1 || portNumber > 65535 || strconv.Itoa(portNumber) != port {
		return nil, ErrExecutionProductionCustody
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if request.Builds.Check(ctx) != nil {
		return nil, ErrExecutionProductionCustody
	}
	custody := &ExecutionProductionCustody{parent: parent, request: request}
	defer func() {
		if retErr != nil {
			custody.err = ErrExecutionProductionCustody
			_ = custody.Close()
			retErr = ErrExecutionProductionCustody
		}
	}()
	paths := []string{parent, request.SourceRepository, request.DataRoot, request.Home, request.Temporary}
	for index, path := range paths {
		if !executionGitPrivateDirectory(path) || index > 0 && filepath.Dir(path) != parent {
			return custody, ErrExecutionProductionCustody
		}
		for _, earlier := range paths[:index] {
			if path == earlier {
				return custody, ErrExecutionProductionCustody
			}
		}
		root, err := openProductionRoot(path)
		if err != nil {
			return custody, err
		}
		custody.roots = append(custody.roots, root)
		if index > 0 && root.volume != custody.roots[0].volume {
			return custody, ErrExecutionProductionCustody
		}
		if index > 1 {
			rows, err := root.file.ReadDir(1)
			if err != nil && !errors.Is(err, io.EOF) || len(rows) != 0 {
				return custody, ErrExecutionProductionCustody
			}
		}
	}
	custody.lease, err = acquireProductionSourceLease(parent)
	if err != nil {
		return custody, err
	}
	custody.leaseInfo, err = custody.lease.Stat()
	if err != nil || custody.admitSource(ctx) != nil {
		return custody, ErrExecutionProductionCustody
	}
	gitIdentity, gitPath, err := request.Git.Check(ctx)
	if err != nil {
		return custody, ErrExecutionProductionCustody
	}
	phebsIdentity, phebsPath, err := request.Phebs.Check(ctx, "phebs")
	if err != nil || phebsIdentity.BuildVCSRevision != request.Builds.reference.source {
		return custody, ErrExecutionProductionCustody
	}
	zoektIdentity, zoektPath, err := request.Zoekt.Check(ctx, "zoekt-git-index")
	if err != nil {
		return custody, ErrExecutionProductionCustody
	}
	surrealIdentity, surrealPath, err := request.Surreal.Check(ctx, "surreal")
	if err != nil || gitIdentity.Role != "git" {
		return custody, ErrExecutionProductionCustody
	}
	gitEnvironment, err := request.Git.Environment(ctx, request.Home, request.Temporary)
	if err != nil {
		return custody, ErrExecutionProductionCustody
	}
	environment := externalToolEnvironment(request.Temporary)
	for index, value := range environment {
		if strings.HasPrefix(value, "HOME=") {
			environment[index] = "HOME=" + request.Home
		} else if strings.HasPrefix(value, "PATH=") {
			environment[index] = "PATH=" + request.Git.Directory()
		}
	}
	custody.tools = []dispatchadmission.ProductionToolBinding{
		{Role: "git", Path: gitPath, Environment: gitEnvironment},
		{Role: "surreal", Path: surrealPath, Environment: environment},
		{Role: "zoekt-git-index", Path: zoektPath, Environment: gitEnvironment},
	}
	custody.environment = append(append([]string(nil), environment...), dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionSelector,
		"PHEBS_SURREAL="+surrealPath, "PHEBS_SURREAL_SHA256="+surrealIdentity.SHA256,
		"PHEBS_ZOEKT_GIT_INDEX="+zoektPath, "PHEBS_ZOEKT_GIT_INDEX_SHA256="+zoektIdentity.SHA256)
	custody.phebsPath = phebsPath
	key := [32]byte{}
	if _, err := rand.Read(key[:]); err != nil {
		return custody, ErrExecutionProductionCustody
	}
	custody.apiKey = hex.EncodeToString(key[:])
	raw, err := yaml.Marshal(map[string]any{
		"server":      map[string]string{"addr": request.Listen, "data_dir": request.DataRoot},
		"auth":        map[string]string{"api_key": custody.apiKey},
		"sync":        map[string]string{"resync_interval": "0"},
		"connections": []map[string]any{{"name": "t422-local", "type": "git", "url": request.SourceRepository, "watch": true}},
	})
	if err != nil || len(raw) > 16<<10 {
		return custody, ErrExecutionProductionCustody
	}
	parsed, err := config.Parse(raw)
	if err != nil || parsed.Server.Addr != request.Listen || parsed.Server.DataDir != request.DataRoot || len(parsed.Connections) != 1 ||
		parsed.Connections[0].URL != request.SourceRepository || !parsed.Connections[0].Watch {
		return custody, ErrExecutionProductionCustody
	}
	custody.config, err = protectProductionConfig(ctx, parent, raw)
	if err != nil {
		return custody, err
	}
	custody.configPath, err = custody.config.Check(ctx, "config")
	if err != nil || custody.check(ctx, dispatchadmission.Site{}) != nil {
		return custody, ErrExecutionProductionCustody
	}
	return custody, nil
}

func openProductionRoot(path string) (productionRoot, error) {
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return productionRoot{}, ErrExecutionProductionCustody
	}
	info, statErr := file.Stat()
	volume, volumeErr := inputCustodyVolume(file)
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || volumeErr != nil || !inputCustodyOwned(info) || !info.IsDir() ||
		info.Mode().Perm() != 0o700 || !inputCustodySame(info, current) {
		_ = file.Close()
		return productionRoot{}, ErrExecutionProductionCustody
	}
	return productionRoot{path: path, file: file, info: info, volume: volume}, nil
}

func (custody *ExecutionProductionCustody) check(ctx context.Context, site dispatchadmission.Site) error {
	if ctx == nil || ctx.Err() != nil || custody.closed || custody.err != nil || custody.config == nil {
		return ErrExecutionProductionCustody
	}
	for _, root := range custody.roots {
		held, err := root.file.Stat()
		current, pathErr := os.Lstat(root.path)
		volume, volumeErr := inputCustodyVolume(root.file)
		canonical, canonicalErr := filepath.EvalSymlinks(root.path)
		if err != nil || pathErr != nil || volumeErr != nil || canonicalErr != nil || canonical != root.path ||
			!os.SameFile(root.info, held) || !os.SameFile(held, current) || !inputCustodyOwned(current) ||
			current.Mode().Perm() != 0o700 || volume != root.volume {
			return ErrExecutionProductionCustody
		}
	}
	for _, control := range custody.controls {
		current, err := os.Lstat(control.path)
		held, statErr := control.file.Stat()
		if err != nil || statErr != nil || !inputCustodySame(control.info, current) || !inputCustodySame(current, held) {
			return ErrExecutionProductionCustody
		}
	}
	lease, err := custody.lease.Stat()
	currentLease, pathErr := os.Lstat(filepath.Join(custody.parent, productionSourceLeaseName))
	if err != nil || pathErr != nil || !inputCustodySame(custody.leaseInfo, lease) || !inputCustodySame(lease, currentLease) {
		return ErrExecutionProductionCustody
	}
	if path, err := custody.config.Check(ctx, "config"); err != nil || path != custody.configPath {
		return ErrExecutionProductionCustody
	}
	if _, path, err := custody.request.Phebs.Check(ctx, "phebs"); err != nil || path != custody.phebsPath {
		return ErrExecutionProductionCustody
	}
	switch site.Role {
	case 0, dispatchadmission.RoleGit, dispatchadmission.RoleZoekt:
		if _, _, err := custody.request.Git.Check(ctx); err != nil {
			return ErrExecutionProductionCustody
		}
	case dispatchadmission.RoleSurreal:
	default:
		return ErrExecutionProductionCustody
	}
	if site.Role == 0 || site.Role == dispatchadmission.RoleZoekt {
		if _, _, err := custody.request.Zoekt.Check(ctx, "zoekt-git-index"); err != nil {
			return ErrExecutionProductionCustody
		}
	}
	if site.Role == 0 || site.Role == dispatchadmission.RoleSurreal {
		if _, _, err := custody.request.Surreal.Check(ctx, "surreal"); err != nil {
			return ErrExecutionProductionCustody
		}
	}
	return nil
}

// Directory identifies retained protected configuration, never public evidence.
func (custody *ExecutionProductionCustody) Directory() string {
	if custody == nil || custody.config == nil {
		return ""
	}
	return custody.config.Directory()
}

// Close releases owned descriptors only, never borrowed tools or mutable data.
// An active/unjoined run refuses Close and retains every input/lease descriptor.
func (custody *ExecutionProductionCustody) Close() error {
	if custody == nil {
		return nil
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if custody.active {
		return ErrExecutionProductionCustody
	}
	if !custody.closed {
		custody.closed = true
		if custody.config != nil && custody.config.Close() != nil {
			custody.err = ErrExecutionProductionCustody
		}
		for _, root := range custody.roots {
			if root.file.Close() != nil {
				custody.err = ErrExecutionProductionCustody
			}
		}
		for _, control := range custody.controls {
			if control.file.Close() != nil {
				custody.err = ErrExecutionProductionCustody
			}
		}
		if custody.lease != nil && custody.lease.Close() != nil {
			custody.err = ErrExecutionProductionCustody
		}
	}
	return custody.err
}

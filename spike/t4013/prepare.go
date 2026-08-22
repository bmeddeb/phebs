package t4013

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t401"
	"gopkg.in/yaml.v3"
)

const (
	PreparedSchema           = "t4013-private-prepared-custody-v1"
	PrepareConfirm           = "prepare-neutral-t4013-custody"
	CleanupConfirm           = "cleanup-neutral-t4013-custody"
	custodyRemoveAttempts    = 11
	custodyRemoveRetryDelay  = 100 * time.Millisecond
	custodyRemoveSettleDelay = 250 * time.Millisecond
)

type PrepareRequest struct {
	ModuleRoot string
	Workspace  string
	PlanPath   string
	Confirm    string
	BasePort   int
}

type Prepared struct {
	Schema     string            `json:"schema"`
	PlanDigest string            `json:"plan_digest"`
	Profiles   []PreparedProfile `json:"profiles"`
}

type PreparedProfile struct {
	Name           string            `json:"name"`
	Repository     string            `json:"repository"`
	RepositoryName string            `json:"repository_name"`
	Config         string            `json:"config"`
	Credential     string            `json:"credential"`
	DataDir        string            `json:"data_dir"`
	Address        string            `json:"address"`
	Catalog        string            `json:"catalog"`
	Revisions      map[string]string `json:"revisions"`
}

func Prepare(ctx context.Context, request PrepareRequest) (result Prepared, retErr error) {
	if ctx == nil {
		return Prepared{}, errors.New("T40.13 prepare requires a context")
	}
	if request.Confirm != PrepareConfirm || request.BasePort < 1024 || request.BasePort > 65533 {
		return Prepared{}, errors.New("T40.13 prepare confirmation or base port is invalid")
	}
	moduleRoot, err := filepath.EvalSymlinks(request.ModuleRoot)
	if err != nil || !filepath.IsAbs(moduleRoot) {
		return Prepared{}, errors.New("T40.13 module root must be an absolute real path")
	}
	workspaceParent, err := filepath.EvalSymlinks(filepath.Dir(request.Workspace))
	if err != nil || !filepath.IsAbs(request.Workspace) {
		return Prepared{}, errors.New("T40.13 workspace parent must be an absolute real path")
	}
	workspace := filepath.Join(workspaceParent, filepath.Base(request.Workspace))
	if workspace == moduleRoot || isWithin(workspace, moduleRoot) || isWithin(moduleRoot, workspace) {
		return Prepared{}, errors.New("T40.13 workspace must be outside and must not contain the module root")
	}
	if _, err := os.Lstat(workspace); err == nil || !os.IsNotExist(err) {
		return Prepared{}, errors.New("T40.13 workspace must not already exist")
	}
	planBytes, err := os.ReadFile(request.PlanPath)
	if err != nil {
		return Prepared{}, fmt.Errorf("read T40.13 plan: %w", err)
	}
	plan, err := DecodePlan(planBytes)
	if err != nil {
		return Prepared{}, err
	}
	if planSchemaVersion(plan.Schema) >= 2 {
		if err := VerifyHostToolchain(ctx, plan.HostToolchain); err != nil {
			return Prepared{}, fmt.Errorf("verify frozen host toolchain before custody: %w", err)
		}
	}
	if err := VerifyInputs(moduleRoot); err != nil {
		return Prepared{}, err
	}
	if err := verifyCleanCheckout(ctx, moduleRoot, plan.SourceCommit); err != nil {
		return Prepared{}, err
	}
	if _, err := HostPreflight(ctx, workspaceParent, plan); err != nil {
		return Prepared{}, err
	}
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("create T40.13 workspace: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			retErr = errors.Join(retErr, destroyCustody(workspace, moduleRoot))
		}
	}()
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		return Prepared{}, err
	}
	prepared := Prepared{Schema: PreparedSchema, PlanDigest: PlanDigest(planBytes), Profiles: []PreparedProfile{}}
	for index, profile := range profiles {
		profileRoot := filepath.Join(workspace, profile.Kind)
		if err := os.Mkdir(profileRoot, 0o700); err != nil {
			return Prepared{}, err
		}
		authored := filepath.Join(profileRoot, "authored")
		receipt, err := t401.Author(ctx, t401.AuthorRequest{
			ModuleRoot: moduleRoot, Output: authored, Profile: profile, ConfirmFrozen: true,
		})
		if err != nil {
			return Prepared{}, fmt.Errorf("author %s: %w", profile.Name, err)
		}
		repository := filepath.Join(authored, "repository.git")
		revisions := make(map[string]string, len(receipt.Revisions))
		for _, revision := range receipt.Revisions {
			revisions[revision.Revision] = revision.Commit
		}
		if err := updateSourceRevision(ctx, repository, revisions["a"]); err != nil {
			return Prepared{}, err
		}
		repositoryName, err := phebssync.RepoName(repository)
		if err != nil {
			return Prepared{}, err
		}
		catalogPath := filepath.Join(profileRoot, "service-catalog.json")
		catalogBytes, err := catalogFor(profile.Kind)
		if err != nil {
			return Prepared{}, err
		}
		if err := writePrivateNew(catalogPath, catalogBytes); err != nil {
			return Prepared{}, err
		}
		credential, err := randomCredential()
		if err != nil {
			return Prepared{}, err
		}
		credentialPath := filepath.Join(profileRoot, "api-key")
		if err := writePrivateNew(credentialPath, []byte(credential+"\n")); err != nil {
			return Prepared{}, err
		}
		dataDir := filepath.Join(profileRoot, "data")
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			return Prepared{}, err
		}
		address := fmt.Sprintf("127.0.0.1:%d", request.BasePort+index)
		configPath := filepath.Join(profileRoot, "phebs.yaml")
		configBytes, err := configFor(repository, repositoryName, catalogPath, dataDir, address, credential)
		if err != nil {
			return Prepared{}, err
		}
		if err := writePrivateNew(configPath, configBytes); err != nil {
			return Prepared{}, err
		}
		prepared.Profiles = append(prepared.Profiles, PreparedProfile{
			Name: profile.Name, Repository: repository, RepositoryName: repositoryName,
			Config: configPath, Credential: credentialPath, DataDir: dataDir,
			Address: address, Catalog: catalogPath, Revisions: revisions,
		})
	}
	if planSchemaVersion(plan.Schema) >= 2 {
		if err := VerifyHostToolchain(ctx, plan.HostToolchain); err != nil {
			return Prepared{}, fmt.Errorf("verify frozen host toolchain after custody authoring: %w", err)
		}
	}
	completed = true
	return prepared, nil
}

func destroyCustody(workspace, moduleRoot string) error {
	return destroyCustodyWith(workspace, moduleRoot, os.RemoveAll, time.Sleep)
}

func destroyCustodyWith(
	workspace, moduleRoot string,
	removeAll func(string) error,
	wait func(time.Duration),
) error {
	if !filepath.IsAbs(workspace) || !filepath.IsAbs(moduleRoot) || workspace == moduleRoot ||
		isWithin(moduleRoot, workspace) || isWithin(workspace, moduleRoot) ||
		removeAll == nil || wait == nil {
		return errors.New("T40.13 custody cleanup scope is invalid")
	}
	for attempt := 0; attempt < custodyRemoveAttempts; attempt++ {
		info, err := os.Lstat(workspace)
		if errors.Is(err, os.ErrNotExist) {
			if attempt == 0 {
				return nil
			}
			wait(custodyRemoveSettleDelay)
			if _, settleErr := os.Lstat(workspace); errors.Is(settleErr, os.ErrNotExist) {
				return nil
			} else if settleErr != nil {
				return errors.New("T40.13 custody cleanup absence check failed")
			}
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("T40.13 custody cleanup target is invalid")
		}
		removeErr := removeAll(workspace)
		if removeErr != nil && !errors.Is(removeErr, syscall.ENOTEMPTY) &&
			!errors.Is(removeErr, syscall.EEXIST) {
			return removeErr
		}
		if attempt+1 < custodyRemoveAttempts {
			wait(custodyRemoveRetryDelay)
		}
	}
	return errors.New("T40.13 custody cleanup did not settle within its retry bound")
}

func HostPreflight(ctx context.Context, dataParent string, plan Plan) (EnvironmentObservation, error) {
	return hostPreflight(ctx, dataParent, 0, plan)
}

func hostPreflight(
	ctx context.Context,
	dataParent string,
	workspaceAllocated int64,
	plan Plan,
) (EnvironmentObservation, error) {
	if ctx == nil || !filepath.IsAbs(dataParent) {
		return EnvironmentObservation{}, errors.New("T40.13 host preflight scope is invalid")
	}
	memory, err := physicalMemory(ctx)
	if err != nil {
		return EnvironmentObservation{}, err
	}
	capacity, err := lifecycle.ProbeCapacity(ctx, dataParent)
	if err != nil {
		return EnvironmentObservation{}, fmt.Errorf("probe T40.13 custody capacity: %w", err)
	}
	if memory < plan.Safety.MinimumMemoryBytes || capacity.AvailableBytes < plan.Safety.MinimumAvailableDiskBytes {
		return EnvironmentObservation{}, errors.New("T40.13 frozen host prerequisite is not met")
	}
	usedPercent := int((capacity.UsedBytes * 100) / capacity.TotalBytes)
	if planSchemaVersion(plan.Schema) >= 10 {
		observedCapacity, capacityErr := lifecycle.NewGate(dataParent).Check(ctx, 0)
		if capacityErr != nil {
			return EnvironmentObservation{}, fmt.Errorf("gate T40.13 custody capacity: %w", capacityErr)
		}
		pressureErr := validatePressureHostPreflight(observedCapacity, plan.Safety)
		if planSchemaVersion(plan.Schema) >= 23 {
			pressureErr = validatePressureHostPreflightV23(
				observedCapacity, workspaceAllocated, plan.Safety,
			)
		}
		if planSchemaVersion(plan.Schema) >= 25 {
			pressureErr = validatePressureHostPreflightV25(
				observedCapacity, workspaceAllocated, plan.Safety,
			)
		}
		if pressureErr != nil {
			return EnvironmentObservation{}, errors.New("T40.13 frozen pressure host prerequisite is not met")
		}
		usedPercent = observedCapacity.UsedPercent
	}
	return EnvironmentObservation{
		OS: runtime.GOOS, Arch: runtime.GOARCH, MemoryBytes: memory,
		FilesystemTotalBytes: capacity.TotalBytes, FilesystemAvailableBytes: capacity.AvailableBytes,
		InitialUsedPercent: usedPercent,
	}, nil
}

func physicalMemory(ctx context.Context) (int64, error) {
	switch runtime.GOOS {
	case "darwin":
		command := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize")
		output, err := command.Output()
		if err != nil {
			return 0, errors.New("T40.13 physical memory is unavailable")
		}
		value, err := strconv.ParseInt(string(bytesTrimSpace(output)), 10, 64)
		if err != nil || value <= 0 {
			return 0, errors.New("T40.13 physical memory is invalid")
		}
		return value, nil
	case "linux":
		raw, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0, errors.New("T40.13 physical memory is unavailable")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
				value, parseErr := strconv.ParseInt(fields[1], 10, 64)
				if parseErr == nil && value > 0 && value <= (1<<63-1)/1024 {
					return value * 1024, nil
				}
			}
		}
		return 0, errors.New("T40.13 physical memory is invalid")
	default:
		return 0, errors.New("T40.13 physical memory probe is unsupported")
	}
}

func MarshalPrepared(value Prepared) ([]byte, error) {
	if err := validatePrepared(value); err != nil {
		return nil, errors.New("T40.13 prepared custody is invalid")
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func DecodePrepared(raw []byte, planDigest string) (Prepared, error) {
	if len(raw) == 0 || len(raw) > MaxObservationBytes {
		return Prepared{}, errors.New("T40.13 prepared custody is outside its fixed byte bound")
	}
	var value Prepared
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Prepared{}, errors.New("T40.13 prepared custody cannot be decoded")
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Prepared{}, errors.New("T40.13 prepared custody has trailing data")
	}
	if value.PlanDigest != planDigest || validatePrepared(value) != nil {
		return Prepared{}, errors.New("T40.13 prepared custody is invalid")
	}
	return value, nil
}

func DestroyPrepared(value Prepared, moduleRoot string) error {
	if validatePrepared(value) != nil || !filepath.IsAbs(moduleRoot) {
		return errors.New("T40.13 prepared cleanup request is invalid")
	}
	workspace := filepath.Dir(filepath.Dir(value.Profiles[0].Config))
	if filepath.Dir(filepath.Dir(value.Profiles[1].Config)) != workspace {
		return errors.New("T40.13 prepared cleanup custody differs")
	}
	for _, profile := range value.Profiles {
		for _, path := range []string{profile.Repository, profile.Config, profile.Credential, profile.DataDir, profile.Catalog} {
			if !isWithin(path, workspace) {
				return errors.New("T40.13 prepared cleanup path escaped custody")
			}
		}
	}
	return destroyCustody(workspace, moduleRoot)
}

// CleanupPrepared removes the exact custody named by a plan-bound prepared
// manifest, then removes the manifest itself. It is safe to call after Execute
// has already removed the custody directory.
func CleanupPrepared(moduleRoot, planPath, preparedPath, confirm string) error {
	if confirm != CleanupConfirm || !filepath.IsAbs(moduleRoot) || !filepath.IsAbs(planPath) ||
		!filepath.IsAbs(preparedPath) {
		return errors.New("T40.13 prepared cleanup request is invalid")
	}
	realModuleRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		return errors.New("T40.13 prepared cleanup module root is invalid")
	}
	for _, path := range []string{planPath, preparedPath} {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("T40.13 prepared cleanup control is invalid")
		}
	}
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read T40.13 cleanup plan: %w", err)
	}
	if _, err := DecodePlan(planBytes); err != nil {
		return err
	}
	preparedBytes, err := os.ReadFile(preparedPath)
	if err != nil {
		return fmt.Errorf("read T40.13 cleanup custody: %w", err)
	}
	prepared, err := DecodePrepared(preparedBytes, PlanDigest(planBytes))
	if err != nil {
		return err
	}
	workspace := filepath.Dir(filepath.Dir(prepared.Profiles[0].Config))
	if planPath == workspace || preparedPath == workspace || isWithin(planPath, workspace) ||
		isWithin(preparedPath, workspace) {
		return errors.New("T40.13 prepared cleanup controls must remain outside custody")
	}
	if err := DestroyPrepared(prepared, realModuleRoot); err != nil {
		return err
	}
	if err := os.Remove(preparedPath); err != nil {
		return fmt.Errorf("remove T40.13 prepared manifest: %w", err)
	}
	return nil
}

func validatePrepared(value Prepared) error {
	if value.Schema != PreparedSchema || !digestIdentity(value.PlanDigest) || len(value.Profiles) != 2 ||
		value.Profiles[0].Name != "structural-2m-v1" || value.Profiles[1].Name != "semantic-262144-v1" {
		return errors.New("invalid prepared identity")
	}
	seenAddresses := map[string]bool{}
	for _, profile := range value.Profiles {
		if reponame.Validate(profile.RepositoryName) != nil || !loopbackAddress(profile.Address) || seenAddresses[profile.Address] ||
			!absolutePrivatePaths(profile) || len(profile.Revisions) != 3 ||
			!hexIdentity(profile.Revisions["a"], 40) || !hexIdentity(profile.Revisions["b"], 40) ||
			!hexIdentity(profile.Revisions["a-return"], 40) {
			return errors.New("invalid prepared profile")
		}
		seenAddresses[profile.Address] = true
	}
	return nil
}

func absolutePrivatePaths(profile PreparedProfile) bool {
	paths := []string{profile.Repository, profile.Config, profile.Credential, profile.DataDir, profile.Catalog}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
			return false
		}
	}
	return true
}

func loopbackAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1024 && number <= 65535
}

func catalogFor(kind string) ([]byte, error) {
	return catalogForShape(kind, 10_000)
}

func catalogForShape(kind string, structuralCells uint64) ([]byte, error) {
	catalog := servicecatalog.Catalog{
		Schema:    servicecatalog.Schema,
		Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "t4013-neutral", Version: "v1"},
		Services:  []servicecatalog.Service{}, Memberships: []servicecatalog.Membership{},
		Unowned: []servicecatalog.UnownedPlacement{
			{Path: ".phebs", Origin: servicecatalog.OriginBase},
			{Path: "go.mod", Origin: servicecatalog.OriginBase},
		},
	}
	switch kind {
	case "structural":
		if structuralCells == 0 || structuralCells > 10_000 {
			return nil, errors.New("T40.13 structural catalog shape is invalid")
		}
		serviceCount := min(100, int(structuralCells))
		for ordinal := 0; ordinal < serviceCount; ordinal++ {
			key := fmt.Sprintf("service-%03d", ordinal)
			catalog.Services = append(catalog.Services, servicecatalog.Service{
				Key: key, DisplayName: fmt.Sprintf("Neutral service %03d", ordinal),
				Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
			})
			catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
				ServiceKey: key, Path: fmt.Sprintf("structural/cells/b000/c%05d", ordinal),
				Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
			})
		}
		bucketCount := (int(structuralCells) + 99) / 100
		for bucket := 1; bucket < bucketCount; bucket++ {
			catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
				Path: fmt.Sprintf("structural/cells/b%03d", bucket), Origin: servicecatalog.OriginBase,
			})
		}
	case "semantic":
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: "semantic", DisplayName: "Neutral semantic service",
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		})
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
			ServiceKey: "semantic", Path: "semantic", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	default:
		return nil, errors.New("T40.13 profile kind is invalid")
	}
	return servicecatalog.Canonical(catalog)
}

func configFor(repository, repositoryName, catalogPath, dataDir, address, credential string) ([]byte, error) {
	secure := false
	enabled := true
	value := config.Config{
		Server:      config.Server{Addr: address, DataDir: dataDir},
		Auth:        config.Auth{APIKey: credential, CookieSecure: &secure},
		Sync:        config.Sync{PollInterval: "250ms", ResyncInterval: "0"},
		Diagnostics: config.Diagnostics{Jobs: true, Candidates: true, Extraction: true},
		Lifecycle:   config.Lifecycle{Enabled: &enabled},
		Experimental: config.Experimental{
			ProvisionalProtoExtraction: true, ProvisionalThriftExtraction: true,
			ProvisionalKafkaExtraction: true,
		},
		ServiceCatalogs: map[string]config.ServiceCatalog{
			repositoryName: {Kind: servicecatalog.AuthorityOperator, ID: "t4013-neutral", Version: "v1", Path: catalogPath},
		},
		Connections: []config.Connection{{Name: "t4013", Type: "git", URL: repository, Watch: true}},
	}
	return yaml.Marshal(value)
}

func updateSourceRevision(ctx context.Context, repository, commit string) error {
	if !hexIdentity(commit, 40) {
		return errors.New("T40.13 source revision is invalid")
	}
	if _, err := gitOutput(ctx, repository, "update-ref", "refs/heads/main", commit); err != nil {
		return err
	}
	return nil
}

func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	command.Env = gitEnvironment()
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("T40.13 git command failed")
	}
	return string(bytesTrimSpace(output)), nil
}

func verifyCleanCheckout(ctx context.Context, moduleRoot, sourceCommit string) error {
	if ctx == nil || !filepath.IsAbs(moduleRoot) || !hexIdentity(sourceCommit, 40) {
		return errors.New("T40.13 checkout identity is invalid")
	}
	commit, err := gitOutput(ctx, moduleRoot, "rev-parse", "HEAD")
	if err != nil || commit != sourceCommit {
		return errors.New("T40.13 checkout differs from the frozen source commit")
	}
	status, err := gitOutput(ctx, moduleRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status != "" {
		return errors.New("T40.13 checkout has modified or untracked files")
	}
	return nil
}

func gitEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			environment = append(environment, entry)
		}
	}
	return append(environment,
		"GIT_NO_LAZY_FETCH=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
	)
}

func randomCredential() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func writePrivateNew(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func isWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}

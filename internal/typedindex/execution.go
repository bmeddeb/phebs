package typedindex

import (
	"context"
	"path"
	"runtime"
)

// ResolvedRC is the only optional operator-copied rc in v1. Imports, recursive
// configs, custom commands, credentials and extra settings are never accepted.
const ResolvedRC = "build --compilation_mode=fastbuild\n"
const DriverPath = "/inputs/tools/bin/phebs-gopackagesdriver"
const LauncherPath = "/inputs/bin/phebs-typed-launcher"
const IndexerPath = "/inputs/tools/bin/scip-go"

// Commands are owned recipes, not operator/browser argv. The executor substitutes
// only exact plan-derived selectors, never an arbitrary shell string.
type Commands struct {
	Driver           string
	Launcher         string
	Indexer          string
	BazelStartup     []string
	BazelBuild       []string
	IndexerArguments []string
	Environment      []string
}

func (p Profile) Commands() (Commands, error) {
	if p.digest == "" {
		return Commands{}, Invalid
	}
	if p.definition.Config.GOARCH != runtime.GOARCH {
		return Commands{}, Unsupported
	}
	rc := "/dev/null"
	if p.definition.RCDigest != "" {
		rc = "/inputs/profile.bazelrc"
	}
	return Commands{
		Driver: DriverPath, Launcher: LauncherPath, Indexer: IndexerPath,
		BazelStartup:     []string{"--nosystem_rc", "--nohome_rc", "--noworkspace_rc", "--bazelrc=" + rc, "--output_user_root=/scratch/bazel-user", "--output_base=/scratch/bazel-output"},
		BazelBuild:       []string{"--repository_disable_download", "--repository_cache=/scratch/cache/repository", "--disk_cache=/scratch/cache/action", "--remote_cache=", "--remote_executor=", "--compilation_mode=fastbuild"},
		IndexerArguments: []string{"index", "--skip-tests", "--skip-implementations"},
		Environment:      []string{"HOME=/scratch/home", "GOOS=linux", "GOARCH=" + p.definition.Config.GOARCH, "PATH=/inputs/tools/go/bin:/inputs/tools/bin:/usr/bin:/bin", "TMPDIR=/scratch/tmp", "GOCACHE=/scratch/cache/go-build", "GOMODCACHE=/inputs/tools/modcache", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off", "GOENV=off", "GOWORK=off", "GOPACKAGESDRIVER=" + DriverPath},
	}, nil
}
func (p Profile) VerifyRC(ctx context.Context, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.digest == "" {
		return Invalid
	}
	if p.definition.RCDigest == "" {
		if len(raw) != 0 {
			return Invalid
		}
		return nil
	}
	if len(raw) != len(ResolvedRC) || string(raw) != ResolvedRC || hash(raw) != p.definition.RCDigest {
		return Invalid
	}
	return nil
}

// Custody consists only of data-directory-relative paths derived from an exact
// admitted request. Lifecycle owns every entry, including failure and resume.
type Custody struct {
	Owner     string
	Root      string
	Inputs    string
	Workspace string
	Scratch   string
	Cache     string
	Server    string
	Workers   string
	Output    string
}

func (a Admission) Custody() (Custody, error) {
	if !digest(a.digest) {
		return Custody{}, Invalid
	}
	root := "typed-index/attempts/" + a.digest[7:]
	return Custody{a.digest, root, path.Join(root, "inputs"), path.Join(root, "workspace"), path.Join(root, "scratch"), path.Join(root, "scratch/cache"), path.Join(root, "scratch/server"), path.Join(root, "scratch/workers"), path.Join(root, "scratch/output")}, nil
}

// Preparation is trusted executor evidence, NOT a request field or a client
// attestation. T45.4 must obtain these facts from verified private copies and
// sandbox observations, before both Bazel planning and member execution.
// This predicate alone never grants host execution.
type Preparation struct {
	ObservedPolicy    Policy
	ToolsDigest       string
	ImageDigest       string
	RCDigest          string
	RequestDigest     string
	BundleDigest      string
	InventoryFiles    int
	InventoryBytes    int64
	PrivateInputs     string
	CopiedAndVerified bool
	ImmutableInputs   bool
	ToolsVerified     bool
	RCVerified        bool
	DirectIO          bool
	NetworkDenied     bool
	PrivateScratch    bool
	CapacityReserved  bool
}

func (a Admission) ValidatePreparation(ctx context.Context, inventory Inventory, evidence Preparation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	custody, err := a.Custody()
	if err != nil {
		return err
	}
	if inventory.digest == "" || inventory.digest != a.request.BundleDigest {
		return Unprepared
	}
	if evidence.ObservedPolicy != a.profile.definition.Policy || evidence.ToolsDigest != a.profile.toolsDigest || evidence.ImageDigest != a.profile.definition.ImageDigest || evidence.RCDigest != a.profile.definition.RCDigest {
		return Unprepared
	}
	if !evidence.CapacityReserved {
		return Capacity
	}
	if evidence.RequestDigest != a.digest || evidence.BundleDigest != inventory.digest || evidence.InventoryFiles != len(inventory.files) || evidence.InventoryBytes != inventory.bytes || evidence.PrivateInputs != custody.Inputs || !evidence.CopiedAndVerified || !evidence.ImmutableInputs || !evidence.ToolsVerified || !evidence.RCVerified || !evidence.DirectIO || !evidence.NetworkDenied || !evidence.PrivateScratch {
		return Unprepared
	}
	return nil
}

// Capabilities describe this contract, not an installed executor/provider.
type Capabilities struct {
	Provider           string `json:"provider"`
	PlanningContract   bool   `json:"planning_contract"`
	ExecutionAvailable bool   `json:"execution_available"`
	Tests              bool   `json:"tests"`
	Implementations    bool   `json:"implementations"`
	GeneratedDocuments bool   `json:"generated_documents"`
}

func Describe() Capabilities { return Capabilities{Provider: ProviderID, PlanningContract: true} }

type Stage string

const (
	StagePreflight   Stage = "preflight"
	StagePlanning    Stage = "planning"
	StageExecution   Stage = "execution"
	StageValidation  Stage = "validation"
	StagePublication Stage = "publication"
)

type Progress struct {
	Stage         Stage   `json:"stage"`
	State         string  `json:"state"`
	Reason        Refusal `json:"reason"`
	ElapsedMillis int64   `json:"elapsed_millis"`
	OutputBytes   int64   `json:"output_bytes"`
}

func DecodeProgress(ctx context.Context, raw []byte) (Progress, error) {
	if err := ctx.Err(); err != nil {
		return Progress{}, err
	}
	var p Progress
	if err := decode(raw, 512, &p); err != nil {
		return Progress{}, err
	}
	switch p.Stage {
	case StagePreflight, StagePlanning, StageExecution, StageValidation, StagePublication:
	default:
		return Progress{}, Invalid
	}
	if p.ElapsedMillis < 0 || p.OutputBytes < 0 || p.OutputBytes > MeasuredPolicy().OutputBytes {
		return Progress{}, Invalid
	}
	switch p.State {
	case "pending", "running", "complete":
		if p.Reason != "" {
			return Progress{}, Invalid
		}
	case "refused":
		switch p.Reason {
		case Disabled, Forbidden, Invalid, Unsupported, Stale, Capacity, Unprepared, WallLimit, ExecutionFailed, Containment, Canceled:
		default:
			return Progress{}, Invalid
		}
	default:
		return Progress{}, Invalid
	}
	return p, nil
}

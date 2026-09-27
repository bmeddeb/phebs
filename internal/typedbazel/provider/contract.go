// Package provider implements the unregistered, closed Bazel worker. Its inputs
// are trusted controller admissions; its output is evidence, not publication.
package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

const (
	SelectionFile     = "typed-bazel-selection.json"
	SelectionSchema   = "phebs-bazel-selection-v1"
	MaxSelectionBytes = typedindex.MaxPlanBytes
	NativeProbePath   = "/inputs/tools/bin/t451b-native-probe"
	NativeAdapterPath = "/inputs/tools/bin/phebs-t451b-native-driver"
	SCIPPath          = "/inputs/tools/bin/scip-go"
	GoDigest          = "sha256:4b4667aec6954798f54de64a96addb757ecf4472e566ed397ef36ff01464c389"
	SCIPDigest        = "sha256:7d162fc544b6669fc8470c59480b754ea24339dfb6f27791e2f66346146ba765"
	BazelDigest       = "sha256:cab23c59d3d39c5e5382f12cd116b47445afdff9813516c18ae3ee8836b3037f"
	nativeSDKRoot     = "external/rules_go++go_sdk+go_default_sdk"
	maxClientBytes    = 1 << 20
	maxRequestBytes   = 4096
	maxTraceBytes     = 4 << 20
)

// Invocation contains only already admitted controller values. A command loader
// must authenticate its immutable controls before constructing these values.
// Inventory is not reconstructed from an untrusted filesystem enumeration.
type Invocation struct {
	Parent    typedindex.Admission
	Execution typedindex.Admission
	Profile   typedindex.Profile
	Inventory typedindex.Inventory
	Plan      typedindex.PackagePlan
	Allowance typedsandbox.Allowance
	Phase     typedindex.Action
}

// Selection is operator-presealed canary authority. Targets include the complete
// configured graph and package edges, not just selected root labels.
type Selection struct {
	Schema  string                     `json:"schema"`
	Source  typedindex.Source          `json:"source"`
	Roots   []string                   `json:"roots"`
	Targets []typedindex.PlannedTarget `json:"targets"`
	Module  string                     `json:"module"`
	Remote  string                     `json:"remote"`
}

type DocumentOutcome struct {
	Document string                   `json:"document"`
	Unit     typedindex.PackageUnitID `json:"unit"`
	RawPath  string                   `json:"raw_path"`
	Path     string                   `json:"path"`
	State    string                   `json:"state"`
}

// Result contains private bounded evidence. Callers must independently validate
// it and classify errors; it cannot install a bundle or advance durable state.
type Result struct {
	Failure       *Failure                 `json:"failure,omitempty"`
	Generated     map[string][]byte        `json:"generated"`
	Schema        string                   `json:"schema"`
	RequestDigest string                   `json:"request_digest"`
	Phase         typedindex.Action        `json:"phase"`
	Plan          json.RawMessage          `json:"plan"`
	RawPlan       planner.Plan             `json:"raw_plan"`
	GoFiles       launcher.NativeGoFiles   `json:"go_files"`
	Documents     []DocumentOutcome        `json:"documents"`
	Units         []typedindex.UnitOutcome `json:"units"`
	Legs          []LegEvidence            `json:"legs"`
	SCIP          []byte                   `json:"scip"`
	SCIPSHA256    string                   `json:"scip_sha256"`
}

func hash(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }
func identity(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return hash(b)
}
func digest(s string) bool {
	if len(s) != 71 || !strings.HasPrefix(s, "sha256:") || strings.ToLower(s) != s {
		return false
	}
	_, e := hex.DecodeString(s[7:])
	return e == nil
}
func decode[T any](raw []byte, bound int) (T, error) {
	var v T
	if len(raw) == 0 || len(raw) > bound {
		return v, typedindex.Invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil {
		return v, typedindex.Invalid
	}
	b, e := json.Marshal(v)
	if e != nil || !bytes.Equal(bytes.TrimSpace(raw), b) {
		return v, typedindex.Invalid
	}
	return v, nil
}
func (i Invocation) validate(ctx context.Context) error {
	if ctx == nil || i.Parent.Digest() == "" || i.Parent.Request().Action != typedindex.Plan || i.Profile.Digest() != i.Parent.Request().ProfileDigest || i.Inventory.Digest() != i.Parent.Request().BundleDigest || i.Allowance.Validate() != nil || i.Allowance.PlanningDigest != i.Parent.Digest() {
		return typedindex.Invalid
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	switch i.Phase {
	case typedindex.Plan:
		if i.Execution.Digest() != "" || i.Plan.Digest() != "" || i.Allowance.WorkerBytesUsed != 0 || i.Allowance.WireBytesUsed != 0 {
			return typedindex.Invalid
		}
	case typedindex.Execute:
		r, e := typedindex.PlannedSuccessor(ctx, i.Parent, i.Plan.Digest())
		if e != nil || i.Execution.Digest() == "" || i.Execution.Request() != r {
			return typedindex.Stale
		}
		if _, e = typedindex.DecodePackagePlan(ctx, i.Parent, i.Plan.Bytes(), i.Plan.Digest()); e != nil {
			return e
		}
	default:
		return typedindex.Invalid
	}
	p := i.Profile.Definition()
	if p.RCDigest != "" || p.Tools.Bazel.Digest != BazelDigest || p.Tools.Bazel.Version != "9.0.0" || p.Tools.Go.Digest != GoDigest || p.Tools.Go.Version != "1.25.0" || p.Tools.Driver.Digest != "sha256:"+launcher.NativeDriverSHA256 || p.Tools.RulesGo.Version != "0.59.0" || p.Tools.Indexer.Digest != SCIPDigest || p.Tools.Indexer.Version != "0.2.7" {
		return typedindex.Unsupported
	}
	return nil
}
func classify(err error) error {
	if err == nil {
		return nil
	}
	var refusal typedindex.Refusal
	if errors.As(err, &refusal) {
		return refusal
	}
	if errors.Is(err, context.Canceled) {
		return typedindex.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return typedindex.WallLimit
	}
	return typedindex.ExecutionFailed
}

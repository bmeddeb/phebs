// Package typedindex defines the closed managed-SCIP authority contract.
// It performs no filesystem I/O, schedules no jobs and starts no processes.
// Executor, durable state and publication integration belong to later tickets.
package typedindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/reponame"
)

const (
	ProviderID             = "bazel-rules-go-scip-v1"
	ProfileSchema          = "phebs-typed-profile-v1"
	GeneratedProfileSchema = "phebs-typed-profile-v2"
	InputProfileSchema     = "phebs-typed-input-profile-v1"
	RequestSchema          = "phebs-typed-request-v1"
	ManagedRequestSchema   = "phebs-typed-request-v2"
	MaxProfileBytes        = 16 << 10
	MaxRequestBytes        = 8 << 10
)

// Refusal is a closed, source-free boundary error. Never persist a raw tool error.
type Refusal string

const (
	Disabled        Refusal = "disabled"
	Forbidden       Refusal = "administrator_required"
	Invalid         Refusal = "invalid_contract"
	Unsupported     Refusal = "unsupported_profile"
	Stale           Refusal = "authority_changed"
	Capacity        Refusal = "capacity_refused"
	Unprepared      Refusal = "prehydration_unverified"
	WallLimit       Refusal = "wall_limit"
	ExecutionFailed Refusal = "execution_failed"
	Containment     Refusal = "containment_failed"
	Canceled        Refusal = "canceled"
)

func (r Refusal) Error() string { return "typed index: " + string(r) }

// Policy is the measured Phase 2 envelope, not a universal capacity guarantee.
type Policy struct {
	MemoryBytes     int64 `json:"memory_bytes"`
	ScratchBytes    int64 `json:"scratch_bytes"`
	ScratchInodes   int64 `json:"scratch_inodes"`
	Tasks           int64 `json:"tasks"`
	Descriptors     int64 `json:"descriptors_per_process"`
	CPUQuotaMicros  int64 `json:"cpu_quota_micros"`
	CPUPeriodMicros int64 `json:"cpu_period_micros"`
	WallSeconds     int64 `json:"wall_seconds"`
	OutputBytes     int64 `json:"output_bytes"`
	SCIPBytes       int64 `json:"scip_bytes"`
}

func MeasuredPolicy() Policy {
	return Policy{MemoryBytes: 4533092352, ScratchBytes: 4573403136, ScratchInodes: 262144, Tasks: 294, Descriptors: 128, CPUQuotaMicros: 200000, CPUPeriodMicros: 100000, WallSeconds: 300, OutputBytes: 16777216, SCIPBytes: 2285819}
}

type Tool struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type Tools struct {
	Bazel    Tool `json:"bazel"`
	RulesGo  Tool `json:"rules_go"`
	Go       Tool `json:"go"`
	Driver   Tool `json:"driver"`
	Indexer  Tool `json:"indexer"`
	Planner  Tool `json:"planner"`
	Launcher Tool `json:"launcher"`
}
type Config struct {
	GOOS                string `json:"goos"`
	GOARCH              string `json:"goarch"`
	Mode                string `json:"compilation_mode"`
	SkipTests           bool   `json:"skip_tests"`
	SkipImplementations bool   `json:"skip_implementations"`
	GeneratedDocuments  string `json:"generated_documents"`
	Network             string `json:"network"`
	Scratch             string `json:"scratch"`
}

func ReducedConfig() Config {
	return Config{GOOS: "linux", GOARCH: "arm64", Mode: "fastbuild", SkipTests: true, SkipImplementations: true, GeneratedDocuments: "omit", Network: "none", Scratch: "ext4-direct-io"}
}

// GeneratedConfig admits the sealed generated-document lane prospectively.
// It changes no execution limit or skip policy and registers no runtime provider.
func GeneratedConfig() Config {
	c := ReducedConfig()
	c.GeneratedDocuments = "sealed"
	return c
}

// ProfileDefinition is operator-owned configuration, never supplied by a browser
// planning request. RC copies can contain only the exact resolved bytes below.
type ProfileDefinition struct {
	Schema       string `json:"schema"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Tools        Tools  `json:"tools"`
	Config       Config `json:"config"`
	Policy       Policy `json:"policy"`
	BundleDigest string `json:"bundle_digest"`
	ImageDigest  string `json:"image_digest"`
	RCDigest     string `json:"rc_digest"` // empty disables all rc files
}

// Profile caches validated fixed-size values and their identities. All fields are
// private; copying or returning a definition cannot mutate the admitted profile.
type Profile struct {
	definition   ProfileDefinition
	digest       string
	configDigest string
	toolsDigest  string
	policyDigest string
}

func (p Profile) permitsGenerated() bool {
	return p.definition.Schema == GeneratedProfileSchema && p.definition.Config.GeneratedDocuments == "sealed"
}

func (p Profile) Digest() string                { return p.digest }
func (p Profile) Definition() ProfileDefinition { return p.definition }

// Provider returns the validated profile's closed provider discriminant. Every
// profile with a non-empty digest was admitted by DecodeProfile, which validates
// its provider-specific contract; request authority derives its provider here rather
// than from a constant, so a request can never claim a provider its profile does
// not carry.
func (p Profile) Provider() string { return p.definition.Provider }

// DecodeProfile requires canonical JSON (whitespace may surround or separate
// tokens). Canonical field spelling/order and explicit zero values make omitted,
// duplicate, case-aliased and unknown fields fail before any mutation.
func DecodeProfile(ctx context.Context, raw []byte) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	var d ProfileDefinition
	if err := decode(raw, MaxProfileBytes, &d); err != nil {
		return Profile{}, err
	}
	if !token(d.Name) || !digest(d.BundleDigest) || !digest(d.ImageDigest) {
		return Profile{}, Invalid
	}
	if d.Schema == InputProfileSchema {
		if err := validateInputProfile(d); err != nil {
			return Profile{}, err
		}
		return Profile{d, identity(d), identity(d.Config), identity(d.Tools), identity(d.Policy)}, nil
	}
	if (d.Schema != ProfileSchema && d.Schema != GeneratedProfileSchema) || d.Provider != ProviderID {
		return Profile{}, Invalid
	}
	wantConfig := ReducedConfig()
	if d.Schema == GeneratedProfileSchema {
		wantConfig = GeneratedConfig()
	}
	if d.Config != wantConfig || d.Policy != MeasuredPolicy() {
		return Profile{}, Unsupported
	}
	for _, t := range []Tool{d.Tools.Bazel, d.Tools.RulesGo, d.Tools.Go, d.Tools.Driver, d.Tools.Indexer, d.Tools.Planner, d.Tools.Launcher} {
		if !token(t.Version) || !digest(t.Digest) {
			return Profile{}, Invalid
		}
	}
	if d.RCDigest != "" && d.RCDigest != hash([]byte(ResolvedRC)) {
		return Profile{}, Invalid
	}
	return Profile{d, identity(d), identity(d.Config), identity(d.Tools), identity(d.Policy)}, nil
}

// Epoch is durable operator state. Persist AdvanceProfile with compare-and-swap
// before accepting work. Even equal-value replacement increments its epoch.
type Epoch struct {
	Number uint64
	Digest string
}

func AdvanceProfile(ctx context.Context, current Epoch, next Profile) (Epoch, error) {
	if err := ctx.Err(); err != nil {
		return Epoch{}, err
	}
	if next.digest == "" || current.Number == math.MaxUint64 || (current.Number == 0 && current.Digest != "") || (current.Number > 0 && !digest(current.Digest)) {
		return Epoch{}, Invalid
	}
	return Epoch{Number: current.Number + 1, Digest: next.digest}, nil
}

type Source struct {
	Repository  string `json:"repository"`
	Incarnation string `json:"incarnation"`
	Generation  string `json:"generation"`
	Commit      string `json:"commit"` // exact authoritative HEAD; never a branch selector
}

// Validate checks only source shape, not current repository authority.
func (s Source) Validate() error {
	if !validSource(s) {
		return Invalid
	}
	return nil
}

func validSource(s Source) bool {
	return reponame.Validate(s.Repository) == nil && len(s.Repository) <= 512 && token(s.Incarnation) && digest(s.Generation) && lowerHex(s.Commit, 40)
}

type Action string

const (
	Plan    Action = "plan"
	Execute Action = "execute"
)

// Purpose is immutable request authority, not a runtime publication option.
type Purpose string

const (
	Publish Purpose = "publish"
	Canary  Purpose = "canary"
	DryRun  Purpose = "dry-run"
)

type Request struct {
	Schema              string  `json:"schema"`
	Action              Action  `json:"action"`
	Source              Source  `json:"source"`
	Provider            string  `json:"provider"`
	ProfileName         string  `json:"profile_name"`
	ProfileEpoch        uint64  `json:"profile_epoch"`
	ProfileDigest       string  `json:"profile_digest"`
	ConfigDigest        string  `json:"config_digest"`
	ToolsDigest         string  `json:"tools_digest"`
	UniverseDigest      string  `json:"universe_digest"`
	BundleDigest        string  `json:"bundle_digest"`
	PolicyDigest        string  `json:"policy_digest"`
	IdempotencyKey      string  `json:"idempotency_key"`
	ParentRequestDigest string  `json:"parent_request_digest"`
	PlanDigest          string  `json:"plan_digest"`
	Purpose             Purpose `json:"purpose,omitempty"`
}

// Authority is trusted server state, loaded after authentication. None of its
// fields may be decoded from the planning/execution request. Ordinary repository
// visibility is insufficient. T45.4 must recheck this state at every transition.
type Authority struct {
	Enabled             bool
	Administrator       bool
	Source              Source
	Profile             Epoch
	UniverseDigest      string
	ParentRequestDigest string
	PlanDigest          string
}

// Admission has no execution capability. Only a later executor may use the
// immutable identity after current-authority and prepared-custody verification.
type Admission struct {
	request Request
	profile Profile
	digest  string
}

func (a Admission) Digest() string   { return a.digest }
func (a Admission) Request() Request { return a.request }

// NewRequest constructs a plan intent from trusted operator/source state. The
// provider is derived from the validated profile, never a constant, so the
// request cannot claim a provider its profile does not carry. Admit remains
// mandatory at every boundary, including for requests constructed here.
func NewRequest(source Source, profile Profile, epoch uint64, universe, key string) Request {
	return Request{Schema: RequestSchema, Action: Plan, Source: source, Provider: profile.definition.Provider, ProfileName: profile.definition.Name, ProfileEpoch: epoch, ProfileDigest: profile.digest, ConfigDigest: profile.configDigest, ToolsDigest: profile.toolsDigest, UniverseDigest: universe, BundleDigest: profile.definition.BundleDigest, PolicyDigest: profile.policyDigest, IdempotencyKey: key}
}

// NewManagedRequest constructs deterministic managed planning authority. It has
// no caller retry key; invalid shape returns a zero Request before encoding.
// Admit must still verify current trusted authority.
func NewManagedRequest(source Source, profile Profile, epoch uint64, universe string, purpose Purpose) Request {
	if len(source.Repository) > 512 || !validSource(source) || profile.digest == "" || epoch == 0 || !digest(universe) || purpose != Publish && purpose != Canary && purpose != DryRun {
		return Request{}
	}
	r := NewRequest(source, profile, epoch, universe, "")
	r.Schema, r.Purpose = ManagedRequestSchema, purpose
	r.IdempotencyKey = managedKey(r)
	return r
}

func managedKey(r Request) string {
	r.Action, r.ParentRequestDigest, r.PlanDigest, r.IdempotencyKey = Plan, "", "", ""
	raw, _ := json.Marshal(r)
	return hash(append([]byte("phebs-typed-managed-planning-key-v2\x00"), raw...))[7:]
}

// ValidatePurpose checks versioned purpose/key shape without asserting current
// authority. It also serves obsolete-owner and census reconstruction.
func (r Request) ValidatePurpose() error {
	switch r.Schema {
	case RequestSchema:
		if r.Purpose != "" {
			return Invalid
		}
	case ManagedRequestSchema:
		if r.Purpose != Publish && r.Purpose != Canary && r.Purpose != DryRun {
			return Invalid
		}
		if r.IdempotencyKey != managedKey(r) {
			return Invalid
		}
	default:
		return Invalid
	}
	return nil
}

// Purpose returns the admitted purpose; historical v1 means publishing.
func (a Admission) Purpose() Purpose {
	if a.digest == "" {
		return ""
	}
	if a.request.Schema == RequestSchema {
		return Publish
	}
	return a.request.Purpose
}

func Admit(ctx context.Context, authority Authority, profile Profile, raw []byte) (Admission, error) {
	if !authority.Enabled {
		return Admission{}, Disabled
	}
	if !authority.Administrator {
		return Admission{}, Forbidden
	}
	if err := ctx.Err(); err != nil {
		return Admission{}, err
	}
	var r Request
	if err := decode(raw, MaxRequestBytes, &r); err != nil {
		return Admission{}, err
	}
	if profile.digest == "" || r.ValidatePurpose() != nil || !validSource(r.Source) || !token(r.IdempotencyKey) || !digest(r.UniverseDigest) || r.ProfileEpoch == 0 {
		return Admission{}, Invalid
	}
	if r.Action != Plan && r.Action != Execute {
		return Admission{}, Invalid
	}
	if r.Provider != profile.definition.Provider || r.ProfileName != profile.definition.Name || r.ProfileDigest != profile.digest || r.ConfigDigest != profile.configDigest || r.ToolsDigest != profile.toolsDigest || r.BundleDigest != profile.definition.BundleDigest || r.PolicyDigest != profile.policyDigest {
		return Admission{}, Stale
	}
	if !validSource(authority.Source) || r.Source != authority.Source || r.ProfileEpoch != authority.Profile.Number || r.ProfileDigest != authority.Profile.Digest || r.UniverseDigest != authority.UniverseDigest {
		return Admission{}, Stale
	}
	if r.Action == Plan {
		if r.ParentRequestDigest != "" || r.PlanDigest != "" {
			return Admission{}, Invalid
		}
	} else {
		if !digest(r.ParentRequestDigest) || !digest(r.PlanDigest) || r.ParentRequestDigest != authority.ParentRequestDigest || r.PlanDigest != authority.PlanDigest {
			return Admission{}, Stale
		}
		parent := r
		parent.Action, parent.ParentRequestDigest, parent.PlanDigest = Plan, "", ""
		if identity(parent) != r.ParentRequestDigest {
			return Admission{}, Stale
		}
	}
	return Admission{r, profile, identity(r)}, nil
}

// PlannedSuccessor binds exactly one planner result to its admitted parent.
// Caller persists the parent/map binding and re-admits against current authority.
func PlannedSuccessor(ctx context.Context, parent Admission, planDigest string) (Request, error) {
	if err := ctx.Err(); err != nil {
		return Request{}, err
	}
	if parent.digest == "" || parent.request.Action != Plan || !digest(planDigest) {
		return Request{}, Invalid
	}
	r := parent.request
	r.Action = Execute
	r.ParentRequestDigest = parent.digest
	r.PlanDigest = planDigest
	return r, nil
}

func decode(raw []byte, limit int, out any) error {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return Invalid
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil {
		return Invalid
	}
	want, err := json.Marshal(out)
	if err != nil {
		return Invalid
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || !bytes.Equal(compact.Bytes(), want) {
		return Invalid
	}
	return nil
}
func identity(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return hash(b)
}
func hash(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }
func digest(s string) bool {
	return strings.HasPrefix(s, "sha256:") && lowerHex(strings.TrimPrefix(s, "sha256:"), 64)
}
func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func token(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return s != "." && s != ".."
}

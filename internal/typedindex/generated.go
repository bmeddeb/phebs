package typedindex

import (
	"context"
	"strings"
)

// MaxGeneratedBytes retains the planner's per-source-file ceiling. Defining this
// lane does not enable generated documents in the reduced v1 profile. Only the
// explicit generated v2 profile admits this lane through plan/bundle sealing.
const MaxGeneratedBytes = 8 << 20

const generatedNamespace = ".phebs-generated"

// PackageUnitID identifies a sealed package-load unit, never a target, service,
// repository, document or member. The planner supplies its stable identity.
type PackageUnitID string

func NewPackageUnitID(planIdentity string) (PackageUnitID, error) {
	if !digest(planIdentity) {
		return "", Invalid
	}
	return PackageUnitID("package-load:" + planIdentity), nil
}

func validPackageUnit(unit PackageUnitID) bool {
	s := string(unit)
	return strings.HasPrefix(s, "package-load:") && digest(strings.TrimPrefix(s, "package-load:"))
}

// GenerationBinding is exact trusted attempt authority. Callers obtain the
// expected value from current state and the sealed plan, not a source descriptor.
type GenerationBinding struct {
	Source        Source `json:"source"`
	RequestDigest string `json:"request_digest"`
	ProfileDigest string `json:"profile_digest"`
	ToolsDigest   string `json:"tools_digest"`
	PlanDigest    string `json:"plan_digest"`
}

func validGenerationBinding(b GenerationBinding) bool {
	return validSource(b.Source) && digest(b.RequestDigest) && digest(b.ProfileDigest) && digest(b.ToolsDigest) && digest(b.PlanDigest)
}

// GeneratedDocument describes bytes in private generated custody. ProvenanceDigest
// must match the sealed plan's generating action/source identity before publication;
// it is not an executable command or an ambient build path. No source bytes are held.
type GeneratedDocument struct {
	Binding          GenerationBinding `json:"binding"`
	Unit             PackageUnitID     `json:"unit"`
	Path             string            `json:"path"`
	ProvenanceDigest string            `json:"provenance_digest"`
	Bytes            int64             `json:"bytes"`
	Digest           string            `json:"digest"`
}

// IsGeneratedPath reserves the entire namespace, including the bare directory,
// so a repository document can never acquire generated-source authority.
func IsGeneratedPath(name string) bool {
	return name == generatedNamespace || strings.HasPrefix(name, generatedNamespace+"/")
}

// GeneratedPath accepts a canonical logical path selected by the sealed plan.
// It never cleans or converts a producer's absolute or parent-traversal path.
func GeneratedPath(unit PackageUnitID, logical string) (string, error) {
	if !validPackageUnit(unit) || !bundlePath(logical) || IsGeneratedPath(logical) {
		return "", Invalid
	}
	name := generatedNamespace + "/" + strings.TrimPrefix(string(unit), "package-load:sha256:") + "/" + logical
	if !bundlePath(name) {
		return "", Invalid
	}
	return name, nil
}

func validGeneratedDocument(d GeneratedDocument) bool {
	if !validGenerationBinding(d.Binding) || !validPackageUnit(d.Unit) || !digest(d.ProvenanceDigest) || !digest(d.Digest) || d.Bytes < 0 || d.Bytes > MaxGeneratedBytes {
		return false
	}
	prefix := generatedNamespace + "/" + strings.TrimPrefix(string(d.Unit), "package-load:sha256:") + "/"
	if !strings.HasPrefix(d.Path, prefix) {
		return false
	}
	name, err := GeneratedPath(d.Unit, strings.TrimPrefix(d.Path, prefix))
	return err == nil && name == d.Path
}

// NewGeneratedDocument seals a bounded byte identity without retaining raw. The
// caller must separately enforce immutable byte custody and complete plan coverage.
func NewGeneratedDocument(ctx context.Context, binding GenerationBinding, unit PackageUnitID, logical, provenance string, raw []byte) (GeneratedDocument, error) {
	if err := ctx.Err(); err != nil {
		return GeneratedDocument{}, err
	}
	if !validGenerationBinding(binding) || !digest(provenance) || raw == nil || len(raw) > MaxGeneratedBytes {
		return GeneratedDocument{}, Invalid
	}
	name, err := GeneratedPath(unit, logical)
	if err != nil {
		return GeneratedDocument{}, err
	}
	return GeneratedDocument{Binding: binding, Unit: unit, Path: name, ProvenanceDigest: provenance, Bytes: int64(len(raw)), Digest: hash(raw)}, nil
}

// VerifyGeneratedDocument checks current authority before hashing at most 8 MiB.
// The bundle verifier additionally compares path/provenance against the sealed plan.
// No filesystem read, process, persistent allocation or mutable byte alias is created.
func VerifyGeneratedDocument(ctx context.Context, d GeneratedDocument, expected GenerationBinding, unit PackageUnitID, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validGenerationBinding(expected) || !validPackageUnit(unit) || !validGeneratedDocument(d) {
		return Invalid
	}
	if d.Binding != expected || d.Unit != unit {
		return Stale
	}
	if raw == nil || int64(len(raw)) != d.Bytes || hash(raw) != d.Digest {
		return Invalid
	}
	return nil
}

package candidate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CallerPlan is the manifest-bound, leaf-addressed replay surface for caller
// execution. Opening a plan validates the canonical manifest and every caller
// leaf envelope, but deliberately does not read repository, local-projection,
// or caller-leaf member contents.
type CallerPlan struct {
	root     string
	manifest Manifest
	state    State
	unit     *analysisUnitView
	policies map[string]PolicyIdentity
}

// State returns the exact primitive publication identity behind the plan.
func (plan *CallerPlan) State() State {
	if plan == nil {
		return State{}
	}
	return plan.state
}

// Domains returns the canonical caller-plane policy identities. The returned
// slice is independent of the plan and may be crossed with Leaves verbatim,
// including pairs whose selected leaf contains no record for that domain.
func (plan *CallerPlan) Domains() []PolicyIdentity {
	if plan == nil {
		return nil
	}
	result := make([]PolicyIdentity, 0)
	for _, identity := range plan.manifest.Policies {
		if identity.Plane == PlaneCaller {
			cloned := identity
			cloned.TypedInputs = slices.Clone(identity.TypedInputs)
			result = append(result, cloned)
		}
	}
	return result
}

// Leaves returns the exact ordered caller-leaf descriptors committed by the
// manifest. Descriptors, including apparently empty domain/leaf pairs, must
// not be inferred from leaf contents.
func (plan *CallerPlan) Leaves() []CallerLeaf {
	if plan == nil {
		return nil
	}
	return slices.Clone(plan.manifest.CallerLeaves)
}

// OpenCallerPlanContext opens the exact expected candidate generation without
// performing the complete strict-publication scan used by OpenContext. The
// manifest digest still binds all publication envelopes; this narrow surface
// validates only the identities and caller leaf plan needed for direct leaf
// execution. ReplayLeaf validates one selected leaf's complete contents.
func OpenCallerPlanContext(
	ctx context.Context,
	root string,
	expected Expected,
) (*CallerPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if IsPublishing(root, expected.Repository) {
		return nil, ErrPublishing
	}
	unit, err := unitView(expected)
	if err != nil {
		return nil, err
	}
	if err := ensureRealDirectory(root); err != nil {
		return nil, err
	}
	manifest, err := readManifest(
		filepath.Join(root, ManifestName(expected.Repository)),
	)
	if err != nil {
		return nil, err
	}
	if err := validateManifestIdentity(manifest, expected, nil); err != nil {
		return nil, err
	}
	policies, err := validateCallerPlan(ctx, manifest)
	if err != nil {
		return nil, err
	}
	if IsPublishing(root, expected.Repository) {
		return nil, ErrPublishing
	}
	return &CallerPlan{
		root: root, manifest: manifest, state: manifest.State(), unit: unit,
		policies: policies,
	}, nil
}

func validateCallerPlan(
	ctx context.Context,
	manifest Manifest,
) (map[string]PolicyIdentity, error) {
	policies := make(map[string]PolicyIdentity, len(manifest.Policies))
	callerPolicies := 0
	for _, identity := range manifest.Policies {
		policies[identity.Domain] = identity
		if identity.Plane == PlaneCaller {
			callerPolicies++
		}
	}
	if callerPolicies == 0 && len(manifest.CallerLeaves) != 0 {
		return nil, fmt.Errorf(
			"%w: caller leaves exist without a caller policy", ErrInvalidManifest,
		)
	}
	prefix := ArtifactPrefix(manifest.Repository, manifest.GenerationDigest)
	var previousPrefix string
	for ordinal, leaf := range manifest.CallerLeaves {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		expectedName := prefix + fmt.Sprintf("caller-%06d.ndjson", ordinal)
		if err := validateArtifactEnvelope(
			leaf.Artifact, ordinal, expectedName,
		); err != nil {
			return nil, err
		}
		if err := validateCallerLeaf(leaf, previousPrefix); err != nil {
			return nil, err
		}
		if previousPrefix != "" && strings.HasPrefix(leaf.Prefix, previousPrefix) {
			return nil, fmt.Errorf("%w: caller leaves overlap", ErrInvalidManifest)
		}
		previousPrefix = leaf.Prefix
	}
	if err := validateMinimalCallerSplits(
		ctx, manifest.CallerLeaves,
	); err != nil {
		return nil, err
	}
	return policies, nil
}

// ReplayLeaf validates and replays one exact leaf descriptor. Every record in
// the selected leaf is validated before domain filtering, so an unrelated
// malformed record cannot hide behind an otherwise empty domain/leaf pair.
func (plan *CallerPlan) ReplayLeaf(
	ctx context.Context,
	domain, version string,
	leaf CallerLeaf,
	visit func(Record) error,
) error {
	if plan == nil || plan.unit == nil || visit == nil {
		return errors.New("candidate caller leaf replay is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	identity, ok := plan.policies[domain]
	if !ok || identity.Version != version || identity.Plane != PlaneCaller {
		return fmt.Errorf(
			"candidate caller domain %s/%s: %w", domain, version, os.ErrNotExist,
		)
	}
	if leaf.Ordinal < 0 || leaf.Ordinal >= len(plan.manifest.CallerLeaves) ||
		plan.manifest.CallerLeaves[leaf.Ordinal] != leaf {
		return fmt.Errorf("%w: caller leaf descriptor mismatch", ErrInvalidManifest)
	}
	if IsPublishing(plan.root, plan.manifest.Repository) {
		return ErrPublishing
	}
	var previous *Record
	err := validateArtifactFile(
		ctx, filepath.Join(plan.root, leaf.Name), leaf.Artifact,
		func(record Record) error {
			if err := validateRecord(
				record, PlaneCaller, plan.policies, plan.unit,
			); err != nil {
				return err
			}
			if !hashHasPrefix(record.Hash, leaf.Prefix) {
				return fmt.Errorf(
					"%w: caller record is outside its leaf", ErrInvalidManifest,
				)
			}
			if previous != nil {
				if previous.Path == record.Path {
					return fmt.Errorf(
						"%w: duplicate caller path %q", ErrInvalidManifest, record.Path,
					)
				}
				if compareCallerRecords(*previous, record) >= 0 {
					return fmt.Errorf(
						"%w: caller records are not canonical", ErrInvalidManifest,
					)
				}
			}
			copied := record
			previous = &copied
			if !slices.Contains(record.Domains, domain) {
				return nil
			}
			record.Required = slices.Contains(record.RequiredDomains, domain)
			return visit(record)
		},
	)
	if err != nil {
		return fmt.Errorf("replay candidate caller leaf %q: %w", leaf.Name, err)
	}
	if IsPublishing(plan.root, plan.manifest.Repository) {
		return ErrPublishing
	}
	return nil
}

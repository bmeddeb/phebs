package extractionpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/candidate"
)

type restoredDomain struct {
	Domain DomainPlan
	Root   candidate.DomainResultRoot
}

// restorePublished reconstructs an excluded filesystem generation from the
// store's exact canonical plan/root controls. It installs every partition
// result and completion bit before pointer publication, then reuses the normal
// authority fence and exact store publication path. No source or evidence
// member is read and no scheduler work is created.
func (runtime *Runtime) restorePublished(
	ctx context.Context,
	repository string,
	domains []restoredDomain,
) (string, error) {
	if err := runtime.validate(); err != nil {
		return "", err
	}
	if ctx == nil || len(domains) == 0 || len(domains) > MaxDomains {
		return "", invalid("restored generation inventory")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	planDigests := make([]string, 0, len(domains))
	seen := make(map[string]struct{}, len(domains))
	plans := make([]DomainPlan, len(domains))
	for index, restored := range domains {
		domain := restored.Domain
		if domain.Schema != DomainSchema || domain.Plan.Repository != repository ||
			!boundedIdentity(domain.RunID, 256) ||
			candidate.ValidateDomainResultPlanControl(domain.Plan) != nil ||
			candidate.ValidateDomainResultRoot(restored.Root, domain.Plan) != nil ||
			len(restored.Root.Results) != len(domain.Plan.Expected) {
			return "", invalid("restored domain authority")
		}
		key := domain.Plan.Domain + "\x00" + domain.Plan.Version
		if _, duplicate := seen[key]; duplicate {
			return "", invalid("duplicate restored domain")
		}
		seen[key] = struct{}{}
		plans[index] = domain
		planDigests = append(planDigests, domain.Plan.Digest)
	}
	target, err := GenerationDigest(repository, planDigests)
	if err != nil {
		return "", err
	}
	directory := runtime.generationDirectory(repository, target)
	if _, err := os.Lstat(directory); err == nil {
		return "", invalid("restored generation already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := ensurePrivateDirectory(runtime.repositoryDirectory(repository)); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(runtime.repositoryDirectory(repository), ".stage-restore-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	generation := Generation{
		Schema: GenerationSchema, Repository: repository, Digest: target,
		Domains: make([]DomainDescriptor, 0, len(domains)),
	}
	workOffset := 0
	for index, restored := range domains {
		domain := restored.Domain
		planName := fmt.Sprintf("plan-%03d.json", index)
		descriptor := DomainDescriptor{
			Domain: domain.Plan.Domain, Version: domain.Plan.Version,
			PlanDigest: domain.Plan.Digest, PlanName: planName, RunID: domain.RunID,
			StartOrdinal: workOffset, EndOrdinal: workOffset + len(domain.Plan.Expected),
		}
		generation.Domains = append(generation.Domains, descriptor)
		workOffset = descriptor.EndOrdinal
		if err := writeExclusiveCanonical(filepath.Join(staging, planName), domain); err != nil {
			return "", err
		}
		resultDirectory := filepath.Join(staging, domainKey(domain.Plan.Domain))
		if err := ensurePrivateDirectory(resultDirectory); err != nil {
			return "", err
		}
		completion := newCompletionControl(domain.Plan)
		for ordinal, result := range restored.Root.Results {
			if result.PartitionOrdinal != ordinal || !markCompletion(&completion, ordinal) {
				return "", invalid("restored partition result order")
			}
			if err := writeExclusiveCanonical(
				filepath.Join(resultDirectory, resultName(ordinal)), result,
			); err != nil {
				return "", err
			}
		}
		if err := writeExclusiveCanonical(
			filepath.Join(resultDirectory, completionName()), completion,
		); err != nil {
			return "", err
		}
		if err := writeExclusiveCanonical(
			filepath.Join(resultDirectory, rootName()), restored.Root,
		); err != nil {
			return "", err
		}
		if err := syncDirectory(resultDirectory); err != nil {
			return "", err
		}
	}
	generation.WorkItems = workOffset
	if err := validateGeneration(generation); err != nil {
		return "", err
	}
	if err := writeExclusiveCanonical(filepath.Join(staging, generationName()), generation); err != nil {
		return "", err
	}
	if err := syncDirectory(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, directory); err != nil {
		return "", err
	}
	if err := syncDirectory(runtime.repositoryDirectory(repository)); err != nil {
		return "", err
	}
	authority, err := authorityForPlans(repository, plans)
	if err != nil {
		return "", err
	}
	if err := runtime.writeAuthorityBinding(authority, target); err != nil {
		return "", err
	}
	if err := runtime.reactivateComplete(ctx, generation, directory); err != nil {
		return "", err
	}
	if !runtime.generationSettled(generation, directory) {
		return "", invalid("restored generation did not settle")
	}
	return target, nil
}

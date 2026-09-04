package relationshippublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	// MaxRecoveryWork bounds charged namespace discovery, repair, protected-v3
	// audit reservations, and pin reconstruction. Marker completion is instead
	// bounded once per discovered repository by each format's fixed generation
	// ceiling (resolver, legacy relationship, and v3 relationship).
	MaxRecoveryWork   = 16_000_000
	MaxRecoveryOwners = MaxLifecycleRepositories *
		((RetainedGenerations + 2) + (RetainedGenerationsV3 + 2))
	MaxRecoveryNamespaces = MaxLifecycleRepositories * 5
)

type RecoveryReport struct {
	Repositories int
	Completed    int
	Unavailable  int
	Invalid      int
}

type RecoveryPinStore interface {
	PinPartitionedExtractionRun(context.Context, string, string) error
	ReconcilePartitionedExtractionOwners(context.Context, []string) error
}

type recoveryPinStoreV3 interface {
	RecoveryPinStoreV3
	RecoverServiceCatalogV3RelationshipReference(
		context.Context,
		store.ServiceCatalogV3RelationshipReference,
	) error
	ReconcileServiceCatalogV3RelationshipReferences(
		context.Context,
		[]store.ServiceCatalogV3RelationshipReference,
	) error
}

type recoveryStoreError struct{ error }

// RecoverAll completes exact component/root markers under the caller's
// exclusive startup mutation lock. Corrupt derived state is counted and left
// invisible; component-only crash residue is independently discovered.
func RecoverAll(ctx context.Context, dataDir string, pins RecoveryPinStore) (RecoveryReport, error) {
	return recoverAll(ctx, dataDir, pins, nil)
}

// RecoverAllWithV3TransitionObserver preserves RecoverAll's startup authority
// while reporting a marker recovery only after current, marker removal, and
// the repository directory sync are durable. A nil observer is identical to
// RecoverAll.
func RecoverAllWithV3TransitionObserver(
	ctx context.Context,
	dataDir string,
	pins RecoveryPinStore,
	afterRecovery PublicationTransitionRecoveryObserverV3,
) (RecoveryReport, error) {
	return recoverAll(ctx, dataDir, pins, afterRecovery)
}

func recoverAll(
	ctx context.Context,
	dataDir string,
	pins RecoveryPinStore,
	afterRecovery PublicationTransitionRecoveryObserverV3,
) (RecoveryReport, error) {
	var report RecoveryReport
	if !filepath.IsAbs(dataDir) || pins == nil {
		return report, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	root := filepath.Join(dataDir, "relationships")
	base := filepath.Join(root, "relationship-publications")
	shadowBase := ShadowBase(root)
	hashes, work, invalid, err := discoverRecoveryNamespaces(ctx, dataDir)
	if err != nil {
		return report, err
	}
	report.Invalid += invalid
	owners := make(map[string]struct{}, MaxRecoveryOwners)
	v3References := make([]store.ServiceCatalogV3RelationshipReference, 0)
	v3Pins, hasV3Pins := pins.(recoveryPinStoreV3)
	reconciliationDeferred := false
	for _, hash := range hashes {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		legacyDirectory := filepath.Join(base, hash)
		shadowDirectory := filepath.Join(shadowBase, hash)
		componentDirectories := []struct {
			directory string
			prefixed  bool
		}{
			{filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces", hash), true},
			{filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings", hash), false},
			{filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings", hash), false},
		}
		legacyUsable, shadowUsable, componentsUsable := true, true, true
		consumed, repairErr := repairRecoveryRelationshipDirectory(
			ctx, legacyDirectory, hash, false, MaxRecoveryWork-work,
		)
		work += consumed
		if repairErr != nil {
			if fatal := recoveryFatal(ctx, repairErr); fatal != nil {
				return report, fatal
			}
			report.Invalid++
			legacyUsable = false
		}
		consumed, repairErr = repairRecoveryRelationshipDirectory(
			ctx, shadowDirectory, hash, true, MaxRecoveryWork-work,
		)
		work += consumed
		if repairErr != nil {
			if fatal := recoveryFatal(ctx, repairErr); fatal != nil {
				return report, fatal
			}
			report.Invalid++
			shadowUsable = false
		}
		for _, component := range componentDirectories {
			_, consumed, repairErr := repairStageDirectories(
				ctx, component.directory, MaxRecoveryWork-work,
			)
			work += consumed
			if repairErr != nil {
				if fatal := recoveryFatal(ctx, repairErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
				componentsUsable = false
			}
		}

		legacyExists := false
		if legacyUsable {
			var existsErr error
			legacyExists, existsErr = directoryExists(legacyDirectory)
			if existsErr != nil {
				if fatal := recoveryFatal(ctx, existsErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
				legacyUsable = false
			}
		}
		shadowExists := false
		if shadowUsable {
			var existsErr error
			shadowExists, existsErr = directoryExists(shadowDirectory)
			if existsErr != nil {
				if fatal := recoveryFatal(ctx, existsErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
				shadowUsable = false
			}
		}
		if !legacyExists && !shadowExists {
			if legacyUsable && shadowUsable && componentsUsable {
				for _, component := range componentDirectories {
					consumed, cleanupErr := cleanupOrphanComponentNamespace(
						ctx, component.directory, component.prefixed, MaxRecoveryWork-work,
					)
					work += consumed
					if cleanupErr != nil {
						if fatal := recoveryFatal(ctx, cleanupErr); fatal != nil {
							return report, fatal
						}
						report.Invalid++
					}
				}
			}
			continue
		}

		report.Repositories++
		legacyRepository, shadowRepository := "", ""
		legacyRepositoryValid, shadowRepositoryValid := false, false
		if legacyExists {
			var unavailable bool
			var repositoryErr error
			legacyRepository, unavailable, repositoryErr = recoveryRepository(legacyDirectory, hash)
			if repositoryErr != nil {
				if fatal := recoveryFatal(ctx, repositoryErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
			} else {
				legacyRepositoryValid = true
				if unavailable {
					report.Unavailable++
				}
			}
		}
		if shadowExists {
			if !hasV3Pins {
				return report, errors.New("relationship v3 recovery pins are unavailable")
			}
			var repositoryErr error
			shadowRepository, repositoryErr = recoveryRepositoryV3(shadowDirectory, hash)
			if repositoryErr != nil {
				if fatal := recoveryFatal(ctx, repositoryErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
			} else {
				shadowRepositoryValid = true
			}
		}
		repositoryMismatch := legacyRepositoryValid && shadowRepositoryValid &&
			legacyRepository != shadowRepository
		if repositoryMismatch {
			report.Invalid++
		}
		resolverRepository := ""
		if !repositoryMismatch {
			if legacyRepositoryValid {
				resolverRepository = legacyRepository
			} else if shadowRepositoryValid {
				resolverRepository = shadowRepository
			}
		}
		resolverRoot := filepath.Join(dataDir, "relationship-resolver-namespaces")
		resolverDirectory := componentDirectories[0].directory
		if resolverRepository != "" && componentControlPresent(resolverDirectory) {
			if _, recoverErr := resolvernamespace.Recover(
				ctx, resolverRoot, resolverRepository,
			); recoverErr != nil {
				if fatal := recoveryFatal(ctx, recoverErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
			}
		}
		if legacyRepositoryValid {
			completed, recoverErr := Recover(ctx, root, legacyRepository)
			if recoverErr != nil {
				if fatal := recoveryFatal(ctx, recoverErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
			} else if completed {
				report.Completed++
			}
		}
		if shadowRepositoryValid {
			completed, recoverErr := recoverV3(
				ctx, root, shadowRepository, v3Pins, afterRecovery,
			)
			if recoverErr != nil {
				if fatal := recoveryFatal(ctx, recoverErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
			} else if completed {
				report.Completed++
			}
		}
		if legacyExists && legacyUsable {
			consumed, deferred, auditErr := recoverLegacyPins(
				ctx, legacyDirectory, pins, owners, MaxRecoveryWork-work,
			)
			work += consumed
			reconciliationDeferred = reconciliationDeferred || deferred
			if auditErr != nil {
				if fatal := recoveryFatal(ctx, auditErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
			}
		}
		if shadowRepositoryValid {
			consumed, references, deferred, auditErr := recoverV3Pins(
				ctx, root, shadowDirectory, shadowRepository, v3Pins, owners,
				MaxRecoveryWork-work,
			)
			work += consumed
			reconciliationDeferred = reconciliationDeferred || deferred
			if auditErr != nil {
				if fatal := recoveryFatal(ctx, auditErr); fatal != nil {
					return report, fatal
				}
				report.Invalid++
			} else {
				v3References = append(v3References, references...)
				if len(v3References) > store.MaxServiceCatalogV3RelationshipReferences {
					return report, ErrLimit
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	orderedOwners := make([]string, 0, len(owners))
	for owner := range owners {
		orderedOwners = append(orderedOwners, owner)
	}
	sort.Strings(orderedOwners)
	// A visible root was pinned before publication. If any namespace audit is
	// invalid, or regular legacy/v3 bytes lie outside the bounded current/rollback
	// audit, preserve both existing global pin sets rather than deleting an
	// owner that this recovery pass deliberately did not re-prove. Lifecycle
	// later performs rename-before-exact-unpin for that readable residue.
	if report.Invalid != 0 || reconciliationDeferred {
		return report, nil
	}
	if err := pins.ReconcilePartitionedExtractionOwners(ctx, orderedOwners); err != nil {
		return report, err
	}
	if hasV3Pins {
		sort.Slice(v3References, func(i, j int) bool {
			return v3References[i].RelationshipGenerationDigest <
				v3References[j].RelationshipGenerationDigest
		})
		if err := v3Pins.ReconcileServiceCatalogV3RelationshipReferences(
			ctx, v3References,
		); err != nil {
			return report, err
		}
	}
	return report, nil
}

func removeUncommittedPublishingTemporaryV3(
	ctx context.Context,
	base string,
	budget int,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	_, err := os.Lstat(filepath.Join(base, "publishing.json.tmp"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if budget < 1 {
		return 0, ErrLimit
	}
	if err := removePublishingTemporaryV3(base); err != nil {
		return 0, err
	}
	return 1, nil
}

func recoveryFatal(ctx context.Context, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var storeErr recoveryStoreError
	if errors.As(cause, &storeErr) {
		return storeErr.error
	}
	if cause == nil || isDerivedRecoveryOmission(cause) {
		return nil
	}
	return cause
}

func recoverLegacyPins(
	ctx context.Context,
	repositoryDirectory string,
	pins RecoveryPinStore,
	owners map[string]struct{},
	budget int,
) (int, bool, error) {
	if budget < 1 {
		return 0, false, ErrLimit
	}
	entries, err := boundedLifecycleDirectory(
		repositoryDirectory, min(MaxRepositoryRepairEntries, budget),
	)
	if err != nil {
		return 0, false, err
	}
	work := len(entries)
	protected, incomplete, err := recoveryProtectedGenerations(repositoryDirectory, entries)
	if err != nil {
		return work, incomplete, err
	}
	validated := make(map[string]struct{}, len(protected))
	var auditErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return work, incomplete, err
		}
		name := entry.Name()
		if _, keep := protected["sha256:"+name]; !keep || !entry.IsDir() ||
			len(name) != 64 || !validLowerHex(name) {
			continue
		}
		if work >= budget {
			return work, incomplete, ErrLimit
		}
		raw, rootErr := readRegular(
			filepath.Join(repositoryDirectory, name, "root.json"), MaxRootBytes,
		)
		work++
		if rootErr != nil {
			if !isDerivedRecoveryOmission(rootErr) {
				return work, incomplete, rootErr
			}
			auditErr = ErrInvalid
			continue
		}
		var rootValue Root
		if decodeExact(raw, MaxRootBytes, &rootValue) != nil || validateRoot(rootValue) != nil ||
			rootValue.GenerationDigest != "sha256:"+name {
			auditErr = ErrInvalid
			continue
		}
		validated["sha256:"+name] = struct{}{}
		if rootValue.Authority.Upstream == nil {
			continue
		}
		owner := "relationship:" + rootValue.GenerationDigest
		if err := addRecoveryOwner(owners, owner); err != nil {
			return work, incomplete, err
		}
		for _, domain := range rootValue.Authority.Upstream.Domains {
			if work >= budget {
				return work, incomplete, ErrLimit
			}
			work++
			if err := pins.PinPartitionedExtractionRun(ctx, domain.RunID, owner); err != nil {
				return work, incomplete, recoveryStoreError{err}
			}
		}
	}
	if len(validated) != len(protected) {
		auditErr = ErrInvalid
	} else {
		for generation := range protected {
			if _, present := validated[generation]; !present {
				auditErr = ErrInvalid
				break
			}
		}
	}
	return work, incomplete, auditErr
}

func recoverV3Pins(
	ctx context.Context,
	root, repositoryDirectory, repository string,
	pins recoveryPinStoreV3,
	owners map[string]struct{},
	budget int,
) (int, []store.ServiceCatalogV3RelationshipReference, bool, error) {
	if budget < 1 {
		return 0, nil, false, ErrLimit
	}
	entries, err := boundedLifecycleDirectory(
		repositoryDirectory, min(MaxRepositoryRepairEntries, budget),
	)
	if err != nil {
		return 0, nil, false, err
	}
	work := len(entries)
	protected, incomplete, err := recoveryProtectedGenerationsV3(repositoryDirectory, entries)
	if err != nil {
		return work, nil, incomplete, err
	}
	references := make([]store.ServiceCatalogV3RelationshipReference, 0, len(protected))
	validated := make(map[string]struct{}, len(protected))
	var auditErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return work, nil, incomplete, err
		}
		name := entry.Name()
		generation := "sha256:" + name
		rootDigest, keep := protected[generation]
		if !keep || !entry.IsDir() || len(name) != 64 || !validLowerHex(name) {
			continue
		}
		if rootDigest == "" {
			if work >= budget {
				return work, nil, incomplete, ErrLimit
			}
			raw, rootErr := readRegular(
				filepath.Join(repositoryDirectory, name, "root.json"), MaxRootBytesV3,
			)
			work++
			if rootErr != nil {
				if !isDerivedRecoveryOmission(rootErr) {
					return work, nil, incomplete, rootErr
				}
				auditErr = ErrInvalid
				continue
			}
			var value RootV3
			if decodeExact(raw, MaxRootBytesV3, &value) != nil || ValidateRootV3(value) != nil ||
				value.Authority.Repository != repository || value.GenerationDigest != generation {
				auditErr = ErrInvalid
				continue
			}
			rootDigest = value.Digest
		}
		// Charge the complete ceiling before the indivisible validator so corrupt
		// generations cannot consume an unaccounted full read.
		if budget-work < MaxGenerationFilesV3 {
			return work, nil, incomplete, ErrLimit
		}
		work += MaxGenerationFilesV3
		publication, validateErr := ValidateGenerationV3(
			ctx, root, repository, generation, rootDigest,
		)
		if validateErr != nil {
			if !isDerivedRecoveryOmission(validateErr) {
				return work, nil, incomplete, validateErr
			}
			auditErr = ErrInvalid
			continue
		}
		rootValue := publication.Root()
		validated[generation] = struct{}{}
		owner := "relationship:" + generation
		if err := addRecoveryOwner(owners, owner); err != nil {
			return work, nil, incomplete, err
		}
		reference := store.ServiceCatalogV3RelationshipReference{
			Repository:                   repository,
			RelationshipGenerationDigest: generation,
			RelationshipRootDigest:       rootValue.Digest,
			CatalogRootDigest:            rootValue.Authority.CatalogRootDigest,
			CatalogControlRevision:       rootValue.Authority.CatalogControlRevision,
			StateControlRevision:         rootValue.Authority.ServiceStateControlRevision,
			StateSummaryDigest:           rootValue.Authority.ServiceStateSummaryDigest,
		}
		if err := pins.RecoverServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
			return work, nil, incomplete, recoveryStoreError{err}
		}
		for _, domain := range rootValue.Authority.Upstream.Domains {
			if work >= budget {
				return work, nil, incomplete, ErrLimit
			}
			work++
			if err := pins.PinPartitionedExtractionRun(ctx, domain.RunID, owner); err != nil {
				return work, nil, incomplete, recoveryStoreError{err}
			}
		}
		references = append(references, reference)
	}
	if len(validated) != len(protected) {
		auditErr = ErrInvalid
	} else {
		for generation := range protected {
			if _, present := validated[generation]; !present {
				auditErr = ErrInvalid
				break
			}
		}
	}
	return work, references, incomplete, auditErr
}

func isDerivedRecoveryOmission(cause error) bool {
	if cause == nil {
		return false
	}
	if joined, ok := cause.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !isDerivedRecoveryOmission(child) {
				return false
			}
		}
		return true
	}
	if unwrapped := errors.Unwrap(cause); unwrapped != nil {
		return isDerivedRecoveryOmission(unwrapped)
	}
	return errors.Is(cause, ErrInvalid) || errors.Is(cause, ErrNotFound) ||
		errors.Is(cause, os.ErrNotExist) || errors.Is(cause, resolvernamespace.ErrInvalid) ||
		errors.Is(cause, resolvernamespace.ErrNotFound)
}

func addRecoveryOwner(owners map[string]struct{}, owner string) error {
	if _, present := owners[owner]; present {
		return nil
	}
	if len(owners) >= MaxRecoveryOwners {
		return ErrLimit
	}
	owners[owner] = struct{}{}
	return nil
}

func componentControlPresent(directory string) bool {
	for _, name := range []string{"current.json", "publishing.json"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil ||
			!errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

func recoveryNamespaceBases(dataDir string) []string {
	return []string{
		filepath.Join(dataDir, "relationships", "relationship-publications"),
		filepath.Join(dataDir, "relationships", RelationshipPublicationsV3Shadow),
		filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces"),
		filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings"),
		filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings"),
	}
}

func discoverRecoveryNamespaces(
	ctx context.Context,
	dataDir string,
) ([]string, int, int, error) {
	values := make(map[string]struct{}, MaxRecoveryNamespaces)
	work, invalid := 0, 0
	for _, base := range recoveryNamespaceBases(dataDir) {
		if err := ctx.Err(); err != nil {
			return nil, work, invalid, err
		}
		entries, err := boundedLifecycleDirectory(base, MaxLifecycleRepositories)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, work, invalid, err
		}
		work += len(entries)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, work, invalid, err
			}
			if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
				invalid++
				continue
			}
			values[entry.Name()] = struct{}{}
			if len(values) > MaxRecoveryNamespaces {
				return nil, work, invalid, ErrLimit
			}
		}
	}
	hashes := make([]string, 0, len(values))
	for value := range values {
		hashes = append(hashes, value)
	}
	sort.Strings(hashes)
	return hashes, work, invalid, nil
}

// repairRecoveryRelationshipDirectory preserves v3 final-marker authority,
// otherwise removes exact uncommitted controls before ordinary stage repair.
func repairRecoveryRelationshipDirectory(
	ctx context.Context,
	directory, repositoryHashValue string,
	v3 bool,
	budget int,
) (int, error) {
	work := 0
	if v3 {
		marker, markerPresent, err := readLifecycleMarkerV3(
			directory, repositoryHashValue,
		)
		if err != nil {
			return work, err
		}
		if markerPresent {
			if _, err := validateLifecycleMarkerRootV3(directory, marker); err != nil {
				return work, err
			}
			// The final marker owns its installed target or exact named stage.
			// RecoverV3 completes it before any stage repair.
			return work, nil
		}
		consumed, err := removeUncommittedPublishingTemporaryV3(
			ctx, directory, budget,
		)
		work += consumed
		if err != nil {
			return work, err
		}
	}
	_, consumed, err := repairStageDirectories(ctx, directory, budget-work)
	return work + consumed, err
}

// repairStageDirectories removes crash-only, never-authoritative build stages
// within both the per-directory repair envelope and the remaining global
// startup budget. It reports directory removal and charged work.
func repairStageDirectories(
	ctx context.Context, directory string, budget int,
) (bool, int, error) {
	if budget < 1 {
		return false, 0, ErrLimit
	}
	entries, err := boundedLifecycleDirectory(
		directory, min(MaxRepositoryRepairEntries, budget),
	)
	if errors.Is(err, os.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	work := len(entries)
	nonStages := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".stage-") {
			nonStages++
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, work, err
		}
		if len(entry.Name()) == len(".stage-") || !entry.IsDir() {
			return false, work, ErrInvalid
		}
		remaining := budget - work
		if remaining < 1 {
			return false, work, ErrLimit
		}
		deleted, complete, drainErr := drainFlatGeneration(
			filepath.Join(directory, entry.Name()), min(MaxStageRepairFiles+1, remaining),
		)
		work += deleted
		if drainErr != nil {
			return false, work, drainErr
		}
		if !complete {
			return false, work, ErrLimit
		}
	}
	if nonStages != 0 {
		return false, work, nil
	}
	if work >= budget {
		return false, work, ErrLimit
	}
	if err := os.Remove(directory); err != nil {
		return false, work, err
	}
	return true, work + 1, nil
}

func cleanupOrphanComponentNamespace(
	ctx context.Context, directory string, prefixed bool, budget int,
) (int, error) {
	if budget < 1 {
		return 0, ErrLimit
	}
	entries, err := boundedLifecycleDirectory(
		directory, min(MaxRepositoryRepairEntries, budget),
	)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	work := len(entries)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return work, err
		}
		if entry.IsDir() {
			name := entry.Name()
			if prefixed {
				name = strings.TrimPrefix(name, "generation-")
			}
			if len(name) != 64 || !validLowerHex(name) {
				return work, ErrInvalid
			}
			remaining := budget - work
			if remaining < 1 {
				return work, ErrLimit
			}
			deleted, complete, drainErr := drainFlatGeneration(
				filepath.Join(directory, entry.Name()), min(MaxStageRepairFiles+1, remaining),
			)
			work += deleted
			if drainErr != nil {
				return work, drainErr
			}
			if !complete {
				return work, ErrLimit
			}
			continue
		}
		if entry.Name() != "current.json" && entry.Name() != "publishing.json" {
			return work, ErrInvalid
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return work, infoErr
		}
		if !info.Mode().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			return work, ErrInvalid
		}
		if work >= budget {
			return work, ErrLimit
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return work, err
		}
		work++
	}
	if work >= budget {
		return work, ErrLimit
	}
	if err := os.Remove(directory); err != nil {
		return work, err
	}
	return work + 1, nil
}

func recoveryProtectedGenerations(
	directory string, entries []os.DirEntry,
) (map[string]struct{}, bool, error) {
	protected := make(map[string]struct{}, RetainedGenerations+2)
	if raw, err := readRegular(filepath.Join(directory, "current.json"), MaxRootBytes); err == nil {
		var pointer Pointer
		if decodeExact(raw, MaxRootBytes, &pointer) != nil || validatePointer(pointer) != nil {
			return nil, false, ErrInvalid
		}
		protected[pointer.GenerationDigest] = struct{}{}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if raw, err := readRegular(filepath.Join(directory, UnavailableName), MaxRootBytes); err == nil {
		var unavailable Unavailable
		if decodeExact(raw, MaxRootBytes, &unavailable) != nil ||
			validateUnavailable(unavailable, unavailable.Upstream.Repository) != nil {
			return nil, false, ErrInvalid
		}
		if unavailable.Prior != nil {
			protected[unavailable.Prior.GenerationDigest] = struct{}{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	var generations []lifecycleGeneration
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != 64 || !validLowerHex(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, false, err
		}
		generations = append(generations, lifecycleGeneration{
			name: name, generation: "sha256:" + name, modified: info.ModTime(),
		})
	}
	sort.Slice(generations, func(i, j int) bool {
		if !generations[i].modified.Equal(generations[j].modified) {
			return generations[i].modified.After(generations[j].modified)
		}
		return generations[i].name > generations[j].name
	})
	remaining := RetainedGenerations - 1
	for _, generation := range generations {
		if _, present := protected[generation.generation]; present {
			continue
		}
		if remaining == 0 {
			break
		}
		protected[generation.generation] = struct{}{}
		remaining--
	}
	incomplete := false
	for _, generation := range generations {
		if _, audited := protected[generation.generation]; !audited {
			incomplete = true
			break
		}
	}
	return protected, incomplete, nil
}

func recoveryProtectedGenerationsV3(
	directory string,
	entries []os.DirEntry,
) (map[string]string, bool, error) {
	protected := make(map[string]string, RetainedGenerationsV3+1)
	currentGeneration := ""
	if raw, err := readRegular(filepath.Join(directory, "current.json"), MaxRootBytesV3); err == nil {
		var pointer PointerV3
		if decodeExact(raw, MaxRootBytesV3, &pointer) != nil || validatePointerV3(pointer) != nil {
			return nil, false, ErrInvalid
		}
		canonical, _ := json.Marshal(pointer)
		if !slices.Equal(raw, canonical) {
			return nil, false, ErrInvalid
		}
		currentGeneration = pointer.GenerationDigest
		protected[pointer.GenerationDigest] = pointer.RootDigest
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	var generations []lifecycleGeneration
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != 64 || !validLowerHex(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, false, err
		}
		generations = append(generations, lifecycleGeneration{
			name: name, generation: "sha256:" + name, modified: info.ModTime(),
		})
	}
	sort.Slice(generations, func(i, j int) bool {
		if !generations[i].modified.Equal(generations[j].modified) {
			return generations[i].modified.After(generations[j].modified)
		}
		return generations[i].name > generations[j].name
	})
	remaining := RetainedGenerationsV3 - 1
	for _, generation := range generations {
		if _, present := protected[generation.generation]; present {
			continue
		}
		if remaining == 0 {
			break
		}
		protected[generation.generation] = ""
		remaining--
	}
	currentFound := currentGeneration == ""
	incomplete := false
	for _, generation := range generations {
		if generation.generation == currentGeneration {
			currentFound = true
		}
		if _, audited := protected[generation.generation]; !audited {
			incomplete = true
		}
	}
	if !currentFound {
		return nil, false, ErrInvalid
	}
	return protected, incomplete, nil
}

func directoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: recovery namespace directory", ErrInvalid)
	}
	return true, nil
}

func recoveryRepository(directory, hash string) (string, bool, error) {
	if raw, err := readRegular(filepath.Join(directory, UnavailableName), MaxRootBytes); err == nil {
		var value Unavailable
		if decodeExact(raw, MaxRootBytes, &value) != nil ||
			validateUnavailable(value, value.Upstream.Repository) != nil ||
			repositoryHash(value.Upstream.Repository) != hash {
			return "", false, ErrInvalid
		}
		return value.Upstream.Repository, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	for _, name := range []string{"publishing.json", "current.json"} {
		raw, err := readRegular(filepath.Join(directory, name), MaxRootBytes)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if name == "publishing.json" {
			var marker Marker
			if decodeExact(raw, MaxRootBytes, &marker) != nil || validateMarker(marker) != nil ||
				repositoryHash(marker.Pointer.Repository) != hash {
				return "", false, ErrInvalid
			}
			return marker.Pointer.Repository, false, nil
		}
		var pointer Pointer
		if decodeExact(raw, MaxRootBytes, &pointer) != nil || validatePointer(pointer) != nil ||
			repositoryHash(pointer.Repository) != hash {
			return "", false, ErrInvalid
		}
		return pointer.Repository, false, nil
	}
	return "", false, ErrInvalid
}

func recoveryRepositoryV3(directory, hash string) (string, error) {
	for _, name := range []string{"publishing.json", "current.json"} {
		raw, err := readRegular(filepath.Join(directory, name), MaxRootBytesV3)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if name == "publishing.json" {
			var marker MarkerV3
			if decodeExact(raw, MaxRootBytesV3, &marker) != nil || validateMarkerV3(marker) != nil ||
				repositoryHash(marker.Pointer.Repository) != hash {
				return "", ErrInvalid
			}
			canonical, _ := json.Marshal(marker)
			if !slices.Equal(raw, canonical) {
				return "", ErrInvalid
			}
			return marker.Pointer.Repository, nil
		}
		var pointer PointerV3
		if decodeExact(raw, MaxRootBytesV3, &pointer) != nil || validatePointerV3(pointer) != nil ||
			repositoryHash(pointer.Repository) != hash {
			return "", ErrInvalid
		}
		canonical, _ := json.Marshal(pointer)
		if !slices.Equal(raw, canonical) {
			return "", ErrInvalid
		}
		return pointer.Repository, nil
	}
	return "", ErrInvalid
}

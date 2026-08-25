package extractionpublication

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
)

const (
	MaxStageLifecycleRepositories = 4_096
	MaxStageRepositoryEntries     = 20_000
	MaxStageRecoveryWork          = 2_000_000

	StageGracePeriod   = 24 * time.Hour
	StageRetainedCount = 2

	MaxStageLifecycleCandidates    = 64
	MaxStageLifecycleDeletes       = 16
	MaxStageLifecycleStats         = 256
	MaxStageLifecycleDescriptors   = 8
	MaxStageLifecycleMetadataBytes = 1 << 20

	stageGenerationPrefix = ".stage-generation-"
	stageRestorePrefix    = ".stage-restore-"
	stageSparsePrefix     = ".stage-sparse-"

	retiredStageGenerationPrefix = ".retired-stage-generation-"
	retiredStageRestorePrefix    = ".retired-stage-restore-"
	retiredStageSparsePrefix     = ".retired-stage-sparse-"

	collectingStageGenerationPrefix = ".collecting-stage-generation-"
	collectingStageRestorePrefix    = ".collecting-stage-restore-"
	collectingStageSparsePrefix     = ".collecting-stage-sparse-"

	maxStageRootEntries   = MaxDomains*2 + 1
	maxStageSparseEntries = candidate.MaxSparseDomains*2 + 1
	maxStageNameBytes     = 255
)

type StageRecoveryReport struct {
	Repositories    int
	Retired         int
	Work            int
	Stats           int
	MetadataBytes   int64
	PeakDescriptors int
}

type StageLifecycleLimits struct {
	Candidates    int
	Deletes       int
	Stats         int
	Descriptors   int
	MetadataBytes int64
}

type StageLifecycleResult struct {
	Cursor          string
	Scanned         int
	Deleted         int
	Stats           int
	MetadataBytes   int64
	PeakDescriptors int
	More            bool
	Active          bool
}

type stageKind string

const (
	stageGeneration stageKind = "generation"
	stageRestore    stageKind = "restore"
	stageSparse     stageKind = "sparse"
)

type stageState uint8

const (
	stageRaw stageState = iota + 1
	stageRetired
	stageCollecting
)

type stageCandidate struct {
	name     string
	kind     stageKind
	state    stageState
	modified time.Time
	expected os.FileInfo
}

type stageRepository struct {
	key           string
	name          string
	sparse        bool
	rootExpected  os.FileInfo
	scopeExpected os.FileInfo
}

type stageDomainTree struct {
	name     string
	expected os.FileInfo
	files    []string
}

type stageTree struct {
	expected os.FileInfo
	files    []string
	domains  []stageDomainTree
}

// RecoverStages is the startup-only raw-stage retirement seam. Every fully
// validated raw stage is renamed before workers exist, so a hard-death
// residue is no longer scanner-visible. The frozen 24-hour/newest-two policy
// applies to the separate retired namespace. An eligible stage is first
// renamed to collecting, after which every bounded turn resumes it regardless
// of directory mtime changes caused by partial draining.
func RecoverStages(ctx context.Context, root string) (StageRecoveryReport, error) {
	return recoverStages(ctx, root)
}

func recoverStages(
	ctx context.Context, root string,
) (report StageRecoveryReport, resultErr error) {
	budget := newRecoveryStageBudget()
	defer func() {
		report.Work = budget.work
		report.Stats = budget.stats
		report.MetadataBytes = budget.metadataBytes
		report.PeakDescriptors = budget.peakDescriptors
	}()
	if ctx == nil || !validStageRootPath(root) {
		return report, invalid("stage recovery input")
	}

	// Phase one is the startup safety boundary. Validate each owned tree in
	// full before renaming raw state. If the aggregate cap is exhausted here,
	// startup refuses; it never starts workers with an uninspected raw suffix.
	repositories, err := stageRepositories(root, false, budget)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	sparseRepositories, sparseErr := stageRepositories(root, true, budget)
	if sparseErr != nil && !errors.Is(sparseErr, os.ErrNotExist) {
		return report, sparseErr
	}
	repositories = append(repositories, sparseRepositories...)
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		authority, err := openStageRepository(root, repository, budget)
		if err != nil {
			return report, err
		}
		stages, err := stageCandidates(ctx, authority, repository.sparse, budget)
		if err == nil && len(stages) > 0 {
			report.Repositories++
		}
		for index := range stages {
			var tree stageTree
			tree, err = inspectStage(ctx, authority, stages[index], budget, true)
			stages[index].expected = tree.expected
			if err != nil {
				break
			}
		}
		if err == nil {
			err = preflightRawRetirement(authority, stages, budget)
		}
		rawCount := len(stagesInState(stages, stageRaw))
		if err == nil && rawCount > 0 &&
			(!budget.canSpendWork(rawCount*2+2) || budget.statsRemaining() < rawCount+1) {
			err = ErrLimit
		}
		changed := false
		if err == nil {
			for index := range stages {
				if stages[index].state != stageRaw {
					continue
				}
				destination := transitionStageName(stages[index], stageRetired)
				if err = renameStage(
					authority, stages[index].name, destination,
					stages[index].expected, budget,
				); err != nil {
					break
				}
				stages[index].name = destination
				stages[index].state = stageRetired
				report.Retired++
				changed = true
			}
		}
		if changed {
			err = errors.Join(err, syncStageRoot(authority.root, budget))
		}
		closeErr := authority.close()
		if err != nil || closeErr != nil {
			return report, errors.Join(err, closeErr)
		}
	}
	return report, nil
}

// SweepStageLifecycle examines one repository namespace under the complete
// controller envelope. Raw stages may belong to live workers and are never
// mutated. Retired stages honor the frozen grace/count policy; collecting
// stages always resume. Cursor state carries both same-repository resumption
// and whether an earlier repository contained retained/live partial state.
func SweepStageLifecycle(
	ctx context.Context,
	root string,
	now time.Time,
	cursor string,
	limits StageLifecycleLimits,
) (result StageLifecycleResult, resultErr error) {
	budget, err := newLifecycleStageBudget(limits)
	if err != nil {
		return result, err
	}
	defer func() { applyStageBudget(&result, budget) }()
	if ctx == nil || now.IsZero() || !validStageRootPath(root) {
		return result, invalid("stage lifecycle input")
	}
	state, err := parseStageCursor(cursor)
	if err != nil {
		return result, err
	}
	sparsePhase := strings.HasPrefix(state.key, "s/")
	repositories, err := stageRepositories(root, sparsePhase, budget)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	position := selectStageRepository(repositories, state)
	if position < 0 {
		result.Active = state.active
		if !sparsePhase {
			result.Cursor = encodeStageCursor(false, result.Active, "s/")
			result.More = true
		}
		return result, nil
	}
	repository := repositories[position]
	authority, err := openStageRepository(root, repository, budget)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, authority.close()) }()
	stages, err := stageCandidates(ctx, authority, repository.sparse, budget)
	if err != nil {
		return result, err
	}
	if err := statStageCandidates(authority, stages, budget); err != nil {
		return result, err
	}

	collecting := stagesInState(stages, stageCollecting)
	eligible, _ := eligibleRetiredStages(stages, now)
	raw := stagesInState(stages, stageRaw)
	localActive := len(raw) > 0
	result.Active = state.active || localActive
	actionable := len(collecting) + len(eligible)
	if actionable > 0 {
		var selected stageCandidate
		if len(collecting) > 0 {
			sort.Slice(collecting, func(i, j int) bool { return collecting[i].name < collecting[j].name })
			selected = collecting[0]
		} else {
			selected = eligible[0]
			// Destination/source fences plus the parent sync are one durable
			// transition. Refuse before rename if all three stats do not fit.
			if budget.statsRemaining() < 3 {
				return result, ErrLimit
			}
			destination := transitionStageName(selected, stageCollecting)
			if err := requireStageDestinationAbsent(authority, destination, budget); err != nil {
				return result, err
			}
			renameErr := renameStage(
				authority, selected.name, destination, selected.expected, budget,
			)
			syncErr := syncStageRoot(authority.root, budget)
			if renameErr != nil || syncErr != nil {
				return result, errors.Join(renameErr, syncErr)
			}
			selected.name = destination
			selected.state = stageCollecting
		}
		complete, err := drainStage(ctx, authority, selected, budget)
		if err != nil {
			return result, err
		}
		if !complete || actionable > 1 {
			result.Cursor = encodeStageCursor(true, result.Active, repository.key)
			result.More = true
			return result, nil
		}
	}
	if position+1 < len(repositories) {
		result.Cursor = encodeStageCursor(false, result.Active, repository.key)
		result.More = true
		return result, nil
	}
	if !repository.sparse {
		result.Cursor = encodeStageCursor(false, result.Active, "s/")
		result.More = true
		return result, nil
	}
	// A complete final repository closes the pass explicitly. The empty cursor
	// is the next hourly pass boundary, not an unconditional wrap/backlog.
	return result, nil
}

func eligibleRetiredStages(
	stages []stageCandidate, now time.Time,
) (eligible, retained []stageCandidate) {
	for _, kind := range []stageKind{stageGeneration, stageRestore, stageSparse} {
		var group []stageCandidate
		for _, stage := range stages {
			if stage.state == stageRetired && stage.kind == kind {
				group = append(group, stage)
			}
		}
		sort.Slice(group, func(i, j int) bool {
			if !group[i].modified.Equal(group[j].modified) {
				return group[i].modified.After(group[j].modified)
			}
			return group[i].name > group[j].name
		})
		for index, stage := range group {
			if index >= StageRetainedCount || !stage.modified.After(now.Add(-StageGracePeriod)) {
				eligible = append(eligible, stage)
			} else {
				retained = append(retained, stage)
			}
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		if !eligible[i].modified.Equal(eligible[j].modified) {
			return eligible[i].modified.Before(eligible[j].modified)
		}
		return eligible[i].name < eligible[j].name
	})
	return eligible, retained
}

func stagesInState(stages []stageCandidate, state stageState) []stageCandidate {
	result := make([]stageCandidate, 0, len(stages))
	for _, stage := range stages {
		if stage.state == state {
			result = append(result, stage)
		}
	}
	return result
}

func preflightRawRetirement(
	authority *stageRootAuthority, stages []stageCandidate, budget *stageBudget,
) error {
	for _, stage := range stages {
		if stage.state != stageRaw {
			continue
		}
		if err := requireStageDestinationAbsent(
			authority, transitionStageName(stage, stageRetired), budget,
		); err != nil {
			return err
		}
	}
	return nil
}

func requireStageDestinationAbsent(
	authority *stageRootAuthority, name string, budget *stageBudget,
) error {
	_, err := budget.rootLstat(authority.root, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return invalid("stage transition collision")
}

func renameStage(
	authority *stageRootAuthority,
	oldName, newName string,
	expected os.FileInfo,
	budget *stageBudget,
) error {
	current, err := budget.rootLstat(authority.root, oldName)
	if err != nil || !sameStageDirectory(expected, current) {
		return errors.Join(err, invalid("stage changed before transition"))
	}
	return budget.rootRename(authority.root, oldName, newName)
}

func stageRepositories(
	rootPath string, sparse bool, budget *stageBudget,
) ([]stageRepository, error) {
	root, err := openStableStageRoot(rootPath, budget)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.close() }()
	if sparse {
		sparseRoot, _, err := openChildStageRoot(root, "candidates", budget)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		sparseEntries, readErr := readStageDirectory(
			sparseRoot.root, MaxStageLifecycleRepositories, budget,
		)
		closeErr := sparseRoot.close()
		if readErr != nil || closeErr != nil {
			return nil, errors.Join(readErr, closeErr)
		}
		repositories := make([]stageRepository, 0, len(sparseEntries))
		for _, entry := range sparseEntries {
			if !validLowerHexName(entry.Name(), 64) {
				continue
			}
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil, invalid("sparse stage repository namespace is special")
			}
			repositories = append(repositories, stageRepository{
				key: "s/" + entry.Name(), name: entry.Name(), sparse: true,
				rootExpected: root.info, scopeExpected: sparseRoot.info,
			})
		}
		sort.Slice(repositories, func(i, j int) bool { return repositories[i].key < repositories[j].key })
		return repositories, nil
	}
	entries, err := readStageDirectory(root.root, MaxStageLifecycleRepositories+1, budget)
	if err != nil {
		return nil, err
	}
	repositories := make([]stageRepository, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "candidates" || !validLowerHexName(entry.Name(), 64) {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, invalid("stage repository namespace is special")
		}
		repositories = append(repositories, stageRepository{
			key: "g/" + entry.Name(), name: entry.Name(),
			rootExpected: root.info, scopeExpected: root.info,
		})
		if len(repositories) > MaxStageLifecycleRepositories {
			return nil, ErrLimit
		}
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].key < repositories[j].key })
	return repositories, nil
}

func openStageRepository(
	rootPath string, repository stageRepository, budget *stageBudget,
) (*stageRootAuthority, error) {
	root, err := openStableStageRoot(rootPath, budget)
	if err != nil {
		return nil, err
	}
	if !sameStageDirectory(root.info, repository.rootExpected) {
		_ = root.close()
		return nil, invalid("stage root changed after inventory")
	}
	parent := root
	if repository.sparse {
		parent, _, err = openChildStageRoot(root, "candidates", budget)
		_ = root.close()
		if err != nil {
			return nil, err
		}
		if !sameStageDirectory(parent.info, repository.scopeExpected) {
			_ = parent.close()
			return nil, invalid("sparse stage scope changed after inventory")
		}
	}
	repositoryRoot, _, err := openChildStageRoot(parent, repository.name, budget)
	_ = parent.close()
	if err != nil {
		return nil, err
	}
	return repositoryRoot, nil
}

func stageCandidates(
	ctx context.Context,
	authority *stageRootAuthority,
	sparse bool,
	budget *stageBudget,
) ([]stageCandidate, error) {
	entries, err := readStageDirectory(authority.root, MaxStageRepositoryEntries, budget)
	if err != nil {
		return nil, err
	}
	result := make([]stageCandidate, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stage, ok := parseStageName(entry.Name())
		if !ok {
			if hasOwnedStagePrefix(entry.Name()) {
				return nil, invalid("stage directory name")
			}
			continue
		}
		if sparse != (stage.kind == stageSparse) {
			return nil, invalid("stage is in the wrong repository namespace")
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, invalid("stage directory is special")
		}
		if err := budget.takeCandidate(); err != nil {
			return nil, err
		}
		result = append(result, stage)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func statStageCandidates(
	authority *stageRootAuthority, stages []stageCandidate, budget *stageBudget,
) error {
	for index := range stages {
		if stages[index].state != stageRetired {
			continue
		}
		info, err := budget.rootLstat(authority.root, stages[index].name)
		if err != nil || !realStageDirectory(info) {
			return errors.Join(err, invalid("stage changed during inventory"))
		}
		stages[index].modified = info.ModTime()
		stages[index].expected = info
	}
	return nil
}

func parseStageName(name string) (stageCandidate, bool) {
	for _, current := range []struct {
		prefix string
		kind   stageKind
		state  stageState
	}{
		{collectingStageGenerationPrefix, stageGeneration, stageCollecting},
		{collectingStageRestorePrefix, stageRestore, stageCollecting},
		{collectingStageSparsePrefix, stageSparse, stageCollecting},
		{retiredStageGenerationPrefix, stageGeneration, stageRetired},
		{retiredStageRestorePrefix, stageRestore, stageRetired},
		{retiredStageSparsePrefix, stageSparse, stageRetired},
		{stageGenerationPrefix, stageGeneration, stageRaw},
		{stageRestorePrefix, stageRestore, stageRaw},
		{stageSparsePrefix, stageSparse, stageRaw},
	} {
		if strings.HasPrefix(name, current.prefix) &&
			validStageSuffix(strings.TrimPrefix(name, current.prefix)) {
			return stageCandidate{name: name, kind: current.kind, state: current.state}, true
		}
	}
	return stageCandidate{}, false
}

func hasOwnedStagePrefix(name string) bool {
	for _, prefix := range []string{
		stageGenerationPrefix, stageRestorePrefix, stageSparsePrefix,
		retiredStageGenerationPrefix, retiredStageRestorePrefix, retiredStageSparsePrefix,
		collectingStageGenerationPrefix, collectingStageRestorePrefix, collectingStageSparsePrefix,
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func validStageSuffix(value string) bool {
	if validLowerHexName(value, 64) {
		return true
	}
	// Go 1.26's MkdirTemp suffix is a canonical uint32 decimal. The lower-hex
	// shape is retained for compatibility without widening stage ownership to
	// arbitrary names.
	if value == "" || len(value) > 10 || len(value) > 1 && value[0] == '0' {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 32)
	return err == nil
}

func transitionStageName(stage stageCandidate, state stageState) string {
	oldPrefix := prefixForState(stage.kind, stage.state)
	newPrefix := prefixForState(stage.kind, state)
	return newPrefix + strings.TrimPrefix(stage.name, oldPrefix)
}

func prefixForState(kind stageKind, state stageState) string {
	switch kind {
	case stageGeneration:
		switch state {
		case stageRaw:
			return stageGenerationPrefix
		case stageRetired:
			return retiredStageGenerationPrefix
		case stageCollecting:
			return collectingStageGenerationPrefix
		}
	case stageRestore:
		switch state {
		case stageRaw:
			return stageRestorePrefix
		case stageRetired:
			return retiredStageRestorePrefix
		case stageCollecting:
			return collectingStageRestorePrefix
		}
	case stageSparse:
		switch state {
		case stageRaw:
			return stageSparsePrefix
		case stageRetired:
			return retiredStageSparsePrefix
		case stageCollecting:
			return collectingStageSparsePrefix
		}
	}
	return ""
}

func inspectStage(
	ctx context.Context,
	repository *stageRootAuthority,
	stage stageCandidate,
	budget *stageBudget,
	full bool,
) (tree stageTree, resultErr error) {
	stageRoot, expected, err := openChildStageRoot(repository, stage.name, budget)
	if err != nil {
		return stageTree{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, stageRoot.close()) }()
	tree, err = inspectOpenStage(ctx, stageRoot, stage, budget, full)
	tree.expected = expected
	return tree, err
}

func inspectOpenStage(
	ctx context.Context,
	stageRoot *stageRootAuthority,
	stage stageCandidate,
	budget *stageBudget,
	full bool,
) (stageTree, error) {
	tree := stageTree{expected: stageRoot.info}
	entryLimit := maxStageRootEntries
	if stage.kind == stageSparse {
		entryLimit = maxStageSparseEntries
	}
	entries, err := readStageDirectory(stageRoot.root, entryLimit, budget)
	if err != nil {
		return stageTree{}, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return stageTree{}, err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return stageTree{}, invalid("stage contains a symlink")
		}
		if entry.IsDir() {
			if stage.kind == stageSparse || !validLowerHexName(entry.Name(), 64) {
				return stageTree{}, invalid("stage contains an unknown directory")
			}
			var domain stageDomainTree
			if full {
				domainRoot, domainExpected, err := openChildStageRoot(stageRoot, entry.Name(), budget)
				if err != nil {
					return stageTree{}, err
				}
				inspectErr := error(nil)
				domain, inspectErr = inspectStageDomain(
					ctx, domainRoot, entry.Name(), domainExpected, stage.kind, budget, true,
				)
				closeErr := domainRoot.close()
				if inspectErr != nil || closeErr != nil {
					return stageTree{}, errors.Join(inspectErr, closeErr)
				}
			} else {
				var err error
				domain, err = inspectStageDomainReadOnly(
					ctx, stageRoot.root, entry.Name(), stage.kind, budget,
				)
				if err != nil {
					return stageTree{}, err
				}
			}
			tree.domains = append(tree.domains, domain)
			continue
		}
		if entry.Type()&os.ModeType != 0 || !validStageFile(stage.kind, entry.Name()) {
			return stageTree{}, invalid("stage contains an unknown or special file")
		}
		if full {
			if err := requireStageRegular(stageRoot.root, entry.Name(), budget); err != nil {
				return stageTree{}, err
			}
		}
		tree.files = append(tree.files, entry.Name())
	}
	return tree, nil
}

func inspectStageDomainReadOnly(
	ctx context.Context,
	root *os.Root,
	name string,
	kind stageKind,
	budget *stageBudget,
) (stageDomainTree, error) {
	directory, err := budget.rootOpen(root, name)
	if err != nil {
		return stageDomainTree{}, err
	}
	limit := 1
	if kind == stageRestore {
		limit = candidate.MaxDomainResultPartitions + 2
	}
	entries, readErr := readOpenStageDirectory(directory, limit, budget)
	closeErr := budget.closeFile(directory)
	if readErr != nil || closeErr != nil {
		return stageDomainTree{}, errors.Join(readErr, closeErr)
	}
	return validateStageDomainEntries(ctx, entries, name, nil, kind, root, budget, false)
}

func inspectStageDomain(
	ctx context.Context,
	domainRoot *stageRootAuthority,
	name string,
	expected os.FileInfo,
	kind stageKind,
	budget *stageBudget,
	full bool,
) (stageDomainTree, error) {
	limit := 1
	if kind == stageRestore {
		limit = candidate.MaxDomainResultPartitions + 2
	}
	entries, err := readStageDirectory(domainRoot.root, limit, budget)
	if err != nil {
		return stageDomainTree{}, err
	}
	return validateStageDomainEntries(
		ctx, entries, name, expected, kind, domainRoot.root, budget, full,
	)
}

func validateStageDomainEntries(
	ctx context.Context,
	entries []os.DirEntry,
	name string,
	expected os.FileInfo,
	kind stageKind,
	root *os.Root,
	budget *stageBudget,
	full bool,
) (stageDomainTree, error) {
	domain := stageDomainTree{name: name, expected: expected, files: make([]string, 0, len(entries))}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return stageDomainTree{}, err
		}
		if entry.IsDir() || entry.Type()&os.ModeType != 0 ||
			!validStageDomainFile(kind, entry.Name()) {
			return stageDomainTree{}, invalid("stage domain contains an unknown or special entry")
		}
		if full {
			if err := requireStageRegular(root, entry.Name(), budget); err != nil {
				return stageDomainTree{}, err
			}
		}
		domain.files = append(domain.files, entry.Name())
	}
	return domain, nil
}

func validStageFile(kind stageKind, name string) bool {
	if kind == stageSparse {
		return validSparseStageFile(name)
	}
	return validStageRootFile(name)
}

func validSparseStageFile(name string) bool {
	if name == candidate.SparseRootFileName {
		return true
	}
	for _, shape := range []struct {
		prefix string
		suffix string
	}{
		{"candidate-partition-domain-", ".json"},
		{"candidate-partition-typed-scope-", ".bin"},
	} {
		if len(name) != len(shape.prefix)+3+len(shape.suffix) ||
			!strings.HasPrefix(name, shape.prefix) || !strings.HasSuffix(name, shape.suffix) {
			continue
		}
		value, err := strconv.Atoi(name[len(shape.prefix) : len(shape.prefix)+3])
		return err == nil && value >= 0 && value < candidate.MaxSparseDomains
	}
	return false
}

func validStageRootFile(name string) bool {
	if name == generationName() {
		return true
	}
	if len(name) != len("plan-000.json") || !strings.HasPrefix(name, "plan-") ||
		!strings.HasSuffix(name, ".json") {
		return false
	}
	value, err := strconv.Atoi(name[len("plan-"):len("plan-000")])
	return err == nil && value >= 0 && value < MaxDomains
}

func validStageDomainFile(kind stageKind, name string) bool {
	if name == completionName() {
		return true
	}
	if kind != stageRestore {
		return false
	}
	if name == rootName() {
		return true
	}
	if len(name) != len("result-00000.json") || !strings.HasPrefix(name, "result-") ||
		!strings.HasSuffix(name, ".json") {
		return false
	}
	value, err := strconv.Atoi(name[len("result-"):len("result-00000")])
	return err == nil && value >= 0 && value < candidate.MaxDomainResultPartitions
}

func drainStage(
	ctx context.Context,
	repository *stageRootAuthority,
	stage stageCandidate,
	budget *stageBudget,
) (bool, error) {
	stageRoot, _, err := openChildStageRoot(repository, stage.name, budget)
	if err != nil {
		return false, err
	}
	tree, err := inspectOpenStage(ctx, stageRoot, stage, budget, false)
	if err != nil {
		_ = stageRoot.close()
		return false, err
	}
	for _, domain := range tree.domains {
		neededStats := 9 + min(len(domain.files), budget.deleteRemaining())
		if budget.deleteRemaining() == 0 || budget.statsRemaining() < neededStats {
			_ = stageRoot.close()
			return false, nil
		}
		complete, err := drainStageDomain(ctx, stageRoot, domain, stage.kind, budget)
		if err != nil || !complete {
			_ = stageRoot.close()
			return false, err
		}
	}
	neededStats := 4 + min(len(tree.files), budget.deleteRemaining())
	if budget.deleteRemaining() == 0 || budget.statsRemaining() < neededStats {
		_ = stageRoot.close()
		return false, nil
	}
	changed := false
	syncChanges := func(base error) error {
		if changed {
			base = errors.Join(base, syncStageRoot(stageRoot.root, budget))
			changed = false
		}
		return base
	}
	for _, name := range tree.files {
		if err := ctx.Err(); err != nil {
			err = syncChanges(err)
			return false, errors.Join(err, stageRoot.close())
		}
		if budget.deleteRemaining() == 0 {
			err = syncChanges(nil)
			return false, errors.Join(err, stageRoot.close())
		}
		if err := removeStageRegular(stageRoot.root, name, budget); err != nil {
			err = syncChanges(err)
			return false, errors.Join(err, stageRoot.close())
		}
		changed = true
	}
	if err := syncChanges(nil); err != nil {
		_ = stageRoot.close()
		return false, err
	}
	empty, err := stageDirectoryEmpty(stageRoot.root, budget)
	if err != nil || !empty {
		_ = stageRoot.close()
		return false, err
	}
	expected := tree.expected
	if err := stageRoot.close(); err != nil {
		return false, err
	}
	current, err := budget.rootLstat(repository.root, stage.name)
	if err != nil || !sameStageDirectory(expected, current) {
		return false, errors.Join(err, invalid("collecting stage changed before removal"))
	}
	if budget.deleteRemaining() == 0 {
		return false, nil
	}
	// The final directory removal is not complete until its parent is durable.
	removeErr := budget.rootRemove(repository.root, stage.name)
	syncErr := syncStageRoot(repository.root, budget)
	if removeErr != nil || syncErr != nil {
		return false, errors.Join(removeErr, syncErr)
	}
	return true, nil
}

func drainStageDomain(
	ctx context.Context,
	stageRoot *stageRootAuthority,
	domain stageDomainTree,
	kind stageKind,
	budget *stageBudget,
) (bool, error) {
	domainRoot, current, err := openChildStageRoot(stageRoot, domain.name, budget)
	if err != nil {
		if domainRoot != nil {
			_ = domainRoot.close()
		}
		return false, err
	}
	domain, err = inspectStageDomain(
		ctx, domainRoot, domain.name, current, kind, budget, false,
	)
	if err != nil {
		_ = domainRoot.close()
		return false, err
	}
	neededStats := 4 + min(len(domain.files), budget.deleteRemaining())
	if budget.deleteRemaining() == 0 || budget.statsRemaining() < neededStats {
		_ = domainRoot.close()
		return false, nil
	}
	changed := false
	syncChanges := func(base error) error {
		if changed {
			base = errors.Join(base, syncStageRoot(domainRoot.root, budget))
			changed = false
		}
		return base
	}
	for _, name := range domain.files {
		if err := ctx.Err(); err != nil {
			err = syncChanges(err)
			return false, errors.Join(err, domainRoot.close())
		}
		if budget.deleteRemaining() == 0 {
			err = syncChanges(nil)
			return false, errors.Join(err, domainRoot.close())
		}
		if err := removeStageRegular(domainRoot.root, name, budget); err != nil {
			err = syncChanges(err)
			return false, errors.Join(err, domainRoot.close())
		}
		changed = true
	}
	if err := syncChanges(nil); err != nil {
		_ = domainRoot.close()
		return false, err
	}
	empty, err := stageDirectoryEmpty(domainRoot.root, budget)
	if err != nil || !empty {
		_ = domainRoot.close()
		return false, err
	}
	if err := domainRoot.close(); err != nil {
		return false, err
	}
	current, err = budget.rootLstat(stageRoot.root, domain.name)
	if err != nil || !sameStageDirectory(domain.expected, current) {
		return false, errors.Join(err, invalid("stage domain changed before removal"))
	}
	if budget.deleteRemaining() == 0 {
		return false, nil
	}
	removeErr := budget.rootRemove(stageRoot.root, domain.name)
	syncErr := syncStageRoot(stageRoot.root, budget)
	if removeErr != nil || syncErr != nil {
		return false, errors.Join(removeErr, syncErr)
	}
	return true, nil
}

func removeStageRegular(root *os.Root, name string, budget *stageBudget) error {
	info, err := budget.rootLstat(root, name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, invalid("stage file changed before removal"))
	}
	return budget.rootRemove(root, name)
}

func requireStageRegular(root *os.Root, name string, budget *stageBudget) error {
	info, err := budget.rootLstat(root, name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, invalid("stage contains a special file"))
	}
	return nil
}

func validLowerHexName(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, current := range value {
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return true
}

type stageCursor struct {
	key    string
	resume bool
	active bool
}

func parseStageCursor(cursor string) (stageCursor, error) {
	if cursor == "" {
		return stageCursor{}, nil
	}
	if len(cursor) != len("a0:s/") && len(cursor) != len("a0:g/")+64 {
		return stageCursor{}, invalid("stage lifecycle cursor")
	}
	prefix, key := cursor[:3], cursor[3:]
	if (prefix != "a0:" && prefix != "a1:" && prefix != "r0:" && prefix != "r1:") ||
		(key != "s/" && (len(key) != len("g/")+64 ||
			(key[:2] != "g/" && key[:2] != "s/") || !validLowerHexName(key[2:], 64))) {
		return stageCursor{}, invalid("stage lifecycle cursor")
	}
	if key == "s/" && prefix[0] == 'r' {
		return stageCursor{}, invalid("stage lifecycle cursor")
	}
	return stageCursor{
		key: key, resume: prefix[0] == 'r', active: prefix[1] == '1',
	}, nil
}

func encodeStageCursor(resume, active bool, key string) string {
	prefix := "a0:"
	if resume {
		prefix = "r0:"
	}
	if active {
		prefix = prefix[:1] + "1:"
	}
	return prefix + key
}

func selectStageRepository(repositories []stageRepository, cursor stageCursor) int {
	position := sort.Search(len(repositories), func(index int) bool {
		if cursor.resume {
			return repositories[index].key >= cursor.key
		}
		return repositories[index].key > cursor.key
	})
	if position == len(repositories) {
		return -1
	}
	return position
}

func validStageRootPath(root string) bool {
	return filepath.IsAbs(root) && root != string(filepath.Separator)
}

type stageRootAuthority struct {
	root   *os.Root
	info   os.FileInfo
	budget *stageBudget
	closed bool
}

func openStableStageRoot(path string, budget *stageBudget) (*stageRootAuthority, error) {
	before, err := budget.pathLstat(path)
	if err != nil {
		return nil, err
	}
	if !realStageDirectory(before) {
		return nil, invalid("stage root is not a real directory")
	}
	root, err := budget.openPathRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := budget.rootOpen(root, ".")
	if err != nil {
		_ = budget.closeRoot(root)
		return nil, err
	}
	after, statErr := budget.fileStat(opened)
	closeErr := budget.closeFile(opened)
	if statErr != nil || closeErr != nil || !sameStageDirectory(before, after) {
		_ = budget.closeRoot(root)
		return nil, errors.Join(statErr, closeErr, invalid("stage root changed while opening"))
	}
	return &stageRootAuthority{root: root, info: after, budget: budget}, nil
}

func openChildStageRoot(
	parent *stageRootAuthority, name string, budget *stageBudget,
) (*stageRootAuthority, os.FileInfo, error) {
	before, err := budget.rootLstat(parent.root, name)
	if err != nil {
		return nil, nil, err
	}
	if !realStageDirectory(before) {
		return nil, nil, invalid("stage child is not a real directory")
	}
	root, err := budget.openChildRoot(parent.root, name)
	if err != nil {
		return nil, nil, err
	}
	opened, err := budget.rootOpen(root, ".")
	if err != nil {
		_ = budget.closeRoot(root)
		return nil, nil, err
	}
	after, statErr := budget.fileStat(opened)
	closeErr := budget.closeFile(opened)
	if statErr != nil || closeErr != nil || !sameStageDirectory(before, after) {
		_ = budget.closeRoot(root)
		return nil, nil, errors.Join(statErr, closeErr, invalid("stage child changed while opening"))
	}
	return &stageRootAuthority{root: root, info: after, budget: budget}, after, nil
}

func (authority *stageRootAuthority) close() error {
	if authority == nil || authority.closed {
		return nil
	}
	authority.closed = true
	return authority.budget.closeRoot(authority.root)
}

func realStageDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func sameStageDirectory(left, right os.FileInfo) bool {
	return realStageDirectory(left) && realStageDirectory(right) && os.SameFile(left, right)
}

func readStageDirectory(
	root *os.Root, limit int, budget *stageBudget,
) ([]os.DirEntry, error) {
	if limit < 1 {
		return nil, ErrLimit
	}
	directory, err := budget.rootOpen(root, ".")
	if err != nil {
		return nil, err
	}
	entries, readErr := readOpenStageDirectory(directory, limit, budget)
	closeErr := budget.closeFile(directory)
	return entries, errors.Join(readErr, closeErr)
}

func readOpenStageDirectory(
	directory *os.File, limit int, budget *stageBudget,
) ([]os.DirEntry, error) {
	entries := make([]os.DirEntry, 0, min(limit, 256))
	for {
		entry, readErr := budget.readDirectoryEntry(directory)
		if entry != nil {
			entries = append(entries, entry)
			if len(entries) > limit {
				return nil, ErrLimit
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func stageDirectoryEmpty(root *os.Root, budget *stageBudget) (bool, error) {
	directory, err := budget.rootOpen(root, ".")
	if err != nil {
		return false, err
	}
	entry, readErr := budget.readDirectoryEntry(directory)
	closeErr := budget.closeFile(directory)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return false, closeErr
	}
	return entry == nil, nil
}

func syncStageRoot(root *os.Root, budget *stageBudget) error {
	directory, err := budget.rootOpen(root, ".")
	if err != nil {
		return err
	}
	syncErr := budget.fileSync(directory)
	return errors.Join(syncErr, budget.closeFile(directory))
}

type stageBudget struct {
	candidateLimit  int
	deleteLimit     int
	statLimit       int
	descriptorLimit int
	metadataLimit   int64
	workLimit       int

	candidates      int
	deleted         int
	stats           int
	descriptors     int
	peakDescriptors int
	metadataBytes   int64
	work            int
}

func newRecoveryStageBudget() *stageBudget {
	return &stageBudget{
		candidateLimit:  MaxStageRecoveryWork,
		deleteLimit:     MaxStageRecoveryWork,
		statLimit:       MaxStageRecoveryWork,
		descriptorLimit: MaxStageLifecycleDescriptors,
		metadataLimit:   int64(MaxStageRecoveryWork) * maxStageNameBytes,
		workLimit:       MaxStageRecoveryWork,
	}
}

func newLifecycleStageBudget(limits StageLifecycleLimits) (*stageBudget, error) {
	if limits.Candidates < 1 || limits.Candidates > MaxStageLifecycleCandidates ||
		limits.Deletes < 1 || limits.Deletes > MaxStageLifecycleDeletes ||
		limits.Stats < 1 || limits.Stats > MaxStageLifecycleStats ||
		limits.Descriptors < 1 || limits.Descriptors > MaxStageLifecycleDescriptors ||
		limits.MetadataBytes < 1 || limits.MetadataBytes > MaxStageLifecycleMetadataBytes {
		return nil, invalid("stage lifecycle limits")
	}
	return &stageBudget{
		candidateLimit:  limits.Candidates,
		deleteLimit:     limits.Deletes,
		statLimit:       limits.Stats,
		descriptorLimit: limits.Descriptors,
		metadataLimit:   limits.MetadataBytes,
	}, nil
}

func applyStageBudget(result *StageLifecycleResult, budget *stageBudget) {
	result.Scanned = budget.candidates
	result.Deleted = budget.deleted
	result.Stats = budget.stats
	result.MetadataBytes = budget.metadataBytes
	result.PeakDescriptors = budget.peakDescriptors
}

func (budget *stageBudget) takeWork() error {
	if budget.workLimit > 0 && budget.work >= budget.workLimit {
		return ErrLimit
	}
	budget.work++
	return nil
}

func (budget *stageBudget) takeCandidate() error {
	if budget.candidates >= budget.candidateLimit {
		return ErrLimit
	}
	budget.candidates++
	return nil
}

func (budget *stageBudget) deleteRemaining() int {
	return budget.deleteLimit - budget.deleted
}

func (budget *stageBudget) statsRemaining() int {
	return budget.statLimit - budget.stats
}

func (budget *stageBudget) canSpendWork(count int) bool {
	return budget.workLimit == 0 || count >= 0 && count <= budget.workLimit-budget.work
}

func (budget *stageBudget) pathLstat(path string) (os.FileInfo, error) {
	if budget.stats >= budget.statLimit {
		return nil, ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return nil, err
	}
	budget.stats++
	return os.Lstat(path)
}

func (budget *stageBudget) rootLstat(root *os.Root, name string) (os.FileInfo, error) {
	if budget.stats >= budget.statLimit {
		return nil, ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return nil, err
	}
	budget.stats++
	return root.Lstat(name)
}

func (budget *stageBudget) fileStat(file *os.File) (os.FileInfo, error) {
	if budget.stats >= budget.statLimit {
		return nil, ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return nil, err
	}
	budget.stats++
	return file.Stat()
}

func (budget *stageBudget) openPathRoot(path string) (*os.Root, error) {
	if budget.descriptors >= budget.descriptorLimit || budget.stats >= budget.statLimit {
		return nil, ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return nil, err
	}
	budget.stats++
	root, err := os.OpenRoot(path)
	if err == nil {
		budget.openedDescriptor()
	}
	return root, err
}

func (budget *stageBudget) openChildRoot(root *os.Root, name string) (*os.Root, error) {
	if budget.descriptors >= budget.descriptorLimit || budget.stats >= budget.statLimit {
		return nil, ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return nil, err
	}
	budget.stats++
	child, err := root.OpenRoot(name)
	if err == nil {
		budget.openedDescriptor()
	}
	return child, err
}

func (budget *stageBudget) rootOpen(root *os.Root, name string) (*os.File, error) {
	if budget.descriptors >= budget.descriptorLimit || budget.stats >= budget.statLimit {
		return nil, ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return nil, err
	}
	budget.stats++
	file, err := root.Open(name)
	if err == nil {
		budget.openedDescriptor()
	}
	return file, err
}

func (budget *stageBudget) openedDescriptor() {
	budget.descriptors++
	budget.peakDescriptors = max(budget.peakDescriptors, budget.descriptors)
}

func (budget *stageBudget) closeRoot(root *os.Root) error {
	if root == nil {
		return nil
	}
	err := root.Close()
	budget.descriptors--
	return err
}

func (budget *stageBudget) closeFile(file *os.File) error {
	if file == nil {
		return nil
	}
	err := file.Close()
	budget.descriptors--
	return err
}

func (budget *stageBudget) readDirectoryEntry(file *os.File) (os.DirEntry, error) {
	// Reserve the maximum package-supported component before issuing ReadDir,
	// so the call itself cannot cross the aggregate metadata envelope.
	if budget.metadataLimit-budget.metadataBytes < maxStageNameBytes {
		return nil, ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return nil, err
	}
	entries, err := file.ReadDir(1)
	if len(entries) == 0 {
		return nil, err
	}
	nameBytes := int64(len(entries[0].Name()))
	if nameBytes > budget.metadataLimit-budget.metadataBytes {
		return nil, ErrLimit
	}
	budget.metadataBytes += nameBytes
	return entries[0], err
}

func (budget *stageBudget) rootRename(root *os.Root, oldName, newName string) error {
	if err := budget.takeWork(); err != nil {
		return err
	}
	return root.Rename(oldName, newName)
}

func (budget *stageBudget) rootRemove(root *os.Root, name string) error {
	if budget.deleted >= budget.deleteLimit {
		return ErrLimit
	}
	if err := budget.takeWork(); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil {
		return err
	}
	budget.deleted++
	return nil
}

func (budget *stageBudget) fileSync(file *os.File) error {
	if err := budget.takeWork(); err != nil {
		return err
	}
	return file.Sync()
}

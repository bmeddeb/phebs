package observationpublication

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

const (
	InventoryPublicationRootSchemaV2    = "phebs-observation-inventory-publication-root-v2"
	InventoryPublicationMarkerSchemaV2  = "phebs-observation-inventory-publication-marker-v2"
	InventoryPublicationDirectoryV2     = "v2"
	InventoryPublicationRootNameV2      = "current.json"
	InventoryPublicationMarkerNameV2    = "publishing.json"
	InventoryPublicationSourceNameV2    = "source"
	InventoryPublicationInventoryNameV2 = "inventory"

	MaxInventoryPublicationStagesV2  = 8
	MaxInventoryPublicationEntriesV2 = MaxRepositoryGenerations + MaxInventoryPublicationStagesV2 + 2
)

// InventoryGenerationRefV2 binds the independently validated T40.3 source
// super-root and T40.4 observation inventory installed as one immutable
// generation. The small reference is lifecycle and archive authority; corpus
// bytes remain in the digest-named directory.
type InventoryGenerationRefV2 struct {
	GenerationDigest  string `json:"generation_digest"`
	Directory         string `json:"directory"`
	SourceRootDigest  string `json:"source_root_digest"`
	InventoryDigest   string `json:"inventory_digest"`
	SourceSegments    int    `json:"source_segments"`
	InventorySegments int    `json:"inventory_segments"`
	Records           int    `json:"records"`
	EncodedBytes      int64  `json:"encoded_bytes"`
	ObservationBytes  int64  `json:"observation_bytes"`
}

type InventoryPublicationRootV2 struct {
	Schema     string                    `json:"schema"`
	Repository string                    `json:"repository"`
	Current    InventoryGenerationRefV2  `json:"current"`
	Prior      *InventoryGenerationRefV2 `json:"prior,omitempty"`
}

type InventoryPublicationMarkerV2 struct {
	Schema       string                      `json:"schema"`
	Repository   string                      `json:"repository"`
	TransitionID string                      `json:"transition_id"`
	Stage        string                      `json:"stage"`
	Candidate    *InventoryGenerationRefV2   `json:"candidate,omitempty"`
	Previous     *InventoryPublicationRootV2 `json:"previous,omitempty"`
}

type InventoryPublicationTransitionV2 struct {
	Repository         string
	TransitionID       string
	Directory          string
	SourceDirectory    string
	InventoryDirectory string
}

type InventoryRecoveryReportV2 struct {
	Repositories int
	Completed    int
	Incomplete   int
}

func inventoryPublicationDirectoryV2(root, repository string) string {
	return filepath.Join(repositoryDirectory(root, repository), InventoryPublicationDirectoryV2)
}

func inventoryPublicationRootPathV2(root, repository string) string {
	return filepath.Join(inventoryPublicationDirectoryV2(root, repository), InventoryPublicationRootNameV2)
}

func inventoryPublicationMarkerPathV2(root, repository string) string {
	return filepath.Join(inventoryPublicationDirectoryV2(root, repository), InventoryPublicationMarkerNameV2)
}

func inventoryGenerationDirectoryV2(root, repository, generation string) string {
	return filepath.Join(inventoryPublicationDirectoryV2(root, repository), strings.TrimPrefix(generation, "sha256:"))
}

// BeginInventoryPublicationV2 creates or resumes one marker-owned stage. A
// worker builds the complete source super-root under SourceDirectory and the
// bound observation inventory under InventoryDirectory. The marker exists
// before corpus work, so a restart can validate and complete only an exact
// finished pair while preserving the prior root for every incomplete prefix.
func BeginInventoryPublicationV2(root, repository string) (InventoryPublicationTransitionV2, error) {
	if !filepath.IsAbs(root) || validateRepository(repository) != nil {
		return InventoryPublicationTransitionV2{}, invalid("inventory v2 publication input")
	}
	if existing, present, err := readInventoryPublicationMarkerV2(root, repository); err != nil {
		return InventoryPublicationTransitionV2{}, err
	} else if present {
		return transitionFromInventoryMarkerV2(root, existing), nil
	}
	directory := inventoryPublicationDirectoryV2(root, repository)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	entries, err := boundedDirectory(directory, MaxInventoryPublicationEntriesV2)
	if err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	// A new transition consumes both one stage directory and publishing.json.
	// Refuse before either is created so lifecycle/archive can always read the
	// namespace and drain an abandoned or collecting entry.
	if len(entries) > MaxInventoryPublicationEntriesV2-2 {
		return InventoryPublicationTransitionV2{}, ErrLimit
	}
	stages := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") ||
			strings.HasPrefix(entry.Name(), "collecting-stage-") {
			stages++
		}
	}
	if stages >= MaxInventoryPublicationStagesV2 {
		return InventoryPublicationTransitionV2{}, ErrLimit
	}
	transitionID, err := inventoryTransitionIDV2()
	if err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	stage := ".stage-" + transitionID
	stageDirectory := filepath.Join(directory, stage)
	if err := os.Mkdir(stageDirectory, 0o700); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	if err := os.Mkdir(filepath.Join(stageDirectory, InventoryPublicationSourceNameV2), 0o700); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	if err := os.Mkdir(filepath.Join(stageDirectory, InventoryPublicationInventoryNameV2), 0o700); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	var previous *InventoryPublicationRootV2
	current, readErr := ReadInventoryPublicationRootV2(root, repository)
	if readErr == nil {
		previous = &current
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return InventoryPublicationTransitionV2{}, readErr
	}
	marker := InventoryPublicationMarkerV2{
		Schema: InventoryPublicationMarkerSchemaV2, Repository: repository,
		TransitionID: transitionID, Stage: stage, Previous: previous,
	}
	if err := writeExclusiveCanonical(inventoryPublicationMarkerPathV2(root, repository), marker); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	if err := syncDirectory(directory); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	return transitionFromInventoryMarkerV2(root, marker), nil
}

// CompleteInventoryPublicationV2 validates every source and observation byte
// before the caller-provided durable worker fence. The fence and exact marker
// token are rechecked before rename/root mutation, so a stale worker cannot
// publish even when it finished valid content-addressed bytes.
func CompleteInventoryPublicationV2(
	ctx context.Context, root, repository, transitionID string,
	fence func(context.Context) error,
) (InventoryPublicationRootV2, error) {
	marker, present, err := readInventoryPublicationMarkerV2(root, repository)
	if err != nil || !present {
		if err == nil {
			err = ErrStale
		}
		return InventoryPublicationRootV2{}, err
	}
	if marker.TransitionID != transitionID {
		return InventoryPublicationRootV2{}, ErrStale
	}
	transition := transitionFromInventoryMarkerV2(root, marker)
	ref, err := validateInventoryPublicationStageV2(ctx, transition)
	if err != nil {
		return InventoryPublicationRootV2{}, err
	}
	if marker.Candidate != nil && *marker.Candidate != ref {
		return InventoryPublicationRootV2{}, invalid("inventory v2 publication candidate changed")
	}
	if fence != nil {
		if err := fence(ctx); err != nil {
			return InventoryPublicationRootV2{}, errors.Join(ErrStale, err)
		}
	}
	confirmed, present, err := readInventoryPublicationMarkerV2(root, repository)
	if err != nil || !present || confirmed.TransitionID != transitionID || confirmed.Stage != marker.Stage {
		return InventoryPublicationRootV2{}, errors.Join(err, ErrStale)
	}
	if confirmed.Candidate == nil {
		candidate := ref
		confirmed.Candidate = &candidate
		if err := writeAtomicCanonical(inventoryPublicationMarkerPathV2(root, repository), confirmed); err != nil {
			return InventoryPublicationRootV2{}, err
		}
	} else if *confirmed.Candidate != ref {
		return InventoryPublicationRootV2{}, ErrStale
	}
	marker = confirmed
	destination := inventoryGenerationDirectoryV2(root, repository, ref.GenerationDigest)
	redundantStage := false
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(transition.Directory, destination); err != nil {
			return InventoryPublicationRootV2{}, err
		}
		if err := syncDirectory(inventoryPublicationDirectoryV2(root, repository)); err != nil {
			return InventoryPublicationRootV2{}, err
		}
	} else if err != nil {
		return InventoryPublicationRootV2{}, err
	} else {
		canonical := transition
		canonical.Directory = destination
		canonical.SourceDirectory = filepath.Join(destination, InventoryPublicationSourceNameV2)
		canonical.InventoryDirectory = filepath.Join(destination, InventoryPublicationInventoryNameV2)
		existing, validateErr := validateInventoryPublicationStageV2(ctx, canonical)
		if validateErr != nil || existing != ref {
			return InventoryPublicationRootV2{}, errors.Join(validateErr, invalid("inventory v2 generation collision"))
		}
		redundantStage = transition.Directory != destination
	}
	next, err := prospectiveInventoryPublicationRootV2(marker, ref)
	if err != nil {
		return InventoryPublicationRootV2{}, err
	}
	if err := writeAtomicCanonical(inventoryPublicationRootPathV2(root, repository), next); err != nil {
		return InventoryPublicationRootV2{}, err
	}
	if err := os.Remove(inventoryPublicationMarkerPathV2(root, repository)); err != nil {
		return InventoryPublicationRootV2{}, err
	}
	if err := syncDirectory(inventoryPublicationDirectoryV2(root, repository)); err != nil {
		return InventoryPublicationRootV2{}, err
	}
	if redundantStage {
		retired := filepath.Join(
			inventoryPublicationDirectoryV2(root, repository),
			"collecting-stage-"+transitionID,
		)
		if err := os.Rename(transition.Directory, retired); err != nil {
			return InventoryPublicationRootV2{}, err
		}
		if err := syncDirectory(inventoryPublicationDirectoryV2(root, repository)); err != nil {
			return InventoryPublicationRootV2{}, err
		}
	}
	return next, nil
}

// RecoverInventoryPublicationV2 is the startup-only completion path. The
// caller holds the common backup/publication mutation lock before workers are
// admitted. Incomplete or corrupt stage bytes remain non-authoritative and the
// prior root is unchanged.
func RecoverInventoryPublicationV2(ctx context.Context, root, repository string) (bool, error) {
	marker, present, err := readInventoryPublicationMarkerV2(root, repository)
	if err != nil || !present {
		return false, err
	}
	if _, err := validateInventoryPublicationStageV2(ctx, transitionFromInventoryMarkerV2(root, marker)); err != nil {
		return false, err
	}
	_, err = CompleteInventoryPublicationV2(ctx, root, repository, marker.TransitionID, nil)
	return err == nil, err
}

// RestartInventoryPublicationV2 retires one incomplete marker-owned stage
// without recursive deletion. A later lifecycle turn drains the collecting
// name within its deletion budget, while a fresh transition can resume work
// from a clean source/observation pair. The durable worker fence prevents a
// stale process from retiring a replacement worker's stage.
func RestartInventoryPublicationV2(
	ctx context.Context, root, repository, transitionID string,
	fence func(context.Context) error,
) (InventoryPublicationTransitionV2, error) {
	marker, present, err := readInventoryPublicationMarkerV2(root, repository)
	if err != nil || !present || marker.TransitionID != transitionID {
		return InventoryPublicationTransitionV2{}, errors.Join(err, ErrStale)
	}
	if marker.Candidate != nil {
		return InventoryPublicationTransitionV2{}, invalid("sealed inventory v2 stage cannot restart")
	}
	if fence != nil {
		if err := fence(ctx); err != nil {
			return InventoryPublicationTransitionV2{}, errors.Join(ErrStale, err)
		}
	}
	confirmed, present, err := readInventoryPublicationMarkerV2(root, repository)
	if err != nil || !present || confirmed.TransitionID != transitionID || confirmed.Candidate != nil {
		return InventoryPublicationTransitionV2{}, errors.Join(err, ErrStale)
	}
	directory := inventoryPublicationDirectoryV2(root, repository)
	stage := filepath.Join(directory, marker.Stage)
	retired := filepath.Join(directory, "collecting-stage-"+transitionID)
	info, err := os.Lstat(stage)
	if errors.Is(err, os.ErrNotExist) {
		info, err = os.Lstat(retired)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return InventoryPublicationTransitionV2{}, errors.Join(err, invalid("inventory v2 retired restart stage"))
		}
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return InventoryPublicationTransitionV2{}, errors.Join(err, invalid("inventory v2 restart stage"))
	} else {
		if err := os.Rename(stage, retired); err != nil {
			return InventoryPublicationTransitionV2{}, err
		}
	}
	if err := os.Remove(inventoryPublicationMarkerPathV2(root, repository)); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	if err := syncDirectory(directory); err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	return BeginInventoryPublicationV2(root, repository)
}

func RecoverInventoryPublicationsV2(ctx context.Context, root string) (InventoryRecoveryReportV2, error) {
	var report InventoryRecoveryReportV2
	entries, err := boundedDirectory(root, MaxLifecycleRepositories)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
			continue
		}
		markerPath := filepath.Join(root, entry.Name(), InventoryPublicationDirectoryV2, InventoryPublicationMarkerNameV2)
		raw, readErr := readBoundedRegular(markerPath, MaxManifestBytes)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return report, readErr
		}
		var marker InventoryPublicationMarkerV2
		if decodeCanonical(raw, &marker) != nil || validateInventoryPublicationMarkerV2(marker) != nil ||
			repositoryHash(marker.Repository) != entry.Name() {
			return report, invalid("inventory v2 recovery marker")
		}
		report.Repositories++
		completed, recoverErr := RecoverInventoryPublicationV2(ctx, root, marker.Repository)
		if recoverErr != nil {
			report.Incomplete++
			continue
		}
		if completed {
			report.Completed++
		}
	}
	return report, nil
}

func ReadInventoryPublicationRootV2(root, repository string) (InventoryPublicationRootV2, error) {
	return ReadInventoryPublicationRootV2Context(context.Background(), root, repository)
}

// ReadInventoryPublicationRootV2Context reads one compact current control,
// including its failed preflight/open attempt. It does not open its generation.
func ReadInventoryPublicationRootV2Context(ctx context.Context, root, repository string) (InventoryPublicationRootV2, error) {
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return InventoryPublicationRootV2{}, err
	}
	raw, err := readBoundedRegular(inventoryPublicationRootPathV2(root, repository), MaxManifestBytes)
	if err != nil {
		return InventoryPublicationRootV2{}, err
	}
	var value InventoryPublicationRootV2
	if decodeCanonical(raw, &value) != nil || validateInventoryPublicationRootV2(value, repository) != nil {
		return InventoryPublicationRootV2{}, invalid("inventory v2 publication root")
	}
	return value, nil
}

func validateInventoryPublicationStageV2(
	ctx context.Context, transition InventoryPublicationTransitionV2,
) (InventoryGenerationRefV2, error) {
	sourceRoot, err := sourcepartition.ReadSuperRoot(transition.SourceDirectory, transition.Repository)
	if err != nil {
		return InventoryGenerationRefV2{}, err
	}
	plan, err := sourcepartition.OpenSuperRoot(ctx, transition.SourceDirectory, sourceRoot)
	if err != nil {
		return InventoryGenerationRefV2{}, err
	}
	inventoryRoot, err := ReadInventoryRootV2(transition.InventoryDirectory, transition.Repository)
	if err != nil {
		return InventoryGenerationRefV2{}, err
	}
	if err := ValidateInventoryStageV2(ctx, transition.InventoryDirectory, inventoryRoot); err != nil {
		return InventoryGenerationRefV2{}, err
	}
	if err := ValidateInventorySourceV2(transition.InventoryDirectory, inventoryRoot, plan); err != nil {
		return InventoryGenerationRefV2{}, err
	}
	ref := InventoryGenerationRefV2{
		GenerationDigest: inventoryRoot.GenerationDigest,
		Directory:        strings.TrimPrefix(inventoryRoot.GenerationDigest, "sha256:"),
		SourceRootDigest: sourceRoot.Digest, InventoryDigest: inventoryRoot.Digest,
		SourceSegments: len(sourceRoot.Segments), InventorySegments: len(inventoryRoot.Segments),
		Records: inventoryRoot.RecordCount, EncodedBytes: inventoryRoot.EncodedMemberBytes,
		ObservationBytes: inventoryRoot.ObservationBytes,
	}
	if err := validateInventoryGenerationRefV2(ref); err != nil {
		return InventoryGenerationRefV2{}, err
	}
	return ref, nil
}

func prospectiveInventoryPublicationRootV2(
	marker InventoryPublicationMarkerV2, candidate InventoryGenerationRefV2,
) (InventoryPublicationRootV2, error) {
	if marker.Previous != nil && marker.Previous.Current.GenerationDigest == candidate.GenerationDigest {
		if marker.Previous.Current != candidate {
			return InventoryPublicationRootV2{}, invalid("inventory v2 same-generation reference changed")
		}
		return *marker.Previous, nil
	}
	root := InventoryPublicationRootV2{
		Schema: InventoryPublicationRootSchemaV2, Repository: marker.Repository,
		Current: candidate,
	}
	if marker.Previous != nil && marker.Previous.Current.GenerationDigest != candidate.GenerationDigest {
		prior := marker.Previous.Current
		root.Prior = &prior
	}
	if err := validateInventoryPublicationRootV2(root, marker.Repository); err != nil {
		return InventoryPublicationRootV2{}, err
	}
	return root, nil
}

func validateInventoryPublicationRootV2(value InventoryPublicationRootV2, repository string) error {
	if value.Schema != InventoryPublicationRootSchemaV2 || value.Repository != repository ||
		validateRepository(repository) != nil || validateInventoryGenerationRefV2(value.Current) != nil {
		return invalid("inventory v2 publication root identity")
	}
	if value.Prior != nil {
		if validateInventoryGenerationRefV2(*value.Prior) != nil ||
			value.Prior.GenerationDigest == value.Current.GenerationDigest ||
			value.Current.Records > 2*MaxInventoryRecordsV2-value.Prior.Records ||
			value.Current.EncodedBytes > 2*MaxGenerationBytes-value.Prior.EncodedBytes ||
			value.Current.ObservationBytes > 2*MaxGenerationBytes-value.Prior.ObservationBytes {
			return invalid("inventory v2 rollback floor")
		}
	}
	return nil
}

func validateInventoryGenerationRefV2(value InventoryGenerationRefV2) error {
	if !validDigest(value.GenerationDigest) || value.Directory != strings.TrimPrefix(value.GenerationDigest, "sha256:") ||
		!validDigest(value.SourceRootDigest) || !validDigest(value.InventoryDigest) ||
		value.SourceSegments < 0 || value.SourceSegments > sourcepartition.MaxSegments ||
		value.InventorySegments < 0 || value.InventorySegments > MaxInventorySegmentsV2 ||
		value.Records < 0 || value.Records > MaxInventoryRecordsV2 ||
		value.EncodedBytes < 0 || value.EncodedBytes > MaxGenerationBytes ||
		value.ObservationBytes < 0 || value.ObservationBytes > MaxGenerationBytes {
		return invalid("inventory v2 generation reference")
	}
	return nil
}

func readInventoryPublicationMarkerV2(
	root, repository string,
) (InventoryPublicationMarkerV2, bool, error) {
	raw, err := readBoundedRegular(inventoryPublicationMarkerPathV2(root, repository), MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return InventoryPublicationMarkerV2{}, false, nil
	}
	if err != nil {
		return InventoryPublicationMarkerV2{}, false, err
	}
	var value InventoryPublicationMarkerV2
	if decodeCanonical(raw, &value) != nil || validateInventoryPublicationMarkerV2(value) != nil ||
		value.Repository != repository {
		return InventoryPublicationMarkerV2{}, false, invalid("inventory v2 publication marker")
	}
	return value, true, nil
}

func validateInventoryPublicationMarkerV2(value InventoryPublicationMarkerV2) error {
	if value.Schema != InventoryPublicationMarkerSchemaV2 || validateRepository(value.Repository) != nil ||
		len(value.TransitionID) != 32 || !validLowerHex(value.TransitionID) ||
		value.Stage != ".stage-"+value.TransitionID {
		return invalid("inventory v2 publication marker identity")
	}
	if value.Previous != nil && validateInventoryPublicationRootV2(*value.Previous, value.Repository) != nil {
		return invalid("inventory v2 publication marker previous root")
	}
	if value.Candidate != nil && validateInventoryGenerationRefV2(*value.Candidate) != nil {
		return invalid("inventory v2 publication marker candidate")
	}
	return nil
}

func transitionFromInventoryMarkerV2(root string, marker InventoryPublicationMarkerV2) InventoryPublicationTransitionV2 {
	directory := filepath.Join(inventoryPublicationDirectoryV2(root, marker.Repository), marker.Stage)
	if marker.Candidate != nil {
		canonical := inventoryGenerationDirectoryV2(root, marker.Repository, marker.Candidate.GenerationDigest)
		if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
			directory = canonical
		}
	}
	return InventoryPublicationTransitionV2{
		Repository: marker.Repository, TransitionID: marker.TransitionID, Directory: directory,
		SourceDirectory:    filepath.Join(directory, InventoryPublicationSourceNameV2),
		InventoryDirectory: filepath.Join(directory, InventoryPublicationInventoryNameV2),
	}
}

func inventoryTransitionIDV2() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

package relationshippublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

// PublicationTransitionControlFileReadsV3 is the exact read cost of either
// transition snapshot.
const PublicationTransitionControlFileReadsV3 uint64 = 5

// PublicationTransitionPointV3 distinguishes the durable marker hit from its
// clean startup recovery.
type PublicationTransitionPointV3 string

const (
	PublicationTransitionHitV3       PublicationTransitionPointV3 = "hit"
	PublicationTransitionRecoveredV3 PublicationTransitionPointV3 = "recovered"
)

// PublicationTransitionRequestV3 names one exact marker-owned publication
// transition. FormerStageName is used only for the bounded absence check and
// is never returned by the source-free snapshot.
type PublicationTransitionRequestV3 struct {
	Point                  PublicationTransitionPointV3
	Repository             string
	PriorGenerationDigest  string
	PriorRootDigest        string
	TargetGenerationDigest string
	TargetRootDigest       string
	FormerStageName        string
}

// PublicationTransitionSnapshotV3 is the source-free result of the fixed
// current/marker/root/current/marker observation. It contains no paths,
// members, worker identity, lease material, or raw control bytes.
type PublicationTransitionSnapshotV3 struct {
	Point                  PublicationTransitionPointV3
	PriorGenerationDigest  string
	PriorRootDigest        string
	TargetGenerationDigest string
	TargetRootDigest       string
	TargetAuthorityDigest  string
}

// PublicationTransitionTargetV3 binds a durable marker hit to the runtime
// plan and actual generation schedule that produced it.
type PublicationTransitionTargetV3 struct {
	Request        PublicationTransitionRequestV3
	PlanDigest     string
	ScheduleDigest string
}

// PublicationTransitionRecoveryTargetV3 contains only authority retained by
// the durable marker. The exact controller supplies its already-frozen prior,
// plan, and schedule when constructing the recovered snapshot request.
type PublicationTransitionRecoveryTargetV3 struct {
	Repository             string
	TargetGenerationDigest string
	TargetRootDigest       string
	FormerStageName        string
}

// PublicationTransitionObserverV3 observes a fully durable marker hit before
// the current pointer advances.
type PublicationTransitionObserverV3 func(context.Context, PublicationTransitionTargetV3) error

// PublicationTransitionRecoveryObserverV3 observes clean marker recovery only
// after the final repository-directory sync.
type PublicationTransitionRecoveryObserverV3 func(
	context.Context,
	PublicationTransitionRecoveryTargetV3,
) error

// ReadPublicationTransitionV3 validates one marker-owned hit or its clean
// recovery using exactly five charged control reads. Metadata-only absence
// checks for the renamed stage and publishing temporary are uncharged.
func ReadPublicationTransitionV3(
	ctx context.Context,
	root string,
	request PublicationTransitionRequestV3,
) (PublicationTransitionSnapshotV3, error) {
	if ctx == nil || validatePublicationTransitionRequestV3(request) != nil {
		return PublicationTransitionSnapshotV3{}, ErrInvalid
	}
	current, err := ReadPointerV3(ctx, root, request.Repository)
	if err != nil {
		return PublicationTransitionSnapshotV3{}, fmt.Errorf(
			"read relationship v3 transition current: %w", err,
		)
	}
	marker, markerPresent, err := readPublicationMarkerV3(ctx, root, request.Repository)
	if err != nil {
		return PublicationTransitionSnapshotV3{}, fmt.Errorf(
			"read relationship v3 transition marker: %w", err,
		)
	}
	publication, err := OpenGenerationV3(
		ctx, root, request.Repository,
		request.TargetGenerationDigest, request.TargetRootDigest,
	)
	if err != nil {
		return PublicationTransitionSnapshotV3{}, fmt.Errorf(
			"read relationship v3 transition target: %w", err,
		)
	}
	confirmedCurrent, err := ReadPointerV3(ctx, root, request.Repository)
	if err != nil {
		return PublicationTransitionSnapshotV3{}, fmt.Errorf(
			"confirm relationship v3 transition current: %w", err,
		)
	}
	confirmedMarker, confirmedMarkerPresent, err := readPublicationMarkerV3(
		ctx, root, request.Repository,
	)
	if err != nil {
		return PublicationTransitionSnapshotV3{}, fmt.Errorf(
			"confirm relationship v3 transition marker: %w", err,
		)
	}
	if current != confirmedCurrent || markerPresent != confirmedMarkerPresent ||
		(markerPresent && marker != confirmedMarker) {
		return PublicationTransitionSnapshotV3{}, fmt.Errorf(
			"%w: relationship v3 transition changed", ErrPublishing,
		)
	}
	if !publication.rootValue.RepositoryComplete ||
		!publication.rootValue.AllServicesComplete ||
		publication.rootValue.FailedServiceCount != 0 {
		return PublicationTransitionSnapshotV3{}, fmt.Errorf(
			"%w: incomplete relationship v3 transition target", ErrInvalid,
		)
	}
	base, _ := RepositoryRootV3(root, request.Repository)
	if err := requirePublicationTransitionAbsentV3(
		filepath.Join(base, request.FormerStageName), "former stage",
	); err != nil {
		return PublicationTransitionSnapshotV3{}, err
	}
	if err := requirePublicationTransitionAbsentV3(
		filepath.Join(base, "publishing.json.tmp"), "publishing temporary",
	); err != nil {
		return PublicationTransitionSnapshotV3{}, err
	}
	switch request.Point {
	case PublicationTransitionHitV3:
		if current.GenerationDigest != request.PriorGenerationDigest ||
			current.RootDigest != request.PriorRootDigest || !markerPresent ||
			marker.Pointer.GenerationDigest != request.TargetGenerationDigest ||
			marker.Pointer.RootDigest != request.TargetRootDigest ||
			marker.StageName != request.FormerStageName {
			return PublicationTransitionSnapshotV3{}, fmt.Errorf(
				"%w: relationship v3 transition hit", ErrInvalid,
			)
		}
	case PublicationTransitionRecoveredV3:
		if current.GenerationDigest != request.TargetGenerationDigest ||
			current.RootDigest != request.TargetRootDigest || markerPresent {
			return PublicationTransitionSnapshotV3{}, fmt.Errorf(
				"%w: relationship v3 transition recovery", ErrInvalid,
			)
		}
	}
	return PublicationTransitionSnapshotV3{
		Point:                  request.Point,
		PriorGenerationDigest:  request.PriorGenerationDigest,
		PriorRootDigest:        request.PriorRootDigest,
		TargetGenerationDigest: publication.rootValue.GenerationDigest,
		TargetRootDigest:       publication.rootValue.Digest,
		TargetAuthorityDigest:  publication.rootValue.AuthorityDigest,
	}, nil
}

func validatePublicationTransitionRequestV3(request PublicationTransitionRequestV3) error {
	if (request.Point != PublicationTransitionHitV3 &&
		request.Point != PublicationTransitionRecoveredV3) ||
		!validDigest(request.PriorGenerationDigest) ||
		!validDigest(request.PriorRootDigest) ||
		!validDigest(request.TargetGenerationDigest) ||
		!validDigest(request.TargetRootDigest) ||
		request.PriorGenerationDigest == request.TargetGenerationDigest ||
		request.PriorRootDigest == request.TargetRootDigest ||
		request.FormerStageName == "" || !validMarkerStageNameV3(request.FormerStageName) {
		return fmt.Errorf("%w: relationship v3 transition request", ErrInvalid)
	}
	if _, err := RepositoryRootV3("/", request.Repository); err != nil {
		return err
	}
	return nil
}

func readPublicationMarkerV3(
	ctx context.Context,
	root, repository string,
) (MarkerV3, bool, error) {
	if err := ctx.Err(); err != nil {
		return MarkerV3{}, false, err
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		return MarkerV3{}, false, err
	}
	if err := validateDirectory(base); err != nil {
		return MarkerV3{}, false, err
	}
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return MarkerV3{}, false, err
	}
	raw, err := readRegular(filepath.Join(base, "publishing.json"), MaxRootBytesV3)
	if errors.Is(err, os.ErrNotExist) {
		return MarkerV3{}, false, nil
	}
	if err != nil {
		return MarkerV3{}, false, err
	}
	var marker MarkerV3
	if err := decodeExact(raw, MaxRootBytesV3, &marker); err != nil ||
		validateMarkerV3(marker) != nil || marker.Pointer.Repository != repository {
		return MarkerV3{}, false, fmt.Errorf("%w: v3 publishing marker", ErrInvalid)
	}
	canonical, _ := json.Marshal(marker)
	if !slices.Equal(raw, canonical) {
		return MarkerV3{}, false, fmt.Errorf("%w: v3 publishing marker bytes", ErrInvalid)
	}
	return marker, true, nil
}

func requirePublicationTransitionAbsentV3(path, label string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: relationship v3 transition %s remains", ErrInvalid, label)
}

func publicationTransitionRuntimeIdentityV3(
	chunk store.GenerationChunk,
	binding runtimeBindingV3,
) (string, string, error) {
	if validateRuntimeBindingV3(binding) != nil || chunk.ID == "" ||
		chunk.Repository != binding.Repository || chunk.Stage != ScheduleStageV3 ||
		chunk.Generation != binding.ScheduleGeneration ||
		chunk.ResourceClass != store.GenerationResourceMemory ||
		chunk.Offset != 0 || chunk.Length != 1 || chunk.Attempt != 0 ||
		chunk.Priority != store.GenerationPriorityNeverRun ||
		chunk.Status != store.GenerationChunkRunning || chunk.NotBefore != nil ||
		chunk.ClaimedAt == nil || chunk.ClaimedAt.IsZero() || chunk.HeartbeatAt == nil ||
		chunk.HeartbeatAt.IsZero() || chunk.HeartbeatAt.Before(*chunk.ClaimedAt) ||
		chunk.FinishedAt != nil || chunk.Error != "" ||
		chunk.ClaimedBy == "" || strings.TrimSpace(chunk.ClaimedBy) != chunk.ClaimedBy ||
		len(chunk.ClaimedBy) > store.MaxGenerationWorkerBytes || !utf8.ValidString(chunk.ClaimedBy) ||
		!validPublicationTransitionLeaseV3(chunk.LeaseToken) {
		return "", "", fmt.Errorf("%w: relationship v3 transition runtime", ErrInvalid)
	}
	scheduleDigest, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: binding.Repository, Stage: ScheduleStageV3,
		Generation: binding.ScheduleGeneration, ResourceClass: store.GenerationResourceMemory,
		TotalItems: 1, ChunkItems: 1, MaxAttempts: ScheduleMaxAttempts,
		RepositoryTokens: ScheduleRepositoryTokens,
	})
	if err != nil || chunk.ScheduleDigest != scheduleDigest ||
		chunk.Identity != publicationTransitionChunkIdentityV3(
			scheduleDigest, chunk.Offset, chunk.Attempt,
		) || scheduleDigest == binding.TargetGeneration {
		return "", "", errors.Join(err, fmt.Errorf(
			"%w: relationship v3 transition schedule", ErrInvalid,
		))
	}
	return binding.TargetGeneration, scheduleDigest, nil
}

func publicationTransitionTargetV3(
	prior PointerV3,
	marker MarkerV3,
	planDigest, scheduleDigest string,
) (PublicationTransitionTargetV3, error) {
	request := PublicationTransitionRequestV3{
		Point: PublicationTransitionHitV3, Repository: marker.Pointer.Repository,
		PriorGenerationDigest: prior.GenerationDigest, PriorRootDigest: prior.RootDigest,
		TargetGenerationDigest: marker.Pointer.GenerationDigest,
		TargetRootDigest:       marker.Pointer.RootDigest, FormerStageName: marker.StageName,
	}
	if validatePointerV3(prior) != nil || validateMarkerV3(marker) != nil ||
		prior.Repository != marker.Pointer.Repository || !validDigest(planDigest) ||
		!validDigest(scheduleDigest) || planDigest == scheduleDigest ||
		planDigest == marker.Pointer.GenerationDigest ||
		scheduleDigest == marker.Pointer.GenerationDigest ||
		validatePublicationTransitionRequestV3(request) != nil {
		return PublicationTransitionTargetV3{}, fmt.Errorf(
			"%w: relationship v3 transition target", ErrInvalid,
		)
	}
	return PublicationTransitionTargetV3{
		Request: request, PlanDigest: planDigest, ScheduleDigest: scheduleDigest,
	}, nil
}

func publicationTransitionChunkIdentityV3(schedule string, offset int64, attempt int) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-generation-chunk-v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d", schedule, offset, attempt)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validPublicationTransitionLeaseV3(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

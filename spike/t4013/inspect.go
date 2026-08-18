package t4013

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const privateSnapshotSchema = "t4013-private-profile-snapshot-v1"

const maxHTTPStatusResponseBytes = 4 << 10

type privateHTTPStatusError struct {
	Status int
	Reason string
}

func (err *privateHTTPStatusError) Error() string {
	if err == nil {
		return "T40.13 HTTP request returned an invalid status"
	}
	return fmt.Sprintf("T40.13 HTTP request returned status %d", err.Status)
}

type privateProfileSnapshot struct {
	Schema                   string
	Name                     string
	IndexedCommit            string
	SourceGeneration         string
	SearchGeneration         string
	SearchRootDigest         string
	ObservationGeneration    string
	ExtractionGeneration     string
	CallerGeneration         string
	RelationshipGeneration   string
	RelationshipRootDigest   string
	RegularFiles             uint64
	PhysicalOwners           uint64
	DeclaredSourceBytes      uint64
	ObservationRecords       uint64
	ObservationUnsupported   uint64
	ExtractionFacts          int64
	ExtractionRows           int64
	ExtractionReferences     int64
	ApplicablePartitions     int
	SettledPartitions        int
	PublishedDomains         int
	UnavailableDomains       int
	RetryExhaustedPartitions int
	SearchLogicalBytes       int64
	SearchAllocatedBytes     int64
	SourceMemberDigests      []string
	BlobReader               BlobReaderObservation
	AcceptedServices         int
	Memberships              int
	UnownedPrefixes          int
	RelationshipPublished    bool
}

type privateConvergenceProbe struct {
	Stage                       string
	SHA256                      string
	ObservationProgress         *ObservationProgressObservation
	ExtractionProgress          *ExtractionProgressObservation
	RepositoryIndexFailureClass string
	callerGenerationDigest      string
}

func convergenceProbe(stage string, values ...any) privateConvergenceProbe {
	raw, err := json.Marshal(struct {
		Stage  string `json:"stage"`
		Values []any  `json:"values"`
	}{Stage: stage, Values: values})
	if err != nil {
		raw = []byte(stage)
	}
	return privateConvergenceProbe{Stage: stage, SHA256: digest(raw)}
}

func observationConvergenceProbe(progress observationpublication.Progress) privateConvergenceProbe {
	projection := &ObservationProgressObservation{
		State: progress.State, SelectedVersion: progress.SelectedVersion,
	}
	if progress.Planning != nil {
		projection.PlanningState = progress.Planning.State
		projection.PlanningPending = progress.Planning.Pending
		projection.PlanningRunning = progress.Planning.Running
		projection.PlanningSucceeded = progress.Planning.Succeeded
		projection.PlanningFailed = progress.Planning.Failed
		if progress.Planning.Refusal != nil {
			projection.RefusalStage = string(progress.Planning.Refusal.Stage)
			projection.RefusalGenerationKind = string(progress.Planning.Refusal.GenerationKind)
			projection.RefusalClassification = string(progress.Planning.Refusal.Classification)
			projection.RefusalDimension = string(progress.Planning.Refusal.Dimension)
			projection.RefusalObserved = progress.Planning.Refusal.Observed
			projection.RefusalLimit = progress.Planning.Refusal.Limit
		}
	}
	if progress.Schedule != nil {
		projection.ScheduleState = progress.Schedule.State
		projection.ScheduleTotalPartitions = progress.Schedule.TotalPartitions
		projection.ScheduleMaterialized = progress.Schedule.Materialized
		projection.SchedulePending = progress.Schedule.Pending
		projection.ScheduleRunning = progress.Schedule.Running
		projection.ScheduleSucceeded = progress.Schedule.Succeeded
		projection.ScheduleFailed = progress.Schedule.Failed
	}
	if progress.Publication != nil {
		projection.PublicationState = progress.Publication.State
		projection.PublicationRecordCount = progress.Publication.RecordCount
		projection.PublicationObservedCount = progress.Publication.ObservedCount
		projection.PublicationUnsupportedCount = progress.Publication.UnsupportedCount
		projection.PublicationSourceSegments = progress.Publication.SourceSegments
		projection.PublicationInventorySegments = progress.Publication.InventorySegments
		projection.PublicationEncodedBytes = progress.Publication.EncodedMemberBytes
		projection.PublicationObservationBytes = progress.Publication.ObservationBytes
	}
	probe := convergenceProbe("observation_publication", progress)
	probe.ObservationProgress = projection
	return probe
}

func observationTerminal(progress observationpublication.Progress) error {
	if progress.State != "failed" || progress.Planning == nil ||
		progress.Planning.State != "failed" {
		return nil
	}
	if progress.Planning.Refusal != nil && expectedObservationBoundRefusal(*progress.Planning.Refusal) {
		return errObservationBoundRefusal
	}
	return errObservationTerminal
}

func expectedObservationBoundRefusal(receipt pipelinerefusal.Receipt) bool {
	return pipelinerefusal.Validate(receipt) == nil &&
		receipt.Stage == pipelinerefusal.StageObservationPublication &&
		receipt.GenerationKind == pipelinerefusal.GenerationObservation &&
		receipt.Classification == pipelinerefusal.ClassificationLimit &&
		receipt.Dimension == pipelinerefusal.DimensionGenerationRecords &&
		receipt.Observed > receipt.Limit &&
		receipt.Limit == observationpublication.MaxGenerationRecords
}

func extractionConvergenceProbe(
	progress extractionpublication.Progress,
	repository store.RepoStatus,
) (privateConvergenceProbe, error) {
	projection := &ExtractionProgressObservation{
		State: progress.State, Total: progress.Total, Materialized: progress.Materialized,
		Pending: progress.Pending, Running: progress.Running, Succeeded: progress.Succeeded,
		Failed: progress.Failed, Domains: progress.Domains, CurrentDomains: progress.CurrentDomains,
	}
	if repository.LastExtractionJobState != store.JobProjectionUnavailable &&
		repository.LastExtractionJobState != store.JobProjectionExact {
		return convergenceProbe("extraction_publication", progress),
			errors.New("T40.13 extraction job projection state is invalid")
	}
	if repository.LastExtractionJobState == store.JobProjectionExact {
		job := repository.LastExtractionJob
		if job == nil || !slices.Contains([]store.JobStatus{
			store.StatusPending, store.StatusClaimed, store.StatusRunning,
			store.StatusDone, store.StatusFailed, store.StatusCanceled,
		}, job.Status) || job.Attempts < 0 || job.Attempts > 1_000_000 {
			return convergenceProbe("extraction_publication", progress),
				errors.New("T40.13 extraction job projection is invalid")
		}
		projection.JobState, projection.JobAttempts = string(job.Status), job.Attempts
		if job.Refusal != nil {
			projection.RefusalStage = string(job.Refusal.Stage)
			projection.RefusalGenerationKind = string(job.Refusal.GenerationKind)
			projection.RefusalClassification = string(job.Refusal.Classification)
			projection.RefusalDimension = string(job.Refusal.Dimension)
			projection.RefusalObserved = job.Refusal.Observed
			projection.RefusalLimit = job.Refusal.Limit
		}
	}
	probe := convergenceProbe("extraction_publication", progress, projection)
	probe.ExtractionProgress = projection
	if projection.JobState == string(store.StatusFailed) ||
		projection.JobState == string(store.StatusCanceled) {
		if projection.RefusalClassification == string(pipelinerefusal.ClassificationLimit) &&
			expectedExtractionBoundRefusal(*projection) {
			return probe, errExtractionBoundRefusal
		}
		return probe, errExtractionJobTerminal
	}
	// The terminal predicate must equal expectedExtractionScheduleTerminal.
	// Runtime.Progress currently proves complete settled store counters before
	// serving them, but the public progress validator deliberately admits the
	// weaker shape. Keep that shape pending rather than raising a terminal whose
	// stopped observation could not seal if the projection boundary evolves.
	if progress.State == string(store.GenerationScheduleSettled) && progress.Failed > 0 &&
		progress.Pending == 0 && progress.Running == 0 &&
		progress.Succeeded+progress.Failed == progress.Total {
		return probe, errExtractionScheduleTerminal
	}
	if repository.LastExtractionJobState != store.JobProjectionExact {
		// Durable current extraction authority supersedes its ordinary completed
		// queue row. Lifecycle may collect that row before a later probe; absence
		// remains pending while authority is non-current so terminal jobs cannot
		// be laundered into progress.
		if progress.State == "current" {
			return probe, nil
		}
		return probe, errors.New("T40.13 extraction job projection has not converged")
	}
	return probe, nil
}

func expectedExtractionBoundRefusal(value ExtractionProgressObservation) bool {
	receipt := pipelinerefusal.Receipt{
		Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.Stage(value.RefusalStage),
		GenerationKind: pipelinerefusal.GenerationKind(value.RefusalGenerationKind),
		Classification: pipelinerefusal.Classification(value.RefusalClassification),
		Dimension:      pipelinerefusal.Dimension(value.RefusalDimension),
		Observed:       value.RefusalObserved, Limit: value.RefusalLimit,
	}
	if pipelinerefusal.Validate(receipt) != nil || receipt.Classification != pipelinerefusal.ClassificationLimit ||
		receipt.Observed <= receipt.Limit {
		return false
	}
	switch receipt.Stage {
	case pipelinerefusal.StageCandidateStrictOpen:
		return receipt.GenerationKind == pipelinerefusal.GenerationCandidate
	case pipelinerefusal.StageDomainInventory, pipelinerefusal.StageExtractorExecution,
		pipelinerefusal.StageEvidenceStaging, pipelinerefusal.StageFinalPublication:
		return receipt.GenerationKind == pipelinerefusal.GenerationExtractionDomain
	default:
		return false
	}
}

type profileInspector struct {
	client                       *http.Client
	credential                   string
	requireObservationV2         bool
	readRelationshipScheduleFunc func(context.Context, PreparedProfile) (store.GenerationScheduleProgress, error)
}

func newProfileInspector(profile PreparedProfile, requireObservationV2 ...bool) (*profileInspector, error) {
	raw, err := os.ReadFile(profile.Credential)
	if err != nil {
		return nil, errors.New("T40.13 credential is unavailable")
	}
	credential := string(bytesTrimSpace(raw))
	if credential == "" || len(credential) > 256 {
		return nil, errors.New("T40.13 credential is invalid")
	}
	requireV2 := len(requireObservationV2) > 0 && requireObservationV2[0]
	return &profileInspector{
		client: &http.Client{Timeout: 30 * time.Second}, credential: credential,
		requireObservationV2: requireV2,
	}, nil
}

func (inspector *profileInspector) health(ctx context.Context, profile PreparedProfile) error {
	var result struct {
		Status string `json:"status"`
	}
	if err := inspector.get(ctx, profile, "/api/health", &result); err != nil {
		return err
	}
	if result.Status != "ok" {
		return errors.New("T40.13 server health is invalid")
	}
	return nil
}

func (inspector *profileInspector) healthClass(
	ctx context.Context,
	profile PreparedProfile,
) (string, error) {
	err := inspector.health(ctx, profile)
	if err == nil {
		return "ok", nil
	}
	if ctx.Err() != nil {
		return "context", err
	}
	switch message := err.Error(); {
	case strings.Contains(message, "returned status"):
		return "status", err
	case strings.Contains(message, "response") || strings.Contains(message, "health is invalid"):
		return "response", err
	default:
		return "transport", err
	}
}

func (inspector *profileInspector) inspectWithProgress(
	ctx context.Context,
	profile PreparedProfile,
	revision string,
) (privateProfileSnapshot, privateConvergenceProbe, error) {
	probe := convergenceProbe("repository_visibility")
	if ctx == nil || !hexIdentity(profile.Revisions[revision], 40) {
		return privateProfileSnapshot{}, probe, errors.New("T40.13 inspection revision is invalid")
	}
	repository, err := inspector.currentRepositoryStatus(ctx, profile)
	if err != nil {
		return privateProfileSnapshot{}, probe, err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, probe, err
	}
	probe, err = repositoryIndexProbe(repository, profile.Revisions[revision])
	if err != nil {
		return privateProfileSnapshot{}, probe, err
	}
	indexDirectory := filepath.Join(profile.DataDir, "index")
	source, err := repositoryindex.ReadSourceManifest(indexDirectory, profile.RepositoryName)
	if err != nil {
		return privateProfileSnapshot{}, convergenceProbe("source_generation", repository.IndexedCommitHash), err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("source_generation", repository.IndexedCommitHash), err
	}
	probe = convergenceProbe("source_generation", source.Digest, source.OwnerCount, source.RegularOwnerCount)
	if len(source.Revisions) != 1 || source.Revisions[0].Commit != repository.IndexedCommitHash {
		return privateProfileSnapshot{}, probe, errors.New("T40.13 source generation is not exact HEAD")
	}
	searchRoot, err := focusedindex.ReadSearchGenerationRoot(indexDirectory, profile.RepositoryName)
	if err != nil {
		return privateProfileSnapshot{}, convergenceProbe("search_generation", source.Digest), err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("search_generation", source.Digest), err
	}
	probe = convergenceProbe("search_generation", source.Digest, searchRoot.Current.GenerationDigest)
	receipt, err := readSearchReceipt(indexDirectory, profile.RepositoryName, searchRoot.Current)
	if err != nil {
		return privateProfileSnapshot{}, probe, err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, probe, err
	}
	var progress observationpublication.Progress
	progressPath := "/api/observation-progress?repository=" + url.QueryEscape(profile.RepositoryName)
	if err := inspector.get(ctx, profile, progressPath, &progress); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("observation_publication", searchRoot.Current.GenerationDigest), err
	}
	if inspector.requireObservationV2 &&
		(progress.SchemaVersion != observationpublication.ProgressSchemaV2 ||
			progress.SelectedVersion != "v2") {
		return privateProfileSnapshot{}, observationConvergenceProbe(progress),
			errors.New("T40.13 selected observation v2 route is absent")
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("observation_publication", searchRoot.Current.GenerationDigest), err
	}
	probe = observationConvergenceProbe(progress)
	if terminal := observationTerminal(progress); terminal != nil {
		return privateProfileSnapshot{}, probe, terminal
	}
	if !observationProgressConverged(progress, source.Digest) {
		return privateProfileSnapshot{}, probe, errors.New("T40.13 observation publication has not converged")
	}
	var extractionSchedule extractionpublication.Progress
	extractionPath := "/api/extraction-progress?repository=" + url.QueryEscape(profile.RepositoryName)
	if err := inspector.get(ctx, profile, extractionPath, &extractionSchedule); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("extraction_publication"), err
	}
	probe, err = extractionConvergenceProbe(extractionSchedule, repository)
	if err != nil {
		return privateProfileSnapshot{}, probe, err
	}
	// A typed schedule snapshot is already enough to prove ordinary progress.
	// Do not scan every partition result until the schedule says that the
	// generation is current; doing so made the inspector's cost grow with the
	// corpus and replaced the typed snapshot with a generic control digest.
	if extractionSchedule.State != "current" {
		return privateProfileSnapshot{}, probe,
			errors.New("T40.13 extraction publication has not converged")
	}
	extraction, extractionProgress, err := inspectExtraction(ctx, profile)
	if err != nil {
		return privateProfileSnapshot{}, convergenceProbe("extraction_publication", extractionProgress), err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("extraction_publication", extractionProgress), err
	}
	callerProbe, callerErr := inspector.callerGenerationTerminal(ctx, profile)
	if callerErr != nil {
		return privateProfileSnapshot{}, callerProbe, callerErr
	}
	publication, err := relationshippublication.OpenCurrent(
		ctx, filepath.Join(profile.DataDir, "relationships"), profile.RepositoryName,
	)
	if err != nil {
		if errors.Is(err, relationshippublication.ErrNotFound) {
			probe, terminalErr := inspector.relationshipGenerationTerminal(ctx, profile)
			return privateProfileSnapshot{}, probe, terminalErr
		}
		return privateProfileSnapshot{}, convergenceProbe("relationship_publication", extractionProgress), err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("relationship_publication", extractionProgress), err
	}
	relationshipRoot := publication.Root()
	probe = convergenceProbe("relationship_publication", relationshipRoot.GenerationDigest, relationshipRoot.Digest,
		relationshipRoot.CompleteServiceCount, relationshipRoot.EmptyServiceCount, relationshipRoot.FailedServiceCount)
	if relationshipRoot.Authority.Upstream == nil ||
		relationshipRoot.Authority.ObservationGenerationDigest !=
			relationshipRoot.Authority.Upstream.Observation.ObservationGenerationDigest ||
		relationshipRoot.Authority.Upstream.Observation.SourceGenerationDigest != source.Digest ||
		!relationshipRoot.RepositoryComplete || !relationshipRoot.AllServicesComplete ||
		relationshipRoot.FailedServiceCount != 0 {
		return privateProfileSnapshot{}, probe, errors.New("T40.13 relationship root has not converged")
	}
	if err := relationshipMatchesExtraction(ctx, relationshipRoot, extraction); err != nil {
		return privateProfileSnapshot{}, probe, err
	}
	catalogRaw, err := os.ReadFile(profile.Catalog)
	if err != nil {
		return privateProfileSnapshot{}, convergenceProbe("service_census", relationshipRoot.Digest), err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("service_census", relationshipRoot.Digest), err
	}
	catalog, err := servicecatalog.Decode(catalogRaw)
	if err != nil {
		return privateProfileSnapshot{}, convergenceProbe("service_census", relationshipRoot.Digest), err
	}
	if err := inspectionContextFence(ctx); err != nil {
		return privateProfileSnapshot{}, convergenceProbe("service_census", relationshipRoot.Digest), err
	}
	result := privateProfileSnapshot{
		Schema: privateSnapshotSchema, Name: profile.Name,
		IndexedCommit: repository.IndexedCommitHash, SourceGeneration: source.Digest,
		SearchGeneration: searchRoot.Current.GenerationDigest, SearchRootDigest: receipt.SearchDigest,
		ObservationGeneration:  relationshipRoot.Authority.ObservationGenerationDigest,
		ExtractionGeneration:   extraction.generation,
		CallerGeneration:       callerProbe.callerGenerationDigest,
		RelationshipGeneration: relationshipRoot.GenerationDigest, RelationshipRootDigest: relationshipRoot.Digest,
		RegularFiles: uint64(source.RegularOwnerCount), PhysicalOwners: uint64(source.OwnerCount),
		DeclaredSourceBytes:    uint64(source.RegularDeclaredBytes),
		ObservationRecords:     uint64(progress.Publication.RecordCount),
		ObservationUnsupported: uint64(progress.Publication.UnsupportedCount),
		ExtractionFacts:        extraction.facts,
		ExtractionRows:         extraction.rows,
		ExtractionReferences:   extraction.references,
		SearchLogicalBytes:     receipt.LogicalBytes, SearchAllocatedBytes: receipt.AllocatedBytes,
		BlobReader: BlobReaderObservation{
			Profile: profile.Name, Revision: revision, Mode: receipt.BlobReaderMode,
			FilesOffered: uint64(receipt.FilesOffered), BatchReads: uint64(receipt.BatchReadCount),
			FallbackReads: uint64(receipt.FallbackReadCount),
		},
		AcceptedServices: acceptedServiceCount(catalog), Memberships: len(catalog.Memberships),
		UnownedPrefixes: len(catalog.Unowned), RelationshipPublished: true,
	}
	if relationshipRoot.ServiceCount != result.AcceptedServices ||
		relationshipRoot.CompleteServiceCount+relationshipRoot.EmptyServiceCount != result.AcceptedServices {
		return privateProfileSnapshot{}, convergenceProbe("service_census", relationshipRoot.Digest,
			result.AcceptedServices), errors.New("T40.13 relationship service census differs from the catalog")
	}
	result.SourceMemberDigests = make([]string, len(source.Members))
	for index, member := range source.Members {
		if err := inspectionContextFence(ctx); err != nil {
			return privateProfileSnapshot{}, probe, err
		}
		result.SourceMemberDigests[index] = member.Digest
	}
	for _, domain := range extraction.status.Domains {
		if err := inspectionContextFence(ctx); err != nil {
			return privateProfileSnapshot{}, probe, err
		}
		result.ApplicablePartitions += domain.Expected
		result.SettledPartitions += domain.Settled
		result.RetryExhaustedPartitions += domain.RetryExhausted
		if domain.Current && domain.RootDigest != "" {
			result.PublishedDomains++
		}
		if domain.Disposition == candidate.PartitionResultUnavailablePrerequisite {
			result.UnavailableDomains++
		}
	}
	if result.ApplicablePartitions == 0 || result.SettledPartitions != result.ApplicablePartitions ||
		result.PublishedDomains != len(extraction.status.Domains) || result.RetryExhaustedPartitions != 0 {
		return privateProfileSnapshot{}, convergenceProbe("extraction_publication", extractionProgress),
			errors.New("T40.13 extraction partitions have not converged")
	}
	return result, convergenceProbe("complete", snapshotAuthority(result)), nil
}

func (inspector *profileInspector) callerGenerationTerminal(
	ctx context.Context,
	profile PreparedProfile,
) (privateConvergenceProbe, error) {
	path := apiresponse.CallerGenerationProgressPath + "?repository=" +
		url.QueryEscape(profile.RepositoryName)
	var progress apiresponse.CallerGenerationProgress
	if err := inspector.get(ctx, profile, path, &progress); err != nil {
		return convergenceProbe("caller_generation"), err
	}
	return classifyCallerGeneration(apiresponse.CallerMapPage{
		Generation: &progress.Generation,
	})
}

func (inspector *profileInspector) relationshipGenerationTerminal(
	ctx context.Context,
	profile PreparedProfile,
) (privateConvergenceProbe, error) {
	read := inspector.readRelationshipScheduleFunc
	if read == nil {
		read = readRelationshipScheduleProgress
	}
	progress, err := read(ctx, profile)
	if err != nil {
		probe := convergenceProbe("relationship_publication")
		if errors.Is(err, store.ErrNotFound) {
			return probe, errors.New("T40.13 relationship publication has not converged")
		}
		return probe, err
	}
	return classifyRelationshipGeneration(progress)
}

func readRelationshipScheduleProgress(
	ctx context.Context,
	profile PreparedProfile,
) (store.GenerationScheduleProgress, error) {
	reader, err := store.OpenLocalGenerationChunkReader(ctx, profile.DataDir)
	if err != nil {
		return store.GenerationScheduleProgress{}, err
	}
	progress, readErr := reader.GenerationScheduleProgress(
		ctx, profile.RepositoryName, relationshippublication.ScheduleStage,
	)
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	closeErr := reader.Close(closeCtx)
	if closeErr != nil {
		return store.GenerationScheduleProgress{}, closeErr
	}
	return progress, readErr
}

func classifyRelationshipGeneration(
	progress store.GenerationScheduleProgress,
) (privateConvergenceProbe, error) {
	probe := convergenceProbe("relationship_publication", progress)
	if progress.Schema != store.GenerationScheduleProgressSchema ||
		!digestIdentity(progress.ScheduleDigest) || !digestIdentity(progress.Generation) ||
		progress.Total != 1 ||
		progress.MaxAttempts != relationshippublication.ScheduleMaxAttempts ||
		progress.Materialized < 0 ||
		progress.Materialized > progress.Total*progress.MaxAttempts || progress.Pending < 0 ||
		progress.Running < 0 || progress.Succeeded < 0 || progress.Failed < 0 ||
		progress.Materialized < progress.Pending+progress.Running ||
		progress.Pending+progress.Running > progress.Total ||
		progress.Succeeded+progress.Failed > progress.Total {
		return probe, errRelationshipTerminal
	}
	switch progress.Status {
	case store.GenerationScheduleActive:
		if progress.Failure != nil || progress.Succeeded != 0 || progress.Failed != 0 {
			return probe, errRelationshipTerminal
		}
		return probe, errors.New("T40.13 relationship publication has not converged")
	case store.GenerationScheduleSettled:
		if progress.Pending != 0 || progress.Running != 0 ||
			progress.Succeeded+progress.Failed != progress.Total || progress.Failed < 1 {
			return probe, errRelationshipTerminal
		}
	default:
		return probe, errRelationshipTerminal
	}
	failure := progress.Failure
	if failure == nil || failure.ScheduleDigest != progress.ScheduleDigest ||
		failure.Generation != progress.Generation || failure.Attempt < 0 ||
		failure.Attempt >= progress.MaxAttempts || failure.Refusal == nil {
		return probe, errRelationshipTerminal
	}
	refusal := *failure.Refusal
	if pipelinerefusal.Validate(refusal) == nil &&
		refusal.Stage == pipelinerefusal.StageRelationshipKafkaProjection &&
		refusal.GenerationKind == pipelinerefusal.GenerationRelationship &&
		refusal.Classification == pipelinerefusal.ClassificationLimit &&
		refusal.Dimension == pipelinerefusal.DimensionResidentBytes &&
		refusal.Observed > refusal.Limit &&
		refusal.Limit == relationshippublication.KafkaResidentLimit {
		return probe, errRelationshipBoundRefusal
	}
	return probe, errRelationshipTerminal
}

func classifyCallerGeneration(
	page apiresponse.CallerMapPage,
) (privateConvergenceProbe, error) {
	if page.Generation == nil {
		return convergenceProbe("caller_generation"),
			errors.New("T40.13 caller generation has not converged")
	}
	generation := page.Generation
	probe := convergenceProbe(
		"caller_generation", generation.State, generation.GenerationDigest,
		generation.PartitionProgress,
	)
	probe.callerGenerationDigest = generation.GenerationDigest
	switch generation.State {
	case "current":
		progress := generation.PartitionProgress
		if !digestIdentity(generation.GenerationDigest) ||
			progress == nil || progress.State != "complete" ||
			progress.TotalPairCount == nil ||
			*progress.TotalPairCount != progress.SettledPairCount ||
			progress.SucceededPairCount != progress.SettledPairCount ||
			progress.RefusedPairCount != 0 {
			return probe, errCallerGenerationTerminal
		}
		return probe, nil
	case "missing", "stale":
		return probe, errors.New("T40.13 caller generation has not converged")
	case "failed":
		// Continue below to distinguish the one frozen aggregate-bound refusal
		// from every other terminal caller-generation state.
	default:
		return probe, errCallerGenerationTerminal
	}
	progress := generation.PartitionProgress
	if progress == nil || progress.State != "complete" ||
		progress.TotalPairCount == nil ||
		*progress.TotalPairCount != progress.SettledPairCount ||
		progress.RefusedPairCount <= 0 ||
		progress.SucceededPairCount+progress.RefusedPairCount != progress.SettledPairCount {
		return probe, errCallerGenerationTerminal
	}
	refused := 0
	for _, refusal := range progress.Refusals {
		refused += refusal.OutcomeCount
		if refusal.Stage == pipelinerefusal.StageCallerGenerationAdmission &&
			refusal.GenerationKind == pipelinerefusal.GenerationCaller &&
			refusal.Classification == pipelinerefusal.ClassificationLimit &&
			refusal.Dimension == pipelinerefusal.DimensionCallerGenerationAbstentions &&
			refusal.Observed > refusal.Limit &&
			refusal.Limit == callerleaf.MaxAggregateAbstentionRecords {
			if refused > progress.RefusedPairCount {
				return probe, errCallerGenerationTerminal
			}
			continue
		}
		return probe, errCallerGenerationTerminal
	}
	if len(progress.Refusals) == 0 || refused != progress.RefusedPairCount {
		return probe, errCallerGenerationTerminal
	}
	return probe, errCallerGenerationBoundRefusal
}

func observationProgressConverged(progress observationpublication.Progress, sourceDigest string) bool {
	if progress.State != "current" || progress.Publication == nil ||
		progress.Publication.State != "current" ||
		progress.Publication.SourceGenerationDigest != sourceDigest {
		return false
	}
	if progress.SchemaVersion == observationpublication.ProgressSchemaV2 {
		return progress.SelectedVersion == "v2" && progress.Planning != nil &&
			progress.Planning.State == "settled" && progress.Planning.Pending == 0 &&
			progress.Planning.Running == 0 && progress.Planning.Failed == 0 &&
			progress.Planning.Succeeded == 1
	}
	return progress.Schedule != nil && progress.Schedule.State == "settled" &&
		progress.Schedule.Pending == 0 && progress.Schedule.Running == 0 &&
		progress.Schedule.Failed == 0 &&
		progress.Schedule.Succeeded == progress.Schedule.TotalPartitions &&
		progress.Schedule.Materialized >= progress.Schedule.TotalPartitions
}

func inspectionContextFence(ctx context.Context) error {
	if ctx == nil {
		return errors.New("T40.13 inspection context is invalid")
	}
	select {
	case <-ctx.Done():
		return errors.New("T40.13 inspection was canceled")
	default:
		return nil
	}
}

type extractionSnapshot struct {
	generation string
	status     extractionpublication.Status
	roots      map[string]candidate.DomainResultRoot
	facts      int64
	rows       int64
	references int64
}

func repositoryIndexProbe(
	repository store.RepoStatus,
	expectedCommit string,
) (privateConvergenceProbe, error) {
	jobStatus := store.JobStatus("")
	attempts := 0
	if repository.LastIndexJobState != store.JobProjectionUnavailable &&
		repository.LastIndexJobState != store.JobProjectionExact {
		return convergenceProbe("repository_index", repository.IndexedCommitHash,
			repository.LastIndexJobState), errors.New("T40.13 index job projection state is invalid")
	}
	if repository.LastIndexJobState == store.JobProjectionExact {
		if repository.LastIndexJob == nil {
			return convergenceProbe("repository_index", repository.IndexedCommitHash,
				repository.LastIndexJobState), errors.New("T40.13 exact index job projection is absent")
		}
		jobStatus = repository.LastIndexJob.Status
		attempts = repository.LastIndexJob.Attempts
		if !slices.Contains([]store.JobStatus{
			store.StatusPending, store.StatusClaimed, store.StatusRunning,
			store.StatusDone, store.StatusFailed, store.StatusCanceled,
		}, jobStatus) || attempts < 0 || attempts > 1_000_000 {
			return convergenceProbe("repository_index", repository.IndexedCommitHash,
				repository.LastIndexJobState), errors.New("T40.13 index job projection is invalid")
		}
	}
	failureClass := ""
	if repository.IndexedCommitHash != expectedCommit &&
		(jobStatus == store.StatusFailed || jobStatus == store.StatusCanceled) {
		failureClass = repositoryIndexFailureClass(repository.LastIndexJob)
	}
	probe := convergenceProbe(
		"repository_index", repository.IndexedCommitHash,
		repository.LastIndexJobState, jobStatus, attempts, failureClass,
	)
	probe.RepositoryIndexFailureClass = failureClass
	if repository.IndexedCommitHash == expectedCommit {
		return probe, nil
	}
	if jobStatus == store.StatusFailed || jobStatus == store.StatusCanceled {
		return probe, errRepositoryIndexTerminal
	}
	return probe, errors.New("T40.13 indexed commit has not converged")
}

func repositoryIndexFailureClass(job *store.Job) string {
	if job != nil && strings.HasPrefix(job.Error, "heartbeat: ") {
		return "lease_heartbeat"
	}
	return "other"
}

func (inspector *profileInspector) currentRepositoryStatus(
	ctx context.Context,
	profile PreparedProfile,
) (store.RepoStatus, error) {
	var repositories []store.RepoStatus
	if err := inspector.get(ctx, profile, "/api/repo-status", &repositories); err != nil {
		return store.RepoStatus{}, err
	}
	for _, repository := range repositories {
		if repository.Name == profile.RepositoryName {
			return repository, nil
		}
	}
	return store.RepoStatus{}, errors.New("T40.13 repository is not visible")
}

func (inspector *profileInspector) get(
	ctx context.Context,
	profile PreparedProfile,
	path string,
	target any,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+profile.Address+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+inspector.credential)
	response, err := inspector.client.Do(request)
	if err != nil {
		return errors.New("T40.13 HTTP request failed")
	}
	if response.StatusCode != http.StatusOK {
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxHTTPStatusResponseBytes+1))
		closeErr := response.Body.Close()
		reason := httpReasonOther
		if readErr == nil && closeErr == nil && len(raw) > 0 && len(raw) <= maxHTTPStatusResponseBytes {
			reason = classifyHTTPStatusReason(response.StatusCode, profile.Address, raw)
		}
		return &privateHTTPStatusError{Status: response.StatusCode, Reason: reason}
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, MaxObservationBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(raw) == 0 || len(raw) > MaxObservationBytes {
		return errors.New("T40.13 HTTP response exceeds its bound")
	}
	if err := decodeHumaResponse(raw, profile.Address, target); err != nil {
		return errors.New("T40.13 HTTP response is invalid")
	}
	return nil
}

func classifyHTTPStatusReason(status int, address string, raw []byte) string {
	var problem struct {
		Type     string            `json:"type,omitempty"`
		Title    string            `json:"title,omitempty"`
		Status   int               `json:"status,omitempty"`
		Detail   string            `json:"detail,omitempty"`
		Instance string            `json:"instance,omitempty"`
		Errors   []json.RawMessage `json:"errors,omitempty"`
	}
	if decodeHumaResponse(raw, address, &problem) != nil || problem.Status != status {
		return httpReasonOther
	}
	switch {
	case status == http.StatusConflict &&
		(problem.Detail == apiresponse.ObservationProgressDetailStale ||
			problem.Detail == apiresponse.ObservationProgressDetailAuthority):
		return httpReason409Stale
	case status == http.StatusConflict && problem.Detail == apiresponse.ObservationProgressDetailControlAbsent:
		return httpReason409ControlAbsent
	case status == http.StatusInternalServerError &&
		(problem.Detail == apiresponse.ObservationProgressDetailStore ||
			problem.Detail == apiresponse.ObservationProgressDetailAuthorize):
		return httpReason500Store
	case status == http.StatusInternalServerError &&
		(problem.Detail == apiresponse.ObservationProgressDetailProjection ||
			problem.Detail == apiresponse.ObservationProgressDetailEncode ||
			problem.Detail == apiresponse.ObservationProgressDetailBound):
		return httpReason500Response
	case status == http.StatusInternalServerError && problem.Detail == apiresponse.ObservationProgressDetailControl:
		return httpReason500Control
	case status == http.StatusInternalServerError && problem.Detail == apiresponse.ObservationProgressDetailPublication:
		return httpReason500Publication
	case status == http.StatusInternalServerError && problem.Detail == apiresponse.ObservationProgressDetailPlanning:
		return httpReason500Planning
	case status == http.StatusInternalServerError && problem.Detail == apiresponse.ObservationProgressDetailSchedule:
		return httpReason500Schedule
	case status == http.StatusUnauthorized:
		return httpReason401Unauthorized
	case status == http.StatusForbidden:
		return httpReason403Forbidden
	case status == http.StatusNotFound:
		return httpReason404NotFound
	case status == http.StatusServiceUnavailable:
		return httpReason503Unavailable
	default:
		return httpReasonOther
	}
}

// decodeHumaResponse consumes Huma's transport-owned top-level $schema link
// without weakening strict decoding of the application body. Huma adds the
// link to object responses but cannot add it to top-level arrays such as
// /api/repos. Duplicate keys, unknown application fields, malformed links,
// and trailing JSON remain fail-closed.
func decodeHumaResponse(raw []byte, address string, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || target == nil {
		return errors.New("T40.13 Huma response is empty")
	}
	if trimmed[0] == '[' {
		return decodeStrict(trimmed, target)
	}
	if trimmed[0] != '{' {
		return errors.New("T40.13 Huma response is not an object or array")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("T40.13 Huma response object is invalid")
	}
	seen := make(map[string]struct{}, 16)
	var body bytes.Buffer
	body.Grow(len(trimmed))
	body.WriteByte('{')
	first := true
	schemaSeen := false
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return errors.New("T40.13 Huma response key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("T40.13 Huma response has a duplicate key")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return errors.New("T40.13 Huma response value is invalid")
		}
		if key == "$schema" {
			var schema string
			if err := decodeStrict(value, &schema); err != nil || validateHumaSchemaLink(schema, address) != nil {
				return errors.New("T40.13 Huma schema link is invalid")
			}
			schemaSeen = true
			continue
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return err
		}
		if !first {
			body.WriteByte(',')
		}
		first = false
		body.Write(encodedKey)
		body.WriteByte(':')
		body.Write(value)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || !schemaSeen {
		return errors.New("T40.13 Huma response lacks its schema link")
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("T40.13 Huma response has trailing data")
	}
	body.WriteByte('}')
	return decodeStrict(body.Bytes(), target)
}

func validateHumaSchemaLink(value, address string) error {
	if len(value) == 0 || len(value) > 512 || address == "" {
		return errors.New("T40.13 Huma schema link is outside its bound")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host != address || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		!strings.HasPrefix(parsed.Path, "/schemas/") {
		return errors.New("T40.13 Huma schema link authority is invalid")
	}
	name := strings.TrimPrefix(parsed.Path, "/schemas/")
	if len(name) < len("A.json") || len(name) > 160 || strings.Contains(name, "/") ||
		!strings.HasSuffix(name, ".json") {
		return errors.New("T40.13 Huma schema name is invalid")
	}
	for _, character := range strings.TrimSuffix(name, ".json") {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return errors.New("T40.13 Huma schema name is invalid")
	}
	return nil
}

func readSearchReceipt(
	indexDirectory, repository string,
	reference focusedindex.SearchGenerationRef,
) (focusedindex.SearchGenerationReceipt, error) {
	path := filepath.Join(
		focusedindex.SearchGenerationRootDirectory(indexDirectory),
		repositoryHash(repository), reference.Directory, "generation.json",
	)
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 1<<20 {
		return focusedindex.SearchGenerationReceipt{}, errors.Join(err, errors.New("T40.13 search receipt is unavailable"))
	}
	var receipt focusedindex.SearchGenerationReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return focusedindex.SearchGenerationReceipt{}, err
	}
	if receipt.Schema != focusedindex.SearchGenerationReceiptSchema || receipt.Repository != repository ||
		receipt.SearchDigest != reference.GenerationDigest || receipt.BlobReaderMode != focusedindex.SearchBlobReaderGoGit ||
		receipt.FilesOffered <= 0 || receipt.BatchReadCount != 0 || receipt.FallbackReadCount != receipt.FilesOffered ||
		receipt.LogicalBytes != reference.LogicalBytes || receipt.AllocatedBytes != reference.AllocatedBytes ||
		receipt.AllocatedState != reference.AllocatedState {
		return focusedindex.SearchGenerationReceipt{}, errors.New("T40.13 search receipt is invalid")
	}
	return receipt, nil
}

func inspectExtraction(
	ctx context.Context,
	profile PreparedProfile,
) (extractionSnapshot, string, error) {
	if err := inspectionContextFence(ctx); err != nil {
		return extractionSnapshot{}, convergenceProbe("extraction_inventory").SHA256, err
	}
	root := filepath.Join(profile.DataDir, "extraction-publications")
	controls, err := filepath.Glob(filepath.Join(root, "*", "*", "generation.json"))
	if err != nil || len(controls) > 64 {
		return extractionSnapshot{}, convergenceProbe("extraction_inventory", len(controls)).SHA256,
			errors.New("T40.13 extraction inventory is invalid")
	}
	if err := inspectionContextFence(ctx); err != nil {
		return extractionSnapshot{}, convergenceProbe("extraction_inventory", len(controls)).SHA256, err
	}
	progress := convergenceProbe("extraction_inventory", len(controls)).SHA256
	runtime := &extractionpublication.Runtime{Root: root}
	for _, control := range controls {
		if err := inspectionContextFence(ctx); err != nil {
			return extractionSnapshot{}, progress, err
		}
		raw, readErr := os.ReadFile(control)
		if err := inspectionContextFence(ctx); err != nil {
			return extractionSnapshot{}, progress, err
		}
		if readErr != nil || len(raw) > int(extractionpublication.MaxGenerationControlBytes) {
			continue
		}
		var generation extractionpublication.Generation
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&generation) != nil || generation.Repository != profile.RepositoryName {
			continue
		}
		status, statusErr := runtime.Status(ctx, profile.RepositoryName, generation.Digest)
		if err := inspectionContextFence(ctx); err != nil {
			return extractionSnapshot{}, progress, err
		}
		if statusErr != nil {
			continue
		}
		progress = convergenceProbe("extraction_status", generation.Digest, status).SHA256
		complete := len(status.Domains) > 0
		for _, domain := range status.Domains {
			complete = complete && domain.Current && domain.RootDigest != "" && domain.Settled == domain.Expected
		}
		if complete {
			result := extractionSnapshot{
				generation: generation.Digest, status: status,
				roots: make(map[string]candidate.DomainResultRoot, len(status.Domains)),
			}
			for _, domain := range status.Domains {
				if err := inspectionContextFence(ctx); err != nil {
					return extractionSnapshot{}, progress, err
				}
				current, currentErr := runtime.Current(ctx, profile.RepositoryName, domain.Domain)
				if err := inspectionContextFence(ctx); err != nil {
					return extractionSnapshot{}, progress, err
				}
				if currentErr != nil || current.Digest != domain.RootDigest || current.PlanDigest != domain.PlanDigest {
					return extractionSnapshot{}, progress,
						errors.Join(currentErr, errors.New("T40.13 extraction root differs from current authority"))
				}
				result.roots[domain.Domain] = current
				result.facts += current.Totals.Facts
				result.rows += current.Totals.Rows
				result.references += current.Totals.References
			}
			return result, progress, nil
		}
	}
	return extractionSnapshot{}, progress, errors.New("T40.13 current extraction generation is unavailable")
}

// extractionGenerationBindsRevision proves that a currently reported
// scheduler generation resolves through production's binding/generation/plan
// validators to the selected source tree.
func extractionGenerationBindsRevision(
	profile PreparedProfile,
	scheduleGeneration string,
	revision string,
) (bool, error) {
	if !digestIdentity(scheduleGeneration) || !hexIdentity(revision, 40) {
		return false, errors.New("T40.13 live extraction identity is invalid")
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(profile.DataDir, "index"), profile.RepositoryName,
	)
	if err != nil {
		return false, err
	}
	if len(source.Revisions) != 1 || source.Revisions[0].Commit != revision {
		return false, nil
	}
	runtime := &extractionpublication.Runtime{
		Root: filepath.Join(profile.DataDir, "extraction-publications"),
	}
	authority, err := runtime.SchedulePlanningAuthority(profile.RepositoryName, scheduleGeneration)
	if err != nil {
		return false, err
	}
	return authority.SourceGenerationDigest == source.Digest, nil
}

func relationshipMatchesExtraction(
	ctx context.Context,
	root relationshippublication.Root,
	extraction extractionSnapshot,
) error {
	if root.Authority.Upstream == nil || len(root.Authority.Upstream.Domains) == 0 ||
		len(root.Authority.Upstream.Domains) != len(root.Authority.Upstream.Required) {
		return errors.New("T40.13 relationship root lacks the complete extraction authority")
	}
	for _, domain := range root.Authority.Upstream.Domains {
		if err := inspectionContextFence(ctx); err != nil {
			return err
		}
		current, ok := extraction.roots[domain.Domain]
		if !ok || domain.PlanDigest != current.PlanDigest || domain.RootDigest != current.Digest ||
			domain.Disposition != current.Disposition {
			return errors.New("T40.13 relationship root differs from current extraction authority")
		}
	}
	return nil
}

func verifyRestoredBoundary(
	ctx context.Context,
	profile PreparedProfile,
	expected privateProfileSnapshot,
) error {
	indexDirectory := filepath.Join(profile.DataDir, "index")
	source, err := repositoryindex.ReadSourceManifest(indexDirectory, profile.RepositoryName)
	if err != nil || source.Digest != expected.SourceGeneration {
		return errors.Join(err, errors.New("T40.13 restored source authority differs"))
	}
	if len(source.Revisions) != 1 || source.Revisions[0].Commit != expected.IndexedCommit {
		return errors.New("T40.13 restored source revision differs")
	}
	// Search backups intentionally retain the selected complete flat publication,
	// not lifecycle-control history. Validate that retained authority directly;
	// startup adopts it back into the lifecycle root before online inspection.
	search, err := focusedindex.ValidateRepositorySearchGeneration(
		ctx, indexDirectory, profile.RepositoryName, source.Revisions,
	)
	if err != nil || search.Digest != expected.SearchRootDigest {
		return errors.Join(err, errors.New("T40.13 restored search authority differs"))
	}
	observation, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, filepath.Join(profile.DataDir, "observations"), profile.RepositoryName,
	)
	if err != nil || observation.ObservationGenerationDigest != expected.ObservationGeneration {
		return errors.Join(err, errors.New("T40.13 restored observation authority differs"))
	}
	relationship, err := relationshippublication.OpenCurrent(
		ctx, filepath.Join(profile.DataDir, "relationships"), profile.RepositoryName,
	)
	if err != nil || relationship.Root().GenerationDigest != expected.RelationshipGeneration ||
		relationship.Root().Digest != expected.RelationshipRootDigest {
		return errors.Join(err, errors.New("T40.13 restored relationship authority differs"))
	}
	for _, name := range []string{"candidates", "observation-plans", "extraction-publications", "relationship-schedules"} {
		if _, err := os.Lstat(filepath.Join(profile.DataDir, name)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("T40.13 restore retained an explicitly excluded derived namespace")
		}
	}
	return nil
}

func acceptedServiceCount(catalog servicecatalog.Catalog) int {
	count := 0
	for _, service := range catalog.Services {
		if service.Disposition == servicecatalog.DispositionAccepted {
			count++
		}
	}
	return count
}

func repositoryHash(repository string) string {
	sum := sha256Sum([]byte(repository))
	return fmt.Sprintf("%x", sum)
}

func sha256Sum(value []byte) [32]byte {
	return sha256.Sum256(value)
}

func privateSnapshotEqual(left, right privateProfileSnapshot) bool {
	left.IndexedCommit, right.IndexedCommit = "", ""
	return reflect.DeepEqual(left, right)
}

func changedSourceMembers(left, right privateProfileSnapshot) int {
	if len(left.SourceMemberDigests) != len(right.SourceMemberDigests) {
		return -1
	}
	changed := 0
	for index := range left.SourceMemberDigests {
		if left.SourceMemberDigests[index] != right.SourceMemberDigests[index] {
			changed++
		}
	}
	return changed
}

func snapshotAuthority(snapshot privateProfileSnapshot) string {
	values := []string{
		snapshot.SourceGeneration, snapshot.SearchGeneration, snapshot.ObservationGeneration,
		snapshot.ExtractionGeneration, snapshot.CallerGeneration,
		snapshot.RelationshipGeneration, snapshot.RelationshipRootDigest,
	}
	return strings.Join(values, "\x00")
}

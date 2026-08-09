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
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const privateSnapshotSchema = "t4013-private-profile-snapshot-v1"

type privateProfileSnapshot struct {
	Schema                   string
	Name                     string
	IndexedCommit            string
	SourceGeneration         string
	SearchGeneration         string
	SearchRootDigest         string
	ObservationGeneration    string
	ExtractionGeneration     string
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

type profileInspector struct {
	client     *http.Client
	credential string
}

func newProfileInspector(profile PreparedProfile) (*profileInspector, error) {
	raw, err := os.ReadFile(profile.Credential)
	if err != nil {
		return nil, errors.New("T40.13 credential is unavailable")
	}
	credential := string(bytesTrimSpace(raw))
	if credential == "" || len(credential) > 256 {
		return nil, errors.New("T40.13 credential is invalid")
	}
	return &profileInspector{
		client: &http.Client{Timeout: 30 * time.Second}, credential: credential,
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

func (inspector *profileInspector) inspect(
	ctx context.Context,
	profile PreparedProfile,
	revision string,
) (privateProfileSnapshot, error) {
	if ctx == nil || !hexIdentity(profile.Revisions[revision], 40) {
		return privateProfileSnapshot{}, errors.New("T40.13 inspection revision is invalid")
	}
	repository, err := inspector.currentRepository(ctx, profile)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	if repository.IndexedCommitHash != profile.Revisions[revision] {
		return privateProfileSnapshot{}, errors.New("T40.13 indexed commit has not converged")
	}
	indexDirectory := filepath.Join(profile.DataDir, "index")
	source, err := repositoryindex.ReadSourceManifest(indexDirectory, profile.RepositoryName)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	if len(source.Revisions) != 1 || source.Revisions[0].Commit != repository.IndexedCommitHash {
		return privateProfileSnapshot{}, errors.New("T40.13 source generation is not exact HEAD")
	}
	searchRoot, err := focusedindex.ReadSearchGenerationRoot(indexDirectory, profile.RepositoryName)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	receipt, err := readSearchReceipt(indexDirectory, profile.RepositoryName, searchRoot.Current)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	var progress observationpublication.Progress
	progressPath := "/api/observation-progress?repository=" + url.QueryEscape(profile.RepositoryName)
	if err := inspector.get(ctx, profile, progressPath, &progress); err != nil {
		return privateProfileSnapshot{}, err
	}
	if progress.State != "current" || progress.Publication == nil || progress.Publication.State != "current" ||
		progress.Publication.SourceGenerationDigest != source.Digest || progress.Schedule == nil ||
		progress.Schedule.State != "settled" || progress.Schedule.Pending != 0 || progress.Schedule.Running != 0 ||
		progress.Schedule.Failed != 0 || progress.Schedule.Materialized != progress.Schedule.TotalPartitions {
		return privateProfileSnapshot{}, errors.New("T40.13 observation publication has not converged")
	}
	extraction, err := inspectExtraction(ctx, profile)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	publication, err := relationshippublication.OpenCurrent(
		ctx, filepath.Join(profile.DataDir, "relationships"), profile.RepositoryName,
	)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	relationshipRoot := publication.Root()
	if relationshipRoot.Authority.ObservationGenerationDigest != progress.Publication.GenerationDigest ||
		!relationshipRoot.RepositoryComplete || !relationshipRoot.AllServicesComplete ||
		relationshipRoot.FailedServiceCount != 0 {
		return privateProfileSnapshot{}, errors.New("T40.13 relationship root has not converged")
	}
	if err := relationshipMatchesExtraction(relationshipRoot, extraction); err != nil {
		return privateProfileSnapshot{}, err
	}
	catalogRaw, err := os.ReadFile(profile.Catalog)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	catalog, err := servicecatalog.Decode(catalogRaw)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	result := privateProfileSnapshot{
		Schema: privateSnapshotSchema, Name: profile.Name,
		IndexedCommit: repository.IndexedCommitHash, SourceGeneration: source.Digest,
		SearchGeneration: searchRoot.Current.GenerationDigest, SearchRootDigest: receipt.SearchDigest,
		ObservationGeneration: progress.Publication.GenerationDigest, ExtractionGeneration: extraction.generation,
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
		return privateProfileSnapshot{}, errors.New("T40.13 relationship service census differs from the catalog")
	}
	result.SourceMemberDigests = make([]string, len(source.Members))
	for index, member := range source.Members {
		result.SourceMemberDigests[index] = member.Digest
	}
	for _, domain := range extraction.status.Domains {
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
		return privateProfileSnapshot{}, errors.New("T40.13 extraction partitions have not converged")
	}
	return result, nil
}

type extractionSnapshot struct {
	generation string
	status     extractionpublication.Status
	roots      map[string]candidate.DomainResultRoot
	facts      int64
	rows       int64
	references int64
}

func (inspector *profileInspector) currentRepository(
	ctx context.Context,
	profile PreparedProfile,
) (store.Repo, error) {
	var repositories []store.Repo
	if err := inspector.get(ctx, profile, "/api/repos", &repositories); err != nil {
		return store.Repo{}, err
	}
	for _, repository := range repositories {
		if repository.Name == profile.RepositoryName {
			return repository, nil
		}
	}
	return store.Repo{}, errors.New("T40.13 repository is not visible")
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
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		_ = response.Body.Close()
		return fmt.Errorf("T40.13 HTTP request returned status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, MaxObservationBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		_ = response.Body.Close()
		return errors.New("T40.13 HTTP response is invalid")
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		_ = response.Body.Close()
		return errors.New("T40.13 HTTP response has trailing data")
	}
	return response.Body.Close()
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
) (extractionSnapshot, error) {
	root := filepath.Join(profile.DataDir, "extraction-publications")
	controls, err := filepath.Glob(filepath.Join(root, "*", "*", "generation.json"))
	if err != nil || len(controls) > 64 {
		return extractionSnapshot{}, errors.New("T40.13 extraction inventory is invalid")
	}
	runtime := &extractionpublication.Runtime{Root: root}
	for _, control := range controls {
		raw, readErr := os.ReadFile(control)
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
		if statusErr != nil {
			continue
		}
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
				current, currentErr := runtime.Current(ctx, profile.RepositoryName, domain.Domain)
				if currentErr != nil || current.Digest != domain.RootDigest || current.PlanDigest != domain.PlanDigest {
					return extractionSnapshot{}, errors.Join(currentErr, errors.New("T40.13 extraction root differs from current authority"))
				}
				result.roots[domain.Domain] = current
				result.facts += current.Totals.Facts
				result.rows += current.Totals.Rows
				result.references += current.Totals.References
			}
			return result, nil
		}
	}
	return extractionSnapshot{}, errors.New("T40.13 current extraction generation is unavailable")
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

func relationshipMatchesExtraction(root relationshippublication.Root, extraction extractionSnapshot) error {
	if root.Authority.Upstream == nil || len(root.Authority.Upstream.Domains) == 0 ||
		len(root.Authority.Upstream.Domains) != len(root.Authority.Upstream.Required) {
		return errors.New("T40.13 relationship root lacks the complete extraction authority")
	}
	for _, domain := range root.Authority.Upstream.Domains {
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
	searchRoot, err := focusedindex.ReadSearchGenerationRoot(indexDirectory, profile.RepositoryName)
	if err != nil || searchRoot.Current.GenerationDigest != expected.SearchGeneration {
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
		snapshot.ExtractionGeneration, snapshot.RelationshipGeneration, snapshot.RelationshipRootDigest,
	}
	return strings.Join(values, "\x00")
}

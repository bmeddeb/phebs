package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

var ErrInvalidServiceCatalogV3Candidate = errors.New(
	"invalid service catalog v3 candidate",
)

const maxServiceCatalogV3PublishAttempts = 4

type ServiceCatalogV3CandidateRoot struct {
	Root            servicecatalogv3.Root
	ControlRevision uint64
	PublishedAt     time.Time
}

type ServiceCatalogV3Pointer struct {
	Repository      string
	RootDigest      string
	ControlRevision uint64
	PublishedAt     time.Time
}

type ServiceCatalogV3Candidate struct {
	Generation      servicecatalogv3.Generation
	ControlRevision uint64
	PublishedAt     time.Time
}

type ServiceCatalogV3CandidateStore interface {
	PublishServiceCatalogV3Candidate(
		context.Context,
		servicecatalogv3.Generation,
	) error
	GetServiceCatalogV3CandidateRoot(
		context.Context,
		string,
	) (*ServiceCatalogV3CandidateRoot, error)
	GetServiceCatalogV3Candidate(
		context.Context,
		string,
	) (*ServiceCatalogV3Candidate, error)
}

var _ ServiceCatalogV3CandidateStore = (*Surreal)(nil)
var _ servicecatalogv3.ReadSource = (*Surreal)(nil)

type serviceCatalogV3RootRec struct {
	RootDigest string           `json:"root_digest"`
	Repository string           `json:"repository"`
	RootBytes  int              `json:"root_bytes"`
	RootJSON   string           `json:"root_json"`
	RecordedAt time.Time        `json:"recorded_at"`
	RecID      *models.RecordID `json:"id"`
}

type serviceCatalogV3MemberRec struct {
	MemberDigest string           `json:"member_digest"`
	Kind         string           `json:"kind"`
	Ordinal      int              `json:"ordinal"`
	ContentBytes int              `json:"content_bytes"`
	Content      string           `json:"content"`
	RecordedAt   time.Time        `json:"recorded_at"`
	RecID        *models.RecordID `json:"id"`
}

type serviceCatalogV3AuthorityVersionRec struct {
	Repository       string           `json:"repository"`
	AuthorityKind    string           `json:"authority_kind"`
	AuthorityID      string           `json:"authority_id"`
	AuthorityVersion string           `json:"authority_version"`
	OverrideID       string           `json:"override_id"`
	OverrideVersion  string           `json:"override_version"`
	LogicalDigest    string           `json:"logical_digest"`
	RecordedAt       time.Time        `json:"recorded_at"`
	RecID            *models.RecordID `json:"id"`
}

type serviceCatalogV3CandidateRec struct {
	Repository      string           `json:"repository"`
	RootDigest      string           `json:"root_digest"`
	ControlRevision uint64           `json:"control_revision"`
	PublishedAt     time.Time        `json:"published_at"`
	RecID           *models.RecordID `json:"id"`
}

type serviceCatalogV3RepoFenceRec struct {
	IndexedCommitHash string `json:"indexed_commit_hash"`
	Deleting          bool   `json:"deleting"`
}

func serviceCatalogV3RootID(digest string) models.RecordID {
	return models.NewRecordID("service_catalog_v3_root", digest[len("sha256:"):])
}

func serviceCatalogV3MemberID(digest string) models.RecordID {
	return models.NewRecordID("service_catalog_v3_member", digest[len("sha256:"):])
}

func serviceCatalogV3CandidateID(repository string) models.RecordID {
	return models.NewRecordID("service_catalog_v3_candidate", repository)
}

func serviceCatalogV3AuthorityVersionID(root servicecatalogv3.Root) models.RecordID {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-catalog-v3-authority-version\x00"))
	for _, value := range []string{
		root.Binding.Repository,
		root.Binding.Authority.Kind,
		root.Binding.Authority.ID,
		root.Binding.Authority.Version,
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	if root.Binding.Override != nil {
		_, _ = hash.Write([]byte(root.Binding.Override.ID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(root.Binding.Override.Version))
	}
	return models.NewRecordID(
		"service_catalog_v3_authority_version",
		fmt.Sprintf("%x", hash.Sum(nil)),
	)
}

func serviceCatalogV3Override(
	root servicecatalogv3.Root,
) (string, string) {
	if root.Binding.Override == nil {
		return "", ""
	}
	return root.Binding.Override.ID, root.Binding.Override.Version
}

func validServiceCatalogV3RecordID(
	record *models.RecordID,
	table, identifier string,
) bool {
	if record == nil || record.Table != table {
		return false
	}
	value, ok := record.ID.(string)
	return ok && value == identifier
}

func serviceCatalogV3MemberRecords(
	generation servicecatalogv3.Generation,
	now time.Time,
) []serviceCatalogV3MemberRec {
	records := make([]serviceCatalogV3MemberRec, len(generation.Members))
	for index, member := range generation.Members {
		var descriptor servicecatalogv3.MemberDescriptor
		if member.Kind == "service" {
			descriptor = generation.Root.ServiceMembers[member.Ordinal]
		} else {
			descriptor = generation.Root.PlacementMembers[member.Ordinal]
		}
		records[index] = serviceCatalogV3MemberRec{
			MemberDigest: descriptor.Digest,
			Kind:         member.Kind,
			Ordinal:      member.Ordinal,
			ContentBytes: len(member.Content),
			Content:      string(member.Content),
			RecordedAt:   now,
		}
	}
	return records
}

func (s *Surreal) PublishServiceCatalogV3Candidate(
	ctx context.Context,
	generation servicecatalogv3.Generation,
) error {
	return s.publishServiceCatalogV3Candidate(ctx, generation, nil)
}

// PublishServiceCatalogV3Holding rebuilds the dark candidate from the exact
// v2 authority that is still selected. This is the crash-recovery exception to
// the ordinary current-repository fence: the selected v2 tuple, not a later
// indexed commit, is the source of truth until its replacement can be fenced.
func (s *Surreal) PublishServiceCatalogV3Holding(
	ctx context.Context,
	selector ServiceRuntimeSelector,
	generation servicecatalogv3.Generation,
) error {
	if validateServiceRuntimeSelector(selector) != nil ||
		selector.Backend != ServiceRuntimeV2 ||
		selector.Repository != generation.Root.Binding.Repository {
		return fmt.Errorf(
			"publish service catalog v3 holding candidate: %w",
			ErrInvalidServiceRuntimeSelector,
		)
	}
	return s.publishServiceCatalogV3Candidate(ctx, generation, &selector)
}

func (s *Surreal) publishServiceCatalogV3Candidate(
	ctx context.Context,
	generation servicecatalogv3.Generation,
	holding *ServiceRuntimeSelector,
) error {
	if err := servicecatalogv3.ValidateGeneration(generation); err != nil {
		return fmt.Errorf("publish service catalog v3 candidate: %w", err)
	}
	if generation.Root.Binding.Source.Kind != servicecatalog.SourceCommitted &&
		generation.Root.Binding.Source.Kind != servicecatalog.SourceOperator {
		return fmt.Errorf(
			"publish service catalog v3 candidate: unsupported dark source: %w",
			ErrInvalidServiceCatalogV3Candidate,
		)
	}
	rootRaw, err := servicecatalogv3.EncodeRoot(generation.Root)
	if err != nil {
		return fmt.Errorf("publish service catalog v3 candidate: root: %w", err)
	}
	for attempt := 0; ; attempt++ {
		err = s.publishServiceCatalogV3CandidateOnce(
			ctx, generation, rootRaw, holding,
		)
		if err == nil || !isRetryableEnqueue(err) || ctx.Err() != nil ||
			attempt+1 >= maxServiceCatalogV3PublishAttempts {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("publish service catalog v3 candidate: %w", err)
	}
	stored, err := s.GetServiceCatalogV3Candidate(ctx, generation.Root.Binding.Repository)
	if err != nil {
		return fmt.Errorf("publish service catalog v3 candidate: strict-open result: %w", err)
	}
	if stored.Generation.Root.Digest != generation.Root.Digest {
		return fmt.Errorf("publish service catalog v3 candidate: pointer mismatch: %w", ErrConflict)
	}
	return nil
}

func (s *Surreal) publishServiceCatalogV3CandidateOnce(
	ctx context.Context,
	generation servicecatalogv3.Generation,
	rootRaw []byte,
	holding *ServiceRuntimeSelector,
) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Cancel(cancelCtx)
	}()
	root := generation.Root
	now := storeTimestamp(time.Now())

	repoResults, err := surrealdb.Query[[]serviceCatalogV3RepoFenceRec](
		ctx, tx, "SELECT indexed_commit_hash, deleting FROM $rid",
		map[string]any{"rid": repoID(root.Binding.Repository)},
	)
	if err != nil {
		return err
	}
	repos := firstDomainRows(repoResults)
	if len(repos) != 1 || repos[0].Deleting {
		return ErrConflict
	}
	if holding == nil {
		if repos[0].IndexedCommitHash != root.Binding.Source.Commit {
			return ErrConflict
		}
	} else if err := verifyServiceCatalogV3HoldingFence(
		ctx, tx, root, *holding,
	); err != nil {
		return err
	}

	memberRecords := serviceCatalogV3MemberRecords(generation, now)
	for _, wanted := range memberRecords {
		identifier := wanted.MemberDigest[len("sha256:"):]
		rid := serviceCatalogV3MemberID(wanted.MemberDigest)
		results, queryErr := surrealdb.Query[[]serviceCatalogV3MemberRec](
			ctx, tx, "SELECT * FROM $rid", map[string]any{"rid": rid},
		)
		if queryErr != nil {
			return queryErr
		}
		rows := firstDomainRows(results)
		if len(rows) > 1 || len(rows) == 1 && !equalServiceCatalogV3Member(
			rows[0], wanted, identifier,
		) {
			return ErrConflict
		}
		if len(rows) == 0 {
			created, createErr := surrealdb.Query[[]serviceCatalogV3MemberRec](
				ctx, tx, `CREATE $rid CONTENT {
					member_digest: $member_digest,
					kind: $kind,
					ordinal: $ordinal,
					content_bytes: $content_bytes,
					content: $content,
					recorded_at: $recorded_at
				} RETURN AFTER`, map[string]any{
					"rid": rid, "member_digest": wanted.MemberDigest,
					"kind": wanted.Kind, "ordinal": wanted.Ordinal,
					"content_bytes": wanted.ContentBytes,
					"content":       wanted.Content, "recorded_at": wanted.RecordedAt,
				},
			)
			if createErr != nil {
				return createErr
			}
			createdRows := firstDomainRows(created)
			if len(createdRows) != 1 || !equalServiceCatalogV3Member(
				createdRows[0], wanted, identifier,
			) {
				return ErrConflict
			}
		}
	}

	rootWanted := serviceCatalogV3RootRec{
		RootDigest: root.Digest,
		Repository: root.Binding.Repository,
		RootBytes:  len(rootRaw),
		RootJSON:   string(rootRaw),
		RecordedAt: now,
	}
	rootID := serviceCatalogV3RootID(root.Digest)
	rootResults, err := surrealdb.Query[[]serviceCatalogV3RootRec](
		ctx, tx, "SELECT * FROM $rid", map[string]any{"rid": rootID},
	)
	if err != nil {
		return err
	}
	rootRows := firstDomainRows(rootResults)
	rootIdentifier := root.Digest[len("sha256:"):]
	if len(rootRows) > 1 || len(rootRows) == 1 &&
		!equalServiceCatalogV3Root(rootRows[0], rootWanted, rootIdentifier) {
		return ErrConflict
	}
	if len(rootRows) == 1 {
		rootWanted.RecordedAt = rootRows[0].RecordedAt
	}
	if len(rootRows) == 0 {
		created, createErr := surrealdb.Query[[]serviceCatalogV3RootRec](
			ctx, tx, `CREATE $rid CONTENT {
				root_digest: $root_digest,
				repository: $repository,
				root_bytes: $root_bytes,
				root_json: $root_json,
				recorded_at: $recorded_at
			} RETURN AFTER`, map[string]any{
				"rid": rootID, "root_digest": rootWanted.RootDigest,
				"repository": rootWanted.Repository, "root_bytes": rootWanted.RootBytes,
				"root_json": rootWanted.RootJSON, "recorded_at": rootWanted.RecordedAt,
			},
		)
		if createErr != nil {
			return createErr
		}
		createdRows := firstDomainRows(created)
		if len(createdRows) != 1 || !equalServiceCatalogV3Root(
			createdRows[0], rootWanted, rootIdentifier,
		) {
			return ErrConflict
		}
	}

	overrideID, overrideVersion := serviceCatalogV3Override(root)
	versionWanted := serviceCatalogV3AuthorityVersionRec{
		Repository:       root.Binding.Repository,
		AuthorityKind:    root.Binding.Authority.Kind,
		AuthorityID:      root.Binding.Authority.ID,
		AuthorityVersion: root.Binding.Authority.Version,
		OverrideID:       overrideID, OverrideVersion: overrideVersion,
		LogicalDigest: root.LogicalDigest, RecordedAt: now,
	}
	versionID := serviceCatalogV3AuthorityVersionID(root)
	versionResults, err := surrealdb.Query[[]serviceCatalogV3AuthorityVersionRec](
		ctx, tx, "SELECT * FROM $rid", map[string]any{"rid": versionID},
	)
	if err != nil {
		return err
	}
	versionRows := firstDomainRows(versionResults)
	versionIdentifier, _ := versionID.ID.(string)
	if len(versionRows) > 1 || len(versionRows) == 1 &&
		!equalServiceCatalogV3AuthorityVersion(
			versionRows[0], versionWanted, versionIdentifier,
		) {
		return ErrConflict
	}
	if len(versionRows) == 0 {
		created, createErr := surrealdb.Query[[]serviceCatalogV3AuthorityVersionRec](
			ctx, tx, `CREATE $rid CONTENT {
				repository: $repository,
				authority_kind: $authority_kind,
				authority_id: $authority_id,
				authority_version: $authority_version,
				override_id: $override_id,
				override_version: $override_version,
				logical_digest: $logical_digest,
				recorded_at: $recorded_at
			} RETURN AFTER`, map[string]any{
				"rid": versionID, "repository": versionWanted.Repository,
				"authority_kind":    versionWanted.AuthorityKind,
				"authority_id":      versionWanted.AuthorityID,
				"authority_version": versionWanted.AuthorityVersion,
				"override_id":       versionWanted.OverrideID,
				"override_version":  versionWanted.OverrideVersion,
				"logical_digest":    versionWanted.LogicalDigest,
				"recorded_at":       versionWanted.RecordedAt,
			},
		)
		if createErr != nil {
			return createErr
		}
		createdRows := firstDomainRows(created)
		if len(createdRows) != 1 || !equalServiceCatalogV3AuthorityVersion(
			createdRows[0], versionWanted, versionIdentifier,
		) {
			return ErrConflict
		}
	}
	if _, err := ensureServiceCatalogV3LifecycleMetadata(
		ctx, tx, root, rootWanted.RecordedAt, true,
	); err != nil {
		return err
	}

	candidateID := serviceCatalogV3CandidateID(root.Binding.Repository)
	candidateResults, err := surrealdb.Query[[]serviceCatalogV3CandidateRec](
		ctx, tx, "SELECT * FROM $rid", map[string]any{"rid": candidateID},
	)
	if err != nil {
		return err
	}
	candidateRows := firstDomainRows(candidateResults)
	if len(candidateRows) > 1 || len(candidateRows) == 1 &&
		!validServiceCatalogV3CandidateRecord(
			candidateRows[0], root.Binding.Repository,
		) {
		return ErrConflict
	}
	priorRoot := ""
	if len(candidateRows) == 1 {
		priorRoot = candidateRows[0].RootDigest
	}
	if err := fenceServiceStateV3CandidateAdvance(
		ctx, tx, root.Binding.Repository, priorRoot, root.Digest,
	); err != nil {
		return err
	}
	if len(candidateRows) == 0 || candidateRows[0].RootDigest != root.Digest {
		revision := uint64(1)
		if len(candidateRows) == 1 {
			revision = candidateRows[0].ControlRevision + 1
		}
		updated, updateErr := surrealdb.Query[[]serviceCatalogV3CandidateRec](
			ctx, tx, `UPSERT $rid CONTENT {
				repository: $repository,
				root_digest: $root_digest,
				control_revision: $control_revision,
				published_at: $published_at
			} RETURN AFTER`, map[string]any{
				"rid": candidateID, "repository": root.Binding.Repository,
				"root_digest": root.Digest, "control_revision": revision,
				"published_at": now,
			},
		)
		if updateErr != nil {
			return updateErr
		}
		updatedRows := firstDomainRows(updated)
		if len(updatedRows) != 1 || !validServiceCatalogV3CandidateRecord(
			updatedRows[0], root.Binding.Repository,
		) || updatedRows[0].RootDigest != root.Digest ||
			updatedRows[0].ControlRevision != revision {
			return ErrConflict
		}
	}
	return tx.Commit(ctx)
}

func verifyServiceCatalogV3HoldingFence(
	ctx context.Context,
	tx *surrealdb.Transaction,
	root servicecatalogv3.Root,
	selector ServiceRuntimeSelector,
) error {
	if validateServiceRuntimeSelector(selector) != nil ||
		selector.Backend != ServiceRuntimeV2 ||
		selector.Repository != root.Binding.Repository ||
		root.MappedV2Digest == "" {
		return ErrInvalidServiceRuntimeSelector
	}
	selectorResults, err := surrealdb.Query[[]serviceRuntimeSelectorRec](
		ctx, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceRuntimeSelectorID(selector.Repository)},
	)
	if err != nil {
		return err
	}
	selectorRows := firstDomainRows(selectorResults)
	if len(selectorRows) != 1 {
		return ErrConflict
	}
	current, err := serviceRuntimeSelectorFromRec(selectorRows[0])
	if err != nil || current != selector {
		return ErrConflict
	}
	target := ServiceRuntimeTarget{
		CatalogGenerationDigest:      selector.CatalogGenerationDigest,
		CatalogControlRevision:       selector.CatalogControlRevision,
		StateControlRevision:         selector.StateControlRevision,
		StateSummaryDigest:           selector.StateSummaryDigest,
		SearchGenerationDigest:       selector.SearchGenerationDigest,
		RelationshipGenerationDigest: selector.RelationshipGenerationDigest,
		RelationshipRootDigest:       selector.RelationshipRootDigest,
	}
	if err := verifyServiceRuntimeV2Target(
		ctx, tx, selector.Repository, target,
	); err != nil {
		return err
	}
	generationResults, err := surrealdb.Query[[]serviceCatalogGenerationRec](
		ctx, tx, "SELECT * FROM $rid",
		map[string]any{
			"rid": serviceCatalogGenerationID(selector.CatalogGenerationDigest),
		},
	)
	if err != nil {
		return err
	}
	generations := firstDomainRows(generationResults)
	if len(generations) != 1 || !serviceCatalogV3HoldingMatchesV2(
		root, selector.CatalogGenerationDigest, generations[0],
	) {
		return ErrConflict
	}
	return nil
}

func serviceCatalogV3HoldingMatchesV2(
	root servicecatalogv3.Root,
	digest string,
	generation serviceCatalogGenerationRec,
) bool {
	overrideID, overrideVersion := "", ""
	if root.Binding.Override != nil {
		overrideID = root.Binding.Override.ID
		overrideVersion = root.Binding.Override.Version
	}
	return generation.Repository == root.Binding.Repository &&
		generation.GenerationDigest == digest &&
		generation.SourceKind == root.Binding.Source.Kind &&
		generation.SourcePath == root.Binding.Source.Path &&
		generation.SourceCommit == root.Binding.Source.Commit &&
		generation.SourceCensusDigest == root.Binding.Source.CensusDigest &&
		generation.SourceFileCount == root.Binding.Source.FileCount &&
		generation.AcceptedFileCount == root.Binding.Source.AcceptedFileCount &&
		generation.UnownedFileCount == root.Binding.Source.UnownedFileCount &&
		generation.LegacyAnalysisUnitDigest == root.Binding.Source.LegacyDigest &&
		generation.AuthorityKind == root.Binding.Authority.Kind &&
		generation.AuthorityID == root.Binding.Authority.ID &&
		generation.AuthorityVersion == root.Binding.Authority.Version &&
		generation.OverrideID == overrideID &&
		generation.OverrideVersion == overrideVersion &&
		generation.CatalogDigest == root.MappedV2Digest
}

func equalServiceCatalogV3Root(
	stored, wanted serviceCatalogV3RootRec,
	identifier string,
) bool {
	return validServiceCatalogV3RecordID(
		stored.RecID, "service_catalog_v3_root", identifier,
	) && stored.RootDigest == wanted.RootDigest &&
		stored.Repository == wanted.Repository &&
		stored.RootBytes == wanted.RootBytes && stored.RootJSON == wanted.RootJSON &&
		!stored.RecordedAt.IsZero()
}

func equalServiceCatalogV3Member(
	stored, wanted serviceCatalogV3MemberRec,
	identifier string,
) bool {
	return validServiceCatalogV3RecordID(
		stored.RecID, "service_catalog_v3_member", identifier,
	) && stored.MemberDigest == wanted.MemberDigest && stored.Kind == wanted.Kind &&
		stored.Ordinal == wanted.Ordinal && stored.ContentBytes == wanted.ContentBytes &&
		stored.Content == wanted.Content && !stored.RecordedAt.IsZero()
}

func equalServiceCatalogV3AuthorityVersion(
	stored, wanted serviceCatalogV3AuthorityVersionRec,
	identifier string,
) bool {
	return validServiceCatalogV3RecordID(
		stored.RecID, "service_catalog_v3_authority_version", identifier,
	) && stored.Repository == wanted.Repository &&
		stored.AuthorityKind == wanted.AuthorityKind &&
		stored.AuthorityID == wanted.AuthorityID &&
		stored.AuthorityVersion == wanted.AuthorityVersion &&
		stored.OverrideID == wanted.OverrideID &&
		stored.OverrideVersion == wanted.OverrideVersion &&
		stored.LogicalDigest == wanted.LogicalDigest && !stored.RecordedAt.IsZero()
}

func validServiceCatalogV3CandidateRecord(
	record serviceCatalogV3CandidateRec,
	repository string,
) bool {
	return validServiceCatalogV3RecordID(
		record.RecID, "service_catalog_v3_candidate", repository,
	) && record.Repository == repository && validSHA256Digest(record.RootDigest) &&
		record.ControlRevision > 0 && !record.PublishedAt.IsZero()
}

func (s *Surreal) GetServiceCatalogV3CandidateRoot(
	ctx context.Context,
	repository string,
) (*ServiceCatalogV3CandidateRoot, error) {
	pointer, err := s.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		return nil, err
	}
	root, err := s.ReadServiceCatalogV3Root(ctx, repository, pointer.RootDigest)
	if err != nil {
		return nil, fmt.Errorf("get service catalog v3 candidate: %w", err)
	}
	return &ServiceCatalogV3CandidateRoot{
		Root: root, ControlRevision: pointer.ControlRevision,
		PublishedAt: pointer.PublishedAt,
	}, nil
}

func (s *Surreal) GetServiceCatalogV3CandidatePointer(
	ctx context.Context,
	repository string,
) (ServiceCatalogV3Pointer, error) {
	if err := validateCandidateRepository(repository); err != nil {
		return ServiceCatalogV3Pointer{}, fmt.Errorf(
			"get service catalog v3 candidate pointer: repository: %w", err,
		)
	}
	candidateResults, err := surrealdb.Query[[]serviceCatalogV3CandidateRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3CandidateID(repository)},
	)
	if err != nil {
		return ServiceCatalogV3Pointer{}, fmt.Errorf(
			"get service catalog v3 candidate pointer: %w", err,
		)
	}
	candidateRows := firstDomainRows(candidateResults)
	if len(candidateRows) == 0 {
		return ServiceCatalogV3Pointer{}, fmt.Errorf(
			"service catalog v3 candidate for %q: %w", repository, ErrNotFound,
		)
	}
	if len(candidateRows) != 1 ||
		!validServiceCatalogV3CandidateRecord(candidateRows[0], repository) {
		return ServiceCatalogV3Pointer{}, fmt.Errorf(
			"get service catalog v3 candidate pointer: %w",
			ErrInvalidServiceCatalogV3Candidate,
		)
	}
	candidate := candidateRows[0]
	return ServiceCatalogV3Pointer{
		Repository: repository, RootDigest: candidate.RootDigest,
		ControlRevision: candidate.ControlRevision,
		PublishedAt:     candidate.PublishedAt.UTC(),
	}, nil
}

func (s *Surreal) ReadServiceCatalogV3Root(
	ctx context.Context,
	repository, digest string,
) (servicecatalogv3.Root, error) {
	if validateCandidateRepository(repository) != nil || !validSHA256Digest(digest) {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root: invalid identity: %w",
			ErrInvalidServiceCatalogV3Candidate,
		)
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return servicecatalogv3.Root{}, fmt.Errorf("read service catalog v3 root: %w", err)
	}
	rootResults, err := surrealdb.Query[[]serviceCatalogV3RootRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3RootID(digest)},
	)
	if err != nil {
		return servicecatalogv3.Root{}, fmt.Errorf("read service catalog v3 root: %w", err)
	}
	rootRows := firstDomainRows(rootResults)
	if len(rootRows) != 1 {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root inventory: %w",
			ErrInvalidServiceCatalogV3Candidate,
		)
	}
	rootRecord := rootRows[0]
	root, err := servicecatalogv3.DecodeRoot([]byte(rootRecord.RootJSON))
	if err != nil || root.Digest != digest ||
		root.Binding.Repository != repository ||
		!equalServiceCatalogV3Root(rootRecord, serviceCatalogV3RootRec{
			RootDigest: root.Digest, Repository: repository,
			RootBytes: len(rootRecord.RootJSON), RootJSON: rootRecord.RootJSON,
		}, digest[len("sha256:"):]) {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root identity: %w",
			ErrInvalidServiceCatalogV3Candidate,
		)
	}
	overrideID, overrideVersion := serviceCatalogV3Override(root)
	versionID := serviceCatalogV3AuthorityVersionID(root)
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root authority version: %w", err,
		)
	}
	versionResults, err := surrealdb.Query[[]serviceCatalogV3AuthorityVersionRec](
		ctx, s.db, "SELECT * FROM $rid", map[string]any{"rid": versionID},
	)
	if err != nil {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root authority version: %w", err,
		)
	}
	versionRows := firstDomainRows(versionResults)
	versionIdentifier, _ := versionID.ID.(string)
	if len(versionRows) != 1 || !equalServiceCatalogV3AuthorityVersion(
		versionRows[0], serviceCatalogV3AuthorityVersionRec{
			Repository:       root.Binding.Repository,
			AuthorityKind:    root.Binding.Authority.Kind,
			AuthorityID:      root.Binding.Authority.ID,
			AuthorityVersion: root.Binding.Authority.Version,
			OverrideID:       overrideID, OverrideVersion: overrideVersion,
			LogicalDigest: root.LogicalDigest,
		}, versionIdentifier,
	) {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root authority-version identity: %w",
			ErrInvalidServiceCatalogV3Candidate,
		)
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root lifecycle: %w", err,
		)
	}
	lifecycleResults, err := surrealdb.Query[[]serviceCatalogV3LifecycleRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3LifecycleID(root.Digest)},
	)
	if err != nil {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root lifecycle: %w", err,
		)
	}
	lifecycleRows := firstDomainRows(lifecycleResults)
	if len(lifecycleRows) != 1 || !equalServiceCatalogV3Lifecycle(
		lifecycleRows[0], serviceCatalogV3LifecycleWanted(root, rootRecord.RecordedAt),
	) {
		return servicecatalogv3.Root{}, fmt.Errorf(
			"read service catalog v3 root lifecycle identity: %w",
			ErrInvalidServiceCatalogV3Candidate,
		)
	}
	return root, nil
}

func (s *Surreal) ReadServiceCatalogV3Member(
	ctx context.Context,
	descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	raw, err := s.serviceCatalogV3MemberContent(ctx, descriptor)
	if err != nil {
		return nil, fmt.Errorf("read service catalog v3 member: %w", err)
	}
	return raw, nil
}

func (s *Surreal) GetServiceCatalogV3Candidate(
	ctx context.Context,
	repository string,
) (*ServiceCatalogV3Candidate, error) {
	opened, err := s.GetServiceCatalogV3CandidateRoot(ctx, repository)
	if err != nil {
		return nil, err
	}
	descriptors := append(
		append([]servicecatalogv3.MemberDescriptor{}, opened.Root.ServiceMembers...),
		opened.Root.PlacementMembers...,
	)
	members := make([]servicecatalogv3.EncodedMember, 0, len(descriptors))
	for _, descriptor := range descriptors {
		raw, readErr := s.ReadServiceCatalogV3Member(ctx, descriptor)
		if readErr != nil {
			return nil, fmt.Errorf("get service catalog v3 candidate: %w", readErr)
		}
		members = append(members, servicecatalogv3.EncodedMember{
			Kind: descriptor.Kind, Ordinal: descriptor.Ordinal,
			Content: raw,
		})
	}
	generation := servicecatalogv3.Generation{Root: opened.Root, Members: members}
	if err := servicecatalogv3.ValidateGeneration(generation); err != nil {
		return nil, fmt.Errorf("get service catalog v3 candidate: complete validation: %w", ErrInvalidServiceCatalogV3Candidate)
	}
	return &ServiceCatalogV3Candidate{
		Generation: generation, ControlRevision: opened.ControlRevision,
		PublishedAt: opened.PublishedAt,
	}, nil
}

const serviceCatalogV3SchemaMigrationVersion = "t41.3-service-catalog-v3-schema-v1"

func serviceCatalogV3SchemaMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "service_catalog_v3_schema")
}

const serviceCatalogV3Schema = `
DEFINE TABLE OVERWRITE service_catalog_v3_root SCHEMAFULL;
DEFINE FIELD OVERWRITE root_digest ON service_catalog_v3_root TYPE string;
DEFINE FIELD OVERWRITE repository ON service_catalog_v3_root TYPE string;
DEFINE FIELD OVERWRITE root_bytes ON service_catalog_v3_root TYPE int ASSERT $value >= 1 AND $value <= 262144;
DEFINE FIELD OVERWRITE root_json ON service_catalog_v3_root TYPE string;
DEFINE FIELD OVERWRITE recorded_at ON service_catalog_v3_root TYPE datetime;

DEFINE TABLE OVERWRITE service_catalog_v3_member SCHEMAFULL;
DEFINE FIELD OVERWRITE member_digest ON service_catalog_v3_member TYPE string;
DEFINE FIELD OVERWRITE kind ON service_catalog_v3_member TYPE string ASSERT $value INSIDE ['service', 'placement'];
DEFINE FIELD OVERWRITE ordinal ON service_catalog_v3_member TYPE int ASSERT $value >= 0 AND $value < 64;
DEFINE FIELD OVERWRITE content_bytes ON service_catalog_v3_member TYPE int ASSERT $value >= 1 AND $value <= 2097152;
DEFINE FIELD OVERWRITE content ON service_catalog_v3_member TYPE string;
DEFINE FIELD OVERWRITE recorded_at ON service_catalog_v3_member TYPE datetime;

DEFINE TABLE OVERWRITE service_catalog_v3_authority_version SCHEMAFULL;
DEFINE FIELD OVERWRITE repository ON service_catalog_v3_authority_version TYPE string;
DEFINE FIELD OVERWRITE authority_kind ON service_catalog_v3_authority_version TYPE string;
DEFINE FIELD OVERWRITE authority_id ON service_catalog_v3_authority_version TYPE string;
DEFINE FIELD OVERWRITE authority_version ON service_catalog_v3_authority_version TYPE string;
DEFINE FIELD OVERWRITE override_id ON service_catalog_v3_authority_version TYPE string;
DEFINE FIELD OVERWRITE override_version ON service_catalog_v3_authority_version TYPE string;
DEFINE FIELD OVERWRITE logical_digest ON service_catalog_v3_authority_version TYPE string;
DEFINE FIELD OVERWRITE recorded_at ON service_catalog_v3_authority_version TYPE datetime;

DEFINE TABLE OVERWRITE service_catalog_v3_candidate SCHEMAFULL;
DEFINE FIELD OVERWRITE repository ON service_catalog_v3_candidate TYPE string;
DEFINE FIELD OVERWRITE root_digest ON service_catalog_v3_candidate TYPE string;
DEFINE FIELD OVERWRITE control_revision ON service_catalog_v3_candidate TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE published_at ON service_catalog_v3_candidate TYPE datetime;
`

func (s *Surreal) migrateServiceCatalogV3Schema(ctx context.Context) error {
	markerResults, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{
		"rid": serviceCatalogV3SchemaMigrationID(),
	})
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 schema: marker: %w", err)
	}
	markerRows := firstDomainRows(markerResults)
	if len(markerRows) == 1 {
		if markerRows[0].Version == serviceCatalogV3SchemaMigrationVersion {
			return nil
		}
		return fmt.Errorf(
			"migrate service catalog v3 schema: unsupported marker %q",
			markerRows[0].Version,
		)
	}
	if len(markerRows) > 1 {
		return errors.New("migrate service catalog v3 schema: duplicate marker")
	}
	preflightSchema, err := surrealdb.Query[any](ctx, s.db, `
DEFINE TABLE IF NOT EXISTS service_catalog_v3_root SCHEMALESS;
DEFINE TABLE IF NOT EXISTS service_catalog_v3_member SCHEMALESS;
DEFINE TABLE IF NOT EXISTS service_catalog_v3_authority_version SCHEMALESS;
DEFINE TABLE IF NOT EXISTS service_catalog_v3_candidate SCHEMALESS;`, nil)
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 schema: preflight schema: %w", err)
	}
	for index, result := range *preflightSchema {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 preflight statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	probe, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db, `RETURN [{ count:
		array::len(SELECT id FROM service_catalog_v3_root LIMIT 1) +
		array::len(SELECT id FROM service_catalog_v3_member LIMIT 1) +
		array::len(SELECT id FROM service_catalog_v3_authority_version LIMIT 1) +
		array::len(SELECT id FROM service_catalog_v3_candidate LIMIT 1)
	}];`, nil)
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 schema: preflight: %w", err)
	}
	probeRows := firstDomainRows(probe)
	if len(probeRows) != 1 || probeRows[0].Count != 0 {
		return errors.New("migrate service catalog v3 schema: unowned pre-migration rows")
	}
	results, err := surrealdb.Query[any](ctx, s.db, serviceCatalogV3Schema, nil)
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 schema: define: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 schema statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	markerWrite, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $rid LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted {
	THROW 'phebs-permanent: unsupported service catalog v3 schema migration'
};
UPSERT $rid SET
	version = IF $current = NONE THEN $wanted ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"rid":    serviceCatalogV3SchemaMigrationID(),
		"wanted": serviceCatalogV3SchemaMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 schema: marker write: %w", err)
	}
	for index, result := range *markerWrite {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 schema marker statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	verified, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{
		"rid": serviceCatalogV3SchemaMigrationID(),
	})
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 schema: verify: %w", err)
	}
	rows := firstDomainRows(verified)
	if len(rows) != 1 || rows[0].Version != serviceCatalogV3SchemaMigrationVersion {
		return errors.New("migrate service catalog v3 schema: completion marker missing")
	}
	return nil
}

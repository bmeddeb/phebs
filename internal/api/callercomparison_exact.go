package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	exactCallerComparisonCursorVersion = "caller-comparison-cursor-v2"
	// One maximum response hydrates no more exact records than an ordinary
	// maximum Caller Map page. Per-side samples remain capped at four, but the
	// page-wide ceiling prevents 100 comparison rows from multiplying that
	// record-size posture by eight.
	exactCallerComparisonPageCitationLimit = callerMapMaxPage

	exactComparisonOccurrence uint8 = iota + 1
	exactComparisonUnit
	exactComparisonUnresolved
)

type exactCallerComparisonCursor struct {
	Schema                string            `json:"schema"`
	Binding               string            `json:"binding"`
	QueryDigest           string            `json:"query_digest"`
	OldVisibility         VisibilityContext `json:"old_visibility"`
	ReplacementVisibility VisibilityContext `json:"replacement_visibility"`
	Snapshot              string            `json:"snapshot"`
	Offset                int               `json:"offset"`
}

type exactComparisonAuthorization struct {
	repository store.Repo
	visibility VisibilityContext
}

type exactComparisonReads struct {
	old         *callerexecute.PublicationRead
	replacement *callerexecute.PublicationRead
	unique      []*callerexecute.PublicationRead
}

type exactComparisonIndexes struct {
	old         *exactCallerIndex
	replacement *exactCallerIndex
	unique      []*exactCallerIndex
}

type exactComparisonIdentity struct {
	kind       uint8
	repository string
	commit     string
	path       string
	unit       string
	start      int
	end        int
}

type exactComparisonRef struct {
	old      bool
	position int
}

type exactComparisonBuildEntry struct {
	identity           exactComparisonIdentity
	unresolved         bool
	hasUnit            bool
	unit               CallerMapUnitCandidate
	sourceRef          exactComparisonRef
	unitGroup          string
	oldCount           int
	oldSample          [callerComparisonCitationLimit]int
	oldSamples         int
	replacementCount   int
	replacementSample  [callerComparisonCitationLimit]int
	replacementSamples int
}

func (s *CallerComparisonService) compareExact(
	ctx context.Context,
	query CallerComparisonQuery,
	pageSize int,
	cursor string,
) (*CallerComparisonPage, error) {
	if s == nil || s.exact == nil || s.opts.CallerReader == nil {
		return nil, huma.Error503ServiceUnavailable("caller comparison unavailable")
	}
	if !catalogAuthenticated(ctx, s.opts) {
		return nil, huma.Error404NotFound("caller comparison not found")
	}
	query = normalizeCallerComparisonQuery(query)
	oldPack, replacementPack, pageSize, err := validateExactCallerComparison(
		query, pageSize,
	)
	if err != nil {
		return nil, err
	}
	queryDigest := digestJSON(struct {
		Query CallerComparisonQuery `json:"query"`
		Size  int                   `json:"page_size"`
	}{query, pageSize})

	oldAuthorization, replacementAuthorization, err :=
		s.authorizeExactComparison(ctx, query)
	if err != nil {
		return nil, err
	}
	finishExactRead, err := s.exact.beginExactRead(ctx)
	if err != nil {
		return nil, err
	}
	defer finishExactRead()
	if cursor != "" {
		return s.continueExactComparison(
			ctx, query, pageSize, cursor, queryDigest,
			oldAuthorization, replacementAuthorization,
		)
	}

	reads, err := s.openExactComparisonReads(
		ctx, oldAuthorization.repository.Name,
		replacementAuthorization.repository.Name,
	)
	if err != nil {
		return nil, err
	}
	defer reads.release()
	if reads.old.Availability != callerexecute.PublicationCurrent ||
		reads.replacement.Availability != callerexecute.PublicationCurrent {
		if err := s.confirmExactComparisonGap(
			ctx, reads, oldAuthorization, replacementAuthorization,
		); err != nil {
			return nil, err
		}
		return exactCallerComparisonGapPage(
			s.exact, query, reads, false, false, nil, nil,
		), nil
	}

	oldDeclaration, err := exactCallerDeclaration(
		ctx, s.opts.Evidence, reads.old, oldPack,
		comparisonCallerQuery(query, query.Old),
	)
	if err != nil {
		return nil, err
	}
	replacementDeclaration, err := exactCallerDeclaration(
		ctx, s.opts.Evidence, reads.replacement, replacementPack,
		comparisonCallerQuery(query, query.Replacement),
	)
	if err != nil {
		return nil, err
	}

	indexes, oldProjectionFailed, replacementProjectionFailed, err :=
		s.exactComparisonIndexes(ctx, reads)
	if err != nil {
		return nil, err
	}
	if indexes != nil {
		defer indexes.release(s.exact)
	}
	if oldProjectionFailed || replacementProjectionFailed {
		if err := s.confirmExactComparisonCurrent(
			ctx, reads, oldAuthorization, replacementAuthorization,
		); err != nil {
			return nil, err
		}
		return exactCallerComparisonGapPage(
			s.exact, query, reads, oldProjectionFailed,
			replacementProjectionFailed, &oldDeclaration,
			&replacementDeclaration,
		), nil
	}

	oldSource, err := s.exactComparisonSource(
		reads.old, indexes.old, oldAuthorization.visibility, oldDeclaration,
	)
	if err != nil {
		return nil, err
	}
	replacementSource, err := s.exactComparisonSource(
		reads.replacement, indexes.replacement,
		replacementAuthorization.visibility, replacementDeclaration,
	)
	if err != nil {
		return nil, err
	}
	oldPositions, oldScanned := exactComparisonPositions(
		indexes.old, oldPack, comparisonCallerQuery(query, query.Old),
		callerMapScanLimit,
	)
	if oldScanned > callerMapScanLimit {
		return nil, huma.Error422UnprocessableEntity(
			"caller comparison exceeded its bounded row scan; narrow the endpoint identity",
		)
	}
	replacementPositions, replacementScanned := exactComparisonPositions(
		indexes.replacement, replacementPack,
		comparisonCallerQuery(query, query.Replacement),
		callerMapScanLimit-oldScanned,
	)
	if !exactCallerComparisonScanAdmits(oldScanned, replacementScanned) {
		return nil, huma.Error422UnprocessableEntity(
			"caller comparison exceeded its bounded row scan; narrow the endpoint identity",
		)
	}
	binding, err := buildExactCallerComparisonBinding(
		query, queryDigest, oldSource, replacementSource,
		indexes.old, indexes.replacement, oldPositions, replacementPositions,
	)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"classify exact caller comparison", err,
		)
	}
	if len(binding.comparison.entries) > 0 {
		if err := s.exact.retainActiveBinding(binding); err != nil {
			return nil, err
		}
		defer s.exact.releaseBinding(binding)
	}
	page, err := s.pageExactCallerComparison(
		ctx, reads, indexes, binding, query, pageSize, 0,
	)
	if err != nil {
		if binding.id != "" {
			s.exact.dropBinding(binding.id)
		}
		return nil, err
	}
	if err := s.confirmExactComparisonCurrent(
		ctx, reads, oldAuthorization, replacementAuthorization,
	); err != nil {
		if binding.id != "" {
			s.exact.dropBinding(binding.id)
		}
		return nil, err
	}
	return page, nil
}

func exactCallerComparisonScanAdmits(oldScanned, replacementScanned int) bool {
	return oldScanned >= 0 && replacementScanned >= 0 &&
		oldScanned <= callerMapScanLimit &&
		replacementScanned <= callerMapScanLimit-oldScanned
}

func validateExactCallerComparison(
	query CallerComparisonQuery,
	pageSize int,
) (protocolPack, protocolPack, int, error) {
	oldPack, err := validateCallerMapQuery(comparisonCallerQuery(query, query.Old))
	if err != nil {
		return protocolPack{}, protocolPack{}, 0,
			huma.Error400BadRequest("old endpoint: " + err.Error())
	}
	replacementPack, err := validateCallerMapQuery(
		comparisonCallerQuery(query, query.Replacement),
	)
	if err != nil {
		return protocolPack{}, protocolPack{}, 0,
			huma.Error400BadRequest("replacement endpoint: " + err.Error())
	}
	if query.Level != "occurrence" && query.Level != "unit" {
		return protocolPack{}, protocolPack{}, 0,
			huma.Error400BadRequest("level must be occurrence or unit")
	}
	if query.Classification != "" && !stringIn(
		query.Classification,
		"old_only_evidence", "both_evidence", "new_only_evidence", "unresolved",
	) {
		return protocolPack{}, protocolPack{}, 0,
			huma.Error400BadRequest("classification is invalid")
	}
	if pageSize == 0 {
		pageSize = callerMapDefaultPage
	}
	if pageSize < 1 || pageSize > callerMapMaxPage {
		return protocolPack{}, protocolPack{}, 0,
			huma.Error400BadRequest(
				fmt.Sprintf("page_size must be from 1 through %d", callerMapMaxPage),
			)
	}
	return oldPack, replacementPack, pageSize, nil
}

func (s *CallerComparisonService) authorizeExactComparison(
	ctx context.Context,
	query CallerComparisonQuery,
) (exactComparisonAuthorization, exactComparisonAuthorization, error) {
	names := []string{query.Old.Repository}
	if query.Replacement.Repository != query.Old.Repository {
		names = append(names, query.Replacement.Repository)
		sort.Strings(names)
	}
	authorized := make(map[string]exactComparisonAuthorization, len(names))
	for _, name := range names {
		repository, visibility, err := s.exact.authorize(ctx, name)
		if err != nil {
			return exactComparisonAuthorization{}, exactComparisonAuthorization{}, err
		}
		authorized[name] = exactComparisonAuthorization{
			repository: repository, visibility: visibility,
		}
	}
	return authorized[query.Old.Repository],
		authorized[query.Replacement.Repository], nil
}

func (s *CallerComparisonService) openExactComparisonReads(
	ctx context.Context,
	oldRepository string,
	replacementRepository string,
) (*exactComparisonReads, error) {
	result := &exactComparisonReads{}
	names := []string{oldRepository}
	if replacementRepository != oldRepository {
		names = append(names, replacementRepository)
		sort.Strings(names)
	}
	opened := make(map[string]*callerexecute.PublicationRead, len(names))
	for _, name := range names {
		read, err := s.opts.CallerReader.Open(ctx, name)
		if err != nil {
			result.release()
			return nil, exactCallerReaderError("open caller comparison generation", err)
		}
		if read == nil {
			result.release()
			return nil, huma.Error500InternalServerError(
				"open caller comparison generation",
				errors.New("caller reader returned no state"),
			)
		}
		opened[name] = read
		result.unique = append(result.unique, read)
	}
	result.old = opened[oldRepository]
	result.replacement = opened[replacementRepository]
	return result, nil
}

func (reads *exactComparisonReads) release() {
	if reads == nil {
		return
	}
	for _, read := range reads.unique {
		_ = read.Release()
	}
}

func (s *CallerComparisonService) exactComparisonIndexes(
	ctx context.Context,
	reads *exactComparisonReads,
) (*exactComparisonIndexes, bool, bool, error) {
	result := &exactComparisonIndexes{}
	byKey := make(map[string]*exactCallerIndex, len(reads.unique))
	failed := make(map[string]bool, len(reads.unique))
	for _, read := range reads.unique {
		key := exactIndexKey(read)
		index, err := s.exact.index(ctx, read)
		if err != nil {
			if errors.Is(err, errExactCallerProjectionInvalid) {
				failed[key] = true
				continue
			}
			result.release(s.exact)
			return nil, false, false, err
		}
		byKey[key] = index
		result.unique = append(result.unique, index)
	}
	oldKey, replacementKey := exactIndexKey(reads.old), exactIndexKey(reads.replacement)
	result.old, result.replacement = byKey[oldKey], byKey[replacementKey]
	return result, failed[oldKey], failed[replacementKey], nil
}

func (indexes *exactComparisonIndexes) release(service *exactCallerMapService) {
	if indexes == nil {
		return
	}
	for _, index := range indexes.unique {
		service.releaseIndex(index)
	}
}

func (s *CallerComparisonService) exactComparisonSource(
	read *callerexecute.PublicationRead,
	index *exactCallerIndex,
	visibility VisibilityContext,
	declaration ContractCatalogClaim,
) (exactCallerComparisonSource, error) {
	publication, err := read.Binding()
	if err != nil {
		return exactCallerComparisonSource{},
			huma.Error500InternalServerError("bind caller comparison generation", err)
	}
	generation := exactComparisonGeneration(s.exact, read, false)
	return exactCallerComparisonSource{
		visibility: visibility, indexKey: index.key,
		publication: publication, declaration: declaration,
		generation: generation,
	}, nil
}

func exactComparisonPositions(
	index *exactCallerIndex,
	pack protocolPack,
	query CallerMapQuery,
	remaining int,
) ([]int, int) {
	ordered := index.endpoints[exactEndpointKey(
		protocolExtractorName(pack.protocol), query.Endpoint.Operation,
	)].source
	positions := make([]int, 0, min(len(ordered), max(0, remaining)))
	scanned := 0
	for _, position := range ordered {
		scanned++
		// Count every operation-bucket position actually inspected, including
		// another resolved declaration lineage. Otherwise a zero-match query
		// could traverse both maximum 200,000-record indexes while claiming a
		// zero-row scan. Read only the first over-limit position needed to prove
		// refusal.
		if scanned > remaining {
			return positions, scanned
		}
		record := index.records[position]
		if record.classification == "resolved_caller" &&
			record.lineage != query.Endpoint.Lineage {
			continue
		}
		if exactCallerRecordMatches(record, query) {
			positions = append(positions, position)
		}
	}
	return positions, scanned
}

func buildExactCallerComparisonBinding(
	query CallerComparisonQuery,
	queryDigest string,
	oldSource exactCallerComparisonSource,
	replacementSource exactCallerComparisonSource,
	oldIndex *exactCallerIndex,
	replacementIndex *exactCallerIndex,
	oldPositions []int,
	replacementPositions []int,
) (*exactCallerBinding, error) {
	entries := make(map[exactComparisonIdentity]*exactComparisonBuildEntry)
	add := func(
		positions []int,
		index *exactCallerIndex,
		source exactCallerComparisonSource,
		replacement bool,
	) {
		for _, position := range positions {
			record := index.records[position]
			identity, unresolved, candidate, hasUnit := exactComparisonIdentityFor(
				query.Level, source.generation, record,
			)
			entry := entries[identity]
			if entry != nil && entry.hasUnit && hasUnit &&
				!reflect.DeepEqual(entry.unit, candidate) {
				identity, unresolved, candidate, hasUnit =
					exactComparisonUnresolvedIdentity(source.generation, record)
				entry = entries[identity]
			}
			ref := exactComparisonRef{old: !replacement, position: position}
			if entry == nil {
				entry = &exactComparisonBuildEntry{
					identity: identity, unresolved: unresolved,
					hasUnit: hasUnit, unit: candidate,
					sourceRef: ref, unitGroup: record.unitGroup,
				}
				entries[identity] = entry
			} else {
				entry.unresolved = entry.unresolved || unresolved
				if exactComparisonSourceRefLess(
					ref, entry.sourceRef, oldIndex, replacementIndex,
					oldSource, replacementSource,
				) {
					entry.sourceRef = ref
				}
				if record.unitGroup < entry.unitGroup {
					entry.unitGroup = record.unitGroup
				}
			}
			if replacement {
				entry.replacementCount++
				if entry.replacementSamples < callerComparisonCitationLimit {
					entry.replacementSample[entry.replacementSamples] = position
					entry.replacementSamples++
				}
			} else {
				entry.oldCount++
				if entry.oldSamples < callerComparisonCitationLimit {
					entry.oldSample[entry.oldSamples] = position
					entry.oldSamples++
				}
			}
		}
	}
	add(oldPositions, oldIndex, oldSource, false)
	add(replacementPositions, replacementIndex, replacementSource, true)

	ordered := make([]*exactComparisonBuildEntry, 0, len(entries))
	for _, entry := range entries {
		if query.Classification == "" ||
			exactComparisonBuildClassification(entry) == query.Classification {
			ordered = append(ordered, entry)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return exactComparisonBuildEntryLess(
			ordered[i], ordered[j], query.Ordering,
			oldIndex, replacementIndex, oldSource, replacementSource,
		)
	})

	retained := make([]exactCallerComparisonEntry, 0, len(ordered))
	records := make([]int, 0, min(len(oldPositions)+len(replacementPositions), callerMapScanLimit))
	for _, entry := range ordered {
		retainedEntry := exactCallerComparisonEntry{
			kind: entry.identity.kind, unresolved: entry.unresolved,
			oldCount:         entry.oldCount,
			replacementCount: entry.replacementCount,
		}
		retainedEntry.oldStart = len(records)
		records = append(records, entry.oldSample[:entry.oldSamples]...)
		retainedEntry.oldEnd = len(records)
		retainedEntry.replacementStart = len(records)
		records = append(
			records,
			entry.replacementSample[:entry.replacementSamples]...,
		)
		retainedEntry.replacementEnd = len(records)
		if entry.oldSamples > 0 {
			retainedEntry.representativeOld = true
			retainedEntry.representative = entry.oldSample[0]
		} else if entry.replacementSamples > 0 {
			retainedEntry.representative = entry.replacementSample[0]
		} else {
			return nil, errors.New("caller comparison entry has no retained occurrence")
		}
		retained = append(retained, retainedEntry)
	}
	comparison := &exactCallerComparisonBinding{
		old: oldSource, replacement: replacementSource, entries: retained,
	}
	comparison.snapshot = digestJSON(struct {
		OldVisibility          VisibilityContext                `json:"old_visibility"`
		ReplacementVisibility  VisibilityContext                `json:"replacement_visibility"`
		Old                    callerexecute.PublicationBinding `json:"old"`
		Replacement            callerexecute.PublicationBinding `json:"replacement"`
		OldDeclaration         ContractCatalogClaim             `json:"old_declaration"`
		ReplacementDeclaration ContractCatalogClaim             `json:"replacement_declaration"`
	}{
		oldSource.visibility, replacementSource.visibility,
		oldSource.publication, replacementSource.publication,
		oldSource.declaration, replacementSource.declaration,
	})
	return &exactCallerBinding{
		createdAt: time.Now(), queryDigest: queryDigest,
		records: records, comparison: comparison,
	}, nil
}

func exactComparisonIdentityFor(
	level string,
	generation CallerMapGeneration,
	record exactCallerRecord,
) (exactComparisonIdentity, bool, CallerMapUnitCandidate, bool) {
	resolved := record.classification == "resolved_caller"
	if level == "unit" && resolved && record.unitState == "resolved" &&
		len(record.unitCandidates) == 1 {
		candidate := record.unitCandidates[0]
		return exactComparisonIdentity{
			kind: exactComparisonUnit, repository: generation.Repository,
			unit: candidate.ID,
		}, false, candidate, true
	}
	if level == "unit" {
		return exactComparisonUnresolvedIdentity(generation, record)
	}
	return exactComparisonIdentity{
		kind:       exactComparisonOccurrence,
		repository: generation.Repository, commit: generation.Commit,
		path: record.path, start: record.startByte, end: record.endByte,
	}, !resolved, CallerMapUnitCandidate{}, false
}

func exactComparisonUnresolvedIdentity(
	generation CallerMapGeneration,
	record exactCallerRecord,
) (exactComparisonIdentity, bool, CallerMapUnitCandidate, bool) {
	return exactComparisonIdentity{
		kind:       exactComparisonUnresolved,
		repository: generation.Repository, commit: generation.Commit,
		path: record.path, start: record.startByte, end: record.endByte,
	}, true, CallerMapUnitCandidate{}, false
}

func exactComparisonBuildClassification(entry *exactComparisonBuildEntry) string {
	if entry.unresolved {
		return "unresolved"
	}
	switch {
	case entry.oldCount > 0 && entry.replacementCount > 0:
		return "both_evidence"
	case entry.oldCount > 0:
		return "old_only_evidence"
	default:
		return "new_only_evidence"
	}
}

func exactComparisonBuildEntryLess(
	left *exactComparisonBuildEntry,
	right *exactComparisonBuildEntry,
	ordering string,
	oldIndex *exactCallerIndex,
	replacementIndex *exactCallerIndex,
	oldSource exactCallerComparisonSource,
	replacementSource exactCallerComparisonSource,
) bool {
	if ordering == "unit" {
		if order := exactComparisonUnitOrderCompare(left, right); order != 0 {
			return order < 0
		}
	}
	if exactComparisonSourceRefLess(
		left.sourceRef, right.sourceRef, oldIndex, replacementIndex,
		oldSource, replacementSource,
	) {
		return true
	}
	if exactComparisonSourceRefLess(
		right.sourceRef, left.sourceRef, oldIndex, replacementIndex,
		oldSource, replacementSource,
	) {
		return false
	}
	return exactComparisonIdentityLess(left.identity, right.identity)
}

// exactComparisonUnitOrderCompare applies one typed lexicographic regime to
// every entry pair. In particular, a resolved-unit/unresolved pair must not
// fall back to source ordering while two resolved units compare by unit ID:
// mixing those regimes makes the comparator non-transitive.
func exactComparisonUnitOrderCompare(
	left *exactComparisonBuildEntry,
	right *exactComparisonBuildEntry,
) int {
	if order := cmp.Compare(left.identity.kind, right.identity.kind); order != 0 {
		return order
	}
	switch left.identity.kind {
	case exactComparisonOccurrence:
		return cmp.Compare(left.unitGroup, right.unitGroup)
	case exactComparisonUnit:
		if order := cmp.Compare(
			left.identity.repository, right.identity.repository,
		); order != 0 {
			return order
		}
		return cmp.Compare(left.identity.unit, right.identity.unit)
	case exactComparisonUnresolved:
		if exactComparisonIdentityLess(left.identity, right.identity) {
			return -1
		}
		if exactComparisonIdentityLess(right.identity, left.identity) {
			return 1
		}
	}
	return 0
}

func exactComparisonSourceRefLess(
	left exactComparisonRef,
	right exactComparisonRef,
	oldIndex *exactCallerIndex,
	replacementIndex *exactCallerIndex,
	oldSource exactCallerComparisonSource,
	replacementSource exactCallerComparisonSource,
) bool {
	leftIndex, leftSource := replacementIndex, replacementSource
	if left.old {
		leftIndex, leftSource = oldIndex, oldSource
	}
	rightIndex, rightSource := replacementIndex, replacementSource
	if right.old {
		rightIndex, rightSource = oldIndex, oldSource
	}
	if leftSource.generation.Repository != rightSource.generation.Repository {
		return leftSource.generation.Repository < rightSource.generation.Repository
	}
	if leftSource.generation.Commit != rightSource.generation.Commit {
		return leftSource.generation.Commit < rightSource.generation.Commit
	}
	return exactCallerRecordLess(
		leftIndex.records[left.position], rightIndex.records[right.position], false,
	)
}

func exactComparisonIdentityLess(
	left exactComparisonIdentity,
	right exactComparisonIdentity,
) bool {
	if left.kind != right.kind {
		return left.kind < right.kind
	}
	if left.repository != right.repository {
		return left.repository < right.repository
	}
	if left.commit != right.commit {
		return left.commit < right.commit
	}
	if left.path != right.path {
		return left.path < right.path
	}
	if left.start != right.start {
		return left.start < right.start
	}
	if left.end != right.end {
		return left.end < right.end
	}
	return left.unit < right.unit
}

func (s *CallerComparisonService) pageExactCallerComparison(
	ctx context.Context,
	reads *exactComparisonReads,
	indexes *exactComparisonIndexes,
	binding *exactCallerBinding,
	query CallerComparisonQuery,
	pageSize int,
	offset int,
) (*CallerComparisonPage, error) {
	comparison := binding.comparison
	if comparison == nil || offset < 0 || offset > len(comparison.entries) {
		return nil, huma.Error409Conflict("caller comparison cursor is no longer valid")
	}
	end := min(offset+pageSize, len(comparison.entries))
	rows := make([]CallerComparisonRow, 0, end-offset)
	remainingCitations := exactCallerComparisonPageCitationLimit
	for _, entry := range comparison.entries[offset:end] {
		row, err := s.hydrateExactComparisonEntry(
			ctx, reads, indexes, binding, query, entry, &remainingCitations,
		)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	pagination := CallerMapPagination{Complete: end == len(comparison.entries)}
	if !pagination.Complete {
		pagination.NextCursor = s.exact.encodeSigned(exactCallerComparisonCursor{
			Schema: exactCallerComparisonCursorVersion, Binding: binding.id,
			QueryDigest:           binding.queryDigest,
			OldVisibility:         comparison.old.visibility,
			ReplacementVisibility: comparison.replacement.visibility,
			Snapshot:              comparison.snapshot, Offset: end,
		})
	}
	total := len(comparison.entries)
	oldDeclaration, replacementDeclaration :=
		comparison.old.declaration, comparison.replacement.declaration
	oldGeneration, replacementGeneration :=
		comparison.old.generation, comparison.replacement.generation
	return &CallerComparisonPage{
		SchemaVersion: callerComparisonSchemaVersion, Query: query,
		Old: CallerComparisonSnapshot{
			Endpoint: query.Old, Declaration: &oldDeclaration,
			Generation: &oldGeneration, MatchingRowsState: "exact",
		},
		Replacement: CallerComparisonSnapshot{
			Endpoint: query.Replacement, Declaration: &replacementDeclaration,
			Generation: &replacementGeneration, MatchingRowsState: "exact",
		},
		Rows: rows, TotalRows: &total, MatchingRowsState: "exact",
		Pagination: pagination,
		Caveat:     "Repository-overlay static source evidence only; old-only, both, new-only, and unresolved describe matching rows in two exact generations, not runtime use, completeness, migration completion, or decommissioning safety.",
	}, nil
}

func (s *CallerComparisonService) hydrateExactComparisonEntry(
	ctx context.Context,
	reads *exactComparisonReads,
	indexes *exactComparisonIndexes,
	binding *exactCallerBinding,
	query CallerComparisonQuery,
	entry exactCallerComparisonEntry,
	remaining *int,
) (CallerComparisonRow, error) {
	representativeIndex := indexes.replacement
	representativeSource := binding.comparison.replacement
	if entry.representativeOld {
		representativeIndex = indexes.old
		representativeSource = binding.comparison.old
	}
	representative := representativeIndex.records[entry.representative]
	identity, _, candidate, hasUnit := exactComparisonIdentityFor(
		query.Level, representativeSource.generation, representative,
	)
	if entry.kind == exactComparisonUnresolved {
		identity, _, candidate, hasUnit = exactComparisonUnresolvedIdentity(
			representativeSource.generation, representative,
		)
	}
	key := exactComparisonDisplayKey(identity)
	var unit *CallerMapUnitCandidate
	if hasUnit {
		owned := candidate
		unit = &owned
	}
	oldSide, err := s.hydrateExactComparisonSide(
		ctx, reads.old, indexes.old, binding, "old",
		entry.oldCount, binding.records[entry.oldStart:entry.oldEnd], remaining,
		entry.kind == exactComparisonUnit,
	)
	if err != nil {
		return CallerComparisonRow{}, err
	}
	replacementSide, err := s.hydrateExactComparisonSide(
		ctx, reads.replacement, indexes.replacement, binding, "replacement",
		entry.replacementCount,
		binding.records[entry.replacementStart:entry.replacementEnd], remaining,
		entry.kind == exactComparisonUnit,
	)
	if err != nil {
		return CallerComparisonRow{}, err
	}
	classification := "new_only_evidence"
	switch {
	case entry.unresolved:
		classification = "unresolved"
	case entry.oldCount > 0 && entry.replacementCount > 0:
		classification = "both_evidence"
	case entry.oldCount > 0:
		classification = "old_only_evidence"
	}
	return CallerComparisonRow{
		Level: query.Level, Key: key, Classification: classification,
		Unit: unit, Old: oldSide, Replacement: replacementSide,
	}, nil
}

func exactComparisonDisplayKey(identity exactComparisonIdentity) string {
	occurrence := fmt.Sprintf(
		"%s@%s:%s:%d-%d", identity.repository, identity.commit,
		identity.path, identity.start, identity.end,
	)
	switch identity.kind {
	case exactComparisonUnit:
		return identity.repository + ":" + identity.unit
	case exactComparisonUnresolved:
		return "unresolved:" + occurrence
	default:
		return occurrence
	}
}

func (s *CallerComparisonService) hydrateExactComparisonSide(
	ctx context.Context,
	read *callerexecute.PublicationRead,
	index *exactCallerIndex,
	binding *exactCallerBinding,
	side string,
	count int,
	positions []int,
	remaining *int,
	unitPromoted bool,
) (CallerComparisonSide, error) {
	limit := min(len(positions), max(0, *remaining))
	result := CallerComparisonSide{
		OccurrenceCount: count,
		Rows:            make([]CallerMapRow, 0, limit),
	}
	for _, position := range positions[:limit] {
		indexed := index.records[position]
		pair, record, err := read.Lease().ReadRecord(ctx, indexed.reference)
		if err != nil {
			if errors.Is(err, callerleaf.ErrInvalidArtifact) {
				return CallerComparisonSide{},
					huma.Error409Conflict("caller comparison generation changed while reading")
			}
			return CallerComparisonSide{},
				huma.Error500InternalServerError("read caller comparison record", err)
		}
		confirmed, err := projectExactCallerRecord(
			callerReadGeneration(read), pair, indexed.reference, record,
		)
		if err != nil || !sameExactCallerRecord(indexed, confirmed) {
			return CallerComparisonSide{},
				huma.Error409Conflict("caller comparison generation changed while reading")
		}
		row := exactCallerRow(read, indexed)
		if unitPromoted {
			// A resolved unit entry has exactly one candidate and the binding
			// proved it identical on every contributing occurrence. Serialize
			// that potentially near-record-sized attribution once on the outer
			// comparison row; citation samples retain only their state/reason and
			// immutable source identity instead of multiplying the payload.
			row.Unit = CallerMapUnitAttribution{
				State: row.Unit.State, Reason: row.Unit.Reason,
			}
		}
		citation, err := s.exact.encodeComparisonCitation(
			binding, side, position, indexed,
		)
		if err != nil {
			return CallerComparisonSide{},
				huma.Error500InternalServerError("encode caller comparison citation", err)
		}
		row.Source.Citation = citation
		result.Rows = append(result.Rows, row)
		(*remaining)--
	}
	result.RowsTruncated = count > len(result.Rows)
	return result, nil
}

func (s *CallerComparisonService) continueExactComparison(
	ctx context.Context,
	query CallerComparisonQuery,
	pageSize int,
	encoded string,
	queryDigest string,
	oldAuthorization exactComparisonAuthorization,
	replacementAuthorization exactComparisonAuthorization,
) (*CallerComparisonPage, error) {
	var cursor exactCallerComparisonCursor
	if len(encoded) > callerMapCursorLimit ||
		s.exact.decodeSigned(encoded, &cursor) != nil ||
		cursor.Schema != exactCallerComparisonCursorVersion {
		return nil, huma.Error400BadRequest("caller comparison cursor is invalid")
	}
	if cursor.QueryDigest != queryDigest {
		return nil, huma.Error400BadRequest(
			"caller comparison cursor does not match the query",
		)
	}
	if cursor.OldVisibility != oldAuthorization.visibility ||
		cursor.ReplacementVisibility != replacementAuthorization.visibility {
		return nil, huma.Error409Conflict("caller comparison cursor is no longer valid")
	}
	binding := s.exact.acquireBinding(cursor.Binding)
	if binding == nil {
		return nil, huma.Error409Conflict("caller comparison cursor is no longer valid")
	}
	defer s.exact.releaseBinding(binding)
	comparison := binding.comparison
	if comparison == nil || binding.queryDigest != queryDigest ||
		comparison.snapshot != cursor.Snapshot ||
		comparison.old.visibility != oldAuthorization.visibility ||
		comparison.replacement.visibility != replacementAuthorization.visibility ||
		cursor.Offset < 1 || cursor.Offset >= len(comparison.entries) {
		return nil, huma.Error409Conflict("caller comparison cursor is no longer valid")
	}
	reads, err := s.reopenExactComparisonReads(ctx, comparison)
	if err != nil {
		s.exact.dropBinding(binding.id)
		return nil, err
	}
	defer reads.release()
	if reads.old.Availability != callerexecute.PublicationCurrent ||
		reads.replacement.Availability != callerexecute.PublicationCurrent {
		s.exact.dropBinding(binding.id)
		return nil, huma.Error409Conflict("caller comparison cursor is no longer valid")
	}
	indexes := s.acquireExactComparisonIndexes(comparison)
	if indexes == nil {
		s.exact.dropBinding(binding.id)
		return nil, huma.Error409Conflict("caller comparison cursor snapshot expired")
	}
	defer indexes.release(s.exact)
	page, err := s.pageExactCallerComparison(
		ctx, reads, indexes, binding, query, pageSize, cursor.Offset,
	)
	if err != nil {
		s.exact.dropBinding(binding.id)
		return nil, err
	}
	if err := s.confirmExactComparisonCurrent(
		ctx, reads, oldAuthorization, replacementAuthorization,
	); err != nil {
		s.exact.dropBinding(binding.id)
		return nil, err
	}
	return page, nil
}

func (s *CallerComparisonService) reopenExactComparisonReads(
	ctx context.Context,
	comparison *exactCallerComparisonBinding,
) (*exactComparisonReads, error) {
	result := &exactComparisonReads{}
	oldRead, err := s.opts.CallerReader.Reopen(ctx, comparison.old.publication)
	if err != nil {
		return nil, exactCallerReaderError("reopen old caller comparison generation", err)
	}
	if oldRead == nil {
		return nil, huma.Error500InternalServerError(
			"reopen old caller comparison generation",
			errors.New("caller reader returned no state"),
		)
	}
	result.old = oldRead
	result.unique = append(result.unique, oldRead)
	if reflect.DeepEqual(
		comparison.old.publication, comparison.replacement.publication,
	) {
		result.replacement = oldRead
		return result, nil
	}
	replacementRead, err := s.opts.CallerReader.Reopen(
		ctx, comparison.replacement.publication,
	)
	if err != nil {
		result.release()
		return nil, exactCallerReaderError(
			"reopen replacement caller comparison generation", err,
		)
	}
	if replacementRead == nil {
		result.release()
		return nil, huma.Error500InternalServerError(
			"reopen replacement caller comparison generation",
			errors.New("caller reader returned no state"),
		)
	}
	result.replacement = replacementRead
	result.unique = append(result.unique, replacementRead)
	return result, nil
}

func (s *CallerComparisonService) acquireExactComparisonIndexes(
	comparison *exactCallerComparisonBinding,
) *exactComparisonIndexes {
	result := &exactComparisonIndexes{}
	oldIndex := s.exact.acquireIndex(comparison.old.indexKey)
	if oldIndex == nil {
		return nil
	}
	result.old = oldIndex
	result.unique = append(result.unique, oldIndex)
	if comparison.replacement.indexKey == comparison.old.indexKey {
		result.replacement = oldIndex
		return result
	}
	replacementIndex := s.exact.acquireIndex(comparison.replacement.indexKey)
	if replacementIndex == nil {
		result.release(s.exact)
		return nil
	}
	result.replacement = replacementIndex
	result.unique = append(result.unique, replacementIndex)
	return result
}

func (s *CallerComparisonService) confirmExactComparisonCurrent(
	ctx context.Context,
	reads *exactComparisonReads,
	oldAuthorization exactComparisonAuthorization,
	replacementAuthorization exactComparisonAuthorization,
) error {
	current, err := s.opts.CallerReader.CurrentPair(
		ctx, reads.old, reads.replacement,
	)
	if err != nil {
		return huma.Error500InternalServerError("confirm caller comparison generations", err)
	}
	if !current {
		return huma.Error409Conflict(
			"caller comparison generations changed while building the response",
		)
	}
	return s.confirmExactComparisonAuthorization(
		ctx, oldAuthorization, replacementAuthorization,
	)
}

func (s *CallerComparisonService) confirmExactComparisonGap(
	ctx context.Context,
	reads *exactComparisonReads,
	oldAuthorization exactComparisonAuthorization,
	replacementAuthorization exactComparisonAuthorization,
) error {
	for _, read := range reads.unique {
		var current bool
		var err error
		if read.Availability == callerexecute.PublicationCurrent {
			current, err = s.opts.CallerReader.Current(ctx, read)
		} else {
			current, err = s.opts.CallerReader.UnavailableCurrent(ctx, read)
		}
		if err != nil {
			return huma.Error500InternalServerError("confirm caller comparison gap", err)
		}
		if !current {
			return huma.Error409Conflict(
				"caller comparison generations changed while building the response",
			)
		}
	}
	return s.confirmExactComparisonAuthorization(
		ctx, oldAuthorization, replacementAuthorization,
	)
}

func (s *CallerComparisonService) confirmExactComparisonAuthorization(
	ctx context.Context,
	oldAuthorization exactComparisonAuthorization,
	replacementAuthorization exactComparisonAuthorization,
) error {
	if _, err := s.exact.confirmAuthorization(
		ctx, oldAuthorization.repository.Name, oldAuthorization.visibility,
	); err != nil {
		return err
	}
	if replacementAuthorization.repository.Name == oldAuthorization.repository.Name {
		if replacementAuthorization.visibility != oldAuthorization.visibility {
			return huma.Error409Conflict(
				"caller comparison authorization changed while building the response; retry",
			)
		}
		return nil
	}
	_, err := s.exact.confirmAuthorization(
		ctx, replacementAuthorization.repository.Name,
		replacementAuthorization.visibility,
	)
	return err
}

func exactCallerComparisonGapPage(
	service *exactCallerMapService,
	query CallerComparisonQuery,
	reads *exactComparisonReads,
	oldProjectionFailed bool,
	replacementProjectionFailed bool,
	oldDeclaration *ContractCatalogClaim,
	replacementDeclaration *ContractCatalogClaim,
) *CallerComparisonPage {
	oldGeneration := exactComparisonGeneration(
		service, reads.old, oldProjectionFailed,
	)
	replacementGeneration := exactComparisonGeneration(
		service, reads.replacement, replacementProjectionFailed,
	)
	oldState := "unavailable"
	if reads.old.Availability == callerexecute.PublicationCurrent &&
		!oldProjectionFailed {
		oldState = "exact"
	}
	replacementState := "unavailable"
	if reads.replacement.Availability == callerexecute.PublicationCurrent &&
		!replacementProjectionFailed {
		replacementState = "exact"
	}
	return &CallerComparisonPage{
		SchemaVersion: callerComparisonSchemaVersion, Query: query,
		Old: CallerComparisonSnapshot{
			Endpoint: query.Old, Declaration: oldDeclaration,
			Generation: &oldGeneration, MatchingRowsState: oldState,
		},
		Replacement: CallerComparisonSnapshot{
			Endpoint: query.Replacement, Declaration: replacementDeclaration,
			Generation:        &replacementGeneration,
			MatchingRowsState: replacementState,
		},
		Rows: []CallerComparisonRow{}, MatchingRowsState: "unavailable",
		Pagination: CallerMapPagination{Complete: true},
		Caveat:     "Caller comparison totals, classifications, and absence are unavailable until both authorized exact complete repository-overlay generations are current.",
	}
}

func exactComparisonGeneration(
	service *exactCallerMapService,
	read *callerexecute.PublicationRead,
	projectionFailed bool,
) CallerMapGeneration {
	generation := service.generation(read)
	if projectionFailed {
		generation.State = string(callerexecute.PublicationFailed)
		generation.Reason = "complete caller generation failed semantic projection validation"
	} else if read != nil && read.Availability != callerexecute.PublicationCurrent {
		generation.State = string(read.Availability)
		generation.Reason = "complete caller generation " + string(read.Availability)
	}
	if read != nil && read.Publication != nil {
		for _, pair := range read.Publication.Pairs {
			generation.ExcludedGoTestRecords += pair.Receipt.ExcludedGoTestRecords
		}
	}
	return generation
}

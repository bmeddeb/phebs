package retentionstatus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const MaxLocalizedDiagnosticsPerRequest = 9

func (collector *Collector) CollectRetention(
	ctx context.Context,
	requests []store.RetentionComponentRequest,
) (Collection, error) {
	if err := ctx.Err(); err != nil {
		return Collection{}, err
	}
	indexed, err := validateRequests(requests)
	if err != nil {
		return Collection{}, err
	}
	if collector == nil || collector.store == nil {
		return Collection{}, errors.New("collect derived retention: authority store is nil")
	}
	storeRequests := []store.RetentionComponentRequest{
		indexed[store.RetentionCandidatePublication],
		indexed[store.RetentionFocusedRepositoryState],
		indexed[store.RetentionResolverPublication],
		indexed[store.RetentionCallerArtifacts],
	}
	authorities, err := collector.store.CollectDerivedRetention(ctx, storeRequests)
	if err != nil {
		return Collection{}, err
	}
	if err := ctx.Err(); err != nil {
		return Collection{}, err
	}
	storeResults, err := normalizeStoreResults(indexed, authorities)
	if err != nil {
		return Collection{}, err
	}

	budget := newCollectionBudget(collector.policy, collector.hooks)
	dataRoot, rootErr := openStableRoot(ctx, collector.dataDir, budget)
	if dataRoot != nil {
		defer func() {
			_ = dataRoot.close()
		}()
	}
	dataVolume := collectDataVolume(
		dataRoot, collector.statFS, budget, rootErr,
	)
	if rootErr == nil {
		if reserveErr := budget.reserveStats(1); reserveErr != nil {
			rootErr = fmt.Errorf("reserve final retention data-root fence: %w", reserveErr)
			rootErr = errors.Join(rootErr, dataRoot.close())
			dataVolume = unavailableDataVolume(rootErr)
		}
	}

	candidatePointer := storeResults[store.RetentionCandidatePublication]
	focusedPointer := storeResults[store.RetentionFocusedRepositoryState]
	resolverPointer := storeResults[store.RetentionResolverPublication]
	if rootErr != nil {
		filesystemErr := fmt.Errorf("retention data root: %w", rootErr)
		if resolverPointer.Err == nil {
			resolverPointer.Diagnostics = append(
				resolverPointer.Diagnostics, filesystemErr,
			)
		}
		callerUnavailable := unavailableFilesystemResult(
			store.RetentionCallerArtifacts, filesystemErr,
		)
		if authorities.CallerErr != nil {
			callerUnavailable.Err = errors.Join(
				callerUnavailable.Err,
				fmt.Errorf("caller canonical receipts: %w", authorities.CallerErr),
			)
		}
		components := []ComponentResult{
			candidatePointer,
			unavailableFilesystemResult(store.RetentionCandidateFiles, filesystemErr),
			focusedPointer,
			unavailableFilesystemResult(store.RetentionFocusedFiles, filesystemErr),
			resolverPointer,
			unavailableFilesystemResult(store.RetentionResolverFiles, filesystemErr),
			callerUnavailable,
		}
		collection := Collection{Components: components, DataVolume: dataVolume}
		if err := validateDiagnosticBound(collection); err != nil {
			return Collection{}, err
		}
		return collection, nil
	}

	candidateScan := scanCandidateFilesAt(
		ctx, dataRoot, "candidates",
		indexed[store.RetentionCandidateFiles],
	)
	if err := cancellationError(ctx, candidateScan.err); err != nil {
		return Collection{}, err
	}
	focusedScan := scanFocusedFilesAt(
		ctx, dataRoot, "index",
		indexed[store.RetentionFocusedFiles],
	)
	if err := cancellationError(ctx, focusedScan.err); err != nil {
		return Collection{}, err
	}

	resolverPointer = collectResolverCanonical(
		ctx, dataRoot, resolverPointer, authorities.ResolverAuthorities,
	)
	if err := cancellationError(ctx, firstComponentError(resolverPointer)); err != nil {
		return Collection{}, err
	}
	resolverScan := scanResolverFilesAt(
		ctx, dataRoot, "resolver-catalogs",
		indexed[store.RetentionResolverFiles],
	)
	if err := cancellationError(ctx, resolverScan.err); err != nil {
		return Collection{}, err
	}

	callerScan := scanCallerFilesAt(
		ctx, dataRoot, "caller-leaves",
		indexed[store.RetentionCallerArtifacts],
	)
	if err := cancellationError(ctx, callerScan.err); err != nil {
		return Collection{}, err
	}
	callerResult := callerScan.componentResult(store.RetentionCallerArtifacts)
	callerResult = collectCallerCanonical(
		ctx, dataRoot, callerResult, authorities,
	)
	if err := cancellationError(ctx, firstComponentError(callerResult)); err != nil {
		return Collection{}, err
	}

	components := []ComponentResult{
		candidatePointer,
		candidateScan.componentResult(store.RetentionCandidateFiles),
		focusedPointer,
		focusedScan.componentResult(store.RetentionFocusedFiles),
		resolverPointer,
		resolverScan.componentResult(store.RetentionResolverFiles),
		callerResult,
	}
	if err := dataRoot.closeReserved(); err != nil {
		filesystemErr := fmt.Errorf("recheck retention data root: %w", err)
		localizeFilesystemFailure(components, filesystemErr)
		dataVolume = unavailableDataVolume(filesystemErr)
	}
	collection := Collection{Components: components, DataVolume: dataVolume}
	if err := validateDiagnosticBound(collection); err != nil {
		return Collection{}, err
	}
	return collection, nil
}

func normalizeStoreResults(
	requests map[store.RetentionComponent]store.RetentionComponentRequest,
	collection store.DerivedRetentionCollection,
) (map[store.RetentionComponent]ComponentResult, error) {
	if len(collection.Results) != derivedStoreRequestCount-1 {
		return nil, fmt.Errorf(
			"derived retention store returned %d results, want %d",
			len(collection.Results), derivedStoreRequestCount-1,
		)
	}
	wanted := map[store.RetentionComponent]struct{}{
		store.RetentionCandidatePublication:   {},
		store.RetentionFocusedRepositoryState: {},
		store.RetentionResolverPublication:    {},
	}
	results := make(map[store.RetentionComponent]ComponentResult, len(wanted))
	for _, observed := range collection.Results {
		if _, ok := wanted[observed.Component]; !ok {
			return nil, fmt.Errorf(
				"derived retention store returned unknown component %q", observed.Component,
			)
		}
		if _, duplicate := results[observed.Component]; duplicate {
			return nil, fmt.Errorf(
				"derived retention store returned duplicate component %q", observed.Component,
			)
		}
		result, err := componentFromStoreSummary(
			requests[observed.Component], observed,
			observed.Component == store.RetentionFocusedRepositoryState && collection.FocusedPartial,
		)
		if err != nil {
			return nil, err
		}
		results[observed.Component] = result
	}
	if len(results) != len(wanted) {
		return nil, errors.New("derived retention store omitted a component result")
	}
	if err := validateAuthorityCount(
		results[store.RetentionCandidatePublication], len(collection.CandidateAuthorities),
	); err != nil {
		return nil, fmt.Errorf("candidate retention authorities: %w", err)
	}
	if err := validateAuthorityCount(
		results[store.RetentionFocusedRepositoryState], len(collection.FocusedAuthorities),
	); err != nil {
		return nil, fmt.Errorf("focused retention authorities: %w", err)
	}
	if err := validateAuthorityCount(
		results[store.RetentionResolverPublication], len(collection.ResolverAuthorities),
	); err != nil {
		return nil, fmt.Errorf("resolver retention authorities: %w", err)
	}
	callerRequest := requests[store.RetentionCallerArtifacts]
	if collection.CallerErr == nil {
		if collection.CallerScannedIdentities < 0 ||
			collection.CallerScannedIdentities > callerRequest.ScanIdentities ||
			collection.CallerTruncated !=
				(collection.CallerScannedIdentities == callerRequest.ScanIdentities) {
			return nil, errors.New("caller retention authority summary is malformed")
		}
		selected := collection.CallerScannedIdentities
		if collection.CallerTruncated {
			selected = callerRequest.ReportedIdentities
		}
		if len(collection.CallerAuthorities) != selected {
			return nil, errors.New("caller retention authority count is malformed")
		}
	} else if len(collection.CallerAuthorities) != 0 ||
		collection.CallerScannedIdentities != 0 || collection.CallerTruncated {
		return nil, errors.New("failed caller retention authority returned partial structure")
	}
	return results, nil
}

func componentFromStoreSummary(
	request store.RetentionComponentRequest,
	observed store.RetentionComponentResult,
	partial bool,
) (ComponentResult, error) {
	result := ComponentResult{Component: observed.Component, Count: unavailableMetric()}
	if observed.Err != nil {
		result.Err = observed.Err
		return result, nil
	}
	summary := observed.Summary
	if summary.Bytes != nil || summary.ScannedIdentities < 0 ||
		summary.ScannedIdentities > request.ScanIdentities {
		return ComponentResult{}, fmt.Errorf(
			"derived retention store component %q returned malformed summary",
			observed.Component,
		)
	}
	if partial {
		if summary.Truncated || summary.ReportedIdentities != int64(summary.ScannedIdentities) {
			return ComponentResult{}, fmt.Errorf(
				"derived retention focused component returned malformed partial summary",
			)
		}
		if summary.ScannedIdentities == 0 {
			result.Err = errors.New("bounded repository prefix contains no focused authority")
			return result, nil
		}
		result.ScannedIdentities = summary.ScannedIdentities
		result.Count = metric(summary.ReportedIdentities, LowerBound)
		result.Diagnostics = []error{
			errors.New("focused repository inventory stopped at its raw-row observation bound"),
		}
		return result, nil
	}
	wantReported := summary.ScannedIdentities
	wantTruncated := summary.ScannedIdentities == request.ScanIdentities
	if wantTruncated {
		wantReported = request.ReportedIdentities
	}
	if summary.ReportedIdentities != int64(wantReported) ||
		summary.Truncated != wantTruncated {
		return ComponentResult{}, fmt.Errorf(
			"derived retention store component %q count disagrees with allocation",
			observed.Component,
		)
	}
	completeness := Exact
	if summary.Truncated {
		completeness = LowerBound
	}
	result.ScannedIdentities = summary.ScannedIdentities
	result.Count = metric(summary.ReportedIdentities, completeness)
	result.Truncated = summary.Truncated
	return result, nil
}

func validateAuthorityCount(result ComponentResult, authorities int) error {
	if result.Err != nil {
		if authorities != 0 {
			return errors.New("failed component returned authorities")
		}
		return nil
	}
	if result.Count.Value == nil || int64(authorities) != *result.Count.Value {
		return fmt.Errorf("authority count %d disagrees with reported count", authorities)
	}
	return nil
}

func collectResolverCanonical(
	ctx context.Context,
	dataRoot *stableRoot,
	result ComponentResult,
	authorities []store.ResolverCatalogPublication,
) ComponentResult {
	result.ByteMetrics = []ByteMetric{byteMetric(CanonicalContent, unavailableMetric())}
	if result.Err != nil {
		return result
	}
	if len(authorities) == 0 {
		if result.Count.Completeness == Exact {
			result.ByteMetrics[0].Metric = metric(0, Exact)
		}
		return result
	}
	directory, err := openCanonicalRootAt(ctx, dataRoot, "resolver-catalogs")
	if err != nil {
		result.Diagnostics = append(
			result.Diagnostics, fmt.Errorf("resolver canonical receipts: %w", err),
		)
		return result
	}
	var canonical int64
	matched := 0
	var firstErr error
	for _, authority := range authorities {
		if err := ctx.Err(); err != nil {
			firstErr = err
			break
		}
		raw, readErr := directory.readRegular(
			ctx, authority.ManifestPath, resolvercatalog.MaxManifestBytes,
		)
		if readErr != nil {
			firstErr = readErr
			break
		}
		state, bytes, parseErr := resolvercatalog.ParseRetentionManifest(raw)
		if parseErr != nil || !resolverAuthorityMatches(state, authority) {
			if parseErr == nil {
				parseErr = errors.New("resolver manifest receipt disagrees with store authority")
			}
			firstErr = parseErr
			break
		}
		next, ok := addCanonicalBytes(canonical, bytes)
		if !ok {
			firstErr = errors.New("resolver canonical receipt total overflows int64")
			break
		}
		canonical = next
		matched++
	}
	firstErr = errors.Join(firstErr, directory.close())
	if firstErr != nil {
		result.Diagnostics = append(
			result.Diagnostics, fmt.Errorf("resolver canonical receipts: %w", firstErr),
		)
	}
	switch {
	case matched == len(authorities) && firstErr == nil &&
		result.Count.Completeness == Exact:
		result.ByteMetrics[0].Metric = metric(canonical, Exact)
	case matched > 0:
		result.ByteMetrics[0].Metric = metric(canonical, LowerBound)
	}
	return result
}

func resolverAuthorityMatches(
	state resolvercatalog.State,
	authority store.ResolverCatalogPublication,
) bool {
	if state.Schema != resolvercatalog.StateSchema ||
		state.Repository != authority.Repository || state.Commit != authority.HeadCommit ||
		state.UnitDigest != authority.UnitDigest ||
		state.DeclarationSetDigest != authority.DeclarationSetDigest ||
		state.CandidateManifestDigest != authority.CandidateManifestDigest ||
		state.SourceLanePolicy != authority.SourceLanePolicy ||
		state.ResolverPackSetDigest != authority.ResolverPackSetDigest ||
		state.CatalogPolicyDigest != authority.CatalogPolicyDigest ||
		state.GenerationDigest != authority.GenerationDigest ||
		state.ManifestDigest != authority.ManifestDigest ||
		state.Manifest != authority.ManifestPath ||
		len(state.Declarations) != len(authority.Declarations) ||
		len(state.ResolverPacks) != len(authority.ResolverPacks) {
		return false
	}
	for index := range state.Declarations {
		left, right := state.Declarations[index], authority.Declarations[index]
		if left.Domain != right.Domain || left.RunID != right.RunID ||
			left.GenerationDigest != right.GenerationDigest {
			return false
		}
	}
	for index := range state.ResolverPacks {
		left, right := state.ResolverPacks[index], authority.ResolverPacks[index]
		if left.Name != right.Name || left.Version != right.Version {
			return false
		}
	}
	return true
}

func collectCallerCanonical(
	ctx context.Context,
	dataRoot *stableRoot,
	result ComponentResult,
	authorities store.DerivedRetentionCollection,
) ComponentResult {
	apparent := result.ByteMetrics
	result.ByteMetrics = []ByteMetric{
		byteMetric(CanonicalReceipt, unavailableMetric()),
	}
	result.ByteMetrics = append(result.ByteMetrics, apparent...)
	if authorities.CallerErr != nil {
		result.Diagnostics = append(
			result.Diagnostics,
			fmt.Errorf("caller canonical receipts: %w", authorities.CallerErr),
		)
		return result
	}
	if result.Err != nil {
		return result
	}
	if len(authorities.CallerAuthorities) == 0 {
		if !authorities.CallerTruncated {
			result.ByteMetrics[0].Metric = metric(0, Exact)
		}
		return result
	}
	var canonical int64
	matched := 0
	var firstErr error
	for _, authority := range authorities.CallerAuthorities {
		repositoryDirectory := callerpublicationid.RepositoryDirectory(
			authority.Generation.Repository,
		)
		callerRoot, openErr := openCanonicalRootAt(ctx, dataRoot, "caller-leaves")
		if openErr != nil {
			firstErr = openErr
			break
		}
		repositoryRoot, repositoryErr := callerRoot.openChild(
			ctx, repositoryDirectory,
		)
		callerCloseErr := callerRoot.close()
		if repositoryErr != nil || callerCloseErr != nil {
			if repositoryRoot != nil {
				repositoryErr = errors.Join(repositoryErr, repositoryRoot.close())
			}
			firstErr = errors.Join(repositoryErr, callerCloseErr)
			break
		}
		raw, readErr := repositoryRoot.readRegular(
			ctx, authority.ManifestPath, callerpublication.MaxManifestBytes,
		)
		state, bytes, parseErr := callerpublication.ParseRetentionManifest(raw)
		repositoryCloseErr := repositoryRoot.close()
		if readErr != nil || parseErr != nil || repositoryCloseErr != nil ||
			!callerAuthorityMatches(state, authority) {
			firstErr = errors.Join(readErr, parseErr, repositoryCloseErr)
			if firstErr == nil {
				firstErr = errors.New("caller manifest receipt disagrees with store authority")
			}
			break
		}
		next, ok := addCanonicalBytes(canonical, bytes)
		if !ok {
			firstErr = errors.New("caller canonical receipt total overflows int64")
			break
		}
		canonical = next
		matched++
	}
	if firstErr != nil {
		result.Diagnostics = append(
			result.Diagnostics, fmt.Errorf("caller canonical receipts: %w", firstErr),
		)
	}
	switch {
	case matched == len(authorities.CallerAuthorities) && firstErr == nil &&
		!authorities.CallerTruncated:
		result.ByteMetrics[0].Metric = metric(canonical, Exact)
	case matched > 0:
		result.ByteMetrics[0].Metric = metric(canonical, LowerBound)
	}
	return result
}

func addCanonicalBytes(total, next int64) (int64, bool) {
	if total < 0 || next < 0 || next > math.MaxInt64-total {
		return 0, false
	}
	return total + next, true
}

func callerAuthorityMatches(
	state callerpublication.State,
	authority store.CallerGenerationPublicationSummary,
) bool {
	semantic := state.Generation
	stored := authority.Generation
	return state.Schema == callerpublication.StateSchema &&
		semantic.Repository == stored.Repository &&
		semantic.HeadCommit == stored.HeadCommit &&
		semantic.UnitDigest == stored.UnitDigest &&
		semantic.DeclarationSetDigest == stored.DeclarationSetDigest &&
		semantic.CandidateManifestDigest == stored.CandidateManifestDigest &&
		semantic.CandidatePolicyDigest == stored.CandidatePolicyDigest &&
		semantic.ResolverGenerationDigest == stored.ResolverGenerationDigest &&
		semantic.ResolverManifestDigest == stored.ResolverManifestDigest &&
		semantic.SourceLanePolicy == stored.SourceLanePolicy &&
		semantic.CallerPolicyDigest == stored.CallerPolicyDigest &&
		semantic.ExtractorSetDigest == stored.ExtractorSetDigest &&
		semantic.Digest == stored.Digest &&
		state.PairSetDigest == authority.PairSetDigest &&
		state.Aggregate.PairCount == authority.PairCount &&
		state.Aggregate.ArtifactCount == authority.ArtifactCount &&
		state.Aggregate.ResultCount == authority.ResultCount &&
		state.Aggregate.AbstentionCount == authority.AbstentionCount &&
		state.Aggregate.CanonicalBytes == authority.CanonicalBytes &&
		state.Aggregate.StagingBytes == authority.StagingBytes &&
		state.Aggregate.PeakOpenFiles == authority.PeakOpenFiles &&
		state.ManifestDigest == authority.ManifestDigest &&
		state.Manifest == authority.ManifestPath
}

func collectDataVolume(
	dataRoot *stableRoot,
	statFS statFSFunc,
	budget *collectionBudget,
	rootErr error,
) DataVolumeResult {
	if rootErr != nil {
		return unavailableDataVolume(fmt.Errorf("retention data root: %w", rootErr))
	}
	if statFS == nil {
		return unavailableDataVolume(errors.New("retention data-volume statfs is unavailable"))
	}
	if budget == nil {
		return unavailableDataVolume(errors.New("retention data-volume budget is unavailable"))
	}
	if dataRoot == nil || dataRoot.closed {
		return unavailableDataVolume(errors.New("retention data-volume root is unavailable"))
	}
	if err := budget.stat(); err != nil {
		return unavailableDataVolume(fmt.Errorf("retention data-volume statfs: %w", err))
	}
	file, err := dataRoot.root.Open(directoryTraversal("."))
	if err != nil {
		return unavailableDataVolume(fmt.Errorf("retention data-volume descriptor: %w", err))
	}
	if err := budget.openedDescriptor(); err != nil {
		_ = file.Close()
		return unavailableDataVolume(fmt.Errorf("retention data-volume descriptor: %w", err))
	}
	// The charge before Root.Open covers its platform-dependent fstat. Fstatfs
	// is a separate metadata operation and owns this second budget slot.
	if err := budget.stat(); err != nil {
		closeErr := file.Close()
		budget.closedDescriptor()
		return unavailableDataVolume(fmt.Errorf(
			"retention data-volume statfs: %w", errors.Join(err, closeErr),
		))
	}
	observed, statErr := statFS(file)
	closeErr := file.Close()
	budget.closedDescriptor()
	if err := errors.Join(statErr, closeErr); err != nil {
		return unavailableDataVolume(fmt.Errorf("retention data-volume statfs: %w", err))
	}
	if observed.BlockSize == 0 ||
		observed.Blocks > uint64(math.MaxInt64)/observed.BlockSize ||
		observed.AvailableBlocks > uint64(math.MaxInt64)/observed.BlockSize ||
		observed.AvailableBlocks > observed.Blocks {
		return unavailableDataVolume(errors.New("retention data-volume statfs values overflow or disagree"))
	}
	total := int64(observed.Blocks * observed.BlockSize)
	available := int64(observed.AvailableBlocks * observed.BlockSize)
	return DataVolumeResult{
		TotalBytes: metric(total, Exact), AvailableBytes: metric(available, Exact),
	}
}

func unavailableDataVolume(err error) DataVolumeResult {
	return DataVolumeResult{
		TotalBytes: unavailableMetric(), AvailableBytes: unavailableMetric(),
		Diagnostics: []error{err},
	}
}

func unavailableFilesystemResult(
	component store.RetentionComponent,
	err error,
) ComponentResult {
	byteMetrics := []ByteMetric{byteMetric(ApparentFile, unavailableMetric())}
	if component == store.RetentionCallerArtifacts {
		byteMetrics = []ByteMetric{
			byteMetric(CanonicalReceipt, unavailableMetric()),
			byteMetric(ApparentFile, unavailableMetric()),
		}
	}
	return ComponentResult{
		Component: component, Count: unavailableMetric(), ByteMetrics: byteMetrics, Err: err,
	}
}

func localizeFilesystemFailure(components []ComponentResult, err error) {
	for index := range components {
		component := &components[index]
		switch component.Component {
		case store.RetentionCandidateFiles, store.RetentionFocusedFiles,
			store.RetentionResolverFiles, store.RetentionCallerArtifacts:
			previousErr := component.Err
			previousDiagnostics := component.Diagnostics
			joined := []error{err, previousErr}
			joined = append(joined, previousDiagnostics...)
			*component = unavailableFilesystemResult(
				component.Component, errors.Join(joined...),
			)
		case store.RetentionResolverPublication:
			if component.Err != nil {
				continue
			}
			for metricIndex := range component.ByteMetrics {
				if component.ByteMetrics[metricIndex].Kind == CanonicalContent {
					component.ByteMetrics[metricIndex].Metric = unavailableMetric()
				}
			}
			joined := append(slices.Clone(component.Diagnostics), err)
			component.Diagnostics = []error{errors.Join(joined...)}
		}
	}
}

func cancellationError(ctx context.Context, observed error) error {
	_ = observed
	return ctx.Err()
}

func firstComponentError(result ComponentResult) error {
	if result.Err != nil {
		return result.Err
	}
	if len(result.Diagnostics) > 0 {
		return result.Diagnostics[0]
	}
	return nil
}

func validateDiagnosticBound(collection Collection) error {
	diagnostics := len(collection.DataVolume.Diagnostics)
	for _, component := range collection.Components {
		if component.Err != nil {
			diagnostics++
		}
		diagnostics += len(component.Diagnostics)
	}
	if diagnostics > MaxLocalizedDiagnosticsPerRequest {
		return fmt.Errorf(
			"derived retention emitted %d diagnostics above bound %d",
			diagnostics, MaxLocalizedDiagnosticsPerRequest,
		)
	}
	return nil
}

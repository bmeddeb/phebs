package t421

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
)

// The actual StartRestored handoff supplies both private inputs. This constructor
// accepts neither an external prior nor a path; Health alone is not acceptance.
func (run *ExecutionEpochOneRun) newArchiveInspection(ctx context.Context) (*executionEpochInspection, error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.flow.epochs == nil || run.flow.epochs.author == nil {
		return nil, errEpochInspection
	}
	run.mu.Lock()
	if run.inspection != nil || run.stopping || run.err != nil || !run.healthy || run.healthDone == nil || run.control == nil ||
		run.epoch.Epoch != 5 || run.archiveInput == nil || !run.archiveInput.valid() || run.archivePrior == nil ||
		run.archivePrior.Phase != "pressure_75" || run.archivePrior.Outcome != "passed" {
		run.mu.Unlock()
		return nil, errEpochInspection
	}
	select {
	case <-run.healthDone:
	default:
		run.mu.Unlock()
		return nil, errEpochInspection
	}
	select {
	case <-run.stop:
		run.mu.Unlock()
		return nil, errEpochInspection
	default:
	}
	reader := &executionEpochInspection{run: run, err: errEpochInspection, archiveInput: *run.archiveInput,
		archivePrior: cloneArchiveAuthority(*run.archivePrior)}
	run.inspection = reader // A refused construction cannot reset the ordinal.
	run.mu.Unlock()
	epochs, author := run.flow.epochs, run.flow.epochs.author
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if author.borrowedBy != run || author.active || author.next != 3 || author.previous == nil || author.previous.Result != author.expected[2] ||
		epochs.released != 5 || !epochs.active || epochs.checkLocked(ctx, 5) != nil {
		return nil, errEpochInspection
	}
	plan := run.flow.plan
	projection, err := expectedStateProjectionForPhase(plan, "archive_restore")
	rows, totals, inventoryErr := correctedInspectionInventory(plan.Profile)
	if plan.Schema != PlanV3Schema || err != nil || inventoryErr != nil || len(rows) != 15 || rows[11].Phase != "archive_restore" || rows[11].ServerEpoch != 5 ||
		projection.PhysicalRevision != "a-return" || projection.LogicalRevision != "a-return" || projection.CatalogSource.SHA256 != run.epoch.CatalogSHA256 {
		return nil, errEpochInspection
	}
	for _, epoch := range totals {
		if epoch.ServerEpoch == 5 {
			reader.maximumReports = epoch.AccountedServerRequestsMaximum
		}
	}
	if reader.maximumReports == 0 || rows[11].TransitionRead == nil || *rows[11].TransitionRead != correctedArchiveTransitionReadBound() {
		return nil, errEpochInspection
	}
	reader.plan, reader.authored, reader.projection, reader.bounds, reader.next, reader.err = plan, author.previous.Result, projection, rows[11], 1, nil
	return reader, nil
}

// Archive reads only the existing exact operation report. It retains no invented
// archive event ordinals, empty-target counts, or full state-inventory evidence.
func (reader *executionEpochInspection) Archive(ctx context.Context) (result recovery.ArchiveTransitionManifest, report epochInspectionReport, retErr error) {
	if reader == nil {
		return result, report, errEpochInspection
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || reader.run == nil || reader.run.epoch.Epoch != 5 || reader.projection.Phase != "archive_restore" ||
		reader.archiveUsed || reader.next != 1 || reader.progressCalls != 0 || reader.tailCalls != 0 || reader.finalUsed ||
		!reader.archiveInput.valid() || reader.bounds.TransitionRead == nil || *reader.bounds.TransitionRead != correctedArchiveTransitionReadBound() {
		return result, report, errEpochInspection
	}
	reader.archiveUsed = true
	raw, status, report, err := reader.read(ctx, "/api/t422/archive/transition", 1<<20, epochInspectionReport{ControlFileReads: 1})
	if err != nil || status != http.StatusOK || report.ControlFileReads != 1 || decodeEpochJSON(raw, &result, false) != nil ||
		result.ManifestSchema != recovery.ManifestSchema || result.ManifestSHA256 != reader.archiveInput.BackupCommandSHA256 ||
		validateEpochArchiveManifest(result) != nil {
		return result, report, errEpochInspection
	}
	retained := result
	retained.Components, retained.Reports = slices.Clone(result.Components), slices.Clone(result.Reports)
	reader.archiveManifest = &retained
	return result, report, nil
}

func validateEpochArchiveManifest(value recovery.ArchiveTransitionManifest) error {
	if len(value.Components) != 6 || len(value.Reports) != 5 {
		return errEpochInspection
	}
	components := make([]ArchiveComponent, len(value.Components))
	for i, v := range value.Components {
		components[i] = ArchiveComponent(v)
	}
	if _, err := archiveManifestInventory(components); err != nil {
		return errEpochInspection
	}
	reports := make([]ArchiveReportProjection, len(value.Reports))
	for i, v := range value.Reports {
		reports[i] = ArchiveReportProjection{Name: v.Name, Schema: v.Schema, Publications: v.Publications,
			V1Publications: v.V1Publications, V2Publications: v.V2Publications, Files: v.Files, Bytes: v.Bytes}
	}
	if _, err := archiveReportInventory(reports); err != nil {
		return errEpochInspection
	}
	return nil
}

func cloneArchiveAuthority(value AuthorityPhaseResult) AuthorityPhaseResult {
	value.ExtractionRoots = slices.Clone(value.ExtractionRoots)
	for i := range value.ExtractionRoots {
		value.ExtractionRoots[i].PartitionResults = slices.Clone(value.ExtractionRoots[i].PartitionResults)
	}
	return value
}

func (reader *executionEpochInspection) restoredCommand(ctx context.Context, operation string) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || reader.run.epoch.Epoch != 5 || reader.run.control == nil || reader.finalUsed {
		return errEpochInspection
	}
	valid := operation == "park" && reader.restoredStep == 0 && reader.projection.Phase == "archive_restore" && reader.archiveManifest != nil && reader.progressReady && reader.tail.Status == "ready" ||
		operation == "drive-fresh" && reader.restoredStep == 1 && reader.projection.Phase == "lifecycle_collection" &&
			reader.restoredSamples.Phases[1].Completed == 1 && reader.restoredSamples.ArchiveComplete
	if !valid || reader.lifecycleCommand(ctx, operation, time.Time{}) != nil {
		return errEpochInspection
	}
	reader.restoredStep++
	return nil
}

// Preserve the actual archive F and the epoch-wide exact ordinal. Only the
// accepted, fenced prior phase can supply this phase13 baseline.
func (reader *executionEpochInspection) beginCollection() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || reader.run == nil || reader.run.epoch.Epoch != 5 || !reader.finalUsed || reader.restoredStep != 1 ||
		reader.projection.Phase != "archive_restore" || reader.archiveAuthority.Phase != "archive_restore" ||
		!reader.restoredSamples.ArchiveComplete || len(reader.evidence.rows) == 0 || !reader.evidence.rows[len(reader.evidence.rows)-1].SelectorAccepted {
		return errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(reader.plan, "lifecycle_collection")
	rows, _, inventoryErr := correctedInspectionInventory(reader.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) != 15 || rows[12].Phase != "lifecycle_collection" || rows[12].ServerEpoch != 5 ||
		projection.CatalogSource.SHA256 != reader.run.epoch.CatalogSHA256 {
		return errEpochInspection
	}
	reader.projection, reader.bounds = projection, rows[12]
	reader.progressCalls, reader.tailCalls, reader.lifecycleCalls, reader.progressReady = 0, 0, 0, false
	reader.tail, reader.finalUsed = epochTailReadiness{}, false
	return nil
}

func (reader *executionEpochInspection) freshCycle(ctx context.Context) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || reader.run.epoch.Epoch != 5 ||
		reader.projection.Phase != "lifecycle_collection" || reader.restoredStep != 2 || reader.finalUsed || !reader.restoredSamples.ArchiveComplete {
		return errEpochInspection
	}
	var cycle lifecycle.CycleObservation
	raw, status, report, err := reader.read(ctx, "/api/t422/lifecycle/fresh-cycle", 16<<10, epochInspectionReport{})
	if err != nil || status != http.StatusOK || report.ControlFileReads != 0 || report.StoreReadAttempts != 0 || report.MemberVisits != 0 ||
		decodeEpochJSON(append(raw, '\n'), &cycle, false) != nil || !pressureCycleValid(cycle, true) {
		return errEpochInspection
	}
	reader.collectionCycle = cycle // Native values; never synthesized from L.
	reader.restoredStep++
	return nil
}

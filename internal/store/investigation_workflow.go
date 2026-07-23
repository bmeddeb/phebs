package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// InvestigationWorkflowStore is the T16.4 execution boundary. Guided
// creation freezes the first Revision and queues its Run atomically. Workers
// can mutate Run state only while holding the current attempt lease; terminal
// publication/failure closes that lease in the same transaction that creates
// the immutable RunArtifact.
type InvestigationWorkflowStore interface {
	CreateGuidedInvestigation(context.Context, GuidedInvestigationRequest) (*GuidedInvestigationCreation, error)
	GetRunAs(context.Context, string, string) (*Run, error)
	ListRunEventsAs(context.Context, string, string) ([]RunEvent, error)
	AcquireInvestigationRunLease(context.Context, string, string) (*InvestigationRunLease, error)
	AdvanceInvestigationRun(context.Context, InvestigationRunLease, RunState, string) (*RunEvent, error)
	RetryInvestigationRun(context.Context, InvestigationRunLease, string, int) (*RunEvent, error)
	FailInvestigationRun(context.Context, InvestigationRunLease, RunArtifact, string) (*RunArtifact, error)
	PublishInvestigationRun(context.Context, InvestigationRunLease, RunArtifact) (*RunArtifact, error)
	CancelInvestigationRunAs(context.Context, string, string, string) (*RunEvent, error)
	GetRunArtifactForRunAs(context.Context, string, string) (*RunArtifact, error)
}

var _ InvestigationWorkflowStore = (*Surreal)(nil)

const (
	// MaxInvestigationRunAttempts is the platform ceiling used when a caller
	// does not supply a stricter pack-specific bound.
	MaxInvestigationRunAttempts = 3

	investigationCoverageSchemaVersion = "investigation-coverage-ledger-v1"
)

// GuidedInvestigationRequest is already preflighted by the API boundary. The
// preview digest binds the caller-visible repository snapshot and all frozen
// Revision inputs, preventing a stale preview from silently creating a
// different Run.
type GuidedInvestigationRequest struct {
	IdempotencyKey          string   `json:"idempotency_key"`
	PreviewDigest           string   `json:"preview_digest"`
	Referent                string   `json:"referent"`
	ClaimFamily             string   `json:"claim_family"`
	Title                   string   `json:"title"`
	NormalizedQuestion      string   `json:"normalized_question"`
	DecisionSought          string   `json:"decision_sought"`
	DeclaredUniverse        string   `json:"declared_universe"`
	SnapshotPolicy          string   `json:"snapshot_policy"`
	BuildConfiguration      string   `json:"build_configuration"`
	PackSelection           string   `json:"pack_selection"`
	EnumerationMethod       string   `json:"enumeration_method"`
	ResolvedInputIdentities []string `json:"resolved_input_identities"`
	RequestedSnapshot       string   `json:"requested_snapshot"`
	InputManifestDigest     string   `json:"input_manifest_digest"`
	RequestedPackVersions   []string `json:"requested_pack_versions"`
	Principal               string   `json:"principal"`
}

// GuidedInvestigationCreation is the atomic result of one submission.
type GuidedInvestigationCreation struct {
	Investigation Investigation `json:"investigation"`
	Revision      Revision      `json:"revision"`
	Run           Run           `json:"run"`
}

// InvestigationRunLease fences one worker attempt. Token is intentionally
// omitted from JSON so it cannot accidentally enter an API response.
type InvestigationRunLease struct {
	RunID      string    `json:"run_id"`
	Attempt    int       `json:"attempt"`
	Worker     string    `json:"worker"`
	AcquiredAt time.Time `json:"acquired_at"`
	Token      string    `json:"-"`
}

// InvestigationCoverageFailure is a bounded diagnostic row. Unit identities
// are caller-visible logical inputs, never hidden repository discoveries.
type InvestigationCoverageFailure struct {
	Unit      string `json:"unit"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

// InvestigationCoverageLedger accounts for the entire preflighted universe.
// Partial and failed units remain first-class even when no fact set publishes.
type InvestigationCoverageLedger struct {
	SchemaVersion string                         `json:"schema_version"`
	EligibleUnits int                            `json:"eligible_units"`
	Analyzed      int                            `json:"analyzed"`
	Partial       int                            `json:"partial"`
	Failed        int                            `json:"failed"`
	Excluded      int                            `json:"excluded"`
	Failures      []InvestigationCoverageFailure `json:"failures"`
}

// CanonicalInvestigationCoverageLedger validates reconciliation and returns
// the deterministic JSON stored in RunArtifact.CoverageLedger.
func CanonicalInvestigationCoverageLedger(ledger InvestigationCoverageLedger) (string, error) {
	if ledger.SchemaVersion == "" {
		ledger.SchemaVersion = investigationCoverageSchemaVersion
	}
	if ledger.SchemaVersion != investigationCoverageSchemaVersion {
		return "", errors.New("unsupported investigation coverage ledger schema")
	}
	if ledger.EligibleUnits < 0 || ledger.Analyzed < 0 || ledger.Partial < 0 ||
		ledger.Failed < 0 || ledger.Excluded < 0 {
		return "", errors.New("investigation coverage counts cannot be negative")
	}
	if ledger.Analyzed+ledger.Partial+ledger.Failed+ledger.Excluded != ledger.EligibleUnits {
		return "", errors.New("investigation coverage counts do not reconcile")
	}
	if len(ledger.Failures) > 10_000 {
		return "", errors.New("investigation coverage failures exceed 10000 entries")
	}
	if len(ledger.Failures) == 0 {
		ledger.Failures = []InvestigationCoverageFailure{}
	} else {
		ledger.Failures = slices.Clone(ledger.Failures)
	}
	for i := range ledger.Failures {
		if err := validateDomainString("coverage failure unit", ledger.Failures[i].Unit, true); err != nil {
			return "", err
		}
		if err := validateDomainString("coverage failure code", ledger.Failures[i].Code, true); err != nil {
			return "", err
		}
	}
	slices.SortFunc(ledger.Failures, func(a, b InvestigationCoverageFailure) int {
		if a.Unit != b.Unit {
			return strings.Compare(a.Unit, b.Unit)
		}
		if a.Code != b.Code {
			return strings.Compare(a.Code, b.Code)
		}
		switch {
		case a.Retryable == b.Retryable:
			return 0
		case !a.Retryable:
			return -1
		default:
			return 1
		}
	})
	for i := 1; i < len(ledger.Failures); i++ {
		if ledger.Failures[i] == ledger.Failures[i-1] {
			return "", errors.New("investigation coverage contains duplicate failure rows")
		}
	}
	encoded, err := json.Marshal(ledger)
	if err != nil {
		return "", fmt.Errorf("encode investigation coverage: %w", err)
	}
	return string(encoded), nil
}

// ParseInvestigationCoverageLedger decodes the closed v1 shape, rejects
// unknown fields, trailing values, or unreconciled content, and canonicalizes it.
func ParseInvestigationCoverageLedger(value string) (*InvestigationCoverageLedger, string, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var ledger InvestigationCoverageLedger
	if err := decoder.Decode(&ledger); err != nil {
		return nil, "", fmt.Errorf("decode investigation coverage: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, "", errors.New("decode investigation coverage: trailing JSON value")
	}
	canonical, err := CanonicalInvestigationCoverageLedger(ledger)
	if err != nil {
		return nil, "", err
	}
	if err := json.Unmarshal([]byte(canonical), &ledger); err != nil {
		return nil, "", fmt.Errorf("decode canonical investigation coverage: %w", err)
	}
	return &ledger, canonical, nil
}

type guidedCreationRecord struct {
	Key             string `json:"creation_key"`
	Principal       string `json:"principal"`
	IdempotencyKey  string `json:"idempotency_key"`
	RequestDigest   string `json:"request_digest"`
	InvestigationID string `json:"investigation_id"`
	RevisionID      string `json:"revision_id"`
	RunID           string `json:"run_id"`
}

func guidedCreationRecordID(principal, idempotencyKey string) models.RecordID {
	return models.NewRecordID("investigation_creation", hashIdentity("icr_", principal, idempotencyKey))
}

func guidedCreationCore(request GuidedInvestigationRequest) any {
	return struct {
		IdempotencyKey, PreviewDigest, Referent, ClaimFamily, Title string
		NormalizedQuestion, DecisionSought, DeclaredUniverse        string
		SnapshotPolicy, BuildConfiguration, PackSelection           string
		EnumerationMethod, RequestedSnapshot, InputManifestDigest   string
		ResolvedInputIdentities, RequestedPackVersions              []string
		Principal                                                   string
	}{
		request.IdempotencyKey, request.PreviewDigest, request.Referent,
		request.ClaimFamily, request.Title, request.NormalizedQuestion,
		request.DecisionSought, request.DeclaredUniverse, request.SnapshotPolicy,
		request.BuildConfiguration, request.PackSelection,
		request.EnumerationMethod, request.RequestedSnapshot,
		request.InputManifestDigest, request.ResolvedInputIdentities,
		request.RequestedPackVersions, request.Principal,
	}
}

func normalizeGuidedInvestigationRequest(request GuidedInvestigationRequest) (GuidedInvestigationRequest, string, error) {
	for name, value := range map[string]string{
		"idempotency key": request.IdempotencyKey, "preview digest": request.PreviewDigest,
		"referent": request.Referent, "claim family": request.ClaimFamily,
		"title": request.Title, "normalized question": request.NormalizedQuestion,
		"decision sought": request.DecisionSought, "declared universe": request.DeclaredUniverse,
		"snapshot policy": request.SnapshotPolicy, "build configuration": request.BuildConfiguration,
		"pack selection": request.PackSelection, "enumeration method": request.EnumerationMethod,
		"requested snapshot":    request.RequestedSnapshot,
		"input manifest digest": request.InputManifestDigest, "principal": request.Principal,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return GuidedInvestigationRequest{}, "", err
		}
	}
	if !validSHA256Digest(request.PreviewDigest) || !validSHA256Digest(request.InputManifestDigest) {
		return GuidedInvestigationRequest{}, "", errors.New("preview and input manifest require sha256 digests")
	}
	if !validInvestigationPrincipal(request.Principal) {
		return GuidedInvestigationRequest{}, "", errors.New("invalid investigation principal")
	}
	var err error
	request.ResolvedInputIdentities, err = canonicalStrings("resolved input identity", request.ResolvedInputIdentities)
	if err != nil {
		return GuidedInvestigationRequest{}, "", err
	}
	request.RequestedPackVersions, err = canonicalStrings("requested pack version", request.RequestedPackVersions)
	if err != nil {
		return GuidedInvestigationRequest{}, "", err
	}
	if len(request.ResolvedInputIdentities) == 0 || len(request.RequestedPackVersions) == 0 {
		return GuidedInvestigationRequest{}, "", errors.New("resolved inputs and requested pack versions are required")
	}
	digest, err := contentDigest(guidedCreationCore(request))
	return request, digest, err
}

func (s *Surreal) getGuidedCreationRecord(ctx context.Context, principal, idempotencyKey string) (*guidedCreationRecord, error) {
	results, err := surrealdb.Query[[]guidedCreationRecord](ctx, s.db,
		`SELECT creation_key, principal, idempotency_key, request_digest,
			investigation_id, revision_id, run_id FROM $rid LIMIT 1`,
		map[string]any{"rid": guidedCreationRecordID(principal, idempotencyKey)})
	if err != nil {
		return nil, fmt.Errorf("get guided investigation creation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

const createGuidedInvestigationSQL = `
BEGIN;
LET $existing = SELECT creation_key, principal, idempotency_key, request_digest,
	investigation_id, revision_id, run_id FROM $creation_rid LIMIT 1;
LET $saved = IF array::len($existing) = 0 THEN
	(CREATE $creation_rid SET creation_key = $creation_key, principal = $principal,
		idempotency_key = $idempotency_key, request_digest = $request_digest,
		investigation_id = $investigation_id, revision_id = $revision_id,
		run_id = $run_id, created_at = $now RETURN AFTER)
	ELSE $existing END;
IF array::len($existing) = 0 {
	CREATE $investigation_rid SET investigation_id = $investigation_id,
		referent = $referent, claim_family = $claim_family, title = $title,
		owner = $principal, lifecycle = 'active', current_revision_id = $revision_id,
		authz_revision = 0, authz_write_revision = 0,
		created_at = $now, updated_at = $now RETURN NONE;
	CREATE $revision_rid SET revision_id = $revision_id,
		investigation_id = $investigation_id, seq = 1,
		normalized_question = $normalized_question, decision_sought = $decision_sought,
		declared_universe = $declared_universe, snapshot_policy = $snapshot_policy,
		build_configuration = $build_configuration, pack_selection = $pack_selection,
		enumeration_method = $enumeration_method, creator = $principal,
		created_at = $now, content_digest = $revision_digest RETURN NONE;
	CREATE $run_rid SET run_id = $run_id, revision_id = $revision_id, seq = 1,
		idempotency_key = $idempotency_key,
		resolved_input_identities = $resolved_input_identities,
		requested_snapshot = $requested_snapshot,
		input_manifest_digest = $input_manifest_digest,
		requested_pack_versions = $requested_pack_versions,
		created_by = $principal, created_at = $now,
		request_digest = $run_request_digest, event_revision = 1 RETURN NONE;
	CREATE $event_rid SET event_id = $event_id, run_id = $run_id, sequence = 1,
		attempt = 1, prior_state = '', new_state = 'queued', actor = $principal,
		reason = 'guided creation submitted', timestamp = $now,
		content_digest = $event_digest RETURN NONE;
	CREATE type::table($job_table) CONTENT {
		target: $run_id, status: 'pending', attempts: 0, created_at: $now,
		pending_key: $run_id, force: false
	} RETURN NONE;
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 202, created_at = $now RETURN NONE;
};
RETURN $saved;
COMMIT;`

// CreateGuidedInvestigation atomically freezes the Investigation, Revision,
// initial queued RunEvent, and one pending queue slot. Concurrent exact
// submissions return the same object graph; reusing the key for different
// frozen inputs fails with ErrConflict.
func (s *Surreal) CreateGuidedInvestigation(ctx context.Context, request GuidedInvestigationRequest) (*GuidedInvestigationCreation, error) {
	request, requestDigest, err := normalizeGuidedInvestigationRequest(request)
	if err != nil {
		return nil, fmt.Errorf("create guided investigation: %w", err)
	}
	if existing, getErr := s.getGuidedCreationRecord(ctx, request.Principal, request.IdempotencyKey); getErr == nil {
		if existing.RequestDigest != requestDigest {
			return nil, fmt.Errorf("create guided investigation: idempotency key reused for different frozen inputs: %w", ErrConflict)
		}
		return s.readGuidedCreation(ctx, existing)
	} else if !errors.Is(getErr, ErrNotFound) {
		return nil, getErr
	}

	now := storeTimestamp(time.Now())
	investigationID, err := newULID(now)
	if err != nil {
		return nil, err
	}
	revision := Revision{
		ID: revisionIdentity(investigationID, 1), InvestigationID: investigationID, Seq: 1,
		NormalizedQuestion: request.NormalizedQuestion, DecisionSought: request.DecisionSought,
		DeclaredUniverse: request.DeclaredUniverse, SnapshotPolicy: request.SnapshotPolicy,
		BuildConfiguration: request.BuildConfiguration, PackSelection: request.PackSelection,
		EnumerationMethod: request.EnumerationMethod, Creator: request.Principal,
	}
	if err := validateRevision(&revision); err != nil {
		return nil, fmt.Errorf("create guided investigation: revision: %w", err)
	}
	runRequest := RunRequest{
		RevisionID: revision.ID, IdempotencyKey: request.IdempotencyKey,
		ResolvedInputIdentities: request.ResolvedInputIdentities,
		RequestedSnapshot:       request.RequestedSnapshot,
		InputManifestDigest:     request.InputManifestDigest,
		RequestedPackVersions:   request.RequestedPackVersions,
		CreatedBy:               request.Principal,
	}
	runRequest, runRequestDigest, err := normalizeRunRequest(runRequest)
	if err != nil {
		return nil, fmt.Errorf("create guided investigation: run request: %w", err)
	}
	runID := runIdentity(revision.ID, runRequest.IdempotencyKey)
	eventID, err := newULID(now)
	if err != nil {
		return nil, err
	}
	event := RunEvent{
		ID: eventID, RunID: runID, Sequence: 1, Attempt: 1,
		NewState: RunQueued, Actor: request.Principal,
		Reason: "guided creation submitted", Timestamp: now,
	}
	event.Digest, _ = contentDigest(runEventCore(event))
	creationKey := hashIdentity("icr_", request.Principal, request.IdempotencyKey)
	vars := map[string]any{
		"creation_rid": guidedCreationRecordID(request.Principal, request.IdempotencyKey),
		"creation_key": creationKey, "principal": request.Principal,
		"idempotency_key": request.IdempotencyKey, "request_digest": requestDigest,
		"investigation_id": investigationID, "revision_id": revision.ID, "run_id": runID,
		"investigation_rid": investigationRecordID(investigationID),
		"referent":          request.Referent, "claim_family": request.ClaimFamily, "title": request.Title,
		"revision_rid":        revisionRecordID(revision.ID),
		"normalized_question": request.NormalizedQuestion, "decision_sought": request.DecisionSought,
		"declared_universe": request.DeclaredUniverse, "snapshot_policy": request.SnapshotPolicy,
		"build_configuration": request.BuildConfiguration, "pack_selection": request.PackSelection,
		"enumeration_method": request.EnumerationMethod, "revision_digest": revision.ContentDigest,
		"run_rid": runRecordID(runID), "resolved_input_identities": runRequest.ResolvedInputIdentities,
		"requested_snapshot":      runRequest.RequestedSnapshot,
		"input_manifest_digest":   runRequest.InputManifestDigest,
		"requested_pack_versions": runRequest.RequestedPackVersions,
		"run_request_digest":      runRequestDigest, "event_rid": runEventRecordID(eventID),
		"event_id": eventID, "event_digest": event.Digest, "now": now,
		"job_table": string(JobInvestigate),
	}
	if err := addInvestigationAuditVars(vars, "investigation.create", investigationID, request.Principal, now); err != nil {
		return nil, fmt.Errorf("create guided investigation: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]guidedCreationRecord](ctx, s.db, createGuidedInvestigationSQL, vars)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				if existing, getErr := s.getGuidedCreationRecord(ctx, request.Principal, request.IdempotencyKey); getErr == nil {
					if existing.RequestDigest != requestDigest {
						return nil, fmt.Errorf("create guided investigation: idempotency key reused for different frozen inputs: %w", ErrConflict)
					}
					return s.readGuidedCreation(ctx, existing)
				}
				continue
			}
			return nil, fmt.Errorf("create guided investigation: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, errors.New("create guided investigation returned no row")
		}
		if rows[0].RequestDigest != requestDigest {
			return nil, fmt.Errorf("create guided investigation: idempotency key reused for different frozen inputs: %w", ErrConflict)
		}
		return s.readGuidedCreation(ctx, &rows[0])
	}
}

func (s *Surreal) readGuidedCreation(ctx context.Context, record *guidedCreationRecord) (*GuidedInvestigationCreation, error) {
	if record == nil {
		return nil, ErrNotFound
	}
	investigation, err := s.GetInvestigation(ctx, record.InvestigationID)
	if err != nil {
		return nil, fmt.Errorf("read guided investigation: investigation: %w", err)
	}
	revision, err := s.GetRevision(ctx, record.RevisionID)
	if err != nil {
		return nil, fmt.Errorf("read guided investigation: revision: %w", err)
	}
	run, err := s.GetRun(ctx, record.RunID)
	if err != nil {
		return nil, fmt.Errorf("read guided investigation: run: %w", err)
	}
	if investigation.Owner != record.Principal || investigation.CurrentRevisionID != revision.ID ||
		revision.InvestigationID != investigation.ID || run.RevisionID != revision.ID {
		return nil, errors.New("read guided investigation: stored object graph is inconsistent")
	}
	return &GuidedInvestigationCreation{
		Investigation: *investigation, Revision: *revision, Run: *run,
	}, nil
}

type investigationRunLeaseRecord struct {
	RunID      string    `json:"run_id"`
	Attempt    int       `json:"lease_attempt"`
	Worker     string    `json:"lease_worker"`
	Token      string    `json:"lease_token"`
	AcquiredAt time.Time `json:"lease_acquired_at"`
}

const acquireInvestigationRunLeaseSQL = `
LET $last = (SELECT sequence, attempt, new_state FROM investigation_run_event
	WHERE run_id = $run_id ORDER BY sequence DESC LIMIT 1)[0];
RETURN IF $last != NONE AND $last.sequence = $expected_sequence
	AND $last.new_state = 'queued' THEN
	(UPDATE $run_rid SET lease_token = $lease_token, lease_attempt = $last.attempt,
		lease_worker = $worker, lease_acquired_at = $now
		WHERE run_id = $run_id AND event_revision = $expected_sequence
		  AND (lease_token = NONE OR lease_token = '') RETURN AFTER)
ELSE [] END;`

func (s *Surreal) AcquireInvestigationRunLease(ctx context.Context, runID, worker string) (*InvestigationRunLease, error) {
	if err := validateDomainString("worker", worker, true); err != nil {
		return nil, fmt.Errorf("acquire investigation run lease: %w", err)
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.State != RunQueued {
		return nil, fmt.Errorf("acquire investigation run lease: run is %s: %w", run.State, ErrConflict)
	}
	events, err := s.ListRunEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	token, err := newLeaseToken()
	if err != nil {
		return nil, err
	}
	now := storeTimestamp(time.Now())
	results, err := surrealdb.Query[[]investigationRunLeaseRecord](ctx, s.db,
		acquireInvestigationRunLeaseSQL, map[string]any{
			"run_rid": runRecordID(runID), "run_id": runID,
			"expected_sequence": events[len(events)-1].Sequence,
			"lease_token":       token, "worker": worker, "now": now,
		})
	if err != nil {
		return nil, fmt.Errorf("acquire investigation run lease: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Token != token {
		return nil, fmt.Errorf("acquire investigation run lease: %w", ErrLeaseLost)
	}
	return &InvestigationRunLease{
		RunID: runID, Attempt: rows[0].Attempt, Worker: worker,
		AcquiredAt: rows[0].AcquiredAt, Token: token,
	}, nil
}

func validInvestigationRunLease(lease InvestigationRunLease) bool {
	return strings.HasPrefix(lease.RunID, "iru_") && lease.Attempt > 0 &&
		lease.Token != "" && validateDomainString("worker", lease.Worker, true) == nil
}

func (s *Surreal) leasedRunState(ctx context.Context, lease InvestigationRunLease) (*investigationRunRec, RunEvent, error) {
	if !validInvestigationRunLease(lease) {
		return nil, RunEvent{}, fmt.Errorf("investigation run lease is invalid: %w", ErrLeaseLost)
	}
	run, err := s.getRunRec(ctx, lease.RunID)
	if err != nil {
		return nil, RunEvent{}, err
	}
	events, err := s.ListRunEvents(ctx, lease.RunID)
	if err != nil {
		return nil, RunEvent{}, err
	}
	if len(events) == 0 || run.EventRevision != events[len(events)-1].Sequence ||
		events[len(events)-1].Attempt != lease.Attempt ||
		run.LeaseToken != lease.Token || run.LeaseAttempt != lease.Attempt ||
		run.LeaseWorker != lease.Worker {
		return nil, RunEvent{}, fmt.Errorf("investigation run lease no longer owns the current attempt: %w", ErrLeaseLost)
	}
	return run, events[len(events)-1], nil
}

const advanceInvestigationRunSQL = `
BEGIN;
LET $locked = UPDATE $run_rid SET event_revision = $next_sequence
	WHERE run_id = $run_id AND event_revision = $expected_sequence
	  AND lease_token = $lease_token AND lease_attempt = $attempt
	  AND lease_worker = $worker RETURN AFTER;
LET $saved = IF array::len($locked) = 1 THEN
	(CREATE $event_rid SET event_id = $event_id, run_id = $run_id,
		sequence = $next_sequence, attempt = $attempt, prior_state = $prior_state,
		new_state = $new_state, actor = $worker, reason = $reason,
		timestamp = $now, content_digest = $event_digest RETURN AFTER)
	ELSE [] END;
RETURN $saved;
COMMIT;`

// AdvanceInvestigationRun records a non-terminal state transition under the
// current attempt lease. Terminal transitions use the atomic publish/fail or
// owner-authorized cancel paths below.
func (s *Surreal) AdvanceInvestigationRun(ctx context.Context, lease InvestigationRunLease, next RunState, reason string) (*RunEvent, error) {
	if next != RunEnumerating && next != RunAnalyzing && next != RunPublishing {
		return nil, errors.New("advance investigation run: terminal or queued transitions use their dedicated workflow")
	}
	if err := validateDomainString("reason", reason, true); err != nil {
		return nil, fmt.Errorf("advance investigation run: %w", err)
	}
	_, last, err := s.leasedRunState(ctx, lease)
	if err != nil {
		return nil, fmt.Errorf("advance investigation run: %w", err)
	}
	if !validRunTransition(last.NewState, next) {
		return nil, fmt.Errorf("advance investigation run: invalid transition %s -> %s: %w", last.NewState, next, ErrConflict)
	}
	now := storeTimestamp(time.Now())
	eventID, err := newULID(now)
	if err != nil {
		return nil, err
	}
	event := RunEvent{
		ID: eventID, RunID: lease.RunID, Sequence: last.Sequence + 1,
		Attempt: lease.Attempt, PriorState: last.NewState, NewState: next,
		Actor: lease.Worker, Reason: reason, Timestamp: now,
	}
	event.Digest, _ = contentDigest(runEventCore(event))
	results, queryErr := surrealdb.Query[[]RunEvent](ctx, s.db, advanceInvestigationRunSQL,
		map[string]any{
			"run_rid": runRecordID(lease.RunID), "run_id": lease.RunID,
			"expected_sequence": last.Sequence, "next_sequence": event.Sequence,
			"lease_token": lease.Token, "attempt": lease.Attempt, "worker": lease.Worker,
			"event_rid": runEventRecordID(event.ID), "event_id": event.ID,
			"prior_state": string(event.PriorState), "new_state": string(event.NewState),
			"reason": event.Reason, "now": now, "event_digest": event.Digest,
		})
	if queryErr != nil {
		return nil, fmt.Errorf("advance investigation run: %w", queryErr)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("advance investigation run: %w", ErrLeaseLost)
	}
	return &rows[0], nil
}

const retryInvestigationRunSQL = `
BEGIN;
LET $locked = UPDATE $run_rid SET event_revision = $next_sequence,
	lease_token = NONE, lease_attempt = NONE, lease_worker = NONE,
	lease_acquired_at = NONE
	WHERE run_id = $run_id AND event_revision = $expected_sequence
	  AND lease_token = $lease_token AND lease_attempt = $prior_attempt
	  AND lease_worker = $worker RETURN AFTER;
LET $saved = IF array::len($locked) = 1 THEN
	(CREATE $event_rid SET event_id = $event_id, run_id = $run_id,
		sequence = $next_sequence, attempt = $next_attempt,
		prior_state = $prior_state, new_state = 'queued', actor = $worker,
		reason = $reason, timestamp = $now, content_digest = $event_digest RETURN AFTER)
	ELSE [] END;
RETURN $saved;
COMMIT;`

// RetryInvestigationRun closes the current lease and appends a new queued
// attempt. The caller supplies the pack-specific ceiling, which may be
// stricter but never looser than the platform maximum.
func (s *Surreal) RetryInvestigationRun(ctx context.Context, lease InvestigationRunLease, reason string, maxAttempts int) (*RunEvent, error) {
	if err := validateDomainString("reason", reason, true); err != nil {
		return nil, fmt.Errorf("retry investigation run: %w", err)
	}
	if maxAttempts < 1 || maxAttempts > MaxInvestigationRunAttempts {
		return nil, fmt.Errorf("retry investigation run: max attempts must be within 1..%d", MaxInvestigationRunAttempts)
	}
	_, last, err := s.leasedRunState(ctx, lease)
	if err != nil {
		return nil, fmt.Errorf("retry investigation run: %w", err)
	}
	if terminalRunState(last.NewState) {
		return nil, fmt.Errorf("retry investigation run: terminal run requires a new Run: %w", ErrConflict)
	}
	if lease.Attempt >= maxAttempts {
		return nil, fmt.Errorf("retry investigation run: attempts exhausted: %w", ErrConflict)
	}
	now := storeTimestamp(time.Now())
	eventID, err := newULID(now)
	if err != nil {
		return nil, err
	}
	event := RunEvent{
		ID: eventID, RunID: lease.RunID, Sequence: last.Sequence + 1,
		Attempt: lease.Attempt + 1, PriorState: last.NewState, NewState: RunQueued,
		Actor: lease.Worker, Reason: reason, Timestamp: now,
	}
	event.Digest, _ = contentDigest(runEventCore(event))
	results, queryErr := surrealdb.Query[[]RunEvent](ctx, s.db, retryInvestigationRunSQL,
		map[string]any{
			"run_rid": runRecordID(lease.RunID), "run_id": lease.RunID,
			"expected_sequence": last.Sequence, "next_sequence": event.Sequence,
			"lease_token": lease.Token, "prior_attempt": lease.Attempt,
			"next_attempt": event.Attempt, "worker": lease.Worker,
			"event_rid": runEventRecordID(event.ID), "event_id": event.ID,
			"prior_state": string(event.PriorState), "reason": event.Reason,
			"now": now, "event_digest": event.Digest,
		})
	if queryErr != nil {
		return nil, fmt.Errorf("retry investigation run: %w", queryErr)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("retry investigation run: %w", ErrLeaseLost)
	}
	return &rows[0], nil
}

func (s *Surreal) investigationIDForLeasedRun(ctx context.Context, runID string) (string, error) {
	return s.investigationIDForRun(ctx, runID)
}

const finalizeInvestigationRunSQL = `
BEGIN;
LET $last = (SELECT sequence, attempt, new_state FROM investigation_run_event
	WHERE run_id = $run_id ORDER BY sequence DESC LIMIT 1)[0];
LET $run_candidate = SELECT id FROM $run_rid
	WHERE run_id = $run_id AND event_revision = $expected_sequence
	  AND lease_token = $lease_token AND lease_attempt = $attempt
	  AND lease_worker = $worker LIMIT 1;
LET $investigation_locked = IF array::len($run_candidate) = 1 THEN
	(UPDATE $investigation_rid SET workflow_revision = (workflow_revision ?? 0) + 1
		WHERE investigation_id = $investigation_id AND lifecycle = 'active' RETURN AFTER)
	ELSE [] END;
LET $eligible = IF array::len($investigation_locked) = 1
	AND $last != NONE AND $last.sequence = $expected_sequence
	AND $last.attempt = $attempt AND $last.new_state = $prior_state THEN
	(UPDATE extraction_run SET retention_revision = (retention_revision ?? 0) + 1
		WHERE run_id IN $pin_references
		  AND evidence_format_version = $evidence_format_version
		  AND retention_quarantined = false
		  AND run_id = record::id(id)
		  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
		  AND ((status = 'published' AND published_key != NONE)
		    OR (status = 'superseded' AND published_key = NONE)) RETURN AFTER)
	ELSE [] END;
LET $existing = SELECT artifact_id, scope, run_id, terminal_status, snapshot_manifest,
	input_manifest, coverage_ledger, fact_references, pin_references,
	eligibility_result, diagnostic_data, created_at, content_digest FROM $artifact_rid LIMIT 1;
LET $other = SELECT artifact_id FROM investigation_run_artifact
	WHERE run_id = $run_id AND artifact_id != $artifact_id LIMIT 1;
LET $owner_existing = SELECT owner_key, artifact_id, owner_kind, owner_id,
	authorized_by, reason, content_digest FROM $owner_rid LIMIT 1;
LET $immutable = array::len($existing) = 0 OR
	($existing[0].artifact_id = $artifact_id AND $existing[0].scope = $scope
	 AND $existing[0].run_id = $run_id AND $existing[0].terminal_status = $terminal_status
	 AND $existing[0].snapshot_manifest = $snapshot_manifest
	 AND $existing[0].input_manifest = $input_manifest
	 AND $existing[0].coverage_ledger = $coverage_ledger
	 AND $existing[0].fact_references = $fact_references
	 AND $existing[0].pin_references = $pin_references
	 AND $existing[0].eligibility_result = $eligibility_result
	 AND $existing[0].diagnostic_data = $diagnostic_data
	 AND $existing[0].content_digest = $content_digest);
LET $owner_immutable = array::len($owner_existing) = 0 OR
	($owner_existing[0].owner_key = $owner_key
	 AND $owner_existing[0].artifact_id = $artifact_id
	 AND $owner_existing[0].owner_kind = $owner_kind
	 AND $owner_existing[0].owner_id = $investigation_id
	 AND $owner_existing[0].authorized_by = $authorized_by
	 AND $owner_existing[0].reason = $owner_reason
	 AND $owner_existing[0].content_digest = $owner_digest);
LET $ready = array::len($investigation_locked) = 1
	AND array::len($run_candidate) = 1
	AND array::len($eligible) = array::len($pin_references)
	AND array::len($other) = 0 AND $immutable AND $owner_immutable
	AND $last != NONE AND $last.sequence = $expected_sequence
	AND $last.attempt = $attempt AND $last.new_state = $prior_state;
LET $run_locked = IF $ready THEN
	(UPDATE $run_rid SET event_revision = $next_sequence,
		lease_token = NONE, lease_attempt = NONE, lease_worker = NONE,
		lease_acquired_at = NONE
		WHERE run_id = $run_id AND event_revision = $expected_sequence
		  AND lease_token = $lease_token AND lease_attempt = $attempt
		  AND lease_worker = $worker RETURN AFTER)
	ELSE [] END;
LET $saved = IF array::len($run_locked) = 1 THEN
	(UPSERT $artifact_rid SET artifact_id = $artifact_id, scope = $scope,
		run_id = $run_id, terminal_status = $terminal_status,
		snapshot_manifest = $snapshot_manifest, input_manifest = $input_manifest,
		coverage_ledger = $coverage_ledger, fact_references = $fact_references,
		pin_references = $pin_references, eligibility_result = $eligibility_result,
		diagnostic_data = $diagnostic_data,
		created_at = IF created_at = NONE THEN $now ELSE created_at END,
		content_digest = $content_digest RETURN AFTER)
	ELSE [] END;
IF array::len($saved) = 1 {
	CREATE $event_rid SET event_id = $event_id, run_id = $run_id,
		sequence = $next_sequence, attempt = $attempt, prior_state = $prior_state,
		new_state = $terminal_status, actor = $worker, reason = $event_reason,
		timestamp = $now, content_digest = $event_digest RETURN NONE;
	UPSERT $owner_rid SET owner_key = $owner_key, artifact_id = $artifact_id,
		owner_kind = $owner_kind, owner_id = $investigation_id,
		authorized_by = $authorized_by, reason = $owner_reason,
		created_at = IF created_at = NONE THEN $now ELSE created_at END,
		content_digest = $owner_digest RETURN NONE;
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE;
};
FOR $pin IN $pins {
	IF array::len($saved) = 1 {
		UPSERT $pin.rid SET pin_key = $pin.pin_key, run_id = $pin.run_id,
			kind = $pin_kind,
			created_at = IF created_at = NONE THEN time::now() ELSE created_at END
			RETURN NONE
	}
};
RETURN $saved;
COMMIT;`

func (s *Surreal) finalizeInvestigationRun(
	ctx context.Context,
	lease InvestigationRunLease,
	artifact RunArtifact,
	reason string,
) (*RunArtifact, error) {
	if artifact.RunID == "" {
		artifact.RunID = lease.RunID
	}
	if artifact.RunID != lease.RunID {
		return nil, errors.New("finalize investigation run: artifact belongs to another run")
	}
	_, canonicalCoverage, err := ParseInvestigationCoverageLedger(artifact.CoverageLedger)
	if err != nil {
		return nil, fmt.Errorf("finalize investigation run: coverage ledger: %w", err)
	}
	artifact.CoverageLedger = canonicalCoverage
	artifact, err = normalizeRunArtifact(artifact)
	if err != nil {
		return nil, fmt.Errorf("finalize investigation run: %w", err)
	}
	if existing, getErr := s.GetRunArtifact(ctx, artifact.ID); getErr == nil {
		if existing.RunID != lease.RunID || existing.ContentDigest != artifact.ContentDigest {
			return nil, fmt.Errorf("finalize investigation run: immutable artifact conflict: %w", ErrConflict)
		}
		return existing, nil
	} else if !errors.Is(getErr, ErrNotFound) {
		return nil, getErr
	}
	_, last, err := s.leasedRunState(ctx, lease)
	if err != nil {
		return nil, fmt.Errorf("finalize investigation run: %w", err)
	}
	switch artifact.TerminalStatus {
	case RunPublished:
		if last.NewState != RunPublishing {
			return nil, fmt.Errorf("finalize investigation run: publish requires publishing state: %w", ErrConflict)
		}
	case RunFailed:
		if terminalRunState(last.NewState) {
			return nil, fmt.Errorf("finalize investigation run: run is terminal: %w", ErrConflict)
		}
		if len(artifact.FactReferences) != 0 || len(artifact.PinReferences) != 0 {
			return nil, errors.New("finalize investigation run: failed artifact cannot publish facts or pins")
		}
	default:
		return nil, errors.New("finalize investigation run: only published or failed artifacts use a worker lease")
	}
	if err := validateDomainString("finalization reason", reason, true); err != nil {
		return nil, fmt.Errorf("finalize investigation run: %w", err)
	}
	investigationID, err := s.investigationIDForLeasedRun(ctx, lease.RunID)
	if err != nil {
		return nil, fmt.Errorf("finalize investigation run: investigation: %w", err)
	}
	artifact.CreatedAt = storeTimestamp(time.Now())
	eventID, err := newULID(artifact.CreatedAt)
	if err != nil {
		return nil, err
	}
	event := RunEvent{
		ID: eventID, RunID: lease.RunID, Sequence: last.Sequence + 1,
		Attempt: lease.Attempt, PriorState: last.NewState,
		NewState: artifact.TerminalStatus, Actor: lease.Worker,
		Reason: reason, Timestamp: artifact.CreatedAt,
	}
	event.Digest, _ = contentDigest(runEventCore(event))
	owner, err := normalizeRunArtifactRetentionOwner(RunArtifactRetentionOwner{
		ArtifactID: artifact.ID, Kind: RunArtifactOwnerInvestigation,
		OwnerID: investigationID, AuthorizedBy: lease.Worker,
		Reason:    "active investigation run artifact",
		CreatedAt: artifact.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize investigation run: retention owner: %w", err)
	}
	kind := runArtifactPinKind(artifact.ID)
	pins := make([]map[string]any, len(artifact.PinReferences))
	for i, runID := range artifact.PinReferences {
		pins[i] = map[string]any{
			"rid":     evidencePinRecordID(runID, kind),
			"pin_key": hashIdentity("pin_", runID, kind), "run_id": runID,
		}
	}
	vars := map[string]any{
		"run_rid": runRecordID(lease.RunID), "run_id": lease.RunID,
		"expected_sequence": last.Sequence, "next_sequence": event.Sequence,
		"lease_token": lease.Token, "attempt": lease.Attempt, "worker": lease.Worker,
		"prior_state": string(last.NewState), "terminal_status": string(artifact.TerminalStatus),
		"investigation_rid": investigationRecordID(investigationID),
		"investigation_id":  investigationID,
		"artifact_rid":      runArtifactRecordID(artifact.ID), "artifact_id": artifact.ID,
		"scope": artifact.Scope, "snapshot_manifest": artifact.SnapshotManifest,
		"input_manifest": artifact.InputManifest, "coverage_ledger": artifact.CoverageLedger,
		"fact_references": artifact.FactReferences, "pin_references": artifact.PinReferences,
		"eligibility_result": artifact.EligibilityResult,
		"diagnostic_data":    artifact.DiagnosticData, "content_digest": artifact.ContentDigest,
		"event_rid": runEventRecordID(event.ID), "event_id": event.ID,
		"event_reason": reason, "event_digest": event.Digest, "now": artifact.CreatedAt,
		"owner_rid": retentionOwnerRecordID(owner.Key), "owner_key": owner.Key,
		"owner_kind": string(owner.Kind), "authorized_by": owner.AuthorizedBy,
		"owner_reason": owner.Reason, "owner_digest": owner.ContentDigest,
		"pins": pins, "pin_kind": kind,
		"evidence_format_version": evidenceFormatVersion,
	}
	auditAction := "investigation.run." + string(artifact.TerminalStatus)
	if err := addInvestigationAuditVars(vars, auditAction, lease.RunID, lease.Worker, artifact.CreatedAt); err != nil {
		return nil, fmt.Errorf("finalize investigation run: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]RunArtifact](ctx, s.db, finalizeInvestigationRunSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("finalize investigation run: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) == 1 {
			return &rows[0], nil
		}
		if _, _, leaseErr := s.leasedRunState(ctx, lease); leaseErr == nil {
			return nil, fmt.Errorf("finalize investigation run: artifact inputs are unavailable or conflict: %w", ErrConflict)
		}
		return nil, fmt.Errorf("finalize investigation run: %w", ErrLeaseLost)
	}
}

// PublishInvestigationRun atomically closes the lease, appends the published
// event, creates the complete RunArtifact, pins its evidence, and records the
// active Investigation retention owner.
func (s *Surreal) PublishInvestigationRun(ctx context.Context, lease InvestigationRunLease, artifact RunArtifact) (*RunArtifact, error) {
	artifact.TerminalStatus = RunPublished
	return s.finalizeInvestigationRun(ctx, lease, artifact, "atomic publication complete")
}

// FailInvestigationRun atomically closes the lease and retains a diagnostic
// artifact. Partial/failed coverage is visible, but no fact or pin set can be
// smuggled through the failure path.
func (s *Surreal) FailInvestigationRun(ctx context.Context, lease InvestigationRunLease, artifact RunArtifact, reason string) (*RunArtifact, error) {
	if len(artifact.FactReferences) != 0 || len(artifact.PinReferences) != 0 {
		return nil, errors.New("fail investigation run: diagnostic artifacts cannot contain fact or pin references")
	}
	artifact.TerminalStatus = RunFailed
	return s.finalizeInvestigationRun(ctx, lease, artifact, reason)
}

const cancelInvestigationRunSQL = `
BEGIN;
LET $authorized = UPDATE $investigation_rid
	SET workflow_revision = (workflow_revision ?? 0) + 1
	WHERE investigation_id = $investigation_id AND owner = $actor RETURN AFTER;
LET $locked = IF array::len($authorized) = 1 THEN
	(UPDATE $run_rid SET event_revision = $next_sequence,
		lease_token = NONE, lease_attempt = NONE, lease_worker = NONE,
		lease_acquired_at = NONE
		WHERE run_id = $run_id AND event_revision = $expected_sequence RETURN AFTER)
	ELSE [] END;
LET $saved = IF array::len($locked) = 1 THEN
	(CREATE $event_rid SET event_id = $event_id, run_id = $run_id,
		sequence = $next_sequence, attempt = $attempt, prior_state = $prior_state,
		new_state = 'canceled', actor = $actor, reason = $reason,
		timestamp = $now, content_digest = $event_digest RETURN AFTER)
	ELSE [] END;
IF array::len($saved) = 1 {
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE
};
RETURN $saved;
COMMIT;`

// CancelInvestigationRunAs is owner-only. The event, lease closure, and audit
// row commit together, so a worker holding the prior token is fenced before a
// cancellation can become observable.
func (s *Surreal) CancelInvestigationRunAs(ctx context.Context, principal, runID, reason string) (*RunEvent, error) {
	if err := validateDomainString("cancellation reason", reason, true); err != nil {
		return nil, fmt.Errorf("cancel investigation run: %w", err)
	}
	investigationID, err := s.investigationIDForRun(ctx, runID)
	if err != nil {
		return nil, ErrNotFound
	}
	projection, err := s.readInvestigationProjection(ctx, principal, investigationID)
	if err != nil || projection.Owner != principal {
		return nil, ErrNotFound
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if terminalRunState(run.State) {
		return nil, fmt.Errorf("cancel investigation run: run is terminal: %w", ErrConflict)
	}
	events, err := s.ListRunEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	last := events[len(events)-1]
	now := storeTimestamp(time.Now())
	eventID, err := newULID(now)
	if err != nil {
		return nil, err
	}
	event := RunEvent{
		ID: eventID, RunID: runID, Sequence: last.Sequence + 1,
		Attempt: last.Attempt, PriorState: last.NewState, NewState: RunCanceled,
		Actor: principal, Reason: reason, Timestamp: now,
	}
	event.Digest, _ = contentDigest(runEventCore(event))
	vars := map[string]any{
		"investigation_rid": investigationRecordID(investigationID),
		"investigation_id":  investigationID, "run_rid": runRecordID(runID),
		"run_id": runID, "actor": principal, "reason": reason,
		"expected_sequence": last.Sequence, "next_sequence": event.Sequence,
		"attempt": event.Attempt, "prior_state": string(last.NewState),
		"event_rid": runEventRecordID(event.ID), "event_id": event.ID,
		"event_digest": event.Digest, "now": now,
	}
	if err := addInvestigationAuditVars(vars, "investigation.run.canceled", runID, principal, now); err != nil {
		return nil, fmt.Errorf("cancel investigation run: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]RunEvent](ctx, s.db, cancelInvestigationRunSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("cancel investigation run: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("cancel investigation run: concurrent terminal transition: %w", ErrConflict)
		}
		return &rows[0], nil
	}
}

func (s *Surreal) getRunArtifactForRun(ctx context.Context, runID string) (*RunArtifact, error) {
	results, err := surrealdb.Query[[]struct {
		ID string `json:"artifact_id"`
	}](ctx, s.db,
		"SELECT artifact_id FROM investigation_run_artifact WHERE run_id = $run_id LIMIT 2",
		map[string]any{"run_id": runID})
	if err != nil {
		return nil, fmt.Errorf("get run artifact for run: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) != 1 {
		return nil, errors.New("get run artifact for run: multiple terminal artifacts")
	}
	return s.GetRunArtifact(ctx, rows[0].ID)
}

// GetRunArtifactForRunAs principal-projects the new status read exactly like
// every T16.3 read: denial masks absence and any stored integrity failure.
func (s *Surreal) GetRunArtifactForRunAs(ctx context.Context, principal, runID string) (*RunArtifact, error) {
	investigationID, resolutionErr := s.investigationIDForRun(ctx, runID)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	artifact, readErr := s.getRunArtifactForRun(ctx, runID)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return artifact, readErr
}

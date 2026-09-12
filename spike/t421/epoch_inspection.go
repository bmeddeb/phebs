package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/store"
)

var errEpochInspection = errors.New("epoch-one exact inspection refused")

const (
	epochReadTrailer = "X-Phebs-T421-Exact-Read-Report"
	// Same bounded native F capture as cmd/phebs/t422_retention_control.go;
	// the separately deduplicated signed receipt has a different byte cap.
	epochFinalResponseBytes = 1 << 20
)

type epochInspectionReport struct {
	Schema              string  `json:"schema"`
	RequestOrdinal      uint64  `json:"request_ordinal"`
	Status              string  `json:"status"`
	ControlFileReads    uint64  `json:"control_file_reads"`
	StoreReadAttempts   uint64  `json:"store_read_attempts"`
	MemberVisits        uint64  `json:"member_visits"`
	StoreWriteAttempts  uint64  `json:"store_write_attempts"`
	VisibleRepositories *uint64 `json:"visible_repositories,omitempty"`
}

type epochProgressInspection struct {
	HTTPStatus int
	Progress   *extractionpublication.Progress
}

type epochTailReadiness struct {
	Schema                       string `json:"schema"`
	Status                       string `json:"status"`
	SelectedRuntimeSHA256        string `json:"selected_runtime_sha256,omitempty"`
	RelationshipGenerationSHA256 string `json:"relationship_generation_sha256,omitempty"`
	RelationshipRootSHA256       string `json:"relationship_root_sha256,omitempty"`
	CallerGenerationSHA256       string `json:"caller_generation_sha256,omitempty"`
	CallerRootSHA256             string `json:"caller_root_sha256,omitempty"`
}

// One reader owns this server epoch's shared exact-read ordinal. The caller
// serializes phase choreography and joins its context before native shutdown;
// no method retries a transport failure or emits receipt/phase-success claims.
type executionEpochInspection struct {
	mu                                 sync.Mutex
	run                                *ExecutionEpochOneRun
	plan                               Plan
	authored                           AuthoredExecutionRevision
	projection                         PhaseStateProjection
	bounds                             phaseInspectionInventory
	next, progressCalls, tailCalls     uint64
	reports                            uint64
	totals                             readaccounting.Counts
	progressReady                      bool
	tail                               epochTailReadiness
	finalUsed                          bool
	retentionUsed                      bool
	selectorCleanupPhase               string
	selectorCleanup                    epochSelectorCleanupObservation
	cold                               AuthorityPhaseResult
	warmAuthority, physicalAuthority   AuthorityPhaseResult
	logicalAuthority                   AuthorityPhaseResult
	activationHit, activationRecovered epochActivationObservation
	markerHit, markerRecovered         epochMarkerObservation
	returnAuthority, staleAuthority    AuthorityPhaseResult
	stalePreparation                   epochStalePreparation
	stalePrepared                      bool
	staleHit, staleRecovered           extractionpublication.StaleLeaseTransition
	checkpointPreparation              epochStalePreparation
	checkpointPrepared                 bool
	checkpointHit, checkpointRecovered extractionpublication.CheckpointRestartTransition
	earlyFinishSamples                 ExecutionEarlyFinishSamples
	midphaseSamples                    ExecutionMidphaseSamples
	pressure                           epochPressureObservations
	lifecycleCalls                     uint64
	pressureBaseline                   *[sha256.Size]byte
	archiveInput                       epochArchiveInput
	archivePrior, archiveAuthority     AuthorityPhaseResult
	archiveManifest                    *recovery.ArchiveTransitionManifest
	archiveUsed                        bool
	restoredStep                       uint8
	restoredSamples                    ExecutionRestoredSamples
	collectionCycle                    lifecycle.CycleObservation
	collectionAuthority                AuthorityPhaseResult
	productAuthority                   AuthorityPhaseResult
	productBaseline                    *[sha256.Size]byte
	productQueryAuthority              epochQueryAuthority
	productFinalCalls                  uint8
	productFirstFinalOrdinal           uint64
	productQueriesComplete             bool // Set only by the typed, complete query corridor.
	productQueries                     []ExecutionProductQuery
	productFinalDigests                [2][sha256.Size]byte
	productQueryEvidence               *QueryEvidence
	maximumReports                     uint64 // Epoch-five inventory, shared across its phases.
	evidence                           epochInspectionLedger
	err                                error
	// Private failed-response diagnostic only, never receipt evidence. Retain
	// the already bounded body (at most the response cap plus one sentinel).
	failureStatus  int
	failureBody    []byte
	failureOrdinal uint64
	readFailure    epochReadFailure
}

// Fixed one-shot private record; URLs, headers and response bodies are not
// copied into it. The already bounded failed-response retention stays separate.
type epochReadFailure struct {
	Stage   string
	Ordinal uint64
	Cause   error
}

func (run *ExecutionEpochOneRun) newEpochInspection(ctx context.Context) (*executionEpochInspection, error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.flow.epochs == nil || run.flow.epochs.author == nil {
		return nil, errEpochInspection
	}
	run.mu.Lock()
	if run.inspection != nil || run.stopping || run.err != nil || !run.healthy || run.healthDone == nil || run.control == nil || run.epoch.Epoch != 1 {
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
	// Reserve the sole ordinal issuer before metadata I/O, without holding the
	// run lock needed by Stop. A refused constructor remains unavailable.
	reader := &executionEpochInspection{run: run, err: errEpochInspection}
	run.inspection = reader
	run.mu.Unlock()
	author := run.flow.epochs.author
	author.mu.Lock()
	defer author.mu.Unlock()
	if author.borrowedBy != run || author.active || author.next != 1 || author.previous == nil || author.previous.Result != author.expected[0] || author.check(ctx) != nil {
		return nil, errEpochInspection
	}
	plan := run.flow.plan
	if plan.Schema != PlanV3Schema {
		return nil, errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(plan, "cold")
	rows, _, inventoryErr := correctedInspectionInventory(plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 2 || rows[1].Phase != "cold" || rows[1].ServerEpoch != 1 ||
		projection.PhysicalRevision != "a" || projection.LogicalRevision != "a" || projection.CatalogSource.SHA256 != run.epoch.CatalogSHA256 {
		return nil, errEpochInspection
	}
	reader.plan, reader.authored, reader.projection, reader.bounds, reader.next, reader.err = plan, author.previous.Result, projection, rows[1], 1, nil
	return reader, nil
}

// Keep the epoch ordinal and positive accounting prefix; only phase-local
// readiness/call counters reset. The parent calls this after joined handoff.
func (reader *executionEpochInspection) beginWarm() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || !reader.finalUsed || reader.projection.Phase != "cold" || reader.cold.Phase != "cold" {
		return errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(reader.plan, "warm_noop")
	rows, _, inventoryErr := correctedInspectionInventory(reader.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 3 || rows[2].Phase != "warm_noop" || rows[2].ServerEpoch != 1 {
		return errEpochInspection
	}
	reader.projection, reader.bounds = projection, rows[2]
	reader.progressCalls, reader.tailCalls, reader.progressReady = 0, 0, false
	reader.tail, reader.finalUsed = epochTailReadiness{}, false
	return nil
}

// canonical decoding also rejects duplicate fields, omitted mandatory zeros,
// alternate number encodings and trailing documents, without a second parser.
func decodeEpochJSON(raw []byte, value any, indent bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return errEpochInspection
	}
	var canonical []byte
	var err error
	if indent {
		canonical, err = json.MarshalIndent(value, "", "  ")
	} else {
		canonical, err = json.Marshal(value)
	}
	if err != nil || !bytes.Equal(raw, append(canonical, '\n')) {
		return errEpochInspection
	}
	return nil
}

func decodeEpochReport(values []string, ordinal uint64, maximum epochInspectionReport) (epochInspectionReport, error) {
	var report epochInspectionReport
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 1024 {
		return report, errEpochInspection
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != values[0] || decodeEpochJSON(append(raw, '\n'), &report, false) != nil ||
		report.Schema != "t421-source-free-read-accounting-v1" || report.RequestOrdinal != ordinal || report.Status != "complete" ||
		report.ControlFileReads > maximum.ControlFileReads || report.StoreReadAttempts > maximum.StoreReadAttempts || report.MemberVisits > maximum.MemberVisits || report.StoreWriteAttempts != 0 {
		return report, errEpochInspection
	}
	return report, nil
}

// Each request uses one fresh non-proxy transport with redirects, compression
// and connection reuse disabled. A consumed ordinal is never retried; actual
// EOF must expose exactly one canonical accounting trailer before admission.
func (reader *executionEpochInspection) read(ctx context.Context, path string, limit int64, maximum epochInspectionReport) (_ []byte, _ int, _ epochInspectionReport, retErr error) {
	return reader.readWithFence(ctx, path, limit, maximum, time.Time{})
}

func (reader *executionEpochInspection) readWithFence(ctx context.Context, path string, limit int64, maximum epochInspectionReport, fence time.Time) (_ []byte, _ int, _ epochInspectionReport, retErr error) {
	raw, status, _, report, err := reader.readRequest(ctx, path, nil, limit, maximum, fence, false)
	return raw, status, report, err
}

// Caller holds reader.mu through the typed query decoder and owns the closed
// tools/call encoding. A nil payload means GET; POST uses only /api/mcp.
// Content-Type belongs to this response, never to shared/stale header state.
func (reader *executionEpochInspection) readQueryRequest(ctx context.Context, path string, payload []byte, limit int64, maximum epochInspectionReport, repositories bool) ([]byte, int, string, epochInspectionReport, error) {
	if reader.run == nil || reader.run.epoch.Epoch != 5 || reader.projection.Phase != "product_queries" ||
		reader.productFinalCalls != 1 || reader.productQueriesComplete {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	return reader.readRequest(ctx, path, payload, limit, maximum, time.Time{}, repositories)
}

func (reader *executionEpochInspection) readRequest(ctx context.Context, path string, payload []byte, limit int64, maximum epochInspectionReport, fence time.Time, repositories bool) (_ []byte, _ int, _ string, _ epochInspectionReport, retErr error) {
	stage, ordinal := "preflight", reader.next
	var cause error
	defer func() {
		if retErr != nil && reader.readFailure.Stage == "" {
			if cause == nil && ctx != nil {
				cause = context.Cause(ctx)
			}
			if cause == nil {
				cause = retErr
			}
			if requestError, ok := cause.(*url.Error); ok {
				cause = requestError.Err // Do not retain the request URL.
			}
			reader.readFailure = epochReadFailure{Stage: stage, Ordinal: ordinal, Cause: cause}
		}
	}()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || reader.run.control == nil || reader.next == 0 || reader.next > 11531 {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	if payload != nil && (path != "/api/mcp" || len(payload) == 0 || len(payload) > 64<<10 || !fence.IsZero() ||
		reader.run.epoch.Epoch != 5 || reader.projection.Phase != "product_queries" || reader.productFinalCalls != 1 || reader.productQueriesComplete) {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	if !fence.IsZero() && (fence.UnixNano() <= 0 || path != "/api/t422/lifecycle/pressure-80" && path != "/api/t422/lifecycle/pressure-90" && path != "/api/t422/lifecycle/pressure-75") {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	if (reader.run.epoch.Epoch == 2 || reader.run.epoch.Epoch == 3 && !reader.run.staleAllowed) && reader.next > 5765 {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	if reader.run.epoch.Epoch == 3 && reader.run.staleAllowed && !reader.run.checkpointAllowed && reader.next > 11530 {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	if reader.run.epoch.Epoch == 5 && (reader.maximumReports == 0 || reader.next > reader.maximumReports) {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	run := reader.run
	run.mu.Lock()
	unavailable := run.stopping || run.err != nil
	run.mu.Unlock()
	select {
	case <-run.stop:
		unavailable = true
	default:
	}
	if unavailable {
		stage = "run_unavailable"
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	token := run.control.RequestToken()
	if token == "" {
		stage = "request_token"
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	reader.beginInspectionEvidence()
	defer reader.finishInspectionEvidence()
	reader.next++
	method := http.MethodGet
	var body io.Reader
	if payload != nil {
		method, body = http.MethodPost, bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://"+run.epoch.Listen+path, body)
	if err != nil {
		stage, cause = "request_construction", err
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
	}
	request.Header.Set("Authorization", "Bearer "+run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	request.Header.Set("X-Phebs-T421-Exact-Reads", "source-free-v1")
	request.Header.Set("X-Phebs-T421-Exact-Read-Ordinal", strconv.FormatUint(ordinal, 10))
	if repositories || path == "/api/t421/final-authority" && reader.plan.Schema == PlanV3Schema && run.epoch.Epoch == 5 && reader.projection.Phase == "product_queries" {
		request.Header.Set("X-Phebs-T422-Query-Evidence", "bound-v1")
	}
	if !fence.IsZero() {
		request.Header.Set("X-Phebs-T422-Ballast-Unix-Nano", strconv.FormatInt(fence.UnixNano(), 10))
	}
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		stage, cause = "http_exchange", err
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	reader.failureStatus, reader.failureBody = response.StatusCode, raw
	reader.failureOrdinal = ordinal
	closeErr := response.Body.Close()
	report, reportErr := decodeEpochReport(response.Trailer.Values(epochReadTrailer), ordinal, maximum)
	// Preserve every accepted positive ledger before interpreting HTTP/body
	// semantics. No history is retained and a later oracle mismatch cannot
	// replace this actual prefix with zero or retry the consumed ordinal.
	if reportErr == nil && len(response.Header.Values(epochReadTrailer)) == 0 && len(response.Trailer) == 1 {
		count, countErr := checkedInspectionReadSum(reader.reports, 1)
		controls, controlErr := checkedInspectionReadSum(reader.totals.ControlFileReads, report.ControlFileReads)
		stores, storeErr := checkedInspectionReadSum(reader.totals.StoreReadAttempts, report.StoreReadAttempts)
		members, memberErr := checkedInspectionReadSum(reader.totals.MemberVisits, report.MemberVisits)
		if countErr != nil || controlErr != nil || storeErr != nil || memberErr != nil {
			stage = "ledger_overflow"
			return nil, response.StatusCode, response.Header.Get("Content-Type"), report, errEpochInspection
		}
		reader.reports, reader.totals = count, readaccounting.Counts{ControlFileReads: controls, StoreReadAttempts: stores, MemberVisits: members}
	}
	if readErr != nil || closeErr != nil || int64(len(raw)) > limit || ctx.Err() != nil || reportErr != nil || !validEpochQueryRepositories(report, repositories) ||
		len(response.Header.Values(epochReadTrailer)) != 0 || len(response.Trailer) != 1 || response.Uncompressed || response.Header.Get("Content-Encoding") != "" ||
		payload != nil && len(response.Header.Values("Content-Type")) != 1 {
		stage, cause = "response_read_or_accounting", errors.Join(readErr, closeErr, context.Cause(ctx), reportErr)
		return nil, response.StatusCode, response.Header.Get("Content-Type"), report, errEpochInspection
	}
	return raw, response.StatusCode, response.Header.Get("Content-Type"), report, nil
}

func (reader *executionEpochInspection) fail(err error) {
	if err == nil {
		reader.failureStatus, reader.failureBody = 0, nil
		reader.failureOrdinal = 0
		return
	}
	reader.err = errEpochInspection
	if reader.readFailure.Stage == "" {
		reader.readFailure = epochReadFailure{Stage: "inspection_state_or_semantics", Ordinal: reader.failureOrdinal, Cause: err}
	}
	if reader.run != nil {
		reader.run.mu.Lock()
		reader.run.err = ErrExecutionEpochOne
		reader.run.mu.Unlock()
		if reader.run.stop != nil {
			reader.run.stopOnce.Do(func() { close(reader.run.stop) })
		}
	}
}

func (reader *executionEpochInspection) Progress(ctx context.Context) (result epochProgressInspection, report epochInspectionReport, retErr error) {
	if reader == nil {
		return result, report, errEpochInspection
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || reader.progressReady || reader.finalUsed || reader.progressCalls >= reader.bounds.ExtractionProgressCalls.Maximum {
		return result, report, errEpochInspection
	}
	if reader.projection.Phase == "archive_restore" && reader.archiveManifest == nil {
		return result, report, errEpochInspection
	}
	reader.progressCalls++
	maximum := epochInspectionReport{ControlFileReads: 2 + uint64(len(reader.plan.Profile.Pipeline.ExtractionDomains)), StoreReadAttempts: 2 + 2*store.MaxGenerationScheduleReadAttempts}
	raw, status, report, err := reader.read(ctx, api.ExtractionProgressPath+"?"+url.Values{"repository": {reader.run.epoch.Repository}}.Encode(), api.ExtractionProgressResponseLimit, maximum)
	result.HTTPStatus = status
	if err != nil {
		return result, report, err
	}
	progress, err := decodeEpochProgress(raw, status, "http://"+reader.run.epoch.Listen)
	if err != nil {
		return result, report, err
	}
	result.Progress = progress
	if progress != nil && progress.State != "unavailable" {
		if progress.Failed != 0 || progress.State == string(store.GenerationScheduleSuperseded) || progress.Domains != len(reader.plan.Profile.Pipeline.ExtractionDomains) || uint64(progress.Total) != reader.plan.Profile.Physical.CombinedModeledPartitions {
			return result, report, errEpochInspection
		}
		reader.progressReady = progress.State == "current"
		if reader.progressReady && (report.ControlFileReads != maximum.ControlFileReads || report.StoreReadAttempts < 4) {
			return result, report, errEpochInspection
		}
	}
	return result, report, nil
}

func decodeEpochProgress(raw []byte, status int, base string) (*extractionpublication.Progress, error) {
	if status == http.StatusOK {
		value := struct {
			Schema string `json:"$schema"`
			extractionpublication.Progress
		}{}
		if decodeEpochJSON(raw, &value, false) != nil || value.Schema != base+"/schemas/ExtractionProgress.json" || extractionpublication.ValidateProgress(value.Progress) != nil {
			return nil, errEpochInspection
		}
		return &value.Progress, nil
	}
	// Poll only the three actual source-free errors emitted by the owning API;
	// no status-only 404/409 acceptance and no generic 5xx retry exists.
	value := struct {
		Schema string `json:"$schema"`
		Type   string `json:"type,omitempty"`
		Title  string `json:"title,omitempty"`
		Status int    `json:"status,omitempty"`
		Detail string `json:"detail,omitempty"`
	}{}
	if decodeEpochJSON(raw, &value, false) != nil || value.Schema != base+"/schemas/ErrorModel.json" || value.Type != "" && value.Type != "about:blank" || value.Status != status || value.Title != http.StatusText(status) {
		return nil, errEpochInspection
	}
	if status == http.StatusNotFound && value.Detail == "extraction progress not found" || status == http.StatusConflict && (value.Detail == api.ExtractionProgressDetailStale || value.Detail == api.ExtractionProgressDetailAuthority) {
		return nil, nil
	}
	return nil, errEpochInspection
}

func (reader *executionEpochInspection) Tail(ctx context.Context) (result epochTailReadiness, report epochInspectionReport, retErr error) {
	if reader == nil {
		return result, report, errEpochInspection
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || !reader.progressReady || reader.tail.Status == "ready" || reader.finalUsed || reader.tailCalls >= reader.bounds.TailReadinessCalls.Maximum {
		return result, report, errEpochInspection
	}
	reader.tailCalls++
	raw, status, report, err := reader.read(ctx, "/api/t421/tail-readiness", 4<<10, epochInspectionReport{ControlFileReads: 4, StoreReadAttempts: 4})
	if err != nil || status != http.StatusOK || decodeEpochJSON(raw, &result, false) != nil || result.Schema != "t421-tail-readiness-source-free-v1" {
		return result, report, errEpochInspection
	}
	switch result.Status {
	case "pending":
		if result != (epochTailReadiness{Schema: result.Schema, Status: "pending"}) {
			return result, report, errEpochInspection
		}
	case "ready":
		if report.ControlFileReads != 4 || report.StoreReadAttempts != 4 {
			return result, report, errEpochInspection
		}
		for _, digest := range []string{result.SelectedRuntimeSHA256, result.RelationshipGenerationSHA256, result.RelationshipRootSHA256, result.CallerGenerationSHA256, result.CallerRootSHA256} {
			if !validDigest(digest) {
				return result, report, errEpochInspection
			}
		}
		if reader.projection.Phase == "physical_delta_b" || reader.projection.Phase == "logical_delta_b" || reader.projection.Phase == "return_a" || reader.projection.Phase == "archive_restore" || reader.projection.Phase == "lifecycle_collection" || reader.projection.Phase == "product_queries" {
			prior := reader.warmAuthority
			switch reader.projection.Phase {
			case "logical_delta_b":
				prior = reader.physicalAuthority
			case "return_a":
				prior = reader.logicalAuthority
			case "archive_restore":
				prior = reader.archivePrior
			case "lifecycle_collection":
				prior = reader.archiveAuthority
			case "product_queries":
				prior = reader.collectionAuthority
			}
			ready, err := correctedTailReadinessTransitionReady(reader.projection.Phase, &tailReadinessIdentity{
				RelationshipGenerationSHA256: prior.RelationshipGenerationSHA256, RelationshipRootSHA256: prior.RelationshipRootSHA256,
				CallerGenerationSHA256: prior.CallerGenerationSHA256, CallerRootSHA256: prior.CallerRootSHA256}, tailReadinessIdentity{
				RelationshipGenerationSHA256: result.RelationshipGenerationSHA256, RelationshipRootSHA256: result.RelationshipRootSHA256,
				CallerGenerationSHA256: result.CallerGenerationSHA256, CallerRootSHA256: result.CallerRootSHA256})
			if err != nil {
				return result, report, errEpochInspection
			}
			if !ready {
				return epochTailReadiness{Schema: result.Schema, Status: "pending"}, report, nil
			}
		}
	default:
		return result, report, errEpochInspection
	}
	reader.tail = result
	return result, report, nil
}

// These closed wire structs deliberately omit caller-supplied phase/revision
// fields. Their other fields retain the native final-reader/receipt JSON shape.
type epochFinalAuthority struct {
	PhysicalCommit                  string      `json:"physical_commit"`
	PhysicalTree                    string      `json:"physical_tree"`
	SourceGenerationSHA256          string      `json:"source_generation_sha256"`
	SearchGenerationSHA256          string      `json:"search_generation_sha256"`
	ObservationGenerationSHA256     string      `json:"observation_generation_sha256"`
	CandidateGenerationSHA256       string      `json:"candidate_generation_sha256"`
	CatalogRootSHA256               string      `json:"catalog_root_sha256"`
	CatalogActivationPlanSHA256     string      `json:"catalog_activation_plan_sha256"`
	CatalogActivationScheduleSHA256 string      `json:"catalog_activation_schedule_sha256"`
	CatalogActivationUnitSHA256     string      `json:"catalog_activation_unit_sha256"`
	ResolverCatalogGenerationSHA256 string      `json:"resolver_catalog_generation_sha256"`
	ResolverCatalogRootSHA256       string      `json:"resolver_catalog_root_sha256"`
	CallerGenerationSHA256          string      `json:"caller_generation_sha256"`
	CallerRootSHA256                string      `json:"caller_root_sha256"`
	RelationshipGenerationSHA256    string      `json:"relationship_generation_sha256"`
	RelationshipRootSHA256          string      `json:"relationship_root_sha256"`
	RelationshipProvenanceSHA256    string      `json:"relationship_provenance_sha256"`
	SearchInventory                 SetIdentity `json:"search_inventory"`
	ObservationInputInventory       SetIdentity `json:"observation_input_inventory"`
	ExtractionRootsSHA256           string      `json:"extraction_roots_sha256"`
	Current                         bool        `json:"current"`
}

type epochFinalProjection struct {
	Schema                    string                     `json:"schema"`
	CatalogLogicalSHA256      string                     `json:"catalog_logical_sha256"`
	SemanticSHA256            string                     `json:"semantic_sha256"`
	CatalogSource             CatalogSourceProfile       `json:"catalog_source"`
	Catalog                   SetIdentity                `json:"catalog"`
	MembershipSet             SetIdentity                `json:"membership_set"`
	Placements                SetIdentity                `json:"placements"`
	UnownedPrefixes           SetIdentity                `json:"unowned_prefixes"`
	ServiceQueries            SetIdentity                `json:"service_queries"`
	SearchInventory           SetIdentity                `json:"search_inventory"`
	ObservationInputInventory SetIdentity                `json:"observation_input_inventory"`
	ExtractionRoots           []ExtractionRootProjection `json:"extraction_roots"`
	RelationshipResults       []RelationshipResult       `json:"relationship_results"`
	ProductRelationship       ProductRelationshipResult  `json:"product_relationship"`
}

type epochFinalResponse struct {
	Schema          string                 `json:"schema"`
	Authority       epochFinalAuthority    `json:"authority"`
	Projection      epochFinalProjection   `json:"projection"`
	ExtractionRoots []ExtractionRootResult `json:"extraction_roots"`
	QueryAuthority  *epochQueryAuthority   `json:"query_authority,omitempty"`
}

type epochQueryAuthority struct {
	CatalogSourceGenerationSHA256     string `json:"catalog_source_generation_sha256"`
	ResolverNamespaceGenerationSHA256 string `json:"resolver_namespace_generation_sha256"`
	ResolverNamespaceRootSHA256       string `json:"resolver_namespace_root_sha256"`
}

func (value *epochQueryAuthority) valid() bool {
	return value != nil && validDigest(value.CatalogSourceGenerationSHA256) &&
		validDigest(value.ResolverNamespaceGenerationSHA256) && validDigest(value.ResolverNamespaceRootSHA256)
}

func (reader *executionEpochInspection) Final(ctx context.Context) (authority AuthorityPhaseResult, projection PhaseStateProjection, report epochInspectionReport, retErr error) {
	if reader == nil {
		return authority, projection, report, errEpochInspection
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	product := reader.projection.Phase == "product_queries"
	secondProductFinal := product && reader.productFinalCalls == 1 && reader.productQueriesComplete
	if reader.err != nil || !reader.progressReady || reader.tail.Status != "ready" || reader.finalUsed && !secondProductFinal {
		return authority, projection, report, errEpochInspection
	}
	if product && (reader.run == nil || reader.run.epoch.Epoch != 5 || !reader.restoredSamples.CollectionComplete ||
		reader.bounds.FinalAuthorityPasses != exactInspectionCalls(2) || reader.productFinalCalls > 1 ||
		reader.productFinalCalls == 0 && reader.productQueriesComplete || reader.productFinalCalls == 1 && (!reader.productQueriesComplete || !validExecutionProductQueries(reader.productQueries))) {
		return authority, projection, report, errEpochInspection
	}
	if secondProductFinal && (reader.productFirstFinalOrdinal == 0 || reader.productFirstFinalOrdinal == ^uint64(0) ||
		reader.productQueries[len(reader.productQueries)-1].LastOrdinal == ^uint64(0) || reader.productQueries[0].FirstOrdinal != reader.productFirstFinalOrdinal+1 ||
		reader.productQueries[len(reader.productQueries)-1].LastOrdinal+1 != reader.next) {
		return authority, projection, report, errEpochInspection
	}
	if reader.projection.Phase == "archive_restore" && reader.archiveManifest == nil {
		return authority, projection, report, errEpochInspection
	}
	if pressureInspectionPhase(reader.projection.Phase) && !reader.pressureFinalReady() {
		return authority, projection, report, errEpochInspection
	}
	if reader.projection.Phase == "lifecycle_collection" && (reader.restoredStep != 3 || reader.lifecycleCalls < reader.bounds.LifecycleStatusCalls.Minimum ||
		reader.lifecycleCalls > reader.bounds.LifecycleStatusCalls.Maximum || !reader.restoredSamples.ArchiveComplete) {
		return authority, projection, report, errEpochInspection
	}
	reader.finalUsed = true
	raw, status, report, err := reader.read(ctx, "/api/t421/final-authority", epochFinalResponseBytes, epochInspectionReport{ControlFileReads: correctedFinalAuthorityControlReadMaximum, StoreReadAttempts: correctedFinalAuthorityStoreReadMaximum, MemberVisits: correctedFinalAuthorityMemberReadMaximum})
	if err != nil || status != http.StatusOK {
		return authority, projection, report, errEpochInspection
	}
	authority, projection, err = reader.decodeFinal(raw)
	if err == nil && product {
		digest := sha256.Sum256(raw)
		if reader.productFinalCalls == 0 {
			reader.productBaseline = &digest
			reader.productFirstFinalOrdinal = report.RequestOrdinal
			reader.productAuthority = cloneArchiveAuthority(authority)
		} else if reader.productBaseline == nil || digest != *reader.productBaseline {
			return authority, projection, report, errEpochInspection
		}
		reader.productFinalDigests[reader.productFinalCalls] = digest
		reader.productFinalCalls++
	}
	if err == nil && authority.Phase == "process_restart" && reader.run.pressureAllowed && reader.run.epoch.Epoch == 4 {
		// decodeFinal has checked checkpoint recovery and byte equality with
		// the canonical typed response. This commits the actual full F,
		// including detailed roots, without copying its mutable return slices.
		digest := sha256.Sum256(raw)
		reader.pressureBaseline = &digest
	}
	if err == nil {
		row := &reader.evidence.rows[len(reader.evidence.rows)-1]
		row.Final = cloneInspectionFinal(ExecutionInspectionFinal{Ordinal: report.RequestOrdinal, Authority: authority.AuthorityState, Projection: projection})
	}
	if err == nil && authority.Phase == "cold" {
		reader.cold = authority
	}
	if err == nil && authority.Phase == "warm_noop" {
		reader.warmAuthority = authority
	}
	if err == nil && authority.Phase == "physical_delta_b" {
		reader.physicalAuthority = authority
	}
	if err == nil && authority.Phase == "logical_delta_b" {
		reader.logicalAuthority = authority
	}
	if err == nil && authority.Phase == "return_a" {
		reader.returnAuthority = authority
	}
	if err == nil && authority.Phase == "stale_lease" {
		reader.staleAuthority = authority
	}
	if err == nil && authority.Phase == "archive_restore" {
		reader.archiveAuthority = cloneArchiveAuthority(authority)
	}
	if err == nil && authority.Phase == "lifecycle_collection" {
		reader.collectionAuthority = cloneArchiveAuthority(authority)
	}
	return authority, projection, report, err
}

func (reader *executionEpochInspection) decodeFinal(raw []byte) (authority AuthorityPhaseResult, projection PhaseStateProjection, retErr error) {
	var value epochFinalResponse
	if len(raw) > epochFinalResponseBytes || decodeEpochJSON(raw, &value, true) != nil || value.Schema != "t421-final-authority-source-free-v1" || value.Projection.Schema != "t421-final-state-projection-source-free-v1" {
		return authority, projection, errEpochInspection
	}
	// Convert only already-validated closed source-free fields; phase/revision
	// provenance comes from the protected plan and the actual author response.
	encoded, _ := json.Marshal(value.Authority)
	if json.Unmarshal(encoded, &authority.AuthorityState) != nil {
		return authority, projection, errEpochInspection
	}
	encoded, _ = json.Marshal(value.Projection)
	if json.Unmarshal(encoded, &projection) != nil {
		return authority, projection, errEpochInspection
	}
	phase := reader.projection.Phase
	if phase != "product_queries" && value.QueryAuthority != nil || phase == "product_queries" && !value.QueryAuthority.valid() {
		return authority, projection, errEpochInspection
	}
	if phase != "cold" && phase != "warm_noop" && phase != "physical_delta_b" && phase != "logical_delta_b" && phase != "return_a" && phase != "stale_lease" && phase != "process_restart" && phase != "archive_restore" && phase != "lifecycle_collection" && phase != "product_queries" && !pressureInspectionPhase(phase) {
		return authority, projection, errEpochInspection
	}
	authority.Phase, authority.Outcome = phase, "passed"
	physical := reader.projection.PhysicalRevision
	logical := reader.projection.LogicalRevision
	authority.PhysicalRevision, authority.LogicalRevision = physical, logical
	authority.ExtractionRoots = value.ExtractionRoots
	projection.Schema, projection.Phase, projection.PhysicalRevision, projection.LogicalRevision = reader.projection.Schema, phase, physical, logical
	if !reflect.DeepEqual(projection, reader.projection) || authority.RelationshipGenerationSHA256 != reader.tail.RelationshipGenerationSHA256 ||
		authority.RelationshipRootSHA256 != reader.tail.RelationshipRootSHA256 || authority.CallerGenerationSHA256 != reader.tail.CallerGenerationSHA256 || authority.CallerRootSHA256 != reader.tail.CallerRootSHA256 {
		return authority, projection, errEpochInspection
	}
	if phase == "process_restart" {
		if !reader.checkpointFinalMatches(authority) {
			return authority, projection, errEpochInspection
		}
		return authority, projection, nil
	}
	if phase == "archive_restore" {
		physicalPlan, ok := namedPhysicalRevision(reader.plan.Revisions.Physical, physical)
		if reader.run == nil || reader.run.epoch.Epoch != 5 || reader.plan.Schema != PlanV3Schema || reader.archiveManifest == nil ||
			reader.archivePrior.Phase != "pressure_75" || reader.archivePrior.Outcome != "passed" || !ok ||
			!validDigest(authority.RelationshipProvenanceSHA256) || validateAuthorityCoverage(authority, physicalPlan, reader.plan) != nil ||
			validateArchiveAuthorityContinuity(authority, reader.archivePrior, reader.plan) != nil {
			return authority, projection, errEpochInspection
		}
		return authority, projection, nil
	}
	if phase == "lifecycle_collection" {
		prior := reader.archiveAuthority
		prior.Phase = phase
		if reader.run == nil || reader.run.epoch.Epoch != 5 || reader.plan.Schema != PlanV3Schema || !reader.restoredSamples.ArchiveComplete ||
			reader.archiveAuthority.Phase != "archive_restore" || !reflect.DeepEqual(authority, prior) {
			return authority, projection, errEpochInspection
		}
		return authority, projection, nil
	}
	if phase == "product_queries" {
		prior := reader.collectionAuthority
		prior.Phase = phase
		if reader.run == nil || reader.run.epoch.Epoch != 5 || reader.plan.Schema != PlanV3Schema || !reader.restoredSamples.CollectionComplete ||
			reader.collectionAuthority.Phase != "lifecycle_collection" || !reflect.DeepEqual(authority, prior) {
			return authority, projection, errEpochInspection
		}
		if reader.productFinalCalls > 0 && reader.productQueryAuthority != *value.QueryAuthority {
			return authority, projection, errEpochInspection
		}
		reader.productQueryAuthority = *value.QueryAuthority // Detached scalar from the validated actual F.
		return authority, projection, nil
	}
	if pressureInspectionPhase(phase) {
		if reader.run == nil || !reader.run.pressureAllowed || reader.run.epoch.Epoch != 4 || reader.pressureBaseline == nil || sha256.Sum256(raw) != *reader.pressureBaseline {
			return authority, projection, errEpochInspection
		}
		return authority, projection, nil
	}
	if phase == "stale_lease" {
		// The actual return-A value already passed the full native/protected
		// projection validator. Reuse requires equality of every authority and
		// detailed root field, not a regenerated or partial expectation.
		prior := reader.returnAuthority
		prior.Phase = phase
		if reader.returnAuthority.Phase != "return_a" || !reader.stalePrepared || reader.staleRecovered.Point != store.GenerationStaleLeaseTransitionRecovered ||
			!reflect.DeepEqual(authority, prior) {
			return authority, projection, errEpochInspection
		}
		return authority, projection, nil
	}
	revisions := []RevisionResult{{Name: "a", PhysicalOutcome: "passed", LogicalOutcome: "passed", PhysicalCommit: reader.authored.Commit, PhysicalTree: reader.authored.Tree}}
	values, phases := []AuthorityPhaseResult{authority}, []string{phase}
	outcomes := map[string]string{phase: "passed"}
	states := map[string]authorityState{phase: {PhysicalRevision: physical, LogicalRevision: logical}}
	if phase == "warm_noop" || phase == "physical_delta_b" || phase == "logical_delta_b" || phase == "return_a" {
		values, phases = append(values, reader.cold), append(phases, "cold")
		outcomes["cold"], states["cold"] = "passed", authorityState{PhysicalRevision: "a", LogicalRevision: "a"}
	}
	if phase == "physical_delta_b" || phase == "logical_delta_b" || phase == "return_a" {
		revisions[0].PhysicalCommit, revisions[0].PhysicalTree = reader.cold.PhysicalCommit, reader.cold.PhysicalTree
		revisions = append(revisions, RevisionResult{Name: "b", PhysicalOutcome: "passed", LogicalOutcome: "not_run", PhysicalCommit: reader.authored.Commit, PhysicalTree: reader.authored.Tree})
		values, phases = append(values, reader.warmAuthority), append(phases, "warm_noop")
		outcomes["warm_noop"], states["warm_noop"] = "passed", authorityState{PhysicalRevision: "a", LogicalRevision: "a"}
	}
	if phase == "logical_delta_b" {
		revisions[1].LogicalOutcome = "passed"
		values, phases = append(values, reader.physicalAuthority), append(phases, "physical_delta_b")
		outcomes["physical_delta_b"], states["physical_delta_b"] = "passed", authorityState{PhysicalRevision: "b", LogicalRevision: "a"}
		recovered := reader.activationRecovered
		if recovered.Schema == "" || authority.CatalogRootSHA256 != recovered.CatalogRootDigest || authority.CatalogActivationPlanSHA256 != recovered.PlanDigest ||
			authority.CatalogActivationScheduleSHA256 != recovered.ScheduleDigest || authority.CatalogActivationUnitSHA256 != recovered.UnitDigest {
			return authority, projection, errEpochInspection
		}
	}
	if phase == "return_a" {
		revisions[1] = RevisionResult{Name: "b", PhysicalOutcome: "passed", LogicalOutcome: "passed",
			PhysicalCommit: reader.logicalAuthority.PhysicalCommit, PhysicalTree: reader.logicalAuthority.PhysicalTree}
		revisions = append(revisions, RevisionResult{Name: "a-return", PhysicalOutcome: "passed", LogicalOutcome: "passed",
			PhysicalCommit: reader.authored.Commit, PhysicalTree: reader.authored.Tree})
		values, phases = append(values, reader.physicalAuthority, reader.logicalAuthority), append(phases, "physical_delta_b", "logical_delta_b")
		outcomes["physical_delta_b"], states["physical_delta_b"] = "passed", authorityState{PhysicalRevision: "b", LogicalRevision: "a"}
		outcomes["logical_delta_b"], states["logical_delta_b"] = "passed", authorityState{PhysicalRevision: "b", LogicalRevision: "b"}
		// Native R opened this exact content-addressed root, including its
		// authority digest. F independently binds the same root to protected
		// return-A projection, rather than treating R as a full F substitute.
		recovered := reader.markerRecovered
		if recovered.Schema == "" || authority.RelationshipGenerationSHA256 != recovered.TargetGenerationDigest || authority.RelationshipRootSHA256 != recovered.TargetRootDigest {
			return authority, projection, errEpochInspection
		}
	}
	if validateAuthorityResults(values, phases, outcomes, states, revisions, reader.plan) != nil {
		return authority, projection, errEpochInspection
	}
	return authority, projection, nil
}

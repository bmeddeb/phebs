package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"sync"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
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
	Schema             string `json:"schema"`
	RequestOrdinal     uint64 `json:"request_ordinal"`
	Status             string `json:"status"`
	ControlFileReads   uint64 `json:"control_file_reads"`
	StoreReadAttempts  uint64 `json:"store_read_attempts"`
	MemberVisits       uint64 `json:"member_visits"`
	StoreWriteAttempts uint64 `json:"store_write_attempts"`
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
	mu                             sync.Mutex
	run                            *ExecutionEpochOneRun
	plan                           Plan
	authored                       AuthoredExecutionRevision
	projection                     PhaseStateProjection
	bounds                         phaseInspectionInventory
	next, progressCalls, tailCalls uint64
	reports                        uint64
	totals                         readaccounting.Counts
	progressReady                  bool
	tail                           epochTailReadiness
	finalUsed                      bool
	err                            error
	// Private failed-response diagnostic only, never receipt evidence. Retain
	// the already bounded body (at most the response cap plus one sentinel).
	failureStatus int
	failureBody   []byte
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
	if !author.active || author.next != 1 || author.previous == nil || author.previous.Result != author.expected[0] || author.check(ctx) != nil {
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
func (reader *executionEpochInspection) read(ctx context.Context, path string, limit int64, maximum epochInspectionReport) ([]byte, int, epochInspectionReport, error) {
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || reader.run.control == nil || reader.next == 0 || reader.next > 11531 {
		return nil, 0, epochInspectionReport{}, errEpochInspection
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
		return nil, 0, epochInspectionReport{}, errEpochInspection
	}
	token := run.control.RequestToken()
	if token == "" {
		return nil, 0, epochInspectionReport{}, errEpochInspection
	}
	ordinal := reader.next
	reader.next++
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+run.epoch.Listen+path, nil)
	if err != nil {
		return nil, 0, epochInspectionReport{}, errEpochInspection
	}
	request.Header.Set("Authorization", "Bearer "+run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	request.Header.Set("X-Phebs-T421-Exact-Reads", "source-free-v1")
	request.Header.Set("X-Phebs-T421-Exact-Read-Ordinal", strconv.FormatUint(ordinal, 10))
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, epochInspectionReport{}, errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	reader.failureStatus, reader.failureBody = response.StatusCode, raw
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
			return nil, response.StatusCode, report, errEpochInspection
		}
		reader.reports, reader.totals = count, readaccounting.Counts{ControlFileReads: controls, StoreReadAttempts: stores, MemberVisits: members}
	}
	if readErr != nil || closeErr != nil || int64(len(raw)) > limit || ctx.Err() != nil || reportErr != nil ||
		len(response.Header.Values(epochReadTrailer)) != 0 || len(response.Trailer) != 1 || response.Uncompressed || response.Header.Get("Content-Encoding") != "" {
		return nil, response.StatusCode, report, errEpochInspection
	}
	return raw, response.StatusCode, report, nil
}

func (reader *executionEpochInspection) fail(err error) {
	if err == nil {
		reader.failureStatus, reader.failureBody = 0, nil
		return
	}
	reader.err = errEpochInspection
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
}

func (reader *executionEpochInspection) Final(ctx context.Context) (authority AuthorityPhaseResult, projection PhaseStateProjection, report epochInspectionReport, retErr error) {
	if reader == nil {
		return authority, projection, report, errEpochInspection
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || !reader.progressReady || reader.tail.Status != "ready" || reader.finalUsed {
		return authority, projection, report, errEpochInspection
	}
	reader.finalUsed = true
	raw, status, report, err := reader.read(ctx, "/api/t421/final-authority", epochFinalResponseBytes, epochInspectionReport{ControlFileReads: correctedFinalAuthorityControlReadMaximum, StoreReadAttempts: correctedFinalAuthorityStoreReadMaximum, MemberVisits: correctedFinalAuthorityMemberReadMaximum})
	if err != nil || status != http.StatusOK {
		return authority, projection, report, errEpochInspection
	}
	authority, projection, err = reader.decodeFinal(raw)
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
	authority.Phase, authority.Outcome = "cold", "passed"
	authority.PhysicalRevision, authority.LogicalRevision = "a", "a"
	authority.ExtractionRoots = value.ExtractionRoots
	projection.Schema, projection.Phase, projection.PhysicalRevision, projection.LogicalRevision = reader.projection.Schema, "cold", "a", "a"
	if !reflect.DeepEqual(projection, reader.projection) || authority.RelationshipGenerationSHA256 != reader.tail.RelationshipGenerationSHA256 ||
		authority.RelationshipRootSHA256 != reader.tail.RelationshipRootSHA256 || authority.CallerGenerationSHA256 != reader.tail.CallerGenerationSHA256 || authority.CallerRootSHA256 != reader.tail.CallerRootSHA256 {
		return authority, projection, errEpochInspection
	}
	revisions := []RevisionResult{{Name: "a", PhysicalOutcome: "passed", LogicalOutcome: "passed", PhysicalCommit: reader.authored.Commit, PhysicalTree: reader.authored.Tree}}
	if validateAuthorityResults([]AuthorityPhaseResult{authority}, []string{"cold"}, map[string]string{"cold": "passed"}, map[string]authorityState{"cold": {PhysicalRevision: "a", LogicalRevision: "a"}}, revisions, reader.plan) != nil {
		return authority, projection, errEpochInspection
	}
	return authority, projection, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t421ExactReadsEnvironment = "PHEBS_T421_EXACT_READS"
	t421ExactReadsContract    = "source-free-v1"

	t421ExactReadActivationHeader = "X-Phebs-T421-Exact-Reads"
	t421ExactReadOrdinalHeader    = "X-Phebs-T421-Exact-Read-Ordinal"
	t421ExactReadTrailer          = "X-Phebs-T421-Exact-Read-Report"
	t421ExactReadReportSchema     = "t421-source-free-read-accounting-v1"
	t421ExactFinalAuthorityPath   = "/api/t421/final-authority"
	t421ExactTailReadinessPath    = "/api/t421/tail-readiness"
	t421ExactMCPPath              = "/api/mcp"
	t421ExactMCPRequestBytes      = 64 << 10
	t421ExactJSONMaxDepth         = 16
	t421ProductServicePath        = "/api/service"
	t421ProductRelationshipsPath  = "/api/service-relationships"

	// The complete phase inventory's largest live server epoch admits 11,531
	// shared API/MCP requests. A new serve process starts again at ordinal one.
	t421ExactReadMaxOrdinal uint64 = 11_531
)

var (
	errT421ExactReadAdmission  = errors.New("T42.1 exact-read admission failed")
	errT421ExactReadAccounting = errors.New("T42.1 exact-read accounting failed")
	errT421ExactReadReport     = errors.New("T42.1 exact-read report failed")
	errT421ExactReadIncomplete = errors.New("T42.1 exact-read handler incomplete")
	errT421ExactReadAuthority  = errors.New("T42.1 exact final-authority read failed")
	errT421ExactReadTail       = errors.New("T42.1 exact tail-readiness read failed")
	errT421ExactReadResponse   = errors.New("T42.1 exact-read response failed")
	errT421ExactReadCommit     = errors.New("T42.1 exact-read commit failed")
)

// Read returns canonical source-free JSON after its route-specific fences.
// Its optional commit must leave prepared caches untouched when it returns an
// error; the transport invokes it only after the body write and clean ledger.
type t421ExactFinalAuthorityRead struct {
	Limits readaccounting.Counts
	Read   func(context.Context) ([]byte, func() error, error)
}

type t421ExactReadReport struct {
	Schema             string `json:"schema"`
	RequestOrdinal     uint64 `json:"request_ordinal"`
	Status             string `json:"status"`
	ControlFileReads   uint64 `json:"control_file_reads"`
	StoreReadAttempts  uint64 `json:"store_read_attempts"`
	MemberVisits       uint64 `json:"member_visits"`
	StoreWriteAttempts uint64 `json:"store_write_attempts"`
}

type t421ExactReadAccountingHandler struct {
	next  http.Handler
	state *t421ExactReadAccountingState
}

type t421ExactReadAccountingState struct {
	report func([]byte) error
	fail   func(error)
	final  t421ExactFinalAuthorityRead
	tail   t421ExactFinalAuthorityRead

	mu          sync.Mutex
	nextOrdinal uint64
	active      bool
	failed      bool
}

var _ http.Handler = (*t421ExactReadAccountingHandler)(nil)

func t421ExactReadsEnabled() (bool, error) {
	value, present := os.LookupEnv(t421ExactReadsEnvironment)
	if !present {
		return false, nil
	}
	if value != t421ExactReadsContract {
		return false, errors.New("T42.1 exact-read contract is invalid")
	}
	return true, nil
}

func t421ExactReadTerminalError(failed <-chan error) error {
	select {
	case failure := <-failed:
		return errors.Join(errors.New("T42.1 exact-read reporting failed"), failure)
	default:
		return nil
	}
}

// t421ExactReadHandler is installed inside auth.Require. The disabled form
// returns next itself, so ordinary requests acquire no wrapper, state, or work.
func t421ExactReadHandler(
	enabled bool,
	next http.Handler,
	report func([]byte) error,
	fail func(error),
	final ...t421ExactFinalAuthorityRead,
) http.Handler {
	if !enabled {
		return next
	}
	return t421NewExactReadAccountingState(report, fail, final...).wrap(next)
}

// t421ExactReadHandlers gives the API and stateless MCP transports one exact
// ordinal/failure boundary. Production must still install authentication
// outside both returned handlers.
func t421ExactReadHandlers(
	enabled bool,
	apiHandler, mcpHandler http.Handler,
	report func([]byte) error,
	fail func(error),
	final ...t421ExactFinalAuthorityRead,
) (http.Handler, http.Handler) {
	if !enabled {
		return apiHandler, mcpHandler
	}
	state := t421NewExactReadAccountingState(report, fail, final...)
	return state.wrap(apiHandler), state.wrap(mcpHandler)
}

func t421NewExactReadAccountingState(
	report func([]byte) error,
	fail func(error),
	final ...t421ExactFinalAuthorityRead,
) *t421ExactReadAccountingState {
	if report == nil || fail == nil || len(final) > 2 {
		panic("T42.1 exact-read reporting is incomplete")
	}
	state := &t421ExactReadAccountingState{
		report: report, fail: fail, nextOrdinal: 1,
	}
	if len(final) == 1 {
		state.final = final[0]
	} else if len(final) == 2 {
		state.final, state.tail = final[0], final[1]
	}
	return state
}

func (state *t421ExactReadAccountingState) wrap(next http.Handler) http.Handler {
	if state == nil || next == nil {
		panic("T42.1 exact-read reporting is incomplete")
	}
	return &t421ExactReadAccountingHandler{next: next, state: state}
}

func (handler *t421ExactReadAccountingHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !t421ExactReadAttempt(request) {
		handler.next.ServeHTTP(writer, request)
		return
	}
	limits, target := t421ExactReadLimits(request, handler.state.final, handler.state.tail)
	ordinal, ok := handler.state.admit(request, target)
	if !ok {
		handler.state.refuse(writer, 0, "admission_refused", errT421ExactReadAdmission)
		return
	}

	ctx, ledger, err := readaccounting.Start(request.Context(), limits)
	if err != nil {
		handler.state.refuse(
			writer, ordinal, "accounting_refused", errT421ExactReadAccounting,
		)
		handler.state.release(nil)
		return
	}
	writer.Header().Add("Trailer", t421ExactReadTrailer)

	completed := false
	status := "complete"
	var terminalErr error
	var commit func() error
	defer func() {
		counts, accountingErr := ledger.Finish()
		if accountingErr != nil {
			status = "accounting_refused"
			terminalErr = errors.Join(terminalErr, errT421ExactReadAccounting)
		}
		if !completed && terminalErr == nil {
			status = "handler_incomplete"
			terminalErr = errors.Join(terminalErr, errT421ExactReadIncomplete)
		}
		if completed && terminalErr == nil && commit != nil {
			if err := handler.state.commit(commit); err != nil {
				status = "commit_refused"
				terminalErr = errT421ExactReadCommit
			}
		}
		reportErr := handler.state.emit(writer, t421ExactReadReport{
			Schema:             t421ExactReadReportSchema,
			RequestOrdinal:     ordinal,
			Status:             status,
			ControlFileReads:   counts.ControlFileReads,
			StoreReadAttempts:  counts.StoreReadAttempts,
			MemberVisits:       counts.MemberVisits,
			StoreWriteAttempts: counts.StoreWriteAttempts,
		})
		if reportErr != nil {
			terminalErr = errors.Join(terminalErr, errT421ExactReadReport)
		}
		handler.state.release(terminalErr)
	}()

	if request.URL.Path == t421ExactFinalAuthorityPath ||
		request.URL.Path == t421ExactTailReadinessPath {
		read, failureStatus, failure := handler.state.final, "final_authority_refused", errT421ExactReadAuthority
		if request.URL.Path == t421ExactTailReadinessPath {
			read, failureStatus, failure = handler.state.tail, "tail_readiness_refused", errT421ExactReadTail
		}
		canonical, pendingCommit, readErr := read.Read(ctx)
		if readErr != nil || !json.Valid(canonical) {
			status = failureStatus
			terminalErr = failure
			http.Error(writer, "exact read request refused", http.StatusConflict)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		written, writeErr := writer.Write(canonical)
		if writeErr != nil || written != len(canonical) {
			status = "response_write_refused"
			terminalErr = errT421ExactReadResponse
			return
		}
		commit = pendingCommit
		completed = true
		return
	}

	handler.next.ServeHTTP(writer, request.WithContext(ctx))
	completed = true
}

func (state *t421ExactReadAccountingState) admit(request *http.Request, target bool) (uint64, bool) {
	activation := request.Header.Values(t421ExactReadActivationHeader)
	ordinals := request.Header.Values(t421ExactReadOrdinalHeader)
	principal, authenticated := auth.PrincipalFromContext(request.Context())
	if !target || len(activation) != 1 || activation[0] != t421ExactReadsContract ||
		len(ordinals) != 1 || !authenticated || !t421ExactReadLegacyPrincipal(principal) {
		return 0, false
	}
	// Bound parsing before ParseUint so an attacker cannot turn this private
	// exact-mode boundary into text work.
	if len(ordinals[0]) == 0 || len(ordinals[0]) > 20 {
		return 0, false
	}
	ordinal, err := strconv.ParseUint(ordinals[0], 10, 64)
	if err != nil || strconv.FormatUint(ordinal, 10) != ordinals[0] ||
		ordinal == 0 || ordinal > t421ExactReadMaxOrdinal {
		return 0, false
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.failed || state.active || ordinal != state.nextOrdinal {
		return 0, false
	}
	state.active = true
	state.nextOrdinal++
	return ordinal, true
}

func t421ExactReadAttempt(request *http.Request) bool {
	if request.URL.Path == api.ExtractionProgressPath || request.URL.Path == api.LifecycleStatusPath {
		return true
	}
	if _, authenticated := auth.PrincipalFromContext(request.Context()); !authenticated {
		return false
	}
	return len(request.Header.Values(t421ExactReadActivationHeader)) != 0 ||
		len(request.Header.Values(t421ExactReadOrdinalHeader)) != 0
}

func t421ExactReadLimits(
	request *http.Request,
	final ...t421ExactFinalAuthorityRead,
) (readaccounting.Counts, bool) {
	if request == nil || request.URL == nil || request.URL.Path != request.URL.EscapedPath() {
		return readaccounting.Counts{}, false
	}
	if request.URL.Path == t421ExactMCPPath {
		return t421ExactMCPReadLimits(request)
	}
	if request.Method != http.MethodGet {
		return readaccounting.Counts{}, false
	}
	switch request.URL.Path {
	case api.ExtractionProgressPath:
		return t421ExtractionProgressReadLimits(), true
	case api.LifecycleStatusPath:
		// The authenticated endpoint snapshots already-maintained in-memory
		// lifecycle state. A zero limit makes any future native read fail closed.
		return readaccounting.Counts{}, true
	case t421ExactFinalAuthorityPath:
		if request.URL.RawQuery != "" || request.URL.ForceQuery || len(final) < 1 ||
			!t421ExactFinalAuthorityReadAvailable(final[0]) {
			return readaccounting.Counts{}, false
		}
		return final[0].Limits, true
	case t421ExactTailReadinessPath:
		if request.URL.RawQuery != "" || request.URL.ForceQuery || len(final) != 2 ||
			!t421ExactFinalAuthorityReadAvailable(final[1]) {
			return readaccounting.Counts{}, false
		}
		return final[1].Limits, true
	case api.SearchPath:
		return t421ExactSearchReadLimits(request.URL.RawQuery)
	case t421ProductServicePath:
		return t421ExactServiceReadLimits(request.URL.RawQuery)
	case t421ProductRelationshipsPath:
		return t421ExactRelationshipReadLimits(request.URL.RawQuery)
	default:
		return readaccounting.Counts{}, false
	}
}

func t421ExactSearchReadLimits(rawQuery string) (readaccounting.Counts, bool) {
	query, ok := t421ExactProductQuery(rawQuery, []string{
		"q", "scope", "repository", "service_key", "max_matches", "context_lines",
	})
	if !ok || query.Get("q") == "" || query.Get("max_matches") != "1" ||
		query.Get("context_lines") != "0" {
		return readaccounting.Counts{}, false
	}
	switch query.Get("scope") {
	case "all_code":
		if query.Has("repository") || query.Has("service_key") {
			return readaccounting.Counts{}, false
		}
		return readaccounting.Counts{ControlFileReads: 2, StoreReadAttempts: 2}, true
	case "service":
		if query.Get("repository") == "" || query.Get("service_key") == "" {
			return readaccounting.Counts{}, false
		}
		return readaccounting.Counts{
			ControlFileReads: 16, StoreReadAttempts: 15,
			MemberVisits: t421ExactCatalogMemberLimit(),
		}, true
	default:
		return readaccounting.Counts{}, false
	}
}

func t421ExactServiceReadLimits(rawQuery string) (readaccounting.Counts, bool) {
	query, ok := t421ExactProductQuery(rawQuery, []string{"repository", "service_key"})
	if !ok || query.Get("repository") == "" || query.Get("service_key") == "" {
		return readaccounting.Counts{}, false
	}
	return readaccounting.Counts{
		StoreReadAttempts: 11, MemberVisits: t421ExactCatalogMemberLimit(),
	}, true
}

func t421ExactRelationshipReadLimits(rawQuery string) (readaccounting.Counts, bool) {
	query, ok := t421ExactProductQuery(rawQuery, []string{
		"repository", "service_key", "view", "kind", "plane", "lookup_key", "page_size", "cursor",
	})
	if !ok || query.Get("repository") == "" || query.Get("service_key") == "" ||
		query.Get("view") == "" || query.Get("plane") == "" || query.Get("lookup_key") == "" ||
		query.Get("page_size") != "1" || query.Has("cursor") && query.Get("cursor") == "" {
		return readaccounting.Counts{}, false
	}
	return t421ExactRelationshipLimits(query.Get("kind"), query.Has("cursor"))
}

func t421ExactProductQuery(raw string, allowed []string) (url.Values, bool) {
	if raw == "" {
		return nil, false
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return nil, false
	}
	allowedKeys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = struct{}{}
	}
	for key, values := range query {
		if _, present := allowedKeys[key]; !present || len(values) != 1 {
			return nil, false
		}
	}
	return query, true
}

func t421ExactMCPReadLimits(request *http.Request) (readaccounting.Counts, bool) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.URL.ForceQuery ||
		request.Body == nil {
		return readaccounting.Counts{}, false
	}
	body := request.Body
	raw, readErr := io.ReadAll(io.LimitReader(body, t421ExactMCPRequestBytes+1))
	closeErr := body.Close()
	request.Body = io.NopCloser(bytes.NewReader(raw))
	request.ContentLength = int64(len(raw))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(raw)), nil
	}
	if readErr != nil || closeErr != nil || len(raw) > t421ExactMCPRequestBytes {
		return readaccounting.Counts{}, false
	}
	call, ok := t421ExactJSONObject(raw, "jsonrpc", "id", "method", "params")
	if !ok {
		return readaccounting.Counts{}, false
	}
	var jsonrpc, method string
	if !t421ExactJSONDecode(call["jsonrpc"], &jsonrpc) || jsonrpc != "2.0" ||
		!t421ExactJSONRPCID(call["id"]) ||
		!t421ExactJSONDecode(call["method"], &method) || method != "tools/call" {
		return readaccounting.Counts{}, false
	}
	params, ok := t421ExactJSONObject(call["params"], "name", "arguments")
	var name string
	if !ok || !t421ExactJSONDecode(params["name"], &name) {
		return readaccounting.Counts{}, false
	}
	return t421ExactMCPToolLimits(name, params["arguments"])
}

func t421ExactMCPToolLimits(name string, arguments json.RawMessage) (readaccounting.Counts, bool) {
	switch name {
	case "search_code":
		var input struct {
			Query        string `json:"query"`
			MaxMatches   *int   `json:"max_matches"`
			ContextLines *int   `json:"context_lines"`
			Scope        string `json:"scope"`
			Repository   string `json:"repository"`
			ServiceKey   string `json:"service_key"`
		}
		if !t421ExactMCPArguments(arguments, &input,
			"query", "max_matches", "context_lines", "scope", "repository", "service_key",
		) || input.Query == "" ||
			input.MaxMatches == nil || *input.MaxMatches != 1 ||
			input.ContextLines == nil || *input.ContextLines != 0 {
			return readaccounting.Counts{}, false
		}
		switch input.Scope {
		case "all_code":
			if input.Repository != "" || input.ServiceKey != "" {
				return readaccounting.Counts{}, false
			}
			return readaccounting.Counts{ControlFileReads: 2, StoreReadAttempts: 2}, true
		case "service":
			if input.Repository == "" || input.ServiceKey == "" {
				return readaccounting.Counts{}, false
			}
			return readaccounting.Counts{
				ControlFileReads: 16, StoreReadAttempts: 15,
				MemberVisits: t421ExactCatalogMemberLimit(),
			}, true
		default:
			return readaccounting.Counts{}, false
		}
	case "get_service":
		var input struct {
			Repository string `json:"repository"`
			ServiceKey string `json:"service_key"`
		}
		if !t421ExactMCPArguments(arguments, &input, "repository", "service_key") ||
			input.Repository == "" || input.ServiceKey == "" {
			return readaccounting.Counts{}, false
		}
		return readaccounting.Counts{
			StoreReadAttempts: 11, MemberVisits: t421ExactCatalogMemberLimit(),
		}, true
	case "list_service_relationships":
		var input struct {
			Repositories []string `json:"repositories"`
			ServiceKey   string   `json:"service_key"`
			View         string   `json:"view"`
			Kind         string   `json:"kind"`
			Plane        string   `json:"plane"`
			LookupKey    string   `json:"lookup_key"`
			PageSize     *int     `json:"page_size"`
			Cursor       string   `json:"cursor"`
		}
		if !t421ExactMCPArguments(arguments, &input,
			"repositories", "service_key", "view", "kind", "plane", "lookup_key", "page_size", "cursor",
		) || len(input.Repositories) != 1 ||
			input.Repositories[0] == "" || input.ServiceKey == "" || input.View == "" ||
			input.Plane == "" || input.LookupKey == "" || input.PageSize == nil || *input.PageSize != 1 {
			return readaccounting.Counts{}, false
		}
		return t421ExactRelationshipLimits(input.Kind, input.Cursor != "")
	default:
		return readaccounting.Counts{}, false
	}
}

func t421ExactMCPArguments(raw json.RawMessage, destination any, allowed ...string) bool {
	if _, ok := t421ExactJSONObject(raw, allowed...); !ok {
		return false
	}
	return t421ExactJSONDecode(raw, destination)
}

func t421ExactJSONObject(raw []byte, allowed ...string) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if !t421ExactJSONDecode(raw, &object) || object == nil {
		return nil, false
	}
	for key := range object {
		if !slices.Contains(allowed, key) {
			return nil, false
		}
	}
	return object, true
}

func t421ExactJSONRPCID(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var id any
	if decoder.Decode(&id) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	switch id.(type) {
	case string, json.Number:
		return true
	default:
		return false
	}
}

func t421ExactJSONDecode(raw []byte, destination any) bool {
	if len(raw) == 0 || destination == nil || !t421ExactJSONKeysUnique(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination) == nil && decoder.Decode(&struct{}{}) == io.EOF
}

func t421ExactJSONKeysUnique(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	return t421ExactJSONValueKeysUnique(decoder, 0) && decoder.Decode(&struct{}{}) == io.EOF
}

func t421ExactJSONValueKeysUnique(decoder *json.Decoder, depth int) bool {
	if depth > t421ExactJSONMaxDepth {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			key, isString := keyToken.(string)
			if keyErr != nil || !isString {
				return false
			}
			if _, duplicate := seen[key]; duplicate {
				return false
			}
			seen[key] = struct{}{}
			if !t421ExactJSONValueKeysUnique(decoder, depth+1) {
				return false
			}
		}
		closing, closeErr := decoder.Token()
		return closeErr == nil && closing == json.Delim('}')
	case '[':
		for decoder.More() {
			if !t421ExactJSONValueKeysUnique(decoder, depth+1) {
				return false
			}
		}
		closing, closeErr := decoder.Token()
		return closeErr == nil && closing == json.Delim(']')
	default:
		return false
	}
}

func t421ExactCatalogMemberLimit() uint64 {
	return uint64(servicecatalogv3.MaxServicesPerMember + servicecatalogv3.MaxMemberships)
}

func t421ExactRelationshipLimits(kind string, continuation bool) (readaccounting.Counts, bool) {
	memberVisits := uint64(relationshippublication.MaxProjectionRecords * relationshippublication.MaxProjectionBucketsV3)
	switch kind {
	case "rpc":
		memberVisits += uint64(rpccallerposting.MaxPostingsPerMember)
	case "kafka":
		memberVisits += uint64(kafkatopicposting.MaxPostingsPerMember)
	default:
		return readaccounting.Counts{}, false
	}
	if continuation {
		return readaccounting.Counts{
			ControlFileReads: 2, StoreReadAttempts: 3, MemberVisits: memberVisits,
		}, true
	}
	return readaccounting.Counts{
		ControlFileReads: 5, StoreReadAttempts: 4,
		MemberVisits: memberVisits + relationshippublication.MaxServicesPerServiceMemberV3,
	}, true
}

func t421ExactFinalAuthorityReadAvailable(read t421ExactFinalAuthorityRead) bool {
	return read.Read != nil && read.Limits.ControlFileReads != math.MaxUint64 &&
		read.Limits.StoreReadAttempts != math.MaxUint64 &&
		read.Limits.MemberVisits != math.MaxUint64 &&
		read.Limits.StoreWriteAttempts != math.MaxUint64
}

func t421ExactReadLegacyPrincipal(principal auth.Principal) bool {
	return principal.AuthMethod == "api_key" && principal.User == nil &&
		principal.APIKeyID != "" && principal.IsAdmin
}

func t421ExtractionProgressReadLimits() readaccounting.Counts {
	return readaccounting.Counts{
		ControlFileReads:  2 + extractionpublication.MaxDomains,
		StoreReadAttempts: 2 + 2*store.MaxGenerationScheduleReadAttempts,
	}
}

func (state *t421ExactReadAccountingState) refuse(
	writer http.ResponseWriter,
	ordinal uint64,
	status string,
	reason error,
) {
	firstFailure := state.markFailed()
	if !firstFailure {
		http.Error(writer, "exact read request refused", http.StatusConflict)
		return
	}
	writer.Header().Add("Trailer", t421ExactReadTrailer)
	encoded, reportErr := state.encode(t421ExactReadReport{
		Schema:         t421ExactReadReportSchema,
		RequestOrdinal: ordinal,
		Status:         status,
	})
	http.Error(writer, "exact read request refused", http.StatusConflict)
	if reportErr == nil {
		writer.Header().Set(t421ExactReadTrailer, encoded.trailer)
		reportErr = state.send(encoded.canonical)
	}
	if reportErr != nil {
		reason = errors.Join(reason, errT421ExactReadReport)
	}
	if firstFailure {
		state.fail(reason)
	}
}

type t421EncodedExactReadReport struct {
	canonical []byte
	trailer   string
}

func (*t421ExactReadAccountingState) encode(
	report t421ExactReadReport,
) (t421EncodedExactReadReport, error) {
	canonical, err := json.Marshal(report)
	if err != nil {
		return t421EncodedExactReadReport{}, err
	}
	return t421EncodedExactReadReport{
		canonical: canonical,
		trailer:   base64.RawURLEncoding.EncodeToString(canonical),
	}, nil
}

func (state *t421ExactReadAccountingState) emit(
	writer http.ResponseWriter,
	report t421ExactReadReport,
) error {
	encoded, err := state.encode(report)
	if err != nil {
		return err
	}
	writer.Header().Set(t421ExactReadTrailer, encoded.trailer)
	return state.send(encoded.canonical)
}

func (state *t421ExactReadAccountingState) send(report []byte) (err error) {
	defer func() {
		if recover() != nil {
			err = errT421ExactReadReport
		}
	}()
	return state.report(report)
}

func (*t421ExactReadAccountingState) commit(commit func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errT421ExactReadCommit
		}
	}()
	return commit()
}

func (state *t421ExactReadAccountingState) release(terminalErr error) {
	state.mu.Lock()
	state.active = false
	firstFailure := terminalErr != nil && !state.failed
	if terminalErr != nil {
		state.failed = true
	}
	state.mu.Unlock()
	if firstFailure {
		state.fail(terminalErr)
	}
}

func (state *t421ExactReadAccountingState) markFailed() bool {
	state.mu.Lock()
	first := !state.failed
	state.failed = true
	state.mu.Unlock()
	return first
}

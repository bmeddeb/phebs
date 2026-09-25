package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// SearchWarmSchema opts one V5 restored (epoch-five) launch into the native
// phase-14 search warm before the query corridor.
const SearchWarmSchema = "t422-search-warm-v1"

// searchWarmMaximum is the product's own cache-owned warming bound; the native
// command never waits longer, so a longer reported wait is not a valid answer.
const searchWarmMaximum = 10 * time.Minute

// epochSearchWarmOptIn is the only place a launch opts in: V5 restored epoch five.
func epochSearchWarmOptIn(epoch uint64, schema string) string {
	if epoch == 5 && schema == PlanV5Schema {
		return SearchWarmSchema
	}
	return ""
}

// The native command's closed, source-free answer. The corridor's queries and
// their exact accounting are unchanged; this is parent control, like park.
type epochSearchWarmObservation struct {
	Schema                         string `json:"schema"`
	Phase                          uint32 `json:"phase"`
	SharedValidated                bool   `json:"shared_validated"`
	SharedExactSHA256              string `json:"shared_exact_sha256,omitempty"`
	SelectedSearchGenerationSHA256 string `json:"selected_search_generation_sha256"`
	ElapsedMS                      uint64 `json:"elapsed_ms"`
}

// validEpochSearchWarm binds the warmed selected generation to phase-14 F:
// both read the same runtime selector's search-generation controls.
func validEpochSearchWarm(value epochSearchWarmObservation, final AuthorityPhaseResult) bool {
	return value.Schema == SearchWarmSchema && value.Phase == 14 &&
		validDigest(value.SelectedSearchGenerationSHA256) &&
		value.SelectedSearchGenerationSHA256 == final.SearchGenerationSHA256 &&
		value.SharedValidated == (value.SharedExactSHA256 == "") &&
		(value.SharedExactSHA256 == "" || validDigest(value.SharedExactSHA256)) &&
		value.ElapsedMS <= uint64(searchWarmMaximum/time.Millisecond)
}

func decodeEpochSearchWarm(raw []byte) (epochSearchWarmObservation, bool) {
	var value epochSearchWarmObservation
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return value, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.More() {
		return value, false
	}
	canonical, err := json.Marshal(value)
	return value, err == nil && bytes.Equal(append(canonical, '\n'), raw)
}

// searchWarm runs once, after phase-14 F and before the first corridor query.
// Any refusal or mismatch is terminal; there is no retry.
func (reader *executionEpochInspection) searchWarm(ctx context.Context, final AuthorityPhaseResult) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || reader.run.control == nil ||
		reader.run.epoch.Epoch != 5 || reader.run.epoch.SearchWarm != SearchWarmSchema || reader.plan.Schema != PlanV5Schema ||
		reader.projection.Phase != "product_queries" || !reader.finalUsed || reader.searchWarmed ||
		len(reader.productQueries) != 0 {
		return errEpochInspection
	}
	token := reader.run.control.RequestToken()
	if token == "" {
		return errEpochInspection
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+reader.run.epoch.Listen+"/api/t422/search/warm", nil)
	if err != nil {
		return errEpochInspection
	}
	request.Header.Set("Authorization", "Bearer "+reader.run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	reader.failureStatus, reader.failureBody, reader.failureOrdinal = response.StatusCode, raw, 0
	closeErr := response.Body.Close()
	value, decoded := decodeEpochSearchWarm(raw)
	if readErr != nil || closeErr != nil || ctx.Err() != nil || response.StatusCode != http.StatusOK || len(raw) > 4096 ||
		response.Uncompressed || response.Header.Get("Content-Encoding") != "" || len(response.Trailer) != 0 ||
		len(response.Header.Values(epochReadTrailer)) != 0 || !decoded || !validEpochSearchWarm(value, final) {
		return errEpochInspection
	}
	reader.searchWarmObservation, reader.searchWarmed = value, true
	return nil
}

package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422AttemptReportSchema = "t422-phase-attempt-report-v1"
const t422AttemptReportPrefix = "exact attempt: "

// This envelope preserves the complete native report and adds only the actual
// authenticated phase at emission. Its producer still holds the ordinary-owner
// turn, so a successful phase drain cannot pass between this lookup and log I/O.
type t422AttemptReport struct {
	Schema      string          `json:"schema"`
	Producer    uint32          `json:"producer"`
	Phase       uint32          `json:"phase"`
	InputSHA256 string          `json:"input_sha256"`
	Kind        string          `json:"kind"`
	Report      json.RawMessage `json:"report"`
}

var errT422AttemptReport = errors.New("T42.2 phase attempt report unavailable")

func t422AttemptReportSink(kind string) func([]byte) error {
	sink := t4013ExactReportSink(t422AttemptReportPrefix)
	return func(raw []byte) error {
		state, err := dispatchadmission.ProductionSemanticState()
		if err != nil {
			return errT422AttemptReport
		}
		encoded, err := t422EncodeAttemptReport(state, kind, raw)
		if err != nil {
			return err
		}
		return sink(encoded)
	}
}

func t422EncodeAttemptReport(state dispatchadmission.ProductionSemanticSnapshot, kind string, raw []byte) ([]byte, error) {
	limit := store.MaxJobLifecycleReportSize
	if kind == "chunk" {
		limit = generationscheduler.MaxChunkLifecycleReportSize
	} else if kind != "job" {
		return nil, errT422AttemptReport
	}
	if state.Mode != dispatchadmission.ProductionSemanticV3 || state.ProducerID < 2 || state.ProducerID > 6 ||
		state.InputSHA256 == ([32]byte{}) || !t422SemanticEpochPhase(uint64(state.ProducerID-1), state.Phase, false) ||
		len(raw) == 0 || len(raw) > limit {
		return nil, errT422AttemptReport
	}
	encoded, err := json.Marshal(t422AttemptReport{Schema: t422AttemptReportSchema, Producer: state.ProducerID,
		Phase: state.Phase, InputSHA256: "sha256:" + hex.EncodeToString(state.InputSHA256[:]), Kind: kind, Report: raw})
	if err != nil || len(encoded) > limit+1024 {
		return nil, errT422AttemptReport
	}
	return encoded, nil
}

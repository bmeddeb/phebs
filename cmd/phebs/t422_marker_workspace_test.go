package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Supplied values prove only the report state machine, not native measurement.
func TestT422MarkerWorkspaceReportOrder(t *testing.T) {
	for _, mode := range []string{"valid", "early_ready", "early_finish", "duplicate", "wrong_phase", "sink_loss"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			reports, state := workspaceReportFixture(4, &output)
			state.Phase = 6
			reports.initial = state
			if mode == "early_ready" {
				if reports.markerReopenReady() == nil {
					t.Fatal("ready without sample")
				}
				return
			}
			if reports.begin(state) != nil || reports.complete(custodybytes.Sample{LogicalBytes: 55, AllocatedBytes: 44}) != nil {
				t.Fatal("sample")
			}
			if mode == "early_finish" {
				if reports.begin(state) == nil {
					t.Fatal("finish admitted before readiness")
				}
				return
			}
			if mode == "wrong_phase" {
				reports.phase = 7
			}
			if mode == "sink_loss" {
				reports.writer = workspaceReportBadWriter{failed: true}
			}
			err := reports.markerReopenReady()
			if mode == "wrong_phase" || mode == "sink_loss" {
				if err == nil || output.Len() != 86 {
					t.Fatal("lost positive sample or false readiness")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "duplicate" {
				if reports.markerReopenReady() == nil {
					t.Fatal("repeated readiness")
				}
				return
			}
			if reports.begin(state) != nil || reports.complete(custodybytes.Sample{LogicalBytes: 3, AllocatedBytes: 4}) != nil || output.Len() != 2*86+26 {
				t.Fatal("exact marker/finish stream", output.String())
			}
		})
	}
}

// Authenticated-envelope decoding uses supplied identity here. The separate
// absent-FD case proves that decoding alone cannot authorize measurement.
func TestT422MarkerWorkspaceDeadlineBinding(t *testing.T) {
	for _, mode := range []string{"valid", "omitted", "negative", "wrong_epoch", "wrong_producer", "wrong_phase", "changed_digest"} {
		t.Run(mode, func(t *testing.T) {
			request := t422SemanticLaunchRequest{Schema: t422SemanticLaunchSchema, Recipe: t422SemanticLaunchRecipe,
				PlanSHA256: "sha256:" + strings.Repeat("1", 64), ConfigSHA256: "sha256:" + strings.Repeat("2", 64),
				ServerEpoch: 3, Repository: "repo", ReturnSourceCommit: strings.Repeat("a", 40), MarkerDeadlineUnixNano: time.Now().Add(time.Minute).UnixNano()}
			snapshot := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 4, Phase: 6}
			switch mode {
			case "omitted":
				request.MarkerDeadlineUnixNano = 0
			case "negative":
				request.MarkerDeadlineUnixNano = -1
			case "wrong_epoch":
				request.ServerEpoch, request.ReturnSourceCommit, snapshot.ProducerID, snapshot.Phase = 2, "", 3, 5
			case "wrong_producer":
				snapshot.ProducerID = 3
			case "wrong_phase":
				snapshot.Phase = 7
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			snapshot.InputSHA256 = sha256.Sum256(raw)
			if mode == "changed_digest" {
				snapshot.InputSHA256[0] ^= 1
			}
			decoded, err := decodeT422SemanticLaunch(raw, snapshot)
			if (err == nil) != (mode == "valid" || mode == "omitted") {
				t.Fatal(mode, err)
			}
			if mode == "omitted" && bytes.Contains(raw, []byte("marker_deadline")) {
				t.Fatal("legacy bytes changed")
			}
			if mode == "valid" && decoded.request.MarkerDeadlineUnixNano != request.MarkerDeadlineUnixNano {
				t.Fatal("deadline changed")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failed := false
	control := &t422LifecycleControl{ctx: ctx, launch: &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: 3, MarkerDeadlineUnixNano: time.Now().Add(time.Minute).UnixNano()}, fail: func(error) { failed = true }}}
	if control.bindWorkspaceBytes(nil) == nil || !failed || control.markerWorkspace != nil {
		t.Fatal("deadline accepted without actual borrowed workspace")
	}
}

package main

import (
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

func TestCallerComparisonReleasedAdmission(t *testing.T) {
	if releasedClaims[callerComparisonPackID] != "compare_operation_callers@"+api.CallerComparisonSchemaVersion {
		t.Fatal("comparison claim implementation identity changed")
	}
	for _, test := range []struct {
		name                                     string
		withoutClaim, withoutCaller              bool
		revokeClaim, revokeCaller                bool
		shape                                    func(*packrelease.PackRelease)
		experimental                             bool
		wantCaller, wantComparison, wantReleased bool
	}{
		{name: "joint admission", wantCaller: true, wantComparison: true, wantReleased: true},
		{name: "Caller Map alone", withoutClaim: true, wantCaller: true},
		{name: "claim alone", withoutCaller: true},
		{name: "comparison revoked", revokeClaim: true, wantCaller: true},
		{name: "caller revoked", revokeCaller: true},
		{name: "comparison expired", shape: expiredValidation, wantCaller: true},
		{name: "comparison suspended", shape: func(release *packrelease.PackRelease) { release.DerivedStatus = packrelease.StatusSuspended }, wantCaller: true},
		{name: "experimental caller is no released dependency", withoutCaller: true, experimental: true, wantCaller: true, wantComparison: true},
		{name: "released claim governs mixed admission", experimental: true, wantCaller: true, wantComparison: true, wantReleased: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := newReleasedSelection(t)
			var records []*packrelease.PackRelease
			if !test.withoutClaim {
				shape := test.shape
				if shape == nil {
					shape = func(*packrelease.PackRelease) {}
				}
				records = append(records, selection.admitShaped(t, callerComparisonPackID, "comparison-key", shape))
			}
			if !test.withoutCaller {
				shape := func(*packrelease.PackRelease) {}
				if len(records) != 0 {
					shape = sharingBinding(records[0])
				}
				records = append(records, selection.admitShaped(t, callerMapPackID, "caller-key", shape))
			}
			bindReleaseLoad(t, records[0], records[1:]...)
			cfg := &config.Config{}
			cfg.Server.DataDir = t.TempDir()
			cfg.Experimental.ProvisionalProtoExtraction = test.experimental
			selection.bind(cfg)
			for _, record := range records {
				if test.revokeClaim && record.PackID == callerComparisonPackID || test.revokeCaller && record.PackID == callerMapPackID {
					cfg.ReleaseSelection.Revoked = append(cfg.ReleaseSelection.Revoked, record.ReleaseID)
				}
			}
			got := serveCallerSurfaces(t, cfg)
			if got.callerMapEnabled != test.wantCaller || got.comparisonTool != test.wantComparison || got.comparisonReleased != test.wantReleased || slices.Contains(got.capabilities, "contract-caller-comparison") != test.wantComparison {
				t.Fatalf("comparison admission = %+v", got)
			}
			if !test.experimental && (got.proofTool || got.topicTool || got.wireTool || slices.Contains(got.capabilities, "contract-impact-report")) {
				t.Fatalf("comparison admitted unrelated products: %+v", got)
			}
		})
	}
}

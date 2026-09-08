package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Real HTTP and genuine PC token generation; fixtures supply response counters,
// not native deletion evidence. The store tests own that separate proof.
func TestEpochSelectorCleanupHTTP(t *testing.T) {
	for _, mode := range []string{"complete", "omitted-policy", "no-final", "wrong-input", "wrong-selector", "wrong-phase", "incomplete", "failed", "over-limit", "unknown-field", "truncated", "redirect", "read-trailer", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			input := sha256.Sum256([]byte("private-input"))
			value := epochSelectorCleanupObservation{Schema: SelectorHandoffCleanupSchema, Phase: 2,
				InputSHA256: "sha256:" + hex.EncodeToString(input[:]), SelectedRuntimeSHA256: testDigest("selected"),
				Turns: 1, StoreReadAttempts: 3, Done: true}
			switch mode {
			case "wrong-input":
				value.InputSHA256 = testDigest("other-input")
			case "wrong-selector":
				value.SelectedRuntimeSHA256 = testDigest("other-selector")
			case "wrong-phase":
				value.Phase = 4
			case "incomplete":
				value.Done = false
			case "failed":
				value.Failed = true
			case "over-limit":
				value.Turns++
			}
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.RequestURI() != "/api/t422/selector-handoff/cleanup" ||
					r.Header.Get("Authorization") != "Bearer private-key" || r.Header.Get("X-Phebs-T421-Exact-Reads") != "" || r.ContentLength != 0 {
					t.Error("cleanup command shape changed")
				}
				if mode == "redirect" {
					w.Header().Set("Location", "/wrong")
					w.WriteHeader(http.StatusFound)
					return
				}
				if mode == "read-trailer" {
					w.Header().Set(epochReadTrailer, "not-an-exact-read")
				}
				raw, _ := json.Marshal(value)
				if mode == "unknown-field" {
					raw = append([]byte(`{"extra":1,`), raw[1:]...)
				}
				if mode == "truncated" {
					raw = raw[:len(raw)-1]
				}
				_, _ = w.Write(append(raw, '\n'))
			}))
			reader.plan = selectorCleanupTestPlan(t)
			reader.projection.Phase, reader.finalUsed = "cold", mode != "no-final"
			reader.tail = epochTailReadiness{Status: "ready", SelectedRuntimeSHA256: testDigest("selected")}
			reader.run.epoch.Epoch, reader.run.attemptInput = 1, input
			if mode == "omitted-policy" {
				reader.plan.SelectorHandoffCleanup = nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			err := reader.cleanupSelectorHandoff(ctx)
			want := mode == "complete" || mode == "omitted-policy"
			if (err == nil) != want {
				t.Fatalf("mode %s: %v", mode, err)
			}
			noCall := mode == "omitted-policy" || mode == "no-final" || mode == "canceled"
			if (calls.Load() == 0) != noCall {
				t.Fatal("wrong native command count", calls.Load())
			}
			if mode == "complete" && reader.selectorCleanup != value {
				t.Fatal("native counters not retained")
			}
			if mode != "omitted-policy" {
				before := calls.Load()
				if reader.cleanupSelectorHandoff(t.Context()) == nil || calls.Load() != before {
					t.Fatal("command retried")
				}
			}
		})
	}
}

func TestEpochSelectorCleanupInputBinding(t *testing.T) {
	epoch := ExecutionEpochConfig{Epoch: 1, ConfigSHA256: testDigest("config"), Repository: "example.com/mono"}
	old, err := epochSemanticInput(testDigest("plan"), epoch, nil)
	if err != nil || strings.Contains(string(old), "selector_handoff_cleanup") {
		t.Fatal("old input changed", err)
	}
	epoch.SelectorHandoffCleanup = SelectorHandoffCleanupSchema
	raw, err := epochSemanticInput(testDigest("plan"), epoch, nil)
	if err != nil || !strings.Contains(string(raw), `"selector_handoff_cleanup":"`+SelectorHandoffCleanupSchema+`"`) || sha256.Sum256(raw) == sha256.Sum256(old) {
		t.Fatal("cleanup input not independently bound", err)
	}
	epoch.SelectorHandoffCleanup = "unknown"
	if _, err := epochSemanticInput(testDigest("plan"), epoch, nil); err == nil {
		t.Fatal("unknown cleanup policy admitted")
	}
}

func TestEpochSelectorCleanupNativeComposition(t *testing.T) {
	plan := selectorCleanupTestPlan(t)
	bound, ok := SelectorHandoffCleanupForPhase(plan, "physical_delta_b")
	if !ok {
		t.Fatal("missing handoff")
	}
	reader := executionEpochInspection{plan: plan, tail: epochTailReadiness{SelectedRuntimeSHA256: testDigest("selected")}}
	for _, counts := range []struct{ deleted, turns, reads, writes, maximum uint64 }{
		{0, 1, 3, 0, 0}, {1, 1, 9, 1, 1}, {2, 1, 9, 2, 2},
		{16, 1, 9, 2, 16}, {17, 2, 16, 2, 16}, {18, 2, 16, 3, 16},
		{10001, 626, 4384, 626, 16},
	} {
		value := epochSelectorCleanupObservation{Schema: SelectorHandoffCleanupSchema, Phase: 4,
			InputSHA256: testDigest("input"), SelectedRuntimeSHA256: testDigest("selected"),
			Turns: counts.turns, Deleted: counts.deleted, MaxDeleted: counts.maximum,
			StoreReadAttempts: counts.reads, StoreWriteAttempts: counts.writes, Done: true}
		if err := reader.validateSelectorCleanup(value, bound, testDigest("input")); err != nil {
			t.Fatal("native composition refused", counts, err)
		}
		for _, mutate := range []func(*epochSelectorCleanupObservation){
			func(v *epochSelectorCleanupObservation) { v.Turns++ },
			func(v *epochSelectorCleanupObservation) { v.StoreReadAttempts++ },
			func(v *epochSelectorCleanupObservation) { v.StoreWriteAttempts++ },
			func(v *epochSelectorCleanupObservation) { v.MaxDeleted++ },
		} {
			bad := value
			mutate(&bad)
			if reader.validateSelectorCleanup(bad, bound, testDigest("input")) == nil {
				t.Fatal("impossible turn composition admitted", bad)
			}
		}
	}
}
